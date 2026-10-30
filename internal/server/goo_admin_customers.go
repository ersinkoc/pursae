package server

import (
	"context"
	"encoding/json"
	"html/template"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Goocommerce › Customers: WooCommerce's customers report — registered customers (billing_* user
// meta) and guest customers (billing e-mails of guest orders) with their order totals.

type gooCustomerRow struct {
	UserID      int64
	Name        string
	Username    string
	LastActive  time.Time
	Registered  time.Time
	Email       string
	Orders      int
	Spend       float64
	AOV         float64
	Country     string
	City        string
	Region      string
	Postcode    string
	ProfileURL  string
	OrdersURL   string
	SpendFmt    template.HTML
	AOVFmt      template.HTML
	LastFmt     string
	RegFmt      string
	CountryName string
}

type gooCustomersData struct {
	Rows    []*gooCustomerRow
	Search  string
	OrderBy string
	Order   string
	Pager   gooPager
	Cols    []gooSortCol
	CSVURL  string
	Total   int
	Guests  int
}

type gooSortCol struct {
	Key, Label, URL, Class string
}

// gooCustomers builds every customer row (registered users with the customer role or orders, and guests).
func (s *Server) gooCustomers(ctx context.Context) []*gooCustomerRow {
	type agg struct {
		n     int
		spend float64
		last  time.Time
	}
	byUser := map[int64]*agg{}
	byEmail := map[string]*agg{}
	guestBilling := map[string]map[string]string{}
	rows, err := s.store.Pool.Query(ctx, `SELECT o.customer_id, o.billing_email, count(*), coalesce(sum(o.total),0)::float8 -
		coalesce(sum((SELECT coalesce(sum(-r.total),0) FROM goo_orders r WHERE r.parent_id = o.id AND r.type = 'shop_order_refund')),0)::float8, max(o.date_created)
		FROM goo_orders o WHERE o.type = 'shop_order' AND o.status NOT IN ('trash','checkout-draft','failed','cancelled','pending')
		GROUP BY o.customer_id, o.billing_email`)
	if err == nil {
		for rows.Next() {
			var uid int64
			var email string
			a := &agg{}
			if rows.Scan(&uid, &email, &a.n, &a.spend, &a.last) != nil {
				continue
			}
			if uid != 0 {
				if b := byUser[uid]; b != nil {
					b.n += a.n
					b.spend += a.spend
					if a.last.After(b.last) {
						b.last = a.last
					}
				} else {
					byUser[uid] = a
				}
			} else if email != "" {
				byEmail[email] = a
			}
		}
		rows.Close()
	}
	// Guests: every e-mail of a guest order (even unpaid), with the latest billing address.
	rows, err = s.store.Pool.Query(ctx, `SELECT DISTINCT ON (billing_email) billing_email, billing, date_created FROM goo_orders
		WHERE type = 'shop_order' AND customer_id = 0 AND billing_email <> '' AND status NOT IN ('trash','checkout-draft')
		ORDER BY billing_email, date_created DESC`)
	if err == nil {
		for rows.Next() {
			var email string
			var raw []byte
			var at time.Time
			if rows.Scan(&email, &raw, &at) == nil {
				b := map[string]string{}
				_ = json.Unmarshal(raw, &b)
				guestBilling[email] = b
				if byEmail[email] == nil {
					byEmail[email] = &agg{last: at}
				}
			}
		}
		rows.Close()
	}
	var out []*gooCustomerRow
	rows, err = s.store.Pool.Query(ctx, `SELECT u.id, u.login, u.display_name, u.email, u.registered_at, u.last_login_at, u.meta, u.profile FROM users u
		WHERE EXISTS (SELECT 1 FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE ur.user_id = u.id AND r.slug = 'customer')
		OR EXISTS (SELECT 1 FROM goo_orders o WHERE o.customer_id = u.id AND o.type = 'shop_order')`)
	if err == nil {
		for rows.Next() {
			r := &gooCustomerRow{}
			var last *time.Time
			var meta, profile []byte
			if rows.Scan(&r.UserID, &r.Username, &r.Name, &r.Email, &r.Registered, &last, &meta, &profile) != nil {
				continue
			}
			m := map[string]any{}
			_ = json.Unmarshal(meta, &m)
			p := map[string]string{}
			_ = json.Unmarshal(profile, &p)
			str := func(k string) string { v, _ := m[k].(string); return v }
			if n := strings.TrimSpace(str("billing_first_name") + " " + str("billing_last_name")); n != "" {
				r.Name = n
			} else if n := strings.TrimSpace(p["first_name"] + " " + p["last_name"]); n != "" {
				r.Name = n
			}
			r.Country, r.City, r.Region, r.Postcode = str("billing_country"), str("billing_city"), str("billing_state"), str("billing_postcode")
			if last != nil {
				r.LastActive = *last
			}
			if a := byUser[r.UserID]; a != nil {
				r.Orders, r.Spend = a.n, a.spend
				if a.last.After(r.LastActive) {
					r.LastActive = a.last
				}
			}
			r.ProfileURL = "/wp-admin/user-edit?user_id=" + itoa64(r.UserID)
			r.OrdersURL = gooAdminURL("orders", "_customer_user", itoa64(r.UserID))
			out = append(out, r)
		}
		rows.Close()
	}
	for email, a := range byEmail {
		b := guestBilling[email]
		if b == nil {
			continue // a registered customer's order placed with another e-mail is counted under the user
		}
		r := &gooCustomerRow{Email: email, Orders: a.n, Spend: a.spend, LastActive: a.last,
			Name: strings.TrimSpace(b["first_name"] + " " + b["last_name"]), Country: b["country"], City: b["city"], Region: b["state"], Postcode: b["postcode"],
			OrdersURL: gooAdminURL("orders", "s", email)}
		out = append(out, r)
	}
	for _, r := range out {
		if r.Orders > 0 {
			r.AOV = r.Spend / float64(r.Orders)
		}
	}
	return out
}

func (s *Server) gooAdminCustomers(c *gin.Context) {
	ctx := c.Request.Context()
	d := &gooCustomersData{Search: strings.TrimSpace(c.Query("s")), OrderBy: c.DefaultQuery("orderby", "date_last_active"), Order: strings.ToLower(c.DefaultQuery("order", "desc"))}
	all := s.gooCustomers(ctx)
	var rows []*gooCustomerRow
	q := strings.ToLower(d.Search)
	for _, r := range all {
		if q != "" && !strings.Contains(strings.ToLower(r.Name+" "+r.Username+" "+r.Email+" "+r.City+" "+r.Postcode), q) {
			continue
		}
		rows = append(rows, r)
	}
	less := map[string]func(a, b *gooCustomerRow) bool{
		"name":             func(a, b *gooCustomerRow) bool { return strings.ToLower(a.Name) < strings.ToLower(b.Name) },
		"username":         func(a, b *gooCustomerRow) bool { return a.Username < b.Username },
		"email":            func(a, b *gooCustomerRow) bool { return a.Email < b.Email },
		"date_last_active": func(a, b *gooCustomerRow) bool { return a.LastActive.Before(b.LastActive) },
		"date_registered":  func(a, b *gooCustomerRow) bool { return a.Registered.Before(b.Registered) },
		"orders_count":     func(a, b *gooCustomerRow) bool { return a.Orders < b.Orders },
		"total_spend":      func(a, b *gooCustomerRow) bool { return a.Spend < b.Spend },
		"avg_order_value":  func(a, b *gooCustomerRow) bool { return a.AOV < b.AOV },
		"country":          func(a, b *gooCustomerRow) bool { return a.Country < b.Country },
		"city":             func(a, b *gooCustomerRow) bool { return a.City < b.City },
		"state":            func(a, b *gooCustomerRow) bool { return a.Region < b.Region },
		"postcode":         func(a, b *gooCustomerRow) bool { return a.Postcode < b.Postcode },
	}
	fn := less[d.OrderBy]
	if fn == nil {
		d.OrderBy, fn = "date_last_active", less["date_last_active"]
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if d.Order == "asc" {
			return fn(rows[i], rows[j])
		}
		return fn(rows[j], rows[i])
	})
	for _, r := range rows {
		r.SpendFmt, r.AOVFmt = s.gooPrice(r.Spend), s.gooPrice(r.AOV)
		r.CountryName = gooCountryName(r.Country)
		r.Region = gooStateName(r.Country, r.Region)
		if !r.LastActive.IsZero() {
			r.LastFmt = r.LastActive.In(s.loc()).Format("January 2, 2006")
		}
		if !r.Registered.IsZero() {
			r.RegFmt = r.Registered.In(s.loc()).Format("January 2, 2006")
		}
		if r.UserID == 0 {
			d.Guests++
		}
	}
	if c.Query("download") == "csv" {
		var out [][]string
		for _, r := range rows {
			out = append(out, []string{r.Name, r.Username, r.LastFmt, r.RegFmt, r.Email, strconv.Itoa(r.Orders), s.gooFmtDec(r.Spend), s.gooFmtDec(r.AOV),
				r.CountryName, r.City, r.Region, r.Postcode})
		}
		gooCSV(c, "customers.csv", []string{"Name", "Username", "Last active", "Date registered", "Email", "Orders", "Total spend", "AOV", "Country / Region", "City", "Region", "Postal code"}, out)
		return
	}
	d.Total = len(rows)
	page := gooPaged(c)
	per := 25
	base := gooAdminURL("customers", "s", d.Search, "orderby", d.OrderBy, "order", d.Order)
	d.Pager = newGooPager(base, page, per, len(rows))
	start := min((page-1)*per, len(rows))
	d.Rows = rows[start:min(start+per, len(rows))]
	d.CSVURL = gooAdminURL("customers", "s", d.Search, "orderby", d.OrderBy, "order", d.Order, "download", "csv")
	for _, col := range []gooOpt{{"name", "Name"}, {"username", "Username"}, {"date_last_active", "Last active"}, {"date_registered", "Date registered"},
		{"email", "Email"}, {"orders_count", "Orders"}, {"total_spend", "Total spend"}, {"avg_order_value", "AOV"}, {"country", "Country / Region"},
		{"city", "City"}, {"state", "Region"}, {"postcode", "Postal code"}} {
		order, cls := "asc", "sortable desc"
		if col.Value == d.OrderBy {
			cls = "sorted " + d.Order
			if d.Order == "asc" {
				order = "desc"
			}
		}
		d.Cols = append(d.Cols, gooSortCol{Key: col.Value, Label: col.Label, Class: cls, URL: gooAdminURL("customers", "s", d.Search, "orderby", col.Value, "order", order)})
	}
	s.gooStoreRender(c, "goocommerce?page=customers", "wc-admin", "Customers", "admin/goo-customers", d)
}
