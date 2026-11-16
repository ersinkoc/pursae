package webbuilder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The server render bundle must draw every renderStatic fixture exactly as the editor's renderStatic
// did under Node (testdata/render, exported by make builder from the same sources).
func TestServerRenderMatchesFixtures(t *testing.T) {
	bundle, err := os.ReadFile(filepath.Join("..", "..", "web", EmbedDir, RenderFile))
	if err != nil {
		t.Skip("no server render bundle (run make builder)")
	}
	r, err := NewServerRenderer(bundle, 1)
	if err != nil {
		t.Fatal(err)
	}
	opts := ServerRenderOptions{PostID: 42, SiteURL: "https://example.com", FormActionPrefix: "https://example.com/wp-json/web-builder/v2/forms/42/"}
	for _, fx := range loadRenderFixtures(t) {
		if strings.Contains(fx.name, ".sample") {
			continue
		}
		got, err := r.Render(fx.doc, opts)
		if err != nil {
			t.Fatalf("%s: %v", fx.name, err)
		}
		if got.HTML != fx.rr.HTML {
			t.Errorf("%s: html differs (got %d bytes, want %d)", fx.name, len(got.HTML), len(fx.rr.HTML))
		}
		if got.CSS != fx.rr.CSS || got.Renderer != fx.rr.Renderer || strings.Join(got.Classes, " ") != strings.Join(fx.rr.Classes, " ") {
			t.Errorf("%s: css/classes/renderer differ", fx.name)
		}
	}
}

func TestServerCatalogAndCreate(t *testing.T) {
	bundle, err := os.ReadFile(filepath.Join("..", "..", "web", EmbedDir, RenderFile))
	if err != nil {
		t.Skip("no server render bundle (run make builder)")
	}
	r, err := NewServerRenderer(bundle, 1)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := r.Catalog()
	if err != nil || !strings.Contains(string(cat), `"kind":"hero"`) {
		t.Fatalf("catalog: %v %.200s", err, cat)
	}
	sec, err := r.CreateSection([]byte(`{"kind":"features","content":{"title":"Why us"},"items":[{"title":"Fast","text":"Go","icon":"zap"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	doc := []byte(`{"site":{"brand":"#0a7","fontPair":"modern","corners":"soft"},"sections":[` + string(sec) + `]}`)
	res, err := ParseDoc(doc, DocOptions{})
	if err != nil || !res.Valid() {
		t.Fatalf("created section does not validate: %v %+v", err, res)
	}
	if _, err := r.CreateSection([]byte(`{"kind":"hero","design":"nope"}`)); err == nil || !strings.Contains(err.Error(), "unknown design") {
		t.Fatalf("bad design: %v", err)
	}
	rr, err := r.Render(res.JSON, ServerRenderOptions{PostID: 1, SiteURL: "https://x.test", FormActionPrefix: "https://x.test/f/"})
	if err != nil || !strings.Contains(rr.HTML, "Why us") {
		t.Fatalf("render: %v", err)
	}
}
