package server

import (
	"archive/zip"
	"bytes"
	"context"
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
		if os.Getenv("WPGO_I18N_MISSES_REAL") != "" { // the plugin packs download in the background: wait for them
			if err := srv.lang.Install(context.Background(), srv.langInstaller(), loc, langPluginPacks); err != nil {
				t.Logf("%s: language packs: %v", loc, err)
			}
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

// TestStoreFrontUntranslated lists the store front texts (shop, product, cart, checkout, order
// received, My account) without a translation in the locales of WPGO_I18N_MISSES, with the real
// WordPress.org packs (core and WooCommerce): what is left is Pursae Commerce's own text.
// WPGO_I18N_MISSES_OUT gets one store-<locale>.txt per locale; WPGO_I18N_MISSES_DUMP is a directory
// for the pages themselves (<locale>-NN.html).
func TestStoreFrontUntranslated(t *testing.T) {
	locales := strings.Split(os.Getenv("WPGO_I18N_MISSES"), ",")
	if locales[0] == "" {
		t.Skip("WPGO_I18N_MISSES not set")
	}
	base, admin, srv := gooSite(t)
	ctx := context.Background()
	f := gooFrontCatalog(t, srv)
	_ = srv.gooSetOpts(ctx, map[string]any{"goo_test_settings": map[string]any{"enabled": "yes"}, "default_country": "US:CA"})
	z := &gooZone{Name: "US", Locations: []gooZoneLocation{{Code: "US", Type: "country"}}}
	_ = srv.gooSaveZone(ctx, z)
	_, _ = srv.gooAddMethod(ctx, z.ID, "flat_rate", map[string]any{"cost": "5"})
	_, _ = srv.gooAddMethod(ctx, z.ID, "free_shipping", map[string]any{})
	_ = srv.gooSaveCoupon(ctx, &gooCoupon{Code: "save5", DiscountType: "fixed_cart", Amount: 5})

	var mu sync.Mutex
	var misses map[string]bool
	i18n.MissHook = func(s string) { mu.Lock(); misses[s] = true; mu.Unlock() }
	defer func() { i18n.MissHook = nil }()

	for _, loc := range locales {
		if err := srv.settings.Set(ctx, map[string]any{"WPLANG": loc}); err != nil {
			t.Fatal(err)
		}
		if err := srv.lang.Install(ctx, srv.langInstaller(), loc, langPluginPacks); err != nil {
			t.Logf("%s: language packs: %v", loc, err)
		}
		mu.Lock()
		misses = map[string]bool{}
		mu.Unlock()
		v := gooFrontClient()
		n := 0
		get := func(t *testing.T, c *http.Client, u string) (int, string, string) {
			code, body, where := get(t, c, u)
			if dir := os.Getenv("WPGO_I18N_MISSES_DUMP"); dir != "" && code == http.StatusOK {
				n++
				os.WriteFile(fmt.Sprintf("%s/%s-%02d.html", dir, loc, n), []byte("<!-- "+u+" -->\n"+body), 0o644)
			}
			return code, body, where
		}
		pages := []string{"/shop/", "/product/shirt/", "/product/hoodie/", "/product/breakfast-set/", "/product/partner-lamp/", "/product-category/clothing/",
			"/?s=shirt&post_type=product", "/my-account/", "/my-account/lost-password/", "/cart/"}
		for _, p := range pages {
			get(t, v, base+p)
		}
		get(t, v, base+"/?add-to-cart="+itoa64(f.mug.ID))
		get(t, v, base+"/?add-to-cart="+itoa64(f.shirt.ID))
		get(t, v, base+"/cart/")
		_, body, _ := get(t, v, base+"/checkout/")
		if strings.Contains(body, "woocommerce-process-checkout-nonce") {
			bad := gooFrontCheckoutForm(gooHidden(t, body, "woocommerce-process-checkout-nonce"))
			bad.Del("billing_first_name")
			bad.Set("billing_email", "x")
			post(t, v, base+"/checkout/", bad)
			_, body, _ = get(t, v, base+"/checkout/")
			_, _, rec := post(t, v, base+"/checkout/", gooFrontCheckoutForm(gooHidden(t, body, "woocommerce-process-checkout-nonce")))
			if rec != "" {
				get(t, v, rec)
			}
		}
		for _, p := range []string{"/shop/", "/my-account/", "/my-account/orders/", "/my-account/edit-address/", "/my-account/edit-address/billing/", "/my-account/edit-account/", "/my-account/downloads/", "/cart/", "/checkout/"} {
			get(t, admin, base+p)
		}
		mu.Lock()
		var list []string
		for s := range misses {
			list = append(list, s)
		}
		mu.Unlock()
		sort.Strings(list)
		t.Logf("%s: %d untranslated store texts", loc, len(list))
		if dir := os.Getenv("WPGO_I18N_MISSES_OUT"); dir != "" {
			os.WriteFile(dir+"/store-"+loc+".txt", []byte(strings.Join(list, "\n")+"\n"), 0o644)
		}
	}
}
