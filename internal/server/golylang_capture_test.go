package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// pllSourceRow reads one pll_string_sources row ("" source when it does not exist).
func pllSourceRow(t *testing.T, srv *Server, original string) (source, domain, ctxt, url string) {
	t.Helper()
	_ = srv.store.Pool.QueryRow(context.Background(), `SELECT source, domain, context, url FROM pll_string_sources
		WHERE original = $1 ORDER BY (source = 'page') LIMIT 1`, original).Scan(&source, &domain, &ctxt, &url)
	return
}

// TestGolylangStringCapture: the page string pass (translation of theme and page text, user
// content left alone), capture (admins only, never the toolbar or user content), gettext template
// functions, the plugin translate import, the Strings screen grouping, delete and Clean.
func TestGolylangStringCapture(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	jar, _ := cookiejar.New(nil)
	anon := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// Plugin off: the gettext functions give the msgid back.
	if _, body, _ := get(t, anon, base+"/"); !strings.Contains(body, ">Skip to content</a>") {
		t.Fatal("the theme's skip link is missing while the plugin is off")
	}

	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	// A French post whose title is also a translated string: user content is never translated.
	pllCreatePost(t, c, base, "?lang=fr", "Twenty Twenty-Five")

	// An admin's visit records the page: theme gettext strings under the theme, page text as page.
	if code, body, _ := get(t, c, base+"/fr/"); code != http.StatusOK || strings.Contains(body, "pll:uc") {
		t.Fatalf("French home: %d (sentinel left: %v)", code, strings.Contains(body, "pll:uc"))
	}
	srv.pllCaptureFlush(ctx)
	if src, dom, _, _ := pllSourceRow(t, srv, "Skip to content"); src != "theme:twentytwentyfive" || dom != "twentytwentyfive" {
		t.Errorf("Skip to content: source %q domain %q", src, dom)
	}
	// Page text is recorded on pages of the default language only: the French page records none.
	if src, _, _, u := pllSourceRow(t, srv, "Designed with"); src != "" {
		t.Errorf("the French page recorded page text: source %q url %q", src, u)
	}
	_, _, _ = get(t, c, base+"/")
	srv.pllCaptureFlush(ctx)
	if src, _, _, u := pllSourceRow(t, srv, "Designed with"); src != "page" || u != "/" {
		t.Errorf("Designed with: source %q url %q", src, u)
	}
	for _, s := range []string{"Skip to toolbar", "Twenty Twenty-Five body"} { // the toolbar and post content are not page text
		if src, _, _, _ := pllSourceRow(t, srv, s); src != "" {
			t.Errorf("%q was recorded (source %q)", s, src)
		}
	}

	// Anonymous visitors never record page text.
	_, _ = srv.store.Pool.Exec(ctx, `DELETE FROM pll_string_sources WHERE source = 'page'`)
	srv.pllCaptureForget()
	_, _, _ = get(t, anon, base+"/")
	_, _, _ = get(t, anon, base+"/?s=injected+search+text")
	srv.pllCaptureFlush(ctx)
	var pageRows int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM pll_string_sources WHERE source = 'page'`).Scan(&pageRows)
	if pageRows != 0 {
		t.Errorf("anonymous visits recorded %d page strings", pageRows)
	}
	// Search results are not recorded for admins either.
	_, _, _ = get(t, c, base+"/?s=injected+search+text")
	srv.pllCaptureFlush(ctx)
	if src, _, _, _ := pllSourceRow(t, srv, "injected search text"); src != "" {
		t.Error("search text was recorded")
	}

	// Translations apply to theme gettext strings and page text, not to user content.
	for orig, tr := range map[string]string{"Skip to content": "Aller au contenu", "Designed with": "Conçu avec", "Twenty Twenty-Five": "Vingt-cinq"} {
		if err := srv.pllSetString(ctx, "fr", orig, tr); err != nil {
			t.Fatal(err)
		}
	}
	srv.pllFlushStrings()
	srv.purgeAll(ctx)
	_, fr, _ := get(t, anon, base+"/fr/")
	for _, want := range []string{">Aller au contenu</a>", "Conçu avec", ">Vingt-cinq</p>", ">Twenty Twenty-Five</a>"} {
		if !strings.Contains(fr, want) {
			t.Errorf("French home lacks %q", want)
		}
	}
	if strings.Contains(fr, "pll:uc") || strings.Contains(fr, "<pll-t") || strings.Contains(fr, "data-pll-") {
		t.Error("the French home carries sentinels or visual markers")
	}
	if _, en, _ := get(t, anon, base+"/"); !strings.Contains(en, ">Skip to content</a>") || strings.Contains(en, "Vingt-cinq") {
		t.Error("the English home was translated")
	}

	// Gettext context and the plugin import.
	gc, _ := gin.CreateTestContext(httptest.NewRecorder())
	gc.Request = httptest.NewRequest(http.MethodGet, "/fr/", nil)
	pllSet(gc, &pllReq{srv: srv, Lang: srv.pllLang(ctx, "fr"), Front: true, Path: "/"})
	_ = srv.pllSetString(ctx, "fr", "noun\x04Post", "Article")
	_ = srv.pllSetString(ctx, "fr", "Read more", "Lire la suite")
	srv.pllFlushStrings()
	if got := srv.pllGettext(gc, "Post", "", 0, "noun", "tt", "theme:twentytwentyfive"); got != "Article" {
		t.Errorf("_x: %q", got)
	}
	if got := srv.pllGettext(gc, "Post", "", 0, "", "tt", "theme:twentytwentyfive"); got != "Post" {
		t.Errorf("__ without context: %q", got)
	}
	if got := srv.pllGettext(gc, "%d comment", "%d comments", 2, "", "tt", "theme:twentytwentyfive"); got != "%d comments" {
		t.Errorf("_n: %q", got)
	}
	if got := (&pluginHost{s: srv}).Translate(gc, "hello-plugin", "Read more", ""); got != "Lire la suite" {
		t.Errorf("plugin translate: %q", got)
	}
	srv.pllCaptureFlush(ctx)
	if src, dom, _, _ := pllSourceRow(t, srv, "Read more"); src != "plugin:hello-plugin" || dom != "hello-plugin" {
		t.Errorf("plugin string: source %q domain %q", src, dom)
	}

	// Strings screen: groups, the context row, keys.
	_, _, _ = get(t, c, base+"/") // records the English page's text again
	srv.pllCaptureFlush(ctx)
	_, body, _ := get(t, c, base+"/wp-admin/admin?page=mlang_strings&group="+url.QueryEscape("Theme: Twenty Twenty-Five"))
	if !strings.Contains(body, "Skip to content") || !strings.Contains(body, `value="Aller au contenu"`) {
		t.Error("the theme group does not list its string with its translation")
	}
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=mlang_strings")
	for _, g := range []string{"Theme: Twenty Twenty-Five", "Page text", "Plugin: hello-plugin"} {
		if !strings.Contains(body, ">"+g+"</option>") {
			t.Errorf("no group %q", g)
		}
	}
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=mlang_strings&s=Post")
	if !strings.Contains(body, `value="noun&#4;Post"`) && !strings.Contains(body, "value=\"noun\x04Post\"") {
		t.Error("the context string is not keyed by context")
	}

	// Strings registered in the option by earlier versions are copied into the table once.
	_ = srv.settings.Set(ctx, map[string]any{"golylang_strings": []any{map[string]any{"name": "footer", "string": "Legacy footer text", "context": "My theme", "multiline": false}}})
	found := false
	for _, r := range srv.pllStrings(ctx) {
		if r.String == "Legacy footer text" && r.Group == "My theme" {
			found = true
		}
	}
	if src, dom, name, _ := pllSourceRow(t, srv, "Legacy footer text"); !found || src != "group:My theme" || dom != pllDomainRegistered || name != "footer" {
		t.Errorf("legacy string: listed %v, row %q %q %q", found, src, dom, name)
	}
	// Delete removes it from both.
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=mlang_strings")
	if code, _, _ := post(t, c, base+"/wp-admin/admin?page=mlang_strings", url.Values{"_wpnonce": {formNonce(t, body)}, "action": {"delete"},
		"strings[]": {"Legacy footer text"}}); code != http.StatusFound {
		t.Fatalf("delete: %d", code)
	}
	for _, r := range srv.pllStrings(ctx) {
		if r.String == "Legacy footer text" {
			t.Error("deleted string is still listed")
		}
	}

	// Clean: stale untranslated strings go, stale translated ones stay with their translation.
	_, _ = srv.store.Pool.Exec(ctx, `INSERT INTO pll_string_sources (original, source, last_seen) VALUES
		('Old untranslated', 'page', now() - interval '40 days'), ('Old translated', 'page', now() - interval '40 days')`)
	_ = srv.pllSetString(ctx, "fr", "Old translated", "Vieux")
	_ = srv.pllSetString(ctx, "fr", "Never listed", "Jamais")
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=mlang_strings")
	if code, _, _ := post(t, c, base+"/wp-admin/admin?page=mlang_strings", url.Values{"_wpnonce": {formNonce(t, body)}, "clean": {"1"}}); code != http.StatusFound {
		t.Fatalf("clean: %d", code)
	}
	if src, _, _, _ := pllSourceRow(t, srv, "Old untranslated"); src != "" {
		t.Error("Clean kept a stale untranslated string")
	}
	if src, _, _, _ := pllSourceRow(t, srv, "Old translated"); src != "page" {
		t.Error("Clean removed a translated string")
	}
	tr := srv.pllStringMap(ctx, "fr")
	if tr["Old translated"] != "Vieux" || tr["Never listed"] != "" || tr["Skip to content"] != "Aller au contenu" {
		t.Errorf("translations after Clean: %v", tr)
	}
	if tr["noun\x04Post"] != "Article" {
		t.Error("Clean removed the translation of a context string")
	}

	// Visual mode callback: markers on theme strings, none in user content.
	page := []byte(`<html><body><a href="#c">Skip to content</a><!--pll:uc post=3--><p>Skip to content</p><!--/pll:uc--><img alt="Skip to content" src="x.png"></body></html>`)
	l := srv.pllLang(ctx, "fr")
	visited := 0
	out := string(srv.pllApplyStrings(gc, page, l, func(s, attr string) string { visited++; return "m" + itoa64(int64(visited)) }))
	if !strings.Contains(out, `<pll-t data-pll-s="m1">Aller au contenu</pll-t>`) || !strings.Contains(out, `<p>Skip to content</p>`) ||
		!strings.Contains(out, `alt="Aller au contenu"`) || !strings.Contains(out, `data-pll-a-alt="m2"`) {
		t.Errorf("visual pass: %s", out)
	}
}

// TestGolylangCaptureDefaultLanguageOnly: page text is recorded on pages of the default language
// only (a translated page shows its own text, which is no source string), the language switcher's
// names are never page text, and Clean removes such rows recorded by earlier versions.
func TestGolylangCaptureDefaultLanguageOnly(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Türkçe", "tr_TR", "tr", "tr")
	if def := srv.pllDefault(ctx); def == nil || def.Slug != "en" {
		t.Fatalf("default language: %+v", def)
	}

	// The string pass itself: an administrator's page in Turkish records nothing, in English it does.
	admin, _ := srv.store.UserByID(ctx, 1)
	page := []byte(`<html><body><p>Öne çıkanlar listesi</p><p>Visible footer text</p></body></html>`)
	for _, tc := range []struct {
		lang, path string
		want       bool
	}{{"tr", "/tr/highlights/", false}, {"en", "/highlights/", true}} {
		_, _ = srv.store.Pool.Exec(ctx, `DELETE FROM pll_string_sources WHERE source = 'page'`)
		srv.pllCaptureForget()
		gc, _ := gin.CreateTestContext(httptest.NewRecorder())
		gc.Request = httptest.NewRequest(http.MethodGet, tc.path, nil)
		gc.Set(ctxUser, admin)
		pllSet(gc, &pllReq{srv: srv, Lang: srv.pllLang(ctx, tc.lang), Front: true, Path: tc.path})
		for _, visual := range []func(string, string) string{nil, func(string, string) string { return "m" }} {
			_ = srv.pllApplyStrings(gc, page, srv.pllLang(ctx, tc.lang), visual)
		}
		srv.pllCaptureFlush(ctx)
		for _, s := range []string{"Öne çıkanlar listesi", "Visible footer text"} {
			src, _, _, u := pllSourceRow(t, srv, s)
			if got := src == "page"; got != tc.want {
				t.Errorf("%s page: %q recorded=%v (url %q), want %v", tc.lang, s, got, u, tc.want)
			}
		}
	}

	// Settings > General with a non-default language selected points to Languages > Translations.
	hint := `translate it in <a href="/wp-admin/admin?page=mlang_strings&amp;group=WordPress">`
	if _, body, _ := get(t, c, base+"/wp-admin/options-general?lang=tr"); strings.Count(body, hint) != 2 || !strings.Contains(body, "Tagline in Türkçe") {
		t.Error("Settings > General (Türkçe) lacks the Site Title and Tagline hints")
	}
	if _, body, _ := get(t, c, base+"/wp-admin/options-general?lang=en"); strings.Contains(body, hint) {
		t.Error("Settings > General (English) shows the translation hint")
	}

	// Real pages with the switcher in the menu: neither /tr/ nor the English page records language
	// names, and /tr/ records no page text at all.
	if err := srv.wbSetActive(ctx, true); err != nil { // Hello Pursae prints the classic menu markup
		t.Fatal(err)
	}
	menu, err := srv.store.CreateMenu(ctx, "Main")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.AddMenuItem(ctx, &store.EditItem{MenuID: menu, Kind: "custom", Object: "custom", URL: "#pll_switcher", Label: "Languages",
		Classes: "pll-show-flags", Status: "publish"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetMenuLocations(ctx, map[string]int64{"primary": menu}); err != nil {
		t.Fatal(err)
	}
	o := srv.pllOpts() // the same menu in both languages' primary location
	o.NavMenus["hello-pursae"] = map[string]map[string]int64{"primary": {"en": menu, "tr": menu}}
	if err := srv.pllSaveOpts(ctx, o); err != nil {
		t.Fatal(err)
	}
	pllCreatePost(t, c, base, "?lang=tr", "Öne çıkanlar")
	pllCreatePost(t, c, base, "?lang=en", "Highlights")
	_, _ = srv.store.Pool.Exec(ctx, `DELETE FROM pll_string_sources WHERE source = 'page'`)
	srv.pllCaptureForget()
	srv.purgeAll(ctx)
	_, trHome, _ := get(t, c, base+"/tr/")
	if !strings.Contains(trHome, "English") || !strings.Contains(trHome, "Türkçe") {
		t.Fatalf("the Turkish home has no language switcher: %s", trHome)
	}
	_, _, _ = get(t, c, base+"/")
	srv.pllCaptureFlush(ctx)
	var trRows, nameRows, enRows int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE url LIKE '/tr%'),
		count(*) FILTER (WHERE original ~ '(English|Türkçe)'), count(*) FILTER (WHERE url = '/')
		FROM pll_string_sources WHERE source = 'page'`).Scan(&trRows, &nameRows, &enRows)
	if trRows != 0 || nameRows != 0 {
		t.Errorf("page rows: %d from /tr/, %d language names", trRows, nameRows)
	}
	if enRows == 0 {
		t.Error("the English home recorded no page text")
	}

	// Clean removes untranslated page text from other-language URLs and language names; translated
	// rows and English page text stay.
	_, _ = srv.store.Pool.Exec(ctx, `INSERT INTO pll_string_sources (original, source, url) VALUES
		('Junk Turkish text', 'page', '/tr/highlights/'), ('Junk language root', 'page', '/tr'),
		('Junk old URL form', 'page', '/language/tr/projects/'), ('English', 'page', '/tr/projects/'),
		('Türkçe', 'page', '/'), ('Translated on tr', 'page', '/tr/x/'), ('Normal English text', 'page', '/trips/'),
		('Theme text', 'theme:x', '/tr/')`)
	_ = srv.pllSetString(ctx, "tr", "Translated on tr", "Çevrildi")
	_, body, _ := get(t, c, base+"/wp-admin/admin?page=mlang_strings")
	if code, _, _ := post(t, c, base+"/wp-admin/admin?page=mlang_strings", url.Values{"_wpnonce": {formNonce(t, body)}, "clean": {"1"}}); code != http.StatusFound {
		t.Fatalf("clean: %d", code)
	}
	for s, keep := range map[string]bool{"Junk Turkish text": false, "Junk language root": false, "Junk old URL form": false, "English": false,
		"Türkçe": false, "Translated on tr": true, "Normal English text": true, "Theme text": true} {
		if src, _, _, _ := pllSourceRow(t, srv, s); (src != "") != keep {
			t.Errorf("after Clean %q: source %q, want kept=%v", s, src, keep)
		}
	}
}
