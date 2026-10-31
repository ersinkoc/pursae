package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/plugins"
	"wpgo/internal/settings"
	"wpgo/internal/store"
)

// Plugins are signed zip packages holding a WebAssembly module and a plugin.json manifest. They run
// sandboxed (internal/plugins) and reach the site only through the Host below and the hooks the
// core fires: init, the_content, admin_notices, wpgo_post_stats and the cron_* events they schedule.
// plugins_host.go holds the rest of the Host and the core side of shortcodes, REST routes, settings
// pages and package assets.

type pluginSystem struct {
	hooks   *plugins.Registry
	rt      *plugins.Runtime
	dir     string // installed packages: <dir>/<slug>.zip
	market  []string
	keys    []ed25519.PublicKey
	initErr error
}

// pluginDirs places plugin data next to the uploads directory (/data/uploads -> /data/plugins).
func (s *Server) pluginDirs() (pkgDir, cacheDir string) {
	base := filepath.Dir(filepath.Clean(s.cfg.UploadsDir))
	return filepath.Join(base, "plugins"), filepath.Join(base, "plugin-cache")
}

// startPlugins creates the runtime and loads every active plugin.
func (s *Server) startPlugins(ctx context.Context) {
	ps := &pluginSystem{hooks: plugins.NewRegistry()}
	s.plugins = ps
	pkgDir, cacheDir := s.pluginDirs()
	ps.dir = pkgDir
	_ = os.MkdirAll(pkgDir, 0o755)
	_ = os.MkdirAll(cacheDir, 0o755)
	for _, k := range strings.FieldsFunc(os.Getenv("WPGO_PLUGIN_KEYS")+","+s.settings.String("plugin_trusted_keys"), func(r rune) bool { return r == ',' || r == '\n' || r == ' ' }) {
		if pub, err := plugins.ParsePublicKey(k); err == nil {
			ps.keys = append(ps.keys, pub)
		}
	}
	// Marketplace demo: packages shipped next to the binary (or built by tools/build-plugins.sh).
	for _, dir := range []string{os.Getenv("WPGO_MARKET_DIR"), "examples/plugins/dist", "/usr/share/wpgo/plugins"} {
		if dir == "" {
			continue
		}
		if m, _ := filepath.Glob(filepath.Join(dir, "*.zip")); len(m) > 0 {
			ps.market = m
			if pub, err := os.ReadFile(filepath.Join(dir, "dev-key.pub")); err == nil && os.Getenv("WPGO_TRUST_DEV_KEY") == "1" {
				if k, err := plugins.ParsePublicKey(lastLine(string(pub))); err == nil {
					ps.keys = append(ps.keys, k)
				}
			}
			break
		}
	}
	rt, err := plugins.NewRuntime(ctx, ps.hooks, plugins.Config{CoreVersion: Version, CacheDir: cacheDir, Logger: slog.Default(),
		OnDisable: func(slug, reason string) {
			slog.Warn("plugin disabled by the runtime", "plugin", slug, "reason", reason)
			_, _ = s.store.Pool.Exec(context.Background(), `UPDATE plugins SET status = 'inactive' WHERE slug = $1`, slug)
		}})
	if err != nil {
		ps.initErr = err
		slog.Error("plugin runtime", "err", err)
		return
	}
	ps.rt = rt
	rows, err := s.store.Pool.Query(ctx, `SELECT slug, permissions FROM plugins WHERE status = 'active' ORDER BY slug`)
	if err != nil {
		return
	}
	type active struct {
		slug  string
		perms []byte
	}
	var list []active
	for rows.Next() {
		var a active
		if rows.Scan(&a.slug, &a.perms) == nil {
			list = append(list, a)
		}
	}
	rows.Close()
	for _, a := range list {
		if err := s.loadPlugin(ctx, a.slug, a.perms); err != nil {
			slog.Error("plugin load", "plugin", a.slug, "err", err)
		}
	}
	ps.hooks.DoAction(ctx, "init")
	s.hubTrustCached(ctx)
}

func lastLine(v string) string {
	ls := lines(v)
	if len(ls) == 0 {
		return ""
	}
	return ls[len(ls)-1]
}

func (s *Server) loadPlugin(ctx context.Context, slug string, rawPerms []byte) error {
	ps := s.plugins
	if ps.rt == nil {
		return ps.initErr
	}
	pkg, err := plugins.LoadPackageFile(filepath.Join(ps.dir, slug+".zip"), ps.keys, !s.settings.Bool("plugin_allow_unsigned"))
	if err != nil {
		return err
	}
	var granted []plugins.Permission
	_ = json.Unmarshal(rawPerms, &granted)
	_, err = ps.rt.Load(ctx, pkg, granted, &pluginHost{s: s})
	return err
}

// Hook helpers used by the core ---------------------------------------------------

func (s *Server) applyFilters(ctx context.Context, name string, value any, args ...any) any {
	if s.plugins == nil || !s.plugins.hooks.HasFilter(name) {
		return value
	}
	return s.plugins.hooks.ApplyFilters(ctx, name, value, args...)
}

type noticeKey struct{}

type noticeSink struct {
	mu   sync.Mutex
	list []plugins.Notice
}

// pluginNotices fires admin_notices and returns what plugins queued, escaped for the admin screen.
func (s *Server) pluginNotices(c *gin.Context) []notice {
	if s.plugins == nil || !s.plugins.hooks.HasAction("admin_notices") {
		return nil
	}
	sink := &noticeSink{}
	ctx, cancel := context.WithTimeout(context.WithValue(c.Request.Context(), noticeKey{}, sink), 2*time.Second)
	defer cancel()
	s.plugins.hooks.DoAction(ctx, "admin_notices")
	var out []notice
	for _, n := range sink.list {
		typ := n.Type
		if typ != "success" && typ != "warning" && typ != "error" {
			typ = "info"
		}
		out = append(out, notice{Type: typ, Message: template.HTML(template.HTMLEscapeString(n.Message)), Dismissible: true})
	}
	return out
}

// pluginHost implements plugins.Host on top of the site's options, plugin_data and a guarded HTTP client.
type pluginHost struct{ s *Server }

// hiddenOptions are never readable by plugins.
func hiddenOption(name string) bool {
	return strings.HasPrefix(name, "auth_") || strings.Contains(name, "secret") || strings.Contains(name, "password") ||
		strings.HasPrefix(name, "plugin_") || name == "admin_email" || name == "mailserver_pass"
}

func (h *pluginHost) GetOption(_ context.Context, plugin string, name string) (any, bool, error) {
	if hiddenOption(name) && !strings.HasPrefix(name, plugins.SettingsOptionPrefix(plugin)) {
		return nil, false, nil
	}
	v := h.s.settings.Get(name)
	return v, v != nil, nil
}

// UpdateOption only writes options in the plugin's own namespace (<slug>_* with dashes as underscores,
// and the plugin_<slug>_* options of its settings page).
func (h *pluginHost) UpdateOption(ctx context.Context, plugin, name string, value any) error {
	if !pluginOptionAllowed(plugin, name) {
		return fmt.Errorf("plugins may only write their own options (%q or %q) and never a core option", strings.ReplaceAll(plugin, "-", "_")+"_*", plugins.SettingsOptionPrefix(plugin)+"*")
	}
	return h.s.settings.Set(ctx, map[string]any{name: value})
}

// pluginOptionAllowed reports whether plugin may write the option name. The options table is one flat key
// space shared with the core, so the prefix test alone is not enough: a plugin slugged "default" would
// reach default_role, "plugin-allow" would reach plugin_allow_unsigned. Known core options (every default)
// and the core's own prefixes are therefore never writable by a plugin.
func pluginOptionAllowed(plugin, name string) bool {
	own := plugins.SettingsOptionPrefix(plugin) // plugin_<slug>_: the options of its settings page
	prefix := strings.ReplaceAll(plugin, "-", "_") + "_"
	if !strings.HasPrefix(name, prefix) && !strings.HasPrefix(name, own) {
		return false
	}
	if _, core := settings.Defaults[name]; core {
		return false
	}
	if strings.HasPrefix(name, own) {
		return true
	}
	for _, p := range []string{"auth_", "wpgo_", "native_", "plugin_"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

var pluginHTTP = &http.Client{
	Timeout: 8 * time.Second,
	Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: 5 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, _ := net.SplitHostPort(address)
		ip := net.ParseIP(host)
		if !publicIP(ip) { // the same policy as the feed fetcher: also carrier-grade NAT, 0.0.0.0/8 and broadcast
			return errors.New("destination not allowed for plugins")
		}
		return nil
	}}).DialContext},
}

func (h *pluginHost) HTTPGet(ctx context.Context, plugin, url string) (*plugins.HTTPResponse, error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return nil, errors.New("only http(s) URLs are allowed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Pursae/"+Version+" plugin/"+plugin)
	resp, err := pluginHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	hdr := map[string]string{}
	for k := range resp.Header {
		hdr[k] = resp.Header.Get(k)
	}
	return &plugins.HTTPResponse{Status: resp.StatusCode, Header: hdr, Body: string(body)}, nil
}

func (h *pluginHost) StorageGet(ctx context.Context, plugin, key string) (string, bool, error) {
	var v string
	err := h.s.store.Pool.QueryRow(ctx, `SELECT d.value #>> '{}' FROM plugin_data d JOIN plugins p ON p.id = d.plugin_id WHERE p.slug = $1 AND d.key = $2`, plugin, key).Scan(&v)
	if err != nil {
		return "", false, nil
	}
	return v, true, nil
}

func (h *pluginHost) StorageSet(ctx context.Context, plugin, key, value string) error {
	raw, _ := json.Marshal(value)
	_, err := h.s.store.Pool.Exec(ctx, `INSERT INTO plugin_data (plugin_id, key, value) SELECT id, $2, $3::jsonb FROM plugins WHERE slug = $1
		ON CONFLICT (plugin_id, key) DO UPDATE SET value = EXCLUDED.value`, plugin, key, raw)
	return err
}

func (h *pluginHost) AdminNotice(ctx context.Context, plugin string, n plugins.Notice) error {
	sink, _ := ctx.Value(noticeKey{}).(*noticeSink)
	if sink == nil {
		return nil
	}
	n.Plugin = plugin
	sink.mu.Lock()
	sink.list = append(sink.list, n)
	sink.mu.Unlock()
	return nil
}

// Plugins screen -------------------------------------------------------------------

type pluginRow struct {
	Slug, Name, Version, Description, Author, AuthorURI, PluginURI string
	Native, Active, Disabled                                       bool
	DisabledWhy                                                    string
	Permissions                                                    []permRow
	ActivateURL, DeactivateURL, DeleteURL, SettingsURL             string
	Logs                                                           []plugins.LogEntry
}

type permRow struct {
	Name, Description string
}

type pluginsData struct {
	Rows             []pluginRow
	Active, Inactive int
	Status           string
	Search           string // "Search installed plugins"
	Consent          *pluginRow
	ConsentNonce     string
	RuntimeErr       string
	BulkNonce        string
	CanDelete        bool
}

func (s *Server) installedPlugins(c *gin.Context) []pluginRow {
	n := func(action, slug string) string {
		return "/wp-admin/plugins?action=" + action + "&plugin=" + slug + "&_wpnonce=" + s.nonce(c, action+"-plugin_"+slug)
	}
	rows := []pluginRow{
		{Slug: "classic-editor", Name: "Classic Editor", Version: "1.6.7", Native: true, Active: s.settings.Bool("native_classic_editor"),
			Description: "Enables the previous \"classic\" editor and the old-style Edit Post screen with TinyMCE, Meta Boxes, etc. Choose the default editor and whether users may switch under Settings &rsaquo; Writing.",
			Author:      "Pursae", SettingsURL: "/wp-admin/options-writing"},
		{Slug: "wpgo-cache", Name: "WPGo Cache", Version: "1.0.0", Native: true, Active: s.settings.Bool("wpgo_cache_enabled"),
			Description: "Full-page cache with tag-based purging, a crawler and page optimizations. Works behind nginx, OpenLiteSpeed, Apache or on its own.",
			Author:      "Pursae", SettingsURL: "/wp-admin/admin?page=wpgo-cache"},
		{Slug: golementorSlug, Name: "Pursae Web Builder", Version: Version, Native: true, Active: s.golementorOn(),
			Description: "Visual drag-and-drop page builder and Theme Builder (headers, footers, single and archive templates), with Hello Pursae Web Builder, the blank theme to build them on. While it is off, none of it is part of the site.",
			Author:      "Pursae"},
		{Slug: msSlug, Name: "Pursae SMTP", Version: Version, Native: true, Active: s.mailSMTPOn(),
			Description: "Make email delivery easy for Pursae. Connect with SMTP, SendLayer, Brevo, Mailgun, SendGrid, Amazon SES, Gmail, Outlook and more, with an email log, reports, failure alerts, backup connection and smart routing. A native clone of WP Mail SMTP.",
			Author:      "Pursae"},
		{Slug: gontactSlug, Name: "Pursae Contact Form", Version: gontactVersion, Native: true, Active: s.gontactOn(),
			Description: "Just another contact form plugin. Simple but flexible: manage multiple contact forms, customize the form and the mail contents with simple markup, Ajax-powered submitting, quiz, reCAPTCHA and Turnstile spam protection, file uploads.",
			Author:      "Pursae", SettingsURL: "/wp-admin/admin?page=wpcf7"},
		{Slug: acfgoSlug, Name: "Pursae Custom Fields", Version: Version, Native: true, Active: s.acfgoOn(), SettingsURL: map[bool]string{true: "/wp-admin/acf-field-groups"}[s.acfgoOn()],
			Description: "Customize WordPress with powerful, professional and intuitive fields: field groups, repeaters, flexible content, options pages, custom post types and blocks. A native clone of Advanced Custom Fields PRO.",
			Author:      "Pursae"},
		golylangPluginRow(s.golylangOn()), // golylang_admin.go
	}
	for i := range rows { // Settings links only while active, as WordPress shows them
		switch rows[i].Slug {
		case msSlug:
			if rows[i].Active {
				rows[i].SettingsURL = "/wp-admin/admin?page=" + msSlug
			}
		case gontactSlug:
			if !rows[i].Active {
				rows[i].SettingsURL = ""
			}
		}
	}
	if !rows[1].Active {
		rows[1].SettingsURL = "" // WordPress shows a plugin's Settings link only while it is active
	}
	if rows[2].Active {
		rows[2].SettingsURL = "/wp-admin/theme-builder"
	}
	rows = append(rows, s.maintPluginRow())    // Maintenance-Go (maintenance_admin.go)
	rows = append(rows, s.seogoPluginRow())    // SEO-Go (seogo.go)
	rows = append(rows, s.gooikePluginRow())   // Gooike (gooike.go)
	rows = append(rows, s.gooPluginRow())      // Goocommerce (goocommerce.go)
	rows = append(rows, s.adminsecPluginRow()) // AdminSecGo (adminsec.go)
	rows = append(rows, s.gocdnPluginRow())    // GoCDN (gocdn.go)
	rows = append(rows, s.hgPluginRow())       // HeadlessGo (headlessgo.go)
	for i := range rows {
		rows[i].ActivateURL, rows[i].DeactivateURL = n("activate", rows[i].Slug), n("deactivate", rows[i].Slug)
	}
	dbRows, err := s.store.Pool.Query(c, `SELECT slug, status FROM plugins ORDER BY slug`)
	if err != nil {
		return rows
	}
	type inst struct{ slug, status string }
	var list []inst
	for dbRows.Next() {
		var r inst
		if dbRows.Scan(&r.slug, &r.status) == nil {
			list = append(list, r)
		}
	}
	dbRows.Close()
	for _, r := range list {
		row := pluginRow{Slug: r.slug, Name: r.slug, Active: r.status == "active"}
		if pkg, err := plugins.LoadPackageFile(filepath.Join(s.plugins.dir, r.slug+".zip"), s.plugins.keys, false); err == nil {
			m := pkg.Manifest
			row.Name, row.Version, row.Description, row.Author, row.AuthorURI, row.PluginURI = m.Name, m.Version, template.HTMLEscapeString(m.Description), m.Author, m.AuthorURI, m.PluginURI
			for _, p := range m.Permissions {
				row.Permissions = append(row.Permissions, permRow{string(p), p.Description()})
			}
		}
		if s.plugins.rt != nil {
			if lp, ok := s.plugins.rt.Plugin(r.slug); ok {
				row.Disabled, row.DisabledWhy = lp.Disabled()
				row.Logs = lp.Logs()
				if len(row.Logs) > 5 {
					row.Logs = row.Logs[len(row.Logs)-5:]
				}
			}
		}
		row.ActivateURL, row.DeactivateURL, row.DeleteURL = n("activate", r.slug), n("deactivate", r.slug), n("delete", r.slug)
		rows = append(rows, row)
	}
	return rows
}

func (s *Server) pluginsScreen(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("activate_plugins", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to manage plugins for this site.")
		return
	}
	if a := c.Query("action"); a != "" && c.Request.Method == http.MethodGet && a != "activate" {
		s.pluginAction(c, a, c.Query("plugin"))
		return
	}
	if c.Request.Method == http.MethodPost {
		bulk := c.PostForm("action")
		if bulk == "" || bulk == "-1" {
			bulk = c.PostForm("action2")
		}
		if strings.HasSuffix(bulk, "-selected") {
			s.pluginsBulk(c, bulk)
			return
		}
		s.pluginAction(c, c.PostForm("action"), c.PostForm("plugin"))
		return
	}
	d := pluginsData{Status: c.DefaultQuery("plugin_status", "all"), BulkNonce: s.nonce(c, "bulk-plugins"), CanDelete: u.Can("delete_plugins", nil)}
	if s.plugins.initErr != nil {
		d.RuntimeErr = s.plugins.initErr.Error()
	}
	all := s.installedPlugins(c)
	d.Search = strings.TrimSpace(c.Query("s"))
	q := strings.ToLower(d.Search)
	for _, r := range all {
		if r.Active {
			d.Active++
		} else {
			d.Inactive++
		}
		if q != "" && !strings.Contains(strings.ToLower(r.Name+" "+r.Description+" "+r.Author), q) {
			continue
		}
		if d.Status == "all" || (d.Status == "active") == r.Active {
			d.Rows = append(d.Rows, r)
		}
	}
	// Activating a Wasm plugin first shows the permissions it asks for.
	if c.Query("action") == "activate" {
		slug := c.Query("plugin")
		if !s.checkNonce(c, "activate-plugin_"+slug) {
			s.denyNonce(c)
			return
		}
		for i := range all {
			if all[i].Slug == slug && all[i].Native {
				s.pluginAction(c, "activate", slug)
				return
			}
			if all[i].Slug == slug {
				d.Consent = &all[i]
				d.ConsentNonce = s.nonce(c, "activate-plugin_"+slug)
			}
		}
	}
	var notices []notice
	switch {
	case c.Query("activate") == "true":
		notices = append(notices, notice{Type: "success", Message: "Plugin activated.", Dismissible: true})
		if c.Query("gwe_theme") == "hello" { // Golementor moved the site to its blank canvas theme
			notices = append(notices, notice{Type: "info", Message: template.HTML("<strong>Hello Pursae Web Builder</strong> is now the active theme: a blank canvas for Pursae Web Builder. Design the header, footer and pages under <a href=\"/wp-admin/theme-builder\">Appearance &rsaquo; Theme Builder</a>. Deactivating Pursae Web Builder brings back your previous theme."), Dismissible: true})
		}
		if c.Query("gwe_setup") == "1" && s.golementorOn() { // Golementor Studio: offer the setup wizard
			notices = append(notices, notice{Type: "info", Message: template.HTML("<strong>Pursae Web Builder is ready.</strong> Start from a complete starter site (header, footer, pages, blog, menus, colors and fonts) and edit everything afterwards.</p><p><a class=\"button button-primary\" href=\"" + gweSetupPath + "\">Run Site Setup</a> <a class=\"button\" href=\"/wp-admin/theme-builder\">Start from scratch</a>"), Dismissible: true})
		}
	case c.Query("deactivate") == "true":
		notices = append(notices, notice{Type: "success", Message: "Plugin deactivated.", Dismissible: true})
		if c.Query("gwe_theme") == "reset" { // Golementor: Hello Golementor was the active theme
			notices = append(notices, notice{Type: "warning", Message: template.HTML("Hello Pursae Web Builder belongs to Pursae Web Builder, so the site was switched to <strong>" +
				template.HTMLEscapeString(s.activeTheme().Name) + "</strong>. Your saved themes and built pages are kept; they come back when you activate Pursae Web Builder."), Dismissible: true})
		}
	case c.Query("activate-multi") != "":
		notices = append(notices, notice{Type: "success", Message: "Selected plugins activated.", Dismissible: true})
	case c.Query("deactivate-multi") != "":
		notices = append(notices, notice{Type: "success", Message: "Selected plugins deactivated.", Dismissible: true})
	case c.Query("deleted-multi") != "":
		notices = append(notices, notice{Type: "success", Message: "The selected plugins have been deleted.", Dismissible: true})
	case c.Query("deleted") == "true":
		notices = append(notices, notice{Type: "success", Message: "The selected plugin has been deleted.", Dismissible: true})
	case c.Query("installed") != "":
		notices = append(notices, notice{Type: "success", Message: template.HTML("Plugin <strong>" + template.HTMLEscapeString(c.Query("installed")) + "</strong> installed. Activate it to review and grant its permissions."), Dismissible: true})
	case c.Query("error") != "":
		notices = append(notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(c.Query("error")))})
	}
	if sk := c.Query("skipped"); sk != "" {
		notices = append(notices, notice{Type: "warning", Message: template.HTML("Skipped: <strong>" + template.HTMLEscapeString(sk) +
			"</strong>. Plugins that ask for permissions are activated one by one, from their own Activate link, so you can review what they may do; built-in and active plugins cannot be deleted."), Dismissible: true})
	}
	s.adminPage(c, "plugins", "Plugins", "plugins-php", "admin/plugins", d, notices...)
}

func (s *Server) pluginAction(c *gin.Context, action, slug string) {
	if !s.checkNonce(c, action+"-plugin_"+slug) {
		s.denyNonce(c)
		return
	}
	fail := func(err error) {
		c.Redirect(http.StatusFound, "/wp-admin/plugins?error="+url.QueryEscape(err.Error()))
	}
	if action == "activate" || action == "deactivate" {
		defer s.asAuditPlugin(c, action, slug)
	}
	switch slug {
	case asSlug: // AdminSecGo
		if err := s.adminsecPluginAction(c, action); err != nil {
			fail(err)
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/plugins?"+action+"=true")
		return
	case "classic-editor":
		_ = s.settings.Set(c, map[string]any{"native_classic_editor": action == "activate"})
		c.Redirect(http.StatusFound, "/wp-admin/plugins?"+action+"=true")
		return
	case "wpgo-cache":
		_ = s.settings.Set(c, map[string]any{"wpgo_cache_enabled": action == "activate"})
		s.configureCache()
		c.Redirect(http.StatusFound, "/wp-admin/plugins?"+action+"=true")
		return
	case maintSlug: // Maintenance-Go
		if err := s.maintPluginAction(c, action); err != nil {
			fail(err)
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/plugins?"+action+"=true")
		return
	case msSlug:
		var err error
		if action == "activate" {
			err = s.mailSMTPActivate(c)
		} else {
			err = s.mailSMTPDeactivate(c)
		}
		if err != nil {
			fail(err)
			return
		}
		if action == "activate" && !s.msLoad().WizardDone { // like upstream, the first activation opens the Setup Wizard
			c.Redirect(http.StatusFound, "/wp-admin/admin?page=wp-mail-smtp-setup-wizard")
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/plugins?"+action+"=true")
		return
	case acfgoSlug:
		toggle := s.acfgoDeactivate
		if action == "activate" {
			toggle = s.acfgoActivate
		}
		if err := toggle(c); err != nil {
			fail(err)
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/plugins?"+action+"=true")
		return
	case golylangSlug:
		if err := s.golylangSwitch(c, action == "activate"); err != nil {
			fail(err)
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/plugins?"+action+"=true")
		return
	case seogoSlug:
		s.seogoPluginAction(c, action)
		return
	case gooikeSlug: // Gooike (gooike.go)
		s.gooikePluginAction(c, action)
		return
	case gooSlug: // Goocommerce (goocommerce.go)
		s.gooPluginAction(c, action)
		return
	case gocdnSlug: // GoCDN (gocdn.go)
		s.gocdnPluginAction(c, action)
		return
	case hgSlug: // HeadlessGo (headlessgo.go)
		s.hgPluginAction(c, action)
		return
	case golementorSlug:
		q := action + "=true"
		if action == "activate" {
			before := s.settings.String("stylesheet")
			if err := s.golementorActivate(c); err != nil {
				fail(err)
				return
			}
			if s.settings.String("stylesheet") != before {
				q += "&gwe_theme=hello"
			}
			if s.gweStarter() == nil && len(s.gweLib().Kits) > 0 {
				q += "&gwe_setup=1" // Golementor Studio: the setup wizard (golementor_setup.go)
			}
		} else {
			switched, err := s.golementorDeactivate(c)
			if err != nil {
				fail(err)
				return
			}
			if switched {
				q += "&gwe_theme=reset"
			}
		}
		c.Redirect(http.StatusFound, "/wp-admin/plugins?"+q)
		return
	case gontactSlug:
		var err error
		if action == "activate" {
			err = s.gontactActivate(c, currentUser(c).ID)
		} else {
			err = s.gontactDeactivate(c)
		}
		if err != nil {
			fail(err)
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/plugins?"+action+"=true")
		return
	}
	// Installed plugins are addressed by slug: it must look like one and name a plugin that is installed,
	// otherwise the slug would be joined into a file path ("../x") and load a package that was never installed.
	if !plugins.ValidSlug(slug) {
		fail(errors.New("Invalid plugin."))
		return
	}
	var installed bool
	if err := s.store.Pool.QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM plugins WHERE slug = $1)`, slug).Scan(&installed); err != nil || !installed {
		fail(errors.New("That plugin is not installed."))
		return
	}
	ctx := c.Request.Context()
	switch action {
	case "activate":
		// Grant exactly the permissions the admin ticked on the consent screen.
		var granted []plugins.Permission
		for _, p := range c.PostFormArray("permissions[]") {
			if pp := plugins.Permission(p); pp.Valid() {
				granted = append(granted, pp)
			}
		}
		raw, _ := json.Marshal(granted)
		if s.plugins.rt != nil {
			_ = s.plugins.rt.Unload(ctx, slug)
		}
		if err := s.loadPlugin(ctx, slug, raw); err != nil {
			fail(err)
			return
		}
		_, _ = s.store.Pool.Exec(ctx, `UPDATE plugins SET status = 'active', permissions = $2 WHERE slug = $1`, slug, raw)
		s.plugins.hooks.DoAction(ctx, "init")
		s.purgeAll(ctx)
	case "deactivate":
		if s.plugins.rt != nil {
			_ = s.plugins.rt.Unload(ctx, slug)
		}
		_, _ = s.store.Pool.Exec(ctx, `UPDATE plugins SET status = 'inactive' WHERE slug = $1`, slug)
		s.purgeAll(ctx)
	case "delete":
		if !currentUser(c).Can("delete_plugins", nil) { // activate_plugins alone does not allow deleting
			fail(errors.New("Sorry, you are not allowed to delete plugins on this site."))
			return
		}
		if s.plugins.rt != nil {
			_ = s.plugins.rt.Unload(ctx, slug)
		}
		_, _ = s.store.Pool.Exec(ctx, `DELETE FROM plugins WHERE slug = $1`, slug)
		_ = os.Remove(filepath.Join(s.plugins.dir, filepath.Base(slug)+".zip"))
		c.Redirect(http.StatusFound, "/wp-admin/plugins?deleted=true")
		return
	default:
		c.Redirect(http.StatusFound, "/wp-admin/plugins")
		return
	}
	c.Redirect(http.StatusFound, "/wp-admin/plugins?"+action+"=true")
}

// installPackage verifies a package and stores it as an inactive plugin.
func (s *Server) installPackage(ctx context.Context, data []byte) (*plugins.Manifest, error) {
	pkg, err := plugins.LoadPackage(bytes.NewReader(data), int64(len(data)), s.plugins.keys, !s.settings.Bool("plugin_allow_unsigned"))
	if err != nil {
		if errors.Is(err, plugins.ErrUnsigned) || errors.Is(err, plugins.ErrBadSignature) {
			return nil, fmt.Errorf("the package signature could not be verified (%v); only plugins signed with a trusted key can be installed", err)
		}
		return nil, err
	}
	if err := pkg.Manifest.CheckCompatible(Version); err != nil {
		return nil, err
	}
	slug := pkg.Manifest.Slug
	if slug == "classic-editor" || slug == "wpgo-cache" || slug == maintSlug || slug == msSlug || slug == gontactSlug || slug == acfgoSlug || slug == golylangSlug || slug == seogoSlug || slug == gooikeSlug || slug == gooSlug || slug == gocdnSlug || slug == hgSlug {
		return nil, errors.New("that slug belongs to a built-in plugin")
	}
	if err := os.WriteFile(filepath.Join(s.plugins.dir, slug+".zip"), data, 0o644); err != nil {
		return nil, err
	}
	_, err = s.store.Pool.Exec(ctx, `INSERT INTO plugins (slug, version, signature) VALUES ($1, $2, $3)
		ON CONFLICT (slug) DO UPDATE SET version = EXCLUDED.version, signature = EXCLUDED.signature`,
		slug, pkg.Manifest.Version, fmt.Sprintf("%x", pkg.Digest))
	return pkg.Manifest, err
}

type marketItem struct {
	Slug, Name, Version, Description, Author string
	Permissions                              []permRow
	Installed                                bool
	InstallURL                               string
	Demo                                     bool
	Icon                                     string
	IconColor                                string // wpgo-plugin-icon-<color> background for the generated icon
	// Marketplace (Hub) listings.
	Hub       bool
	IconURL   string
	Homepage  string
	Downloads int
	Updated   string
	// Native (built-in) plugins.
	Native, Active           bool
	SettingsURL, ActivateURL string
	NativeKind               string // "Built in" note shown in the card footer
}

type pluginInstallData struct {
	Tab         string
	Search      string
	Items       []marketItem
	Counts      map[string]int
	UploadNonce string
}

// pluginInstall is plugin-install.php: a small marketplace and the zip upload form.
func (s *Server) pluginInstall(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("install_plugins", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to install plugins on this site.")
		return
	}
	ctx := c.Request.Context()
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "plugin-upload") {
			s.denyNonce(c)
			return
		}
		f, _, err := c.Request.FormFile("pluginzip")
		if err != nil {
			c.Redirect(http.StatusFound, "/wp-admin/plugins?error="+url.QueryEscape("No package was uploaded."))
			return
		}
		data, _ := io.ReadAll(io.LimitReader(f, 32<<20))
		f.Close()
		m, err := s.installPackage(ctx, data)
		if err != nil {
			c.Redirect(http.StatusFound, "/wp-admin/plugins?error="+url.QueryEscape("Installation failed: "+err.Error()))
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/plugins?installed="+url.QueryEscape(m.Name))
		return
	}
	if c.Query("action") == "install-plugin" {
		slug := c.Query("plugin")
		if !s.checkNonce(c, "install-plugin_"+slug) {
			s.denyNonce(c)
			return
		}
		if c.Query("source") == "hub" {
			m, err := s.hubInstallPlugin(ctx, slug)
			if err != nil {
				c.Redirect(http.StatusFound, "/wp-admin/plugins?error="+url.QueryEscape("Installation failed: "+err.Error()))
				return
			}
			c.Redirect(http.StatusFound, "/wp-admin/plugins?installed="+url.QueryEscape(m.Name))
			return
		}
		for _, path := range s.plugins.market {
			pkg, err := plugins.LoadPackageFile(path, nil, false)
			if err != nil || pkg.Manifest.Slug != slug {
				continue
			}
			data, _ := os.ReadFile(path)
			m, err := s.installPackage(ctx, data)
			if err != nil {
				c.Redirect(http.StatusFound, "/wp-admin/plugins?error="+url.QueryEscape("Installation failed: "+err.Error()))
				return
			}
			c.Redirect(http.StatusFound, "/wp-admin/plugins?installed="+url.QueryEscape(m.Name))
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/plugin-install?error=notfound")
		return
	}
	d := pluginInstallData{Tab: c.DefaultQuery("tab", "featured"), Search: strings.TrimSpace(c.Query("s")), UploadNonce: s.nonce(c, "plugin-upload")}
	installed := map[string]bool{}
	var native []marketItem
	icons := map[string][2]string{"classic-editor": {"dashicons-edit-page", "blue"}, "wpgo-cache": {"dashicons-performance", "green"}, golementorSlug: {"dashicons-layout", "purple"}, maintSlug: {"dashicons-hammer", "teal"}, msSlug: {"dashicons-email-alt", "blue"}, gontactSlug: {"dashicons-email", "teal"}, acfgoSlug: {"dashicons-welcome-widgets-menus", "green"}, golylangSlug: {"dashicons-translation", "teal"}, seogoSlug: {"dashicons-chart-line", "blue"}, gooikeSlug: {"dashicons-shield", "orange"}, gooSlug: {"dashicons-cart", "purple"}, gocdnSlug: {"dashicons-cloud-upload", "blue"}, hgSlug: {"dashicons-rest-api", "blue"}}
	for _, r := range s.installedPlugins(c) {
		installed[r.Slug] = true
		if r.Native {
			ic := icons[r.Slug]
			native = append(native, marketItem{Slug: r.Slug, Name: r.Name, Version: r.Version, Description: r.Description, Author: r.Author,
				Native: true, Active: r.Active, SettingsURL: r.SettingsURL, ActivateURL: r.ActivateURL, Icon: ic[0], IconColor: ic[1], NativeKind: "Built in to Pursae"})
		}
	}
	var featured []marketItem
	var notices []notice
	// The marketplace: packages published on the WordPress Go Hub.
	if s.hubBase() != "" {
		items, err := s.hubCatalog(ctx, "plugins")
		if err != nil {
			notices = append(notices, notice{Type: "warning", Message: template.HTML("The Pursae Hub could not be reached (" + html.EscapeString(err.Error()) + "). Only locally available packages are listed."), Dismissible: true})
		}
		for _, h := range items {
			it := marketItem{Slug: h.Slug, Name: h.Name, Version: h.Version, Description: h.Description, Author: h.Author, Hub: true,
				IconURL: h.IconURL, Homepage: h.Homepage, Downloads: h.Downloads, Installed: installed[h.Slug], Icon: "dashicons-admin-plugins", IconColor: "teal",
				InstallURL: "/wp-admin/plugin-install?action=install-plugin&source=hub&plugin=" + h.Slug + "&_wpnonce=" + s.nonce(c, "install-plugin_"+h.Slug)}
			if t, err := time.Parse(time.RFC3339, h.Updated); err == nil {
				it.Updated = t.In(s.loc()).Format("January 2, 2006")
			}
			for _, p := range h.Permissions {
				it.Permissions = append(it.Permissions, permRow{p, plugins.Permission(p).Description()})
			}
			featured = append(featured, it)
		}
	}
	hubSlugs := map[string]bool{}
	for _, it := range featured {
		hubSlugs[it.Slug] = true
	}
	for _, path := range s.plugins.market {
		pkg, err := plugins.LoadPackageFile(path, nil, false)
		if err != nil {
			continue
		}
		m := pkg.Manifest
		if hubSlugs[m.Slug] {
			continue
		}
		it := marketItem{Slug: m.Slug, Name: m.Name, Version: m.Version, Description: m.Description, Author: m.Author,
			Installed: installed[m.Slug], InstallURL: "/wp-admin/plugin-install?action=install-plugin&plugin=" + m.Slug + "&_wpnonce=" + s.nonce(c, "install-plugin_"+m.Slug), Icon: "dashicons-admin-plugins", IconColor: "teal"}
		for _, p := range m.Permissions {
			it.Permissions = append(it.Permissions, permRow{string(p), p.Description()})
		}
		featured = append(featured, it)
	}
	sort.SliceStable(featured, func(i, j int) bool {
		if featured[i].Hub != featured[j].Hub {
			return featured[i].Hub
		}
		return featured[i].Name < featured[j].Name
	})
	// Fan-made concept listings: not real packages, shown on their own tab so they never look installable.
	demos := []marketItem{
		{Name: "GoForms", Description: "Drag-and-drop contact forms with spam protection, stored submissions and e-mail notifications.", Author: "Pursae fans", Icon: "dashicons-feedback"},
		{Name: "SEO Gopher", Description: "Meta titles, Open Graph tags, XML sitemaps and redirects, written as a sandboxed Wasm plugin.", Author: "Pursae fans", Icon: "dashicons-search"},
		{Name: "Gopher Shop", Description: "Products, carts and checkout for small stores. Payments through Stripe and PayPal.", Author: "Pursae fans", Icon: "dashicons-cart"},
		{Name: "Backup Burrow", Description: "Scheduled database and uploads backups to S3-compatible storage.", Author: "Pursae fans", Icon: "dashicons-backup"},
	}
	for i := range demos {
		demos[i].Demo = true
		demos[i].Slug = strings.ToLower(strings.ReplaceAll(demos[i].Name, " ", "-"))
	}
	d.Counts = map[string]int{"featured": len(featured), "native": len(native), "demos": len(demos)}
	switch d.Tab {
	case "native":
		d.Items = native
	case "demos":
		d.Items = demos
	case "search":
		q := strings.ToLower(d.Search)
		for _, it := range append(append(append([]marketItem{}, featured...), native...), demos...) {
			if q == "" || strings.Contains(strings.ToLower(it.Name+" "+it.Description+" "+it.Author), q) {
				d.Items = append(d.Items, it)
			}
		}
	case "upload":
		d.Items = featured
	default:
		d.Tab = "featured"
		d.Items = featured
	}
	body := "plugin-install-php"
	if d.Tab == "upload" {
		body += " show-upload-view"
	}
	s.adminPage(c, "plugin-install", "Add Plugins", body, "admin/plugin-install", d, notices...)
}

// pluginPostStats lets plugins add numbers to the classic editor's publish box (wpgo_post_stats).
func (s *Server) pluginPostStats(ctx context.Context, p *store.Post) map[string]any {
	out, _ := s.applyFilters(ctx, "wpgo_post_stats", map[string]any{}, map[string]any{"id": p.ID, "content": p.Content, "title": p.Title}).(map[string]any)
	return out
}

// statsLine renders plugin stats for the publish box.
func statsLine(st map[string]any) string {
	var parts []string
	keys := make([]string, 0, len(st))
	for k := range st {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, strings.ReplaceAll(k, "_", " ")+": "+fmt.Sprint(st[k]))
	}
	return strings.Join(parts, " · ")
}
