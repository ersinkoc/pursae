package server

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"wpgo/internal/store"
	"wpgo/web"
)

// i18nMO builds a .mo file (little endian) from msgid -> msgstr.
func i18nMO(entries map[string]string) []byte {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	n := len(keys)
	var origs, trans bytes.Buffer
	var oo, to [][2]int
	for _, k := range keys {
		oo = append(oo, [2]int{len(k), origs.Len()})
		origs.WriteString(k + "\x00")
		to = append(to, [2]int{len(entries[k]), trans.Len()})
		trans.WriteString(entries[k] + "\x00")
	}
	base := 28 + n*16
	var b bytes.Buffer
	w := func(v int) { _ = binary.Write(&b, binary.LittleEndian, uint32(v)) }
	for _, v := range []int{0x950412de, 0, n, 28, 28 + n*8, 0, 0} {
		w(v)
	}
	for _, s := range oo {
		w(s[0])
		w(base + s[1])
	}
	for _, s := range to {
		w(s[0])
		w(base + origs.Len() + s[1])
	}
	b.Write(origs.Bytes())
	b.Write(trans.Bytes())
	return b.Bytes()
}

func TestAdminLanguage(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installUsersSite(t, c, base)

	var pack bytes.Buffer
	zw := zip.NewWriter(&pack)
	f, _ := zw.Create("admin-de_DE.mo")
	f.Write(i18nMO(map[string]string{
		"": "Plural-Forms: nplurals=2; plural=n != 1;\n", "Posts": "Beiträge", "Settings": "Einstellungen",
		"Save Changes": "Änderungen speichern", "General Settings": "Allgemeine Einstellungen", "Hello world!": "Hallo Welt!",
		"Leave a Reply": "Schreibe einen Kommentar",
	}))
	zw.Close()
	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/core/1.0/":
			fmt.Fprintf(w, `{"translations":[{"language":"de_DE","package":"%s/de.zip"}]}`, api.URL)
		case "/de.zip":
			w.Write(pack.Bytes())
		default:
			fmt.Fprint(w, `{"translations":[]}`)
		}
	}))
	defer api.Close()
	srv.cfg.TranslationsAPI = api.URL

	// Settings › General lists the WordPress.org languages; choosing one installs its pack.
	_, body, _ := get(t, c, base+"/wp-admin/options-general")
	if !strings.Contains(body, `value="de_DE"`) || !strings.Contains(body, `value="tr_TR"`) {
		t.Fatal("Site Language lacks the WordPress.org languages")
	}
	form := url.Values{"option_page": {"general"}, "_wpnonce": {formNonce(t, body)}, "blogname": {"Sprach Site"}, "blogdescription": {""},
		"admin_email": {"admin@example.test"}, "timezone_string": {"UTC"}, "date_format": {"F j, Y"}, "time_format": {"g:i a"},
		"start_of_week": {"1"}, "WPLANG": {"de_DE"}}
	if code, _, loc := post(t, c, base+"/wp-admin/options", form); code != http.StatusFound || strings.Contains(loc, "lang-error") {
		t.Fatalf("save: %d %s", code, loc)
	}
	if srv.siteLocale() != "de_DE" || !srv.lang.IsInstalled("de_DE") {
		t.Fatalf("locale %s, installed %v", srv.siteLocale(), srv.lang.IsInstalled("de_DE"))
	}
	_, body, _ = get(t, c, base+"/wp-admin/options-general")
	for _, want := range []string{`<html class="wp-toolbar" lang="de-DE"`, "Allgemeine Einstellungen", `value="Änderungen speichern"`} {
		if !strings.Contains(body, want) {
			t.Errorf("admin not translated: missing %s", want)
		}
	}
	// User content stays as written: a post titled like a core string.
	p := &store.Post{Type: "post", Status: "publish", Title: "Settings", Slug: "settings-post", Content: "<p>x</p>", AuthorID: 1}
	if err := srv.store.InsertPost(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	_, body, _ = get(t, c, base+"/wp-admin/edit")
	if !strings.Contains(body, `translate="no" class="row-title"`) || !strings.Contains(body, `>Settings</a>`) {
		t.Error("post title translated in the list")
	}
	// The front end is not translated (it shows the site's content).
	_, front, _ := get(t, c, base+"/?p="+itoa64(p.ID))
	if strings.Contains(front, "Schreibe einen Kommentar") {
		t.Error("front end translated")
	}
	// Profile › Language: English for this user only.
	_, body, _ = get(t, c, base+"/wp-admin/profile")
	if !strings.Contains(body, `name="locale"`) {
		t.Fatal("no Language field on the profile")
	}
	pf := url.Values{"_wpnonce": {formNonce(t, body)}, "nickname": {"admin"}, "email": {"admin@example.test"}, "locale": {"en_US"}}
	post(t, c, base+"/wp-admin/profile", pf)
	_, body, _ = get(t, c, base+"/wp-admin/options-general")
	if !strings.Contains(body, "General Settings") || strings.Contains(body, "Allgemeine Einstellungen") {
		t.Error("the user's English did not override the site language")
	}
	// An unknown locale is refused.
	_, body, _ = get(t, c, base+"/wp-admin/options-general")
	form.Set("_wpnonce", formNonce(t, body))
	form.Set("WPLANG", "xx_XX")
	post(t, c, base+"/wp-admin/options", form)
	if srv.siteLocale() != "de_DE" {
		t.Errorf("unknown locale saved: %s", srv.siteLocale())
	}
}

// A right-to-left language loads the -rtl stylesheets, and the strings the admin scripts build
// in the browser come from wpgo-l10n.js.
func TestAdminRTLAndScriptTranslations(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installUsersSite(t, c, base)

	lits := srv.adminJSStrings()
	if len(lits) < 50 {
		t.Fatalf("only %d script strings found", len(lits))
	}
	lit := ""
	for _, l := range lits {
		if l == "Delete this field?" {
			lit = l
		}
	}
	if lit == "" {
		t.Fatal(`"Delete this field?" (Pursae Custom Fields's field group editor) not among the script strings`)
	}
	var pack bytes.Buffer
	zw := zip.NewWriter(&pack)
	f, _ := zw.Create("admin-he_IL.mo")
	f.Write(i18nMO(map[string]string{"": "Plural-Forms: nplurals=2; plural=n != 1;\n", "Plugins": "תוספים", lit: "למחוק את השדה?",
		"Built-in": "מובנה"}))
	zw.Close()
	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/core/1.0/":
			fmt.Fprintf(w, `{"translations":[{"language":"he_IL","package":"%s/he.zip"}]}`, api.URL)
		case "/he.zip":
			w.Write(pack.Bytes())
		default:
			fmt.Fprint(w, `{"translations":[]}`)
		}
	}))
	defer api.Close()
	srv.cfg.TranslationsAPI = api.URL
	_, body, _ := get(t, c, base+"/wp-admin/options-general")
	form := url.Values{"option_page": {"general"}, "_wpnonce": {formNonce(t, body)}, "blogname": {"RTL"}, "blogdescription": {""},
		"admin_email": {"admin@example.test"}, "timezone_string": {"UTC"}, "date_format": {"F j, Y"}, "time_format": {"g:i a"},
		"start_of_week": {"1"}, "WPLANG": {"he_IL"}}
	post(t, c, base+"/wp-admin/options", form)

	_, body, _ = get(t, c, base+"/wp-admin/plugins")
	for _, want := range []string{`lang="he-IL" dir="rtl"`, `<body class="rtl `, "-rtl.min.css", "/wp-admin/wpgo-l10n.js?ver=he_IL-", ">מובנה<"} {
		if !strings.Contains(body, want) {
			t.Errorf("plugins screen lacks %s", want)
		}
	}
	if strings.Contains(body, "common.min.css") && !strings.Contains(body, "common-rtl.min.css") {
		t.Error("left-to-right common.min.css still loaded")
	}
	code, js, _ := get(t, c, base+"/wp-admin/wpgo-l10n.js")
	if code != http.StatusOK || !strings.Contains(js, "window.WPGOL10n") || !strings.Contains(js, `"למחוק את השדה?"`) {
		t.Errorf("wpgo-l10n.js: %d %.200s", code, js)
	}
	// An -rtl stylesheet exists for every admin stylesheet it rewrites to.
	for _, m := range stylesheetHref.FindAllStringSubmatch(body, -1) {
		if p := m[2]; strings.HasSuffix(p, "-rtl") {
			if code, _, _ := get(t, c, base+p+m[3]); code != http.StatusOK {
				t.Errorf("%s%s: %d", p, m[3], code)
			}
		}
	}
}

// TestEmbeddedOwnStrings guards the embed list of web/web.go: the Docker image has no WPGO_WEB_DIR,
// so WordPress Go's own translations must be compiled in.
func TestEmbeddedOwnStrings(t *testing.T) {
	fsys := web.FS("")
	for _, name := range []string{"languages/wpgo-tr_TR.json", "languages/wpgo-de_DE.json", "languages/wpgo-ar.json"} {
		if b, err := fs.ReadFile(fsys, name); err != nil || len(b) < 1000 {
			t.Errorf("%s is not embedded: %v", name, err)
		}
	}
}

// TestInstallAndLoginLanguage is WordPress' language choice: first on the install screen (the pack
// is downloaded and becomes the site language), then in the language dropdown of the login screen.
func TestInstallAndLoginLanguage(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	var pack bytes.Buffer
	zw := zip.NewWriter(&pack)
	f, _ := zw.Create("admin-de_DE.mo")
	f.Write(i18nMO(map[string]string{"": "Plural-Forms: nplurals=2; plural=n != 1;\n", "Site Title": "Titel der Website", "Username": "Benutzername", "Language": "Sprache"}))
	zw.Close()
	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/core/1.0/":
			fmt.Fprintf(w, `{"translations":[{"language":"de_DE","package":"%s/de.zip"}]}`, api.URL)
		case "/de.zip":
			w.Write(pack.Bytes())
		default:
			fmt.Fprint(w, `{"translations":[]}`)
		}
	}))
	defer api.Close()
	srv.cfg.TranslationsAPI = api.URL

	// Step 0: the language list.
	_, body, _ := get(t, c, base+"/wp-admin/install")
	if !strings.Contains(body, "Select a default language") || !strings.Contains(body, `value="de_DE"`) || strings.Contains(body, `name="user_name"`) {
		t.Fatal("no language step on the install screen")
	}
	// Choosing German downloads the pack and renders the form in German.
	_, body, _ = get(t, c, base+"/wp-admin/install?language=de_DE")
	for _, want := range []string{`lang="de-DE"`, "Titel der Website", "Benutzername", `name="language" value="de_DE"`} {
		if !strings.Contains(body, want) {
			t.Errorf("install form: missing %s", want)
		}
	}
	if !srv.lang.IsInstalled("de_DE") {
		t.Fatal("pack not downloaded")
	}
	// The install saves it as the site language.
	if code, _, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{"weblog_title": {"Sprachtest"}, "user_name": {"admin"},
		"admin_password": {"Violet-Harbor-Lantern-93!"}, "admin_password2": {"Violet-Harbor-Lantern-93!"}, "admin_email": {"admin@example.test"}, "language": {"de_DE"}}); code != http.StatusOK {
		t.Fatalf("install: %d", code)
	}
	if srv.siteLocale() != "de_DE" {
		t.Fatalf("site language %q", srv.siteLocale())
	}
	// The login screen has the dropdown and follows the choice (and remembers it).
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	_, body, _ = get(t, anon, base+"/wp-login")
	for _, want := range []string{`id="language-switcher-locales"`, `value="de_DE"`, `value="en_US"`, `lang="de-DE"`} {
		if !strings.Contains(body, want) {
			t.Errorf("login: missing %s", want)
		}
	}
	resp, err := anon.Get(base + "/wp-login?wp_lang=en_US")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `lang="en-US"`) {
		t.Error("English not applied on the login screen")
	}
	remembered := false
	for _, ck := range resp.Cookies() {
		remembered = remembered || ck.Name == "wp_lang" && ck.Value == "en_US"
	}
	if !remembered {
		t.Error("the choice is not remembered in the wp_lang cookie")
	}
	// An unknown language is ignored.
	_, body, _ = get(t, anon, base+"/wp-login?wp_lang=xx_XX")
	if !strings.Contains(body, `lang="de-DE"`) {
		t.Error("an unknown wp_lang changed the language")
	}
}
