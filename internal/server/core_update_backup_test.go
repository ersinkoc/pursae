package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Update to version X takes the same pre-update backup as the automatic update (database, plugin
// packages and theme files, no uploads) and installs nothing when that backup fails.
func TestCoreManualUpdateBacksUpFirst(t *testing.T) {
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
	oldExe, oldRestart, oldBackup := coreExecutable, coreRestart, corePreUpdateBackup
	t.Cleanup(func() { coreExecutable, coreRestart, corePreUpdateBackup = oldExe, oldRestart, oldBackup })
	coreRestart = func(exe string) error { restarted <- exe; return nil }
	coreExecutable = func() (string, error) { return target, nil }
	pkgDir, _ := srv.pluginDirs()
	os.MkdirAll(pkgDir, 0o755)
	os.WriteFile(filepath.Join(pkgDir, "mine.zip"), []byte("my own plugin"), 0o644)
	upl := filepath.Join(srv.cfg.UploadsDir, "photo.jpg")
	os.MkdirAll(filepath.Dir(upl), 0o755)
	os.WriteFile(upl, []byte("jpeg"), 0o644)

	_, body, _ := get(t, c, base+"/wp-admin/update-core")
	nonce := formNonce(t, body[strings.Index(body, "do-core-upgrade"):])

	// The backup fails: nothing is downloaded or replaced, and the reason is shown.
	corePreUpdateBackup = func(*Server, context.Context, string) (string, error) {
		return "", errors.New("not enough free disk space")
	}
	_, _, loc := post(t, c, base+"/wp-admin/update-core?action=do-core-upgrade", url.Values{"_wpnonce": {nonce}})
	if !strings.Contains(loc, "error=") || !strings.Contains(loc, url.QueryEscape("The backup before the update failed")) {
		t.Fatalf("a failed backup did not stop the update: %s", loc)
	}
	if b, _ := os.ReadFile(target); string(b) != "old binary" {
		t.Fatal("updated without a backup")
	}
	for i := 0; i < 100 && coreBusy.Load(); i++ {
		time.Sleep(20 * time.Millisecond)
	}

	// The real backup: taken, then the update goes ahead.
	var name string
	corePreUpdateBackup = func(s *Server, ctx context.Context, to string) (string, error) {
		n, err := oldBackup(s, ctx, to)
		name = n
		return n, err
	}
	code, body, _ := post(t, c, base+"/wp-admin/update-core?action=do-core-upgrade", url.Values{"_wpnonce": {nonce}})
	if code != http.StatusOK || !strings.Contains(body, "Updated to Pursae 9.9.9, restarting") {
		t.Fatalf("core update: %d %s", code, body)
	}
	<-restarted
	if name == "" {
		t.Fatal("no backup before the manual update")
	}
	m, err := bmgrReadManifest(filepath.Join(srv.bmgrDir(), name))
	if err != nil || m.Kind != bmgrPreUpdateKind || len(m.Tables) == 0 || !strings.Contains(m.Note, "to 9.9.9") || strings.Contains(m.Note, "automatic update") {
		t.Fatalf("pre-update backup %s: %+v %v", name, m, err)
	}
	groups := map[string]int{}
	for _, f := range m.Files {
		groups[f.Group] = f.Count
	}
	if groups["plugins"] < 1 {
		t.Errorf("the plugin packages are not in the backup: %+v", m.Files)
	}
	if _, ok := groups["uploads"]; ok {
		t.Errorf("the media uploads are in the backup: %+v", m.Files)
	}
}
