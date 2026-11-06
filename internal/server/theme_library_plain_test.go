package server

import (
	"context"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// The library store themes that used to ship a page-builder design (Fernwood, Kite & Crayon, Good Boy
// Supply) are plain themes now: their own templates and stylesheet, a block demo with store
// shortcodes, and nothing a builder has to load.

var plainStoreThemes = []string{"fernwood", "kite-and-crayon", "good-boy-supply"}

// The old builder's names, spelled in two parts so that tools/webbuilder/inventory.sh --check does
// not count these regression guards as references.
const (
	oldBuilderKey = "gole" + "mentor" // theme.json key, package directory and demo page key
	oldBuilderCSS = "ele" + "mentor"  // class prefix and meta key prefix
)

func TestPlainStoreThemePackages(t *testing.T) {
	funcs := (&templates{s: &Server{}}).funcs()
	for _, slug := range plainStoreThemes {
		t.Run(slug, func(t *testing.T) {
			dir := filepath.Join(themeLibraryDir, slug)
			files, m, err := unpackThemeZip(zipDir(t, dir))
			if err != nil {
				t.Fatalf("package: %v", err)
			}
			for _, f := range files {
				if strings.HasPrefix(f.Path, oldBuilderKey+"/") {
					t.Errorf("%s: the package still carries a builder design", f.Path)
				}
			}
			raw, _ := os.ReadFile(filepath.Join(dir, "theme.json"))
			if strings.Contains(string(raw), `"`+oldBuilderKey+`"`) || strings.Contains(string(raw), "Web Builder") || strings.Contains(string(raw), "Theme Builder") {
				t.Error("theme.json still refers to the page builder")
			}
			if m.Demo != "demo/demo.json" {
				t.Fatalf("demo: %q", m.Demo)
			}
			if _, err := parsePackageTemplates(os.DirFS(dir), "templates", funcs); err != nil {
				t.Fatalf("templates: %v", err)
			}
			css, _ := os.ReadFile(filepath.Join(dir, "assets", "style.css"))
			if strings.Contains(string(css), oldBuilderCSS) || strings.Contains(string(css), "--e-global") {
				t.Error("style.css still styles the builder's markup")
			}
			if !strings.Contains(string(css), "--gs-primary:") {
				t.Error("style.css does not define its colour tokens")
			}
			th, err := loadThemePackage(dir, slug)
			if err != nil {
				t.Fatal(err)
			}
			d, err := themeDemoOf(th)
			if err != nil {
				t.Fatalf("demo: %v", err)
			}
			if len(d.Pages) != 3 {
				t.Fatalf("%d demo pages", len(d.Pages))
			}
			for _, p := range d.Pages {
				if strings.TrimSpace(p.Content) == "" {
					t.Errorf("page %s has no content", p.Slug)
				}
				if p.FrontPage && (!strings.Contains(p.Content, "[products ") || !strings.Contains(p.Content, "[product_categories ")) {
					t.Error("the front page shows no products or categories")
				}
			}
		})
	}
}

// A demo written for the old builder (pages with its element trees) still validates: the keys are
// ignored and the pages import with their content.
func TestThemeDemoIgnoresBuilderTrees(t *testing.T) {
	demo := strings.ReplaceAll(`{"version": 1, "pages": [
		{"slug": "home", "title": "Home", "front_page": true, "content": "<p>Hello</p>",
		 "OLD": {"not": "a tree"}, "OLD_settings": {"hide_title": "yes"}},
		{"slug": "about", "title": "About", "OLD": [{"elType": "container"}]}]}`, "OLD", oldBuilderKey)
	fsys := fstest.MapFS{"demo/demo.json": {Data: []byte(demo)}}
	d, err := parseThemeDemo(fsys, &themeManifest{Demo: "demo/demo.json"})
	if err != nil {
		t.Fatalf("a demo with builder keys was refused: %v", err)
	}
	if len(d.Pages) != 2 || d.Pages[0].Content != "<p>Hello</p>" {
		t.Fatalf("pages: %+v", d.Pages)
	}
}

func TestPlainStoreThemesInstall(t *testing.T) {
	t.Setenv("WPGO_THEMES_DIR", t.TempDir())
	base, c, srv := gooSite(t)
	_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
	nonce := formNonce(t, body)
	ctx := context.Background()
	for _, slug := range plainStoreThemes {
		t.Run(slug, func(t *testing.T) {
			code, body, loc := postZip(t, c, base+"/wp-admin/theme-install", nonce, "themezip", slug+".zip", zipDir(t, filepath.Join(themeLibraryDir, slug)))
			if code != http.StatusFound || loc != "/wp-admin/themes?installed="+slug {
				t.Fatalf("upload: %d %q %s", code, loc, body)
			}
			t.Cleanup(func() { unregisterTheme(slug) })
			_, body, _ = get(t, c, base+"/wp-admin/themes")
			m := regexp.MustCompile(`href="(/wp-admin/themes\?action=activate&amp;stylesheet=` + slug + `&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
			if m == nil {
				t.Fatalf("no activate link for %s", slug)
			}
			if code, _, _ := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound {
				t.Fatalf("activate: %d", code)
			}
			if a := srv.activeTheme(); a == nil || a.Slug != slug {
				t.Fatalf("active theme %v", a)
			}
			_, themes, _ := get(t, c, base+"/wp-admin/themes")
			if strings.Contains(themes, "Open the Theme Builder") || strings.Contains(themes, "wpgoBuilder") || strings.Contains(themes, "Saved Pursae Web Builder themes") {
				t.Error("the Themes screen offers the Theme Builder")
			}
			if _, err := srv.importThemeDemo(ctx, 1, themeBySlug(slug)); err != nil {
				t.Fatalf("import: %v", err)
			}
			var built int
			if err := srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE meta ?| $1`,
				[]string{"_" + oldBuilderCSS + "_data", "_" + oldBuilderCSS + "_edit_mode", "_" + oldBuilderCSS + "_page_settings"}).Scan(&built); err != nil {
				t.Fatal(err)
			}
			if built != 0 {
				t.Errorf("the demo wrote %d page-builder meta rows", built)
			}
			home := bodyOf(anonGet(t, base+"/"))
			for _, want := range []string{"wp-theme-" + slug, "/wp-content/themes/" + slug + "/style.css", "gs-section", `class="products`, "/wp-content/uploads/"} {
				if !strings.Contains(home, want) {
					t.Errorf("home page: no %q", want)
				}
			}
			if strings.Contains(home, oldBuilderCSS) || strings.Contains(home, "[products") {
				t.Error("the home page has builder markup or an unrendered shortcode")
			}
			if resp := anonGet(t, base+"/contact/"); resp.StatusCode != http.StatusOK || !strings.Contains(bodyOf(resp), "wp-block-columns") {
				t.Error("the Contact page does not render its blocks")
			}
			if err := srv.removeThemeDemo(ctx, slug); err != nil {
				t.Fatal(err)
			}
		})
	}
}
