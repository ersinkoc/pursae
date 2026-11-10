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

// Action topics are delivered, module topics are offered and fired, and every attempt is logged.
func TestGooWebhookActionModuleTopicsAndLog(t *testing.T) {
	gooWebhookAllowPrivate.Store(true)
	defer gooWebhookAllowPrivate.Store(false)
	base, c, srv := gooSite(t)
	ctx := context.Background()
	var mu sync.Mutex
	var topics, bodies []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		topics, bodies = append(topics, r.Header.Get("X-WC-Webhook-Topic")), append(bodies, string(b))
		mu.Unlock()
		w.WriteHeader(202)
		_, _ = w.Write([]byte("thanks"))
	}))
	defer hook.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503); _, _ = w.Write([]byte("down")) }))
	defer failing.Close()
	v3 := gooAdminAPI(t, base, c)

	// Topic validation: a bare "action" needs a hook name; module topics need the module on.
	if r := v3("POST", "/webhooks", map[string]any{"topic": "action", "delivery_url": hook.URL}); r.Code != 400 {
		t.Errorf("bare action accepted: %d", r.Code)
	}
	if r := v3("POST", "/webhooks", map[string]any{"topic": "subscription.created", "delivery_url": hook.URL}); r.Code != 400 {
		t.Errorf("module topic with the module off: %d", r.Code)
	}
	for _, k := range []string{gsubKey, bkKey, gmemKey, gcKey} {
		if err := srv.gooSetExtension(ctx, k, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, topic := range []string{"action.woocommerce_order_created", "subscription.created", "booking.created", "membership.created"} {
		if r := v3("POST", "/webhooks", map[string]any{"topic": topic, "delivery_url": hook.URL, "secret": "s"}); r.Code != 201 {
			t.Fatalf("create %s: %d %s", topic, r.Code, r.Raw)
		}
	}
	var failID int64
	_ = srv.store.Pool.QueryRow(ctx, `INSERT INTO goo_webhooks (name, status, topic, delivery_url, secret) VALUES ('Bad', 'active', 'order.created', $1, 'x') RETURNING id`, failing.URL).Scan(&failID)
	defer func(d []time.Duration) { gooWebhookRetryDelays = d }(gooWebhookRetryDelays)
	gooWebhookRetryDelays = nil

	p := gooMakeSimple(t, srv, "Action product", 3, nil)
	if r := v3("POST", "/orders", map[string]any{"billing": gooTestAddress, "line_items": []any{map[string]any{"product_id": p.ID, "quantity": 1}}}); r.Code != 201 {
		t.Fatalf("order: %d %s", r.Code, r.Raw)
	}
	// Module events.
	u := gxaCustomer(t, srv, "wh@example.test")
	next := time.Now().AddDate(0, 0, 5)
	sub := &gooSubscription{CustomerID: u.ID, Status: "active", Period: "month", Interval: 1, Currency: "USD", Start: time.Now(), NextPayment: &next,
		Billing: map[string]string{}, Shipping: map[string]string{}}
	if err := srv.gsubSave(ctx, sub); err != nil {
		t.Fatal(err)
	}
	bk := &bkBooking{ProductID: p.ID, CustomerID: u.ID, Status: "confirmed", Start: time.Now(), End: time.Now().Add(time.Hour), Persons: map[string]any{}}
	if err := srv.bkInsert(ctx, bk); err != nil {
		t.Fatal(err)
	}
	inCart := &bkBooking{ProductID: p.ID, Status: "in-cart", Start: time.Now(), End: time.Now().Add(time.Hour), Persons: map[string]any{}}
	if err := srv.bkInsert(ctx, inCart); err != nil {
		t.Fatal(err)
	}
	plan := &gooMembershipPlan{Name: "Club", Slug: "club", AccessMethod: "manual", AccessLength: "unlimited"}
	if err := srv.gmemSavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.gmemGrant(ctx, u.ID, plan, 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	gooWebhookWG.Wait()
	mu.Lock()
	joined := strings.Join(topics, ",")
	mu.Unlock()
	for _, want := range []string{"action.woocommerce_order_created", "subscription.created", "booking.created", "membership.created"} {
		if !strings.Contains(joined, want) {
			t.Errorf("topic %s not delivered: %s", want, joined)
		}
	}
	if strings.Count(joined, "booking.created") != 1 {
		t.Errorf("an in-cart booking must not be delivered: %s", joined)
	}
	mu.Lock()
	for i, tp := range topics {
		if tp == "action.woocommerce_order_created" && !strings.Contains(bodies[i], `"action":"order_created"`) {
			t.Errorf("action body: %s", bodies[i])
		}
	}
	mu.Unlock()

	// The log keeps the failing delivery's HTTP code and body; a ping logs one more.
	r := v3("GET", fmt.Sprintf("/webhooks/%d/deliveries", failID), nil)
	l := r.list()
	if r.Code != 200 || len(l) == 0 || int(gooInt(l[0].(map[string]any)["response_code"])) != 503 || l[0].(map[string]any)["response_body"] != "down" || l[0].(map[string]any)["success"] != false {
		t.Fatalf("deliveries: %d %s", r.Code, r.Raw)
	}
	ping := v3("POST", fmt.Sprintf("/webhooks/%d/ping", failID), nil)
	if ping.Code != 200 || int(gooInt(ping.obj()["response_code"])) != 503 {
		t.Errorf("ping: %d %s", ping.Code, ping.Raw)
	}
	if got := len(v3("GET", fmt.Sprintf("/webhooks/%d/deliveries", failID), nil).list()); got != len(l)+1 {
		t.Errorf("log after ping: %d, want %d", got, len(l)+1)
	}
	if r := v3("POST", "/webhooks/99999/ping", nil); r.Code != 404 {
		t.Errorf("ping unknown: %d", r.Code)
	}
}

// The module endpoints list, read and update, and disappear with their module.
func TestGooV3ModuleEndpoints(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	v3 := gooAdminAPI(t, base, c)
	for _, p := range []string{"/subscriptions", "/bookings", "/memberships", "/gift-cards"} {
		if r := v3("GET", p, nil); r.Code != 404 {
			t.Errorf("%s with the module off: %d", p, r.Code)
		}
	}
	for _, k := range []string{gsubKey, bkKey, gmemKey, gcKey} {
		if err := srv.gooSetExtension(ctx, k, true); err != nil {
			t.Fatal(err)
		}
	}
	u := gxaCustomer(t, srv, "api@example.test")
	p := gooMakeSimple(t, srv, "Module product", 9, nil)

	next := time.Now().AddDate(0, 0, 3)
	sub := &gooSubscription{CustomerID: u.ID, Status: "active", Period: "month", Interval: 1, Currency: "USD", RecurringTotal: 9, Start: time.Now(), NextPayment: &next,
		Items: []gsubItem{{ProductID: p.ID, Name: "Module product", Qty: 1, Price: 9, Total: 9}}, Billing: map[string]string{}, Shipping: map[string]string{}}
	if err := srv.gsubSave(ctx, sub); err != nil {
		t.Fatal(err)
	}
	r := v3("GET", "/subscriptions?status=active", nil)
	if r.Code != 200 || len(r.list()) != 1 || r.list()[0].(map[string]any)["billing_period"] != "month" {
		t.Fatalf("subscriptions: %d %s", r.Code, r.Raw)
	}
	if r := v3("GET", "/subscriptions?status=bogus", nil); r.Code != 400 {
		t.Errorf("bad status: %d", r.Code)
	}
	if r := v3("PUT", fmt.Sprintf("/subscriptions/%d", sub.ID), map[string]any{"status": "on-hold"}); r.Code != 200 || r.obj()["status"] != "on-hold" {
		t.Errorf("subscription update: %d %s", r.Code, r.Raw)
	}
	if r := v3("PUT", fmt.Sprintf("/subscriptions/%d", sub.ID), map[string]any{"status": "nope"}); r.Code != 400 {
		t.Errorf("subscription bad status: %d", r.Code)
	}

	bk := &bkBooking{ProductID: p.ID, CustomerID: u.ID, Status: "unpaid", Start: time.Now().Add(24 * time.Hour), End: time.Now().Add(25 * time.Hour), Persons: map[string]any{}}
	if err := srv.bkInsert(ctx, bk); err != nil {
		t.Fatal(err)
	}
	if r := v3("GET", "/bookings?status=unpaid", nil); r.Code != 200 || len(r.list()) != 1 {
		t.Fatalf("bookings: %d %s", r.Code, r.Raw)
	}
	if r := v3("PUT", fmt.Sprintf("/bookings/%d", bk.ID), map[string]any{"status": "confirmed"}); r.Code != 200 || r.obj()["status"] != "confirmed" {
		t.Errorf("booking update: %d %s", r.Code, r.Raw)
	}

	plan := &gooMembershipPlan{Name: "Club", Slug: "club", AccessMethod: "manual", AccessLength: "unlimited"}
	if err := srv.gmemSavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	m, err := srv.gmemGrant(ctx, u.ID, plan, 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if r := v3("GET", fmt.Sprintf("/memberships?customer=%d", u.ID), nil); r.Code != 200 || len(r.list()) != 1 || r.list()[0].(map[string]any)["plan_name"] != "Club" {
		t.Fatalf("memberships: %d %s", r.Code, r.Raw)
	}
	if r := v3("PUT", fmt.Sprintf("/memberships/%d", m.ID), map[string]any{"status": "paused"}); r.Code != 200 || r.obj()["status"] != "paused" || r.obj()["has_access"] != false {
		t.Errorf("membership update: %d %s", r.Code, r.Raw)
	}

	card := &gcCard{Initial: 40, Recipient: "r@example.test", Sender: "Shop"}
	if err := srv.gcCreate(ctx, card, 1, "test"); err != nil {
		t.Fatal(err)
	}
	if r := v3("GET", fmt.Sprintf("/gift-cards/%d", card.ID), nil); r.Code != 200 || r.obj()["balance"] != float64(40) || len(r.obj()["activity"].([]any)) != 1 {
		t.Fatalf("gift card: %d %s", r.Code, r.Raw)
	}
	if r := v3("PUT", fmt.Sprintf("/gift-cards/%d", card.ID), map[string]any{"status": "disabled", "date_expires": "2031-01-02"}); r.Code != 200 || r.obj()["status"] != "disabled" || r.obj()["date_expires"] != "2031-01-02" {
		t.Errorf("gift card update: %d %s", r.Code, r.Raw)
	}
	if r := v3("GET", "/gift-cards?status=disabled", nil); len(r.list()) != 1 {
		t.Errorf("gift card filter: %s", r.Raw)
	}
	if r := v3("GET", "/gift-cards?status=bogus", nil); r.Code != 400 {
		t.Errorf("gift card bad status: %d", r.Code)
	}
	if r := v3("GET", "/subscriptions/99999", nil); r.Code != 404 {
		t.Errorf("unknown id: %d", r.Code)
	}
}

// The Store API add-item body carries every module's form fields under extensions.<namespace>.
func TestPaoStoreFormAllModules(t *testing.T) {
	f := paoStoreForm(map[string]any{"extensions": map[string]any{
		"gift_cards": map[string]any{"wc_gc_giftcard_to": "a@example.test", "wc_gc_giftcard_amount": float64(50)},
		"bookings":   map[string]any{"wc_bookings_field_start_date_year": "2031"},
	}})
	if gxForm(f, "wc_gc_giftcard_to") != "a@example.test" || gxForm(f, "wc_gc_giftcard_amount") != "50" || gxForm(f, "wc_bookings_field_start_date_year") != "2031" {
		t.Errorf("flattened form: %v", f)
	}
}

// A store customer gets the store's "Reset password" e-mail; any other account gets the core message.
func TestGooResetPasswordMailByRole(t *testing.T) {
	base, c, srv := gooSite(t)
	anon := &http.Client{CheckRedirect: c.CheckRedirect}
	gxaCustomer(t, srv, "shopper@example.test")
	for login, subject := range map[string]string{"shopper@example.test": "Password Reset Request for", "admin": "] Password Reset"} {
		if code, _, _ := post(t, anon, base+"/wp-login?action=lostpassword", url.Values{"user_login": {login}}); code != http.StatusFound {
			t.Fatalf("lost password %s: %d", login, code)
		}
		if m := lastMail(t, subject); !strings.Contains(m.Body, "action=rp") {
			t.Errorf("%s: no reset link in %q", login, m.Subject)
		}
	}
	for _, m := range sentMail(t) {
		if strings.Contains(m.Subject, "] Password Reset") && m.To[0] == "shopper@example.test" {
			t.Errorf("the customer also got the core message")
		}
	}
}
