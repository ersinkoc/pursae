package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Catalog queries (WC_Product_Query / the shop's main query): visibility, categories, tags,
// attributes (layered nav), price range, on-sale / featured / in-stock filters and the catalog orderings.

type gooProductQuery struct {
	Types       []string // product types; nil = all (never variations unless asked)
	Status      []string // nil = publish
	IDs         []int64
	Exclude     []int64
	ParentID    *int64  // variations of a product
	Category    []int64 // product_cat term ids (children included by the caller)
	Tag         []int64
	AttrTerms   map[string][]int64 // pa_color → term ids (AND across attributes, OR within)
	Search      string
	SKU         string
	MinPrice    *float64
	MaxPrice    *float64
	OnSale      bool
	Featured    bool
	InStock     bool
	StockStatus string
	Visibility  string // catalog (shop, archives), search, "" (any)
	OrderBy     string // menu_order, popularity, rating, date, price, price-desc, title, id, rand
	Order       string
	Page        int
	PerPage     int // -1 = all
}

// gooQueryProducts runs a catalog query and returns the products in order with the total count.
func (s *Server) gooQueryProducts(ctx context.Context, q gooProductQuery) ([]*gooProduct, int, error) {
	args := []any{}
	add := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	conds := []string{}
	postTypes := []string{"product"}
	if containsStr(q.Types, gooTypeVariation) {
		postTypes = []string{"product", "product_variation"}
		if len(q.Types) == 1 {
			postTypes = []string{"product_variation"}
		}
	}
	conds = append(conds, "po.type = ANY("+add(postTypes)+")")
	if len(q.Status) > 0 {
		conds = append(conds, "po.status = ANY("+add(q.Status)+")")
	} else {
		conds = append(conds, "po.status = 'publish'")
	}
	if len(q.Types) > 0 {
		conds = append(conds, "coalesce(p.type, 'simple') = ANY("+add(q.Types)+")")
	}
	if len(q.IDs) > 0 {
		conds = append(conds, "po.id = ANY("+add(q.IDs)+")")
	}
	if q.Visibility != "" { // catalog listings: products hidden from this visitor (Memberships)
		q.Exclude = append(q.Exclude, s.gooListingExclude(ctx, "product")...)
	}
	if len(q.Exclude) > 0 {
		conds = append(conds, "NOT po.id = ANY("+add(q.Exclude)+")")
	}
	if q.ParentID != nil {
		conds = append(conds, "coalesce(po.parent_id,0) = "+add(*q.ParentID))
	}
	if len(q.Category) > 0 {
		conds = append(conds, "po.id IN (SELECT post_id FROM post_terms WHERE term_id = ANY("+add(q.Category)+"))")
	}
	if len(q.Tag) > 0 {
		conds = append(conds, "po.id IN (SELECT post_id FROM post_terms WHERE term_id = ANY("+add(q.Tag)+"))")
	}
	for _, ids := range q.AttrTerms {
		if len(ids) > 0 {
			conds = append(conds, "po.id IN (SELECT post_id FROM post_terms WHERE term_id = ANY("+add(ids)+"))")
		}
	}
	if q.Search != "" {
		ph := add(q.Search)
		like := add("%" + q.Search + "%")
		conds = append(conds, "(po.search @@ plainto_tsquery('simple', "+ph+") OR po.title ILIKE "+like+" OR p.sku ILIKE "+like+
			" OR po.id IN (SELECT parent_id FROM posts v JOIN goo_products vp ON vp.post_id = v.id WHERE v.type = 'product_variation' AND vp.sku ILIKE "+like+"))")
	}
	if q.SKU != "" {
		conds = append(conds, "p.sku = "+add(q.SKU))
	}
	if q.MinPrice != nil {
		conds = append(conds, "coalesce(p.max_price, p.price) >= "+add(*q.MinPrice))
	}
	if q.MaxPrice != nil {
		conds = append(conds, "coalesce(p.min_price, p.price) <= "+add(*q.MaxPrice))
	}
	if q.OnSale {
		conds = append(conds, `(p.sale_price IS NOT NULL AND (p.sale_from IS NULL OR p.sale_from <= now()) AND (p.sale_to IS NULL OR p.sale_to >= now())
			OR po.id IN (SELECT v.parent_id FROM posts v JOIN goo_products vp ON vp.post_id = v.id WHERE v.type = 'product_variation' AND v.status = 'publish'
			AND vp.sale_price IS NOT NULL AND (vp.sale_from IS NULL OR vp.sale_from <= now()) AND (vp.sale_to IS NULL OR vp.sale_to >= now())))`)
	}
	if q.Featured {
		conds = append(conds, "p.featured")
	}
	if q.InStock {
		conds = append(conds, "coalesce(p.stock_status,'instock') <> 'outofstock'")
	}
	if q.StockStatus != "" {
		conds = append(conds, "coalesce(p.stock_status,'instock') = "+add(q.StockStatus))
	}
	switch q.Visibility {
	case "catalog":
		conds = append(conds, "coalesce(p.catalog_visibility,'visible') IN ('visible','catalog')")
		if s.gooOptBool("hide_out_of_stock_items") {
			conds = append(conds, "coalesce(p.stock_status,'instock') <> 'outofstock'")
		}
	case "search":
		conds = append(conds, "coalesce(p.catalog_visibility,'visible') IN ('visible','search')")
	}
	where := strings.Join(conds, " AND ")
	from := `posts po LEFT JOIN goo_products p ON p.post_id = po.id`
	var total int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM `+from+` WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	desc := strings.EqualFold(q.Order, "desc")
	dir := func(def string) string {
		if q.Order == "" {
			return def
		}
		if desc {
			return "DESC"
		}
		return "ASC"
	}
	var order string
	switch q.OrderBy {
	case "popularity":
		order = "coalesce(p.total_sales,0) " + dir("DESC") + ", po.id DESC"
	case "rating":
		order = "coalesce(p.average_rating,0) " + dir("DESC") + ", coalesce(p.rating_count,0) DESC, po.id DESC"
	case "date":
		order = "coalesce(po.published_at, po.created_at) " + dir("DESC") + ", po.id DESC"
	case "price":
		order = "coalesce(p.min_price, p.price) " + dir("ASC") + " NULLS LAST, po.id DESC"
	case "price-desc":
		order = "coalesce(p.max_price, p.price) DESC NULLS LAST, po.id DESC"
	case "title":
		order = "lower(po.title) " + dir("ASC") + ", po.id"
	case "id":
		order = "po.id " + dir("DESC")
	case "rand":
		order = "random()"
	case "include":
		order = "array_position(" + add(q.IDs) + "::bigint[], po.id)"
	default: // menu_order: the custom ordering, then name
		order = "po.menu_order " + dir("ASC") + ", lower(po.title) ASC, po.id"
	}
	sql := `SELECT po.id FROM ` + from + ` WHERE ` + where + ` ORDER BY ` + order
	if q.PerPage > 0 {
		sql += fmt.Sprintf(" LIMIT %d OFFSET %d", q.PerPage, (max(q.Page, 1)-1)*q.PerPage)
	}
	rows, err := s.store.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	m, err := s.gooGetProducts(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*gooProduct, 0, len(ids))
	for _, id := range ids {
		if p := m[id]; p != nil {
			out = append(out, p)
		}
	}
	return out, total, nil
}

// gooTermAndChildren returns a term id with its descendants (a category archive lists subcategories' products).
func (s *Server) gooTermAndChildren(ctx context.Context, termID int64) []int64 {
	rows, err := s.store.Pool.Query(ctx, `WITH RECURSIVE t(id) AS (SELECT $1::bigint UNION SELECT terms.id FROM terms JOIN t ON terms.parent_id = t.id) SELECT id FROM t`, termID)
	if err != nil {
		return []int64{termID}
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// gooPriceRange is the min / max active price of the published catalog (the price filter's bounds).
func (s *Server) gooPriceRange(ctx context.Context, categories []int64) (float64, float64) {
	var lo, hi float64
	q := `SELECT coalesce(min(coalesce(p.min_price, p.price)),0)::float8, coalesce(max(coalesce(p.max_price, p.price)),0)::float8
		FROM goo_products p JOIN posts po ON po.id = p.post_id WHERE po.type = 'product' AND po.status = 'publish'`
	args := []any{}
	if len(categories) > 0 {
		q += ` AND po.id IN (SELECT post_id FROM post_terms WHERE term_id = ANY($1))`
		args = append(args, categories)
	}
	_ = s.store.Pool.QueryRow(ctx, q, args...).Scan(&lo, &hi)
	return lo, hi
}

// gooUpdateRating recalculates a product's average rating and rating count from approved reviews.
func (s *Server) gooUpdateRating(ctx context.Context, productID int64) {
	_, _ = s.store.Pool.Exec(ctx, `UPDATE goo_products SET
		average_rating = coalesce((SELECT round(avg((meta->>'rating')::numeric), 2) FROM comments WHERE post_id = $1 AND status = 'approved'
			AND type IN ('review','comment') AND meta ? 'rating' AND (meta->>'rating') ~ '^[1-5]$'), 0),
		rating_count = (SELECT count(*) FROM comments WHERE post_id = $1 AND status = 'approved' AND type IN ('review','comment') AND meta ? 'rating'
			AND (meta->>'rating') ~ '^[1-5]$')
		WHERE post_id = $1`, productID)
}

// gooIsVerifiedOwner is wc_customer_bought_product(): an order with the product, paid.
func (s *Server) gooIsVerifiedOwner(ctx context.Context, email string, userID, productID int64) bool {
	var ok bool
	_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM goo_orders o JOIN goo_order_items i ON i.order_id = o.id
		WHERE o.type = 'shop_order' AND o.status IN ('processing','completed') AND (i.product_id = $3 OR i.variation_id = $3)
		AND ((o.customer_id <> 0 AND o.customer_id = $2) OR (o.billing_email <> '' AND o.billing_email = lower($1))))`, email, userID, productID).Scan(&ok)
	return ok
}
