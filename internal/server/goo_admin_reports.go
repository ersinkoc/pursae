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

	"wpgo/internal/i18n"
)

// Goocommerce › Reports (page=reports&tab=orders|customers|stock&report=…): WooCommerce's legacy
// reports with pure CSS / SVG charts. Sales count the paid statuses (processing, completed, on-hold).

// gooInterval is a bucket of a report chart.
type gooInterval struct {
	Start, End time.Time
	Label      string
}

// gooBar is a chart bar (heights in %).
type gooBar struct {
	Label  string
	Title  string
	Value  float64
	Height float64
	Second float64 // a second series' height (orders)
}

// gooChart is a bar chart rendered as SVG.
type gooChart struct {
	Bars []gooBar
}

// SVG draws the chart: bars for the main series and a line of dots for the second.
func (ch gooChart) SVG() template.HTML {
	n := len(ch.Bars)
	if n == 0 {
		return ""
	}
	w, h := 900.0, 240.0
	bw := w / float64(n)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="goo-chart" viewBox="0 0 %.0f %.0f" preserveAspectRatio="none" role="img" aria-label="Chart">`, w, h+24)
	for i := 0; i <= 4; i++ {
		y := h - h*float64(i)/4
		fmt.Fprintf(&b, `<line x1="0" x2="%.0f" y1="%.1f" y2="%.1f" class="goo-grid"/>`, w, y, y)
	}
	var pts []string
	for i, bar := range ch.Bars {
		x := float64(i) * bw
		bh := h * bar.Height / 100
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" class="goo-bar"><title>%s</title></rect>`,
			x+bw*0.15, h-bh, bw*0.7, bh, template.HTMLEscapeString(bar.Title))
		if bar.Second > 0 || len(pts) > 0 {
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", x+bw/2, h-h*bar.Second/100))
		}
		step := max(1, n/12)
		if i%step == 0 {
			fmt.Fprintf(&b, `<text x="%.1f" y="%.0f" class="goo-axis">%s</text>`, x+bw/2, h+16, template.HTMLEscapeString(bar.Label))
		}
	}
	if len(pts) > 1 {
		fmt.Fprintf(&b, `<polyline points="%s" class="goo-line"/>`, strings.Join(pts, " "))
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// gooRange is the date range of a report: [From, To).
type gooRange struct {
	Key, Label string
	From, To   time.Time
	ByMonth    bool
	Start, End string // custom inputs
}

// gooReportRange reads range=year|last_month|month|7day|custom (WC_Admin_Report::calculate_current_range).
func (s *Server) gooReportRange(c *gin.Context) gooRange {
	loc := s.loc()
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	r := gooRange{Key: c.DefaultQuery("range", "7day")}
	switch r.Key {
	case "year":
		r.From, r.To, r.ByMonth = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, loc), today.AddDate(0, 0, 1), true
		r.Label = "Year"
	case "last_month":
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
		r.From, r.To, r.Label = first.AddDate(0, -1, 0), first, "Last month"
	case "month":
		r.From, r.To, r.Label = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc), today.AddDate(0, 0, 1), "This month"
	case "custom":
		r.Start, r.End = c.Query("start_date"), c.Query("end_date")
		f, e1 := time.ParseInLocation("2006-01-02", r.Start, loc)
		t, e2 := time.ParseInLocation("2006-01-02", r.End, loc)
		if e1 != nil || e2 != nil || t.Before(f) {
			f, t = today.AddDate(0, 0, -6), today
		}
		r.From, r.To, r.Label = f, t.AddDate(0, 0, 1), "Custom"
		r.ByMonth = r.To.Sub(r.From) > 92*24*time.Hour
	default:
		r.Key = "7day"
		r.From, r.To, r.Label = today.AddDate(0, 0, -6), today.AddDate(0, 0, 1), "Last 7 days"
	}
	return r
}

func (r gooRange) Intervals() []gooInterval {
	var out []gooInterval
	if r.ByMonth {
		for t := time.Date(r.From.Year(), r.From.Month(), 1, 0, 0, 0, 0, r.From.Location()); t.Before(r.To); t = t.AddDate(0, 1, 0) {
			out = append(out, gooInterval{Start: t, End: t.AddDate(0, 1, 0), Label: t.Format("Jan")})
		}
		return out
	}
	for t := r.From; t.Before(r.To); t = t.AddDate(0, 0, 1) {
		out = append(out, gooInterval{Start: t, End: t.AddDate(0, 0, 1), Label: t.Format("Jan 2")})
	}
	return out
}

// gooSeries sums a value of the paid orders per interval; expr is an SQL expression over goo_orders o.
func (s *Server) gooSeries(ctx context.Context, ivs []gooInterval, expr string, extraWhere string) []float64 {
	out := make([]float64, len(ivs))
	if len(ivs) == 0 {
		return out
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT o.date_created, (`+expr+`)::float8 FROM goo_orders o WHERE o.type = 'shop_order' AND o.status = ANY($1)
		AND o.date_created >= $2 AND o.date_created < $3`+extraWhere, gooPaidList, ivs[0].Start, ivs[len(ivs)-1].End)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var at time.Time
		var v float64
		if rows.Scan(&at, &v) != nil {
			continue
		}
		for i, iv := range ivs {
			if !at.Before(iv.Start) && at.Before(iv.End) {
				out[i] += v
				break
			}
		}
	}
	return out
}

func gooBars(ivs []gooInterval, main, second []float64, title func(i int) string) gooChart {
	var mx, mx2 float64
	for i := range main {
		mx = math.Max(mx, main[i])
		if second != nil {
			mx2 = math.Max(mx2, second[i])
		}
	}
	var ch gooChart
	for i, iv := range ivs {
		b := gooBar{Label: iv.Label, Value: main[i], Title: title(i)}
		if mx > 0 {
			b.Height = main[i] / mx * 100
		}
		if second != nil && mx2 > 0 {
			b.Second = second[i] / mx2 * 100
		}
		ch.Bars = append(ch.Bars, b)
	}
	return ch
}

// gooLegend is a line of a report's legend.
type gooLegend struct {
	Value template.HTML
	Label string
	Color string
}

// gooTable is a report / analytics table.
type gooTable struct {
	Headers []string
	Rows    [][]template.HTML
	CSV     [][]string
	Empty   string
}

func (t *gooTable) add(html []template.HTML, csv []string) {
	t.Rows = append(t.Rows, html)
	t.CSV = append(t.CSV, csv)
}

type gooReportsData struct {
	Tab, Report string
	Tabs        []gooOpt
	Reports     []gooView
	Range       gooRange
	Ranges      []gooView
	Legend      []gooLegend
	Chart       gooChart
	HasChart    bool
	Table       *gooTable
	Title       string
	BaseURL     string
	Note        string
	Products    []gooOpt
	ProductID   string
}

var gooReportDefs = map[string][]gooOpt{
	"orders":    {{"sales_by_date", "Sales by date"}, {"sales_by_product", "Sales by product"}, {"sales_by_category", "Sales by category"}, {"coupon_usage", "Coupons by date"}},
	"customers": {{"customers", "Customers vs. guests"}, {"customer_list", "Customer list"}},
	"stock":     {{"low_in_stock", "Low in stock"}, {"out_of_stock", "Out of stock"}, {"most_stocked", "Most stocked"}},
}

func (s *Server) gooAdminReports(c *gin.Context) {
	ctx := c.Request.Context()
	tab := c.DefaultQuery("tab", "orders")
	defs, ok := gooReportDefs[tab]
	if !ok {
		tab, defs = "orders", gooReportDefs["orders"]
	}
	report := c.DefaultQuery("report", defs[0].Value)
	valid := false
	for _, d := range defs {
		if d.Value == report {
			valid = true
		}
	}
	if !valid {
		report = defs[0].Value
	}
	d := &gooReportsData{Tab: tab, Report: report, Tabs: []gooOpt{{"orders", "Orders"}, {"customers", "Customers"}, {"stock", "Stock"}}, Range: s.gooReportRange(c)}
	for _, rd := range defs {
		d.Reports = append(d.Reports, gooView{Label: rd.Label, URL: gooAdminURL("reports", "tab", tab, "report", rd.Value), Current: rd.Value == report})
		if rd.Value == report {
			d.Title = rd.Label
		}
	}
	d.BaseURL = gooAdminURL("reports", "tab", tab, "report", report)
	for _, rg := range []gooOpt{{"year", "Year"}, {"last_month", "Last month"}, {"month", "This month"}, {"7day", "Last 7 days"}} {
		d.Ranges = append(d.Ranges, gooView{Label: rg.Label, URL: d.BaseURL + "&range=" + rg.Value, Current: d.Range.Key == rg.Value})
	}
	ivs := d.Range.Intervals()
	s.gooL10nIntervals(c, ivs, d.Range.ByMonth)
	from, to := d.Range.From, d.Range.To
	cur := s.gooCurrency()
	price := func(v float64) template.HTML { return s.gooPrice(v) }
	days := math.Max(1, math.Round(to.Sub(from).Hours()/24))
	switch report {
	case "sales_by_date":
		t := s.gooTotals(ctx, from, to)
		avg := (t.Gross - t.Refunds) / days
		per := "daily"
		if d.Range.ByMonth {
			avg = (t.Gross - t.Refunds) / math.Max(1, float64(len(ivs)))
			per = "monthly"
		}
		var refundOrders int
		_ = s.store.Pool.QueryRow(ctx, `SELECT count(DISTINCT parent_id) FROM goo_orders WHERE type = 'shop_order_refund' AND date_created >= $1 AND date_created < $2`, from, to).Scan(&refundOrders)
		d.Legend = []gooLegend{
			{price(t.Gross - t.Refunds), "gross sales in this period", "#b1d4ea"},
			{price(t.Net), "net sales in this period", "#3498db"},
			{price(avg), "average " + per + " sales", "#b1d4ea"},
			{template.HTML(strconv.Itoa(t.Orders)), "orders placed", "#dbe1e3"},
			{template.HTML(strconv.Itoa(t.Items)), "items purchased", "#ecf0f1"},
			{template.HTML(string(price(t.Refunds)) + " (" + strconv.Itoa(t.RefundedItems) + " items)"), "refunded " + plural(refundOrders, "1 order", "%d orders"), "#e74c3c"},
			{price(t.Shipping), "charged for shipping", "#5cc488"},
			{price(t.Discount), "worth of coupons used", "#f1c40f"},
		}
		sales := s.gooSeries(ctx, ivs, "o.total", "")
		orders := s.gooSeries(ctx, ivs, "1", "")
		d.Chart = gooBars(ivs, sales, orders, func(i int) string {
			format := "%1$s: %2$s, %3$d orders"
			if int(orders[i]) == 1 {
				format = "%1$s: %2$s, %3$d order"
			}
			return i18n.Sprintf(s.__(c, format), ivs[i].Label, s.gooPricePlain(sales[i], cur), strconv.Itoa(int(orders[i])))
		})
		d.HasChart = true
	case "sales_by_product":
		d.Products = s.gooProductOptions(ctx, false)
		d.ProductID = c.Query("product_ids")
		pid, _ := strconv.ParseInt(d.ProductID, 10, 64)
		tbl := &gooTable{Headers: []string{"Product", "Items sold", "Sales"}, Empty: "No products sold in this period."}
		rows, err := s.store.Pool.Query(ctx, `SELECT i.product_id, max(i.name), sum(i.quantity)::float8, sum(i.total)::float8 FROM goo_order_items i JOIN goo_orders o ON o.id = i.order_id
			WHERE i.type = 'line_item' AND o.type = 'shop_order' AND o.status = ANY($1) AND o.date_created >= $2 AND o.date_created < $3
			GROUP BY i.product_id ORDER BY 3 DESC, 4 DESC LIMIT 50`, gooPaidList, from, to)
		if err == nil {
			for rows.Next() {
				var id int64
				var name string
				var qty, total float64
				if rows.Scan(&id, &name, &qty, &total) == nil {
					link := template.HTML(`<a href="` + template.HTMLEscapeString(d.BaseURL+"&range="+d.Range.Key+"&product_ids="+itoa64(id)) + `">` + template.HTMLEscapeString(name) + `</a>`)
					tbl.add([]template.HTML{link, template.HTML(strconv.Itoa(int(qty))), price(total)}, []string{name, strconv.Itoa(int(qty)), s.gooFmtDec(total)})
				}
			}
			rows.Close()
		}
		d.Table = tbl
		where := ""
		if pid > 0 {
			where = " AND o.id IN (SELECT order_id FROM goo_order_items WHERE product_id = " + itoa64(pid) + " OR variation_id = " + itoa64(pid) + ")"
		}
		amount := s.gooSeries(ctx, ivs, "(SELECT coalesce(sum(i.total),0) FROM goo_order_items i WHERE i.order_id = o.id AND i.type = 'line_item'"+gooProductFilter(pid)+")", where)
		qty := s.gooSeries(ctx, ivs, "(SELECT coalesce(sum(i.quantity),0) FROM goo_order_items i WHERE i.order_id = o.id AND i.type = 'line_item'"+gooProductFilter(pid)+")", where)
		var ta, tq float64
		for i := range amount {
			ta += amount[i]
			tq += qty[i]
		}
		d.Legend = []gooLegend{{price(ta), "sales for the selected items", "#3498db"}, {template.HTML(strconv.Itoa(int(tq))), "purchases for the selected items", "#dbe1e3"}}
		d.Chart = gooBars(ivs, amount, qty, func(i int) string { return fmt.Sprintf("%s: %s", ivs[i].Label, s.gooPricePlain(amount[i], cur)) })
		d.HasChart = true
	case "sales_by_category":
		tbl := &gooTable{Headers: []string{"Category", "Items sold", "Sales"}, Empty: "No categories with sales in this period."}
		rows, err := s.store.Pool.Query(ctx, `SELECT t.id, t.name, sum(i.quantity)::float8, sum(i.total)::float8 FROM goo_order_items i
			JOIN goo_orders o ON o.id = i.order_id JOIN post_terms pt ON pt.post_id = i.product_id JOIN terms t ON t.id = pt.term_id AND t.taxonomy = 'product_cat'
			WHERE i.type = 'line_item' AND o.type = 'shop_order' AND o.status = ANY($1) AND o.date_created >= $2 AND o.date_created < $3
			GROUP BY t.id, t.name ORDER BY 4 DESC`, gooPaidList, from, to)
		var total float64
		if err == nil {
			for rows.Next() {
				var id int64
				var name string
				var qty, amt float64
				if rows.Scan(&id, &name, &qty, &amt) == nil {
					total += amt
					tbl.add([]template.HTML{template.HTML(template.HTMLEscapeString(name)), template.HTML(strconv.Itoa(int(qty))), price(amt)}, []string{name, strconv.Itoa(int(qty)), s.gooFmtDec(amt)})
				}
			}
			rows.Close()
		}
		d.Table = tbl
		d.Legend = []gooLegend{{price(total), "sales in the categories", "#3498db"}}
	case "coupon_usage":
		disc := s.gooSeries(ctx, ivs, "o.discount_total", "")
		used := s.gooSeries(ctx, ivs, "(SELECT count(*) FROM goo_order_items i WHERE i.order_id = o.id AND i.type = 'coupon')", "")
		var td, tu float64
		for i := range disc {
			td += disc[i]
			tu += used[i]
		}
		d.Legend = []gooLegend{{price(td), "discounts in total", "#3498db"}, {template.HTML(strconv.Itoa(int(tu))), "coupons used in total", "#dbe1e3"}}
		d.Chart = gooBars(ivs, disc, used, func(i int) string { return fmt.Sprintf("%s: %s", ivs[i].Label, s.gooPricePlain(disc[i], cur)) })
		d.HasChart = true
		tbl := &gooTable{Headers: []string{"Coupon", "Uses", "Discount"}, Empty: "No coupons used in this period."}
		rows, err := s.store.Pool.Query(ctx, `SELECT lower(i.name), count(*), coalesce(sum(i.total),0)::float8 FROM goo_order_items i JOIN goo_orders o ON o.id = i.order_id
			WHERE i.type = 'coupon' AND o.type = 'shop_order' AND o.status = ANY($1) AND o.date_created >= $2 AND o.date_created < $3 GROUP BY 1 ORDER BY 2 DESC`, gooPaidList, from, to)
		if err == nil {
			for rows.Next() {
				var code string
				var n int
				var amt float64
				if rows.Scan(&code, &n, &amt) == nil {
					tbl.add([]template.HTML{template.HTML("<code>" + template.HTMLEscapeString(code) + "</code>"), template.HTML(strconv.Itoa(n)), price(amt)}, []string{code, strconv.Itoa(n), s.gooFmtDec(amt)})
				}
			}
			rows.Close()
		}
		d.Table = tbl
	case "customers":
		var custOrders, guestOrders, signups int
		_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE customer_id <> 0), count(*) FILTER (WHERE customer_id = 0) FROM goo_orders
			WHERE type = 'shop_order' AND status = ANY($1) AND date_created >= $2 AND date_created < $3`, gooPaidList, from, to).Scan(&custOrders, &guestOrders)
		_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM users u JOIN user_roles ur ON ur.user_id = u.id JOIN roles r ON r.id = ur.role_id
			WHERE r.slug = 'customer' AND u.registered_at >= $1 AND u.registered_at < $2`, from, to).Scan(&signups)
		d.Legend = []gooLegend{{template.HTML(strconv.Itoa(signups)), "signups in this period", "#3498db"},
			{template.HTML(strconv.Itoa(custOrders)), "customer orders", "#5cc488"}, {template.HTML(strconv.Itoa(guestOrders)), "guest orders", "#e74c3c"}}
		co := s.gooSeries(ctx, ivs, "1", " AND o.customer_id <> 0")
		go_ := s.gooSeries(ctx, ivs, "1", " AND o.customer_id = 0")
		d.Chart = gooBars(ivs, co, go_, func(i int) string {
			return i18n.Sprintf(s.__(c, "%1$s: %2$d customer orders, %3$d guest orders"), ivs[i].Label, strconv.Itoa(int(co[i])), strconv.Itoa(int(go_[i])))
		})
		d.HasChart = true
	case "customer_list":
		tbl := &gooTable{Headers: []string{"Name (Last, First)", "Username", "Email", "Location", "Orders", "Money spent", "Last order"}, Empty: "No customers found."}
		for _, r := range s.gooCustomers(ctx) {
			if r.UserID == 0 {
				continue
			}
			loc := strings.Trim(strings.Join([]string{r.City, gooStateName(r.Country, r.Region), gooCountryName(r.Country)}, ", "), ", ")
			last := "&ndash;"
			if r.Orders > 0 && !r.LastActive.IsZero() {
				last = r.LastActive.In(s.loc()).Format("January 2, 2006")
			}
			tbl.add([]template.HTML{template.HTML(`<a href="` + r.ProfileURL + `">` + template.HTMLEscapeString(r.Name) + `</a>`), template.HTML(template.HTMLEscapeString(r.Username)),
				template.HTML(template.HTMLEscapeString(r.Email)), template.HTML(template.HTMLEscapeString(loc)), template.HTML(strconv.Itoa(r.Orders)), price(r.Spend), template.HTML(last)},
				[]string{r.Name, r.Username, r.Email, loc, strconv.Itoa(r.Orders), s.gooFmtDec(r.Spend), strings.ReplaceAll(last, "&ndash;", "")})
		}
		d.Table = tbl
	case "low_in_stock", "out_of_stock", "most_stocked":
		kind := map[string]string{"low_in_stock": "lowstock", "out_of_stock": "outofstock", "most_stocked": "most"}[report]
		d.Table = gooStockTable(s.gooStockReport(ctx, kind, 100))
	}
	if c.Query("download") == "csv" && d.Table != nil {
		gooCSV(c, "report-"+report+".csv", d.Table.Headers, d.Table.CSV)
		return
	}
	s.gooStoreRender(c, "goocommerce?page=reports", "wc-reports", "Reports", "admin/goo-reports", d)
}

func gooProductFilter(pid int64) string {
	if pid <= 0 {
		return ""
	}
	return " AND (i.product_id = " + itoa64(pid) + " OR i.variation_id = " + itoa64(pid) + ")"
}

// Stock ------------------------------------------------------------------------------------------------------

type gooStockRow struct {
	ID      int64
	Name    string
	Parent  string
	SKU     string
	Qty     string
	Status  string
	EditURL string
}

// gooStockReport lists products by stock: lowstock, outofstock, most.
func (s *Server) gooStockReport(ctx context.Context, kind string, limit int) []gooStockRow {
	low, none := s.gooOptInt("notify_low_stock_amount"), s.gooOptInt("notify_no_stock_amount")
	where := ""
	order := "po.title"
	switch kind {
	case "lowstock":
		where = fmt.Sprintf(`p.manage_stock AND p.stock_quantity <= coalesce(p.low_stock_amount, %d) AND p.stock_quantity > %d`, low, none)
	case "outofstock":
		where = `p.stock_status = 'outofstock'`
	default:
		where = fmt.Sprintf(`p.manage_stock AND p.stock_quantity > %d`, none)
		order = "p.stock_quantity DESC"
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT po.id, po.title, coalesce(pp.title,''), coalesce(pp.id,0), p.sku, p.stock_quantity, p.stock_status
		FROM goo_products p JOIN posts po ON po.id = p.post_id LEFT JOIN posts pp ON pp.id = po.parent_id AND po.type = 'product_variation'
		WHERE po.status = 'publish' AND po.type IN ('product','product_variation') AND `+where+` ORDER BY `+order+fmt.Sprintf(` LIMIT %d`, limit))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []gooStockRow
	for rows.Next() {
		var r gooStockRow
		var parentID int64
		var qty *int
		var st string
		if rows.Scan(&r.ID, &r.Name, &r.Parent, &parentID, &r.SKU, &qty, &st) != nil {
			continue
		}
		r.Qty = "&ndash;"
		if qty != nil {
			r.Qty = strconv.Itoa(*qty)
		}
		r.Status = map[string]string{"instock": "In stock", "outofstock": "Out of stock", "onbackorder": "On backorder"}[st]
		edit := r.ID
		if parentID != 0 {
			edit = parentID
		}
		r.EditURL = "/wp-admin/post?post=" + itoa64(edit) + "&action=edit"
		out = append(out, r)
	}
	return out
}

func gooStockTable(list []gooStockRow) *gooTable {
	t := &gooTable{Headers: []string{"Product", "Parent", "Units in stock", "Stock status", "Actions"}, Empty: "No products found."}
	for _, r := range list {
		name := template.HTMLEscapeString(r.Name)
		if r.SKU != "" {
			name += " (" + template.HTMLEscapeString(r.SKU) + ")"
		}
		parent := "&ndash;"
		if r.Parent != "" {
			parent = template.HTMLEscapeString(r.Parent)
		}
		t.add([]template.HTML{template.HTML(name), template.HTML(parent), template.HTML(r.Qty), template.HTML(`<mark class="` + strings.ToLower(strings.ReplaceAll(r.Status, " ", "")) + `">` + r.Status + `</mark>`),
			template.HTML(`<a class="button tips edit" href="` + r.EditURL + `">Edit</a>`)}, []string{r.Name, r.Parent, strings.ReplaceAll(r.Qty, "&ndash;", ""), r.Status, ""})
	}
	return t
}
