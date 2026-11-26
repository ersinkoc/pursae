package server

import (
	"html"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// copyThemeTree copies a theme directory (the starter example) so a test can edit it.
func copyThemeTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	return dst
}

// TestThemeTemplatesReadOnlyPublicSettings: a theme package whose template asks for the site's
// secrets (auth_secret, the license key, captcha and Stripe secrets, the admin e-mail) renders them
// empty for a visitor, while the public settings and the active theme's mods still render, and the
// core admin screens keep reading private settings.
func TestThemeTemplatesReadOnlyPublicSettings(t *testing.T) {
	t.Setenv("PURSAE_THEMES_DIR", t.TempDir())
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Packages"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK {
		t.Fatalf("install: %d %s", code, body)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	if err := srv.settings.Set(t.Context(), map[string]any{
		"pursae_license_key":          "LIC-REVIEW-123",
		"gontact_recaptcha_secret":    "RECAPTCHA-SECRET-REVIEW",
		"woocommerce_stripe_settings": map[string]any{"secret_key": "sk_live_REVIEW"},
		"posts_per_page":              7,
		"theme_mods_other":            map[string]any{"accent": "#654321"},
	}); err != nil {
		t.Fatal(err)
	}
	secret := srv.siteSecret()
	if len(secret) < 16 {
		t.Fatalf("auth_secret %q", secret)
	}

	// The starter theme with one block that asks for secrets and public settings.
	src := copyThemeTree(t, starterDir)
	idx := filepath.Join(src, "templates", "index.html")
	b, err := os.ReadFile(idx)
	if err != nil {
		t.Fatal(err)
	}
	probe := `<pre id="probe">S={{settingStr "auth_secret"}}|L={{settingStr "pursae_license_key"}}|R={{settingStr "gontact_recaptcha_secret"}}` +
		`|W={{json (setting "woocommerce_stripe_settings")}}|E={{settingStr "admin_email"}}|B={{settingBool "pursae_license_key"}}` +
		`|I={{settingInt "auth_secret"}}|D={{settingID "auth_secret"}}|O={{json (setting "theme_mods_other")}}` +
		`|name={{settingStr "blogname"}}|ppp={{settingInt "posts_per_page"}}|front={{settingStr "show_on_front"}}` +
		`|fmt={{settingStr "date_format"}}|mods={{json (setting "theme_mods_starter")}}</pre>`
	nb := strings.Replace(string(b), `{{template "footer" .}}`, probe+"\n"+`{{template "footer" .}}`, 1)
	if nb == string(b) {
		t.Fatal("template edit failed")
	}
	if err := os.WriteFile(idx, []byte(nb), 0o644); err != nil {
		t.Fatal(err)
	}
	_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
	if code, _, loc := postZip(t, c, base+"/wp-admin/theme-install", formNonce(t, body), "themezip", "starter.zip", zipDir(t, src)); code != http.StatusFound || strings.Contains(loc, "error=") {
		t.Fatalf("upload: %d %s", code, loc)
	}
	t.Cleanup(func() { unregisterTheme("starter") })
	_, body, _ = get(t, c, base+"/wp-admin/themes")
	m := regexp.MustCompile(`href="(/wp-admin/themes\?action=activate&amp;stylesheet=starter&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no activate link")
	}
	get(t, c, base+html.UnescapeString(m[1]))
	if srv.settings.String("stylesheet") != "starter" {
		t.Fatalf("active theme %q", srv.settings.String("stylesheet"))
	}
	if err := srv.settings.Set(t.Context(), map[string]any{"theme_mods_starter": map[string]any{"accent": "#123456"}}); err != nil {
		t.Fatal(err)
	}

	jar, _ := cookiejar.New(nil)
	code, page, _ := get(t, &http.Client{Jar: jar}, base+"/")
	i := strings.Index(page, `<pre id="probe">`)
	if code != http.StatusOK || i < 0 {
		t.Fatalf("front page %d lacks the probe:\n%s", code, page)
	}
	frag := html.UnescapeString(page[i : i+strings.Index(page[i:], "</pre>")])
	for _, leak := range []string{secret, "LIC-REVIEW-123", "RECAPTCHA-SECRET-REVIEW", "sk_live_REVIEW", "admin@example.test", "#654321"} {
		if strings.Contains(frag, leak) {
			t.Errorf("a theme template printed a private setting %q", strings.ReplaceAll(leak, secret, "<auth_secret>"))
		}
	}
	for _, want := range []string{"S=|L=|R=|W=null|E=|B=false|I=0|D=0|O=null|", "|name=Packages|", "|ppp=7|", "|front=posts|", `|mods={"accent":"#123456"}`} {
		if !strings.Contains(frag, want) {
			t.Errorf("probe lacks %q:\n%s", want, strings.ReplaceAll(frag, secret, "<auth_secret>"))
		}
	}
	if !regexp.MustCompile(`\|fmt=[^|]+\|`).MatchString(frag) {
		t.Errorf("date_format did not render: %s", frag)
	}

	// The other template sets: themes and ACF blocks (funcs), the plugins' own templates (coreFuncs).
	for name, fm := range map[string]map[string]any{"funcs": srv.tmpl.funcs(), "coreFuncs": srv.tmpl.coreFuncs(srv)} {
		if v := fm["settingStr"].(func(string) string)("auth_secret"); v != "" {
			t.Errorf("%s: settingStr auth_secret is readable", name)
		}
		if v := fm["setting"].(func(string) any)("woocommerce_stripe_settings"); v != nil {
			t.Errorf("%s: setting woocommerce_stripe_settings = %v", name, v)
		}
		if v := fm["settingStr"].(func(string) string)("blogname"); v != "Packages" {
			t.Errorf("%s: settingStr blogname = %q", name, v)
		}
	}
	// The core admin screens still read private settings.
	if v := srv.tmpl.adminFuncs()["settingStr"].(func(string) string)("admin_email"); v != "admin@example.test" {
		t.Errorf("admin settingStr admin_email = %q", v)
	}
	if _, body, _ := get(t, c, base+"/wp-admin/options-general"); !strings.Contains(body, `value="admin@example.test"`) {
		t.Error("Settings › General lost the administration e-mail")
	}
}

// TestThemeManifestRefusesRemoteScripts: a package's scripts must be its own files (they are printed
// with the response's CSP nonce); styles may still name a remote stylesheet.
func TestThemeManifestRefusesRemoteScripts(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(starterDir, "theme.json"))
	if err != nil {
		t.Fatal(err)
	}
	local := `{"handle": "starter-navigation", "src": "assets/js/navigation.js", "deps": [], "in_footer": true}`
	if !strings.Contains(string(b), local) {
		t.Fatal("starter manifest changed")
	}
	if _, err := parseThemeManifest(b); err != nil {
		t.Fatalf("starter manifest: %v", err)
	}
	for _, src := range []string{"https://attacker.example/x.js", "http://attacker.example/x.js", "//attacker.example/x.js", "/wp-content/uploads/x.js", "../x.js"} {
		m := strings.Replace(string(b), local, local+`, {"handle": "remote", "src": "`+src+`"}`, 1)
		_, err := parseThemeManifest([]byte(m))
		if err == nil || !strings.Contains(err.Error(), "scripts[1].src") {
			t.Errorf("script %s: %v", src, err)
		}
	}
	m := strings.Replace(string(b), `"styles": [`, `"styles": [{"handle": "remote-font", "src": "https://fonts.example.com/inter.css"}, `, 1)
	if m == string(b) {
		t.Fatal("styles edit failed")
	}
	if _, err := parseThemeManifest([]byte(m)); err != nil {
		t.Errorf("a remote stylesheet is refused: %v", err)
	}

	// A script that reached the package another way is never printed.
	pkg := &themePackage{}
	th := &Theme{Slug: "starter", Version: "1.0.0"}
	if tag := pkg.scriptTag(th, themeAsset{Handle: "remote", Src: "https://attacker.example/x.js"}); tag != "" {
		t.Errorf("remote script printed: %s", tag)
	}
	if tag := pkg.scriptTag(th, themeAsset{Handle: "nav", Src: "assets/js/navigation.js"}); !strings.Contains(tag, `src="/wp-content/themes/starter/js/navigation.js?ver=1.0.0"`) {
		t.Errorf("package script: %s", tag)
	}
}

// TestThemeUploadRefusesRemoteScript: the upload of a package with a remote script fails.
func TestThemeUploadRefusesRemoteScript(t *testing.T) {
	t.Setenv("PURSAE_THEMES_DIR", t.TempDir())
	ts, c, _ := testServerBuilderOff(t)
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Packages"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK {
		t.Fatalf("install: %d %s", code, body)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	src := copyThemeTree(t, starterDir)
	editFile(t, filepath.Join(src, "theme.json"), map[string]string{
		`{"handle": "starter-navigation", "src": "assets/js/navigation.js", "deps": [], "in_footer": true}`: `{"handle": "starter-navigation", "src": "assets/js/navigation.js", "deps": [], "in_footer": true}, {"handle": "remote", "src": "https://attacker.example/x.js", "deps": [], "in_footer": false}`,
	})
	t.Cleanup(func() { unregisterTheme("starter") })
	_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
	_, _, loc := postZip(t, c, base+"/wp-admin/theme-install", formNonce(t, body), "themezip", "starter.zip", zipDir(t, src))
	if !strings.Contains(loc, "error=") || !strings.Contains(loc, "remote") {
		t.Fatalf("upload with a remote script: %s", loc)
	}
	if themeBySlug("starter") != nil {
		t.Fatal("the package was installed")
	}
}
