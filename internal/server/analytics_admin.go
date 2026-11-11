package server

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// The Analytics screen of Pursae Analytics (analytics.go): every report on one page, and the
// Settings tab.

const analyticsNonce = "pursae-analytics-settings"

// analyticsRange is the period a report covers.
type analyticsRange struct {
	Key, Label string
	From, To   time.Time
	Hourly     bool
}

var analyticsRangeKeys = []struct{ Key, Label string }{
	{"today", "Today"}, {"7d", "Last 7 days"}, {"30d", "Last 30 days"}, {"month", "This month"}, {"90d", "Last 90 days"}, {"12m", "Last 12 months"},
}

// analyticsRangeFor is the period of a range key in the site's time zone; unknown keys are 30 days.
func analyticsRangeFor(key string, now time.Time, loc *time.Location) analyticsRange {
	n := now.In(loc)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
	end := today.AddDate(0, 0, 1)
	r := analyticsRange{Key: key, To: end}
	switch key {
	case "today":
		r.From, r.Hourly = today, true
	case "7d":
		r.From = today.AddDate(0, 0, -6)
	case "month":
		r.From = time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, loc)
	case "90d":
		r.From = today.AddDate(0, 0, -89)
	case "12m":
		r.From = today.AddDate(-1, 0, 1)
	default:
		r.Key, r.From = "30d", today.AddDate(0, 0, -29)
	}
	for _, k := range analyticsRangeKeys {
		if k.Key == r.Key {
			r.Label = k.Label
		}
	}
	return r
}

// analyticsStats are the headline numbers of a period.
type analyticsStats struct {
	Visitors, Visits, Pageviews int64
	Bounces                     int64
	EngagedMs                   float64 // average visible time of a visit
}

func (st analyticsStats) BounceRate() float64 {
	if st.Visits == 0 {
		return 0
	}
	return float64(st.Bounces) * 100 / float64(st.Visits)
}

func (st analyticsStats) ViewsPerVisit() float64 {
	if st.Visits == 0 {
		return 0
	}
	return float64(st.Pageviews) / float64(st.Visits)
}

// analyticsCard is one headline number with its change against the previous period.
type analyticsCard struct {
	Label, Value, Change string
	Up, Good, Flat       bool
}

// analyticsRow is a row of a breakdown table.
type analyticsRow struct {
	Label, Sub, Link string
	Flag             string
	Visitors, Views  int64
	Pct              float64
}

// analyticsTable is one breakdown.
type analyticsTable struct {
	ID, Title, Col string
	Rows           []analyticsRow
	ShowViews      bool
	Empty          string
}

// analyticsVital is a Core Web Vital at the 75th percentile.
type analyticsVital struct {
	Key, Name, Value, Rating, Help string
	Samples                        int64
}

// analyticsVitalPage is a page's p75 vitals.
type analyticsVitalPage struct {
	Path             string
	LCP, INP, CLS    string
	LCPR, INPR, CLSR string
	Samples          int64
}

type analyticsData struct {
	Tab        string
	Ranges     []analyticsRange
	Range      analyticsRange
	Cards      []analyticsCard
	Realtime   int64
	Chart      template.HTML
	Tables     []analyticsTable
	Vitals     []analyticsVital
	VitalPages []analyticsVitalPage
	HasData    bool
	Settings   analyticsSettings
	Nonce      string
	Saved      bool
	CountryOff bool // no visit had a country header: explain where countries come from
}

// analyticsAdmin is /wp-admin/analytics: the overview, or ?tab=settings.
func (s *Server) analyticsAdmin(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("manage_options", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	tab := c.Query("tab")
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, analyticsNonce) {
			s.denyNonce(c)
			return
		}
		if c.PostForm("action") == "reset" {
			_, _ = s.store.Pool.Exec(c, `TRUNCATE analytics_events`)
			c.Redirect(http.StatusFound, "/wp-admin/analytics?tab=settings&reset=1")
			return
		}
		st := analyticsSettings{
			ExcludeLoggedIn: c.PostForm("exclude_logged_in") == "1", ExcludeIPs: c.PostForm("exclude_ips"),
			RespectDNT: c.PostForm("respect_dnt") == "1", RetentionDays: analyticsAtoi(c.PostForm("retention_days"), 395),
			TrackOutbound: c.PostForm("track_outbound") == "1", TrackDownloads: c.PostForm("track_downloads") == "1",
			TrackForms: c.PostForm("track_forms") == "1", Track404: c.PostForm("track_404") == "1",
			Vitals: c.PostForm("vitals") == "1", VitalsSample: analyticsAtoi(c.PostForm("vitals_sample"), 100),
		}
		if err := s.analyticsSave(c, st); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/analytics?tab=settings&saved=1")
		return
	}
	d := analyticsData{Tab: tab, Settings: s.analyticsLoad(), Nonce: s.nonce(c, analyticsNonce), Saved: c.Query("saved") == "1"}
	var notices []notice
	if c.Query("reset") == "1" {
		notices = append(notices, notice{Type: "success", Message: "All statistics were deleted.", Dismissible: true})
	}
	if tab != "settings" {
		d.Tab = ""
		if err := s.analyticsOverview(c, &d, c.Query("range")); err != nil {
			notices = append(notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString("The statistics could not be read: " + err.Error()))})
		}
	}
	s.adminPage(c, analyticsMenuScreen(tab), "Analytics", "toplevel_page_pursae-analytics", "admin/analytics", d, notices...)
}

// analyticsOverview fills the reports of a period.
func (s *Server) analyticsOverview(ctx context.Context, d *analyticsData, key string) error {
	loc := s.loc()
	now := time.Now()
	for _, k := range analyticsRangeKeys {
		d.Ranges = append(d.Ranges, analyticsRangeFor(k.Key, now, loc))
	}
	r := analyticsRangeFor(key, now, loc)
	d.Range = r
	cur, err := s.analyticsStatsFor(ctx, r.From, r.To)
	if err != nil {
		return err
	}
	prevFrom := r.From.Add(-r.To.Sub(r.From))
	prev, _ := s.analyticsStatsFor(ctx, prevFrom, r.From)
	d.HasData = cur.Pageviews > 0
	dur := func(ms float64) string {
		sec := int(ms / 1000)
		if sec < 60 {
			return strconv.Itoa(sec) + "s"
		}
		return fmt.Sprintf("%dm %02ds", sec/60, sec%60)
	}
	card := func(label, value string, curV, prevV float64, higherIsGood bool) analyticsCard {
		cd := analyticsCard{Label: label, Value: value, Flat: true}
		if prevV > 0 {
			ch := (curV - prevV) * 100 / prevV
			cd.Change = fmt.Sprintf("%+.0f%%", ch)
			cd.Flat = math.Abs(ch) < 0.5
			cd.Up = ch > 0
			cd.Good = cd.Up == higherIsGood
		} else if curV > 0 {
			cd.Change, cd.Flat = "new", true
		}
		return cd
	}
	d.Cards = []analyticsCard{
		card("Unique visitors", analyticsNum(cur.Visitors), float64(cur.Visitors), float64(prev.Visitors), true),
		card("Visits", analyticsNum(cur.Visits), float64(cur.Visits), float64(prev.Visits), true),
		card("Page views", analyticsNum(cur.Pageviews), float64(cur.Pageviews), float64(prev.Pageviews), true),
		card("Views per visit", strconv.FormatFloat(math.Round(cur.ViewsPerVisit()*100)/100, 'f', -1, 64), cur.ViewsPerVisit(), prev.ViewsPerVisit(), true),
		card("Bounce rate", fmt.Sprintf("%.0f%%", cur.BounceRate()), cur.BounceRate(), prev.BounceRate(), false),
		card("Visit duration", dur(cur.EngagedMs), cur.EngagedMs, prev.EngagedMs, true),
	}
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(DISTINCT visitor) FROM analytics_events WHERE name = 'pageview' AND ts > now() - interval '5 minutes'`).Scan(&d.Realtime)
	d.Chart, err = s.analyticsChart(ctx, r, loc)
	if err != nil {
		return err
	}
	d.Tables, err = s.analyticsTables(ctx, r)
	if err != nil {
		return err
	}
	for _, t := range d.Tables {
		if t.ID == "countries" && len(t.Rows) == 1 && t.Rows[0].Label == "Unknown" {
			d.CountryOff = true
		}
	}
	d.Vitals, d.VitalPages, err = s.analyticsVitals(ctx, r)
	return err
}

// analyticsVisitsCTE turns the page views of [$1, $2) into visits: a visit ends after 30 minutes
// without a view by the same visitor.
const analyticsVisitsCTE = `WITH pv AS (
	SELECT visitor, ts, path, engaged_ms,
		CASE WHEN lag(ts) OVER w IS NULL OR ts - lag(ts) OVER w > interval '30 minutes' THEN 1 ELSE 0 END AS new_visit
	FROM analytics_events WHERE name = 'pageview' AND ts >= $1 AND ts < $2
	WINDOW w AS (PARTITION BY visitor ORDER BY ts)
), pvn AS (
	SELECT visitor, ts, path, engaged_ms, sum(new_visit) OVER (PARTITION BY visitor ORDER BY ts) AS visit_no FROM pv
), visits AS (
	SELECT visitor, visit_no, count(*) AS views, sum(engaged_ms) AS engaged,
		(array_agg(path ORDER BY ts))[1] AS entry
	FROM pvn GROUP BY visitor, visit_no
)`

func (s *Server) analyticsStatsFor(ctx context.Context, from, to time.Time) (analyticsStats, error) {
	var st analyticsStats
	err := s.store.Pool.QueryRow(ctx, analyticsVisitsCTE+`
		SELECT (SELECT count(DISTINCT visitor) FROM pv), (SELECT count(*) FROM visits), (SELECT count(*) FROM pv),
			(SELECT count(*) FROM visits WHERE views = 1), (SELECT coalesce(avg(engaged), 0) FROM visits)`, from, to).
		Scan(&st.Visitors, &st.Visits, &st.Pageviews, &st.Bounces, &st.EngagedMs)
	return st, err
}

// analyticsTables are the breakdowns of a period.
func (s *Server) analyticsTables(ctx context.Context, r analyticsRange) ([]analyticsTable, error) {
	type spec struct {
		id, title, col, sql, empty string
		views                      bool
		label                      func(string) (string, string)
	}
	base := ` FROM analytics_events WHERE name = 'pageview' AND ts >= $1 AND ts < $2 `
	specs := []spec{
		{id: "pages", title: "Top pages", col: "Page", views: true,
			sql: `SELECT path, count(DISTINCT visitor), count(*)` + base + `GROUP BY 1 ORDER BY 2 DESC, 3 DESC LIMIT 10`},
		{id: "entry", title: "Entry pages", col: "Page",
			sql: analyticsVisitsCTE + ` SELECT entry, count(DISTINCT visitor), count(*) FROM visits GROUP BY 1 ORDER BY 2 DESC, 3 DESC LIMIT 10`},
		{id: "sources", title: "Sources", col: "Source", empty: "Direct / None",
			sql: `SELECT coalesce(nullif(referrer, ''), nullif(utm_source, ''), ''), count(DISTINCT visitor), count(*)` + base + `GROUP BY 1 ORDER BY 2 DESC LIMIT 10`},
		{id: "campaigns", title: "UTM campaigns", col: "Campaign (source / medium)",
			sql: `SELECT utm_campaign || '|' || utm_source || ' / ' || utm_medium, count(DISTINCT visitor), count(*)` + base +
				`AND (utm_campaign <> '' OR utm_source <> '' OR utm_medium <> '') GROUP BY 1 ORDER BY 2 DESC LIMIT 10`,
			label: func(v string) (string, string) {
				camp, sm, _ := strings.Cut(v, "|")
				if camp == "" {
					camp = "(no campaign)"
				}
				return camp, sm
			}},
		{id: "countries", title: "Countries", col: "Country", empty: "Unknown",
			sql: `SELECT country, count(DISTINCT visitor), count(*)` + base + `GROUP BY 1 ORDER BY 2 DESC LIMIT 10`},
		{id: "devices", title: "Devices", col: "Device",
			sql: `SELECT device, count(DISTINCT visitor), count(*)` + base + `GROUP BY 1 ORDER BY 2 DESC LIMIT 10`},
		{id: "browsers", title: "Browsers", col: "Browser",
			sql: `SELECT browser, count(DISTINCT visitor), count(*)` + base + `GROUP BY 1 ORDER BY 2 DESC LIMIT 10`},
		{id: "os", title: "Operating systems", col: "Operating system",
			sql: `SELECT os, count(DISTINCT visitor), count(*)` + base + `GROUP BY 1 ORDER BY 2 DESC LIMIT 10`},
		{id: "goals", title: "Goals", col: "Event", views: true,
			sql: `SELECT name, count(DISTINCT visitor), count(*) FROM analytics_events WHERE name IN ('outbound', 'download', 'form', '404')
				AND ts >= $1 AND ts < $2 GROUP BY 1 ORDER BY 3 DESC`,
			label: func(v string) (string, string) {
				return map[string]string{"outbound": "Outbound link clicks", "download": "File downloads", "form": "Form submissions", "404": "404 – page not found"}[v], ""
			}},
		{id: "outbound", title: "Outbound links", col: "Link", views: true,
			sql: `SELECT target, count(DISTINCT visitor), count(*) FROM analytics_events WHERE name = 'outbound' AND ts >= $1 AND ts < $2 GROUP BY 1 ORDER BY 3 DESC LIMIT 10`},
		{id: "downloads", title: "Downloads", col: "File", views: true,
			sql: `SELECT target, count(DISTINCT visitor), count(*) FROM analytics_events WHERE name = 'download' AND ts >= $1 AND ts < $2 GROUP BY 1 ORDER BY 3 DESC LIMIT 10`},
		{id: "notfound", title: "404 pages", col: "Address", views: true,
			sql: `SELECT target, count(DISTINCT visitor), count(*) FROM analytics_events WHERE name = '404' AND ts >= $1 AND ts < $2 GROUP BY 1 ORDER BY 3 DESC LIMIT 10`},
	}
	var out []analyticsTable
	for _, sp := range specs {
		rows, err := s.store.Pool.Query(ctx, sp.sql, r.From, r.To)
		if err != nil {
			return nil, err
		}
		t := analyticsTable{ID: sp.id, Title: sp.title, Col: sp.col, ShowViews: sp.views}
		var top int64
		for rows.Next() {
			var row analyticsRow
			var v string
			if err := rows.Scan(&v, &row.Visitors, &row.Views); err != nil {
				rows.Close()
				return nil, err
			}
			row.Label = v
			if sp.label != nil {
				row.Label, row.Sub = sp.label(v)
			}
			if row.Label == "" {
				row.Label = sp.empty
			}
			switch sp.id {
			case "pages", "entry", "notfound":
				row.Link = v
			case "outbound", "downloads":
				if strings.HasPrefix(v, "http") {
					row.Link = v
				}
			case "countries":
				if v != "" {
					row.Flag, row.Label = analyticsFlag(v), gooCountryName(v)
				}
			}
			metric := row.Visitors
			if sp.views {
				metric = row.Views
			}
			if metric > top {
				top = metric
			}
			t.Rows = append(t.Rows, row)
		}
		rows.Close()
		for i := range t.Rows {
			metric := t.Rows[i].Visitors
			if sp.views {
				metric = t.Rows[i].Views
			}
			if top > 0 {
				t.Rows[i].Pct = float64(metric) * 100 / float64(top)
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// analyticsVitalDefs are the Core Web Vitals with Google's thresholds (good ≤ first, poor > second).
var analyticsVitalDefs = []struct {
	Key, Col, Name, Help string
	Good, Poor           float64
	CLS                  bool
}{
	{"lcp", "lcp_ms", "Largest Contentful Paint (LCP)", "Loading: when the main content appeared. Good ≤ 2.5 s.", 2500, 4000, false},
	{"inp", "inp_ms", "Interaction to Next Paint (INP)", "Responsiveness: the slowest reaction to a click, tap or key. Good ≤ 200 ms.", 200, 500, false},
	{"cls", "cls", "Cumulative Layout Shift (CLS)", "Visual stability: how much the page jumped while loading. Good ≤ 0.1.", 0.1, 0.25, true},
	{"fcp", "fcp_ms", "First Contentful Paint (FCP)", "When the first text or image appeared. Good ≤ 1.8 s.", 1800, 3000, false},
	{"ttfb", "ttfb_ms", "Time to First Byte (TTFB)", "How fast the server answered. Good ≤ 0.8 s.", 800, 1800, false},
}

func analyticsRate(v, good, poor float64) string {
	switch {
	case v <= good:
		return "good"
	case v <= poor:
		return "needs-improvement"
	}
	return "poor"
}

func analyticsVitalText(v float64, cls bool) string {
	if cls {
		return strconv.FormatFloat(math.Round(v*1000)/1000, 'f', -1, 64)
	}
	if v >= 1000 {
		return strconv.FormatFloat(math.Round(v/10)/100, 'f', -1, 64) + " s"
	}
	return strconv.Itoa(int(v+0.5)) + " ms"
}

// analyticsVitals are the p75 Core Web Vitals of the period, site-wide and for the pages with the
// slowest LCP (pages with at least 3 measured views).
func (s *Server) analyticsVitals(ctx context.Context, r analyticsRange) ([]analyticsVital, []analyticsVitalPage, error) {
	var vitals []analyticsVital
	for _, v := range analyticsVitalDefs {
		var p75 *float64
		var n int64
		if err := s.store.Pool.QueryRow(ctx, `SELECT percentile_cont(0.75) WITHIN GROUP (ORDER BY `+v.Col+`), count(`+v.Col+`)
			FROM analytics_events WHERE name = 'pageview' AND ts >= $1 AND ts < $2 AND `+v.Col+` IS NOT NULL`, r.From, r.To).Scan(&p75, &n); err != nil {
			return nil, nil, err
		}
		av := analyticsVital{Key: v.Key, Name: v.Name, Help: v.Help, Samples: n, Value: "—", Rating: "none"}
		if p75 != nil && n > 0 {
			av.Value, av.Rating = analyticsVitalText(*p75, v.CLS), analyticsRate(*p75, v.Good, v.Poor)
		}
		vitals = append(vitals, av)
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT path,
			percentile_cont(0.75) WITHIN GROUP (ORDER BY lcp_ms), percentile_cont(0.75) WITHIN GROUP (ORDER BY inp_ms),
			percentile_cont(0.75) WITHIN GROUP (ORDER BY cls), count(lcp_ms)
		FROM analytics_events WHERE name = 'pageview' AND ts >= $1 AND ts < $2 AND lcp_ms IS NOT NULL
		GROUP BY path HAVING count(lcp_ms) >= 3 ORDER BY 2 DESC LIMIT 10`, r.From, r.To)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var pages []analyticsVitalPage
	for rows.Next() {
		var p analyticsVitalPage
		var lcp, inp, cls *float64
		if err := rows.Scan(&p.Path, &lcp, &inp, &cls, &p.Samples); err != nil {
			return nil, nil, err
		}
		one := func(v *float64, i int) (string, string) {
			if v == nil {
				return "—", "none"
			}
			d := analyticsVitalDefs[i]
			return analyticsVitalText(*v, d.CLS), analyticsRate(*v, d.Good, d.Poor)
		}
		p.LCP, p.LCPR = one(lcp, 0)
		p.INP, p.INPR = one(inp, 1)
		p.CLS, p.CLSR = one(cls, 2)
		pages = append(pages, p)
	}
	return vitals, pages, rows.Err()
}

// analyticsChart draws visitors (area) and page views (line) per day, or per hour for Today, as an
// inline SVG: no chart library, readable without JavaScript.
func (s *Server) analyticsChart(ctx context.Context, r analyticsRange, loc *time.Location) (template.HTML, error) {
	step := 24 * time.Hour
	if r.Hourly {
		step = time.Hour
	}
	_, offset := time.Now().In(loc).Zone()
	rows, err := s.store.Pool.Query(ctx, `SELECT floor((extract(epoch FROM ts) + $3) / $4)::bigint AS b, count(DISTINCT visitor), count(*)
		FROM analytics_events WHERE name = 'pageview' AND ts >= $1 AND ts < $2 GROUP BY 1`, r.From, r.To, offset, int64(step/time.Second))
	if err != nil {
		return "", err
	}
	type pt struct{ v, p int64 }
	got := map[int64]pt{}
	for rows.Next() {
		var b int64
		var p pt
		if err := rows.Scan(&b, &p.v, &p.p); err != nil {
			rows.Close()
			return "", err
		}
		got[b] = p
	}
	rows.Close()
	var labels []string
	var pts []pt
	for t := r.From; t.Before(r.To); {
		b := (t.Unix() + int64(offset)) / int64(step/time.Second)
		pts = append(pts, got[b])
		if r.Hourly {
			labels = append(labels, t.Format("15:00"))
			t = t.Add(time.Hour)
		} else {
			labels = append(labels, t.Format("Jan 2"))
			t = t.AddDate(0, 0, 1)
		}
	}
	if len(pts) == 0 {
		return "", nil
	}
	const W, H, padL, padR, padT, padB = 1000.0, 240.0, 44.0, 12.0, 12.0, 28.0
	var top int64 = 1
	for _, p := range pts {
		top = max(top, p.p, p.v)
	}
	// A round scale: 1, 2 or 5 times a power of ten above the largest value.
	scale := math.Pow(10, math.Floor(math.Log10(float64(top))))
	for _, m := range []float64{1, 2, 5, 10} {
		if m*scale >= float64(top) {
			scale *= m
			break
		}
	}
	x := func(i int) float64 {
		if len(pts) == 1 {
			return padL + (W-padL-padR)/2
		}
		return padL + float64(i)*(W-padL-padR)/float64(len(pts)-1)
	}
	y := func(v int64) float64 { return padT + (H-padT-padB)*(1-float64(v)/scale) }
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="pa-chart-svg" viewBox="0 0 %.0f %.0f" preserveAspectRatio="none" role="img" aria-label="Visitors and page views, %s">`, W, H, html.EscapeString(r.Label))
	for i := 0; i <= 4; i++ {
		v := int64(scale * float64(i) / 4)
		yy := y(v)
		fmt.Fprintf(&b, `<line x1="%.0f" x2="%.0f" y1="%.1f" y2="%.1f" class="pa-grid"/><text x="%.0f" y="%.1f" class="pa-axis" text-anchor="end">%s</text>`,
			padL, W-padR, yy, yy, padL-6, yy+4, analyticsNum(v))
	}
	var area, line strings.Builder
	fmt.Fprintf(&area, "M%.1f %.1f", x(0), y(0))
	for i, p := range pts {
		fmt.Fprintf(&area, " L%.1f %.1f", x(i), y(p.v))
		if i == 0 {
			fmt.Fprintf(&line, "M%.1f %.1f", x(i), y(p.p))
		} else {
			fmt.Fprintf(&line, " L%.1f %.1f", x(i), y(p.p))
		}
	}
	fmt.Fprintf(&area, " L%.1f %.1f Z", x(len(pts)-1), y(0))
	fmt.Fprintf(&b, `<path d="%s" class="pa-area"/><path d="%s" class="pa-line"/>`, area.String(), line.String())
	every := max(1, len(pts)/8)
	for i, p := range pts {
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="3" class="pa-dot"><title>%s: %s visitors, %s page views</title></circle>`,
			x(i), y(p.v), html.EscapeString(labels[i]), analyticsNum(p.v), analyticsNum(p.p))
		if i%every == 0 || i == len(pts)-1 {
			fmt.Fprintf(&b, `<text x="%.1f" y="%.0f" class="pa-axis" text-anchor="middle">%s</text>`, x(i), H-8, html.EscapeString(labels[i]))
		}
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String()), nil
}

// analyticsNum prints a count with thousands separators (12,345), 250k from 100,000 and 1.2M from a million.
func analyticsNum(n int64) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(math.Round(float64(n)/1e5)/10, 'f', -1, 64) + "M"
	case n >= 100_000:
		return strconv.FormatFloat(math.Round(float64(n)/100)/10, 'f', -1, 64) + "k"
	}
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// analyticsFlag is the flag emoji of a country code.
func analyticsFlag(cc string) string {
	if len(cc) != 2 {
		return ""
	}
	return string([]rune{rune(cc[0]-'A') + 0x1F1E6, rune(cc[1]-'A') + 0x1F1E6})
}

// pursaeAnalytics is GET /pursae/v1/analytics?range=7d: the overview as JSON (AI tools, MCP,
// dashboards elsewhere). 404 while the plugin is off.
func (s *Server) pursaeAnalytics(c *gin.Context, _ *restRequest) {
	if !s.analyticsOn() {
		restErr(c, http.StatusNotFound, "rest_no_route", "Pursae Analytics is not active.")
		return
	}
	var d analyticsData
	if err := s.analyticsOverview(c, &d, c.Query("range")); err != nil {
		restErr(c, http.StatusInternalServerError, "pursae_analytics_error", err.Error())
		return
	}
	cards := map[string]string{}
	for _, cd := range d.Cards {
		cards[cd.Label] = cd.Value
	}
	tables := map[string]any{}
	for _, t := range d.Tables {
		rows := make([]map[string]any, 0, len(t.Rows))
		for _, r := range t.Rows {
			row := map[string]any{"label": r.Label, "visitors": r.Visitors, "views": r.Views}
			if r.Sub != "" {
				row["detail"] = r.Sub
			}
			rows = append(rows, row)
		}
		tables[t.ID] = rows
	}
	vitals := map[string]any{}
	for _, v := range d.Vitals {
		vitals[v.Key] = map[string]any{"p75": v.Value, "rating": v.Rating, "samples": v.Samples}
	}
	restJSON(c, http.StatusOK, map[string]any{"range": d.Range.Key, "from": d.Range.From, "to": d.Range.To,
		"realtime_visitors": d.Realtime, "summary": cards, "reports": tables, "web_vitals": vitals})
}
