package server

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestTwentySeventeen(t *testing.T) {
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

	// Activate the theme from the Themes screen.
	_, body, _ = get(t, c, base+"/wp-admin/themes")
	m := regexp.MustCompile(`action=activate&(?:amp;)?stylesheet=twentyseventeen&(?:amp;)?_wpnonce=([0-9a-zA-Z]+)`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("Twenty Seventeen is not listed on the Themes screen")
	}
	if code, _, loc := get(t, c, base+"/wp-admin/themes?action=activate&stylesheet=twentyseventeen&_wpnonce="+m[1]); code != http.StatusFound || !strings.Contains(loc, "activated=true") {
		t.Fatalf("activate: %d %s", code, loc)
	}
	_, body, _ = get(t, c, base+"/wp-admin/themes")
	if !strings.Contains(body, "Twenty Seventeen") {
		t.Fatal("themes screen misses Twenty Seventeen")
	}

	anon := &http.Client{CheckRedirect: c.CheckRedirect}
	assets := map[string]bool{}
	assetRe := regexp.MustCompile(`/wp-content/themes/twentyseventeen/[^"'\s)?]+`)
	page := func(cl *http.Client, u string, wantCode int, wants ...string) string {
		t.Helper()
		code, body, _ := get(t, cl, base+u)
		if code != wantCode {
			t.Errorf("%s: status %d, want %d", u, code, wantCode)
		}
		if strings.Contains(body, "Template error") || strings.Contains(body, "<no value>") {
			t.Errorf("%s: template error", u)
		}
		for _, w := range append([]string{`id="masthead"`, `id='twentyseventeen-style-css'`, `id="twentyseventeen-global-js-extra"`,
			`var twentyseventeenScreenReaderText = {"quote":"\` + "u003Csvg class=" + `\"icon icon-quote-right\"`,
			`/assets/images/header.jpg" width="2000" height="1200" alt="" decoding="async" fetchpriority="high"`,
			`<symbol id="icon-arrow-right"`, `id="jquery-scrollto-js" defer`, `class="site-info"`, `colors-light`, `has-header-image`}, wants...) {
			if !strings.Contains(body, w) {
				t.Errorf("%s misses %q", u, w)
			}
		}
		if dir := os.Getenv("WPGO_TT17_DUMP"); dir != "" {
			_ = os.WriteFile(filepath.Join(dir, strings.NewReplacer("/", "_", "?", "_").Replace(u)+".html"), []byte(body), 0o644)
		}
		for _, a := range assetRe.FindAllString(body, -1) {
			assets[a] = true
		}
		return body
	}

	home := page(anon, "/", http.StatusOK, `<body class="home blog wp-embed-responsive wp-theme-twentyseventeen hfeed has-header-image has-sidebar colors-light">`,
		`<h1 class="site-title"><a href="`, `aria-current="page"`, `<h2 class="page-title">Posts</h2>`, `<h3 class="entry-title"><a href="`,
		"Hello world!", `<aside id="secondary" class="widget-area" aria-label="Blog Sidebar">`, `class="menu-scroll-down"`)
	link := regexp.MustCompile(`<h3 class="entry-title"><a href="([^"]+)" rel="bookmark">Hello world!</a></h3>`).FindStringSubmatch(home)
	if link == nil {
		t.Fatal("no Hello world! link on the home page")
	}
	single, _ := url.Parse(link[1])
	page(anon, single.Path, http.StatusOK, `single-post`, `has-sidebar`, `<h1 class="entry-title">Hello world!</h1>`,
		`<span class="posted-on"><span class="screen-reader-text">Posted on</span>`, `<span class="byline"> by <span class="author vcard">`,
		`<div id="comments" class="comments-area">`, `One Reply to &ldquo;Hello world!&rdquo;`, `class='avatar avatar-100 photo'`,
		`id="commentform"`, `<h3 id="reply-title" class="comment-reply-title">`, `<p class="site-title">`)
	page(anon, "/sample-page/", http.StatusOK, `page-template-default page page-id-`, `page-two-column`,
		`<h1 class="entry-title">Sample Page</h1>`)
	page(anon, "/category/uncategorized/", http.StatusOK, `archive category category-uncategorized`, `page-two-column`,
		`<h1 class="page-title">Category: <span>Uncategorized</span></h1>`, `<h2 class="entry-title"><a href="`)
	page(anon, "/?s=hello", http.StatusOK, `search search-results`, `Search Results for: <span>hello</span>`,
		`<div class="entry-summary">`)
	page(anon, "/?s=zzzzqqq", http.StatusOK, `search-no-results`, `Nothing Found`, `id="search-form-1"`)
	page(anon, "/no-such-page/", http.StatusNotFound, `error404`, `Oops! That page can&rsquo;t be found.`, `<button type="submit" class="search-submit"><svg class="icon icon-search"`)
	// Logged in: admin bar and edit links.
	page(c, "/", http.StatusOK, `logged-in admin-bar`, `<span class="edit-link"><a class="post-edit-link"`)

	// Top and social menus: navigation-top with the dropdown icon, navigation.js, social icons in the footer.
	db := getPool(t)
	ctx := context.Background()
	var topID, socialID, parentID int64
	if err := db.QueryRow(ctx, `INSERT INTO menus (slug, name, location) VALUES ('main', 'Main', 'top') RETURNING id`).Scan(&topID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO menu_items (menu_id, kind, object, url, label) VALUES ($1, 'custom', 'custom', '/', 'Home') RETURNING id`, topID).Scan(&parentID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO menu_items (menu_id, parent_id, sort_order, kind, object, url, label) VALUES ($1, $2, 1, 'custom', 'custom', '/sample-page/', 'Sample')`, topID, parentID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO menus (slug, name, location) VALUES ('social-links', 'Social', 'social') RETURNING id`).Scan(&socialID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO menu_items (menu_id, kind, object, url, label) VALUES ($1, 'custom', 'custom', 'https://github.com/wordpress', 'GitHub')`, socialID); err != nil {
		t.Fatal(err)
	}
	page(c, "/?s=menus", http.StatusOK, `<div class="navigation-top">`, `<nav id="site-navigation" class="main-navigation" aria-label="Top Menu">`,
		`<div class="menu-main-container"><ul id="top-menu" class="menu">`, `menu-item-has-children`, `Home<svg class="icon icon-angle-down"`,
		`<ul class="sub-menu">`, `id="twentyseventeen-navigation-js" defer`, `"expand":"Expand child menu"`,
		`<nav class="social-navigation" aria-label="Footer Social Links Menu">`, `<ul id="menu-social-links" class="social-links-menu">`,
		`<span class="screen-reader-text">GitHub</span><svg class="icon icon-github"`)

	// Fonts referenced by the font stylesheet.
	_, css, _ := get(t, anon, base+"/wp-content/themes/twentyseventeen/assets/fonts/font-libre-franklin.css")
	for _, f := range regexp.MustCompile(`url\('\./([^'?]+)`).FindAllStringSubmatch(css, -1) {
		assets[path.Join("/wp-content/themes/twentyseventeen/assets/fonts", f[1])] = true
	}
	assets["/wp-content/themes/twentyseventeen/assets/css/editor-style.css"] = true
	assets["/wp-content/themes/twentyseventeen/screenshot.png"] = true
	if len(assets) < 10 {
		t.Errorf("only %d theme assets referenced", len(assets))
	}
	for a := range assets {
		if code, _, _ := get(t, anon, base+a); code != http.StatusOK {
			t.Errorf("asset %s: %d", a, code)
		}
	}
}
