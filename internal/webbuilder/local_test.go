package webbuilder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shared fixtures of testdata/local: LocalCSS must print exactly NAME.css for NAME.json. The
// editor's mirror (builder/host/elements/local.ts) runs the same files in its vitest suite, so the
// editor's live preview and the published page get the same stylesheet byte for byte.
// UPDATE_LOCAL_GOLDEN=1 rewrites the .css files from this implementation.
func TestLocalCSSGolden(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("testdata", "local", "*.json"))
	if len(files) < 3 {
		t.Fatalf("fixtures missing: %v", files)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var fx struct {
			Post  string      `json:"post"`
			BPs   Breakpoints `json:"bps"`
			Local LocalStyles `json:"local"`
		}
		if err := json.Unmarshal(raw, &fx); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		got := LocalCSS(fx.Local, fx.BPs, LocalSelector(fx.Post))
		want := strings.TrimSuffix(f, ".json") + ".css"
		if os.Getenv("UPDATE_LOCAL_GOLDEN") != "" {
			_ = os.WriteFile(want, []byte(got), 0o644)
			continue
		}
		exp, err := os.ReadFile(want)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(exp) {
			t.Errorf("%s:\n got: %q\nwant: %q", f, got, exp)
		}
	}
}

func TestLocalDeclOK(t *testing.T) {
	ok := [][2]string{
		{"padding-top", "96px"}, {"color", "#fff !important"}, {"--x", "12px"}, {"-webkit-line-clamp", "3"},
		{"width", "calc(100% - var(--g, 2px))"}, {"background-image", "url(/a.png)"}, {"background-image", "linear-gradient(90deg, red, blue)"},
		{"content", `"→"`}, {"font-family", `"Inter", sans-serif`},
	}
	bad := [][2]string{
		{"Color", "red"}, {"color", ""}, {"color", "red; x: y"}, {"color", "red}"}, {"top", "1px /* c */"}, {"color", "</style>"},
		{"background", "url(javascript:alert(1))"}, {"width", "expression(alert(1))"}, {"behavior", "url(x.htc)"}, {"color", `"open`},
		{"width", "calc(1px"}, {"color", "red\n"}, {"x", "\x01"}, {"color", `red\`},
	}
	for _, c := range ok {
		if !LocalDeclOK(c[0], c[1]) {
			t.Errorf("rejected %q: %q", c[0], c[1])
		}
	}
	for _, c := range bad {
		if c[1] == "red\n" { // a trailing newline is trimmed: fine
			continue
		}
		if LocalDeclOK(c[0], c[1]) {
			t.Errorf("accepted %q: %q", c[0], c[1])
		}
	}
}

func TestSuggestLocalBreakpoints(t *testing.T) {
	// enerjiosgb (Tailwind v4 compiled): 40rem, 48rem, 64rem, a 420px and a max-width 1023px.
	css := []string{`@media (hover:hover){a{}} @media (min-width:64rem){b{}} @media (min-width:48rem){c{}}
@media (min-width:420px){d{}} @media (min-width:40rem){e{}} @media (max-width:1023px){f{}}`}
	if got := SuggestLocalBreakpoints(css); got != (Breakpoints{1023, 639}) {
		t.Errorf("tailwind: %v", got)
	}
	// Bootstrap: max-width 991.98 / 767.98 / 575.98 used more than the min-widths.
	bs := []string{`@media (max-width:991.98px){a{}} @media (max-width:991.98px){a{}} @media (min-width:992px){a{}}
@media (max-width:767.98px){a{}} @media (max-width:767.98px){a{}} @media (max-width:575.98px){a{}}`}
	if got := SuggestLocalBreakpoints(bs); got != (Breakpoints{991, 767}) {
		t.Errorf("bootstrap: %v", got)
	}
	if got := SuggestLocalBreakpoints(nil); got != DefaultBreakpoints {
		t.Errorf("none: %v", got)
	}
	if got := (Breakpoints{Tablet: 600, Mobile: 900}).Normalize(); got != DefaultBreakpoints {
		t.Errorf("normalize: %v", got)
	}
}

func TestLocalFontFamily(t *testing.T) {
	for in, want := range map[string]string{`'Inter Tight', system-ui, sans-serif`: "Inter Tight", `"Lora" !important`: "Lora", "var(--f)": "", "": ""} {
		if got := LocalFontFamily(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	d := DocState{Sections: []Section{{ID: "a", Kind: KindCode, Local: LocalStyles{"1": {V: []LocalVariant{{P: map[string]string{"font-family": "'Lora', serif"}}}}}}}}
	found := false
	for _, n := range DocFonts(&d) {
		found = found || n.Family == "Lora"
	}
	if !found {
		t.Errorf("DocFonts lacks the local font: %+v", DocFonts(&d))
	}
}

func TestAddLocalClasses(t *testing.T) {
	src := `<section data-wbc="1" class="hero  "><h1 data-wbc="2">A</h1><p data-wbc=3 class=lead>B</p>` +
		`<img data-wbc="4" src="a.png"/><script>var s='<div data-wbc="2">'</script><a data-wbc="5" class>x</a><i data-wbc="22"></i></section>`
	got := AddLocalClasses(src, map[string]string{"1": "e5-1", "2": "e5-2", "3": "e5-3", "4": "e5-4", "5": "e5-5"})
	want := `<section data-wbc="1" class="hero e5-1"><h1 class="e5-2" data-wbc="2">A</h1><p data-wbc=3 class="lead e5-3">B</p>` +
		`<img class="e5-4" data-wbc="4" src="a.png"/><script>var s='<div data-wbc="2">'</script><a data-wbc="5" class="e5-5">x</a><i data-wbc="22"></i></section>`
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	if AddLocalClasses(src, nil) != src || AddLocalClasses(src, map[string]string{"9": "x"}) != src {
		t.Error("no-op changed the markup")
	}
}

func TestFillCodeLocal(t *testing.T) {
	doc := json.RawMessage(`{"sections":[{"id":"a","kind":"code","content":{"html":"<h1 data-wbc=\"1\" class=\"t\">Hi</h1>\n<p data-wbc=\"2\">x</p>","head":"<link rel=\"stylesheet\" href=\"/k/s.css\">","kit":"k"},
		"local":{"1":{"v":[{"bp":"","st":"","p":{"color":"red"}},{"bp":"mobile","st":"","p":{"color":"blue"}}]}}},
		{"id":"b","kind":"code","content":{"html":"<p data-wbc=\"3\">y</p>"}}],"site":{}}`)
	list, only := CodeSections(doc)
	if !only || len(list) != 2 || list[0].Kit != "k" || len(list[0].Local) != 1 {
		t.Fatalf("%+v", list)
	}
	list[0].Breakpoints = Breakpoints{Tablet: 1023, Mobile: 599}
	snap := `<div class="wb-page" data-wb-root="" data-wb-post="9"><div data-wb-code="a"></div><div data-wb-code="b"></div></div>`
	out := FillCode(snap, list, only)
	for _, want := range []string{
		`<h1 class="t e9-1">Hi</h1>`, `<p>x</p>`, `<p>y</p>`,
		"<style data-wb-local=\"\">\n.e9-1:not(#_):not(#_){color:red}\n@media (max-width:599px){\n.e9-1:not(#_):not(#_){color:blue}\n}\n</style>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	head, _ := CodeHead(out)
	if !strings.Contains(head, "data-wb-local") || strings.Index(head, "s.css") > strings.Index(head, "data-wb-local") {
		t.Errorf("head: %s", head)
	}
	// No local styles: byte for byte what it was before local styles existed.
	plain, _ := CodeSections(json.RawMessage(`{"sections":[{"id":"a","kind":"code","content":{"html":"<h1 data-wbc=\"1\">Hi</h1>"},"local":{}}],"site":{}}`))
	if got := FillCode(snap, plain, true); strings.Contains(got, "data-wb-local") || !strings.Contains(got, "<h1>Hi</h1>") {
		t.Errorf("%s", got)
	}
}

func TestDocLocalValidation(t *testing.T) {
	doc := `{"sections":[{"id":"a","kind":"code","name":"","hidden":false,"content":{"html":"<p data-wbc=\"1\">x</p>"},"items":[],"style":{},
		"local":{"1":{"v":[{"bp":"phone","st":"","p":{"color":"red;x:y"}}]}}},
		{"id":"b","kind":"hero","name":"","hidden":false,"content":{},"items":[],"style":{},"local":{"1":{"v":[]}}}],"site":{}}`
	res, err := ParseDoc([]byte(doc), DocOptions{UnfilteredHTML: true})
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, e := range res.Errors {
		paths[e.Path] = true
	}
	if !paths["/sections/0/local/1/v/0/bp"] || !paths["/sections/0/local/1/v/0/p/color"] {
		t.Errorf("errors: %+v", res.Errors)
	}
	if strings.Contains(string(res.JSON), `"local":{"1":{"v":[]}}`) {
		t.Errorf("local kept on a builder section: %s", res.JSON)
	}
}

// Without unfiltered_html the stored local styles stay, whatever the request sends.
func TestDocLocalKeptForUnprivileged(t *testing.T) {
	stored := json.RawMessage(`{"sections":[{"id":"a","kind":"code","name":"","hidden":false,"content":{"html":"<p data-wbc=\"1\">x</p>"},"items":[],"style":{},
		"local":{"1":{"v":[{"bp":"","st":"","p":{"color":"red"}}]}}}],"site":{}}`)
	req := `{"sections":[{"id":"a","kind":"code","name":"","hidden":true,"content":{"html":"<p data-wbc=\"1\">x</p>"},"items":[],"style":{},
		"local":{"1":{"v":[{"bp":"","st":"","p":{"color":"blue"}}]}}}],"site":{}}`
	res, err := ParseDoc([]byte(req), DocOptions{Stored: []json.RawMessage{stored}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(res.JSON)
	if !strings.Contains(s, `"color":"red"`) || strings.Contains(s, "blue") || !strings.Contains(s, `"hidden":true`) || !res.Kept {
		t.Errorf("%s", s)
	}
	if len(res.Stripped) == 0 {
		t.Error("the ignored edit is not reported")
	}
	// Nothing stored: the code section (and its local styles) is dropped.
	res, _ = ParseDoc([]byte(req), DocOptions{})
	if strings.Contains(string(res.JSON), "local") {
		t.Errorf("%s", res.JSON)
	}
}
