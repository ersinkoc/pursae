package store

import "testing"

func TestPrefixed(t *testing.T) {
	got := prefixed("c", "id, coalesce(parent_id,0), author_name")
	want := "c.id, coalesce(c.parent_id,0), c.author_name"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
