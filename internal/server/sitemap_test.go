package server

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func TestParseSitemapName(t *testing.T) {
	for _, tc := range []struct {
		in       string
		provider string
		subtype  string
		page     int
		ok       bool
	}{
		{"posts-post-1", "posts", "post", 1, true},
		{"posts-post-12", "posts", "post", 12, true},
		{"posts-my-type-3", "posts", "my-type", 3, true},
		{"posts-page", "posts", "page", 1, true},
		{"taxonomies-product_cat-2", "taxonomies", "product_cat", 2, true},
		{"users-1", "users", "", 1, true},
		{"users-4", "users", "", 4, true},
		{"users", "users", "", 1, true},
		{"posts-post-0", "", "", 0, false},
		{"posts", "", "", 0, false},
		{"users-post-1", "", "", 0, false},
		{"comments-1", "", "", 0, false},
	} {
		p, st, n, ok := parseSitemapName(tc.in)
		if ok != tc.ok || p != tc.provider || st != tc.subtype || n != tc.page {
			t.Errorf("parseSitemapName(%q) = %q %q %d %v, want %q %q %d %v", tc.in, p, st, n, ok, tc.provider, tc.subtype, tc.page, tc.ok)
		}
	}
}

var sitemapLocRe = regexp.MustCompile(`<loc>([^<]+)</loc>`)

func TestCoreSitemapsCPTsTaxonomiesAndPaging(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := srv.store.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	// 2001 more published posts: two pages of posts; a password post never shows.
	exec(`INSERT INTO posts (type, author_id, slug, title, status, published_at)
		SELECT 'post', 1, 'bulk-' || g, 'Bulk ' || g, 'publish', now() FROM generate_series(1, 2001) g`)
	exec(`INSERT INTO posts (type, author_id, slug, title, status, password, published_at) VALUES ('post', 1, 'locked', 'Locked', 'publish', 'secret', now())`)
	exec(`INSERT INTO posts (type, author_id, slug, title, status, published_at) VALUES ('post', 1, 'draft-one', 'Draft', 'draft', now())`)
	var total int
	if err := srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE type = 'post' AND status = 'publish' AND password = ''`).Scan(&total); err != nil {
		t.Fatal(err)
	}

	// A public Pursae Custom Fields post type and taxonomy.
	exec(`INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels)
		VALUES ('smbook', 'Books', false, true, 'smbooks', false, '{"title":true,"editor":true}', '{"name":"Books"}') ON CONFLICT (slug) DO NOTHING`)
	exec(`INSERT INTO taxonomies (slug, name, hierarchical, object_types, rest_base, public, is_builtin, labels)
		VALUES ('smgenre', 'Genres', false, '["smbook"]', 'smgenre', true, false, '{"name":"Genres"}') ON CONFLICT (slug) DO NOTHING`)
	acfgoRegistry.Lock()
	acfgoRegistry.types["smbook"] = &acfgoPostTypeDef{Name: "smbook", Public: true, ShowUI: true}
	acfgoRegistry.taxes["smgenre"] = &acfgoTaxonomyDef{Name: "smgenre", Public: true, ObjectTypes: []string{"smbook"}}
	acfgoRegistry.Unlock()
	t.Cleanup(func() {
		acfgoRegistry.Lock()
		delete(acfgoRegistry.types, "smbook")
		delete(acfgoRegistry.taxes, "smgenre")
		acfgoRegistry.Unlock()
	})
	exec(`INSERT INTO posts (type, author_id, slug, title, status, published_at) VALUES ('smbook', 1, 'dune', 'Dune', 'publish', now())`)
	exec(`INSERT INTO terms (taxonomy, slug, name, post_count) VALUES ('smgenre', 'scifi', 'Sci-fi', 1), ('smgenre', 'empty-genre', 'Empty', 0)`)
	// A product and a product category (listed only while Pursae Commerce is on).
	exec(`INSERT INTO posts (type, author_id, slug, title, status, published_at) VALUES ('product', 1, 'mug', 'Mug', 'publish', now())`)
	exec(`UPDATE terms SET post_count = 1 WHERE taxonomy = 'product_cat' AND slug = 'uncategorized'`)

	index := func() string {
		t.Helper()
		code, body, _ := get(t, c, base+"/wp-sitemap.xml")
		if code != http.StatusOK {
			t.Fatalf("index: %d", code)
		}
		return body
	}
	locs := func(name string) []string {
		t.Helper()
		code, body, _ := get(t, c, base+"/wp-sitemap-"+name+".xml")
		if code != http.StatusOK {
			t.Fatalf("%s: %d", name, code)
		}
		var out []string
		for _, m := range sitemapLocRe.FindAllStringSubmatch(body, -1) {
			out = append(out, m[1])
		}
		return out
	}
	status := func(name string) int {
		code, _, _ := get(t, c, base+"/wp-sitemap-"+name+".xml")
		return code
	}

	idx := index()
	for _, want := range []string{"posts-post-1", "posts-post-2", "posts-page-1", "posts-smbook-1", "taxonomies-category-1", "taxonomies-smgenre-1"} {
		if !strings.Contains(idx, "/wp-sitemap-"+want+".xml") {
			t.Errorf("index lacks %s:\n%s", want, idx)
		}
	}
	for _, unwanted := range []string{"posts-post-3", "posts-product-1", "taxonomies-product_cat-1", "posts-attachment", "taxonomies-post_tag-1", "users-1"} {
		if strings.Contains(idx, "/wp-sitemap-"+unwanted+".xml") {
			t.Errorf("index lists %s", unwanted)
		}
	}
	p1, p2 := locs("posts-post-1"), locs("posts-post-2")
	if len(p1) != sitemapMaxURLs || len(p1)+len(p2) != total {
		t.Errorf("posts pages hold %d + %d URLs, want %d + %d", len(p1), len(p2), sitemapMaxURLs, total-sitemapMaxURLs)
	}
	all := strings.Join(append(p1, p2...), "\n")
	if strings.Contains(all, "locked") || strings.Contains(all, "draft-one") {
		t.Error("a password-protected or draft post is in the posts sitemap")
	}
	if code := status("posts-post-3"); code != http.StatusNotFound {
		t.Errorf("page past the last: %d", code)
	}
	if got := locs("posts-smbook-1"); len(got) != 1 || !strings.Contains(got[0], "dune") {
		t.Errorf("custom post type sitemap: %v", got)
	}
	if got := locs("taxonomies-smgenre-1"); len(got) != 1 || !strings.Contains(got[0], "scifi") {
		t.Errorf("custom taxonomy sitemap (empty terms hidden): %v", got)
	}
	if code := status("posts-product-1"); code != http.StatusNotFound {
		t.Errorf("products sitemap while Pursae Commerce is off: %d", code)
	}
	if code := status("posts-nosuchtype-1"); code != http.StatusNotFound {
		t.Errorf("unknown post type: %d", code)
	}
	if code := status("posts-wb_template-1"); code != http.StatusNotFound {
		t.Errorf("non-public post type: %d", code)
	}

	// Pursae Commerce on: products and their categories join, with pretty product links.
	if err := srv.settings.Set(ctx, map[string]any{gooSetting: true}); err != nil {
		t.Fatal(err)
	}
	idx = index()
	if !strings.Contains(idx, "/wp-sitemap-posts-product-1.xml") || !strings.Contains(idx, "/wp-sitemap-taxonomies-product_cat-1.xml") {
		t.Errorf("index lacks products while Pursae Commerce is on:\n%s", idx)
	}
	if got := locs("posts-product-1"); len(got) != 1 || !strings.Contains(got[0], "/mug") {
		t.Errorf("products sitemap: %v", got)
	}
	if got := locs("taxonomies-product_cat-1"); len(got) != 1 || !strings.Contains(got[0], "uncategorized") {
		t.Errorf("product categories sitemap: %v", got)
	}

	// Discouraging search engines removes every sitemap.
	if err := srv.settings.Set(ctx, map[string]any{"blog_public": false}); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := get(t, c, base+"/wp-sitemap.xml"); code != http.StatusNotFound {
		t.Errorf("index while not public: %d", code)
	}
}
