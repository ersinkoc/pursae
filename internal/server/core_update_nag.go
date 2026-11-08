package server

import (
	"context"
	"html/template"
	"log/slog"
	"strings"
)

// A newer core release on every admin screen, like WordPress: the "Pursae X is available! Please
// update now." notice at the top of the page (update_nag), the count on Dashboard › Updates and
// "Get Version X" in the footer (core_update_footer). The Updates screen itself shows the release
// with its button, so the notice is left out there, and the full-screen block editor has no room for it.
//
// The admin pages never ask the Manager: the newest release is kept in the wpgo_core_latest option,
// written whenever the site reads the Manager's index (the daily wpgo_hub_check, Updates › Check
// again, the Updates screen, a core update) and at start-up from the cached index. Once the binary
// is updated, Version catches up and the notice goes away by itself.

const coreLatestOption = "wpgo_core_latest"

// rememberCoreLatest stores the newest core release an index announces ("" when it announces none,
// so a release the Manager withdraws stops being offered).
func (s *Server) rememberCoreLatest(ctx context.Context, idx *hubIndex) {
	latest := ""
	if idx != nil && idx.Core != nil && validReleaseVersion(idx.Core.Latest) {
		latest = strings.TrimSpace(idx.Core.Latest)
	}
	if s.settings.String(coreLatestOption) == latest {
		return
	}
	if err := s.settings.Set(ctx, map[string]any{coreLatestOption: latest}); err != nil {
		slog.Warn("hub: remember the latest core release", "err", err)
	}
}

// validReleaseVersion accepts the version strings a release can carry (1.2.3, v1.2.3, 1.2.3-beta.1).
func validReleaseVersion(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 40 {
		return false
	}
	for _, r := range v {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '.' || r == '-' || r == '+') {
			return false
		}
	}
	return true
}

// coreUpdateAvailable is the newer core release the site may update to, or "".
func (s *Server) coreUpdateAvailable() string {
	if s.hubBase() == "" {
		return ""
	}
	if v := s.settings.String(coreLatestOption); v != "" && versionLess(Version, v) {
		return v
	}
	return ""
}

// coreUpdateChrome puts a newer release on the admin page: the notice, the Updates count and the footer link.
func (s *Server) coreUpdateChrome(v *adminView, tmpl string) {
	if v.Screen == "update-core" { // the Updates screen (and its restart page) speaks for itself, from the live index
		return
	}
	latest := s.coreUpdateAvailable()
	if latest == "" {
		return
	}
	v.CanUpdateCore = v.User.Can("update_core", nil)
	if tmpl != "admin/block-editor" {
		v.CoreUpdate = latest
	}
	v.FooterUpdate = latest
	for _, top := range v.Menu {
		for _, sub := range top.Sub {
			if sub.Slug == "update-core" {
				sub.Title = template.HTML(`Updates <span class="update-plugins count-1"><span class="update-count">1</span></span>`)
			}
		}
	}
}
