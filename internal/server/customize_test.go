package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
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

	// The changeset is persisted (transients), not process memory, and Save Draft keeps it for 30 days.
	var n int
	if err := getPool(t).QueryRow(context.Background(), `SELECT count(*) FROM transients WHERE key = $1`, "customize_changeset:"+uuid[1]).Scan(&n); err != nil || n != 1 {
		t.Errorf("changeset not stored in transients: n=%d err=%v", n, err)
	}
	if code, body := send("draft", map[string]any{"blogname": "Draft Title"}); code != http.StatusOK || !strings.Contains(body, `"status":"draft"`) {
		t.Errorf("draft: %d %s", code, body)
	}
	var ttl float64
	_ = getPool(t).QueryRow(context.Background(), `SELECT extract(epoch FROM expires_at - now()) FROM transients WHERE key = $1`, "customize_changeset:"+uuid[1]).Scan(&ttl)
	if ttl < 20*24*3600 {
		t.Errorf("draft TTL too short: %.0fs", ttl)
	}
	if _, body, _ = get(t, c, base+"/?"+czParam+"="+uuid[1]); !strings.Contains(body, "Draft Title") {
		t.Error("draft changeset not previewable")
	}
	if code, _ := send("preview", map[string]any{"blogname": "Preview Title"}); code != http.StatusOK {
		t.Errorf("preview after draft: %d", code)
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

func TestShowAvatarsAndDefaultFormat(t *testing.T) {
	if !czUUIDValid.MatchString("0123456789abcdef0123456789abcdef") || czUUIDValid.MatchString("../x") {
		t.Error("changeset id validation")
	}
	ts, c, s := testServerBuilderOff(t)
	base := ts.URL
	if code, _, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Test Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK {
		t.Fatalf("install failed: %d", code)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login failed: %d", code)
	}
	ctx := context.Background()
	if got := s.avatarImg(ctx, "a@example.test", 32); !strings.Contains(got, "<img") {
		t.Errorf("avatar missing with Show Avatars on: %q", got)
	}
	if err := s.settings.Set(ctx, map[string]any{"show_avatars": false, "default_post_format": "aside"}); err != nil {
		t.Fatal(err)
	}
	if got := s.avatarImg(ctx, "a@example.test", 32); got != "" {
		t.Errorf("avatar shown with Show Avatars off: %q", got)
	}
	get(t, c, base+"/wp-admin/post-new")
	var format string
	if err := getPool(t).QueryRow(ctx, `SELECT format FROM posts WHERE status = 'auto-draft' ORDER BY id DESC LIMIT 1`).Scan(&format); err != nil || format != "aside" {
		t.Errorf("new post format = %q (err %v), want aside", format, err)
	}
}

// A changeset that expired, or belongs to another user, cannot be previewed or saved as a draft.
func TestCustomizeForeignAndExpiredChangeset(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, body, _ := get(t, c, base+"/wp-admin/customize")
	nonce := czNonceRe.FindStringSubmatch(body)
	if nonce == nil {
		t.Fatal("no nonce")
	}
	ctx := context.Background()
	theme := srv.activeTheme().Slug
	foreign, expired := newChangesetID(), newChangesetID()
	if err := srv.czSave(ctx, foreign, &czChangeset{UserID: 99999, Theme: theme, Mods: map[string]any{}, Options: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	cs := &czChangeset{UserID: 1, Theme: theme, Mods: map[string]any{}, Options: map[string]any{}, Expires: time.Now().Add(-time.Hour)}
	if err := srv.store.SetTransient(ctx, czKey(expired), cs, time.Hour); err != nil {
		t.Fatal(err)
	}
	for name, uuid := range map[string]string{"foreign": foreign, "expired": expired, "malformed": "../x"} {
		for _, action := range []string{"preview", "draft"} {
			code, _, _ := post(t, c, base+"/wp-admin/customize", url.Values{"action": {action}, "uuid": {uuid}, "_wpnonce": {nonce[1]}, "customized": {`{"blogname":"x"}`}})
			if code != http.StatusBadRequest {
				t.Errorf("%s changeset, action %s: %d, want 400", name, action, code)
			}
		}
	}
}

// TestCustomizeMenuLocationsAndDraftReopen: a menu location set in the Customizer is previewed from the
// changeset and published to menu_locations; ?changeset_uuid reopens the owner's draft with its values.
func TestCustomizeMenuLocationsAndDraftReopen(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	menuID, err := srv.store.CreateMenu(ctx, "Top")
	if err != nil {
		t.Fatal(err)
	}
	th := &Theme{Slug: "x", MenuLocations: []MenuLocation{{Slug: "primary", Name: "Primary"}}}
	opts, _, err := srv.czSanitize(ctx, th, map[string]string{"nav_menu_locations[primary]": strconv.FormatInt(menuID, 10)})
	if err != nil {
		t.Fatal(err)
	}
	if got := czMenuLocationsOf(opts)["primary"]; got != menuID {
		t.Fatalf("sanitized location = %d", got)
	}
	if _, _, err := srv.czSanitize(ctx, th, map[string]string{"nav_menu_locations[nowhere]": "1"}); err == nil {
		t.Error("unknown location accepted")
	}
	if _, _, err := srv.czSanitize(ctx, th, map[string]string{"nav_menu_locations[primary]": "999999"}); err == nil {
		t.Error("missing menu accepted")
	}
	// Preview override, read back from JSON (float64) like a stored changeset.
	cs := &czChangeset{Options: map[string]any{czMenuLocationsKey: map[string]any{"primary": float64(0)}}}
	_ = srv.store.SetMenuLocations(ctx, map[string]int64{"primary": menuID})
	if items, _ := srv.menuAt(ctx, cs, "primary"); items != nil {
		t.Error("preview did not unassign the location")
	}

	// Draft reopen.
	var uid int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&uid)
	active := srv.activeTheme()
	uuid := newChangesetID()
	if err := srv.czSave(ctx, uuid, &czChangeset{UserID: uid, Theme: active.Slug, Status: "draft",
		Options: map[string]any{"blogname": "Reopened Draft"}, Mods: srv.themeMods(active.Slug)}); err != nil {
		t.Fatal(err)
	}
	_, body, _ := get(t, c, ts.URL+"/wp-admin/customize?changeset_uuid="+uuid)
	if !strings.Contains(body, `value="Reopened Draft"`) || !strings.Contains(body, `value="draft" name="_customize-radio-changeset_status" checked="checked"`) || !strings.Contains(body, uuid) {
		t.Error("draft not reopened with its values")
	}
	// Another uuid (not a draft of this user) opens a fresh changeset.
	_, body, _ = get(t, c, ts.URL+"/wp-admin/customize?changeset_uuid=00000000000000000000000000000000")
	if strings.Contains(body, "Reopened Draft") {
		t.Error("unknown changeset reopened")
	}
}
