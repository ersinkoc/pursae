package server

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

// Pursae Multi Language: the strings a site shows (theme templates, plugins, text found on its
// pages) are recorded in pll_string_sources for the Translations screen. Recording never writes
// while the page renders: pllCaptureString only queues the string in memory, deduplicated
// process-wide, and a batch is written shortly after on the bounded background writer
// (background.go). Server.Close writes what is still queued.
//
// Limits (abuse: pages echo request data, ?s= and 404 paths): at most pllCapturePerRequest new
// strings per request, pllCapturePerMinute per server, strings up to pllCaptureMaxRunes characters,
// and a bounded queue. Callers decide who may capture page text (never anonymous visitors).

const (
	pllCapturePerRequest = 500
	pllCapturePerMinute  = 5000
	pllCaptureMaxRunes   = 2000
	pllCaptureMaxBytes   = 2400 // keeps the primary key under PostgreSQL's btree row limit
	pllCaptureMaxMeta    = 200  // context, domain and source
	pllCaptureMaxURL     = 500
	pllCaptureQueueMax   = 10000
	pllCaptureBatch      = 500
	pllCaptureDelay      = 2 * time.Second
	pllCaptureRefresh    = 6 * time.Hour // a known string's last_seen is refreshed at most this often
	pllCaptureSeenMax    = 200000        // dedup entries kept before the set starts over
	// pllCaptureAnonPerHour is how many new theme or plugin strings (gettext, theme mods, the
	// plugins' translate import) one source may record per hour from requests of visitors who
	// cannot manage_options: a template or plugin that passes request data (a search term, a
	// name) through __ / wpgo.T cannot fill the table from anonymous traffic.
	pllCaptureAnonPerHour = 200
)

type pllCaptureItem struct {
	original, context, domain, source, url string
}

func (it pllCaptureItem) key() string {
	return it.original + "\x00" + it.context + "\x00" + it.domain
}

// pllCapturer is the per-server capture queue.
type pllCapturer struct {
	seen      sync.Map // key => time.Time queued
	seenN     atomic.Int64
	mu        sync.Mutex
	pending   map[string]pllCaptureItem
	scheduled bool
	window    time.Time // start of the current one-minute window
	inWindow  int
	anon      map[string]*pllCaptureBudget // source => new strings from untrusted requests this hour
}

type pllCaptureBudget struct {
	start time.Time
	n     int
}

var pllCapturers sync.Map // *Server => *pllCapturer

func (s *Server) pllCapture() *pllCapturer {
	v, _ := pllCapturers.LoadOrStore(s, &pllCapturer{})
	return v.(*pllCapturer)
}

// pllCaptureClean makes a value safe to store: valid UTF-8, no NUL, trimmed.
func pllCaptureClean(v string) string {
	if !utf8.ValidString(v) {
		v = strings.ToValidUTF8(v, "")
	}
	if strings.IndexByte(v, 0) >= 0 {
		v = strings.ReplaceAll(v, "\x00", "")
	}
	return strings.TrimSpace(v)
}

func pllCaptureHasLetter(v string) bool {
	for _, r := range v {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// pllCaptureString records a string the site shows: original (plain text), its gettext context
// and domain, where it comes from (source: "theme:<slug>", "plugin:<slug>", "core" or "page") and
// the page it was seen on. It returns at once; the row is written in the background. Nothing is
// recorded while Multi Language is off, nor page text (source "page") while the String capture
// setting is off.
func (s *Server) pllCaptureString(ctx context.Context, original, context, domain, source, url string) {
	if s == nil || s.store == nil || !s.golylangOn() {
		return
	}
	if pllCaptureClean(source) == "page" && !s.pllOpts().StringCapture {
		return
	}
	s.pllCaptureQueue(ctx, original, context, domain, source, url)
}

// pllCaptureQueue is pllCaptureString without the plugin and setting checks (the page string pass
// makes them once per page).
func (s *Server) pllCaptureQueue(ctx context.Context, original, context, domain, source, url string) {
	if s == nil || s.store == nil {
		return
	}
	it := pllCaptureItem{original: pllCaptureClean(original), context: pllCaptureClean(context), domain: pllCaptureClean(domain),
		source: pllCaptureClean(source), url: pllCaptureClean(url)}
	if it.original == "" || len(it.original) > pllCaptureMaxBytes || utf8.RuneCountInString(it.original) > pllCaptureMaxRunes ||
		!pllCaptureHasLetter(it.original) || len(it.context) > pllCaptureMaxMeta || len(it.domain) > pllCaptureMaxMeta || len(it.source) > pllCaptureMaxMeta {
		return
	}
	if len(it.url) > pllCaptureMaxURL {
		cut := pllCaptureMaxURL
		for cut > 0 && !utf8.RuneStart(it.url[cut]) {
			cut--
		}
		it.url = it.url[:cut]
	}
	pc := s.pllCapture()
	key := it.key()
	now := time.Now()
	if v, ok := pc.seen.Load(key); ok && now.Sub(v.(time.Time)) < pllCaptureRefresh {
		return
	}
	if !pllCaptureRequestSlot(ctx) {
		return
	}
	untrusted := it.source != "page" && !s.pllCaptureTrusted(ctx) // page text callers check the user themselves
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if now.Sub(pc.window) >= time.Minute {
		pc.window, pc.inWindow = now, 0
	}
	if pc.inWindow >= pllCapturePerMinute || len(pc.pending) >= pllCaptureQueueMax {
		return
	}
	if untrusted {
		if pc.anon == nil {
			pc.anon = map[string]*pllCaptureBudget{}
		}
		b := pc.anon[it.source]
		if b == nil || now.Sub(b.start) >= time.Hour {
			if b == nil && len(pc.anon) >= 1000 { // sources are theme and plugin slugs: a few
				return
			}
			b = &pllCaptureBudget{start: now}
			pc.anon[it.source] = b
		}
		if b.n >= pllCaptureAnonPerHour {
			return
		}
		b.n++
	}
	if _, loaded := pc.seen.Swap(key, now); !loaded {
		if pc.seenN.Add(1) > pllCaptureSeenMax {
			pc.seen.Clear()
			pc.seenN.Store(0)
		}
	}
	pc.inWindow++
	if pc.pending == nil {
		pc.pending = map[string]pllCaptureItem{}
	}
	if _, dup := pc.pending[key]; !dup {
		pc.pending[key] = it
	}
	if !pc.scheduled {
		pc.scheduled = true
		time.AfterFunc(pllCaptureDelay, func() { s.pllCaptureSubmit() })
	}
}

// pllCaptureTrusted reports whether the request of ctx comes from a user who can manage_options
// or is a visual translation session (no request: not trusted).
func (s *Server) pllCaptureTrusted(ctx context.Context) bool {
	var c *gin.Context
	if gc, ok := ctx.(*gin.Context); ok {
		c = gc
	} else if rc := renderFrom(ctx); rc != nil {
		c = rc.c
	}
	if c == nil || c.Request == nil {
		return false
	}
	if u := currentUser(c); u != nil && u.Can("manage_options", nil) {
		return true
	}
	return s.pllVisualFrom(c) != nil
}

// pllCaptureRequestSlot counts a new string against the request's limit (no request: no limit
// beyond the per-minute one).
func pllCaptureRequestSlot(ctx context.Context) bool {
	r := pllFrom(ctx)
	if r == nil {
		return true
	}
	v, _ := r.cache.LoadOrStore("capture:n", new(atomic.Int32))
	return v.(*atomic.Int32).Add(1) <= pllCapturePerRequest
}

// take removes up to n queued items.
func (pc *pllCapturer) take(n int) []pllCaptureItem {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	var out []pllCaptureItem
	for k, it := range pc.pending {
		if n > 0 && len(out) >= n {
			break
		}
		out = append(out, it)
		delete(pc.pending, k)
	}
	if len(pc.pending) == 0 {
		pc.scheduled = false
	}
	return out
}

// pllCaptureSubmit hands the queued strings to the background writer in batches.
func (s *Server) pllCaptureSubmit() {
	pc := s.pllCapture()
	for {
		batch := pc.take(pllCaptureBatch)
		if len(batch) == 0 {
			return
		}
		s.rt.writes.submit(func(ctx context.Context) {
			if err := s.pllCaptureWrite(ctx, batch); err != nil && ctx.Err() == nil {
				slog.Warn("golylang: string capture", "err", err)
			}
		})
	}
}

// pllCaptureFlush writes everything queued now (Server.Close, tests).
func (s *Server) pllCaptureFlush(ctx context.Context) {
	v, ok := pllCapturers.Load(s)
	if !ok {
		return
	}
	pc := v.(*pllCapturer)
	for {
		batch := pc.take(pllCaptureBatch)
		if len(batch) == 0 {
			return
		}
		if err := s.pllCaptureWrite(ctx, batch); err != nil {
			slog.Warn("golylang: string capture", "err", err)
			return
		}
	}
}

// pllCaptureWrite inserts a batch (keys are unique within it) or refreshes last_seen. A string
// first found on a page and later registered by a theme or plugin takes the more precise source.
func (s *Server) pllCaptureWrite(ctx context.Context, batch []pllCaptureItem) error {
	if len(batch) == 0 {
		return nil
	}
	n := len(batch)
	orig, ctxs, doms, srcs, urls := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	for i, it := range batch {
		orig[i], ctxs[i], doms[i], srcs[i], urls[i] = it.original, it.context, it.domain, it.source, it.url
	}
	_, err := s.store.Pool.Exec(ctx, `INSERT INTO pll_string_sources (original, context, domain, source, url)
		SELECT * FROM unnest($1::text[], $2::text[], $3::text[], $4::text[], $5::text[])
		ON CONFLICT (original, context, domain) DO UPDATE SET last_seen = now(),
			source = CASE WHEN pll_string_sources.source IN ('', 'page') AND EXCLUDED.source NOT IN ('', 'page')
				THEN EXCLUDED.source ELSE pll_string_sources.source END,
			url = CASE WHEN pll_string_sources.url = '' THEN EXCLUDED.url ELSE pll_string_sources.url END`,
		orig, ctxs, doms, srcs, urls)
	return err
}

// pllCaptureForget drops the dedup memory (after strings were deleted or cleaned, so they can be
// recorded again when they show up).
func (s *Server) pllCaptureForget() {
	if v, ok := pllCapturers.Load(s); ok {
		pc := v.(*pllCapturer)
		pc.seen.Clear()
		pc.seenN.Store(0)
	}
}
