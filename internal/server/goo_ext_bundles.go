package server

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Product Bundles (WooCommerce Product Bundles): a "bundle" product made of bundled items, each with
// a default / min / max quantity, optional or not, priced individually (with a discount) or included
// in the bundle's own price. In the cart a bundle is a container line plus one child line per chosen
// item (Data["bundled_by"] = the container's key); children follow the container's quantity and
// disappear with it. Children reduce their own stock as ordinary lines of the order.

const (
	bnKey  = "bundles"
	bnType = "bundle"
)

func init() {
	gooRegisterExtension(&gooExtension{
		Key: bnKey, Name: "Product Bundles", Icon: "dashicons-screenoptions", Category: "Merchandising",
		Description: "Offer personalized product bundles, bulk discount packages and assembled products: bundled items with quantities, optional items and discounts.",
		Fields: []gooField{
			{Type: "title", Title: "Product Bundles"},
			{ID: "show_part_of", Title: "Cart item data", Type: "checkbox", Default: true, Desc: "Show &quot;Part of: &lt;bundle&gt;&quot; under bundled items in the cart, checkout and orders."},
			{ID: "show_includes", Title: "Bundle contents", Type: "checkbox", Default: true, Desc: "List the bundled items (&quot;Includes&quot;) under the bundle in the cart."},
			{Type: "sectionend"},
		},
	})
	gooRegisterProductType(&gooProductTypeDef{
		Type: bnType, Label: "Product bundle", Module: bnKey,
		Purchasable: func(s *Server, ctx context.Context, p *gooProduct) bool {
			return s.gooExtOn(bnKey) && (p.Status == "publish" || p.Status == "private") && len(bnItemsOf(p)) > 0
		},
		NeedsShipping: func(s *Server, ctx context.Context, p *gooProduct) bool {
			// Unassembled bundles ship their items; an assembled bundle ships as one parcel.
			return s.gooExtOn(bnKey) && bnAssembled(p) && !p.Virtual
		},
		AddToCartText: func(s *Server, p *gooProduct, single bool) string {
			if single {
				return "Add to cart"
			}
			for _, it := range bnItemsOf(p) {
				if it.Optional || it.Min != it.Max {
					return "Select options"
				}
			}
			return "Add to cart"
		},
	})
	gooAddToCartHooks = append(gooAddToCartHooks, bnAddToCart)
	gooCartItemPriceHooks = append(gooCartItemPriceHooks, bnCartPrice)
	gooCartItemDataHooks = append(gooCartItemDataHooks, bnItemData)
	gooCheckoutHooks = append(gooCheckoutHooks, bnCheckout)
	gooPriceHTMLHooks = append(gooPriceHTMLHooks, bnPriceHTML)
	gooProductPriceHooks = append(gooProductPriceHooks, func(s *Server, ctx context.Context, p *gooProduct, price float64) float64 {
		if !s.gooExtOn(bnKey) || p.Type != bnType {
			return price
		}
		lo, _ := s.bnPriceRange(ctx, p)
		return lo
	})
	gooOnHTML("single_before_add_to_cart", bnSingleForm)
	gooCartItemVisibleHooks = append(gooCartItemVisibleHooks, bnCartItemVisibleHidx)
	gooOrderItemVisibleHooks = append(gooOrderItemVisibleHooks, bnOrderItemVisibleHidx)
	gooProductPanels = append(gooProductPanels, &gooProductPanel{ID: "bundled_products", Label: "Bundled Products", Module: bnKey, ShowFor: []string{bnType}, Render: bnPanel, Save: bnPanelSave})
}

// bnItem is a bundled item (WC_Bundled_Item_Data).
type bnItem struct {
	ID                int
	ProductID         int64
	Default, Min, Max int // Max 0 = no limit
	Optional          bool
	PricedIndividual  bool
	Discount          float64
	VisibleProduct    bool
	VisibleCart       bool
	VisibleOrder      bool
	HideThumbnail     bool
	OverrideTitle     bool
	Title             string
}

func bnItemsOf(p *gooProduct) []bnItem {
	var out []bnItem
	for _, m := range gxList(p.Meta["_bundle_data"]) {
		vis := func(k string) bool {
			v, ok := m[k]
			return !ok || gxStr(v) != "hidden"
		}
		it := bnItem{ID: int(gxNum(m["item_id"])), ProductID: int64(gxNum(m["product_id"])), Default: int(gxNum(m["quantity_default"])),
			Min: int(gxNum(m["quantity_min"])), Max: int(gxNum(m["quantity_max"])), Optional: gxBool(m["optional"]),
			PricedIndividual: gxBool(m["priced_individually"]), Discount: gxNum(m["discount"]),
			VisibleProduct: vis("single_product_visibility"), VisibleCart: vis("cart_visibility"), VisibleOrder: vis("order_visibility"),
			HideThumbnail: gxBool(m["hide_thumbnail"]), OverrideTitle: gxBool(m["override_title"]), Title: gxStr(m["title"])}
		if it.ProductID != 0 {
			out = append(out, it)
		}
	}
	return out
}

func bnAssembled(p *gooProduct) bool { return gxStr(p.Meta["_wc_pb_shipping_type"]) == "assembled" }

func bnMetaInt(p *gooProduct, k string) int { return int(gxNum(p.Meta[k])) }

// bnTitle is a bundled item's title.
func (it bnItem) title(p *gooProduct) string {
	if it.OverrideTitle && it.Title != "" {
		return it.Title
	}
	if p != nil {
		return p.Name
	}
	return ""
}

func bnDiscounted(v, discount float64) float64 {
	if discount <= 0 {
		return v
	}
	return v * (1 - discount/100)
}

// bnPriceRange is the bundle's min / max price (max = +Inf without quantity limits).
func (s *Server) bnPriceRange(ctx context.Context, p *gooProduct) (float64, float64) {
	lo, hi := p.PriceValue(), p.PriceValue()
	for _, it := range bnItemsOf(p) {
		if !it.PricedIndividual {
			continue
		}
		cp, err := s.gooGetProduct(ctx, it.ProductID)
		if err != nil {
			continue
		}
		pmin, pmax := cp.PriceValue(), cp.PriceValue()
		if cp.Type == gooTypeVariable && cp.MinPrice != nil && cp.MaxPrice != nil {
			pmin, pmax = *cp.MinPrice, *cp.MaxPrice
		}
		if !it.Optional {
			lo += bnDiscounted(pmin, it.Discount) * float64(it.Min)
		}
		if it.Max <= 0 {
			hi = math.Inf(1)
		} else {
			hi += bnDiscounted(pmax, it.Discount) * float64(it.Max)
		}
	}
	return gooRound(lo, s.gooDecimals()), hi
}

// bnPriceHTML is "From: $X" for bundles whose price varies with the configuration.
func bnPriceHTML(s *Server, ctx context.Context, p *gooProduct, h template.HTML) template.HTML {
	if !s.gooExtOn(bnKey) || p.Type != bnType {
		return h
	}
	lo, hi := s.bnPriceRange(ctx, p)
	if lo == hi {
		return s.gooPrice(lo)
	}
	return template.HTML(`<span class="from">From: </span>`) + s.gooPrice(lo)
}

// Cart ----------------------------------------------------------------------------------------------------

// bnAddToCart validates the bundle's configuration (quantities, optional items, variations, stock,
// bundle size) and stores it in the container's data ("stamp"); bnCartPrice adds the children.
func bnAddToCart(s *Server, ctx context.Context, p *gooProduct, qty int, form map[string][]string, data map[string]any) error {
	if !s.gooExtOn(bnKey) || p.Type != bnType {
		return nil
	}
	stamp := map[string]any{}
	total := 0
	for _, it := range bnItemsOf(p) {
		id := strconv.Itoa(it.ID)
		cp, err := s.gooGetProduct(ctx, it.ProductID)
		if err != nil || cp.Status != "publish" {
			return gooCartErr("&quot;%s&quot; cannot be purchased &ndash; one of its bundled products is unavailable.", html.EscapeString(p.Name))
		}
		name := html.EscapeString(it.title(cp))
		if it.Optional && form != nil && gxForm(form, "bundle_selected_optional_"+id) == "" {
			continue
		}
		n := it.Default
		if form != nil {
			if v, ok := form["bundle_quantity_"+id]; ok && len(v) > 0 && strings.TrimSpace(v[0]) != "" {
				n = gxAtoi(v[0])
			}
		}
		if n < it.Min {
			return gooCartErr("The quantity of &quot;%s&quot; cannot be lower than %d.", name, it.Min)
		}
		if it.Max > 0 && n > it.Max {
			return gooCartErr("The quantity of &quot;%s&quot; cannot be higher than %d.", name, it.Max)
		}
		if n <= 0 {
			continue
		}
		buy := cp
		var varID int64
		attrs := map[string]any{}
		if cp.Type == gooTypeVariable {
			want := map[string]string{}
			for _, a := range cp.Attributes {
				if a.Variation {
					if v := gxForm(form, "bundle_attribute_"+a.Key()+"_"+id); v != "" {
						want[a.Key()] = v
					}
				}
			}
			var v *gooProduct
			if vid, _ := strconv.ParseInt(gxForm(form, "bundle_variation_id_"+id), 10, 64); vid != 0 {
				if cand, err := s.gooGetProduct(ctx, vid); err == nil && cand.ParentID == cp.ID {
					v = cand
				}
			}
			if v == nil {
				v, err = s.gooMatchVariation(ctx, cp, want)
				if err != nil || v == nil {
					return gooCartErr("Please choose &quot;%s&quot; options&hellip;", name)
				}
			}
			for _, a := range cp.Attributes {
				if !a.Variation {
					continue
				}
				val := v.VariationAttrs[a.Key()]
				if val == "" {
					val = want[a.Key()]
				}
				if val == "" {
					return gooCartErr("Please choose &quot;%s&quot; options&hellip;", name)
				}
				attrs["attribute_"+a.Key()] = val
			}
			v.parent = cp
			buy, varID = v, v.ID
		} else if cp.Type != gooTypeSimple {
			return gooCartErr("&quot;%s&quot; cannot be bundled.", name)
		}
		if !s.gooIsPurchasable(ctx, buy) || !s.gooIsInStock(ctx, buy) {
			return gooCartErr("&quot;%s&quot; cannot be added to the cart because &quot;%s&quot; is out of stock.", html.EscapeString(p.Name), name)
		}
		if !s.gooHasEnoughStock(ctx, buy, n*qty) {
			return gooCartErr("&quot;%s&quot; cannot be added to the cart because there is not enough stock of &quot;%s&quot; (%d remaining).", html.EscapeString(p.Name), name, s.gooStockQty(ctx, buy))
		}
		total += n
		stamp[id] = map[string]any{"product_id": float64(it.ProductID), "variation_id": float64(varID), "attributes": attrs, "quantity": float64(n),
			"optional_selected": it.Optional, "discount": it.Discount}
	}
	if mn := bnMetaInt(p, "_wc_pb_min_bundle_size"); mn > 0 && total < mn {
		return gooCartErr("Please choose at least %d items.", mn)
	}
	if mx := bnMetaInt(p, "_wc_pb_max_bundle_size"); mx > 0 && total > mx {
		return gooCartErr("Please choose up to %d items.", mx)
	}
	if len(stamp) == 0 {
		return gooCartErr("Please choose at least one item for &quot;%s&quot;.", html.EscapeString(p.Name))
	}
	data["stamp"] = stamp
	return nil
}

func bnStamp(it *gooCartItem) map[string]any {
	m, _ := it.Data["stamp"].(map[string]any)
	return m
}

func bnBundledBy(it *gooCartItem) string { return gxStr(it.Data["bundled_by"]) }

// bnReconcile keeps the cart's bundles consistent: orphan children go, missing children come back,
// child quantities follow their container.
func (s *Server) bnReconcile(ctx context.Context, cart *gooCart) {
	containers := map[string]*gooCartItem{}
	for _, it := range cart.Items {
		if it.product != nil && it.product.Type == bnType && bnStamp(it) != nil {
			containers[it.Key] = it
		}
	}
	changed := false
	var items []*gooCartItem
	have := map[string]*gooCartItem{} // container key + item id → child
	for _, it := range cart.Items {
		if by := bnBundledBy(it); by != "" {
			if containers[by] == nil {
				changed = true // the container was removed: so are its items
				continue
			}
			have[by+"|"+gxStr(it.Data["bundled_item_id"])] = it
		}
		items = append(items, it)
	}
	// Rebuild in order: each container followed by its children.
	var out []*gooCartItem
	for _, it := range items {
		if bnBundledBy(it) != "" {
			continue
		}
		out = append(out, it)
		stamp := bnStamp(it)
		if stamp == nil || containers[it.Key] == nil {
			continue
		}
		bundleItems := map[string]bnItem{}
		for _, bi := range bnItemsOf(it.product) {
			bundleItems[strconv.Itoa(bi.ID)] = bi
		}
		ids := make([]string, 0, len(stamp))
		for id := range stamp {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return gxAtoi(ids[i]) < gxAtoi(ids[j]) })
		for _, id := range ids {
			st, _ := stamp[id].(map[string]any)
			want := int(gxNum(st["quantity"])) * it.Quantity
			child := have[it.Key+"|"+id]
			if child == nil {
				child = s.bnNewChild(ctx, it, id, st, bundleItems[id])
				if child == nil {
					continue
				}
				s.bnChildPrice(it, child)
				changed = true
			}
			if child.Quantity != want {
				child.Quantity = want
				changed = true
			}
			out = append(out, child)
		}
	}
	if changed || len(out) != len(cart.Items) {
		gxCartItemsSwap(cart, out)
	} else {
		for i := range out {
			if out[i] != cart.Items[i] {
				gxCartItemsSwap(cart, out)
				break
			}
		}
	}
}

// bnNewChild builds a bundled item's cart line.
func (s *Server) bnNewChild(ctx context.Context, container *gooCartItem, id string, st map[string]any, bi bnItem) *gooCartItem {
	pid, vid := int64(gxNum(st["product_id"])), int64(gxNum(st["variation_id"]))
	buyID := pid
	if vid != 0 {
		buyID = vid
	}
	p, err := s.gooGetProduct(ctx, buyID)
	if err != nil {
		return nil
	}
	if vid != 0 {
		p.parent, _ = s.gooGetProduct(ctx, pid)
	}
	variation := map[string]string{}
	if attrs, ok := st["attributes"].(map[string]any); ok {
		for k, v := range attrs {
			variation[k] = gxStr(v)
		}
	}
	if vid == 0 {
		variation = nil
	}
	data := map[string]any{"bundled_by": container.Key, "bundled_item_id": id, "bundle_name": container.product.Name}
	if !bi.VisibleCart {
		data["bundled_item_hidden"] = true
	}
	if !bi.VisibleOrder {
		data["bundled_item_hidden_order"] = true
	}
	data = gxRoundTrip(data).(map[string]any)
	it := &gooCartItem{Key: gooCartItemKey(pid, vid, variation, data), ProductID: pid, VariationID: vid, Variation: variation, Data: data, product: p}
	it.TaxClass = s.gooTaxClassOf(ctx, p)
	it.Taxable = s.gooTaxEnabled() && p.TaxStatus == "taxable"
	if cats, err := s.store.TermsForPosts(ctx, []int64{pid}, "product_cat"); err == nil {
		for _, t := range cats[pid] {
			it.categories = append(it.categories, t.ID)
		}
	}
	return it
}

// bnCartPrice prices bundled items: 0 when included in the bundle's price, else their price less the
// item discount. Children of assembled bundles do not ship on their own.
func bnCartPrice(s *Server, ctx context.Context, cart *gooCart, it *gooCartItem) {
	if !s.gooExtOn(bnKey) {
		return
	}
	if len(cart.Items) > 0 && it == cart.Items[0] {
		s.bnReconcile(ctx, cart)
	}
	if by := bnBundledBy(it); by != "" {
		if container := cart.Item(by); container != nil && container.product != nil {
			s.bnChildPrice(container, it)
		}
	}
}

// bnChildPrice prices a bundled item and keeps children of assembled bundles from shipping alone.
func (s *Server) bnChildPrice(container, it *gooCartItem) {
	var bi *bnItem
	for _, x := range bnItemsOf(container.product) {
		if strconv.Itoa(x.ID) == gxStr(it.Data["bundled_item_id"]) {
			x := x
			bi = &x
		}
	}
	if bi == nil || !bi.PricedIndividual {
		it.Price = 0
	} else {
		it.Price = gooRound(bnDiscounted(it.product.PriceValue(), bi.Discount), s.gooDecimals())
	}
	if bnAssembled(container.product) && !it.product.Virtual {
		cp := *it.product
		cp.Virtual = true
		it.product = &cp
	}
}

// bnItemData shows "Part of: <bundle>" under children and the contents under containers.
func bnItemData(s *Server, ctx context.Context, it *gooCartItem) []gooKV {
	if !s.gooExtOn(bnKey) {
		return nil
	}
	if by := bnBundledBy(it); by != "" {
		if !s.gooExtBool(bnKey, "show_part_of") {
			return nil
		}
		return []gooKV{{"Part of", gxStr(it.Data["bundle_name"])}}
	}
	if stamp := bnStamp(it); stamp != nil && s.gooExtBool(bnKey, "show_includes") {
		var parts []string
		ids := make([]string, 0, len(stamp))
		for id := range stamp {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return gxAtoi(ids[i]) < gxAtoi(ids[j]) })
		titles := map[string]string{}
		hiddenIDs := map[string]bool{}
		if it.product != nil {
			for _, bi := range bnItemsOf(it.product) {
				if !bi.VisibleCart {
					hiddenIDs[strconv.Itoa(bi.ID)] = true // hidden in the cart: not listed under the bundle either
				}
				if bi.OverrideTitle && bi.Title != "" {
					titles[strconv.Itoa(bi.ID)] = bi.Title
				}
			}
		}
		for _, id := range ids {
			if hiddenIDs[id] {
				continue
			}
			st, _ := stamp[id].(map[string]any)
			name := titles[id]
			if name == "" {
				if cp, err := s.gooGetProduct(ctx, int64(gxNum(st["product_id"]))); err == nil {
					name = cp.Name
				}
			}
			parts = append(parts, fmt.Sprintf("%s × %d", name, int(gxNum(st["quantity"]))))
		}
		return []gooKV{{"Includes", strings.Join(parts, ", ")}}
	}
	return nil
}

// bnCheckout records the parent / child relation in the order items' meta.
func bnCheckout(s *Server, ctx context.Context, cart *gooCart, o *gooOrder) error {
	if !s.gooExtOn(bnKey) {
		return nil
	}
	lines := o.ItemsOf("line_item")
	children := map[string][]string{}
	for _, it := range cart.Items {
		if by := bnBundledBy(it); by != "" {
			children[by] = append(children[by], it.Key)
		}
	}
	for i, it := range cart.Items {
		if i >= len(lines) {
			break
		}
		li := lines[i]
		if li.ProductID != it.ProductID {
			continue
		}
		if bnStamp(it) != nil {
			li.Meta["_bundle_cart_key"] = it.Key
			li.Meta["_bundled_items"] = children[it.Key]
		} else if by := bnBundledBy(it); by != "" {
			li.Meta["_bundle_cart_key"] = it.Key
			li.Meta["_bundled_by"] = by
			if gxBool(it.Data["bundled_item_hidden_order"]) {
				li.Meta["_bundled_item_hidden"] = "yes"
			}
			if cont := cart.Item(by); cont != nil && cont.product != nil {
				li.Meta["_bundled_item_title"] = li.Name
				for _, bi := range bnItemsOf(cont.product) {
					if strconv.Itoa(bi.ID) == gxStr(it.Data["bundled_item_id"]) && bi.OverrideTitle && bi.Title != "" {
						li.Name = bi.Title
					}
				}
			}
		}
	}
	return nil
}

// Single product ----------------------------------------------------------------------------------------

// bnSingleForm lists the bundled items inside the add-to-cart form.
func bnSingleForm(s *Server, c *gin.Context, p *gooProduct) template.HTML {
	if !s.gooExtOn(bnKey) || p.Type != bnType {
		return ""
	}
	ctx := c.Request.Context()
	layout := defaultStr(gxStr(p.Meta["_wc_pb_layout"]), "default")
	var b strings.Builder
	fmt.Fprintf(&b, `<div class="bundle_form bundle_form_%d layout_%s" data-bundle-id="%d" data-form-location="%s">`, p.ID, html.EscapeString(layout), p.ID,
		html.EscapeString(defaultStr(gxStr(p.Meta["_wc_pb_add_to_cart_form_location"]), "default")))
	if layout == "tabular" {
		b.WriteString(`<table cellspacing="0" class="bundled_products"><thead><tr><th class="bundled_item_col bundled_item_images_head"></th><th class="bundled_item_col bundled_item_details_head">Product</th><th class="bundled_item_col bundled_item_qty_head">Quantity</th></tr></thead><tbody>`)
	} else if layout == "grid" {
		b.WriteString(`<ul class="bundled_products columns-3">`)
	}
	for _, it := range bnItemsOf(p) {
		cp, err := s.gooGetProduct(ctx, it.ProductID)
		if err != nil {
			continue
		}
		id := strconv.Itoa(it.ID)
		hidden := ""
		if !it.VisibleProduct {
			hidden = ` style="display:none"`
		}
		img := ""
		if imgID := s.gooImageOf(ctx, cp); imgID != 0 && !it.HideThumbnail {
			if a, err := s.store.GetAttachment(ctx, imgID); err == nil {
				img = string(s.attachmentImage(ctx, a, "thumbnail", "bundled_product_image"))
			}
		}
		price := ""
		if it.PricedIndividual {
			v := cp.PriceValue()
			if cp.Type == gooTypeVariable && cp.MinPrice != nil {
				v = *cp.MinPrice
			}
			disc := bnDiscounted(v, it.Discount)
			if it.Discount > 0 {
				price = `<span class="price"><del>` + string(s.gooPrice(v)) + `</del> <ins>` + string(s.gooPrice(gooRound(disc, s.gooDecimals()))) + `</ins></span>`
			} else {
				price = `<span class="price">` + string(s.gooPrice(v)) + `</span>`
			}
		}
		var details strings.Builder
		title := html.EscapeString(it.title(cp))
		if it.Min == it.Max && it.Min > 1 {
			title = fmt.Sprintf("%d &times; %s", it.Min, title)
		}
		fmt.Fprintf(&details, `<h4 class="bundled_product_title product_title"><span class="bundled_product_title_inner">%s</span>`, title)
		if it.Optional {
			details.WriteString(` <span class="bundled_product_optional_suffix">&ndash; optional</span>`)
		}
		details.WriteString(`</h4>` + price)
		if it.Optional {
			label := "Add"
			if it.PricedIndividual {
				label = "Add for " + string(s.gooPrice(gooRound(bnDiscounted(cp.PriceValue(), it.Discount), s.gooDecimals())))
			}
			fmt.Fprintf(&details, `<label class="bundled_product_optional_checkbox"><input class="bundled_product_checkbox" type="checkbox" name="bundle_selected_optional_%s" value="1"> %s</label>`, id, label)
		}
		if cp.Type == gooTypeVariable {
			details.WriteString(`<table class="variations" cellspacing="0"><tbody>`)
			for _, a := range cp.Attributes {
				if !a.Variation {
					continue
				}
				name := "bundle_attribute_" + html.EscapeString(a.Key()) + "_" + id
				fmt.Fprintf(&details, `<tr><th class="label"><label for="%s">%s</label></th><td class="value"><select id="%s" name="%s" data-attribute_name="attribute_%s"><option value="">Choose an option</option>`,
					name, html.EscapeString(s.gooAttrLabel(ctx, a)), name, name, html.EscapeString(a.Key()))
				for _, o := range a.Options {
					fmt.Fprintf(&details, `<option value="%s"%s>%s</option>`, html.EscapeString(o), gxSel(o, cp.DefaultAttributes[a.Key()]), html.EscapeString(s.gooAttrValueLabel(ctx, a, o)))
				}
				details.WriteString(`</select></td></tr>`)
			}
			details.WriteString(`</tbody></table>`)
		}
		qty := ""
		if it.Min == it.Max {
			qty = fmt.Sprintf(`<input type="hidden" name="bundle_quantity_%s" value="%d">`, id, it.Min)
		} else {
			maxAttr := ""
			if it.Max > 0 {
				maxAttr = fmt.Sprintf(` max="%d"`, it.Max)
			}
			qty = fmt.Sprintf(`<div class="quantity"><label class="screen-reader-text" for="bundle_quantity_%s">%s quantity</label><input type="number" id="bundle_quantity_%s" class="input-text qty text" step="1" min="%d"%s name="bundle_quantity_%s" value="%d" inputmode="numeric"></div>`,
				id, title, id, it.Min, maxAttr, id, it.Default)
		}
		switch layout {
		case "tabular":
			fmt.Fprintf(&b, `<tr class="bundled_product bundled_product_summary product bundled_item_%s"%s><td class="bundled_item_col bundled_item_images_col">%s</td><td class="bundled_item_col bundled_item_details_col">%s</td><td class="bundled_item_col bundled_item_qty_col">%s</td></tr>`, id, hidden, img, details.String(), qty)
		case "grid":
			fmt.Fprintf(&b, `<li class="bundled_product bundled_product_summary product bundled_item_%s"%s><div class="bundled_product_images images">%s</div><div class="details">%s%s</div></li>`, id, hidden, img, details.String(), qty)
		default:
			fmt.Fprintf(&b, `<div class="bundled_product bundled_product_summary product bundled_item_%s"%s><div class="bundled_product_images images">%s</div><div class="details">%s%s</div></div>`, id, hidden, img, details.String(), qty)
		}
	}
	switch layout {
	case "tabular":
		b.WriteString(`</tbody></table>`)
	case "grid":
		b.WriteString(`</ul>`)
	}
	b.WriteString(`<div class="cart bundle_data"><div class="bundle_wrap"><div class="bundle_price"></div></div></div></div>`)
	return template.HTML(b.String())
}

// Admin panel ----------------------------------------------------------------------------------------------

func bnRow(s *Server, ctx context.Context, it bnItem) string {
	name := ""
	if it.ProductID != 0 {
		if p, err := s.gooGetProduct(ctx, it.ProductID); err == nil {
			name = p.Name
		}
	}
	yn := func(field string, on bool) string {
		return bkSelectName(field+"[]", map[bool]string{true: "yes", false: "no"}[on], []gooOpt{{"no", "No"}, {"yes", "Yes"}})
	}
	vis := func(field string, on bool) string {
		return bkSelectName(field+"[]", map[bool]string{true: "visible", false: "hidden"}[on], []gooOpt{{"visible", "Visible"}, {"hidden", "Hidden"}})
	}
	maxV := ""
	if it.Max > 0 {
		maxV = strconv.Itoa(it.Max)
	}
	return `<tr class="bundled_item"><td><input type="hidden" name="bundle_item_id[]" value="` + strconv.Itoa(it.ID) + `"><input type="number" name="bundle_product_id[]" value="` +
		map[bool]string{true: itoa64(it.ProductID), false: ""}[it.ProductID != 0] + `" placeholder="Product ID" style="width:90px"> <span class="description">` + html.EscapeString(name) + `</span></td>` +
		`<td><input type="number" name="bundle_quantity_default[]" value="` + strconv.Itoa(it.Default) + `" min="0" style="width:60px"></td>` +
		`<td><input type="number" name="bundle_quantity_min[]" value="` + strconv.Itoa(it.Min) + `" min="0" style="width:60px"></td>` +
		`<td><input type="number" name="bundle_quantity_max[]" value="` + maxV + `" min="0" style="width:60px"></td>` +
		`<td>` + yn("bundle_optional", it.Optional) + `</td><td>` + yn("bundle_priced_individually", it.PricedIndividual) + `</td>` +
		`<td><input type="text" name="bundle_discount[]" value="` + map[bool]string{true: gxFmt(it.Discount), false: ""}[it.Discount > 0] + `" size="4"></td>` +
		`<td>` + vis("bundle_single_product_visibility", it.VisibleProduct) + vis("bundle_cart_visibility", it.VisibleCart) + vis("bundle_order_visibility", it.VisibleOrder) + `</td>` +
		`<td>` + yn("bundle_hide_thumbnail", it.HideThumbnail) + `</td>` +
		`<td>` + yn("bundle_override_title", it.OverrideTitle) + `<input type="text" name="bundle_title[]" value="` + html.EscapeString(it.Title) + `" placeholder="Title"></td>` +
		`<td class="remove"><a href="#" class="button" onclick="this.closest('tr').remove();return false">&times;</a></td></tr>`
}

func bnPanel(s *Server, c *gin.Context, p *gooProduct) template.HTML {
	ctx := c.Request.Context()
	var b strings.Builder
	b.WriteString(`<div class="options_group bundle_data_options">`)
	b.WriteString(bkField("Form location", "_wc_pb_add_to_cart_form_location", bkSelect("_wc_pb_add_to_cart_form_location", gxStr(p.Meta["_wc_pb_add_to_cart_form_location"]),
		[]gooOpt{{"default", "Default"}, {"after_summary", "Before Tabs"}}), "Choose where to display the add-to-cart form of the bundle."))
	b.WriteString(bkField("Layout", "_wc_pb_layout", bkSelect("_wc_pb_layout", gxStr(p.Meta["_wc_pb_layout"]),
		[]gooOpt{{"default", "Standard"}, {"tabular", "Tabular"}, {"grid", "Grid"}}), "Select the Standard option to have all bundled products displayed in a list, or Tabular / Grid."))
	b.WriteString(bkField("Item grouping / shipping", "_wc_pb_shipping_type", bkSelect("_wc_pb_shipping_type", gxStr(p.Meta["_wc_pb_shipping_type"]),
		[]gooOpt{{"unassembled", "Unassembled – bundled products are shipped individually"}, {"assembled", "Assembled – bundled products are shipped in the bundle's package"}}), ""))
	b.WriteString(bkField("Min bundle size", "_wc_pb_min_bundle_size", bkInput("_wc_pb_min_bundle_size", "number", map[bool]string{true: strconv.Itoa(bnMetaInt(p, "_wc_pb_min_bundle_size")), false: ""}[bnMetaInt(p, "_wc_pb_min_bundle_size") > 0], ` min="0"`), "Minimum number of items in the bundle."))
	b.WriteString(bkField("Max bundle size", "_wc_pb_max_bundle_size", bkInput("_wc_pb_max_bundle_size", "number", map[bool]string{true: strconv.Itoa(bnMetaInt(p, "_wc_pb_max_bundle_size")), false: ""}[bnMetaInt(p, "_wc_pb_max_bundle_size") > 0], ` min="0"`), "Maximum number of items in the bundle."))
	b.WriteString(`</div><div class="options_group wc-bundled-items"><table class="widefat"><thead><tr><th>Product</th><th>Default Qty</th><th>Min Qty</th><th>Max Qty</th><th>Optional</th><th>Priced Individually</th><th>Discount %</th><th>Visibility (product / cart / order)</th><th>Hide Thumbnail</th><th>Override Title</th><th></th></tr></thead><tbody>`)
	for _, it := range bnItemsOf(p) {
		b.WriteString(bnRow(s, ctx, it))
	}
	b.WriteString(`</tbody></table><p><template>` + bnRow(s, ctx, bnItem{Default: 1, Min: 1, Max: 1, VisibleProduct: true, VisibleCart: true, VisibleOrder: true}) +
		`</template><a href="#" class="button add_bundled_product" ` + bkAddRowJS + `>Add Product</a></p></div>`)
	return template.HTML(b.String())
}

func bnPanelSave(s *Server, c *gin.Context, p *gooProduct) {
	if p.Meta == nil {
		p.Meta = map[string]any{}
	}
	p.Meta["_wc_pb_add_to_cart_form_location"] = defaultStr(c.PostForm("_wc_pb_add_to_cart_form_location"), "default")
	p.Meta["_wc_pb_layout"] = defaultStr(c.PostForm("_wc_pb_layout"), "default")
	p.Meta["_wc_pb_shipping_type"] = defaultStr(c.PostForm("_wc_pb_shipping_type"), "unassembled")
	p.Meta["_wc_pb_min_bundle_size"] = float64(gxAtoi(c.PostForm("_wc_pb_min_bundle_size")))
	p.Meta["_wc_pb_max_bundle_size"] = float64(gxAtoi(c.PostForm("_wc_pb_max_bundle_size")))
	f := func(k string) []string { return c.PostFormArray(k + "[]") }
	ids, pids := f("bundle_item_id"), f("bundle_product_id")
	next := 0
	for _, it := range bnItemsOf(p) {
		next = max(next, it.ID)
	}
	for _, v := range ids {
		next = max(next, gxAtoi(v))
	}
	var items []any
	for i, pidStr := range pids {
		at := func(k string) string {
			if l := f(k); i < len(l) {
				return strings.TrimSpace(l[i])
			}
			return ""
		}
		pid, _ := strconv.ParseInt(strings.TrimSpace(pidStr), 10, 64)
		if pid == 0 || pid == p.ID {
			continue
		}
		if cp, err := s.gooGetProduct(c.Request.Context(), pid); err != nil || (cp.Type != gooTypeSimple && cp.Type != gooTypeVariable) {
			continue
		}
		id := gxAtoi(at("bundle_item_id"))
		if id == 0 {
			next++
			id = next
		}
		def, mn := gxAtoi(at("bundle_quantity_default")), gxAtoi(at("bundle_quantity_min"))
		mx := gxAtoi(at("bundle_quantity_max"))
		if def < mn {
			def = mn
		}
		if mx > 0 && def > mx {
			def = mx
		}
		items = append(items, map[string]any{"item_id": float64(id), "product_id": float64(pid), "quantity_default": float64(def), "quantity_min": float64(mn),
			"quantity_max": float64(mx), "optional": at("bundle_optional") == "yes", "priced_individually": at("bundle_priced_individually") == "yes",
			"discount": math.Min(math.Max(gxAtof(at("bundle_discount")), 0), 100), "single_product_visibility": defaultStr(at("bundle_single_product_visibility"), "visible"),
			"cart_visibility": defaultStr(at("bundle_cart_visibility"), "visible"), "order_visibility": defaultStr(at("bundle_order_visibility"), "visible"),
			"hide_thumbnail": at("bundle_hide_thumbnail") == "yes", "override_title": at("bundle_override_title") == "yes", "title": at("bundle_title")})
	}
	p.Meta["_bundle_data"] = items
	if p.RegularPrice == nil {
		zero := 0.0
		p.RegularPrice = &zero // a bundle of individually priced items has no price of its own
	}
}

// bnCartItemVisibleHidx hides bundled items whose cart visibility is "hidden" from the cart, mini-cart
// and checkout review (they still count in the totals).
func bnCartItemVisibleHidx(s *Server, ctx context.Context, it *gooCartItem, where string) bool {
	if !s.gooExtOn(bnKey) || bnBundledBy(it) == "" {
		return true
	}
	return !gxBool(it.Data["bundled_item_hidden"])
}

// bnOrderItemVisibleHidx hides bundled items whose order visibility is "hidden" from order details,
// the pay page, e-mails and invoices.
func bnOrderItemVisibleHidx(s *Server, ctx context.Context, it *gooOrderItem, where string) bool {
	if !s.gooExtOn(bnKey) || it.Meta == nil || gxStr(it.Meta["_bundled_by"]) == "" {
		return true
	}
	return gxStr(it.Meta["_bundled_item_hidden"]) != "yes"
}
