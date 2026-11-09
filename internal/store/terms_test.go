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

// TestUpdateTermParentLoop: a term cannot take its own descendant (or a term of another taxonomy) as
// parent (wp_check_term_hierarchy_for_loops); a loop already stored stays visible in the tree.
func TestUpdateTermParentLoop(t *testing.T) {
	s, _, _, _ := wbTestStore(t)
	ctx := context.Background()
	a := &Term{Taxonomy: "category", Name: "Loop A"}
	if err := s.InsertTerm(ctx, a); err != nil {
		t.Fatal(err)
	}
	b := &Term{Taxonomy: "category", Name: "Loop B", ParentID: a.ID}
	c := &Term{Taxonomy: "category", Name: "Loop C"}
	tag := &Term{Taxonomy: "post_tag", Name: "Loop tag"}
	for _, tm := range []*Term{b, c, tag} {
		if err := s.InsertTerm(ctx, tm); err != nil {
			t.Fatal(err)
		}
	}
	parentOf := func(id int64) int64 {
		t.Helper()
		tm, err := s.GetTerm(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return tm.ParentID
	}
	inTree := func() map[int64]bool {
		t.Helper()
		all, err := s.HierarchicalTerms(ctx, "category", false)
		if err != nil {
			t.Fatal(err)
		}
		got := map[int64]bool{}
		for _, tm := range all {
			if got[tm.ID] {
				t.Errorf("term %d listed twice", tm.ID)
			}
			got[tm.ID] = true
		}
		return got
	}

	// A under its child B would close a loop: A goes back to the top level.
	a.ParentID = b.ID
	if err := s.UpdateTerm(ctx, a); err != nil {
		t.Fatal(err)
	}
	if p := parentOf(a.ID); p != 0 {
		t.Errorf("A.parent = %d, want 0", p)
	}
	if p := parentOf(b.ID); p != a.ID {
		t.Errorf("B.parent = %d, want A", p)
	}
	// A legal move still works; a parent from another taxonomy or a missing one is dropped.
	a.ParentID = c.ID
	if err := s.UpdateTerm(ctx, a); err != nil || parentOf(a.ID) != c.ID {
		t.Errorf("A under C: parent %d, %v", parentOf(a.ID), err)
	}
	for _, bad := range []int64{tag.ID, 99999999} {
		b.ParentID = bad
		if err := s.UpdateTerm(ctx, b); err != nil || parentOf(b.ID) != 0 {
			t.Errorf("B under %d: parent %d, %v", bad, parentOf(b.ID), err)
		}
	}
	if got := inTree(); !got[a.ID] || !got[b.ID] || !got[c.ID] {
		t.Errorf("tree = %v", got)
	}

	// A loop written before the check (raw data) is still listed.
	if _, err := s.Pool.Exec(ctx, `UPDATE terms SET parent_id = CASE id WHEN $1::bigint THEN $2::bigint ELSE $1::bigint END WHERE id IN ($1, $2)`, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if got := inTree(); !got[a.ID] || !got[b.ID] {
		t.Errorf("looped terms hidden: %v", got)
	}
	// And updating a term inside that loop terminates and breaks it.
	a.ParentID = b.ID
	if err := s.UpdateTerm(ctx, a); err != nil || parentOf(a.ID) != 0 {
		t.Errorf("update inside stored loop: parent %d, %v", parentOf(a.ID), err)
	}
}

// TestMenuLocationsSeveral: one menu can be assigned to several theme locations at once.
func TestMenuLocationsSeveral(t *testing.T) {
	s, _, _, _ := wbTestStore(t)
	ctx := context.Background()
	main, err := s.CreateMenu(ctx, "Main")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateMenu(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMenuItem(ctx, &EditItem{MenuID: main, Kind: "custom", Object: "custom", URL: "https://example.test/", Label: "Home", Status: "publish"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMenuLocations(ctx, map[string]int64{"primary": main, "footer": main, "social": other, "mobile": 99999999}); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"primary", "footer"} {
		if items, err := s.MenuAt(ctx, l); err != nil || len(items) != 1 {
			t.Errorf("MenuAt(%s) = %v, %v", l, items, err)
		}
	}
	if items, _ := s.MenuAt(ctx, "mobile"); len(items) != 0 {
		t.Errorf("unknown menu id assigned mobile: %v", items)
	}
	menus, err := s.Menus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, m := range menus {
		got[m.Name] = m.Locations
	}
	if !slices.Equal(got["Main"], []string{"footer", "primary"}) || !slices.Equal(got["Other"], []string{"social"}) {
		t.Errorf("locations = %v", got)
	}
	// Reassigning a location moves it; 0 clears it; deleting a menu frees its locations.
	if err := s.SetMenuLocations(ctx, map[string]int64{"footer": other, "primary": 0}); err != nil {
		t.Fatal(err)
	}
	if items, _ := s.MenuAt(ctx, "primary"); len(items) != 0 {
		t.Errorf("cleared primary: %v", items)
	}
	if err := s.DeleteMenu(ctx, other); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM menu_locations`).Scan(&n)
	if n != 0 {
		t.Errorf("menu_locations left after delete: %d", n)
	}
}
