package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHealthURL(t *testing.T) {
	for addr, want := range map[string]string{
		":8080":          "http://127.0.0.1:8080/healthz",
		"0.0.0.0:9000":   "http://127.0.0.1:9000/healthz",
		"[::]:80":        "http://127.0.0.1:80/healthz",
		"10.0.0.5:8080":  "http://10.0.0.5:8080/healthz",
		"localhost:8088": "http://localhost:8088/healthz",
	} {
		if got, err := healthURL(addr, "/healthz"); err != nil || got != want {
			t.Errorf("healthURL(%q) = %q, %v; want %q", addr, got, err, want)
		}
	}
	if _, err := healthURL("8080", "/healthz"); err == nil {
		t.Error("an address without a port is an error")
	}
}

func TestHealthcheck(t *testing.T) {
	var ready atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" && !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	addr := strings.TrimPrefix(ts.URL, "http://")
	if err := healthcheck(addr, nil); err != nil {
		t.Fatalf("healthz: %v", err)
	}
	if err := healthcheck(addr, []string{"ready"}); err == nil {
		t.Fatal("readyz 503 should fail")
	}
	ready.Store(true)
	if err := healthcheck(addr, []string{"ready"}); err != nil {
		t.Fatalf("readyz: %v", err)
	}
	ts.Close()
	if err := healthcheck(addr, nil); err == nil {
		t.Fatal("a closed server should fail")
	}
}
