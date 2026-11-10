package cache

import (
	"context"
	"net/http"
	"sync/atomic"
)

type degradedKey struct{}

// WithDegradedFlag returns a request whose context carries a "degraded render" flag. A
// component that had to leave out part of the page (a plugin callback skipped because its
// instance was busy) calls MarkDegraded; the cache then does not store that response, so one
// incomplete render is never served to every later visitor.
func WithDegradedFlag(r *http.Request) *http.Request {
	if _, ok := r.Context().Value(degradedKey{}).(*atomic.Bool); ok {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), degradedKey{}, new(atomic.Bool)))
}

// MarkDegraded flags the request owning ctx as incompletely rendered (no-op without a flag).
func MarkDegraded(ctx context.Context) {
	if f, ok := ctx.Value(degradedKey{}).(*atomic.Bool); ok {
		f.Store(true)
	}
}

// Degraded reports whether MarkDegraded was called for the request.
func Degraded(r *http.Request) bool {
	f, ok := r.Context().Value(degradedKey{}).(*atomic.Bool)
	return ok && f.Load()
}
