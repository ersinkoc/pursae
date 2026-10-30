package server

import (
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Goocommerce › Gift Cards: the issued cards with their balance, status and expiry, a manual card
// form, and each card's screen (status, balance adjustment, activity log).

func init() {
	gooExtAdminPages["gift-cards"] = func(s *Server, c *gin.Context) { s.gcAdmin(c) }
}

func gcAdminHead() template.HTML {
	return template.HTML(`<h1 class="wp-heading-inline">Gift Cards</h1> <a href="/wp-admin/goocommerce?page=gift-cards&amp;action=new" class="page-title-action">Add Gift Card</a><hr class="wp-header-end">`)
}

func (s *Server) gcAdmin(c *gin.Context) {
	if !s.gooExtOn(gcKey) {
		c.String(http.StatusNotFound, "The Gift Cards extension is not active.")
		return
	}
	switch c.Query("action") {
	case "new":
		s.gcAdminNew(c)
	case "edit":
		s.gcAdminEdit(c)
	default:
		s.gcAdminList(c)
	}
}

func (s *Server) gcStatusLabel(g *gcCard) string {
	if g.Status == "disabled" {
		return "Disabled"
	}
	if g.Expires != nil && time.Now().After(g.Expires.AddDate(0, 0, 1)) {
		return "Expired"
	}
	if g.Status == "redeemed" {
		return "Used"
	}
	if !g.Delivered && g.DeliverDate != nil {
		return "Scheduled"
	}
	return "Active"
}

func (s *Server) gcAdminList(c *gin.Context) {
	ctx := c.Request.Context()
	where := []string{"true"}
	var args []any
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	status := c.Query("status")
	switch status {
	case "active", "disabled", "redeemed":
		where = append(where, "status = "+arg(status))
	case "expired":
		where = append(where, "expires < current_date")
	}
	if q := strings.TrimSpace(c.Query("s")); q != "" {
		where = append(where, "(code ILIKE "+arg("%"+q+"%")+" OR recipient ILIKE "+arg("%"+q+"%")+" OR sender ILIKE "+arg("%"+q+"%")+")")
	}
	page := max(gxAtoi(c.Query("paged")), 1)
	per := 20
	var total int
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_gift_cards WHERE `+strings.Join(where, " AND "), args...).Scan(&total)
	cards := s.gcQuery(ctx, strings.Join(where, " AND ")+fmt.Sprintf(" ORDER BY id DESC LIMIT %d OFFSET %d", per, (page-1)*per), args...)
	var b strings.Builder
	b.WriteString(`<ul class="subsubsub">`)
	for i, st := range []gooOpt{{"", "All"}, {"active", "Active"}, {"redeemed", "Used"}, {"expired", "Expired"}, {"disabled", "Disabled"}} {
		cls := ""
		if st.Value == status {
			cls = ` class="current"`
		}
		sep := ""
		if i > 0 {
			sep = " | "
		}
		fmt.Fprintf(&b, `<li>%s<a href="/wp-admin/goocommerce?page=gift-cards&amp;status=%s"%s>%s</a></li>`, sep, st.Value, cls, st.Label)
	}
	b.WriteString(`</ul><form method="get" action="/wp-admin/goocommerce"><input type="hidden" name="page" value="gift-cards"><p class="search-box"><input type="search" name="s" value="` +
		html.EscapeString(c.Query("s")) + `"> <input type="submit" class="button" value="Search Gift Cards"></p></form>`)
	b.WriteString(`<div class="tablenav top">` + gxPagination("/wp-admin/goocommerce?page=gift-cards&status="+url.QueryEscape(status), page, total, per) + `<br class="clear"></div>`)
	b.WriteString(`<table class="wp-list-table widefat fixed striped"><thead><tr><th scope="col" class="column-primary">Code</th><th scope="col">Recipient</th><th scope="col">Sender</th><th scope="col">Initial</th>` +
		`<th scope="col">Balance</th><th scope="col">Status</th><th scope="col">Expires</th><th scope="col">Order</th><th scope="col">Created</th></tr></thead><tbody>`)
	if len(cards) == 0 {
		b.WriteString(`<tr class="no-items"><td colspan="9">No gift cards found.</td></tr>`)
	}
	for _, g := range cards {
		exp, order := "&ndash;", "&ndash;"
		if g.Expires != nil {
			exp = html.EscapeString(s.gxDate(*g.Expires))
		}
		if g.OrderID != 0 {
			order = fmt.Sprintf(`<a href="/wp-admin/goocommerce?page=orders&amp;action=edit&amp;id=%d">#%d</a>`, g.OrderID, g.OrderID)
		}
		fmt.Fprintf(&b, `<tr><td class="column-primary"><a href="/wp-admin/goocommerce?page=gift-cards&amp;action=edit&amp;id=%d"><code>%s</code></a></td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td><mark class="order-status status-%s"><span>%s</span></mark></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			g.ID, html.EscapeString(g.Code), html.EscapeString(g.Recipient), html.EscapeString(g.Sender), s.gooPriceIn(g.Initial, g.Currency), s.gooPriceIn(g.Balance, g.Currency),
			g.Status, s.gcStatusLabel(g), exp, order, html.EscapeString(s.gxDate(g.Created)))
	}
	b.WriteString(`</tbody></table>`)
	s.gxAdminPage(c, "goo-ext-gift-cards", "gift-cards", "Gift Cards", gcAdminHead(), template.HTML(b.String()))
}

func (s *Server) gcAdminNew(c *gin.Context) {
	ctx := c.Request.Context()
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "goo-gift-card-new") {
			s.denyNonce(c)
			return
		}
		fail := func(msg string) {
			c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=gift-cards&action=new&goo-error="+url.QueryEscape(msg))
		}
		amount := gooRound(gxAtof(c.PostForm("amount")), s.gooDecimals())
		if amount <= 0 {
			fail("Please enter a valid gift card amount.")
			return
		}
		to := strings.TrimSpace(c.PostForm("recipient"))
		if a, err := mail.ParseAddress(to); err != nil || a.Address != to {
			fail("Please enter a valid recipient e-mail.")
			return
		}
		g := &gcCard{Initial: amount, Recipient: to, RecipientName: strings.TrimSpace(c.PostForm("recipient_name")),
			Sender: defaultStr(strings.TrimSpace(c.PostForm("sender")), s.settings.String("blogname")), Message: strings.TrimSpace(c.PostForm("message"))}
		if e, err := time.ParseInLocation("2006-01-02", c.PostForm("expires"), s.gxLoc()); err == nil {
			g.Expires = &e
		}
		uid := int64(0)
		if u := currentUser(c); u != nil {
			uid = u.ID
		}
		if err := s.gcCreate(ctx, g, uid, "Created manually"); err != nil {
			fail(err.Error())
			return
		}
		if c.PostForm("send") != "" {
			s.gcDeliver(ctx, g)
		}
		c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=gift-cards&action=edit&id="+itoa64(g.ID)+"&goo-msg="+url.QueryEscape("Gift card "+g.Code+" created."))
		return
	}
	var b strings.Builder
	b.WriteString(`<form method="post" action="/wp-admin/goocommerce?page=gift-cards&amp;action=new"><input type="hidden" name="_wpnonce" value="` + s.nonce(c, "goo-gift-card-new") + `"><table class="form-table"><tbody>`)
	row := func(label, input string) {
		b.WriteString(`<tr><th scope="row">` + label + `</th><td>` + input + `</td></tr>`)
	}
	row(`<label for="amount">Amount</label>`, `<input type="text" name="amount" id="amount" class="wc_input_price" required> `+html.EscapeString(s.gooCurrency()))
	row(`<label for="recipient">To</label>`, `<input type="email" name="recipient" id="recipient" class="regular-text" required>`)
	row(`<label for="recipient_name">Recipient name</label>`, `<input type="text" name="recipient_name" id="recipient_name" class="regular-text">`)
	row(`<label for="sender">From</label>`, `<input type="text" name="sender" id="sender" class="regular-text" value="`+html.EscapeString(s.settings.String("blogname"))+`">`)
	row(`<label for="message">Message</label>`, `<textarea name="message" id="message" rows="3" class="large-text"></textarea>`)
	row(`<label for="expires">Expiration date</label>`, `<input type="date" name="expires" id="expires"> <span class="description">Leave empty for a gift card that never expires.</span>`)
	row(`Delivery`, `<label><input type="checkbox" name="send" value="1" checked> E-mail the gift card to the recipient now</label>`)
	b.WriteString(`</tbody></table><p class="submit"><button type="submit" class="button button-primary">Create gift card</button></p></form>`)
	s.gxAdminPage(c, "goo-ext-gift-cards", "gift-cards", "Add Gift Card", gcAdminHead(), template.HTML(b.String()))
}

func (s *Server) gcAdminEdit(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := strconv.ParseInt(c.Query("id"), 10, 64)
	g, err := s.gcGet(ctx, id)
	if err != nil {
		c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=gift-cards&goo-error="+url.QueryEscape("Gift card not found."))
		return
	}
	back := "/wp-admin/goocommerce?page=gift-cards&action=edit&id=" + itoa64(g.ID)
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "goo-gift-card-"+itoa64(g.ID)) {
			s.denyNonce(c)
			return
		}
		uid := int64(0)
		if u := currentUser(c); u != nil {
			uid = u.ID
		}
		msg := "Gift card updated."
		switch c.PostForm("gc_action") {
		case "disable":
			g.Status = "disabled"
			_, _ = s.store.Pool.Exec(ctx, `UPDATE goo_gift_cards SET status = 'disabled' WHERE id = $1`, g.ID)
			_ = s.gcLog(ctx, g.ID, "manual", 0, 0, uid, "Disabled")
			msg = "Gift card disabled."
		case "enable":
			g.Status = "active"
			_ = s.gcAdjust(ctx, g, 0, "manual", 0, uid, "Enabled")
			msg = "Gift card enabled."
		case "adjust":
			delta := gxAtof(c.PostForm("adjust_amount"))
			if delta != 0 {
				_ = s.gcAdjust(ctx, g, delta, "manual", 0, uid, strings.TrimSpace(c.PostForm("adjust_note")))
			}
			msg = "Balance updated."
		case "resend":
			s.gcDeliver(ctx, g)
			msg = "Gift card e-mail sent."
		case "save":
			if e, err := time.ParseInLocation("2006-01-02", c.PostForm("expires"), s.gxLoc()); err == nil {
				g.Expires = &e
			} else {
				g.Expires = nil
			}
			_, _ = s.store.Pool.Exec(ctx, `UPDATE goo_gift_cards SET expires = $2, recipient = $3, recipient_name = $4, sender = $5, message = $6 WHERE id = $1`,
				g.ID, g.Expires, strings.TrimSpace(c.PostForm("recipient")), strings.TrimSpace(c.PostForm("recipient_name")), strings.TrimSpace(c.PostForm("sender")), strings.TrimSpace(c.PostForm("message")))
		}
		c.Redirect(http.StatusFound, back+"&goo-msg="+url.QueryEscape(msg))
		return
	}
	nonce := s.nonce(c, "goo-gift-card-"+itoa64(g.ID))
	var b strings.Builder
	form := func(action, inner string) string {
		return `<form method="post" action="` + html.EscapeString(back) + `" style="display:inline"><input type="hidden" name="_wpnonce" value="` + nonce + `"><input type="hidden" name="gc_action" value="` + action + `">` + inner + `</form>`
	}
	fmt.Fprintf(&b, `<div id="poststuff"><div class="postbox"><div class="postbox-header"><h2>Gift card <code>%s</code> &mdash; %s</h2></div><div class="inside">`, html.EscapeString(g.Code), s.gcStatusLabel(g))
	fmt.Fprintf(&b, `<p><strong>Balance:</strong> %s of %s`, s.gooPriceIn(g.Balance, g.Currency), s.gooPriceIn(g.Initial, g.Currency))
	if g.OrderID != 0 {
		fmt.Fprintf(&b, ` &middot; <strong>Order:</strong> <a href="/wp-admin/goocommerce?page=orders&amp;action=edit&amp;id=%d">#%d</a>`, g.OrderID, g.OrderID)
	}
	if g.RedeemedBy != 0 {
		fmt.Fprintf(&b, ` &middot; <strong>In the account of user</strong> #%d`, g.RedeemedBy)
	}
	b.WriteString(`</p><p>`)
	if g.Status == "disabled" {
		b.WriteString(form("enable", `<button type="submit" class="button">Enable</button>`))
	} else {
		b.WriteString(form("disable", `<button type="submit" class="button">Disable</button>`))
	}
	b.WriteString(` ` + form("resend", `<button type="submit" class="button">Resend e-mail</button>`) + `</p>`)
	exp := ""
	if g.Expires != nil {
		exp = g.Expires.In(s.gxLoc()).Format("2006-01-02")
	}
	b.WriteString(form("save", `<table class="form-table"><tbody>`+
		`<tr><th>To</th><td><input type="email" name="recipient" value="`+html.EscapeString(g.Recipient)+`" class="regular-text"></td></tr>`+
		`<tr><th>Recipient name</th><td><input type="text" name="recipient_name" value="`+html.EscapeString(g.RecipientName)+`" class="regular-text"></td></tr>`+
		`<tr><th>From</th><td><input type="text" name="sender" value="`+html.EscapeString(g.Sender)+`" class="regular-text"></td></tr>`+
		`<tr><th>Message</th><td><textarea name="message" rows="3" class="large-text">`+html.EscapeString(g.Message)+`</textarea></td></tr>`+
		`<tr><th>Expiration date</th><td><input type="date" name="expires" value="`+exp+`"></td></tr></tbody></table><p><button type="submit" class="button button-primary">Save gift card</button></p>`))
	b.WriteString(`<h3>Adjust balance</h3>` + form("adjust", `<input type="text" name="adjust_amount" class="wc_input_price" placeholder="+10 or -5"> <input type="text" name="adjust_note" placeholder="Note" class="regular-text"> <button type="submit" class="button">Adjust</button>`))
	b.WriteString(`</div></div><div class="postbox"><div class="postbox-header"><h2>Activity</h2></div><div class="inside"><table class="widefat striped"><thead><tr><th>Date</th><th>Event</th><th>Amount</th><th>Order</th><th>User</th><th>Note</th></tr></thead><tbody>`)
	acts := s.gcActivities(ctx, g.ID)
	if len(acts) == 0 {
		b.WriteString(`<tr><td colspan="6">No activity.</td></tr>`)
	}
	for _, a := range acts {
		order, user := "&ndash;", "&ndash;"
		if a.OrderID != 0 {
			order = fmt.Sprintf(`<a href="/wp-admin/goocommerce?page=orders&amp;action=edit&amp;id=%d">#%d</a>`, a.OrderID, a.OrderID)
		}
		if a.UserID != 0 {
			user = "#" + itoa64(a.UserID)
		}
		label := map[string]string{"issued": "Issued", "used": "Used", "refunded": "Refunded", "manual": "Manual"}[a.Type]
		fmt.Fprintf(&b, `<tr><td>%s %s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`, html.EscapeString(s.gxDate(a.Date)), html.EscapeString(s.gxTime(a.Date)),
			defaultStr(label, a.Type), s.gooPriceIn(a.Amount, g.Currency), order, user, html.EscapeString(a.Note))
	}
	b.WriteString(`</tbody></table></div></div></div>`)
	s.gxAdminPage(c, "goo-ext-gift-cards", "gift-cards", "Edit Gift Card", gcAdminHead(), template.HTML(b.String()))
}
