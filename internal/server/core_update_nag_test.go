package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/auth"
	"wpgo/internal/store"
)

const coreNagMarkup = `<div class="update-nag notice notice-warning inline"><strong>Pursae 9.9.9</strong> is available!`

func TestCoreUpdateNagOnEveryAdminScreen(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	hub := fakeHub(t) // announces 9.9.9
	srv.cfg.HubURL = hub.URL
	ctx := context.Background()

	// Nothing until the site has read the Manager's index.
	_, body, _ := get(t, c, base+"/wp-admin/edit")
	if strings.Contains(body, "update-nag") || strings.Contains(body, "Get Version") {
		t.Fatal("a release is announced before the index was read")
	}

	// The Updates screen reads the index: it shows the release itself, not the notice on top.
	_, body, _ = get(t, c, base+"/wp-admin/update-core")
	if strings.Contains(body, "update-nag") || strings.Contains(body, "Get Version") {
		t.Error("the Updates screen repeats the notice")
	}
	if srv.settings.String(coreLatestOption) != "9.9.9" {
		t.Fatalf("remembered release %q", srv.settings.String(coreLatestOption))
	}

	// Every other admin screen: the notice, the Updates count and the footer link.
	for _, path := range []string{"/wp-admin/", "/wp-admin/edit", "/wp-admin/options-general", "/wp-admin/plugins"} {
		_, body, _ = get(t, c, base+path)
		for _, want := range []string{coreNagMarkup, `<a href="/wp-admin/update-core" aria-label="Please update Pursae now">Please update now</a>`,
			`Updates <span class="update-plugins count-1"><span class="update-count">1</span></span>`,
			`<strong><a href="/wp-admin/update-core">Get Version 9.9.9</a></strong>`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", path, want)
			}
		}
	}

	// A user who may not update is told to notify the administrator, without the link.
	hash, _ := auth.HashPassword("sub password")
	if _, err := srv.store.CreateUser(ctx, store.NewUser{Login: "sub", Email: "sub@example.test", PasswordHash: hash, Role: "subscriber"}); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	sub := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if code, _, _ := post(t, sub, base+"/wp-login", url.Values{"log": {"sub"}, "pwd": {"sub password"}}); code != http.StatusFound {
		t.Fatalf("subscriber login: %d", code)
	}
	_, body, _ = get(t, sub, base+"/wp-admin/profile")
	if !strings.Contains(body, coreNagMarkup) || !strings.Contains(body, "Please notify the site administrator.") ||
		strings.Contains(body, "Please update now") || strings.Contains(body, "Get Version") {
		t.Error("subscriber's notice")
	}

	// Once the binary is the announced version the notice goes away.
	old := Version
	Version = "9.9.9"
	_, body, _ = get(t, c, base+"/wp-admin/edit")
	Version = old
	if strings.Contains(body, "update-nag") || strings.Contains(body, "count-1") || strings.Contains(body, "Get Version") {
		t.Error("the notice stays after the update")
	}

	// Update checks off: no notice.
	srv.cfg.HubURL = "off"
	_, body, _ = get(t, c, base+"/wp-admin/edit")
	if strings.Contains(body, "update-nag") {
		t.Error("notice with the Manager switched off")
	}
	srv.cfg.HubURL = hub.URL

	// A release the Manager withdraws, or a version that is not one, is not offered.
	srv.rememberCoreLatest(ctx, &hubIndex{API: 1})
	_, body, _ = get(t, c, base+"/wp-admin/edit")
	if strings.Contains(body, "update-nag") || srv.settings.String(coreLatestOption) != "" {
		t.Error("a withdrawn release is still announced")
	}
	srv.rememberCoreLatest(ctx, &hubIndex{API: 1, Core: &hubCore{Latest: "9.9.9"}})
	srv.hubDropCaches(ctx) // license changed or forgotten
	if srv.settings.String(coreLatestOption) != "" {
		t.Error("the release survives dropping the Manager caches")
	}
	srv.rememberCoreLatest(ctx, &hubIndex{API: 1, Core: &hubCore{Latest: `99.0.0<script>`}})
	if srv.settings.String(coreLatestOption) != "" {
		t.Error("an invalid version was stored")
	}
}

func TestValidReleaseVersion(t *testing.T) {
	for v, want := range map[string]bool{"0.1.2": true, "v1.0.0": true, "1.2.3-beta.1": true, "1.0.0+build.5": true,
		"": false, "1.0 beta": false, "<b>1</b>": false, strings.Repeat("1", 41): false} {
		if got := validReleaseVersion(v); got != want {
			t.Errorf("validReleaseVersion(%q) = %v", v, got)
		}
	}
}
