package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"wpgo/internal/webbuilder"
)

// Pursae Multi Language in the Web Builder editor: GET documents/{id}/languages and
// POST documents/{id}/translations. Integration test (needs WPGO_TEST_ADMIN_DB / WPGO_TEST_DB).
func TestWebBuilderLanguages(t *testing.T) {
	srv, base, w := wbrStart(t)
	pg := wbrPost(t, srv, "page", "About us", "publish", 1)

	// Multi Language off: nothing to show, no translations.
	code, v, _ := w.do(http.MethodGet, wbrDocPath(pg.ID, "/languages"), nil, nil)
	if code != http.StatusOK || wbrObj(v)["enabled"] != false {
		t.Fatalf("off: %d %v", code, v)
	}
	if code, v, _ := w.do(http.MethodPost, wbrDocPath(pg.ID, "/translations"), map[string]any{"lang": "fr"}, nil); code != http.StatusBadRequest {
		t.Fatalf("translate while off: %d %v", code, v)
	}

	golylangToggle(t, w.c, base, "activate")
	pllAddLanguage(t, w.c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, w.c, base, "Français", "fr_FR", "fr", "fr")

	code, v, _ = w.do(http.MethodGet, wbrDocPath(pg.ID, "/languages"), nil, nil)
	o := wbrObj(v)
	if code != http.StatusOK || o["enabled"] != true || wbrObj(o["current"])["slug"] != "en" {
		t.Fatalf("on: %d %v", code, v)
	}
	if miss, _ := o["missing"].([]any); len(miss) != 1 || wbrObj(miss[0])["slug"] != "fr" {
		t.Fatalf("missing: %v", o["missing"])
	}

	// The page has a published builder doc; the translation starts from a copy of it.
	doc, render := wbrFixture(t, "initial")
	if code, v, _ := w.do(http.MethodPost, wbrDocPath(pg.ID, "/publish"), wbrPublishBody(doc, render, 0, "publish", map[string]any{"title": "About us", "slug": "about-us", "indexable": true}), nil); code != http.StatusOK {
		t.Fatalf("publish source: %d %v", code, v)
	}

	code, v, _ = w.do(http.MethodPost, wbrDocPath(pg.ID, "/translations"), map[string]any{"lang": "fr"}, nil)
	tr := wbrObj(v)
	id, _ := tr["postId"].(float64)
	if code != http.StatusCreated || id <= 0 || !strings.Contains(tr["editUrl"].(string), "action=web-builder") {
		t.Fatalf("translate: %d %v", code, v)
	}
	if tr["status"] != "draft" || tr["slug"] != "fr" {
		t.Errorf("translation answer: %v", tr)
	}
	ctx := context.Background()
	copyPost, err := srv.store.GetPost(ctx, int64(id))
	if err != nil {
		t.Fatal(err)
	}
	if copyPost.Status != "draft" || copyPost.Type != "page" || copyPost.Meta[webbuilder.PostMarkerMeta] != "1" {
		t.Errorf("copy: status %q type %q marker %v", copyPost.Status, copyPost.Type, copyPost.Meta[webbuilder.PostMarkerMeta])
	}
	wbDB := srv.store.WebBuilder()
	if rev, err := wbDB.DocRev(ctx, copyPost.ID); err != nil || rev < 1 {
		t.Errorf("the copy has no builder doc: rev %d, %v", rev, err)
	}
	src, err1 := wbDB.Doc(ctx, pg.ID)
	dst, err2 := wbDB.Doc(ctx, copyPost.ID)
	if err1 != nil || err2 != nil || string(dst.Doc) == "" || string(dst.Doc) != string(src.Doc) {
		t.Errorf("copied doc differs: %v %v", err1, err2)
	}
	// The editor can open the copy's document straight away.
	if code, v, _ := w.do(http.MethodGet, wbrDocPath(copyPost.ID, ""), nil, nil); code != http.StatusOK || wbrObj(v)["doc"] == nil {
		t.Errorf("open the copy: %d %v", code, v)
	}
	if code, v, _ := w.do(http.MethodPost, wbrDocPath(pg.ID, "/translations"), map[string]any{"lang": "fr"}, nil); code != http.StatusConflict {
		t.Fatalf("second translation: %d %v", code, v)
	}
	if code, _, _ := w.do(http.MethodPost, wbrDocPath(pg.ID, "/translations"), map[string]any{"lang": "xx"}, nil); code != http.StatusBadRequest {
		t.Fatalf("unknown language: %d", code)
	}

	// Both sides now list each other and nothing is missing.
	for _, pid := range []int64{pg.ID, int64(id)} {
		_, v, _ = w.do(http.MethodGet, wbrDocPath(pid, "/languages"), nil, nil)
		o = wbrObj(v)
		if l, _ := o["translations"].([]any); len(l) != 2 {
			t.Fatalf("post %d translations: %v", pid, o["translations"])
		}
		if m, _ := o["missing"].([]any); len(m) != 0 {
			t.Fatalf("post %d missing: %v", pid, m)
		}
	}
}

// GET documents/{id}/plugins: the editor's "Page tools" tabs. Integration test.
func TestWebBuilderPluginPanels(t *testing.T) {
	srv, base, w := wbrStart(t)
	pg := wbrPost(t, srv, "page", "Panels", "publish", 1)
	tpl := wbtTemplate(t, srv, webbuilder.TemplateHeader, "Panels header", []string{"include/general"}, 0, "publish")
	panels := func(c *wbrClient, id int64) map[string]any {
		t.Helper()
		code, v, _ := c.do(http.MethodGet, wbrDocPath(id, "/plugins"), nil, nil)
		if code != http.StatusOK {
			t.Fatalf("plugins %d: %d %v", id, code, v)
		}
		return wbrObj(v)
	}
	want := func(name string, got map[string]any, langs, seo, seoEdit bool) {
		t.Helper()
		if got["languages"] != langs || got["seo"] != seo || got["seoEdit"] != seoEdit || len(got) != 5 {
			t.Fatalf("%s: %v (want languages=%v seo=%v seoEdit=%v)", name, got, langs, seo, seoEdit)
		}
	}

	// Both plugins off.
	want("off", panels(w, pg.ID), false, false, false)

	// Multi Language on but no language yet: still nothing to show.
	golylangToggle(t, w.c, base, "activate")
	want("no languages", panels(w, pg.ID), false, false, false)
	pllAddLanguage(t, w.c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, w.c, base, "Français", "fr_FR", "fr", "fr")
	want("languages on", panels(w, pg.ID), true, false, false)
	// Templates are translated types too: they get the (read-only) languages tab but no SEO tab.
	want("template, languages on", panels(w, tpl.ID), true, false, false)

	// Pursae SEO on: the SEO tab for pages, never for templates.
	seogoToggle(t, w.c, base, "activate")
	want("both on", panels(w, pg.ID), true, true, true)
	want("template, both on", panels(w, tpl.ID), true, false, false)

	// A contributor: own draft yes (edit_posts, on-page SEO box), the admin's page no, a template no.
	contrib := wbrUser(t, srv, base, "panelcontrib", "contributor")
	cu, err := srv.store.UserByLoginOrEmail(context.Background(), "panelcontrib")
	if err != nil {
		t.Fatal(err)
	}
	own := wbrPost(t, srv, "post", "Own draft", "draft", cu.ID)
	want("contributor own draft", panels(contrib, own.ID), true, true, true)
	for name, id := range map[string]int64{"admin's page": pg.ID, "template": tpl.ID} {
		if code, v, _ := contrib.do(http.MethodGet, wbrDocPath(id, "/plugins"), nil, nil); code != http.StatusForbidden || wbrCode(v) != webbuilder.ErrForbidden {
			t.Errorf("contributor on %s: %d %v", name, code, v)
		}
	}
	// A subscriber has no edit capability at all.
	sub := wbrUser(t, srv, base, "panelsub", "subscriber")
	for _, id := range []int64{pg.ID, tpl.ID} {
		if code, _, _ := sub.do(http.MethodGet, wbrDocPath(id, "/plugins"), nil, nil); code != http.StatusForbidden {
			t.Errorf("subscriber on %d: %d", id, code)
		}
	}
	// An editor edits any page but not Theme Builder templates (edit_theme_options).
	ed := wbrUser(t, srv, base, "paneleditor", "editor")
	want("editor, page", panels(ed, pg.ID), true, true, true)
	if code, _, _ := ed.do(http.MethodGet, wbrDocPath(tpl.ID, "/plugins"), nil, nil); code != http.StatusForbidden {
		t.Errorf("editor on a template: %d", code)
	}
	// Unknown / trashed posts.
	if code, _, _ := w.do(http.MethodGet, wbrDocPath(999999, "/plugins"), nil, nil); code != http.StatusNotFound {
		t.Errorf("unknown post: %d", code)
	}

	// Plugins off again: the tabs go away.
	seogoToggle(t, w.c, base, "deactivate")
	want("seo off", panels(w, pg.ID), true, false, false)
	golylangToggle(t, w.c, base, "deactivate")
	want("all off", panels(w, pg.ID), false, false, false)
}

// Who may list and create translations. Integration test.
func TestWebBuilderLanguagesPermissions(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	golylangToggle(t, admin.c, base, "activate")
	pllAddLanguage(t, admin.c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, admin.c, base, "Français", "fr_FR", "fr", "fr")
	pllAddLanguage(t, admin.c, base, "Deutsch", "de_DE", "de", "de")

	adminPage := wbrPost(t, srv, "page", "Admin page", "publish", 1)
	tpl := wbtTemplate(t, srv, webbuilder.TemplateHeader, "Lang header", []string{"include/general"}, 0, "publish")
	contrib := wbrUser(t, srv, base, "langcontrib", "contributor")
	cu, err := srv.store.UserByLoginOrEmail(ctx, "langcontrib")
	if err != nil {
		t.Fatal(err)
	}
	own := wbrPost(t, srv, "post", "Contributor post", "draft", cu.ID)
	sub := wbrUser(t, srv, base, "langsub", "subscriber")
	ed := wbrUser(t, srv, base, "langeditor", "editor")
	translate := func(c *wbrClient, id int64, lang string) (int, any) {
		code, v, _ := c.do(http.MethodPost, wbrDocPath(id, "/translations"), map[string]any{"lang": lang}, nil)
		return code, v
	}
	count := func(typ string) int {
		var n int
		if err := srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE type = $1`, typ).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	pages := count("page")

	// Nobody who cannot edit the post may read its languages or translate it: nothing is created.
	for name, tc := range map[string]struct {
		c  *wbrClient
		id int64
	}{"contributor, admin's page": {contrib, adminPage.ID}, "subscriber, admin's page": {sub, adminPage.ID},
		"subscriber, contributor draft": {sub, own.ID}, "contributor, template": {contrib, tpl.ID}, "editor, template": {ed, tpl.ID}} {
		if code, v, _ := tc.c.do(http.MethodGet, wbrDocPath(tc.id, "/languages"), nil, nil); code != http.StatusForbidden || wbrCode(v) != webbuilder.ErrForbidden {
			t.Errorf("%s: languages %d %v", name, code, v)
		}
		if code, v := translate(tc.c, tc.id, "fr"); code != http.StatusForbidden || wbrCode(v) != webbuilder.ErrForbidden {
			t.Errorf("%s: translate %d %v", name, code, v)
		}
	}
	if got := count("page"); got != pages {
		t.Fatalf("a refused request created %d page(s)", got-pages)
	}

	// A template is translated by a user with edit_theme_options: the copy is a disabled template
	// with the same type, conditions and priority, linked to the source; once published it is the
	// header of its language.
	_, v, _ := admin.do(http.MethodGet, wbrDocPath(tpl.ID, "/languages"), nil, nil)
	o := wbrObj(v)
	if m, _ := o["missing"].([]any); o["enabled"] != true || o["canTranslate"] != true || len(m) != 2 {
		t.Errorf("template languages: %v", o)
	}
	code, v := translate(admin, tpl.ID, "fr")
	tplFr, _ := wbrObj(v)["postId"].(float64)
	if code != http.StatusCreated || tplFr <= 0 || wbrObj(v)["status"] != "draft" {
		t.Fatalf("admin translating a template: %d %v", code, v)
	}
	got, err := srv.store.WebBuilder().Template(ctx, int64(tplFr))
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != webbuilder.TemplateHeader || got.Status != "draft" || strings.Join(got.Conditions, ",") != "include/general" {
		t.Errorf("template translation: %+v", got)
	}
	if tr := srv.pllTranslations(ctx, pllPost, tpl.ID); tr["fr"] != int64(tplFr) {
		t.Errorf("template translation not linked: %v", tr)
	}
	_, v, _ = admin.do(http.MethodGet, wbrDocPath(tpl.ID, "/languages"), nil, nil)
	if m, _ := wbrObj(v)["missing"].([]any); len(m) != 1 || wbrObj(m[0])["slug"] != "de" {
		t.Errorf("template missing after fr: %v", v)
	}
	if code, v := translate(admin, tpl.ID, "fr"); code != http.StatusConflict {
		t.Errorf("second fr template translation: %d %v", code, v)
	}
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET status = 'publish' WHERE id = $1`, int64(tplFr)); err != nil {
		t.Fatal(err)
	}
	srv.wbTemplatesChanged(ctx)
	for lang, want := range map[string]int64{"fr": int64(tplFr), "en": tpl.ID, "de": tpl.ID} {
		c, _ := wbtCtx(srv, nil, "/")
		pllSet(c, &pllReq{srv: srv, Lang: srv.pllLang(ctx, lang), Filter: true, Front: true})
		if _, id := srv.wbPickTemplate(c, webbuilder.LocationHeader, &siteQuery{Kind: "home"}); id != want {
			t.Errorf("%s header: %d, want %d", lang, id, want)
		}
	}

	// A contributor translates their own draft: the copy is a draft of the contributor's page tree.
	_, v, _ = contrib.do(http.MethodGet, wbrDocPath(own.ID, "/languages"), nil, nil)
	if o = wbrObj(v); o["canTranslate"] != true {
		t.Errorf("contributor canTranslate: %v", o)
	}
	if code, v := translate(contrib, own.ID, "fr"); code != http.StatusCreated || wbrObj(v)["status"] != "draft" {
		t.Fatalf("contributor translating own draft: %d %v", code, v)
	}
	if code, v := translate(contrib, own.ID, "fr"); code != http.StatusConflict || wbrCode(v) != "wb_translation_exists" {
		t.Errorf("contributor, second fr: %d %v", code, v)
	}
	if code, v := translate(contrib, own.ID, "zz"); code != http.StatusBadRequest || wbrCode(v) != "wb_bad_language" {
		t.Errorf("unknown language: %d %v", code, v)
	}
	if code, v := translate(contrib, own.ID, ""); code != http.StatusBadRequest || wbrCode(v) != "wb_bad_language" {
		t.Errorf("empty language: %d %v", code, v)
	}
	// Still the source's own language: that is a translation that exists (the page itself).
	if code, v := translate(contrib, own.ID, "en"); code != http.StatusConflict {
		t.Errorf("translating into the page's own language: %d %v", code, v)
	}

	// The administrator translates the contributor's page into German: the copy belongs to the
	// administrator, so the contributor's panel lists it read-only (no title, status or edit link),
	// not as missing.
	if code, v := translate(admin, own.ID, "de"); code != http.StatusCreated {
		t.Fatalf("admin translating: %d %v", code, v)
	}
	_, v, _ = contrib.do(http.MethodGet, wbrDocPath(own.ID, "/languages"), nil, nil)
	o = wbrObj(v)
	var slugs []string
	for _, x := range o["translations"].([]any) {
		slugs = append(slugs, wbrObj(x)["slug"].(string))
	}
	if strings.Join(slugs, ",") != "de,en,fr" {
		t.Errorf("contributor's translations: %v", slugs)
	}
	for _, x := range o["translations"].([]any) {
		tr := wbrObj(x)
		if tr["slug"] == "de" && (tr["readOnly"] != true || tr["editUrl"] != "" || tr["title"] != "" || tr["status"] != "") {
			t.Errorf("read-only translation leaks: %v", tr)
		}
		if tr["slug"] != "de" && tr["readOnly"] == true {
			t.Errorf("own translation is read-only: %v", tr)
		}
	}
	if m, _ := o["missing"].([]any); len(m) != 0 {
		t.Errorf("contributor's missing: %v", m)
	}
	_, v, _ = admin.do(http.MethodGet, wbrDocPath(own.ID, "/languages"), nil, nil)
	if l, _ := wbrObj(v)["translations"].([]any); len(l) != 3 {
		t.Errorf("admin's translations: %v", v)
	}
}

// A trashed translation is offered again, and creating it replaces the trashed one. Integration test.
func TestWebBuilderLanguagesTrashedTranslation(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	golylangToggle(t, admin.c, base, "activate")
	pllAddLanguage(t, admin.c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, admin.c, base, "Français", "fr_FR", "fr", "fr")
	pg := wbrPost(t, srv, "page", "Trash me", "publish", 1)

	code, v, _ := admin.do(http.MethodPost, wbrDocPath(pg.ID, "/translations"), map[string]any{"lang": "fr"}, nil)
	first, _ := wbrObj(v)["postId"].(float64)
	if code != http.StatusCreated || first <= 0 {
		t.Fatalf("first translation: %d %v", code, v)
	}
	if err := srv.store.TrashPost(ctx, int64(first)); err != nil {
		t.Fatal(err)
	}
	_, v, _ = admin.do(http.MethodGet, wbrDocPath(pg.ID, "/languages"), nil, nil)
	o := wbrObj(v)
	if l, _ := o["translations"].([]any); len(l) != 1 {
		t.Fatalf("translations with the French one trashed: %v", o["translations"])
	}
	if m, _ := o["missing"].([]any); len(m) != 1 || wbrObj(m[0])["slug"] != "fr" {
		t.Fatalf("a trashed translation must be offered again: %v", o["missing"])
	}
	code, v, _ = admin.do(http.MethodPost, wbrDocPath(pg.ID, "/translations"), map[string]any{"lang": "fr"}, nil)
	second, _ := wbrObj(v)["postId"].(float64)
	if code != http.StatusCreated || second <= 0 || second == first {
		t.Fatalf("second translation: %d %v", code, v)
	}
	_, v, _ = admin.do(http.MethodGet, wbrDocPath(pg.ID, "/languages"), nil, nil)
	o = wbrObj(v)
	var fr float64
	for _, x := range o["translations"].([]any) {
		if tr := wbrObj(x); tr["slug"] == "fr" {
			fr, _ = tr["postId"].(float64)
		}
	}
	if fr != second {
		t.Errorf("French translation is %v, want the new %v", fr, second)
	}
	if m, _ := o["missing"].([]any); len(m) != 0 {
		t.Errorf("missing after recreate: %v", m)
	}
}

// Creating a translation needs the type's create capability, not just the right to edit the source
// (a per-user deny of edit_pages). Integration test.
func TestWebBuilderLanguagesCreateCap(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	golylangToggle(t, admin.c, base, "activate")
	pllAddLanguage(t, admin.c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, admin.c, base, "Français", "fr_FR", "fr", "fr")
	pg := wbrPost(t, srv, "page", "Cap page", "publish", 1)
	ed := wbrUser(t, srv, base, "capeditor", "editor")
	eu, err := srv.store.UserByLoginOrEmail(ctx, "capeditor")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetUserCapExceptions(ctx, eu.ID, map[string]string{"edit_pages": "deny"}); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		var n int
		if err := srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE type = 'page'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	_, v, _ := ed.do(http.MethodGet, wbrDocPath(pg.ID, "/languages"), nil, nil)
	if o := wbrObj(v); o["canTranslate"] != false {
		t.Errorf("canTranslate without edit_pages: %v", o)
	}
	if code, v, _ := ed.do(http.MethodPost, wbrDocPath(pg.ID, "/translations"), map[string]any{"lang": "fr"}, nil); code != http.StatusForbidden || wbrCode(v) != webbuilder.ErrForbidden {
		t.Errorf("translate without edit_pages: %d %v", code, v)
	}
	if got := count(); got != before {
		t.Fatalf("a refused request created %d page(s)", got-before)
	}
}
