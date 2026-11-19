package cache

import (
	"testing"
	"time"
)

func TestPurgeAllFinishesDiskCleanupBeforeAdmittingNewEntries(t *testing.T) {
	now := time.Unix(1000, 0)
	s, err := NewStore(StoreOptions{MaxEntries: 1, DiskDir: t.TempDir(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	const key = "https://example.test/page"
	entry := func(k, body string) *Entry {
		return &Entry{Key: k, Status: 200, Body: []byte(body), Created: now, Expires: now.Add(time.Hour)}
	}
	if err := s.Set(entry(key, "old")); err != nil {
		t.Fatal(err)
	}

	entered, release := make(chan struct{}), make(chan struct{})
	purged := make(chan int, 1)
	go func() {
		purged <- s.purgeAll(func() {
			close(entered)
			<-release
			s.disk.clear()
		})
	}()
	<-entered
	unlocked := s.mu.TryLock()
	if unlocked {
		s.mu.Unlock()
	}
	written := make(chan error, 1)
	go func() { written <- s.Set(entry(key, "new")) }()
	if unlocked {
		// The faulty ordering lets the new record reach disk before clear.
		err = <-written
	}
	close(release)
	if n := <-purged; n != 1 {
		t.Errorf("purged %d entries, want 1", n)
	}
	if !unlocked {
		err = <-written
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(entry("https://example.test/other", "other")); err != nil {
		t.Fatal(err)
	}
	if e, hit := s.Get(key); !hit || string(e.Body) != "new" {
		t.Fatalf("entry after eviction = %v, want the new disk record", e)
	}
}
