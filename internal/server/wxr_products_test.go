package server

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"wpgo/internal/store"
)

func TestPHPUnserialize(t *testing.T) {
	v, err := phpUnserialize(`a:3:{s:8:"pa_color";a:3:{s:4:"name";s:8:"pa_color";s:11:"is_taxonomy";i:1;s:5:"value";s:0:"";}i:7;b:1;s:1:"d";d:1.5;}`)
	if err != nil {
		t.Fatal(err)
	}
	arr := v.(*phpArray)
	if len(arr.Keys) != 3 || phpString(arr.Get("7")) != "1" || phpString(arr.Get("d")) != "1.5" {
		t.Fatalf("array: %+v", arr)
	}
	color := arr.Get("pa_color").(*phpArray)
	if phpString(color.Get("name")) != "pa_color" || !phpTruthy(color.Get("is_taxonomy")) || phpString(color.Get("value")) != "" {
		t.Errorf("nested: %+v", color)
	}
	// Byte lengths: a UTF-8 string is counted in bytes, as PHP does.
	if v, err := phpUnserialize(`s:7:"Größe";`); err != nil || v != "Größe" {
		t.Errorf("utf-8 string: %v %v", v, err)
	}
	if v, err := phpUnserialize(`O:8:"stdClass":1:{s:1:"a";N;}`); err != nil || v.(*phpArray).Get("a") != nil {
		t.Errorf("object: %v %v", v, err)
	}
	for _, bad := range []string{"", "x", `s:9:"short";`, `a:2:{i:0;i:1;}`, `a:1:{a:0:{}i:1;}`, `i:abc;`, strings.Repeat("a:1:{i:0;", 40) + "N;" + strings.Repeat("}", 40)} {
		if _, err := phpUnserialize(bad); err == nil {
			t.Errorf("%q must not parse", bad)
		}
	}
	if got := phpIDs(`a:2:{i:0;i:41;i:1;s:2:"42";}`); !slices.Equal(got, []int64{41, 42}) {
		t.Errorf("serialized ids: %v", got)
	}
	if got := phpIDs("32, 33,,x"); !slices.Equal(got, []int64{32, 33}) {
		t.Errorf("comma ids: %v", got)
	}
	if n := wxrMetaInt("12.000000"); n == nil || *n != 12 {
		t.Errorf("stock: %v", n)
	}
	if wxrMetaFloat("") != nil || wxrMetaFloat("abc") != nil || *wxrMetaFloat("19.90") != 19.9 {
		t.Errorf("prices")
	}
	if d := wxrMetaTime("1577836800", time.UTC); d == nil || d.Year() != 2020 {
		t.Errorf("sale date: %v", d)
	}
	dl := wxrDownloads(`a:2:{s:1:"a";a:3:{s:4:"name";s:1:"A";s:4:"file";s:22:"https://x.test/old.zip";s:7:"enabled";b:1;}s:1:"b";a:3:{s:4:"name";s:1:"B";s:4:"file";s:5:"b.zip";s:7:"enabled";b:0;}}`,
		strings.NewReplacer("https://x.test/old.zip", "https://new.test/new.zip"))
	if len(dl) != 1 || dl[0].ID != "a" || dl[0].Name != "A" || dl[0].File != "https://new.test/new.zip" {
		t.Errorf("downloads: %+v", dl)
	}
	attrs := []gooProductAttr{{Name: "Shoe Size"}, {Name: "Color", Taxonomy: "pa_color"}}
	if k := wxrAttrKey(attrs, "shoe-size"); k != "shoe-size" {
		t.Errorf("custom key: %q", k)
	}
	if k := wxrAttrKey(attrs, "pa_color"); k != "pa_color" {
		t.Errorf("taxonomy key: %q", k)
	}
}

// A WooCommerce export: products of every type with their prices, stock, attributes, variations,
// terms and images go into Pursae Commerce; the order and the coupon are counted, not imported;
// importing the file again adds nothing, and a missing variation is added back.
func TestWXRImportWooCommerceProducts(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installUsersSite(t, c, ts.URL)
	ctx := context.Background()
	fixture, err := os.ReadFile("testdata/wxr-woocommerce.xml")
	if err != nil {
		t.Fatal(err)
	}

	img := image.NewRGBA(image.Rect(0, 0, 12, 12))
	img.Set(2, 2, color.RGBA{G: 200, A: 255})
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "shop.example.test" && strings.HasPrefix(r.URL.Path, "/wp-content/uploads/2024/03/") && strings.HasSuffix(r.URL.Path, ".png") {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBuf.Bytes())
			return
		}
		http.NotFound(w, r)
	}))
	defer files.Close()
	old := wxrFetchTransport
	wxrFetchTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r2 := r.Clone(r.Context())
		r2.URL.Scheme, r2.URL.Host, r2.Host = "http", files.Listener.Addr().String(), r.URL.Host
		return http.DefaultTransport.RoundTrip(r2)
	})
	t.Cleanup(func() { wxrFetchTransport = old })

	body := wxrImportFlow(t, c, srv, ts.URL, string(fixture), true, nil)
	report := regexp.MustCompile(`<p>Imported[^<]*</p>`).FindString(body)
	for _, want := range []string{"4 media files", "1 comments", "Imported 6 products and 2 variations into Pursae Commerce.", "Skipped 1 orders / 1 coupons."} {
		if !strings.Contains(report, want) {
			t.Errorf("report %q misses %q", report, want)
		}
	}
	for _, want := range []string{"Skipped 1 orders, 1 coupons: WooCommerce orders, coupons and customers are not imported", "Pursae Commerce is not active"} {
		if !strings.Contains(body, want) {
			t.Errorf("report log misses %q", want)
		}
	}
	if strings.Contains(body, "Invalid post type") {
		t.Errorf("WooCommerce items reported as unknown types: %s", body)
	}

	mediaID := func(title string) int64 {
		var id int64
		if err := srv.store.Pool.QueryRow(ctx, `SELECT id FROM media WHERE title = $1`, title).Scan(&id); err != nil {
			t.Fatalf("media %q: %v", title, err)
		}
		return id
	}
	teeImg, hoodieImg, backImg, capImg := mediaID("tee"), mediaID("hoodie"), mediaID("hoodie-back"), mediaID("cap")
	product := func(slug string) *gooProduct {
		t.Helper()
		post, err := srv.store.PostBySlug(ctx, "product", slug)
		if err != nil {
			t.Fatalf("product %s: %v", slug, err)
		}
		p, err := srv.gooGetProduct(ctx, post.ID)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	termSlugs := func(id int64, tax string) []string {
		terms, _ := srv.store.TermsForPosts(ctx, []int64{id}, tax)
		var out []string
		for _, tm := range terms[id] {
			out = append(out, tm.Slug)
		}
		slices.Sort(out)
		return out
	}
	eqf := func(p *float64, want float64) bool { return p != nil && *p == want }

	tee, hoodie, capP, guide, set, socks := product("classic-tee"), product("hoodie"), product("summer-cap"), product("style-guide"), product("tee-and-cap-set"), product("partner-socks")

	// The simple product.
	if tee.Type != gooTypeSimple || !eqf(tee.RegularPrice, 20) || tee.SalePrice != nil || tee.SKU != "TEE-1" || !tee.ManageStock ||
		tee.StockQuantity == nil || *tee.StockQuantity != 15 || tee.StockStatus != "instock" || tee.Weight != "0.2" ||
		tee.Length != "30" || tee.Width != "20" || tee.Height != "2" || tee.TaxStatus != "taxable" || tee.Virtual || tee.Downloadable {
		t.Errorf("simple product data: %+v", tee)
	}
	if !tee.Featured || tee.CatalogVisibility != "visible" || tee.Status != "publish" || tee.Post.MenuOrder != 1 || tee.Post.Excerpt != "Our best seller." {
		t.Errorf("simple product post: featured=%v vis=%q status=%q order=%d", tee.Featured, tee.CatalogVisibility, tee.Status, tee.Post.MenuOrder)
	}
	if tee.ImageID != teeImg || tee.Post.FeaturedMediaID != teeImg {
		t.Errorf("tee image = %d/%d, want %d", tee.ImageID, tee.Post.FeaturedMediaID, teeImg)
	}
	if len(tee.Attributes) != 1 || tee.Attributes[0].Name != "Material" || tee.Attributes[0].Taxonomy != "" ||
		!slices.Equal(tee.Attributes[0].Options, []string{"Cotton", "Linen"}) || !tee.Attributes[0].Visible || tee.Attributes[0].Variation {
		t.Errorf("custom attribute: %+v", tee.Attributes)
	}
	if !slices.Equal(tee.UpsellIDs, []int64{hoodie.ID}) || !slices.Equal(tee.CrossSellIDs, []int64{capP.ID}) {
		t.Errorf("upsells %v / cross-sells %v, want [%d] / [%d]", tee.UpsellIDs, tee.CrossSellIDs, hoodie.ID, capP.ID)
	}
	if got := termSlugs(tee.ID, "product_cat"); !slices.Equal(got, []string{"t-shirts"}) {
		t.Errorf("tee categories: %v", got)
	}
	if got := termSlugs(tee.ID, "product_tag"); !slices.Equal(got, []string{"cotton", "summer"}) {
		t.Errorf("tee tags: %v", got)
	}
	ship, err := srv.store.TermBySlug(ctx, "product_shipping_class", "small-items")
	if err != nil || tee.ShippingClassID != ship.ID {
		t.Errorf("shipping class: %d (%v)", tee.ShippingClassID, err)
	}
	if tee.Post.Meta["custom_badge"] != "New" {
		t.Errorf("custom field not kept: %v", tee.Post.Meta)
	}
	for _, k := range []string{"_regular_price", "_sku", "_stock", "_product_attributes", "_edit_lock", "total_sales", "_price"} {
		if _, ok := tee.Post.Meta[k]; ok {
			t.Errorf("WooCommerce meta %s copied as a custom field", k)
		}
	}
	if strings.Contains(tee.Post.Content, "shop.example.test") {
		t.Errorf("image URL in the description not rewritten: %s", tee.Post.Content)
	}
	var rating string
	var verified string
	if err := srv.store.Pool.QueryRow(ctx, `SELECT meta->>'rating', meta->>'verified' FROM comments WHERE post_id = $1 AND type = 'review'`, tee.ID).Scan(&rating, &verified); err != nil || rating != "4" || verified != "0" {
		t.Errorf("review: rating=%q verified=%q (%v)", rating, verified, err)
	}
	if tee.AverageRating != 4 || tee.RatingCount != 1 {
		t.Errorf("rating = %v / %d", tee.AverageRating, tee.RatingCount)
	}
	var teeImgParent int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT coalesce(post_id,0) FROM media WHERE id = $1`, teeImg).Scan(&teeImgParent)
	if teeImgParent != tee.ID {
		t.Errorf("tee image attached to %d, want %d", teeImgParent, tee.ID)
	}

	// The variable product and its variations.
	if hoodie.Type != gooTypeVariable || hoodie.ImageID != hoodieImg || !slices.Equal(hoodie.GalleryIDs, []int64{backImg, capImg}) {
		t.Errorf("variable product: type=%q image=%d gallery=%v", hoodie.Type, hoodie.ImageID, hoodie.GalleryIDs)
	}
	if len(hoodie.Attributes) != 1 || hoodie.Attributes[0].Taxonomy != "pa_color" || hoodie.Attributes[0].Name != "Color" ||
		!hoodie.Attributes[0].Variation || !slices.Equal(hoodie.Attributes[0].Options, []string{"blue", "red"}) {
		t.Errorf("taxonomy attribute: %+v", hoodie.Attributes)
	}
	if hoodie.DefaultAttributes["pa_color"] != "blue" {
		t.Errorf("default attributes: %v", hoodie.DefaultAttributes)
	}
	if !eqf(hoodie.MinPrice, 39) || !eqf(hoodie.MaxPrice, 45) || hoodie.StockStatus != "instock" {
		t.Errorf("variable price range %v-%v status %q", hoodie.MinPrice, hoodie.MaxPrice, hoodie.StockStatus)
	}
	if got := termSlugs(hoodie.ID, "pa_color"); !slices.Equal(got, []string{"blue", "red"}) {
		t.Errorf("hoodie colour terms: %v", got)
	}
	if ga := srv.gooAttributeByTaxonomy(ctx, "pa_color"); ga == nil || ga.Name != "Color" {
		t.Errorf("global attribute: %+v", ga)
	}
	vars, err := srv.gooVariations(ctx, hoodie.ID, true)
	if err != nil || len(vars) != 2 {
		t.Fatalf("variations = %d (%v)", len(vars), err)
	}
	blue, red := vars[0], vars[1]
	if blue.VariationAttrs["pa_color"] != "blue" || !eqf(blue.RegularPrice, 45) || blue.SKU != "HOOD-BLUE" || !blue.ManageStock ||
		blue.StockQuantity == nil || *blue.StockQuantity != 3 || blue.ImageID != backImg || blue.Description != "Navy blue." || blue.Name != "Hoodie - Blue" {
		t.Errorf("blue variation: %+v", blue)
	}
	if red.VariationAttrs["pa_color"] != "red" || !eqf(red.SalePrice, 39) || !red.IsOnSale() || red.ManageStock || red.SKU != "HOOD-RED" {
		t.Errorf("red variation: %+v", red)
	}
	if m, err := srv.gooMatchVariation(ctx, hoodie, map[string]string{"attribute_pa_color": "red"}); err != nil || m.ID != red.ID {
		t.Errorf("matching the red variation: %v", err)
	}

	// The product on sale.
	if !eqf(capP.RegularPrice, 15) || !eqf(capP.SalePrice, 10) || !capP.IsOnSale() || capP.SaleFrom == nil || capP.SaleFrom.UTC().Year() != 2020 ||
		capP.SaleTo == nil || capP.SaleTo.UTC().Year() != 2099 || capP.Backorders != "notify" || capP.StockStatus != "onbackorder" || capP.CatalogVisibility != "catalog" {
		t.Errorf("sale product: %+v", capP)
	}
	// Downloadable, grouped and external products.
	if !guide.Downloadable || !guide.Virtual || len(guide.Downloads) != 1 || guide.Downloads[0].Name != "Style guide (PDF)" ||
		!strings.HasSuffix(guide.Downloads[0].File, "/style-guide.pdf") {
		t.Errorf("downloadable product: %+v", guide.Downloads)
	}
	if set.Type != gooTypeGrouped || !slices.Equal(set.Children, []int64{tee.ID, capP.ID}) || !eqf(set.MinPrice, 10) || !eqf(set.MaxPrice, 20) {
		t.Errorf("grouped product: type=%q children=%v range=%v-%v", set.Type, set.Children, set.MinPrice, set.MaxPrice)
	}
	if socks.Type != gooTypeExternal || socks.ProductURL != "https://partner.example.test/socks" || socks.ButtonText != "Buy at the partner" ||
		!eqf(socks.RegularPrice, 9) || socks.CatalogVisibility != "hidden" {
		t.Errorf("external product: %+v", socks)
	}

	// Category hierarchy.
	shirts, err1 := srv.store.TermBySlug(ctx, "product_cat", "t-shirts")
	clothing, err2 := srv.store.TermBySlug(ctx, "product_cat", "clothing")
	if err1 != nil || err2 != nil || shirts.ParentID != clothing.ID || shirts.Name != "T-shirts" {
		t.Errorf("category hierarchy: %+v %+v", shirts, clothing)
	}
	// Orders and coupons are not imported; product_type / product_visibility are not terms here.
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM posts WHERE type IN ('shop_order','shop_coupon')) + (SELECT count(*) FROM goo_orders) +
		(SELECT count(*) FROM goo_coupons) + (SELECT count(*) FROM terms WHERE taxonomy IN ('product_type','product_visibility'))`).Scan(&n)
	if n != 0 {
		t.Errorf("%d order, coupon or internal term rows imported", n)
	}

	counts := func() (products, variations, media int) {
		_ = srv.store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM posts WHERE type = 'product'), (SELECT count(*) FROM posts WHERE type = 'product_variation'),
			(SELECT count(*) FROM media)`).Scan(&products, &variations, &media)
		return
	}
	p0, v0, m0 := counts()
	if p0 != 6 || v0 != 2 {
		t.Fatalf("products %d, variations %d", p0, v0)
	}

	// A second import adds nothing.
	admin, _ := srv.store.UserByLoginOrEmail(ctx, "admin")
	rep, err := srv.importWXRWith(ctx, admin, fixture, wxrImportOptions{FetchAttachments: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Products != 0 || rep.Variations != 0 || rep.Attachments != 0 || rep.SkippedOrders != 1 || rep.SkippedCoupons != 1 {
		t.Errorf("re-import report: %+v", rep)
	}
	if p, v, m := counts(); p != p0 || v != v0 || m != m0 {
		t.Errorf("re-import duplicated: products %d→%d variations %d→%d media %d→%d", p0, p, v0, v, m0, m)
	}

	// A variation lost (an import that stopped half way) comes back on the next run, once.
	if _, err := srv.store.Pool.Exec(ctx, `DELETE FROM posts WHERE id = $1`, red.ID); err != nil {
		t.Fatal(err)
	}
	rep, err = srv.importWXRWith(ctx, admin, fixture, wxrImportOptions{FetchAttachments: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Products != 0 || rep.Variations != 1 {
		t.Errorf("retry report: products %d variations %d", rep.Products, rep.Variations)
	}
	if _, v, _ := counts(); v != v0 {
		t.Errorf("variations after the retry: %d, want %d", v, v0)
	}
	if !strings.Contains(importSummary(rep), "skipped 1 orders / 1 coupons") {
		t.Errorf("CLI summary: %s", importSummary(rep))
	}
}

// wxrWooDraftFixture is a WooCommerce export of a draft variable product: no post_name (WordPress
// leaves it empty on drafts), no category, a Cyrillic global attribute (pa_размер, which WooCommerce
// keeps url-decoded while the variation meta keys are sanitize_title()'d, so %xx-encoded) and markup
// in the purchase note and a variation description.
const wxrWooDraftFixture = `<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0" xmlns:excerpt="http://wordpress.org/export/1.2/excerpt/" xmlns:content="http://purl.org/rss/1.0/modules/content/"
	xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:wp="http://wordpress.org/export/1.2/">
<channel>
	<title>Магазин</title>
	<wp:wxr_version>1.2</wp:wxr_version>
	<wp:base_site_url>https://shop.example.test</wp:base_site_url>
	<wp:term><wp:term_id>30</wp:term_id><wp:term_taxonomy><![CDATA[pa_размер]]></wp:term_taxonomy><wp:term_slug><![CDATA[s]]></wp:term_slug><wp:term_name><![CDATA[S]]></wp:term_name></wp:term>
	<wp:term><wp:term_id>31</wp:term_id><wp:term_taxonomy><![CDATA[pa_размер]]></wp:term_taxonomy><wp:term_slug><![CDATA[m]]></wp:term_slug><wp:term_name><![CDATA[M]]></wp:term_name></wp:term>
	<item>
		<title><![CDATA[Худи]]></title>
		<content:encoded><![CDATA[<p>Тёплое худи.</p>]]></content:encoded>
		<excerpt:encoded><![CDATA[]]></excerpt:encoded>
		<wp:post_id>60</wp:post_id>
		<wp:post_date><![CDATA[2024-03-05 11:00:00]]></wp:post_date>
		<wp:post_name><![CDATA[]]></wp:post_name>
		<wp:status><![CDATA[draft]]></wp:status>
		<wp:post_parent>0</wp:post_parent>
		<wp:post_type><![CDATA[product]]></wp:post_type>
		<category domain="pa_размер" nicename="s"><![CDATA[S]]></category>
		<category domain="pa_размер" nicename="m"><![CDATA[M]]></category>
		<category domain="product_type" nicename="variable"><![CDATA[variable]]></category>
		<wp:postmeta><wp:meta_key><![CDATA[_sku]]></wp:meta_key><wp:meta_value><![CDATA[HOOD-1]]></wp:meta_value></wp:postmeta>
		<wp:postmeta><wp:meta_key><![CDATA[_purchase_note]]></wp:meta_key><wp:meta_value><![CDATA[<a href="javascript:alert(1)">Claim</a><img src=x onerror=alert(2)>Thanks]]></wp:meta_value></wp:postmeta>
		<wp:postmeta><wp:meta_key><![CDATA[_product_attributes]]></wp:meta_key><wp:meta_value><![CDATA[a:1:{s:15:"pa_размер";a:6:{s:4:"name";s:15:"pa_размер";s:5:"value";s:0:"";s:8:"position";i:0;s:10:"is_visible";i:1;s:12:"is_variation";i:1;s:11:"is_taxonomy";i:1;}}]]></wp:meta_value></wp:postmeta>
		<wp:postmeta><wp:meta_key><![CDATA[_default_attributes]]></wp:meta_key><wp:meta_value><![CDATA[a:1:{s:15:"pa_размер";s:1:"m";}]]></wp:meta_value></wp:postmeta>
	</item>
	<item>
		<title><![CDATA[Худи - S]]></title>
		<wp:post_id>61</wp:post_id>
		<wp:post_date><![CDATA[2024-03-05 11:00:00]]></wp:post_date>
		<wp:post_name><![CDATA[]]></wp:post_name>
		<wp:status><![CDATA[publish]]></wp:status>
		<wp:post_parent>60</wp:post_parent>
		<wp:post_type><![CDATA[product_variation]]></wp:post_type>
		<wp:postmeta><wp:meta_key><![CDATA[_sku]]></wp:meta_key><wp:meta_value><![CDATA[HOOD-S]]></wp:meta_value></wp:postmeta>
		<wp:postmeta><wp:meta_key><![CDATA[_regular_price]]></wp:meta_key><wp:meta_value><![CDATA[30]]></wp:meta_value></wp:postmeta>
		<wp:postmeta><wp:meta_key><![CDATA[_variation_description]]></wp:meta_key><wp:meta_value><![CDATA[<b>Small</b><script>alert(3)</script>]]></wp:meta_value></wp:postmeta>
		<wp:postmeta><wp:meta_key><![CDATA[attribute_pa_%d1%80%d0%b0%d0%b7%d0%bc%d0%b5%d1%80]]></wp:meta_key><wp:meta_value><![CDATA[s]]></wp:meta_value></wp:postmeta>
	</item>
	<item>
		<title><![CDATA[Худи - M]]></title>
		<wp:post_id>62</wp:post_id>
		<wp:post_date><![CDATA[2024-03-05 11:00:00]]></wp:post_date>
		<wp:post_name><![CDATA[]]></wp:post_name>
		<wp:status><![CDATA[publish]]></wp:status>
		<wp:post_parent>60</wp:post_parent>
		<wp:post_type><![CDATA[product_variation]]></wp:post_type>
		<wp:postmeta><wp:meta_key><![CDATA[_sku]]></wp:meta_key><wp:meta_value><![CDATA[TAKEN-M]]></wp:meta_value></wp:postmeta>
		<wp:postmeta><wp:meta_key><![CDATA[_regular_price]]></wp:meta_key><wp:meta_value><![CDATA[32]]></wp:meta_value></wp:postmeta>
		<wp:postmeta><wp:meta_key><![CDATA[attribute_pa_%d1%80%d0%b0%d0%b7%d0%bc%d0%b5%d1%80]]></wp:meta_key><wp:meta_value><![CDATA[m]]></wp:meta_value></wp:postmeta>
	</item>
</channel>
</rss>`

// A draft product without a slug is found again by its title and date (post_exists), so a second
// import adds nothing; a non-ASCII global attribute gets an ASCII slug and its terms, options and
// variation keys follow it; a variation whose SKU is taken is reported; the default product category
// is the store's; markup is filtered for a user without unfiltered_html.
func TestWXRImportWooDraftNonASCIIAttribute(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	uid, err := srv.store.CreateUser(ctx, store.NewUser{Login: "wxrmgr", Email: "wxrmgr@example.test", PasswordHash: "x", Role: "administrator"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetUserCapExceptions(ctx, uid, map[string]string{"unfiltered_html": "deny"}); err != nil {
		t.Fatal(err)
	}
	u, err := srv.store.UserByID(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if u.Can("unfiltered_html", nil) {
		t.Fatal("the cap exception did not apply")
	}
	gooMakeSimple(t, srv, "Other", 5, func(p *gooProduct) { p.SKU = "TAKEN-M" })
	def := &store.Term{Taxonomy: "product_cat", Slug: "sin-categoria", Name: "Sin categoría"}
	if err := srv.store.InsertTerm(ctx, def); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSetOpts(ctx, map[string]any{"default_product_cat": int(def.ID)}); err != nil {
		t.Fatal(err)
	}

	rep, err := srv.importWXRWith(ctx, u, []byte(wxrWooDraftFixture), wxrImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Products != 1 || rep.Variations != 2 {
		t.Fatalf("report: products %d variations %d, log %v", rep.Products, rep.Variations, rep.Log)
	}
	log := strings.Join(rep.Log, "\n")
	if !strings.Contains(log, "Variation “Худи - M”: the SKU TAKEN-M is used by another product here, so it was left empty.") {
		t.Errorf("taken variation SKU not reported: %s", log)
	}
	if strings.Contains(log, "attribute") {
		t.Errorf("attribute reported as failed: %s", log)
	}

	var id int64
	if err := srv.store.Pool.QueryRow(ctx, `SELECT id FROM posts WHERE type = 'product' AND title = 'Худи'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	p, err := srv.gooGetProduct(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	ga := srv.gooAttributeByTaxonomy(ctx, "pa_"+wxrAttrSlug("размер"))
	if ga == nil || ga.Name != "Размер" || !strings.HasPrefix(ga.Slug, "attr-") {
		t.Fatalf("global attribute: %+v", ga)
	}
	tax := ga.Taxonomy()
	if p.Status != "draft" || p.SKU != "HOOD-1" || len(p.Attributes) != 1 || p.Attributes[0].Taxonomy != tax || p.Attributes[0].Name != "Размер" ||
		!p.Attributes[0].Variation || !slices.Equal(p.Attributes[0].Options, []string{"s", "m"}) || p.DefaultAttributes[tax] != "m" {
		t.Errorf("product: status %q sku %q attributes %+v defaults %v", p.Status, p.SKU, p.Attributes, p.DefaultAttributes)
	}
	if terms, _ := srv.store.TermsForPosts(ctx, []int64{id}, tax); len(terms[id]) != 2 {
		t.Errorf("attribute terms on the product: %v", terms[id])
	}
	if cats, _ := srv.store.TermsForPosts(ctx, []int64{id}, "product_cat"); len(cats[id]) != 1 || cats[id][0].ID != def.ID {
		t.Errorf("default category: %v", cats[id])
	}
	if strings.Contains(p.PurchaseNote, "javascript:") || strings.Contains(p.PurchaseNote, "onerror") || !strings.Contains(p.PurchaseNote, "Thanks") {
		t.Errorf("purchase note not filtered: %q", p.PurchaseNote)
	}
	vars, err := srv.gooVariations(ctx, id, true)
	if err != nil || len(vars) != 2 {
		t.Fatalf("variations: %d %v", len(vars), err)
	}
	for _, v := range vars {
		if len(v.VariationAttrs) != 1 || (v.VariationAttrs[tax] != "s" && v.VariationAttrs[tax] != "m") {
			t.Errorf("variation %q attributes %v", v.Name, v.VariationAttrs)
		}
		if v.VariationAttrs[tax] == "s" && (v.SKU != "HOOD-S" || strings.Contains(v.Description, "<script") || !strings.Contains(v.Description, "<b>Small</b>")) {
			t.Errorf("variation S: sku %q description %q", v.SKU, v.Description)
		}
		if v.VariationAttrs[tax] == "m" && v.SKU != "" {
			t.Errorf("variation M kept a taken SKU: %q", v.SKU)
		}
	}
	if m, err := srv.gooMatchVariation(ctx, p, map[string]string{"attribute_" + tax: "m"}); err != nil || m.VariationAttrs[tax] != "m" {
		t.Errorf("matching the M variation: %v", err)
	}

	// A second import finds the draft by title and date: nothing is added, and the existing
	// variations are not reported as SKU conflicts.
	rep, err = srv.importWXRWith(ctx, u, []byte(wxrWooDraftFixture), wxrImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var products, variations, attrs int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM posts WHERE type = 'product' AND title = 'Худи'),
		(SELECT count(*) FROM posts WHERE type = 'product_variation'), (SELECT count(*) FROM goo_attributes)`).Scan(&products, &variations, &attrs)
	if rep.Products != 0 || rep.Variations != 0 || products != 1 || variations != 2 || attrs != 1 {
		t.Errorf("re-import: report %d/%d, rows: products %d variations %d attributes %d", rep.Products, rep.Variations, products, variations, attrs)
	}
	if strings.Contains(strings.Join(rep.Log, "\n"), "SKU") {
		t.Errorf("re-import reported SKUs: %v", rep.Log)
	}
}

func TestWXRAttrSlug(t *testing.T) {
	for in, want := range map[string]string{"color": "color", "shoe-size": "shoe-size", "größe": "grosse", "taille_été": "taille_ete"} {
		if got := wxrAttrSlug(in); got != want {
			t.Errorf("wxrAttrSlug(%q) = %q, want %q", in, got, want)
		}
	}
	if a, b := wxrAttrSlug("размер"), wxrAttrSlug("цвет"); !strings.HasPrefix(a, "attr-") || a == b || a != wxrAttrSlug("размер") {
		t.Errorf("non-Latin slugs: %q %q", a, b)
	}
	if long := wxrAttrSlug(strings.Repeat("abcdefgh-", 5)); len(long) > 28 {
		t.Errorf("long slug %q", long)
	}
	if got := wxrFileTaxonomy("pa_%d1%80%d0%b0%d0%b7%d0%bc%d0%b5%d1%80"); got != "pa_размер" {
		t.Errorf("decoded taxonomy %q", got)
	}
	if got := wxrOurTaxonomy(map[string]*gooAttribute{"pa_размер": {Slug: "razmer"}}, "pa_%d1%80%d0%b0%d0%b7%d0%bc%d0%b5%d1%80"); got != "pa_razmer" {
		t.Errorf("mapped taxonomy %q", got)
	}
	attrs := []gooProductAttr{{Name: "Shoe Size"}}
	if k := wxrAttrKey(attrs, "shoe%2dsize"); k != "shoe-size" {
		t.Errorf("encoded custom key %q", k)
	}
}
