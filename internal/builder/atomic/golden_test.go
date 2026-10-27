package atomic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The golden fixtures in testdata/lab come from the upstream lab (WordPress + upstream 4.3.5 +
// Pro 4.3.0, atomic experiments on): a page (post 165) built in the v4 editor with local styles,
// two global classes, three variables and an instance of component 178, its published HTML and
// the CSS files the styles manager wrote (scratchpad tool gwe-tools/atomic_fixtures.py).

func labEnv(t *testing.T) (*Env, *GlobalClasses, map[string]Variable) {
	t.Helper()
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", "lab", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var gc struct {
		Order []string                   `json:"order"`
		Items map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(read("global-classes.json"), &gc); err != nil {
		t.Fatal(err)
	}
	classes := &GlobalClasses{}
	for _, id := range gc.Order {
		v, _ := Decode(gc.Items[id])
		m := asMap(v)
		classes.Items = append(classes.Items, GlobalClass{ID: id, Label: phpString(m.Get("label")), Variants: listOf(m.Get("variants"))})
	}
	var vars struct {
		Data map[string]struct {
			Type  string          `json:"type"`
			Label string          `json:"label"`
			Value json.RawMessage `json:"value"`
		} `json:"data"`
	}
	if err := json.Unmarshal(read("variables.json"), &vars); err != nil {
		t.Fatal(err)
	}
	variables := map[string]Variable{}
	for id, v := range vars.Data {
		val, _ := Decode(v.Value)
		variables[id] = Variable{Label: v.Label, Type: v.Type, Value: VariableValue(val)}
	}
	env := &Env{
		AssetsURL: "http://localhost:8095/wp-content/plugins/elementor/assets/",
		Image: func(id int64, size string) (Image, bool) {
			if id != 166 {
				return Image{}, false
			}
			return Image{URL: "http://localhost:8095/wp-content/uploads/2026/10/w-logo-blue.png", Width: 80, Height: 80}, true
		},
		SVG: func(id int64, url string) string {
			const assets = "http://localhost:8095/wp-content/plugins/elementor/assets/"
			if strings.HasPrefix(url, assets) {
				b, _ := os.ReadFile("../../../web/gowebeditor/" + strings.TrimPrefix(url, assets))
				return string(b)
			}
			return ""
		},
		Variable:   func(id string) (Variable, bool) { v, ok := variables[id]; return v, ok },
		ClassLabel: classes.Label,
		Component: func(id int64) ([]byte, bool) {
			if id == 178 {
				return read("component-178.json"), true
			}
			return nil, false
		},
	}
	return env, classes, variables
}

func TestLabPageHTML(t *testing.T) {
	env, _, _ := labEnv(t)
	raw, _ := os.ReadFile("testdata/lab/page-165.json")
	nodes, err := ParseNodes(raw)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, n := range nodes {
		b.WriteString(env.Render(n))
	}
	want, _ := os.ReadFile("testdata/lab/page-165.html")
	w := string(want)
	w = w[strings.Index(w, ">\n")+2:]
	w = strings.TrimPrefix(w, "\t\t\t\t")
	w = w[:strings.LastIndex(w, "\t\t</div>")]
	// WordPress' content filter adds decoding="async" to images.
	w = strings.ReplaceAll(w, `<img decoding="async" `, `<img `)
	got := b.String()
	if got != w {
		os.WriteFile(filepath.Join(t.TempDir(), "got.html"), []byte(got), 0o644)
		t.Errorf("page HTML differs at byte %d:\n got: %q\nwant: %q", diffAt(got, w), around(got, diffAt(got, w)), around(w, diffAt(got, w)))
	}
}

func diffAt(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func around(s string, i int) string {
	lo, hi := i-120, i+200
	if lo < 0 {
		lo = 0
	}
	if hi > len(s) {
		hi = len(s)
	}
	return s[lo:hi]
}

func TestLabCSSFiles(t *testing.T) {
	env, classes, _ := labEnv(t)
	page, _ := os.ReadFile("testdata/lab/page-165.json")
	comp, _ := os.ReadFile("testdata/lab/component-178.json")
	pageNodes, err := ParseNodes(page)
	if err != nil {
		t.Fatal(err)
	}
	compNodes, _ := ParseNodes(comp)
	ids := append(ClassIDs(pageNodes), ClassIDs(compNodes)...)
	sources := []StyleSource{
		{Key: "base", Styles: BaseStyles()},
		{Key: "global-165-frontend", Styles: classes.StylesFor(ids)},
		{Key: "local-165-frontend", Styles: LocalStyles(pageNodes)},
		{Key: "local-178-frontend", Styles: LocalStyles(compNodes)},
	}
	bps := []Breakpoint{{Name: "mobile", Value: 767, Direction: "max"}, {Name: "tablet", Value: 1024, Direction: "max"}}
	files := env.CSSFiles(sources, bps)
	got := map[string]CSSFile{}
	var order []string
	for _, f := range files {
		got[f.Handle] = f
		order = append(order, f.Handle+" "+f.Media)
	}
	wantOrder := []string{"base-desktop all", "global-165-frontend-desktop all", "local-165-frontend-desktop all", "local-178-frontend-desktop all",
		"local-165-frontend-tablet @media(max-width:1024px)", "base-mobile @media(max-width:767px)", "global-165-frontend-mobile @media(max-width:767px)",
		"local-165-frontend-mobile @media(max-width:767px)"}
	if strings.Join(order, "\n") != strings.Join(wantOrder, "\n") {
		t.Errorf("files:\n%s\nwant:\n%s", strings.Join(order, "\n"), strings.Join(wantOrder, "\n"))
	}
	entries, _ := os.ReadDir("testdata/lab/css")
	matched := 0
	for _, e := range entries {
		handle := strings.TrimSuffix(e.Name(), ".css")
		if strings.Contains(handle, "-preview-") {
			continue
		}
		want, _ := os.ReadFile(filepath.Join("testdata/lab/css", e.Name()))
		g := got[handle].CSS
		if g != string(want) {
			i := diffAt(g, string(want))
			t.Errorf("%s differs at %d:\n got: %q\nwant: %q", handle, i, around(g, i), around(string(want), i))
			continue
		}
		matched++
	}
	t.Logf("%d CSS files match byte-for-byte", matched)
}
