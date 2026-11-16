package webbuilder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const codeTestDoc = `{"site":{"brand":"#0a7","fontPair":"modern","corners":"soft"},"sections":[
 {"id":"c1","kind":"code","name":"Header","hidden":false,"content":{"html":"<header class=\"ln\"><a data-wbc=\"1\" href=\"/\">Pursae</a></header>","head":"<link rel=\"stylesheet\" href=\"/wp-content/uploads/web-builder/kits/merge/assets/style.css\">","kit":"merge"},"items":[],"style":{"palette":"light","align":"left","padding":0,"titleSize":40,"hideOn":[]}},
 {"id":"c2","kind":"code","name":"Hero","hidden":false,"content":{"html":"<section id=\"stage\"><h1 data-wbc=\"2\">Hi</h1><script src=\"/wp-content/uploads/web-builder/kits/merge/assets/js/a.js\"></script></section>","head":"<link rel=\"stylesheet\" href=\"/wp-content/uploads/web-builder/kits/merge/assets/style.css\">\n<link rel=\"stylesheet\" href=\"/wp-content/uploads/web-builder/kits/merge/assets/css/x.css\">","kit":"merge"},"items":[],"style":{"palette":"light","align":"left","padding":0,"titleSize":40,"hideOn":[]}}
]}`

func TestCodeSectionsFill(t *testing.T) {
	list, only := CodeSections([]byte(codeTestDoc))
	if len(list) != 2 || !only {
		t.Fatalf("CodeSections = %d, only %v", len(list), only)
	}
	snap := `<div class="wb-page cv @container relative bg-white text-left" data-wb-root="" data-wb-post="7" style="--x:1"><div data-wb-code="c1"></div><div data-wb-code="c2"></div><div data-wb-code="gone"></div></div>`
	out := FillCode(snap, list, only)
	if !IsCodePage(out) {
		t.Fatalf("not a code page: %s", out)
	}
	if strings.Contains(out, "bg-white") || !strings.Contains(out, `data-wb-post="7"`) || !strings.Contains(out, `style="display:contents"`) {
		t.Fatalf("root not simplified: %.200s", out)
	}
	if strings.Contains(out, "data-wbc") || strings.Contains(out, `"gone"`) {
		t.Fatalf("editor ids or unknown placeholder kept: %s", out)
	}
	head, body := CodeHead(out)
	if strings.Count(head, "style.css") != 1 || !strings.Contains(head, "x.css") {
		t.Fatalf("head not merged: %q", head)
	}
	if strings.Contains(body, "<template") || !strings.Contains(body, `<header class="ln">`) || !strings.Contains(body, "a.js") {
		t.Fatalf("body: %s", body)
	}
	if got := FillCode(`<div class="wb-page" data-wb-root=""><section>x</section></div>`, list, only); strings.Contains(got, "template") {
		t.Fatalf("a snapshot without placeholders changed: %s", got)
	}
}

func TestCodeFallback(t *testing.T) {
	got := CodeFallback(`<style>p{}</style><section><h2 data-wbc="3">Title</h2><script>alert(1)</script><!-- c --><p>Body</p></section><script>x()</script>`)
	if strings.Contains(got, "script") || strings.Contains(got, "style") || strings.Contains(got, "data-wbc") || strings.Contains(got, "<!--") {
		t.Fatalf("fallback kept code: %s", got)
	}
	if !strings.Contains(got, "<h2>Title</h2>") || !strings.Contains(got, "<p>Body</p>") {
		t.Fatalf("fallback lost content: %s", got)
	}
}

func TestCodeSectionValidation(t *testing.T) {
	res, err := ParseDoc([]byte(codeTestDoc), DocOptions{UnfilteredHTML: true})
	if err != nil || !res.Valid() {
		t.Fatalf("code doc invalid: %v %+v", err, res.Errors)
	}
	if !strings.Contains(string(res.JSON), `data-wbc=\"2\"`) {
		t.Fatalf("code markup changed by normalization: %s", res.JSON)
	}
	res, err = ParseDoc([]byte(codeTestDoc), DocOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if HasCode(res.JSON) {
		t.Fatalf("code sections kept without unfiltered_html: %s", res.JSON)
	}
	big := strings.Repeat("x", MaxHTMLSectionLen+10)
	doc := strings.Replace(codeTestDoc, `Hi</h1>`, big+`</h1>`, 1)
	if res, _ := ParseDoc([]byte(doc), DocOptions{UnfilteredHTML: true}); res == nil || !res.Valid() {
		t.Fatalf("code section above the html-section limit rejected: %+v", res)
	}
	nested := `{"site":{"brand":"#0a7","fontPair":"modern","corners":"soft"},"sections":[{"id":"l1","kind":"layout","name":"L","hidden":false,"content":{"type":"flex","structure":"col"},"items":[{"id":"i1","title":"","text":"","sub":{"id":"c9","kind":"code","name":"C","hidden":false,"content":{"html":"<p>x</p>"},"items":[],"style":{"palette":"light","align":"left","padding":0,"titleSize":40,"hideOn":[]}}}],"style":{"palette":"light","align":"left","padding":0,"titleSize":40,"hideOn":[]}}]}`
	if res, _ := ParseDoc([]byte(nested), DocOptions{UnfilteredHTML: true}); res != nil && res.Valid() {
		t.Fatal("a nested code section validated")
	}
}

// The renderer draws a placeholder that the snapshot sanitizer keeps and FillCode fills.
func TestCodeSectionServerRender(t *testing.T) {
	bundle, err := os.ReadFile(filepath.Join("..", "..", "web", EmbedDir, RenderFile))
	if err != nil {
		t.Skip("no server render bundle (run make builder)")
	}
	r, err := NewServerRenderer(bundle, 1)
	if err != nil {
		t.Fatal(err)
	}
	rr, err := r.Render([]byte(codeTestDoc), ServerRenderOptions{PostID: 7, SiteURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rr.HTML, `data-wb-code="c1"`) || !strings.Contains(rr.HTML, `data-wb-code="c2"`) {
		t.Fatalf("no placeholders: %s", rr.HTML)
	}
	clean, _, err := SanitizeSnapshot(rr.HTML, SnapshotOptions{EmbedHTML: true, SearchAction: "https://example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	list, only := CodeSections([]byte(codeTestDoc))
	out := FillCode(clean, list, only)
	if !strings.Contains(out, `<h1>Hi</h1>`) || !strings.Contains(out, `<header class="ln">`) || !IsCodePage(out) {
		t.Fatalf("not filled: %s", out)
	}
}

// Editor ids: every element of a code section is a block (the svg itself, not its drawing; no
// script/style), older sections get theirs above the highest id in use, and nothing changes when
// every element has one.
func TestTagCodeDoc(t *testing.T) {
	doc := `{"site":{},"sections":[` +
		`{"id":"a","kind":"code","content":{"html":"<section class=\"s\"><h2 data-wbc=\"7\">Hi</h2><div class=\"card\"><p>One</p></div><svg viewBox=\"0 0 1 1\"><path d=\"M0\"></path></svg><script>x()</script></section>"}},` +
		`{"id":"b","kind":"hero","content":{"title":"<p>untouched</p>"}}]}`
	out := string(TagCodeDoc(json.RawMessage(doc)))
	var d struct {
		Sections []struct {
			Content map[string]string `json:"content"`
		} `json:"sections"`
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	h := d.Sections[0].Content["html"]
	for _, want := range []string{`<section class="s" data-wbc="8">`, `<h2 data-wbc="7">`, `<div class="card" data-wbc="9">`, `<p data-wbc="10">`, `<svg viewBox="0 0 1 1" data-wbc="11"><path d="M0"></path></svg>`, `<script>x()</script>`} {
		if !strings.Contains(h, want) {
			t.Errorf("tagged markup lacks %s: %s", want, h)
		}
	}
	if d.Sections[1].Content["title"] != "<p>untouched</p>" {
		t.Errorf("a non-code section changed: %v", d.Sections[1].Content)
	}
	if again := string(TagCodeDoc(json.RawMessage(out))); again != out {
		t.Errorf("a fully tagged doc changed:\n%s\n%s", out, again)
	}
	if StripCodeIDs(h) != `<section class="s"><h2>Hi</h2><div class="card"><p>One</p></div><svg viewBox="0 0 1 1"><path d="M0"></path></svg><script>x()</script></section>` {
		t.Errorf("published markup keeps ids: %s", StripCodeIDs(h))
	}
}
