package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/mail"
)

// wc-analytics: the JSON WooCommerce Analytics serves to external dashboards (revenue and orders stats
// per interval, a products report), computed by the same queries as the admin Analytics screens.
// Weekly store summary e-mail: gooWeeklySummary.

const gooAnalyticsNS = "wc-analytics"

func (s *Server) registerGooAnalyticsREST() {
	caps := gooCapsAll("view_woocommerce_reports")
	reg := func(pattern string, h restHandler) {
		s.gooRoute(gooAnalyticsNS, pattern, map[string]restHandler{http.MethodGet: func(c *gin.Context, req *restRequest) {
			u, aerr := s.gooV3User(c, req.Method)
			if aerr != nil {
				gooFail(c, aerr)
				return
			}
			if u == nil {
				restErr(c, http.StatusUnauthorized, "woocommerce_rest_cannot_view", "Sorry, you cannot list resources.")
				return
			}
			if !u.Can(caps.Read, nil) {
				restErr(c, http.StatusForbidden, "woocommerce_rest_cannot_view", "Sorry, you cannot list resources.")
				return
			}
			h(c, req)
		}})
	}
	reg("/reports/revenue/stats", s.gooAnalyticsRevenueStats)
	reg("/reports/orders/stats", s.gooAnalyticsOrdersStats)
	reg("/reports/products", s.gooAnalyticsProducts)
}

// gooAnalyticsRange reads after/before (ISO 8601 date or date-time, default: the last 7 days) and
// interval (hour, day, week, month, quarter, year; default day) like the Analytics REST parameters.
func (s *Server) gooAnalyticsRange(c *gin.Context) (from, to time.Time, interval string, errMsg string) {
	loc := s.loc()
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	from, to = today.AddDate(0, 0, -6), today.AddDate(0, 0, 1)
	parse := func(v string) (time.Time, bool) {
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
			if t, err := time.ParseInLocation(layout, v, loc); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	}
	if v := c.Query("after"); v != "" {
		t, ok := parse(v)
		if !ok {
			return from, to, "", "Invalid parameter(s): after"
		}
		from = t
	}
	if v := c.Query("before"); v != "" {
		t, ok := parse(v)
		if !ok {
			return from, to, "", "Invalid parameter(s): before"
		}
		to = t
		if len(v) == 10 { // a bare date includes that whole day
			to = t.AddDate(0, 0, 1)
		}
	}
	interval = c.DefaultQuery("interval", "day")
	switch interval {
	case "hour", "day", "week", "month", "quarter", "year":
	default:
		return from, to, "", "Invalid parameter(s): interval"
	}
	if !to.After(from) {
		return from, to, "", "Invalid parameter(s): before"
	}
	return
}

// gooAnalyticsIntervals cuts [from, to) into interval-sized buckets (weeks start on the site's
// start_of_week), clipping the first and last bucket to the range. A cap keeps a request cheap.
func (s *Server) gooAnalyticsIntervals(from, to time.Time, interval string) []gooInterval {
	loc := from.Location()
	start := func(t time.Time) time.Time {
		switch interval {
		case "hour":
			return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc)
		case "week":
			d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
			sow := s.settings.Int("start_of_week")
			return d.AddDate(0, 0, -((int(d.Weekday()) - sow + 7) % 7))
		case "month":
			return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
		case "quarter":
			return time.Date(t.Year(), ((t.Month()-1)/3)*3+1, 1, 0, 0, 0, 0, loc)
		case "year":
			return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, loc)
		}
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	}
	next := func(t time.Time) time.Time {
		switch interval {
		case "hour":
			return t.Add(time.Hour)
		case "week":
			return t.AddDate(0, 0, 7)
		case "month":
			return t.AddDate(0, 1, 0)
		case "quarter":
			return t.AddDate(0, 3, 0)
		case "year":
			return t.AddDate(1, 0, 0)
		}
		return t.AddDate(0, 0, 1)
	}
	var out []gooInterval
	for b := start(from); b.Before(to) && len(out) < 1000; b = next(b) {
		iv := gooInterval{Start: b, End: next(b), Label: b.Format("2006-01-02 15:04:05")}
		if iv.Start.Before(from) {
			iv.Start = from
		}
		if iv.End.After(to) {
			iv.End = to
		}
		out = append(out, iv)
	}
	return out
}

func (s *Server) gooAnalyticsNum(v float64) float64 {
	f, _ := strconv.ParseFloat(s.gooDec(v), 64)
	return f
}

// gooAnalyticsAOV is net revenue per order.
func gooAnalyticsAOV(net float64, orders int) float64 {
	if orders == 0 {
		return 0
	}
	return net / float64(orders)
}

func (s *Server) gooAnalyticsRevenueStats(c *gin.Context, req *restRequest) {
	from, to, interval, msg := s.gooAnalyticsRange(c)
	if msg != "" {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", msg)
		return
	}
	ctx := c.Request.Context()
	fig := func(t gooTotalsRow) map[string]any {
		return map[string]any{
			"orders_count": t.Orders, "num_items_sold": t.Items - t.RefundedItems,
			"gross_sales": s.gooAnalyticsNum(t.Gross - t.Tax - t.ShippingTax + t.Discount), "total_sales": s.gooAnalyticsNum(t.Gross - t.Refunds),
			"coupons": s.gooAnalyticsNum(t.Discount), "refunds": s.gooAnalyticsNum(t.Refunds), "taxes": s.gooAnalyticsNum(t.Tax + t.ShippingTax),
			"shipping": s.gooAnalyticsNum(t.Shipping), "net_revenue": s.gooAnalyticsNum(t.Net),
		}
	}
	ivs := s.gooAnalyticsIntervals(from, to, interval)
	rows := s.gooTotalsByInterval(ctx, ivs)
	list := make([]any, 0, len(ivs))
	for i, iv := range ivs {
		list = append(list, map[string]any{"interval": interval, "date_start": iv.Start.Format("2006-01-02 15:04:05"), "date_end": iv.End.Add(-time.Second).Format("2006-01-02 15:04:05"),
			"subtotals": fig(rows[i])})
	}
	restJSON(c, http.StatusOK, map[string]any{"totals": fig(s.gooTotals(ctx, from, to)), "intervals": list})
}

func (s *Server) gooAnalyticsOrdersStats(c *gin.Context, req *restRequest) {
	from, to, interval, msg := s.gooAnalyticsRange(c)
	if msg != "" {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", msg)
		return
	}
	ctx := c.Request.Context()
	fig := func(t gooTotalsRow) map[string]any {
		return map[string]any{"orders_count": t.Orders, "num_items_sold": t.Items - t.RefundedItems, "net_revenue": s.gooAnalyticsNum(t.Net),
			"avg_order_value": s.gooAnalyticsNum(gooAnalyticsAOV(t.Net, t.Orders)),
			"avg_items_per_order": func() float64 {
				if t.Orders == 0 {
					return 0
				}
				return float64(int(float64(t.Items-t.RefundedItems)/float64(t.Orders)*100+0.5)) / 100
			}()}
	}
	ivs := s.gooAnalyticsIntervals(from, to, interval)
	rows := s.gooTotalsByInterval(ctx, ivs)
	list := make([]any, 0, len(ivs))
	for i, iv := range ivs {
		list = append(list, map[string]any{"interval": interval, "date_start": iv.Start.Format("2006-01-02 15:04:05"), "date_end": iv.End.Add(-time.Second).Format("2006-01-02 15:04:05"),
			"subtotals": fig(rows[i])})
	}
	restJSON(c, http.StatusOK, map[string]any{"totals": fig(s.gooTotals(ctx, from, to)), "intervals": list})
}

// gooAnalyticsProducts lists the products sold in the period, best sellers first (items sold net of refunds).
func (s *Server) gooAnalyticsProducts(c *gin.Context, req *restRequest) {
	from, to, _, msg := s.gooAnalyticsRange(c)
	if msg != "" {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", msg)
		return
	}
	limit := 10
	if n, err := strconv.Atoi(c.Query("per_page")); err == nil && n >= 1 && n <= 100 {
		limit = n
	}
	ctx := c.Request.Context()
	rows, err := s.store.Pool.Query(ctx, `SELECT i.product_id, max(i.name), sum(i.quantity)::float8, sum(i.total)::float8, count(DISTINCT o.id)
		FROM goo_order_items i JOIN goo_orders o ON o.id = i.order_id
		WHERE o.type = 'shop_order' AND o.status = ANY($1) AND i.type = 'line_item' AND o.date_created >= $2 AND o.date_created < $3
		GROUP BY i.product_id ORDER BY 3 DESC, i.product_id LIMIT $4`, gooPaidList, from, to, limit)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "woocommerce_rest_cannot_view", err.Error())
		return
	}
	type row struct {
		id       int64
		name     string
		qty, net float64
		orders   int
	}
	var list []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.id, &r.name, &r.qty, &r.net, &r.orders) == nil {
			list = append(list, r)
		}
	}
	rows.Close()
	out := make([]any, 0, len(list))
	for _, r := range list {
		name := r.name
		if p, err := s.gooGetProduct(ctx, r.id); err == nil {
			name = p.Name
		}
		out = append(out, map[string]any{"product_id": r.id, "items_sold": int(r.qty), "net_revenue": s.gooAnalyticsNum(r.net), "orders_count": r.orders,
			"extended_info": map[string]any{"name": name}})
	}
	restJSON(c, http.StatusOK, out)
}

// gooWeeklySummary e-mails last week's figures (Monday to Sunday before `now`) to the stock notification
// recipients. It does nothing unless the "Weekly summary" setting is on, and sends once per week.
func (s *Server) gooWeeklySummary(ctx context.Context, now time.Time) error {
	if !s.gooOn() || !s.gooOptBool("weekly_summary_email") {
		return nil
	}
	rcpt := gooEmailRecipients(s.gooOptString("stock_email_recipient"))
	if len(rcpt) == 0 {
		rcpt = gooEmailRecipients(s.settings.String("admin_email"))
	}
	if len(rcpt) == 0 {
		return nil
	}
	loc := s.loc()
	n := now.In(loc)
	thisMon := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
	thisMon = thisMon.AddDate(0, 0, -((int(thisMon.Weekday()) + 6) % 7))
	from := thisMon.AddDate(0, 0, -7)
	key := from.Format("2006-01-02")
	if s.gooOptString("weekly_summary_last") == key {
		return nil
	}
	t := s.gooTotals(ctx, from, thisMon)
	prev := s.gooTotals(ctx, from.AddDate(0, 0, -7), from)
	pct := func(cur, old float64) string {
		if old == 0 {
			return "n/a"
		}
		return fmt.Sprintf("%+.1f%%", (cur-old)/old*100)
	}
	cur := gooCurrencySymbol(s.gooCurrency())
	var b strings.Builder
	fmt.Fprintf(&b, "Store summary for %s - %s\n\n", from.Format("2006-01-02"), thisMon.AddDate(0, 0, -1).Format("2006-01-02"))
	fmt.Fprintf(&b, "Orders: %d (%s vs the week before)\n", t.Orders, pct(float64(t.Orders), float64(prev.Orders)))
	fmt.Fprintf(&b, "Items sold: %d\n", t.Items-t.RefundedItems)
	fmt.Fprintf(&b, "Total sales: %s%s\n", cur, s.gooDec(t.Gross-t.Refunds))
	fmt.Fprintf(&b, "Net sales: %s%s (%s)\n", cur, s.gooDec(t.Net), pct(t.Net, prev.Net))
	fmt.Fprintf(&b, "Refunds: %s%s\n", cur, s.gooDec(t.Refunds))
	fmt.Fprintf(&b, "Average order value: %s%s\n", cur, s.gooDec(gooAnalyticsAOV(t.Net, t.Orders)))
	s.sendMail(ctx, mail.Message{To: rcpt, Subject: fmt.Sprintf("[%s] Weekly store summary", s.settings.String("blogname")), Body: b.String()})
	return s.gooSetOpts(ctx, map[string]any{"weekly_summary_last": key})
}
