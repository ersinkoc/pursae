package server

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestTwentyTwentyTheme(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL

	code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Test Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	})
	if code != http.StatusOK || !strings.Contains(body, "Success!") {
		t.Fatalf("install failed: %d", code)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login failed: %d", code)
	}

	// Activate Twenty Twenty from the Themes screen.
	_, body, _ = get(t, c, base+"/wp-admin/themes")
	m := regexp.MustCompile(`action=activate(?:&amp;|&)stylesheet=twentytwenty(?:&amp;|&)_wpnonce=([0-9a-f]+)`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Twenty Twenty activate link on the Themes screen")
	}
	if code, _, loc := get(t, c, base+"/wp-admin/themes?action=activate&stylesheet=twentytwenty&_wpnonce="+m[1]); code != http.StatusFound || !strings.Contains(loc, "activated=true") {
		t.Fatalf("activate: %d %s", code, loc)
	}
	_, body, _ = get(t, c, base+"/wp-admin/themes")
	if !strings.Contains(body, "Twenty Twenty") {
		t.Fatal("themes screen misses Twenty Twenty")
	}

	assets := map[string]bool{}
	assetRe := regexp.MustCompile(`(?:href|src)=['"]((?:` + regexp.QuoteMeta(base) + `)?/wp-content/themes/twentytwenty/[^'"]+)['"]`)
	page := func(c *http.Client, path string, wantCode int, wants ...string) string {
		t.Helper()
		code, body, _ := get(t, c, base+path)
		if dir := os.Getenv("WPGO_TT20_DUMP"); dir != "" {
			name := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(path, "-"), "-")
			_ = os.WriteFile(filepath.Join(dir, "page-"+name+".html"), []byte(body), 0o644)
		}
		if code != wantCode {
			t.Errorf("%s: status %d, want %d", path, code, wantCode)
		}
		if strings.Contains(body, "Template error") {
			t.Fatalf("%s: template error", path)
		}
		common := []string{`id="site-header"`, `id='twentytwenty-style-css'`, `id="twentytwenty-style-inline-css"`, `color: #cd2653;`,
			`id='twentytwenty-print-style-css'`, `id="twentytwenty-js" defer`, `class="search-modal cover-modal header-footer-group"`,
			`class="menu-modal cover-modal header-footer-group"`, `id="site-footer"`, `<a class="skip-link screen-reader-text" href="#site-content">`,
			`<main id="site-content">`, `enable-search-modal`, `wp-theme-twentytwenty`}
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

	home := page(c, "/", http.StatusOK, `<h1 class="site-title"><a href=`, `aria-current="page" rel="home">Test Site</a></h1>`,
		`<body class="home blog logged-in admin-bar`, `class="entry-title heading-size-1"`, "Hello world!",
		`<ul class="primary-menu reset-list-style">`, `class="page_item page-item-`, `Sample Page</a>`,
		`<div class="post-meta-wrapper post-meta-single post-meta-single-top">`, `class="footer-nav-widgets-wrapper header-footer-group"`,
		`<div class="widget widget_block`, "footer-top-visible", "has-no-pagination", "showing-comments", "show-avatars")
	if bc := regexp.MustCompile(`<body class="([^"]*)"`).FindStringSubmatch(home); bc == nil || strings.Contains(bc[1], "singular") {
		t.Error("home must not have the singular body class")
	}
	post := regexp.MustCompile(`<h2 class="entry-title heading-size-1"><a href="([^"]+)">Hello world!</a></h2>`).FindStringSubmatch(home)
	if post == nil {
		t.Fatal("no Hello world! link on the home page")
	}
	postPath := strings.TrimPrefix(post[1], base)

	// The single post, as the admin and as a visitor.
	page(c, postPath, http.StatusOK, `<h1 class="entry-title">Hello world!</h1>`, "singular", "missing-post-thumbnail", "showing-comments",
		`class="entry-header has-text-align-center header-footer-group"`, `<div class="comments" id="comments">`,
		`<div class="comments-inner section-inner thin max-percentage">`, `avatar-120`, `<span class="fn">A WordPress Go Commenter</span>`,
		`class="comment-form section-inner thin max-percentage"`, `id="reply-title" class="comment-reply-title"`,
		`class="comment-reply-link"`, `post-edit meta-wrapper`, `<div class="site-title faux-heading">`, `comment-reply-js`)
	anon := &http.Client{CheckRedirect: c.CheckRedirect}
	visitor := page(anon, postPath, http.StatusOK, `<form action="/wp-comments-post" method="post" id="commentform"`, `id="author"`)
	if strings.Contains(visitor, "post-edit meta-wrapper") {
		t.Error("visitors must not see the edit link")
	}

	// The Sample Page.
	sample := regexp.MustCompile(`<a href="([^"]+)">Sample Page</a>`).FindStringSubmatch(home)
	if sample == nil {
		t.Fatal("no Sample Page link in the header menu")
	}
	samplePage := page(anon, strings.TrimPrefix(sample[1], base), http.StatusOK, `<h1 class="entry-title">Sample Page</h1>`, "page-template-default",
		"singular", "not-showing-comments", `current_page_item current-menu-item`, `aria-current="page">Sample Page</a>`)
	if strings.Contains(samplePage, `class="comments-wrapper`) || strings.Contains(samplePage, "post-meta-single-top") {
		t.Error("the Sample Page must have no comments wrapper or post meta")
	}

	page(anon, "/category/uncategorized/", http.StatusOK, `<h1 class="archive-title"><span class="color-accent">Category:</span> <span>Uncategorized</span></h1>`,
		"archive category category-uncategorized", "Hello world!")
	page(anon, "/?s=hello", http.StatusOK, `<span class="color-accent">Search:</span> &ldquo;hello&rdquo;`, "We found 1 result for your search.",
		"search search-results", `id="search-form-1"`)
	page(anon, "/?s=zzzqqq", http.StatusOK, "We could not find any results", `<div class="no-search-results-form section-inner thin">`,
		`aria-label="search again"`, `id="search-form-2"`, "search-no-results")
	page(anon, "/no-such-page-at-all/", http.StatusNotFound, `<h1 class="entry-title">Page Not Found</h1>`, "error404", `aria-label="404 not found"`,
		"not-showing-comments")

	// Every theme asset the pages reference, plus the fonts the stylesheets load, is served.
	for _, extra := range []string{"/wp-content/themes/twentytwenty/assets/fonts/inter/Inter-upright-var.woff2",
		"/wp-content/themes/twentytwenty/assets/fonts/inter/Inter-italic-var.woff2", "/wp-content/themes/twentytwenty/screenshot.png",
		"/wp-content/themes/twentytwenty/assets/css/editor-style-block.css", "/wp-content/themes/twentytwenty/assets/css/editor-style-classic.css"} {
		assets[extra] = true
	}
	if len(assets) < 5 {
		t.Errorf("found only %d theme assets", len(assets))
	}
	for a := range assets {
		if code, _, _ := get(t, anon, base+a); code != http.StatusOK {
			t.Errorf("asset %s: %d", a, code)
		}
	}
}
