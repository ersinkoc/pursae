package server

import (
	"strings"
	"testing"
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

// With Golementor active the site's toolbar offers "Edit with Golementor" on posts and pages and
// the Theme Builder everywhere; with it off, nothing.
func TestFrontToolbarGolementor(t *testing.T) {
	ts, c := testServer(t) // Golementor on
	installAndLogin(t, ts, c)
	_, body, _ := get(t, c, ts.URL+"/sample-page/")
	if !strings.Contains(body, "Edit with Pursae Web Builder") || !strings.Contains(body, "action=web-builder") || !strings.Contains(body, "/wp-admin/theme-builder") {
		t.Error("a page's toolbar lacks Edit with Pursae Web Builder")
	}
	_, body, _ = get(t, c, ts.URL+"/?s=hello")
	if strings.Contains(body, "Edit with Pursae Web Builder") || !strings.Contains(body, "wp-admin-bar-golementor_edit_page") || !strings.Contains(body, "ab-icon dashicons-layout") {
		t.Error("a search page should offer only the Theme Builder")
	}
	off, c2, _ := testServerBuilderOff(t)
	installAndLogin(t, off, c2)
	if _, body, _ = get(t, c2, off.URL+"/sample-page/"); strings.Contains(body, "golementor_edit_page") {
		t.Error("the toolbar shows Pursae Web Builder while it is off")
	}
}
