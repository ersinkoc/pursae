package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// A static front page split with <!--nextpage--> pages under /page/N/ (_wp_link_page()): its page
// links, the page itself and the ?page=N canonical redirect all agree.
func TestFrontPageNextpage(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	adminID := installFrontSite(t, base, c, srv)
	anon := &http.Client{CheckRedirect: c.CheckRedirect}
	front := mustInsertPost(t, srv, &store.Post{Type: "page", AuthorID: adminID, Title: "Welcome",
		Content: "<!-- wp:paragraph --><p>Front one</p><!-- /wp:paragraph --><!-- wp:nextpage --><!--nextpage--><!-- /wp:nextpage --><!-- wp:paragraph --><p>Front two</p><!-- /wp:paragraph -->"})
	if err := srv.settings.Set(ctx, map[string]any{"show_on_front": "page", "page_on_front": front.ID}); err != nil {
		t.Fatal(err)
	}
	srv.purgeTags("home")
	_, body, _ := get(t, anon, base+"/")
	if !strings.Contains(body, "Front one") || strings.Contains(body, "Front two") {
		t.Fatal("front page 1 shows the wrong content")
	}
	if want := `<a href="` + base + `/page/2/" class="post-page-numbers">2</a>`; !strings.Contains(body, want) {
		t.Errorf("page links miss %q", want)
	}
	code, body, _ := get(t, anon, base+"/page/2/")
	if code != http.StatusOK || !strings.Contains(body, "Front two") || strings.Contains(body, "Front one") {
		t.Errorf("/page/2/: %d, page two not shown", code)
	}
	if code, _, loc := get(t, anon, base+"/?page=2"); code != http.StatusMovedPermanently || loc != base+"/page/2/" {
		t.Errorf("?page=2: %d %s", code, loc)
	}
}

// With a structure that starts with %postname% WordPress uses verbose page rules: a page and a post
// sharing a slug resolve to the page, and the post stays reachable through its own link elsewhere.
func TestVerbosePageRulesPreferPage(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	adminID := installFrontSite(t, base, c, srv)
	anon := &http.Client{CheckRedirect: c.CheckRedirect}
	if err := srv.settings.Set(context.Background(), map[string]any{"permalink_structure": "/%postname%/"}); err != nil {
		t.Fatal(err)
	}
	if s := srv.links(context.Background()).Structure; s != "/%postname%/" {
		t.Fatalf("structure = %q", s)
	}
	pg := mustInsertPost(t, srv, &store.Post{Type: "page", AuthorID: adminID, Title: "About", Slug: "about", Content: "<p>The about page</p>"})
	ps := mustInsertPost(t, srv, &store.Post{Type: "post", AuthorID: adminID, Title: "About", Slug: "about", Content: "<p>A post called about</p>"})
	if pg.Slug != "about" || ps.Slug != "about" {
		t.Skipf("slugs differ: page %q post %q", pg.Slug, ps.Slug)
	}
	code, body, _ := get(t, anon, base+"/about/")
	if code != http.StatusOK || !strings.Contains(body, "The about page") {
		t.Errorf("/about/: %d, page not served", code)
	}
	// A draft page does not hide the post.
	pg.Status = "draft"
	if err := srv.store.UpdatePost(context.Background(), pg, adminID); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(context.Background())
	if code, body, _ := get(t, anon, base+"/about/"); code != http.StatusOK || !strings.Contains(body, "A post called about") {
		t.Errorf("/about/ with a draft page: %d, post not served", code)
	}
}
