package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/mail"
	"wpgo/internal/wp"
)

// Small helpers shared by the Bookings, Product Bundles, Product Add-Ons and Gift Cards modules
// (prefix gx, to stay clear of the other module branches).

// gxForm is the first trimmed value of a posted field.
func gxForm(form map[string][]string, key string) string {
	if v := form[key]; len(v) > 0 {
		return strings.TrimSpace(v[0])
	}
	return ""
}

// gxNum reads a number from a JSON-decoded value (float64, string, int, bool).
func gxNum(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	case bool:
		if x {
			return 1
		}
	case json.Number:
		f, _ := x.Float64()
		return f
	}
	return 0
}

// gxStr reads a string from a JSON-decoded value.
func gxStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case bool:
		if x {
			return "yes"
		}
		return "no"
	}
	return ""
}

// gxBool reads a yes/no flag.
func gxBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "yes" || x == "1" || x == "true" || x == "on"
	case float64:
		return x != 0
	}
	return false
}

// gxList reads a list of objects from product meta (decoded JSON).
func gxList(v any) []map[string]any {
	var out []map[string]any
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
	case []map[string]any:
		out = x
	}
	return out
}

// gxRoundTrip normalises a value to its JSON-decoded form (maps of any, float64 numbers), the shape
// cart item data and product meta have after a reload.
func gxRoundTrip(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

func gxAtoi(v string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(v))
	return n
}

func gxAtof(v string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
	return f
}

func gxFmt(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// gxLoc is the site's time zone (timezone_string, else the gmt_offset).
func (s *Server) gxLoc() *time.Location {
	if tz := s.settings.String("timezone_string"); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			return l
		}
	}
	if off := gxAtof(s.settings.String("gmt_offset")); off != 0 {
		return time.FixedZone(fmt.Sprintf("UTC%+g", off), int(off*3600))
	}
	return time.UTC
}

// gxDate / gxTime format a time with the site's date and time formats.
func (s *Server) gxDate(t time.Time) string {
	return wp.FormatDate(t.In(s.gxLoc()), defaultStr(s.settings.String("date_format"), "F j, Y"))
}

func (s *Server) gxTime(t time.Time) string {
	return wp.FormatDate(t.In(s.gxLoc()), defaultStr(s.settings.String("time_format"), "g:i a"))
}

// gxCartItemsSwap replaces the cart's items (a new slice, so the engine's running loops are not
// disturbed) and stores them in the session.
func gxCartItemsSwap(cart *gooCart, items []*gooCartItem) {
	cart.Items = items
	cart.persist()
}

// gxAdminPage renders a module admin screen through its page template (web/admin/templates/pages/
// goo-ext-<module>.html): a heading row, the notices, then the Go-built body.
func (s *Server) gxAdminPage(c *gin.Context, tmpl, screen, title string, head, body template.HTML, notices ...notice) {
	s.adminPage(c, "goocommerce?page="+screen, title, "goocommerce_page_goo-"+screen, "admin/"+tmpl,
		map[string]any{"Title": title, "Head": head, "Body": body}, append(gooFlash(c), notices...)...)
}

// gxSendEmail sends a module e-mail registered with gooRegisterEmail: its settings (enabled, subject,
// heading, additional content, recipient) apply; placeholders {site_title} and those given are replaced.
func (s *Server) gxSendEmail(ctx context.Context, id string, to []string, vars map[string]string, body string, att ...mail.Attachment) bool {
	set := s.gooEmailSettings(id)
	if set["enabled"] == "no" {
		return false
	}
	d := gooEmailDefByID(id)
	if d != nil && !d.Customer {
		to = nil
		for _, r := range strings.Split(set["recipient"], ",") {
			if r = strings.TrimSpace(r); r != "" {
				to = append(to, r)
			}
		}
	}
	var rcpt []string
	for _, t := range to {
		if t = strings.TrimSpace(t); t != "" && strings.Contains(t, "@") {
			rcpt = append(rcpt, t)
		}
	}
	if len(rcpt) == 0 {
		return false
	}
	site := s.settings.String("blogname")
	repl := []string{"{site_title}", site, "{blogname}", site}
	for k, v := range vars {
		repl = append(repl, "{"+k+"}", v)
	}
	r := strings.NewReplacer(repl...)
	subject := r.Replace(set["subject"])
	heading := r.Replace(set["heading"])
	extra := r.Replace(set["additional_content"])
	page := `<!DOCTYPE html><html><head><meta charset="utf-8"></head><body style="background:#f7f7f7;margin:0;padding:24px 0;font-family:Helvetica,Arial,sans-serif">` +
		`<table width="600" align="center" cellpadding="0" cellspacing="0" style="background:#fff;border:1px solid #dedede;border-radius:3px">` +
		`<tr><td style="background:#7f54b3;color:#fff;padding:30px 48px;font-size:26px;font-weight:300">` + html.EscapeString(heading) + `</td></tr>` +
		`<tr><td style="padding:40px 48px;color:#636363;font-size:14px;line-height:1.5">` + body
	if extra != "" {
		page += `<p>` + html.EscapeString(extra) + `</p>`
	}
	page += `</td></tr></table><p style="text-align:center;color:#8a8a8a;font-size:12px">` + html.EscapeString(site) + `</p></body></html>`
	s.sendMail(ctx, mail.Message{To: rcpt, Subject: subject, HTML: page, Body: gxStripTags(body), Attachments: att})
	return true
}

// gxStripTags is a plain-text version of a small HTML body.
func gxStripTags(v string) string {
	var b strings.Builder
	in := false
	for _, r := range v {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
			b.WriteByte(' ')
		case !in:
			b.WriteRune(r)
		}
	}
	return html.UnescapeString(strings.Join(strings.Fields(b.String()), " "))
}

// gxSel / gxChk print selected / checked attributes.
func gxSel(a, b string) string {
	if a == b {
		return " selected"
	}
	return ""
}

func gxChk(on bool) string {
	if on {
		return " checked"
	}
	return ""
}

// gxPagination prints the list table's page links.
func gxPagination(base string, page, total, per int) string {
	pages := (total + per - 1) / per
	var b strings.Builder
	fmt.Fprintf(&b, `<div class="tablenav-pages"><span class="displaying-num">%d %s</span>`, total, map[bool]string{true: "item", false: "items"}[total == 1])
	if pages > 1 {
		b.WriteString(`<span class="pagination-links">`)
		for i := 1; i <= pages; i++ {
			if i == page {
				fmt.Fprintf(&b, ` <span class="tablenav-pages-navspan button disabled">%d</span>`, i)
			} else {
				fmt.Fprintf(&b, ` <a class="button" href="%s&amp;paged=%d">%d</a>`, html.EscapeString(base), i, i)
			}
		}
		b.WriteString(`</span>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}
