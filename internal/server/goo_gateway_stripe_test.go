package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func gooStripeSign(body []byte, secret string, ts time.Time) string {
	t := strconv.FormatInt(ts.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(t + "."))
	mac.Write(body)
	return "t=" + t + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestGooStripeVerifySignature(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	now := time.Now()
	ok := gooStripeSign(body, "whsec_a", now)
	if err := gooStripeVerifySignature(body, ok, "whsec_a", now, gooStripeTolerance); err != nil {
		t.Fatalf("valid signature refused: %v", err)
	}
	// A rotated secret: Stripe sends one v1 per secret.
	other := gooStripeSign(body, "whsec_old", now)
	multi := ok + "," + other[strings.Index(other, "v1="):]
	if err := gooStripeVerifySignature(body, multi, "whsec_old", now, gooStripeTolerance); err != nil {
		t.Errorf("second v1 not accepted: %v", err)
	}
	cases := map[string]struct {
		body   []byte
		header string
		secret string
	}{
		"wrong secret":   {body, ok, "whsec_b"},
		"changed body":   {[]byte(`{"id":"evt_2"}`), ok, "whsec_a"},
		"no secret":      {body, ok, ""},
		"no header":      {body, "", "whsec_a"},
		"no v1":          {body, "t=" + strconv.FormatInt(now.Unix(), 10), "whsec_a"},
		"replayed (old)": {body, gooStripeSign(body, "whsec_a", now.Add(-10*time.Minute)), "whsec_a"},
		"future":         {body, gooStripeSign(body, "whsec_a", now.Add(10*time.Minute)), "whsec_a"},
		"bad timestamp":  {body, "t=abc,v1=00", "whsec_a"},
	}
	for name, tc := range cases {
		if err := gooStripeVerifySignature(tc.body, tc.header, tc.secret, now, gooStripeTolerance); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestGooStripeAmount(t *testing.T) {
	for _, tc := range []struct {
		amt  float64
		cur  string
		want int64
	}{{10.5, "USD", 1050}, {0.1 + 0.2, "eur", 30}, {1500, "JPY", 1500}, {1.234, "KWD", 1230}, {19.99, "TRY", 1999}} {
		if got := gooStripeAmount(tc.amt, tc.cur); got != tc.want {
			t.Errorf("%v %s = %d, want %d", tc.amt, tc.cur, got, tc.want)
		}
		if tc.cur != "KWD" {
			if back := gooStripeFromAmount(float64(tc.want), tc.cur); back != gooRound(tc.amt, 2) && tc.cur != "JPY" {
				t.Errorf("back %v %s = %v", tc.want, tc.cur, back)
			}
		}
	}
}

// gooFakeStripe is an httptest Stripe: it keeps PaymentIntents in memory and records every request.
type gooFakeStripe struct {
	mu       sync.Mutex
	srv      *httptest.Server
	intents  map[string]map[string]any
	sessions map[string]map[string]any
	calls    []gooFakeStripeCall
	n        int
}

type gooFakeStripeCall struct {
	Method, Path, Idem, Auth string
	Form                     url.Values
}

func (f *gooFakeStripe) record(r *http.Request) url.Values {
	_ = r.ParseForm()
	form := r.PostForm
	if r.Method == http.MethodGet {
		form = r.URL.Query()
	}
	f.calls = append(f.calls, gooFakeStripeCall{Method: r.Method, Path: r.URL.Path, Idem: r.Header.Get("Idempotency-Key"), Auth: r.Header.Get("Authorization"), Form: form})
	return form
}

func (f *gooFakeStripe) last(path string) *gooFakeStripeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].Path == path {
			c := f.calls[i]
			return &c
		}
	}
	return nil
}

func (f *gooFakeStripe) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Path == path {
			n++
		}
	}
	return n
}

var gooFakeCard = map[string]any{"id": "pm_card", "object": "payment_method", "customer": "cus_fake",
	"card": map[string]any{"brand": "visa", "last4": "4242", "exp_month": float64(4), "exp_year": float64(2030)}}

func newGooFakeStripe(t *testing.T) *gooFakeStripe {
	f := &gooFakeStripe{intents: map[string]map[string]any{}, sessions: map[string]map[string]any{}}
	idem := map[string]map[string]any{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		form := f.record(r)
		out := func(code int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(v)
		}
		md := func(prefix string) map[string]any {
			m := map[string]any{}
			for k, v := range form {
				if strings.HasPrefix(k, prefix+"[") {
					m[strings.TrimSuffix(strings.TrimPrefix(k, prefix+"["), "]")] = v[0]
				}
			}
			return m
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer sk_") {
			out(401, map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": "Invalid API Key"}})
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if key != "" && idem[key] != nil {
			out(200, idem[key])
			return
		}
		f.n++
		switch p := r.URL.Path; {
		case p == "/v1/customers" && r.Method == http.MethodPost:
			out(200, map[string]any{"id": "cus_fake"})
		case p == "/v1/checkout/sessions" && r.Method == http.MethodPost:
			id := fmt.Sprintf("cs_test_%d", f.n)
			sess := map[string]any{"id": id, "object": "checkout.session", "mode": form.Get("mode"), "url": "https://checkout.stripe.test/" + id,
				"client_reference_id": form.Get("client_reference_id"), "metadata": md("metadata"), "customer": form.Get("customer"), "status": "open"}
			if form.Get("mode") == "payment" {
				pid := fmt.Sprintf("pi_test_%d", f.n)
				amt, _ := strconv.ParseFloat(form.Get("line_items[0][price_data][unit_amount]"), 64)
				f.intents[pid] = map[string]any{"id": pid, "object": "payment_intent", "status": "requires_payment_method", "amount": amt,
					"currency": form.Get("line_items[0][price_data][currency]"), "metadata": md("payment_intent_data[metadata]"),
					"setup_future_usage": form.Get("payment_intent_data[setup_future_usage]"), "customer": form.Get("customer")}
				sess["payment_intent"] = pid
			}
			f.sessions[id] = sess
			out(200, sess)
		case strings.HasPrefix(p, "/v1/checkout/sessions/") && r.Method == http.MethodGet:
			sess := f.sessions[strings.TrimPrefix(p, "/v1/checkout/sessions/")]
			if sess == nil {
				out(404, map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": "No such session"}})
				return
			}
			cp := map[string]any{}
			for k, v := range sess {
				cp[k] = v
			}
			if pid, ok := sess["payment_intent"].(string); ok && strings.Contains(r.URL.RawQuery, "payment_intent") {
				pi := map[string]any{}
				for k, v := range f.intents[pid] {
					pi[k] = v
				}
				if pi["payment_method"] == "pm_card" {
					pi["payment_method"] = gooFakeCard
				}
				cp["payment_intent"] = pi
			}
			if cp["mode"] == "setup" {
				cp["status"] = "complete"
				cp["setup_intent"] = map[string]any{"id": "seti_1", "status": "succeeded", "payment_method": map[string]any{"id": "pm_new", "card": map[string]any{"brand": "mastercard", "last4": "4444", "exp_month": float64(1), "exp_year": float64(2031)}}}
			}
			out(200, cp)
		case strings.HasPrefix(p, "/v1/payment_intents/") && r.Method == http.MethodGet:
			pi := f.intents[strings.TrimPrefix(p, "/v1/payment_intents/")]
			if pi == nil {
				out(404, map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": "No such payment_intent"}})
				return
			}
			out(200, pi)
		case p == "/v1/payment_intents" && r.Method == http.MethodPost:
			pid := fmt.Sprintf("pi_test_%d", f.n)
			amt, _ := strconv.ParseFloat(form.Get("amount"), 64)
			pi := map[string]any{"id": pid, "object": "payment_intent", "amount": amt, "amount_received": amt, "currency": form.Get("currency"),
				"customer": form.Get("customer"), "payment_method": form.Get("payment_method"), "metadata": md("metadata"), "status": "succeeded"}
			switch form.Get("payment_method") {
			case "pm_decline":
				out(402, map[string]any{"error": map[string]any{"type": "card_error", "code": "card_declined", "message": "Your card was declined."}})
				return
			case "pm_slow":
				pi["status"], pi["amount_received"] = "processing", float64(0)
			}
			f.intents[pid] = pi
			if key != "" {
				idem[key] = pi
			}
			out(200, pi)
		case p == "/v1/refunds" && r.Method == http.MethodPost:
			res := map[string]any{"id": fmt.Sprintf("re_%d", f.n), "status": "succeeded", "amount": form.Get("amount")}
			if key != "" {
				idem[key] = res
			}
			out(200, res)
		case strings.HasPrefix(p, "/v1/payment_methods/") && strings.HasSuffix(p, "/detach"):
			out(200, map[string]any{"id": strings.Split(p, "/")[3]})
		default:
			out(404, map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": "unknown route " + p}})
		}
	}))
	old := gooStripeAPIBase
	gooStripeAPIBase = f.srv.URL
	t.Cleanup(func() { gooStripeAPIBase = old; f.srv.Close() })
	return f
}

// succeed marks an intent paid with the test card, as the customer would on Stripe's page.
func (f *gooFakeStripe) succeed(pid string, saveCard bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pi := f.intents[pid]
	pi["status"], pi["amount_received"] = "succeeded", pi["amount"]
	if saveCard {
		pi["payment_method"] = "pm_card"
	}
}

func gooStripeEnable(t *testing.T, srv *Server) {
	t.Helper()
	if err := srv.gooSetOpts(context.Background(), map[string]any{"goo_stripe_settings": map[string]any{
		"enabled": "yes", "testmode": "yes", "test_secret_key": "sk_test_fake", "test_webhook_secret": "whsec_fake"}}); err != nil {
		t.Fatal(err)
	}
}

func TestGooStripeGateway(t *testing.T) {
	base, admin, srv := gooSite(t)
	ctx := context.Background()
	fake := newGooFakeStripe(t)
	gooStripeEnable(t, srv)
	guest := gooGuestClient()

	// The settings screen never prints a saved secret back.
	if _, page, _ := get(t, admin, base+"/wp-admin/goocommerce?page=settings&tab=checkout&section=goo_stripe"); strings.Contains(page, "sk_test_fake") || strings.Contains(page, "whsec_fake") {
		t.Error("the Stripe settings page shows a saved secret")
	} else if !strings.Contains(page, "/wc-api/goo_stripe/") {
		t.Error("the settings page does not name the webhook endpoint")
	}

	checkout := func(name string, price float64) (*gooOrder, string) {
		t.Helper()
		p := gooMakeSimple(t, srv, name, price, func(p *gooProduct) { p.Virtual = true })
		c := gooTestCtx()
		if _, err := srv.gooCartFor(c).Add(ctx, p.ID, 1, 0, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		form := map[string][]string{}
		for k, v := range gxCheckoutForm {
			form[k] = v
		}
		form["payment_method"] = []string{gooStripeID}
		res, o, errs := srv.gooProcessCheckout(c, form)
		if len(errs) > 0 || o == nil || res == nil || res.Result != "success" {
			t.Fatalf("stripe checkout: %v %+v", errs, res)
		}
		o, _ = srv.gooGetOrder(ctx, o.ID)
		return o, res.Redirect
	}

	// Checkout: a hosted Checkout Session for the order total; the order waits for payment.
	o, redirect := checkout("Stripe Lamp", 12.5)
	if !strings.HasPrefix(redirect, "https://checkout.stripe.test/") || o.Status != "pending" {
		t.Fatalf("checkout redirect %q, status %s", redirect, o.Status)
	}
	sc := fake.last("/v1/checkout/sessions")
	if sc.Form.Get("line_items[0][price_data][unit_amount]") != "1250" || sc.Form.Get("line_items[0][price_data][currency]") != "usd" ||
		sc.Form.Get("payment_intent_data[metadata][order_key]") != o.OrderKey || sc.Auth != "Bearer sk_test_fake" {
		t.Fatalf("session form: %v", sc.Form)
	}
	sid := gxaStr(o.Meta, "_stripe_session_id")
	pid := gxaStr(fake.sessions[sid], "payment_intent")

	// The customer comes back before paying (or with a forged return): nothing is paid.
	retURL := func(o *gooOrder, key, sid string) string {
		return base + "/wc-api/goo_stripe/?order_id=" + itoa64(o.ID) + "&key=" + url.QueryEscape(key) + "&session_id=" + sid
	}
	if code, _, loc := get(t, guest, retURL(o, o.OrderKey, sid)); code != http.StatusFound || strings.Contains(loc, "order-received") {
		t.Fatalf("unpaid return: %d %s", code, loc)
	}
	fake.succeed(pid, false)
	if _, _, loc := get(t, guest, retURL(o, "wc_order_forged", sid)); strings.Contains(loc, "order-received") {
		t.Error("a wrong order key completed the order")
	}
	if got, _ := srv.gooGetOrder(ctx, o.ID); got.IsPaid() {
		t.Fatal("order paid through a forged return")
	}
	// Paid on Stripe: the return completes the order.
	if code, _, loc := get(t, guest, retURL(o, o.OrderKey, sid)); code != http.StatusFound || !strings.Contains(loc, "order-received") {
		t.Fatalf("paid return: %d %s", code, loc)
	}
	o, _ = srv.gooGetOrder(ctx, o.ID)
	if !o.IsPaid() || o.TransactionID != pid {
		t.Fatalf("after return: status %s txn %q", o.Status, o.TransactionID)
	}

	// Webhooks: signed, once per event id, refused when unsigned or stale.
	hook := func(evID, typ string, obj map[string]any, sign func([]byte) string) (int, string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"id": evID, "type": typ, "data": map[string]any{"object": obj}})
		req, _ := http.NewRequest(http.MethodPost, base+"/wc-api/goo_stripe/", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		if sign != nil {
			req.Header.Set("Stripe-Signature", sign(body))
		}
		resp, err := guest.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	good := func(b []byte) string { return gooStripeSign(b, "whsec_fake", time.Now()) }
	o2, _ := checkout("Stripe Chair", 40)
	pid2 := gxaStr(fake.sessions[gxaStr(o2.Meta, "_stripe_session_id")], "payment_intent")
	fake.succeed(pid2, false)
	piObj := func() map[string]any {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		cp := map[string]any{}
		for k, v := range fake.intents[pid2] {
			cp[k] = v
		}
		return cp
	}
	if code, _ := hook("evt_unsigned", "payment_intent.succeeded", piObj(), nil); code != http.StatusBadRequest {
		t.Errorf("unsigned webhook: %d", code)
	}
	if code, _ := hook("evt_wrong", "payment_intent.succeeded", piObj(), func(b []byte) string { return gooStripeSign(b, "whsec_other", time.Now()) }); code != http.StatusBadRequest {
		t.Errorf("wrongly signed webhook: %d", code)
	}
	if code, _ := hook("evt_old", "payment_intent.succeeded", piObj(), func(b []byte) string { return gooStripeSign(b, "whsec_fake", time.Now().Add(-time.Hour)) }); code != http.StatusBadRequest {
		t.Errorf("replayed webhook: %d", code)
	}
	if got, _ := srv.gooGetOrder(ctx, o2.ID); got.IsPaid() {
		t.Fatal("a refused webhook paid the order")
	}
	// A forged order key inside a correctly signed event (another site on the account) is ignored.
	forged := piObj()
	forged["metadata"] = map[string]any{"order_id": itoa64(o2.ID), "order_key": "wc_order_other"}
	if code, _ := hook("evt_other_site", "payment_intent.succeeded", forged, good); code != http.StatusOK {
		t.Errorf("foreign event: %d", code)
	}
	if got, _ := srv.gooGetOrder(ctx, o2.ID); got.IsPaid() {
		t.Fatal("an event for another order key paid the order")
	}
	if code, body := hook("evt_paid", "payment_intent.succeeded", piObj(), good); code != http.StatusOK || strings.Contains(body, "duplicate") {
		t.Fatalf("signed webhook: %d %s", code, body)
	}
	o2, _ = srv.gooGetOrder(ctx, o2.ID)
	if !o2.IsPaid() || o2.TransactionID != pid2 {
		t.Fatalf("webhook did not pay the order: %s %q", o2.Status, o2.TransactionID)
	}
	if _, body := hook("evt_paid", "payment_intent.succeeded", piObj(), good); !strings.Contains(body, "duplicate") {
		t.Errorf("a redelivered event was handled again: %s", body)
	}
	// Unknown gateway on the callback route: WooCommerce's -1.
	if code, body, _ := get(t, guest, base+"/wc-api/nope/"); code != http.StatusBadRequest || body != "-1" {
		t.Errorf("unknown wc-api: %d %q", code, body)
	}

	// A payment that does not match the total is held for review, not accepted.
	o3, _ := checkout("Stripe Desk", 99)
	pid3 := gxaStr(fake.sessions[gxaStr(o3.Meta, "_stripe_session_id")], "payment_intent")
	fake.mu.Lock()
	fake.intents[pid3]["status"], fake.intents[pid3]["amount_received"] = "succeeded", float64(100)
	fake.mu.Unlock()
	get(t, guest, retURL(o3, o3.OrderKey, gxaStr(o3.Meta, "_stripe_session_id")))
	if got, _ := srv.gooGetOrder(ctx, o3.ID); got.Status != "on-hold" {
		t.Errorf("underpaid order: %s", got.Status)
	}

	// Admin refund: through /v1/refunds with an idempotency key, recorded once even when Stripe's
	// charge.refunded event follows.
	if _, err := srv.gooCreateRefund(ctx, o2, 15, "Damaged", nil, false, true, 1); err != nil {
		t.Fatalf("api refund: %v", err)
	}
	rc := fake.last("/v1/refunds")
	if rc == nil || rc.Form.Get("amount") != "1500" || rc.Form.Get("payment_intent") != pid2 || rc.Idem == "" {
		t.Fatalf("refund call: %+v", rc)
	}
	refundObj := map[string]any{"id": "ch_1", "object": "charge", "payment_intent": pid2, "amount_refunded": float64(1500), "metadata": map[string]any{}}
	if code, _ := hook("evt_refund_1", "charge.refunded", refundObj, good); code != http.StatusOK {
		t.Fatalf("charge.refunded: %d", code)
	}
	if got := srv.gooTotalRefunded(ctx, o2.ID); got != 15 {
		t.Fatalf("refunded after own refund event: %v", got)
	}
	// A further refund made in the Stripe Dashboard is recorded.
	refundObj["amount_refunded"] = float64(2500)
	hook("evt_refund_2", "charge.refunded", refundObj, good)
	hook("evt_refund_3", "charge.refunded", refundObj, good) // a second event with the same total adds nothing
	if got := srv.gooTotalRefunded(ctx, o2.ID); got != 25 {
		t.Fatalf("refunded after dashboard refund: %v", got)
	}
	// A provider error removes the refund record again.
	gooStripeAPIBaseSave := gooStripeAPIBase
	gooStripeAPIBase = "http://127.0.0.1:1"
	if _, err := srv.gooCreateRefund(ctx, o2, 5, "", nil, false, true, 1); err == nil {
		t.Error("a refund with Stripe unreachable succeeded")
	}
	gooStripeAPIBase = gooStripeAPIBaseSave
	if got := srv.gooTotalRefunded(ctx, o2.ID); got != 25 {
		t.Errorf("failed refund left a record: %v", got)
	}
}

// Saved cards and off-session renewals.
func TestGooStripeTokensAndRenewals(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	fake := newGooFakeStripe(t)
	gooStripeEnable(t, srv)
	gxaOn(t, srv, gsubKey, true)
	u := gxaCustomer(t, srv, "stripe-sub@example.test")
	if err := srv.gooSetUserMeta(ctx, u.ID, map[string]any{"_goo_stripe_customer_id_test": "cus_fake"}); err != nil {
		t.Fatal(err)
	}

	// The first payment of a subscription keeps the card: token on the order, the subscription and the account.
	o := &gooOrder{Status: "pending", Type: "shop_order", Currency: "USD", CustomerID: u.ID, PaymentMethod: gooStripeID, Total: 10,
		Billing: map[string]string{"email": u.Email}, Shipping: map[string]string{}, Meta: map[string]any{},
		Items: []*gooOrderItem{{Type: "line_item", Name: "Box", Quantity: 1, Total: 10, Subtotal: 10, Meta: map[string]any{
			"_subscription_period": "month", "_subscription_interval": float64(1), "_subscription_recurring_amount": float64(10), "_subscription_recurring_line_total": float64(10)}}}}
	if err := srv.gooSaveOrder(ctx, o, true); err != nil {
		t.Fatal(err)
	}
	pi := map[string]any{"id": "pi_first", "status": "succeeded", "amount": float64(1000), "amount_received": float64(1000), "currency": "usd",
		"setup_future_usage": "off_session", "customer": "cus_fake", "payment_method": gooFakeCard,
		"metadata": map[string]any{"order_id": itoa64(o.ID), "order_key": o.OrderKey}}
	if err := srv.gooStripeApplyIntent(ctx, o, pi); err != nil {
		t.Fatal(err)
	}
	o, _ = srv.gooGetOrder(ctx, o.ID)
	if !o.IsPaid() || gxaStr(o.Meta, "_payment_token") != "pm_card" {
		t.Fatalf("first payment: %s token %v", o.Status, o.Meta["_payment_token"])
	}
	toks := srv.gooUserTokens(ctx, u.ID, gooStripeID)
	if len(toks) != 1 || toks[0].Last4 != "4242" || !toks[0].Default || toks[0].Label() != "Visa ending in 4242 (expires 04/30)" {
		t.Fatalf("saved tokens: %+v", toks)
	}
	subs, _, _ := srv.gsubList(ctx, gsubQuery{ParentOrderID: o.ID})
	if len(subs) != 1 || gxaStr(subs[0].Meta, "_payment_token") != "pm_card" || srv.gsubManualRenewal(subs[0]) {
		t.Fatalf("subscription does not renew with the card: %+v", subs)
	}
	sub := subs[0]

	// Scheduled renewal: charged off-session with the saved card.
	past := time.Now().Add(-time.Hour)
	sub.NextPayment = &past
	if err := srv.gsubSave(ctx, sub); err != nil {
		t.Fatal(err)
	}
	srv.gsubScheduler(ctx)
	sub, _ = srv.gsubGet(ctx, sub.ID)
	ro, err := srv.gooGetOrder(ctx, sub.LastOrderID)
	if err != nil || ro.ID == o.ID || !ro.IsPaid() {
		t.Fatalf("renewal order not paid: %v %v", ro, err)
	}
	call := fake.last("/v1/payment_intents")
	if call.Form.Get("off_session") != "true" || call.Form.Get("payment_method") != "pm_card" || call.Form.Get("customer") != "cus_fake" ||
		call.Form.Get("amount") != "1000" || call.Idem != fmt.Sprintf("goo-renewal-%d-0", ro.ID) {
		t.Fatalf("renewal charge: %+v", call)
	}
	// A retry of the same attempt (lost response) reuses the key, so Stripe charges once.
	n := fake.count("/v1/payment_intents")
	ro2 := &gooOrder{ID: ro.ID, Total: ro.Total, Currency: "USD", OrderKey: ro.OrderKey, Meta: map[string]any{"_stripe_customer_id": "cus_fake"}}
	if _, err := (gooStripeGateway{}).ProcessRenewal(srv, ctx, ro2, "pm_card"); err != nil {
		t.Fatal(err)
	}
	if fake.last("/v1/payment_intents").Idem != call.Idem || fake.count("/v1/payment_intents") != n+1 {
		t.Error("retried renewal did not reuse the idempotency key")
	}

	// Declined card: the renewal fails and the next attempt uses a new key.
	dec := &gooOrder{Status: "pending", Type: "shop_order", Currency: "USD", CustomerID: u.ID, PaymentMethod: gooStripeID, Total: 10,
		Billing: map[string]string{}, Shipping: map[string]string{}, Meta: map[string]any{"_stripe_customer_id": "cus_fake"}}
	if err := srv.gooSaveOrder(ctx, dec, true); err != nil {
		t.Fatal(err)
	}
	_, err = (gooStripeGateway{}).ProcessRenewal(srv, ctx, dec, "pm_decline")
	var se *gooStripeError
	if !errors.As(err, &se) || se.Code != "card_declined" {
		t.Fatalf("declined renewal: %v", err)
	}
	if gxaFloat(dec.Meta, "_stripe_renewal_attempts") != 1 {
		t.Error("a decline did not count as an attempt")
	}
	// A slow payment method: the order waits on hold for Stripe's webhook.
	slow := &gooOrder{Status: "pending", Type: "shop_order", Currency: "USD", CustomerID: u.ID, PaymentMethod: gooStripeID, Total: 10,
		Billing: map[string]string{}, Shipping: map[string]string{}, Meta: map[string]any{"_stripe_customer_id": "cus_fake"}}
	if err := srv.gooSaveOrder(ctx, slow, true); err != nil {
		t.Fatal(err)
	}
	if txn, err := (gooStripeGateway{}).ProcessRenewal(srv, ctx, slow, "pm_slow"); !errors.Is(err, errGooPaymentPending) || txn == "" {
		t.Errorf("processing renewal: %q %v", txn, err)
	}

	// The scheduler with a declined card: the order fails, the subscription goes on hold.
	sub.Meta["_payment_token"] = "pm_decline"
	sub.NextPayment = &past
	delete(sub.Meta, "_pending_renewal")
	if err := srv.gsubSave(ctx, sub); err != nil {
		t.Fatal(err)
	}
	srv.gsubScheduler(ctx)
	sub, _ = srv.gsubGet(ctx, sub.ID)
	if fo, _ := srv.gooGetOrder(ctx, sub.LastOrderID); fo == nil || fo.Status != "failed" || sub.Status != "on-hold" {
		t.Fatalf("declined scheduled renewal: order %v, subscription %s", fo, sub.Status)
	}

	// Deleting a saved card detaches it at Stripe and turns its subscriptions to manual renewals.
	sub.Meta["_payment_token"] = "pm_card"
	_ = srv.gsubSave(ctx, sub)
	tok := srv.gooUserTokens(ctx, u.ID, gooStripeID)[0]
	if err := (gooStripeGateway{}).DeleteToken(srv, ctx, tok); err != nil {
		t.Fatal(err)
	}
	if fake.last("/v1/payment_methods/pm_card/detach") == nil {
		t.Error("the card was not detached at Stripe")
	}

	// A setup session (Payment methods › Add, or a subscription's Change payment) saves the new card.
	sess := map[string]any{"id": "cs_setup", "mode": "setup", "status": "complete", "customer": "cus_fake",
		"metadata":     map[string]any{"user_id": itoa64(u.ID), "site": srv.links(ctx).HomeURL("/"), "subscription_id": itoa64(sub.ID)},
		"setup_intent": map[string]any{"id": "seti_1", "status": "succeeded", "payment_method": map[string]any{"id": "pm_new", "card": map[string]any{"brand": "mastercard", "last4": "4444", "exp_month": float64(1), "exp_year": float64(2031)}}}}
	if id, err := srv.gooStripeApplySetup(ctx, sess, u.ID+1); err != nil || id != 0 {
		t.Errorf("another user's setup session was applied: %d %v", id, err)
	}
	if id, err := srv.gooStripeApplySetup(ctx, sess, u.ID); err != nil || id != sub.ID {
		t.Fatalf("setup session: %d %v", id, err)
	}
	sub, _ = srv.gsubGet(ctx, sub.ID)
	if gxaStr(sub.Meta, "_payment_token") != "pm_new" {
		t.Errorf("subscription token after change: %v", sub.Meta["_payment_token"])
	}
	if len(srv.gooUserTokens(ctx, u.ID, gooStripeID)) != 2 {
		t.Error("the new card was not saved to the account")
	}
	// Test-mode tokens are hidden in live mode.
	_ = srv.gooSetOpts(ctx, map[string]any{"goo_stripe_settings": map[string]any{"enabled": "yes", "testmode": "no", "secret_key": "sk_live_fake"}})
	if len(srv.gooUserTokens(ctx, u.ID, gooStripeID)) != 0 {
		t.Error("test-mode cards are offered in live mode")
	}
}
