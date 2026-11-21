package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// WooCommerce in a WXR file: products (post type "product") and their variations ("product_variation")
// are imported into Pursae Commerce (goo_product.go) with their WooCommerce post meta mapped onto the
// product data; orders, refunds and coupons are counted and reported, not imported (WooCommerce's own
// WXR export carries them only while orders are stored as posts, and customers not at all).
//
// The WordPress importer copies the post meta as it is and WooCommerce reads it back; Pursae keeps the
// product data in typed columns, so the meta below is read here instead of being copied. Other meta
// (custom fields, SEO plugins) is kept on the post like the importer does for posts.

// wxrWooMetaKeys are the WooCommerce product meta keys read into the product data (or derived again
// from it, like _price and the rating counters), so they are not copied as custom fields.
var wxrWooMetaKeys = map[string]bool{
	"_regular_price": true, "_sale_price": true, "_sale_price_dates_from": true, "_sale_price_dates_to": true, "_price": true,
	"_sku": true, "_manage_stock": true, "_stock": true, "_stock_status": true, "_backorders": true, "_low_stock_amount": true,
	"_sold_individually": true, "_weight": true, "_length": true, "_width": true, "_height": true, "_tax_status": true,
	"_tax_class": true, "_virtual": true, "_downloadable": true, "_downloadable_files": true, "_download_limit": true,
	"_download_expiry": true, "_purchase_note": true, "_product_url": true, "_button_text": true, "_product_attributes": true,
	"_default_attributes": true, "_thumbnail_id": true, "_product_image_gallery": true, "_upsell_ids": true, "_crosssell_ids": true,
	"_children": true, "_variation_description": true, "_featured": true, "_visibility": true, "_product_version": true,
	"total_sales": true, "_wc_average_rating": true, "_wc_rating_count": true, "_wc_review_count": true,
	"_edit_lock": true, "_edit_last": true, demoMetaKey: true, demoMetaRef: true,
}

// wxrWooSkippedTypes are the WooCommerce post types that are counted, not imported.
var wxrWooSkippedTypes = map[string]string{
	"shop_order": "orders", "shop_order_placehold": "orders", "shop_order_refund": "refunds", "shop_coupon": "coupons",
	"shop_subscription": "subscriptions",
}

// wxrProductEnv is what the product pass shares with the rest of the import.
type wxrProductEnv struct {
	ensureTerm func(tax, slug, name string, parent int64) int64
	idMap      map[int64]int64 // ids in the file -> ours (attachments are in by now)
	remember   func(old, id int64)
	authors    map[string]int64
	loc        *time.Location
	urls       *strings.Replacer // old upload URLs -> ours, nil when none
	attrs      map[string]*gooAttribute
}

// wxrIsProductTaxonomy reports whether a taxonomy of the file belongs to the store's products.
func wxrIsProductTaxonomy(tax string) bool {
	return tax == "product_cat" || tax == "product_tag" || tax == "product_shipping_class" || strings.HasPrefix(tax, "pa_")
}

// wxrEnsureAttribute finds the global attribute of a pa_ taxonomy, creating it when missing. The file
// does not carry the attribute's label (it lives in woocommerce_attribute_taxonomies), so a new one
// is named after its slug ("pa_shoe-size" -> "Shoe size"), which can be renamed under Products › Attributes.
//
// WooCommerce keeps non-ASCII attribute slugs (wc_sanitize_taxonomy_name url-decodes them: "pa_размер");
// Pursae Commerce's attribute slugs are ASCII, so such an attribute gets an ASCII slug here
// (wxrAttrSlug) and its taxonomy differs from the file's: callers use the returned attribute's
// Taxonomy() for its terms and keys, never the file's name.
func (s *Server) wxrEnsureAttribute(ctx context.Context, cache map[string]*gooAttribute, tax string) (*gooAttribute, error) {
	tax = wxrFileTaxonomy(tax)
	if a, ok := cache[tax]; ok {
		if a == nil {
			return nil, fmt.Errorf("attribute %s could not be created", tax)
		}
		return a, nil
	}
	slug := strings.TrimPrefix(tax, "pa_")
	ascii := wxrAttrSlug(slug)
	name := wxrUcfirst(strings.NewReplacer("-", " ", "_", " ").Replace(slug))
	a := s.gooAttributeByTaxonomy(ctx, "pa_"+ascii)
	if a == nil && ascii != slug {
		// An attribute made here by hand for the store being moved ("Размер" with the slug "razmer").
		a = &gooAttribute{}
		if err := s.store.Pool.QueryRow(ctx, `SELECT id, name, slug, type, order_by, has_archives FROM goo_attributes
			WHERE lower(name) = lower($1) ORDER BY id LIMIT 1`, name).Scan(&a.ID, &a.Name, &a.Slug, &a.Type, &a.OrderBy, &a.HasArchives); err != nil {
			a = nil
		}
	}
	if a == nil {
		a = &gooAttribute{Name: name, Slug: ascii}
		if err := s.gooSaveAttribute(ctx, a); err != nil {
			cache[tax] = nil
			return nil, err
		}
	}
	cache[tax] = a
	return a, nil
}

// wxrFileTaxonomy is a taxonomy name of the file in its decoded form: variation meta keys carry
// sanitize_title() of it ("pa_%d1%80..."), terms and _product_attributes the UTF-8 one.
func wxrFileTaxonomy(tax string) string {
	if rest, ok := strings.CutPrefix(tax, "pa_"); ok && strings.Contains(rest, "%") {
		return "pa_" + wp.SanitizeTitle(rest)
	}
	return tax
}

// wxrAttrSlug is the ASCII attribute slug for a WooCommerce one: the same slug when it is valid here,
// else its Latin transliteration ("größe" -> "grosse"), else (Cyrillic, Greek, CJK, ...) a slug derived
// from a hash of it, so that importing the file again finds the same attribute.
func wxrAttrSlug(slug string) string {
	ascii := gooSanitizeKey(wp.RemoveAccents(slug))
	if ascii == slug && len(ascii) <= 28 {
		return ascii
	}
	sum := sha256.Sum256([]byte(slug))
	hash := hex.EncodeToString(sum[:4])
	switch {
	case ascii == "":
		return "attr-" + hash
	case len(ascii) > 28:
		return strings.TrimRight(ascii[:19], "-_") + "-" + hash
	}
	return ascii
}

// wxrUcfirst upper-cases the first letter (ucfirst on UTF-8; the name of a non-ASCII attribute).
func wxrUcfirst(v string) string {
	r, n := utf8.DecodeRuneInString(v)
	if r == utf8.RuneError {
		return v
	}
	return string(unicode.ToUpper(r)) + v[n:]
}

// wxrOurTaxonomy maps a pa_ taxonomy of the file (as a term domain, attribute name or variation key)
// to the taxonomy of its attribute here; other keys are returned unchanged. An attribute this run has
// not looked up (the variations of a product imported before) has the slug wxrAttrSlug gives it.
func wxrOurTaxonomy(cache map[string]*gooAttribute, key string) string {
	if !strings.HasPrefix(key, "pa_") {
		return key
	}
	tax := wxrFileTaxonomy(key)
	if a := cache[tax]; a != nil {
		return a.Taxonomy()
	}
	return "pa_" + wxrAttrSlug(strings.TrimPrefix(tax, "pa_"))
}

// wxrMetaFloat reads a price-like meta value ("" = not set).
func wxrMetaFloat(v string) *float64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return &f
}

// wxrMetaInt reads a quantity ("12", "12.000000"; "" = not set).
func wxrMetaInt(v string) *int {
	f := wxrMetaFloat(v)
	if f == nil || math.Abs(*f) > math.MaxInt32 {
		return nil
	}
	n := int(math.Round(*f))
	return &n
}

// wxrMetaTime reads a sale date: a Unix timestamp, as WooCommerce stores it.
func wxrMetaTime(v string, loc *time.Location) *time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
		t := time.Unix(n, 0).UTC()
		return &t
	}
	if t, err := time.ParseInLocation("2006-01-02", v, loc); err == nil { // WooCommerce before 3.0
		return &t
	}
	return nil
}

func wxrYes(v string) bool { return v == "yes" || v == "1" }

// wxrImportProducts imports the products of the file, then their variations, then the links between
// products (grouped children, upsells, cross-sells). It returns the ids of the posts it created.
func (s *Server) wxrImportProducts(ctx context.Context, u *store.User, items []*wxrItem, env *wxrProductEnv, rep *importReport) []int64 {
	if len(items) == 0 {
		return nil
	}
	var created []int64
	type link struct {
		id                         int64
		children, upsells, crosses []int64
	}
	var links []link
	parents := map[int64]*gooProduct{} // our product id -> product, for its variations
	existing := map[int64]bool{}       // our ids of products that were already here
	var variations []*wxrItem
	for _, it := range items {
		if it.Type == "product_variation" {
			variations = append(variations, it)
			continue
		}
		if exID := s.wxrExistingProduct(ctx, it, env.loc); exID != 0 {
			rep.Log = append(rep.Log, fmt.Sprintf("Product “%s” already exists.", it.Title))
			env.remember(it.PostID, exID)
			existing[exID] = true
			rep.Skipped++
			continue
		}
		p, ln, err := s.wxrProduct(ctx, u, it, env, rep)
		if err != nil {
			rep.Log = append(rep.Log, fmt.Sprintf("Failed to import “%s”: %v", it.Title, err))
			continue
		}
		env.remember(it.PostID, p.ID)
		parents[p.ID] = p
		created = append(created, p.ID)
		rep.Products++
		if ln.children != nil || ln.upsells != nil || ln.crosses != nil {
			links = append(links, link{id: p.ID, children: ln.children, upsells: ln.upsells, crosses: ln.crosses})
		}
		rep.Comments += s.wxrImportComments(ctx, it, p.ID, env.loc)
		if len(it.Comments) > 0 {
			_ = s.store.RecountComments(ctx, p.ID)
			s.gooUpdateRating(ctx, p.ID)
		}
	}
	for _, it := range variations {
		parentID, ok := env.idMap[it.Parent]
		if !ok {
			rep.Log = append(rep.Log, fmt.Sprintf("Variation “%s” skipped: its product is not in the import.", it.Title))
			rep.Skipped++
			continue
		}
		parent := parents[parentID]
		if parent == nil {
			var err error
			if parent, err = s.gooGetProduct(ctx, parentID); err != nil {
				rep.Skipped++
				continue
			}
			parents[parentID] = parent
		}
		if !gooSubsVariableLike(parent.Type) {
			rep.Log = append(rep.Log, fmt.Sprintf("Variation “%s” skipped: “%s” is not a variable product here.", it.Title, parent.Name))
			rep.Skipped++
			continue
		}
		v := s.wxrVariation(ctx, u, it, parent, env)
		// A product that was here already keeps its variations: one with the same attributes is the
		// same variation (a second run of an import that stopped half way adds only the missing ones).
		if existing[parentID] {
			if same := s.wxrSameVariation(ctx, parentID, v.VariationAttrs); same != 0 {
				env.remember(it.PostID, same)
				rep.Skipped++
				continue
			}
		}
		if v.SKU != "" && s.gooSKUTaken(ctx, v.SKU, 0) {
			rep.Log = append(rep.Log, fmt.Sprintf("Variation “%s”: the SKU %s is used by another product here, so it was left empty.", v.Name, v.SKU))
			v.SKU = ""
		}
		if err := s.gooSaveProduct(ctx, v, u.ID); err != nil {
			rep.Log = append(rep.Log, fmt.Sprintf("Failed to import variation “%s”: %v", it.Title, err))
			continue
		}
		env.remember(it.PostID, v.ID)
		created = append(created, v.ID)
		rep.Variations++
	}
	// Grouped children, upsells and cross-sells point at ids of the file.
	remap := func(old []int64) []int64 {
		var out []int64
		for _, id := range old {
			if n, ok := env.idMap[id]; ok {
				out = append(out, n)
			}
		}
		return out
	}
	for _, ln := range links {
		p, err := s.gooGetProduct(ctx, ln.id)
		if err != nil {
			continue
		}
		p.UpsellIDs, p.CrossSellIDs = remap(ln.upsells), remap(ln.crosses)
		if p.Type == gooTypeGrouped {
			p.Children = remap(ln.children)
		}
		if err := s.gooSaveProduct(ctx, p, u.ID); err != nil {
			rep.Log = append(rep.Log, fmt.Sprintf("Failed to link the products of “%s”: %v", p.Name, err))
		}
	}
	if rep.Products > 0 && !s.gooOn() {
		rep.Log = append(rep.Log, "Pursae Commerce is not active: the products were imported and appear on the site once it is activated on the Plugins screen.")
	}
	return created
}

// wxrExistingProduct is the product of the site that an item of the file already is, 0 when none: the
// one with its slug and title, or, for an item without a slug (WordPress leaves post_name empty on
// drafts and pending posts), the one with its title and date, as the WordPress importer's
// post_exists() matches posts. Without a date, the title and content.
func (s *Server) wxrExistingProduct(ctx context.Context, it *wxrItem, loc *time.Location) int64 {
	if it.Name != "" {
		if ex, err := s.store.PostBySlug(ctx, "product", it.Name); err == nil && ex.Title == it.Title {
			return ex.ID
		}
		return 0
	}
	if it.Title == "" {
		return 0
	}
	var id int64
	var err error
	if t, perr := time.ParseInLocation("2006-01-02 15:04:05", it.PostDate, loc); perr == nil {
		err = s.store.Pool.QueryRow(ctx, `SELECT id FROM posts WHERE type = 'product' AND status <> 'trash' AND title = $1
			AND date_trunc('second', published_at) = $2 ORDER BY id LIMIT 1`, it.Title, t).Scan(&id)
	} else {
		err = s.store.Pool.QueryRow(ctx, `SELECT id FROM posts WHERE type = 'product' AND status <> 'trash' AND title = $1
			AND content = $2 ORDER BY id LIMIT 1`, it.Title, it.Content).Scan(&id)
	}
	if err != nil {
		return 0
	}
	return id
}

type wxrProductLinks struct {
	children, upsells, crosses []int64
}

// wxrProduct creates one product (not a variation) from its item.
func (s *Server) wxrProduct(ctx context.Context, u *store.User, it *wxrItem, env *wxrProductEnv, rep *importReport) (*gooProduct, wxrProductLinks, error) {
	var ln wxrProductLinks
	typ := gooTypeSimple
	var cats, tags []int64
	var shipClass int64
	attrTerms := map[string][]string{} // pa_ taxonomy -> the product's term slugs
	visibility := map[string]bool{}
	for _, cat := range it.Categories {
		switch {
		case cat.Domain == "product_type":
			typ = cat.Nicename
		case cat.Domain == "product_visibility":
			visibility[cat.Nicename] = true
		case cat.Domain == "product_cat":
			if id := env.ensureTerm("product_cat", cat.Nicename, cat.Name, 0); id != 0 {
				cats = append(cats, id)
			}
		case cat.Domain == "product_tag":
			if id := env.ensureTerm("product_tag", cat.Nicename, cat.Name, 0); id != 0 {
				tags = append(tags, id)
			}
		case cat.Domain == "product_shipping_class":
			shipClass = env.ensureTerm("product_shipping_class", cat.Nicename, cat.Name, 0)
		case strings.HasPrefix(cat.Domain, "pa_"):
			ga, err := s.wxrEnsureAttribute(ctx, env.attrs, cat.Domain)
			if err != nil {
				continue
			}
			if id := env.ensureTerm(ga.Taxonomy(), cat.Nicename, cat.Name, 0); id != 0 {
				slug := cat.Nicename
				if strings.Contains(slug, "%") {
					slug = wp.SanitizeTitle(slug)
				}
				attrTerms[ga.Taxonomy()] = append(attrTerms[ga.Taxonomy()], slug)
			}
		}
	}
	switch typ {
	case gooTypeSimple, gooTypeVariable, gooTypeGrouped, gooTypeExternal:
	default:
		// Types of other plugins (subscriptions, bundles, bookings, ...) keep their own meta, which is
		// not read here: the product comes in as a simple one with its price and stock.
		rep.Log = append(rep.Log, fmt.Sprintf("Product “%s” has the type “%s”, which is imported as a simple product.", it.Title, typ))
		typ = gooTypeSimple
	}
	p := gooNewProduct(typ)
	p.Name, p.Slug, p.Status = it.Title, it.Name, defaultStr(it.Status, "draft")
	s.wxrProductData(ctx, u, p, it, env)
	if p.SKU != "" && s.gooSKUTaken(ctx, p.SKU, 0) {
		rep.Log = append(rep.Log, fmt.Sprintf("Product “%s”: the SKU %s is used by another product here, so it was left empty.", it.Title, p.SKU))
		p.SKU = ""
	}
	p.ShippingClassID = shipClass
	if typ == gooTypeExternal {
		p.ProductURL, p.ButtonText = it.meta("_product_url"), it.meta("_button_text")
	}
	// Featured and catalog visibility: product_visibility terms (WooCommerce 3+), or the old meta.
	p.Featured = visibility["featured"] || it.meta("_featured") == "yes"
	switch {
	case visibility["exclude-from-catalog"] && visibility["exclude-from-search"]:
		p.CatalogVisibility = "hidden"
	case visibility["exclude-from-catalog"]:
		p.CatalogVisibility = "search"
	case visibility["exclude-from-search"]:
		p.CatalogVisibility = "catalog"
	default:
		switch v := it.meta("_visibility"); v {
		case "visible", "catalog", "search", "hidden":
			p.CatalogVisibility = v
		}
	}
	p.Attributes, p.DefaultAttributes = s.wxrAttributes(ctx, it, attrTerms, env, rep)
	if thumb, err := strconv.ParseInt(it.meta("_thumbnail_id"), 10, 64); err == nil {
		p.ImageID = env.idMap[thumb]
	}
	for _, old := range phpIDs(it.meta("_product_image_gallery")) {
		if id, ok := env.idMap[old]; ok {
			p.GalleryIDs = append(p.GalleryIDs, id)
		}
	}
	ln.upsells, ln.crosses = phpIDs(it.meta("_upsell_ids")), phpIDs(it.meta("_crosssell_ids"))
	if typ == gooTypeGrouped {
		ln.children = phpIDs(it.meta("_children"))
	}
	post := &store.Post{Type: "product", AuthorID: env.authors[it.Creator], Content: it.Content, Excerpt: it.Excerpt,
		MenuOrder: it.MenuOrder, Password: it.Password, CommentStatus: defaultStr(it.CommentStatus, "open"),
		PingStatus: defaultStr(it.PingStatus, "closed"), Format: "standard", Meta: map[string]any{}}
	if post.AuthorID == 0 {
		post.AuthorID = u.ID
	}
	if !u.Can("unfiltered_html", nil) { // content_save_pre, as for posts
		post.Content, post.Excerpt = wp.KsesPost(post.Content), wp.KsesPost(post.Excerpt)
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", it.PostDate, env.loc); err == nil {
		post.PublishedAt = &t
	}
	for _, m := range it.Meta {
		if !wxrWooMetaKeys[m.Key] && !strings.HasPrefix(m.Key, "attribute_") {
			post.Meta[m.Key] = m.Value
		}
	}
	seogoImportMeta(post.Meta)
	p.Post = post
	if err := s.gooSaveProduct(ctx, p, u.ID); err != nil {
		return nil, ln, err
	}
	if len(cats) == 0 { // wp_set_object_terms with the default product category, as WooCommerce does
		if t, err := s.store.TermBySlug(ctx, "product_cat", s.gooDefaultCatSlug()); err == nil {
			cats = []int64{t.ID}
		}
	}
	_ = s.store.SetPostTerms(ctx, p.ID, "product_cat", cats)
	_ = s.store.SetPostTerms(ctx, p.ID, "product_tag", tags)
	if shipClass != 0 {
		_ = s.store.SetPostTerms(ctx, p.ID, "product_shipping_class", []int64{shipClass})
	}
	return p, ln, nil
}

// wxrProductData reads the meta shared by products and variations.
func (s *Server) wxrProductData(ctx context.Context, u *store.User, p *gooProduct, it *wxrItem, env *wxrProductEnv) {
	p.SKU = strings.TrimSpace(it.meta("_sku"))
	p.RegularPrice, p.SalePrice = wxrMetaFloat(it.meta("_regular_price")), wxrMetaFloat(it.meta("_sale_price"))
	if p.RegularPrice == nil && p.SalePrice == nil && p.Type != gooTypeVariable && p.Type != gooTypeGrouped {
		p.RegularPrice = wxrMetaFloat(it.meta("_price")) // files written by hand or by old versions
	}
	p.SaleFrom, p.SaleTo = wxrMetaTime(it.meta("_sale_price_dates_from"), env.loc), wxrMetaTime(it.meta("_sale_price_dates_to"), env.loc)
	p.ManageStock = wxrYes(it.meta("_manage_stock"))
	if p.ManageStock {
		p.StockQuantity = wxrMetaInt(it.meta("_stock"))
	}
	switch v := it.meta("_stock_status"); v {
	case "instock", "outofstock", "onbackorder":
		p.StockStatus = v
	}
	switch v := it.meta("_backorders"); v {
	case "no", "notify", "yes":
		p.Backorders = v
	}
	p.LowStockAmount = wxrMetaInt(it.meta("_low_stock_amount"))
	p.SoldIndividually = wxrYes(it.meta("_sold_individually"))
	p.Weight, p.Length, p.Width, p.Height = it.meta("_weight"), it.meta("_length"), it.meta("_width"), it.meta("_height")
	switch v := it.meta("_tax_status"); v {
	case "taxable", "shipping", "none":
		p.TaxStatus = v
	}
	p.TaxClass = it.meta("_tax_class")
	p.Virtual, p.Downloadable = wxrYes(it.meta("_virtual")), wxrYes(it.meta("_downloadable"))
	if n := wxrMetaInt(it.meta("_download_limit")); n != nil {
		p.DownloadLimit = *n
	}
	if n := wxrMetaInt(it.meta("_download_expiry")); n != nil {
		p.DownloadExpiry = *n
	}
	p.PurchaseNote = it.meta("_purchase_note")
	if !u.Can("unfiltered_html", nil) { // the note is HTML in the order emails (content_save_pre, as the CSV importer)
		p.PurchaseNote = wp.KsesPost(p.PurchaseNote)
	}
	if p.Downloadable {
		p.Downloads = wxrDownloads(it.meta("_downloadable_files"), env.urls)
	}
}

// wxrDownloads reads _downloadable_files: {key: {id, name, file, enabled}}. File URLs on the old
// site's uploads that were imported point at the imported files; others keep their URL.
func wxrDownloads(v string, urls *strings.Replacer) []gooDownload {
	parsed, err := phpUnserialize(v)
	if err != nil {
		return nil
	}
	arr, ok := parsed.(*phpArray)
	if !ok {
		return nil
	}
	var out []gooDownload
	for i, x := range arr.Vals {
		f, ok := x.(*phpArray)
		if !ok {
			continue
		}
		if en := f.Get("enabled"); en != nil && !phpTruthy(en) {
			continue
		}
		d := gooDownload{ID: defaultStr(phpString(f.Get("id")), phpString(arr.Keys[i])), Name: phpString(f.Get("name")), File: phpString(f.Get("file"))}
		if d.File == "" {
			continue
		}
		if urls != nil {
			d.File = urls.Replace(d.File)
		}
		out = append(out, d)
	}
	return out
}

// wxrAttributes reads _product_attributes (and _default_attributes). A taxonomy attribute takes its
// options from the product's pa_ terms (keyed by our taxonomy), a custom one from its "A | B" value.
func (s *Server) wxrAttributes(ctx context.Context, it *wxrItem, terms map[string][]string, env *wxrProductEnv, rep *importReport) ([]gooProductAttr, map[string]string) {
	defaults := map[string]string{}
	parsed, err := phpUnserialize(it.meta("_product_attributes"))
	if err != nil {
		return nil, defaults
	}
	arr, ok := parsed.(*phpArray)
	if !ok {
		return nil, defaults
	}
	var out []gooProductAttr
	for i, x := range arr.Vals {
		f, ok := x.(*phpArray)
		if !ok {
			continue
		}
		name := phpString(f.Get("name"))
		if name == "" {
			name = phpString(arr.Keys[i])
		}
		pos := len(out)
		if n, err := strconv.Atoi(phpString(f.Get("position"))); err == nil {
			pos = n
		}
		a := gooProductAttr{Name: name, Position: pos, Visible: phpTruthy(f.Get("is_visible")), Variation: phpTruthy(f.Get("is_variation"))}
		if phpTruthy(f.Get("is_taxonomy")) {
			ga, err := s.wxrEnsureAttribute(ctx, env.attrs, name)
			if err != nil {
				rep.Log = append(rep.Log, fmt.Sprintf("Product “%s”: the attribute %s was skipped: %v", it.Title, name, err))
				continue
			}
			a.ID, a.Name, a.Taxonomy, a.Options = ga.ID, ga.Name, ga.Taxonomy(), terms[ga.Taxonomy()]
		} else {
			for _, o := range strings.Split(phpString(f.Get("value")), "|") {
				if o = strings.TrimSpace(o); o != "" {
					a.Options = append(a.Options, o)
				}
			}
		}
		out = append(out, a)
	}
	if parsed, err := phpUnserialize(it.meta("_default_attributes")); err == nil {
		if d, ok := parsed.(*phpArray); ok {
			for i, k := range d.Keys {
				key := wxrAttrKey(out, wxrOurTaxonomy(env.attrs, phpString(k)))
				if v := phpString(d.Vals[i]); key != "" && v != "" {
					defaults[key] = v
				}
			}
		}
	}
	return out, defaults
}

// wxrAttrKey maps a WooCommerce attribute key (a pa_ taxonomy, already mapped to ours by
// wxrOurTaxonomy, or sanitize_title() of a custom attribute's name, %xx-encoded when not ASCII) to the
// key Pursae Commerce uses for that attribute.
func wxrAttrKey(attrs []gooProductAttr, key string) string {
	if strings.Contains(key, "%") {
		key = wp.SanitizeTitle(key)
	}
	for _, a := range attrs {
		if a.Taxonomy != "" && a.Taxonomy == key {
			return a.Key()
		}
		if a.Taxonomy == "" && (wp.SanitizeTitle(a.Name) == key || gooSanitizeKey(a.Name) == key || strings.EqualFold(a.Name, key)) {
			return a.Key()
		}
	}
	if strings.HasPrefix(key, "pa_") {
		return key
	}
	return gooSanitizeKey(key)
}

// wxrVariation builds (does not save) a variation of parent from its item.
func (s *Server) wxrVariation(ctx context.Context, u *store.User, it *wxrItem, parent *gooProduct, env *wxrProductEnv) *gooProduct {
	v := gooNewProduct(gooTypeVariation)
	v.ParentID, v.Name, v.Slug = parent.ID, defaultStr(it.Title, parent.Name), it.Name
	v.Status = "publish"
	if it.Status == "private" { // a disabled variation
		v.Status = "private"
	}
	s.wxrProductData(ctx, u, v, it, env)
	v.Description = it.meta("_variation_description")
	if !u.Can("unfiltered_html", nil) {
		v.Description = wp.KsesPost(v.Description)
	}
	if thumb, err := strconv.ParseInt(it.meta("_thumbnail_id"), 10, 64); err == nil {
		v.ImageID = env.idMap[thumb]
	}
	for _, m := range it.Meta {
		if k, ok := strings.CutPrefix(m.Key, "attribute_"); ok {
			val := m.Value
			if strings.HasPrefix(k, "pa_") && strings.Contains(val, "%") {
				val = wp.SanitizeTitle(val)
			}
			v.VariationAttrs[wxrAttrKey(parent.Attributes, wxrOurTaxonomy(env.attrs, k))] = val
		}
	}
	post := &store.Post{Type: "product_variation", AuthorID: parent.Post.AuthorID, MenuOrder: it.MenuOrder,
		CommentStatus: "closed", PingStatus: "closed", Format: "standard", Meta: map[string]any{}}
	if post.AuthorID == 0 {
		post.AuthorID = u.ID
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", it.PostDate, env.loc); err == nil {
		post.PublishedAt = &t
	}
	for _, m := range it.Meta {
		if !wxrWooMetaKeys[m.Key] && !strings.HasPrefix(m.Key, "attribute_") {
			post.Meta[m.Key] = m.Value
		}
	}
	v.Post = post
	return v
}

// wxrSameVariation finds a variation of the product with exactly these attributes.
func (s *Server) wxrSameVariation(ctx context.Context, parentID int64, attrs map[string]string) int64 {
	vars, err := s.gooVariations(ctx, parentID, true)
	if err != nil {
		return 0
	}
	for _, v := range vars {
		if len(v.VariationAttrs) != len(attrs) {
			continue
		}
		same := true
		for k, val := range attrs {
			if got, ok := v.VariationAttrs[k]; !ok || !strings.EqualFold(got, val) {
				same = false
				break
			}
		}
		if same {
			return v.ID
		}
	}
	return 0
}

// wxrWooSkippedLog is the report line for the WooCommerce records that are not imported.
func wxrWooSkippedLog(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	var parts []string
	for _, what := range []string{"orders", "refunds", "coupons", "subscriptions"} {
		if n := counts[what]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, what))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "Skipped " + strings.Join(parts, ", ") + ": WooCommerce orders, coupons and customers are not imported from a WXR file."
}
