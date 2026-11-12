package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Subscriptions: what customers and store managers change on an existing subscription — addresses,
// payment method, resubscribing, manual subscriptions, line items and processing a renewal by hand.

// gsubAddrFields are the editable address fields (label per key).
var gsubAddrFields = []gooOpt{
	{"first_name", "First name"}, {"last_name", "Last name"}, {"company", "Company"}, {"address_1", "Address line 1"},
	{"address_2", "Address line 2"}, {"city", "City"}, {"state", "State / County"}, {"postcode", "Postcode / ZIP"},
	{"country", "Country / Region"}, {"email", "Email"}, {"phone", "Phone"},
}

// gsubReadAddress reads prefix_key form fields (prefix "billing" → billing_first_name …) over the old address.
func gsubReadAddress(c *gin.Context, prefix string, old map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range old {
		out[k] = v
	}
	for _, f := range gsubAddrFields {
		if v, ok := c.GetPostForm(prefix + "_" + f.Value); ok {
			if prefix == "shipping" && f.Value == "email" {
				continue
			}
			out[f.Value] = strings.TrimSpace(v)
		}
	}
	return out
}

// gsubAddressInputs are the address inputs of a form.
func gsubAddressInputs(prefix string, addr map[string]string) string {
	var b strings.Builder
	for _, f := range gsubAddrFields {
		if prefix == "shipping" && (f.Value == "email" || f.Value == "phone") {
			continue
		}
		id := prefix + "_" + f.Value
		b.WriteString(`<p class="form-row form-row-wide" id="` + id + `_field"><label for="` + id + `">` + f.Label + `</label>` +
			`<input type="text" class="input-text" name="` + id + `" id="` + id + `" value="` + gooEsc(addr[f.Value]) + `"></p>`)
	}
	return b.String()
}

// gsubPaymentChoices are the payment methods a customer may switch a subscription to: gateways that
// renew without a stored token, gateways that collect a new card on the provider's page
// (gooTokenGateway: the change finishes when the customer returns from it), or any gateway when
// manual renewals are accepted.
func (s *Server) gsubPaymentChoices() []*gooGatewayHandle {
	var out []*gooGatewayHandle
	manual := s.gooExtBool(gsubKey, "accept_manual_renewals") && !s.gooExtBool(gsubKey, "turn_off_automatic_payments")
	for _, g := range s.gooGatewaysOrdered() {
		if !g.Enabled() {
			continue
		}
		auto := false
		if rg, ok := g.gooGatewayDef.(gooRenewalGateway); ok && g.Supports("subscriptions") {
			if _, setup := g.gooGatewayDef.(gooTokenGateway); !rg.RenewalNeedsToken() || setup {
				auto = true
			}
		}
		if auto && !s.gooExtBool(gsubKey, "turn_off_automatic_payments") || manual {
			out = append(out, g)
		}
	}
	return out
}

// gsubCanResubscribe reports whether a subscription ended and may be bought again.
func gsubCanResubscribe(sub *gooSubscription) bool {
	return containsStr([]string{"cancelled", "expired"}, sub.Status) && len(sub.Items) > 0
}

// gsubAccountExtras are the "Change address", "Change payment" and "Resubscribe" parts of the
// view-subscription page.
func (s *Server) gsubAccountExtras(c *gin.Context, sub *gooSubscription) string {
	ctx := c.Request.Context()
	view := s.gooEndpointURL(ctx, "myaccount", "view-subscription", itoa64(sub.ID))
	var b strings.Builder
	if gsubCanResubscribe(sub) {
		b.WriteString(`<p><a href="` + gooEsc(view+"?gsub_resubscribe="+itoa64(sub.ID)+"&_wpnonce="+s.nonce(c, "gsub-"+itoa64(sub.ID))) + `" class="button resubscribe">Resubscribe</a></p>`)
	}
	if sub.Status == "cancelled" || sub.Status == "expired" {
		return b.String()
	}
	nonce := s.nonce(c, "gsub-"+itoa64(sub.ID))
	form := func(kind string) string {
		return `<form method="post" action="` + gooEsc(view) + `" class="woocommerce-EditAccountForm edit-account gsub-change-` + kind + `">` +
			`<input type="hidden" name="gsub_form" value="` + kind + `"><input type="hidden" name="subscription_id" value="` + itoa64(sub.ID) + `">` +
			`<input type="hidden" name="_wpnonce" value="` + nonce + `">`
	}
	if choices := s.gsubPaymentChoices(); len(choices) > 0 {
		b.WriteString(`<section class="gsub-change-payment-section"><h2>Change payment method</h2>` + form("payment") + `<ul class="wc_payment_methods payment_methods methods">`)
		for _, g := range choices {
			checked := ""
			if g.ID() == sub.PaymentMethod {
				checked = " checked"
			}
			b.WriteString(`<li class="wc_payment_method payment_method_` + gooEsc(g.ID()) + `"><input id="payment_method_` + gooEsc(g.ID()) + `" type="radio" class="input-radio" name="payment_method" value="` + gooEsc(g.ID()) + `"` + checked + `> ` +
				`<label for="payment_method_` + gooEsc(g.ID()) + `">` + gooEsc(g.Title()) + `</label></li>`)
		}
		b.WriteString(`</ul><p><button type="submit" class="button" name="save_payment" value="1">Change payment method</button></p></form></section>`)
	}
	b.WriteString(`<section class="gsub-change-address-section"><h2>Change address</h2>` + form("address") +
		`<h3>Billing address</h3>` + gsubAddressInputs("billing", sub.Billing) + `<h3>Shipping address</h3>` + gsubAddressInputs("shipping", sub.Shipping) +
		`<p><button type="submit" class="button" name="save_address" value="1">Save address</button></p></form></section>`)
	return b.String()
}

// gsubFrontPost handles the customer's address / payment forms and the resubscribe link; false when the
// request is none of them.
func gsubFrontPost(s *Server, c *gin.Context) bool {
	resub := c.Query("gsub_resubscribe")
	kind := ""
	if c.Request.Method == http.MethodPost {
		kind = c.PostForm("gsub_form")
	}
	if resub == "" && kind == "" {
		return false
	}
	ctx := c.Request.Context()
	idStr := resub
	if kind != "" {
		idStr = c.PostForm("subscription_id")
	}
	id, _ := strconv.ParseInt(idStr, 10, 64)
	u := currentUser(c)
	sub, err := s.gsubGet(ctx, id)
	back := s.gooAccountURL(ctx, "subscriptions")
	fail := func(msg string) bool {
		s.gooAddNotice(c, "error", msg)
		s.gooSaveSession(c)
		c.Redirect(302, back)
		return true
	}
	if err != nil || u == nil || sub.CustomerID != u.ID {
		return fail("That subscription does not exist. Please contact us if you need assistance.")
	}
	if !s.checkNonce(c, "gsub-"+itoa64(sub.ID)) {
		return fail("Security error. Please contact us if you need assistance.")
	}
	view := s.gooEndpointURL(ctx, "myaccount", "view-subscription", itoa64(sub.ID))
	back = view
	switch {
	case resub != "":
		if !gsubCanResubscribe(sub) {
			return fail("That subscription can not be resubscribed to.")
		}
		cart := s.gooCartFor(c)
		added := 0
		for _, it := range sub.Items {
			if _, err := cart.Add(ctx, it.ProductID, max(it.Qty, 1), it.VariationID, nil, nil, map[string]any{"subscription_resubscribe": float64(sub.ID)}); err != nil {
				s.gooAddNotice(c, "error", err.Error())
				continue
			}
			added++
		}
		s.gooSaveSession(c)
		if added == 0 {
			c.Redirect(302, view)
			return true
		}
		c.Redirect(302, s.gooPageURL(ctx, "cart"))
		return true
	case kind == "address":
		if sub.Status == "cancelled" || sub.Status == "expired" {
			return fail("That subscription can not be changed.")
		}
		billing := gsubReadAddress(c, "billing", sub.Billing)
		if billing["email"] != "" && !strings.Contains(billing["email"], "@") {
			s.gooAddNotice(c, "error", "Please provide a valid email address.")
			s.gooSaveSession(c)
			c.Redirect(302, view)
			return true
		}
		sub.Billing, sub.Shipping = billing, gsubReadAddress(c, "shipping", sub.Shipping)
		s.gsubAddNote(ctx, sub, "Billing and shipping address updated by the subscriber.", false, "customer")
		if err := s.gsubSave(ctx, sub); err != nil {
			return fail(gooEsc(err.Error()))
		}
		s.gooAddNotice(c, "success", "Address changed successfully.")
	case kind == "payment":
		if sub.Status == "cancelled" || sub.Status == "expired" {
			return fail("That subscription can not be changed.")
		}
		want := c.PostForm("payment_method")
		var pick *gooGatewayHandle
		for _, g := range s.gsubPaymentChoices() {
			if g.ID() == want {
				pick = g
			}
		}
		if pick == nil {
			s.gooAddNotice(c, "error", "Invalid payment method.")
			s.gooSaveSession(c)
			c.Redirect(302, view)
			return true
		}
		if rg, ok := pick.gooGatewayDef.(gooRenewalGateway); ok && rg.RenewalNeedsToken() && !s.gooExtBool(gsubKey, "turn_off_automatic_payments") {
			if tg, ok := pick.gooGatewayDef.(gooTokenGateway); ok {
				// A card gateway: the customer enters the new card on the provider's page; the
				// subscription changes when they come back (or the provider's webhook says so).
				target, err := tg.SetupURL(s, c, sub.ID)
				if err != nil {
					return fail(gooEsc(err.Error()))
				}
				s.gooSaveSession(c)
				c.Redirect(302, target)
				return true
			}
		}
		if pick.ID() != sub.PaymentMethod {
			sub.PaymentMethod, sub.PaymentMethodTitle = pick.ID(), pick.Title()
			delete(sub.Meta, "_payment_token") // a token belongs to the old method
			s.gsubAddNote(ctx, sub, "Payment method changed to "+pick.Title()+" by the subscriber.", false, "customer")
			if err := s.gsubSave(ctx, sub); err != nil {
				return fail(gooEsc(err.Error()))
			}
		}
		s.gooAddNotice(c, "success", "Payment method updated.")
	}
	s.gooSaveSession(c)
	c.Redirect(302, view)
	return true
}

// Admin -------------------------------------------------------------------------------------------------

// gsubAdminApplyEdits copies the Edit Subscription form's addresses and line items onto the subscription.
// Lines can be removed (item_remove[i]) and one added per save (add_product, add_qty, add_total), and
// the customer changed (customer_id). The returned error is shown after the valid edits are saved.
func (s *Server) gsubAdminApplyEdits(c *gin.Context, sub *gooSubscription, by string) error {
	ctx := c.Request.Context()
	sub.Billing, sub.Shipping = gsubReadAddress(c, "billing", sub.Billing), gsubReadAddress(c, "shipping", sub.Shipping) // only the posted fields change
	changed := false
	for i := range sub.Items {
		idx := strconv.Itoa(i)
		it := &sub.Items[i]
		if v, ok := c.GetPostForm("item_qty[" + idx + "]"); ok {
			if q, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && q >= 1 && q <= gooMaxLineQty {
				it.Qty, changed = q, true
			}
		}
		if v, ok := c.GetPostForm("item_price[" + idx + "]"); ok {
			if p, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && p >= 0 {
				it.Price, changed = gooRound(p, s.gooDecimals()), true
			}
		}
		if v, ok := c.GetPostForm("item_tax[" + idx + "]"); ok {
			if t, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && t >= 0 {
				it.Tax, changed = gooRound(t, s.gooDecimals()), true
			}
		}
		it.Total = gooRound(it.Price*float64(it.Qty), s.gooDecimals())
	}
	// Remove ticked lines (a subscription keeps at least one), then add the new line.
	var kept []gsubItem
	var removed []string
	for i, it := range sub.Items {
		if c.PostForm("item_remove["+strconv.Itoa(i)+"]") != "" {
			removed = append(removed, it.Name)
			continue
		}
		kept = append(kept, it)
	}
	var notes []string
	var err error
	if pid := gooInt(c.PostForm("add_product")); pid != 0 {
		l := gsubNewLine{ProductID: pid, Qty: int(gooInt(c.PostForm("add_qty")))}
		if v := strings.TrimSpace(c.PostForm("add_total")); v != "" {
			if f, perr := strconv.ParseFloat(v, 64); perr == nil && f >= 0 {
				l.Total = &f
			}
		}
		it, _, lerr := s.gsubLineItem(ctx, l)
		if lerr != nil {
			err = errors.New("The line could not be added: " + lerr.Error())
		} else {
			kept = append(kept, it)
			notes = append(notes, "Line item added: "+gooEsc(it.Name)+" &times; "+strconv.Itoa(it.Qty)+".")
			changed = true
		}
	}
	if len(removed) > 0 {
		if len(kept) == 0 {
			err = errors.New("A subscription needs at least one line item.")
		} else {
			notes = append(notes, "Line item removed: "+gooEsc(strings.Join(removed, ", "))+".")
			changed = true
		}
	}
	if len(kept) > 0 && (len(removed) > 0 || len(kept) != len(sub.Items)) {
		sub.Items = kept
	}
	// Customer.
	if v := strings.TrimSpace(c.PostForm("customer_id")); v != "" {
		if id := gooInt(v); id != sub.CustomerID {
			if u, uerr := s.store.UserByID(ctx, id); uerr != nil || id == 0 {
				err = errors.New("The customer does not exist.")
			} else {
				notes = append(notes, fmt.Sprintf("Customer changed from user #%d to %s (#%d).", sub.CustomerID, gooEsc(u.DisplayName), u.ID))
				sub.CustomerID = u.ID
			}
		}
	}
	for _, n := range notes {
		s.gsubAddNote(ctx, sub, n, false, by)
	}
	if changed {
		s.gsubRecalc(sub)
	}
	return err
}

// gsubAdminNew is the Add Subscription screen and its POST.
func (s *Server) gsubAdminNew(c *gin.Context) {
	ctx := c.Request.Context()
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "gsub-new") {
			s.denyNonce(c)
			return
		}
		in := gsubNew{CustomerID: gooInt(c.PostForm("customer_id")), Status: c.PostForm("status"), Period: c.PostForm("billing_period"),
			Interval: int(gooInt(c.PostForm("billing_interval"))), PaymentMethod: c.PostForm("payment_method"),
			Billing: gsubReadAddress(c, "billing", nil), Shipping: gsubReadAddress(c, "shipping", nil)}
		if u := currentUser(c); u != nil {
			in.By = u.DisplayName
		}
		in.NextPayment = s.gxaParseTime(c.PostForm("next_payment"))
		in.End = s.gxaParseTime(c.PostForm("end"))
		in.TrialEnd = s.gxaParseTime(c.PostForm("trial_end"))
		in.Start = s.gxaParseTime(c.PostForm("start"))
		for i := 0; i < 5; i++ {
			idx := strconv.Itoa(i)
			pid := gooInt(c.PostForm("line_product[" + idx + "]"))
			if pid == 0 {
				continue
			}
			l := gsubNewLine{ProductID: pid, Qty: int(gooInt(c.PostForm("line_qty[" + idx + "]")))}
			if v := strings.TrimSpace(c.PostForm("line_total[" + idx + "]")); v != "" {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					l.Total = &f
				}
			}
			in.Lines = append(in.Lines, l)
		}
		sub, err := s.gsubManualCreate(ctx, in)
		if err != nil {
			gxaAdminRedirect(c, "subscriptions", "action=new", err.Error(), true)
			return
		}
		gxaAdminRedirect(c, "subscriptions", "action=edit&id="+itoa64(sub.ID), "Subscription created.", false)
		return
	}
	var b strings.Builder
	b.WriteString(`<form method="post" action="/wp-admin/goocommerce?page=subscriptions&amp;action=new" id="post"><input type="hidden" name="_wpnonce" value="` + s.nonce(c, "gsub-new") + `">`)
	b.WriteString(`<div id="poststuff"><div class="postbox"><h2 class="hndle"><span>Subscription details</span></h2><div class="inside">`)
	b.WriteString(`<p class="form-field"><label for="customer_id">Customer (user ID)</label> <input type="number" min="1" name="customer_id" id="customer_id" required></p>`)
	b.WriteString(`<p class="form-field"><label for="status">Status</label> ` + gxaSelect("status", "status", gsubStatuses, "active", "") + `</p>`)
	b.WriteString(`<p class="form-field"><label for="billing_interval">Billing interval</label> <input type="number" min="1" name="billing_interval" id="billing_interval" placeholder="from product"> ` +
		gxaSelect("billing_period", "billing_period", append([]gooOpt{{"", "from product"}}, gsubPeriods...), "", "") + `</p>`)
	b.WriteString(`<p class="form-field"><label for="next_payment">Next payment</label> <input type="datetime-local" name="next_payment" id="next_payment"> <label for="end">End</label> <input type="datetime-local" name="end" id="end"></p>`)
	var gws []gooOpt
	gws = append(gws, gooOpt{"", "Manual renewal"})
	for _, g := range s.gooGatewaysOrdered() {
		gws = append(gws, gooOpt{g.ID(), g.Title()})
	}
	b.WriteString(`<p class="form-field"><label for="payment_method">Payment method</label> ` + gxaSelect("payment_method", "payment_method", gws, "", "") + `</p>`)
	b.WriteString(`<table class="widefat"><thead><tr><th>Product ID</th><th>Quantity</th><th>Recurring total (blank = product price)</th></tr></thead><tbody>`)
	for i := 0; i < 5; i++ {
		idx := strconv.Itoa(i)
		b.WriteString(`<tr><td><input type="number" min="1" name="line_product[` + idx + `]"></td><td><input type="number" min="1" name="line_qty[` + idx + `]" value="1"></td><td><input type="text" name="line_total[` + idx + `]"></td></tr>`)
	}
	b.WriteString(`</tbody></table><div class="order_data_column_container"><div class="order_data_column"><h3>Billing</h3>` + gsubAdminAddress("billing", nil) +
		`</div><div class="order_data_column"><h3>Shipping</h3>` + gsubAdminAddress("shipping", nil) + `</div></div>`)
	b.WriteString(`<p><button type="submit" class="button button-primary" name="save" value="Create">Create subscription</button></p></div></div></div></form>`)
	head := `<h1 class="wp-heading-inline">Add Subscription</h1> <a href="/wp-admin/goocommerce?page=subscriptions" class="page-title-action">&larr; All subscriptions</a>`
	s.gxaAdminRender(c, "subscriptions", "Add Subscription", "goo-ext-subscriptions", head, b.String())
}

// gsubAdminAddress are the admin address inputs.
func gsubAdminAddress(prefix string, addr map[string]string) string {
	var b strings.Builder
	for _, f := range gsubAddrFields {
		if prefix == "shipping" && (f.Value == "email" || f.Value == "phone") {
			continue
		}
		id := prefix + "_" + f.Value
		b.WriteString(`<p class="form-field form-field-wide"><label for="` + id + `">` + f.Label + `:</label><input type="text" name="` + id + `" id="` + id + `" value="` + gooEsc(addr[f.Value]) + `"></p>`)
	}
	return b.String()
}

// gsubAdminProcessRenewal is the "Process renewal" action: a renewal order now, paid through the
// stored payment method or invoiced to the customer.
func (s *Server) gsubAdminProcessRenewal(ctx context.Context, sub *gooSubscription, by string) (string, bool) {
	if !containsStr([]string{"active", "on-hold"}, sub.Status) {
		return "Only an active or on-hold subscription can be renewed.", true
	}
	if gxaInt64(sub.Meta, "_pending_renewal") != 0 {
		return "The subscription already has an unpaid renewal order.", true
	}
	s.gsubAddNote(ctx, sub, "Renewal processed by "+defaultStr(by, "the store manager")+".", false, by)
	o, err := s.gsubRunRenewal(ctx, sub)
	if o == nil {
		return "The renewal order could not be created.", true
	}
	if err != nil {
		return "Renewal order #" + itoa64(o.ID) + " created, but its payment failed: " + err.Error(), true
	}
	return "Renewal order #" + itoa64(o.ID) + " created.", false
}
