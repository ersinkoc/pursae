package webbuilder

import (
	"encoding/json"
	"strings"
	"testing"
)

// The enerjiosgb shape: body > div.shell > [skip link, header, main > sections, footer, to-top],
// the page script after the shell, a sprite and a modal outside it.
const shellSrc = `<!doctype html><html lang="tr" class="no-js" data-theme="light"><head><title>T</title>
<link rel="stylesheet" href="css/style.css"></head>
<body class="home" data-page="1">
<svg width="0" height="0"><symbol id="i"><path d="M0 0"/></symbol></svg>
<div class="flex min-h-screen flex-col">
<a href="#icerik" class="sr-only">Skip</a>
<header id="site-header"><div><nav><a href="index.html">Home</a><a href="hakkimizda.html?tab=2#x">About</a></nav></div></header>
<main id="icerik" class="flex-1">
<section id="a"><h1>One</h1></section>
<section id="b"><h2>Two</h2></section>
</main>
<footer class="foot"><p>© 2026</p></footer>
<button type="button" id="to-top">Up</button>
</div>
<div id="lightbox" hidden></div>
<script src="js/main.js" defer></script>
</body></html>`

func TestConvertCodeShell(t *testing.T) {
	files := map[string]bool{"css/style.css": true, "js/main.js": true}
	res, err := ConvertCode(shellSrc, CodeImportOptions{KitURL: "/k/", KitFiles: files, Split: true,
		LinkMap: map[string]string{"index.html": "/", "hakkimizda.html": "/hakkimizda/"}})
	if err != nil {
		t.Fatal(err)
	}
	shell := `<div class="flex min-h-screen flex-col">`
	if res.Shell != shell || res.HTMLAttrs != `class="no-js" data-theme="light"` || res.BodyAttrs != `class="home" data-page="1"` || res.Lang != "tr" {
		t.Fatalf("shell %q html %q body %q", res.Shell, res.HTMLAttrs, res.BodyAttrs)
	}
	if res.Header == nil || res.Header.Wrap != shell || !strings.Contains(res.Header.HTML, `class="sr-only"`) || !strings.Contains(res.Header.HTML, `<header id="site-header"`) {
		t.Fatalf("header: %+v", res.Header)
	}
	if !strings.Contains(res.Header.HTML, `href="/hakkimizda/?tab=2#x"`) {
		t.Errorf("query string lost: %s", res.Header.HTML)
	}
	if len(res.HeaderBefore) != 1 || res.HeaderBefore[0].Wrap != "" || !strings.Contains(res.HeaderBefore[0].HTML, "<symbol") {
		t.Errorf("sprite before the shell: %+v", res.HeaderBefore)
	}
	if res.Footer == nil || res.Footer.Wrap != shell || !strings.Contains(res.Footer.HTML, `id="to-top"`) || strings.Contains(res.Footer.HTML, "main.js") {
		t.Fatalf("footer: %+v", res.Footer)
	}
	if len(res.FooterAfter) != 1 || !strings.Contains(res.FooterAfter[0].HTML, `id="lightbox"`) || res.FooterAfter[0].Wrap != "" {
		t.Errorf("modal after the shell: %+v", res.FooterAfter)
	}
	// The script after the shell comes after the footer (and the lightbox), as in the design: a
	// tail section, printed after the footer template.
	if len(res.Sections) != 3 || res.Sections[0].Wrap != shell+"\n"+`<main id="icerik" class="flex-1">` || strings.Contains(res.Sections[1].HTML, `main.js`) ||
		!res.Sections[2].Tail || res.Sections[2].Wrap != "" || !strings.Contains(res.Sections[2].HTML, `src="/k/js/main.js"`) {
		t.Fatalf("sections: %+v", res.Sections)
	}

	// Without Split every part keeps the shell, so the page alone (canvas layout) is the design.
	all, err := ConvertCode(shellSrc, CodeImportOptions{KitURL: "/k/", KitFiles: files})
	if err != nil {
		t.Fatal(err)
	}
	var list []CodeContent
	var snap strings.Builder
	snap.WriteString(`<div class="wb-page" data-wb-root="">`)
	for i, p := range all.Sections {
		id := "s" + string(rune('a'+i))
		list = append(list, CodeContent{ID: id, HTML: p.HTML, Wrap: p.Wrap})
		snap.WriteString(`<div data-wb-code="` + id + `"></div>`)
	}
	snap.WriteString(`</div>`)
	_, out := CodeHead(UnwrapCodePage(FillCode(snap.String(), list, true)))
	if strings.Count(out, shell) != 1 || strings.Count(out, "<main") != 1 {
		t.Fatalf("canvas page: %s", out)
	}
	for _, order := range [][2]string{{"<symbol", shell}, {shell, "sr-only"}, {"sr-only", "<main"}, {"</main>", "<footer"}, {"to-top", "lightbox"}} {
		if i, j := strings.Index(out, order[0]), strings.Index(out, order[1]); i < 0 || j < 0 || i > j {
			t.Errorf("%s should come before %s: %s", order[0], order[1], out)
		}
	}
	if i, j := strings.Index(out, `id="to-top"`), strings.Index(out, `</div><div id="lightbox"`); i < 0 || j < i {
		t.Errorf("to-top button not inside the shell: %s", out)
	}
}

// Merge-like designs (header, main at body level, footer inside main) keep today's parts.
func TestConvertCodeBodyLevelUnchanged(t *testing.T) {
	res, err := ConvertCode(codeImportSrc, CodeImportOptions{Split: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Shell != "" || len(res.HeaderBefore) != 0 || len(res.FooterAfter) != 0 || res.Header.Wrap != "" {
		t.Fatalf("body-level design got a shell: %q %+v", res.Shell, res.HeaderBefore)
	}
}

func activeHeader(cur string) string {
	cls := func(href string) string {
		if href == cur {
			return `class="nav text-brand-600" aria-current="page"`
		}
		return `class="nav text-ink-700 hover:bg-ink-100"`
	}
	btn := "nav text-ink-700 hover:bg-ink-100"
	if strings.HasPrefix(cur, "/kadro/") || cur == "/hakkimizda/" {
		btn = "nav text-brand-600"
	}
	news := cls("/haberler/")
	if strings.HasPrefix(cur, "/haber/") {
		news = `class="nav text-brand-600"`
	}
	return `<header><ul><li><a href="/" ` + cls("/") + `>Home</a></li>` +
		`<li><button type="button" class="` + btn + `" aria-controls="menu-a">Corporate</button></li>` +
		`<li><a href="/haberler/" ` + news + `>News</a></li></ul>` +
		`<div id="menu-a"><a href="/hakkimizda/">About</a><a href="/kadro/x/">X</a></div></header>`
}

func TestClusterPartsActive(t *testing.T) {
	paths := []string{"/", "/hakkimizda/", "/haberler/", "/haber/1/", "/haber/2/", "/kadro/x/", "/kadro/y/", "/iletisim/", ""}
	var samples []ActiveSample
	for _, p := range paths {
		samples = append(samples, ActiveSample{Path: p, Parts: []string{activeHeader(p)}})
	}
	cls := ClusterParts(samples)
	if len(cls) != 1 || len(cls[0].Members) != len(paths) || cls[0].Rule == nil {
		t.Fatalf("clusters: %d, members %v", len(cls), cls[0].Members)
	}
	base := cls[0].Parts[0]
	if strings.Contains(base, "text-brand-600") || strings.Contains(base, "aria-current") {
		t.Fatalf("shared markup is not the plain state: %s", base)
	}
	rule := cls[0].Rule
	var news, btn *ActiveItem
	for i := range rule.Items {
		switch rule.Items[i].Key {
		case "href:/haberler/":
			news = &rule.Items[i]
		case "controls:menu-a":
			btn = &rule.Items[i]
		}
	}
	if news == nil || !news.Exact || len(news.Prefixes) != 1 || news.Prefixes[0] != "/haber/" {
		t.Errorf("news item: %+v", news)
	}
	if btn == nil || len(btn.Prefixes) != 1 || btn.Prefixes[0] != "/kadro/" || len(btn.Paths) != 1 || btn.Paths[0] != "/hakkimizda/" {
		t.Errorf("menu button item: %+v", btn)
	}
	// A page added later under /haber/ marks the news link too; an unknown page marks nothing.
	if got := ApplyActive(base, rule, "/haber/3/"); !strings.Contains(got, `<a class="nav text-brand-600" href="/haberler/">`) && !strings.Contains(got, `href="/haberler/" class="nav text-brand-600"`) {
		t.Errorf("section prefix: %s", got)
	}
	if got := ApplyActive(base, rule, "/elsewhere/"); got != base {
		t.Errorf("unknown page changed the header")
	}
	// The rule survives storage.
	if r := ParseActiveRule(rule.String()); r == nil || len(r.Items) != len(rule.Items) {
		t.Errorf("round trip: %v", r)
	}

	// A copy that differs elsewhere is a cluster of its own.
	samples = append(samples, ActiveSample{Path: "/other/", Parts: []string{strings.Replace(activeHeader("/other/"), "News", "Blog", 1)}})
	if cls := ClusterParts(samples); len(cls) != 2 || len(cls[1].Members) != 1 {
		t.Errorf("a different header joined the cluster: %d", len(cls))
	}
}

func TestShareShell(t *testing.T) {
	shell := `<div class="shell">`
	h := "<svg></svg>\n" + shell + `<a href="#c">Skip</a><header>H</header></div>`
	m := shell + `<main id="c"><section>S</section></main></div>`
	f := shell + `<footer>F</footer><button id="to-top"></button></div><div id="modal"></div>`
	hh, mm, ff, ok := ShareShell(h, m, f, []string{shell}, true)
	if !ok {
		t.Fatal("not shared")
	}
	if got := hh + mm + ff; got != "<svg></svg>\n"+shell+`<a href="#c">Skip</a><header>H</header><main id="c"><section>S</section></main><footer>F</footer><button id="to-top"></button></div><div id="modal"></div>` {
		t.Fatalf("shared: %s", got)
	}
	// A theme main (not a code part) is printed inside the shell.
	if hh, mm, ff, ok := ShareShell(h, "<main>theme</main>", f, []string{shell}, false); !ok || !strings.HasSuffix(hh, "</header>") || mm != "<main>theme</main>" || !strings.HasPrefix(ff, "<footer>") {
		t.Errorf("theme main: %v %q %q", ok, hh, ff)
	}
	// Markup that does not end or start with the shell is left alone.
	if _, _, _, ok := ShareShell(`<header>H</header>`, m, f, []string{shell}, true); ok {
		t.Error("shared a shell the header does not have")
	}
	if _, _, _, ok := ShareShell(h, `<p>notice</p>`+m, f, []string{shell}, true); ok {
		t.Error("shared a shell the page does not start with")
	}
	if k := ShellLen([]string{shell}, []string{shell, "<main>"}, []string{shell}, true); k != 1 {
		t.Errorf("ShellLen = %d", k)
	}
	if k := ShellLen([]string{shell}, []string{`<div class="other">`}, []string{shell}, true); k != 0 {
		t.Errorf("ShellLen with another design's page = %d", k)
	}
}

func TestPlanSite(t *testing.T) {
	plan := PlanSite([]string{"index.html", "404.html", "hizmet/3.html", "kadro.html", "kadro/a.html", "docs/index.html", "docs/x/y.html", "css/a.css"})
	got := map[string]SitePlanPage{}
	for _, p := range plan.Pages {
		got[p.Path] = p
	}
	if !got["index.html"].Front || !got["404.html"].NotFound || got["hizmet/3.html"].Slug != "3" || got["hizmet/3.html"].Parent != "dir:hizmet" {
		t.Fatalf("pages: %+v", plan.Pages)
	}
	if _, ok := got["css/a.css"]; ok {
		t.Error("a stylesheet is a page")
	}
	if got["docs/index.html"].Slug != "docs" || got["docs/index.html"].Parent != "" {
		t.Errorf("folder page: %+v", got["docs/index.html"])
	}
	folders := map[string]SitePlanFolder{}
	for _, f := range plan.Folders {
		folders[f.Key] = f
	}
	if folders["dir:hizmet"].Page != "" || folders["dir:kadro"].Page != "kadro.html" || folders["dir:docs"].Page != "docs/index.html" || folders["dir:docs/x"].Parent != "dir:docs" {
		t.Errorf("folders: %+v", plan.Folders)
	}
}

func TestConvertSite(t *testing.T) {
	page := func(cur, rel string) string {
		return strings.Replace(shellSrc, `<a href="index.html">Home</a>`, func() string {
			if cur == "index.html" {
				return `<a href="` + rel + `index.html" class="on" aria-current="page">Home</a>`
			}
			return `<a href="` + rel + `index.html" class="off">Home</a>`
		}(), 1)
	}
	res, err := ConvertSite([]SitePage{{Path: "index.html", HTML: page("index.html", "")}, {Path: "a/b.html", HTML: page("", "../")}, {Path: "hakkimizda.html", HTML: page("", "")}},
		SiteImportOptions{KitURL: "/k/", KitFiles: map[string]bool{"css/style.css": true, "js/main.js": true},
			URLs: map[string]string{"index.html": "/", "a/b.html": "/a/b/", "hakkimizda.html": "/hakkimizda/"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Headers) != 1 || len(res.Headers[0].Pages) != 3 || res.Headers[0].Rule == nil || len(res.Footers) != 1 {
		t.Fatalf("templates: %d headers %+v, %d footers", len(res.Headers), res.Headers, len(res.Footers))
	}
	h := res.Headers[0]
	if len(h.Sections) != 2 || h.Sections[0].Name == "" || h.Sections[1].Wrap != `<div class="flex min-h-screen flex-col">` {
		t.Errorf("header sections: %+v", h.Sections)
	}
	if strings.Contains(h.Sections[1].HTML, "aria-current") {
		t.Errorf("header template keeps a marked link")
	}
	if !strings.Contains(res.Pages[1].Code.Header.HTML, `href="/"`) {
		t.Errorf("links of a page in a folder not mapped")
	}
	if len(res.Unmapped) != 0 {
		t.Errorf("unmapped: %v", res.Unmapped)
	}
}

func TestMergeDocAttrsAndCodeInfo(t *testing.T) {
	a, cls := MergeDocAttrs([]string{"lang"}, `class="a b" data-x="1" onload="evil()" lang="en"`, `class="b c" data-x="2" data-y="&quot;q"`)
	if cls != "a b c" || a != `data-x="1" data-y="&#34;q"` {
		t.Errorf("merge: %q %q", a, cls)
	}
	doc, _ := json.Marshal(map[string]any{"sections": []any{
		map[string]any{"id": "h", "kind": "code", "hidden": true, "content": map[string]any{"html": "x", "wrap": "<div class=\"z\">"}},
		map[string]any{"id": "a", "kind": "code", "content": map[string]any{"html": "<p>a</p>", "wrap": "<div class=\"s\">\n<main>", "lang": "tr", "bodyAttrs": `class="home"`}},
		map[string]any{"id": "b", "kind": "code", "content": map[string]any{"html": "<p>b</p>", "wrap": "<div class=\"s\">", "active": `{"roles":[{"tag":"a","base":{"class":"x"}}],"items":[{"role":0,"key":"href:/","exact":true}]}`}},
	}})
	info := CodeInfo(doc)
	if info == nil || info.Lang != "tr" || info.BodyAttrs != `class="home"` || info.Active == nil || len(info.Common) != 1 || len(info.First) != 2 || len(info.Last) != 1 {
		t.Fatalf("info: %+v", info)
	}
}

// The merge shape: a bare 404 page (no header, no footer) and a home page whose footer sits inside
// main. Neither may be served by a footer template (the home page keeps its own), and the bare 404
// by no header template either; Uncovered names them so the whole-site templates exclude them.
func TestConvertSiteUncovered(t *testing.T) {
	plain := `<!doctype html><html><body><header class="h"><a href="index.html">Home</a></header><main><section><h1>P</h1></section></main><footer class="f">F</footer></body></html>`
	home := `<!doctype html><html><body><header class="h"><a href="index.html">Home</a></header><main><section><h1>H</h1></section><footer class="f">F</footer></main></body></html>`
	bare := `<!doctype html><html><body><main><h1>Not here</h1><a href="index.html">Home</a></main></body></html>`
	res, err := ConvertSite([]SitePage{{Path: "index.html", HTML: home}, {Path: "a.html", HTML: plain}, {Path: "b.html", HTML: plain}, {Path: "404.html", HTML: bare}},
		SiteImportOptions{KitURL: "/k/", URLs: map[string]string{"index.html": "/", "a.html": "/a/", "b.html": "/b/"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pages[0].Code.FooterInPage || !strings.Contains(codeHTML(res.Pages[0].Code), `class="f"`) {
		t.Fatalf("home keeps its footer in the page")
	}
	if len(res.Footers) != 1 || len(res.Footers[0].Pages) != 2 {
		t.Fatalf("footers: %+v", res.Footers)
	}
	if got := res.Uncovered(false); len(got) != 2 || got[0] != 0 || got[1] != 3 {
		t.Errorf("footer uncovered: %v, want [0 3]", got)
	}
	if got := res.Uncovered(true); len(got) != 1 || got[0] != 3 {
		t.Errorf("header uncovered: %v, want [3]", got)
	}
}

func codeHTML(r *CodeImport) string {
	var b strings.Builder
	for _, s := range r.Sections {
		b.WriteString(s.HTML)
	}
	return b.String()
}

// The umbral shape: a page in a folder whose scripts read file paths from data attributes and load
// modules through an import map with root-relative URLs.
func TestConvertCodeDataAttrsAndImportMap(t *testing.T) {
	src := `<!doctype html><html><head>
<script type="importmap">{"imports": {"three": "/assets/vendor/three.module.js", "three/addons/": "/assets/vendor/addons/", "other/": "/elsewhere/", "x": "../assets/vendor/x.js?v=2", "cdn": "https://cdn.example/a.js"}}</script>
</head><body><main><section class="fable" data-wide="../assets/film.mp4" data-wide-poster="../assets/film.jpg#t" data-label="Fable" data-at="1.5" data-missing="../assets/none.mp4" data-src="../assets/film.jpg">
<video poster="../assets/film.jpg"></video></section></main></body></html>`
	res, err := ConvertCode(src, CodeImportOptions{KitURL: "/k/", Base: "identity",
		KitFiles: map[string]bool{"assets/film.mp4": true, "assets/film.jpg": true, "assets/vendor/three.module.js": true, "assets/vendor/x.js": true, "assets/vendor/addons/a.js": true}})
	if err != nil {
		t.Fatal(err)
	}
	body := codeHTML(res)
	for _, want := range []string{`data-wide="/k/assets/film.mp4"`, `data-wide-poster="/k/assets/film.jpg#t"`, `data-label="Fable"`, `data-at="1.5"`,
		`data-missing="../assets/none.mp4"`, `data-src="/k/assets/film.jpg"`, `poster="/k/assets/film.jpg"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in %s", want, body)
		}
	}
	for _, want := range []string{`"three": "/k/assets/vendor/three.module.js"`, `"three/addons/": "/k/assets/vendor/addons/"`, `"other/": "/elsewhere/"`, `"x": "/k/assets/vendor/x.js?v=2"`, `"cdn": "https://cdn.example/a.js"`} {
		if !strings.Contains(res.Head, want) {
			t.Errorf("import map: missing %s in %s", want, res.Head)
		}
	}
	// A data attribute is reported only when it reads as the path of a design file (an asset
	// extension); other values need not be URLs.
	if strings.Join(res.Missing, ",") != "assets/none.mp4" {
		t.Errorf("missing: %v", res.Missing)
	}
}

// The Salient (WordPress) shape: a lazy loader keeps the image's real src and srcset in data-*
// attributes (a srcset-like list), a video's source in data-nectar-video-src; a slider reads a
// JSON list. Every candidate naming a kit file is mapped; a file put elsewhere (Remap: the Media
// Library) gets its URL; candidates and files that are nowhere are reported.
func TestConvertCodeDataLists(t *testing.T) {
	src := `<!doctype html><html><head></head><body><main><section>
<img src="data:image/svg+xml;charset=utf-8,%3Csvg%3E" data-nectar-img-src="../wp-content/uploads/a.png" data-nectar-img-srcset="../wp-content/uploads/a.png 1489w, ../wp-content/uploads/a-300x169.png 300w, ../wp-content/uploads/a-gone.png 768w" alt="">
<video><source data-nectar-video-src="../wp-content/uploads/film.mp4" type="video/mp4"></video>
<video><source data-nectar-video-src="../wp-content/uploads/lost.webm"></video>
<div data-slides='["../wp-content/uploads/a.png","label"]' data-json="{&#34;src&#34;:&#34;..\/wp-content\/uploads\/a.png&#34;}" data-cols="1, 2" data-title="Hi, there" data-x="a.png 1x"></div>
</section></main></body></html>`
	res, err := ConvertCode(src, CodeImportOptions{KitURL: "/k/", Base: "blog",
		KitFiles: map[string]bool{"wp-content/uploads/a.png": true, "wp-content/uploads/a-300x169.png": true},
		Remap:    map[string]string{"wp-content/uploads/film.mp4": "/wp-content/uploads/2026/11/film.mp4"}})
	if err != nil {
		t.Fatal(err)
	}
	body := codeHTML(res)
	for _, want := range []string{
		`data-nectar-img-src="/k/wp-content/uploads/a.png"`,
		`data-nectar-img-srcset="/k/wp-content/uploads/a.png 1489w, /k/wp-content/uploads/a-300x169.png 300w, ../wp-content/uploads/a-gone.png 768w"`,
		`data-nectar-video-src="/wp-content/uploads/2026/11/film.mp4"`,
		`data-nectar-video-src="../wp-content/uploads/lost.webm"`,
		`data-slides="[&#34;/k/wp-content/uploads/a.png&#34;,&#34;label&#34;]"`,
		`data-json="{&#34;src&#34;:&#34;\/k\/wp-content\/uploads\/a.png&#34;}"`,
		`data-cols="1, 2"`, `data-title="Hi, there"`, `data-x="a.png 1x"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in %s", want, body)
		}
	}
	if got := strings.Join(res.Missing, ","); got != "a.png,wp-content/uploads/a-gone.png,wp-content/uploads/lost.webm" && got != "blog/a.png,wp-content/uploads/a-gone.png,wp-content/uploads/lost.webm" {
		t.Errorf("missing: %v", res.Missing)
	}
}

// The umbral Motif shape: an app screen whose header bar, asides and main sit in one grid wrapper and
// that has no footer; and a page without a header whose footer shares the wrapper. The shell is
// printed once, by the header and the page (or the page and the footer).
func TestShareShellTwoParts(t *testing.T) {
	app := `<div class="app" id="app">`
	h := app + `<header class="bar">B</header></div>`
	m := app + `<aside>L</aside><main>S</main><aside>R</aside></div>` + "\n" + `<dialog id="d"></dialog>`
	// The page's sections: the grid's and, after the shell, a dialog in no wrapper.
	k, withH, withF := ShellPlan([]string{app}, nil, &CodeDocInfo{First: []string{app}, Last: nil, Common: nil})
	if k != 1 || !withH || withF {
		t.Fatalf("plan without footer: %d %v %v", k, withH, withF)
	}
	hh, mm, ff, ok := ShareShell(h, m, "", []string{app}, true)
	if !ok || hh+mm+ff != app+`<header class="bar">B</header><aside>L</aside><main>S</main><aside>R</aside></div>`+"\n"+`<dialog id="d"></dialog>` {
		t.Fatalf("header + page: %v %s", ok, hh+mm+ff)
	}
	f := app + `<footer>F</footer></div>`
	m2 := app + `<main>S</main></div>`
	if k, withH, withF := ShellPlan(nil, []string{app}, &CodeDocInfo{First: []string{app, "<main>"}, Last: []string{app, "<main>"}, Common: []string{app, "<main>"}}); k != 1 || withH || !withF {
		t.Fatalf("plan without header: %d %v %v", k, withH, withF)
	}
	hh, mm, ff, ok = ShareShell("", m2, f, []string{app}, true)
	if !ok || hh+mm+ff != app+`<main>S</main><footer>F</footer></div>` {
		t.Fatalf("page + footer: %v %s", ok, hh+mm+ff)
	}
	// Not without a code page, and not with nothing to share it with.
	if _, _, _, ok := ShareShell(h, "<main>theme</main>", "", []string{app}, false); ok {
		t.Error("shared a header's shell with a theme main alone")
	}
	if k, _, _ := ShellPlan(nil, nil, &CodeDocInfo{First: []string{app}, Common: []string{app}}); k != 0 {
		t.Errorf("a page alone has no shared shell: %d", k)
	}
}
