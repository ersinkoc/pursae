package builder

import (
	"strings"
	"testing"
)

// TestProCGolden checks the fork-C Pro widget fixtures (testdata/golden/pro-c-*):
// CSS must match rule set for rule set.
func TestProCGolden(t *testing.T) {
	rc := testRC(t)
	n := 0
	for _, f := range loadFixtures(t) {
		if !strings.HasPrefix(f.name, "pro-c-") {
			continue
		}
		n++
		if proFixtureCurrent != nil {
			proFixtureCurrent(f.doc)
		}
		res, err := Render(f.doc, rc)
		if err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		got := normalizeHTML(res.HTML)
		want := normalizeHTML(f.wantHTML)
		if got != want {
			t.Errorf("%s: HTML differs%s", f.name, firstDiff(got, want))
		}
		wantRules := splitRules(f.wantCSS)
		have := map[string]bool{}
		for _, r := range splitRules(res.CSS) {
			have[r] = true
		}
		m := 0
		for _, r := range wantRules {
			if have[r] {
				m++
			} else {
				t.Errorf("%s: missing CSS rule %s", f.name, r)
			}
		}
		t.Logf("%s: %d/%d CSS rule sets", f.name, m, len(wantRules))
	}
	if n == 0 {
		t.Skip("no pro-c fixtures")
	}
}

// TestProCDefaults renders every fork-C widget with its control defaults, on the page and in the
// editor, without panicking.
func TestProCDefaults(t *testing.T) {
	repanic = true
	defer func() { repanic = false }()
	for _, name := range []string{"media-carousel", "testimonial-carousel", "reviews", "nested-carousel", "gallery", "off-canvas", "mega-menu"} {
		el := &Element{ID: "cd00001", ElType: "widget", WidgetType: name, Settings: map[string]any{}}
		doc := &Document{ID: 1, Type: "wp-page", Elements: []*Element{el}}
		res, err := Render(doc, &RenderContext{})
		// a gallery without images renders nothing, like upstream
		if err != nil || (name != "gallery" && !strings.Contains(res.HTML, "elementor-widget-")) {
			t.Errorf("%s: render failed: %v", name, err)
		}
		if _, err := RenderWidget(el, doc, &RenderContext{}); err != nil {
			t.Errorf("%s: editor render: %v", name, err)
		}
	}
}
