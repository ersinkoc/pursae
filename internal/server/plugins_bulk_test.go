package server

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// Plugins › Bulk actions activate and deactivate several plugins at once, like plugins.php.
func TestPluginsBulkActions(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	if !strings.Contains(body, `id="bulk-action-selector-top"`) || !strings.Contains(body, `value="deactivate-selected"`) {
		t.Fatal("the Plugins screen has no bulk actions")
	}
	m := regexp.MustCompile(`id="bulk-action-form"[^>]*>\s*<input type="hidden" name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no bulk nonce")
	}
	send := func(action string, slugs ...string) string {
		t.Helper()
		form := url.Values{"_wpnonce": {m[1]}, "action": {action}, "action2": {"-1"}, "checked[]": slugs}
		code, _, loc := post(t, c, base+"/wp-admin/plugins", form)
		if code != http.StatusFound {
			t.Fatalf("%s: %d", action, code)
		}
		return loc
	}
	loc := send("activate-selected", gontactSlug, golementorSlug)
	if !strings.Contains(loc, "activate-multi=2") || !srv.gontactOn() || !srv.golementorOn() {
		t.Fatalf("bulk activate: %s", loc)
	}
	if _, b, _ := get(t, c, base+loc); !strings.Contains(b, "Selected plugins activated.") {
		t.Error("no bulk activation notice")
	}
	// Classic Editor and WPGo Cache start active; an already inactive plugin is left alone.
	loc = send("deactivate-selected", "classic-editor", "wpgo-cache", gontactSlug, golementorSlug, maintSlug)
	if !strings.Contains(loc, "deactivate-multi=4") || srv.settings.Bool("native_classic_editor") || srv.golementorOn() || srv.gontactOn() || srv.settings.Bool("wpgo_cache_enabled") {
		t.Fatalf("bulk deactivate: %s", loc)
	}
	// Built-in plugins cannot be deleted.
	if loc = send("delete-selected", "classic-editor"); !strings.Contains(loc, "skipped=Classic+Editor") {
		t.Errorf("bulk delete of a built-in plugin: %s", loc)
	}
	if code, _, _ := post(t, c, base+"/wp-admin/plugins", url.Values{"_wpnonce": {"bad"}, "action": {"activate-selected"}, "checked[]": {"classic-editor"}}); code == http.StatusFound {
		t.Error("bulk action accepted a bad nonce")
	}
}
