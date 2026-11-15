package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A Stripe event is acknowledged when it names no order of this site, but not when the order could
// not be looked up: then the webhook answers 500 and forgets the event, so Stripe sends it again
// instead of a Dashboard refund or dispute being lost.
func TestGooStripeEventLookupErrors(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	gooStripeEnable(t, srv)

	missing := func() map[string]any {
		return map[string]any{"id": "ch_missing", "object": "charge", "payment_intent": "pi_nobody", "amount_refunded": float64(500),
			"metadata": map[string]any{"order_id": "987654321"}, "reason": "fraudulent"}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for _, typ := range []string{"charge.refunded", "charge.dispute.created", "payment_intent.succeeded", "checkout.session.completed"} {
		// No such order (store.ErrNotFound / pgx.ErrNoRows): nothing to do, the event is acknowledged.
		if err := srv.gooStripeEvent(ctx, typ, missing()); err != nil {
			t.Errorf("%s for a missing order: %v", typ, err)
		}
		// The lookup itself failed: the error reaches the webhook handler.
		if err := srv.gooStripeEvent(cancelled, typ, missing()); err == nil {
			t.Errorf("%s with the database unreachable was acknowledged", typ)
		}
	}
	// gooStripeEventOrder: not found and database errors are told apart.
	if o, err := srv.gooStripeEventOrder(ctx, missing()); o != nil || err != nil {
		t.Errorf("missing order: %v %v", o, err)
	}
	if _, err := srv.gooStripeEventOrder(cancelled, missing()); err == nil {
		t.Error("gooStripeEventOrder hid a database error")
	}

	// Through the webhook: with the orders table unreadable the event is answered 500 and its claim
	// released, so the redelivery is handled (not answered as a duplicate).
	hook := func(evID string) (int, string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"id": evID, "type": "charge.refunded", "data": map[string]any{"object": missing()}})
		req, _ := http.NewRequest(http.MethodPost, base+"/wc-api/goo_stripe/", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Stripe-Signature", gooStripeSign(body, "whsec_fake", time.Now()))
		resp, err := gooGuestClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	claimed := func(evID string) bool {
		var n int
		_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_gateway_events WHERE gateway = $1 AND event_id = $2`, gooStripeID, evID).Scan(&n)
		return n == 1
	}
	if _, err := srv.store.Pool.Exec(ctx, `ALTER TABLE goo_orders RENAME TO goo_orders_unreadable`); err != nil {
		t.Fatal(err)
	}
	code, _ := hook("evt_db_down")
	if _, err := srv.store.Pool.Exec(ctx, `ALTER TABLE goo_orders_unreadable RENAME TO goo_orders`); err != nil {
		t.Fatal(err)
	}
	if code != http.StatusInternalServerError {
		t.Fatalf("webhook with the order lookup failing: %d, want 500", code)
	}
	if claimed("evt_db_down") {
		t.Fatal("the failed event's claim was kept, so Stripe's retry would be ignored")
	}
	if code, body := hook("evt_db_down"); code != http.StatusOK || strings.Contains(body, "duplicate") {
		t.Fatalf("redelivery after the failure: %d %s", code, body)
	}
}
