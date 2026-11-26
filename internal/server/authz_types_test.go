package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// authzInlineSave sends Quick Edit for a post as the client.
func authzInlineSave(t *testing.T, srv *Server, c *http.Client, base string, id int64, title, status string) string {
	t.Helper()
	_, body, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"inline-save"}, "_inline_edit": {actionNonce(t, srv, c, base, "inlineeditnonce")},
		"post_ID": {itoa64(id)}, "post_title": {title}, "post_name": {"slug-" + itoa64(id)}, "_status": {status}})
	return body
}

// authzUpload sends a multipart upload to /wp/v2/media with extra form fields.
func authzUpload(t *testing.T, c *http.Client, base, name string, data []byte, fields map[string]string) (int, string) {
	t.Helper()
	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, base+"/wp-json/wp/v2/media", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-WP-Nonce", nonce)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// authzSigned reports whether a response hands out a signed link to a private file.
var authzSignedRe = regexp.MustCompile(`/wp-content/uploads/private/[^"\s]*\?e=\d+&s=[0-9a-f]{32}`)

func authzSigned(body string) bool {
	body = strings.NewReplacer(`\/`, "/", "\\"+"u0026", "&", "&amp;", "&").Replace(body)
	return authzSignedRe.MatchString(body)
}

// O5: a product's meta capabilities are the product ones while Commerce is on. An editor (who may
// edit everyone's posts) can no longer change a product through Quick Edit, the Custom Fields box,
// the Heartbeat autosave or read its revisions through /wp/v2/posts; a shop manager and the
// administrator still edit it.
func TestAuthzProductMetaCaps(t *testing.T) {
	base, admin, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Fountain pen", 25, nil)
	editor := hgSecondUser(t, srv, base, "eddie", "editor")
	manager := hgSecondUser(t, srv, base, "shopper", "shop_manager")
	if !gooCapsOn() {
		t.Fatal("product capabilities are off with Commerce on")
	}
	load := func() *store.Post {
		t.Helper()
		got, err := srv.store.GetPost(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	if body := authzInlineSave(t, srv, editor, base, p.ID, "Hacked", "draft"); !strings.Contains(body, "not allowed") {
		t.Errorf("editor inline-save answer: %.200s", body)
	}
	if got := load(); got.Status != "publish" || got.Title != "Fountain pen" {
		t.Fatalf("an editor changed a product through Quick Edit: %q %s", got.Title, got.Status)
	}
	// The Custom Fields box and the heartbeat autosave take any post id too.
	_, body, _ := post(t, editor, base+"/wp-admin/admin-ajax", url.Values{"action": {"add-meta"}, "_ajax_nonce-add-meta": {actionNonce(t, srv, editor, base, "add-meta")},
		"post_id": {itoa64(p.ID)}, "metakeyinput": {"injected"}, "metavalue": {"1"}})
	if body != "-1" || load().Meta["injected"] != nil {
		t.Errorf("editor add-meta on a product: %q", body)
	}
	hb := ceHeartbeat(t, editor, base, url.Values{"action": {"heartbeat"}, "data[wp_autosave][post_id]": {itoa64(p.ID)}, "data[wp_autosave][post_title]": {"Autosaved"},
		"data[wp_autosave][content]": {"x"}, "data[wp_autosave][_wpnonce]": {actionNonce(t, srv, editor, base, "update-post_"+itoa64(p.ID))}})
	if as, _ := hb["wp_autosave"].(map[string]any); as == nil || as["success"] != false {
		t.Errorf("editor heartbeat autosave on a product: %v", hb["wp_autosave"])
	}
	// The posts route is for posts: a product's revisions and autosaves are not under it.
	for _, path := range []string{"/revisions", "/autosaves"} {
		if code, out := restGet(t, editor, base, "/wp-json/wp/v2/posts/"+itoa64(p.ID)+path); code != http.StatusNotFound {
			t.Errorf("editor GET posts/<product>%s: %d %.200s", path, code, out)
		}
	}
	if code, out := restSend(t, admin, base, http.MethodPost, "/wp-json/wp/v2/posts/"+itoa64(p.ID)+"/autosaves", `{"title":"x"}`); code != http.StatusNotFound {
		t.Errorf("POST posts/<product>/autosaves: %d %.200s", code, out)
	}

	// A shop manager and the administrator keep editing products.
	authzInlineSave(t, srv, manager, base, p.ID, "Fountain pen II", "draft")
	if got := load(); got.Status != "draft" || got.Title != "Fountain pen II" {
		t.Errorf("shop manager inline-save: %q %s", got.Title, got.Status)
	}
	authzInlineSave(t, srv, admin, base, p.ID, "Fountain pen III", "publish")
	if got := load(); got.Status != "publish" || got.Title != "Fountain pen III" {
		t.Errorf("administrator inline-save: %q %s", got.Title, got.Status)
	}

	// Ordinary posts are unchanged for the editor.
	own := mustInsertPost(t, srv, &store.Post{Type: "post", Title: "Admin post", Slug: "admin-post", AuthorID: 1})
	authzInlineSave(t, srv, editor, base, own.ID, "Edited by the editor", "publish")
	if got, _ := srv.store.GetPost(ctx, own.ID); got.Title != "Edited by the editor" {
		t.Errorf("editor lost Quick Edit on posts: %q", got.Title)
	}
}

// O5: ACF-Go's configuration types and contact forms need manage_options for every post meta
// capability, so an editor (even as the stored author) cannot switch a field group off or rename
// a contact form through the generic handlers; the administrator can.
func TestAuthzConfigTypesNeedManageOptions(t *testing.T) {
	ts, admin, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, admin)
	base := ts.URL
	ctx := context.Background()
	editor := hgSecondUser(t, srv, base, "eddie", "editor")
	ed, err := srv.store.UserByLoginOrEmail(ctx, "eddie")
	if err != nil {
		t.Fatal(err)
	}
	adm, _ := srv.store.UserByID(ctx, 1)
	for _, typ := range []string{acfgoGroupType, acfgoPostType, acfgoTaxType, acfgoOptionsType, gontactPostType} {
		// The plugins register their types on activation; the test only needs the rows.
		if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO content_types (slug, name, public) VALUES ($1, $1, false) ON CONFLICT (slug) DO NOTHING`, typ); err != nil {
			t.Fatal(err)
		}
		p := mustInsertPost(t, srv, &store.Post{Type: typ, Title: "Config " + typ, Slug: "config-" + typ, AuthorID: ed.ID, Content: "{}"})
		for _, cp := range []string{"edit_post", "delete_post", "publish_post", "read_post"} {
			if ed.Can(cp, p) {
				t.Errorf("editor (the author) has %s on %s", cp, typ)
			}
			if !adm.Can(cp, p) {
				t.Errorf("administrator lacks %s on %s", cp, typ)
			}
		}
		authzInlineSave(t, srv, editor, base, p.ID, "Hijacked", "draft")
		if got, _ := srv.store.GetPost(ctx, p.ID); got.Status != "publish" || got.Title != "Config "+typ {
			t.Errorf("editor changed a %s through Quick Edit: %q %s", typ, got.Title, got.Status)
		}
		authzInlineSave(t, srv, admin, base, p.ID, "Renamed", "draft")
		if got, _ := srv.store.GetPost(ctx, p.ID); got.Status != "draft" || got.Title != "Renamed" {
			t.Errorf("administrator could not Quick Edit a %s: %q %s", typ, got.Title, got.Status)
		}
	}
	// A post of a type nobody registered capabilities for is left to its own screens.
	if genericEditType("elementor_library") || !genericEditType("post") || !genericEditType(acfgoGroupType) {
		t.Error("genericEditType")
	}
}

// O4: a signed link to a private file goes only to someone who may edit the attachment, and for a
// Commerce downloadable file only to someone who also manages products.
func TestAuthzPrivateMediaSigning(t *testing.T) {
	base, admin, srv := gooSite(t)
	ctx := context.Background()
	author := hgSecondUser(t, srv, base, "andy", "author")
	editor := hgSecondUser(t, srv, base, "eddie", "editor")
	manager := hgSecondUser(t, srv, base, "shopper", "shop_manager")
	img := gocdnTestImage(400, 300, "jpeg")
	makePrivate := func(id int64) *store.Attachment {
		t.Helper()
		a, err := srv.store.GetAttachment(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.mediaSetPrivate(ctx, a, true); err != nil {
			t.Fatal(err)
		}
		return a
	}
	getAttachment := func(c *http.Client, id int64) string {
		t.Helper()
		_, body, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"get-attachment"}, "id": {itoa64(id)}})
		return body
	}
	queryAttachments := func(c *http.Client) string {
		t.Helper()
		_, body, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"query-attachments"}, "query[posts_per_page]": {"40"}})
		return body
	}

	adminID, _ := testUpload(t, admin, base, "admin-private.jpg", img)
	a := makePrivate(adminID)
	if body := getAttachment(admin, adminID); !authzSigned(body) {
		t.Fatalf("the administrator gets no signed link (test assumption): %v", regexp.MustCompile(`"url":"[^"]*"`).FindAllString(body, -1))
	}
	// An author may browse the library but not edit the administrator's file: no signature anywhere.
	if body := getAttachment(author, adminID); !strings.Contains(body, `"success":true`) || authzSigned(body) {
		t.Errorf("author get-attachment: signed=%v %.200s", authzSigned(body), body)
	}
	if body := queryAttachments(author); authzSigned(body) {
		t.Error("author query-attachments carries a signed link")
	}
	if _, body := restGet(t, author, base, "/wp-json/wp/v2/media/"+itoa64(adminID)); authzSigned(body) {
		t.Error("author REST media carries a signed link")
	}
	// The editor (edit_others_posts) may edit it.
	if !authzSigned(getAttachment(editor, adminID)) {
		t.Error("editor gets no signed link for a file they may edit")
	}
	// The author's own private file is signed for them.
	ownID, _ := testUpload(t, author, base, "author-private.jpg", img)
	makePrivate(ownID)
	if !authzSigned(getAttachment(author, ownID)) {
		t.Error("author gets no signed link for their own private file")
	}

	// A product download: the editor may edit the file but does not manage the store.
	gooMakeSimple(t, srv, "E-book", 5, func(p *gooProduct) {
		p.Downloadable, p.Virtual = true, true
		p.Downloads = []gooDownload{{ID: "f1", Name: "book", File: base + "/wp-content/uploads/" + mediaPrivatePath(a.FilePath, false)}}
	})
	if authzSigned(getAttachment(editor, adminID)) {
		t.Error("editor gets a signed link to a product download")
	}
	if !authzSigned(getAttachment(manager, adminID)) || !authzSigned(getAttachment(admin, adminID)) {
		t.Error("shop manager / administrator get no signed link to a product download")
	}
	// A file uploaded as a downloadable file is a store file from the start.
	code, raw := authzUpload(t, admin, base, "manual.jpg", img, map[string]string{"type": "downloadable_product"})
	m := regexp.MustCompile(`"id":(\d+)`).FindStringSubmatch(raw)
	if code != http.StatusCreated || m == nil {
		t.Fatalf("downloadable upload: %d %.300s", code, raw)
	}
	dl, _ := srv.store.GetAttachment(ctx, gooAtoi64(m[1]))
	if !mediaIsPrivate(dl) || dl.Meta[mediaCommerceDownloadMeta] != true {
		t.Fatalf("downloadable upload not marked: %s %v", dl.FilePath, dl.Meta[mediaCommerceDownloadMeta])
	}
	if authzSigned(getAttachment(editor, dl.ID)) || !authzSigned(getAttachment(manager, dl.ID)) {
		t.Error("downloadable upload: editor signed or shop manager not")
	}
	// async-upload.php needs upload_files even to show an item.
	sub := hgSecondUser(t, srv, base, "sub", "subscriber")
	if code, body, _ := get(t, sub, base+"/wp-admin/async-upload?attachment_id="+itoa64(adminID)+"&fetch=3"); code == http.StatusOK && strings.Contains(body, "private") {
		t.Errorf("subscriber read an item through async-upload: %d", code)
	}
}

// D10: send-attachment-to-editor needs the media modal's nonce and upload_files.
func TestAuthzSendAttachmentToEditor(t *testing.T) {
	ts, admin, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, admin)
	base := ts.URL
	id, _ := testUpload(t, admin, base, "pic.jpg", gocdnTestImage(200, 100, "jpeg"))
	sub := hgSecondUser(t, srv, base, "sub", "subscriber")
	send := func(c *http.Client, nonce string) string {
		t.Helper()
		form := url.Values{"action": {"send-attachment-to-editor"}, "attachment[id]": {itoa64(id)}}
		if nonce != "" {
			form.Set("nonce", nonce)
		}
		_, body, _ := post(t, c, base+"/wp-admin/admin-ajax", form)
		return body
	}
	if body := send(sub, actionNonce(t, srv, sub, base, "media-send-to-editor")); strings.Contains(body, `"success":true`) {
		t.Errorf("subscriber got the editor markup: %.200s", body)
	}
	if body := send(admin, ""); strings.Contains(body, `"success":true`) {
		t.Errorf("no nonce accepted: %.200s", body)
	}
	if body := send(admin, actionNonce(t, srv, admin, base, "media-send-to-editor")); !strings.Contains(body, `"success":true`) || !strings.Contains(body, "wp-image-"+itoa64(id)) {
		t.Errorf("administrator with the nonce: %.200s", body)
	}
}

// D8: an upload or an attachment edit names a parent post only if the user may edit that post.
func TestAuthzRESTMediaParent(t *testing.T) {
	ts, admin, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, admin)
	base := ts.URL
	ctx := context.Background()
	author := hgSecondUser(t, srv, base, "andy", "author")
	au, _ := srv.store.UserByLoginOrEmail(ctx, "andy")
	theirs := mustInsertPost(t, srv, &store.Post{Type: "post", Title: "Admin's", Slug: "admins", AuthorID: 1})
	mine := mustInsertPost(t, srv, &store.Post{Type: "post", Title: "Mine", Slug: "mine", AuthorID: au.ID, Status: "draft"})
	img := gocdnTestImage(120, 80, "jpeg")

	if code, out := authzUpload(t, author, base, "a.jpg", img, map[string]string{"post": itoa64(theirs.ID)}); code != http.StatusForbidden {
		t.Errorf("author upload to another user's post: %d %.200s", code, out)
	}
	code, out := authzUpload(t, author, base, "b.jpg", img, map[string]string{"post": itoa64(mine.ID)})
	m := regexp.MustCompile(`"id":(\d+)`).FindStringSubmatch(out)
	if code != http.StatusCreated || m == nil {
		t.Fatalf("author upload to own post: %d %.200s", code, out)
	}
	id := m[1]
	if code, out := restSend(t, author, base, http.MethodPost, "/wp-json/wp/v2/media/"+id, `{"post":`+itoa64(theirs.ID)+`}`); code != http.StatusForbidden {
		t.Errorf("author moves own attachment to another user's post: %d %.200s", code, out)
	}
	if code, out := restSend(t, author, base, http.MethodPost, "/wp-json/wp/v2/media/"+id, `{"post":0}`); code != http.StatusOK {
		t.Errorf("author detaches own attachment: %d %.200s", code, out)
	}
	// The media modal's save-attachment has the same rule.
	_, body, _ := post(t, author, base+"/wp-admin/admin-ajax", url.Values{"action": {"save-attachment"}, "id": {id},
		"nonce": {actionNonce(t, srv, author, base, "update-post_"+id)}, "changes[parent]": {itoa64(theirs.ID)}})
	if a, _ := srv.store.GetAttachment(ctx, gooAtoi64(id)); a.PostID == theirs.ID || strings.Contains(body, `"success":true`) {
		t.Errorf("save-attachment attached to another user's post: %.200s", body)
	}
	// The administrator may attach anywhere.
	if code, out := restSend(t, admin, base, http.MethodPost, "/wp-json/wp/v2/media/"+id, `{"post":`+itoa64(theirs.ID)+`}`); code != http.StatusOK {
		t.Errorf("administrator attach: %d %.200s", code, out)
	}
}

// D9: ?_embed does not show anonymous readers an attachment of an unpublished post through
// another post's featured_media.
func TestAuthzEmbedFeaturedMediaReadable(t *testing.T) {
	ts, admin, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, admin)
	base := ts.URL
	ctx := context.Background()
	id, _ := testUpload(t, admin, base, "unreleased.jpg", gocdnTestImage(120, 80, "jpeg"))
	draft := mustInsertPost(t, srv, &store.Post{Type: "post", Title: "Draft", Slug: "draft-x", AuthorID: 1, Status: "draft"})
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE media SET post_id = $1 WHERE id = $2`, draft.ID, id); err != nil {
		t.Fatal(err)
	}
	pub := mustInsertPost(t, srv, &store.Post{Type: "post", Title: "Public", Slug: "public-x", AuthorID: 1, FeaturedMediaID: id})
	anon := gocdnAnon()
	embedded := func(body string) bool {
		t.Helper()
		var out struct {
			Embedded map[string]json.RawMessage `json:"_embedded"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("embed reply: %v %.200s", err, body)
		}
		_, ok := out.Embedded["wp:featuredmedia"]
		return ok
	}
	resp := doGet(t, anon, base+"/wp-json/wp/v2/posts/"+itoa64(pub.ID)+"?_embed", nil)
	if body := string(readAll(resp)); resp.StatusCode != http.StatusOK || embedded(body) {
		t.Errorf("anonymous embed leaks the attachment: %d %.300s", resp.StatusCode, body)
	}
	if _, body := restGet(t, admin, base, "/wp-json/wp/v2/posts/"+itoa64(pub.ID)+"?_embed"); !embedded(body) {
		t.Error("the administrator lost the embedded featured image")
	}
	// Attached to the published post itself, it is public.
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE media SET post_id = $1 WHERE id = $2`, pub.ID, id); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	resp = doGet(t, anon, base+"/wp-json/wp/v2/posts/"+itoa64(pub.ID)+"?_embed", nil)
	if body := string(readAll(resp)); !embedded(body) {
		t.Errorf("public featured image not embedded: %.300s", body)
	}
}

// D11: product taxonomies are managed with manage_product_terms.
func TestAuthzProductTermsCap(t *testing.T) {
	base, _, srv := gooSite(t)
	editor := hgSecondUser(t, srv, base, "eddie", "editor")
	manager := hgSecondUser(t, srv, base, "shopper", "shop_manager")
	for _, tax := range []string{"product_cat", "product_tag", "product_shipping_class", "pa_color"} {
		if code, _, _ := get(t, editor, base+"/wp-admin/edit-tags?taxonomy="+tax); code != http.StatusForbidden {
			t.Errorf("editor opened %s terms: %d", tax, code)
		}
		if code, _, _ := get(t, manager, base+"/wp-admin/edit-tags?taxonomy="+tax); code != http.StatusOK {
			t.Errorf("shop manager could not open %s terms: %d", tax, code)
		}
	}
	if code, _, _ := get(t, editor, base+"/wp-admin/edit-tags?taxonomy=category"); code != http.StatusOK {
		t.Errorf("editor lost the category screen: %d", code)
	}
	if termsCap("product_cat") != "manage_product_terms" || termsCap("category") != "manage_categories" {
		t.Error("termsCap")
	}
}

// D12: Publishing Soon shows a contributor only their own scheduled posts.
func TestAuthzDashboardScheduledOwnOnly(t *testing.T) {
	ts, admin, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, admin)
	base := ts.URL
	ctx := context.Background()
	contrib := hgSecondUser(t, srv, base, "connie", "contributor")
	cu, _ := srv.store.UserByLoginOrEmail(ctx, "connie")
	soon := time.Now().Add(72 * time.Hour)
	mustInsertPost(t, srv, &store.Post{Type: "post", Title: "Confidential merger announcement", Slug: "merger", AuthorID: 1, Status: "future", PublishedAt: &soon})
	mustInsertPost(t, srv, &store.Post{Type: "post", Title: "Connie scheduled piece", Slug: "connie-piece", AuthorID: cu.ID, Status: "future", PublishedAt: &soon})
	_, body, _ := get(t, contrib, base+"/wp-admin/")
	if strings.Contains(body, "Confidential merger announcement") {
		t.Error("a contributor sees another user's scheduled post title on the dashboard")
	}
	if !strings.Contains(body, "Connie scheduled piece") {
		t.Error("a contributor does not see their own scheduled post")
	}
	if _, body, _ := get(t, admin, base+"/wp-admin/"); !strings.Contains(body, "Confidential merger announcement") || !strings.Contains(body, "Connie scheduled piece") {
		t.Error("the administrator does not see every scheduled post")
	}
}

// O7: HeadlessGo ownership is decided over every locale version of a document. A version that is
// missing in the requested locale no longer makes the user its owner, so an author cannot delete
// another user's document in every language, nor take over its lock.
func TestHeadlessGoOwnershipAcrossLocales(t *testing.T) {
	base, c, srv := hgSite(t)
	ctx := context.Background()
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	hgModel(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	author := hgSecondUser(t, srv, base, "ann", "author")
	au, _ := srv.store.UserByLoginOrEmail(ctx, "ann")
	exists := func(doc, loc string) bool { _, err := srv.hgGetEntry(ctx, "article", doc, loc); return err == nil }
	refused := func(l string) bool { return strings.Contains(l, "hg-error") }

	// Another user's document, English only: the author asks for French, every locale.
	theirs := hgDocOf(t, hgCall(t, base, "POST", "/articles", full, map[string]any{"data": map[string]any{"title": "Theirs"}}))
	if _, _, l := hgEntryForm(t, author, base, "article", theirs, "delete_entry", url.Values{"locale": {"fr"}, "all_locales": {"1"}}); !refused(l) {
		t.Errorf("delete in every locale through a missing version allowed: %s", l)
	}
	hgEntryForm(t, author, base, "article", theirs, "bulk_entries", url.Values{"locale": {"fr"}, "all_locales": {"1"}, "bulk_action": {"delete"}, "docs[]": {theirs}})
	hgEntryForm(t, author, base, "article", theirs, "publish_entry", url.Values{"locale": {"fr"}})
	if !exists(theirs, "en") {
		t.Fatal("an author deleted another user's document through a missing locale")
	}
	if e, _ := srv.hgGetEntry(ctx, "article", theirs, "en"); e.Status() == "published" {
		t.Error("an author published another user's document")
	}

	// Lock takeover and review submission need edit_others_posts on another user's entry.
	sc := srv.hgReg(ctx).Get("article")
	caps := func(login, do, doc, loc string) error {
		t.Helper()
		u, err := srv.store.UserByLoginOrEmail(ctx, login)
		if err != nil {
			t.Fatal(err)
		}
		gc, _ := gin.CreateTestContext(httptest.NewRecorder())
		gc.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
		return srv.hgAdminCaps(gc, u, sc, do, doc, loc)
	}
	for _, do := range []string{"lock_takeover", "submit_review"} {
		if caps("ann", do, theirs, "fr") == nil || caps("ann", do, theirs, "en") == nil {
			t.Errorf("author may %s on another user's entry", do)
		}
	}
	asCreateUser(t, srv, "eddie", "editor", asStrongPass)
	if caps("eddie", "lock_takeover", theirs, "en") != nil {
		t.Error("an editor may not take over a lock")
	}
	if caps("ann", "save_entry", "", "fr") != nil {
		t.Error("a new entry is the author's own")
	}
	if caps("ann", "delete_entry", "no-such-document", "en") == nil {
		t.Error("a document that does not exist counted as owned")
	}

	// A document whose every version is the author's: they delete it everywhere. A mixed one:
	// only their own version.
	mine := hgDocOf(t, hgCall(t, base, "POST", "/articles", full, map[string]any{"data": map[string]any{"title": "Mine"}}))
	hgCall(t, base, "PUT", "/articles/"+mine+"?locale=fr", full, map[string]any{"data": map[string]any{"title": "Le mien"}})
	mixed := hgDocOf(t, hgCall(t, base, "POST", "/articles", full, map[string]any{"data": map[string]any{"title": "Mixed"}}))
	hgCall(t, base, "PUT", "/articles/"+mixed+"?locale=fr", full, map[string]any{"data": map[string]any{"title": "Mixte"}})
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE hg_entries SET created_by = $1 WHERE document_id = $2 OR (document_id = $3 AND locale = 'fr')`, au.ID, mine, mixed); err != nil {
		t.Fatal(err)
	}
	if _, _, l := hgEntryForm(t, author, base, "article", mine, "delete_entry", url.Values{"locale": {"en"}, "all_locales": {"1"}}); refused(l) || exists(mine, "en") || exists(mine, "fr") {
		t.Errorf("author could not delete their own document everywhere: %s", l)
	}
	if _, _, l := hgEntryForm(t, author, base, "article", mixed, "delete_entry", url.Values{"locale": {"fr"}, "all_locales": {"1"}}); !refused(l) || !exists(mixed, "en") {
		t.Errorf("author deleted a document with another user's version everywhere: %s", l)
	}
	if _, _, l := hgEntryForm(t, author, base, "article", mixed, "delete_entry", url.Values{"locale": {"fr"}}); refused(l) || exists(mixed, "fr") || !exists(mixed, "en") {
		t.Errorf("author could not delete their own locale version: %s", l)
	}
}
