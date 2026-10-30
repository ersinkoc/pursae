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

// Bookings resources (WooCommerce Bookings resources): global resources with an available quantity
// and availability rules of their own, linked to bookable products with a base and a block cost.
// A product's resources are chosen by the customer or assigned automatically (the first resource
// with room); a booking holds its resource's capacity across every product that uses it.

func init() {
	gooExtMenuItems = append(gooExtMenuItems, gooExtMenuItem{Module: bkKey, Page: "booking-resources", Title: "Resources"})
	gooExtAdminPages["booking-resources"] = func(s *Server, c *gin.Context) { s.gooBkResourcesAdmin(c) }
	gooProductPanels = append(gooProductPanels,
		&gooProductPanel{ID: "bookings_resources", Label: "Resources", Module: bkKey, ShowFor: []string{bkType}, Render: gooBkPanelResources, Save: gooBkSaveResources})
}

// gooBkResource is a global bookable resource.
type gooBkResource struct {
	ID    int64
	Name  string
	Qty   int
	Rules []bkRule
	Order int
}

// gxbProdRes is a product's link to a resource with its costs.
type gxbProdRes struct {
	ID        int64
	BaseCost  float64
	BlockCost float64
}

// gxbConfResources reads the product's resource settings into its booking conf.
func gxbConfResources(p *gooProduct, c *bkConf) {
	c.HasResources = gxBool(bkMeta(p, "has_resources"))
	c.ResourceLabel = gxStr(bkMeta(p, "resource_label"))
	c.ResourceAssign = defaultStr(gxStr(bkMeta(p, "resources_assignment")), "customer")
	for _, m := range gxList(bkMeta(p, "resources")) {
		if id := int64(gxNum(m["id"])); id > 0 {
			c.Resources = append(c.Resources, gxbProdRes{ID: id, BaseCost: gxNum(m["base_cost"]), BlockCost: gxNum(m["block_cost"])})
		}
	}
	if !c.HasResources {
		c.Resources = nil
	}
}

// resourceLabel is the label of the resource field ("Resource" by default).
func (c *bkConf) resourceLabel() string { return defaultStr(c.ResourceLabel, "Resource") }

// resourceCosts are the base and block costs of a linked resource.
func (c *bkConf) resourceCosts(id int64) (float64, float64) {
	for _, r := range c.Resources {
		if r.ID == id {
			return r.BaseCost, r.BlockCost
		}
	}
	return 0, 0
}

// Storage ---------------------------------------------------------------------------------------------

func gxbScanResource(row interface{ Scan(...any) error }) (*gooBkResource, error) {
	r := &gooBkResource{}
	var rules []any
	if err := row.Scan(&r.ID, &r.Name, &r.Qty, &rules, &r.Order); err != nil {
		return nil, err
	}
	r.Rules = gxbRulesOf(rules)
	return r, nil
}

func gxbRulesOf(v any) []bkRule {
	var out []bkRule
	for _, m := range gxList(v) {
		out = append(out, bkRule{Type: gxStr(m["type"]), From: gxStr(m["from"]), To: gxStr(m["to"]), Bookable: gxBool(m["bookable"]), Priority: int(gxNum(m["priority"]))})
	}
	return out
}

func gxbRulesJSON(rules []bkRule) []any {
	out := []any{}
	for _, r := range rules {
		out = append(out, map[string]any{"type": r.Type, "from": r.From, "to": r.To, "bookable": r.Bookable, "priority": float64(max(r.Priority, 1))})
	}
	return out
}

const gxbResCols = `id, name, qty, availability, menu_order`

// gooBkResource loads a resource.
func (s *Server) gooBkResource(ctx context.Context, id int64) (*gooBkResource, error) {
	return gxbScanResource(s.store.Pool.QueryRow(ctx, `SELECT `+gxbResCols+` FROM goo_booking_resources WHERE id = $1`, id))
}

// gooBkResources lists every resource (menu order, then name).
func (s *Server) gooBkResources(ctx context.Context) []*gooBkResource {
	rows, err := s.store.Pool.Query(ctx, `SELECT `+gxbResCols+` FROM goo_booking_resources ORDER BY menu_order, name, id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*gooBkResource
	for rows.Next() {
		if r, err := gxbScanResource(rows); err == nil {
			out = append(out, r)
		}
	}
	return out
}

// gooBkSaveResource inserts or updates a resource.
func (s *Server) gooBkSaveResource(ctx context.Context, r *gooBkResource) error {
	if r.Qty < 1 {
		r.Qty = 1
	}
	if r.ID == 0 {
		return s.store.Pool.QueryRow(ctx, `INSERT INTO goo_booking_resources (name, qty, availability, menu_order) VALUES ($1,$2,$3,$4) RETURNING id`,
			r.Name, r.Qty, gxbRulesJSON(r.Rules), r.Order).Scan(&r.ID)
	}
	_, err := s.store.Pool.Exec(ctx, `UPDATE goo_booking_resources SET name=$2, qty=$3, availability=$4, menu_order=$5, date_modified=now() WHERE id=$1`,
		r.ID, r.Name, r.Qty, gxbRulesJSON(r.Rules), r.Order)
	return err
}

// gooBkProductResources are a product's linked resources that still exist, in the product's order.
func (s *Server) gooBkProductResources(ctx context.Context, c *bkConf) []*gooBkResource {
	if !c.HasResources {
		return nil
	}
	var out []*gooBkResource
	for _, pr := range c.Resources {
		if r, err := s.gooBkResource(ctx, pr.ID); err == nil {
			out = append(out, r)
		}
	}
	return out
}

// gooBkResourceName is a resource's name ("" for none or a deleted one).
func (s *Server) gooBkResourceName(ctx context.Context, id int64) string {
	if id == 0 {
		return ""
	}
	if r, err := s.gooBkResource(ctx, id); err == nil {
		return r.Name
	}
	return "#" + itoa64(id)
}

// gooBkResourceLabelFor is the product's resource label for a booking.
func (s *Server) gooBkResourceLabelFor(ctx context.Context, productID int64) string {
	if p, err := s.gooGetProduct(ctx, productID); err == nil {
		return bkConfOf(p).resourceLabel()
	}
	return "Resource"
}

// Availability ------------------------------------------------------------------------------------------

// gxbRulesDecide is the verdict of the most important rule matching the block [s, e), if any
// matches: lowest priority number first; within a priority, the rule lower in the table.
func gxbRulesDecide(list []bkRule, timed bool, s, e time.Time) (bookable, matched bool) {
	type ix struct {
		r bkRule
		i int
	}
	rules := make([]ix, len(list))
	for i, r := range list {
		if r.Priority == 0 {
			r.Priority = 10
		}
		rules[i] = ix{r, i}
	}
	for a := 1; a < len(rules); a++ { // stable insertion sort (few rules)
		for b := a; b > 0; b-- {
			x, y := rules[b-1], rules[b]
			if x.r.Priority > y.r.Priority || (x.r.Priority == y.r.Priority && x.i < y.i) {
				rules[b-1], rules[b] = y, x
			} else {
				break
			}
		}
	}
	for _, x := range rules {
		r := x.r
		if m, isDate := bkDateMatch(r.Type, r.From, r.To, s); isDate {
			if m {
				return r.Bookable, true
			}
			continue
		}
		if !timed {
			continue // time rules do not apply to day / month blocks
		}
		if strings.HasPrefix(r.Type, "time") && bkTimeMatch(r, s, e) {
			return r.Bookable, true
		}
	}
	return false, false
}

// gxbBookable is the block's availability for a resource: the resource's rules override the
// product's (nil: the product alone).
func gxbBookable(c *bkConf, res *gooBkResource, s, e time.Time) bool {
	if res != nil {
		if v, ok := gxbRulesDecide(res.Rules, c.timed(), s, e); ok {
			return v
		}
	}
	return c.bookable(s, e)
}

// gxbCapacity is the places per block: the resource's quantity, else the product's maximum.
func gxbCapacity(c *bkConf, res *gooBkResource) int {
	if res != nil && res.Qty > 0 {
		return res.Qty
	}
	return c.MaxPerBlock
}

// gooBkPickResource checks the booking against the product's resources and returns the resource
// it gets: the requested one (customer selected), else the first one with room. Products without
// resources check the product itself and return 0.
func (s *Server) gooBkPickResource(ctx context.Context, p *gooProduct, c *bkConf, requested int64, start time.Time, blocks, persons int, exclude int64) (int64, error) {
	list := s.gooBkProductResources(ctx, c)
	if len(list) == 0 {
		return 0, s.gxbCheck(ctx, p, c, nil, start, blocks, persons, exclude)
	}
	if requested != 0 {
		for _, r := range list {
			if r.ID == requested {
				return r.ID, s.gxbCheck(ctx, p, c, r, start, blocks, persons, exclude)
			}
		}
		return 0, gooCartErr("Please choose a valid %s.", html.EscapeString(strings.ToLower(c.resourceLabel())))
	}
	var first error
	for _, r := range list {
		err := s.gxbCheck(ctx, p, c, r, start, blocks, persons, exclude)
		if err == nil {
			return r.ID, nil
		}
		if first == nil {
			first = err
		}
	}
	return 0, first
}

// gxbRequestedResource reads the posted resource (wc_bookings_field_resource).
func gxbRequestedResource(form map[string][]string) int64 {
	id, _ := strconv.ParseInt(gxForm(form, "wc_bookings_field_resource"), 10, 64)
	return id
}

// gooBkSlotsAll lists the day's slots for a resource; with resource 0 on a product with resources,
// the places of all its resources are summed (automatic assignment).
func (s *Server) gooBkSlotsAll(ctx context.Context, p *gooProduct, c *bkConf, day time.Time, resource int64) []bkSlot {
	list := s.gooBkProductResources(ctx, c)
	if len(list) == 0 {
		return s.gxbSlots(ctx, p, c, nil, day)
	}
	if resource != 0 {
		for _, r := range list {
			if r.ID == resource {
				return s.gxbSlots(ctx, p, c, r, day)
			}
		}
		return nil
	}
	merged := map[string]*bkSlot{}
	var order []string
	for _, r := range list {
		for _, sl := range s.gxbSlots(ctx, p, c, r, day) {
			if m := merged[sl.Time]; m != nil {
				m.Available += sl.Available
				continue
			}
			v := sl
			merged[sl.Time] = &v
			order = append(order, sl.Time)
		}
	}
	// Times are "15:04": string order is time order.
	for a := 1; a < len(order); a++ {
		for b := a; b > 0 && order[b-1] > order[b]; b-- {
			order[b-1], order[b] = order[b], order[b-1]
		}
	}
	out := make([]bkSlot, 0, len(order))
	for _, t := range order {
		out = append(out, *merged[t])
	}
	return out
}

// Product panel -------------------------------------------------------------------------------------

func gxbResRow(id int64, name string, base, block float64) string {
	return `<tr><td><input type="hidden" name="resource_id[]" value="` + itoa64(id) + `"><strong>` + html.EscapeString(name) + `</strong> <small>#` + itoa64(id) + `</small></td>` +
		`<td><input type="text" name="resource_base_cost[]" value="` + gxFmt(base) + `" size="6" class="wc_input_price"></td>` +
		`<td><input type="text" name="resource_block_cost[]" value="` + gxFmt(block) + `" size="6" class="wc_input_price"></td>` +
		`<td class="remove"><a href="#" class="button" onclick="this.closest('tr').remove();return false">&times;</a></td></tr>`
}

func gooBkPanelResources(s *Server, c *gin.Context, p *gooProduct) template.HTML {
	k := bkConfOf(p)
	ctx := c.Request.Context()
	all := s.gooBkResources(ctx)
	names := map[int64]string{}
	for _, r := range all {
		names[r.ID] = r.Name
	}
	var b strings.Builder
	b.WriteString(`<div class="options_group">`)
	b.WriteString(bkField("Has resources", "_wc_booking_has_resources", bkCheckbox("_wc_booking_has_resources", k.HasResources), "Enable this if this bookable product has multiple bookable resources, for example room types or instructors."))
	b.WriteString(bkField("Label", "_wc_booking_resource_label", bkInput("_wc_booking_resource_label", "text", k.ResourceLabel, ` placeholder="Resource"`), "The label shown on the frontend if the resource is customer defined."))
	b.WriteString(bkField("Resources are&hellip;", "_wc_booking_resources_assignment", bkSelect("_wc_booking_resources_assignment", k.ResourceAssign,
		[]gooOpt{{"customer", "Customer selected"}, {"automatic", "Automatically assigned"}}), "Customer selected resources allow customers to choose one from the booking form."))
	b.WriteString(`</div><div class="options_group woocommerce_bookable_resources"><table class="widefat"><thead><tr><th>Resource</th><th>Base Cost</th><th>Block Cost</th><th></th></tr></thead><tbody>`)
	for _, pr := range k.Resources {
		n, ok := names[pr.ID]
		if !ok {
			continue
		}
		b.WriteString(gxbResRow(pr.ID, n, pr.BaseCost, pr.BlockCost))
	}
	b.WriteString(`</tbody></table><p>`)
	if len(all) == 0 {
		b.WriteString(`<span class="description">No resources yet: <a href="/wp-admin/goocommerce?page=booking-resources&amp;action=edit">add a resource</a> first.</span>`)
	} else {
		b.WriteString(`<select class="add_resource_id">`)
		for _, r := range all {
			fmt.Fprintf(&b, `<option value="%d">%s</option>`, r.ID, html.EscapeString(r.Name))
		}
		b.WriteString(`</select>`)
		for _, r := range all {
			fmt.Fprintf(&b, `<template data-resource="%d">%s</template>`, r.ID, gxbResRow(r.ID, r.Name, 0, 0))
		}
		b.WriteString(` <a href="#" class="button add_resource" onclick="var p=this.parentNode,v=p.querySelector('select').value,t=p.querySelector('template[data-resource=&quot;'+v+'&quot;]'),tb=this.closest('div').querySelector('tbody');` +
			`if(t&&!tb.querySelector('input[name=&quot;resource_id[]&quot;][value=&quot;'+v+'&quot;]'))tb.insertAdjacentHTML('beforeend',t.innerHTML);return false">Link resource</a>` +
			` <a href="/wp-admin/goocommerce?page=booking-resources">Manage resources</a>`)
	}
	b.WriteString(`</p></div>`)
	return template.HTML(b.String())
}

func gooBkSaveResources(s *Server, c *gin.Context, p *gooProduct) {
	if p.Meta == nil {
		p.Meta = map[string]any{}
	}
	bkSetMeta(p, "has_resources", c.PostForm("_wc_booking_has_resources") == "yes")
	bkSetMeta(p, "resource_label", strings.TrimSpace(c.PostForm("_wc_booking_resource_label")))
	assign := c.PostForm("_wc_booking_resources_assignment")
	if assign != "automatic" {
		assign = "customer"
	}
	bkSetMeta(p, "resources_assignment", assign)
	ids, bases, blocks := c.PostFormArray("resource_id[]"), c.PostFormArray("resource_base_cost[]"), c.PostFormArray("resource_block_cost[]")
	seen := map[int64]bool{}
	list := []any{}
	for i, v := range ids {
		id, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		at := func(l []string) string {
			if i < len(l) {
				return l[i]
			}
			return ""
		}
		list = append(list, map[string]any{"id": float64(id), "base_cost": gxAtof(at(bases)), "block_cost": gxAtof(at(blocks))})
	}
	bkSetMeta(p, "resources", list)
	bkSyncPrice(p)
}

// Bookings › Resources -------------------------------------------------------------------------------

func gxbRulesFromForm(form url.Values) []bkRule {
	types, froms, tos := form["wc_booking_availability_type[]"], form["wc_booking_availability_from[]"], form["wc_booking_availability_to[]"]
	books, prios := form["wc_booking_availability_bookable[]"], form["wc_booking_availability_priority[]"]
	var out []bkRule
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
		out = append(out, bkRule{Type: t, From: at(froms), To: at(tos), Bookable: at(books) != "no", Priority: max(gxAtoi(at(prios)), 1)})
	}
	return out
}

func (s *Server) gooBkResourcesAdmin(c *gin.Context) {
	if !s.gooExtOn(bkKey) {
		c.String(http.StatusNotFound, "The Bookings extension is not active.")
		return
	}
	switch c.Query("action") {
	case "edit":
		s.gxbResourceEdit(c)
	case "delete":
		s.gxbResourceDelete(c)
	default:
		s.gxbResourceList(c)
	}
}

func (s *Server) gxbResourceList(c *gin.Context) {
	ctx := c.Request.Context()
	uses := map[int64]int{}
	for _, p := range s.bkProducts(ctx) {
		for _, r := range bkConfOf(p).Resources {
			uses[r.ID]++
		}
	}
	var b strings.Builder
	b.WriteString(`<p>Resources are shared by bookable products: each has an available quantity per block and availability rules that override the product's rules. Link them to products in the product's Resources panel.</p>`)
	b.WriteString(`<table class="wp-list-table widefat fixed striped posts"><thead><tr><th scope="col" class="column-primary">Name</th><th scope="col">Available quantity</th><th scope="col">Availability rules</th><th scope="col">Products</th><th scope="col">Actions</th></tr></thead><tbody id="the-list">`)
	list := s.gooBkResources(ctx)
	if len(list) == 0 {
		b.WriteString(`<tr class="no-items"><td class="colspanchange" colspan="5">No resources found</td></tr>`)
	}
	nonce := s.nonce(c, "goo-booking-resources")
	for _, r := range list {
		edit := "/wp-admin/goocommerce?page=booking-resources&amp;action=edit&amp;id=" + itoa64(r.ID)
		fmt.Fprintf(&b, `<tr id="resource-%d"><td class="column-primary"><a href="%s"><strong>%s</strong></a></td><td>%d</td><td>%d</td><td>%d</td>`+
			`<td><a class="button" href="%s">Edit</a> <a class="button" href="/wp-admin/goocommerce?page=booking-resources&amp;action=delete&amp;id=%d&amp;_wpnonce=%s" onclick="return confirm('Delete this resource?')">Delete</a></td></tr>`,
			r.ID, edit, html.EscapeString(defaultStr(r.Name, "(no name)")), r.Qty, len(r.Rules), uses[r.ID], edit, r.ID, nonce)
	}
	b.WriteString(`</tbody></table>`)
	s.gxAdminPage(c, "goo-ext-bookings", "booking-resources", "Resources", bkAdminHead("resources"), template.HTML(b.String()))
}

func (s *Server) gxbResourceEdit(c *gin.Context) {
	ctx := c.Request.Context()
	r := &gooBkResource{Qty: 1}
	if id, _ := strconv.ParseInt(c.Query("id"), 10, 64); id != 0 {
		var err error
		if r, err = s.gooBkResource(ctx, id); err != nil {
			c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=booking-resources&goo-error="+url.QueryEscape("Resource not found."))
			return
		}
	}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "goo-booking-resource-edit") {
			s.denyNonce(c)
			return
		}
		r.Name = strings.TrimSpace(c.PostForm("post_title"))
		r.Qty = max(gxAtoi(c.PostForm("_wc_booking_qty")), 1)
		r.Order = gxAtoi(c.PostForm("menu_order"))
		r.Rules = gxbRulesFromForm(c.Request.PostForm)
		if r.Name == "" {
			c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=booking-resources&action=edit&id="+itoa64(r.ID)+"&goo-error="+url.QueryEscape("Please enter a name for the resource."))
			return
		}
		if err := s.gooBkSaveResource(ctx, r); err != nil {
			c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=booking-resources&goo-error="+url.QueryEscape(err.Error()))
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=booking-resources&action=edit&id="+itoa64(r.ID)+"&goo-msg="+url.QueryEscape("Resource saved."))
		return
	}
	title := "Add Resource"
	if r.ID != 0 {
		title = "Edit Resource"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<form method="post" action="/wp-admin/goocommerce?page=booking-resources&amp;action=edit&amp;id=%d"><input type="hidden" name="_wpnonce" value="%s">`, r.ID, s.nonce(c, "goo-booking-resource-edit"))
	b.WriteString(`<div id="poststuff"><div id="titlediv"><input type="text" name="post_title" id="title" size="30" value="` + html.EscapeString(r.Name) + `" placeholder="Resource name" required spellcheck="true" autocomplete="off" style="width:100%;font-size:1.5em;padding:3px 8px"></div>`)
	b.WriteString(`<div class="postbox" id="bookings_resource_data"><div class="postbox-header"><h2>Resource details</h2></div><div class="inside"><div class="woocommerce_options_panel">`)
	b.WriteString(`<div class="options_group">`)
	b.WriteString(bkField("Available Quantity", "_wc_booking_qty", bkInput("_wc_booking_qty", "number", strconv.Itoa(r.Qty), ` min="1" step="1"`), "The quantity of this resource available at any given time."))
	b.WriteString(bkField("Order", "menu_order", bkInput("menu_order", "number", strconv.Itoa(r.Order), ` step="1"`), "Resources with a lower number are listed and assigned first."))
	b.WriteString(`</div><div class="options_group wc_booking_availability"><p>Set availability rules for this resource. They override the availability rules of the products it is linked to: dates are YYYY-MM-DD, months 1–12, weeks 1–53, days 1 (Monday)–7 (Sunday), times HH:MM.</p>`)
	b.WriteString(`<table class="widefat"><thead><tr><th>Range type</th><th>From</th><th>To</th><th>Bookable</th><th>Priority</th><th></th></tr></thead><tbody>`)
	for _, rule := range r.Rules {
		b.WriteString(bkRuleRow(rule))
	}
	b.WriteString(`</tbody></table><p><template>` + bkRuleRow(bkRule{Type: "custom", Bookable: true, Priority: 10}) + `</template><a href="#" class="button add_row" ` + bkAddRowJS + `>Add Range</a></p></div>`)
	b.WriteString(`</div></div></div></div><p class="submit"><button type="submit" class="button button-primary">Save Resource</button> <a class="button" href="/wp-admin/goocommerce?page=booking-resources">Back to resources</a></p></form>`)
	s.gxAdminPage(c, "goo-ext-bookings", "booking-resources", title, bkAdminHead("resources"), template.HTML(b.String()))
}

func (s *Server) gxbResourceDelete(c *gin.Context) {
	if !s.checkNonceValue(c, c.Query("_wpnonce"), "goo-booking-resources") {
		s.denyNonce(c)
		return
	}
	id, _ := strconv.ParseInt(c.Query("id"), 10, 64)
	_, _ = s.store.Pool.Exec(c.Request.Context(), `DELETE FROM goo_booking_resources WHERE id = $1`, id)
	c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=booking-resources&goo-msg="+url.QueryEscape("Resource deleted."))
}
