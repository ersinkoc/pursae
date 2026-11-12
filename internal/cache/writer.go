package cache

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// passWriter streams a bypassed response, removing internal headers and adding the
// bypass status, no-cache proxy hints for pages and browser-cache headers for assets.
type passWriter struct {
	http.ResponseWriter
	c           *Cache
	r           *http.Request
	wroteHeader bool
	hijacked    bool
}

func (p *passWriter) WriteHeader(code int) {
	if p.wroteHeader {
		return
	}
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		p.ResponseWriter.WriteHeader(code) // informational; the final header follows
		return
	}
	p.wroteHeader = true
	h := p.Header()
	stripInternal(h)
	p.c.decorateUncached(h, p.r, code)
	p.ResponseWriter.WriteHeader(code)
}

func (p *passWriter) Write(b []byte) (int, error) {
	if !p.wroteHeader {
		p.WriteHeader(http.StatusOK)
	}
	return p.ResponseWriter.Write(b)
}

func (p *passWriter) Flush() {
	if !p.wroteHeader {
		p.WriteHeader(http.StatusOK)
	}
	http.NewResponseController(p.ResponseWriter).Flush()
}

func (p *passWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(p.ResponseWriter).Hijack()
	p.hijacked = err == nil
	return conn, rw, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (p *passWriter) Unwrap() http.ResponseWriter { return p.ResponseWriter }

// finish strips internal headers when the handler returned without writing, since
// net/http then sends the header map as is.
func (p *passWriter) finish() {
	if !p.wroteHeader && !p.hijacked {
		stripInternal(p.Header())
	}
}

// decorateUncached adds headers to a response that is not served from the cache.
func (c *Cache) decorateUncached(h http.Header, r *http.Request, code int) {
	isRead := r.Method == http.MethodGet || r.Method == http.MethodHead
	mt := mediaType(h)
	if isRead && code == http.StatusOK && isStaticType(mt) && h.Get("Cache-Control") == "" {
		if v := c.BrowserCacheHeader(); v != "" {
			h.Set("Cache-Control", v)
		}
	}
	if !c.cfg.Enabled || !isRead || (mt != "" && !cacheableType(mt)) {
		return
	}
	h.Set(HeaderStatus, "bypass")
	c.writeNoCacheHints(h, r)
}

const (
	modeUndecided = iota
	modeCapture
	modePass
	modeHijacked
)

// captureWriter buffers a cacheable response so it can be stored and then served
// with ETag and gzip negotiation. It falls back to streaming (modePass) as soon as
// the response turns out not to be cacheable or exceeds MaxEntrySize.
type captureWriter struct {
	w      http.ResponseWriter
	c      *Cache
	r      *http.Request
	d      Decision
	mode   int
	status int
	header http.Header // response headers as committed by WriteHeader
	buf    bytes.Buffer
	ttl    time.Duration
	tags   []string
	html   bool
	start  time.Time
	gen    uint64 // the store's purge generation before the render (Store.SetSince)
	// release, when set, ends this render's claim on its key (flightGroup) once the page is
	// stored, before it is written to a possibly slow client.
	release func()
}

func newCaptureWriter(w http.ResponseWriter, c *Cache, r *http.Request, d Decision) *captureWriter {
	return &captureWriter{w: w, c: c, r: r, d: d, start: c.now(), gen: c.store.Generation()}
}

func (cw *captureWriter) Header() http.Header { return cw.w.Header() }

// Unwrap lets http.ResponseController reach the underlying writer.
func (cw *captureWriter) Unwrap() http.ResponseWriter { return cw.w }

func (cw *captureWriter) WriteHeader(code int) {
	if cw.mode != modeUndecided {
		return
	}
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		cw.w.WriteHeader(code)
		return
	}
	cw.status = code
	h := cw.w.Header()
	ttl, ok := cw.c.storableTTL(cw.r, code, h)
	cw.tags = parseTags(h.Get(HeaderTags))
	stripInternal(h)
	if !ok {
		cw.mode = modePass
		cw.releaseFlight() // nothing will be stored: the waiting requests render their own copy now
		cw.c.decorateUncached(h, cw.r, code)
		cw.w.WriteHeader(code)
		return
	}
	cw.ttl = ttl
	cw.html = mediaType(h) == "text/html"
	cw.header = h.Clone()
	cw.mode = modeCapture
}

func (cw *captureWriter) Write(b []byte) (int, error) {
	if cw.mode == modeUndecided {
		cw.WriteHeader(http.StatusOK)
	}
	switch cw.mode {
	case modeCapture:
		if int64(cw.buf.Len()+len(b)) > cw.c.cfg.MaxEntrySize {
			if err := cw.spill(); err != nil {
				return 0, err
			}
			return cw.w.Write(b)
		}
		return cw.buf.Write(b)
	case modeHijacked:
		return 0, http.ErrHijacked
	}
	return cw.w.Write(b)
}

// spill abandons capturing and streams what was buffered.
func (cw *captureWriter) spill() error {
	cw.mode = modePass
	cw.releaseFlight()
	cw.restoreHeaders()
	h := cw.w.Header()
	cw.c.decorateUncached(h, cw.r, cw.status)
	cw.w.WriteHeader(cw.status)
	n, err := cw.w.Write(cw.buf.Bytes())
	if err == nil && n != cw.buf.Len() {
		err = io.ErrShortWrite
	}
	cw.buf = bytes.Buffer{}
	return err
}

func (cw *captureWriter) restoreHeaders() {
	h := cw.w.Header()
	clear(h)
	for k, values := range cw.header {
		h[k] = append([]string(nil), values...)
	}
}

// releaseFlight ends this render's claim on its key. It runs as soon as the outcome is known
// (stored, or streaming uncached), never after the body went out to a possibly slow client: the
// requests waiting for this render must not wait for that client too.
func (cw *captureWriter) releaseFlight() {
	if cw.release != nil {
		cw.release()
	}
}

// Flush streams a response that the handler explicitly flushes.
func (cw *captureWriter) Flush() {
	_ = cw.FlushError()
}

// FlushError also exposes streaming errors through http.ResponseController.
func (cw *captureWriter) FlushError() error {
	if cw.mode == modeUndecided {
		cw.WriteHeader(http.StatusOK)
	}
	if cw.mode == modeCapture {
		if err := cw.spill(); err != nil {
			return err
		}
	}
	if cw.mode == modePass {
		return http.NewResponseController(cw.w).Flush()
	}
	return nil
}

// Hijack abandons caching and hands the connection to the handler.
func (cw *captureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(cw.w).Hijack()
	if err == nil {
		cw.mode = modeHijacked
		cw.releaseFlight()
	}
	return conn, rw, err
}

// finish stores and serves a captured response.
func (cw *captureWriter) finish() {
	switch cw.mode {
	case modeUndecided:
		// The handler wrote nothing: an empty 200, which is not worth caching.
		cw.mode = modePass
		cw.releaseFlight()
		h := cw.w.Header()
		stripInternal(h)
		cw.w.WriteHeader(http.StatusOK)
		return
	case modeCapture:
	default:
		return
	}
	cw.restoreHeaders()
	body := cw.buf.Bytes()
	opt := cw.c.cfg.Optimize
	if cw.html && opt.enabled() && (cw.status == http.StatusOK || cw.status == http.StatusNotFound) {
		body = Optimize(body, opt)
	}
	e, err := NewEntry(cw.d.Key, cw.status, cw.w.Header(), body, cw.tags, cw.start, cw.ttl)
	if err != nil {
		// Unreachable in practice: storableTTL already rejected Set-Cookie.
		cw.releaseFlight()
		cw.c.decorateUncached(cw.w.Header(), cw.r, cw.status)
		cw.w.WriteHeader(cw.status)
		cw.w.Write(body)
		return
	}
	// HEAD responses carry no body, so only GET results are stored. A purge that ran while the
	// page rendered (a post saved meanwhile) wins: the page is served but not stored.
	if cw.r.Method == http.MethodGet {
		_ = cw.c.store.SetSince(e, cw.gen)
	}
	cw.releaseFlight()
	cw.c.serve(cw.w, cw.r, e, "miss", cw.d.Private)
}

// storableTTL decides whether a response may be stored and for how long.
func (c *Cache) storableTTL(r *http.Request, code int, h http.Header) (time.Duration, bool) {
	if r.Method == http.MethodHead || len(h.Values("Set-Cookie")) > 0 || len(h.Values("Trailer")) > 0 || h.Get("Content-Encoding") != "" || !cacheableVary(h) {
		return 0, false
	}
	for _, v := range h.Values(HeaderControl) {
		if hasToken(v, "no-cache") || hasToken(v, "no-store") || hasToken(v, "private") {
			return 0, false
		}
	}
	for _, cc := range h.Values("Cache-Control") {
		if hasToken(cc, "private") || hasToken(cc, "no-store") || hasToken(cc, "no-cache") {
			return 0, false
		}
	}
	if Degraded(r) {
		return 0, false
	}
	mt := mediaType(h)
	switch code {
	case http.StatusOK, http.StatusNotFound:
		if !cacheableType(mt) {
			return 0, false
		}
	case http.StatusMovedPermanently:
		if mt != "" && !cacheableType(mt) {
			return 0, false
		}
	default:
		return 0, false
	}

	var ttl time.Duration
	switch {
	case code == http.StatusNotFound:
		ttl = c.cfg.NotFoundTTL
	case code == http.StatusMovedPermanently:
		ttl = c.cfg.RedirectTTL
	case isFeedType(mt) || isFeedPath(r.URL.Path):
		ttl = c.cfg.FeedTTL
	case r.URL.Path == "/" || r.URL.Path == "":
		ttl = c.cfg.FrontPageTTL
	default:
		ttl = c.cfg.PublicTTL
	}
	if v := strings.TrimSpace(h.Get(HeaderTTL)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			ttl = time.Duration(n) * time.Second
		}
	}
	// Search results: every ?s= value is its own entry, so they expire soon instead of piling up for days.
	if ttl > searchTTL && r.URL.Query().Has("s") {
		ttl = searchTTL
	}
	return ttl, ttl > 0
}
