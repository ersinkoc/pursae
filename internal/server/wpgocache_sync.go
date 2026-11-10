package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/auth"
	"wpgo/internal/cache"
)

// Pursae Cache keeps pages in the memory of each wpgo process. With several instances on one
// database (operations/reverse-proxy-and-tls.md) a post saved through instance A used to purge only
// A's copy: B kept serving the old page for the whole TTL. Every purge is therefore announced on a
// PostgreSQL channel and applied by the other instances (WatchCachePurges).

// cachePurgeChannel is the NOTIFY channel of page cache purges.
const cachePurgeChannel = "wpgo_cache_purge"

// cachePurgeMaxPayload keeps a notification under PostgreSQL's 8000-byte payload limit; a purge
// with more tags than that is announced as a purge of everything.
const cachePurgeMaxPayload = 7000

// cachePurgeMsg is the payload: the purge plus the instance that made it.
type cachePurgeMsg struct {
	Origin string `json:"o"`
	cache.Purge
}

var (
	cacheOriginOnce sync.Once
	cacheOriginID   string
)

// cacheOrigin identifies this process on the purge channel, so it skips its own announcements.
func cacheOrigin() string {
	cacheOriginOnce.Do(func() { cacheOriginID = auth.RandomToken(12) })
	return cacheOriginID
}

// cachePurgePayload encodes p for the channel.
func cachePurgePayload(origin string, p cache.Purge) string {
	raw, _ := json.Marshal(cachePurgeMsg{Origin: origin, Purge: p})
	if len(raw) > cachePurgeMaxPayload {
		raw, _ = json.Marshal(cachePurgeMsg{Origin: origin, Purge: cache.Purge{All: true}})
	}
	return string(raw)
}

// announcePurge tells the other instances about a purge of this one (cache.Options.OnPurge).
func (s *Server) announcePurge(p cache.Purge) {
	if s.store == nil || s.store.Pool == nil {
		return
	}
	s.asyncExec(`SELECT pg_notify($1, $2)`, cachePurgeChannel, cachePurgePayload(cacheOrigin(), p))
}

// applyRemotePurge applies a purge announced on the channel; its own announcements are skipped.
func (s *Server) applyRemotePurge(payload string) {
	var m cachePurgeMsg
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		m = cachePurgeMsg{Purge: cache.Purge{All: true}} // unreadable: be safe
	}
	if m.Origin == cacheOrigin() {
		return
	}
	if c := s.currentCache(); c != nil {
		c.Apply(m.Purge)
	}
}

// WatchCachePurges applies the page cache purges of the other instances sharing the database until
// ctx ends. main runs it for `wpgo serve`. The LISTEN connection is opened outside the pool. After
// the listener was down, notifications may have been missed, so the local cache is emptied.
func (s *Server) WatchCachePurges(ctx context.Context) {
	backoff := time.Second
	first := true
	for ctx.Err() == nil {
		err := s.listenCachePurges(ctx, !first)
		if ctx.Err() != nil {
			return
		}
		first = false
		slog.Warn("page cache: purge listener stopped, reconnecting", "err", err, "in", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// listenCachePurges runs one LISTEN session until it fails or ctx ends.
func (s *Server) listenCachePurges(ctx context.Context, missed bool) error {
	conn, err := pgx.ConnectConfig(ctx, s.store.Pool.Config().ConnConfig.Copy())
	if err != nil {
		return err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, "LISTEN "+cachePurgeChannel); err != nil {
		return err
	}
	if missed { // purges announced while nobody listened are lost
		if c := s.currentCache(); c != nil {
			c.Apply(cache.Purge{All: true})
		}
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		s.applyRemotePurge(n.Payload)
	}
}
