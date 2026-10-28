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
