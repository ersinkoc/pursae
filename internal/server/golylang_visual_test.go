package server

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

func TestPllVisualPath(t *testing.T) {
	for in, want := range map[string]string{
		"":                             "/",
		"/":                            "/",
		"/fr/hello/":                   "/fr/hello/",
		"/?p=3&pll_visual=x":           "/?p=3",
		"https://evil.example/":        "/",
		"//evil.example/":              "/",
		"/\\evil.example":              "/",
		"\\\\evil.example":             "/",
		"javascript:alert(1)":          "/",
		"/wp-admin/options":            "/",
		"/WP-LOGIN":                    "/",
		"/wp-json/wp/v2/users":         "/",
		"/a\nb":                        "/",
		"/fr/page/#frag":               "/fr/page/",
		" /fr/ ":                       "/fr/",
		"/?customize_changeset_uuid=1": "/",
	} {
		if got := pllVisualPath(in); got != want {
			t.Errorf("pllVisualPath(%q) = %q, want %q", in, got, want)
		}
	}
	if m := pllVisualMarker("Skip to content"); !regexp.MustCompile(`^s[0-9a-f]{12}$`).MatchString(m) || m != pllVisualMarker("Skip to content") {
		t.Errorf("marker %q", m)
	}
}

// pllVisualOpen opens the visual editor shell and returns its body and the session token.
func pllVisualOpen(t *testing.T, c *http.Client, base, path, lang string) (string, string) {
	t.Helper()
	code, body, _ := get(t, c, base+pllVisualURL(path, lang))
	if code != http.StatusOK {
		t.Fatalf("visual shell %s %s: %d", path, lang, code)
	}
	m := regexp.MustCompile(`id="pll-v-frame" title="Page preview" src="([^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("visual shell without an iframe:\n%s", body)
	}
	src := html.UnescapeString(m[1])
	u, err := url.Parse(src)
	if err != nil || u.Host != "" || !strings.HasPrefix(src, "/") {
		t.Fatalf("iframe src %q is not a same-origin path", src)
	}
	tok := u.Query().Get(pllVisualParam)
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(tok) {
		t.Fatalf("iframe src %q has no token", src)
	}
	return body, tok
}

type pllVisualData struct {
	Lang struct {
		Slug    string `json:"slug"`
		Default bool   `json:"default"`
	} `json:"lang"`
	Want       string            `json:"want"`
	Strings    []pllVisualItem   `json:"strings"`
	Posts      []pllVisualPost   `json:"posts"`
	Images     []pllVisualImage  `json:"images"`
	Alternates map[string]string `json:"alternates"`
}

var pllVisualIslandRe = regexp.MustCompile(`(?s)<script type="application/json" id="pll-visual-data">(.*?)</script>`)

// pllVisualPage fetches a page in the visual session tok and decodes its data island.
func pllVisualPageGet(t *testing.T, c *http.Client, base, path, tok string) (string, *pllVisualData) {
	t.Helper()
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	resp, err := c.Get(base + path + sep + pllVisualParam + "=" + tok)
	if err != nil {
		t.Fatal(err)
	}
	body := bodyOf(resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("visual page %s: %d", path, resp.StatusCode)
	}
	m := pllVisualIslandRe.FindStringSubmatch(body)
	if m == nil {
		return body, nil
	}
	if !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
		t.Errorf("visual page %s is cacheable: %q", path, resp.Header.Get("Cache-Control"))
	}
	d := &pllVisualData{}
	if err := json.Unmarshal([]byte(m[1]), d); err != nil {
		t.Fatalf("data island of %s: %v", path, err)
	}
	return body, d
}

func pllVisualFind(d *pllVisualData, original string) *pllVisualItem {
	if d == nil {
		return nil
	}
	for i := range d.Strings {
		if d.Strings[i].Original == original {
			return &d.Strings[i]
		}
	}
	return nil
}

// pllVisualClean asserts a page carries nothing of visual mode.
func pllVisualClean(t *testing.T, what, body string) {
	t.Helper()
	for _, bad := range []string{"<pll-t", "data-pll-", "pll-visual", "<pll-uc", "pll:uc"} {
		if strings.Contains(body, bad) {
			t.Errorf("%s contains %q", what, bad)
		}
	}
}

func pllVisualSave(t *testing.T, c *http.Client, base, lang, original, translation string) {
	t.Helper()
	code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/visual/strings",
		map[string]any{"lang": lang, "items": []any{map[string]any{"original": original, "translation": translation}}})
	if code != http.StatusOK {
		t.Fatalf("save %q: %d %v", original, code, obj)
	}
}

func TestGolylangVisualTT5(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	installAndLogin(t, ts, c)

	// Off: no route, no toolbar item.
	if code, _, _ := get(t, c, base+pllVisualURL("/", "fr")); code != http.StatusForbidden {
		t.Errorf("visual shell while off: %d", code)
	}
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	// One language: the shell explains, the toolbar has no item.
	if _, body, _ := get(t, c, base+pllVisualURL("/", "")); !strings.Contains(body, "Add at least two languages") || strings.Contains(body, "pll-v-frame") {
		t.Error("visual shell with one language")
	}
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")

	_, enLink := pllCreatePost(t, c, base, "", "Visual EN")
	frID, frLink := pllCreatePost(t, c, base, "?lang=fr", "Visual FR")
	rel := func(link string) string { u, _ := url.Parse(link); return u.RequestURI() }

	// Toolbar entry on the front end.
	_, home, _ := get(t, c, base+"/")
	if !strings.Contains(home, "wp-admin-bar-translate-page") || !strings.Contains(home, "Translate Page") ||
		!strings.Contains(home, "page=mlang_visual&amp;url=%2F&amp;lang=fr") {
		t.Error("the front toolbar has no Translate Page item")
	}
	// Entry links: Translations screen and posts list row action.
	if _, body, _ := get(t, c, base+"/wp-admin/admin?page=mlang_strings"); !strings.Contains(body, "page=mlang_visual") {
		t.Error("Translations screen has no Translate visually link")
	}
	if _, body, _ := get(t, c, base+"/wp-admin/edit"); !strings.Contains(body, "Translate visually") || !strings.Contains(body, "page=mlang_visual") {
		t.Error("posts list has no Translate visually row action")
	}

	// Shell: open-redirect safe url parameter.
	// javascript: URLs never reach the handler: the core firewall answers 403 first.
	if code, _, _ := get(t, c, base+pllVisualURL("javascript:alert(1)", "fr")); code != http.StatusForbidden {
		t.Errorf("visual shell javascript: url: %d, want 403 from the firewall", code)
	}
	for _, evil := range []string{"https://evil.example/x", "//evil.example/x", "/\\evil.example", "/wp-admin/options"} {
		body, _ := pllVisualOpen(t, c, base, evil, "fr")
		if strings.Contains(body, "evil.example") || strings.Contains(body, "javascript:") || strings.Contains(body, "/wp-admin/options") {
			t.Errorf("shell keeps url %q", evil)
		}
		if !strings.Contains(body, `id="pll-visual-close" href="/"`) {
			t.Errorf("shell for %q does not fall back to /", evil)
		}
	}
	// Unknown language: the first one to translate into.
	if body, _ := pllVisualOpen(t, c, base, "/", "zz"); !strings.Contains(body, `data-lang="fr"`) {
		t.Error("unknown language not replaced")
	}

	shell, tok := pllVisualOpen(t, c, base, rel(frLink), "fr")
	if !strings.Contains(shell, "Strings on this page") || !strings.Contains(shell, `src="`+html.EscapeString(rel(frLink))) {
		t.Errorf("shell:\n%s", shell)
	}

	// The visual page: markers, regions, data island, script; no toolbar.
	body, d := pllVisualPageGet(t, c, base, rel(frLink), tok)
	if d == nil {
		t.Fatalf("no data island:\n%s", body)
	}
	for _, want := range []string{"<pll-t data-pll-s=", `<pll-uc data-pll-post="` + itoa64(frID) + `"`, "/wp-includes/js/pll-visual.js", "/wp-includes/css/pll-visual.css"} {
		if !strings.Contains(body, want) {
			t.Errorf("visual page lacks %q", want)
		}
	}
	if strings.Contains(body, "wpadminbar") || strings.Contains(body, "pll:uc") {
		t.Error("visual page has the toolbar or a sentinel")
	}
	if d.Lang.Slug != "fr" || d.Want != "fr" {
		t.Errorf("data island language %+v want %q", d.Lang, d.Want)
	}
	skip := pllVisualFind(d, "Skip to content")
	if skip == nil || skip.Key != "Skip to content" || skip.Translation != "" || !strings.Contains(body, `data-pll-s="`+skip.M+`"`) {
		t.Fatalf("Skip to content not listed: %+v", skip)
	}
	if pllVisualFind(d, "Visual FR body") != nil {
		t.Error("post content listed as a string")
	}
	var region *pllVisualPost
	for i := range d.Posts {
		if d.Posts[i].ID == frID {
			region = &d.Posts[i]
		}
	}
	if region == nil || region.Lang != "fr" || region.EditURL != "/wp-admin/post?post="+itoa64(frID)+"&action=edit" {
		t.Errorf("post region: %+v", region)
	}

	// Normal requests: no marker, no script (logged in without token, anonymous, foreign token).
	_, plain, _ := get(t, c, base+rel(frLink))
	pllVisualClean(t, "logged-in page", plain)
	if !strings.Contains(plain, "wpadminbar") {
		t.Error("toolbar missing on the normal page")
	}
	anon := anonGet(t, base+rel(frLink)+"?"+pllVisualParam+"="+tok)
	pllVisualClean(t, "anonymous page with the token", bodyOf(anon))
	pllVisualClean(t, "anonymous page", bodyOf(anonGet(t, base+rel(frLink))))
	if _, b, _ := get(t, c, base+rel(frLink)+"?"+pllVisualParam+"=0123456789abcdef0123456789abcdef"); strings.Contains(b, "pll-visual") {
		t.Error("an unknown token opens visual mode")
	}
	// The token is bound to its user: another administrator gets the normal page.
	asCreateUser(t, srv, "visual-admin2", "administrator", asStrongPass)
	other := asNewClient()
	if code, _, _ := asLogin(t, other, base, "/wp-login", "visual-admin2", asStrongPass); code != http.StatusFound {
		t.Fatalf("login second admin: %d", code)
	}
	if _, b, _ := get(t, other, base+rel(frLink)+"?"+pllVisualParam+"="+tok); strings.Contains(b, "pll-visual") || !strings.Contains(b, "wpadminbar") {
		t.Error("the token works for another user")
	}
	// An editor cannot open the shell or save.
	asCreateUser(t, srv, "visual-editor", "editor", asStrongPass)
	ed := asNewClient()
	if code, _, _ := asLogin(t, ed, base, "/wp-login", "visual-editor", asStrongPass); code != http.StatusFound {
		t.Fatalf("login editor: %d", code)
	}
	if code, _, _ := get(t, ed, base+pllVisualURL("/", "fr")); code != http.StatusForbidden {
		t.Errorf("editor opens the shell: %d", code)
	}
	if code, _, _ := pllREST(t, ed, base, http.MethodPost, "/wp-json/pll/v1/visual/strings",
		map[string]any{"lang": "fr", "items": []any{map[string]any{"original": "Skip to content", "translation": "x"}}}); code != http.StatusForbidden {
		t.Errorf("editor saves a string: %d", code)
	}

	// Save validation: nonce, default language, bad items.
	req, _ := http.NewRequest(http.MethodPost, base+"/wp-json/pll/v1/visual/strings", strings.NewReader(`{"lang":"fr","items":[{"original":"Skip to content","translation":"x"}]}`))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := c.Do(req); err != nil || resp.StatusCode == http.StatusOK {
		t.Errorf("save without nonce: %v %v", err, resp)
	}
	for _, bad := range []map[string]any{
		{"lang": "en", "items": []any{map[string]any{"original": "Skip to content", "translation": "x"}}},
		{"lang": "fr", "items": []any{}},
		{"lang": "fr", "items": []any{map[string]any{"original": "", "translation": "x"}}},
		{"lang": "zz", "items": []any{map[string]any{"original": "Skip to content", "translation": "x"}}},
	} {
		if code, _, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/visual/strings", bad); code != http.StatusBadRequest {
			t.Errorf("save %v: %d", bad, code)
		}
	}

	// Saving a string changes the translated page (also for visitors), not the default language.
	pllVisualSave(t, c, base, "fr", skip.Key, "Aller au contenu")
	if b := bodyOf(anonGet(t, base+rel(frLink))); !strings.Contains(b, "Aller au contenu") || strings.Contains(b, "Skip to content") {
		t.Error("the French page does not show the saved translation")
	}
	if b := bodyOf(anonGet(t, base+rel(enLink))); !strings.Contains(b, "Skip to content") || strings.Contains(b, "Aller au contenu") {
		t.Error("the English page changed")
	}
	// In visual mode the string still edits its original.
	_, d = pllVisualPageGet(t, c, base, rel(frLink), tok)
	if it := pllVisualFind(d, "Skip to content"); it == nil || it.Translation != "Aller au contenu" || it.Shown != "Aller au contenu" {
		t.Errorf("translated string in visual mode: %+v", it)
	}
	// A page text the Strings screen did not know is listed there after a save.
	var title *pllVisualItem
	for i := range d.Strings {
		if d.Strings[i].Original == "Comments" || d.Strings[i].Original == "More posts" {
			title = &d.Strings[i]
		}
	}
	if title != nil {
		pllVisualSave(t, c, base, "fr", title.Key, "Traduit "+title.Original)
		var n int
		_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM pll_string_sources WHERE original = $1`, title.Key).Scan(&n)
		if n == 0 {
			t.Errorf("%q not in pll_string_sources after a save", title.Key)
		}
	}
	// Deleting a translation (empty) restores the original.
	pllVisualSave(t, c, base, "fr", skip.Key, "")
	if b := bodyOf(anonGet(t, base+rel(frLink))); !strings.Contains(b, "Skip to content") {
		t.Error("emptied translation not removed")
	}

	// A default-language page in a French session: listed, read-only, regions offer the translation.
	_, d = pllVisualPageGet(t, c, base, rel(enLink), tok)
	if d == nil || !d.Lang.Default || d.Want != "fr" || len(d.Posts) == 0 {
		t.Fatalf("default-language visual page: %+v", d)
	}
	for _, p := range d.Posts {
		if p.Lang != "en" || p.Target != "fr" || p.TrID != 0 || !strings.Contains(p.CreateURL, "from_post="+itoa64(p.ID)) {
			t.Errorf("untranslated post region without a create link: %+v", p)
		}
	}
}

func TestGolylangVisualImages(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	img := &store.Attachment{Slug: "vis", FilePath: "2026/11/vis.png", Mime: "image/png", Width: 800, Height: 600, Alt: "A red bike"}
	if err := srv.store.InsertAttachment(ctx, img); err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(img.ID, 10)
	code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts?lang=fr", map[string]any{"title": "Images FR", "status": "publish",
		"content": `<!-- wp:image {"id":` + id + `} --><figure class="wp-block-image"><img src="/wp-content/uploads/2026/11/vis.png" alt="A red bike" class="wp-image-` + id + `"/></figure><!-- /wp:image -->`})
	if code != http.StatusCreated {
		t.Fatalf("create post: %d %v", code, obj)
	}
	u, _ := url.Parse(obj["link"].(string))
	path := u.RequestURI()
	if code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/settings", map[string]any{"media_support": true}); code != http.StatusOK {
		t.Fatalf("enable media: %d %v", code, obj)
	}
	shell, tok := pllVisualOpen(t, c, base, path, "fr")
	if !strings.Contains(shell, "pll-v-img-save") {
		t.Error("the shell has no image editor while the media module is on")
	}
	body, d := pllVisualPageGet(t, c, base, path, tok)
	if d == nil || !strings.Contains(body, `<img data-pll-img="`+id+`"`) {
		t.Fatalf("image not marked:\n%s", body)
	}
	found := false
	for _, im := range d.Images {
		found = found || (im.ID == img.ID && im.Alt == "A red bike")
	}
	if !found {
		t.Errorf("image not in the data island: %+v", d.Images)
	}
	// The panel's image editor: GET then POST pll/v1/media/{id}/i18n.
	if code, obj, _ := pllREST(t, c, base, http.MethodGet, "/wp-json/pll/v1/media/"+id+"/i18n", nil); code != http.StatusOK || obj["base"].(map[string]any)["alt_text"] != "A red bike" {
		t.Fatalf("image texts: %d %v", code, obj)
	}
	if code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/media/"+id+"/i18n", map[string]any{"fr": map[string]any{"alt_text": "Un vélo rouge"}}); code != http.StatusOK {
		t.Fatalf("save alt: %d %v", code, obj)
	}
	if b := bodyOf(anonGet(t, base+path)); !strings.Contains(b, `alt="Un vélo rouge"`) {
		t.Error("the French page does not show the translated alt text")
	}
	_, d = pllVisualPageGet(t, c, base, path, tok)
	for _, im := range d.Images {
		if im.ID == img.ID && im.Alt != "Un vélo rouge" {
			t.Errorf("visual page alt %q", im.Alt)
		}
	}
}

func TestGolylangVisualPackageTheme(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	installLibraryTheme(t, c, base, "launchpad", true)
	if srv.activeTheme().Slug != "launchpad" {
		t.Fatalf("active theme %s", srv.activeTheme().Slug)
	}
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	_, tok := pllVisualOpen(t, c, base, "/fr/", "fr")
	body, d := pllVisualPageGet(t, c, base, "/fr/", tok)
	if d == nil {
		t.Fatalf("no data island:\n%s", body)
	}
	cta := pllVisualFind(d, "Start free trial") // the Customizer's cta_label (text theme_mod)
	if cta == nil || !strings.Contains(body, `data-pll-s="`+cta.M+`"`) {
		t.Fatalf("the CTA theme mod is not listed: %+v", d.Strings)
	}
	pllVisualSave(t, c, base, "fr", cta.Key, "Essai gratuit")
	if b := bodyOf(anonGet(t, base+"/fr/")); !strings.Contains(b, "Essai gratuit") || strings.Contains(b, "Start free trial") {
		t.Error("the French page does not show the translated CTA")
	}
	if b := bodyOf(anonGet(t, base+"/")); !strings.Contains(b, "Start free trial") {
		t.Error("the English CTA changed")
	}
	// Translated by the theme_mod hook, the string keeps its original in visual mode.
	_, d = pllVisualPageGet(t, c, base, "/fr/", tok)
	if it := pllVisualFind(d, "Start free trial"); it == nil || it.Translation != "Essai gratuit" {
		t.Errorf("translated CTA in visual mode: %+v", it)
	}
	if pllVisualFind(d, "Essai gratuit") != nil {
		t.Error("the translation is listed as an original")
	}
	pllVisualClean(t, "anonymous launchpad page", bodyOf(anonGet(t, base+"/fr/")))
}

func TestGolylangVisualWebBuilder(t *testing.T) {
	srv, base, c := wbtfStart(t)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	// Hello Pursae with a Theme Builder header and footer.
	wbtfTemplate(t, srv, webbuilder.TemplateHeader, "Site header", []string{"include/general"}, `<p class="hdr">Book your table today</p>`, "", "", nil)
	wbtfTemplate(t, srv, webbuilder.TemplateFooter, "Site footer", []string{"include/general"}, `<p class="ftr">Open every day from noon</p>`, "", "", nil)
	_, tok := pllVisualOpen(t, c, base, "/fr/", "fr")
	body, d := pllVisualPageGet(t, c, base, "/fr/", tok)
	if d == nil {
		t.Fatalf("no data island:\n%s", body)
	}
	if !strings.Contains(body, `class="wb-part wb-part-header"`) {
		t.Fatalf("not the Theme Builder header:\n%s", body)
	}
	hdr, ftr := pllVisualFind(d, "Book your table today"), pllVisualFind(d, "Open every day from noon")
	if hdr == nil || ftr == nil {
		t.Fatalf("header/footer strings not listed: %+v", d.Strings)
	}
	pllVisualSave(t, c, base, "fr", hdr.Key, "Réservez votre table")
	wbfPurge(srv)
	if b := bodyOf(anonGet(t, base+"/fr/")); !strings.Contains(b, "Réservez votre table") {
		t.Error("the French header is not translated")
	}
	if b := bodyOf(anonGet(t, base+"/")); !strings.Contains(b, "Book your table today") {
		t.Error("the English header changed")
	}

	// A Web Builder page: its snapshot is a content region offering the builder.
	p := wbtfPage(t, srv, "page", "Visual WB", `<section><h1>WB-CANVAS-MARK</h1></section>`, "", "")
	body, d = pllVisualPageGet(t, c, base, "/visual-wb/", tok)
	if d == nil {
		t.Fatalf("no data island on the builder page:\n%s", body)
	}
	if !strings.Contains(body, `<pll-uc data-pll-post="`+itoa64(p.ID)+`"`) || !strings.Contains(body, "WB-CANVAS-MARK") {
		t.Errorf("builder page without its region:\n%s", body)
	}
	var region *pllVisualPost
	for i := range d.Posts {
		if d.Posts[i].ID == p.ID {
			region = &d.Posts[i]
		}
	}
	if region == nil || region.BuilderURL != wbEditURL(p.ID) || region.CreateURL == "" || region.Target != "fr" {
		t.Errorf("builder region: %+v", region)
	}
	if pllVisualFind(d, "WB-CANVAS-MARK") != nil {
		t.Error("builder content listed as a string")
	}
	pllVisualClean(t, "anonymous builder page", bodyOf(anonGet(t, base+"/visual-wb/")))
	pllVisualClean(t, "anonymous Hello Pursae page", bodyOf(anonGet(t, base+"/fr/")))
}

// A Web Builder canvas page (a theme other than Hello Pursae): the builder writes the whole
// document; the visual page still gets its region, data island and script.
func TestGolylangVisualWBCanvas(t *testing.T) {
	srv, base, c := wbfStart(t)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	p := wbfPage(t, srv, "page", "Canvas Visual", []string{}, "", "")
	wbfPurge(srv)
	path := strings.TrimPrefix(wbfURL(base, p), base)
	_, tok := pllVisualOpen(t, c, base, path, "fr")
	body, d := pllVisualPageGet(t, c, base, path, tok)
	if d == nil {
		t.Fatalf("no data island on the canvas page:\n%s", body)
	}
	for _, want := range []string{wbfSnapshotMark, `<pll-uc data-pll-post="` + itoa64(p.ID) + `"`, "/wp-includes/js/pll-visual.js"} {
		if !strings.Contains(body, want) {
			t.Errorf("canvas visual page lacks %q", want)
		}
	}
	if strings.Contains(body, "wpadminbar") {
		t.Error("canvas visual page has the toolbar")
	}
	var region *pllVisualPost
	for i := range d.Posts {
		if d.Posts[i].ID == p.ID {
			region = &d.Posts[i]
		}
	}
	if region == nil || region.BuilderURL != wbEditURL(p.ID) || region.CreateURL == "" {
		t.Errorf("canvas region: %+v", region)
	}
	if pllVisualFind(d, wbfSnapshotMark) != nil {
		t.Error("builder content listed as a string")
	}
	pllVisualClean(t, "anonymous canvas page", bodyOf(anonGet(t, wbfURL(base, p))))
	_, plain, _ := get(t, c, wbfURL(base, p))
	pllVisualClean(t, "logged-in canvas page", plain)
}
