package webbuilder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// The renderStatic fixtures in testdata/render are exported by `make builder` (builder/scripts/live.mjs,
// upstream src/builder/live/fixtures.tsx): <name>.doc.json is the DocState, <name>.render.json its
// RenderResult. They cover the default sections, every wired design, layouts and every runtime behaviour.

type renderFixture struct {
	name string
	rr   RenderResult
	doc  []byte
}

func loadRenderFixtures(t *testing.T) []renderFixture {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join("testdata", "render", "*.render.json"))
	if len(files) == 0 {
		t.Fatal("no renderStatic fixtures in testdata/render (run make builder)")
	}
	sort.Strings(files)
	var out []renderFixture
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var rr RenderResult
		if err := json.Unmarshal(b, &rr); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		doc, err := os.ReadFile(strings.TrimSuffix(f, ".render.json") + ".doc.json")
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out = append(out, renderFixture{name: strings.TrimSuffix(filepath.Base(f), ".render.json"), rr: rr, doc: doc})
	}
	return out
}

// domTokens is domString split per node, for a readable first difference.
func domTokens(t testing.TB, s string) []string {
	t.Helper()
	nodes, err := parseFragment(s)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			out = append(out, "#"+n.Data)
		case html.ElementNode:
			attrs := make([]string, 0, len(n.Attr))
			for _, a := range n.Attr {
				attrs = append(attrs, a.Namespace+":"+a.Key+"="+a.Val)
			}
			sort.Strings(attrs)
			out = append(out, "<"+n.Namespace+":"+n.Data+" "+strings.Join(attrs, " ")+">")
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			out = append(out, "</"+n.Data+">")
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return out
}

func firstDiff(a, b []string) string {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return "in:  " + trunc(a[i], 400) + "\nout: " + trunc(b[i], 400)
		}
	}
	return "lengths differ"
}

// TestRenderFixturesSanitizeEquivalent: every fixture snapshot, as an unfiltered_html author publishes it,
// passes the sanitizer with an equivalent DOM (nothing the renderer emits is outside the allowlist), and the
// page CSS passes the CSS sanitizer unchanged.
func TestRenderFixturesSanitizeEquivalent(t *testing.T) {
	opts := SnapshotOptions{FormActionPrefix: testFormPrefix, EmbedHTML: true, SearchAction: testHome}
	for _, f := range loadRenderFixtures(t) {
		t.Run(f.name, func(t *testing.T) {
			out, changed, err := SanitizeSnapshot(f.rr.HTML, opts)
			if err != nil {
				t.Fatal(err)
			}
			if a, b := domTokens(t, f.rr.HTML), domTokens(t, out); strings.Join(a, "\n") != strings.Join(b, "\n") {
				t.Fatalf("DOM differs after sanitizing (changed=%v)\n%s", changed, firstDiff(a, b))
			}
			assertSafe(t, out, opts)
			if f.rr.CSS != "" {
				if css, changed, err := SanitizeCSS(f.rr.CSS, CSSOptions{}); err != nil || changed {
					t.Errorf("css changed=%v err=%v\n%s", changed, err, css)
				}
			}
		})
	}
}

// Without unfiltered_html the html sections' srcdoc iframes go and nothing else changes.
func TestRenderFixturesWithoutEmbed(t *testing.T) {
	opts := SnapshotOptions{FormActionPrefix: testFormPrefix, SearchAction: testHome}
	srcdoc := regexp.MustCompile(`(?i) srcdoc="[^"]*"`)
	for _, f := range loadRenderFixtures(t) {
		out, _, err := SanitizeSnapshot(f.rr.HTML, opts)
		if err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		if strings.Contains(strings.ToLower(out), "srcdoc") {
			t.Errorf("%s: srcdoc iframe kept without EmbedHTML", f.name)
		}
		if !srcdoc.MatchString(f.rr.HTML) {
			if a, b := domTokens(t, f.rr.HTML), domTokens(t, out); strings.Join(a, "\n") != strings.Join(b, "\n") {
				t.Errorf("%s: DOM differs without embeds\n%s", f.name, firstDiff(a, b))
			}
		}
	}
}

var featureAttr = map[Feature]*regexp.Regexp{
	FeatureMenu:    regexp.MustCompile(` data-wb-menu=`),
	FeatureAcc:     regexp.MustCompile(` data-wb-acc=`),
	FeatureTabs:    regexp.MustCompile(` data-wb-tabs=`),
	FeatureDismiss: regexp.MustCompile(` data-wb-dismiss=`),
	FeatureForm:    regexp.MustCompile(` data-wb-form=`),
	FeatureMotion:  regexp.MustCompile(` data-wb-(motion|parallax|scroll-fade|scroll-scale)=`),
	FeatureEmbed:   regexp.MustCompile(` data-wb-embed=`),
}

// The RenderResult metadata matches its markup and the contract, and the doc validates.
func TestRenderFixturesContract(t *testing.T) {
	editorAttr := regexp.MustCompile(` data-(?:label|el|section|selected|preview|mode|grid|mbg)=`)
	opacity0 := regexp.MustCompile(`opacity:\s*0(?:[;"]|$)`)
	classAttr := regexp.MustCompile(` class="([^"]*)"`)
	seen := map[Feature]bool{}
	for _, f := range loadRenderFixtures(t) {
		rr := f.rr
		if rr.Renderer != RendererVersion {
			t.Errorf("%s: renderer %d, want %d (re-run make builder)", f.name, rr.Renderer, RendererVersion)
		}
		page := regexp.MustCompile(`(?i) srcdoc="[^"]*"`).ReplaceAllString(rr.HTML, ` srcdoc=""`)
		if m := editorAttr.FindString(page); m != "" {
			t.Errorf("%s: editor attribute %q in the snapshot", f.name, m)
		}
		if opacity0.MatchString(page) {
			t.Errorf("%s: inline opacity:0 (content must be visible without JavaScript)", f.name)
		}
		if !sort.SliceIsSorted(rr.Features, func(i, j int) bool { return rr.Features[i] < rr.Features[j] }) {
			t.Errorf("%s: features not sorted: %v", f.name, rr.Features)
		}
		has := map[Feature]bool{}
		for _, ft := range rr.Features {
			has[ft] = true
			seen[ft] = true
		}
		for ft, re := range featureAttr {
			if re.MatchString(page) != has[ft] {
				t.Errorf("%s: feature %s listed=%v, markup=%v", f.name, ft, has[ft], re.MatchString(page))
			}
		}
		classes := map[string]bool{}
		for _, m := range classAttr.FindAllStringSubmatch(page, -1) {
			for _, c := range strings.Fields(html.UnescapeString(m[1])) {
				classes[c] = true
			}
		}
		for _, c := range rr.Classes {
			if !classes[c] {
				t.Errorf("%s: class %q listed but not in the markup", f.name, c)
			}
			delete(classes, c)
		}
		for c := range classes {
			t.Errorf("%s: class %q in the markup but not listed", f.name, c)
		}
		for _, fn := range rr.Fonts {
			if fn.Family == "" || len(fn.Weights) == 0 {
				t.Errorf("%s: bad font need %+v", f.name, fn)
			}
		}
		r, err := ParseDoc(f.doc, DocOptions{UnfilteredHTML: true})
		if err != nil || !r.Valid() {
			t.Errorf("%s.doc.json does not validate: %v %+v", f.name, err, r)
		}
	}
	// Tabs have no wired design upstream yet (faq/tabs); every other behaviour is covered.
	for _, ft := range []Feature{FeatureMenu, FeatureAcc, FeatureDismiss, FeatureForm, FeatureMotion, FeatureEmbed} {
		if !seen[ft] {
			t.Errorf("no fixture exercises feature %s", ft)
		}
	}
}
