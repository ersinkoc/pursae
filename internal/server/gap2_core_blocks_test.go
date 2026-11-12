package server

import (
	"context"
	"io"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// TestBlockThemeBlocksRender covers the server-rendered navigation, post-author-name, post-terms and
// comments blocks inside post content.
func TestBlockThemeBlocksRender(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	var uid int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&uid)
	content := `<!-- wp:navigation {"overlayMenu":"never"} -->
<!-- wp:navigation-link {"label":"Docs","url":"/docs/"} /-->
<!-- wp:navigation-submenu {"label":"More","url":"/more/"} -->
<!-- wp:navigation-link {"label":"Deep","url":"/deep/"} /-->
<!-- /wp:navigation-submenu -->
<!-- /wp:navigation -->
<!-- wp:post-author-name {"isLink":true} /-->
<!-- wp:post-terms {"term":"category"} /-->
<!-- wp:comments -->
<div class="wp-block-comments"><!-- wp:comments-title /-->
<!-- wp:comment-template -->
<!-- wp:comment-author-name /-->
<!-- wp:comment-content /-->
<!-- wp:comment-date /-->
<!-- /wp:comment-template -->
<!-- wp:post-comments-form /--></div>
<!-- /wp:comments -->`
	p := &store.Post{Type: "post", Status: "publish", Title: "Blocks", Slug: "blocks-post", Content: content, AuthorID: uid, CommentStatus: "open"}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	cm := &store.Comment{PostID: p.ID, AuthorName: "Ada", AuthorEmail: "ada@example.com", Content: "Nice <b>one</b>", Status: "approved"}
	if err := srv.store.InsertComment(ctx, cm); err != nil {
		t.Fatal(err)
	}
	var cat int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM terms WHERE taxonomy = 'category' ORDER BY id LIMIT 1`).Scan(&cat)
	if err := srv.store.SetPostTerms(ctx, p.ID, "category", []int64{cat}); err != nil {
		t.Fatal(err)
	}
	link := srv.links(ctx).Permalink(p)
	if strings.HasPrefix(link, "/") {
		link = ts.URL + link
	}
	res, err := c.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	html := string(body)
	for _, want := range []string{
		`<nav class="wp-block-navigation"`,
		`wp-block-navigation-link"><a class="wp-block-navigation-item__content" href="/docs/"`,
		`wp-block-navigation-submenu"><a`,
		`<ul class="wp-block-navigation__submenu-container wp-block-navigation-submenu"><li`,
		`class="wp-block-post-author-name__link"`,
		`class="taxonomy-category wp-block-post-terms"`,
		`One response to`,
		`<ol class="wp-block-comment-template"><li id="comment-`,
		`<div class="wp-block-comment-author-name">Ada</div>`,
		`Nice <b>one</b>`,
		`wp-block-post-comments-form`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
}

// TestSearchStemming: with an English site a search for "running" finds a post that only says "runs",
// and switching to a language without a stemmer clears the stemmed vectors.
func TestSearchStemming(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)
	ctx := context.Background()
	p := &store.Post{Type: "post", Status: "publish", Title: "Daily", Slug: "daily-stem", Content: "<p>She runs every morning.</p>"}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.SyncSearchStem(ctx, "en_US"); err != nil {
		t.Fatal(err)
	}
	found := func() bool {
		posts, _, err := srv.store.QueryPosts(ctx, store.PostQuery{Types: []string{"post"}, Statuses: []string{"publish"}, Search: "running", OrderBy: "relevance", PerPage: 10, Page: 1})
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range posts {
			if q.ID == p.ID {
				return true
			}
		}
		return false
	}
	if !found() {
		t.Fatal("stemmed search did not find runs for running")
	}
	if _, err := srv.store.SyncSearchStem(ctx, "ja"); err != nil {
		t.Fatal(err)
	}
	if found() {
		t.Fatal("simple config still stems")
	}
}
