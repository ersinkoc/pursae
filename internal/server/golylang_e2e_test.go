package server

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// TestGolylangEndToEnd walks a bilingual site the way an owner and a visitor use it: Pursae Multi
// Language with English (default, no code in URLs) and Turkish in directory mode, posts, categories,
// tags and a Commerce product with their translations, the language switcher, search forms, the page
// cache and its language cookie, the REST API with lang in the query string and in the JSON body, and
// the editors' term lists.
func TestGolylangEndToEnd(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	f := gooFrontCatalog(t, srv)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Türkçe", "tr_TR", "tr", "tr")
	if o := srv.pllOpts(); o.ForceLang != 1 || !o.HideDefault {
		t.Fatalf("default URL options: %+v", o)
	}

	// Categories and tags: the English ones, their Turkish translations (lang in the JSON body for
	// the category, in the query string for the tag).
	code, news, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/categories", map[string]any{"name": "News"})
	if code != http.StatusCreated || news["lang"] != "en" {
		t.Fatalf("create News: %d %v", code, news)
	}
	newsID := int64(news["id"].(float64))
	code, haber, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/categories", map[string]any{"name": "Haberler", "slug": "haberler",
		"lang": "tr", "translations": map[string]any{"en": newsID}})
	if code != http.StatusCreated || haber["lang"] != "tr" {
		t.Fatalf("create Haberler with lang in the body: %d %v", code, haber)
	}
	haberID := int64(haber["id"].(float64))
	if tr, _ := haber["translations"].(map[string]any); tr["en"] != float64(newsID) {
		t.Errorf("Haberler is not linked to News: %v", haber["translations"])
	}
	_, tag, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/tags", map[string]any{"name": "Travel"})
	tagID := int64(tag["id"].(float64))
	code, etiket, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/tags?lang=tr&translations[en]="+itoa64(tagID), map[string]any{"name": "Seyahat"})
	if code != http.StatusCreated || etiket["lang"] != "tr" {
		t.Fatalf("create Seyahat: %d %v", code, etiket)
	}
	etiketID := int64(etiket["id"].(float64))
	// A term with the same name in another language is no duplicate (the name check sees one language).
	if code, same, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/tags", map[string]any{"name": "Travel", "lang": "tr"}); code != http.StatusCreated || same["lang"] != "tr" {
		t.Errorf("a Turkish tag named like an English one: %d %v", code, same)
	}

	// A term translation link only joins a term of the same taxonomy.
	if code, odd, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/tags", map[string]any{"name": "Gezi", "lang": "tr",
		"translations": map[string]any{"en": newsID}}); code != http.StatusCreated {
		t.Errorf("create Gezi: %d", code)
	} else if tr, _ := odd["translations"].(map[string]any); tr["en"] != nil {
		t.Errorf("a tag was linked to a category as its translation: %v", tr)
	}

	// Posts: an English post and its Turkish translation, lang and translations in the JSON body.
	switcher := `<!-- wp:polylang/language-switcher {"show_names":true} /-->`
	code, en, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts", map[string]any{"title": "Hello Travellers", "status": "publish",
		"content": "<!-- wp:paragraph --><p>English body</p><!-- /wp:paragraph -->" + switcher, "categories": []int64{newsID}, "tags": []int64{tagID}})
	if code != http.StatusCreated || en["lang"] != "en" {
		t.Fatalf("create the English post: %d %v", code, en)
	}
	enID := int64(en["id"].(float64))
	enLink := en["link"].(string)
	code, trp, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts", map[string]any{"title": "Merhaba Gezginler", "status": "publish",
		"content": "<!-- wp:paragraph --><p>Turkish body</p><!-- /wp:paragraph -->" + switcher + `<!-- wp:search {"label":"Ara"} /-->`, "categories": []int64{haberID}, "tags": []int64{etiketID},
		"lang": "tr", "translations": map[string]any{"en": enID}})
	if code != http.StatusCreated || trp["lang"] != "tr" {
		t.Fatalf("create the Turkish post with lang in the body: %d lang=%v", code, trp["lang"])
	}
	trID := int64(trp["id"].(float64))
	trLink := trp["link"].(string)
	if tr, _ := trp["translations"].(map[string]any); tr["en"] != float64(enID) {
		t.Errorf("the Turkish post is not linked to the English one: %v", trp["translations"])
	}
	if !strings.HasPrefix(trLink, base+"/tr/") || strings.Contains(enLink, "/en/") {
		t.Fatalf("permalinks: en %s, tr %s", enLink, trLink)
	}

	// Front end: each language's home lists its own posts.
	_, home, _ := get(t, c, base+"/")
	if !strings.Contains(home, "Hello Travellers") || strings.Contains(home, "Merhaba Gezginler") {
		t.Error("/ is not the English home")
	}
	_, trHome, _ := get(t, c, base+"/tr/")
	if !strings.Contains(trHome, "Merhaba Gezginler") || strings.Contains(trHome, "Hello Travellers") || !strings.Contains(trHome, `lang="tr-TR"`) {
		t.Error("/tr/ is not the Turkish home")
	}
	if code, _, loc := get(t, c, base+"/en/"); code != http.StatusMovedPermanently || loc != "/" {
		t.Errorf("/en/: %d %s, want 301 to /", code, loc)
	}

	// The Turkish post: language, hreflang, the switcher links to the English translation.
	code, page, _ := get(t, c, trLink)
	if code != http.StatusOK || !strings.Contains(page, `lang="tr-TR"`) || !strings.Contains(page, "Turkish body") {
		t.Fatalf("Turkish post: %d", code)
	}
	if !strings.Contains(page, `hreflang="en"`) || !strings.Contains(page, `hreflang="tr"`) {
		t.Error("the Turkish post has no hreflang links")
	}
	sw := regexp.MustCompile(`<li class="lang-item [^"]*lang-item-en[ "][^>]*[^>]*><a[^>]*href="([^"]+)"`).FindStringSubmatch(page)
	if sw == nil || html.UnescapeString(sw[1]) != enLink {
		t.Errorf("the switcher's English link on the Turkish post = %v, want %s", sw, enLink)
	}
	_, enPage, _ := get(t, c, enLink)
	sw = regexp.MustCompile(`<li class="lang-item [^"]*lang-item-tr[ "][^>]*[^>]*><a[^>]*href="([^"]+)"`).FindStringSubmatch(enPage)
	if sw == nil || html.UnescapeString(sw[1]) != trLink {
		t.Errorf("the switcher's Turkish link on the English post = %v, want %s", sw, trLink)
	}
	// A Search block in the content submits to the Turkish home.
	if sf := regexp.MustCompile(`<form role="search" method="get" action="([^"]+)"[^>]*><label class="wp-block-search__label[^"]*" for="[^"]+">Ara</label>`).FindStringSubmatch(page); sf == nil || sf[1] != base+"/tr/" {
		t.Errorf("the Search block on the Turkish post submits to %v, want %s/tr/", sf, base)
	}
	// The post's term links carry the post's language.
	if !strings.Contains(page, base+"/tr/category/haberler/") {
		t.Error("the Turkish post does not link to the Turkish category archive")
	}

	// Category and tag archives per language.
	code, arch, _ := get(t, c, base+"/tr/category/haberler/")
	if code != http.StatusOK || !strings.Contains(arch, "Merhaba Gezginler") || strings.Contains(arch, "Hello Travellers") || !strings.Contains(arch, `lang="tr-TR"`) {
		t.Errorf("Turkish category archive: %d", code)
	}
	code, arch, _ = get(t, c, base+"/category/news/")
	if code != http.StatusOK || !strings.Contains(arch, "Hello Travellers") || strings.Contains(arch, "Merhaba Gezginler") {
		t.Errorf("English category archive: %d", code)
	}
	code, arch, _ = get(t, c, base+"/tr/tag/seyahat/")
	if code != http.StatusOK || !strings.Contains(arch, "Merhaba Gezginler") || strings.Contains(arch, "Hello Travellers") {
		t.Errorf("Turkish tag archive: %d", code)
	}

	// Search: the form on a Turkish page submits to the Turkish home, and the results stay Turkish.
	_, search, _ := get(t, c, base+"/tr/?s=Gezginler")
	if !strings.Contains(search, `role="search" method="get" action="`+base+`/tr/"`) {
		t.Errorf("the search form on /tr/ does not submit to /tr/:\n%s", firstMatch(search, `<form role="search"[^>]*>`))
	}
	if !strings.Contains(search, "Merhaba Gezginler") || !strings.Contains(search, `lang="tr-TR"`) {
		t.Error("a search on /tr/ does not find the Turkish post")
	}
	if _, search, _ = get(t, c, base+"/?s=Gezginler"); strings.Contains(search, "Merhaba Gezginler") {
		t.Error("an English search finds the Turkish post")
	}

	// REST: ?lang= narrows lists.
	if _, _, list := pllREST(t, c, base, http.MethodGet, "/wp-json/wp/v2/posts?lang=tr", nil); len(list) != 1 {
		t.Errorf("posts?lang=tr: %d posts, want 1", len(list))
	}
	_, _, cats := pllREST(t, c, base, http.MethodGet, "/wp-json/wp/v2/categories?lang=tr&per_page=100", nil)
	for _, v := range cats {
		if m, _ := v.(map[string]any); m["lang"] != "tr" {
			t.Errorf("categories?lang=tr lists %v (%v)", m["name"], m["lang"])
		}
	}

	// Editors: the block editor asks for the post's language, the classic editor lists its terms.
	_, ed, _ := get(t, c, base+"/wp-admin/post?post="+itoa64(trID)+"&action=edit")
	if !strings.Contains(ed, `window.pllPostLang = "tr"`) || !strings.Contains(ed, "wp.apiFetch.use") {
		t.Error("the block editor has no language middleware for the Turkish post")
	}
	if err := srv.settings.Set(ctx, map[string]any{"classic-editor-replace": "classic"}); err != nil {
		t.Fatal(err)
	}
	_, ed, _ = get(t, c, base+"/wp-admin/post?post="+itoa64(trID)+"&action=edit")
	if !strings.Contains(ed, `id="in-category-`+itoa64(haberID)+`"`) || strings.Contains(ed, `id="in-category-`+itoa64(newsID)+`"`) {
		t.Error("the classic editor of the Turkish post does not list the Turkish categories only")
	}
	_, ed, _ = get(t, c, base+"/wp-admin/post?post="+itoa64(enID)+"&action=edit")
	if !strings.Contains(ed, `id="in-category-`+itoa64(newsID)+`"`) || strings.Contains(ed, `id="in-category-`+itoa64(haberID)+`"`) {
		t.Error("the classic editor of the English post does not list the English categories only")
	}
	// A classic editor save resolves typed tags among the post's language: the Turkish "Travel" (not
	// the English one with the same name), and a new tag gets the post's language.
	_, ed, _ = get(t, c, base+"/wp-admin/post?post="+itoa64(trID)+"&action=edit")
	if code, _, _ := post(t, c, base+"/wp-admin/post", url.Values{"_wpnonce": {formNonce(t, ed)}, "action": {"editpost"}, "visibility": {"public"},
		"post_ID": {itoa64(trID)}, "post_type": {"post"}, "post_title": {"Merhaba Gezginler"}, "content": {"<p>Turkish body</p>"}, "post_status": {"publish"},
		"save": {"Update"}, "post_category[]": {itoa64(haberID)}, "tax_input[post_tag]": {"Seyahat, Travel, Tarifler"}}); code != http.StatusFound {
		t.Fatalf("classic save of the Turkish post: %d", code)
	}
	trTags, _ := srv.store.TermsForPosts(ctx, []int64{trID}, "post_tag")
	if len(trTags[trID]) != 3 {
		t.Fatalf("the Turkish post has %d tags after a classic save, want 3", len(trTags[trID]))
	}
	for _, tg := range trTags[trID] {
		if tg.ID == tagID {
			t.Error("a classic save gave the Turkish post the English tag Travel")
		}
		if l := srv.pllObjLang(context.Background(), pllTerm, tg.ID); l != "tr" {
			t.Errorf("tag %s of the Turkish post is in %q, want tr", tg.Name, l)
		}
	}
	_ = srv.settings.Set(ctx, map[string]any{"classic-editor-replace": "block"})

	// Commerce: a product's Turkish translation lives under /tr/ and renders in Turkish.
	kupa := gooPllTranslate(t, c, base, srv, f.mug, "tr", "Kupa")
	kp, err := srv.store.GetPost(ctx, kupa.ID)
	if err != nil {
		t.Fatal(err)
	}
	kupaLink := srv.links(ctx).Permalink(kp)
	if !strings.HasPrefix(kupaLink, base+"/tr/") {
		t.Fatalf("Turkish product permalink %s", kupaLink)
	}
	code, prod, _ := get(t, c, kupaLink)
	if code != http.StatusOK || !strings.Contains(prod, `lang="tr-TR"`) || !strings.Contains(prod, "Kupa") {
		t.Errorf("Turkish product page: %d", code)
	}
	if !strings.Contains(prod, `hreflang="en"`) {
		t.Error("the Turkish product has no hreflang link to the English one")
	}
	// The product editor of a Turkish translation keeps a selected attribute term that has no Turkish
	// translation (here a term from before Multi Language), and saving keeps the attribute.
	gomlek := gooPllTranslate(t, c, base, srv, f.shirt, "tr", "Gomlek")
	if len(gomlek.Attributes) == 0 || len(gomlek.Attributes[0].Options) == 0 || gomlek.Attributes[0].Options[0] != "red" {
		t.Fatalf("the Turkish shirt's attributes: %+v", gomlek.Attributes)
	}
	gid, nonce, ged := gooCatEditor(t, c, base+"/wp-admin/post?post="+itoa64(gomlek.ID)+"&action=edit")
	sel := regexp.MustCompile(`<option value="(\d+)" selected="selected">Red</option>`).FindStringSubmatch(ged)
	if sel == nil {
		t.Fatal("the Turkish product editor drops the selected untranslated attribute term Red")
	}
	gooCatSave(t, c, base, gid, nonce, url.Values{"post_title": {"Gomlek"}, "product-type": {"simple"}, "_regular_price": {"40"},
		"attribute_names[0]": {"pa_color"}, "attribute_values[0][]": {sel[1]}, "attribute_visibility[0]": {"1"}, "attribute_position[0]": {"0"}})
	if g, err := srv.gooGetProduct(ctx, gid); err != nil || len(g.Attributes) != 1 || len(g.Attributes[0].Options) != 1 || g.Attributes[0].Options[0] != "red" {
		t.Errorf("saving the Turkish product lost its color: %+v %v", g.Attributes, err)
	}

	// An English product or product category under /tr/ goes to its own URL, as posts do.
	mp, _ := srv.store.GetPost(ctx, f.mug.ID)
	mugLink := srv.links(ctx).Permalink(mp)
	if code, _, loc := get(t, c, strings.Replace(mugLink, base+"/", base+"/tr/", 1)); code != http.StatusMovedPermanently || loc != mugLink {
		t.Errorf("English product under /tr/: %d %s, want 301 to %s", code, loc, mugLink)
	}
	catLink := srv.gooTermLink(ctx, f.cat)
	if code, _, loc := get(t, c, strings.Replace(catLink, base+"/", base+"/tr/", 1)); code != http.StatusMovedPermanently || loc != catLink {
		t.Errorf("English product category under /tr/: %d %s, want 301 to %s", code, loc, catLink)
	}

	// Pagination and feeds of the Turkish home stay Turkish.
	for i := 0; i < 11; i++ {
		pllCreatePost(t, c, base, "?lang=tr", "Ek yazı "+itoa64(int64(i)))
	}
	if _, trHome, _ = get(t, c, base+"/tr/"); !strings.Contains(trHome, `href="`+base+`/tr/page/2/"`) || strings.Contains(trHome, `href="`+base+`/page/2/"`) {
		t.Error("the Turkish home's pagination does not link to /tr/page/2/")
	}
	_, feed, _ := get(t, c, base+"/tr/feed/")
	if !strings.Contains(feed, "<language>tr-TR</language>") || !strings.Contains(feed, "<link>"+base+"/tr/</link>") || strings.Contains(feed, "Hello Travellers") {
		t.Errorf("the Turkish feed:\n%s", firstMatch(feed, `(?s)<channel>.*?</language>`))
	}

	// Page cache: visitors share cached pages; the language is remembered by the cookie script, not a
	// Set-Cookie header.
	if !srv.pageCacheOn() {
		t.Fatal("Pursae Cache is off")
	}
	first := anonGet(t, trLink)
	second := anonGet(t, trLink)
	if second.Header.Get("X-WPGo-Cache") != "hit" {
		t.Errorf("the Turkish post is not served from the cache: %q", second.Header.Get("X-WPGo-Cache"))
	}
	if first.Header.Get("Set-Cookie") != "" || !strings.Contains(bodyOf(first), `pll_language=tr`) {
		t.Error("the cached Turkish post does not remember its language with the cookie script")
	}
	// A returning Turkish visitor on / goes to /tr/ (the default language is hidden, so only with
	// browser detection); English stays on / and that answer is not cached for everyone.
	_, body, _ := get(t, c, base+"/wp-admin/admin?page=mlang_settings")
	m := regexp.MustCompile(`href="(/wp-admin/admin\?page=mlang_settings&amp;pll_action=activate&amp;module=browser[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Activate link for browser detection")
	}
	get(t, c, base+html.UnescapeString(m[1]))
	if !srv.pllOpts().Browser {
		t.Fatal("browser detection did not switch on")
	}
	for i := 0; i < 2; i++ {
		r := anonLangGet(t, base+"/", "en-US,en;q=0.9")
		if r.StatusCode != http.StatusOK || r.Header.Get("X-WPGo-Cache") == "hit" {
			t.Fatalf("English visitor %d on /: %d %q", i, r.StatusCode, r.Header.Get("X-WPGo-Cache"))
		}
	}
	if r := anonLangGet(t, base+"/", "tr-TR,tr;q=0.9"); r.StatusCode != http.StatusFound || r.Header.Get("Location") != base+"/tr/" {
		t.Errorf("Turkish visitor on / after English visits: %d %s, want 302 to /tr/", r.StatusCode, r.Header.Get("Location"))
	}
	if r := anonGet(t, base+"/", &http.Cookie{Name: pllCookie, Value: "tr"}); r.StatusCode != http.StatusFound {
		t.Errorf("visitor with pll_language=tr on /: %d, want 302", r.StatusCode)
	}
	// Choosing English in the language switcher on /tr/ (a link to / with this site as Referer) opens
	// the English home page and remembers English, whatever the cookie and the browser say; a reload of
	// / or a link from another site still follows the cookie (Polylang's get_home_language).
	trVisitor := func(ref, cookie string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, base+"/", nil)
		req.Header.Set("Accept-Language", "tr-TR,tr;q=0.9")
		req.AddCookie(&http.Cookie{Name: pllCookie, Value: cookie})
		if ref != "" {
			req.Header.Set("Referer", ref)
		}
		resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if r := trVisitor(base+"/tr/", "tr"); r.StatusCode != http.StatusOK {
		t.Errorf("English chosen in the switcher on /tr/: %d %s, want 200", r.StatusCode, r.Header.Get("Location"))
	} else if b := bodyOf(r); !strings.Contains(b, `id="pll-cookie-js"`) || !strings.Contains(b, pllCookie+`=en`) {
		t.Error("the English home page opened from the switcher does not set pll_language=en")
	}
	if r := trVisitor("", "en"); r.StatusCode != http.StatusOK {
		t.Errorf("Turkish browser with pll_language=en on /: %d %s, want 200", r.StatusCode, r.Header.Get("Location"))
	}
	for _, ref := range []string{base + "/", "https://elsewhere.example/tr/"} {
		if r := trVisitor(ref, "tr"); r.StatusCode != http.StatusFound || r.Header.Get("Location") != base+"/tr/" {
			t.Errorf("Referer %s with pll_language=tr on /: %d %s, want 302 to /tr/", ref, r.StatusCode, r.Header.Get("Location"))
		}
	}

	// Synchronization of custom fields keeps each translation's SEO text.
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=mlang_settings")
	if code, _, _ := post(t, c, base+"/wp-admin/admin?page=mlang_settings", url.Values{"_wpnonce": {formNonce(t, body)}, "module": {"sync"},
		"sync[post_meta]": {"1"}}); code != http.StatusFound {
		t.Fatal("save sync")
	}
	setMeta := func(id int64, kv map[string]any) {
		t.Helper()
		p, err := srv.store.GetPost(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range kv {
			p.Meta[k] = v
		}
		if err := srv.store.UpdatePost(ctx, p, 1); err != nil {
			t.Fatal(err)
		}
	}
	setMeta(trID, map[string]any{"_seogo_title": "Türkçe başlık", "_seogo_description": "Türkçe açıklama"})
	setMeta(enID, map[string]any{"_seogo_title": "English title", "_seogo_canonical": "https://example.com/en", "_seogo_noindex": "1", "shared_field": "x"})
	enPost, _ := srv.store.GetPost(ctx, enID)
	srv.pllSync(ctx, enPost)
	trPost, _ := srv.store.GetPost(ctx, trID)
	if trPost.Meta["_seogo_title"] != "Türkçe başlık" || trPost.Meta["_seogo_description"] != "Türkçe açıklama" {
		t.Errorf("synchronization overwrote the Turkish SEO text: %v %v", trPost.Meta["_seogo_title"], trPost.Meta["_seogo_description"])
	}
	if _, ok := trPost.Meta["_seogo_canonical"]; ok {
		t.Error("synchronization copied the English canonical")
	}
	if trPost.Meta["shared_field"] != "x" || trPost.Meta["_seogo_noindex"] != "1" {
		t.Errorf("synchronization did not copy the shared fields: %v %v", trPost.Meta["shared_field"], trPost.Meta["_seogo_noindex"])
	}
}

// TestGolylangContentModeCookie: with the language set from content a Turkish post has no language in
// its URL; it still becomes the remembered language (cookie script behind the page cache, Set-Cookie
// without it), not the default one pllFront read from the URL.
func TestGolylangContentModeCookie(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Türkçe", "tr_TR", "tr", "tr")
	_, body, _ := get(t, c, base+"/wp-admin/admin?page=mlang_settings")
	if code, _, _ := post(t, c, base+"/wp-admin/admin?page=mlang_settings", url.Values{"_wpnonce": {formNonce(t, body)}, "module": {"url"},
		"force_lang": {"0"}, "rewrite": {"1"}}); code != http.StatusFound {
		t.Fatal("save URL modifications")
	}
	if o := srv.pllOpts(); o.ForceLang != 0 || o.HideDefault {
		t.Fatalf("URL options: %+v", o)
	}
	_, trLink := pllCreatePost(t, c, base, "?lang=tr", "Merhaba Dünya")
	if strings.Contains(trLink, "lang=") || strings.Contains(trLink, "/tr/") {
		t.Fatalf("content mode permalink %s", trLink)
	}
	// Behind the page cache: the cookie script writes tr.
	r := anonGet(t, trLink)
	if b := bodyOf(r); !strings.Contains(b, `id="pll-cookie-js"`) || !strings.Contains(b, `pll_language=tr`) {
		t.Errorf("content mode, cache on: no cookie script for tr")
	}
	// Without the page cache: Set-Cookie carries tr, never the default language, and a visitor whose
	// cookie is already tr gets no cookie at all.
	if err := srv.settings.Set(context.Background(), map[string]any{"wpgo_cache_enabled": false}); err != nil {
		t.Fatal(err)
	}
	srv.configureCache()
	if srv.pageCacheOn() {
		t.Fatal("Pursae Cache is still on")
	}
	r = anonGet(t, trLink)
	if cks := strings.Join(r.Header.Values("Set-Cookie"), "\n"); strings.Count(cks, pllCookie+"=") != 1 || !strings.Contains(cks, pllCookie+"=tr;") {
		t.Errorf("content mode, cache off: Set-Cookie %q, want one pll_language=tr", cks)
	}
	if r := anonGet(t, trLink, &http.Cookie{Name: pllCookie, Value: "tr"}); strings.Contains(strings.Join(r.Header.Values("Set-Cookie"), "\n"), pllCookie+"=") {
		t.Error("reading a Turkish post resets the Turkish language cookie")
	}
	// The Atom feed of a Turkish request keeps a valid xml:base (the unfiltered root, not "/?lang=tr").
	if _, atom, _ := get(t, c, base+"/feed/atom/?lang=tr"); !strings.Contains(atom, `xml:base="`+base+`/wp-atom.php"`) {
		t.Errorf("Atom xml:base in query mode:\n%s", firstMatch(atom, `xml:base="[^"]*"`))
	}
	// The search form in query mode keeps the language in a hidden field.
	_, search, _ := get(t, c, base+"/?lang=tr&s=Merhaba")
	if !strings.Contains(search, `<input type="hidden" name="lang" value="tr" />`) {
		t.Errorf("query mode search form has no hidden lang field:\n%s", firstMatch(search, `(?s)<form role="search".*?</form>`))
	}
	if !strings.Contains(search, "Merhaba Dünya") {
		t.Error("a Turkish search in query mode does not find the Turkish post")
	}
}

func anonLangGet(t *testing.T, u, accept string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Accept-Language", accept)
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func firstMatch(s, expr string) string {
	if m := regexp.MustCompile(expr).FindString(s); m != "" {
		return m
	}
	if len(s) > 400 {
		return s[:400]
	}
	return s
}

// TestPllForgetKeepsRequestState: a language write drops only language and translation keys from the
// request cache, never the gettext, string capture or media state of the page being rendered.
func TestPllForgetKeepsRequestState(t *testing.T) {
	r := &pllReq{}
	ctx := context.WithValue(context.Background(), pllCtxKey{}, r)
	for _, k := range []string{"post:5", "post:6", "term:5", "fronttr:9", "known:Hello", "capture:n", "pll:mi18n:tr:5"} {
		r.cache.Store(k, "x")
	}
	pllForget(ctx, pllPost, 5)
	for k, want := range map[string]bool{"post:5": false, "fronttr:9": false, "post:6": true, "term:5": true, "known:Hello": true, "capture:n": true, "pll:mi18n:tr:5": true} {
		if _, ok := r.cache.Load(k); ok != want {
			t.Errorf("%s kept = %v, want %v", k, ok, want)
		}
	}
}
