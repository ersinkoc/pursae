package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// Concurrency of order status changes, stock and coupon usage: each test races real goroutines on
// separate pool connections against one database and checks the invariants that a lost update breaks.

// gooRaceOrder saves a pending checkout order with qty of each product and the coupon codes.
func gooRaceOrder(t *testing.T, srv *Server, email string, customer int64, lines map[*gooProduct]int, coupons ...string) *gooOrder {
	t.Helper()
	o := &gooOrder{Status: "pending", CreatedVia: "checkout", CustomerID: customer, Billing: map[string]string{"email": email, "first_name": "Race"},
		Shipping: map[string]string{}, Meta: map[string]any{}, PaymentMethod: "goo_test", Total: 10}
	for p, qty := range lines {
		o.Items = append(o.Items, &gooOrderItem{Type: "line_item", Name: p.Name, ProductID: p.ID, Quantity: float64(qty), Subtotal: 10, Total: 10, Meta: map[string]any{}})
	}
	for _, code := range coupons {
		o.Items = append(o.Items, &gooOrderItem{Type: "coupon", Name: code, Meta: map[string]any{}})
	}
	if err := srv.gooSaveOrder(context.Background(), o, true); err != nil {
		t.Fatal(err)
	}
	return o
}

// gooRace runs fns at the same moment, each on its own goroutine, and waits for all of them.
func gooRace(fns ...func()) {
	var start, done sync.WaitGroup
	start.Add(1)
	for _, fn := range fns {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			fn()
		}()
	}
	start.Done()
	done.Wait()
}

func gooStockOf(t *testing.T, srv *Server, id int64) (int, string) {
	t.Helper()
	var qty int
	var status string
	if err := srv.store.Pool.QueryRow(context.Background(), `SELECT stock_quantity, stock_status FROM goo_products WHERE post_id = $1`, id).Scan(&qty, &status); err != nil {
		t.Fatal(err)
	}
	return qty, status
}

// The hourly job cancelling an unpaid order and the payment arriving for it must not both win: a
// paid order is never left cancelled, and stock is reduced once per paid order.
func TestGooOrderStatusRacePaymentVersusUnpaidJob(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSetOpts(ctx, map[string]any{"hold_stock_minutes": 1}); err != nil {
		t.Fatal(err)
	}
	p := gooMakeSimple(t, srv, "Racer", 10, func(p *gooProduct) { p.ManageStock, p.StockQuantity = true, gooIntPtr(100) })

	// Deterministic: a copy loaded before the payment (what the job holds) cannot cancel the paid order.
	o := gooRaceOrder(t, srv, "a@example.test", 0, map[*gooProduct]int{p: 1})
	stale, _ := srv.gooGetOrder(ctx, o.ID)
	stale2, _ := srv.gooGetOrder(ctx, o.ID) // an edit screen opened before the payment
	fresh, _ := srv.gooGetOrder(ctx, o.ID)
	if err := srv.gooPaymentComplete(ctx, fresh, "tx-1"); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSetStatusFrom(ctx, stale, []string{"pending"}, "cancelled", "Unpaid order cancelled - time limit reached.", ""); !errors.Is(err, errGooStatusChanged) {
		t.Errorf("cancelling a paid order from a stale copy: %v", err)
	}
	// A stale full save (admin screen, API) keeps the status and the bookkeeping made meanwhile.
	stale, _ = srv.gooGetOrder(ctx, o.ID)
	_ = srv.gooSetStatus(ctx, stale, "completed", "", "admin")
	stale2.CustomerNote = "edited"
	if err := srv.gooSaveOrder(ctx, stale2, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := srv.gooGetOrder(ctx, o.ID); got.Status != "completed" || !got.StockReduced || got.CustomerNote != "edited" ||
		got.Items[0].Meta["_reduced_stock"] != float64(1) || got.Meta["_recorded_sales"] != true {
		t.Errorf("stale save undid the status change: %s reduced=%v note=%q item=%v meta=%v", got.Status, got.StockReduced, got.CustomerNote, got.Items[0].Meta, got.Meta)
	}

	// Concurrent: the job and the payments race on 12 expired orders.
	var ids []int64
	for range 12 {
		o := gooRaceOrder(t, srv, "b@example.test", 0, map[*gooProduct]int{p: 1})
		ids = append(ids, o.ID)
	}
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE goo_orders SET date_modified = now() - interval '2 hours' WHERE id = ANY($1)`, ids); err != nil {
		t.Fatal(err)
	}
	before, _ := gooStockOf(t, srv, p.ID)
	fns := []func(){func() { _ = srv.gooHourly(ctx) }}
	for _, id := range ids {
		fns = append(fns, func() {
			o, err := srv.gooGetOrder(ctx, id)
			if err == nil {
				err = srv.gooPaymentComplete(ctx, o, "tx")
			}
			if err != nil {
				t.Error(err)
			}
		})
	}
	gooRace(fns...)
	for _, id := range ids {
		o, _ := srv.gooGetOrder(ctx, id)
		if o.Status != "processing" || !o.StockReduced || o.DatePaid == nil {
			t.Errorf("order %d after payment and the unpaid job: %s reduced=%v", id, o.Status, o.StockReduced)
		}
	}
	if after, _ := gooStockOf(t, srv, p.ID); after != before-len(ids) {
		t.Errorf("stock %d → %d for %d paid orders", before, after, len(ids))
	}
}

// Orders paid at the same time cannot sell more than is in stock: the ones that miss the stock go
// on hold with a note, the stock ends at zero and out of stock.
func TestGooStockRaceNoOverselling(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Last units", 10, func(p *gooProduct) { p.ManageStock, p.StockQuantity = true, gooIntPtr(3) })
	other := gooMakeSimple(t, srv, "Plenty", 10, func(p *gooProduct) { p.ManageStock, p.StockQuantity = true, gooIntPtr(100) })
	var orders []*gooOrder
	for range 8 {
		orders = append(orders, gooRaceOrder(t, srv, "c@example.test", 0, map[*gooProduct]int{other: 1, p: 1}))
	}
	var fns []func()
	for _, o := range orders {
		fns = append(fns, func() {
			if err := srv.gooPaymentComplete(ctx, o, "tx"); err != nil {
				t.Error(err)
			}
		})
	}
	gooRace(fns...)
	paid, held := 0, 0
	for _, o := range orders {
		o, _ := srv.gooGetOrder(ctx, o.ID)
		switch o.Status {
		case "processing":
			paid++
			if !o.StockReduced {
				t.Errorf("paid order %d without its stock reduced", o.ID)
			}
		case "on-hold":
			held++
			if o.StockReduced {
				t.Errorf("held order %d has its stock reduced", o.ID)
			}
			notes, _ := srv.gooOrderNotes(ctx, o.ID, false)
			if len(notes) == 0 || !strings.Contains(notes[0].Content, "Not enough units of Last units") {
				t.Errorf("held order %d note: %+v", o.ID, notes)
			}
		default:
			t.Errorf("order %d: %s", o.ID, o.Status)
		}
	}
	firstBatch := orders
	if paid != 3 || held != 5 {
		t.Errorf("paid %d, on hold %d; want 3 and 5", paid, held)
	}
	if qty, status := gooStockOf(t, srv, p.ID); qty != 0 || status != "outofstock" {
		t.Errorf("last units: %d %s", qty, status)
	}
	// The other line of a held order was put back: only the 3 paid orders took one each.
	if qty, status := gooStockOf(t, srv, other.ID); qty != 97 || status != "instock" {
		t.Errorf("plenty: %d %s", qty, status)
	}

	// With backorders allowed every order is paid and the stock goes below zero.
	bo := gooMakeSimple(t, srv, "Backorder", 10, func(p *gooProduct) { p.ManageStock, p.StockQuantity, p.Backorders = true, gooIntPtr(1), "notify" })
	orders = nil
	for range 3 {
		orders = append(orders, gooRaceOrder(t, srv, "d@example.test", 0, map[*gooProduct]int{bo: 1}))
	}
	fns = nil
	for _, o := range orders {
		fns = append(fns, func() { _ = srv.gooPaymentComplete(ctx, o, "tx") })
	}
	gooRace(fns...)
	for _, o := range orders {
		if o, _ := srv.gooGetOrder(ctx, o.ID); o.Status != "processing" {
			t.Errorf("backorder order %d: %s", o.ID, o.Status)
		}
	}
	if qty, status := gooStockOf(t, srv, bo.ID); qty != -2 || status != "onbackorder" {
		t.Errorf("backorder product: %d %s", qty, status)
	}

	// An order paid later, once the stock is back, is reduced on its way to processing.
	if _, err := srv.gooUpdateStock(ctx, p, 10, true); err != nil {
		t.Fatal(err)
	}
	var heldOrder *gooOrder
	for _, o := range firstBatch {
		if o, _ := srv.gooGetOrder(ctx, o.ID); o.Status == "on-hold" {
			heldOrder = o
		}
	}
	if heldOrder == nil {
		t.Fatal("no held order")
	}
	if err := srv.gooPaymentComplete(ctx, heldOrder, ""); err != nil {
		t.Fatal(err)
	}
	if o, _ := srv.gooGetOrder(ctx, heldOrder.ID); o.Status != "processing" || !o.StockReduced {
		t.Errorf("held order after restocking: %s reduced=%v", o.Status, o.StockReduced)
	}
	if qty, _ := gooStockOf(t, srv, p.ID); qty != 9 {
		t.Errorf("stock after the held order: %d", qty)
	}
}

// Orders racing for a coupon's last use: exactly the usage limit is counted, per coupon and per customer.
func TestGooCouponUsageRace(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	one, perUser := 1, 1
	if err := srv.gooSaveCoupon(ctx, &gooCoupon{Code: "ONCE", DiscountType: "fixed_cart", Amount: 1, UsageLimit: &one}); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSaveCoupon(ctx, &gooCoupon{Code: "MINE", DiscountType: "fixed_cart", Amount: 1, UsageLimitPerUser: &perUser}); err != nil {
		t.Fatal(err)
	}
	p := gooMakeSimple(t, srv, "Couponed", 10, nil)
	var orders []*gooOrder
	for i := range 6 {
		email := "e" + itoa64(int64(i)) + "@example.test"
		orders = append(orders, gooRaceOrder(t, srv, email, 0, map[*gooProduct]int{p: 1}, "once"))
	}
	for range 4 { // one customer, four orders with a once-per-customer coupon
		orders = append(orders, gooRaceOrder(t, srv, "Same@Example.test", 0, map[*gooProduct]int{p: 1}, "MINE"))
	}
	var fns []func()
	for _, o := range orders {
		fns = append(fns, func() {
			if err := srv.gooPaymentComplete(ctx, o, "tx"); err != nil {
				t.Error(err)
			}
		})
	}
	gooRace(fns...)
	counts := map[string]int{}
	for _, o := range orders {
		o, _ := srv.gooGetOrder(ctx, o.ID)
		code := o.ItemsOf("coupon")[0].Name
		switch o.Status {
		case "processing":
			counts[code]++
			if o.Meta["_recorded_coupon_usage_counts"] != true {
				t.Errorf("paid order %d did not record its coupon use", o.ID)
			}
		case "on-hold":
			notes, _ := srv.gooOrderNotes(ctx, o.ID, false)
			if len(notes) == 0 || !strings.Contains(notes[0].Content, `Usage limit for coupon "`+code+`" has been reached.`) {
				t.Errorf("held order %d note: %+v", o.ID, notes)
			}
		default:
			t.Errorf("order %d: %s", o.ID, o.Status)
		}
	}
	if counts["once"] != 1 || counts["MINE"] != 1 {
		t.Errorf("orders paid with the coupon: %v", counts)
	}
	for code, want := range map[string]int{"ONCE": 1, "MINE": 1} {
		c, err := srv.gooCouponByCode(ctx, code)
		if err != nil {
			t.Fatal(err)
		}
		if c.UsageCount != want || len(c.UsedBy) != want {
			t.Errorf("%s: usage %d used_by %v", code, c.UsageCount, c.UsedBy)
		}
	}
	// Cancelling the paid order gives the use back once, even when cancelled twice at the same time.
	var paidOnce *gooOrder
	for _, o := range orders[:6] {
		if o, _ := srv.gooGetOrder(ctx, o.ID); o.Status == "processing" {
			paidOnce = o
		}
	}
	a, _ := srv.gooGetOrder(ctx, paidOnce.ID)
	b, _ := srv.gooGetOrder(ctx, paidOnce.ID)
	gooRace(func() { _ = srv.gooSetStatus(ctx, a, "cancelled", "", "") }, func() { _ = srv.gooSetStatus(ctx, b, "cancelled", "", "") })
	if c, _ := srv.gooCouponByCode(ctx, "ONCE"); c.UsageCount != 0 {
		t.Errorf("ONCE after cancelling: %d", c.UsageCount)
	}
}
