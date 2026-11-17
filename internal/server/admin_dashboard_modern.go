package server

import (
	"context"
	"fmt"
	"html/template"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"wpgo/internal/store"
)

// The Pursae Modern dashboard (admin_theme.go): a greeting with setup steps, metric tiles fed by the
// plugins that are on (Analytics visitors, Commerce sales and orders, Forum activity, comments), a
// 30-day traffic chart, the latest orders and the top pages. It sits above the usual dashboard boxes,
// which keep working as before. Nothing here is computed for WordPress Classic users.

type pmDash struct {
	Greeting, Weekday, Date string
	Steps                   []pmStep
	StepsDone, StepsPct     int
	Tiles                   []pmTile
	Chart                   *pmChart
	AnalyticsOff            bool // show the "turn on Pursae Analytics" card instead of the chart
	Health                  healthSummary
	HealthPct               int
	HealthSegments          []bool
	Orders                  []pmOrder
	TopPages                []pmRank
	ActivePlugins           int
}

type pmStep struct {
	Label, Hint, URL string
	Done             bool
}

type pmTile struct {
	Label, Icon, URL string
	Value            template.HTML
	Delta            string
	Up, Flat         bool
	Spark            string // polyline points in a 90×28 box
}

type pmChart struct {
	W, H              int
	Line, Area, Prev  string
	YTicks            []pmTick
	XTicks            []pmTick
	Total, PrevTotal  int64
	Delta             string
	Up                bool
	Points            []pmPoint // hover targets
}

type pmTick struct {
	Pos  float64
	Text string
}

type pmPoint struct {
	X, Y  float64
	Label string
	Value int64
}

type pmOrder struct {
	Number, Customer, Initials, Status, StatusClass, When, URL string
	Total                                                      template.HTML
}

type pmRank struct {
	Label string
	Value int64
	Pct   int
}

// modernDashboard gathers the figures of the Pursae Modern dashboard for user u.
func (s *Server) modernDashboard(ctx context.Context, u *store.User, health healthSummary, posts, pages, comments, pending int) *pmDash {
	loc := s.loc()
	now := time.Now().In(loc)
	d := &pmDash{Health: health}
	switch h := now.Hour(); {
	case h < 5:
		d.Greeting = "Good evening"
	case h < 12:
		d.Greeting = "Good morning"
	case h < 18:
		d.Greeting = "Good afternoon"
	default:
		d.Greeting = "Good evening"
	}
	d.Weekday = now.Weekday().String()
	d.Date = now.Format("02.01.2006")

	on := []bool{s.acfgoOn(), s.adminsecOn(), s.analyticsOn(), s.bmgrOn(), s.forumOn(), s.gocdnOn(), s.golylangOn(), s.gontactOn(),
		s.gooOn(), s.gooikeOn(), s.hgOn(), s.mailSMTPOn(), s.pixelOn(), s.seogoOn(), s.wbOn(), s.pageCacheOn()}
	for _, v := range on {
		if v {
			d.ActivePlugins++
		}
	}

	// Setup steps: each one is checked against the site, not stored.
	theme := s.activeTheme().Slug
	host := ""
	if su, err := url.Parse(s.settings.String("siteurl")); err == nil {
		host = su.Hostname()
	}
	local := host == "" || host == "localhost" || host == "127.0.0.1" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".test")
	d.Steps = []pmStep{
		{Label: "Install Pursae and pick a language", Done: true, URL: "/wp-admin/options-general"},
		{Label: "Switch on the plugins you need", Hint: strconv.Itoa(d.ActivePlugins) + "/" + strconv.Itoa(len(on)), Done: d.ActivePlugins >= 3, URL: "/wp-admin/plugins"},
		{Label: "Choose your site's theme", Done: theme != "" && theme != "hello-pursae" && theme != "twentytwentyfive", URL: "/wp-admin/themes"},
		{Label: "Write your first post", Done: posts > 1, URL: "/wp-admin/post-new"},
		{Label: "Connect your domain", Hint: "~2 min", Done: !local, URL: "/wp-admin/options-general"},
	}
	for _, st := range d.Steps {
		if st.Done {
			d.StepsDone++
		}
	}
	d.StepsPct = d.StepsDone * 100 / len(d.Steps)

	if health.Good+health.Recommended+health.Critical > 0 {
		d.HealthPct = (health.Good*100 + health.Recommended*50) / (health.Good + health.Recommended + health.Critical)
	}
	d.HealthSegments = make([]bool, 20)
	for i := range d.HealthSegments {
		d.HealthSegments[i] = i < d.HealthPct/5
	}

	// Tiles: plugin figures first, content counts fill the row up to four.
	week := now.AddDate(0, 0, -7)
	prevWeek := now.AddDate(0, 0, -14)
	_, offset := now.Zone()
	if s.analyticsOn() && u.Can("manage_options", nil) {
		cur, _ := s.analyticsStatsFor(ctx, week, now)
		prev, _ := s.analyticsStatsFor(ctx, prevWeek, week)
		views, visitors := s.pmAnalyticsDaily(ctx, now.AddDate(0, 0, -6), 7, offset)
		_ = views
		d.Tiles = append(d.Tiles, pmTileFrom("Visitors", "dashicons-visibility", "/wp-admin/analytics", template.HTML(pmInt(cur.Visitors)), float64(cur.Visitors), float64(prev.Visitors), visitors))
		d.Chart = s.pmTrafficChart(ctx, now, offset)
		d.TopPages = s.pmTopPages(ctx, week, now)
	} else if u.Can("activate_plugins", nil) {
		d.AnalyticsOff = true
	}
	if s.gooOn() && u.Can("manage_options", nil) {
		cur, prev := s.gooTotals(ctx, week, now), s.gooTotals(ctx, prevWeek, week)
		sales, orders := s.pmOrdersDaily(ctx, now.AddDate(0, 0, -6), 7, offset)
		d.Tiles = append(d.Tiles,
			pmTileFrom("Sales", "dashicons-cart", "/wp-admin/goocommerce?page=analytics&path=revenue", s.gooPrice(cur.Gross), cur.Gross, prev.Gross, sales),
			pmTileFrom("Orders", "dashicons-clipboard", "/wp-admin/goocommerce?page=orders", template.HTML(strconv.Itoa(cur.Orders)), float64(cur.Orders), float64(prev.Orders), orders))
		d.Orders = s.pmRecentOrders(ctx, 5)
	}
	if s.forumOn() && len(d.Tiles) < 4 {
		cur, prev, daily := s.pmCountByDay(ctx, `SELECT published_at FROM posts WHERE type IN ('topic','reply') AND status = 'publish' AND published_at >= $1 AND published_at < $2`, now, offset)
		d.Tiles = append(d.Tiles, pmTileFrom("Forum posts", "dashicons-groups", "/wp-admin/forum?tab=overview", template.HTML(pmInt(cur)), float64(cur), float64(prev), daily))
	}
	if len(d.Tiles) < 4 {
		cur, prev, daily := s.pmCountByDay(ctx, `SELECT created_at FROM comments WHERE created_at >= $1 AND created_at < $2`, now, offset)
		t := pmTileFrom("Comments", "dashicons-admin-comments", "/wp-admin/edit-comments", template.HTML(pmInt(cur)), float64(cur), float64(prev), daily)
		d.Tiles = append(d.Tiles, t)
	}
	for _, extra := range []struct {
		label, icon, url string
		n               int
	}{{"Published posts", "dashicons-admin-post", "/wp-admin/edit", posts}, {"Pages", "dashicons-admin-page", "/wp-admin/edit?post_type=page", pages}, {"Awaiting moderation", "dashicons-format-chat", "/wp-admin/edit-comments?comment_status=moderated", pending}} {
		if len(d.Tiles) >= 4 {
			break
		}
		d.Tiles = append(d.Tiles, pmTile{Label: extra.label, Icon: extra.icon, URL: extra.url, Value: template.HTML(strconv.Itoa(extra.n)), Flat: true})
	}
	_ = comments
	return d
}

func pmTileFrom(label, icon, link string, value template.HTML, cur, prev float64, daily []float64) pmTile {
	t := pmTile{Label: label, Icon: icon, URL: link, Value: value, Spark: pmSpark(daily)}
	switch {
	case prev == 0 && cur == 0:
		t.Flat, t.Delta = true, "0%"
	case prev == 0:
		t.Up, t.Delta = true, "new"
	default:
		ch := (cur - prev) / prev * 100
		t.Up = ch >= 0
		t.Delta = strconv.FormatFloat(math.Abs(ch), 'f', 1, 64) + "%"
	}
	return t
}

func pmInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	if n < 1000 {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func pmSpark(v []float64) string {
	if len(v) < 2 {
		return ""
	}
	lo, hi := v[0], v[0]
	for _, x := range v {
		lo, hi = math.Min(lo, x), math.Max(hi, x)
	}
	pts := make([]string, len(v))
	for i, x := range v {
		y := 14.0
		if hi > lo {
			y = 26 - (x-lo)/(hi-lo)*22
		}
		pts[i] = fmt.Sprintf("%.1f,%.1f", float64(i)/float64(len(v)-1)*90, y)
	}
	return strings.Join(pts, " ")
}

// pmDayIndex buckets t into day i of [from, from+days) in the site's time zone (offset seconds east of UTC).
func pmDays(from time.Time, days int) (time.Time, time.Time) {
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	return start, start.AddDate(0, 0, days)
}

// pmAnalyticsDaily returns page views and unique visitors per day for days days from from.
func (s *Server) pmAnalyticsDaily(ctx context.Context, from time.Time, days, offset int) (views, visitors []float64) {
	start, end := pmDays(from, days)
	views, visitors = make([]float64, days), make([]float64, days)
	rows, err := s.store.Pool.Query(ctx, `SELECT ((ts + make_interval(secs => $3)) AT TIME ZONE 'UTC')::date AS day, count(*), count(DISTINCT visitor)
		FROM analytics_events WHERE name = 'pageview' AND ts >= $1 AND ts < $2 GROUP BY 1`, start, end, offset)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var day time.Time
		var v, u int64
		if rows.Scan(&day, &v, &u) != nil {
			continue
		}
		i := int(time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, start.Location()).Sub(start).Hours() / 24)
		if i >= 0 && i < days {
			views[i], visitors[i] = float64(v), float64(u)
		}
	}
	return
}

// pmOrdersDaily returns paid sales and order counts per day.
func (s *Server) pmOrdersDaily(ctx context.Context, from time.Time, days, offset int) (sales, orders []float64) {
	start, end := pmDays(from, days)
	sales, orders = make([]float64, days), make([]float64, days)
	rows, err := s.store.Pool.Query(ctx, `SELECT ((date_created + make_interval(secs => $4)) AT TIME ZONE 'UTC')::date, coalesce(sum(total),0)::float8, count(*)
		FROM goo_orders WHERE type = 'shop_order' AND status = ANY($1) AND date_created >= $2 AND date_created < $3 GROUP BY 1`, gooPaidList, start, end, offset)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var day time.Time
		var total float64
		var n int64
		if rows.Scan(&day, &total, &n) != nil {
			continue
		}
		i := int(time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, start.Location()).Sub(start).Hours() / 24)
		if i >= 0 && i < days {
			sales[i], orders[i] = total, float64(n)
		}
	}
	return
}

// pmCountByDay counts the rows of q (selecting one timestamp, with $1/$2 as the range) in the last
// 7 days, the 7 before, and per day of the last 7.
func (s *Server) pmCountByDay(ctx context.Context, q string, now time.Time, offset int) (cur, prev int64, daily []float64) {
	start, end := pmDays(now.AddDate(0, 0, -6), 7)
	daily = make([]float64, 7)
	rows, err := s.store.Pool.Query(ctx, q, start.AddDate(0, 0, -7), end)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var ts time.Time
		if rows.Scan(&ts) != nil {
			continue
		}
		ts = ts.In(start.Location())
		if ts.Before(start) {
			prev++
			continue
		}
		cur++
		if i := int(ts.Sub(start).Hours() / 24); i >= 0 && i < 7 {
			daily[i]++
		}
	}
	return
}

// pmTrafficChart is the last 30 days of page views (line and area) over the 30 days before (dashed).
func (s *Server) pmTrafficChart(ctx context.Context, now time.Time, offset int) *pmChart {
	const days, W, H = 30, 640, 240
	const padL, padR, padT, padB = 44.0, 10.0, 10.0, 26.0
	from := now.AddDate(0, 0, -(days - 1))
	cur, _ := s.pmAnalyticsDaily(ctx, from, days, offset)
	prev, _ := s.pmAnalyticsDaily(ctx, from.AddDate(0, 0, -days), days, offset)
	c := &pmChart{W: W, H: H}
	hi := 1.0
	for i := range cur {
		c.Total += int64(cur[i])
		c.PrevTotal += int64(prev[i])
		hi = math.Max(hi, math.Max(cur[i], prev[i]))
	}
	hi = pmNiceCeil(hi * 1.1)
	x := func(i int) float64 { return padL + float64(i)/float64(days-1)*(W-padL-padR) }
	y := func(v float64) float64 { return padT + (1-v/hi)*(H-padT-padB) }
	path := func(v []float64) string {
		var b strings.Builder
		for i := range v {
			if i == 0 {
				fmt.Fprintf(&b, "M%.1f,%.1f", x(i), y(v[i]))
				continue
			}
			cx := (x(i-1) + x(i)) / 2
			fmt.Fprintf(&b, "C%.1f,%.1f %.1f,%.1f %.1f,%.1f", cx, y(v[i-1]), cx, y(v[i]), x(i), y(v[i]))
		}
		return b.String()
	}
	c.Line, c.Prev = path(cur), path(prev)
	c.Area = c.Line + fmt.Sprintf("L%.1f,%.1fL%.1f,%.1fZ", x(days-1), y(0), x(0), y(0))
	for _, f := range []float64{0, .25, .5, .75, 1} {
		c.YTicks = append(c.YTicks, pmTick{Pos: y(hi * f), Text: pmInt(int64(hi * f))})
	}
	for i := 0; i < days; i += 5 {
		c.XTicks = append(c.XTicks, pmTick{Pos: x(i), Text: from.AddDate(0, 0, i).Format("02.01")})
	}
	c.XTicks = append(c.XTicks, pmTick{Pos: x(days - 1), Text: from.AddDate(0, 0, days-1).Format("02.01")})
	for i := range cur {
		c.Points = append(c.Points, pmPoint{X: x(i), Y: y(cur[i]), Label: from.AddDate(0, 0, i).Format("02.01.2006"), Value: int64(cur[i])})
	}
	t := pmTileFrom("", "", "", "", float64(c.Total), float64(c.PrevTotal), nil)
	c.Delta, c.Up = t.Delta, t.Up
	return c
}

func pmNiceCeil(v float64) float64 {
	if v <= 0 {
		return 1
	}
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if m*p >= v {
			return m * p
		}
	}
	return 10 * p
}

func (s *Server) pmTopPages(ctx context.Context, from, to time.Time) []pmRank {
	rows, err := s.store.Pool.Query(ctx, `SELECT path, count(*) FROM analytics_events WHERE name = 'pageview' AND ts >= $1 AND ts < $2
		GROUP BY path ORDER BY 2 DESC, 1 LIMIT 5`, from, to)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []pmRank
	var top int64
	for rows.Next() {
		var r pmRank
		if rows.Scan(&r.Label, &r.Value) != nil {
			continue
		}
		if top == 0 {
			top = r.Value
		}
		if top > 0 {
			r.Pct = int(r.Value * 100 / top)
		}
		out = append(out, r)
	}
	return out
}

func (s *Server) pmRecentOrders(ctx context.Context, n int) []pmOrder {
	rows, err := s.store.Pool.Query(ctx, `SELECT id, status, total::float8, currency, billing->>'first_name', billing->>'last_name', date_created
		FROM goo_orders WHERE type = 'shop_order' AND status <> 'checkout-draft' ORDER BY date_created DESC, id DESC LIMIT $1`, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	loc := s.loc()
	var out []pmOrder
	for rows.Next() {
		var id int64
		var status, currency string
		var first, last *string
		var total float64
		var at time.Time
		if rows.Scan(&id, &status, &total, &currency, &first, &last, &at) != nil {
			continue
		}
		name := strings.TrimSpace(deref(first) + " " + deref(last))
		if name == "" {
			name = "Guest"
		}
		ini := ""
		for _, w := range strings.Fields(name) {
			ini += string([]rune(w)[:1])
		}
		if r := []rune(ini); len(r) > 2 {
			ini = string(r[:2])
		}
		cls := map[string]string{"completed": "ok", "processing": "info", "on-hold": "warn", "pending": "warn", "refunded": "bad", "failed": "bad", "cancelled": "muted"}[status]
		out = append(out, pmOrder{
			Number: "#" + itoa64(id), Customer: name, Initials: strings.ToUpper(ini), Status: gooStatusLabel(status), StatusClass: cls,
			When: at.In(loc).Format("02.01 15:04"), URL: "/wp-admin/goocommerce?page=orders&action=edit&id=" + itoa64(id),
			Total: s.gooPriceIn(total, currency),
		})
	}
	return out
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
