package builder

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// TestUnknownWidget checks that unknown element types render a comment, never panic.
func TestUnknownWidget(t *testing.T) {
	els, err := ParseElements([]byte(`[{"id":"abc1234","elType":"container","settings":[],"elements":[
		{"id":"abc1235","elType":"widget","widgetType":"no-such-widget","settings":{"a":1},"elements":[]},
		{"id":"abc1236","elType":"mystery","settings":{},"elements":[]}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Render(&Document{ID: 1, Type: "wp-page", Elements: els}, &RenderContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.HTML, `<!-- Golementor: unsupported element "no-such-widget"`) || !strings.Contains(res.HTML, `"mystery"`) {
		t.Errorf("missing comments:\n%s", res.HTML)
	}
	if _, err := RenderWidget(els[0].Elements[0], &Document{ID: 1}, &RenderContext{}); err == nil {
		t.Error("RenderWidget of an unknown widget should fail")
	}
}

var garbage = []any{nil, "", "x", "0", float64(0), float64(3), true, false, []any{}, []any{"a", float64(1)}, map[string]any{}, map[string]any{"url": 5, "id": "z", "value": []any{1}, "library": map[string]any{}, "size": "x", "unit": []any{}}}

// TestWidgetsRobust renders every widget (and the layout elements) with defaults and with
// wrongly-typed values for every control; nothing may panic.
func TestWidgetsRobust(t *testing.T) {
	repanic = true
	defer func() { repanic = false }()
	names := []string{}
	for n := range widgetDefs {
		names = append(names, n)
	}
	sort.Strings(names)
	bps := activeBreakpoints(nil)
	for _, n := range names {
		st := stackFor("widget:"+n, bps)
		if st == nil {
			continue
		}
		for gi, g := range garbage {
			settings := map[string]any{}
			if gi > 0 {
				for _, c := range st.all {
					settings[c.Name] = g
					if c.isRepeater() {
						item := map[string]any{"_id": "r1"}
						for _, f := range c.Fields {
							item[f.Name] = g
						}
						settings[c.Name] = []any{item, g}
					}
				}
			}
			el := &Element{ID: "abc1234", ElType: "widget", WidgetType: n, Settings: settings}
			doc := &Document{ID: 9, Type: "wp-page", Elements: []*Element{{ID: "abc1233", ElType: "container", Settings: map[string]any{}, Elements: []*Element{el}}}}
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s with %s: panic: %v", n, fmt.Sprintf("%#v", g), r)
					}
				}()
				if _, err := Render(doc, &RenderContext{}); err != nil {
					t.Errorf("%s: %v", n, err)
				}
				if _, err := RenderWidget(el, doc, &RenderContext{}); err != nil {
					t.Errorf("%s: RenderWidget: %v", n, err)
				}
			}()
		}
	}
	for _, kind := range []string{"section", "column", "container"} {
		st := stackFor(kind, bps)
		for _, g := range garbage {
			settings := map[string]any{}
			for _, c := range st.all {
				settings[c.Name] = g
			}
			el := &Element{ID: "abc1234", ElType: kind, Settings: settings}
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s with %#v: panic: %v", kind, g, r)
					}
				}()
				_, _ = Render(&Document{ID: 9, Elements: []*Element{el}, Settings: settings}, &RenderContext{})
			}()
		}
	}
}
