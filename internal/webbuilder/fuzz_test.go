package webbuilder

import (
	"encoding/json"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// Fuzz targets. The S2 gate runs each for 60 s:
//
//	go test ./internal/webbuilder -run '^$' -fuzz FuzzSanitizeSnapshot -fuzztime 60s
//
// (and FuzzSanitizeCSS, FuzzParseDoc, FuzzSanitizeRich, FuzzCompile, FuzzConditions). Plain go
// test runs the seeds.

func FuzzSanitizeSnapshot(f *testing.F) {
	for _, s := range xssCorpus {
		f.Add(s, false)
		f.Add(s, true)
	}
	f.Add(goodSnapshot, false)
	f.Add(`<form data-wb-search data-wb-form method=post action="javascript:x"><input name=s type=search><input type=hidden name=name><select name=choice></select><button>Go</button></form>`, false)
	f.Fuzz(func(t *testing.T, inner string, embed bool) {
		opts := SnapshotOptions{FormActionPrefix: testFormPrefix, EmbedHTML: embed, SearchAction: "https://example.com/"}
		out, _, err := SanitizeSnapshot(wrapRoot(inner), opts)
		if err != nil {
			if err == ErrSnapshotRoot || err == ErrSnapshotTooLarge {
				return
			}
			t.Fatalf("unexpected error %v", err)
		}
		assertSafe(t, out, opts)
		// Idempotent: sanitized output is stable under a second pass.
		again, _, err := SanitizeSnapshot(out, opts)
		if err != nil {
			t.Fatalf("second pass: %v on %q", err, out)
		}
		if domString(t, again) != domString(t, out) {
			t.Fatalf("not idempotent:\n1: %q\n2: %q", out, again)
		}
	})
}

func FuzzSanitizeCSS(f *testing.F) {
	for _, s := range []string{
		`a{color:red}`, `@media (x){a{b:c}}`, `a{background:url(javascript:x)}`, `@import "x";`,
		`a{width:\65 xpression(1)}`, `}}{{;;`, `a{b:"unterminated`, `/* x`, `@font-face{src:url(/x)}`,
		`.x{&:hover{@media (hover:hover){color:red}}}`, `a{b:url("x\"y")}`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out, _, err := SanitizeCSS(in, CSSOptions{})
		if err != nil {
			return
		}
		if hasStyleClose(out) {
			t.Fatalf("</style in output %q", out)
		}
		n := cssNormalize(out)
		for _, bad := range []string{"expression(", "-moz-binding", "javascript:", "vbscript:", "@import", "@charset"} {
			if strings.Contains(n, bad) {
				t.Fatalf("%q survived in %q (from %q)", bad, out, in)
			}
		}
		// Output is a fixed point.
		again, changed, err := SanitizeCSS(out, CSSOptions{})
		if err != nil || changed || again != out {
			t.Fatalf("not idempotent: %q → %q (changed=%v err=%v)", out, again, changed, err)
		}
		if st, _ := SanitizeStyleAttr(in); strings.Contains(cssNormalize(st), "javascript:") {
			t.Fatalf("style attr kept javascript: %q", st)
		}
	})
}

func FuzzParseDoc(f *testing.F) {
	f.Add(string(readSample(f)), false)
	f.Add(`{"sections":[{"id":"a","kind":"html","content":{"html":"<script>"},"items":[{"id":"b","sub":{"id":"c","kind":"html"}}]}],"site":{}}`, false)
	f.Add(`{"sections":null,"site":null}`, true)
	f.Fuzz(func(t *testing.T, raw string, priv bool) {
		r, err := ParseDoc([]byte(raw), DocOptions{UnfilteredHTML: priv})
		if err != nil {
			return
		}
		if !priv {
			var m map[string]any
			if err := json.Unmarshal(r.JSON, &m); err != nil {
				t.Fatal(err)
			}
			if list, ok := m["sections"].([]any); ok {
				for _, sec := range list {
					assertUnprivileged(t, sec, r.JSON)
				}
			}
			for _, s := range r.Doc.Sections {
				if s.Kind == KindHTML || s.Content["html"] != "" || s.Style.CSS != "" {
					t.Fatalf("privileged field survived: %s", r.JSON)
				}
			}
		}
		_ = FallbackHTML(&r.Doc)
		_ = FormSchemas(&r.Doc)
		_ = DocFonts(&r.Doc)
	})
}

func FuzzSanitizeRich(f *testing.F) {
	for _, s := range []string{`<b>x</b>`, `<a href="javascript:x">y</a>`, `<ul><li>a<li>b</ul>`, `<p>a<br><br><br>b</p>`, `<i><img src=x onerror=1></i>`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := SanitizeRich(in)
		nodes, err := parseFragment(out)
		if err != nil {
			t.Fatal(err)
		}
		allowed := toSet(RichTextElements)
		for _, n := range nodes {
			assertRich(t, n, allowed, out)
		}
	})
}

// assertUnprivileged walks a section the way the editor reads it.
func assertUnprivileged(t *testing.T, sec any, js []byte) {
	sm, ok := sec.(map[string]any)
	if !ok {
		return
	}
	if sm["kind"] == KindHTML {
		t.Fatalf("html section survived: %s", js)
	}
	if c, ok := sm["content"].(map[string]any); ok && c["html"] != nil {
		t.Fatalf("content.html survived: %s", js)
	}
	if st, ok := sm["style"].(map[string]any); ok && st["css"] != nil {
		t.Fatalf("style.css survived: %s", js)
	}
	if items, ok := sm["items"].([]any); ok {
		for _, it := range items {
			if im, ok := it.(map[string]any); ok {
				assertUnprivileged(t, im["sub"], js)
			}
		}
	}
}

// assertRich: only RichTextElements, a[href|target|rel] with a safe href, no other attributes.
func assertRich(t *testing.T, n *html.Node, allowed map[string]bool, out string) {
	if n.Type == html.ElementNode {
		if n.Namespace != "" || !allowed[n.Data] {
			t.Fatalf("element %s in rich output %q", n.Data, out)
		}
		for _, a := range n.Attr {
			switch {
			case n.Data == "a" && a.Key == "href":
				if _, ok := SafeHref(a.Val); !ok {
					t.Fatalf("unsafe href %q in %q", a.Val, out)
				}
			case n.Data == "a" && (a.Key == "target" || a.Key == "rel"):
			default:
				t.Fatalf("attribute %s in rich output %q", a.Key, out)
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		assertRich(t, c, allowed, out)
	}
}

// hostileResolver answers every slot with attacker-chosen text and URLs; HTML slots return a
// fixed trusted fragment (they only ever carry core generator output).
type hostileResolver struct {
	val   string
	depth int
}

func (h *hostileResolver) Text(SlotName, SlotArgs) (string, bool) { return h.val, h.val != "" }
func (h *hostileResolver) HTML(SlotName, SlotArgs) (string, bool) { return "<em>ok</em>", true }
func (h *hostileResolver) Attrs(SlotName, SlotArgs) (map[string]string, bool) {
	return map[string]string{"href": h.val, "src": h.val, "srcset": h.val, "sizes": h.val, "width": h.val, "alt": h.val,
		"datetime": h.val, "aria-current": h.val, "class": h.val, "onclick": h.val, "style": h.val}, true
}
func (h *hostileResolver) items() LoopIter {
	var rs []Resolver
	for i := 0; i < 3; i++ {
		rs = append(rs, &hostileResolver{val: h.val, depth: h.depth + 1})
	}
	return &SliceIter{Items: rs}
}
func (h *hostileResolver) Loop(string, SlotArgs) (LoopIter, bool) { return h.items(), true }
func (h *hostileResolver) Menu(string, SlotArgs) (LoopIter, bool) { return h.items(), true }

func FuzzCompile(f *testing.F) {
	for _, s := range []string{
		`<h1 data-wb-slot="post.title">x</h1>`,
		`<a data-wb-slot="post.url" href="#"><img data-wb-slot="post.image"></a>`,
		`<ul data-wb-loop="s1"><li data-wb-loop-item><span data-wb-slot="post.title"></span><ul data-wb-loop="s2"><li data-wb-loop-item><time data-wb-slot="post.date" data-wb-format="F j, Y"></time></li></ul></li><li data-wb-loop-empty>none</li></ul>`,
		`<ul data-wb-menu-loc="primary" data-wb-loop-part="2/2"><li data-wb-loop-item><a data-wb-slot="menu.url"><span data-wb-slot="menu.label"></span></a></li></ul>`,
		`<p data-wb-if="site.tagline" data-wb-slot="site.tagline"></p><p data-wb-if-not="search.query">none</p>`,
		`<pre data-wb-if="x">` + "\n\nx</pre><textarea data-wb-slot=\"site.title\"></textarea>",
		`<svg><path data-wb-slot="post.title"></path></svg><p title="data-wb-slot">x</p>`,
	} {
		f.Add(s, `"><script>alert(1)</script>`)
		f.Add(s, "javascript:alert(1)")
		f.Add(s, "")
	}
	f.Fuzz(func(t *testing.T, inner, val string) {
		opts := SnapshotOptions{FormActionPrefix: testFormPrefix}
		snap, _, err := SanitizeSnapshot(wrapRoot(inner), opts)
		if err != nil {
			return
		}
		p, err := Compile(snap)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		if !hasPlaceholders(snap) {
			if s, ok := p.Static(); !ok || s != snap {
				t.Fatalf("static path changed the snapshot")
			}
		}
		r := &hostileResolver{val: val}
		out, err := p.ExecString(r)
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		again, _ := p.ExecString(r)
		if again != out {
			t.Fatal("exec not deterministic")
		}
		assertSafe(t, out, opts)
		if strings.Contains(strings.ToLower(out), "<script") || strings.Contains(out, "onclick") {
			t.Fatalf("injection survived: %q", out)
		}
		// Raw compile of unsanitized input must not panic either.
		if q, err := Compile(inner); err == nil {
			_, _ = q.ExecString(r)
		}
	})
}

func FuzzConditions(f *testing.F) {
	for _, s := range []string{
		"include/general", "exclude/404", "include/singular/post/in/category/3", "include/singular/page/child_of/7",
		"include/archive/author/5", "include/archive/type/book", "include/archive/category/12", "include/singular/in",
		"include/archive/home/1", "include/singular/post/9999999999999999999", "include/singular/front_page",
	} {
		f.Add(s)
	}
	facts := []QueryFacts{
		{View: ViewSingular, PostType: "post", PostID: 3, ParentID: 7, AuthorID: 5, Terms: map[string][]int64{"category": {3, 12}}},
		{View: ViewArchive, Archive: ArchiveTerm, Taxonomy: "category", TermID: 12},
		{View: ViewArchive, Archive: ArchiveHome, FrontPage: true},
		{View: ViewArchive, Archive: ArchiveAuthor, AuthorID: 5},
		{View: View404},
	}
	f.Fuzz(func(t *testing.T, rule string) {
		checkConditionAgainstPattern(t, rule)
		c, err := ParseCondition(rule)
		for _, q := range facts {
			if err == nil {
				_ = c.Match(q)
			}
		}
		rules := strings.Split(rule, ",")
		norm, errs := NormalizeConditions(rules)
		for _, r := range norm {
			if _, err := ParseCondition(r); err != nil {
				t.Fatalf("normalized rule %q invalid", r)
			}
		}
		if len(errs) == 0 {
			// Normalizing is idempotent.
			again, errs2 := NormalizeConditions(norm)
			if len(errs2) != 0 || strings.Join(again, ",") != strings.Join(norm, ",") {
				t.Fatalf("not idempotent: %v → %v", norm, again)
			}
		}
		cands := []TemplateCandidate{{ID: 1, Type: TemplateHeader, Conditions: ParseConditions(rules)}, {ID: 2, Type: TemplateArchive, Conditions: ParseConditions(rules)}}
		for _, q := range facts {
			_ = PickTemplate(LocationMain, cands, q)
		}
		_ = TemplatesConflict(&cands[0], &cands[1])
	})
}
