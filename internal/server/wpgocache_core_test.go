package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

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
