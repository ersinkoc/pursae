package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wpgo/internal/auth"
	"wpgo/internal/plugins"
)

// actionNonce mints a nonce for action for the logged-in test client.
func actionNonce(t *testing.T, srv *Server, c *http.Client, base, action string) string {
	t.Helper()
	u, _ := url.Parse(base)
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == auth.CookieName {
			sess, err := srv.auth.Lookup(context.Background(), ck.Value)
			if err != nil {
				t.Fatal(err)
			}
			return srv.auth.Nonce(action, sess.UserID, sess.Token)
		}
	}
	t.Fatal("no session cookie")
	return ""
}

// Deleting an installed Wasm plugin through the Plugins screen removes its row, package, options,
// transients and scheduled events, and leaves other plugins' data alone.
func TestPluginDeleteLeavesNoResidue(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	installDemo(t, srv)
	ctx := context.Background()
	h := &pluginHost{s: srv}
	_ = h.UpdateOption(ctx, "shortcode-demo", "shortcode_demo_secret", "k")
	_ = h.TransientSet(ctx, "shortcode-demo", "t", json.RawMessage(`1`), 0)
	_ = h.UpdateOption(ctx, "other-plugin", "other_plugin_opt", "keep")
	if err := h.ScheduleEvent(ctx, "shortcode-demo", plugins.ScheduledEvent{Hook: "cron_shortcode_demo_tick", Interval: 3600}); err != nil {
		t.Fatal(err)
	}
	// An inactive plugin's due event must be skipped without an error before the delete.
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE plugins SET status = 'inactive' WHERE slug = 'shortcode-demo'`)
	_ = srv.plugins.rt.Unload(ctx, "shortcode-demo")
	zipPath := filepath.Join(srv.plugins.dir, "shortcode-demo.zip")
	if _, err := os.Stat(zipPath); err != nil {
		t.Fatalf("package missing before delete: %v", err)
	}

	form := url.Values{"action": {"delete"}, "plugin": {"shortcode-demo"}, "_wpnonce": {actionNonce(t, srv, c, ts.URL, "delete-plugin_shortcode-demo")}}
	code, _, loc := post(t, c, ts.URL+"/wp-admin/plugins", form)
	if code != http.StatusFound || !strings.Contains(loc, "deleted=true") {
		t.Fatalf("delete: %d %s", code, loc)
	}
	count := func(q string, args ...any) int {
		var n int
		_ = srv.store.Pool.QueryRow(ctx, q, args...).Scan(&n)
		return n
	}
	if n := count(`SELECT count(*) FROM plugins WHERE slug = 'shortcode-demo'`); n != 0 {
		t.Errorf("plugins row left: %d", n)
	}
	if n := count(`SELECT count(*) FROM jobs WHERE payload->>'plugin' = 'shortcode-demo'`); n != 0 {
		t.Errorf("jobs left: %d", n)
	}
	if n := count(`SELECT count(*) FROM settings WHERE key LIKE $1`, pluginOptionKey("shortcode-demo", "%")); n != 0 {
		t.Errorf("options left: %d", n)
	}
	if n := count(`SELECT count(*) FROM transients WHERE key LIKE $1`, transientKey("shortcode-demo", "%")); n != 0 {
		t.Errorf("transients left: %d", n)
	}
	if n := count(`SELECT count(*) FROM plugin_data WHERE plugin = 'shortcode-demo'`); n != 0 {
		t.Errorf("plugin_data left: %d", n)
	}
	if _, err := os.Stat(zipPath); !os.IsNotExist(err) {
		t.Errorf("package file left: %v", err)
	}
	if v, _, _ := h.GetOption(ctx, "other-plugin", "other_plugin_opt"); v == nil {
		t.Error("another plugin's option was purged")
	}
}

// Reloading a replaced package that asks for more than was granted keeps only the granted
// permissions and reports the rest as missing.
func TestPluginReloadReportsMissingPermissions(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	installDemo(t, srv)
	ctx := context.Background()
	m, err := srv.installPackage(ctx, demoPackage(t))
	if err != nil {
		t.Fatal(err)
	}
	granted, _ := json.Marshal([]plugins.Permission{plugins.PermOptionsRead})
	if err := srv.reloadPlugin(ctx, "shortcode-demo", m, granted); err != nil {
		t.Fatal(err)
	}
	var perms, missing []byte
	_ = srv.store.Pool.QueryRow(ctx, `SELECT permissions, missing_permissions FROM plugins WHERE slug = 'shortcode-demo'`).Scan(&perms, &missing)
	var keep, miss []string
	_ = json.Unmarshal(perms, &keep)
	_ = json.Unmarshal(missing, &miss)
	if len(keep) != 1 || keep[0] != "options:read" {
		t.Errorf("granted permissions after reload: %v", keep)
	}
	if len(miss) != len(m.Permissions)-1 {
		t.Errorf("missing permissions after reload: %v (declared %d)", miss, len(m.Permissions))
	}
}
