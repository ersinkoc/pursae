package store

import (
	"context"
	"strconv"
	"testing"
)

// TestTermCountFollowsPublishTransitions: terms.post_count counts published posts, so publishing a
// draft that already has a category, or unpublishing it, recounts without a SetPostTerms call.
func TestTermCountFollowsPublishTransitions(t *testing.T) {
	s, _, u1, _ := wbTestStore(t)
	ctx := context.Background()
	cat := &Term{Taxonomy: "category", Name: "News"}
	if err := s.InsertTerm(ctx, cat); err != nil {
		t.Fatal(err)
	}
	p := &Post{Type: "post", AuthorID: u1, Title: "Draft news", Status: "draft"}
	if err := s.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPostTerms(ctx, p.ID, "category", []int64{cat.ID}); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		t.Helper()
		got, err := s.GetTerm(ctx, cat.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Count
	}
	if n := count(); n != 0 {
		t.Fatalf("draft counted: %d", n)
	}
	for _, step := range []struct {
		status string
		want   int
	}{{"publish", 1}, {"draft", 0}, {"private", 0}, {"publish", 1}, {"pending", 0}} {
		cur, err := s.GetPost(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		cur.Status = step.status
		if err := s.UpdatePost(ctx, cur, u1); err != nil {
			t.Fatal(err)
		}
		if n := count(); n != step.want {
			t.Errorf("after %s: post_count %d, want %d", step.status, n, step.want)
		}
	}
}

// TestPostByOldSlugContainment: the old-slug lookup (run on every 404) still finds renamed posts and
// returns ErrNotFound for unknown slugs.
func TestPostByOldSlugContainment(t *testing.T) {
	s, _, u1, _ := wbTestStore(t)
	ctx := context.Background()
	p := &Post{Type: "post", AuthorID: u1, Title: "First name", Slug: "first-name", Status: "publish"}
	if err := s.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Slug = "second-name"
	if err := s.UpdatePost(ctx, p, u1); err != nil {
		t.Fatal(err)
	}
	got, err := s.PostByOldSlug(ctx, "first-name", 0, 0, 0, nil)
	if err != nil || got.ID != p.ID {
		t.Fatalf("old slug: %v %v", got, err)
	}
	y := p.PublishedAt.UTC().Year()
	if _, err := s.PostByOldSlug(ctx, "first-name", y, 0, 0, nil); err != nil {
		t.Errorf("old slug with the right year: %v", err)
	}
	if _, err := s.PostByOldSlug(ctx, "first-name", y-3, 0, 0, nil); err != ErrNotFound {
		t.Errorf("old slug with the wrong year: %v", err)
	}
	for _, slug := range []string{"backup", "first", `x"]}`} {
		if _, err := s.PostByOldSlug(ctx, slug, 0, 0, 0, nil); err != ErrNotFound {
			t.Errorf("%q: %v, want ErrNotFound", slug, err)
		}
	}
}

// TestNonLatinSlugs: titles in other scripts keep their letters; a title with nothing slug-worthy
// published straight away gets its id, never "0"; terms keep their name.
func TestNonLatinSlugs(t *testing.T) {
	s, _, u1, _ := wbTestStore(t)
	ctx := context.Background()
	p := &Post{Type: "post", AuthorID: u1, Title: "Привет мир", Status: "publish"}
	if err := s.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	if p.Slug != "привет-мир" {
		t.Errorf("cyrillic slug = %q", p.Slug)
	}
	if got, err := s.PostBySlug(ctx, "post", "привет-мир"); err != nil || got.ID != p.ID {
		t.Errorf("PostBySlug: %v", err)
	}
	sym := &Post{Type: "post", AuthorID: u1, Title: "!!!", Status: "publish"}
	if err := s.InsertPost(ctx, sym); err != nil {
		t.Fatal(err)
	}
	if want := strconv.FormatInt(sym.ID, 10); sym.Slug != want {
		t.Errorf("symbol title slug = %q, want %q", sym.Slug, want)
	}
	if got, _ := s.GetPost(ctx, sym.ID); got.Slug != strconv.FormatInt(sym.ID, 10) {
		t.Errorf("stored slug = %q", got.Slug)
	}
	cat := &Term{Taxonomy: "category", Name: "Новости"}
	if err := s.InsertTerm(ctx, cat); err != nil {
		t.Fatal(err)
	}
	if cat.Slug != "новости" {
		t.Errorf("term slug = %q", cat.Slug)
	}
	imported := &Term{Taxonomy: "category", Name: "Imported", Slug: "%d0%bc%d0%b8%d1%80"}
	if err := s.InsertTerm(ctx, imported); err != nil {
		t.Fatal(err)
	}
	if imported.Slug != "мир" {
		t.Errorf("percent-encoded slug = %q", imported.Slug)
	}
}

// TestExcludePassworded: the search filter for logged-out visitors leaves out protected posts.
func TestExcludePassworded(t *testing.T) {
	s, _, u1, _ := wbTestStore(t)
	ctx := context.Background()
	for _, p := range []*Post{
		{Type: "post", AuthorID: u1, Title: "Open", Content: "<p>zanzibar open</p>", Status: "publish"},
		{Type: "post", AuthorID: u1, Title: "Locked", Content: "<p>zanzibar merger</p>", Status: "publish", Password: "members"},
	} {
		if err := s.InsertPost(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	q := PostQuery{Types: []string{"post"}, Statuses: []string{"publish"}, Search: "zanzibar", PerPage: 10}
	all, _, _ := s.QueryPosts(ctx, q)
	q.ExcludePassworded = true
	open, _, _ := s.QueryPosts(ctx, q)
	if len(all) != 2 || len(open) != 1 || open[0].Title != "Open" {
		t.Errorf("all %d, without protected %d", len(all), len(open))
	}
}

// TestQueryPostsNoContent: NoContent lists posts without their bodies but keeps titles and slugs.
func TestQueryPostsNoContent(t *testing.T) {
	s, _, u1, _ := wbTestStore(t)
	ctx := context.Background()
	p := &Post{Type: "page", AuthorID: u1, Title: "Light", Content: "<p>long body</p>", Excerpt: "short", Status: "publish", MenuOrder: 3}
	if err := s.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, n, err := s.QueryPosts(ctx, PostQuery{Types: []string{"page"}, Statuses: []string{"publish"}, PerPage: -1, NoContent: true})
	if err != nil || n != 1 || len(got) != 1 {
		t.Fatalf("query: %v, %d", err, n)
	}
	if g := got[0]; g.ID != p.ID || g.Title != "Light" || g.Slug != p.Slug || g.MenuOrder != 3 || g.Content != "" || g.Excerpt != "" {
		t.Errorf("light post = %+v", g)
	}
}
