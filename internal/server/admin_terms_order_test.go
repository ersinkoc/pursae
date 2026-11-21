package server

import (
	"slices"
	"testing"

	"wpgo/internal/store"
)

func TestSpliceTermOrder(t *testing.T) {
	all := []*store.Term{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}}
	// the page showed 2,3,4 and the user moved 4 to the top: 1 and 5 keep their places
	got, err := spliceTermOrder(all, []int64{4, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{1, 4, 2, 3, 5}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, err := spliceTermOrder(all, []int64{2, 9}); err == nil {
		t.Fatal("an unknown id is refused")
	}
	if _, err := spliceTermOrder(all, []int64{2, 2}); err == nil {
		t.Fatal("a repeated id is refused")
	}
}
