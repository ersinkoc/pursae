package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/cli"
	"wpgo/internal/config"
	"wpgo/internal/plugins"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// registerExtensionCLI registers the plugin and theme commands. See cli.go for the environment (cliEnv) and conventions.
//
// Nouns: plugin (list, get, status, activate, deactivate, delete/uninstall, install, update, key; NOT new/build/keygen/pack/verify, which cli.go registers), theme.
//
// The commands reuse the Plugins and Themes screens' code paths: plugin activate/deactivate/delete
// run pluginAction itself (the REST runAdminAs pattern, signed in as the acting administrator), so
// built-in switches, Wasm loading and lifecycle hooks, dependency checks, auditing and statistics
// behave as in the admin. What reaches a running server, and when:
//   - built-in plugins and theme switches are settings: live through the settings NOTIFY trigger;
//   - Wasm plugins are rows of the plugins table and zips in the shared plugin directory: running
//     servers load, reload or unload them within ~10-20 s (syncPluginsOnce);
//   - page cache purges go out on wpgo_cache_purge (the cliServer's purge-only cache);
//   - process-local state (page cache configuration, the theme registry, trusted keys, ACF-Go
//     registrations) needs a restart: the commands warn where that applies.
func registerExtensionCLI(r *cli.Registry, e *cliEnv) {
	registerPluginCLI(r, e)
	registerThemeCLI(r, e)
}

// --- shared plumbing -------------------------------------------------------------------

// extServer is the CLI server with the plugin system (keys, package directory, market) set up
// like startPlugins does, without the runtime.
func (e *cliEnv) extServer(ctx context.Context) (*Server, error) {
	s, err := e.Server(ctx)
	if err != nil {
		return nil, err
	}
	s.extPluginSystem(ctx)
	return s, nil
}

// extPluginSystem mirrors the plugin parts of startPlugins (plugins.go) without creating the
// runtime, loading plugins or starting the sync loop.
func (s *Server) extPluginSystem(ctx context.Context) {
	if s.plugins != nil {
		return
	}
	ps := &pluginSystem{hooks: plugins.NewRegistry()}
	ps.dir, _ = s.pluginDirs()
	for _, k := range strings.FieldsFunc(config.Env("PLUGIN_KEYS"), func(r rune) bool { return r == ',' || r == '\n' || r == ' ' }) {
		if pub, err := plugins.ParsePublicKey(k); err == nil {
			ps.envKeys = append(ps.envKeys, pub)
		}
	}
	for _, dir := range []string{config.Env("MARKET_DIR"), "examples/plugins/dist", "/usr/share/wpgo/plugins"} {
		if dir == "" {
			continue
		}
		if m, _ := filepath.Glob(filepath.Join(dir, "*.zip")); len(m) > 0 {
			ps.market = m
			if pub, err := os.ReadFile(filepath.Join(dir, "dev-key.pub")); err == nil && devTrustDevKey() { //nolint:gosec // G703: the market directory is operator configuration
				if k, err := plugins.ParsePublicKey(lastLine(string(pub))); err == nil {
					ps.envKeys = append(ps.envKeys, k)
				}
			}
			break
		}
	}
	s.plugins = ps
	s.applySiteKeys()
	s.hubTrustCached(ctx) // the Manager's keys, so Manager-signed packages verify
}

// extStartRuntime creates the Wasm runtime, so loadPlugin really verifies and loads a package and
// the activate/deactivate/uninstall lifecycle hooks run. The returned func releases it.
func (s *Server) extStartRuntime(ctx context.Context) (func(), error) {
	ps := s.plugins
	if ps.rt != nil {
		return func() {}, nil
	}
	rt, err := plugins.NewRuntime(ctx, ps.hooks, plugins.Config{CoreVersion: Version, MaxHTTPBody: pluginHTTPMaxBody, Logger: slog.Default(),
		OnDisable: func(slug, reason string) {
			slog.Warn("plugin disabled by the runtime", "plugin", slug, "reason", reason)
			s.recordPluginDisabled(context.Background(), slug, reason)
		}})
	if err != nil {
		ps.initErr = err // loadPlugin now fails instead of "activating" an unverified package
		return nil, cli.Errorf("the plugin runtime could not start: %v", err)
	}
	ps.rt = rt
	return func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = rt.Close(cctx)
		ps.rt = nil
	}, nil
}

// extKeepCache runs fn and then puts the purge-only page cache back. Some admin paths call
// configureCache (plugin toggles, theme switches and installs), which would replace it with a
// serving cache whose purges are only queued; its PurgeAll on the old cache already went out as
// an announcement, so running servers drop their pages either way. The preload timer the new
// cache may have scheduled is stopped: a CLI process never crawls the site.
func (s *Server) extKeepCache(fn func()) {
	pc := s.cache.Load()
	defer func() {
		if pc != nil && s.cache.Load() != pc {
			s.cache.Store(pc)
		}
		s.warm.mu.Lock()
		if s.warm.timer != nil {
			s.warm.timer.Stop()
			s.warm.timer = nil
		}
		s.warm.mu.Unlock()
	}()
	fn()
}

// extAdminContext is a synthetic admin request signed in as u (nil: anonymous), for the admin
// handlers the commands reuse and as the context of audited calls (asAudit reads the user from a
// *gin.Context). Values, cancellation and deadlines fall back to ctx.
func (s *Server) extAdminContext(ctx context.Context, u *store.User, form url.Values) (*gin.Context, *httptest.ResponseRecorder) {
	if gin.Mode() == gin.DebugMode {
		gin.SetMode(gin.ReleaseMode) // no debug banner on the command's stdout
	}
	rec := httptest.NewRecorder()
	sub, eng := gin.CreateTestContext(rec)
	eng.ContextWithFallback = true
	req := httptest.NewRequest(http.MethodPost, "/wp-admin/plugins", http.NoBody).WithContext(ctx)
	if home, err := url.Parse(s.settings.String("home")); err == nil && home.Host != "" {
		req.Host, req.URL.Host, req.URL.Scheme = home.Host, home.Host, home.Scheme
	}
	req.RemoteAddr = "" // the audit log shows no IP for command-line actions
	req.Header.Set("User-Agent", "pursae-cli")
	if form == nil {
		form = url.Values{}
	}
	req.PostForm, req.Form = form, form
	sub.Request = req
	sub.Keys = map[any]any{}
	if u != nil {
		sub.Keys[ctxUser] = u
	}
	return sub, rec
}

// extActionContext is the context theme commands pass to audited helpers: an admin request as the
// acting user when there is one. Without --user and without an administrator it is plain ctx.
func (e *cliEnv) extActionContext(c *cli.Ctx, s *Server) (context.Context, error) {
	u, err := e.Actor(c)
	if err != nil {
		if c.Flag("user") != "" {
			return nil, err
		}
		return c.Context, nil
	}
	gc, _ := s.extAdminContext(c.Context, u, nil)
	return gc, nil
}

// extFlushTelemetry sends the statistics events the command queued (hubEvent keeps them in
// memory and flushes on a timer a short-lived process never reaches).
func (s *Server) extFlushTelemetry(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	s.hubFlushEvents(ctx)
}

// readPackageArg reads a package zip from a file or, for "-", standard input, up to max bytes.
func readPackageArg(c *cli.Ctx, arg string, max int64) ([]byte, error) {
	var r io.Reader
	if arg == "-" {
		r = c.Stdin()
	} else {
		f, err := os.Open(arg) //nolint:gosec // the operator names the file
		if err != nil {
			return nil, cli.Errorf("%v", err)
		}
		defer f.Close()
		if st, err := f.Stat(); err == nil && st.Size() > max {
			return nil, cli.Errorf("%s: the package is larger than %d MiB", arg, max>>20)
		}
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, cli.Errorf("%s: %v", arg, err)
	}
	if int64(len(data)) > max {
		return nil, cli.Errorf("%s: the package is larger than %d MiB", arg, max>>20)
	}
	return data, nil
}

// isPackagePath reports whether an install argument names a package file rather than a slug:
// "-" (stdin), a .zip name or a path. A bare word is a file only when it exists (statFile).
func isPackagePath(arg string, statFile func(string) bool) bool {
	if arg == "-" || strings.HasSuffix(strings.ToLower(arg), ".zip") || strings.ContainsAny(arg, `/\`) {
		return true
	}
	return statFile != nil && statFile(arg)
}

func extRegularFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// extFieldRequested reports whether the output asks for field (expensive columns are only computed then).
func extFieldRequested(c *cli.Ctx, field string) bool {
	if strings.EqualFold(c.Field, field) {
		return true
	}
	for _, f := range c.Fields {
		if strings.EqualFold(f, field) {
			return true
		}
	}
	return false
}

// --- plugins ---------------------------------------------------------------------------

// pluginRestartSlugs are built-in plugins whose switch also changes process-local state on the
// server that saves it (page cache configuration, cache exclusions, ACF-Go registrations).
var pluginRestartSlugs = map[string]string{
	"wpgo-cache": "the page cache is configured at start-up",
	gooSlug:      "the store pages join the page cache exclusions at start-up",
	forumSlug:    "the forum's cache variants are configured at start-up",
	acfgoSlug:    "custom post types and fields are registered at start-up",
}

// pluginStatus is a row's WP-CLI status: active, inactive or disabled (stopped by the runtime or
// refused at load, with a reason).
func pluginStatus(r pluginRow) string {
	switch {
	case r.Disabled:
		return "disabled"
	case r.Active:
		return "active"
	}
	return "inactive"
}

// pluginCLIRow is pluginJSON with WP-CLI field names: name is the slug, title the display name.
func pluginCLIRow(r pluginRow) cli.Row {
	row := cli.Row(pluginJSON(r))
	row["title"] = row["name"]
	row["name"] = r.Slug
	row["status"] = pluginStatus(r)
	row["description"] = extPlainText(r.Description)
	perms := make([]string, 0, len(r.Permissions))
	for _, p := range r.Permissions {
		perms = append(perms, p.Name)
	}
	row["permissions"] = perms
	if _, ok := row["disabled"]; !ok {
		row["disabled"], row["disabled_reason"] = false, ""
	}
	if _, ok := row["update_available"]; !ok {
		row["update_available"] = false
	}
	row["missing_permissions"] = append([]string{}, r.MissingPerms...)
	if u := pluginURLSlug(r.Slug); u != r.Slug {
		row["alias"] = u
	}
	return row
}

// findPluginRow finds a plugin by slug or by its Plugins screen alias (web-builder).
func findPluginRow(rows []pluginRow, slug string) (pluginRow, bool) {
	slug = pluginSlugFromURL(strings.TrimSpace(slug))
	for _, r := range rows {
		if r.Slug == slug {
			return r, true
		}
	}
	return pluginRow{}, false
}

// pluginNotFound is the error for an unknown slug, with a suggestion when one is close.
func pluginNotFound(rows []pluginRow, slug string) error {
	var best string
	for _, r := range rows {
		if strings.Contains(r.Slug, slug) || strings.Contains(slug, r.Slug) {
			best = r.Slug
			break
		}
	}
	if best != "" {
		return cli.Errorf("the '%s' plugin could not be found. Did you mean '%s'?", slug, best)
	}
	return cli.Errorf("the '%s' plugin could not be found", slug)
}

// safePluginPermissions are the permissions a manifest declares minus the dangerous ones
// (content:html, admin:scripts: the equivalent of an administrator's session). It is what the REST
// API, MCP and the CLI grant when no permissions are named; the consent screen leaves the dangerous
// ones unticked as well, so they are granted only when someone names them.
func safePluginPermissions(declared []permRow) []string {
	out := make([]string, 0, len(declared))
	for _, p := range declared {
		if !p.Danger && !plugins.Permission(p.Name).Dangerous() {
			out = append(out, p.Name)
		}
	}
	return out
}

// grantedPermissions is what activate grants a Wasm plugin: without --grant every permission its
// manifest asks for except the dangerous ones (as REST and MCP do); with --grant the names listed,
// where "all" stands for every non-dangerous one and "none" (alone) grants nothing. A dangerous
// permission is granted only when it is named. Names the manifest does not declare are refused.
func grantedPermissions(declared []permRow, grant []string, given bool) ([]string, error) {
	all := make([]string, 0, len(declared))
	for _, p := range declared {
		all = append(all, p.Name)
	}
	safe := safePluginPermissions(declared)
	if !given {
		return safe, nil
	}
	if len(grant) == 1 && (grant[0] == "none" || grant[0] == "") {
		return []string{}, nil
	}
	out := []string{}
	for _, g := range grant {
		if g == "all" {
			for _, p := range safe {
				if !slices.Contains(out, p) {
					out = append(out, p)
				}
			}
			continue
		}
		if !slices.Contains(all, g) {
			if len(all) == 0 {
				return nil, cli.Usagef("unknown permission %q: the plugin asks for none", g)
			}
			return nil, cli.Usagef("unknown permission %q; the plugin asks for: %s", g, strings.Join(all, ", "))
		}
		if !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	return out, nil
}

// pluginActionResult turns pluginAction's response into an error: an error= redirect, a 4xx/5xx
// (the nonce screen) or anything but a redirect or a page. A built-in plugin's setup wizard
// redirect is a success.
func pluginActionResult(loc string, status int, body, action string) error {
	if u, err := url.Parse(loc); err == nil {
		if msg := u.Query().Get("error"); msg != "" {
			return errors.New(wp.StripTags(msg))
		}
	}
	if status >= 400 || (status != http.StatusFound && status != http.StatusOK && status != http.StatusSeeOther) {
		msg := strings.TrimSpace(wp.StripTags(body))
		if msg == "" || len(msg) > 300 {
			msg = fmt.Sprintf("the plugin could not be %sed (HTTP %d)", strings.TrimSuffix(action, "e"), status)
		}
		return errors.New(msg)
	}
	return nil
}

// cliPluginAction runs the Plugins screen's action for one plugin, signed in as u.
func (s *Server) cliPluginAction(ctx context.Context, u *store.User, action, slug string, perms []string) (err error) {
	form := url.Values{"action": {action}, "plugin": {slug}}
	for _, p := range perms {
		form.Add("permissions[]", p)
	}
	sub, rec := s.extAdminContext(ctx, u, form)
	form.Set("_wpnonce", s.nonce(sub, action+"-plugin_"+slug))
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s: internal error: %v", slug, r)
		}
	}()
	s.extKeepCache(func() { s.pluginAction(sub, action, slug) })
	status := rec.Code
	if sub.Writer.Written() {
		status = sub.Writer.Status()
	}
	return pluginActionResult(rec.Header().Get("Location"), status, rec.Body.String(), action)
}

// pluginTargets resolves the slugs of activate/deactivate/delete: the arguments, or with --all
// every installed package (built-in plugins are named one by one: --all would also switch on
// maintenance mode, a shop, a forum ...).
func pluginTargets(c *cli.Ctx, rows []pluginRow, all bool, keep func(pluginRow) bool) ([]pluginRow, error) {
	if all && len(c.Args) > 0 {
		return nil, cli.Usagef("pass plugin slugs or --all, not both")
	}
	if !all && len(c.Args) == 0 {
		return nil, cli.Usagef("please specify one or more plugins, or use --all")
	}
	var out []pluginRow
	if all {
		for _, r := range rows {
			if !r.Native && keep(r) {
				out = append(out, r)
			}
		}
		return out, nil
	}
	for _, a := range c.Args {
		r, ok := findPluginRow(rows, a)
		if !ok {
			return nil, pluginNotFound(rows, a)
		}
		out = append(out, r)
	}
	return out, nil
}

func registerPluginCLI(r *cli.Registry, e *cliEnv) {
	pluginFields := []string{"name", "status", "version", "source"}
	r.Add(cli.Command{
		Path:  "plugin list",
		Short: "List built-in plugins and installed plugin packages.",
		Long: "Built-in plugins are part of the binary (source native) and are switched on and off; Wasm plugin\n" +
			"packages are installed from Pursae Manager (hub) or uploaded (upload).\n\n" +
			"Fields: name (slug), title, status (active|inactive|disabled), version, source, native, author,\n" +
			"description, permissions, missing_permissions, disabled_reason, alias, update, update_version.\n" +
			"update and update_version ask Pursae Manager and are only computed when requested with --fields or --field.",
		Flags: []cli.Flag{
			{Name: "status", Usage: "Only plugins with this status.", Values: []string{"active", "inactive", "disabled"}},
			{Name: "source", Usage: "Only plugins from this source.", Values: []string{pluginSourceNative, pluginSourceUpload, pluginSourceHub}},
		},
		Examples: []string{"plugin list", "plugin list --status=active --format=json", "plugin list --source=hub --fields=name,version,update,update_version", "plugin list --status=active --format=ids"},
		Run: func(c *cli.Ctx) error {
			s, err := e.extServer(c.Context)
			if err != nil {
				return err
			}
			var ups map[string]hubItem
			if extFieldRequested(c, "update") || extFieldRequested(c, "update_version") {
				ups = s.hubPluginUpdates(c.Context)
			}
			var out []cli.Row
			for _, p := range s.pluginInventory(c.Context) {
				if st := c.Flag("status"); st != "" && pluginStatus(p) != st {
					continue
				}
				if src := c.Flag("source"); src != "" && p.Source != src {
					continue
				}
				row := pluginCLIRow(p)
				row["update"], row["update_version"] = "none", ""
				if it, ok := ups[p.Slug]; ok {
					row["update"], row["update_version"], row["update_available"] = "available", it.Version, true
				}
				out = append(out, row)
			}
			return c.Print(out, cli.View{Fields: pluginFields, ID: "name"})
		},
	})
	r.Add(cli.Command{
		Path:     "plugin get",
		Short:    "Get details about a plugin.",
		Args:     []cli.Arg{{Name: "plugin", Usage: "Plugin slug (web-builder is accepted for golementor)."}},
		Examples: []string{"plugin get seo-go", "plugin get my-plugin --field=version", "plugin get my-plugin --format=json"},
		Run: func(c *cli.Ctx) error {
			s, err := e.extServer(c.Context)
			if err != nil {
				return err
			}
			rows := s.pluginInventory(c.Context)
			p, ok := findPluginRow(rows, c.Arg(0))
			if !ok {
				return pluginNotFound(rows, c.Arg(0))
			}
			row := pluginCLIRow(p)
			row["requires_plugins"], row["requires"], row["signer"], row["plugin_uri"], row["author_uri"] = []string{}, "", "", p.PluginURI, p.AuthorURI
			if !p.Native {
				if pkg, err := plugins.LoadPackageFile(filepath.Join(s.plugins.dir, p.Slug+".zip"), nil, false); err == nil {
					row["requires_plugins"] = append([]string{}, pkg.Manifest.RequiresPlugins...)
					row["requires"] = pkg.Manifest.Requires
				}
				var signer string
				_ = s.store.Pool.QueryRow(c.Context, `SELECT signer FROM plugins WHERE slug = $1`, p.Slug).Scan(&signer)
				row["signer"] = signer
			}
			return c.PrintItem(row, []string{"name", "title", "status", "version", "source", "native", "author", "description", "permissions", "requires_plugins", "signer"})
		},
	})
	r.Add(cli.Command{
		Path:     "plugin status",
		Short:    "Reveal the status of one or all plugins.",
		Long:     "Without a slug: one line per plugin with its status letter (A active, I inactive, D disabled) and\nN for a built-in plugin. With a slug: that plugin's details.",
		Args:     []cli.Arg{{Name: "plugin", Usage: "Plugin slug.", Optional: true}},
		Examples: []string{"plugin status", "plugin status maintenance"},
		Run: func(c *cli.Ctx) error {
			s, err := e.extServer(c.Context)
			if err != nil {
				return err
			}
			rows := s.pluginInventory(c.Context)
			if slug := c.Arg(0); slug != "" {
				p, ok := findPluginRow(rows, slug)
				if !ok {
					return pluginNotFound(rows, slug)
				}
				c.Println(pluginStatusDetails(p))
				return nil
			}
			c.Println(pluginStatusSummary(rows))
			return nil
		},
	})

	actionFlags := func(extra ...cli.Flag) []cli.Flag {
		return append([]cli.Flag{{Name: "all", Bool: true, Usage: "Every installed plugin package (built-in plugins must be named)."}}, extra...)
	}
	r.Add(cli.Command{
		Path:  "plugin activate",
		Short: "Activate one or more plugins.",
		Long: "Runs the Plugins screen's activation: a built-in plugin is switched on with its side effects\n" +
			"(scheduled events, roles, content types, pages); a Wasm package is verified, loaded and its\n" +
			"activate hook runs. A Wasm plugin is granted every permission its manifest asks for except the\n" +
			"dangerous ones (content:html, admin:scripts: the equivalent of an administrator's session), like\n" +
			"the REST API; --grant names the permissions instead (all = every non-dangerous one, none =\n" +
			"nothing), and a dangerous permission is granted only when --grant names it. Plugins it requires\n" +
			"must be active first. Running servers load a Wasm plugin within about 20 seconds.",
		Args:     []cli.Arg{{Name: "plugin", Usage: "Plugin slug.", Optional: true, Repeat: true}},
		Flags:    actionFlags(cli.Flag{Name: "grant", Usage: "Permissions to grant a Wasm plugin, comma-separated: names, all (every non-dangerous one; the default) or none. content:html and admin:scripts only when named."}),
		Examples: []string{"plugin activate seo-go", "plugin activate my-plugin --grant=options:read,content:filter", "plugin activate my-plugin --grant=all,content:html", "plugin activate --all"},
		Run: func(c *cli.Ctx) error {
			return e.pluginActivate(c, c.Bool("all"))
		},
	})
	r.Add(cli.Command{
		Path:     "plugin deactivate",
		Short:    "Deactivate one or more plugins.",
		Long:     "Runs the Plugins screen's deactivation (the plugin's deactivate hook, then unloading).\nWith --uninstall a Wasm package is deleted afterwards (asks for confirmation).",
		Args:     []cli.Arg{{Name: "plugin", Usage: "Plugin slug.", Optional: true, Repeat: true}},
		Flags:    actionFlags(cli.Flag{Name: "uninstall", Bool: true, Usage: "Delete the plugin packages after deactivating them."}),
		Examples: []string{"plugin deactivate maintenance", "plugin deactivate my-plugin --uninstall --yes", "plugin deactivate --all"},
		Run:      e.pluginDeactivate,
	})
	for _, verb := range []string{"delete", "uninstall"} {
		short := "Delete plugin packages and their data."
		if verb == "uninstall" {
			short = "Uninstall plugin packages (alias of plugin delete)."
		}
		r.Add(cli.Command{
			Path:  "plugin " + verb,
			Short: short,
			Long: "Runs the plugin's uninstall hook, then removes its row, data (plugin_data, scheduled events,\n" +
				"wasm:<slug>:* options, transients) and package files. Built-in plugins cannot be deleted;\n" +
				"an active plugin is refused unless --deactivate is given. Asks for confirmation (--yes).",
			Args:     []cli.Arg{{Name: "plugin", Usage: "Plugin slug.", Optional: true, Repeat: true}},
			Flags:    actionFlags(cli.Flag{Name: "deactivate", Bool: true, Usage: "Deactivate active plugins first."}),
			Examples: []string{"plugin " + verb + " my-plugin --yes", "plugin " + verb + " my-plugin --deactivate --yes"},
			Run:      e.pluginDelete,
		})
	}
	r.Add(cli.Command{
		Path:  "plugin install",
		Short: "Install plugin packages from a zip file or by slug.",
		Long: "A file (or - for standard input) is verified against the trusted keys and installed like an\n" +
			"upload. A slug is installed from Pursae Manager, or from the market directory (PURSAE_MARKET_DIR)\n" +
			"when no Manager is configured. A new plugin is installed inactive; a new version of an active\n" +
			"plugin is loaded right away. An older version, or a package signed by another publisher, is\n" +
			"refused unless --force is given (files and market packages only).",
		Args: []cli.Arg{{Name: "plugin", Usage: "Zip file, - or slug.", Repeat: true}},
		Flags: []cli.Flag{
			{Name: "activate", Bool: true, Usage: "Activate the plugins after installing them."},
			{Name: "force", Bool: true, Usage: "Replace an installed plugin even with an older version or another signer."},
			{Name: "grant", Usage: "With --activate: permissions to grant, comma-separated: names, all (every non-dangerous one; the default) or none. content:html and admin:scripts only when named."},
		},
		Examples: []string{"plugin install ./my-plugin-1.2.0.zip --activate", "plugin install hello-dolly", "plugin install - < my-plugin.zip"},
		Run:      e.pluginInstall,
	})
	r.Add(cli.Command{
		Path:  "plugin update",
		Short: "Update plugins installed from Pursae Manager.",
		Long:  "Downloads the newest version from Pursae Manager. An active plugin is reloaded and rolled back\nwhen the new version cannot be loaded. Uploaded plugins are updated by installing the new zip.",
		Args:  []cli.Arg{{Name: "plugin", Usage: "Plugin slug.", Optional: true, Repeat: true}},
		Flags: []cli.Flag{
			{Name: "all", Bool: true, Usage: "Every plugin with an update."},
			{Name: "dry-run", Bool: true, Usage: "Only list the available updates."},
		},
		Examples: []string{"plugin update --all --dry-run", "plugin update my-plugin", "plugin update --all"},
		Run:      e.pluginUpdate,
	})

	r.Noun("plugin key", "Manage the Ed25519 keys whose signed plugin packages may be installed (Plugins > Trusted Keys).")
	r.Add(cli.Command{
		Path:     "plugin key list",
		Short:    "List the trusted plugin signing keys.",
		Long:     "Keys from the Trusted Keys screen (source option) and from PURSAE_PLUGIN_KEYS (source env, read-only).",
		Examples: []string{"plugin key list", "plugin key list --format=json"},
		Run: func(c *cli.Ctx) error {
			s, err := e.extServer(c.Context)
			if err != nil {
				return err
			}
			var out []cli.Row
			for _, k := range s.plugins.envKeys {
				out = append(out, cli.Row{"fingerprint": keyFingerprint(k), "key": plugins.EncodeKey(k), "comment": "", "source": "env"})
			}
			for _, k := range parseTrustedKeys(s.settings.String("plugin_trusted_keys")) {
				out = append(out, cli.Row{"fingerprint": k.Fingerprint, "key": k.Key, "comment": k.Comment, "source": "option"})
			}
			return c.Print(out, cli.View{Fields: []string{"fingerprint", "comment", "source", "key"}, ID: "fingerprint"})
		},
	})
	keyWarning := func(c *cli.Ctx) {
		c.Warning("running servers read the trusted keys at start-up: restart the server to apply")
	}
	r.Add(cli.Command{
		Path:     "plugin key add",
		Short:    "Trust a publisher's signing key.",
		Args:     []cli.Arg{{Name: "key", Usage: "Ed25519 public key, base64 (32 bytes)."}},
		Flags:    []cli.Flag{{Name: "comment", Usage: "Who the key belongs to."}},
		Examples: []string{"plugin key add 'MCowBQYDK2VwAyEA…' --comment='Acme Plugins'"},
		Run: func(c *cli.Ctx) error {
			s, err := e.extServer(c.Context)
			if err != nil {
				return err
			}
			msg, err := s.changeTrustedKeys(c.Context, "add", c.Arg(0), c.Flag("comment"), "")
			if err != nil {
				return cli.Errorf("%v", err)
			}
			c.Success("%s", msg)
			keyWarning(c)
			return nil
		},
	})
	r.Add(cli.Command{
		Path:     "plugin key remove",
		Short:    "Stop trusting a signing key.",
		Long:     "Installed plugins signed with the key keep running; new packages signed with it are refused.\nKeys from PURSAE_PLUGIN_KEYS cannot be removed here.",
		Args:     []cli.Arg{{Name: "fingerprint", Usage: "The key's fingerprint (plugin key list)."}},
		Examples: []string{"plugin key remove 3f2a9c… --yes"},
		Run: func(c *cli.Ctx) error {
			s, err := e.extServer(c.Context)
			if err != nil {
				return err
			}
			fp := strings.TrimSpace(c.Arg(0))
			for _, k := range s.plugins.envKeys {
				if keyFingerprint(k) == fp {
					return cli.Errorf("that key comes from PURSAE_PLUGIN_KEYS; remove it from the environment")
				}
			}
			if err := c.Confirm("Stop trusting the key %s?", fp); err != nil {
				return err
			}
			msg, err := s.changeTrustedKeys(c.Context, "remove", "", "", fp)
			if err != nil {
				return cli.Errorf("%v", err)
			}
			c.Success("%s", msg)
			keyWarning(c)
			return nil
		},
	})
}

// extPlainText is an HTML description as plain text for the terminal: tags stripped, entities
// decoded (&rsaquo; -> ›), whitespace collapsed.
func extPlainText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(wp.StripTags(s))), " ")
}

// pluginStatusSummary is `plugin status` without a slug.
func pluginStatusSummary(rows []pluginRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d installed plugins:\n", len(rows))
	width := 0
	for _, r := range rows {
		width = max(width, len(r.Slug))
	}
	for _, r := range rows {
		letter := strings.ToUpper(pluginStatus(r)[:1])
		native := " "
		if r.Native {
			native = "N"
		}
		fmt.Fprintf(&b, "  %s%s %-*s %s\n", letter, native, width, r.Slug, r.Version)
	}
	b.WriteString("\nLegend: A = Active, I = Inactive, D = Disabled, N = Built-in")
	return b.String()
}

// pluginStatusDetails is `plugin status <slug>`.
func pluginStatusDetails(r pluginRow) string {
	status := map[string]string{"active": "Active", "inactive": "Inactive", "disabled": "Disabled"}[pluginStatus(r)]
	if r.Disabled && r.DisabledWhy != "" {
		status += " (" + r.DisabledWhy + ")"
	}
	kind := "Plugin package (" + r.Source + ")"
	if r.Native {
		kind = "Built-in"
	}
	lines := []string{
		"Plugin " + r.Slug + " details:",
		"    Name: " + r.Name,
		"    Status: " + status,
		"    Version: " + r.Version,
		"    Type: " + kind,
		"    Author: " + r.Author,
		"    Description: " + extPlainText(r.Description),
	}
	if len(r.Permissions) > 0 {
		names := make([]string, 0, len(r.Permissions))
		for _, p := range r.Permissions {
			names = append(names, p.Name)
		}
		lines = append(lines, "    Permissions: "+strings.Join(names, ", "))
	}
	return strings.Join(lines, "\n")
}

// pluginApplyNotes tells what a running server does with a switched plugin.
func pluginApplyNotes(c *cli.Ctx, r pluginRow) {
	if why, ok := pluginRestartSlugs[r.Slug]; ok {
		c.Warning("%s: %s; restart the server to apply fully", r.Slug, why)
	}
}

func (e *cliEnv) pluginActivate(c *cli.Ctx, all bool) error {
	s, err := e.extServer(c.Context)
	if err != nil {
		return err
	}
	defer s.extFlushTelemetry(c.Context)
	rows := s.pluginInventory(c.Context)
	targets, err := pluginTargets(c, rows, all, func(r pluginRow) bool { return !r.Active })
	if err != nil {
		return err
	}
	return e.activatePlugins(c, s, targets)
}

// activatePlugins activates rows (install --activate shares it).
func (e *cliEnv) activatePlugins(c *cli.Ctx, s *Server, targets []pluginRow) error {
	u, err := e.Actor(c)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(targets, func(r pluginRow) bool { return !r.Native && !r.Active }) {
		release, err := s.extStartRuntime(c.Context)
		if err != nil {
			return err
		}
		defer release()
	}
	done, failed := 0, 0
	for _, r := range targets {
		if r.Active && !r.Disabled {
			c.Warning("plugin '%s' is already active.", r.Slug)
			done++
			continue
		}
		var perms []string
		if !r.Native {
			if perms, err = grantedPermissions(r.Permissions, c.List("grant"), c.Has("grant")); err != nil {
				return err
			}
			for _, p := range r.Permissions {
				if plugins.Permission(p.Name).Dangerous() && !slices.Contains(perms, p.Name) {
					c.Warning("plugin '%s' asks for %s (%s): not granted; name it with --grant to grant it.", r.Slug, p.Name, p.Description)
				}
			}
		}
		if err := s.cliPluginAction(c.Context, u, "activate", r.Slug, perms); err != nil {
			c.Warning("plugin '%s' could not be activated: %v", r.Slug, err)
			failed++
			continue
		}
		c.Success("Plugin '%s' activated.", r.Slug)
		pluginApplyNotes(c, r)
		if !r.Native {
			c.Log("Running servers load it within about 20 seconds.")
		}
		done++
	}
	return pluginBatchResult("activated", done, failed)
}

// pluginBatchResult is WP-CLI's summary error when some items of a batch failed.
func pluginBatchResult(verb string, done, failed int) error {
	if failed == 0 {
		return nil
	}
	return cli.Errorf("only %d of %d plugins %s", done, done+failed, verb)
}

func (e *cliEnv) pluginDeactivate(c *cli.Ctx) error {
	s, err := e.extServer(c.Context)
	if err != nil {
		return err
	}
	defer s.extFlushTelemetry(c.Context)
	rows := s.pluginInventory(c.Context)
	targets, err := pluginTargets(c, rows, c.Bool("all"), func(r pluginRow) bool { return r.Active || r.Disabled })
	if err != nil {
		return err
	}
	uninstall := c.Bool("uninstall")
	if uninstall {
		for _, r := range targets {
			if r.Native {
				return cli.Errorf("built-in plugins cannot be uninstalled ('%s'); deactivate it without --uninstall", r.Slug)
			}
		}
		if len(targets) > 0 {
			if err := c.Confirm("Deactivate and permanently delete %d plugin(s) and their data?", len(targets)); err != nil {
				return err
			}
		}
	}
	u, err := e.Actor(c)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(targets, func(r pluginRow) bool { return !r.Native }) {
		release, err := s.extStartRuntime(c.Context) // the deactivate and uninstall hooks run in loaded plugins
		if err != nil {
			return err
		}
		defer release()
		if err := s.cliLoadForHooks(c.Context, targets); err != nil {
			return err
		}
	}
	done, failed := 0, 0
	for _, r := range targets {
		if r.Active || r.Disabled {
			if err := s.cliPluginAction(c.Context, u, "deactivate", r.Slug, nil); err != nil {
				c.Warning("plugin '%s' could not be deactivated: %v", r.Slug, err)
				failed++
				continue
			}
			c.Success("Plugin '%s' deactivated.", r.Slug)
			pluginApplyNotes(c, r)
		} else if !uninstall {
			c.Warning("plugin '%s' isn't active.", r.Slug)
		}
		if uninstall {
			if err := s.cliPluginAction(c.Context, u, "delete", r.Slug, nil); err != nil {
				c.Warning("plugin '%s' could not be uninstalled: %v", r.Slug, err)
				failed++
				continue
			}
			c.Success("Uninstalled and deleted '%s' plugin.", r.Slug)
		}
		done++
	}
	return pluginBatchResult("deactivated", done, failed)
}

// cliLoadForHooks loads the active Wasm plugins among targets into the CLI's runtime, so their
// deactivate hook runs (on a server they are loaded already). A plugin that no longer loads is
// still deactivated, without its hook.
func (s *Server) cliLoadForHooks(ctx context.Context, targets []pluginRow) error {
	for _, r := range targets {
		if r.Native || !r.Active {
			continue
		}
		var perms []byte
		if err := s.store.Pool.QueryRow(ctx, `SELECT permissions FROM plugins WHERE slug = $1`, r.Slug).Scan(&perms); err != nil {
			continue
		}
		if err := s.loadPlugin(ctx, r.Slug, perms); err != nil {
			slog.Warn("plugin could not be loaded for its deactivate hook", "plugin", r.Slug, "err", err)
		}
	}
	return nil
}

func (e *cliEnv) pluginDelete(c *cli.Ctx) error {
	s, err := e.extServer(c.Context)
	if err != nil {
		return err
	}
	defer s.extFlushTelemetry(c.Context)
	rows := s.pluginInventory(c.Context)
	targets, err := pluginTargets(c, rows, c.Bool("all"), func(pluginRow) bool { return true })
	if err != nil {
		return err
	}
	for _, r := range targets {
		if r.Native {
			return cli.Errorf("'%s' is a built-in plugin and cannot be deleted; deactivate it instead", r.Slug)
		}
		if r.Active && !c.Bool("deactivate") {
			return cli.Errorf("the '%s' plugin is active; deactivate it first or pass --deactivate", r.Slug)
		}
	}
	if len(targets) == 0 {
		c.Warning("no plugins to delete.")
		return nil
	}
	if err := c.Confirm("Permanently delete %d plugin(s) and their data?", len(targets)); err != nil {
		return err
	}
	u, err := e.Actor(c)
	if err != nil {
		return err
	}
	release, err := s.extStartRuntime(c.Context) // the uninstall hook
	if err != nil {
		return err
	}
	defer release()
	if err := s.cliLoadForHooks(c.Context, targets); err != nil {
		return err
	}
	done, failed := 0, 0
	for _, r := range targets {
		if r.Active {
			if err := s.cliPluginAction(c.Context, u, "deactivate", r.Slug, nil); err != nil {
				c.Warning("plugin '%s' could not be deactivated: %v", r.Slug, err)
				failed++
				continue
			}
			c.Log("Deactivated '%s'.", r.Slug)
		}
		if err := s.cliPluginAction(c.Context, u, "delete", r.Slug, nil); err != nil {
			c.Warning("plugin '%s' could not be deleted: %v", r.Slug, err)
			failed++
			continue
		}
		c.Success("Deleted '%s' plugin.", r.Slug)
		done++
	}
	return pluginBatchResult("deleted", done, failed)
}

// pluginInstallStatus is the installed plugin's status ("" when it is not installed).
func (s *Server) pluginInstallStatus(ctx context.Context, slug string) string {
	var status string
	if plugins.ValidSlug(slug) {
		_ = s.store.Pool.QueryRow(ctx, `SELECT status FROM plugins WHERE slug = $1`, slug).Scan(&status)
	}
	return status
}

// marketPackage is the market directory's package of slug (PURSAE_MARKET_DIR …), or "".
func (s *Server) marketPackage(slug string) string {
	for _, path := range s.plugins.market {
		if pkg, err := plugins.LoadPackageFile(path, nil, false); err == nil && pkg.Manifest.Slug == slug {
			return path
		}
	}
	return ""
}

func (e *cliEnv) pluginInstall(c *cli.Ctx) error {
	s, err := e.extServer(c.Context)
	if err != nil {
		return err
	}
	defer s.extFlushTelemetry(c.Context)
	if err := os.MkdirAll(s.plugins.dir, 0o755); err != nil {
		return cli.Errorf("plugin directory %s: %v", s.plugins.dir, err)
	}
	type job struct {
		arg, slug string
		data      []byte // a file; nil: a slug
	}
	var jobs []job
	for _, a := range c.Args {
		if isPackagePath(a, extRegularFile) {
			data, err := readPackageArg(c, a, pluginMaxPackage)
			if err != nil {
				return err
			}
			j := job{arg: a, data: data}
			if pkg, err := plugins.LoadPackage(bytes.NewReader(data), int64(len(data)), nil, false); err == nil {
				j.slug = pkg.Manifest.Slug
			}
			jobs = append(jobs, j)
			continue
		}
		if !plugins.ValidSlug(a) {
			return cli.Errorf("'%s' is neither a package file nor a plugin slug", a)
		}
		jobs = append(jobs, job{arg: a, slug: a})
	}
	// A new version of an active plugin is loaded right away (installAndReload): that needs the runtime.
	for _, j := range jobs {
		if s.pluginInstallStatus(c.Context, j.slug) == "active" {
			release, err := s.extStartRuntime(c.Context)
			if err != nil {
				return err
			}
			defer release()
			break
		}
	}
	var installed []string
	failed := 0
	for _, j := range jobs {
		var m *plugins.Manifest
		var updated bool
		var err error
		s.extKeepCache(func() {
			switch {
			case j.data != nil:
				m, updated, err = s.installAndReload(c.Context, j.data, pluginSourceUpload, "", c.Bool("force"))
			case s.hubBase() != "":
				m, updated, err = s.hubInstallPlugin(c.Context, j.slug)
				if err != nil {
					if path := s.marketPackage(j.slug); path != "" {
						data, rerr := os.ReadFile(path)
						if rerr == nil {
							m, updated, err = s.installAndReload(c.Context, data, pluginSourceUpload, j.slug, c.Bool("force"))
						}
					}
				}
			default:
				path := s.marketPackage(j.slug)
				if path == "" {
					err = errors.New("no Pursae Manager is configured and the market directory has no such plugin; install a zip file instead")
					return
				}
				var data []byte
				if data, err = os.ReadFile(path); err == nil { //nolint:gosec // G703: the market directory is operator configuration
					m, updated, err = s.installAndReload(c.Context, data, pluginSourceUpload, j.slug, c.Bool("force"))
				}
			}
		})
		if err != nil {
			var rep *pluginReplaceError
			if errors.As(err, &rep) {
				if j.data != nil || s.hubBase() == "" {
					c.Warning("%s: %v; pass --force to replace the installed plugin anyway", j.arg, err)
				} else {
					c.Warning("%s: %v", j.arg, err)
				}
			} else {
				c.Warning("%s: installation failed: %v", j.arg, err)
			}
			if m == nil {
				failed++
				continue
			}
		}
		if updated {
			c.Success("Updated %s to %s.", m.Slug, m.Version)
		} else {
			c.Success("Installed %s %s.", m.Slug, m.Version)
		}
		installed = append(installed, m.Slug)
	}
	if c.Bool("activate") && len(installed) > 0 {
		rows := s.pluginInventory(c.Context)
		var targets []pluginRow
		for _, slug := range installed {
			if r, ok := findPluginRow(rows, slug); ok {
				targets = append(targets, r)
			}
		}
		if err := e.activatePlugins(c, s, targets); err != nil {
			return err
		}
	} else if len(installed) > 0 {
		c.Log("Running servers pick up a new version of an active plugin within about 20 seconds.")
	}
	return pluginBatchResult("installed", len(installed), failed)
}

func (e *cliEnv) pluginUpdate(c *cli.Ctx) error {
	s, err := e.extServer(c.Context)
	if err != nil {
		return err
	}
	defer s.extFlushTelemetry(c.Context)
	all := c.Bool("all")
	if all && len(c.Args) > 0 {
		return cli.Usagef("pass plugin slugs or --all, not both")
	}
	if !all && len(c.Args) == 0 {
		return cli.Usagef("please specify one or more plugins, or use --all")
	}
	if s.hubBase() == "" {
		return cli.Errorf("Pursae Manager is off (PURSAE_HUB=off); update uploaded plugins with 'plugin install <zip>'")
	}
	ups := s.hubPluginUpdates(c.Context)
	rows := s.pluginInventory(c.Context)
	var targets []pluginRow
	if all {
		for _, r := range rows {
			if _, ok := ups[r.Slug]; ok {
				targets = append(targets, r)
			}
		}
	} else {
		for _, a := range c.Args {
			r, ok := findPluginRow(rows, a)
			if !ok {
				return pluginNotFound(rows, a)
			}
			if r.Native {
				return cli.Errorf("'%s' is a built-in plugin: it is updated with Pursae itself", r.Slug)
			}
			targets = append(targets, r)
		}
	}
	if c.Bool("dry-run") {
		var out []cli.Row
		for _, r := range targets {
			it, ok := ups[r.Slug]
			if !ok {
				continue
			}
			out = append(out, cli.Row{"name": r.Slug, "status": pluginStatus(r), "version": r.Version, "update_version": it.Version})
		}
		if len(out) == 0 && !c.Quiet && c.Format == "table" {
			c.Println("No plugin updates available.")
			return nil
		}
		return c.Print(out, cli.View{Fields: []string{"name", "status", "version", "update_version"}, ID: "name"})
	}
	if len(targets) == 0 {
		c.Success("Plugins already updated.")
		return nil
	}
	if slices.ContainsFunc(targets, func(r pluginRow) bool { return r.Active }) {
		release, err := s.extStartRuntime(c.Context)
		if err != nil {
			return err
		}
		defer release()
	}
	done, failed := 0, 0
	for _, r := range targets {
		var m *plugins.Manifest
		var err error
		s.extKeepCache(func() { m, err = s.hubUpdatePlugin(c.Context, r.Slug) })
		if err != nil {
			c.Warning("%s: %v", r.Slug, err)
			failed++
			continue
		}
		c.Success("Updated %s %s -> %s.", r.Slug, r.Version, m.Version)
		done++
	}
	if done > 0 {
		c.Log("Running servers load the new versions within about 20 seconds.")
	}
	return pluginBatchResult("updated", done, failed)
}
