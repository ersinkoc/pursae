package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"wpgo/internal/auth"
)

// The heartbeat (action in the POST body, as heartbeat.js sends it) is no activity: it must not
// refresh last_seen_at, or the idle timeout never fires while an editor tab stays open.
// Integration test.
func TestHeartbeatDoesNotRefreshSession(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	old := time.Now().Add(-5 * time.Minute)
	if _, err := getPool(t).Exec(ctx, `UPDATE sessions SET last_seen_at = $1`, old); err != nil {
		t.Fatal(err)
	}
	seen := func() time.Time {
		var ls time.Time
		if err := getPool(t).QueryRow(ctx, `SELECT max(last_seen_at) FROM sessions`).Scan(&ls); err != nil {
			t.Fatal(err)
		}
		return ls
	}
	if code, _, _ := post(t, c, ts.URL+"/wp-admin/admin-ajax", url.Values{"action": {"heartbeat"}, "interval": {"10"}}); code != http.StatusOK {
		t.Fatalf("heartbeat: %d", code)
	}
	if got := seen(); got.Sub(old).Abs() > time.Second {
		t.Errorf("heartbeat refreshed last_seen_at to %v", got)
	}
	if code, _, _ := get(t, c, ts.URL+"/wp-admin/"); code != http.StatusOK {
		t.Fatalf("dashboard: %d", code)
	}
	if got := seen(); time.Since(got) > time.Minute {
		t.Errorf("a page load did not refresh last_seen_at (%v)", got)
	}
}

// The core per-login throttle keys on the account: failures recorded under the username also
// count when the same account is tried by its e-mail address. Integration test.
func TestCoreLoginThrottleSharesUsernameAndEmail(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	for i := 0; i < auth.MaxFailedLoginsPerLogin; i++ {
		if _, err := getPool(t).Exec(ctx, `INSERT INTO login_attempts(ip, login, success, pending) VALUES ($1, 'admin', false, false)`, fmt.Sprintf("203.0.113.%d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	anon := asNewClient()
	_, body, _ := post(t, anon, ts.URL+"/wp-login", url.Values{"log": {"ADMIN@example.test"}, "pwd": {"wrong"}})
	if !strings.Contains(body, "Too many failed login attempts") {
		t.Error("the e-mail address has its own per-login budget")
	}
	var n int
	_ = getPool(t).QueryRow(ctx, `SELECT count(*) FROM login_attempts WHERE login <> 'admin'`).Scan(&n)
	if n != 0 {
		t.Errorf("%d attempts stored under a non-canonical key", n)
	}
}
