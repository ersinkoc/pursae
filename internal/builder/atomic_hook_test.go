package builder

import (
	"os"
	"strings"
	"testing"
)

// The lab page (atomic/testdata/lab) through builder.Render: the document wrapper and the
// non-atomic plumbing around the atomic renderer.
func TestAtomicDocument(t *testing.T) {
	raw, err := os.ReadFile("atomic/testdata/lab/page-165.json")
	if err != nil {
		t.Fatal(err)
	}
	els, err := ParseElements(raw)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Render(&Document{ID: 165, Type: "wp-page", Elements: els}, &RenderContext{})
	if err != nil {
		t.Fatal(err)
	}
	head := `<div data-elementor-type="wp-page" data-elementor-id="165" class="elementor elementor-165">` + "\n"
	if !strings.HasPrefix(res.HTML, head) {
		t.Errorf("wrapper: %.200q", res.HTML)
	}
	for _, want := range []string{`<h2 data-interaction-id="c8067b5" class="e-c8067b5-fd75171 g-5f18df1 g-b9f010b e-heading-base e-default-h2" id="hero-title"`,
		`x-data="eTabsf1ab1bf"`, `<details class="elementor-element`} {
		if !strings.Contains(res.HTML, want) {
			t.Errorf("missing %q", want)
		}
	}
	got := strings.Join(res.Scripts, ",")
	for _, h := range []string{"elementor-v2-frontend-handlers", "elementor-youtube-handler", "elementor-v2-alpinejs", "elementor-tabs-handler", "elementor-background-video-handler"} {
		if !strings.Contains(got, h) {
			t.Errorf("scripts %s miss %s", got, h)
		}
	}
	os.WriteFile(os.TempDir()+"/atomic-doc.html", []byte(res.HTML), 0o644)
}
