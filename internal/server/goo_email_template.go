package server

import (
	"context"
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
)

// The e-mail template: WooCommerce's emails/email-header.php, email-footer.php, email-order-details.php,
// email-order-items.php and email-addresses.php as inline-styled HTML (mail clients ignore <style>),
// colored from the Emails settings (base, background, body background, text), plus a plain-text version.

const gooEmailFont = `"Helvetica Neue",Helvetica,Roboto,Arial,sans-serif`

// gooEmailColors are the template colors and the shades WooCommerce derives from them.
type gooEmailColors struct {
	Base, BaseText, BaseLighter20, BaseLighter40, BG, BGDarker10, Body, Text, TextLighter20 string
}

func (s *Server) gooEmailPalette() gooEmailColors {
	pick := func(key, def string) string {
		v := strings.TrimSpace(s.gooOptString(key))
		if _, _, _, ok := gooParseHex(v); !ok {
			return def
		}
		return v
	}
	base := pick("email_base_color", "#7f54b3")
	bg := pick("email_background_color", "#f7f7f7")
	body := pick("email_body_background_color", "#ffffff")
	text := pick("email_text_color", "#3c3c3c")
	return gooEmailColors{Base: base, BaseText: gooLightOrDark(base, "#202020", "#ffffff"), BaseLighter20: gooHexShade(base, 20, true),
		BaseLighter40: gooHexShade(base, 40, true), BG: bg, BGDarker10: gooHexShade(bg, 10, false), Body: body, Text: text,
		TextLighter20: gooHexShade(text, 20, true)}
}

func gooParseHex(v string) (r, g, b int, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "#")
	if len(v) == 3 {
		v = string([]byte{v[0], v[0], v[1], v[1], v[2], v[2]})
	}
	if len(v) != 6 {
		return 0, 0, 0, false
	}
	n, err := strconv.ParseUint(v, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(n >> 16 & 0xff), int(n >> 8 & 0xff), int(n & 0xff), true
}

// gooHexShade is wc_hex_lighter() / wc_hex_darker().
func gooHexShade(v string, factor float64, lighter bool) string {
	r, g, b, ok := gooParseHex(v)
	if !ok {
		return v
	}
	adj := func(c int) int {
		if lighter {
			return c + int(math.Round(float64(255-c)/100*factor))
		}
		return c - int(math.Round(float64(c)/100*factor))
	}
	return fmt.Sprintf("#%02x%02x%02x", adj(r), adj(g), adj(b))
}

// gooLightOrDark is wc_light_or_dark(): the dark color on light backgrounds.
func gooLightOrDark(v, dark, light string) string {
	r, g, b, ok := gooParseHex(v)
	if !ok {
		return light
	}
	if (r*299+g*587+b*114)/1000 > 155 {
		return dark
	}
	return light
}

// gooAutoP is wpautop() for the additional content: paragraphs and line breaks.
func gooAutoP(v string) string {
	v = strings.TrimSpace(strings.ReplaceAll(v, "\r\n", "\n"))
	if v == "" {
		return ""
	}
	var out strings.Builder
	for _, p := range strings.Split(v, "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out.WriteString(`<p style="margin:0 0 16px;">` + strings.ReplaceAll(p, "\n", "<br>\n") + "</p>\n")
		}
	}
	return out.String()
}

// gooEmailFooter is the footer text with its placeholders.
func (s *Server) gooEmailFooter(ctx context.Context) string {
	f := s.gooOptString("email_footer_text")
	if f == "" {
		f = "{site_title} &mdash; Built with WordPress Go"
	}
	home := s.links(ctx).Home
	return strings.NewReplacer("{site_title}", gooEsc(s.settings.String("blogname")), "{site_url}", home, "{site_address}", home,
		"{store_email}", gooEsc(s.gooEmailFromAddress())).Replace(f)
}

// gooEmailWrap puts content into the header / footer layout.
func (s *Server) gooEmailWrap(ctx context.Context, heading, content string) string {
	c := s.gooEmailPalette()
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta http-equiv="Content-Type" content="text/html; charset=UTF-8"><meta content="width=device-width, initial-scale=1.0" name="viewport"><title>` +
		gooEsc(s.settings.String("blogname")) + `</title></head>`)
	b.WriteString(`<body leftmargin="0" marginwidth="0" topmargin="0" marginheight="0" offset="0" style="background-color:` + c.BG + `;padding:0;text-align:center;">`)
	b.WriteString(`<table width="100%" id="outer_wrapper" style="background-color:` + c.BG + `;"><tr><td></td><td width="600">`)
	b.WriteString(`<div id="wrapper" dir="ltr" style="margin:0 auto;padding:70px 0;width:100%;max-width:600px;-webkit-text-size-adjust:none;">`)
	b.WriteString(`<table border="0" cellpadding="0" cellspacing="0" height="100%" width="100%"><tr><td align="center" valign="top">`)
	if img := strings.TrimSpace(s.gooOptString("email_header_image")); img != "" {
		b.WriteString(`<div id="template_header_image"><p style="margin-top:0;"><img src="` + gooEsc(img) + `" alt="` + gooEsc(s.settings.String("blogname")) +
			`" style="border:none;display:inline-block;font-size:14px;font-weight:bold;height:auto;outline:none;text-decoration:none;text-transform:capitalize;vertical-align:middle;max-width:100%;margin-left:0;margin-right:0;"></p></div>`)
	}
	b.WriteString(`<table border="0" cellpadding="0" cellspacing="0" width="100%" id="template_container" style="background-color:` + c.Body +
		`;border:1px solid ` + c.BGDarker10 + `;box-shadow:0 1px 4px rgba(0,0,0,0.1);border-radius:3px;">`)
	b.WriteString(`<tr><td align="center" valign="top"><table border="0" cellpadding="0" cellspacing="0" width="100%" id="template_header" style="background-color:` + c.Base +
		`;color:` + c.BaseText + `;border-bottom:0;font-weight:bold;line-height:100%;vertical-align:middle;font-family:` + gooEmailFont + `;border-radius:3px 3px 0 0;">`)
	b.WriteString(`<tr><td id="header_wrapper" style="padding:36px 48px;display:block;"><h1 style="font-family:` + gooEmailFont +
		`;font-size:30px;font-weight:300;line-height:150%;margin:0;text-align:left;text-shadow:0 1px 0 ` + c.BaseLighter20 + `;color:` + c.BaseText +
		`;background-color:inherit;">` + gooEsc(heading) + `</h1></td></tr></table></td></tr>`)
	b.WriteString(`<tr><td align="center" valign="top"><table border="0" cellpadding="0" cellspacing="0" width="100%" id="template_body"><tr><td valign="top" id="body_content" style="background-color:` + c.Body + `;">`)
	b.WriteString(`<table border="0" cellpadding="20" cellspacing="0" width="100%"><tr><td valign="top" style="padding:48px 48px 32px;">`)
	b.WriteString(`<div id="body_content_inner" style="color:` + c.TextLighter20 + `;font-family:` + gooEmailFont + `;font-size:14px;line-height:150%;text-align:left;">`)
	b.WriteString(content)
	b.WriteString(`</div></td></tr></table></td></tr></table></td></tr></table></td></tr>`)
	b.WriteString(`<tr><td align="center" valign="top"><table border="0" cellpadding="10" cellspacing="0" width="100%" id="template_footer"><tr><td valign="top" style="padding:0;border-radius:6px;">`)
	b.WriteString(`<table border="0" cellpadding="10" cellspacing="0" width="100%"><tr><td colspan="2" valign="middle" id="credit" style="border-radius:6px;border:0;color:` + c.TextLighter20 +
		`;font-family:` + gooEmailFont + `;font-size:12px;line-height:150%;text-align:center;padding:24px 0;"><p style="margin:0 0 16px;">` + s.gooEmailFooter(ctx) + `</p></td></tr></table>`)
	b.WriteString(`</td></tr></table></td></tr></table></div></td><td></td></tr></table></body></html>`)
	return b.String()
}

// gooEmailP is a paragraph of the body.
func gooEmailP(v string) string { return `<p style="margin:0 0 16px;">` + v + "</p>\n" }

// gooEmailContent is the body of each e-mail (HTML and plain text) before the additional content.
func (s *Server) gooEmailContent(ctx context.Context, d *gooEmailDef, o *gooOrder, args map[string]any) (string, string) {
	site := s.settings.String("blogname")
	first := ""
	if o != nil {
		first = o.Billing["first_name"]
	}
	hi := func() (string, string) {
		if first == "" {
			return gooEmailP("Hi there,"), "Hi there,\n\n"
		}
		return gooEmailP("Hi " + gooEsc(first) + ","), "Hi " + first + ",\n\n"
	}
	var h, p string
	withOrder := func(admin bool) {
		oh, op := s.gooEmailOrderDetails(ctx, o, admin, args)
		ah, ap := s.gooEmailAddresses(o)
		h += oh + ah
		p += "\n" + op + "\n" + ap
	}
	switch d.ID {
	case "new_order":
		h = gooEmailP("You’ve received the following order from " + gooEsc(o.BillingName()) + ":")
		p = "You’ve received the following order from " + o.BillingName() + ":\n"
		withOrder(true)
	case "cancelled_order":
		msg := "Notification to let you know &mdash; order #" + o.Number() + " belonging to " + gooEsc(o.BillingName()) + " has been cancelled:"
		h, p = gooEmailP(msg), gooPlainMsg(msg)+"\n"
		withOrder(true)
	case "failed_order":
		msg := "Payment for order #" + o.Number() + " from " + gooEsc(o.BillingName()) + " has failed. The order was as follows:"
		h, p = gooEmailP(msg), gooPlainMsg(msg)+"\n"
		withOrder(true)
	case "customer_on_hold_order":
		h, p = hi()
		msg := "Thanks for your order. It’s on-hold until we confirm that payment has been received. In the meantime, here’s a reminder of what you ordered:"
		h += gooEmailP(msg)
		p += msg + "\n"
		withOrder(false)
	case "customer_processing_order":
		h, p = hi()
		msg := "Just to let you know &mdash; we’ve received your order #" + o.Number() + ", and it is now being processed:"
		h += gooEmailP(msg)
		p += gooPlainMsg(msg) + "\n"
		withOrder(false)
	case "customer_completed_order":
		h, p = hi()
		h += gooEmailP("We have finished processing your order.")
		p += "We have finished processing your order.\n"
		withOrder(false)
	case "customer_refunded_order":
		h, p = hi()
		msg := "Your order on " + gooEsc(site) + " has been refunded. There are more details below for your reference:"
		if gooBool(args["partial"]) {
			msg = "Your order on " + gooEsc(site) + " has been partially refunded. There are more details below for your reference:"
		}
		h += gooEmailP(msg)
		p += gooPlainMsg(msg) + "\n"
		withOrder(false)
	case "customer_invoice":
		h, p = hi()
		if o.IsPaid() || o.Total <= 0 {
			msg := "Here are the details of your order placed on " + gooEsc(s.gooEmailDate(o.DateCreated)) + ":"
			h += gooEmailP(msg)
			p += gooPlainMsg(msg) + "\n"
		} else {
			pay := s.gooOrderPayURL(ctx, o)
			h += gooEmailP("An order has been created for you on " + gooEsc(site) + ". Your invoice is below, with a link to make payment when you’re ready: " +
				`<a href="` + gooEsc(pay) + `" style="color:` + s.gooEmailPalette().Base + `;font-weight:normal;text-decoration:underline;">Pay for this order</a>`)
			p += "An order has been created for you on " + site + ". Your invoice is below, with a link to make payment when you’re ready: " + pay + "\n"
		}
		withOrder(false)
	case "customer_note":
		note, _ := args["note"].(string)
		h, p = hi()
		h += gooEmailP("The following note has been added to your order:") +
			`<blockquote style="margin:0 0 16px;padding-left:16px;border-left:3px solid ` + s.gooEmailPalette().Base + `;">` + strings.ReplaceAll(gooEsc(note), "\n", "<br>") + "</blockquote>\n" +
			gooEmailP("As a reminder, here are your order details:")
		p += "The following note has been added to your order:\n\n----------\n\n" + note + "\n\n----------\n\nAs a reminder, here are your order details:\n"
		withOrder(false)
	case "customer_new_account":
		login, _ := args["login"].(string)
		uid := gooInt(args["user_id"])
		if login == "" && uid != 0 {
			if u, err := s.store.UserByID(ctx, uid); err == nil {
				login = u.Login
			}
		}
		acct := s.gooAccountURL(ctx, "")
		h = gooEmailP("Hi "+gooEsc(login)+",") +
			gooEmailP("Thanks for creating an account on "+gooEsc(site)+". Your username is <strong>"+gooEsc(login)+"</strong>. You can access your account area to view orders, change your password, and more at: "+
				`<a href="`+gooEsc(acct)+`" style="color:`+s.gooEmailPalette().Base+`;font-weight:normal;text-decoration:underline;">`+gooEsc(acct)+`</a>`)
		p = "Hi " + login + ",\n\nThanks for creating an account on " + site + ". Your username is " + login + ". You can access your account area to view orders, change your password, and more at: " + acct + "\n"
		if gooBool(args["password_generated"]) {
			link, _ := args["reset_url"].(string)
			if link == "" && uid != 0 {
				link = s.gooEmailResetURL(ctx, uid)
			}
			if link == "" {
				link = s.gooAccountURL(ctx, "lost-password")
			}
			h += gooEmailP(`<a href="` + gooEsc(link) + `" style="color:` + s.gooEmailPalette().Base + `;font-weight:normal;text-decoration:underline;">Click here to set your new password.</a>`)
			p += "\nTo set your password, visit the following address: " + link + "\n"
		}
	case "customer_reset_password":
		login, _ := args["login"].(string)
		link, _ := args["reset_url"].(string)
		if uid := gooInt(args["user_id"]); uid != 0 {
			if u, err := s.store.UserByID(ctx, uid); err == nil && login == "" {
				login = u.Login
			}
			if link == "" {
				link = s.gooEmailResetURL(ctx, uid)
			}
		}
		h = gooEmailP("Hi "+gooEsc(login)+",") + gooEmailP("Someone has requested a new password for the following account on "+gooEsc(site)+":") +
			gooEmailP("Username: <strong>"+gooEsc(login)+"</strong>") + gooEmailP("If you didn’t make this request, just ignore this email. If you’d like to proceed:") +
			gooEmailP(`<a class="link" href="`+gooEsc(link)+`" style="color:`+s.gooEmailPalette().Base+`;font-weight:normal;text-decoration:underline;">Click here to reset your password</a>`)
		p = "Hi " + login + ",\n\nSomeone has requested a new password for the following account on " + site + ":\n\nUsername: " + login +
			"\n\nIf you didn’t make this request, just ignore this email. If you’d like to proceed:\n\n" + link + "\n"
	case "low_stock", "no_stock", "backorder":
		prod, _ := args["product"].(*gooProduct)
		name := "A product"
		qty := 0
		if prod != nil {
			name = s.gooProductTitle(ctx, prod)
			if prod.Type == gooTypeVariation {
				if sum := s.gooVariationSummary(ctx, prod); sum != "" {
					name += " - " + sum
				}
			}
			if prod.SKU != "" {
				name += " (" + prod.SKU + ")"
			}
			if prod.StockQuantity != nil {
				qty = *prod.StockQuantity
			}
		}
		var msg string
		switch d.ID {
		case "low_stock":
			msg = gooEsc(name) + " is low in stock. There are " + strconv.Itoa(qty) + " left."
		case "no_stock":
			msg = gooEsc(name) + " is out of stock."
		default:
			n := int(gooInt(args["quantity"]))
			order := ""
			if o != nil {
				order = o.Number()
			}
			msg = strconv.Itoa(n) + " units of " + gooEsc(name) + " have been backordered in order #" + order + "."
		}
		h, p = gooEmailP(msg), html.UnescapeString(msg)+"\n"
	default:
		if o != nil {
			withOrder(!d.Customer)
		}
	}
	return h, p
}

// gooEmailItemMeta lists an order item's displayed meta (variation attributes, add-on data).
func (s *Server) gooEmailItemMeta(ctx context.Context, it *gooOrderItem) []gooKV {
	var out []gooKV
	if list, ok := it.Meta["_display"].([]any); ok {
		for _, x := range list {
			m := gooObj(x)
			out = append(out, gooKV{gooStr(m["key"]), gooStr(m["value"])})
		}
		return out
	}
	for k, v := range it.Meta {
		if strings.HasPrefix(k, "_") {
			continue
		}
		if strings.HasPrefix(k, "pa_") {
			a := gooProductAttr{Taxonomy: k}
			out = append(out, gooKV{s.gooAttrLabel(ctx, a), s.gooAttrValueLabel(ctx, a, gooStr(v))})
		} else if it.VariationID != 0 {
			out = append(out, gooKV{k, gooStr(v)})
		}
	}
	return out
}

// gooEmailTotals is WC_Order::get_order_item_totals(): label, HTML value, plain value.
func (s *Server) gooEmailTotals(ctx context.Context, o *gooOrder, args map[string]any) [][3]string {
	cur := o.Currency
	price := func(v float64) (string, string) { return string(s.gooPriceIn(v, cur)), s.gooPricePlain(v, cur) }
	incl := s.gooOptString("tax_display_cart") == "incl"
	var rows [][3]string
	add := func(label string, v float64, prefix string) {
		h, p := price(v)
		rows = append(rows, [3]string{label, prefix + h, prefix + p})
	}
	sub := o.Subtotal()
	if incl {
		for _, it := range o.ItemsOf("line_item") {
			sub += it.SubtotalTax
		}
	}
	add("Subtotal:", sub, "")
	if o.DiscountTotal > 0 {
		d := o.DiscountTotal
		if incl {
			d += o.DiscountTax
		}
		add("Discount:", d, "-")
	}
	if ships := o.ItemsOf("shipping"); len(ships) > 0 {
		var names []string
		for _, it := range ships {
			names = append(names, it.Name)
		}
		v := o.ShippingTotal
		if incl {
			v += o.ShippingTax
		}
		h, p := price(v)
		via := strings.Join(names, ", ")
		if v == 0 {
			h, p = gooEsc(via), via
		} else {
			h += `&nbsp;<small class="shipped_via">via ` + gooEsc(via) + `</small>`
			p += " via " + via
		}
		rows = append(rows, [3]string{"Shipping:", h, p})
	}
	for _, it := range o.ItemsOf("fee") {
		v := it.Total
		if incl {
			v += it.TotalTax
		}
		add(it.Name+":", v, "")
	}
	if s.gooTaxEnabled() && !incl && o.TotalTax != 0 {
		if s.gooOptString("tax_total_display") == "itemized" {
			for _, it := range o.ItemsOf("tax") {
				a, _ := gooFloat(it.Meta["tax_amount"])
				b, _ := gooFloat(it.Meta["shipping_tax_amount"])
				add(defaultStr(gooStr(it.Meta["label"]), "Tax")+":", a+b, "")
			}
		} else {
			add("Tax:", o.TotalTax, "")
		}
	}
	if o.Total > 0 && o.PaymentMethodTitle != "" {
		rows = append(rows, [3]string{"Payment method:", gooEsc(o.PaymentMethodTitle), o.PaymentMethodTitle})
	}
	refunded := 0.0
	if !gooBool(args["preview"]) && o.ID != 0 {
		if list, err := s.gooRefunds(ctx, o.ID); err == nil {
			for _, r := range list {
				add("Refund:", -r.Total, "-")
				refunded += -r.Total
			}
		}
	}
	h, p := price(o.Total)
	if refunded > 0 {
		nh, np := price(o.Total - refunded)
		h, p = "<del>"+gooPlainMsg(h)+"</del> <ins>"+nh+"</ins>", p+" → "+np
	}
	rows = append(rows, [3]string{"Total:", h, p})
	return rows
}

// gooEmailOrderDetails is email-order-details.php (+ email-order-items.php).
func (s *Server) gooEmailOrderDetails(ctx context.Context, o *gooOrder, admin bool, args map[string]any) (string, string) {
	c := s.gooEmailPalette()
	td := `color:` + c.TextLighter20 + `;border:1px solid #e5e5e5;vertical-align:middle;padding:12px;text-align:left;`
	date := s.gooEmailDate(o.DateCreated)
	title := "[Order #" + o.Number() + "]"
	if admin && o.ID != 0 {
		title = `<a class="link" href="` + gooEsc(s.links(ctx).HomeURL("/wp-admin/goocommerce?page=orders&action=edit&id="+o.Number())) + `" style="color:` + c.Base + `;font-weight:normal;text-decoration:underline;">` + title + `</a>`
	}
	var b strings.Builder
	b.WriteString(`<h2 style="color:` + c.Base + `;display:block;font-family:` + gooEmailFont + `;font-size:18px;font-weight:bold;line-height:130%;margin:0 0 18px;text-align:left;">` +
		title + ` (<time datetime="` + o.DateCreated.Format("2006-01-02T15:04:05Z07:00") + `">` + gooEsc(date) + `</time>)</h2>`)
	b.WriteString(`<div style="margin-bottom:40px;"><table class="td" cellspacing="0" cellpadding="6" border="1" style="color:` + c.TextLighter20 +
		`;border:1px solid #e5e5e5;vertical-align:middle;width:100%;font-family:` + gooEmailFont + `;">`)
	b.WriteString(`<thead><tr><th class="td" scope="col" style="` + td + `">Product</th><th class="td" scope="col" style="` + td + `">Quantity</th><th class="td" scope="col" style="` + td + `">Price</th></tr></thead><tbody>`)
	var p strings.Builder
	p.WriteString("[Order #" + o.Number() + "] (" + date + ")\n\n")
	incl := s.gooOptString("tax_display_cart") == "incl"
	for _, it := range o.ItemsOf("line_item") {
		name := gooEsc(it.Name)
		sku := ""
		pid := it.ProductID
		if it.VariationID != 0 {
			pid = it.VariationID
		}
		if pid != 0 && !gooBool(args["preview"]) {
			if prod, err := s.gooGetProduct(ctx, pid); err == nil && prod.SKU != "" {
				sku = prod.SKU
			}
		}
		if sku != "" {
			name += " (#" + gooEsc(sku) + ")"
		}
		meta := s.gooEmailItemMeta(ctx, it)
		var mh strings.Builder
		if len(meta) > 0 {
			mh.WriteString(`<ul class="wc-item-meta" style="font-size:small;margin:1em 0 0;padding:0;list-style:none;">`)
			for _, kv := range meta {
				mh.WriteString(`<li style="margin:0.5em 0 0;padding:0;"><strong class="wc-item-meta-label" style="float:left;margin-right:.25em;clear:both;">` + gooEsc(kv.Key) + `:</strong> <p style="margin:0;">` + gooEsc(kv.Value) + `</p></li>`)
			}
			mh.WriteString(`</ul>`)
		}
		v := it.Subtotal
		if incl {
			v += it.SubtotalTax
		}
		b.WriteString(`<tr class="order_item"><td class="td" style="` + td + `word-wrap:break-word;">` + name + mh.String() + `</td><td class="td" style="` + td + `">` +
			strconv.Itoa(it.Qty()) + `</td><td class="td" style="` + td + `">` + string(s.gooPriceIn(v, o.Currency)) + `</td></tr>`)
		p.WriteString(it.Name)
		if sku != "" {
			p.WriteString(" (#" + sku + ")")
		}
		p.WriteString(" × " + strconv.Itoa(it.Qty()) + " = " + s.gooPricePlain(v, o.Currency) + "\n")
		for _, kv := range meta {
			p.WriteString(kv.Key + ": " + kv.Value + "\n")
		}
		if note := s.gooEmailPurchaseNote(ctx, it, o, admin, args); note != "" {
			b.WriteString(`<tr><td colspan="3" style="text-align:left;vertical-align:middle;border:1px solid #eee;font-family:` + gooEmailFont + `;">` + note + `</td></tr>`)
			p.WriteString(gooPlainMsg(note) + "\n")
		}
		p.WriteString("\n")
	}
	b.WriteString(`</tbody><tfoot>`)
	p.WriteString("==========\n\n")
	for i, row := range s.gooEmailTotals(ctx, o, args) {
		border := ""
		if i == 0 {
			border = "border-top-width:4px;"
		}
		b.WriteString(`<tr><th class="td" scope="row" colspan="2" style="` + td + border + `">` + gooEsc(row[0]) + `</th><td class="td" style="` + td + border + `">` + row[1] + `</td></tr>`)
		p.WriteString(row[0] + "\t" + row[2] + "\n")
	}
	if o.CustomerNote != "" {
		b.WriteString(`<tr><th class="td" scope="row" colspan="2" style="` + td + `">Note:</th><td class="td" style="` + td + `">` + strings.ReplaceAll(gooEsc(o.CustomerNote), "\n", "<br>") + `</td></tr>`)
		p.WriteString("Note:\t" + o.CustomerNote + "\n")
	}
	b.WriteString(`</tfoot></table></div>`)
	return b.String(), p.String()
}

// gooEmailPurchaseNote is the product's purchase note, shown to customers of paid orders.
func (s *Server) gooEmailPurchaseNote(ctx context.Context, it *gooOrderItem, o *gooOrder, admin bool, args map[string]any) string {
	if admin || !o.IsPaid() || gooBool(args["preview"]) || it.ProductID == 0 {
		return ""
	}
	p, err := s.gooGetProduct(ctx, it.ProductID)
	if err != nil || strings.TrimSpace(p.PurchaseNote) == "" {
		return ""
	}
	return p.PurchaseNote
}

// gooEmailFormatAddress is WC_Countries::get_formatted_address() as lines.
func gooEmailFormatAddress(a map[string]string) []string {
	var lines []string
	add := func(v string) {
		if v = strings.TrimSpace(v); v != "" {
			lines = append(lines, v)
		}
	}
	add(a["first_name"] + " " + a["last_name"])
	add(a["company"])
	add(a["address_1"])
	add(a["address_2"])
	state := a["state"]
	if state != "" {
		state = gooStateName(a["country"], state)
	}
	add(strings.TrimSpace(a["city"] + ", " + state + " " + a["postcode"]))
	if len(lines) > 0 && strings.HasPrefix(lines[len(lines)-1], ",") {
		lines[len(lines)-1] = strings.TrimSpace(strings.TrimPrefix(lines[len(lines)-1], ","))
	}
	if a["country"] != "" {
		add(gooCountryName(a["country"]))
	}
	return lines
}

// gooEmailAddresses is email-addresses.php: billing (with phone / e-mail) and shipping addresses.
func (s *Server) gooEmailAddresses(o *gooOrder) (string, string) {
	c := s.gooEmailPalette()
	box := `padding:12px;color:` + c.TextLighter20 + `;border:1px solid #e5e5e5;`
	h2 := `<h2 style="color:` + c.Base + `;display:block;font-family:` + gooEmailFont + `;font-size:18px;font-weight:bold;line-height:130%;margin:0 0 18px;text-align:left;">`
	billing := gooEmailFormatAddress(o.Billing)
	var b strings.Builder
	var p strings.Builder
	b.WriteString(`<table id="addresses" cellspacing="0" cellpadding="0" border="0" style="width:100%;vertical-align:top;margin-bottom:40px;padding:0;"><tr>`)
	b.WriteString(`<td valign="top" width="50%" style="text-align:left;font-family:` + gooEmailFont + `;border:0;padding:0;">` + h2 + `Billing address</h2><address class="address" style="` + box + `">`)
	for i, l := range billing {
		if i > 0 {
			b.WriteString("<br>")
		}
		b.WriteString(gooEsc(l))
	}
	p.WriteString("Billing address\n\n" + strings.Join(billing, "\n") + "\n")
	if ph := o.Billing["phone"]; ph != "" {
		b.WriteString(`<br><a href="tel:` + gooEsc(ph) + `" style="color:` + c.Base + `;font-weight:normal;text-decoration:underline;">` + gooEsc(ph) + `</a>`)
		p.WriteString(ph + "\n")
	}
	if em := o.Billing["email"]; em != "" {
		b.WriteString(`<br>` + gooEsc(em))
		p.WriteString(em + "\n")
	}
	b.WriteString(`</address></td>`)
	if shipping := gooEmailFormatAddress(o.Shipping); len(shipping) > 0 && len(o.ItemsOf("shipping")) > 0 {
		esc := make([]string, len(shipping))
		for i, l := range shipping {
			esc[i] = gooEsc(l)
		}
		b.WriteString(`<td valign="top" width="50%" style="text-align:left;font-family:` + gooEmailFont + `;padding:0;">` + h2 + `Shipping address</h2><address class="address" style="` + box + `">` +
			strings.Join(esc, "<br>") + `</address></td>`)
		p.WriteString("\nShipping address\n\n" + strings.Join(shipping, "\n") + "\n")
	}
	b.WriteString(`</tr></table>`)
	return b.String(), p.String()
}
