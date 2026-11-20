package server

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// A static site in the shape of the enerjiosgb design: body > div.shell > [skip link, header, main,
// footer, to-top button], script after the shell; the header marks the current page's link
// (classes + aria-current) and the menu button of the page's section.
func siteTestPage(cur, title, body string) string {
	link := func(href, label string) string {
		if href == cur {
			return `<a href="` + href + `" class="nav on" aria-current="page">` + label + `</a>`
		}
		return `<a href="` + href + `" class="nav off">` + label + `</a>`
	}
	btn := "nav off"
	if strings.HasPrefix(cur, "hizmet/") || strings.HasPrefix(cur, "../hizmet/") {
		btn = "nav on"
	}
	up := ""
	if strings.Contains(cur, "/") {
		up = "../"
	}
	rel := func(h string) string { return up + h }
	return `<!doctype html><html lang="tr" class="no-js" data-theme="light"><head><meta charset="utf-8"><title>` + title + `</title>
<meta name="description" content="` + title + ` description">
<link rel="stylesheet" href="` + rel("css/site.css?v=3") + `">
</head><body class="site" data-x="1">
<div class="shell flex">
<a href="#c" class="sr-only">Skip</a>
<header id="site-header"><nav><ul><li>` + strings.ReplaceAll(link("index.html", "Home"), `"index.html"`, `"`+rel("index.html")+`"`) +
		`</li><li><button type="button" class="` + btn + `" aria-controls="menu-services">Services</button></li><li>` +
		strings.ReplaceAll(link("iletisim.html", "Contact"), `"iletisim.html"`, `"`+rel("iletisim.html")+`"`) + `</li></ul>
<div id="menu-services"><a href="` + rel("hizmet/1.html") + `">One</a><a href="` + rel("hizmet/2.html") + `">Two</a></div></nav></header>
<main id="c" class="flex-1">
` + body + `
</main>
<footer class="foot"><p>© 2026</p><a href="` + rel("iletisim.html?ref=footer#form") + `">Write</a></footer>
<button type="button" id="to-top">Up</button>
</div>
<script src="` + rel("js/main.js") + `" defer></script>
</body></html>`
}

func TestMCPImportSite(t *testing.T) {
	ts, c, srv := newTestServer(t, true)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, call := mcpTestAgent(t, base, c, "Site agent", nil)
	if r := call("activate_plugin", map[string]any{"slug": "golementor"}); r["isError"] == true {
		t.Fatalf("activate: %v", mcpText(r))
	}
	if r := call("activate_theme", map[string]any{"slug": wbHelloSlug}); r["isError"] == true {
		t.Fatalf("theme: %v", mcpText(r))
	}
	mcpStructured(t, "web_builder_kit_upload", call("web_builder_kit_upload", map[string]any{"kit": "site", "files": []any{
		map[string]any{"path": "css/site.css", "text": ".nav.on{color:red}"},
		map[string]any{"path": "js/main.js", "text": "document.documentElement.classList.remove('no-js')"},
	}}))
	pages := []any{
		map[string]any{"path": "index.html", "html": siteTestPage("index.html", "Home", `<section id="hero"><h1>Welcome</h1></section><section id="more"><h2>More</h2></section>`)},
		map[string]any{"path": "iletisim.html", "html": siteTestPage("iletisim.html", "Contact", `<section><h1>Contact</h1></section>`)},
		map[string]any{"path": "hizmet/1.html", "html": siteTestPage("../hizmet/1.html", "One", `<section><h1>One</h1><a href="2.html">next</a></section>`)},
		map[string]any{"path": "hizmet/2.html", "html": siteTestPage("../hizmet/2.html", "Two", `<section><h1>Two</h1><a href="../index.html">home</a></section>`)},
		map[string]any{"path": "404.html", "html": siteTestPage("404.html", "Not found", `<section><h1>Lost</h1></section>`)},
	}
	out := mcpStructured(t, "web_builder_import_site", call("web_builder_import_site", map[string]any{"kit": "site", "pages": pages, "site_name": "Demo"}))
	tpls, _ := out["templates"].([]any)
	if len(tpls) != 3 || len(asList(out["pages"])) != 4 || out["front_page"] == nil {
		t.Fatalf("import: %v", out)
	}
	for _, x := range tpls {
		m := x.(map[string]any)
		if m["type"] == "header" && m["active_links"] == nil {
			t.Errorf("the header learned no active links: %v", m)
		}
	}
	if w, ok := out["warnings"]; ok {
		t.Logf("warnings: %v", w)
	}
	fetch := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(b)
	}
	for _, x := range []struct{ path, on, off string }{
		{"/", `aria-current="page" class="nav on"[^>]*href="[^"]*/">Home`, `class="nav off"[^>]*href="[^"]*/iletisim/">Contact`},
		{"/iletisim/", `aria-current="page" class="nav on"[^>]*href="[^"]*/iletisim/">Contact`, `class="nav off"[^>]*href="[^"]*/">Home`},
		{"/hizmet/1/", `class="nav on" aria-controls="menu-services"[^>]*>Services`, `class="nav off"[^>]*href="[^"]*/">Home`},
	} {
		code, html := fetch(x.path)
		if code != 200 {
			t.Fatalf("%s: %d", x.path, code)
		}
		// The menu is bound to a Pursae menu (menus defaults on): binding attributes and the items' URLs.
		if !regexpFind(x.on, html) || !regexpFind(x.off, html) {
			t.Errorf("%s: active link not marked: %s", x.path, wbSnippet(html, "<nav>"))
		}
		// One shell around header, main and footer; skip link before the header, to-top after the footer.
		if strings.Count(html, `<div class="shell flex">`) != 1 {
			t.Errorf("%s: shell printed %d times", x.path, strings.Count(html, `<div class="shell flex">`))
		}
		i, j, k, l := strings.Index(html, `<div class="shell flex">`), strings.Index(html, `class="sr-only"`), strings.Index(html, `<main id="c"`), strings.Index(html, `id="to-top"`)
		if i < 0 || !(i < j && j < k && k < l) || !strings.Contains(html[l:], "</button></div>") {
			t.Errorf("%s: structure: %s", x.path, wbSnippet(html, `id="to-top"`))
		}
		if !strings.Contains(html, `<html lang="tr" class="no-js" data-theme="light">`) || !strings.Contains(html, `data-x="1"`) || !strings.Contains(html, ` site`) {
			t.Errorf("%s: html/body attributes: %.300s", x.path, html)
		}
	}
	// The code parts are worked out once per input (wbCodePartsMemo): the next responses, and a
	// response worked out without the memo, are the same page (bar the nonce).
	nonceAttr := regexp.MustCompile(`nonce="[^"]*"`)
	rendered := func(path string) string { // past the page cache
		t.Helper()
		if pc := srv.currentCache(); pc != nil {
			pc.PurgeAll()
		}
		_, body := fetch(path)
		return body
	}
	for _, path := range []string{"/", "/iletisim/", "/hizmet/1/", "/hizmet/2/"} {
		wbCodePartsMemo.purge()
		first := rendered(path)
		if wbCodePartsMemo.len() == 0 {
			t.Errorf("%s: nothing memoized", path)
		}
		second := rendered(path)
		wbCodePartsMemo.purge()
		plain := rendered(path)
		a, b, c := nonceAttr.ReplaceAllString(first, ""), nonceAttr.ReplaceAllString(second, ""), nonceAttr.ReplaceAllString(plain, "")
		if a != b || a != c {
			t.Errorf("%s: memoized code parts differ (%d / %d / %d bytes)", path, len(a), len(b), len(c))
		}
	}
	_, one := fetch("/hizmet/1/")
	if !strings.Contains(one, `href="/hizmet/2/"`) || !strings.Contains(one, `href="/iletisim/?ref=footer#form"`) {
		t.Errorf("links not mapped: %s", wbSnippet(one, "next"))
	}
	if code, _ := fetch("/hizmet/"); code != 404 {
		t.Errorf("the placeholder parent is visible: %d", code)
	}
	code, nf := fetch("/no-such-page/")
	if code != 404 || !strings.Contains(nf, "<h1>Lost</h1>") {
		t.Errorf("404 template: %d", code)
	}
	// Canvas layout (one file, no templates): the page's own header and footer in one shell, the
	// design's document attributes.
	canvas := mcpStructured(t, "web_builder_import_html", call("web_builder_import_html", map[string]any{"html": pages[1].(map[string]any)["html"],
		"kit": "site", "path": "iletisim.html", "title": "Canvas copy"}))
	cp, _ := canvas["page"].(map[string]any)
	resp, err := http.Get(strAny(cp["url"]))
	if err != nil {
		t.Fatal(err)
	}
	cb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if html := string(cb); strings.Count(html, `<div class="shell flex">`) != 1 || !strings.Contains(html, `<html lang="tr" class="no-js" data-theme="light">`) ||
		strings.Index(html, `id="site-header"`) > strings.Index(html, `id="to-top"`) {
		t.Errorf("canvas page: %s", wbSnippet(html, `<div class="shell`))
	}

	// Again: the same pages and templates are updated.
	again := mcpStructured(t, "web_builder_import_site", call("web_builder_import_site", map[string]any{"kit": "site", "pages": pages}))
	if len(asList(again["pages_created"])) != 0 || len(asList(again["pages_updated"])) != 4 {
		t.Errorf("re-import created pages: %v / %v", again["pages_created"], again["pages_updated"])
	}
	for i, x := range asList(again["templates"]) {
		if x.(map[string]any)["id"] != tpls[i].(map[string]any)["id"] {
			t.Errorf("re-import made a new template: %v", x)
		}
	}
}

func wbSnippet(s, at string) string {
	i := strings.Index(s, at)
	if i < 0 {
		return "(" + at + " missing)"
	}
	lo, hi := max(0, i-300), min(len(s), i+300)
	return s[lo:hi]
}

// The corpus shapes (landing/merge, umbral-site): a home page whose footer sits inside main, a bare
// 404 page, a stylesheet with root-relative font URLs and an inline module importing a file next to
// its page. The whole-site templates leave the pages without that part alone, the kit's files answer
// at the site's root, and the code pages allow blob: images.
func TestMCPImportSiteCorpusShapes(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, call := mcpTestAgent(t, base, c, "Site agent", nil)
	if r := call("activate_plugin", map[string]any{"slug": "golementor"}); r["isError"] == true {
		t.Fatalf("activate: %v", mcpText(r))
	}
	if r := call("activate_theme", map[string]any{"slug": wbHelloSlug}); r["isError"] == true {
		t.Fatalf("theme: %v", mcpText(r))
	}
	mcpStructured(t, "web_builder_kit_upload", call("web_builder_kit_upload", map[string]any{"kit": "corp", "files": []any{
		map[string]any{"path": "assets/style.css", "text": "@font-face{font-family:X;src:url(/assets/fonts/x.woff2)}"},
		map[string]any{"path": "assets/fonts/x.woff2", "data": "d09GMgABAAAAAA=="},
		map[string]any{"path": "post/js/engine.js", "text": "export const x = 1"},
	}}))
	doc := func(body string) string {
		return `<!doctype html><html lang="en"><head><title>T</title><link rel="stylesheet" href="/assets/style.css"></head><body>` + body + `</body></html>`
	}
	header := `<header class="site-h"><a href="/">Home</a><a href="/post/">Post</a></header>`
	footer := `<footer class="site-f">© 2026</footer>`
	pages := []any{
		map[string]any{"path": "index.html", "html": doc(header + `<main><section><h1>Home</h1></section>` + footer + `</main>`)},
		map[string]any{"path": "post/index.html", "html": doc(header + `<main><section><h1>Post</h1></section></main>` + footer +
			`<script type="module">import { x } from "./js/engine.js"; document.title = "x" + x</script>`)},
		map[string]any{"path": "about.html", "html": doc(header + `<main><section><h1>About</h1></section></main>` + footer)},
		map[string]any{"path": "404.html", "html": doc(`<main><h1>Lost</h1><a href="/">Home</a></main>`)},
		// An app screen: header bar and main in one grid, a dialog after it, no footer.
		map[string]any{"path": "app/index.html", "html": doc(`<div class="app"><header class="bar">Bar</header><main><section>Canvas</section></main></div><dialog id="dlg"></dialog>`)},
	}
	out := mcpStructured(t, "web_builder_import_site", call("web_builder_import_site", map[string]any{"kit": "corp", "pages": pages}))
	if len(asList(out["pages"])) != 4 {
		t.Fatalf("import: %v", out)
	}
	fetch := func(path string) (int, string, http.Header) {
		t.Helper()
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(b), resp.Header
	}
	code, home, h := fetch("/")
	if code != 200 || strings.Count(home, `class="site-f"`) != 1 || strings.Count(home, `class="site-h"`) != 1 {
		t.Errorf("home: %d, %d footers, %d headers", code, strings.Count(home, `class="site-f"`), strings.Count(home, `class="site-h"`))
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self' data: https: blob:") && !strings.Contains(csp, "blob:") {
		t.Errorf("code page CSP without blob: images: %s", csp)
	}
	if _, about, _ := fetch("/about/"); strings.Count(about, `class="site-f"`) != 1 || strings.Count(about, `class="site-h"`) != 1 {
		t.Errorf("about: header and footer from the templates expected once")
	}
	code, nf, _ := fetch("/no-such-page/")
	if code != 404 || !strings.Contains(nf, "<h1>Lost</h1>") || strings.Contains(nf, `class="site-h"`) || strings.Contains(nf, `class="site-f"`) {
		t.Errorf("bare 404 got the site's header or footer: %d %s", code, wbSnippet(nf, "Lost"))
	}
	if _, app, _ := fetch("/app/"); strings.Count(app, `<div class="app"`) != 1 || strings.Contains(app, `class="site-f"`) ||
		!strings.Contains(app, `<div class="app"><header class="bar">Bar</header>`) {
		t.Errorf("app page: the grid wrapper must be printed once around header and main: %s", wbSnippet(app, `class="bar"`))
	}
	// The kit at the root: the stylesheet's font, the module next to its page.
	for _, p := range []string{"/assets/fonts/x.woff2", "/post/js/engine.js", "/assets/style.css"} {
		if code, body, hd := fetch(p); code != 200 || body == "" || hd.Get("Cache-Control") != wbKitCacheShort {
			t.Errorf("%s: %d %q %q", p, code, body, hd.Get("Cache-Control"))
		}
	}
	for _, p := range []string{"/post/js/none.js", "/assets/../post/js/engine.js.html", "/wp-content/x/assets/style.css"} {
		if code, _, _ := fetch(p); code == 200 {
			t.Errorf("%s: served", p)
		}
	}
}

// A static copy of a WordPress site keeps its archive pages as files (author/admin/index.html,
// category/news/index.html, 2026/09/index.html). The import makes them pages at those paths and
// claims the paths, so they answer there instead of this site's author, category and date
// archives (page 1 only); a claim ends with its page.
func TestMCPImportSiteClaimsArchivePaths(t *testing.T) {
	ts, c, srv := newTestServer(t, true)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, call := mcpTestAgent(t, base, c, "Site agent", nil)
	if r := call("activate_plugin", map[string]any{"slug": "golementor"}); r["isError"] == true {
		t.Fatalf("activate: %v", mcpText(r))
	}
	if r := call("activate_theme", map[string]any{"slug": wbHelloSlug}); r["isError"] == true {
		t.Fatalf("theme: %v", mcpText(r))
	}
	mcpStructured(t, "web_builder_kit_upload", call("web_builder_kit_upload", map[string]any{"kit": "wp", "files": []any{
		map[string]any{"path": "css/site.css", "text": "body{margin:0}"},
	}}))
	doc := func(h1 string) string {
		return `<!doctype html><html lang="en"><head><title>` + h1 + `</title></head><body><main><section><h1>` + h1 + `</h1></section></main></body></html>`
	}
	pages := []any{
		map[string]any{"path": "index.html", "html": doc("Home")},
		map[string]any{"path": "author/admin/index.html", "html": doc("Design author")},
		map[string]any{"path": "category/news/index.html", "html": doc("Design category")},
		map[string]any{"path": "2026/09/index.html", "html": doc("Design month")},
		map[string]any{"path": "about/index.html", "html": doc("About")},
	}
	out := mcpStructured(t, "web_builder_import_site", call("web_builder_import_site", map[string]any{"kit": "wp", "pages": pages, "templates": false}))
	var cl []string
	for _, x := range asList(out["claimed_paths"]) {
		cl = append(cl, x.(string))
	}
	claimed := strings.Join(cl, " ")
	for _, want := range []string{"/author/admin/ (author archive)", "/category/news/ (category archive)", "/2026/09/ (date archive)"} {
		if !strings.Contains(claimed, want) {
			t.Errorf("claimed %q, want %s", claimed, want)
		}
	}
	if strings.Contains(claimed, "/about/") {
		t.Errorf("a page no archive takes was claimed: %s", claimed)
	}
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(b)
	}
	for path, h1 := range map[string]string{"/author/admin/": "Design author", "/category/news/": "Design category", "/2026/09/": "Design month", "/about/": "About"} {
		if code, body := get(path); code != 200 || !strings.Contains(body, "<h1>"+h1+"</h1>") {
			t.Errorf("%s: %d, want the imported page %q: %.200s", path, code, h1, wbSnippet(body, "<h1"))
		}
	}
	// Later archive pages are the archive's, not the imported page's.
	if _, body := get("/author/admin/page/2/"); strings.Contains(body, "Design author") {
		t.Error("/author/admin/page/2/ served the imported page")
	}
	// The claim ends with its page: the author archive answers again.
	var id int64
	for _, p := range asList(out["pages"]) {
		if m := p.(map[string]any); m["path"] == "author/admin/index.html" {
			id = int64(m["post_id"].(float64))
		}
	}
	if err := srv.store.DeletePost(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	srv.wbPurgePages()
	if _, body := get("/author/admin/"); strings.Contains(body, "Design author") {
		t.Error("a deleted page still claims its path")
	}
	if err := srv.wbSiteDropClaims(t.Context(), []int64{id}); err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.wbSiteClaims()["author/admin"]; ok {
		t.Error("the claim of a removed page was kept")
	}
	if _, ok := srv.wbSiteClaims()["category/news"]; !ok {
		t.Error("another page's claim was dropped")
	}
}

// A copy of a live site: its canonical domain is detected, links to its pages point at the
// imported pages, and neither its comment form nor its plugin's AJAX endpoint reaches the original
// site on the imported page (the safe default); tracking is kept and reported.
func TestMCPImportSiteOriginalDomain(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, call := mcpTestAgent(t, base, c, "Site agent", nil)
	if r := call("activate_plugin", map[string]any{"slug": "golementor"}); r["isError"] == true {
		t.Fatalf("activate: %v", mcpText(r))
	}
	mcpStructured(t, "web_builder_kit_upload", call("web_builder_kit_upload", map[string]any{"kit": "copy", "files": []any{
		map[string]any{"path": "wp-content/uploads/a.png", "data": "iVBORw0KGgo="},
	}}))
	doc := func(canon, body string) string {
		return `<!doctype html><html lang="en"><head><title>T</title><link rel="canonical" href="https://old.example/` + canon + `">
<script>var ajax = {"url":"https:\/\/old.example\/wp-admin\/admin-ajax.php"};</script>
<script async src="https://www.googletagmanager.com/gtag/js?id=G-1"></script></head><body><main><section>` + body + `</section></main></body></html>`
	}
	pages := []any{
		map[string]any{"path": "index.html", "html": doc("", `<h1>Home</h1><a href="https://old.example/about/">About</a><img src="https://old.example/wp-content/uploads/a.png" alt="">`)},
		map[string]any{"path": "about/index.html", "html": doc("about/", `<h1>About</h1><form action="https://old.example/wp-comments-post.php" method="post"><textarea name="comment"></textarea></form>`)},
	}
	out := mcpStructured(t, "web_builder_import_site", call("web_builder_import_site", map[string]any{"kit": "copy", "pages": pages, "templates": false}))
	os, _ := out["original_site"].(map[string]any)
	if os == nil || !strings.Contains(strings.Join(func() []string {
		var l []string
		for _, x := range asList(os["domains"]) {
			l = append(l, x.(string))
		}
		return l
	}(), ","), "old.example") || os["links"] != "rewrite" {
		t.Fatalf("original_site: %v", out)
	}
	if !strings.Contains(strings.Join(func() []string {
		var l []string
		for _, x := range asList(out["warnings"]) {
			l = append(l, x.(string))
		}
		return l
	}(), "\n"), "Google Tag Manager") {
		t.Errorf("tracking not reported: %v", out["warnings"])
	}
	for _, path := range []string{"/", "/about/"} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		page := string(b)
		if strings.Contains(page, "old.example") {
			t.Errorf("%s still names the original site: %s", path, wbSnippet(page, "old.example"))
		}
		if !strings.Contains(page, `\/wp-admin\/admin-ajax.php"`) || !strings.Contains(page, "googletagmanager.com/gtag/js?id=G-1") {
			t.Errorf("%s: endpoint or tracking: %.400s", path, wbSnippet(page, "admin-ajax"))
		}
	}
	resp, _ := http.Get(base + "/")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !regexpFind(`href="[^"]*/about/"`, string(b)) || !strings.Contains(string(b), `src="/wp-content/uploads/web-builder/kits/copy/wp-content/uploads/a.png`) {
		t.Errorf("home: links and files of the original site: %s", wbSnippet(string(b), "About"))
	}
	resp, _ = http.Get(base + "/about/")
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `action="/wp-comments-post.php"`) {
		t.Errorf("about: the comment form: %s", wbSnippet(string(b), "<form"))
	}
}

// A WordPress Salient copy (Decoland): no <main>, no <footer>; the footer sits in the content's
// wrapper and the page's scripts follow the shell. The site gets one header and one footer
// template, and each page is printed as the design: the shell once, #ajax-content-wrap once around
// the page's rows and the footer, the drawer after the shell, the page's own scripts after it.
func TestMCPImportSiteSalientShape(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, call := mcpTestAgent(t, base, c, "Site agent", nil)
	if r := call("activate_plugin", map[string]any{"slug": "golementor"}); r["isError"] == true {
		t.Fatalf("activate: %v", mcpText(r))
	}
	if r := call("activate_theme", map[string]any{"slug": wbHelloSlug}); r["isError"] == true {
		t.Fatalf("theme: %v", mcpText(r))
	}
	mcpStructured(t, "web_builder_kit_upload", call("web_builder_kit_upload", map[string]any{"kit": "salient", "files": []any{
		map[string]any{"path": "wp-content/themes/salient/style.css", "text": "#footer-outer{color:#333}"},
	}}))
	page := func(cur, title, seed string) string {
		up := ""
		if cur != "index.html" {
			up = "../"
		}
		item := func(href, label string) string {
			if href == cur {
				return `<li class="menu-item current-menu-item"><a aria-current="page" href="` + up + href + `">` + label + `</a></li>`
			}
			return `<li class="menu-item"><a href="` + up + href + `">` + label + `</a></li>`
		}
		nav := item("index.html", "Home") + item("blog/index.html", "Blog")
		return `<!doctype html><html lang="tr"><head><title>` + title + `</title>
<script type="application/ld+json">{"name":"` + title + `"}</script>
<link rel="stylesheet" href="` + up + `wp-content/themes/salient/style.css?ver=1"></head><body class="page">
<nav class="skip-wrap"><a class="skip" href="#ajax-content-wrap">Skip</a></nav>
<div class="ocm-effect-wrap"><div class="ocm-effect-wrap-inner">
<div id="header-outer"><div id="fws_` + seed + `a1b2c3" class="wpb_row">Open</div><header id="top"><nav><ul>` + nav + `</ul></nav></header></div>
<div id="ajax-content-wrap">
<div class="container-wrap"><h1>` + title + `</h1></div>
<div id="footer-outer"><p>Footer</p></div>
<div id="slide-out-widget-area"><ul>` + nav + `</ul></div>
</div></div></div>
<script>var page = {"postID":"` + seed + `"};</script>
<aside id="dc-drawer">Drawer</aside>
</body></html>`
	}
	pages := []any{
		map[string]any{"path": "index.html", "html": page("index.html", "Home", "100")},
		map[string]any{"path": "blog/index.html", "html": page("blog/index.html", "Blog", "201")},
		map[string]any{"path": "hakkimizda/index.html", "html": page("hakkimizda/index.html", "About", "302")},
	}
	out := mcpStructured(t, "web_builder_import_site", call("web_builder_import_site", map[string]any{"kit": "salient", "pages": pages}))
	var types []string
	for _, x := range asList(out["templates"]) {
		types = append(types, x.(map[string]any)["type"].(string))
	}
	if strings.Join(types, ",") != "header,footer" {
		t.Fatalf("templates: %v", out["templates"])
	}
	for _, x := range []struct{ path, seed, title string }{{"/", "100", "Home"}, {"/blog/", "201", "Blog"}, {"/hakkimizda/", "302", "About"}} {
		resp, err := http.Get(base + x.path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		html := string(b)
		for _, once := range []string{`<div class="ocm-effect-wrap">`, `<div id="ajax-content-wrap">`, `id="footer-outer"`, `id="dc-drawer"`} {
			if strings.Count(html, once) != 1 {
				t.Errorf("%s: %s printed %d times", x.path, once, strings.Count(html, once))
			}
		}
		order := []string{`<div class="ocm-effect-wrap">`, `<header id="top"`, `<div id="ajax-content-wrap">`, "<h1>" + x.title + "</h1>", `id="footer-outer"`,
			`id="slide-out-widget-area"`, `id="dc-drawer"`, `"postID":"` + x.seed + `"`}
		last := -1
		for _, o := range order {
			i := strings.Index(html, o)
			if i < 0 || i < last {
				t.Errorf("%s: %s out of order (%d after %d): %s", x.path, o, i, last, wbSnippet(html, `id="ajax-content-wrap"`))
				break
			}
			last = i
		}
		// The footer closes the content's wrapper and the shell before the drawer.
		if i, j := strings.Index(html, `id="slide-out-widget-area"`), strings.Index(html, `id="dc-drawer"`); i < 0 || j < 0 || strings.Count(html[i:j], "</div>") < 4 {
			t.Errorf("%s: wrappers not closed before the drawer: %s", x.path, wbSnippet(html, `id="dc-drawer"`))
		}
		if !strings.Contains(html, `{"name":"`+x.title+`"}`) || strings.Count(html, "application/ld+json") != 1 {
			t.Errorf("%s: the page's own JSON-LD", x.path)
		}
	}
}

// Pretty-permalink posts of a WordPress copy (<slug>/index.html at the top, a blog page listing
// them) become posts under one single-post template when the site's permalinks are /%postname%/:
// same addresses, the blog list a loop of posts, a post written later listed and drawn too.
func TestMCPImportSitePostCollection(t *testing.T) {
	ts, c, srv := newTestServer(t, true)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, call := mcpTestAgent(t, base, c, "Site agent", nil)
	if r := call("activate_plugin", map[string]any{"slug": "golementor"}); r["isError"] == true {
		t.Fatalf("activate: %v", mcpText(r))
	}
	if r := call("activate_theme", map[string]any{"slug": wbHelloSlug}); r["isError"] == true {
		t.Fatalf("theme: %v", mcpText(r))
	}
	if err := srv.settings.Set(t.Context(), map[string]any{"permalink_structure": "/%postname%/"}); err != nil {
		t.Fatal(err)
	}
	// The site's own posts are older than the import (Hello world! written at install).
	if _, err := srv.store.Pool.Exec(t.Context(), `UPDATE posts SET published_at = published_at - interval '1 day' WHERE type = 'post'`); err != nil {
		t.Fatal(err)
	}
	mcpStructured(t, "web_builder_kit_upload", call("web_builder_kit_upload", map[string]any{"kit": "wp", "files": []any{
		map[string]any{"path": "css/site.css", "text": "body{margin:0}"},
	}}))
	nav := `<header class="top"><nav><a class="n" href="../blog/index.html">Blog</a></nav></header>`
	doc := func(title, cls, main string) string {
		return `<!doctype html><html lang="en"><head><title>` + title + `</title><link rel="stylesheet" href="../css/site.css"></head><body class="` + cls + `">` +
			nav + `<main>` + main + `</main><footer class="foot">Footer</footer></body></html>`
	}
	post := func(title, id, body string) string {
		return doc(title, "single single-post postid-"+id, `<section class="hero"><h1>`+title+`</h1></section><article id="post-`+id+`" class="post-`+id+`"><div class="content">`+body+`</div></article>`)
	}
	card := func(slug, title string) string {
		return `<article class="card"><a href="../` + slug + `/index.html"><h3>` + title + `</h3></a></article>`
	}
	pages := []any{
		map[string]any{"path": "alpha/index.html", "html": post("Alpha post", "11", `<p>Alpha body text.</p><h2>More</h2>`)},
		map[string]any{"path": "beta/index.html", "html": post("Beta post", "12", `<p>Beta body.</p><ul><li>x</li></ul>`)},
		map[string]any{"path": "gamma/index.html", "html": post("Gamma post", "13", `<blockquote>Gamma.</blockquote>`)},
		map[string]any{"path": "blog/index.html", "html": doc("Blog", "page", `<section><h1>Blog</h1><div class="grid">`+card("alpha", "Alpha post")+card("beta", "Beta post")+card("gamma", "Gamma post")+`</div></section>`)},
	}
	out := mcpStructured(t, "web_builder_import_site", call("web_builder_import_site", map[string]any{"kit": "wp", "pages": pages, "collections": true}))
	b, _ := out["bindings"].(map[string]any)
	if b == nil || len(asList(b["groups"])) != 1 || len(asList(b["listings"])) != 1 {
		t.Fatalf("bindings: %v", out["bindings"])
	}
	if g := asList(b["groups"])[0].(map[string]any); g["type"] != "post" {
		t.Fatalf("group: %v", g)
	}
	var n int
	if err := srv.store.Pool.QueryRow(t.Context(), `SELECT count(*) FROM posts WHERE type = 'post' AND meta->>$1 = 'wp'`, wbSiteMetaKit).Scan(&n); err != nil || n != 3 {
		t.Fatalf("posts made: %d %v", n, err)
	}
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(b)
	}
	for path, want := range map[string]string{"/alpha/": "<p>Alpha body text.</p>", "/beta/": "<li>x</li>", "/gamma/": "<blockquote>Gamma.</blockquote>"} {
		code, body := get(path)
		if code != 200 || !strings.Contains(body, want) || !strings.Contains(body, `<section class="hero"><h1`) {
			t.Errorf("%s: %d %s", path, code, wbSnippet(body, "hero"))
		}
	}
	_, blog := get("/blog/")
	for _, title := range []string{"Alpha post", "Beta post", "Gamma post"} {
		if !regexpFind(`<h3[^>]*>`+title+`</h3>`, blog) {
			t.Errorf("blog list: %s missing: %s", title, wbSnippet(blog, "grid"))
		}
	}
	// A post written later is drawn by the template and listed.
	var created struct {
		ID int64 `json:"id"`
	}
	if r := call("create_post", map[string]any{"title": "Delta post", "content": "<p>Delta words.</p>", "status": "publish"}); r["isError"] == true {
		t.Logf("create_post: %v", mcpText(r))
	} else {
		_ = created
		srv.wbPurgePages()
		if code, body := get("/delta-post/"); code != 200 || !regexpFind(`<section class="hero"><h1[^>]*>Delta post</h1>`, body) {
			t.Errorf("a new post is not drawn by the template: %d %s", code, wbSnippet(body, "<body"))
		}
	}
}
