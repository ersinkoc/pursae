package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// S6: order keys are compared in constant time, and an empty key never matches (not even an order
// without a key).
func TestGooOrderKeyMatches(t *testing.T) {
	for _, tc := range []struct {
		stored, given string
		want          bool
	}{
		{"wc_order_abc123", "wc_order_abc123", true},
		{"wc_order_abc123", "wc_order_abc124", false},
		{"wc_order_abc123", "wc_order_abc12", false},
		{"wc_order_abc123", "", false},
		{"", "", false},
		{"", "wc_order_abc123", false},
	} {
		if got := gooOrderKeyMatches(tc.stored, tc.given); got != tc.want {
			t.Errorf("gooOrderKeyMatches(%q, %q) = %v", tc.stored, tc.given, got)
		}
	}

	// The pay page and the order received page still take the right key, and only that one.
	_, _, srv := gooSite(t)
	ctx := context.Background()
	o := &gooOrder{Status: "pending", Type: "shop_order", Currency: "USD", Total: 10, Billing: map[string]string{"email": "guest@example.test", "first_name": "Keyholder"},
		Shipping: map[string]string{}, Meta: map[string]any{}, Items: []*gooOrderItem{{Type: "line_item", Name: "Box", Quantity: 1, Total: 10, Subtotal: 10, Meta: map[string]any{}}}}
	if err := srv.gooSaveOrder(ctx, o, true); err != nil {
		t.Fatal(err)
	}
	if o.OrderKey == "" {
		t.Fatal("saved order has no key")
	}
	for _, key := range []string{"", o.OrderKey + "x", strings.ToUpper(o.OrderKey)} {
		if got, _ := srv.gooPayableOrder(gooTestCtx(), o.ID, key); got != nil {
			t.Errorf("pay page opened with key %q", key)
		}
		c := gooTestCtx()
		c.Request = httptest.NewRequest(http.MethodGet, "/checkout/order-received/?key="+key, nil)
		if strings.Contains(srv.gooThankYouHTML(c, o.ID), "Order number:") {
			t.Errorf("order received page showed the order with key %q", key)
		}
	}
	if got, msg := srv.gooPayableOrder(gooTestCtx(), o.ID, o.OrderKey); got == nil {
		t.Errorf("pay page refused the order's key: %s", msg)
	}
	c := gooTestCtx()
	c.Request = httptest.NewRequest(http.MethodGet, "/checkout/order-received/?key="+o.OrderKey, nil)
	if !strings.Contains(srv.gooThankYouHTML(c, o.ID), "Order number:") {
		t.Error("order received page hid the order from its key")
	}
}
