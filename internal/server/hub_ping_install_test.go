package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
	// The installer was told on the install screen: their address is reported for update e-mails.
	contacts, _ := pings[0]["contacts"].([]any)
	if len(contacts) != 1 || contacts[0].(map[string]any)["email"] != "admin@example.test" || contacts[0].(map[string]any)["role"] != "administrator" {
		t.Fatalf("contacts after install: %v", pings[0]["contacts"])
	}
	// The profile has the switch; turned off, the next ping reports no one (an empty list, not absent).
	if _, body, _ := get(t, c, ts.URL+"/wp-admin/profile"); !strings.Contains(body, `id="pursae_update_emails" value="1" checked`) {
		t.Fatalf("profile has no checked Pursae updates switch")
	}
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE users SET meta = meta || '{"pursae_update_emails": false}'`); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(srv.hubPingBody(ctx))
	if !strings.Contains(string(b), `"contacts":[]`) {
		t.Fatalf("ping after opting out: %s", b)
	}
}

// A user who works in the admin is told once, on their first admin screen, and is reported from then
// on; a user who never opened the admin is not reported.
func TestUpdateEmailsNoticeOnce(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	srv.cfg.HubURL = "http://127.0.0.1:1" // a Manager is configured (never reached here)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE users SET meta = meta - 'pursae_update_emails'`); err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(srv.hubContacts(ctx)); string(b) != "[]" {
		t.Fatalf("contacts before the notice: %s", b)
	}
	_, body, _ := get(t, c, ts.URL+"/wp-admin/")
	if !strings.Contains(body, "to send you Pursae product updates") {
		t.Fatalf("no update e-mails notice on the first admin screen")
	}
	if _, body, _ = get(t, c, ts.URL+"/wp-admin/"); strings.Contains(body, "to send you Pursae product updates") {
		t.Fatalf("update e-mails notice shown twice")
	}
	if got := srv.hubContacts(ctx); len(got) != 1 {
		t.Fatalf("contacts after the notice: %+v", got)
	}
}
