package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wpgo/internal/cache"
	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Pursae Commerce widgets of the Web Builder (contract §11.12) and the store-view guard.

func wbShopLayout(items string) string {
	return `{"sections":[{"id":"s1","kind":"layout","name":"","hidden":false,"content":{},"items":[` + items + `],"style":{"palette":"light","align":"left","padding":0,"titleSize":0,"hideOn":[]}}],"site":{}}`
}

// wbShopStart is a store with the builder on and Hello Pursae active.
func wbShopStart(t *testing.T) (string, *http.Client, *Server, *gooCatalogFixture) {
	t.Helper()
	base, admin, srv := gooSite(t)
	f := gooFrontCatalog(t, srv)
	if err := srv.wbSetActive(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	return base, admin, srv, f
}

// wbShopAdminCtx is a gin context of the administrator (preview objects need read_post).
func wbShopAdminCtx(t *testing.T, srv *Server) *gin.Context {
	t.Helper()
	admin, err := srv.store.UserByID(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	c := gooTestCtx()
	c.Set(ctxUser, admin)
	return c
}

func TestWBAddTagsOnce(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Header(cache.HeaderTags, "goo:shop,post:3")
	wbAddTags(c, "goo:shop", "wb-theme")
	if got := w.Header().Get(cache.HeaderTags); got != "goo:shop,post:3,wb-theme" {
		t.Errorf("tags %q", got)
	}
}

// A product category whose term id equals a builder page's id shows the product grid, never that
// page (draft) snapshot; the shop and the cart keep the store markup when their page has a doc.
func TestWBShopStoreViewGuard(t *testing.T) {
	base, _, srv, f := wbShopStart(t)
	ctx := context.Background()
	leak := &store.Post{Type: "page", Title: "Secret", Slug: "secret", Status: "draft", AuthorID: 1, Content: "<p>secret</p>"}
	if err := srv.store.InsertPost(ctx, leak); err != nil {
		t.Fatal(err)
	}
	wbtfPublish(t, srv, leak.ID, `<p>LEAK-MARK</p>`, "", "", nil)
	var maxTerm int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT coalesce(max(id),0) FROM terms`).Scan(&maxTerm)
	if maxTerm >= leak.ID {
		t.Skipf("term ids (%d) already pass the page id (%d)", maxTerm, leak.ID)
	}
	if _, err := srv.store.Pool.Exec(ctx, `SELECT setval(pg_get_serial_sequence('terms','id'), $1)`, leak.ID-1); err != nil {
		t.Fatal(err)
	}
	shoes := &store.Term{Taxonomy: "product_cat", Name: "Shoes", Slug: "shoes"}
	if err := srv.store.InsertTerm(ctx, shoes); err != nil {
		t.Fatal(err)
	}
	if shoes.ID != leak.ID {
		t.Fatalf("term id %d, page id %d", shoes.ID, leak.ID)
	}
	if err := srv.store.SetPostTerms(ctx, f.mug.ID, "product_cat", []int64{shoes.ID}); err != nil {
		t.Fatal(err)
	}
	v := gooFrontClient()
	code, body, _ := get(t, v, base+"/product-category/shoes/")
	if code != http.StatusOK {
		t.Fatalf("category: %d", code)
	}
	wbtfWant(t, "category", body, `<ul class="products`, `woocommerce-loop-product__title">Mug</h2>`)
	wbtfBad(t, "category", body, "LEAK-MARK")

	// The shop and cart pages designed in the builder (no store widget): the store stays.
	for _, key := range []string{"shop", "cart"} {
		id := srv.gooPageIDIn(ctx, key)
		if id <= 0 {
			t.Fatalf("no %s page", key)
		}
		wbtfPublish(t, srv, id, `<p>`+strings.ToUpper(key)+`-DOC-MARK</p>`, "", "", nil)
	}
	_, body, _ = get(t, v, base+"/shop/")
	wbtfWant(t, "shop", body, `<ul class="products`)
	wbtfBad(t, "shop", body, "SHOP-DOC-MARK")
	_, body, _ = get(t, v, base+"/cart/")
	wbtfWant(t, "cart", body, `cart-empty`)
	wbtfBad(t, "cart", body, "CART-DOC-MARK")

	// A Cart page doc with the cart widget keeps its design and the cart.
	cartID := srv.gooPageIDIn(ctx, "cart")
	wbtfPublish(t, srv, cartID, `<p>CART-DOC-MARK</p><div class="wb-shop" data-wb-slot="shop.cart-page" data-wb-item="cp"></div>`,
		wbShopLayout(`{"id":"cp","title":"shop-cart-page","text":""}`), "", nil)
	_, body, _ = get(t, v, base+"/cart/")
	wbtfWant(t, "cart with widget", body, "CART-DOC-MARK", `cart-empty`)
}

// Single Product and Product Archive templates render on their store views only; generic main
// templates never take a store view.
func TestWBShopTemplates(t *testing.T) {
	base, _, srv, f := wbShopStart(t)
	wbtfTemplate(t, srv, webbuilder.TemplateSinglePage, "Pages", []string{"include/general"}, `<p>PAGE-TPL-MARK</p>`, "", "", nil)
	wbtfTemplate(t, srv, webbuilder.TemplateSingleProduct, "Product", webbuilder.DefaultConditions[webbuilder.TemplateSingleProduct],
		`<section>SP-MARK<h1 data-wb-slot="product.title" data-wb-item="t1"></h1><p class="price wb-product-price" data-wb-slot="product.price" data-wb-item="t2"></p>`+
			`<span class="onsale" data-wb-if="product.on-sale" data-wb-slot="product.on-sale" data-wb-item="t3"></span>`+
			`<div data-wb-slot="product.add-to-cart" data-wb-item="t4"></div><div data-wb-slot="shop.notices" data-wb-item="t5"></div></section>`,
		wbShopLayout(`{"id":"t1","title":"product-title","text":""},{"id":"t2","title":"product-price","text":""},{"id":"t3","title":"product-sale-badge","text":""},{"id":"t4","title":"product-add-to-cart","text":""},{"id":"t5","title":"shop-notices","text":""}`), "", nil)
	wbtfTemplate(t, srv, webbuilder.TemplateProductArchive, "Products", webbuilder.DefaultConditions[webbuilder.TemplateProductArchive],
		`<section>PA-MARK<h1 data-wb-slot="shop.archive-title" data-wb-item="a1"></h1><div data-wb-slot="shop.archive-desc" data-wb-item="a2"></div><div data-wb-slot="shop.archive" data-wb-item="a3"></div></section>`,
		wbShopLayout(`{"id":"a1","title":"shop-archive-title","text":""},{"id":"a2","title":"shop-archive-desc","text":""},{"id":"a3","title":"shop-archive-products","text":""}`), "", nil)
	v := gooFrontClient()

	code, body, _ := get(t, v, base+"/product/shirt/")
	if code != http.StatusOK {
		t.Fatalf("product: %d", code)
	}
	wbtfWant(t, "product", body, "SP-MARK", `data-wb-item="t1">Shirt</h1>`, `id="product-`+itoa64(f.shirt.ID)+`"`, `single_add_to_cart_button`,
		`>Sale!</span>`, `<del aria-hidden="true">`, `goocommerce.css`, `goo-add-to-cart-variation-js`)
	wbtfBad(t, "product", body, "PAGE-TPL-MARK", "PA-MARK")

	code, body, _ = get(t, v, base+"/shop/")
	if code != http.StatusOK {
		t.Fatalf("shop: %d", code)
	}
	wbtfWant(t, "shop", body, "PA-MARK", `woocommerce-loop-product__title">Shirt</h2>`)
	if n := strings.Count(body, `<ul class="products`); n != 1 {
		t.Errorf("shop: %d product lists, want 1", n)
	}
	wbtfBad(t, "shop", body, "SP-MARK", "PAGE-TPL-MARK")

	_, body, _ = get(t, v, base+"/product-category/clothing/")
	wbtfWant(t, "category", body, "PA-MARK", `>Clothing</h1>`, "Things to wear")

	_, body, _ = get(t, v, base+"/cart/")
	wbtfBad(t, "cart", body, "PA-MARK", "SP-MARK", "PAGE-TPL-MARK")

	// Notices after adding to the cart show in the template (the store body that took them is replaced).
	code, _, loc := get(t, v, base+"/product/mug/?add-to-cart="+itoa64(f.mug.ID))
	if code != http.StatusFound {
		t.Fatalf("add-to-cart: %d", code)
	}
	_, body, _ = get(t, v, loc)
	if !strings.Contains(body, "has been added to your cart") {
		t.Errorf("the added-to-cart notice is lost on %s", loc)
	}
}

// Commerce widgets on an ordinary page: settings from the stored doc, store assets, bound products.
func TestWBShopWidgetsOnPage(t *testing.T) {
	base, _, srv, f := wbShopStart(t)
	ctx := context.Background()
	draft := gooMakeSimple(t, srv, "Hidden Lamp", 12, nil)
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE posts SET status = 'draft' WHERE id = $1`, draft.ID)
	body := `<div class="grid" data-wb-slot="shop.products" data-wb-item="c1"></div>` +
		`<div class="forged" data-wb-slot="shop.products" data-wb-item="nope"></div>` +
		`<div class="atc" data-wb-slot="product.add-to-cart" data-wb-item="c2"></div>` +
		`<div class="atc-draft" data-wb-slot="product.add-to-cart" data-wb-item="c3"></div>` +
		`<a class="cart" data-wb-slot="shop.cart-url" data-wb-item="c4"><span data-wb-slot="shop.cart-count" data-wb-item="c4"></span></a>` +
		`<div class="cp" data-wb-slot="shop.cart-page" data-wb-item="c5">CP</div>` +
		`<h2 class="pt" data-wb-slot="product.title" data-wb-item="c6"></h2>`
	doc := wbShopLayout(`{"id":"c1","title":"shop-products","text":"","dyn":{"limit":2,"columns":2,"orderBy":"title","order":"asc"}},` +
		`{"id":"c2","title":"product-add-to-cart","text":"","dyn":{"product":` + itoa64(f.mug.ID) + `}},` +
		`{"id":"c3","title":"product-add-to-cart","text":"","dyn":{"product":` + itoa64(draft.ID) + `}},` +
		`{"id":"c4","title":"shop-cart","text":""},{"id":"c5","title":"shop-cart-page","text":""},{"id":"c6","title":"product-title","text":""}`)
	pg := wbtfPage(t, srv, "page", "Store front", body, doc, `{"layout":"site"}`)
	v := gooFrontClient()
	code, out, _ := get(t, v, base+"/store-front/")
	if code != http.StatusOK {
		t.Fatalf("page %d: %d", pg.ID, code)
	}
	grid := regexp.MustCompile(`(?s)<div class="grid"[^>]*>(.*?)</div>\s*<div class="forged"`).FindStringSubmatch(out)
	if grid == nil {
		t.Fatal("no products grid")
	}
	if !strings.Contains(grid[1], `<ul class="products columns-2">`) || strings.Count(grid[1], `<li class="`) != 2 || !strings.Contains(grid[1], "Breakfast Set") {
		t.Errorf("grid (limit 2, columns 2, by title):\n%s", grid[1])
	}
	// A forged item id gets the defaults (4 columns), never another cell's settings.
	wbtfWant(t, "page", out, `<ul class="products columns-4">`, `goocommerce.css`, `goo-add-to-cart-js`, `data-goo-cart-count=""`, `href="`+base+`/cart/"`)
	atc := regexp.MustCompile(`(?s)<div class="atc"[^>]*>(.*?)</div>\s*<div class="atc-draft"[^>]*>(.*?)</div>`).FindStringSubmatch(out)
	if atc == nil || !strings.Contains(atc[1], `name="add-to-cart" value="`+itoa64(f.mug.ID)+`"`) || strings.TrimSpace(atc[2]) != "" {
		t.Errorf("bound add to cart: %q", atc)
	}
	// Cart page slot off the cart page, product slots without a product: empty.
	wbtfWant(t, "page", out, `data-wb-item="c5"></div>`, `data-wb-item="c6"></h2>`)

	// Another theme (the canvas builds its head after the fill): the store assets still come along.
	if err := srv.switchTheme(ctx, defaultTheme); err != nil {
		t.Fatal(err)
	}
	_, out, _ = get(t, v, base+"/store-front/?canvas=1")
	wbtfWant(t, "canvas", out, `<main id="wb-main">`, `<ul class="products columns-2">`, `goocommerce.css`, `goo-add-to-cart-js`)
	if err := srv.switchTheme(ctx, webbuilder.HelloPursaeTheme); err != nil {
		t.Fatal(err)
	}

	// Commerce off: every store slot is empty and the sample has no shop.
	if err := srv.settings.Set(ctx, map[string]any{gooSetting: false}); err != nil {
		t.Fatal(err)
	}
	_, out, _ = get(t, v, base+"/store-front/?off=1")
	wbtfBad(t, "commerce off", out, `class="products`, `name="add-to-cart"`, `data-goo-cart-count`, `goocommerce.css`)
	c := wbShopAdminCtx(t, srv)
	pv, _ := wbParsePreview("post:" + itoa64(pg.ID))
	q, err := srv.wbResolvePreview(c, pv, nil)
	if err != nil {
		t.Fatal(err)
	}
	dyn, err := srv.wbDynContext(c, "post:"+itoa64(pg.ID), q)
	if err != nil || dyn.Shop != nil {
		t.Errorf("commerce off: shop sample %v (%v)", dyn.Shop, err)
	}
}

// Preview objects of the store resolve to the store views and carry the shop sample.
func TestWBShopPreviewContext(t *testing.T) {
	_, _, srv, f := wbShopStart(t)
	c := wbShopAdminCtx(t, srv)
	for obj, want := range map[string]string{
		"post:" + itoa64(f.shirt.ID):           "Product: Shirt",
		"type:product":                         "Shop",
		"term:product_cat:" + itoa64(f.cat.ID): "Product category: Clothing",
	} {
		pv, err := wbParsePreview(obj)
		if err != nil {
			t.Fatal(obj, err)
		}
		q, err := srv.wbResolvePreview(c, pv, nil)
		if err != nil {
			t.Fatalf("%s: %v", obj, err)
		}
		if got := srv.wbPreviewLabel(q); got != want {
			t.Errorf("%s: label %q, want %q", obj, got, want)
		}
		if u := srv.wbPreviewURL(srv.links(c), q, 7); !strings.Contains(u, "wb_template_preview=7") || strings.Contains(u, "?p=") {
			t.Errorf("%s: url %s", obj, u)
		}
		dyn, err := srv.wbDynContext(c, obj, q)
		if err != nil {
			t.Fatalf("%s: %v", obj, err)
		}
		if dyn.Shop == nil || len(dyn.Shop.Products) == 0 || len(dyn.Shop.Categories) == 0 {
			t.Fatalf("%s: shop sample %+v", obj, dyn.Shop)
		}
		if strings.HasPrefix(obj, "post:") && (dyn.Shop.Product == nil || dyn.Shop.Product.Title != "Shirt" || !strings.Contains(dyn.Shop.Product.PriceHTML, "30.00")) {
			t.Errorf("preview product %+v", dyn.Shop.Product)
		}
	}
	// single-product's latest is the newest product.
	tp := &store.WBTemplate{Type: webbuilder.TemplateSingleProduct}
	q, err := srv.wbResolvePreview(c, wbPreview{Kind: webbuilder.PreviewLatest}, tp)
	if err != nil || q.Store != webbuilder.StoreKindProduct {
		t.Errorf("latest product: %+v %v", q, err)
	}
	// The conditions dialog offers products.
	boot := srv.wbSiteBoot(c, nil)
	found := false
	for _, pt := range boot.PostTypes {
		found = found || pt.Slug == "product"
	}
	if !found {
		t.Error("SiteBoot lacks the product type")
	}
}

// The store types need Pursae Commerce.
func TestWBShopTemplateCreateGate(t *testing.T) {
	_, _, srv, _ := wbShopStart(t)
	if !srv.wbTemplateTypeOffered(webbuilder.TemplateSingleProduct) {
		t.Fatal("single-product not offered with Commerce on")
	}
	_ = srv.settings.Set(context.Background(), map[string]any{gooSetting: false})
	if srv.wbTemplateTypeOffered(webbuilder.TemplateProductArchive) || !srv.wbTemplateTypeOffered(webbuilder.TemplateArchive) {
		t.Error("store types offered with Commerce off")
	}
}

// The site link and the search form follow the request language; the language switcher lists the
// languages.
func TestWBLanguageSwitcherWidget(t *testing.T) {
	srv, base, admin := wbtfStart(t)
	golylangToggle(t, admin, base, "activate")
	pllAddLanguage(t, admin, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, admin, base, "Français", "fr_FR", "fr", "fr")
	pllCreatePost(t, admin, base, "?lang=en", "Hello there")
	pllCreatePost(t, admin, base, "?lang=fr", "Bonjour")
	wbtfTemplate(t, srv, webbuilder.TemplateHeader, "Header", []string{"include/general"},
		`<header><a class="home" data-wb-slot="site.url">Home</a><nav class="langs" data-wb-slot="site.languages" data-wb-item="ls"></nav>LANG-HDR`+
			`<form data-wb-search="" role="search" method="get" action="`+base+`/"><input type="search" name="s"/><button type="submit">Go</button></form></header>`,
		wbShopLayout(`{"id":"ls","title":"language-switcher","text":"","dyn":{"names":true}}`), "", nil)
	code, body := wbtfGet(t, base+"/fr/")
	if code != http.StatusOK {
		t.Fatalf("/fr/: %d", code)
	}
	wbtfWant(t, "fr home", body, "LANG-HDR", `href="`+base+`/fr/">Home</a>`, `<ul class="wb-lang-switcher">`, `hreflang="fr-FR"`, `hreflang="en-US"`,
		`<form data-wb-search="" role="search" method="get" action="`+base+`/fr/">`)
	// Multi Language off: no switcher, the plain home link.
	golylangToggle(t, admin, base, "deactivate")
	_, body = wbtfGet(t, base+"/")
	wbtfWant(t, "off", body, `href="`+base+`/">Home</a>`, `method="get" action="`+base+`/">`)
	wbtfBad(t, "off", body, "wb-lang-switcher")
}

// A notice taken by a builder widget (shop.notices on a page, the cart widget on a Cart page doc)
// is gone from the session afterwards: it shows once, not on every later request.
func TestWBShopNoticesTakenOnce(t *testing.T) {
	base, _, srv, f := wbShopStart(t)
	ctx := context.Background()
	wbtfPage(t, srv, "page", "Notice board", `<p>BOARD-MARK</p><div class="nb" data-wb-slot="shop.notices" data-wb-item="n1"></div>`,
		wbShopLayout(`{"id":"n1","title":"shop-notices","text":""}`), `{"layout":"site"}`)
	cartID := srv.gooPageIDIn(ctx, "cart")
	if cartID <= 0 {
		t.Fatal("no cart page")
	}
	wbtfPublish(t, srv, cartID, `<p>CART-DOC-MARK</p><div class="wb-shop" data-wb-slot="shop.cart-page" data-wb-item="cp"></div>`,
		wbShopLayout(`{"id":"cp","title":"shop-cart-page","text":""}`), "", nil)
	v := gooFrontClient()
	addToCart := func() {
		t.Helper()
		code, _, _ := post(t, v, base+"/product/mug/", url.Values{"add-to-cart": {itoa64(f.mug.ID)}, "quantity": {"1"}})
		if code != http.StatusFound {
			t.Fatalf("add to cart: %d", code)
		}
	}
	storedNotices := func() int {
		t.Helper()
		var n int
		if err := srv.store.Pool.QueryRow(ctx, `SELECT coalesce(max(CASE WHEN jsonb_typeof(data->'notices') = 'array' THEN jsonb_array_length(data->'notices') ELSE 0 END),0) FROM goo_sessions`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	const notice = "has been added to your cart"
	for _, path := range []string{"/notice-board/", "/cart/"} {
		addToCart()
		if storedNotices() == 0 {
			t.Fatalf("%s: the add-to-cart notice is not in the session", path)
		}
		_, body, _ := get(t, v, base+path)
		if !strings.Contains(body, notice) {
			t.Errorf("%s: no notice on the first view", path)
		}
		if n := storedNotices(); n != 0 {
			t.Errorf("%s: %d notices still stored after they were shown", path, n)
		}
		_, body, _ = get(t, v, base+path)
		if strings.Contains(body, notice) {
			t.Errorf("%s: the notice shows again on the next view", path)
		}
	}
}
