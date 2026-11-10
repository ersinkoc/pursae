package server

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestGolylangDeleteDefaultLanguagePicksAnother: deleting the default language moves its content to the
// remaining language, which becomes the default.
func TestGolylangDeleteDefaultLanguagePicksAnother(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Deutsch", "de_DE", "de", "de")
	ctx := context.Background()
	enID, _ := pllCreatePost(t, c, base, "", "Hello English")
	if err := srv.pllDeleteLanguageWith(ctx, srv.pllLang(ctx, "en"), "default"); err != nil {
		t.Fatal(err)
	}
	if srv.pllLang(ctx, "en") != nil {
		t.Fatal("English is still there")
	}
	if d := srv.pllDefault(ctx); d == nil || d.Slug != "de" {
		t.Fatalf("default after deleting English = %v, want de", d)
	}
	if got := srv.pllObjLang(ctx, pllPost, enID); got != "de" {
		t.Errorf("the English post moved to %q, want de", got)
	}
}

// TestGolylangAjaxLinkTranslation: the Languages box links, re-links and unlinks a translation.
func TestGolylangAjaxLinkTranslation(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	ctx := context.Background()
	enID, _ := pllCreatePost(t, c, base, "", "Hello English")
	frID, _ := pllCreatePost(t, c, base, "?lang=fr", "Bonjour")
	fr2ID, _ := pllCreatePost(t, c, base, "?lang=fr", "Salut")
	_, body, _ := get(t, c, base+"/wp-admin/post?post="+strconv.FormatInt(enID, 10)+"&action=edit")
	nm := regexp.MustCompile(`var nonce = "([^"]+)"`).FindStringSubmatch(body)
	if nm == nil {
		t.Fatal("no Languages box nonce")
	}
	link := func(nonce string, target int64) int {
		code, _, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"pll_link_translation"}, "_pll_nonce": {nonce},
			"post_id": {strconv.FormatInt(enID, 10)}, "lang": {"fr"}, "target": {strconv.FormatInt(target, 10)}})
		return code
	}
	if code := link("bad", frID); code != http.StatusForbidden {
		t.Fatalf("bad nonce: %d", code)
	}
	if code := link(nm[1], frID); code != http.StatusOK {
		t.Fatalf("link: %d", code)
	}
	if tr := srv.pllTranslations(ctx, pllPost, enID); tr["fr"] != frID {
		t.Fatalf("after linking: %v", tr)
	}
	if code := link(nm[1], fr2ID); code != http.StatusOK {
		t.Fatalf("re-link: %d", code)
	}
	if tr := srv.pllTranslations(ctx, pllPost, enID); tr["fr"] != fr2ID {
		t.Fatalf("after re-linking: %v", tr)
	}
	if tr := srv.pllTranslations(ctx, pllPost, frID); tr["en"] != 0 {
		t.Errorf("the replaced translation is still linked: %v", tr)
	}
	if code := link(nm[1], 0); code != http.StatusOK {
		t.Fatalf("unlink: %d", code)
	}
	if tr := srv.pllTranslations(ctx, pllPost, enID); tr["fr"] != 0 {
		t.Errorf("after unlinking: %v", tr)
	}
}

// TestGolylangDomainsSetting: the per-language domain mode validates its URLs and keeps nothing on error.
func TestGolylangDomainsSetting(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Deutsch", "de_DE", "de", "de")
	// the domain mode needs pretty permalinks
	if err := srv.settings.Set(context.Background(), map[string]any{"permalink_structure": "/%postname%/"}); err != nil {
		t.Skipf("cannot set permalinks: %v", err)
	}
	save := func(vals url.Values) string {
		_, body, _ := get(t, c, base+"/wp-admin/admin?page=mlang_settings&module=url")
		vals.Set("_wpnonce", formNonce(t, body))
		vals.Set("module", "url")
		vals.Set("force_lang", "3")
		_, _, loc := post(t, c, base+"/wp-admin/admin?page=mlang_settings", vals)
		return loc
	}
	// a missing / invalid domain is refused
	if loc := save(url.Values{"domains[en]": {"https://example.com/"}, "domains[de]": {"not a url"}}); !strings.Contains(loc, "pll_error") {
		t.Fatalf("invalid domain accepted: %s", loc)
	}
	if o := srv.pllOpts(); o.ForceLang == 3 {
		t.Error("force_lang was saved despite the invalid domain")
	}
	if loc := save(url.Values{"domains[en]": {"https://example.com/"}, "domains[de]": {"https://example.de"}}); !strings.Contains(loc, "settings-updated") {
		t.Fatalf("valid domains refused: %s", loc)
	}
	o := srv.pllOpts()
	if o.ForceLang != 3 || o.Domains["en"] != "https://example.com" || o.Domains["de"] != "https://example.de" {
		t.Errorf("saved options: force_lang %d domains %v", o.ForceLang, o.Domains)
	}
}

// TestGolylangShareSlugsWithoutCommerce: with Share slugs two translations may use the same slug; without
// it the second one gets a suffix.
func TestGolylangShareSlugsWithoutCommerce(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	ctx := context.Background()
	slugOf := func(id int64) string {
		var s string
		_ = srv.store.Pool.QueryRow(ctx, `SELECT slug FROM posts WHERE id = $1`, id).Scan(&s)
		return s
	}
	o := srv.pllOpts()
	o.ShareSlugs = false
	_ = srv.pllSaveOpts(ctx, o)
	srv.pllFlush()
	enID, _ := pllCreatePost(t, c, base, "", "About us")
	frID, _ := pllCreatePost(t, c, base, "?lang=fr", "About us")
	if a, b := slugOf(enID), slugOf(frID); a == b {
		t.Fatalf("slugs equal without Share slugs: %q", a)
	}
	o = srv.pllOpts()
	o.ShareSlugs = true
	if o.ForceLang == 0 {
		o.ForceLang = 1
	}
	_ = srv.pllSaveOpts(ctx, o)
	srv.pllFlush()
	enID2, _ := pllCreatePost(t, c, base, "", "Contact")
	frID2, _ := pllCreatePost(t, c, base, "?lang=fr", "Contact")
	if a, b := slugOf(enID2), slugOf(frID2); a != b || a == "" {
		t.Errorf("Share slugs: %q vs %q, want the same slug", a, b)
	}
}
