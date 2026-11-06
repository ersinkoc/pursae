package server

import (
	"context"
	"strings"
	"testing"

	"wpgo/internal/webbuilder"
)

// On the site, the toolbar shows Customize to users who can change the theme, as WordPress does:
// the link is .hide-if-no-customize, made visible by the customize-support body class script.
func TestFrontToolbarCustomize(t *testing.T) {
	ts, c := testServer(t)
	installAndLogin(t, ts, c)
	_, body, _ := get(t, c, ts.URL+"/")
	for _, want := range []string{`id='wp-admin-bar-customize'`, `href='/wp-admin/customize'`, `' customize-support'`, "no-customize-support"} {
		if !strings.Contains(body, want) {
			t.Errorf("front toolbar lacks %q", want)
		}
	}
}

// The site's toolbar offers "Edit with Pursae Web Builder" on a page built with it (a builder
// document), not on other pages or listings, and nothing while the plugin is off. The old
// builder's node (Theme Builder) is gone.
func TestFrontToolbarWebBuilder(t *testing.T) {
	srv, base, c := wbfStart(t)
	built := wbfPage(t, srv, "page", "Toolbar Built", []string{}, "", "")
	_, body, _ := get(t, c, wbfURL(base, built))
	if !strings.Contains(body, "Edit with Pursae Web Builder") || !strings.Contains(body, "action=web-builder'") || !strings.Contains(body, "ab-icon dashicons-layout") {
		t.Error("a builder page's toolbar lacks Edit with Pursae Web Builder")
	}
	for _, u := range []string{base + "/sample-page/", base + "/?s=hello"} {
		if _, body, _ = get(t, c, u); strings.Contains(body, "web_builder_edit_page") || strings.Contains(body, "/wp-admin/theme-builder") {
			t.Errorf("%s: the toolbar offers the builder", u)
		}
	}
	if err := srv.settings.Set(context.Background(), map[string]any{webbuilder.PluginOption: false}); err != nil {
		t.Fatal(err)
	}
	wbfPurge(srv)
	if _, body, _ = get(t, c, wbfURL(base, built)); strings.Contains(body, "web_builder_edit_page") {
		t.Error("the toolbar shows Pursae Web Builder while it is off")
	}
}
