package builder

// Golden tests against output captured from WordPress + upstream 4.3.5 (testdata/golden, made by
// testdata/tools/fixture.sh): each fixture has <name>.data.json (_elementor_data),
// <name>.settings.json (page settings), <name>.id, <name>.css (post-<id>.css) and <name>.html (the
// document markup).
//
// CSS must match byte for byte. HTML is compared after whitespace normalisation (PHP templates
// emit tabs/newlines that carry no meaning) and after dropping attributes added by the lab's theme
// (Twenty Twenty-One adds an inline style to every attachment <img>).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

type fixture struct {
	name     string
	doc      *Document
	wantCSS  string
	wantHTML string
}

func loadFixtures(t *testing.T) []fixture {
	t.Helper()
	files, err := filepath.Glob("testdata/golden/*.data.json")
	if err != nil {
		t.Fatal(err)
	}
	var out []fixture
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".data.json")
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		els, err := ParseElements(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		idRaw, _ := os.ReadFile("testdata/golden/" + name + ".id")
		id, _ := strconv.ParseInt(strings.TrimSpace(string(idRaw)), 10, 64)
		settings := map[string]any{}
		if s, err := os.ReadFile("testdata/golden/" + name + ".settings.json"); err == nil && len(strings.TrimSpace(string(s))) > 0 {
			var v any
			if err := json.Unmarshal(s, &v); err == nil {
				if m, ok := v.(map[string]any); ok {
					settings = m
				}
			}
		}
		settings["__fixture_name"] = name // Golementor Pro fixtures: the page is the current post
		css, _ := os.ReadFile("testdata/golden/" + name + ".css")
		html, _ := os.ReadFile("testdata/golden/" + name + ".html")
		doc := &Document{ID: id, Type: "wp-page", Elements: els, Settings: settings}
		if strings.HasPrefix(name, "pro-") { // captured with upstream Pro active
			doc.PostType = "page"
		}
		out = append(out, fixture{name: name, doc: doc, wantCSS: string(css), wantHTML: string(html)})
	}
	return out
}

// knownDiff returns the documented reason when testdata/golden/<name>.knowndiff lists kind
// ("css: reason" / "html: reason" lines); such fixtures are reported instead of failing.
func knownDiff(name, kind string) string {
	b, err := os.ReadFile("testdata/golden/" + name + ".knowndiff")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if k, reason, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == kind {
			return strings.TrimSpace(reason)
		}
	}
	return ""
}

func testKit(t *testing.T) *Document {
	t.Helper()
	raw, err := os.ReadFile("testdata/golden/kit4.settings.json")
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return &Document{ID: 4, Type: "kit", Settings: s}
}

func testAttachments(t *testing.T) map[int64]*Attachment {
	t.Helper()
	raw, err := os.ReadFile("testdata/golden/attachments.json")
	if err != nil {
		return nil
	}
	var list []*Attachment
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	m := map[int64]*Attachment{}
	for _, a := range list {
		m[a.ID] = a
	}
	return m
}

func testRC(t *testing.T) *RenderContext {
	atts := testAttachments(t)
	rc := &RenderContext{
		KitID:      4,
		Kit:        testKit(t),
		HomeURL:    "http://localhost:8095",
		UploadsURL: "http://localhost:8095/wp-content/uploads",
		Attachment: func(id int64) *Attachment { return atts[id] },
		MediaURL: func(id int64, size string) string {
			if a := atts[id]; a != nil {
				u, _, _, _ := a.imageSrc(size)
				return u
			}
			return ""
		},
		PostTitle: func(id int64) string {
			if a := atts[id]; a != nil {
				return a.Title
			}
			return ""
		},
		Shortcode: func(s string) string { return s },
	}
	if proTestRC != nil { // Golementor Pro fixtures need the site data Pro widgets read
		proTestRC(rc)
	}
	return rc
}

// proTestRC lets the Golementor Pro tests add the site data Pro widgets read (golden_pro_test.go).
var proTestRC func(rc *RenderContext)

// splitRules splits a stylesheet into top-level rule sets (media blocks are split per inner rule
// and prefixed with the query) for match-rate reporting.
func splitRules(css string) []string {
	var out []string
	depth := 0
	start := 0
	media := ""
	for i := 0; i < len(css); i++ {
		switch css[i] {
		case '{':
			if depth == 0 && strings.HasPrefix(css[start:i], "@media") {
				media = css[start:i]
				start = i + 1
			}
			depth++
		case '}':
			depth--
			if media != "" && depth == 0 {
				media = ""
				start = i + 1
				continue
			}
			if (media == "" && depth == 0) || (media != "" && depth == 1) {
				out = append(out, media+css[start:i+1])
				start = i + 1
			}
		}
	}
	return out
}

var (
	wsRe         = regexp.MustCompile(`\s+`)
	tagGapRe     = regexp.MustCompile(`>\s+<`)
	themeImgAttr = regexp.MustCompile(` style="width:100%;height:[0-9.]+%;max-width:[0-9]+px;?"`)
)

func normalizeHTML(s string) string {
	s = themeImgAttr.ReplaceAllString(s, "")
	s = wsRe.ReplaceAllString(s, " ")
	s = tagGapRe.ReplaceAllString(s, "><")
	s = strings.ReplaceAll(s, "> ", ">")
	s = strings.ReplaceAll(s, " <", "<")
	return strings.TrimSpace(s)
}

func firstDiff(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	from := i - 120
	if from < 0 {
		from = 0
	}
	end := func(s string) string {
		e := i + 160
		if e > len(s) {
			e = len(s)
		}
		return s[from:e]
	}
	return "\n  got:  …" + end(a) + "\n  want: …" + end(b)
}

func init() {
	WPWidgetRenderer = func(idBase string, instance map[string]any, args map[string]string) string {
		b, err := os.ReadFile("testdata/golden/page5.wp-widget-" + idBase + ".html")
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func TestGoldenCSS(t *testing.T) {
	rc := testRC(t)
	total, matched := 0, 0
	for _, f := range loadFixtures(t) {
		if f.wantCSS == "" {
			continue
		}
		if proFixtureCurrent != nil {
			proFixtureCurrent(f.doc)
		}
		res, err := Render(f.doc, rc)
		if err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		want := splitRules(f.wantCSS)
		got := map[string]bool{}
		for _, r := range splitRules(res.CSS) {
			got[r] = true
		}
		m := 0
		for _, r := range want {
			if got[r] {
				m++
			}
		}
		total += len(want)
		matched += m
		if res.CSS != f.wantCSS {
			if reason := knownDiff(f.name, "css"); reason != "" {
				t.Logf("%s: known CSS difference (%s)", f.name, reason)
				continue
			}
			t.Errorf("%s: CSS differs (%d/%d rule sets match)%s", f.name, m, len(want), firstDiff(res.CSS, f.wantCSS))
		}
	}
	t.Logf("CSS rule sets matched: %d/%d", matched, total)
}

func TestGoldenHTML(t *testing.T) {
	rc := testRC(t)
	for _, f := range loadFixtures(t) {
		if f.wantHTML == "" {
			continue
		}
		if proFixtureCurrent != nil {
			proFixtureCurrent(f.doc)
		}
		res, err := Render(f.doc, rc)
		if err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		got, want := normalizeHTML(res.HTML), normalizeHTML(f.wantHTML)
		if got != want {
			if reason := knownDiff(f.name, "html"); reason != "" {
				t.Logf("%s: known HTML difference (%s)", f.name, reason)
				continue
			}
			t.Errorf("%s: HTML differs%s", f.name, firstDiff(got, want))
		}
	}
}

func TestKitCSS(t *testing.T) {
	want, err := os.ReadFile("testdata/golden/kit4.css")
	if err != nil {
		t.Fatal(err)
	}
	kit := testKit(t)
	got := KitCSS(kit, &RenderContext{KitID: 4})
	if got != string(want) {
		t.Errorf("kit CSS differs%s", firstDiff(got, string(want)))
	}
}

// TestKitThemeCSS renders an inactive kit with theme-style settings (lab kit 121); globals resolve
// against the active kit (4) like in upstream.
func TestKitThemeCSS(t *testing.T) {
	want, err := os.ReadFile("testdata/golden/kit-theme.css")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/golden/kit-theme.settings.json")
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	got := KitCSS(&Document{ID: 121, Type: "kit", Settings: s}, &RenderContext{KitID: 4, Kit: testKit(t)})
	if got != string(want) {
		w, g := splitRules(string(want)), map[string]bool{}
		for _, r := range splitRules(got) {
			g[r] = true
		}
		m := 0
		for _, r := range w {
			if g[r] {
				m++
			}
		}
		t.Errorf("kit-theme CSS differs (%d/%d rule sets match)%s", m, len(w), firstDiff(got, string(want)))
	}
}

// TestGoldenAssets compares Result.Styles/Scripts with _elementor_page_assets where the lab kept
// it (testdata/golden/<name>.assets.json; the meta is dropped whenever upstream clears its cache).
func TestGoldenAssets(t *testing.T) {
	rc := testRC(t)
	for _, f := range loadFixtures(t) {
		raw, err := os.ReadFile("testdata/golden/" + f.name + ".assets.json")
		if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var want struct {
			Styles  []string `json:"styles"`
			Scripts []string `json:"scripts"`
		}
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		if proFixtureCurrent != nil {
			proFixtureCurrent(f.doc)
		}
		res, err := Render(f.doc, rc)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(res.Styles, ",") != strings.Join(want.Styles, ",") || strings.Join(res.Scripts, ",") != strings.Join(want.Scripts, ",") {
			t.Errorf("%s: assets\n  got:  %v %v\n  want: %v %v", f.name, res.Styles, res.Scripts, want.Styles, want.Scripts)
		}
	}
}
