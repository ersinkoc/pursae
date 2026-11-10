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
	d.Cards = []gooCard{{Label: "Active subscriptions", Value: gooCount(active)}, {Label: "Monthly recurring revenue", Value: s.gooPrice(activeMRR)},
		{Label: "New subscriptions", Value: gooCount(added)}, {Label: "Cancelled / expired", Value: gooCount(ended)},
		{Label: "Churn rate", Value: gooH(strconv.FormatFloat(churn, 'f', 1, 64) + "%")}}
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
	tbl.Headers = []string{"Bookable product", "Bookings", "Booked hours", "Cancelled", "Revenue"}
	rows, err := s.store.Pool.Query(ctx, `SELECT b.product_id, coalesce(max(po.title),''), count(*) FILTER (WHERE b.status = ANY($3)),
		coalesce(sum(extract(epoch FROM (b.end_at - b.start_at)) / 3600) FILTER (WHERE b.status = ANY($3)),0)::float8,
		count(*) FILTER (WHERE b.status = 'cancelled'), coalesce(sum(b.cost) FILTER (WHERE b.status IN ('paid','confirmed','complete')),0)::float8
		FROM goo_bookings b LEFT JOIN posts po ON po.id = b.product_id WHERE b.start_at >= $1 AND b.start_at < $2 AND b.status <> 'in-cart'
		GROUP BY b.product_id ORDER BY 6 DESC, 3 DESC`, from, to, bkHolding)
	var bookings, cancelled int
	var hours, revenue float64
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
			tbl.add([]template.HTML{gooH(title), gooCount(n), gooH(hs), gooCount(c), s.gooPrice(rev)}, []string{title, strconv.Itoa(n), hs, strconv.Itoa(c), s.gooFmtDec(rev)})
		}
		rows.Close()
	}
	d.Cards = []gooCard{{Label: "Bookings", Value: gooCount(bookings)}, {Label: "Booked hours", Value: gooH(strconv.FormatFloat(hours, 'f', 1, 64))},
		{Label: "Cancelled", Value: gooCount(cancelled)}, {Label: "Booking revenue", Value: s.gooPrice(revenue)}}
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
