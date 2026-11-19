package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"wpgo/internal/cache"
)

func TestPageCacheRebuildRejectsWriteFromRoutedRequest(t *testing.T) {
	_, _, srv := newTestServer(t, false)
	ctx := context.Background()
	if err := srv.settings.Set(ctx, map[string]any{
		"wpgo_cache_enabled": true, "wpgo_cache_serve": true, "wpgo_cache_disk": true,
	}); err != nil {
		t.Fatal(err)
	}
	srv.configureCache()
	old := srv.currentCache()
	handler := old.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "response from the routed request")
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.test/after-rebuild/", nil)
	routed, release := make(chan struct{}), make(chan struct{})
	resume := sync.OnceFunc(func() { close(release) })
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		close(routed)
		<-release
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		done <- w
		close(done)
	}()
	t.Cleanup(func() {
		resume()
		<-done
	})
	<-routed
	if err := srv.settings.Set(ctx, map[string]any{"wpgo_cache_ttl_public": 120}); err != nil {
		t.Fatal(err)
	}
	srv.configureCache()
	resume()
	if w := <-done; w.Code != http.StatusOK || w.Body.String() != "response from the routed request" {
		t.Fatalf("request did not complete: status=%d body=%q", w.Code, w.Body.String())
	}
	restarted := cache.New(srv.cacheConfig(), cache.Options{CSPMode: srv.cspCacheState})
	if _, ok := restarted.Store().Get(restarted.Key(req)); ok {
		t.Fatal("request routed to the previous cache published to the replacement's disk directory")
	}
}
