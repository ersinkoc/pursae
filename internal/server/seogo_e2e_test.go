package server

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// End-to-end checks of Pursae SEO on real front-end pages: one title, one canonical, one meta
// description, a valid JSON-LD graph, Open Graph tags, breadcrumbs, the sitemaps (products and
// every language included) and robots.txt — alone, with Pursae Commerce, and with Pursae Multi
// Language.

// seogoHeadCheck asserts the head of a page has exactly one of each SEO tag and returns the
// canonical URL ("" when the page has none) and the parsed JSON-LD graph.
func seogoHeadCheck(t *testing.T, what, page string, wantCanonical bool) (string, []map[string]any) {
	t.Helper()
	if n := strings.Count(page, "<title>"); n != 1 {
		t.Errorf("%s: %d <title> elements", what, n)
	}
	if n := strings.Count(page, `<meta name="description"`); n > 1 {
		t.Errorf("%s: %d meta descriptions", what, n)
	}
	if n := strings.Count(page, `name="robots"`); n != 1 {
		t.Errorf("%s: %d robots metas", what, n)
	}
	n := strings.Count(page, `rel="canonical"`) + strings.Count(page, `rel='canonical'`)
	if wantCanonical && n != 1 || !wantCanonical && n != 0 {
		t.Errorf("%s: %d canonical links (want canonical: %v)", what, n, wantCanonical)
	}
	for _, og := range []string{"og:locale", "og:type", "og:title", "og:site_name"} {
		if c := strings.Count(page, `property="`+og+`"`); c != 1 {
			t.Errorf("%s: %d %s tags", what, c, og)
		}
	}
	if n := strings.Count(page, `class="seogo-schema-graph"`); n != 1 {
		t.Errorf("%s: %d Pursae SEO JSON-LD scripts", what, n)
	}
	can := ""
	if m := regexp.MustCompile(`<link rel="canonical" href="([^"]+)"`).FindStringSubmatch(page); m != nil {
		can = html.UnescapeString(m[1])
	}
	return can, seogoJSONLD(t, page)
}

func seogoMetaContent(page, attr, name string) string {
	m := regexp.MustCompile(`<meta ` + attr + `="` + regexp.QuoteMeta(name) + `" content="([^"]*)"`).FindStringSubmatch(page)
	if m == nil {
		return ""
	}
	return html.UnescapeString(m[1])
}

// seogoSitemapLocs parses a sitemap or a sitemap index and returns its <loc> values; alternates maps
// each URL to its hreflang alternates (hreflang => href).
func seogoSitemapLocs(t *testing.T, body string) (locs []string, alternates map[string]map[string]string) {
	t.Helper()
	var doc struct {
		URLs []struct {
			Loc  string `xml:"loc"`
			Alts []struct {
				Rel      string `xml:"rel,attr"`
				Hreflang string `xml:"hreflang,attr"`
				Href     string `xml:"href,attr"`
			} `xml:"http://www.w3.org/1999/xhtml link"`
		} `xml:"url"`
		Sitemaps []struct {
			Loc string `xml:"loc"`
		} `xml:"sitemap"`
	}
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("sitemap is not valid XML: %v\n%s", err, body)
	}
	alternates = map[string]map[string]string{}
	for _, u := range doc.URLs {
		locs = append(locs, u.Loc)
		for _, a := range u.Alts {
			if alternates[u.Loc] == nil {
				alternates[u.Loc] = map[string]string{}
			}
			alternates[u.Loc][a.Hreflang] = a.Href
		}
	}
	for _, s := range doc.Sitemaps {
		locs = append(locs, s.Loc)
	}
	return locs, alternates
}

func containsLoc(locs []string, want string) bool {
	for _, l := range locs {
		if l == want {
			return true
		}
	}
	return false
}

func breadcrumbNames(graph []map[string]any) []string {
	bc := graphNode(graph, "BreadcrumbList")
	if bc == nil {
		return nil
	}
	var out []string
	items, _ := bc["itemListElement"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		name, _ := m["name"].(string)
		out = append(out, name)
	}
	return out
}

func TestSEOGoEndToEndWithCommerce(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	seogoToggle(t, c, base, "activate")
	_ = srv.gooSetOpts(ctx, map[string]any{"catalog_rows": 1})

	// A category, a post in it with the breadcrumb shortcode and an image, a page.
	code, body := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/categories", `{"name":"News"}`)
	var cat struct {
		ID   int64  `json:"id"`
		Link string `json:"link"`
	}
	if code != http.StatusCreated || json.Unmarshal([]byte(body), &cat) != nil {
		t.Fatalf("create category: %d %s", code, body)
	}
	code, body = restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts", `{"title":"Launch day","status":"publish","categories":[`+itoa64(cat.ID)+`],
		"excerpt":"We launched today.","content":"<!-- wp:paragraph --><p>[seogo_breadcrumb]</p><!-- /wp:paragraph --><!-- wp:image --><figure class=\"wp-block-image\"><img src=\"/wp-content/uploads/launch.jpg\" alt=\"\"/></figure><!-- /wp:image -->"}`)
	var postObj struct {
		ID   int64  `json:"id"`
		Link string `json:"link"`
	}
	if code != http.StatusCreated || json.Unmarshal([]byte(body), &postObj) != nil {
		t.Fatalf("create post: %d %s", code, body)
	}

	_, page, _ := get(t, c, postObj.Link)
	can, graph := seogoHeadCheck(t, "post", page, true)
	if can != postObj.Link {
		t.Errorf("post canonical = %q, want %q", can, postObj.Link)
	}
	if !strings.Contains(page, "<title>Launch day - Toggle Site</title>") {
		t.Errorf("post title: %s", regexp.MustCompile(`<title>.*?</title>`).FindString(page))
	}
	if d := seogoMetaContent(page, "name", "description"); d != "We launched today." {
		t.Errorf("post description = %q", d)
	}
	if seogoMetaContent(page, "property", "og:type") != "article" || seogoMetaContent(page, "property", "og:url") != postObj.Link ||
		seogoMetaContent(page, "property", "og:image") != base+"/wp-content/uploads/launch.jpg" || seogoMetaContent(page, "property", "og:locale") != "en_US" {
		t.Error("post Open Graph tags")
	}
	if got := breadcrumbNames(graph); strings.Join(got, " > ") != "Home > News > Launch day" {
		t.Errorf("post BreadcrumbList = %v", got)
	}
	if !strings.Contains(page, `<nav aria-label="breadcrumbs" class="seogo-breadcrumb rank-math-breadcrumb">`) || !strings.Contains(page, `href="`+cat.Link+`"`) {
		t.Error("the breadcrumb shortcode did not render the trail")
	}
	for _, typ := range []string{"Organization", "WebSite", "WebPage", "Article", "Person", "ImageObject"} {
		if graphNode(graph, typ) == nil {
			t.Errorf("post graph lacks %s", typ)
		}
	}
	// The Person @id hashes the user id, not the login (no offline login confirmation).
	if p := graphNode(graph, "Person"); p != nil {
		admin, _ := srv.store.UserByID(ctx, 1)
		if want := personID(base+"/", admin); p["@id"] != want || strings.Contains(want, "21232f297a57a5a743894a0e4a801fc3") {
			t.Errorf("Person @id = %v", p["@id"])
		}
	}

	// Category archive.
	_, page, _ = get(t, c, cat.Link)
	if can, _ := seogoHeadCheck(t, "category", page, true); can != cat.Link {
		t.Errorf("category canonical = %q", can)
	}
	if !strings.Contains(page, "<title>News Archives - Toggle Site</title>") {
		t.Errorf("category title: %s", regexp.MustCompile(`<title>.*?</title>`).FindString(page))
	}

	// Products: one with imported Yoast meta and a featured image, in a category; four more so the
	// shop has a second page (catalog_rows 1 = 4 per page).
	shoes := gooCatTerm(t, srv, "product_cat", "Shoes")
	att := &store.Attachment{UploaderID: 1, Slug: "runner", FilePath: "2026/11/runner.jpg", Mime: "image/jpeg", Width: 1200, Height: 630, Alt: "Runner", Title: "Runner"}
	if err := srv.store.InsertAttachment(ctx, att); err != nil {
		t.Fatal(err)
	}
	runner := gooMakeSimple(t, srv, "Runner", 90, func(p *gooProduct) { p.ImageID = att.ID })
	if err := srv.store.SetPostTerms(ctx, runner.ID, "product_cat", []int64{shoes.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET meta = meta || '{"_yoast_wpseo_metadesc":"Light running shoes.","_yoast_wpseo_title":"%%title%% shoes %%sep%% %%sitename%%"}'::jsonb WHERE id = $1`, runner.ID); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 4; i++ {
		gooMakeSimple(t, srv, "Filler "+strconv.Itoa(i), 10, nil)
	}
	productURL := srv.gooPermalink(ctx, runner)
	code, page, _ = get(t, c, productURL)
	if code != http.StatusOK {
		t.Fatalf("product page: %d", code)
	}
	can, graph = seogoHeadCheck(t, "product", page, true)
	if can != productURL {
		t.Errorf("product canonical = %q, want %q", can, productURL)
	}
	if d := seogoMetaContent(page, "name", "description"); d != "Light running shoes." {
		t.Errorf("product description (imported Yoast meta) = %q", d)
	}
	if !strings.Contains(page, "<title>Runner shoes - Toggle Site</title>") {
		t.Errorf("product title (imported Yoast meta): %s", regexp.MustCompile(`<title>.*?</title>`).FindString(page))
	}
	if seogoMetaContent(page, "property", "og:type") != "product" {
		t.Errorf("product og:type = %q", seogoMetaContent(page, "property", "og:type"))
	}
	if img := seogoMetaContent(page, "property", "og:image"); !strings.Contains(img, "runner.jpg") {
		t.Errorf("product og:image = %q", img)
	}
	if got := breadcrumbNames(graph); len(got) < 3 || got[len(got)-2] != "Shoes" || got[len(got)-1] != "Runner" {
		t.Errorf("product BreadcrumbList = %v", got)
	}

	// Product category archive whose term id collides with page_on_front: still its own canonical.
	shoesURL := srv.gooTermLink(ctx, shoes)
	_ = srv.settings.Set(ctx, map[string]any{"show_on_front": "page", "page_on_front": shoes.ID})
	_, page, _ = get(t, c, shoesURL)
	if can, _ := seogoHeadCheck(t, "product category", page, true); can != shoesURL {
		t.Errorf("product category canonical = %q, want %q", can, shoesURL)
	}
	if !strings.Contains(page, "<title>Shoes Archives - Toggle Site</title>") {
		t.Errorf("product category title: %s", regexp.MustCompile(`<title>.*?</title>`).FindString(page))
	}
	_ = srv.settings.Set(ctx, map[string]any{"show_on_front": "posts", "page_on_front": 0})

	// Shop page 2: canonical page 2, rel=prev page 1.
	shopURL := srv.gooShopURL(ctx)
	shop2 := srv.links(ctx).Paged(shopURL, 2)
	code, page, _ = get(t, c, shop2)
	if code != http.StatusOK {
		t.Fatalf("shop page 2 (%s): %d", shop2, code)
	}
	if can, _ := seogoHeadCheck(t, "shop page 2", page, true); can != shop2 {
		t.Errorf("shop page 2 canonical = %q, want %q", can, shop2)
	}
	if !strings.Contains(page, `<link rel="prev" href="`+shopURL+`" />`) {
		t.Error("shop page 2 has no rel=prev")
	}

	// Cart: noindex, out of the sitemap.
	cartURL := srv.gooPageURL(ctx, "cart")
	_, page, _ = get(t, c, cartURL)
	if !strings.Contains(page, `<meta name="robots" content="noindex, follow" />`) {
		t.Error("the cart page is indexable")
	}

	// Sitemaps.
	_, idx, _ := get(t, c, base+"/sitemap_index.xml")
	locs, _ := seogoSitemapLocs(t, idx)
	for _, want := range []string{"post", "page", "category", "product", "product_cat"} {
		if !containsLoc(locs, base+"/"+want+"-sitemap.xml") {
			t.Errorf("sitemap index lacks %s-sitemap.xml: %v", want, locs)
		}
	}
	if containsLoc(locs, base+"/author-sitemap.xml") {
		t.Error("the author sitemap is listed while user enumeration is blocked")
	}
	if code, _, _ := get(t, c, base+"/author-sitemap.xml"); code != http.StatusNotFound {
		t.Errorf("author-sitemap.xml while enumeration is blocked: %d", code)
	}
	_, sm, _ := get(t, c, base+"/post-sitemap.xml")
	if l, _ := seogoSitemapLocs(t, sm); !containsLoc(l, postObj.Link) {
		t.Errorf("post sitemap lacks the post: %v", l)
	}
	_, sm, _ = get(t, c, base+"/product-sitemap.xml")
	if l, _ := seogoSitemapLocs(t, sm); !containsLoc(l, productURL) || len(l) != 5 {
		t.Errorf("product sitemap: %v", l)
	}
	if !strings.Contains(sm, "runner.jpg") {
		t.Error("product sitemap lacks the featured image")
	}
	_, sm, _ = get(t, c, base+"/product_cat-sitemap.xml")
	if l, _ := seogoSitemapLocs(t, sm); !containsLoc(l, shoesURL) {
		t.Errorf("product_cat sitemap: %v", l)
	}
	_, sm, _ = get(t, c, base+"/page-sitemap.xml")
	if l, _ := seogoSitemapLocs(t, sm); containsLoc(l, cartURL) || !containsLoc(l, base+"/") {
		t.Errorf("page sitemap (cart excluded, home first): %v", l)
	}
	_ = srv.seogoSave(ctx, func() *seogoOptions { o := srv.seogoOpts().clone(); o.SitemapPerPage = 2; return o }())
	_, idx, _ = get(t, c, base+"/sitemap_index.xml")
	if locs, _ = seogoSitemapLocs(t, idx); !containsLoc(locs, base+"/product-sitemap3.xml") || containsLoc(locs, base+"/product-sitemap4.xml") {
		t.Errorf("paged product sitemaps: %v", locs)
	}
	_, sm, _ = get(t, c, base+"/product-sitemap3.xml")
	if l, _ := seogoSitemapLocs(t, sm); len(l) != 1 {
		t.Errorf("product-sitemap3.xml: %v", l)
	}
	if code, _, _ := get(t, c, base+"/product-sitemap4.xml"); code != http.StatusNotFound {
		t.Errorf("product-sitemap4.xml: %d", code)
	}
	if _, robots, _ := get(t, c, base+"/robots.txt"); strings.Count(robots, "Sitemap: ") != 1 || !strings.Contains(robots, "Sitemap: "+base+"/sitemap_index.xml") {
		t.Errorf("robots.txt: %s", robots)
	}

	// The product editor has the Pursae SEO box and saves it.
	_, edit, _ := get(t, c, base+"/wp-admin/post?post="+itoa64(runner.ID)+"&action=edit")
	if !strings.Contains(edit, `id="seogo-metabox"`) {
		t.Error("no Pursae SEO box in the product editor")
	}

	// A partial settings import keeps everything else (and never breaks the site).
	if code, loc := seogoForm(t, c, base, "tools", url.Values{"action": {"import_settings"}, "settings_json": {`{"separator":"|"}`}}); code != http.StatusFound || strings.Contains(loc, "seogo-error") {
		t.Fatalf("import settings: %d %s", code, loc)
	}
	if o := srv.seogoOpts(); o.Separator != "|" || o.Types["post"].Title == "" || !o.Modules["sitemap"] {
		t.Errorf("partial import lost settings: %+v", o.Modules)
	}
	if code, _, _ := get(t, c, postObj.Link); code != http.StatusOK {
		t.Errorf("post after a partial import: %d", code)
	}

	// Without Pursae SEO the core head keeps the same rules: Cart noindex, store canonicals.
	seogoToggle(t, c, base, "deactivate")
	_, page, _ = get(t, c, cartURL)
	if !strings.Contains(page, "<meta name='robots' content='noindex, follow' />") {
		t.Error("core: the cart page is indexable")
	}
	_ = srv.settings.Set(ctx, map[string]any{"show_on_front": "page", "page_on_front": shoes.ID})
	_, page, _ = get(t, c, shoesURL)
	if !strings.Contains(page, `<link rel="canonical" href="`+shoesURL+`" />`) || strings.Count(page, `rel="canonical"`) != 1 {
		t.Error("core: product category canonical")
	}
	_ = srv.settings.Set(ctx, map[string]any{"show_on_front": "posts", "page_on_front": 0})
	_, page, _ = get(t, c, shop2)
	if !strings.Contains(page, `<link rel="canonical" href="`+shop2+`" />`) {
		t.Error("core: shop page 2 canonical")
	}
	_, page, _ = get(t, c, productURL)
	if !strings.Contains(page, `<link rel="canonical" href="`+productURL+`" />`) || !strings.Contains(page, "/?p="+itoa64(runner.ID)) {
		t.Error("core: product canonical or shortlink")
	}
}

// A stored null (an old partial import, a REST patch with "types": null) must not crash anything.
func TestSEOGoNullOptionMaps(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	seogoToggle(t, c, base, "activate")
	if err := srv.settings.Set(context.Background(), map[string]any{seogoOptionsKey: map[string]any{"separator": "|", "types": nil, "modules": nil, "roles": nil}}); err != nil {
		t.Fatal(err)
	}
	o := srv.seogoOpts()
	if o.Types["post"].Title == "" || !o.Modules["sitemap"] || o.Roles == nil {
		t.Errorf("defaults not restored over nulls: %+v", o.Modules)
	}
	for _, u := range []string{"/", "/wp-admin/", "/wp-admin/plugins", "/sitemap_index.xml"} {
		if code, _, _ := get(t, c, base+u); code != http.StatusOK {
			t.Errorf("%s with null option maps: %d", u, code)
		}
	}
	if code, loc := seogoForm(t, c, base, "tools", url.Values{"action": {"import_settings"}, "settings_json": {`null`}}); code != http.StatusFound || !strings.Contains(loc, "seogo-error") {
		t.Errorf("a null import was accepted: %d %s", code, loc)
	}
}

func TestSEOGoEndToEndMultiLanguage(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	seogoToggle(t, c, base, "activate")
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Türkçe", "tr_TR", "tr", "tr")

	// The site title in Turkish (Languages › Translations).
	_, body, _ := get(t, c, base+"/wp-admin/admin?page=mlang_strings")
	if code, _, _ := post(t, c, base+"/wp-admin/admin?page=mlang_strings", url.Values{"_wpnonce": {formNonce(t, body)},
		"original[0]": {"Toggle Site"}, "translation[tr][0]": {"Geçiş Sitesi"}, "translation[en][0]": {""}}); code != http.StatusFound {
		t.Fatalf("save strings: %d", code)
	}

	// A category and its Turkish translation, a post in each language.
	code, enCat, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/categories", map[string]any{"name": "News"})
	if code != http.StatusCreated {
		t.Fatalf("create category: %d %v", code, enCat)
	}
	enCatID := int64(enCat["id"].(float64))
	code, trCat, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/categories?lang=tr&translations[en]="+itoa64(enCatID), map[string]any{"name": "Haberler"})
	if code != http.StatusCreated {
		t.Fatalf("create Turkish category: %d %v", code, trCat)
	}
	trCatID := int64(trCat["id"].(float64))
	enID, enLink := pllCreatePost(t, c, base, "", "Hello world news")
	trID, trLink := pllCreatePost(t, c, base, "?lang=tr&translations[en]="+strconv.FormatInt(enID, 10), "Merhaba dünya haberi")
	if !strings.Contains(trLink, "/tr/") {
		t.Fatalf("Turkish permalink: %s", trLink)
	}
	_ = srv.store.SetPostTerms(ctx, enID, "category", []int64{enCatID})
	_ = srv.store.SetPostTerms(ctx, trID, "category", []int64{trCatID})
	product := gooMakeSimple(t, srv, "Lamp", 30, nil)
	productURL := srv.gooPermalink(ctx, product)

	// The Turkish post: Turkish locale, site name, home crumb; one of every tag.
	code, page, _ := get(t, c, trLink)
	if code != http.StatusOK {
		t.Fatalf("Turkish post: %d", code)
	}
	can, graph := seogoHeadCheck(t, "Turkish post", page, true)
	if can != trLink {
		t.Errorf("Turkish canonical = %q, want %q", can, trLink)
	}
	if !strings.Contains(page, "<title>Merhaba dünya haberi - Geçiş Sitesi</title>") {
		t.Errorf("Turkish title: %s", regexp.MustCompile(`<title>.*?</title>`).FindString(page))
	}
	if seogoMetaContent(page, "property", "og:locale") != "tr_TR" || seogoMetaContent(page, "property", "og:site_name") != "Geçiş Sitesi" {
		t.Errorf("Turkish og:locale/og:site_name: %q %q", seogoMetaContent(page, "property", "og:locale"), seogoMetaContent(page, "property", "og:site_name"))
	}
	if seogoMetaContent(page, "property", "og:locale:alternate") != "en_US" {
		t.Error("Turkish post has no og:locale:alternate en_US")
	}
	if web := graphNode(graph, "WebPage"); web == nil || web["inLanguage"] != "tr-TR" {
		t.Errorf("Turkish WebPage inLanguage: %v", web)
	}
	if site := graphNode(graph, "WebSite"); site == nil || site["name"] != "Geçiş Sitesi" || site["url"] != base+"/tr/" {
		t.Errorf("Turkish WebSite node: %v", site)
	}
	bc := graphNode(graph, "BreadcrumbList")
	items, _ := bc["itemListElement"].([]any)
	if len(items) == 0 || items[0].(map[string]any)["item"] != base+"/tr/" {
		t.Errorf("Turkish home crumb: %v", bc)
	}
	if !strings.Contains(page, `hreflang="tr"`) || !strings.Contains(page, `hreflang="en"`) {
		t.Error("Turkish post has no hreflang links")
	}

	// The English post stays English.
	_, page, _ = get(t, c, enLink)
	seogoHeadCheck(t, "English post", page, true)
	if seogoMetaContent(page, "property", "og:locale") != "en_US" || !strings.Contains(page, "<title>Hello world news - Toggle Site</title>") {
		t.Error("English post locale or title")
	}

	// The Turkish home.
	_, page, _ = get(t, c, base+"/tr/")
	if can, _ := seogoHeadCheck(t, "Turkish home", page, true); can != base+"/tr/" {
		t.Errorf("Turkish home canonical = %q", can)
	}

	// Sitemaps: one index, every language, hreflang alternates.
	code, idx, _ := get(t, c, base+"/sitemap_index.xml")
	locs, _ := seogoSitemapLocs(t, idx)
	if code != http.StatusOK || !containsLoc(locs, base+"/post-sitemap.xml") || !containsLoc(locs, base+"/product-sitemap.xml") {
		t.Fatalf("sitemap index: %d %v", code, locs)
	}
	_, sm, _ := get(t, c, base+"/post-sitemap.xml")
	locs, alts := seogoSitemapLocs(t, sm)
	if !containsLoc(locs, enLink) || !containsLoc(locs, trLink) {
		t.Errorf("post sitemap lacks a language: %v", locs)
	}
	if a := alts[trLink]; a["en"] != enLink || a["tr"] != trLink {
		t.Errorf("hreflang alternates of the Turkish post: %v", a)
	}
	if a := alts[enLink]; a["en"] != enLink || a["tr"] != trLink {
		t.Errorf("hreflang alternates of the English post: %v", a)
	}
	_, sm, _ = get(t, c, base+"/page-sitemap.xml")
	if l, _ := seogoSitemapLocs(t, sm); !containsLoc(l, base+"/") || !containsLoc(l, base+"/tr/") {
		t.Errorf("page sitemap lacks a language home: %v", l)
	}
	_, sm, _ = get(t, c, base+"/category-sitemap.xml")
	if _, a := seogoSitemapLocs(t, sm); len(a) != 2 {
		t.Errorf("category sitemap: not both categories have hreflang alternates:\n%s", sm)
	}
	_, sm, _ = get(t, c, base+"/product-sitemap.xml")
	if l, _ := seogoSitemapLocs(t, sm); !containsLoc(l, productURL) {
		t.Errorf("product sitemap: %v", l)
	}
	for _, u := range []string{"/tr/sitemap_index.xml", "/tr/post-sitemap.xml"} {
		if code, body, _ := get(t, c, base+u); code != http.StatusOK || !strings.Contains(body, "Pursae SEO") {
			t.Errorf("%s: %d", u, code)
		}
	}
	if _, robots, _ := get(t, c, base+"/robots.txt"); !strings.Contains(robots, "Sitemap: "+base+"/sitemap_index.xml") {
		t.Errorf("robots.txt: %s", robots)
	}

	// Without Pursae SEO the core index (the one robots.txt names) lists every language too.
	seogoToggle(t, c, base, "deactivate")
	_, core, _ := get(t, c, base+"/wp-sitemap.xml")
	if !strings.Contains(core, base+"/wp-sitemap-posts-post-1.xml") || !strings.Contains(core, base+"/tr/wp-sitemap-posts-post-1.xml") {
		t.Errorf("core sitemap index does not list every language:\n%s", core)
	}
	if _, fr, _ := get(t, c, base+"/tr/wp-sitemap.xml"); strings.Contains(fr, base+"/wp-sitemap-posts-post-1.xml") {
		t.Error("/tr/wp-sitemap.xml lists other languages")
	}
}

// A public Pursae Custom Fields post type: its archive gets a real title and canonical, its entries
// the SEO box and a sitemap.
func TestSEOGoCustomPostType(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	installAndLogin(t, ts, c)
	seogoToggle(t, c, base, "activate")
	exec := func(q string) {
		t.Helper()
		if _, err := srv.store.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels)
		VALUES ('seobook', 'Books', false, true, 'seobooks', false, '{"title":true,"editor":true}', '{"name":"Books"}') ON CONFLICT (slug) DO NOTHING`)
	acfgoRegistry.Lock()
	acfgoRegistry.types["seobook"] = &acfgoPostTypeDef{Name: "seobook", Public: true, ShowUI: true, HasArchive: true, Slug: "books",
		Labels: map[string]string{"name": "Books", "singular_name": "Book"}}
	acfgoRegistry.Unlock()
	t.Cleanup(func() {
		acfgoRegistry.Lock()
		delete(acfgoRegistry.types, "seobook")
		acfgoRegistry.Unlock()
	})
	book := &store.Post{Type: "seobook", AuthorID: 1, Slug: "dune", Title: "Dune", Status: "publish", Content: "<p>A desert planet.</p>", Meta: map[string]any{}}
	if err := srv.store.InsertPost(ctx, book); err != nil {
		t.Fatal(err)
	}
	archive := srv.links(ctx).PostTypeArchiveLink("seobook")
	code, page, _ := get(t, c, archive)
	if code != http.StatusOK {
		t.Fatalf("archive %s: %d", archive, code)
	}
	if can, _ := seogoHeadCheck(t, "CPT archive", page, true); can != archive {
		t.Errorf("CPT archive canonical = %q, want %q", can, archive)
	}
	if !strings.Contains(page, "<title>Books Archive - Toggle Site</title>") {
		t.Errorf("CPT archive title: %s", regexp.MustCompile(`<title>.*?</title>`).FindString(page))
	}
	bookURL := srv.links(ctx).Permalink(book)
	_, page, _ = get(t, c, bookURL)
	if can, _ := seogoHeadCheck(t, "CPT entry", page, true); can != bookURL {
		t.Errorf("CPT entry canonical = %q", can)
	}
	_, idx, _ := get(t, c, base+"/sitemap_index.xml")
	if l, _ := seogoSitemapLocs(t, idx); !containsLoc(l, base+"/seobook-sitemap.xml") {
		t.Errorf("sitemap index lacks the custom post type: %v", l)
	}
	_, sm, _ := get(t, c, base+"/seobook-sitemap.xml")
	if l, _ := seogoSitemapLocs(t, sm); !containsLoc(l, bookURL) {
		t.Errorf("CPT sitemap: %v", l)
	}
	_ = srv.settings.Set(ctx, map[string]any{"classic-editor-replace": "classic"})
	if _, edit, _ := get(t, c, base+"/wp-admin/post?post="+itoa64(book.ID)+"&action=edit"); !strings.Contains(edit, `id="seogo-metabox"`) {
		t.Error("no Pursae SEO box on a custom post type entry")
	}
	if _, titles, _ := get(t, c, base+"/wp-admin/seogo?page=titles"); !strings.Contains(titles, "title_pta_seobook") {
		t.Error("Search Appearance has no row for the custom post type archive")
	}
}
