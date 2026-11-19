package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wpgo/internal/auth"
	"wpgo/internal/cache"
	"wpgo/internal/cli"
	"wpgo/internal/config"
	"wpgo/internal/db"
	"wpgo/internal/plugins/pkgtool"
	"wpgo/internal/settings"
	"wpgo/internal/store"
	"wpgo/web"
)

// The WP-CLI style command line: `pursae <noun> <verb> [args] [--flags]`. Like wp-cli loading
// WordPress, it works on the site's database (DATABASE_URL) and files (WPGO_UPLOADS_DIR …) directly,
// so it must run with the same environment as `serve` (in Docker: docker exec into the container).
// What it writes reaches a running server live: settings through the settings NOTIFY trigger
// (settings.Store.Watch), page cache purges through the wpgo_cache_purge channel
// (WatchCachePurges), Wasm plugin rows through the 10 s plugin sync loop. Process-local side effects
// (configureCache, theme registry, language catalogs, trusted keys …) need a server restart.
//
// Commands live in package server (files cli_<group>.go) so they can reuse the unexported helpers
// the admin screens and REST use. Each group registers through one function listed in cliGroups.

// cliGroups are the command groups, in registration order.
var cliGroups = []func(r *cli.Registry, e *cliEnv){
	registerCoreCLI,      // cli.go: serve, migrate … (help entries), plugin new/build/keygen/pack/verify
	registerContentCLI,   // cli_content.go: post, post meta/term/revision, post-type, taxonomy, term, comment
	registerMediaCLI,     // cli_media.go: media, menu, import
	registerUserCLI,      // cli_user.go: user, user meta/session/application-password, role, cap
	registerOptionCLI,    // cli_option.go: option, transient, cache, rewrite, maintenance-mode, search-replace
	registerExtensionCLI, // cli_extension.go: plugin (list/activate/…), theme
	registerOpsCLI,       // cli_ops.go: cron, db, core, backup, language, mail, site
}

// CLIDescription is the text under the usage line of `pursae help`.
const CLIDescription = `Pursae (WordPress Go) command line. Commands run against the site's database
(DATABASE_URL) and files, like WP-CLI loads WordPress: run them with the same environment as the
server (in Docker: docker exec <container> pursae <command>).`

// NewCLI builds the command tree for program prog (used for usage and help text). The returned
// close function releases the database pool and drains queued work; call it before exiting.
func NewCLI(cfg config.Config, prog string, stderr io.Writer) (*cli.Registry, func()) {
	if stderr == nil {
		stderr = io.Discard
	}
	r := cli.New(prog)
	r.Description = CLIDescription
	r.AddGlobalFlag(cli.Flag{Name: "user", Usage: "Act as this user (ID, login or e-mail); default: the first administrator."})
	e := &cliEnv{cfg: cfg, prog: prog, stderr: stderr}
	for _, reg := range cliGroups {
		reg(r, e)
	}
	for _, cmd := range r.Commands() {
		if cmd.Run != nil && !cmd.Raw && !cliWorksUninstalled(cmd.Path) {
			run := cmd.Run
			cmd.Run = func(c *cli.Ctx) error {
				if err := e.RequireInstalled(c.Context); err != nil {
					return err
				}
				return run(c)
			}
		}
	}
	return r, e.close
}

// cliUninstalledOK are the commands (path prefixes) that run before the site is installed:
// installing it, the database and migrations, restoring a backup, language packs, and reading
// options. Everything else refuses through RequireInstalled, as wp-cli does: writing an option
// such as siteurl first would mark the site installed without an administrator.
var cliUninstalledOK = []string{"core", "db", "backup", "language", "option get", "option list", "option pluck"}

func cliWorksUninstalled(path string) bool {
	for _, p := range cliUninstalledOK {
		if path == p || strings.HasPrefix(path, p+" ") {
			return true
		}
	}
	return false
}

// CLI runs the command line args (without the program name) and returns the exit code.
func CLI(ctx context.Context, cfg config.Config, prog string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	r, closeEnv := NewCLI(cfg, prog, stderr)
	defer closeEnv()
	return r.Run(ctx, args, cli.IO{In: stdin, Out: stdout, Err: stderr, Interactive: isTerminal(stdin)})
}

// isTerminal reports whether r is an interactive terminal (Confirm prompts instead of refusing).
// It asks the terminal driver (cli_tty_*.go): a character-device check alone would count
// /dev/null (cron, systemd, docker exec without -t) as a terminal.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && isatty(f.Fd())
}

// --- environment ---------------------------------------------------------------------

// cliEnv is what commands use to reach the site. Everything is built lazily on first use, so
// `pursae help` and commands that need no database never connect.
//
// Cost, cheapest first:
//   - Pool / Store / Settings: one pgx pool (plus a check for pending migrations) and one
//     SELECT of the autoload settings. Prefer these.
//   - Server: a lightweight *Server (see cliServer) for the unexported helpers that need one.
//     Still cheap: no templates, routes, plugin runtime, theme packages or background loops.
type cliEnv struct {
	cfg    config.Config
	prog   string
	stderr io.Writer // warnings that are not tied to a command's Ctx

	pool *pgxpool.Pool
	st   *store.Store
	set  *settings.Store
	srv  *Server
}

// Config is the process configuration (WPGO_* environment).
func (e *cliEnv) Config() config.Config { return e.cfg }

// Pool connects to the database on first use. It warns (stderr through slog) when migrations
// are pending: the CLI never migrates by itself (`<prog> migrate` does).
func (e *cliEnv) Pool(ctx context.Context) (*pgxpool.Pool, error) {
	if e.pool != nil {
		return e.pool, nil
	}
	pool, err := db.Connect(ctx, e.cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("database: %w (check DATABASE_URL)", err)
	}
	if n, err := db.Pending(ctx, pool); err != nil {
		fmt.Fprintf(e.stderr, "Warning: could not check for pending migrations: %v\n", err)
	} else if n > 0 {
		fmt.Fprintf(e.stderr, "Warning: %d database migration(s) pending; run '%s migrate' (or start the server).\n", n, e.prog)
	}
	e.pool = pool
	return pool, nil
}

// RequireInstalled fails unless the database is migrated and the site installed (the siteurl
// setting exists, siteInstalled), with a message saying which command to run first.
func (e *cliEnv) RequireInstalled(ctx context.Context) error {
	pool, err := e.Pool(ctx)
	if err != nil {
		return err
	}
	ok, err := siteInstalled(ctx, pool)
	if db.IsUndefinedTable(err) {
		return cli.Errorf("the database is not migrated; run '%s migrate', then '%s core install'", e.prog, e.prog)
	}
	if err != nil {
		return err
	}
	if !ok {
		return cli.Errorf("Pursae is not installed; run '%s core install' (or open /wp-admin/install)", e.prog)
	}
	return nil
}

// Store returns the data access layer over Pool.
func (e *cliEnv) Store(ctx context.Context) (*store.Store, error) {
	if e.st != nil {
		return e.st, nil
	}
	pool, err := e.Pool(ctx)
	if err != nil {
		return nil, err
	}
	e.st = store.New(pool)
	return e.st, nil
}

// Settings returns the loaded options store. Set() writes reach running servers through the
// settings NOTIFY trigger; their process-local side effects do not (warn, see package comment).
func (e *cliEnv) Settings(ctx context.Context) (*settings.Store, error) {
	if e.set != nil {
		return e.set, nil
	}
	pool, err := e.Pool(ctx)
	if err != nil {
		return nil, err
	}
	st := settings.New(pool)
	if err := st.Load(ctx); err != nil {
		return nil, err
	}
	e.set = st
	return st, nil
}

// Server returns the lightweight *Server the commands share (cliServer).
func (e *cliEnv) Server(ctx context.Context) (*Server, error) {
	if e.srv != nil {
		return e.srv, nil
	}
	st, err := e.Store(ctx)
	if err != nil {
		return nil, err
	}
	set, err := e.Settings(ctx)
	if err != nil {
		return nil, err
	}
	e.srv = e.cliServer(ctx, st, set)
	return e.srv, nil
}

// cliServer builds a *Server for command-line use, the HeadlessCLI pattern with the real config.
// Compared to New it does NOT parse templates, build routes, load theme packages, start the Wasm
// plugin runtime (s.plugins == nil: plugin hooks are no-ops, like wp-cli --skip-plugins), run
// ACF-Go registration, start background loops or resume media jobs, and it never writes
// auth_secret. What it does set up:
//   - cfg, store, settings, auth (secret from WPGO_SECRET or the stored auth_secret), web overlay
//     FS, theme library dir, the media index;
//   - a purge-only page cache: every purge helper (purgePost, purgeTerm, purgeAttachment,
//     purgeTags, purgeAll, onPostSaved) ends in a synchronous pg_notify on wpgo_cache_purge that
//     running servers apply, plus a synchronous forward to the front proxy when one is set;
//   - gocdnOffloadInline, so Pursae CDN bucket copies finish before the process exits.
//
// Groups that need more (theme packages: s.loadThemePackages(); templates: newTemplates(s);
// languages: s.initLanguages(); the plugin runtime) add it themselves and document the cost.
func (e *cliEnv) cliServer(ctx context.Context, st *store.Store, set *settings.Store) *Server {
	secret := e.cfg.Secret
	if secret == "" {
		secret = set.String("auth_secret")
	}
	s := &Server{
		cfg:      e.cfg,
		store:    st,
		settings: set,
		auth:     auth.NewManager(st.Pool, secret),
		web:      overlayFS{base: web.FS(e.cfg.WebDir), dir: themeOverrideDir(e.cfg.UploadsDir)},
	}
	if e.cfg.ThemeLibraryDir != "" {
		s.themeLibrary = os.DirFS(e.cfg.ThemeLibraryDir)
	}
	if err := s.loadMediaIndex(ctx); err != nil {
		slog.Warn("media index could not be loaded", "err", err)
	}
	cc := s.cacheConfig()
	cc.Enabled, cc.DiskDir = false, "" // never serves; only turns purges into announcements
	s.cache.Store(&pageCache{c: cache.New(cc, cache.Options{OnPurge: func(p cache.Purge) {
		if err := e.announce(context.WithoutCancel(ctx), s, p); err != nil {
			slog.Warn("page cache: announcing a purge", "err", err)
		}
	}})})
	s.onPostSaved = s.purgePost
	s.onPurgeAll = func(context.Context) {
		if c := s.currentCache(); c != nil {
			c.PurgeAll()
		}
	}
	gocdnOffloadInline = true
	return s
}

// Purge tells running servers to drop pages: cache.Purge{All: true}, {Tags: …} or {URL: …}. It is
// synchronous (pg_notify on wpgo_cache_purge, then the front proxy when configured). Servers that
// are not running miss it; persisted disk cache files (<uploads>/../wpgo-cache) survive.
func (e *cliEnv) Purge(ctx context.Context, p cache.Purge) error {
	s, err := e.Server(ctx)
	if err != nil {
		return err
	}
	return e.announce(ctx, s, p)
}

// PurgeAll is Purge(cache.Purge{All: true}); running servers also reload their media index.
func (e *cliEnv) PurgeAll(ctx context.Context) error { return e.Purge(ctx, cache.Purge{All: true}) }

const cliCachePurgeTimeout = 10 * time.Second

func (e *cliEnv) announce(ctx context.Context, s *Server, p cache.Purge) error {
	ctx, cancel := context.WithTimeout(ctx, cliCachePurgeTimeout)
	defer cancel()
	if err := s.notifyCachePurge(ctx, p); err != nil {
		return err
	}
	return s.purgeCLIProxy(ctx, p)
}

func (s *Server) notifyCachePurge(ctx context.Context, p cache.Purge) error {
	_, err := s.store.Pool.Exec(ctx, `SELECT pg_notify($1, $2)`, cachePurgeChannel, cachePurgePayload(cacheOrigin(), p))
	return err
}

func (s *Server) purgeCLIProxy(ctx context.Context, p cache.Purge) error {
	endpoint := s.settings.String("wpgo_cache_purge_url")
	if !proxyPurgeConfigured(cache.ServerHint(s.settings.String("wpgo_cache_server")), endpoint) {
		return nil
	}
	prefix := "wpgo_"
	if c := s.currentCache(); c != nil {
		prefix = c.Config().TagPrefix
	}
	req, err := proxyPurgeRequest(ctx, endpoint, prefix, p)
	if err != nil || req == nil {
		return err
	}
	resp, err := proxyPurgeClient.Do(req)
	if err != nil {
		return fmt.Errorf("front proxy purge: %w", err)
	}
	resp.Body.Close()
	return nil
}

// User resolves a user reference: a numeric ID, an e-mail or a login (case-insensitive).
func (e *cliEnv) User(ctx context.Context, ref string) (*store.User, error) {
	st, err := e.Store(ctx)
	if err != nil {
		return nil, err
	}
	ref = strings.TrimSpace(ref)
	var u *store.User
	if id, perr := strconv.ParseInt(ref, 10, 64); perr == nil && id > 0 {
		u, err = st.UserByID(ctx, id)
	} else {
		u, err = st.UserByLoginOrEmail(ctx, ref)
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
		return nil, cli.Errorf("invalid user ID, e-mail or login: '%s'", ref)
	}
	return u, err
}

// Actor is the user a command acts as: the global --user, else the first administrator (lowest
// ID). The CLI has full rights (no capability checks, like wp-cli); the actor only fills author,
// editor (revisions), uploader and audit fields.
func (e *cliEnv) Actor(c *cli.Ctx) (*store.User, error) {
	if ref := c.Flag("user"); ref != "" {
		return e.User(c.Context, ref)
	}
	pool, err := e.Pool(c.Context)
	if err != nil {
		return nil, err
	}
	var id int64
	err = pool.QueryRow(c.Context, `SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id = u.id JOIN roles r ON r.id = ur.role_id
		WHERE r.slug = 'administrator' ORDER BY u.id LIMIT 1`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, cli.Errorf("the site has no administrator; pass --user=<login>")
	}
	if err != nil {
		return nil, err
	}
	return e.User(c.Context, strconv.FormatInt(id, 10))
}

// close drains the lightweight server's queues (async writes, CDN purges) and closes the pool.
func (e *cliEnv) close() {
	if e.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		e.srv.Close(ctx)
		cancel()
	}
	if e.pool != nil {
		e.pool.Close()
	}
}

// --- core commands -------------------------------------------------------------------

// pkgtoolVerbs are the `plugin` subcommands of the packaging tool (internal/plugins/pkgtool),
// with their synopses (the tool's own single-dash flags).
var pkgtoolVerbs = map[string]struct{ short, synopsis, long string }{
	"new": {"Scaffold a new Wasm plugin project (pkgtool).",
		"[<slug>] [-slug <slug>] [-dir <dir>] [-name <display name>] [-sdk <path>]",
		"Creates a plugin project in -dir (default ./<slug>): plugin.json and a main package. -sdk is the Pursae checkout that provides the wpgo module (default ../pursae)."},
	"build": {"Compile a plugin project and pack it (pkgtool).",
		"[-dir <dir>] [-key <private.key>] [-out <file.zip>]",
		"Compiles the plugin in -dir (default .) to Wasm and packs it into -out (default <slug>-<version>.zip in -dir); signed with -key, unsigned without."},
	"keygen": {"Create an Ed25519 signing key pair (pkgtool).",
		"-out <prefix> [-comment <text>]",
		"Writes <prefix>.pub and <prefix>.key (rebuilds a missing .pub from an existing .key)."},
	"pack": {"Pack and sign a plugin directory into a .zip (pkgtool).",
		"-dir <dir> -wasm <plugin.wasm> -out <file.zip> [-key <private.key>]",
		"Packs plugin.json from -dir and the compiled -wasm into -out; signed with -key, unsigned without."},
	"verify": {"Verify a plugin package's signature (pkgtool).",
		"-pub <key.pub> <package.zip>",
		"Checks the package's signature against the trusted public key."},
}

// IsPkgtoolVerb reports whether `plugin <verb>` belongs to the packaging tool.
func IsPkgtoolVerb(verb string) bool { _, ok := pkgtoolVerbs[verb]; return ok }

// registerCoreCLI documents the commands main handles itself and wires the plugin packaging tool.
func registerCoreCLI(r *cli.Registry, e *cliEnv) {
	external := []cli.Command{
		{Path: "serve", Short: "Run the web server (the default command).", Long: "Applies pending migrations, then serves the site on WPGO_ADDR."},
		{Path: "launch", Short: "Container entrypoint: run the installed binary (core updates).", Long: "Runs the binary installed in WPGO_BIN_DIR (internal/coreupdate), with the given arguments (default: serve)."},
		{Path: "migrate", Short: "Apply pending database migrations."},
		{Path: "version", Short: "Print the Pursae version."},
		{Path: "healthcheck", Short: "Container HEALTHCHECK: GET /healthz on WPGO_ADDR."},
		{Path: "mcp", Short: "Stdio MCP bridge to a site's /wp-json/pursae/v1/mcp."},
		{Path: "setup-token", Short: "Print the install URL with a new setup token."},
		{Path: "adminsec", Short: "Pursae Security recovery: disable-hide-login | reset-user <login> | unlock-all | deactivate.",
			Synopsis: "disable-hide-login | reset-user <login> | unlock-all | deactivate"},
		{Path: "headless", Short: "Pursae Headless entries: export [--type uid]... | import [--overwrite] (NDJSON).",
			Synopsis: "export [--type <uid>]... > entries.ndjson | import [--overwrite] < entries.ndjson"},
	}
	for _, c := range external {
		c.External = true
		r.Add(c)
	}
	r.Noun("plugin", "Manage plugins, and build plugin packages (new, build, keygen, pack, verify).")
	for verb, v := range pkgtoolVerbs {
		r.Add(cli.Command{
			Path:     "plugin " + verb,
			Short:    v.short,
			Long:     v.long + "\n\nPlugin packaging tool: its flags take a single dash (-out, not --out).",
			Synopsis: v.synopsis,
			Raw:      true,
			Run: func(c *cli.Ctx) error {
				return pkgtool.Run(append([]string{verb}, c.Args...))
			},
		})
	}
}
