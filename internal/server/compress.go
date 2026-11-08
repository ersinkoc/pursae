package server

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
)

// Response compression for everything Pursae Cache does not serve compressed itself: the admin,
// REST, logged-in pages, the cart, bypassed pages. Pursae Cache's stored pages and the embedded
// static files arrive already encoded and pass untouched.

// gzipMinSize is the smallest body worth compressing.
const gzipMinSize = 1024

var gzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
	return w
}}

// gzipResponses compresses responses for clients that accept gzip. It leaves alone HEAD requests,
// upgrades (WebSocket), responses that already carry a Content-Encoding or Cache-Control: no-transform,
// bodies under gzipMinSize, already-compressed media (images, video, audio, fonts, archives) and
// streams (text/event-stream).
func gzipResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.Header.Get("Upgrade") != "" || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipWriter{w: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

// acceptsGzip reports whether an Accept-Encoding value allows gzip (q=0 refuses it).
func acceptsGzip(ae string) bool {
	for _, part := range strings.Split(ae, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "gzip" && name != "x-gzip" && name != "*" {
			continue
		}
		q := strings.ReplaceAll(strings.ToLower(params), " ", "")
		if q == "q=0" || q == "q=0.0" || q == "q=0.00" || q == "q=0.000" {
			return false
		}
		return true
	}
	return false
}

// compressibleType reports whether a media type gains from gzip.
func compressibleType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(ct))
	}
	switch {
	case mt == "text/event-stream":
		return false
	case strings.HasPrefix(mt, "text/"):
		return true
	case mt == "image/svg+xml", mt == "image/x-icon", mt == "image/vnd.microsoft.icon":
		return true
	case strings.HasPrefix(mt, "application/"):
		sub := strings.TrimPrefix(mt, "application/")
		return sub == "json" || sub == "javascript" || sub == "x-javascript" || sub == "xml" || sub == "xhtml+xml" ||
			sub == "rss+xml" || sub == "atom+xml" || sub == "manifest+json" || sub == "ld+json" || sub == "wasm" ||
			strings.HasSuffix(sub, "+json") || strings.HasSuffix(sub, "+xml")
	}
	return false
}

const (
	gzUndecided = iota
	gzBuffering // eligible, collecting the first gzipMinSize bytes
	gzCompress
	gzPass
)

// gzipWriter decides at the first header/body write: eligible responses are buffered until they
// pass gzipMinSize and then compressed; everything else streams through unchanged.
type gzipWriter struct {
	w      http.ResponseWriter
	mode   int
	status int
	buf    bytes.Buffer
	gz     *gzip.Writer
	header http.Header // the header as WriteHeader saw it: later changes are ignored, as net/http does
}

func (g *gzipWriter) Header() http.Header { return g.w.Header() }

// Unwrap lets http.ResponseController reach the underlying writer.
func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.w }

func (g *gzipWriter) WriteHeader(code int) {
	if g.mode != gzUndecided {
		return
	}
	if code >= 100 && code < 200 {
		g.w.WriteHeader(code) // informational; the final header follows
		return
	}
	g.status = code
	h := g.w.Header()
	switch {
	case code == http.StatusNoContent || code == http.StatusNotModified || code == http.StatusPartialContent || code < 200,
		h.Get("Content-Encoding") != "",
		strings.Contains(strings.ToLower(h.Get("Cache-Control")), "no-transform"):
		g.pass()
		return
	}
	if ct := h.Get("Content-Type"); ct != "" {
		if !compressibleType(ct) {
			g.pass()
			return
		}
		addVary(h, "Accept-Encoding")
	}
	if cl := h.Get("Content-Length"); cl != "" && len(cl) < 4 { // under 1000 bytes
		g.pass()
		return
	}
	g.mode = gzBuffering
	g.header = h.Clone()
}

// restoreHeader puts back the header WriteHeader saw (a handler may not change it afterwards).
func (g *gzipWriter) restoreHeader() {
	if g.header == nil {
		return
	}
	h := g.w.Header()
	for k := range h {
		delete(h, k)
	}
	for k, v := range g.header {
		h[k] = v
	}
	g.header = nil
}

// pass sends the header as it is and streams the body.
func (g *gzipWriter) pass() {
	g.mode = gzPass
	g.w.WriteHeader(g.status)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if g.mode == gzUndecided {
		g.WriteHeader(http.StatusOK)
	}
	switch g.mode {
	case gzBuffering:
		g.buf.Write(b)
		if g.buf.Len() >= gzipMinSize {
			if err := g.start(); err != nil {
				return 0, err
			}
		}
		return len(b), nil
	case gzCompress:
		return g.gz.Write(b)
	}
	return g.w.Write(b)
}

// start decides on the buffered bytes: compress when the (sniffed) type allows it, else pass.
func (g *gzipWriter) start() error {
	g.restoreHeader()
	h := g.w.Header()
	if h.Get("Content-Type") == "" {
		ct := http.DetectContentType(g.buf.Bytes())
		h.Set("Content-Type", ct)
		if !compressibleType(ct) {
			return g.flushPlain()
		}
		addVary(h, "Accept-Encoding")
	}
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	if et := h.Get("ETag"); et != "" && !strings.HasPrefix(et, "W/") {
		h.Set("ETag", "W/"+et) // the encoded body is not byte-identical to the strong validator's
	}
	g.mode = gzCompress
	g.w.WriteHeader(g.status)
	g.gz = gzipPool.Get().(*gzip.Writer)
	g.gz.Reset(g.w)
	_, err := g.gz.Write(g.buf.Bytes())
	g.buf = bytes.Buffer{}
	return err
}

// flushPlain sends the header and the buffered bytes uncompressed and streams the rest.
func (g *gzipWriter) flushPlain() error {
	g.restoreHeader()
	g.pass()
	_, err := g.w.Write(g.buf.Bytes())
	g.buf = bytes.Buffer{}
	return err
}

// Flush sends what is buffered (compressing it when it may still grow past gzipMinSize).
func (g *gzipWriter) Flush() {
	switch g.mode {
	case gzUndecided:
		g.WriteHeader(http.StatusOK)
		if g.mode == gzBuffering { // a streamed response of unknown size: just stream it
			_ = g.flushPlain()
		}
	case gzBuffering:
		_ = g.flushPlain()
	case gzCompress:
		_ = g.gz.Flush()
	}
	_ = http.NewResponseController(g.w).Flush()
}

// Hijack hands the connection over (WebSocket upgrades that slipped past the Upgrade check).
func (g *gzipWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	g.mode = gzPass
	return http.NewResponseController(g.w).Hijack()
}

// close finishes the response: a small buffered body goes out uncompressed.
func (g *gzipWriter) close() {
	switch g.mode {
	case gzBuffering:
		g.restoreHeader()
		if h := g.w.Header(); h.Get("Content-Type") == "" && g.buf.Len() > 0 {
			h.Set("Content-Type", http.DetectContentType(g.buf.Bytes()))
		}
		_ = g.flushPlain()
	case gzCompress:
		_ = g.gz.Close()
		g.gz.Reset(io.Discard)
		gzipPool.Put(g.gz)
		g.gz = nil
	}
}

// addVary adds a field to Vary unless it is listed already.
func addVary(h http.Header, field string) {
	for _, v := range h.Values("Vary") {
		for _, f := range strings.Split(v, ",") {
			if f = strings.TrimSpace(f); strings.EqualFold(f, field) || f == "*" {
				return
			}
		}
	}
	h.Add("Vary", field)
}
