package server

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// A footer script that a header script depends on moves to the header (WP_Dependencies gives a
// dependency the lowest group of the scripts that need it); footer scripts nobody in the header
// needs stay in the footer.
func TestScriptHeadGroupTakesFooterDeps(t *testing.T) {
	deps := func(h string) ([]string, bool) {
		r, ok := registry.Scripts[h]
		if !ok {
			return nil, false
		}
		return r.Deps, true
	}
	if r := registry.Scripts["nav-menu"]; r == nil || r.Footer {
		t.Fatal("nav-menu is expected to be a header script")
	}
	if r := registry.Scripts["wp-i18n"]; r == nil || !r.Footer {
		t.Fatal("wp-i18n is expected to be a footer script")
	}
	order := resolve([]string{"nav-menu", "wp-api-fetch"}, deps)
	head := headScripts(order, deps)
	for _, h := range []string{"nav-menu", "wp-i18n", "wp-hooks", "underscore", "jquery"} {
		if !head[h] {
			t.Errorf("%s is not in the header group", h)
		}
	}
	if head["wp-api-fetch"] {
		t.Error("wp-api-fetch (footer, needed by no header script) moved to the header")
	}
}

// TestNavMenusTranslatedHeadScripts is the "wp is not defined" bug on a translated site: nav-menu
// prints in <head> with its wp.i18n.setLocaleData() translations, so wp-i18n (a footer script)
// must print in <head> before it, and only once on the page.
func TestNavMenusTranslatedHeadScripts(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installUsersSite(t, c, base)

	sum := md5.Sum([]byte("wp-admin/js/nav-menu.js"))
	var pack bytes.Buffer
	zw := zip.NewWriter(&pack)
	f, _ := zw.Create("admin-tr_TR.mo")
	f.Write(i18nMO(map[string]string{"": "Plural-Forms: nplurals=2; plural=(n > 1);\n", "Menus": "Menüler"}))
	f, _ = zw.Create("tr_TR-" + hex.EncodeToString(sum[:]) + ".json")
	f.Write([]byte(`{"locale_data":{"messages":{"":{"domain":"messages"},"Menu Item":["Menü öğesi"]}}}`))
	zw.Close()
	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/core/1.0/":
			fmt.Fprintf(w, `{"translations":[{"language":"tr_TR","package":"%s/tr.zip"}]}`, api.URL)
		case "/tr.zip":
			w.Write(pack.Bytes())
		default:
			fmt.Fprint(w, `{"translations":[]}`)
		}
	}))
	defer api.Close()
	srv.cfg.TranslationsAPI = api.URL
	_, body, _ := get(t, c, base+"/wp-admin/options-general")
	form := url.Values{"option_page": {"general"}, "_wpnonce": {formNonce(t, body)}, "blogname": {"Menüler"}, "blogdescription": {""},
		"admin_email": {"admin@example.test"}, "timezone_string": {"UTC"}, "date_format": {"F j, Y"}, "time_format": {"g:i a"},
		"start_of_week": {"1"}, "WPLANG": {"tr_TR"}}
	post(t, c, base+"/wp-admin/options", form)
	if srv.siteLocale() != "tr_TR" {
		t.Fatalf("site locale %s", srv.siteLocale())
	}

	_, body, _ = get(t, c, base+"/wp-admin/nav-menus")
	head, _, ok := strings.Cut(body, "</head>")
	if !ok {
		t.Fatal("no </head>")
	}
	tr := strings.Index(head, `id="nav-menu-js-translations"`)
	if tr < 0 || !strings.Contains(head, "Menü öğesi") {
		t.Fatal("nav-menu's translations are not printed in <head>")
	}
	for _, h := range []string{"wp-hooks", "wp-i18n", "underscore"} {
		id := `id="` + h + `-js"`
		if n := strings.Count(body, id); n != 1 {
			t.Errorf("%s printed %d times", h, n)
		}
		if i := strings.Index(head, id); i < 0 || i > tr {
			t.Errorf("%s is not printed in <head> before nav-menu's translations", h)
		}
	}
}
