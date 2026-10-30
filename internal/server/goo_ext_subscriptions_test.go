package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Test helpers of the Subscriptions, Memberships, Pre-Orders and Points modules -------------------------

// gxaMails records the module e-mails triggered during a test.
type gxaMails struct {
	mu  sync.Mutex
	ids []string
}

func (m *gxaMails) has(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.ids {
		if v == id {
			return true
		}
	}
	return false
}

func (m *gxaMails) reset() {
	m.mu.Lock()
	m.ids = nil
	m.mu.Unlock()
}

func gxaRecordMails(t *testing.T) *gxaMails {
	t.Helper()
	rec := &gxaMails{}
	old := gooEmailSender
	gooEmailSender = func(s *Server, ctx context.Context, id string, o *gooOrder, args map[string]any) error {
		rec.mu.Lock()
		rec.ids = append(rec.ids, id)
		rec.mu.Unlock()
		return nil
	}
	t.Cleanup(func() { gooEmailSender = old })
	return rec
}

// gxaCtx is a request context (GET, or POST with a form) of a user (nil = a visitor).
func gxaCtx(u *store.User, method, target string, form url.Values) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if method == http.MethodPost {
		c.Request = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		c.Request = httptest.NewRequest(method, target, nil)
	}
	if u != nil {
		c.Set(ctxUser, u)
		c.Request = c.Request.WithContext(withPluginUser(c.Request.Context(), u))
	}
	return c
}

// gxaCustomer creates a customer account.
func gxaCustomer(t *testing.T, srv *Server, email string) *store.User {
	t.Helper()
	ctx := context.Background()
	id, err := srv.gooCreateCustomer(ctx, email, "", "", map[string]string{"billing_first_name": "Ada", "billing_last_name": "Lovelace"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := srv.store.UserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// gxaCheckoutForm is a valid checkout with a payment method.
func gxaCheckoutForm(email, method string) map[string][]string {
	return map[string][]string{
		"billing_first_name": {"Ada"}, "billing_last_name": {"Lovelace"}, "billing_country": {"US"}, "billing_address_1": {"1 Main St"},
		"billing_city": {"Austin"}, "billing_state": {"TX"}, "billing_postcode": {"73301"}, "billing_phone": {"555-1234"},
		"billing_email": {email}, "payment_method": {method},
	}
}

func gxaEnableGateways(t *testing.T, srv *Server) {
	t.Helper()
	if err := srv.gooSetOpts(context.Background(), map[string]any{"goo_test_settings": map[string]any{"enabled": "yes"},
		"cheque_settings": map[string]any{"enabled": "yes"}}); err != nil {
		t.Fatal(err)
	}
}

func gxaPanel(t *testing.T, id string) *gooProductPanel {
	t.Helper()
	for _, p := range gooProductPanels {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("no product panel %s", id)
	return nil
}

// gxaSavePanel posts a product panel's form and saves the product.
func gxaSavePanel(t *testing.T, srv *Server, panel string, p *gooProduct, form url.Values) *gooProduct {
	t.Helper()
	c := gxaCtx(nil, http.MethodPost, "/wp-admin/post", form)
	gxaPanel(t, panel).Save(srv, c, p)
	if err := srv.gooSaveProduct(context.Background(), p, 1); err != nil {
		t.Fatal(err)
	}
	out, err := srv.gooGetProduct(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// gxaAdminOK fetches a module admin screen: 200 without a template error.
func gxaAdminOK(t *testing.T, cl *http.Client, u string, want ...string) string {
	t.Helper()
	code, body, _ := get(t, cl, u)
	if code != http.StatusOK || strings.Contains(body, "Template error") {
		t.Fatalf("%s: %d %.300s", u, code, body)
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("%s lacks %q", u, w)
		}
	}
	return body
}

func gxaOn(t *testing.T, srv *Server, key string, on bool) {
	t.Helper()
	if err := srv.gooSetExtension(context.Background(), key, on); err != nil {
		t.Fatal(err)
	}
}

func gxaSetExt(t *testing.T, srv *Server, key string, vals map[string]any) {
	t.Helper()
	cur := srv.gooExtSettings(key)
	for k, v := range vals {
		cur[k] = v
	}
	if err := srv.gooSetOpts(context.Background(), map[string]any{"ext_" + key + "_settings": cur}); err != nil {
		t.Fatal(err)
	}
}

// Subscriptions ------------------------------------------------------------------------------------------

func TestGooExtSubscriptions(t *testing.T) {
	base, cl, srv := gooSite(t)
	ctx := context.Background()
	mails := gxaRecordMails(t)
	gxaEnableGateways(t, srv)

	box := gooNewProduct("subscription")
	box.Name, box.RegularPrice, box.Virtual = "Coffee Box", gooFloatPtr(10), true
	if err := srv.gooSaveProduct(ctx, box, 1); err != nil {
		t.Fatal(err)
	}
	// Off: the type is not purchasable and the price shows no schedule.
	if srv.gooIsPurchasable(ctx, box) {
		t.Error("a subscription is purchasable while the module is off")
	}
	if h := string(srv.gooFilterPriceHTML(ctx, box, srv.gooPrice(10))); strings.Contains(h, "month") {
		t.Errorf("price html while off: %s", h)
	}

	gxaOn(t, srv, gsubKey, true)
	// The product panel saves the schedule.
	if h := string(gxaPanel(t, "subscription_pricing").Render(srv, gooTestCtx(), box)); !strings.Contains(h, `name="_subscription_price"`) || !strings.Contains(h, "every 2nd") {
		t.Errorf("panel: %.200s", h)
	}
	box = gxaSavePanel(t, srv, "subscription_pricing", box, url.Values{"_subscription_price": {"10"}, "_subscription_period_interval": {"1"},
		"_subscription_period": {"month"}, "_subscription_length": {"6"}, "_subscription_sign_up_fee": {"5"}, "_subscription_trial_length": {"7"},
		"_subscription_trial_period": {"day"}, "_subscription_limit": {"no"}})
	if box.RegularPrice == nil || *box.RegularPrice != 10 || gxaStr(box.Meta, "_subscription_period") != "month" || gxaInt(box.Meta, "_subscription_length") != 6 {
		t.Fatalf("saved schedule: %v %v", box.RegularPrice, box.Meta)
	}
	plain := strings.Join(strings.Fields(gsubPlain(string(srv.gooFilterPriceHTML(ctx, box, srv.gooPrice(10))))), " ")
	if want := "$10.00 / month for 6 months with a 7-day free trial and a $5.00 sign-up fee"; plain != want {
		t.Errorf("price string %q, want %q", plain, want)
	}
	if def := gooProductTypes["subscription"]; def.AddToCartText(srv, box, true) != "Sign up now" {
		t.Error("button text")
	}

	// A guest can not check out a subscription without an account.
	gc := gooTestCtx()
	if _, err := srv.gooCartFor(gc).Add(gc, box.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, errs := srv.gooProcessCheckout(gc, gxaCheckoutForm("guest@example.test", "goo_test")); len(errs) == 0 || !strings.Contains(strings.Join(errs, " "), "create an account") {
		t.Errorf("guest checkout: %v", errs)
	}

	// The customer's first payment is the sign-up fee (free trial).
	u := gxaCustomer(t, srv, "subscriber@example.test")
	c := gxaCtx(u, http.MethodGet, "/", nil)
	cart := srv.gooCartFor(c)
	if _, err := cart.Add(c, box.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "first payment", cart.Total, 5)
	if h := string(srv.gsubRecurringTotals(c, cart)); !strings.Contains(h, "Recurring totals") || !strings.Contains(h, "First renewal") {
		t.Errorf("recurring totals: %s", h)
	}
	res, order, errs := srv.gooProcessCheckout(c, gxaCheckoutForm(u.Email, "goo_test"))
	if len(errs) > 0 || res == nil {
		t.Fatalf("checkout: %v", errs)
	}
	subs, _, _ := srv.gsubList(ctx, gsubQuery{ParentOrderID: order.ID})
	if len(subs) != 1 {
		t.Fatalf("subscriptions of the order: %d", len(subs))
	}
	sub := subs[0]
	if sub.Status != "active" || sub.CustomerID != u.ID || sub.Period != "month" || sub.TrialEnd == nil || sub.NextPayment == nil || sub.End == nil {
		t.Fatalf("subscription: %+v", sub)
	}
	gooAlmost(t, "recurring total", sub.RecurringTotal, 10)
	if d := sub.NextPayment.Sub(time.Now()); d < 6*24*time.Hour || d > 8*24*time.Hour {
		t.Errorf("next payment in %v, want the trial end (7 days)", d)
	}
	if want := gxaAddPeriod(*sub.TrialEnd, "month", 6); sub.End.Sub(want).Abs() > time.Minute {
		t.Errorf("end %v, want %v", sub.End, want)
	}

	// Automatic renewal with the test gateway.
	past := time.Now().Add(-time.Hour)
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE goo_subscriptions SET next_payment = $2 WHERE id = $1`, sub.ID, past)
	srv.gooFire(ctx, "hourly", nil)
	sub, _ = srv.gsubGet(ctx, sub.ID)
	renewal, err := srv.gooGetOrder(ctx, sub.LastOrderID)
	if err != nil || renewal.ID == order.ID || renewal.CreatedVia != "subscription" || gxaInt64(renewal.Meta, "_subscription_renewal") != sub.ID {
		t.Fatalf("renewal order: %+v %v", renewal, err)
	}
	if renewal.Status != "processing" || sub.Status != "active" || sub.NextPayment == nil || !sub.NextPayment.After(time.Now()) {
		t.Errorf("after renewal: order %s, subscription %s next %v", renewal.Status, sub.Status, sub.NextPayment)
	}
	gooAlmost(t, "renewal total", renewal.Total, 10)
	if !mails.has("new_renewal_order") || !mails.has("customer_processing_renewal_order") {
		t.Errorf("renewal mails: %v", mails.ids)
	}
	if len(srv.gsubRelatedOrders(ctx, sub)) != 2 {
		t.Errorf("related orders: %d", len(srv.gsubRelatedOrders(ctx, sub)))
	}

	// A manual renewal is invoiced, then put on hold after the grace period; paying reactivates.
	mails.reset()
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE goo_subscriptions SET next_payment = $2, payment_method = 'cheque' WHERE id = $1`, sub.ID, past)
	srv.gooFire(ctx, "hourly", nil)
	sub, _ = srv.gsubGet(ctx, sub.ID)
	manual, _ := srv.gooGetOrder(ctx, sub.LastOrderID)
	if manual.Status != "pending" || !mails.has("customer_invoice") || sub.Status != "active" {
		t.Fatalf("manual renewal: order %s, sub %s, mails %v", manual.Status, sub.Status, mails.ids)
	}
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE goo_orders SET date_created = now() - interval '10 days' WHERE id = $1`, manual.ID)
	srv.gooFire(ctx, "hourly", nil)
	if sub, _ = srv.gsubGet(ctx, sub.ID); sub.Status != "on-hold" {
		t.Errorf("unpaid past grace: %s", sub.Status)
	}
	manual, _ = srv.gooGetOrder(ctx, manual.ID)
	if err := srv.gooPaymentComplete(ctx, manual, "tx"); err != nil {
		t.Fatal(err)
	}
	if sub, _ = srv.gsubGet(ctx, sub.ID); sub.Status != "active" || gxaInt64(sub.Meta, "_pending_renewal") != 0 {
		t.Errorf("after paying the renewal: %s %v", sub.Status, sub.Meta["_pending_renewal"])
	}

	// My Account.
	if h := string(gsubAccountList(srv, c, "")); !strings.Contains(h, "#"+itoa64(sub.ID)) || !strings.Contains(h, "woocommerce-MyAccount-subscriptions") {
		t.Errorf("account list: %.300s", h)
	}
	if h := string(gsubAccountView(srv, c, itoa64(sub.ID))); !strings.Contains(h, "Related orders") || !strings.Contains(h, "Cancel") {
		t.Errorf("account view: %.300s", h)
	}
	if h := string(gsubAccountView(srv, gxaCtx(gxaCustomer(t, srv, "other@example.test"), http.MethodGet, "/", nil), itoa64(sub.ID))); !strings.Contains(h, "Invalid subscription") {
		t.Error("another customer sees the subscription")
	}

	// Customer suspension (allowed once) and cancellation to pending-cancel.
	gxaSetExt(t, srv, gsubKey, map[string]any{"max_customer_suspensions": 1.0})
	nonce := srv.nonce(c, "gsub-"+itoa64(sub.ID))
	act := func(to string) {
		cc := gxaCtx(u, http.MethodGet, "/my-account/view-subscription/"+itoa64(sub.ID)+"/?subscription_id="+itoa64(sub.ID)+"&change_subscription_to="+to+"&_wpnonce="+nonce, nil)
		if !gsubFrontRoute(srv, cc, "") {
			t.Fatalf("front route did not handle %s", to)
		}
	}
	act("on-hold")
	if sub, _ = srv.gsubGet(ctx, sub.ID); sub.Status != "on-hold" {
		t.Errorf("suspend: %s", sub.Status)
	}
	act("active")
	if sub, _ = srv.gsubGet(ctx, sub.ID); sub.Status != "active" {
		t.Errorf("reactivate: %s", sub.Status)
	}
	if len(srv.gsubCustomerActions(ctx, sub)) == 0 || srv.gsubCustomerActions(ctx, sub)[0].Value == "on-hold" {
		t.Error("a second suspension is offered")
	}
	act("cancelled")
	if sub, _ = srv.gsubGet(ctx, sub.ID); sub.Status != "pending-cancel" || sub.End == nil || sub.NextPayment != nil {
		t.Errorf("cancel: %s end %v next %v", sub.Status, sub.End, sub.NextPayment)
	}
	if !mails.has("cancelled_subscription") {
		t.Error("no cancelled subscription e-mail")
	}

	// Limit to one active subscription.
	box.Meta["_subscription_limit"] = "active"
	_ = srv.gooSaveProduct(ctx, box, 1)
	if srv.gooIsPurchasable(c.Request.Context(), box) {
		t.Error("a limited subscription is purchasable again")
	}

	// End of the prepaid period → cancelled; an active subscription past its end → expired.
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE goo_subscriptions SET end_date = $2 WHERE id = $1`, sub.ID, past)
	srv.gooFire(ctx, "hourly", nil)
	if sub, _ = srv.gsubGet(ctx, sub.ID); sub.Status != "cancelled" {
		t.Errorf("pending-cancel at its end: %s", sub.Status)
	}
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE goo_subscriptions SET status = 'active', end_date = $2 WHERE id = $1`, sub.ID, past)
	srv.gooFire(ctx, "hourly", nil)
	if sub, _ = srv.gsubGet(ctx, sub.ID); sub.Status != "expired" || !mails.has("expired_subscription") {
		t.Errorf("expiry: %s", sub.Status)
	}

	// Mixed checkout off: a simple product can not join a subscription.
	gxaSetExt(t, srv, gsubKey, map[string]any{"multiple_purchase": false})
	box.Meta["_subscription_limit"] = "no"
	_ = srv.gooSaveProduct(ctx, box, 1)
	mug := gooMakeSimple(t, srv, "Mug", 8, func(p *gooProduct) { p.Virtual = true })
	c3 := gxaCtx(u, http.MethodGet, "/", nil)
	if _, err := srv.gooCartFor(c3).Add(c3, box.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.gooCartFor(c3).Add(c3, mug.ID, 1, 0, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "can not be purchased at the same time") {
		t.Errorf("mixed checkout: %v", err)
	}

	// Admin screens.
	gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=subscriptions", "#"+itoa64(sub.ID), "Next Payment")
	gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=subscriptions&status=expired", "#"+itoa64(sub.ID))
	body := gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=subscriptions&action=edit&id="+itoa64(sub.ID), "Related Orders", "Subscription notes")
	future := time.Now().AddDate(0, 1, 0).Format("2006-01-02T15:04")
	code, _, loc := post(t, cl, base+"/wp-admin/goocommerce?page=subscriptions&action=edit&id="+itoa64(sub.ID), url.Values{
		"_wpnonce": {formNonce(t, body)}, "action": {"editsubscription"}, "id": {itoa64(sub.ID)}, "order_status": {"active"}, "next_payment": {future}, "end_never": {"1"}})
	if code != http.StatusFound || !strings.Contains(loc, "goo-msg") {
		t.Fatalf("admin save: %d %s", code, loc)
	}
	if sub, _ = srv.gsubGet(ctx, sub.ID); sub.Status != "active" || sub.NextPayment == nil || sub.End != nil {
		t.Errorf("after the admin save: %s %v %v", sub.Status, sub.NextPayment, sub.End)
	}
	_, _, _ = post(t, cl, base+"/wp-admin/goocommerce?page=subscriptions&action=edit&id="+itoa64(sub.ID), url.Values{
		"_wpnonce": {formNonce(t, body)}, "action": {"editsubscription"}, "id": {itoa64(sub.ID)}, "subscription_note": {"Called the customer"}, "add_note": {"1"}})
	if sub, _ = srv.gsubGet(ctx, sub.ID); !strings.Contains(gxaStr(gxaNoteList(sub.Meta["_notes"])[0], "content"), "Called the customer") {
		t.Error("admin note not added")
	}
	gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=ext&ext=subscriptions", "Mixed Checkout")
}
