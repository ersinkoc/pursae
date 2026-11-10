package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The variant cache is trimmed oldest first until it fits the limit.
func TestGoCDNPruneDir(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i, name := range []string{"old", "mid", "new"} {
		p := filepath.Join(dir, name[:1], name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := now.Add(time.Duration(i-3) * time.Hour)
		_ = os.Chtimes(p, mt, mt)
	}
	if n := gocdnPruneDir(dir, 1000); n != 0 {
		t.Fatalf("removed %d under the limit", n)
	}
	if n := gocdnPruneDir(dir, 150); n != 2 {
		t.Fatalf("removed %d, want 2", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "n", "new")); err != nil {
		t.Fatal("the newest file was removed")
	}
}

func TestGoCDNSanitizeImageSettings(t *testing.T) {
	st := gocdnSanitize(gocdnSettings{ImgCDNURL: " https://i.example.com/ ", ImgCacheMaxMB: -5})
	if st.ImgCDNURL != "https://i.example.com" || st.ImgCacheMaxMB != 0 {
		t.Fatalf("%+v", st)
	}
	if gocdnDefaults().ImgCacheMaxMB != 2048 {
		t.Fatal("default cache limit")
	}
}
