package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

func TestPllMediaHelpers(t *testing.T) {
	if id := pllImgID(`<img src="/a.png" class="wp-image-42 size-full">`); id != 42 {
		t.Errorf("wp-image id = %d", id)
	}
	if id := pllImgID(`<img data-wb-media-id="7" src="/a.png">`); id != 7 {
		t.Errorf("data-wb-media-id = %d", id)
	}
	if id := pllImgID(`<img src="/a.png">`); id != 0 {
		t.Errorf("no id = %d", id)
	}
	for in, want := range map[string]string{
		`<img src="http://x.test/wp-content/uploads/2026/11/a-300x200.png?v=1" alt="">`: "2026/11/a-300x200.png",
		`<img src="/wp-content/uploads/../secret.png">`:                                 "",
		`<img src="https://cdn.example.com/a.png">`:                                     "",
	} {
		if got := pllImgUploadPath(in); got != want {
			t.Errorf("pllImgUploadPath(%s) = %q, want %q", in, got, want)
		}
	}
	if pllNormText(" Tom &amp; \n Jerry ") != "Tom & Jerry" {
		t.Error("pllNormText")
	}
	blank := "   "
	if pllMediaClean("alt", &blank) != nil {
		t.Error("a blank text is not cleared")
	}
	long := strings.Repeat("é", pllMediaMaxLen+10)
	if v := pllMediaClean("description", &long); v == nil || len([]rune(*v)) != pllMediaMaxLen {
		t.Error("a long text is not capped")
	}
	nul := " a\x00b "
	if v := pllMediaClean("title", &nul); v == nil || *v != "ab" {
		t.Errorf("clean title = %v", v)
	}

	// REST view detection: only GET /wp-json reads outside context=edit.
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		method, target string
		want           bool
	}{
		{http.MethodGet, "/wp-json/wp/v2/media/5?lang=fr", true},
		{http.MethodGet, "/wp-json/wp/v2/media/5?lang=fr&context=edit", false},
		{http.MethodPost, "/wp-json/wp/v2/media/5?lang=fr", false},
		{http.MethodGet, "/wp-json/wp/v2/media/5?lang=fr&_method=POST", false},
		{http.MethodGet, "/wp-admin/upload?lang=fr", false},
		{http.MethodGet, "/?rest_route=/wp/v2/media/5&lang=fr", true},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(tc.method, tc.target, nil)
		if got := pllMediaRESTView(c); got != tc.want {
			t.Errorf("%s %s: %v, want %v", tc.method, tc.target, got, tc.want)
		}
	}
	if pllMediaRESTView(context.Background()) {
		t.Error("a plain context counts as a REST view")
	}
	if pllMediaLang(context.Background()) != nil {
		t.Error("a context without a language gets an overlay language")
	}
}

func TestGolylangMediaTranslations(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")

	img := &store.Attachment{Slug: "pic", FilePath: "2026/11/pic.png", Mime: "image/png", Width: 800, Height: 600,
		Alt: "Base alt", Title: "Base title", Caption: "Base caption"}
	feat := &store.Attachment{Slug: "feat", FilePath: "2026/11/feat.png", Mime: "image/png", Width: 800, Height: 600, Alt: "Featured base"}
	for _, a := range []*store.Attachment{img, feat} {
		if err := srv.store.InsertAttachment(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	id := strconv.FormatInt(img.ID, 10)
	content := `<!-- wp:image {"id":` + id + `} --><figure class="wp-block-image"><img src="/wp-content/uploads/2026/11/pic.png" alt="Base alt" class="wp-image-` + id + `"/><figcaption class="wp-element-caption">Base caption</figcaption></figure><!-- /wp:image -->` +
		`<!-- wp:image --><figure class="wp-block-image"><img src="/wp-content/uploads/2026/11/pic.png" alt="Custom alt" class="wp-image-` + id + `"/><figcaption class="wp-element-caption">My own caption</figcaption></figure><!-- /wp:image -->` +
		`<!-- wp:html --><p><img src="/wp-content/uploads/2026/11/pic.png" alt="" class="builder-img"></p><!-- /wp:html -->`
	mk := func(query, title string) string {
		code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts"+query, map[string]any{"title": title, "content": content,
			"status": "publish", "featured_media": feat.ID})
		if code != http.StatusCreated {
			t.Fatalf("create post %q: %d %v", title, code, obj)
		}
		link, _ := obj["link"].(string)
		return link
	}
	enLink := mk("", "Media EN")
	frLink := mk("?lang=fr", "Media FR")

	// The module is off: no route, no fields, base texts everywhere.
	if code, _, _ := pllREST(t, c, base, http.MethodGet, "/wp-json/pll/v1/media/"+id+"/i18n", nil); code != http.StatusNotFound {
		t.Errorf("pll/v1/media while the media module is off: %d", code)
	}
	if _, body, _ := get(t, c, base+"/wp-admin/post?post="+id+"&action=edit"); strings.Contains(body, "pll_i18n[") {
		t.Error("the edit screen shows translation fields while the media module is off")
	}
	if code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/settings", map[string]any{"media_support": true}); code != http.StatusOK || obj["media_support"] != true {
		t.Fatalf("enable media: %d %v", code, obj)
	}

	// The default language is the media row itself.
	if code, _, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/media/"+id+"/i18n", map[string]any{"en": map[string]any{"alt_text": "x"}}); code != http.StatusBadRequest {
		t.Errorf("default language override: %d, want 400", code)
	}
	code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/media/"+id+"/i18n",
		map[string]any{"fr": map[string]any{"alt_text": "Texte alt", "caption": "Légende <b>FR</b><script>x()</script>"}})
	if code != http.StatusOK {
		t.Fatalf("pll/v1/media POST: %d %v", code, obj)
	}
	fr, _ := obj["i18n"].(map[string]any)["fr"].(map[string]any)
	if fr["alt_text"] != "Texte alt" || fr["title"] != nil {
		t.Fatalf("i18n after POST: %v", obj["i18n"])
	}
	if b, _ := obj["base"].(map[string]any); b["alt_text"] != "Base alt" {
		t.Errorf("base texts: %v", obj["base"])
	}
	if code, _, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/media/"+strconv.FormatInt(feat.ID, 10)+"/i18n",
		map[string]any{"i18n": map[string]any{"fr": map[string]any{"alt_text": "Image à la une"}}}); code != http.StatusOK {
		t.Fatalf("featured override: %d", code)
	}

	// Front end: French page gets the French texts, author-changed texts stay, English is untouched.
	_, body, _ := get(t, c, frLink)
	for _, want := range []string{`alt="Texte alt"`, `alt="Custom alt"`, "My own caption", "Légende <b>FR</b>", `alt="Image à la une"`} {
		if !strings.Contains(body, want) {
			t.Errorf("French page lacks %q", want)
		}
	}
	if strings.Contains(body, "x()") || strings.Contains(body, ">Base caption<") || strings.Contains(body, "Featured base") {
		t.Error("French page keeps a base text or an unsafe caption")
	}
	if n := strings.Count(body, `alt="Texte alt"`); n < 2 {
		t.Errorf("the uploads-URL image without an id was not translated (%d French alts)", n)
	}
	_, body, _ = get(t, c, enLink)
	if strings.Contains(body, "Texte alt") || strings.Contains(body, "Image à la une") || !strings.Contains(body, `alt="Base alt"`) || !strings.Contains(body, "Featured base") {
		t.Error("the English page shows French media texts")
	}
	// Anonymous visitors get the same translated page.
	anon := &http.Client{CheckRedirect: c.CheckRedirect}
	if _, body, _ := get(t, anon, frLink); !strings.Contains(body, `alt="Texte alt"`) {
		t.Error("anonymous French page lacks the French alt")
	}

	// REST: view with ?lang=fr overlays, context=edit keeps the base and lists pll_i18n.
	_, raw := restGet(t, c, base, "/wp-json/wp/v2/media/"+id+"?lang=fr")
	var m map[string]any
	_ = json.Unmarshal([]byte(raw), &m)
	if m["alt_text"] != "Texte alt" || m["pll_i18n"] != nil {
		t.Errorf("media?lang=fr: alt %v, pll_i18n %v", m["alt_text"], m["pll_i18n"])
	}
	_, raw = restGet(t, c, base, "/wp-json/wp/v2/media/"+id+"?lang=fr&context=edit")
	m = nil
	_ = json.Unmarshal([]byte(raw), &m)
	if m["alt_text"] != "Base alt" {
		t.Errorf("media?context=edit alt = %v", m["alt_text"])
	}
	if pi, _ := m["pll_i18n"].(map[string]any); pi == nil || pi["fr"].(map[string]any)["alt_text"] != "Texte alt" {
		t.Errorf("pll_i18n = %v", m["pll_i18n"])
	}
	code, obj, _ = pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/media/"+id, map[string]any{"pll_i18n": map[string]any{"fr": map[string]any{"title": "Titre"}}})
	if code != http.StatusOK || obj["pll_i18n"].(map[string]any)["fr"].(map[string]any)["title"] != "Titre" {
		t.Errorf("wp/v2/media update pll_i18n: %d %v", code, obj["pll_i18n"])
	}
	if code, _, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/media/"+id, map[string]any{"pll_i18n": "bad"}); code != http.StatusBadRequest {
		t.Errorf("bad pll_i18n: %d", code)
	}

	// The media row was never overwritten.
	if a, _ := srv.store.GetAttachment(ctx, img.ID); a.Alt != "Base alt" || a.Caption != "Base caption" || a.Title != "Base title" || a.Base != nil {
		t.Fatalf("media row changed: %+v", a)
	}

	// Edit screen: the Translations box saves; an emptied field falls back.
	_, body, _ = get(t, c, base+"/wp-admin/post?post="+id+"&action=edit")
	if !strings.Contains(body, `id="pll_media_i18n"`) || !strings.Contains(body, `name="pll_i18n[fr][alt]"`) || !strings.Contains(body, "Texte alt") {
		t.Fatal("the edit screen lacks the Translations box")
	}
	if strings.Contains(body, `name="pll_i18n[en]`) {
		t.Error("the edit screen has fields for the default language")
	}
	code, _, loc := post(t, c, base+"/wp-admin/post", url.Values{"_wpnonce": {formNonce(t, body)}, "post_ID": {id}, "post_type": {"attachment"},
		"action": {"editpost"}, "post_title": {"Base title"}, "_wp_attachment_image_alt": {"Base alt"}, "excerpt": {"Base caption"}, "content": {""},
		"pll_i18n[fr][alt]": {"Alt FR 2"}, "pll_i18n[fr][title]": {"Titre"}, "pll_i18n[fr][caption]": {""}, "pll_i18n[fr][description]": {""}})
	if code != http.StatusFound || !strings.Contains(loc, "message=4") {
		t.Fatalf("save attachment: %d %s", code, loc)
	}
	rows := srv.pllMediaRows(ctx, "fr", []int64{img.ID})[img.ID]["fr"]
	if rows == nil || rows["alt"] == nil || *rows["alt"] != "Alt FR 2" || rows["caption"] != nil {
		t.Fatalf("saved overrides: %v", rows)
	}

	// Media modal: compat fields and save-attachment-compat.
	_, ajax, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"get-attachment"}, "id": {id}})
	var js struct {
		Data struct {
			Compat struct{ Item string }   `json:"compat"`
			Nonces struct{ Update string } `json:"nonces"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(ajax), &js); err != nil || !strings.Contains(js.Data.Compat.Item, `name="attachments[`+id+`][pll_i18n][fr][alt]"`) {
		t.Fatalf("compat item: %v %.300s", err, ajax)
	}
	_, ajax, _ = post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"save-attachment-compat"}, "id": {id}, "nonce": {js.Data.Nonces.Update},
		"attachments[" + id + "][pll_i18n][fr][caption]": {"Nouvelle légende"}})
	if !strings.Contains(ajax, `"success":true`) {
		t.Fatalf("save-attachment-compat: %.300s", ajax)
	}
	rows = srv.pllMediaRows(ctx, "fr", []int64{img.ID})[img.ID]["fr"]
	if rows["caption"] == nil || *rows["caption"] != "Nouvelle légende" || rows["alt"] == nil || *rows["alt"] != "Alt FR 2" {
		t.Errorf("compat save: %v", rows)
	}
	_, ajax, _ = post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"save-attachment-compat"}, "id": {id}, "nonce": {"bad"},
		"attachments[" + id + "][pll_i18n][fr][caption]": {"Hack"}})
	if rows := srv.pllMediaRows(ctx, "fr", []int64{img.ID})[img.ID]["fr"]; *rows["caption"] == "Hack" {
		t.Error("save-attachment-compat saved without a valid nonce")
	}

	// List view: a column per language; the Media Library is not filtered by the admin language.
	_, body, _ = get(t, c, base+"/wp-admin/upload?mode=list&lang=fr")
	if !strings.Contains(body, "column-language_fr") || !strings.Contains(body, "pll_icon_edit") || !strings.Contains(body, "pll_icon_tick") {
		t.Error("the media list lacks the language columns")
	}
	if !strings.Contains(body, `id="post-`+id+`"`) {
		t.Error("the media list is filtered by the admin language")
	}
	_, _, _ = get(t, c, base+"/wp-admin/upload?mode=list&lang=all")

	// WXR: the overrides travel with the attachment.
	exp := srv.pllMediaExport(ctx)
	if !strings.Contains(exp[img.ID], "Alt FR 2") {
		t.Fatalf("export: %v", exp)
	}
	other := &store.Attachment{Slug: "other", FilePath: "2026/11/other.png", Mime: "image/png"}
	_ = srv.store.InsertAttachment(ctx, other)
	srv.pllMediaImport(ctx, other.ID, exp[img.ID])
	if r := srv.pllMediaRows(ctx, "fr", []int64{other.ID})[other.ID]["fr"]; r == nil || *r["alt"] != "Alt FR 2" {
		t.Errorf("import: %v", r)
	}

	// Module off again: base texts on the French page.
	pllREST(t, c, base, http.MethodPost, "/wp-json/pll/v1/settings", map[string]any{"media_support": false})
	if _, body, _ := get(t, c, frLink); strings.Contains(body, "Alt FR 2") || !strings.Contains(body, `alt="Base alt"`) {
		t.Error("French media texts show while the media module is off")
	}
	if _, body, _ := get(t, c, base+"/wp-admin/upload?mode=list"); strings.Contains(body, "column-language_fr") {
		t.Error("language columns show while the media module is off")
	}
}
