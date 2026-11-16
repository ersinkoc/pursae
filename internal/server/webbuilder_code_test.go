package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
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
