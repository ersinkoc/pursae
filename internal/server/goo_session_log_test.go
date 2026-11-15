package server

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
)

// slogCapture records the messages of the default logger for the rest of the test.
type slogCapture struct {
	mu   sync.Mutex
	msgs []string
}

func (h *slogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (h *slogCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs = append(h.msgs, r.Message)
	return nil
}
func (h *slogCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *slogCapture) WithGroup(string) slog.Handler      { return h }

func (h *slogCapture) has(msg string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Contains(h.msgs, msg)
}

func captureSlog(t *testing.T) *slogCapture {
	t.Helper()
	h := &slogCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// A failed session cleanup or session load is logged instead of being dropped, and the daily job
// still finishes its other steps without panicking.
func TestGooSessionErrorsLogged(t *testing.T) {
	_, _, srv := gooSite(t)
	logs := captureSlog(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := srv.gooCleanupSessions(ctx); err == nil {
		t.Fatal("gooCleanupSessions on a cancelled context: no error")
	}
	if err := srv.gooDaily(ctx); err != nil {
		t.Fatalf("gooDaily: %v", err)
	}
	if !logs.has("goocommerce: session cleanup") {
		t.Error("the failed session cleanup was not logged")
	}
	if srv.gooLoadSession(ctx, &gooSession{Key: "k"}) {
		t.Error("a failed load must not count as a session")
	}
	if !logs.has("goocommerce: session load") {
		t.Error("the failed session load was not logged")
	}
	if srv.gooLoadSession(context.Background(), &gooSession{Key: "missing"}) {
		t.Error("an unknown key is no session")
	}
}
