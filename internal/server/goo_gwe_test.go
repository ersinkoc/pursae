package server

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
)

// The Golementor store themes of the library: valid packages with a valid design and demo.
var gooGweThemes = []struct{ slug, product, variable, cat string }{
	{"fernwood", "fiddle-leaf-fig", "monstera-deliciosa", "houseplants"},
	{"good-boy-supply", "cat-tree", "orthopedic-dog-bed", "dogs"},
	{"kite-and-crayon", "world-puzzle", "building-blocks", "toys"},
}

func TestGooGolementorThemePackages(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)
	for _, tc := range gooGweThemes {
		lt := srv.libraryTheme(tc.slug)
		if lt == nil {
			t.Fatalf("%s is not in the library", tc.slug)
		}
		m := lt.Manifest
		if m.Golementor != "golementor/design.json" || !gooIsStoreManifest(m) || m.Demo == "" {
			t.Errorf("%s: manifest %+v", tc.slug, m)
		}
		snap, err := parseGweDesign(srv.themeLibrary, lt.Dir+"/"+m.Golementor)
		if err != nil {
			t.Fatalf("%s: %v", tc.slug, err)
		}
		types := map[string]int{}
		for _, p := range snap.Parts {
			types[p.Type]++
		}
		if types["header"] != 1 || types["footer"] != 1 || types["single-product"] != 1 || types["product-archive"] != 1 || types["single-page"] != 3 {
			t.Errorf("%s parts: %v", tc.slug, types)
		}
		if len(snap.Kit["system_colors"].([]any)) != 4 {
			t.Errorf("%s: the kit has no global colors", tc.slug)
		}
	}
	// A design that is not one is refused.
	dir := t.TempDir()
	_ = os.WriteFile(dir+"/d.json", []byte(`{"format":"other","parts":[]}`), 0o644)
	if _, err := parseGweDesign(os.DirFS(dir), "d.json"); err == nil {
		t.Error("a design of another format was accepted")
	}
	_ = os.WriteFile(dir+"/d.json", []byte(`{"format":"gowebeditor-theme","parts":[{"type":"bogus","elements":[]}]}`), 0o644)
	if _, err := parseGweDesign(os.DirFS(dir), "d.json"); err == nil {
		t.Error("an unknown part type was accepted")
	}
}

func TestGooGolementorThemesInstall(t *testing.T) {
	for _, tc := range gooGweThemes {
		t.Run(tc.slug, func(t *testing.T) {
			base, c, srv := gooSite(t)
			ctx := context.Background()
			_, body, _ := get(t, c, base+"/wp-admin/goocommerce?page=themes")
			if !strings.Contains(body, `id="goo-theme-`+tc.slug+`"`) {
				t.Fatal("the theme is not on the Store Themes screen")
			}
			nonce := formNonce(t, body)
			for _, step := range gooThemeSteps {
				gooThemeStepPost(t, c, base, nonce, tc.slug, step)
			}
			if a := srv.activeTheme(); a == nil || a.Slug != tc.slug {
				t.Fatalf("active theme %v", a)
			}
			if !srv.golementorOn() {
				t.Fatal("Golementor was not switched on")
			}
			parts, _ := srv.gwePartPosts(ctx, "", "publish")
			if len(parts) != 7 {
				t.Fatalf("%d published theme parts, want 7", len(parts))
			}
			if srv.settings.String("gwe_loaded_theme") != tc.slug || srv.gweWorkingCopyDirty(ctx) {
				t.Error("the working copy is not the theme's design")
			}

			v := gooFrontClient()
			code, home, _ := get(t, v, base+"/")
			if code != http.StatusOK {
				t.Fatalf("home: %d", code)
			}
			gooMustContain(t, "home", home, "elementor-location-header", "elementor-location-footer", "goo-menu-cart",
				"elementor-widget-woocommerce-products", "elementor-widget-wc-categories", `/wp-content/themes/`+tc.slug+`/style.css`,
				"goocommerce-elements.css", `class="products columns-4"`)
			code, shop, _ := get(t, v, base+"/shop/")
			if code != http.StatusOK {
				t.Fatalf("shop: %d", code)
			}
			gooMustContain(t, "shop", shop, "elementor-widget-wc-archive-products", "woocommerce-result-count", "elementor-location-product-archive",
				"woocommerce-breadcrumb")
			if _, cat, _ := get(t, v, base+"/product-category/"+tc.cat+"/"); !strings.Contains(cat, "elementor-location-product-archive") {
				t.Error("the category archive does not use the Products Archive part")
			}
			code, prod, _ := get(t, v, base+"/product/"+tc.variable+"/")
			if code != http.StatusOK {
				t.Fatalf("product: %d", code)
			}
			gooMustContain(t, "product", prod, "elementor-location-single-product", `<div id="product-`, "woocommerce-product-gallery",
				`<form class="variations_form cart"`, "elementor-widget-woocommerce-product-data-tabs", "related products", `<p class="price">`)

			// Cart and checkout through their theme parts.
			if code, _, _ := get(t, v, base+"/shop/?add-to-cart="+gooProductID(t, srv, tc.product)); code != http.StatusFound {
				t.Fatalf("add to cart: %d", code)
			}
			_, cart, _ := get(t, v, base+"/cart/")
			gooMustContain(t, "cart", cart, "elementor-widget-woocommerce-cart", `<form class="woocommerce-cart-form"`, `class="goo-cart-count">1<`)
			_, co, _ := get(t, v, base+"/checkout/")
			gooMustContain(t, "checkout", co, "elementor-widget-woocommerce-checkout-page", `name="billing_first_name"`, "Place order")
			_, acct, _ := get(t, v, base+"/my-account/")
			gooMustContain(t, "my account", acct, "elementor-widget-woocommerce-my-account")
			// The pages are Golementor documents with their images.
			_, about, _ := get(t, v, base+"/about/")
			if !strings.Contains(about, `data-elementor-type="wp-page"`) || !strings.Contains(about, "/wp-content/uploads/") {
				t.Error("the About page is not a Golementor page with its image")
			}
		})
	}
}

// gooProductID is the id of a product by slug, as a string.
func gooProductID(t *testing.T, srv *Server, slug string) string {
	t.Helper()
	var id int64
	if err := srv.store.Pool.QueryRow(context.Background(), `SELECT id FROM posts WHERE type = 'product' AND slug = $1`, slug).Scan(&id); err != nil {
		t.Fatalf("product %s: %v", slug, err)
	}
	return itoa64(id)
}
