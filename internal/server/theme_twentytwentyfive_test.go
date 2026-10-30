package server

import (
	"context"
	"html"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// installLibraryTheme installs a theme of the library (Add Themes › Library › Install) and, with
// activate, activates it: the tests of widget areas and theme switching use one, since the default
// theme has no widget areas.
func installLibraryTheme(t *testing.T, c *http.Client, base, slug string, activate bool) {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=library")
	m := regexp.MustCompile(`href="(/wp-admin/theme-install\?action=install-theme&amp;theme=` + slug + `&amp;_wpnonce=[^"&]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no Install link for %s in the library", slug)
	}
	if code, _, loc := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound || !strings.Contains(loc, "installed="+slug) {
		t.Fatalf("install %s: %d %s", slug, code, loc)
	}
	if !activate {
		return
	}
	_, body, _ = get(t, c, base+"/wp-admin/themes")
	a := regexp.MustCompile(`action=activate&(?:amp;)?stylesheet=` + slug + `&(?:amp;)?_wpnonce=([0-9a-zA-Z]+)`).FindStringSubmatch(body)
	if a == nil {
		t.Fatalf("no Activate link for %s", slug)
	}
	if code, _, _ := get(t, c, base+"/wp-admin/themes?action=activate&stylesheet="+slug+"&_wpnonce="+a[1]); code != http.StatusFound {
		t.Fatalf("activate %s: %d", slug, code)
	}
}

// TestTwentyTwentyFive renders the default theme's templates: header and navigation, the blog's query
// loop and pagination, a single post with its meta, post navigation, comments and More posts, a page,
// archives, search, 404, the per-block core styles and the front-end performance basics.
func TestTwentyTwentyFive(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	anon := &http.Client{CheckRedirect: c.CheckRedirect}
	if srv.activeTheme().Slug != "twentytwentyfive" || srv.settings.String("stylesheet") != "twentytwentyfive" {
		t.Fatalf("default theme %s", srv.activeTheme().Slug)
	}
	var admin int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&admin)
	cat := &store.Term{Taxonomy: "category", Slug: "meadows", Name: "Meadows", Description: "Wild flowers."}
	if err := srv.store.InsertTerm(ctx, cat); err != nil {
		t.Fatal(err)
	}
	tag := &store.Term{Taxonomy: "post_tag", Slug: "spring", Name: "Spring"}
	if err := srv.store.InsertTerm(ctx, tag); err != nil {
		t.Fatal(err)
	}
	var first, last *store.Post
	for i := 0; i < 12; i++ {
		p := mustInsertPost(t, srv, &store.Post{Type: "post", AuthorID: admin, Title: "Meadow note " + itoa(i+1),
			Content:       "<!-- wp:paragraph -->\n<p>Buttercups and clover, note " + itoa(i+1) + ".</p>\n<!-- /wp:paragraph -->\n\n<!-- wp:table -->\n<figure class=\"wp-block-table\"><table><tbody><tr><td>a</td></tr></tbody></table></figure>\n<!-- /wp:table -->",
			CommentStatus: "open"})
		if err := srv.store.SetPostTerms(ctx, p.ID, "category", []int64{cat.ID}); err != nil {
			t.Fatal(err)
		}
		if err := srv.store.SetPostTerms(ctx, p.ID, "post_tag", []int64{tag.ID}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = p
		}
		last = p
	}
	_ = srv.store.RecountTerms(ctx, []int64{cat.ID, tag.ID})
	lb := srv.links(ctx)

	check := func(name, path string, status int, wants ...string) string {
		t.Helper()
		code, body, _ := get(t, anon, base+path)
		if code != status {
			t.Fatalf("%s: %d, want %d", name, code, status)
		}
		for _, w := range wants {
			if !strings.Contains(body, w) {
				t.Errorf("%s misses %q", name, w)
			}
		}
		return body
	}

	// Blog home: header, page-list navigation fallback, the query loop with full content and
	// dates, query pagination, the footer; one theme stylesheet, the font preloaded, no jQuery.
	home := check("home", "/", http.StatusOK,
		`<body class="home blog wp-embed-responsive wp-theme-twentytwentyfive">`,
		`<a class="skip-link screen-reader-text" id="wp-skip-link" href="#wp--skip-link--target">Skip to content</a><div class="wp-site-blocks"><header class="wp-block-template-part">`,
		`<p class="wp-block-site-title"><a href="`+base+`/" target="_self" rel="home" aria-current="page">Toggle Site</a></p>`,
		`class="wp-block-pages-list__item wp-block-navigation-item`, `wp-block-navigation__responsive-container-open`,
		`<main id="wp--skip-link--target" class="wp-block-group has-global-padding is-layout-constrained wp-block-group-is-layout-constrained"`,
		`<h1 class="wp-block-heading has-text-align-left">Blog</h1>`,
		`<li class="wp-block-post post-`, `<h2 class="wp-block-post-title has-x-large-font-size"><a href="`+lb.Permalink(last)+`" target="_self">Meadow note 12</a></h2>`,
		`Buttercups and clover, note 12.`, `class="wp-block-post-date has-small-font-size"><a href="`+lb.Permalink(last)+`"><time datetime="`,
		`<nav class="alignwide wp-block-query-pagination`, `<span aria-current="page" class="page-numbers current">1</span>`,
		`<a href="`+base+`/page/2/" class="wp-block-query-pagination-next">Next Page`,
		`<footer class="wp-block-template-part">`, `<h2 class="wp-block-site-title">`,
		`<link rel="preload" href="/wp-content/themes/twentytwentyfive/fonts/manrope/Manrope-VariableFont_wght.woff2" as="font" type="font/woff2" crossorigin>`,
		`<link rel='stylesheet' id='twentytwentyfive-style-css' href='/wp-content/themes/twentytwentyfive/style.min.css?ver=1.5' media='all' />`,
		`<script src="/wp-content/themes/twentytwentyfive/js/navigation.js?ver=1.5" id="twentytwentyfive-navigation-js" defer data-wp-strategy="defer"></script>`,
		`<style id='wp-block-styles-inline-css'>`, `.wp-block-table`)
	for _, unwanted := range []string{"jquery", "wp-block-library-css", "classic-theme-styles", "global-styles-inline-css", "wpgo:block-styles",
		".wp-block-cover,", "<script>"} {
		if strings.Contains(home, unwanted) {
			t.Errorf("home page has %q", unwanted)
		}
	}
	if strings.Contains(home, ".wp-block-navigation{") {
		t.Error("the inline block styles repeat a block the theme stylesheet carries")
	}
	check("home page 2", "/page/2/", http.StatusOK, "paged-2", `class="wp-block-query-pagination-previous"`, "Previous Page", "Meadow note 2")

	// The stylesheet and its fonts are served; the font face swaps.
	css := check("stylesheet", "/wp-content/themes/twentytwentyfive/style.min.css?ver=1.5", http.StatusOK,
		"font-family:Manrope", "font-display:swap", "url('fonts/manrope/Manrope-VariableFont_wght.woff2')", "--wp--preset--color--base:#FFFFFF",
		".wp-container-core-group-is-layout-ec1e7a1e{", ".is-style-post-terms-1", "ul.is-style-checkmark-list", ".wp-block-comments .comment-body")
	if len(css) > 120000 {
		t.Errorf("style.min.css is %d bytes", len(css))
	}
	check("font", "/wp-content/themes/twentytwentyfive/fonts/manrope/Manrope-VariableFont_wght.woff2", http.StatusOK)

	// Single post: title, written by, content, tags, post navigation, comments, More posts.
	srv.store.Pool.Exec(ctx, `INSERT INTO comments (post_id, author_name, author_email, content, status, type) VALUES ($1, 'Ada', 'ada@example.test', 'Lovely meadow.', 'approved', 'comment')`, first.ID)
	srv.store.Pool.Exec(ctx, `UPDATE posts SET comment_count = 1 WHERE id = $1`, first.ID)
	single := check("single", pathOf(t, base, lb.Permalink(first)), http.StatusOK,
		"post-template-default single single-post postid-"+itoa64(first.ID),
		`<h1 class="wp-block-post-title">Meadow note 1</h1>`, `<p>Written by </p>`, `class="wp-block-post-author-name__link">`,
		`<div style="font-weight:300" class="taxonomy-category wp-block-post-terms"><a href="`+lb.TermLink(cat)+`" rel="tag">Meadows</a></div>`,
		`<div class="taxonomy-post_tag is-style-post-terms-1 wp-block-post-terms"><a href="`+lb.TermLink(tag)+`" rel="tag">Spring</a></div>`,
		`aria-label="Post navigation"`, `<div class="post-navigation-link-next wp-block-post-navigation-link"><a href="`, `rel="next">Meadow note 2</a>`,
		`<div id="comments" class="wp-block-comments wp-block-comments-query-loop"`, `<h3 class="wp-block-comments-title has-large-font-size">One response to &#8220;Meadow note 1&#8221;</h3>`,
		`<ol class="comment-list wp-block-comment-template">`, "Lovely meadow.", `avatar-50 photo' height='50' width='50'`,
		`<div class="wp-block-post-comments-form"><div id="respond" class="comment-respond">`, `<h3 id="reply-title" class="comment-reply-title">Leave a Reply`,
		`class="submit wp-block-button__link wp-element-button"`, "comment-reply.min.js",
		`>More posts</h2>`, `<h3 class="wp-block-post-title has-large-font-size"><a href="`+lb.Permalink(last)+`" target="_self">Meadow note 12</a></h3>`)
	_ = single

	// Page, archive, search, 404.
	check("page", "/sample-page/", http.StatusOK, `<h1 class="wp-block-post-title">Sample Page</h1>`, "page-template-default page page-id-",
		`aria-current="page">Sample Page</a>`, `current-menu-item`)
	check("category", pathOf(t, base, lb.TermLink(cat)), http.StatusOK, `<h1 class="wp-block-query-title">Category: <span>Meadows</span></h1>`,
		`<div class="wp-block-term-description"><p>Wild flowers.</p>`, "archive category category-meadows")
	check("search", "/?s=clover", http.StatusOK, `<h1 class="wp-block-query-title">Search results for: &#8220;clover&#8221;</h1>`,
		`<input class="wp-block-search__input" id="wp-block-search__input-1" placeholder="Type here..." value="clover" type="search" name="s" required />`,
		"search search-results", `>More posts</h2>`)
	check("empty search", "/?s=zzzz", http.StatusOK, "search-no-results", "Sorry, but nothing was found. Please try a search with different keywords.")
	check("404", "/no-such-page/", http.StatusNotFound, `<h1 class="wp-block-heading">Page not found</h1>`,
		`<img src="/wp-content/themes/twentytwentyfive/images/404-image.webp" alt="Small totara tree on ridge above Long Point" width="760" height="973"`)

	// A menu at the Primary Menu location replaces the page list.
	menuID, err := srv.store.CreateMenu(ctx, "Main")
	if err != nil {
		t.Fatal(err)
	}
	parent := &store.EditItem{MenuID: menuID, Label: "Meadows", Kind: "taxonomy", Object: "category", TargetID: cat.ID, Order: 1, Status: "publish"}
	if err := srv.store.AddMenuItem(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.AddMenuItem(ctx, &store.EditItem{MenuID: menuID, ParentID: parent.ID, Label: "Contact us", Kind: "custom", Object: "custom", URL: "https://example.test/contact", Order: 2, Status: "publish"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetMenuLocations(ctx, map[string]int64{"primary": menuID}); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	nav := check("menu", pathOf(t, base, lb.TermLink(cat)), http.StatusOK,
		`<li class="wp-block-navigation-item has-child open-on-hover-click current-menu-item wp-block-navigation-submenu"><a class="wp-block-navigation-item__content" href="`+lb.TermLink(cat)+`" aria-current="page"><span class="wp-block-navigation-item__label">Meadows</span></a>`,
		`<button aria-label="Meadows submenu" class="wp-block-navigation__submenu-icon wp-block-navigation-submenu__toggle" aria-expanded="false">`,
		`<span class="wp-block-navigation-item__label">Contact us</span>`)
	if strings.Contains(nav, "wp-block-pages-list__item") {
		t.Error("the page list fallback shows next to the menu")
	}

	// Featured image: 3:2, the large size with srcset, the first one eager, the others lazy.
	img := mustUploadTestImage(t, srv, admin)
	srv.store.Pool.Exec(ctx, `UPDATE posts SET featured_media_id = $1 WHERE type = 'post'`, img)
	srv.purgeAll(ctx)
	home = check("home with images", "/", http.StatusOK, `<figure style="aspect-ratio:3/2;" class="wp-block-post-featured-image"><a href="`, `fetchpriority="high"`,
		`sizes="(max-width: 645px) 100vw, 645px"`, `style="width:100%;height:100%;object-fit:cover;aspect-ratio:3/2;"`)
	if strings.Count(home, `fetchpriority="high"`) != 1 || strings.Count(home, `loading="lazy"`) < 2 {
		t.Errorf("featured images: %d eager, %d lazy", strings.Count(home, `fetchpriority="high"`), strings.Count(home, `loading="lazy"`))
	}
	for _, tag := range regexp.MustCompile(`<img [^>]*>`).FindAllString(home, -1) {
		if !strings.Contains(tag, ` width="`) || !strings.Contains(tag, ` height="`) {
			t.Errorf("image without dimensions: %s", tag)
		}
	}

	// Page No Title: the content alone.
	pg := mustInsertPost(t, srv, &store.Post{Type: "page", AuthorID: admin, Title: "Landing", Content: "<p>Landing text</p>", Meta: map[string]any{"_wp_page_template": "page-no-title"}})
	body := check("page no title", pathOf(t, base, lb.Permalink(pg)), http.StatusOK, "Landing text", `style="margin-top:0"`)
	if strings.Contains(body, `<h1 class="wp-block-post-title">Landing</h1>`) {
		t.Error("Page No Title prints the title")
	}

	// A site whose theme was one of the removed bundled themes runs on the default theme.
	if err := srv.settings.Set(ctx, map[string]any{"stylesheet": "twentysixteen", "template": "twentysixteen"}); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	check("removed theme", "/", http.StatusOK, "wp-theme-twentytwentyfive")
	srv.migrateRemovedThemes(ctx)
	if srv.settings.String("stylesheet") != "twentytwentyfive" || srv.settings.String("current_theme") != "Twenty Twenty-Five" {
		t.Errorf("migration left stylesheet %q", srv.settings.String("stylesheet"))
	}
	if code, _, _ := get(t, c, base+"/wp-admin/themes"); code != http.StatusOK {
		t.Errorf("themes screen after the migration: %d", code)
	}
}

// mustUploadTestImage stores a 1600x1200 PNG as an attachment through the media pipeline.
func mustUploadTestImage(t *testing.T, srv *Server, uid int64) int64 {
	t.Helper()
	f, err := os.Open("../../web/themes/twentytwentyfive/assets/screenshot.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	a, err := srv.insertAttachmentFile(context.Background(), "tt5-test.png", f, uid, 0)
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

// TestTwentyTwentyFiveBuiltAssets checks the generated files the theme ships: the color schemes
// only carry what differs from the default palette, every demo set validates.
func TestTwentyTwentyFiveBuiltAssets(t *testing.T) {
	s := &Server{web: os.DirFS("../../web")}
	th := themeBySlug("twentytwentyfive")
	for _, sc := range tt5Schemes[1:] {
		css := s.tt5SchemeCSS(map[string]any{"color_scheme": sc[0]})
		if !strings.Contains(css, "--wp--preset--color--base:") || len(css) > 4000 {
			t.Errorf("scheme %s: %d bytes", sc[0], len(css))
		}
	}
	if len(th.Demos) < 3 {
		t.Fatalf("%d demo sets", len(th.Demos))
	}
	for _, ds := range th.Demos {
		src, err := s.bundledDemoSource(th, &ds)
		if err != nil {
			t.Errorf("demo %s: %v", ds.Slug, err)
			continue
		}
		d := src.Demo
		if len(d.Posts) < 6 || len(d.Posts) > 10 || len(d.Pages) < 3 || len(d.Menus) == 0 || len(d.Categories) < 2 || len(d.Tags) == 0 {
			t.Errorf("demo %s: %d posts, %d pages, %d menus", ds.Slug, len(d.Posts), len(d.Pages), len(d.Menus))
		}
		front := false
		for _, p := range d.Pages {
			front = front || p.FrontPage
		}
		if !front {
			t.Errorf("demo %s has no homepage", ds.Slug)
		}
		for _, m := range d.Media {
			if !strings.HasSuffix(m.File, ".webp") {
				t.Errorf("demo %s: %s is not WebP", ds.Slug, m.File)
			}
		}
		if _, err := fs.Stat(s.web, "themes/twentytwentyfive/assets/"+ds.Image); err != nil {
			t.Errorf("demo %s: preview %s missing", ds.Slug, ds.Image)
		}
	}
}
