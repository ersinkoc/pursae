package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A contributor (edit_posts only) may not publish, unpublish or delete entries, nor change another
// user's entry; an administrator still can.
func TestHeadlessGoAdminEntryCaps(t *testing.T) {
	base, admin, srv := hgSite(t)
	hgMemo(t, srv, false)
	contrib := hgSecondUser(t, srv, base, "connie", "contributor")
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	doc := hgDocOf(t, hgCall(t, base, "POST", "/memos", full, map[string]any{"data": map[string]any{"title": "One"}}))
	published := func() bool {
		r := hgCall(t, base, "GET", "/memos/"+doc, full, nil)
		return r.Code == 200
	}
	if published() {
		t.Fatal("a new entry must be a draft")
	}
	hgEntryForm(t, contrib, base, "memo", doc, "publish_entry", nil)
	if published() {
		t.Error("a contributor published an entry")
	}
	hgEntryForm(t, contrib, base, "memo", doc, "save_entry", url.Values{"payload": {`{"title":"Hacked"}`}})
	if r := hgCall(t, base, "GET", "/memos/"+doc+"?status=draft", full, nil); strings.Contains(r.Raw, "Hacked") {
		t.Error("a contributor changed another user's entry")
	}
	hgEntryForm(t, contrib, base, "memo", doc, "delete_entry", nil)
	if r := hgCall(t, base, "GET", "/memos/"+doc+"?status=draft", full, nil); r.Code != 200 {
		t.Errorf("a contributor deleted an entry: %d", r.Code)
	}
	hgEntryForm(t, admin, base, "memo", doc, "publish_entry", nil)
	if !published() {
		t.Error("the administrator could not publish")
	}
}

func TestHeadlessGoETagAndRateLimit(t *testing.T) {
	base, _, srv := hgSite(t)
	ctx := context.Background()
	hgMemo(t, srv, false)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	hgCall(t, base, "POST", "/memos?status=published", full, map[string]any{"data": map[string]any{"title": "One"}})
	st := srv.hgLoad()
	st.Public = map[string][]string{"memo": {"find"}}
	st.CacheMaxAge = 60
	st.RateLimit = 5
	_ = srv.hgSaveSettings(ctx, st)

	r := hgCall(t, base, "GET", "/memos", "", nil)
	etag := r.Header.Get("ETag")
	if r.Code != 200 || etag == "" || !strings.Contains(r.Header.Get("Cache-Control"), "s-maxage=60") {
		t.Fatalf("public read: %d etag=%q cc=%q", r.Code, etag, r.Header.Get("Cache-Control"))
	}
	if r := hgCall(t, base, "GET", "/memos", "", nil, "If-None-Match", etag); r.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: %d", r.Code)
	}
	got429 := false
	for i := 0; i < 10; i++ {
		if r := hgCall(t, base, "GET", "/memos", "", nil); r.Code == http.StatusTooManyRequests {
			got429 = r.Header.Get("Retry-After") != ""
			break
		}
	}
	if !got429 {
		t.Error("the rate limit never answered 429 with Retry-After")
	}
	// The token has its own bucket.
	if r := hgCall(t, base, "GET", "/memos", full, nil); r.Code != 200 {
		t.Errorf("token read while the address is limited: %d", r.Code)
	}
}

func TestHeadlessGoGraphQLRootFieldCapAndIntrospection(t *testing.T) {
	base, _, srv := hgSite(t)
	ctx := context.Background()
	hgMemo(t, srv, false)
	st := srv.hgLoad()
	st.Public = map[string][]string{"memo": {"find"}}
	st.NoPublicIntro = true
	_ = srv.hgSaveSettings(ctx, st)
	var q strings.Builder
	q.WriteString("{")
	for i := 0; i < hgGQLMaxRootFields+1; i++ {
		q.WriteString(" a" + itoa64(int64(i)) + ": memos { documentId }")
	}
	q.WriteString("}")
	r := hgCall(t, base, "POST", "/graphql", "", map[string]any{"query": q.String()})
	if r.Code != 400 || !strings.Contains(r.Raw, "root fields") {
		t.Errorf("alias flood: %d %s", r.Code, r.Raw)
	}
	r = hgCall(t, base, "POST", "/graphql", "", map[string]any{"query": "{ __schema { queryType { name } } }"})
	if r.Code != 403 {
		t.Errorf("public introspection: %d %s", r.Code, r.Raw)
	}
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	if r := hgCall(t, base, "POST", "/graphql", full, map[string]any{"query": "{ __schema { queryType { name } } }"}); r.Code != 200 {
		t.Errorf("token introspection: %d %s", r.Code, r.Raw)
	}
}
