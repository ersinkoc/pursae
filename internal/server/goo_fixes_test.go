package server

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"wpgo/internal/store"
)

// Regression tests for commerce fixes: stale stock on the product editor, concurrent refunds, the
// terms checkbox of an unpublished page, coupon restrictions on API order edits, variations of an
// unpublished product, rounding at subtotal, the order-received page of registered customers and the
// wishlist cookie in the page cache.

// The product editor keeps the stored stock when an order changed it while the screen was open
// (WooCommerce's "The stock has not been updated because the value has changed since editing").
func TestGooProductSaveStaleStock(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	_ = srv.gooSetOpts(ctx, map[string]any{"manage_stock": true})
	p := gooMakeSimple(t, srv, "Stocked", 10, func(p *gooProduct) { p.ManageStock, p.StockQuantity = true, gooIntPtr(10) })

	_, nonce, body := gooCatEditor(t, c, base+"/wp-admin/post?post="+itoa64(p.ID)+"&action=edit")
	if orig := gooHidden(t, body, "_original_stock"); orig != "10" {
		t.Fatalf("_original_stock %q, want 10", orig)
	}
	// Three units sell while the screen is open.
	if _, err := srv.gooUpdateStock(ctx, p, -3, false); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"post_title": {"Stocked"}, "save": {"Update"}, "product-type": {"simple"}, "_regular_price": {"12"},
		"_manage_stock": {"yes"}, "_stock": {"10"}, "_original_stock": {"10"}, "_backorders": {"no"}}
	loc := gooCatSave(t, c, base, p.ID, nonce, form)
	if !strings.Contains(loc, "goo_error=stock"+itoa64(p.ID)) {
		t.Errorf("stale stock redirect: %s", loc)
	}
	got, _ := srv.gooGetProduct(ctx, p.ID)
	if got.StockQuantity == nil || *got.StockQuantity != 7 || got.RegularPrice == nil || *got.RegularPrice != 12 {
		t.Errorf("after a stale save: stock %v price %v, want 7 and 12", got.StockQuantity, got.RegularPrice)
	}
	if _, body, _ := get(t, c, base+loc); !strings.Contains(body, "The stock has not been updated because the value has changed since editing. Product "+itoa64(p.ID)+" has 7 units in stock.") {
		t.Error("no stale stock notice")
	}
	// A fresh form changes the stock.
	form.Set("_stock", "20")
	form.Set("_original_stock", "7")
	gooCatSave(t, c, base, p.ID, nonce, form)
	if got, _ := srv.gooGetProduct(ctx, p.ID); got.StockQuantity == nil || *got.StockQuantity != 20 {
		t.Errorf("stock after a fresh save: %v, want 20", got.StockQuantity)
	}

	// Variations: the row carries its original stock and is checked the same way.
	if err := srv.gooSaveAttribute(ctx, &gooAttribute{Name: "Size"}); err != nil {
		t.Fatal(err)
	}
	_ = srv.store.InsertTerm(ctx, termFor("pa_size", "S"))
	vp := gooNewProduct(gooTypeVariable)
	vp.Name = "Sized"
	vp.Attributes = []gooProductAttr{{Taxonomy: "pa_size", Options: []string{"s"}, Visible: true, Variation: true}}
	if err := srv.gooSaveProduct(ctx, vp, 1); err != nil {
		t.Fatal(err)
	}
	v := gooNewProduct(gooTypeVariation)
	v.ParentID, v.Name, v.RegularPrice, v.Status = vp.ID, "Sized - S", gooFloatPtr(5), "publish"
	v.VariationAttrs = map[string]string{"pa_size": "s"}
	v.ManageStock, v.StockQuantity = true, gooIntPtr(5)
	if err := srv.gooSaveProduct(ctx, v, 1); err != nil {
		t.Fatal(err)
	}
	_, nonce, body = gooCatEditor(t, c, base+"/wp-admin/post?post="+itoa64(vp.ID)+"&action=edit")
	if !strings.Contains(body, `name="variable_original_stock[0]" value="5"`) {
		t.Error("the variation row lacks variable_original_stock")
	}
	if _, err := srv.gooUpdateStock(ctx, v, -2, false); err != nil {
		t.Fatal(err)
	}
	vform := url.Values{"post_title": {"Sized"}, "save": {"Update"}, "product-type": {"variable"}, "goo_variations_loaded": {"1"},
		"variable_post_id[0]": {itoa64(v.ID)}, "attribute_pa_size[0]": {"s"}, "variable_enabled[0]": {"on"}, "variable_regular_price[0]": {"5"},
		"variable_manage_stock[0]": {"on"}, "variable_stock[0]": {"5"}, "variable_original_stock[0]": {"5"}}
	loc = gooCatSave(t, c, base, vp.ID, nonce, vform)
	if !strings.Contains(loc, "goo_error=stock"+itoa64(v.ID)) {
		t.Errorf("stale variation stock redirect: %s", loc)
	}
	if got, _ := srv.gooGetProduct(ctx, v.ID); got.StockQuantity == nil || *got.StockQuantity != 3 {
		t.Errorf("variation stock after a stale save: %v, want 3", got.StockQuantity)
	}
}

// Two refunds of the same order racing (a double-clicked button, a retried API call) refund once.
func TestGooRefundRace(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Refundable", 10, nil)
	o := gooRaceOrder(t, srv, "r@example.test", 0, map[*gooProduct]int{p: 1})
	if err := srv.gooSetStatus(ctx, o, "processing", "", ""); err != nil {
		t.Fatal(err)
	}
	var ok atomic.Int32
	var fns []func()
	for range 4 {
		fns = append(fns, func() {
			cp, err := srv.gooGetOrder(ctx, o.ID)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := srv.gooCreateRefund(ctx, cp, cp.Total, "dup", nil, false, true, 1); err == nil {
				ok.Add(1)
			}
		})
	}
	gooRace(fns...)
	if ok.Load() != 1 {
		t.Errorf("%d refunds of the full total succeeded, want 1", ok.Load())
	}
	gooAlmost(t, "refunded", srv.gooTotalRefunded(ctx, o.ID), o.Total)
	if refunds, _ := srv.gooRefunds(ctx, o.ID); len(refunds) != 1 {
		t.Errorf("%d refund rows, want 1", len(refunds))
	}
}

// A Terms page that is no longer published neither shows the checkbox nor blocks the checkout; a
// published one is required when the form showed it.
func TestGooCheckoutTermsUnpublished(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	mug := gooMakeSimple(t, srv, "Terms Mug", 10, func(p *gooProduct) { p.Virtual = true })
	terms := &store.Post{Type: "page", Title: "Terms", Slug: "terms", Status: "draft", AuthorID: 1, Meta: map[string]any{}}
	if err := srv.store.InsertPost(ctx, terms); err != nil {
		t.Fatal(err)
	}
	_ = srv.gooSetOpts(ctx, map[string]any{"terms_page_id": terms.ID, "cheque_settings": map[string]any{"enabled": "yes"}})
	form := func(extra ...string) map[string][]string {
		f := map[string][]string{"billing_first_name": {"Ada"}, "billing_last_name": {"Lovelace"}, "billing_country": {"US"}, "billing_address_1": {"1 Main St"},
			"billing_city": {"Beverly Hills"}, "billing_state": {"CA"}, "billing_postcode": {"90210"}, "billing_phone": {"555-1234"},
			"billing_email": {"ada@example.test"}, "payment_method": {"cheque"}}
		for _, k := range extra {
			f[k] = []string{"1"}
		}
		return f
	}
	const termsErr = "Please read and accept the terms and conditions"
	checkout := func(f map[string][]string) []string {
		c := gooTestCtx()
		cart := srv.gooCartFor(c)
		if _, err := cart.Add(ctx, mug.ID, 1, 0, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(srv.gooPaymentHTML(c, cart), `name="terms"`) != (terms.Status == "publish") {
			t.Error("the terms checkbox does not follow the page's status")
		}
		_, _, errs := srv.gooProcessCheckout(c, f)
		return errs
	}
	// Draft page: no checkbox, no terms error (even from a stale form that posted terms-field).
	for _, f := range []map[string][]string{form(), form("terms-field")} {
		if errs := checkout(f); strings.Contains(strings.Join(errs, "|"), termsErr) {
			t.Errorf("terms required for a draft terms page: %v", errs)
		}
	}
	// Published page: the box is required when the form showed it.
	terms.Status = "publish"
	if err := srv.store.UpdatePost(ctx, terms, 1); err != nil {
		t.Fatal(err)
	}
	if errs := checkout(form("terms-field")); !strings.Contains(strings.Join(errs, "|"), termsErr) {
		t.Errorf("terms not required for a published terms page: %v", errs)
	}
	if errs := checkout(form("terms-field", "terms")); len(errs) > 0 {
		t.Errorf("accepted terms: %v", errs)
	}
}

// API order edits recompute coupon discounts with the coupon's product restrictions, and keep the
// recorded discount of a coupon deleted since.
func TestGooOrderTaxesCouponRestrictions(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	a := gooMakeSimple(t, srv, "A", 100, nil)
	b := gooMakeSimple(t, srv, "B", 50, nil)
	if err := srv.gooSaveCoupon(ctx, &gooCoupon{Code: "save20", DiscountType: "percent", Amount: 20, ProductIDs: []int64{a.ID}}); err != nil {
		t.Fatal(err)
	}
	o := &gooOrder{Status: "processing", Currency: "USD", Billing: map[string]string{}, Shipping: map[string]string{}, Meta: map[string]any{},
		Items: []*gooOrderItem{
			{Type: "line_item", Name: "A", ProductID: a.ID, Quantity: 1, Subtotal: 100, Total: 80, Meta: map[string]any{}},
			{Type: "line_item", Name: "B", ProductID: b.ID, Quantity: 2, Subtotal: 100, Total: 100, Meta: map[string]any{}},
			{Type: "coupon", Name: "save20", Total: 20, Meta: map[string]any{}},
		}}
	srv.gooOrderTaxes(ctx, o)
	lines := o.ItemsOf("line_item")
	gooAlmost(t, "A total", lines[0].Total, 80)
	gooAlmost(t, "B total", lines[1].Total, 100)
	gooAlmost(t, "coupon discount", o.ItemsOf("coupon")[0].Total, 20)

	// A deleted coupon keeps its discount, spread over the lines.
	o2 := &gooOrder{Status: "processing", Currency: "USD", Billing: map[string]string{}, Shipping: map[string]string{}, Meta: map[string]any{},
		Items: []*gooOrderItem{
			{Type: "line_item", Name: "A", ProductID: a.ID, Quantity: 1, Subtotal: 100, Total: 90, Meta: map[string]any{}},
			{Type: "coupon", Name: "gone", Total: 10, Meta: map[string]any{}},
		}}
	srv.gooOrderTaxes(ctx, o2)
	gooAlmost(t, "line after a deleted coupon", o2.ItemsOf("line_item")[0].Total, 90)
	gooAlmost(t, "deleted coupon discount", o2.ItemsOf("coupon")[0].Total, 10)
}

// A variation of a draft (or pending, scheduled) variable product cannot be bought by its own id.
func TestGooVariationOfDraftParent(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSaveAttribute(ctx, &gooAttribute{Name: "Tone"}); err != nil {
		t.Fatal(err)
	}
	_ = srv.store.InsertTerm(ctx, termFor("pa_tone", "Dark"))
	parent := gooNewProduct(gooTypeVariable)
	parent.Name, parent.Status = "Launch", "draft"
	parent.Attributes = []gooProductAttr{{Taxonomy: "pa_tone", Options: []string{"dark"}, Visible: true, Variation: true}}
	if err := srv.gooSaveProduct(ctx, parent, 1); err != nil {
		t.Fatal(err)
	}
	v := gooNewProduct(gooTypeVariation)
	v.ParentID, v.Name, v.RegularPrice, v.Status = parent.ID, "Launch - dark", gooFloatPtr(9), "publish"
	v.VariationAttrs = map[string]string{"pa_tone": "dark"}
	if err := srv.gooSaveProduct(ctx, v, 1); err != nil {
		t.Fatal(err)
	}
	v, _ = srv.gooGetProduct(ctx, v.ID)
	if srv.gooIsPurchasable(ctx, v) {
		t.Error("a variation of a draft product is purchasable")
	}
	cart := srv.gooCartFor(gooTestCtx())
	if _, err := cart.Add(ctx, v.ID, 1, 0, nil, nil, nil); err == nil {
		t.Error("a variation of a draft product was added to the cart by its id")
	}
	parent, _ = srv.gooGetProduct(ctx, parent.ID)
	parent.Status = "publish"
	if err := srv.gooSaveProduct(ctx, parent, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := cart.Add(ctx, v.ID, 1, 0, nil, nil, nil); err != nil {
		t.Errorf("a variation of a published product: %v", err)
	}
}

// Prices entered with tax and rounding at subtotal: the total is the sum of the shelf prices.
func TestGooRoundAtSubtotalInclusive(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSetOpts(ctx, map[string]any{"calc_taxes": true, "prices_include_tax": "yes", "tax_round_at_subtotal": true,
		"default_country": "DE", "tax_based_on": "base", "currency": "EUR"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSaveTaxRate(ctx, &gooTaxRate{Country: "DE", Rate: 19, Name: "MwSt", Priority: 1}); err != nil {
		t.Fatal(err)
	}
	a := gooMakeSimple(t, srv, "Eins", 9.99, func(p *gooProduct) { p.Virtual = true })
	b := gooMakeSimple(t, srv, "Zwei", 9.99, func(p *gooProduct) { p.Virtual = true })
	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	cart.sess.Data.Customer.Billing = map[string]string{"country": "DE"}
	if _, err := cart.Add(ctx, a.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cart.Add(ctx, b.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	cart.Calculate()
	gooAlmost(t, "total tax", cart.TotalTax, 3.19)
	gooAlmost(t, "total", cart.Total, 19.98)
}

// The order-received page of a registered customer's order shows its details only to that customer.
func TestGooOrderReceivedKnownShopper(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Known", 10, nil)
	cid, err := srv.store.CreateUser(ctx, store.NewUser{Login: "shopper", Email: "shopper@example.test", PasswordHash: "x", Role: "customer"})
	if err != nil {
		t.Fatal(err)
	}
	cust, _ := srv.store.UserByID(ctx, cid)
	o := &gooOrder{Status: "processing", CreatedVia: "checkout", CustomerID: cust.ID, Currency: "USD", Total: 10,
		Billing:  map[string]string{"email": "shopper@example.test", "first_name": "Sam", "address_1": "42 Secret Lane"},
		Shipping: map[string]string{}, Meta: map[string]any{},
		Items: []*gooOrderItem{{Type: "line_item", Name: "Known", ProductID: p.ID, Quantity: 1, Subtotal: 10, Total: 10, Meta: map[string]any{}}}}
	if err := srv.gooSaveOrder(ctx, o, true); err != nil {
		t.Fatal(err)
	}
	o, _ = srv.gooGetOrder(ctx, o.ID)
	u := srv.gooOrderReceivedURL(ctx, o)
	if pu, err := url.Parse(u); err == nil && pu.Host != "" {
		u = base + pu.RequestURI()
	}
	code, body, _ := get(t, gooFrontClient(), u)
	if code != http.StatusOK {
		t.Fatalf("order received: %d", code)
	}
	gooMustContain(t, "anonymous order received", body, "Thank you. Your order has been received.", "Please log in to your account to view this order.")
	for _, leak := range []string{"42 Secret Lane", "shopper@example.test", "woocommerce-order-overview", "download_file="} {
		if strings.Contains(body, leak) {
			t.Errorf("anonymous order received page shows %q", leak)
		}
	}
	// The shop manager (the admin) may look.
	gc := gooTestCtx()
	gc.Request = gc.Request.WithContext(ctx)
	admin, _ := srv.store.UserByID(ctx, 1)
	gc.Set(ctxUser, admin)
	gc.Request.URL.RawQuery = "key=" + url.QueryEscape(o.OrderKey)
	if out := srv.gooThankYouHTML(gc, o.ID); !strings.Contains(out, "42 Secret Lane") {
		t.Error("the shop manager does not see the order details")
	}
	gc.Set(ctxUser, cust)
	if out := srv.gooThankYouHTML(gc, o.ID); !strings.Contains(out, "42 Secret Lane") {
		t.Error("the customer does not see their order details")
	}
	// A guest order stays on key-only access.
	o.CustomerID = 0
	other := gooTestCtx()
	other.Request.URL.RawQuery = "key=" + url.QueryEscape(o.OrderKey)
	if err := srv.gooSaveOrder(ctx, o, true); err != nil {
		t.Fatal(err)
	}
	if out := srv.gooThankYouHTML(other, o.ID); !strings.Contains(out, "42 Secret Lane") {
		t.Error("a guest order's details are hidden from the key holder")
	}
	if regexp.MustCompile(`key=`).FindString(u) == "" {
		t.Errorf("order received URL without a key: %s", u)
	}
}

// A visitor whose only store cookie is the wishlist key is never served from, nor stored in, the cache.
func TestGooCacheExcludesWishlistCookie(t *testing.T) {
	_, _, srv := gooSite(t)
	_, cookies := srv.gooCacheExcludes(nil, nil)
	found := false
	for _, ck := range cookies {
		found = found || ck == gooWLCookie
	}
	if !found {
		t.Errorf("cache cookie exclusions %v lack %s", cookies, gooWLCookie)
	}
}
