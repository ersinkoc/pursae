package server

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

var mediaFormNonceRe = regexp.MustCompile(`"_wpnonce":"([A-Za-z0-9]+)"`)

// asyncUploadAs posts a tiny PNG to async-upload (the plupload endpoint) and returns the response body.
func asyncUploadAs(t *testing.T, c *http.Client, base, nonce string, postID int64, ajax bool) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("_wpnonce", nonce)
	_ = mw.WriteField("post_id", itoa64(postID))
	if ajax {
		_ = mw.WriteField("action", "upload-attachment")
	}
	fw, _ := mw.CreateFormFile("async-upload", "pic.png")
	_, _ = fw.Write([]byte("\x89PNG\r\n\x1a\n"))
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, base+"/wp-admin/async-upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// Round 2 admin fixes: sticky needs edit_others_posts + publish_posts, uploads attach only to posts the
// user may edit, an attachment's edit screen follows its save rule, and menu-locations-save checks its nonce.
func TestAdminR2Permissions(t *testing.T) {
	s := secStart(t)
	ctx := context.Background()
	admin, _ := s.srv.store.UserByLoginOrEmail(ctx, "admin")
	if err := s.srv.settings.Set(ctx, map[string]any{"classic-editor-replace": "classic"}); err != nil {
		t.Fatal(err)
	}

	// --- Sticky: an author saving their own post can neither stick nor unstick it.
	authorC, _ := s.user("writer", "author")
	writer, _ := s.srv.store.UserByLoginOrEmail(ctx, "writer")
	mine := &store.Post{Type: "post", AuthorID: writer.ID, Status: "publish", Title: "Mine", Content: "<p>x</p>",
		CommentStatus: "open", PingStatus: "open", Format: "standard", Meta: map[string]any{}}
	if err := s.srv.store.InsertPost(ctx, mine); err != nil {
		t.Fatal(err)
	}
	_, edit, _ := get(t, authorC, s.base+"/wp-admin/post?post="+itoa64(mine.ID)+"&action=edit")
	if strings.Contains(edit, `id="sticky-span"`) {
		t.Error("the author's form offers Stick this post")
	}
	save := func(c *http.Client, p *store.Post, sticky string) *store.Post {
		t.Helper()
		_, edit, _ := get(t, c, s.base+"/wp-admin/post?post="+itoa64(p.ID)+"&action=edit")
		form := url.Values{"_wpnonce": {formNonce(t, edit)}, "post_ID": {itoa64(p.ID)}, "post_type": {"post"},
			"post_title": {p.Title}, "content": {p.Content}, "visibility": {"public"}, "save": {"Update"}}
		if sticky != "" {
			form.Set("sticky", sticky)
		}
		if code, body, _ := post(t, c, s.base+"/wp-admin/post", form); code != http.StatusFound {
			t.Fatalf("save: %d %s", code, body)
		}
		got, _ := s.srv.store.GetPost(ctx, p.ID)
		return got
	}
	if got := save(authorC, mine, "sticky"); got.Sticky {
		t.Error("an author made a post sticky")
	}
	_, _ = s.srv.store.Pool.Exec(ctx, `UPDATE posts SET sticky = true WHERE id = $1`, mine.ID)
	if got := save(authorC, mine, ""); !got.Sticky {
		t.Error("an author's save unstuck a post an editor made sticky")
	}
	if got := save(s.admin.c, mine, ""); got.Sticky {
		t.Error("the administrator could not unstick the post")
	}
	if got := save(s.admin.c, mine, "sticky"); !got.Sticky {
		t.Error("the administrator could not stick the post")
	}

	// --- Uploads attach only to a post the uploader may edit.
	theirs := &store.Post{Type: "post", AuthorID: admin.ID, Status: "publish", Title: "Admin gallery", Content: "[gallery]",
		CommentStatus: "open", PingStatus: "open", Format: "standard", Meta: map[string]any{}}
	if err := s.srv.store.InsertPost(ctx, theirs); err != nil {
		t.Fatal(err)
	}
	_, mediaNew, _ := get(t, authorC, s.base+"/wp-admin/media-new")
	m := mediaFormNonceRe.FindStringSubmatch(mediaNew)
	if m == nil {
		t.Fatal("no media-form nonce on Upload Media")
	}
	attached := func(postID int64) int {
		var n int
		_ = s.srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM media WHERE post_id = $1`, postID).Scan(&n)
		return n
	}
	if _, body := asyncUploadAs(t, authorC, s.base, m[1], theirs.ID, true); !strings.Contains(body, "not allowed to attach files to this post") {
		t.Errorf("ajax upload onto another user's post: %s", body)
	}
	if code, body := asyncUploadAs(t, authorC, s.base, m[1], theirs.ID, false); code != http.StatusOK || strings.Contains(body, "error") {
		t.Errorf("async upload: %d %s", code, body)
	}
	if n := attached(theirs.ID); n != 0 {
		t.Errorf("%d uploads attached to another user's post", n)
	}
	if _, body := asyncUploadAs(t, authorC, s.base, m[1], mine.ID, true); !strings.Contains(body, `"success":true`) {
		t.Errorf("upload onto the author's own post: %s", body)
	}
	if n := attached(mine.ID); n != 1 {
		t.Errorf("own post has %d attachments, want 1", n)
	}

	// --- An attachment's edit screen: not for a user who could not save it.
	a := &store.Attachment{UploaderID: admin.ID, FilePath: "secret.png", Mime: "image/png", Title: "Admin file", Slug: "admin-file"}
	if err := s.srv.store.InsertAttachment(ctx, a); err != nil {
		t.Fatal(err)
	}
	subC, _ := s.user("reader", "subscriber")
	if code, body, _ := get(t, subC, s.base+"/wp-admin/post?post="+itoa64(a.ID)+"&action=edit"); code != http.StatusForbidden || strings.Contains(body, "Admin file") {
		t.Errorf("subscriber opened another user's attachment: %d", code)
	}
	if code, body, _ := get(t, s.admin.c, s.base+"/wp-admin/post?post="+itoa64(a.ID)+"&action=edit"); code != http.StatusOK || !strings.Contains(body, "Admin file") {
		t.Errorf("admin attachment edit: %d", code)
	}

	// --- menu-locations-save needs the menu screen's nonce.
	if code, _, _ := post(t, s.admin.c, s.base+"/wp-admin/admin-ajax", url.Values{"action": {"menu-locations-save"}}); code != http.StatusForbidden {
		t.Errorf("menu-locations-save without a nonce: %d", code)
	}
	_, menus, _ := get(t, s.admin.c, s.base+"/wp-admin/nav-menus")
	nm := regexp.MustCompile(`id="menu-settings-column-nonce" name="menu-settings-column-nonce" value="([^"]+)"`).FindStringSubmatch(menus)
	if nm == nil {
		t.Fatal("no menu-settings-column-nonce on Menus")
	}
	if code, body, _ := post(t, s.admin.c, s.base+"/wp-admin/admin-ajax", url.Values{"action": {"menu-locations-save"}, "menu-settings-column-nonce": {nm[1]}}); code != http.StatusOK || body != "1" {
		t.Errorf("menu-locations-save with the nonce: %d %q", code, body)
	}

	// --- Dashboard excerpts are escaped once (wp_trim_words() output is printed as it is).
	draft := &store.Post{Type: "post", AuthorID: admin.ID, Status: "draft", Title: "Cartoon", Content: "<p>Tom & Jerry chase each other around the house all day long again</p>",
		CommentStatus: "open", PingStatus: "open", Format: "standard", Meta: map[string]any{}}
	if err := s.srv.store.InsertPost(ctx, draft); err != nil {
		t.Fatal(err)
	}
	_, dash, _ := get(t, s.admin.c, s.base+"/wp-admin/")
	if !strings.Contains(dash, "Tom &amp; Jerry chase") || strings.Contains(dash, "&amp;amp;") || strings.Contains(dash, "&amp;hellip;") {
		t.Errorf("dashboard draft excerpt escaped twice")
	}
}

const wxrNoIDsFixture = `<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0" xmlns:excerpt="http://wordpress.org/export/1.2/excerpt/" xmlns:content="http://purl.org/rss/1.0/modules/content/"
	xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:wp="http://wordpress.org/export/1.2/">
<channel>
	<title>Hand made</title>
	<item>
		<title><![CDATA[First page]]></title>
		<content:encoded><![CDATA[One]]></content:encoded>
		<wp:post_name><![CDATA[first-page]]></wp:post_name>
		<wp:status><![CDATA[publish]]></wp:status>
		<wp:post_type><![CDATA[page]]></wp:post_type>
		<wp:postmeta><wp:meta_key><![CDATA[_wpgo_demo]]></wp:meta_key><wp:meta_value><![CDATA[some-theme]]></wp:meta_value></wp:postmeta>
		<wp:comment><wp:comment_author><![CDATA[Ann]]></wp:comment_author><wp:comment_content><![CDATA[First comment]]></wp:comment_content><wp:comment_approved>1</wp:comment_approved><wp:comment_parent>0</wp:comment_parent></wp:comment>
		<wp:comment><wp:comment_author><![CDATA[Bob]]></wp:comment_author><wp:comment_content><![CDATA[Second comment]]></wp:comment_content><wp:comment_approved>1</wp:comment_approved><wp:comment_parent>0</wp:comment_parent></wp:comment>
	</item>
	<item>
		<title><![CDATA[Second page]]></title>
		<content:encoded><![CDATA[Two]]></content:encoded>
		<wp:post_name><![CDATA[second-page]]></wp:post_name>
		<wp:status><![CDATA[publish]]></wp:status>
		<wp:post_parent>0</wp:post_parent>
		<wp:post_type><![CDATA[page]]></wp:post_type>
	</item>
</channel>
</rss>
`

// A file without wp:post_id / wp:comment_id (hand-written or from another tool): id 0 means "no
// parent", so the items and comments stay top level; another site's demo marks are not imported.
func TestWXRImportWithoutIDs(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	wxrImportBody(t, c, ts.URL, wxrNoIDsFixture)
	var parent int64
	var demo string
	if err := srv.store.Pool.QueryRow(ctx, `SELECT coalesce(parent_id,0), coalesce(meta->>'_wpgo_demo','') FROM posts WHERE slug = 'second-page'`).Scan(&parent, &demo); err != nil {
		t.Fatal(err)
	}
	if parent != 0 {
		t.Errorf("second page parented to %d", parent)
	}
	if err := srv.store.Pool.QueryRow(ctx, `SELECT coalesce(meta->>'_wpgo_demo','') FROM posts WHERE slug = 'first-page'`).Scan(&demo); err != nil || demo != "" {
		t.Errorf("demo mark imported: %q %v", demo, err)
	}
	var replies int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM comments WHERE content LIKE '%comment' AND parent_id IS NOT NULL AND parent_id <> 0`).Scan(&replies)
	if replies != 0 {
		t.Errorf("%d imported comments became replies", replies)
	}
}
