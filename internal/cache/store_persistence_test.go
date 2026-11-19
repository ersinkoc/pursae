package cache

import (
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

func TestFailedDiskReplacementDoesNotReviveOldEntry(t *testing.T) {
	now := time.Unix(1000, 0)
	opts := StoreOptions{
		DiskDir: t.TempDir(), MaxEntries: 1,
		Now:    func() time.Time { return now },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	s, err := NewStore(opts)
	if err != nil {
		t.Fatal(err)
	}
	put := func(key, body string, created time.Time) {
		t.Helper()
		if err := s.Set(&Entry{Key: key, Status: http.StatusOK, Body: []byte(body), Created: created, Expires: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	const key = "https://example.test/page"
	put(key, "old", now)
	// Inject a failure after the disk write starts. Go's gob time encoding
	// reserves an offset of -1 minute for UTC.
	put(key, "updated", now.In(time.FixedZone("unencodable", -60)))
	if e, hit := s.Get(key); !hit || string(e.Body) != "updated" {
		t.Fatalf("memory replacement missing: %v", e)
	}

	put("https://example.test/other", "other", now)
	if e, hit := s.Get(key); hit {
		t.Errorf("eviction restored a superseded entry: %q", e.Body)
	}
	reopened, err := NewStore(opts)
	if err != nil {
		t.Fatal(err)
	}
	if e, hit := reopened.Get(key); hit {
		t.Errorf("restart restored a superseded entry: %q", e.Body)
	}
}
