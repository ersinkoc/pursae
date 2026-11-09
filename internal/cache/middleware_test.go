package cache

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testApp renders a page per path and counts renders.
type testApp struct {
	calls atomic.Int64
	mod   func(w http.ResponseWriter, r *http.Request) // optional header tweaks
}

func (a *testApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := a.calls.Add(1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set(HeaderTags, "home, post:12,term:3")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	code := http.StatusOK
	if r.URL.Path == "/missing" {
		code = http.StatusNotFound
	}
	if a.mod != nil {
		a.mod(w, r)
	}
	w.WriteHeader(code)
	io.WriteString(w, "<html><body><h1>"+r.URL.Path+"</h1>"+strings.Repeat("<p>lorem ipsum dolor</p>", 50))
	io.WriteString(w, "<!-- render "+string(rune('0'+n%10))+" --></body></html>")
}

func newTestCache(t testing.TB, mut func(*Config)) (*Cache, *testApp, http.Handler) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.ServerHint = HintNone
	if mut != nil {
		mut(&cfg)
	}
	c := New(cfg, Options{})
	app := &testApp{}
	return c, app, c.Middleware(app)
}

func do(h http.Handler, method, target string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Add(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func status(w *httptest.ResponseRecorder) string { return w.Header().Get(HeaderStatus) }

func TestHitMissBypass(t *testing.T) {
	c, app, h := newTestCache(t, nil)
	w1 := do(h, "GET", "http://example.com/hello/")
	if status(w1) != "miss" || w1.Code != 200 {
		t.Fatalf("first: %q %d", status(w1), w1.Code)
	}
	w2 := do(h, "GET", "http://example.com/hello/")
	if status(w2) != "hit" {
		t.Fatalf("second: %q", status(w2))
	}
	if w1.Body.String() != w2.Body.String() {
		t.Fatal("hit body differs from miss body")
	}
	if app.calls.Load() != 1 {
		t.Fatalf("app rendered %d times", app.calls.Load())
	}
	if w2.Header().Get("Age") == "" || w2.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers not restored: %v", w2.Header())
	}
	// HEAD is served from the cache.
	if w := do(h, "HEAD", "http://example.com/hello/"); status(w) != "hit" || w.Body.Len() != 0 {
		t.Fatalf("HEAD: %q len=%d", status(w), w.Body.Len())
	}
	// POST bypasses entirely and carries no cache status.
	if w := do(h, "POST", "http://example.com/hello/"); status(w) != "" {
		t.Fatalf("POST status %q", status(w))
	}
	// Excluded paths bypass.
	if w := do(h, "GET", "http://example.com/wp-admin/edit"); status(w) != "bypass" {
		t.Fatalf("wp-admin: %q", status(w))
	}
	// Authorization bypasses.
	if w := do(h, "GET", "http://example.com/hello/", "Authorization", "Basic eDp5"); status(w) != "bypass" {
		t.Fatalf("authorization: %q", status(w))
	}
	// 404 is cached with its own TTL.
	do(h, "GET", "http://example.com/missing")
	w := do(h, "GET", "http://example.com/missing")
	if status(w) != "hit" || w.Code != 404 {
		t.Fatalf("404: %q %d", status(w), w.Code)
	}
	if st := c.Stats(); st.Hits < 2 || st.Entries != 2 {
		t.Fatalf("stats %+v", st)
	}
}

func TestDisabledPassesThroughAndStripsInternal(t *testing.T) {
	_, app, h := newTestCache(t, func(c *Config) { c.Enabled = false })
	w := do(h, "GET", "http://example.com/")
	do(h, "GET", "http://example.com/")
	if app.calls.Load() != 2 || status(w) != "" || w.Header().Get(HeaderTags) != "" {
		t.Fatalf("calls=%d status=%q tags=%q", app.calls.Load(), status(w), w.Header().Get(HeaderTags))
	}
}

func TestCookieBypass(t *testing.T) {
	_, app, h := newTestCache(t, func(c *Config) { c.ExcludeCookies = []string{"woocommerce_items_in_cart"} })
	do(h, "GET", "http://example.com/p/") // prime
	for _, ck := range []string{
		"wpgo_logged_in=abc",
		"wp-postpass_123abc=x",
		"comment_author_email_abcdef=a%40b.c",
		"woocommerce_items_in_cart=1",
	} {
		w := do(h, "GET", "http://example.com/p/", "Cookie", ck)
		if status(w) != "bypass" {
			t.Errorf("cookie %s: status %q", ck, status(w))
		}
		if !strings.Contains(w.Body.String(), "<h1>/p/</h1>") {
			t.Errorf("cookie %s: page not served", ck)
		}
	}
	if app.calls.Load() != 5 {
		t.Fatalf("calls %d", app.calls.Load())
	}
	// Unrelated cookies do not bypass.
	if w := do(h, "GET", "http://example.com/p/", "Cookie", "theme=dark"); status(w) != "hit" {
		t.Fatalf("unrelated cookie: %q", status(w))
	}
}

func TestCacheLoggedInPrivateBucket(t *testing.T) {
	c, app, h := newTestCache(t, func(c *Config) { c.CacheLoggedIn = true; c.ServerHint = HintLiteSpeed })
	do(h, "GET", "http://example.com/p/", "Cookie", "wpgo_logged_in=alice")
	w := do(h, "GET", "http://example.com/p/", "Cookie", "wpgo_logged_in=alice")
	if status(w) != "hit" {
		t.Fatalf("alice second: %q", status(w))
	}
	if w.Header().Get("X-LiteSpeed-Cache-Control") != "no-cache" || !strings.Contains(w.Header().Get("Cache-Control"), "private") {
		t.Fatalf("private entry leaked to proxy: %v", w.Header())
	}
	if w := do(h, "GET", "http://example.com/p/", "Cookie", "wpgo_logged_in=bob"); status(w) != "miss" {
		t.Fatalf("bob shares alice's bucket: %q", status(w))
	}
	if w := do(h, "GET", "http://example.com/p/"); status(w) != "miss" {
		t.Fatalf("anonymous shares a private bucket: %q", status(w))
	}
	if w := do(h, "GET", "http://example.com/p/", "Cookie", "wpgo_logged_in=alice", "Cache-Control", "no-cache"); status(w) != "bypass" {
		t.Fatalf("logged-in no-cache: %q", status(w))
	}
	if app.calls.Load() != 4 || c.Stats().Entries != 3 {
		t.Fatalf("calls=%d entries=%d", app.calls.Load(), c.Stats().Entries)
	}
}

func TestQueryStringRules(t *testing.T) {
	c, app, h := newTestCache(t, nil)
	do(h, "GET", "http://example.com/post/")
	if w := do(h, "GET", "http://example.com/post/?utm_source=x&fbclid=y&gclid=z"); status(w) != "hit" {
		t.Fatalf("dropped params: %q", status(w))
	}
	if w := do(h, "GET", "http://example.com/post/?foo=1"); status(w) != "bypass" {
		t.Fatalf("unknown param: %q", status(w))
	}
	if w := do(h, "GET", "http://example.com/post/?nocache"); status(w) != "bypass" {
		t.Fatalf("nocache: %q", status(w))
	}
	if w := do(h, "GET", "http://example.com/?s=go&paged=2"); status(w) != "miss" {
		t.Fatalf("allowed: %q", status(w))
	}
	// Parameter order and tracking params do not change the key.
	if w := do(h, "GET", "http://example.com/?paged=2&utm_medium=m&s=go"); status(w) != "hit" {
		t.Fatalf("reordered: %q", status(w))
	}
	if w := do(h, "GET", "http://example.com/?s=rust"); status(w) != "miss" {
		t.Fatalf("different search: %q", status(w))
	}
	if app.calls.Load() != 5 {
		t.Fatalf("calls %d", app.calls.Load())
	}
	r := httptest.NewRequest("GET", "http://Example.com/a/?utm_x=1&s=b", nil)
	if k := c.Key(r); k != "http://example.com/a/?s=b#d" {
		t.Fatalf("key %q", k)
	}
	r.Header.Set("X-Forwarded-Proto", "https")
	if k := c.Key(r); !strings.HasPrefix(k, "https://") {
		t.Fatalf("scheme not honored: %q", k)
	}
}

func TestMobileVary(t *testing.T) {
	_, app, h := newTestCache(t, func(c *Config) { c.CacheMobileSeparately = true })
	iphone := "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile/15E148"
	do(h, "GET", "http://example.com/", "User-Agent", "Mozilla/5.0 (Macintosh)")
	if w := do(h, "GET", "http://example.com/", "User-Agent", iphone); status(w) != "miss" {
		t.Fatalf("mobile shares desktop entry: %q", status(w))
	}
	if w := do(h, "GET", "http://example.com/", "User-Agent", iphone); status(w) != "hit" {
		t.Fatalf("mobile second: %q", status(w))
	}
	if app.calls.Load() != 2 {
		t.Fatalf("calls %d", app.calls.Load())
	}
}

func TestPurgeTagsURLAndAll(t *testing.T) {
	c, _, h := newTestCache(t, nil)
	h2 := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set(HeaderTags, "post:99")
		io.WriteString(w, "other")
	}))
	do(h, "GET", "http://example.com/a/")
	do(h, "GET", "http://example.com/b/")
	do(h, "GET", "http://example.com/b/?paged=2")
	do(h2, "GET", "http://example.com/c/")
	if st := c.Stats(); st.Tags["post:12"] != 3 || st.Tags["post:99"] != 1 {
		t.Fatalf("tag counts %v", st.Tags)
	}
	if n := c.PurgeURL("/b/"); n != 2 {
		t.Fatalf("PurgeURL removed %d", n)
	}
	if n := c.PurgeTags("post:12"); n != 1 {
		t.Fatalf("PurgeTags removed %d", n)
	}
	if w := do(h2, "GET", "http://example.com/c/"); status(w) != "hit" {
		t.Fatalf("unrelated tag purged: %q", status(w))
	}
	if w := do(h, "GET", "http://example.com/a/"); status(w) != "miss" {
		t.Fatalf("tagged page survived purge: %q", status(w))
	}
	c.PurgeAll()
	if st := c.Stats(); st.Entries != 0 || len(st.Tags) != 0 || st.Bytes != 0 {
		t.Fatalf("after PurgeAll: %+v", st)
	}
}

func TestExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	cfg := DefaultConfig()
	cfg.PublicTTL = time.Minute
	c := New(cfg, Options{Now: clock})
	h := c.Middleware(&testApp{})
	do(h, "GET", "http://example.com/x/")
	now = now.Add(30 * time.Second)
	if w := do(h, "GET", "http://example.com/x/"); status(w) != "hit" || w.Header().Get("Age") != "30" {
		t.Fatalf("fresh: %q age=%s", status(w), w.Header().Get("Age"))
	}
	now = now.Add(31 * time.Second)
	if n := c.PurgeExpired(); n != 1 {
		t.Fatalf("PurgeExpired %d", n)
	}
	if w := do(h, "GET", "http://example.com/x/"); status(w) != "miss" {
		t.Fatalf("expired: %q", status(w))
	}
}

func TestGzipNegotiationAndETag(t *testing.T) {
	_, _, h := newTestCache(t, nil)
	plain := do(h, "GET", "http://example.com/g/")
	gz := do(h, "GET", "http://example.com/g/", "Accept-Encoding", "br, gzip;q=0.8")
	if gz.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(gz.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("gzip headers: %v", gz.Header())
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	if string(got) != plain.Body.String() {
		t.Fatal("gzip body does not decompress to the plain body")
	}
	if no := do(h, "GET", "http://example.com/g/", "Accept-Encoding", "gzip;q=0"); no.Header().Get("Content-Encoding") != "" {
		t.Fatal("gzip served despite q=0")
	}
	etag := plain.Header().Get("ETag")
	if etag == "" || gz.Header().Get("ETag") == etag {
		t.Fatalf("etags plain=%q gzip=%q", etag, gz.Header().Get("ETag"))
	}
	for _, inm := range []string{etag, gz.Header().Get("ETag"), `W/` + etag, `"nope", ` + etag} {
		w := do(h, "GET", "http://example.com/g/", "If-None-Match", inm)
		if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
			t.Fatalf("If-None-Match %s: %d", inm, w.Code)
		}
	}
	if w := do(h, "GET", "http://example.com/g/", "If-None-Match", `"other"`); w.Code != 200 {
		t.Fatalf("non-matching etag: %d", w.Code)
	}
}

func TestUncacheableResponses(t *testing.T) {
	cases := map[string]func(w http.ResponseWriter, r *http.Request){
		"set-cookie":    func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Set-Cookie", "a=b") },
		"private":       func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Cache-Control", "private, max-age=0") },
		"no-store":      func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Cache-Control", "no-store") },
		"app-no-cache":  func(w http.ResponseWriter, r *http.Request) { w.Header().Set(HeaderControl, "no-cache") },
		"ttl-zero":      func(w http.ResponseWriter, r *http.Request) { w.Header().Set(HeaderTTL, "0") },
		"json":          func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Content-Type", "application/json") },
		"pre-encoded":   func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Content-Encoding", "br") },
		"feed-ttl-zero": func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Content-Type", "application/rss+xml") },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			c, app, h := newTestCache(t, nil)
			app.mod = mod
			do(h, "GET", "http://example.com/u/")
			w := do(h, "GET", "http://example.com/u/")
			if app.calls.Load() != 2 || c.Stats().Entries != 0 {
				t.Fatalf("cached: calls=%d entries=%d", app.calls.Load(), c.Stats().Entries)
			}
			for _, k := range []string{HeaderTags, HeaderTTL, HeaderControl} {
				if w.Header().Get(k) != "" {
					t.Fatalf("internal header %s leaked", k)
				}
			}
			if name == "set-cookie" && w.Header().Get("Set-Cookie") == "" {
				t.Fatal("Set-Cookie dropped from uncached response")
			}
		})
	}
}

func TestInternalHeadersStrippedAndTTLOverride(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cfg := DefaultConfig()
	cfg.ServerHint = HintNginx
	c := New(cfg, Options{Now: func() time.Time { return now }})
	app := &testApp{mod: func(w http.ResponseWriter, r *http.Request) { w.Header().Set(HeaderTTL, "120") }}
	h := c.Middleware(app)
	for i := 0; i < 2; i++ {
		w := do(h, "GET", "http://example.com/t/")
		for _, k := range []string{HeaderTags, HeaderTTL, HeaderControl} {
			if w.Header().Get(k) != "" {
				t.Fatalf("internal header %s leaked on %s", k, status(w))
			}
		}
		if w.Header().Get("X-Accel-Expires") != "120" {
			t.Fatalf("TTL override not applied: %v", w.Header())
		}
	}
}

func TestServerHints(t *testing.T) {
	type check struct{ header, want string }
	cases := []struct {
		hint ServerHint
		req  []string
		want []check
		none []string
	}{
		{HintLiteSpeed, nil, []check{
			{"X-LiteSpeed-Cache-Control", "public,max-age=604800"},
			{"X-LiteSpeed-Tag", "wpgo_home,wpgo_post:12,wpgo_term:3"},
		}, []string{"X-Accel-Expires", "Surrogate-Key"}},
		{HintAuto, []string{"X-LSCACHE", "on"}, []check{{"X-LiteSpeed-Cache-Control", "public,max-age=604800"}}, nil},
		{HintAuto, nil, nil, []string{"X-LiteSpeed-Cache-Control", "X-LiteSpeed-Tag", "X-Accel-Expires", "Surrogate-Key"}},
		{HintNginx, nil, []check{{"X-Accel-Expires", "604800"}}, []string{"X-LiteSpeed-Cache-Control"}},
		{HintGeneric, nil, []check{
			{"Cache-Control", "public, s-maxage=604800, max-age=0"},
			{"Surrogate-Key", "wpgo_home wpgo_post:12 wpgo_term:3"},
			{"Cache-Tag", "wpgo_home,wpgo_post:12,wpgo_term:3"},
		}, nil},
		{HintNone, nil, nil, []string{"X-LiteSpeed-Cache-Control", "X-Accel-Expires", "Surrogate-Key", "Cache-Tag"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.hint), func(t *testing.T) {
			now := time.Unix(1_700_000_000, 0)
			cfg := DefaultConfig()
			cfg.ServerHint = tc.hint
			c := New(cfg, Options{Now: func() time.Time { return now }, IsLiteSpeed: func(r *http.Request) bool { return r.Header.Get("X-LSCACHE") != "" }})
			h := c.Middleware(&testApp{})
			for _, w := range []*httptest.ResponseRecorder{do(h, "GET", "http://example.com/p/", tc.req...), do(h, "GET", "http://example.com/p/", tc.req...)} {
				for _, ck := range tc.want {
					if got := w.Header().Get(ck.header); got != ck.want {
						t.Errorf("%s (%s) = %q, want %q", ck.header, status(w), got, ck.want)
					}
				}
				for _, k := range tc.none {
					if v := w.Header().Get(k); v != "" {
						t.Errorf("unexpected %s: %q", k, v)
					}
				}
			}
			// Bypassed pages tell the proxy not to cache.
			w := do(h, "GET", "http://example.com/p/", append([]string{"Cookie", "wpgo_logged_in=x"}, tc.req...)...)
			if tc.hint == HintLiteSpeed && w.Header().Get("X-LiteSpeed-Cache-Control") != "no-cache" {
				t.Errorf("bypass hint missing: %v", w.Header())
			}
			if tc.hint == HintNginx && w.Header().Get("X-Accel-Expires") != "0" {
				t.Errorf("bypass hint missing: %v", w.Header())
			}
		})
	}
}

func TestPurgeHeader(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ServerHint = HintLiteSpeed
	c := New(cfg, Options{})
	if got := c.PurgeHeader("post:12", "home"); got != "tag=wpgo_home, tag=wpgo_post:12" {
		t.Fatalf("PurgeHeader = %q", got)
	}
	if got := c.PurgeHeader(); got != "*" {
		t.Fatalf("PurgeHeader() = %q", got)
	}
	cfg.ServerHint = HintNginx
	if got := New(cfg, Options{}).PurgeHeader("x"); got != "" {
		t.Fatalf("nginx PurgeHeader = %q", got)
	}
}

func TestBrowserCacheTTLForAssets(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BrowserCacheTTL = 24 * time.Hour
	c := New(cfg, Options{})
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		io.WriteString(w, "body{}")
	}))
	w := do(h, "GET", "http://example.com/wp-includes/a.css")
	if w.Header().Get("Cache-Control") != "public, max-age=86400" || status(w) != "" {
		t.Fatalf("asset headers: %v", w.Header())
	}
	if c.Stats().Entries != 0 {
		t.Fatal("asset stored")
	}
}

func TestLargeResponseStreamsUncached(t *testing.T) {
	c, _, h := newTestCache(t, func(c *Config) { c.MaxEntrySize = 100 })
	w := do(h, "GET", "http://example.com/big/")
	if w.Code != 200 || !strings.HasSuffix(w.Body.String(), "</html>") || status(w) != "bypass" {
		t.Fatalf("big: %d %q", w.Code, status(w))
	}
	if c.Stats().Entries != 0 {
		t.Fatal("oversized response stored")
	}
}

func TestFlushPassthrough(t *testing.T) {
	cfg := DefaultConfig()
	c := New(cfg, Options{})
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: 1\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush: %v", err)
		}
	}))
	w := do(h, "GET", "http://example.com/events")
	if !w.Flushed {
		t.Fatal("flush did not reach the client")
	}
}

func TestOptimizeAppliedOnStore(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Optimize = OptimizeConfig{LazyImages: true, LazySkipFirst: 1}
	c := New(cfg, Options{})
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<html><body><img src="a.jpg"><img src="b.jpg"></body></html>`)
	}))
	w := do(h, "GET", "http://example.com/o/")
	if !strings.Contains(w.Body.String(), `<img src="b.jpg" loading="lazy" decoding="async">`) {
		t.Fatalf("not optimized: %s", w.Body.String())
	}
}

func BenchmarkCacheHit(b *testing.B) {
	_, _, h := newTestCache(b, nil)
	do(h, "GET", "http://example.com/bench/")
	r := httptest.NewRequest("GET", "http://example.com/bench/?utm_source=x", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			b.Fatal(w.Code)
		}
	}
}

// The ops handler sets X-Request-ID before the cache runs; a hit must return its own ID, not the one of the
// request that filled the cache.
func TestRequestIDNotReplayed(t *testing.T) {
	_, _, h := newTestCache(t, nil)
	var n atomic.Int64
	outer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "req-"+string(rune('a'+n.Add(1))))
		h.ServeHTTP(w, r)
	})
	w1 := do(outer, "GET", "http://example.com/rid/")
	w2 := do(outer, "GET", "http://example.com/rid/")
	if status(w2) != "hit" {
		t.Fatalf("second: %q", status(w2))
	}
	if w1.Header().Get("X-Request-ID") != "req-b" || w2.Header().Get("X-Request-ID") != "req-c" {
		t.Fatalf("request ids: %q then %q", w1.Header().Get("X-Request-ID"), w2.Header().Get("X-Request-ID"))
	}
}

// Search result pages are cached for at most searchTTL, whatever the public TTL.
func TestSearchTTLCapped(t *testing.T) {
	c, _, h := newTestCache(t, nil)
	do(h, "GET", "http://example.com/?s=gopher")
	do(h, "GET", "http://example.com/about/")
	now := time.Now()
	e, ok := c.store.Get(c.Key(httptest.NewRequest("GET", "http://example.com/?s=gopher", nil)))
	if !ok || e.Expires.Sub(now) > searchTTL+time.Minute {
		t.Fatalf("search entry: ok=%v expires in %s", ok, e.Expires.Sub(now))
	}
	e, ok = c.store.Get(c.Key(httptest.NewRequest("GET", "http://example.com/about/", nil)))
	if !ok || e.Expires.Sub(now) <= searchTTL+time.Minute {
		t.Fatalf("page entry: ok=%v expires in %s", ok, e.Expires.Sub(now))
	}
}

func TestRepeatedQueryValueOrder(t *testing.T) {
	c := New(DefaultConfig(), Options{})
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, r.URL.Query().Get("s"))
	}))
	for _, query := range []string{"s=z&s=a", "s=a&s=z", "s=&s=x", "s=x&s="} {
		r := httptest.NewRequest("GET", "http://example.com/?"+query, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if got, want := w.Body.String(), r.URL.Query().Get("s"); got != want {
			t.Errorf("%s: got %q, want %q", query, got, want)
		}
	}
}
