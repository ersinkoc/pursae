package server

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"wpgo/internal/db"
)

// The schema guard (internal/db/schema_guard.go) from the server's side: Migrate refuses a binary
// below the floor a newer release recorded (unless PURSAE_ALLOW_OLDER_BINARY=1), the start-up logs a
// warning once when the schema is newer than this binary but still compatible, and /readyz reports
// both conditions.

func init() { db.BinaryVersion = Version } // what Migrate records as migrated_by and checks against

// schemaGuardTTL is how long /readyz reuses a schema check: a newer instance may migrate (and raise
// the floor) while this one runs, but readiness is polled every few seconds.
const schemaGuardTTL = 30 * time.Second

type schemaGuardState struct {
	mu   sync.Mutex
	at   time.Time
	st   db.SchemaStatus
	warn sync.Once
}

// schemaStatus is the cached schema check of this binary against the database.
func (s *Server) schemaStatus(ctx context.Context) (db.SchemaStatus, error) {
	g := &s.schemaGuard
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.at.IsZero() && time.Since(g.at) < schemaGuardTTL && g.st.Binary == Version {
		return g.st, nil
	}
	st, err := db.CheckSchema(ctx, s.store.Pool, Version)
	if err != nil {
		return st, err // not cached: the next probe asks again
	}
	g.st, g.at = st, time.Now()
	s.warnSchema(st)
	return st, nil
}

// warnSchema logs, once per process, that the database is newer than this binary.
func (s *Server) warnSchema(st db.SchemaStatus) {
	if !st.Newer() || s.sandbox != nil {
		return
	}
	s.schemaGuard.warn.Do(func() {
		if st.TooOld() { // only reachable with PURSAE_ALLOW_OLDER_BINARY=1
			slog.Error("schema guard overridden by "+db.AllowOlderBinaryEnv,
				"reason", (&db.BinaryTooOldError{Status: st}).Error(), "version", st.Binary, "needs", st.MinBinary)
			return
		}
		slog.Warn("schema guard: newer schema", "detail", st.Warning(), "version", st.Binary, "unknown_migrations", len(st.Unknown))
	})
}
