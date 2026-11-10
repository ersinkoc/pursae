package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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
	// A card addressed to the account's (unverified) e-mail is listed with its code masked; once added
	// by code it is the account's and shows in full.
	if out := string(gcAccount(srv, ac, "")); strings.Contains(out, card.Code) || !strings.Contains(out, gcMaskCode(card.Code)) || !strings.Contains(out, "add_gift_card_to_account") {
		t.Errorf("account gift cards: %s", out)
	}
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE goo_gift_cards SET redeemed_by = $1 WHERE id = $2`, admin.ID, card.ID)
	if out := string(gcAccount(srv, ac, "")); !strings.Contains(out, card.Code) {
		t.Errorf("account gift cards after adding: %s", out)
	}
}

// Refunds of an order paid partly by a gift card credit the card back: a partial refund restores the
// same share of what the card paid, the full refund restores the rest, each with an activity row.
func TestGooExtGiftCardsPartialRefund(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxTestGateway(t, srv)
	if err := srv.gooSetExtension(ctx, gcKey, true); err != nil {
		t.Fatal(err)
	}
	lamp := gooMakeSimple(t, srv, "Lamp", 100, func(p *gooProduct) { p.Virtual = true })
	card := &gcCard{Initial: 30, Recipient: "r@example.test", Sender: "Shop"}
	if err := srv.gcCreate(ctx, card, 1, "test"); err != nil {
		t.Fatal(err)
	}
	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	cart.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "CA", "postcode": "90210", "email": "buyer@example.test"}
	if _, err := cart.Add(ctx, lamp.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.gcApply(c, card.Code); err != nil {
		t.Fatal(err)
	}
	o, errs := gxCheckout(t, srv, c, "goo_test")
	if len(errs) > 0 || o == nil {
		t.Fatalf("checkout: %v", errs)
	}
	gooAlmost(t, "charged", o.Total, 70)
	if g, _ := srv.gcGet(ctx, card.ID); g.Balance != 0 || g.Status != "redeemed" {
		t.Fatalf("card after the order: %v %s", g.Balance, g.Status)
	}
	// Half of the charge refunded: half of the 30 comes back.
	if _, err := srv.gooCreateRefund(ctx, o, 35, "half", nil, false, false, 1); err != nil {
		t.Fatal(err)
	}
	if g, _ := srv.gcGet(ctx, card.ID); g.Balance != 15 || g.Status != "active" {
		t.Errorf("card after the partial refund: %v %s", g.Balance, g.Status)
	}
	// The rest: the order becomes refunded and the card gets the other 15, not 30.
	o, _ = srv.gooGetOrder(ctx, o.ID)
	if _, err := srv.gooCreateRefund(ctx, o, 35, "rest", nil, false, false, 1); err != nil {
		t.Fatal(err)
	}
	if g, _ := srv.gcGet(ctx, card.ID); g.Balance != 30 {
		t.Errorf("card after the full refund: %v", g.Balance)
	}
	refunds := 0
	for _, a := range srv.gcActivities(ctx, card.ID) {
		if a.Type == "refunded" && a.OrderID == o.ID {
			refunds++
			gooAlmost(t, "credited", a.Amount, 15)
		}
	}
	if refunds != 2 {
		t.Errorf("refund activity rows: %d", refunds)
	}
}

// A refunded or cancelled order disables the cards it sold; the clamped debit is logged as it
// happened; an expired card cannot be reserved; the expiry reminder goes out once.
func TestGooExtGiftCardsLifecycle(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxTestGateway(t, srv)
	if err := srv.gooSetExtension(ctx, gcKey, true); err != nil {
		t.Fatal(err)
	}
	gp := gooNewProduct(gcType)
	gp.Name = "Store gift card"
	gp = gxSavePanels(t, srv, gp, gcKey, url.Values{"_gift_card_amounts": {"50"}})
	buyer := gooTestCtx()
	cart := srv.gooCartFor(buyer)
	cart.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "CA", "postcode": "90210", "email": "buyer@example.test"}
	form := map[string][]string{"wc_gc_giftcard_amount": {"50"}, "wc_gc_giftcard_to": {"grace@example.test"}, "wc_gc_giftcard_from": {"Ada"}}
	if _, err := cart.Add(ctx, gp.ID, 2, 0, nil, form, nil); err != nil {
		t.Fatal(err)
	}
	o, errs := gxCheckout(t, srv, buyer, "goo_test")
	if len(errs) > 0 || o == nil {
		t.Fatalf("checkout: %v", errs)
	}
	cards := srv.gcCardsForEmail(ctx, "grace@example.test")
	if len(cards) != 2 {
		t.Fatalf("issued cards: %d", len(cards))
	}
	status := func(id int64) string { g, _ := srv.gcGet(ctx, id); return g.Status }

	// Refunding one of the two cards' line disables one card only.
	var item int64
	for _, it := range o.ItemsOf("line_item") {
		item = it.ID
	}
	if _, err := srv.gooCreateRefund(ctx, o, 50, "one card", []gooRefundLine{{ItemID: item, Qty: 1, Total: 50}}, false, false, 1); err != nil {
		t.Fatal(err)
	}
	disabled := 0
	for _, g := range cards {
		if status(g.ID) == "disabled" {
			disabled++
		}
	}
	if disabled != 1 {
		t.Fatalf("cards disabled after a one-card refund: %d", disabled)
	}
	// Cancelling the order disables the rest; paying it again re-enables only those.
	o, _ = srv.gooGetOrder(ctx, o.ID)
	if err := srv.gooSetStatus(ctx, o, "cancelled", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, g := range cards {
		if status(g.ID) != "disabled" {
			t.Errorf("card %d still %s after the cancellation", g.ID, status(g.ID))
		}
	}
	o, _ = srv.gooGetOrder(ctx, o.ID)
	_ = srv.gooSetStatus(ctx, o, "processing", "", "")
	active := 0
	for _, g := range cards {
		if status(g.ID) == "active" {
			active++
		}
	}
	if active != 1 {
		t.Errorf("cards active after reactivation: %d, want 1 (the refunded one stays disabled)", active)
	}

	// The clamped debit logs what really left the card.
	g := &gcCard{Initial: 5, Recipient: "a@example.test", Sender: "Shop"}
	if err := srv.gcCreate(ctx, g, 1, "t"); err != nil {
		t.Fatal(err)
	}
	if err := srv.gcAdjust(ctx, g, -10, "manual", 0, 1, "too much"); err != nil {
		t.Fatal(err)
	}
	if g.Balance != 0 || g.Status != "redeemed" {
		t.Errorf("after the debit: %v %s", g.Balance, g.Status)
	}
	if acts := srv.gcActivities(ctx, g.ID); len(acts) == 0 || acts[0].Type != "manual" || acts[0].Amount != 5 {
		t.Errorf("activity: %+v", acts)
	}

	// An expired card is not reserved.
	e := &gcCard{Initial: 20, Recipient: "e@example.test", Sender: "Shop"}
	if err := srv.gcCreate(ctx, e, 1, "t"); err != nil {
		t.Fatal(err)
	}
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE goo_gift_cards SET expires = current_date - 3 WHERE id = $1`, e.ID)
	fee := &gooOrderItem{Type: "fee", Total: -10, Meta: map[string]any{"_fee_id": gcFeePref + strings.ToLower(e.Code)}}
	if _, err := gcReserve(srv, ctx, nil, &gooOrder{Meta: map[string]any{}, Items: []*gooOrderItem{fee}}); err == nil {
		t.Error("an expired card was reserved")
	}
	if got, _ := srv.gcGet(ctx, e.ID); got.Balance != 20 {
		t.Errorf("expired card balance %v", got.Balance)
	}

	// Reminder a week before expiry, once.
	r := &gcCard{Initial: 20, Recipient: "r@example.test", Sender: "Shop", Delivered: true}
	if err := srv.gcCreate(ctx, r, 1, "t"); err != nil {
		t.Fatal(err)
	}
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE goo_gift_cards SET expires = current_date + 3, delivered = true WHERE id = $1`, r.ID)
	srv.gooFire(ctx, "daily", nil)
	srv.gooFire(ctx, "daily", nil)
	if n := gxMailCount(t, srv, "gift card expires on"); n != 1 {
		t.Errorf("reminder e-mails: %d", n)
	}
}

func TestGooExtGiftCardsREST(t *testing.T) {
	base, client, srv := gooSite(t)
	ctx := context.Background()
	gooShopSetup(t, srv)
	if err := srv.gooSetExtension(ctx, gcKey, true); err != nil {
		t.Fatal(err)
	}
	gp := gooNewProduct(gcType)
	gp.Name = "Store gift card"
	gp = gxSavePanels(t, srv, gp, gcKey, url.Values{"_gift_card_amounts": {"50"}})
	card := &gcCard{Initial: 30, Recipient: "r@example.test", Sender: "Shop"}
	if err := srv.gcCreate(ctx, card, 1, "t"); err != nil {
		t.Fatal(err)
	}

	guest := gooGuest()
	api := base + "/wp-json/wc/store/v1"
	r := gooReq(t, guest, "GET", api+"/cart", nil, nil)
	nonce := map[string]string{"Nonce": r.Header.Get("Nonce")}
	if r := gooReq(t, guest, "POST", api+"/cart/add-item", map[string]any{"id": gp.ID, "quantity": 1}, nonce); r.Code != 400 {
		t.Fatalf("add without gift card fields: %d %s", r.Code, r.Raw)
	}
	r = gooReq(t, guest, "POST", api+"/cart/add-item", map[string]any{"id": gp.ID, "quantity": 1,
		"extensions": map[string]any{"gift_cards": map[string]any{"amount": "50", "to": "g@example.test", "from": "Ada"}}}, nonce)
	if r.Code != 201 || gooJS(r.Body, "totals", "total_items") != "5000" {
		t.Fatalf("add gift card: %d %s", r.Code, r.Raw)
	}
	r = gooReq(t, guest, "POST", api+"/cart/apply-gift-card", map[string]any{"code": card.Code}, nonce)
	if r.Code != 200 || gooJS(r.Body, "extensions", "gift_cards", "applied", 0, "code") != card.Code {
		t.Fatalf("apply-gift-card: %d %s", r.Code, r.Raw)
	}
	if r := gooReq(t, guest, "POST", api+"/cart/apply-gift-card", map[string]any{"code": "nope"}, nonce); r.Code != 400 {
		t.Errorf("unknown gift card: %d", r.Code)
	}
	r = gooReq(t, guest, "POST", api+"/cart/remove-gift-card", map[string]any{"code": card.Code}, nonce)
	if r.Code != 200 || gooJ(r.Body, "extensions", "gift_cards", "applied", 0) != nil {
		t.Fatalf("remove-gift-card: %d %s", r.Code, r.Raw)
	}

	v3 := gooAdminAPI(t, base, client)
	if r := v3("GET", "/gift-cards", nil); r.Code != 200 || len(r.list()) != 1 || r.Header.Get("X-WP-Total") != "1" {
		t.Fatalf("list: %d %s", r.Code, r.Raw)
	}
	id := card.ID
	if r := v3("POST", "/gift-cards/"+itoa64(id)+"/adjust", map[string]any{"amount": -10, "note": "pos"}); r.Code != 200 || gooJ(r.Body, "balance") != float64(20) {
		t.Fatalf("adjust: %d %s", r.Code, r.Raw)
	}
	if r := v3("PUT", "/gift-cards/"+itoa64(id), map[string]any{"status": "disabled"}); r.Code != 200 || gooJS(r.Body, "status") != "disabled" {
		t.Fatalf("disable: %d %s", r.Code, r.Raw)
	}
	if r := v3("GET", "/gift-cards/"+itoa64(id), nil); r.Code != 200 || len(gooJ(r.Body, "activity").([]any)) < 2 {
		t.Fatalf("get: %d %s", r.Code, r.Raw)
	}
	if r := gooReq(t, guest, "GET", base+"/wp-json/wc/v3/gift-cards", nil, nil); r.Code != 401 && r.Code != 403 {
		t.Errorf("anonymous list: %d", r.Code)
	}
}

// A design chosen at purchase is validated, kept on the order item and shown on the signed print page;
// the print page refuses a wrong token.
func TestGooExtGiftCardsDesignAndPrint(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxTestGateway(t, srv)
	if err := srv.gooSetExtension(ctx, gcKey, true); err != nil {
		t.Fatal(err)
	}
	gp := gooNewProduct(gcType)
	gp.Name = "Designed card"
	gp = gxSavePanels(t, srv, gp, gcKey, url.Values{"_gift_card_amounts": {"25"}, "_gift_card_designs": {"birthday, bogus, holiday"}})
	if got := gcProductDesigns(gp); len(got) != 2 || got[0] != "birthday" || got[1] != "holiday" {
		t.Fatalf("designs: %v", got)
	}
	buyer := gooTestCtx()
	cart := srv.gooCartFor(buyer)
	cart.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "CA", "postcode": "90210", "email": "buyer@example.test"}
	form := map[string][]string{"wc_gc_giftcard_amount": {"25"}, "wc_gc_giftcard_to": {"ann@example.test"}, "wc_gc_giftcard_from": {"Bo"}, "wc_gc_giftcard_design": {"nope"}}
	if _, err := cart.Add(ctx, gp.ID, 1, 0, nil, form, nil); err == nil {
		t.Fatal("an unknown design was accepted")
	}
	form["wc_gc_giftcard_design"] = []string{"holiday"}
	if _, err := cart.Add(ctx, gp.ID, 1, 0, nil, form, nil); err != nil {
		t.Fatal(err)
	}
	if o, errs := gxCheckout(t, srv, buyer, "goo_test"); len(errs) > 0 || o == nil {
		t.Fatalf("checkout: %v", errs)
	}
	cards := srv.gcCardsForEmail(ctx, "ann@example.test")
	if len(cards) != 1 {
		t.Fatalf("cards: %d", len(cards))
	}
	g := cards[0]
	if d := srv.gcCardDesign(ctx, g); d != "holiday" {
		t.Fatalf("stored design %q", d)
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "/?"+url.Values{"card": {fmt.Sprint(g.ID)}, "token": {srv.gcPrintToken(g.ID)}}.Encode(), nil)
	srv.gcPrintPage(c)
	if body := rec.Body.String(); rec.Code != 200 || !strings.Contains(body, g.Code) || !strings.Contains(body, "#2e7d32") {
		t.Errorf("print page %d: %.200s", rec.Code, body)
	}
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "/?card="+fmt.Sprint(g.ID)+"&token=bad", nil)
	srv.gcPrintPage(c)
	if rec.Code != 404 {
		t.Errorf("bad token status %d", rec.Code)
	}
}

// Concurrent admin adjustments and checkout debits on one card all land: the balance is the sum of the
// deltas, never an overwritten absolute value.
func TestGooExtGiftCardsAdjustRace(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	g := &gcCard{Initial: 100, Recipient: "a@example.test", Sender: "Shop"}
	if err := srv.gcCreate(ctx, g, 1, "t"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			c, _ := srv.gcGet(ctx, g.ID)
			_ = srv.gcAdjust(ctx, c, -5, "used", 0, 1, "spend")
		}()
		go func() {
			defer wg.Done()
			c, _ := srv.gcGet(ctx, g.ID)
			_ = srv.gcAdjust(ctx, c, 2, "manual", 0, 1, "admin")
		}()
	}
	wg.Wait()
	got, _ := srv.gcGet(ctx, g.ID)
	gooAlmost(t, "balance", got.Balance, 100-50+20)
}

// Refunding the order that sold a card disables it even after part of its balance was spent, and the
// spent balance is not credited back to anyone.
func TestGooExtGiftCardsPurchasedRefundSpent(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxTestGateway(t, srv)
	if err := srv.gooSetExtension(ctx, gcKey, true); err != nil {
		t.Fatal(err)
	}
	gp := gooNewProduct(gcType)
	gp.Name = "Card"
	gp = gxSavePanels(t, srv, gp, gcKey, url.Values{"_gift_card_amounts": {"40"}})
	buyer := gooTestCtx()
	cart := srv.gooCartFor(buyer)
	cart.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "CA", "postcode": "90210", "email": "buyer@example.test"}
	form := map[string][]string{"wc_gc_giftcard_amount": {"40"}, "wc_gc_giftcard_to": {"z@example.test"}, "wc_gc_giftcard_from": {"Bo"}}
	if _, err := cart.Add(ctx, gp.ID, 1, 0, nil, form, nil); err != nil {
		t.Fatal(err)
	}
	o, errs := gxCheckout(t, srv, buyer, "goo_test")
	if len(errs) > 0 || o == nil {
		t.Fatalf("checkout: %v", errs)
	}
	cards := srv.gcCardsForEmail(ctx, "z@example.test")
	if len(cards) != 1 {
		t.Fatalf("cards: %d", len(cards))
	}
	if err := srv.gcAdjust(ctx, cards[0], -15, "used", 0, 1, "spent elsewhere"); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.gooCreateRefund(ctx, o, o.Total, "full", nil, false, false, 1); err != nil {
		t.Fatal(err)
	}
	got, _ := srv.gcGet(ctx, cards[0].ID)
	if got.Status != "disabled" {
		t.Errorf("status %s", got.Status)
	}
	gooAlmost(t, "balance untouched", got.Balance, 25)
}
