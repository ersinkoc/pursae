package atomic

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every style and every element's settings the lab saved must validate.
func TestValidateLabData(t *testing.T) {
	for _, name := range []string{"page-165.json", "component-178.json"} {
		raw, err := os.ReadFile("testdata/lab/" + name)
		if err != nil {
			t.Fatal(err)
		}
		nodes, err := ParseNodes(raw)
		if err != nil {
			t.Fatal(err)
		}
		Walk(nodes, func(n *Node) {
			if errs := ValidateSettings(n.Type(), n.Settings); len(errs) > 0 {
				t.Errorf("%s %s %s settings: %s", name, n.ID, n.Type(), JoinErrors(errs))
			}
			for _, k := range n.Styles.Keys() {
				if _, errs := ValidateStyle(n.Styles.Get(k)); len(errs) > 0 {
					t.Errorf("%s %s style %s: %s", name, n.ID, k, JoinErrors(errs))
				}
			}
		})
	}
	var gc struct {
		Items map[string]json.RawMessage `json:"items"`
	}
	raw, _ := os.ReadFile("testdata/lab/global-classes.json")
	if err := json.Unmarshal(raw, &gc); err != nil {
		t.Fatal(err)
	}
	for id, item := range gc.Items {
		v, _ := Decode(item)
		if _, errs := ValidateStyle(v); len(errs) > 0 {
			t.Errorf("class %s: %s", id, JoinErrors(errs))
		}
	}
}

// The lab's REST captures (capture-v4/rest/curl 11, 13, 76): upstream's error strings.
func TestValidateStyleErrors(t *testing.T) {
	style := func(label string, meta map[string]any, props map[string]any) map[string]any {
		return map[string]any{"id": "g-curl002", "type": "class", "label": label,
			"variants": []any{map[string]any{"meta": meta, "props": props, "custom_css": nil}}}
	}
	color := map[string]any{"color": map[string]any{"$$type": "color", "value": "#123456"}}
	cases := []struct {
		style map[string]any
		want  string
	}{
		{style("Bad Label With Spaces!!", map[string]any{"breakpoint": nil, "state": nil}, map[string]any{}),
			"label: class_name_contains_spaces, meta.breakpoint: missing_or_invalid_value"},
		{style("Bad Label", map[string]any{"breakpoint": "desktop", "state": nil}, color), "label: class_name_contains_spaces"},
		{style("curl-class-renamed", map[string]any{"breakpoint": nil, "state": "hover"}, color), "meta.breakpoint: missing_or_invalid_value"},
		{style("ok-class", map[string]any{"breakpoint": "desktop", "state": "bogus"}, color), "meta.state: missing_or_invalid_value"},
		{style("ok-class", map[string]any{"breakpoint": "desktop", "state": nil},
			map[string]any{"font-size": map[string]any{"$$type": "size", "value": map[string]any{"size": "big", "unit": "px"}}}),
			"variants[0].font-size: invalid_value"},
		{style("ok-class", map[string]any{"breakpoint": "desktop", "state": nil},
			map[string]any{"color": map[string]any{"$$type": "size", "value": "#fff"}}), "variants[0].color: invalid_value"},
		{style("ok-class", map[string]any{"breakpoint": "tablet", "state": "hover"},
			map[string]any{"font-size": map[string]any{"$$type": "size", "value": map[string]any{"size": 0, "unit": "px"}},
				"unknown-prop": "dropped"}), ""},
		{style("1abc", map[string]any{"breakpoint": "desktop", "state": nil}, nil), "label: class_name_starts_with_digit"},
		{style("container", map[string]any{"breakpoint": "desktop", "state": nil}, nil), "label: reserved_class_name"},
	}
	for i, c := range cases {
		out, errs := ValidateStyle(c.style)
		if got := JoinErrors(errs); got != c.want {
			t.Errorf("case %d: got %q want %q", i, got, c.want)
		}
		if c.want == "" {
			props := asMap(asMap(listOf(asMap(out).Get("variants"))[0]).Get("props"))
			if props.Has("unknown-prop") || !props.Has("font-size") {
				t.Errorf("case %d: props not reduced to the schema: %v", i, props.Keys())
			}
		}
	}
}

// The FAQ schema of an accordion with marked-up titles and answers (lab page 330, capture
// scratchpad gaps/atomic/lab-faq.html): upstream's markdown → plain text pipeline.
func TestFAQSchemaGolden(t *testing.T) {
	env, _, _ := labEnv(t)
	raw, _ := os.ReadFile("testdata/lab/faq-accordion.json")
	nodes, err := ParseNodes(raw)
	if err != nil {
		t.Fatal(err)
	}
	out := env.Render(nodes[0])
	want, _ := os.ReadFile("testdata/lab/faq-accordion.jsonld.html")
	w := strings.TrimSpace(string(want))
	i := strings.Index(out, `<script type="application/ld+json">`)
	if i < 0 {
		t.Fatalf("no JSON-LD in %q", out)
	}
	got := out[i : i+strings.Index(out[i:], "</script>")+len("</script>")]
	if got != w {
		t.Errorf("FAQ JSON-LD\n got: %s\nwant: %s", got, w)
	}
}

// An atomic form with every field widget (lab page 314): the front-end markup matches upstream
// apart from the per-request default ids of fields without an id (Default_Id_Provider uses
// wp_unique_id; here they are the captured defaults).
func TestFormGolden(t *testing.T) {
	env, _, _ := labEnv(t)
	raw, _ := os.ReadFile("testdata/lab/form.json")
	nodes, err := ParseNodes(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(env.Render(nodes[0]))
	want, _ := os.ReadFile("testdata/lab/form.html")
	norm := regexp.MustCompile(`(e-form-(?:radio-button|date-picker|time-picker|select|file-upload))-\d+`)
	g, w := norm.ReplaceAllString(got, "$1-N"), norm.ReplaceAllString(strings.TrimSpace(string(want)), "$1-N")
	if g != w {
		t.Errorf("form markup differs at byte %d:\n got: %q\nwant: %q", diffAt(g, w), around(g, diffAt(g, w)), around(w, diffAt(g, w)))
	}
}
