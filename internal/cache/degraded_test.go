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

func TestLateDegradedResponseIsNotStored(t *testing.T) {
	for _, when := range []string{"after-header", "after-body"} {
		t.Run(when, func(t *testing.T) {
			c := New(DefaultConfig(), Options{})
			calls := 0
			h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusOK)
				if when == "after-header" {
					MarkDegraded(r.Context())
				}
				_, _ = w.Write([]byte("partial"))
				if when == "after-body" {
					MarkDegraded(r.Context())
				}
				_, _ = w.Write([]byte(" page"))
			}))
			for i := 0; i < 2; i++ {
				w := do(h, http.MethodGet, "http://example.com/x/")
				if w.Body.String() != "partial page" {
					t.Fatalf("response body = %q", w.Body.String())
				}
			}
			if calls != 2 || c.Stats().Entries != 0 {
				t.Fatalf("calls=%d cached entries=%d", calls, c.Stats().Entries)
			}
		})
	}
}
