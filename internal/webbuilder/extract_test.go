package webbuilder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadExtractFixture(t *testing.T) (*Extract, *Extract) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "import", "corporate.extract.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Desktop *Extract `json:"desktop"`
		Mobile  *Extract `json:"mobile"`
	}
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatal(err)
	}
	return fx.Desktop, fx.Mobile
}

// A real browser extraction (testdata/import/corporate.html, captured with extract.js at 1440 and 390
// px) becomes sections that validate, render with the server bundle and keep the page's look and SEO.
func TestConvertExtractFixture(t *testing.T) {
	desk, mob := loadExtractFixture(t)
	res, err := ConvertExtract(desk, mob)
	if err != nil {
		t.Fatal(err)
	}
	if res.SEO.Title == "" || !strings.Contains(res.SEO.Description, "Aksoy Hukuk") || len(res.SEO.H1) != 1 {
		t.Errorf("seo: %+v", res.SEO)
	}
	all, _ := json.Marshal(res.Sections)
	s := string(all)
	for _, want := range []string{`"node":"heading"`, `"tag":"h1"`, `"dir":"grid"`, `"cols":4`, `"colsM":2`, `"menu":true`, `"bgImage":"linear-gradient(`, `"m":{`} {
		if !strings.Contains(s, want) {
			t.Errorf("sections lack %s", want)
		}
	}
	bundle, err := os.ReadFile(filepath.Join("..", "..", "web", EmbedDir, RenderFile))
	if err != nil {
		t.Skip("no server render bundle (run make builder)")
	}
	r, err := NewServerRenderer(bundle, 1)
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
	pr, err := ParseDoc([]byte(`{"site":{"brand":"#0f2a44","fontPair":"plain","corners":"soft"},"sections":[`+strings.Join(secs, ",")+`]}`), DocOptions{})
	if err != nil || !pr.Valid() {
		t.Fatalf("doc: %v %+v", err, pr)
	}
	rr, err := r.Render(pr.JSON, ServerRenderOptions{PostID: 3, SiteURL: "https://x.test", FormActionPrefix: "https://x.test/f/"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<h1", "Çalışma Alanlarımız", "Ücretsiz ön görüşme", `alt="Av. Mehmet Aksoy ofiste"`, "color: rgb(201, 162, 75)"} {
		if !strings.Contains(rr.HTML, want) {
			t.Errorf("render lacks %q", want)
		}
	}
	if _, _, err := SanitizeSnapshot(rr.HTML, SnapshotOptions{FormActionPrefix: "https://x.test/f/"}); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
}

// The extraction comes from a browser running the imported page: nothing in it is trusted.
func TestConvertExtractHostile(t *testing.T) {
	deep := &ExtractNode{N: "box"}
	cur := deep
	for i := 0; i < 200; i++ {
		k := &ExtractNode{N: "box"}
		cur.K = []*ExtractNode{k, {N: "text", T: "x"}}
		cur = k
	}
	root := &ExtractNode{N: "box", P: "0", Lay: map[string]any{"dir": "row; color:red", "cols": 9999.0, "gap": -5.0}, K: []*ExtractNode{
		{N: "link", T: `click <img src=x onerror=alert(1)><script>alert(2)</script>`, Href: "javascript:alert(3)"},
		{N: "button", T: "Go", Href: "  JaVaScRiPt:alert(4)"},
		{N: "image", T: "javascript:alert(5)"},
		{N: "image", T: "https://ok.example/a.png", X: `"><script>`},
		{N: "icon", T: "data:image/svg+xml,<svg onload=alert(1)>"},
		{N: "iframe", T: "https://evil.example"},
		{N: "text", T: "styled", Es: map[string]any{
			"color": "red;background:url(javascript:x)", "bg": "#ABCDEF", "bgImage": "url(javascript:alert(1))",
			"border": "1px solid red</style><script>", "shadow": "0 0 1px red; behavior:url(x)", "fontSize": 1e9,
			"font": "Inter'; } body{display:none", "padding": map[string]any{"t": "x", "l": 1e9}, "width": -4.0, "unknown": "x",
		}},
		deep,
	}}
	res, err := ConvertExtract(&Extract{V: 1, Sections: []ExtractSection{{Tag: "section", ID: "1bad id", Bg: "red;x", Root: root}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res.Sections)
	s := string(b)
	for _, bad := range []string{"javascript", "onerror", "<script", "onload", "evil.example", "</style", "behavior", "display:none", "1e+09", "unknown", "row; color", "1bad"} {
		if strings.Contains(strings.ToLower(s), strings.ToLower(bad)) {
			t.Errorf("kept %q: %.300s", bad, s[max(0, strings.Index(strings.ToLower(s), strings.ToLower(bad))-120):])
		}
	}
	if !strings.Contains(s, `"bg":"#abcdef"`) || !strings.Contains(s, "https://ok.example/a.png") || !strings.Contains(s, `"cols":12`) {
		t.Errorf("valid values were dropped: %.400s", s)
	}
	if res.Nodes > maxExtractNodes || strings.Count(s, `"node":"box"`) > maxExtractDepth+5 {
		t.Errorf("nesting was not capped: %d nodes", res.Nodes)
	}
	if _, err := ConvertExtract(&Extract{V: 2}, nil); err == nil {
		t.Error("an unknown format must be refused")
	}
	if _, err := ConvertExtract(&Extract{V: 1, Error: "boom"}, nil); err == nil {
		t.Error("a browser error must be reported")
	}
}

// A part with more nodes than a section may hold is split into consecutive sections.
func TestConvertExtractSplitsBigParts(t *testing.T) {
	root := &ExtractNode{N: "box", P: "0", Es: map[string]any{"padding": map[string]any{"t": 80.0, "r": 0.0, "b": 80.0, "l": 0.0}, "bg": "#eeeeee"}}
	for i := 0; i < 30; i++ {
		row := &ExtractNode{N: "box", Lay: map[string]any{"dir": "row"}}
		for j := 0; j < 30; j++ {
			row.K = append(row.K, &ExtractNode{N: "text", T: "cell"})
		}
		root.K = append(root.K, row)
	}
	res, err := ConvertExtract(&Extract{V: 1, Sections: []ExtractSection{{Tag: "section", Bg: "#eeeeee", Root: root}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sections) < 2 {
		t.Fatalf("not split: %d sections", len(res.Sections))
	}
	total := 0
	for i, s := range res.Sections {
		items := s["items"].([]map[string]any)
		if len(items) > 500 {
			t.Errorf("section %d has %d items", i, len(items))
		}
		total += len(items)
	}
	if total < 30*31 {
		t.Errorf("nodes lost: %d", total)
	}
}

func TestBorderSidesOK(t *testing.T) {
	for v, want := range map[string]bool{
		"border-top:1px solid #ccc;border-bottom:2px dashed rgba(0,0,0,.2)": true,
		"border-left:4px solid #c9a24b":                                     true,
		"border-top:1px solid red;color:red":                                false,
		"border-top:url(javascript:x)":                                      false,
		"border-top:1px solid red</style>":                                  false,
		"1px solid red":                                                     false,
	} {
		if got := BorderSidesOK(v); got != want {
			t.Errorf("BorderSidesOK(%q) = %v", v, got)
		}
	}
}
