package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/coreupdate"
)

// Automatic core updates (Dashboard › Updates › Automatic updates, off by default). With the option
// on, the daily wpgo_hub_check installs a newer core release by itself, exactly like "Update to
// version X": the binary is downloaded, verified and swapped in, and the site restarts into it.
//
// A failed update goes back to the previous version without anyone stepping in:
//   - the download, the signature or the trial run of the new binary fails: nothing was changed;
//   - the new binary cannot be started: the old one is put back at once (coreupdate.Restore);
//   - it dies while starting: the next start restores the previous binary (coreupdate.Guard);
//   - it starts but never answers requests: confirmUpdate restores the previous binary and restarts
//     into it (coreupdate.Rollback).
//
// A release that was rolled back is remembered (coreAutoSkipOption) and not tried again by itself,
// so a broken release does not make the site restart every day; a newer release, or an update made
// by hand, is tried as usual. The outcome of the last attempt is shown on the Updates screen.
//
// Two more limits keep an unattended update safe:
//   - it stays within the running minor version: 0.3.1 → 0.3.2 is automatic, 0.3.x → 0.4.0 waits for
//     a click on "Update to version X" (a new minor may bring migrations or changes worth reading the
//     release notes for);
//   - the database is backed up first (preUpdateBackup, a database-only Backup Manager archive, also
//     with the plugin off); when that backup fails the update is skipped, and the newest
//     preUpdateBackupKeep of these archives are kept.

const (
	coreAutoUpdateOption = "pursae_core_auto_update"      // "1" when on
	coreAutoSkipOption   = "pursae_core_auto_update_skip" // a release that was rolled back
	coreAutoLastOption   = "pursae_core_auto_update_last" // coreAutoResult of the last attempt
)

// coreAutoResult is the last automatic core update attempt.
type coreAutoResult struct {
	Version string    `json:"version"`
	At      time.Time `json:"at"`
	Status  string    `json:"status"` // started | failed | rolled_back | skipped (backup failed) | manual (new minor)
	Error   string    `json:"error,omitempty"`
	Backup  string    `json:"backup,omitempty"` // the pre-update backup archive
}

func (s *Server) coreAutoLast() *coreAutoResult {
	v := s.settings.Get(coreAutoLastOption)
	if v == nil {
		return nil
	}
	raw, _ := json.Marshal(v)
	var r coreAutoResult
	if json.Unmarshal(raw, &r) != nil || r.Version == "" {
		return nil
	}
	return &r
}

func (s *Server) setCoreAutoLast(ctx context.Context, r coreAutoResult) {
	if err := s.settings.Set(ctx, map[string]any{coreAutoLastOption: r}); err != nil {
		slog.Warn("core auto-update: record the result", "err", err)
	}
}

// noteCoreRollback remembers a release the site went back from, before the Updates screen clears the
// notice, so the automatic update does not try it again.
func (s *Server) noteCoreRollback(ctx context.Context, st *coreupdate.State) {
	if st == nil || st.Status != "rolled_back" || st.To == "" {
		return
	}
	vals := map[string]any{}
	if s.settings.String(coreAutoSkipOption) != st.To {
		vals[coreAutoSkipOption] = st.To
	}
	if last := s.coreAutoLast(); last != nil && last.Version == st.To && last.Status == "started" {
		vals[coreAutoLastOption] = coreAutoResult{Version: st.To, At: st.At, Status: "rolled_back", Error: st.Message}
	}
	if len(vals) > 0 {
		if err := s.settings.Set(ctx, vals); err != nil {
			slog.Warn("core auto-update: remember the rolled back release", "err", err)
		}
	}
}

var coreAutoDevTrustOnce sync.Once

// coreAutoUpdate runs from the daily Manager check: it installs a newer release when automatic
// updates are on and restarts into it. It returns the version it is restarting into, or "".
func (s *Server) coreAutoUpdate(ctx context.Context) string {
	if !s.settings.Bool(coreAutoUpdateOption) || s.sandbox != nil {
		return ""
	}
	if devTrustHubKeys() {
		// PURSAE_TRUST_HUB_KEYS=1 (devhub builds) trusts every key the Manager lists, endorsed by the
		// release root or not: whoever answers as the Manager could sign a binary. An update made by
		// hand still works; an unattended one is never installed under that trust.
		coreAutoDevTrustOnce.Do(func() {
			slog.Warn("core auto-update skipped: PURSAE_TRUST_HUB_KEYS=1 trusts unendorsed Manager keys; update by hand under Dashboard › Updates")
		})
		return ""
	}
	latest := s.coreUpdateAvailable()
	if latest == "" {
		return ""
	}
	target, err := coreUpdateTarget()
	if err != nil {
		return "" // this site cannot replace its binary (container image, go run): shown on the screen
	}
	s.noteCoreRollback(ctx, coreupdate.Read(target))
	if s.settings.String(coreAutoSkipOption) == latest {
		return "" // went back from this release before
	}
	if !sameMinor(Version, latest) {
		// A new minor (or major) release is installed by hand only; say so once, not every day.
		if last := s.coreAutoLast(); last == nil || last.Version != latest || last.Status != "manual" {
			slog.Info("core auto-update: a new minor release is not installed automatically; update by hand under Dashboard › Updates", "version", latest, "running", Version)
			s.setCoreAutoLast(ctx, coreAutoResult{Version: latest, At: time.Now().UTC(), Status: "manual"})
		}
		return ""
	}
	if !coreBusy.CompareAndSwap(false, true) {
		return ""
	}
	backup, err := s.preUpdateBackup(ctx, latest)
	if err != nil {
		coreBusy.Store(false)
		slog.Warn("core auto-update skipped: the database backup before the update failed", "version", latest, "err", err)
		s.setCoreAutoLast(ctx, coreAutoResult{Version: latest, At: time.Now().UTC(), Status: "skipped", Error: err.Error()})
		return ""
	}
	version, err := s.coreUpdate(ctx, target)
	if err != nil {
		coreBusy.Store(false)
		slog.Warn("core auto-update failed; the site stays on its version", "version", latest, "err", err)
		s.setCoreAutoLast(ctx, coreAutoResult{Version: latest, At: time.Now().UTC(), Status: "failed", Error: err.Error(), Backup: backup})
		return ""
	}
	slog.Info("core auto-updated, restarting", "from", Version, "to", version, "exe", target, "backup", backup)
	s.setCoreAutoLast(ctx, coreAutoResult{Version: version, At: time.Now().UTC(), Status: "started", Backup: backup})
	s.hubEvent("core_update", "core", "", version)
	s.coreRestartLater(target)
	return version
}

// preUpdateBackupKeep is how many pre-update backups are kept (the oldest beyond it are deleted).
const preUpdateBackupKeep = 3

// bmgrPreUpdateKind is the manifest kind and file-name prefix of pre-update backups.
const bmgrPreUpdateKind = "pre-update"

// preUpdateBackup writes a database-only Backup Manager archive (every table, every user, no media,
// theme or plugin files) into the backups directory before an automatic core update, whether the
// Backup Manager plugin is on or not, and prunes the older ones. It fails when another backup or
// restore is running, the disk is too full or the export fails: the update is then skipped.
func (s *Server) preUpdateBackup(ctx context.Context, to string) (string, error) {
	j, err := s.bmgrStart("backup")
	if err != nil {
		return "", err
	}
	cats := make([]string, 0, len(bmgrCategories))
	for _, c := range bmgrCategories {
		cats = append(cats, c.Key)
	}
	name, err := s.bmgrRunBackup(ctx, j, bmgrSelection{Categories: cats, UserMode: "all", Kind: bmgrPreUpdateKind, NoFiles: true,
		Note: "Database backup taken automatically before the automatic update from Pursae " + Version + " to " + to})
	s.bmgrUpdate(j, func(j *bmgrJob) { j.File = name })
	s.bmgrFinish(j, err)
	if err != nil {
		return "", err
	}
	s.bmgrPruneKind(bmgrPreUpdateKind, bmgrPreUpdateKind+"-", preUpdateBackupKeep)
	return name, nil
}

// sameMinor reports whether two versions share major and minor (0.3.1 and 0.3.4-beta.1 do, 0.3.9
// and 0.4.0 do not). A version that does not parse matches nothing.
func sameMinor(a, b string) bool {
	ma, okA := majorMinor(a)
	mb, okB := majorMinor(b)
	return okA && okB && ma == mb
}

func majorMinor(v string) ([2]int, bool) {
	v, _, _ = strings.Cut(strings.TrimPrefix(strings.TrimSpace(v), "v"), "+")
	v, _, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return [2]int{}, false
	}
	var out [2]int
	for i := range out {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return [2]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// coreAutoUpdateToggle is Dashboard › Updates › "Enable/Disable automatic updates".
func (s *Server) coreAutoUpdateToggle(c *gin.Context) {
	if !s.checkNonce(c, "upgrade-core") {
		s.denyNonce(c)
		return
	}
	on := c.PostForm("enable") == "1"
	vals := map[string]any{coreAutoUpdateOption: ""}
	if on {
		vals[coreAutoUpdateOption] = "1"
	}
	if err := s.settings.Set(c, vals); err != nil {
		c.Redirect(http.StatusFound, "/wp-admin/update-core?error="+url.QueryEscape("The setting could not be saved."))
		return
	}
	msg := "disabled"
	if on {
		msg = "enabled"
	}
	c.Redirect(http.StatusFound, "/wp-admin/update-core?auto-update="+msg)
}

// coreAutoUpdateView is what the Updates screen shows about automatic updates.
func (s *Server) coreAutoUpdateView(c *gin.Context) map[string]any {
	d := map[string]any{"On": s.settings.Bool(coreAutoUpdateOption)}
	if _, err := coreUpdateTarget(); err != nil {
		d["Blocked"] = err.Error()
	}
	if skip := s.settings.String(coreAutoSkipOption); skip != "" && versionLess(Version, skip) {
		d["Skipped"] = skip
	}
	if last := s.coreAutoLast(); last != nil {
		d["Last"] = last
		d["LastOK"] = last.Status == "started" && !versionLess(Version, last.Version) // running it now
		d["LastAt"] = last.At.In(s.loc()).Format("January 2, 2006 at 3:04 pm")
	}
	return d
}
