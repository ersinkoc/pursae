package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wpgo/internal/auth"
	"wpgo/internal/plugins"
)

// The same Hub-signed package uploaded over a Hub plugin keeps it a Hub plugin, so the upload does
// not silently stop its updates (PRS-PLG27-002).
func TestPluginUploadKeepsHubSource(t *testing.T) {
	base, c, srv, hub := managerSite(t)
	ctx := context.Background()
	hubInstallWordCounter(t, base, c)
	source := func() (s string) {
		srv.store.Pool.QueryRow(ctx, `SELECT source FROM plugins WHERE slug = 'word-counter'`).Scan(&s)
		return
	}
	if s := source(); s != pluginSourceHub {
		t.Fatalf("installed from the Manager with source %q", s)
	}
	if _, _, err := srv.installAndReload(ctx, hub.zips["1.0.0"], pluginSourceUpload, "", true); err != nil {
		t.Fatal(err)
	}
	if s := source(); s != pluginSourceHub {
		t.Errorf("a Hub-signed upload over a Hub plugin changed its source to %q", s)
	}
}

// A local unsigned build is not silently replaced by a publisher's package of the same slug
// (PRS-PLG27-002), and an active plugin cannot be deleted before it is deactivated (PRS-PLG27-005).
func TestPluginUnsignedReplaceAndActiveDelete(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	installDemo(t, srv) // unsigned, active
	ctx := context.Background()

	pkg, err := plugins.LoadPackage(bytes.NewReader(demoPackage(t)), int64(len(demoPackage(t))), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	srv.plugins.keyMu.Lock()
	srv.plugins.siteKeys = append(srv.plugins.siteKeys, pub)
	srv.plugins.keyMu.Unlock()
	var buf bytes.Buffer
	if err := plugins.WritePackage(&buf, pkg.ManifestJSON, pkg.Wasm, nil, priv); err != nil {
		t.Fatal(err)
	}
	var rerr *pluginReplaceError
	if _, err := srv.installPackageFrom(ctx, buf.Bytes(), pluginSourceHub, ""); !errors.As(err, &rerr) || !strings.Contains(err.Error(), "unsigned") {
		t.Fatalf("a signed package replaced the unsigned local build: %v", err)
	}

	slug := pkg.Manifest.Slug
	form := url.Values{"action": {"delete"}, "plugin": {slug}, "_wpnonce": {actionNonce(t, srv, c, ts.URL, "delete-plugin_"+slug)}}
	if _, _, loc := post(t, c, ts.URL+"/wp-admin/plugins", form); strings.Contains(loc, "deleted=true") {
		t.Fatalf("an active plugin was deleted: %s", loc)
	}
	var n int
	srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM plugins WHERE slug = $1`, slug).Scan(&n)
	if _, err := os.Stat(filepath.Join(srv.plugins.dir, slug+".zip")); n != 1 || err != nil {
		t.Fatalf("active plugin removed: row %d, package %v", n, err)
	}
}

// An https site marks the login cookie Secure even when the request reaches it as plain http through
// a proxy it does not trust (PRS-SEC27-004).
func TestLoginCookieSecureOnHTTPSSite(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	if err := srv.settings.Set(context.Background(), map[string]any{"home": "https://example.test"}); err != nil {
		t.Fatal(err)
	}
	nc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := nc.PostForm(ts.URL+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, ck := range resp.Cookies() {
		if ck.Name == auth.CookieName && ck.Value != "" {
			if !ck.Secure {
				t.Fatal("login cookie of an https site is not Secure")
			}
			return
		}
	}
	t.Fatalf("no login cookie (status %d)", resp.StatusCode)
}
