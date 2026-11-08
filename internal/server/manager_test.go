package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// managerSite starts a site connected to a fake Pursae Manager, installed and logged in.
func managerSite(t *testing.T) (base string, c *http.Client, srv *Server, hub *fakeManager) {
	t.Helper()
	withTrustHubKeys(t, true) // the fake Manager's dev key carries no endorsement by the pinned root; tests of that turn it off
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	hub = fakeHub(t)
	srv.cfg.HubURL = hub.URL // the Manager URL is fixed in production builds
	return ts.URL, c, srv, hub
}

// hubInstallWordCounter installs word-counter from the Manager and activates it with its permission.
func hubInstallWordCounter(t *testing.T, base string, c *http.Client) {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/plugin-install")
	m := regexp.MustCompile(`href="([^"]*action=install-plugin&amp;source=hub&amp;plugin=word-counter[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Manager install link")
	}
	if code, _, loc := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound || !strings.Contains(loc, "installed=") {
		t.Fatalf("hub install: %d %s", code, loc)
	}
	_, body, _ = get(t, c, base+"/wp-admin/plugins")
	a := regexp.MustCompile(`href="(/wp-admin/plugins\?action=activate&amp;plugin=word-counter&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
	if a == nil {
		t.Fatal("no Activate link for word-counter")
	}
	_, body, _ = get(t, c, base+html.UnescapeString(a[1]))
	if !strings.Contains(body, "Allow and Activate") {
		t.Fatal("no permission consent screen")
	}
	code, _, loc := post(t, c, base+"/wp-admin/plugins", url.Values{"action": {"activate"}, "plugin": {"word-counter"},
		"permissions[]": {"posts:read"}, "_wpnonce": {formNonce(t, body[strings.Index(body, `name="plugin" value="word-counter"`):])}})
	if code != http.StatusFound || !strings.Contains(loc, "activate=true") {
		t.Fatalf("activate: %d %s", code, loc)
	}
}

func TestLicenseScreenAndHeaders(t *testing.T) {
	base, c, srv, hub := managerSite(t)
	ctx := context.Background()

	_, body, _ := get(t, c, base+"/wp-admin/options-license")
	for _, want := range []string{"License Settings", `href="/wp-admin/options-license"`, `name="pursae_license_key"`, "No license key entered", `value="Save Changes"`} {
		if !strings.Contains(body, want) {
			t.Errorf("License screen lacks %q", want)
		}
	}
	if srv.licensePro() || srv.licenseBeta() {
		t.Error("pro/beta without a key")
	}
	nonce := formNonce(t, body)

	// An unknown key: the Manager's message is shown.
	code, _, loc := post(t, c, base+"/wp-admin/options-license", url.Values{"_wpnonce": {nonce}, licenseOption: {"PRS-NOPE"}, "submit": {"Save Changes"}})
	if code != http.StatusFound || !strings.Contains(loc, "settings-updated=true") {
		t.Fatalf("save: %d %s", code, loc)
	}
	_, body, _ = get(t, c, base+"/wp-admin/options-license")
	if !strings.Contains(body, "Not valid") || !strings.Contains(body, "This key does not exist.") || !strings.Contains(body, `value="Check now"`) {
		t.Error("an unknown key is not reported")
	}

	// The catalog answer is cached per key.
	get(t, c, base+"/wp-admin/plugin-install")

	// A valid key: plan, channels, expiry and domains.
	post(t, c, base+"/wp-admin/options-license", url.Values{"_wpnonce": {nonce}, licenseOption: {hub.validKey}, "submit": {"Save Changes"}})
	_, body, _ = get(t, c, base+"/wp-admin/options-license")
	for _, want := range []string{"<strong>Valid</strong>", "<td>Pro</td>", "Included: beta versions", "October 31, 2027", "<code>" + hubSiteURL(base) + "</code>", "plugin/acme-pro", "Last checked"} {
		if !strings.Contains(body, want) {
			t.Errorf("valid license screen lacks %q", want)
		}
	}
	if !srv.licensePro() || !srv.licenseBeta() {
		t.Error("licensePro/licenseBeta false for a pro beta license")
	}
	get(t, c, base+"/wp-admin/plugin-install") // a different key: fetched again

	// Cached for 12 hours: the screen does not ask again; Check now does.
	count := func(path string) int {
		n := 0
		for _, r := range hub.requests() {
			if r.Path == path {
				n++
			}
		}
		return n
	}
	before := count("/api/v1/license")
	get(t, c, base+"/wp-admin/options-license")
	if count("/api/v1/license") != before {
		t.Error("the license answer is not cached")
	}
	post(t, c, base+"/wp-admin/options-license", url.Values{"_wpnonce": {nonce}, "check": {"Check now"}})
	if count("/api/v1/license") != before+1 {
		t.Error("Check now did not ask the Manager")
	}
	if n := count("/api/v1/plugins"); n != 2 {
		t.Errorf("catalog fetched %d times, want 2 (once per license key)", n)
	}

	// Every request carries the site and, once entered, the license key.
	for _, r := range hub.requests() {
		if r.Site != hubSiteURL(base) {
			t.Errorf("%s %s: X-Pursae-Site %q", r.Method, r.Path, r.Site)
		}
	}
	srv.hubPing(ctx)
	srv.hubEvent("theme_preview", "theme", "x", "1.0.0")
	srv.hubFlushEvents(ctx)
	reqs := hub.requests()
	for _, path := range []string{"/api/v1/sites/ping", "/api/v1/sites/events", "/api/v1/index"} {
		i := slices.IndexFunc(reqs, func(r managerReq) bool { return r.Path == path && r.License == hub.validKey })
		if i < 0 {
			t.Errorf("%s was not sent with the license key", path)
		}
	}
	if p := hub.pings[len(hub.pings)-1]; p["license"] != hub.validKey {
		t.Errorf("ping license %v", p["license"])
	}

	// Plugins cannot read the key.
	h := &pluginHost{s: srv}
	if v, ok, _ := h.GetOption(ctx, "word-counter", licenseOption); ok || v != nil {
		t.Error("a plugin can read the license key")
	}
	if pluginOptionAllowed("pursae", licenseOption) {
		t.Error("a plugin slugged pursae may write the license key")
	}
}

func TestHubAccessBadges(t *testing.T) {
	base, c, _, hub := managerSite(t)
	hub.access, hub.channel = "pro", "beta"
	_, body, _ := get(t, c, base+"/wp-admin/plugin-install")
	if !strings.Contains(body, `wpgo-badge-pro`) || !strings.Contains(body, ">Pro</span>") || !strings.Contains(body, ">Beta</span>") {
		t.Error("the Add Plugin card shows no Pro/Beta badge")
	}
}

func TestHubEventsBatching(t *testing.T) {
	themesDir := t.TempDir()
	t.Setenv("WPGO_THEMES_DIR", themesDir)
	lib := libraryCopy(t)
	base, c, srv, hub := managerSite(t)
	srv.themeLibrary = os.DirFS(lib)
	t.Cleanup(srv.closePreviews)
	t.Cleanup(func() {
		unregisterTheme("wpgo-fixture")
		unregisterTheme("wpgo-fixture-two")
	})
	ctx := context.Background()

	hubInstallWordCounter(t, base, c)
	if code, _, _ := get(t, c, base+"/wp-admin/theme-preview?theme=wpgo-fixture"); code != http.StatusOK {
		t.Fatalf("preview: %d", code)
	}
	waitReady(t, c, base, "wpgo-fixture") // the sandbox installs, activates and imports the demo: none of it is sent
	// Deactivate through the Plugins screen.
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	if d := regexp.MustCompile(`href="(/wp-admin/plugins\?action=deactivate&amp;plugin=word-counter&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body); d != nil {
		get(t, c, base+html.UnescapeString(d[1]))
	} else {
		t.Fatal("no Deactivate link")
	}
	if n := len(hub.eventTypes()); n != 0 {
		t.Errorf("events were sent before the batch interval: %d", n)
	}
	srv.hubFlushEvents(ctx)
	want := []string{"plugin_install:word-counter", "plugin_activate:word-counter", "theme_preview:wpgo-fixture", "plugin_deactivate:word-counter"}
	if got := hub.eventTypes(); !slices.Equal(got, want) {
		t.Errorf("events %v, want %v", got, want)
	}
	if q := srv.hubQueuedEvents(); len(q) != 0 {
		t.Errorf("queue not empty after a flush: %v", q)
	}
	for _, e := range hub.events {
		if e.Kind == "" || e.Version == "" || e.At == "" {
			t.Errorf("incomplete event %+v", e)
		}
	}

	// A full batch is sent without waiting.
	for i := 0; i < hubEventsBatch; i++ {
		srv.hubEvent("theme_preview", "theme", "wpgo-fixture", "1.0.0")
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(hub.eventTypes()) < len(want)+hubEventsBatch && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := len(hub.eventTypes()); n != len(want)+hubEventsBatch {
		t.Errorf("a full batch was not sent at once: %d events", n)
	}

	// Unreachable: a bounded backlog is kept for later.
	hub.Close()
	for i := 0; i < hubEventsBacklog+20; i++ {
		srv.hubEvent("theme_preview", "theme", "wpgo-fixture", "1.0.0")
	}
	srv.hubFlushEvents(ctx)
	if n := len(srv.hubQueuedEvents()); n != hubEventsBacklog {
		t.Errorf("backlog %d, want %d", n, hubEventsBacklog)
	}
	// Switched off: dropped.
	srv.cfg.HubURL = "off"
	srv.hubEvent("theme_preview", "theme", "wpgo-fixture", "1.0.0")
	srv.hubFlushEvents(ctx)
	if n := len(srv.hubQueuedEvents()); n != 0 {
		t.Errorf("events kept with the Manager off: %d", n)
	}
}

func TestHubPluginUpdate(t *testing.T) {
	base, c, srv, hub := managerSite(t)
	ctx := context.Background()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	hub.keys = append(hub.keys, map[string]string{"id": "hub-2026", "public": base64.StdEncoding.EncodeToString(pub)})
	hubInstallWordCounter(t, base, c)
	status := func() (version, st, source string) {
		srv.store.Pool.QueryRow(ctx, `SELECT version, status, source FROM plugins WHERE slug = 'word-counter'`).Scan(&version, &st, &source)
		return
	}
	if v, st, src := status(); v != "1.0.0" || st != "active" || src != "hub" {
		t.Fatalf("installed: %s %s %s", v, st, src)
	}

	// 1.1.0 is published: the Plugins screen offers it.
	hub.publish(t, "1.1.0", priv)
	srv.hubDropCaches(ctx)
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	if !strings.Contains(body, "There is a new version of Word Counter available.") || !strings.Contains(body, `class="plugin-update-tr active"`) {
		t.Fatal("no update row on the Plugins screen")
	}
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=upgrade-plugin&amp;plugin=word-counter&amp;_wpnonce=[^"]+)" class="update-link"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Update now link")
	}
	_, body, _ = get(t, c, base+"/wp-admin/update-core")
	if !strings.Contains(body, "You have version 1.0.0 installed. Update to 1.1.0.") || !strings.Contains(body, `value="Update Plugins"`) {
		t.Error("Dashboard › Updates does not list the plugin update")
	}
	if code, _, _ := get(t, c, base+"/wp-admin/plugins?action=upgrade-plugin&plugin=word-counter&_wpnonce=bad"); code == http.StatusFound {
		t.Error("update without a valid nonce")
	}
	code, _, loc := get(t, c, base+html.UnescapeString(m[1]))
	if code != http.StatusFound || !strings.Contains(loc, "updated=Word+Counter+1.1.0") {
		t.Fatalf("update now: %d %s", code, loc)
	}
	if v, st, _ := status(); v != "1.1.0" || st != "active" {
		t.Errorf("after the update: %s %s", v, st)
	}
	if lp, ok := srv.plugins.rt.Plugin("word-counter"); !ok || lp.Version() != "1.1.0" {
		t.Error("the new version is not loaded")
	}

	// A wrong checksum is refused and nothing changes.
	hub.publish(t, "1.2.0", priv)
	hub.badSHA = true
	srv.hubDropCaches(ctx)
	_, body, _ = get(t, c, base+"/wp-admin/update-core")
	nonce := formNonce(t, body[strings.Index(body, "upgrade-plugins"):])
	_, _, loc = post(t, c, base+"/wp-admin/update-core?action=do-plugin-upgrade", url.Values{"_wpnonce": {nonce}, "checked[]": {"word-counter"}})
	if !strings.Contains(loc, "checksum") {
		t.Errorf("bad sha256 not reported: %s", loc)
	}
	// A package signed by a key the Manager does not list is refused.
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	hub.badSHA = false
	hub.publish(t, "1.2.0", stranger)
	srv.hubDropCaches(ctx)
	_, _, loc = post(t, c, base+"/wp-admin/update-core?action=do-plugin-upgrade", url.Values{"_wpnonce": {nonce}, "checked[]": {"word-counter"}})
	if !strings.Contains(loc, "signature") {
		t.Errorf("untrusted signature not reported: %s", loc)
	}
	if v, st, _ := status(); v != "1.1.0" || st != "active" {
		t.Errorf("a refused update changed the plugin: %s %s", v, st)
	}
	// Dashboard › Updates › Update Plugins.
	hub.publish(t, "1.2.0", priv)
	srv.hubDropCaches(ctx)
	_, _, loc = post(t, c, base+"/wp-admin/update-core?action=do-plugin-upgrade", url.Values{"_wpnonce": {nonce}, "checked[]": {"word-counter"}})
	if !strings.Contains(loc, "plugins-updated=Word+Counter+1.2.0") {
		t.Errorf("Update Plugins: %s", loc)
	}
	if v, st, _ := status(); v != "1.2.0" || st != "active" {
		t.Errorf("after Update Plugins: %s %s", v, st)
	}
	_, body, _ = get(t, c, base+"/wp-admin/update-core")
	if !strings.Contains(body, "Your plugins are all up to date.") {
		t.Error("the updated plugin is still listed")
	}
	srv.hubFlushEvents(ctx)
	if got := hub.eventTypes(); slices.Index(got, "plugin_update:word-counter") < 0 {
		t.Errorf("no plugin_update event: %v", got)
	}
}

// signedCore is a fake core binary and its release artifact signed with priv.
func signedCore(version, goos, goarch string, bin []byte, priv ed25519.PrivateKey, keyID string) *coreArtifact {
	sum := sha256.Sum256(bin)
	sumHex := hex.EncodeToString(sum[:])
	return &coreArtifact{OS: goos, Arch: goarch, Size: int64(len(bin)), SHA256: sumHex, KeyID: keyID,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, coreSignedMessage(version, goos, goarch, sumHex)))}
}

func TestCoreArtifactInstall(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	keys := []hubKey{{ID: "hub-2026", Public: base64.StdEncoding.EncodeToString(pub)}}
	bin := []byte("\x7fELF new pursae binary")
	dir := t.TempDir()
	target := filepath.Join(dir, "pursae")
	reset := func() {
		if err := os.WriteFile(target, []byte("old binary"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	unchanged := func(name string) {
		t.Helper()
		if b, _ := os.ReadFile(target); string(b) != "old binary" {
			t.Errorf("%s: the executable was replaced", name)
		}
		if m, _ := filepath.Glob(filepath.Join(dir, ".*")); len(m) > 0 {
			t.Errorf("%s: temporary files left: %v", name, m)
		}
	}
	for _, tc := range []struct {
		name string
		art  func() *coreArtifact
		body []byte
	}{
		{"bad sha256", func() *coreArtifact {
			a := signedCore("0.2.0", "linux", "amd64", bin, priv, "hub-2026")
			a.SHA256 = strings.Repeat("ab", 32)
			return a
		}, bin},
		{"tampered body", func() *coreArtifact { return signedCore("0.2.0", "linux", "amd64", bin, priv, "hub-2026") }, append([]byte("x"), bin[1:]...)},
		{"signature of another version", func() *coreArtifact { return signedCore("0.1.9", "linux", "amd64", bin, priv, "hub-2026") }, bin},
		{"signature of another platform", func() *coreArtifact {
			a := signedCore("0.2.0", "darwin", "arm64", bin, priv, "hub-2026")
			a.OS, a.Arch = "linux", "amd64"
			return a
		}, bin},
		{"unknown key", func() *coreArtifact { return signedCore("0.2.0", "linux", "amd64", bin, priv, "other") }, bin},
		{"wrong size", func() *coreArtifact {
			a := signedCore("0.2.0", "linux", "amd64", bin, priv, "hub-2026")
			a.Size++
			return a
		}, bin},
	} {
		reset()
		if err := installCoreArtifact(target, "0.2.0", tc.art(), keys, bytes.NewReader(tc.body), nil); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
		unchanged(tc.name)
	}
	reset()
	if err := installCoreArtifact(target, "0.2.0", signedCore("0.2.0", "linux", "amd64", bin, priv, "hub-2026"), keys, bytes.NewReader(bin), nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); !bytes.Equal(b, bin) {
		t.Error("the executable was not replaced")
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}

	// Executables that cannot be replaced.
	goRun := filepath.Join(dir, "go-build123", "b001", "exe", "wpgo")
	os.MkdirAll(filepath.Dir(goRun), 0o755)
	os.WriteFile(goRun, bin, 0o755)
	if err := coreReplaceable(goRun); err == nil || !strings.Contains(err.Error(), "go run") {
		t.Errorf("go run binary: %v", err)
	}
	if os.Geteuid() != 0 {
		ro := filepath.Join(t.TempDir(), "ro")
		os.MkdirAll(ro, 0o755)
		roExe := filepath.Join(ro, "pursae")
		os.WriteFile(roExe, bin, 0o755)
		os.Chmod(ro, 0o555)
		t.Cleanup(func() { os.Chmod(ro, 0o755) })
		if err := coreReplaceable(roExe); err == nil || !strings.Contains(err.Error(), "not writable") {
			t.Errorf("read-only directory: %v", err)
		}
	}
	if err := coreReplaceable(target); err != nil {
		t.Errorf("writable executable refused: %v", err)
	}
}

func TestCoreSelfUpdate(t *testing.T) {
	withRootKeys(t)           // a development build: no pinned release key …
	withTrustHubKeys(t, true) // … with the opt-in to the Manager's keys (hub_trust.go)
	base, c, srv, hub := managerSite(t)
	ctx := context.Background()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	hub.keys = append(hub.keys, map[string]string{"id": "hub-2026", "public": base64.StdEncoding.EncodeToString(pub)})
	bin := []byte("\x7fELF pursae 9.9.9")
	art := signedCore("9.9.9", runtime.GOOS, runtime.GOARCH, bin, priv, "hub-2026")
	art.URL = hub.URL + "/api/v1/core/9.9.9/" + runtime.GOOS + "-" + runtime.GOARCH
	hub.coreBin = bin
	hub.core["artifacts"] = []*coreArtifact{art}
	srv.hubDropCaches(ctx)

	target := filepath.Join(t.TempDir(), "pursae")
	os.WriteFile(target, []byte("old binary"), 0o755)
	restarted := make(chan string, 1)
	oldExe, oldRestart := coreExecutable, coreRestart
	t.Cleanup(func() { coreExecutable, coreRestart = oldExe, oldRestart })
	coreRestart = func(exe string) error { restarted <- exe; return nil }

	// go run: refused with a reason on the screen.
	coreExecutable = func() (string, error) { return "/tmp/go-build4242/b001/exe/wpgo", nil }
	_, body, _ := get(t, c, base+"/wp-admin/update-core")
	if !strings.Contains(body, "This site cannot update itself") || !strings.Contains(body, "go run") || strings.Contains(body, "Update to version 9.9.9") {
		t.Error("a go run build offers the self-update")
	}

	coreExecutable = func() (string, error) { return target, nil }
	_, body, _ = get(t, c, base+"/wp-admin/update-core")
	if !strings.Contains(body, `value="Update to version 9.9.9"`) || !strings.Contains(body, `action="/wp-admin/update-core?action=do-core-upgrade"`) {
		t.Fatal("no Update to version button")
	}
	nonce := formNonce(t, body[strings.Index(body, "do-core-upgrade"):])
	if code, _, _ := post(t, c, base+"/wp-admin/update-core?action=do-core-upgrade", url.Values{"_wpnonce": {"bad"}}); code == http.StatusOK {
		t.Error("core update without a valid nonce")
	}
	code, body, _ := post(t, c, base+"/wp-admin/update-core?action=do-core-upgrade", url.Values{"_wpnonce": {nonce}})
	if code != http.StatusOK || !strings.Contains(body, "Updated to Pursae 9.9.9, restarting") {
		t.Fatalf("core update: %d %s", code, body)
	}
	select {
	case exe := <-restarted:
		if exe != target {
			t.Errorf("restarted %s", exe)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no restart")
	}
	if b, _ := os.ReadFile(target); !bytes.Equal(b, bin) {
		t.Error("the executable was not replaced")
	}
	if got := hub.eventTypes(); slices.Index(got, "core_update:") < 0 {
		t.Errorf("no core_update event: %v", got)
	}

	// A tampered download is refused with the reason.
	os.WriteFile(target, []byte("old binary"), 0o755)
	hub.mu.Lock()
	hub.coreBin = []byte("\x7fELF pursae 6.6.6") // same size, other bytes
	hub.mu.Unlock()
	_, _, loc := post(t, c, base+"/wp-admin/update-core?action=do-core-upgrade", url.Values{"_wpnonce": {nonce}})
	if !strings.Contains(loc, "error=") || !strings.Contains(loc, "SHA-256") {
		t.Errorf("tampered binary: %s", loc)
	}
	if b, _ := os.ReadFile(target); string(b) != "old binary" {
		t.Error("a tampered binary replaced the executable")
	}

	// An imported release: the Manager redirects to the GitHub asset; the site follows, verifies
	// and installs it, and its license and site headers stay with the Manager.
	var assetHeaders http.Header
	assetBin := bin
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assetHeaders = r.Header.Clone()
		w.Write(assetBin)
	}))
	t.Cleanup(gh.Close)
	hub.mu.Lock()
	hub.coreBin, hub.coreAt = nil, gh.URL+"/pursae/pursae/releases/download/v9.9.9/pursae-"+runtime.GOOS+"-"+runtime.GOARCH
	hub.mu.Unlock()
	code, body, _ = post(t, c, base+"/wp-admin/update-core?action=do-core-upgrade", url.Values{"_wpnonce": {nonce}})
	if code != http.StatusOK || !strings.Contains(body, "Updated to Pursae 9.9.9, restarting") {
		t.Fatalf("core update through a redirect: %d %s", code, body)
	}
	<-restarted
	if b, _ := os.ReadFile(target); !bytes.Equal(b, bin) {
		t.Error("the redirected download was not installed")
	}
	if assetHeaders == nil || assetHeaders.Get("X-Pursae-License") != "" || assetHeaders.Get("X-Pursae-Site") != "" {
		t.Errorf("the asset host got the site's credentials: %v", assetHeaders)
	}
	// … and a file replaced on GitHub after the import is refused.
	os.WriteFile(target, []byte("old binary"), 0o755)
	assetBin = []byte("\x7fELF pursae 6.6.6")
	_, _, loc = post(t, c, base+"/wp-admin/update-core?action=do-core-upgrade", url.Values{"_wpnonce": {nonce}})
	if !strings.Contains(loc, "error=") || !strings.Contains(loc, "SHA-256") {
		t.Errorf("replaced GitHub asset: %s", loc)
	}
	if b, _ := os.ReadFile(target); string(b) != "old binary" {
		t.Error("a replaced GitHub asset was installed")
	}
}

// The fake binaries of these tests cannot run; coreTryBinary has its own test below.
func init() { coreTryBinary = func(string, string) error { return nil } }

func TestCoreTryBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	os.WriteFile(good, []byte("#!/bin/sh\necho Pursae 0.5.0\n"), 0o755)
	bad := filepath.Join(dir, "bad")
	os.WriteFile(bad, []byte("#!/nonexistent/interpreter\n"), 0o755)
	saved := coreTryBinary
	coreTryBinary = realCoreTryBinary
	defer func() { coreTryBinary = saved }()
	if err := coreTryBinary(good, "0.5.0"); err != nil {
		t.Errorf("good binary: %v", err)
	}
	if err := coreTryBinary(good, "0.6.0"); err == nil {
		t.Error("a binary reporting another version was accepted")
	}
	if err := coreTryBinary(bad, "0.5.0"); err == nil || !strings.Contains(err.Error(), "does not run") {
		t.Errorf("a binary that cannot start was accepted: %v", err)
	}
}
