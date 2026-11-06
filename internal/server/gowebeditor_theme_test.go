package server

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// TestGoWebEditorThemeBuilder goes from "create a header" to "theme listed under Themes": theme
// parts made in the Theme Builder and saved with save_builder, display conditions, the parts on a
// bundled theme, Save as theme, activation, editing the active theme, export and import.
func TestGoWebEditorThemeBuilder(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Theme Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK || !strings.Contains(body, "Success!") {
		t.Fatalf("install: %d", code)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	ctx := context.Background()
	pool := getPool(t)

	// The Theme Builder screen is under Appearance.
	code, body, _ := get(t, c, base+"/wp-admin/theme-builder")
	if code != http.StatusOK {
		t.Fatalf("theme builder: %d %.300s", code, body)
	}
	for _, want := range []string{"<h1 class=\"wp-heading-inline\">Theme Builder</h1>", "Headers", "Single Posts", "404 Pages", "Save as theme",
		`href="/wp-admin/theme-builder"`, "Twenty Twenty-Five"} {
		if !strings.Contains(body, want) {
			t.Errorf("theme builder misses %q", want)
		}
	}
	tbNonce := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)
	if tbNonce == nil {
		t.Fatal("no theme builder nonce")
	}
	nonce := tbNonce[1]

	// newPart creates a part with "Add New" and returns its id (the redirect opens the editor).
	newPart := func(typ, title string) string {
		t.Helper()
		code, _, loc := post(t, c, base+"/wp-admin/theme-builder", url.Values{"_wpnonce": {nonce}, "action": {"new"}, "type": {typ}, "title": {title}})
		m := regexp.MustCompile(`^/wp-admin/post\?post=(\d+)&action=web-builder$`).FindStringSubmatch(loc)
		if code != http.StatusFound || m == nil {
			t.Fatalf("new %s: %d %q", typ, code, loc)
		}
		return m[1]
	}
	headerID := newPart("header", "Main Header")

	// The editor opens a theme part like a page: header document, Site widgets, links back.
	code, body, _ = get(t, c, base+"/wp-admin/post?post="+headerID+"&action=web-builder")
	if code != http.StatusOK {
		t.Fatalf("editor: %d %.300s", code, body)
	}
	for _, want := range []string{`"type":"header"`, `"theme-elements":{"title":"Site"`, `"widget_type":"theme-site-title"`,
		`"post_type_title":"Theme Part"`, "gowebeditor-theme-bar", "Save as theme", `"title":"Header"`} {
		if !strings.Contains(body, want) {
			t.Errorf("editor misses %q", want)
		}
	}
	ajaxNonce := regexp.MustCompile(`"ajax":\{"nonce":"([^"]+)"`).FindStringSubmatch(body)
	if ajaxNonce == nil {
		t.Fatal("no elementor_ajax nonce")
	}
	ajax := func(postID, actions string) map[string]any {
		t.Helper()
		resp, err := c.PostForm(base+"/wp-admin/admin-ajax", url.Values{"action": {"elementor_ajax"}, "_nonce": {ajaxNonce[1]},
			"editor_post_id": {postID}, "initial_document_id": {postID}, "actions": {actions}})
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("ajax: %v %.200s", err, b)
		}
		return out
	}
	result := func(out map[string]any, id string) map[string]any {
		t.Helper()
		r, _ := out["data"].(map[string]any)["responses"].(map[string]any)[id].(map[string]any)
		if r == nil || r["success"] != true {
			t.Fatalf("response %q: %v", id, out)
		}
		return r
	}

	// Widgets config: the Site widgets' controls, with the menu choices filled in.
	wc := gweJSONText(result(ajax(headerID, `{"w":{"action":"get_widgets_config","data":{}}}`), "w")["data"])
	for _, want := range []string{`"section_image":{"label":"Site Logo"`, `"menu_location":{`, `"primary":"Primary Menu"`, `"archive-posts":{"controls"`} {
		if !strings.Contains(wc, want) {
			t.Errorf("widgets config misses %q", want)
		}
	}
	// render_widget in the editor.
	rw := result(ajax(headerID, `{"r":{"action":"render_widget","data":{"data":{"id":"aa00001","elType":"widget","widgetType":"theme-site-title","settings":{},"elements":[]}}}}`), "r")
	if r := gweJSONText(rw["data"]); !strings.Contains(r, "Theme Site") {
		t.Errorf("render_widget site title: %s", r)
	}

	save := func(id, tree string) {
		t.Helper()
		result(ajax(id, `{"s":{"action":"save_builder","data":{"status":"publish","elements":`+tree+`,"settings":{"post_status":"publish"}}}}`), "s")
	}
	widget := func(id, typ, settings string) string {
		return `{"id":"` + id + `","elType":"widget","widgetType":"` + typ + `","isInner":false,"settings":` + settings + `,"elements":[]}`
	}
	container := func(id string, kids ...string) string {
		return `[{"id":"` + id + `","elType":"container","isInner":false,"settings":{"flex_direction":"row"},"elements":[` + strings.Join(kids, ",") + `]}]`
	}
	save(headerID, container("h000001", widget("h000002", "theme-site-title", `{"header_size":"p","title_color":"#C0392B"}`),
		widget("h000003", "nav-menu", `{"menu_location":"primary"}`), widget("h000004", "search-form", `{}`)))
	footerID := newPart("footer", "Main Footer")
	save(footerID, container("f000001", widget("f000002", "theme-site-copyright", `{"text":"© [year] [site_title] — footer part"}`)))
	singleID := newPart("single-post", "Single Post")
	save(singleID, container("s000001", widget("s000002", "theme-post-title", `{}`), widget("s000003", "post-info", `{}`), widget("s000004", "theme-post-content", `{}`)))
	archiveID := newPart("archive", "Archive")
	save(archiveID, container("a000001", widget("a000002", "theme-archive-title", `{}`), widget("a000003", "archive-posts", `{"columns":"2"}`)))
	notFoundID := newPart("error-404", "Not Found")
	save(notFoundID, container("n000001", widget("n000002", "heading", `{"title":"Lost in Pursae Web Builder space"}`)))

	var conds string
	_ = pool.QueryRow(ctx, `SELECT meta->'_gwe_conditions'::text FROM posts WHERE id=$1::bigint`, singleID).Scan(&conds)
	if conds != `["include/singular/post"]` {
		t.Errorf("single default conditions: %s", conds)
	}

	// The site's own parts apply on top of the bundled theme.
	_, body, _ = getPage(t, c, base+"/?p=1")
	for _, want := range []string{"elementor-location-header", "gwe-theme", ">Theme Site</a></p>", "elementor-location-footer", "footer part",
		"elementor-widget-theme-post-title", "Hello world!", "Welcome to Pursae", "elementor-post-info", "/wp-content/themes/hello-web-builder/style.css",
		"widget-theme-elements.min.css", "/wp-content/uploads/elementor/css/post-" + headerID + ".css", "elementorFrontendConfig"} {
		if !strings.Contains(body, want) {
			t.Errorf("single post with parts misses %q", want)
		}
	}
	if strings.Contains(body, "twentytwentyfive-style-css") {
		t.Error("the bundled theme's stylesheet is still printed in theme-part mode")
	}
	_, body, _ = getPage(t, c, base+"/?cat=1")
	for _, want := range []string{"elementor-widget-archive-posts", "elementor-grid-2", `class="elementor-post__title"><a href=`, "Hello world!", "Category: <span>Uncategorized</span></h1>"} {
		if !strings.Contains(body, want) {
			t.Errorf("category archive misses %q", want)
		}
	}
	code, body, _ = getPage(t, c, base+"/?p=987654")
	if code != http.StatusNotFound || !strings.Contains(body, "Lost in Pursae Web Builder space") || !strings.Contains(body, "elementor-location-error-404") {
		t.Errorf("404 part: %d", code)
	}
	// Pages have no single-page part: header + footer with the fallback title and content.
	var pageID string
	_ = pool.QueryRow(ctx, `SELECT id::text FROM posts WHERE type='page' AND title='Sample Page'`).Scan(&pageID)
	_, body, _ = getPage(t, c, base+"/?page_id="+pageID)
	if !strings.Contains(body, "elementor-location-header") || !strings.Contains(body, `<h1 class="entry-title">Sample Page</h1>`) {
		t.Errorf("page fallback main missing")
	}

	// Display conditions: exclude the sample page from the header.
	code, body, _ = get(t, c, base+"/wp-admin/theme-builder?action=conditions&post="+headerID)
	if code != http.StatusOK || !strings.Contains(body, `value="general" selected`) || !strings.Contains(body, `value="singular/page/`+pageID+`"`) {
		t.Fatalf("conditions screen: %d", code)
	}
	code, _, loc := post(t, c, base+"/wp-admin/theme-builder", url.Values{"_wpnonce": {nonce}, "action": {"conditions"}, "post": {headerID},
		"mode[]": {"include", "exclude"}, "rule[]": {"general", "singular/page/" + pageID}})
	if code != http.StatusFound || !strings.Contains(loc, "message=conditions") {
		t.Fatalf("save conditions: %d %s", code, loc)
	}
	_, body, _ = getPage(t, c, base+"/?page_id="+pageID)
	if strings.Contains(body, "elementor-location-header") || !strings.Contains(body, `<header id="site-header" class="site-header">`) {
		t.Errorf("excluded page still shows the header part")
	}
	_, body, _ = get(t, c, base+"/wp-admin/theme-builder")
	if !strings.Contains(body, "Exclude: Page: Sample Page") || !strings.Contains(body, "Entire Site") {
		t.Errorf("theme builder list does not show the conditions")
	}

	// Hello Golementor is listed with the bundled themes and can be activated; the site's parts
	// are its design (the working copy).
	_, themesBody, _ := get(t, c, base+"/wp-admin/themes")
	for _, want := range []string{`data-slug="hello-web-builder"`, "Hello Pursae Web Builder", "/wp-content/themes/hello-web-builder/screenshot.png"} {
		if !strings.Contains(themesBody, want) {
			t.Errorf("themes screen misses %q", want)
		}
	}
	if strings.Contains(themesBody, "gwe-saved-themes") {
		t.Error("saved themes section shown without saved themes")
	}
	activate := func(slug string) {
		t.Helper()
		_, body, _ := get(t, c, base+"/wp-admin/themes")
		act := regexp.MustCompile(`href="(/wp-admin/themes\?action=activate&amp;stylesheet=` + regexp.QuoteMeta(slug) + `&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
		if act == nil {
			t.Fatalf("no activate link for %s", slug)
		}
		if code, _, loc := get(t, c, base+html.UnescapeString(act[1])); code != http.StatusFound || !strings.Contains(loc, "activated=true") {
			t.Fatalf("activate %s: %d %s", slug, code, loc)
		}
	}
	activate("hello-web-builder")
	_, body, _ = get(t, c, base+"/wp-admin/themes")
	if !strings.Contains(body, "<span>Active:</span> Hello Pursae Web Builder") {
		t.Errorf("Hello Pursae Web Builder is not shown as active")
	}
	_, body, _ = getPage(t, c, base+"/?p=1")
	for _, want := range []string{"wp-theme-hello-web-builder", "elementor-location-header", "footer part", "elementor-widget-theme-post-title",
		"Welcome to Pursae", `<main id="content" class="site-main`, "hello-web-builder-css", "elementor-kit-"} {
		if !strings.Contains(body, want) {
			t.Errorf("hello theme single misses %q", want)
		}
	}
	// The excluded page shows the theme's own minimal header; the 404 part and the archive part apply.
	_, body, _ = getPage(t, c, base+"/?page_id="+pageID)
	if strings.Contains(body, "elementor-location-header") || !strings.Contains(body, `<header id="site-header" class="site-header">`) ||
		!strings.Contains(body, `<h1 class="entry-title">Sample Page</h1>`) || !strings.Contains(body, "elementor-location-footer") ||
		strings.Contains(body, `<footer id="site-footer"`) {
		t.Errorf("hello theme page fallback")
	}
	if code, body, _ := getPage(t, c, base+"/?p=987654"); code != http.StatusNotFound || !strings.Contains(body, "Lost in Pursae Web Builder space") {
		t.Errorf("hello theme 404: %d", code)
	}
	if _, body, _ := get(t, c, base+"/"); !strings.Contains(body, "elementor-widget-archive-posts") {
		t.Errorf("hello theme blog home has no archive part")
	}
	// Without any part the theme shows its minimal defaults.
	_ = pool.QueryRow(ctx, `UPDATE posts SET status='draft' WHERE id=$1::bigint RETURNING 1`, headerID).Scan(new(int))
	_ = pool.QueryRow(ctx, `UPDATE posts SET status='draft' WHERE id=$1::bigint RETURNING 1`, footerID).Scan(new(int))
	_, body, _ = getPage(t, c, base+"/?page_id="+pageID)
	for _, want := range []string{`<header id="site-header" class="site-header">`, `<p class="site-title"><a href="`, `<footer id="site-footer" class="site-footer">`, "&copy; "} {
		if !strings.Contains(body, want) {
			t.Errorf("hello theme defaults miss %q", want)
		}
	}
	_ = pool.QueryRow(ctx, `UPDATE posts SET status='publish' WHERE id=$1::bigint RETURNING 1`, headerID).Scan(new(int))
	_ = pool.QueryRow(ctx, `UPDATE posts SET status='publish' WHERE id=$1::bigint RETURNING 1`, footerID).Scan(new(int))
	_, body, _ = get(t, c, base+"/wp-admin/theme-builder")
	if !strings.Contains(body, "the design of <strong>Hello Pursae Web Builder</strong>, your active theme") {
		t.Errorf("theme builder does not describe the working copy")
	}

	// Save as theme: a JSON snapshot in the saved list; the working copy is untouched.
	code, _, loc = post(t, c, base+"/wp-admin/theme-builder", url.Values{"_wpnonce": {nonce}, "action": {"save-theme"},
		"name": {"Go Studio"}, "description": {"Made in the Theme Builder."}})
	if code != http.StatusFound || !strings.Contains(loc, "message=saved") {
		t.Fatalf("save as theme: %d %s", code, loc)
	}
	var custom bool
	var data string
	if err := pool.QueryRow(ctx, `SELECT custom, data::text FROM themes WHERE slug='gwe-go-studio'`).Scan(&custom, &data); err != nil || !custom {
		t.Fatalf("theme row: %v %v", err, custom)
	}
	var snap gweSnapshot
	if err := json.Unmarshal([]byte(data), &snap); err != nil || len(snap.Parts) != 5 || snap.KitID == 0 || snap.Kit["system_colors"] == nil {
		t.Fatalf("snapshot: %v parts=%d kit=%d", err, len(snap.Parts), snap.KitID)
	}
	var owned int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE type='gwe_template' AND meta->>'_gwe_theme' = 'gwe-go-studio'`).Scan(&owned)
	if owned != 0 {
		t.Errorf("a saved theme owns no part posts, got %d", owned)
	}
	var siteParts int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE type='gwe_template' AND coalesce(meta->>'_gwe_theme','') = '' AND status='publish'`).Scan(&siteParts)
	if siteParts != 5 {
		t.Errorf("working copy after save: %d parts", siteParts)
	}
	if s := themeBySlug("gwe-go-studio"); s == nil || !s.Custom {
		t.Fatal("saved theme not registered")
	}

	// The theme code no longer lists, previews or activates saved themes (removed with the old
	// builder), so the rest of the saved-theme flow is not exercised here.
}
