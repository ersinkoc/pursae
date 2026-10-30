package server

import (
	"context"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/links"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Goocommerce's catalog admin screens: Products list, the product editor (Product data box with
// General, Inventory, Shipping, Linked Products, Attributes, Variations, Advanced), Attributes, Reviews.
//
// The products list is edit.php?post_type=product (WC_Admin_List_Table_Products): WooCommerce's
// columns, views, filters, featured toggle, duplicate link, bulk edit and the CSV import / export
// buttons. Everything it answers is a GET (POST /wp-admin/edit belongs to core's Screen Options);
// the importer's upload posts to /wp-admin/goocommerce?page=product_importer.

const gooCatalogVersion = gooVersion

// gooCatalogAssets prints the catalog admin stylesheet and script (after the footer scripts, so
// jQuery, wp.media and jQuery UI are there).
func (s *Server) gooCatalogAssets(c *gin.Context) {
	if _, done := c.Get("goo.catalogAssets"); done {
		return
	}
	c.Set("goo.catalogAssets", true)
	addBodyHTML(c, template.HTML(`<link rel="stylesheet" id="goo-admin-catalog-css" href="`+gooAssets+`/css/admin-catalog.css?ver=`+gooCatalogVersion+`" media="all" />`))
	addLateFooterHTML(c, template.HTML(`<script src="`+gooAssets+`/js/admin/catalog.js?ver=`+gooCatalogVersion+`"></script>`+"\n"))
}

type gooProductRow struct {
	ID                              int64
	Name                            string
	States                          []string
	EditURL, ViewURL, ViewLabel     string
	TrashURL, RestoreURL, DeleteURL string
	DuplicateURL, FeaturedURL       string
	Thumb                           string
	SKU                             string
	Stock, Price                    template.HTML
	Cats, Tags                      template.HTML
	Featured                        bool
	DateLabel, DateValue            string
	Classes                         string
	IsTrash, CanEdit, CanDelete     bool
	Type                            string
}

type gooProductsData struct {
	Statuses     []statusLink
	Rows         []gooProductRow
	Search       string
	Status       string
	Pagination   pagination
	BulkNonce    string
	IsTrash      bool
	Sorting      bool
	OrderNonce   string
	SortLinks    map[string]sortLink
	Categories   []*store.Term
	Cat          string
	ProductType  string
	StockStatus  string
	Types        []gooOpt
	ColCount     int
	Hidden       map[string]bool
	BulkEdit     []gooProductRow // the selected products of the Bulk Edit panel
	BulkTaxClass []gooOpt
	ShipClasses  []*store.Term
	ExportURL    string
	ImportURL    string
}

// gooProductListColumns are the Screen Options toggles of the products list.
var gooProductListColumns = []screenColumn{{"thumb", "Image"}, {"sku", "SKU"}, {"is_in_stock", "Stock"}, {"price", "Price"},
	{"product_cat", "Categories"}, {"product_tag", "Tags"}, {"featured", "Featured"}, {"date", "Date"}}

// gooAdminProducts is edit.php?post_type=product.
func (s *Server) gooAdminProducts(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("edit_products", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit posts in this post type.")
		return
	}
	switch {
	case c.Query("goo_search_products") != "" || c.Query("action") == "woocommerce_json_search_products":
		s.gooSearchProductsJSON(c)
		return
	case c.Query("action") == "woocommerce_feature_product":
		s.gooToggleFeatured(c)
		return
	case c.Query("action") == "duplicate_product":
		s.gooDuplicateAction(c)
		return
	case c.Query("action") == "woocommerce_product_ordering":
		s.gooProductOrdering(c)
		return
	case c.Query("page") == "product_exporter":
		s.gooProductExporter(c)
		return
	case c.Query("page") == "product_importer":
		s.gooProductImporter(c)
		return
	}
	if a := gooBulkAction(c); a != "" {
		if a != "edit" || c.Query("bulk_edit") != "" {
			s.gooProductsBulk(c, a)
			return
		}
	}
	s.gooRenderProducts(c)
}

// gooBulkAction is the bulk action chosen with Apply (bulk_action) or Bulk Edit's Update (bulk_edit).
func gooBulkAction(c *gin.Context) string {
	if c.Query("bulk_action") == "" && c.Query("bulk_edit") == "" {
		return ""
	}
	for _, k := range []string{"action", "action2"} {
		if v := c.Query(k); v != "" && v != "-1" {
			return v
		}
	}
	return ""
}

func (s *Server) gooRenderProducts(c *gin.Context) {
	ctx := c.Request.Context()
	u := currentUser(c)
	status := c.DefaultQuery("post_status", "all")
	search := strings.TrimSpace(c.Query("s"))
	page, _ := strconv.Atoi(c.Query("paged"))
	if page < 1 {
		page = 1
	}
	perPage := s.userOptionInt(u, "edit_product_per_page", 20)
	orderby, order := c.Query("orderby"), strings.ToLower(c.Query("order"))
	sorting := orderby == "menu_order title"

	q := gooProductQuery{Search: search, Page: page, PerPage: perPage, Order: order}
	switch orderby {
	case "title", "name":
		q.OrderBy = "title"
	case "price":
		q.OrderBy = "price"
		if order == "desc" {
			q.OrderBy, q.Order = "price-desc", ""
		}
	case "menu_order title":
		q.OrderBy, q.Order = "menu_order", "asc"
	case "id":
		q.OrderBy = "id"
	default:
		q.OrderBy = "date"
		if order == "" {
			q.Order = "desc"
		}
	}
	switch status {
	case "all", "":
		q.Status = []string{"publish", "future", "draft", "pending", "private"}
	default:
		q.Status = []string{status}
	}
	d := gooProductsData{Search: search, Status: status, BulkNonce: s.nonce(c, "bulk-posts"), IsTrash: status == "trash", Sorting: sorting,
		OrderNonce: s.nonce(c, "product-ordering"), Cat: c.Query("product_cat"), ProductType: c.Query("product_type"), StockStatus: c.Query("stock_status"),
		Hidden: s.hiddenColumns(u, "edit-product"), ExportURL: "/wp-admin/edit?post_type=product&page=product_exporter",
		ImportURL: "/wp-admin/edit?post_type=product&page=product_importer"}
	if d.Cat != "" {
		if t, err := s.store.TermBySlug(ctx, "product_cat", d.Cat); err == nil {
			q.Category = s.gooTermAndChildren(ctx, t.ID)
		} else {
			q.Category = []int64{-1}
		}
	}
	if tag := c.Query("product_tag"); tag != "" {
		if t, err := s.store.TermBySlug(ctx, "product_tag", tag); err == nil {
			q.Tag = []int64{t.ID}
		} else {
			q.Tag = []int64{-1}
		}
	}
	switch d.ProductType {
	case "":
	case "downloadable", "virtual":
		q.IDs = s.gooIDsWhere(ctx, d.ProductType)
	default:
		q.Types = []string{d.ProductType}
	}
	if d.StockStatus != "" {
		q.StockStatus = d.StockStatus
	}
	products, total, err := s.gooQueryProducts(ctx, q)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	if status == "trash" {
		products, total = s.gooTrashedProducts(ctx, search, page, perPage)
	}
	d.Types = s.gooProductTypeOptions()
	d.Categories, _ = s.store.HierarchicalTerms(ctx, "product_cat", false)

	// Views (WP_Posts_List_Table::get_views() plus WooCommerce's "Sorting").
	counts, _ := s.store.StatusCounts(ctx, "product", 0)
	all := counts["publish"] + counts["future"] + counts["draft"] + counts["pending"] + counts["private"]
	base := "/wp-admin/edit?post_type=product"
	d.Statuses = append(d.Statuses, statusLink{"all", "All", base, all, (status == "all" || status == "") && !sorting})
	for _, st := range []struct{ key, label string }{{"publish", "Published"}, {"future", "Scheduled"}, {"draft", "Drafts"}, {"pending", "Pending"},
		{"private", "Private"}, {"trash", "Trash"}} {
		if counts[st.key] > 0 {
			d.Statuses = append(d.Statuses, statusLink{st.key, st.label, base + "&post_status=" + st.key, counts[st.key], status == st.key})
		}
	}
	if u.Can("edit_others_products", nil) {
		d.Statuses = append(d.Statuses, statusLink{"byorder", "Sorting", base + "&orderby=menu_order+title&order=ASC", -1, sorting})
	}

	d.Rows = s.gooProductRows(c, products)
	if ids := c.QueryArray("post[]"); gooBulkAction(c) == "edit" && len(ids) > 0 {
		var sel []int64
		for _, raw := range ids {
			if id, _ := strconv.ParseInt(raw, 10, 64); id > 0 {
				sel = append(sel, id)
			}
		}
		m, _ := s.gooGetProducts(ctx, sel)
		for _, id := range sel {
			if p := m[id]; p != nil && p.Type != gooTypeVariation {
				d.BulkEdit = append(d.BulkEdit, gooProductRow{ID: p.ID, Name: defaultStr(p.Name, "(no title)")})
			}
		}
		d.BulkTaxClass = s.gooTaxClasses()
		d.ShipClasses, _, _ = s.store.QueryTerms(ctx, store.TermQuery{Taxonomy: "product_shipping_class"})
	}
	d.Pagination = newPagination(total, perPage, page, *c.Request.URL)
	d.SortLinks = sortLinks(c, map[string]bool{"name": false, "price": false, "date": true}, "date", "desc")
	d.ColCount = 2
	for _, col := range gooProductListColumns {
		if !d.Hidden[col.ID] {
			d.ColCount++
		}
	}
	s.setScreenOptions(c, screenOptions{Screen: "edit-product", Columns: gooProductListColumns, Hidden: d.Hidden,
		PerPageOption: "edit_product_per_page", PerPage: perPage})
	if sorting {
		s.pageAssets(c).Script("jquery-ui-sortable")
	}
	s.gooCatalogAssets(c)
	s.adminPage(c, "edit?post_type=product", "Products", "edit-php post-type-product", "admin/goo-products", d, s.gooProductListNotices(c)...)
}

// gooTrashedProducts is the Trash view (gooGetProducts only loads live statuses).
func (s *Server) gooTrashedProducts(ctx context.Context, search string, page, perPage int) ([]*gooProduct, int) {
	var total int
	like := "%" + search + "%"
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE type = 'product' AND status = 'trash' AND ($1 = '' OR title ILIKE $2)`, search, like).Scan(&total)
	rows, err := s.store.Pool.Query(ctx, `SELECT id FROM posts WHERE type = 'product' AND status = 'trash' AND ($1 = '' OR title ILIKE $2)
		ORDER BY modified_at DESC, id DESC LIMIT $3 OFFSET $4`, search, like, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0
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
	return out, total
}

// gooIDsWhere are the ids of products flagged downloadable or virtual (the product type filter's sub-options).
func (s *Server) gooIDsWhere(ctx context.Context, flag string) []int64 {
	col := "downloadable"
	if flag == "virtual" {
		col = "virtual"
	}
	ids := []int64{-1}
	rows, err := s.store.Pool.Query(ctx, `SELECT post_id FROM goo_products WHERE `+col+` AND type <> 'variation'`)
	if err != nil {
		return ids
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// gooProductTypeOptions is the product type selector: the core four plus the switched-on modules' types.
func (s *Server) gooProductTypeOptions() []gooOpt {
	out := []gooOpt{{gooTypeSimple, "Simple product"}, {gooTypeGrouped, "Grouped product"}, {gooTypeExternal, "External/Affiliate product"}, {gooTypeVariable, "Variable product"}}
	keys := make([]string, 0, len(gooProductTypes))
	for k := range gooProductTypes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		def := gooProductTypes[k]
		if def.Module == "" || s.gooExtOn(def.Module) {
			out = append(out, gooOpt{def.Type, def.Label})
		}
	}
	return out
}

// gooProductRows builds the rows of the products table (WC_Admin_List_Table_Products::render_*_column()).
func (s *Server) gooProductRows(c *gin.Context, products []*gooProduct) []gooProductRow {
	ctx := c.Request.Context()
	u := currentUser(c)
	lb := s.links(ctx)
	loc := s.loc()
	ids := make([]int64, 0, len(products))
	for _, p := range products {
		ids = append(ids, p.ID)
	}
	cats, _ := s.store.TermsForPosts(ctx, ids, "product_cat")
	tags, _ := s.store.TermsForPosts(ctx, ids, "product_tag")
	featNonce := s.nonce(c, "woocommerce-feature-product")
	var out []gooProductRow
	for _, p := range products {
		post := p.Post
		r := gooProductRow{ID: p.ID, Name: p.Name, SKU: p.SKU, Featured: p.Featured, IsTrash: p.Status == "trash", Type: p.Type,
			EditURL: links.EditLink(p.ID), CanEdit: gooCanProduct(u, "edit", post), CanDelete: gooCanProduct(u, "delete", post)}
		if strings.TrimSpace(r.Name) == "" {
			r.Name = "(no title)"
		}
		switch p.Status {
		case "draft":
			r.States = append(r.States, "Draft")
		case "pending":
			r.States = append(r.States, "Pending")
		case "future":
			r.States = append(r.States, "Scheduled")
		case "private":
			r.States = append(r.States, "Private")
		}
		if post.Password != "" {
			r.States = append(r.States, "Password protected")
		}
		id := itoa64(p.ID)
		r.TrashURL = "/wp-admin/post?post=" + id + "&action=trash&_wpnonce=" + s.nonce(c, "trash-post_"+id)
		r.RestoreURL = "/wp-admin/post?post=" + id + "&action=untrash&_wpnonce=" + s.nonce(c, "untrash-post_"+id)
		r.DeleteURL = "/wp-admin/post?post=" + id + "&action=delete&_wpnonce=" + s.nonce(c, "delete-post_"+id)
		r.DuplicateURL = "/wp-admin/edit?post_type=product&action=duplicate_product&post=" + id + "&_wpnonce=" + s.nonce(c, "woocommerce-duplicate-product_"+id)
		r.FeaturedURL = "/wp-admin/edit?post_type=product&action=woocommerce_feature_product&product_id=" + id + "&_wpnonce=" + featNonce
		if p.Status == "publish" || p.Status == "private" {
			r.ViewURL, r.ViewLabel = s.gooPermalink(ctx, p), "View"
		} else if p.Status != "trash" {
			pl := lb.HomeURL("/?post_type=product&p=" + id)
			r.ViewURL, r.ViewLabel = pl+"&preview=true", "Preview"
		}
		if p.ImageID > 0 {
			r.Thumb = s.gooAttachmentURL(ctx, p.ImageID, "thumbnail")
		}
		r.Stock = s.gooStockHTML(ctx, p)
		r.Price = s.gooAdminPriceHTML(ctx, p)
		r.Cats = termLinksHTML(cats[p.ID], "/wp-admin/edit?post_type=product&product_cat=")
		r.Tags = termLinksHTML(tags[p.ID], "/wp-admin/edit?post_type=product&product_tag=")
		t := post.Date().In(loc)
		switch p.Status {
		case "publish", "private":
			r.DateLabel = "Published"
		case "future":
			r.DateLabel = "Scheduled"
		default:
			r.DateLabel = "Last Modified"
			t = post.ModifiedAt.In(loc)
		}
		r.DateValue = wp.FormatDate(t, "Y/m/d") + " at " + wp.FormatDate(t, "g:i a")
		classes := []string{"iedit", "level-0", "post-" + id, "type-product", "status-" + p.Status, "hentry", "product-type-" + p.Type}
		for _, ct := range cats[p.ID] {
			classes = append(classes, "product_cat-"+ct.Slug)
		}
		if p.Featured {
			classes = append(classes, "featured")
		}
		r.Classes = strings.Join(classes, " ")
		out = append(out, r)
	}
	return out
}

// gooAttachmentURL is an attachment's URL at a size (the full file when the size is missing).
func (s *Server) gooAttachmentURL(ctx context.Context, id int64, size string) string {
	a, err := s.store.GetAttachment(ctx, id)
	if err != nil {
		return ""
	}
	if size != "" && size != "full" {
		if v := a.Variant(size); v != nil {
			return s.uploadURL(ctx, v.FilePath)
		}
	}
	return s.uploadURL(ctx, a.FilePath)
}

// gooStockLabels are wc_get_product_stock_status_options().
var gooStockLabels = map[string]string{"instock": "In stock", "outofstock": "Out of stock", "onbackorder": "On backorder"}

// gooStockHTML is the Stock column: <mark class="instock">In stock</mark> (12).
func (s *Server) gooStockHTML(ctx context.Context, p *gooProduct) template.HTML {
	st := p.StockStatus
	if st == "" {
		st = "instock"
	}
	label := gooStockLabels[st]
	if label == "" {
		label = st
	}
	out := `<mark class="` + template.HTMLEscapeString(st) + `">` + label + `</mark>`
	if s.gooManageStock() && p.ManageStock && p.StockQuantity != nil {
		out += " (" + strconv.Itoa(*p.StockQuantity) + ")"
	}
	return template.HTML(out)
}

// gooAdminPriceHTML is get_price_html() for the admin list: sale prices struck through, ranges for
// variable and grouped products, "–" without a price.
func (s *Server) gooAdminPriceHTML(ctx context.Context, p *gooProduct) template.HTML {
	na := template.HTML(`<span class="na">&ndash;</span>`)
	switch p.Type {
	case gooTypeVariable, gooSubsTypeVariable, gooTypeGrouped:
		if p.MinPrice == nil {
			return na
		}
		if p.MaxPrice == nil || *p.MaxPrice == *p.MinPrice {
			if gooSubsVariableLike(p.Type) {
				// One price for every variation: show the sale like a simple product's when they all are on sale.
				if vars, err := s.gooVariations(ctx, p.ID, false); err == nil && len(vars) > 0 {
					onSale, reg := true, -1.0
					for _, v := range vars {
						if !v.IsOnSale() || v.RegularPrice == nil {
							onSale = false
							break
						}
						if reg >= 0 && *v.RegularPrice != reg {
							onSale = false
							break
						}
						reg = *v.RegularPrice
					}
					if onSale {
						return gooSaleHTML(s.gooPrice(reg), s.gooPrice(*p.MinPrice), s.gooPricePlain(reg, s.gooCurrency()), s.gooPricePlain(*p.MinPrice, s.gooCurrency()))
					}
				}
			}
			return s.gooPrice(*p.MinPrice)
		}
		return template.HTML(`<span class="price-range">`) + s.gooPrice(*p.MinPrice) + ` <span aria-hidden="true">&ndash;</span><span class="screen-reader-text">Price range: ` +
			template.HTML(template.HTMLEscapeString(s.gooPricePlain(*p.MinPrice, s.gooCurrency())+" through "+s.gooPricePlain(*p.MaxPrice, s.gooCurrency()))) +
			`</span> ` + s.gooPrice(*p.MaxPrice) + `</span>`
	}
	if p.IsOnSale() && p.RegularPrice != nil {
		return gooSaleHTML(s.gooPrice(*p.RegularPrice), s.gooPrice(*p.SalePrice), s.gooPricePlain(*p.RegularPrice, s.gooCurrency()), s.gooPricePlain(*p.SalePrice, s.gooCurrency()))
	}
	if v := p.activePrice(time.Now()); v != nil {
		return s.gooPrice(*v)
	}
	return na
}

// gooSaleHTML is wc_format_sale_price().
func gooSaleHTML(regular, sale template.HTML, regPlain, salePlain string) template.HTML {
	return `<del aria-hidden="true">` + regular + `</del> <span class="screen-reader-text">Original price was: ` + template.HTML(template.HTMLEscapeString(regPlain)) +
		`.</span><ins aria-hidden="true">` + sale + `</ins><span class="screen-reader-text">Current price is: ` + template.HTML(template.HTMLEscapeString(salePlain)) + `.</span>`
}

// gooProductListNotices are the list's result notices (trashed, restored, deleted, updated, duplicated…).
func (s *Server) gooProductListNotices(c *gin.Context) []notice {
	out := postNotices(c, typeLabels{Type: "product", Name: "Products", Singular: "Product"})
	if c.Query("goo_error") == "duplicate" {
		out = append(out, notice{Type: "error", Message: "Product creation failed, could not find original product.", Dismissible: true})
	}
	return out
}

// Featured toggle, duplicate, ordering -----------------------------------------------------------------

// gooToggleFeatured is the star of the Featured column (woocommerce_feature_product).
func (s *Server) gooToggleFeatured(c *gin.Context) {
	ctx := c.Request.Context()
	if !s.checkNonce(c, "woocommerce-feature-product") {
		s.denyNonce(c)
		return
	}
	id, _ := strconv.ParseInt(c.Query("product_id"), 10, 64)
	p, err := s.gooGetProduct(ctx, id)
	if err != nil || p.Type == gooTypeVariation || !gooCanProduct(currentUser(c), "edit", p.Post) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit this item.")
		return
	}
	p.Featured = !p.Featured
	if _, err := s.store.Pool.Exec(ctx, `UPDATE goo_products SET featured = $2, updated_at = now() WHERE post_id = $1`, p.ID, p.Featured); err != nil ||
		s.gooEnsureData(ctx, p) != nil {
		c.String(http.StatusInternalServerError, "Could not update the product.")
		return
	}
	s.gooPurgeProduct(ctx, p)
	if c.GetHeader("X-Requested-With") == "XMLHttpRequest" {
		c.JSON(http.StatusOK, gin.H{"success": true, "featured": p.Featured})
		return
	}
	back := c.Request.Referer()
	if back == "" || !strings.Contains(back, "/wp-admin/") {
		back = "/wp-admin/edit?post_type=product"
	}
	c.Redirect(http.StatusFound, back)
}

// gooEnsureData writes the product row of a post that has none yet (a product made outside the editor).
func (s *Server) gooEnsureData(ctx context.Context, p *gooProduct) error {
	var ok bool
	_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM goo_products WHERE post_id = $1)`, p.ID).Scan(&ok)
	if ok {
		return nil
	}
	s.gooNormalizeProduct(p)
	return s.gooWriteProductData(ctx, p)
}

// gooDuplicateAction is WC_Admin_Duplicate_Product::duplicate_product_action().
func (s *Server) gooDuplicateAction(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := strconv.ParseInt(c.Query("post"), 10, 64)
	if !s.checkNonce(c, "woocommerce-duplicate-product_"+itoa64(id)) {
		s.denyNonce(c)
		return
	}
	if !currentUser(c).Can("publish_products", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to duplicate products.")
		return
	}
	p, err := s.gooGetProduct(ctx, id)
	if err != nil || p.Type == gooTypeVariation {
		c.Redirect(http.StatusFound, "/wp-admin/edit?post_type=product&goo_error=duplicate")
		return
	}
	dup, err := s.gooDuplicateProduct(ctx, p, currentUser(c).ID)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(http.StatusFound, links.EditLink(dup.ID))
}

// gooDuplicateProduct is WC_Admin_Duplicate_Product::product_duplicate(): a draft "(Copy)" without
// SKU (or with a unique "-1" SKU), not featured, with no sales or reviews, and copies of the variations.
func (s *Server) gooDuplicateProduct(ctx context.Context, src *gooProduct, editorID int64) (*gooProduct, error) {
	dup := *src
	dup.ID, dup.parent = 0, nil
	post := *src.Post
	post.ID, post.Slug, post.PublishedAt, post.CommentCount = 0, "", nil, 0
	post.Meta = gooCloneMeta(src.Post.Meta)
	dup.Post = &post
	dup.Name = src.Name + " (Copy)"
	dup.Slug = ""
	dup.Status = "draft"
	dup.Featured = false
	dup.TotalSales, dup.AverageRating, dup.RatingCount = 0, 0, 0
	dup.Meta = gooCloneMeta(src.Meta)
	dup.Attributes = append([]gooProductAttr(nil), src.Attributes...)
	dup.Downloads = append([]gooDownload(nil), src.Downloads...)
	for i := range dup.Downloads {
		dup.Downloads[i].ID = ""
	}
	if src.SKU != "" {
		dup.SKU = s.gooUniqueSKU(ctx, src.SKU)
	}
	if err := s.gooSaveProduct(ctx, &dup, editorID); err != nil {
		return nil, err
	}
	for _, tax := range []string{"product_cat", "product_tag"} {
		if terms, err := s.store.TermsForPosts(ctx, []int64{src.ID}, tax); err == nil {
			var ids []int64
			for _, t := range terms[src.ID] {
				ids = append(ids, t.ID)
			}
			_ = s.store.SetPostTerms(ctx, dup.ID, tax, ids)
		}
	}
	if gooSubsVariableLike(src.Type) {
		vars, _ := s.gooVariations(ctx, src.ID, true)
		for _, v := range vars {
			nv := *v
			nv.ID, nv.parent, nv.ParentID = 0, nil, dup.ID
			vp := *v.Post
			vp.ID, vp.Slug, vp.PublishedAt = 0, "", nil
			vp.Meta = gooCloneMeta(v.Post.Meta)
			nv.Post = &vp
			nv.Meta = gooCloneMeta(v.Meta)
			nv.Slug = ""
			nv.TotalSales = 0
			if v.SKU != "" {
				nv.SKU = s.gooUniqueSKU(ctx, v.SKU)
			}
			nv.Downloads = append([]gooDownload(nil), v.Downloads...)
			for i := range nv.Downloads {
				nv.Downloads[i].ID = ""
			}
			if err := s.gooSaveProduct(ctx, &nv, editorID); err != nil {
				return nil, err
			}
		}
		_ = s.gooSyncVariable(ctx, &dup)
	}
	s.gooFire(ctx, "product_duplicated", &dup)
	return &dup, nil
}

func gooCloneMeta(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// gooUniqueSKU is wc_product_generate_unique_sku(): sku-1, sku-2, …
func (s *Server) gooUniqueSKU(ctx context.Context, sku string) string {
	for i := 1; ; i++ {
		cand := sku + "-" + strconv.Itoa(i)
		if !s.gooSKUTaken(ctx, cand, 0) {
			return cand
		}
	}
}

// gooSKUTaken is ! wc_product_has_unique_sku(): another product or variation (not trashed) uses the SKU.
func (s *Server) gooSKUTaken(ctx context.Context, sku string, excludeID int64) bool {
	sku = strings.TrimSpace(sku)
	if sku == "" {
		return false
	}
	var taken bool
	_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM goo_products p JOIN posts po ON po.id = p.post_id
		WHERE p.sku = $1 AND p.post_id <> $2 AND po.status NOT IN ('trash','auto-draft')
		AND NOT (po.type = 'product_variation' AND po.parent_id IN (SELECT id FROM posts WHERE status = 'trash')))`, sku, excludeID).Scan(&taken)
	return taken
}

// gooProductOrdering is the drag and drop of the Sorting view (WC_AJAX::product_ordering): the product
// moves between its new neighbours and the menu orders are renumbered.
func (s *Server) gooProductOrdering(c *gin.Context) {
	ctx := c.Request.Context()
	if !s.checkNonce(c, "product-ordering") || !currentUser(c).Can("edit_others_products", nil) {
		c.JSON(http.StatusForbidden, gin.H{"success": false})
		return
	}
	id, _ := strconv.ParseInt(c.Query("id"), 10, 64)
	prev, _ := strconv.ParseInt(c.Query("previd"), 10, 64)
	next, _ := strconv.ParseInt(c.Query("nextid"), 10, 64)
	rows, err := s.store.Pool.Query(ctx, `SELECT id FROM posts WHERE type = 'product' AND status NOT IN ('trash','auto-draft')
		ORDER BY menu_order, lower(title), id`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false})
		return
	}
	var ids []int64
	for rows.Next() {
		var v int64
		if rows.Scan(&v) == nil && v != id {
			ids = append(ids, v)
		}
	}
	rows.Close()
	at := 0
	for i, v := range ids {
		if v == prev {
			at = i + 1
		}
		if v == next && prev == 0 {
			at = i
		}
	}
	ids = append(ids[:at], append([]int64{id}, ids[at:]...)...)
	out := map[string]int{}
	for i, v := range ids {
		_, _ = s.store.Pool.Exec(ctx, `UPDATE posts SET menu_order = $2 WHERE id = $1 AND menu_order <> $2`, v, i)
		out[itoa64(v)] = i
	}
	s.purgeTags("goo:shop")
	c.JSON(http.StatusOK, out)
}

// gooSearchProductsJSON is WC_AJAX::json_search_products_and_variations(): id → "Name (#SKU)" for
// the Linked Products fields. exclude / include_variations / limit like WooCommerce's.
func (s *Server) gooSearchProductsJSON(c *gin.Context) {
	ctx := c.Request.Context()
	term := strings.TrimSpace(c.Query("goo_search_products"))
	if term == "" {
		term = strings.TrimSpace(c.Query("term"))
	}
	out := map[string]string{}
	if term == "" {
		c.JSON(http.StatusOK, out)
		return
	}
	var exclude []int64
	for _, raw := range strings.Split(c.Query("exclude"), ",") {
		if id, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); id > 0 {
			exclude = append(exclude, id)
		}
	}
	q := gooProductQuery{Search: term, Status: []string{"publish", "private", "draft", "pending", "future"}, Exclude: exclude, PerPage: 30, OrderBy: "title"}
	if c.Query("include_variations") == "1" || c.Query("include_variations") == "true" {
		q.Types = []string{gooTypeSimple, gooTypeVariable, gooTypeGrouped, gooTypeExternal, gooTypeVariation}
		for t := range gooProductTypes {
			q.Types = append(q.Types, t)
		}
	}
	if id, _ := strconv.ParseInt(strings.TrimPrefix(term, "#"), 10, 64); id > 0 {
		q.Search, q.IDs = "", []int64{id}
	}
	list, _, err := s.gooQueryProducts(ctx, q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, out)
		return
	}
	for _, p := range list {
		out[itoa64(p.ID)] = s.gooFormattedName(ctx, p)
	}
	c.JSON(http.StatusOK, out)
}

// gooFormattedName is get_formatted_name(): "Name (#SKU)" or "Name (#ID)"; variations add their attributes.
func (s *Server) gooFormattedName(ctx context.Context, p *gooProduct) string {
	name := p.Name
	if p.Type == gooTypeVariation {
		name = s.gooProductTitle(ctx, p)
		if sum := s.gooVariationSummary(ctx, p); sum != "" {
			name += " – " + sum
		}
	}
	ident := "#" + itoa64(p.ID)
	if p.SKU != "" {
		ident = p.SKU
	}
	return name + " (" + ident + ")"
}

// Bulk actions ------------------------------------------------------------------------------------------

// gooProductsBulk applies a bulk action of the products list: trash, untrash, delete, edit.
func (s *Server) gooProductsBulk(c *gin.Context, action string) {
	ctx := c.Request.Context()
	u := currentUser(c)
	if !s.checkNonce(c, "bulk-posts") {
		s.denyNonce(c)
		return
	}
	var ids []int64
	seen := map[int64]bool{}
	for _, raw := range c.QueryArray("post[]") {
		if id, _ := strconv.ParseInt(raw, 10, 64); id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	n := 0
	var done []string
	for _, id := range ids {
		p, err := s.gooGetProduct(ctx, id)
		if err != nil || p.Type == gooTypeVariation {
			continue
		}
		if action == "edit" {
			if !gooCanProduct(u, "edit", p.Post) {
				continue
			}
			if s.gooBulkEditProduct(c, p) == nil {
				n++
			}
			continue
		}
		if !gooCanProduct(u, "delete", p.Post) {
			continue
		}
		if s.gooTrashAction(ctx, p, action) == nil {
			n++
			done = append(done, itoa64(id))
		}
	}
	key := map[string]string{"trash": "trashed", "untrash": "untrashed", "delete": "deleted", "edit": "updated"}[action]
	target := "/wp-admin/edit?post_type=product"
	if st := c.Query("post_status"); st != "" && st != "all" && action == "edit" {
		target += "&post_status=" + url.QueryEscape(st)
	}
	if key != "" {
		target += "&" + key + "=" + strconv.Itoa(n)
		if action == "trash" {
			target += "&ids=" + strings.Join(done, ",")
		}
	}
	c.Redirect(http.StatusFound, target)
}

// gooTrashAction trashes, restores or deletes a product with its variations.
func (s *Server) gooTrashAction(ctx context.Context, p *gooProduct, action string) error {
	var err error
	switch action {
	case "trash":
		err = s.store.TrashPost(ctx, p.ID)
	case "untrash":
		err = s.store.UntrashPost(ctx, p.ID)
	case "delete":
		if gooSubsVariableLike(p.Type) {
			if vars, verr := s.gooVariations(ctx, p.ID, true); verr == nil {
				for _, v := range vars {
					_ = s.store.DeletePost(ctx, v.ID)
				}
			}
		}
		err = s.store.DeletePost(ctx, p.ID)
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	if err == nil {
		s.afterPostSave(ctx, p.Post)
		s.gooPurgeProduct(ctx, p)
	}
	return err
}

// gooBulkEditProduct is WC_Admin_Post_Types::bulk_edit_save(): only the fields set in the Bulk Edit
// panel change. Prices apply to simple and external products, like WooCommerce.
func (s *Server) gooBulkEditProduct(c *gin.Context, p *gooProduct) error {
	ctx := c.Request.Context()
	q := c.Request.URL.Query()
	val := func(k string) string { return strings.TrimSpace(q.Get(k)) }
	set := func(k string) bool { v := val(k); return v != "" && v != "-1" }
	priceable := p.Type == gooTypeSimple || p.Type == gooTypeExternal || (gooProductTypes[p.Type] != nil)
	if priceable {
		if ch := val("change_regular_price"); ch != "" {
			p.RegularPrice = s.gooChangePrice(p.RegularPrice, ch, val("_regular_price"), nil)
		}
		if ch := val("change_sale_price"); ch != "" {
			p.SalePrice = s.gooChangePrice(p.SalePrice, ch, val("_sale_price"), p.RegularPrice)
		}
	}
	if set("_status") {
		p.Status = val("_status")
	}
	if set("_tax_status") {
		p.TaxStatus = val("_tax_status")
	}
	if q.Has("_tax_class") && val("_tax_class") != "_no_change" {
		p.TaxClass = val("_tax_class")
		if p.TaxClass == "standard" {
			p.TaxClass = ""
		}
	}
	if val("change_weight") != "" {
		p.Weight = val("_weight")
	}
	if val("change_dimensions") != "" {
		p.Length, p.Width, p.Height = val("_length"), val("_width"), val("_height")
	}
	if set("_shipping_class") && val("_shipping_class") != "_no_shipping_class" {
		id, _ := strconv.ParseInt(val("_shipping_class"), 10, 64)
		p.ShippingClassID = id
	} else if val("_shipping_class") == "_no_shipping_class" {
		p.ShippingClassID = 0
	}
	if set("_visibility") {
		p.CatalogVisibility = val("_visibility")
	}
	if set("_featured") {
		p.Featured = val("_featured") == "yes"
	}
	if set("_sold_individually") {
		p.SoldIndividually = val("_sold_individually") == "yes"
	}
	if set("_manage_stock") && p.Type != gooTypeGrouped && p.Type != gooTypeExternal {
		p.ManageStock = val("_manage_stock") == "yes"
	}
	if set("_backorders") {
		p.Backorders = val("_backorders")
	}
	if ch := val("change_stock"); ch != "" && p.ManageStock {
		if n, err := strconv.Atoi(val("_stock")); err == nil {
			cur := 0
			if p.StockQuantity != nil {
				cur = *p.StockQuantity
			}
			switch ch {
			case "2":
				n = cur + n
			case "3":
				n = cur - n
			}
			p.StockQuantity = &n
		}
	}
	if set("_stock_status") && (!p.ManageStock || gooSubsVariableLike(p.Type)) && p.Type != gooTypeExternal {
		p.StockStatus = val("_stock_status")
	}
	return s.gooSaveProduct(ctx, p, currentUser(c).ID)
}

// gooChangePrice applies a bulk price change: 1 set, 2 increase, 3 decrease (fixed or %), 4 regular
// price decreased by (sale prices only).
func (s *Server) gooChangePrice(cur *float64, mode, raw string, regular *float64) *float64 {
	percent := strings.HasSuffix(raw, "%")
	v := s.gooParseAmount(strings.TrimSuffix(raw, "%"))
	if mode == "1" {
		if v == nil {
			return nil
		}
		return gooFloatPtr(gooRound(*v, s.gooDecimals()))
	}
	if v == nil {
		return cur
	}
	base := cur
	if mode == "4" {
		base = regular
	}
	if base == nil {
		return cur
	}
	delta := *v
	if percent {
		delta = *base * *v / 100
	}
	var out float64
	switch mode {
	case "2":
		out = *base + delta
	case "3", "4":
		out = math.Max(0, *base-delta)
	default:
		return cur
	}
	return gooFloatPtr(gooRound(out, s.gooDecimals()))
}

// Entry points of the other catalog screens (implemented in goo_admin_catalog*.go).

func (s *Server) gooAdminProductEdit(c *gin.Context, id int64) { s.gooProductEditor(c, id) }
func (s *Server) gooAdminAttributes(c *gin.Context)            { s.gooAttributesScreen(c) }
func (s *Server) gooAdminReviews(c *gin.Context)               { s.gooReviewsScreen(c) }

// gooCanProduct is map_meta_cap() for products: edit / delete with WooCommerce's product capabilities.
func gooCanProduct(u *store.User, action string, p *store.Post) bool {
	if u == nil || p == nil {
		return false
	}
	if !u.Can(action+"_products", nil) {
		return false
	}
	if p.AuthorID != 0 && p.AuthorID != u.ID && !u.Can(action+"_others_products", nil) {
		return false
	}
	switch p.Status {
	case "publish", "future", "trash":
		return u.Can(action+"_published_products", nil)
	case "private":
		return u.Can(action+"_private_products", nil)
	}
	return true
}
