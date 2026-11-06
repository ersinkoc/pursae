package server

import (
	"context"
	"html/template"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/webbuilder"
)

// Settings › Web Builder (options-general?page=web-builder, add_options_page() style): the post types
// the builder edits and the pages whose published markup came from an older renderer. The plugin is
// a plain on/off switch on the Plugins screen (wbSetActive); while it is off this screen and its menu
// entry are gone, like any inactive plugin's.

const (
	// wbSettingsPage is the page slug of Settings › Web Builder.
	wbSettingsPage = "web-builder"
	// wbSettingsURL is the screen's admin URL.
	wbSettingsURL = "/wp-admin/options-general?page=" + wbSettingsPage
	// wbPostTypesOption lists the post types that open in the builder, comma separated.
	wbPostTypesOption = "web_builder_post_types"
	// wbSettingsNonce is the nonce action of the settings form.
	wbSettingsNonce = "web-builder-settings"
)

// wbEnabledTypes are the post types the builder edits (Settings › Web Builder; pages and posts by
// default).
func (s *Server) wbEnabledTypes() []string {
	var out []string
	for _, t := range strings.Split(s.settings.String(wbPostTypesOption), ",") {
		if t = strings.TrimSpace(t); t != "" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// wbEnabledType reports whether posts of the type open in the builder.
func (s *Server) wbEnabledType(typ string) bool {
	return typ != "" && slices.Contains(s.wbEnabledTypes(), typ)
}

// wbSetActive is the Plugins screen's Activate / Deactivate of Pursae Web Builder: the option and a
// cache purge (built pages render differently with the plugin off), nothing else. No theme switch,
// no setup wizard, no kit: the builder needs none of them.
func (s *Server) wbSetActive(ctx context.Context, on bool) error {
	if err := s.settings.Set(ctx, map[string]any{webbuilder.PluginOption: on}); err != nil {
		return err
	}
	s.purgeAll(ctx)
	return nil
}

// wbTypeChoice is a post type offered on the settings screen.
type wbTypeChoice struct {
	Slug, Label string
	Checked     bool
}

// wbPostTypeChoices are the public post types with an editor (attachments excluded), in
// registration order, plus any enabled type that no longer qualifies so it can be switched off.
func (s *Server) wbPostTypeChoices(ctx context.Context) []wbTypeChoice {
	enabled := s.wbEnabledTypes()
	var out []wbTypeChoice
	rows, err := s.store.Pool.Query(ctx, `SELECT slug, name FROM content_types
		WHERE public AND slug <> 'attachment' AND coalesce(supports->>'editor', 'false') = 'true' ORDER BY id`)
	if err == nil {
		for rows.Next() {
			var ch wbTypeChoice
			if rows.Scan(&ch.Slug, &ch.Label) == nil {
				ch.Checked = slices.Contains(enabled, ch.Slug)
				out = append(out, ch)
			}
		}
		rows.Close()
	}
	for _, t := range enabled {
		if !slices.ContainsFunc(out, func(ch wbTypeChoice) bool { return ch.Slug == t }) {
			out = append(out, wbTypeChoice{Slug: t, Label: t, Checked: true})
		}
	}
	return out
}

// wbOutdatedPage is a built post whose snapshot an older renderer produced.
type wbOutdatedPage struct {
	ID              int64
	Title, Type     string
	Renderer        int
	EditURL, Status string
}

// wbOutdatedLimit caps the list on the settings screen.
const wbOutdatedLimit = 200

// wbOutdatedPages lists the built posts (not trashed) whose published markup came from a renderer
// older than webbuilder.RendererVersion, most recently modified first, and how many there are.
// Only the editor renders (renderStatic runs in the browser), so each needs opening and publishing.
func (s *Server) wbOutdatedPages(ctx context.Context) ([]wbOutdatedPage, int, error) {
	var total int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM web_builder_docs d JOIN posts p ON p.id = d.post_id
		WHERE d.renderer < $1 AND p.status NOT IN ('trash', 'auto-draft')`, webbuilder.RendererVersion).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT p.id, p.title, p.type, p.status, d.renderer FROM web_builder_docs d
		JOIN posts p ON p.id = d.post_id
		WHERE d.renderer < $1 AND p.status NOT IN ('trash', 'auto-draft')
		ORDER BY p.modified_at DESC, p.id DESC LIMIT $2`, webbuilder.RendererVersion, wbOutdatedLimit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []wbOutdatedPage
	for rows.Next() {
		var p wbOutdatedPage
		if err := rows.Scan(&p.ID, &p.Title, &p.Type, &p.Status, &p.Renderer); err != nil {
			return nil, 0, err
		}
		if p.Title == "" {
			p.Title = "(no title)"
		}
		p.EditURL = wbEditURL(p.ID)
		out = append(out, p)
	}
	return out, total, rows.Err()
}

// wbSettingsData is the template data of Settings › Web Builder.
type wbSettingsData struct {
	Nonce           string
	OptionPage      string
	Types           []wbTypeChoice
	Outdated        []wbOutdatedPage
	OutdatedTotal   int
	OutdatedMore    bool
	OutdatedErr     bool
	RendererVersion int
}

// wbSettingsScreen renders Settings › Web Builder (GET options-general?page=web-builder; the caller
// checked manage_options). It also takes the dismissal of the upgrade notice, which must work while
// the plugin is off.
func (s *Server) wbSettingsScreen(c *gin.Context) {
	if c.Query("dismiss") == wbUpgradeNoticeDismiss {
		s.wbDismissUpgradeNotice(c) // webbuilder_upgrade.go
		return
	}
	if !s.wbOn() {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	ctx := c.Request.Context()
	d := wbSettingsData{Nonce: s.nonce(c, wbSettingsNonce), OptionPage: pluginSettingsSlug + wbSettingsPage,
		Types: s.wbPostTypeChoices(ctx), RendererVersion: webbuilder.RendererVersion}
	var err error
	if d.Outdated, d.OutdatedTotal, err = s.wbOutdatedPages(ctx); err != nil {
		d.OutdatedErr = true
	}
	d.OutdatedMore = d.OutdatedTotal > len(d.Outdated)
	var notices []notice
	if c.Query("settings-updated") == "true" {
		notices = append(notices, notice{Type: "success", Message: "<strong>Settings saved.</strong>", Dismissible: true})
	}
	s.adminPage(c, "options-general?page="+wbSettingsPage, "Web Builder Settings", "settings_page_"+wbSettingsPage,
		"admin/web-builder-settings", d, notices...)
}

// wbSettingsSave is options.php for Settings › Web Builder (option_page plugin:web-builder; the
// caller checked manage_options). Only post types offered on the screen are stored.
func (s *Server) wbSettingsSave(c *gin.Context) {
	if !s.wbOn() {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	if !s.checkNonce(c, wbSettingsNonce) {
		s.denyNonce(c)
		return
	}
	ctx := c.Request.Context()
	sent := c.PostFormArray(wbPostTypesOption + "[]")
	var keep []string
	for _, ch := range s.wbPostTypeChoices(ctx) {
		if slices.Contains(sent, ch.Slug) {
			keep = append(keep, ch.Slug)
		}
	}
	if err := s.settings.Set(ctx, map[string]any{wbPostTypesOption: strings.Join(keep, ",")}); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	s.purgeAll(ctx) // the toolbar's "Edit with Pursae Web Builder" and built pages follow the types
	c.Redirect(http.StatusFound, wbSettingsURL+"&settings-updated=true")
}

// wbSettingsMenu adds Settings › Web Builder while the plugin is active (plugins_host.go calls it
// from pluginMenuEntries).
func (s *Server) wbSettingsMenu(menu []*menuTop, screen string) {
	if !s.wbOn() {
		return
	}
	for _, top := range menu {
		if top.ID != "menu-settings" {
			continue
		}
		slug := "options-general?page=" + wbSettingsPage
		sub := &menuSub{Slug: slug, Title: template.HTML("Web Builder"), Current: slug == screen}
		if sub.Current {
			top.Current = true
		}
		top.Sub = append(top.Sub, sub)
	}
}
