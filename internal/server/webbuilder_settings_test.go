package server

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Integration tests of the plugin switch and Settings › Web Builder (need the test database).

// The Plugins screen switches Pursae Web Builder on and off: Hello Pursae comes and goes with it
// (theme_hellopursae_test.go covers the switching), no setup wizard.
func TestWebBuilderPluginToggle(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	theme := srv.settings.String("stylesheet")

	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	row := regexp.MustCompile(`(?s)<tr class="inactive" data-slug="golementor".*?</tr>`).FindString(body)
	if row == "" || !strings.Contains(row, "Pursae Web Builder") || !strings.Contains(row, "publish them as fast, static HTML") {
		t.Fatalf("Plugins row: %.600s", row)
	}
	if strings.Contains(row, "Hello Pursae Web Builder") || !strings.Contains(row, "Appearance &rsaquo; Theme Builder") {
		t.Error("the row describes the old builder, or not the Theme Builder")
	}

	loc := togglePlugin(t, c, base, "activate")
	if loc != "/wp-admin/plugins?activate=true" || !srv.wbOn() {
		t.Fatalf("activate: %s on=%v", loc, srv.wbOn())
	}
	if got := srv.settings.String("stylesheet"); got != wbHelloSlug {
		t.Errorf("activation left the theme on %s", got)
	}
	_, body, _ = get(t, c, base+loc)
	if !strings.Contains(body, "Plugin activated.") || strings.Contains(body, "Run Site Setup") || !strings.Contains(body, "Your site now uses Hello Pursae") {
		t.Error("activation notices")
	}
	row = regexp.MustCompile(`(?s)<tr class="active" data-slug="golementor".*?</tr>`).FindString(body)
	if !strings.Contains(row, `href="`+strings.ReplaceAll(wbSettingsURL, "&", "&amp;")+`"`) && !strings.Contains(row, `href="`+wbSettingsURL+`"`) {
		t.Errorf("the active row has no Settings link to Settings › Web Builder: %.600s", row)
	}

	loc = togglePlugin(t, c, base, "deactivate")
	if loc != "/wp-admin/plugins?deactivate=true" || srv.wbOn() {
		t.Fatalf("deactivate: %s on=%v", loc, srv.wbOn())
	}
	if got := srv.settings.String("stylesheet"); got != theme {
		t.Errorf("deactivation left the theme on %s, want %s", got, theme)
	}
}

// Settings › Web Builder: menu entry and screen only while the plugin is on, manage_options only,
// the post types decide what opens in the builder, outdated pages are listed.
func TestWebBuilderSettingsScreen(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()

	if code, _, _ := get(t, c, base+wbSettingsURL); code != http.StatusForbidden {
		t.Errorf("settings while off: %d, want 403", code)
	}
	if _, body, _ := get(t, c, base+"/wp-admin/"); strings.Contains(body, "options-general?page=web-builder") {
		t.Error("the Settings menu lists Web Builder while the plugin is off")
	}
	if err := srv.wbSetActive(ctx, true); err != nil {
		t.Fatal(err)
	}
	if !srv.wbEnabledType("page") || !srv.wbEnabledType("post") || srv.wbEnabledType("attachment") || srv.wbEnabledType("") {
		t.Fatalf("default post types: %v", srv.wbEnabledTypes())
	}

	code, body, _ := get(t, c, base+wbSettingsURL)
	if code != http.StatusOK || !strings.Contains(body, "<h1>Web Builder Settings</h1>") {
		t.Fatalf("settings: %d %.300s", code, body)
	}
	if !regexp.MustCompile(`<li class=" ?current"><a href="[^"]*options-general\?page=web-builder"[^>]*>Web Builder</a>`).MatchString(body) {
		t.Errorf("no current Settings › Web Builder menu entry:\n%s", regexp.MustCompile(`(?s)id="menu-settings".*?</ul>`).FindString(body))
	}
	for _, want := range []string{`name="option_page" value="plugin:web-builder"`, `id="web_builder_post_types-page" value="page" checked="checked"`,
		`id="web_builder_post_types-post" value="post" checked="checked"`, `<h2 class="title">Regenerate pages</h2>`, `id="web-builder-outdated-none"`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings screen lacks %s", want)
		}
	}
	for _, bad := range []string{"web_builder_post_types-attachment", "AI", "API key"} {
		if strings.Contains(body, bad) {
			t.Errorf("settings screen contains %q", bad)
		}
	}
	nonce := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)
	if nonce == nil {
		t.Fatal("no nonce")
	}

	// Save: only offered types are kept.
	form := url.Values{"option_page": {"plugin:web-builder"}, "action": {"update"}, "_wpnonce": {nonce[1]},
		"web_builder_post_types[]": {"page", "attachment", "nope"}}
	code, _, loc := post(t, c, base+"/wp-admin/options", form)
	if code != http.StatusFound || loc != wbSettingsURL+"&settings-updated=true" {
		t.Fatalf("save: %d %s", code, loc)
	}
	if got := srv.settings.String(wbPostTypesOption); got != "page" {
		t.Errorf("saved post types %q, want page", got)
	}
	if srv.wbEnabledType("post") || !srv.wbEnabledType("page") {
		t.Error("wbEnabledType does not follow the option")
	}
	if _, body, _ = get(t, c, base+loc); !strings.Contains(body, "Settings saved.") ||
		strings.Contains(body, `id="web_builder_post_types-post" value="post" checked`) {
		t.Error("the saved settings are not shown")
	}
	form.Set("_wpnonce", "bad")
	if code, _, _ := post(t, c, base+"/wp-admin/options", form); code != http.StatusForbidden {
		t.Errorf("bad nonce: %d, want 403", code)
	}

	// A post type that is off does not open in the builder.
	p := wbTestPost(t, srv, "post", "Builder Post", "draft", 1)
	if code, _, _ := wbOpen(t, c, base, p.ID); code != http.StatusForbidden {
		t.Errorf("editor of a disabled post type: %d, want 403", code)
	}

	// Pages published by an older renderer are listed with a link to the editor.
	pg := wbTestPost(t, srv, "page", "Old Render", "publish", 1)
	d := &store.WebBuilderDoc{PostID: pg.ID, Doc: []byte(`{"version":1,"sections":[]}`), Renderer: webbuilder.RendererVersion - 1}
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO web_builder_docs (post_id, doc, renderer) VALUES ($1, $2, $3)`, d.PostID, d.Doc, d.Renderer); err != nil {
		t.Fatal(err)
	}
	cur := wbTestPost(t, srv, "page", "Current Render", "publish", 1)
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO web_builder_docs (post_id, doc, renderer) VALUES ($1, $2, $3)`, cur.ID, d.Doc, webbuilder.RendererVersion); err != nil {
		t.Fatal(err)
	}
	_, body, _ = get(t, c, base+wbSettingsURL)
	list := regexp.MustCompile(`(?s)<ul class="ul-disc" id="web-builder-outdated">.*?</ul>`).FindString(body)
	if !strings.Contains(list, `href="`+strings.ReplaceAll(wbEditURL(pg.ID), "&", "&amp;")+`">Old Render</a>`) || strings.Contains(list, "Current Render") {
		t.Errorf("outdated list: %s", list)
	}

	// manage_options only: an editor gets neither the screen nor the save.
	asCreateUser(t, srv, "ed", "editor", asStrongPass)
	ec := asNewClient()
	if code, _, _ := asLogin(t, ec, base, "/wp-login", "ed", asStrongPass); code != http.StatusFound {
		t.Fatalf("editor login: %d", code)
	}
	if code, _, _ := get(t, ec, base+wbSettingsURL); code != http.StatusForbidden {
		t.Errorf("editor on the settings screen: %d, want 403", code)
	}
	form.Set("_wpnonce", nonce[1])
	if code, _, _ := post(t, ec, base+"/wp-admin/options", form); code != http.StatusForbidden {
		t.Errorf("editor saving the settings: %d, want 403", code)
	}
}

// Settings › Web Builder offers only post types that exist right now: Products only while Pursae
// Commerce is on (its content_types row stays when it is off), ACF-Go types only with show_ui.
func TestWebBuilderSettingsPostTypesRegistered(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if err := srv.wbSetActive(ctx, true); err != nil {
		t.Fatal(err)
	}
	slugs := func() []string {
		var out []string
		for _, ch := range srv.wbPostTypeChoices(ctx) {
			out = append(out, ch.Slug)
		}
		return out
	}
	has := func(slug string) bool { return slices.Contains(slugs(), slug) }

	if err := srv.settings.Set(ctx, map[string]any{gooSetting: false}); err != nil {
		t.Fatal(err)
	}
	if has("product") || !has("page") || !has("post") {
		t.Errorf("Commerce off: %v", slugs())
	}
	if _, body, _ := get(t, c, ts.URL+wbSettingsURL); strings.Contains(body, `value="product"`) {
		t.Error("the screen lists product while Commerce is off")
	}
	if err := srv.settings.Set(ctx, map[string]any{gooSetting: true}); err != nil {
		t.Fatal(err)
	}
	if !has("product") {
		t.Errorf("Commerce on: %v", slugs())
	}
	_ = srv.settings.Set(ctx, map[string]any{gooSetting: false})

	// An ACF-Go type: listed while registered with show_ui, not without it or once unregistered.
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels)
		VALUES ('wbt_book', 'Books', false, true, 'books', false, '{"title":true,"editor":true}', '{"name":"Books"}') ON CONFLICT (slug) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	set := func(pt *acfgoPostTypeDef) {
		acfgoRegistry.Lock()
		if pt == nil {
			delete(acfgoRegistry.types, "wbt_book")
		} else {
			acfgoRegistry.types["wbt_book"] = pt
		}
		acfgoRegistry.Unlock()
	}
	t.Cleanup(func() { set(nil) })
	if has("wbt_book") {
		t.Error("unregistered ACF-Go type listed")
	}
	set(&acfgoPostTypeDef{Name: "wbt_book", Public: true, ShowUI: false})
	if has("wbt_book") {
		t.Error("ACF-Go type without show_ui listed")
	}
	set(&acfgoPostTypeDef{Name: "wbt_book", Public: true, ShowUI: true})
	if !has("wbt_book") {
		t.Errorf("registered ACF-Go type missing: %v", slugs())
	}

	// An enabled type that no longer exists is still listed (checked) so it can be switched off.
	if err := srv.settings.Set(ctx, map[string]any{wbPostTypesOption: "page,post,product"}); err != nil {
		t.Fatal(err)
	}
	for _, ch := range srv.wbPostTypeChoices(ctx) {
		if ch.Slug == "product" && !ch.Checked {
			t.Error("enabled product not checked")
		}
	}
	if !has("product") {
		t.Error("enabled product missing while Commerce is off")
	}
}
