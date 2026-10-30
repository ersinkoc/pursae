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
)

// Bookings in the admin: the product data panels (Booking, Availability, Costs, Persons) and
// Goocommerce › Bookings (All Bookings, Edit booking, Create booking, Calendar).

func init() {
	gooProductPanels = append(gooProductPanels,
		&gooProductPanel{ID: "bookings_general", Label: "Booking", Module: bkKey, ShowFor: []string{bkType}, Render: bkPanelGeneral, Save: bkSaveGeneral},
		&gooProductPanel{ID: "bookings_availability", Label: "Availability", Module: bkKey, ShowFor: []string{bkType}, Render: bkPanelAvailability, Save: bkSaveAvailability},
		&gooProductPanel{ID: "bookings_pricing", Label: "Costs", Module: bkKey, ShowFor: []string{bkType}, Render: bkPanelCosts, Save: bkSaveCosts},
		&gooProductPanel{ID: "bookings_persons", Label: "Persons", Module: bkKey, ShowFor: []string{bkType}, Render: bkPanelPersons, Save: bkSavePersons},
	)
	gooExtAdminPages["bookings"] = func(s *Server, c *gin.Context) { s.bkAdmin(c) }
}

// Panels ------------------------------------------------------------------------------------------------

func bkField(label, id, input, tip string) string {
	out := `<p class="form-field ` + id + `_field"><label for="` + id + `">` + label + `</label>` + input
	if tip != "" {
		out += `<span class="description">` + tip + `</span>`
	}
	return out + `</p>`
}

func bkInput(id, typ, val, attrs string) string {
	return `<input type="` + typ + `" class="short" name="` + id + `" id="` + id + `" value="` + html.EscapeString(val) + `"` + attrs + `>`
}

func bkSelect(id, val string, opts []gooOpt) string {
	out := `<select name="` + id + `" id="` + id + `" class="select short">`
	for _, o := range opts {
		out += `<option value="` + html.EscapeString(o.Value) + `"` + gxSel(o.Value, val) + `>` + o.Label + `</option>`
	}
	return out + `</select>`
}

func bkCheckbox(id string, on bool) string {
	return `<input type="checkbox" class="checkbox" name="` + id + `" id="` + id + `" value="yes"` + gxChk(on) + `>`
}

var bkUnits = []gooOpt{{"month", "Month(s)"}, {"day", "Day(s)"}, {"hour", "Hour(s)"}, {"minute", "Minute(s)"}}
var bkLimitUnits = []gooOpt{{"month", "Month(s)"}, {"week", "Week(s)"}, {"day", "Day(s)"}, {"hour", "Hour(s)"}, {"minute", "Minute(s)"}}

func bkPanelGeneral(s *Server, c *gin.Context, p *gooProduct) template.HTML {
	k := bkConfOf(p)
	var b strings.Builder
	b.WriteString(`<div class="options_group">`)
	b.WriteString(bkField("Booking duration", "_wc_booking_duration_type",
		bkSelect("_wc_booking_duration_type", k.DurationType, []gooOpt{{"fixed", "Fixed blocks of"}, {"customer", "Customer defined blocks of"}})+
			bkInput("_wc_booking_duration", "number", strconv.Itoa(k.Duration), ` min="1" step="1" style="width:60px"`)+
			bkSelect("_wc_booking_duration_unit", k.Unit, bkUnits), ""))
	b.WriteString(bkField("Minimum duration", "_wc_booking_min_duration", bkInput("_wc_booking_min_duration", "number", strconv.Itoa(k.MinDuration), ` min="1" step="1"`), "The minimum allowed duration the user can input."))
	b.WriteString(bkField("Maximum duration", "_wc_booking_max_duration", bkInput("_wc_booking_max_duration", "number", strconv.Itoa(k.MaxDuration), ` min="1" step="1"`), "The maximum allowed duration the user can input."))
	b.WriteString(bkField("Calendar display mode", "_wc_booking_calendar_display_mode",
		bkSelect("_wc_booking_calendar_display_mode", k.CalendarDisplay, []gooOpt{{"", "Display calendar on click"}, {"always_visible", "Calendar always visible"}}), ""))
	b.WriteString(bkField("Requires confirmation?", "_wc_booking_requires_confirmation", bkCheckbox("_wc_booking_requires_confirmation", k.RequiresConfirm), "Check this box if the booking requires admin approval/confirmation. Payment will not be taken during checkout."))
	b.WriteString(bkField("Can be cancelled?", "_wc_booking_user_can_cancel", bkCheckbox("_wc_booking_user_can_cancel", k.CanCancel), "Check this box if the booking can be cancelled by the customer after it has been purchased. A refund will not be sent automatically."))
	b.WriteString(bkField("Booking can be cancelled until", "_wc_booking_cancel_limit",
		bkInput("_wc_booking_cancel_limit", "number", strconv.Itoa(k.CancelLimit), ` min="1" step="1" style="width:60px"`)+bkSelect("_wc_booking_cancel_limit_unit", k.CancelUnit, bkLimitUnits)+" before the start date.", ""))
	b.WriteString(`</div><div class="options_group">`)
	b.WriteString(bkField("Max bookings per block", "_wc_booking_qty", bkInput("_wc_booking_qty", "number", strconv.Itoa(k.MaxPerBlock), ` min="1" step="1"`), "The maximum bookings allowed per block (or per person when persons count as bookings)."))
	b.WriteString(bkField("Minimum block bookable", "_wc_booking_min_date",
		bkInput("_wc_booking_min_date", "number", strconv.Itoa(k.MinDate), ` min="0" step="1" style="width:60px"`)+bkSelect("_wc_booking_min_date_unit", k.MinDateUnit, bkLimitUnits)+" into the future", ""))
	b.WriteString(bkField("Maximum block bookable", "_wc_booking_max_date",
		bkInput("_wc_booking_max_date", "number", strconv.Itoa(k.MaxDate), ` min="1" step="1" style="width:60px"`)+bkSelect("_wc_booking_max_date_unit", k.MaxDateUnit, bkLimitUnits)+" into the future", ""))
	b.WriteString(bkField("Require a buffer period of", "_wc_booking_buffer_period", bkInput("_wc_booking_buffer_period", "number", strconv.Itoa(k.Buffer), ` min="0" step="1"`), "blocks (in the duration unit) between bookings."))
	b.WriteString(bkField("First block starts at", "_wc_booking_first_block_time", bkInput("_wc_booking_first_block_time", "time", k.FirstBlockTime, ""), "For hour and minute blocks: the time of the first block of a day."))
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

func bkSetMeta(p *gooProduct, k string, v any) { p.Meta["_wc_booking_"+k] = v }

func bkSaveGeneral(s *Server, c *gin.Context, p *gooProduct) {
	if p.Meta == nil {
		p.Meta = map[string]any{}
	}
	for _, k := range []string{"duration_type", "duration_unit", "calendar_display_mode", "cancel_limit_unit", "min_date_unit", "max_date_unit", "first_block_time"} {
		bkSetMeta(p, k, c.PostForm("_wc_booking_"+k))
	}
	for _, k := range []string{"duration", "min_duration", "max_duration", "cancel_limit", "qty", "min_date", "max_date", "buffer_period"} {
		bkSetMeta(p, k, float64(gxAtoi(c.PostForm("_wc_booking_"+k))))
	}
	for _, k := range []string{"requires_confirmation", "user_can_cancel"} {
		bkSetMeta(p, k, c.PostForm("_wc_booking_"+k) == "yes")
	}
	// Bookable products are virtual unless the editor posted the Virtual box; one booking per cart line.
	if _, posted := c.GetPostForm("_virtual"); !posted {
		p.Virtual = true
	}
	p.SoldIndividually = true
	bkSyncPrice(p)
}

// bkSyncPrice stores the "From:" cost as the product's price (catalog sorting and filters).
func bkSyncPrice(p *gooProduct) {
	if p.Type != bkType {
		return
	}
	v := bkFromCost(bkConfOf(p))
	p.RegularPrice, p.SalePrice = &v, nil
}

var bkRuleTypes = []gooOpt{{"custom", "Date range"}, {"months", "Range of months"}, {"weeks", "Range of weeks"}, {"days", "Range of days"},
	{"time", "Time Range (all week)"}, {"time:1", "Monday"}, {"time:2", "Tuesday"}, {"time:3", "Wednesday"}, {"time:4", "Thursday"},
	{"time:5", "Friday"}, {"time:6", "Saturday"}, {"time:7", "Sunday"}}

func bkRuleRow(r bkRule) string {
	return `<tr><td>` + bkSelectName("wc_booking_availability_type[]", r.Type, bkRuleTypes) + `</td>` +
		`<td><input type="text" name="wc_booking_availability_from[]" value="` + html.EscapeString(r.From) + `" placeholder="From" size="10"></td>` +
		`<td><input type="text" name="wc_booking_availability_to[]" value="` + html.EscapeString(r.To) + `" placeholder="To" size="10"></td>` +
		`<td>` + bkSelectName("wc_booking_availability_bookable[]", map[bool]string{true: "yes", false: "no"}[r.Bookable], []gooOpt{{"yes", "Yes"}, {"no", "No"}}) + `</td>` +
		`<td><input type="number" name="wc_booking_availability_priority[]" value="` + strconv.Itoa(max(r.Priority, 1)) + `" min="1" style="width:60px"></td>` +
		`<td class="remove"><a href="#" class="button" onclick="this.closest('tr').remove();return false">&times;</a></td></tr>`
}

func bkSelectName(name, val string, opts []gooOpt) string {
	out := `<select name="` + name + `">`
	for _, o := range opts {
		out += `<option value="` + html.EscapeString(o.Value) + `"` + gxSel(o.Value, val) + `>` + o.Label + `</option>`
	}
	return out + `</select>`
}

// bkAddRowJS appends a copy of the template row (the <template> before the button) to the table.
const bkAddRowJS = `onclick="var t=this.parentNode.querySelector('template');this.closest('div').querySelector('tbody').insertAdjacentHTML('beforeend',t.innerHTML);return false"`

func bkPanelAvailability(s *Server, c *gin.Context, p *gooProduct) template.HTML {
	k := bkConfOf(p)
	var b strings.Builder
	b.WriteString(`<div class="options_group">`)
	b.WriteString(bkField("All dates are&hellip;", "_wc_booking_default_date_availability",
		bkSelect("_wc_booking_default_date_availability", map[bool]string{true: "available", false: "non-available"}[k.DefaultAvailable],
			[]gooOpt{{"available", "available by default"}, {"non-available", "not-available by default"}}), "This option affects how you use the rules below."))
	b.WriteString(`</div><div class="options_group wc_booking_availability"><p>Set availability rules. Rules with a lower priority number override rules with a higher one; within a priority, rules lower in the table win. Dates are YYYY-MM-DD, months 1–12, weeks 1–53, days 1 (Monday)–7 (Sunday), times HH:MM.</p>`)
	b.WriteString(`<table class="widefat"><thead><tr><th>Range type</th><th>From</th><th>To</th><th>Bookable</th><th>Priority</th><th></th></tr></thead><tbody>`)
	for _, r := range k.Rules {
		b.WriteString(bkRuleRow(r))
	}
	b.WriteString(`</tbody></table><p><template>` + bkRuleRow(bkRule{Type: "custom", Bookable: true, Priority: 10}) + `</template><a href="#" class="button add_row" ` + bkAddRowJS + `>Add Range</a></p></div>`)
	return template.HTML(b.String())
}

func bkSaveAvailability(s *Server, c *gin.Context, p *gooProduct) {
	if p.Meta == nil {
		p.Meta = map[string]any{}
	}
	bkSetMeta(p, "default_date_availability", defaultStr(c.PostForm("_wc_booking_default_date_availability"), "available"))
	types, froms, tos := c.PostFormArray("wc_booking_availability_type[]"), c.PostFormArray("wc_booking_availability_from[]"), c.PostFormArray("wc_booking_availability_to[]")
	books, prios := c.PostFormArray("wc_booking_availability_bookable[]"), c.PostFormArray("wc_booking_availability_priority[]")
	var rules []any
	for i, t := range types {
		at := func(l []string) string {
			if i < len(l) {
				return strings.TrimSpace(l[i])
			}
			return ""
		}
		if at(froms) == "" && at(tos) == "" {
			continue
		}
		rules = append(rules, map[string]any{"type": t, "from": at(froms), "to": at(tos), "bookable": at(books) != "no", "priority": float64(max(gxAtoi(at(prios)), 1))})
	}
	bkSetMeta(p, "availability", rules)
}

var bkCostTypes = append([]gooOpt{{"custom", "Date range"}, {"months", "Range of months"}, {"weeks", "Range of weeks"}, {"days", "Range of days"},
	{"persons", "Person count"}, {"blocks", "Block count"}}, bkRuleTypes[4:]...)
var bkMods = []gooOpt{{"plus", "+"}, {"minus", "-"}, {"times", "&times;"}, {"divide", "&divide;"}, {"equals", "="}}

func bkCostRow(r bkCostRule) string {
	return `<tr><td>` + bkSelectName("wc_booking_pricing_type[]", r.Type, bkCostTypes) + `</td>` +
		`<td><input type="text" name="wc_booking_pricing_from[]" value="` + html.EscapeString(r.From) + `" placeholder="From" size="10"></td>` +
		`<td><input type="text" name="wc_booking_pricing_to[]" value="` + html.EscapeString(r.To) + `" placeholder="To" size="10"></td>` +
		`<td>` + bkSelectName("wc_booking_pricing_base_cost_modifier[]", r.BaseMod, bkMods) + `<input type="text" name="wc_booking_pricing_base_cost[]" value="` + gxFmt(r.BaseCost) + `" size="6" class="wc_input_price"></td>` +
		`<td>` + bkSelectName("wc_booking_pricing_cost_modifier[]", r.Mod, bkMods) + `<input type="text" name="wc_booking_pricing_cost[]" value="` + gxFmt(r.Cost) + `" size="6" class="wc_input_price"></td>` +
		`<td class="remove"><a href="#" class="button" onclick="this.closest('tr').remove();return false">&times;</a></td></tr>`
}

func bkPanelCosts(s *Server, c *gin.Context, p *gooProduct) template.HTML {
	k := bkConfOf(p)
	var b strings.Builder
	b.WriteString(`<div class="options_group">`)
	b.WriteString(bkField("Base cost", "_wc_booking_cost", bkInput("_wc_booking_cost", "text", gxFmt(k.BaseCost), ` class="wc_input_price"`), "One-off cost for the booking as a whole."))
	b.WriteString(bkField("Block cost", "_wc_booking_block_cost", bkInput("_wc_booking_block_cost", "text", gxFmt(k.BlockCost), ` class="wc_input_price"`), "This is the cost per block booked. All other costs (for resources and persons) are added to this."))
	b.WriteString(bkField("Display cost", "_wc_display_cost", bkInput("_wc_display_cost", "text", map[bool]string{true: gxFmt(k.DisplayCost), false: ""}[k.DisplayCost > 0], ` class="wc_input_price"`), "The cost is displayed to the user on the frontend. Leave blank to have it calculated for you. If a booking has varying costs, this will be prefixed with the word &quot;from:&quot;."))
	b.WriteString(`</div><div class="options_group wc_booking_pricing"><table class="widefat"><thead><tr><th>Range type</th><th>From</th><th>To</th><th>Base cost</th><th>Block cost</th><th></th></tr></thead><tbody>`)
	for _, r := range k.CostRules {
		b.WriteString(bkCostRow(r))
	}
	b.WriteString(`</tbody></table><p><template>` + bkCostRow(bkCostRule{Type: "custom"}) + `</template><a href="#" class="button add_row" ` + bkAddRowJS + `>Add Range</a></p></div>`)
	return template.HTML(b.String())
}

func bkSaveCosts(s *Server, c *gin.Context, p *gooProduct) {
	if p.Meta == nil {
		p.Meta = map[string]any{}
	}
	bkSetMeta(p, "cost", gxAtof(c.PostForm("_wc_booking_cost")))
	bkSetMeta(p, "block_cost", gxAtof(c.PostForm("_wc_booking_block_cost")))
	p.Meta["_wc_display_cost"] = gxAtof(c.PostForm("_wc_display_cost"))
	types, froms, tos := c.PostFormArray("wc_booking_pricing_type[]"), c.PostFormArray("wc_booking_pricing_from[]"), c.PostFormArray("wc_booking_pricing_to[]")
	bmods, bcosts := c.PostFormArray("wc_booking_pricing_base_cost_modifier[]"), c.PostFormArray("wc_booking_pricing_base_cost[]")
	mods, costs := c.PostFormArray("wc_booking_pricing_cost_modifier[]"), c.PostFormArray("wc_booking_pricing_cost[]")
	var rules []any
	for i, t := range types {
		at := func(l []string) string {
			if i < len(l) {
				return strings.TrimSpace(l[i])
			}
			return ""
		}
		if at(froms) == "" && at(tos) == "" {
			continue
		}
		rules = append(rules, map[string]any{"type": t, "from": at(froms), "to": at(tos), "base_modifier": at(bmods), "base_cost": gxAtof(at(bcosts)),
			"modifier": at(mods), "cost": gxAtof(at(costs))})
	}
	bkSetMeta(p, "pricing", rules)
	bkSyncPrice(p)
}

func bkPersonRow(pt bkPersonType) string {
	return `<tr><td><input type="hidden" name="person_id[]" value="` + html.EscapeString(pt.ID) + `"><input type="text" name="person_name[]" value="` + html.EscapeString(pt.Name) + `" placeholder="Person Type Name"></td>` +
		`<td><input type="text" name="person_cost[]" value="` + gxFmt(pt.BaseCost) + `" size="6" class="wc_input_price"></td>` +
		`<td><input type="text" name="person_block_cost[]" value="` + gxFmt(pt.BlockCost) + `" size="6" class="wc_input_price"></td>` +
		`<td><input type="text" name="person_description[]" value="` + html.EscapeString(pt.Description) + `"></td>` +
		`<td><input type="number" name="person_min[]" value="` + strconv.Itoa(pt.Min) + `" min="0" style="width:60px"></td>` +
		`<td><input type="number" name="person_max[]" value="` + map[bool]string{true: strconv.Itoa(pt.Max), false: ""}[pt.Max > 0] + `" min="0" style="width:60px"></td>` +
		`<td class="remove"><a href="#" class="button" onclick="this.closest('tr').remove();return false">&times;</a></td></tr>`
}

func bkPanelPersons(s *Server, c *gin.Context, p *gooProduct) template.HTML {
	k := bkConfOf(p)
	var b strings.Builder
	b.WriteString(`<div class="options_group">`)
	b.WriteString(bkField("Has persons", "_wc_booking_has_persons", bkCheckbox("_wc_booking_has_persons", k.HasPersons), "Enable this if this bookable product can be booked by a customer defined number of persons."))
	b.WriteString(bkField("Min persons", "_wc_booking_min_persons_group", bkInput("_wc_booking_min_persons_group", "number", strconv.Itoa(k.MinPersons), ` min="1" step="1"`), "The minimum number of persons per booking."))
	b.WriteString(bkField("Max persons", "_wc_booking_max_persons_group", bkInput("_wc_booking_max_persons_group", "number", map[bool]string{true: strconv.Itoa(k.MaxPersons), false: ""}[k.MaxPersons > 0], ` min="0" step="1"`), "The maximum number of persons per booking."))
	b.WriteString(bkField("Multiply all costs by person count", "_wc_booking_person_cost_multiplier", bkCheckbox("_wc_booking_person_cost_multiplier", k.PersonCostMult), "Enable this to multiply the entire cost of the booking (block and base costs) by the person count."))
	b.WriteString(bkField("Count persons as bookings", "_wc_booking_person_qty_multiplier", bkCheckbox("_wc_booking_person_qty_multiplier", k.PersonQtyMult), "Enable this to count each person as a booking until the max bookings per block is reached."))
	b.WriteString(bkField("Enable person types", "_wc_booking_has_person_types", bkCheckbox("_wc_booking_has_person_types", k.HasPersonTypes), "Person types allow you to offer different booking costs for different types of individuals, for example, adults and children."))
	b.WriteString(`</div><div class="options_group persons-types"><table class="widefat"><thead><tr><th>Person Type Name</th><th>Base Cost</th><th>Block Cost</th><th>Description</th><th>Min</th><th>Max</th><th></th></tr></thead><tbody>`)
	if k.HasPersonTypes {
		for _, pt := range k.PersonTypes {
			b.WriteString(bkPersonRow(pt))
		}
	}
	b.WriteString(`</tbody></table><p><template>` + bkPersonRow(bkPersonType{Name: "Person"}) + `</template><a href="#" class="button add_row" ` + bkAddRowJS + `>Add Person Type</a></p></div>`)
	return template.HTML(b.String())
}

func bkSavePersons(s *Server, c *gin.Context, p *gooProduct) {
	if p.Meta == nil {
		p.Meta = map[string]any{}
	}
	for _, k := range []string{"has_persons", "person_cost_multiplier", "person_qty_multiplier", "has_person_types"} {
		bkSetMeta(p, k, c.PostForm("_wc_booking_"+k) == "yes")
	}
	bkSetMeta(p, "min_persons_group", float64(max(gxAtoi(c.PostForm("_wc_booking_min_persons_group")), 1)))
	bkSetMeta(p, "max_persons_group", float64(gxAtoi(c.PostForm("_wc_booking_max_persons_group"))))
	ids, names, costs, bcosts := c.PostFormArray("person_id[]"), c.PostFormArray("person_name[]"), c.PostFormArray("person_cost[]"), c.PostFormArray("person_block_cost[]")
	descs, mins, maxs := c.PostFormArray("person_description[]"), c.PostFormArray("person_min[]"), c.PostFormArray("person_max[]")
	var types []any
	for i, n := range names {
		at := func(l []string) string {
			if i < len(l) {
				return strings.TrimSpace(l[i])
			}
			return ""
		}
		if strings.TrimSpace(n) == "" {
			continue
		}
		id := at(ids)
		if id == "" {
			id = gooRandomKey(6)
		}
		types = append(types, map[string]any{"id": id, "name": strings.TrimSpace(n), "description": at(descs), "cost": gxAtof(at(costs)),
			"block_cost": gxAtof(at(bcosts)), "min": float64(gxAtoi(at(mins))), "max": float64(gxAtoi(at(maxs)))})
	}
	p.Meta["_wc_booking_person_types"] = types
	bkSyncPrice(p)
}

// Goocommerce › Bookings ---------------------------------------------------------------------------------

func (s *Server) bkAdmin(c *gin.Context) {
	if !s.gooExtOn(bkKey) {
		c.String(http.StatusNotFound, "The Bookings extension is not active.")
		return
	}
	switch c.Query("action") {
	case "edit":
		s.bkAdminEdit(c)
	case "create":
		s.bkAdminCreate(c)
	case "calendar":
		s.bkAdminCalendar(c)
	case "confirm", "cancel":
		s.bkAdminQuick(c)
	default:
		s.bkAdminList(c)
	}
}

func bkAdminHead(active string) template.HTML {
	tabs := []gooOpt{{"", "All Bookings"}, {"calendar", "Calendar"}, {"create", "Create Booking"}}
	out := `<h1 class="wp-heading-inline">Bookings</h1> <a href="/wp-admin/goocommerce?page=bookings&amp;action=create" class="page-title-action">Add Booking</a><hr class="wp-header-end"><nav class="nav-tab-wrapper woo-nav-tab-wrapper">`
	for _, t := range tabs {
		cls := "nav-tab"
		if t.Value == active {
			cls += " nav-tab-active"
		}
		href := "/wp-admin/goocommerce?page=bookings"
		if t.Value != "" {
			href += "&amp;action=" + t.Value
		}
		out += `<a href="` + href + `" class="` + cls + `">` + t.Label + `</a>`
	}
	return template.HTML(out + `</nav>`)
}

func (s *Server) bkProductName(ctx context.Context, id int64) string {
	if p, err := s.gooGetProduct(ctx, id); err == nil {
		return p.Name
	}
	return "#" + itoa64(id)
}

func (s *Server) bkCustomerName(ctx context.Context, b *bkBooking) string {
	if b.OrderID != 0 {
		if o, err := s.gooGetOrder(ctx, b.OrderID); err == nil {
			if n := o.BillingName(); strings.TrimSpace(n) != "" {
				return n
			}
		}
	}
	if b.CustomerID != 0 {
		if u, err := s.store.UserByID(ctx, b.CustomerID); err == nil {
			return u.DisplayName
		}
	}
	return "Guest"
}

func (s *Server) bkAdminList(c *gin.Context) {
	ctx := c.Request.Context()
	status := c.Query("post_status")
	product, _ := strconv.ParseInt(c.Query("filter_bookings"), 10, 64)
	month := c.Query("m")
	where := []string{"true"}
	var args []any
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	if status != "" {
		where = append(where, "status = "+arg(status))
	} else {
		where = append(where, "status <> 'in-cart'")
	}
	if product != 0 {
		where = append(where, "product_id = "+arg(product))
	}
	if t, err := time.ParseInLocation("2006-01", month, s.gxLoc()); err == nil {
		where = append(where, "start_at >= "+arg(t)+" AND start_at < "+arg(t.AddDate(0, 1, 0)))
	}
	if q := strings.TrimSpace(c.Query("s")); q != "" {
		n, _ := strconv.ParseInt(strings.TrimPrefix(q, "#"), 10, 64)
		where = append(where, "(id = "+arg(n)+" OR order_id = "+arg(n)+")")
	}
	page := max(gxAtoi(c.Query("paged")), 1)
	per := 20
	var total int
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_bookings WHERE `+strings.Join(where, " AND "), args...).Scan(&total)
	list := s.bkQuery(ctx, strings.Join(where, " AND ")+fmt.Sprintf(" ORDER BY start_at DESC LIMIT %d OFFSET %d", per, (page-1)*per), args...)
	counts := map[string]int{}
	all := 0
	if rows, err := s.store.Pool.Query(ctx, `SELECT status, count(*) FROM goo_bookings GROUP BY status`); err == nil {
		for rows.Next() {
			var st string
			var n int
			if rows.Scan(&st, &n) == nil {
				counts[st] = n
				if st != "in-cart" {
					all += n
				}
			}
		}
		rows.Close()
	}
	var b strings.Builder
	b.WriteString(`<ul class="subsubsub">`)
	cls := func(on bool) string {
		if on {
			return ` class="current" aria-current="page"`
		}
		return ""
	}
	fmt.Fprintf(&b, `<li class="all"><a href="/wp-admin/goocommerce?page=bookings"%s>All <span class="count">(%d)</span></a></li>`, cls(status == ""), all)
	for _, st := range bkStatuses {
		if counts[st.Value] == 0 {
			continue
		}
		fmt.Fprintf(&b, ` | <li class="%s"><a href="/wp-admin/goocommerce?page=bookings&amp;post_status=%s"%s>%s <span class="count">(%d)</span></a></li>`, st.Value, st.Value, cls(status == st.Value), st.Label, counts[st.Value])
	}
	b.WriteString(`</ul>`)
	// Filters.
	b.WriteString(`<form method="get" action="/wp-admin/goocommerce"><input type="hidden" name="page" value="bookings"><input type="hidden" name="post_status" value="` + html.EscapeString(status) + `">`)
	b.WriteString(`<p class="search-box"><label class="screen-reader-text" for="post-search-input">Search Bookings:</label><input type="search" id="post-search-input" name="s" value="` + html.EscapeString(c.Query("s")) + `"> <input type="submit" class="button" value="Search Bookings"></p>`)
	b.WriteString(`<div class="tablenav top"><div class="alignleft actions"><select name="filter_bookings"><option value="">All Bookable Products</option>`)
	for _, p := range s.bkProducts(ctx) {
		fmt.Fprintf(&b, `<option value="%d"%s>%s</option>`, p.ID, gxSel(itoa64(p.ID), itoa64(product)), html.EscapeString(p.Name))
	}
	b.WriteString(`</select> <input type="month" name="m" value="` + html.EscapeString(month) + `"> <input type="submit" class="button" value="Filter"></div>`)
	base := "/wp-admin/goocommerce?page=bookings&post_status=" + url.QueryEscape(status) + "&filter_bookings=" + c.Query("filter_bookings") + "&m=" + url.QueryEscape(month)
	b.WriteString(gxPagination(base, page, total, per) + `<br class="clear"></div></form>`)
	b.WriteString(`<table class="wp-list-table widefat fixed striped posts"><thead><tr><th scope="col" class="manage-column column-booking_status">Status</th><th scope="col" class="manage-column column-booking_id column-primary">ID</th>` +
		`<th scope="col">Booked Product</th><th scope="col"># of Persons</th><th scope="col">Booked By</th><th scope="col">Order</th><th scope="col">Start Date</th><th scope="col">End Date</th><th scope="col">Actions</th></tr></thead><tbody id="the-list">`)
	if len(list) == 0 {
		b.WriteString(`<tr class="no-items"><td class="colspanchange" colspan="9">No Bookings found</td></tr>`)
	}
	nonce := s.nonce(c, "goo-bookings")
	for _, bk := range list {
		edit := "/wp-admin/goocommerce?page=bookings&amp;action=edit&amp;id=" + itoa64(bk.ID)
		order := "&ndash;"
		if bk.OrderID != 0 {
			order = `<a href="/wp-admin/goocommerce?page=orders&amp;action=edit&amp;id=` + itoa64(bk.OrderID) + `">#` + itoa64(bk.OrderID) + `</a>`
		}
		actions := `<a class="button" href="` + edit + `">Edit</a>`
		if bk.Status == "pending-confirmation" {
			actions += ` <a class="button" href="/wp-admin/goocommerce?page=bookings&amp;action=confirm&amp;id=` + itoa64(bk.ID) + `&amp;_wpnonce=` + nonce + `">Confirm</a>`
		}
		if bk.Status != "cancelled" && bk.Status != "complete" {
			actions += ` <a class="button" href="/wp-admin/goocommerce?page=bookings&amp;action=cancel&amp;id=` + itoa64(bk.ID) + `&amp;_wpnonce=` + nonce + `">Cancel</a>`
		}
		fmt.Fprintf(&b, `<tr id="booking-%d"><td class="booking_status column-booking_status"><span class="status-%s tips">%s</span></td><td class="column-primary"><a href="%s"><strong>Booking #%d</strong></a></td>`+
			`<td>%s</td><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			bk.ID, bk.Status, html.EscapeString(bkStatusLabel(bk.Status)), edit, bk.ID, html.EscapeString(s.bkProductName(ctx, bk.ProductID)), bkPersonsTotal(bk.Persons),
			html.EscapeString(s.bkCustomerName(ctx, bk)), order, html.EscapeString(s.bkWhen(bk, bk.Start)), html.EscapeString(s.bkWhen(bk, bk.End)), actions)
	}
	b.WriteString(`</tbody></table>`)
	s.gxAdminPage(c, "goo-ext-bookings", "bookings", "Bookings", bkAdminHead(""), template.HTML(b.String()))
}

// bkProducts lists the bookable products.
func (s *Server) bkProducts(ctx context.Context) []*gooProduct {
	rows, err := s.store.Pool.Query(ctx, `SELECT p.post_id FROM goo_products p JOIN posts ON posts.id = p.post_id WHERE p.type = $1 AND posts.status <> 'trash' ORDER BY posts.title`, bkType)
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
	m, _ := s.gooGetProducts(ctx, ids)
	var out []*gooProduct
	for _, id := range ids {
		if p := m[id]; p != nil {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) bkAdminQuick(c *gin.Context) {
	ctx := c.Request.Context()
	if !s.checkNonceValue(c, c.Query("_wpnonce"), "goo-bookings") {
		s.denyNonce(c)
		return
	}
	id, _ := strconv.ParseInt(c.Query("id"), 10, 64)
	b, err := s.bkGet(ctx, id)
	if err != nil {
		c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=bookings&goo-error="+url.QueryEscape("Booking not found."))
		return
	}
	to := map[string]string{"confirm": "confirmed", "cancel": "cancelled"}[c.Query("action")]
	_ = s.bkSetStatus(ctx, b, to)
	c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=bookings&goo-msg="+url.QueryEscape(fmt.Sprintf("Booking #%d is now %s.", b.ID, strings.ToLower(bkStatusLabel(to)))))
}

func (s *Server) bkAdminEdit(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := strconv.ParseInt(c.Query("id"), 10, 64)
	b, err := s.bkGet(ctx, id)
	if err != nil {
		c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=bookings&goo-error="+url.QueryEscape("Booking not found."))
		return
	}
	loc := s.gxLoc()
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "goo-booking-edit") {
			s.denyNonce(c)
			return
		}
		b.AllDay = c.PostForm("_booking_all_day") == "yes"
		parse := func(d, t string) (time.Time, error) {
			if b.AllDay || t == "" {
				t = "00:00"
			}
			return time.ParseInLocation("2006-01-02 15:04", d+" "+t, loc)
		}
		start, err1 := parse(c.PostForm("booking_start_date"), c.PostForm("booking_start_time"))
		end, err2 := parse(c.PostForm("booking_end_date"), c.PostForm("booking_end_time"))
		if err1 != nil || err2 != nil || !end.After(start) && !(b.AllDay && !end.Before(start)) {
			c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=bookings&action=edit&id="+itoa64(b.ID)+"&goo-error="+url.QueryEscape("Please enter a valid start and end date."))
			return
		}
		if b.AllDay {
			end = end.AddDate(0, 0, 1) // the end date shown is the last day
		}
		b.Start, b.End = start, end
		if pid, _ := strconv.ParseInt(c.PostForm("product_id"), 10, 64); pid != 0 {
			b.ProductID = pid
		}
		b.OrderID, _ = strconv.ParseInt(c.PostForm("_booking_order_id"), 10, 64)
		b.CustomerID, _ = strconv.ParseInt(c.PostForm("_booking_customer_id"), 10, 64)
		persons := map[string]any{}
		for k, v := range c.Request.PostForm {
			if strings.HasPrefix(k, "booking_persons[") && len(v) > 0 {
				if n := gxAtoi(v[0]); n > 0 {
					persons[strings.TrimSuffix(strings.TrimPrefix(k, "booking_persons["), "]")] = float64(n)
				}
			}
		}
		b.Persons = persons
		if v := strings.TrimSpace(c.PostForm("_booking_cost")); v != "" {
			b.Cost = gxAtof(v)
		}
		newStatus := c.PostForm("_booking_status")
		if err := s.bkUpdate(ctx, b); err == nil && newStatus != "" {
			_ = s.bkSetStatus(ctx, b, newStatus)
		}
		c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=bookings&action=edit&id="+itoa64(b.ID)+"&goo-msg="+url.QueryEscape("Booking updated."))
		return
	}
	endShown := b.End
	if b.AllDay {
		endShown = b.End.AddDate(0, 0, -1)
	}
	var out strings.Builder
	fmt.Fprintf(&out, `<form method="post" action="/wp-admin/goocommerce?page=bookings&amp;action=edit&amp;id=%d"><input type="hidden" name="_wpnonce" value="%s">`, b.ID, s.nonce(c, "goo-booking-edit"))
	fmt.Fprintf(&out, `<div id="poststuff"><div class="postbox" id="woocommerce-booking-data"><div class="postbox-header"><h2>Booking #%d details</h2></div><div class="inside"><table class="form-table"><tbody>`, b.ID)
	row := func(label, input string) { out.WriteString(`<tr><th scope="row">` + label + `</th><td>` + input + `</td></tr>`) }
	var opts strings.Builder
	for _, st := range bkStatuses {
		opts.WriteString(`<option value="` + st.Value + `"` + gxSel(st.Value, b.Status) + `>` + st.Label + `</option>`)
	}
	row("Booking status", `<select name="_booking_status">`+opts.String()+`</select>`)
	var prods strings.Builder
	for _, p := range s.bkProducts(ctx) {
		prods.WriteString(fmt.Sprintf(`<option value="%d"%s>%s</option>`, p.ID, gxSel(itoa64(p.ID), itoa64(b.ProductID)), html.EscapeString(p.Name)))
	}
	row("Booked product", `<select name="product_id">`+prods.String()+`</select>`)
	row("Order ID", fmt.Sprintf(`<input type="number" name="_booking_order_id" value="%d" min="0">`, b.OrderID))
	row("Customer (user ID)", fmt.Sprintf(`<input type="number" name="_booking_customer_id" value="%d" min="0"> %s`, b.CustomerID, html.EscapeString(s.bkCustomerName(ctx, b))))
	row("All day booking", `<label><input type="checkbox" name="_booking_all_day" value="yes"`+gxChk(b.AllDay)+`> Check this box if the booking is for all day.</label>`)
	row("Start date", fmt.Sprintf(`<input type="date" name="booking_start_date" value="%s"> <input type="time" name="booking_start_time" value="%s">`, b.Start.In(loc).Format("2006-01-02"), b.Start.In(loc).Format("15:04")))
	row("End date", fmt.Sprintf(`<input type="date" name="booking_end_date" value="%s"> <input type="time" name="booking_end_time" value="%s">`, endShown.In(loc).Format("2006-01-02"), b.End.In(loc).Format("15:04")))
	if p, err := s.gooGetProduct(ctx, b.ProductID); err == nil {
		conf := bkConfOf(p)
		if conf.HasPersons {
			var pr strings.Builder
			for _, pt := range conf.PersonTypes {
				pr.WriteString(fmt.Sprintf(`<label>%s <input type="number" name="booking_persons[%s]" value="%d" min="0" style="width:70px"></label> `, html.EscapeString(pt.Name), html.EscapeString(pt.ID), int(gxNum(b.Persons[pt.ID]))))
			}
			row("Persons", pr.String())
		}
	}
	row("Cost", `<input type="text" name="_booking_cost" value="`+gxFmt(b.Cost)+`" class="wc_input_price">`)
	out.WriteString(`</tbody></table></div></div></div><p class="submit"><button type="submit" class="button button-primary">Save Booking</button></p></form>`)
	s.gxAdminPage(c, "goo-ext-bookings", "bookings", fmt.Sprintf("Edit Booking #%d", b.ID), bkAdminHead("edit"), template.HTML(out.String()))
}

// bkAdminCreate is "Create Booking": a customer, a product, the order to attach, then the date.
func (s *Server) bkAdminCreate(c *gin.Context) {
	ctx := c.Request.Context()
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "goo-booking-create") {
			s.denyNonce(c)
			return
		}
		b, err := s.bkCreateFromAdmin(ctx, c.Request.PostForm)
		if err != nil {
			c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=bookings&action=create&goo-error="+url.QueryEscape(gxStripTags(err.Error())))
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=bookings&action=edit&id="+itoa64(b.ID)+"&goo-msg="+url.QueryEscape("Booking created."))
		return
	}
	var out strings.Builder
	out.WriteString(`<p>You can create a new booking for a customer here. This form will create a booking for the user, and optionally an associated order. Created orders will be marked as pending payment.</p>`)
	out.WriteString(`<form method="post" action="/wp-admin/goocommerce?page=bookings&amp;action=create"><input type="hidden" name="_wpnonce" value="` + s.nonce(c, "goo-booking-create") + `"><table class="form-table"><tbody>`)
	out.WriteString(`<tr><th scope="row"><label for="customer_id">Customer</label></th><td><input type="number" name="customer_id" id="customer_id" min="0" placeholder="User ID (0 = guest)"></td></tr>`)
	out.WriteString(`<tr><th scope="row"><label for="bookable_product_id">Bookable Product</label></th><td><select name="bookable_product_id" id="bookable_product_id">`)
	for _, p := range s.bkProducts(ctx) {
		out.WriteString(fmt.Sprintf(`<option value="%d">%s</option>`, p.ID, html.EscapeString(p.Name)))
	}
	out.WriteString(`</select></td></tr><tr><th scope="row">Create Order</th><td><label><input type="radio" name="booking_order" value="new" checked> Create a new corresponding order for this new booking. Please note - the booking will not be active until the order is processed/completed.</label><br>` +
		`<label><input type="radio" name="booking_order" value="existing"> Assign this booking to an existing order with this ID: <input type="number" name="booking_order_id" min="0"></label><br>` +
		`<label><input type="radio" name="booking_order" value=""> Don't create an order for this booking.</label></td></tr>`)
	out.WriteString(`<tr><th scope="row">Date</th><td><input type="date" name="wc_bookings_field_start_date" required> <input type="time" name="wc_bookings_field_start_date_time"> <span class="description">The time is used for hour and minute blocks.</span></td></tr>`)
	out.WriteString(`<tr><th scope="row">Duration</th><td><input type="number" name="wc_bookings_field_duration" min="1" value="1"> <span class="description">Blocks, for customer defined durations.</span></td></tr>`)
	out.WriteString(`<tr><th scope="row">Persons</th><td><input type="number" name="wc_bookings_field_persons" min="0" value="1"></td></tr>`)
	out.WriteString(`</tbody></table><p class="submit"><button type="submit" class="button button-primary">Create Booking</button></p></form>`)
	s.gxAdminPage(c, "goo-ext-bookings", "bookings", "Create Booking", bkAdminHead("create"), template.HTML(out.String()))
}

// bkCreateFromAdmin creates a booking from the admin form (availability is checked, like WooCommerce
// Bookings' create screen), with a new pending order when asked.
func (s *Server) bkCreateFromAdmin(ctx context.Context, form map[string][]string) (*bkBooking, error) {
	pid, _ := strconv.ParseInt(gxForm(form, "bookable_product_id"), 10, 64)
	p, err := s.gooGetProduct(ctx, pid)
	if err != nil || p.Type != bkType {
		return nil, fmt.Errorf("Please choose a bookable product.")
	}
	conf := bkConfOf(p)
	start, blocks, persons, err := s.bkParseRequest(conf, form)
	if err != nil {
		return nil, err
	}
	if err := s.bkCheck(ctx, p, conf, start, blocks, bkSumPersons(persons), 0); err != nil {
		return nil, err
	}
	end := start
	for i := 0; i < blocks; i++ {
		end = conf.blockEnd(end)
	}
	pm := map[string]any{}
	for k, v := range persons {
		pm[k] = float64(v)
	}
	cost := gooRound(bkCost(conf, start, blocks, persons), s.gooDecimals())
	customer, _ := strconv.ParseInt(gxForm(form, "customer_id"), 10, 64)
	b := &bkBooking{ProductID: p.ID, CustomerID: customer, Status: "unpaid", Start: start, End: end, AllDay: !conf.timed(), Persons: pm, Cost: cost}
	switch gxForm(form, "booking_order") {
	case "existing":
		b.OrderID, _ = strconv.ParseInt(gxForm(form, "booking_order_id"), 10, 64)
		if _, err := s.gooGetOrder(ctx, b.OrderID); err != nil {
			return nil, fmt.Errorf("Invalid order ID provided")
		}
	case "new":
		o := &gooOrder{Status: "pending", CustomerID: customer, CreatedVia: "bookings", Billing: map[string]string{}, Shipping: map[string]string{}, Meta: map[string]any{}}
		if customer != 0 {
			if u, err := s.store.UserByID(ctx, customer); err == nil {
				o.Billing["email"] = u.Email
				o.Billing["first_name"] = u.Profile["first_name"]
				o.Billing["last_name"] = u.Profile["last_name"]
			}
		}
		o.Items = []*gooOrderItem{{Type: "line_item", Name: p.Name, ProductID: p.ID, Quantity: 1, Subtotal: cost, Total: cost, Meta: map[string]any{}}}
		s.gooRecalculate(o)
		if err := s.gooSaveOrder(ctx, o, true); err != nil {
			return nil, err
		}
		b.OrderID, b.OrderItemID = o.ID, o.Items[0].ID
	}
	if err := s.bkInsert(ctx, b); err != nil {
		return nil, err
	}
	if b.OrderItemID != 0 {
		_, _ = s.store.Pool.Exec(ctx, `UPDATE goo_order_items SET meta = meta || $2::jsonb WHERE id = $1`, b.OrderItemID,
			fmt.Sprintf(`{"_goo_booking":{"_booking_id":%d}}`, b.ID))
	}
	s.bkMail(ctx, "new_booking", b)
	return b, nil
}

// bkAdminCalendar is the month calendar of bookings.
func (s *Server) bkAdminCalendar(c *gin.Context) {
	ctx := c.Request.Context()
	loc := s.gxLoc()
	month, err := time.ParseInLocation("2006-01", c.Query("calendar_month"), loc)
	if err != nil {
		now := time.Now().In(loc)
		month = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	}
	next := month.AddDate(0, 1, 0)
	list := s.bkQuery(ctx, `status NOT IN ('cancelled','in-cart') AND start_at < $1 AND end_at > $2 ORDER BY start_at`, next, month)
	byDay := map[int][]*bkBooking{}
	for _, b := range list {
		for d := b.Start.In(loc); d.Before(b.End) && d.Before(next); d = d.AddDate(0, 0, 1) {
			if !d.Before(month) {
				byDay[d.Day()] = append(byDay[d.Day()], b)
			}
			if !b.AllDay {
				break
			}
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, `<div class="wc_bookings_calendar_form"><div class="tablenav"><div class="filters"><a class="button prev" href="/wp-admin/goocommerce?page=bookings&amp;action=calendar&amp;calendar_month=%s">&larr;</a> `+
		`<strong class="calendar-month">%s</strong> <a class="button next" href="/wp-admin/goocommerce?page=bookings&amp;action=calendar&amp;calendar_month=%s">&rarr;</a></div></div>`,
		month.AddDate(0, -1, 0).Format("2006-01"), month.Format("January 2006"), next.Format("2006-01"))
	out.WriteString(`<style>.wc_bookings_calendar{width:100%;border-collapse:collapse;table-layout:fixed}.wc_bookings_calendar td,.wc_bookings_calendar th{border:1px solid #dcdcde;vertical-align:top;padding:4px;background:#fff}.wc_bookings_calendar td{height:90px}.wc_bookings_calendar .day-number{color:#646970;font-weight:600}.wc_bookings_calendar ul{margin:2px 0}.wc_bookings_calendar li{background:#f0e9f8;border-left:3px solid #7f54b3;padding:1px 4px;margin:2px 0;font-size:12px;overflow:hidden;white-space:nowrap;text-overflow:ellipsis}</style>`)
	first := gxAtoi(defaultStr(s.gooExtString(bkKey, "calendar_first_day"), "1"))
	out.WriteString(`<table class="wc_bookings_calendar widefat"><thead><tr>`)
	days := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	for i := 0; i < 7; i++ {
		out.WriteString(`<th>` + days[(i+first)%7] + `</th>`)
	}
	out.WriteString(`</tr></thead><tbody><tr>`)
	offset := (int(month.Weekday()) - first + 7) % 7
	for i := 0; i < offset; i++ {
		out.WriteString(`<td class="calendar-diff-month"></td>`)
	}
	last := next.AddDate(0, 0, -1).Day()
	for d := 1; d <= last; d++ {
		fmt.Fprintf(&out, `<td><span class="day-number">%d</span><ul class="bookings">`, d)
		for _, b := range byDay[d] {
			label := s.bkProductName(ctx, b.ProductID)
			if !b.AllDay {
				label = s.gxTime(b.Start) + " " + label
			}
			fmt.Fprintf(&out, `<li><a href="/wp-admin/goocommerce?page=bookings&amp;action=edit&amp;id=%d" title="%s">#%d %s</a></li>`, b.ID, html.EscapeString(bkStatusLabel(b.Status)), b.ID, html.EscapeString(label))
		}
		out.WriteString(`</ul></td>`)
		if (offset+d)%7 == 0 && d != last {
			out.WriteString(`</tr><tr>`)
		}
	}
	for i := (offset + last) % 7; i != 0 && i < 7; i++ {
		out.WriteString(`<td class="calendar-diff-month"></td>`)
	}
	out.WriteString(`</tr></tbody></table></div>`)
	s.gxAdminPage(c, "goo-ext-bookings", "bookings", "Bookings Calendar", bkAdminHead("calendar"), template.HTML(out.String()))
}
