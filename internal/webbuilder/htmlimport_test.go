package webbuilder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportHTMLStructure(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "import", "landing.html"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := ImportHTML(string(src), ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "Acme Studio" || res.Brand != "#e4572e" {
		t.Errorf("title %q brand %q", res.Title, res.Brand)
	}
	if len(res.Sections) != 5 {
		t.Fatalf("want 5 parts (header, hero, work, about, footer), got %d", len(res.Sections))
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(res.Sections)
	s := buf.String()
	for _, want := range []string{`"node":"heading"`, `"tag":"h1"`, `We build <i>brands</i> that last.`, `"node":"button"`, `"href":"#work"`,
		`"node":"image"`, `https://images.example.com/a.jpg`, `"dir":"grid"`, `"cols":3`, `"node":"input"`, `"node":"divider"`, `"bg":"#111827"`,
		`"palette":"dark"`, `<ul><li>Brand</li>`, `"fontSize":56`, `"cssId":"work"`, `"self":"center"`, `"align":"center"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, bad := range []string{"javascript:", "alert", "onclick", "evil.example", "scale(1.02)"} {
		if strings.Contains(s, bad) {
			t.Errorf("kept %q", bad)
		}
	}
}

// Imported sections must pass the doc validation and render with the server bundle.
func TestImportHTMLRenders(t *testing.T) {
	bundle, err := os.ReadFile(filepath.Join("..", "..", "web", EmbedDir, RenderFile))
	if err != nil {
		t.Skip("no server render bundle (run make builder)")
	}
	r, err := NewServerRenderer(bundle, 1)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(filepath.Join("testdata", "import", "landing.html"))
	res, err := ImportHTML(string(src), ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var secs []string
	for _, spec := range res.Sections {
		b, _ := json.Marshal(spec)
		sec, err := r.CreateSection(b)
		if err != nil {
			t.Fatal(err)
		}
		secs = append(secs, string(sec))
	}
	doc := `{"site":{"brand":"` + res.Brand + `","fontPair":"plain","corners":"soft"},"sections":[` + strings.Join(secs, ",") + `]}`
	pr, err := ParseDoc([]byte(doc), DocOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !pr.Valid() {
		t.Fatalf("invalid: %+v", pr.Errors)
	}
	rr, err := r.Render(pr.JSON, ServerRenderOptions{PostID: 7, SiteURL: "https://x.test", FormActionPrefix: "https://x.test/f/"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Northwind", "See our work", "Subscribe", "© 2026 Acme Studio"} {
		if !strings.Contains(rr.HTML, want) {
			t.Errorf("render lacks %q", want)
		}
	}
	if _, _, err := SanitizeSnapshot(rr.HTML, SnapshotOptions{FormActionPrefix: "https://x.test/f/"}); err != nil {
		t.Fatalf("snapshot sanitizer: %v", err)
	}
}

// Patch 0017: a colored word survives the rich-text sanitizer, serialized as the browser does; other
// span styles do not.
func TestRichSpanColor(t *testing.T) {
	for in, want := range map[string]string{
		`We <span style="color:#4F46E5">remember</span>.`:      `We <span style="color: rgb(79, 70, 229);">remember</span>.`,
		`a <span style="color: rgba(1,2,3,.5)">b</span>`:       `a <span style="color: rgba(1, 2, 3, 0.5);">b</span>`,
		`a <span style="font-size:40px">b</span>`:              `a b`,
		`a <span style="color:red;background:url(x)">b</span>`: `a b`,
		`<span style="color:#fff"><b>x</b></span>`:             `<span style="color: rgb(255, 255, 255);"><b>x</b></span>`,
		`<span style="color:expression(alert(1))">x</span>`:    `x`,
	} {
		if got := SanitizeRich(in); got != want {
			t.Errorf("SanitizeRich(%q) = %q, want %q", in, got, want)
		}
	}
	if !IsRich(`<span style="color: rgb(1, 2, 3);">x</span>`) {
		t.Error("a colored span is rich text")
	}
}
