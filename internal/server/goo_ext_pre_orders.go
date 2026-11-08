package server

import (
	"context"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/mail"
)

// Goocommerce › Pre-Orders (module "pre_orders"): WooCommerce Pre-Orders. A product takes pre-orders
// until its availability date, with an optional pre-order fee, charged upfront or upon release. Orders
// containing a pre-order wait in the "Pre-ordered" status; when the release date passes (or the store
// completes them) upfront orders go to processing and upon-release orders become pending with a
// payment link e-mailed to the customer.

const gpreKey = "pre_orders"

// gpreProduct is a product's pre-order settings.
type gpreProduct struct {
	Enabled      bool
	Availability *time.Time
	Fee          float64
	WhenToCharge string // upfront, upon_release
}

func gpreOf(p *gooProduct) gpreProduct {
	if p == nil || p.Meta == nil {
		return gpreProduct{WhenToCharge: "upfront"}
	}
	out := gpreProduct{Enabled: gxaStr(p.Meta, "_wc_pre_orders_enabled") == "yes", Fee: gxaFloat(p.Meta, "_wc_pre_orders_fee"),
		WhenToCharge: defaultStr(gxaStr(p.Meta, "_wc_pre_orders_when_to_charge"), "upfront")}
	if ts := gxaInt64(p.Meta, "_wc_pre_orders_availability_datetime"); ts > 0 {
		t := time.Unix(ts, 0)
		out.Availability = &t
	}
	return out
}

// gpreActive reports whether a product takes pre-orders now (enabled, not released yet). A variation
// follows its parent.
func (s *Server) gpreActive(ctx context.Context, p *gooProduct) (gpreProduct, bool) {
	if p == nil {
		return gpreProduct{}, false
	}
	if p.Type == gooTypeVariation {
		if parent := s.gooParent(ctx, p); parent != nil {
			p = parent
		}
	}
	po := gpreOf(p)
	if !po.Enabled {
		return po, false
	}
	if po.Availability != nil && !po.Availability.After(time.Now()) {
		return po, false
	}
	return po, true
}

// gpreDate is the availability date for messages ("at a future date" without one).
func (s *Server) gpreDate(po gpreProduct) string {
	if po.Availability == nil {
		return "at a future date"
	}
	return s.gxaDate(po.Availability)
}

func (s *Server) gpreMessage(field string, po gpreProduct) string {
	return strings.ReplaceAll(s.gooExtString(gpreKey, field), "{availability_date}", s.gpreDate(po))
}

func init() {
	gooOrderStatuses = append(gooOrderStatuses, gooOpt{"pre-ordered", "Pre-ordered"})

	gooRegisterExtension(&gooExtension{
		Key: gpreKey, Name: "Pre-Orders", Icon: "dashicons-calendar-alt", Category: "Merchandising",
		Description: "Allow customers to order products before they are available: charge upfront or upon release, add a pre-order fee, and complete every pre-order automatically on the release date.",
		Fields:      gpreFields(),
		Pages:       []gooExtMenuItem{{Page: "pre-orders", Title: "Pre-orders"}},
	})
	gooExtAdminPages["pre-orders"] = func(s *Server, c *gin.Context) { s.gpreAdmin(c) }

	gooProductPanels = append(gooProductPanels, &gooProductPanel{ID: "wc_pre_orders_data", Label: "Pre-orders", Module: gpreKey,
		ShowFor: []string{"simple", "variable"}, Render: gprePanelRender, Save: gprePanelSave})

	// The button text and the availability message on the product page and in the shop loop.
	gooOnHTML("single_after_add_to_cart", func(s *Server, c *gin.Context, p *gooProduct) template.HTML {
		if !s.gooExtOn(gpreKey) {
			return ""
		}
		po, ok := s.gpreActive(c.Request.Context(), p)
		if !ok {
			return ""
		}
		btn := s.gooExtString(gpreKey, "add_to_cart_button_text")
		charge := s.gooExtString(gpreKey, "upfront_message")
		if po.WhenToCharge == "upon_release" {
			charge = s.gooExtString(gpreKey, "upon_release_message")
		}
		out := `<div class="wc-pre-orders-availability-date"><p class="wc-pre-orders-message">` + s.gpreMessage("single_product_message", po) + `</p>`
		if charge != "" {
			out += `<p class="wc-pre-orders-charge-message">` + gooEsc(charge) + `</p>`
		}
		out += `</div><script>(function(){var b=document.querySelectorAll('form.cart .single_add_to_cart_button');for(var i=0;i<b.length;i++){b[i].textContent=` +
			strconv.Quote(btn) + `;b[i].classList.add('wc-pre-orders-button');}})();</script>`
		return template.HTML(out)
	})
	gooOnHTML("loop_after_item", func(s *Server, c *gin.Context, p *gooProduct) template.HTML {
		if !s.gooExtOn(gpreKey) {
			return ""
		}
		if po, ok := s.gpreActive(c.Request.Context(), p); ok {
			return template.HTML(`<div class="wc-pre-orders-availability-date wc-pre-orders-shop-message">` + s.gpreMessage("shop_loop_product_message", po) + `</div>`)
		}
		return ""
	})

	// A pre-order may not share the cart with other products.
	gooAddToCartHooks = append(gooAddToCartHooks, func(s *Server, ctx context.Context, p *gooProduct, qty int, form map[string][]string, data map[string]any) error {
		if !s.gooExtOn(gpreKey) || s.gooExtBool(gpreKey, "allow_mixed_cart") {
			return nil
		}
		cart := gxaCart(ctx)
		if cart == nil || cart.IsEmpty() {
			return nil
		}
		_, adding := s.gpreActive(ctx, p)
		for _, it := range cart.Items {
			_, pre := s.gpreActive(ctx, it.Product())
			if adding && (!pre || (it.ProductID != p.ID && it.ProductID != p.ParentID)) {
				return gooCartErr("Your cart already contains items. A pre-order must be purchased separately: please complete or empty your cart first.")
			}
			if !adding && pre {
				return gooCartErr("Your cart contains a pre-order, which must be purchased separately. Please complete or remove the pre-order first.")
			}
		}
		return nil
	})

	gooCartItemPriceHooks = append(gooCartItemPriceHooks, func(s *Server, ctx context.Context, cart *gooCart, it *gooCartItem) {
		if !s.gooExtOn(gpreKey) {
			return
		}
		if po, ok := s.gpreActive(ctx, it.Product()); ok && po.Fee > 0 {
			it.Price += po.Fee
		}
	})
	gooCartItemDataHooks = append(gooCartItemDataHooks, func(s *Server, ctx context.Context, it *gooCartItem) []gooKV {
		if !s.gooExtOn(gpreKey) {
			return nil
		}
		po, ok := s.gpreActive(ctx, it.Product())
		if !ok {
			return nil
		}
		out := []gooKV{{Key: "Release date", Value: gxaStripTags(s.gpreDate(po))}}
		if po.Fee > 0 {
			out = append(out, gooKV{Key: "Pre-order fee", Value: s.gooPricePlain(po.Fee, s.gooCurrency())})
		}
		return out
	})

	// The order records the pre-order; charged upon release, it skips the payment.
	gooCheckoutHooks = append(gooCheckoutHooks, func(s *Server, ctx context.Context, cart *gooCart, o *gooOrder) error {
		if !s.gooExtOn(gpreKey) {
			return nil
		}
		for _, it := range cart.Items {
			po, ok := s.gpreActive(ctx, it.Product())
			if !ok {
				continue
			}
			o.Meta["_wc_pre_orders_is_pre_order"] = true
			o.Meta["_wc_pre_orders_product_id"] = float64(it.ProductID)
			o.Meta["_wc_pre_orders_when_charged"] = po.WhenToCharge
			o.Meta["_wc_pre_orders_status"] = "active"
			if po.Availability != nil {
				o.Meta["_wc_pre_orders_availability"] = float64(po.Availability.Unix())
			}
			if po.WhenToCharge == "upon_release" {
				// No payment now: the gateway finds an order that is not awaiting payment.
				o.Status = "pre-ordered"
			}
			break
		}
		return nil
	})
	gooOn("order_created", 40, func(s *Server, ctx context.Context, arg any) {
		o, ok := arg.(*gooOrder)
		if !ok || !s.gooExtOn(gpreKey) || o.Status != "pre-ordered" {
			return
		}
		_ = s.gooReduceStock(ctx, o, false) // a pre-order may sell stock still to come
		_ = s.gooAddOrderNote(ctx, o, "Order placed as a pre-order; payment will be requested upon release.", false, "")
		_ = s.gooTriggerEmail(ctx, "wc_pre_orders_new_pre_order", o, nil)
		_ = s.gooTriggerEmail(ctx, "wc_pre_orders_pre_ordered", o, nil)
	})
	gooOn("order_status_changed", 40, func(s *Server, ctx context.Context, arg any) {
		ch, ok := arg.(*gooStatusChange)
		if !ok || !s.gooExtOn(gpreKey) {
			return
		}
		o := ch.Order
		if o.Meta["_wc_pre_orders_is_pre_order"] != true || gxaStr(o.Meta, "_wc_pre_orders_status") != "active" {
			return
		}
		switch ch.To {
		case "processing", "completed", "on-hold":
			if ch.To == "on-hold" && gxaStr(o.Meta, "_wc_pre_orders_when_charged") == "upfront" {
				return // awaiting an offline payment
			}
			_ = s.gooSetStatus(ctx, o, "pre-ordered", "Pre-order placed.", "")
			_ = s.gooTriggerEmail(ctx, "wc_pre_orders_new_pre_order", o, nil)
			_ = s.gooTriggerEmail(ctx, "wc_pre_orders_pre_ordered", o, nil)
		case "cancelled":
			if ch.From == "pre-ordered" {
				o.Meta["_wc_pre_orders_status"] = "cancelled"
				_ = s.gooSaveOrder(ctx, o, false)
				_ = s.gooTriggerEmail(ctx, "wc_pre_orders_pre_order_cancelled", o, nil)
			}
		}
	})
	for _, job := range []string{"hourly", "daily"} {
		gooOn(job, 40, func(s *Server, ctx context.Context, _ any) {
			if s.gooExtOn(gpreKey) {
				s.gpreReleaseDue(ctx)
			}
		})
	}

	for _, e := range []*gooEmailDef{
		{ID: "wc_pre_orders_new_pre_order", Title: "New pre-order", Description: "New pre-order emails are sent when a pre-order is received.",
			Subject: "[{site_title}] New customer pre-order ({order_number}) - {order_date}", Heading: "New customer pre-order"},
		{ID: "wc_pre_orders_pre_ordered", Title: "Pre-order received", Customer: true, Description: "This is an order notification sent to the customer after placing a pre-order.",
			Subject: "Your {site_title} pre-order from {order_date}", Heading: "Thanks for your pre-order"},
		{ID: "wc_pre_orders_pre_order_available", Title: "Pre-order available", Customer: true,
			Description: "This is an order notification sent to the customer once a pre-order is complete.",
			Subject:     "Your {site_title} pre-order from {order_date} is now available", Heading: "Pre-order {order_number} is now available"},
		{ID: "wc_pre_orders_pre_order_cancelled", Title: "Pre-order cancelled", Customer: true,
			Description: "This is an order notification sent to the customer when their pre-order is cancelled.",
			Subject:     "Your {site_title} pre-order from {order_date} has been cancelled", Heading: "Pre-order cancelled"},
		{ID: "wc_pre_orders_pre_order_date_changed", Title: "Pre-order date changed", Customer: true,
			Description: "This is an order notification sent to the customer when the release date of their pre-order is changed.",
			Subject:     "Your {site_title} pre-order release date has been changed", Heading: "Pre-order release date changed"},
	} {
		e.Module = gpreKey
		gooRegisterEmail(e)
	}
}

func gpreFields() []gooField {
	return []gooField{
		{ID: "button_title", Title: "Button text", Type: "title"},
		{ID: "add_to_cart_button_text", Title: "Add to cart button text", Type: "text", Default: "Pre-order now", Desc: "This controls the add to cart button text on single product pages for products that have pre-orders enabled."},
		{ID: "place_order_button_text", Title: "Place order button text", Type: "text", Default: "Place pre-order now", Desc: "This controls the place order button text on the checkout when an order contains a pre-order."},
		{ID: "button_end", Type: "sectionend"},
		{ID: "product_title", Title: "Product message", Type: "title", Desc: "Use {availability_date} to show the pre-order release date."},
		{ID: "single_product_message", Title: "Single product page message", Type: "textarea", Default: "Available on {availability_date}."},
		{ID: "shop_loop_product_message", Title: "Shop loop product message", Type: "textarea", Default: "Available on {availability_date}."},
		{ID: "upfront_message", Title: "Charged upfront message", Type: "text", Default: "Charged upfront"},
		{ID: "upon_release_message", Title: "Charged upon release message", Type: "text", Default: "Charged upon release"},
		{ID: "product_end", Type: "sectionend"},
		{ID: "cart_title", Title: "Cart", Type: "title"},
		{ID: "allow_mixed_cart", Title: "Mixed cart", Type: "checkbox", Default: false, Desc: "Allow pre-order products and other products in the same cart"},
		{ID: "cart_end", Type: "sectionend"},
	}
}

// Product panel -----------------------------------------------------------------------------------------

func gprePanelRender(s *Server, c *gin.Context, p *gooProduct) template.HTML {
	po := gpreOf(p)
	checked := ""
	if po.Enabled {
		checked = ` checked="checked"`
	}
	fee := ""
	if po.Fee > 0 {
		fee = gooFormatDecimal(po.Fee, s.gooDecimals())
	}
	var b strings.Builder
	b.WriteString(`<div id="wc_pre_orders_data" class="panel woocommerce_options_panel"><div class="options_group">`)
	b.WriteString(`<p class="form-field _wc_pre_orders_enabled_field"><label for="_wc_pre_orders_enabled">Enable pre-orders</label><input type="checkbox" class="checkbox" name="_wc_pre_orders_enabled" id="_wc_pre_orders_enabled" value="yes"` + checked + `> <span class="description">Enable pre-orders for this product.</span></p>`)
	b.WriteString(`<p class="form-field _wc_pre_orders_availability_datetime_field"><label for="_wc_pre_orders_availability_datetime">Availability date/time</label><input type="datetime-local" class="short" name="_wc_pre_orders_availability_datetime" id="_wc_pre_orders_availability_datetime" value="` + s.gxaInputDate(po.Availability, true) + `"> <span class="description">Set the date and time when pre-orders will be completed. Leave blank to complete them manually.</span></p>`)
	b.WriteString(`<p class="form-field _wc_pre_orders_fee_field"><label for="_wc_pre_orders_fee">Pre-order fee (` + gooCurrencySymbol(s.gooCurrency()) + `)</label><input type="text" class="short wc_input_price" name="_wc_pre_orders_fee" id="_wc_pre_orders_fee" value="` + gooEsc(fee) + `"> <span class="description">Set a fee to be charged when a pre-order is placed. Leave blank to not charge a pre-order fee.</span></p>`)
	b.WriteString(`<p class="form-field _wc_pre_orders_when_to_charge_field"><label for="_wc_pre_orders_when_to_charge">When to charge</label>` +
		gxaSelect("_wc_pre_orders_when_to_charge", "_wc_pre_orders_when_to_charge", []gooOpt{{"upfront", "Upfront"}, {"upon_release", "Upon release"}}, po.WhenToCharge, ` class="select short"`) +
		` <span class="description">Select "Upon release" to charge the entire pre-order amount (the product price + pre-order fee if applicable) when the pre-order becomes available. Select "Upfront" to charge the entire pre-order amount during the initial checkout.</span></p>`)
	b.WriteString(`</div></div>`)
	return template.HTML(b.String())
}

func gprePanelSave(s *Server, c *gin.Context, p *gooProduct) {
	if p.Meta == nil {
		p.Meta = map[string]any{}
	}
	if c.PostForm("_wc_pre_orders_enabled") != "" {
		p.Meta["_wc_pre_orders_enabled"] = "yes"
	} else {
		p.Meta["_wc_pre_orders_enabled"] = "no"
	}
	if t := s.gxaParseTime(c.PostForm("_wc_pre_orders_availability_datetime")); t != nil {
		p.Meta["_wc_pre_orders_availability_datetime"] = float64(t.Unix())
	} else {
		delete(p.Meta, "_wc_pre_orders_availability_datetime")
	}
	fee := 0.0
	if v := s.gooParseAmount(c.PostForm("_wc_pre_orders_fee")); v != nil && *v > 0 {
		fee = *v
	}
	p.Meta["_wc_pre_orders_fee"] = fee
	when := c.PostForm("_wc_pre_orders_when_to_charge")
	if when != "upon_release" {
		when = "upfront"
	}
	p.Meta["_wc_pre_orders_when_to_charge"] = when
}

// Release ------------------------------------------------------------------------------------------------

// gprePreOrders are the orders waiting on a product's release (all products when 0).
func (s *Server) gprePreOrders(ctx context.Context, productID int64) []*gooOrder {
	q := `SELECT id FROM goo_orders WHERE type = 'shop_order' AND status = 'pre-ordered'`
	args := []any{}
	if productID != 0 {
		q += ` AND (meta->>'_wc_pre_orders_product_id')::numeric = $1`
		args = append(args, productID)
	}
	rows, err := s.store.Pool.Query(ctx, q+` ORDER BY id`, args...)
	if err != nil {
		return nil
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	var out []*gooOrder
	for _, id := range ids {
		if o, err := s.gooGetOrder(ctx, id); err == nil {
			out = append(out, o)
		}
	}
	return out
}

// gpreComplete releases a pre-order: paid ones go to processing, upon-release ones become pending
// and the customer gets the payment link.
func (s *Server) gpreComplete(ctx context.Context, o *gooOrder) error {
	o.Meta["_wc_pre_orders_status"] = "completed"
	paid := o.DatePaid != nil || gxaStr(o.Meta, "_wc_pre_orders_when_charged") != "upon_release"
	if paid {
		to := "processing"
		if !s.gooOrderNeedsProcessing(ctx, o) {
			to = "completed"
		}
		if err := s.gooSetStatus(ctx, o, to, "Pre-order completed.", ""); err != nil {
			return err
		}
	} else {
		if err := s.gooSetStatus(ctx, o, "pending", "Pre-order released; awaiting payment.", ""); err != nil {
			return err
		}
		_ = s.gooTriggerEmail(ctx, "customer_invoice", o, nil)
	}
	_ = s.gooTriggerEmail(ctx, "wc_pre_orders_pre_order_available", o, nil)
	return nil
}

// gpreReleaseDue completes the pre-orders whose product's availability date passed.
func (s *Server) gpreReleaseDue(ctx context.Context) {
	now := time.Now()
	products := map[int64]*gooProduct{}
	for _, o := range s.gprePreOrders(ctx, 0) {
		pid := gxaInt64(o.Meta, "_wc_pre_orders_product_id")
		p, ok := products[pid]
		if !ok {
			p, _ = s.gooGetProduct(ctx, pid)
			products[pid] = p
		}
		var avail *time.Time
		if p != nil {
			avail = gpreOf(p).Availability
		} else if ts := gxaInt64(o.Meta, "_wc_pre_orders_availability"); ts > 0 {
			t := time.Unix(ts, 0)
			avail = &t
		}
		if avail == nil || avail.After(now) {
			continue
		}
		_ = s.gpreComplete(ctx, o)
	}
	// Released products stop taking pre-orders.
	for _, p := range products {
		if p == nil {
			continue
		}
		if po := gpreOf(p); po.Enabled && po.Availability != nil && !po.Availability.After(now) {
			p.Meta["_wc_pre_orders_enabled"] = "no"
			_ = s.gooSaveProduct(ctx, p, 0)
		}
	}
}

// Admin ----------------------------------------------------------------------------------------------------

// gpreAdmin is Goocommerce › Pre-orders: products with pre-orders and their actions, and the orders.
func (s *Server) gpreAdmin(c *gin.Context) {
	ctx := c.Request.Context()
	if c.Request.Method == "POST" {
		if !s.checkNonce(c, "gpre-actions") {
			s.denyNonce(c)
			return
		}
		pid, _ := strconv.ParseInt(c.PostForm("product_id"), 10, 64)
		orders := s.gprePreOrders(ctx, pid)
		if pid == 0 {
			gxaAdminRedirect(c, "pre-orders", "", "Please choose a product.", true)
			return
		}
		n := 0
		switch c.PostForm("action_type") {
		case "complete":
			for _, o := range orders {
				if s.gpreComplete(ctx, o) == nil {
					n++
				}
			}
			if p, err := s.gooGetProduct(ctx, pid); err == nil {
				p.Meta["_wc_pre_orders_enabled"] = "no"
				_ = s.gooSaveProduct(ctx, p, currentUser(c).ID)
			}
			gxaAdminRedirect(c, "pre-orders", "", fmt.Sprintf("%d pre-orders completed.", n), false)
		case "cancel":
			for _, o := range orders {
				if s.gooSetStatus(ctx, o, "cancelled", "Pre-order cancelled by the store.", "") == nil {
					n++
				}
			}
			gxaAdminRedirect(c, "pre-orders", "", fmt.Sprintf("%d pre-orders cancelled.", n), false)
		case "change-date":
			t := s.gxaParseTime(c.PostForm("new_availability_date"))
			if t == nil || t.Before(time.Now()) {
				gxaAdminRedirect(c, "pre-orders", "tab=actions&product_id="+itoa64(pid), "Please enter a release date in the future.", true)
				return
			}
			p, err := s.gooGetProduct(ctx, pid)
			if err != nil {
				gxaAdminRedirect(c, "pre-orders", "", "Product not found.", true)
				return
			}
			p.Meta["_wc_pre_orders_availability_datetime"] = float64(t.Unix())
			_ = s.gooSaveProduct(ctx, p, currentUser(c).ID)
			msg := strings.TrimSpace(c.PostForm("message"))
			for _, o := range orders {
				o.Meta["_wc_pre_orders_availability"] = float64(t.Unix())
				_ = s.gooSaveOrder(ctx, o, false)
				_ = s.gooAddOrderNote(ctx, o, "Pre-order release date changed to "+gxaStripTags(s.gxaDate(t))+".", false, "")
				if c.PostForm("send_email") != "" {
					_ = s.gooTriggerEmail(ctx, "wc_pre_orders_pre_order_date_changed", o, map[string]any{"message": msg, "availability_date": gxaStripTags(s.gxaDate(t))})
				}
				n++
			}
			gxaAdminRedirect(c, "pre-orders", "", fmt.Sprintf("Release date changed for %d pre-orders.", n), false)
		case "email":
			msg := strings.TrimSpace(c.PostForm("message"))
			if msg == "" {
				gxaAdminRedirect(c, "pre-orders", "tab=actions&product_id="+itoa64(pid), "Please enter a message.", true)
				return
			}
			subject := defaultStr(strings.TrimSpace(c.PostForm("subject")), "An update on your pre-order from "+s.settings.String("blogname"))
			for _, o := range orders {
				if email := o.Billing["email"]; email != "" {
					s.sendMail(ctx, mail.Message{To: []string{email}, Subject: subject, Body: msg,
						HTML: "<p>" + strings.ReplaceAll(gooEsc(msg), "\n", "<br>") + "</p>"})
					_ = s.gooAddOrderNote(ctx, o, "Pre-order customer e-mailed: "+gooEsc(subject), false, "")
					n++
				}
			}
			gxaAdminRedirect(c, "pre-orders", "", fmt.Sprintf("%d customers e-mailed.", n), false)
		default:
			gxaAdminRedirect(c, "pre-orders", "", "", false)
		}
		return
	}
	nonce := s.nonce(c, "gpre-actions")
	tab := defaultStr(c.Query("tab"), "manage")
	var b strings.Builder
	b.WriteString(`<nav class="nav-tab-wrapper woo-nav-tab-wrapper">`)
	for _, t := range []gooOpt{{"manage", "Manage"}, {"actions", "Actions"}} {
		cls := "nav-tab"
		if t.Value == tab {
			cls += " nav-tab-active"
		}
		b.WriteString(`<a href="/wp-admin/goocommerce?page=pre-orders&amp;tab=` + t.Value + `" class="` + cls + `">` + t.Label + `</a>`)
	}
	b.WriteString(`</nav>`)
	all := s.gprePreOrders(ctx, 0)
	byProduct := map[int64][]*gooOrder{}
	var pids []int64
	for _, o := range all {
		pid := gxaInt64(o.Meta, "_wc_pre_orders_product_id")
		if _, ok := byProduct[pid]; !ok {
			pids = append(pids, pid)
		}
		byProduct[pid] = append(byProduct[pid], o)
	}
	// Products with pre-orders enabled appear too, even without orders.
	if rows, err := s.store.Pool.Query(ctx, `SELECT post_id FROM goo_products WHERE meta->>'_wc_pre_orders_enabled' = 'yes' ORDER BY post_id`); err == nil {
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil && byProduct[id] == nil {
				pids = append(pids, id)
				byProduct[id] = nil
			}
		}
		rows.Close()
	}
	products, _ := s.gooGetProducts(ctx, pids)
	if tab == "actions" {
		sel, _ := strconv.ParseInt(c.Query("product_id"), 10, 64)
		opts := []gooOpt{{"", "Select a product"}}
		for _, id := range pids {
			if p := products[id]; p != nil {
				opts = append(opts, gooOpt{itoa64(id), fmt.Sprintf("%s (%d)", p.Name, len(byProduct[id]))})
			}
		}
		form := func(action, title, desc, fields, button string) {
			b.WriteString(`<div class="postbox"><h2 class="hndle"><span>` + title + `</span></h2><div class="inside"><p>` + desc + `</p>` +
				`<form method="post" action="/wp-admin/goocommerce?page=pre-orders"><input type="hidden" name="_wpnonce" value="` + nonce + `"><input type="hidden" name="action_type" value="` + action + `">` +
				`<table class="form-table"><tbody><tr><th scope="row">Product</th><td>` + gxaSelect("product_id", "", opts, itoa64(sel), "") + `</td></tr>` + fields +
				`</tbody></table><p class="submit"><button type="submit" class="button button-primary">` + button + `</button></p></form></div></div>`)
		}
		b.WriteString(`<div id="poststuff">`)
		form("email", "Email", "You may send an email message to all customers who have pre-ordered a specific product.",
			`<tr><th scope="row">Subject</th><td><input type="text" name="subject" class="regular-text"></td></tr><tr><th scope="row">Message</th><td><textarea name="message" rows="4" class="large-text"></textarea></td></tr>`, "Send Emails")
		form("change-date", "Change release date", "You may change the release date for all pre-orders of a specific product. This will send an email notification to each customer informing them that the pre-order release date was changed, along with the new release date.",
			`<tr><th scope="row">New availability date</th><td><input type="datetime-local" name="new_availability_date"></td></tr><tr><th scope="row">Message</th><td><textarea name="message" rows="3" class="large-text"></textarea></td></tr><tr><th scope="row">Send email</th><td><label><input type="checkbox" name="send_email" value="1" checked> Send the customer a notification e-mail</label></td></tr>`, "Change Release Date")
		form("complete", "Complete", "You may complete all pre-orders for a specific product. This will charge the customer's card the pre-ordered amount (upon release pre-orders become pending payment) and mark their order as processing.", "", "Complete Pre-Orders")
		form("cancel", "Cancel", "You may cancel all pre-orders for a specific product. This will mark the order as cancelled and send an email notification to each customer.", "", "Cancel Pre-Orders")
		b.WriteString(`</div>`)
		s.gxaAdminRender(c, "pre-orders", "Pre-orders", "goo-ext-pre-orders", `<h1 class="wp-heading-inline">Pre-orders</h1>`, b.String())
		return
	}
	b.WriteString(`<h2>Products</h2><table class="wp-list-table widefat fixed striped"><thead><tr><th class="column-primary">Product</th><th>Availability date</th><th>When charged</th><th>Pre-orders</th><th>Actions</th></tr></thead><tbody>`)
	if len(pids) == 0 {
		b.WriteString(`<tr class="no-items"><td colspan="5">No products with pre-orders.</td></tr>`)
	}
	for _, id := range pids {
		p := products[id]
		if p == nil {
			continue
		}
		po := gpreOf(p)
		b.WriteString(`<tr><td class="column-primary"><strong><a href="/wp-admin/post?post=` + itoa64(id) + `&amp;action=edit">` + gooEsc(p.Name) + `</a></strong></td>` +
			`<td>` + s.gxaDateTime(po.Availability) + `</td><td>` + map[string]string{"upfront": "Upfront", "upon_release": "Upon release"}[po.WhenToCharge] + `</td>` +
			`<td>` + strconv.Itoa(len(byProduct[id])) + `</td><td><a class="button" href="/wp-admin/goocommerce?page=pre-orders&amp;tab=actions&amp;product_id=` + itoa64(id) + `">Actions</a></td></tr>`)
	}
	b.WriteString(`</tbody></table><h2>Pre-orders</h2><table class="wp-list-table widefat fixed striped"><thead><tr><th class="column-primary">Order</th><th>Customer</th><th>Product</th><th>Status</th><th>Release date</th><th>Total</th></tr></thead><tbody>`)
	if len(all) == 0 {
		b.WriteString(`<tr class="no-items"><td colspan="6">No pre-orders found.</td></tr>`)
	}
	for _, o := range all {
		pid := gxaInt64(o.Meta, "_wc_pre_orders_product_id")
		name := "#" + itoa64(pid)
		var avail *time.Time
		if p := products[pid]; p != nil {
			name, avail = p.Name, gpreOf(p).Availability
		}
		b.WriteString(`<tr><td class="column-primary">` + gxaOrderLink(o.ID) + `</td><td>` + gooEsc(o.BillingName()) + ` <span class="description">` + gooEsc(o.Billing["email"]) + `</span></td>` +
			`<td>` + gooEsc(name) + `</td><td><mark class="order-status status-pre-ordered"><span>` + gooEsc(gooStatusLabel(o.Status)) + `</span></mark></td>` +
			`<td>` + s.gxaDateTime(avail) + `</td><td>` + string(s.gooPriceIn(o.Total, o.Currency)) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)
	s.gxaAdminRender(c, "pre-orders", "Pre-orders", "goo-ext-pre-orders", `<h1 class="wp-heading-inline">Pre-orders</h1>`, b.String())
}
