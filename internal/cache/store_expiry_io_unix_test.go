//go:build unix

package cache

import (
	"net/http"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestDiskLoadRechecksExpiryAfterIO(t *testing.T) {
	for _, tc := range []struct {
		name    string
		advance time.Duration
		want    bool
	}{{"fresh", time.Hour - time.Nanosecond, true}, {"expired", time.Hour, false}} {
		t.Run(tc.name, func(t *testing.T) {
			base := time.Unix(1000, 0)
			var now atomic.Int64
			now.Store(base.UnixNano())
			s, err := NewStore(StoreOptions{MaxEntries: 1, DiskDir: t.TempDir(), Now: func() time.Time { return time.Unix(0, now.Load()) }})
			if err != nil {
				t.Fatal(err)
			}
			key := "https://example.test/a#d"
			a, _ := NewEntry(key, 200, http.Header{}, []byte("a"), nil, base, time.Hour)
			b, _ := NewEntry("https://example.test/b#d", 200, http.Header{}, []byte("b"), nil, base, time.Hour)
			if err := s.Set(a); err != nil {
				t.Fatal(err)
			}
			if err := s.Set(b); err != nil {
				t.Fatal(err)
			}
			path := s.disk.path(key)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			done := make(chan bool, 1)
			go func() { _, ok := s.Get(key); done <- ok }()
			w, err := os.OpenFile(path, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			now.Store(base.Add(tc.advance).UnixNano())
			if _, err := w.Write(raw); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if got := <-done; got != tc.want {
				t.Fatalf("hit=%v want %v", got, tc.want)
			}
		})
	}
}
