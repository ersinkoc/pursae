package server

import (
	"context"
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Analytics (page=analytics&path=…): WooCommerce Admin's Overview with performance cards, a chart and
// leaderboards, and the report pages (Products, Revenue, Orders, Variations, Categories, Coupons,
// Taxes, Downloads, Stock, Customers): a summary, a table and a CSV download each. SQL runs over
// goo_orders / goo_order_items; sales count the paid statuses (processing, completed, on-hold).

// gooPeriod is the analytics date range and its comparison range.
type gooPeriod struct {
	Key, Label, Compare string
	From, To            time.Time // [From, To)
	PrevFrom, PrevTo    time.Time
	After, Before       string
}

var gooPeriods = []gooOpt{{"today", "Today"}, {"yesterday", "Yesterday"}, {"week", "Week to date"}, {"last_week", "Last week"},
	{"month", "Month to date"}, {"last_month", "Last month"}, {"quarter", "Quarter to date"}, {"last_quarter", "Last quarter"},
	{"year", "Year to date"}, {"last_year", "Last year"}, {"custom", "Custom"}}

func (s *Server) gooAnalyticsPeriod(c *gin.Context) gooPeriod {
	loc := s.loc()
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	p := gooPeriod{Key: c.DefaultQuery("period", "month"), Compare: c.DefaultQuery("compare", "previous_year")}
	weekStart := today.AddDate(0, 0, -int((today.Weekday()+6)%7))
	qStart := time.Date(now.Year(), time.Month((int(now.Month())-1)/3*3+1), 1, 0, 0, 0, 0, loc)
	switch p.Key {
	case "today":
		p.From, p.To = today, today.AddDate(0, 0, 1)
	case "yesterday":
		p.From, p.To = today.AddDate(0, 0, -1), today
	case "week":
		p.From, p.To = weekStart, today.AddDate(0, 0, 1)
	case "last_week":
		p.From, p.To = weekStart.AddDate(0, 0, -7), weekStart
	case "last_month":
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
		p.From, p.To = first.AddDate(0, -1, 0), first
	case "quarter":
		p.From, p.To = qStart, today.AddDate(0, 0, 1)
	case "last_quarter":
		p.From, p.To = qStart.AddDate(0, -3, 0), qStart
	case "year":
		p.From, p.To = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, loc), today.AddDate(0, 0, 1)
	case "last_year":
		p.From, p.To = time.Date(now.Year()-1, 1, 1, 0, 0, 0, 0, loc), time.Date(now.Year(), 1, 1, 0, 0, 0, 0, loc)
	case "custom":
		p.After, p.Before = c.Query("after"), c.Query("before")
		f, e1 := time.ParseInLocation("2006-01-02", p.After, loc)
		t, e2 := time.ParseInLocation("2006-01-02", p.Before, loc)
		if e1 != nil || e2 != nil || t.Before(f) {
			f, t = today.AddDate(0, 0, -29), today
		}
		p.From, p.To = f, t.AddDate(0, 0, 1)
	default:
		p.Key = "month"
		p.From, p.To = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc), today.AddDate(0, 0, 1)
	}
	if p.Compare == "previous_period" {
		d := p.To.Sub(p.From)
		p.PrevFrom, p.PrevTo = p.From.Add(-d), p.From
	} else {
		p.Compare = "previous_year"
		p.PrevFrom, p.PrevTo = p.From.AddDate(-1, 0, 0), p.To.AddDate(-1, 0, 0)
	}
	for _, o := range gooPeriods {
		if o.Value == p.Key {
			p.Label = o.Label
		}
	}
	return p
}

func (p gooPeriod) RangeText() string {
	return p.From.Format("Jan 2, 2006") + " - " + p.To.AddDate(0, 0, -1).Format("Jan 2, 2006")
}

func (p gooPeriod) PrevText() string {
	return p.PrevFrom.Format("Jan 2, 2006") + " - " + p.PrevTo.AddDate(0, 0, -1).Format("Jan 2, 2006")
}

// gooPerf are the overview's performance indicators for a range.
type gooPerf struct {
	TotalSales, NetSales, AOV, Returns, GrossDiscounted, Tax, Shipping float64
	Orders, ItemsSold, DiscountedOrders                                int
}

func (s *Server) gooPerformance(ctx context.Context, from, to time.Time) gooPerf {
	t := s.gooTotals(ctx, from, to)
	p := gooPerf{Orders: t.Orders, ItemsSold: t.Items - t.RefundedItems, Returns: t.Refunds, GrossDiscounted: t.Discount, Tax: t.Tax + t.ShippingTax, Shipping: t.Shipping}
	p.NetSales = t.Gross - t.Tax - t.ShippingTax - t.Shipping - t.Refunds
	p.TotalSales = t.Gross - t.Refunds
	if p.Orders > 0 {
		p.AOV = p.NetSales / float64(p.Orders)
	}
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_orders WHERE type = 'shop_order' AND status = ANY($1) AND date_created >= $2 AND date_created < $3
		AND discount_total > 0`, gooPaidList, from, to).Scan(&p.DiscountedOrders)
	return p
}

type gooCard struct {
	Key, Label string
	Value      template.HTML
	Prev       template.HTML
	Delta      string
	Up         bool
	Down       bool
	Raw        float64
	URL        string
}

type gooLeader struct {
	Title   string
	Headers []string
	Rows    [][]template.HTML
}

type gooAnalyticsData struct {
	Path, Title string
	Period      gooPeriod
	Periods     []gooOpt
	Cards       []gooCard
	Chart       gooChart
	ChartLabel  string
	Leaders     []gooLeader
	Table       *gooTable
	CSVURL      string
	Query       string
}

var gooAnalyticsPages = []gooOpt{{"", "Overview"}, {"products", "Products"}, {"revenue", "Revenue"}, {"orders", "Orders"}, {"variations", "Variations"},
	{"categories", "Categories"}, {"coupons", "Coupons"}, {"taxes", "Taxes"}, {"downloads", "Downloads"}, {"stock", "Stock"}, {"customers", "Customers"}}

func gooDelta(cur, prev float64) (string, bool, bool) {
	if prev == 0 {
		if cur == 0 {
			return "0%", false, false
		}
		return "N/A", cur > 0, false
	}
	d := (cur - prev) / math.Abs(prev) * 100
	return fmt.Sprintf("%+.0f%%", d), d > 0, d < 0
}

func (s *Server) gooAdminAnalytics(c *gin.Context) {
	ctx := c.Request.Context()
	path := strings.Trim(c.Query("path"), "/")
	path = strings.TrimPrefix(path, "analytics/")
	d := &gooAnalyticsData{Path: path, Period: s.gooAnalyticsPeriod(c), Periods: gooPeriods}
	valid := false
	for _, p := range gooAnalyticsPages {
		if p.Value == path {
			valid, d.Title = true, p.Label
		}
	}
	if !valid {
		d.Path, d.Title = "", "Overview"
	}
	q := "period=" + d.Period.Key + "&compare=" + d.Period.Compare
	if d.Period.Key == "custom" {
		q += "&after=" + d.Period.After + "&before=" + d.Period.Before
	}
	d.Query = q
	d.CSVURL = gooAdminURL("analytics", "path", d.Path) + "&" + q + "&download=csv"
	from, to := d.Period.From, d.Period.To
	cur, prev := s.gooPerformance(ctx, from, to), s.gooPerformance(ctx, d.Period.PrevFrom, d.Period.PrevTo)
	money := func(key, label string, a, b float64, link string) gooCard {
		delta, up, down := gooDelta(a, b)
		return gooCard{Key: key, Label: label, Value: s.gooPrice(a), Prev: s.gooPrice(b), Delta: delta, Up: up, Down: down, Raw: a, URL: link}
	}
	count := func(key, label string, a, b int, link string) gooCard {
		delta, up, down := gooDelta(float64(a), float64(b))
		return gooCard{Key: key, Label: label, Value: template.HTML(formatNumber(a)), Prev: template.HTML(formatNumber(b)), Delta: delta, Up: up, Down: down, Raw: float64(a), URL: link}
	}
	an := func(p string) string { return gooAdminURL("analytics", "path", p) + "&" + q }
	ivs := gooRange{From: from, To: to, ByMonth: to.Sub(from) > 92*24*time.Hour}.Intervals()
	tbl := &gooTable{Empty: "No data for the selected date range"}
	switch d.Path {
	case "":
		d.Cards = []gooCard{
			money("total_sales", "Total sales", cur.TotalSales, prev.TotalSales, an("revenue")),
			money("net_revenue", "Net sales", cur.NetSales, prev.NetSales, an("revenue")),
			count("orders_count", "Orders", cur.Orders, prev.Orders, an("orders")),
			money("avg_order_value", "Average order value", cur.AOV, prev.AOV, an("orders")),
			count("items_sold", "Products sold", cur.ItemsSold, prev.ItemsSold, an("products")),
			money("refunds", "Returns", cur.Returns, prev.Returns, an("revenue")),
			count("discounted_orders", "Discounted orders", cur.DiscountedOrders, prev.DiscountedOrders, an("coupons")),
			money("gross_discount", "Gross discounted", cur.GrossDiscounted, prev.GrossDiscounted, an("coupons")),
			money("total_tax", "Total tax", cur.Tax, prev.Tax, an("taxes")),
			money("shipping", "Shipping", cur.Shipping, prev.Shipping, an("revenue")),
		}
		net := s.gooSeries(ctx, ivs, "o.total - o.total_tax - o.shipping_total", "")
		d.Chart = gooBars(ivs, net, nil, func(i int) string { return ivs[i].Label + ": " + s.gooPricePlain(net[i], s.gooCurrency()) })
		d.ChartLabel = "Net sales"
		d.Leaders = s.gooLeaderboards(ctx, from, to)
		tbl = nil
	case "products", "variations":
		col := "i.product_id"
		cond := ""
		if d.Path == "variations" {
			col, cond = "i.variation_id", " AND i.variation_id <> 0"
		}
		tbl.Headers = []string{"Product title", "SKU", "Items sold", "Net sales", "Orders", "Status", "Stock"}
		var items int
		var sales float64
		var orders int
		rows, err := s.store.Pool.Query(ctx, `SELECT `+col+`, max(i.name), sum(i.quantity)::float8, sum(i.total)::float8, count(DISTINCT o.id) FROM goo_order_items i
			JOIN goo_orders o ON o.id = i.order_id WHERE i.type = 'line_item' AND o.type = 'shop_order' AND o.status = ANY($1) AND o.date_created >= $2 AND o.date_created < $3`+cond+`
			GROUP BY 1 ORDER BY 4 DESC`, gooPaidList, from, to)
		if err == nil {
			type row struct {
				id     int64
				name   string
				qty    float64
				amt    float64
				orders int
			}
			var list []row
			for rows.Next() {
				var r row
				if rows.Scan(&r.id, &r.name, &r.qty, &r.amt, &r.orders) == nil {
					list = append(list, r)
				}
			}
			rows.Close()
			for _, r := range list {
				sku, status, stock := "", "", "N/A"
				if p, err := s.gooGetProduct(ctx, r.id); err == nil {
					sku = p.SKU
					status = map[string]string{"instock": "In stock", "outofstock": "Out of stock", "onbackorder": "On backorder"}[p.StockStatus]
					if p.ManageStock && p.StockQuantity != nil {
						stock = strconv.Itoa(*p.StockQuantity)
					}
				}
				items += int(r.qty)
				sales += r.amt
				orders += r.orders
				tbl.add([]template.HTML{gooH(r.name), gooH(sku), gooH(strconv.Itoa(int(r.qty))), s.gooPrice(r.amt), gooH(strconv.Itoa(r.orders)), gooH(status), gooH(stock)},
					[]string{r.name, sku, strconv.Itoa(int(r.qty)), s.gooFmtDec(r.amt), strconv.Itoa(r.orders), status, stock})
			}
		}
		d.Cards = []gooCard{{Label: "Items sold", Value: gooH(strconv.Itoa(items))}, {Label: "Net sales", Value: s.gooPrice(sales)}, {Label: "Orders", Value: gooH(strconv.Itoa(orders))}}
	case "revenue":
		tbl.Headers = []string{"Date", "Orders", "Gross sales", "Returns", "Coupons", "Net sales", "Taxes", "Shipping", "Total sales"}
		ivDays := gooRange{From: from, To: to}.Intervals()
		for i := len(ivDays) - 1; i >= 0; i-- {
			iv := ivDays[i]
			t := s.gooTotals(ctx, iv.Start, iv.End)
			gross := t.Gross - t.Tax - t.ShippingTax - t.Shipping + t.Discount
			net := t.Gross - t.Tax - t.ShippingTax - t.Shipping - t.Refunds
			total := t.Gross - t.Refunds
			date := iv.Start.Format("January 2, 2006")
			tbl.add([]template.HTML{gooH(date), gooH(strconv.Itoa(t.Orders)), s.gooPrice(gross), s.gooPrice(t.Refunds), s.gooPrice(t.Discount), s.gooPrice(net), s.gooPrice(t.Tax + t.ShippingTax), s.gooPrice(t.Shipping), s.gooPrice(total)},
				[]string{iv.Start.Format("2006-01-02"), strconv.Itoa(t.Orders), s.gooFmtDec(gross), s.gooFmtDec(t.Refunds), s.gooFmtDec(t.Discount), s.gooFmtDec(net), s.gooFmtDec(t.Tax + t.ShippingTax), s.gooFmtDec(t.Shipping), s.gooFmtDec(total)})
		}
		d.Cards = []gooCard{money("total_sales", "Total sales", cur.TotalSales, prev.TotalSales, ""), money("net_revenue", "Net sales", cur.NetSales, prev.NetSales, ""),
			money("refunds", "Returns", cur.Returns, prev.Returns, ""), money("coupons", "Coupons", cur.GrossDiscounted, prev.GrossDiscounted, ""),
			money("taxes", "Taxes", cur.Tax, prev.Tax, ""), money("shipping", "Shipping", cur.Shipping, prev.Shipping, "")}
		net := s.gooSeries(ctx, ivs, "o.total - o.total_tax - o.shipping_total", "")
		d.Chart = gooBars(ivs, net, nil, func(i int) string { return ivs[i].Label + ": " + s.gooPricePlain(net[i], s.gooCurrency()) })
		d.ChartLabel = "Net sales"
	case "orders":
		tbl.Headers = []string{"Date", "Order #", "Status", "Customer", "Customer type", "Product(s)", "Items sold", "Coupon(s)", "Net sales"}
		after, before := from, to
		orders, _, _ := s.gooQueryOrders(ctx, gooOrderQuery{Status: gooPaidList, After: &after, Before: &before, PerPage: 500, Page: 1})
		for _, o := range orders {
			var products, coupons []string
			for _, it := range o.ItemsOf("line_item") {
				products = append(products, it.Name)
			}
			for _, it := range o.ItemsOf("coupon") {
				coupons = append(coupons, it.Name)
			}
			typ := "new"
			var earlier bool
			_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM goo_orders WHERE type = 'shop_order' AND id <> $1 AND date_created < $2 AND status = ANY($3)
				AND ((customer_id <> 0 AND customer_id = $4) OR (billing_email <> '' AND billing_email = $5)))`, o.ID, o.DateCreated, gooPaidList, o.CustomerID, strings.ToLower(o.Billing["email"])).Scan(&earlier)
			if earlier {
				typ = "returning"
			}
			net := o.Total - o.TotalTax - o.ShippingTotal - s.gooTotalRefunded(ctx, o.ID)
			name := o.BillingName()
			tbl.add([]template.HTML{gooH(o.DateCreated.In(s.loc()).Format("January 2, 2006")), template.HTML(`<a href="` + gooAdminURL("orders", "action", "edit", "id", itoa64(o.ID)) + `">` + o.Number() + `</a>`),
				gooH(gooStatusLabel(o.Status)), gooH(name), gooH(typ), gooH(strings.Join(products, ", ")), gooH(strconv.Itoa(o.ItemCount())), gooH(strings.Join(coupons, ", ")), s.gooPriceIn(net, o.Currency)},
				[]string{o.DateCreated.In(s.loc()).Format("2006-01-02"), o.Number(), gooStatusLabel(o.Status), name, typ, strings.Join(products, ", "), strconv.Itoa(o.ItemCount()), strings.Join(coupons, ", "), s.gooFmtDec(net)})
		}
		d.Cards = []gooCard{count("orders_count", "Orders", cur.Orders, prev.Orders, ""), money("net_revenue", "Net sales", cur.NetSales, prev.NetSales, ""),
			money("avg_order_value", "Average order value", cur.AOV, prev.AOV, ""), count("items_sold", "Average items per order", cur.ItemsSold, prev.ItemsSold, "")}
		if cur.Orders > 0 {
			d.Cards[3].Value = gooH(strconv.FormatFloat(float64(cur.ItemsSold)/float64(cur.Orders), 'f', 1, 64))
		}
		cnt := s.gooSeries(ctx, ivs, "1", "")
		d.Chart = gooBars(ivs, cnt, nil, func(i int) string { return fmt.Sprintf("%s: %d orders", ivs[i].Label, int(cnt[i])) })
		d.ChartLabel = "Orders"
	case "categories":
		tbl.Headers = []string{"Category", "Items sold", "Net sales", "Products", "Orders"}
		rows, err := s.store.Pool.Query(ctx, `SELECT t.name, sum(i.quantity)::float8, sum(i.total)::float8, count(DISTINCT i.product_id), count(DISTINCT o.id) FROM goo_order_items i
			JOIN goo_orders o ON o.id = i.order_id JOIN post_terms pt ON pt.post_id = i.product_id JOIN terms t ON t.id = pt.term_id AND t.taxonomy = 'product_cat'
			WHERE i.type = 'line_item' AND o.type = 'shop_order' AND o.status = ANY($1) AND o.date_created >= $2 AND o.date_created < $3 GROUP BY t.id, t.name ORDER BY 3 DESC`, gooPaidList, from, to)
		var items int
		var sales float64
		if err == nil {
			for rows.Next() {
				var name string
				var qty, amt float64
				var prods, orders int
				if rows.Scan(&name, &qty, &amt, &prods, &orders) == nil {
					items += int(qty)
					sales += amt
					tbl.add([]template.HTML{gooH(name), gooH(strconv.Itoa(int(qty))), s.gooPrice(amt), gooH(strconv.Itoa(prods)), gooH(strconv.Itoa(orders))},
						[]string{name, strconv.Itoa(int(qty)), s.gooFmtDec(amt), strconv.Itoa(prods), strconv.Itoa(orders)})
				}
			}
			rows.Close()
		}
		d.Cards = []gooCard{{Label: "Items sold", Value: gooH(strconv.Itoa(items))}, {Label: "Net sales", Value: s.gooPrice(sales)}}
	case "coupons":
		tbl.Headers = []string{"Coupon code", "Orders", "Amount discounted", "Created", "Expires", "Type"}
		rows, err := s.store.Pool.Query(ctx, `SELECT lower(i.name), count(DISTINCT o.id), coalesce(sum(i.total),0)::float8 FROM goo_order_items i JOIN goo_orders o ON o.id = i.order_id
			WHERE i.type = 'coupon' AND o.type = 'shop_order' AND o.status = ANY($1) AND o.date_created >= $2 AND o.date_created < $3 GROUP BY 1 ORDER BY 3 DESC`, gooPaidList, from, to)
		var orders int
		var amount float64
		if err == nil {
			type row struct {
				code string
				n    int
				amt  float64
			}
			var list []row
			for rows.Next() {
				var r row
				if rows.Scan(&r.code, &r.n, &r.amt) == nil {
					list = append(list, r)
				}
			}
			rows.Close()
			for _, r := range list {
				created, expires, typ := "", "N/A", ""
				if cp, err := s.gooCouponByCode(ctx, r.code); err == nil {
					created = cp.DateCreated.In(s.loc()).Format("January 2, 2006")
					if cp.DateExpires != nil {
						expires = cp.DateExpires.In(s.loc()).Format("January 2, 2006")
					}
					typ = gooOptLabel(gooCouponTypes, cp.DiscountType)
				}
				orders += r.n
				amount += r.amt
				tbl.add([]template.HTML{template.HTML("<code>" + template.HTMLEscapeString(r.code) + "</code>"), gooH(strconv.Itoa(r.n)), s.gooPrice(r.amt), gooH(created), gooH(expires), gooH(typ)},
					[]string{r.code, strconv.Itoa(r.n), s.gooFmtDec(r.amt), created, expires, typ})
			}
		}
		d.Cards = []gooCard{{Label: "Discounted orders", Value: gooH(strconv.Itoa(orders))}, {Label: "Amount", Value: s.gooPrice(amount)}}
	case "taxes":
		tbl.Headers = []string{"Tax code", "Rate", "Total tax", "Order tax", "Shipping tax", "Orders"}
		rows, err := s.store.Pool.Query(ctx, `SELECT i.name, max(coalesce((i.meta->>'rate_percent')::numeric, 0))::float8,
			sum(coalesce((i.meta->>'tax_amount')::numeric,0))::float8, sum(coalesce((i.meta->>'shipping_tax_amount')::numeric,0))::float8, count(DISTINCT o.id)
			FROM goo_order_items i JOIN goo_orders o ON o.id = i.order_id WHERE i.type = 'tax' AND o.type = 'shop_order' AND o.status = ANY($1)
			AND o.date_created >= $2 AND o.date_created < $3 GROUP BY i.name ORDER BY 3 DESC`, gooPaidList, from, to)
		var total, order, ship float64
		var orders int
		if err == nil {
			for rows.Next() {
				var code string
				var rate, ot, st float64
				var n int
				if rows.Scan(&code, &rate, &ot, &st, &n) == nil {
					total += ot + st
					order += ot
					ship += st
					orders += n
					rs := strconv.FormatFloat(rate, 'f', -1, 64) + "%"
					tbl.add([]template.HTML{gooH(code), gooH(rs), s.gooPrice(ot + st), s.gooPrice(ot), s.gooPrice(st), gooH(strconv.Itoa(n))},
						[]string{code, rs, s.gooFmtDec(ot + st), s.gooFmtDec(ot), s.gooFmtDec(st), strconv.Itoa(n)})
				}
			}
			rows.Close()
		}
		d.Cards = []gooCard{{Label: "Total tax", Value: s.gooPrice(total)}, {Label: "Order tax", Value: s.gooPrice(order)}, {Label: "Shipping tax", Value: s.gooPrice(ship)}, {Label: "Orders", Value: gooH(strconv.Itoa(orders))}}
	case "downloads":
		tbl.Headers = []string{"Date", "Product title", "File name", "Order #", "Username", "Downloads"}
		rows, err := s.store.Pool.Query(ctx, `SELECT dp.access_granted, dp.product_id, dp.download_id, dp.order_id, coalesce(u.login, dp.user_email), dp.download_count
			FROM goo_download_permissions dp LEFT JOIN users u ON u.id = dp.user_id WHERE dp.access_granted >= $1 AND dp.access_granted < $2 ORDER BY dp.access_granted DESC LIMIT 500`, from, to)
		var downloads int
		if err == nil {
			type row struct {
				at         time.Time
				pid, order int64
				dl, user   string
				n          int
			}
			var list []row
			for rows.Next() {
				var r row
				if rows.Scan(&r.at, &r.pid, &r.dl, &r.order, &r.user, &r.n) == nil {
					list = append(list, r)
				}
			}
			rows.Close()
			for _, r := range list {
				title, file := "", r.dl
				if p, err := s.gooGetProduct(ctx, r.pid); err == nil {
					title = s.gooProductTitle(ctx, p)
					for _, f := range p.Downloads {
						if f.ID == r.dl {
							file = f.Name
						}
					}
				}
				downloads += r.n
				tbl.add([]template.HTML{gooH(r.at.In(s.loc()).Format("January 2, 2006")), gooH(title), gooH(file), template.HTML(`<a href="` + gooAdminURL("orders", "action", "edit", "id", itoa64(r.order)) + `">` + itoa64(r.order) + `</a>`), gooH(r.user), gooH(strconv.Itoa(r.n))},
					[]string{r.at.In(s.loc()).Format("2006-01-02"), title, file, itoa64(r.order), r.user, strconv.Itoa(r.n)})
			}
		}
		d.Cards = []gooCard{{Label: "Downloads", Value: gooH(strconv.Itoa(downloads))}}
	case "stock":
		tbl.Headers = []string{"Product / Variation", "SKU", "Status", "Stock"}
		rows, err := s.store.Pool.Query(ctx, `SELECT po.id, po.title, p.sku, p.stock_status, p.manage_stock, p.stock_quantity FROM goo_products p JOIN posts po ON po.id = p.post_id
			WHERE po.status = 'publish' AND po.type IN ('product','product_variation') AND p.type <> 'variable' ORDER BY po.title LIMIT 1000`)
		counts := map[string]int{}
		if err == nil {
			for rows.Next() {
				var id int64
				var title, sku, st string
				var manage bool
				var qty *int
				if rows.Scan(&id, &title, &sku, &st, &manage, &qty) != nil {
					continue
				}
				stock := "N/A"
				if manage && qty != nil {
					stock = strconv.Itoa(*qty)
					if *qty <= s.gooOptInt("notify_low_stock_amount") && st == "instock" {
						counts["lowstock"]++
					}
				}
				counts[st]++
				label := map[string]string{"instock": "In stock", "outofstock": "Out of stock", "onbackorder": "On backorder"}[st]
				tbl.add([]template.HTML{template.HTML(`<a href="/wp-admin/post?post=` + itoa64(id) + `&amp;action=edit">` + template.HTMLEscapeString(title) + `</a>`), gooH(sku), gooH(label), gooH(stock)},
					[]string{title, sku, label, stock})
			}
			rows.Close()
		}
		d.Cards = []gooCard{{Label: "Products", Value: gooH(strconv.Itoa(len(tbl.Rows)))}, {Label: "Out of stock", Value: gooH(strconv.Itoa(counts["outofstock"]))},
			{Label: "Low stock", Value: gooH(strconv.Itoa(counts["lowstock"]))}, {Label: "On backorder", Value: gooH(strconv.Itoa(counts["onbackorder"]))}, {Label: "In stock", Value: gooH(strconv.Itoa(counts["instock"]))}}
	case "customers":
		tbl.Headers = []string{"Name", "Username", "Last active", "Date registered", "Email", "Orders", "Total spend", "AOV", "Country / Region", "City", "Region", "Postal code"}
		var spend float64
		var orders, n int
		for _, r := range s.gooCustomers(ctx) {
			n++
			spend += r.Spend
			orders += r.Orders
			last, reg := "", ""
			if !r.LastActive.IsZero() {
				last = r.LastActive.In(s.loc()).Format("January 2, 2006")
			}
			if !r.Registered.IsZero() {
				reg = r.Registered.In(s.loc()).Format("January 2, 2006")
			}
			region := gooStateName(r.Country, r.Region)
			tbl.add([]template.HTML{gooH(r.Name), gooH(r.Username), gooH(last), gooH(reg), gooH(r.Email), gooH(strconv.Itoa(r.Orders)), s.gooPrice(r.Spend), s.gooPrice(r.AOV),
				gooH(gooCountryName(r.Country)), gooH(r.City), gooH(region), gooH(r.Postcode)},
				[]string{r.Name, r.Username, last, reg, r.Email, strconv.Itoa(r.Orders), s.gooFmtDec(r.Spend), s.gooFmtDec(r.AOV), gooCountryName(r.Country), r.City, region, r.Postcode})
		}
		aov := 0.0
		if orders > 0 {
			aov = spend / float64(orders)
		}
		d.Cards = []gooCard{{Label: "Customers", Value: gooH(strconv.Itoa(n))}, {Label: "Average orders", Value: gooH(fmt.Sprintf("%.1f", float64(orders)/math.Max(1, float64(n))))},
			{Label: "Average lifetime spend", Value: s.gooPrice(spend / math.Max(1, float64(n)))}, {Label: "Average order value", Value: s.gooPrice(aov)}}
	}
	if tbl != nil {
		d.Table = tbl
		if c.Query("download") == "csv" {
			name := d.Path
			gooCSV(c, "wc-"+name+"-report-export.csv", tbl.Headers, tbl.CSV)
			return
		}
	}
	screen := "goocommerce?page=analytics"
	if d.Path != "" {
		screen += "&path=" + d.Path
	}
	s.gooStoreRender(c, screen, "wc-admin", "Analytics &rsaquo; "+d.Title, "admin/goo-analytics", d)
}

func gooH(v string) template.HTML { return template.HTML(template.HTMLEscapeString(v)) }

// gooLeaderboards are the Overview's top products, categories, coupons and customers.
func (s *Server) gooLeaderboards(ctx context.Context, from, to time.Time) []gooLeader {
	var out []gooLeader
	add := func(title string, headers []string, sql string) {
		lb := gooLeader{Title: title, Headers: headers}
		rows, err := s.store.Pool.Query(ctx, sql, gooPaidList, from, to)
		if err == nil {
			for rows.Next() {
				var name string
				var n, amt float64
				if rows.Scan(&name, &n, &amt) == nil {
					lb.Rows = append(lb.Rows, []template.HTML{gooH(name), gooH(strconv.Itoa(int(n))), s.gooPrice(amt)})
				}
			}
			rows.Close()
		}
		out = append(out, lb)
	}
	base := ` FROM goo_order_items i JOIN goo_orders o ON o.id = i.order_id WHERE o.type = 'shop_order' AND o.status = ANY($1) AND o.date_created >= $2 AND o.date_created < $3`
	add("Top products - Items sold", []string{"Product", "Items sold", "Net sales"},
		`SELECT max(i.name), sum(i.quantity)::float8, sum(i.total)::float8`+base+` AND i.type = 'line_item' GROUP BY i.product_id ORDER BY 2 DESC LIMIT 5`)
	lb := gooLeader{Title: "Top categories - Items sold", Headers: []string{"Category", "Items sold", "Net sales"}}
	rows, err := s.store.Pool.Query(ctx, `SELECT t.name, sum(i.quantity)::float8, sum(i.total)::float8 FROM goo_order_items i JOIN goo_orders o ON o.id = i.order_id
		JOIN post_terms pt ON pt.post_id = i.product_id JOIN terms t ON t.id = pt.term_id AND t.taxonomy = 'product_cat'
		WHERE i.type = 'line_item' AND o.type = 'shop_order' AND o.status = ANY($1) AND o.date_created >= $2 AND o.date_created < $3 GROUP BY t.id, t.name ORDER BY 2 DESC LIMIT 5`, gooPaidList, from, to)
	if err == nil {
		for rows.Next() {
			var name string
			var n, amt float64
			if rows.Scan(&name, &n, &amt) == nil {
				lb.Rows = append(lb.Rows, []template.HTML{gooH(name), gooH(strconv.Itoa(int(n))), s.gooPrice(amt)})
			}
		}
		rows.Close()
	}
	out = append(out, lb)
	add("Top coupons - Number of orders", []string{"Coupon code", "Orders", "Amount discounted"},
		`SELECT lower(i.name), count(DISTINCT o.id)::float8, sum(i.total)::float8`+base+` AND i.type = 'coupon' GROUP BY 1 ORDER BY 2 DESC LIMIT 5`)
	lb = gooLeader{Title: "Top customers - Total spend", Headers: []string{"Customer name", "Orders", "Total spend"}}
	rows, err = s.store.Pool.Query(ctx, `SELECT max(trim(coalesce(billing->>'first_name','') || ' ' || coalesce(billing->>'last_name',''))), count(*)::float8, sum(total)::float8
		FROM goo_orders WHERE type = 'shop_order' AND status = ANY($1) AND date_created >= $2 AND date_created < $3
		GROUP BY CASE WHEN customer_id <> 0 THEN customer_id::text ELSE billing_email END ORDER BY 3 DESC LIMIT 5`, gooPaidList, from, to)
	if err == nil {
		for rows.Next() {
			var name string
			var n, amt float64
			if rows.Scan(&name, &n, &amt) == nil {
				lb.Rows = append(lb.Rows, []template.HTML{gooH(name), gooH(strconv.Itoa(int(n))), s.gooPrice(amt)})
			}
		}
		rows.Close()
	}
	out = append(out, lb)
	return out
}
