package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// gooThemeStepPost runs one step of the Store Themes screen and decodes its answer.
func gooThemeStepPost(t *testing.T, c *http.Client, base, nonce, theme, step string) gooThemeStep {
	t.Helper()
	code, body, _ := post(t, c, base+"/wp-admin/goocommerce?page=themes", url.Values{"_wpnonce": {nonce}, "action": {"step"},
		"theme": {theme}, "step": {step}})
	var res gooThemeStep
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("step %s: %d %s", step, code, body)
	}
	if code != http.StatusOK || !res.OK {
		t.Fatalf("step %s: %d %+v", step, code, res)
	}
	return res
}

func TestGooStoreThemesScreen(t *testing.T) {
	base, c, srv := gooSite(t)
	code, body, _ := get(t, c, base+"/wp-admin/goocommerce?page=themes")
	if code != http.StatusOK {
		t.Fatalf("Store Themes: %d", code)
	}
	for _, want := range []string{`id="goo-theme-threadline"`, `id="goo-theme-voltage-store"`, "Install &amp; set up store", "Live preview",
		`href="/wp-admin/goocommerce?page=themes"`} {
		if !strings.Contains(body, want) {
			t.Errorf("Store Themes screen lacks %q", want)
		}
	}
	if strings.Contains(body, `id="goo-theme-inkwell"`) {
		t.Error("a theme without store support is listed")
	}
	// Home: the task and the hint point at the screen while the theme is not a store theme.
	_, home, _ := get(t, c, base+"/wp-admin/goocommerce")
	if !strings.Contains(home, "Choose a store theme") || !strings.Contains(home, "Pick a store theme") {
		t.Error("Home does not offer the store themes")
	}
	nonce := formNonce(t, body)

	// A forged nonce and an unknown theme are refused.
	if code, _, _ := post(t, c, base+"/wp-admin/goocommerce?page=themes", url.Values{"_wpnonce": {"bad"}, "action": {"step"}, "theme": {"threadline"}, "step": {"install"}}); code == http.StatusOK {
		t.Error("a bad nonce was accepted")
	}
	if code, _, _ := post(t, c, base+"/wp-admin/goocommerce?page=themes", url.Values{"_wpnonce": {nonce}, "action": {"step"}, "theme": {"inkwell"}, "step": {"install"}}); code != http.StatusBadRequest {
		t.Errorf("a non-store theme: %d, want 400", code)
	}

	steps := []string{}
	for _, step := range gooThemeSteps {
		res := gooThemeStepPost(t, c, base, nonce, "threadline", step)
		steps = append(steps, res.Step)
		if step == "demo" && (!res.Done || res.ShopURL == "" || !strings.Contains(res.Message, "Threadline")) {
			t.Errorf("demo step answer: %+v", res)
		}
	}
	if a := srv.activeTheme(); a == nil || a.Slug != "threadline" {
		t.Fatalf("active theme %v, want threadline", a)
	}
	if !srv.demoImported("threadline") {
		t.Fatal("the demo store is not imported")
	}
	var products int
	_ = srv.store.Pool.QueryRow(t.Context(), `SELECT count(*) FROM posts WHERE type = 'product' AND status = 'publish'`).Scan(&products)
	if products < 12 {
		t.Errorf("%d demo products, want 12", products)
	}
	// The installed theme is an ordinary theme of Appearance › Themes.
	_, themes, _ := get(t, c, base+"/wp-admin/themes")
	if !strings.Contains(themes, "Threadline") {
		t.Error("Appearance › Themes does not list the store theme")
	}
	// The card now offers the store; running the steps again changes nothing.
	_, body, _ = get(t, c, base+"/wp-admin/goocommerce?page=themes")
	card := body[strings.Index(body, `id="goo-theme-threadline"`):]
	card = card[:strings.Index(card, `class="goo-theme-card`)+1]
	if !strings.Contains(card, "Visit store") {
		t.Error("the active store theme does not offer its store")
	}
	if res := gooThemeStepPost(t, c, base, nonce, "threadline", "install"); !strings.Contains(res.Message, "already installed") {
		t.Errorf("second install: %+v", res)
	}
	_, home, _ = get(t, c, base+"/wp-admin/goocommerce")
	if strings.Contains(home, "Pick a store theme") {
		t.Error("Home still asks for a store theme")
	}
	// The shop renders with the theme.
	if code, shop, _ := get(t, c, base+"/shop/"); code != http.StatusOK || !strings.Contains(shop, "Linen Wrap Dress") {
		t.Errorf("shop: %d", code)
	}
}

func TestGooStoreThemesNoJSSetup(t *testing.T) {
	base, c, srv := gooSite(t)
	_, body, _ := get(t, c, base+"/wp-admin/goocommerce?page=themes")
	nonce := formNonce(t, body)
	code, _, loc := post(t, c, base+"/wp-admin/goocommerce?page=themes", url.Values{"_wpnonce": {nonce}, "action": {"setup"}, "theme": {"roast-and-ritual"}})
	if code != http.StatusFound || !strings.Contains(loc, "result=done") {
		t.Fatalf("setup: %d %s", code, loc)
	}
	if a := srv.activeTheme(); a == nil || a.Slug != "roast-and-ritual" || !srv.demoImported("roast-and-ritual") {
		t.Fatalf("setup did not activate and import roast-and-ritual")
	}
	_, done, _ := get(t, c, base+loc)
	if !strings.Contains(done, "Your store is ready") || !strings.Contains(done, "Visit your store") {
		t.Error("the result notice is missing")
	}
}

// A library that cannot be read is said so on the screen, and an installed store package that is
// not in the library is still listed.
func TestGooStoreThemesLibraryOffAndInstalledOnly(t *testing.T) {
	base, c, srv := gooSite(t)
	srv.themeLibrary = nil // no directory source; the test server has no Manager either
	th := &Theme{Slug: "zipstore", Name: "Zip Store", Version: "1.0.0",
		Package: &themePackage{Manifest: &themeManifest{Slug: "zipstore", Name: "Zip Store", Version: "1.0.0",
			Supports: map[string]any{"woocommerce": true}}}}
	registerTheme(th)
	t.Cleanup(func() {
		themesMu.Lock()
		delete(themes, th.Slug)
		themesMu.Unlock()
	})
	_, body, _ := get(t, c, base+"/wp-admin/goocommerce?page=themes")
	if !strings.Contains(body, "The theme library is not configured") {
		t.Error("no notice that the library is off")
	}
	if !strings.Contains(body, `id="goo-theme-zipstore"`) {
		t.Error("an installed store theme outside the library is not listed")
	}
}
