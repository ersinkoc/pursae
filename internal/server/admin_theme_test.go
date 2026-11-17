package server

import (
	"testing"

	"wpgo/internal/store"
)

func TestAdminThemeFor(t *testing.T) {
	cases := []struct {
		name string
		u    *store.User
		want string
	}{
		{"no user", nil, "classic"},
		{"unset", &store.User{Meta: map[string]any{}}, "classic"},
		{"unknown", &store.User{Meta: map[string]any{"admin_theme": "neon"}}, "classic"},
		{"classic", &store.User{Meta: map[string]any{"admin_theme": "classic"}}, "classic"},
		{"modern", &store.User{Meta: map[string]any{"admin_theme": "modern"}}, "modern"},
	}
	for _, c := range cases {
		if got := adminThemeFor(c.u).Slug; got != c.want {
			t.Errorf("%s: adminThemeFor = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAdminThemeAssets(t *testing.T) {
	classic, modern := adminThemes[0], adminThemes[1]
	if classic.Slug != "classic" || classic.CSS() != "" || classic.BodyClass() != "" || classic.Modern() {
		t.Fatalf("WordPress Classic must be the default and add nothing: %+v", classic)
	}
	if modern.CSS() != "/wp-admin/assets/css/pursae-modern.css" || modern.BodyClass() != "pursae-modern" || !modern.Modern() {
		t.Fatalf("Pursae Modern assets: css=%q class=%q", modern.CSS(), modern.BodyClass())
	}
}

func TestAdminThemeMode(t *testing.T) {
	cases := []struct {
		meta  map[string]any
		mode  string
		class string
	}{
		{map[string]any{"admin_theme": "modern"}, "light", "pursae-modern"},
		{map[string]any{"admin_theme": "modern", "admin_theme_mode": "dark"}, "dark", "pursae-modern pm-dark"},
		{map[string]any{"admin_theme": "modern", "admin_theme_mode": "auto"}, "auto", "pursae-modern pm-auto"},
		{map[string]any{"admin_theme": "modern", "admin_theme_mode": "neon"}, "light", "pursae-modern"},
		{map[string]any{"admin_theme": "classic", "admin_theme_mode": "dark"}, "light", ""}, // Classic has no dark mode
	}
	for _, c := range cases {
		th := adminThemeFor(&store.User{Meta: c.meta})
		if th.Modern() && th.Mode != c.mode {
			t.Errorf("%v: mode %q, want %q", c.meta, th.Mode, c.mode)
		}
		if got := th.BodyClass(); got != c.class {
			t.Errorf("%v: body class %q, want %q", c.meta, got, c.class)
		}
	}
}
