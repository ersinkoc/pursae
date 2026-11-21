package coreupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// WaitHealthy confirms an update only on 200 OK from the readiness probe: a 503 (migrations pending,
// schema guard) and a redirect are not ready.
func TestWaitHealthyNeeds200(t *testing.T) {
	var calls atomic.Int32
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 2 {
			http.Error(w, "migrations pending", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ready.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !WaitHealthy(ctx, ready.URL+"/readyz") || calls.Load() < 2 {
		t.Fatalf("not confirmed after the probe turned ready (%d calls)", calls.Load())
	}

	for name, code := range map[string]int{"503": http.StatusServiceUnavailable, "302": http.StatusFound, "404": http.StatusNotFound} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if code == http.StatusFound {
				http.Redirect(w, r, "/elsewhere", code)
				return
			}
			w.WriteHeader(code)
		}))
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		if WaitHealthy(ctx, srv.URL+"/readyz") {
			t.Errorf("%s counted as ready", name)
		}
		cancel()
		srv.Close()
	}
}
