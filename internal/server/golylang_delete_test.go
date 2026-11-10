package server

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// TestGolylangDeleteLanguageKeepsContentSafe: deleting a language that has content asks what happens to
// it (nothing is deleted without an answer), and the chosen way is applied in one go.
func TestGolylangDeleteLanguageKeepsContentSafe(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	enID, _ := pllCreatePost(t, c, base, "", "Hello English")
	frID, _ := pllCreatePost(t, c, base, "?lang=fr&translations[en]="+strconv.FormatInt(enID, 10), "Bonjour")
	ctx := context.Background()

	var frLang *pllLanguage
	for _, l := range srv.pllLanguages(ctx) {
		if l.Slug == "fr" {
			frLang = l
		}
	}
	// Translation status: a lone English post is untranslated; a source edited after its translation is outdated.
	base0, _ := srv.pllTranslationStatus(ctx, frLang)
	pllCreatePost(t, c, base, "", "Only English")
	if m, o := srv.pllTranslationStatus(ctx, frLang); m != base0+1 || o != 0 {
		t.Fatalf("status = missing %d outdated %d, want %d and 0", m, o, base0+1)
	}
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET modified_at = now() + interval '1 hour' WHERE id = $1`, enID); err != nil {
		t.Fatal(err)
	}
	if m, o := srv.pllTranslationStatus(ctx, frLang); m != base0+1 || o != 1 {
		t.Fatalf("status after a source edit = missing %d outdated %d, want %d and 1", m, o, base0+1)
	}
	_, page, _ := get(t, c, base+"/wp-admin/admin?page=mlang")
	if !strings.Contains(page, "column-untranslated") {
		t.Error("the Languages screen has no Untranslated column")
	}
	nonce := formNonce(t, page)
	_ = nonce
	// REST without a choice is refused.
	if code, obj, _ := pllREST(t, c, base, http.MethodDelete, "/wp-json/pll/v1/languages/"+strconv.FormatInt(frLang.ID, 10), nil); code != http.StatusConflict {
		t.Fatalf("REST delete of a language with content: %d %v", code, obj)
	}
	// The admin link without a choice renders the choices and deletes nothing.
	delURL := ""
	for _, m := range strings.Split(page, `href="`) {
		if strings.HasPrefix(m, "/wp-admin/admin?page=mlang&amp;pll_action=delete&amp;lang="+strconv.FormatInt(frLang.ID, 10)) {
			delURL = strings.ReplaceAll(strings.SplitN(m, `"`, 2)[0], "&amp;", "&")
		}
	}
	if delURL == "" {
		t.Fatal("no delete link")
	}
	code, body, _ := get(t, c, base+delURL)
	if code != http.StatusOK || !strings.Contains(body, "Nothing was deleted") || srv.pllLang(ctx, "fr") == nil {
		t.Fatalf("delete without a choice: %d, language still there: %v", code, srv.pllLang(ctx, "fr") != nil)
	}
	// Turn the posts into drafts.
	if code, _, loc := get(t, c, base+delURL+"&content=draft"); code != http.StatusFound || !strings.Contains(loc, "pll_msg=deleted") {
		t.Fatalf("delete with content=draft: %d %s", code, loc)
	}
	var status string
	_ = srv.store.Pool.QueryRow(ctx, `SELECT status FROM posts WHERE id = $1`, frID).Scan(&status)
	if status != "draft" {
		t.Errorf("French post status = %q, want draft", status)
	}
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM pll_post_langs WHERE post_id = $1 AND lang = 'en'`, frID).Scan(&n)
	if n != 1 {
		t.Errorf("the former French post should be an untranslated English item")
	}
	_ = url.Values{}
}
