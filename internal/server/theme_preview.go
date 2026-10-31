package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wpgo/internal/auth"
	"wpgo/internal/config"
	"wpgo/internal/db"
	"wpgo/internal/plugins"
	"wpgo/internal/store"
	"wpgo/web"
)

// Theme previews with demo content (docs/theme-package.md, "Live Preview"). Add Themes › Library ›
// Live Preview (and Preview with demo content of an installed theme that ships a demo) shows the theme
// as it looks with its demo content imported, without installing anything on the site.
//
// Each previewed theme gets a sandbox: a second, complete site in its own PostgreSQL schema of the
// same database (wpgo_preview_<slug>, selected through the pool's search_path and migrated with the
// normal migrations), with its own uploads and themes directory under <data>/preview/<slug>/. It is
// installed like a fresh site and the theme is installed, activated and given its demo content by
// the same code as on the site (installThemePackage, switchTheme, importThemeDemo). A sandbox is
// built on first preview in the background (the iframe shows "Preparing preview…" meanwhile), kept
// for later previews, rebuilt when the theme's files or version (or the site's address and permalink
// settings) change, and at most previewCap of them are kept (least recently used go first);
// Add Themes › Library › Clear theme previews drops them all.
//
// A sandbox is a second *Server: it registers its package themes in its own registry (themeFor), never
// writes the process-wide registries, and runs without page cache, plugin runtime, scheduler, mail or
// Hub. The site serves it under /wp-admin/theme-preview/<slug>/ to administrators only: every request
// reaches it as an anonymous visitor (no cookies in, none out), only GET and HEAD, and never its
// wp-admin, login or comment posting. The sandbox believes it lives at the site's own address; the
// prefix is added to its responses (links, assets, uploads, redirects) on the way out.

const (
	previewCap        = 8                         // sandboxes kept
	previewBuildLimit = 3 * time.Minute           // one build
	previewFormat     = "1"                       // bump to rebuild every sandbox after a change to how they are built
	previewPrefix     = "/wp-admin/theme-preview" // + "/<slug>/…"
	previewSchemaBase = "wpgo_preview_"
	previewKeyOption  = "wpgo_preview_key"
)

// sandboxInfo is the part of a sandbox Server that differs from a site.
type sandboxInfo struct {
	themesDir string
	mu        sync.RWMutex
	themes    map[string]*Theme // its package themes (themeFor)
}

// startSandbox finishes newServer for a sandbox.
func (s *Server) startSandbox(ctx context.Context) (*Server, error) {
	s.web = overlayFS{base: web.FS(s.cfg.WebDir), dir: s.sandbox.themesDir}
	if s.sandbox.themes == nil {
		s.sandbox.themes = map[string]*Theme{}
	}
	s.loadThemePackages() // the sandbox's own packages (a sandbox kept from before a restart)
	tmpl, err := newTemplates(s)
	if err != nil {
		return nil, err
	}
	s.tmpl = tmpl
	s.plugins = &pluginSystem{hooks: plugins.NewRegistry(), initErr: errors.New("plugins do not run in a theme preview")}
	s.engine = s.routes()
	s.onPostSaved = func(context.Context, *store.Post) {}
	s.onPurgeAll = func(context.Context) {}
	return s, nil
}

// Sources ----------------------------------------------------------------------------------

// previewSource is what a sandbox is built from: a library theme or an installed package.
type previewSource struct {
	Manifest *themeManifest
	Library  bool
	fsys     fs.FS
	dir      string // the theme's directory inside fsys
}

func (src *previewSource) zip() ([]byte, error) {
	return zipThemeDir(src.fsys, src.dir)
}

// previewSourceFor finds a theme to preview: kind "installed" is an installed package with demo
// content, anything else the library theme (falling back to an installed package with a demo); the
// library is only looked at with library set (the user may preview library themes). A Manager
// library theme is downloaded into the library cache first (theme_library.go); the error says why
// that failed.
func (s *Server) previewSourceFor(ctx context.Context, slug, kind string, library bool) (*previewSource, error) {
	installed := func() *previewSource {
		t := s.installedTheme(slug)
		if t == nil || t.Package == nil || t.Package.Manifest.Demo == "" {
			return nil
		}
		return &previewSource{Manifest: t.Package.Manifest, fsys: os.DirFS(filepath.Dir(t.Package.Dir)), dir: filepath.Base(t.Package.Dir)}
	}
	if kind == "installed" {
		return installed(), nil
	}
	if lt := s.libraryTheme(ctx, slug); library && lt != nil {
		fsys, dir, err := s.libraryFiles(ctx, lt)
		if err != nil {
			return nil, err
		}
		return &previewSource{Manifest: lt.Manifest, Library: true, fsys: fsys, dir: dir}, nil
	}
	return installed(), nil
}

// Manager ----------------------------------------------------------------------------------

type previewManager struct {
	s     *Server
	mu    sync.Mutex
	boxes map[string]*previewBox // by slug
	sem   chan struct{}          // concurrent builds
}

// previewBox is one sandbox.
type previewBox struct {
	slug, schema, dir string
	key               string
	library           bool
	done              chan struct{} // closed when the build has finished
	cancel            context.CancelFunc

	mu      sync.RWMutex // read-held while a request is served; Lock to close
	srv     *Server
	pool    *pgxpool.Pool
	handler http.Handler
	err     error
	closed  bool
}

func newPreviewManager(s *Server) *previewManager {
	return &previewManager{s: s, boxes: map[string]*previewBox{}, sem: make(chan struct{}, 2)}
}

// previewSchema is the schema of a slug's sandbox (slugs are [a-z0-9-], so the mapping is one to one;
// long slugs are cut to PostgreSQL's 63 bytes with a hash).
func previewSchema(slug string) string {
	name := previewSchemaBase + strings.ReplaceAll(slug, "-", "_")
	if len(name) > 63 {
		sum := sha256.Sum256([]byte(slug))
		name = name[:50] + "_" + hex.EncodeToString(sum[:6])
	}
	return name
}

func (m *previewManager) root() string {
	return filepath.Join(filepath.Dir(filepath.Clean(m.s.cfg.UploadsDir)), "preview")
}

// home is the address the sandboxes live at: the site's.
func (m *previewManager) home() string {
	home := strings.TrimRight(m.s.settings.String("home"), "/")
	if home == "" {
		home = m.s.cfg.SiteURL
	}
	return home
}

// copiedOptions are the site settings a sandbox takes over, so dates and URLs look like the site's.
var previewCopiedOptions = []string{"permalink_structure", "category_base", "tag_base", "timezone_string", "gmt_offset",
	"date_format", "time_format", "start_of_week"}

// key identifies what a sandbox was built from: format, address, copied settings and the package.
func (m *previewManager) key(pkg []byte) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s\n", previewFormat, m.home())
	for _, k := range previewCopiedOptions {
		fmt.Fprintf(h, "%s=%v\n", k, m.s.settings.Get(k))
	}
	h.Write(pkg)
	return hex.EncodeToString(h.Sum(nil))
}

func (b *previewBox) state() string {
	select {
	case <-b.done:
		b.mu.RLock()
		defer b.mu.RUnlock()
		switch {
		case b.err != nil:
			return "failed"
		case b.closed:
			return "missing"
		}
		return "ready"
	default:
		return "building"
	}
}

// get returns the sandbox of a slug, if any.
func (m *previewManager) get(slug string) *previewBox {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.boxes[slug]
}

// ensure returns the sandbox of a source, starting a build when there is none, it is out of date or
// (with retry) its last build failed.
func (m *previewManager) ensure(src *previewSource, retry bool) (*previewBox, error) {
	pkg, err := src.zip()
	if err != nil {
		return nil, err
	}
	key := m.key(pkg)
	slug := src.Manifest.Slug
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.boxes[slug]
	if old != nil {
		switch old.state() {
		case "building":
			return old, nil
		case "ready":
			if old.key == key {
				return old, nil
			}
		case "failed":
			if old.key == key && !retry {
				return old, nil
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewBuildLimit)
	b := &previewBox{slug: slug, schema: previewSchema(slug), dir: filepath.Join(m.root(), slug), key: key,
		library: src.Library, done: make(chan struct{}), cancel: cancel}
	m.boxes[slug] = b
	go m.build(ctx, b, old, pkg)
	return b, nil
}

// build opens the sandbox kept from before (same key) or builds it anew.
func (m *previewManager) build(ctx context.Context, b, old *previewBox, pkg []byte) {
	defer b.cancel()
	defer close(b.done)
	if old != nil {
		old.close()
	}
	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-ctx.Done():
		b.fail(ctx.Err())
		return
	}
	start := time.Now()
	reused, err := m.open(ctx, b)
	if err == nil && !reused {
		err = m.create(ctx, b, pkg)
	}
	if err != nil {
		log.Printf("theme preview %s: %v", b.slug, err)
		b.fail(err)
		m.dropFiles(context.Background(), b.slug)
		return
	}
	if !reused {
		log.Printf("theme preview %s built in %s", b.slug, time.Since(start).Round(time.Millisecond))
	}
	m.touch(b.slug)
	m.evict(context.Background())
}

func (b *previewBox) fail(err error) {
	b.mu.Lock()
	b.err = err
	if b.pool != nil {
		b.pool.Close()
	}
	b.srv, b.pool, b.handler = nil, nil, nil
	b.mu.Unlock()
}

// close shuts a sandbox down (its schema and files stay).
func (b *previewBox) close() {
	if b.cancel != nil {
		b.cancel()
	}
	<-b.done
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pool != nil {
		b.pool.Close()
	}
	b.srv, b.pool, b.handler, b.closed = nil, nil, nil, true
}

func (m *previewManager) sandboxConfig(b *previewBox) config.Config {
	return config.Config{
		SiteURL:    m.home(),
		UploadsDir: filepath.Join(b.dir, "uploads"),
		WebDir:     m.s.cfg.WebDir,
		HubURL:     "off",
		MailDir:    filepath.Join(b.dir, "mail"),
	}
}

func (m *previewManager) newSandboxServer(ctx context.Context, b *previewBox, pool *pgxpool.Pool) (*Server, error) {
	return newServer(ctx, m.sandboxConfig(b), pool, &sandboxInfo{themesDir: filepath.Join(b.dir, "themes")})
}

// open reuses a sandbox kept from an earlier build with the same key (after a restart).
func (m *previewManager) open(ctx context.Context, b *previewBox) (bool, error) {
	var exists bool
	if err := m.s.store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, b.schema).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(b.dir, "themes", b.slug, "theme.json")); err != nil {
		return false, nil
	}
	pool, err := db.ConnectSchema(ctx, m.s.store.Pool.Config(), b.schema, 4)
	if err != nil {
		return false, err
	}
	var key string
	_ = pool.QueryRow(ctx, `SELECT value #>> '{}' FROM settings WHERE key = $1`, previewKeyOption).Scan(&key)
	if key != b.key {
		pool.Close()
		return false, nil
	}
	srv, err := m.newSandboxServer(ctx, b, pool)
	if err != nil || srv.activeTheme().Slug != b.slug {
		pool.Close()
		return false, err
	}
	b.ready(srv, pool)
	return true, nil
}

func (b *previewBox) ready(srv *Server, pool *pgxpool.Pool) {
	b.mu.Lock()
	b.srv, b.pool, b.handler = srv, pool, srv.appHandler()
	b.mu.Unlock()
}

// create builds a sandbox: schema, migrations, a fresh install, the theme and its demo content.
func (m *previewManager) create(ctx context.Context, b *previewBox, pkg []byte) error {
	if err := m.dropFiles(ctx, b.slug); err != nil {
		return err
	}
	if _, err := m.s.store.Pool.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{b.schema}.Sanitize()); err != nil {
		return err
	}
	for _, d := range []string{"uploads", "themes"} {
		if err := os.MkdirAll(filepath.Join(b.dir, d), 0o755); err != nil {
			return err
		}
	}
	pool, err := db.ConnectSchema(ctx, m.s.store.Pool.Config(), b.schema, 4)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			pool.Close()
		}
	}()
	if _, err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	srv, err := m.newSandboxServer(ctx, b, pool)
	if err != nil {
		return err
	}
	if err := srv.install(ctx, installView{SiteTitle: "Theme Preview", UserName: "admin", Password: auth.RandomToken(32),
		Email: "admin@preview.invalid", Discourage: true}); err != nil {
		return fmt.Errorf("install: %w", err)
	}
	copied := map[string]any{}
	for _, k := range previewCopiedOptions {
		if v := m.s.settings.Get(k); v != nil {
			copied[k] = v
		}
	}
	if err := srv.settings.Set(ctx, copied); err != nil {
		return err
	}
	t, err := srv.installThemePackage(ctx, pkg)
	if err != nil {
		return fmt.Errorf("install the theme: %w", err)
	}
	if err := srv.switchTheme(ctx, t.Slug); err != nil {
		return err
	}
	title := map[string]any{"blogname": t.Name}
	if t.Description != "" {
		title["blogdescription"] = t.Description
	}
	if err := srv.settings.Set(ctx, title); err != nil { // the demo's own site title wins below
		return err
	}
	if t.Package.Manifest.Demo != "" {
		var uid int64
		if err := pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&uid); err != nil {
			return err
		}
		if _, err := srv.importThemeDemo(ctx, uid, t); err != nil {
			return fmt.Errorf("demo content: %w", err)
		}
	}
	if err := srv.settings.Set(ctx, map[string]any{previewKeyOption: b.key}); err != nil {
		return err
	}
	ok = true
	b.ready(srv, pool)
	return nil
}

// dropFiles removes a slug's schema and directory (the in-memory sandbox must be closed already).
func (m *previewManager) dropFiles(ctx context.Context, slug string) error {
	if _, err := m.s.store.Pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+pgx.Identifier{previewSchema(slug)}.Sanitize()+` CASCADE`); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(m.root(), slug))
}

// touch marks a sandbox as used now (its directory's .used file: the LRU order survives restarts).
func (m *previewManager) touch(slug string) {
	f := filepath.Join(m.root(), slug, ".used")
	now := time.Now()
	if err := os.Chtimes(f, now, now); err != nil {
		_ = os.WriteFile(f, nil, 0o644)
	}
}

// previewEntry is a kept sandbox, for eviction and the count on the Library tab.
type previewEntry struct {
	slug, schema string
	used         time.Time
}

// list returns the kept sandboxes: their directories and schemas (a schema without a directory has
// no slug and counts as the oldest).
func (m *previewManager) list(ctx context.Context) []previewEntry {
	var out []previewEntry
	seen := map[string]bool{}
	entries, _ := os.ReadDir(m.root())
	for _, e := range entries {
		if !e.IsDir() || !validSlug(e.Name()) {
			continue
		}
		pe := previewEntry{slug: e.Name(), schema: previewSchema(e.Name())}
		if st, err := os.Stat(filepath.Join(m.root(), e.Name(), ".used")); err == nil {
			pe.used = st.ModTime()
		}
		seen[pe.schema] = true
		out = append(out, pe)
	}
	rows, err := m.s.store.Pool.Query(ctx, `SELECT nspname FROM pg_namespace WHERE starts_with(nspname, $1)`, previewSchemaBase)
	if err == nil {
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil && !seen[name] {
				out = append(out, previewEntry{schema: name})
			}
		}
		rows.Close()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].used.Before(out[j].used) })
	return out
}

// evict drops the least recently used sandboxes beyond previewCap (never one being built or used in
// the last minute).
func (m *previewManager) evict(ctx context.Context) {
	list := m.list(ctx)
	for i := 0; len(list)-i > previewCap && i < len(list); i++ {
		pe := list[i]
		if pe.slug == "" {
			_, _ = m.s.store.Pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+pgx.Identifier{pe.schema}.Sanitize()+` CASCADE`)
			continue
		}
		if time.Since(pe.used) < time.Minute {
			continue
		}
		m.mu.Lock()
		b := m.boxes[pe.slug]
		if b != nil && b.state() == "building" {
			m.mu.Unlock()
			continue
		}
		delete(m.boxes, pe.slug)
		m.mu.Unlock()
		if b != nil {
			b.close()
		}
		if err := m.dropFiles(ctx, pe.slug); err != nil {
			log.Printf("theme preview %s: %v", pe.slug, err)
		}
	}
}

// clear drops every sandbox.
func (m *previewManager) clear(ctx context.Context) error {
	m.mu.Lock()
	boxes := m.boxes
	m.boxes = map[string]*previewBox{}
	m.mu.Unlock()
	for _, b := range boxes {
		b.close()
	}
	for _, pe := range m.list(ctx) {
		if _, err := m.s.store.Pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+pgx.Identifier{pe.schema}.Sanitize()+` CASCADE`); err != nil {
			return err
		}
	}
	return os.RemoveAll(m.root())
}

// closePreviews shuts every sandbox down (their schemas and files stay); for tests and shutdown.
func (s *Server) closePreviews() {
	if s.previews == nil {
		return
	}
	s.previews.mu.Lock()
	boxes := s.previews.boxes
	s.previews.boxes = map[string]*previewBox{}
	s.previews.mu.Unlock()
	for _, b := range boxes {
		b.close()
	}
}

// Screens --------------------------------------------------------------------------------

// canPreview: previews are for administrators (switch_themes; install_themes for a library theme).
func canPreview(u *store.User, library bool) bool {
	return u != nil && u.Can("switch_themes", nil) && (!library || u.Can("install_themes", nil))
}

// previewURL is the Live Preview screen of a theme.
func previewURL(slug string, installed bool) string {
	u := previewPrefix + "?theme=" + url.QueryEscape(slug)
	if installed {
		u += "&source=installed"
	}
	return u
}

type previewNav struct{ URL, Name string }

// themePreviewScreen is the full-screen theme preview (WordPress's theme-install overlay): the theme's
// details and actions in the sidebar, the sandbox in an iframe. ?action=status reports the build
// (JSON, polled by the Preparing preview page); a POST with action=clear drops every sandbox.
func (s *Server) themePreviewScreen(c *gin.Context) {
	u := currentUser(c)
	if c.Request.Method == http.MethodPost {
		if c.PostForm("action") != "clear" || !canPreview(u, true) {
			c.String(http.StatusForbidden, "Sorry, you are not allowed to do that.")
			return
		}
		if !s.checkNonce(c, "clear-theme-previews") {
			s.denyNonce(c)
			return
		}
		if err := s.previews.clear(c.Request.Context()); err != nil {
			c.Redirect(http.StatusFound, "/wp-admin/theme-install?error="+url.QueryEscape("The theme previews could not be cleared: "+err.Error()))
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/theme-install?previews=cleared")
		return
	}
	slug := c.Query("theme")
	if c.Query("action") == "status" {
		if !canPreview(u, false) {
			c.JSON(http.StatusForbidden, gin.H{"state": "forbidden"})
			return
		}
		state, msg := "missing", ""
		if b := s.previews.get(slug); b != nil {
			state = b.state()
			if state == "failed" {
				msg = b.err.Error()
			}
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"state": state, "error": msg})
		return
	}
	if !canPreview(u, false) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to preview themes on this site.")
		return
	}
	installedSource := c.Query("source") == "installed"
	src, err := s.previewSourceFor(c, slug, c.Query("source"), canPreview(u, true))
	if err != nil {
		c.String(http.StatusBadGateway, "The theme could not be downloaded from Pursae Manager: "+err.Error())
		return
	}
	if src == nil || (installedSource && src.Library) {
		c.String(http.StatusNotFound, "The requested theme does not exist.")
		return
	}
	if !canPreview(u, src.Library) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to preview themes on this site.")
		return
	}
	if _, err := s.previews.ensure(src, true); err != nil {
		c.String(http.StatusInternalServerError, "The preview could not be prepared: "+err.Error())
		return
	}
	m := src.Manifest
	s.hubEvent("theme_preview", "theme", slug, m.Version) // statistics (hub_telemetry.go)
	back := "/wp-admin/theme-install"
	if !src.Library {
		back = "/wp-admin/themes?theme=" + url.QueryEscape(slug)
	} else if r := c.Query("return"); r != "" {
		back = safeRedirect(r, back)
	}
	d := map[string]any{"Name": m.Name, "Author": m.Author, "Version": m.Version, "Description": m.Description,
		"Style": m.Style, "Category": themeCategoryLabel(m.Category), "HasDemo": m.Demo != "", "Back": back,
		"Frame": previewPrefix + "/" + slug + "/", "Library": src.Library}
	if src.Library {
		d["Screenshot"] = libraryScreenshotURL(m)
		// Previous / next theme of the library.
		lib := s.libraryThemes(c)
		for i, lt := range lib {
			if lt.Manifest.Slug != slug {
				continue
			}
			if i > 0 {
				d["Prev"] = previewNav{previewURL(lib[i-1].Manifest.Slug, false), lib[i-1].Manifest.Name}
			}
			if i+1 < len(lib) {
				d["Next"] = previewNav{previewURL(lib[i+1].Manifest.Slug, false), lib[i+1].Manifest.Name}
			}
		}
	} else if t := s.installedTheme(slug); t != nil {
		d["Screenshot"] = t.ScreenshotSrc() + "?ver=" + url.QueryEscape(t.Version)
	}
	// The actions: what Add Themes or Themes would offer for this theme.
	active := s.activeTheme()
	installed := s.installedTheme(slug)
	installURL := "/wp-admin/theme-install?action=install-theme&theme=" + slug + "&_wpnonce=" + s.nonce(c, "install-theme_"+slug)
	activateURL := "/wp-admin/themes?action=activate&stylesheet=" + slug + "&_wpnonce=" + s.nonce(c, "switch-theme_"+slug)
	switch {
	case src.Library && (installed == nil || installed.Package == nil):
		if u.Can("install_themes", nil) {
			d["Primary"], d["PrimaryLabel"] = installURL, "Install"
			if m.Demo != "" && u.Can("import", nil) {
				d["DemoURL"] = installURL + "&demo=1"
			}
		}
	case src.Library && semverLess(installed.Version, m.Version):
		d["Primary"], d["PrimaryLabel"] = installURL, "Update"
	case installed != nil && installed.Slug != active.Slug:
		d["Primary"], d["PrimaryLabel"] = activateURL, "Activate"
	case installed != nil:
		d["Primary"], d["PrimaryLabel"] = "/wp-admin/customize", "Customize"
		if m.Demo != "" && u.Can("import", nil) && !s.demoImported(slug) {
			d["ImportURL"] = "/wp-admin/themes?action=import-demo&stylesheet=" + slug + "&_wpnonce=" + s.nonce(c, "import-demo_"+slug)
		}
	}
	if installed != nil && installed.Package != nil {
		d["Installed"] = true
	}
	s.adminPage(c, "themes", "Preview: "+m.Name, "theme-install-php full-overlay-active", "admin/theme-preview", d)
}

// Sandbox requests -------------------------------------------------------------------------

// themePreviewSite serves /wp-admin/theme-preview/<slug>/… from the theme's sandbox.
func (s *Server) themePreviewSite(c *gin.Context) {
	rest := strings.TrimPrefix(c.Param("rest"), "/")
	slug, sub, _ := strings.Cut(rest, "/")
	sub = "/" + sub
	u := currentUser(c)
	if !canPreview(u, false) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to preview themes on this site.")
		return
	}
	if !validSlug(slug) {
		c.Status(http.StatusNotFound)
		return
	}
	if rest == slug && c.Request.Method == http.MethodGet { // /wp-admin/theme-preview/<slug>: the home page
		c.Redirect(http.StatusMovedPermanently, previewPrefix+"/"+slug+"/")
		return
	}
	if msg := previewRefused(c.Request.Method, sub); msg != "" {
		c.Header("X-Robots-Tag", "noindex, nofollow")
		c.String(http.StatusForbidden, msg)
		return
	}
	b := s.previews.get(slug)
	if b == nil || b.state() == "missing" {
		src, err := s.previewSourceFor(c, slug, "", canPreview(u, true))
		if err != nil {
			c.String(http.StatusBadGateway, "The theme could not be downloaded from Pursae Manager: "+err.Error())
			return
		}
		if src == nil || !canPreview(u, src.Library) {
			c.Status(http.StatusNotFound)
			return
		}
		if b, err = s.previews.ensure(src, false); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
	}
	switch b.state() {
	case "building", "failed":
		s.previewPreparing(c, slug, b)
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.handler == nil {
		s.previewPreparing(c, slug, b)
		return
	}
	s.previews.touchLater(slug)
	serveSandbox(c, b.handler, b.srv.settings.String("home"), previewPrefix+"/"+slug, sub)
}

// touchLater marks a sandbox used, at most once a minute.
func (m *previewManager) touchLater(slug string) {
	f := filepath.Join(m.root(), slug, ".used")
	if st, err := os.Stat(f); err == nil && time.Since(st.ModTime()) < time.Minute {
		return
	}
	m.touch(slug)
}

// previewRefused is why a request may not reach a sandbox ("" when it may): a preview is read-only
// and has no back end.
func previewRefused(method, p string) string {
	if method != http.MethodGet && method != http.MethodHead {
		return "This is a theme preview: nothing can be changed or submitted here."
	}
	clean := path.Clean(p)
	for _, static := range []string{"/wp-admin/assets/", "/wp-admin/css/", "/wp-admin/js/", "/wp-admin/images/"} {
		if strings.HasPrefix(clean+"/", static) && clean+"/" != static {
			return ""
		}
	}
	for _, refused := range []string{"/wp-admin", "/wp-login", "/wp-comments-post", "/xmlrpc", "/wp-cron", "/wp-signup", "/wp-activate"} {
		if clean == refused || strings.HasPrefix(clean, refused+"/") || strings.HasPrefix(clean, refused+".") {
			return "This is a theme preview: the dashboard and the login are not part of it."
		}
	}
	return ""
}

// previewPreparing is the page the preview iframe shows while the sandbox is being built: it polls the
// status and reloads when the preview is ready.
func (s *Server) previewPreparing(c *gin.Context, slug string, b *previewBox) {
	msg, failed := "Preparing preview&hellip;", false
	detail := "The theme is being installed with its demo content in a private copy of the site. This takes a few seconds the first time."
	if b.state() == "failed" {
		failed = true
		msg = "The preview could not be prepared."
		detail = template.HTMLEscapeString(b.err.Error())
	}
	status := previewPrefix + "?theme=" + url.QueryEscape(slug) + "&action=status"
	page := `<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="robots" content="noindex, nofollow"><title>Preparing preview</title>
<link rel="stylesheet" href="/wp-admin/css/common.min.css"><style>html,body{height:100%;margin:0;background:#f0f0f1}
body{display:flex;align-items:center;justify-content:center;font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;color:#1d2327}
.wpgo-preparing{text-align:center;max-width:420px;padding:20px}.wpgo-preparing .spinner{float:none;visibility:visible;margin:0 auto 12px;display:block}
.wpgo-preparing p{color:#50575e}</style></head><body><div class="wpgo-preparing" role="status" aria-live="polite">`
	if !failed {
		page += `<span class="spinner is-active"></span>`
	}
	page += `<h1 style="font-size:1.3em;margin:0 0 6px">` + msg + `</h1><p>` + detail + `</p></div>`
	if !failed {
		page += `<script>(function(){var u=` + jsString(status) + `;function poll(){fetch(u,{credentials:"same-origin",cache:"no-store"}).then(function(r){return r.json()}).then(function(d){if(d.state==="ready"||d.state==="failed"||d.state==="missing"){location.reload();}else{setTimeout(poll,1000);}}).catch(function(){setTimeout(poll,2000);});}setTimeout(poll,800);})();</script>`
	}
	page += `</body></html>`
	c.Header("Cache-Control", "no-store")
	c.Header("X-Robots-Tag", "noindex, nofollow")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page))
}

func jsString(v string) string {
	b, _ := json.Marshal(v) // escapes <, > and & for a <script>
	return string(b)
}

// bufferedResponse collects a sandbox response before it is rewritten.
type bufferedResponse struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (w *bufferedResponse) Header() http.Header { return w.header }
func (w *bufferedResponse) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.body.Write(b)
}
func (w *bufferedResponse) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
}

// serveSandbox hands a request to a sandbox as an anonymous visitor at path sub and writes its response
// with every URL of the site moved under prefix.
func serveSandbox(c *gin.Context, h http.Handler, home, prefix, sub string) {
	// A fresh context: nothing of the site's request (user, language, plugin state) reaches the sandbox.
	ctx, cancel := context.WithCancel(context.Background())
	stop := context.AfterFunc(c.Request.Context(), cancel)
	defer stop()
	defer cancel()
	r := c.Request.Clone(ctx)
	r.URL.Path, r.URL.RawPath = sub, ""
	r.RequestURI = r.URL.RequestURI()
	for _, k := range []string{"Cookie", "Authorization", "Accept-Encoding", "If-None-Match", "If-Modified-Since", "X-WP-Nonce"} {
		r.Header.Del(k)
	}
	w := &bufferedResponse{header: http.Header{}}
	h.ServeHTTP(w, r)
	if w.code == 0 {
		w.code = http.StatusOK
	}

	rw := newPreviewRewriter(c.Request.Host, home, prefix)
	out := c.Writer.Header()
	for k := range out { // the site's own headers (admin CSP, …) give way to the sandbox's
		out.Del(k)
	}
	for k, vs := range w.header {
		switch http.CanonicalHeaderKey(k) {
		case "Set-Cookie", "Content-Length":
			continue
		case "Location", "Link", "Content-Location":
			for _, v := range vs {
				out.Add(k, rw.header(v))
			}
			continue
		}
		out[k] = vs
	}
	out.Set("X-Robots-Tag", "noindex, nofollow")
	body := w.body.Bytes()
	if ct := w.header.Get("Content-Type"); previewRewritable(ct) {
		body = rw.body(body, strings.HasPrefix(ct, "text/html") || strings.HasPrefix(ct, "text/css"))
		out.Set("Cache-Control", "no-cache")
	}
	c.Status(w.code)
	if c.Request.Method == http.MethodHead {
		c.Writer.WriteHeaderNow()
		return
	}
	_, _ = c.Writer.Write(body)
}

func previewRewritable(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	switch ct {
	case "text/html", "text/css", "application/json", "application/ld+json", "text/xml", "application/xml",
		"application/rss+xml", "application/atom+xml", "application/javascript", "text/javascript", "application/xhtml+xml":
		return true
	}
	return strings.HasSuffix(ct, "+json") || strings.HasSuffix(ct, "+xml")
}

// previewRewriter moves the site's URLs in a sandbox response under the preview prefix: absolute URLs
// on the site's host (also JSON-escaped and protocol-relative) and, in HTML and CSS, root-relative
// URLs in attributes and url().
type previewRewriter struct {
	prefix string
	abs    *regexp.Regexp
}

var (
	previewAttrRe   = regexp.MustCompile(`(?i)(\s(?:href|src|action|poster|formaction|data-src|data-href|data-url|data-bg|data-background)\s*=\s*["']?)/([^/])`)
	previewSrcsetRe = regexp.MustCompile(`(?i)(\s(?:srcset|data-srcset)\s*=\s*["'])([^"']*)`)
	previewSrcItem  = regexp.MustCompile(`(^|,\s*)/([^/])`)
	previewURLRe    = regexp.MustCompile(`(?i)(url\(\s*["']?)/([^/])`)
	previewImportRe = regexp.MustCompile(`(?i)(@import\s+["'])/([^/])`)
)

func newPreviewRewriter(host, home, prefix string) *previewRewriter {
	hosts := map[string]bool{host: true}
	if hu, err := url.Parse(home); err == nil && hu.Host != "" {
		hosts[hu.Host] = true
	}
	var alts []string
	for h := range hosts {
		if h != "" {
			alts = append(alts, regexp.QuoteMeta(h))
		}
	}
	sort.Strings(alts)
	re := regexp.MustCompile(`(?i)((?:https?:)?(?://|\\/\\/))(` + strings.Join(alts, "|") + `)(\\/|/|\\"|["'\s<>?#),;&]|$)`)
	return &previewRewriter{prefix: prefix, abs: re}
}

// absolute rewrites the absolute URLs on the site's hosts.
func (rw *previewRewriter) absolute(s string) string {
	escaped := strings.ReplaceAll(rw.prefix, "/", `\/`)
	return rw.abs.ReplaceAllStringFunc(s, func(m string) string {
		sm := rw.abs.FindStringSubmatch(m)
		p := rw.prefix
		if strings.Contains(sm[1], `\/`) {
			p = escaped
		}
		tail := sm[3]
		if tail == "" || (tail != "/" && tail != `\/`) { // the bare origin: the home page
			if strings.Contains(sm[1], `\/`) {
				return sm[1] + sm[2] + p + `\/` + tail
			}
			return sm[1] + sm[2] + p + "/" + tail
		}
		return sm[1] + sm[2] + p + tail
	})
}

func (rw *previewRewriter) body(b []byte, markup bool) []byte {
	s := rw.absolute(string(b))
	if markup {
		repl := "${1}" + rw.prefix + "/${2}"
		s = previewAttrRe.ReplaceAllString(s, repl)
		s = previewURLRe.ReplaceAllString(s, repl)
		s = previewImportRe.ReplaceAllString(s, repl)
		s = previewSrcsetRe.ReplaceAllStringFunc(s, func(m string) string {
			sm := previewSrcsetRe.FindStringSubmatch(m)
			return sm[1] + previewSrcItem.ReplaceAllString(sm[2], "${1}"+rw.prefix+"/${2}")
		})
	}
	return []byte(s)
}

// header rewrites a Location-like header: absolute URLs and root-relative paths.
func (rw *previewRewriter) header(v string) string {
	if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
		return rw.prefix + v
	}
	return rw.absolute(v)
}

// previewCount is the number of kept sandboxes (the Library tab's Clear theme previews).
func (s *Server) previewCount(ctx context.Context) int {
	if s.previews == nil {
		return 0
	}
	return len(s.previews.list(ctx))
}
