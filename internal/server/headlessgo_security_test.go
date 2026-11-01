package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// A preview token reads its own document's draft in its own locale, and nothing else: no other
// locale, no drafts of related entries, no types the public may not read.
func TestHeadlessGoPreviewTokenScope(t *testing.T) {
	base, c, srv := hgSite(t)
	ctx := context.Background()
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	hgModel(t, srv)
	st := srv.hgLoad()
	st.Public = map[string][]string{"article": {"find", "findOne"}, "category": {"find", "findOne"}}
	_ = srv.hgSaveSettings(ctx, st)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	r := hgCall(t, base, "POST", "/authors", full, map[string]any{"data": map[string]any{"name": "Ada Hidden"}})
	ada := r.data()["documentId"].(string)
	r = hgCall(t, base, "POST", "/categories", full, map[string]any{"data": map[string]any{"name": "Unannounced"}})
	cat := r.data()["documentId"].(string)
	r = hgCall(t, base, "POST", "/articles", full, map[string]any{"data": map[string]any{"title": "Draft en", "author": ada, "categories": []any{cat}}})
	doc := r.data()["documentId"].(string)
	if r := hgCall(t, base, "PUT", "/articles/"+doc+"?locale=fr", full, map[string]any{"data": map[string]any{"title": "Brouillon secret"}}); r.Code != 200 {
		t.Fatalf("fr draft: %d %s", r.Code, r.Raw)
	}
	ptok := srv.hgPreviewToken(srv.hgLoad(), "article", doc, "en", time.Hour)

	if r := hgCall(t, base, "GET", "/articles/"+doc+"?status=draft", ptok, nil); r.Code != 200 || r.data()["title"] != "Draft en" {
		t.Fatalf("preview read: %d %s", r.Code, r.Raw)
	}
	for _, path := range []string{"/articles/" + doc + "?status=draft&locale=fr", "/articles?status=draft&locale=fr"} {
		if r := hgCall(t, base, "GET", path, ptok, nil); r.Code != http.StatusForbidden || strings.Contains(r.Raw, "Brouillon") {
			t.Errorf("preview token in another locale %s: %d %s", path, r.Code, r.Raw)
		}
	}
	r = hgCall(t, base, "GET", "/articles/"+doc+"?status=draft&populate=author,categories,localizations", ptok, nil)
	if r.Code != 200 || strings.Contains(r.Raw, "Ada Hidden") || strings.Contains(r.Raw, "Unannounced") || strings.Contains(r.Raw, "Brouillon") {
		t.Errorf("preview populate reaches other drafts or types: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "GET", "/articles?status=draft&filters[categories][name][$eq]=Unannounced", ptok, nil); r.Code != 200 || len(r.list()) != 0 {
		t.Errorf("preview filter through a draft category: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "GET", "/articles?status=draft&filters[author][name][$eq]=Ada%20Hidden", ptok, nil); r.Code != 400 {
		t.Errorf("preview filter through a type the public cannot read: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "GET", "/categories/"+cat+"?status=draft", ptok, nil); r.Code != http.StatusForbidden {
		t.Errorf("preview token on another type: %d %s", r.Code, r.Raw)
	}
	// GraphQL: the same scope.
	q := `{"query":"{ article(documentId: \"` + doc + `\", status: DRAFT, locale: \"fr\") { title } }"}`
	req, _ := http.NewRequest(http.MethodPost, base+hgAPIPrefix+"/graphql", strings.NewReader(q))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ptok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if raw, _ := json.Marshal(out); strings.Contains(string(raw), "Brouillon") || out["errors"] == nil {
		t.Errorf("GraphQL preview in another locale: %s", raw)
	}
}

// Webhooks are delivered to public addresses only: the address is checked when the connection
// is made (after DNS resolution), so a webhook cannot read the server's own services.
func TestHeadlessGoWebhookPublicOnly(t *testing.T) {
	_, _, srv := hgSite(t)
	ctx := context.Background()
	var hits atomic.Int32
	inside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("internal-secret-body"))
	}))
	defer inside.Close()
	u, _ := url.Parse(inside.URL)
	for _, target := range []string{inside.URL + "/x", "http://localhost:" + u.Port() + "/x"} {
		w := &hgWebhook{Name: "Inside", URL: target, Events: []string{"entry.create"}, Enabled: true}
		if err := srv.hgSaveWebhook(ctx, w); err != nil {
			t.Fatal(err)
		}
		res := srv.hgTestWebhook(ctx, w)
		if res.Success || res.StatusCode != 0 || strings.Contains(res.Response, "internal-secret") || res.Error == "" {
			t.Errorf("webhook to %s: %+v", target, res)
		}
	}
	if hits.Load() != 0 {
		t.Errorf("the loopback server was reached %d times", hits.Load())
	}
}
