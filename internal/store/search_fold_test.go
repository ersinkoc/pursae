package store

import (
	"context"
	"slices"
	"testing"
)

// TestSearchDottedDotlessI: Turkish words are found whatever i the title or the term is written with,
// through the title, the 'simple' vector and the stemmed one; English upper case keeps working.
func TestSearchDottedDotlessI(t *testing.T) {
	s, _, u1, _ := wbTestStore(t)
	ctx := context.Background()
	if _, err := s.SyncSearchStem(ctx, "tr_TR"); err != nil {
		t.Fatal(err)
	}
	lower := &Post{Type: "post", AuthorID: u1, Title: "ışık", Status: "publish"}
	upper := &Post{Type: "post", AuthorID: u1, Title: "Işık Testi", Status: "publish"}
	body := &Post{Type: "post", AuthorID: u1, Title: "Gece", Content: "<p>Sokakta IŞIK yok.</p>", Status: "publish"}
	city := &Post{Type: "post", AuthorID: u1, Title: "İstanbul", Status: "publish"}
	info := &Post{Type: "post", AuthorID: u1, Title: "Info", Status: "publish"}
	for _, p := range []*Post{lower, upper, body, city, info} {
		if err := s.InsertPost(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	find := func(term string) []int64 {
		t.Helper()
		posts, _, err := s.QueryPosts(ctx, PostQuery{Types: []string{"post"}, Statuses: []string{"publish"}, Search: term, OrderBy: "relevance", PerPage: 20})
		if err != nil {
			t.Fatalf("search %q: %v", term, err)
		}
		var ids []int64
		for _, p := range posts {
			ids = append(ids, p.ID)
		}
		slices.Sort(ids)
		return ids
	}
	want := []int64{lower.ID, upper.ID, body.ID}
	slices.Sort(want)
	for _, term := range []string{"IŞIK", "ışık", "Işık", "işik"} {
		if got := find(term); !slices.Equal(got, want) {
			t.Errorf("search %q = %v, want %v", term, got, want)
		}
	}
	for _, term := range []string{"istanbul", "İSTANBUL", "ISTANBUL"} {
		if got := find(term); !slices.Equal(got, []int64{city.ID}) {
			t.Errorf("search %q = %v, want [%d]", term, got, city.ID)
		}
	}
	if got := find("INFO"); !slices.Equal(got, []int64{info.ID}) {
		t.Errorf("search INFO = %v, want [%d]", got, info.ID)
	}
}
