package cache

import (
	"errors"
	"os"
	"testing"
	"time"
)

func newDiskOrderStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(StoreOptions{MaxEntries: 1, DiskDir: t.TempDir(), Now: func() time.Time { return time.Unix(1000, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func setDiskOrderEntry(t *testing.T, s *Store, key, body string) *Entry {
	t.Helper()
	e, err := NewEntry(key, 200, nil, []byte(body), nil, s.now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(e); err != nil {
		t.Fatal(err)
	}
	return e
}

func checkLateDiskPublication(t *testing.T, replace bool) string {
	t.Helper()
	s := newDiskOrderStore(t)
	const key = "https://example.test/page"
	first := setDiskOrderEntry(t, s, key, "first")
	item := s.items[key]
	staged, size, err := s.disk.stage(first)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(staged) })

	// A staged write only finishes after the replacement or purge takes effect.
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		<-release
		done <- s.finishDiskWrite(item, staged, size, nil)
	}()
	released := false
	defer func() {
		if !released {
			close(release)
			<-done
		}
	}()
	if replace {
		setDiskOrderEntry(t, s, key, "second")
	} else {
		s.Delete(key)
	}
	close(release)
	released = true
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staged); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging file survived: %v", err)
	}
	e, err := s.disk.load(key)
	if err != nil {
		t.Fatal(err)
	}
	if e == nil {
		return ""
	}
	return string(e.Body)
}

func TestLateDiskPublicationKeepsCurrentEntry(t *testing.T) {
	if got := checkLateDiskPublication(t, true); got != "second" {
		t.Fatalf("disk body = %q, want second", got)
	}
	if got := checkLateDiskPublication(t, false); got != "" {
		t.Fatalf("disk body = %q after deletion, want a miss", got)
	}
}

func TestPendingDiskEntryDoesNotReadPredecessor(t *testing.T) {
	s := newDiskOrderStore(t)
	const key = "https://example.test/page"
	first := setDiskOrderEntry(t, s, key, "first")
	second := setDiskOrderEntry(t, s, key, "second")
	item := s.items[key]
	staged, size, err := s.disk.stage(second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(staged) })
	previous, _, err := s.disk.stage(first)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(previous) })
	if err := s.disk.publish(key, previous); err != nil {
		t.Fatal(err)
	}
	item.staging = true
	setDiskOrderEntry(t, s, "https://example.test/other", "other")
	if got, hit := s.Get(key); hit {
		t.Fatalf("pending item reloaded %q", got.Body)
	}
	if err := s.finishDiskWrite(item, staged, size, nil); err != nil {
		t.Fatal(err)
	}
	if got, hit := s.Get(key); !hit || string(got.Body) != "second" {
		t.Fatalf("published item did not reload: %v", got)
	}
}

// An entry evicted while its disk write is pending is counted at its real file size once
// the write lands, so the disk-only quota still holds.
func TestDiskQuotaCountsFileFinishedAfterEviction(t *testing.T) {
	now := time.Unix(1000, 0)
	probe := newDiskOrderStore(t)
	first := setDiskOrderEntry(t, probe, "https://example.test/first", "first page body")
	fi, err := os.Stat(probe.disk.path(first.Key))
	if err != nil {
		t.Fatal(err)
	}

	s, err := NewStore(StoreOptions{MaxEntries: 1, DiskDir: t.TempDir(), MaxDiskBytes: fi.Size() - 1, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	setDiskOrderEntry(t, s, first.Key, "first page body")
	s.mu.Lock()
	item := s.items[first.Key]
	s.disk.remove(first.Key)
	item.staging, item.file = true, 1 // a write still in flight, with only an estimate counted
	s.mu.Unlock()
	staged, size, err := s.disk.stage(first)
	if err != nil {
		t.Fatal(err)
	}

	second := setDiskOrderEntry(t, s, "https://example.test/second", "second")
	s.mu.Lock()
	stubbed := item.diskElem != nil
	s.mu.Unlock()
	if !stubbed {
		t.Fatal("first entry should be disk-only after the second one evicted it")
	}
	if err := s.finishDiskWrite(item, staged, size, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.disk.path(first.Key)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized disk-only file should be trimmed once its size is known: %v", err)
	}
	s.mu.Lock()
	stub := s.stubBytes
	s.mu.Unlock()
	if stub != 0 {
		t.Fatalf("stubBytes = %d after trimming, want 0", stub)
	}
	if _, ok := s.Get(second.Key); !ok {
		t.Fatal("resident entry lost")
	}
}
