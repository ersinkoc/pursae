package server

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestTwentyTwentyOne installs a site, adds primary and footer menus, activates Twenty Twenty-One and
// checks the main templates and every theme asset they reference.
func TestTwentyTwentyOne(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"TT1 Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK || !strings.Contains(body, "Success!") {
		t.Fatalf("install failed: %d", code)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login failed: %d", code)
	}

	// Menus: primary with a sub-menu, footer with a social link.
	ctx := context.Background()
	db := getPool(t)
	var pageID, postID int64
	if err := db.QueryRow(ctx, `SELECT id FROM posts WHERE title = 'Sample Page'`).Scan(&pageID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT id FROM posts WHERE title = 'Hello world!'`).Scan(&postID); err != nil {
		t.Fatal(err)
	}
	var primary, footer, parent int64
	_ = db.QueryRow(ctx, `INSERT INTO menus (slug, name, location) VALUES ('main', 'Main', 'primary') RETURNING id`).Scan(&primary)
	_ = db.QueryRow(ctx, `INSERT INTO menus (slug, name, location) VALUES ('links', 'Links', 'footer') RETURNING id`).Scan(&footer)
	if err := db.QueryRow(ctx, `INSERT INTO menu_items (menu_id, sort_order, kind, object, url, label) VALUES ($1, 1, 'custom', 'custom', $2, 'Home') RETURNING id`,
		primary, base+"/").Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO menu_items (menu_id, parent_id, sort_order, kind, object, url, label) VALUES ($1, $2, 2, 'custom', 'custom', 'https://example.org/about', 'About')`, primary, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO menu_items (menu_id, sort_order, kind, object, url, label) VALUES ($1, 1, 'custom', 'custom', 'https://github.com/wordpress', 'GitHub')`, footer); err != nil {
		t.Fatal(err)
	}

	// Two more posts (a long one for the excerpt link, a sticky aside) and one post per page for pagination.
	long := strings.Repeat("lorem ipsum ", 40)
	if _, err := db.Exec(ctx, `INSERT INTO posts (type, author_id, slug, title, content, status, published_at, format, sticky)
		VALUES ('post', 1, 'long-read', 'Long read', $1, 'publish', now() - interval '1 hour', 'standard', false),
		       ('post', 1, 'an-aside', 'An aside', '<p>Just a quick note.</p>', 'publish', now() - interval '2 hours', 'aside', true)`, long); err != nil {
		t.Fatal(err)
	}
	_, body, _ := get(t, c, base+"/wp-admin/options-reading")
	if code, _, _ := post(t, c, base+"/wp-admin/options", url.Values{"option_page": {"reading"}, "_wpnonce": {formNonce(t, body)},
		"show_on_front": {"posts"}, "posts_per_page": {"1"}, "posts_per_rss": {"10"}, "rss_use_excerpt": {"0"}, "blog_public": {"1"}}); code != http.StatusFound {
		t.Fatalf("reading settings: %d", code)
	}

	// Activate the theme from the Themes screen.
	_, body, _ = get(t, c, base+"/wp-admin/themes")
	m := regexp.MustCompile(`stylesheet=twentytwentyone&(?:amp;)?_wpnonce=([^"&]+)`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("Twenty Twenty-One is not on the Themes screen")
	}
	if code, _, loc := get(t, c, base+"/wp-admin/themes?action=activate&stylesheet=twentytwentyone&_wpnonce="+m[1]); code != http.StatusFound || !strings.Contains(loc, "activated=true") {
		t.Fatalf("activate: %d %s", code, loc)
	}

	anon := &http.Client{CheckRedirect: c.CheckRedirect}
	follow := func(cl *http.Client, u string) (int, string) {
		for i := 0; i < 5; i++ {
			code, body, loc := get(t, cl, u)
			if code < 300 || code >= 400 || loc == "" {
				return code, body
			}
			if strings.HasPrefix(loc, "/") {
				loc = base + loc
			}
			u = loc
		}
		t.Fatalf("too many redirects for %s", u)
		return 0, ""
	}

	assetRe := regexp.MustCompile(`/wp-content/themes/twentytwentyone/[^"'?\s)]+`)
	assets := map[string]bool{}
	check := func(name string, cl *http.Client, path string, wantCode int, wants ...string) string {
		t.Helper()
		code, body := follow(cl, base+path)
		if code != wantCode {
			t.Errorf("%s: status %d, want %d", name, code, wantCode)
		}
		if strings.Contains(body, "Template error") || strings.Contains(body, "<no value>") {
			t.Errorf("%s: template error", name)
		}
		for _, w := range append([]string{`id="masthead"`, `id='twenty-twenty-one-style-css'`, `id="colophon"`, `class="site-info"`,
			"twenty-twenty-one-responsive-embeds-script-js", "document.body.classList.remove('no-js')"}, wants...) {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", name, w)
			}
		}
		if dir := os.Getenv("TT1_DUMP"); dir != "" {
			_ = os.WriteFile(dir+"/"+strings.ReplaceAll(name, " ", "-")+".html", []byte(body), 0o644)
		}
		for _, a := range assetRe.FindAllString(body, -1) {
			assets[a] = true
		}
		return body
	}

	home := check("home", anon, "/", http.StatusOK,
		`<body class="home blog wp-embed-responsive wp-theme-twentytwentyone is-light-theme no-js hfeed has-main-navigation`,
		`<h1 class="site-title">TT1 Site</h1>`, `class="site-header has-title-and-tagline has-menu"`,
		`<span class="posted-on">Published <time class="entry-date published updated"`,
		`id="primary-mobile-menu"`, `<ul id="primary-menu-list" class="menu-wrapper">`, `class="sub-menu-toggle"`, `<ul class="sub-menu">`,
		`<ul class="footer-navigation-wrapper">`, `<svg class="svg-icon" width="24" height="24"`, `<span class="screen-reader-text">GitHub</span>`,
		"twenty-twenty-one-primary-navigation-script-js", `<aside class="widget-area">`, "Proudly powered by")
	if strings.Contains(home, "emoji") {
		t.Error("home: emoji script printed")
	}
	check("home sticky aside", anon, "/", http.StatusOK, "format-aside", "sticky", "<p>Featured post</p>", "<p>Just a quick note.</p>",
		`class="next page-numbers"`, `<span aria-current="page" class="page-numbers current">Page 1</span>`, `<span class="nav-next-text">Older <span class="nav-short">posts</span></span>`)
	check("home page 2", anon, "/page/2/", http.StatusOK, "paged-2", "Long read", `&hellip; <a class="more-link" href=`,
		`Continue reading <span class="screen-reader-text">Long read</span>`, `class="prev page-numbers"`, `<p class="site-title"><a href=`)
	check("home page 3", anon, "/page/3/", http.StatusOK, "format-aside", `<span class="page-numbers dots">&hellip;</span>`)
	postPath := "/?p=" + itoa64(postID)

	check("single", anon, postPath, http.StatusOK,
		"singular", `<h1 class="entry-title">Hello world!</h1>`, `<div class="posted-by">`, `<span class="byline">By <a href=`,
		`class="comments-area default-max-width show-avatars"`, `id="commentform"`, "Leave a comment", `rows="5"`, "1 comment",
		`avatar-60`, `<p class="site-title"><a href=`)
	check("single logged in", c, postPath, http.StatusOK, "Edit <span class=\"screen-reader-text\">Hello world!</span>", "wpadminbar")
	check("page", anon, "/?page_id="+itoa64(pageID), http.StatusOK,
		"page-template-default", `<h1 class="entry-title">Sample Page</h1>`, `class="entry-header alignwide"`)
	check("category", anon, "/category/uncategorized/", http.StatusOK,
		"archive category", `Category: <span>Uncategorized</span>`, `<header class="page-header alignwide">`,
		`<h2 class="entry-title default-max-width"><a href=`, "Hello world!</a></h2>", `<div class="post-taxonomies"><span class="cat-links">Categorized as <a href=`)
	check("search", anon, "/?s=hello", http.StatusOK,
		"search-results", `Results for &quot;<span class="page-description search-term">hello</span>&quot;`, "We found 1 result for your search.")
	check("search none", anon, "/?s=zzzqqq", http.StatusOK, "search-no-results", "Sorry, but nothing matched", `id="search-form-1"`)
	check("404", anon, "/no-such-page-at-all/", http.StatusNotFound, "error404", "Nothing here", `class="search-form"`)

	assets["/wp-content/themes/twentytwentyone/screenshot.png"] = true
	for _, f := range []string{"assets/css/style-editor.css", "assets/js/primary-navigation.js", "assets/js/responsive-embeds.js", "assets/css/print.css"} {
		assets["/wp-content/themes/twentytwentyone/"+f] = true
	}
	for a := range assets {
		if code, _, _ := get(t, anon, base+a); code != http.StatusOK {
			t.Errorf("asset %s: %d", a, code)
		}
	}
	if len(assets) < 6 {
		t.Errorf("only %d theme assets referenced", len(assets))
	}
}
