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
// (and FuzzSanitizeCSS, FuzzParseDoc, FuzzSanitizeRich). Plain go test runs the seeds.

func FuzzSanitizeSnapshot(f *testing.F) {
	for _, s := range xssCorpus {
		f.Add(s, false)
		f.Add(s, true)
	}
	f.Add(goodSnapshot, false)
	f.Fuzz(func(t *testing.T, inner string, embed bool) {
		opts := SnapshotOptions{FormActionPrefix: testFormPrefix, EmbedHTML: embed}
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
