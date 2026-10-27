package cache

import (
	"strings"
	"testing"
)

func opt(doc string, o OptimizeConfig) string { return string(Optimize([]byte(doc), o)) }

func TestLazyImages(t *testing.T) {
	doc := `<body><IMG src="hero.jpg" alt="a > b"><img src="b.jpg" loading="eager"><img src='c.jpg' alt=x/>` +
		`<img src="d.jpg" decoding="sync" /><pre><img src="pre.jpg"></pre><!-- <img src="comment.jpg"> -->` +
		`<script>var s = '<img src="js.jpg">';</script></body>`
	got := opt(doc, OptimizeConfig{LazyImages: true, LazySkipFirst: 1})
	want := `<body><IMG src="hero.jpg" alt="a > b"><img src="b.jpg" loading="eager"><img src='c.jpg' alt=x/ loading="lazy" decoding="async">` +
		`<img src="d.jpg" decoding="sync" loading="lazy" /><pre><img src="pre.jpg"></pre><!-- <img src="comment.jpg"> -->` +
		`<script>var s = '<img src="js.jpg">';</script></body>`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	// LazySkipFirst 0 makes every image lazy.
	if got := opt(`<img src="a">`, OptimizeConfig{LazyImages: true}); got != `<img src="a" loading="lazy" decoding="async">` {
		t.Fatalf("skip 0: %s", got)
	}
}

func TestDeferScripts(t *testing.T) {
	doc := `<html><head><script src="/head.js" id="jquery-core-js"></script></head><body>` +
		`<script src="/wp-includes/js/jquery/jquery.min.js?ver=3" id="jquery-core-js"></script>` +
		`<script src="/a.js" id="slider-js"></script>` +
		`<script src="/b.js" id="other-js"></script>` +
		`<script src="/c.js" id="comment-reply-js" async></script>` +
		`<script type="module" src="/m.js" id="mod-js"></script>` +
		`<script src="/d.js" id="dep-js"></script><script id="dep-js-after">dep()</script>` +
		`<script>var x = "<script src='/x.js' id='slider-js'>";</script>` +
		`</body></html>`
	got := opt(doc, OptimizeConfig{DeferScripts: true, DeferAllowlist: []string{"jquery", "slider", "comment-reply", "mod", "dep"}})
	for _, want := range []string{
		`<head><script src="/head.js" id="jquery-core-js"></script></head>`, // head scripts untouched
		`<script src="/wp-includes/js/jquery/jquery.min.js?ver=3" id="jquery-core-js" defer></script>`,
		`<script src="/a.js" id="slider-js" defer></script>`,
		`<script src="/b.js" id="other-js"></script>`,
		`<script src="/c.js" id="comment-reply-js" async></script>`,
		`<script type="module" src="/m.js" id="mod-js"></script>`,
		`<script src="/d.js" id="dep-js"></script>`,
		`<script>var x = "<script src='/x.js' id='slider-js'>";</script>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s\nin %s", want, got)
		}
	}
}

func TestCollapseWhitespace(t *testing.T) {
	doc := "<html>\n  <head>\n    <style>\n  a  {  }\n</style>\n  </head>\n<body>\n\n  <p>Hello   world</p>  \n" +
		"<pre>\n  keep   this\n</pre>\n  <textarea>  a\n\n b </textarea>\n\n<script>\n  if (a  <  b) {}\n</script>\n\n</body>\n</html>"
	got := opt(doc, OptimizeConfig{CollapseWhitespace: true})
	want := "<html> <head> <style>\n  a  {  }\n</style> </head> <body> <p>Hello   world</p> " +
		"<pre>\n  keep   this\n</pre> <textarea>  a\n\n b </textarea> <script>\n  if (a  <  b) {}\n</script> </body> </html>"
	if got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func TestPreload(t *testing.T) {
	doc := `<!doctype html><html><head data-x="1"><meta charset="utf-8"></head><body><header>x</header></body></html>`
	got := opt(doc, OptimizeConfig{Preload: []PreloadAsset{
		{Href: "/fonts/a.woff2", As: "font", Type: "font/woff2", CrossOrigin: true},
		{Href: `/x".css`, As: "style"},
	}})
	want := `<head data-x="1"><link rel="preload" href="/fonts/a.woff2" as="font" type="font/woff2" crossorigin>` +
		`<link rel="preload" href="/x&#34;.css" as="style"><meta charset="utf-8">`
	if !strings.Contains(got, want) || strings.Count(got, "preload") != 2 {
		t.Fatalf("got %s", got)
	}
	// Already preloaded assets are not duplicated.
	again := opt(got, OptimizeConfig{Preload: []PreloadAsset{{Href: "/fonts/a.woff2", As: "font"}}})
	if strings.Count(again, "/fonts/a.woff2") != 1 {
		t.Fatalf("duplicated: %s", again)
	}
}

func TestOptimizeDisabledAndMalformed(t *testing.T) {
	doc := `<body><img src="a"><img src="b`
	if got := opt(doc, OptimizeConfig{}); got != doc {
		t.Fatal("disabled optimizer changed the document")
	}
	if got := opt(doc, OptimizeConfig{LazyImages: true}); !strings.HasSuffix(got, `<img src="b`) {
		t.Fatalf("unterminated tag modified: %s", got)
	}
	unterminated := `<body><script src="/a.js" id="a-js">never closed <img src="x">`
	if got := opt(unterminated, OptimizeConfig{LazyImages: true}); got != unterminated {
		t.Fatalf("content after unterminated script modified: %s", got)
	}
}
