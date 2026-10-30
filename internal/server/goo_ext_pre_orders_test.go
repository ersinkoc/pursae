package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGooExtPreOrders(t *testing.T) {
	base, cl, srv := gooSite(t)
	ctx := context.Background()
	mails := gxaRecordMails(t)
	gxaEnableGateways(t, srv)

	console := gooMakeSimple(t, srv, "Console", 100, func(p *gooProduct) { p.Virtual = true })
	game := gooMakeSimple(t, srv, "Game", 40, func(p *gooProduct) { p.Virtual = true })
	release := time.Now().Add(48 * time.Hour).In(srv.loc()).Format("2006-01-02T15:04")
	console = gxaSavePanel(t, srv, "wc_pre_orders_data", console, url.Values{"_wc_pre_orders_enabled": {"yes"},
		"_wc_pre_orders_availability_datetime": {release}, "_wc_pre_orders_fee": {"5"}, "_wc_pre_orders_when_to_charge": {"upfront"}})
	if po := gpreOf(console); !po.Enabled || po.Availability == nil || po.Fee != 5 || po.WhenToCharge != "upfront" {
		t.Fatalf("saved pre-order settings: %+v", po)
	}
	if gooStatusLabel("pre-ordered") != "Pre-ordered" {
		t.Error("the Pre-ordered status is not registered")
	}

	// Off: a normal product.
	u := gxaCustomer(t, srv, "gamer@example.test")
	c := gxaCtx(u, http.MethodGet, "/", nil)
	cart := srv.gooCartFor(c)
	if _, err := cart.Add(c, console.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "price while off", cart.Total, 100)
	if h := srv.gooHTML(c, "single_after_add_to_cart", console); h != "" {
		t.Errorf("pre-order html while off: %s", h)
	}
	cart.Empty()

	gxaOn(t, srv, gpreKey, true)
	if h := string(srv.gooHTML(c, "single_after_add_to_cart", console)); !strings.Contains(h, "Pre-order now") || !strings.Contains(h, "Available on") {
		t.Errorf("product page: %s", h)
	}
	if h := string(srv.gooHTML(c, "loop_after_item", console)); !strings.Contains(h, "wc-pre-orders-availability-date") {
		t.Errorf("loop: %s", h)
	}
	cart.Calculate()
	if _, err := cart.Add(c, console.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "price with the pre-order fee", cart.Total, 105)
	if kv := srv.gooItemData(ctx, cart.Items[0]); len(kv) == 0 || kv[0].Key != "Release date" {
		t.Errorf("item data: %+v", kv)
	}
	if _, err := cart.Add(c, game.ID, 1, 0, nil, nil, nil); err == nil {
		t.Error("a normal product joined a pre-order in the cart")
	}

	// Upfront: paid now, then Pre-ordered.
	_, o, errs := srv.gooProcessCheckout(c, gxaCheckoutForm(u.Email, "goo_test"))
	if len(errs) > 0 {
		t.Fatalf("checkout: %v", errs)
	}
	o, _ = srv.gooGetOrder(ctx, o.ID)
	if o.Status != "pre-ordered" || o.DatePaid == nil || o.Meta["_wc_pre_orders_is_pre_order"] != true {
		t.Fatalf("upfront pre-order: %s paid %v meta %v", o.Status, o.DatePaid, o.Meta)
	}
	if !mails.has("wc_pre_orders_new_pre_order") {
		t.Error("no new pre-order e-mail")
	}
	// Nothing happens before the release date.
	srv.gooFire(ctx, "hourly", nil)
	if o, _ = srv.gooGetOrder(ctx, o.ID); o.Status != "pre-ordered" {
		t.Fatalf("released early: %s", o.Status)
	}

	// Upon release: no payment now.
	game = gxaSavePanel(t, srv, "wc_pre_orders_data", game, url.Values{"_wc_pre_orders_enabled": {"yes"},
		"_wc_pre_orders_availability_datetime": {release}, "_wc_pre_orders_when_to_charge": {"upon_release"}})
	c2 := gxaCtx(u, http.MethodGet, "/", nil)
	if _, err := srv.gooCartFor(c2).Add(c2, game.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	_, o2, errs := srv.gooProcessCheckout(c2, gxaCheckoutForm(u.Email, "goo_test"))
	if len(errs) > 0 {
		t.Fatalf("upon release checkout: %v", errs)
	}
	o2, _ = srv.gooGetOrder(ctx, o2.ID)
	if o2.Status != "pre-ordered" || o2.DatePaid != nil || o2.TransactionID != "" {
		t.Fatalf("upon release pre-order: %s paid %v tx %q", o2.Status, o2.DatePaid, o2.TransactionID)
	}

	// The release date passes: the upfront order is processed.
	console.Meta["_wc_pre_orders_availability_datetime"] = float64(time.Now().Add(-time.Minute).Unix())
	_ = srv.gooSaveProduct(ctx, console, 1)
	mails.reset()
	srv.gooFire(ctx, "daily", nil)
	if o, _ = srv.gooGetOrder(ctx, o.ID); o.Status != "completed" && o.Status != "processing" {
		t.Errorf("released upfront order: %s", o.Status)
	}
	if !mails.has("wc_pre_orders_pre_order_available") {
		t.Error("no pre-order available e-mail")
	}
	if p, _ := srv.gooGetProduct(ctx, console.ID); gpreOf(p).Enabled {
		t.Error("a released product still takes pre-orders")
	}

	// Admin: the list, the actions; "Complete now" makes the upon-release order pending with an invoice.
	gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=pre-orders", "Game", "#"+itoa64(o2.ID))
	body := gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=pre-orders&tab=actions&product_id="+itoa64(game.ID), "Change release date", "Complete Pre-Orders")
	nonce := formNonce(t, body)
	newDate := time.Now().Add(96 * time.Hour).In(srv.loc()).Format("2006-01-02T15:04")
	code, _, loc := post(t, cl, base+"/wp-admin/goocommerce?page=pre-orders", url.Values{"_wpnonce": {nonce}, "action_type": {"change-date"},
		"product_id": {itoa64(game.ID)}, "new_availability_date": {newDate}, "send_email": {"1"}})
	if code != http.StatusFound || !strings.Contains(loc, "goo-msg") {
		t.Fatalf("change date: %d %s", code, loc)
	}
	if !mails.has("wc_pre_orders_pre_order_date_changed") {
		t.Error("no date changed e-mail")
	}
	mails.reset()
	_, _, _ = post(t, cl, base+"/wp-admin/goocommerce?page=pre-orders", url.Values{"_wpnonce": {nonce}, "action_type": {"complete"}, "product_id": {itoa64(game.ID)}})
	if o2, _ = srv.gooGetOrder(ctx, o2.ID); o2.Status != "pending" || !mails.has("customer_invoice") {
		t.Errorf("completed upon-release order: %s, mails %v", o2.Status, mails.ids)
	}

	// Cancel all: a new pre-order of a third product.
	disc := gooMakeSimple(t, srv, "Disc", 10, func(p *gooProduct) {
		p.Virtual = true
		p.Meta["_wc_pre_orders_enabled"] = "yes"
	})
	c3 := gxaCtx(u, http.MethodGet, "/", nil)
	if _, err := srv.gooCartFor(c3).Add(c3, disc.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	_, o3, errs := srv.gooProcessCheckout(c3, gxaCheckoutForm(u.Email, "goo_test"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	srv.gooFire(ctx, "daily", nil) // no date: completed manually only
	if o3, _ = srv.gooGetOrder(ctx, o3.ID); o3.Status != "pre-ordered" {
		t.Fatalf("pre-order without a date: %s", o3.Status)
	}
	_, _, _ = post(t, cl, base+"/wp-admin/goocommerce?page=pre-orders", url.Values{"_wpnonce": {nonce}, "action_type": {"cancel"}, "product_id": {itoa64(disc.ID)}})
	if o3, _ = srv.gooGetOrder(ctx, o3.ID); o3.Status != "cancelled" || !mails.has("wc_pre_orders_pre_order_cancelled") {
		t.Errorf("cancelled pre-order: %s", o3.Status)
	}
	gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=ext&ext=pre_orders", "Pre-order now")
}
