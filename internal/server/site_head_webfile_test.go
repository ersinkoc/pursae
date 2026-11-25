package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"wpgo/internal/config"
	"wpgo/web"
)

// TestWebFileMemo checks webFile's memo: embedded files come back the same from the memo, theme
// paths (which an edited copy can override) and other file systems are always read afresh.
func TestWebFileMemo(t *testing.T) {
	dir := t.TempDir()
	s := &Server{web: overlayFS{base: web.FS(""), dir: dir}}
	const css = "wp-includes/css/global-styles-classic.css"
	raw, err := fs.ReadFile(web.FS(""), css)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(raw))
	for range 2 {
		if got := s.webFile(css); got != want || got == "" {
			t.Fatalf("webFile(%s) = %d bytes, want %d", css, len(got), len(want))
		}
	}
	if s.webFile("wp-includes/css/no-such-file.css") != "" {
		t.Fatal("missing file not empty")
	}

	// A theme file edited in the override directory shows at once, before and after a first read.
	theme := "themes/twentytwentyfive/assets/style.css"
	if s.webFile(theme) == "" {
		t.Fatalf("%s not embedded", theme)
	}
	p := filepath.Join(dir, "twentytwentyfive", "assets", "style.css")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"/* edit 1 */", "/* edit 2 */"} {
		if err := os.WriteFile(p, []byte("\n"+v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := s.webFile(theme); got != v {
			t.Fatalf("theme file after edit = %q, want %q", got, v)
		}
	}

	// Another file system (PURSAE_WEB_DIR, tests) with the same name is not served from the memo.
	other := &Server{web: fstest.MapFS{css: {Data: []byte(" other ")}}}
	for range 2 {
		if got := other.webFile(css); got != "other" {
			t.Fatalf("other fs: webFile = %q", got)
		}
	}
	live := &Server{cfg: config.Config{WebDir: "../../web"}, web: overlayFS{base: web.FS("../../web")}}
	if got := live.webFile(css); got != want {
		t.Fatalf("web dir: webFile = %d bytes, want %d", len(got), len(want))
	}
}

func BenchmarkWebFile(b *testing.B) {
	s := &Server{web: overlayFS{base: web.FS(""), dir: b.TempDir()}}
	b.ReportAllocs()
	for b.Loop() {
		_ = s.webFile("wp-includes/css/dist/block-library/theme.min.css")
		_ = s.webFile("wp-includes/css/classic-themes.min.css")
		_ = s.webFile("wp-includes/css/global-styles-classic.css")
	}
}
