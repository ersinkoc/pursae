package server

import (
	"context"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Goocommerce Product Compare (WooCommerce Products Compare): "Compare" buttons on the single product
// and in the catalog collect up to N products in the session (Extra["compare"]); a sticky bar in the
// footer lists them and links to the "Compare products" page, whose [goocommerce_compare] shortcode
// renders the comparison table. Toggling works without JavaScript (a POST with goo_module=compare) and
// through /?goo-ajax=compare_toggle.

const gooCmpKey = "compare"

var gooCmpFieldOpts = []gooOpt{
	{"image", "Image"}, {"title", "Title"}, {"price", "Price"}, {"rating", "Rating"}, {"availability", "Availability"},
	{"sku", "SKU"}, {"weight", "Weight"}, {"dimensions", "Dimensions"}, {"attributes", "Attributes"}, {"description", "Description"},
	{"add_to_cart", "Add to cart"},
}

func init() {
	all := make([]any, 0, len(gooCmpFieldOpts)) // []any: what the settings screen's multiselect reads
	for _, o := range gooCmpFieldOpts {
		all = append(all, o.Value)
	}
	gooRegisterExtension(&gooExtension{
		Key:         gooCmpKey,
		Name:        "Product Compare",
		Description: "Let customers compare products side by side: price, rating, stock, SKU, weight, dimensions and attributes, on a \"Compare products\" page.",
		Icon:        "dashicons-columns",
		Category:    "Merchandising",
		Fields: []gooField{
			{ID: "general", Title: "Compare options", Type: "title"},
			{ID: "button_text", Title: "Button text", Type: "text", Default: "Compare"},
			{ID: "max_products", Title: "Maximum products", Type: "number", Default: float64(4), Min: "2", Step: "1", Desc: "How many products can be compared at once."},
			{ID: "show_in_loops", Title: "Catalog", Type: "checkbox", Default: true, Desc: "Show the Compare button in product lists"},
			{ID: "fields", Title: "Fields shown", Type: "multiselect", Default: all, Options: gooCmpFieldOpts, Desc: "The rows of the comparison table."},
			{ID: "general_end", Type: "sectionend"},
		},
		OnActivate: func(s *Server, ctx context.Context) error {
			_, err := s.gooXCEnsurePage(ctx, "compare", "compare", "Compare products", "[goocommerce_compare]")
			return err
		},
	})
	gooOnHTML("single_summary_end", func(s *Server, c *gin.Context, p *gooProduct) template.HTML { return s.gooCmpButton(c, p, true) })
	gooOnHTML("loop_after_item", func(s *Server, c *gin.Context, p *gooProduct) template.HTML { return s.gooCmpButton(c, p, false) })
	gooOnPage("footer", gooCmpFooter)
	gooFrontRoutes = append(gooFrontRoutes, gooCmpRoute)
	gooContentFilters = append(gooContentFilters, gooCmpShortcode)
}

// gooXCEnsurePage creates a module's store page (once) and stores its id as the option <key>_page_id,
// so s.gooPageID(key) / s.gooPageURL(ctx, key) find it like the core pages.
func (s *Server) gooXCEnsurePage(ctx context.Context, key, slug, title, shortcode string) (int64, error) {
	if id := s.gooPageID(key); id > 0 {
		if p, err := s.store.GetPost(ctx, id); err == nil && p.Status != "trash" {
			return id, nil
		}
	}
	var id int64
	_ = s.store.Pool.QueryRow(ctx, `SELECT id FROM posts WHERE type = 'page' AND status = 'publish' AND content LIKE '%' || $1 || '%' ORDER BY id LIMIT 1`, shortcode).Scan(&id)
	if id == 0 {
		var author int64
		if gc, ok := ctx.(*gin.Context); ok {
			if u := currentUser(gc); u != nil {
				author = u.ID
			}
		}
		if author == 0 {
			_ = s.store.Pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&author)
		}
		p := &store.Post{Type: "page", Slug: slug, Title: title, Content: "<!-- wp:shortcode -->" + shortcode + "<!-- /wp:shortcode -->", Status: "publish",
			AuthorID: author, CommentStatus: "closed", PingStatus: "closed", Meta: map[string]any{}}
		if err := s.store.InsertPost(ctx, p); err != nil {
			return 0, err
		}
		id = p.ID
	}
	return id, s.gooSetOpts(ctx, map[string]any{key + "_page_id": float64(id)})
}

// gooXCSessionIDs reads a list of product ids from the session's module data.
func gooXCSessionIDs(sess *gooSession, key string) []int64 {
	var out []int64
	switch v := sess.Data.Extra[key].(type) {
	case []any:
		for _, x := range v {
			if f, ok := x.(float64); ok && f > 0 {
				out = append(out, int64(f))
			}
		}
	case []int64:
		out = append(out, v...)
	}
	return out
}

// gooXCSetSessionIDs stores a list of product ids in the session's module data.
func gooXCSetSessionIDs(sess *gooSession, key string, ids []int64) {
	if sess.Data.Extra == nil {
		sess.Data.Extra = map[string]any{}
	}
	if len(ids) == 0 {
		delete(sess.Data.Extra, key)
	} else {
		list := make([]any, 0, len(ids))
		for _, id := range ids {
			list = append(list, float64(id))
		}
		sess.Data.Extra[key] = list
	}
	sess.touch()
}

// gooXCStringList reads a multiselect setting.
func gooXCStringList(v any) []string {
	switch l := v.(type) {
	case []string:
		return l
	case []any:
		var out []string
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if l != "" {
			return strings.Split(l, ",")
		}
	}
	return nil
}

// Compare list --------------------------------------------------------------------------------------------

// gooCmpMax is the maximum number of compared products.
func (s *Server) gooCmpMax() int {
	if n := s.gooExtInt(gooCmpKey, "max_products"); n >= 2 {
		return n
	}
	return 4
}

// gooCmpList is the visitor's compare list (product ids, oldest first).
func (s *Server) gooCmpList(c *gin.Context) []int64 {
	return gooXCSessionIDs(s.gooSessionFor(c), gooCmpKey)
}

// gooCmpToggle adds or removes a product (add: true adds, false removes; nil toggles). It returns
// whether the product is in the list afterwards and an error message (the list is full).
func (s *Server) gooCmpToggle(c *gin.Context, productID int64, add *bool) (bool, string) {
	ids := s.gooCmpList(c)
	in := gooInt64In(productID, ids)
	want := !in
	if add != nil {
		want = *add
	}
	if want == in {
		return in, ""
	}
	sess := s.gooSessionFor(c)
	if !want {
		var keep []int64
		for _, id := range ids {
			if id != productID {
				keep = append(keep, id)
			}
		}
		gooXCSetSessionIDs(sess, gooCmpKey, keep)
		return false, ""
	}
	p, err := s.gooGetProduct(c, productID)
	if err != nil || !gooXCVisible(p) {
		return false, "This product cannot be compared."
	}
	if len(ids) >= s.gooCmpMax() {
		return false, "You can compare up to " + strconv.Itoa(s.gooCmpMax()) + " products. Remove one to add another."
	}
	gooXCSetSessionIDs(sess, gooCmpKey, append(ids, productID))
	return true, ""
}

// gooCmpProducts loads the compared products in list order (dropping ones that are gone).
func (s *Server) gooCmpProducts(ctx context.Context, ids []int64) []*gooProduct {
	m, _ := s.gooGetProducts(ctx, ids)
	var out []*gooProduct
	for _, id := range ids {
		if p := m[id]; gooXCVisible(p) {
			out = append(out, p)
		}
	}
	return out
}

// Front ---------------------------------------------------------------------------------------------------

// gooCmpButton is the Compare button (a small form, so it works without JavaScript).
func (s *Server) gooCmpButton(c *gin.Context, p *gooProduct, single bool) template.HTML {
	if !s.gooExtOn(gooCmpKey) || !gooXCVisible(p) || (!single && !s.gooExtBool(gooCmpKey, "show_in_loops")) {
		return ""
	}
	in := gooInt64In(p.ID, s.gooCmpList(c))
	action, cls, pressed := "add", "button goo-compare-button", "false"
	if in {
		action, cls, pressed = "remove", cls+" added", "true"
	}
	text := defaultStr(s.gooExtString(gooCmpKey, "button_text"), "Compare")
	return template.HTML(`<form class="goo-compare-form" method="post" action="">` +
		`<input type="hidden" name="goo_module" value="compare"><input type="hidden" name="goo_compare_action" value="` + action + `">` +
		`<input type="hidden" name="product_id" value="` + itoa64(p.ID) + `">` +
		`<button type="submit" class="` + cls + `" data-product_id="` + itoa64(p.ID) + `" aria-pressed="` + pressed + `" aria-label="` + gooEsc(text+": "+p.Name) + `">` +
		gooEsc(text) + `</button></form>`)
}

// gooCmpBar is the sticky bar listing the compared products.
func (s *Server) gooCmpBar(c *gin.Context) template.HTML {
	ctx := context.Context(c)
	prods := s.gooCmpProducts(ctx, s.gooCmpList(c))
	hidden := ""
	if len(prods) == 0 {
		hidden = " hidden"
	}
	var b strings.Builder
	b.WriteString(`<div class="goo-compare-bar" id="goo-compare-bar" role="region" aria-label="Products to compare"` + hidden + `><div class="goo-compare-bar-inner"><ul class="goo-compare-bar-items">`)
	for _, p := range prods {
		b.WriteString(`<li class="goo-compare-bar-item"><a href="` + gooEsc(s.gooPermalink(ctx, p)) + `" title="` + gooEsc(p.Name) + `">` + string(s.gooXCThumb(ctx, p, "thumbnail")) +
			`<span class="screen-reader-text">` + gooEsc(p.Name) + `</span></a>` +
			`<form method="post" action="" class="goo-compare-form"><input type="hidden" name="goo_module" value="compare"><input type="hidden" name="goo_compare_action" value="remove">` +
			`<input type="hidden" name="product_id" value="` + itoa64(p.ID) + `"><button type="submit" class="goo-compare-remove" data-product_id="` + itoa64(p.ID) +
			`" aria-label="` + gooEsc("Remove "+p.Name+" from comparison") + `">&times;</button></form></li>`)
	}
	b.WriteString(`</ul><div class="goo-compare-bar-actions"><a class="button alt goo-compare-now" href="` + gooEsc(s.gooPageURL(ctx, "compare")) + `">Compare now <span class="count">(` +
		strconv.Itoa(len(prods)) + `)</span></a>` +
		`<form method="post" action="" class="goo-compare-form"><input type="hidden" name="goo_module" value="compare"><input type="hidden" name="goo_compare_action" value="clear">` +
		`<button type="submit" class="button goo-compare-clear">Clear</button></form></div></div></div>`)
	return template.HTML(b.String())
}

// gooCmpFooter prints the compare bar and the module's assets.
func gooCmpFooter(s *Server, c *gin.Context) template.HTML {
	if !s.gooExtOn(gooCmpKey) {
		return ""
	}
	return template.HTML(`<link rel="stylesheet" id="goo-ext-compare-css" href="`+gooAssets+`/css/ext-compare.css?ver=`+gooAssetVer()+`" media="all">`) +
		s.gooCmpBar(c) +
		template.HTML(`<script src="`+gooAssets+`/js/ext-compare.js?ver=`+gooAssetVer()+`" id="goo-ext-compare-js" data-endpoint="`+
			gooEsc(s.links(c).HomeURL("/?goo-ajax=compare_toggle"))+`" defer></script>`)
}

// gooCmpRoute handles /?goo-ajax=compare_toggle (JSON) and the no-JS form (goo_module=compare).
func gooCmpRoute(s *Server, c *gin.Context, path string) bool {
	ajax := c.Query("goo-ajax") == "compare_toggle"
	form := c.Request.Method == http.MethodPost && c.PostForm("goo_module") == gooCmpKey
	if (!ajax && !form) || !s.gooExtOn(gooCmpKey) {
		return false
	}
	pid, _ := strconv.ParseInt(defaultStr(c.PostForm("product_id"), c.Query("product_id")), 10, 64)
	action := defaultStr(c.PostForm("goo_compare_action"), c.Query("goo_compare_action"))
	var in bool
	var msg string
	switch action {
	case "clear":
		gooXCSetSessionIDs(s.gooSessionFor(c), gooCmpKey, nil)
	case "add":
		t := true
		in, msg = s.gooCmpToggle(c, pid, &t)
	case "remove":
		f := false
		in, msg = s.gooCmpToggle(c, pid, &f)
	default:
		in, msg = s.gooCmpToggle(c, pid, nil)
	}
	ids := s.gooCmpList(c)
	if ajax {
		s.gooSaveSession(c)
		out := gin.H{"success": msg == "", "in_compare": in, "count": len(ids), "ids": ids, "max": s.gooCmpMax(),
			"compare_url": s.gooPageURL(c, "compare"), "bar": string(s.gooCmpBar(c))}
		if msg != "" {
			out["message"] = msg
		}
		gooXCJSON(c, http.StatusOK, out)
		return true
	}
	if msg != "" {
		s.gooAddNotice(c, "error", gooEsc(msg))
	}
	s.gooSaveSession(c)
	back := gooXCSafeReferer(c, s.gooPageURL(c, "compare"))
	c.Redirect(http.StatusSeeOther, back)
	return true
}

// gooXCSafeReferer is the same-site page the form was posted from (else def).
func gooXCSafeReferer(c *gin.Context, def string) string {
	ref := c.PostForm("_wp_http_referer")
	if ref == "" {
		ref = c.Request.Referer()
	}
	if ref == "" {
		return def
	}
	// Browsers read "\" as "/" and drop tabs and newlines, so "/\evil.example" or "http:\\evil.example"
	// would leave the site although url.Parse sees no host.
	if strings.Contains(ref, "\\") || hasControlChar(ref) {
		return def
	}
	u, err := url.Parse(ref)
	if err != nil {
		return def
	}
	if u.Host == "" && u.Scheme == "" {
		if !strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "//") {
			return def
		}
		return ref // a path on this site
	}
	if u.Host != c.Request.Host || (u.Scheme != "http" && u.Scheme != "https") {
		return def
	}
	return ref
}

// gooCmpShortcode expands [goocommerce_compare] (the "Compare products" page).
func gooCmpShortcode(s *Server, ctx context.Context, content string) string {
	if !strings.Contains(content, "[goocommerce_compare") || !s.gooExtOn(gooCmpKey) {
		return content
	}
	c, ok := ctx.(*gin.Context)
	if !ok || c.Request == nil {
		return content
	}
	table := string(s.gooCmpTable(c))
	for _, tag := range []string{"<p>[goocommerce_compare]</p>", "[goocommerce_compare]"} {
		content = strings.ReplaceAll(content, tag, table)
	}
	return content
}

// gooCmpTable is the comparison table.
func (s *Server) gooCmpTable(c *gin.Context) template.HTML {
	ctx := context.Context(c)
	prods := s.gooCmpProducts(ctx, s.gooCmpList(c))
	if len(prods) == 0 {
		return template.HTML(`<div class="woocommerce goo-compare"><p class="woocommerce-info cart-empty">No products to compare yet. Use the Compare button on products to add them here.</p>` +
			`<p class="return-to-shop"><a class="button wc-backward" href="` + gooEsc(s.gooShopURL(ctx)) + `">Return to shop</a></p></div>`)
	}
	fields := gooXCStringList(s.gooExtSettings(gooCmpKey)["fields"])
	if len(fields) == 0 {
		for _, o := range gooCmpFieldOpts {
			fields = append(fields, o.Value)
		}
	}
	var b strings.Builder
	b.WriteString(`<div class="woocommerce goo-compare"><div class="goo-compare-table-wrap"><table class="shop_table goo-compare-table"><caption class="screen-reader-text">Product comparison</caption>`)
	// Header row: product names with a remove button.
	b.WriteString(`<thead><tr><td class="goo-compare-label"></td>`)
	for _, p := range prods {
		b.WriteString(`<th scope="col" class="goo-compare-product product-` + itoa64(p.ID) + `">` +
			`<form method="post" action="" class="goo-compare-form"><input type="hidden" name="goo_module" value="compare"><input type="hidden" name="goo_compare_action" value="remove">` +
			`<input type="hidden" name="product_id" value="` + itoa64(p.ID) + `"><button type="submit" class="goo-compare-remove remove" aria-label="` + gooEsc("Remove "+p.Name+" from comparison") + `">&times; Remove</button></form></th>`)
	}
	b.WriteString(`</tr></thead><tbody>`)
	row := func(key, label string, cell func(p *gooProduct) string) {
		b.WriteString(`<tr class="goo-compare-row goo-compare-` + gooEsc(key) + `"><th scope="row" class="goo-compare-label">` + gooEsc(label) + `</th>`)
		for _, p := range prods {
			b.WriteString(`<td>` + cell(p) + `</td>`)
		}
		b.WriteString(`</tr>`)
	}
	wu, du := defaultStr(s.gooOptString("weight_unit"), "kg"), defaultStr(s.gooOptString("dimension_unit"), "cm")
	for _, f := range fields {
		switch f {
		case "image":
			row(f, "Image", func(p *gooProduct) string {
				return `<a href="` + gooEsc(s.gooPermalink(ctx, p)) + `">` + string(s.gooXCThumb(ctx, p, "woocommerce_thumbnail")) + `</a>`
			})
		case "title":
			row(f, "Product", func(p *gooProduct) string {
				return `<a href="` + gooEsc(s.gooPermalink(ctx, p)) + `" class="goo-compare-title">` + gooEsc(p.Name) + `</a>`
			})
		case "price":
			row(f, "Price", func(p *gooProduct) string {
				return `<span class="price">` + string(s.gooXCPriceHTML(ctx, p)) + `</span>`
			})
		case "rating":
			row(f, "Rating", func(p *gooProduct) string {
				if r := gooXCRatingHTML(p.AverageRating, p.RatingCount); r != "" {
					return string(r) + ` <span class="count">(` + strconv.Itoa(p.RatingCount) + `)</span>`
				}
				return `<span class="goo-compare-empty">&ndash;</span>`
			})
		case "availability":
			row(f, "Availability", func(p *gooProduct) string {
				txt, cls := s.gooXCAvailability(ctx, p)
				return `<p class="stock ` + cls + `">` + gooEsc(txt) + `</p>`
			})
		case "sku":
			row(f, "SKU", func(p *gooProduct) string { return gooEsc(defaultStr(p.SKU, "–")) })
		case "weight":
			row(f, "Weight", func(p *gooProduct) string {
				if strings.TrimSpace(p.Weight) == "" {
					return "&ndash;"
				}
				return gooEsc(p.Weight + " " + wu)
			})
		case "dimensions":
			row(f, "Dimensions", func(p *gooProduct) string {
				var parts []string
				for _, d := range []string{p.Length, p.Width, p.Height} {
					if strings.TrimSpace(d) != "" {
						parts = append(parts, d)
					}
				}
				if len(parts) == 0 {
					return "&ndash;"
				}
				return gooEsc(strings.Join(parts, " × ") + " " + du)
			})
		case "attributes":
			// One row per visible attribute found on any compared product.
			type attrRow struct {
				key, label string
			}
			var attrs []attrRow
			seen := map[string]bool{}
			for _, p := range prods {
				for _, a := range p.Attributes {
					if !a.Visible && !a.Variation {
						continue
					}
					if !seen[a.Key()] {
						seen[a.Key()] = true
						attrs = append(attrs, attrRow{a.Key(), s.gooAttrLabel(ctx, a)})
					}
				}
			}
			for _, ar := range attrs {
				ar := ar
				row("attribute-"+ar.key, ar.label, func(p *gooProduct) string {
					for _, a := range p.Attributes {
						if a.Key() == ar.key {
							var vals []string
							for _, o := range a.Options {
								vals = append(vals, s.gooAttrValueLabel(ctx, a, o))
							}
							return gooEsc(strings.Join(vals, ", "))
						}
					}
					return "&ndash;"
				})
			}
		case "description":
			row(f, "Description", func(p *gooProduct) string {
				txt := ""
				if p.Post != nil {
					txt = p.Post.Excerpt
					if strings.TrimSpace(wp.StripTags(txt)) == "" {
						txt = p.Post.Content
					}
				}
				return wp.TrimWords(txt, 30, "&hellip;")
			})
		case "add_to_cart":
			row(f, "Add to cart", func(p *gooProduct) string { return string(s.gooXCLoopAddToCart(ctx, p)) })
		}
	}
	b.WriteString(`</tbody></table></div>`)
	b.WriteString(`<form method="post" action="" class="goo-compare-form goo-compare-clear-form"><input type="hidden" name="goo_module" value="compare">` +
		`<input type="hidden" name="goo_compare_action" value="clear"><button type="submit" class="button goo-compare-clear">Clear all</button></form></div>`)
	return template.HTML(b.String())
}

// gooXCLoopAddToCart is woocommerce_template_loop_add_to_cart(): ?add-to-cart= for simple
// products, a link to the product page for the others.
func (s *Server) gooXCLoopAddToCart(ctx context.Context, p *gooProduct) template.HTML {
	if p.Type == gooTypeSimple && s.gooIsPurchasable(ctx, p) && s.gooIsInStock(ctx, p) {
		return template.HTML(`<a href="` + gooEsc(s.gooPermalink(ctx, p)+"?add-to-cart="+itoa64(p.ID)) + `" data-quantity="1" class="button product_type_simple add_to_cart_button ajax_add_to_cart" data-product_id="` +
			itoa64(p.ID) + `" data-product_sku="` + gooEsc(p.SKU) + `" aria-label="` + gooEsc("Add to cart: “"+p.Name+"”") + `" rel="nofollow">` + gooEsc(s.gooXCAddToCartText(p, false)) + `</a>`)
	}
	href := s.gooPermalink(ctx, p)
	if p.Type == gooTypeExternal && p.ProductURL != "" {
		href = p.ProductURL
	}
	text := s.gooXCAddToCartText(p, false)
	if p.Type == gooTypeSimple {
		text = "Read more"
	}
	return template.HTML(`<a href="` + gooEsc(href) + `" class="button product_type_` + gooEsc(p.Type) + `" data-product_id="` + itoa64(p.ID) + `" rel="nofollow">` + gooEsc(text) + `</a>`)
}
