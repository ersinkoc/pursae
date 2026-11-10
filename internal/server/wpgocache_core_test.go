package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"wpgo/internal/auth"
	"wpgo/internal/cache"
)

// After a core update the pages persisted on disk were rendered by the old version: the first start
// of the new version must drop them, later starts of the same version keep them.
func TestPageCacheDroppedAfterCoreVersionChange(t *testing.T) {
	ts, c0, srv := newTestServer(t, false)
	ctx := context.Background()
	installClassic(t, c0, ts.URL) // the anonymous visitor below uses a client without the login cookie
	if err := srv.settings.Set(ctx, map[string]any{"wpgo_cache_enabled": true, "wpgo_cache_disk": true}); err != nil {
		t.Fatal(err)
	}
	srv.configureCache()
	if got := srv.settings.String(cacheCoreVersionOption); got != Version {
		t.Fatalf("recorded core version = %q, want %q", got, Version)
	}
	get := func() string {
		resp, err := http.Get(ts.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.Header.Get("X-Wpgo-Cache")
	}
	get()
	if h := get(); h != "hit" {
		t.Fatalf("second request: X-Wpgo-Cache = %q, want hit", h)
	}
	restart := func() *cache.Cache { // a new process: the cache loads the persisted pages back
		return cache.New(srv.cacheConfig(), cache.Options{LoginCookie: auth.CookieName, Logger: slog.Default()})
	}

	// Same version: the persisted pages survive the restart.
	c := restart()
	srv.dropCacheOfOtherCore(c)
	if n := c.PurgeAll(); n == 0 {
		t.Fatal("a restart on the same version dropped the persisted pages")
	}
	get()
	get() // fill the cache (and the disk) again

	old := Version
	Version = "99.0.0"
	t.Cleanup(func() { Version = old })
	c = restart()
	srv.dropCacheOfOtherCore(c)
	if n := c.PurgeAll(); n != 0 {
		t.Fatalf("%d pages of the previous core version survived the update", n)
	}
	if got := srv.settings.String(cacheCoreVersionOption); got != "99.0.0" {
		t.Fatalf("recorded core version = %q after the update", got)
	}
}

// cacheNonce mints a nonce for the logged-in test client.
func cacheNonce(t *testing.T, srv *Server, c *http.Client, base, action string) string {
	t.Helper()
	u, _ := url.Parse(base)
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == auth.CookieName {
			sess, err := srv.auth.Lookup(context.Background(), ck.Value)
			if err != nil {
				t.Fatal(err)
			}
			return srv.auth.Nonce(action, sess.UserID, sess.Token)
		}
	}
	t.Fatal("no session cookie")
	return ""
}

// Every settings option reaches the engine configuration (a saved option that is not wired up is
// the regression this guards against), and the admin actions purge what they promise.
func TestCacheConfigAndAdminActions(t *testing.T) {
	ts, c, srv := newTestServer(t, false)
	ctx := context.Background()
	installAndLogin(t, ts, c)
	if err := srv.settings.Set(ctx, map[string]any{
		"wpgo_cache_enabled": true, "wpgo_cache_serve": true, "wpgo_cache_ttl_public": 111, "wpgo_cache_ttl_front": 222,
		"wpgo_cache_ttl_feed": 333, "wpgo_cache_ttl_404": 44, "wpgo_cache_mobile": true, "wpgo_cache_logged_in": true,
		"wpgo_cache_exclude_uri": "/skip-me\n/and-me", "wpgo_cache_drop_qs": "zz_track", "wpgo_cache_query_strings": "keepme",
		"wpgo_cache_exclude_cookies": "ck_x", "wpgo_cache_exclude_ua": "BadBot", "wpgo_cache_browser_ttl": 55,
		"wpgo_cache_redirect_ttl": 66, "wpgo_cache_max_mb": 3, "wpgo_cache_server": "nginx",
		"wpgo_opt_lazy": true, "wpgo_opt_lazy_skip": 2, "wpgo_opt_minify_html": true, "wpgo_opt_defer_all": true,
		"wpgo_opt_defer_exclude": "jquery", "wpgo_opt_delay_list": "chat.js", "wpgo_opt_delay_timeout": 7,
		"wpgo_opt_minify_css": true, "wpgo_opt_preconnect": "https://a.example", "wpgo_opt_dns_prefetch": "//b.example",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := srv.cacheConfig()
	has := func(list []string, v string) bool {
		for _, x := range list {
			if x == v {
				return true
			}
		}
		return false
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{"enabled", cfg.Enabled}, {"ttl public", cfg.PublicTTL == 111*time.Second}, {"ttl front", cfg.FrontPageTTL == 222*time.Second},
		{"ttl feed", cfg.FeedTTL == 333*time.Second}, {"ttl 404", cfg.NotFoundTTL == 44*time.Second},
		{"mobile", cfg.CacheMobileSeparately}, {"logged in", cfg.CacheLoggedIn},
		{"exclude uri", has(cfg.ExcludePaths, "/skip-me") && has(cfg.ExcludePaths, "/and-me")},
		{"drop qs", has(cfg.DropQueryStrings, "zz_track")}, {"query strings", has(cfg.CacheQueryStrings, "keepme")},
		{"exclude cookies", has(cfg.ExcludeCookies, "ck_x")}, {"exclude ua", has(cfg.ExcludeUserAgents, "BadBot")},
		{"browser ttl", cfg.BrowserCacheTTL == 55*time.Second}, {"redirect ttl", cfg.RedirectTTL == 66*time.Second},
		{"max mb", cfg.MaxBytes == 3<<20}, {"server hint", cfg.ServerHint == cache.HintNginx},
		{"lazy", cfg.Optimize.LazyImages && cfg.Optimize.LazySkipFirst == 2}, {"minify html", cfg.Optimize.CollapseWhitespace},
		{"defer all", cfg.Optimize.DeferAll && has(cfg.Optimize.DeferExclude, "jquery")},
		{"delay", has(cfg.Optimize.DelayScripts, "chat.js") && cfg.Optimize.DelayTimeout == 7},
		{"minify css", cfg.Optimize.MinifyInlineCSS},
		{"preconnect", has(cfg.Optimize.Preconnect, "https://a.example") && has(cfg.Optimize.DNSPrefetch, "//b.example")},
	}
	for _, ch := range checks {
		if !ch.ok {
			t.Errorf("cacheConfig: option %q does not reach the engine config", ch.name)
		}
	}
	// Serve off disables the engine even when the plugin is enabled.
	_ = srv.settings.Set(ctx, map[string]any{"wpgo_cache_serve": false})
	if srv.cacheConfig().Enabled {
		t.Error("cacheConfig: Enabled with wpgo_cache_serve off")
	}
	_ = srv.settings.Set(ctx, map[string]any{"wpgo_cache_serve": true, "wpgo_cache_mobile": false,
		"wpgo_cache_logged_in": false, "wpgo_cache_exclude_uri": "", "wpgo_cache_exclude_cookies": "", "wpgo_cache_server": ""})
	srv.configureCache()

	// Warm-up list: the home page, and no duplicates.
	urls := srv.warmupURLs(ctx)
	if len(urls) == 0 || !strings.HasSuffix(urls[0], "/") {
		t.Errorf("warmupURLs = %v, want the home page first", urls)
	}
	seen := map[string]bool{}
	for _, u := range urls {
		if seen[u] {
			t.Errorf("warmupURLs lists %s twice", u)
		}
		seen[u] = true
	}

	// purge_url resolves a relative URL against the site home; a bad nonce purges nothing.
	cl := &http.Client{Jar: c.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	anon := func(p string) string {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.Header.Get("X-Wpgo-Cache")
	}
	anon("/")
	if anon("/") != "hit" {
		t.Fatal("home page not cached")
	}
	post := func(form url.Values) (int, string) {
		resp, err := cl.PostForm(ts.URL+"/wp-admin/admin?page=wpgo-cache", form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode, resp.Header.Get("Location")
	}
	good := cacheNonce(t, srv, c, ts.URL, "wpgo-cache-purge")
	if code, _ := post(url.Values{"action": {"purge_url"}, "purge_url": {"/"}, "_wpnonce": {"bad"}}); code == http.StatusFound {
		t.Error("purge_url with a bad nonce was accepted")
	}
	if anon("/") != "hit" {
		t.Error("a purge with a bad nonce evicted the page")
	}
	_, loc := post(url.Values{"action": {"purge_url"}, "purge_url": {"/"}, "_wpnonce": {good}})
	if !strings.Contains(loc, "wpgo-cache-msg=") || !strings.Contains(loc, "Purged") {
		t.Errorf("purge_url redirect = %q, want a Purged message", loc)
	}
	if anon("/") == "hit" {
		t.Error("purge_url with a relative URL did not evict the home page")
	}
	anon("/")
	post(url.Values{"action": {"purge_all"}, "_wpnonce": {good}})
	if anon("/") == "hit" {
		t.Error("purge_all did not evict the home page")
	}
}
