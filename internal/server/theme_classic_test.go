package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClassicColors(t *testing.T) {
	css := classicColorCSS(map[string]any{})
	for _, want := range []string{"<style id='classic-style-inline-css'>", "--cl-accent-light:#2563eb;", "--cl-bg-light:#ffffff;", "--cl-on-accent-light:#ffffff;"} {
		if !strings.Contains(css, want) {
			t.Errorf("default colors miss %q: %s", want, css)
		}
	}
	if strings.Contains(css, "--cl-link-light") || strings.Contains(css, "--cl-header-bg-light") {
		t.Errorf("unset link and header colors are printed: %s", css)
	}
	// A light accent is darkened for text on white and gets dark text on buttons; a dark header
	// background gets light header text.
	css = classicColorCSS(map[string]any{"accent_color": "#ffcc00", "header_bg_color": "#101010"})
	if strings.Contains(css, "--cl-accent-text-light:#ffcc00") || !strings.Contains(css, "--cl-on-accent-light:#111111") ||
		!strings.Contains(css, "--cl-header-text-light:#ffffff") {
		t.Errorf("readable colors not derived: %s", css)
	}
	m := regexp.MustCompile(`--cl-accent-text-light:(#[0-9a-f]{6})`).FindStringSubmatch(css)
	if m == nil || classicContrast(m[1], "#ffffff") < 4.5 {
		t.Errorf("accent text contrast too low: %v", m)
	}
	m = regexp.MustCompile(`--cl-accent-dark:(#[0-9a-f]{6})`).FindStringSubmatch(classicColorCSS(map[string]any{"accent_color": "#1e3a8a"}))
	if m == nil || classicContrast(m[1], classicDarkBG) < 4.5 {
		t.Errorf("dark accent contrast too low: %v", m)
	}
	if got := string(classicFooterHTML(`<b>{site}</b> {year} <script>alert(1)</script><a href="javascript:alert(1)" onclick="x()">x</a> <a href="https://example.test/">ok</a>`, "A & B", 2031)); got !=
		`<b>A &amp; B</b> 2031 x <a href="https://example.test/">ok</a>` {
		t.Errorf("footer text: %s", got)
	}
}

func TestClassicTheme(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL
	ctx := context.Background()
	db := getPool(t)

	code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Test Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	})
	if code != http.StatusOK || !strings.Contains(body, "Success!") {
		t.Fatalf("install failed: %d", code)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login failed: %d", code)
	}

	// Listed on the Themes, Add Themes and Theme File Editor screens and in REST, then activated.
	_, body, _ = get(t, c, base+"/wp-admin/themes")
	m := regexp.MustCompile(`action=activate(?:&amp;|&)stylesheet=classic(?:&amp;|&)_wpnonce=([0-9a-f]+)`).FindStringSubmatch(body)
	if m == nil || !strings.Contains(body, "/wp-content/themes/classic/screenshot.png") {
		t.Fatal("no Classic card on the Themes screen")
	}
	if _, body, _ := get(t, c, base+"/wp-admin/theme-install"); !strings.Contains(body, "Classic") {
		t.Error("Add Themes misses Classic")
	}
	if code, b := restGet(t, c, base, "/wp-json/wp/v2/themes/classic"); code != http.StatusOK || !strings.Contains(b, `"editor-styles":true`) {
		t.Errorf("REST theme: %d %.200s", code, b)
	}
	if code, b, _ := get(t, c, base+"/wp-admin/theme-editor?theme=classic&file=style.css"); code != http.StatusOK || !strings.Contains(b, "Theme Name: Classic") {
		t.Errorf("theme editor: %d", code)
	}
	if code, _, loc := get(t, c, base+"/wp-admin/themes?action=activate&stylesheet=classic&_wpnonce="+m[1]); code != http.StatusFound || !strings.Contains(loc, "activated=true") {
		t.Fatalf("activate: %d %s", code, loc)
	}

	// Content: a featured image and a tag on Hello world!, a sticky aside, a protected post and an author bio.
	var helloID, mediaID, tagID, userID int64
	year := strconv.Itoa(time.Now().Year())
	if err := db.QueryRow(ctx, `SELECT id, author_id FROM posts WHERE title = 'Hello world!'`).Scan(&helloID, &userID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO media (slug, file_path, mime_type, width, height, title, caption) VALUES ('lake', '2026/10/lake.jpg', 'image/jpeg', 1600, 1000, 'lake', 'The lake at dawn') RETURNING id`).Scan(&mediaID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO terms (taxonomy, slug, name, post_count) VALUES ('post_tag', 'slow', 'Slow', 1) RETURNING id`).Scan(&tagID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE posts SET featured_media_id = $1 WHERE id = $2`, []any{mediaID, helloID}},
		{`INSERT INTO post_terms (post_id, term_id) VALUES ($1, $2)`, []any{helloID, tagID}},
		{`UPDATE users SET profile = profile || '{"description":"Writes about slow things."}' WHERE id = $1`, []any{userID}},
		{`INSERT INTO posts (type, author_id, slug, title, content, status, published_at, format, sticky) VALUES ('post', $1, 'an-aside', 'An aside', '<p>Just a short note.</p>', 'publish', now() - interval '2 days', 'aside', true)`, []any{userID}},
		{`INSERT INTO posts (type, author_id, slug, title, content, status, published_at, password) VALUES ('post', $1, 'locked', 'Locked away', '<p>Secret words.</p>', 'publish', now() - interval '3 days', 'pw')`, []any{userID}},
		{`INSERT INTO widgets (area_id, type, sort_order, settings) SELECT id, 'block', 0, '{"content":"<!-- wp:paragraph --><p>Sidebar hello</p><!-- /wp:paragraph -->"}' FROM widget_areas WHERE theme = 'classic' AND slug = 'sidebar-1'`, nil},
	} {
		if _, err := db.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}

	assets := map[string]bool{}
	assetRe := regexp.MustCompile(`(?:href|src)=['"]((?:` + regexp.QuoteMeta(base) + `)?/wp-content/themes/classic/[^'"]+)['"]`)
	anon := &http.Client{CheckRedirect: c.CheckRedirect}
	page := func(c *http.Client, path string, wantCode int, wants ...string) string {
		t.Helper()
		code, body, _ := get(t, c, base+path)
		if dir := os.Getenv("WPGO_CLASSIC_DUMP"); dir != "" {
			name := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(path, "-"), "-")
			_ = os.WriteFile(filepath.Join(dir, "page-"+name+".html"), []byte(body), 0o644)
		}
		if code != wantCode {
			t.Errorf("%s: status %d, want %d", path, code, wantCode)
		}
		if strings.Contains(body, "Template error") {
			t.Fatalf("%s: template error", path)
		}
		common := []string{`<html lang="en-US" class="no-js" data-color-scheme="light">`, `<a class="skip-link screen-reader-text" href="#main">`,
			`id='classic-style-css'`, `<style id='classic-style-inline-css'>`, `--cl-accent-light:#2563eb;`, `<header id="masthead" class="site-header">`,
			`<main id="main" class="site-main"`, `<footer id="colophon" class="site-footer">`, "wp-theme-classic", "classic-layout-list",
			"classic-fonts-editorial", "classic-width-normal", "classic-header-inline", "classic-scheme-light", `<meta name="theme-color" content="#ffffff" />`,
			`<p class="footer-text">© ` + year + ` Test Site</p>`, "Proudly powered by", `class="footer-widgets"`, `id="classic-navigation-js" defer`,
			`<button type="button" class="menu-toggle" aria-controls="site-navigation-menu" aria-expanded="false">`}
		for _, want := range append(common, wants...) {
			if !strings.Contains(body, want) {
				t.Errorf("%s misses %q", path, want)
			}
		}
		for _, m := range assetRe.FindAllStringSubmatch(body, -1) {
			assets[strings.TrimPrefix(strings.ReplaceAll(m[1], "&amp;", "&"), base)] = true
		}
		return body
	}
	bodyClass := func(html string) string {
		m := regexp.MustCompile(`<body class="([^"]*)"`).FindStringSubmatch(html)
		if m == nil {
			return ""
		}
		return " " + m[1] + " "
	}

	home := page(anon, "/", http.StatusOK, `<h1 class="site-title"><a href=`, `aria-current="page">Test Site</a></h1>`, `<div class="post-list post-list--list">`,
		`class="post-card__media"`, `fetchpriority="high"`, `class="sticky-badge"`, "is-sticky", "format-aside", "Just a short note.",
		`class="post-password-form"`, `rel="category tag">Uncategorized</a>`, "min read", `class="more-link"`, "hfeed")
	if !strings.Contains(home, `class="page_item page-item-`) || strings.Contains(home, ">An aside</a></h2>") {
		t.Error("no page menu fallback, or the aside shows a title in the list")
	}
	if bc := bodyClass(home); strings.Contains(bc, " singular ") || strings.Contains(bc, " has-sidebar ") || !strings.Contains(bc, " home blog ") {
		t.Errorf("home body classes: %s", bc)
	}
	hello := regexp.MustCompile(`<h2 class="entry-title"><a href="([^"]+)" rel="bookmark">Hello world!</a></h2>`).FindStringSubmatch(home)
	if hello == nil {
		t.Fatal("no Hello world! card on the home page")
	}
	postPath := strings.TrimPrefix(hello[1], base)

	// Single post with featured image, caption, tags, author bio, post navigation and threaded comments.
	single := page(anon, postPath, http.StatusOK, `<h1 class="entry-title">Hello world!</h1>`, "singular", "single-post", `<figure class="entry-thumbnail alignwide">`,
		`<figcaption class="wp-caption-text">The lake at dawn</figcaption>`, `rel="tag">Slow</a>`, `<aside class="author-bio" aria-label="About the author">`,
		"Writes about slow things.", `<nav class="navigation post-navigation" aria-label="Posts">`, `rel="prev"`, `<section id="comments" class="comments-area`,
		`<ol class="comment-list">`, `avatar-48`, "One comment", `class="comment-reply-title">Leave a comment`, `id="commentform"`, `comment-reply-js`,
		`<span class="byline">By <span class="author vcard">`)
	if !strings.Contains(single, `<p class="site-title"><a href=`) {
		t.Error("the site title is not a paragraph on single posts")
	}
	locked := regexp.MustCompile(`<a href="([^"]+)" rel="bookmark">Protected: Locked away</a>`).FindStringSubmatch(home)
	if locked == nil {
		t.Fatal("no protected post card")
	}
	page(anon, strings.TrimPrefix(locked[1], base), http.StatusOK, `class="post-password-form"`, "post-password-required")

	sample := regexp.MustCompile(`<a href="([^"]+)">Sample Page</a>`).FindStringSubmatch(home)
	if sample == nil {
		t.Fatal("no Sample Page link in the fallback menu")
	}
	samplePage := page(anon, strings.TrimPrefix(sample[1], base), http.StatusOK, `<h1 class="entry-title">Sample Page</h1>`, "page-template-default",
		`<article id="post-`, "entry--page", `current_page_item current-menu-item`, `aria-current="page">Sample Page</a>`)
	if strings.Contains(samplePage, "entry-meta") || strings.Contains(samplePage, "author-bio") {
		t.Error("pages show post meta")
	}

	page(anon, "/category/uncategorized/", http.StatusOK, `<p class="archive-header__label">Category</p>`, `<h1 class="page-title">Uncategorized</h1>`,
		"archive category category-uncategorized", "Hello world!")
	page(anon, "/tag/slow/", http.StatusOK, `<p class="archive-header__label">Tag</p>`, `<h1 class="page-title">Slow</h1>`, "archive tag tag-slow", "1 post")
	page(anon, "/author/admin/", http.StatusOK, `<p class="archive-header__label">Author</p>`, `<div class="archive-header__avatar">`, "archive author author-admin")
	page(anon, "/"+year+"/", http.StatusOK, `<p class="archive-header__label">Year</p>`, `<h1 class="page-title">`+year+`</h1>`, "archive date")
	page(anon, "/?s=hello", http.StatusOK, `<p class="search-result-count">1 result</p>`, `<span class="search-term">hello</span>`, "search search-results",
		`id="search-field-header"`, `Hello world!`)
	page(anon, "/?s=zzzqqq", http.StatusOK, "0 results", "Nothing matched your search.", "search-no-results")
	page(anon, "/no-such-page-at-all/", http.StatusNotFound, `<h1 class="page-title">Page not found</h1>`, "error404", `id="search-field-404"`)

	// Menus: primary with a sub-menu, footer and social links (icons with screen reader labels).
	var primaryID, parentID, footerID, socialID int64
	steps := []struct {
		sql  string
		args []any
		dest *int64
	}{
		{`INSERT INTO menus (slug, name, location) VALUES ('main', 'Main', 'primary') RETURNING id`, nil, &primaryID},
		{`INSERT INTO menu_items (menu_id, kind, object, url, label) VALUES ($1, 'custom', 'custom', '/', 'Start') RETURNING id`, []any{&primaryID}, &parentID},
		{`INSERT INTO menu_items (menu_id, parent_id, sort_order, kind, object, url, label) VALUES ($1, $2, 1, 'custom', 'custom', '/sample-page/', 'Child') RETURNING id`, []any{&primaryID, &parentID}, new(int64)},
		{`INSERT INTO menus (slug, name, location) VALUES ('foot', 'Foot', 'footer') RETURNING id`, nil, &footerID},
		{`INSERT INTO menu_items (menu_id, kind, object, url, label) VALUES ($1, 'custom', 'custom', '/imprint/', 'Imprint') RETURNING id`, []any{&footerID}, new(int64)},
		{`INSERT INTO menus (slug, name, location) VALUES ('social-links', 'Social', 'social') RETURNING id`, nil, &socialID},
		{`INSERT INTO menu_items (menu_id, kind, object, url, label) VALUES ($1, 'custom', 'custom', 'https://github.com/wordpress', 'GitHub') RETURNING id`, []any{&socialID}, new(int64)},
		{`INSERT INTO menu_items (menu_id, sort_order, kind, object, url, label) VALUES ($1, 1, 'custom', 'custom', 'https://x.com/wordpress', 'X') RETURNING id`, []any{&socialID}, new(int64)},
	}
	for _, st := range steps {
		args := make([]any, len(st.args))
		for i, a := range st.args {
			args[i] = *(a.(*int64))
		}
		if err := db.QueryRow(ctx, st.sql, args...).Scan(st.dest); err != nil {
			t.Fatalf("%s: %v", st.sql, err)
		}
	}
	s := page(anon, "/?s=menus", http.StatusOK, `<ul id="menu-primary-menu" class="primary-menu">`, `menu-item-has-children`, `<ul class="sub-menu">`,
		`<nav class="footer-navigation" aria-label="Footer menu">`, `>Imprint</a>`, `<nav class="social-navigation" aria-label="Social links">`,
		`<ul class="social-links">`, `<span class="screen-reader-text">GitHub</span>`, `M18.244 2.25h3.308`, `<span class="screen-reader-text">X</span>`)
	if strings.Contains(s, "page_item page-item-") && !strings.Contains(s, "Start</a>") {
		t.Error("the page fallback replaced the primary menu")
	}

	// The Customizer: Classic's sections and controls, validation, preview and publish.
	_, body, _ = get(t, c, base+"/wp-admin/customize")
	for _, want := range []string{`data-section="classic_typography"`, `data-section="classic_layout"`, `data-section="classic_posts"`, `data-section="classic_footer"`,
		`id="_customize-input-show_title" type="checkbox" value="1" data-setting="mod:show_title" checked="checked"`, `id="_customize-input-font_pairing"`,
		`id="_customize-input-excerpt_length" type="number" min="10" max="120"`, `id="_customize-input-footer_text" rows="4" data-setting="mod:footer_text">© {year} {site}</textarea>`,
		`data-default-color="#2563eb" data-setting="mod:accent_color"`, `"mod:accent_color"`, `postMessage = [`} {
		if !strings.Contains(body, want) {
			t.Errorf("customize screen misses %q", want)
		}
	}
	nonce, uuid := czNonceRe.FindStringSubmatch(body), czUUIDRe.FindStringSubmatch(body)
	if nonce == nil || uuid == nil {
		t.Fatal("no nonce or changeset id on the customize screen")
	}
	send := func(action string, values map[string]any) (int, string) {
		b, _ := json.Marshal(values)
		code, body, _ := post(t, c, base+"/wp-admin/customize", url.Values{"action": {action}, "uuid": {uuid[1]}, "_wpnonce": {nonce[1]}, "customized": {string(b)}})
		return code, body
	}
	for _, bad := range []map[string]any{{"mod:blog_layout": "carousel"}, {"mod:accent_color": "red"}, {"mod:excerpt_length": "many"}} {
		if code, _ := send("publish", bad); code != http.StatusBadRequest {
			t.Errorf("invalid value %v accepted: %d", bad, code)
		}
	}

	if code, b := send("preview", map[string]any{"mod:header_layout": "centered", "mod:show_tagline": "0"}); code != http.StatusOK {
		t.Fatalf("preview: %d %s", code, b)
	}
	_, prev, _ := get(t, c, base+"/?"+czParam+"="+uuid[1])
	if bc := bodyClass(prev); !strings.Contains(bc, " classic-header-centered ") || !strings.Contains(bc, " classic-hide-tagline ") ||
		!strings.Contains(prev, `id="classic-customize-preview-js" defer`) {
		t.Errorf("preview misses the changes: %s", bc)
	}
	assets["/wp-content/themes/classic/js/customize-preview.js?ver="+classicVersion] = true
	if _, live, _ := get(t, anon, base+"/?live=1"); strings.Contains(live, "classic-header-centered") || strings.Contains(live, "customize-preview.js") {
		t.Error("preview values leaked to the live site")
	}

	if code, b := send("publish", map[string]any{"mod:accent_color": "#C2410C", "mod:blog_layout": "grid", "mod:sidebar": "left", "mod:color_scheme": "auto",
		"mod:font_pairing": "modern", "mod:dark_toggle": "1", "mod:show_credit": "0", "mod:show_author_bio": "0", "mod:excerpt_length": 500,
		"mod:footer_text": `Made in {year} by <strong>{site}</strong><script>alert(1)</script> <a href="javascript:alert(1)">x</a>`}); code != http.StatusOK {
		t.Fatalf("publish: %d %s", code, b)
	}
	var raw []byte
	if err := db.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'theme_mods_classic'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var mods map[string]any
	_ = json.Unmarshal(raw, &mods)
	if mods["accent_color"] != "#c2410c" || mods["dark_toggle"] != true || mods["show_credit"] != false || mods["excerpt_length"] != 120.0 ||
		strings.Contains(mods["footer_text"].(string), "<script") {
		t.Errorf("stored theme mods: %s", raw)
	}

	_, after, _ := get(t, anon, base+"/?after=1")
	bc := bodyClass(after)
	for _, want := range []string{" classic-layout-grid ", " classic-sidebar-left ", " has-sidebar ", " classic-fonts-modern ", " classic-scheme-auto ", " has-scheme-toggle "} {
		if !strings.Contains(bc, want) {
			t.Errorf("published body classes miss %q: %s", want, bc)
		}
	}
	for _, want := range []string{`data-color-scheme="auto"`, "--cl-accent-light:#c2410c;", `<div class="post-list post-list--grid">`,
		`<aside id="secondary" class="sidebar widget-area" aria-label="Sidebar">`, "Sidebar hello", `class="scheme-toggle" aria-pressed="false"`,
		`id="classic-color-scheme-js"`, `media="(prefers-color-scheme: dark)"`, `<p class="footer-text">Made in ` + year + ` by <strong>Test Site</strong> x</p>`} {
		if !strings.Contains(after, want) {
			t.Errorf("published front page misses %q", want)
		}
	}
	for _, bad := range []string{"alert(1)", "Proudly powered by", "javascript:"} {
		if strings.Contains(after, bad) {
			t.Errorf("published front page contains %q", bad)
		}
	}
	if _, single, _ := get(t, anon, base+postPath+"?after=1"); strings.Contains(single, "author-bio") {
		t.Error("the author bio shows although it is switched off")
	}
	if _, minimal := send("publish", map[string]any{"mod:blog_layout": "minimal", "mod:sidebar": "none"}); !strings.Contains(minimal, `"success":true`) {
		t.Fatalf("publish minimal: %s", minimal)
	}
	if _, m, _ := get(t, anon, base+"/?minimal=1"); !strings.Contains(m, `<div class="post-list post-list--minimal">`) || !strings.Contains(m, `post-minimal"`) ||
		strings.Contains(m, "post-card__media") || strings.Contains(m, `id="secondary"`) {
		t.Error("minimal layout")
	}

	// Every theme asset the pages reference, the editor style and the screenshot are served.
	for _, extra := range []string{"/wp-content/themes/classic/screenshot.png", "/wp-content/themes/classic/editor-style.css",
		"/wp-content/themes/classic/js/navigation.js"} {
		assets[extra] = true
	}
	if len(assets) < 4 {
		t.Errorf("found only %d theme assets", len(assets))
	}
	for a := range assets {
		if code, _, _ := get(t, anon, base+a); code != http.StatusOK {
			t.Errorf("asset %s: %d", a, code)
		}
	}
}
