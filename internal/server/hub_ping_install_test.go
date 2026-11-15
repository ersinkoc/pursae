package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"wpgo/internal/store"
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

	// The install screen offers update e-mails as an unticked box.
	if _, body, _ := get(t, c, ts.URL+"/wp-admin/install?language=en_US"); !strings.Contains(body, `id="pursae_update_emails"`) ||
		strings.Contains(body, `id="pursae_update_emails" aria-describedby="pursae-update-emails-desc" value="1" checked`) {
		t.Fatalf("install screen has no unticked Pursae updates box")
	}
	installAndLogin(t, ts, c) // the box is not ticked
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
	// Update e-mails are opt-in: the owner did not tick the box, so no one is reported (an empty
	// list, not absent) and the profile switch is off.
	if contacts, ok := pings[0]["contacts"].([]any); !ok || len(contacts) != 0 {
		t.Fatalf("contacts after an install without the box: %v", pings[0]["contacts"])
	}
	if _, body, _ := get(t, c, ts.URL+"/wp-admin/profile"); !strings.Contains(body, `id="pursae_update_emails" value="1" />`) {
		t.Fatalf("profile has no unticked Pursae updates switch")
	}
	b, _ := json.Marshal(srv.hubPingBody(ctx))
	if !strings.Contains(string(b), `"contacts":[]`) {
		t.Fatalf("ping without opt-in: %s", b)
	}
}

// Ticking the box on the install screen opts the owner in and records the time of consent.
func TestUpdateEmailsInstallOptIn(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	ctx := context.Background()
	if code, body, _ := post(t, c, ts.URL+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Opt-in Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"},
		"admin_email": {"admin@example.test"}, "pursae_update_emails": {"1"},
	}); code != http.StatusOK || !strings.Contains(body, "Success!") {
		t.Fatalf("install: %d", code)
	}
	got := srv.hubContacts(ctx)
	if len(got) != 1 || got[0].Email != "admin@example.test" || got[0].Role != "administrator" {
		t.Fatalf("contacts after opting in on the install screen: %+v", got)
	}
	var at string
	if err := srv.store.Pool.QueryRow(ctx, `SELECT coalesce(meta->>'pursae_update_emails_consent_at', '') FROM users WHERE login = 'admin'`).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339, at); err != nil {
		t.Fatalf("consent time %q: %v", at, err)
	}
	// A validation error keeps the box as the owner left it.
	ts2, c2, _ := testServerBuilderOff(t)
	if _, body, _ := post(t, c2, ts2.URL+"/wp-admin/install?step=2", url.Values{
		"user_name": {"admin"}, "admin_password": {"x"}, "admin_email": {""}, "pursae_update_emails": {"1"},
	}); !strings.Contains(body, `value="1" checked="checked"`) || !strings.Contains(body, "You must provide an email address.") {
		t.Fatalf("install form lost the Pursae updates box after a validation error")
	}
}

// A user who works in the admin is invited once, on their first admin screen, to opt in; the notice
// never opts them in. A user who never opened the admin is not reported either.
func TestUpdateEmailsNoticeOnce(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	srv.cfg.HubURL = "http://127.0.0.1:1" // a Manager is configured (never reached here)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	// A site from before opt-in: the old default stored true with no consent time. Not consent.
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE users SET meta = (meta - 'pursae_update_emails_consent_at') || '{"pursae_update_emails": true}'`); err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(srv.hubContacts(ctx)); string(b) != "[]" {
		t.Fatalf("contacts before the notice: %s", b)
	}
	_, body, _ := get(t, c, ts.URL+"/wp-admin/")
	if !strings.Contains(body, "Want Pursae product update e-mails") || !strings.Contains(body, `href="/wp-admin/profile#pursae-update-emails"`) {
		t.Fatalf("no opt-in invitation on the first admin screen")
	}
	if _, body, _ = get(t, c, ts.URL+"/wp-admin/"); strings.Contains(body, "Want Pursae product update e-mails") {
		t.Fatalf("opt-in invitation shown twice")
	}
	if b, _ := json.Marshal(srv.hubContacts(ctx)); string(b) != "[]" {
		t.Fatalf("contacts after the notice: %s", b)
	}
	var v string
	srv.store.Pool.QueryRow(ctx, `SELECT meta->>'pursae_update_emails' FROM users WHERE login = 'admin'`).Scan(&v)
	if v != "false" {
		t.Fatalf("switch after the notice: %q, want false", v)
	}
}

// Only the user can opt in, in their own profile (recording the time of consent); an administrator
// editing someone else can turn the e-mails off but not on.
func TestUpdateEmailsProfileConsent(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	consentAt := func(id int64) string {
		var at string
		srv.store.Pool.QueryRow(ctx, `SELECT coalesce(meta->>'pursae_update_emails_consent_at', '') FROM users WHERE id = $1`, id).Scan(&at)
		return at
	}
	emails := func() (out []string) {
		for _, h := range srv.hubContacts(ctx) {
			out = append(out, h.Email)
		}
		return out
	}

	// Self opt-in.
	_, body, _ := get(t, c, ts.URL+"/wp-admin/profile")
	form := url.Values{"_wpnonce": {formNonce(t, body)}, "action": {"update"}, "nickname": {"admin"}, "email": {"admin@example.test"},
		"pursae_update_emails_shown": {"1"}, "pursae_update_emails": {"1"}}
	if code, _, _ := post(t, c, ts.URL+"/wp-admin/profile", form); code != http.StatusFound {
		t.Fatalf("save profile: %d", code)
	}
	admin, err := srv.store.UserByLoginOrEmail(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339, consentAt(admin.ID)); err != nil {
		t.Fatalf("no consent time after opting in: %v", err)
	}
	if got := emails(); len(got) != 1 || got[0] != "admin@example.test" {
		t.Fatalf("contacts after self opt-in: %v", got)
	}

	// An administrator cannot opt another user in.
	uid, err := srv.store.CreateUser(ctx, store.NewUser{Login: "writer", Email: "writer@example.test", PasswordHash: "x", Role: "author"})
	if err != nil {
		t.Fatal(err)
	}
	editURL := ts.URL + "/wp-admin/user-edit?user_id=" + itoa64(uid)
	_, body, _ = get(t, c, editURL)
	if !strings.Contains(body, `id="pursae_update_emails" value="1" disabled="disabled"`) {
		t.Fatalf("another user's Pursae updates switch is not read-only")
	}
	other := url.Values{"_wpnonce": {formNonce(t, body)}, "user_id": {itoa64(uid)}, "action": {"update"}, "nickname": {"writer"},
		"email": {"writer@example.test"}, "pursae_update_emails_shown": {"1"}, "pursae_update_emails": {"1"}}
	if code, _, _ := post(t, c, editURL, other); code != http.StatusFound {
		t.Fatalf("save user-edit: %d", code)
	}
	if at := consentAt(uid); at != "" {
		t.Fatalf("an administrator opted another user in: consent at %q", at)
	}
	if got := emails(); len(got) != 1 {
		t.Fatalf("contacts after an administrator tried to opt another user in: %v", got)
	}

	// The user opts in themselves; an administrator can still turn it off for them.
	if err := srv.setUpdateEmails(ctx, &store.User{ID: uid}, true); err != nil {
		t.Fatal(err)
	}
	if got := emails(); len(got) != 2 {
		t.Fatalf("contacts after the writer opted in: %v", got)
	}
	_, body, _ = get(t, c, editURL)
	other.Set("_wpnonce", formNonce(t, body))
	other.Del("pursae_update_emails")
	if code, _, _ := post(t, c, editURL, other); code != http.StatusFound {
		t.Fatalf("save user-edit: %d", code)
	}
	if at := consentAt(uid); at != "" {
		t.Fatalf("consent time kept after opting out: %q", at)
	}
	if got := emails(); len(got) != 1 || got[0] != "admin@example.test" {
		t.Fatalf("contacts after an administrator opted the writer out: %v", got)
	}
}
