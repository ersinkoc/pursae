package server

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/plugins"
)

// Core self-update (Dashboard › Updates › Update to version X). A release built by Pursae Manager
// carries one binary per platform in /api/v1/index core.artifacts (pursae-manager/API.md, "Core
// artifacts"). The site downloads the one for its GOOS/GOARCH from the Manager, checks the size and
// SHA-256, verifies the Ed25519 signature by the Manager key key_id over
// "pursae-core-v1\n{version}\n{os}-{arch}\n{sha256 hex}", writes it next to the running executable
// (temporary file, fsync, chmod 0755, rename over the old one), answers "Updated, restarting…" and
// re-executes itself. Database migrations run on start as always.
//
// It refuses when the executable cannot be replaced: a `go run` build in the Go build cache, or a
// file (or directory) the server may not write, as in the container image; such sites update by
// pulling the new image or replacing the binary themselves.

const coreMaxArtifact = 1 << 30 // the binary embeds the admin, the builder and 135 languages (~300 MB)

// coreArtifact is one binary of a core release.
type coreArtifact struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
	KeyID     string `json:"key_id"`
}

// Hooks for tests: the running executable and the restart.
var (
	coreExecutable = func() (string, error) {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(exe)
	}
	coreRestart = func(exe string) error { return syscall.Exec(exe, os.Args, os.Environ()) }
)

// artifactFor is the release's binary for this platform.
func (c *hubCore) artifactFor(goos, goarch string) *coreArtifact {
	if c == nil {
		return nil
	}
	for i := range c.Artifacts {
		if c.Artifacts[i].OS == goos && c.Artifacts[i].Arch == goarch {
			return &c.Artifacts[i]
		}
	}
	return nil
}

// coreSignedMessage is what the Manager signs for one artifact.
func coreSignedMessage(version, goos, goarch, sumHex string) []byte {
	return []byte("pursae-core-v1\n" + version + "\n" + goos + "-" + goarch + "\n" + strings.ToLower(sumHex))
}

// verifyCoreArtifact checks a downloaded binary (its size and SHA-256 as computed while it was written)
// against the release: size, checksum, and the signature by a key the Manager lists.
func verifyCoreArtifact(version string, art *coreArtifact, keys []hubKey, size int64, sum []byte) error {
	if art.Size > 0 && size != art.Size {
		return fmt.Errorf("the download has %d bytes, the release says %d", size, art.Size)
	}
	sumHex := hex.EncodeToString(sum)
	if !strings.EqualFold(sumHex, art.SHA256) {
		return errors.New("the download does not match the published SHA-256")
	}
	var pub ed25519.PublicKey
	for _, k := range keys {
		if k.ID == art.KeyID {
			p, err := plugins.ParsePublicKey(k.Public)
			if err != nil {
				return fmt.Errorf("the signing key %q is not a valid Ed25519 key", k.ID)
			}
			pub = p
		}
	}
	if pub == nil {
		return fmt.Errorf("the release is signed by %q, which Pursae Manager does not list", art.KeyID)
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(art.Signature))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("the release signature is malformed")
	}
	if !ed25519.Verify(pub, coreSignedMessage(version, art.OS, art.Arch, sumHex), sig) {
		return errors.New("the release signature does not verify")
	}
	return nil
}

// coreUpdateTarget is the executable to replace, or why it cannot be replaced.
func coreUpdateTarget() (string, error) {
	exe, err := coreExecutable()
	if err != nil {
		return "", fmt.Errorf("the running executable cannot be found: %v", err)
	}
	if err := coreReplaceable(exe); err != nil {
		return "", err
	}
	return exe, nil
}

// coreReplaceable reports why the binary at exe cannot be replaced in place.
func coreReplaceable(exe string) error {
	slash := filepath.ToSlash(exe)
	if strings.Contains(slash, "/go-build") {
		return errors.New("Pursae is running from `go run` (a temporary build); build a binary or update the source instead")
	}
	fi, err := os.Stat(exe)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return errors.New("the executable is not a regular file")
	}
	if f, err := os.OpenFile(exe, os.O_WRONLY, 0); err != nil {
		return fmt.Errorf("the executable %s is not writable by the server; replace it (or pull the new container image) yourself", exe)
	} else {
		f.Close()
	}
	probe, err := os.CreateTemp(filepath.Dir(exe), ".pursae-update-probe-")
	if err != nil {
		return fmt.Errorf("the directory %s is not writable by the server; replace the executable (or pull the new container image) yourself", filepath.Dir(exe))
	}
	probe.Close()
	_ = os.Remove(probe.Name())
	return nil
}

// installCoreArtifact streams body into a temporary file next to target, verifies it and renames it over
// target. Nothing is replaced unless the size, SHA-256 and signature match.
func installCoreArtifact(target, version string, art *coreArtifact, keys []hubKey, body io.Reader) error {
	f, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".update-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			f.Close()
			_ = os.Remove(tmp)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(body, coreMaxArtifact+1))
	if err != nil {
		return fmt.Errorf("download: %v", err)
	}
	if n > coreMaxArtifact {
		return fmt.Errorf("the download is larger than %d MiB", coreMaxArtifact>>20)
	}
	if err := verifyCoreArtifact(version, art, keys, n, h.Sum(nil)); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	ok = true
	if d, err := os.Open(filepath.Dir(target)); err == nil { // make the rename durable
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// coreUpdate downloads, verifies and installs the release's binary over target.
func (s *Server) coreUpdate(ctx context.Context, target string) (string, error) {
	base := s.hubBase()
	if base == "" {
		return "", errHubOff
	}
	idx, err := s.hubFetchIndex(ctx) // fresh: the current release and keys
	if err != nil {
		return "", err
	}
	if idx.Core == nil || !versionLess(Version, idx.Core.Latest) {
		return "", errors.New("there is no newer release")
	}
	art := idx.Core.artifactFor(runtime.GOOS, runtime.GOARCH)
	if art == nil {
		return "", fmt.Errorf("the release has no build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if !strings.HasPrefix(art.URL, base+"/") { // only the Manager itself serves binaries
		return "", errors.New("the release binary is not served by Pursae Manager")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := s.hubNewRequest(ctx, http.MethodGet, art.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return "", errors.New("this release needs a license for its channel (Settings › License)")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	if err := installCoreArtifact(target, idx.Core.Latest, art, idx.Keys, resp.Body); err != nil {
		return "", err
	}
	return idx.Core.Latest, nil
}

// upgradeCore is Dashboard › Updates › Update to version X (update-core.php?action=do-core-upgrade).
func (s *Server) upgradeCore(c *gin.Context) {
	if !s.checkNonce(c, "upgrade-core") {
		s.denyNonce(c)
		return
	}
	fail := func(msg string) {
		c.Redirect(http.StatusFound, "/wp-admin/update-core?error="+url.QueryEscape("Update failed: "+msg))
	}
	target, err := coreUpdateTarget()
	if err != nil {
		fail(err.Error())
		return
	}
	version, err := s.coreUpdate(c.Request.Context(), target)
	if err != nil {
		fail(err.Error())
		return
	}
	slog.Info("core updated, restarting", "from", Version, "to", version, "exe", target)
	s.hubEvent("core_update", "core", "", version)
	s.adminPage(c, "update-core", "Pursae Updates", "update-core-php", "admin/update-core-restart", map[string]any{"Version": version})
	c.Writer.Flush()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.hubFlushEvents(ctx)
		cancel()
		time.Sleep(time.Second) // let the page reach the browser
		if err := coreRestart(target); err != nil {
			slog.Error("core update: restart", "err", err)
		}
	}()
}
