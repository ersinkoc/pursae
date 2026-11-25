package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestBuildYourOwnPages(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	base := ts.URL

	for _, tc := range []struct {
		path string
		want []string
	}{
		{"/wp-admin/plugin-build", []string{"Build your own plugin", "pursae plugin keygen", "/wp-admin/plugin-keys", "/wp-admin/plugin-install?tab=upload", "hello@pursae.com", "https://pursae.com/forum/", "https://pursae.com/docs/plugins/publishing/", `id="menu-plugins"`}},
		{"/wp-admin/theme-build", []string{"Build your own theme", "theme.json", "/wp-admin/theme-install", "hello@pursae.com", "https://pursae.com/docs/themes/hello-pursae/", `id="menu-appearance"`}},
	} {
		code, body, _ := get(t, c, base+tc.path)
		if code != http.StatusOK {
			t.Fatalf("%s: %d", tc.path, code)
		}
		for _, w := range tc.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s misses %q", tc.path, w)
			}
		}
	}

	for path, link := range map[string]string{
		"/wp-admin/plugins": `href="/wp-admin/plugin-build"`,
		"/wp-admin/themes":  `href="/wp-admin/theme-build"`,
	} {
		if _, body, _ := get(t, c, base+path); !strings.Contains(body, link) {
			t.Errorf("%s has no %s button", path, link)
		}
	}

	asCreateUser(t, srv, "editor1", "editor", asStrongPass)
	ec := asNewClient()
	if code, _, _ := asLogin(t, ec, base, "/wp-login", "editor1", asStrongPass); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	for _, p := range []string{"/wp-admin/plugin-build", "/wp-admin/theme-build"} {
		if code, _, _ := get(t, ec, base+p); code != http.StatusForbidden {
			t.Errorf("editor opened %s: %d", p, code)
		}
	}
}
