package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestCustomizeColorScheme checks Twenty Twenty-Five's color schemes: the default palette prints no
// CSS, a style variation only what differs from it.
func TestCustomizeColorScheme(t *testing.T) {
	s := &Server{web: os.DirFS("../../web")}
	if got := s.tt5SchemeCSS(map[string]any{}); got != "" {
		t.Errorf("default scheme prints CSS: %.100s", got)
	}
	if got := s.tt5SchemeCSS(map[string]any{"color_scheme": "no-such-scheme"}); got != "" {
		t.Errorf("unknown scheme prints CSS: %.100s", got)
	}
	css := s.tt5SchemeCSS(map[string]any{"color_scheme": "evening"})
	for _, want := range []string{":root{", "--wp--preset--color--base:#1B1B1B", "--wp--preset--color--contrast:#F0F0F0"} {
		if !strings.Contains(css, want) {
			t.Errorf("evening scheme misses %q", want)
		}
	}
	if strings.Contains(css, "--wp--preset--font-size--small") {
		t.Error("evening scheme repeats properties that do not change")
	}
	v := &siteView{Theme: themeBySlug("twentytwentyfive"), ThemeMods: map[string]any{"background_color": "000000"}}
	if customBackground(v) != "" {
		t.Error("Twenty Twenty-Five has no custom background")
	}
}

var (
	czNonceRe = regexp.MustCompile(`nonce = "([^"]+)"`)
	czUUIDRe  = regexp.MustCompile(`uuid = "([0-9a-f]+)"`)
)

func TestCustomize(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t) // with the Web Builder on its site settings replace the Customizer
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Test Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK || !strings.Contains(body, "Success!") {
		t.Fatalf("install failed: %d", code)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login failed: %d", code)
	}

	// The screen.
	code, body, _ := get(t, c, base+"/wp-admin/customize")
	if code != http.StatusOK || strings.Contains(body, "Template error") {
		t.Fatalf("customize: %d %.300s", code, body)
	}
	for _, want := range []string{`id="customize-controls"`, "Site Identity", "Site Icon", "Color scheme", `value="evening"`, "Homepage Settings",
		"Additional CSS", `href="/wp-admin/nav-menus"`, `href="/wp-admin/widgets"`, "customize-controls.min.css", "color-picker.min.js", `id="tmpl-media-frame"`} {
		if !strings.Contains(body, want) {
			t.Errorf("customize screen misses %q", want)
		}
	}
	nonce, uuid := czNonceRe.FindStringSubmatch(body), czUUIDRe.FindStringSubmatch(body)
	if nonce == nil || uuid == nil {
		t.Fatal("no nonce or changeset id on the customize screen")
	}
	send := func(action string, values map[string]any) (int, string) {
		b, _ := json.Marshal(values)
		code, body, _ := post(t, c, base+"/wp-admin/customize", url.Values{"action": {action}, "uuid": {uuid[1]}, "_wpnonce": {nonce[1]}, "customized": {string(b)}})
		return code, body
	}
	if code, _ := send("publish", map[string]any{"mod:no_such_mod": "x"}); code != http.StatusBadRequest {
		t.Errorf("unknown setting accepted: %d", code)
	}
	if code, _, _ := post(t, c, base+"/wp-admin/customize", url.Values{"action": {"publish"}, "_wpnonce": {"bad"}, "customized": {`{"blogname":"x"}`}}); code != http.StatusForbidden {
		t.Errorf("bad nonce accepted: %d", code)
	}

	// Preview: unsaved values render in the preview only.
	if code, body := send("preview", map[string]any{"blogname": "Preview Title", "mod:color_scheme": "evening", "custom_css": "body { outline: 1px solid; }"}); code != http.StatusOK {
		t.Fatalf("preview: %d %s", code, body)
	}
	resp, err := c.Get(base + "/?" + czParam + "=" + uuid[1])
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("preview is cacheable: %q", cc)
	}
	_, body, _ = get(t, c, base+"/?"+czParam+"="+uuid[1])
	for _, want := range []string{"Preview Title", "twentytwentyfive-color-scheme-inline-css", `<style id="wp-custom-css">`, "body { outline: 1px solid; }", `id="wpgo-customize-preview"`} {
		if !strings.Contains(body, want) {
			t.Errorf("preview misses %q", want)
		}
	}
	if strings.Contains(body, `id="wpadminbar"`) {
		t.Error("preview shows the toolbar")
	}
	_, body, _ = get(t, c, base+"/")
	if strings.Contains(body, "Preview Title") || strings.Contains(body, "twentytwentyfive-color-scheme-inline-css") || strings.Contains(body, "wp-custom-css") {
		t.Error("preview values leaked to the live site")
	}
	jar, _ := cookiejar.New(nil)
	anon := &http.Client{Jar: jar}
	_, body, _ = get(t, anon, base+"/?"+czParam+"="+uuid[1])
	if strings.Contains(body, "Preview Title") {
		t.Error("anonymous visitor sees the changeset")
	}

	// Site icon: an image attachment.
	ctx := context.Background()
	db := getPool(t)
	var iconID int64
	if err := db.QueryRow(ctx, `INSERT INTO media (slug, file_path, mime_type, width, height, title) VALUES ('icon', '2026/10/icon.png', 'image/png', 512, 512, 'icon') RETURNING id`).Scan(&iconID); err != nil {
		t.Fatal(err)
	}

	// Publish.
	if code, body := send("publish", map[string]any{"blogname": "Published Title", "blogdescription": "Just <b>another</b> site", "mod:color_scheme": "evening",
		"custom_css": "body { color: red; }", "site_icon": iconID}); code != http.StatusOK || !strings.Contains(body, `"success":true`) {
		t.Fatalf("publish: %d %s", code, body)
	}
	_, body, _ = get(t, anon, base+"/")
	for _, want := range []string{">Published Title</a>", "Just another site", "<title>Published Title &#8211; Just another site</title>",
		"<style id='twentytwentyfive-color-scheme-inline-css'>", "--wp--preset--color--base:#1B1B1B",
		"<style id=\"wp-custom-css\">\n\t\t\tbody { color: red; }\t\t</style>",
		`<link rel="icon" href="` + base + `/wp-content/uploads/2026/10/icon.png" sizes="32x32" />`, `<link rel="apple-touch-icon"`} {
		if !strings.Contains(body, want) {
			t.Errorf("published front page misses %q", want)
		}
	}
	if i, j := strings.Index(body, "wp-custom-css"), strings.Index(body, "</head>"); i < 0 || i > j {
		t.Error("custom CSS is not in the head")
	}
	var mods []byte
	if err := db.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'theme_mods_twentytwentyfive'`).Scan(&mods); err != nil || !strings.Contains(string(mods), `"color_scheme": "evening"`) {
		t.Errorf("theme mods not stored: %s %v", mods, err)
	}

	// Homepage Settings: same page for both is rejected; a static front page previews and publishes.
	var pageID int64
	if err := db.QueryRow(ctx, `SELECT id FROM posts WHERE title = 'Sample Page'`).Scan(&pageID); err != nil {
		t.Fatal(err)
	}
	if code, _ := send("publish", map[string]any{"show_on_front": "page", "page_on_front": pageID, "page_for_posts": pageID}); code != http.StatusBadRequest {
		t.Errorf("same homepage and posts page accepted: %d", code)
	}
	if code, body := send("preview", map[string]any{"show_on_front": "page", "page_on_front": pageID}); code != http.StatusOK {
		t.Fatalf("preview homepage: %d %s", code, body)
	}
	_, body, _ = get(t, c, base+"/?"+czParam+"="+uuid[1])
	if !strings.Contains(body, "page-id-") || !strings.Contains(body, "Sample Page") {
		t.Error("homepage preview does not show the static page")
	}
	_, body, _ = get(t, anon, base+"/")
	if strings.Contains(body, "page-id-") {
		t.Error("homepage preview leaked")
	}
}
