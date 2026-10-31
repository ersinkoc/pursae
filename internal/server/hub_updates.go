package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/plugins"
)

// Updates of plugins installed from Pursae Manager (plugins.source = 'hub'). The catalog
// (GET /api/v1/plugins, cached 12 hours, per license) gives each listing's newest version the site may
// see; a higher version than the installed one is an update. The Plugins screen shows WordPress' "There
// is a new version of X available. Update now" row under the plugin, Dashboard › Updates lists them
// with "Update Plugins". Updating downloads the package and verifies its SHA-256 and signature exactly
// like an install (hubInstallPlugin), replaces the package and, if the plugin was active, loads the new
// version with the permissions granted before (a new version that asks for more gets none of the new
// ones until the admin re-activates it from the consent screen).
//
// Themes: the theme library comes from the Manager (theme_library.go). An installed package theme
// whose slug the theme catalog (GET /api/v1/themes, cached like the plugins) lists with a higher
// version has an update, unless it was uploaded (themePackageSource). Appearance › Themes shows the
// "New version available. Update now" notice on its card and details, Dashboard › Updates lists it
// under Themes with "Update Themes". Updating downloads the package, checks its SHA-256 and size and
// installs it through installThemePackage, which replaces the files in place: theme mods, widgets and
// the active theme stay as they are. Nothing is updated automatically.

// pluginUpdate is a newer version of an installed plugin on the Manager.
type pluginUpdate struct {
	Version, Channel, Homepage string
	URL                        string // Update now (Plugins screen), with its nonce
}

// hubPluginUpdates maps the slug of every Manager-installed plugin with a newer published version to
// its listing. Without Manager plugins it never touches the network.
func (s *Server) hubPluginUpdates(ctx context.Context) map[string]hubItem {
	if s.hubBase() == "" {
		return nil
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT slug, version FROM plugins WHERE source = 'hub'`)
	if err != nil {
		return nil
	}
	installed := map[string]string{}
	for rows.Next() {
		var slug, v string
		if rows.Scan(&slug, &v) == nil {
			installed[slug] = v
		}
	}
	rows.Close()
	if len(installed) == 0 {
		return nil
	}
	items, err := s.hubCatalog(ctx, "plugins")
	if err != nil {
		return nil
	}
	out := map[string]hubItem{}
	for _, it := range items {
		if v, ok := installed[it.Slug]; ok && versionLess(v, it.Version) {
			out[it.Slug] = it
		}
	}
	return out
}

// pluginsWithUpdates marks the rows that have a Manager update (Plugins screen, Dashboard › Updates).
func (s *Server) pluginsWithUpdates(c *gin.Context, rows []pluginRow) []pluginRow {
	ups := s.hubPluginUpdates(c.Request.Context())
	if len(ups) == 0 {
		return rows
	}
	for i := range rows {
		if it, ok := ups[rows[i].Slug]; ok && !rows[i].Native {
			rows[i].Update = &pluginUpdate{Version: it.Version, Channel: it.Channel, Homepage: it.Homepage,
				URL: "/wp-admin/plugins?action=upgrade-plugin&plugin=" + url.QueryEscape(rows[i].Slug) + "&_wpnonce=" + s.nonce(c, "upgrade-plugin_"+rows[i].Slug)}
		}
	}
	return rows
}

// hubUpdatePlugin updates one Manager-installed plugin to the newest version the Manager offers.
func (s *Server) hubUpdatePlugin(ctx context.Context, slug string) (*plugins.Manifest, error) {
	if !plugins.ValidSlug(slug) {
		return nil, errors.New("Invalid plugin.")
	}
	var installed, status, source string
	var perms []byte
	if err := s.store.Pool.QueryRow(ctx, `SELECT version, status, source, permissions FROM plugins WHERE slug = $1`, slug).Scan(&installed, &status, &source, &perms); err != nil {
		return nil, errors.New("That plugin is not installed.")
	}
	if source != pluginSourceHub {
		return nil, errors.New("Only plugins installed from Pursae Manager can be updated here; upload the new version instead.")
	}
	if _, err := s.hubIndexCached(ctx); err != nil { // the Manager's signing keys
		return nil, err
	}
	it, err := s.hubItemFor(ctx, "plugins", slug)
	if err != nil {
		return nil, err
	}
	if !versionLess(installed, it.Version) {
		return nil, fmt.Errorf("%s %s is already the newest version.", slug, installed)
	}
	data, err := s.hubDownload(ctx, it)
	if err != nil {
		return nil, err
	}
	active := status == "active"
	if active && s.plugins.rt != nil {
		_ = s.plugins.rt.Unload(ctx, slug)
	}
	m, err := s.installPackageFrom(ctx, data, pluginSourceHub, slug)
	if err != nil {
		if active { // the old package is still in place: bring it back
			_ = s.loadPlugin(ctx, slug, perms)
		}
		return nil, err
	}
	if active {
		var granted []plugins.Permission
		_ = json.Unmarshal(perms, &granted)
		var keep []plugins.Permission
		for _, p := range granted { // only what the new version still asks for
			for _, want := range m.Permissions {
				if p == want {
					keep = append(keep, p)
				}
			}
		}
		raw, _ := json.Marshal(keep)
		if err := s.loadPlugin(ctx, slug, raw); err != nil {
			_, _ = s.store.Pool.Exec(ctx, `UPDATE plugins SET status = 'inactive' WHERE slug = $1`, slug)
			return m, fmt.Errorf("%s was updated to %s but could not be activated again: %v", m.Name, m.Version, err)
		}
		_, _ = s.store.Pool.Exec(ctx, `UPDATE plugins SET permissions = $2 WHERE slug = $1`, slug, raw)
		s.plugins.hooks.DoAction(ctx, "init")
	}
	s.purgeAll(ctx)
	return m, nil
}

// pluginUpgrade is the Plugins screen's "Update now" (update.php?action=upgrade-plugin).
func (s *Server) pluginUpgrade(c *gin.Context) {
	slug := c.Query("plugin")
	if !s.checkNonce(c, "upgrade-plugin_"+slug) {
		s.denyNonce(c)
		return
	}
	if !currentUser(c).Can("update_plugins", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to update plugins for this site.")
		return
	}
	m, err := s.hubUpdatePlugin(c.Request.Context(), slug)
	if err != nil {
		c.Redirect(http.StatusFound, "/wp-admin/plugins?error="+url.QueryEscape("Update failed: "+err.Error()))
		return
	}
	c.Redirect(http.StatusFound, "/wp-admin/plugins?updated="+url.QueryEscape(m.Name+" "+m.Version))
}

// updatePlugins is Dashboard › Updates › Update Plugins (update-core.php?action=do-plugin-upgrade).
func (s *Server) updatePlugins(c *gin.Context) {
	if !s.checkNonce(c, "upgrade-core") {
		s.denyNonce(c)
		return
	}
	if !currentUser(c).Can("update_plugins", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to update plugins for this site.")
		return
	}
	slugs := c.PostFormArray("checked[]")
	sort.Strings(slugs)
	var done, errs []string
	for _, slug := range slugs {
		m, err := s.hubUpdatePlugin(c.Request.Context(), slug)
		if err != nil {
			errs = append(errs, slug+": "+err.Error())
			continue
		}
		done = append(done, m.Name+" "+m.Version)
	}
	q := url.Values{}
	if len(done) > 0 {
		q.Set("plugins-updated", strings.Join(done, ", "))
	}
	if len(errs) > 0 {
		q.Set("error", strings.Join(errs, "; "))
	}
	c.Redirect(http.StatusFound, "/wp-admin/update-core?"+q.Encode())
}

// themeUpdate is a newer version of an installed theme on the Manager.
type themeUpdate struct {
	Version, Channel string
	URL              string // Update now (Appearance › Themes), with its nonce
}

// hubThemeUpdates maps the slug of every installed package theme with a newer version on the Manager
// to its listing. Without package themes from the Manager it never touches the network.
func (s *Server) hubThemeUpdates(ctx context.Context) map[string]hubItem {
	if s.hubBase() == "" || s.sandbox != nil {
		return nil
	}
	installed := map[string]string{}
	for _, t := range s.installedThemes() {
		if t.Package != nil && !t.Custom && themePackageSource(t) != themeSourceUpload {
			installed[t.Slug] = t.Version
		}
	}
	if len(installed) == 0 {
		return nil
	}
	lib, err := s.managerLibraryThemes(ctx) // the catalog, valid listings only; a Manager that failed is not asked again at once
	if err != nil {
		return nil
	}
	out := map[string]hubItem{}
	for _, lt := range lib {
		if v, ok := installed[lt.Manifest.Slug]; ok && semverLess(v, lt.Manifest.Version) {
			out[lt.Manifest.Slug] = *lt.Item
		}
	}
	return out
}

// themeUpdateURL is the Update now link of a theme (Appearance › Themes).
func (s *Server) themeUpdateURL(c *gin.Context, slug string) string {
	return "/wp-admin/themes?action=upgrade-theme&stylesheet=" + url.QueryEscape(slug) + "&_wpnonce=" + s.nonce(c, "upgrade-theme_"+slug)
}

// hubUpdateTheme updates one installed package theme to the newest version the Manager offers.
func (s *Server) hubUpdateTheme(ctx context.Context, slug string) (*Theme, error) {
	t := s.installedTheme(slug)
	if t == nil || t.Package == nil || t.Custom {
		return nil, errors.New("That theme is not installed.")
	}
	if themePackageSource(t) == themeSourceUpload {
		return nil, errors.New("Only themes installed from Pursae Manager can be updated here; upload the new version instead.")
	}
	lt, err := s.hubThemeListing(ctx, slug)
	if err != nil {
		return nil, err
	}
	if !semverLess(t.Version, lt.Manifest.Version) {
		return nil, fmt.Errorf("%s %s is already the newest version.", t.Name, t.Version)
	}
	return s.installLibraryTheme(ctx, lt)
}

// themeUpgrade is Appearance › Themes › Update now (update.php?action=upgrade-theme).
func (s *Server) themeUpgrade(c *gin.Context) {
	slug := c.Query("stylesheet")
	if !s.checkNonce(c, "upgrade-theme_"+slug) {
		s.denyNonce(c)
		return
	}
	if !currentUser(c).Can("update_themes", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to update themes for this site.")
		return
	}
	t, err := s.hubUpdateTheme(c, slug)
	if err != nil {
		c.Redirect(http.StatusFound, "/wp-admin/themes?error="+url.QueryEscape("Update failed: "+err.Error()))
		return
	}
	c.Redirect(http.StatusFound, "/wp-admin/themes?updated="+url.QueryEscape(t.Slug))
}

// updateThemes is Dashboard › Updates › Update Themes (update-core.php?action=do-theme-upgrade).
func (s *Server) updateThemes(c *gin.Context) {
	if !s.checkNonce(c, "upgrade-core") {
		s.denyNonce(c)
		return
	}
	if !currentUser(c).Can("update_themes", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to update themes for this site.")
		return
	}
	slugs := c.PostFormArray("checked[]")
	sort.Strings(slugs)
	var done, errs []string
	for _, slug := range slugs {
		t, err := s.hubUpdateTheme(c, slug)
		if err != nil {
			errs = append(errs, slug+": "+err.Error())
			continue
		}
		done = append(done, t.Name+" "+t.Version)
	}
	q := url.Values{}
	if len(done) > 0 {
		q.Set("themes-updated", strings.Join(done, ", "))
	}
	if len(errs) > 0 {
		q.Set("error", strings.Join(errs, "; "))
	}
	c.Redirect(http.StatusFound, "/wp-admin/update-core?"+q.Encode())
}
