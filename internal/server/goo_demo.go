package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"wpgo/internal/store"
)

// Goocommerce demo content: the "goocommerce" section of a theme's demo/demo.json (docs/theme-package.md).
// Importing it switches Goocommerce on (pages included), applies the store settings, and creates
// product categories, global attributes, products with their images, variations and reviews, coupons
// and a shipping zone. Every created post, term and coupon is marked with _wpgo_demo so Remove Demo
// Content deletes exactly them.
//
//	"goocommerce": {
//	  "settings": {"currency": "EUR", "currency_pos": "right_space", "catalog_columns": 3},
//	  "categories": [{"slug": "dresses", "name": "Dresses", "description": "…", "parent": "", "image": "media-id"}],
//	  "attributes": [{"slug": "size", "name": "Size", "terms": ["S", "M", "L"]}],
//	  "products": [{"slug": "linen-dress", "name": "Linen Dress", "type": "simple|variable|grouped|external",
//	    "regular_price": "89", "sale_price": "69", "sku": "LD-01", "description": "<p>…</p>", "short_description": "…",
//	    "categories": ["dresses"], "tags": ["summer"], "image": "media-id", "gallery": ["media-id"],
//	    "featured": true, "stock": 12, "virtual": false, "weight": "0.4", "menu_order": 1,
//	    "attributes": [{"name": "Size", "values": ["S", "M"], "variation": true, "visible": true}],
//	    "variations": [{"attributes": {"size": "s"}, "regular_price": "89", "sale_price": "", "stock": 4, "sku": "LD-01-S"}],
//	    "children": ["other-slug"], "external_url": "https://…", "button_text": "Buy at the maker",
//	    "upsells": ["slug"], "cross_sells": ["slug"],
//	    "reviews": [{"author": "Mara", "email": "mara@example.com", "rating": 5, "content": "…"}]}],
//	  "coupons": [{"code": "WELCOME10", "type": "percent", "amount": "10", "description": "…"}],
//	  "shipping": {"zone": "Everywhere", "flat_rate": "5", "free_over": "100", "local_pickup": true}
//	}

type gooDemo struct {
	Settings   map[string]any      `json:"settings"`
	Categories []gooDemoCategory   `json:"categories"`
	Attributes []gooDemoAttribute  `json:"attributes"`
	Products   []gooDemoProduct    `json:"products"`
	Coupons    []gooDemoCoupon     `json:"coupons"`
	Shipping   *gooDemoShipping    `json:"shipping"`
	Tags       []map[string]string `json:"tags"`
	Extensions []string            `json:"extensions"` // modules switched on by the demo
	Extra      map[string]any      `json:"extra,omitempty"`
}

type gooDemoCategory struct {
	Slug, Name, Description, Parent, Image string
}

type gooDemoAttribute struct {
	Slug  string   `json:"slug"`
	Name  string   `json:"name"`
	Terms []string `json:"terms"`
}

type gooDemoProductAttr struct {
	Name      string   `json:"name"`
	Values    []string `json:"values"`
	Variation bool     `json:"variation"`
	Visible   *bool    `json:"visible"`
}

type gooDemoVariation struct {
	Attributes   map[string]string `json:"attributes"`
	RegularPrice string            `json:"regular_price"`
	SalePrice    string            `json:"sale_price"`
	Stock        *int              `json:"stock"`
	SKU          string            `json:"sku"`
	Image        string            `json:"image"`
}

type gooDemoReview struct {
	Author  string `json:"author"`
	Email   string `json:"email"`
	Rating  int    `json:"rating"`
	Content string `json:"content"`
}

type gooDemoProduct struct {
	Slug             string               `json:"slug"`
	Name             string               `json:"name"`
	Type             string               `json:"type"`
	RegularPrice     string               `json:"regular_price"`
	SalePrice        string               `json:"sale_price"`
	SKU              string               `json:"sku"`
	Description      string               `json:"description"`
	ShortDescription string               `json:"short_description"`
	Categories       []string             `json:"categories"`
	Tags             []string             `json:"tags"`
	Image            string               `json:"image"`
	Gallery          []string             `json:"gallery"`
	Featured         bool                 `json:"featured"`
	Stock            *int                 `json:"stock"`
	Virtual          bool                 `json:"virtual"`
	Downloadable     bool                 `json:"downloadable"`
	Downloads        []gooDownload        `json:"downloads"`
	Weight           string               `json:"weight"`
	MenuOrder        int                  `json:"menu_order"`
	Attributes       []gooDemoProductAttr `json:"attributes"`
	Variations       []gooDemoVariation   `json:"variations"`
	Children         []string             `json:"children"`
	ExternalURL      string               `json:"external_url"`
	ButtonText       string               `json:"button_text"`
	Upsells          []string             `json:"upsells"`
	CrossSells       []string             `json:"cross_sells"`
	Reviews          []gooDemoReview      `json:"reviews"`
	Meta             map[string]any       `json:"meta"`
}

type gooDemoCoupon struct {
	Code        string `json:"code"`
	Type        string `json:"type"`
	Amount      string `json:"amount"`
	Description string `json:"description"`
	FreeShip    bool   `json:"free_shipping"`
	MinAmount   string `json:"minimum_amount"`
}

type gooDemoShipping struct {
	Zone        string `json:"zone"`
	FlatRate    string `json:"flat_rate"`
	FreeOver    string `json:"free_over"`
	LocalPickup bool   `json:"local_pickup"`
}

// gooDemoSettings are the store options a demo may set.
var gooDemoSettings = map[string]bool{"currency": true, "currency_pos": true, "price_thousand_sep": true, "price_decimal_sep": true,
	"price_num_decimals": true, "catalog_columns": true, "catalog_rows": true, "default_catalog_orderby": true, "weight_unit": true,
	"dimension_unit": true, "demo_store": true, "demo_store_notice": true, "shop_page_display": true, "category_archive_display": true,
	"enable_reviews": true, "calc_taxes": true, "default_country": true}

// gooValidateDemo checks a demo's goocommerce section; mediaIDs are the demo's media handles.
func gooValidateDemo(raw json.RawMessage, media []demoMedia) []string {
	if len(raw) == 0 {
		return nil
	}
	var problems []string
	bad := func(format string, args ...any) {
		problems = append(problems, "goocommerce."+fmt.Sprintf(format, args...))
	}
	var d gooDemo
	if err := json.Unmarshal(raw, &d); err != nil {
		return []string{"goocommerce: " + err.Error()}
	}
	mids := map[string]bool{}
	for _, m := range media {
		mids[m.ID] = true
	}
	medium := func(at, id string) {
		if id != "" && !mids[id] {
			bad("%s %q is not a media item of the demo", at, id)
		}
	}
	for k := range d.Settings {
		if !gooDemoSettings[k] {
			bad("settings.%s may not be set", k)
		}
	}
	cats := map[string]bool{}
	for i, c := range d.Categories {
		if !themeSlugRe.MatchString(c.Slug) || cats[c.Slug] {
			bad("categories[%d].slug %q is invalid or listed twice", i, c.Slug)
		}
		cats[c.Slug] = true
		if strings.TrimSpace(c.Name) == "" {
			bad("categories[%d].name is required", i)
		}
		medium(fmt.Sprintf("categories[%d].image", i), c.Image)
	}
	for i, c := range d.Categories {
		if c.Parent != "" && !cats[c.Parent] {
			bad("categories[%d].parent %q is not a category of the demo", i, c.Parent)
		}
	}
	attrs := map[string]bool{}
	for i, a := range d.Attributes {
		if !themeSlugRe.MatchString(a.Slug) || len(a.Slug) > 28 {
			bad("attributes[%d].slug %q is invalid", i, a.Slug)
		}
		attrs[strings.ToLower(a.Name)] = true
	}
	slugs := map[string]bool{}
	for _, p := range d.Products {
		slugs[p.Slug] = true
	}
	seen := map[string]bool{}
	for i, p := range d.Products {
		at := fmt.Sprintf("products[%d]", i)
		if !themeSlugRe.MatchString(p.Slug) || seen[p.Slug] {
			bad("%s.slug %q is invalid or listed twice", at, p.Slug)
		}
		seen[p.Slug] = true
		if strings.TrimSpace(p.Name) == "" {
			bad("%s.name is required", at)
		}
		switch p.Type {
		case "", "simple", "variable", "grouped", "external":
		default:
			bad("%s.type %q must be simple, variable, grouped or external", at, p.Type)
		}
		for _, v := range []string{p.RegularPrice, p.SalePrice} {
			if v != "" {
				if _, err := strconv.ParseFloat(v, 64); err != nil {
					bad("%s: price %q is not a number", at, v)
				}
			}
		}
		for _, c := range p.Categories {
			if !cats[c] {
				bad("%s.categories: %q is not a category of the demo", at, c)
			}
		}
		medium(at+".image", p.Image)
		for _, g := range p.Gallery {
			medium(at+".gallery", g)
		}
		for _, list := range [][]string{p.Children, p.Upsells, p.CrossSells} {
			for _, s := range list {
				if !slugs[s] {
					bad("%s: %q is not a product of the demo", at, s)
				}
			}
		}
		if p.Type == "variable" && len(p.Variations) == 0 {
			bad("%s: a variable product needs variations", at)
		}
		for j, v := range p.Variations {
			medium(fmt.Sprintf("%s.variations[%d].image", at, j), v.Image)
		}
		for j, r := range p.Reviews {
			if r.Rating < 1 || r.Rating > 5 || strings.TrimSpace(r.Content) == "" || strings.TrimSpace(r.Author) == "" {
				bad("%s.reviews[%d] needs an author, content and a rating from 1 to 5", at, j)
			}
		}
	}
	for i, c := range d.Coupons {
		if strings.TrimSpace(c.Code) == "" {
			bad("coupons[%d].code is required", i)
		}
	}
	for _, k := range d.Extensions {
		if gooExtensions[k] == nil {
			bad("extensions: %q is not a Goocommerce extension", k)
		}
	}
	return problems
}

// gooImportDemo imports a demo's goocommerce section (idempotent: items already imported are skipped).
func (s *Server) gooImportDemo(ctx context.Context, uid int64, slug string, raw json.RawMessage, mediaIDs map[string]int64) (int, error) {
	if len(raw) == 0 {
		return 0, nil
	}
	var d gooDemo
	if err := json.Unmarshal(raw, &d); err != nil {
		return 0, err
	}
	mark := func(ref string) map[string]any { return map[string]any{demoMetaKey: slug, demoMetaRef: ref} }
	// The store: on, with its pages.
	if !s.gooOn() {
		if err := s.gooCreatePages(ctx, uid); err != nil {
			return 0, err
		}
		vals := map[string]any{gooSetting: true}
		if s.gooOptString("version") == "" {
			vals[gooOptPrefix+"version"] = gooVersion
			vals[gooOptPrefix+"email_from_name"] = s.settings.String("blogname")
			vals[gooOptPrefix+"email_from_address"] = s.settings.String("admin_email")
		}
		if err := s.settings.Set(ctx, vals); err != nil {
			return 0, err
		}
		s.configureCache()
	} else if err := s.gooCreatePages(ctx, uid); err != nil {
		return 0, err
	}
	if len(d.Settings) > 0 {
		vals := map[string]any{}
		for k, v := range d.Settings {
			if gooDemoSettings[k] {
				vals[k] = v
			}
		}
		if err := s.gooSetOpts(ctx, vals); err != nil {
			return 0, err
		}
	}
	for _, k := range d.Extensions {
		if !s.gooExtOn(k) {
			_ = s.gooSetExtension(ctx, k, true)
		}
	}
	count := 0
	// Categories (parents first).
	catIDs := map[string]int64{}
	for pass := 0; pass <= len(d.Categories); pass++ {
		left := 0
		for _, c := range d.Categories {
			if _, ok := catIDs[c.Slug]; ok {
				continue
			}
			if c.Parent != "" {
				if _, ok := catIDs[c.Parent]; !ok {
					left++
					continue
				}
			}
			if ex, err := s.store.TermBySlug(ctx, "product_cat", c.Slug); err == nil {
				catIDs[c.Slug] = ex.ID
				continue
			}
			meta := mark(c.Slug)
			if id := mediaIDs[c.Image]; id > 0 {
				meta["thumbnail_id"] = float64(id)
			}
			t := &store.Term{Taxonomy: "product_cat", Slug: c.Slug, Name: c.Name, Description: c.Description, ParentID: catIDs[c.Parent], Meta: meta}
			if err := s.store.InsertTerm(ctx, t); err != nil {
				return count, fmt.Errorf("product category %q: %v", c.Name, err)
			}
			catIDs[c.Slug] = t.ID
			count++
		}
		if left == 0 {
			break
		}
	}
	tagID := func(name string) int64 {
		sl := gooSanitizeKey(name)
		if t, err := s.store.TermBySlug(ctx, "product_tag", sl); err == nil {
			return t.ID
		}
		t := &store.Term{Taxonomy: "product_tag", Slug: sl, Name: name, Meta: mark("tag-" + sl)}
		if err := s.store.InsertTerm(ctx, t); err != nil {
			return 0
		}
		return t.ID
	}
	// Global attributes and their terms.
	attrTax := map[string]string{} // lower(name) → pa_slug
	for _, a := range d.Attributes {
		ga := s.gooAttributeByTaxonomy(ctx, "pa_"+a.Slug)
		if ga == nil {
			ga = &gooAttribute{Name: a.Name, Slug: a.Slug}
			if err := s.gooSaveAttribute(ctx, ga); err != nil {
				return count, err
			}
		}
		attrTax[strings.ToLower(a.Name)] = ga.Taxonomy()
		for i, tn := range a.Terms {
			sl := gooSanitizeKey(tn)
			if _, err := s.store.TermBySlug(ctx, ga.Taxonomy(), sl); err == nil {
				continue
			}
			meta := mark(ga.Taxonomy() + ":" + sl)
			meta["order"] = float64(i)
			_ = s.store.InsertTerm(ctx, &store.Term{Taxonomy: ga.Taxonomy(), Slug: sl, Name: tn, Meta: meta})
		}
	}
	// Products: a first pass creates them all, a second links grouped children, upsells and cross-sells.
	ids := map[string]int64{}
	price := func(v string) *float64 {
		if v == "" {
			return nil
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil
		}
		return &f
	}
	for i, dp := range d.Products {
		if id := s.demoItemID(ctx, "posts", "product", slug, "product:"+dp.Slug); id > 0 {
			ids[dp.Slug] = id
			continue
		}
		typ := defaultStr(dp.Type, gooTypeSimple)
		p := gooNewProduct(typ)
		p.Name, p.Slug, p.SKU, p.Featured = dp.Name, dp.Slug, dp.SKU, dp.Featured
		p.RegularPrice, p.SalePrice = price(dp.RegularPrice), price(dp.SalePrice)
		p.Virtual, p.Weight = dp.Virtual, dp.Weight
		p.Downloadable, p.Downloads = dp.Downloadable, dp.Downloads
		if v, _ := dp.Meta["_downloadable"].(string); v == "yes" {
			p.Downloadable = true // older demos flag it in meta
		}
		p.ProductURL, p.ButtonText = dp.ExternalURL, dp.ButtonText
		if dp.Stock != nil {
			p.ManageStock, p.StockQuantity = true, gooIntPtr(*dp.Stock)
		}
		p.ImageID = mediaIDs[dp.Image]
		for _, g := range dp.Gallery {
			if id := mediaIDs[g]; id > 0 {
				p.GalleryIDs = append(p.GalleryIDs, id)
			}
		}
		for k, v := range dp.Meta {
			p.Meta[k] = v
		}
		for pos, a := range dp.Attributes {
			visible := true
			if a.Visible != nil {
				visible = *a.Visible
			}
			pa := gooProductAttr{Name: a.Name, Position: pos, Visible: visible, Variation: a.Variation}
			if tax, ok := attrTax[strings.ToLower(a.Name)]; ok {
				pa.Taxonomy = tax
				for _, v := range a.Values {
					pa.Options = append(pa.Options, gooSanitizeKey(v))
				}
			} else {
				pa.Options = a.Values
			}
			p.Attributes = append(p.Attributes, pa)
		}
		p.Post = &store.Post{Type: "product", Content: dp.Description, Excerpt: dp.ShortDescription, CommentStatus: "open", PingStatus: "closed",
			MenuOrder: defaultInt(dp.MenuOrder, i), Meta: mark("product:" + dp.Slug)}
		if err := s.gooSaveProduct(ctx, p, uid); err != nil {
			return count, fmt.Errorf("product %q: %v", dp.Name, err)
		}
		ids[dp.Slug] = p.ID
		count++
		var cats []int64
		for _, c := range dp.Categories {
			if id := catIDs[c]; id > 0 {
				cats = append(cats, id)
			}
		}
		if len(cats) == 0 {
			if t, err := s.store.TermBySlug(ctx, "product_cat", "uncategorized"); err == nil {
				cats = append(cats, t.ID)
			}
		}
		_ = s.store.SetPostTerms(ctx, p.ID, "product_cat", cats)
		var tags []int64
		for _, tg := range dp.Tags {
			if id := tagID(tg); id > 0 {
				tags = append(tags, id)
			}
		}
		if len(tags) > 0 {
			_ = s.store.SetPostTerms(ctx, p.ID, "product_tag", tags)
		}
		// Variations.
		for j, dv := range dp.Variations {
			v := gooNewProduct(gooTypeVariation)
			v.ParentID, v.SKU = p.ID, dv.SKU
			v.RegularPrice, v.SalePrice = price(dv.RegularPrice), price(dv.SalePrice)
			if dv.Stock != nil {
				v.ManageStock, v.StockQuantity = true, gooIntPtr(*dv.Stock)
			}
			v.ImageID = mediaIDs[dv.Image]
			var label []string
			for k, val := range dv.Attributes {
				key := k
				if tax, ok := attrTax[strings.ToLower(k)]; ok {
					key, val = tax, gooSanitizeKey(val)
				} else if tax, ok := attrTax[strings.ToLower(strings.TrimPrefix(k, "pa_"))]; ok {
					key, val = tax, gooSanitizeKey(val)
				} else {
					key = gooSanitizeKey(k)
				}
				v.VariationAttrs[key] = val
				label = append(label, dv.Attributes[k])
			}
			v.Name = p.Name + " - " + strings.Join(label, ", ")
			v.Post = &store.Post{Type: "product_variation", Status: "publish", CommentStatus: "closed", PingStatus: "closed", MenuOrder: j,
				Meta: mark(fmt.Sprintf("variation:%s:%d", dp.Slug, j))}
			if err := s.gooSaveProduct(ctx, v, uid); err != nil {
				return count, fmt.Errorf("variation of %q: %v", dp.Name, err)
			}
		}
		// Reviews (approved, verified buyers of the demo).
		for _, r := range dp.Reviews {
			cm := &store.Comment{PostID: p.ID, AuthorName: r.Author, AuthorEmail: r.Email, Content: r.Content, Type: "review", Status: "approved",
				Meta: map[string]any{"rating": strconv.Itoa(r.Rating), "verified": false, demoMetaKey: slug}}
			if err := s.store.InsertComment(ctx, cm); err == nil {
				_ = s.store.RecountComments(ctx, p.ID)
			}
		}
		if len(dp.Reviews) > 0 {
			s.gooUpdateRating(ctx, p.ID)
		}
	}
	for _, dp := range d.Products {
		if len(dp.Children) == 0 && len(dp.Upsells) == 0 && len(dp.CrossSells) == 0 {
			continue
		}
		p, err := s.gooGetProduct(ctx, ids[dp.Slug])
		if err != nil {
			continue
		}
		pick := func(list []string) []int64 {
			var out []int64
			for _, sl := range list {
				if id := ids[sl]; id > 0 {
					out = append(out, id)
				}
			}
			return out
		}
		p.Children, p.UpsellIDs, p.CrossSellIDs = pick(dp.Children), pick(dp.Upsells), pick(dp.CrossSells)
		if err := s.gooSaveProduct(ctx, p, uid); err != nil {
			return count, err
		}
	}
	// Coupons.
	for _, dc := range d.Coupons {
		if _, err := s.gooCouponByCode(ctx, dc.Code); err == nil {
			continue
		}
		amt, _ := strconv.ParseFloat(dc.Amount, 64)
		cp := &gooCoupon{Code: dc.Code, DiscountType: defaultStr(dc.Type, "percent"), Amount: amt, Description: dc.Description,
			FreeShipping: dc.FreeShip, MinimumAmount: price(dc.MinAmount), Meta: mark("coupon:" + strings.ToLower(dc.Code))}
		if err := s.gooSaveCoupon(ctx, cp); err == nil {
			count++
		}
	}
	// A shipping zone covering everywhere (zone 0) gets the demo's methods once.
	if d.Shipping != nil {
		existing, _ := s.gooMethodInstances(ctx, 0)
		has := false
		for _, m := range existing {
			if dm, _ := m.Settings[demoMetaKey].(string); dm == slug {
				has = true
			}
		}
		if !has {
			if d.Shipping.FlatRate != "" {
				_, _ = s.gooAddMethod(ctx, 0, "flat_rate", map[string]any{"cost": d.Shipping.FlatRate, demoMetaKey: slug})
			}
			if d.Shipping.FreeOver != "" {
				_, _ = s.gooAddMethod(ctx, 0, "free_shipping", map[string]any{"requires": "min_amount", "min_amount": d.Shipping.FreeOver, demoMetaKey: slug})
			}
			if d.Shipping.LocalPickup {
				_, _ = s.gooAddMethod(ctx, 0, "local_pickup", map[string]any{demoMetaKey: slug})
			}
		}
	}
	// The demo should be usable at once: the test gateway and cash on delivery are switched on.
	for _, gw := range []string{"cod", "goo_test"} {
		st := s.gooOptMap(gw + "_settings")
		if _, ok := st["enabled"]; !ok {
			m := map[string]any{}
			for k, v := range st {
				m[k] = v
			}
			m["enabled"] = "yes"
			_ = s.gooSetOpts(ctx, map[string]any{gw + "_settings": m})
		}
	}
	s.purgeAll(ctx)
	return count, nil
}

// gooRemoveDemo deletes what gooImportDemo created besides posts and terms (coupons, shipping methods).
func (s *Server) gooRemoveDemo(ctx context.Context, slug string) {
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM goo_coupons WHERE meta->>$1 = $2`, demoMetaKey, slug)
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM goo_shipping_methods WHERE settings->>$1 = $2`, demoMetaKey, slug)
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM comments WHERE meta->>$1 = $2`, demoMetaKey, slug)
}

func defaultInt(v, def int) int {
	if v != 0 {
		return v
	}
	return def
}
