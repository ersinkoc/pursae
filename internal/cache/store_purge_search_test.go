package cache

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestPurgeAllRestoresSearchQuota(t *testing.T) {
	for _, disk := range []bool{false, true} {
		t.Run(fmt.Sprintf("disk=%v", disk), func(t *testing.T) {
			now := time.Unix(1000, 0)
			opts := StoreOptions{MaxEntries: maxSearchEntries + 1, Now: func() time.Time { return now }}
			if disk {
				opts.DiskDir = t.TempDir()
				opts.MaxEntries = 1
				opts.MaxDiskEntries = maxSearchEntries + 1
			}
			s, err := NewStore(opts)
			if err != nil {
				t.Fatal(err)
			}
			put := func(key string) error {
				e, err := NewEntry(key, http.StatusOK, http.Header{}, []byte("page"), nil, now, time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				return s.Set(e)
			}
			count := maxSearchEntries
			if disk {
				// Include resident and disk-only entries without a thousand fsyncs.
				count = 3
			}
			for i := range count {
				if err := put(fmt.Sprintf("https://example.test/?s=q%d#d", i)); err != nil {
					t.Fatal(err)
				}
			}
			if !disk {
				if err := put("https://example.test/?s=full#d"); err != ErrNotCacheable {
					t.Fatalf("full quota: %v", err)
				}
			}
			if n := s.PurgeAll(); n != count {
				t.Fatalf("purged %d entries, want %d", n, count)
			}
			if s.searches != 0 {
				t.Fatalf("empty cache still counts %d searches", s.searches)
			}
			if err := put("https://example.test/?s=after-purge#d"); err != nil {
				t.Fatalf("search quota not restored after PurgeAll: %v", err)
			}
			s.PurgeAll()
			s.PurgeAll()
			if err := put("https://example.test/?s=after-empty-purge#d"); err != nil {
				t.Fatalf("search after repeated purge: %v", err)
			}
		})
	}
}
