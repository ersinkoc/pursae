package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestLicenseAlertFor(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	soon := now.Add(5 * 24 * time.Hour).Format(time.RFC3339)
	far := now.Add(90 * 24 * time.Hour).Format(time.RFC3339)
	past := now.Add(-time.Hour).Format(time.RFC3339)
	for _, tc := range []struct {
		name  string
		l     *licenseStatus
		alert string
		ok    bool
	}{
		{"valid far", &licenseStatus{Valid: true, Expires: far}, "", true},
		{"valid no expiry", &licenseStatus{Valid: true}, "", true},
		{"expiring", &licenseStatus{Valid: true, Expires: soon}, "expiring:" + soon, true},
		{"expired by date", &licenseStatus{Valid: true, Expires: past}, "expired", true},
		{"revoked", &licenseStatus{Error: "revoked"}, "revoked", true},
		{"wrong site", &licenseStatus{Error: "domain_mismatch"}, "domain_mismatch", true},
		{"unreachable keeps state", &licenseStatus{Error: "unreachable"}, "", false},
		{"nil", nil, "", false},
	} {
		got, ok := licenseAlertFor(tc.l, now)
		if got != tc.alert || ok != tc.ok {
			t.Errorf("%s: (%q, %v), want (%q, %v)", tc.name, got, ok, tc.alert, tc.ok)
		}
	}
}

func TestHubIncompatible(t *testing.T) {
	old := Version
	defer func() { Version = old }()
	Version = "1.2.0"
	for req, want := range map[string]bool{"": false, "1.2.0": false, "1.1.9": false, "1.3.0": true, "2.0.0": true} {
		if got := hubIncompatible(req); got != want {
			t.Errorf("Version 1.2.0 requires %q: %v", req, got)
		}
	}
	Version = "dev"
	if hubIncompatible("9.0.0") {
		t.Error("a dev build is never incompatible")
	}
}

// The license is re-checked by the daily ping: an unknown key raises a notice for administrators and
// one e-mail; a valid key clears it.
func TestLicenseWatchAlert(t *testing.T) {
	base, c, srv, hub := managerSite(t)
	ctx := context.Background()
	_ = srv.settings.Set(ctx, map[string]any{licenseOption: "PRS-NOPE"})
	if err := srv.hubPing(ctx); err != nil {
		t.Fatal(err)
	}
	if got := srv.settings.String(licenseAlertOption); got != "unknown" {
		t.Fatalf("alert = %q", got)
	}
	if srv.settings.String(licenseSentOption) != "unknown" {
		t.Error("no alert e-mail recorded")
	}
	_, body, _ := get(t, c, base+"/wp-admin/")
	if !strings.Contains(body, "Pursae license: This license key does not exist.") {
		t.Error("no license notice on the admin screens")
	}
	_ = srv.settings.Set(ctx, map[string]any{licenseOption: hub.validKey})
	srv.licenseForget(ctx)
	if err := srv.hubPing(ctx); err != nil {
		t.Fatal(err)
	}
	if srv.settings.String(licenseAlertOption) != "" || srv.settings.String(licenseSentOption) != "" {
		t.Error("a valid license did not clear the alert")
	}
}

// Statistics off: the ping carries no inventory and no events are queued.
func TestStatsOptOut(t *testing.T) {
	_, _, srv, hub := managerSite(t)
	ctx := context.Background()
	srv.hubEvent("plugin_install", "plugin", "a", "1.0.0")
	if len(srv.hubQueuedEvents()) != 1 {
		t.Fatal("event not queued with statistics on")
	}
	_ = srv.settings.Set(ctx, map[string]any{statsOption: "off"})
	srv.hubEvent("plugin_install", "plugin", "b", "1.0.0")
	if n := len(srv.hubQueuedEvents()); n != 1 {
		t.Errorf("queued with statistics off: %d", n)
	}
	b := srv.hubPingBody(ctx)
	if b.URL == "" || b.Version == "" || len(b.Plugins) != 0 || len(b.Themes) != 0 || b.OS != "" || b.Theme != "" || b.Locale != "" {
		t.Errorf("minimal ping expected: %+v", b)
	}
	if err := srv.hubPing(ctx); err != nil {
		t.Fatal(err)
	}
	if len(hub.pings) != 1 || len(hub.pings[0]["plugins"].([]any)) != 0 {
		t.Errorf("ping with statistics off: %v", hub.pings)
	}
}

// The marketplace shows compatibility and changelog, filters by tag, and auto-updates apply from the daily check.
func TestMarketplaceCompatAndAutoUpdate(t *testing.T) {
	base, c, srv, hub := managerSite(t)
	ctx := context.Background()
	hub.extra = map[string]any{"requires_core": "99.0.0", "tested_core": "100.0.0", "tags": []string{"counting"}, "changelog": "Faster counting."}
	old := Version
	Version = "1.0.0"
	defer func() { Version = old }()
	_, body, _ := get(t, c, base+"/wp-admin/plugin-install")
	for _, want := range []string{"Incompatible", "Requires Pursae 99.0.0 or newer", "tag=counting"} {
		if !strings.Contains(body, want) {
			t.Errorf("marketplace lacks %q", want)
		}
	}
	if strings.Contains(body, `class="install-now`) {
		t.Error("an incompatible listing can be installed")
	}
	if _, body, _ = get(t, c, base+"/wp-admin/plugin-install?tag=other"); strings.Contains(body, "Word Counter") {
		t.Error("tag filter keeps a listing without that tag")
	}
	hub.extra = map[string]any{"changelog": "Faster counting."}
	srv.hubDropCaches(ctx)
	hubInstallWordCounter(t, base, c)

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	hub.keys = append(hub.keys, map[string]string{"id": "hub-2026", "public": base64.StdEncoding.EncodeToString(pub)})
	hub.publish(t, "1.1.0", priv)
	srv.hubDropCaches(ctx)
	_, body, _ = get(t, c, base+"/wp-admin/plugins")
	if !strings.Contains(body, "Faster counting.") {
		t.Error("changelog missing from the update row")
	}
	m := regexp.MustCompile(`href="([^"]*action=enable-auto-update[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Enable auto-updates link")
	}
	if code, _, _ := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound {
		t.Fatalf("enable auto-updates: %d", code)
	}
	if !srv.pluginAutoUpdates()["word-counter"] {
		t.Fatal("auto-update not stored")
	}
	if err := srv.hubPing(ctx); err != nil {
		t.Fatal(err)
	}
	var v string
	_ = srv.store.Pool.QueryRow(ctx, `SELECT version FROM plugins WHERE slug = 'word-counter'`).Scan(&v)
	if v != "1.1.0" {
		t.Errorf("auto-update did not run: version %s", v)
	}
}

// The License screen saves the statistics switch (hidden 0 + checkbox 1) and leaves it alone when the field is absent.
func TestStatsSwitchSaved(t *testing.T) {
	base, c, srv, _ := managerSite(t)
	_, body, _ := get(t, c, base+"/wp-admin/options-license")
	if !strings.Contains(body, `name="pursae_send_stats"`) || !strings.Contains(body, `checked="checked"`) {
		t.Fatal("switch missing or not on by default")
	}
	nonce := formNonce(t, body)
	post(t, c, base+"/wp-admin/options-license", url.Values{"_wpnonce": {nonce}, statsOption: {"0"}, "submit": {"Save Changes"}})
	if srv.statsEnabled() {
		t.Error("unchecked switch did not turn statistics off")
	}
	post(t, c, base+"/wp-admin/options-license", url.Values{"_wpnonce": {nonce}, "submit": {"Save Changes"}})
	if srv.statsEnabled() {
		t.Error("a post without the field changed the setting")
	}
	post(t, c, base+"/wp-admin/options-license", url.Values{"_wpnonce": {nonce}, statsOption: {"0", "1"}, "submit": {"Save Changes"}})
	if !srv.statsEnabled() {
		t.Error("checked switch did not turn statistics on")
	}
}
