package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
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

func TestHeadlessGoRestoreReportsDroppedFields(t *testing.T) {
	base, admin, srv := hgSite(t)
	ctx := context.Background()
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Gizmo", SingularName: "gizmo", PluralName: "gizmos", Fields: []hgField{
		{Name: "name", Type: "text"}, {Name: "colour", Type: "text"}}})
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	doc := hgDocOf(t, hgCall(t, base, "POST", "/gizmos", full, map[string]any{"data": map[string]any{"name": "A", "colour": "red"}}))
	hgCall(t, base, "PUT", "/gizmos/"+doc, full, map[string]any{"data": map[string]any{"name": "B"}})
	e, _ := srv.hgGetEntry(ctx, "gizmo", doc, "")
	var v1 int64
	for _, v := range srv.hgVersions(ctx, e.ID, 50) {
		if v.Action == "create" {
			v1 = v.ID
		}
	}
	g := hgCopySchema(srv.hgReg(ctx).Get("gizmo"))
	g.Fields = g.Fields[:1] // colour is removed
	hgMustSchema(t, srv, g)
	_, body, _ := get(t, admin, base+"/wp-admin/headlessgo?page=history&type=gizmo&doc="+doc+"&v="+itoa64(v1))
	if !strings.Contains(body, "fields the content type no longer has") || !strings.Contains(body, `<code translate="no">colour</code>`) {
		t.Fatal("the history screen does not name the dropped field")
	}
	code, _, loc := hgAdminSubmit(t, admin, base, url.Values{"page": {"entry"}, "do": {"restore_version"}, "type": {"gizmo"}, "doc": {doc}, "version": {itoa64(v1)}})
	if code != http.StatusFound || strings.Contains(loc, "hg-error") || !strings.Contains(loc, "no+longer+has+these+fields%3A+colour") {
		t.Fatalf("restore message: %d %s", code, loc)
	}
	if e, _ := srv.hgGetEntry(ctx, "gizmo", doc, ""); e.Data["name"] != "A" || e.Data["colour"] != nil {
		t.Fatalf("restored values: %v", e.Data)
	}
}
