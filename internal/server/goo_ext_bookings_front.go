package server

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Bookings on the storefront: the booking form on the product page, the slots / cost AJAX route,
// the "From:" price, My Account › Bookings and the customer's cancel link.

func init() {
	gooOnHTML("single_before_add_to_cart", bkBookingForm)
	gooFrontRoutes = append(gooFrontRoutes, bkFrontRoute)
	gooPriceHTMLHooks = append(gooPriceHTMLHooks, bkPriceHTML)
	gooAccountEndpoints = append(gooAccountEndpoints, &gooAccountEndpoint{Slug: "bookings", Title: "Bookings", Module: bkKey, After: "orders", Render: bkAccountBookings})
}

// bkPriceHTML is "From: $X" for bookable products whose cost varies.
func bkPriceHTML(s *Server, ctx context.Context, p *gooProduct, h template.HTML) template.HTML {
	if !s.gooExtOn(bkKey) || p.Type != bkType {
		return h
	}
	c := bkConfOf(p)
	price := s.gooPrice(bkFromCost(c))
	if c.DisplayCost > 0 || len(c.CostRules) > 0 || c.DurationType == "customer" || (c.HasPersons && (c.PersonCostMult || c.HasPersonTypes)) {
		return template.HTML(`<span class="from">From: </span>`) + price
	}
	return price
}

// bkBookingForm is the booking form inside the add-to-cart form (wc-bookings-booking-form).
func bkBookingForm(s *Server, c *gin.Context, p *gooProduct) template.HTML {
	if !s.gooExtOn(bkKey) || p.Type != bkType {
		return ""
	}
	conf := bkConfOf(p)
	from, to := s.bkWindow(conf)
	var b strings.Builder
	fmt.Fprintf(&b, `<div id="wc-bookings-booking-form" class="wc-bookings-booking-form" data-product-id="%d" data-unit="%s" data-ajax="%s" data-first-day="%s">`,
		p.ID, conf.Unit, html.EscapeString(s.links(c).HomeURL("/")), html.EscapeString(defaultStr(s.gooExtString(bkKey, "calendar_first_day"), "1")))
	dateType, minV, maxV, label := "date", from.Format("2006-01-02"), to.Format("2006-01-02"), "Date"
	if conf.Unit == "month" {
		dateType, minV, maxV, label = "month", from.Format("2006-01"), to.Format("2006-01"), "Month"
	}
	visible := ""
	if conf.CalendarDisplay == "always_visible" {
		visible = " always-visible"
	}
	fmt.Fprintf(&b, `<fieldset class="wc-bookings-date-picker wc_bookings_field_start_date%s"><legend><span class="label">%s</span>: <small class="wc-bookings-date-picker-choose-date">Choose&hellip;</small></legend>`, visible, label)
	if conf.Unit != "month" {
		b.WriteString(`<div class="picker wc-bookings-calendar" aria-hidden="true"></div>`)
		b.WriteString(`<style>.wc-bookings-calendar td.not-bookable span{opacity:.35;cursor:not-allowed}.wc-bookings-calendar td.fully_booked span{text-decoration:line-through}</style>`)
	}
	fmt.Fprintf(&b, `<div class="wc-bookings-date-picker-date-fields"><label><span class="screen-reader-text">%s</span><input type="%s" class="booking_date input-text" name="wc_bookings_field_start_date" min="%s" max="%s" required></label></div></fieldset>`,
		label, dateType, minV, maxV)
	if conf.timed() {
		b.WriteString(`<div class="form-field form-field-wide"><label for="wc_bookings_field_start_date">Time:</label><ul class="block-picker"><li>Choose a date above to see available times.</li></ul>` +
			`<input type="hidden" class="required_for_calculation" name="wc_bookings_field_start_date_time" id="wc_bookings_field_start_date_time"></div>`)
	}
	if conf.DurationType == "customer" {
		fmt.Fprintf(&b, `<p class="form-field form-field-wide wc_bookings_field_duration"><label for="wc_bookings_field_duration">Duration (%s):</label>`+
			`<input type="number" value="%d" step="1" min="%d" max="%d" name="wc_bookings_field_duration" id="wc_bookings_field_duration"> <span>&times; %s</span></p>`,
			conf.Unit+"s", conf.MinDuration, conf.MinDuration, conf.MaxDuration, bkUnitLabel(conf.Duration, conf.Unit))
	}
	if conf.ResourceAssign == "customer" {
		if list := s.gooBkProductResources(c.Request.Context(), conf); len(list) > 0 {
			fmt.Fprintf(&b, `<p class="form-field form-field-wide wc_bookings_field_resource"><label for="wc_bookings_field_resource">%s:</label><select name="wc_bookings_field_resource" id="wc_bookings_field_resource">`,
				html.EscapeString(conf.resourceLabel()))
			for _, r := range list {
				extra := ""
				if rb, rk := conf.resourceCosts(r.ID); rb != 0 || rk != 0 {
					extra = " (+" + gxStripTags(string(s.gooPrice(rb+rk))) + ")"
				}
				fmt.Fprintf(&b, `<option value="%d">%s%s</option>`, r.ID, html.EscapeString(r.Name), html.EscapeString(extra))
			}
			b.WriteString(`</select></p>`)
		}
	}
	if conf.HasPersons {
		for i, pt := range conf.PersonTypes {
			val := pt.Min
			if i == 0 && val < conf.MinPersons {
				val = conf.MinPersons
			}
			maxAttr := ""
			if pt.Max > 0 {
				maxAttr = fmt.Sprintf(` max="%d"`, pt.Max)
			} else if conf.MaxPersons > 0 {
				maxAttr = fmt.Sprintf(` max="%d"`, conf.MaxPersons)
			}
			fmt.Fprintf(&b, `<p class="form-field form-field-wide wc_bookings_field_persons_%s"><label for="wc_bookings_field_persons_%s">%s:</label>`+
				`<input type="number" value="%d" step="1" min="%d"%s name="wc_bookings_field_persons_%s" id="wc_bookings_field_persons_%s">`,
				html.EscapeString(pt.ID), html.EscapeString(pt.ID), html.EscapeString(pt.Name), val, pt.Min, maxAttr, html.EscapeString(pt.ID), html.EscapeString(pt.ID))
			if pt.Description != "" {
				b.WriteString(` <span class="person-type-description">` + html.EscapeString(pt.Description) + `</span>`)
			}
			b.WriteString(`</p>`)
		}
	}
	b.WriteString(`<div class="wc-bookings-booking-cost" style="display:none"></div></div>`)
	b.WriteString(bkFormJS)
	return template.HTML(b.String())
}

// bkFormJS draws the month calendar, loads the time slots and the cost (/?goo-ajax=booking_slots).
const bkFormJS = `<script>(function(){var f=document.currentScript.previousElementSibling;if(!f||f.dataset.bkInit)return;f.dataset.bkInit=1;
var form=f.closest('form')||f,input=f.querySelector('input[name=wc_bookings_field_start_date]'),cal=f.querySelector('.wc-bookings-calendar'),
slots=f.querySelector('.block-picker'),time=f.querySelector('input[name=wc_bookings_field_start_date_time]'),cost=f.querySelector('.wc-bookings-booking-cost'),
btn=form.querySelector('.single_add_to_cart_button'),fd=+f.dataset.firstDay,view=new Date(),names=['January','February','March','April','May','June','July','August','September','October','November','December'];
function pad(n){return(n<10?'0':'')+n}function iso(d){return d.getFullYear()+'-'+pad(d.getMonth()+1)+'-'+pad(d.getDate())}
if(input.min){var m=input.min.split('-');view=new Date(+m[0],+m[1]-1,1)}
function draw(){if(!cal)return;var y=view.getFullYear(),mo=view.getMonth(),first=new Date(y,mo,1),off=(first.getDay()-fd+7)%7,days=new Date(y,mo+1,0).getDate(),
h='<div class="ui-datepicker-header"><a href="#" class="ui-datepicker-prev">&lsaquo;</a> <span class="ui-datepicker-title">'+names[mo]+' '+y+'</span> <a href="#" class="ui-datepicker-next">&rsaquo;</a></div><table class="ui-datepicker-calendar"><tbody><tr>';
var key=y+'-'+pad(mo+1),full=busy[key]||{};if(!busy[key])month(key);
for(var i=0;i<off;i++)h+='<td></td>';for(var d=1;d<=days;d++){var v=iso(new Date(y,mo,d)),ok=(!input.min||v>=input.min)&&(!input.max||v<=input.max)&&!full[v];
h+='<td class="'+(ok?'bookable':'ui-datepicker-unselectable not-bookable'+(full[v]?' fully_booked':''))+(v===input.value?' ui-datepicker-current-day':'')+'">'+(ok?'<a href="#" data-date="'+v+'">'+d+'</a>':'<span>'+d+'</span>')+'</td>';if((off+d)%7===0)h+='</tr><tr>'}
cal.innerHTML=h+'</tr></tbody></table>'}
var busy={};function month(key){busy[key]={};var p=new URLSearchParams();p.set('goo-ajax','booking_month');p.set('product_id',f.dataset.productId);p.set('month',key);
var r=f.querySelector('select[name=wc_bookings_field_resource]');if(r)p.set(r.name,r.value);
fetch(f.dataset.ajax+'?'+p.toString(),{credentials:'same-origin'}).then(function(r){return r.json()}).then(function(r){var m={};(r.unavailable||[]).forEach(function(d){m[d]=1});busy[key]=m;draw()})}
if(cal)cal.addEventListener('click',function(e){var a=e.target.closest('a');if(!a)return;e.preventDefault();if(a.dataset.date){input.value=a.dataset.date;draw();load()}
else{view=new Date(view.getFullYear(),view.getMonth()+(a.classList.contains('ui-datepicker-next')?1:-1),1);draw()}});
function q(){var p=new URLSearchParams();p.set('goo-ajax','booking_slots');p.set('product_id',f.dataset.productId);p.set('date',input.value);
f.querySelectorAll('input[name^=wc_bookings_field_persons],input[name=wc_bookings_field_duration],select[name=wc_bookings_field_resource]').forEach(function(i){p.set(i.name,i.value)});if(time&&time.value)p.set('time',time.value);return p}
function load(){if(!input.value)return;fetch(f.dataset.ajax+'?'+q().toString(),{credentials:'same-origin'}).then(function(r){return r.json()}).then(function(r){
if(slots){if(!r.slots||!r.slots.length){slots.innerHTML='<li>No blocks available on this date.</li>'}else{slots.innerHTML=r.slots.map(function(s){return '<li class="block"><a href="#" data-value="'+s.time+'"'+(time.value===s.time?' class="selected"':'')+'>'+s.label+(r.remaining?' <small class="booking-spaces-left">('+s.available+' left)</small>':'')+'</a></li>'}).join('')}}
cost.style.display='block';cost.innerHTML=r.error?'<span class="booking-error">'+r.error+'</span>':(r.cost_html?'Booking cost: <strong>'+r.cost_html+'</strong>':'');if(btn)btn.disabled=!!r.error||(time&&!time.value)})}
if(slots)slots.addEventListener('click',function(e){var a=e.target.closest('a');if(!a)return;e.preventDefault();time.value=a.dataset.value;load()});
input.addEventListener('change',function(){if(time)time.value='';draw();load()});f.addEventListener('change',function(e){if(e.target.name==='wc_bookings_field_resource'){busy={};draw()}if(e.target!==input)load()});draw()})();</script>`

// bkFrontRoute serves /?goo-ajax=booking_slots and the customer's booking cancellation.
func bkFrontRoute(s *Server, c *gin.Context, path string) bool {
	if !s.gooExtOn(bkKey) {
		return false
	}
	switch {
	case c.Query("goo-ajax") == "booking_slots":
		s.bkSlotsJSON(c)
		return true
	case c.Query("goo-ajax") == "booking_month":
		s.gooBkMonthJSON(c)
		return true
	case c.Query("goo-ajax") == "booking_ics" || c.Query("goo-ajax") == "booking_feed":
		s.bkICSRoute(c)
		return true
	case c.Query("cancel_booking") == "true" && c.Query("booking_id") != "":
		s.bkCustomerCancel(c)
		return true
	}
	return false
}

// bkSlotsJSON answers the booking form: the date's free slots and the cost of the chosen booking.
func (s *Server) bkSlotsJSON(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := strconv.ParseInt(c.Query("product_id"), 10, 64)
	p, err := s.gooGetProduct(ctx, id)
	if err != nil || p.Type != bkType || p.Status != "publish" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Invalid booking product."})
		return
	}
	conf := bkConfOf(p)
	loc := s.gxLoc()
	date := c.Query("date")
	if conf.Unit == "month" && len(date) == 7 {
		date += "-01"
	}
	day, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"slots": []bkSlot{}, "error": "Date is required - please choose one above"})
		return
	}
	requested, _ := strconv.ParseInt(c.Query("wc_bookings_field_resource"), 10, 64)
	slotList := s.gooBkSlotsAll(ctx, p, conf, day, requested)
	out := gin.H{"slots": slotList, "remaining": s.gooExtBool(bkKey, "show_remaining")}
	if out["slots"] == nil {
		out["slots"] = []bkSlot{}
	}
	form := map[string][]string{"wc_bookings_field_start_date": {date}}
	for k, v := range c.Request.URL.Query() {
		if strings.HasPrefix(k, "wc_bookings_field_") {
			form[k] = v
		}
	}
	if t := c.Query("time"); t != "" {
		form["wc_bookings_field_start_date_time"] = []string{t}
	} else if len(slotList) > 0 {
		form["wc_bookings_field_start_date_time"] = []string{slotList[0].Time}
	}
	if !conf.timed() || c.Query("time") != "" || len(out["slots"].([]bkSlot)) > 0 {
		start, blocks, persons, err := s.bkParseRequest(conf, form)
		resID := requested
		if err == nil && (!conf.timed() || c.Query("time") != "") {
			resID, err = s.gooBkPickResource(ctx, p, conf, requested, start, blocks, bkSumPersons(persons), 0)
		}
		if err != nil {
			out["error"] = err.Error()
		} else {
			if resID != 0 {
				out["resource_id"] = resID
				out["resource"] = s.gooBkResourceName(ctx, resID)
			}
			v := gooRound(gooBkCostWith(conf, start, blocks, persons, resID), s.gooDecimals())
			out["cost"] = v
			out["cost_html"] = string(s.gooPrice(v))
		}
	}
	c.JSON(http.StatusOK, out)
}

// gooBkMonthJSON lists the days of a month that cannot be booked (fully booked, closed by the rules or
// outside the booking window), so the calendar greys them out like the "fully_booked" days of the
// original form: {"unavailable": ["2026-11-03", …]}.
func (s *Server) gooBkMonthJSON(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := strconv.ParseInt(c.Query("product_id"), 10, 64)
	p, err := s.gooGetProduct(ctx, id)
	if err != nil || p.Type != bkType || p.Status != "publish" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Invalid booking product."})
		return
	}
	loc := s.gxLoc()
	first, err := time.ParseInLocation("2006-01", c.Query("month"), loc)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "month must be YYYY-MM"})
		return
	}
	requested, _ := strconv.ParseInt(c.Query("wc_bookings_field_resource"), 10, 64)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"unavailable": s.gooBkUnavailableDays(ctx, p, bkConfOf(p), first, requested)})
}

// gooBkUnavailableDays are the days of first's month on which nothing can be booked.
func (s *Server) gooBkUnavailableDays(ctx context.Context, p *gooProduct, conf *bkConf, first time.Time, resource int64) []string {
	out := []string{}
	if conf.Unit == "month" {
		return out
	}
	for day := first; day.Month() == first.Month(); day = day.AddDate(0, 0, 1) {
		date := day.Format("2006-01-02")
		if conf.timed() {
			if len(s.gooBkSlotsAll(ctx, p, conf, day, resource)) == 0 {
				out = append(out, date)
			}
			continue
		}
		start, blocks, persons, err := s.bkParseRequest(conf, map[string][]string{"wc_bookings_field_start_date": {date}})
		if err == nil {
			_, err = s.gooBkPickResource(ctx, p, conf, resource, start, blocks, max(bkSumPersons(persons), 1), 0)
		}
		if err != nil {
			out = append(out, date)
		}
	}
	return out
}

// bkCanCancel reports whether the customer may still cancel a booking.
func (s *Server) bkCanCancel(ctx context.Context, b *bkBooking) bool {
	if !containsStr([]string{"unpaid", "pending-confirmation", "confirmed", "paid"}, b.Status) {
		return false
	}
	p, err := s.gooGetProduct(ctx, b.ProductID)
	if err != nil {
		return false
	}
	conf := bkConfOf(p)
	if !conf.CanCancel {
		return false
	}
	return time.Now().Before(bkAddUnits(b.Start, -conf.CancelLimit, conf.CancelUnit))
}

// bkCancelURL is the customer's cancel link.
func (s *Server) bkCancelURL(c *gin.Context, b *bkBooking) string {
	return s.gooAccountURL(c.Request.Context(), "bookings") + "?cancel_booking=true&booking_id=" + itoa64(b.ID) + "&_wpnonce=" + url.QueryEscape(s.nonce(c, "woocommerce-bookings-cancel-booking"))
}

// bkCustomerCancel cancels the customer's booking (My Account › Bookings › Cancel).
func (s *Server) bkCustomerCancel(c *gin.Context) {
	ctx := c.Request.Context()
	back := s.gooAccountURL(ctx, "bookings")
	u := currentUser(c)
	id, _ := strconv.ParseInt(c.Query("booking_id"), 10, 64)
	b, err := s.bkGet(ctx, id)
	switch {
	case u == nil || err != nil || b.CustomerID != u.ID:
		s.gooAddNotice(c, "error", "Invalid booking.")
	case !s.checkNonceValue(c, c.Query("_wpnonce"), "woocommerce-bookings-cancel-booking"):
		s.gooAddNotice(c, "error", "The link you followed has expired.")
	case !s.bkCanCancel(ctx, b):
		s.gooAddNotice(c, "error", "Your booking can no longer be cancelled.")
	default:
		_ = s.bkSetStatus(ctx, b, "cancelled")
		s.gooAddNotice(c, "success", "Your booking was cancelled.")
	}
	s.gooSaveSession(c)
	c.Redirect(http.StatusFound, back)
}

// bkAccountBookings is My Account › Bookings: upcoming and past bookings.
func bkAccountBookings(s *Server, c *gin.Context, arg string) template.HTML {
	if !s.gooExtOn(bkKey) {
		return ""
	}
	u := currentUser(c)
	if u == nil {
		return ""
	}
	ctx := c.Request.Context()
	all := s.bkQuery(ctx, `customer_id = $1 AND status <> 'in-cart' ORDER BY start_at`, u.ID)
	now := time.Now()
	var upcoming, past []*bkBooking
	for _, b := range all {
		if b.End.After(now) {
			upcoming = append(upcoming, b)
		} else {
			past = append(past, b)
		}
	}
	sort.SliceStable(past, func(i, j int) bool { return past[i].Start.After(past[j].Start) })
	var out strings.Builder
	table := func(title string, list []*bkBooking) {
		out.WriteString(`<h2>` + title + `</h2>`)
		if len(list) == 0 {
			out.WriteString(`<div class="woocommerce-info">No bookings found.</div>`)
			return
		}
		out.WriteString(`<table class="shop_table my_account_bookings"><thead><tr><th scope="col" class="booking-id">ID</th><th scope="col" class="booked-product">Booked</th>` +
			`<th scope="col" class="order-number">Order</th><th scope="col" class="booking-start-date">Start Date</th><th scope="col" class="booking-end-date">End Date</th>` +
			`<th scope="col" class="booking-status">Status</th><th scope="col" class="booking-cancel"></th></tr></thead><tbody>`)
		for _, b := range list {
			name := "&ndash;"
			if p, err := s.gooGetProduct(ctx, b.ProductID); err == nil {
				name = `<a href="` + html.EscapeString(s.gooPermalink(ctx, p)) + `">` + html.EscapeString(p.Name) + `</a>`
				if b.ResourceID != 0 {
					name += `<br><small class="booking-resource">` + html.EscapeString(bkConfOf(p).resourceLabel()+": "+s.gooBkResourceName(ctx, b.ResourceID)) + `</small>`
				}
			}
			order := "&ndash;"
			if b.OrderID != 0 {
				order = `<a href="` + html.EscapeString(s.gooEndpointURL(ctx, "myaccount", defaultStr(s.gooOptString("myaccount_view_order_endpoint"), "view-order"), itoa64(b.OrderID))) + `">` + itoa64(b.OrderID) + `</a>`
			}
			cancel := ""
			if s.bkCanCancel(ctx, b) {
				cancel = `<a href="` + html.EscapeString(s.bkCancelURL(c, b)) + `" class="button cancel">Cancel</a>`
			}
			if b.Status != "cancelled" {
				cancel += ` <a href="` + html.EscapeString(s.bkICSURL(ctx, b)) + `" class="button add-to-calendar">Add to calendar</a>`
			}
			fmt.Fprintf(&out, `<tr><td class="booking-id">%d</td><td class="booked-product">%s</td><td class="order-number">%s</td><td class="booking-start-date">%s</td>`+
				`<td class="booking-end-date">%s</td><td class="booking-status">%s</td><td class="booking-cancel">%s</td></tr>`,
				b.ID, name, order, html.EscapeString(s.bkWhen(b, b.Start)), html.EscapeString(s.bkWhen(b, b.End)), html.EscapeString(bkStatusLabel(b.Status)), cancel)
		}
		out.WriteString(`</tbody></table>`)
	}
	table("Upcoming Bookings", upcoming)
	table("Past Bookings", past)
	return template.HTML(out.String())
}
