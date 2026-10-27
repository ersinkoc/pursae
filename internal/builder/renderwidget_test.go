package builder

import (
	"strings"
	"testing"
)

// TestRenderWidgetCaptured compares with the render_widget response captured from the lab
// (capture/curl/05-render_widget): heading in edit mode gets inline-editing attributes.
func TestRenderWidgetCaptured(t *testing.T) {
	el := &Element{ID: "c1b2c3d", ElType: "widget", WidgetType: "heading", Settings: map[string]any{"title": "Rendered on server", "header_size": "h3"}}
	got, err := RenderWidget(el, &Document{ID: 16, Type: "wp-page"}, &RenderContext{})
	if err != nil {
		t.Fatal(err)
	}
	want := "\t\t\t<h3 class=\"elementor-heading-title elementor-size-default elementor-inline-editing\" data-elementor-setting-key=\"title\">Rendered on server</h3>\t\t"
	if normalizeHTML(got) != normalizeHTML(want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// TestCustomBreakpoints checks responsive duplication and media queries with extra breakpoints.
func TestCustomBreakpoints(t *testing.T) {
	kit := &Document{ID: 4, Type: "kit", Settings: map[string]any{
		"active_breakpoints": []any{"viewport_mobile", "viewport_tablet", "viewport_laptop", "viewport_widescreen"},
		"viewport_laptop":    1300,
	}}
	els, err := ParseElements([]byte(`[{"id":"aaaaaa1","elType":"widget","widgetType":"heading","settings":{"title":"x","align":"left","align_laptop":"center","align_widescreen":"right","align_mobile":"justify"},"elements":[]}]`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Render(&Document{ID: 7, Type: "wp-page", Elements: els}, &RenderContext{Kit: kit})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"@media(min-width:2400px){.elementor-7 .elementor-element.elementor-element-aaaaaa1{text-align:end;}}",
		"@media(max-width:1300px){.elementor-7 .elementor-element.elementor-element-aaaaaa1{text-align:center;}}",
		"@media(max-width:767px){.elementor-7 .elementor-element.elementor-element-aaaaaa1{text-align:justify;}}",
	} {
		if !strings.Contains(res.CSS, want) {
			t.Errorf("missing %s in\n%s", want, res.CSS)
		}
	}
}
