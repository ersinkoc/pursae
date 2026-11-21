package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"wpgo/internal/config"
)

const stockMarkD = "M14 59C13.2 50 12.6 38 13 28C13.5 15.5 22.6 7 34 7C45.6 7 55 16 55 27C55 38.5 45.6 47 34 47L27.5 47C22.5 47 18.6 50.6 14 59Z"

// The login screens animate the mark only when it is the stock Pursae mark: no Hub logo, or a Hub
// SVG with the same path data in another color. Any other Hub logo keeps today's image.
func TestBrandStockLogo(t *testing.T) {
	uploads := t.TempDir()
	s := &Server{cfg: config.Config{UploadsDir: uploads}, web: os.DirFS("../../web")}
	dir := filepath.Join(uploads, "brand")
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		// A new mtime, so the cache sees every rewrite even within the file system's timestamp precision.
		mt := time.Now().Add(time.Duration(len(body)+len(name)) * time.Second)
		_ = os.Chtimes(p, mt, mt)
	}
	resetBrand := func() { _ = os.RemoveAll(dir) }

	if !s.brandStockLogo() {
		t.Fatal("no Hub logo: want the stock mark")
	}

	recolored := `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">
  <path d="M 14,59 C 13.2,50 12.6,38 13,28 C13.5 15.5 22.6 7 34 7C45.6 7 55 16 55 27C55 38.5 45.6 47 34 47L27.5 47C22.5 47 18.6 50.6 14 59Z" fill="none" stroke="#E11D48" stroke-width="4.2"/>
  <ellipse cx="30" cy="24.5" rx="3.1" ry="4.1" fill="#E11D48"/><ellipse cx="41.5" cy="24.5" rx="3.1" ry="4.1" fill="#E11D48"/></svg>`
	write("logo.svg", recolored)
	if !s.brandStockLogo() {
		t.Error("a recolored stock mark from the Hub: want the stock mark")
	}
	write("logo-dark.svg", strings.ReplaceAll(recolored, "#E11D48", "#fff"))
	if !s.brandStockLogo() {
		t.Error("recolored light and dark stock marks: want the stock mark")
	}

	other := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><path d="M0 0H1V1Z"/></svg>`
	write("logo-dark.svg", other)
	if s.brandStockLogo() {
		t.Error("a custom dark logo: want no animated mark")
	}

	resetBrand()
	for name, body := range map[string]string{
		"logo.svg":  other,
		"logo.png":  "\x89PNG\r\n\x1a\n",
		"logo.webp": "RIFF....WEBP",
	} {
		resetBrand()
		write(name, body)
		if s.brandStockLogo() {
			t.Errorf("Hub logo %s: want no animated mark", name)
		}
	}

	// Extra paths (a logo that draws the mark plus a wordmark) are not the stock mark either.
	resetBrand()
	write("logo.svg", `<svg xmlns="http://www.w3.org/2000/svg"><path d="`+stockMarkD+`"/><path d="M0 0H1"/></svg>`)
	if s.brandStockLogo() {
		t.Error("the mark plus another path: want no animated mark")
	}

	resetBrand()
	if !s.brandStockLogo() {
		t.Error("the Hub logo removed: want the stock mark back")
	}
}

func TestSVGMarkPath(t *testing.T) {
	want := normalizePathData(stockMarkD)
	if got := svgMarkPath([]byte(`<svg><path d="` + stockMarkD + `"/></svg>`)); got != want {
		t.Errorf("svgMarkPath = %q, want %q", got, want)
	}
	for _, in := range []string{"", "<html><path d='M0 0'/></html>", "<svg></svg>", "not xml <svg"} {
		if got := svgMarkPath([]byte(in)); got != "" {
			t.Errorf("svgMarkPath(%q) = %q, want empty", in, got)
		}
	}
}

// The login screens draw the stock mark inline, with its nonced script; a custom Hub logo keeps the
// image background and loads no script.
func TestLoginMascotHTML(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	script := regexp.MustCompile(`<script nonce="[^"]+" src="/wp-admin/assets/js/login-mascot\.js\?ver=[^"]+" defer></script>`)
	for _, p := range []string{"/wp-login", "/wp-login?action=lostpassword"} {
		code, body, _ := get(t, asNewClient(), ts.URL+p)
		if code != 200 {
			t.Fatalf("%s: HTTP %d", p, code)
		}
		if !script.MatchString(body) {
			t.Errorf("%s: no nonced login-mascot.js", p)
		}
		if n := strings.Count(body, `<svg class="pursae-mascot"`); n != 2 { // the logo and Pursae Modern's aside mark
			t.Errorf("%s: %d inline mascots, want 2", p, n)
		}
		if !strings.Contains(body, `<a class="pursae-mascot-link" href="`) || !strings.Contains(body, `<span class="screen-reader-text">Powered by Pursae</span>`) {
			t.Errorf("%s: the logo link lost its class or accessible name", p)
		}
		if strings.Contains(body, "pursae-mark-white.svg") {
			t.Errorf("%s: the aside still shows the image mark", p)
		}
	}

	dir := filepath.Join(srv.cfg.UploadsDir, "brand")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, body, _ := get(t, asNewClient(), ts.URL+"/wp-login")
	if strings.Contains(body, "pursae-mascot") || strings.Contains(body, "login-mascot.js") {
		t.Error("custom Hub logo: the login page still draws the animated mark")
	}
	if !strings.Contains(body, `<a href="`+ts.URL+`/">Powered by Pursae</a>`) && !strings.Contains(body, `">Powered by Pursae</a></h1>`) {
		t.Error("custom Hub logo: the logo link is gone")
	}
	if !strings.Contains(body, "pursae-mark-white.svg") {
		t.Error("custom Hub logo: the aside lost its image mark")
	}
}
