package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Goocommerce Quick View (WooCommerce Quick View): a "Quick view" button under catalog items opens a
// modal with the product summary — gallery, title, rating, price, short description, the add-to-cart
// form (simple products; a variation picker for variable ones) and the meta — loaded from
// /?goo-ajax=quick_view&product_id=. The modal is keyboard accessible (focus trap, Esc, focus return).

const gooQVKey = "quick_view"

func init() {
	gooRegisterExtension(&gooExtension{
		Key:         gooQVKey,
		Name:        "Quick View",
		Description: "Let customers see a product's details, gallery and add-to-cart form in a modal without leaving the shop page.",
		Icon:        "dashicons-visibility",
		Category:    "Merchandising",
		Fields: []gooField{
			{ID: "general", Title: "Quick View options", Type: "title"},
			{ID: "button_text", Title: "Button text", Type: "text", Default: "Quick view"},
			{ID: "show_on_mobile", Title: "Mobile", Type: "checkbox", Default: true, Desc: "Show the Quick view button on small screens"},
			{ID: "enable_lightbox", Title: "Lightbox", Type: "checkbox", Default: true, Desc: "Open gallery images in a lightbox inside the quick view"},
			{ID: "general_end", Type: "sectionend"},
		},
	})
	gooOnHTML("loop_after_item", gooQVLoopButton)
	gooOnPage("footer", gooQVFooter)
	gooFrontRoutes = append(gooFrontRoutes, gooQVRoute)
}

// Shared product display helpers (also used by Compare and Wishlists) ----------------------------------

// gooXCImageURL is an attachment's URL in a size ("" = full), "" when there is none.
func (s *Server) gooXCImageURL(ctx context.Context, id int64, size string) string {
	if id <= 0 {
		return ""
	}
	a, err := s.store.GetAttachment(ctx, id)
	if err != nil {
		return ""
	}
	if size != "" {
		if v := a.Variant(size); v != nil {
			return s.uploadURL(ctx, v.FilePath)
		}
	}
	return s.uploadURL(ctx, a.FilePath)
}

// gooXCPlaceholder is wc_placeholder_img_src().
func (s *Server) gooXCPlaceholder(ctx context.Context) string {
	return s.links(ctx).HomeURL(gooAssets + "/images/placeholder.png")
}

// gooXCThumb is a product's thumbnail <img> (woocommerce_get_product_thumbnail()).
func (s *Server) gooXCThumb(ctx context.Context, p *gooProduct, size string) template.HTML {
	src := s.gooXCImageURL(ctx, s.gooImageOf(ctx, p), size)
	cls := "attachment-woocommerce_thumbnail size-woocommerce_thumbnail"
	if src == "" {
		src, cls = s.gooXCPlaceholder(ctx), "woocommerce-placeholder wp-post-image"
	}
	return template.HTML(`<img src="` + gooEsc(src) + `" class="` + cls + `" alt="` + gooEsc(s.gooProductTitle(ctx, p)) + `" loading="lazy" decoding="async">`)
}

// gooXCPriceHTML is get_price_html(): sale prices struck through, a variable product's range, then
// the modules' price HTML hooks.
func (s *Server) gooXCPriceHTML(ctx context.Context, p *gooProduct) template.HTML {
	if p == nil {
		return ""
	}
	loc := s.gooBaseLocation()
	disp := func(v float64) float64 { return s.gooPriceToDisplay(ctx, p, v, loc) }
	var h template.HTML
	switch p.Type {
	case gooTypeVariable, gooTypeGrouped:
		if p.MinPrice == nil {
			return s.gooFilterPriceHTML(ctx, p, "")
		}
		lo, hi := disp(*p.MinPrice), disp(*p.MinPrice)
		if p.MaxPrice != nil {
			hi = disp(*p.MaxPrice)
		}
		if lo != hi {
			h = s.gooPrice(lo) + ` <span aria-hidden="true">&ndash;</span> <span class="screen-reader-text">through</span> ` + s.gooPrice(hi)
		} else {
			h = s.gooPrice(lo)
		}
		if p.Type == gooTypeGrouped && lo != hi {
			h = `<span class="from">From: </span>` + s.gooPrice(lo)
		}
	default:
		if !p.HasPrice() {
			return s.gooFilterPriceHTML(ctx, p, "")
		}
		if p.IsOnSale() && p.RegularPrice != nil {
			h = `<del aria-hidden="true">` + s.gooPrice(disp(*p.RegularPrice)) + `</del> <ins aria-hidden="true">` + s.gooPrice(disp(p.PriceValue())) + `</ins>`
		} else {
			h = s.gooPrice(disp(p.PriceValue()))
		}
	}
	return s.gooFilterPriceHTML(ctx, p, h)
}

// gooXCRatingHTML is wc_get_rating_html().
func gooXCRatingHTML(avg float64, count int) template.HTML {
	if count <= 0 || avg <= 0 {
		return ""
	}
	r := strconv.FormatFloat(avg, 'f', 2, 64)
	return template.HTML(fmt.Sprintf(`<div class="star-rating" role="img" aria-label="Rated %s out of 5"><span style="width:%s%%">Rated <strong class="rating">%s</strong> out of 5</span></div>`,
		r, strconv.FormatFloat(avg/5*100, 'f', -1, 64), r))
}

// gooXCAvailability is the stock line (wc_get_stock_html()).
func (s *Server) gooXCAvailability(ctx context.Context, p *gooProduct) (string, string) {
	if p.Type == gooTypeVariable || p.Type == gooTypeGrouped || p.Type == gooTypeExternal {
		if p.StockStatus == "outofstock" {
			return "Out of stock", "out-of-stock"
		}
		return "In stock", "in-stock"
	}
	if !s.gooIsInStock(ctx, p) {
		return "Out of stock", "out-of-stock"
	}
	if q := s.gooStockQty(ctx, p); q >= 0 {
		if q <= 0 && s.gooBackordersAllowed(ctx, p) {
			return "Available on backorder", "available-on-backorder"
		}
		return strconv.Itoa(q) + " in stock", "in-stock"
	}
	if p.StockStatus == "onbackorder" {
		return "Available on backorder", "available-on-backorder"
	}
	return "In stock", "in-stock"
}

// gooXCAddToCartText is the add-to-cart button text of a product (woocommerce_product_add_to_cart_text).
func (s *Server) gooXCAddToCartText(p *gooProduct, single bool) string {
	if def := gooProductTypes[p.Type]; def != nil && def.AddToCartText != nil {
		return def.AddToCartText(s, p, single)
	}
	switch p.Type {
	case gooTypeExternal:
		return defaultStr(p.ButtonText, "Buy product")
	case gooTypeVariable:
		if single {
			return "Add to cart"
		}
		return "Select options"
	case gooTypeGrouped:
		if single {
			return "Add to cart"
		}
		return "View products"
	}
	return "Add to cart"
}

// gooXCVisible reports whether a product may be shown on the front (published and not hidden).
func gooXCVisible(p *gooProduct) bool {
	return p != nil && p.Status == "publish" && p.Type != gooTypeVariation
}

// gooXCWantsJSON writes a JSON response.
func gooXCJSON(c *gin.Context, code int, v any) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Robots-Tag", "noindex")
	c.JSON(code, v)
}

// Quick View ------------------------------------------------------------------------------------------

// gooQVLoopButton is the Quick view button under a catalog item.
func gooQVLoopButton(s *Server, c *gin.Context, p *gooProduct) template.HTML {
	if !s.gooExtOn(gooQVKey) || !gooXCVisible(p) {
		return ""
	}
	cls := "button goo-quick-view-button"
	if !s.gooExtBool(gooQVKey, "show_on_mobile") {
		cls += " goo-quick-view-hide-mobile"
	}
	text := defaultStr(s.gooExtString(gooQVKey, "button_text"), "Quick view")
	return template.HTML(`<a href="` + gooEsc(s.gooPermalink(c, p)) + `" class="` + cls + `" data-product_id="` + itoa64(p.ID) +
		`" role="button" aria-haspopup="dialog" aria-label="` + gooEsc(text+": "+p.Name) + `">` + gooEsc(text) + `</a>`)
}

// gooQVFooter prints the modal shell and the module's assets once per page.
func gooQVFooter(s *Server, c *gin.Context) template.HTML {
	if !s.gooExtOn(gooQVKey) {
		return ""
	}
	home := s.links(c).HomeURL("/")
	lightbox, mobile := "0", "0"
	if s.gooExtBool(gooQVKey, "enable_lightbox") {
		lightbox = "1"
	}
	if s.gooExtBool(gooQVKey, "show_on_mobile") {
		mobile = "1"
	}
	return template.HTML(`<link rel="stylesheet" id="goo-ext-quick-view-css" href="` + gooAssets + `/css/ext-quick-view.css?ver=` + gooVersion + `" media="all">` +
		`<div id="goo-quick-view" class="goo-quick-view-modal" role="dialog" aria-modal="true" aria-labelledby="goo-quick-view-title" hidden` +
		` data-endpoint="` + gooEsc(home+"?goo-ajax=quick_view") + `" data-lightbox="` + lightbox + `" data-mobile="` + mobile + `">` +
		`<div class="goo-quick-view-overlay" data-goo-qv-close></div>` +
		`<div class="goo-quick-view-dialog" role="document"><button type="button" class="goo-quick-view-close" data-goo-qv-close aria-label="Close quick view">&times;</button>` +
		`<div class="goo-quick-view-content" aria-live="polite"></div></div></div>` +
		`<script src="` + gooAssets + `/js/ext-quick-view.js?ver=` + gooVersion + `" id="goo-ext-quick-view-js" defer></script>`)
}

// gooQVRoute serves /?goo-ajax=quick_view&product_id=: the product summary HTML.
func gooQVRoute(s *Server, c *gin.Context, path string) bool {
	if c.Query("goo-ajax") != "quick_view" || !s.gooExtOn(gooQVKey) {
		return false
	}
	c.Header("X-Robots-Tag", "noindex")
	c.Header("Cache-Control", "no-store")
	id, _ := strconv.ParseInt(c.Query("product_id"), 10, 64)
	p, err := s.gooGetProduct(c, id)
	if err != nil || !gooXCVisible(p) {
		c.Data(http.StatusNotFound, "text/html; charset=utf-8", []byte(`<p class="woocommerce-info">This product is not available.</p>`))
		return true
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(s.gooQVSummary(c, p)))
	return true
}

// gooQVSummary is the modal's content for a product.
func (s *Server) gooQVSummary(c *gin.Context, p *gooProduct) template.HTML {
	ctx := context.Context(c)
	var b strings.Builder
	link := s.gooPermalink(ctx, p)
	b.WriteString(`<div class="goo-quick-view-product product type-product product-type-` + gooEsc(p.Type) + `" data-product_id="` + itoa64(p.ID) + `">`)

	// Gallery: the image and the gallery images, as thumbnails that swap the main image.
	ids := []int64{}
	if p.ImageID > 0 {
		ids = append(ids, p.ImageID)
	}
	for _, g := range p.GalleryIDs {
		if g > 0 && g != p.ImageID {
			ids = append(ids, g)
		}
	}
	b.WriteString(`<div class="woocommerce-product-gallery images goo-quick-view-gallery">`)
	if len(ids) == 0 {
		b.WriteString(`<div class="woocommerce-product-gallery__image--placeholder"><img src="` + gooEsc(s.gooXCPlaceholder(ctx)) + `" alt="Awaiting product image" class="wp-post-image"></div>`)
	} else {
		full, medium := s.gooXCImageURL(ctx, ids[0], ""), s.gooXCImageURL(ctx, ids[0], "large")
		b.WriteString(`<figure class="woocommerce-product-gallery__wrapper"><div class="woocommerce-product-gallery__image"><a href="` + gooEsc(full) +
			`" class="goo-quick-view-zoom" data-goo-qv-lightbox><img src="` + gooEsc(medium) + `" class="wp-post-image goo-quick-view-main" alt="` + gooEsc(p.Name) + `"></a></div></figure>`)
		if len(ids) > 1 {
			b.WriteString(`<ol class="flex-control-nav flex-control-thumbs">`)
			for i, id := range ids {
				th, lg, fu := s.gooXCImageURL(ctx, id, "thumbnail"), s.gooXCImageURL(ctx, id, "large"), s.gooXCImageURL(ctx, id, "")
				cur := ""
				if i == 0 {
					cur = ` aria-current="true"`
				}
				b.WriteString(fmt.Sprintf(`<li><button type="button" class="goo-quick-view-thumb" data-large="%s" data-full="%s"%s aria-label="Show image %d"><img src="%s" alt=""></button></li>`,
					gooEsc(lg), gooEsc(fu), cur, i+1, gooEsc(th)))
			}
			b.WriteString(`</ol>`)
		}
	}
	b.WriteString(`</div>`)

	b.WriteString(`<div class="summary entry-summary">`)
	b.WriteString(`<h2 class="product_title entry-title" id="goo-quick-view-title">` + gooEsc(p.Name) + `</h2>`)
	if r := gooXCRatingHTML(p.AverageRating, p.RatingCount); r != "" {
		reviews := "customer reviews"
		if p.RatingCount == 1 {
			reviews = "customer review"
		}
		b.WriteString(`<div class="woocommerce-product-rating">` + string(r) + `<a href="` + gooEsc(link) + `#reviews" class="woocommerce-review-link" rel="nofollow">(<span class="count">` +
			strconv.Itoa(p.RatingCount) + `</span> ` + reviews + `)</a></div>`)
	}
	b.WriteString(`<p class="price">` + string(s.gooXCPriceHTML(ctx, p)) + `</p>`)
	if p.Post != nil && strings.TrimSpace(p.Post.Excerpt) != "" {
		b.WriteString(`<div class="woocommerce-product-details__short-description">` + p.Post.Excerpt + `</div>`)
	}
	b.WriteString(string(s.gooQVCartForm(c, p, link)))

	// Meta.
	b.WriteString(`<div class="product_meta">`)
	if p.SKU != "" {
		b.WriteString(`<span class="sku_wrapper">SKU: <span class="sku">` + gooEsc(p.SKU) + `</span></span> `)
	}
	if terms, err := s.store.TermsForPosts(ctx, []int64{p.ID}, "product_cat"); err == nil && len(terms[p.ID]) > 0 {
		label := "Category: "
		if len(terms[p.ID]) > 1 {
			label = "Categories: "
		}
		var links []string
		for _, t := range terms[p.ID] {
			links = append(links, `<a href="`+gooEsc(s.links(ctx).HomeURL("/?product_cat="+urlEscape(t.Slug)))+`" rel="tag">`+gooEsc(t.Name)+`</a>`)
		}
		b.WriteString(`<span class="posted_in">` + label + strings.Join(links, ", ") + `</span>`)
	}
	b.WriteString(`</div>`)
	b.WriteString(`<p class="goo-quick-view-details"><a href="` + gooEsc(link) + `">View full details</a></p>`)
	b.WriteString(`</div></div>`)
	return template.HTML(b.String())
}

// gooQVCartForm is the add-to-cart part of the summary.
func (s *Server) gooQVCartForm(c *gin.Context, p *gooProduct, link string) template.HTML {
	ctx := context.Context(c)
	avail, cls := s.gooXCAvailability(ctx, p)
	stock := `<p class="stock ` + cls + `">` + gooEsc(avail) + `</p>`
	switch p.Type {
	case gooTypeSimple:
		if !s.gooIsPurchasable(ctx, p) {
			return ""
		}
		if !s.gooIsInStock(ctx, p) {
			return template.HTML(stock)
		}
		max := ""
		if q := s.gooStockQty(ctx, p); q > 0 && !s.gooBackordersAllowed(ctx, p) {
			max = ` max="` + strconv.Itoa(q) + `"`
		}
		qty := `<div class="quantity"><label class="screen-reader-text" for="goo-qv-qty-` + itoa64(p.ID) + `">` + gooEsc(p.Name) + ` quantity</label>` +
			`<input type="number" id="goo-qv-qty-` + itoa64(p.ID) + `" class="input-text qty text" name="quantity" value="1" min="1"` + max + ` step="1" inputmode="numeric" autocomplete="off"></div>`
		if p.SoldIndividually {
			qty = `<input type="hidden" name="quantity" value="1">`
		}
		return template.HTML(stock + `<form class="cart" action="` + gooEsc(link) + `" method="post" enctype="multipart/form-data">` + qty +
			`<button type="submit" name="add-to-cart" value="` + itoa64(p.ID) + `" class="single_add_to_cart_button button alt">` + gooEsc(s.gooXCAddToCartText(p, true)) + `</button></form>`)
	case gooTypeVariable:
		vars, _ := s.gooVariations(ctx, p.ID, false)
		type jsVar struct {
			ID         int64             `json:"variation_id"`
			Attributes map[string]string `json:"attributes"`
			Price      string            `json:"price_html"`
			InStock    bool              `json:"is_in_stock"`
			Purchase   bool              `json:"is_purchasable"`
		}
		var list []jsVar
		for _, v := range vars {
			v.parent = p
			attrs := map[string]string{}
			for _, a := range p.Attributes {
				if a.Variation {
					attrs["attribute_"+a.Key()] = v.VariationAttrs[a.Key()]
				}
			}
			list = append(list, jsVar{ID: v.ID, Attributes: attrs, Price: string(s.gooXCPriceHTML(ctx, v)), InStock: s.gooIsInStock(ctx, v), Purchase: s.gooIsPurchasable(ctx, v)})
		}
		if len(list) == 0 {
			return template.HTML(`<p class="stock out-of-stock">This product is currently out of stock and unavailable.</p>`)
		}
		raw, _ := json.Marshal(list)
		var b strings.Builder
		b.WriteString(`<form class="variations_form cart" action="` + gooEsc(link) + `" method="post" enctype="multipart/form-data" data-product_id="` + itoa64(p.ID) +
			`" data-product_variations="` + gooEsc(string(raw)) + `"><table class="variations" role="presentation"><tbody>`)
		for _, a := range p.Attributes {
			if !a.Variation {
				continue
			}
			name := "attribute_" + a.Key()
			id := "goo-qv-" + itoa64(p.ID) + "-" + gooSanitizeKey(a.Key())
			b.WriteString(`<tr><th class="label"><label for="` + gooEsc(id) + `">` + gooEsc(s.gooAttrLabel(ctx, a)) + `</label></th><td class="value"><select id="` + gooEsc(id) +
				`" name="` + gooEsc(name) + `" data-attribute_name="` + gooEsc(name) + `"><option value="">Choose an option</option>`)
			for _, o := range a.Options {
				sel := ""
				if p.DefaultAttributes[a.Key()] == o {
					sel = " selected"
				}
				b.WriteString(`<option value="` + gooEsc(o) + `"` + sel + `>` + gooEsc(s.gooAttrValueLabel(ctx, a, o)) + `</option>`)
			}
			b.WriteString(`</select></td></tr>`)
		}
		b.WriteString(`</tbody></table><div class="single_variation_wrap"><div class="woocommerce-variation single_variation" aria-live="polite"></div>` +
			`<div class="woocommerce-variation-add-to-cart variations_button"><div class="quantity"><label class="screen-reader-text" for="goo-qv-qty-` + itoa64(p.ID) + `">` +
			gooEsc(p.Name) + ` quantity</label><input type="number" id="goo-qv-qty-` + itoa64(p.ID) + `" class="input-text qty text" name="quantity" value="1" min="1" step="1" inputmode="numeric"></div>` +
			`<button type="submit" class="single_add_to_cart_button button alt disabled" disabled>` + gooEsc(s.gooXCAddToCartText(p, true)) + `</button>` +
			`<input type="hidden" name="add-to-cart" value="` + itoa64(p.ID) + `"><input type="hidden" name="product_id" value="` + itoa64(p.ID) + `"><input type="hidden" name="variation_id" class="variation_id" value="0">` +
			`</div></div></form>`)
		return template.HTML(b.String())
	case gooTypeExternal:
		if p.ProductURL == "" {
			return ""
		}
		return template.HTML(`<form class="cart" action="` + gooEsc(p.ProductURL) + `" method="get"><button type="submit" class="single_add_to_cart_button button alt">` +
			gooEsc(s.gooXCAddToCartText(p, true)) + `</button></form>`)
	}
	// Grouped products and module types (bundles, bookings, …) are chosen on the full page.
	return template.HTML(`<p class="goo-quick-view-options"><a class="button" href="` + gooEsc(link) + `">` + gooEsc(s.gooXCAddToCartText(p, false)) + `</a></p>`)
}
