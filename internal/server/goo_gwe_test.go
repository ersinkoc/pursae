package server

import (
	"context"
	"html"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
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

// The store conditions pick the Theme Builder parts of store views.
func TestGooGolementorStoreConditions(t *testing.T) {
	product := &siteQuery{Kind: "page", Post: &store.Post{ID: 42, Type: "page"}, Store: "product"}
	cat := &siteQuery{Kind: "page", Post: &store.Post{ID: 7, Type: "page"}, Store: "product_cat", StoreTerm: 7}
	cart := &siteQuery{Kind: "page", Post: &store.Post{ID: 3, Type: "page"}, Store: "cart"}
	page := &siteQuery{Kind: "page", Post: &store.Post{ID: 5, Type: "page"}}
	for _, tc := range []struct {
		rule string
		q    *siteQuery
		want int
	}{
		{"product", product, 40}, {"product/42", product, 55}, {"product/43", product, 0}, {"product", cat, 0},
		{"product_archive", cat, 40}, {"product_archive/product_cat", cat, 45}, {"product_archive/product_cat/7", cat, 55},
		{"product_archive/product_cat/8", cat, 0}, {"product_archive/shop", cat, 0}, {"product_archive", product, 0},
		{"goocommerce/cart", cart, 55}, {"goocommerce/checkout", cart, 0}, {"goocommerce/cart", page, 0},
		{"singular/page", page, 30},
	} {
		if got := gweCondScore(tc.rule, tc.q); got != tc.want {
			t.Errorf("%s on %s: %d, want %d", tc.rule, tc.q.Store, got, tc.want)
		}
	}
	if types := gweLocationTypes("main", product); types[0] != "single-product" {
		t.Errorf("product location types %v", types)
	}
	if types := gweLocationTypes("main", cat); types[0] != "product-archive" {
		t.Errorf("archive location types %v", types)
	}
	// A specific product part beats the general one, a store part beats All Pages.
	parts := []*gwePart{{ID: 1, Type: "single-page", Conditions: []string{"include/singular/page"}},
		{ID: 2, Type: "single-product", Conditions: []string{"include/product"}},
		{ID: 3, Type: "single-product", Conditions: []string{"include/product/42"}}}
	if p := gwePick(parts, "main", product); p == nil || p.ID != 3 {
		t.Errorf("picked %v for the product", p)
	}
	cartParts := []*gwePart{{ID: 1, Type: "single-page", Conditions: []string{"include/singular/page"}},
		{ID: 2, Type: "single-page", Conditions: []string{"include/goocommerce/cart"}}}
	if p := gwePick(cartParts, "main", cart); p == nil || p.ID != 2 {
		t.Errorf("picked %v for the cart", p)
	}
}

// With Golylang: Theme Builder parts get language links; a translated header shows on the pages of
// its language, the language switcher widget lists the languages; a duplicated Golementor page
// keeps its design.
func TestGooGolementorThemeLanguages(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	_, body, _ := get(t, c, base+"/wp-admin/goocommerce?page=themes")
	nonce := formNonce(t, body)
	for _, step := range gooThemeSteps {
		gooThemeStepPost(t, c, base, nonce, "fernwood", step)
	}
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Deutsch", "de_DE", "de", "de")

	// The conditions screen offers the store's rules.
	posts, _ := srv.gwePartPosts(ctx, "", "publish")
	var header *store.Post
	for _, p := range posts {
		if gweDocType(p) == "header" {
			header = p
		}
	}
	if header == nil {
		t.Fatal("no header part")
	}
	_, conds, _ := get(t, c, base+gweThemeBuilderPath+"?action=conditions&post="+itoa64(header.ID))
	gooMustContain(t, "conditions", conds, `value="product"`, `value="goocommerce/checkout"`, "Specific Product Category")

	// Theme Builder: language links; translating the header opens a linked German draft.
	_, tb, _ := get(t, c, base+gweThemeBuilderPath)
	gooMustContain(t, "Theme Builder", tb, "Single Products", "Products Archives", "gwe-tb-langs", "+ DE")
	m := regexp.MustCompile(`href="(` + gweThemeBuilderPath + `\?action=translate&amp;post=` + itoa64(header.ID) + `&amp;new_lang=de&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(tb)
	if m == nil {
		t.Fatal("no translate link for the header")
	}
	code, _, loc := get(t, c, base+html.UnescapeString(m[1]))
	deID := srv.pllGet(ctx, pllPost, header.ID, "de")
	if code != http.StatusFound || deID == 0 || !strings.Contains(loc, "post="+itoa64(deID)) {
		t.Fatalf("translate: %d %s (de %d)", code, loc, deID)
	}
	de, _ := srv.store.GetPost(ctx, deID)
	if de.Status != "draft" || srv.pllObjLang(ctx, pllPost, deID) != "de" || gweDocType(de) != "header" {
		t.Fatalf("German header: %s %s", de.Status, gweDocType(de))
	}
	// Publish it with German announcement text.
	de.Status = "publish"
	de.Meta["_elementor_data"] = strings.Replace(de.Meta["_elementor_data"].(string), "Free delivery on orders over $60", "Kostenloser Versand ab 60 $", 1)
	if err := srv.store.UpdatePost(ctx, de, 1); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)

	// A German product: its page uses the German header, the English one the English header.
	var fig int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM posts WHERE type = 'product' AND slug = 'fiddle-leaf-fig'`).Scan(&fig)
	src, _ := srv.gooGetProduct(ctx, fig)
	gooPllTranslate(t, c, base, srv, src, "de", "Geigenfeige")
	v := gooFrontClient()
	_, deBody, _ := get(t, v, base+"/de/product/geigenfeige/")
	gooMustContain(t, "German product", deBody, "Kostenloser Versand ab 60 $", "elementor-location-single-product", `class="goo-lang-switcher"`,
		`href="`+base+`/product/fiddle-leaf-fig/"`)
	if strings.Contains(deBody, "Free delivery on orders over $60") {
		t.Error("the German page shows the English header")
	}
	_, enBody, _ := get(t, v, base+"/product/fiddle-leaf-fig/")
	if !strings.Contains(enBody, "Free delivery on orders over $60") || strings.Contains(enBody, "Kostenloser Versand") {
		t.Error("the English page does not show the English header")
	}

	// A translation of a Golementor page with Duplicate content keeps the design.
	home := int64(srv.settings.Int("page_on_front"))
	hp, _ := srv.store.GetPost(ctx, home)
	np := &store.Post{Type: "page", Title: "Start", Status: "draft", AuthorID: 1, Meta: map[string]any{}}
	if err := srv.store.InsertPost(ctx, np); err != nil {
		t.Fatal(err)
	}
	srv.pllCopyForTranslation(ctx, hp, np, srv.pllLang(ctx, "de"), true)
	got, _ := srv.store.GetPost(ctx, np.ID)
	if !gweBuilt(got) || got.Meta["_elementor_data"] != hp.Meta["_elementor_data"] {
		t.Error("the duplicated page lost its Golementor design")
	}
	np2 := &store.Post{Type: "page", Title: "Leer", Status: "draft", AuthorID: 1, Meta: map[string]any{}}
	_ = srv.store.InsertPost(ctx, np2)
	srv.pllCopyForTranslation(ctx, hp, np2, srv.pllLang(ctx, "de"), false)
	if got, _ := srv.store.GetPost(ctx, np2.ID); gweBuilt(got) {
		t.Error("a translation without Duplicate content copied the design")
	}
}
