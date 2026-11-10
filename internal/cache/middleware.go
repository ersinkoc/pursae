package cache

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Response headers the application uses to talk to the cache. The first three are
// always stripped before the response reaches the client.
const (
	// HeaderTags lists the tags of a page: "home,post:12,term:3,author:1,feed".
	HeaderTags = "X-WPGo-Cache-Tags"
	// HeaderTTL overrides the TTL in seconds; "0" disables caching.
	HeaderTTL = "X-WPGo-Cache-TTL"
	// HeaderControl set to "no-cache" (or "private", "no-store") disables caching.
	HeaderControl = "X-WPGo-Cache-Control"
	// HeaderStatus is set by the cache on page responses: hit, miss or bypass.
	HeaderStatus = "X-WPGo-Cache"
)

// Options carries dependencies supplied by the core.
type Options struct {
	// LoginCookie is the name of the core's login cookie. Default "wpgo_logged_in".
	LoginCookie string
	// Logger receives warnings (disk errors). Default slog.Default().
	Logger *slog.Logger
	// Now is the clock. Default time.Now.
	Now func() time.Time
	// Store, when set, is used instead of building one from Config.
	Store *Store
	// IsLiteSpeed overrides detection of a LiteSpeed front server for HintAuto.
	IsLiteSpeed func(*http.Request) bool
	// OnPurge, when set, is called after every purge made through PurgeAll, PurgeTags and
	// PurgeURL (not Apply, PurgeExpired or the store's own methods), so the core can pass it on
	// to the other processes that serve the same site from their own memory.
	OnPurge func(Purge)
}

// Purge describes one purge: everything, a set of tags, or every cached variant of a URL.
type Purge struct {
	All  bool     `json:"a,omitempty"`
	Tags []string `json:"t,omitempty"`
	URL  string   `json:"u,omitempty"`
}

// Cache is the wpgo-cache engine: a middleware plus the purge API.
type Cache struct {
	cfg           Config
	store         *Store
	log           *slog.Logger
	now           func() time.Time
	loginCookie   string
	bypassCookies []string
	allowedQuery  map[string]bool
	detectLS      func(*http.Request) bool
	lsEnv         bool

	onPurge func(Purge)

	flight     flightGroup
	flightWait time.Duration // how long a request waits for a concurrent render of its page
}

// New builds a cache. If the disk directory cannot be used, persistence is disabled
// and a warning is logged; the in-memory cache still works.
func New(cfg Config, opts Options) *Cache {
	cfg = cfg.normalized()
	c := &Cache{
		cfg:         cfg,
		log:         opts.Logger,
		now:         opts.Now,
		loginCookie: opts.LoginCookie,
		detectLS:    opts.IsLiteSpeed,
		onPurge:     opts.OnPurge,
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.loginCookie == "" {
		c.loginCookie = DefaultLoginCookie
	}
	c.bypassCookies = append([]string(nil), cfg.BypassCookies...)
	c.allowedQuery = make(map[string]bool, len(cfg.CacheQueryStrings))
	for _, q := range cfg.CacheQueryStrings {
		c.allowedQuery[q] = true
	}
	c.lsEnv = os.Getenv("X_LSCACHE") != "" || os.Getenv("LSCACHE") != ""
	c.flightWait = defaultFlightWait

	c.store = opts.Store
	if c.store == nil {
		so := StoreOptions{MaxEntries: cfg.MaxEntries, MaxBytes: cfg.MaxBytes, DiskDir: cfg.DiskDir, Now: c.now, Logger: c.log}
		s, err := NewStore(so)
		if err != nil {
			c.log.Warn("wpgo-cache: disk persistence disabled", "dir", cfg.DiskDir, "err", err)
			so.DiskDir = ""
			s, _ = NewStore(so)
		}
		c.store = s
	}
	return c
}

// Config returns the effective configuration.
func (c *Cache) Config() Config { return c.cfg }

// Store returns the underlying store.
func (c *Cache) Store() *Store { return c.store }

// PurgeAll empties the cache.
func (c *Cache) PurgeAll() int { return c.purge(Purge{All: true}) }

// PurgeTags removes every page carrying any of tags.
func (c *Cache) PurgeTags(tags ...string) int {
	if len(tags) == 0 {
		return 0
	}
	return c.purge(Purge{Tags: tags})
}

// PurgeURL removes every cached variant of a URL's path.
func (c *Cache) PurgeURL(u string) int { return c.purge(Purge{URL: u}) }

func (c *Cache) purge(p Purge) int {
	n := c.Apply(p)
	if c.onPurge != nil {
		c.onPurge(p)
	}
	return n
}

// Apply runs a purge on this cache only, without calling [Options.OnPurge]: the core uses it for
// purges another process announced.
func (c *Cache) Apply(p Purge) int {
	switch {
	case p.All:
		return c.store.PurgeAll()
	case p.URL != "":
		return c.store.PurgeURL(p.URL)
	case len(p.Tags) > 0:
		return c.store.PurgeTags(p.Tags...)
	}
	return 0
}

// PurgeExpired removes expired entries.
func (c *Cache) PurgeExpired() int { return c.store.PurgeExpired() }

// Stats returns store statistics.
func (c *Cache) Stats() Stats { return c.store.Stats() }

// RunJanitor calls PurgeExpired every interval until ctx is done.
func (c *Cache) RunJanitor(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.PurgeExpired()
		}
	}
}

// Middleware wraps the public-site handler. Cacheable GET/HEAD requests are served
// from the cache when possible; misses are rendered by next, captured, stored and
// served. Everything else streams through untouched apart from the removal of the
// internal X-WPGo-Cache-* headers.
func (c *Cache) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = WithDegradedFlag(r)
		d := c.Classify(r)
		if d.Key == "" {
			pw := &passWriter{ResponseWriter: w, c: c, r: r}
			next.ServeHTTP(pw, r)
			pw.finish()
			return
		}
		if e, ok := c.store.Get(d.Key); ok && cacheableVary(e.Header) {
			c.serve(w, r, e, "hit", d.Private)
			return
		}
		var release func()
		if r.Method == http.MethodGet && !d.Private {
			// Concurrent misses of one page render it once: the others wait for that render
			// and are then served from the cache with their own conditional and encoding
			// negotiation. When it was not stored (not cacheable, purged meanwhile) or takes
			// too long, they render the page themselves.
			var wait <-chan struct{}
			if release, wait = c.flight.join(d.Key); wait != nil {
				t := time.NewTimer(c.flightWait)
				select {
				case <-wait:
				case <-t.C:
				case <-r.Context().Done():
					t.Stop()
					return
				}
				t.Stop()
				if e, ok := c.store.Get(d.Key); ok && cacheableVary(e.Header) {
					c.serve(w, r, e, "hit", d.Private)
					return
				}
			} else {
				defer release()
			}
		}
		cw := newCaptureWriter(w, c, r, d)
		cw.release = release
		next.ServeHTTP(cw, r)
		cw.finish()
	})
}

// defaultFlightWait bounds how long a request waits for another request's render of the same page.
const defaultFlightWait = 10 * time.Second

// flightGroup tracks the page renders in progress by cache key.
type flightGroup struct {
	mu sync.Mutex
	m  map[string]chan struct{}
}

// join makes the caller the render of key (release != nil, to be called once the page is stored
// or abandoned) or returns the channel closed when the render in progress ends (wait != nil).
func (f *flightGroup) join(key string) (release func(), wait <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ch, ok := f.m[key]; ok {
		return nil, ch
	}
	if f.m == nil {
		f.m = make(map[string]chan struct{})
	}
	ch := make(chan struct{})
	f.m[key] = ch
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			delete(f.m, key)
			f.mu.Unlock()
			close(ch)
		})
	}, nil
}

// serve writes a cached (or just captured) entry, negotiating gzip and ETag.
func (c *Cache) serve(w http.ResponseWriter, r *http.Request, e *Entry, status string, private bool) {
	h := w.Header()
	for _, k := range unstoredHeaders {
		h.Del(k)
	}
	for k, vs := range e.Header {
		if k == requestIDHeader { // entries stored before it was filtered: keep the live request's ID
			continue
		}
		h[k] = append([]string(nil), vs...)
	}
	now := c.now()
	age := now.Sub(e.Created)
	if age < 0 {
		age = 0
	}
	h.Set(HeaderStatus, status)
	h.Set("Age", strconv.Itoa(int(age/time.Second)))
	if private {
		h.Set("Cache-Control", "private, no-cache")
		c.writeNoCacheHints(h, r)
	} else {
		c.writePublicHints(h, r, e.Expires.Sub(now), e.Expires.Sub(e.Created), e.Tags)
	}

	body, etag := e.Body, e.ETag
	if e.Gzip != nil {
		addVary(h, "Accept-Encoding")
		if acceptsGzip(r.Header.Get("Accept-Encoding")) {
			body = e.Gzip
			etag = strings.TrimSuffix(e.ETag, `"`) + `-gz"`
			h.Set("Content-Encoding", "gzip")
		}
	}
	if e.Status == http.StatusOK {
		h.Set("ETag", etag)
		if inm := r.Header.Get("If-None-Match"); inm != "" && etagMatch(inm, e.ETag) {
			h.Del("Content-Type")
			h.Del("Content-Encoding")
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(e.Status)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}

// etagMatch implements weak comparison for If-None-Match against the entry's base
// ETag; the gzip variant ("…-gz") matches too.
func etagMatch(header, etag string) bool {
	base := strings.Trim(etag, `"`)
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" {
			return true
		}
		part = strings.TrimPrefix(part, "W/")
		part = strings.Trim(part, `"`)
		if part == base || part == base+"-gz" {
			return true
		}
	}
	return false
}

// acceptsGzip parses Accept-Encoding, honoring q=0.
func acceptsGzip(v string) bool {
	star := false
	for _, part := range strings.Split(v, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.ToLower(strings.TrimSpace(name))
		zero := false
		for _, p := range strings.Split(params, ";") {
			k, val, ok := strings.Cut(strings.TrimSpace(p), "=")
			if ok && strings.EqualFold(k, "q") {
				if q, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil && q == 0 {
					zero = true
				}
			}
		}
		switch name {
		case "gzip", "x-gzip":
			return !zero
		case "*":
			star = !zero
		}
	}
	return star
}

func addVary(h http.Header, field string) {
	for _, v := range h.Values("Vary") {
		if hasToken(v, field) || strings.TrimSpace(v) == "*" {
			return
		}
	}
	h.Add("Vary", field)
}

// stripInternal removes the application-to-cache headers.
func stripInternal(h http.Header) {
	h.Del(HeaderTags)
	h.Del(HeaderTTL)
	h.Del(HeaderControl)
}
