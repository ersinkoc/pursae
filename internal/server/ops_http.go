package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/db"
)

// Operations plumbing around every request: liveness and readiness probes, request IDs, the
// access log and the per-read body deadline (see newHTTPServer in cmd/wpgo for the timeouts).

// HeaderRequestID carries the request ID: taken from the proxy when it looks sane, else generated;
// echoed on the response and written to the access log.
const HeaderRequestID = "X-Request-ID"

var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._:@/+=-]{1,128}$`)

// bodyIdleTimeout is how long a request body may make no progress: a client that stops sending
// mid-upload fails after it instead of holding the connection and its handler forever. There is
// no limit on the total time (a 16 GB backup import over a slow link is fine).
var bodyIdleTimeout = time.Minute

// opsHandler wraps the site handler: /healthz and /readyz first (before maintenance mode, the
// security plugin and the page cache), then a request ID and the body deadline.
func (s *Server) opsHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			s.healthz(w, r)
			return
		case "/readyz":
			s.readyz(w, r)
			return
		}
		id := r.Header.Get(HeaderRequestID)
		if !requestIDRe.MatchString(id) {
			id = newRequestID()
			r.Header.Set(HeaderRequestID, id)
		}
		w.Header().Set(HeaderRequestID, id)
		if r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0 {
			r.Body = &idleBody{ReadCloser: r.Body, rc: http.NewResponseController(w), idle: bodyIdleTimeout}
		}
		next.ServeHTTP(w, r)
	})
}

func newRequestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// idleBody sets the connection's read deadline before each read of the body and clears it once
// the body is done: a stalled client fails after bodyIdleTimeout, while a handler that keeps
// working after reading the body is not cut off (an expired deadline would cancel its context).
type idleBody struct {
	io.ReadCloser
	rc   *http.ResponseController
	idle time.Duration
	done bool
}

func (b *idleBody) Read(p []byte) (int, error) {
	if !b.done {
		_ = b.rc.SetReadDeadline(time.Now().Add(b.idle))
	}
	n, err := b.ReadCloser.Read(p)
	if err != nil && !b.done {
		b.done = true
		_ = b.rc.SetReadDeadline(time.Time{})
	}
	return n, err
}

func (b *idleBody) Close() error {
	if !b.done {
		b.done = true
		_ = b.rc.SetReadDeadline(time.Time{})
	}
	return b.ReadCloser.Close()
}

// healthz is the liveness probe: the process serves HTTP. It does not touch the database, so a
// database outage does not get the container restarted in a loop.
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	probeAnswer(w, r, http.StatusOK, "ok")
}

// readyz is the readiness probe: the database answers and every migration of this binary is
// applied.
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Pool.Ping(ctx); err != nil {
		probeAnswer(w, r, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if !migrationsApplied.Load() {
		n, err := db.Pending(ctx, s.store.Pool)
		if err != nil || n > 0 {
			probeAnswer(w, r, http.StatusServiceUnavailable, "migrations pending")
			return
		}
		migrationsApplied.Store(true) // migrations are embedded: once applied, they stay applied
	}
	probeAnswer(w, r, http.StatusOK, "ok")
}

var migrationsApplied atomic.Bool

func probeAnswer(w http.ResponseWriter, r *http.Request, code int, msg string) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.Set("Allow", "GET, HEAD")
		code, msg = http.StatusMethodNotAllowed, "method not allowed"
	}
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, msg+"\n")
	}
}

// accessLog writes one line per request: status, method, URI, duration and request ID. With
// WPGO_LOG_FORMAT=json the line is a JSON object (log/slog) for log shippers. Static assets are
// not logged; probes never reach it (opsHandler answers them).
func accessLog() gin.HandlerFunc {
	var jl *slog.Logger
	if strings.EqualFold(os.Getenv("WPGO_LOG_FORMAT"), "json") {
		jl = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/wp-admin/assets/") || strings.HasPrefix(p, "/wp-includes/") || strings.HasPrefix(p, "/wp-content/") {
			return
		}
		d := time.Since(start).Round(time.Microsecond)
		id := c.Request.Header.Get(HeaderRequestID)
		if jl != nil {
			jl.LogAttrs(c.Request.Context(), slog.LevelInfo, "request",
				slog.Int("status", c.Writer.Status()), slog.String("method", c.Request.Method),
				slog.String("uri", c.Request.URL.RequestURI()), slog.Duration("duration", d),
				slog.String("ip", c.ClientIP()), slog.String("request_id", id))
			return
		}
		log.Printf("%d %s %s %s id=%s", c.Writer.Status(), c.Request.Method, c.Request.URL.RequestURI(), d, id)
	}
}
