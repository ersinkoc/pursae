package server

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// installAndLogin installs the site on a test server and logs the admin in.
func installAndLogin(t *testing.T, ts *httptest.Server, c *http.Client) {
	t.Helper()
	if code, body, _ := post(t, c, ts.URL+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Toggle Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK || !strings.Contains(body, "Success!") {
		t.Fatalf("install: %d", code)
	}
	if code, _, _ := post(t, c, ts.URL+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
}

// pluginLink finds the Activate or Deactivate link of Golementor on the Plugins screen.
func pluginLink(t *testing.T, c *http.Client, base, action string) string {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=` + action + `&amp;plugin=golementor[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s link for Golementor on the Plugins screen", action)
	}
	return html.UnescapeString(m[1])
}

func togglePlugin(t *testing.T, c *http.Client, base, action string) string {
	t.Helper()
	code, _, loc := get(t, c, base+pluginLink(t, c, base, action))
	if code != http.StatusFound || !strings.Contains(loc, action+"=true") {
		t.Fatalf("%s Golementor: %d %s", action, code, loc)
	}
	return loc
}

func restGet(t *testing.T, c *http.Client, base, path string) (int, string) {
	t.Helper()
	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	req, _ := http.NewRequest(http.MethodGet, base+path, nil)
	req.Header.Set("X-WP-Nonce", nonce)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// builderTraces are strings that only Golementor's output contains.
var builderTraces = []string{"elementor", "gowebeditor", "Golementor", "gwe-theme"}

func TestGolementorOffByDefault(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	if srv.golementorOn() {
		t.Fatal("Golementor is on after a fresh install")
	}

	// Plugins: listed, native and inactive.
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	row := regexp.MustCompile(`(?s)<tr class="(\w+)" data-slug="golementor"`).FindStringSubmatch(body)
	if row == nil || row[1] != "inactive" {
		t.Fatalf("Golementor is not listed as an inactive plugin: %v", row)
	}
	_, body, _ = get(t, c, base+"/wp-admin/plugin-install?tab=native")
	if !strings.Contains(body, "plugin-card-golementor") || strings.Contains(body, `aria-label="Golementor settings"`) {
		t.Error("the native plugin tab lacks the inactive Golementor card or shows its settings link")
	}

	// Admin: no menu entries, no screens.
	_, body, _ = get(t, c, base+"/wp-admin/")
	for _, bad := range []string{"menu-gowebeditor", "Theme Builder", "gowebeditor", "Custom Fonts", "Form Submissions"} {
		if strings.Contains(body, bad) {
			t.Errorf("the admin menu contains %q while Golementor is off", bad)
		}
	}
	for _, p := range []string{"/wp-admin/gowebeditor-theme-builder", "/wp-admin/gowebeditor-theme-builder?action=conditions&post=1", "/wp-admin/gowebeditor-pro?tab=popups",
		"/wp-admin/gowebeditor-pro?tab=settings", "/wp-admin/gowebeditor-icons", "/wp-content/plugins/gowebeditor/assets/js/editor.min.js",
		"/wp-content/gowebeditor/theme-screenshot/x.svg", "/wp-content/themes/hello-gowebeditor/style.css"} {
		if code, _, _ := get(t, c, base+p); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, code)
		}
	}
	for _, p := range []string{"/wp-json/elementor/v1/globals", "/wp-json/elementor/v1/site-navigation/recent-posts", "/wp-json/elementor-pro/v1/license/get-license-status"} {
		if code, b := restGet(t, c, base, p); code != http.StatusNotFound || !strings.Contains(b, "rest_no_route") {
			t.Errorf("%s: %d %.80s, want rest_no_route", p, code, b)
		}
	}
	if _, b := restGet(t, c, base, "/wp-json/"); strings.Contains(b, "elementor") {
		t.Error("the REST index lists Golementor's routes")
	}
	if code, b := restGet(t, c, base, "/wp-json/wp/v2/themes"); code != http.StatusOK || strings.Contains(b, "hello-gowebeditor") {
		t.Errorf("REST themes: %d, Hello Golementor listed: %v", code, strings.Contains(b, "hello-gowebeditor"))
	}
	if code, _ := restGet(t, c, base, "/wp-json/wp/v2/themes/hello-gowebeditor"); code != http.StatusNotFound {
		t.Errorf("REST theme Hello Golementor: %d, want 404", code)
	}

	// admin-ajax: the builder's actions are unknown, heartbeat still works.
	if code, _, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"elementor_ajax"}, "actions": {"{}"}}); code != http.StatusBadRequest {
		t.Errorf("elementor_ajax: %d, want 400", code)
	}
	if code, _, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"heartbeat"}}); code != http.StatusOK {
		t.Errorf("heartbeat: %d, want 200", code)
	}
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if code, _, loc := post(t, anon, base+"/wp-admin/admin-ajax", url.Values{"action": {"elementor_pro_forms_send_form"}}); code != http.StatusFound || !strings.Contains(loc, "wp-login") {
		t.Errorf("visitor form submit: %d %s, want the login redirect", code, loc)
	}

	// Editors: no Edit with Golementor, and the builder URL opens the normal editor.
	var pageID string
	if err := getPool(t).QueryRow(context.Background(), `SELECT id::text FROM posts WHERE type='page' AND title='Sample Page'`).Scan(&pageID); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/wp-admin/edit?post_type=page", "/wp-admin/post?post=" + pageID + "&action=edit", "/wp-admin/post?post=" + pageID + "&action=edit&classic-editor",
		"/wp-admin/post?post=" + pageID + "&action=golementor", "/wp-admin/edit?action=elementor_new_post&post_type=page&_wpnonce=x"} {
		code, body, _ := get(t, c, base+p)
		if code != http.StatusOK {
			t.Errorf("%s: %d", p, code)
		}
		for _, bad := range []string{"Edit with Golementor", "gowebeditor", "ElementorConfig"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s contains %q", p, bad)
			}
		}
	}

	// Themes: Hello Golementor is not listed, previewed or activatable.
	_, body, _ = get(t, c, base+"/wp-admin/themes")
	if strings.Contains(body, "hello-gowebeditor") || strings.Contains(body, "Hello Golementor") || strings.Contains(body, "Saved Golementor themes") {
		t.Error("the Themes screen shows Hello Golementor")
	}
	if err := srv.switchTheme(context.Background(), helloThemeSlug); err == nil {
		t.Error("Hello Golementor could be activated while Golementor is off")
	}
	if th := srv.installedTheme(helloThemeSlug); th != nil {
		t.Error("Hello Golementor is an installed theme while Golementor is off")
	}

	// Front end: nothing of the builder, for visitors and for the admin.
	for name, client := range map[string]*http.Client{"visitor": anon, "admin": c} {
		code, body, _ := get(t, client, base+"/")
		if code != http.StatusOK {
			t.Fatalf("%s home: %d", name, code)
		}
		for _, bad := range builderTraces {
			if strings.Contains(strings.ToLower(body), strings.ToLower(bad)) {
				t.Errorf("the %s's front page contains %q while Golementor is off", name, bad)
			}
		}
	}
}

func TestGolementorActivate(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	var pageID string
	if err := getPool(t).QueryRow(context.Background(), `SELECT id::text FROM posts WHERE type='page' AND title='Sample Page'`).Scan(&pageID); err != nil {
		t.Fatal(err)
	}

	togglePlugin(t, c, base, "activate")
	if !srv.golementorOn() {
		t.Fatal("the Activate link did not switch Golementor on")
	}
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	if !regexp.MustCompile(`<tr class="active" data-slug="golementor"`).MatchString(body) {
		t.Error("Golementor is not listed as active")
	}
	_, body, _ = get(t, c, base+"/wp-admin/")
	for _, want := range []string{"menu-gowebeditor", "Theme Builder", "Custom Fonts", "Form Submissions"} {
		if !strings.Contains(body, want) {
			t.Errorf("the admin menu misses %q", want)
		}
	}
	for _, p := range []string{"/wp-admin/gowebeditor-theme-builder", "/wp-admin/gowebeditor-pro?tab=popups", "/wp-admin/gowebeditor-icons",
		"/wp-content/plugins/gowebeditor/assets/js/editor.min.js", "/wp-content/themes/hello-gowebeditor/style.css"} {
		if code, _, _ := get(t, c, base+p); code != http.StatusOK {
			t.Errorf("%s: %d, want 200", p, code)
		}
	}
	if code, b := restGet(t, c, base, "/wp-json/elementor/v1/globals"); code != http.StatusOK || !strings.Contains(b, "colors") {
		t.Errorf("elementor/v1/globals: %d %.80s", code, b)
	}
	if code, b := restGet(t, c, base, "/wp-json/wp/v2/themes"); code != http.StatusOK || !strings.Contains(b, "hello-gowebeditor") {
		t.Errorf("REST themes lack Hello Golementor: %d", code)
	}
	_, body, _ = get(t, c, base+"/wp-admin/edit?post_type=page")
	if !strings.Contains(body, "/wp-admin/post?post="+pageID+"&amp;action=golementor") || !strings.Contains(body, "Edit with Golementor") {
		t.Error("the pages list has no Edit with Golementor action")
	}
	_, body, _ = get(t, c, base+"/wp-admin/post?post="+pageID+"&action=golementor")
	if !strings.Contains(body, "var ElementorConfig = {") {
		t.Error("the builder does not open")
	}
	_, body, _ = get(t, c, base+"/wp-admin/themes")
	if !strings.Contains(body, "hello-gowebeditor") {
		t.Error("the Themes screen lacks Hello Golementor")
	}
	if code, _, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"elementor_js_log"}}); code != http.StatusOK {
		t.Errorf("elementor_js_log: %d", code)
	}
	// The editor's upstream admin links (Theme Builder app, settings…) land on the native screens.
	for page, want := range map[string]string{"elementor-app&ver=4.3.5": "/wp-admin/gowebeditor-theme-builder",
		"elementor-settings": "/wp-admin/gowebeditor-pro?tab=settings", "e-form-submissions": "/wp-admin/gowebeditor-pro?tab=submissions"} {
		if code, _, loc := get(t, c, base+"/wp-admin/admin?page="+page); code != http.StatusFound || loc != want {
			t.Errorf("admin?page=%s: %d %q, want a redirect to %s", page, code, loc, want)
		}
	}
	if code, body, _ := get(t, c, base+"/wp-admin/admin?page=no-such-plugin"); code != http.StatusOK || !strings.Contains(body, "No such plugin") {
		t.Errorf("admin?page of a missing plugin: %d", code)
	}
}

func TestGolementorDeactivateFallsBackToDefaultTheme(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()

	// Activating moves the site to Hello Golementor, the blank canvas, and says so.
	if loc := togglePlugin(t, c, base, "activate"); !strings.Contains(loc, "gwe_theme=hello") {
		t.Errorf("activate redirect %q lacks the theme notice", loc)
	} else if _, body, _ := get(t, c, base+loc); !strings.Contains(body, "is now the active theme") {
		t.Error("the Plugins screen does not say Hello Golementor is active")
	}
	if got := srv.settings.String("stylesheet"); got != helloThemeSlug {
		t.Fatalf("stylesheet %q after activating, want Hello Golementor", got)
	}
	_ = ctx
	if _, body, _ := get(t, c, base+"/"); !strings.Contains(body, "wp-theme-"+helloThemeSlug) {
		t.Error("the site does not render with Hello Golementor while it is active")
	}

	loc := togglePlugin(t, c, base, "deactivate")
	if !strings.Contains(loc, "gwe_theme=reset") {
		t.Errorf("deactivate redirect %q lacks the theme notice", loc)
	}
	if got := srv.settings.String("stylesheet"); got != defaultTheme {
		t.Errorf("stylesheet %q after deactivating, want %s", got, defaultTheme)
	}
	_, body, _ := get(t, c, base+loc)
	if !strings.Contains(body, "Hello Golementor belongs to Golementor") || !strings.Contains(body, "Twenty Twenty-Five") {
		t.Error("the Plugins screen has no notice about the theme switch")
	}
	_, body, _ = get(t, c, base+"/")
	if !strings.Contains(body, "wp-theme-twentytwentyfive") || strings.Contains(strings.ToLower(body), "elementor") {
		t.Error("the site does not render with Twenty Twenty-Five after deactivating")
	}
	_, body, _ = get(t, c, base+"/wp-admin/themes")
	if strings.Contains(body, "hello-gowebeditor") {
		t.Error("the Themes screen still lists Hello Golementor")
	}

	// An old install that still has Hello Golementor as its stylesheet falls back without a write.
	if err := srv.settings.Set(ctx, map[string]any{"stylesheet": helloThemeSlug, "template": helloThemeSlug}); err != nil {
		t.Fatal(err)
	}
	if th := srv.activeTheme(); th.Slug != defaultTheme {
		t.Errorf("active theme %s while Golementor is off, want %s", th.Slug, defaultTheme)
	}
	if _, body, _ = get(t, c, base+"/"); !strings.Contains(body, "wp-theme-twentytwentyfive") {
		t.Error("a leftover Hello Golementor stylesheet does not fall back to the default theme")
	}
}

func TestGolementorBuiltPageWhileOff(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	togglePlugin(t, c, base, "activate")

	// A page built with Golementor: the element tree in meta, the plain text of it in the content.
	tree := `[{"id":"1a2b3c4","elType":"container","settings":{},"elements":[{"id":"5d6e7f8","elType":"widget","widgetType":"heading","settings":{"title":"Built with the builder"},"elements":[]}]}]`
	meta, _ := json.Marshal(map[string]any{"_elementor_edit_mode": "builder", "_elementor_template_type": "wp-page", "_elementor_data": tree})
	if _, err := getPool(t).Exec(ctx, `UPDATE posts SET meta = $1::jsonb, content = 'Built with the builder' WHERE type='page' AND slug='sample-page'`, string(meta)); err != nil {
		t.Fatal(err)
	}
	_, body, _ := get(t, c, base+"/sample-page/")
	if !strings.Contains(body, "elementor-widget-heading") || !strings.Contains(body, "elementor-kit-") {
		t.Fatalf("the built page does not render through the builder while it is on")
	}

	togglePlugin(t, c, base, "deactivate")
	for name, client := range map[string]*http.Client{"admin": c, "visitor": {CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}} {
		code, body, _ := get(t, client, base+"/sample-page/")
		if code != http.StatusOK {
			t.Fatalf("%s: built page %d while Golementor is off", name, code)
		}
		if !strings.Contains(body, "Built with the builder") {
			t.Errorf("%s: the built page shows no content while Golementor is off", name)
		}
		for _, bad := range []string{"elementor", "gowebeditor", "Golementor"} {
			if strings.Contains(strings.ToLower(body), strings.ToLower(bad)) {
				t.Errorf("%s: the built page contains %q while Golementor is off", name, bad)
			}
		}
	}
	// Nothing was deleted: switching it on again brings the builder page back.
	togglePlugin(t, c, base, "activate")
	if _, body, _ = get(t, c, base+"/sample-page/"); !strings.Contains(body, "elementor-widget-heading") {
		t.Error("the built page is not rendered by the builder after activating again")
	}
}

// An inactive native plugin adds nothing to the admin, like WordPress: no menu, no toolbar node,
// no screens, no Settings link. WPGo Cache starts active.
func TestInactiveNativePluginLeavesAdmin(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, body, _ := get(t, c, base+"/wp-admin/")
	if !strings.Contains(body, "toplevel_page_wpgo-cache") || !strings.Contains(body, "wp-admin-bar-wpgo-cache") {
		t.Fatal("an active WPGo Cache is missing from the menu or the toolbar")
	}
	_, body, _ = get(t, c, base+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=deactivate&amp;plugin=wpgo-cache[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Deactivate link for WPGo Cache")
	}
	if code, _, _ := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound {
		t.Fatalf("deactivate WPGo Cache: %d", code)
	}
	_, body, _ = get(t, c, base+"/wp-admin/")
	for _, gone := range []string{"toplevel_page_wpgo-cache", "wp-admin-bar-wpgo-cache", "menu-gowebeditor", "gowebeditor-theme-builder"} {
		if strings.Contains(body, gone) {
			t.Errorf("inactive plugin still in the admin: %s", gone)
		}
	}
	if code, _, _ := get(t, c, base+"/wp-admin/admin?page=wpgo-cache-settings"); code != http.StatusForbidden {
		t.Errorf("inactive WPGo Cache screen: %d, want 403", code)
	}
	_, body, _ = get(t, c, base+"/wp-admin/plugins")
	if strings.Contains(body, `href="/wp-admin/admin?page=wpgo-cache"`) {
		t.Error("the Plugins screen links to the settings of an inactive plugin")
	}
}

// Deactivating goes back to the theme that was active before Golementor, not always the default.
func TestGolementorRestoresPreviousTheme(t *testing.T) {
	t.Setenv("WPGO_THEMES_DIR", t.TempDir())
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	installLibraryTheme(t, c, base, "inkwell", true)
	togglePlugin(t, c, base, "activate")
	if got := srv.settings.String("stylesheet"); got != helloThemeSlug {
		t.Fatalf("stylesheet %q after activating, want Hello Golementor", got)
	}
	togglePlugin(t, c, base, "deactivate")
	if got := srv.settings.String("stylesheet"); got != "inkwell" {
		t.Errorf("stylesheet %q after deactivating, want inkwell back", got)
	}
}
