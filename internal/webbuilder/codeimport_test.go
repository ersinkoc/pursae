package webbuilder

import (
	"strings"
	"testing"
)

const codeImportSrc = `<!doctype html>
<html lang="tr"><head><meta charset="utf-8"><title>Pursae · Home</title>
<meta name="description" content="Go WordPress.">
<meta property="og:image" content="assets/og.png">
<link rel="icon" href="data:image/svg+xml,x">
<link rel="stylesheet" href="assets/style.css?v=2">
<style>
.hero { background: url("assets/bg.webp") }
</style>
</head><body>
<!-- notes -->
<svg width="0" height="0" style="position:absolute"><symbol id="mark"><path d="M0 0"/></symbol></svg>
<a class="skip" href="#main">Skip</a>
<header class="ln"><a class="brand" href="#"><svg><use href="#mark"/></svg><span>Pursae</span></a><nav><a href="forum/index.html">Forum</a><a href="#faq">FAQ</a></nav></header>
<main id="main">
<section class="stage" id="stage"><h1>Edit <em>this</em> page</h1><img src="assets/hero.webp" srcset="assets/hero.webp 1x, assets/hero@2x.webp 2x" alt=""></section>
<script>window.x=1</script>
<section id="mimari"><h2>Architecture</h2><p>One binary.</p></section>
<div class="aft">
  <section class="sec" id="faq"><h2>FAQ</h2><div class="q">Is it free?</div></section>
  <section class="sec" id="cta"><h2>Try it</h2><a class="btn" href="en/index.html#kur">Beta</a></section>
  <footer class="pgf"><p>© 2026</p><a href="#">Privacy</a></footer>
</div>
</main>
<script src="assets/js/app.js?v=1"></script>
</body></html>`

func TestConvertCodeSplit(t *testing.T) {
	files := map[string]bool{"assets/style.css": true, "assets/bg.webp": true, "assets/hero.webp": true, "assets/hero@2x.webp": true, "assets/js/app.js": true, "assets/og.png": true}
	res, err := ConvertCode(codeImportSrc, CodeImportOptions{KitURL: "/k/merge", KitFiles: files, Split: true,
		LinkMap: map[string]string{"forum/index.html": "/forum/"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.SEO.Title != "Pursae · Home" || res.SEO.Description != "Go WordPress." || res.SEO.OGImage != "/k/merge/assets/og.png" || res.Lang != "tr" {
		t.Fatalf("seo: %+v", res.SEO)
	}
	if !strings.Contains(res.Head, `href="/k/merge/assets/style.css"`) || !strings.Contains(res.Head, `url(&#34;/k/merge/assets/bg.webp&#34;)`) && !strings.Contains(res.Head, `url("/k/merge/assets/bg.webp")`) || strings.Contains(res.Head, "icon") {
		t.Fatalf("head: %s", res.Head)
	}
	if res.Header == nil || !strings.Contains(res.Header.HTML, `<symbol id="mark">`) || !strings.Contains(res.Header.HTML, `class="skip"`) ||
		!strings.Contains(res.Header.HTML, `href="/forum/"`) || res.Header.Wrap != "" {
		t.Fatalf("header: %+v", res.Header)
	}
	if res.Footer == nil || !strings.Contains(res.Footer.HTML, `class="pgf"`) || res.Footer.Wrap != `<div class="aft">` {
		t.Fatalf("footer: %+v", res.Footer)
	}
	names := []string{}
	for _, p := range res.Sections {
		names = append(names, p.Name)
	}
	if len(res.Sections) != 5 || names[4] != "Footer" || !res.FooterInPage {
		t.Fatalf("sections %v (an in-main footer stays on the page too)", names)
	}
	if res.Sections[0].Wrap != `<main id="main">` || res.Sections[2].Wrap != "<main id=\"main\">\n<div class=\"aft\">" {
		t.Fatalf("wraps: %q / %q", res.Sections[0].Wrap, res.Sections[2].Wrap)
	}
	if !strings.Contains(res.Sections[0].HTML, "window.x=1") || !strings.Contains(res.Sections[4].HTML, `src="/k/merge/assets/js/app.js"`) || strings.Contains(res.Footer.HTML, "app.js") {
		t.Fatalf("scripts not kept with their parts: %q", res.Sections[4].HTML)
	}
	if !strings.Contains(res.Sections[0].HTML, `srcset="/k/merge/assets/hero.webp 1x, /k/merge/assets/hero@2x.webp 2x"`) {
		t.Fatalf("srcset: %s", res.Sections[0].HTML)
	}
	if !strings.Contains(res.Sections[0].HTML, `<h1 data-wbc=`) || !strings.Contains(res.Sections[2].HTML, `<div class="q" data-wbc=`) || strings.Contains(res.Header.HTML, `<use data-wbc`) {
		t.Fatalf("editable ids: %s", res.Sections[0].HTML)
	}
	if len(res.Unmapped) != 1 || res.Unmapped[0] != "en/index.html#kur" {
		t.Fatalf("unmapped: %v", res.Unmapped)
	}

	// Published: consecutive parts share one main and one div.aft, and the page keeps them in order.
	var list []CodeContent
	var snap strings.Builder
	snap.WriteString(`<div class="wb-page" data-wb-root="" data-wb-post="3">`)
	for i, p := range res.Sections {
		id := "s" + string(rune('a'+i))
		list = append(list, CodeContent{ID: id, HTML: p.HTML, Head: res.Head, Wrap: p.Wrap})
		snap.WriteString(`<div data-wb-code="` + id + `"></div>`)
	}
	snap.WriteString(`</div>`)
	out := UnwrapCodePage(FillCode(snap.String(), list, true))
	_, out = CodeHead(out)
	if strings.Count(out, "<main") != 1 || strings.Count(out, `<div class="aft">`) != 1 || strings.Contains(out, "data-wbc") {
		t.Fatalf("wrappers: %s", out)
	}
	if i, j := strings.Index(out, `</div></main>`), strings.Index(out, "app.js"); i < 0 || j > i {
		t.Fatalf("structure: %s", out)
	}
}

func TestConvertCodeNoSplit(t *testing.T) {
	res, err := ConvertCode(codeImportSrc, CodeImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Header != nil || res.Footer != nil {
		t.Fatal("split without Split")
	}
	if !strings.Contains(res.Sections[0].HTML, "<symbol") || res.Sections[0].Wrap != "" {
		t.Fatalf("first part: %+v", res.Sections[0])
	}
	if len(res.Missing) == 0 {
		t.Fatal("missing files not reported without a kit")
	}
}
