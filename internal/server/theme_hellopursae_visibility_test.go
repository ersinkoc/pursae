package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"wpgo/internal/webbuilder"
)

// helloWithBuilderOff puts a site on Hello Pursae with Pursae Web Builder off, as a site whose owner
// kept the theme after deactivating the plugin (the theme cannot be chosen while the plugin is off).
func helloWithBuilderOff(t *testing.T, srv *Server) {
	t.Helper()
	ctx := context.Background()
	if err := srv.settings.Set(ctx, map[string]any{webbuilder.PluginOption: true}); err != nil {
		t.Fatal(err)
	}
	if err := srv.switchTheme(ctx, wbHelloSlug); err != nil {
		t.Fatal(err)
	}
	if err := srv.settings.Set(ctx, map[string]any{webbuilder.PluginOption: false}); err != nil {
		t.Fatal(err)
	}
}

// Hello Pursae comes with Pursae Web Builder: while the plugin is off the Themes screen does not list
// it and it cannot be activated; with the plugin on it is listed (and active); a site left on it after
// deactivation keeps seeing it.
func TestHelloPursaeHiddenWithoutBuilder(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	card := `data-slug="` + wbHelloSlug + `"`

	_, body, _ := get(t, c, base+"/wp-admin/themes")
	if strings.Contains(body, card) {
		t.Error("Themes lists Hello Pursae while Pursae Web Builder is off")
	}
	if srv.installedTheme(wbHelloSlug) != nil {
		t.Error("Hello Pursae can be activated while Pursae Web Builder is off")
	}
	if code, _, _ := get(t, c, base+"/?theme="+wbHelloSlug); code == http.StatusOK && srv.activeTheme().Slug == wbHelloSlug {
		t.Error("Hello Pursae became active")
	}

	if err := srv.wbSetActive(ctx, true); err != nil {
		t.Fatal(err)
	}
	_, body, _ = get(t, c, base+"/wp-admin/themes")
	if !strings.Contains(body, card) || srv.activeTheme().Slug != wbHelloSlug {
		t.Error("Hello Pursae is not listed and active with Pursae Web Builder on")
	}

	// The owner keeps Hello Pursae themselves: deactivation leaves it active, and it stays listed.
	if err := srv.settings.Set(ctx, map[string]any{wbPreviousThemeOption: nil}); err != nil {
		t.Fatal(err)
	}
	if err := srv.wbSetActive(ctx, false); err != nil {
		t.Fatal(err)
	}
	if srv.activeTheme().Slug != wbHelloSlug {
		t.Fatalf("active theme %q, want Hello Pursae kept", srv.activeTheme().Slug)
	}
	if _, body, _ = get(t, c, base+"/wp-admin/themes"); !strings.Contains(body, card) {
		t.Error("the active Hello Pursae is hidden after deactivation")
	}
}
