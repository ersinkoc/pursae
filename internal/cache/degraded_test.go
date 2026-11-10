package cache

import (
	"net/http"
	"testing"
)

func TestDegradedResponseIsNotStored(t *testing.T) {
	c := New(func() Config { cfg := DefaultConfig(); cfg.ServerHint = HintNone; return cfg }(), Options{})
	degrade := true
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if degrade {
			MarkDegraded(r.Context())
		}
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		_, _ = w.Write([]byte("<html>page</html>"))
	}))
	if s := status(do(h, "GET", "http://example.com/x/")); s == "hit" {
		t.Fatalf("first = %q", s)
	}
	if s := status(do(h, "GET", "http://example.com/x/")); s == "hit" {
		t.Fatal("a degraded render was served from the cache")
	}
	degrade = false
	do(h, "GET", "http://example.com/x/")
	if s := status(do(h, "GET", "http://example.com/x/")); s != "hit" {
		t.Fatalf("a complete render must be cached, got %q", s)
	}
}
