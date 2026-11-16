package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
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

const (
	coreAutoUpdateOption = "pursae_core_auto_update"      // "1" when on
	coreAutoSkipOption   = "pursae_core_auto_update_skip" // a release that was rolled back
	coreAutoLastOption   = "pursae_core_auto_update_last" // coreAutoResult of the last attempt
)

// coreAutoResult is the last automatic core update attempt.
type coreAutoResult struct {
	Version string    `json:"version"`
	At      time.Time `json:"at"`
	Status  string    `json:"status"` // started | failed | rolled_back
	Error   string    `json:"error,omitempty"`
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
		// WPGO_TRUST_HUB_KEYS=1 (devhub builds) trusts every key the Manager lists, endorsed by the
		// release root or not: whoever answers as the Manager could sign a binary. An update made by
		// hand still works; an unattended one is never installed under that trust.
		coreAutoDevTrustOnce.Do(func() {
			slog.Warn("core auto-update skipped: WPGO_TRUST_HUB_KEYS=1 trusts unendorsed Manager keys; update by hand under Dashboard › Updates")
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
	if !coreBusy.CompareAndSwap(false, true) {
		return ""
	}
	version, err := s.coreUpdate(ctx, target)
	if err != nil {
		coreBusy.Store(false)
		slog.Warn("core auto-update failed; the site stays on its version", "version", latest, "err", err)
		s.setCoreAutoLast(ctx, coreAutoResult{Version: latest, At: time.Now().UTC(), Status: "failed", Error: err.Error()})
		return ""
	}
	slog.Info("core auto-updated, restarting", "from", Version, "to", version, "exe", target)
	s.setCoreAutoLast(ctx, coreAutoResult{Version: version, At: time.Now().UTC(), Status: "started"})
	s.hubEvent("core_update", "core", "", version)
	s.coreRestartLater(target)
	return version
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
