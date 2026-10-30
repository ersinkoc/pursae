package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// wc/v3 products, variations, attributes (+ terms), categories, tags, shipping classes and reviews
// (WC_REST_Products_Controller and friends), with WooCommerce's fields and query parameters.

func (s *Server) registerGooV3Products() {
	prod := gooCaps{"edit_products", "edit_products", "delete_products"}
	terms := gooCapsAll("manage_product_terms")
	s.gooV3("/products", prod, map[string]restHandler{http.MethodGet: s.gooV3ProductsList, http.MethodPost: s.gooV3Create(s.gooV3ProductCreate)})
	s.gooV3("/products/batch", prod, map[string]restHandler{http.MethodPost: func(c *gin.Context, req *restRequest) {
		s.gooBatch(c, req, gooBatchOps{Create: s.gooV3ProductCreate, Update: s.gooV3ProductUpdate, Delete: s.gooV3ProductDelete})
	}})
	s.gooV3("/products/(?P<id>[\\d]+)", prod, map[string]restHandler{
		http.MethodGet: s.gooV3Get(func(c *gin.Context, id int64) (map[string]any, *gooAPIErr) {
			return s.gooV3ProductGet(c.Request.Context(), id, false)
		}),
		http.MethodPut:    s.gooV3Update(s.gooV3ProductUpdate),
		http.MethodDelete: s.gooV3Delete(s.gooV3ProductDelete),
	})
	s.gooV3("/products/(?P<product_id>[\\d]+)/duplicate", prod, map[string]restHandler{http.MethodPost: s.gooV3ProductDuplicate})
	s.gooV3("/products/(?P<product_id>[\\d]+)/variations", prod, map[string]restHandler{http.MethodGet: s.gooV3VariationsList,
		http.MethodPost: func(c *gin.Context, req *restRequest) {
			obj, err := s.gooV3VariationCreate(c, gooInt(req.Params["product_id"]), req.Body)
			if err != nil {
				gooFail(c, err)
				return
			}
			restJSON(c, http.StatusCreated, obj)
		}})
	s.gooV3("/products/(?P<product_id>[\\d]+)/variations/batch", prod, map[string]restHandler{http.MethodPost: func(c *gin.Context, req *restRequest) {
		pid := gooInt(req.Params["product_id"])
		s.gooBatch(c, req, gooBatchOps{
			Create: func(c *gin.Context, b map[string]any) (map[string]any, *gooAPIErr) {
				return s.gooV3VariationCreate(c, pid, b)
			},
			Update: func(c *gin.Context, id int64, b map[string]any) (map[string]any, *gooAPIErr) {
				return s.gooV3VariationUpdate(c, pid, id, b)
			},
			Delete: func(c *gin.Context, id int64, force bool) (map[string]any, *gooAPIErr) {
				return s.gooV3VariationDelete(c, pid, id, force)
			},
		})
	}})
	s.gooV3("/products/(?P<product_id>[\\d]+)/variations/(?P<id>[\\d]+)", prod, map[string]restHandler{
		http.MethodGet: func(c *gin.Context, req *restRequest) {
			v, err := s.gooV3Variation(c.Request.Context(), gooInt(req.Params["product_id"]), req.ID())
			if err != nil {
				gooFail(c, err)
				return
			}
			restJSON(c, http.StatusOK, s.gooV3VariationJSON(c.Request.Context(), v))
		},
		http.MethodPut: func(c *gin.Context, req *restRequest) {
			obj, err := s.gooV3VariationUpdate(c, gooInt(req.Params["product_id"]), req.ID(), req.Body)
			if err != nil {
				gooFail(c, err)
				return
			}
			restJSON(c, http.StatusOK, obj)
		},
		http.MethodDelete: func(c *gin.Context, req *restRequest) {
			force, _ := gooQueryBool(c, "force")
			if v, ok := req.Body["force"]; ok {
				force = gooBool(v)
			}
			obj, err := s.gooV3VariationDelete(c, gooInt(req.Params["product_id"]), req.ID(), force)
			if err != nil {
				gooFail(c, err)
				return
			}
			restJSON(c, http.StatusOK, obj)
		},
	})

	// Attributes and their terms.
	s.gooV3("/products/attributes", terms, map[string]restHandler{http.MethodGet: s.gooV3AttributesList, http.MethodPost: s.gooV3Create(s.gooV3AttributeCreate)})
	s.gooV3("/products/attributes/batch", terms, map[string]restHandler{http.MethodPost: func(c *gin.Context, req *restRequest) {
		s.gooBatch(c, req, gooBatchOps{Create: s.gooV3AttributeCreate, Update: s.gooV3AttributeUpdate, Delete: s.gooV3AttributeDelete})
	}})
	s.gooV3("/products/attributes/(?P<id>[\\d]+)", terms, map[string]restHandler{
		http.MethodGet: s.gooV3Get(func(c *gin.Context, id int64) (map[string]any, *gooAPIErr) {
			a := s.gooAttributeByID(c.Request.Context(), id)
			if a == nil {
				return nil, gooErr(404, "woocommerce_rest_attribute_invalid", "Resource does not exist.")
			}
			return s.gooV3AttributeJSON(c.Request.Context(), a), nil
		}),
		http.MethodPut:    s.gooV3Update(s.gooV3AttributeUpdate),
		http.MethodDelete: s.gooV3Delete(s.gooV3AttributeDelete),
	})
	attrTax := func(c *gin.Context, req *restRequest) (string, *gooAPIErr) {
		a := s.gooAttributeByID(c.Request.Context(), gooInt(req.Params["attribute_id"]))
		if a == nil {
			return "", gooErr(404, "woocommerce_rest_taxonomy_invalid", "Resource does not exist.")
		}
		return a.Taxonomy(), nil
	}
	s.gooTermRoutes("/products/attributes/(?P<attribute_id>[\\d]+)/terms", terms, attrTax)
	fixed := func(tax string) func(*gin.Context, *restRequest) (string, *gooAPIErr) {
		return func(*gin.Context, *restRequest) (string, *gooAPIErr) { return tax, nil }
	}
	s.gooTermRoutes("/products/categories", terms, fixed("product_cat"))
	s.gooTermRoutes("/products/tags", terms, fixed("product_tag"))
	s.gooTermRoutes("/products/shipping_classes", terms, fixed("product_shipping_class"))

	// Reviews.
	rev := gooCapsAll("moderate_comments")
	s.gooV3("/products/reviews", rev, map[string]restHandler{http.MethodGet: s.gooV3ReviewsList, http.MethodPost: s.gooV3Create(s.gooV3ReviewCreate)})
	s.gooV3("/products/reviews/batch", rev, map[string]restHandler{http.MethodPost: func(c *gin.Context, req *restRequest) {
		s.gooBatch(c, req, gooBatchOps{Create: s.gooV3ReviewCreate, Update: s.gooV3ReviewUpdate, Delete: s.gooV3ReviewDelete})
	}})
	s.gooV3("/products/reviews/(?P<id>[\\d]+)", rev, map[string]restHandler{
		http.MethodGet: s.gooV3Get(func(c *gin.Context, id int64) (map[string]any, *gooAPIErr) {
			r, err := s.gooReviewByID(c.Request.Context(), id)
			if err != nil {
				return nil, err
			}
			return s.gooV3ReviewJSON(c.Request.Context(), r), nil
		}),
		http.MethodPut:    s.gooV3Update(s.gooV3ReviewUpdate),
		http.MethodDelete: s.gooV3Delete(s.gooV3ReviewDelete),
	})
}

// Generic handlers --------------------------------------------------------------------------------------

func (s *Server) gooV3Get(fn func(c *gin.Context, id int64) (map[string]any, *gooAPIErr)) restHandler {
	return func(c *gin.Context, req *restRequest) {
		obj, err := fn(c, req.ID())
		if err != nil {
			gooFail(c, err)
			return
		}
		restJSON(c, http.StatusOK, obj)
	}
}

func (s *Server) gooV3Create(fn func(c *gin.Context, b map[string]any) (map[string]any, *gooAPIErr)) restHandler {
	return func(c *gin.Context, req *restRequest) {
		if _, ok := req.Body["id"]; ok && gooInt(req.Body["id"]) != 0 {
			restErr(c, http.StatusBadRequest, "woocommerce_rest_resource_exists", "Cannot create existing resource.")
			return
		}
		obj, err := fn(c, gooFormBody(c, req))
		if err != nil {
			gooFail(c, err)
			return
		}
		restJSON(c, http.StatusCreated, obj)
	}
}

func (s *Server) gooV3Update(fn func(c *gin.Context, id int64, b map[string]any) (map[string]any, *gooAPIErr)) restHandler {
	return func(c *gin.Context, req *restRequest) {
		obj, err := fn(c, req.ID(), gooFormBody(c, req))
		if err != nil {
			gooFail(c, err)
			return
		}
		restJSON(c, http.StatusOK, obj)
	}
}

func (s *Server) gooV3Delete(fn func(c *gin.Context, id int64, force bool) (map[string]any, *gooAPIErr)) restHandler {
	return func(c *gin.Context, req *restRequest) {
		force, _ := gooQueryBool(c, "force")
		if v, ok := req.Body["force"]; ok {
			force = gooBool(v)
		}
		obj, err := fn(c, req.ID(), force)
		if err != nil {
			gooFail(c, err)
			return
		}
		restJSON(c, http.StatusOK, obj)
	}
}

// gooFormBody is the request body; form-encoded and query parameters fill in when there is no JSON.
func gooFormBody(c *gin.Context, req *restRequest) map[string]any {
	if len(req.Body) > 0 {
		return req.Body
	}
	b := map[string]any{}
	_ = c.Request.ParseForm()
	for k, v := range c.Request.Form {
		if len(v) > 0 && !strings.HasPrefix(k, "consumer_") && k != "_method" && k != "_wpnonce" {
			b[k] = v[0]
		}
	}
	return b
}

// Products -----------------------------------------------------------------------------------------------

func gooProductErr() *gooAPIErr {
	return gooErr(http.StatusNotFound, "woocommerce_rest_product_invalid_id", "Invalid ID.")
}

func (s *Server) gooV3ProductGet(ctx context.Context, id int64, allowVariation bool) (map[string]any, *gooAPIErr) {
	p, err := s.gooGetProduct(ctx, id)
	if err != nil || (p.Type == gooTypeVariation && !allowVariation) || p.Status == "auto-draft" {
		return nil, gooProductErr()
	}
	return s.gooV3ProductJSON(ctx, p), nil
}

// gooTermsOf lists a product's terms of a taxonomy as {id, name, slug}.
func (s *Server) gooTermsOf(ctx context.Context, id int64, tax string) []any {
	out := []any{}
	terms, _ := s.store.TermsForPosts(ctx, []int64{id}, tax)
	for _, t := range terms[id] {
		out = append(out, map[string]any{"id": t.ID, "name": t.Name, "slug": t.Slug})
	}
	return out
}

// gooV3Images lists product images as WooCommerce's {id, date_created, src, name, alt}.
func (s *Server) gooV3Images(ctx context.Context, ids []int64) []any {
	out := []any{}
	for _, id := range ids {
		a, err := s.store.GetAttachment(ctx, id)
		if err != nil {
			continue
		}
		out = append(out, map[string]any{"id": a.ID, "date_created": s.gooDate(&a.CreatedAt), "date_created_gmt": gooDateGMT(&a.CreatedAt),
			"date_modified": s.gooDate(&a.UpdatedAt), "date_modified_gmt": gooDateGMT(&a.UpdatedAt), "src": s.uploadURL(ctx, a.FilePath), "name": a.Title, "alt": a.Alt})
	}
	return out
}

// gooV3AttrOptions are an attribute's options as names (term names for taxonomy attributes).
func (s *Server) gooV3AttrOptions(ctx context.Context, a gooProductAttr) []string {
	out := []string{}
	for _, o := range a.Options {
		out = append(out, s.gooAttrValueLabel(ctx, a, o))
	}
	return out
}

// gooRelatedIDs are wc_get_related_products(): products sharing a category or tag.
func (s *Server) gooRelatedIDs(ctx context.Context, id int64) []int64 {
	out := []int64{}
	rows, err := s.store.Pool.Query(ctx, `SELECT DISTINCT pt2.post_id FROM post_terms pt1 JOIN terms t ON t.id = pt1.term_id AND t.taxonomy IN ('product_cat','product_tag')
		JOIN post_terms pt2 ON pt2.term_id = pt1.term_id JOIN posts p ON p.id = pt2.post_id
		WHERE pt1.post_id = $1 AND pt2.post_id <> $1 AND p.type = 'product' AND p.status = 'publish' ORDER BY pt2.post_id LIMIT 5`, id)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var r int64
		if rows.Scan(&r) == nil {
			out = append(out, r)
		}
	}
	return out
}

// gooV3ProductJSON is a product as WC_REST_Products_Controller returns it.
func (s *Server) gooV3ProductJSON(ctx context.Context, p *gooProduct) map[string]any {
	post := p.Post
	if post == nil {
		post = &store.Post{}
	}
	created, modified := post.Date(), post.ModifiedAt
	price := ""
	if p.HasPrice() {
		price = gooFormatDecimal(p.PriceValue(), -1)
	}
	attrs := []any{}
	for i, a := range p.Attributes {
		attrs = append(attrs, map[string]any{"id": a.ID, "name": s.gooAttrLabel(ctx, a), "slug": defaultStr(a.Taxonomy, a.Key()), "position": defaultInt(a.Position, i),
			"visible": a.Visible, "variation": a.Variation, "options": s.gooV3AttrOptions(ctx, a)})
	}
	defAttrs := []any{}
	for _, a := range p.Attributes {
		if v, ok := p.DefaultAttributes[a.Key()]; ok && v != "" {
			defAttrs = append(defAttrs, map[string]any{"id": a.ID, "name": s.gooAttrLabel(ctx, a), "option": s.gooAttrValueLabel(ctx, a, v)})
		}
	}
	shipClass := ""
	if p.ShippingClassID != 0 {
		if t, err := s.store.GetTerm(ctx, p.ShippingClassID); err == nil {
			shipClass = t.Slug
		}
	}
	var variations []int64
	if p.Type == gooTypeVariable {
		vars, _ := s.gooVariations(ctx, p.ID, true)
		for _, v := range vars {
			variations = append(variations, v.ID)
		}
	}
	grouped := []int64{}
	if p.Type == gooTypeGrouped {
		grouped = nonNilSlice(p.Children)
	}
	var stockQty any
	if p.StockQuantity != nil {
		stockQty = *p.StockQuantity
	}
	var lowStock any
	if p.LowStockAmount != nil {
		lowStock = *p.LowStockAmount
	}
	downloads := []any{}
	for _, d := range p.Downloads {
		downloads = append(downloads, map[string]any{"id": d.ID, "name": d.Name, "file": d.File})
	}
	return map[string]any{
		"id": p.ID, "name": p.Name, "slug": p.Slug, "permalink": s.gooPermalink(ctx, p),
		"date_created": s.gooDate(&created), "date_created_gmt": gooDateGMT(&created), "date_modified": s.gooDate(&modified), "date_modified_gmt": gooDateGMT(&modified),
		"type": p.Type, "status": p.Status, "featured": p.Featured, "catalog_visibility": p.CatalogVisibility,
		"description": post.Content, "short_description": post.Excerpt, "sku": p.SKU, "global_unique_id": "",
		"price": price, "regular_price": gooDecPtr(p.RegularPrice), "sale_price": gooDecPtr(p.SalePrice),
		"date_on_sale_from": s.gooDate(p.SaleFrom), "date_on_sale_from_gmt": gooDateGMT(p.SaleFrom), "date_on_sale_to": s.gooDate(p.SaleTo), "date_on_sale_to_gmt": gooDateGMT(p.SaleTo),
		"price_html": s.gooPriceHTML(ctx, p), "on_sale": p.IsOnSale(), "purchasable": s.gooIsPurchasable(ctx, p), "total_sales": p.TotalSales,
		"virtual": p.Virtual, "downloadable": p.Downloadable, "downloads": downloads, "download_limit": p.DownloadLimit, "download_expiry": p.DownloadExpiry,
		"external_url": p.ProductURL, "button_text": p.ButtonText, "tax_status": p.TaxStatus, "tax_class": p.TaxClass,
		"manage_stock": p.ManageStock, "stock_quantity": stockQty, "stock_status": p.StockStatus, "backorders": p.Backorders,
		"backorders_allowed": p.Backorders != "no", "backordered": p.StockQuantity != nil && *p.StockQuantity < 0 && p.ManageStock,
		"low_stock_amount": lowStock, "sold_individually": p.SoldIndividually, "weight": p.Weight,
		"dimensions":        map[string]any{"length": p.Length, "width": p.Width, "height": p.Height},
		"shipping_required": s.gooNeedsShipping(ctx, p), "shipping_taxable": p.TaxStatus == "taxable" || p.TaxStatus == "shipping",
		"shipping_class": shipClass, "shipping_class_id": p.ShippingClassID, "reviews_allowed": post.CommentStatus == "open",
		"average_rating": strconv.FormatFloat(p.AverageRating, 'f', 2, 64), "rating_count": p.RatingCount,
		"related_ids": s.gooRelatedIDs(ctx, p.ID), "upsell_ids": nonNilSlice(p.UpsellIDs), "cross_sell_ids": nonNilSlice(p.CrossSellIDs),
		"parent_id": p.ParentID, "purchase_note": p.PurchaseNote,
		"categories": s.gooTermsOf(ctx, p.ID, "product_cat"), "tags": s.gooTermsOf(ctx, p.ID, "product_tag"), "brands": []any{},
		"images": s.gooV3Images(ctx, gooProductImageIDs(p)), "attributes": attrs, "default_attributes": defAttrs,
		"variations": nonNilSlice(variations), "grouped_products": grouped, "menu_order": post.MenuOrder,
		"post_password": post.Password, "has_options": p.Type == gooTypeVariable, "meta_data": gooMetaJSON(p.Meta),
		"_links": s.gooLinks(ctx, "products", p.ID),
	}
}


// gooProductStatuses are the post statuses a product can have through the API.
var gooProductStatuses = []string{"draft", "pending", "private", "publish", "future"}

// gooV3ApplyProduct writes the request fields onto a product (prepare_object_for_database).
func (s *Server) gooV3ApplyProduct(ctx context.Context, p *gooProduct, b map[string]any) (cats, tags []int64, setCats, setTags bool, aerr *gooAPIErr) {
	if p.Post == nil {
		p.Post = &store.Post{Type: "product", CommentStatus: "open", PingStatus: "closed", Meta: map[string]any{}}
		if p.Type == gooTypeVariation {
			p.Post.Type, p.Post.CommentStatus = "product_variation", "closed"
		}
	}
	if v, ok := b["name"]; ok {
		p.Name = gooStr(v)
	}
	if v, ok := b["slug"]; ok {
		p.Slug = wp.SanitizeTitle(gooStr(v))
	}
	if v, ok := b["type"]; ok && p.Type != gooTypeVariation {
		t := gooStr(v)
		if !containsStr([]string{gooTypeSimple, gooTypeVariable, gooTypeGrouped, gooTypeExternal}, t) && gooProductTypes[t] == nil {
			return nil, nil, false, false, gooErr(400, "rest_invalid_param", "Invalid parameter(s): type")
		}
		p.Type = t
	}
	if v, ok := b["status"]; ok {
		st := gooStr(v)
		if !containsStr(gooProductStatuses, st) {
			return nil, nil, false, false, gooErr(400, "rest_invalid_param", "Invalid parameter(s): status")
		}
		p.Status = st
	}
	if v, ok := b["featured"]; ok {
		p.Featured = gooBool(v)
	}
	if v, ok := b["catalog_visibility"]; ok {
		p.CatalogVisibility = gooStr(v)
	}
	if v, ok := b["description"]; ok {
		if p.Type == gooTypeVariation {
			p.Description = gooStr(v)
		} else {
			p.Post.Content = gooStr(v)
		}
	}
	if v, ok := b["short_description"]; ok {
		p.Post.Excerpt = gooStr(v)
	}
	if v, ok := b["sku"]; ok {
		sku := strings.TrimSpace(gooStr(v))
		if sku != "" {
			var taken bool
			_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM goo_products gp JOIN posts po ON po.id = gp.post_id WHERE gp.sku = $1 AND gp.post_id <> $2 AND po.status <> 'trash')`, sku, p.ID).Scan(&taken)
			if taken {
				return nil, nil, false, false, gooErr(400, "product_invalid_sku", "Invalid or duplicated SKU.")
			}
		}
		p.SKU = sku
	}
	if v, ok := b["regular_price"]; ok {
		p.RegularPrice = gooRESTAmount(v)
	}
	if v, ok := b["sale_price"]; ok {
		p.SalePrice = gooRESTAmount(v)
	}
	for _, k := range []string{"date_on_sale_from", "date_on_sale_to"} {
		t, set, err := s.gooBodyDate(b, k)
		if err != nil {
			return nil, nil, false, false, err
		}
		if set {
			if k == "date_on_sale_from" {
				p.SaleFrom = t
			} else {
				p.SaleTo = t
			}
		}
	}
	bools := map[string]*bool{"virtual": &p.Virtual, "downloadable": &p.Downloadable, "manage_stock": &p.ManageStock, "sold_individually": &p.SoldIndividually}
	for k, dst := range bools {
		if v, ok := b[k]; ok {
			*dst = gooBool(v)
		}
	}
	if v, ok := b["downloads"]; ok {
		p.Downloads = nil
		for _, x := range gooList(v) {
			m := gooObj(x)
			if f := gooStr(m["file"]); f != "" {
				p.Downloads = append(p.Downloads, gooDownload{ID: gooStr(m["id"]), Name: gooStr(m["name"]), File: f})
			}
		}
	}
	if v, ok := b["download_limit"]; ok {
		p.DownloadLimit = int(gooInt(v))
	}
	if v, ok := b["download_expiry"]; ok {
		p.DownloadExpiry = int(gooInt(v))
	}
	strs := map[string]*string{"external_url": &p.ProductURL, "button_text": &p.ButtonText, "tax_status": &p.TaxStatus, "tax_class": &p.TaxClass,
		"stock_status": &p.StockStatus, "backorders": &p.Backorders, "weight": &p.Weight, "purchase_note": &p.PurchaseNote}
	for k, dst := range strs {
		if v, ok := b[k]; ok {
			*dst = gooStr(v)
		}
	}
	if p.TaxClass == "standard" {
		p.TaxClass = ""
	}
	if v, ok := b["stock_quantity"]; ok {
		if v == nil || gooStr(v) == "" {
			p.StockQuantity = nil
		} else {
			p.StockQuantity = gooIntPtr(int(gooInt(v)))
		}
	}
	if v, ok := b["low_stock_amount"]; ok {
		if v == nil || gooStr(v) == "" {
			p.LowStockAmount = nil
		} else {
			p.LowStockAmount = gooIntPtr(int(gooInt(v)))
		}
	}
	if d := gooObj(b["dimensions"]); d != nil {
		if v, ok := d["length"]; ok {
			p.Length = gooStr(v)
		}
		if v, ok := d["width"]; ok {
			p.Width = gooStr(v)
		}
		if v, ok := d["height"]; ok {
			p.Height = gooStr(v)
		}
	}
	if v, ok := b["shipping_class"]; ok {
		p.ShippingClassID = 0
		if slug := gooStr(v); slug != "" {
			if t, err := s.store.TermBySlug(ctx, "product_shipping_class", slug); err == nil {
				p.ShippingClassID = t.ID
			}
		}
	}
	if v, ok := b["shipping_class_id"]; ok {
		p.ShippingClassID = gooInt(v)
	}
	if v, ok := b["reviews_allowed"]; ok {
		p.Post.CommentStatus = map[bool]string{true: "open", false: "closed"}[gooBool(v)]
	}
	if v, ok := b["upsell_ids"]; ok {
		p.UpsellIDs = gooIDList(v)
	}
	if v, ok := b["cross_sell_ids"]; ok {
		p.CrossSellIDs = gooIDList(v)
	}
	if v, ok := b["parent_id"]; ok && p.Type != gooTypeVariation {
		p.ParentID = gooInt(v)
	}
	if v, ok := b["grouped_products"]; ok {
		p.Children = gooIDList(v)
	}
	if v, ok := b["menu_order"]; ok {
		p.Post.MenuOrder = int(gooInt(v))
	}
	if v, ok := b["post_password"]; ok {
		p.Post.Password = gooStr(v)
	}
	if v, ok := b["categories"]; ok {
		cats, setCats = gooIDList(v), true
	}
	if v, ok := b["tags"]; ok {
		tags, setTags = gooIDList(v), true
	}
	if v, ok := b["images"]; ok && p.Type != gooTypeVariation {
		ids, err := s.gooV3ImageIDs(ctx, gooList(v))
		if err != nil {
			return nil, nil, false, false, err
		}
		p.ImageID, p.GalleryIDs = 0, nil
		if len(ids) > 0 {
			p.ImageID, p.GalleryIDs = ids[0], ids[1:]
		}
	}
	if v, ok := b["image"]; ok && p.Type == gooTypeVariation {
		p.ImageID = 0
		if m := gooObj(v); m != nil {
			ids, err := s.gooV3ImageIDs(ctx, []any{m})
			if err != nil {
				return nil, nil, false, false, err
			}
			if len(ids) > 0 {
				p.ImageID = ids[0]
			}
		}
	}
	if v, ok := b["attributes"]; ok {
		if p.Type == gooTypeVariation {
			p.VariationAttrs = map[string]string{}
			parent := s.gooParent(ctx, p)
			for _, x := range gooList(v) {
				m := gooObj(x)
				key := s.gooV3AttrKey(ctx, m, parent)
				if key == "" {
					continue
				}
				p.VariationAttrs[key] = s.gooResolveAttrValue(ctx, key, gooStr(m["option"]))
			}
		} else {
			attrs, err := s.gooV3ProductAttrs(ctx, gooList(v))
			if err != nil {
				return nil, nil, false, false, err
			}
			p.Attributes = attrs
		}
	}
	if v, ok := b["default_attributes"]; ok {
		p.DefaultAttributes = map[string]string{}
		for _, x := range gooList(v) {
			m := gooObj(x)
			if key := s.gooV3AttrKey(ctx, m, p); key != "" {
				p.DefaultAttributes[key] = s.gooResolveAttrValue(ctx, key, gooStr(m["option"]))
			}
		}
	}
	if v, ok := b["meta_data"]; ok {
		gooApplyMeta(p.Meta, v)
	}
	return cats, tags, setCats, setTags, nil
}

// gooV3AttrKey finds the attribute key an {id, name} pair refers to.
func (s *Server) gooV3AttrKey(ctx context.Context, m map[string]any, p *gooProduct) string {
	if id := gooInt(m["id"]); id > 0 {
		if a := s.gooAttributeByID(ctx, id); a != nil {
			return a.Taxonomy()
		}
		return ""
	}
	name := gooStr(m["name"])
	if name == "" {
		return ""
	}
	if p != nil {
		for _, a := range p.Attributes {
			if strings.EqualFold(a.Name, name) || strings.EqualFold(a.Key(), name) || strings.EqualFold(s.gooAttrLabel(ctx, a), name) {
				return a.Key()
			}
		}
	}
	if strings.HasPrefix(name, "pa_") {
		return name
	}
	return gooSanitizeKey(name)
}

// gooV3ProductAttrs reads attributes [{id, name, position, visible, variation, options}].
func (s *Server) gooV3ProductAttrs(ctx context.Context, list []any) ([]gooProductAttr, *gooAPIErr) {
	var out []gooProductAttr
	for i, x := range list {
		m := gooObj(x)
		a := gooProductAttr{Position: i, Visible: true}
		if v, ok := m["position"]; ok {
			a.Position = int(gooInt(v))
		}
		if v, ok := m["visible"]; ok {
			a.Visible = gooBool(v)
		}
		a.Variation = gooBool(m["variation"])
		var options []string
		switch o := m["options"].(type) {
		case []any:
			for _, x := range o {
				if v := strings.TrimSpace(gooStr(x)); v != "" {
					options = append(options, v)
				}
			}
		case string:
			for _, v := range strings.Split(o, "|") {
				if v = strings.TrimSpace(v); v != "" {
					options = append(options, v)
				}
			}
		}
		if id := gooInt(m["id"]); id > 0 {
			ga := s.gooAttributeByID(ctx, id)
			if ga == nil {
				return nil, gooErr(400, "woocommerce_rest_invalid_attribute", "Invalid attribute ID.")
			}
			a.ID, a.Taxonomy, a.Name = ga.ID, ga.Taxonomy(), ga.Name
			for _, opt := range options {
				t, err := s.store.TermBySlug(ctx, a.Taxonomy, opt)
				if err != nil {
					t, err = s.store.TermByName(ctx, a.Taxonomy, opt)
				}
				if err != nil {
					t = &store.Term{Taxonomy: a.Taxonomy, Name: opt}
					if err := s.store.InsertTerm(ctx, t); err != nil {
						continue
					}
				}
				a.Options = append(a.Options, t.Slug)
			}
		} else {
			a.Name = strings.TrimSpace(gooStr(m["name"]))
			if a.Name == "" {
				continue
			}
			a.Options = options
		}
		if a.Options == nil {
			a.Options = []string{}
		}
		out = append(out, a)
	}
	return out, nil
}

// gooV3ImageIDs reads images [{id} | {src}]: a src must be a file of this site's media library
// (remote images are not downloaded).
func (s *Server) gooV3ImageIDs(ctx context.Context, list []any) ([]int64, *gooAPIErr) {
	var ids []int64
	for _, x := range list {
		m := gooObj(x)
		if id := gooInt(m["id"]); id > 0 {
			if _, err := s.store.GetAttachment(ctx, id); err != nil {
				return nil, gooErr(400, "woocommerce_product_invalid_image_id", "#"+itoa64(id)+" is an invalid image ID.")
			}
			ids = append(ids, id)
			continue
		}
		src := gooStr(m["src"])
		if src == "" {
			continue
		}
		_, rel, ok := strings.Cut(src, "/wp-content/uploads/")
		var id int64
		if ok {
			_ = s.store.Pool.QueryRow(ctx, `SELECT id FROM media WHERE file_path = $1`, rel).Scan(&id)
		}
		if id == 0 {
			return nil, gooErr(400, "woocommerce_product_image_upload_error", "Error getting remote image "+src+". Upload the image to the media library first.")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// gooV3SaveTerms sets a product's categories / tags after a save; new products get Uncategorized.
func (s *Server) gooV3SaveTerms(ctx context.Context, p *gooProduct, cats, tags []int64, setCats, setTags, isNew bool) {
	if setCats {
		_ = s.store.SetPostTerms(ctx, p.ID, "product_cat", cats)
	} else if isNew && p.Type != gooTypeVariation {
		if t, err := s.store.TermBySlug(ctx, "product_cat", "uncategorized"); err == nil {
			_ = s.store.SetPostTerms(ctx, p.ID, "product_cat", []int64{t.ID})
		}
	}
	if setTags {
		_ = s.store.SetPostTerms(ctx, p.ID, "product_tag", tags)
	}
}

func (s *Server) gooV3ProductCreate(c *gin.Context, b map[string]any) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	typ := gooStr(b["type"])
	if typ == "" {
		typ = gooTypeSimple
	}
	p := gooNewProduct(typ)
	cats, tags, setCats, setTags, aerr := s.gooV3ApplyProduct(ctx, p, b)
	if aerr != nil {
		return nil, aerr
	}
	if strings.TrimSpace(p.Name) == "" && p.Status == "publish" {
		p.Name = "Product"
	}
	if err := s.gooSaveProduct(ctx, p, gooActorID(c)); err != nil {
		return nil, gooErr(400, "woocommerce_rest_cannot_create", err.Error())
	}
	s.gooV3SaveTerms(ctx, p, cats, tags, setCats, setTags, true)
	p, _ = s.gooGetProduct(ctx, p.ID)
	return s.gooV3ProductJSON(ctx, p), nil
}

func (s *Server) gooV3ProductUpdate(c *gin.Context, id int64, b map[string]any) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	p, err := s.gooGetProduct(ctx, id)
	if err != nil || p.Type == gooTypeVariation {
		return nil, gooProductErr()
	}
	cats, tags, setCats, setTags, aerr := s.gooV3ApplyProduct(ctx, p, b)
	if aerr != nil {
		return nil, aerr
	}
	if err := s.gooSaveProduct(ctx, p, gooActorID(c)); err != nil {
		return nil, gooErr(400, "woocommerce_rest_cannot_edit", err.Error())
	}
	s.gooV3SaveTerms(ctx, p, cats, tags, setCats, setTags, false)
	if p.ManageStock && p.StockQuantity != nil {
		s.gooPurgeProduct(ctx, p)
	}
	p, _ = s.gooGetProduct(ctx, p.ID)
	return s.gooV3ProductJSON(ctx, p), nil
}

func (s *Server) gooV3ProductDelete(c *gin.Context, id int64, force bool) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	p, err := s.gooGetProduct(ctx, id)
	if err != nil || p.Type == gooTypeVariation {
		return nil, gooProductErr()
	}
	obj := s.gooV3ProductJSON(ctx, p)
	if !force {
		if p.Status == "trash" {
			return nil, gooErr(http.StatusGone, "woocommerce_rest_already_trashed", "The product has already been deleted.")
		}
		if err := s.store.TrashPost(ctx, id); err != nil {
			return nil, gooErr(500, "woocommerce_rest_cannot_delete", err.Error())
		}
		obj["status"] = "trash"
	} else {
		if p.Type == gooTypeVariable {
			vars, _ := s.gooVariations(ctx, id, true)
			for _, v := range vars {
				_ = s.store.DeletePost(ctx, v.ID)
				_, _ = s.store.Pool.Exec(ctx, `DELETE FROM goo_products WHERE post_id = $1`, v.ID)
			}
		}
		if err := s.store.DeletePost(ctx, id); err != nil {
			return nil, gooErr(500, "woocommerce_rest_cannot_delete", err.Error())
		}
		_, _ = s.store.Pool.Exec(ctx, `DELETE FROM goo_products WHERE post_id = $1`, id)
	}
	s.gooPurgeProduct(ctx, p)
	s.gooWebhookFire(ctx, "product.deleted", id, map[string]any{"id": id})
	return obj, nil
}

// gooV3ProductDuplicate is POST /products/{id}/duplicate (WC_Admin_Duplicate_Product).
func (s *Server) gooV3ProductDuplicate(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	p, err := s.gooGetProduct(ctx, gooInt(req.Params["product_id"]))
	if err != nil || p.Type == gooTypeVariation {
		gooFail(c, gooProductErr())
		return
	}
	vars, _ := s.gooVariations(ctx, p.ID, true)
	cp := *p
	post := *p.Post
	post.ID, post.Slug, post.Status = 0, "", "draft"
	cp.Post, cp.ID, cp.Slug, cp.Status, cp.Name, cp.TotalSales = &post, 0, "", "draft", p.Name+" (Copy)", 0
	if cp.SKU != "" {
		cp.SKU += "-1"
	}
	if err := s.gooSaveProduct(ctx, &cp, gooActorID(c)); err != nil {
		gooFail(c, gooErr(400, "woocommerce_rest_cannot_create", err.Error()))
		return
	}
	cats, _ := s.store.TermsForPosts(ctx, []int64{p.ID}, "")
	byTax := map[string][]int64{}
	for _, t := range cats[p.ID] {
		byTax[t.Taxonomy] = append(byTax[t.Taxonomy], t.ID)
	}
	for tax, ids := range byTax {
		_ = s.store.SetPostTerms(ctx, cp.ID, tax, ids)
	}
	for _, v := range vars {
		nv := *v
		vp := *v.Post
		vp.ID, vp.Slug = 0, ""
		nv.Post, nv.ID, nv.ParentID, nv.Slug = &vp, 0, cp.ID, ""
		if nv.SKU != "" {
			nv.SKU += "-1"
		}
		_ = s.gooSaveProduct(ctx, &nv, gooActorID(c))
	}
	np, _ := s.gooGetProduct(ctx, cp.ID)
	restJSON(c, http.StatusCreated, s.gooV3ProductJSON(ctx, np))
}

// gooV3ProductsList is GET /products with WooCommerce's filters.
func (s *Server) gooV3ProductsList(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	page, per, offset := gooPage(c)
	q := gooProductQuery{Page: page, PerPage: per, Search: strings.TrimSpace(c.Query("search")), SKU: c.Query("sku")}
	switch st := c.DefaultQuery("status", "any"); st {
	case "any", "":
		q.Status = gooProductStatuses
	default:
		q.Status = strings.Split(st, ",")
	}
	if t := c.Query("type"); t != "" {
		q.Types = []string{t}
	}
	q.IDs, q.Exclude = gooQueryIDs(c, "include"), gooQueryIDs(c, "exclude")
	if cat := c.Query("category"); cat != "" {
		for _, id := range gooIDList(cat) {
			q.Category = append(q.Category, id)
		}
	}
	if tag := c.Query("tag"); tag != "" {
		q.Tag = gooIDList(tag)
	}
	if v, ok := gooQueryBool(c, "featured"); ok && v {
		q.Featured = true
	}
	if v, ok := gooQueryBool(c, "on_sale"); ok && v {
		q.OnSale = true
	}
	if v := c.Query("min_price"); v != "" {
		q.MinPrice = s.gooParseAmount(v)
	}
	if v := c.Query("max_price"); v != "" {
		q.MaxPrice = s.gooParseAmount(v)
	}
	if v := c.Query("stock_status"); v != "" {
		q.StockStatus = v
	}
	if a, t := c.Query("attribute"), c.Query("attribute_term"); a != "" && t != "" {
		q.AttrTerms = map[string][]int64{a: gooIDList(t)}
	}
	// Filters the catalog query has no field for narrow the id list.
	extra := []string{}
	args := []any{}
	add := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if v := c.Query("slug"); v != "" {
		extra = append(extra, "po.slug = ANY("+add(strings.Split(v, ","))+")")
	}
	for _, k := range []string{"after", "before", "modified_after", "modified_before"} {
		if v := c.Query(k); v != "" {
			t, ok := s.gooParseDate(v, gooBool(c.Query("dates_are_gmt")))
			if !ok || t == nil {
				continue
			}
			col, op := "coalesce(po.published_at, po.created_at)", ">"
			if strings.HasPrefix(k, "modified") {
				col = "po.modified_at"
			}
			if strings.HasSuffix(k, "before") {
				op = "<"
			}
			extra = append(extra, col+" "+op+" "+add(*t))
		}
	}
	if ids := gooQueryIDs(c, "parent"); len(ids) > 0 {
		extra = append(extra, "coalesce(po.parent_id,0) = ANY("+add(ids)+")")
	}
	if ids := gooQueryIDs(c, "parent_exclude"); len(ids) > 0 {
		extra = append(extra, "NOT coalesce(po.parent_id,0) = ANY("+add(ids)+")")
	}
	if v := c.Query("shipping_class"); v != "" {
		extra = append(extra, "gp.shipping_class_id = ANY("+add(gooIDList(v))+")")
	}
	if v := c.Query("tax_class"); v != "" {
		if v == "standard" {
			v = ""
		}
		extra = append(extra, "gp.tax_class = "+add(v))
	}
	if len(extra) > 0 {
		rows, err := s.store.Pool.Query(ctx, `SELECT po.id FROM posts po LEFT JOIN goo_products gp ON gp.post_id = po.id WHERE po.type = 'product' AND `+strings.Join(extra, " AND "), args...)
		if err == nil {
			ids := []int64{-1}
			for rows.Next() {
				var id int64
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
			if len(q.IDs) > 0 {
				var both []int64
				for _, id := range q.IDs {
					if gooInt64In(id, ids) {
						both = append(both, id)
					}
				}
				ids = append(both, -1)
			}
			q.IDs = ids
		}
	}
	switch ob := c.DefaultQuery("orderby", "date"); ob {
	case "price":
		q.OrderBy = "price"
		if strings.EqualFold(c.Query("order"), "desc") {
			q.OrderBy = "price-desc"
		}
	case "id", "include", "title", "popularity", "rating", "menu_order", "date":
		q.OrderBy = ob
	default:
		q.OrderBy = "date"
	}
	q.Order = c.DefaultQuery("order", "desc")
	if offset >= 0 {
		// An explicit offset: fetch from the start and slice.
		q.Page, q.PerPage = 1, offset+per
	}
	list, total, err := s.gooQueryProducts(ctx, q)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "woocommerce_rest_cannot_view", err.Error())
		return
	}
	if offset >= 0 {
		if offset < len(list) {
			list = list[offset:]
		} else {
			list = nil
		}
	}
	paginate(c, total, per, page)
	out := make([]any, 0, len(list))
	for _, p := range list {
		out = append(out, s.gooV3ProductJSON(ctx, p))
	}
	restJSON(c, http.StatusOK, out)
}

// Variations --------------------------------------------------------------------------------------------

func (s *Server) gooV3Variation(ctx context.Context, parentID, id int64) (*gooProduct, *gooAPIErr) {
	v, err := s.gooGetProduct(ctx, id)
	if err != nil || v.Type != gooTypeVariation || v.ParentID != parentID {
		return nil, gooErr(http.StatusNotFound, "woocommerce_rest_product_variation_invalid_id", "Invalid ID.")
	}
	return v, nil
}

// gooV3VariationJSON is a variation as WC_REST_Product_Variations_Controller returns it.
func (s *Server) gooV3VariationJSON(ctx context.Context, v *gooProduct) map[string]any {
	out := s.gooV3ProductJSON(ctx, v)
	out["description"] = v.Description
	parent := s.gooParent(ctx, v)
	attrs := []any{}
	if parent != nil {
		for _, a := range parent.Attributes {
			if !a.Variation {
				continue
			}
			if val, ok := v.VariationAttrs[a.Key()]; ok && val != "" {
				attrs = append(attrs, map[string]any{"id": a.ID, "name": s.gooAttrLabel(ctx, a), "option": s.gooAttrValueLabel(ctx, a, val)})
			}
		}
	}
	out["attributes"] = attrs
	var img any
	if imgs := s.gooV3Images(ctx, []int64{v.ImageID}); v.ImageID != 0 && len(imgs) > 0 {
		img = imgs[0]
	}
	out["image"] = img
	for _, k := range []string{"images", "categories", "tags", "brands", "default_attributes", "variations", "grouped_products", "related_ids",
		"upsell_ids", "cross_sell_ids", "featured", "catalog_visibility", "short_description", "reviews_allowed", "average_rating", "rating_count",
		"external_url", "button_text", "total_sales", "type", "slug", "has_options", "post_password", "sold_individually", "purchase_note"} {
		delete(out, k)
	}
	out["status"] = v.Status
	out["_links"] = restLinks(s.gooRESTURL(ctx, gooV3NS+"/products/"+itoa64(v.ParentID)+"/variations/"+itoa64(v.ID)),
		s.gooRESTURL(ctx, gooV3NS+"/products/"+itoa64(v.ParentID)+"/variations"), map[string]any{"up": []map[string]any{{"href": s.gooRESTURL(ctx, gooV3NS+"/products/"+itoa64(v.ParentID))}}})
	return out
}

func (s *Server) gooV3VariationsList(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	pid := gooInt(req.Params["product_id"])
	parent, err := s.gooGetProduct(ctx, pid)
	if err != nil || parent.Type == gooTypeVariation {
		gooFail(c, gooErr(http.StatusNotFound, "woocommerce_rest_product_invalid_id", "Invalid ID."))
		return
	}
	vars, _ := s.gooVariations(ctx, pid, true)
	if inc := gooQueryIDs(c, "include"); len(inc) > 0 {
		var keep []*gooProduct
		for _, v := range vars {
			if gooInt64In(v.ID, inc) {
				keep = append(keep, v)
			}
		}
		vars = keep
	}
	if st := c.Query("stock_status"); st != "" {
		var keep []*gooProduct
		for _, v := range vars {
			if v.StockStatus == st {
				keep = append(keep, v)
			}
		}
		vars = keep
	}
	if strings.EqualFold(c.Query("order"), "desc") {
		for i, j := 0, len(vars)-1; i < j; i, j = i+1, j-1 {
			vars[i], vars[j] = vars[j], vars[i]
		}
	}
	page := gooPaginateSlice(c, vars)
	out := make([]any, 0, len(page))
	for _, v := range page {
		v.parent = parent
		out = append(out, s.gooV3VariationJSON(ctx, v))
	}
	restJSON(c, http.StatusOK, out)
}

func (s *Server) gooV3VariationCreate(c *gin.Context, parentID int64, b map[string]any) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	parent, err := s.gooGetProduct(ctx, parentID)
	if err != nil || parent.Type == gooTypeVariation {
		return nil, gooErr(http.StatusNotFound, "woocommerce_rest_product_invalid_id", "Invalid ID.")
	}
	v := gooNewProduct(gooTypeVariation)
	v.ParentID, v.parent = parentID, parent
	v.TaxClass = "parent"
	if _, ok := b["status"]; !ok {
		v.Status = "publish"
	}
	if _, _, _, _, aerr := s.gooV3ApplyProduct(ctx, v, b); aerr != nil {
		return nil, aerr
	}
	if st, ok := b["status"]; ok && gooStr(st) != "publish" {
		v.Status = "private"
	}
	if v.Name == "" {
		v.Name = parent.Name
	}
	if err := s.gooSaveProduct(ctx, v, gooActorID(c)); err != nil {
		return nil, gooErr(400, "woocommerce_rest_cannot_create", err.Error())
	}
	v, _ = s.gooGetProduct(ctx, v.ID)
	return s.gooV3VariationJSON(ctx, v), nil
}

func (s *Server) gooV3VariationUpdate(c *gin.Context, parentID, id int64, b map[string]any) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	v, aerr := s.gooV3Variation(ctx, parentID, id)
	if aerr != nil {
		return nil, aerr
	}
	if _, _, _, _, aerr := s.gooV3ApplyProduct(ctx, v, b); aerr != nil {
		return nil, aerr
	}
	if st, ok := b["status"]; ok && gooStr(st) != "publish" {
		v.Status = "private"
	}
	if err := s.gooSaveProduct(ctx, v, gooActorID(c)); err != nil {
		return nil, gooErr(400, "woocommerce_rest_cannot_edit", err.Error())
	}
	v, _ = s.gooGetProduct(ctx, v.ID)
	return s.gooV3VariationJSON(ctx, v), nil
}

func (s *Server) gooV3VariationDelete(c *gin.Context, parentID, id int64, force bool) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	v, aerr := s.gooV3Variation(ctx, parentID, id)
	if aerr != nil {
		return nil, aerr
	}
	if !force {
		return nil, gooErr(http.StatusNotImplemented, "woocommerce_rest_trash_not_supported", "Resource does not support trashing.")
	}
	obj := s.gooV3VariationJSON(ctx, v)
	if err := s.store.DeletePost(ctx, id); err != nil {
		return nil, gooErr(500, "woocommerce_rest_cannot_delete", err.Error())
	}
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM goo_products WHERE post_id = $1`, id)
	if parent, err := s.gooGetProduct(ctx, parentID); err == nil {
		_ = s.gooSyncVariable(ctx, parent)
		s.gooPurgeProduct(ctx, parent)
	}
	s.gooWebhookFire(ctx, "product.deleted", id, map[string]any{"id": id})
	return obj, nil
}

// Attributes ---------------------------------------------------------------------------------------------

func (s *Server) gooV3AttributeJSON(ctx context.Context, a *gooAttribute) map[string]any {
	return map[string]any{"id": a.ID, "name": a.Name, "slug": a.Taxonomy(), "type": a.Type, "order_by": a.OrderBy, "has_archives": a.HasArchives,
		"_links": s.gooLinks(ctx, "products/attributes", a.ID)}
}

func (s *Server) gooV3AttributesList(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	list, _ := s.gooAttributes(ctx)
	out := []any{}
	for _, a := range list {
		out = append(out, s.gooV3AttributeJSON(ctx, a))
	}
	restJSON(c, http.StatusOK, out)
}

func (s *Server) gooV3ApplyAttribute(a *gooAttribute, b map[string]any) {
	if v, ok := b["name"]; ok {
		a.Name = gooStr(v)
	}
	if v, ok := b["slug"]; ok {
		a.Slug = strings.TrimPrefix(gooStr(v), "pa_")
	}
	if v, ok := b["type"]; ok {
		a.Type = gooStr(v)
	}
	if v, ok := b["order_by"]; ok {
		a.OrderBy = gooStr(v)
	}
	if v, ok := b["has_archives"]; ok {
		a.HasArchives = gooBool(v)
	}
}

func (s *Server) gooV3AttributeCreate(c *gin.Context, b map[string]any) (map[string]any, *gooAPIErr) {
	a := &gooAttribute{}
	s.gooV3ApplyAttribute(a, b)
	if strings.TrimSpace(a.Name) == "" {
		return nil, gooErr(400, "rest_missing_callback_param", "Missing parameter(s): name")
	}
	if err := s.gooSaveAttribute(c.Request.Context(), a); err != nil {
		return nil, gooErr(400, "woocommerce_rest_cannot_create", err.Error())
	}
	return s.gooV3AttributeJSON(c.Request.Context(), a), nil
}

func (s *Server) gooV3AttributeUpdate(c *gin.Context, id int64, b map[string]any) (map[string]any, *gooAPIErr) {
	a := s.gooAttributeByID(c.Request.Context(), id)
	if a == nil {
		return nil, gooErr(404, "woocommerce_rest_attribute_invalid", "Resource does not exist.")
	}
	s.gooV3ApplyAttribute(a, b)
	if err := s.gooSaveAttribute(c.Request.Context(), a); err != nil {
		return nil, gooErr(400, "woocommerce_rest_cannot_edit", err.Error())
	}
	return s.gooV3AttributeJSON(c.Request.Context(), a), nil
}

func (s *Server) gooV3AttributeDelete(c *gin.Context, id int64, force bool) (map[string]any, *gooAPIErr) {
	if !force {
		return nil, gooErr(http.StatusNotImplemented, "woocommerce_rest_trash_not_supported", "Resource does not support trashing.")
	}
	a := s.gooAttributeByID(c.Request.Context(), id)
	if a == nil {
		return nil, gooErr(404, "woocommerce_rest_attribute_invalid", "Resource does not exist.")
	}
	obj := s.gooV3AttributeJSON(c.Request.Context(), a)
	if err := s.gooDeleteAttribute(c.Request.Context(), id); err != nil {
		return nil, gooErr(500, "woocommerce_rest_cannot_delete", err.Error())
	}
	return obj, nil
}

// Terms (categories, tags, shipping classes, attribute terms) -----------------------------------------------

// gooTermRoutes registers the CRUD + batch routes of a product taxonomy.
func (s *Server) gooTermRoutes(base string, caps gooCaps, tax func(*gin.Context, *restRequest) (string, *gooAPIErr)) {
	withTax := func(fn func(c *gin.Context, req *restRequest, tax string)) restHandler {
		return func(c *gin.Context, req *restRequest) {
			t, err := tax(c, req)
			if err != nil {
				gooFail(c, err)
				return
			}
			fn(c, req, t)
		}
	}
	s.gooV3(base, caps, map[string]restHandler{
		http.MethodGet: withTax(s.gooV3TermsList),
		http.MethodPost: withTax(func(c *gin.Context, req *restRequest, tax string) {
			obj, err := s.gooV3TermCreate(c, tax, gooFormBody(c, req))
			if err != nil {
				gooFail(c, err)
				return
			}
			restJSON(c, http.StatusCreated, obj)
		}),
	})
	s.gooV3(base+"/batch", caps, map[string]restHandler{http.MethodPost: withTax(func(c *gin.Context, req *restRequest, tax string) {
		s.gooBatch(c, req, gooBatchOps{
			Create: func(c *gin.Context, b map[string]any) (map[string]any, *gooAPIErr) {
				return s.gooV3TermCreate(c, tax, b)
			},
			Update: func(c *gin.Context, id int64, b map[string]any) (map[string]any, *gooAPIErr) {
				return s.gooV3TermUpdate(c, tax, id, b)
			},
			Delete: func(c *gin.Context, id int64, force bool) (map[string]any, *gooAPIErr) {
				return s.gooV3TermDelete(c, tax, id, force)
			},
		})
	})})
	s.gooV3(base+"/(?P<id>[\\d]+)", caps, map[string]restHandler{
		http.MethodGet: withTax(func(c *gin.Context, req *restRequest, tax string) {
			t, err := s.store.GetTerm(c.Request.Context(), req.ID())
			if err != nil || t.Taxonomy != tax {
				gooFail(c, gooErr(404, "woocommerce_rest_term_invalid", "Resource does not exist."))
				return
			}
			restJSON(c, http.StatusOK, s.gooV3TermJSON(c.Request.Context(), t))
		}),
		http.MethodPut: withTax(func(c *gin.Context, req *restRequest, tax string) {
			obj, err := s.gooV3TermUpdate(c, tax, req.ID(), gooFormBody(c, req))
			if err != nil {
				gooFail(c, err)
				return
			}
			restJSON(c, http.StatusOK, obj)
		}),
		http.MethodDelete: withTax(func(c *gin.Context, req *restRequest, tax string) {
			force, _ := gooQueryBool(c, "force")
			if v, ok := req.Body["force"]; ok {
				force = gooBool(v)
			}
			obj, err := s.gooV3TermDelete(c, tax, req.ID(), force)
			if err != nil {
				gooFail(c, err)
				return
			}
			restJSON(c, http.StatusOK, obj)
		}),
	})
}

// gooV3TermJSON is a product term in WooCommerce's shape for its taxonomy.
func (s *Server) gooV3TermJSON(ctx context.Context, t *store.Term) map[string]any {
	out := map[string]any{"id": t.ID, "name": t.Name, "slug": t.Slug, "description": t.Description, "count": t.Count}
	coll := map[string]string{"product_cat": "products/categories", "product_tag": "products/tags", "product_shipping_class": "products/shipping_classes"}[t.Taxonomy]
	switch {
	case t.Taxonomy == "product_cat":
		out["parent"] = t.ParentID
		out["display"] = defaultStr(gooStr(t.Meta["display_type"]), "default")
		out["menu_order"] = gooInt(t.Meta["order"])
		var img any
		if id := gooInt(t.Meta["thumbnail_id"]); id > 0 {
			if imgs := s.gooV3Images(ctx, []int64{id}); len(imgs) > 0 {
				img = imgs[0]
			}
		}
		out["image"] = img
	case strings.HasPrefix(t.Taxonomy, "pa_"):
		out["menu_order"] = gooInt(t.Meta["order"])
		if a := s.gooAttributeByTaxonomy(ctx, t.Taxonomy); a != nil {
			coll = "products/attributes/" + itoa64(a.ID) + "/terms"
		}
	}
	if coll != "" {
		out["_links"] = s.gooLinks(ctx, coll, t.ID)
	}
	return out
}

func (s *Server) gooV3TermsList(c *gin.Context, req *restRequest, tax string) {
	ctx := c.Request.Context()
	hide, _ := gooQueryBool(c, "hide_empty")
	orderBy := c.DefaultQuery("orderby", "name")
	q := store.TermQuery{Taxonomy: tax, Search: c.Query("search"), HideEmpty: hide, OrderBy: orderBy, Order: c.DefaultQuery("order", "asc"), IDs: gooQueryIDs(c, "include")}
	terms, _, err := s.store.QueryTerms(ctx, q)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "woocommerce_rest_cannot_view", err.Error())
		return
	}
	if strings.HasPrefix(tax, "pa_") && orderBy == "menu_order" || tax == "product_cat" && orderBy == "menu_order" {
		if strings.HasPrefix(tax, "pa_") {
			terms, _ = s.gooAttributeTerms(ctx, tax)
		}
	}
	exclude := gooQueryIDs(c, "exclude")
	parent, hasParent := c.GetQuery("parent")
	slug := c.Query("slug")
	product := gooInt(c.Query("product"))
	var productTerms []int64
	if product > 0 {
		pt, _ := s.store.TermsForPosts(ctx, []int64{product}, tax)
		for _, t := range pt[product] {
			productTerms = append(productTerms, t.ID)
		}
	}
	var keep []*store.Term
	for _, t := range terms {
		if gooInt64In(t.ID, exclude) || hasParent && t.ParentID != gooInt(parent) || slug != "" && t.Slug != slug || product > 0 && !gooInt64In(t.ID, productTerms) {
			continue
		}
		keep = append(keep, t)
	}
	page := gooPaginateSlice(c, keep)
	out := make([]any, 0, len(page))
	for _, t := range page {
		out = append(out, s.gooV3TermJSON(ctx, t))
	}
	restJSON(c, http.StatusOK, out)
}

func (s *Server) gooV3ApplyTerm(ctx context.Context, t *store.Term, b map[string]any) *gooAPIErr {
	if v, ok := b["name"]; ok {
		t.Name = strings.TrimSpace(gooStr(v))
	}
	if v, ok := b["slug"]; ok {
		t.Slug = gooStr(v)
	}
	if v, ok := b["description"]; ok {
		t.Description = gooStr(v)
	}
	if t.Meta == nil {
		t.Meta = map[string]any{}
	}
	if v, ok := b["parent"]; ok && t.Taxonomy == "product_cat" {
		pid := gooInt(v)
		if pid != 0 {
			if pt, err := s.store.GetTerm(ctx, pid); err != nil || pt.Taxonomy != t.Taxonomy {
				return gooErr(400, "woocommerce_rest_term_invalid", "Parent resource does not exist.")
			}
		}
		t.ParentID = pid
	}
	if v, ok := b["display"]; ok && t.Taxonomy == "product_cat" {
		t.Meta["display_type"] = gooStr(v)
	}
	if v, ok := b["menu_order"]; ok {
		t.Meta["order"] = float64(gooInt(v))
	}
	if v, ok := b["image"]; ok && t.Taxonomy == "product_cat" {
		delete(t.Meta, "thumbnail_id")
		if m := gooObj(v); m != nil {
			ids, err := s.gooV3ImageIDs(ctx, []any{m})
			if err != nil {
				return err
			}
			if len(ids) > 0 {
				t.Meta["thumbnail_id"] = float64(ids[0])
			}
		}
	}
	return nil
}

func (s *Server) gooV3TermCreate(c *gin.Context, tax string, b map[string]any) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	t := &store.Term{Taxonomy: tax, Meta: map[string]any{}}
	if err := s.gooV3ApplyTerm(ctx, t, b); err != nil {
		return nil, err
	}
	if t.Name == "" {
		return nil, gooErr(400, "rest_missing_callback_param", "Missing parameter(s): name")
	}
	if err := s.store.InsertTerm(ctx, t); err != nil {
		if errors.Is(err, store.ErrTermExists) {
			return nil, gooErr(400, "term_exists", err.Error())
		}
		return nil, gooErr(500, "woocommerce_rest_cannot_create", err.Error())
	}
	if len(t.Meta) > 0 {
		_ = s.store.UpdateTerm(ctx, t)
	}
	t, _ = s.store.GetTerm(ctx, t.ID)
	return s.gooV3TermJSON(ctx, t), nil
}

func (s *Server) gooV3TermUpdate(c *gin.Context, tax string, id int64, b map[string]any) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	t, err := s.store.GetTerm(ctx, id)
	if err != nil || t.Taxonomy != tax {
		return nil, gooErr(404, "woocommerce_rest_term_invalid", "Resource does not exist.")
	}
	if aerr := s.gooV3ApplyTerm(ctx, t, b); aerr != nil {
		return nil, aerr
	}
	if err := s.store.UpdateTerm(ctx, t); err != nil {
		return nil, gooErr(500, "woocommerce_rest_cannot_edit", err.Error())
	}
	t, _ = s.store.GetTerm(ctx, t.ID)
	return s.gooV3TermJSON(ctx, t), nil
}

func (s *Server) gooV3TermDelete(c *gin.Context, tax string, id int64, force bool) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	if !force {
		return nil, gooErr(http.StatusNotImplemented, "woocommerce_rest_trash_not_supported", "Resource does not support trashing.")
	}
	t, err := s.store.GetTerm(ctx, id)
	if err != nil || t.Taxonomy != tax {
		return nil, gooErr(404, "woocommerce_rest_term_invalid", "Resource does not exist.")
	}
	obj := s.gooV3TermJSON(ctx, t)
	if err := s.store.DeleteTerm(ctx, t, 0); err != nil {
		return nil, gooErr(500, "woocommerce_rest_cannot_delete", err.Error())
	}
	if tax == "product_shipping_class" {
		_, _ = s.store.Pool.Exec(ctx, `UPDATE goo_products SET shipping_class_id = NULL WHERE shipping_class_id = $1`, id)
	}
	return obj, nil
}

// Reviews ----------------------------------------------------------------------------------------------

// gooReviewStatus maps WooCommerce's review statuses to comment statuses and back.
func gooReviewStatusIn(st string) string {
	switch st {
	case "hold", "pending":
		return "pending"
	case "approved", "spam", "trash":
		return st
	}
	return ""
}

func gooReviewStatusOut(st string) string {
	if st == "pending" {
		return "hold"
	}
	return st
}

func (s *Server) gooReviewByID(ctx context.Context, id int64) (*gooReview, *gooAPIErr) {
	cm, err := s.store.GetComment(ctx, id)
	if err != nil || (cm.Type != "review" && cm.Type != "comment") {
		return nil, gooErr(404, "woocommerce_rest_review_invalid_id", "Invalid review ID.")
	}
	post, err := s.store.GetPost(ctx, cm.PostID)
	if err != nil || post.Type != "product" {
		return nil, gooErr(404, "woocommerce_rest_review_invalid_id", "Invalid review ID.")
	}
	return &gooReview{Comment: cm, ProductName: post.Title, ProductSlug: post.Slug}, nil
}

func (s *Server) gooV3ReviewJSON(ctx context.Context, r *gooReview) map[string]any {
	return map[string]any{"id": r.ID, "date_created": s.gooDate(&r.CreatedAt), "date_created_gmt": gooDateGMT(&r.CreatedAt),
		"product_id": r.PostID, "product_name": r.ProductName, "product_permalink": s.links(ctx).HomeURL("/" + s.gooProductBase() + "/" + r.ProductSlug + "/"),
		"status": gooReviewStatusOut(r.Status), "reviewer": r.AuthorName, "reviewer_email": r.AuthorEmail, "review": r.Content,
		"rating": gooInt(r.Meta["rating"]), "verified": gooBool(r.Meta["verified"]), "reviewer_avatar_urls": s.gooAvatarURLs(ctx, r.AuthorEmail),
		"_links": s.gooLinks(ctx, "products/reviews", r.ID)}
}

func (s *Server) gooV3ReviewsList(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	page, per, _ := gooPage(c)
	statuses := []string{"approved"}
	if st := c.DefaultQuery("status", "approved"); st == "all" {
		statuses = []string{"all"}
	} else if v := gooReviewStatusIn(st); v != "" {
		statuses = []string{v}
	}
	list, total, err := s.gooQueryReviews(ctx, statuses, gooQueryIDs(c, "product"), nil, gooQueryIDs(c, "include"), gooQueryIDs(c, "exclude"),
		c.Query("search"), c.DefaultQuery("orderby", "date_gmt"), c.DefaultQuery("order", "desc"), page, per)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "woocommerce_rest_cannot_view", err.Error())
		return
	}
	paginate(c, total, per, page)
	out := make([]any, 0, len(list))
	for _, r := range list {
		out = append(out, s.gooV3ReviewJSON(ctx, r))
	}
	restJSON(c, http.StatusOK, out)
}

func (s *Server) gooV3ApplyReview(ctx context.Context, cm *store.Comment, b map[string]any) *gooAPIErr {
	if v, ok := b["product_id"]; ok {
		post, err := s.store.GetPost(ctx, gooInt(v))
		if err != nil || post.Type != "product" {
			return gooErr(404, "woocommerce_rest_product_invalid_id", "Invalid product ID.")
		}
		cm.PostID = post.ID
	}
	if v, ok := b["review"]; ok {
		cm.Content = gooStr(v)
	}
	if v, ok := b["reviewer"]; ok {
		cm.AuthorName = gooStr(v)
	}
	if v, ok := b["reviewer_email"]; ok {
		cm.AuthorEmail = gooStr(v)
	}
	if v, ok := b["rating"]; ok {
		r := gooInt(v)
		if r < 0 || r > 5 {
			return gooErr(400, "rest_invalid_param", "Invalid parameter(s): rating")
		}
		cm.Meta["rating"] = float64(r)
	}
	if v, ok := b["verified"]; ok {
		cm.Meta["verified"] = gooBool(v)
	}
	if v, ok := b["status"]; ok {
		if st := gooReviewStatusIn(gooStr(v)); st != "" {
			cm.Status = st
		}
	}
	return nil
}

func (s *Server) gooV3ReviewCreate(c *gin.Context, b map[string]any) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	cm := &store.Comment{Type: "review", Status: "approved", Meta: map[string]any{}, AuthorIP: clientIP(c), UserAgent: c.Request.UserAgent()}
	if _, ok := b["product_id"]; !ok {
		return nil, gooErr(400, "rest_missing_callback_param", "Missing parameter(s): product_id")
	}
	if err := s.gooV3ApplyReview(ctx, cm, b); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cm.Content) == "" {
		return nil, gooErr(400, "woocommerce_rest_review_content_invalid", "Invalid review content.")
	}
	if cm.AuthorName == "" || cm.AuthorEmail == "" {
		return nil, gooErr(400, "woocommerce_rest_review_invalid_author", "Invalid reviewer data.")
	}
	if u, err := s.store.UserByLoginOrEmail(ctx, cm.AuthorEmail); err == nil {
		cm.UserID = u.ID
	}
	if _, ok := b["verified"]; !ok {
		cm.Meta["verified"] = s.gooIsVerifiedOwner(ctx, cm.AuthorEmail, cm.UserID, cm.PostID)
	}
	if err := s.store.InsertComment(ctx, cm); err != nil {
		return nil, gooErr(500, "woocommerce_rest_cannot_create", err.Error())
	}
	_ = s.store.RecountComments(ctx, cm.PostID)
	s.gooUpdateRating(ctx, cm.PostID)
	r, aerr := s.gooReviewByID(ctx, cm.ID)
	if aerr != nil {
		return nil, aerr
	}
	return s.gooV3ReviewJSON(ctx, r), nil
}

func (s *Server) gooV3ReviewUpdate(c *gin.Context, id int64, b map[string]any) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	r, aerr := s.gooReviewByID(ctx, id)
	if aerr != nil {
		return nil, aerr
	}
	if r.Meta == nil {
		r.Meta = map[string]any{}
	}
	if err := s.gooV3ApplyReview(ctx, r.Comment, b); err != nil {
		return nil, err
	}
	if err := s.store.UpdateComment(ctx, r.Comment); err != nil {
		return nil, gooErr(500, "woocommerce_rest_cannot_edit", err.Error())
	}
	if _, ok := b["status"]; ok {
		_ = s.store.SetCommentStatus(ctx, id, r.Status)
	}
	s.gooUpdateRating(ctx, r.PostID)
	r, aerr = s.gooReviewByID(ctx, id)
	if aerr != nil {
		return nil, aerr
	}
	return s.gooV3ReviewJSON(ctx, r), nil
}

func (s *Server) gooV3ReviewDelete(c *gin.Context, id int64, force bool) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	r, aerr := s.gooReviewByID(ctx, id)
	if aerr != nil {
		return nil, aerr
	}
	obj := s.gooV3ReviewJSON(ctx, r)
	if force {
		if err := s.store.DeleteComment(ctx, id); err != nil {
			return nil, gooErr(500, "woocommerce_rest_cannot_delete", err.Error())
		}
		_ = s.store.RecountComments(ctx, r.PostID)
		s.gooUpdateRating(ctx, r.PostID)
		return map[string]any{"deleted": true, "previous": obj}, nil
	}
	if r.Status == "trash" {
		return nil, gooErr(http.StatusGone, "woocommerce_rest_already_trashed", "The object has already been trashed.")
	}
	_ = s.store.SetCommentStatus(ctx, id, "trash")
	s.gooUpdateRating(ctx, r.PostID)
	r, _ = s.gooReviewByID(ctx, id)
	return s.gooV3ReviewJSON(ctx, r), nil
}
