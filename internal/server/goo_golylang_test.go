package server

import (
	"context"
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// gooPllSite is a store with Golylang on, English (default) and German, and the catalog fixture.
func gooPllSite(t *testing.T) (string, *http.Client, *Server, *gooCatalogFixture) {
	t.Helper()
	base, c, srv := gooSite(t)
	f := gooFrontCatalog(t, srv)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Deutsch", "de_DE", "de", "de")
	return base, c, srv, f
}

// gooPllTranslate follows the products list's "Add a translation" link for a product and publishes the
// new translation under name.
func gooPllTranslate(t *testing.T, c *http.Client, base string, srv *Server, src *gooProduct, lang, name string) *gooProduct {
	t.Helper()
	ctx := context.Background()
	_, list, _ := get(t, c, base+"/wp-admin/edit?post_type=product")
	re := regexp.MustCompile(`href="(/wp-admin/post-new\?post_type=product&amp;from_post=` + itoa64(src.ID) + `&amp;new_lang=` + lang + `&amp;_wpnonce=[^"]+)"`)
	m := re.FindStringSubmatch(list)
	if m == nil {
		t.Fatalf("no Add translation link for %s in %s", src.Name, lang)
	}
	code, body, _ := get(t, c, base+html.UnescapeString(m[1]))
	if code != http.StatusOK || !strings.Contains(body, `id="ml_box"`) {
		t.Fatalf("new translation screen: %d", code)
	}
	id := srv.pllGet(ctx, pllPost, src.ID, lang)
	if id == 0 || id == src.ID {
		t.Fatalf("the new %s product is not linked to %s", lang, src.Name)
	}
	p, err := srv.gooGetProduct(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p.Name, p.Slug, p.Status = name, strings.ToLower(name), "publish"
	if err := srv.gooSaveProduct(ctx, p, 1); err != nil {
		t.Fatal(err)
	}
	return p
}

// gooPllTranslatePage creates a translation of a page in lang.
func gooPllTranslatePage(t *testing.T, srv *Server, srcID int64, lang, title, slug string) int64 {
	t.Helper()
	ctx := context.Background()
	p := &store.Post{Type: "page", Title: title, Slug: slug, Status: "publish", AuthorID: 1, Meta: map[string]any{}}
	if src, err := srv.store.GetPost(ctx, srcID); err == nil {
		p.Content = src.Content
	}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := srv.pllSetLang(ctx, pllPost, p.ID, lang); err != nil {
		t.Fatal(err)
	}
	tr := srv.pllTranslations(ctx, pllPost, srcID)
	tr[lang] = p.ID
	if err := srv.pllSaveTranslations(ctx, pllPost, tr); err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func TestGooGolylangProducts(t *testing.T) {
	base, c, srv, f := gooPllSite(t)
	ctx := context.Background()
	for _, typ := range []string{"product"} {
		if !srv.pllTranslatedType(typ) {
			t.Errorf("%s is not translated", typ)
		}
	}
	for _, tax := range []string{"product_cat", "product_tag", "pa_color"} {
		if !srv.pllTranslatedTaxonomy(tax) {
			t.Errorf("%s is not translated", tax)
		}
	}
	// Existing products got the default language when the first language was added.
	if l := srv.pllObjLang(ctx, pllPost, f.hoodie.ID); l != "en" {
		t.Errorf("hoodie language %q, want en", l)
	}
	// The products list has a column per language.
	_, list, _ := get(t, c, base+"/wp-admin/edit?post_type=product")
	gooMustContain(t, "products list", list, `column-language_de`, `class="pll_icon_add"`)

	// A German attribute term: the translated variable product takes it.
	red, _ := srv.store.TermBySlug(ctx, "pa_color", "red")
	rot := &store.Term{Taxonomy: "pa_color", Name: "Rot", Slug: "rot", Meta: map[string]any{}}
	if err := srv.store.InsertTerm(ctx, rot); err != nil {
		t.Fatal(err)
	}
	_ = srv.pllSetLang(ctx, pllTerm, rot.ID, "de")
	_ = srv.pllSaveTranslations(ctx, pllTerm, map[string]int64{"en": red.ID, "de": rot.ID})

	// Translations through the admin flow: data, SKU and variations follow.
	pulli := gooPllTranslate(t, c, base, srv, f.hoodie, "de", "Kapuzenpulli")
	if pulli.Type != gooTypeVariable {
		t.Fatalf("translation type %q", pulli.Type)
	}
	vars, _ := srv.gooVariations(ctx, pulli.ID, true)
	if len(vars) != 2 {
		t.Fatalf("%d German variations, want 2", len(vars))
	}
	var deRed *gooProduct
	for _, v := range vars {
		if v.VariationAttrs["pa_color"] == "rot" {
			deRed = v
		}
	}
	if deRed == nil || deRed.SKU != "HO-red" || srv.pllGet(ctx, pllPost, f.red.ID, "de") != deRed.ID {
		t.Fatalf("the red variation is not translated and linked: %+v", deRed)
	}
	if got := pulli.Attributes[0].Options; len(got) != 2 || got[0] != "rot" || got[1] != "blue" {
		t.Errorf("translated attribute options %v, want [rot blue]", got)
	}
	becher := gooPllTranslate(t, c, base, srv, f.mug, "de", "Becher")
	hemd := gooPllTranslate(t, c, base, srv, f.shirt, "de", "Hemd")
	if hemd.SKU != "SH-1" || hemd.StockQuantity == nil || *hemd.StockQuantity != 10 {
		t.Errorf("the shirt's translation does not share SKU and stock: %q %v", hemd.SKU, hemd.StockQuantity)
	}
	// A SKU repeats inside a translation group only.
	if srv.gooSKUTaken(ctx, "SH-1", hemd.ID) {
		t.Error("the translation's own SKU counts as taken")
	}
	if !srv.gooSKUTaken(ctx, "SH-1", f.mug.ID) {
		t.Error("another product may take the shirt's SKU")
	}

	// Shared data: a price change on the source reaches the translation, and the variations.
	shirt, _ := srv.gooGetProduct(ctx, f.shirt.ID)
	shirt.RegularPrice, shirt.SalePrice = gooFloatPtr(45), nil
	if err := srv.gooSaveProduct(ctx, shirt, 1); err != nil {
		t.Fatal(err)
	}
	if h, _ := srv.gooGetProduct(ctx, hemd.ID); h.RegularPrice == nil || *h.RegularPrice != 45 || h.SalePrice != nil || h.Name != "Hemd" {
		t.Errorf("translation after the source's save: %v %v %q", h.RegularPrice, h.SalePrice, h.Name)
	}
	redV, _ := srv.gooGetProduct(ctx, f.red.ID)
	redV.RegularPrice = gooFloatPtr(77)
	if err := srv.gooSaveProduct(ctx, redV, 1); err != nil {
		t.Fatal(err)
	}
	if v, _ := srv.gooGetProduct(ctx, deRed.ID); v.RegularPrice == nil || *v.RegularPrice != 77 {
		t.Errorf("German red variation price %v, want 77", v.RegularPrice)
	}
	// Stock is one: a sale in either language reduces both.
	if _, err := srv.gooUpdateStock(ctx, hemd, -3, false); err != nil {
		t.Fatal(err)
	}
	if s, _ := srv.gooGetProduct(ctx, f.shirt.ID); s.StockQuantity == nil || *s.StockQuantity != 7 {
		t.Errorf("source stock after a German sale: %v, want 7", s.StockQuantity)
	}

	// Front end: the catalog per language, URLs with the product's language, hreflang and the switcher.
	if link := srv.gooPermalink(ctx, becher); !strings.Contains(link, "/de/product/becher/") {
		t.Errorf("German product URL %s", link)
	}
	if link := srv.gooPermalink(ctx, f.mug); strings.Contains(link, "/de/") || strings.Contains(link, "/en/") {
		t.Errorf("English product URL %s", link)
	}
	gooPllTranslatePage(t, srv, srv.gooPageID("shop"), "de", "Laden", "laden")
	v := gooFrontClient()
	_, shopEN, _ := get(t, v, base+"/shop/")
	if !strings.Contains(shopEN, ">Mug<") || strings.Contains(shopEN, ">Becher<") {
		t.Error("the English shop does not list the English products only")
	}
	code, shopDE, _ := get(t, v, base+"/de/laden/")
	if code != http.StatusOK {
		t.Fatalf("German shop: %d", code)
	}
	gooMustContain(t, "German shop", shopDE, `lang="de-DE"`, "Becher", "Kapuzenpulli", `href="`+base+`/shop/" hreflang="en"`)
	if strings.Contains(shopDE, ">Mug<") {
		t.Error("the German shop lists English products")
	}
	code, prodDE, _ := get(t, v, base+"/de/product/becher/")
	if code != http.StatusOK {
		t.Fatalf("German product page: %d", code)
	}
	gooMustContain(t, "German product", prodDE, `lang="de-DE"`, "Becher", `href="`+base+`/product/mug/" hreflang="en"`)

	// Category archives: the German category lists the German products.
	kleidung := &store.Term{Taxonomy: "product_cat", Name: "Kleidung", Slug: "kleidung", Meta: map[string]any{}}
	if err := srv.store.InsertTerm(ctx, kleidung); err != nil {
		t.Fatal(err)
	}
	_ = srv.pllSetLang(ctx, pllTerm, kleidung.ID, "de")
	_ = srv.pllSaveTranslations(ctx, pllTerm, map[string]int64{"en": f.cat.ID, "de": kleidung.ID})
	_ = srv.store.SetPostTerms(ctx, hemd.ID, "product_cat", []int64{kleidung.ID})
	if link := srv.gooTermLink(ctx, kleidung); !strings.Contains(link, "/de/product-category/kleidung/") {
		t.Errorf("German category URL %s", link)
	}
	if code, body, _ := get(t, v, base+"/de/product-category/kleidung/"); code != http.StatusOK || !strings.Contains(body, "Hemd") {
		t.Errorf("German category: %d", code)
	}
}

func TestGooGolylangCartCheckoutStrings(t *testing.T) {
	base, c, srv, f := gooPllSite(t)
	ctx := context.Background()
	gooPllTranslate(t, c, base, srv, f.mug, "de", "Becher")
	gooPllTranslatePage(t, srv, srv.gooPageID("cart"), "de", "Warenkorb", "warenkorb")
	gooPllTranslatePage(t, srv, srv.gooPageID("checkout"), "de", "Kasse", "kasse")
	_ = srv.gooSetOpts(ctx, map[string]any{"cod_settings": map[string]any{"enabled": "yes", "title": "Cash on delivery"},
		"demo_store": true, "demo_store_notice": "Free shipping this week"})
	if _, err := srv.gooAddMethod(ctx, 0, "flat_rate", map[string]any{"cost": "5", "title": "Flat rate"}); err != nil {
		t.Fatal(err)
	}

	// Store strings are registered in Languages › Translations and translated on the front end.
	var group []string
	for _, r := range srv.pllStrings(ctx) {
		if r.Group == "Goocommerce" {
			group = append(group, r.String)
		}
	}
	joined := strings.Join(group, "|")
	for _, want := range []string{"Free shipping this week", "Cash on delivery", "Flat rate", "Your {site_title} order has been received!"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Goocommerce strings lack %q", want)
		}
	}
	for orig, tr := range map[string]string{"Cash on delivery": "Nachnahme", "Flat rate": "Pauschale", "Free shipping this week": "Versandkostenfrei diese Woche",
		"Your {site_title} order has been received!": "Ihre Bestellung bei {site_title} ist eingegangen!"} {
		if err := srv.pllSetString(ctx, "de", orig, tr); err != nil {
			t.Fatal(err)
		}
	}

	// The cart page per language; items show in the visitor's language.
	v := gooFrontClient()
	if code, _, _ := get(t, v, base+"/shop/?add-to-cart="+itoa64(f.mug.ID)); code != http.StatusFound {
		t.Fatalf("add to cart: %d", code)
	}
	_, cartEN, _ := get(t, v, base+"/cart/")
	gooMustContain(t, "English cart", cartEN, `woocommerce-cart-form`, ">Mug</a>", "Free shipping this week")
	code, cartDE, _ := get(t, v, base+"/de/warenkorb/")
	if code != http.StatusOK {
		t.Fatalf("German cart: %d", code)
	}
	gooMustContain(t, "German cart", cartDE, `woocommerce-cart-form`, ">Becher</a>", "Pauschale", "Versandkostenfrei diese Woche", `href="`+base+`/de/kasse/"`)
	if strings.Contains(cartDE, ">Mug</a>") {
		t.Error("the German cart shows the English product")
	}
	// The German checkout: translated gateway title.
	code, coDE, _ := get(t, v, base+"/de/kasse/")
	if code != http.StatusOK {
		t.Fatalf("German checkout: %d", code)
	}
	gooMustContain(t, "German checkout", coDE, `name="woocommerce_checkout_place_order"`, "Nachnahme")

	// An order placed in German gets its language; its e-mails are in German.
	o := &gooOrder{Status: "processing", Currency: "USD", Billing: map[string]string{"email": "kunde@example.test", "first_name": "Kim"}, Shipping: map[string]string{},
		Meta: map[string]any{gooPllOrderLang: "de"}}
	if err := srv.gooSaveOrder(ctx, o, false); err != nil {
		t.Fatal(err)
	}
	e, err := srv.gooRenderEmail(ctx, gooEmailDefByID("customer_processing_order"), srv.gooEmailSettings("customer_processing_order"), o, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.Subject, "Ihre Bestellung bei") {
		t.Errorf("German order e-mail subject %q", e.Subject)
	}
	o.Meta[gooPllOrderLang] = "en"
	if e, _ := srv.gooRenderEmail(ctx, gooEmailDefByID("customer_processing_order"), srv.gooEmailSettings("customer_processing_order"), o, nil); !strings.Contains(e.Subject, "order has been received") {
		t.Errorf("English order e-mail subject %q", e.Subject)
	}
	// The checkout hook stores the language of the request.
	gc := gooTestCtx()
	pllSet(gc, &pllReq{srv: srv, Lang: srv.pllLang(ctx, "de"), Front: true, Filter: true})
	o2 := &gooOrder{Meta: map[string]any{}}
	for _, h := range gooCheckoutHooks {
		_ = h(srv, gc, nil, o2)
	}
	if o2.Meta[gooPllOrderLang] != "de" {
		t.Errorf("order language %v, want de", o2.Meta[gooPllOrderLang])
	}
}

// A store theme's demo imported while Golylang is on: everything gets the default language, the
// theme shows the language switcher, and translating a product keeps the store working.
func TestGooGolylangStoreThemeDemo(t *testing.T) {
	base, c, srv := gooSite(t)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Deutsch", "de_DE", "de", "de")
	ctx := context.Background()
	_, body, _ := get(t, c, base+"/wp-admin/goocommerce?page=themes")
	if !strings.Contains(body, "Golylang is active") {
		t.Error("the Store Themes screen does not mention Golylang")
	}
	nonce := formNonce(t, body)
	for _, step := range gooThemeSteps {
		gooThemeStepPost(t, c, base, nonce, "green-basket", step)
	}
	if srv.pllUntranslatedContent(ctx) {
		t.Error("demo items without a language")
	}
	var missing int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts p WHERE p.type IN ('product','product_variation') AND p.status = 'publish'
		AND NOT EXISTS (SELECT 1 FROM pll_post_langs l WHERE l.post_id = p.id)`).Scan(&missing)
	if missing > 0 {
		t.Errorf("%d demo products or variations without a language", missing)
	}
	v := gooFrontClient()
	code, shop, _ := get(t, v, base+"/shop/")
	if code != http.StatusOK || !strings.Contains(shop, "Heirloom Tomatoes") {
		t.Fatalf("shop: %d", code)
	}
	if strings.Contains(shop, `class="goo-lang-switcher"`) {
		t.Error("the switcher shows a language without content")
	}
	// A German translation of a demo product: the switcher appears, the German product page works.
	var tomatoes int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM posts WHERE type = 'product' AND slug = 'heirloom-tomatoes'`).Scan(&tomatoes)
	src, err := srv.gooGetProduct(ctx, tomatoes)
	if err != nil {
		t.Fatal(err)
	}
	de := gooPllTranslate(t, c, base, srv, src, "de", "Tomaten")
	if de.RegularPrice == nil || *de.RegularPrice != *src.RegularPrice || de.ImageID != src.ImageID {
		t.Errorf("the German product does not share price and image")
	}
	_, shop, _ = get(t, v, base+"/shop/")
	gooMustContain(t, "shop", shop, `class="goo-lang-switcher"`, `hreflang="de-DE" href="`+base+`/de/"`)
	_, prod, _ := get(t, v, base+"/product/heirloom-tomatoes/")
	gooMustContain(t, "product", prod, `class="goo-lang-switcher"`, `href="`+base+`/de/product/tomaten/"`)
	if code, body, _ := get(t, v, base+"/de/product/tomaten/"); code != http.StatusOK || !strings.Contains(body, `lang="de-DE"`) || !strings.Contains(body, "Tomaten") {
		t.Errorf("German product: %d", code)
	}
}
