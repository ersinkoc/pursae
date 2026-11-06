package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/cache"
	"wpgo/internal/store"
)

// TestSiteViewableType covers which post types the front end routes (no database needed).
func TestSiteViewableType(t *testing.T) {
	acfgoRegistry.Lock()
	saved := acfgoRegistry.types
	acfgoRegistry.types = map[string]*acfgoPostTypeDef{
		"book":        {Name: "book", Public: true, Slug: "books", HasArchive: true, ArchiveSlug: "library"},
		"note":        {Name: "note", Public: true, Slug: "note"},
		"secret":      {Name: "secret", Public: false, Slug: "secret", HasArchive: true},
		"wb_template": {Name: "wb_template", Public: true, Slug: "wb_template", HasArchive: true},
		"product":     {Name: "product", Public: true, Slug: "product", HasArchive: true},
	}
	acfgoRegistry.Unlock()
	t.Cleanup(func() {
		acfgoRegistry.Lock()
		acfgoRegistry.types = saved
		acfgoRegistry.Unlock()
	})
	for typ, want := range map[string]bool{"post": true, "page": true, "book": true, "note": true, "secret": false,
		"wb_template": false, "product": false, "attachment": false, "nope": false} {
		if got := siteViewableType(typ); got != want {
			t.Errorf("siteViewableType(%q) = %v, want %v", typ, got, want)
		}
	}
	if base, archive := siteTypeBase("book"); base != "books" || archive != "library" {
		t.Errorf("book slugs = %q %q", base, archive)
	}
	if base, archive := siteTypeBase("note"); base != "note" || archive != "" {
		t.Errorf("note slugs = %q %q", base, archive)
	}
	if base, archive := siteTypeBase("wb_template"); base != "" || archive != "" {
		t.Errorf("wb_template routed: %q %q", base, archive)
	}
	if siteArchiveType("note") != nil || siteArchiveType("book") == nil || siteArchiveType("secret") != nil {
		t.Error("siteArchiveType")
	}
	if got := len(siteRoutedTypes()); got != 2 {
		t.Errorf("routed types = %d, want 2", got)
	}
	q := &siteQuery{Kind: "post-type-archive", PostType: "book"}
	if full, plain := q.archiveTitle(); full != "Archives: <span>book</span>" || plain != "book" {
		t.Errorf("archive title without labels = %q %q", full, plain)
	}
}

// TestFrontCustomPostTypes covers the front end of public Pursae Custom Fields post types: pretty
// singles (flat and hierarchical), the post type archive with pagination and feed, the canonical
// redirect of ?p=ID without a loop, plain permalinks and the types that are never routed.
func TestFrontCustomPostTypes(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	adminID := installFrontSite(t, base, c, srv)
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	acfgoActivate(t, c, base)
	if err := srv.settings.Set(ctx, map[string]any{"permalink_structure": "/%postname%/", "posts_per_page": 2}); err != nil {
		t.Fatal(err)
	}
	for _, d := range []map[string]any{
		{"title": "Books", "post_type": "book", "public": true, "has_archive": true, "has_archive_slug": "library",
			"rewrite": map[string]any{"slug": "books"}, "labels": map[string]any{"name": "Books", "singular_name": "Book"}},
		{"title": "Chapters", "post_type": "chapter", "public": true, "hierarchical": true,
			"labels": map[string]any{"name": "Chapters", "singular_name": "Chapter"}},
		{"title": "Secrets", "post_type": "secret", "public": false, "has_archive": true,
			"labels": map[string]any{"name": "Secrets", "singular_name": "Secret"}},
	} {
		if err := srv.acfgoSaveObject(ctx, acfgoPostType, d); err != nil {
			t.Fatal(err)
		}
	}

	at := func(day int) *time.Time { d := time.Date(2026, 10, day, 12, 0, 0, 0, time.UTC); return &d }
	dune := mustInsertPost(t, srv, &store.Post{Type: "book", Title: "Dune", Slug: "dune", Content: "<p>Spice.</p>", AuthorID: adminID, PublishedAt: at(3)})
	mustInsertPost(t, srv, &store.Post{Type: "book", Title: "Emma", Slug: "emma", Content: "<p>Highbury.</p>", AuthorID: adminID, PublishedAt: at(2)})
	mustInsertPost(t, srv, &store.Post{Type: "book", Title: "Ulysses", Slug: "ulysses", Content: "<p>Dublin.</p>", AuthorID: adminID, PublishedAt: at(1)})
	draft := mustInsertPost(t, srv, &store.Post{Type: "book", Title: "Unfinished", Slug: "unfinished", Status: "draft", AuthorID: adminID, PublishedAt: at(4)})
	part := mustInsertPost(t, srv, &store.Post{Type: "chapter", Title: "Part One", Slug: "part-one", AuthorID: adminID, PublishedAt: at(1)})
	intro := mustInsertPost(t, srv, &store.Post{Type: "chapter", Title: "Introduction", Slug: "intro", ParentID: part.ID, AuthorID: adminID, PublishedAt: at(1)})
	secret := mustInsertPost(t, srv, &store.Post{Type: "secret", Title: "Hidden", Slug: "hidden", AuthorID: adminID, PublishedAt: at(1)})
	// wb_template is registered by its own migration; make sure the row exists for this test.
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels)
		VALUES ('wb_template', 'Templates', false, false, 'wb_template', false, '{}', '{}') ON CONFLICT (slug) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	tpl := mustInsertPost(t, srv, &store.Post{Type: "wb_template", Title: "Header", Slug: "header", AuthorID: adminID, PublishedAt: at(1)})
	srv.purgeTags("archive:book", "archive:chapter")

	lb := srv.links(ctx)
	if got := lb.Permalink(dune); got != base+"/books/dune/" {
		t.Fatalf("book permalink = %s", got)
	}
	if got := lb.Permalink(intro); got != base+"/chapter/part-one/intro/" {
		t.Errorf("chapter permalink = %s", got)
	}
	if got := lb.PostTypeArchiveLink("book"); got != base+"/library/" {
		t.Errorf("book archive link = %s", got)
	}
	if got := lb.PostTypeArchiveLink("chapter"); got != "" {
		t.Errorf("chapter has no archive, link = %s", got)
	}

	// Single entry.
	code, body, _ := get(t, anon, base+"/books/dune/")
	if code != http.StatusOK || !strings.Contains(body, "Spice.") {
		t.Fatalf("/books/dune/: %d", code)
	}
	for _, want := range []string{"single-book", "book-template-default", "postid-" + itoa64(dune.ID), "<title>Dune &#8211; Front Site</title>"} {
		if !strings.Contains(body, want) {
			t.Errorf("book single misses %q", want)
		}
	}
	if strings.Contains(body, "single-post ") {
		t.Error("book single carries single-post")
	}

	// ?p=ID goes to the pretty permalink once; the pretty permalink answers 200 (no loop).
	for _, u := range []string{"/?p=" + itoa64(dune.ID), "/?p=" + itoa64(dune.ID) + "&post_type=book", "/books/dune", "/books/dune/?post_type=book"} {
		code, _, loc := get(t, anon, base+u)
		if code != http.StatusMovedPermanently || loc != base+"/books/dune/" {
			t.Errorf("%s: %d %q, want 301 to /books/dune/", u, code, loc)
		}
	}
	if code, _, loc := get(t, anon, base+"/books/dune/?utm_source=x"); code != http.StatusOK {
		t.Errorf("/books/dune/?utm_source=x: %d %s", code, loc)
	}

	// Hierarchical entries live under their ancestors' path.
	if code, body, _ := get(t, anon, base+"/chapter/part-one/intro/"); code != http.StatusOK || !strings.Contains(body, "single-chapter") {
		t.Errorf("/chapter/part-one/intro/: %d", code)
	}
	if code, _, _ := get(t, anon, base+"/chapter/part-one/"); code != http.StatusOK {
		t.Errorf("/chapter/part-one/: %d", code)
	}
	if code, _, _ := get(t, anon, base+"/chapter/intro/"); code != http.StatusNotFound {
		t.Errorf("/chapter/intro/ (child without parent): %d", code)
	}
	if code, _, loc := get(t, anon, base+"/?p="+itoa64(intro.ID)); code != http.StatusMovedPermanently || loc != base+"/chapter/part-one/intro/" {
		t.Errorf("?p=intro: %d %s", code, loc)
	}
	if code, _, _ := get(t, anon, base+"/chapter/"); code != http.StatusNotFound {
		t.Errorf("/chapter/ without an archive: %d", code)
	}

	// Archive with pagination.
	code, body, _ = get(t, anon, base+"/library/")
	if code != http.StatusOK {
		t.Fatalf("/library/: %d", code)
	}
	for _, want := range []string{"Archives: <span>Books</span>", "post-type-archive", "post-type-archive-book", "<title>Books &#8211; Front Site</title>",
		"Dune", "Emma", `href="` + base + `/library/page/2/"`} {
		if !strings.Contains(body, want) {
			t.Errorf("archive misses %q", want)
		}
	}
	if strings.Contains(body, "Ulysses") || strings.Contains(body, "Hello world!") || strings.Contains(body, "Unfinished") {
		t.Error("archive page 1 lists the wrong entries")
	}
	code, body, _ = get(t, anon, base+"/library/page/2/")
	if code != http.StatusOK || !strings.Contains(body, "Ulysses") || strings.Contains(body, ">Dune<") || !strings.Contains(body, "paged-2") {
		t.Errorf("/library/page/2/: %d", code)
	}
	if !strings.Contains(body, "<title>Books &#8211; Page 2 &#8211; Front Site</title>") {
		t.Error("paged archive title")
	}
	for u, want := range map[string]string{
		"/?post_type=book":         "/library/",
		"/?post_type=book&paged=2": "/library/page/2/",
		"/library/?paged=2":        "/library/page/2/",
		"/library":                 "/library/",
	} {
		if code, _, loc := get(t, anon, base+u); code != http.StatusMovedPermanently || loc != base+want {
			t.Errorf("%s: %d %q, want 301 to %s", u, code, loc, want)
		}
	}
	code, body, _ = get(t, anon, base+"/library/feed/")
	if code != http.StatusOK || !strings.Contains(body, "Dune") || strings.Contains(body, "Hello world!") {
		t.Errorf("/library/feed/: %d", code)
	}

	// Cache tags of the archive.
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	gc, _ := gin.CreateTestContext(w)
	gc.Request = httptest.NewRequest(http.MethodGet, "/library/", nil)
	srv.cacheTags(gc, &siteQuery{Kind: "post-type-archive", PostType: "book"})
	if tags := w.Header().Get(cache.HeaderTags); !strings.Contains(","+tags+",", ",archive:book,") {
		t.Errorf("archive cache tags = %q", tags)
	}

	// Never routed: drafts (without preview), non-public types, Web Builder templates, unknown slugs.
	for _, u := range []string{"/books/unfinished/", "/?p=" + itoa64(secret.ID), "/secret/hidden/", "/?p=" + itoa64(tpl.ID),
		"/wb_template/header/", "/books/nope/", "/?post_type=secret", "/?post_type=wb_template"} {
		code, _, loc := get(t, anon, base+u)
		if strings.HasPrefix(u, "/?post_type=") {
			// An unknown post_type query var is not an archive: the home page, without a redirect.
			if code != http.StatusOK || strings.Contains(loc, "secret") {
				t.Errorf("%s: %d %s", u, code, loc)
			}
			continue
		}
		if code != http.StatusNotFound {
			t.Errorf("%s: %d %s, want 404", u, code, loc)
		}
	}
	// The author previews the draft through ?p=&preview=true.
	if code, body, _ := get(t, c, base+"/?p="+itoa64(draft.ID)+"&preview=true"); code != http.StatusOK || !strings.Contains(body, "Unfinished") {
		t.Errorf("draft preview: %d", code)
	}

	// Plain permalinks keep ?p=ID and ?post_type=, served without redirects.
	if err := srv.settings.Set(ctx, map[string]any{"permalink_structure": ""}); err != nil {
		t.Fatal(err)
	}
	srv.purgeTags("home", "archive:book", "post:"+itoa64(dune.ID))
	lb = srv.links(ctx)
	if got := lb.Permalink(dune); got != base+"/?p="+itoa64(dune.ID) {
		t.Errorf("plain book permalink = %s", got)
	}
	if code, body, _ := get(t, anon, base+"/?p="+itoa64(dune.ID)); code != http.StatusOK || !strings.Contains(body, "Spice.") {
		t.Errorf("plain ?p=: %d", code)
	}
	code, body, _ = get(t, anon, base+"/?post_type=book")
	if code != http.StatusOK || !strings.Contains(body, "post-type-archive-book") || !strings.Contains(body, `href="`+base+`/?paged=2&amp;post_type=book"`) {
		t.Errorf("plain archive: %d", code)
	}
	if code, body, _ := get(t, anon, base+"/?post_type=book&paged=2"); code != http.StatusOK || !strings.Contains(body, "Ulysses") {
		t.Errorf("plain archive page 2: %d", code)
	}
}
