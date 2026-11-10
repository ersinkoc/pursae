package cache

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mkEntry(t *testing.T, key string, size int, tags ...string) *Entry {
	t.Helper()
	h := http.Header{"Content-Type": {"text/html"}, "Content-Length": {"1"}}
	e, err := NewEntry(key, 200, h, []byte(strings.Repeat("x", size)), tags, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestNewEntryFilters(t *testing.T) {
	e := mkEntry(t, "http://a/#d", 1000, "b", "a", "a", " ")
	if e.Header.Get("Content-Length") != "" || e.Header.Get("Content-Type") != "text/html" {
		t.Fatalf("header %v", e.Header)
	}
	if fmt.Sprint(e.Tags) != "[a b]" {
		t.Fatalf("tags %v", e.Tags)
	}
	if e.Gzip == nil || !strings.HasPrefix(e.ETag, `"`) {
		t.Fatal("gzip/etag not computed")
	}
	_, err := NewEntry("k", 200, http.Header{"Set-Cookie": {"a=b"}}, nil, nil, time.Now(), time.Hour)
	if err != ErrNotCacheable {
		t.Fatalf("Set-Cookie entry: %v", err)
	}
}

func TestLRUEvictionByBytes(t *testing.T) {
	probe := mkEntry(t, "http://h/0#d", 1000)
	s, _ := NewStore(StoreOptions{MaxBytes: probe.size()*3 + 10})
	for i := range 3 {
		s.Set(mkEntry(t, fmt.Sprintf("http://h/%d#d", i), 1000))
	}
	s.Get("http://h/0#d") // 0 becomes most recently used; 1 is now the LRU
	s.Set(mkEntry(t, "http://h/3#d", 1000))
	if _, ok := s.Get("http://h/1#d"); ok {
		t.Fatal("LRU entry not evicted")
	}
	for _, k := range []string{"http://h/0#d", "http://h/2#d", "http://h/3#d"} {
		if _, ok := s.Get(k); !ok {
			t.Fatalf("%s evicted", k)
		}
	}
	if st := s.Stats(); st.Bytes > probe.size()*3+10 || st.Entries != 3 {
		t.Fatalf("stats %+v", st)
	}
	// An entry bigger than the whole budget is refused.
	if err := s.Set(mkEntry(t, "http://h/huge#d", 10000)); err == nil {
		t.Fatal("oversized entry accepted")
	}
}

func TestLRUEvictionByEntries(t *testing.T) {
	s, _ := NewStore(StoreOptions{MaxEntries: 2})
	s.Set(mkEntry(t, "http://h/a#d", 10, "t"))
	s.Set(mkEntry(t, "http://h/b#d", 10, "t"))
	s.Set(mkEntry(t, "http://h/c#d", 10, "t"))
	if _, ok := s.Get("http://h/a#d"); ok {
		t.Fatal("a not evicted")
	}
	if st := s.Stats(); st.Entries != 2 || st.Tags["t"] != 2 {
		t.Fatalf("stats %+v", st)
	}
}

func TestStatsHitRatio(t *testing.T) {
	s, _ := NewStore(StoreOptions{})
	s.Set(mkEntry(t, "k", 1))
	s.Get("k")
	s.Get("k")
	s.Get("nope")
	st := s.Stats()
	if st.Hits != 2 || st.Misses != 1 || st.HitRatio < 0.66 || st.HitRatio > 0.67 {
		t.Fatalf("stats %+v", st)
	}
}

func TestDiskPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s1, err := NewStore(StoreOptions{DiskDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	e := mkEntry(t, "https://example.com/p/#d", 2000, "post:1", "home")
	e.Header.Set("X-Custom", "v")
	s1.Set(e)
	s1.Set(mkEntry(t, "https://example.com/q/#d", 10, "post:2"))
	if files, _ := filepath.Glob(filepath.Join(dir, "*"+diskExt)); len(files) != 2 {
		t.Fatalf("files on disk: %v", files)
	}

	// "Restart": a new store indexes metadata only and loads bodies lazily.
	s2, err := NewStore(StoreOptions{DiskDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if st := s2.Stats(); st.Entries != 0 || st.DiskEntries != 2 || st.Tags["post:1"] != 1 {
		t.Fatalf("after restart: %+v", st)
	}
	got, ok := s2.Get("https://example.com/p/#d")
	if !ok {
		t.Fatal("persisted entry not found")
	}
	if string(got.Body) != string(e.Body) || string(got.Gzip) != string(e.Gzip) || got.ETag != e.ETag ||
		got.Header.Get("X-Custom") != "v" || got.Status != 200 || !got.Expires.Equal(e.Expires) {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if st := s2.Stats(); st.Entries != 1 || st.DiskEntries != 1 {
		t.Fatalf("after lazy load: %+v", st)
	}
	// Tag purges reach entries that were never loaded, and remove their files.
	if n := s2.PurgeTags("post:2"); n != 1 {
		t.Fatalf("purge disk-only: %d", n)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*"+diskExt)); len(files) != 1 {
		t.Fatalf("files after purge: %v", files)
	}
	s2.PurgeAll()
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Fatalf("files after PurgeAll: %v", files)
	}
}

func TestDiskEvictionKeepsStub(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(StoreOptions{DiskDir: dir, MaxEntries: 1})
	s.Set(mkEntry(t, "http://h/a#d", 10, "a"))
	s.Set(mkEntry(t, "http://h/b#d", 10, "b"))
	if st := s.Stats(); st.Entries != 1 || st.DiskEntries != 1 {
		t.Fatalf("stats %+v", st)
	}
	if _, ok := s.Get("http://h/a#d"); !ok {
		t.Fatal("evicted entry not reloaded from disk")
	}
}

func TestDiskSkipsExpiredAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(StoreOptions{DiskDir: dir})
	e := mkEntry(t, "http://h/old#d", 10)
	e.Expires = time.Now().Add(-time.Second)
	s.disk.save(e)
	os.WriteFile(filepath.Join(dir, "junk"+diskExt), []byte("garbage"), 0o600)
	s2, err := NewStore(StoreOptions{DiskDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if st := s2.Stats(); st.DiskEntries != 0 {
		t.Fatalf("stats %+v", st)
	}
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Fatalf("stale files kept: %v", files)
	}
}

func TestPurgeURLWithHost(t *testing.T) {
	s, _ := NewStore(StoreOptions{})
	s.Set(mkEntry(t, "https://a.com/p/#d", 1))
	s.Set(mkEntry(t, "https://a.com/p/?paged=2#m", 1))
	s.Set(mkEntry(t, "https://b.com/p/#d", 1))
	if n := s.PurgeURL("http://a.com/p/"); n != 2 {
		t.Fatalf("purged %d", n)
	}
	if _, ok := s.Get("https://b.com/p/#d"); !ok {
		t.Fatal("other host purged")
	}
}

func TestWildcard(t *testing.T) {
	for _, tc := range []struct {
		pat, s string
		want   bool
	}{
		{"/shop/*/cart", "/shop/x/y/cart", true},
		{"/shop/*/cart", "/shop/x/cart/z", false},
		{"*.xml", "/sitemap.xml", true},
		{"/wp-admin", "/wp-admin/edit", true},
		{"/a*", "/abc", true},
		{"/a*b*c", "/aXbYc", true},
	} {
		if got := matchPrefixOrWildcard(tc.pat, tc.s); got != tc.want {
			t.Errorf("%q ~ %q = %v", tc.pat, tc.s, got)
		}
	}
}

// Disk persistence is bounded too: arbitrary query values (?s=, ?page=) must not grow the files and the
// in-memory index without limit; the oldest disk-only entries go first.
func TestDiskEntriesBounded(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(StoreOptions{DiskDir: dir, MaxEntries: 2, MaxDiskEntries: 3})
	for i := 0; i < 20; i++ {
		if err := s.Set(mkEntry(t, fmt.Sprintf("http://h/?s=%d#d", i), 10, "search")); err != nil {
			t.Fatal(err)
		}
	}
	st := s.Stats()
	if st.Entries != 2 || st.DiskEntries != 3 || st.Tags["search"] != 5 {
		t.Fatalf("stats %+v", st)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 5 {
		t.Fatalf("%d files on disk, want 5", len(files))
	}
	if _, ok := s.Get("http://h/?s=0#d"); ok {
		t.Error("the oldest entry survived the disk bound")
	}
	if _, ok := s.Get("http://h/?s=17#d"); !ok {
		t.Error("a recent disk-only entry was dropped")
	}
	// After a restart the scan applies the same bound.
	s2, err := NewStore(StoreOptions{DiskDir: dir, MaxEntries: 2, MaxDiskEntries: 2})
	if err != nil {
		t.Fatal(err)
	}
	if st := s2.Stats(); st.DiskEntries != 2 {
		t.Fatalf("after restart: %+v", st)
	}
	if files, _ := os.ReadDir(dir); len(files) != 2 {
		t.Fatalf("after restart: %d files", len(files))
	}
}

// The disk-only entries are bounded by bytes too, not only by count: a few large pages must not
// fill the disk while staying under the entry count.
func TestDiskBytesBounded(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(StoreOptions{DiskDir: dir, MaxEntries: 1, MaxDiskEntries: 1000, MaxDiskBytes: 3 * 2048})
	for i := 0; i < 20; i++ {
		if err := s.Set(mkEntry(t, fmt.Sprintf("http://h/big/%d#d", i), 2000, "big")); err != nil {
			t.Fatal(err)
		}
	}
	if st := s.Stats(); st.DiskEntries > 3 || st.DiskEntries == 0 {
		t.Fatalf("%d disk-only entries kept, want 1 to 3: %+v", st.DiskEntries, st)
	}
	if files, _ := os.ReadDir(dir); len(files) > 4 {
		t.Fatalf("%d files on disk", len(files))
	}
	// After a restart the scan applies the bound by file size.
	s2, err := NewStore(StoreOptions{DiskDir: dir, MaxEntries: 1, MaxDiskEntries: 1000, MaxDiskBytes: 2048})
	if err != nil {
		t.Fatal(err)
	}
	if st := s2.Stats(); st.DiskEntries > 1 {
		t.Fatalf("after restart: %+v", st)
	}
}
