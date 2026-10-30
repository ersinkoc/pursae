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

	"github.com/gin-gonic/gin"
)

// Goocommerce › Subscriptions, continued (module "subscriptions"):
//   - variable subscriptions: the "variable-subscription" product type, whose variations carry their own
//     billing schedule (period, interval, length, sign-up fee, free trial, renewal day);
//   - switching: subscribers upgrade or downgrade between the variations of a variable subscription or
//     the subscriptions of a grouped product, through the cart; the switch order changes the
//     subscription's line item when it is paid (prorated recurring amount, sign-up fee and length);
//   - synchronised renewals: a product's renewal day of the week, month or year, with the first
//     payment prorated (or not) and the "days with no fee" grace.
// New names are prefixed gooSubs / gxs (other modules are written in parallel).

// gooSubsTypeVariable is the variable subscription product type.
const gooSubsTypeVariable = "variable-subscription"

// gooSubsVariableLike reports whether a product type has variations (variable products and variable
// subscriptions share the variable product's data box, cart and storefront code).
func gooSubsVariableLike(t string) bool { return t == gooTypeVariable || t == gooSubsTypeVariable }

// gxsVariationFieldHooks print module fields in a variation row of the product data box; gxsVariationSaveHooks
// save them (i is the row index of the posted variable_*[i] fields).
var (
	gxsVariationFieldHooks []func(s *Server, ctx context.Context, v *gooProduct, i int) template.HTML
	gxsVariationSaveHooks  []func(s *Server, c *gin.Context, parent, v *gooProduct, i string)
)

func init() {
	gooRegisterProductType(&gooProductTypeDef{Type: gooSubsTypeVariable, Label: "Variable subscription", Module: gsubKey,
		Purchasable: func(s *Server, ctx context.Context, p *gooProduct) bool {
			if !s.gooExtOn(gsubKey) || !p.HasPrice() {
				return false
			}
			if u := gxaUser(ctx); u != nil && !gooSubsSwitching(ctx) {
				if limit := defaultStr(gxaStr(p.Meta, "_subscription_limit"), "no"); limit != "no" && s.gsubHasSubscription(ctx, u.ID, p.ID, gooSubsLimitStatuses(limit)) {
					return false
				}
			}
			return true
		},
		AddToCartText: func(s *Server, p *gooProduct, single bool) string {
			if single {
				return defaultStr(s.gooExtString(gsubKey, "add_to_cart_button_text"), "Sign up now")
			}
			return "Select options"
		},
	})

	gxsVariationFieldHooks = append(gxsVariationFieldHooks, gooSubsVariationFields)
	gxsVariationSaveHooks = append(gxsVariationSaveHooks, gooSubsVariationSave)

	// A synchronised variation shows its first payment date under its price.
	gooPriceHTMLHooks = append(gooPriceHTMLHooks, func(s *Server, ctx context.Context, p *gooProduct, h template.HTML) template.HTML {
		if !s.gooExtOn(gsubKey) || p == nil || p.Type != gooTypeVariation || !s.gsubIsSubscription(ctx, p) {
			return h
		}
		return h + template.HTML(s.gooSubsFirstPaymentHTML(p))
	})

	// Product page: the first payment date of a synchronised subscription and the switch fields.
	gooOnHTML("single_before_add_to_cart", func(s *Server, c *gin.Context, p *gooProduct) template.HTML {
		if !s.gooExtOn(gsubKey) {
			return ""
		}
		out := ""
		if p.Type == "subscription" {
			out += s.gooSubsFirstPaymentHTML(p)
		}
		return template.HTML(out + s.gooSubsSwitchFields(c, p))
	})

	// Add to cart: a variable subscription's purchase limit, and the switch of a subscription.
	gooAddToCartHooks = append(gooAddToCartHooks, func(s *Server, ctx context.Context, p *gooProduct, qty int, form map[string][]string, data map[string]any) error {
		if !s.gooExtOn(gsubKey) {
			return nil
		}
		first := func(k string) string {
			if v := form[k]; len(v) > 0 {
				return v[0]
			}
			return ""
		}
		if first("switch-subscription") == "" {
			if p.Type == gooTypeVariation {
				if parent := s.gooParent(ctx, p); parent != nil && parent.Type == gooSubsTypeVariable {
					limit := defaultStr(gxaStr(parent.Meta, "_subscription_limit"), "no")
					if u := gxaUser(ctx); u != nil && limit != "no" && s.gsubHasSubscription(ctx, u.ID, parent.ID, gooSubsLimitStatuses(limit)) {
						return gooCartErr("You have an active subscription to this product already.")
					}
				}
			}
			return nil
		}
		return s.gooSubsAddSwitch(ctx, p, first("switch-subscription"), first("item"), first("_wcsnonce"), data)
	})

	// Checkout: a switch line records the subscription it changes and the dates the switch sets.
	gooCheckoutHooks = append(gooCheckoutHooks, func(s *Server, ctx context.Context, cart *gooCart, o *gooOrder) error {
		if !s.gooExtOn(gsubKey) {
			return nil
		}
		lines := o.ItemsOf("line_item")
		for i, it := range cart.Items {
			d := gooSubsSwitchData(it)
			if d == nil || i >= len(lines) {
				continue
			}
			subID := gxaInt64(d, "subscription_id")
			sub, err := s.gsubGet(ctx, subID)
			if err != nil || sub.CustomerID != o.CustomerID {
				return fmt.Errorf("There was an error locating the switch details.")
			}
			q, err := s.gooSubsSwitchQuote(ctx, it, s.gooDisplayPrice(ctx, it.Product()))
			if err != nil {
				return err
			}
			m := lines[i].Meta
			m["_subscription_switch"] = float64(subID)
			m["_switched_subscription_item_index"] = float64(gxaInt(d, "item"))
			if q.Next != nil {
				m["_subscription_switch_next_payment"] = float64(q.Next.Unix())
			}
			m["_subscription_switch_end"] = 0.0
			if q.End != nil {
				m["_subscription_switch_end"] = float64(q.End.Unix())
			}
			m["_subscription_switch_direction"] = map[bool]string{true: "upgrade", false: "downgrade"}[q.Upgrade]
			o.Meta["_subscription_switch"] = float64(subID)
		}
		return nil
	})
}

// gooSubsLimitStatuses are the subscription statuses a purchase limit counts.
func gooSubsLimitStatuses(limit string) []string {
	if limit == "any" {
		return append([]string{"cancelled", "expired"}, gsubLiveStatuses...)
	}
	return gsubLiveStatuses
}

// gooSubsSwitching reports whether the request switches a subscription (purchase limits do not apply).
func gooSubsSwitching(ctx context.Context) bool {
	gc := gxaGin(ctx)
	if gc == nil || gc.Request == nil {
		return false
	}
	return gc.Query("switch-subscription") != "" || (gc.Request.Method == "POST" && gc.PostForm("switch-subscription") != "")
}

// Variable subscriptions -------------------------------------------------------------------------------

// gooSubsVariablePriceHTML is a variable subscription's price: "From: $5.00 / month" when its variations
// differ, else the shared price and schedule.
func (s *Server) gooSubsVariablePriceHTML(ctx context.Context, p *gooProduct, h template.HTML) template.HTML {
	vars, _ := s.gooVariations(ctx, p.ID, false)
	var min *gooProduct
	same := true
	var firstStr string
	for _, v := range vars {
		if !v.HasPrice() {
			continue
		}
		v.parent = p
		str := fmt.Sprintf("%v|%s", v.PriceValue(), s.gsubPriceString(gsubScheduleOf(v)))
		if min == nil {
			firstStr = str
		} else if str != firstStr {
			same = false
		}
		if min == nil || v.PriceValue() < min.PriceValue() {
			min = v
		}
	}
	if min == nil {
		return h
	}
	details := `<span class="subscription-details">` + s.gsubPriceString(gsubScheduleOf(min)) + `</span>`
	if same {
		return h + template.HTML(details)
	}
	return template.HTML(`<span class="from">From: </span>` + string(s.gooPrice(s.gooDisplayPrice(ctx, min))) + details)
}

// gooSubsVariationFields are a variable subscription variation's schedule fields.
func gooSubsVariationFields(s *Server, ctx context.Context, v *gooProduct, i int) template.HTML {
	parent := s.gooParent(ctx, v)
	if !s.gooExtOn(gsubKey) || parent == nil || parent.Type != gooSubsTypeVariable {
		return ""
	}
	sc := gsubScheduleOf(v)
	idx := "[" + strconv.Itoa(i) + "]"
	var intervals, lengths []gooOpt
	for n := 1; n <= 6; n++ {
		label := "every"
		if n > 1 {
			label = "every " + gxaOrdinal(n)
		}
		intervals = append(intervals, gooOpt{strconv.Itoa(n), label})
	}
	lengths = append(lengths, gooOpt{"0", "Never expire"})
	for n := 1; n <= 24; n++ {
		lengths = append(lengths, gooOpt{strconv.Itoa(n), gxaPeriodLabel(sc.Period, n)})
	}
	fee := ""
	if sc.SignUpFee > 0 {
		fee = gooFormatDecimal(sc.SignUpFee, s.gooDecimals())
	}
	cur := gooCurrencySymbol(s.gooCurrency())
	var b strings.Builder
	b.WriteString(`<div class="variable_subscription_pricing variable_subscription_pricing_` + strconv.Itoa(i) + ` show_if_variable-subscription">`)
	b.WriteString(`<p class="form-row form-row-first form-field variable_subscription_period_interval_field"><label>Billing interval</label>` +
		gxaSelect("variable_subscription_period_interval"+idx, "", intervals, strconv.Itoa(sc.Interval), ` class="wc_input_subscription_period_interval"`) + `</p>`)
	b.WriteString(`<p class="form-row form-row-last form-field variable_subscription_period_field"><label>Billing period</label>` +
		gxaSelect("variable_subscription_period"+idx, "", gsubPeriods, sc.Period, ` class="wc_input_subscription_period"`) + `</p>`)
	b.WriteString(`<p class="form-row form-row-first form-field variable_subscription_length_field"><label>Expire after</label>` +
		gxaSelect("variable_subscription_length"+idx, "", lengths, strconv.Itoa(sc.Length), ` class="wc_input_subscription_length"`) + `</p>`)
	b.WriteString(`<p class="form-row form-row-last form-field variable_subscription_sign_up_fee_field"><label>Sign-up fee (` + cur + `)</label>` +
		`<input type="text" class="wc_input_price wc_input_subscription_intial_price" name="variable_subscription_sign_up_fee` + idx + `" value="` + gooEsc(fee) + `" placeholder="e.g. 9.90"></p>`)
	b.WriteString(`<p class="form-row form-row-first form-field variable_subscription_trial_length_field"><label>Free trial</label>` +
		`<input type="text" class="wc_input_subscription_trial_length" name="variable_subscription_trial_length` + idx + `" value="` + strconv.Itoa(sc.TrialLength) + `"></p>`)
	b.WriteString(`<p class="form-row form-row-last form-field variable_subscription_trial_period_field"><label>Subscription trial period</label>` +
		gxaSelect("variable_subscription_trial_period"+idx, "", []gooOpt{{"day", "days"}, {"week", "weeks"}, {"month", "months"}, {"year", "years"}}, sc.TrialPeriod, ` class="wc_input_subscription_trial_period"`) + `</p>`)
	b.WriteString(s.gooSubsSyncField(sc, "variable_subscription_payment_sync_date", idx, "form-row form-row-full"))
	b.WriteString(`<p class="form-row form-row-full description">The regular price above is the price of each billing period.</p></div>`)
	return template.HTML(b.String())
}

// gooSubsVariationSave saves a variable subscription variation's schedule (its _subscription_* meta).
func gooSubsVariationSave(s *Server, c *gin.Context, parent, v *gooProduct, i string) {
	if !s.gooExtOn(gsubKey) || parent == nil || parent.Type != gooSubsTypeVariable {
		return
	}
	get := func(k string) string { return strings.TrimSpace(c.PostForm(k + "[" + i + "]")) }
	if v.Meta == nil {
		v.Meta = map[string]any{}
	}
	num := func(k string, lo, hi int) float64 {
		n, _ := strconv.Atoi(get(k))
		return float64(min(max(n, lo), hi))
	}
	period := get("variable_subscription_period")
	if !containsStr([]string{"day", "week", "month", "year"}, period) {
		period = "month"
	}
	v.Meta["_subscription_period"] = period
	v.Meta["_subscription_period_interval"] = num("variable_subscription_period_interval", 1, 6)
	v.Meta["_subscription_length"] = num("variable_subscription_length", 0, 1000)
	fee := 0.0
	if f := s.gooParseAmount(get("variable_subscription_sign_up_fee")); f != nil && *f > 0 {
		fee = *f
	}
	v.Meta["_subscription_sign_up_fee"] = fee
	v.Meta["_subscription_trial_length"] = num("variable_subscription_trial_length", 0, 1000)
	tp := get("variable_subscription_trial_period")
	if !containsStr([]string{"day", "week", "month", "year"}, tp) {
		tp = "day"
	}
	v.Meta["_subscription_trial_period"] = tp
	gooSubsSaveSyncDay(v.Meta, period, get("variable_subscription_payment_sync_date"), get("variable_subscription_payment_sync_date_day"), get("variable_subscription_payment_sync_date_month"))
	if v.RegularPrice != nil {
		v.Meta["_subscription_price"] = *v.RegularPrice
	} else {
		delete(v.Meta, "_subscription_price")
	}
}

// Synchronised renewals --------------------------------------------------------------------------------

var gooSubsWeekdays = []string{"", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

// gooSubsSyncDayOf reads _subscription_payment_sync_date: a day number (week 1-7, month 1-28) or, for
// yearly products, {"day": d, "month": m} kept as MMDD.
func gooSubsSyncDayOf(m map[string]any) int {
	switch v := m["_subscription_payment_sync_date"].(type) {
	case map[string]any:
		mo, d := gxaInt(v, "month"), gxaInt(v, "day")
		if mo >= 1 && mo <= 12 && d >= 1 && d <= 31 {
			return mo*100 + d
		}
		return 0
	default:
		return max(gxaInt(m, "_subscription_payment_sync_date"), 0)
	}
}

// gooSubsSaveSyncDay stores a posted renewal day for the period (0 = not synchronised).
func gooSubsSaveSyncDay(meta map[string]any, period, day, yearDay, yearMonth string) {
	n, _ := strconv.Atoi(strings.TrimSpace(day))
	switch period {
	case "week":
		if n < 1 || n > 7 {
			n = 0
		}
		meta["_subscription_payment_sync_date"] = float64(n)
	case "month":
		if n < 1 || n > 28 {
			n = 0
		}
		meta["_subscription_payment_sync_date"] = float64(n)
	case "year":
		d, _ := strconv.Atoi(strings.TrimSpace(yearDay))
		mo, _ := strconv.Atoi(strings.TrimSpace(yearMonth))
		if mo < 1 || mo > 12 || d < 1 {
			meta["_subscription_payment_sync_date"] = map[string]any{"day": 0.0, "month": 0.0}
			return
		}
		d = min(d, gooSubsDaysIn(time.Month(mo), 2027)) // a non-leap year: Feb 29 is not a renewal day
		meta["_subscription_payment_sync_date"] = map[string]any{"day": float64(d), "month": float64(mo)}
	default:
		meta["_subscription_payment_sync_date"] = 0.0
	}
}

func gooSubsDaysIn(m time.Month, year int) int {
	return time.Date(year, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// gooSubsSyncLabel is the renewal day in a price string: " on Wednesday", " on the 1st of each month".
func gooSubsSyncLabel(period string, day int) string {
	switch period {
	case "week":
		if day >= 1 && day <= 7 {
			return " on " + gooSubsWeekdays[day]
		}
	case "month":
		if day >= 1 && day <= 28 {
			return " on the " + gxaOrdinal(day) + " of each month"
		}
	case "year":
		if mo, d := day/100, day%100; mo >= 1 && mo <= 12 && d >= 1 {
			return " on " + time.Month(mo).String() + " " + gxaOrdinal(d) + " each year"
		}
	}
	return ""
}

// gooSubsSyncField is the "Synchronise renewals" field of a schedule (only while the setting is on).
func (s *Server) gooSubsSyncField(sc gsubSchedule, name, idx, cls string) string {
	if !s.gooExtBool(gsubKey, "sync_payments") || sc.Period == "day" {
		return ""
	}
	label := `<label>Synchronise renewals</label>`
	desc := `<span class="description">Align the payment date for all customers who purchase this subscription to a specific day of the week, month or year. Save the product to change the period of this choice.</span>`
	switch sc.Period {
	case "week", "month":
		opts := []gooOpt{{"0", "Do not synchronise"}}
		if sc.Period == "week" {
			for d := 1; d <= 7; d++ {
				opts = append(opts, gooOpt{strconv.Itoa(d), gooSubsWeekdays[d]})
			}
		} else {
			for d := 1; d <= 28; d++ {
				opts = append(opts, gooOpt{strconv.Itoa(d), gxaOrdinal(d) + " day of the month"})
			}
		}
		return `<p class="` + cls + ` _subscription_payment_sync_date_field">` + label + gxaSelect(name+idx, "", opts, strconv.Itoa(sc.SyncDay), ` class="wc_input_subscription_payment_sync"`) + desc + `</p>`
	}
	months := []gooOpt{{"0", "Do not synchronise"}}
	for m := 1; m <= 12; m++ {
		months = append(months, gooOpt{strconv.Itoa(m), time.Month(m).String()})
	}
	day := ""
	if sc.SyncDay > 0 {
		day = strconv.Itoa(sc.SyncDay % 100)
	}
	return `<p class="` + cls + ` _subscription_payment_sync_date_field">` + label + gxaSelect(name+"_month"+idx, "", months, strconv.Itoa(sc.SyncDay/100), ` class="wc_input_subscription_payment_sync"`) +
		` <input type="number" min="1" max="31" name="` + gooEsc(name+"_day"+idx) + `" value="` + day + `" class="wc_input_subscription_payment_sync_day" placeholder="Day">` + desc + `</p>`
}

// gooSubsNextSyncDate is the first renewal day on or after from's date (keeping from's time of day).
func gooSubsNextSyncDate(from time.Time, period string, day int, loc *time.Location) time.Time {
	t := from.In(loc)
	clock := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, t.Hour(), t.Minute(), t.Second(), 0, loc)
	}
	switch period {
	case "week":
		iso := int(t.Weekday())
		if iso == 0 {
			iso = 7
		}
		return t.AddDate(0, 0, (day-iso+7)%7)
	case "month":
		c := clock(t.Year(), t.Month(), day)
		if c.Before(clock(t.Year(), t.Month(), t.Day())) {
			c = clock(t.Year(), t.Month()+1, day)
		}
		return c
	case "year":
		mo, d := time.Month(day/100), day%100
		c := clock(t.Year(), mo, min(d, gooSubsDaysIn(mo, t.Year())))
		if c.Before(clock(t.Year(), t.Month(), t.Day())) {
			c = clock(t.Year()+1, mo, min(d, gooSubsDaysIn(mo, t.Year()+1)))
		}
		return c
	}
	return t
}

func gooSubsSameDate(a, b time.Time, loc *time.Location) bool {
	a, b = a.In(loc), b.In(loc)
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

// gooSubsFirstRenewal is a new subscription's first renewal: the synchronised day after the start (or the
// trial end), else the trial end or one billing period. synced reports a moved first renewal.
func gooSubsFirstRenewal(now time.Time, period string, interval int, trialEnd *time.Time, syncDay int, loc *time.Location) (time.Time, bool) {
	base := now
	if trialEnd != nil {
		base = *trialEnd
	}
	if syncDay > 0 && period != "day" {
		if next := gooSubsNextSyncDate(base, period, syncDay, loc); !gooSubsSameDate(next, base, loc) {
			return next, true
		}
	}
	if trialEnd != nil {
		return *trialEnd, false
	}
	return gxaAddPeriod(now, period, max(interval, 1)), false
}

// gooSubsSyncDay is a schedule's renewal day while synchronisation is on.
func (s *Server) gooSubsSyncDay(sc gsubSchedule) int {
	if sc.Period == "day" || !s.gooExtBool(gsubKey, "sync_payments") {
		return 0
	}
	return sc.SyncDay
}

// gooSubsFirstPayment is the share of the recurring price charged at sign-up (1 = the whole first period,
// 0 during a free trial or before an unprorated synchronised renewal) and the first renewal date.
func (s *Server) gooSubsFirstPayment(p *gooProduct, sc gsubSchedule, now time.Time) (float64, time.Time) {
	var te *time.Time
	if sc.TrialLength > 0 {
		t := gxaAddPeriod(now, sc.TrialPeriod, sc.TrialLength)
		te = &t
	}
	first, synced := gooSubsFirstRenewal(now, sc.Period, sc.Interval, te, s.gooSubsSyncDay(sc), s.loc())
	if te != nil {
		return 0, first
	}
	if !synced {
		return 1, first
	}
	days := math.Ceil(first.Sub(now).Hours() / 24)
	if noFee := s.gooExtInt(gsubKey, "days_no_fee"); noFee > 0 && days <= float64(noFee) {
		return 0, first
	}
	prorate := func() float64 {
		cycle := first.Sub(gxaAddPeriod(first, sc.Period, -max(sc.Interval, 1))).Hours() / 24
		if cycle <= 0 {
			return 1
		}
		return math.Min(1, days/cycle)
	}
	switch s.gooExtString(gsubKey, "prorate_synced_payments") {
	case "recurring":
		return 1, first
	case "yes":
		return prorate(), first
	case "virtual":
		if p != nil && p.Virtual {
			return prorate(), first
		}
	}
	return 0, first
}

// gooSubsFirstPaymentHTML is "First payment: March 1, 2027" for a synchronised product.
func (s *Server) gooSubsFirstPaymentHTML(p *gooProduct) string {
	sc := gsubScheduleOf(p)
	if s.gooSubsSyncDay(sc) == 0 {
		return ""
	}
	_, first := s.gooSubsFirstPayment(p, sc, time.Now())
	return `<p class="first-payment-date"><small>First payment: ` + s.gxaDate(&first) + `</small></p>`
}

// Switching --------------------------------------------------------------------------------------------

// gooSubsSwitchData is a cart item's switch details ({"subscription_id", "item"}), nil for a purchase.
func gooSubsSwitchData(it *gooCartItem) map[string]any {
	if it == nil || it.Data == nil {
		return nil
	}
	d, _ := it.Data["subscription_switch"].(map[string]any)
	return d
}

func (s *Server) gooSubsSwitchAllowed(kind string) bool {
	v := s.gooExtString(gsubKey, "allow_switching")
	return v == kind || v == "variable_grouped"
}

// gooSubsGroupedParents are the grouped products a product belongs to.
func (s *Server) gooSubsGroupedParents(ctx context.Context, productID int64) []*gooProduct {
	rows, err := s.store.Pool.Query(ctx, `SELECT g.post_id FROM goo_products g JOIN posts p ON p.id = g.post_id
		WHERE g.type = 'grouped' AND p.status = 'publish' AND $1 = ANY(g.children) ORDER BY g.post_id`, productID)
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
	var out []*gooProduct
	for _, id := range ids {
		if p, err := s.gooGetProduct(ctx, id); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// gooSubsSwitchTarget is the product a subscription line may be switched within: its variable
// subscription ("variable") or a grouped product of subscriptions ("grouped"); nil when it can not switch.
func (s *Server) gooSubsSwitchTarget(ctx context.Context, sub *gooSubscription, idx int) (*gooProduct, string) {
	if !s.gooExtOn(gsubKey) || sub.Status != "active" || idx < 0 || idx >= len(sub.Items) {
		return nil, ""
	}
	item := sub.Items[idx]
	if item.VariationID != 0 && s.gooSubsSwitchAllowed("variable") {
		if parent, err := s.gooGetProduct(ctx, item.ProductID); err == nil && parent.Type == gooSubsTypeVariable && parent.Status == "publish" {
			if vars, _ := s.gooVariations(ctx, parent.ID, false); len(vars) > 1 {
				return parent, "variable"
			}
		}
	}
	if item.VariationID == 0 && s.gooSubsSwitchAllowed("grouped") {
		for _, gp := range s.gooSubsGroupedParents(ctx, item.ProductID) {
			kids, _ := s.gooGetProducts(ctx, gp.Children)
			n := 0
			for _, k := range kids {
				if k.Type == "subscription" && k.Status == "publish" {
					n++
				}
			}
			if n > 1 {
				return gp, "grouped"
			}
		}
	}
	return nil, ""
}

// gooSubsSwitchLink is the "Upgrade or Downgrade" button of a subscription line in My Account.
func (s *Server) gooSubsSwitchLink(c *gin.Context, sub *gooSubscription, idx int) string {
	ctx := c.Request.Context()
	target, _ := s.gooSubsSwitchTarget(ctx, sub, idx)
	if target == nil {
		return ""
	}
	q := url.Values{"switch-subscription": {itoa64(sub.ID)}, "item": {strconv.Itoa(idx)}, "_wcsnonce": {s.nonce(c, "wcs_switch_request")}}
	link := s.gooPermalink(ctx, target)
	sep := "?"
	if strings.Contains(link, "?") {
		sep = "&"
	}
	return ` <a href="` + gooEsc(link+sep+q.Encode()) + `" class="wcs-switch-link button">` +
		gooEsc(defaultStr(s.gooExtString(gsubKey, "switch_button_text"), "Upgrade or Downgrade")) + `</a>`
}

// gooSubsSwitchFields carry a switch request through the product page's add-to-cart form.
func (s *Server) gooSubsSwitchFields(c *gin.Context, p *gooProduct) string {
	idStr := c.Query("switch-subscription")
	u := currentUser(c)
	if idStr == "" || u == nil {
		return ""
	}
	id, _ := strconv.ParseInt(idStr, 10, 64)
	idx, err := strconv.Atoi(c.Query("item"))
	sub, gerr := s.gsubGet(c.Request.Context(), id)
	if err != nil || gerr != nil || sub.CustomerID != u.ID {
		return ""
	}
	if target, _ := s.gooSubsSwitchTarget(c.Request.Context(), sub, idx); target == nil || target.ID != p.ID {
		return ""
	}
	return `<div class="woocommerce-info wcs-switch-notice">Choose a new subscription.</div>` +
		`<input type="hidden" name="switch-subscription" value="` + itoa64(sub.ID) + `"><input type="hidden" name="item" value="` + strconv.Itoa(idx) + `">` +
		`<input type="hidden" name="_wcsnonce" value="` + gooEsc(c.Query("_wcsnonce")) + `">`
}

// gooSubsAddSwitch validates a switch added to the cart and records it in the item's data.
func (s *Server) gooSubsAddSwitch(ctx context.Context, p *gooProduct, subIDStr, itemStr, nonce string, data map[string]any) error {
	u, gc := gxaUser(ctx), gxaGin(ctx)
	if u == nil || gc == nil {
		return gooCartErr("You must be logged in to switch a subscription.")
	}
	fail := gooCartErr("There was an error locating the switch details.")
	if !s.checkNonceValue(gc, nonce, "wcs_switch_request") {
		return fail
	}
	subID, _ := strconv.ParseInt(subIDStr, 10, 64)
	idx, err := strconv.Atoi(itemStr)
	sub, gerr := s.gsubGet(ctx, subID)
	if err != nil || gerr != nil || sub.CustomerID != u.ID {
		return fail
	}
	target, kind := s.gooSubsSwitchTarget(ctx, sub, idx)
	if target == nil {
		return gooCartErr("You can not switch this subscription. It may be inactive, or switching is not allowed for it.")
	}
	item := sub.Items[idx]
	switch kind {
	case "variable":
		if p.Type != gooTypeVariation || p.ParentID != target.ID {
			return gooCartErr("You can only switch to a subscription product.")
		}
		if p.ID == item.VariationID {
			return gooCartErr("You can not switch to the same subscription.")
		}
	case "grouped":
		if p.Type != "subscription" || !gxaHasID(target.Children, p.ID) {
			return gooCartErr("You can only switch to a subscription product.")
		}
		if p.ID == item.ProductID {
			return gooCartErr("You can not switch to the same subscription.")
		}
	}
	data["subscription_switch"] = map[string]any{"subscription_id": float64(sub.ID), "item": float64(idx)}
	// One switch per subscription line: a new choice replaces an earlier one in the cart.
	if cart := gxaCart(ctx); cart != nil {
		for _, it := range append([]*gooCartItem(nil), cart.Items...) {
			if d := gooSubsSwitchData(it); d != nil && gxaInt64(d, "subscription_id") == sub.ID && gxaInt(d, "item") == idx {
				cart.Remove(it.Key)
			}
		}
	}
	return nil
}

// gooSubsQuote is what a switch costs now and the dates it gives the subscription.
type gooSubsQuote struct {
	Charge  float64 // per unit, due now
	Next    *time.Time
	End     *time.Time
	Upgrade bool
}

// gooSubsSwitchQuote prices a switch cart item (unit = the new recurring unit price) like
// WCS_Switch_Totals_Calculator: prorated upgrades pay the difference per day for the days left in the
// paid period; prorated downgrades extend the next payment by the credit; unprorated switches pay
// nothing now and the new price from the next renewal. The sign-up fee and length follow their settings.
func (s *Server) gooSubsSwitchQuote(ctx context.Context, it *gooCartItem, unit float64) (gooSubsQuote, error) {
	var q gooSubsQuote
	d := gooSubsSwitchData(it)
	if d == nil {
		return q, fmt.Errorf("not a switch")
	}
	sub, err := s.gsubGet(ctx, gxaInt64(d, "subscription_id"))
	idx := gxaInt(d, "item")
	if err != nil || idx < 0 || idx >= len(sub.Items) {
		return q, fmt.Errorf("There was an error locating the switch details.")
	}
	old := sub.Items[idx]
	p := it.Product()
	sc := gsubScheduleOf(p)
	now := time.Now()
	qty := float64(max(it.Quantity, 1))
	dayLen := func(a, b time.Time) float64 { return math.Max(b.Sub(a).Hours()/24, 1) }
	var oldCycle float64
	if sub.NextPayment != nil {
		oldCycle = dayLen(gxaAddPeriod(*sub.NextPayment, sub.Period, -sub.Interval), *sub.NextPayment)
	} else {
		oldCycle = dayLen(now, gxaAddPeriod(now, sub.Period, sub.Interval))
	}
	newCycle := dayLen(now, gxaAddPeriod(now, sc.Period, sc.Interval))
	oldPerDay := old.Total / oldCycle
	newPerDay := unit * qty / newCycle
	daysLeft := 0.0
	if sub.NextPayment != nil && sub.NextPayment.After(now) {
		daysLeft = math.Ceil(sub.NextPayment.Sub(now).Hours() / 24)
	}
	q.Upgrade = newPerDay > oldPerDay
	if sub.NextPayment != nil {
		n := *sub.NextPayment
		q.Next = &n
	}
	mode := s.gooExtString(gsubKey, "apportion_recurring_price")
	prorate := mode == "yes" || (mode == "virtual" && p.Virtual) || (q.Upgrade && (mode == "yes-upgrade" || (mode == "virtual-upgrade" && p.Virtual)))
	charge := 0.0
	if prorate && daysLeft > 0 {
		if q.Upgrade {
			charge = (newPerDay - oldPerDay) * daysLeft
		} else if newPerDay > 0 && q.Next != nil {
			if extra := int(math.Floor((oldPerDay - newPerDay) * daysLeft / newPerDay)); extra > 0 {
				n := q.Next.AddDate(0, 0, extra)
				q.Next = &n
			}
		}
	}
	switch s.gooExtString(gsubKey, "apportion_sign_up_fee") {
	case "full":
		charge += sc.SignUpFee * qty
	case "yes":
		charge += math.Max(0, sc.SignUpFee*qty-s.gooSubsPaidSignUpFee(ctx, sub, old))
	}
	if sc.Length > 0 {
		lmode := s.gooExtString(gsubKey, "apportion_length")
		base := now
		if q.Next != nil {
			base = *q.Next
		}
		var e time.Time
		if lmode == "yes" || (lmode == "virtual" && p.Virtual) {
			// Payments already made count toward the new length.
			start := sub.Start
			if sub.TrialEnd != nil {
				start = *sub.TrialEnd
			}
			e = gxaAddPeriod(start, sc.Period, sc.Length)
			if e.Before(base) {
				e = base
			}
		} else {
			e = gxaAddPeriod(now, sc.Period, sc.Length)
		}
		q.End = &e
	}
	q.Charge = gooRound(charge/qty, s.gooDecimals())
	return q, nil
}

// gooSubsPaidSignUpFee is the sign-up fee paid for a subscription line on its parent order.
func (s *Server) gooSubsPaidSignUpFee(ctx context.Context, sub *gooSubscription, item gsubItem) float64 {
	o, err := s.gooGetOrder(ctx, sub.ParentOrderID)
	if err != nil {
		return 0
	}
	if len(o.Items) == 0 {
		o.Items, _ = s.gooOrderItems(ctx, o.ID)
	}
	for _, line := range o.ItemsOf("line_item") {
		if line.ProductID == item.ProductID && line.VariationID == item.VariationID {
			return gxaFloat(line.Meta, "_subscription_sign_up_fee") * float64(line.Qty())
		}
	}
	return 0
}

// gooSubsApplySwitch changes the subscriptions of a paid switch order (once per order): the line
// item, billing schedule, recurring totals and the dates the switch set.
func (s *Server) gooSubsApplySwitch(ctx context.Context, o *gooOrder) {
	if o.Meta["_subscription_switch"] == nil {
		return
	}
	if len(o.Items) == 0 {
		o.Items, _ = s.gooOrderItems(ctx, o.ID)
	}
	for _, line := range o.ItemsOf("line_item") {
		subID := gxaInt64(line.Meta, "_subscription_switch")
		if subID == 0 {
			continue
		}
		sub, err := s.gsubGet(ctx, subID)
		if err != nil || sub.CustomerID != o.CustomerID {
			continue
		}
		var done []any
		if list, ok := sub.Meta["_switch_orders"].([]any); ok {
			done = list
		}
		applied := false
		for _, v := range done {
			if f, ok := v.(float64); ok && int64(f) == o.ID {
				applied = true
			}
		}
		idx := gxaInt(line.Meta, "_switched_subscription_item_index")
		if applied || idx < 0 || idx >= len(sub.Items) {
			continue
		}
		old := sub.Items[idx]
		sub.Items[idx] = gsubItem{ProductID: line.ProductID, VariationID: line.VariationID, Name: line.Name, Qty: line.Qty(),
			Price: gxaFloat(line.Meta, "_subscription_recurring_amount"), Total: gxaFloat(line.Meta, "_subscription_recurring_line_total"),
			Tax: gxaFloat(line.Meta, "_subscription_recurring_line_tax"), TaxClass: line.TaxClass}
		if p := gxaStr(line.Meta, "_subscription_period"); p != "" {
			sub.Period = p
		}
		sub.Interval = max(gxaInt(line.Meta, "_subscription_interval"), 1)
		sub.SyncDay = gxaInt(line.Meta, "_subscription_sync_day")
		if np := gxaInt64(line.Meta, "_subscription_switch_next_payment"); np > 0 {
			t := time.Unix(np, 0)
			sub.NextPayment = &t
		}
		sub.End = nil
		if e := gxaInt64(line.Meta, "_subscription_switch_end"); e > 0 {
			t := time.Unix(e, 0)
			sub.End = &t
		}
		if sub.End != nil && sub.NextPayment != nil && !sub.NextPayment.Before(*sub.End) {
			sub.NextPayment = nil
		}
		sub.RecurringTotal, sub.RecurringTax = 0, 0
		for _, it := range sub.Items {
			sub.RecurringTotal += it.Total + it.Tax
			sub.RecurringTax += it.Tax
		}
		for _, sl := range gxaNoteList(sub.Meta["_shipping_lines"]) {
			sub.RecurringTotal += gxaFloat(sl, "total") + gxaFloat(sl, "tax")
			sub.RecurringTax += gxaFloat(sl, "tax")
		}
		sub.RecurringTotal = gooRound(sub.RecurringTotal, s.gooDecimals())
		sub.RecurringTax = gooRound(sub.RecurringTax, s.gooDecimals())
		sub.Meta["_switch_orders"] = append(done, float64(o.ID))
		s.gsubAddNote(ctx, sub, fmt.Sprintf("Customer switched from: %s to %s (switch order #%d).", gooEsc(old.Name), gooEsc(line.Name), o.ID), false, "")
		if err := s.gsubSave(ctx, sub); err != nil {
			continue
		}
		_ = s.gooAddOrderNote(ctx, o, fmt.Sprintf("Switched subscription #%d from %s to %s.", sub.ID, old.Name, line.Name), false, "")
		s.gooFire(ctx, "subscription_switched", sub)
	}
}
