package webbuilder

import (
	"fmt"
	"strings"
	"testing"
)

// A WordPress copy saved with pretty permalinks: every post is <slug>/index.html alone in its
// folder at the site's top, beside the pages (blog/, hakkimizda/), and the blog page lists the
// posts. The posts carry the original site's record numbers (id="post-1699", postid-1699, a
// hidden comment_post_ID) and their body-end scripts its post id and nonce.
func leafSite() []SitePage {
	head := `<!doctype html><html lang="tr"><head><title>%s</title>
<script type="application/ld+json">{"name":"%s"}</script>
<link rel="stylesheet" href="../wp-content/style.css?ver=%s"></head>`
	nav := `<header class="top"><nav><a class="n" href="../blog/index.html">Blog</a><a class="n" href="../hakkimizda/index.html">About</a></nav></header>`
	post := func(slug, title, id, body string) SitePage {
		return SitePage{Path: slug + "/index.html", HTML: fmt.Sprintf(head, title, title, id) +
			`<body class="wp-singular single single-post postid-` + id + `">` + nav +
			`<main><section class="hero"><h1 class="entry-title">` + title + `</h1></section>
<article id="post-` + id + `" class="post-` + id + ` post"><div class="content">` + body + `</div>
<form class="comment"><input type="hidden" name="comment_post_ID" value="` + id + `"><textarea name="comment"></textarea></form></article></main>
<footer class="foot">© Decoland</footer>
<script>var love = {"postID":"` + id + `","nonce":"a1b2c3d4` + id + `"};</script>
</body></html>`}
	}
	card := func(slug, title, id string) string {
		return `<article class="card post-` + id + `"><a href="../` + slug + `/index.html"><h3>` + title + `</h3></a><p>Read ` + title + ` and more about the market today.</p></article>`
	}
	page := func(slug, title, main string) SitePage {
		return SitePage{Path: slug + "/index.html", HTML: fmt.Sprintf(head, title, title, "1") + `<body class="wp-singular page">` + nav +
			`<main>` + main + `</main><footer class="foot">© Decoland</footer></body></html>`}
	}
	return []SitePage{
		post("bursa", "Bursa from the world", "1699", `<p>Bursa is a furniture capital.</p><h1>A second heading</h1><p>234 units in 160,000 m².</p>`),
		post("marmara", "Marmara trade", "1698", `<p>Trade flows through the Marmara.</p><ul><li>Exports</li><li>People</li></ul>`),
		post("showroom", "Showroom strategy", "1697", `<p>A showroom is a brand's stage.</p><blockquote>Show it.</blockquote><p>Then sell it.</p>`),
		page("blog", "Blog", `<section><h1>Blog</h1><div class="grid">`+card("bursa", "Bursa from the world", "1699")+card("marmara", "Marmara trade", "1698")+
			card("showroom", "Showroom strategy", "1697")+`</div></section>`),
		page("hakkimizda", "About", `<section><h1>About</h1><p>We build.</p></section><section><h2>Team</h2></section>`),
	}
}

func TestSiteLeafSetsAndPostCollection(t *testing.T) {
	pages := leafSite()
	urls := map[string]string{}
	for _, p := range pages {
		urls[p.Path] = "/" + strings.TrimSuffix(p.Path, "index.html")
	}
	res, err := ConvertSite(pages, SiteImportOptions{KitURL: "/k/", KitFiles: map[string]bool{"wp-content/style.css": true}, URLs: urls})
	if err != nil {
		t.Fatal(err)
	}
	sets := SiteLeafSets(res)
	if len(sets["*:single-post"]) != 3 || len(sets["*:page"]) != 2 {
		t.Fatalf("sets: %v", sets)
	}
	if p, ok := SiteLeafParent("*:single-post"); !ok || p != "" {
		t.Fatalf("parent: %q %v", p, ok)
	}
	// Not as posts (the site's permalinks would move them): the group is made, the list stays
	// markup (a loop of pages could not select them).
	b := BindSite(res, SiteBindOptions{Groups: map[string]bool{"*:single-post": true}})
	if len(b.Groups) != 1 || b.Groups[0].AsPosts || len(b.Listings) != 0 {
		t.Fatalf("as pages: groups %+v listings %+v notes %v", b.Groups, b.Listings, b.Notes)
	}
	res, _ = ConvertSite(pages, SiteImportOptions{KitURL: "/k/", KitFiles: map[string]bool{"wp-content/style.css": true}, URLs: urls})
	b = BindSite(res, SiteBindOptions{Groups: map[string]bool{"*:single-post": true}, PostSets: map[string]bool{"*:single-post": true}})
	if len(b.Groups) != 1 || !b.Groups[0].AsPosts || len(b.Groups[0].Posts) != 3 {
		t.Fatalf("groups: %+v notes %v", b.Groups, b.Notes)
	}
	// Each post's title is its hero <h1>, also for a post whose content has another <h1>.
	for _, p := range b.Groups[0].Posts {
		if pl := b.Plans[p.Page]; pl == nil || pl.Title != p.Title || p.Title == "" {
			t.Errorf("title of %s: plan %+v, group %q", res.Pages[p.Page].Path, pl, p.Title)
		}
	}
	g := b.Groups[0]
	if strings.Contains(g.Head, "ld+json") || !strings.Contains(g.Head, "style.css") {
		t.Errorf("group head: %s", g.Head)
	}
	if len(b.Listings) != 1 || res.Pages[b.Listings[0].Page].Path != "blog/index.html" {
		t.Fatalf("listings: %+v notes %v", b.Listings, b.Notes)
	}
	l := b.Listings[0]
	binds := ParseCodeBinds(res.Pages[l.Page].Code.Sections[0].Binds)
	var loop *CodeBind
	for _, x := range binds {
		if x.Kind == BindLoop {
			loop = x
		}
	}
	if loop == nil || loop.Query[LoopPostType] != "post" || loop.Query[LoopParent] != "" {
		t.Fatalf("loop: %+v", binds)
	}
	// The pages of another kind are not one template (and are reported).
	b = BindSite(res, SiteBindOptions{Groups: map[string]bool{"*:page": true}})
	if len(b.Groups) != 0 || !strings.Contains(strings.Join(b.Notes, "\n"), "*:page") {
		t.Errorf("pages: %+v %v", b.Groups, b.Notes)
	}
}
