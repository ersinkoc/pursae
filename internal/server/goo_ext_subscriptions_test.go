package server

import (
	"context"
	"html"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
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
	if manual.Status != "pending" || !mails.has("customer_renewal_invoice") || sub.Status != "active" {
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
	// The detail page is no tab of its own: it lights up Subscriptions.
	nav := srv.gooAccountNav(c, "view-subscription")
	if strings.Contains(nav, "view-subscription") || strings.Contains(nav, "></a>") {
		t.Errorf("account menu lists the subscription detail page: %s", nav)
	}
	if !regexp.MustCompile(`--subscriptions[^"]*is-active`).MatchString(nav) {
		t.Errorf("Subscriptions is not active on a subscription page: %s", nav)
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
	// The allowance is per billing period: a paid renewal order starts a new one.
	if gxaInt(sub.Meta, "_suspension_count") != 1 {
		t.Errorf("suspension count before renewal: %v", sub.Meta["_suspension_count"])
	}
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE goo_subscriptions SET next_payment = $2, payment_method = 'cheque' WHERE id = $1`, sub.ID, past)
	srv.gooFire(ctx, "hourly", nil)
	sub, _ = srv.gsubGet(ctx, sub.ID)
	if ro, err := srv.gooGetOrder(ctx, sub.LastOrderID); err == nil && ro.Status == "pending" {
		if err := srv.gooPaymentComplete(ctx, ro, "tx2"); err != nil {
			t.Fatal(err)
		}
		sub, _ = srv.gsubGet(ctx, sub.ID)
	}
	if gxaInt(sub.Meta, "_suspension_count") != 0 {
		t.Errorf("suspension count not reset by the paid renewal: %v", sub.Meta["_suspension_count"])
	}
	if acts := srv.gsubCustomerActions(ctx, sub); len(acts) == 0 || acts[0].Value != "on-hold" {
		t.Error("suspension is not offered again in the new period")
	}
	mails.reset()
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
	if !mails.has("cancelled_subscription") {
		t.Error("no cancelled e-mail when pending-cancel reaches its end")
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

// Variable subscriptions, switching, synchronised renewals ----------------------------------------------

// gxsPlan is a variation of a variable subscription made by gxsMakeVariableSub.
type gxsPlan struct {
	Name        string
	Price       float64
	Period      string
	Interval    int
	Length      int
	Fee         float64
	Trial       int
	TrialPeriod string
}

// gxsMakeVariableSub creates a published variable subscription with one virtual variation per plan
// (attribute "Plan").
func gxsMakeVariableSub(t *testing.T, srv *Server, name string, plans ...gxsPlan) (*gooProduct, []*gooProduct) {
	t.Helper()
	ctx := context.Background()
	p := gooNewProduct(gooSubsTypeVariable)
	p.Name = name
	var opts []string
	for _, pl := range plans {
		opts = append(opts, pl.Name)
	}
	p.Attributes = []gooProductAttr{{Name: "Plan", Options: opts, Visible: true, Variation: true}}
	if err := srv.gooSaveProduct(ctx, p, 1); err != nil {
		t.Fatal(err)
	}
	var vars []*gooProduct
	for _, pl := range plans {
		v := gooNewProduct(gooTypeVariation)
		v.ParentID, v.Name, v.RegularPrice, v.Virtual = p.ID, name+" - "+pl.Name, gooFloatPtr(pl.Price), true
		v.VariationAttrs = map[string]string{"plan": pl.Name}
		v.Meta = map[string]any{"_subscription_period": defaultStr(pl.Period, "month"), "_subscription_period_interval": float64(max(pl.Interval, 1)),
			"_subscription_length": float64(pl.Length), "_subscription_sign_up_fee": pl.Fee, "_subscription_trial_length": float64(pl.Trial),
			"_subscription_trial_period": defaultStr(pl.TrialPeriod, "day")}
		if err := srv.gooSaveProduct(ctx, v, 1); err != nil {
			t.Fatal(err)
		}
		vars = append(vars, v)
	}
	p, _ = srv.gooGetProduct(ctx, p.ID)
	return p, vars
}

func gxsPlainPrice(srv *Server, p *gooProduct) string {
	return strings.Join(strings.Fields(html.UnescapeString(gsubPlain(string(srv.gooPriceHTML(gooTestCtx(), p))))), " ")
}

// gxsBuy checks out a product (a variation with variationID) for a customer with the test gateway.
func gxsBuy(t *testing.T, srv *Server, u *store.User, productID, variationID int64) *gooOrder {
	t.Helper()
	c := gxaCtx(u, http.MethodGet, "/", nil)
	if _, err := srv.gooCartFor(c).Add(c, productID, 1, variationID, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	_, o, errs := srv.gooProcessCheckout(c, gxaCheckoutForm(u.Email, "goo_test"))
	if len(errs) > 0 || o == nil {
		t.Fatalf("checkout: %v", errs)
	}
	return o
}

func TestGooExtSubscriptionsVariable(t *testing.T) {
	base, cl, srv := gooSite(t)
	ctx := context.Background()
	gxaEnableGateways(t, srv)
	gxaOn(t, srv, gsubKey, true)

	// The product editor: the type, then the variations' schedule fields.
	id, nonce, body := gooCatEditor(t, cl, base+"/wp-admin/post-new?post_type=product")
	if !strings.Contains(body, `value="variable-subscription"`) || !strings.Contains(body, "Variable subscription") {
		t.Fatal("no Variable subscription product type")
	}
	form := url.Values{"post_title": {"Coffee Club"}, "product-type": {gooSubsTypeVariable}, "goo_attributes_loaded": {"1"},
		"attribute_names[0]": {"Plan"}, "attribute_values[0]": {"Basic | Pro"}, "attribute_visibility[0]": {"1"}, "attribute_variation[0]": {"1"},
		"attribute_position[0]": {"0"}, "goo_generate_variations": {"1"}, "_subscription_limit": {"no"}}
	if loc := gooCatSave(t, cl, base, id, nonce, form); !strings.Contains(loc, "goo_variations_added=2") {
		t.Fatalf("generate: %s", loc)
	}
	vars, _ := srv.gooVariations(ctx, id, true)
	if len(vars) != 2 {
		t.Fatalf("%d variations", len(vars))
	}
	_, nonce, body = gooCatEditor(t, cl, base+"/wp-admin/post?post="+itoa64(id)+"&action=edit")
	for _, want := range []string{"variable_subscription_period[0]", "variable_subscription_sign_up_fee[1]", "show_if_variable-subscription"} {
		if !strings.Contains(body, want) {
			t.Errorf("variation fields lack %q", want)
		}
	}
	form.Del("goo_generate_variations")
	form.Set("publish", "Publish")
	form.Set("goo_variations_loaded", "1")
	fields := []map[string]string{
		{"variable_regular_price": "10", "variable_subscription_period": "month", "variable_subscription_period_interval": "1", "variable_subscription_length": "0",
			"variable_subscription_sign_up_fee": "", "variable_subscription_trial_length": "7", "variable_subscription_trial_period": "day"},
		{"variable_regular_price": "25", "variable_subscription_period": "month", "variable_subscription_period_interval": "2", "variable_subscription_length": "12",
			"variable_subscription_sign_up_fee": "5", "variable_subscription_trial_length": "0", "variable_subscription_trial_period": "day"},
	}
	for i, v := range vars {
		k := func(n string) string { return n + "[" + strconv.Itoa(i) + "]" }
		form.Set(k("variable_post_id"), itoa64(v.ID))
		form.Set(k("attribute_plan"), v.VariationAttrs["plan"])
		form.Set(k("variable_enabled"), "on")
		form.Set(k("variable_is_virtual"), "on")
		for name, val := range fields[i] {
			form.Set(k(name), val)
		}
	}
	gooCatSave(t, cl, base, id, nonce, form)
	parent, _ := srv.gooGetProduct(ctx, id)
	vars, _ = srv.gooVariations(ctx, id, true)
	basic, pro := vars[0], vars[1]
	if parent.Type != gooSubsTypeVariable || parent.MinPrice == nil || *parent.MinPrice != 10 || *parent.MaxPrice != 25 {
		t.Fatalf("parent: %s %v %v", parent.Type, parent.MinPrice, parent.MaxPrice)
	}
	if sc := gsubScheduleOf(pro); sc.Interval != 2 || sc.Length != 12 || sc.SignUpFee != 5 || sc.Price != 25 {
		t.Errorf("pro schedule: %+v", sc)
	}
	if sc := gsubScheduleOf(basic); sc.TrialLength != 7 || sc.Period != "month" {
		t.Errorf("basic schedule: %+v", sc)
	}

	// Storefront price strings.
	if got, want := gxsPlainPrice(srv, basic), "$10.00 / month with a 7-day free trial"; got != want {
		t.Errorf("basic price %q, want %q", got, want)
	}
	if got, want := gxsPlainPrice(srv, pro), "$25.00 every 2 months for 12 months and a $5.00 sign-up fee"; got != want {
		t.Errorf("pro price %q, want %q", got, want)
	}
	if got := gxsPlainPrice(srv, parent); !strings.HasPrefix(got, "From: $10.00 / month") {
		t.Errorf("parent price %q", got)
	}
	if data := srv.gooVariationData(gooTestCtx(), parent, pro); !strings.Contains(gooStr(data["price_html"]), "every 2 months") {
		t.Errorf("variation price_html: %v", data["price_html"])
	}
	if txt := srv.gooAddToCartText(gooTestCtx(), parent, false); txt != "Select options" {
		t.Errorf("loop button %q", txt)
	}

	// REST: the product type value.
	v3 := gooAdminAPI(t, base, cl)
	if r := v3("GET", "/products/"+itoa64(id), nil); r.Code != 200 || gooJS(r.Body, "type") != gooSubsTypeVariable || len(gooJ(r.Body, "variations").([]any)) != 2 {
		t.Errorf("REST product: %d %.300s", r.Code, r.Raw)
	}
	if r := v3("POST", "/products", map[string]any{"name": "Tea Club", "type": gooSubsTypeVariable}); r.Code != 201 || gooJS(r.Body, "type") != gooSubsTypeVariable {
		t.Errorf("REST create: %d %.300s", r.Code, r.Raw)
	}

	// Cart and checkout of a variation: the first payment is its price and sign-up fee.
	u := gxaCustomer(t, srv, "club@example.test")
	c := gxaCtx(u, http.MethodGet, "/", nil)
	cart := srv.gooCartFor(c)
	if _, err := cart.Add(c, parent.ID, 1, pro.ID, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "first payment", cart.Total, 30)
	if h := string(srv.gsubRecurringTotals(c, cart)); !strings.Contains(h, "every 2 months") {
		t.Errorf("recurring totals: %s", h)
	}
	_, order, errs := srv.gooProcessCheckout(c, gxaCheckoutForm(u.Email, "goo_test"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	subs, _, _ := srv.gsubList(ctx, gsubQuery{ParentOrderID: order.ID})
	if len(subs) != 1 {
		t.Fatalf("%d subscriptions", len(subs))
	}
	if sub := subs[0]; sub.Items[0].VariationID != pro.ID || sub.Items[0].ProductID != parent.ID || sub.Interval != 2 || sub.End == nil || sub.RecurringTotal != 25 {
		t.Errorf("subscription: %+v", sub)
	}

	// The variable subscription's purchase limit.
	parent.Meta["_subscription_limit"] = "active"
	_ = srv.gooSaveProduct(ctx, parent, 1)
	c2 := gxaCtx(u, http.MethodGet, "/", nil)
	if _, err := srv.gooCartFor(c2).Add(c2, parent.ID, 1, basic.ID, nil, nil, nil); err == nil {
		t.Error("the purchase limit lets a second subscription in")
	}

	// Off: the variations can not be bought.
	gxaOn(t, srv, gsubKey, false)
	basic, _ = srv.gooGetProduct(ctx, basic.ID)
	if srv.gooIsPurchasable(ctx, basic) {
		t.Error("a variable subscription variation is purchasable while the module is off")
	}
}

func TestGooExtSubscriptionsSwitch(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxaEnableGateways(t, srv)
	gxaOn(t, srv, gsubKey, true)
	gxaSetExt(t, srv, gsubKey, map[string]any{"allow_switching": "variable_grouped", "apportion_recurring_price": "yes", "apportion_sign_up_fee": "no"})
	parent, vars := gxsMakeVariableSub(t, srv, "Streaming", gxsPlan{Name: "Basic", Price: 10}, gxsPlan{Name: "Premium", Price: 30, Fee: 4})
	basic, premium := vars[0], vars[1]

	u := gxaCustomer(t, srv, "switcher@example.test")
	o := gxsBuy(t, srv, u, parent.ID, basic.ID)
	subs, _, _ := srv.gsubList(ctx, gsubQuery{ParentOrderID: o.ID})
	if len(subs) != 1 {
		t.Fatalf("%d subscriptions", len(subs))
	}
	sub := subs[0]
	next := *sub.NextPayment

	// My Account › View subscription offers the switch.
	c := gxaCtx(u, http.MethodGet, "/", nil)
	view := string(gsubAccountView(srv, c, itoa64(sub.ID)))
	m := regexp.MustCompile(`href="([^"]*switch-subscription=[^"]*)" class="wcs-switch-link button">Upgrade or Downgrade<`).FindStringSubmatch(view)
	if m == nil {
		t.Fatalf("no switch link: %.500s", view)
	}
	link, _ := url.Parse(html.UnescapeString(m[1]))
	q := link.Query()
	if q.Get("switch-subscription") != itoa64(sub.ID) || q.Get("item") != "0" {
		t.Fatalf("switch link %s", link)
	}
	// The product page carries the switch through its form.
	pc := gxaCtx(u, http.MethodGet, "/?"+q.Encode(), nil)
	if h := string(srv.gooHTML(pc, "single_before_add_to_cart", parent)); !strings.Contains(h, `name="switch-subscription" value="`+itoa64(sub.ID)+`"`) || !strings.Contains(h, "Choose a new subscription") {
		t.Errorf("switch fields: %s", h)
	}
	switchForm := map[string][]string{"switch-subscription": {itoa64(sub.ID)}, "item": {"0"}, "_wcsnonce": {q.Get("_wcsnonce")}}

	// Switching to the same plan is refused; a bad nonce too.
	c1 := gxaCtx(u, http.MethodPost, "/", url.Values(switchForm))
	if _, err := srv.gooCartFor(c1).Add(c1, parent.ID, 1, basic.ID, nil, switchForm, nil); err == nil || !strings.Contains(err.Error(), "same subscription") {
		t.Errorf("same plan: %v", err)
	}
	bad := map[string][]string{"switch-subscription": {itoa64(sub.ID)}, "item": {"0"}, "_wcsnonce": {"nope"}}
	if _, err := srv.gooCartFor(c1).Add(c1, parent.ID, 1, premium.ID, nil, bad, nil); err == nil {
		t.Error("a switch with a bad nonce was accepted")
	}

	// Upgrade, prorated: the difference for the rest of the paid month is due now (no sign-up fee).
	cart := srv.gooCartFor(c1)
	if _, err := cart.Add(c1, parent.ID, 1, premium.ID, nil, switchForm, nil); err != nil {
		t.Fatal(err)
	}
	if math.Abs(cart.Total-20) > 0.75 {
		t.Errorf("prorated upgrade = %.2f, want about 20", cart.Total)
	}
	if h := string(srv.gsubRecurringTotals(c1, cart)); !strings.Contains(h, srv.gxaDate(&next)) {
		t.Errorf("recurring totals do not keep the next payment %s: %s", srv.gxaDate(&next), h)
	}
	_, so, errs := srv.gooProcessCheckout(c1, gxaCheckoutForm(u.Email, "goo_test"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if gxaInt64(so.Meta, "_subscription_switch") != sub.ID {
		t.Errorf("switch order meta: %v", so.Meta)
	}
	sub, _ = srv.gsubGet(ctx, sub.ID)
	if sub.Items[0].VariationID != premium.ID || sub.RecurringTotal != 30 || sub.NextPayment == nil || sub.NextPayment.Sub(next).Abs() > time.Second {
		t.Errorf("after the upgrade: %+v next %v (was %v)", sub.Items, sub.NextPayment, next)
	}
	if all, _, _ := srv.gsubList(ctx, gsubQuery{CustomerID: u.ID}); len(all) != 1 {
		t.Errorf("the switch created a subscription: %d", len(all))
	}
	if h := srv.gsubRelatedOrdersTable(ctx, sub, func(*gooOrder) string { return "#" }); !strings.Contains(h, "Switch Order") {
		t.Errorf("related orders: %s", h)
	}

	// Downgrade, prorated: nothing due now, the credit extends the next payment.
	c2 := gxaCtx(u, http.MethodPost, "/", url.Values(switchForm))
	cart2 := srv.gooCartFor(c2)
	if _, err := cart2.Add(c2, parent.ID, 1, basic.ID, nil, switchForm, nil); err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "downgrade due now", cart2.Total, 0)
	if _, _, errs := srv.gooProcessCheckout(c2, gxaCheckoutForm(u.Email, "goo_test")); len(errs) > 0 {
		t.Fatal(errs)
	}
	sub, _ = srv.gsubGet(ctx, sub.ID)
	if sub.Items[0].VariationID != basic.ID || sub.RecurringTotal != 10 || sub.NextPayment == nil || sub.NextPayment.Sub(next) < 24*time.Hour {
		t.Errorf("after the downgrade: %+v next %v (was %v)", sub.Items, sub.NextPayment, next)
	}

	// Grouped subscriptions, unprorated: nothing now but the full sign-up fee; the price changes at renewal.
	gxaSetExt(t, srv, gsubKey, map[string]any{"apportion_recurring_price": "no", "apportion_sign_up_fee": "full"})
	mk := func(name string, price, fee float64) *gooProduct {
		p := gooNewProduct("subscription")
		p.Name, p.RegularPrice, p.Virtual = name, gooFloatPtr(price), true
		p.Meta = map[string]any{"_subscription_period": "week", "_subscription_period_interval": 1.0, "_subscription_sign_up_fee": fee}
		if err := srv.gooSaveProduct(ctx, p, 1); err != nil {
			t.Fatal(err)
		}
		return p
	}
	silver, gold := mk("Silver", 5, 0), mk("Gold", 8, 3)
	group := gooNewProduct(gooTypeGrouped)
	group.Name, group.Children = "Memberships", []int64{silver.ID, gold.ID}
	if err := srv.gooSaveProduct(ctx, group, 1); err != nil {
		t.Fatal(err)
	}
	u2 := gxaCustomer(t, srv, "grouped@example.test")
	o2 := gxsBuy(t, srv, u2, silver.ID, 0)
	gsubs, _, _ := srv.gsubList(ctx, gsubQuery{ParentOrderID: o2.ID})
	if len(gsubs) != 1 {
		t.Fatalf("%d grouped subscriptions", len(gsubs))
	}
	gsub := gsubs[0]
	gnext := *gsub.NextPayment
	gc := gxaCtx(u2, http.MethodGet, "/", nil)
	if !strings.Contains(srv.gooSubsSwitchLink(gc, gsub, 0), "switch-subscription") {
		t.Fatal("no switch link for a grouped subscription")
	}
	gform := map[string][]string{"switch-subscription": {itoa64(gsub.ID)}, "item": {"0"}, "_wcsnonce": {srv.nonce(gc, "wcs_switch_request")}}
	gpc := gxaCtx(u2, http.MethodPost, "/", url.Values(gform))
	gcart := srv.gooCartFor(gpc)
	if _, err := gcart.Add(gpc, gold.ID, 1, 0, nil, gform, nil); err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "grouped switch due now", gcart.Total, 3)
	if _, _, errs := srv.gooProcessCheckout(gpc, gxaCheckoutForm(u2.Email, "goo_test")); len(errs) > 0 {
		t.Fatal(errs)
	}
	if gsub, _ = srv.gsubGet(ctx, gsub.ID); gsub.Items[0].ProductID != gold.ID || gsub.RecurringTotal != 8 || gsub.NextPayment == nil || gsub.NextPayment.Sub(gnext).Abs() > time.Second {
		t.Errorf("grouped switch: %+v next %v (was %v)", gsub.Items, gsub.NextPayment, gnext)
	}

	// Switching off: no button.
	gxaSetExt(t, srv, gsubKey, map[string]any{"allow_switching": "no"})
	if l := srv.gooSubsSwitchLink(gc, gsub, 0); l != "" {
		t.Errorf("switch link while switching is off: %s", l)
	}
}

func TestGooExtSubscriptionsSync(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxaEnableGateways(t, srv)
	gxaOn(t, srv, gsubKey, true)
	utc := time.UTC

	// The renewal day arithmetic.
	wed := time.Date(2027, 3, 10, 9, 0, 0, 0, utc) // a Wednesday
	if d := gooSubsNextSyncDate(wed, "week", 5, utc); d.Weekday() != time.Friday || d.Day() != 12 {
		t.Errorf("next Friday: %v", d)
	}
	if d := gooSubsNextSyncDate(wed, "week", 3, utc); !d.Equal(wed) {
		t.Errorf("synced to today: %v", d)
	}
	if d := gooSubsNextSyncDate(wed, "month", 1, utc); d.Month() != time.April || d.Day() != 1 {
		t.Errorf("next 1st: %v", d)
	}
	if d := gooSubsNextSyncDate(wed, "month", 15, utc); d.Month() != time.March || d.Day() != 15 {
		t.Errorf("next 15th: %v", d)
	}
	if d := gooSubsNextSyncDate(wed, "year", 301, utc); d.Year() != 2028 || d.Month() != time.March || d.Day() != 1 {
		t.Errorf("next March 1st: %v", d)
	}
	if first, synced := gooSubsFirstRenewal(wed, "month", 1, nil, 10, utc); synced || first.Month() != time.April || first.Day() != 10 {
		t.Errorf("synced to today: first renewal %v synced %v", first, synced)
	}
	if got := gooSubsSyncLabel("month", 1) + gooSubsSyncLabel("week", 3) + gooSubsSyncLabel("year", 301); got != " on the 1st of each month on Wednesday on March 1st each year" {
		t.Errorf("labels %q", got)
	}

	// A product synchronised to a day of the month that is not today.
	gxaSetExt(t, srv, gsubKey, map[string]any{"sync_payments": true, "prorate_synced_payments": "yes"})
	now := time.Now().In(srv.loc())
	day := (now.Day()+10-1)%28 + 1
	box := gooNewProduct("subscription")
	box.Name, box.RegularPrice, box.Virtual = "Magazine", gooFloatPtr(30), true
	if err := srv.gooSaveProduct(ctx, box, 1); err != nil {
		t.Fatal(err)
	}
	panel := gxaPanel(t, "subscription_pricing")
	if h := string(panel.Render(srv, gooTestCtx(), box)); !strings.Contains(h, "Synchronise renewals") || !strings.Contains(h, `name="_subscription_payment_sync_date"`) {
		t.Errorf("panel lacks the sync field: %.300s", h)
	}
	box = gxaSavePanel(t, srv, "subscription_pricing", box, url.Values{"_subscription_price": {"30"}, "_subscription_period_interval": {"1"},
		"_subscription_period": {"month"}, "_subscription_length": {"0"}, "_subscription_payment_sync_date": {strconv.Itoa(day)}, "_subscription_limit": {"no"}})
	if sc := gsubScheduleOf(box); sc.SyncDay != day {
		t.Fatalf("saved sync day %d, want %d", sc.SyncDay, day)
	}
	if plain := gxsPlainPrice(srv, box); !strings.Contains(plain, "/ month on the "+gxaOrdinal(day)+" of each month") {
		t.Errorf("price string %q", plain)
	}
	first := gooSubsNextSyncDate(now, "month", day, srv.loc())
	if h := string(srv.gooHTML(gooTestCtx(), "single_before_add_to_cart", box)); !strings.Contains(h, "First payment: "+srv.gxaDate(&first)) {
		t.Errorf("product page: %s", h)
	}

	// Prorated first payment: the days until the renewal day.
	days := math.Ceil(first.Sub(now).Hours() / 24)
	cycle := first.Sub(gxaAddPeriod(first, "month", -1)).Hours() / 24
	want := gooRound(30*days/cycle, 2)
	u := gxaCustomer(t, srv, "synced@example.test")
	c := gxaCtx(u, http.MethodGet, "/", nil)
	cart := srv.gooCartFor(c)
	if _, err := cart.Add(c, box.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if math.Abs(cart.Total-want) > 1.01 || cart.Total <= 0 || cart.Total >= 30 {
		t.Errorf("prorated first payment %.2f, want about %.2f", cart.Total, want)
	}
	if h := string(srv.gsubRecurringTotals(c, cart)); !strings.Contains(h, "First renewal: "+srv.gxaDate(&first)) {
		t.Errorf("recurring totals: %s", h)
	}
	// The other first-payment rules.
	for _, tc := range []struct {
		mode   string
		noFee  float64
		amount float64
	}{{"no", 0, 0}, {"recurring", 0, 30}, {"yes", 40, 0}} {
		gxaSetExt(t, srv, gsubKey, map[string]any{"prorate_synced_payments": tc.mode, "days_no_fee": tc.noFee})
		cart.Calculate()
		gooAlmost(t, "first payment ("+tc.mode+")", cart.Total, tc.amount)
	}
	gxaSetExt(t, srv, gsubKey, map[string]any{"prorate_synced_payments": "recurring", "days_no_fee": 0.0})
	cart.Calculate()

	// The subscription renews on the synchronised day.
	_, o, errs := srv.gooProcessCheckout(c, gxaCheckoutForm(u.Email, "goo_test"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	subs, _, _ := srv.gsubList(ctx, gsubQuery{ParentOrderID: o.ID})
	if len(subs) != 1 {
		t.Fatalf("%d subscriptions", len(subs))
	}
	sub := subs[0]
	if sub.SyncDay != day || sub.NextPayment == nil || !gooSubsSameDate(*sub.NextPayment, first, srv.loc()) {
		t.Errorf("synced subscription: day %d next %v, want %v", sub.SyncDay, sub.NextPayment, first)
	}
	// A renewal keeps the day.
	past := gxaAddPeriod(*sub.NextPayment, "month", -1)
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE goo_subscriptions SET next_payment = $2 WHERE id = $1`, sub.ID, past)
	srv.gooFire(ctx, "hourly", nil)
	if sub, _ = srv.gsubGet(ctx, sub.ID); sub.NextPayment == nil || sub.NextPayment.In(srv.loc()).Day() != day {
		t.Errorf("after the renewal: %v (day %d)", sub.NextPayment, day)
	}

	// Weekly, synchronised to tomorrow's weekday: the first payment falls on it.
	tomorrow := now.AddDate(0, 0, 1).Weekday()
	wd := int(tomorrow)
	if wd == 0 {
		wd = 7
	}
	weekly := gooNewProduct("subscription")
	weekly.Name, weekly.RegularPrice, weekly.Virtual = "Veg Box", gooFloatPtr(7), true
	weekly.Meta = map[string]any{"_subscription_period": "week", "_subscription_period_interval": 1.0, "_subscription_payment_sync_date": float64(wd)}
	if err := srv.gooSaveProduct(ctx, weekly, 1); err != nil {
		t.Fatal(err)
	}
	if _, fw := srv.gooSubsFirstPayment(weekly, gsubScheduleOf(weekly), now); fw.Weekday() != tomorrow {
		t.Errorf("weekly first payment on %v, want %v", fw.Weekday(), tomorrow)
	}

	// Off: no synchronisation.
	gxaSetExt(t, srv, gsubKey, map[string]any{"sync_payments": false})
	if frac, f := srv.gooSubsFirstPayment(box, gsubScheduleOf(box), now); frac != 1 || f.Sub(gxaAddPeriod(now, "month", 1)).Abs() > time.Minute {
		t.Errorf("unsynced first payment: %v %v", frac, f)
	}
}

// The subscription detail page has no menu tab, but its page still has a title (it was empty).
func TestGooViewSubscriptionTitle(t *testing.T) {
	s := &Server{}
	if got := s.gooEndpointTitle("view-subscription", "42"); got != "Subscription #42" {
		t.Errorf("view-subscription title: %q", got)
	}
}

type gsubFakeCardGateway struct{ gooTestGateway }

func (gsubFakeCardGateway) ID() string              { return "fake_card" }
func (gsubFakeCardGateway) Supports(f string) bool  { return f == "subscriptions" }
func (gsubFakeCardGateway) RenewalNeedsToken() bool { return true }
func (gsubFakeCardGateway) ProcessRenewal(s *Server, ctx context.Context, o *gooOrder, token string) (string, error) {
	return "tx_" + token, nil
}

func TestGsubManualRenewalTokenGateway(t *testing.T) {
	gooRegisterGateway(gsubFakeCardGateway{})
	defer delete(gooGatewayRegistry, "fake_card")
	_, _, s := gooSite(t)
	sub := &gooSubscription{PaymentMethod: "fake_card", Meta: map[string]any{}}
	if !s.gsubManualRenewal(sub) {
		t.Fatal("no token: renewal must stay manual")
	}
	sub.Meta["_payment_token"] = "tok_1"
	if s.gsubManualRenewal(sub) {
		t.Fatal("token + renewal gateway: renewal must be automatic")
	}
	sub.Meta["_requires_manual_renewal"] = true
	if !s.gsubManualRenewal(sub) {
		t.Fatal("manual flag wins")
	}
	if !s.gsubManualRenewal(&gooSubscription{PaymentMethod: "bacs", Meta: map[string]any{"_payment_token": "x"}}) {
		t.Fatal("gateway without renewal support is manual")
	}
}
