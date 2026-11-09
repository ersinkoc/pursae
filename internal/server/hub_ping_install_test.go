package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// A fresh site pings the Manager as soon as the installer has run. Before, the first check ran a
// minute after start with no site address, the Manager refused it (bad_url), the retries ran out
// while nobody had installed yet, and the site stayed off the Manager until the next daily check.
func TestHubPingAfterInstall(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	var mu sync.Mutex
	var pings []map[string]any
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/sites/ping" && r.Method == http.MethodPost {
			var p map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &p)
			mu.Lock()
			pings = append(pings, p)
			mu.Unlock()
			if u, _ := p["url"].(string); u == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer hub.Close()
	srv.cfg.HubURL = hub.URL
	ctx := context.Background()

	// Not installed: no address, so no ping and no error to retry.
	if err := srv.hubPing(ctx); err != nil {
		t.Fatalf("ping before install: %v", err)
	}
	if len(pings) != 0 {
		t.Fatalf("pinged before install: %v", pings)
	}

	installAndLogin(t, ts, c)
	var due int
	srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE name = 'wpgo_hub_check' AND recurrence = ''
		AND status = 'queued' AND run_at <= now()`).Scan(&due)
	if due != 1 {
		t.Fatalf("hub checks due right after install: %d, want 1", due)
	}
	srv.runDueJobs(ctx, "test", srv.jobHandlers())
	mu.Lock()
	defer mu.Unlock()
	if len(pings) != 1 || pings[0]["url"] != ts.URL {
		t.Fatalf("pings after install: %v", pings)
	}
	if got := srv.settings.String(hubReportedVersionOption); got != Version {
		t.Errorf("reported version %q, want %s", got, Version)
	}
}
