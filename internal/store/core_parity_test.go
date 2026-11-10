package store

import (
	"context"
	"testing"
	"time"
)

func TestPublishDuePostIDsReturnsPublished(t *testing.T) {
	s, _, u1, _ := wbTestStore(t)
	ctx := context.Background()
	p := &Post{Type: "post", AuthorID: u1, Title: "Due", Status: "draft"}
	if err := s.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE posts SET status = 'future', published_at = now() - interval '1 minute' WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	ids, err := s.PublishDuePostIDs(ctx)
	if err != nil || len(ids) != 1 || ids[0] != p.ID {
		t.Fatalf("PublishDuePostIDs = %v, %v; want [%d]", ids, err, p.ID)
	}
	if ids, _ := s.PublishDuePostIDs(ctx); len(ids) != 0 {
		t.Errorf("second run = %v", ids)
	}
}

func TestRevisionsAreCapped(t *testing.T) {
	s, _, u1, _ := wbTestStore(t)
	ctx := context.Background()
	t.Setenv("WPGO_POST_REVISIONS", "3")
	p := &Post{Type: "post", AuthorID: u1, Title: "v0", Status: "publish"}
	if err := s.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"v1", "v2", "v3", "v4", "v5"} {
		p.Title = title
		if err := s.UpdatePost(ctx, p, u1); err != nil {
			t.Fatal(err)
		}
	}
	count := func() (n int) {
		_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM post_revisions WHERE post_id = $1 AND NOT is_autosave`, p.ID).Scan(&n)
		return
	}
	if n := count(); n != 3 {
		t.Fatalf("revisions = %d, want 3", n)
	}
	var newest string
	_ = s.Pool.QueryRow(ctx, `SELECT title FROM post_revisions WHERE post_id = $1 ORDER BY id DESC LIMIT 1`, p.ID).Scan(&newest)
	if newest != "v5" {
		t.Errorf("newest revision %q, want v5", newest)
	}
	// The maintenance run trims to a smaller number; a negative limit keeps everything.
	if n, err := s.PruneRevisions(ctx, 1); err != nil || n != 2 || count() != 1 {
		t.Errorf("PruneRevisions(1) = %d, %v; left %d", n, err, count())
	}
	t.Setenv("WPGO_POST_REVISIONS", "-1")
	for _, title := range []string{"a", "b", "c"} {
		p.Title = title
		_ = s.UpdatePost(ctx, p, u1)
	}
	if n := count(); n != 4 {
		t.Errorf("unlimited: %d revisions, want 4", n)
	}
}

func TestSearchRelevanceTitleFirst(t *testing.T) {
	s, _, u1, _ := wbTestStore(t)
	ctx := context.Background()
	old := time.Now().Add(-48 * time.Hour)
	body := &Post{Type: "post", AuthorID: u1, Title: "Gardening notes", Content: "<p>a long text about zebra stripes</p>", Status: "publish"}
	title := &Post{Type: "post", AuthorID: u1, Title: "Zebra", Content: "<p>short</p>", Status: "publish", PublishedAt: &old}
	for _, p := range []*Post{body, title} {
		if err := s.InsertPost(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	posts, _, err := s.QueryPosts(ctx, PostQuery{Types: []string{"post"}, Statuses: []string{"publish"}, Search: "zebra", OrderBy: "relevance", PerPage: 10})
	if err != nil || len(posts) != 2 {
		t.Fatalf("search = %d posts, %v", len(posts), err)
	}
	if posts[0].ID != title.ID {
		t.Errorf("title match should come first, got %q", posts[0].Title)
	}
}
