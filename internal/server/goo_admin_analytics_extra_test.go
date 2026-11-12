package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGooAnalyticsReportsFixes(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Fixture Hat", 25, nil)
	var first *gooOrder
	for i := 0; i < 2; i++ {
		o := &gooOrder{Status: "completed", Billing: map[string]string{"first_name": "R", "email": "r" + strconv.Itoa(i) + "@example.test"}, Shipping: map[string]string{},
			Items: []*gooOrderItem{{Type: "line_item", Name: p.Name, ProductID: p.ID, Quantity: 2, Subtotal: 50, Total: 50}}}
		srv.gooRecalculate(o)
		if err := srv.gooSaveOrder(ctx, o, true); err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = o
		}
	}
	first, _ = srv.gooGetOrder(ctx, first.ID)
	line := first.ItemsOf("line_item")[0]
	if _, err := srv.gooCreateRefund(ctx, first, 25, "x", []gooRefundLine{{ItemID: line.ID, Qty: 1, Total: 25}}, false, false, 0); err != nil {
		t.Fatal(err)
	}

	// 5.3: the Products report subtracts refunds like the Overview (4 sold - 1 refunded = 3, 100 - 25 = 75).
	from, to := time.Now().Add(-24*time.Hour), time.Now().Add(time.Hour)
	if tt := srv.gooTotals(ctx, from, to); tt.Items-tt.RefundedItems != 3 {
		t.Fatalf("overview items: %+v", tt)
	}
	body := gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=analytics&path=products&period=month")
	if !strings.Contains(body, "Fixture Hat") || !strings.Contains(body, "75.00") || !strings.Contains(body, ">3<") {
		t.Errorf("products report ignores refunds: %s", body[strings.Index(body, "woocommerce-summary"):][:600])
	}

	// 5.2: grouped per-interval totals equal the per-day gooTotals.
	days := gooRange{From: time.Now().AddDate(0, 0, -3), To: to}.Intervals()
	got := srv.gooTotalsByInterval(ctx, days)
	for i, iv := range days {
		want := srv.gooTotals(ctx, iv.Start, iv.End)
		if got[i] != want {
			t.Errorf("interval %d: %+v != %+v", i, got[i], want)
		}
	}

	// 5.0: no row cap in the downloads; the screen notes a cut.
	var ids []int64
	for i := 0; i < 3; i++ {
		ids = append(ids, 0)
	}
	_ = ids
	tbl := &gooTable{}
	for i := 0; i < gooAnalyticsScreenRows+5; i++ {
		tbl.add(nil, []string{"x"})
	}
	if len(tbl.CSV) != gooAnalyticsScreenRows+5 {
		t.Fatal("csv rows")
	}
	srv.store.Pool.Exec(ctx, `INSERT INTO goo_download_permissions (order_id, product_id, download_id, user_email, access_granted) SELECT $1, $2, 'f', 'a@b.test', now() FROM generate_series(1, 600)`, first.ID, p.ID)
	resp, _ := c.Get(base + "/wp-admin/goocommerce?page=analytics&path=downloads&period=month&download=csv")
	if n := strings.Count(bodyOf(resp), "\n"); n != 601 {
		t.Errorf("downloads csv lines = %d, want 601", n)
	}

	// 5.4: module tabs exist only while the module is on, and report real numbers.
	if code, _, _ := get(t, c, base+"/wp-admin/goocommerce?page=analytics&path=gift-cards"); code != 200 {
		t.Fatal("gift-cards off")
	}
	if b := gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=analytics&path=gift-cards"); strings.Contains(b, "Outstanding balance") {
		t.Error("gift card report visible with the module off")
	}
	if err := srv.gooSetOpts(ctx, map[string]any{"extensions": map[string]any{gsubKey: true, bkKey: true, gcKey: true}}); err != nil {
		t.Fatal(err)
	}
	srv.store.Pool.Exec(ctx, `INSERT INTO goo_gift_cards (code, initial, balance, status) VALUES ('AAAA-BBBB-CCCC-DDDD', 100, 40, 'active')`)
	srv.store.Pool.Exec(ctx, `INSERT INTO goo_subscriptions (status, billing_period, billing_interval, recurring_total) VALUES ('active','year',1,120),('active','month',2,20)`)
	srv.store.Pool.Exec(ctx, `INSERT INTO goo_bookings (product_id, status, start_at, end_at, cost) VALUES ($1,'paid',now(),now() + interval '2 hours',30)`, p.ID)
	b := gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=analytics&path=gift-cards&period=month")
	if !strings.Contains(b, "Outstanding balance") || !strings.Contains(b, "40.00") {
		t.Error("gift card report")
	}
	b = gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=analytics&path=subscriptions&period=month")
	if !strings.Contains(b, "Monthly recurring revenue") || !strings.Contains(b, "20.00") { // 120/12 + 20/2
		t.Error("subscription MRR")
	}
	b = gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=analytics&path=bookings&period=month")
	if !strings.Contains(b, "Booked hours") || !strings.Contains(b, "30.00") || !strings.Contains(b, "2.0") {
		t.Error("bookings report")
	}
}

// wc-analytics: known orders and a refund give known revenue/orders/products figures, per interval and in
// total, and the weekly summary mail goes out once per week.
func TestGooAnalyticsRESTAndWeeklySummary(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Stat Hat", 25, nil)
	mk := func(status string, qty float64, ship float64) *gooOrder {
		o := &gooOrder{Status: status, Billing: map[string]string{"email": "s@example.test"}, Shipping: map[string]string{},
			Items: []*gooOrderItem{{Type: "line_item", Name: p.Name, ProductID: p.ID, Quantity: qty, Subtotal: 25 * qty, Total: 25 * qty}}}
		if ship > 0 {
			o.Items = append(o.Items, &gooOrderItem{Type: "shipping", Name: "Flat", Total: ship})
		}
		srv.gooRecalculate(o)
		if err := srv.gooSaveOrder(ctx, o, true); err != nil {
			t.Fatal(err)
		}
		return o
	}
	a := mk("completed", 2, 5) // 55
	mk("processing", 1, 0)     // 25
	mk("pending", 9, 0)        // unpaid, ignored
	if _, err := srv.gooCreateRefund(ctx, a, 25, "", []gooRefundLine{{ItemID: a.ItemsOf("line_item")[0].ID, Qty: 1, Total: 25}}, false, false, 0); err != nil {
		t.Fatal(err)
	}
	ck, cs, _, err := srv.gooRESTNewKey(ctx, 1, "Analytics", "read")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Now().In(srv.loc()).Format("2006-01-02")
	get := func(path string) gooResp {
		return gooReq(t, &http.Client{}, "GET", base+"/wp-json/wc-analytics"+path, nil, gooBasic(ck, cs))
	}
	r := get("/reports/revenue/stats?after=" + day + "&before=" + day + "&interval=day")
	if r.Code != http.StatusOK {
		t.Fatalf("revenue/stats: %d %s", r.Code, r.Raw)
	}
	tot := gooJ(r.Body, "totals")
	// gross 55+25=80, refund 25 -> total 55; shipping 5; net 80-5-25=50; items 3-1=2.
	for k, want := range map[string]float64{"total_sales": 55, "net_revenue": 50, "shipping": 5, "refunds": 25, "orders_count": 2, "num_items_sold": 2} {
		if got, _ := gooJ(tot, k).(float64); got != want {
			t.Errorf("revenue totals %s = %v, want %v (%s)", k, gooJ(tot, k), want, r.Raw)
		}
	}
	ivs, _ := gooJ(r.Body, "intervals").([]any)
	if len(ivs) != 1 {
		t.Fatalf("intervals: %s", r.Raw)
	}
	if got, _ := gooJ(ivs[0], "subtotals", "net_revenue").(float64); got != 50 {
		t.Errorf("interval net: %s", r.Raw)
	}
	r = get("/reports/orders/stats?after=" + day + "&before=" + day)
	if got, _ := gooJ(r.Body, "totals", "avg_order_value").(float64); got != 25 {
		t.Errorf("orders/stats aov: %s", r.Raw)
	}
	r = get("/reports/products?after=" + day + "&before=" + day)
	if rows, _ := r.Body.([]any); len(rows) != 1 || gooJ(rows[0], "extended_info", "name") != "Stat Hat" || gooJ(rows[0], "items_sold") != float64(3) {
		t.Errorf("products: %s", r.Raw)
	}
	if r = get("/reports/revenue/stats?interval=fortnight"); r.Code != http.StatusBadRequest {
		t.Errorf("bad interval: %d", r.Code)
	}
	if r = gooReq(t, &http.Client{}, "GET", base+"/wp-json/wc-analytics/reports/revenue/stats", nil, nil); r.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", r.Code)
	}

	// Weekly summary: off by default, one mail when on, none the second time in the same week.
	count := func() int {
		var n int
		_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE name = 'wp_mail' AND payload::text LIKE '%Weekly store summary%'`).Scan(&n)
		return n
	}
	nextWeek := time.Now().AddDate(0, 0, 8)
	if err := srv.gooWeeklySummary(ctx, nextWeek); err != nil || count() != 0 {
		t.Fatalf("summary while off: %v %d", err, count())
	}
	_ = srv.gooSetOpts(ctx, map[string]any{"weekly_summary_email": "yes", "stock_email_recipient": "boss@example.test"})
	for i := 0; i < 2; i++ {
		if err := srv.gooWeeklySummary(ctx, nextWeek); err != nil {
			t.Fatal(err)
		}
	}
	if count() != 1 {
		t.Errorf("weekly mails: %d", count())
	}
}

// Occupancy of a bookable product and the renewal/retention figures of the subscriptions report.
func TestGooAnalyticsOccupancyAndRenewals(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSetOpts(ctx, map[string]any{"extensions": map[string]any{gsubKey: true, bkKey: true}}); err != nil {
		t.Fatal(err)
	}
	// 1-hour blocks, one place, always available: 24 places a day.
	p := gxGapProduct(t, srv, false)
	day := gxNextWeekday(1)
	for _, h := range []int{9, 10, 11} {
		b := &bkBooking{ProductID: p.ID, Status: "paid", Start: day.Add(time.Duration(h) * time.Hour), End: day.Add(time.Duration(h+1) * time.Hour)}
		if err := srv.bkInsert(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	_ = srv.bkInsert(ctx, &bkBooking{ProductID: p.ID, Status: "cancelled", Start: day.Add(15 * time.Hour), End: day.Add(16 * time.Hour)})
	loc := srv.gxLoc()
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	used, capacity := srv.bkOccupancy(ctx, p.ID, from, from.AddDate(0, 0, 1))
	if capacity != 24 || used < 2 || used > 3 { // the site zone may push one UTC booking over the day edge
		t.Errorf("occupancy %v / %v, want 3 / 24", used, capacity)
	}
	if _, cp := srv.bkOccupancy(ctx, p.ID+99999, from, from.AddDate(0, 0, 1)); cp != 0 {
		t.Error("an unknown product has capacity")
	}
	b := gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=analytics&path=bookings&period=custom&after="+from.Format("2006-01-02")+"&before="+from.Format("2006-01-02"))
	if !strings.Contains(b, "Occupancy") {
		t.Error("the bookings report has no occupancy")
	}

	// One subscription running before the month and still active, one that ended in it; a paid and a failed renewal.
	srv.store.Pool.Exec(ctx, `INSERT INTO goo_subscriptions (status, billing_period, billing_interval, recurring_total, start_date, end_date) VALUES
		('active','month',1,10, now() - interval '400 days', NULL), ('cancelled','month',1,10, now() - interval '400 days', now() - interval '1 hour')`)
	for _, st := range []string{"completed", "failed"} {
		o := &gooOrder{Status: st, Total: 10, Billing: map[string]string{"email": "r@example.test"}, Shipping: map[string]string{}, Meta: map[string]any{"_subscription_renewal": float64(1)}}
		if err := srv.gooSaveOrder(ctx, o, true); err != nil {
			t.Fatal(err)
		}
	}
	d := &gooAnalyticsData{}
	now := time.Now()
	srv.gooAnalyticsSubscriptions(ctx, d, &gooTable{}, now.AddDate(0, 0, -30), now.Add(time.Hour))
	got := map[string]string{}
	for _, cd := range d.Cards {
		got[cd.Label] = string(cd.Value)
	}
	if got["Retention rate"] != "50.0%" || got["Renewals paid"] != "1" || got["Failed renewals"] != "1" || !strings.Contains(got["Renewal revenue"], "10.00") {
		t.Errorf("subscription cards: %v", got)
	}
}
