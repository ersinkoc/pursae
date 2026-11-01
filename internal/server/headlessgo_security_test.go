package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// Filtering and sorting through a relation only consider targets the caller may read: types it
// has no find/findOne permission on are invalid keys, WordPress posts and pages must be published,
// and a user's login name is not a filter or sort key.
func TestHeadlessGoRelationFilterPermissions(t *testing.T) {
	base, _, srv := hgSite(t)
	ctx := context.Background()
	hgModel(t, srv)
	st := srv.hgLoad()
	st.Public = map[string][]string{"article": {"find", "findOne"}, "category": {"find", "findOne"}}
	_ = srv.hgSaveSettings(ctx, st)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})

	page := &store.Post{Type: "page", Title: "Layoffs plan", Slug: "layoffs", Status: "publish", AuthorID: 1}
	if err := srv.store.InsertPost(ctx, page); err != nil {
		t.Fatal(err)
	}
	r := hgCall(t, base, "POST", "/authors", full, map[string]any{"data": map[string]any{"name": "Ada"}})
	ada := r.data()["documentId"].(string)
	r = hgCall(t, base, "POST", "/articles?status=published", full, map[string]any{"data": map[string]any{"title": "Public post", "writer": 1, "page": page.ID, "author": ada}})
	if r.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Code, r.Raw)
	}
	// The page becomes private after it was linked.
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET status = 'private' WHERE id = $1`, page.ID); err != nil {
		t.Fatal(err)
	}

	count := func(path string) (int, int) {
		r := hgCall(t, base, "GET", path, "", nil)
		return r.Code, len(r.list())
	}
	for _, path := range []string{
		"/articles?filters[writer][username][$startsWith]=a", // login names
		"/articles?sort=writer.username",
		"/articles?filters[author][name][$eq]=Ada", // the public may not read authors
		"/articles?filters[author][$notNull]=true",
		"/articles?sort=author.name",
	} {
		if code, n := count(path); code != http.StatusBadRequest {
			t.Errorf("%s: %d with %d results, want 400", path, code, n)
		}
	}
	for _, path := range []string{
		"/articles?filters[page][status][$eq]=private", // a private page is not a readable target
		"/articles?filters[page][title][$eq]=Layoffs%20plan",
		"/articles?filters[page][id][$gt]=0",
	} {
		if code, n := count(path); code != http.StatusOK || n != 0 {
			t.Errorf("%s: %d with %d results, want 200 and none", path, code, n)
		}
	}
	// Published again, the page is a readable target.
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE posts SET status = 'publish' WHERE id = $1`, page.ID)
	if code, n := count("/articles?filters[page][title][$eq]=Layoffs%20plan"); code != 200 || n != 1 {
		t.Errorf("filter through a published page: %d %d", code, n)
	}
	// What the public may read still filters: the user's public slug and name, readable types.
	if code, n := count("/articles?filters[writer][slug][$eq]=admin"); code != 200 || n != 1 {
		t.Errorf("filter by the user's slug: %d %d", code, n)
	}
	// A full-access token reads authors, so it may filter through them.
	if r := hgCall(t, base, "GET", "/articles?filters[author][name][$eq]=Ada&sort=author.name", full, nil); r.Code != 200 || len(r.list()) != 1 {
		t.Errorf("full-access filter through authors: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "GET", "/articles?filters[writer][username][$eq]=admin", full, nil); r.Code != 400 {
		t.Errorf("login names are not filterable, even with a token: %d %s", r.Code, r.Raw)
	}
}

// A write's response with populate shows related entries as a read would: no drafts for a token
// that may not read them.
func TestHeadlessGoWritePopulateHidesDrafts(t *testing.T) {
	base, _, srv := hgSite(t)
	hgModel(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	r := hgCall(t, base, "POST", "/categories", full, map[string]any{"data": map[string]any{"name": "Unannounced"}})
	cat := r.data()["documentId"].(string)
	writer := hgNewToken(t, srv, &hgToken{Name: "Writer", Kind: "custom", Permissions: map[string][]string{
		"article": {"create", "find", "findOne"}, "category": {"find", "findOne"}}})
	r = hgCall(t, base, "POST", "/articles?populate=categories", writer, map[string]any{"data": map[string]any{"title": "Mine", "categories": []any{cat}}})
	if r.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Code, r.Raw)
	}
	if strings.Contains(r.Raw, "Unannounced") {
		t.Errorf("a write response shows a related draft: %s", r.Raw)
	}
	// The full-access token reads drafts: it sees the related draft.
	r = hgCall(t, base, "POST", "/articles?populate=categories", full, map[string]any{"data": map[string]any{"title": "Ours", "categories": []any{cat}}})
	if !strings.Contains(r.Raw, "Unannounced") {
		t.Errorf("full-access write response: %s", r.Raw)
	}
}
