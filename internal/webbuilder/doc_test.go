package webbuilder

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
)

func readSample(t testing.TB) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/doc-sample.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustParse(t testing.TB, raw string, opts DocOptions) *DocResult {
	t.Helper()
	r, err := ParseDoc([]byte(raw), opts)
	if err != nil {
		t.Fatalf("ParseDoc: %v", err)
	}
	return r
}

func TestParseDocSampleStrip(t *testing.T) {
	raw := readSample(t)
	r := mustParse(t, string(raw), DocOptions{})
	if !r.Valid() {
		t.Fatalf("sample invalid: %+v", r.Errors)
	}
	if strings.Join(r.Stripped, ",") != StrippedCustomCSS+","+StrippedHTMLSections {
		t.Fatalf("stripped=%v", r.Stripped)
	}
	for _, s := range r.Doc.Sections {
		if s.Kind == KindHTML {
			t.Fatal("html section kept without unfiltered_html")
		}
		if s.Style.CSS != "" {
			t.Fatal("custom css kept without unfiltered_html")
		}
	}
	js := string(r.JSON)
	for _, bad := range []string{"onload", "javascript:", `"html":`} {
		if strings.Contains(js, bad) {
			t.Errorf("normalized doc still contains %q", bad)
		}
	}
	// Unknown fields survive at every level.
	var m map[string]any
	_ = json.Unmarshal(r.JSON, &m)
	if m["clientExtra"] != true {
		t.Error("top-level unknown field lost")
	}
	secs := m["sections"].([]any)
	hero := secs[1].(map[string]any)
	if hero["unknownTopLevel"] == nil || hero["content"].(map[string]any)["futureField"] != "kept" {
		t.Error("section unknown fields lost")
	}

	priv := mustParse(t, string(raw), DocOptions{UnfilteredHTML: true})
	if !priv.Valid() || len(priv.Stripped) != 0 {
		t.Fatalf("privileged: errors=%v stripped=%v", priv.Errors, priv.Stripped)
	}
	found := false
	for _, s := range priv.Doc.Sections {
		if s.Kind == KindHTML && s.Content["html"] != "" {
			found = true
		}
		if s.ID == "text" && s.Style.CSS == "" {
			t.Error("custom css lost for unfiltered_html")
		}
	}
	if !found {
		t.Error("html section lost for unfiltered_html")
	}
}

func TestParseDocRichAndSVG(t *testing.T) {
	r := mustParse(t, string(readSample(t)), DocOptions{UnfilteredHTML: true})
	var hero, features *Section
	for i := range r.Doc.Sections {
		switch r.Doc.Sections[i].ID {
		case "hero":
			hero = &r.Doc.Sections[i]
		case "features":
			features = &r.Doc.Sections[i]
		}
	}
	if got := hero.Bg.DecorSVG; strings.Contains(got, "script") || strings.Contains(got, "onload") || !strings.Contains(got, "<circle") {
		t.Errorf("decorSvg=%q", got)
	}
	if got := features.Items[1].Text; got != "Sanitized twice." {
		t.Errorf("rich item text=%q", got)
	}
	if got := hero.Content["title"]; got != "Build pages <b>faster</b>" {
		t.Errorf("rich title=%q", got)
	}
	// Plain text is not rich: stored as typed, escaped by every renderer.
	if got := hero.Content["body"]; !strings.Contains(got, "& more") {
		t.Errorf("plain body changed: %q", got)
	}
}

func TestParseDocCaseVariants(t *testing.T) {
	raw := `{"sections":[{"id":"a","kind":"hero","Kind":"html","name":"x","hidden":false,"content":{},"items":[],
		"style":{"palette":"brand","align":"left","padding":0,"titleSize":1,"hideOn":[],"CSS":"x{}"}}],"site":{"brand":"#000","fontPair":"plain","corners":"soft"}}`
	r := mustParse(t, raw, DocOptions{})
	if !r.Valid() {
		t.Fatal(r.Errors)
	}
	if r.Doc.Sections[0].Kind != "hero" || strings.Contains(string(r.JSON), "CSS") || strings.Contains(string(r.JSON), "Kind") {
		t.Fatalf("case variants survived: %s", r.JSON)
	}
}

func errCodes(r *DocResult) map[string]string {
	m := map[string]string{}
	for _, e := range r.Errors {
		m[e.Path] = e.Code
	}
	return m
}

func TestParseDocValidation(t *testing.T) {
	sec := func(id, kind, extra string) string {
		return `{"id":"` + id + `","kind":"` + kind + `","name":"n","hidden":false,"content":{},"items":[],"style":{"palette":"light","align":"left","padding":0,"titleSize":1,"hideOn":[]}` + extra + `}`
	}
	doc := func(secs ...string) string {
		return `{"sections":[` + strings.Join(secs, ",") + `],"site":{"brand":"#000","fontPair":"plain","corners":"soft"}}`
	}
	cases := []struct {
		name, raw, path, code string
	}{
		{"kind", doc(sec("a", "bogus", "")), "/doc/sections/0/kind", VErrKind},
		{"id", doc(sec("a b", "text", "")), "/doc/sections/0/id", VErrID},
		{"dup", doc(sec("a", "text", ""), sec("a", "text", "")), "/doc/sections/1/id", VErrID},
		{"href", doc(`{"id":"a","kind":"cta","name":"n","hidden":false,"content":{"primaryHref":"javascript:alert(1)"},"items":[],"style":{"palette":"light","align":"left","padding":0,"titleSize":1,"hideOn":[]}}`), "/doc/sections/0/content/primaryHref", VErrURL},
		{"item href", doc(`{"id":"a","kind":"nav","name":"n","hidden":false,"content":{},"items":[{"id":"i","title":"t","text":"","href":"vbscript:x"}],"style":{"palette":"light","align":"left","padding":0,"titleSize":1,"hideOn":[]}}`), "/doc/sections/0/items/0/href", VErrURL},
		{"image", doc(`{"id":"a","kind":"image","name":"n","hidden":false,"content":{"image":"data:text/html,x"},"items":[],"style":{"palette":"light","align":"left","padding":0,"titleSize":1,"hideOn":[]}}`), "/doc/sections/0/content/image", VErrURL},
		{"custom image", doc(`{"id":"a","kind":"custom","name":"n","hidden":false,"content":{},"items":[{"id":"i","title":"javascript:x","text":"","node":"image"}],"style":{"palette":"light","align":"left","padding":0,"titleSize":1,"hideOn":[]}}`), "/doc/sections/0/items/0/title", VErrURL},
		{"css value", doc(sec("a", "text", `,"els":{"title":{"bgImage":"url(javascript:alert(1))"}}`)), "/doc/sections/0/els/title/bgImage", VErrCSS},
		{"border", doc(sec("a", "text", `,"els":{"title":{"border":"1px solid red;background:url(javascript:x)"}}`)), "/doc/sections/0/els/title/border", VErrCSS},
		{"palette", doc(`{"id":"a","kind":"text","name":"n","hidden":false,"content":{},"items":[],"style":{"palette":"neon","align":"left","padding":0,"titleSize":1,"hideOn":[]}}`), "/doc/sections/0/style/palette", VErrEnum},
		{"hideOn", doc(`{"id":"a","kind":"text","name":"n","hidden":false,"content":{},"items":[],"style":{"palette":"light","align":"left","padding":0,"titleSize":1,"hideOn":["watch"]}}`), "/doc/sections/0/style/hideOn/0", VErrEnum},
		{"cssId", doc(`{"id":"a","kind":"text","name":"n","hidden":false,"content":{},"items":[],"style":{"palette":"light","align":"left","padding":0,"titleSize":1,"hideOn":[],"cssId":"x\" onclick=\"y"}}`), "/doc/sections/0/style/cssId", VErrID},
		{"entrance", doc(sec("a", "text", `,"els":{"title":{"motion":{"entrance":"explode"}}}`)), "/doc/sections/0/els/title/motion/entrance", VErrEnum},
		{"fontPair", `{"sections":[],"site":{"brand":"#000","fontPair":"comic","corners":"soft"}}`, "/doc/site/fontPair", VErrEnum},
		{"type", `{"sections":[{"id":1}],"site":{}}`, "", VErrEnum},
		{"required", `{"site":{}}`, "/doc/sections", VErrRequired},
		{"long", doc(`{"id":"a","kind":"text","name":"n","hidden":false,"content":{"title":"` + strings.Repeat("x", MaxStringBytes+1) + `"},"items":[],"style":{"palette":"light","align":"left","padding":0,"titleSize":1,"hideOn":[]}}`), "/doc/sections/0/content/title", VErrTooLong},
	}
	for _, c := range cases {
		r := mustParse(t, c.raw, DocOptions{PathPrefix: "/doc"})
		if r.Valid() {
			t.Errorf("%s: valid, want %s at %s", c.name, c.code, c.path)
			continue
		}
		codes := errCodes(r)
		if c.path == "" {
			if r.Errors[0].Code != c.code {
				t.Errorf("%s: errors=%+v", c.name, r.Errors)
			}
			continue
		}
		if codes[c.path] != c.code {
			t.Errorf("%s: errors=%+v, want %s at %s", c.name, r.Errors, c.code, c.path)
		}
	}
}

func TestParseDocLimits(t *testing.T) {
	if _, err := ParseDoc(make([]byte, MaxDocBytes+1), DocOptions{}); err != ErrDocTooLarge {
		t.Errorf("too large: %v", err)
	}
	for _, raw := range []string{``, `[]`, `{"sections":[]} x`, `{`} {
		if _, err := ParseDoc([]byte(raw), DocOptions{}); err == nil {
			t.Errorf("%q parsed", raw)
		}
	}
	// Too many sections.
	var secs []string
	for i := 0; i <= MaxSections; i++ {
		secs = append(secs, `{"id":"s`+itoa(i)+`","kind":"text","name":"n","hidden":false,"content":{},"items":[],"style":{"palette":"light","align":"left","padding":0,"titleSize":1,"hideOn":[]}}`)
	}
	r := mustParse(t, `{"sections":[`+strings.Join(secs, ",")+`],"site":{}}`, DocOptions{})
	if errCodes(r)["/sections"] != VErrTooMany {
		t.Errorf("too many sections: %+v", r.Errors)
	}
	// Too deep.
	inner := `{"id":"x0","kind":"layout","name":"n","hidden":false,"content":{},"items":[],"style":{"palette":"light","align":"left","padding":0,"titleSize":1,"hideOn":[]}}`
	for i := 1; i <= MaxDepth+1; i++ {
		inner = `{"id":"x` + itoa(i) + `","kind":"layout","name":"n","hidden":false,"content":{},"items":[{"id":"c","title":"layout","text":"","sub":` + inner + `}],"style":{"palette":"light","align":"left","padding":0,"titleSize":1,"hideOn":[]}}`
	}
	r = mustParse(t, `{"sections":[`+inner+`],"site":{}}`, DocOptions{})
	deep := false
	for _, e := range r.Errors {
		if e.Code == VErrTooDeep {
			deep = true
		}
	}
	if !deep {
		t.Errorf("too deep: %+v", r.Errors)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestParseSection(t *testing.T) {
	html := `{"id":"h","kind":"html","name":"Embed","hidden":false,"content":{"html":"<b>x</b>"},"items":[],"style":{"palette":"light","align":"left","padding":0,"titleSize":1,"hideOn":[]}}`
	r, err := ParseSection([]byte(html), DocOptions{})
	if err != nil || r.Section != nil || string(r.JSON) != "null" || len(r.Stripped) != 1 {
		t.Fatalf("html section for subscriber: %+v %v", r, err)
	}
	r, err = ParseSection([]byte(html), DocOptions{UnfilteredHTML: true})
	if err != nil || r.Section == nil || !r.Valid() {
		t.Fatalf("html section for admin: %+v %v", r, err)
	}
	hero := `{"id":"a","kind":"hero","name":"H","hidden":false,"content":{"title":"<b>x</b><img src=x onerror=1>","visual":"html","html":"<script>1</script>"},"items":[],"style":{"palette":"light","align":"left","padding":0,"titleSize":1,"hideOn":[]}}`
	r, _ = ParseSection([]byte(hero), DocOptions{PathPrefix: "/section"})
	if r.Section == nil || r.Section.Content["title"] != "<b>x</b>" || r.Section.Content["html"] != "" {
		t.Fatalf("hero section: %+v", r.Section)
	}
}
