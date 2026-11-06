package store

import (
	"context"
	"slices"
	"testing"
)

// TestTermsForPostsAllTaxonomies: an empty taxonomy returns the terms of every taxonomy (WPGo
// Cache tags a single page with all of them), a named one only its own.
func TestTermsForPostsAllTaxonomies(t *testing.T) {
	s, _, u1, _ := wbTestStore(t)
	ctx := context.Background()
	p := &Post{Type: "post", AuthorID: u1, Title: "Tagged", Status: "publish"}
	if err := s.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	cat := &Term{Taxonomy: "category", Name: "News"}
	tag := &Term{Taxonomy: "post_tag", Name: "Go"}
	for _, tm := range []*Term{cat, tag} {
		if err := s.InsertTerm(ctx, tm); err != nil {
			t.Fatal(err)
		}
		if err := s.SetPostTerms(ctx, p.ID, tm.Taxonomy, []int64{tm.ID}); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(tax string) []int64 {
		t.Helper()
		m, err := s.TermsForPosts(ctx, []int64{p.ID}, tax)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, tm := range m[p.ID] {
			out = append(out, tm.ID)
		}
		slices.Sort(out)
		return out
	}
	all := []int64{cat.ID, tag.ID}
	slices.Sort(all)
	if got := ids(""); !slices.Equal(got, all) {
		t.Errorf("all taxonomies = %v, want %v", got, all)
	}
	if got := ids("category"); !slices.Equal(got, []int64{cat.ID}) {
		t.Errorf("category = %v", got)
	}
}
