package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
)

// Product Add-Ons (WooCommerce Product Add-Ons): extra fields on the product page — multiple choice,
// checkboxes, text, customer defined price, quantity, file upload, date picker, headings — whose
// options add flat fees, per-quantity or percentage prices to the cart item. Add-ons come from global
// groups (all products or product categories, by priority) and from the product itself.

const paoKey = "product_addons"

func init() {
	gooRegisterExtension(&gooExtension{
		Key: paoKey, Name: "Product Add-Ons", Icon: "dashicons-forms", Category: "Merchandising",
		Description: "Let customers personalize products with paid or free options: gift wrapping, engravings, special messages, uploads and more.",
		Fields: []gooField{
			{Type: "title", Title: "Product Add-Ons"},
			{ID: "show_totals", Title: "Totals", Type: "checkbox", Default: true, Desc: "Show the &quot;Options total&quot; and &quot;Grand total&quot; box on the product page."},
			{ID: "max_upload_mb", Title: "Maximum upload size (MB)", Type: "number", Default: 5, Min: "1"},
			{ID: "allowed_types", Title: "Allowed file types", Type: "text", Default: "jpg,jpeg,png,gif,webp,pdf,txt,doc,docx", Desc: "Comma-separated file extensions accepted by file upload add-ons."},
			{Type: "sectionend"},
		},
		Pages: []gooExtMenuItem{{Page: "product-addons", Title: "Add-ons"}},
	})
	gooOnHTML("single_before_add_to_cart", paoProductFields)
	gooAddToCartHooks = append(gooAddToCartHooks, paoAddToCart)
	gooCartItemPriceHooks = append(gooCartItemPriceHooks, paoCartPrice)
	gooCartItemDataHooks = append(gooCartItemDataHooks, paoItemData)
	gooFrontRoutes = append(gooFrontRoutes, paoFrontRoute)
	gooProductPanels = append(gooProductPanels, &gooProductPanel{ID: "product_addons", Label: "Add-ons", Module: paoKey, Render: paoPanel, Save: paoPanelSave})
	gooExtAdminPages["product-addons"] = func(s *Server, c *gin.Context) { s.paoAdmin(c) }
}

type paoOption struct {
	Label     string  `json:"label"`
	Price     float64 `json:"price"`
	PriceType string  `json:"price_type"`
	Image     int64   `json:"image,omitempty"`
}

// paoAddon is an add-on field (the product_addons meta format).
type paoAddon struct {
	Name             string      `json:"name"`
	TitleFormat      string      `json:"title_format"` // label, heading, hide
	DescEnable       bool        `json:"description_enable"`
	Description      string      `json:"description"`
	Type             string      `json:"type"`    // multiple_choice, checkbox, custom_text, custom_textarea, file_upload, custom_price, input_multiplier, heading, datepicker
	Display          string      `json:"display"` // select, radiobutton, images
	Position         int         `json:"position"`
	Required         bool        `json:"required"`
	Restrictions     bool        `json:"restrictions"`
	RestrictionsType string      `json:"restrictions_type"` // any_text, only_letters, only_numbers, only_letters_numbers, email
	AdjustPrice      bool        `json:"adjust_price"`
	PriceType        string      `json:"price_type"` // flat_fee, quantity_based, percentage_based
	Price            float64     `json:"price"`
	Min              float64     `json:"min"`
	Max              float64     `json:"max"`
	Options          []paoOption `json:"options"`
}

var paoTypes = []gooOpt{{"multiple_choice", "Multiple Choice"}, {"checkbox", "Checkboxes"}, {"custom_text", "Short Text"}, {"custom_textarea", "Long Text"},
	{"file_upload", "File Upload"}, {"custom_price", "Customer Defined Price"}, {"input_multiplier", "Quantity"}, {"datepicker", "Date Picker"}, {"heading", "Heading"}}
var paoPriceTypes = []gooOpt{{"flat_fee", "Flat Fee"}, {"quantity_based", "Quantity Based"}, {"percentage_based", "Percentage Based"}}

func paoDecode(v any) []paoAddon {
	raw, _ := json.Marshal(v)
	var out []paoAddon
	_ = json.Unmarshal(raw, &out)
	return out
}

func paoSlug(v string) string {
	v = gooSanitizeKey(strings.ToLower(v))
	if v == "" {
		v = "addon"
	}
	return v
}

// paoGroup is a global add-on group.
type paoGroup struct {
	ID          int64
	Name        string
	Priority    int
	AllProducts bool
	Categories  []int64
	Fields      []paoAddon
}

func (s *Server) paoGroups(ctx context.Context) []*paoGroup {
	rows, err := s.store.Pool.Query(ctx, `SELECT id, name, priority, all_products, categories, fields FROM goo_addon_groups ORDER BY priority, id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*paoGroup
	for rows.Next() {
		g := &paoGroup{}
		var fields []byte
		if rows.Scan(&g.ID, &g.Name, &g.Priority, &g.AllProducts, &g.Categories, &fields) == nil {
			_ = json.Unmarshal(fields, &g.Fields)
			out = append(out, g)
		}
	}
	return out
}

func (s *Server) paoSaveGroup(ctx context.Context, g *paoGroup) error {
	fields, _ := json.Marshal(g.Fields)
	if g.Categories == nil {
		g.Categories = []int64{}
	}
	if g.ID == 0 {
		return s.store.Pool.QueryRow(ctx, `INSERT INTO goo_addon_groups (name, priority, all_products, categories, fields) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			g.Name, g.Priority, g.AllProducts, g.Categories, fields).Scan(&g.ID)
	}
	_, err := s.store.Pool.Exec(ctx, `UPDATE goo_addon_groups SET name=$2, priority=$3, all_products=$4, categories=$5, fields=$6, date_modified=now() WHERE id=$1`,
		g.ID, g.Name, g.Priority, g.AllProducts, g.Categories, fields)
	return err
}

// paoAddonsFor lists a product's add-ons: the matching global groups by priority and the product's
// own (priority 10), unless the product excludes the global ones.
func (s *Server) paoAddonsFor(ctx context.Context, p *gooProduct) []paoAddon {
	owner := p
	if p.Type == gooTypeVariation {
		if parent := s.gooParent(ctx, p); parent != nil {
			owner = parent
		}
	}
	type pri struct {
		a paoAddon
		p int
	}
	var all []pri
	if !gxBool(owner.Meta["_product_addons_exclude_global"]) {
		var cats map[int64]bool
		for _, g := range s.paoGroups(ctx) {
			ok := g.AllProducts
			if !ok {
				if cats == nil {
					cats = map[int64]bool{}
					if m, err := s.store.TermsForPosts(ctx, []int64{owner.ID}, "product_cat"); err == nil {
						for _, t := range m[owner.ID] {
							cats[t.ID] = true
						}
					}
				}
				for _, cid := range g.Categories {
					if cats[cid] {
						ok = true
					}
				}
			}
			if ok {
				for _, a := range g.Fields {
					all = append(all, pri{a, g.Priority})
				}
			}
		}
	}
	for _, a := range paoDecode(owner.Meta["_product_addons"]) {
		all = append(all, pri{a, 10})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].p < all[j].p })
	out := make([]paoAddon, len(all))
	for i, x := range all {
		out[i] = x.a
	}
	return out
}

// paoFieldName is the add-on's input name ("addon-<product>-<name>-<position>").
func paoFieldName(productID int64, a paoAddon, i int) string {
	return fmt.Sprintf("addon-%d-%s-%d", productID, paoSlug(a.Name), i)
}

func paoOptionValue(o paoOption, i int) string { return fmt.Sprintf("%s-%d", paoSlug(o.Label), i+1) }

// paoOptionPrice is an option's price for display ("(+$5.00)" / "(+10%)").
func (s *Server) paoPriceLabel(price float64, typ string) string {
	if price == 0 {
		return ""
	}
	sign := "+"
	if price < 0 {
		sign = "-"
	}
	if typ == "percentage_based" {
		return fmt.Sprintf("(%s%s%%)", sign, gxFmt(paoAbs(price)))
	}
	return "(" + sign + s.gooPricePlain(paoAbs(price), s.gooCurrency()) + ")"
}

func paoAbs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// Front ------------------------------------------------------------------------------------------------

// paoProductFields prints the add-on fields inside the add-to-cart form, with the totals box.
func paoProductFields(s *Server, c *gin.Context, p *gooProduct) template.HTML {
	if !s.gooExtOn(paoKey) {
		return ""
	}
	ctx := c.Request.Context()
	addons := s.paoAddonsFor(ctx, p)
	if len(addons) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="wc-pao-addons-container">`)
	for i, a := range addons {
		name := paoFieldName(p.ID, a, i)
		esc := html.EscapeString
		fmt.Fprintf(&b, `<div class="wc-pao-addon-container wc-pao-addon wc-pao-addon-%s%s" data-product-name="%s">`, esc(paoSlug(a.Name)), map[bool]string{true: " wc-pao-required-addon", false: ""}[a.Required], esc(p.Name))
		if a.Type == "heading" {
			fmt.Fprintf(&b, `<h2 class="wc-pao-addon-heading">%s</h2>`, esc(a.Name))
			if a.DescEnable && a.Description != "" {
				b.WriteString(`<div class="wc-pao-addon-description">` + esc(a.Description) + `</div>`)
			}
			b.WriteString(`</div>`)
			continue
		}
		title := esc(a.Name)
		if a.AdjustPrice && a.Type != "multiple_choice" && a.Type != "checkbox" {
			if l := s.paoPriceLabel(a.Price, a.PriceType); l != "" {
				title += ` <span class="wc-pao-addon-price">` + esc(l) + `</span>`
			}
		}
		if a.Required {
			title += ` <em class="required" title="Required field">*</em>`
		}
		switch a.TitleFormat {
		case "heading":
			b.WriteString(`<h2 class="wc-pao-addon-name">` + title + `</h2>`)
		case "hide":
		default:
			fmt.Fprintf(&b, `<label for="%s" class="wc-pao-addon-name" data-addon-name="%s" data-has-per-person-pricing="" data-has-per-block-pricing="">%s</label>`, esc(name), esc(a.Name), title)
		}
		if a.DescEnable && a.Description != "" {
			b.WriteString(`<div class="wc-pao-addon-description">` + esc(a.Description) + `</div>`)
		}
		req := map[bool]string{true: " required", false: ""}[a.Required]
		data := func(price float64, typ string) string {
			return fmt.Sprintf(` data-price="%s" data-price-type="%s" data-label="%s"`, gxFmt(price), esc(defaultStr(typ, "flat_fee")), esc(a.Name))
		}
		b.WriteString(`<p class="form-row form-row-wide wc-pao-addon-wrap wc-pao-addon-` + esc(paoSlug(a.Name)) + `">`)
		switch a.Type {
		case "multiple_choice":
			switch a.Display {
			case "radiobutton", "images":
				for oi, o := range a.Options {
					img := ""
					if a.Display == "images" && o.Image != 0 {
						if att, err := s.store.GetAttachment(ctx, o.Image); err == nil {
							img = string(s.attachmentImage(ctx, att, "thumbnail", "wc-pao-addon-image-swatch"))
						}
					}
					fmt.Fprintf(&b, `<label class="wc-pao-addon-option"><input type="radio" class="wc-pao-addon-field wc-pao-addon-radio" name="%s" value="%s"%s%s> %s%s %s</label><br>`,
						esc(name), esc(paoOptionValue(o, oi)), data(o.Price, o.PriceType), req, img, esc(o.Label), esc(s.paoPriceLabel(o.Price, o.PriceType)))
				}
			default:
				fmt.Fprintf(&b, `<select class="wc-pao-addon-field wc-pao-addon-select" name="%s" id="%s"%s><option value="">%s</option>`, esc(name), esc(name), req,
					map[bool]string{true: "Select an option...", false: "None"}[a.Required])
				for oi, o := range a.Options {
					fmt.Fprintf(&b, `<option value="%s"%s>%s %s</option>`, esc(paoOptionValue(o, oi)), data(o.Price, o.PriceType), esc(o.Label), esc(s.paoPriceLabel(o.Price, o.PriceType)))
				}
				b.WriteString(`</select>`)
			}
		case "checkbox":
			for oi, o := range a.Options {
				fmt.Fprintf(&b, `<label class="wc-pao-addon-option"><input type="checkbox" class="wc-pao-addon-field wc-pao-addon-checkbox" name="%s[]" value="%s"%s> %s %s</label><br>`,
					esc(name), esc(paoOptionValue(o, oi)), data(o.Price, o.PriceType), esc(o.Label), esc(s.paoPriceLabel(o.Price, o.PriceType)))
			}
		case "custom_text":
			attrs := ""
			if a.Restrictions && a.Max > 0 {
				attrs += fmt.Sprintf(` maxlength="%d"`, int(a.Max))
			}
			if a.RestrictionsType == "email" {
				attrs += ` type-email`
			}
			fmt.Fprintf(&b, `<input type="text" class="input-text wc-pao-addon-field wc-pao-addon-custom-text" name="%s" id="%s"%s%s%s>`, esc(name), esc(name), data(paoAdjusted(a), a.PriceType), req, attrs)
		case "custom_textarea":
			attrs := ""
			if a.Restrictions && a.Max > 0 {
				attrs += fmt.Sprintf(` maxlength="%d"`, int(a.Max))
			}
			fmt.Fprintf(&b, `<textarea class="input-text wc-pao-addon-field wc-pao-addon-custom-textarea" rows="4" cols="20" name="%s" id="%s"%s%s%s></textarea>`, esc(name), esc(name), data(paoAdjusted(a), a.PriceType), req, attrs)
		case "file_upload":
			// The file input posts with the form (multipart) when scripts are off; the script uploads it
			// right away instead and keeps only the hidden field's URL.
			fmt.Fprintf(&b, `<input type="file" name="%s" class="wc-pao-addon-file-upload input-text wc-pao-addon-field" data-target="%s" data-upload="%s"%s%s> <small>(max file size %d MB)</small><input type="hidden" name="%s" value="">`,
				esc(name), esc(name), esc(s.links(c).HomeURL("/")+"?goo-ajax=addon_upload"), data(paoAdjusted(a), a.PriceType), req, max(s.gooExtInt(paoKey, "max_upload_mb"), 1), esc(name))
		case "custom_price":
			attrs := ` min="0"`
			if a.Restrictions && a.Min > 0 {
				attrs = ` min="` + gxFmt(a.Min) + `"`
			}
			if a.Restrictions && a.Max > 0 {
				attrs += ` max="` + gxFmt(a.Max) + `"`
			}
			fmt.Fprintf(&b, `<input type="number" step="any" class="input-text wc-pao-addon-field wc-pao-addon-custom-price" name="%s" id="%s" data-price-type="quantity_based" data-custom-price="1"%s%s>`, esc(name), esc(name), attrs, req)
		case "input_multiplier":
			attrs := ` min="0"`
			if a.Restrictions && a.Min > 0 {
				attrs = ` min="` + gxFmt(a.Min) + `"`
			}
			if a.Restrictions && a.Max > 0 {
				attrs += ` max="` + gxFmt(a.Max) + `"`
			}
			fmt.Fprintf(&b, `<input type="number" step="1" class="input-text wc-pao-addon-field wc-pao-addon-input-multiplier" name="%s" id="%s" data-multiplier="1"%s%s%s>`, esc(name), esc(name), data(paoAdjusted(a), a.PriceType), attrs, req)
		case "datepicker":
			fmt.Fprintf(&b, `<input type="date" class="input-text wc-pao-addon-field wc-pao-addon-datepicker" name="%s" id="%s"%s%s>`, esc(name), esc(name), data(paoAdjusted(a), a.PriceType), req)
		}
		b.WriteString(`</p></div>`)
	}
	if s.gooExtBool(paoKey, "show_totals") {
		fmt.Fprintf(&b, `<div id="product-addons-total" data-price="%s" data-show-sub-total="1" data-currency="%s" data-decimals="%d"></div>`,
			gxFmt(s.gooDisplayPrice(ctx, p)), html.EscapeString(s.gooPricePlain(0, s.gooCurrency())), s.gooDecimals())
	}
	b.WriteString(`</div>`)
	b.WriteString(paoJS)
	return template.HTML(b.String())
}

// paoAdjusted is the price of a field with "adjust price" on, else 0.
func paoAdjusted(a paoAddon) float64 {
	if a.AdjustPrice {
		return a.Price
	}
	return 0
}

// paoJS computes "Options total" and "Grand total" and uploads files (/?goo-ajax=addon_upload).
const paoJS = `<script>(function(){var box=document.currentScript.previousElementSibling;if(!box||box.dataset.paoInit)return;box.dataset.paoInit=1;
var form=box.closest('form')||document,tot=box.querySelector('#product-addons-total');
function money(v){var t=tot?tot.dataset.currency:'';var n=v.toFixed(tot?+tot.dataset.decimals:2);return t.replace(/[0-9.,]+/,n)}
function calc(){if(!tot)return;var base=+tot.dataset.price,qty=+((form.querySelector('input.qty')||{}).value||1),opt=0;
box.querySelectorAll('.wc-pao-addon-field').forEach(function(f){var el=f,price=0,type;
if(f.tagName==='SELECT'){el=f.options[f.selectedIndex];if(!f.value)return}else if((f.type==='checkbox'||f.type==='radio')&&!f.checked)return;
else if(f.type!=='checkbox'&&f.type!=='radio'&&!f.value)return;
if(f.dataset.customPrice){opt+=(+f.value||0)*qty;return}
price=+(el.dataset.price||0);type=el.dataset.priceType||f.dataset.priceType;if(f.dataset.multiplier)price*=(+f.value||0);
if(type==='percentage_based')opt+=base*price/100*qty;else if(type==='flat_fee')opt+=price;else opt+=price*qty});
tot.innerHTML=opt?'<div class="product-addon-totals"><ul><li><div class="wc-pao-col1"><strong>Options total</strong></div><div class="wc-pao-col2"><strong>'+money(opt)+'</strong></div></li><li class="wc-pao-subtotal-line"><div class="wc-pao-col1"><strong>Grand total</strong></div><div class="wc-pao-col2"><strong class="price amount">'+money(base*qty+opt)+'</strong></div></li></ul></div>':''}
box.querySelectorAll('input[type=file].wc-pao-addon-file-upload').forEach(function(f){f.addEventListener('change',function(){if(!f.files.length)return;var d=new FormData();
d.append('file',f.files[0]);d.append('goo_module','product_addons');fetch(f.dataset.upload,{method:'POST',body:d,credentials:'same-origin'}).then(function(r){return r.json()}).then(function(r){
var h=form.querySelector('input[type=hidden][name="'+f.dataset.target+'"]');if(r.url){h.value=r.url;h.dataset.price=f.dataset.price;f.removeAttribute('name');calc()}else{alert(r.error||'Upload failed');f.value=''}})})});
form.addEventListener('change',calc);form.addEventListener('input',calc);calc()})();</script>`

// paoValues reads the posted values of a field.
func paoValues(form map[string][]string, name string, multi bool) []string {
	var out []string
	keys := []string{name}
	if multi {
		keys = []string{name + "[]", name}
	}
	for _, k := range keys {
		for _, v := range form[k] {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

var (
	paoLettersRe = regexp.MustCompile(`^[\p{L}\s]*$`)
	paoNumbersRe = regexp.MustCompile(`^[0-9\s]*$`)
	paoAlnumRe   = regexp.MustCompile(`^[\p{L}0-9\s]*$`)
)

// paoAddToCart validates the posted add-ons and stores the chosen ones ("addons") in the item data.
func paoAddToCart(s *Server, ctx context.Context, p *gooProduct, qty int, form map[string][]string, data map[string]any) error {
	if !s.gooExtOn(paoKey) {
		return nil
	}
	addons := s.paoAddonsFor(ctx, p)
	if len(addons) == 0 {
		return nil
	}
	if form == nil {
		form = map[string][]string{}
	}
	form, err := s.paoFormUploadsMpf(ctx, p, addons, form)
	if err != nil {
		return err
	}
	var chosen []any
	add := func(a paoAddon, value, display string, price float64, priceType string) {
		chosen = append(chosen, map[string]any{"name": a.Name, "value": value, "display": display, "price": price, "price_type": defaultStr(priceType, "flat_fee"), "field_type": a.Type})
	}
	for i, a := range addons {
		name := paoFieldName(p.ID, a, i)
		label := html.EscapeString(a.Name)
		vals := paoValues(form, name, a.Type == "checkbox")
		if a.Type == "heading" {
			continue
		}
		if a.Required && len(vals) == 0 {
			return gooCartErr(`&quot;%s&quot; is a required field.`, label)
		}
		if len(vals) == 0 {
			continue
		}
		v := vals[0]
		switch a.Type {
		case "multiple_choice", "checkbox":
			for _, val := range vals {
				found := false
				for oi, o := range a.Options {
					if paoOptionValue(o, oi) == val {
						add(a, val, o.Label, o.Price, o.PriceType)
						found = true
					}
				}
				if !found {
					return gooCartErr(`Invalid value posted for &quot;%s&quot;.`, label)
				}
				if a.Type == "multiple_choice" {
					break
				}
			}
		case "custom_text", "custom_textarea":
			n := len([]rune(v))
			if a.Restrictions && a.Min > 0 && n < int(a.Min) {
				return gooCartErr(`The minimum allowed length for &quot;%s&quot; is %d.`, label, int(a.Min))
			}
			if a.Restrictions && a.Max > 0 && n > int(a.Max) {
				return gooCartErr(`The maximum allowed length for &quot;%s&quot; is %d.`, label, int(a.Max))
			}
			if a.Type == "custom_text" {
				switch a.RestrictionsType {
				case "only_letters":
					if !paoLettersRe.MatchString(v) {
						return gooCartErr(`Only letters are allowed for &quot;%s&quot;.`, label)
					}
				case "only_numbers":
					if !paoNumbersRe.MatchString(v) {
						return gooCartErr(`Only numbers are allowed for &quot;%s&quot;.`, label)
					}
				case "only_letters_numbers":
					if !paoAlnumRe.MatchString(v) {
						return gooCartErr(`Only letters and numbers are allowed for &quot;%s&quot;.`, label)
					}
				case "email":
					if _, err := mail.ParseAddress(v); err != nil || !strings.Contains(v, "@") {
						return gooCartErr(`A valid email address is required for &quot;%s&quot;.`, label)
					}
				}
			}
			add(a, v, v, paoAdjusted(a), a.PriceType)
		case "custom_price":
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 {
				return gooCartErr(`Please enter a valid amount for &quot;%s&quot;.`, label)
			}
			if a.Restrictions && a.Min > 0 && f < a.Min {
				return gooCartErr(`The minimum allowed amount for &quot;%s&quot; is %s.`, label, gxFmt(a.Min))
			}
			if a.Restrictions && a.Max > 0 && f > a.Max {
				return gooCartErr(`The maximum allowed amount for &quot;%s&quot; is %s.`, label, gxFmt(a.Max))
			}
			if f == 0 {
				continue
			}
			add(a, v, s.gooPricePlain(f, s.gooCurrency()), f, "quantity_based")
		case "input_multiplier":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return gooCartErr(`Please enter a valid number for &quot;%s&quot;.`, label)
			}
			if a.Restrictions && a.Min > 0 && float64(n) < a.Min {
				return gooCartErr(`The minimum allowed value for &quot;%s&quot; is %s.`, label, gxFmt(a.Min))
			}
			if a.Restrictions && a.Max > 0 && float64(n) > a.Max {
				return gooCartErr(`The maximum allowed value for &quot;%s&quot; is %s.`, label, gxFmt(a.Max))
			}
			if n == 0 {
				continue
			}
			add(a, v, v, paoAdjusted(a)*float64(n), a.PriceType)
		case "datepicker":
			if _, err := time.Parse("2006-01-02", v); err != nil {
				return gooCartErr(`Please enter a valid date for &quot;%s&quot;.`, label)
			}
			add(a, v, v, paoAdjusted(a), a.PriceType)
		case "file_upload":
			if !strings.Contains(v, "/wp-content/uploads/goocommerce_uploads/") || strings.Contains(v, "..") {
				return gooCartErr(`The file for &quot;%s&quot; could not be found. Please upload it again.`, label)
			}
			add(a, v, filepath.Base(v), paoAdjusted(a), a.PriceType)
		}
	}
	if len(chosen) > 0 {
		data["addons"] = chosen
	}
	return nil
}

func paoItemAddons(it *gooCartItem) []map[string]any { return gxList(it.Data["addons"]) }

// paoCartPrice adds the add-on prices: flat fees once per line (spread over the quantity), quantity
// based per unit, percentages of the product's price.
func paoCartPrice(s *Server, ctx context.Context, cart *gooCart, it *gooCartItem) {
	if !s.gooExtOn(paoKey) {
		return
	}
	list := paoItemAddons(it)
	if len(list) == 0 || it.Quantity <= 0 {
		return
	}
	base := it.Price
	extra := 0.0
	for _, a := range list {
		price := gxNum(a["price"])
		switch gxStr(a["price_type"]) {
		case "flat_fee":
			extra += price / float64(it.Quantity)
		case "percentage_based":
			extra += base * price / 100
		default:
			extra += price
		}
	}
	it.Price = base + extra
}

// paoItemData shows "Gift wrap (+$5.00): Yes" under the item.
func paoItemData(s *Server, ctx context.Context, it *gooCartItem) []gooKV {
	if !s.gooExtOn(paoKey) {
		return nil
	}
	var out []gooKV
	for _, a := range paoItemAddons(it) {
		key := gxStr(a["name"])
		if l := s.paoPriceLabel(gxNum(a["price"]), gxStr(a["price_type"])); l != "" && gxStr(a["field_type"]) != "custom_price" {
			key += " " + l
		}
		out = append(out, gooKV{key, gxStr(a["display"])})
	}
	return out
}

// File uploads -------------------------------------------------------------------------------------------

// paoFrontRoute serves /?goo-ajax=addon_upload (POST, multipart "file").
func paoFrontRoute(s *Server, c *gin.Context, path string) bool {
	if !s.gooExtOn(paoKey) || c.Query("goo-ajax") != "addon_upload" {
		return false
	}
	if c.Request.Method != http.MethodPost {
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "Invalid request."})
		return true
	}
	url, name, err := s.paoStoreUpload(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return true
	}
	c.JSON(http.StatusOK, gin.H{"url": url, "name": name})
	return true
}

// paoStoreUpload saves an uploaded add-on file under uploads/goocommerce_uploads/<random>/.
func (s *Server) paoStoreUpload(c *gin.Context) (string, string, error) {
	limit := int64(max(s.gooExtInt(paoKey, "max_upload_mb"), 1)) << 20
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit+1<<20)
	fh, err := c.FormFile("file")
	if err != nil {
		return "", "", fmt.Errorf("No file was uploaded.")
	}
	return s.paoSaveUploadMpf(c, fh)
}

// paoFormUploadsMpf stores the files of file upload add-ons posted with the add-to-cart form itself
// (multipart, no script) and puts their URLs in the form, as the AJAX upload would have. A field that
// already holds an uploaded file's URL keeps it.
func (s *Server) paoFormUploadsMpf(ctx context.Context, p *gooProduct, addons []paoAddon, form map[string][]string) (map[string][]string, error) {
	cart := gooCartFromCtx(ctx)
	if cart == nil || cart.c == nil || cart.c.Request == nil || cart.c.Request.MultipartForm == nil {
		return form, nil
	}
	files := cart.c.Request.MultipartForm.File
	var out map[string][]string
	for i, a := range addons {
		if a.Type != "file_upload" {
			continue
		}
		name := paoFieldName(p.ID, a, i)
		if len(paoValues(form, name, false)) > 0 || len(files[name]) == 0 || files[name][0].Filename == "" {
			continue
		}
		url, _, err := s.paoSaveUploadMpf(cart.c, files[name][0])
		if err != nil {
			return form, gooCartErr("&quot;%s&quot;: %s", html.EscapeString(a.Name), html.EscapeString(err.Error()))
		}
		if out == nil {
			out = make(map[string][]string, len(form)+1)
			for k, v := range form {
				out[k] = v
			}
		}
		out[name] = []string{url}
	}
	if out == nil {
		return form, nil
	}
	return out, nil
}

// paoSaveUploadMpf validates an uploaded file (size, allowed extension) and saves it under
// uploads/goocommerce_uploads/<random>/: its URL and clean name.
func (s *Server) paoSaveUploadMpf(c *gin.Context, fh *multipart.FileHeader) (string, string, error) {
	limit := int64(max(s.gooExtInt(paoKey, "max_upload_mb"), 1)) << 20
	if fh.Size > limit {
		return "", "", fmt.Errorf("The file is too large (maximum %d MB).", limit>>20)
	}
	base := filepath.Base(fh.Filename)
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(base), "."))
	allowed := false
	for _, a := range strings.Split(s.gooExtString(paoKey, "allowed_types"), ",") {
		if strings.EqualFold(strings.TrimSpace(a), ext) && ext != "" {
			allowed = true
		}
	}
	if !allowed {
		return "", "", fmt.Errorf("This file type is not allowed.")
	}
	clean := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, strings.TrimSuffix(base, filepath.Ext(base)))
	if clean == "" {
		clean = "file"
	}
	rnd := make([]byte, 16)
	_, _ = rand.Read(rnd)
	dirName := hex.EncodeToString(rnd)
	rel := filepath.ToSlash(filepath.Join("goocommerce_uploads", dirName, clean+"."+ext))
	dst := filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", "", err
	}
	src, err := fh.Open()
	if err != nil {
		return "", "", err
	}
	defer src.Close()
	out, err := os.Create(dst)
	if err != nil {
		return "", "", err
	}
	if _, err := io.Copy(out, io.LimitReader(src, limit)); err != nil {
		out.Close()
		return "", "", err
	}
	out.Close()
	return s.uploadURL(c, rel), clean + "." + ext, nil
}
