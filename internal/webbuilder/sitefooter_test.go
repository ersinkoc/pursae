package webbuilder

import (
	"strings"
	"testing"
)

// salientPage is a page in the shape of a WordPress Salient (WPBakery) site saved as static files
// (Decoland): no <main> and no <footer> element; body > [script, nav.skip, div.ocm > div.inner >
// [#header-space, #header-outer > header#top, #ajax-content-wrap > [.container-wrap (the page),
// .global-footer, #footer-outer, #slide-out]]], then the page's scripts (with its post id and
// nonces), a drawer and a last style and script. The header's and footer's WPBakery rows carry a
// random id per request; the head has the page's JSON-LD and a stylesheet with a cache-buster
// that changed between saves.
func salientPage(cur, title, ver, nonce, seed string) string {
	up := ""
	if cur != "index.html" {
		up = "../"
	}
	item := func(href, label string) string {
		cls := "menu-item"
		aria := ""
		if href == cur {
			cls += " current-menu-item"
			aria = ` aria-current="page"`
		}
		return `<li class="` + cls + `"><a href="` + up + href + `"` + aria + `>` + label + `</a></li>`
	}
	nav := item("index.html", "Home") + item("blog/index.html", "Blog") + item("hakkimizda/index.html", "About")
	return `<!doctype html><html lang="tr"><head><title>` + title + `</title>
<script type="application/ld+json" class="rank-math-schema">{"@type":"WebPage","name":"` + title + `"}</script>
<link rel="stylesheet" id="main-css" href="` + up + `wp-content/themes/salient/style.css?ver=18.3.0" media="all">
<link rel="stylesheet" id="dyn-css" href="` + up + `wp-content/uploads/salient/menu-dynamic.css?ver=` + ver + `" media="all">
</head><body class="page">
<script>document.documentElement.classList.remove("no-js");</script>
<nav class="skip-wrap"><a class="skip" href="#ajax-content-wrap">Skip</a></nav>
<div class="ocm-effect-wrap"><div class="ocm-effect-wrap-inner">
<div id="header-space"></div>
<div id="header-outer" data-transparency-option="">
<div id="fws_` + seed + `a1b2c3" class="wpb_row"><p>Open today</p></div>
<header id="top"><a id="logo" class="logo" href="` + up + `index.html"><img src="` + up + `wp-content/uploads/logo.png" alt="Logo"></a>
<nav><ul class="sf-menu">` + nav + `</ul></nav></header>
</div>
<div id="ajax-content-wrap">
<div class="container-wrap"><div class="row"><h1>` + title + `</h1><p>The ` + title + ` page.</p></div></div>
<div class="nectar-global-section footer"><div id="fws_` + seed + `d4e5f6" class="wpb_row"><a class="nectar-button" href="` + up + `hakkimizda/index.html">Visit us</a></div></div>
<div id="footer-outer"><p>© 2026 Decoland</p><ul>` + nav + `</ul></div>
<div id="slide-out-widget-area"><ul>` + nav + `</ul></div>
</div>
</div></div>
<script>var nectarLove = {"postID":"` + seed + `","loveNonce":"` + nonce + `"};</script>
<script src="` + up + `wp-content/themes/salient/js/init.js?ver=18.3.0"></script>
<button id="dc-trigger" type="button">Book</button>
<div id="dc-overlay"></div>
<aside id="dc-drawer"><form><input type="hidden" name="nonce" value="` + nonce + `"><button type="submit" id="fl-btn-f1790001892650">Send</button></form></aside>
<style>.dc{color:red}</style>
<script>document.body.classList.add("ready");</script>
</body></html>`
}

func salientSite() []SitePage {
	return []SitePage{
		{Path: "index.html", HTML: salientPage("index.html", "Home", "19958", "cf8e961f00", "100")},
		{Path: "blog/index.html", HTML: salientPage("blog/index.html", "Blog", "19958", "8c9e7343e9", "201")},
		{Path: "hakkimizda/index.html", HTML: salientPage("hakkimizda/index.html", "About", "19515", "d889cf1afd", "302")},
		{Path: "yazi/index.html", HTML: salientPage("yazi/index.html", "Post", "19515", "7cffb383e8", "403")},
	}
}

func TestConvertSiteSalientShell(t *testing.T) {
	files := map[string]bool{"wp-content/themes/salient/style.css": true, "wp-content/uploads/salient/menu-dynamic.css": true,
		"wp-content/uploads/logo.png": true, "wp-content/themes/salient/js/init.js": true}
	urls := map[string]string{"index.html": "/", "blog/index.html": "/blog/", "hakkimizda/index.html": "/hakkimizda/", "yazi/index.html": "/yazi/"}
	res, err := ConvertSite(salientSite(), SiteImportOptions{KitURL: "/k/", KitFiles: files, URLs: urls})
	if err != nil {
		t.Fatal(err)
	}
	// One header (the current menu item is an active-link rule; the random row id is volatile),
	// one footer found without a <footer> element.
	if len(res.Headers) != 1 || res.Headers[0].Rule == nil || len(res.Footers) != 1 || len(res.Headers[0].Pages) != 4 || len(res.Footers[0].Pages) != 4 {
		t.Fatalf("headers %d footers %d", len(res.Headers), len(res.Footers))
	}
	shell := `<div class="ocm-effect-wrap">` + "\n" + `<div class="ocm-effect-wrap-inner">`
	ajax := shell + "\n" + `<div id="ajax-content-wrap">`
	blog := res.Pages[1].Code
	if blog.Shell != shell || blog.Header == nil || blog.Header.Wrap != shell || !strings.HasPrefix(blog.Header.HTML, `<div id="header-space"`) ||
		!strings.Contains(blog.Header.HTML, `<header id="top"`) {
		t.Fatalf("header: shell %q %+v", blog.Shell, blog.Header)
	}
	if len(blog.HeaderBefore) != 1 || !strings.Contains(blog.HeaderBefore[0].HTML, "skip-wrap") {
		t.Errorf("before the shell: %+v", blog.HeaderBefore)
	}
	if blog.Footer == nil || blog.Footer.Wrap != ajax || !strings.HasPrefix(blog.Footer.HTML, `<div class="nectar-global-section footer"`) ||
		!strings.Contains(blog.Footer.HTML, `id="footer-outer"`) || !strings.Contains(blog.Footer.HTML, `id="slide-out-widget-area"`) {
		t.Fatalf("footer: %+v", blog.Footer)
	}
	var after []string
	for _, p := range blog.FooterAfter {
		after = append(after, p.HTML)
		if p.Wrap != "" || strings.Contains(p.HTML, "<script") {
			t.Errorf("footer after: %+v", p)
		}
	}
	if j := strings.Join(after, "\n"); !strings.Contains(j, `id="dc-trigger"`) || !strings.Contains(j, `id="dc-drawer"`) {
		t.Errorf("drawer: %v", after)
	}
	// The page: its rows in the content's wrapper, its scripts after the footer (tail).
	if len(blog.Sections) < 2 || blog.Sections[0].Wrap != ajax || !strings.Contains(blog.Sections[0].HTML, "<h1") {
		t.Fatalf("sections: %+v", blog.Sections)
	}
	var tail []string
	for _, s := range blog.Sections[1:] {
		if !s.Tail || s.Wrap != "" {
			t.Errorf("not a tail section: %+v", s)
		}
		tail = append(tail, s.HTML)
	}
	if j := strings.Join(tail, "\n"); !strings.Contains(j, `"postID":"201"`) || !strings.Contains(j, "init.js") || !strings.Contains(j, `.dc{color:red}`) {
		t.Errorf("tail: %s", j)
	}
	// Heads: the template's has what all pages have; each page keeps its JSON-LD and its own
	// cache-busted stylesheet.
	th := res.Headers[0].Head
	if strings.Contains(th, "ld+json") || strings.Contains(th, "menu-dynamic") || !strings.Contains(th, "style.css") {
		t.Errorf("template head: %s", th)
	}
	if !strings.Contains(blog.Head, `"name":"Blog"`) || !strings.Contains(blog.Head, "menu-dynamic.css?ver=19958") {
		t.Errorf("page head: %s", blog.Head)
	}
	// The footer template, printed after the page in the shared wrappers, gives the design back.
	ft := res.Footers[0]
	if len(ft.Sections) < 2 || ft.Sections[0].Wrap != ajax {
		t.Fatalf("footer template: %+v", ft.Sections)
	}
	// The pages one template draws keep their scripts after the footer (a tail section).
	b := BindSite(res, SiteBindOptions{Groups: map[string]bool{"*": true}, PostSets: map[string]bool{"*": true}})
	if len(b.Groups) != 1 {
		t.Fatalf("groups: %+v %v", b.Groups, b.Notes)
	}
	secs := b.Groups[0].Sections
	if last := secs[len(secs)-1]; !last.Tail || last.Wrap != "" || !strings.Contains(last.HTML, "init.js") || secs[0].Wrap != ajax {
		t.Errorf("group sections: %+v", secs)
	}
}

// The wrappers the page shares with the footer (deeper than with the header) are printed once.
func TestShareShellDeep(t *testing.T) {
	o, i, a := `<div class="o">`, `<div class="i">`, `<div id="a">`
	h := o + i + `<div id="h">H</div></div></div>` + "\n<nav>skip</nav>"
	h = "<nav>skip</nav>\n" + o + i + `<div id="h">H</div></div></div>`
	m := o + i + a + `<div class="row">P</div></div></div></div>`
	f := o + i + a + `<div id="f">F</div></div></div></div>` + "\n<aside>D</aside>"
	main := &CodeDocInfo{First: []string{o, i, a}, Last: []string{o, i, a}, Common: []string{o, i, a}}
	k, withH, withF := ShellPlan([]string{o, i}, []string{o, i, a}, main)
	kh, kf := ShellDepths([]string{o, i}, []string{o, i, a}, main)
	if k != 2 || !withH || !withF || kh != 2 || kf != 3 {
		t.Fatalf("plan: %d %v %v %d %d", k, withH, withF, kh, kf)
	}
	hh, mm, ff, ok := ShareShellDeep(h, m, f, []string{o, i, a}, k, kh, kf)
	want := "<nav>skip</nav>\n" + o + i + `<div id="h">H</div>` + a + `<div class="row">P</div>` + `<div id="f">F</div></div></div></div>` + "\n<aside>D</aside>"
	if !ok || hh+mm+ff != want {
		t.Fatalf("shared: %v\n%s\nwant\n%s", ok, hh+mm+ff, want)
	}
	// A page in other wrappers is left alone.
	if _, _, _, ok := ShareShellDeep(h, `<div class="x">P</div>`, f, []string{o, i, a}, 2, 2, 3); ok {
		t.Error("shared with a page in other wrappers")
	}
}

// FillCode marks a page's tail (the design's scripts after its shell) so the front end prints it
// after the footer; CodeInfo leaves tail sections out of the page's wrappers.
func TestFillCodeTail(t *testing.T) {
	list := []CodeContent{{ID: "a", HTML: `<p>A</p>`, Wrap: `<div class="w">`}, {ID: "t", HTML: `<script>x()</script>`, Tail: true}}
	snap := `<div class="wb-page" data-wb-root=""><div data-wb-code="a"></div><div data-wb-code="t"></div></div>`
	_, out := CodeHead(UnwrapCodePage(FillCode(snap, list, true)))
	page, tail := SplitCodeTail(out)
	if page != `<div class="w"><p>A</p></div>` || tail != `<script>x()</script>` {
		t.Fatalf("page %q tail %q", page, tail)
	}
	if StripCodeTail(out) != `<div class="w"><p>A</p></div><script>x()</script>` {
		t.Fatalf("stripped: %q", StripCodeTail(out))
	}
	// A tail section that is not at the end is printed in place.
	list2 := []CodeContent{list[1], list[0]}
	snap2 := `<div class="wb-page" data-wb-root=""><div data-wb-code="t"></div><div data-wb-code="a"></div></div>`
	if out := FillCode(snap2, list2, true); strings.Contains(out, CodeTailMark) {
		t.Fatalf("a tail before other sections: %s", out)
	}
}
