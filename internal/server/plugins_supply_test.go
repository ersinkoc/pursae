package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"html"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"wpgo/internal/plugins"
	"wpgo/internal/store"
)

// uploadPluginZip posts a package to the Upload Plugin form and returns the redirect.
func uploadPluginZip(t *testing.T, c *http.Client, base string, zip []byte) string {
	t.Helper()
	_, page, _ := get(t, c, base+"/wp-admin/plugin-install?tab=upload")
	nonce := formNonce(t, page[strings.Index(page, "wp-upload-form"):])
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("_wpnonce", nonce)
	fw, _ := w.CreateFormFile("pluginzip", "plugin.zip")
	_, _ = fw.Write(zip)
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, base+"/wp-admin/plugin-install", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.Header.Get("Location")
}

// repackWordCounter is word-counter 1.0.0's manifest with another version (and, when broken, a
// module that does not compile), signed with priv.
func repackWordCounter(t *testing.T, hub *fakeManager, version string, priv ed25519.PrivateKey, broken bool) []byte {
	t.Helper()
	pkg, err := plugins.LoadPackage(bytes.NewReader(hub.zips["1.0.0"]), int64(len(hub.zips["1.0.0"])), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	pkg.Manifest.Version = version
	manifest, _ := json.Marshal(pkg.Manifest)
	wasm := pkg.Wasm
	if broken {
		wasm = []byte("\x00asm\x01\x00\x00\x00garbage")
	}
	var buf bytes.Buffer
	if err := plugins.WritePackage(&buf, manifest, wasm, nil, priv); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The key that signed an installed plugin is pinned: an update from another publisher is refused
// from the Manager, an older or foreign upload needs "Replace current with uploaded", and a Manager
// update that does not load is rolled back to the kept <slug>.zip.prev.
func TestPluginSignerPinDowngradeAndRollback(t *testing.T) {
	base, c, srv, hub := managerSite(t)
	ctx := context.Background()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	hub.keys = append(hub.keys, map[string]string{"id": "hub-2026", "public": base64.StdEncoding.EncodeToString(pub)})
	hubInstallWordCounter(t, base, c)
	row := func() (version, signer string) {
		srv.store.Pool.QueryRow(ctx, `SELECT version, signer FROM plugins WHERE slug = 'word-counter'`).Scan(&version, &signer)
		return
	}
	if v, signer := row(); v != "1.0.0" || signer == "" {
		t.Fatalf("installed %s, signer %q not recorded", v, signer)
	}

	// Another publisher's key, trusted by the site but not the Manager's: refused as an update.
	strangerPub, stranger, _ := ed25519.GenerateKey(rand.Reader)
	srv.plugins.keyMu.Lock()
	srv.plugins.siteKeys = append(srv.plugins.siteKeys, strangerPub)
	srv.plugins.keyMu.Unlock()
	hub.publish(t, "1.1.0", stranger)
	srv.hubDropCaches(ctx)
	if _, err := srv.hubUpdatePlugin(ctx, "word-counter"); err == nil || !strings.Contains(err.Error(), "another key") {
		t.Fatalf("update by another publisher: %v", err)
	}
	if v, _ := row(); v != "1.0.0" {
		t.Fatalf("refused update changed the plugin: %s", v)
	}

	// A Manager key rotation is not a new publisher: 1.1.0 by hub-2026 installs, the old package is kept.
	hub.publish(t, "1.1.0", priv)
	srv.hubDropCaches(ctx)
	if _, err := srv.hubUpdatePlugin(ctx, "word-counter"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srv.plugins.dir, "word-counter.zip.prev")); err != nil {
		t.Error("no word-counter.zip.prev after an update")
	}

	// 1.2.0 does not compile: the update is rolled back to 1.1.0, which is loaded again.
	hub.mu.Lock()
	hub.zips["1.2.0"], hub.latest = repackWordCounter(t, hub, "1.2.0", priv, true), "1.2.0"
	hub.mu.Unlock()
	srv.hubDropCaches(ctx)
	if _, err := srv.hubUpdatePlugin(ctx, "word-counter"); err == nil || !strings.Contains(err.Error(), "1.1.0 was restored") {
		t.Fatalf("broken update: %v", err)
	}
	if v, _ := row(); v != "1.1.0" {
		t.Errorf("after the rollback: %s", v)
	}
	if lp, ok := srv.plugins.rt.Plugin("word-counter"); !ok || lp.Version() != "1.1.0" {
		t.Error("the previous version is not loaded after the rollback")
	}

	// Uploading the older 1.0.0 asks first, then replaces on confirmation.
	loc := uploadPluginZip(t, c, base, hub.zips["1.0.0"])
	if !strings.Contains(loc, "replace=word-counter") || !strings.Contains(loc, "older") {
		t.Fatalf("downgrade upload: %s", loc)
	}
	if v, _ := row(); v != "1.1.0" {
		t.Fatalf("a downgrade was installed without confirmation: %s", v)
	}
	_, page, _ := get(t, c, base+loc)
	m := regexp.MustCompile(`href="(/wp-admin/plugin-install\?action=replace-plugin&amp;plugin=word-counter&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no Replace current with uploaded link")
	}
	if code, _, _ := get(t, c, base+"/wp-admin/plugin-install?action=replace-plugin&plugin=word-counter&_wpnonce=bad"); code == http.StatusFound {
		t.Error("replace without a valid nonce")
	}
	if _, _, loc = get(t, c, base+html.UnescapeString(m[1])); !strings.Contains(loc, "updated=Word+Counter+1.0.0") {
		t.Fatalf("replace: %s", loc)
	}
	if v, _ := row(); v != "1.0.0" {
		t.Errorf("after the confirmed replace: %s", v)
	}

	// Built-in slugs are reserved, the Pursae Security and Backup Manager ones included.
	for _, slug := range []string{asSlug, bmgrSlug, "golementor", "web-builder", pixelSlug} {
		if !srv.builtinPluginSlug(slug) {
			t.Errorf("%s is not reserved", slug)
		}
	}
}

// Without a pinned root key a build refuses to update itself unless WPGO_TRUST_HUB_KEYS=1; with
// one, only a Manager key the root endorsed verifies the release.
func TestCoreSelfUpdateNeedsPinnedRoot(t *testing.T) {
	base, c, srv, hub := managerSite(t)
	ctx := context.Background()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	bin := []byte("\x7fELF pursae 9.9.9")
	art := signedCore("9.9.9", runtime.GOOS, runtime.GOARCH, bin, priv, "hub-2026")
	art.URL = hub.URL + "/api/v1/core/9.9.9/" + runtime.GOOS + "-" + runtime.GOARCH
	hub.coreBin = bin
	hub.core["artifacts"] = []*coreArtifact{art}
	target := filepath.Join(t.TempDir(), "pursae")
	os.WriteFile(target, []byte("old binary"), 0o755)
	oldExe, oldRestart := coreExecutable, coreRestart
	t.Cleanup(func() { coreExecutable, coreRestart = oldExe, oldRestart })
	coreExecutable = func() (string, error) { return target, nil }
	coreRestart = func(string) error { return nil }

	withRootKeys(t)
	withTrustHubKeys(t, false)
	hub.keys = append(hub.keys, map[string]string{"id": "hub-2026", "public": base64.StdEncoding.EncodeToString(pub)})
	srv.hubDropCaches(ctx)
	_, body, _ := get(t, c, base+"/wp-admin/update-core")
	if !strings.Contains(body, "This site cannot update itself") || !strings.Contains(body, "WPGO_TRUST_HUB_KEYS") {
		t.Error("the Updates screen does not explain why a build without a root key cannot update")
	}
	if _, err := srv.coreUpdate(ctx, target); err == nil {
		t.Fatal("core update without a root key")
	}

	rootPub, rootPriv, _ := ed25519.GenerateKey(rand.Reader)
	withRootKeys(t, rootPub)
	if _, err := srv.coreUpdate(ctx, target); err == nil || !strings.Contains(err.Error(), "endorsed") {
		t.Fatalf("unendorsed Manager key: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "old binary" {
		t.Fatal("replaced by a release signed with an unendorsed key")
	}
	k := endorse(rootPriv, "hub-2026", pub)
	hub.mu.Lock()
	hub.keys[len(hub.keys)-1]["endorsement"] = k.Endorsement
	hub.mu.Unlock()
	if v, err := srv.coreUpdate(ctx, target); err != nil || v != "9.9.9" {
		t.Fatalf("endorsed release: %v %v", v, err)
	}
	if b, _ := os.ReadFile(target); !bytes.Equal(b, bin) {
		t.Error("the endorsed release was not installed")
	}
}

// save_post_<type> and save_post fire for posts and pages after every save.
func TestSavePostFiresPluginHooks(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)
	ctx := context.Background()
	var got []string
	srv.plugins.hooks.AddAction("save_post_page", 10, func(_ context.Context, args ...any) { got = append(got, "save_post_page") })
	srv.plugins.hooks.AddAction("save_post", 10, func(_ context.Context, args ...any) {
		data, _ := args[1].(*plugins.PostData)
		if data == nil || data.Title != "Hooked" {
			t.Errorf("save_post post argument %#v", args[1])
		}
		got = append(got, "save_post")
	})
	p := &store.Post{Type: "page", Title: "Hooked", Status: "draft"}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	srv.afterPostSave(ctx, p)
	if strings.Join(got, ",") != "save_post_page,save_post" {
		t.Errorf("fired %v", got)
	}
	got = nil
	srv.afterPostSave(ctx, &store.Post{ID: p.ID, Type: "wp_block"})
	if len(got) != 0 {
		t.Errorf("fired for a post type plugins cannot see: %v", got)
	}
}

// Migration 0035 copies existing plugin options into the plugin namespace and leaves the core's
// and the built-in plugins' options where they are.
func TestPluginOptionNamespaceMigration(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)
	ctx := context.Background()
	sql, err := os.ReadFile("../db/migrations/0035_plugin_option_namespace.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = srv.store.Pool.Exec(ctx, `INSERT INTO plugins (slug, version) VALUES ('wp', '1.0.0'), ('allow', '1.0.0'), ('demo-x', '1.0.0');
		INSERT INTO settings (key, value) VALUES ('wp_mail_smtp', '"core"'), ('plugin_allow_unsigned', 'false'), ('demo_x_count', '3'), ('plugin_demo_x_title', '"T"'), ('demoxy_other', '1')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.Pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	val := func(key string) string {
		var v string
		_ = srv.store.Pool.QueryRow(ctx, `SELECT value::text FROM settings WHERE key = $1`, key).Scan(&v)
		return v
	}
	if val("wasm:demo-x:demo_x_count") != "3" || val("wasm:demo-x:plugin_demo_x_title") != `"T"` {
		t.Error("plugin options not copied into the namespace")
	}
	if val("wasm:demo-x:demoxy_other") != "" {
		t.Error("an option of another prefix was copied")
	}
	if val("wasm:allow:plugin_allow_unsigned") != "" {
		t.Error("plugin_allow_unsigned copied into a plugin namespace")
	}
	if val("wp_mail_smtp") != `"core"` {
		t.Error("a built-in option was moved")
	}
}
