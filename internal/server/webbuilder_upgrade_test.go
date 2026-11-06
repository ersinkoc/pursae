package server

import (
	"context"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/webbuilder"
)

// Integration tests of the one-time upgrade from the old builder (need the test database).

// wbuRegisterTheme puts a minimal theme in the process-wide registry for one test.
func wbuRegisterTheme(t *testing.T, th *Theme) {
	t.Helper()
	registerTheme(th)
	t.Cleanup(func() {
		themesMu.Lock()
		delete(themes, th.Slug)
		themesMu.Unlock()
	})
}

// wbuReset makes the upgrade run again on the next call, with the given options.
func wbuReset(t *testing.T, srv *Server, values map[string]any) {
	t.Helper()
	v := map[string]any{wbUpgradedOption: false, wbUpgradeNoticeOption: nil}
	for k, x := range values {
		v[k] = x
	}
	if err := srv.settings.Set(context.Background(), v); err != nil {
		t.Fatal(err)
	}
}

func TestWebBuilderUpgrade(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()

	// newServer ran before the install; the first start of an installed site without the old
	// builder marks the upgrade done and shows nothing.
	wbuReset(t, srv, nil)
	srv.wbUpgrade(ctx)
	if !srv.settings.Bool(wbUpgradedOption) || srv.settings.Get(wbUpgradeNoticeOption) != nil || srv.settings.String("stylesheet") != defaultTheme {
		t.Fatalf("fresh site: upgraded=%v notice=%v theme=%s", srv.settings.Bool(wbUpgradedOption), srv.settings.Get(wbUpgradeNoticeOption), srv.settings.String("stylesheet"))
	}
	if _, body, _ := get(t, c, base+"/wp-admin/"); strings.Contains(body, "was replaced by the new builder") {
		t.Error("notice on a site that never used the old builder")
	}

	// Hello Web Builder active, the theme before it still installed: back to that theme.
	wbuRegisterTheme(t, &Theme{Slug: "wbu-previous", Name: "WBU Previous", Version: "1.0"})
	wbuReset(t, srv, map[string]any{"stylesheet": "hello-web-builder", "template": "hello-web-builder",
		wbPrevThemeOption: "wbu-previous", webbuilder.PluginOption: true})
	srv.wbUpgrade(ctx)
	if got := srv.settings.String("stylesheet"); got != "wbu-previous" {
		t.Fatalf("theme after the upgrade: %s, want wbu-previous", got)
	}
	if srv.settings.String(wbPrevThemeOption) != "" || !srv.settings.Bool(wbUpgradedOption) {
		t.Error("the upgrade did not clean up its options")
	}
	if !srv.wbOn() {
		t.Error("the upgrade switched the plugin off")
	}

	// Runs once: a later start leaves the theme alone.
	if err := srv.settings.Set(ctx, map[string]any{"stylesheet": "hello-web-builder"}); err != nil {
		t.Fatal(err)
	}
	srv.wbUpgrade(ctx)
	if got := srv.settings.String("stylesheet"); got != "hello-web-builder" {
		t.Errorf("the upgrade ran twice: theme %s", got)
	}
	_ = srv.settings.Set(ctx, map[string]any{"stylesheet": "wbu-previous", "template": "wbu-previous"})

	// The notice: on the Dashboard and the Themes screen, for admins, with the theme's name.
	_, body, _ := get(t, c, base+"/wp-admin/")
	if !strings.Contains(body, "Pursae Web Builder was replaced by the new builder.") || !strings.Contains(body, "the theme was switched to <strong>WBU Previous</strong>.") {
		t.Fatalf("no upgrade notice on the Dashboard")
	}
	if _, b, _ := get(t, c, base+"/wp-admin/themes"); !strings.Contains(b, "was replaced by the new builder") {
		t.Error("no upgrade notice on the Themes screen")
	}
	if _, b, _ := get(t, c, base+"/wp-admin/plugins"); strings.Contains(b, "was replaced by the new builder") {
		t.Error("upgrade notice on the Plugins screen")
	}
	asCreateUser(t, srv, "ed", "editor", asStrongPass)
	ec := asNewClient()
	if code, _, _ := asLogin(t, ec, base, "/wp-login", "ed", asStrongPass); code != http.StatusFound {
		t.Fatalf("editor login: %d", code)
	}
	if _, b, _ := get(t, ec, base+"/wp-admin/"); strings.Contains(b, "was replaced by the new builder") {
		t.Error("upgrade notice for an editor")
	}

	// Dismiss: a bad nonce is refused, the link clears the notice and returns to the screen.
	m := regexp.MustCompile(`href="([^"]+)" id="web-builder-upgrade-dismiss"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Dismiss link")
	}
	dismiss := html.UnescapeString(m[1])
	if code, _, _ := get(t, c, base+strings.Replace(dismiss, "_wpnonce=", "_wpnonce=x", 1)); code != http.StatusForbidden {
		t.Errorf("dismiss with a bad nonce: %d, want 403", code)
	}
	if code, _, loc := get(t, c, base+dismiss); code != http.StatusFound || loc != "/wp-admin/" {
		t.Fatalf("dismiss: %d %s", code, loc)
	}
	if _, b, _ := get(t, c, base+"/wp-admin/"); strings.Contains(b, "was replaced by the new builder") {
		t.Error("the notice is back after Dismiss")
	}

	// The previous theme is gone: the default theme. The plugin being off does not matter.
	wbuReset(t, srv, map[string]any{"stylesheet": "hello-web-builder", "template": "hello-web-builder",
		wbPrevThemeOption: "no-such-theme", webbuilder.PluginOption: false})
	srv.wbUpgrade(ctx)
	if got := srv.settings.String("stylesheet"); got != defaultTheme {
		t.Errorf("theme with a missing previous theme: %s, want %s", got, defaultTheme)
	}
	_, body, _ = get(t, c, base+"/wp-admin/themes")
	if !strings.Contains(body, "the theme was switched to <strong>Twenty Twenty-Five</strong>.") {
		t.Error("no notice naming the default theme")
	}
	m = regexp.MustCompile(`href="([^"]+)" id="web-builder-upgrade-dismiss"`).FindStringSubmatch(body)
	if m == nil || !strings.Contains(m[1], "from=themes") {
		t.Fatal("no Dismiss link back to Themes")
	}
	if code, _, loc := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound || loc != "/wp-admin/themes" {
		t.Errorf("dismiss from Themes: %d %s", code, loc)
	}

	// A theme saved with the old builder (themes.custom) and a design package also move; the
	// previous theme is not used when it is itself a builder theme.
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO themes (slug, version, status, custom) VALUES ('wbu-saved', '1.0', 'active', true)
		ON CONFLICT (slug) DO UPDATE SET custom = true`); err != nil {
		t.Fatal(err)
	}
	wbuReset(t, srv, map[string]any{"stylesheet": "wbu-saved", "template": "wbu-saved", wbPrevThemeOption: "hello-web-builder", webbuilder.PluginOption: true})
	srv.wbUpgrade(ctx)
	if got := srv.settings.String("stylesheet"); got != defaultTheme {
		t.Errorf("saved theme: now %s, want %s", got, defaultTheme)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "golementor"), 0o755); err != nil {
		t.Fatal(err)
	}
	wbuRegisterTheme(t, &Theme{Slug: "wbu-design", Name: "WBU Design", Version: "1.0", Package: &themePackage{Dir: dir}})
	if !srv.wbOldBuilderTheme(ctx, "wbu-design") || srv.wbOldBuilderTheme(ctx, "wbu-previous") || srv.wbOldBuilderTheme(ctx, defaultTheme) ||
		!srv.wbOldBuilderTheme(ctx, "hello-gowebeditor") || srv.wbOldBuilderTheme(ctx, "") {
		t.Error("wbOldBuilderTheme")
	}
	wbuReset(t, srv, map[string]any{"stylesheet": "wbu-design", "template": "wbu-design", wbPrevThemeOption: "wbu-previous"})
	srv.wbUpgrade(ctx)
	if got := srv.settings.String("stylesheet"); got != "wbu-previous" {
		t.Errorf("design package: now %s, want wbu-previous", got)
	}

	// An active theme that is not installed at all (its files are gone) moves to the default theme
	// with the notice, instead of staying active with the upgrade marked done.
	for _, missing := range []string{"wbu-missing-theme", "hello-web-builder"} {
		wbuReset(t, srv, map[string]any{"stylesheet": missing, "template": missing, wbPrevThemeOption: "", webbuilder.PluginOption: false})
		srv.wbUpgrade(ctx)
		if got := srv.settings.String("stylesheet"); got != defaultTheme || !srv.settings.Bool(wbUpgradedOption) {
			t.Errorf("missing theme %s: now %s, want %s", missing, got, defaultTheme)
		}
		if m, _ := srv.settings.Get(wbUpgradeNoticeOption).(map[string]any); m == nil || m["theme"] != "Twenty Twenty-Five" {
			t.Errorf("missing theme %s: notice %v", missing, srv.settings.Get(wbUpgradeNoticeOption))
		}
	}

	// An ordinary theme is left alone, and a site that had the plugin on still gets the notice.
	wbuReset(t, srv, map[string]any{"stylesheet": "wbu-previous", "template": "wbu-previous", webbuilder.PluginOption: true})
	srv.wbUpgrade(ctx)
	if got := srv.settings.String("stylesheet"); got != "wbu-previous" {
		t.Errorf("ordinary theme switched to %s", got)
	}
	_, body, _ = get(t, c, base+"/wp-admin/")
	if !strings.Contains(body, "those pages now show their text content in the theme.") || strings.Contains(body, "the theme was switched") {
		t.Error("notice without a theme switch")
	}
}
