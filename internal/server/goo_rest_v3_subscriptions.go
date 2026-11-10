package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// wc/v3/subscriptions (WC_REST_Subscriptions_Controller): list, create, read, update, delete and batch
// subscriptions, their notes and related orders, plus the subscription.created / updated / deleted
// webhook topics. Every route answers 404 rest_no_route while the Subscriptions module is off.

func (s *Server) registerGooV3Subscriptions() {
	mw := gooCapsAll("manage_woocommerce")
	on := func(h restHandler) restHandler {
		return func(c *gin.Context, req *restRequest) {
			if !s.gooExtOn(gsubKey) {
				gooNoRoute(c)
				return
			}
			h(c, req)
		}
	}
	s.gooV3("/subscriptions", mw, map[string]restHandler{http.MethodGet: on(s.gooV3SubscriptionsList), http.MethodPost: on(s.gooV3Create(s.gooV3SubscriptionCreate))})
	s.gooV3("/subscriptions/batch", mw, map[string]restHandler{http.MethodPost: on(func(c *gin.Context, req *restRequest) {
		s.gooBatch(c, req, gooBatchOps{Create: s.gooV3SubscriptionCreate, Update: s.gooV3SubscriptionUpdate, Delete: s.gooV3SubscriptionDelete})
	})})
	s.gooV3("/subscriptions/(?P<id>[\\d]+)", mw, map[string]restHandler{
		http.MethodGet: on(s.gooV3Get(func(c *gin.Context, id int64) (map[string]any, *gooAPIErr) {
			sub, err := s.gsubGet(c.Request.Context(), id)
			if err != nil {
				return nil, gooErr(404, "woocommerce_rest_invalid_id", "Invalid ID.")
			}
			return s.gooV3SubscriptionJSON(c.Request.Context(), sub), nil
		})),
		http.MethodPut:    on(s.gooV3Update(s.gooV3SubscriptionUpdate)),
		http.MethodDelete: on(s.gooV3Delete(s.gooV3SubscriptionDelete)),
	})
	s.gooV3("/subscriptions/(?P<id>[\\d]+)/orders", mw, map[string]restHandler{http.MethodGet: on(s.gooV3SubscriptionOrders)})
	s.gooV3("/subscriptions/(?P<id>[\\d]+)/notes", mw, map[string]restHandler{http.MethodGet: on(s.gooV3SubscriptionNotes), http.MethodPost: on(s.gooV3SubscriptionNoteCreate)})
}

// gsubWebhook delivers a subscription.* topic with the subscription's wc/v3 resource.
func (s *Server) gsubWebhook(ctx context.Context, topic string, sub *gooSubscription) {
	if !s.gooExtOn(gsubKey) || !s.gooWebhooksFor(ctx, topic) {
		return
	}
	s.gooWebhookFire(ctx, topic, sub.ID, s.gooV3SubscriptionJSON(ctx, sub))
}

// gsubWCSDate is the date format WooCommerce Subscriptions' API uses: "Y-m-d H:i:s" in GMT, "0" for none.
func gsubWCSDate(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "0"
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

func (s *Server) gooV3SubscriptionJSON(ctx context.Context, sub *gooSubscription) map[string]any {
	lines := []any{}
	for i, it := range sub.Items {
		lines = append(lines, map[string]any{"id": i + 1, "name": it.Name, "product_id": it.ProductID, "variation_id": it.VariationID,
			"quantity": it.Qty, "tax_class": it.TaxClass, "subtotal": gooMoney(it.Total, s.gooDecimals()), "subtotal_tax": gooMoney(it.Tax, s.gooDecimals()),
			"total": gooMoney(it.Total, s.gooDecimals()), "total_tax": gooMoney(it.Tax, s.gooDecimals()), "price": it.Price, "meta_data": []any{}})
	}
	ship := []any{}
	for i, sl := range gxaNoteList(sub.Meta["_shipping_lines"]) {
		ship = append(ship, map[string]any{"id": i + 1, "method_title": gxaStr(sl, "name"), "method_id": gxaStr(sl, "method_id"),
			"total": gooMoney(gxaFloat(sl, "total"), s.gooDecimals()), "total_tax": gooMoney(gxaFloat(sl, "tax"), s.gooDecimals())})
	}
	created, modified := sub.DateCreated, sub.DateModified
	start := sub.Start
	return map[string]any{"id": sub.ID, "parent_id": sub.ParentOrderID, "status": sub.Status, "order_key": "", "number": itoa64(sub.ID),
		"currency": sub.Currency, "version": "", "prices_include_tax": false,
		"date_created": s.gooDate(&created), "date_created_gmt": gooDateGMT(&created),
		"date_modified": s.gooDate(&modified), "date_modified_gmt": gooDateGMT(&modified),
		"customer_id": sub.CustomerID, "total": gooMoney(sub.RecurringTotal, s.gooDecimals()), "total_tax": gooMoney(sub.RecurringTax, s.gooDecimals()),
		"billing": sub.Billing, "shipping": sub.Shipping, "payment_method": sub.PaymentMethod, "payment_method_title": sub.PaymentMethodTitle,
		"requires_manual_renewal": s.gsubManualRenewal(sub), "customer_note": "",
		"line_items": lines, "shipping_lines": ship,
		"billing_period": sub.Period, "billing_interval": strconv.Itoa(sub.Interval),
		"start_date": gsubWCSDate(&start), "trial_end_date": gsubWCSDate(sub.TrialEnd), "next_payment_date": gsubWCSDate(sub.NextPayment),
		"end_date": gsubWCSDate(sub.End), "last_order_id": sub.LastOrderID,
		"_links": s.gooLinks(ctx, "subscriptions", sub.ID)}
}

func gooMoney(v float64, dec int) string { return strconv.FormatFloat(gooRound(v, dec), 'f', dec, 64) }

// gsubRecalc recomputes a subscription's recurring totals from its lines and shipping lines.
func (s *Server) gsubRecalc(sub *gooSubscription) {
	dec := s.gooDecimals()
	sub.RecurringTotal, sub.RecurringTax = 0, 0
	for _, it := range sub.Items {
		sub.RecurringTotal += it.Total + it.Tax
		sub.RecurringTax += it.Tax
	}
	for _, sl := range gxaNoteList(sub.Meta["_shipping_lines"]) {
		sub.RecurringTotal += gxaFloat(sl, "total") + gxaFloat(sl, "tax")
		sub.RecurringTax += gxaFloat(sl, "tax")
	}
	sub.RecurringTotal, sub.RecurringTax = gooRound(sub.RecurringTotal, dec), gooRound(sub.RecurringTax, dec)
}

// gsubNewLine is a line of a subscription made by hand.
type gsubNewLine struct {
	ProductID, VariationID int64
	Qty                    int
	Total                  *float64 // line total excluding tax; nil = the product's recurring price × qty
	Tax                    float64
}

// gsubNew is the input of gsubManualCreate (admin "Add Subscription" and POST /subscriptions).
type gsubNew struct {
	CustomerID         int64
	Status             string
	Period             string
	Interval           int
	Start              *time.Time
	TrialEnd           *time.Time
	NextPayment        *time.Time
	End                *time.Time
	Billing, Shipping  map[string]string
	PaymentMethod      string
	PaymentMethodTitle string
	Lines              []gsubNewLine
	By                 string
}

// gsubManualCreate makes a subscription without an order: the store manager sold it by phone or moved it
// from another system. Without dates the next payment is one period after the start.
func (s *Server) gsubManualCreate(ctx context.Context, in gsubNew) (*gooSubscription, error) {
	if in.CustomerID == 0 {
		return nil, errors.New("A customer is required.")
	}
	u, err := s.store.UserByID(ctx, in.CustomerID)
	if err != nil {
		return nil, errors.New("The customer does not exist.")
	}
	if len(in.Lines) == 0 {
		return nil, errors.New("A subscription needs at least one line item.")
	}
	sub := &gooSubscription{CustomerID: u.ID, Currency: s.gooCurrency(), Billing: map[string]string{}, Shipping: map[string]string{},
		PaymentMethod: in.PaymentMethod, PaymentMethodTitle: in.PaymentMethodTitle, Meta: map[string]any{}, Status: defaultStr(in.Status, "pending")}
	valid := false
	for _, st := range gsubStatuses {
		valid = valid || st.Value == sub.Status
	}
	if !valid {
		return nil, errors.New("Invalid subscription status.")
	}
	for k, v := range in.Billing {
		sub.Billing[k] = v
	}
	for k, v := range in.Shipping {
		sub.Shipping[k] = v
	}
	if sub.Billing["email"] == "" {
		sub.Billing["email"] = u.Email
	}
	if in.PaymentMethod != "" && in.PaymentMethodTitle == "" {
		if gw := s.gooGateway(in.PaymentMethod); gw != nil {
			sub.PaymentMethodTitle = gw.Title()
		}
	}
	for i, l := range in.Lines {
		p, err := s.gooGetProduct(ctx, l.ProductID)
		if err != nil {
			return nil, fmt.Errorf("Line %d: the product does not exist.", i+1)
		}
		pid, vid := l.ProductID, l.VariationID
		src := p
		if vid != 0 {
			if v, err := s.gooGetProduct(ctx, vid); err == nil {
				src = v
			}
		}
		if p.Type == gooTypeVariation {
			// A variation id given as the product: the schedule lives on the parent.
			vid, pid = p.ID, p.ParentID
			if parent, err := s.gooGetProduct(ctx, p.ParentID); err == nil {
				p = parent
			}
		}
		sched := gsubScheduleOf(p)
		if i == 0 {
			sub.Period, sub.Interval = sched.Period, sched.Interval
		}
		qty := max(l.Qty, 1)
		price := sched.Price
		if src != p && src.HasPrice() {
			price = src.PriceValue()
		}
		total := gooRound(price*float64(qty), s.gooDecimals())
		if l.Total != nil {
			total = gooRound(*l.Total, s.gooDecimals())
			price = total / float64(qty)
		}
		sub.Items = append(sub.Items, gsubItem{ProductID: pid, VariationID: vid, Name: src.Name, Qty: qty, Price: gooRound(price, s.gooDecimals()), Total: total, Tax: gooRound(l.Tax, s.gooDecimals())})
	}
	if in.Period != "" {
		if !containsStr([]string{"day", "week", "month", "year"}, in.Period) {
			return nil, errors.New("The billing period must be day, week, month or year.")
		}
		sub.Period = in.Period
	}
	if in.Interval > 0 {
		sub.Interval = in.Interval
	}
	now := time.Now()
	sub.Start = now
	if in.Start != nil {
		sub.Start = *in.Start
	}
	sub.TrialEnd, sub.End = in.TrialEnd, in.End
	sub.NextPayment = in.NextPayment
	if sub.NextPayment == nil && containsStr([]string{"active", "on-hold"}, sub.Status) {
		first := gxaAddPeriod(sub.Start, sub.Period, sub.Interval)
		if sub.TrialEnd != nil && sub.TrialEnd.After(first) {
			first = *sub.TrialEnd
		}
		sub.NextPayment = &first
	}
	if sub.End != nil && sub.NextPayment != nil && !sub.NextPayment.Before(*sub.End) {
		return nil, errors.New("The end date must be after the next payment date.")
	}
	s.gsubRecalc(sub)
	s.gsubAddNote(ctx, sub, "Subscription created manually. Status set to "+gsubStatusLabel(sub.Status)+".", false, in.By)
	if err := s.gsubSave(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

// REST handlers -----------------------------------------------------------------------------------------

func gsubAddrFrom(v any) map[string]string {
	out := map[string]string{}
	if m, ok := v.(map[string]any); ok {
		for k, x := range m {
			out[k] = strings.TrimSpace(gooStr(x))
		}
	}
	return out
}

func (s *Server) gsubRESTDate(b map[string]any, key string) (*time.Time, bool, *gooAPIErr) {
	v, ok := b[key]
	if !ok {
		return nil, false, nil
	}
	str := strings.TrimSpace(gooStr(v))
	if str == "" || str == "0" {
		return nil, true, nil
	}
	// WCS dates are GMT; an ISO date with a "T" and no zone is read as GMT too.
	for _, f := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(f, str, time.UTC); err == nil {
			return &t, true, nil
		}
	}
	return nil, true, gooErr(400, "rest_invalid_param", "Invalid parameter(s): "+key)
}

func (s *Server) gooV3SubscriptionCreate(c *gin.Context, b map[string]any) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	in := gsubNew{CustomerID: gooInt(b["customer_id"]), Status: gooStr(b["status"]), Period: gooStr(b["billing_period"]), Interval: int(gooInt(b["billing_interval"])),
		Billing: gsubAddrFrom(b["billing"]), Shipping: gsubAddrFrom(b["shipping"]), PaymentMethod: gooStr(b["payment_method"]),
		PaymentMethodTitle: gooStr(b["payment_method_title"])}
	if u := gooV3Actor(c); u != nil {
		in.By = u.DisplayName
	}
	for _, key := range []string{"start_date", "trial_end_date", "next_payment_date", "end_date"} {
		t, _, aerr := s.gsubRESTDate(b, key)
		if aerr != nil {
			return nil, aerr
		}
		switch key {
		case "start_date":
			in.Start = t
		case "trial_end_date":
			in.TrialEnd = t
		case "next_payment_date":
			in.NextPayment = t
		case "end_date":
			in.End = t
		}
	}
	lines, _ := b["line_items"].([]any)
	for _, x := range lines {
		m, _ := x.(map[string]any)
		l := gsubNewLine{ProductID: gooInt(m["product_id"]), VariationID: gooInt(m["variation_id"]), Qty: int(gooInt(m["quantity"]))}
		if f, ok := gooFloat(m["total"]); ok && m["total"] != nil && m["total"] != "" {
			l.Total = &f
		}
		l.Tax, _ = gooFloat(m["total_tax"])
		in.Lines = append(in.Lines, l)
	}
	sub, err := s.gsubManualCreate(ctx, in)
	if err != nil {
		return nil, gooErr(400, "woocommerce_rest_invalid_subscription", err.Error())
	}
	return s.gooV3SubscriptionJSON(ctx, sub), nil
}

func (s *Server) gooV3SubscriptionUpdate(c *gin.Context, id int64, b map[string]any) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	sub, err := s.gsubGet(ctx, id)
	if err != nil {
		return nil, gooErr(404, "woocommerce_rest_invalid_id", "Invalid ID.")
	}
	by := ""
	if u := gooV3Actor(c); u != nil {
		by = u.DisplayName
	}
	if v, ok := b["billing"]; ok {
		for k, x := range gsubAddrFrom(v) {
			sub.Billing[k] = x
		}
	}
	if v, ok := b["shipping"]; ok {
		for k, x := range gsubAddrFrom(v) {
			sub.Shipping[k] = x
		}
	}
	if v, ok := b["payment_method"]; ok {
		sub.PaymentMethod = gooStr(v)
		sub.PaymentMethodTitle = ""
		if gw := s.gooGateway(sub.PaymentMethod); gw != nil {
			sub.PaymentMethodTitle = gw.Title()
		}
		delete(sub.Meta, "_payment_token")
	}
	if v, ok := b["payment_method_title"]; ok {
		sub.PaymentMethodTitle = gooStr(v)
	}
	if v, ok := b["billing_period"]; ok {
		p := gooStr(v)
		if !containsStr([]string{"day", "week", "month", "year"}, p) {
			return nil, gooErr(400, "rest_invalid_param", "Invalid parameter(s): billing_period")
		}
		sub.Period = p
	}
	if v, ok := b["billing_interval"]; ok {
		if n := int(gooInt(v)); n >= 1 {
			sub.Interval = n
		} else {
			return nil, gooErr(400, "rest_invalid_param", "Invalid parameter(s): billing_interval")
		}
	}
	if v, ok := b["customer_id"]; ok && gooInt(v) != sub.CustomerID {
		if _, err := s.store.UserByID(ctx, gooInt(v)); err != nil {
			return nil, gooErr(400, "woocommerce_rest_invalid_customer", "The customer does not exist.")
		}
		sub.CustomerID = gooInt(v)
	}
	for _, key := range []string{"trial_end_date", "next_payment_date", "end_date"} {
		t, set, aerr := s.gsubRESTDate(b, key)
		if aerr != nil {
			return nil, aerr
		}
		if !set {
			continue
		}
		switch key {
		case "trial_end_date":
			sub.TrialEnd = t
		case "next_payment_date":
			sub.NextPayment = t
		case "end_date":
			sub.End = t
		}
	}
	if sub.End != nil && sub.NextPayment != nil && !sub.NextPayment.Before(*sub.End) {
		return nil, gooErr(400, "woocommerce_rest_invalid_subscription", "The end date must be after the next payment date.")
	}
	if lines, ok := b["line_items"].([]any); ok {
		for _, x := range lines {
			m, _ := x.(map[string]any)
			n := int(gooInt(m["id"]))
			if n < 1 || n > len(sub.Items) {
				return nil, gooErr(400, "woocommerce_rest_invalid_line_item", "Line item "+strconv.Itoa(n)+" does not exist.")
			}
			it := &sub.Items[n-1]
			if q, ok := m["quantity"]; ok && gooInt(q) >= 1 {
				it.Qty = int(gooInt(q))
			}
			if f, ok := gooFloat(m["total"]); ok && m["total"] != nil {
				it.Total = gooRound(f, s.gooDecimals())
			}
			if f, ok := gooFloat(m["total_tax"]); ok && m["total_tax"] != nil {
				it.Tax = gooRound(f, s.gooDecimals())
			}
			if it.Qty > 0 {
				it.Price = gooRound(it.Total/float64(it.Qty), s.gooDecimals())
			}
		}
		s.gsubRecalc(sub)
	}
	if v, ok := b["status"]; ok {
		to := gooStr(v)
		valid := false
		for _, st := range gsubStatuses {
			valid = valid || st.Value == to
		}
		if !valid {
			return nil, gooErr(400, "rest_invalid_param", "Invalid parameter(s): status")
		}
		if err := s.gsubUpdateStatus(ctx, sub, to, "Status changed by "+defaultStr(by, "the REST API")+".", by); err != nil {
			return nil, gooErr(500, "woocommerce_rest_cannot_edit", err.Error())
		}
	}
	if err := s.gsubSave(ctx, sub); err != nil {
		return nil, gooErr(500, "woocommerce_rest_cannot_edit", err.Error())
	}
	return s.gooV3SubscriptionJSON(ctx, sub), nil
}

func (s *Server) gooV3SubscriptionDelete(c *gin.Context, id int64, force bool) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	sub, err := s.gsubGet(ctx, id)
	if err != nil {
		return nil, gooErr(404, "woocommerce_rest_invalid_id", "Invalid ID.")
	}
	if !force {
		return nil, gooErr(http.StatusNotImplemented, "woocommerce_rest_trash_not_supported", "Subscriptions do not support trashing; delete them with force=true.")
	}
	obj := s.gooV3SubscriptionJSON(ctx, sub)
	if _, err := s.store.Pool.Exec(ctx, `DELETE FROM goo_subscriptions WHERE id = $1`, id); err != nil {
		return nil, gooErr(500, "woocommerce_rest_cannot_delete", err.Error())
	}
	s.gsubWebhook(ctx, "subscription.deleted", sub)
	return obj, nil
}

func (s *Server) gooV3SubscriptionsList(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	page, per, offset := gooPage(c)
	q := gsubQuery{Search: c.Query("search"), CustomerID: gooInt(c.Query("customer")), ProductID: gooInt(c.Query("product")), Page: page, PerPage: per}
	if st := gooQueryList(c, "status"); len(st) > 0 && !containsStr(st, "any") && !containsStr(st, "all") {
		for i := range st {
			st[i] = strings.TrimPrefix(st[i], "wc-")
			if gooOptLabel(gsubStatuses, st[i]) == st[i] {
				restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): status")
				return
			}
		}
		q.Status = st
	}
	if offset >= 0 {
		q.Page, q.PerPage = 1, offset+per
	}
	list, total, err := s.gsubList(ctx, q)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "woocommerce_rest_cannot_view", err.Error())
		return
	}
	if offset >= 0 {
		if offset < len(list) {
			list = list[offset:]
		} else {
			list = nil
		}
	}
	paginate(c, total, per, page)
	out := make([]any, 0, len(list))
	for _, sub := range list {
		out = append(out, s.gooV3SubscriptionJSON(ctx, sub))
	}
	restJSON(c, http.StatusOK, out)
}

func (s *Server) gooV3SubscriptionOrders(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	sub, err := s.gsubGet(ctx, req.ID())
	if err != nil {
		gooFail(c, gooErr(404, "woocommerce_rest_invalid_id", "Invalid ID."))
		return
	}
	out := []any{}
	for _, o := range s.gsubRelatedOrders(ctx, sub) {
		out = append(out, s.gooV3OrderJSON(ctx, o))
	}
	restJSON(c, http.StatusOK, out)
}

func (s *Server) gsubNoteJSON(ctx context.Context, sub *gooSubscription, n map[string]any, id int) map[string]any {
	var when *time.Time
	if t, err := time.Parse(time.RFC3339, gxaStr(n, "date")); err == nil {
		when = &t
	}
	author := defaultStr(gxaStr(n, "by"), "Pursae Commerce")
	col := s.gooRESTURL(ctx, gooV3NS+"/subscriptions/"+itoa64(sub.ID)+"/notes")
	return map[string]any{"id": id, "author": author, "date_created": s.gooDate(when), "date_created_gmt": gooDateGMT(when),
		"note": gxaStr(n, "content"), "customer_note": n["customer"] == true, "added_by_user": gxaStr(n, "by") != "",
		"_links": restLinks(col+"/"+strconv.Itoa(id), col, nil)}
}

func (s *Server) gooV3SubscriptionNotes(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	sub, err := s.gsubGet(ctx, req.ID())
	if err != nil {
		gooFail(c, gooErr(404, "woocommerce_rest_invalid_id", "Invalid ID."))
		return
	}
	notes := gxaNoteList(sub.Meta["_notes"]) // newest first; ids count from the oldest
	out := []any{}
	for i, n := range notes {
		out = append(out, s.gsubNoteJSON(ctx, sub, n, len(notes)-i))
	}
	paginate(c, len(out), len(out), 1)
	restJSON(c, http.StatusOK, out)
}

func (s *Server) gooV3SubscriptionNoteCreate(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	sub, err := s.gsubGet(ctx, req.ID())
	if err != nil {
		gooFail(c, gooErr(404, "woocommerce_rest_invalid_id", "Invalid ID."))
		return
	}
	b := gooFormBody(c, req)
	note := strings.TrimSpace(gooStr(b["note"]))
	if note == "" {
		gooFail(c, gooErr(400, "woocommerce_rest_invalid_note", "Note content is required."))
		return
	}
	by := ""
	if u := gooV3Actor(c); u != nil {
		by = u.DisplayName
	}
	s.gsubAddNote(ctx, sub, gooEsc(note), gooBool(b["customer_note"]), by)
	if err := s.gsubSave(ctx, sub); err != nil {
		gooFail(c, gooErr(500, "woocommerce_rest_cannot_create", err.Error()))
		return
	}
	notes := gxaNoteList(sub.Meta["_notes"])
	restJSON(c, http.StatusCreated, s.gsubNoteJSON(ctx, sub, notes[0], len(notes)))
}
