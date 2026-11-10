package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// gsubFakeCard is a card gateway with stored tokens: it records the renewals it is asked to charge.
type gsubFakeCard struct {
	gooTestGateway
	charged []string
}

func (g *gsubFakeCard) ID() string { return "fake_card" }
func (g *gsubFakeCard) ProcessRenewal(s *Server, ctx context.Context, o *gooOrder, token string) (string, error) {
	g.charged = append(g.charged, token)
	if token == "bad" {
		return "", errors.New("card declined")
	}
	return "txn_" + token, nil
}
func (g *gsubFakeCard) RenewalNeedsToken() bool { return true }

func TestGooSubscriptionsAutomaticRenewalGateway(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	mails := gxaRecordMails(t)
	gxaOn(t, srv, gsubKey, true)
	fake := &gsubFakeCard{}
	gooRegisterGateway(fake)
	t.Cleanup(func() { delete(gooGatewayRegistry, "fake_card") })
	u := gxaCustomer(t, srv, "auto@example.test")
	mk := func(method, token string) *gooSubscription {
		next := time.Now().Add(-time.Hour)
		sub := &gooSubscription{CustomerID: u.ID, Status: "active", Period: "month", Interval: 1, Currency: "USD", Start: time.Now().AddDate(0, -1, 0),
			NextPayment: &next, Billing: map[string]string{"email": u.Email}, Shipping: map[string]string{}, PaymentMethod: method,
			Items: []gsubItem{{Name: "Box", Qty: 1, Total: 10}}, Meta: map[string]any{}}
		if token != "" {
			sub.Meta["_payment_token"] = token
		}
		if err := srv.gsubSave(ctx, sub); err != nil {
			t.Fatal(err)
		}
		return sub
	}
	if !srv.gsubManualRenewal(mk("cheque", "")) {
		t.Error("cheque renews automatically")
	}
	if srv.gsubManualRenewal(mk("goo_test", "")) {
		t.Error("the test gateway needs no token")
	}
	noTok := mk("fake_card", "")
	if !srv.gsubManualRenewal(noTok) {
		t.Error("a token gateway without a stored token renews automatically")
	}
	good, bad := mk("fake_card", "tok_1"), mk("fake_card", "bad")
	if srv.gsubManualRenewal(good) {
		t.Error("a stored token should renew automatically")
	}
	srv.gsubScheduler(ctx)
	if len(fake.charged) != 2 {
		t.Fatalf("charged tokens: %v", fake.charged)
	}
	g, _ := srv.gsubGet(ctx, good.ID)
	if o, err := srv.gooGetOrder(ctx, g.LastOrderID); err != nil || !containsStr(gooPaidStatuses, o.Status) {
		t.Errorf("tokenised renewal order not paid: %v %v", o, err)
	}
	b, _ := srv.gsubGet(ctx, bad.ID)
	if b.Status != "on-hold" {
		t.Errorf("declined renewal: subscription %s", b.Status)
	}
	// Manual renewal: the dedicated invoice e-mail, not the generic one.
	if !mails.has("customer_renewal_invoice") {
		t.Error("no customer_renewal_invoice e-mail")
	}
	if mails.has("customer_invoice") {
		t.Error("generic customer_invoice e-mail sent for a renewal")
	}
}

func TestGooSubscriptionsAcceptManualRenewalsAndButton(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxaEnableGateways(t, srv)
	gxaOn(t, srv, gsubKey, true)
	box := gooNewProduct("subscription")
	box.Name, box.RegularPrice = "Box", gooFloatPtr(10)
	plain := gooNewProduct("simple")
	plain.Name, plain.RegularPrice = "Mug", gooFloatPtr(5)
	subCart := &gooCart{Items: []*gooCartItem{{product: box}}}
	mugCart := &gooCart{Items: []*gooCartItem{{product: plain}}}
	c := gxaCtx(nil, http.MethodGet, "/", nil)
	all := []*gooGatewayHandle{srv.gooGateway("cheque"), srv.gooGateway("goo_test")}
	ids := func(g []*gooGatewayHandle) (o []string) {
		for _, h := range g {
			o = append(o, h.ID())
		}
		return
	}
	if got := ids(gsubGatewayFilter(srv, c, subCart, all)); len(got) != 1 || got[0] != "goo_test" {
		t.Errorf("manual renewals off: %v", got)
	}
	if got := ids(gsubGatewayFilter(srv, c, mugCart, all)); len(got) != 2 {
		t.Errorf("cart without subscriptions filtered: %v", got)
	}
	gxaSetExt(t, srv, gsubKey, map[string]any{"accept_manual_renewals": true})
	if got := ids(gsubGatewayFilter(srv, c, subCart, all)); len(got) != 2 {
		t.Errorf("manual renewals on: %v", got)
	}

	if got := srv.gooPlaceOrderText(ctx, mugCart); got != "Place order" {
		t.Errorf("plain cart button: %q", got)
	}
	if got := srv.gooPlaceOrderText(ctx, subCart); got != "Sign up now" {
		t.Errorf("subscription cart button: %q", got)
	}
	gxaSetExt(t, srv, gsubKey, map[string]any{"order_button_text": "Subscribe"})
	if got := srv.gooPlaceOrderText(ctx, subCart); got != "Subscribe" {
		t.Errorf("custom button: %q", got)
	}
}

func TestGooSubscriptionsSuspensionCountResets(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxaOn(t, srv, gsubKey, true)
	gxaSetExt(t, srv, gsubKey, map[string]any{"max_customer_suspensions": 1.0})
	u := gxaCustomer(t, srv, "susp@example.test")
	next := time.Now().AddDate(0, 0, 3)
	sub := &gooSubscription{CustomerID: u.ID, Status: "active", Period: "month", Interval: 1, Currency: "USD", Start: time.Now().AddDate(0, -1, 0),
		NextPayment: &next, Billing: map[string]string{"email": u.Email}, Shipping: map[string]string{},
		Items: []gsubItem{{Name: "Box", Qty: 1, Total: 10}}, Meta: map[string]any{"_suspension_count": float64(1)}}
	if err := srv.gsubSave(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if a := srv.gsubCustomerActions(ctx, sub); len(a) > 0 && a[0].Value == "on-hold" {
		t.Fatal("suspension offered over the limit")
	}
	o, err := srv.gsubCreateRenewal(ctx, sub, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.gooPaymentComplete(ctx, o, "txn"); err != nil {
		t.Fatal(err)
	}
	sub, _ = srv.gsubGet(ctx, sub.ID)
	if gxaInt(sub.Meta, "_suspension_count") != 0 {
		t.Errorf("count not reset: %v", sub.Meta["_suspension_count"])
	}
	found := false
	for _, a := range srv.gsubCustomerActions(ctx, sub) {
		found = found || a.Value == "on-hold"
	}
	if !found {
		t.Error("suspension not offered again after a paid renewal")
	}
}
