package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// pllR2Site is a Multi Language site with English (default, hidden) and French.
func pllR2Site(t *testing.T) (base string, c *http.Client, srv *Server) {
	t.Helper()
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	golylangToggle(t, c, ts.URL, "activate")
	pllAddLanguage(t, c, ts.URL, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, ts.URL, "Français", "fr_FR", "fr", "fr")
	return ts.URL, c, srv
}

func pllR2Link(t *testing.T, c *http.Client, base string, id int64, tr map[string]int64) {
	t.Helper()
	if code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts/"+itoa64(id), map[string]any{"translations": tr}); code != http.StatusOK {
		t.Fatalf("link %d: %d %v", id, code, obj)
	}
}

var inlineNonceRe = regexp.MustCompile(`name="_inline_edit" value="([^"]+)"`)

// TestGolylangSyncRespectsAdminSaver: a wp-admin save (Quick Edit here; the classic editor, Bulk
// Edit and the custom fields box take the same path) copies synchronized fields only onto the
// translations the saving user may edit, as the REST path already did.
func TestGolylangSyncRespectsAdminSaver(t *testing.T) {
	base, c, srv := pllR2Site(t)
	ctx := context.Background()
	o := srv.pllOpts()
	o.Sync = []string{"comment_status"}
	if err := srv.pllSaveOpts(ctx, o); err != nil {
		t.Fatal(err)
	}
	asCreateUser(t, srv, "writer", "author", asStrongPass)
	ac := &http.Client{CheckRedirect: c.CheckRedirect}
	ac.Jar, _ = cookiejar.New(nil)
	if code, _, _ := asLogin(t, ac, base, "/wp-login", "writer", asStrongPass); code != http.StatusFound {
		t.Fatalf("author login: %d", code)
	}
	enID, _ := pllCreatePost(t, ac, base, "?lang=en", "Writer English")
	frID, _ := pllCreatePost(t, c, base, "?lang=fr", "Admin French")
	pllR2Link(t, c, base, frID, map[string]int64{"en": enID})
	if got := srv.pllTranslations(ctx, pllPost, enID)["fr"]; got != frID {
		t.Fatalf("translations not linked: %v", srv.pllTranslations(ctx, pllPost, enID))
	}

	quickEdit := func(cl *http.Client, id int64, title, comments string) {
		t.Helper()
		_, list, _ := get(t, cl, base+"/wp-admin/edit")
		m := inlineNonceRe.FindStringSubmatch(list)
		if m == nil {
			t.Fatal("no Quick Edit nonce")
		}
		code, row, _ := post(t, cl, base+"/wp-admin/admin-ajax", url.Values{"action": {"inline-save"}, "_inline_edit": {m[1]}, "post_ID": {itoa64(id)},
			"post_type": {"post"}, "post_title": {title}, "post_name": {""}, "comment_status": {comments}, "ping_status": {"open"}, "_status": {"publish"}})
		if code != http.StatusOK || strings.Contains(row, "not allowed") {
			t.Fatalf("quick edit %d: %d %.200s", id, code, row)
		}
	}
	status := func(id int64) string {
		p, err := srv.store.GetPost(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return p.CommentStatus
	}

	quickEdit(ac, enID, "Writer English", "")
	if status(enID) != "closed" {
		t.Fatalf("the author's own post: comment_status %q", status(enID))
	}
	if status(frID) != "open" {
		t.Errorf("an author's Quick Edit closed comments on the administrator's translation it cannot edit")
	}
	// Control: the administrator may edit both, so the synchronization still runs.
	quickEdit(c, enID, "Writer English", "")
	if status(frID) != "closed" {
		t.Errorf("the administrator's save did not synchronize comment_status")
	}
}

// TestGolylangTranslationLinksValidated: the term form links only terms of the same taxonomy in the
// posted language (a posted id is never relabelled), and a REST translations entry for the saved
// object's own language does not push the object out of its group.
func TestGolylangTranslationLinksValidated(t *testing.T) {
	base, c, srv := pllR2Site(t)
	ctx := context.Background()
	mk := func(route, name, lang string) int64 {
		t.Helper()
		code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/"+route+"?lang="+lang, map[string]any{"name": name})
		if code != http.StatusCreated {
			t.Fatalf("create %s %q: %d %v", route, name, code, obj)
		}
		id, _ := obj["id"].(float64)
		return int64(id)
	}
	catEN := mk("categories", "Fruit", "en")
	catEN2 := mk("categories", "Vegetables", "en")
	catFR := mk("categories", "Fruits", "fr")
	tagEN := mk("tags", "Apple", "en")

	save := func(tr map[string]int64) {
		t.Helper()
		_, page, _ := get(t, c, base+"/wp-admin/term?taxonomy=category&tag_ID="+itoa64(catEN))
		m := regexp.MustCompile(`(?s)id="edittag".*?name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(page)
		if m == nil {
			t.Fatal("no nonce on the term edit form")
		}
		form := url.Values{"_wpnonce": {m[1]}, "action": {"editedtag"}, "taxonomy": {"category"}, "tag_ID": {itoa64(catEN)},
			"name": {"Fruit"}, "slug": {"fruit"}, "term_lang_choice": {"en"}}
		for lang, id := range tr {
			form.Set("term_tr_lang["+lang+"]", strconv.FormatInt(id, 10))
		}
		if code, body, _ := post(t, c, base+"/wp-admin/term", form); code != http.StatusFound {
			t.Fatalf("term save: %d %.300s", code, body)
		}
	}
	save(map[string]int64{"fr": tagEN})
	if l := srv.pllObjLang(ctx, pllTerm, tagEN); l != "en" {
		t.Errorf("a tag posted as the French category translation was relabelled %q", l)
	}
	if srv.pllTranslations(ctx, pllTerm, catEN)["fr"] == tagEN {
		t.Error("a tag joined a category's translation group")
	}
	save(map[string]int64{"fr": catEN2})
	if l := srv.pllObjLang(ctx, pllTerm, catEN2); l != "en" {
		t.Errorf("an English category posted as the French translation was relabelled %q", l)
	}
	save(map[string]int64{"fr": catFR})
	if got := srv.pllTranslations(ctx, pllTerm, catEN)["fr"]; got != catFR {
		t.Errorf("valid link not made: %v", srv.pllTranslations(ctx, pllTerm, catEN))
	}

	// REST: {"translations": {"en": other}} on an English post keeps the post in its group.
	enID, _ := pllCreatePost(t, c, base, "?lang=en", "Group owner")
	otherID, _ := pllCreatePost(t, c, base, "?lang=en", "Another English post")
	frID, _ := pllCreatePost(t, c, base, "?lang=fr", "Propriétaire")
	pllR2Link(t, c, base, enID, map[string]int64{"fr": frID})
	pllR2Link(t, c, base, enID, map[string]int64{"en": otherID, "fr": frID})
	tr := srv.pllTranslations(ctx, pllPost, enID)
	if tr["en"] != enID || tr["fr"] != frID {
		t.Errorf("the saved post left its own group: %v", tr)
	}
}

// TestGolylangArchiveSwitcherTranslatedBase: with Translate slugs, the hreflang links (and the
// switcher) of an author archive carry each language's own base, not the current one's.
func TestGolylangArchiveSwitcherTranslatedBase(t *testing.T) {
	base, c, srv := pllR2Site(t)
	ctx := context.Background()
	pllCreatePost(t, c, base, "?lang=en", "Hello archive")
	pllCreatePost(t, c, base, "?lang=fr", "Bonjour archive")
	o := srv.pllOpts()
	o.TranslateSlugs = true
	if err := srv.pllSaveOpts(ctx, o); err != nil {
		t.Fatal(err)
	}
	if err := srv.pllSetString(ctx, "fr", "author", "auteur"); err != nil {
		t.Fatal(err)
	}
	srv.pllFlushStrings()
	code, body, _ := get(t, c, base+"/fr/auteur/admin/")
	if code != http.StatusOK {
		t.Fatalf("/fr/auteur/admin/: %d", code)
	}
	if !strings.Contains(body, `href="`+base+`/author/admin/" hreflang="en"`) {
		m := regexp.MustCompile(`<link rel="alternate"[^>]*hreflang="en"[^>]*>`).FindString(body)
		t.Errorf("English alternate of the French author archive: %s", m)
	}
	if !strings.Contains(body, `href="`+base+`/fr/auteur/admin/" hreflang="fr"`) {
		t.Errorf("French alternate missing")
	}
}

// TestGolylangLanguageRenameKeepsStrings: changing a language code keeps its string translations
// on the site (the cache was keyed by the old code).
func TestGolylangLanguageRenameKeepsStrings(t *testing.T) {
	base, c, srv := pllR2Site(t)
	ctx := context.Background()
	pllCreatePost(t, c, base, "?lang=fr", "Un article")
	if err := srv.pllSetString(ctx, "fr", "Toggle Site", "Site Bascule"); err != nil {
		t.Fatal(err)
	}
	srv.pllFlushStrings()
	if _, body, _ := get(t, c, base+"/fr/"); !strings.Contains(body, "Site Bascule") {
		t.Fatal("translated site title missing before the rename")
	}
	if code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/languages/fr", map[string]any{"slug": "fr-fr"}); code != http.StatusOK {
		t.Fatalf("rename: %d %v", code, obj)
	}
	if _, body, _ := get(t, c, base+"/fr-fr/"); !strings.Contains(body, "Site Bascule") {
		t.Error("the string translations are lost after the language code changed")
	}
}

// TestGolylangRESTSettingsValidated: POST pll/v1/settings applies the checks of the URL form.
func TestGolylangRESTSettingsValidated(t *testing.T) {
	base, c, srv := pllR2Site(t)
	for _, body := range []map[string]any{
		{"domains": map[string]any{"fr": "javascript:alert(1)"}},
		{"domains": map[string]any{"fr": "fr.example.test"}},
		{"force_lang": 3, "domains": map[string]any{"en": base}},
	} {
		if code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/settings", body); code != http.StatusBadRequest {
			t.Errorf("%v: %d %v", body, code, obj)
		}
	}
	if o := srv.pllOpts(); o.ForceLang != 1 || len(o.Domains) != 0 {
		t.Errorf("rejected settings were stored: %+v", o)
	}
	if code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/settings",
		map[string]any{"force_lang": 3, "domains": map[string]any{"en": base, "fr": "https://fr.example.test/"}}); code != http.StatusOK {
		t.Errorf("valid domains: %d %v", code, obj)
	}
	if d := srv.pllOpts().Domains["fr"]; d != "https://fr.example.test" {
		t.Errorf("stored domain %q", d)
	}
}

// TestGolylangShareSlugsLanguageChange: with Share slugs, moving a post into a language that
// already has its slug gives it a unique one there.
func TestGolylangShareSlugsLanguageChange(t *testing.T) {
	base, c, srv := pllR2Site(t)
	ctx := context.Background()
	o := srv.pllOpts()
	o.ShareSlugs = true
	if err := srv.pllSaveOpts(ctx, o); err != nil {
		t.Fatal(err)
	}
	enID, enLink := pllCreatePost(t, c, base, "?lang=en", "Shared slug")
	frID, _ := pllCreatePost(t, c, base, "?lang=fr", "Shared slug")
	slug := func(id int64) string {
		p, err := srv.store.GetPost(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return p.Slug
	}
	if slug(enID) != "shared-slug" || slug(frID) != "shared-slug" {
		t.Fatalf("share slugs: %q %q", slug(enID), slug(frID))
	}
	if code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts/"+itoa64(frID), map[string]any{"lang": "en"}); code != http.StatusOK {
		t.Fatalf("change language: %d %v", code, obj)
	}
	if srv.pllObjLang(ctx, pllPost, frID) != "en" {
		t.Fatal("language not changed")
	}
	if s := slug(frID); s == "shared-slug" {
		t.Error("two English posts share the slug shared-slug")
	}
	if code, _, _ := get(t, c, enLink); code != http.StatusOK {
		t.Errorf("%s: %d", enLink, code)
	}
}
