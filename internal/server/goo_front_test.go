package server

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// Storefront tests: the shop and product pages inside the theme, add to cart, the cart forms and AJAX,
// checkout (with and without JavaScript), order received, My Account, downloads and reviews.

// gooFrontClient is a visitor's browser: cookies, no redirects followed.
func gooFrontClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// gooHidden reads a hidden input's value from a page.
func gooHidden(t *testing.T, body, name string) string {
	t.Helper()
	m := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `" value="([^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s field in the page", name)
	}
	return html.UnescapeString(m[1])
}

func gooMustContain(t *testing.T, what, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("%s lacks %q", what, w)
		}
	}
}

// gooFrontCatalog creates a category, a tag, a color attribute and products of every type.
type gooCatalogFixture struct {
	cat, tag                  *store.Term
	shirt, mug, hoodie, group *gooProduct
	ext, ebook                *gooProduct
	red, blue                 *gooProduct // hoodie variations
}

func gooFrontCatalog(t *testing.T, srv *Server) *gooCatalogFixture {
	t.Helper()
	ctx := context.Background()
	f := &gooCatalogFixture{cat: &store.Term{Taxonomy: "product_cat", Name: "Clothing", Slug: "clothing", Description: "Things to wear"},
		tag: &store.Term{Taxonomy: "product_tag", Name: "Summer", Slug: "summer"}}
	for _, term := range []*store.Term{f.cat, f.tag} {
		if err := srv.store.InsertTerm(ctx, term); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.gooSaveAttribute(ctx, &gooAttribute{Name: "Color", HasArchives: true}); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"Red", "Blue"} {
		if err := srv.store.InsertTerm(ctx, termFor("pa_color", col)); err != nil {
			t.Fatal(err)
		}
	}
	f.shirt = gooMakeSimple(t, srv, "Shirt", 40, func(p *gooProduct) {
		p.SalePrice, p.SKU, p.ManageStock, p.StockQuantity = gooFloatPtr(30), "SH-1", true, gooIntPtr(10)
		p.Post = &store.Post{Type: "product", Content: "A fine <strong>shirt</strong>.", Excerpt: "Short shirt text", CommentStatus: "open", Meta: map[string]any{}}
		p.Weight, p.Length, p.Width, p.Height = "0.5", "30", "20", "2"
		p.Attributes = []gooProductAttr{{Taxonomy: "pa_color", Options: []string{"red"}, Visible: true}}
	})
	f.mug = gooMakeSimple(t, srv, "Mug", 10, nil)
	f.ebook = gooMakeSimple(t, srv, "E-book", 5, func(p *gooProduct) {
		p.Virtual, p.Downloadable = true, true
		p.Downloads = []gooDownload{{Name: "Book PDF", File: "https://files.example.test/book.pdf"}}
		p.DownloadLimit = 1
	})
	f.ext = gooMakeSimple(t, srv, "Partner Lamp", 99, func(p *gooProduct) {
		p.Type, p.ProductURL, p.ButtonText = gooTypeExternal, "https://partner.example.test/lamp?ref=goo", "Buy at partner"
	})
	f.hoodie = gooNewProduct(gooTypeVariable)
	f.hoodie.Name = "Hoodie"
	f.hoodie.Attributes = []gooProductAttr{{Taxonomy: "pa_color", Options: []string{"red", "blue"}, Visible: true, Variation: true}}
	if err := srv.gooSaveProduct(ctx, f.hoodie, 1); err != nil {
		t.Fatal(err)
	}
	for i, col := range []string{"red", "blue"} {
		v := gooNewProduct(gooTypeVariation)
		v.ParentID, v.Name, v.RegularPrice = f.hoodie.ID, "Hoodie - "+col, gooFloatPtr(float64(50+10*i))
		v.VariationAttrs = map[string]string{"pa_color": col}
		v.SKU = "HO-" + col
		if err := srv.gooSaveProduct(ctx, v, 1); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			f.red = v
		} else {
			f.blue = v
		}
	}
	f.group = gooNewProduct(gooTypeGrouped)
	f.group.Name, f.group.Children = "Breakfast Set", []int64{f.mug.ID, f.shirt.ID}
	if err := srv.gooSaveProduct(ctx, f.group, 1); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*gooProduct{f.shirt, f.hoodie} {
		if err := srv.store.SetPostTerms(ctx, p.ID, "product_cat", []int64{f.cat.ID}); err != nil {
			t.Fatal(err)
		}
	}
	_ = srv.store.SetPostTerms(ctx, f.shirt.ID, "product_tag", []int64{f.tag.ID})
	f.hoodie, _ = srv.gooGetProduct(ctx, f.hoodie.ID)
	return f
}

func TestGooFrontShopAndProduct(t *testing.T) {
	base, _, srv := gooSite(t)
	f := gooFrontCatalog(t, srv)
	v := gooFrontClient()

	code, body, _ := get(t, v, base+"/shop/")
	if code != http.StatusOK {
		t.Fatalf("shop: %d", code)
	}
	gooMustContain(t, "shop", body, `class="woocommerce columns-4"`, `<ul class="products columns-4">`, `woocommerce-loop-product__title">Shirt</h2>`,
		`<span class="onsale">Sale!</span>`, `<del aria-hidden="true">`, `add_to_cart_button ajax_add_to_cart`, `?add-to-cart=`+itoa64(f.mug.ID),
		`>Select options</a>`, `>View products</a>`, `>Buy at partner</a>`, `https://partner.example.test/lamp?ref=goo`, `woocommerce-result-count`,
		`<select name="orderby" class="orderby"`, `woocommerce-shop`, `woocommerce-breadcrumb`, `goocommerce.css`, `woocommerce-no-js`,
		`<link rel="canonical" href="`+base+`/shop/"`)
	// A variable product shows its price range.
	gooMustContain(t, "shop", body, `Price range: $50.00 through $60.00`)
	if !strings.Contains(body, "Toggle Site") {
		t.Error("the shop does not render inside the theme")
	}

	for _, a := range []string{"/css/goocommerce.css", "/css/goocommerce-layout.css", "/css/goocommerce-smallscreen.css", "/js/frontend/checkout.js", "/images/placeholder.svg"} {
		if code, _, _ := get(t, v, base+gooAssets+a); code != http.StatusOK {
			t.Errorf("asset %s: %d", a, code)
		}
	}

	// Sorting.
	order := func(u string) []string {
		_, b, _ := get(t, v, u)
		var out []string
		for _, m := range regexp.MustCompile(`woocommerce-loop-product__title">([^<]+)<`).FindAllStringSubmatch(b, -1) {
			out = append(out, m[1])
		}
		return out
	}
	if got := order(base + "/shop/?orderby=price"); len(got) == 0 || got[0] != "E-book" {
		t.Errorf("price ascending: %v", got)
	}
	if got := order(base + "/shop/?orderby=price-desc"); len(got) == 0 || got[0] != "Partner Lamp" {
		t.Errorf("price descending: %v", got)
	}
	// Filters.
	if got := order(base + "/shop/?min_price=45&max_price=55"); len(got) != 1 || got[0] != "Hoodie" {
		t.Errorf("price filter: %v", got)
	}
	if got := order(base + "/shop/?filter_color=blue"); len(got) != 1 || got[0] != "Hoodie" {
		t.Errorf("attribute filter: %v", got)
	}
	if got := order(base + "/shop/?filter_color=red,blue&query_type_color=and"); len(got) != 1 || got[0] != "Hoodie" {
		t.Errorf("attribute AND filter: %v", got)
	}

	// Category and tag archives, plain query vars.
	code, body, _ = get(t, v, base+"/product-category/clothing/")
	if code != http.StatusOK {
		t.Fatalf("category archive: %d", code)
	}
	gooMustContain(t, "category archive", body, "tax-product_cat", "term-clothing", "Things to wear", ">Shirt</h2>", ">Hoodie</h2>")
	if strings.Contains(body, ">Mug</h2>") {
		t.Error("the category archive lists an uncategorized product")
	}
	if _, body, _ = get(t, v, base+"/product-tag/summer/"); !strings.Contains(body, ">Shirt</h2>") || strings.Contains(body, ">Hoodie</h2>") {
		t.Error("tag archive")
	}
	if code, body, _ = get(t, v, base+"/?post_type=product"); code != http.StatusOK || !strings.Contains(body, "products columns-4") {
		t.Errorf("?post_type=product: %d", code)
	}
	if code, _, loc := get(t, v, base+"/?p="+itoa64(f.shirt.ID)); code != http.StatusMovedPermanently || !strings.HasSuffix(loc, "/product/shirt/") {
		t.Errorf("?p=: %d %s", code, loc)
	}

	// Single product.
	code, body, _ = get(t, v, base+"/product/shirt/")
	if code != http.StatusOK {
		t.Fatalf("product: %d", code)
	}
	gooMustContain(t, "product", body, "single-product", `id="product-`+itoa64(f.shirt.ID)+`"`, `woocommerce-product-gallery`, `summary entry-summary`,
		`<p class="price">`, `woocommerce-product-details__short-description`, `<form class="cart"`, `class="input-text qty text"`, `name="add-to-cart" value="`+itoa64(f.shirt.ID)+`"`,
		`<p class="stock in-stock">10 in stock</p>`, `<span class="sku">SH-1</span>`, `posted_in`, `woocommerce-tabs wc-tabs-wrapper`, `tab-description`,
		`woocommerce-product-attributes shop_attributes`, `0.5 kg`, `Reviews (0)`, `id="commentform"`, `application/ld+json`, `"@type":"Product"`,
		`<section class="related products">`, `<link rel="canonical" href="`+base+`/product/shirt/"`)
	if strings.Contains(body, `id="comments" class="comments-area"`) {
		t.Error("the theme's comments render on a product")
	}

	// Variable product: the variations JSON.
	_, body, _ = get(t, v, base+"/product/hoodie/")
	m := regexp.MustCompile(`data-product_variations="([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no data-product_variations")
	}
	var vars []map[string]any
	if err := json.Unmarshal([]byte(html.UnescapeString(m[1])), &vars); err != nil {
		t.Fatal(err)
	}
	if len(vars) != 2 || vars[0]["variation_id"].(float64) != float64(f.red.ID) || vars[0]["display_price"].(float64) != 50 ||
		vars[0]["attributes"].(map[string]any)["attribute_pa_color"] != "red" || vars[1]["sku"] != "HO-blue" {
		t.Errorf("variations: %+v", vars)
	}
	gooMustContain(t, "variable product", body, `<form class="variations_form cart"`, `<table class="variations"`, `name="attribute_pa_color"`, `<option value="blue"`, `name="variation_id"`, "add-to-cart-variation.js")

	// Grouped product.
	_, body, _ = get(t, v, base+"/product/breakfast-set/")
	gooMustContain(t, "grouped product", body, `woocommerce-grouped-product-list group_table`, `name="quantity[`+itoa64(f.mug.ID)+`]"`)

	// Unknown product: 404; plugin off: 404.
	if code, _, _ := get(t, v, base+"/product/nope/"); code != http.StatusNotFound {
		t.Errorf("unknown product: %d", code)
	}
	if err := srv.gooDeactivate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := get(t, v, base+"/product/shirt/"); code != http.StatusNotFound {
		t.Errorf("product while the plugin is off: %d", code)
	}
}

func gooFrontAjax(t *testing.T, c *http.Client, base, action string, form url.Values) map[string]any {
	t.Helper()
	code, body, _ := post(t, c, base+"/?goo-ajax="+action, form)
	if code != http.StatusOK {
		t.Fatalf("%s: %d %s", action, code, body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%s: %v %s", action, err, body)
	}
	return out
}

func TestGooFrontCart(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	f := gooFrontCatalog(t, srv)
	_ = srv.gooSetOpts(ctx, map[string]any{"default_country": "US:CA"})
	z := &gooZone{Name: "US", Locations: []gooZoneLocation{{Code: "US", Type: "country"}}}
	if err := srv.gooSaveZone(ctx, z); err != nil {
		t.Fatal(err)
	}
	flat, _ := srv.gooAddMethod(ctx, z.ID, "flat_rate", map[string]any{"cost": "5"})
	free, _ := srv.gooAddMethod(ctx, z.ID, "free_shipping", map[string]any{})
	if err := srv.gooSaveCoupon(ctx, &gooCoupon{Code: "save5", DiscountType: "fixed_cart", Amount: 5}); err != nil {
		t.Fatal(err)
	}
	v := gooFrontClient()

	// ?add-to-cart= link.
	code, _, loc := get(t, v, base+"/shop/?add-to-cart="+itoa64(f.mug.ID))
	if code != http.StatusFound || strings.Contains(loc, "add-to-cart") {
		t.Fatalf("add-to-cart link: %d %s", code, loc)
	}
	_, body, _ := get(t, v, loc)
	gooMustContain(t, "page after adding", body, `<div class="woocommerce-message" role="alert">&ldquo;Mug&rdquo; has been added to your cart.`, `class="button wc-forward">View cart</a>`)
	// Notices show once.
	if _, body, _ = get(t, v, loc); strings.Contains(body, `woocommerce-message`) {
		t.Error("the notice shows twice")
	}

	// Variable product form.
	_, body, _ = get(t, v, base+"/product/hoodie/")
	code, _, _ = post(t, v, base+"/product/hoodie/", url.Values{"add-to-cart": {itoa64(f.hoodie.ID)}, "product_id": {itoa64(f.hoodie.ID)}, "variation_id": {itoa64(f.blue.ID)},
		"attribute_pa_color": {"blue"}, "quantity": {"2"}})
	if code != http.StatusFound {
		t.Fatalf("variable add: %d", code)
	}
	// Grouped product form.
	code, _, _ = post(t, v, base+"/product/breakfast-set/", url.Values{"add-to-cart": {itoa64(f.group.ID)}, "quantity[" + itoa64(f.mug.ID) + "]": {"1"}, "quantity[" + itoa64(f.shirt.ID) + "]": {"0"}})
	if code != http.StatusFound {
		t.Fatalf("grouped add: %d", code)
	}

	// Cart page.
	code, body, _ = get(t, v, base+"/cart/")
	if code != http.StatusOK {
		t.Fatalf("cart: %d", code)
	}
	gooMustContain(t, "cart", body, `<form class="woocommerce-cart-form"`, `shop_table shop_table_responsive cart`, `woocommerce-cart`, `>Mug</a>`, `>Hoodie</a>`,
		`<dt class="variation-Color">Color:</dt>`, `name="coupon_code"`, `name="update_cart"`, `<div class="cart_totals`, `Proceed to checkout`, `woocommerce-cart-form__cart-item cart_item`)
	if !strings.Contains(body, `value="2"`) {
		t.Error("the mug's quantity (2) is not shown")
	}
	cart := regexp.MustCompile(`name="cart\[([0-9a-f]{32})\]\[qty\]" value="(\d+)"`).FindAllStringSubmatch(body, -1)
	if len(cart) != 2 {
		t.Fatalf("cart rows: %v", cart)
	}
	nonce := gooHidden(t, body, "woocommerce-cart-nonce")

	// Update a quantity and apply a coupon.
	form := url.Values{"woocommerce-cart-nonce": {nonce}, "update_cart": {"Update cart"}}
	for _, r := range cart {
		q := r[2]
		if q == "2" && strings.Contains(body, "") {
			q = "3"
		}
		form.Set("cart["+r[1]+"][qty]", q)
	}
	if code, _, _ := post(t, v, base+"/cart/", form); code != http.StatusFound {
		t.Fatalf("update cart: %d", code)
	}
	if code, _, _ := post(t, v, base+"/cart/", url.Values{"woocommerce-cart-nonce": {nonce}, "apply_coupon": {"Apply coupon"}, "coupon_code": {"SAVE5"}}); code != http.StatusFound {
		t.Fatalf("apply coupon: %d", code)
	}
	_, body, _ = get(t, v, base+"/cart/")
	gooMustContain(t, "cart after update", body, "Cart updated.", "Coupon code applied successfully.", `cart-discount coupon-save5`, `[Remove]`)

	// Shipping: calculate for California, then choose free shipping.
	calc := gooHidden(t, body, "woocommerce-shipping-calculator-nonce")
	if code, _, _ := post(t, v, base+"/cart/", url.Values{"woocommerce-shipping-calculator-nonce": {calc}, "calc_shipping": {"1"}, "calc_shipping_country": {"US"},
		"calc_shipping_state": {"CA"}, "calc_shipping_postcode": {"90210"}}); code != http.StatusFound {
		t.Fatalf("shipping calculator: %d", code)
	}
	_, body, _ = get(t, v, base+"/cart/")
	flatID, freeID := "flat_rate:"+itoa64(flat.InstanceID), "free_shipping:"+itoa64(free.InstanceID)
	gooMustContain(t, "cart shipping", body, `name="shipping_method[0]"`, `value="`+flatID+`"`, `value="`+freeID+`"`, `form="goo-cart-form"`)
	if code, _, _ := post(t, v, base+"/cart/", url.Values{"woocommerce-cart-nonce": {nonce}, "update_cart": {"1"}, "shipping_method[0]": {freeID}}); code != http.StatusFound {
		t.Fatal("choose shipping")
	}
	_, body, _ = get(t, v, base+"/cart/")
	if !regexp.MustCompile(`value="` + regexp.QuoteMeta(freeID) + `" class="shipping_method" checked="checked"`).MatchString(body) {
		t.Error("free shipping is not the chosen rate")
	}

	// Cache headers of the private pages.
	for _, u := range []string{"/cart/", "/checkout/", "/my-account/"} {
		resp, err := v.Get(base + u)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
			t.Errorf("%s Cache-Control: %q", u, cc)
		}
	}

	// AJAX: add, fragments, remove.
	a := gooFrontClient()
	out := gooFrontAjax(t, a, base, "add_to_cart", url.Values{"product_id": {itoa64(f.mug.ID)}, "quantity": {"1"}})
	frags, _ := out["fragments"].(map[string]any)
	mini, _ := frags["div.widget_shopping_cart_content"].(string)
	if !strings.Contains(mini, "woocommerce-mini-cart-item") || !strings.Contains(mini, "Mug") || out["cart_hash"] == "" {
		t.Fatalf("add_to_cart fragments: %+v", out)
	}
	if link, _ := frags["a.cart-contents"].(string); !strings.Contains(link, "1 item") {
		t.Errorf("cart link fragment: %s", link)
	}
	out = gooFrontAjax(t, a, base, "add_to_cart", url.Values{"product_id": {itoa64(f.hoodie.ID)}, "quantity": {"1"}})
	if out["error"] != true {
		t.Errorf("a variable product without options was added by AJAX: %+v", out)
	}
	out = gooFrontAjax(t, a, base, "get_refreshed_fragments", url.Values{})
	frags, _ = out["fragments"].(map[string]any)
	mini, _ = frags["div.widget_shopping_cart_content"].(string)
	key := regexp.MustCompile(`data-cart_item_key="([0-9a-f]{32})"`).FindStringSubmatch(mini)
	if key == nil {
		t.Fatalf("no cart item key in the mini cart: %s", mini)
	}
	out = gooFrontAjax(t, a, base, "remove_from_cart", url.Values{"cart_item_key": {key[1]}})
	frags, _ = out["fragments"].(map[string]any)
	if mini, _ = frags["div.widget_shopping_cart_content"].(string); !strings.Contains(mini, "No products in the cart.") {
		t.Errorf("after remove_from_cart: %s", mini)
	}
}

// gooFrontCheckoutForm is a valid billing form for the checkout page's fields.
func gooFrontCheckoutForm(nonce string) url.Values {
	return url.Values{"billing_first_name": {"Ada"}, "billing_last_name": {"Lovelace"}, "billing_country": {"US"}, "billing_address_1": {"1 Main St"},
		"billing_city": {"Beverly Hills"}, "billing_state": {"CA"}, "billing_postcode": {"90210"}, "billing_phone": {"555-1234"},
		"billing_email": {"ada@example.test"}, "payment_method": {"goo_test"}, "woocommerce-process-checkout-nonce": {nonce}}
}

func TestGooFrontCheckout(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	f := gooFrontCatalog(t, srv)
	_ = srv.gooSetOpts(ctx, map[string]any{"goo_test_settings": map[string]any{"enabled": "yes"}})
	z := &gooZone{Name: "US", Locations: []gooZoneLocation{{Code: "US", Type: "country"}}}
	_ = srv.gooSaveZone(ctx, z)
	_, _ = srv.gooAddMethod(ctx, z.ID, "flat_rate", map[string]any{"cost": "5"})
	v := gooFrontClient()

	// An empty cart sends the checkout to the cart.
	if code, _, loc := get(t, v, base+"/checkout/"); code != http.StatusFound || !strings.Contains(loc, "/cart/") {
		t.Errorf("empty checkout: %d %s", code, loc)
	}
	get(t, v, base+"/?add-to-cart="+itoa64(f.mug.ID))
	code, body, _ := get(t, v, base+"/checkout/")
	if code != http.StatusOK {
		t.Fatalf("checkout: %d", code)
	}
	gooMustContain(t, "checkout", body, `<form name="checkout" method="post" class="checkout woocommerce-checkout"`, `id="billing_first_name"`, `<select name="billing_country"`,
		`id="billing_state"`, `name="ship_to_different_address"`, `id="order_comments"`, `woocommerce-checkout-review-order-table`, `<ul class="wc_payment_methods payment_methods methods">`,
		`value="goo_test"`, `id="place_order"`, `woocommerce-privacy-policy-text`, `showlogin`, `showcoupon`, "goo_country_select_params", "checkout.js")
	nonce := gooHidden(t, body, "woocommerce-process-checkout-nonce")

	// Validation errors come back as notices.
	bad := gooFrontCheckoutForm(nonce)
	bad.Del("billing_first_name")
	code, _, loc := post(t, v, base+"/checkout/", bad)
	if code != http.StatusFound {
		t.Fatalf("invalid checkout: %d", code)
	}
	_, body, _ = get(t, v, loc)
	gooMustContain(t, "checkout errors", body, `<ul class="woocommerce-error" role="alert">`, "Billing First name</strong> is a required field")

	// Without JavaScript: a POST creates the order and redirects to order received.
	code, _, loc = post(t, v, base+"/checkout/", gooFrontCheckoutForm(nonce))
	if code != http.StatusFound || !strings.Contains(loc, "/checkout/order-received/") {
		t.Fatalf("checkout POST: %d %s", code, loc)
	}
	_, body, _ = get(t, v, loc)
	gooMustContain(t, "order received", body, "Thank you. Your order has been received.", `woocommerce-order-overview`, "Order number:", `woocommerce-table--order-details`,
		">Mug</a>", "Billing address", "ada@example.test", "woocommerce-order-received")
	// A wrong key shows no details.
	wrong := regexp.MustCompile(`key=[^&]+`).ReplaceAllString(loc, "key=wc_order_wrong")
	_, body, _ = get(t, v, wrong)
	if !strings.Contains(body, "Thank you. Your order has been received.") || strings.Contains(body, "woocommerce-order-overview") || strings.Contains(body, "ada@example.test") {
		t.Error("order details shown with a wrong key")
	}
	m := regexp.MustCompile(`order-received/(\d+)/`).FindStringSubmatch(loc)
	o, err := srv.gooGetOrder(ctx, gooAtoi64(m[1]))
	if err != nil || o.Status != "processing" || o.Billing["email"] != "ada@example.test" {
		t.Fatalf("order: %v %+v", err, o)
	}

	// With JavaScript: update_order_review then checkout.
	get(t, v, base+"/?add-to-cart="+itoa64(f.shirt.ID))
	_, body, _ = get(t, v, base+"/checkout/")
	nonce = gooHidden(t, body, "woocommerce-process-checkout-nonce")
	params := regexp.MustCompile(`"update_order_review_nonce":"([^"]+)"`).FindStringSubmatch(body)
	if params == nil {
		t.Fatal("no update_order_review nonce in the page")
	}
	form := gooFrontCheckoutForm(nonce)
	review := gooFrontAjax(t, v, base, "update_order_review", url.Values{"security": {params[1]}, "post_data": {form.Encode()}})
	frags, _ := review["fragments"].(map[string]any)
	if review["result"] != "success" || !strings.Contains(frags[".woocommerce-checkout-review-order-table"].(string), "Shirt") || !strings.Contains(frags[".woocommerce-checkout-payment"].(string), "place_order") {
		t.Fatalf("update_order_review: %+v", review)
	}
	res := gooFrontAjax(t, v, base, "checkout", form)
	if res["result"] != "success" || !strings.Contains(res["redirect"].(string), "order-received") {
		t.Fatalf("AJAX checkout: %+v", res)
	}
	res = gooFrontAjax(t, v, base, "checkout", form)
	if res["result"] != "failure" || !strings.Contains(res["messages"].(string), "woocommerce-error") {
		t.Errorf("AJAX checkout with an empty cart: %+v", res)
	}
}

func TestGooFrontAccount(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	f := gooFrontCatalog(t, srv)
	_ = srv.gooSetOpts(ctx, map[string]any{"goo_test_settings": map[string]any{"enabled": "yes"}, "enable_myaccount_registration": true})
	v := gooFrontClient()

	_, body, _ := get(t, v, base+"/my-account/")
	gooMustContain(t, "my account (logged out)", body, `woocommerce-form woocommerce-form-login login`, `name="username"`, `Lost your password?`, `woocommerce-form-register`, "woocommerce-account")
	// Register.
	code, _, _ := post(t, v, base+"/my-account/", url.Values{"email": {"grace@example.test"}, "woocommerce-register-nonce": {gooHidden(t, body, "woocommerce-register-nonce")}, "register": {"Register"}})
	if code != http.StatusFound {
		t.Fatalf("register: %d", code)
	}
	_, body, _ = get(t, v, base+"/my-account/")
	gooMustContain(t, "dashboard", body, `woocommerce-MyAccount-navigation`, `woocommerce-MyAccount-navigation-link--orders`, `From your account dashboard`, `customer-logout/?_wpnonce=`)
	logout := regexp.MustCompile(`href="([^"]*customer-logout/\?_wpnonce=[^"]+)"`).FindStringSubmatch(body)
	if code, _, _ := get(t, v, html.UnescapeString(logout[1])); code != http.StatusFound {
		t.Fatalf("logout: %d", code)
	}
	if _, body, _ = get(t, v, base+"/my-account/"); !strings.Contains(body, "woocommerce-form-login") {
		t.Fatal("still logged in after logging out")
	}

	// Log in with the core credentials (a customer with a password).
	uid, err := srv.gooCreateCustomer(ctx, "linus@example.test", "linus", "s3cret-pass", map[string]string{"billing_first_name": "Linus"})
	if err != nil {
		t.Fatal(err)
	}
	code, _, _ = post(t, v, base+"/my-account/", url.Values{"username": {"linus"}, "password": {"wrong"}, "woocommerce-login-nonce": {gooHidden(t, body, "woocommerce-login-nonce")}, "login": {"Log in"}})
	_, body, _ = get(t, v, base+"/my-account/")
	if !strings.Contains(body, "is incorrect") {
		t.Error("no error for a wrong password")
	}
	code, _, _ = post(t, v, base+"/my-account/", url.Values{"username": {"linus"}, "password": {"s3cret-pass"}, "woocommerce-login-nonce": {gooHidden(t, body, "woocommerce-login-nonce")}, "login": {"Log in"}})
	if code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}

	// Buy the e-book as the customer, then see the order, view it, download.
	get(t, v, base+"/?add-to-cart="+itoa64(f.ebook.ID))
	_, body, _ = get(t, v, base+"/checkout/")
	form := gooFrontCheckoutForm(gooHidden(t, body, "woocommerce-process-checkout-nonce"))
	form.Set("billing_email", "linus@example.test")
	code, _, loc := post(t, v, base+"/checkout/", form)
	if code != http.StatusFound || !strings.Contains(loc, "order-received") {
		t.Fatalf("customer checkout: %d %s", code, loc)
	}
	orders, _, _ := srv.gooQueryOrders(ctx, gooOrderQuery{CustomerID: uid})
	if len(orders) != 1 || orders[0].Status != "completed" {
		t.Fatalf("customer orders: %+v", orders)
	}
	o := orders[0]
	_, body, _ = get(t, v, base+"/my-account/orders/")
	gooMustContain(t, "orders", body, `woocommerce-orders-table`, `#`+itoa64(o.ID), `/my-account/view-order/`+itoa64(o.ID)+`/`, "Completed")
	_, body, _ = get(t, v, base+"/my-account/view-order/"+itoa64(o.ID)+"/")
	gooMustContain(t, "view order", body, `<mark class="order-number">`+itoa64(o.ID)+`</mark>`, `woocommerce-table--order-details`, "Order again", "E-book")
	// Another customer's order is refused.
	other := &gooOrder{Status: "processing", Billing: map[string]string{"email": "x@example.test"}, CustomerID: 999999}
	_ = srv.gooSaveOrder(ctx, other, false)
	if _, body, _ = get(t, v, base+"/my-account/view-order/"+itoa64(other.ID)+"/"); !strings.Contains(body, "Invalid order.") {
		t.Error("another customer's order is visible")
	}

	_, body, _ = get(t, v, base+"/my-account/downloads/")
	dl := regexp.MustCompile(`href="([^"]*download_file=[^"]+)" class="woocommerce-MyAccount-downloads-file`).FindStringSubmatch(body)
	if dl == nil {
		t.Fatalf("no download link:\n%s", body[strings.Index(body, "woocommerce-MyAccount-content"):][:800])
	}
	code, _, loc = get(t, v, html.UnescapeString(dl[1]))
	if code != http.StatusFound || loc != "https://files.example.test/book.pdf" {
		t.Errorf("download: %d %s", code, loc)
	}
	if code, _, _ = get(t, v, html.UnescapeString(dl[1])); code != http.StatusForbidden {
		t.Errorf("download past the limit: %d", code)
	}
	if code, _, _ = get(t, v, base+"/?download_file="+itoa64(f.ebook.ID)+"&order="+o.OrderKey+"&key=nope"); code != http.StatusForbidden {
		t.Errorf("download with a bad key: %d", code)
	}
	// A guest purchase with the account's (unverified) e-mail is not listed under the account.
	guest := &gooOrder{Status: "completed", Billing: map[string]string{"email": "LINUS@example.test"}}
	if err := srv.gooSaveOrder(ctx, guest, false); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO goo_download_permissions (download_id, product_id, order_id, order_key, user_email, user_id)
		VALUES ($1, $2, $3, $4, $5, 0)`, f.ebook.Downloads[0].ID, f.ebook.ID, guest.ID, guest.OrderKey, "linus@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, body, _ = get(t, v, base+"/my-account/downloads/"); strings.Contains(body, guest.OrderKey) {
		t.Error("a guest order's downloads are listed for an account with the same e-mail")
	}

	// Edit the billing address.
	_, body, _ = get(t, v, base+"/my-account/edit-address/billing/")
	gooMustContain(t, "edit address", body, `woocommerce-address-fields`, `name="billing_address_1"`, `name="save_address"`)
	addr := url.Values{"billing_first_name": {"Linus"}, "billing_last_name": {"T"}, "billing_country": {"US"}, "billing_address_1": {"2 Oak Rd"}, "billing_city": {"Portland"},
		"billing_state": {"OR"}, "billing_postcode": {"97201"}, "billing_phone": {"555"}, "billing_email": {"linus@example.test"}, "address_type": {"billing"},
		"woocommerce-edit-address-nonce": {gooHidden(t, body, "woocommerce-edit-address-nonce")}, "save_address": {"Save address"}}
	if code, _, _ = post(t, v, base+"/my-account/edit-address/billing/", addr); code != http.StatusFound {
		t.Fatalf("save address: %d", code)
	}
	u, _ := srv.store.UserByID(ctx, uid)
	if u.Meta["billing_address_1"] != "2 Oak Rd" || u.Meta["billing_state"] != "OR" {
		t.Errorf("billing meta: %+v", u.Meta)
	}
	_, body, _ = get(t, v, base+"/my-account/edit-address/")
	gooMustContain(t, "addresses", body, "Address changed successfully.", "2 Oak Rd", "Oregon")

	// Account details: a wrong current password is refused, a right one changes it.
	_, body, _ = get(t, v, base+"/my-account/edit-account/")
	n := gooHidden(t, body, "save-account-details-nonce")
	acc := url.Values{"account_first_name": {"Linus"}, "account_last_name": {"T"}, "account_display_name": {"Linus T"}, "account_email": {"linus@example.test"},
		"password_current": {"bad"}, "password_1": {"new-pass-1"}, "password_2": {"new-pass-1"}, "save-account-details-nonce": {n}, "save_account_details": {"1"}}
	post(t, v, base+"/my-account/edit-account/", acc)
	if _, body, _ = get(t, v, base+"/my-account/edit-account/"); !strings.Contains(body, "Your current password is incorrect.") {
		t.Error("a wrong current password was accepted")
	}
	acc.Set("password_current", "s3cret-pass")
	acc.Set("save-account-details-nonce", gooHidden(t, body, "save-account-details-nonce"))
	post(t, v, base+"/my-account/edit-account/", acc)
	u, _ = srv.store.UserByID(ctx, uid)
	if u.DisplayName != "Linus T" {
		t.Errorf("display name: %q", u.DisplayName)
	}
}

func TestGooFrontReviews(t *testing.T) {
	base, admin, srv := gooSite(t)
	ctx := context.Background()
	f := gooFrontCatalog(t, srv)
	v := gooFrontClient()
	_, body, _ := get(t, v, base+"/product/shirt/")
	nonce := gooHidden(t, body, "_wpnonce")
	form := url.Values{"comment_post_ID": {itoa64(f.shirt.ID)}, "goo_review": {"1"}, "_wpnonce": {nonce}, "author": {"Rita"}, "email": {"rita@example.test"},
		"comment": {"Lovely shirt."}}
	// The rating is required by default.
	if code, _, _ := post(t, v, base+"/product/shirt/", form); code != http.StatusFound {
		t.Fatalf("review without rating: %d", code)
	}
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM comments WHERE post_id = $1`, f.shirt.ID).Scan(&n)
	if n != 0 {
		t.Error("a review without a rating was saved")
	}
	// A first-time reviewer is held for moderation (comment_previously_approved): no rating yet.
	form.Set("rating", "5")
	if code, _, _ := post(t, v, base+"/product/shirt/", form); code != http.StatusFound {
		t.Fatalf("review: %d", code)
	}
	if p, _ := srv.gooGetProduct(ctx, f.shirt.ID); p.RatingCount != 0 {
		t.Error("a pending review counts in the rating")
	}
	if _, body, _ = get(t, v, base+"/product/shirt/"); !strings.Contains(body, "Your review is awaiting approval") {
		t.Error("the reviewer does not see the pending review")
	}
	// An approved review (the admin moderates) updates the rating.
	_, body, _ = get(t, admin, base+"/product/shirt/")
	form = url.Values{"comment_post_ID": {itoa64(f.shirt.ID)}, "goo_review": {"1"}, "_wpnonce": {gooHidden(t, body, "_wpnonce")}, "comment": {"Fits well."}, "rating": {"4"}}
	if code, _, _ := post(t, admin, base+"/product/shirt/", form); code != http.StatusFound {
		t.Fatalf("admin review: %d", code)
	}
	p, _ := srv.gooGetProduct(ctx, f.shirt.ID)
	if p.RatingCount != 1 || p.AverageRating != 4 {
		t.Fatalf("rating after the review: %d %v", p.RatingCount, p.AverageRating)
	}
	_, body, _ = get(t, gooFrontClient(), base+"/product/shirt/")
	gooMustContain(t, "product with a review", body, "Reviews (1)", `woocommerce-review__author">admin`, "Fits well.", `aria-label="Rated 4.00 out of 5"`, `"aggregateRating"`)
	if strings.Contains(body, "Lovely shirt.") {
		t.Error("a pending review is public")
	}
	_, body, _ = get(t, gooFrontClient(), base+"/shop/")
	if !strings.Contains(body, `<div class="star-rating" role="img" aria-label="Rated 4.00 out of 5">`) {
		t.Error("the shop loop shows no rating")
	}
}

func TestGooFrontShortcodes(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	f := gooFrontCatalog(t, srv)
	page := &store.Post{Type: "page", Slug: "deals", Title: "Deals", Status: "publish", AuthorID: 1, Meta: map[string]any{},
		Content: `<!-- wp:shortcode -->[products limit="2" orderby="price" order="DESC" columns="2"]<!-- /wp:shortcode -->` + "\n\n" +
			`[sale_products]` + "\n\n" + `[add_to_cart id="` + itoa64(f.mug.ID) + `"]` + "\n\n" + `[woocommerce_product_categories]` + "\n\n" + `[goocommerce_order_tracking]`}
	if err := srv.store.InsertPost(ctx, page); err != nil {
		t.Fatal(err)
	}
	_, body, _ := get(t, c, base+"/deals/")
	gooMustContain(t, "shortcodes", body, `<div class="woocommerce columns-2"><ul class="products columns-2">`, `>Partner Lamp</h2>`, `add_to_cart_inline`,
		`product-category product`, `track_order`, "goocommerce.css")
	if !strings.Contains(body, `>Shirt</h2>`) {
		t.Error("[sale_products] lacks the shirt")
	}
	if strings.Contains(body, "[products") || strings.Contains(body, "[sale_products]") {
		t.Error("unexpanded shortcodes")
	}
}
