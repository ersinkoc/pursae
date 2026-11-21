package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wpgo/internal/coreupdate"
)

// pendingUpdate is an executable dir/pursae updated from 1.0.0 ("old") to 1.0.1 ("new"), not yet
// confirmed.
func pendingUpdate(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "pursae")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := coreupdate.Prepare(exe, "1.0.0", "1.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(exe); err != nil { // Prepare hard-links the old binary to .prev
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

// An update is confirmed by /readyz, not by the login page: a binary whose readiness probe keeps
// failing (migrations pending, schema guard) is rolled back even though other pages answer.
func TestAwaitReadyUsesReadyz(t *testing.T) {
	healthURLFor := func(ready bool) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/readyz" && !ready {
				http.Error(w, "migrations pending", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK) // /wp-login and every other page answer
		}))
		t.Cleanup(srv.Close)
		u, err := healthURL(strings.TrimPrefix(srv.URL, "http://"), "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	ctx := context.Background()

	exe := pendingUpdate(t)
	if awaitReady(ctx, exe, healthURLFor(true), "1.0.1", nil, 10*time.Second) {
		t.Fatal("a ready binary was rolled back")
	}
	if st := coreupdate.Read(exe); st == nil || st.Status != "confirmed" {
		t.Fatalf("not confirmed: %+v", st)
	}

	exe = pendingUpdate(t)
	if !awaitReady(ctx, exe, healthURLFor(false), "1.0.1", nil, 500*time.Millisecond) {
		t.Fatal("a binary that never became ready was not rolled back")
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatalf("the previous binary was not restored: %q", b)
	}
	if st := coreupdate.Read(exe); st == nil || st.Status != "rolled_back" {
		t.Fatalf("rollback not recorded: %+v", st)
	}

	// Shutting down during the window is not a failed update.
	exe = pendingUpdate(t)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if awaitReady(cctx, exe, healthURLFor(false), "1.0.1", nil, time.Second) {
		t.Fatal("rolled back while shutting down")
	}
	if st := coreupdate.Read(exe); st == nil || st.Status != "pending" {
		t.Fatalf("state changed while shutting down: %+v", st)
	}
}
