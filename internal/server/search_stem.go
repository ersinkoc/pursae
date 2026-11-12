package server

import (
	"context"
	"log/slog"
	"time"
)

// syncSearchStemAsync points the stemmed post search at the site language (WPLANG) and rebuilds the
// stemmed vectors in the background when the language changed (store.SyncSearchStem, migration 0101).
// It runs at start-up and after Settings › General or the installer changes the language.
func (s *Server) syncSearchStemAsync() {
	locale := s.settings.String("WPLANG")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		start := time.Now()
		rebuilt, err := s.store.SyncSearchStem(ctx, locale)
		switch {
		case err != nil:
			slog.Warn("search stemming", "locale", locale, "err", err)
		case rebuilt:
			slog.Info("search stemming rebuilt", "locale", locale, "took", time.Since(start).Round(time.Millisecond))
		}
	}()
}
