package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// A guest browser: no admin login, its own cookie jar (nonces and the cart session live there).
func gooGuestClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// gooPayPath is the order-pay URL as a path on the test server.
func gooPayPath(t *testing.T, srv *Server, o *gooOrder) string {
	t.Helper()
	u, err := url.Parse(srv.gooOrderPayURL(context.Background(), o))
	if err != nil {
		t.Fatal(err)
	}
	return u.RequestURI()
}

func gooPendingOrder(t *testing.T, srv *Server, name string, price float64) *gooOrder {
	t.Helper()
	ctx := context.Background()
	if err := srv.gooSetOpts(ctx, map[string]any{"goo_test_settings": map[string]any{"enabled": "yes"}}); err != nil {
		t.Fatal(err)
	}
	p := gooMakeSimple(t, srv, name, price, func(p *gooProduct) { p.Virtual = true })
	c := gooTestCtx()
	if _, err := srv.gooCartFor(c).Add(ctx, p.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	o, errs := gxCheckout(t, srv, c, "goo_test")
	if len(errs) > 0 || o == nil {
		t.Fatalf("checkout: %v", errs)
	}
	if err := srv.gooSetStatus(ctx, o, "failed", "Payment declined.", ""); err != nil {
		t.Fatal(err)
	}
	return o
}

// The Pay for order page: right key, wrong key, a customer's order seen as a guest, and a declined
// payment followed by a successful retry.
func TestGooOrderPayAndDeclinedRetry(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	o := gooPendingOrder(t, srv, "Pay Lamp", 25)
	path := gooPayPath(t, srv, o)
	guest := gooGuestClient()

	// Guest + correct key: the payment form.
	code, body, _ := get(t, guest, base+path)
	if code != http.StatusOK || !strings.Contains(body, `id="place_order"`) || !strings.Contains(body, `name="woocommerce_pay"`) {
		t.Fatalf("order-pay page: %d, form missing", code)
	}
	// Wrong key: invalid order, no form.
	_, bad, _ := get(t, guest, base+strings.Replace(path, o.OrderKey, "wc_order_wrongkey", 1))
	if !strings.Contains(bad, "this order is invalid and cannot be paid for") || strings.Contains(bad, `id="place_order"`) {
		t.Error("a wrong order key still shows the payment form")
	}
	// An order owned by a customer asks a guest to log in.
	o2 := gooPendingOrder(t, srv, "Owned Lamp", 30)
	o2.CustomerID = 1
	if err := srv.gooSaveOrder(ctx, o2, false); err != nil {
		t.Fatal(err)
	}
	_, owned, _ := get(t, guest, base+gooPayPath(t, srv, o2))
	if !strings.Contains(owned, "Please log in to your account") || strings.Contains(owned, `id="place_order"`) {
		t.Error("a customer's order is payable by a guest")
	}

	pay := func(holder string) {
		t.Helper()
		_, page, _ := get(t, guest, base+path)
		m := regexp.MustCompile(`name="woocommerce-pay-nonce" value="([^"]+)"`).FindStringSubmatch(page)
		if m == nil {
			t.Fatal("no pay nonce on the page")
		}
		post(t, guest, base+path, url.Values{"woocommerce_pay": {"1"}, "order_id": {itoa64(o.ID)}, "key": {o.OrderKey},
			"payment_method": {"goo_test"}, "goo_test_card_holder": {holder}, "woocommerce-pay-nonce": {m[1]}})
	}
	// Declined: the order stays failed and the page offers the form again.
	pay("decline")
	if got, _ := srv.gooGetOrder(ctx, o.ID); got.Status != "failed" {
		t.Fatalf("declined payment left the order %s", got.Status)
	}
	if _, again, _ := get(t, guest, base+path); !strings.Contains(again, `id="place_order"`) {
		t.Error("no retry form after a declined payment")
	}
	// Retry succeeds.
	pay("Ada")
	got, _ := srv.gooGetOrder(ctx, o.ID)
	if got.Status != "processing" || got.TransactionID == "" {
		t.Fatalf("retry: status %s tx %q", got.Status, got.TransactionID)
	}
	// Paid: the page refuses a second payment.
	if _, done, _ := get(t, guest, base+path); strings.Contains(done, `id="place_order"`) {
		t.Error("a paid order can be paid again")
	}
}

// Cash on delivery is limited to the chosen shipping methods and to virtual orders when enabled.
func TestGooCODAvailability(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gw := gooGatewayRegistry["cod"]
	physical := gooMakeSimple(t, srv, "COD Chair", 40, nil)
	virtual := gooMakeSimple(t, srv, "COD Ebook", 10, func(p *gooProduct) { p.Virtual = true })

	set := func(m map[string]any) {
		t.Helper()
		if err := srv.gooSetOpts(ctx, map[string]any{"cod_settings": m}); err != nil {
			t.Fatal(err)
		}
	}
	cartWith := func(p *gooProduct, chosen string) (*gooCart, func() bool) {
		c := gooTestCtx()
		cart := srv.gooCartFor(c)
		if _, err := cart.Add(ctx, p.ID, 1, 0, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if chosen != "" {
			cart.sess.Data.ChosenShipping = map[string]string{"0": chosen}
		}
		return cart, func() bool { return gw.Available(srv, c, cart) }
	}

	set(map[string]any{"enabled": "yes", "enable_for_virtual": "yes"})
	if _, ok := cartWith(physical, "flat_rate:1"); !ok() {
		t.Error("COD with no method restriction is unavailable")
	}
	set(map[string]any{"enabled": "yes", "enable_for_methods": []any{"flat_rate"}, "enable_for_virtual": "yes"})
	if _, ok := cartWith(physical, "flat_rate:1"); !ok() {
		t.Error("COD unavailable for the allowed method")
	}
	if _, ok := cartWith(physical, "free_shipping:2"); ok() {
		t.Error("COD available for a method it is not enabled for")
	}
	if _, ok := cartWith(virtual, ""); !ok() {
		t.Error("COD unavailable for a virtual order although enabled for virtual")
	}
	set(map[string]any{"enabled": "yes", "enable_for_virtual": "no"})
	if _, ok := cartWith(virtual, ""); ok() {
		t.Error("COD available for a virtual order although disabled for virtual")
	}
}

// Cash on delivery does not release downloadable files. WooCommerce keeps an order that contains a
// downloadable item on hold until the payment is recorded; processing would grant the files at once.
func TestGooCODDownloadableStaysUnpaid(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gw := gooGatewayRegistry["cod"]
	ebook := gooMakeSimple(t, srv, "COD Manual", 12, func(p *gooProduct) {
		p.Virtual, p.Downloadable = true, true
		p.Downloads = []gooDownload{{Name: "manual.pdf", File: "https://files.example.test/manual.pdf"}}
	})
	chair := gooMakeSimple(t, srv, "COD Chair Paid Later", 40, nil)
	perms := func(id int64) int {
		t.Helper()
		var n int
		if err := srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_download_permissions WHERE order_id = $1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	place := func(p *gooProduct, total float64, extra ...*gooProduct) *gooOrder {
		t.Helper()
		items := []*gooOrderItem{{Type: "line_item", Name: p.Name, ProductID: p.ID, Quantity: 1, Subtotal: total, Total: total, Meta: map[string]any{}}}
		for _, x := range extra {
			items = append(items, &gooOrderItem{Type: "line_item", Name: x.Name, ProductID: x.ID, Quantity: 1, Subtotal: 1, Total: 1, Meta: map[string]any{}})
		}
		o := &gooOrder{Status: "pending", Currency: "USD", Total: total, PaymentMethod: "cod",
			Billing: map[string]string{"email": "cod@example.test"}, Shipping: map[string]string{}, Meta: map[string]any{}, Items: items}
		if err := srv.gooSaveOrder(ctx, o, true); err != nil {
			t.Fatal(err)
		}
		if _, err := gw.ProcessPayment(srv, gooTestCtx(), o); err != nil {
			t.Fatal(err)
		}
		return o
	}

	digital := place(ebook, 12)
	got, err := srv.gooGetOrder(ctx, digital.ID)
	if err != nil || got.Status != "on-hold" || got.DatePaid != nil || perms(digital.ID) != 0 {
		t.Fatalf("downloadable COD: status %s paid %v perms %d err %v", got.Status, got != nil && got.DatePaid != nil, perms(digital.ID), err)
	}
	if err := srv.gooSetStatus(ctx, got, "completed", "Payment received.", ""); err != nil {
		t.Fatal(err)
	}
	if perms(digital.ID) == 0 {
		t.Fatal("completing the order did not grant the download")
	}

	physical := place(chair, 40)
	got, err = srv.gooGetOrder(ctx, physical.ID)
	if err != nil || got.Status != "processing" || perms(physical.ID) != 0 {
		t.Fatalf("physical COD: status %s perms %d err %v", got.Status, perms(physical.ID), err)
	}

	mixed := place(chair, 41, ebook)
	got, err = srv.gooGetOrder(ctx, mixed.ID)
	if err != nil || got.Status != "on-hold" || perms(mixed.ID) != 0 {
		t.Fatalf("mixed COD: status %s perms %d err %v", got.Status, perms(mixed.ID), err)
	}
}

// The store's dynamic blocks render the same markup as their shortcodes, and nothing while off.
func TestGooDynamicBlocks(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	lamp := gooMakeSimple(t, srv, "Block Lamp", 12, nil)
	page := &store.Post{Type: "page", Slug: "blocks", Title: "Blocks", Status: "publish", AuthorID: 1, Meta: map[string]any{},
		Content: `<!-- wp:goocommerce/products {"columns":2,"limit":4} /-->` + "\n\n" +
			`<!-- wp:goocommerce/featured-product {"productId":` + itoa64(lamp.ID) + `} /-->` + "\n\n" +
			`<!-- wp:goocommerce/product-categories /-->` + "\n\n" +
			`<!-- wp:goocommerce/mini-cart /-->` + "\n\n" +
			`<!-- wp:goocommerce/product-search /-->`}
	if err := srv.store.InsertPost(ctx, page); err != nil {
		t.Fatal(err)
	}
	_, body, _ := get(t, c, base+"/blocks/")
	gooMustContain(t, "blocks", body, `<ul class="products columns-2">`, `>Block Lamp</h2>`, `widget_shopping_cart`, `product-search`)
	if strings.Contains(body, "wp:goocommerce") {
		t.Error("an unrendered block comment reached the page")
	}
}
