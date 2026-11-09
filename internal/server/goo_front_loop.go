package server

import (
	"context"
	"fmt"
	"html/template"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// The product loop and the archives: get_price_html(), wc_get_rating_html(), stock HTML, content-product.php,
// archive-product.php (result count, ordering, pagination, subcategories), the catalog filters and widgets.

// Prices --------------------------------------------------------------------------------------------------

// gooDisplayLoc is the tax location shop prices are shown for: the customer's when the session knows it.
func (s *Server) gooDisplayLoc(c *gin.Context) gooLocation {
	if v, ok := c.Get("goo.displayloc"); ok {
		return v.(gooLocation)
	}
	loc := s.gooBaseLocation()
	if s.gooTaxEnabled() && (s.gooHasSessionCookie(c) || currentUser(c) != nil) {
		if l := s.gooCartFor(c).TaxLocation; l.Country != "" {
			loc = l
		}
	}
	c.Set("goo.displayloc", loc)
	return loc
}

// gooToDisplay is wc_get_price_to_display() for an amount of a product.
func (s *Server) gooToDisplay(c *gin.Context, p *gooProduct, price float64) float64 {
	return s.gooPriceToDisplay(c.Request.Context(), p, price, s.gooDisplayLoc(c))
}

// gooActive is the product's catalog price after the modules' price hooks, nil without a price.
func (s *Server) gooActive(c *gin.Context, p *gooProduct) *float64 {
	if !p.HasPrice() {
		return nil
	}
	v := s.gooDisplayPrice(c.Request.Context(), p)
	return &v
}

// gooPriceHTML is get_price_html() (with the modules' price HTML filters).
func (s *Server) gooPriceHTML(c *gin.Context, p *gooProduct) template.HTML {
	ctx := c.Request.Context()
	var h string
	switch p.Type {
	case gooTypeVariable, gooSubsTypeVariable:
		h = s.gooVariablePriceHTML(c, p)
	case gooTypeGrouped:
		kids, _ := s.gooGetProducts(ctx, p.Children)
		var lo, hi *float64
		for _, k := range kids {
			if k.Status != "publish" {
				continue
			}
			pr := s.gooActive(c, k)
			if pr == nil {
				continue
			}
			d := s.gooToDisplay(c, k, *pr)
			if lo == nil || d < *lo {
				lo = gooFloatPtr(d)
			}
			if hi == nil || d > *hi {
				hi = gooFloatPtr(d)
			}
		}
		if lo != nil {
			if *lo != *hi {
				h = s.gooFormatPriceRange(*lo, *hi)
			} else {
				h = string(s.gooPrice(*lo))
			}
			h += s.gooPriceSuffix(c, p, *lo)
		}
	default:
		active := s.gooActive(c, p)
		if active == nil {
			break
		}
		d := s.gooToDisplay(c, p, *active)
		if p.IsOnSale() && p.RegularPrice != nil {
			h = s.gooSaleHTML(s.gooToDisplay(c, p, *p.RegularPrice), d)
		} else {
			h = string(s.gooPrice(d))
		}
		h += s.gooPriceSuffix(c, p, *active)
	}
	return s.gooFilterPriceHTML(ctx, p, template.HTML(h))
}

// gooSaleHTML is wc_format_sale_price().
func (s *Server) gooSaleHTML(regular, sale float64) string {
	return `<del aria-hidden="true">` + string(s.gooPrice(regular)) + `</del> <span class="screen-reader-text">Original price was: ` + s.gooPricePlain(regular, s.gooCurrency()) +
		`.</span><ins aria-hidden="true">` + string(s.gooPrice(sale)) + `</ins><span class="screen-reader-text">Current price is: ` + s.gooPricePlain(sale, s.gooCurrency()) + `.</span>`
}

// gooPriceRange is wc_format_price_range().
func (s *Server) gooFormatPriceRange(lo, hi float64) string {
	return `<span aria-hidden="true">` + string(s.gooPrice(lo)) + ` &ndash; ` + string(s.gooPrice(hi)) + `</span><span class="screen-reader-text">Price range: ` +
		s.gooPricePlain(lo, s.gooCurrency()) + ` through ` + s.gooPricePlain(hi, s.gooCurrency()) + `</span>`
}

// gooVariablePriceHTML is WC_Product_Variable::get_price_html().
func (s *Server) gooVariablePriceHTML(c *gin.Context, p *gooProduct) string {
	vars := s.gooVariationsCached(c, p)
	var lo, hi, rlo, rhi *float64
	for _, v := range vars {
		pr := s.gooActive(c, v)
		if pr == nil {
			continue
		}
		d := s.gooToDisplay(c, v, *pr)
		r := d
		if v.RegularPrice != nil {
			r = s.gooToDisplay(c, v, *v.RegularPrice)
		}
		if lo == nil || d < *lo {
			lo = gooFloatPtr(d)
		}
		if hi == nil || d > *hi {
			hi = gooFloatPtr(d)
		}
		if rlo == nil || r < *rlo {
			rlo = gooFloatPtr(r)
		}
		if rhi == nil || r > *rhi {
			rhi = gooFloatPtr(r)
		}
	}
	if lo == nil {
		return ""
	}
	var h string
	switch {
	case *lo != *hi:
		h = s.gooFormatPriceRange(*lo, *hi)
	case p.IsOnSale() || (*rlo == *rhi && *rlo != *lo):
		h = s.gooSaleHTML(*rlo, *lo)
	default:
		h = string(s.gooPrice(*lo))
	}
	return h + s.gooPriceSuffix(c, p, *lo)
}

// gooVariationsCached loads a variable product's enabled variations once per request.
func (s *Server) gooVariationsCached(c *gin.Context, p *gooProduct) []*gooProduct {
	key := "goo.vars." + itoa64(p.ID)
	if v, ok := c.Get(key); ok {
		return v.([]*gooProduct)
	}
	vars, _ := s.gooVariations(c.Request.Context(), p.ID, false)
	for _, v := range vars {
		v.parent = p
	}
	c.Set(key, vars)
	return vars
}

// variable products are on sale when a variation is.
func (s *Server) gooOnSale(c *gin.Context, p *gooProduct) bool {
	switch p.Type {
	case gooTypeVariable, gooSubsTypeVariable:
		for _, v := range s.gooVariationsCached(c, p) {
			if v.IsOnSale() {
				return true
			}
		}
		return false
	case gooTypeGrouped:
		kids, _ := s.gooGetProducts(c.Request.Context(), p.Children)
		for _, k := range kids {
			if k.Status == "publish" && k.IsOnSale() {
				return true
			}
		}
		return false
	}
	return p.IsOnSale()
}

// gooPriceSuffix is get_price_suffix(): the price display suffix with its placeholders.
func (s *Server) gooPriceSuffix(c *gin.Context, p *gooProduct, price float64) string {
	suffix := strings.TrimSpace(s.gooOptString("price_display_suffix"))
	if suffix == "" || !s.gooTaxEnabled() || p.TaxStatus != "taxable" {
		return ""
	}
	ctx := c.Request.Context()
	loc := s.gooDisplayLoc(c)
	suffix = strings.ReplaceAll(suffix, "{price_including_tax}", string(s.gooPrice(s.gooPriceIncludingTax(ctx, p, price, 1, loc))))
	suffix = strings.ReplaceAll(suffix, "{price_excluding_tax}", string(s.gooPrice(s.gooPriceExcludingTax(ctx, p, price, 1, loc))))
	return ` <small class="woocommerce-price-suffix">` + wp.KsesPost(suffix) + `</small>`
}

// Ratings, stock --------------------------------------------------------------------------------------------

// gooRatingHTML is wc_get_rating_html() (empty without a rating).
func gooRatingHTML(rating float64, count int) string {
	if rating <= 0 {
		return ""
	}
	label := "Rated " + gooFmtRating(rating) + " out of 5"
	return `<div class="star-rating" role="img" aria-label="` + label + `">` + gooStarSpan(rating, count) + `</div>`
}

func gooStarSpan(rating float64, count int) string {
	w := strconv.FormatFloat(math.Round(rating/5*10000)/100, 'f', -1, 64)
	if count > 0 {
		return `<span style="width:` + w + `%">Rated <strong class="rating">` + gooFmtRating(rating) + `</strong> out of 5 based on <span class="rating">` + strconv.Itoa(count) + `</span> customer rating` + map[bool]string{true: "", false: "s"}[count == 1] + `</span>`
	}
	return `<span style="width:` + w + `%">Rated <strong class="rating">` + gooFmtRating(rating) + `</strong> out of 5</span>`
}

func gooFmtRating(r float64) string { return strconv.FormatFloat(math.Round(r*100)/100, 'f', 2, 64) }

// gooAvailability is get_availability(): the stock text and class.
func (s *Server) gooAvailability(c *gin.Context, p *gooProduct) (string, string) {
	ctx := c.Request.Context()
	if gooSubsVariableLike(p.Type) || p.Type == gooTypeGrouped || p.Type == gooTypeExternal {
		if !s.gooIsInStock(ctx, p) && p.Type != gooTypeExternal {
			return "Out of stock", "out-of-stock"
		}
		return "", "in-stock"
	}
	if !s.gooIsInStock(ctx, p) {
		return "Out of stock", "out-of-stock"
	}
	managed := s.gooManagingStock(ctx, p)
	qty := s.gooStockQty(ctx, p)
	owner := s.gooStockOwner(ctx, p)
	if managed && qty <= 0 && s.gooBackordersAllowed(ctx, p) {
		if owner.Backorders == "notify" {
			return "Available on backorder", "available-on-backorder"
		}
		return "", "in-stock"
	}
	if !managed {
		if p.StockStatus == "onbackorder" {
			return "Available on backorder", "available-on-backorder"
		}
		return "", "in-stock"
	}
	low := s.gooOptInt("notify_low_stock_amount")
	if owner.LowStockAmount != nil {
		low = *owner.LowStockAmount
	}
	text := ""
	switch s.gooOptString("stock_format") {
	case "no_amount":
		text = "In stock"
	case "low_amount":
		if qty <= low {
			text = fmt.Sprintf("Only %d left in stock", qty)
		} else {
			text = "In stock"
		}
	default:
		text = fmt.Sprintf("%d in stock", qty)
	}
	if owner.Backorders == "notify" {
		text += " (can be backordered)"
	}
	return text, "in-stock"
}

// gooFrontStockHTML is wc_get_stock_html().
func (s *Server) gooFrontStockHTML(c *gin.Context, p *gooProduct) string {
	text, class := s.gooAvailability(c, p)
	if text == "" {
		return ""
	}
	return `<p class="stock ` + class + `">` + gooEsc(text) + `</p>`
}

// The loop ---------------------------------------------------------------------------------------------------

// gooProductClasses is wc_get_product_class().
func (s *Server) gooProductClasses(c *gin.Context, p *gooProduct, extra ...string) string {
	ctx := c.Request.Context()
	cl := append([]string{}, extra...)
	cl = append(cl, "product", "type-product", "post-"+itoa64(p.ID), "status-"+p.Status)
	st := p.StockStatus
	if st == "" {
		st = "instock"
	}
	if !s.gooIsInStock(ctx, p) {
		st = "outofstock"
	}
	cl = append(cl, st)
	terms, _ := s.gooProductTerms(c, p.ID)
	for _, t := range terms {
		switch t.Taxonomy {
		case "product_cat":
			cl = append(cl, "product_cat-"+t.Slug)
		case "product_tag":
			cl = append(cl, "product_tag-"+t.Slug)
		}
	}
	if p.ImageID > 0 {
		cl = append(cl, "has-post-thumbnail")
	}
	if s.gooOnSale(c, p) {
		cl = append(cl, "sale")
	}
	if p.Featured {
		cl = append(cl, "featured")
	}
	if p.Downloadable {
		cl = append(cl, "downloadable")
	}
	if p.Virtual {
		cl = append(cl, "virtual")
	}
	if p.TaxStatus != "" {
		cl = append(cl, "shipping-"+p.TaxStatus)
	}
	if s.gooIsPurchasable(ctx, p) || gooSubsVariableLike(p.Type) {
		cl = append(cl, "purchasable")
	}
	cl = append(cl, "product-type-"+p.Type)
	return strings.Join(cl, " ")
}

// gooProductTerms are a product's terms (all taxonomies), loaded once per request.
func (s *Server) gooProductTerms(c *gin.Context, id int64) ([]*store.Term, error) {
	key := "goo.terms." + itoa64(id)
	if v, ok := c.Get(key); ok {
		return v.([]*store.Term), nil
	}
	terms, err := s.gooAllTerms(c.Request.Context(), id)
	c.Set(key, terms)
	return terms, err
}

// gooAllTerms are a post's terms of every taxonomy (TermsForPosts needs one taxonomy).
func (s *Server) gooAllTerms(ctx context.Context, id int64) ([]*store.Term, error) {
	rows, err := s.store.Pool.Query(ctx, `SELECT t.id, t.taxonomy, coalesce(t.parent_id,0), t.slug, t.name, t.description, t.post_count
		FROM post_terms pt JOIN terms t ON t.id = pt.term_id WHERE pt.post_id = $1 ORDER BY t.taxonomy, lower(t.name)`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.Term
	for rows.Next() {
		t := &store.Term{Meta: map[string]any{}}
		if err := rows.Scan(&t.ID, &t.Taxonomy, &t.ParentID, &t.Slug, &t.Name, &t.Description, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// gooAddToCartText is add_to_cart_text() (single = single_add_to_cart_text()).
func (s *Server) gooAddToCartText(c *gin.Context, p *gooProduct, single bool) string {
	ctx := c.Request.Context()
	if def := gooProductTypes[p.Type]; def != nil && def.AddToCartText != nil && (def.Module == "" || s.gooExtOn(def.Module)) {
		return def.AddToCartText(s, p, single)
	}
	switch p.Type {
	case gooTypeExternal:
		return defaultStr(p.ButtonText, "Buy product")
	case gooTypeGrouped:
		if single {
			return "Add to cart"
		}
		return "View products"
	case gooTypeVariable, gooSubsTypeVariable:
		if single {
			return "Add to cart"
		}
		if s.gooIsInStock(ctx, p) && p.Price != nil {
			return "Select options"
		}
		return "Read more"
	}
	if single {
		return "Add to cart"
	}
	if s.gooIsPurchasable(ctx, p) && s.gooIsInStock(ctx, p) {
		return "Add to cart"
	}
	return "Read more"
}

// gooLoopAddToCart is woocommerce_template_loop_add_to_cart().
func (s *Server) gooLoopAddToCart(c *gin.Context, p *gooProduct, extraClass string) string {
	ctx := c.Request.Context()
	link := s.gooPermalink(ctx, p)
	simple := !gooSubsVariableLike(p.Type) && p.Type != gooTypeGrouped && p.Type != gooTypeExternal
	buyable := simple && s.gooIsPurchasable(ctx, p) && s.gooIsInStock(ctx, p)
	if buyable {
		link = s.gooAddToCartURL(c, p)
	}
	if p.Type == gooTypeExternal && p.ProductURL != "" {
		link = p.ProductURL
	}
	classes := []string{"button", "product_type_" + p.Type}
	if extraClass != "" {
		classes = append(classes, extraClass)
	}
	if buyable {
		classes = append(classes, "add_to_cart_button")
		if s.gooOptBool("enable_ajax_add_to_cart") && !s.gooHasAddToCartHooks() {
			classes = append(classes, "ajax_add_to_cart")
		}
	}
	text := s.gooAddToCartText(c, p, false)
	desc := "woocommerce_loop_add_to_cart_link_describedby_" + itoa64(p.ID)
	aria := text + ": &ldquo;" + gooEsc(p.Name) + "&rdquo;"
	if buyable {
		aria = "Add to cart: &ldquo;" + gooEsc(p.Name) + "&rdquo;"
	}
	var b strings.Builder
	b.WriteString(`<a href="` + gooEsc(link) + `" aria-describedby="` + desc + `" data-quantity="1" class="` + strings.Join(classes, " ") + `" data-product_id="` + itoa64(p.ID) +
		`" data-product_sku="` + gooEsc(p.SKU) + `" aria-label="` + aria + `" rel="nofollow"`)
	if buyable {
		b.WriteString(` data-success_message="&ldquo;` + gooEsc(p.Name) + `&rdquo; has been added to your cart"`)
	}
	b.WriteString(`>` + gooEsc(text) + `</a>`)
	b.WriteString(`<span id="` + desc + `" class="screen-reader-text">`)
	if gooSubsVariableLike(p.Type) && buyable == false && s.gooIsInStock(ctx, p) {
		b.WriteString(`This product has multiple variants. The options may be chosen on the product page`)
	}
	b.WriteString(`</span>`)
	return b.String()
}

// gooHasAddToCartHooks: modules validating the posted form need the product page's form, not AJAX.
func (s *Server) gooHasAddToCartHooks() bool { return false }

// gooAddToCartURL is add_to_cart_url() of a simple product: the current page with ?add-to-cart=ID.
func (s *Server) gooAddToCartURL(c *gin.Context, p *gooProduct) string {
	u := *c.Request.URL
	q := u.Query()
	for _, k := range []string{"add-to-cart", "added-to-cart", "goo-ajax", "wc-ajax", "quantity"} {
		q.Del(k)
	}
	q.Set("add-to-cart", itoa64(p.ID))
	u.RawQuery = q.Encode()
	path := u.Path
	if path == "" {
		path = "/"
	}
	return s.links(c.Request.Context()).HomeURL(path + "?" + u.RawQuery)
}

// gooLoopItem is content-product.php.
func (s *Server) gooLoopItem(c *gin.Context, p *gooProduct, i, cols int) string {
	ctx := c.Request.Context()
	pos := ""
	if cols > 0 {
		switch {
		case i%cols == 0:
			pos = "first"
		case i%cols == cols-1:
			pos = "last"
		}
	}
	link := s.gooPermalink(ctx, p)
	var b strings.Builder
	b.WriteString(`<li class="` + s.gooProductClasses(c, p, strings.TrimSpace(pos)) + `">`)
	b.WriteString(`<a href="` + gooEsc(link) + `" class="woocommerce-LoopProduct-link woocommerce-loop-product__link">`)
	if s.gooOnSale(c, p) {
		b.WriteString(`<span class="onsale">Sale!</span>`)
	}
	b.WriteString(s.gooImageHTML(ctx, p.ImageID, "medium", "attachment-woocommerce_thumbnail size-woocommerce_thumbnail"))
	b.WriteString(`<h2 class="woocommerce-loop-product__title">` + s.pllUC(c, gooEsc(p.Name), 0) + `</h2>`)
	if s.gooOptBool("enable_review_rating") {
		b.WriteString(gooRatingHTML(p.AverageRating, 0))
	}
	if ph := s.gooPriceHTML(c, p); ph != "" {
		b.WriteString(`<span class="price">` + string(ph) + `</span>`)
	}
	b.WriteString(`</a>`)
	b.WriteString(s.gooLoopAddToCart(c, p, ""))
	b.WriteString(string(s.gooHTML(c, "loop_after_item", p)))
	b.WriteString(`</li>`)
	return b.String()
}

// gooLoopHTML is a products list: <ul class="products columns-N">.
func (s *Server) gooLoopHTML(c *gin.Context, list []*gooProduct, cols int) string {
	if cols < 1 {
		cols = 4
	}
	var b strings.Builder
	b.WriteString(`<ul class="products columns-` + strconv.Itoa(cols) + `">`)
	for i, p := range list {
		b.WriteString(s.gooLoopItem(c, p, i, cols))
	}
	b.WriteString(`</ul>`)
	return b.String()
}

// gooCategoryItem is content-product_cat.php.
func (s *Server) gooCategoryItem(c *gin.Context, t *store.Term, i, cols int) string {
	ctx := c.Request.Context()
	pos := ""
	switch {
	case i%cols == 0:
		pos = " first"
	case i%cols == cols-1:
		pos = " last"
	}
	var thumb int64
	if v, ok := t.Meta["thumbnail_id"].(float64); ok {
		thumb = int64(v)
	}
	img := s.gooImageHTML(ctx, thumb, "medium", "")
	return `<li class="product-category product` + pos + `"><a aria-label="Visit product category ` + gooEsc(t.Name) + `" href="` + gooEsc(s.gooTermLink(ctx, t)) + `">` + img +
		`<h2 class="woocommerce-loop-category__title">` + gooEsc(t.Name) + ` <mark class="count">(` + strconv.Itoa(t.Count) + `)</mark></h2></a></li>`
}

// Archives ----------------------------------------------------------------------------------------------------

// gooCatalogOrderings are the catalog ordering options (woocommerce_catalog_orderby).
var gooCatalogOrderings = []gooOpt{
	{"menu_order", "Default sorting"}, {"popularity", "Sort by popularity"}, {"rating", "Sort by average rating"},
	{"date", "Sort by latest"}, {"price", "Sort by price: low to high"}, {"price-desc", "Sort by price: high to low"},
}

func (s *Server) gooColumns() int {
	n := s.gooOptInt("catalog_columns")
	if n < 1 || n > 6 {
		n = 4
	}
	return n
}

func (s *Server) gooPerPage() int {
	rows := s.gooOptInt("catalog_rows")
	if rows < 1 {
		rows = 4
	}
	return s.gooColumns() * rows
}

// gooArchiveBase is the archive's first-page URL (paging and the ordering form build on it).
func (s *Server) gooArchiveBase(c *gin.Context, fv *gooFrontView) string {
	ctx := c.Request.Context()
	if fv.Term != nil {
		return s.gooTermLink(ctx, fv.Term)
	}
	if fv.Search != "" {
		return s.links(ctx).HomeURL("/?s=" + url.QueryEscape(fv.Search) + "&post_type=product")
	}
	return s.gooShopURL(ctx)
}

// gooOrderBy is the requested catalog ordering.
func (s *Server) gooOrderBy(c *gin.Context) string {
	if ob := c.Query("orderby"); ob != "" {
		for _, o := range gooCatalogOrderings {
			if o.Value == ob {
				return ob
			}
		}
	}
	if c.Query("s") != "" {
		return "relevance"
	}
	return defaultStr(s.gooOptString("default_catalog_orderby"), "menu_order")
}

// gooCatalogQuery is the main product query of an archive with the layered nav filters of the request.
func (s *Server) gooCatalogQuery(c *gin.Context, fv *gooFrontView) gooProductQuery {
	ctx := c.Request.Context()
	q := gooProductQuery{Visibility: "catalog", Page: fv.Paged, PerPage: s.gooPerPage()}
	ob := s.gooOrderBy(c)
	if ob != "relevance" {
		q.OrderBy = ob
	}
	if fv.Search != "" {
		q.Search = fv.Search
		q.Visibility = "search"
	}
	if fv.Term != nil {
		switch fv.Term.Taxonomy {
		case "product_cat":
			q.Category = s.gooTermAndChildren(ctx, fv.Term.ID)
		default:
			q.Tag = []int64{fv.Term.ID}
		}
	}
	s.gooApplyFilters(c, &q)
	return q
}

// gooApplyFilters reads min_price, max_price, filter_<attr> + query_type_<attr> and rating_filter.
func (s *Server) gooApplyFilters(c *gin.Context, q *gooProductQuery) {
	ctx := c.Request.Context()
	v := c.Request.URL.Query()
	if mp := s.gooParseAmount(v.Get("min_price")); mp != nil {
		q.MinPrice = mp
	}
	if mp := s.gooParseAmount(v.Get("max_price")); mp != nil {
		q.MaxPrice = mp
	}
	for key, vals := range v {
		if !strings.HasPrefix(key, "filter_") || len(vals) == 0 || vals[0] == "" {
			continue
		}
		tax := "pa_" + gooSanitizeKey(strings.TrimPrefix(key, "filter_"))
		var ids []int64
		for _, slug := range strings.Split(vals[0], ",") {
			if t, err := s.store.TermBySlug(ctx, tax, strings.TrimSpace(slug)); err == nil {
				ids = append(ids, t.ID)
			} else {
				ids = append(ids, -1)
			}
		}
		if q.AttrTerms == nil {
			q.AttrTerms = map[string][]int64{}
		}
		if strings.ToLower(v.Get("query_type_"+strings.TrimPrefix(key, "filter_"))) == "or" {
			q.AttrTerms[tax] = ids
		} else {
			for i, id := range ids {
				q.AttrTerms[tax+"#"+strconv.Itoa(i)] = []int64{id}
			}
		}
	}
	if rf := v.Get("rating_filter"); rf != "" {
		var ratings []int
		for _, r := range strings.Split(rf, ",") {
			if n := gooAtoi(r); n >= 1 && n <= 5 {
				ratings = append(ratings, n)
			}
		}
		if len(ratings) > 0 {
			ids := []int64{-1}
			rows, err := s.store.Pool.Query(ctx, `SELECT post_id FROM goo_products WHERE round(average_rating)::int = ANY($1)`, ratings)
			if err == nil {
				for rows.Next() {
					var id int64
					if rows.Scan(&id) == nil {
						ids = append(ids, id)
					}
				}
				rows.Close()
			}
			if len(q.IDs) > 0 {
				keep := []int64{-1}
				for _, id := range q.IDs {
					if gooInt64In(id, ids) {
						keep = append(keep, id)
					}
				}
				ids = keep
			}
			q.IDs = ids
		}
	}
}

// gooArchiveHTML is archive-product.php's content.
func (s *Server) gooArchiveHTML(c *gin.Context, fv *gooFrontView) string {
	ctx := c.Request.Context()
	cols := s.gooColumns()
	var b strings.Builder
	b.WriteString(`<div class="woocommerce columns-` + strconv.Itoa(cols) + `">`)
	b.WriteString(s.gooBreadcrumb(c, fv))
	b.WriteString(`<header class="woocommerce-products-header">`)
	if s.gooThemeSupports() {
		title := "Shop"
		if fv.Term != nil {
			title = gooEsc(fv.Term.Name)
		} else if fv.Search != "" {
			title = "Search results: &ldquo;" + gooEsc(fv.Search) + "&rdquo;"
		}
		b.WriteString(`<h1 class="woocommerce-products-header__title page-title">` + title + `</h1>`)
	}
	if fv.Term != nil && strings.TrimSpace(fv.Term.Description) != "" && fv.Paged <= 1 {
		b.WriteString(`<div class="term-description">` + s.gooKeep(c, wp.Autop(wp.KsesPost(fv.Term.Description))) + `</div>`)
	} else if fv.Kind == "shop" && fv.Paged <= 1 && fv.Search == "" {
		if pg, err := s.store.GetPost(ctx, s.gooPageIDIn(ctx, "shop")); err == nil && strings.TrimSpace(pg.Content) != "" && !strings.Contains(pg.Content, gooRenderTag) {
			b.WriteString(`<div class="page-description">` + s.gooRenderContent(ctx, c, pg.Content) + `</div>`)
		}
	}
	b.WriteString(`</header>`)
	b.WriteString(s.gooNoticesHTML(c))

	// Subcategories (shop_page_display / category_archive_display).
	display := ""
	var parent int64 = -1
	filtered := gooFiltered(c)
	if fv.Kind == "shop" && fv.Search == "" && !filtered {
		display, parent = s.gooOptString("shop_page_display"), 0
	} else if fv.Kind == "product_cat" && !filtered {
		display, parent = s.gooOptString("category_archive_display"), fv.Term.ID
	}
	var cats []*store.Term
	if (display == "subcategories" || display == "both") && fv.Paged <= 1 && parent >= 0 {
		all, _ := s.store.HierarchicalTerms(ctx, "product_cat", true)
		for _, t := range all {
			if t.ParentID == parent && t.Count > 0 && t.Slug != "uncategorized" {
				cats = append(cats, t)
			}
		}
	}
	if display == "subcategories" && len(cats) > 0 {
		b.WriteString(`<ul class="products columns-` + strconv.Itoa(cols) + `">`)
		for i, t := range cats {
			b.WriteString(s.gooCategoryItem(c, t, i, cols))
		}
		b.WriteString(`</ul></div>`)
		return b.String()
	}

	q := s.gooCatalogQuery(c, fv)
	products, total, err := s.gooQueryProducts(ctx, q)
	if err != nil || (total == 0 && len(cats) == 0) {
		b.WriteString(`<div class="woocommerce-no-products-found"><div class="woocommerce-info" role="status">No products were found matching your selection.</div></div></div>`)
		return b.String()
	}
	b.WriteString(s.gooResultCount(fv.Paged, q.PerPage, total))
	b.WriteString(s.gooOrderingForm(c, fv))
	if len(cats) > 0 {
		b.WriteString(`<ul class="products columns-` + strconv.Itoa(cols) + `">`)
		for i, t := range cats {
			b.WriteString(s.gooCategoryItem(c, t, i, cols))
		}
		for i, p := range products {
			b.WriteString(s.gooLoopItem(c, p, i+len(cats), cols))
		}
		b.WriteString(`</ul>`)
	} else {
		b.WriteString(s.gooLoopHTML(c, products, cols))
	}
	pages := (total + q.PerPage - 1) / q.PerPage
	b.WriteString(s.gooPagination(c, fv.BaseURL, fv.Paged, pages, ""))
	b.WriteString(`</div>`)
	return b.String()
}

// gooFiltered reports whether the request carries catalog filters.
func gooFiltered(c *gin.Context) bool {
	for k := range c.Request.URL.Query() {
		if k == "min_price" || k == "max_price" || k == "rating_filter" || strings.HasPrefix(k, "filter_") {
			return true
		}
	}
	return false
}

// gooThemeSupports reports whether the active theme declares WooCommerce support (it then prints
// store titles itself through the store templates instead of the page title).
func (s *Server) gooThemeSupports() bool {
	th := s.activeTheme()
	if th == nil || th.Supports == nil {
		return false
	}
	_, ok := th.Supports["woocommerce"]
	return ok
}

// gooRenderContent renders post content (description, shop page content) like the_content.
func (s *Server) gooRenderContent(ctx context.Context, c *gin.Context, content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	depth, _ := c.Get("goo.depth")
	d, _ := depth.(int)
	if d > 2 {
		return ""
	}
	c.Set("goo.depth", d+1)
	defer c.Set("goo.depth", d)
	out := s.renderPostContent(ctx, content)
	out = s.doShortcodes(ctx, out)
	out = s.gooShortcodes(ctx, out)
	return s.gooKeep(c, out) // the user's text: the store's translation leaves it alone
}

// gooResultCount is woocommerce_result_count().
func (s *Server) gooResultCount(paged, perPage, total int) string {
	var txt string
	switch {
	case total == 1:
		txt = "Showing the single result"
	case total <= perPage || perPage < 0:
		txt = "Showing all " + strconv.Itoa(total) + " results"
	default:
		first := (paged-1)*perPage + 1
		last := min(total, paged*perPage)
		txt = fmt.Sprintf("Showing %d&ndash;%d of %d results", first, last, total)
	}
	return `<p class="woocommerce-result-count" role="alert" aria-relevant="all">` + txt + `</p>`
}

// gooOrderingForm is woocommerce_catalog_ordering().
func (s *Server) gooOrderingForm(c *gin.Context, fv *gooFrontView) string {
	cur := s.gooOrderBy(c)
	var b strings.Builder
	b.WriteString(`<form class="woocommerce-ordering" method="get"><select name="orderby" class="orderby" aria-label="Shop order">`)
	opts := gooCatalogOrderings
	if !s.gooOptBool("enable_review_rating") {
		opts = nil
		for _, o := range gooCatalogOrderings {
			if o.Value != "rating" {
				opts = append(opts, o)
			}
		}
	}
	if fv.Search != "" {
		opts = append([]gooOpt{{"relevance", "Relevance"}}, opts...)
	}
	for _, o := range opts {
		sel := ""
		if o.Value == cur {
			sel = ` selected='selected'`
		}
		b.WriteString(`<option value="` + o.Value + `"` + sel + `>` + o.Label + `</option>`)
	}
	b.WriteString(`</select><input type="hidden" name="paged" value="1" />`)
	// wc_query_string_form_fields(): keep the other query args.
	v := c.Request.URL.Query()
	for _, k := range gooSortedKeysV(v) {
		if k == "orderby" || k == "paged" || k == "submit" || k == "product-page" {
			continue
		}
		for _, val := range v[k] {
			b.WriteString(`<input type="hidden" name="` + gooEsc(k) + `" value="` + gooEsc(val) + `" />`)
		}
	}
	b.WriteString(`</form>`)
	return b.String()
}

func gooSortedKeysV(v url.Values) []string {
	out := make([]string, 0, len(v))
	for k := range v {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// gooPagination is woocommerce_pagination() (pageArg "" uses /page/N/ or paged, else a query arg).
func (s *Server) gooPagination(c *gin.Context, base string, current, total int, pageArg string) string {
	if total < 2 {
		return ""
	}
	lb := s.links(c.Request.Context())
	keep := url.Values{}
	for k, vals := range c.Request.URL.Query() {
		if k == "paged" || k == pageArg || k == "add-to-cart" {
			continue
		}
		keep[k] = vals
	}
	link := func(n int) string {
		var u string
		if pageArg != "" {
			u = s.gooCurrentURL(c, pageArg)
			if n > 1 {
				uu, _ := url.Parse(u)
				q := uu.Query()
				q.Set(pageArg, strconv.Itoa(n))
				uu.RawQuery = q.Encode()
				u = uu.String()
			}
			return u
		}
		u = lb.Paged(base, n)
		if len(keep) > 0 {
			uu, err := url.Parse(u)
			if err == nil {
				q := uu.Query()
				for k, vals := range keep {
					if !q.Has(k) {
						q[k] = vals
					}
				}
				uu.RawQuery = q.Encode()
				u = uu.String()
			}
		}
		return u
	}
	var b strings.Builder
	b.WriteString(`<nav class="woocommerce-pagination" aria-label="Product Pagination"><ul class="page-numbers">`)
	if current > 1 {
		b.WriteString(`<li><a class="prev page-numbers" href="` + gooEsc(link(current-1)) + `" aria-label="Previous Page">&larr;</a></li>`)
	}
	dots := false
	for n := 1; n <= total; n++ {
		switch {
		case n == current:
			b.WriteString(`<li><span aria-label="Page ` + strconv.Itoa(n) + `" aria-current="page" class="page-numbers current">` + strconv.Itoa(n) + `</span></li>`)
			dots = true
		case n <= 2 || n > total-2 || (n >= current-2 && n <= current+2):
			b.WriteString(`<li><a aria-label="Page ` + strconv.Itoa(n) + `" class="page-numbers" href="` + gooEsc(link(n)) + `">` + strconv.Itoa(n) + `</a></li>`)
			dots = true
		case dots:
			b.WriteString(`<li><span class="page-numbers dots">&hellip;</span></li>`)
			dots = false
		}
	}
	if current < total {
		b.WriteString(`<li><a class="next page-numbers" href="` + gooEsc(link(current+1)) + `" aria-label="Next Page">&rarr;</a></li>`)
	}
	b.WriteString(`</ul></nav>`)
	return b.String()
}

// gooBreadcrumb is woocommerce_breadcrumb().
func (s *Server) gooBreadcrumb(c *gin.Context, fv *gooFrontView) string {
	ctx := c.Request.Context()
	lb := s.links(ctx)
	type crumb struct{ name, url string }
	crumbs := []crumb{{"Home", lb.Front()}} // the language home (Golylang)
	shop := func() {
		if id := s.gooPageIDIn(ctx, "shop"); id > 0 && int64(s.settings.Int("page_on_front")) != s.gooPageID("shop") {
			if pg, err := s.store.GetPost(ctx, id); err == nil {
				crumbs = append(crumbs, crumb{pg.Title, s.gooShopURL(ctx)})
			}
		}
	}
	termChain := func(t *store.Term) {
		var chain []*store.Term
		cur := t
		for i := 0; cur != nil && i < 20; i++ {
			chain = append([]*store.Term{cur}, chain...)
			if cur.ParentID == 0 {
				break
			}
			cur, _ = s.store.GetTerm(ctx, cur.ParentID)
		}
		for _, x := range chain {
			crumbs = append(crumbs, crumb{x.Name, s.gooTermLink(ctx, x)})
		}
	}
	switch fv.Kind {
	case "shop":
		if fv.Search != "" {
			shop()
			crumbs = append(crumbs, crumb{gooEsc(s.gooL10nText(ctx, "Search results for “"+fv.Search+"”")), ""}) // text after the delimiter: translated here
		} else if id := s.gooPageIDIn(ctx, "shop"); id > 0 {
			if pg, err := s.store.GetPost(ctx, id); err == nil {
				crumbs = append(crumbs, crumb{pg.Title, ""})
			}
		}
	case "product_cat":
		shop()
		termChain(fv.Term)
		crumbs[len(crumbs)-1].url = ""
	case "product_tag":
		shop()
		crumbs = append(crumbs, crumb{gooEsc(s.gooL10nText(ctx, "Products tagged “"+fv.Term.Name+"”")), ""})
	case "product_attr":
		shop()
		crumbs = append(crumbs, crumb{gooEsc(fv.Term.Name), ""})
	case "product":
		shop()
		terms, _ := s.gooProductTerms(c, fv.Product.ID)
		for _, t := range terms {
			if t.Taxonomy == "product_cat" {
				termChain(t)
				break
			}
		}
		crumbs = append(crumbs, crumb{gooEsc(fv.Product.Name), ""})
	}
	if fv.Paged > 1 {
		crumbs[len(crumbs)-1].url = fv.BaseURL
		crumbs = append(crumbs, crumb{gooEsc(s.gooL10nText(ctx, "Page "+strconv.Itoa(fv.Paged))), ""})
	}
	var b strings.Builder
	b.WriteString(`<nav class="woocommerce-breadcrumb" aria-label="Breadcrumb">`)
	for i, cr := range crumbs {
		if i > 0 {
			b.WriteString(`&nbsp;&#47;&nbsp;`)
		}
		name := cr.name
		if i == 0 || cr.url != "" && !strings.Contains(name, "&") {
			name = gooEsc(name)
		}
		if cr.url != "" {
			b.WriteString(`<a href="` + gooEsc(cr.url) + `">` + name + `</a>`)
		} else {
			b.WriteString(name)
		}
	}
	b.WriteString(`</nav>`)
	return b.String()
}

// Widgets ------------------------------------------------------------------------------------------------------

// gooWidgetWrap is the widget markup of the store widgets printed by shortcode.
func gooWidgetWrap(class, title, body string) string {
	h := ""
	if title != "" {
		h = `<h2 class="widget-title">` + gooEsc(title) + `</h2>`
	}
	return `<div class="widget woocommerce ` + class + `">` + h + body + `</div>`
}

// gooFilterBase is the URL the filter widgets link to: the current archive (shop when not on one).
func (s *Server) gooFilterBase(c *gin.Context) string {
	fv := gooViewFrom(c)
	if fv != nil && (fv.Kind == "shop" || strings.HasPrefix(fv.Kind, "product_")) && fv.Kind != "product" && fv.BaseURL != "" {
		return fv.BaseURL
	}
	return s.gooShopURL(c.Request.Context())
}

// gooFilterLink is the archive URL with the current query args changed.
func (s *Server) gooFilterLink(c *gin.Context, set map[string]string) string {
	base := s.gooFilterBase(c)
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	for k, vals := range c.Request.URL.Query() {
		if k == "paged" || k == "add-to-cart" || k == "product-page" {
			continue
		}
		q[k] = vals
	}
	for k, v := range set {
		if v == "" {
			q.Del(k)
		} else {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// gooWidgetPriceFilter is WC_Widget_Price_Filter.
func (s *Server) gooWidgetPriceFilter(c *gin.Context, title string) string {
	ctx := c.Request.Context()
	var cats []int64
	if fv := gooViewFrom(c); fv != nil && fv.Term != nil && fv.Term.Taxonomy == "product_cat" {
		cats = s.gooTermAndChildren(ctx, fv.Term.ID)
	}
	lo, hi := s.gooPriceRange(ctx, cats)
	if lo == hi {
		return ""
	}
	step := 10.0
	lo, hi = math.Floor(lo/step)*step, math.Ceil(hi/step)*step
	cmin, cmax := c.Query("min_price"), c.Query("max_price")
	fmtv := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	var b strings.Builder
	b.WriteString(`<form method="get" action="` + gooEsc(s.gooFilterBase(c)) + `"><div class="price_slider_wrapper">`)
	b.WriteString(`<div class="price_slider_amount" data-step="` + fmtv(step) + `">`)
	b.WriteString(`<label class="screen-reader-text" for="min_price">Min price</label><input type="number" id="min_price" name="min_price" value="` + gooEsc(defaultStr(cmin, fmtv(lo))) + `" data-min="` + fmtv(lo) + `" placeholder="Min price" min="` + fmtv(lo) + `" max="` + fmtv(hi) + `" step="1" />`)
	b.WriteString(`<label class="screen-reader-text" for="max_price">Max price</label><input type="number" id="max_price" name="max_price" value="` + gooEsc(defaultStr(cmax, fmtv(hi))) + `" data-max="` + fmtv(hi) + `" placeholder="Max price" min="` + fmtv(lo) + `" max="` + fmtv(hi) + `" step="1" />`)
	b.WriteString(`<button type="submit" class="button">Filter</button>`)
	b.WriteString(`<div class="price_label">Price: <span class="from">` + string(s.gooPrice(lo)) + `</span> &mdash; <span class="to">` + string(s.gooPrice(hi)) + `</span></div>`)
	for k, vals := range c.Request.URL.Query() {
		if k == "min_price" || k == "max_price" || k == "paged" || k == "add-to-cart" {
			continue
		}
		b.WriteString(`<input type="hidden" name="` + gooEsc(k) + `" value="` + gooEsc(vals[0]) + `" />`)
	}
	b.WriteString(`<div class="clear"></div></div></div></form>`)
	return gooWidgetWrap("widget_price_filter", defaultStr(title, "Filter by price"), b.String())
}

// gooWidgetLayeredNav is WC_Widget_Layered_Nav (list display) for an attribute.
func (s *Server) gooWidgetLayeredNav(c *gin.Context, attr, title, queryType string) string {
	ctx := c.Request.Context()
	attr = strings.TrimPrefix(gooSanitizeKey(attr), "pa_")
	tax := "pa_" + attr
	a := s.gooAttributeByTaxonomy(ctx, tax)
	if a == nil {
		return ""
	}
	terms, _ := s.gooAttributeTerms(ctx, tax)
	if queryType != "or" {
		queryType = "and"
	}
	current := map[string]bool{}
	for _, sl := range strings.Split(c.Query("filter_"+attr), ",") {
		if sl != "" {
			current[sl] = true
		}
	}
	// Product counts within the current archive and the other filters.
	fv := gooViewFrom(c)
	base := gooProductQuery{Visibility: "catalog", PerPage: -1}
	if fv != nil && fv.Kind != "product" {
		base = s.gooCatalogQuery(c, fv)
		base.PerPage, base.Page = -1, 1
	}
	var b strings.Builder
	b.WriteString(`<ul class="woocommerce-widget-layered-nav-list">`)
	shown := 0
	for _, t := range terms {
		q := base
		q.AttrTerms = map[string][]int64{}
		for k, v := range base.AttrTerms {
			if !strings.HasPrefix(k, tax) {
				q.AttrTerms[k] = v
			}
		}
		q.AttrTerms[tax] = []int64{t.ID}
		_, count, _ := s.gooQueryProductsCount(ctx, q)
		if count == 0 && !current[t.Slug] {
			continue
		}
		shown++
		next := map[string]bool{}
		for k := range current {
			next[k] = true
		}
		if current[t.Slug] {
			delete(next, t.Slug)
		} else {
			next[t.Slug] = true
		}
		var slugs []string
		for k := range next {
			slugs = append(slugs, k)
		}
		sort.Strings(slugs)
		set := map[string]string{"filter_" + attr: strings.Join(slugs, ",")}
		if queryType == "or" && len(slugs) > 0 {
			set["query_type_"+attr] = "or"
		} else {
			set["query_type_"+attr] = ""
		}
		cls := "woocommerce-widget-layered-nav-list__item wc-layered-nav-term"
		if current[t.Slug] {
			cls += " woocommerce-widget-layered-nav-list__item--chosen chosen"
		}
		b.WriteString(`<li class="` + cls + `"><a rel="nofollow" href="` + gooEsc(s.gooFilterLink(c, set)) + `">` + gooEsc(t.Name) + `</a> <span class="count">(` + strconv.Itoa(count) + `)</span></li>`)
	}
	b.WriteString(`</ul>`)
	if shown == 0 {
		return ""
	}
	return gooWidgetWrap("woocommerce-widget-layered-nav", defaultStr(title, "Filter by "+a.Name), b.String())
}

// gooQueryProductsCount counts a catalog query.
func (s *Server) gooQueryProductsCount(ctx context.Context, q gooProductQuery) ([]*gooProduct, int, error) {
	q.PerPage, q.Page = 1, 1
	q.OrderBy = "id"
	return s.gooQueryProducts(ctx, q)
}

// gooWidgetActiveFilters is WC_Widget_Layered_Nav_Filters.
func (s *Server) gooWidgetActiveFilters(c *gin.Context, title string) string {
	ctx := c.Request.Context()
	v := c.Request.URL.Query()
	var items []string
	for _, k := range gooSortedKeysV(v) {
		if !strings.HasPrefix(k, "filter_") {
			continue
		}
		attr := strings.TrimPrefix(k, "filter_")
		slugs := strings.Split(v.Get(k), ",")
		for _, sl := range slugs {
			if sl == "" {
				continue
			}
			var rest []string
			for _, o := range slugs {
				if o != sl && o != "" {
					rest = append(rest, o)
				}
			}
			name := sl
			if t, err := s.store.TermBySlug(ctx, "pa_"+attr, sl); err == nil {
				name = t.Name
			}
			items = append(items, `<li class="chosen"><a rel="nofollow" aria-label="Remove filter" href="`+gooEsc(s.gooFilterLink(c, map[string]string{k: strings.Join(rest, ",")}))+`">`+gooEsc(name)+`</a></li>`)
		}
	}
	if mp := v.Get("min_price"); mp != "" {
		if f := s.gooParseAmount(mp); f != nil {
			items = append(items, `<li class="chosen"><a rel="nofollow" aria-label="Remove filter" href="`+gooEsc(s.gooFilterLink(c, map[string]string{"min_price": ""}))+`">Min `+string(s.gooPrice(*f))+`</a></li>`)
		}
	}
	if mp := v.Get("max_price"); mp != "" {
		if f := s.gooParseAmount(mp); f != nil {
			items = append(items, `<li class="chosen"><a rel="nofollow" aria-label="Remove filter" href="`+gooEsc(s.gooFilterLink(c, map[string]string{"max_price": ""}))+`">Max `+string(s.gooPrice(*f))+`</a></li>`)
		}
	}
	if rf := v.Get("rating_filter"); rf != "" {
		for _, r := range strings.Split(rf, ",") {
			if n := gooAtoi(r); n >= 1 && n <= 5 {
				items = append(items, `<li class="chosen"><a rel="nofollow" aria-label="Remove filter" href="`+gooEsc(s.gooFilterLink(c, map[string]string{"rating_filter": ""}))+`">Rated `+strconv.Itoa(n)+` out of 5</a></li>`)
			}
		}
	}
	if len(items) == 0 {
		return ""
	}
	return gooWidgetWrap("widget_layered_nav_filters", defaultStr(title, "Active filters"), `<ul>`+strings.Join(items, "")+`</ul>`)
}

// gooWidgetRatingFilter is WC_Widget_Rating_Filter.
func (s *Server) gooWidgetRatingFilter(c *gin.Context, title string) string {
	current := map[int]bool{}
	for _, r := range strings.Split(c.Query("rating_filter"), ",") {
		if n := gooAtoi(r); n > 0 {
			current[n] = true
		}
	}
	var b strings.Builder
	b.WriteString(`<ul>`)
	for r := 5; r >= 1; r-- {
		next := []string{}
		for n := 5; n >= 1; n-- {
			if (current[n] && n != r) || (!current[r] && n == r) {
				next = append(next, strconv.Itoa(n))
			}
		}
		cls := "wc-layered-nav-rating"
		if current[r] {
			cls += " chosen"
		}
		b.WriteString(`<li class="` + cls + `"><a href="` + gooEsc(s.gooFilterLink(c, map[string]string{"rating_filter": strings.Join(next, ",")})) + `">` + gooRatingHTML(float64(r), 0) + `</a></li>`)
	}
	b.WriteString(`</ul>`)
	return gooWidgetWrap("widget_rating_filter", defaultStr(title, "Average rating"), b.String())
}

// gooWidgetCategories is WC_Widget_Product_Categories (list, with counts and hierarchy).
func (s *Server) gooWidgetCategories(c *gin.Context, title string, count, hideEmpty bool) string {
	ctx := c.Request.Context()
	all, _ := s.store.HierarchicalTerms(ctx, "product_cat", hideEmpty)
	var current int64
	if fv := gooViewFrom(c); fv != nil && fv.Term != nil && fv.Term.Taxonomy == "product_cat" {
		current = fv.Term.ID
	}
	children := map[int64][]*store.Term{}
	for _, t := range all {
		children[t.ParentID] = append(children[t.ParentID], t)
	}
	var walk func(parent int64) string
	walk = func(parent int64) string {
		var b strings.Builder
		for _, t := range children[parent] {
			cls := "cat-item cat-item-" + itoa64(t.ID)
			if t.ID == current {
				cls += " current-cat"
			}
			if len(children[t.ID]) > 0 {
				cls += " cat-parent"
			}
			b.WriteString(`<li class="` + cls + `"><a href="` + gooEsc(s.gooTermLink(ctx, t)) + `">` + gooEsc(t.Name) + `</a>`)
			if count {
				b.WriteString(` <span class="count">(` + strconv.Itoa(t.Count) + `)</span>`)
			}
			if sub := walk(t.ID); sub != "" {
				b.WriteString(`<ul class='children'>` + sub + `</ul>`)
			}
			b.WriteString(`</li>`)
		}
		return b.String()
	}
	return gooWidgetWrap("widget_product_categories", defaultStr(title, "Product categories"), `<ul class="product-categories">`+walk(0)+`</ul>`)
}

// gooProductSearchForm is get_product_search_form().
func (s *Server) gooProductSearchForm(c *gin.Context) string {
	id := "woocommerce-product-search-field-" + gooRandomKey(6)
	action, hidden := searchFormAction(s.links(c.Request.Context())) // Golylang: the language home
	return `<form role="search" method="get" class="woocommerce-product-search" action="` + gooEsc(action) + `">` +
		`<label class="screen-reader-text" for="` + id + `">Search for:</label>` +
		`<input type="search" id="` + id + `" class="search-field" placeholder="Search products&hellip;" value="` + gooEsc(c.Query("s")) + `" name="s" />` +
		`<button type="submit" value="Search" class="">Search</button><input type="hidden" name="post_type" value="product" />` + string(hidden) + `</form>`
}
