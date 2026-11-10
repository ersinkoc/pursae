package server

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/coreupdate"
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
	// The listening socket goes to the new binary and requests in flight finish first, so visitors
	// wait a moment instead of getting an error (internal/coreupdate/listener.go).
	coreRestart = func(exe string) error { return coreupdate.Restart(exe, 10*time.Second) }
	coreExit    = os.Exit
)

// coreBusy is held from the start of a core update or reinstall until the restart (or the failure):
// two at once would each keep "the running binary" as <exe>.prev, the second one keeping the first
// one's new binary, so a rollback would go back to the version it is rolling back from.
var coreBusy atomic.Bool

// errCoreBusy refuses a second core update while one is running.
var errCoreBusy = errors.New("another core update is already running; wait for the site to restart")

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
// against the release: size, checksum, and the signature by one of keys, the Manager keys this build
// trusts for releases (coreReleaseKeys: endorsed by a pinned root key, hub_trust.go).
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
		return fmt.Errorf("the release is signed by %q, which is not a release key this build trusts", art.KeyID)
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
	if err := coreTrustCheck(); err != nil { // no root key to verify a release with (hub_trust.go)
		return "", err
	}
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
	// The new binary is written next to exe and renamed over it, so only the directory has to be
	// writable. (Opening a running executable for writing fails on Linux with "text file busy".)
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
// before runs right before the rename (core_update keeps the running binary for a rollback there).
func installCoreArtifact(target, version string, art *coreArtifact, keys []hubKey, body io.Reader, before func() error) error {
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
	if err := coreTryBinary(tmp, version); err != nil {
		return err
	}
	if before != nil {
		if err := before(); err != nil {
			return err
		}
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
	keys, err := coreReleaseKeys(idx.Keys) // endorsed by a root key compiled into this build
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := s.hubNewRequest(ctx, http.MethodGet, art.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{CheckRedirect: hubCheckRedirect}).Do(req)
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
	// The running binary stays as <exe>.prev until the new one has started and answered requests;
	// if it dies before that, the next start goes back to it (internal/coreupdate).
	keep := func() error { return coreupdate.Prepare(target, Version, idx.Core.Latest) }
	if err := installCoreArtifact(target, idx.Core.Latest, art, keys, resp.Body, keep); err != nil {
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
		c.Redirect(http.StatusFound, "/wp-admin/update-core?error="+url.QueryEscape(s.updateFailed(c, msg)))
	}
	if !coreBusy.CompareAndSwap(false, true) {
		fail(errCoreBusy.Error())
		return
	}
	handedOver := false
	defer func() {
		if !handedOver {
			coreBusy.Store(false)
		}
	}()
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
	handedOver = true
	s.coreRestartInto(c, target, version, false)
}

// coreRestartInto answers the "restarting" page and hands the site over to the binary now at target
// (an update or a reinstall of the previous version).
func (s *Server) coreRestartInto(c *gin.Context, target, version string, reinstall bool) {
	s.adminPage(c, "update-core", "Pursae Updates", "update-core-php", "admin/update-core-restart", map[string]any{"Version": version, "Reinstall": reinstall})
	c.Writer.Flush()
	s.coreRestartLater(target)
}

// coreRestartLater hands the site over to the binary at target a second from now (after the answer has
// left); coreBusy is released only when the hand-over did not happen.
func (s *Server) coreRestartLater(target string) {
	go func() {
		defer coreBusy.Store(false) // reached only when the exec did not happen (or in tests)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.hubFlushEvents(ctx)
		cancel()
		time.Sleep(time.Second) // let the page reach the browser
		if err := coreRestart(target); err != nil {
			// Still running the old code: put the old binary back so the next start is not the new one.
			slog.Error("core update: restart failed, going back to the previous binary", "err", err)
			coreupdate.Restore(target, err)
			if coreupdate.Stopped() {
				// The listener was already shut down for the hand-over and an http.Server cannot serve
				// again: exit so the supervisor (Docker, systemd) starts the restored binary.
				slog.Error("core update: server already stopped; exiting so the supervisor restarts it")
				coreExit(1)
			}
		}
	}()
}

// coreTryBinary runs the downloaded binary once (`version`) before it replaces the running one: a
// binary this system cannot start (wrong platform, a missing ELF interpreter, …) is refused while
// nothing has changed yet.
var coreTryBinary = realCoreTryBinary

func realCoreTryBinary(path, version string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("the new binary does not run on this system (%v); nothing was changed", err)
	}
	if !strings.Contains(string(out), version) {
		return fmt.Errorf("the new binary reports %q instead of %s; nothing was changed", strings.TrimSpace(string(out)), version)
	}
	return nil
}

// corePrevious is the version the running core was updated from and can go back to: its binary is
// still kept next to target (keptUntil set) or it can be downloaded again from Pursae Manager. from
// is "" when there is none (no update recorded, already the older version, or nowhere to get it).
func (s *Server) corePrevious(target string) (from string, keptUntil time.Time) {
	from, keptUntil = coreupdate.Previous(target, Version)
	if from == "" || !coreupdate.Less(from, Version) || (keptUntil.IsZero() && s.hubBase() == "") {
		return "", time.Time{}
	}
	return from, keptUntil
}

// reinstallCore is Dashboard › Updates › Reinstall version X (action=do-core-reinstall): back to the
// version the site was updated from. The kept binary is swapped back while it is there (24 hours
// after the update); later the signed build is downloaded again from Pursae Manager
// (GET /api/v1/core/{version}). Database migrations of the newer version are not undone.
func (s *Server) reinstallCore(c *gin.Context) {
	if !s.checkNonce(c, "upgrade-core") {
		s.denyNonce(c)
		return
	}
	fail := func(msg string) {
		c.Redirect(http.StatusFound, "/wp-admin/update-core?error="+url.QueryEscape(s.updateFailed(c, msg)))
	}
	if !coreBusy.CompareAndSwap(false, true) {
		fail(errCoreBusy.Error())
		return
	}
	handedOver := false
	defer func() {
		if !handedOver {
			coreBusy.Store(false)
		}
	}()
	exe, err := coreExecutable()
	if err != nil {
		fail("the running executable cannot be found: " + err.Error())
		return
	}
	if err := coreReplaceable(exe); err != nil {
		fail(err.Error())
		return
	}
	prev, kept := s.corePrevious(exe)
	if prev == "" || c.PostForm("version") != prev {
		fail("there is no previous version to reinstall")
		return
	}
	if !kept.IsZero() {
		if err := coreTryBinary(coreupdate.PrevPath(exe), prev); err != nil {
			fail(err.Error())
			return
		}
		err = coreupdate.SwapPrev(exe, Version, prev)
	} else {
		var target string
		if target, err = coreUpdateTarget(); err == nil {
			err = s.coreDownload(c.Request.Context(), target, prev)
		}
	}
	if err != nil {
		fail(err.Error())
		return
	}
	slog.Info("core reinstalled, restarting", "from", Version, "to", prev, "exe", exe, "local", !kept.IsZero())
	s.hubEvent("core_update", "core", "", prev)
	handedOver = true
	s.coreRestartInto(c, exe, prev, true)
}

// coreDownload installs the signed build of one release, fetched from Pursae Manager's
// GET /api/v1/core/{version}, over target; the running binary is kept as <exe>.prev.
func (s *Server) coreDownload(ctx context.Context, target, version string) error {
	base := s.hubBase()
	if base == "" {
		return errHubOff
	}
	idx, err := s.hubFetchIndex(ctx) // fresh keys
	if err != nil {
		return err
	}
	keys, err := coreReleaseKeys(idx.Keys)
	if err != nil {
		return err
	}
	req, err := s.hubNewRequest(ctx, http.MethodGet, base+"/api/v1/core/"+url.PathEscape(version), nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{CheckRedirect: hubCheckRedirect, Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	var rel struct {
		Core *hubCore `json:"core"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel)
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("Pursae %s needs a license for its channel (Settings › License)", version)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("Pursae Manager does not offer %s (any more)", version)
	case resp.StatusCode != http.StatusOK || err != nil || rel.Core == nil || rel.Core.Latest != version:
		return fmt.Errorf("Pursae Manager: no release information for %s (HTTP %d)", version, resp.StatusCode)
	}
	art := rel.Core.artifactFor(runtime.GOOS, runtime.GOARCH)
	if art == nil {
		return fmt.Errorf("the release %s has no build for %s/%s", version, runtime.GOOS, runtime.GOARCH)
	}
	if !strings.HasPrefix(art.URL, base+"/") { // only the Manager itself serves binaries
		return errors.New("the release binary is not served by Pursae Manager")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if req, err = s.hubNewRequest(ctx, http.MethodGet, art.URL, nil); err != nil {
		return err
	}
	if resp, err = (&http.Client{CheckRedirect: hubCheckRedirect}).Do(req); err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	keep := func() error { return coreupdate.Prepare(target, Version, version) }
	return installCoreArtifact(target, version, art, keys, resp.Body, keep)
}
