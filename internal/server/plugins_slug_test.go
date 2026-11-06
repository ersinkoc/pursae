package server

import "testing"

// The Settings links of the core plugins are found by slug, whatever the order of the rows.
func TestNativeSettingsLinksBySlug(t *testing.T) {
	rows := []pluginRow{
		{Slug: gontactSlug, Active: false, SettingsURL: "/wp-admin/admin?page=wpcf7"},
		{Slug: msSlug, Active: true},
		{Slug: golementorSlug, Active: true},
		{Slug: "classic-editor", Active: false, SettingsURL: "/wp-admin/options-writing"},
		{Slug: "wpgo-cache", Active: false, SettingsURL: "/wp-admin/admin?page=wpgo-cache"},
	}
	nativeSettingsLinks(rows)
	want := map[string]string{
		gontactSlug:      "",
		msSlug:           "/wp-admin/admin?page=" + msSlug,
		golementorSlug:   "/wp-admin/theme-builder",
		"classic-editor": "/wp-admin/options-writing",
		"wpgo-cache":     "",
	}
	for _, r := range rows {
		if r.SettingsURL != want[r.Slug] {
			t.Errorf("%s: SettingsURL %q, want %q", r.Slug, r.SettingsURL, want[r.Slug])
		}
	}

	rows = []pluginRow{
		{Slug: "wpgo-cache", Active: true, SettingsURL: "/wp-admin/admin?page=wpgo-cache"},
		{Slug: golementorSlug, Active: false},
		{Slug: msSlug, Active: false},
		{Slug: gontactSlug, Active: true, SettingsURL: "/wp-admin/admin?page=wpcf7"},
	}
	nativeSettingsLinks(rows)
	want = map[string]string{
		"wpgo-cache":   "/wp-admin/admin?page=wpgo-cache",
		golementorSlug: "",
		msSlug:         "",
		gontactSlug:    "/wp-admin/admin?page=wpcf7",
	}
	for _, r := range rows {
		if r.SettingsURL != want[r.Slug] {
			t.Errorf("%s: SettingsURL %q, want %q", r.Slug, r.SettingsURL, want[r.Slug])
		}
	}
}

func TestPluginURLSlug(t *testing.T) {
	if pluginURLSlug("golementor") != "web-builder" || pluginSlugFromURL("web-builder") != "golementor" {
		t.Error("the builder's link slug does not round-trip")
	}
	for _, s := range []string{"classic-editor", "wpgo-cache", gontactSlug} {
		if pluginURLSlug(s) != s || pluginSlugFromURL(s) != s {
			t.Errorf("%s changed in links", s)
		}
	}
}
