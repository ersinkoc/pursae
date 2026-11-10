package server

import (
	"context"
	"encoding/csv"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Goocommerce's store admin screens: Home, Orders, Customers, Coupons, Marketing, Settings, Reports,
// Analytics and Status (/wp-admin/goocommerce?page=…). This file holds the shared pieces (rendering,
// notices, pagination, CSV) and the Home and Marketing screens; the others live in goo_admin_*.go.

// gooStoreNonce is the nonce action of every store admin form except the settings screens (gooNonce).
const gooStoreNonce = "goocommerce-store"

// gooAdminURL builds /wp-admin/goocommerce?page=<page>&k=v… (pairs of key, value; empty values skipped).
func gooAdminURL(page string, kv ...string) string {
	q := url.Values{}
	if page != "" {
		q.Set("page", page)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			q.Set(kv[i], kv[i+1])
		}
	}
	u := "/wp-admin/goocommerce"
	if enc := gooOrderedQuery(q, append([]string{"page"}, gooKeys(kv)...)); enc != "" {
		u += "?" + enc
	}
	return u
}

func gooKeys(kv []string) []string {
	var out []string
	for i := 0; i < len(kv); i += 2 {
		out = append(out, kv[i])
	}
	return out
}

// gooOrderedQuery encodes q keeping the given key order (page first, like WooCommerce's links).
func gooOrderedQuery(q url.Values, order []string) string {
	var parts []string
	seen := map[string]bool{}
	for _, k := range order {
		if seen[k] {
			continue
		}
		seen[k] = true
		for _, v := range q[k] {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// gooRedirect sends the browser to a store admin URL with a success message or an error.
func gooRedirect(c *gin.Context, to, msg, errMsg string) {
	sep := "?"
	if strings.Contains(to, "?") {
		sep = "&"
	}
	if msg != "" {
		to += sep + "goo-msg=" + url.QueryEscape(msg)
		sep = "&"
	}
	if errMsg != "" {
		to += sep + "goo-error=" + url.QueryEscape(errMsg)
	}
	c.Redirect(http.StatusFound, to)
}

// gooStoreNotices are the notices carried by a redirect (goo-msg / goo-error).
func gooStoreNotices(c *gin.Context) []notice {
	var out []notice
	if m := c.Query("goo-msg"); m != "" {
		out = append(out, notice{Type: "success", Message: template.HTML(template.HTMLEscapeString(m)), Dismissible: true})
	}
	if e := c.Query("goo-error"); e != "" {
		out = append(out, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(e)), Dismissible: true})
	}
	return out
}

// gooStoreRender renders a store admin screen. screen is the menu slug ("goocommerce?page=orders"),
// wcPage WooCommerce's page slug used for the body class (wc-orders, wc-settings, …).
func (s *Server) gooStoreRender(c *gin.Context, screen, wcPage, title, tmpl string, data any, notices ...notice) {
	body := "woocommerce_page_" + wcPage + " woocommerce-admin-page"
	if wcPage == "wc-admin" {
		body = "toplevel_page_goocommerce woocommerce-admin-page"
	}
	s.adminPage(c, screen, title+" ‹ Pursae Commerce", body, tmpl, data, append(gooStoreNotices(c), notices...)...)
}

// gooDeny answers a failed nonce check.
func (s *Server) gooDeny(c *gin.Context) { s.denyNonce(c) }

// Pagination ---------------------------------------------------------------------------------------------

// gooPager is a list table's pagination (tablenav-pages).
type gooPager struct {
	Page, Pages, Total, PerPage int
	base                        string // the list URL without paged
}

func newGooPager(base string, page, perPage, total int) gooPager {
	pages := int(math.Ceil(float64(total) / float64(max(perPage, 1))))
	return gooPager{Page: max(page, 1), Pages: max(pages, 1), Total: total, PerPage: perPage, base: base}
}

func (p gooPager) url(n int) string {
	if n <= 1 {
		return p.base
	}
	sep := "?"
	if strings.Contains(p.base, "?") {
		sep = "&"
	}
	return p.base + sep + "paged=" + strconv.Itoa(n)
}

// HTML is the tablenav-pages block.
func (p gooPager) HTML() template.HTML {
	var b strings.Builder
	cls := "tablenav-pages"
	if p.Pages <= 1 {
		cls += " one-page"
	}
	items := "items"
	if p.Total == 1 {
		items = "item"
	}
	fmt.Fprintf(&b, `<div class="%s"><span class="displaying-num">%s %s</span>`, cls, formatNumber(p.Total), items)
	if p.Pages > 1 {
		b.WriteString(`<span class="pagination-links">`)
		link := func(ok bool, n int, cls, sym, label string) {
			if ok {
				fmt.Fprintf(&b, `<a class="%s button" href="%s"><span class="screen-reader-text">%s</span><span aria-hidden="true">%s</span></a> `, cls, template.HTMLEscapeString(p.url(n)), label, sym)
			} else {
				fmt.Fprintf(&b, `<span class="tablenav-pages-navspan button disabled" aria-hidden="true">%s</span> `, sym)
			}
		}
		link(p.Page > 1, 1, "first-page", "&laquo;", "First page")
		link(p.Page > 1, p.Page-1, "prev-page", "&lsaquo;", "Previous page")
		fmt.Fprintf(&b, `<span class="paging-input"><span class="tablenav-paging-text">%d of <span class="total-pages">%d</span></span></span> `, p.Page, p.Pages)
		link(p.Page < p.Pages, p.Page+1, "next-page", "&rsaquo;", "Next page")
		link(p.Page < p.Pages, p.Pages, "last-page", "&raquo;", "Last page")
		b.WriteString(`</span>`)
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

func gooPaged(c *gin.Context) int {
	n, _ := strconv.Atoi(c.Query("paged"))
	return max(n, 1)
}

// gooView is a subsubsub view link (All (3) | Processing (1) …).
type gooView struct {
	Label   string
	URL     string
	Count   int
	Current bool
}

// gooCSV writes rows as a CSV download.
func gooCSV(c *gin.Context, filename string, header []string, rows [][]string) {
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	w := csv.NewWriter(c.Writer)
	_ = w.Write(gooCSVEscapeRow(header))
	for _, r := range rows {
		_ = w.Write(gooCSVEscapeRow(r))
	}
	w.Flush()
}

// gooCSVEscapeRow neutralises formula cells (customer names, cities, coupon codes, …) in an export;
// numbers, including negative amounts, stay numeric.
func gooCSVEscapeRow(row []string) []string {
	out := make([]string, len(row))
	for i, v := range row {
		if _, err := strconv.ParseFloat(v, 64); err == nil {
			out[i] = v
			continue
		}
		out[i] = gooCSVEscape(v)
	}
	return out
}

// Formatting ----------------------------------------------------------------------------------------------

// gooAdminDate is the admin's date for a time in the site's zone (M j, Y), "x mins ago" within a day like the orders list.
func (s *Server) gooAdminDate(t time.Time) string {
	if t.IsZero() {
		return "&ndash;"
	}
	d := time.Since(t)
	if d >= 0 && d < 24*time.Hour {
		switch {
		case d < time.Minute:
			return "now"
		case d < time.Hour:
			return plural(int(d.Minutes()), "1 min ago", "%d mins ago")
		default:
			return plural(int(d.Hours()), "1 hour ago", "%d hours ago")
		}
	}
	return t.In(s.loc()).Format("Jan 2, 2006")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf(many, n)
}

// gooFmtNum is a number with the store's decimals and separators (no currency).
func (s *Server) gooFmtNum(v float64) string { return s.gooNumberFormat(v, s.gooDecimals()) }

// gooFmtDec is a plain decimal for form inputs ("12.50").
func (s *Server) gooFmtDec(v float64) string { return gooFormatDecimal(v, s.gooDecimals()) }

// gooFloat parses a posted amount; "" and junk are 0.
func (s *Server) gooFloat(v string) float64 {
	if f := s.gooParseAmount(v); f != nil {
		return *f
	}
	return 0
}

// gooPaidList are the statuses the reports count as sales (WooCommerce's report statuses).
var gooPaidList = []string{"processing", "completed", "on-hold"}

// Home ------------------------------------------------------------------------------------------------------

type gooHomeStat struct {
	Label  string
	Sales  template.HTML
	Orders int
	Items  int
}

type gooHomeTask struct {
	Title, Desc, URL string
	Done             bool
}

type gooHomeData struct {
	Stats       []gooHomeStat
	Recent      []*gooOrderRow
	LowStock    []gooStockRow
	OutOfStock  int
	Pending     int
	Processing  int
	OnHold      int
	ReviewCount int
	Tasks       []gooHomeTask
	TasksDone   int
	ShopURL     string
}

func (s *Server) gooAdminHome(c *gin.Context) {
	ctx := c.Request.Context()
	u := currentUser(c)
	d := &gooHomeData{ShopURL: s.gooShopURL(ctx)}
	now := time.Now().In(s.loc())
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.loc())
	week := today.AddDate(0, 0, -int((today.Weekday()+6)%7))
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, s.loc())
	for _, r := range []struct {
		label string
		from  time.Time
	}{{"Today", today}, {"Week to date", week}, {"Month to date", month}} {
		t := s.gooTotals(ctx, r.from, now.Add(time.Minute))
		d.Stats = append(d.Stats, gooHomeStat{Label: r.label, Sales: s.gooPrice(t.Gross - t.Refunds), Orders: t.Orders, Items: t.Items})
	}
	orders, _, _ := s.gooQueryOrders(ctx, gooOrderQuery{PerPage: 5, Page: 1})
	refunded := s.gooTotalsRefunded(ctx, gooOrderIDs(orders))
	for _, o := range orders {
		d.Recent = append(d.Recent, s.gooOrderRowOf(ctx, o, refunded[o.ID]))
	}
	d.LowStock = s.gooStockReport(ctx, "lowstock", 5)
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_products p JOIN posts po ON po.id = p.post_id
		WHERE po.status = 'publish' AND p.stock_status = 'outofstock'`).Scan(&d.OutOfStock)
	counts := s.gooOrderStatusCounts(ctx)
	d.Pending, d.Processing, d.OnHold = counts["pending"], counts["processing"], counts["on-hold"]
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM comments cm JOIN posts p ON p.id = cm.post_id WHERE p.type = 'product' AND cm.status = 'pending'`).Scan(&d.ReviewCount)

	var products int
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE type = 'product' AND status <> 'trash' AND status <> 'auto-draft'`).Scan(&products)
	payments := false
	for _, g := range s.gooGatewaysOrdered() {
		if g.Enabled() {
			payments = true
		}
	}
	var rates, zones int
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_tax_rates`).Scan(&rates)
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_shipping_methods`).Scan(&zones)
	d.Tasks = []gooHomeTask{
		s.gooThemeTask(), // goo_admin_themes.go
		{"Store details", "Your store's address, currency and selling locations.", gooAdminURL("settings"), s.gooOptString("store_address") != ""},
		{"Add products", "Start by adding the first product to your store.", "/wp-admin/post-new?post_type=product", products > 0},
		{"Set up payments", "Choose payment providers and enable payment methods at checkout.", gooAdminURL("settings", "tab", "checkout"), payments},
		{"Set up tax", "Collect sales tax: enable taxes and add the rates of your locations.", gooAdminURL("settings", "tab", "tax"), s.gooTaxEnabled() && rates > 0},
		{"Set up shipping", "Add shipping zones and the methods available in each.", gooAdminURL("settings", "tab", "shipping"), zones > 0},
		{"Add a coupon", "Offer discounts to your customers with coupon codes.", gooAdminURL("coupons", "action", "new"), s.gooCouponCount(ctx) > 0},
	}
	for _, t := range d.Tasks {
		if t.Done {
			d.TasksDone++
		}
	}
	var notices []notice
	if hint := s.gooThemeHint(); hint != "" && u.Can("install_themes", nil) {
		notices = append(notices, notice{Type: "info", Message: template.HTML(hint)})
	}
	s.gooStoreRender(c, "goocommerce", "wc-admin", "Home", "admin/goo-home", d, notices...)
}

func (s *Server) gooCouponCount(ctx context.Context) int {
	var n int
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_coupons WHERE status <> 'trash'`).Scan(&n)
	return n
}

// gooTotals are the sales figures of the paid orders created in [from, to).
type gooTotalsRow struct {
	Gross, Net, Tax, Shipping, ShippingTax, Discount, Refunds float64
	Orders, Items, RefundedItems                              int
}

func (s *Server) gooTotals(ctx context.Context, from, to time.Time) gooTotalsRow {
	var t gooTotalsRow
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(total),0)::float8, coalesce(sum(total_tax),0)::float8, coalesce(sum(shipping_total),0)::float8,
		coalesce(sum(shipping_tax),0)::float8, coalesce(sum(discount_total),0)::float8,
		coalesce((SELECT sum(i.quantity) FROM goo_order_items i JOIN goo_orders o2 ON o2.id = i.order_id WHERE i.type = 'line_item' AND o2.type = 'shop_order'
			AND o2.status = ANY($1) AND o2.date_created >= $2 AND o2.date_created < $3),0)::int
		FROM goo_orders WHERE type = 'shop_order' AND status = ANY($1) AND date_created >= $2 AND date_created < $3`, gooPaidList, from, to).
		Scan(&t.Orders, &t.Gross, &t.Tax, &t.Shipping, &t.ShippingTax, &t.Discount, &t.Items)
	_ = s.store.Pool.QueryRow(ctx, `SELECT coalesce(sum(-r.total),0)::float8, coalesce((SELECT sum(-i.quantity) FROM goo_order_items i JOIN goo_orders r2 ON r2.id = i.order_id
			WHERE r2.type = 'shop_order_refund' AND i.type = 'line_item' AND r2.date_created >= $2 AND r2.date_created < $3),0)::int
		FROM goo_orders r JOIN goo_orders o ON o.id = r.parent_id
		WHERE r.type = 'shop_order_refund' AND o.status = ANY($1 || '{refunded}'::text[]) AND r.date_created >= $2 AND r.date_created < $3`, gooPaidList, from, to).Scan(&t.Refunds, &t.RefundedItems)
	t.Net = t.Gross - t.Tax - t.Shipping - t.ShippingTax - t.Refunds
	return t
}

// Marketing ---------------------------------------------------------------------------------------------------

type gooMarketingData struct {
	Active, Expired, Used int
	Discounted            template.HTML
	Orders                int
	Top                   []struct {
		Code   string
		Uses   int
		Amount template.HTML
		URL    string
	}
	CouponsEnabled bool
}

func (s *Server) gooAdminMarketing(c *gin.Context) {
	ctx := c.Request.Context()
	d := &gooMarketingData{CouponsEnabled: s.gooCouponsEnabled()}
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE date_expires IS NULL OR date_expires > now()), count(*) FILTER (WHERE date_expires <= now()),
		coalesce(sum(usage_count),0) FROM goo_coupons WHERE status = 'publish'`).Scan(&d.Active, &d.Expired, &d.Used)
	var amount float64
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(DISTINCT o.id), coalesce(sum((i.meta->>'discount_amount')::numeric),0)::float8 FROM goo_order_items i
		JOIN goo_orders o ON o.id = i.order_id WHERE i.type = 'coupon' AND o.type = 'shop_order' AND o.status = ANY($1)`, gooPaidList).Scan(&d.Orders, &amount)
	d.Discounted = s.gooPrice(amount)
	rows, err := s.store.Pool.Query(ctx, `SELECT c.id, c.code, c.usage_count, coalesce((SELECT sum((i.meta->>'discount_amount')::numeric) FROM goo_order_items i
		JOIN goo_orders o ON o.id = i.order_id WHERE i.type = 'coupon' AND lower(i.name) = lower(c.code) AND o.status = ANY($1)),0)::float8
		FROM goo_coupons c WHERE c.status = 'publish' ORDER BY c.usage_count DESC, c.id DESC LIMIT 5`, gooPaidList)
	if err == nil {
		for rows.Next() {
			var id int64
			var code string
			var uses int
			var amt float64
			if rows.Scan(&id, &code, &uses, &amt) == nil {
				d.Top = append(d.Top, struct {
					Code   string
					Uses   int
					Amount template.HTML
					URL    string
				}{code, uses, s.gooPrice(amt), gooAdminURL("coupons", "action", "edit", "id", itoa64(id))})
			}
		}
		rows.Close()
	}
	s.gooStoreRender(c, "goocommerce?page=marketing", "wc-admin", "Marketing", "admin/goo-marketing", d)
}
