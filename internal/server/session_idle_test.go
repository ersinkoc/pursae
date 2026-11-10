package server

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"
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
