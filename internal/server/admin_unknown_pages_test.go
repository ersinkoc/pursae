package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Unregistered admin?page= screens and unknown plugin tabs never render a placeholder screen:
// an unknown page is admin.php's 403 die, an unknown Pursae SMTP tab opens the first tab.
func TestUnknownPluginAdminPages(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if err := srv.settings.Set(ctx, map[string]any{"wpgo_cache_enabled": true, msSetting: true}); err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{"no-such-plugin", "wpgo-cache-bogus", "wp-mail-smtp-about"} {
		code, body, _ := get(t, c, base+"/wp-admin/admin?page="+page)
		if code != http.StatusForbidden || !strings.Contains(body, "Sorry, you are not allowed to access this page.") {
			t.Errorf("admin?page=%s: %d", page, code)
		}
	}
	for _, page := range []string{"wpgo-cache", "wpgo-cache-settings", "wpgo-cache-optimize", "wpgo-cache-toolbox"} {
		if code, body, _ := get(t, c, base+"/wp-admin/admin?page="+page); code != http.StatusOK || strings.Contains(body, "not available yet") {
			t.Errorf("admin?page=%s: %d", page, code)
		}
	}
	for u, want := range map[string]string{
		"/wp-admin/admin?page=wp-mail-smtp&tab=nope":       "/wp-admin/admin?page=wp-mail-smtp",
		"/wp-admin/admin?page=wp-mail-smtp-tools&tab=nope": "/wp-admin/admin?page=wp-mail-smtp-tools",
	} {
		if code, _, loc := get(t, c, base+u); code != http.StatusFound || loc != want {
			t.Errorf("%s: %d → %q, want %q", u, code, loc, want)
		}
	}
	if code, body, _ := get(t, c, base+"/wp-admin/admin?page=wp-mail-smtp&tab=misc"); code != http.StatusOK || strings.Contains(body, "not available yet") {
		t.Errorf("Pursae SMTP Misc tab: %d", code)
	}
}
