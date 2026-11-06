package server

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"wpgo/internal/webbuilder"
)

// The upgrade from the old (Elementor-derived) builder to Pursae Web Builder, run once at startup
// (wbUpgrade, from startPlugins). The old builder's data goes with migration 0028; what is left to
// the server is the theme: Hello Web Builder, a theme saved with the old builder and the old
// builder's design packages render through code that no longer exists, so a site on one of them
// moves to the theme it had before (golementor_previous_theme) or, failing that, the default theme.
// An admin notice on the Dashboard and the Themes screen says what happened until it is dismissed.

const (
	// wbUpgradedOption marks the upgrade as done (it never runs twice).
	wbUpgradedOption = "web_builder_upgraded"
	// wbUpgradeNoticeOption holds the pending notice: {"theme": "<name of the new theme>"}, theme
	// empty when the theme was not switched. Absent or empty once dismissed.
	wbUpgradeNoticeOption = "web_builder_upgrade_notice"
	// wbPrevThemeOption is the theme the old builder left for Hello Web Builder on activation.
	wbPrevThemeOption = "golementor_previous_theme"
	// wbUpgradeNoticeDismiss is the dismiss= value of the notice's Dismiss link (wbSettingsScreen).
	wbUpgradeNoticeDismiss = "upgrade-notice"
	// wbUpgradeNoticeNonce is that link's nonce action.
	wbUpgradeNoticeNonce = "web-builder-dismiss-upgrade-notice"
)

// wbOldHelloThemes are the old builder's blank canvas theme, under its name before and after the
// Pursae rename.
var wbOldHelloThemes = map[string]bool{"hello-web-builder": true, "hello-gowebeditor": true}

// wbUpgrade runs the one-time upgrade. A site that is not installed yet has nothing to upgrade and
// is marked done once it is (the next start); sandboxes never get here.
func (s *Server) wbUpgrade(ctx context.Context) {
	if s.sandbox != nil || !s.settings.Installed() || s.settings.Bool(wbUpgradedOption) {
		return
	}
	// The old builder was in use when its plugin was on or it once moved the site to its theme.
	used := s.settings.Bool(webbuilder.PluginOption) || s.settings.String(wbPrevThemeOption) != ""
	switched := ""
	if cur := s.settings.String("stylesheet"); s.wbOldBuilderTheme(ctx, cur) {
		target := s.wbUpgradeTarget(ctx)
		_ = s.restoreTheme(ctx, target) // the default theme comes back even after a delete
		if err := s.switchTheme(ctx, target); err != nil {
			log.Printf("web builder upgrade: switching from %s to %s: %v", cur, target, err)
			return // retried at the next start
		}
		if t := s.installedTheme(target); t != nil {
			switched = t.Name
		} else {
			switched = target
		}
		used = true
	}
	values := map[string]any{wbUpgradedOption: true}
	if used {
		values[wbUpgradeNoticeOption] = map[string]any{"theme": switched}
	}
	if s.settings.String(wbPrevThemeOption) != "" {
		values[wbPrevThemeOption] = ""
	}
	if err := s.settings.Set(ctx, values); err != nil {
		log.Printf("web builder upgrade: %v", err)
	}
}

// wbOldBuilderTheme reports whether slug is a theme of the old builder: Hello Web Builder, a theme
// saved with it (themes.custom) or a design package (a golementor/ directory in the package).
func (s *Server) wbOldBuilderTheme(ctx context.Context, slug string) bool {
	if slug == "" {
		return false
	}
	if wbOldHelloThemes[slug] {
		return true
	}
	var custom bool
	// The custom column goes with the old builder's data; an error means no saved themes.
	if err := s.store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM themes WHERE slug = $1 AND custom)`, slug).Scan(&custom); err == nil && custom {
		return true
	}
	if t := s.themeFor(slug); t != nil && t.Package != nil && t.Package.Dir != "" {
		if st, err := os.Stat(filepath.Join(t.Package.Dir, "golementor")); err == nil && st.IsDir() {
			return true
		}
	}
	return false
}

// wbUpgradeTarget is the theme the upgrade moves to: the one active before the old builder took
// over when it is still installed and not itself a builder theme, else the default theme.
func (s *Server) wbUpgradeTarget(ctx context.Context) string {
	prev := s.settings.String(wbPrevThemeOption)
	if prev != "" && !s.wbOldBuilderTheme(ctx, prev) && !s.themeDeleted(prev) && s.installedTheme(prev) != nil {
		return prev
	}
	return defaultTheme
}

// wbUpgradeNotice is the pending upgrade notice for this request: on the Dashboard and the Themes
// screen, for users who manage options (pluginNotices calls it).
func (s *Server) wbUpgradeNotice(c *gin.Context) (notice, bool) {
	m, ok := s.settings.Get(wbUpgradeNoticeOption).(map[string]any)
	if !ok {
		return notice{}, false
	}
	from := ""
	switch c.Request.URL.Path {
	case "/wp-admin", "/wp-admin/", "/wp-admin/index":
	case "/wp-admin/themes":
		from = "themes"
	default:
		return notice{}, false
	}
	if u := currentUser(c); u == nil || !u.Can("manage_options", nil) {
		return notice{}, false
	}
	msg := "<strong>Pursae Web Builder was replaced by the new builder.</strong> Pages built with the old builder were removed"
	if th, _ := m["theme"].(string); th != "" {
		msg += "; the theme was switched to <strong>" + template.HTMLEscapeString(th) + "</strong>."
	} else {
		msg += "."
	}
	dismiss := wbSettingsURL + "&dismiss=" + wbUpgradeNoticeDismiss + "&_wpnonce=" + s.nonce(c, wbUpgradeNoticeNonce)
	if from != "" {
		dismiss += "&from=" + from
	}
	msg += ` <a href="` + template.HTMLEscapeString(dismiss) + `" id="web-builder-upgrade-dismiss">Dismiss</a>`
	return notice{Type: "info", Message: template.HTML(msg), Dismissible: true}, true
}

// wbDismissUpgradeNotice clears the upgrade notice (GET with a nonce, manage_options checked by the
// options screen) and goes back to the screen that showed it.
func (s *Server) wbDismissUpgradeNotice(c *gin.Context) {
	if !s.checkNonce(c, wbUpgradeNoticeNonce) {
		s.denyNonce(c)
		return
	}
	if err := s.settings.Set(c, map[string]any{wbUpgradeNoticeOption: nil}); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	back := "/wp-admin/"
	if c.Query("from") == "themes" {
		back = "/wp-admin/themes"
	}
	c.Redirect(http.StatusFound, back)
}
