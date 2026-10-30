package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// gxPostRoute calls a front route with a POST form on the given visitor context's session.
func gxPostRoute(srv *Server, route gooFrontRoute, sessionFrom *gin.Context, target string, form url.Values) (*httptest.ResponseRecorder, *gin.Context) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request.Header.Set("X-Requested-With", "XMLHttpRequest")
	if v, ok := sessionFrom.Get(gooCtxSession); ok {
		c.Set(gooCtxSession, v)
	}
	route(srv, c, "")
	return w, c
}

func TestGooExtGiftCards(t *testing.T) {
	base, client, srv := gooSite(t)
	ctx := context.Background()
	gxTestGateway(t, srv)
	z := &gooZone{Name: "United States", Locations: []gooZoneLocation{{Code: "US", Type: "country"}}}
	if err := srv.gooSaveZone(ctx, z); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.gooAddMethod(ctx, z.ID, "flat_rate", map[string]any{"cost": "5"}); err != nil {
		t.Fatal(err)
	}

	gp := gooNewProduct(gcType)
	gp.Name = "Store gift card"
	gp = gxSavePanels(t, srv, gp, gcKey, url.Values{"_gift_card_amounts": {"50, 25"}, "_gift_card_custom_amount": {"yes"}, "_gift_card_min": {"10"},
		"_gift_card_max": {"200"}, "_gift_card_expiration_days": {"365"}})
	if gxStr(gp.Meta["_gift_card_amounts"]) != "25, 50" || !gp.Virtual || gp.RegularPrice == nil || *gp.RegularPrice != 25 {
		t.Fatalf("saved gift card product: %+v %v", gp.Meta, gp.RegularPrice)
	}
	shirt := gooMakeSimple(t, srv, "Shirt", 40, nil)

	// Off.
	if srv.gooIsPurchasable(ctx, gp) {
		t.Error("gift card purchasable while the module is off")
	}
	if err := srv.gooSetExtension(ctx, gcKey, true); err != nil {
		t.Fatal(err)
	}
	if h := string(srv.gooFilterPriceHTML(ctx, gp, "")); !strings.Contains(h, "10.00") || !strings.Contains(h, "200.00") {
		t.Errorf("price html: %s", h)
	}
	if out := string(srv.gooHTML(gooTestCtx(), "single_before_add_to_cart", gp)); !strings.Contains(out, "wc_gc_giftcard_to") || !strings.Contains(out, `value="custom"`) {
		t.Errorf("gift card form: %s", out)
	}

	buyer := gooTestCtx()
	cart := srv.gooCartFor(buyer)
	cart.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "CA", "postcode": "90210", "email": "buyer@example.test"}
	gf := func(amount, to string) map[string][]string {
		return map[string][]string{"wc_gc_giftcard_amount": {amount}, "wc_gc_giftcard_to": {to}, "wc_gc_giftcard_from": {"Ada"}, "wc_gc_giftcard_to_name": {"Grace"},
			"wc_gc_giftcard_message": {"Happy birthday!"}}
	}
	if _, err := cart.Add(ctx, gp.ID, 1, 0, nil, gf("50", "not-an-email"), nil); err == nil || !strings.Contains(err.Error(), "valid email") {
		t.Errorf("invalid recipient: %v", err)
	}
	if _, err := cart.Add(ctx, gp.ID, 1, 0, nil, gf("30", "grace@example.test"), nil); err == nil {
		t.Error("an amount outside the list was accepted")
	}
	bad := gf("custom", "grace@example.test")
	bad["wc_gc_giftcard_custom_amount"] = []string{"500"}
	if _, err := cart.Add(ctx, gp.ID, 1, 0, nil, bad, nil); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Errorf("custom amount above the max: %v", err)
	}
	key, err := cart.Add(ctx, gp.ID, 1, 0, nil, gf("50", "grace@example.test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	later := gf("custom", "later@example.test")
	later["wc_gc_giftcard_custom_amount"] = []string{"15"}
	later["wc_gc_giftcard_delivery"] = []string{time.Now().AddDate(0, 0, 3).Format("2006-01-02")}
	if _, err := cart.Add(ctx, gp.ID, 1, 0, nil, later, nil); err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "gift card price", cart.Item(key).Price, 50)
	gooAlmost(t, "subtotal", cart.Subtotal, 65)
	if cart.NeedsShipping() {
		t.Error("gift cards need shipping")
	}
	kv := srv.gooItemData(ctx, cart.Item(key))
	if len(kv) < 3 || kv[0].Value != "Grace <grace@example.test>" || kv[2].Value != "Happy birthday!" {
		t.Errorf("item data: %+v", kv)
	}

	// Paying issues the codes: one now (e-mailed), one scheduled.
	o, errs := gxCheckout(t, srv, buyer, "goo_test")
	if len(errs) > 0 {
		t.Fatalf("checkout: %v", errs)
	}
	if o.Status != "processing" && o.Status != "completed" {
		t.Fatalf("order status %s", o.Status)
	}
	cards := srv.gcCardsForEmail(ctx, "grace@example.test")
	if len(cards) != 1 || cards[0].Balance != 50 || !cards[0].Delivered || cards[0].Expires == nil || len(cards[0].Code) != 19 {
		t.Fatalf("issued cards: %+v", cards)
	}
	card := cards[0]
	if gxMailCount(t, srv, "gift card from Ada") == 0 {
		t.Error("no gift card e-mail")
	}
	// Changing the status again does not issue twice.
	_ = srv.gooSetStatus(ctx, o, "completed", "", "")
	if n := len(srv.gcCardsForEmail(ctx, "grace@example.test")); n != 1 {
		t.Errorf("cards after completing: %d", n)
	}
	sched := srv.gcCardsForEmail(ctx, "later@example.test")
	if len(sched) != 1 || sched[0].Delivered {
		t.Fatalf("scheduled card: %+v", sched)
	}
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE goo_gift_cards SET deliver_date = current_date WHERE id = $1`, sched[0].ID)
	srv.gooFire(ctx, "daily", nil)
	if g, _ := srv.gcGet(ctx, sched[0].ID); !g.Delivered {
		t.Error("the scheduled card was not delivered by the daily job")
	}

	// Redeem: the card pays for the shirt and its shipping (40 + 5).
	shopper := gooTestCtx()
	cart2 := srv.gooCartFor(shopper)
	cart2.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "CA", "postcode": "90210", "email": "shopper@example.test"}
	if _, err := cart2.Add(ctx, shirt.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	w, _ := gxPostRoute(srv, gcFrontRoute, shopper, "/?goo-ajax=apply_gift_card", url.Values{"goo_module": {"gift_cards"}, "wc_gc_cart_code": {"nope"}})
	if !strings.Contains(w.Body.String(), "failure") {
		t.Errorf("unknown code: %s", w.Body.String())
	}
	w, _ = gxPostRoute(srv, gcFrontRoute, shopper, "/?goo-ajax=apply_gift_card", url.Values{"goo_module": {"gift_cards"}, "wc_gc_cart_code": {strings.ToLower(strings.ReplaceAll(card.Code, "-", ""))}})
	var res struct{ Result, Message string }
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Result != "success" {
		t.Fatalf("apply: %s", w.Body.String())
	}
	cart2.Calculate()
	gooAlmost(t, "shipping", cart2.ShippingTotal, 5)
	gooAlmost(t, "total paid by the gift card", cart2.Total, 0)
	if fees := gcCartGiftCards(cart2); len(fees) != 1 || fees[0].Amount != -45 {
		t.Errorf("gift card fees: %+v", fees)
	}
	if out := string(srv.gooPageHTML(shopper, "cart_after")); !strings.Contains(out, card.Code) || !strings.Contains(out, "remove_gift_card") {
		t.Errorf("cart gift card form: %s", out)
	}
	o2, errs := gxCheckout(t, srv, shopper, "goo_test")
	if len(errs) > 0 {
		t.Fatalf("checkout with the gift card: %v", errs)
	}
	gooAlmost(t, "order total", o2.Total, 0)
	if g, _ := srv.gcGet(ctx, card.ID); g.Balance != 5 {
		t.Errorf("balance after the order: %v", g.Balance)
	}
	if len(gcApplied(srv.gooSessionFor(shopper))) != 0 {
		t.Error("the gift card stays applied after the order")
	}

	// The remaining 5 pays part of the next order; cancelling that order credits it back.
	shopper2 := gooTestCtx()
	cart3 := srv.gooCartFor(shopper2)
	cart3.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "CA", "postcode": "90210", "email": "shopper@example.test"}
	_, _ = cart3.Add(ctx, shirt.ID, 1, 0, nil, nil, nil)
	if _, err := srv.gcApply(shopper2, card.Code); err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "total with the rest of the card", cart3.Total, 40)
	_ = srv.gooSetOpts(ctx, map[string]any{"cheque_settings": map[string]any{"enabled": "yes"}})
	o3, errs := gxCheckout(t, srv, shopper2, "cheque")
	if len(errs) > 0 {
		t.Fatalf("cheque checkout: %v", errs)
	}
	if g, _ := srv.gcGet(ctx, card.ID); g.Balance != 0 || g.Status != "redeemed" {
		t.Errorf("card after the second order: %v %s", g.Balance, g.Status)
	}
	if _, err := srv.gcApply(gooTestCtx(), card.Code); err == nil {
		t.Error("an empty card was applied")
	}
	if err := srv.gooSetStatus(ctx, o3, "cancelled", "", ""); err != nil {
		t.Fatal(err)
	}
	if g, _ := srv.gcGet(ctx, card.ID); g.Balance != 5 || g.Status != "active" {
		t.Errorf("card after cancelling: %v %s", g.Balance, g.Status)
	}
	_ = srv.gooSetStatus(ctx, o3, "cancelled", "", "")
	acts := srv.gcActivities(ctx, card.ID)
	types := map[string]int{}
	for _, a := range acts {
		types[a.Type]++
	}
	if types["issued"] != 1 || types["used"] != 2 || types["refunded"] != 1 {
		t.Errorf("activity: %v", types)
	}

	// Remove link.
	c4 := gooTestCtx()
	srv.gooCartFor(c4)
	if _, err := srv.gcApply(c4, card.Code); err != nil {
		t.Fatal(err)
	}
	rc, rw := gxGetCtx("/?goo-ajax=remove_gift_card&giftcard=" + url.QueryEscape(card.Code) + "&_wpnonce=" + srv.nonce(c4, "wc_gc_remove_giftcard"))
	rc.Set(gooCtxSession, srv.gooSessionFor(c4))
	if !gcFrontRoute(srv, rc, "") || rw.Code != http.StatusFound || len(gcApplied(srv.gooSessionFor(c4))) != 0 {
		t.Errorf("remove: %d %v", rw.Code, gcApplied(srv.gooSessionFor(c4)))
	}

	// Admin: list, manual card, disable, activity; a disabled card cannot be used.
	for _, u := range []string{"", "&status=active", "&s=" + card.Code[:4], "&action=new", "&action=edit&id=" + itoa64(card.ID)} {
		code, body, _ := get(t, client, base+"/wp-admin/goocommerce?page=gift-cards"+u)
		if code != http.StatusOK || strings.Contains(body, "Template error") {
			t.Errorf("gift cards admin %s: %d", u, code)
		}
	}
	_, body, _ := get(t, client, base+"/wp-admin/goocommerce?page=gift-cards&action=new")
	code, _, loc := post(t, client, base+"/wp-admin/goocommerce?page=gift-cards&action=new", url.Values{"_wpnonce": {formNonce(t, body)}, "amount": {"20"},
		"recipient": {"vip@example.test"}, "sender": {"The shop"}, "send": {"1"}})
	if code != http.StatusFound || !strings.Contains(loc, "action=edit") {
		t.Fatalf("manual card: %d %s", code, loc)
	}
	manual := srv.gcCardsForEmail(ctx, "vip@example.test")
	if len(manual) != 1 || manual[0].Balance != 20 || !manual[0].Delivered {
		t.Fatalf("manual card: %+v", manual)
	}
	editURL := base + "/wp-admin/goocommerce?page=gift-cards&action=edit&id=" + itoa64(manual[0].ID)
	_, body, _ = get(t, client, editURL)
	if code, _, _ := post(t, client, editURL, url.Values{"_wpnonce": {formNonce(t, body)}, "gc_action": {"disable"}}); code != http.StatusFound {
		t.Errorf("disable: %d", code)
	}
	if _, err := srv.gcApply(gooTestCtx(), manual[0].Code); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("a disabled card: %v", err)
	}
	_, body, _ = get(t, client, editURL)
	_, _, _ = post(t, client, editURL, url.Values{"_wpnonce": {formNonce(t, body)}, "gc_action": {"adjust"}, "adjust_amount": {"-5"}, "adjust_note": {"Goodwill"}})
	if g, _ := srv.gcGet(ctx, manual[0].ID); g.Balance != 15 || g.Status != "disabled" {
		t.Errorf("adjusted card: %v %s", g.Balance, g.Status)
	}
	if _, body, _ := get(t, client, editURL); !strings.Contains(body, "Goodwill") || !strings.Contains(body, "Issued") {
		t.Error("activity log lacks the adjustment")
	}

	// My Account › Gift Cards for the recipient's account.
	admin, _ := srv.store.UserByID(ctx, 1)
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE goo_gift_cards SET recipient = $1 WHERE id = $2`, admin.Email, card.ID)
	ac := gooTestCtx()
	ac.Set(ctxUser, admin)
	if out := string(gcAccount(srv, ac, "")); !strings.Contains(out, card.Code) || !strings.Contains(out, "add_gift_card_to_account") {
		t.Errorf("account gift cards: %s", out)
	}
}
