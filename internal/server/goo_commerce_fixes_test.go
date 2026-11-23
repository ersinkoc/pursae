package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"wpgo/internal/auth"
	"wpgo/internal/store"
)

func TestGooEmailRestrictionWildcards(t *testing.T) {
	ok := []struct{ pattern, email string }{
		{"*@example.com", "ada@example.com"},
		{"*@*.edu", "ada@cs.mit.edu"},
		{"admin*@shop.com", "admin@shop.com"},
		{"admin*@shop.com", "administrator@shop.com"},
	}
	bad := []struct{ pattern, email string }{
		{"*@example.com", "ada@example.com.evil"},
		{"*@example.com", "ada@notexample.com"},
		{"*@*.edu", "ada@school.com"},
		{"a*b*c@d.com", "ab@d.com"},
	}
	for _, c := range ok {
		if !gooEmailAllowed(c.email, []string{c.pattern}) {
			t.Errorf("%q should allow %q", c.pattern, c.email)
		}
	}
	for _, c := range bad {
		if gooEmailAllowed(c.email, []string{c.pattern}) {
			t.Errorf("%q should not allow %q", c.pattern, c.email)
		}
	}
}

// Logging in merges a guest line into the same line already in the account cart. The quantities add;
// a product sold individually stays at one.
func TestGooLoginMergesGuestQuantity(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Mug", 10, nil)
	solo := gooMakeSimple(t, srv, "Poster", 5, func(p *gooProduct) { p.SoldIndividually = true })
	extra := gooMakeSimple(t, srv, "Sticker", 2, nil)
	hash, err := auth.HashPassword("s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	uid, err := srv.store.CreateUser(ctx, store.NewUser{Login: "ada", Email: "ada@example.test", PasswordHash: hash, Role: "customer"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := srv.store.UserByID(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}

	uc := gooTestCtx()
	uc.Set(ctxUser, user)
	userCart := srv.gooCartFor(uc)
	if _, err := userCart.Add(ctx, p.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := userCart.Add(ctx, solo.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	srv.gooSaveSession(uc)

	gc := gooTestCtx()
	guest := srv.gooCartFor(gc)
	if _, err := guest.Add(ctx, p.ID, 3, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := guest.Add(ctx, solo.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := guest.Add(ctx, extra.ID, 2, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	srv.gooSaveSession(gc)

	lc, _ := gin.CreateTestContext(httptest.NewRecorder())
	lc.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	lc.Request.AddCookie(&http.Cookie{Name: gooSessionCookie, Value: guest.sess.Key})
	lc.Set(ctxUser, user)
	merged := srv.gooCartFor(lc)
	qty := map[int64]int{}
	for _, it := range merged.Items {
		qty[it.ProductID] = it.Quantity
	}
	if qty[p.ID] != 4 || qty[solo.ID] != 1 || qty[extra.ID] != 2 {
		t.Fatalf("merged quantities mug=%d poster=%d sticker=%d, want 4, 1 and 2", qty[p.ID], qty[solo.ID], qty[extra.ID])
	}
}

// A fixed cart coupon of 10 across three equal lines stays 10 after Recalculate. Rounding each
// share to 3.33 used to leave the order a cent higher than the cart the customer paid.
func TestGooFixedCartCouponRecalculatePenny(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSetOpts(ctx, map[string]any{"goo_test_settings": map[string]any{"enabled": "yes"}}); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSaveCoupon(ctx, &gooCoupon{Code: "ten", DiscountType: "fixed_cart", Amount: 10}); err != nil {
		t.Fatal(err)
	}
	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	for _, name := range []string{"A", "B", "C"} {
		p := gooMakeSimple(t, srv, name, 10, func(p *gooProduct) { p.Virtual = true })
		if _, err := cart.Add(ctx, p.ID, 1, 0, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := cart.ApplyCoupon(ctx, "ten"); err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "cart total", cart.Total, 20)
	form := map[string][]string{"billing_first_name": {"Ada"}, "billing_last_name": {"L"}, "billing_country": {"US"}, "billing_address_1": {"1 Main"},
		"billing_city": {"LA"}, "billing_state": {"CA"}, "billing_postcode": {"90210"}, "billing_phone": {"555"}, "billing_email": {"ada@example.test"}, "payment_method": {"goo_test"}}
	_, order, errs := srv.gooProcessCheckout(c, form)
	if len(errs) > 0 || order == nil {
		t.Fatalf("checkout: %v", errs)
	}
	o, err := srv.gooGetOrder(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "order total", o.Total, 20)
	srv.gooOrderApplyCoupons(ctx, o)
	srv.gooOrderCalcTaxes(ctx, o)
	srv.gooRecalculate(o)
	gooAlmost(t, "total after recalculate", o.Total, 20)
	var lines float64
	for _, it := range o.ItemsOf("line_item") {
		lines += it.Total
	}
	gooAlmost(t, "line sum after recalculate", lines, 20)
	if cps := o.ItemsOf("coupon"); len(cps) != 1 || cps[0].Total != 10 {
		t.Fatalf("coupon line %+v, want a discount of 10", cps)
	}
}

// Prices entered with tax, rounded at subtotal: three shelf prices of 9.99 charge 29.97, and the
// stored lines add up to that instead of leaving the tax unrounded and the lines a cent short.
func TestGooInclusiveOrderLinesMatchTotal(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSetOpts(ctx, map[string]any{"calc_taxes": true, "prices_include_tax": "yes", "tax_round_at_subtotal": true,
		"default_country": "DE", "tax_based_on": "base", "currency": "EUR", "goo_test_settings": map[string]any{"enabled": "yes"}}); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSaveTaxRate(ctx, &gooTaxRate{Country: "DE", Rate: 19, Name: "MwSt", Priority: 1}); err != nil {
		t.Fatal(err)
	}
	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	cart.sess.Data.Customer.Billing = map[string]string{"country": "DE"}
	for _, name := range []string{"A", "B", "C"} {
		p := gooMakeSimple(t, srv, name, 9.99, func(p *gooProduct) { p.Virtual = true })
		if _, err := cart.Add(ctx, p.ID, 1, 0, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	cart.Calculate()
	gooAlmost(t, "cart total", cart.Total, 29.97)
	form := map[string][]string{"billing_first_name": {"Ada"}, "billing_last_name": {"L"}, "billing_country": {"DE"}, "billing_address_1": {"Weg"},
		"billing_city": {"Berlin"}, "billing_state": {"DE-BE"}, "billing_postcode": {"10115"}, "billing_phone": {"123"}, "billing_email": {"ada@example.test"}, "payment_method": {"goo_test"}}
	_, order, errs := srv.gooProcessCheckout(c, form)
	if len(errs) > 0 || order == nil {
		t.Fatalf("checkout: %v", errs)
	}
	o, err := srv.gooGetOrder(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "order total", o.Total, 29.97)
	var lines, tax float64
	for _, it := range o.ItemsOf("line_item") {
		lines += it.Total
		tax += it.TotalTax
	}
	gooAlmost(t, "lines plus tax", lines+tax, o.Total)
}
