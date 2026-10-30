package server

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// gooCatEditor opens an editor URL and returns the product id and the form nonce.
func gooCatEditor(t *testing.T, c *http.Client, u string) (int64, string, string) {
	t.Helper()
	code, body, _ := get(t, c, u)
	if code != http.StatusOK {
		t.Fatalf("GET %s: %d %.300s", u, code, body)
	}
	m := regexp.MustCompile(`id="post_ID" name="post_ID" value="(\d+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no post_ID on %s", u)
	}
	id, _ := strconv.ParseInt(m[1], 10, 64)
	return id, formNonce(t, body), body
}

// gooCatSave posts the product editor and returns the redirect target.
func gooCatSave(t *testing.T, c *http.Client, base string, id int64, nonce string, form url.Values) string {
	t.Helper()
	form.Set("post_ID", itoa64(id))
	form.Set("_wpnonce", nonce)
	form.Set("action", "editpost")
	form.Set("post_type", "product")
	code, body, loc := post(t, c, base+"/wp-admin/post", form)
	if code != http.StatusFound {
		t.Fatalf("save product %d: %d %.400s", id, code, body)
	}
	return loc
}

func gooCatHref(t *testing.T, body, pattern string) string {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no match for %s", pattern)
	}
	return html.UnescapeString(m[1])
}

func gooCatTerm(t *testing.T, srv *Server, tax, name string) *store.Term {
	t.Helper()
	term := &store.Term{Taxonomy: tax, Name: name}
	if err := srv.store.InsertTerm(context.Background(), term); err != nil {
		t.Fatal(err)
	}
	return term
}

func TestGooCatalogList(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	_ = srv.gooSetOpts(ctx, map[string]any{"manage_stock": true, "currency": "USD"})
	sale := gooMakeSimple(t, srv, "Blue Hoodie", 50, func(p *gooProduct) {
		p.SalePrice, p.SKU, p.ManageStock, p.StockQuantity = gooFloatPtr(40), "HOOD-1", true, gooIntPtr(12)
	})
	gooMakeSimple(t, srv, "Sold Out Cap", 15, func(p *gooProduct) { p.StockStatus, p.SKU = "outofstock", "CAP-1" })
	gooMakeSimple(t, srv, "Ebook", 9, func(p *gooProduct) { p.Downloadable, p.Virtual = true, true })

	code, body, _ := get(t, c, base+"/wp-admin/edit?post_type=product")
	if code != http.StatusOK {
		t.Fatalf("products list: %d %.300s", code, body)
	}
	for _, want := range []string{"column-thumb", "column-sku", "column-is_in_stock", "column-price", "column-product_cat", "column-product_tag",
		"column-featured", "column-date", "Select a category", "Filter by product type", "&rarr; Downloadable", "Filter by stock status",
		`<mark class="instock">In stock</mark> (12)`, `<mark class="outofstock">Out of stock</mark>`, "<del", "Blue Hoodie", "HOOD-1", "ID: " + itoa64(sale.ID),
		"Duplicate", "Add new product", "page=product_importer", "page=product_exporter", "Sorting", "admin-catalog.css", "post-search-input"} {
		if !strings.Contains(body, want) {
			t.Errorf("products list lacks %q", want)
		}
	}
	// Search by SKU and the stock / type filters.
	_, body, _ = get(t, c, base+"/wp-admin/edit?post_type=product&s=CAP-1")
	if !strings.Contains(body, "Sold Out Cap") || strings.Contains(body, "Blue Hoodie</a>") {
		t.Error("SKU search did not narrow the list")
	}
	_, body, _ = get(t, c, base+"/wp-admin/edit?post_type=product&stock_status=outofstock")
	if !strings.Contains(body, "Sold Out Cap") || strings.Contains(body, ">Blue Hoodie</a>") {
		t.Error("stock filter did not narrow the list")
	}
	_, body, _ = get(t, c, base+"/wp-admin/edit?post_type=product&product_type=downloadable")
	if !strings.Contains(body, ">Ebook</a>") || strings.Contains(body, ">Blue Hoodie</a>") {
		t.Error("downloadable filter did not narrow the list")
	}
	// Featured toggle.
	_, body, _ = get(t, c, base+"/wp-admin/edit?post_type=product")
	feat := gooCatHref(t, body, `href="([^"]*woocommerce_feature_product&amp;product_id=`+itoa64(sale.ID)+`[^"]*)"`)
	if code, _, _ := get(t, c, base+feat); code != http.StatusFound {
		t.Fatalf("featured toggle: %d", code)
	}
	if p, _ := srv.gooGetProduct(ctx, sale.ID); !p.Featured {
		t.Error("product not featured after the toggle")
	}
	if code, _, _ := get(t, c, base+strings.Replace(feat, "_wpnonce=", "_wpnonce=x", 1)); code != http.StatusForbidden {
		t.Error("featured toggle accepted a bad nonce")
	}
	// Product search JSON (Linked Products).
	_, js, _ := get(t, c, base+"/wp-admin/edit?post_type=product&goo_search_products=hood")
	var found map[string]string
	if err := json.Unmarshal([]byte(js), &found); err != nil || found[itoa64(sale.ID)] != "Blue Hoodie (HOOD-1)" {
		t.Errorf("product search: %v %s", err, js)
	}
	// Bulk edit: price +10%, stock status, featured off.
	_, body, _ = get(t, c, base+"/wp-admin/edit?post_type=product")
	nonce := gooCatHref(t, body, `name="_wpnonce" value="([^"]+)"`)
	q := url.Values{"post_type": {"product"}, "action": {"edit"}, "bulk_edit": {"Update"}, "_wpnonce": {nonce}, "post[]": {itoa64(sale.ID)},
		"change_regular_price": {"2"}, "_regular_price": {"10%"}, "_featured": {"no"}}
	if code, _, loc := get(t, c, base+"/wp-admin/edit?"+q.Encode()); code != http.StatusFound || !strings.Contains(loc, "updated=1") {
		t.Fatalf("bulk edit: %d %s", code, loc)
	}
	p, _ := srv.gooGetProduct(ctx, sale.ID)
	if p.RegularPrice == nil || *p.RegularPrice != 55 || p.Featured {
		t.Errorf("bulk edit result: price %v featured %v", p.RegularPrice, p.Featured)
	}
	// Choosing Edit + Apply opens the Bulk Edit panel.
	q = url.Values{"post_type": {"product"}, "action": {"edit"}, "bulk_action": {"Apply"}, "_wpnonce": {nonce}, "post[]": {itoa64(sale.ID)}}
	if _, body, _ = get(t, c, base+"/wp-admin/edit?"+q.Encode()); !strings.Contains(body, `id="bulk-edit"`) || !strings.Contains(body, "change_regular_price") {
		t.Error("the Bulk Edit panel is missing")
	}
}

func TestGooCatalogEditorSimple(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	_ = srv.gooSetOpts(ctx, map[string]any{"manage_stock": true})
	cat := gooCatTerm(t, srv, "product_cat", "Clothing")
	ship := gooCatTerm(t, srv, "product_shipping_class", "Bulky")
	other := gooMakeSimple(t, srv, "Other", 5, func(p *gooProduct) { p.SKU = "TAKEN" })

	id, nonce, body := gooCatEditor(t, c, base+"/wp-admin/post-new?post_type=product")
	for _, want := range []string{`id="woocommerce-product-data"`, `name="product-type"`, "Simple product", "Variable product", "External/Affiliate product",
		"Grouped product", "general_product_data", "inventory_product_data", "shipping_product_data", "linked_product_data", "product_attributes",
		"variable_product_options", "advanced_product_data", "Product short description", "Product categories", "Product tags", "Product image",
		"Product gallery", "Catalog visibility", "This is a featured product", "_sale_price_dates_from", "Downloadable files", "No shipping class",
		"Bulky", "Clothing", "Upsells", "Cross-sells", "Grouped products", "Enable reviews", "Purchase note", "wp-editor-area", "catalog.js"} {
		if !strings.Contains(body, want) {
			t.Errorf("editor lacks %q", want)
		}
	}
	form := url.Values{"post_title": {"Linen Shirt"}, "content": {"<p>Soft linen.</p>"}, "excerpt": {"Short one"}, "publish": {"Publish"},
		"product-type": {"simple"}, "_regular_price": {"30"}, "_sale_price": {"25"}, "_sale_price_dates_to": {"2099-12-31"},
		"_sku": {"LIN-1"}, "_manage_stock": {"yes"}, "_stock": {"7"}, "_backorders": {"notify"}, "_low_stock_amount": {"2"},
		"_weight": {"0.4"}, "_length": {"30"}, "_width": {"20"}, "_height": {"2"}, "product_shipping_class": {itoa64(ship.ID)},
		"_tax_status": {"taxable"}, "_tax_class": {""}, "upsell_ids[]": {itoa64(other.ID)}, "crosssell_ids[]": {itoa64(other.ID)},
		"_purchase_note": {"Thanks!"}, "menu_order": {"3"}, "comment_status": {"open"}, "_visibility": {"catalog"}, "_featured": {"on"},
		"tax_input[product_cat][]": {itoa64(cat.ID), "new:" + itoa64(cat.ID) + ":Shirts"}, "tax_input[product_tag]": {"summer, linen"},
		"_downloadable": {"on"}, "_wc_file_names[]": {"Guide"}, "_wc_file_urls[]": {"https://example.com/guide.pdf"}, "_wc_file_hashes[]": {""},
		"_download_limit": {"3"}, "_download_expiry": {""}, "goo_attributes_loaded": {"1"},
		"attribute_names[0]": {"Material"}, "attribute_values[0]": {"Linen | Cotton"}, "attribute_visibility[0]": {"1"}}
	loc := gooCatSave(t, c, base, id, nonce, form)
	if !strings.Contains(loc, "message=6") {
		t.Errorf("publish redirect: %s", loc)
	}
	p, err := srv.gooGetProduct(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case p.Name != "Linen Shirt" || p.Status != "publish" || p.Type != gooTypeSimple:
		t.Errorf("post: %q %s %s", p.Name, p.Status, p.Type)
	case p.Post.Content != "<p>Soft linen.</p>" || p.Post.Excerpt != "Short one":
		t.Errorf("descriptions: %q %q", p.Post.Content, p.Post.Excerpt)
	case p.RegularPrice == nil || *p.RegularPrice != 30 || p.SalePrice == nil || *p.SalePrice != 25 || p.SaleTo == nil || p.Price == nil || *p.Price != 25:
		t.Errorf("prices: %v %v %v %v", p.RegularPrice, p.SalePrice, p.SaleTo, p.Price)
	case p.SKU != "LIN-1" || !p.ManageStock || p.StockQuantity == nil || *p.StockQuantity != 7 || p.Backorders != "notify" || p.LowStockAmount == nil:
		t.Errorf("inventory: %s %v %v %s", p.SKU, p.ManageStock, p.StockQuantity, p.Backorders)
	case p.Weight != "0.4" || p.Length != "30" || p.ShippingClassID != ship.ID:
		t.Errorf("shipping: %s %s %d", p.Weight, p.Length, p.ShippingClassID)
	case len(p.UpsellIDs) != 1 || p.UpsellIDs[0] != other.ID || len(p.CrossSellIDs) != 1:
		t.Errorf("linked: %v %v", p.UpsellIDs, p.CrossSellIDs)
	case p.PurchaseNote != "Thanks!" || p.Post.MenuOrder != 3 || p.Post.CommentStatus != "open":
		t.Errorf("advanced: %q %d %s", p.PurchaseNote, p.Post.MenuOrder, p.Post.CommentStatus)
	case p.CatalogVisibility != "catalog" || !p.Featured:
		t.Errorf("catalog visibility: %s %v", p.CatalogVisibility, p.Featured)
	case !p.Downloadable || len(p.Downloads) != 1 || p.Downloads[0].Name != "Guide" || p.Downloads[0].ID == "" || p.DownloadLimit != 3 || p.DownloadExpiry != -1:
		t.Errorf("downloads: %+v %d %d", p.Downloads, p.DownloadLimit, p.DownloadExpiry)
	case len(p.Attributes) != 1 || p.Attributes[0].Name != "Material" || len(p.Attributes[0].Options) != 2 || !p.Attributes[0].Visible:
		t.Errorf("attributes: %+v", p.Attributes)
	}
	cats, _ := srv.store.TermsForPosts(ctx, []int64{id}, "product_cat")
	tags, _ := srv.store.TermsForPosts(ctx, []int64{id}, "product_tag")
	if len(cats[id]) != 2 || len(tags[id]) != 2 {
		t.Errorf("terms: %d categories, %d tags", len(cats[id]), len(tags[id]))
	}
	if sh, err := srv.store.TermByName(ctx, "product_cat", "Shirts"); err != nil || sh.ParentID != cat.ID {
		t.Errorf("new category: %v %+v", err, sh)
	}

	// The edit screen shows the saved values and the update notice.
	_, nonce, body = gooCatEditor(t, c, base+"/wp-admin/post?post="+itoa64(id)+"&action=edit&message=1")
	for _, want := range []string{"Product updated.", "View Product", `value="LIN-1"`, `value="30"`, "Guide", "Other (TAKEN)", "Linen | Cotton"} {
		if !strings.Contains(body, want) {
			t.Errorf("edit screen lacks %q", want)
		}
	}

	// A taken SKU is refused with WooCommerce's notice; the rest saves.
	form.Set("_sku", "TAKEN")
	form.Set("_regular_price", "31")
	form.Del("publish")
	form.Set("save", "Update")
	loc = gooCatSave(t, c, base, id, nonce, form)
	if !strings.Contains(loc, "goo_error=sku") {
		t.Errorf("duplicate SKU redirect: %s", loc)
	}
	p, _ = srv.gooGetProduct(ctx, id)
	if p.SKU != "LIN-1" || *p.RegularPrice != 31 {
		t.Errorf("after duplicate SKU: %s %v", p.SKU, *p.RegularPrice)
	}
	if _, body, _ := get(t, c, base+loc); !strings.Contains(body, "Invalid or duplicated SKU.") {
		t.Error("no duplicated SKU notice")
	}

	// A bad nonce is refused.
	if code, _, _ := post(t, c, base+"/wp-admin/post", url.Values{"post_ID": {itoa64(id)}, "_wpnonce": {"bad"}, "post_title": {"x"}}); code != http.StatusForbidden {
		t.Errorf("bad nonce save: %d", code)
	}
}

func TestGooCatalogEditorVariableGroupedExternal(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	_ = srv.gooSetOpts(ctx, map[string]any{"manage_stock": true})
	color := &gooAttribute{Name: "Color", Slug: "color"}
	if err := srv.gooSaveAttribute(ctx, color); err != nil {
		t.Fatal(err)
	}
	red := gooCatTerm(t, srv, "pa_color", "Red")
	blue := gooCatTerm(t, srv, "pa_color", "Blue")

	id, nonce, _ := gooCatEditor(t, c, base+"/wp-admin/post-new?post_type=product")
	form := url.Values{"post_title": {"Tee"}, "product-type": {"variable"}, "_sku": {"TEE"}, "goo_attributes_loaded": {"1"},
		"attribute_names[0]": {"pa_color"}, "attribute_values[0][]": {itoa64(red.ID), itoa64(blue.ID)}, "attribute_visibility[0]": {"1"}, "attribute_variation[0]": {"1"}, "attribute_position[0]": {"0"},
		"attribute_names[1]": {"Size"}, "attribute_values[1]": {"S | M"}, "attribute_variation[1]": {"1"}, "attribute_position[1]": {"1"},
		"goo_generate_variations": {"1"}}
	loc := gooCatSave(t, c, base, id, nonce, form)
	if !strings.Contains(loc, "goo_variations_added=4") || !strings.Contains(loc, "goo_tab=variations") {
		t.Fatalf("generate redirect: %s", loc)
	}
	vars, _ := srv.gooVariations(ctx, id, true)
	if len(vars) != 4 {
		t.Fatalf("%d variations generated", len(vars))
	}
	if p, _ := srv.gooGetProduct(ctx, id); p.Status != "draft" || len(p.Attributes) != 2 || p.Attributes[0].Taxonomy != "pa_color" || !p.Attributes[1].Variation {
		t.Errorf("parent after generate: %s %+v", p.Status, p.Attributes)
	}
	// Generating again adds nothing.
	_, nonce, body := gooCatEditor(t, c, base+loc)
	for _, want := range []string{"4 variations added", "variable_post_id[3]", "Any Color&hellip;", "Default Form Values", "Generate variations", "Add manually"} {
		if !strings.Contains(body, want) {
			t.Errorf("variations tab lacks %q", want)
		}
	}
	// Prices, stock and enable state per variation; one removed; a default value.
	form.Del("goo_generate_variations")
	form.Set("publish", "Publish")
	form.Set("goo_variations_loaded", "1")
	form.Set("default_attribute_pa_color", "red")
	for i, v := range vars {
		k := func(n string) string { return n + "[" + strconv.Itoa(i) + "]" }
		form.Set(k("variable_post_id"), itoa64(v.ID))
		form.Set(k("attribute_pa_color"), v.VariationAttrs["pa_color"])
		form.Set(k("attribute_size"), v.VariationAttrs["size"])
		form.Set(k("variable_regular_price"), strconv.Itoa(10+i))
		form.Set(k("variable_tax_class"), "parent")
		form.Set(k("variable_enabled"), "on")
		if i == 0 {
			form.Set(k("variable_sku"), "TEE-RED-S")
			form.Set(k("variable_manage_stock"), "on")
			form.Set(k("variable_stock"), "5")
			form.Set(k("variable_sale_price"), "8")
		}
	}
	form.Add("goo_delete_variations[]", itoa64(vars[3].ID))
	loc = gooCatSave(t, c, base, id, nonce, form)
	if !strings.Contains(loc, "message=6") {
		t.Errorf("publish variable: %s", loc)
	}
	p, _ := srv.gooGetProduct(ctx, id)
	vars, _ = srv.gooVariations(ctx, id, true)
	if len(vars) != 3 {
		t.Fatalf("%d variations after removing one", len(vars))
	}
	if p.Status != "publish" || p.MinPrice == nil || *p.MinPrice != 8 || p.MaxPrice == nil || *p.MaxPrice != 12 || p.DefaultAttributes["pa_color"] != "red" {
		t.Errorf("variable parent: %s %v %v %v", p.Status, p.MinPrice, p.MaxPrice, p.DefaultAttributes)
	}
	if v := vars[0]; v.SKU != "TEE-RED-S" || !v.ManageStock || v.StockQuantity == nil || *v.StockQuantity != 5 || v.Status != "publish" || v.TaxClass != "parent" {
		t.Errorf("variation 0: %+v", v)
	}
	// Bulk action: set regular prices.
	_, nonce, _ = gooCatEditor(t, c, base+"/wp-admin/post?post="+itoa64(id)+"&action=edit")
	bulk := url.Values{"post_title": {"Tee"}, "product-type": {"variable"}, "save": {"Update"}, "variable_bulk_action": {"variable_regular_price"},
		"variable_bulk_value": {"20"}, "goo_variation_bulk": {"1"}}
	gooCatSave(t, c, base, id, nonce, bulk)
	vars, _ = srv.gooVariations(ctx, id, true)
	for _, v := range vars {
		if v.RegularPrice == nil || *v.RegularPrice != 20 {
			t.Errorf("bulk price of %d: %v", v.ID, v.RegularPrice)
		}
	}
	if p, _ := srv.gooGetProduct(ctx, id); len(p.Attributes) != 2 {
		t.Errorf("a save without the Attributes tab dropped the attributes: %+v", p.Attributes)
	}

	// Grouped.
	kid1 := gooMakeSimple(t, srv, "Kid one", 3, nil)
	kid2 := gooMakeSimple(t, srv, "Kid two", 7, nil)
	gid, nonce, _ := gooCatEditor(t, c, base+"/wp-admin/post-new?post_type=product")
	gooCatSave(t, c, base, gid, nonce, url.Values{"post_title": {"Bundle"}, "product-type": {"grouped"}, "publish": {"Publish"},
		"grouped_products[]": {itoa64(kid1.ID), itoa64(kid2.ID)}})
	g, _ := srv.gooGetProduct(ctx, gid)
	if g.Type != gooTypeGrouped || len(g.Children) != 2 || g.MinPrice == nil || *g.MinPrice != 3 || *g.MaxPrice != 7 {
		t.Errorf("grouped: %s %v %v %v", g.Type, g.Children, g.MinPrice, g.MaxPrice)
	}

	// External.
	eid, nonce, _ := gooCatEditor(t, c, base+"/wp-admin/post-new?post_type=product")
	gooCatSave(t, c, base, eid, nonce, url.Values{"post_title": {"Affiliate"}, "product-type": {"external"}, "publish": {"Publish"},
		"_regular_price": {"99"}, "_product_url": {"https://shop.example.com/x"}, "_button_text": {"Buy there"}})
	e, _ := srv.gooGetProduct(ctx, eid)
	if e.Type != gooTypeExternal || e.ProductURL != "https://shop.example.com/x" || e.ButtonText != "Buy there" || *e.RegularPrice != 99 {
		t.Errorf("external: %+v", e)
	}

	// The list shows the variable range.
	_, body, _ = get(t, c, base+"/wp-admin/edit?post_type=product")
	if !strings.Contains(body, "price-range") {
		t.Error("no price range for the variable product")
	}

	// Duplicate the variable product: a draft "(Copy)" with its variations.
	dup := gooCatHref(t, body, `href="([^"]*action=duplicate_product&amp;post=`+itoa64(id)+`[^"]*)"`)
	code, _, loc := get(t, c, base+dup)
	if code != http.StatusFound {
		t.Fatalf("duplicate: %d", code)
	}
	nid, _ := strconv.ParseInt(regexp.MustCompile(`post=(\d+)`).FindStringSubmatch(loc)[1], 10, 64)
	d, err := srv.gooGetProduct(ctx, nid)
	if err != nil || d.Name != "Tee (Copy)" || d.Status != "draft" || d.SKU == "TEE" || d.SKU == "" {
		t.Fatalf("duplicate: %v %+v", err, d)
	}
	if dv, _ := srv.gooVariations(ctx, nid, true); len(dv) != 3 {
		t.Errorf("duplicate has %d variations", len(dv))
	}

	// Trash and restore from the row actions.
	trash := gooCatHref(t, body, `href="([^"]*post=`+itoa64(eid)+`&amp;action=trash[^"]*)"`)
	if code, _, loc := get(t, c, base+trash); code != http.StatusFound || !strings.Contains(loc, "trashed=1") {
		t.Fatalf("trash: %d %s", code, loc)
	}
	if e, _ := srv.gooGetProduct(ctx, eid); e.Status != "trash" {
		t.Errorf("status after trash: %s", e.Status)
	}
	_, body, _ = get(t, c, base+"/wp-admin/edit?post_type=product&post_status=trash")
	restore := gooCatHref(t, body, `href="([^"]*post=`+itoa64(eid)+`&amp;action=untrash[^"]*)"`)
	if code, _, _ := get(t, c, base+restore); code != http.StatusFound {
		t.Fatalf("restore: %d", code)
	}
	if e, _ := srv.gooGetProduct(ctx, eid); e.Status != "draft" {
		t.Errorf("status after restore: %s", e.Status)
	}
	// Bulk trash and delete permanently.
	nonceList := gooCatHref(t, body, `name="_wpnonce" value="([^"]+)"`)
	q := url.Values{"post_type": {"product"}, "action": {"trash"}, "bulk_action": {"Apply"}, "_wpnonce": {nonceList}, "post[]": {itoa64(nid)}}
	get(t, c, base+"/wp-admin/edit?"+q.Encode())
	q.Set("action", "delete")
	if code, _, loc := get(t, c, base+"/wp-admin/edit?"+q.Encode()); code != http.StatusFound || !strings.Contains(loc, "deleted=1") {
		t.Fatalf("bulk delete: %d %s", code, loc)
	}
	if _, err := srv.gooGetProduct(ctx, nid); err == nil {
		t.Error("the deleted copy still exists")
	}
	var orphans int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE parent_id = $1`, nid).Scan(&orphans)
	if orphans != 0 {
		t.Errorf("%d variations left after deleting their parent", orphans)
	}
}

// gooCatImport runs the importer (upload → mapping → import) and returns the done screen.
func gooCatImport(t *testing.T, c *http.Client, base string, data []byte, update bool) string {
	t.Helper()
	_, page, _ := get(t, c, base+"/wp-admin/edit?post_type=product&page=product_importer")
	nonce := formNonce(t, page)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("import", "products.csv")
	_, _ = fw.Write(data)
	_ = mw.WriteField("_wpnonce", nonce)
	_ = mw.WriteField("step", "mapping")
	_ = mw.WriteField("delimiter", ",")
	if update {
		_ = mw.WriteField("update_existing", "1")
	}
	mw.Close()
	resp, err := c.Post(base+"/wp-admin/goocommerce?page=product_importer", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	body := string(b)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Map CSV fields to products") {
		t.Fatalf("import mapping: %d %.500s", resp.StatusCode, body)
	}
	form := url.Values{"_wpnonce": {formNonce(t, body)}, "step": {"import"}, "delimiter": {","},
		"file": {gooCatHref(t, body, `name="file" value="([^"]+)"`)}}
	if update {
		form.Set("update_existing", "1")
	}
	for _, m := range regexp.MustCompile(`(?s)<select name="map_to\[(\d+)\]">(.*?)</select>`).FindAllStringSubmatch(body, -1) {
		sel := regexp.MustCompile(`<option value="([^"]*)" selected="selected">`).FindStringSubmatch(m[2])
		if sel != nil {
			form.Set("map_to["+m[1]+"]", sel[1])
		} else {
			form.Set("map_to["+m[1]+"]", "")
		}
	}
	code, done, _ := post(t, c, base+"/wp-admin/goocommerce?page=product_importer", form)
	if code != http.StatusOK || !strings.Contains(done, "Import complete!") {
		t.Fatalf("import: %d %.800s", code, done)
	}
	return done
}

func TestGooCatalogCSV(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	_ = srv.gooSetOpts(ctx, map[string]any{"manage_stock": true})
	if err := srv.gooSaveAttribute(ctx, &gooAttribute{Name: "Color", Slug: "color"}); err != nil {
		t.Fatal(err)
	}
	red, blue := gooCatTerm(t, srv, "pa_color", "Red"), gooCatTerm(t, srv, "pa_color", "Blue")
	parentCat := gooCatTerm(t, srv, "product_cat", "Apparel")
	sub := &store.Term{Taxonomy: "product_cat", Name: "Shirts, Tees", ParentID: parentCat.ID}
	_ = srv.store.InsertTerm(ctx, sub)

	simple := gooMakeSimple(t, srv, "Mug", 12.5, func(p *gooProduct) {
		p.SKU, p.ManageStock, p.StockQuantity, p.SalePrice, p.Weight = "MUG", true, gooIntPtr(4), gooFloatPtr(10), "0.3"
	})
	_ = srv.store.SetPostTerms(ctx, simple.ID, "product_cat", []int64{sub.ID})
	tagIDs, _ := srv.store.TermIDsByNames(ctx, "product_tag", []string{"kitchen"})
	_ = srv.store.SetPostTerms(ctx, simple.ID, "product_tag", tagIDs)
	v := gooNewProduct(gooTypeVariable)
	v.Name, v.SKU = "Polo", "POLO"
	v.Attributes = []gooProductAttr{{Name: "Color", Taxonomy: "pa_color", Options: []string{red.Slug, blue.Slug}, Visible: true, Variation: true},
		{Name: "Fit", Options: []string{"Slim", "Regular"}, Visible: true}}
	v.UpsellIDs = []int64{simple.ID}
	if err := srv.gooSaveProduct(ctx, v, 1); err != nil {
		t.Fatal(err)
	}
	for i, col := range []string{red.Slug, blue.Slug} {
		vv := srv.gooNewVariation(ctx, v, map[string]string{"pa_color": col}, i)
		vv.RegularPrice, vv.SKU = gooFloatPtr(float64(20+i)), "POLO-"+strings.ToUpper(col)
		if err := srv.gooSaveProduct(ctx, vv, 1); err != nil {
			t.Fatal(err)
		}
	}

	// Export.
	_, page, _ := get(t, c, base+"/wp-admin/edit?post_type=product&page=product_exporter")
	if !strings.Contains(page, "Generate CSV") {
		t.Fatal("no exporter form")
	}
	nonce := formNonce(t, page)
	code, data, _ := get(t, c, base+"/wp-admin/edit?post_type=product&page=product_exporter&action=download_product_csv&_wpnonce="+nonce)
	if code != http.StatusOK {
		t.Fatalf("export: %d %.300s", code, data)
	}
	rows, err := gooReadCSV([]byte(data), ",")
	if err != nil || len(rows) != 5 {
		t.Fatalf("export rows: %v %d\n%s", err, len(rows), data)
	}
	header := strings.Join(rows[0], "|")
	for _, want := range []string{"ID", "Type", "SKU", "Name", "Published", "Is featured?", "Visibility in catalog", "Short description", "Description",
		"Date sale price starts", "Date sale price ends", "Tax status", "Tax class", "In stock?", "Stock", "Low stock amount", "Backorders allowed?",
		"Sold individually?", "Weight (kg)", "Length (cm)", "Width (cm)", "Height (cm)", "Allow customer reviews?", "Purchase note", "Sale price",
		"Regular price", "Categories", "Tags", "Shipping class", "Images", "Download limit", "Download expiry days", "Parent", "Grouped products",
		"Upsells", "Cross-sells", "External URL", "Button text", "Position", "Attribute 1 name", "Attribute 1 value(s)", "Attribute 1 visible",
		"Attribute 1 global", "Attribute 2 name"} {
		if !strings.Contains("|"+header+"|", "|"+want+"|") {
			t.Errorf("export header lacks %q", want)
		}
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	var mugRow, varRow []string
	for _, r := range rows[1:] {
		switch r[col["SKU"]] {
		case "MUG":
			mugRow = r
		case "POLO-RED":
			varRow = r
		}
	}
	if mugRow == nil || mugRow[col["Categories"]] != `Apparel > Shirts\, Tees` || mugRow[col["Stock"]] != "4" || mugRow[col["Sale price"]] != "10" || mugRow[col["Tags"]] != "kitchen" {
		t.Errorf("mug row: %v", mugRow)
	}
	if varRow == nil || varRow[col["Type"]] != "variation" || varRow[col["Parent"]] != "POLO" || varRow[col["Attribute 1 value(s)"]] != "Red" {
		t.Errorf("variation row: %v", varRow)
	}

	// Update through the importer: new prices.
	mugRow[col["Regular price"]] = "15"
	varRow[col["Regular price"]] = "33"
	var out bytes.Buffer
	w := csv.NewWriter(&out)
	_ = w.WriteAll(rows)
	done := gooCatImport(t, c, base, out.Bytes(), true)
	if !strings.Contains(done, "4 products updated") {
		t.Errorf("update import: %.600s", done)
	}
	if p, _ := srv.gooGetProduct(ctx, simple.ID); *p.RegularPrice != 15 || *p.StockQuantity != 4 {
		t.Errorf("mug after update: %v %v", *p.RegularPrice, *p.StockQuantity)
	}

	// Re-create everything from the file into an empty catalog.
	for _, id := range []int64{simple.ID, v.ID} {
		p, _ := srv.gooGetProduct(ctx, id)
		_ = srv.gooTrashAction(ctx, p, "delete")
	}
	done = gooCatImport(t, c, base, out.Bytes(), false)
	if !strings.Contains(done, "4 products imported") {
		t.Fatalf("create import: %.800s", done)
	}
	list, _, _ := srv.gooQueryProducts(ctx, gooProductQuery{SKU: "POLO", Status: []string{"publish"}})
	if len(list) != 1 {
		t.Fatalf("imported POLO: %d", len(list))
	}
	polo := list[0]
	pv, _ := srv.gooVariations(ctx, polo.ID, true)
	if polo.Type != gooTypeVariable || len(pv) != 2 || len(polo.Attributes) != 2 || !polo.Attributes[0].Variation || polo.Attributes[1].Variation {
		t.Errorf("imported polo: %s %d vars %+v", polo.Type, len(pv), polo.Attributes)
	}
	prices := map[string]float64{}
	for _, x := range pv {
		prices[x.VariationAttrs["pa_color"]] = *x.RegularPrice
	}
	if prices["red"] != 33 || prices["blue"] != 21 {
		t.Errorf("imported variation prices: %v", prices)
	}
	mugs, _, _ := srv.gooQueryProducts(ctx, gooProductQuery{SKU: "MUG"})
	if len(mugs) != 1 || *mugs[0].RegularPrice != 15 || *mugs[0].SalePrice != 10 || mugs[0].Weight != "0.3" {
		t.Fatalf("imported mug: %+v", mugs)
	}
	if len(polo.UpsellIDs) != 1 || polo.UpsellIDs[0] != mugs[0].ID {
		t.Errorf("imported upsells: %v (mug %d)", polo.UpsellIDs, mugs[0].ID)
	}
	cats, _ := srv.store.TermsForPosts(ctx, []int64{mugs[0].ID}, "product_cat")
	if len(cats[mugs[0].ID]) != 1 || cats[mugs[0].ID][0].ID != sub.ID {
		t.Errorf("imported category: %+v", cats[mugs[0].ID])
	}
	// Importing the same file again without "update" skips existing SKUs.
	if done = gooCatImport(t, c, base, out.Bytes(), false); !strings.Contains(done, "4 products were skipped") {
		t.Errorf("re-import: %.600s", done)
	}
}

func TestGooCatalogAttributes(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	u := base + "/wp-admin/goocommerce?page=attributes"
	code, body, _ := get(t, c, u)
	if code != http.StatusOK || !strings.Contains(body, "Add new attribute") || !strings.Contains(body, "Enable archives?") || !strings.Contains(body, "Default sort order") {
		t.Fatalf("attributes screen: %d %.300s", code, body)
	}
	code, _, loc := post(t, c, u, url.Values{"_wpnonce": {formNonce(t, body)}, "attribute_label": {"Size"}, "attribute_name": {""},
		"attribute_orderby": {"name"}, "attribute_public": {"1"}, "add_new_attribute": {"Add attribute"}})
	if code != http.StatusFound || !strings.Contains(loc, "added=1") {
		t.Fatalf("add attribute: %d %s", code, loc)
	}
	a := srv.gooAttributeByTaxonomy(ctx, "pa_size")
	if a == nil || a.Name != "Size" || a.OrderBy != "name" || !a.HasArchives {
		t.Fatalf("attribute: %+v", a)
	}
	gooCatTerm(t, srv, "pa_size", "Large")
	_, body, _ = get(t, c, u+"&added=1")
	for _, want := range []string{"Attribute added successfully", "Size", "Large", "Configure terms", "edit-tags?taxonomy=pa_size&amp;post_type=product"} {
		if !strings.Contains(body, want) {
			t.Errorf("attributes table lacks %q", want)
		}
	}
	// Reserved slugs are refused.
	code, body, _ = post(t, c, u, url.Values{"_wpnonce": {formNonce(t, body)}, "attribute_label": {"Type"}, "attribute_name": {"type"}, "add_new_attribute": {"1"}})
	if code != http.StatusOK || !strings.Contains(body, "reserved term") {
		t.Errorf("reserved slug: %d", code)
	}
	// Edit.
	_, body, _ = get(t, c, u+"&edit="+itoa64(a.ID))
	if !strings.Contains(body, "Edit attribute") {
		t.Fatal("no edit form")
	}
	code, _, loc = post(t, c, u+"&edit="+itoa64(a.ID), url.Values{"_wpnonce": {formNonce(t, body)}, "attribute_label": {"Shoe size"}, "attribute_name": {"shoe-size"},
		"attribute_orderby": {"id"}, "save_attribute": {"Update"}})
	if code != http.StatusFound || !strings.Contains(loc, "updated=1") {
		t.Fatalf("edit attribute: %d %s", code, loc)
	}
	if a2 := srv.gooAttributeByTaxonomy(ctx, "pa_shoe-size"); a2 == nil || a2.Name != "Shoe size" {
		t.Fatalf("renamed attribute: %+v", a2)
	}
	if terms, _ := srv.gooAttributeTerms(ctx, "pa_shoe-size"); len(terms) != 1 {
		t.Errorf("terms moved with the rename: %d", len(terms))
	}
	// Delete.
	_, body, _ = get(t, c, u)
	del := gooCatHref(t, body, `href="([^"]*delete=`+itoa64(a.ID)+`[^"]*)"`)
	if code, _, loc := get(t, c, base+del); code != http.StatusFound || !strings.Contains(loc, "deleted=1") {
		t.Fatalf("delete attribute: %d %s", code, loc)
	}
	if srv.gooAttributeByTaxonomy(ctx, "pa_shoe-size") != nil {
		t.Error("attribute still exists")
	}
}

func TestGooCatalogReviews(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Rated thing", 10, nil)
	var ids []int64
	for i, r := range []int{5, 3, 4} {
		cm := &store.Comment{PostID: p.ID, AuthorName: "Buyer " + strconv.Itoa(i), AuthorEmail: "b" + strconv.Itoa(i) + "@example.com",
			Content: "Review " + strconv.Itoa(i), Type: "review", Status: "pending", Meta: map[string]any{"rating": r, "verified": i == 0}}
		if err := srv.store.InsertComment(ctx, cm); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, cm.ID)
	}
	u := base + "/wp-admin/goocommerce?page=reviews"
	code, body, _ := get(t, c, u)
	if code != http.StatusOK {
		t.Fatalf("reviews: %d %.300s", code, body)
	}
	for _, want := range []string{"Review 0", "Rated thing", "Rated 5 out of 5", "verified owner", "Pending <span class=\"count\">(<span class=\"pending-count\">3",
		"All ratings", "Filter by product type", "Approve", "column-rating", "Submitted on"} {
		if !strings.Contains(body, want) {
			t.Errorf("reviews screen lacks %q", want)
		}
	}
	// Approve one from its row action.
	approve := gooCatHref(t, body, `href="([^"]*action=approve&amp;c=`+itoa64(ids[0])+`[^"]*)"`)
	if code, _, loc := get(t, c, base+approve); code != http.StatusFound || !strings.Contains(loc, "approved=1") {
		t.Fatalf("approve: %d %s", code, loc)
	}
	got, _ := srv.gooGetProduct(ctx, p.ID)
	if got.RatingCount != 1 || got.AverageRating != 5 {
		t.Errorf("rating after one approval: %v / %d", got.AverageRating, got.RatingCount)
	}
	// Bulk approve the rest → average (5+3+4)/3.
	nonce := gooCatHref(t, body, `name="_wpnonce" value="([^"]+)"`)
	q := url.Values{"page": {"reviews"}, "action": {"approve"}, "bulk_action": {"Apply"}, "_wpnonce": {nonce}, "delete_comments[]": {itoa64(ids[1]), itoa64(ids[2])}}
	if code, _, loc := get(t, c, base+"/wp-admin/goocommerce?"+q.Encode()); code != http.StatusFound || !strings.Contains(loc, "approved=2") {
		t.Fatalf("bulk approve: %d %s", code, loc)
	}
	got, _ = srv.gooGetProduct(ctx, p.ID)
	gooAlmost(t, "average rating", got.AverageRating, 4)
	if got.RatingCount != 3 {
		t.Errorf("rating count %d", got.RatingCount)
	}
	// Rating filter.
	_, body, _ = get(t, c, u+"&review_rating=3")
	if !strings.Contains(body, "Review 1") || strings.Contains(body, "Review 0") {
		t.Error("rating filter")
	}
	// Spam one → average recalculated.
	q.Set("action", "spam")
	q["delete_comments[]"] = []string{itoa64(ids[1])}
	get(t, c, base+"/wp-admin/goocommerce?"+q.Encode())
	got, _ = srv.gooGetProduct(ctx, p.ID)
	gooAlmost(t, "average after spam", got.AverageRating, 4.5)
}

func TestGooCatalogPluginOff(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Hidden", 1, nil)
	if err := srv.gooDeactivate(ctx); err != nil {
		t.Fatal(err)
	}
	ginCtx := gooTestCtx()
	ginCtx.Request.URL.RawQuery = "post_type=product"
	if srv.gooAdminScreen(ginCtx, "edit") || srv.gooAdminScreen(ginCtx, "post-new") {
		t.Error("gooAdminScreen served a request while the plugin is off")
	}
	// The core screens behave as before: products are not a core list type.
	if code, body, _ := get(t, c, base+"/wp-admin/edit?post_type=product"); code != http.StatusBadRequest || strings.Contains(body, "woocommerce") {
		t.Errorf("products list while off: %d", code)
	}
	if code, body, _ := get(t, c, base+"/wp-admin/post?post="+itoa64(p.ID)+"&action=edit"); strings.Contains(body, "woocommerce-product-data") {
		t.Errorf("product editor while off: %d", code)
	}
	if code, _, _ := get(t, c, base+"/wp-admin/edit"); code != http.StatusOK {
		t.Errorf("posts list while off: %d", code)
	}
	if code, _, _ := get(t, c, base+"/wp-admin/goocommerce?page=attributes"); code != http.StatusNotFound {
		t.Errorf("attributes while off: %d", code)
	}
}
