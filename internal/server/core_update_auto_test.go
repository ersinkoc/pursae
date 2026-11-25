package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"wpgo/internal/coreupdate"
)

// Dashboard › Updates › Automatic updates: off by default; on, the daily check installs a newer
// release and restarts; a release the site went back from is not tried again by itself.
func TestCoreAutoUpdate(t *testing.T) {
	savedVersion := Version
	Version = "9.9.0" // a patch release of the running minor version: installed automatically
	t.Cleanup(func() { Version = savedVersion })
	withRootKeys(t)
	withTrustHubKeys(t, true)
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
	coreExecutable = func() (string, error) { return target, nil }
	waitIdle := func() {
		t.Helper()
		for i := 0; i < 100 && coreBusy.Load(); i++ {
			time.Sleep(50 * time.Millisecond)
		}
		if coreBusy.Load() {
			t.Fatal("coreBusy was not released")
		}
	}

	_, body, _ := get(t, c, base+"/wp-admin/update-core") // reads the index: 9.9.9 is the latest
	if !strings.Contains(body, `id="core-auto-updates"`) || !strings.Contains(body, `value="Enable automatic updates"`) {
		t.Fatal("no Automatic updates section, or it is not off by default")
	}
	if v := srv.coreAutoUpdate(ctx); v != "" {
		t.Fatalf("updated with automatic updates off: %q", v)
	}
	if b, _ := os.ReadFile(target); string(b) != "old binary" {
		t.Fatal("the executable was replaced with automatic updates off")
	}

	nonce := formNonce(t, body[strings.Index(body, "core-auto-update"):])
	_, _, loc := post(t, c, base+"/wp-admin/update-core?action=core-auto-update", url.Values{"_wpnonce": {nonce}, "enable": {"1"}})
	if !strings.Contains(loc, "auto-update=enabled") || !srv.settings.Bool(coreAutoUpdateOption) {
		t.Fatalf("enabling: %s", loc)
	}

	// A failed download changes nothing and is recorded.
	hub.mu.Lock()
	hub.coreBin = []byte("\x7fELF pursae 6.6.6")
	hub.mu.Unlock()
	if v := srv.coreAutoUpdate(ctx); v != "" {
		t.Fatalf("a tampered release was installed: %q", v)
	}
	if last := srv.coreAutoLast(); last == nil || last.Status != "failed" || !strings.Contains(last.Error, "SHA-256") {
		t.Fatalf("failure not recorded: %+v", last)
	}
	if b, _ := os.ReadFile(target); string(b) != "old binary" {
		t.Fatal("a tampered binary replaced the executable")
	}
	waitIdle()

	hub.mu.Lock()
	hub.coreBin = bin
	hub.mu.Unlock()
	if v := srv.coreAutoUpdate(ctx); v != "9.9.9" {
		t.Fatalf("automatic update: %q", v)
	}
	select {
	case exe := <-restarted:
		if exe != target {
			t.Errorf("restarted %s", exe)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no restart")
	}
	waitIdle()
	if b, _ := os.ReadFile(target); !bytes.Equal(b, bin) {
		t.Fatal("the executable was not replaced")
	}
	// The database was backed up first, into a Backup Manager archive, with the plugin off.
	last := srv.coreAutoLast()
	if last == nil || last.Backup == "" {
		t.Fatalf("no pre-update backup recorded: %+v", last)
	}
	if m, err := bmgrReadManifest(filepath.Join(srv.bmgrDir(), last.Backup)); err != nil || m.Kind != bmgrPreUpdateKind || len(m.Tables) == 0 {
		t.Fatalf("pre-update backup %s: %+v %v", last.Backup, m, err)
	} else {
		for _, f := range m.Files { // plugin packages and theme files, never the media uploads
			if f.Group != "plugins" && f.Group != "themes" {
				t.Fatalf("pre-update backup holds the %s files", f.Group)
			}
		}
	}
	if st := coreupdate.Read(target); st == nil || st.Status != "pending" || st.To != "9.9.9" {
		t.Fatalf("no pending update recorded: %+v", st)
	}

	// 9.9.9 never answered: confirmUpdate put the old binary back.
	if !coreupdate.Rollback(target, "9.9.9") {
		t.Fatal("no rollback")
	}
	if b, _ := os.ReadFile(target); string(b) != "old binary" {
		t.Fatal("the previous binary was not restored")
	}
	if v := srv.coreAutoUpdate(ctx); v != "" {
		t.Fatalf("a rolled back release was tried again: %q", v)
	}
	if srv.settings.String(coreAutoSkipOption) != "9.9.9" {
		t.Fatal("the rolled back release was not remembered")
	}
	_, body, _ = get(t, c, base+"/wp-admin/update-core")
	if !strings.Contains(body, "did not answer requests") || !strings.Contains(body, "Pursae 9.9.9 was rolled back before") ||
		!strings.Contains(body, "rolled back to the previous version") {
		t.Errorf("the Updates screen does not explain the rollback:\n%s", body[strings.Index(body, "<h1>"):])
	}
	if coreupdate.Read(target) != nil {
		t.Error("the rollback notice was not cleared")
	}
	if v := srv.coreAutoUpdate(ctx); v != "" { // still skipped once the notice is gone
		t.Fatalf("a rolled back release was tried again after the notice: %q", v)
	}
	if b, _ := os.ReadFile(target); string(b) != "old binary" {
		t.Fatal("the executable changed")
	}

	_, body, _ = get(t, c, base+"/wp-admin/update-core")
	nonce = formNonce(t, body[strings.Index(body, "core-auto-update"):])
	post(t, c, base+"/wp-admin/update-core?action=core-auto-update", url.Values{"_wpnonce": {nonce}, "enable": {"0"}})
	if srv.settings.Bool(coreAutoUpdateOption) {
		t.Error("automatic updates still on")
	}
}
