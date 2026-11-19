package cache

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

type diskReadErrorGate struct {
	slog.Handler
	entered chan struct{}
	resume  chan struct{}
	blocked atomic.Bool
}

func (g *diskReadErrorGate) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == "wpgo-cache: loading persisted entry" && g.blocked.CompareAndSwap(false, true) {
		close(g.entered)
		<-g.resume
	}
	return g.Handler.Handle(ctx, record)
}

func TestDelayedDiskReadFailurePreservesRecoveredEntry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reevict bool
	}{
		{name: "resident"},
		{name: "evicted again", reevict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate := &diskReadErrorGate{
				Handler: slog.NewTextHandler(io.Discard, nil),
				entered: make(chan struct{}),
				resume:  make(chan struct{}),
			}
			s, err := NewStore(StoreOptions{DiskDir: t.TempDir(), MaxEntries: 1, Logger: slog.New(gate)})
			if err != nil {
				t.Fatal(err)
			}
			want := mkEntry(t, "https://example.test/page", 10)
			other := mkEntry(t, "https://example.test/other", 20)
			if err := s.Set(want); err != nil {
				t.Fatal(err)
			}
			if err := s.Set(other); err != nil {
				t.Fatal(err)
			}
			path := s.disk.path(want.Key)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("bad"), 0o600); err != nil {
				t.Fatal(err)
			}

			done := make(chan *Entry, 1)
			resume := sync.OnceFunc(func() { close(gate.resume) })
			defer func() {
				resume()
				<-done
			}()
			go func() {
				e, _ := s.Get(want.Key)
				done <- e
				close(done)
			}()
			select {
			case <-gate.entered:
			case <-done:
				t.Fatal("disk read returned before reporting an error")
			}

			// Restore the file after the first read closes it, then promote it via a second read.
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if e, ok := s.Get(want.Key); !ok || string(e.Body) != string(want.Body) {
				t.Fatalf("second read did not recover the entry: %+v", e)
			}
			if tc.reevict {
				if err := s.Set(other); err != nil {
					t.Fatal(err)
				}
			}
			resume()
			if e := <-done; !tc.reevict && (e == nil || string(e.Body) != string(want.Body)) {
				t.Errorf("delayed read did not use the recovered entry: %+v", e)
			}
			if e, ok := s.Get(want.Key); !ok || string(e.Body) != string(want.Body) {
				t.Fatalf("delayed failure lost the recovered entry: %+v", e)
			}
		})
	}
}
