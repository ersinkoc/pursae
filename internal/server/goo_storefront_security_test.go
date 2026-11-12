package server

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Cash on delivery does not take a payment: a virtual gift card bought with it stays on-hold and is
// not issued (or e-mailed) until the merchant marks the order paid, like the downloadable-file rule.
func TestGooSecurity_CODGiftCardNotIssuedUnpaid(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSetOpts(ctx, map[string]any{"cod_settings": map[string]any{"enabled": "yes"}}); err != nil {
		t.Fatal(err)
	}
	gp := gooNewProduct(gcType)
	gp.Name = "Store gift card"
	gp = gxSavePanels(t, srv, gp, gcKey, url.Values{"_gift_card_amounts": {"50"}})
	if err := srv.gooSetExtension(ctx, gcKey, true); err != nil {
		t.Fatal(err)
	}
	buyer := gooTestCtx()
	cart := srv.gooCartFor(buyer)
	cart.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "CA", "postcode": "90210", "email": "buyer@example.test"}
	if _, err := cart.Add(ctx, gp.ID, 1, 0, nil, map[string][]string{"wc_gc_giftcard_amount": {"50"}, "wc_gc_giftcard_to": {"attacker@example.test"},
		"wc_gc_giftcard_from": {"Eve"}}, nil); err != nil {
		t.Fatal(err)
	}
	o, errs := gxCheckout(t, srv, buyer, "cod")
	if len(errs) > 0 || o == nil {
		t.Fatalf("COD checkout: %v", errs)
	}
	if o.Status != "on-hold" {
		t.Errorf("COD gift card order status %q, want on-hold", o.Status)
	}
	if cards := srv.gcCardsForEmail(ctx, "attacker@example.test"); len(cards) != 0 {
		t.Fatalf("unpaid cash-on-delivery order (status %s) issued %d gift card(s), balance %.2f", o.Status, len(cards), cards[0].Balance)
	}
	// The merchant records the payment: the card is issued then.
	if err := srv.gooSetStatus(ctx, o, "processing", "Payment received.", ""); err != nil {
		t.Fatal(err)
	}
	if cards := srv.gcCardsForEmail(ctx, "attacker@example.test"); len(cards) != 1 {
		t.Errorf("paid order issued %d gift card(s), want 1", len(cards))
	}
}

// gooPayOrderWith posts the order-pay form as the guest who owns the order.
func gooPayOrderWith(t *testing.T, base string, srv *Server, o *gooOrder, method string) *gooOrder {
	t.Helper()
	path := gooPayPath(t, srv, o)
	guest := gooGuestClient()
	_, page, _ := get(t, guest, base+path)
	m := regexp.MustCompile(`name="woocommerce-pay-nonce" value="([^"]+)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no pay nonce on the page")
	}
	post(t, guest, base+path, url.Values{"woocommerce_pay": {"1"}, "order_id": {itoa64(o.ID)}, "key": {o.OrderKey},
		"payment_method": {method}, "woocommerce-pay-nonce": {m[1]}})
	got, err := srv.gooGetOrder(context.Background(), o.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// Pay for order applies the same gateway availability as the checkout: cash on delivery switched off
// for virtual orders cannot be chosen for a virtual order on the order-pay page.
func TestGooSecurity_OrderPayCODRespectsVirtualRestriction(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	o := gooPendingOrder(t, srv, "Virtual Lamp", 25)
	if err := srv.gooSetOpts(ctx, map[string]any{"cod_settings": map[string]any{"enabled": "yes", "enable_for_virtual": "no"}}); err != nil {
		t.Fatal(err)
	}
	_, page, _ := get(t, gooGuestClient(), base+gooPayPath(t, srv, o))
	if strings.Contains(page, `value="cod"`) {
		t.Error("order-pay page offers cash on delivery for a virtual order")
	}
	if got := gooPayOrderWith(t, base, srv, o, "cod"); got.Status != "failed" || got.PaymentMethod == "cod" {
		t.Errorf("virtual order paid with disabled-for-virtual COD on order-pay: status %s, method %s", got.Status, got.PaymentMethod)
	}
}

// A physical order shipped with a method cash on delivery is enabled for can still be paid with it on
// order-pay; one shipped with another method cannot.
func TestGooSecurity_OrderPayCODShippingMethods(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	gxTestGateway(t, srv)
	z := &gooZone{Name: "United States", Locations: []gooZoneLocation{{Code: "US", Type: "country"}}}
	if err := srv.gooSaveZone(ctx, z); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.gooAddMethod(ctx, z.ID, "flat_rate", map[string]any{"cost": "5"}); err != nil {
		t.Fatal(err)
	}
	newOrder := func(name string) *gooOrder {
		p := gooMakeSimple(t, srv, name, 20, nil)
		c := gooTestCtx()
		if _, err := srv.gooCartFor(c).Add(ctx, p.ID, 1, 0, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		o, errs := gxCheckout(t, srv, c, "goo_test")
		if len(errs) > 0 || o == nil {
			t.Fatalf("checkout: %v", errs)
		}
		if len(o.ItemsOf("shipping")) == 0 {
			t.Fatal("setup: order has no shipping line")
		}
		if err := srv.gooSetStatus(ctx, o, "failed", "Payment declined.", ""); err != nil {
			t.Fatal(err)
		}
		return o
	}
	if err := srv.gooSetOpts(ctx, map[string]any{"cod_settings": map[string]any{"enabled": "yes", "enable_for_virtual": "no",
		"enable_for_methods": []any{"local_pickup"}}}); err != nil {
		t.Fatal(err)
	}
	if got := gooPayOrderWith(t, base, srv, newOrder("Crate"), "cod"); got.Status != "failed" {
		t.Errorf("COD limited to local pickup paid a flat rate order: status %s", got.Status)
	}
	if err := srv.gooSetOpts(ctx, map[string]any{"cod_settings": map[string]any{"enabled": "yes", "enable_for_virtual": "no",
		"enable_for_methods": []any{"flat_rate"}}}); err != nil {
		t.Fatal(err)
	}
	if got := gooPayOrderWith(t, base, srv, newOrder("Barrel"), "cod"); got.Status != "processing" || got.PaymentMethod != "cod" {
		t.Errorf("COD enabled for flat rate refused a flat rate order: status %s, method %s", got.Status, got.PaymentMethod)
	}
}

// Products that Memberships hides completely from a visitor are not shown by the store shortcodes an
// author places in a post, whatever their catalog-visibility attribute says.
func TestGooSecurity_ShortcodeMembershipHiddenProducts(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gooMakeSimple(t, srv, "Open Bowl", 10, nil)
	vip := gooMakeSimple(t, srv, "Velvet Bowl", 30, nil)
	plan := &gooMembershipPlan{Name: "Velvet", AccessMethod: "manual", AccessLength: "unlimited",
		Rules: gmemRules{ProductIDs: []int64{vip.ID}, ProductRestrict: "view"}}
	if err := srv.gmemSavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSetExtension(ctx, gmemKey, true); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSetOpts(ctx, map[string]any{"ext_" + gmemKey + "_settings": map[string]any{"restriction_mode": "hide"}}); err != nil {
		t.Fatal(err)
	}
	if !srv.gooHiddenProduct(ctx, vip.ID) {
		t.Fatal("setup: product not hidden from visitors")
	}
	if out := srv.gooShortcodes(gooTestCtx(), `[products]`); strings.Contains(out, "Velvet Bowl") || !strings.Contains(out, "Open Bowl") {
		t.Fatalf("control: plain [products] lists the hidden product or misses the open one")
	}
	for _, sc := range []string{`[products visibility="hidden"]`, `[product_page id="` + itoa64(vip.ID) + `"]`, `[add_to_cart id="` + itoa64(vip.ID) + `"]`} {
		if out := srv.gooShortcodes(gooTestCtx(), sc); strings.Contains(out, "Velvet Bowl") || strings.Contains(out, `data-product_id="`+itoa64(vip.ID)+`"`) {
			t.Errorf("%s shows a product Memberships hides from the visitor", sc)
		}
	}
	if out := srv.gooShortcodes(gooTestCtx(), `[products visibility="hidden"]`); !strings.Contains(out, "Open Bowl") {
		t.Error(`[products visibility="hidden"] lost the open product`)
	}
}

// An anonymous shop URL does not turn into an unbounded number of term lookups and SQL conditions:
// filter_<attr> slugs are deduplicated and capped, and so is the number of filter keys.
func TestGooSecurity_ShopFiltersBoundedSlugs(t *testing.T) {
	_, _, srv := gooSite(t)
	slugs := strings.TrimSuffix(strings.Repeat("x,", 3000), ",")
	c, _ := gxGetCtx("/shop/?filter_color=" + slugs)
	q := gooProductQuery{Visibility: "catalog", PerPage: 12, Page: 1}
	srv.gooApplyFilters(c, &q)
	if n := len(q.AttrTerms); n != 1 {
		t.Errorf("3000 duplicate filter_color slugs produced %d SQL conditions, want 1", n)
	}
	var distinct []string
	for i := 0; i < 500; i++ {
		distinct = append(distinct, "s"+itoa64(int64(i)))
	}
	keys := url.Values{}
	for i := 0; i < 40; i++ {
		keys.Set("filter_a"+itoa64(int64(i)), strings.Join(distinct, ","))
	}
	c, _ = gxGetCtx("/shop/?" + keys.Encode())
	q = gooProductQuery{Visibility: "catalog", PerPage: 12, Page: 1}
	srv.gooApplyFilters(c, &q)
	if n, max := len(q.AttrTerms), gooMaxFilterKeys*gooMaxFilterSlugs; n > max {
		t.Errorf("40 filter keys of 500 slugs produced %d conditions, want at most %d", n, max)
	}
	c, _ = gxGetCtx("/shop/?filter_color=" + strings.Join(distinct, ",") + "&query_type_color=or")
	q = gooProductQuery{Visibility: "catalog", PerPage: 12, Page: 1}
	srv.gooApplyFilters(c, &q)
	if n := len(q.AttrTerms["pa_color"]); n > gooMaxFilterSlugs {
		t.Errorf("OR filter kept %d term ids, want at most %d", n, gooMaxFilterSlugs)
	}
	if _, _, err := srv.gooQueryProducts(context.Background(), q); err != nil {
		t.Errorf("bounded filter query: %v", err)
	}
}

// The no-nonce AJAX update_shipping_method action only accepts the cart's real package indexes and
// the rates offered for them, and stores at most one choice per package.
func TestGooSecurity_CartAjaxShippingMethodKeysBounded(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	z := &gooZone{Name: "United States", Locations: []gooZoneLocation{{Code: "US", Type: "country"}}}
	if err := srv.gooSaveZone(ctx, z); err != nil {
		t.Fatal(err)
	}
	for _, cost := range []string{"5", "9"} {
		if _, err := srv.gooAddMethod(ctx, z.ID, "flat_rate", map[string]any{"cost": cost}); err != nil {
			t.Fatal(err)
		}
	}
	p := gooMakeSimple(t, srv, "Mug", 10, nil)
	visitor := gooTestCtx()
	cart := srv.gooCartFor(visitor)
	addr := map[string]string{"country": "US", "state": "CA", "postcode": "90210"}
	cart.sess.Data.Customer.Billing, cart.sess.Data.Customer.Shipping = addr, addr
	if _, err := cart.Add(ctx, p.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	cart.Calculate()
	if len(cart.Packages) != 1 || len(cart.Packages[0].Rates) != 2 {
		t.Fatalf("setup: %d packages", len(cart.Packages))
	}
	second := cart.Packages[0].Rates[1].ID
	form := url.Values{}
	for i := 1; i < 500; i++ {
		form.Set("shipping_method["+itoa64(int64(i))+"]", second)
	}
	form.Set("shipping_method[0]", second)
	w, c := gxPostRoute(srv, func(s *Server, c *gin.Context, _ string) bool {
		_ = c.Request.ParseForm()
		s.gooAjax(c, "update_shipping_method")
		return true
	}, visitor, "/?wc-ajax=update_shipping_method", form)
	if w.Code != http.StatusOK {
		t.Fatalf("update_shipping_method: %d", w.Code)
	}
	chosen := srv.gooSessionFor(c).Data.ChosenShipping
	if len(chosen) != 1 || chosen["0"] != second {
		t.Errorf("chosen shipping after 500 keys = %v, want only package 0 = %s", chosen, second)
	}
	// A rate that is not offered for the package is ignored.
	_, c = gxPostRoute(srv, func(s *Server, c *gin.Context, _ string) bool {
		_ = c.Request.ParseForm()
		s.gooAjax(c, "update_shipping_method")
		return true
	}, visitor, "/?wc-ajax=update_shipping_method", url.Values{"shipping_method[0]": {"free_shipping:999"}})
	if got := srv.gooSessionFor(c).Data.ChosenShipping["0"]; got != second {
		t.Errorf("unoffered rate replaced the choice: %q", got)
	}
}

// The layered nav and Active filters widgets read the same anonymous filter_<attr> list as the shop
// query and are bounded the same way: deduplicated, capped slugs and keys, so a huge comma list gives
// a bounded number of chips, links and term lookups.
func TestGooSecurity_FilterWidgetsBoundedSlugs(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSaveAttribute(ctx, &gooAttribute{Name: "Glaze", HasArchives: true}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Celadon", "Ash"} {
		if err := srv.store.InsertTerm(ctx, termFor("pa_glaze", name)); err != nil {
			t.Fatal(err)
		}
	}
	gooMakeSimple(t, srv, "Bowl", 10, func(p *gooProduct) {
		p.Attributes = []gooProductAttr{{Taxonomy: "pa_glaze", Options: []string{"celadon", "ash"}, Visible: true}}
	})
	var junk []string
	for i := 0; i < 3000; i++ {
		junk = append(junk, "zz"+itoa64(int64(i)))
	}
	list := "celadon,celadon," + strings.Join(junk, ",")
	keys := url.Values{"filter_glaze": {list}}
	for i := 0; i < 40; i++ {
		keys.Set("filter_x"+itoa64(int64(i)), list)
	}
	keys.Set("orderby", strings.Repeat("j", 100000))
	c, _ := gxGetCtx("/shop/?" + keys.Encode())
	active := srv.gooWidgetActiveFilters(c, "")
	if n, max := strings.Count(active, `<li class="chosen">`), gooMaxFilterSlugs; n == 0 || n > max {
		t.Errorf("Active filters rendered %d chips, want 1..%d", n, max)
	}
	if strings.Count(active, ">Celadon<") != 1 {
		t.Errorf("Active filters: the known term Celadon should show once by name")
	}
	if len(active) > 200_000 {
		t.Errorf("Active filters output is %d bytes for 41 keys of 3000 slugs", len(active))
	}
	c, _ = gxGetCtx("/shop/?filter_glaze=" + url.QueryEscape(list) + "&junk=" + strings.Repeat("j", 100000))
	nav := srv.gooWidgetLayeredNav(c, "glaze", "", "and")
	if !strings.Contains(nav, "Celadon") || !strings.Contains(nav, "chosen") {
		t.Fatalf("layered nav lost the chosen term: %q", nav)
	}
	for _, href := range regexp.MustCompile(`href="([^"]*)"`).FindAllStringSubmatch(nav, -1) {
		u, err := url.Parse(strings.ReplaceAll(href[1], "&amp;", "&"))
		if err != nil {
			t.Fatal(err)
		}
		if n := len(strings.Split(u.Query().Get("filter_glaze"), ",")); n > gooMaxFilterSlugs+1 {
			t.Errorf("layered nav link carries %d slugs, want at most %d", n, gooMaxFilterSlugs+1)
		}
		if len(href[1]) > 20_000 {
			t.Errorf("layered nav link is %d bytes", len(href[1]))
		}
	}
	// A normal selection still works: two real slugs, each removable.
	c, _ = gxGetCtx("/shop/?filter_glaze=celadon,ash")
	active = srv.gooWidgetActiveFilters(c, "")
	if !strings.Contains(active, ">Celadon<") || !strings.Contains(active, ">Ash<") {
		t.Errorf("Active filters for celadon,ash = %q", active)
	}
}
