package builder

import (
	"strings"
	"testing"
)

// The group A Pro widgets render with their default settings in the editor (render_widget) and
// never reference external hosts.
func TestProAWidgetsEditorDefaults(t *testing.T) {
	repanic = true
	defer func() { repanic = false }()
	for _, name := range []string{"blockquote", "flip-box", "call-to-action", "price-list", "price-table", "countdown",
		"animated-headline", "progress-tracker", "code-highlight", "lottie"} {
		el := &Element{ID: "a9" + strings.Repeat("0", 5), ElType: "widget", WidgetType: name, Settings: map[string]any{}}
		out, err := RenderWidget(el, &Document{ID: 1}, &RenderContext{HomeURL: "http://x.test"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.TrimSpace(out) == "" {
			t.Errorf("%s: empty render", name)
		}
		if strings.Contains(out, "cdnjs") || strings.Contains(out, "unsupported element") {
			t.Errorf("%s: unexpected output %.200s", name, out)
		}
	}
}

func TestProAShareLink(t *testing.T) {
	got := proAAddQueryArg(proAAddQueryArg("https://twitter.com/intent/tweet", "text", proARawURLEncode("a b&c")+" — X"), "via", "go")
	if want := "https://twitter.com/intent/tweet?text=a+b%26c+%E2%80%94+X&via=go"; got != want {
		t.Errorf("got %s", got)
	}
}
