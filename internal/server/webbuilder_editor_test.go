package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Integration tests of the Web Builder editor screen and assets (need the test database).

func wbTestPost(t *testing.T, srv *Server, typ, title, status string, author int64) *store.Post {
	t.Helper()
	p := &store.Post{Type: typ, Title: title, Slug: strings.ToLower(strings.ReplaceAll(title, " ", "-")), Status: status, AuthorID: author}
	if err := srv.store.InsertPost(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

var wbBootRe = regexp.MustCompile(`<script>window\.__WEB_BUILDER__ = (.*);</script>`)

// wbOpen opens the editor of a post; it returns the status, the page and its headers.
func wbOpen(t *testing.T, c *http.Client, base string, id int64) (int, string, http.Header) {
	t.Helper()
	resp, err := c.Get(base + wbEditURL(id))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

// wbBootOf decodes the boot config of an editor page.
func wbBootOf(t *testing.T, page string) (map[string]any, string) {
	t.Helper()
	m := wbBootRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no boot config in the editor page:\n%.2000s", page)
	}
	var boot map[string]any
	if err := json.Unmarshal([]byte(m[1]), &boot); err != nil {
		t.Fatalf("boot config: %v\n%s", err, m[1])
	}
	return boot, m[1]
}

func wbPath(v any, path string) any {
	for _, k := range strings.Split(path, ".") {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	return v
}

func TestWebBuilderEditorScreen(t *testing.T) {
	ts, c, srv := newTestServer(t, true)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if !srv.wbOn() {
		t.Fatal("wbOn is false with the builder switched on")
	}
	page := wbTestPost(t, srv, "page", `About </script><script>alert(1)</script> us`, "draft", 1)

	code, body, h := wbOpen(t, c, base, page.ID)
	if code != http.StatusOK {
		t.Fatalf("editor: %d %.500s", code, body)
	}
	if cc := h.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control %q, want no-store", cc)
	}
	if ct := h.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type %q", ct)
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self' 'unsafe-inline'") || !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("admin CSP missing on the editor: %q", csp)
	}
	// No admin chrome, the old builder or a second script source.
	for _, bad := range []string{"wpadminbar", "adminmenu", "elementor", "gowebeditor", "<script src=\"http"} {
		if strings.Contains(body, bad) {
			t.Errorf("editor page contains %q", bad)
		}
	}
	if n := strings.Count(strings.ToLower(body), "</script"); n != 2 {
		t.Errorf("%d </script in the page, want 2 (title escaping)", n)
	}
	boot, raw := wbBootOf(t, body)

	// The bundle the page loads exists and is served.
	m, err := srv.wbManifest()
	if err != nil {
		t.Fatal(err)
	}
	prefix, entry, _ := wbEditorBundle(m)
	js := regexp.MustCompile(`<script type="module" src="([^"]+)"`).FindStringSubmatch(body)
	if js == nil || js[1] != base+prefix+entry.JS {
		t.Fatalf("module script %v, want %s", js, base+prefix+entry.JS)
	}
	for _, u := range append([]string{js[1]}, base+prefix+entry.CSS[0], base+webbuilder.SiteAssetsPath+m.Site.Runtime) {
		resp, err := c.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != wbImmutable {
			t.Errorf("GET %s: %d, Cache-Control %q", u, resp.StatusCode, resp.Header.Get("Cache-Control"))
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") && !strings.HasPrefix(ct, "text/css") {
			t.Errorf("GET %s: Content-Type %q", u, ct)
		}
	}

	// The boot config, field by field (contract §2).
	want := map[string]any{
		"apiVersion": float64(webbuilder.APIVersion), "rendererVersion": float64(webbuilder.RendererVersion),
		"document.postId": float64(page.ID), "document.postType": "page", "document.status": "draft",
		"document.rev": float64(0), "document.doc": nil, "document.draft": nil, "document.lock": nil,
		"document.detached": false, "document.page.title": page.Title, "document.page.slug": page.Slug,
		"document.page.indexable": true,
		"user.id":                 float64(1), "rest.root": base + "/wp-json/", "rest.namespace": "web-builder/v2",
		"links.site": base, "links.exit": base + "/wp-admin/edit?post_type=page",
		"links.editPost": base + "/wp-admin/post?post=" + itoa64(page.ID) + "&action=edit",
		"caps.publish":   true, "caps.uploadFiles": true, "caps.unfilteredHtml": true, "caps.editOthers": true,
		"locale": "en_US", "siteAssets": base + webbuilder.SiteAssetsPath,
		"ai.enabled": false, "ai.llmBase": "", "ai.imagesUrl": "", "ai.allowCode": false,
		"media.maxUploadBytes": float64(maxUploadBytes),
	}
	for k, v := range want {
		if got := wbPath(boot, k); got != v {
			t.Errorf("boot %s = %#v, want %#v", k, got, v)
		}
	}
	nonce, _ := wbPath(boot, "rest.nonce").(string)
	if nonce == "" {
		t.Fatal("no rest nonce")
	}
	if got := wbPath(boot, "fonts.cssUrl"); got != base+"/wp-json/web-builder/v2/fonts/css2?_wpnonce="+nonce {
		t.Errorf("fonts.cssUrl = %v", got)
	}
	if prov, ok := wbPath(boot, "ai.providers").([]any); !ok || len(prov) != 0 {
		t.Errorf("ai.providers = %#v, want []", wbPath(boot, "ai.providers"))
	}
	if types, _ := wbPath(boot, "media.types").([]any); len(types) == 0 {
		t.Error("no media types")
	}
	if p, _ := wbPath(boot, "links.preview").(string); !strings.HasSuffix(p, "preview=true") {
		t.Errorf("links.preview = %q", p)
	}
	// The nonce is a working wp_rest nonce.
	req, _ := http.NewRequest(http.MethodGet, base+"/wp-json/wp/v2/users/me", nil)
	req.Header.Set("X-WP-Nonce", nonce)
	if resp, err := c.Do(req); err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("wp_rest nonce from the boot config does not authenticate: %v %v", resp, err)
	} else {
		resp.Body.Close()
	}
	// No secret of the site or the user.
	admin, _ := srv.store.UserByID(ctx, 1)
	for name, secret := range map[string]string{"auth_secret": srv.settings.String("auth_secret"), "password hash": admin.PasswordHash} {
		if secret != "" && strings.Contains(raw, secret) {
			t.Errorf("boot config leaks the %s", name)
		}
	}
	for _, bad := range []string{"password", "secret", "apiKey", "api_key", "x-api-key"} {
		if strings.Contains(strings.ToLower(raw), strings.ToLower(bad)) {
			t.Errorf("boot config mentions %q", bad)
		}
	}

	// The editor took the core edit lock.
	p, _ := srv.store.GetPost(ctx, page.ID)
	if uid, at, ok := wbParseLock(p.Meta[webbuilder.EditLockMeta]); !ok || uid != 1 || time.Since(at) > time.Minute {
		t.Errorf("_edit_lock = %v", p.Meta[webbuilder.EditLockMeta])
	}

	// Another user's live lock is reported, not stolen; an expired one is taken over.
	editorID := asCreateUser(t, srv, "ed", "editor", asStrongPass)
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE users SET display_name = 'Edith' WHERE id = $1`, editorID); err != nil {
		t.Fatal(err)
	}
	live := strconv.FormatInt(time.Now().Unix(), 10) + ":" + itoa64(editorID)
	wbSetMeta(t, srv, page.ID, webbuilder.EditLockMeta, live)
	_, body, _ = wbOpen(t, c, base, page.ID)
	boot, _ = wbBootOf(t, body)
	if wbPath(boot, "document.lock.user.id") != float64(editorID) || wbPath(boot, "document.lock.user.name") != "Edith" {
		t.Errorf("document.lock = %#v", wbPath(boot, "document.lock"))
	}
	if p, _ := srv.store.GetPost(ctx, page.ID); p.Meta[webbuilder.EditLockMeta] != live {
		t.Errorf("a live lock was overwritten: %v", p.Meta[webbuilder.EditLockMeta])
	}
	wbSetMeta(t, srv, page.ID, webbuilder.EditLockMeta, strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)+":"+itoa64(editorID))
	_, body, _ = wbOpen(t, c, base, page.ID)
	if boot, _ = wbBootOf(t, body); wbPath(boot, "document.lock") != nil {
		t.Errorf("expired lock reported: %#v", wbPath(boot, "document.lock"))
	}
	if p, _ := srv.store.GetPost(ctx, page.ID); !strings.HasSuffix(p.Meta[webbuilder.EditLockMeta].(string), ":1") {
		t.Errorf("expired lock not taken: %v", p.Meta[webbuilder.EditLockMeta])
	}

	// A stored doc and the user's newer draft come through as stored JSON (unknown fields kept).
	docJSON := `{"sections":[{"id":"s1","kind":"hero","future":{"x":1}}],"site":{"brand":"#123456"}}`
	d := &store.WebBuilderDoc{PostID: page.ID, Doc: json.RawMessage(docJSON), Page: json.RawMessage(`{"title":"Stored","slug":"stored","description":"","indexable":false}`), Renderer: 1}
	if err := srv.store.WebBuilder().PublishDoc(ctx, d, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := srv.store.WebBuilder().SaveDraft(ctx, &store.WebBuilderDraft{PostID: page.ID, UserID: 1, Doc: json.RawMessage(`{"sections":[],"site":{}}`), BaseRev: 1}); err != nil {
		t.Fatal(err)
	}
	// Another user's draft is never shown.
	if err := srv.store.WebBuilder().SaveDraft(ctx, &store.WebBuilderDraft{PostID: page.ID, UserID: editorID, Doc: json.RawMessage(`{"sections":[{"id":"theirs"}],"site":{}}`)}); err != nil {
		t.Fatal(err)
	}
	_, body, _ = wbOpen(t, c, base, page.ID)
	boot, raw = wbBootOf(t, body)
	if wbPath(boot, "document.rev") != float64(1) || wbPath(boot, "document.renderer") != float64(1) || wbPath(boot, "document.page.title") != "Stored" {
		t.Errorf("document = %#v", wbPath(boot, "document"))
	}
	if !strings.Contains(raw, `"future":{"x":1}`) {
		t.Error("unknown doc fields were dropped")
	}
	if wbPath(boot, "document.draft.baseRev") != float64(1) || wbPath(boot, "document.draft.page.title") != "Stored" {
		t.Errorf("draft = %#v", wbPath(boot, "document.draft"))
	}
	if strings.Contains(raw, "theirs") {
		t.Error("another user's draft is in the boot config")
	}

	// Trashed posts and post types the builder does not edit are refused.
	if err := srv.store.TrashPost(ctx, page.ID); err != nil {
		t.Fatal(err)
	}
	if code, body, _ := wbOpen(t, c, base, page.ID); code != http.StatusForbidden || strings.Contains(body, "__WEB_BUILDER__") {
		t.Errorf("trashed post: %d", code)
	}
	block := wbTestPost(t, srv, "wp_block", "Pattern", "publish", 1)
	if code, body, _ := wbOpen(t, c, base, block.ID); code == http.StatusOK && strings.Contains(body, "__WEB_BUILDER__") {
		t.Errorf("wp_block opened in the builder: %d", code)
	}
	// A post opens too.
	post := wbTestPost(t, srv, "post", "Hello builder", "publish", 1)
	if code, body, _ := wbOpen(t, c, base, post.ID); code != http.StatusOK {
		t.Errorf("post: %d", code)
	} else if boot, _ := wbBootOf(t, body); wbPath(boot, "links.exit") != base+"/wp-admin/edit" {
		t.Errorf("post links.exit = %v", wbPath(boot, "links.exit"))
	}
	// The development action of the side-by-side phase redirects to the editor action.
	if code, _, loc := get(t, c, base+"/wp-admin/post?post="+itoa64(post.ID)+"&action=web-builder-next"); code != http.StatusMovedPermanently || loc != wbEditURL(post.ID) {
		t.Errorf("action=web-builder-next: %d %q", code, loc)
	}
}

func wbSetMeta(t *testing.T, srv *Server, id int64, key, value string) {
	t.Helper()
	if _, err := srv.store.Pool.Exec(context.Background(), `UPDATE posts SET meta = meta || jsonb_build_object($2::text, $3::text) WHERE id = $1`, id, key, value); err != nil {
		t.Fatal(err)
	}
}

func TestWebBuilderEditorCaps(t *testing.T) {
	ts, c, srv := newTestServer(t, true)
	base := ts.URL
	installAndLogin(t, ts, c)
	adminPage := wbTestPost(t, srv, "page", "Admin page", "publish", 1)

	sub := hgSecondUser(t, srv, base, "sub", "subscriber")
	if code, body, _ := wbOpen(t, sub, base, adminPage.ID); code != http.StatusForbidden || strings.Contains(body, "__WEB_BUILDER__") {
		t.Errorf("subscriber: %d", code)
	}
	contrib := hgSecondUser(t, srv, base, "contrib", "contributor")
	if code, body, _ := wbOpen(t, contrib, base, adminPage.ID); code != http.StatusForbidden || strings.Contains(body, "__WEB_BUILDER__") {
		t.Errorf("contributor on another's page: %d", code)
	}
	if p, _ := srv.store.GetPost(context.Background(), adminPage.ID); p.Meta[webbuilder.EditLockMeta] != nil {
		t.Errorf("a refused user took the lock: %v", p.Meta[webbuilder.EditLockMeta])
	}
	anon := anonClient()
	if code, body, _ := wbOpen(t, anon, base, adminPage.ID); code == http.StatusOK || strings.Contains(body, "__WEB_BUILDER__") {
		t.Errorf("logged out: %d", code)
	}

	// A contributor's own draft post opens, without publish, upload or unfiltered HTML.
	u, err := srv.store.UserByLoginOrEmail(context.Background(), "contrib")
	if err != nil {
		t.Fatal(err)
	}
	own := wbTestPost(t, srv, "post", "Contrib draft", "draft", u.ID)
	code, body, _ := wbOpen(t, contrib, base, own.ID)
	if code != http.StatusOK {
		t.Fatalf("contributor's own draft: %d %.300s", code, body)
	}
	boot, _ := wbBootOf(t, body)
	for k, v := range map[string]any{"caps.publish": false, "caps.uploadFiles": false, "caps.unfilteredHtml": false, "caps.editOthers": false, "user.id": float64(u.ID)} {
		if got := wbPath(boot, k); got != v {
			t.Errorf("contributor boot %s = %v, want %v", k, got, v)
		}
	}
}

// With the plugin off the new editor's action falls back to the core editor and its assets are gone.
func TestWebBuilderOff(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	if srv.wbOn() {
		t.Fatal("wbOn on a fresh install")
	}
	p := wbTestPost(t, srv, "page", "Plain", "publish", 1)
	if code, body, _ := wbOpen(t, c, base, p.ID); code != http.StatusOK || strings.Contains(body, "__WEB_BUILDER__") {
		t.Errorf("plugin off: %d, new editor shown: %v", code, strings.Contains(body, "__WEB_BUILDER__"))
	}
	m, err := srv.wbManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{webbuilder.EditorAssetsPath + m.Editor.JS, webbuilder.SiteAssetsPath + m.Site.Runtime, webbuilder.SiteAssetsPath + m.Site.CSS} {
		if code, _, _ := get(t, c, base+path); code != http.StatusNotFound {
			t.Errorf("plugin off: GET %s = %d, want 404", path, code)
		}
	}
}
