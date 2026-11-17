package server

import (
	"io"
	"net/http"
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
		{"/", `aria-current="page" class="nav on" href="/">Home`, `class="nav off" href="/iletisim/">Contact`},
		{"/iletisim/", `aria-current="page" class="nav on" href="/iletisim/">Contact`, `class="nav off" href="/">Home`},
		{"/hizmet/1/", `class="nav on" aria-controls="menu-services">Services`, `class="nav off" href="/">Home`},
	} {
		code, html := fetch(x.path)
		if code != 200 {
			t.Fatalf("%s: %d", x.path, code)
		}
		if !strings.Contains(html, x.on) || !strings.Contains(html, x.off) {
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
