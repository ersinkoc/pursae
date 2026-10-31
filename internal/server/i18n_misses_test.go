package server

import (
	"archive/zip"
	"bytes"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"wpgo/internal/i18n"
)

// TestAdminUntranslated lists the admin texts that have no translation in the locales named by
// WPGO_I18N_MISSES (e.g. "tr_TR,sv_SE,he_IL"): it activates every native plugin, crawls the admin
// screens (links on the pages, two levels) and collects what the catalog reports through
// i18n.MissHook; WPGO_I18N_MISSES_PLUGINS limits which plugins are activated (WPGO_I18N_MISSES_REAL=1 downloads the real WordPress language packs, so only WordPress Go's own
// texts are left). WPGO_I18N_MISSES_OUT is a directory that gets one <locale>.txt per locale.
func TestAdminUntranslated(t *testing.T) {
	locales := strings.Split(os.Getenv("WPGO_I18N_MISSES"), ",")
	if locales[0] == "" {
		t.Skip("WPGO_I18N_MISSES not set")
	}
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	base := ts.URL

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/core/1.0/" {
			var parts []string
			for _, l := range locales {
				parts = append(parts, fmt.Sprintf(`{"language":"%s","package":"http://%s/%s.zip"}`, l, r.Host, l))
			}
			fmt.Fprintf(w, `{"translations":[%s]}`, strings.Join(parts, ","))
			return
		}
		var pack bytes.Buffer
		zw := zip.NewWriter(&pack)
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".zip")
		f, _ := zw.Create("admin-" + name + ".mo")
		f.Write(i18nMO(map[string]string{"": "Plural-Forms: nplurals=2; plural=n != 1;\n", "Settings": "x"}))
		zw.Close()
		w.Write(pack.Bytes())
	}))
	defer api.Close()
	if os.Getenv("WPGO_I18N_MISSES_REAL") == "" { // offline: an empty core pack, so WordPress' own texts count as untranslated too
		srv.cfg.TranslationsAPI = api.URL
	}

	// Activate every native plugin.
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	only := os.Getenv("WPGO_I18N_MISSES_PLUGINS") // e.g. "gocdn,headlessgo"; empty: all
	for _, m := range regexp.MustCompile(`href="(/wp-admin/plugins\?action=activate&amp;plugin=([^&"]*)[^"]*)"`).FindAllStringSubmatch(body, -1) {
		if only == "" || strings.Contains(","+only+",", ","+strings.Split(m[2], "/")[0]+",") {
			get(t, c, base+html.UnescapeString(m[1]))
		}
	}

	var mu sync.Mutex
	var misses map[string]bool
	i18n.MissHook = func(s string) { mu.Lock(); misses[s] = true; mu.Unlock() }
	defer func() { i18n.MissHook = nil }()

	skip := regexp.MustCompile(`action=|_wpnonce|logout|delete|trash|spam|customize|export|download|\.(css|js|png|svg)|wp-login|install`)
	href := regexp.MustCompile(`href="(/wp-admin/[^"#]*)"`)
	for _, loc := range locales {
		_, body, _ := get(t, c, base+"/wp-admin/options-general")
		form := url.Values{"option_page": {"general"}, "_wpnonce": {formNonce(t, body)}, "blogname": {"Site"}, "blogdescription": {""},
			"admin_email": {"admin@example.test"}, "timezone_string": {"UTC"}, "date_format": {"F j, Y"}, "time_format": {"g:i a"},
			"start_of_week": {"1"}, "WPLANG": {loc}}
		post(t, c, base+"/wp-admin/options", form)
		if srv.siteLocale() != loc {
			t.Fatalf("locale %s not set (%s)", loc, srv.siteLocale())
		}
		mu.Lock()
		misses = map[string]bool{}
		mu.Unlock()
		seen := map[string]bool{}
		queue := []string{"/wp-admin/"}
		for depth := 0; depth < 2; depth++ {
			var next []string
			for _, p := range queue {
				if seen[p] || len(seen) > 600 {
					continue
				}
				seen[p] = true
				_, page, _ := get(t, c, base+p)
				for _, m := range href.FindAllStringSubmatch(page, -1) {
					q := html.UnescapeString(m[1])
					if !skip.MatchString(q) && !seen[q] {
						next = append(next, q)
					}
				}
			}
			queue = next
		}
		mu.Lock()
		var list []string
		for s := range misses {
			list = append(list, s)
		}
		mu.Unlock()
		sort.Strings(list)
		t.Logf("%s: %d pages, %d untranslated texts", loc, len(seen), len(list))
		if dir := os.Getenv("WPGO_I18N_MISSES_OUT"); dir != "" {
			os.WriteFile(dir+"/"+loc+".txt", []byte(strings.Join(list, "\n")+"\n"), 0o644)
		}
	}
}
