package server

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Helpers shared by the Subscriptions, Memberships, Pre-Orders and Points and Rewards modules
// (prefix gxa so they never clash with other modules' helpers).

// gxaGin is the request's gin context when ctx is one (the storefront passes c to cart calls).
func gxaGin(ctx context.Context) *gin.Context {
	if gc, ok := ctx.(*gin.Context); ok {
		return gc
	}
	if cart := gooCartFromCtx(ctx); cart != nil && cart.c != nil {
		return cart.c
	}
	if rc := renderFrom(ctx); rc != nil && rc.c != nil {
		return rc.c
	}
	return nil
}

// gxaUser is the logged-in user of a hook's context: the request's user, the plugin user carried by
// the request context, or the user of the content being rendered.
func gxaUser(ctx context.Context) *store.User {
	if ctx == nil {
		return nil
	}
	if cart := gooCartFromCtx(ctx); cart != nil && cart.c != nil {
		ctx = cart.c
	}
	if gc, ok := ctx.(*gin.Context); ok {
		if u := currentUser(gc); u != nil {
			return u
		}
		if gc.Request != nil {
			ctx = gc.Request.Context()
		}
	}
	if u := pluginUserFrom(ctx); u != nil {
		return u
	}
	if rc := renderFrom(ctx); rc != nil {
		if rc.user != nil {
			return rc.user
		}
		if rc.c != nil {
			return currentUser(rc.c)
		}
	}
	return nil
}

// gxaCart is the request's cart when the hook runs inside one (add-to-cart hooks receive no cart).
func gxaCart(ctx context.Context) *gooCart {
	if c := gooCartFromCtx(ctx); c != nil {
		return c
	}
	if gc := gxaGin(ctx); gc != nil {
		if v, ok := gc.Get("goo.cart"); ok {
			if cart, ok := v.(*gooCart); ok {
				return cart
			}
		}
	}
	return nil
}

func gxaStr(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case bool:
		if v {
			return "yes"
		}
		return "no"
	}
	return ""
}

func gxaFloat(m map[string]any, k string) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f
	}
	return 0
}

func gxaInt(m map[string]any, k string) int { return int(gxaFloat(m, k)) }

func gxaInt64(m map[string]any, k string) int64 { return int64(gxaFloat(m, k)) }

// gxaParseIDs reads "1, 2,3" (or a posted list) as ids.
func gxaParseIDs(vals ...string) []int64 {
	var out []int64
	seen := map[int64]bool{}
	for _, v := range vals {
		for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' }) {
			if n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil && n > 0 && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

func gxaJoinIDs(ids []int64) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, itoa64(id))
	}
	return strings.Join(parts, ", ")
}

func gxaHasID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// gxaDate formats a date with the site's date format ("–" when there is none).
func (s *Server) gxaDate(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "&ndash;"
	}
	return gooEsc(wp.FormatDate(t.In(s.loc()), s.settings.String("date_format")))
}

// gxaDateTime formats a date and time with the site's formats.
func (s *Server) gxaDateTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "&ndash;"
	}
	return gooEsc(wp.FormatDate(t.In(s.loc()), s.settings.String("date_format")+" "+s.settings.String("time_format")))
}

// gxaInputDate is a date for an <input type="date"> / "datetime-local" in the site's time zone.
func (s *Server) gxaInputDate(t *time.Time, withTime bool) string {
	if t == nil || t.IsZero() {
		return ""
	}
	if withTime {
		return t.In(s.loc()).Format("2006-01-02T15:04")
	}
	return t.In(s.loc()).Format("2006-01-02")
}

// gxaParseTime reads a posted date ("2026-01-02", "2026-01-02 15:04", "2026-01-02T15:04") in the
// site's time zone; nil for an empty or invalid value.
func (s *Server) gxaParseTime(v string) *time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	for _, f := range []string{"2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(f, v, s.loc()); err == nil {
			return &t
		}
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
		t := time.Unix(n, 0)
		return &t
	}
	return nil
}

// gxaAddPeriod adds n periods (day, week, month, year; plurals accepted) to a time. Months and
// years clamp to the last day of the target month like wcs_add_months (Jan 31 + 1 month = Feb 28,
// Feb 29 + 1 year = Feb 28) instead of overflowing into the next month as time.AddDate does.
func gxaAddPeriod(t time.Time, period string, n int) time.Time {
	switch strings.TrimSuffix(period, "s") {
	case "day":
		return t.AddDate(0, 0, n)
	case "week":
		return t.AddDate(0, 0, 7*n)
	case "year":
		return gxaAddMonths(t, 12*n)
	}
	return gxaAddMonths(t, n)
}

// gxaAddMonths adds n calendar months keeping the wall clock and clamping the day to the target month.
func gxaAddMonths(t time.Time, n int) time.Time {
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, n, 0)
	day := min(t.Day(), gooSubsDaysIn(first.Month(), first.Year()))
	return time.Date(first.Year(), first.Month(), day, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// gxaPeriodLabel is "month" / "6 months".
func gxaPeriodLabel(period string, n int) string {
	period = strings.TrimSuffix(period, "s")
	if n == 1 {
		return period
	}
	return strconv.Itoa(n) + " " + period + "s"
}

// gxaOrdinal is "2nd", "3rd", "11th".
func gxaOrdinal(n int) string {
	suf := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suf = "st"
		case 2:
			suf = "nd"
		case 3:
			suf = "rd"
		}
	}
	return strconv.Itoa(n) + suf
}

// gxaSelect renders a <select>.
func gxaSelect(name, id string, opts []gooOpt, val string, attrs string) string {
	var b strings.Builder
	b.WriteString(`<select name="` + gooEsc(name) + `"`)
	if id != "" {
		b.WriteString(` id="` + gooEsc(id) + `"`)
	}
	b.WriteString(attrs + `>`)
	for _, o := range opts {
		sel := ""
		if o.Value == val {
			sel = ` selected="selected"`
		}
		b.WriteString(`<option value="` + gooEsc(o.Value) + `"` + sel + `>` + gooEsc(o.Label) + `</option>`)
	}
	b.WriteString(`</select>`)
	return b.String()
}

// gxaAdminRender renders a module admin screen through its goo-ext-<module> template.
func (s *Server) gxaAdminRender(c *gin.Context, page, title, tmpl string, head, body string) {
	s.adminPage(c, "goocommerce?page="+page, title, "goocommerce_page_goo-"+page, "admin/"+tmpl,
		map[string]any{"Head": template.HTML(head), "Body": template.HTML(body)}, gooFlash(c)...)
}

// gxaAdminRedirect goes back to a module screen with a message (or an error).
func gxaAdminRedirect(c *gin.Context, page, params, msg string, isErr bool) {
	u := "/wp-admin/goocommerce?page=" + page
	if params != "" {
		u += "&" + params
	}
	if msg != "" {
		key := "goo-msg"
		if isErr {
			key = "goo-error"
		}
		u += "&" + key + "=" + url.QueryEscape(msg)
	}
	c.Redirect(http.StatusFound, u)
}

// gxaUserLabel is "Display Name (email)" for admin lists, with a link to the user.
func (s *Server) gxaUserLabel(ctx context.Context, id int64, link bool) string {
	if id == 0 {
		return "Guest"
	}
	u, err := s.store.UserByID(ctx, id)
	if err != nil {
		return "#" + itoa64(id)
	}
	name := gooEsc(defaultStr(u.DisplayName, u.Login))
	if link {
		name = `<a href="/wp-admin/user-edit?user_id=` + itoa64(id) + `">` + name + `</a>`
	}
	return name + ` <span class="description">(` + gooEsc(u.Email) + `)</span>`
}

// gxaFindUser finds a user by id, login or e-mail (admin forms).
func (s *Server) gxaFindUser(ctx context.Context, v string) (*store.User, error) {
	v = strings.TrimSpace(v)
	if id, err := strconv.ParseInt(v, 10, 64); err == nil {
		return s.store.UserByID(ctx, id)
	}
	return s.store.UserByLoginOrEmail(ctx, v)
}

// gxaViews renders the status views of a list ("All (3) | Active (2)").
func gxaViews(base string, current string, views []gooOpt, counts map[string]int) string {
	var b strings.Builder
	b.WriteString(`<ul class="subsubsub">`)
	total := 0
	for _, n := range counts {
		total += n
	}
	items := []string{}
	cls := ""
	if current == "" {
		cls = ` class="current" aria-current="page"`
	}
	items = append(items, fmt.Sprintf(`<li class="all"><a href="%s"%s>All <span class="count">(%d)</span></a>`, html.EscapeString(base), cls, total))
	for _, v := range views {
		if counts[v.Value] == 0 {
			continue
		}
		cls = ""
		if current == v.Value {
			cls = ` class="current" aria-current="page"`
		}
		items = append(items, fmt.Sprintf(`<li class="%s"><a href="%s&amp;status=%s"%s>%s <span class="count">(%d)</span></a>`,
			gooEsc(v.Value), html.EscapeString(base), url.QueryEscape(v.Value), cls, gooEsc(v.Label), counts[v.Value]))
	}
	b.WriteString(strings.Join(items, " |</li>"))
	b.WriteString(`</li></ul>`)
	return b.String()
}

// gxaNote is a note on a module record (subscription notes, membership notes).
func gxaNoteList(v any) []map[string]any {
	var out []map[string]any
	if list, ok := v.([]any); ok {
		for _, x := range list {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	if list, ok := v.([]map[string]any); ok {
		out = append(out, list...)
	}
	return out
}

// gxaPage is the ?paged= value.
func gxaPage(c *gin.Context) int {
	p, _ := strconv.Atoi(c.Query("paged"))
	return max(p, 1)
}

// gxaPagination renders the tablenav pages.
func gxaPagination(base string, page, perPage, total int) string {
	pages := (total + perPage - 1) / perPage
	out := fmt.Sprintf(`<div class="tablenav-pages"><span class="displaying-num">%d %s</span>`, total, map[bool]string{true: "item", false: "items"}[total == 1])
	if pages > 1 {
		out += `<span class="pagination-links">`
		if page > 1 {
			out += fmt.Sprintf(`<a class="prev-page button" href="%s&amp;paged=%d">&lsaquo;</a> `, html.EscapeString(base), page-1)
		}
		out += fmt.Sprintf(`<span class="paging-input">%d of <span class="total-pages">%d</span></span>`, page, pages)
		if page < pages {
			out += fmt.Sprintf(` <a class="next-page button" href="%s&amp;paged=%d">&rsaquo;</a>`, html.EscapeString(base), page+1)
		}
		out += `</span>`
	}
	return out + `</div>`
}

// gxaOrderLink is "#123" linking to the admin order screen.
func gxaOrderLink(id int64) string {
	if id == 0 {
		return "&ndash;"
	}
	return `<a href="/wp-admin/goocommerce?page=orders&amp;action=edit&amp;id=` + itoa64(id) + `">#` + itoa64(id) + `</a>`
}

// gxaYesNo are the options of a yes / no select.
var gxaYesNo = []gooOpt{{"yes", "Yes"}, {"no", "No"}}
