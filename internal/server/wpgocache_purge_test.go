package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"wpgo/internal/cache"
	"wpgo/internal/store"
)

// pageCacheGet fetches a page as an anonymous visitor and returns its X-WPGo-Cache status and body.
func pageCacheGet(t *testing.T, u string) (string, string) {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.Header.Get(cache.HeaderStatus), string(b)
}

// pageCacheWarm fetches a page until it is served from the cache.
func pageCacheWarm(t *testing.T, u string) {
	t.Helper()
	pageCacheGet(t, u)
	if st, _ := pageCacheGet(t, u); st != "hit" {
		t.Fatalf("%s: X-WPGo-Cache = %q on the second request, want hit", u, st)
	}
}

// Stale pages the purges used to miss: a term renamed or a media item edited through REST, and
// purges made by another instance sharing the database. (The archive a post was moved out of is
// covered too: it lists the post, so it carries the post's author tag.)
func TestPageCachePurgeCoverage(t *testing.T) {
	ts, c0, srv := newTestServer(t, false)
	ctx := context.Background()
	installClassic(t, c0, ts.URL)
	if err := srv.settings.Set(ctx, map[string]any{"wpgo_cache_enabled": true}); err != nil {
		t.Fatal(err)
	}
	srv.configureCache()
	path := func(u string) string {
		p, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}
		return ts.URL + p.RequestURI()
	}

	alpha := &store.Term{Taxonomy: "category", Name: "Alpha"}
	bravo := &store.Term{Taxonomy: "category", Name: "Bravo"}
	for _, tm := range []*store.Term{alpha, bravo} {
		if err := srv.store.InsertTerm(ctx, tm); err != nil {
			t.Fatal(err)
		}
	}
	img := &store.Attachment{UploaderID: 1, Slug: "pic", FilePath: "2026/11/pic.jpg", Mime: "image/jpeg", Width: 800, Height: 600, Title: "Pic", Alt: "old alt"}
	if err := srv.store.InsertAttachment(ctx, img); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p := &store.Post{Type: "post", Status: "publish", Title: "Moving Target", Content: "<p>Body</p>", AuthorID: 1,
		PublishedAt: &now, FeaturedMediaID: img.ID}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetPostTerms(ctx, p.ID, "category", []int64{alpha.ID}); err != nil {
		t.Fatal(err)
	}
	srv.afterPostSave(ctx, p)
	lb := srv.links(ctx)
	alphaURL, postURL := path(lb.TermLink(alpha)), path(lb.Permalink(p))

	// 1. The post moves from Alpha to Bravo: Alpha's archive no longer lists it.
	pageCacheWarm(t, alphaURL)
	if _, body := pageCacheGet(t, alphaURL); !strings.Contains(body, "Moving Target") {
		t.Fatal("the Alpha archive does not list the post")
	}
	if err := srv.store.SetPostTerms(ctx, p.ID, "category", []int64{bravo.ID}); err != nil {
		t.Fatal(err)
	}
	srv.afterPostSave(ctx, p)
	if st, body := pageCacheGet(t, alphaURL); st == "hit" || strings.Contains(body, "Moving Target") {
		t.Fatalf("the Alpha archive still lists the post after it moved to Bravo (X-WPGo-Cache %q)", st)
	}

	// 2. Bravo is renamed through REST: the post's page shows the new name.
	pageCacheWarm(t, postURL)
	nonce := restNonce(t, srv, c0, ts.URL)
	rest := func(method, route, body string) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+"/wp-json"+route, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-WP-Nonce", nonce)
		resp, err := c0.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s %s: %d", method, route, resp.StatusCode)
		}
	}
	rest(http.MethodPost, "/wp/v2/categories/"+itoa64(bravo.ID), `{"name":"Bravo Renamed"}`)
	if st, body := pageCacheGet(t, postURL); st == "hit" || !strings.Contains(body, "Bravo Renamed") {
		t.Fatalf("the post's page still shows the old category name (X-WPGo-Cache %q)", st)
	}

	// 3. The featured image's alt text is edited through REST: the post's page is rendered again.
	pageCacheWarm(t, postURL)
	rest(http.MethodPost, "/wp/v2/media/"+itoa64(img.ID), `{"alt_text":"new alt"}`)
	if st, body := pageCacheGet(t, postURL); st == "hit" || strings.Contains(body, "old alt") {
		t.Fatalf("the post's page still shows the old featured image text (X-WPGo-Cache %q)", st)
	}

	// 4. Another instance announces a purge of the front page: this one drops its copy.
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	go srv.WatchCachePurges(wctx)
	home := ts.URL + "/"
	pageCacheWarm(t, home)
	payload := cachePurgePayload("another-instance", cache.Purge{Tags: []string{"home"}})
	deadline := time.Now().Add(5 * time.Second)
	for {
		// Repeated: the listener may not have subscribed yet when the first notification goes out.
		if _, err := srv.store.Pool.Exec(ctx, `SELECT pg_notify($1, $2)`, cachePurgeChannel, payload); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
		if st, _ := pageCacheGet(t, home); st != "hit" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a purge announced by another instance did not reach this one")
		}
	}
}

// The purge announcements: this instance's own are skipped, others' applied, oversized ones become a
// purge of everything.
func TestCachePurgeAnnouncements(t *testing.T) {
	s := &Server{}
	cfg := cache.DefaultConfig()
	cfg.ServerHint = cache.HintNone
	c := cache.New(cfg, cache.Options{})
	s.cache.Store(&pageCache{c: c})
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set(cache.HeaderTags, "home")
		io.WriteString(w, "<html>page</html>")
	}))
	fill := func() {
		w := &discardRW{h: http.Header{}}
		h.ServeHTTP(w, httpGet("http://example.com/"))
	}
	fill()
	s.applyRemotePurge(cachePurgePayload(cacheOrigin(), cache.Purge{Tags: []string{"home"}}))
	if c.Stats().Entries != 1 {
		t.Fatal("this instance's own announcement purged its cache again")
	}
	s.applyRemotePurge(cachePurgePayload("other", cache.Purge{Tags: []string{"home"}}))
	if c.Stats().Entries != 0 {
		t.Fatal("another instance's purge was not applied")
	}
	fill()
	s.applyRemotePurge("not json")
	if c.Stats().Entries != 0 {
		t.Fatal("an unreadable announcement must purge everything")
	}
	big := make([]string, 2000)
	for i := range big {
		big[i] = "term:" + itoa64(int64(i))
	}
	if p := cachePurgePayload("x", cache.Purge{Tags: big}); len(p) > cachePurgeMaxPayload || !strings.Contains(p, `"a":true`) {
		t.Fatalf("oversized announcement: %d bytes %.60s", len(p), p)
	}
}

type discardRW struct{ h http.Header }

func (d *discardRW) Header() http.Header         { return d.h }
func (d *discardRW) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardRW) WriteHeader(int)             {}

func httpGet(u string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, u, nil)
	r.Host = "example.com"
	return r
}

// Moving a post to the Trash purges its pages before the change (its terms are needed); a visitor
// whose page was rendered in between must not leave the published post in the cache.
func TestPageCacheTrashRaceRepurged(t *testing.T) {
	ts, c0, srv := newTestServer(t, false)
	ctx := context.Background()
	installClassic(t, c0, ts.URL)
	if err := srv.settings.Set(ctx, map[string]any{"wpgo_cache_enabled": true}); err != nil {
		t.Fatal(err)
	}
	srv.configureCache()
	now := time.Now()
	p := &store.Post{Type: "post", Status: "publish", Title: "Soon Trashed", Content: "<p>Secret body</p>", AuthorID: 1, PublishedAt: &now}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(srv.links(ctx).Permalink(p))
	postURL := ts.URL + u.RequestURI()
	pageCacheWarm(t, postURL)

	// The visitor arrives right after the first purge, before the status changes.
	purge := srv.onPostSaved
	srv.onPostSaved = func(ctx context.Context, q *store.Post) {
		purge(ctx, q)
		if q.ID == p.ID && q.Status == "publish" {
			pageCacheGet(t, postURL)
		}
	}
	defer func() { srv.onPostSaved = purge }()
	cu, _ := url.Parse(ts.URL)
	var nonce string
	for _, ck := range c0.Jar.Cookies(cu) {
		if sess, err := srv.auth.Lookup(ctx, ck.Value); err == nil {
			nonce = srv.auth.Nonce("trash-post_"+itoa64(p.ID), sess.UserID, sess.Token)
		}
	}
	if code, _, _ := get(t, c0, ts.URL+"/wp-admin/post?post="+itoa64(p.ID)+"&action=trash&_wpnonce="+nonce); code != http.StatusOK && code != http.StatusFound {
		t.Fatalf("trash: %d", code)
	}
	if got, _ := srv.store.GetPost(ctx, p.ID); got == nil || got.Status != "trash" {
		t.Fatal("the post was not trashed")
	}
	if st, body := pageCacheGet(t, postURL); st == "hit" || strings.Contains(body, "Secret body") {
		t.Fatalf("the trashed post is still served from the cache (X-WPGo-Cache %q)", st)
	}
}
