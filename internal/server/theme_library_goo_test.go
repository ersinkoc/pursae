package server

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Theme library, Goocommerce store themes: every package validates, carries a store demo whose
// references resolve, installs, activates, imports its demo (the store switches on and gets its
// products, categories, coupons and shipping) and renders the home page, the shop, a product, a
// category and the cart inside the theme.

var gooThemeSlugs = []string{"threadline", "glow-botanica", "voltage-store", "roast-and-ritual", "dog-ear-books",
	"oak-and-form", "peak-athletics", "aurum-and-pearl", "green-basket", "kiln-and-loom"}

func TestGooThemePackages(t *testing.T) {
	funcs := (&templates{s: &Server{}}).funcs()
	for _, slug := range gooThemeSlugs {
		t.Run(slug, func(t *testing.T) {
			dir := filepath.Join(themeLibraryDir, slug)
			if _, err := os.Stat(dir); err != nil {
				t.Fatalf("missing: %v", err)
			}
			files, m, err := unpackThemeZip(zipDir(t, dir))
			if err != nil {
				t.Fatalf("package: %v", err)
			}
			if m.Slug != slug || m.Author != "WordPress Go" {
				t.Fatalf("manifest identity: %s %s", m.Slug, m.Author)
			}
			if _, err := loadThemePackage(dir, slug); err != nil {
				t.Fatalf("load: %v", err)
			}
			if _, err := parsePackageTemplates(os.DirFS(dir), "templates", funcs); err != nil {
				t.Fatalf("templates: %v", err)
			}
			for _, f := range []string{"readme.md", "assets/style.css", "assets/editor-style.css", "assets/screenshot.png", "templates/header.html", "templates/page.html"} {
				if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
					t.Errorf("%s missing", f)
				}
			}
			header, _ := os.ReadFile(filepath.Join(dir, "templates", "header.html"))
			if !strings.Contains(string(header), "goo_cart_link") && !strings.Contains(string(header), "goo_cart_count") {
				t.Error("the header has no cart link (goo_cart_link / goo_cart_count)")
			}
			css, _ := os.ReadFile(filepath.Join(dir, "assets", "style.css"))
			for _, sel := range []string{"ul.products", "span.onsale", "table.shop_table", "woocommerce-MyAccount-navigation", "form.checkout", "single_add_to_cart_button"} {
				if !strings.Contains(string(css), sel) {
					t.Errorf("style.css does not style %s", sel)
				}
			}
			var meta struct{ Category, Style, Demo string }
			raw, _ := os.ReadFile(filepath.Join(dir, "theme.json"))
			_ = json.Unmarshal(raw, &meta)
			if meta.Category != "shop" || meta.Style == "" || len(meta.Style) > 80 || meta.Demo != "demo/demo.json" {
				t.Errorf("library metadata: %+v", meta)
			}
			inPkg := map[string]int{}
			images := 0
			for _, f := range files {
				inPkg[f.Path] = len(f.Data)
				if strings.HasPrefix(f.Path, "demo/images/") {
					images++
					if !strings.HasPrefix(path.Base(f.Path), slug+"-") {
						t.Errorf("%s: demo images are prefixed with the theme slug", f.Path)
					}
					if len(f.Data) > 1<<20 {
						t.Errorf("%s is %d KB (max 1 MiB)", f.Path, len(f.Data)>>10)
					}
				}
			}
			if images < 14 || images > 24 {
				t.Errorf("%d demo images, want 14–24", images)
			}
			// The demo validates as a whole (core sections and the goocommerce section).
			th, err := loadThemePackage(dir, slug)
			if err != nil {
				t.Fatal(err)
			}
			d, err := themeDemoOf(th)
			if err != nil {
				t.Fatalf("demo: %v", err)
			}
			for _, md := range d.Media {
				if _, ok := inPkg[md.File]; !ok {
					t.Errorf("media %s: %s not in the package", md.ID, md.File)
				}
			}
			var store gooDemo
			if err := json.Unmarshal(d.Store, &store); err != nil {
				t.Fatalf("goocommerce section: %v", err)
			}
			if len(store.Products) < 10 || len(store.Categories) < 3 {
				t.Errorf("store demo: %d products, %d categories", len(store.Products), len(store.Categories))
			}
			kinds := map[string]bool{}
			for _, p := range store.Products {
				kinds[defaultStr(p.Type, "simple")] = true
				if p.Image == "" {
					t.Errorf("product %s has no image", p.Slug)
				}
			}
			if !kinds["simple"] || !kinds["variable"] {
				t.Errorf("store demo product types: %v", kinds)
			}
			home := false
			for _, p := range d.Pages {
				if p.FrontPage && strings.Contains(p.Content, "[products") {
					home = true
				}
			}
			if !home {
				t.Error("the front page shows no products")
			}
		})
	}
}

func TestGooThemeInstallAndDemo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WPGO_THEMES_DIR", dir)
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Store themes"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK {
		t.Fatalf("install: %d %s", code, body)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
	nonce := formNonce(t, body)
	ctx := context.Background()
	for _, slug := range gooThemeSlugs {
		t.Run(slug, func(t *testing.T) {
			tdir := filepath.Join(themeLibraryDir, slug)
			code, body, loc := postZip(t, c, base+"/wp-admin/theme-install", nonce, "themezip", slug+".zip", zipDir(t, tdir))
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
			th := themeBySlug(slug)
			rep, err := srv.importThemeDemo(ctx, 1, th)
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if !srv.gooOn() {
				t.Fatal("the demo did not switch the store on")
			}
			products, total, err := srv.gooQueryProducts(ctx, gooProductQuery{PerPage: 50})
			if err != nil || total < 10 {
				t.Fatalf("products after import: %d %v", total, err)
			}
			if rep.Posts < 10 {
				t.Errorf("import report: %+v", rep)
			}
			// A second import adds nothing.
			if _, err := srv.importThemeDemo(ctx, 1, th); err != nil {
				t.Fatal(err)
			}
			if _, again, _ := srv.gooQueryProducts(ctx, gooProductQuery{PerPage: 50}); again != total {
				t.Errorf("re-import duplicated products: %d → %d", total, again)
			}
			pu, _ := url.Parse(srv.gooPermalink(ctx, products[0]))
			for _, u := range []string{"/", "/shop/", pu.Path, "/cart/", "/my-account/"} {
				resp := anonGet(t, base+u)
				page := bodyOf(resp)
				if resp.StatusCode != http.StatusOK || strings.Contains(page, "Template error") {
					t.Errorf("%s: %d", u, resp.StatusCode)
				}
				if !strings.Contains(page, "wp-theme-"+slug) {
					t.Errorf("%s is not rendered by the theme", u)
				}
			}
			if page := bodyOf(anonGet(t, base+"/shop/")); !strings.Contains(page, `class="products`) {
				t.Error("the shop shows no product grid")
			}
			// Remove Demo Content deletes the products and keeps the store usable.
			if err := srv.removeThemeDemo(ctx, slug); err != nil {
				t.Fatal(err)
			}
			if _, left, _ := srv.gooQueryProducts(ctx, gooProductQuery{PerPage: 50}); left != 0 {
				t.Errorf("%d products left after removing the demo", left)
			}
		})
	}
}
