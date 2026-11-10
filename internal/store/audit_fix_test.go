package store

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// TestHierarchicalTermsKeepsNonEmptyBranch: with hideEmpty an empty parent whose child has posts
// stays in the list (get_terms( hierarchical, hide_empty )), and fully empty branches go.
func TestHierarchicalTermsKeepsNonEmptyBranch(t *testing.T) {
	s, _, u1, _ := wbTestStore(t)
	ctx := context.Background()
	parent := &Term{Taxonomy: "category", Name: "Clothing"}
	if err := s.InsertTerm(ctx, parent); err != nil {
		t.Fatal(err)
	}
	child := &Term{Taxonomy: "category", Name: "T-Shirts", ParentID: parent.ID}
	if err := s.InsertTerm(ctx, child); err != nil {
		t.Fatal(err)
	}
	empty := &Term{Taxonomy: "category", Name: "Nothing here"}
	if err := s.InsertTerm(ctx, empty); err != nil {
		t.Fatal(err)
	}
	p := &Post{Type: "post", AuthorID: u1, Title: "Shirt", Status: "publish"}
	if err := s.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPostTerms(ctx, p.ID, "category", []int64{child.ID}); err != nil {
		t.Fatal(err)
	}
	all, err := s.HierarchicalTerms(ctx, "category", true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]*Term{}
	for _, t := range all {
		got[t.ID] = t
	}
	if got[parent.ID] == nil || got[child.ID] == nil {
		t.Fatalf("branch dropped: %+v", all)
	}
	if got[empty.ID] != nil {
		t.Error("empty term kept")
	}
	if got[parent.ID].TreeCount != 1 || got[parent.ID].Count != 0 || got[child.ID].Depth != 1 {
		t.Errorf("parent %+v child %+v", got[parent.ID], got[child.ID])
	}
}

// TestConcurrentSlugsStayUnique: parallel inserts of one title get distinct slugs, and parallel
// inserts of one new tag create it once.
func TestConcurrentSlugsStayUnique(t *testing.T) {
	s, _, u1, _ := wbTestStore(t)
	ctx := context.Background()
	const n = 8
	var wg sync.WaitGroup
	posts := make([]*Post, n)
	terms := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			posts[i] = &Post{Type: "post", AuthorID: u1, Title: "Hello", Status: "publish"}
			if err := s.InsertPost(ctx, posts[i]); err != nil {
				t.Error(err)
			}
			terms[i] = s.InsertTerm(ctx, &Term{Taxonomy: "post_tag", Name: "golang"})
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, p := range posts {
		if seen[p.Slug] {
			t.Errorf("slug %q used twice", p.Slug)
		}
		seen[p.Slug] = true
	}
	created := 0
	for _, err := range terms {
		switch {
		case err == nil:
			created++
		case !errors.Is(err, ErrTermExists):
			t.Error(err)
		}
	}
	if created != 1 {
		t.Errorf("tag created %d times", created)
	}
}

// TestUpdateWithEmptySanitizedSlug: a slug of punctuation only falls back to the title, then the id.
func TestUpdateWithEmptySanitizedSlug(t *testing.T) {
	s, _, u1, _ := wbTestStore(t)
	ctx := context.Background()
	p := &Post{Type: "post", AuthorID: u1, Title: "Kept title", Status: "publish"}
	if err := s.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Slug = "???"
	if err := s.UpdatePost(ctx, p, u1); err != nil {
		t.Fatal(err)
	}
	if p.Slug != "kept-title" {
		t.Errorf("slug %q, want kept-title", p.Slug)
	}
	p.Slug, p.Title = "!!!", "!!!"
	if err := s.UpdatePost(ctx, p, u1); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetPost(ctx, p.ID); got == nil || got.Slug == "" {
		t.Errorf("stored empty slug: %+v", got)
	}
}

// TestDeletePostReparentsChildren: deleting a page moves its child pages to the page's parent.
func TestDeletePostReparentsChildren(t *testing.T) {
	s, _, u1, _ := wbTestStore(t)
	ctx := context.Background()
	top := &Post{Type: "page", AuthorID: u1, Title: "Services", Status: "publish"}
	if err := s.InsertPost(ctx, top); err != nil {
		t.Fatal(err)
	}
	mid := &Post{Type: "page", AuthorID: u1, Title: "Web", Status: "publish", ParentID: top.ID}
	if err := s.InsertPost(ctx, mid); err != nil {
		t.Fatal(err)
	}
	leaf := &Post{Type: "page", AuthorID: u1, Title: "SEO", Status: "publish", ParentID: mid.ID}
	if err := s.InsertPost(ctx, leaf); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePost(ctx, mid.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetPost(ctx, leaf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ParentID != top.ID {
		t.Errorf("parent %d, want %d", got.ParentID, top.ID)
	}
}

func insertComment(t *testing.T, s *Store, c *Comment) *Comment {
	t.Helper()
	if c.Type == "" {
		c.Type = "comment"
	}
	if err := s.InsertComment(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c
}

// TestCommentModerationChecks: comment_previously_approved needs a name and an e-mail, the
// duplicate test looks at the parent and the author, and trashing twice keeps Restore working.
func TestCommentModerationChecks(t *testing.T) {
	s, page, _, _ := wbTestStore(t)
	ctx := context.Background()
	anon := insertComment(t, s, &Comment{PostID: page, Content: "first", Status: "approved"})
	if ok, err := s.HasApprovedComment(ctx, "", ""); err != nil || ok {
		t.Errorf("anonymous counts as approved: %v %v", ok, err)
	}
	insertComment(t, s, &Comment{PostID: page, AuthorName: "Ann", AuthorEmail: "ann@example.test", Content: "hi", Status: "approved"})
	if ok, _ := s.HasApprovedComment(ctx, "Ann", "ann@example.test"); !ok {
		t.Error("approved author not recognised")
	}
	// A registered user's e-mail is matched on the user's own comments, not a guest's.
	if ok, _ := s.HasApprovedComment(ctx, "Ann", "one@example.test"); ok {
		t.Error("guest name and a user's e-mail counted as approved")
	}

	dup := func(c *Comment) bool {
		t.Helper()
		ok, err := s.IsDuplicateComment(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	insertComment(t, s, &Comment{PostID: page, AuthorName: "A", Content: "Thanks!", Status: "approved"})
	if dup(&Comment{PostID: page, AuthorName: "B", Content: "Thanks!"}) {
		t.Error("another anonymous visitor flagged as duplicate")
	}
	if !dup(&Comment{PostID: page, AuthorName: "A", Content: "Thanks!"}) {
		t.Error("same author and text not flagged")
	}
	if dup(&Comment{PostID: page, ParentID: anon.ID, AuthorName: "A", Content: "Thanks!"}) {
		t.Error("reply to another comment flagged as duplicate")
	}

	c := insertComment(t, s, &Comment{PostID: page, AuthorName: "C", Content: "trash me", Status: "approved"})
	for range 2 {
		if err := s.SetCommentStatus(ctx, c.ID, "trash"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetCommentStatus(ctx, c.ID, "restore"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetComment(ctx, c.ID); got == nil || got.Status != "approved" {
		t.Errorf("restored to %+v, want approved", got)
	}
}

// TestUsersUniqueIgnoringCase: a login or e-mail differing only by case is rejected by the database.
func TestUsersUniqueIgnoringCase(t *testing.T) {
	s, _, _, _ := wbTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateUser(ctx, NewUser{Login: "WB-ONE", Email: "other@example.test", Role: "subscriber"}); !errors.Is(err, ErrLoginTaken) {
		t.Errorf("login: %v", err)
	}
	if _, err := s.CreateUser(ctx, NewUser{Login: "wb-three", Email: "ONE@example.test", Role: "subscriber"}); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("email: %v", err)
	}
}

// TestSearchEscapesLike: % and _ in a search term match literally.
func TestSearchEscapesLike(t *testing.T) {
	s, _, u1, _ := wbTestStore(t)
	ctx := context.Background()
	for _, title := range []string{"100% sure", "Plain title", "Other"} {
		if err := s.InsertPost(ctx, &Post{Type: "post", AuthorID: u1, Title: title, Status: "publish"}); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err := s.QueryPosts(ctx, PostQuery{Types: []string{"post"}, Statuses: []string{"publish"}, Search: "%"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "100% sure" {
		var titles []string
		for _, p := range got {
			titles = append(titles, p.Title)
		}
		t.Errorf("search %%: %v", titles)
	}
	for _, name := range []string{"50% off", "50 percent"} {
		if err := s.InsertTerm(ctx, &Term{Taxonomy: "post_tag", Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	terms, _, err := s.QueryTerms(ctx, TermQuery{Taxonomy: "post_tag", Search: "50%"})
	if err != nil {
		t.Fatal(err)
	}
	if len(terms) != 1 || terms[0].Name != "50% off" {
		t.Errorf("term search 50%%: %d results", len(terms))
	}
}
