package server

import (
	"context"

	"github.com/jackc/pgx/v5"
	"strconv"
	"sync"
	"time"

	"wpgo/internal/wp"
)

// Personal data exporters and erasers (wp_privacy_personal_data_exporters / _erasers). The core's own
// exporters (profile, comments, media) live in admin_tools2.go; every other feature with personal
// data registers here so Tools > Export / Erase Personal Data covers it:
//   - Pursae Commerce: orders and download permissions (the erasure follows Settings > Accounts &
//     Privacy: "Remove personal data from orders on request", "Remove access to downloads on request"),
//   - Pursae Commerce account data: wishlists, points log, subscriptions and memberships of the user
//     with that e-mail (wishlists are erased, subscriptions anonymized like orders, the rest retained),
//   - Pursae SMTP: the e-mail log rows addressed to the person,
//   - Wasm plugins: the pursae_personal_data_export filter and the pursae_personal_data_erase action
//     (docs/developer/plugins/hooks.md).
//
// Pursae Contact Form keeps no submissions and Pursae Pixel / Cookie log no personal data, so they
// have nothing to export or erase.

type personalDataExporter func(ctx context.Context, s *Server, email string) []exportGroup

// personalDataEraser reports how many items it removed or anonymized and how many it had to keep,
// with the reasons for the latter (shown on the request screen).
type personalDataEraser func(ctx context.Context, s *Server, email string) eraseResult

type eraseResult struct {
	Removed, Retained int
	Messages          []string
}

var personalDataRegistry struct {
	sync.RWMutex
	exporters []personalDataExporter
	erasers   []personalDataEraser
}

// registerPersonalData adds an exporter and/or an eraser (either may be nil).
func registerPersonalData(exp personalDataExporter, era personalDataEraser) {
	personalDataRegistry.Lock()
	defer personalDataRegistry.Unlock()
	if exp != nil {
		personalDataRegistry.exporters = append(personalDataRegistry.exporters, exp)
	}
	if era != nil {
		personalDataRegistry.erasers = append(personalDataRegistry.erasers, era)
	}
}

func init() {
	registerPersonalData(gooPersonalDataExporter, gooPersonalDataEraser)
	registerPersonalData(gooAccountDataExporter, gooAccountDataEraser)
	registerPersonalData(smtpLogExporter, smtpLogEraser)
}

// registeredPersonalData runs every registered exporter, then the plugin filter.
func (s *Server) registeredPersonalData(ctx context.Context, email string) []exportGroup {
	personalDataRegistry.RLock()
	exps := append([]personalDataExporter(nil), personalDataRegistry.exporters...)
	personalDataRegistry.RUnlock()
	var groups []exportGroup
	for _, e := range exps {
		groups = append(groups, e(ctx, s, email)...)
	}
	return s.pluginPersonalData(ctx, email, groups)
}

// pluginExportGroup is the shape a plugin sees and returns: WordPress' group_id / group_label / items.
type pluginExportGroup struct {
	ID          string               `json:"group_id"`
	Label       string               `json:"group_label"`
	Description string               `json:"group_description"`
	Items       [][]pluginExportItem `json:"items"`
}

type pluginExportItem struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// pluginPersonalData passes the groups through the pursae_personal_data_export filter. Plugins may add
// groups; the groups the core built come back as sent.
func (s *Server) pluginPersonalData(ctx context.Context, email string, groups []exportGroup) []exportGroup {
	if s.plugins == nil || !s.plugins.hooks.HasFilter("pursae_personal_data_export") {
		return groups
	}
	in := make([]pluginExportGroup, 0, len(groups))
	for _, g := range groups {
		pg := pluginExportGroup{ID: g.ID, Label: g.Label, Description: g.Description}
		for _, item := range g.Items {
			var row []pluginExportItem
			for _, kv := range item {
				row = append(row, pluginExportItem{kv.Name, kv.Value})
			}
			pg.Items = append(pg.Items, row)
		}
		in = append(in, pg)
	}
	out, ok := s.applyFilters(ctx, "pursae_personal_data_export", in, email).([]pluginExportGroup)
	if !ok {
		return groups
	}
	res := make([]exportGroup, 0, len(out))
	for _, pg := range out {
		g := exportGroup{ID: pg.ID, Label: pg.Label, Description: pg.Description}
		for _, item := range pg.Items {
			var row []exportItem
			for _, kv := range item {
				row = append(row, exportItem{kv.Name, kv.Value})
			}
			g.Items = append(g.Items, row)
		}
		res = append(res, g)
	}
	return res
}

// erasePersonalDataRegistered runs every registered eraser, then the plugin action.
func (s *Server) erasePersonalDataRegistered(ctx context.Context, email string) eraseResult {
	personalDataRegistry.RLock()
	erasers := append([]personalDataEraser(nil), personalDataRegistry.erasers...)
	personalDataRegistry.RUnlock()
	var total eraseResult
	for _, e := range erasers {
		r := e(ctx, s, email)
		total.Removed += r.Removed
		total.Retained += r.Retained
		total.Messages = append(total.Messages, r.Messages...)
	}
	if s.plugins != nil && s.plugins.hooks.HasAction("pursae_personal_data_erase") {
		s.plugins.hooks.DoAction(ctx, "pursae_personal_data_erase", email)
	}
	return total
}

// ---- Pursae Commerce -------------------------------------------------------------------------

// orders still being worked on keep their data (WooCommerce retains them too).
const gooOpenOrderStatuses = `('pending','processing','on-hold')`

func gooPersonalDataExporter(ctx context.Context, s *Server, email string) []exportGroup {
	if !s.gooOn() {
		return nil
	}
	dateFmt := s.settings.String("date_format") + " " + s.settings.String("time_format")
	var groups []exportGroup
	rows, err := s.store.Pool.Query(ctx, `SELECT id, status, date_created, total::text, currency, billing, shipping, billing_email, customer_ip, user_agent, customer_note, payment_method_title
		FROM goo_orders WHERE type = 'shop_order' AND lower(billing_email) = lower($1)
		OR (type = 'shop_order' AND customer_id <> 0 AND customer_id = (SELECT id FROM users WHERE lower(email) = lower($1))) ORDER BY id`, email)
	if err == nil {
		g := exportGroup{ID: "woocommerce-customer-orders", Label: "Orders", Description: "This is a list of orders placed on this site."}
		for rows.Next() {
			var id int64
			var status, total, currency, bemail, ip, ua, note, pm string
			var created time.Time
			var billing, shipping map[string]any
			if rows.Scan(&id, &status, &created, &total, &currency, &billing, &shipping, &bemail, &ip, &ua, &note, &pm) != nil {
				continue
			}
			str := func(m map[string]any, k string) string { v, _ := m[k].(string); return v }
			item := []exportItem{{"Order ID", strconv.FormatInt(id, 10)}, {"Order Date", wp.FormatDate(created.In(s.loc()), dateFmt)}, {"Order Status", status},
				{"Order Total", total + " " + currency}, {"Payment Method", pm}, {"Billing Email", bemail}, {"IP Address", ip}, {"Browser User Agent", ua}, {"Customer Note", note}}
			for _, side := range []struct {
				Name string
				M    map[string]any
			}{{"Billing", billing}, {"Shipping", shipping}} {
				for _, f := range [][2]string{{"first_name", "First Name"}, {"last_name", "Last Name"}, {"company", "Company"}, {"address_1", "Address 1"}, {"address_2", "Address 2"},
					{"city", "City"}, {"state", "State"}, {"postcode", "Postcode"}, {"country", "Country"}, {"phone", "Phone"}} {
					if v := str(side.M, f[0]); v != "" {
						item = append(item, exportItem{side.Name + " " + f[1], v})
					}
				}
			}
			g.Items = append(g.Items, item)
		}
		rows.Close()
		if len(g.Items) > 0 {
			groups = append(groups, g)
		}
	}
	drows, err := s.store.Pool.Query(ctx, `SELECT product_id, order_id, access_granted, access_expires, download_count FROM goo_download_permissions
		WHERE lower(user_email) = lower($1) ORDER BY id`, email)
	if err == nil {
		g := exportGroup{ID: "woocommerce-customer-downloads", Label: "Customer downloads", Description: "This is a list of downloads purchased on this site."}
		for drows.Next() {
			var product, order int64
			var granted time.Time
			var expires *time.Time
			var count int
			if drows.Scan(&product, &order, &granted, &expires, &count) != nil {
				continue
			}
			exp := "Never"
			if expires != nil {
				exp = wp.FormatDate(expires.In(s.loc()), dateFmt)
			}
			g.Items = append(g.Items, []exportItem{{"Product ID", strconv.FormatInt(product, 10)}, {"Order ID", strconv.FormatInt(order, 10)},
				{"Access granted", wp.FormatDate(granted.In(s.loc()), dateFmt)}, {"Access expires", exp}, {"Download count", strconv.Itoa(count)}})
		}
		drows.Close()
		if len(g.Items) > 0 {
			groups = append(groups, g)
		}
	}
	return groups
}

func gooPersonalDataEraser(ctx context.Context, s *Server, email string) eraseResult {
	var res eraseResult
	if !s.gooOn() {
		return res
	}
	if s.gooOptBool("erasure_request_removes_order_data") {
		var retained int
		_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_orders WHERE type = 'shop_order' AND lower(billing_email) = lower($1) AND status IN `+gooOpenOrderStatuses, email).Scan(&retained)
		res.Retained += retained
		if retained > 0 {
			res.Messages = append(res.Messages, "Personal data within orders that are still pending, processing or on hold has been retained.")
		}
		tag, err := s.store.Pool.Exec(ctx, `UPDATE goo_orders SET billing = '{}', shipping = '{}', billing_email = 'deleted@site.invalid', customer_ip = '',
			user_agent = '', customer_note = '', customer_id = 0, date_modified = now()
			WHERE type = 'shop_order' AND lower(billing_email) = lower($1) AND status NOT IN `+gooOpenOrderStatuses, email)
		if err == nil {
			res.Removed += int(tag.RowsAffected())
		}
	} else {
		var n int
		_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_orders WHERE type = 'shop_order' AND lower(billing_email) = lower($1)`, email).Scan(&n)
		if n > 0 {
			res.Retained += n
			res.Messages = append(res.Messages, "Orders were retained: Settings > Accounts & Privacy does not remove personal data from orders on request.")
		}
	}
	if s.gooOptBool("erasure_request_removes_download_data") {
		tag, err := s.store.Pool.Exec(ctx, `DELETE FROM goo_download_permissions WHERE lower(user_email) = lower($1)`, email)
		if err == nil {
			res.Removed += int(tag.RowsAffected())
		}
	}
	return res
}

// ---- Pursae SMTP -----------------------------------------------------------------------------

func smtpLogExporter(ctx context.Context, s *Server, email string) []exportGroup {
	rows, err := s.store.Pool.Query(ctx, `SELECT subject, from_addr, status, date_sent FROM wpmailsmtp_emails_log
		WHERE EXISTS (SELECT 1 FROM unnest(to_addrs) a WHERE lower(a) = lower($1)) ORDER BY id`, email)
	if err != nil {
		return nil
	}
	defer rows.Close()
	dateFmt := s.settings.String("date_format") + " " + s.settings.String("time_format")
	g := exportGroup{ID: "email-log", Label: "Email log", Description: "E-mails this site sent to the address."}
	for rows.Next() {
		var subject, from, status string
		var at time.Time
		if rows.Scan(&subject, &from, &status, &at) == nil {
			g.Items = append(g.Items, []exportItem{{"Subject", subject}, {"From", from}, {"Status", status}, {"Sent", wp.FormatDate(at.In(s.loc()), dateFmt)}})
		}
	}
	if len(g.Items) == 0 {
		return nil
	}
	return []exportGroup{g}
}

func smtpLogEraser(ctx context.Context, s *Server, email string) eraseResult {
	tag, err := s.store.Pool.Exec(ctx, `DELETE FROM wpmailsmtp_emails_log WHERE EXISTS (SELECT 1 FROM unnest(to_addrs) a WHERE lower(a) = lower($1))`, email)
	if err != nil {
		return eraseResult{}
	}
	return eraseResult{Removed: int(tag.RowsAffected())}
}

// ---- Pursae Commerce account data (wishlists, points, subscriptions, memberships) -----------------

// gooAccountUserID is the user with the address (0 when none): these records hang off the account.
func gooAccountUserID(ctx context.Context, s *Server, email string) int64 {
	var id int64
	_ = s.store.Pool.QueryRow(ctx, `SELECT id FROM users WHERE lower(email) = lower($1)`, email).Scan(&id)
	return id
}

// gooOpenSubscriptionStatuses are subscriptions still billing; they keep their data.
const gooOpenSubscriptionStatuses = `('pending','active','on-hold','pending-cancel')`

func gooAccountDataExporter(ctx context.Context, s *Server, email string) []exportGroup {
	if !s.gooOn() {
		return nil
	}
	uid := gooAccountUserID(ctx, s, email)
	if uid == 0 {
		return nil
	}
	dateFmt := s.settings.String("date_format") + " " + s.settings.String("time_format")
	date := func(t time.Time) string { return wp.FormatDate(t.In(s.loc()), dateFmt) }
	var groups []exportGroup
	collect := func(g exportGroup, sql string, scan func(pgxRows) []exportItem) {
		rows, err := s.store.Pool.Query(ctx, sql, uid)
		if err != nil {
			return
		}
		for rows.Next() {
			if item := scan(rows); item != nil {
				g.Items = append(g.Items, item)
			}
		}
		rows.Close()
		if len(g.Items) > 0 {
			groups = append(groups, g)
		}
	}
	collect(exportGroup{ID: "woocommerce-wishlists", Label: "Wishlists", Description: "Products saved to wishlists on this site."},
		`SELECT w.title, w.privacy, i.product_id, i.variation_id, i.quantity, i.added FROM goo_wishlists w
		JOIN goo_wishlist_items i ON i.wishlist_id = w.id WHERE w.user_id = $1 ORDER BY w.id, i.id`,
		func(r pgxRows) []exportItem {
			var title, privacy string
			var product, variation int64
			var qty int
			var added time.Time
			if r.Scan(&title, &privacy, &product, &variation, &qty, &added) != nil {
				return nil
			}
			return []exportItem{{"Wishlist", title}, {"Privacy", privacy}, {"Product ID", strconv.FormatInt(product, 10)},
				{"Variation ID", strconv.FormatInt(variation, 10)}, {"Quantity", strconv.Itoa(qty)}, {"Added", date(added)}}
		})
	collect(exportGroup{ID: "woocommerce-points", Label: "Points and rewards", Description: "Points earned and spent on this site."},
		`SELECT points, type, order_id, note, date FROM goo_points_log WHERE user_id = $1 ORDER BY id`,
		func(r pgxRows) []exportItem {
			var points int
			var typ, note string
			var order int64
			var at time.Time
			if r.Scan(&points, &typ, &order, &note, &at) != nil {
				return nil
			}
			return []exportItem{{"Points", strconv.Itoa(points)}, {"Event", typ}, {"Order ID", strconv.FormatInt(order, 10)}, {"Note", note}, {"Date", date(at)}}
		})
	collect(exportGroup{ID: "woocommerce-subscriptions", Label: "Subscriptions", Description: "Subscriptions held on this site."},
		`SELECT id, status, billing_interval, billing_period, recurring_total::text, currency, payment_method_title, start_date, billing, shipping
		FROM goo_subscriptions WHERE customer_id = $1 ORDER BY id`,
		func(r pgxRows) []exportItem {
			var id int64
			var status, period, total, currency, pm string
			var interval int
			var start time.Time
			var billing, shipping map[string]any
			if r.Scan(&id, &status, &interval, &period, &total, &currency, &pm, &start, &billing, &shipping) != nil {
				return nil
			}
			item := []exportItem{{"Subscription ID", strconv.FormatInt(id, 10)}, {"Status", status},
				{"Billing", total + " " + currency + " every " + strconv.Itoa(interval) + " " + period}, {"Payment Method", pm}, {"Start Date", date(start)}}
			for _, side := range []struct {
				Name string
				M    map[string]any
			}{{"Billing", billing}, {"Shipping", shipping}} {
				for _, f := range [][2]string{{"first_name", "First Name"}, {"last_name", "Last Name"}, {"address_1", "Address 1"}, {"city", "City"},
					{"postcode", "Postcode"}, {"country", "Country"}, {"email", "Email"}, {"phone", "Phone"}} {
					if v, _ := side.M[f[0]].(string); v != "" {
						item = append(item, exportItem{side.Name + " " + f[1], v})
					}
				}
			}
			return item
		})
	collect(exportGroup{ID: "woocommerce-memberships", Label: "Memberships", Description: "Membership plans of the account."},
		`SELECT coalesce(p.name, ''), m.status, m.start_date, m.end_date FROM goo_user_memberships m
		LEFT JOIN goo_membership_plans p ON p.id = m.plan_id WHERE m.user_id = $1 ORDER BY m.id`,
		func(r pgxRows) []exportItem {
			var plan, status string
			var start time.Time
			var end *time.Time
			if r.Scan(&plan, &status, &start, &end) != nil {
				return nil
			}
			e := "Never"
			if end != nil {
				e = date(*end)
			}
			return []exportItem{{"Plan", plan}, {"Status", status}, {"Start Date", date(start)}, {"End Date", e}}
		})
	return groups
}

func gooAccountDataEraser(ctx context.Context, s *Server, email string) eraseResult {
	var res eraseResult
	if !s.gooOn() {
		return res
	}
	uid := gooAccountUserID(ctx, s, email)
	if uid == 0 {
		return res
	}
	if tag, err := s.store.Pool.Exec(ctx, `DELETE FROM goo_wishlists WHERE user_id = $1`, uid); err == nil {
		res.Removed += int(tag.RowsAffected())
	}
	// Ended subscriptions are anonymized when orders are (the same setting); running ones keep billing.
	if s.gooOptBool("erasure_request_removes_order_data") {
		if tag, err := s.store.Pool.Exec(ctx, `UPDATE goo_subscriptions SET billing = '{}', shipping = '{}', date_modified = now()
			WHERE customer_id = $1 AND status NOT IN `+gooOpenSubscriptionStatuses+` AND (billing <> '{}' OR shipping <> '{}')`, uid); err == nil {
			res.Removed += int(tag.RowsAffected())
		}
	}
	var subs, points, members int
	_ = s.store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM goo_subscriptions WHERE customer_id = $1 AND status IN `+gooOpenSubscriptionStatuses+`),
		(SELECT count(*) FROM goo_points_log WHERE user_id = $1), (SELECT count(*) FROM goo_user_memberships WHERE user_id = $1)`, uid).Scan(&subs, &points, &members)
	if subs > 0 {
		res.Retained += subs
		res.Messages = append(res.Messages, "Personal data within active subscriptions has been retained.")
	}
	if points+members > 0 {
		res.Retained += points + members
		res.Messages = append(res.Messages, "Points and memberships belong to the user account and are removed when the account is deleted.")
	}
	return res
}

type pgxRows = pgx.Rows
