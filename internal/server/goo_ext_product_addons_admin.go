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

// Product Add-Ons in the admin: the add-on fields editor (shared by the product panel and the global
// groups), the product's "Add-ons" tab and Goocommerce › Add-ons (global add-on groups).

// paoEditorRow is one add-on in the editor. Options are one per line: "Label | price | price type".
func paoEditorRow(a paoAddon) string {
	esc := html.EscapeString
	yn := func(field string, on bool) string {
		return bkSelectName(field+"[]", map[bool]string{true: "yes", false: "no"}[on], []gooOpt{{"no", "No"}, {"yes", "Yes"}})
	}
	var opts []string
	for _, o := range a.Options {
		line := o.Label + " | " + gxFmt(o.Price) + " | " + defaultStr(o.PriceType, "flat_fee")
		if o.Image != 0 {
			line += " | " + itoa64(o.Image)
		}
		opts = append(opts, line)
	}
	minV, maxV := "", ""
	if a.Min != 0 {
		minV = gxFmt(a.Min)
	}
	if a.Max != 0 {
		maxV = gxFmt(a.Max)
	}
	dmin, dmax := "", a.DateMax
	if a.DateMinDays != nil {
		dmin = strconv.Itoa(*a.DateMinDays)
	}
	return `<div class="wc-pao-addon wc-pao-addon-editor postbox" style="padding:10px 12px;margin:10px 0"><table class="form-table" style="margin:0"><tbody>` +
		`<tr><th>Type</th><td>` + bkSelectName("product_addon_type[]", a.Type, paoTypes) + ` Display as ` +
		bkSelectName("product_addon_display[]", a.Display, []gooOpt{{"select", "Dropdowns"}, {"radiobutton", "Radio Buttons"}, {"images", "Images"}}) +
		` <a href="#" class="button" style="float:right" onclick="this.closest('.wc-pao-addon').remove();return false">Remove</a></td></tr>` +
		`<tr><th>Title</th><td><input type="text" name="product_addon_name[]" value="` + esc(a.Name) + `" class="regular-text"> Format ` +
		bkSelectName("product_addon_title_format[]", a.TitleFormat, []gooOpt{{"label", "Label"}, {"heading", "Heading"}, {"hide", "Hide"}}) + `</td></tr>` +
		`<tr><th>Description</th><td>` + yn("product_addon_description_enable", a.DescEnable) + ` <input type="text" name="product_addon_description[]" value="` + esc(a.Description) + `" class="regular-text"></td></tr>` +
		`<tr><th>Required field</th><td>` + yn("product_addon_required", a.Required) + `</td></tr>` +
		`<tr><th>Options</th><td><textarea name="product_addon_options[]" rows="3" class="large-text code" placeholder="Label | price | flat_fee, quantity_based or percentage_based | image id">` + esc(strings.Join(opts, "\n")) + `</textarea>` +
		`<p class="description">Multiple choice and checkboxes: one option per line.</p></td></tr>` +
		`<tr><th>Adjust price</th><td>` + yn("product_addon_adjust_price", a.AdjustPrice) + ` ` + bkSelectName("product_addon_price_type[]", a.PriceType, paoPriceTypes) +
		` <input type="text" name="product_addon_price[]" value="` + map[bool]string{true: gxFmt(a.Price), false: ""}[a.Price != 0] + `" size="6" class="wc_input_price"> <span class="description">Text, upload, quantity and date fields.</span></td></tr>` +
		`<tr><th>Restrictions</th><td>` + yn("product_addon_restrictions", a.Restrictions) + ` ` +
		bkSelectName("product_addon_restrictions_type[]", a.RestrictionsType, []gooOpt{{"any_text", "Any Text"}, {"only_letters", "Only Letters"}, {"only_numbers", "Only Numbers"}, {"only_letters_numbers", "Only Letters and Numbers"}, {"email", "Only Email Address"}}) +
		` Min <input type="number" step="any" name="product_addon_min[]" value="` + minV + `" style="width:80px"> Max <input type="number" step="any" name="product_addon_max[]" value="` + maxV + `" style="width:80px">` +
		` <span class="description">Character limits for text, amount limits for prices and quantities.</span></td></tr>` +
		`<tr><th>Date limits</th><td>Earliest: today + <input type="number" name="product_addon_date_min_days[]" value="` + esc(dmin) + `" style="width:70px"> days ` +
		`Latest <input type="date" name="product_addon_date_max[]" value="` + esc(dmax) + `"> <span class="description">Date picker only. Leave empty for no limit; a negative number allows past dates.</span></td></tr>` +
		`<tr><th>Conditional logic</th><td>Show only if the field <input type="text" name="product_addon_condition_field[]" value="` + esc(a.ConditionField) + `" class="regular-text" placeholder="Title of another field"> has the value ` +
		`<input type="text" name="product_addon_condition_value[]" value="` + esc(a.ConditionValue) + `" class="regular-text" placeholder="Option label or text (empty: any value)"></td></tr></tbody></table></div>`
}

// paoEditor is the add-on fields editor with its "Add Field" button.
func paoEditor(addons []paoAddon) string {
	var b strings.Builder
	b.WriteString(`<div class="wc-pao-addons"><div class="wc-pao-addons-list">`)
	for _, a := range addons {
		b.WriteString(paoEditorRow(a))
	}
	b.WriteString(`</div><template>` + paoEditorRow(paoAddon{Type: "multiple_choice", Display: "select", TitleFormat: "label", PriceType: "flat_fee"}) + `</template>` +
		`<p><a href="#" class="button wc-pao-add-field" onclick="var w=this.closest('.wc-pao-addons');w.querySelector('.wc-pao-addons-list').insertAdjacentHTML('beforeend',w.querySelector('template').innerHTML);return false">Add Field</a></p></div>`)
	return b.String()
}

// paoReadEditor reads the posted add-on fields.
func paoReadEditor(form url.Values) []paoAddon {
	arr := func(k string) []string { return form["product_addon_"+k+"[]"] }
	names := arr("name")
	var out []paoAddon
	for i, n := range names {
		at := func(k string) string {
			if l := arr(k); i < len(l) {
				return strings.TrimSpace(l[i])
			}
			return ""
		}
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		a := paoAddon{Name: n, Type: defaultStr(at("type"), "multiple_choice"), Display: defaultStr(at("display"), "select"), TitleFormat: defaultStr(at("title_format"), "label"),
			DescEnable: at("description_enable") == "yes", Description: at("description"), Required: at("required") == "yes", Position: len(out),
			AdjustPrice: at("adjust_price") == "yes", PriceType: defaultStr(at("price_type"), "flat_fee"), Price: gxAtof(at("price")),
			Restrictions: at("restrictions") == "yes", RestrictionsType: defaultStr(at("restrictions_type"), "any_text"), Min: gxAtof(at("min")), Max: gxAtof(at("max"))}
		a.ConditionField, a.ConditionValue = at("condition_field"), at("condition_value")
		if v := at("date_min_days"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				a.DateMinDays = &n
			}
		}
		if v := at("date_max"); v != "" {
			if _, err := time.Parse("2006-01-02", v); err == nil {
				a.DateMax = v
			}
		}
		if a.Type == "multiple_choice" || a.Type == "checkbox" {
			for _, line := range strings.Split(at("options"), "\n") {
				parts := strings.Split(line, "|")
				label := strings.TrimSpace(parts[0])
				if label == "" {
					continue
				}
				o := paoOption{Label: label, PriceType: "flat_fee"}
				if len(parts) > 1 {
					o.Price = gxAtof(parts[1])
				}
				if len(parts) > 2 {
					if pt := strings.TrimSpace(parts[2]); pt == "quantity_based" || pt == "percentage_based" || pt == "flat_fee" {
						o.PriceType = pt
					}
				}
				if len(parts) > 3 {
					o.Image, _ = strconv.ParseInt(strings.TrimSpace(parts[3]), 10, 64)
				}
				a.Options = append(a.Options, o)
			}
		}
		out = append(out, a)
	}
	return out
}

func paoPanel(s *Server, c *gin.Context, p *gooProduct) template.HTML {
	return template.HTML(`<div class="options_group"><p class="form-field _product_addons_exclude_global_field"><label for="_product_addons_exclude_global">Exclude global add-ons</label>` +
		`<input type="checkbox" class="checkbox" name="_product_addons_exclude_global" id="_product_addons_exclude_global" value="1"` + gxChk(gxBool(p.Meta["_product_addons_exclude_global"])) + `>` +
		`<span class="description">Check this to exclude this product from all Global Add-ons.</span></p></div><div class="options_group">` + paoEditor(paoDecode(p.Meta["_product_addons"])) + `</div>`)
}

func paoPanelSave(s *Server, c *gin.Context, p *gooProduct) {
	if p.Meta == nil {
		p.Meta = map[string]any{}
	}
	p.Meta["_product_addons_exclude_global"] = c.PostForm("_product_addons_exclude_global") != ""
	p.Meta["_product_addons"] = gxRoundTrip(paoReadEditor(c.Request.PostForm))
}

// Goocommerce › Add-ons ----------------------------------------------------------------------------------

func (s *Server) paoAdmin(c *gin.Context) {
	if !s.gooExtOn(paoKey) {
		c.String(http.StatusNotFound, "The Product Add-Ons extension is not active.")
		return
	}
	ctx := c.Request.Context()
	head := template.HTML(`<h1 class="wp-heading-inline">Add-ons</h1> <a href="/wp-admin/goocommerce?page=product-addons&amp;edit=0" class="page-title-action">Create New</a><hr class="wp-header-end">`)
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "goo-addon-groups") {
			s.denyNonce(c)
			return
		}
		if del, _ := strconv.ParseInt(c.PostForm("delete"), 10, 64); del != 0 {
			_, _ = s.store.Pool.Exec(ctx, `DELETE FROM goo_addon_groups WHERE id = $1`, del)
			s.purgeAll(ctx)
			c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=product-addons&goo-msg="+url.QueryEscape("Add-on deleted successfully"))
			return
		}
		g := &paoGroup{Name: strings.TrimSpace(c.PostForm("addon-reference")), Priority: gxAtoi(defaultStr(c.PostForm("addon-priority"), "10"))}
		g.ID, _ = strconv.ParseInt(c.PostForm("edit_id"), 10, 64)
		for _, v := range c.PostFormArray("addon-objects[]") {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
				g.Categories = append(g.Categories, id)
			}
		}
		g.AllProducts = c.PostForm("addon-all-products") != "" || len(g.Categories) == 0
		if g.AllProducts {
			g.Categories = nil
		}
		g.Fields = paoReadEditor(c.Request.PostForm)
		if g.Name == "" {
			g.Name = "Add-ons Group"
		}
		if err := s.paoSaveGroup(ctx, g); err != nil {
			c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=product-addons&goo-error="+url.QueryEscape(err.Error()))
			return
		}
		s.purgeAll(ctx)
		c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=product-addons&edit="+itoa64(g.ID)+"&goo-msg="+url.QueryEscape("Add-on saved successfully"))
		return
	}
	nonce := s.nonce(c, "goo-addon-groups")
	cats, _ := s.store.HierarchicalTerms(ctx, "product_cat", false)
	catName := map[int64]string{}
	for _, t := range cats {
		catName[t.ID] = t.Name
	}
	if e := c.Query("edit"); e != "" {
		id, _ := strconv.ParseInt(e, 10, 64)
		g := &paoGroup{Priority: 10, AllProducts: true}
		if id != 0 {
			for _, x := range s.paoGroups(ctx) {
				if x.ID == id {
					g = x
				}
			}
		}
		have := map[int64]bool{}
		for _, cid := range g.Categories {
			have[cid] = true
		}
		var b strings.Builder
		fmt.Fprintf(&b, `<form method="post" action="/wp-admin/goocommerce?page=product-addons"><input type="hidden" name="_wpnonce" value="%s"><input type="hidden" name="edit_id" value="%d">`, nonce, g.ID)
		b.WriteString(`<table class="form-table"><tbody>`)
		fmt.Fprintf(&b, `<tr><th><label for="addon-reference">Name</label></th><td><input type="text" name="addon-reference" id="addon-reference" value="%s" class="regular-text"><p class="description">This name is for your reference only and will not be visible to customers.</p></td></tr>`, html.EscapeString(g.Name))
		fmt.Fprintf(&b, `<tr><th><label for="addon-priority">Priority</label></th><td><input type="number" name="addon-priority" id="addon-priority" value="%d"><p class="description">This determines the order when there are multiple add-ons for the same product. Lower numbers display first.</p></td></tr>`, g.Priority)
		b.WriteString(`<tr><th><label for="addon-objects">Product Categories</label></th><td><label><input type="checkbox" name="addon-all-products" value="1"` + gxChk(g.AllProducts) + `> All Products</label><br><select name="addon-objects[]" id="addon-objects" multiple size="6" style="min-width:260px">`)
		for _, t := range cats {
			fmt.Fprintf(&b, `<option value="%d"%s>%s</option>`, t.ID, map[bool]string{true: " selected", false: ""}[have[t.ID]], html.EscapeString(t.Name))
		}
		b.WriteString(`</select><p class="description">Select which categories this add-on should apply to. Create add-ons for a single product when editing that product.</p></td></tr></tbody></table>`)
		b.WriteString(`<h2>Add-ons</h2>` + paoEditor(g.Fields) + `<p class="submit"><button type="submit" class="button button-primary">Save Global Add-on</button></p></form>`)
		s.gxAdminPage(c, "goo-ext-product-addons", "product-addons", "Add-ons", head, template.HTML(b.String()))
		return
	}
	var b strings.Builder
	b.WriteString(`<p>Global add-ons apply to all products, or products in the chosen categories. Products can also have their own add-ons, set in their Add-ons tab.</p>`)
	b.WriteString(`<table class="wp-list-table widefat fixed striped"><thead><tr><th scope="col" class="column-primary">Name</th><th scope="col">Priority</th><th scope="col">Product Categories</th><th scope="col">Number of Fields</th><th scope="col"></th></tr></thead><tbody>`)
	groups := s.paoGroups(ctx)
	if len(groups) == 0 {
		b.WriteString(`<tr class="no-items"><td colspan="5">No global add-ons exists yet.</td></tr>`)
	}
	for _, g := range groups {
		applies := "All Products"
		if !g.AllProducts {
			var names []string
			for _, cid := range g.Categories {
				names = append(names, defaultStr(catName[cid], "#"+itoa64(cid)))
			}
			applies = strings.Join(names, ", ")
		}
		fmt.Fprintf(&b, `<tr><td class="column-primary"><a href="/wp-admin/goocommerce?page=product-addons&amp;edit=%d"><strong>%s</strong></a></td><td>%d</td><td>%s</td><td>%d</td>`+
			`<td><a class="button" href="/wp-admin/goocommerce?page=product-addons&amp;edit=%d">Edit</a> <form method="post" action="/wp-admin/goocommerce?page=product-addons" style="display:inline"><input type="hidden" name="_wpnonce" value="%s"><input type="hidden" name="delete" value="%d"><button type="submit" class="button-link delete">Delete</button></form></td></tr>`,
			g.ID, html.EscapeString(g.Name), g.Priority, html.EscapeString(applies), len(g.Fields), g.ID, nonce, g.ID)
	}
	b.WriteString(`</tbody></table>`)
	s.gxAdminPage(c, "goo-ext-product-addons", "product-addons", "Add-ons", head, template.HTML(b.String()))
}

// paoAddonsCount is the number of add-ons a product shows (for tests and the admin).
func (s *Server) paoAddonsCount(ctx context.Context, p *gooProduct) int {
	return len(s.paoAddonsFor(ctx, p))
}
