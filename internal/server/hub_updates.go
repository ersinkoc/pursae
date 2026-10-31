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
// Themes: Appearance › Add Theme does not list Manager themes yet (themes come from the bundled
// library or an upload), so there are no Manager theme updates.

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
