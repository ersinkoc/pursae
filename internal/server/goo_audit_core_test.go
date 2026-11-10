package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// Regression tests for the Commerce core audit: refund restock bounded by _reduced_stock, checkout
// stock per stock owner, the atomic download limit, sold-individually variations, customer usernames,
// ex-tax admin lines on prices-include-tax stores, coupon recalculation on gross amounts and the line
// totals of wc/v3 order writes.

func gooAuditStock(t *testing.T, srv *Server, id int64) int {
	t.Helper()
	p, err := srv.gooGetProduct(context.Background(), id)
	if err != nil || p.StockQuantity == nil {
		t.Fatalf("stock of %d: %v", id, err)
	}
	return *p.StockQuantity
}

// A refund only restocks what the order took from stock and has not put back yet.
func TestGooRefundRestockOnlyReduced(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	_ = srv.gooSetOpts(ctx, map[string]any{"manage_stock": true})
	p := gooMakeSimple(t, srv, "Counted", 10, func(p *gooProduct) { p.ManageStock, p.StockQuantity = true, gooIntPtr(10) })
	newOrder := func() *gooOrder {
		o := &gooOrder{Status: "processing", Currency: "USD", Total: 20, Billing: map[string]string{"email": "r@example.test"}, Shipping: map[string]string{}, Meta: map[string]any{},
			Items: []*gooOrderItem{{Type: "line_item", Name: "Counted", ProductID: p.ID, Quantity: 2, Subtotal: 20, Total: 20, Meta: map[string]any{}}}}
		if err := srv.gooSaveOrder(ctx, o, true); err != nil {
			t.Fatal(err)
		}
		return o
	}

	// Cancelled (stock put back) then refunded with restock: nothing more comes back.
	o := newOrder()
	if err := srv.gooReduceStock(ctx, o, false); err != nil {
		t.Fatal(err)
	}
	if n := gooAuditStock(t, srv, p.ID); n != 8 {
		t.Fatalf("stock after the order = %d, want 8", n)
	}
	srv.gooRestoreStock(ctx, o)
	o, _ = srv.gooGetOrder(ctx, o.ID)
	line := o.ItemsOf("line_item")[0]
	if _, err := srv.gooCreateRefund(ctx, o, 20, "", []gooRefundLine{{ItemID: line.ID, Qty: 2, Total: 20}}, true, false, 1); err != nil {
		t.Fatal(err)
	}
	if n := gooAuditStock(t, srv, p.ID); n != 10 {
		t.Errorf("stock after refunding a cancelled order = %d, want 10", n)
	}

	// Refunding the same line twice restocks at most what was reduced.
	o = newOrder()
	if err := srv.gooReduceStock(ctx, o, false); err != nil {
		t.Fatal(err)
	}
	o, _ = srv.gooGetOrder(ctx, o.ID)
	line = o.ItemsOf("line_item")[0]
	if _, err := srv.gooCreateRefund(ctx, o, 10, "", []gooRefundLine{{ItemID: line.ID, Qty: 1, Total: 10}}, true, false, 1); err != nil {
		t.Fatal(err)
	}
	if n := gooAuditStock(t, srv, p.ID); n != 9 {
		t.Errorf("stock after refunding one = %d, want 9", n)
	}
	o, _ = srv.gooGetOrder(ctx, o.ID)
	line = o.ItemsOf("line_item")[0]
	if red, _ := line.Meta["_reduced_stock"].(float64); red != 1 {
		t.Errorf("_reduced_stock after refunding one = %v, want 1", red)
	}
	if _, err := srv.gooCreateRefund(ctx, o, 10, "", []gooRefundLine{{ItemID: line.ID, Qty: 2, Total: 10}}, true, false, 1); err != nil {
		t.Fatal(err)
	}
	if n := gooAuditStock(t, srv, p.ID); n != 10 {
		t.Errorf("stock after refunding again = %d, want 10", n)
	}

	// A pending order whose stock was never reduced restocks nothing.
	o = newOrder()
	line = o.ItemsOf("line_item")[0]
	if _, err := srv.gooCreateRefund(ctx, o, 20, "", []gooRefundLine{{ItemID: line.ID, Qty: 2, Total: 20}}, true, false, 1); err != nil {
		t.Fatal(err)
	}
	if n := gooAuditStock(t, srv, p.ID); n != 10 {
		t.Errorf("stock after refunding an unreduced order = %d, want 10", n)
	}
}

// gooAuditVariable makes a variable product with two variations; mut changes the parent.
func gooAuditVariable(t *testing.T, srv *Server, mut func(p *gooProduct)) (*gooProduct, *gooProduct, *gooProduct) {
	t.Helper()
	ctx := context.Background()
	if err := srv.gooSaveAttribute(ctx, &gooAttribute{Name: "Shade"}); err != nil {
		t.Fatal(err)
	}
	_ = srv.store.InsertTerm(ctx, termFor("pa_shade", "Red"))
	_ = srv.store.InsertTerm(ctx, termFor("pa_shade", "Blue"))
	parent := gooNewProduct(gooTypeVariable)
	parent.Name, parent.Status = "Shaded", "publish"
	parent.Attributes = []gooProductAttr{{Taxonomy: "pa_shade", Options: []string{"red", "blue"}, Visible: true, Variation: true}}
	if mut != nil {
		mut(parent)
	}
	if err := srv.gooSaveProduct(ctx, parent, 1); err != nil {
		t.Fatal(err)
	}
	var vs []*gooProduct
	for _, shade := range []string{"red", "blue"} {
		v := gooNewProduct(gooTypeVariation)
		v.ParentID, v.Name, v.RegularPrice, v.Status, v.Virtual = parent.ID, "Shaded - "+shade, gooFloatPtr(5), "publish", true
		v.VariationAttrs = map[string]string{"pa_shade": shade}
		if err := srv.gooSaveProduct(ctx, v, 1); err != nil {
			t.Fatal(err)
		}
		vs = append(vs, v)
	}
	return parent, vs[0], vs[1]
}

// Checkout sums the cart's quantities per stock owner (parent-managed variations).
func TestGooCheckoutStockPerOwner(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	_ = srv.gooSetOpts(ctx, map[string]any{"manage_stock": true, "goo_test_settings": map[string]any{"enabled": "yes"}})
	parent, red, blue := gooAuditVariable(t, srv, func(p *gooProduct) { p.ManageStock, p.StockQuantity = true, gooIntPtr(5) })
	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	if _, err := cart.Add(ctx, parent.ID, 2, red.ID, map[string]string{"attribute_pa_shade": "red"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cart.Add(ctx, parent.ID, 2, blue.ID, map[string]string{"attribute_pa_shade": "blue"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	// Other orders take the parent's stock down to 3 while the cart waits.
	if _, err := srv.gooUpdateStock(ctx, parent, 3, true); err != nil {
		t.Fatal(err)
	}
	delete(c.Keys, "goo.cart") // the checkout request loads the cart (and the products) afresh
	_, o, errs := srv.gooProcessCheckout(c, gxaCheckoutForm("owner@example.test", "goo_test"))
	if o != nil || !strings.Contains(strings.Join(errs, "|"), "do not have enough") {
		t.Fatalf("checkout of 4 units against a stock of 3: order %v, errors %v", o != nil, errs)
	}
	if n := gooAuditStock(t, srv, parent.ID); n != 3 {
		t.Errorf("parent stock = %d, want 3", n)
	}
}

// A sold-individually parent limits its variations to one through the cart update as well.
func TestGooSoldIndividuallyVariationQuantity(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	parent, red, _ := gooAuditVariable(t, srv, func(p *gooProduct) { p.SoldIndividually = true })
	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	key, err := cart.Add(ctx, parent.ID, 1, red.ID, map[string]string{"attribute_pa_shade": "red"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := cart.SetQuantity(ctx, key, 10); err == nil {
		t.Error("quantity 10 of a sold-individually variation was accepted")
	}
	if it := cart.Item(key); it == nil || it.Quantity != 1 {
		t.Errorf("cart item after the refused update: %+v", it)
	}
}

// Parallel downloads cannot exceed the download limit.
func TestGooDownloadLimitConcurrent(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Limited", 5, func(p *gooProduct) {
		p.Downloadable, p.Virtual, p.DownloadLimit = true, true, 1
		p.Downloads = []gooDownload{{ID: "lim1", Name: "file.pdf", File: "https://files.example.test/file.pdf"}}
	})
	o := &gooOrder{Status: "completed", Currency: "USD", Total: 5, Billing: map[string]string{"email": "dl@example.test"}, Shipping: map[string]string{}, Meta: map[string]any{},
		Items: []*gooOrderItem{{Type: "line_item", Name: "Limited", ProductID: p.ID, Quantity: 1, Subtotal: 5, Total: 5, Meta: map[string]any{}}}}
	if err := srv.gooSaveOrder(ctx, o, true); err != nil {
		t.Fatal(err)
	}
	srv.gooGrantDownloads(ctx, o)
	u := fmt.Sprintf("%s/?download_file=%d&order=%s&email=%s&key=lim1", base, p.ID, o.OrderKey, "dl@example.test")
	var wg sync.WaitGroup
	var mu sync.Mutex
	served := 0
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, u, nil)
			resp, err := gooGuest().Do(req)
			if err != nil {
				return
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusFound {
				mu.Lock()
				served++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if served != 1 {
		t.Errorf("served %d downloads with a limit of 1", served)
	}
	var count, remaining int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT download_count, downloads_remaining FROM goo_download_permissions WHERE order_id = $1`, o.ID).Scan(&count, &remaining)
	if count != 1 || remaining != 0 {
		t.Errorf("download_count %d remaining %d, want 1 and 0", count, remaining)
	}
}

// Customer registration validates the username like the core does.
func TestGooCreateCustomerUsername(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	for i, bad := range []string{"<b>x</b>", "two\nlines", strings.Repeat("a", 61), "zero​width"} {
		if _, err := srv.gooCreateCustomer(ctx, fmt.Sprintf("bad%d@example.test", i), bad, "s3cret-pass", map[string]string{}); err == nil {
			t.Errorf("username %q was accepted", bad)
		}
	}
	if _, err := srv.gooCreateCustomer(ctx, "good@example.test", "good.name-1", "s3cret-pass", map[string]string{}); err != nil {
		t.Errorf("valid username: %v", err)
	}
	if _, err := srv.gooCreateCustomer(ctx, "other@example.test", "Good.Name-1", "s3cret-pass", map[string]string{}); err == nil {
		t.Error("a taken username (other case) was accepted")
	}
	if _, err := srv.gooCreateCustomer(ctx, "gen@example.test", "", "s3cret-pass", map[string]string{"billing_first_name": "Gen"}); err != nil {
		t.Errorf("generated username: %v", err)
	}
}

// gooAuditInclusive sets up a prices-include-tax store with 20% VAT (DE).
func gooAuditInclusive(t *testing.T, srv *Server) {
	t.Helper()
	ctx := context.Background()
	if err := srv.gooSetOpts(ctx, map[string]any{"calc_taxes": true, "prices_include_tax": "yes", "default_country": "DE", "tax_based_on": "billing", "currency": "EUR",
		"goo_test_settings": map[string]any{"enabled": "yes"}}); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSaveTaxRate(ctx, &gooTaxRate{Country: "DE", Rate: 20, Name: "VAT", Priority: 1}); err != nil {
		t.Fatal(err)
	}
}

// Admin "Add item(s)" stores the ex-tax price on a prices-include-tax store.
func TestGooOrderAddProductInclusive(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gooAuditInclusive(t, srv)
	p := gooMakeSimple(t, srv, "Gross", 120, func(p *gooProduct) { p.Virtual = true })
	o := &gooOrder{Status: "pending", Currency: "EUR", PricesIncludeTax: true, Billing: map[string]string{"country": "DE"}, Shipping: map[string]string{}, Meta: map[string]any{}}
	if err := srv.gooOrderAddProduct(ctx, o, p.ID, 2); err != nil {
		t.Fatal(err)
	}
	srv.gooOrderCalcTaxes(ctx, o)
	srv.gooRecalculate(o)
	line := o.ItemsOf("line_item")[0]
	gooAlmost(t, "line subtotal", line.Subtotal, 200)
	gooAlmost(t, "line tax", line.TotalTax, 40)
	gooAlmost(t, "order total", o.Total, 240)
}

// An order placed with a fixed coupon on a prices-include-tax store keeps its totals on Recalculate.
func TestGooOrderRecalculateInclusiveCoupon(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gooAuditInclusive(t, srv)
	p := gooMakeSimple(t, srv, "Gross", 120, func(p *gooProduct) { p.Virtual = true })
	q := gooMakeSimple(t, srv, "Gross2", 60, func(p *gooProduct) { p.Virtual = true })
	for _, cp := range []*gooCoupon{{Code: "twelve", DiscountType: "fixed_cart", Amount: 12}, {Code: "perunit", DiscountType: "fixed_product", Amount: 6}} {
		if err := srv.gooSaveCoupon(ctx, cp); err != nil {
			t.Fatal(err)
		}
	}
	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	cart.sess.Data.Customer.Billing = map[string]string{"country": "DE"}
	if _, err := cart.Add(ctx, p.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cart.Add(ctx, q.ID, 2, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"twelve", "perunit"} {
		if err := cart.ApplyCoupon(ctx, code); err != nil {
			t.Fatal(err)
		}
	}
	form := map[string][]string{"billing_first_name": {"Eva"}, "billing_last_name": {"M"}, "billing_country": {"DE"}, "billing_address_1": {"Weg 1"},
		"billing_city": {"Berlin"}, "billing_state": {"DE-BE"}, "billing_postcode": {"10115"}, "billing_phone": {"123"}, "billing_email": {"eva@example.test"}, "payment_method": {"goo_test"}}
	_, order, errs := srv.gooProcessCheckout(c, form)
	if len(errs) > 0 || order == nil {
		t.Fatalf("checkout: %v", errs)
	}
	o, _ := srv.gooGetOrder(ctx, order.ID)
	// 240 gross - 12 - 6×3 = 210.
	gooAlmost(t, "order total from the cart", o.Total, 210)
	before := map[int64]float64{}
	for _, it := range o.ItemsOf("line_item") {
		before[it.ID] = it.Total
	}
	srv.gooOrderApplyCoupons(ctx, o)
	srv.gooOrderCalcTaxes(ctx, o)
	srv.gooRecalculate(o)
	gooAlmost(t, "order total after Recalculate", o.Total, 210)
	for _, it := range o.ItemsOf("line_item") {
		gooAlmost(t, "line total after Recalculate", it.Total, before[it.ID])
	}
}

// wc/v3 order writes keep the posted line total and do not reset manual line discounts.
func TestGooAPIV3LineTotals(t *testing.T) {
	base, c, srv := gooSite(t)
	gooShopSetup(t, srv)
	p := gooMakeSimple(t, srv, "Desk", 100, nil)
	v3 := gooAdminAPI(t, base, c)
	o := v3("POST", "/orders", map[string]any{"billing": gooTestAddress, "shipping": gooTestAddress,
		"line_items": []any{map[string]any{"product_id": p.ID, "quantity": 1, "subtotal": "100.00", "total": "80.00"}}})
	if o.Code != 201 {
		t.Fatalf("create order: %d %s", o.Code, o.Raw)
	}
	if gooJS(o.Body, "line_items", 0, "subtotal") != "100.00" || gooJS(o.Body, "line_items", 0, "total") != "80.00" || gooJS(o.Body, "total") != "80.00" {
		t.Errorf("discounted line: %s", o.Raw)
	}
	oid := gooInt(gooJ(o.Body, "id"))
	upd := v3("PUT", fmt.Sprintf("/orders/%d", oid), map[string]any{"fee_lines": []any{map[string]any{"name": "Handling", "total": "5.00", "tax_status": "none"}}})
	if upd.Code != 200 || gooJS(upd.Body, "line_items", 0, "total") != "80.00" || gooJS(upd.Body, "total") != "85.00" {
		t.Errorf("after adding a fee: %d %s", upd.Code, upd.Raw)
	}
	// Only a total: the subtotal is the product's price.
	o2 := v3("POST", "/orders", map[string]any{"line_items": []any{map[string]any{"product_id": p.ID, "quantity": 1, "total": "70.00"}}})
	if gooJS(o2.Body, "line_items", 0, "subtotal") != "100.00" || gooJS(o2.Body, "line_items", 0, "total") != "70.00" {
		t.Errorf("total only: %s", o2.Raw)
	}
}
