package server

import (
	"context"
	"strconv"
	"time"

	"html/template"
)

// gooSoldOrRefunded selects the order rows an items report counts: paid orders created in [$2, $3) plus
// the refunds of paid or refunded orders made in the same range (their line items carry negative
// quantities and totals), so Products / Variations / Categories agree with the Overview. It needs
// "goo_orders o" and "LEFT JOIN goo_orders po ON po.id = o.parent_id" in the query.
const gooSoldOrRefunded = `((o.type = 'shop_order' AND o.status = ANY($1) AND o.date_created >= $2 AND o.date_created < $3)
	OR (o.type = 'shop_order_refund' AND po.status = ANY($1 || '{refunded}'::text[]) AND o.date_created >= $2 AND o.date_created < $3))`

type gooStockInfo struct{ sku, status, stock string }

// gooAnalyticsStockInfo loads SKU, stock status and quantity of many products in one query.
func (s *Server) gooAnalyticsStockInfo(ctx context.Context, ids []int64) map[int64]gooStockInfo {
	out := map[int64]gooStockInfo{}
	if len(ids) == 0 {
		return out
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT post_id, sku, stock_status, manage_stock, stock_quantity FROM goo_products WHERE post_id = ANY($1)`, ids)
	if err != nil {
		return out
	}
	defer rows.Close()
	labels := map[string]string{"instock": "In stock", "outofstock": "Out of stock", "onbackorder": "On backorder"}
	for rows.Next() {
		var id int64
		var sku, st string
		var manage bool
		var qty *int
		if rows.Scan(&id, &sku, &st, &manage, &qty) != nil {
			continue
		}
		stock := "N/A"
		if manage && qty != nil {
			stock = strconv.Itoa(*qty)
		}
		out[id] = gooStockInfo{sku, labels[st], stock}
	}
	return out
}

// gooTotalsByInterval is gooTotals for many intervals at once: four grouped queries instead of two per
// interval. The result is parallel to ivs.
func (s *Server) gooTotalsByInterval(ctx context.Context, ivs []gooInterval) []gooTotalsRow {
	out := make([]gooTotalsRow, len(ivs))
	if len(ivs) == 0 {
		return out
	}
	starts, ends := make([]time.Time, len(ivs)), make([]time.Time, len(ivs))
	for i, iv := range ivs {
		starts[i], ends[i] = iv.Start, iv.End
	}
	const buckets = `unnest($2::timestamptz[], $3::timestamptz[]) WITH ORDINALITY AS x(s, e, idx)`
	if rows, err := s.store.Pool.Query(ctx, `SELECT x.idx, count(*), coalesce(sum(o.total),0)::float8, coalesce(sum(o.total_tax),0)::float8, coalesce(sum(o.shipping_total),0)::float8,
		coalesce(sum(o.shipping_tax),0)::float8, coalesce(sum(o.discount_total),0)::float8 FROM `+buckets+`
		JOIN goo_orders o ON o.date_created >= x.s AND o.date_created < x.e WHERE o.type = 'shop_order' AND o.status = ANY($1) GROUP BY x.idx`, gooPaidList, starts, ends); err == nil {
		for rows.Next() {
			var idx int
			var t gooTotalsRow
			if rows.Scan(&idx, &t.Orders, &t.Gross, &t.Tax, &t.Shipping, &t.ShippingTax, &t.Discount) == nil && idx >= 1 && idx <= len(out) {
				out[idx-1].Orders, out[idx-1].Gross, out[idx-1].Tax = t.Orders, t.Gross, t.Tax
				out[idx-1].Shipping, out[idx-1].ShippingTax, out[idx-1].Discount = t.Shipping, t.ShippingTax, t.Discount
			}
		}
		rows.Close()
	}
	if rows, err := s.store.Pool.Query(ctx, `SELECT x.idx, coalesce(sum(i.quantity),0)::int FROM `+buckets+`
		JOIN goo_orders o ON o.date_created >= x.s AND o.date_created < x.e JOIN goo_order_items i ON i.order_id = o.id
		WHERE i.type = 'line_item' AND o.type = 'shop_order' AND o.status = ANY($1) GROUP BY x.idx`, gooPaidList, starts, ends); err == nil {
		for rows.Next() {
			var idx, n int
			if rows.Scan(&idx, &n) == nil && idx >= 1 && idx <= len(out) {
				out[idx-1].Items = n
			}
		}
		rows.Close()
	}
	if rows, err := s.store.Pool.Query(ctx, `SELECT x.idx, coalesce(sum(-r.total),0)::float8 FROM `+buckets+`
		JOIN goo_orders r ON r.date_created >= x.s AND r.date_created < x.e JOIN goo_orders o ON o.id = r.parent_id
		WHERE r.type = 'shop_order_refund' AND o.status = ANY($1 || '{refunded}'::text[]) GROUP BY x.idx`, gooPaidList, starts, ends); err == nil {
		for rows.Next() {
			var idx int
			var v float64
			if rows.Scan(&idx, &v) == nil && idx >= 1 && idx <= len(out) {
				out[idx-1].Refunds = v
			}
		}
		rows.Close()
	}
	if rows, err := s.store.Pool.Query(ctx, `SELECT x.idx, coalesce(sum(-i.quantity),0)::int FROM `+buckets+`
		JOIN goo_orders r ON r.date_created >= x.s AND r.date_created < x.e JOIN goo_order_items i ON i.order_id = r.id
		WHERE r.type = 'shop_order_refund' AND i.type = 'line_item' AND $1::text IS NOT NULL GROUP BY x.idx`, "", starts, ends); err == nil {
		for rows.Next() {
			var idx, n int
			if rows.Scan(&idx, &n) == nil && idx >= 1 && idx <= len(out) {
				out[idx-1].RefundedItems = n
			}
		}
		rows.Close()
	}
	for i := range out {
		t := &out[i]
		t.Net = t.Gross - t.Tax - t.Shipping - t.ShippingTax - t.Refunds
	}
	return out
}

// Optional-module reports ---------------------------------------------------------------------------------

func gooCount(n int) template.HTML { return gooH(formatNumber(n)) }

// gooAnalyticsSubscriptions: active count, monthly recurring revenue, new and ended subscriptions, churn.
func (s *Server) gooAnalyticsSubscriptions(ctx context.Context, d *gooAnalyticsData, tbl *gooTable, from, to time.Time) {
	// A subscription's recurring total normalised to one month.
	const mrr = `CASE billing_period WHEN 'day' THEN recurring_total * 30.4375 WHEN 'week' THEN recurring_total * 4.348214
		WHEN 'year' THEN recurring_total / 12 ELSE recurring_total END / GREATEST(billing_interval, 1)`
	var active, added, ended, atStart int
	var activeMRR float64
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(`+mrr+`),0)::float8 FROM goo_subscriptions WHERE status IN ('active','pending-cancel')`).Scan(&active, &activeMRR)
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_subscriptions WHERE start_date >= $1 AND start_date < $2 AND status <> 'pending'`, from, to).Scan(&added)
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_subscriptions WHERE status IN ('cancelled','expired') AND end_date >= $1 AND end_date < $2`, from, to).Scan(&ended)
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_subscriptions WHERE status <> 'pending' AND start_date < $1 AND (end_date IS NULL OR end_date >= $1 OR status IN ('active','pending-cancel','on-hold'))`, from).Scan(&atStart)
	churn := 0.0
	if atStart > 0 {
		churn = float64(ended) / float64(atStart) * 100
	}
	// Renewal and sign-up revenue: paid renewal orders and paid parent orders in the range (net of refunds).
	// Retention: of the subscriptions running at the start of the range, the share still running at its end.
	var renewals, failed, retained int
	var renewalRev, signupRev float64
	paid := gooPaidStatuses
	const net = `coalesce(sum(o.total + coalesce((SELECT sum(r.total) FROM goo_orders r WHERE r.parent_id = o.id AND r.type = 'shop_order_refund'),0)),0)::float8`
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*), `+net+` FROM goo_orders o WHERE o.type = 'shop_order' AND o.meta ? '_subscription_renewal'
		AND o.status = ANY($3) AND coalesce(o.date_paid, o.date_created) >= $1 AND coalesce(o.date_paid, o.date_created) < $2`, from, to, paid).Scan(&renewals, &renewalRev)
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_orders o WHERE o.type = 'shop_order' AND o.meta ? '_subscription_renewal'
		AND o.status = 'failed' AND o.date_created >= $1 AND o.date_created < $2`, from, to).Scan(&failed)
	_ = s.store.Pool.QueryRow(ctx, `SELECT `+net+` FROM goo_orders o WHERE o.type = 'shop_order' AND o.status = ANY($3)
		AND coalesce(o.date_paid, o.date_created) >= $1 AND coalesce(o.date_paid, o.date_created) < $2
		AND o.id IN (SELECT parent_order_id FROM goo_subscriptions WHERE parent_order_id <> 0)`, from, to, paid).Scan(&signupRev)
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_subscriptions WHERE status <> 'pending' AND start_date < $1
		AND (end_date IS NULL OR end_date >= $2 OR status IN ('active','pending-cancel','on-hold'))`, from, to).Scan(&retained)
	retention := 0.0
	if atStart > 0 {
		retention = min(float64(retained)/float64(atStart)*100, 100)
	}
	d.Cards = []gooCard{{Label: "Active subscriptions", Value: gooCount(active)}, {Label: "Monthly recurring revenue", Value: s.gooPrice(activeMRR)},
		{Label: "New subscriptions", Value: gooCount(added)}, {Label: "Cancelled / expired", Value: gooCount(ended)},
		{Label: "Churn rate", Value: gooH(strconv.FormatFloat(churn, 'f', 1, 64) + "%")},
		{Label: "Retention rate", Value: gooH(strconv.FormatFloat(retention, 'f', 1, 64) + "%")},
		{Label: "Renewal revenue", Value: s.gooPrice(renewalRev)}, {Label: "Renewals paid", Value: gooCount(renewals)},
		{Label: "Failed renewals", Value: gooCount(failed)}, {Label: "Sign-up revenue", Value: s.gooPrice(signupRev)}}
	tbl.Headers = []string{"Status", "Subscriptions", "Monthly recurring revenue"}
	rows, err := s.store.Pool.Query(ctx, `SELECT status, count(*), coalesce(sum(`+mrr+`),0)::float8 FROM goo_subscriptions GROUP BY status ORDER BY 2 DESC`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		var v float64
		if rows.Scan(&st, &n, &v) == nil {
			label := gooOptLabel(gsubStatuses, st)
			tbl.add([]template.HTML{gooH(label), gooCount(n), s.gooPrice(v)}, []string{label, strconv.Itoa(n), s.gooFmtDec(v)})
		}
	}
}

// gooAnalyticsBookings: bookings starting in the range, revenue, cancellations and booked hours per product.
func (s *Server) gooAnalyticsBookings(ctx context.Context, d *gooAnalyticsData, tbl *gooTable, from, to time.Time) {
	tbl.Headers = []string{"Bookable product", "Bookings", "Booked hours", "Occupancy", "Cancelled", "Revenue"}
	rows, err := s.store.Pool.Query(ctx, `SELECT b.product_id, coalesce(max(po.title),''), count(*) FILTER (WHERE b.status = ANY($3)),
		coalesce(sum(extract(epoch FROM (b.end_at - b.start_at)) / 3600) FILTER (WHERE b.status = ANY($3)),0)::float8,
		count(*) FILTER (WHERE b.status = 'cancelled'), coalesce(sum(b.cost) FILTER (WHERE b.status IN ('paid','confirmed','complete')),0)::float8
		FROM goo_bookings b LEFT JOIN posts po ON po.id = b.product_id WHERE b.start_at >= $1 AND b.start_at < $2 AND b.status <> 'in-cart'
		GROUP BY b.product_id ORDER BY 6 DESC, 3 DESC`, from, to, bkHolding)
	var bookings, cancelled int
	var hours, revenue float64
	var list []bkReportRow
	if err == nil {
		for rows.Next() {
			var id int64
			var title string
			var n, c int
			var h, rev float64
			if rows.Scan(&id, &title, &n, &h, &c, &rev) != nil {
				continue
			}
			bookings += n
			cancelled += c
			hours += h
			revenue += rev
			hs := strconv.FormatFloat(h, 'f', 1, 64)
			list = append(list, bkReportRow{id, title, n, c, h, rev, hs})
		}
		rows.Close()
	}
	// Occupancy needs the products' rules, read after the rows are closed.
	var used, capacity float64
	for _, r := range list {
		occ := "–"
		if u, cp := s.bkOccupancy(ctx, r.id, from, to); cp > 0 {
			used += u
			capacity += cp
			occ = strconv.FormatFloat(min(u/cp*100, 100), 'f', 1, 64) + "%"
		}
		tbl.add([]template.HTML{gooH(r.title), gooCount(r.n), gooH(r.hs), gooH(occ), gooCount(r.c), s.gooPrice(r.rev)},
			[]string{r.title, strconv.Itoa(r.n), r.hs, occ, strconv.Itoa(r.c), s.gooFmtDec(r.rev)})
	}
	total := "–"
	if capacity > 0 {
		total = strconv.FormatFloat(min(used/capacity*100, 100), 'f', 1, 64) + "%"
	}
	d.Cards = []gooCard{{Label: "Bookings", Value: gooCount(bookings)}, {Label: "Booked hours", Value: gooH(strconv.FormatFloat(hours, 'f', 1, 64))},
		{Label: "Occupancy", Value: gooH(total)}, {Label: "Cancelled", Value: gooCount(cancelled)}, {Label: "Booking revenue", Value: s.gooPrice(revenue)}}
}

type bkReportRow struct {
	id     int64
	title  string
	n, c   int
	h, rev float64
	hs     string
}

// bkOccupancy is a bookable product's used and offered places between from and to: every block the
// availability rules open counts its places (max bookings per block, or the summed quantity of the
// product's resources), every booking holding a place counts its blocks (times its persons when each
// person takes a place). Ranges longer than a year are cut to keep the report cheap; month-unit
// products are not rated (capacity 0).
func (s *Server) bkOccupancy(ctx context.Context, productID int64, from, to time.Time) (used, capacity float64) {
	p, err := s.gooGetProduct(ctx, productID)
	if err != nil || p.Type != bkType {
		return 0, 0
	}
	c := bkConfOf(p)
	if c.Unit == "month" {
		return 0, 0
	}
	places := c.MaxPerBlock
	if res := s.gooBkProductResources(ctx, c); len(res) > 0 {
		places = 0
		for _, r := range res {
			places += gxbCapacity(c, r)
		}
	}
	if places <= 0 {
		return 0, 0
	}
	loc := s.gxLoc()
	from, to = from.In(loc), to.In(loc)
	if to.Sub(from) > 366*24*time.Hour {
		to = from.AddDate(0, 0, 366)
	}
	step := c.blockMinutes()
	y, m, d := from.Date()
	for day := time.Date(y, m, d, 0, 0, 0, 0, loc); day.Before(to); day = day.AddDate(0, 0, 1) {
		next := day.AddDate(0, 0, 1)
		if !c.timed() {
			if !day.Before(from) && c.bookable(day, next) {
				capacity += float64(places)
			}
			continue
		}
		if step <= 0 {
			return 0, 0
		}
		for mins := max(bkMinutesOf(c.FirstBlockTime), 0); ; mins += step {
			t := time.Date(day.Year(), day.Month(), day.Day(), 0, mins, 0, 0, loc)
			e := t.Add(time.Duration(step) * time.Minute)
			if (e.After(next) && !c.AllowOvernight) || !t.Before(next) {
				break
			}
			if !t.Before(from) && t.Before(to) && c.bookable(t, e) {
				capacity += float64(places)
			}
		}
	}
	if capacity == 0 {
		return 0, 0
	}
	for _, b := range s.bkQuery(ctx, `product_id = $1 AND start_at >= $2 AND start_at < $3 AND status = ANY($4)`, productID, from, to, []string{"unpaid", "pending-confirmation", "confirmed", "paid", "complete"}) {
		blocks := 1.0
		if c.timed() {
			blocks = max(b.End.Sub(b.Start).Minutes()/float64(step), 1)
		} else if days := b.End.Sub(b.Start).Hours() / 24; days > 1 {
			blocks = float64(int(days + 0.5))
		}
		if c.PersonQtyMult {
			blocks *= float64(max(bkPersonsTotal(b.Persons), 1))
		}
		used += blocks
	}
	return used, capacity
}

// gooAnalyticsGiftCards: cards sold, amount redeemed and the outstanding liability (balance of live cards).
func (s *Server) gooAnalyticsGiftCards(ctx context.Context, d *gooAnalyticsData, tbl *gooTable, from, to time.Time) {
	var issued int
	var issuedValue, used, liability float64
	var live int
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(initial),0)::float8 FROM goo_gift_cards WHERE created >= $1 AND created < $2`, from, to).Scan(&issued, &issuedValue)
	_ = s.store.Pool.QueryRow(ctx, `SELECT coalesce(sum(abs(amount)),0)::float8 FROM goo_gift_card_activity WHERE type = 'used' AND date >= $1 AND date < $2`, from, to).Scan(&used)
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(balance),0)::float8 FROM goo_gift_cards WHERE status = 'active' AND (expires IS NULL OR expires >= current_date)`).Scan(&live, &liability)
	d.Cards = []gooCard{{Label: "Cards sold", Value: gooCount(issued)}, {Label: "Value sold", Value: s.gooPrice(issuedValue)},
		{Label: "Redeemed", Value: s.gooPrice(used)}, {Label: "Outstanding balance", Value: s.gooPrice(liability)}, {Label: "Active cards", Value: gooCount(live)}}
	tbl.Headers = []string{"Status", "Cards", "Initial value", "Balance"}
	rows, err := s.store.Pool.Query(ctx, `SELECT status, count(*), coalesce(sum(initial),0)::float8, coalesce(sum(balance),0)::float8 FROM goo_gift_cards GROUP BY status ORDER BY 2 DESC`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		var init, bal float64
		if rows.Scan(&st, &n, &init, &bal) == nil {
			tbl.add([]template.HTML{gooH(st), gooCount(n), s.gooPrice(init), s.gooPrice(bal)}, []string{st, strconv.Itoa(n), s.gooFmtDec(init), s.gooFmtDec(bal)})
		}
	}
}
