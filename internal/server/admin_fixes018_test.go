package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// Regression tests for the admin-screen fixes of release 0.1.8.

// Directory URLs under the uploads route never list their files.
func TestUploadsNoDirectoryListing(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "wp-personal-data-exports"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wp-personal-data-exports", "wp-personal-data-file-secret.zip"), []byte("zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.StripPrefix("/wp-content/uploads", http.FileServer(noListFS{http.Dir(dir)})))
	defer ts.Close()
	for _, p := range []string{"/wp-content/uploads/", "/wp-content/uploads/wp-personal-data-exports/", "/wp-content/uploads/wp-personal-data-exports"} {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound || strings.Contains(string(b), "secret") {
			t.Errorf("GET %s: %d %q", p, resp.StatusCode, b)
		}
	}
	resp, err := http.Get(ts.URL + "/wp-content/uploads/wp-personal-data-exports/wp-personal-data-file-secret.zip")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("file: %d", resp.StatusCode)
	}
}

var quickDraftNonceRe = regexp.MustCompile(`value="post-quickdraft-save" />\s*<input type="hidden" name="_wpnonce" value="([^"]+)"`)

// Quick Draft and attachment texts go through kses for users without unfiltered_html.
func TestAdminFixes018Kses(t *testing.T) {
	s := secStart(t)
	ctx := context.Background()
	// The uploads route answers 404 for a directory.
	if code, body, _ := get(t, s.admin.c, s.base+"/wp-content/uploads/"); code != http.StatusNotFound || strings.Contains(body, "<pre>") {
		t.Errorf("uploads root listed: %d %q", code, body)
	}

	contrib, _ := s.user("contrib", "contributor")
	_, dash, _ := get(t, contrib, s.base+"/wp-admin/")
	m := quickDraftNonceRe.FindStringSubmatch(dash)
	if m == nil {
		t.Fatal("no Quick Draft form for a contributor")
	}
	if code, _, _ := post(t, contrib, s.base+"/wp-admin/index", url.Values{"_wpnonce": {m[1]}, "action": {"post-quickdraft-save"},
		"post_title": {"Sneaky"}, "content": {`<!-- wp:html --><script>alert(1)</script><img src=x onerror=alert(2)><!-- /wp:html -->`}}); code != http.StatusFound {
		t.Fatalf("quick draft: %d", code)
	}
	var content string
	if err := s.srv.store.Pool.QueryRow(ctx, `SELECT content FROM posts WHERE title = 'Sneaky'`).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "<script") || strings.Contains(content, "onerror") || !strings.Contains(content, "<!-- wp:html -->") {
		t.Errorf("quick draft content not filtered: %q", content)
	}
	// Plain text keeps working.
	if code, _, _ := post(t, contrib, s.base+"/wp-admin/index", url.Values{"_wpnonce": {m[1]}, "post_title": {"Plain"}, "content": {"One\n\nTwo"}}); code != http.StatusFound {
		t.Fatalf("quick draft: %d", code)
	}
	_ = s.srv.store.Pool.QueryRow(ctx, `SELECT content FROM posts WHERE title = 'Plain'`).Scan(&content)
	if !strings.Contains(content, "<p>One</p>") || !strings.Contains(content, "<p>Two</p>") {
		t.Errorf("plain quick draft: %q", content)
	}

	// An author's attachment caption and description.
	_, authorREST := s.user("writer", "author")
	writer, _ := s.srv.store.UserByLoginOrEmail(ctx, "writer")
	a := &store.Attachment{UploaderID: writer.ID, FilePath: "x.png", Mime: "image/png", Title: "x", Slug: "x"}
	if err := s.srv.store.InsertAttachment(ctx, a); err != nil {
		t.Fatal(err)
	}
	code, out, _, _ := authorREST.do("POST", "/wp/v2/media/"+itoa64(a.ID), map[string]any{
		"caption": `Nice <img src=x onerror=alert(1)>`, "description": `<script>alert(1)</script><strong>ok</strong>`})
	if code != http.StatusOK {
		t.Fatalf("update media: %d %v", code, out)
	}
	got, _ := s.srv.store.GetAttachment(ctx, a.ID)
	if strings.Contains(got.Caption, "onerror") || strings.Contains(got.Description, "<script") || !strings.Contains(got.Description, "<strong>ok</strong>") {
		t.Errorf("attachment texts not filtered: caption=%q description=%q", got.Caption, got.Description)
	}
	// Rows stored before the fix are filtered on output.
	_, _ = s.srv.store.Pool.Exec(ctx, `UPDATE media SET caption = '<img src=x onerror=alert(1)>' WHERE id = $1`, a.ID)
	_, out, _, _ = s.admin.do("GET", "/wp/v2/media/"+itoa64(a.ID), nil)
	if cap, _ := out["caption"].(map[string]any); cap == nil || strings.Contains(cap["rendered"].(string), "onerror") {
		t.Errorf("caption.rendered not filtered: %v", out["caption"])
	}
	// An administrator keeps unfiltered HTML.
	code, _, _, _ = s.admin.do("POST", "/wp/v2/media/"+itoa64(a.ID), map[string]any{"description": `<script>ok()</script>`})
	got, _ = s.srv.store.GetAttachment(ctx, a.ID)
	if code != http.StatusOK || got.Description != `<script>ok()</script>` {
		t.Errorf("admin description: %d %q", code, got.Description)
	}
}

var undoRe = regexp.MustCompile(`href="(/wp-admin/edit\?post_type=post&amp;doaction=undo[^"]+)">Undo</a>`)

// Classic Preview, autosave previews, Undo after trashing, Empty Trash, parent loops, and the link
// dialog's search.
func TestAdminFixes018Posts(t *testing.T) {
	s := secStart(t)
	ctx := context.Background()
	c := s.admin.c
	admin, _ := s.srv.store.UserByLoginOrEmail(ctx, "admin")
	if err := s.srv.settings.Set(ctx, map[string]any{"classic-editor-replace": "classic"}); err != nil {
		t.Fatal(err)
	}

	// --- Classic Preview Changes stores an autosave, never the live post.
	p := &store.Post{Type: "post", AuthorID: admin.ID, Status: "publish", Title: "Live title", Slug: "live-post", Content: "<p>Live words</p>",
		CommentStatus: "open", PingStatus: "open", Format: "standard", Meta: map[string]any{}}
	if err := s.srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	_, edit, _ := get(t, c, s.base+"/wp-admin/post?post="+itoa64(p.ID)+"&action=edit")
	if !strings.Contains(edit, `name="wp-preview" id="wp-preview"`) {
		t.Error("classic edit form has no wp-preview input")
	}
	code, _, loc := post(t, c, s.base+"/wp-admin/post", url.Values{"_wpnonce": {formNonce(t, edit)}, "action": {"editpost"}, "post_ID": {itoa64(p.ID)},
		"post_type": {"post"}, "post_title": {"Draft title"}, "content": {"<p>Unfinished words</p>"}, "wp-preview": {"dopreview"}})
	if code != http.StatusFound || !strings.Contains(loc, "preview_id="+itoa64(p.ID)) || !strings.Contains(loc, "preview=true") {
		t.Fatalf("preview: %d %s", code, loc)
	}
	live, _ := s.srv.store.GetPost(ctx, p.ID)
	if live.Content != "<p>Live words</p>" || live.Title != "Live title" || live.Status != "publish" {
		t.Errorf("preview changed the live post: %q %q %s", live.Title, live.Content, live.Status)
	}
	if !strings.HasPrefix(loc, "http") {
		loc = s.base + loc
	}
	_, page, _ := get(t, c, loc)
	if !strings.Contains(page, "Unfinished words") || strings.Contains(page, "Live words") {
		t.Errorf("preview page does not show the autosave")
	}
	_, page, _ = get(t, c, regexp.MustCompile(`preview_nonce=[^&]+`).ReplaceAllString(loc, "preview_nonce=bad"))
	if !strings.Contains(page, "Live words") || strings.Contains(page, "Unfinished words") {
		t.Errorf("a bad preview nonce must show the stored post")
	}
	anon := asNewClient()
	_, page, _ = get(t, anon, loc)
	if strings.Contains(page, "Unfinished words") {
		t.Errorf("a visitor sees the autosave")
	}

	// A draft of the current user is saved in place and previewed with preview=true.
	d := &store.Post{Type: "post", AuthorID: admin.ID, Status: "draft", Title: "Draft", Content: "<p>old draft</p>", CommentStatus: "open", PingStatus: "open", Format: "standard", Meta: map[string]any{}}
	if err := s.srv.store.InsertPost(ctx, d); err != nil {
		t.Fatal(err)
	}
	_, edit, _ = get(t, c, s.base+"/wp-admin/post?post="+itoa64(d.ID)+"&action=edit")
	code, _, loc = post(t, c, s.base+"/wp-admin/post", url.Values{"_wpnonce": {formNonce(t, edit)}, "post_ID": {itoa64(d.ID)},
		"post_title": {"Draft"}, "content": {"<p>new draft</p>"}, "wp-preview": {"dopreview"}})
	d2, _ := s.srv.store.GetPost(ctx, d.ID)
	if code != http.StatusFound || strings.Contains(loc, "preview_id") || !strings.Contains(loc, "preview=true") || d2.Content != "<p>new draft</p>" || d2.Status != "draft" {
		t.Errorf("draft preview: %d %s %q %s", code, loc, d2.Content, d2.Status)
	}

	// --- Undo after Trash.
	if err := s.srv.store.TrashPost(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	_, list, _ := get(t, c, s.base+"/wp-admin/edit?trashed=1&ids="+itoa64(p.ID))
	m := undoRe.FindStringSubmatch(list)
	if m == nil || !strings.Contains(m[1], "_wpnonce=") {
		t.Fatalf("no signed Undo link: %v", m)
	}
	undo := html.UnescapeString(m[1])
	code, _, loc = get(t, c, s.base+undo)
	if code != http.StatusFound || !strings.Contains(loc, "untrashed=1") {
		t.Errorf("undo: %d %s", code, loc)
	}
	// wp_untrash_post() restores to draft, as in WordPress 5.6+.
	if back, _ := s.srv.store.GetPost(ctx, p.ID); back.Status == "trash" {
		t.Errorf("status after undo = %s", back.Status)
	}
	bulkNonce := regexp.MustCompile(`_wpnonce=([^&]+)`).FindStringSubmatch(undo)[1]
	if code, _, _ := get(t, c, s.base+strings.Replace(undo, "_wpnonce="+bulkNonce, "_wpnonce=bad", 1)); code != http.StatusForbidden {
		t.Errorf("undo with a bad nonce: %d", code)
	}

	// --- Empty Trash.
	var trashed []int64
	for _, title := range []string{"Junk one", "Junk two"} {
		j := &store.Post{Type: "post", AuthorID: admin.ID, Status: "publish", Title: title, CommentStatus: "open", PingStatus: "open", Format: "standard", Meta: map[string]any{}}
		if err := s.srv.store.InsertPost(ctx, j); err != nil {
			t.Fatal(err)
		}
		_ = s.srv.store.TrashPost(ctx, j.ID)
		trashed = append(trashed, j.ID)
	}
	_, list, _ = get(t, c, s.base+"/wp-admin/edit?post_status=trash")
	if !strings.Contains(list, `<input type="submit" name="delete_all" id="delete_all" class="button apply" value="Empty Trash" />`) {
		t.Error("no Empty Trash button in the Trash view")
	}
	code, _, loc = get(t, c, s.base+"/wp-admin/edit?post_type=post&post_status=trash&delete_all=Empty+Trash&action=-1&_wpnonce="+bulkNonce)
	if code != http.StatusFound || !strings.Contains(loc, "deleted=2") {
		t.Errorf("empty trash: %d %s", code, loc)
	}
	for _, id := range trashed {
		if _, err := s.srv.store.GetPost(ctx, id); err == nil {
			t.Errorf("post %d still exists after Empty Trash", id)
		}
	}
	if _, err := s.srv.store.GetPost(ctx, p.ID); err != nil {
		t.Errorf("a published post was deleted by Empty Trash")
	}

	// --- Parent loops are refused.
	about := &store.Post{Type: "page", AuthorID: admin.ID, Status: "publish", Title: "About", Slug: "about", CommentStatus: "closed", PingStatus: "closed", Format: "standard", Meta: map[string]any{}}
	if err := s.srv.store.InsertPost(ctx, about); err != nil {
		t.Fatal(err)
	}
	team := &store.Post{Type: "page", AuthorID: admin.ID, Status: "publish", Title: "Team", Slug: "team", ParentID: about.ID, CommentStatus: "closed", PingStatus: "closed", Format: "standard", Meta: map[string]any{}}
	if err := s.srv.store.InsertPost(ctx, team); err != nil {
		t.Fatal(err)
	}
	about.ParentID = about.ID
	_ = s.srv.store.UpdatePost(ctx, about, admin.ID)
	about.ParentID = team.ID // Team is About's child
	_ = s.srv.store.UpdatePost(ctx, about, admin.ID)
	if got, _ := s.srv.store.GetPost(ctx, about.ID); got.ParentID != 0 {
		t.Errorf("About's parent = %d, want 0", got.ParentID)
	}
	if got, _ := s.srv.store.GetPost(ctx, team.ID); got.ParentID != about.ID {
		t.Errorf("Team's parent = %d", got.ParentID)
	}
	// A loop stored earlier is listed on Pages, not hidden.
	_, _ = s.srv.store.Pool.Exec(ctx, `UPDATE posts SET parent_id = $2 WHERE id = $1`, about.ID, team.ID)
	_, list, _ = get(t, c, s.base+"/wp-admin/edit?post_type=page")
	if !strings.Contains(list, ">About</a>") || !strings.Contains(list, ">Team</a>") {
		t.Error("pages in a parent loop vanish from the Pages list")
	}
	_, _ = s.srv.store.Pool.Exec(ctx, `UPDATE posts SET parent_id = NULL WHERE id = $1`, about.ID)
	// Terms.
	parent := &store.Term{Taxonomy: "category", Name: "Parent", Slug: "parent"}
	_ = s.srv.store.InsertTerm(ctx, parent)
	child := &store.Term{Taxonomy: "category", Name: "Child", Slug: "child", ParentID: parent.ID}
	_ = s.srv.store.InsertTerm(ctx, child)
	parent.ParentID = child.ID
	if err := s.srv.store.UpdateTerm(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.srv.store.GetTerm(ctx, parent.ID); got.ParentID != 0 {
		t.Errorf("term parent loop stored: %d", got.ParentID)
	}

	// --- The classic link dialog searches content.
	_, edit, _ = get(t, c, s.base+"/wp-admin/post?post="+itoa64(p.ID)+"&action=edit")
	lm := regexp.MustCompile(`name="_ajax_linking_nonce" value="([^"]+)"`).FindStringSubmatch(edit)
	if lm == nil {
		t.Fatal("no internal-linking nonce on the classic edit screen")
	}
	code, body, _ := post(t, c, s.base+"/wp-admin/admin-ajax", url.Values{"action": {"wp-link-ajax"}, "_ajax_linking_nonce": {lm[1]}, "page": {"1"}, "search": {"About"}})
	var res []map[string]any
	if code != http.StatusOK || json.Unmarshal([]byte(body), &res) != nil || len(res) == 0 || res[0]["title"] != "About" || res[0]["info"] != "Page" ||
		!strings.Contains(res[0]["permalink"].(string), "about") {
		t.Errorf("wp-link-ajax search: %d %s", code, body)
	}
	code, body, _ = post(t, c, s.base+"/wp-admin/admin-ajax", url.Values{"action": {"wp-link-ajax"}, "_ajax_linking_nonce": {lm[1]}, "page": {"1"}})
	if code != http.StatusOK || !strings.Contains(body, `"About"`) {
		t.Errorf("wp-link-ajax recent: %d %s", code, body)
	}
	code, body, _ = post(t, c, s.base+"/wp-admin/admin-ajax", url.Values{"action": {"wp-link-ajax"}, "_ajax_linking_nonce": {lm[1]}, "search": {"zzzznothing"}})
	if code != http.StatusOK || strings.TrimSpace(body) != "false" {
		t.Errorf("wp-link-ajax no results: %d %s", code, body)
	}
	if code, _, _ := post(t, c, s.base+"/wp-admin/admin-ajax", url.Values{"action": {"wp-link-ajax"}, "_ajax_linking_nonce": {"bad"}}); code != http.StatusForbidden {
		t.Errorf("wp-link-ajax with a bad nonce: %d", code)
	}
}

// wxrImportBody imports an export file through Tools › Import (upload, author step, background
// job) and returns the report page.
func wxrImportBody(t *testing.T, c *http.Client, srv *Server, base, xml string) string {
	t.Helper()
	return wxrImportFlow(t, c, srv, base, xml, true, nil)
}

// wxrImportFlow drives the three importer steps; the background job is run by hand.
func wxrImportFlow(t *testing.T, c *http.Client, srv *Server, base, xml string, fetch bool, authors url.Values) string {
	t.Helper()
	_, form, _ := get(t, c, base+"/wp-admin/import")
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("_wpnonce", formNonce(t, form))
	fw, _ := mw.CreateFormFile("import", "export.xml")
	_, _ = io.WriteString(fw, xml)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, base+"/wp-admin/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), "Assign Authors") {
		t.Fatalf("import upload: %d %s", resp.StatusCode, b)
	}
	vals := url.Values{"_wpnonce": {formNonce(t, string(b))}, "import_id": {regexp.MustCompile(`name="import_id" value="([a-f0-9]+)"`).FindStringSubmatch(string(b))[1]}}
	if fetch {
		vals.Set("fetch_attachments", "1")
	}
	for _, m := range regexp.MustCompile(`name="imported_authors" value="([^"]*)"`).FindAllStringSubmatch(string(b), -1) {
		vals.Add("imported_authors", m[1])
	}
	for k, v := range authors {
		vals[k] = v
	}
	code, _, loc := post(t, c, base+"/wp-admin/import", vals)
	if code != http.StatusSeeOther || !strings.Contains(loc, "run=") {
		t.Fatalf("import queue: %d %s", code, loc)
	}
	_, running, _ := get(t, c, base+loc)
	if !strings.Contains(running, "Waiting for the import to start") {
		t.Errorf("progress page while queued: %s", running)
	}
	srv.runDueJobs(context.Background(), "test", srv.jobHandlers())
	code, body, _ := get(t, c, base+loc)
	if code != http.StatusOK || !strings.Contains(body, "All done.") {
		t.Fatalf("import: %d %s", code, body)
	}
	return body
}

// Posts and media share one id space (migration 0003), so every item of an export has its own
// <wp:post_id>; a re-import maps the featured image to the media and the menu item to the page.
func TestWXRAttachmentIDsDoNotCollide(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installUsersSite(t, c, ts.URL)
	ctx := context.Background()
	admin, _ := srv.store.UserByLoginOrEmail(ctx, "admin")

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)
	if err := os.MkdirAll(filepath.Join(srv.cfg.UploadsDir, "2026", "11"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srv.cfg.UploadsDir, "2026", "11", "logo.png"), pngBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	contact := &store.Post{Type: "page", AuthorID: admin.ID, Status: "publish", Title: "Contact", Slug: "contact", CommentStatus: "closed", PingStatus: "closed", Format: "standard", Meta: map[string]any{}}
	if err := srv.store.InsertPost(ctx, contact); err != nil {
		t.Fatal(err)
	}
	logo := &store.Attachment{UploaderID: admin.ID, FilePath: "2026/11/logo.png", Mime: "image/png", Title: "logo", Slug: "logo", Width: 8, Height: 8}
	if err := srv.store.InsertAttachment(ctx, logo); err != nil {
		t.Fatal(err)
	}
	if logo.ID == contact.ID {
		t.Fatalf("media id %d is also a post id", logo.ID)
	}
	withLogo := &store.Post{Type: "post", AuthorID: admin.ID, Status: "publish", Title: "With logo", Slug: "with-logo", FeaturedMediaID: logo.ID, CommentStatus: "open", PingStatus: "open", Format: "standard", Meta: map[string]any{}}
	if err := srv.store.InsertPost(ctx, withLogo); err != nil {
		t.Fatal(err)
	}
	menuID, _ := srv.store.CreateMenu(ctx, "Main")
	if err := srv.store.AddMenuItem(ctx, &store.EditItem{MenuID: menuID, Kind: "post_type", Object: "page", TargetID: contact.ID, Label: "Contact", Order: 1, Status: "publish"}); err != nil {
		t.Fatal(err)
	}

	_, xml, _ := get(t, c, ts.URL+"/wp-admin/export?download=true&content=all")
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`<wp:post_id>(\d+)</wp:post_id>`).FindAllStringSubmatch(xml, -1) {
		if seen[m[1]] {
			t.Errorf("post id %s used by more than one item", m[1])
		}
		seen[m[1]] = true
	}

	// Remove the content, then import the file again.
	_, _ = srv.store.Pool.Exec(ctx, `DELETE FROM posts WHERE id = ANY($1)`, []int64{contact.ID, withLogo.ID})
	_, _ = srv.store.Pool.Exec(ctx, `DELETE FROM media WHERE id = $1`, logo.ID)
	_ = srv.store.DeleteMenu(ctx, menuID)
	old := wxrFetchTransport
	wxrFetchTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r2 := r.Clone(r.Context())
		r2.URL.Scheme, r2.URL.Host = "http", ts.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(r2)
	})
	t.Cleanup(func() { wxrFetchTransport = old })
	body := wxrImportBody(t, c, srv, ts.URL, xml)
	if strings.Contains(body, "Failed to import Media") {
		t.Fatalf("media import failed: %s", regexp.MustCompile(`Failed to import Media[^<]*`).FindString(body))
	}
	var newPage, newPost, newMedia, featured int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM posts WHERE type = 'page' AND title = 'Contact'`).Scan(&newPage)
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id, coalesce(featured_media_id, 0) FROM posts WHERE title = 'With logo'`).Scan(&newPost, &featured)
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM media WHERE title = 'logo'`).Scan(&newMedia)
	if newPage == 0 || newPost == 0 || newMedia == 0 {
		t.Fatalf("not imported: page %d post %d media %d", newPage, newPost, newMedia)
	}
	if featured != newMedia {
		t.Errorf("featured image = %d, want media %d", featured, newMedia)
	}
	var menu int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM menus WHERE slug = 'main'`).Scan(&menu)
	items, _ := srv.store.MenuItems(ctx, menu)
	if len(items) != 1 || items[0].TargetID != newPage {
		t.Errorf("menu item target: %+v, want page %d", items, newPage)
	}
}

// "All content" carries the posts and terms of Pursae Custom Fields types, and the importer takes
// them back; items of unknown types are logged.
func TestWXRCustomPostTypes(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installUsersSite(t, c, ts.URL)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := srv.store.Pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels)
		VALUES ('wxbook', 'Books', false, true, 'wxbooks', false, '{"title":true,"editor":true}', '{"name":"Books"}') ON CONFLICT (slug) DO NOTHING`)
	exec(`INSERT INTO taxonomies (slug, name, hierarchical, object_types, rest_base, public, is_builtin, labels)
		VALUES ('wxgenre', 'Genres', true, '["wxbook"]', 'wxgenre', true, false, '{"name":"Genres"}') ON CONFLICT (slug) DO NOTHING`)
	acfgoRegistry.Lock()
	acfgoRegistry.types["wxbook"] = &acfgoPostTypeDef{Name: "wxbook", Public: true, ShowUI: true, Labels: map[string]string{"name": "Books", "singular_name": "Book"}}
	acfgoRegistry.taxes["wxgenre"] = &acfgoTaxonomyDef{Name: "wxgenre", Public: true, Hierarchical: true, ObjectTypes: []string{"wxbook"}}
	acfgoRegistry.Unlock()
	t.Cleanup(func() {
		acfgoRegistry.Lock()
		delete(acfgoRegistry.types, "wxbook")
		delete(acfgoRegistry.taxes, "wxgenre")
		acfgoRegistry.Unlock()
	})
	admin, _ := srv.store.UserByLoginOrEmail(ctx, "admin")
	fiction := &store.Term{Taxonomy: "wxgenre", Name: "Fiction", Slug: "fiction"}
	_ = srv.store.InsertTerm(ctx, fiction)
	scifi := &store.Term{Taxonomy: "wxgenre", Name: "Sci-fi", Slug: "scifi", ParentID: fiction.ID}
	_ = srv.store.InsertTerm(ctx, scifi)
	book := &store.Post{Type: "wxbook", AuthorID: admin.ID, Status: "publish", Title: "Dune", Slug: "dune", Content: "<p>Spice</p>", CommentStatus: "closed", PingStatus: "closed", Format: "standard", Meta: map[string]any{}}
	if err := srv.store.InsertPost(ctx, book); err != nil {
		t.Fatal(err)
	}
	_ = srv.store.SetPostTerms(ctx, book.ID, "wxgenre", []int64{scifi.ID})

	_, xml, _ := get(t, c, ts.URL+"/wp-admin/export?download=true&content=all")
	for _, want := range []string{"<wp:post_type><![CDATA[wxbook]]></wp:post_type>", "<title><![CDATA[Dune]]></title>",
		"<wp:term_taxonomy><![CDATA[wxgenre]]></wp:term_taxonomy><wp:term_slug><![CDATA[scifi]]></wp:term_slug><wp:term_parent><![CDATA[fiction]]></wp:term_parent>",
		`<category domain="wxgenre" nicename="scifi"><![CDATA[Sci-fi]]></category>`} {
		if !strings.Contains(xml, want) {
			t.Errorf("export misses %q", want)
		}
	}

	exec(`DELETE FROM posts WHERE id = $1`, book.ID)
	exec(`DELETE FROM terms WHERE taxonomy = 'wxgenre'`)
	unknown := `<item><title><![CDATA[Ghost]]></title><wp:post_id>999999</wp:post_id><wp:status><![CDATA[publish]]></wp:status><wp:post_type><![CDATA[ghost_type]]></wp:post_type></item>`
	body := wxrImportBody(t, c, srv, ts.URL, strings.Replace(xml, "</channel>", unknown+"\n</channel>", 1))
	if !strings.Contains(body, "Invalid post type ghost_type") {
		t.Errorf("an unknown post type is not reported")
	}
	var id int64
	if err := srv.store.Pool.QueryRow(ctx, `SELECT id FROM posts WHERE type = 'wxbook' AND title = 'Dune'`).Scan(&id); err != nil {
		t.Fatalf("book not imported: %v", err)
	}
	var slug, parentSlug string
	if err := srv.store.Pool.QueryRow(ctx, `SELECT t.slug, coalesce(p.slug, '') FROM post_terms pt JOIN terms t ON t.id = pt.term_id
		LEFT JOIN terms p ON p.id = t.parent_id WHERE pt.post_id = $1 AND t.taxonomy = 'wxgenre'`, id).Scan(&slug, &parentSlug); err != nil {
		t.Fatalf("book term not imported: %v", err)
	}
	if slug != "scifi" || parentSlug != "fiction" {
		t.Errorf("book term = %q (parent %q)", slug, parentSlug)
	}
}
