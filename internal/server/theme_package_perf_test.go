package server

import (
	"context"
	"html"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// copyThemeDir copies an example package to dst/<slug> with theme.json rewritten by edit.
func copyThemeDir(t *testing.T, src, dst string, edit func(string) string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if rel == "theme.json" {
			b = []byte(edit(string(b)))
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// perfPackageSite installs a site whose active theme is a copy of the starter package with a
// preloaded font and per-block styles, and Pursae Cache on with Defer JavaScript and Preload Fonts.
func perfPackageSite(t *testing.T) (string, *http.Client, *Server, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("WPGO_THEMES_DIR", dir)
	pkgDir := filepath.Join(dir, "starter")
	copyThemeDir(t, starterDir, pkgDir, func(s string) string {
		s = strings.Replace(s, `"preload_fonts": [],`, `"preload_fonts": ["assets/fonts/body.woff2"],`+"\n  \"block_styles\": true,", 1)
		return s
	})
	if err := os.MkdirAll(filepath.Join(pkgDir, "assets", "fonts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "assets", "fonts", "body.woff2"), []byte("wOF2 test font"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts, c, srv := testServerBuilderOff(t)
	t.Cleanup(func() { unregisterTheme("starter") })
	base := ts.URL
	installAndLogin(t, ts, c)
	_, body, _ := get(t, c, base+"/wp-admin/themes")
	m := regexp.MustCompile(`href="(/wp-admin/themes\?action=activate&amp;stylesheet=starter&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no activate link for the starter package")
	}
	if code, _, _ := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound {
		t.Fatalf("activate starter: %d", code)
	}
	if err := srv.settings.Set(context.Background(), map[string]any{"wpgo_cache_enabled": true, "wpgo_opt_defer": true, "wpgo_opt_preload_fonts": true}); err != nil {
		t.Fatal(err)
	}
	srv.configureCache()
	return base, c, srv, pkgDir
}

// Theme packages: per-block core styles, font preloads, deferred scripts and long-lived browser
// caching of versioned assets (the performance report of a site built on a theme package).
func TestThemePackagePerformance(t *testing.T) {
	base, _, srv, pkgDir := perfPackageSite(t)

	first := anonGet(t, base+"/")
	page := bodyOf(first)
	if first.Header.Get("X-WPGo-Cache") != "miss" {
		t.Fatalf("first anonymous visit: X-WPGo-Cache %q", first.Header.Get("X-WPGo-Cache"))
	}

	// 1) "block_styles": true: no render-blocking block library, the used blocks' styles inline.
	if strings.Contains(page, "block-library/style.min.css") || strings.Contains(page, strings.TrimSpace(blockStylesMarker)) {
		t.Error("the package theme still loads the whole block library (block_styles ignored)")
	}
	if th := srv.activeTheme(); th.Slug != "starter" {
		t.Fatalf("active theme %s", th.Slug)
	} else if th.BlockStyles == nil {
		t.Error("the package theme has no BlockStyles")
	}

	// 2) The preload points at the URL the font is served from.
	const font = "/wp-content/themes/starter/fonts/body.woff2"
	if !strings.Contains(page, `<link rel="preload" href="`+font+`" as="font" type="font/woff2" crossorigin>`) {
		t.Errorf("no font preload for %s in\n%s", font, head(page))
	}
	if n := strings.Count(page, `href="`+font+`"`); n != 1 {
		t.Errorf("the font is preloaded %d times", n)
	}
	// Also on a page the cache does not optimize (a bypass cookie): the theme prints the preload.
	bypassed := anonGet(t, base+"/", &http.Cookie{Name: "comment_author_x", Value: "1"})
	if bypassed.Header.Get("X-WPGo-Cache") != "bypass" || !strings.Contains(bodyOf(bypassed), `<link rel="preload" href="`+font+`"`) {
		t.Errorf("no font preload on a bypassed page (X-WPGo-Cache %q)", bypassed.Header.Get("X-WPGo-Cache"))
	}
	if r := anonGet(t, base+font); r.StatusCode != http.StatusOK || bodyOf(r) != "wOF2 test font" {
		t.Errorf("preloaded font: %d", r.StatusCode)
	}

	// 3) Defer JavaScript defers the package's footer script.
	if !regexp.MustCompile(`<script src="/wp-content/themes/starter/js/navigation\.js\?ver=1\.0\.0" id="starter-navigation-js" defer>`).MatchString(page) {
		t.Errorf("the package script is not deferred: %s", regexp.MustCompile(`<script[^>]*starter-navigation-js[^>]*>`).FindString(page))
	}

	// 4) Versioned assets are cached for a year; unversioned ones and edited files revalidate.
	cc := func(u string) string {
		t.Helper()
		r := anonGet(t, base+u)
		if r.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", u, r.StatusCode)
		}
		return r.Header.Get("Cache-Control")
	}
	const long = "public, max-age=31536000, immutable"
	if got := cc("/wp-content/themes/starter/style.css?ver=1.0.0"); got != long {
		t.Errorf("versioned stylesheet: Cache-Control %q, want %q", got, long)
	}
	if got := cc("/wp-content/themes/starter/js/navigation.js?ver=1.0.0"); got != long {
		t.Errorf("versioned script: Cache-Control %q, want %q", got, long)
	}
	for _, u := range []string{"/wp-content/themes/starter/style.css", "/wp-content/themes/starter/style.css?ver=0.9.0", font} {
		if got := cc(u); got != "no-cache" {
			t.Errorf("%s: Cache-Control %q, want no-cache", u, got)
		}
	}
	// An edit in the Theme File Editor keeps the version: that file revalidates from then on.
	gc, _ := gin.CreateTestContext(httptest.NewRecorder())
	gc.Request = httptest.NewRequest(http.MethodPost, "/wp-admin/theme-editor", nil)
	if err := srv.saveThemeFile(gc, "starter", "style.css", "body{color:red}\n"); err != nil {
		t.Fatal(err)
	}
	if got := cc("/wp-content/themes/starter/style.css?ver=1.0.0"); got != "no-cache" {
		t.Errorf("edited stylesheet: Cache-Control %q, want no-cache", got)
	}
	if got := cc("/wp-content/themes/starter/js/navigation.js?ver=1.0.0"); got != long {
		t.Errorf("unedited script after another file's edit: Cache-Control %q", got)
	}
	// The edit survives a restart (the package is loaded again from disk).
	srv.loadThemePackages()
	if got := cc("/wp-content/themes/starter/style.css?ver=1.0.0"); got != "no-cache" {
		t.Errorf("edited stylesheet after a reload: Cache-Control %q, want no-cache", got)
	}
	if _, err := os.Stat(filepath.Join(pkgDir, "assets", "style.css")); err != nil {
		t.Fatal(err)
	}
}

// head returns the <head> of a page for failure messages.
func head(page string) string {
	if i := strings.Index(page, "</head>"); i >= 0 {
		return page[:i]
	}
	return page
}

// Pursae Multi Language: a first visit without the language cookie is cacheable like any anonymous
// request; the cookie is set by a script in the page instead of a Set-Cookie header (Polylang does
// the same when a page cache is active).
func TestGolylangFirstVisitCacheable(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	_, frLink := pllCreatePost(t, c, base, "?lang=fr", "Bonjour cache")
	if err := srv.settings.Set(context.Background(), map[string]any{"wpgo_cache_enabled": true}); err != nil {
		t.Fatal(err)
	}
	srv.configureCache()

	for _, u := range []string{base + "/", frLink} {
		first := anonGet(t, u)
		page := bodyOf(first)
		if first.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", u, first.StatusCode)
		}
		if st := first.Header.Get("X-WPGo-Cache"); st != "miss" {
			t.Errorf("%s first visit: X-WPGo-Cache %q, want miss", u, st)
		}
		if sc := first.Header.Values("Set-Cookie"); len(sc) > 0 {
			t.Errorf("%s first visit sets cookies: %v", u, sc)
		}
		want := "en"
		if u == frLink {
			want = "fr"
		}
		if !strings.Contains(page, `document.cookie="pll_language=`+want+`;`) {
			t.Errorf("%s: no language cookie script for %s", u, want)
		}
		if second := anonGet(t, u); second.Header.Get("X-WPGo-Cache") != "hit" {
			t.Errorf("%s second visit: X-WPGo-Cache %q, want hit", u, second.Header.Get("X-WPGo-Cache"))
		}
	}

	// Without the page cache the cookie is still sent as a header, as before.
	if err := srv.settings.Set(context.Background(), map[string]any{"wpgo_cache_enabled": false}); err != nil {
		t.Fatal(err)
	}
	srv.configureCache()
	r := anonGet(t, frLink)
	if sc := strings.Join(r.Header.Values("Set-Cookie"), "; "); !strings.Contains(sc, "pll_language=fr") {
		t.Errorf("cache off: Set-Cookie %q, want pll_language=fr", sc)
	}
	if strings.Contains(bodyOf(r), `document.cookie="pll_language=`) {
		t.Error("cache off: the cookie script is printed as well")
	}
}

func TestThemeManifestBlockStyles(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  []string // nil: classic behaviour
		bad   bool
	}{
		{`false`, nil, false},
		{`true`, []string{}, false},
		{`["navigation", "post-title"]`, []string{"navigation", "post-title"}, false},
		{`["core/navigation"]`, nil, true},
		{`"yes"`, nil, true},
	} {
		raw := starterManifest(t, map[string]string{`"preload_fonts": [],`: `"preload_fonts": [], "block_styles": ` + tc.value + `,`})
		m, err := parseThemeManifest([]byte(raw))
		if tc.bad {
			if err == nil {
				t.Errorf("block_styles %s accepted", tc.value)
			}
			continue
		}
		if err != nil {
			t.Fatalf("block_styles %s: %v", tc.value, err)
		}
		got := newPackageTheme(m, t.TempDir()).BlockStyles
		if (got == nil) != (tc.want == nil) || strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("block_styles %s: BlockStyles %#v, want %#v", tc.value, got, tc.want)
		}
	}
}
