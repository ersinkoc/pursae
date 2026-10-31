package server

import (
	"context"
	"testing"
)

func TestHeadlessGoHistoryRelationLosersAndDeletes(t *testing.T) {
	base, _, srv := hgSite(t)
	hgLibrary(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	ada := hgDocOf(t, hgCall(t, base, "POST", "/writers", full, map[string]any{"data": map[string]any{"name": "Ada"}}))
	bob := hgDocOf(t, hgCall(t, base, "POST", "/writers", full, map[string]any{"data": map[string]any{"name": "Bob"}}))
	b1 := hgDocOf(t, hgCall(t, base, "POST", "/books", full, map[string]any{"data": map[string]any{"title": "One", "writer": ada}}))
	b2 := hgDocOf(t, hgCall(t, base, "POST", "/books", full, map[string]any{"data": map[string]any{"title": "Two", "writer": bob}}))
	has := func(list []string, want string) bool {
		for _, x := range list {
			if x == want {
				return true
			}
		}
		return false
	}
	// Bob takes book One through his inverse field: Ada, who lost it, records it too.
	if r := hgCall(t, base, "PUT", "/writers/"+bob, full, map[string]any{"data": map[string]any{"books": []any{b1, b2}}}); r.Code != 200 {
		t.Fatalf("inverse write: %d %s", r.Code, r.Raw)
	}
	if n := hgNotes(t, srv, "writer", ada); !has(n, `link: Unlinked by Book "One" via books: it now belongs to Writer "Bob"`) {
		t.Fatalf("ada's history after Bob took her book: %v", n)
	}
	// Deleting an owning entry is recorded on the entry it pointed at.
	hgCall(t, base, "DELETE", "/books/"+b2, full, nil)
	if n := hgNotes(t, srv, "writer", bob); !has(n, `link: Unlinked by Book "Two" via books: it was deleted`) {
		t.Fatalf("bob's history after book Two was deleted: %v", n)
	}
	// Deleting a related document is recorded on the entries that lose their link to it.
	hgCall(t, base, "DELETE", "/writers/"+bob, full, nil)
	if n := hgNotes(t, srv, "book", b1); !has(n, `link: Unlinked Writer "Bob" via writer: it was deleted`) {
		t.Fatalf("book One's history after its writer was deleted: %v", n)
	}
	if e, _ := srv.hgGetEntry(context.Background(), "book", b1, ""); e.Data["writer"] != nil {
		t.Fatalf("the deleted writer is still linked: %v", e.Data)
	}
}
