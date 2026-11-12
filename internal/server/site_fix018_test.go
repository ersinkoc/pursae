package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// TestPermalinkKeywordSegments: feed, page and comment-page are whole path segments, like
// WordPress' rewrite rules; slugs that merely end in those words are left alone.
func TestPermalinkKeywordSegments(t *testing.T) {
	for _, c := range []struct {
		path      string
		match     bool
		rest, arg string
	}{
		{"feed", true, "", ""},
		{"feed/atom", true, "", "atom"},
		{"news/feed", true, "news", ""},
		{"news/feed/atom", true, "news", "atom"},
		{"category/news/feed/rss2", true, "category/news", "rss2"},
		{"rss-feed", false, "", ""},
		{"chicken-feed", false, "", ""},
		{"category/news-feed", false, "", ""},
	} {
		m := feedRe.FindStringSubmatch(c.path)
		if (m != nil) != c.match || (m != nil && (m[1] != c.rest || m[2] != c.arg)) {
			t.Errorf("feedRe(%q) = %q", c.path, m)
		}
	}
	for _, c := range []struct {
		path      string
		match     bool
		rest, num string
	}{
		{"page/2", true, "", "2"},
		{"my-page/page/2", true, "my-page", "2"},
		{"category/x/page/10", true, "category/x", "10"},
		{"front-page/2", false, "", ""},
		{"homepage/3", false, "", ""},
	} {
		m := pagedRe.FindStringSubmatch(c.path)
		if (m != nil) != c.match || (m != nil && (m[1] != c.rest || m[2] != c.num)) {
			t.Errorf("pagedRe(%q) = %q", c.path, m)
		}
	}
	if m := commentPageRe.FindStringSubmatch("x-comment-page-2"); m != nil {
		t.Errorf("commentPageRe matched a slug: %q", m)
	}
	if m := commentPageRe.FindStringSubmatch("hello/comment-page-2"); m == nil || m[1] != "hello" || m[2] != "2" {
		t.Errorf("commentPageRe(hello/comment-page-2) = %q", m)
	}
}

func TestPostFormatTerm(t *testing.T) {
	if postFormatTerm("standard") != nil || postFormatTerm("nope") != nil || postFormatTerm("") != nil {
		t.Error("standard or unknown formats have no archive")
	}
	tm := postFormatTerm("post-format-aside")
	if tm == nil || tm.Slug != "post-format-aside" || tm.Taxonomy != "post_format" {
		t.Fatalf("aside term = %+v", tm)
	}
	q := &siteQuery{Kind: "taxonomy", Term: postFormatTerm("gallery")}
	if full, _ := q.archiveTitle(); full != "Galleries" {
		t.Errorf("gallery archive title = %q", full)
	}
}

// TestFrontRelease018 covers front-end fixes of 0.1.8: slugs ending in "feed", password-protected
// posts in the Latest Posts block and in anonymous search, 404s for empty date and paged archives,
// child categories in category feeds, post format and Pursae Custom Fields taxonomy archives,
// non-Latin slugs and the Page List block's nested links.
func TestFrontRelease018(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	adminID := installFrontSite(t, base, c, srv)
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if err := srv.settings.Set(ctx, map[string]any{"permalink_structure": "/%postname%/"}); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	lb := srv.links(ctx)

	// A slug that ends in "feed" is a post, and its own feed still works.
	feedPost := mustInsertPost(t, srv, &store.Post{Type: "post", AuthorID: adminID, Title: "Chicken Feed", Content: "<p>Grain for hens.</p>"})
	if feedPost.Slug != "chicken-feed" {
		t.Fatalf("slug = %q", feedPost.Slug)
	}
	if code, body, _ := get(t, anon, base+"/chicken-feed/"); code != http.StatusOK || !strings.Contains(body, "Grain for hens.") {
		t.Errorf("/chicken-feed/: %d", code)
	}
	if code, body, _ := get(t, anon, base+"/chicken-feed/feed/"); code != http.StatusOK || !strings.Contains(body, "<rss") {
		t.Errorf("/chicken-feed/feed/: %d", code)
	}

	// Password-protected posts: the Latest Posts block shows a notice, anonymous search skips them.
	mustInsertPost(t, srv, &store.Post{Type: "post", AuthorID: adminID, Title: "Locked memo", Password: "members",
		Content: "<p>Quetzalcoatl merger secret.</p>", Excerpt: ""})
	for _, mode := range []string{"excerpt", "full_post"} {
		lp := mustInsertPost(t, srv, &store.Post{Type: "page", AuthorID: adminID, Title: "Latest " + mode,
			Content: `<!-- wp:latest-posts {"displayPostContent":true,"displayPostContentRadio":"` + mode + `"} /-->`})
		code, body, _ := get(t, anon, lb.Permalink(lp))
		if code != http.StatusOK {
			t.Fatalf("latest posts page (%s): %d", mode, code)
		}
		if strings.Contains(body, "Quetzalcoatl") {
			t.Errorf("latest posts (%s) shows a protected post's text", mode)
		}
		if !strings.Contains(body, "This content is password protected.") || !strings.Contains(body, "Locked memo") {
			t.Errorf("latest posts (%s) misses the protected notice or title", mode)
		}
	}
	if _, body, _ := get(t, anon, base+"/?s=Quetzalcoatl"); !strings.Contains(body, "search-no-results") {
		t.Error("anonymous search finds a protected post by its hidden text")
	}
	if _, body, _ := get(t, anon, base+"/?s=Quetzalcoatl&feed=rss2"); strings.Contains(body, "Locked memo") {
		t.Error("search feed finds a protected post by its hidden text")
	}
	if _, body, _ := get(t, anon, base+"/wp-json/wp/v2/posts?search=Quetzalcoatl"); strings.Contains(body, "Locked memo") {
		t.Error("anonymous REST search finds a protected post by its hidden text")
	}
	if _, body, _ := get(t, anon, base+"/wp-json/wp/v2/search?search=Quetzalcoatl"); strings.Contains(body, "Locked memo") {
		t.Error("anonymous /wp/v2/search finds a protected post by its hidden text")
	}
	if _, body, _ := get(t, c, base+"/?s=Quetzalcoatl"); !strings.Contains(body, "search-results") || strings.Contains(body, "search-no-results") {
		t.Error("a logged-in search should still find the protected post")
	}

	// WP::handle_404(): empty date archives and paged archives past the end are 404s.
	empty := &store.Term{Taxonomy: "category", Name: "Empty shelf"}
	if err := srv.store.InsertTerm(ctx, empty); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"/1999/", "/2024/13/", "/page/99/", "/category/uncategorized/page/99/", "/?s=Hello&paged=50"} {
		if code, body, _ := get(t, anon, base+u); code != http.StatusNotFound || !strings.Contains(body, "error404") {
			t.Errorf("%s: %d, want 404", u, code)
		}
	}
	if code, _, _ := get(t, anon, base+"/category/empty-shelf/"); code != http.StatusOK {
		t.Errorf("an empty category's first page: %d, want 200", code)
	}

	// Category feeds include child categories, like the archive.
	parent := &store.Term{Taxonomy: "category", Name: "Regional"}
	if err := srv.store.InsertTerm(ctx, parent); err != nil {
		t.Fatal(err)
	}
	child := &store.Term{Taxonomy: "category", Name: "Harbour", ParentID: parent.ID}
	if err := srv.store.InsertTerm(ctx, child); err != nil {
		t.Fatal(err)
	}
	local := mustInsertPost(t, srv, &store.Post{Type: "post", AuthorID: adminID, Title: "Harbour reopens", Content: "<p>Boats.</p>"})
	if err := srv.store.SetPostTerms(ctx, local.ID, "category", []int64{child.ID}); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	if _, body, _ := get(t, anon, base+"/category/regional/feed/"); !strings.Contains(body, "Harbour reopens") {
		t.Error("parent category feed misses a post of its child category")
	}

	// Post format archives: /type/aside/ lists asides, ?post_format=aside redirects there.
	aside := mustInsertPost(t, srv, &store.Post{Type: "post", AuthorID: adminID, Title: "Quick aside", Format: "aside", Content: "<p>Short.</p>"})
	srv.purgeAll(ctx)
	code, body, _ := get(t, anon, base+"/type/aside/")
	if code != http.StatusOK || !strings.Contains(body, "Quick aside") || strings.Contains(body, "Chicken Feed") || !strings.Contains(body, "Asides") {
		t.Errorf("/type/aside/: %d", code)
	}
	if code, _, loc := get(t, anon, base+"/?post_format=aside"); code != http.StatusMovedPermanently || loc != base+"/type/aside/" {
		t.Errorf("?post_format=aside: %d %q", code, loc)
	}
	for _, u := range []string{"/type/standard/", "/type/nope/"} {
		if code, _, _ := get(t, anon, base+u); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", u, code)
		}
	}
	_ = aside

	// Pursae Custom Fields taxonomy archives (/?taxonomy=genre&term=fiction).
	acfgoActivate(t, c, base)
	if err := srv.acfgoSaveObject(ctx, acfgoPostType, map[string]any{"title": "Books", "post_type": "book", "public": true,
		"labels": map[string]any{"name": "Books", "singular_name": "Book"}}); err != nil {
		t.Fatal(err)
	}
	if err := srv.acfgoSaveObject(ctx, acfgoTaxType, map[string]any{"title": "Genres", "taxonomy": "genre", "public": true,
		"hierarchical": true, "object_type": []any{"book"}, "labels": map[string]any{"name": "Genres", "singular_name": "Genre"}}); err != nil {
		t.Fatal(err)
	}
	fiction := &store.Term{Taxonomy: "genre", Name: "Fiction"}
	if err := srv.store.InsertTerm(ctx, fiction); err != nil {
		t.Fatal(err)
	}
	dune := mustInsertPost(t, srv, &store.Post{Type: "book", AuthorID: adminID, Title: "Dune", Content: "<p>Spice.</p>"})
	if err := srv.store.SetPostTerms(ctx, dune.ID, "genre", []int64{fiction.ID}); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	termURL := srv.links(ctx).TermLink(fiction)
	code, body, _ = get(t, anon, termURL)
	if code != http.StatusOK || !strings.Contains(body, "Dune") || strings.Contains(body, "Chicken Feed") || !strings.Contains(body, "Genre: <span>Fiction</span>") {
		t.Errorf("%s: %d", termURL, code)
	}
	// Paging and feeds of a query-var taxonomy archive: links.Builder.Paged gives
	// /page/2/?taxonomy=genre&term=fiction, the feed link /feed/?taxonomy=genre&term=fiction.
	foundation := mustInsertPost(t, srv, &store.Post{Type: "book", AuthorID: adminID, Title: "Foundation", Content: "<p>Psychohistory.</p>"})
	if err := srv.store.SetPostTerms(ctx, foundation.ID, "genre", []int64{fiction.ID}); err != nil {
		t.Fatal(err)
	}
	if err := srv.settings.Set(ctx, map[string]any{"posts_per_page": 1}); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	_, page1, _ := get(t, anon, termURL)
	page2URL := srv.links(ctx).Paged(termURL, 2)
	if !strings.Contains(page2URL, "/page/2/") {
		t.Fatalf("paged term URL = %s", page2URL)
	}
	code, page2, _ := get(t, anon, page2URL)
	if code != http.StatusOK {
		t.Errorf("%s: %d", page2URL, code)
	}
	for _, title := range []string{"Dune", "Foundation"} {
		if strings.Contains(page1, ">"+title+"<") == strings.Contains(page2, ">"+title+"<") {
			t.Errorf("%q should be on exactly one of the two genre pages", title)
		}
	}
	if code, _, _ := get(t, anon, srv.links(ctx).Paged(termURL, 3)); code != http.StatusNotFound {
		t.Errorf("genre page 3: %d, want 404", code)
	}
	if err := srv.settings.Set(ctx, map[string]any{"posts_per_page": 10}); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	if _, body, _ := get(t, anon, base+"/feed/?taxonomy=genre&term=fiction"); !strings.Contains(body, "<title>Dune</title>") || strings.Contains(body, "Chicken Feed") {
		t.Error("the genre feed should list the genre's books only")
	}
	if code, _, _ := get(t, anon, base+"/?taxonomy=genre&term=nope"); code != http.StatusNotFound {
		t.Errorf("unknown genre term: %d, want 404", code)
	}

	// Non-Latin slugs: the post and the category answer at their (escaped) permalinks.
	ru := mustInsertPost(t, srv, &store.Post{Type: "post", AuthorID: adminID, Title: "Привет мир", Content: "<p>Здравствуйте.</p>"})
	if ru.Slug != "привет-мир" {
		t.Fatalf("cyrillic slug = %q", ru.Slug)
	}
	ruLink := srv.links(ctx).Permalink(ru)
	if !strings.Contains(ruLink, "/%D0%BF%D1%80%D0%B8%D0%B2%D0%B5%D1%82-%D0%BC%D0%B8%D1%80/") {
		t.Errorf("cyrillic permalink = %s", ruLink)
	}
	if code, body, _ := get(t, anon, ruLink); code != http.StatusOK || !strings.Contains(body, "Здравствуйте.") {
		t.Errorf("cyrillic post: %d", code)
	}
	news := &store.Term{Taxonomy: "category", Name: "Новости"}
	if err := srv.store.InsertTerm(ctx, news); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetPostTerms(ctx, ru.ID, "category", []int64{news.ID}); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	if code, body, _ := get(t, anon, srv.links(ctx).TermLink(news)); code != http.StatusOK || !strings.Contains(body, "Привет мир") {
		t.Errorf("cyrillic category: %d", code)
	}

	// Page List: nested pages link to their full path (ancestors walked in memory).
	top := mustInsertPost(t, srv, &store.Post{Type: "page", AuthorID: adminID, Title: "Services"})
	mid := mustInsertPost(t, srv, &store.Post{Type: "page", AuthorID: adminID, Title: "Consulting", ParentID: top.ID})
	mustInsertPost(t, srv, &store.Post{Type: "page", AuthorID: adminID, Title: "Audits", ParentID: mid.ID})
	list := mustInsertPost(t, srv, &store.Post{Type: "page", AuthorID: adminID, Title: "Sitemap list", Content: `<!-- wp:page-list /-->`})
	srv.purgeAll(ctx)
	if _, body, _ := get(t, anon, srv.links(ctx).Permalink(list)); !strings.Contains(body, `href="`+base+`/services/consulting/audits/"`) {
		t.Error("page list misses the nested page's full path")
	}

	// Twenty Twenty-Five's blog heading prints the posts page title once escaped.
	if srv.activeTheme().Slug != "twentytwentyfive" {
		t.Fatalf("active theme %q, the blog heading check needs Twenty Twenty-Five", srv.activeTheme().Slug)
	}
	blog := mustInsertPost(t, srv, &store.Post{Type: "page", AuthorID: adminID, Title: "Ben's Blog & News"})
	front := mustInsertPost(t, srv, &store.Post{Type: "page", AuthorID: adminID, Title: "Welcome"})
	if err := srv.settings.Set(ctx, map[string]any{"show_on_front": "page", "page_on_front": front.ID, "page_for_posts": blog.ID}); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	code, body, _ = get(t, anon, srv.links(ctx).Permalink(blog))
	if code != http.StatusOK || strings.Contains(body, "&amp;#8217;") || strings.Contains(body, "&amp;amp;") ||
		!strings.Contains(body, ">Ben&#8217;s Blog &amp; News</h1>") {
		t.Errorf("blog heading double-escaped or missing: %d", code)
	}
}

// TestWXRTermSlugsAndFileName: importing a WXR whose non-ASCII slugs are %xx octets reuses the
// existing terms, and the export file name stays ASCII for a non-Latin site name.
func TestWXRTermSlugsAndFileName(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	adminID := installFrontSite(t, base, c, srv)
	tag := &store.Term{Taxonomy: "post_tag", Name: "мир"}
	if err := srv.store.InsertTerm(ctx, tag); err != nil {
		t.Fatal(err)
	}
	cat := &store.Term{Taxonomy: "category", Name: "Новости"}
	if err := srv.store.InsertTerm(ctx, cat); err != nil {
		t.Fatal(err)
	}
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:wp="http://wordpress.org/export/1.2/" xmlns:dc="http://purl.org/dc/elements/1.1/">
<channel>
	<wp:category><wp:term_id>7</wp:term_id><wp:category_nicename>` + wxrSlug(cat.Slug) + `</wp:category_nicename><wp:category_parent></wp:category_parent><wp:cat_name>Новости</wp:cat_name></wp:category>
	<wp:tag><wp:term_id>8</wp:term_id><wp:tag_slug>` + wxrSlug(tag.Slug) + `</wp:tag_slug><wp:tag_name>мир</wp:tag_name></wp:tag>
	<item>
		<title>Imported news</title>
		<dc:creator>admin</dc:creator>
		<content:encoded><![CDATA[<p>Hello.</p>]]></content:encoded>
		<wp:post_id>501</wp:post_id>
		<wp:post_name>imported-news</wp:post_name>
		<wp:status>publish</wp:status>
		<wp:post_type>post</wp:post_type>
		<category domain="category" nicename="` + wxrSlug(cat.Slug) + `"><![CDATA[Новости]]></category>
		<category domain="post_tag" nicename="` + wxrSlug(tag.Slug) + `"><![CDATA[мир]]></category>
	</item>
</channel>
</rss>`
	if !strings.Contains(doc, "%d0%bc") {
		t.Fatalf("wxrSlug should write %%xx octets: %s", wxrSlug(tag.Slug))
	}
	admin, err := srv.store.UserByID(ctx, adminID)
	if err != nil {
		t.Fatal(err)
	}
	gc, _ := gin.CreateTestContext(httptest.NewRecorder())
	gc.Request = httptest.NewRequest(http.MethodPost, "/wp-admin/import", nil)
	if _, err := srv.importWXR(gc, admin, []byte(doc)); err != nil {
		t.Fatal(err)
	}
	var tags, cats int
	pool := getPool(t)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM terms WHERE taxonomy = 'post_tag'`).Scan(&tags)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM terms WHERE taxonomy = 'category' AND name = 'Новости'`).Scan(&cats)
	if tags != 1 || cats != 1 {
		t.Errorf("re-import duplicated terms: %d tags, %d Новости categories", tags, cats)
	}
	var linked int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM post_terms pt JOIN posts p ON p.id = pt.post_id
		WHERE p.title = 'Imported news' AND pt.term_id = ANY($1)`, []int64{tag.ID, cat.ID}).Scan(&linked)
	if linked != 2 {
		t.Errorf("imported post linked to %d of the existing terms, want 2", linked)
	}

	if err := srv.settings.Set(ctx, map[string]any{"blogname": "Блог"}); err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(base + "/wp-admin/export?_wpnonce=" + exportNonce(t, c, base) + "&download=true&content=all")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	cd := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment; filename=pursae.WordPress.") {
		t.Errorf("Content-Disposition = %q", cd)
	}
}
