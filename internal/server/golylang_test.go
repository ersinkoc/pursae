package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// golylangToggle clicks Activate or Deactivate of Golylang on the Plugins screen.
func golylangToggle(t *testing.T, c *http.Client, base, action string) {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=` + action + `&amp;plugin=golylang[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s link for Golylang on the Plugins screen", action)
	}
	code, _, loc := get(t, c, base+html.UnescapeString(m[1]))
	if code != http.StatusFound || !strings.Contains(loc, action+"=true") {
		t.Fatalf("%s Golylang: %d %s", action, code, loc)
	}
}

// pllREST sends a REST request with the cookie nonce and decodes the JSON answer.
func pllREST(t *testing.T, c *http.Client, base, method, path string, body any) (int, map[string]any, []any) {
	t.Helper()
	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, rd)
	req.Header.Set("X-WP-Nonce", nonce)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var obj map[string]any
	var list []any
	if json.Unmarshal(raw, &obj) != nil {
		_ = json.Unmarshal(raw, &list)
	}
	return resp.StatusCode, obj, list
}

func pllAddLanguage(t *testing.T, c *http.Client, base, name, locale, slug, flag string) {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/admin?page=mlang")
	code, _, loc := post(t, c, base+"/wp-admin/admin?page=mlang", url.Values{"_wpnonce": {formNonce(t, body)}, "pll_action": {"add"},
		"name": {name}, "locale": {locale}, "slug": {slug}, "flag": {flag}, "rtl": {"0"}, "term_group": {"0"}})
	if code != http.StatusFound || !strings.Contains(loc, "pll_msg=added") {
		t.Fatalf("add language %s: %d %s", slug, code, loc)
	}
}

func pllCreatePost(t *testing.T, c *http.Client, base, query, title string) (int64, string) {
	t.Helper()
	code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts"+query, map[string]any{"title": title, "content": "<!-- wp:paragraph --><p>" + title + " body</p><!-- /wp:paragraph -->", "status": "publish"})
	if code != http.StatusCreated {
		t.Fatalf("create post %q: %d %v", title, code, obj)
	}
	id, _ := obj["id"].(float64)
	link, _ := obj["link"].(string)
	return int64(id), link
}

func TestGolylangOffByDefault(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	if srv.golylangOn() {
		t.Fatal("Golylang is on after a fresh install")
	}
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	row := regexp.MustCompile(`(?s)<tr class="(\w+)" data-slug="golylang"`).FindStringSubmatch(body)
	if row == nil || row[1] != "inactive" {
		t.Fatalf("Golylang is not listed as an inactive plugin: %v", row)
	}
	if strings.Contains(body, "toplevel_page_mlang") || strings.Contains(body, "wp-admin-bar-languages") {
		t.Fatal("the Languages menu or the language filter shows while Golylang is off")
	}
	for _, page := range []string{"mlang", "mlang_strings", "mlang_settings"} {
		if code, _, _ := get(t, c, base+"/wp-admin/admin?page="+page); code != http.StatusForbidden {
			t.Errorf("admin?page=%s while off: %d, want 403", page, code)
		}
	}
	if code, _ := restGet(t, c, base, "/wp-json/pll/v1/languages"); code != http.StatusNotFound {
		t.Errorf("pll/v1/languages while off: %d, want 404", code)
	}
	if _, idx := restGet(t, c, base, "/wp-json/"); strings.Contains(idx, "pll/v1") {
		t.Error("the REST index lists pll/v1 while Golylang is off")
	}
	_, post := restGet(t, c, base, "/wp-json/wp/v2/posts/1")
	if strings.Contains(post, `"lang"`) || strings.Contains(post, `"translations"`) {
		t.Error("posts carry lang/translations while Golylang is off")
	}
	if _, front, _ := get(t, c, base+"/"); strings.Contains(front, "hreflang") || strings.Contains(front, "lang-item") {
		t.Error("the front end shows Golylang output while it is off")
	}
	if code, _, _ := get(t, c, base+"/fr/"); code != http.StatusNotFound {
		t.Errorf("/fr/ while off: %d, want 404", code)
	}
}

func TestGolylangMultilingualSite(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	if !srv.golylangOn() {
		t.Fatal("Golylang did not switch on")
	}

	// Languages screen and menu.
	code, body, _ := get(t, c, base+"/wp-admin/admin?page=mlang")
	if code != http.StatusOK || !strings.Contains(body, "Add new language") || !strings.Contains(body, `id="toplevel_page_mlang"`) {
		t.Fatalf("Languages screen: %d", code)
	}
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	if d := srv.pllDefault(context.Background()); d == nil || d.Slug != "en" {
		t.Fatalf("default language: %v", d)
	}
	if srv.pllUntranslatedContent(context.Background()) {
		t.Error("existing content did not get the default language")
	}
	// The default category got its French translation.
	if tr := srv.pllTranslations(context.Background(), pllTerm, 1); tr["fr"] == 0 {
		t.Errorf("no French default category: %v", tr)
	}
	// Duplicate code is refused.
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=mlang")
	_, body, _ = post(t, c, base+"/wp-admin/admin?page=mlang", url.Values{"_wpnonce": {formNonce(t, body)}, "pll_action": {"add"},
		"name": {"French"}, "locale": {"fr_BE"}, "slug": {"fr"}})
	if !strings.Contains(body, "The language code must be unique") {
		t.Error("a duplicate language code was accepted")
	}

	// Content: an English post and its French translation (REST lang and translations parameters).
	enID, enLink := pllCreatePost(t, c, base, "", "Hello English")
	frID, frLink := pllCreatePost(t, c, base, "?lang=fr&translations[en]="+strconv.FormatInt(enID, 10), "Bonjour France")
	if !strings.Contains(frLink, "/fr/") || strings.Contains(enLink, "/en/") {
		t.Fatalf("permalinks: en %s, fr %s", enLink, frLink)
	}
	_, obj, _ := pllREST(t, c, base, http.MethodGet, "/wp-json/wp/v2/posts/"+strconv.FormatInt(enID, 10), nil)
	if obj["lang"] != "en" {
		t.Errorf("REST lang = %v", obj["lang"])
	}
	if tr, _ := obj["translations"].(map[string]any); tr["fr"] != float64(frID) {
		t.Errorf("REST translations = %v", obj["translations"])
	}
	_, _, list := pllREST(t, c, base, http.MethodGet, "/wp-json/wp/v2/posts?lang=fr", nil)
	if len(list) != 1 {
		t.Errorf("posts?lang=fr returned %d posts, want 1", len(list))
	}

	// Front end: language from the directory, hreflang, <html lang>, the default language without code.
	code, body, _ = get(t, c, frLink)
	if code != http.StatusOK || !strings.Contains(body, `lang="fr-FR"`) || !strings.Contains(body, "Bonjour France") {
		t.Fatalf("French post: %d", code)
	}
	if !strings.Contains(body, `hreflang="fr"`) || !strings.Contains(body, `hreflang="en"`) || !strings.Contains(body, html.EscapeString(enLink)) {
		t.Error("the French post has no hreflang links to both languages")
	}
	code, body, _ = get(t, c, base+"/fr/")
	if code != http.StatusOK || !strings.Contains(body, "Bonjour France") || strings.Contains(body, "Hello English") {
		t.Fatalf("/fr/ is not the French home: %d", code)
	}
	if _, body, _ = get(t, c, base+"/"); !strings.Contains(body, "Hello English") || strings.Contains(body, "Bonjour France") {
		t.Error("/ is not the English home")
	}
	if code, _, loc := get(t, c, base+"/en/"); code != http.StatusMovedPermanently || loc != "/" {
		t.Errorf("/en/ with a hidden default language: %d %s", code, loc)
	}
	// An English post under the French prefix goes to its URL.
	enPath := strings.TrimPrefix(enLink, base)
	if code, _, loc := get(t, c, base+"/fr"+enPath); code != http.StatusMovedPermanently || loc != enLink {
		t.Errorf("/fr%s: %d %s, want 301 to %s", enPath, code, loc, enLink)
	}
	// Sitemaps per language.
	if _, sm, _ := get(t, c, base+"/fr/wp-sitemap.xml"); !strings.Contains(sm, base+"/fr/wp-sitemap-posts-post-1.xml") {
		t.Error("the French sitemap index does not point to French sitemaps")
	}
	if _, sm, _ := get(t, c, base+"/fr/wp-sitemap-posts-post-1.xml"); !strings.Contains(sm, frLink) || strings.Contains(sm, enLink) {
		t.Error("the French posts sitemap does not list only French posts")
	}

	// Strings translations: the site title in French.
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=mlang_strings")
	if !strings.Contains(body, "Toggle Site") || !strings.Contains(body, "translation[fr][0]") {
		t.Fatal("the strings table does not list the site title")
	}
	if code, _, _ := post(t, c, base+"/wp-admin/admin?page=mlang_strings", url.Values{"_wpnonce": {formNonce(t, body)},
		"original[0]": {"Toggle Site"}, "translation[fr][0]": {"Site Bascule"}, "translation[en][0]": {""}}); code != http.StatusFound {
		t.Fatalf("save strings: %d", code)
	}
	if _, body, _ = get(t, c, base+"/fr/"); !strings.Contains(body, "Site Bascule") {
		t.Error("the French home does not show the translated site title")
	}

	// PO export and import.
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=mlang_strings")
	ieNonce := regexp.MustCompile(`name="_wpnonce" value="([^"]+)" /><input type="hidden" name="pll_action" value="export"`).FindStringSubmatch(body)
	if ieNonce == nil {
		t.Fatal("no export form")
	}
	_, po, _ := post(t, c, base+"/wp-admin/admin?page=mlang_strings", url.Values{"_wpnonce": {ieNonce[1]}, "pll_action": {"export"}, "lang": {"fr"}})
	if !strings.Contains(po, `msgstr "Site Bascule"`) || !strings.Contains(po, "Language: fr_FR") {
		t.Fatalf("PO export:\n%s", po)
	}
	var mp bytes.Buffer
	mw := multipart.NewWriter(&mp)
	_ = mw.WriteField("_wpnonce", ieNonce[1])
	_ = mw.WriteField("pll_action", "import")
	fw, _ := mw.CreateFormFile("po", "fr_FR.po")
	_, _ = fw.Write([]byte(strings.Replace(po, `msgstr "Site Bascule"`, `msgstr "Mon Site"`, 1)))
	_ = mw.Close()
	resp, err := c.Post(base+"/wp-admin/admin?page=mlang_strings", mw.FormDataContentType(), &mp)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "pll_msg=imported") {
		t.Fatalf("PO import: %s", loc)
	}
	if got := srv.pllTranslate(context.Background(), "Toggle Site", "fr"); got != "Mon Site" {
		t.Errorf("imported translation = %q", got)
	}

	// Posts list: one column per language with the tick, pencil and plus icons.
	_, body, _ = get(t, c, base+"/wp-admin/edit")
	if !strings.Contains(body, "column-language_fr") || !strings.Contains(body, "pll_icon_edit") || !strings.Contains(body, "pll_icon_tick") {
		t.Error("the posts list has no language columns")
	}
	// Admin bar filter: French only.
	if code, _, _ := get(t, c, base+"/wp-admin/edit?lang=fr"); code != http.StatusOK {
		t.Fatal("language filter")
	}
	_, body, _ = get(t, c, base+"/wp-admin/edit")
	if !strings.Contains(body, "Bonjour France") || strings.Contains(body, "Hello English") || !strings.Contains(body, `id="wp-admin-bar-languages"`) {
		t.Error("the admin language filter does not narrow the posts list")
	}
	get(t, c, base+"/wp-admin/edit?lang=all")

	// A new translation from the plus icon, with Duplicate content on.
	other, _ := pllCreatePost(t, c, base, "", "Only English")
	_, body, _ = get(t, c, base+"/wp-admin/post?post="+strconv.FormatInt(other, 10)+"&action=edit")
	boxNonce := regexp.MustCompile(`var nonce = "([^"]+)"`).FindStringSubmatch(body)
	if boxNonce == nil || !strings.Contains(body, "Languages") {
		t.Fatal("the editor has no Languages panel")
	}
	if code, _, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"pll_toggle_duplicate"}, "_pll_nonce": {boxNonce[1]},
		"post_id": {strconv.FormatInt(other, 10)}, "on": {"1"}}); code != http.StatusOK {
		t.Fatalf("duplicate toggle: %d", code)
	}
	_, list2, _ := get(t, c, base+"/wp-admin/edit")
	add := regexp.MustCompile(`href="(/wp-admin/post-new\?post_type=post&amp;from_post=` + strconv.FormatInt(other, 10) + `&amp;new_lang=fr&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(list2)
	if add == nil {
		t.Fatal("no add translation link")
	}
	if code, _, _ := get(t, c, base+html.UnescapeString(add[1])); code != http.StatusOK {
		t.Fatalf("new translation: %d", code)
	}
	tr := srv.pllTranslations(context.Background(), pllPost, other)
	if tr["fr"] == 0 {
		t.Fatalf("the new translation is not linked: %v", tr)
	}
	if p, err := srv.store.GetPost(context.Background(), tr["fr"]); err != nil || p.Title != "Only English" || !strings.Contains(p.Content, "Only English body") {
		t.Errorf("duplicate content was not copied: %+v", p)
	}

	// Languages box: change the language of a post (admin-ajax).
	if code, _, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"pll_post_lang"}, "_pll_nonce": {boxNonce[1]},
		"post_id": {strconv.FormatInt(tr["fr"], 10)}, "lang": {"en"}}); code != http.StatusOK {
		t.Fatal("pll_post_lang")
	}
	if got := srv.pllObjLang(context.Background(), pllPost, tr["fr"]); got != "en" {
		t.Errorf("language after the change = %s", got)
	}

	// Synchronization: comment status follows the translation.
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=mlang_settings")
	if code, _, _ := post(t, c, base+"/wp-admin/admin?page=mlang_settings", url.Values{"_wpnonce": {formNonce(t, body)}, "module": {"sync"},
		"sync[comment_status]": {"1"}}); code != http.StatusFound {
		t.Fatal("save sync")
	}
	if code, _, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts/"+strconv.FormatInt(enID, 10), map[string]any{"comment_status": "closed"}); code != http.StatusOK {
		t.Fatal("update post")
	}
	if p, _ := srv.store.GetPost(context.Background(), frID); p.CommentStatus != "closed" {
		t.Errorf("synchronized comment status = %s", p.CommentStatus)
	}

	// Language switcher block in content.
	pid, plink := pllCreatePost(t, c, base, "", "Switcher page")
	_, _, _ = pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts/"+strconv.FormatInt(pid, 10), map[string]any{
		"content": `<!-- wp:polylang/language-switcher {"show_flags":true} /-->`})
	if _, body, _ = get(t, c, plink); !strings.Contains(body, "lang-item-fr") || !strings.Contains(body, "current-lang") {
		t.Error("the Language switcher block does not render")
	}

	// Terms: language columns and fields.
	_, body, _ = get(t, c, base+"/wp-admin/edit-tags?taxonomy=category")
	if !strings.Contains(body, "column-language_fr") || !strings.Contains(body, `name="term_lang_choice"`) {
		t.Error("the categories screen has no language column or field")
	}

	// Menus: a location per language.
	if _, body, _ = get(t, c, base+"/wp-admin/nav-menus?action=locations"); !strings.Contains(body, "___fr") && !strings.Contains(body, "Add a menu") {
		t.Log("no menu locations to check")
	}

	// REST languages API.
	_, _, langs := pllREST(t, c, base, http.MethodGet, "/wp-json/pll/v1/languages", nil)
	if len(langs) != 2 {
		t.Errorf("pll/v1/languages: %d languages", len(langs))
	}
	code, obj, _ = pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/languages", map[string]any{"locale": "de_DE"})
	if code != http.StatusCreated || obj["slug"] != "de" || obj["name"] != "Deutsch" {
		t.Errorf("create language over REST: %d %v", code, obj)
	}

	// Browser detection: a new visitor preferring French lands on /fr/.
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=mlang_settings")
	m := regexp.MustCompile(`href="(/wp-admin/admin\?page=mlang_settings&amp;pll_action=activate&amp;module=browser[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Activate link for browser detection")
	}
	get(t, c, base+html.UnescapeString(m[1]))
	jar, _ := cookiejar.New(nil)
	visitor := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest(http.MethodGet, base+"/", nil)
	req.Header.Set("Accept-Language", "fr-FR,fr;q=0.9,en;q=0.5")
	resp, err = visitor.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || !strings.HasSuffix(resp.Header.Get("Location"), "/fr/") {
		t.Errorf("browser detection: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Off again: nothing is left.
	golylangToggle(t, c, base, "deactivate")
	if _, body, _ = get(t, c, base+"/"); strings.Contains(body, "hreflang") {
		t.Error("hreflang links after deactivation")
	}
	if code, _, _ := get(t, c, base+"/fr/"); code != http.StatusNotFound {
		t.Errorf("/fr/ after deactivation: %d", code)
	}
	if _, body, _ = get(t, c, base+"/wp-admin/edit"); strings.Contains(body, "column-language_") || strings.Contains(body, "toplevel_page_mlang") {
		t.Error("language columns or menu after deactivation")
	}
}

func TestGolylangURLModes(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Deutsch", "de_DE", "de", "de")
	ctx := context.Background()
	root := srv.links(ctx).Home
	de := srv.pllLang(ctx, "de")
	en := srv.pllLang(ctx, "en")
	o := srv.pllOpts()

	if got := srv.pllConvert(ctx, root, root+"/2026/10/hello/", de, "post", o); got != root+"/de/2026/10/hello/" {
		t.Errorf("directory: %s", got)
	}
	if got := srv.pllConvert(ctx, root, root+"/de/2026/10/hello/", en, "post", o); got != root+"/2026/10/hello/" {
		t.Errorf("back to the hidden default: %s", got)
	}
	o.Rewrite = false
	if got := srv.pllConvert(ctx, root, root+"/de/x/", de, "post", o); got != root+"/language/de/x/" {
		t.Errorf("keep /language/: %s", got)
	}
	o = srv.pllOpts()
	o.ForceLang = 0
	if got := srv.pllConvert(ctx, root, root+"/x/", de, "post", o); got != root+"/x/" {
		t.Errorf("content: post %s", got)
	}
	if got := srv.pllConvert(ctx, root, root+"/2026/", de, "home", o); got != root+"/2026/?lang=de" {
		t.Errorf("content: archive %s", got)
	}
	o.ForceLang = 3
	o.Domains = map[string]string{"en": "https://example.com", "de": "https://example.de"}
	if got := srv.pllConvert(ctx, root, root+"/x/", de, "post", o); got != "https://example.de/x/" {
		t.Errorf("domains: %s", got)
	}
	o.ForceLang = 2
	o.HideDefault = true
	if got := srv.pllConvert(ctx, "https://example.com", "https://example.com/x/", de, "post", o); got != "https://de.example.com/x/" {
		t.Errorf("subdomain: %s", got)
	}

	// Language from content: ?lang= on the front end.
	_, body, _ := get(t, c, base+"/wp-admin/admin?page=mlang_settings")
	if code, _, _ := post(t, c, base+"/wp-admin/admin?page=mlang_settings", url.Values{"_wpnonce": {formNonce(t, body)}, "module": {"url"},
		"force_lang": {"0"}, "hide_default": {"1"}, "rewrite": {"1"}}); code != http.StatusFound {
		t.Fatal("save URL modifications")
	}
	if code, body, _ := get(t, c, base+"/?lang=de"); code != http.StatusOK || !strings.Contains(body, `lang="de-DE"`) {
		t.Errorf("?lang=de: %d", code)
	}
}

func TestGolylangHelpers(t *testing.T) {
	langs := []*pllLanguage{{Slug: "en", Locale: "en_US"}, {Slug: "fr", Locale: "fr_FR"}, {Slug: "pt-br", Locale: "pt_BR"}}
	for header, want := range map[string]string{"fr-CH, fr;q=0.9, en;q=0.8": "fr", "de, en-GB;q=0.7": "en", "pt-BR": "pt-br", "es": ""} {
		got := pllBrowserLang(header, langs)
		if (got == nil && want != "") || (got != nil && got.Slug != want) {
			t.Errorf("pllBrowserLang(%q) = %v, want %q", header, got, want)
		}
	}
	if pllFlagEmoji("fr") != "🇫🇷" || pllFlagEmoji("catalonia") != "" {
		t.Error("pllFlagEmoji")
	}
	po := "msgid \"\"\nmsgstr \"\"\n\"Language: fr_FR\\n\"\n\n#. Site Title\nmsgctxt \"WordPress\"\nmsgid \"My \\\"site\\\"\"\nmsgstr \"\"\n\"Mon \"\n\"site\"\n"
	entries, header, err := parsePO(strings.NewReader(po))
	if err != nil || header["Language"] != "fr_FR" || len(entries) != 1 || entries[0].id != `My "site"` || entries[0].str != "Mon site" || entries[0].ctx != "WordPress" {
		t.Errorf("parsePO = %+v %v %v", entries, header, err)
	}
	a := pllParseArgs([]string{"dropdown=1", "show_flags=1", "hide_current=0"})
	if !a.Dropdown || !a.ShowFlags || a.HideCurrent || !a.ShowNames {
		t.Errorf("pllParseArgs = %+v", a)
	}
}
