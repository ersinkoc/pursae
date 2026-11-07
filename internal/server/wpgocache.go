package server

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/auth"
	"wpgo/internal/cache"
	"wpgo/internal/media"
	"wpgo/internal/settings"
	"wpgo/internal/store"
)

// wpgo-cache is a native plugin: a LiteSpeed Cache style full-page cache that works behind any server
// (nginx, OpenLiteSpeed, Apache or none). Pages live in the Go process, are tagged while rendering and
// purged by tag when content changes; optional headers let a front proxy cache them too.

type pageCache struct {
	c *cache.Cache
	h http.Handler // c.Middleware(inner)
}

type warmState struct {
	mu      sync.Mutex
	running bool
	last    cache.WarmupProgress
	at      time.Time
}

// cacheOptionKeys are the plugin's options, saved from its settings screens.
var cacheOptionKeys = map[string][]string{
	"wpgo-cache-settings": {"wpgo_cache_enabled", "wpgo_cache_ttl_public", "wpgo_cache_ttl_front", "wpgo_cache_ttl_feed",
		"wpgo_cache_ttl_404", "wpgo_cache_mobile", "wpgo_cache_logged_in", "wpgo_cache_exclude_uri", "wpgo_cache_drop_qs",
		"wpgo_cache_server", "wpgo_cache_disk"},
	"wpgo-cache-optimize": {"wpgo_opt_lazy", "wpgo_opt_lazy_skip", "wpgo_opt_defer", "wpgo_opt_defer_list", "wpgo_opt_minify_html",
		"wpgo_opt_preload_fonts"},
}

// cacheConfig turns the plugin options into the engine configuration.
func (s *Server) cacheConfig() cache.Config {
	cfg := cache.DefaultConfig()
	st := s.settings
	cfg.Enabled = st.Bool("wpgo_cache_enabled")
	sec := func(k string) time.Duration { return time.Duration(st.Int(k)) * time.Second }
	cfg.PublicTTL, cfg.FrontPageTTL, cfg.FeedTTL, cfg.NotFoundTTL = sec("wpgo_cache_ttl_public"), sec("wpgo_cache_ttl_front"), sec("wpgo_cache_ttl_feed"), sec("wpgo_cache_ttl_404")
	cfg.CacheMobileSeparately = st.Bool("wpgo_cache_mobile")
	cfg.CacheLoggedIn = st.Bool("wpgo_cache_logged_in")
	cfg.ExcludePaths = append(cfg.ExcludePaths, lines(st.String("wpgo_cache_exclude_uri"))...)
	cfg.ExcludePaths, cfg.ExcludeCookies = s.gooCacheExcludes(cfg.ExcludePaths, cfg.ExcludeCookies) // Goocommerce: cart, checkout, my account
	cfg.DropQueryStrings = append(cfg.DropQueryStrings, lines(st.String("wpgo_cache_drop_qs"))...)
	cfg.ServerHint = cache.ServerHint(st.String("wpgo_cache_server"))
	if st.Bool("wpgo_cache_disk") {
		cfg.DiskDir = s.cfg.UploadsDir + "/../wpgo-cache"
	}
	cfg.Optimize = cache.OptimizeConfig{
		LazyImages:         st.Bool("wpgo_opt_lazy"),
		LazySkipFirst:      st.Int("wpgo_opt_lazy_skip"),
		DeferScripts:       st.Bool("wpgo_opt_defer"),
		DeferAllowlist:     deferList(lines(st.String("wpgo_opt_defer_list"))),
		CollapseWhitespace: st.Bool("wpgo_opt_minify_html"),
	}
	if st.Bool("wpgo_opt_preload_fonts") {
		// The fonts the active theme paints above the fold, so text renders without a late swap.
		if t := s.activeTheme(); t != nil {
			for _, f := range t.PreloadFonts {
				cfg.Optimize.Preload = append(cfg.Optimize.Preload, cache.PreloadAsset{
					Href: t.URI() + "/" + f, As: "font", Type: "font/woff2", CrossOrigin: true,
					IfDocContains: t.URI() + "/"}) // not on pages without the theme (builder canvas)
			}
		}
	}
	return cfg
}

// cacheSafeDefer are the core and default-theme scripts nothing inline depends on, deferred when
// "Defer JavaScript" is on and the list is left empty.
var cacheSafeDefer = []string{"comment-reply", "wp-embed", "twentytwentyfive-navigation", "skip-link-focus-fix", "keyboard-image-navigation"}

func deferList(v []string) []string {
	if len(v) == 0 {
		return cacheSafeDefer
	}
	return v
}

func lines(v string) []string {
	var out []string
	for _, l := range strings.Split(v, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// configureCache (re)builds the page cache from the current options. Entries are dropped, like
// LiteSpeed Cache purging everything when its settings change.
func (s *Server) configureCache() {
	if s.sandbox != nil { // a theme-preview sandbox is served without the page cache
		return
	}
	// A rebuild at run time drops every page, also the copies on disk: the new cache would
	// otherwise load them back (only the start-up keeps the persisted pages).
	if old := s.currentCache(); old != nil {
		old.PurgeAll()
	}
	c := cache.New(s.cacheConfig(), cache.Options{LoginCookie: auth.CookieName, Logger: slog.Default()})
	s.cache.Store(&pageCache{c: c, h: c.Middleware(s.appHandler())})
}

func (s *Server) currentCache() *cache.Cache {
	if pc := s.cache.Load(); pc != nil {
		return pc.c
	}
	return nil
}

// Tagging ----------------------------------------------------------------------

// cacheTags labels a public response for tag purges. Every theme page shows the sidebar widgets
// (recent posts, comments, archives, categories), so pages also carry "widgets".
func (s *Server) cacheTags(c *gin.Context, q *siteQuery) {
	tags := []string{"widgets"}
	switch q.Kind {
	case "home", "front-page":
		tags = append(tags, "home", "archive:post")
	case "single", "page", "attachment":
		if q.Post != nil {
			tags = append(tags, "post:"+itoa64(q.Post.ID), "author:"+itoa64(q.Post.AuthorID))
			if terms, err := s.store.TermsForPosts(c, []int64{q.Post.ID}, ""); err == nil {
				for _, t := range terms[q.Post.ID] {
					tags = append(tags, "term:"+itoa64(t.ID))
				}
			}
			if q.Kind == "page" && q.Post.ID == int64(s.settings.Int("page_on_front")) {
				tags = append(tags, "home")
			}
		}
	case "category", "tag":
		if q.Term != nil {
			tags = append(tags, "term:"+itoa64(q.Term.ID))
		}
	case "author":
		if q.Author != nil {
			tags = append(tags, "author:"+itoa64(q.Author.ID))
		}
	case "date":
		d := fmt.Sprintf("date:%04d", q.Year)
		if q.Month > 0 {
			d += fmt.Sprintf("-%02d", q.Month)
			if q.Day > 0 {
				d += fmt.Sprintf("-%02d", q.Day)
			}
		}
		tags = append(tags, d, "archive:post")
	case "post-type-archive":
		tags = append(tags, "archive:"+q.PostType)
	case "search":
		tags = append(tags, "search")
	case "404":
		tags = append(tags, "404")
	}
	if q.Feed != "" {
		tags = []string{"feed"}
		if q.Post != nil {
			tags = append(tags, "post:"+itoa64(q.Post.ID))
		}
	}
	if q.Kind == "front-page" || q.Kind == "home" {
		c.Header(cache.HeaderTTL, strconv.Itoa(s.settings.Int("wpgo_cache_ttl_front")))
	}
	tags = append(tags, s.gooCacheTags(c, q)...) // Goocommerce: products and the shop (goo_front.go)
	tags = append(tags, s.wbCacheTags(c, q)...)  // Pursae Web Builder pages (webbuilder_hooks.go)
	c.Header(cache.HeaderTags, strings.Join(tags, ","))
}

// Purging ----------------------------------------------------------------------

// purgePost drops the pages a post change can affect: the post, every listing it appears on and,
// when it is (or was) public, the sidebar widgets that list recent posts.
func (s *Server) purgePost(ctx context.Context, p *store.Post) {
	c := s.currentCache()
	if c == nil || p == nil {
		return
	}
	tags := []string{"post:" + itoa64(p.ID), "author:" + itoa64(p.AuthorID), "feed", "home", "404", "search", "archive:" + p.Type}
	if p.PublishedAt != nil {
		t := p.PublishedAt.In(s.loc())
		tags = append(tags, t.Format("date:2006"), t.Format("date:2006-01"), t.Format("date:2006-01-02"))
	}
	if terms, err := s.store.TermsForPosts(ctx, []int64{p.ID}, ""); err == nil {
		for _, t := range terms[p.ID] {
			tags = append(tags, "term:"+itoa64(t.ID))
		}
	}
	if p.Status != "draft" && p.Status != "auto-draft" && p.Status != "pending" {
		tags = append(tags, "widgets")
	}
	c.PurgeTags(tags...)
}

func (s *Server) purgeTags(tags ...string) {
	if c := s.currentCache(); c != nil {
		c.PurgeTags(tags...)
	}
}

// Admin screens -------------------------------------------------------------------

type cacheScreen struct {
	Tab        string
	Stats      cache.Stats
	HitPct     string
	Size       string
	Enabled    bool
	Nonce      string
	PurgeNonce string
	Warm       cache.WarmupProgress
	Warming    bool
	WarmAt     string
	Server     string
	ServerOpts []choice
	TopTags    []tagCount
}

type tagCount struct {
	Tag   string
	Count int
}

// pluginAdmin serves /wp-admin/admin?page=..., the screens registered by native plugins.
func (s *Server) pluginAdmin(c *gin.Context) {
	page := c.Query("page")
	if s.acfgoAdminPage(c) { // ACF-Go options pages
		return
	}
	if s.adminsecAdmin(c, page) { // AdminSecGo: admin?page=adminsecgo… (adminsec_admin.go)
		return
	}
	if page == bmgrSlug { // Backup Manager (backup_admin.go)
		s.bmgrAdmin(c)
		return
	}
	if page == maintSlug { // Maintenance-Go (maintenance_admin.go)
		s.maintAdmin(c)
		return
	}
	if strings.HasPrefix(page, msSlug) { // WPGO Mail SMTP
		s.mailSMTPAdmin(c, page)
		return
	}
	if s.gontactAdmin(c) { // Gontact Form: admin?page=wpcf7… (gontact_admin.go)
		return
	}
	if s.golylangAdmin(c, page) { // Golylang: Languages screens (mlang, mlang_strings, mlang_settings)
		return
	}
	// Any other page is not registered: admin.php answers that with a 403 die, never a blank screen.
	if _, ok := cacheScreenTitles[page]; !ok {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	u := currentUser(c)
	// An inactive plugin's screens are gone, as in WordPress (the menu hides them too).
	if !u.Can("manage_options", nil) || !s.settings.Bool("wpgo_cache_enabled") {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	if c.Request.Method == http.MethodPost || c.Query("action") != "" {
		s.cacheAction(c, page)
		return
	}
	d := cacheScreen{Tab: page, Enabled: s.settings.Bool("wpgo_cache_enabled"), Nonce: s.nonce(c, page+"-options"),
		PurgeNonce: s.nonce(c, "wpgo-cache-purge"), Server: s.settings.String("wpgo_cache_server")}
	if cc := s.currentCache(); cc != nil {
		d.Stats = cc.Stats()
		d.HitPct = fmt.Sprintf("%.1f%%", d.Stats.HitRatio*100)
		d.Size = media.HumanSize(d.Stats.Bytes)
		for t, n := range d.Stats.Tags {
			if strings.HasPrefix(t, "post:") || strings.HasPrefix(t, "term:") || t == "home" {
				d.TopTags = append(d.TopTags, tagCount{t, n})
			}
		}
	}
	for _, o := range [][2]string{{"auto", "Auto-detect (LiteSpeed headers only when LiteSpeed is in front)"}, {"litespeed", "LiteSpeed / OpenLiteSpeed"},
		{"nginx", "nginx (fastcgi/proxy cache)"}, {"generic", "Generic CDN or proxy (Cache-Control, Surrogate-Key)"}, {"none", "None: cache inside Pursae only"}} {
		d.ServerOpts = append(d.ServerOpts, choice{Value: o[0], Label: o[1], Checked: o[0] == d.Server})
	}
	s.warm.mu.Lock()
	d.Warm, d.Warming = s.warm.last, s.warm.running
	if !s.warm.at.IsZero() {
		d.WarmAt = s.warm.at.In(s.loc()).Format("Jan 2, 15:04")
	}
	s.warm.mu.Unlock()
	s.adminPage(c, "admin?page="+page, cacheScreenTitles[page], "wpgo-cache-page", "admin/wpgo-cache", d)
}

// cacheScreenTitles are Pursae Cache's admin?page= screens (the Pursae Cache menu).
var cacheScreenTitles = map[string]string{"wpgo-cache": "Pursae Cache", "wpgo-cache-settings": "Cache Settings",
	"wpgo-cache-optimize": "Page Optimization", "wpgo-cache-toolbox": "Toolbox"}

func (s *Server) cacheAction(c *gin.Context, page string) {
	back := "/wp-admin/admin?page=" + page
	if keys, ok := cacheOptionKeys[page]; ok && c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, page+"-options") {
			s.denyNonce(c)
			return
		}
		values := map[string]any{}
		for _, k := range keys {
			raw, present := c.GetPostForm(k)
			switch settings.Defaults[k].(type) {
			case bool:
				values[k] = present && raw != "" && raw != "0"
			case int:
				if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n >= 0 {
					values[k] = n
				}
			default:
				if present {
					values[k] = strings.TrimSpace(raw)
				}
			}
		}
		if err := s.settings.Set(c, values); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		s.configureCache()
		c.Redirect(http.StatusFound, back+"&settings-updated=true")
		return
	}
	if !s.checkNonce(c, "wpgo-cache-purge") {
		s.denyNonce(c)
		return
	}
	cc := s.currentCache()
	msg := ""
	switch c.DefaultQuery("action", c.PostForm("action")) {
	case "purge_all":
		n := 0
		if cc != nil {
			n = cc.PurgeAll()
		}
		msg = fmt.Sprintf("Purged all caches (%d pages).", n)
	case "purge_front":
		if cc != nil {
			cc.PurgeTags("home")
		}
		msg = "Purged the front page."
	case "purge_404":
		if cc != nil {
			cc.PurgeTags("404")
		}
		msg = "Purged all 404 pages."
	case "purge_url":
		u := strings.TrimSpace(c.PostForm("purge_url"))
		if pu, err := url.Parse(u); err == nil && cc != nil && u != "" {
			if pu.Host == "" {
				u = s.links(c).Home + "/" + strings.TrimLeft(pu.Path, "/")
				if pu.RawQuery != "" {
					u += "?" + pu.RawQuery
				}
			}
			msg = fmt.Sprintf("Purged %d cached copies of %s.", cc.PurgeURL(u), u)
		}
	case "warm":
		s.startWarmup(c)
		msg = "Crawler started: the cache is being warmed from the sitemap."
	}
	if r := c.Query("_wp_http_referer"); r != "" && strings.HasPrefix(r, "/") && !strings.HasPrefix(r, "//") {
		back = r
	}
	sep := "?"
	if strings.Contains(back, "?") {
		sep = "&"
	}
	c.Redirect(http.StatusFound, back+sep+"wpgo-cache-msg="+url.QueryEscape(msg))
}

// startWarmup crawls every public URL in-process (home, posts, pages, terms, authors) at a gentle rate.
func (s *Server) startWarmup(c *gin.Context) {
	cc := s.currentCache()
	s.warm.mu.Lock()
	if s.warm.running || cc == nil {
		s.warm.mu.Unlock()
		return
	}
	s.warm.running = true
	s.warm.mu.Unlock()
	lb := s.links(c)
	urls := []string{lb.HomeURL("/")}
	ctx := context.WithoutCancel(c.Request.Context())
	posts, _, _ := s.store.QueryPosts(ctx, store.PostQuery{Types: []string{"post", "page"}, Statuses: []string{"publish"}, PerPage: 1000, Page: 1})
	for _, p := range posts {
		urls = append(urls, lb.Permalink(p))
	}
	for _, tax := range []string{"category", "post_tag"} {
		terms, _, _ := s.store.QueryTerms(ctx, store.TermQuery{Taxonomy: tax, HideEmpty: true})
		for _, t := range terms {
			urls = append(urls, lb.TermLink(t))
		}
	}
	go func() {
		prog, err := cc.Warm(ctx, s.appHandler(), urls, cache.WarmupOptions{Concurrency: 2, RatePerSecond: 8})
		if err != nil {
			slog.Warn("wpgo-cache warmup", "err", err)
		}
		s.warm.mu.Lock()
		s.warm.running, s.warm.last, s.warm.at = false, prog, time.Now()
		s.warm.mu.Unlock()
	}()
}

// cacheAdminBar is the toolbar node with LiteSpeed-style purge shortcuts.
func (s *Server) cacheAdminBar(c *gin.Context, u *store.User) template.HTML {
	if !u.Can("manage_options", nil) || !s.settings.Bool("wpgo_cache_enabled") {
		return ""
	}
	n := s.nonce(c, "wpgo-cache-purge")
	ref := url.QueryEscape(c.Request.URL.RequestURI())
	item := func(id, action, label string) string {
		return `<li role="group" id="wp-admin-bar-wpgo-cache-` + id + `"><a class="ab-item" role="menuitem" href="/wp-admin/admin?page=wpgo-cache&amp;action=` + action + `&amp;_wpnonce=` + n + `&amp;_wp_http_referer=` + ref + `">` + label + `</a></li>`
	}
	return template.HTML(`<li role="group" id="wp-admin-bar-wpgo-cache" class="menupop"><a class="ab-item" role="menuitem" aria-expanded="false" href="/wp-admin/admin?page=wpgo-cache"><span class="ab-icon dashicons dashicons-performance" aria-hidden="true" style="top:2px"></span><span class="ab-label">Cache</span></a>
<div class="ab-sub-wrapper"><ul role="menu" id="wp-admin-bar-wpgo-cache-default" class="ab-submenu">` +
		item("purge-all", "purge_all", "Purge All") + item("purge-front", "purge_front", "Purge Front Page") + item("purge-404", "purge_404", "Purge All 404 Pages") +
		`<li role="group" id="wp-admin-bar-wpgo-cache-settings"><a class="ab-item" role="menuitem" href="/wp-admin/admin?page=wpgo-cache-settings">Cache Settings</a></li></ul></div></li>`)
}
