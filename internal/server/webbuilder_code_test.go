package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

const codeTestPage = `<!doctype html><html lang="tr"><head><meta charset="utf-8"><title>Kod Sayfası</title>
<meta name="description" content="A design kept as code.">
<link rel="stylesheet" href="assets/style.css?v=9">
</head><body>
<svg width="0" height="0" style="position:absolute"><symbol id="mark"><path d="M0 0"/></symbol></svg>
<header class="ln"><a class="brand" href="index.html">Brand</a><a href="forum/index.html">Forum</a></header>
<main id="main">
<section id="hero"><h1>Hello <em>code</em></h1><img src="assets/img/a.png" alt="A"></section>
<div class="aft"><section id="faq"><h2>FAQ</h2></section><footer class="pgf"><p>© 2026</p></footer></div>
</main>
<script src="assets/app.js"></script>
</body></html>`

func TestMCPImportHTMLCode(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, call := mcpTestAgent(t, base, c, "Code agent", nil)
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nfake"))
	// Bad paths are refused before anything is written.
	for _, bad := range []string{"../x.css", "a.php", "x/.next/a.css", "/abs.css"} {
		if r := call("web_builder_kit_upload", map[string]any{"kit": "demo", "files": []any{map[string]any{"path": bad, "text": "x"}}}); r["isError"] != true {
			t.Errorf("kit path %q accepted", bad)
		}
	}
	kit := mcpStructured(t, "web_builder_kit_upload", call("web_builder_kit_upload", map[string]any{"kit": "demo", "name": "Demo", "files": []any{
		map[string]any{"path": "assets/style.css", "text": ".ln{display:flex}.hero{background:url(img/a.png)}"},
		map[string]any{"path": "assets/img/a.png", "data": png},
		map[string]any{"path": "assets/app.js", "text": `fetch("https://api.example.com/stars").then(()=>{});import("https://cdn.example.net/three.js")`},
	}}))
	k, _ := kit["kit"].(map[string]any)
	if k == nil || k["version"] != float64(1) || k["files"] != float64(3) {
		t.Fatalf("kit: %v", kit)
	}
	// Kit files: immutable at the current version, revalidated otherwise.
	resp, err := http.Get(base + "/wp-content/uploads/web-builder/kits/demo/assets/style.css?v=1")
	if err != nil || resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("kit file: %v %v", err, resp)
	}
	if r, _ := http.Get(base + "/wp-content/uploads/web-builder/kits/demo/assets/style.css"); strings.Contains(r.Header.Get("Cache-Control"), "immutable") {
		t.Error("an unversioned kit URL is cached as immutable")
	}

	out := mcpStructured(t, "web_builder_import_html", call("web_builder_import_html", map[string]any{"html": codeTestPage, "kit": "demo", "path": "index.html",
		"link_map": map[string]any{"forum/index.html": "/forums/", "index.html": "/"}}))
	page, _ := out["page"].(map[string]any)
	if page == nil || page["status"] != "publish" || !strings.Contains(strAny(out["method"]), "code") {
		t.Fatalf("import: %v", out)
	}
	secs, _ := page["sections"].([]any)
	if len(secs) != 4 { // header, hero, faq, footer
		t.Fatalf("sections: %v", secs)
	}
	resp, err = http.Get(strAny(page["url"]))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	resp.Body.Close()
	html := b.String()
	for _, want := range []string{`<header class="ln">`, `<main id="main">`, `<div class="aft"><section id="faq">`, `href="/forums/"`,
		`/wp-content/uploads/web-builder/kits/demo/assets/style.css?v=1`, `src="/wp-content/uploads/web-builder/kits/demo/assets/img/a.png?v=1"`,
		`<script src="/wp-content/uploads/web-builder/kits/demo/assets/app.js?v=1" nonce="`, `<symbol id="mark">`} {
		if !strings.Contains(html, want) {
			t.Errorf("published page lacks %q", want)
		}
	}
	if strings.Count(html, "<main") != 1 || strings.Contains(html, "data-wbc") || strings.Contains(html, "web-builder-css") || strings.Contains(html, "data-wb-root") {
		t.Errorf("code page not published as the design's own markup")
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "https://api.example.com") || !strings.Contains(csp, "https://cdn.example.net") {
		t.Errorf("CSP lacks the kit's origins: %s", csp)
	}

	// Header and footer as site-wide templates, from a file whose content is not imported.
	tpl := mcpStructured(t, "web_builder_import_html", call("web_builder_import_html", map[string]any{"html": codeTestPage, "kit": "demo", "path": "index.html",
		"templates": true, "templates_only": true}))
	if tpl["page"] != nil || tpl["header_template"] == nil || tpl["footer_template"] == nil {
		t.Fatalf("templates only: %v", tpl)
	}

	// Without unfiltered_html (an author) there is no code import and no kit upload.
	_, ed := mcpTestAgentRole(t, base, c, "codeauthor", "author")
	if r := ed("web_builder_import_html", map[string]any{"html": codeTestPage, "method": "code", "title": "x"}); r["isError"] != true {
		t.Error("an author imported code")
	}
	if r := ed("web_builder_kit_upload", map[string]any{"kit": "demo2", "files": []any{map[string]any{"path": "a.css", "text": "x"}}}); r["isError"] != true {
		t.Error("an author uploaded a kit")
	}
}

// The editor gets every element of a code section as a block: a section stored before every
// element was tagged comes back with data-wbc ids on its document and on the user's draft.
func TestWBCodeBlocksTaggedOnLoad(t *testing.T) {
	srv, _, admin := wbrStart(t)
	p := wbrPost(t, srv, "page", "Old code page", "publish", 1)
	doc := json.RawMessage(`{"site":{},"sections":[{"id":"c1","kind":"code","name":"Hero","content":{"html":"<section class=\"hero\"><h1 data-wbc=\"3\">Hi</h1><div class=\"cards\"><div class=\"card\">A</div></div></section>"},"items":[],"style":{"palette":"light","align":"left","padding":0,"titleSize":40,"hideOn":[]}}]}`)
	if err := srv.store.WebBuilder().PublishDoc(context.Background(), &store.WebBuilderDoc{PostID: p.ID, Doc: doc, Page: json.RawMessage(`{}`), Renderer: 1}, 0); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.WebBuilder().SaveDraft(context.Background(), &store.WebBuilderDraft{PostID: p.ID, UserID: 1, Doc: doc, Page: json.RawMessage(`{}`), BaseRev: 1}); err != nil {
		t.Fatal(err)
	}
	code, v, _ := admin.do(http.MethodGet, wbrDocPath(p.ID, ""), nil, nil)
	if code != http.StatusOK {
		t.Fatalf("document: %d %v", code, v)
	}
	htmlOf := func(d any) string {
		m, _ := d.(map[string]any)
		secs, _ := m["sections"].([]any)
		if len(secs) == 0 {
			return ""
		}
		c, _ := secs[0].(map[string]any)["content"].(map[string]any)
		return strAny(c["html"])
	}
	got := wbrObj(v)
	draft, _ := got["draft"].(map[string]any)
	for name, h := range map[string]string{"doc": htmlOf(got["doc"]), "draft": htmlOf(draft["doc"])} {
		want := `<section class="hero" data-wbc="4"><h1 data-wbc="3">Hi</h1><div class="cards" data-wbc="5"><div class="card" data-wbc="6">A</div></div></section>`
		if h != want {
			t.Errorf("editor %s: %s", name, h)
		}
	}
}

// A design whose scripts sit in the head, in sections, in the header and in the footer, loads from
// CDNs and embeds a map.
const codeCSPPage = `<!doctype html><html><head><title>CSP design</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter&amp;display=swap">
<style>@import url("https://styles.example.org/base.css");</style>
<script>window.headRan=1</script>
<script src="https://cdn.example.com/lib.js" defer></script>
</head><body>
<header class="hd"><a href="/">Brand</a><script>window.hdrRan=1</script></header>
<main>
<section id="hero"><h1>Hi</h1><button onclick="go()">Go</button><button onclick="go()">Again</button><img src="x.png" onload="seen()"><script>window.secRan=1</script></section>
<section id="map"><iframe src="https://www.google.com/maps/embed?pb=1" loading="lazy"></iframe></section>
</main>
<footer class="ft"><p>F</p><script>window.ftRan=1</script></footer>
</body></html>`

var codeScriptTagRE = regexp.MustCompile(`(?is)<script\b[^>]*>`)

// Code pages under the nonce policy: every design script (head scripts lifted from the code head
// template, section scripts, header and footer template scripts, CDN scripts) carries the response's
// nonce in report and enforce mode; the policy allows the design's CDN, style, font and frame origins;
// inline event handlers and untrusted admin scripts are reported by the import.
func TestWBCodeCSP(t *testing.T) {
	ts, c, srv := newTestServer(t, true)
	ctx := context.Background()
	installAndLogin(t, ts, c)
	if err := srv.switchTheme(ctx, wbHelloSlug); err != nil {
		t.Fatal(err)
	}
	_, call := mcpTestAgent(t, ts.URL, c, "CSP agent", nil)
	mcpStructured(t, "web_builder_kit_upload", call("web_builder_kit_upload", map[string]any{"kit": "csp", "name": "CSP", "files": []any{
		map[string]any{"path": "x.png", "data": base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nfake"))},
		map[string]any{"path": "app.js", "text": `fetch("https://api.example.net/v1/x")`},
	}}))
	out := mcpStructured(t, "web_builder_import_html", call("web_builder_import_html", map[string]any{"html": codeCSPPage, "kit": "csp", "path": "index.html",
		"templates": true, "title": "CSP design"}))
	warns := strings.Join(strSlice(out["warnings"]), "\n")
	if !strings.Contains(warns, "3 inline event handler attribute(s)") || !strings.Contains(warns, "2× onclick on <button>") || !strings.Contains(warns, "1× onload on <img>") {
		t.Errorf("import warnings lack the inline handlers: %s", warns)
	}
	if strings.Contains(warns, "csp_admin_scripts") {
		t.Errorf("warned about untrusted scripts on a trusting site: %s", warns)
	}
	pageURL := strAny(out["page"].(map[string]any)["url"])

	for _, mode := range []string{cspModeReport, cspModeEnforce} {
		if err := srv.settings.Set(ctx, map[string]any{"csp_mode": mode}); err != nil {
			t.Fatal(err)
		}
		resp := anonGet(t, pageURL)
		nonce, body := pageNonce(t, resp)
		for _, want := range []string{"window.headRan=1", "window.secRan=1", "window.hdrRan=1", "window.ftRan=1", `src="https://cdn.example.com/lib.js"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: page lacks %s", mode, want)
			}
		}
		head, _, _ := strings.Cut(body, "</head>")
		if !strings.Contains(head, "window.headRan=1") || !strings.Contains(head, `src="https://cdn.example.com/lib.js"`) || strings.Contains(body, "data-wb-code-head") {
			t.Errorf("%s: the code head was not lifted into <head>", mode)
		}
		for _, tag := range codeScriptTagRE.FindAllString(body, -1) {
			if !strings.Contains(tag, `nonce="`+nonce+`"`) && !strings.Contains(tag, `type="application/ld+json"`) {
				t.Errorf("%s: script without the page's nonce: %s", mode, tag)
			}
		}
		policy := resp.Header.Get("Content-Security-Policy")
		dir := func(p, name string) string {
			for _, part := range strings.Split(p, ";") {
				if f := strings.Fields(part); len(f) > 0 && f[0] == name {
					return " " + strings.Join(f[1:], " ") + " "
				}
			}
			return ""
		}
		for d, wants := range map[string][]string{
			"script-src":  {"https://cdn.example.com", "https://api.example.net"},
			"connect-src": {"'self'", "https://api.example.net", "https://cdn.example.com"},
			"style-src":   {"'self'", "https://fonts.googleapis.com", "https://styles.example.org"},
			"font-src":    {"'self'", "https://fonts.gstatic.com"},
			"img-src":     {"'self'", "https:"},
		} {
			for _, w := range wants {
				if !strings.Contains(dir(policy, d), " "+w+" ") {
					t.Errorf("%s: %s lacks %s: %s", mode, d, w, policy)
				}
			}
		}
		if fs := dir(policy, "frame-src"); !strings.Contains(fs, " https: ") && !strings.Contains(fs, " https://www.google.com ") {
			t.Errorf("%s: frame-src does not allow the map: %s", mode, policy)
		}
		if strings.Contains(policy+resp.Header.Get("Content-Security-Policy-Report-Only"), "unsafe-hashes") {
			t.Errorf("%s: policy allows unsafe-hashes", mode)
		}
		switch mode {
		case cspModeEnforce:
			if s := dir(policy, "script-src"); !strings.Contains(s, "'strict-dynamic'") || strings.Contains(s, "'unsafe-inline'") {
				t.Errorf("enforced script-src: %s", s)
			}
		case cspModeReport:
			ro := resp.Header.Get("Content-Security-Policy-Report-Only")
			if !strings.Contains(dir(ro, "script-src"), " https://cdn.example.com ") || dir(ro, "connect-src") != "" || dir(ro, "style-src") != "" {
				t.Errorf("report-only policy: %s", ro)
			}
		}
	}

	// Stored administrator scripts untrusted (csp_admin_scripts off): the design's scripts get no
	// nonce, and the import says so under the enforced policy.
	if err := srv.settings.Set(ctx, map[string]any{"csp_admin_scripts": false}); err != nil {
		t.Fatal(err)
	}
	out = mcpStructured(t, "web_builder_import_html", call("web_builder_import_html", map[string]any{"html": codeCSPPage, "kit": "csp", "path": "index.html", "title": "CSP design 2"}))
	if warns := strings.Join(strSlice(out["warnings"]), "\n"); !strings.Contains(warns, "csp_admin_scripts is off") || !strings.Contains(warns, "do not run") {
		t.Errorf("no warning about untrusted scripts: %s", warns)
	}
	nonce, body := pageNonce(t, anonGet(t, strAny(out["page"].(map[string]any)["url"])))
	if !strings.Contains(body, "<script>window.secRan=1</script>") || strings.Contains(body, `nonce="`+nonce+`">window.secRan`) {
		t.Error("an untrusted design script got the nonce")
	}
}

// strSlice is a JSON array of strings.
func strSlice(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		out = append(out, strAny(x))
	}
	return out
}
