package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/plugins"
)

func TestSplitPermissions(t *testing.T) {
	keep, missing := splitPermissions(
		[]plugins.Permission{"options:read", "http:outbound"},
		[]plugins.Permission{"options:read", "content:filter"})
	if len(keep) != 1 || keep[0] != "options:read" || len(missing) != 1 || missing[0] != "content:filter" {
		t.Fatalf("keep=%v missing=%v", keep, missing)
	}
}

func TestTrustedKeysRoundTrip(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	text := plugins.EncodeKey(pub) + " Acme  Corp\nnot-a-key\n"
	keys := parseTrustedKeys(text)
	if len(keys) != 1 || keys[0].Comment != "Acme Corp" || keys[0].Fingerprint != keyFingerprint(pub) {
		t.Fatalf("parsed %+v", keys)
	}
	if again := parseTrustedKeys(formatTrustedKeys(keys)); len(again) != 1 || again[0].Key != keys[0].Key {
		t.Fatalf("round trip %+v", again)
	}
	if len(parseTrustedKeys("a,b\n")) != 0 {
		t.Error("garbage parsed as a key")
	}
}

func TestPluginMaxPackageMatchesManager(t *testing.T) {
	if pluginMaxPackage != hubMaxPackage || pluginMaxPackage>>20 != 64 {
		t.Fatalf("limit %d", pluginMaxPackage)
	}
}

func TestTrustedKeysScreenAppliesLive(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	_, body, _ := get(t, c, ts.URL+"/wp-admin/plugin-keys")
	nonce := ""
	if i := strings.Index(body, `name="_wpnonce" value="`); i >= 0 {
		nonce = body[i+len(`name="_wpnonce" value="`):]
		nonce = nonce[:strings.Index(nonce, `"`)]
	}
	if nonce == "" {
		t.Fatal("no nonce on the Trusted Keys screen")
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	before := len(srv.plugins.trusted())
	code, _, loc := post(t, c, ts.URL+"/wp-admin/plugin-keys", url.Values{"_wpnonce": {nonce}, "action": {"add"}, "key": {plugins.EncodeKey(pub)}, "comment": {"Acme"}})
	if code != 302 || !strings.Contains(loc, "done=") || len(srv.plugins.trusted()) != before+1 {
		t.Fatalf("add: %d %s trusted=%d", code, loc, len(srv.plugins.trusted()))
	}
	code, _, loc = post(t, c, ts.URL+"/wp-admin/plugin-keys", url.Values{"_wpnonce": {nonce}, "action": {"remove"}, "fingerprint": {keyFingerprint(pub)}})
	if code != 302 || !strings.Contains(loc, "done=") || len(srv.plugins.trusted()) != before {
		t.Fatalf("remove: %d %s trusted=%d", code, loc, len(srv.plugins.trusted()))
	}
	if _, _, loc = post(t, c, ts.URL+"/wp-admin/plugin-keys", url.Values{"_wpnonce": {nonce}, "action": {"add"}, "key": {"nope"}}); !strings.Contains(loc, "error=") {
		t.Errorf("an invalid key was accepted: %s", loc)
	}
}

func TestRecordPluginDisabledKeepsReason(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)
	ctx := context.Background()
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO plugins (slug, version, status) VALUES ('crashy-test', '1.0.0', 'active')`); err != nil {
		t.Fatal(err)
	}
	srv.recordPluginDisabled(ctx, "crashy-test", "5 consecutive failures")
	var status, why string
	if err := srv.store.Pool.QueryRow(ctx, `SELECT status, disabled_reason FROM plugins WHERE slug = 'crashy-test'`).Scan(&status, &why); err != nil {
		t.Fatal(err)
	}
	if status != "inactive" || why != "5 consecutive failures" {
		t.Fatalf("status=%q reason=%q", status, why)
	}
	for _, r := range srv.pluginInventory(ctx) {
		if r.Slug == "crashy-test" && (!r.Disabled || r.DisabledWhy != why) {
			t.Errorf("the Plugins screen lost the reason: %+v", r)
		}
	}
}
