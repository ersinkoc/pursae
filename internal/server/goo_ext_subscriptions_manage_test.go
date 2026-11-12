package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func gsubTestProduct(t *testing.T, srv *Server) *gooProduct {
	t.Helper()
	box := gooNewProduct("subscription")
	box.Name, box.RegularPrice, box.Virtual = "Tea Box", gooFloatPtr(12), true
	box.Meta = map[string]any{"_subscription_period": "month", "_subscription_period_interval": float64(1), "_subscription_price": float64(12)}
	if err := srv.gooSaveProduct(context.Background(), box, 1); err != nil {
		t.Fatal(err)
	}
	return box
}

func gsubTestSub(t *testing.T, srv *Server, uid int64, status string) *gooSubscription {
	t.Helper()
	next := time.Now().AddDate(0, 0, 5)
	sub := &gooSubscription{CustomerID: uid, Status: status, Period: "month", Interval: 1, Currency: "USD", Start: time.Now().AddDate(0, -1, 0),
		NextPayment: &next, Billing: map[string]string{"email": "a@example.test"}, Shipping: map[string]string{}, PaymentMethod: "cheque", PaymentMethodTitle: "Cheque",
		Items: []gsubItem{{ProductID: 0, Name: "Box", Qty: 1, Price: 10, Total: 10}}, RecurringTotal: 10, Meta: map[string]any{}}
	if err := srv.gsubSave(context.Background(), sub); err != nil {
		t.Fatal(err)
	}
	return sub
}

// The hourly job reads only the rows it needs, in batches: with a tiny batch every due subscription is
// still handled and the ended ones are the only ones the SQL filter returns.
func TestGooSubscriptionsSchedulerBatches(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxaOn(t, srv, gsubKey, true)
	defer func(n int) { gsubBatchSize = n }(gsubBatchSize)
	gsubBatchSize = 2
	u := gxaCustomer(t, srv, "batch@example.test")
	var ended []int64
	for range 5 {
		sub := gsubTestSub(t, srv, u.ID, "active")
		_, _ = srv.store.Pool.Exec(ctx, `UPDATE goo_subscriptions SET end_date = now() - interval '1 hour' WHERE id = $1`, sub.ID)
		ended = append(ended, sub.ID)
	}
	live := gsubTestSub(t, srv, u.ID, "active")
	list, total, _ := srv.gsubList(ctx, gsubQuery{Status: []string{"active"}, Ended: true})
	if total != 5 || len(list) != 5 {
		t.Fatalf("ended filter: %d/%d", len(list), total)
	}
	srv.gsubScheduler(ctx)
	for _, id := range ended {
		if sub, _ := srv.gsubGet(ctx, id); sub.Status != "expired" {
			t.Errorf("subscription %d: %s", id, sub.Status)
		}
	}
	if sub, _ := srv.gsubGet(ctx, live.ID); sub.Status != "active" {
		t.Errorf("live subscription: %s", sub.Status)
	}
	live.Meta["_pending_renewal"] = float64(99)
	_ = srv.gsubSave(ctx, live)
	if list, _, _ := srv.gsubList(ctx, gsubQuery{PendingRenew: true}); len(list) != 1 || list[0].ID != live.ID {
		t.Errorf("pending-renewal filter: %d rows", len(list))
	}
}

// A gateway callback and the customer's return can both create the subscriptions of one order: only one wins.
func TestGooSubscriptionsCreateFromOrderOnce(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxaOn(t, srv, gsubKey, true)
	u := gxaCustomer(t, srv, "race@example.test")
	o := &gooOrder{Status: "processing", Type: "shop_order", Currency: "USD", CustomerID: u.ID, Billing: map[string]string{"email": u.Email}, Shipping: map[string]string{},
		Items: []*gooOrderItem{{Type: "line_item", Name: "Box", Quantity: 1, Total: 10, Subtotal: 10, Meta: map[string]any{
			"_subscription_period": "month", "_subscription_interval": float64(1), "_subscription_recurring_amount": float64(10), "_subscription_recurring_line_total": float64(10)}}}}
	if err := srv.gooSaveOrder(ctx, o, true); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cp := *o
			srv.gsubCreateFromOrder(ctx, &cp)
		}()
	}
	wg.Wait()
	if _, n, _ := srv.gsubList(ctx, gsubQuery{ParentOrderID: o.ID}); n != 1 {
		t.Fatalf("%d subscriptions for one order", n)
	}
}

func TestGooSubscriptionsRESTAndWebhooks(t *testing.T) {
	gooWebhookAllowPrivate.Store(true)
	defer gooWebhookAllowPrivate.Store(false)
	base, c, srv := gooSite(t)
	ctx := context.Background()
	v3 := gooAdminAPI(t, base, c)
	if r := v3("GET", "/subscriptions", nil); r.Code != 404 {
		t.Fatalf("module off: %d %s", r.Code, r.Raw)
	}
	gxaOn(t, srv, gsubKey, true)
	box := gsubTestProduct(t, srv)
	u := gxaCustomer(t, srv, "rest@example.test")

	var mu sync.Mutex
	var topics []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		topics = append(topics, r.Header.Get("X-WC-Webhook-Topic"))
		mu.Unlock()
	}))
	defer hook.Close()
	for _, tp := range []string{"subscription.created", "subscription.updated", "subscription.deleted"} {
		if r := v3("POST", "/webhooks", map[string]any{"topic": tp, "delivery_url": hook.URL, "secret": "s"}); r.Code != 201 {
			t.Fatalf("webhook %s: %d %s", tp, r.Code, r.Raw)
		}
	}

	r := v3("POST", "/subscriptions", map[string]any{"customer_id": u.ID, "status": "active", "payment_method": "cheque",
		"line_items": []any{map[string]any{"product_id": box.ID, "quantity": 2}}, "billing": map[string]any{"first_name": "Ada", "city": "London"}})
	if r.Code != 201 || gooJS(r.Body, "status") != "active" || gooJS(r.Body, "total") != "24.00" || gooJS(r.Body, "billing", "city") != "London" ||
		gooJS(r.Body, "billing_period") != "month" || gooJS(r.Body, "next_payment_date") == "0" {
		t.Fatalf("create: %d %s", r.Code, r.Raw)
	}
	id := int64(gooJ(r.Body, "id").(float64))
	path := fmt.Sprintf("/subscriptions/%d", id)
	if r := v3("POST", "/subscriptions", map[string]any{"customer_id": u.ID, "line_items": []any{}}); r.Code != 400 {
		t.Errorf("empty subscription accepted: %d", r.Code)
	}
	if r := v3("GET", "/subscriptions?status=active", nil); r.Code != 200 || len(r.list()) != 1 {
		t.Errorf("list: %d %s", r.Code, r.Raw)
	}
	if r := v3("GET", "/subscriptions?status=cancelled", nil); len(r.list()) != 0 {
		t.Errorf("list cancelled: %s", r.Raw)
	}
	if r := v3("GET", path, nil); r.Code != 200 || gooJS(r.Body, "customer_id") == "" {
		t.Errorf("get: %d %s", r.Code, r.Raw)
	}
	r = v3("PUT", path, map[string]any{"billing": map[string]any{"city": "Paris"}, "line_items": []any{map[string]any{"id": 1, "quantity": 3, "total": "30.00"}}, "status": "on-hold"})
	if r.Code != 200 || gooJS(r.Body, "status") != "on-hold" || gooJS(r.Body, "billing", "city") != "Paris" || gooJS(r.Body, "total") != "30.00" {
		t.Fatalf("update: %d %s", r.Code, r.Raw)
	}
	if r := v3("PUT", path, map[string]any{"status": "bogus"}); r.Code != 400 {
		t.Errorf("bad status: %d", r.Code)
	}
	if r := v3("POST", path+"/notes", map[string]any{"note": "Rang the customer"}); r.Code != 201 || gooJS(r.Body, "note") != "Rang the customer" {
		t.Errorf("note: %d %s", r.Code, r.Raw)
	}
	if r := v3("GET", path+"/notes", nil); r.Code != 200 || len(r.list()) < 2 {
		t.Errorf("notes: %d %s", r.Code, r.Raw)
	}
	if r := v3("GET", path+"/orders", nil); r.Code != 200 {
		t.Errorf("orders: %d %s", r.Code, r.Raw)
	}
	if r := v3("DELETE", path, nil); r.Code != 501 {
		t.Errorf("trash: %d", r.Code)
	}
	if r := v3("DELETE", path+"?force=true", nil); r.Code != 200 {
		t.Errorf("delete: %d %s", r.Code, r.Raw)
	}
	if r := v3("GET", path, nil); r.Code != 404 {
		t.Errorf("deleted subscription: %d", r.Code)
	}
	gooWebhookWG.Wait()
	mu.Lock()
	defer mu.Unlock()
	seen := map[string]bool{}
	for _, tp := range topics {
		seen[tp] = true
	}
	for _, tp := range []string{"subscription.created", "subscription.updated", "subscription.deleted"} {
		if !seen[tp] {
			t.Errorf("webhook %s not delivered (%v)", tp, topics)
		}
	}
	_ = ctx
}

func TestGooSubscriptionsCustomerChanges(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxaEnableGateways(t, srv)
	gxaOn(t, srv, gsubKey, true)
	box := gsubTestProduct(t, srv)
	u := gxaCustomer(t, srv, "cust@example.test")
	other := gxaCustomer(t, srv, "other2@example.test")
	sub := gsubTestSub(t, srv, u.ID, "active")
	sub.Items[0].ProductID = box.ID
	sub.PaymentMethod, sub.PaymentMethodTitle = "goo_test", "Test"
	sub.Meta["_payment_token"] = "tok"
	_ = srv.gsubSave(ctx, sub)
	view := "/my-account/view-subscription/" + itoa64(sub.ID) + "/"
	nonce := srv.nonce(gxaCtx(u, http.MethodGet, "/", nil), "gsub-"+itoa64(sub.ID))
	form := func(kind string, kv url.Values) url.Values {
		kv.Set("gsub_form", kind)
		kv.Set("subscription_id", itoa64(sub.ID))
		kv.Set("_wpnonce", nonce)
		return kv
	}

	// Address.
	if !gsubFrontRoute(srv, gxaCtx(u, http.MethodPost, view, form("address", url.Values{"billing_city": {"Berlin"}, "shipping_city": {"Bonn"}, "billing_email": {"new@example.test"}})), "") {
		t.Fatal("address form not handled")
	}
	got, _ := srv.gsubGet(ctx, sub.ID)
	if got.Billing["city"] != "Berlin" || got.Shipping["city"] != "Bonn" || got.Billing["email"] != "new@example.test" {
		t.Errorf("address: %v %v", got.Billing, got.Shipping)
	}
	gsubFrontRoute(srv, gxaCtx(u, http.MethodPost, view, form("address", url.Values{"billing_email": {"not-an-email"}})), "")
	if got, _ = srv.gsubGet(ctx, sub.ID); got.Billing["email"] != "new@example.test" {
		t.Error("an invalid e-mail was saved")
	}
	// Another customer's subscription, or a bad nonce, changes nothing.
	gsubFrontRoute(srv, gxaCtx(other, http.MethodPost, view, form("address", url.Values{"billing_city": {"Evil"}})), "")
	bad := form("address", url.Values{"billing_city": {"Evil"}})
	bad.Set("_wpnonce", "nope")
	gsubFrontRoute(srv, gxaCtx(u, http.MethodPost, view, bad), "")
	if got, _ = srv.gsubGet(ctx, sub.ID); got.Billing["city"] != "Berlin" {
		t.Errorf("unauthorised address change: %v", got.Billing)
	}

	// Payment: a gateway that renews without a token is offered; the old token is dropped.
	gsubFrontRoute(srv, gxaCtx(u, http.MethodPost, view, form("payment", url.Values{"payment_method": {"cheque"}})), "")
	if got, _ = srv.gsubGet(ctx, sub.ID); got.PaymentMethod != "goo_test" {
		t.Errorf("a manual-only method was accepted: %s", got.PaymentMethod)
	}
	gxaSetExt(t, srv, gsubKey, map[string]any{"accept_manual_renewals": true})
	gsubFrontRoute(srv, gxaCtx(u, http.MethodPost, view, form("payment", url.Values{"payment_method": {"cheque"}})), "")
	got, _ = srv.gsubGet(ctx, sub.ID)
	if got.PaymentMethod != "cheque" || got.Meta["_payment_token"] != nil {
		t.Errorf("payment change: %s %v", got.PaymentMethod, got.Meta)
	}
	if h := srv.gsubAccountExtras(gxaCtx(u, http.MethodGet, view, nil), got); !strings.Contains(h, "Change address") || !strings.Contains(h, "Change payment method") || strings.Contains(h, "Resubscribe") {
		t.Errorf("extras of an active subscription: %s", h)
	}

	// Resubscribe: only for ended subscriptions; the products go back into the cart.
	rs := "?gsub_resubscribe=" + itoa64(sub.ID) + "&_wpnonce=" + nonce
	cc := gxaCtx(u, http.MethodGet, view+rs, nil)
	gsubFrontRoute(srv, cc, "")
	if cart := srv.gooCartFor(gxaCtx(u, http.MethodGet, "/", nil)); !cart.IsEmpty() {
		t.Error("an active subscription was resubscribed")
	}
	got.Status = "cancelled"
	_ = srv.gsubSave(ctx, got)
	if h := srv.gsubAccountExtras(gxaCtx(u, http.MethodGet, view, nil), got); !strings.Contains(h, "Resubscribe") || strings.Contains(h, "Change address") {
		t.Errorf("extras of a cancelled subscription: %s", h)
	}
	cc = gxaCtx(u, http.MethodGet, view+rs, nil)
	if !gsubFrontRoute(srv, cc, "") {
		t.Fatal("resubscribe not handled")
	}
	if loc := cc.Writer.Header().Get("Location"); !strings.Contains(loc, "cart") {
		t.Errorf("resubscribe redirect: %q", loc)
	}
	if cart := srv.gooCartFor(cc); cart.IsEmpty() || cart.Items[0].ProductID != box.ID {
		t.Errorf("resubscribe cart: %+v", cart.Items)
	}
}

func TestGooSubscriptionsAdminManage(t *testing.T) {
	base, cl, srv := gooSite(t)
	ctx := context.Background()
	gxaEnableGateways(t, srv)
	gxaOn(t, srv, gsubKey, true)
	box := gsubTestProduct(t, srv)
	u := gxaCustomer(t, srv, "adm@example.test")

	body := gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=subscriptions", "Add Subscription")
	_ = body
	body = gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=subscriptions&action=new", "Subscription details", "billing_first_name")
	code, _, loc := post(t, cl, base+"/wp-admin/goocommerce?page=subscriptions&action=new", url.Values{
		"_wpnonce": {formNonce(t, body)}, "customer_id": {itoa64(u.ID)}, "status": {"active"}, "payment_method": {"goo_test"},
		"line_product[0]": {itoa64(box.ID)}, "line_qty[0]": {"2"}, "billing_first_name": {"Grace"}})
	if code != http.StatusFound || !strings.Contains(loc, "goo-msg") || !strings.Contains(loc, "action=edit") {
		t.Fatalf("create: %d %s", code, loc)
	}
	list, total, _ := srv.gsubList(ctx, gsubQuery{CustomerID: u.ID})
	if total != 1 || list[0].RecurringTotal != 24 || list[0].Billing["first_name"] != "Grace" || list[0].NextPayment == nil {
		t.Fatalf("created subscription: %d %+v", total, list)
	}
	sub := list[0]
	edit := base + "/wp-admin/goocommerce?page=subscriptions&action=edit&id=" + itoa64(sub.ID)
	body = gxaAdminOK(t, cl, edit, "item_price[0]", "shipping_city", "Process renewal")
	code, _, loc = post(t, cl, edit, url.Values{"_wpnonce": {formNonce(t, body)}, "action": {"editsubscription"}, "id": {itoa64(sub.ID)},
		"billing_city": {"Lyon"}, "shipping_city": {"Nice"}, "item_price[0]": {"20"}, "item_qty[0]": {"3"}, "item_tax[0]": {"6"}})
	if code != http.StatusFound || !strings.Contains(loc, "goo-msg") {
		t.Fatalf("edit: %d %s", code, loc)
	}
	sub, _ = srv.gsubGet(ctx, sub.ID)
	if sub.Billing["city"] != "Lyon" || sub.Shipping["city"] != "Nice" || sub.Items[0].Qty != 3 || sub.Items[0].Total != 60 || sub.RecurringTotal != 66 || sub.RecurringTax != 6 {
		t.Errorf("edited subscription: %v %v %+v total=%v", sub.Billing, sub.Shipping, sub.Items, sub.RecurringTotal)
	}
	// Add a line, then remove the first one; the last line cannot be removed; the customer can change.
	body = gxaAdminOK(t, cl, edit, "add_product", "item_remove[0]", `name="customer_id"`)
	extra := gooMakeSimple(t, srv, "Extra tea", 7, nil)
	u2 := gxaCustomer(t, srv, "adm2@example.test")
	ev := func(kv ...string) url.Values {
		v := url.Values{"_wpnonce": {formNonce(t, body)}, "action": {"editsubscription"}, "id": {itoa64(sub.ID)}}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return v
	}
	if code, _, loc = post(t, cl, edit, ev("add_product", itoa64(extra.ID), "add_qty", "2", "add_total", "14")); code != http.StatusFound || strings.Contains(loc, "goo-err") {
		t.Fatalf("add line: %d %s", code, loc)
	}
	sub, _ = srv.gsubGet(ctx, sub.ID)
	if len(sub.Items) != 2 || sub.Items[1].ProductID != extra.ID || sub.Items[1].Total != 14 || sub.RecurringTotal != 80 {
		t.Fatalf("after adding a line: %+v total=%v", sub.Items, sub.RecurringTotal)
	}
	if code, _, loc = post(t, cl, edit, ev("item_remove[0]", "1", "customer_id", itoa64(u2.ID))); code != http.StatusFound || strings.Contains(loc, "goo-err") {
		t.Fatalf("remove line: %d %s", code, loc)
	}
	sub, _ = srv.gsubGet(ctx, sub.ID)
	if len(sub.Items) != 1 || sub.Items[0].ProductID != extra.ID || sub.RecurringTotal != 14 || sub.RecurringTax != 0 || sub.CustomerID != u2.ID {
		t.Fatalf("after removing a line: %+v total=%v customer=%d", sub.Items, sub.RecurringTotal, sub.CustomerID)
	}
	_, _, _ = post(t, cl, edit, ev("item_remove[0]", "1", "customer_id", "99999999"))
	sub, _ = srv.gsubGet(ctx, sub.ID)
	if len(sub.Items) != 1 || sub.CustomerID != u2.ID {
		t.Errorf("the last line was removed or a missing customer accepted: %+v %d", sub.Items, sub.CustomerID)
	}
	code, _, loc = post(t, cl, edit, url.Values{"_wpnonce": {formNonce(t, body)}, "action": {"editsubscription"}, "id": {itoa64(sub.ID)}, "process_renewal": {"1"}})
	if code != http.StatusFound || !strings.Contains(loc, "goo-msg") {
		t.Fatalf("process renewal: %d %s", code, loc)
	}
	sub, _ = srv.gsubGet(ctx, sub.ID)
	if sub.LastOrderID == 0 {
		t.Fatal("no renewal order")
	}
	if o, err := srv.gooGetOrder(ctx, sub.LastOrderID); err != nil || !containsStr(gooPaidStatuses, o.Status) {
		t.Errorf("renewal order not paid by the stored method: %v %v", o, err)
	}
	if _, err := srv.gsubManualCreate(ctx, gsubNew{CustomerID: u.ID}); err == nil {
		t.Error("a subscription without lines was created")
	}
}
