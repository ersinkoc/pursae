package server

import (
	"context"
	"html"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/cache"
	"wpgo/internal/links"
	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Tests of the Theme Builder front (webbuilder_theme_front.go, webbuilder_slots.go). The unit
// tests run without a database; the integration tests need WPGO_TEST_ADMIN_DB / WPGO_TEST_DB.

// --- unit ------------------------------------------------------------------------------------------

func TestWBThemeFrontAdminBarInsert(t *testing.T) {
	bar := `<ul id='wp-admin-bar-root-default'><li>A</li>` + wbAdminBarSecondary + `><li>B</li></ul>`
	got := wbInsertAdminBarNodes(bar, "<li>N</li>")
	if !strings.Contains(got, "<li>A</li><li>N</li>"+wbAdminBarSecondary) {
		t.Errorf("node not at the end of the primary group: %s", got)
	}
	if wbInsertAdminBarNodes(bar, "") != bar || wbInsertAdminBarNodes("<ul></ul>", "<li>N</li>") != "<ul></ul>" {
		t.Error("no nodes / no secondary group must leave the bar alone")
	}
}

func TestWBThemeFrontCacheTags(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Header(cache.HeaderTags, "widgets,post:3")
	wbAddCacheTags(c, true, true)
	if got := w.Header().Get(cache.HeaderTags); got != "widgets,post:3,wb-theme,wb-loop" {
		t.Errorf("tags %q", got)
	}
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	wbAddCacheTags(c, false, false)
	if got := w.Header().Get(cache.HeaderTags); got != "" {
		t.Errorf("no tags expected, got %q", got)
	}
}

func TestWBThemeFrontExcerpt(t *testing.T) {
	p := &store.Post{Content: "<!-- wp:paragraph --><p>One <b>two</b> three &amp; four five six</p><!-- /wp:paragraph --><!--more-->Hidden rest"}
	if got := wbExcerpt(p, 5); got != "One two three &amp; four&hellip;" {
		t.Errorf("trimmed: %q", got)
	}
	if got := wbExcerpt(&store.Post{Excerpt: `Manual <script>x()</script> "excerpt"`}, 0); got != "Manual &#34;excerpt&#34;" {
		t.Errorf("manual: %q", got)
	}
	if got := wbExcerpt(&store.Post{Password: "x", Content: "secret"}, 0); strings.Contains(got, "secret") {
		t.Errorf("protected: %q", got)
	}
}

func TestWBThemeFrontLoopDefs(t *testing.T) {
	d := &store.WebBuilderDoc{PostID: 990001, Rev: 3, Doc: []byte(`{"sections":[
		{"id":"l1","kind":"loop","content":{"source":"query","perPage":"3","postType":"page"}},
		{"id":"lay","kind":"layout","content":{},"items":[{"id":"c","title":"layout","text":"","sub":{"id":"l2","kind":"loop","content":{"source":"related"}}}]},
		{"id":"n1","kind":"nav","content":{"source":"query"}}]}`)}
	defs := wbLoopDefs(d)
	if len(defs) != 2 {
		t.Fatalf("defs %v", defs)
	}
	if l1 := defs["l1"]; l1.spec.Source != "query" || l1.spec.PerPage != 3 || l1.spec.PostType != "page" || !l1.typeSet {
		t.Errorf("l1 %+v", l1)
	}
	if l2 := defs["l2"]; l2.spec.Source != "related" || l2.typeSet || len(l2.spec.Terms) != 1 || l2.spec.Terms[0].Taxonomy != "category" {
		t.Errorf("nested related loop %+v", l2)
	}
	if _, ok := defs["n1"]; ok {
		t.Error("a nav section is not a loop")
	}
}

// A loop id is resolved once per fill: a second data-wb-loop element of the same id (or a nested
// one, once per outer item) reuses the posts instead of running its query again.
func TestWBThemeFrontLoopMemo(t *testing.T) {
	d := &store.WebBuilderDoc{PostID: 990002, Rev: 1, Doc: []byte(`{"sections":[{"id":"r1","kind":"loop","content":{"source":"related"}}]}`)}
	e := &wbSlotEnv{doc: d} // no server: a second query would panic
	if posts, ok := e.loopPosts("r1"); !ok || posts != nil {
		t.Fatalf("related loop without a singular post: %v %v", posts, ok)
	}
	e.main = &wbSlotPost{p: &store.Post{ID: 1, Type: "post"}}
	for i := 0; i < 3; i++ {
		if posts, ok := e.loopPosts("r1"); !ok || posts != nil {
			t.Fatalf("memoized loop: %v %v", posts, ok)
		}
	}
	if _, ok := e.loopPosts("nope"); ok {
		t.Error("unknown loop id resolved")
	}
}

// post.terms prints only public taxonomies, whatever data-wb-tax the snapshot names.
func TestWBThemeFrontTermsPublicOnly(t *testing.T) {
	lb := &links.Builder{Home: "http://x"}
	e := &wbSlotEnv{lb: lb, publicTax: map[string]bool{"internal_status": false, "genre": true}}
	sp := &wbSlotPost{p: &store.Post{ID: 1}, terms: map[string][]*store.Term{
		"category":        {{ID: 2, Taxonomy: "category", Slug: "news", Name: "News"}},
		"internal_status": {{ID: 3, Taxonomy: "internal_status", Slug: "hold", Name: "Legal hold"}},
	}}
	r := &wbSlotResolver{env: e, post: sp}
	if out, ok := r.HTML(webbuilder.SlotPostTerms, webbuilder.SlotArgs{Tax: "internal_status"}); ok || strings.Contains(out, "Legal hold") {
		t.Errorf("private taxonomy printed: %q", out)
	}
	if out, ok := r.HTML(webbuilder.SlotPostTerms, webbuilder.SlotArgs{Tax: "category"}); !ok || !strings.Contains(out, ">News</a>") {
		t.Errorf("category terms: %q %v", out, ok)
	}
	if e.publicTaxonomy("unknown_tax") {
		t.Error("an unregistered taxonomy counts as public")
	}
}

// --- integration helpers ---------------------------------------------------------------------------

// wbtfTemplate creates a published template whose snapshot root holds body, with doc as the
// stored doc (the loops' queries), css and features.
func wbtfTemplate(t *testing.T, srv *Server, typ webbuilder.TemplateType, title string, conds []string, body, doc, css string, features []string) *store.WBTemplate {
	t.Helper()
	tp := wbtTemplate(t, srv, typ, title, conds, 0, "")
	wbtfPublish(t, srv, tp.ID, body, doc, css, features)
	return tp
}

// wbtfPublish publishes a new snapshot of post id.
func wbtfPublish(t *testing.T, srv *Server, id int64, body, doc, css string, features []string) {
	t.Helper()
	ctx := context.Background()
	wb := srv.store.WebBuilder()
	rev, _ := wb.DocRev(ctx, id)
	if doc == "" {
		doc = `{"sections":[],"site":{}}`
	}
	if features == nil {
		features = []string{}
	}
	d := &store.WebBuilderDoc{PostID: id, Doc: []byte(doc), Page: []byte(`{}`),
		HTML: `<div class="wb-page" data-wb-root data-wb-post="` + itoa64(id) + `">` + body + `</div>`, CSS: css, Fonts: []byte(`[]`),
		Features: features, Renderer: webbuilder.RendererVersion, SnapshotUser: 1, SnapshotUnfiltered: true}
	if p, err := srv.store.GetPost(ctx, id); err == nil {
		d.ContentHash = store.WebBuilderContentHash(p.Content)
	}
	if err := wb.PublishDoc(ctx, d, rev); err != nil {
		t.Fatal(err)
	}
	srv.wbTemplatesChanged(ctx)
}

// wbtfPage creates a published page or post with a builder doc (page settings page).
func wbtfPage(t *testing.T, srv *Server, typ, title, body, doc, page string) *store.Post {
	t.Helper()
	ctx := context.Background()
	p := &store.Post{Type: typ, Title: title, Slug: strings.ToLower(strings.ReplaceAll(title, " ", "-")), Status: "publish", AuthorID: 1, Content: "<p>Fallback of " + title + "</p>"}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	wbtfPublish(t, srv, p.ID, body, doc, "", nil)
	if page != "" {
		if _, err := srv.store.Pool.Exec(ctx, `UPDATE web_builder_docs SET page = $2 WHERE post_id = $1`, p.ID, []byte(page)); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func wbtfPost(t *testing.T, srv *Server, typ, title, content string, age time.Duration, termIDs ...int64) *store.Post {
	t.Helper()
	ctx := context.Background()
	at := time.Now().Add(-age)
	p := &store.Post{Type: typ, Title: title, Slug: strings.ToLower(regexp.MustCompile(`[^A-Za-z0-9]+`).ReplaceAllString(title, "-")),
		Status: "publish", AuthorID: 1, Content: content, PublishedAt: &at, CommentStatus: "open"}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	if len(termIDs) > 0 {
		if err := srv.store.SetPostTerms(ctx, p.ID, "category", termIDs); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// wbtfStart is a site with the plugin on and Hello Pursae active, pretty permalinks, two posts
// per page.
func wbtfStart(t *testing.T) (*Server, string, *http.Client) {
	t.Helper()
	ts, c, srv := newTestServer(t, true)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if !srv.wbOn() {
		t.Fatal("Pursae Web Builder is off")
	}
	if err := srv.switchTheme(ctx, wbHelloSlug); err != nil {
		t.Fatal(err)
	}
	if err := srv.settings.Set(ctx, map[string]any{"permalink_structure": "/%postname%/", "posts_per_page": 2}); err != nil {
		t.Fatal(err)
	}
	return srv, ts.URL, c
}

func wbtfGet(t *testing.T, u string) (int, string) {
	t.Helper()
	r := anonGet(t, u)
	b := bodyOf(r)
	if os.Getenv("WBTF_DUMP") != "" {
		t.Logf("%s:\n%s", u, b)
	}
	return r.StatusCode, b
}

func wbtfWant(t *testing.T, what, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("%s lacks %q", what, w)
		}
	}
}

func wbtfBad(t *testing.T, what, body string, bad ...string) {
	t.Helper()
	for _, b := range bad {
		if strings.Contains(body, b) {
			t.Errorf("%s contains %q", what, b)
		}
	}
}

const (
	wbtfHeader = `<header class="hdr"><a data-wb-slot="site.url"><img data-wb-if="site.logo" data-wb-slot="site.logo" alt=""><span data-wb-if-not="site.logo" data-wb-slot="site.title"></span></a>` +
		`<p class="tag" data-wb-slot="site.tagline"></p>` +
		`<nav><span class="contents" data-wb-menu-loc="primary" data-wb-loop-part="1/2"><a data-wb-loop-item="" data-wb-slot="menu.url" class="ml"><span data-wb-slot="menu.label"></span></a></span>` +
		`<span class="contents" data-wb-menu-loc="primary" data-wb-loop-part="2/2"><a data-wb-loop-item="" data-wb-slot="menu.url" class="ml"><span data-wb-slot="menu.label"></span></a></span></nav>HEADER-MARK</header>`
	wbtfFooter = `<footer class="ftr">&copy; <span data-wb-slot="site.year"></span> <span data-wb-slot="site.title"></span> FOOTER-MARK` +
		`<details data-wb-menu><summary>More</summary><a href="#top">Top</a></details></footer>`
	wbtfSingle = `<article class="single"><h1 data-wb-slot="post.title"></h1><time data-wb-slot="post.date" data-wb-format="Y-m-d"></time>` +
		` by <a data-wb-slot="post.author-url"><span data-wb-slot="post.author"></span></a>` +
		`<p data-wb-if="post.terms">In <span data-wb-slot="post.terms" data-wb-tax="category" data-wb-sep=" | "></span></p>` +
		`<img data-wb-slot="post.image" data-wb-size="large" alt="">` +
		`<div class="content" data-wb-slot="post.content"></div><div class="nav" data-wb-slot="post.nav"></div>` +
		`<a data-wb-slot="post.comments-url"><span data-wb-slot="post.comments-count"></span> comments</a>` +
		`<div data-wb-slot="post.comments"></div>` +
		`<div class="related" data-wb-loop="rel1" data-wb-source="query" data-wb-per-page="24"><article data-wb-loop-item=""><a class="rl" data-wb-slot="post.url"><span data-wb-slot="post.title"></span></a></article><p data-wb-loop-empty="">No related</p></div>` +
		`SINGLE-MARK</article>`
	wbtfSingleDoc = `{"sections":[{"id":"rel1","kind":"loop","content":{"source":"related","perPage":"1"}}],"site":{}}`
	wbtfArchive   = `<section class="arch"><h1 data-wb-slot="archive.title"></h1><div class="desc" data-wb-slot="archive.description"></div>` +
		`<div class="grid" data-wb-loop="ml"><div class="card" data-wb-loop-item=""><h2 data-wb-slot="post.title"></h2><p class="ex" data-wb-slot="post.excerpt" data-wb-words="5"></p></div><p data-wb-loop-empty="">ARCH-EMPTY</p></div>` +
		`<nav class="pg" data-wb-slot="loop.pagination"></nav>` +
		`<div class="forged" data-wb-loop="forged" data-wb-source="query"><i data-wb-loop-item="">FORGED</i></div>` +
		`<ul class="latest" data-wb-loop="q1" data-wb-per-page="24" data-wb-post-type="page"><li data-wb-loop-item=""><span data-wb-slot="post.title"></span></li></ul>ARCHIVE-MARK</section>`
	wbtfArchiveDoc = `{"sections":[{"id":"ml","kind":"loop","content":{"source":"main","pagination":"numbers"}},{"id":"q1","kind":"loop","content":{"source":"query","perPage":"1","postType":"post"}}],"site":{}}`
	wbtfSearch     = `<section><h1>Results for <span data-wb-slot="search.query"></span> (<span data-wb-slot="search.count"></span>)</h1>` +
		`<div data-wb-loop="sm"><p class="hit" data-wb-loop-item=""><a data-wb-slot="post.url"><span data-wb-slot="post.title"></span></a></p><p data-wb-loop-empty="">SEARCH-EMPTY</p></div>SEARCH-MARK</section>`
	wbtfSearchDoc = `{"sections":[{"id":"sm","kind":"loop","content":{"source":"main"}}],"site":{}}`
	wbtf404       = `<section><h1>LOST-MARK</h1><form data-wb-search="" method="get" action="/"><input type="search" name="s"><button type="submit">Go</button></form></section>`
)

// --- integration -----------------------------------------------------------------------------------

func TestWBThemeFrontRender(t *testing.T) {
	srv, base, admin := wbtfStart(t)
	ctx := context.Background()
	if err := srv.settings.Set(ctx, map[string]any{"blogname": `Toggle <script>alert(1)</script> & Co`, "blogdescription": "A <b>tag</b>line"}); err != nil {
		t.Fatal(err)
	}
	news := &store.Term{Taxonomy: "category", Slug: "news", Name: "News & Views", Description: "All the news."}
	if err := srv.store.InsertTerm(ctx, news); err != nil {
		t.Fatal(err)
	}
	p1 := wbtfPost(t, srv, "post", "Alpha First", "<p>Alpha body one two three four five six seven.</p>", 3*time.Hour, news.ID)
	p2 := wbtfPost(t, srv, "post", "Beta Second", "<p>Beta body.</p>", 2*time.Hour, news.ID)
	p3 := wbtfPost(t, srv, "post", `<script>bad()</script> Gamma`, "<p>Gamma body.</p>", time.Hour, news.ID)
	about := wbtfPost(t, srv, "page", "About Us", "<p>About body.</p>", 4*time.Hour)

	menuID, err := srv.store.CreateMenu(ctx, "Main")
	if err != nil {
		t.Fatal(err)
	}
	for i, it := range []*store.EditItem{
		{MenuID: menuID, Label: `Say "Hi" <img src=x onerror=alert(1)>`, Kind: "custom", Object: "custom", URL: "javascript:alert(1)", Order: 1, Status: "publish"},
		{MenuID: menuID, Label: "About", Kind: "post_type", Object: "page", TargetID: about.ID, Order: 2, Status: "publish"},
		{MenuID: menuID, Label: "Docs", Kind: "custom", Object: "custom", URL: "https://example.test/docs", Order: 3, Status: "publish"},
	} {
		if err := srv.store.AddMenuItem(ctx, it); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := srv.store.SetMenuLocations(ctx, map[string]int64{"primary": menuID}); err != nil {
		t.Fatal(err)
	}

	// Without templates Hello Pursae prints its own minimal header.
	_, body := wbtfGet(t, base+"/about-us/")
	wbtfWant(t, "no templates", body, `<header id="site-header" class="site-header">`, "About body.")
	wbtfBad(t, "no templates", body, "wb-part", "wb-site")

	hdr := wbtfTemplate(t, srv, webbuilder.TemplateHeader, "Site header", []string{"include/general"}, wbtfHeader, "", ".hdr{color:red}", nil)
	ftr := wbtfTemplate(t, srv, webbuilder.TemplateFooter, "Site footer", []string{"include/general"}, wbtfFooter, "", ".ftr{color:blue}", []string{"menu"})
	single := wbtfTemplate(t, srv, webbuilder.TemplateSinglePost, "Post layout", []string{"include/singular/post"}, wbtfSingle, wbtfSingleDoc, "", nil)
	arch := wbtfTemplate(t, srv, webbuilder.TemplateArchive, "Archive layout", []string{"include/archive"}, wbtfArchive, wbtfArchiveDoc, "", nil)
	wbtfTemplate(t, srv, webbuilder.TemplateSearch, "Search layout", []string{"include/archive/search"}, wbtfSearch, wbtfSearchDoc, "", nil)
	wbtfTemplate(t, srv, webbuilder.Template404, "Lost layout", []string{"include/404"}, wbtf404, "", "", nil)

	// --- a single post -------------------------------------------------------------------------
	code, body := wbtfGet(t, base+"/"+p1.Slug+"/")
	if code != http.StatusOK {
		t.Fatalf("single: %d %s", code, lastRenderError.Load())
	}
	wbtfWant(t, "single", body,
		"hello-pursae-css", `<div class="wb-part wb-part-header">`, `<div class="wb-part wb-part-footer">`, `<main id="content" class="site-main wb-main-part">`,
		"HEADER-MARK", "FOOTER-MARK", "SINGLE-MARK", "wb-site",
		`<span data-wb-if-not="site.logo" data-wb-slot="site.title">Toggle &lt;script&gt;alert(1)&lt;/script&gt; &amp; Co</span>`,
		`<p class="tag" data-wb-slot="site.tagline">A &lt;b&gt;tag&lt;/b&gt;line</p>`,
		`<span data-wb-slot="menu.label">Say &#34;Hi&#34;</span>`,
		`<h1 data-wb-slot="post.title">Alpha First</h1>`,
		`<time data-wb-slot="post.date" data-wb-format="Y-m-d" datetime="`, `">`+p1.Date().In(srv.loc()).Format("2006-01-02")+`</time>`,
		`<span data-wb-slot="post.author">admin</span>`,
		`rel="tag">News &amp; Views</a>`,
		"Alpha body one two",                                // post.content: theContent
		`class="navigation post-navigation"`, "Beta Second", // post.nav (next post)
		`id="comments"`, // post.comments: Hello Pursae's comments template
		`<a data-wb-slot="post.comments-url" href="`+base+`/`+p1.Slug+`/#comments">`,
		`<span data-wb-slot="post.comments-count">0</span>`,
		`<style id="wb-css-`+itoa64(hdr.ID)+`">`, ".hdr{color:red}", `<style id="wb-css-`+itoa64(ftr.ID)+`">`,
		"web-builder-css", "web-builder-runtime-js", "<script>document.documentElement.classList.add('wb-js')</script>",
	)
	wbtfBad(t, "single", body, "<script>alert(1)", "<script>bad()", `href="javascript:`, "javascript:alert", "onerror", "site-header", "site-footer", "No related")
	if !regexp.MustCompile(`<a class="ml" data-wb-loop-item="" data-wb-slot="menu.url"><span data-wb-slot="menu.label">Say`).MatchString(body) {
		t.Error("the javascript: menu item lost its element (only the href must go)")
	}
	// The related loop shows one other post of the category (the stored perPage, not the forged attribute).
	if n := strings.Count(body, `class="rl"`); n != 1 {
		t.Errorf("related loop items: %d", n)
	}
	// Three roots (header, main, footer), one runtime, one class script; the runtime script comes
	// after the parts and the class script before the builder CSS.
	if n := strings.Count(body, "data-wb-root"); n != 3 {
		t.Errorf("roots: %d", n)
	}
	if strings.Count(body, "web-builder-runtime-js") != 1 || strings.Count(body, "classList.add('wb-js')") != 1 || strings.Count(body, "id='web-builder-css'") != 1 {
		t.Error("runtime, class script or base CSS printed more than once")
	}
	if strings.Index(body, "classList.add('wb-js')") > strings.Index(body, "web-builder-css") || strings.Index(body, "web-builder-runtime-js") < strings.Index(body, "FOOTER-MARK") {
		t.Error("asset order")
	}
	if strings.Count(body, "<main") != 1 {
		t.Error("not exactly one main element")
	}
	// The site's runtime reads the dismiss key from the closest root (several roots per page).
	if site, err := srv.wbSiteAssets(); err == nil && site.Runtime != "" {
		if b, err := fsReadFile(srv, webbuilder.EmbedDir+"/site/"+site.Runtime); err == nil && !strings.Contains(b, "closest(") {
			t.Error("the runtime does not look up the closest data-wb-root")
		}
	}

	// The current menu item is marked on its page; the page itself has no main template.
	_, body = wbtfGet(t, base+"/about-us/")
	if !regexp.MustCompile(`<a class="ml is-current" data-wb-loop-item="" data-wb-slot="menu.url" href="[^"]*/about-us/" aria-current="page">`).MatchString(body) {
		t.Errorf("current menu item not marked")
	}
	wbtfWant(t, "page", body, "HEADER-MARK", "About body.", `<main id="content" class="site-main">`)
	wbtfBad(t, "page", body, "SINGLE-MARK")
	// The split menu halves: 3 items, 2 + 1.
	if strings.Count(body, `class="ml`) != 3 {
		t.Errorf("menu items: %d", strings.Count(body, `class="ml`))
	}

	// The <script> title is escaped where the template shows it.
	_, body = wbtfGet(t, base+"/"+p3.Slug+"/")
	wbtfWant(t, "script title", body, `&lt;script&gt;bad()&lt;/script&gt; Gamma`)
	wbtfBad(t, "script title", body, "<script>bad()")

	// --- archive with the main loop, pagination, a forged and a custom loop ----------------------
	lb := srv.links(ctx)
	code, body = wbtfGet(t, base+pathOf(t, base, lb.TermLink(news)))
	if code != http.StatusOK {
		t.Fatalf("archive: %d", code)
	}
	wbtfWant(t, "archive", body, "ARCHIVE-MARK", `Category: <span>News &amp; Views</span>`, "All the news.",
		`class="navigation pagination"`, `/page/2/`)
	if n := strings.Count(body, `class="card"`); n != 2 {
		t.Errorf("main loop items: %d (posts_per_page 2)", n)
	}
	wbtfWant(t, "archive excerpt", body, `<p class="ex" data-wb-slot="post.excerpt" data-wb-words="5">Gamma body.</p>`)
	wbtfBad(t, "archive", body, "FORGED", "ARCH-EMPTY", "SINGLE-MARK", "<script>bad()")
	if n := strings.Count(body, `<li data-wb-loop-item="">`); n != 1 {
		t.Errorf("custom loop items: %d (stored perPage 1, forged 24)", n)
	}
	_, body = wbtfGet(t, base+pathOf(t, base, lb.TermLink(news))+"page/2/")
	if n := strings.Count(body, `class="card"`); n != 1 || !strings.Contains(body, "Alpha First") {
		t.Errorf("page 2: %d cards", n)
	}

	// --- posts index (archive template: include/archive) ------------------------------------------
	_, body = wbtfGet(t, base+"/")
	wbtfWant(t, "home", body, "ARCHIVE-MARK", "HEADER-MARK")

	// --- search -----------------------------------------------------------------------------------
	_, body = wbtfGet(t, base+"/?s=Beta")
	wbtfWant(t, "search", body, "SEARCH-MARK", `Results for <span data-wb-slot="search.query">Beta</span> (<span data-wb-slot="search.count">1</span>)`, `class="hit"`)
	wbtfBad(t, "search", body, "ARCHIVE-MARK", "SEARCH-EMPTY")
	_, body = wbtfGet(t, base+"/?s=%3Cscript%3Ezzz")
	wbtfWant(t, "empty search", body, "SEARCH-EMPTY", "&lt;script&gt;zzz")
	wbtfBad(t, "empty search", body, "<script>zzz")

	// --- 404 --------------------------------------------------------------------------------------
	code, body = wbtfGet(t, base+"/no-such-thing-here/")
	if code != http.StatusNotFound {
		t.Errorf("404 status %d", code)
	}
	wbtfWant(t, "404", body, "LOST-MARK", "HEADER-MARK", `data-wb-search=""`)

	// --- a builder page: its own doc is main, dynamic slots filled, layout site/canvas -----------
	own := wbtfPage(t, srv, "page", "Landing Own", `<section><h1 data-wb-slot="site.title"></h1><div data-wb-slot="post.content"></div>OWN-MARK</section>`, "", `{"layout":"site"}`)
	_, body = wbtfGet(t, base+"/landing-own/")
	wbtfWant(t, "own doc", body, "OWN-MARK", "HEADER-MARK", "FOOTER-MARK", `<h1 data-wb-slot="site.title">Toggle &lt;script&gt;`)
	wbtfBad(t, "own doc", body, "Fallback of Landing Own")
	canvasPage := wbtfPage(t, srv, "page", "Landing Canvas", `<nav>OWN-NAV</nav><section>CANVAS-MARK</section>`, `{"sections":[{"id":"n","kind":"nav","content":{}}],"site":{}}`, "")
	_, body = wbtfGet(t, base+"/landing-canvas/")
	wbtfWant(t, "canvas layout", body, "CANVAS-MARK", "OWN-NAV", "hello-pursae-css")
	wbtfBad(t, "canvas layout", body, "HEADER-MARK", "FOOTER-MARK", `id="site-header"`, `id="site-footer"`, "wb-part-header", "wb-part-footer", "layout canvas")
	_ = canvasPage
	_ = own

	// --- the admin bar ------------------------------------------------------------------------------
	_, abody, _ := get(t, admin, base+"/"+p1.Slug+"/")
	wbtfWant(t, "admin bar", abody, "wp-admin-bar-web_builder_edit_header", "wp-admin-bar-web_builder_edit_footer", "wp-admin-bar-web_builder_edit_template",
		html.EscapeString(wbEditURL(hdr.ID)), html.EscapeString(wbEditURL(single.ID)), html.EscapeString(wbEditURL(ftr.ID)))
	// ?wb_template_preview=<id>: a draft template fills its location for the admin, never for a
	// visitor, and the response is not cached.
	draft := wbtfTemplate(t, srv, webbuilder.TemplateSinglePage, "Draft page layout", nil, "<section>DRAFT-PAGE-MARK</section>", "", "", nil)
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET status = 'draft' WHERE id = $1`, draft.ID); err != nil {
		t.Fatal(err)
	}
	srv.wbTemplatesChanged(ctx)
	pv := base + "/landing-own/?" + webbuilder.PreviewQuery + "=" + itoa64(draft.ID)
	_, abody, _ = get(t, admin, pv)
	wbtfWant(t, "template preview", abody, "DRAFT-PAGE-MARK", "HEADER-MARK", "wp-admin-bar-web_builder_edit_template")
	wbtfBad(t, "template preview", abody, "OWN-MARK")
	_, body = wbtfGet(t, pv)
	wbtfWant(t, "visitor preview", body, "OWN-MARK")
	wbtfBad(t, "visitor preview", body, "DRAFT-PAGE-MARK")
	// A forced main template's post.content on a builder page is the page's plain content, not
	// its unfilled snapshot.
	draftC := wbtfTemplate(t, srv, webbuilder.TemplateSinglePage, "Draft content layout", nil, `<section>DRAFT-CONTENT-MARK<div data-wb-slot="post.content"></div></section>`, "", "", nil)
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET status = 'draft' WHERE id = $1`, draftC.ID); err != nil {
		t.Fatal(err)
	}
	srv.wbTemplatesChanged(ctx)
	_, abody, _ = get(t, admin, base+"/landing-own/?"+webbuilder.PreviewQuery+"="+itoa64(draftC.ID))
	wbtfWant(t, "content preview", abody, "DRAFT-CONTENT-MARK", "Fallback of Landing Own")
	wbtfBad(t, "content preview", abody, "OWN-MARK")
	// A header template previewed on a canvas page shows (the page otherwise has no header).
	draftHdr := wbtfTemplate(t, srv, webbuilder.TemplateHeader, "Draft header", nil, "<section>DRAFT-HEADER-MARK</section>", "", "", nil)
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET status = 'draft' WHERE id = $1`, draftHdr.ID); err != nil {
		t.Fatal(err)
	}
	srv.wbTemplatesChanged(ctx)
	_, abody, _ = get(t, admin, base+"/landing-canvas/?"+webbuilder.PreviewQuery+"="+itoa64(draftHdr.ID))
	wbtfWant(t, "header preview on a canvas page", abody, "DRAFT-HEADER-MARK", "CANVAS-MARK", "wb-part-header")
	_ = arch
	_ = p2
}

// A front page template, custom post type single and archive templates, and the fallbacks of the
// theme without a matching template.
func TestWBThemeFrontViews(t *testing.T) {
	srv, base, admin := wbtfStart(t)
	ctx := context.Background()
	acfgoRegistry.Lock()
	saved := acfgoRegistry.types
	types := map[string]*acfgoPostTypeDef{}
	for k, v := range saved {
		types[k] = v
	}
	types["book"] = &acfgoPostTypeDef{Name: "book", Public: true, Slug: "books", HasArchive: true, ArchiveSlug: "library",
		Labels: map[string]string{"name": "Books", "singular_name": "Book"}}
	acfgoRegistry.types = types
	acfgoRegistry.Unlock()
	t.Cleanup(func() {
		acfgoRegistry.Lock()
		acfgoRegistry.types = saved
		acfgoRegistry.Unlock()
	})
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels)
		VALUES ('book', 'Books', false, true, 'books', false, '{"title":true,"editor":true}', '{"name":"Books"}')`); err != nil {
		t.Fatal(err)
	}
	book := wbtfPost(t, srv, "book", "Dune Book", "<p>Spice.</p>", time.Hour)
	wbtfPost(t, srv, "book", "Emma Book", "<p>Novel.</p>", 2*time.Hour)
	post := wbtfPost(t, srv, "post", "Plain Post", "<p>Plain body.</p>", time.Hour)

	// No main templates: the theme's own content.
	_, body := wbtfGet(t, base+"/books/dune-book/")
	wbtfWant(t, "cpt without template", body, "Spice.", `<main id="content" class="site-main">`)

	wbtfTemplate(t, srv, webbuilder.TemplateSingle, "Book layout", []string{"include/singular/book"},
		`<article><h1 data-wb-slot="post.title"></h1><div data-wb-slot="post.content"></div>BOOK-MARK</article>`, "", "", nil)
	wbtfTemplate(t, srv, webbuilder.TemplateArchive, "Library", []string{"include/archive/type/book"},
		`<section><h1 data-wb-slot="archive.title"></h1><div data-wb-loop="bl"><p class="bk" data-wb-loop-item=""><span data-wb-slot="post.title"></span></p></div>LIBRARY-MARK</section>`,
		`{"sections":[{"id":"bl","kind":"loop","content":{"source":"main"}}],"site":{}}`, "", nil)

	code, body := wbtfGet(t, base+"/books/dune-book/")
	if code != http.StatusOK {
		t.Fatalf("book: %d", code)
	}
	wbtfWant(t, "book", body, "BOOK-MARK", `<h1 data-wb-slot="post.title">Dune Book</h1>`, "Spice.")
	_, body = wbtfGet(t, base+"/library/")
	wbtfWant(t, "library", body, "LIBRARY-MARK", "Archives: <span>Books</span>")
	if n := strings.Count(body, `class="bk"`); n != 2 {
		t.Errorf("library items: %d", n)
	}
	// The book template never fills a post, the library never the blog.
	_, body = wbtfGet(t, base+"/plain-post/")
	wbtfWant(t, "post", body, "Plain body.")
	wbtfBad(t, "post", body, "BOOK-MARK")
	_, body = wbtfGet(t, base+"/")
	wbtfBad(t, "blog", body, "LIBRARY-MARK")

	// A static front page with a front-page template.
	front := wbtfPost(t, srv, "page", "Welcome Home", "<p>Front body.</p>", time.Hour)
	if err := srv.settings.Set(ctx, map[string]any{"show_on_front": "page", "page_on_front": front.ID}); err != nil {
		t.Fatal(err)
	}
	wbtfTemplate(t, srv, webbuilder.TemplateFrontPage, "Front", []string{"include/singular/front_page"},
		`<section><h1 data-wb-slot="post.title"></h1>FRONT-MARK<div data-wb-loop="fl"><p class="fp" data-wb-loop-item=""><span data-wb-slot="post.title"></span></p></div></section>`,
		`{"sections":[{"id":"fl","kind":"loop","content":{"source":"query","perPage":"5","postType":"book","orderBy":"title","order":"asc"}}],"site":{}}`, "", nil)
	_, body = wbtfGet(t, base+"/")
	wbtfWant(t, "front", body, "FRONT-MARK", `<h1 data-wb-slot="post.title">Welcome Home</h1>`)
	tail := body[strings.Index(body, "FRONT-MARK")+1:]
	if i, j := strings.Index(tail, "Dune Book"), strings.Index(tail, "Emma Book"); i < 0 || j < 0 || i > j || strings.Count(tail, `class="fp"`) != 2 {
		t.Errorf("front loop: books by title asc: %.600s", tail)
	}
	wbtfBad(t, "front", body, "Front body.")
	// The front page's own path redirects to the home URL, so front_page rules see one URL.
	if code, _, loc := get(t, gooFrontClient(), base+"/welcome-home/"); code != http.StatusMovedPermanently || (loc != base+"/" && loc != "/") {
		t.Errorf("front page path: %d %q", code, loc)
	}
	// Custom loops show private posts only to users who may read them.
	hidden := wbtfPost(t, srv, "book", "Hidden Book", "<p>Private.</p>", time.Hour)
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET status = 'private' WHERE id = $1`, hidden.ID); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	_, body = wbtfGet(t, base+"/")
	wbtfBad(t, "visitor front", body, "Hidden Book")
	_, abody, _ := get(t, admin, base+"/")
	wbtfWant(t, "admin front", abody, "Hidden Book")
	_ = book
	_ = post
}

// Pages whose parts run a loop carry wb-loop and are purged when any viewable post is saved; the
// content is in the HTML without JavaScript.
func TestWBThemeFrontCache(t *testing.T) {
	srv, base, _ := wbtfStart(t)
	ctx := context.Background()
	if err := srv.settings.Set(ctx, map[string]any{"wpgo_cache_enabled": true}); err != nil {
		t.Fatal(err)
	}
	srv.configureCache()
	about := wbtfPost(t, srv, "page", "Cached About", "<p>About.</p>", time.Hour)
	p := wbtfPost(t, srv, "post", "Old Title", "<p>Body.</p>", time.Hour)
	wbtfTemplate(t, srv, webbuilder.TemplateFooter, "Recent footer", []string{"include/general"},
		`<footer><ul data-wb-loop="rf"><li class="rp" data-wb-loop-item=""><span data-wb-slot="post.title"></span></li></ul></footer>`,
		`{"sections":[{"id":"rf","kind":"loop","content":{"source":"query","perPage":"3"}}],"site":{}}`, "", nil)

	u := base + "/cached-about/"
	first := anonGet(t, u)
	b1 := bodyOf(first)
	if !strings.Contains(b1, `<li class="rp" data-wb-loop-item=""><span data-wb-slot="post.title">Old Title</span></li>`) {
		t.Fatalf("loop not rendered server side: %.2000s", b1)
	}
	// Readable without JS: no runtime (no features), no hiding class script.
	wbtfBad(t, "no-JS page", b1, "web-builder-runtime-js", "classList.add('wb-js')")
	if tags := first.Header.Get(cache.HeaderTags); tags != "" && (!strings.Contains(tags, webbuilder.CacheTagTheme) || !strings.Contains(tags, webbuilder.CacheTagLoop)) {
		t.Errorf("cache tags %q", tags)
	}
	if r := anonGet(t, u); r.Header.Get("X-WPGo-Cache") != "hit" {
		t.Fatalf("not cached: %q", r.Header.Get("X-WPGo-Cache"))
	}
	// Another post's save purges the page (it is not tagged with that post).
	p.Title = "New Title"
	if err := srv.store.UpdatePost(ctx, p, 1); err != nil {
		t.Fatal(err)
	}
	srv.afterPostSave(ctx, p)
	r := anonGet(t, u)
	b := bodyOf(r)
	if r.Header.Get("X-WPGo-Cache") == "hit" || !strings.Contains(b, "New Title") {
		t.Errorf("loop page not purged on a post save: %q", r.Header.Get("X-WPGo-Cache"))
	}
	// Saving a template purges everything (wbTemplatesChanged); a nav menu item does not reach the loop purge.
	if anonGet(t, u).Header.Get("X-WPGo-Cache") != "hit" {
		t.Fatal("not cached again")
	}
	srv.wbPurgeLoops(ctx, &store.Post{ID: 1, Type: "nav_menu_item", Status: "publish"})
	if anonGet(t, u).Header.Get("X-WPGo-Cache") != "hit" {
		t.Error("a non-viewable type purged the loop pages")
	}
	_ = about
}

// Other themes keep the canvas, with dynamic slots filled; plugin off: the theme.
func TestWBThemeFrontOtherTheme(t *testing.T) {
	srv, base, _ := wbtfStart(t)
	ctx := context.Background()
	if err := srv.switchTheme(ctx, defaultTheme); err != nil {
		t.Fatal(err)
	}
	wbtfTemplate(t, srv, webbuilder.TemplateHeader, "Ignored header", []string{"include/general"}, "IGNORED-HEADER", "", "", nil)
	p := wbtfPage(t, srv, "page", "Canvas Dyn", `<section><h1 data-wb-slot="site.title"></h1><p data-wb-slot="post.title"></p>CDYN-MARK</section>`, "", "")
	_, body := wbtfGet(t, base+"/canvas-dyn/")
	wbtfWant(t, "canvas", body, "CDYN-MARK", `<h1 data-wb-slot="site.title">Toggle Site</h1>`, `<p data-wb-slot="post.title">Canvas Dyn</p>`, `<main id="wb-main">`, "web-builder-page")
	wbtfBad(t, "canvas", body, "IGNORED-HEADER", "wb-part", "twentytwentyfive-style-css")
	_ = p
}

// fsReadFile reads an embedded web file of the server.
func fsReadFile(srv *Server, name string) (string, error) {
	b, err := fs.ReadFile(srv.web, name)
	return string(b), err
}

// Store views render as synthetic pages: page rules must not reach them, the main template stays
// the store's, and a term archive is never mistaken for the page with the same id.
func TestWBThemeFrontStoreViews(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	f := gooFrontCatalog(t, srv)
	if err := srv.wbSetActive(ctx, true); err != nil {
		t.Fatal(err)
	}
	if got := srv.settings.String("stylesheet"); got != webbuilder.HelloPursaeTheme {
		t.Fatalf("stylesheet %s", got)
	}
	wbtfTemplate(t, srv, webbuilder.TemplateSinglePage, "Page layout", []string{"include/singular/page"}, `<div class="wb-page" data-wb-root><p>PAGE-TPL-MARK</p></div>`, "", "", nil)
	wbtfTemplate(t, srv, webbuilder.TemplateHeader, "Page header", []string{"include/general", "exclude/singular/page"}, `<div class="wb-page" data-wb-root><p>HDR-MARK</p></div>`, "", "", nil)
	wbtfTemplate(t, srv, webbuilder.TemplateHeader, "Entry header", []string{"include/singular/page/" + itoa64(f.cat.ID)}, `<div class="wb-page" data-wb-root><p>ENTRY-HDR-MARK</p></div>`, "", "", nil)
	v := gooFrontClient()
	for _, u := range []string{"/shop/", "/product/shirt/", "/product-category/clothing/"} {
		code, body, _ := get(t, v, base+u)
		if code != http.StatusOK {
			t.Fatalf("%s: %d", u, code)
		}
		if strings.Contains(body, "PAGE-TPL-MARK") {
			t.Errorf("%s: a page template took over the store view", u)
		}
		if !strings.Contains(body, "HDR-MARK") || strings.Contains(body, "ENTRY-HDR-MARK") {
			t.Errorf("%s: header: page-rule header %v, entry header %v", u, strings.Contains(body, ">HDR-MARK"), strings.Contains(body, "ENTRY-HDR-MARK"))
		}
	}
}

// A draft autosave changes nothing a loop shows: it does not purge the wb-loop pages; a publish does.
func TestWBThemeFrontAutosaveKeepsLoopCache(t *testing.T) {
	srv, base, c := wbtfStart(t)
	admin := newWBRClient(t, c, base)
	if !srv.settings.Bool("wpgo_cache_enabled") {
		t.Skip("WPGo Cache is off by default")
	}
	wbtfTemplate(t, srv, webbuilder.TemplateFooter, "Loop footer", []string{"include/general"},
		`<section>LOOP-FOOTER<div data-wb-loop="fl"><p data-wb-loop-item=""><span data-wb-slot="post.title"></span></p></div></section>`,
		`{"sections":[{"id":"fl","kind":"loop","content":{"source":"query","perPage":"5","postType":"post"}}],"site":{}}`, "", nil)
	draft := wbrPost(t, srv, "post", "Draft In Progress", "draft", 1)
	anonGet(t, base+"/")
	if r := anonGet(t, base+"/"); r.Header.Get("X-WPGo-Cache") != "hit" {
		t.Fatalf("front page not cached: %q", r.Header.Get("X-WPGo-Cache"))
	} else if !strings.Contains(bodyOf(r), "LOOP-FOOTER") {
		t.Fatal("the loop footer does not render")
	}
	if code, v, _ := admin.do(http.MethodPost, "/wp/v2/posts/"+itoa64(draft.ID)+"/autosaves", map[string]any{"content": "<p>typing</p>"}, nil); code != http.StatusOK {
		t.Fatalf("autosave: %d %v", code, v)
	}
	if r := anonGet(t, base+"/"); r.Header.Get("X-WPGo-Cache") != "hit" {
		t.Errorf("a draft autosave purged the loop pages: %q", r.Header.Get("X-WPGo-Cache"))
	}
	if code, v, _ := admin.do(http.MethodPost, "/wp/v2/posts/"+itoa64(draft.ID), map[string]any{"status": "publish"}, nil); code != http.StatusOK {
		t.Fatalf("publish: %d %v", code, v)
	}
	if r := anonGet(t, base+"/"); r.Header.Get("X-WPGo-Cache") == "hit" {
		t.Error("publishing a post did not purge the loop pages")
	}
}

// Trashing from the admin purges the loop pages again once the trash is stored: a page rendered
// between the early purge (afterPostSave) and the trash does not keep the post in the cache.
func TestWBThemeFrontTrashPurgesLoopsAfterStore(t *testing.T) {
	srv, base, _ := wbtfStart(t)
	if !srv.settings.Bool("wpgo_cache_enabled") {
		t.Skip("WPGo Cache is off by default")
	}
	ctx := context.Background()
	wbtfTemplate(t, srv, webbuilder.TemplateFooter, "Loop footer", []string{"include/general"},
		`<section>LOOP-FOOTER<div data-wb-loop="fl"><p data-wb-loop-item=""><span data-wb-slot="post.title"></span></p></div></section>`,
		`{"sections":[{"id":"fl","kind":"loop","content":{"source":"query","perPage":"5","postType":"post"}}],"site":{}}`, "", nil)
	doomed := wbtfPost(t, srv, "post", "Soon Trashed", "<p>x</p>", 0)
	// A concurrent visitor: re-renders (and caches) the page right after every post save hook.
	prev := srv.onPostSaved
	srv.onPostSaved = func(c context.Context, p *store.Post) {
		if prev != nil {
			prev(c, p)
		}
		if p.ID == doomed.ID {
			anonGet(t, base+"/about-the-loop/") // any URL: a cached page with the footer loop
		}
	}
	if b := bodyOf(anonGet(t, base+"/about-the-loop/")); !strings.Contains(b, "Soon Trashed") {
		t.Fatal("the loop does not list the post")
	}
	adminU, err := srv.store.UserByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	id := itoa64(doomed.ID)
	if w := wbtaDirect(srv, adminU, "/wp-admin/post?post="+id+"&action=trash", "trash-post_"+id, srv.postEdit); w.Code != http.StatusFound {
		t.Fatalf("trash: %d", w.Code)
	}
	if b := bodyOf(anonGet(t, base+"/about-the-loop/")); strings.Contains(b, "Soon Trashed") {
		t.Error("the cached loop still lists the trashed post")
	}
}
