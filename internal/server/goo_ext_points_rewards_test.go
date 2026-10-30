package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestGooExtPoints(t *testing.T) {
	base, cl, srv := gooSite(t)
	ctx := context.Background()
	gxaRecordMails(t)
	gxaEnableGateways(t, srv)

	a := gooMakeSimple(t, srv, "Tea", 10, func(p *gooProduct) { p.Virtual = true })
	b := gooMakeSimple(t, srv, "Teapot", 30, func(p *gooProduct) { p.Virtual = true })

	// Off: orders earn nothing.
	u0 := gxaCustomer(t, srv, "early@example.test")
	c0 := gxaCtx(u0, http.MethodGet, "/", nil)
	_, _ = srv.gooCartFor(c0).Add(c0, b.ID, 1, 0, nil, nil, nil)
	if _, _, errs := srv.gooProcessCheckout(c0, gxaCheckoutForm(u0.Email, "goo_test")); len(errs) > 0 {
		t.Fatal(errs)
	}
	if n := srv.gptsBalance(ctx, u0.ID); n != 0 {
		t.Errorf("points earned while off: %d", n)
	}

	gxaOn(t, srv, gptsKey, true)
	gxaSetExt(t, srv, gptsKey, map[string]any{"account_signup_points": 50.0, "product_review_points": 25.0})
	a = gxaSavePanel(t, srv, "wc_points_rewards", a, url.Values{"_wc_points_earned": {"200%"}})
	if gxaStr(a.Meta, "_wc_points_earned") != "200%" {
		t.Fatalf("panel save: %v", a.Meta)
	}
	if h := string(gxaPanel(t, "wc_points_rewards").Render(srv, gooTestCtx(), a)); !strings.Contains(h, `value="200%"`) {
		t.Errorf("panel render: %s", h)
	}

	// Account sign-up points.
	u := gxaCustomer(t, srv, "loyal@example.test")
	if n := srv.gptsBalance(ctx, u.ID); n != 50 {
		t.Fatalf("signup points: %d", n)
	}
	c := gxaCtx(u, http.MethodGet, "/", nil)
	if h := string(srv.gooHTML(c, "single_before_add_to_cart", a)); !strings.Contains(h, "earn <strong>20</strong> Points") {
		t.Errorf("product message: %s", h)
	}

	// An order earns 2 × 10 × 200% + 30 = 50 points when processing.
	cart := srv.gooCartFor(c)
	_, _ = cart.Add(c, a.ID, 1, 0, nil, nil, nil)
	_, _ = cart.Add(c, b.ID, 1, 0, nil, nil, nil)
	if h := srv.gptsCartMessages(c); !strings.Contains(h, "earn <strong>50</strong> Points") || !strings.Contains(h, "Apply Discount") {
		t.Errorf("cart messages: %s", h)
	}
	_, o, errs := srv.gooProcessCheckout(c, gxaCheckoutForm(u.Email, "goo_test"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if n := srv.gptsBalance(ctx, u.ID); n != 100 {
		t.Fatalf("balance after the order: %d", n)
	}
	o, _ = srv.gooGetOrder(ctx, o.ID)
	_ = srv.gooSetStatus(ctx, o, "completed", "", "")
	if n := srv.gptsBalance(ctx, u.ID); n != 100 {
		t.Errorf("points awarded twice: %d", n)
	}

	// Redeem 100 points = $1 on the next order.
	nonce := srv.nonce(gxaCtx(u, http.MethodGet, "/", nil), "goo-points-redeem")
	c2 := gxaCtx(u, http.MethodPost, "/cart/", url.Values{"goo_module": {"points_rewards"}, "goo_points_redeem": {"1"}, "_wpnonce": {nonce}})
	cart2 := srv.gooCartFor(c2)
	if _, err := cart2.Add(c2, b.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !gptsFrontRoute(srv, c2, "cart") {
		t.Fatal("the redeem form was not handled")
	}
	if len(cart2.Fees) != 1 || cart2.Fees[0].Name != "Points redemption" {
		t.Fatalf("fees: %+v", cart2.Fees)
	}
	gooAlmost(t, "discounted total", cart2.Total, 29)
	_, o2, errs := srv.gooProcessCheckout(c2, gxaCheckoutForm(u.Email, "goo_test"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	// 100 − 100 redeemed + 30 earned on the line total (a negative fee does not reduce the lines).
	if n := srv.gptsBalance(ctx, u.ID); n != 30 {
		t.Errorf("balance after redeeming: %d", n)
	}
	if v, _ := gptsSessionPoints(srv.gooSessionFor(c2)); v != 0 {
		t.Error("the redemption stays in the session after the order")
	}
	// Cancelling gives the redeemed points back and takes the earned ones.
	o2, _ = srv.gooGetOrder(ctx, o2.ID)
	if err := srv.gooSetStatus(ctx, o2, "cancelled", "", ""); err != nil {
		t.Fatal(err)
	}
	if n := srv.gptsBalance(ctx, u.ID); n != 100 {
		t.Errorf("balance after cancelling: %d", n)
	}

	// Reviews earn points once per product.
	_, _ = srv.store.Pool.Exec(ctx, `INSERT INTO comments (post_id, user_id, content, type, status) VALUES ($1, $2, 'Lovely', 'review', 'approved'), ($1, $2, 'Again', 'review', 'approved')`, a.ID, u.ID)
	srv.gooFire(ctx, "hourly", nil)
	srv.gooFire(ctx, "hourly", nil)
	if n := srv.gptsBalance(ctx, u.ID); n != 125 {
		t.Errorf("balance after a review: %d", n)
	}
	if uu, _ := srv.store.UserByID(ctx, u.ID); gxaInt(uu.Meta, "goo_points_balance") != 125 {
		t.Errorf("cached balance: %v", uu.Meta["goo_points_balance"])
	}

	// My Account.
	if h := string(gptsAccountRender(srv, c, "")); !strings.Contains(h, "<strong>125</strong>") || !strings.Contains(h, "Points earned for purchase") {
		t.Errorf("account: %.400s", h)
	}

	// Admin: adjust, log, categories.
	body := gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=points-and-rewards", "loyal@example.test", "Adjust points")
	code, _, loc := post(t, cl, base+"/wp-admin/goocommerce?page=points-and-rewards", url.Values{"_wpnonce": {formNonce(t, body)}, "points_action": {"adjust"},
		"customer": {u.Email}, "points": {"-25"}, "mode": {"add"}, "note": {"Goodwill"}})
	if code != http.StatusFound || !strings.Contains(loc, "goo-msg") {
		t.Fatalf("adjust: %d %s", code, loc)
	}
	if n := srv.gptsBalance(ctx, u.ID); n != 100 {
		t.Errorf("balance after the adjustment: %d", n)
	}
	gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=points-and-rewards&tab=log&customer="+itoa64(u.ID), "Points adjusted by the store", "Goodwill")
	gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=points-and-rewards&tab=categories", "Category Points")

	// Expiry: earned points older than the period expire.
	gxaSetExt(t, srv, gptsKey, map[string]any{"points_expiry_months": 1.0})
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE goo_points_log SET date = now() - interval '2 months' WHERE user_id = $1`, u.ID)
	srv.gooFire(ctx, "daily", nil)
	if n := srv.gptsBalance(ctx, u.ID); n != 0 {
		t.Errorf("balance after expiry: %d", n)
	}
}
