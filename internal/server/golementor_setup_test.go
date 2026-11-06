package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestGolementorSetupWizard installs the fixture kit with the wizard, checks what it made and the
// front end through the Theme Builder conditions, re-runs it (keep, replace) and removes it.
func TestGolementorSetupWizard(t *testing.T) {
	t.Skip("old builder: activation no longer switches the theme or offers the setup wizard (R1d); the test goes with R2")
	ts, c, srv := newTestServer(t, true)
	srv.gweLibOverride = os.DirFS(gweLibFixture)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	pool := getPool(t)
	scalar := func(q string, args ...any) string {
		t.Helper()
		var v *string
		if err := pool.QueryRow(ctx, q, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if v == nil {
			return ""
		}
		return *v
	}
	themeBefore := srv.settings.String("stylesheet")
	if themeBefore == helloThemeSlug {
		t.Fatal("the test expects another theme before the wizard")
	}

	// A theme part of the user's own (the working copy) and the kit's state before.
	code, tb, _ := get(t, c, base+"/wp-admin/theme-builder")
	tbNonce := nonceRe.FindStringSubmatch(tb)
	if code != http.StatusOK || tbNonce == nil {
		t.Fatalf("theme builder: %d", code)
	}
	_, _, loc := post(t, c, base+"/wp-admin/theme-builder", url.Values{"_wpnonce": {tbNonce[1]}, "action": {"new"}, "type": {"header"}, "title": {"My own header"}})
	ownPart := regexp.MustCompile(`post=(\d+)`).FindStringSubmatch(loc)
	if ownPart == nil {
		t.Fatalf("new part: %q", loc)
	}
	_, kitBefore := srv.gweKitSettings(ctx)
	colorsBefore, _ := json.Marshal(kitBefore["system_colors"])

	// The screen: menu entry, kits, presets.
	code, body, _ := get(t, c, base+gweSetupPath)
	if code != http.StatusOK {
		t.Fatalf("wizard: %d %.300s", code, body)
	}
	for _, want := range []string{`<h1 class="wp-heading-inline">Site Setup</h1>`, `href="/wp-admin/web-builder-setup"`, "Fixture Studio", "Choose a starter site",
		`name="kit" value="fixture-studio" checked`, `value="ocean"`, "Playfair Display / Source Sans 3", `name="pages_fixture-studio[]" value="services"`,
		`/wp-content/plugins/web-builder/assets/library/kits/fixture-studio/screenshot.webp`, "Live Preview", `name="logo_id"`} {
		if !strings.Contains(body, want) {
			t.Errorf("wizard misses %q", want)
		}
	}
	if strings.Contains(body, "Remove starter site") {
		t.Error("nothing is installed yet")
	}
	nonce := nonceRe.FindStringSubmatch(body)[1]

	// Live preview: the kit's header, page and footer with the chosen palette, nothing installed.
	code, pv, _ := get(t, c, base+gweSetupPath+"/preview?kit=fixture-studio&palette=ocean&fonts=editorial")
	if code != http.StatusOK {
		t.Fatalf("preview: %d %.300s", code, pv)
	}
	for _, want := range []string{"FX-HEADER", "FX-HOME welcome", "FX-FOOTER", "--e-global-color-primary:#0B5394", "Playfair Display",
		`page=contact`, "Preview of <strong>Fixture Studio</strong>", "elementorFrontendConfig", ">Journal<"} {
		if !strings.Contains(pv, want) {
			t.Errorf("preview misses %q", want)
		}
	}
	if _, pv, _ = get(t, c, base+gweSetupPath+"/preview?kit=fixture-studio&page=blog"); !strings.Contains(pv, "FX-ARCHIVE") || !strings.Contains(pv, "FX second post") {
		t.Errorf("preview blog: %.300s", pv)
	}
	if _, pv, _ = get(t, c, base+gweSetupPath+"/preview?kit=fixture-studio&page=post-1"); !strings.Contains(pv, "FX-SINGLE-POST") || !strings.Contains(pv, "FX-POST-BODY one") {
		t.Errorf("preview post: %.300s", pv)
	}
	if code, _, _ := get(t, c, base+gweSetupPath+"/preview?kit=nope"); code != http.StatusNotFound {
		t.Errorf("preview of an unknown kit: %d", code)
	}
	if n := scalar(`SELECT count(*)::text FROM posts WHERE meta ? '_gwe_starter'`); n != "0" {
		t.Errorf("the preview created %s posts", n)
	}

	// Install.
	install := func(form url.Values) {
		t.Helper()
		form.Set("_wpnonce", nonce)
		form.Set("action", "install")
		code, body, loc := post(t, c, base+gweSetupPath, form)
		if code != http.StatusFound || loc != gweSetupPath+"?done=1" {
			t.Fatalf("install: %d %q %.300s", code, loc, body)
		}
	}
	install(url.Values{"kit": {"fixture-studio"}, "palette": {"ocean"}, "fonts": {"editorial"}, "blogname": {"FX Site"}, "blogdescription": {""},
		"pages_sent_fixture-studio": {"1"}, "pages_fixture-studio[]": {"blog", "about", "contact"}}) // home is always made

	// What it made, all tagged.
	if got := scalar(`SELECT string_agg(meta->>'_gwe_starter_ref', ',' ORDER BY id) FROM posts WHERE type = 'page' AND meta->>'_gwe_starter' = 'fixture-studio'`); got != "home,blog,about,contact" {
		t.Errorf("pages: %s", got)
	}
	if got := scalar(`SELECT string_agg(meta->>'_elementor_template_type' || '=' || (meta->'_gwe_conditions')::text, ' ' ORDER BY id) FROM posts WHERE type = 'gwe_template' AND status = 'publish'`); !strings.Contains(got, `header=["include/general"]`) ||
		!strings.Contains(got, `error-404=["include/singular/not_found404"]`) || strings.Count(got, "=") != 7 || strings.Contains(got, "{{") {
		t.Errorf("parts: %s", got)
	}
	contactID := scalar(`SELECT id::text FROM posts WHERE type = 'page' AND meta->>'_gwe_starter_ref' = 'contact'`)
	if got := scalar(`SELECT (meta->'_gwe_conditions')::text FROM posts WHERE type = 'gwe_template' AND meta->>'_elementor_template_type' = 'single-page' AND status = 'publish'`); !strings.Contains(got, "exclude/singular/page/"+contactID) {
		t.Errorf("single page conditions: %s (contact %s)", got, contactID)
	}
	if got := scalar(`SELECT status FROM posts WHERE id = $1`, ownPart[1]); got != "trash" {
		t.Errorf("the user's part: %s", got)
	}
	if got := scalar(`SELECT (meta->>'_wp_page_template') || ' ' || (meta->'_elementor_page_settings')::text FROM posts WHERE id = $1`, contactID); got != `elementor_header_footer {"hide_title": "yes"}` {
		t.Errorf("contact page template and settings: %s", got)
	}
	if got := scalar(`SELECT coalesce(meta->>'_wp_page_template','') FROM posts WHERE type = 'page' AND meta->>'_gwe_starter_ref' = 'about'`); got != "" {
		t.Errorf("about page template: %q", got)
	}
	if got := scalar(`SELECT count(*)::text || '/' || count(featured_media_id)::text FROM posts WHERE type = 'post' AND meta->>'_gwe_starter' = 'fixture-studio'`); got != "3/3" {
		t.Errorf("posts/images: %s", got)
	}
	if got := scalar(`SELECT count(*)::text FROM media WHERE meta->>'_gwe_starter' = 'fixture-studio'`); got != "2" {
		t.Errorf("media: %s", got)
	}
	if got := scalar(`SELECT string_agg(name || '@' || coalesce(location,''), ',' ORDER BY id) FROM menus`); got != "Main menu@primary,Footer menu@footer" {
		t.Errorf("menus: %s", got)
	}
	if got := scalar(`SELECT string_agg(label, ',' ORDER BY sort_order) FROM menu_items i JOIN menus m ON m.id = i.menu_id WHERE m.location = 'primary'`); got != "Home,About,Journal,Contact" {
		t.Errorf("primary menu: %s", got)
	}
	if got := scalar(`SELECT string_agg(status, ',' ORDER BY id) FROM posts WHERE slug IN ('hello-world', 'sample-page')`); got != "trash,trash" {
		t.Errorf("samples: %s", got)
	}
	homeID := scalar(`SELECT id::text FROM posts WHERE type = 'page' AND meta->>'_gwe_starter_ref' = 'home'`)
	blogID := scalar(`SELECT id::text FROM posts WHERE type = 'page' AND meta->>'_gwe_starter_ref' = 'blog'`)
	if srv.settings.String("show_on_front") != "page" || itoa64(int64(srv.settings.Int("page_on_front"))) != homeID || itoa64(int64(srv.settings.Int("page_for_posts"))) != blogID {
		t.Errorf("reading: %s %d %d", srv.settings.String("show_on_front"), srv.settings.Int("page_on_front"), srv.settings.Int("page_for_posts"))
	}
	if srv.settings.String("blogname") != "FX Site" || srv.settings.String("blogdescription") != "FX tagline from the kit" || srv.settings.String("stylesheet") != helloThemeSlug {
		t.Errorf("site: %q %q %q", srv.settings.String("blogname"), srv.settings.String("blogdescription"), srv.settings.String("stylesheet"))
	}
	_, kit := srv.gweKitSettings(ctx)
	kitJSON, _ := json.Marshal(kit)
	for _, want := range []string{`"system_colors":[{"_id":"primary","color":"#0B5394","title":"Primary"}`, `{"_id":"fxlight","color":"#F8FAFC","title":"Light"}`,
		`"typography_font_family":"Playfair Display"`, `"typography_font_family":"Source Sans 3"`, `"body_background_color":"#FFFFFF"`,
		`"container_width":{"size":1180`, `"__globals__":{"body_color":"globals/colors?id=text","button_background_color":"globals/colors?id=primary"}`} {
		if !strings.Contains(string(kitJSON), want) {
			t.Errorf("kit settings miss %s", want)
		}
	}
	code, body, _ = get(t, c, base+gweSetupPath+"?done=1")
	if code != http.StatusOK || !strings.Contains(body, "Your site is ready.") || !strings.Contains(body, "Remove starter site") || !strings.Contains(body, "4 pages, 3 posts, 7 theme parts") {
		t.Errorf("done screen: %d", code)
	}

	// The front end: every location through the Theme Builder conditions.
	page := func(path string, status int, want ...string) string {
		t.Helper()
		code, body, _ := get(t, c, base+path)
		if code != status {
			t.Errorf("%s: %d", path, code)
		}
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s misses %q", path, w)
			}
		}
		return body
	}
	contactURL := base + "/contact/"
	home := page("/", http.StatusOK, "FX-HEADER", "FX-FOOTER", "FX-HOME welcome", `href="`+contactURL+`"`, "/wp-content/plugins/web-builder/assets/library/images/hero.webp",
		"FX Site", ">Journal<", "FX Footer About", "elementor-location-header", "elementor-location-footer")
	if strings.Contains(home, "FX-SINGLE-PAGE") {
		t.Error("the front page uses the single page part")
	}
	page("/blog/", http.StatusOK, "FX-HEADER", "FX-ARCHIVE", "FX first post", "FX second post", "FX-FOOTER")
	firstPost, err := srv.store.GetPost(ctx, gweInt(scalar(`SELECT id::text FROM posts WHERE type = 'post' AND slug = 'fx-first-post'`)))
	if err != nil {
		t.Fatal(err)
	}
	page(strings.TrimPrefix(srv.links(ctx).Permalink(firstPost), base), http.StatusOK, "FX-HEADER", "FX-SINGLE-POST", "FX first post", "FX-POST-BODY one", `href="`+base+`/about/"`, "golementor-fixture-studio-post-a", "FX News", "FX-FOOTER")
	page("/about/", http.StatusOK, "FX-HEADER", "FX-SINGLE-PAGE", "FX-ABOUT we are a small studio", "/library/kits/fixture-studio/images/team.webp",
		`<main id="content" class="site-main gwe-main-part">`, "FX-FOOTER")
	contact := page("/contact/", http.StatusOK, "FX-HEADER", "FX-CONTACT get in touch", "elementor-form", "FX-FOOTER")
	if strings.Contains(contact, "FX-SINGLE-PAGE") {
		t.Error("the contact page is excluded from the single page part")
	}
	page("/?s=zebrafish", http.StatusOK, "FX-HEADER", "FX-SEARCH", "FX third post", "FX-FOOTER")
	page("/no-such-page-at-all/", http.StatusNotFound, "FX-HEADER", "FX-404 nothing here", "FX-FOOTER")
	if css := regexp.MustCompile(`href='([^']*/elementor/css/post-\d+\.css[^']*)'`).FindAllStringSubmatch(home, -1); len(css) == 0 {
		t.Error("no kit stylesheet")
	} else {
		found := false
		for _, m := range css {
			if _, b, _ := get(t, c, m[1]); strings.Contains(b, "--e-global-color-primary:#0B5394") {
				found = true
			}
		}
		if !found {
			t.Error("the kit stylesheet lacks the ocean palette")
		}
	}

	// Sections inserted now link the installed kit pages.
	if _, b := restGet(t, c, base, "/wp-json/web-builder/v1/library/sections/hero/hero-split"); !strings.Contains(b, `"url":"`+contactURL+`"`) {
		t.Errorf("section page token: %.400s", b)
	}

	// Re-run, keep: content stays, colors and details change.
	_, body, _ = get(t, c, base+gweSetupPath)
	if !strings.Contains(body, `name="mode" value="replace" checked`) || !strings.Contains(body, `name="mode" value="keep"`) {
		t.Error("re-run offers no replace/keep choice")
	}
	install(url.Values{"kit": {"fixture-studio"}, "mode": {"keep"}, "palette": {"forest"}, "blogname": {"FX Kept"}})
	if got := scalar(`SELECT id::text FROM posts WHERE type = 'page' AND meta->>'_gwe_starter_ref' = 'home'`); got != homeID {
		t.Errorf("keep replaced the pages: %s", got)
	}
	if _, kit = srv.gweKitSettings(ctx); !strings.Contains(gweJSON(kit["system_colors"]), `"color":"#166534"`) || srv.settings.String("blogname") != "FX Kept" {
		t.Errorf("keep did not restyle: %s %s", gweJSON(kit["system_colors"]), srv.settings.String("blogname"))
	}

	// Re-run, replace: the old starter goes, a fresh one comes (default pages, kit colors, the
	// kit's site title as the field was left empty).
	install(url.Values{"kit": {"fixture-studio"}, "mode": {"replace"}})
	if got := scalar(`SELECT count(*)::text FROM posts WHERE meta->>'_gwe_starter' = 'fixture-studio' AND id <= $1`, homeID); got != "0" {
		t.Errorf("replace kept %s old items", got)
	}
	if got := scalar(`SELECT string_agg(meta->>'_gwe_starter_ref', ',' ORDER BY id) FROM posts WHERE type = 'page' AND meta->>'_gwe_starter' = 'fixture-studio'`); got != "home,blog,about,contact" {
		t.Errorf("replace pages: %s", got)
	}
	if got := scalar(`SELECT count(*)::text FROM posts WHERE type = 'gwe_template' AND status = 'publish'`); got != "7" {
		t.Errorf("replace parts: %s", got)
	}
	if got := scalar(`SELECT count(*)::text FROM menus`); got != "2" {
		t.Errorf("replace menus: %s", got)
	}
	if got := scalar(`SELECT count(*)::text FROM media WHERE meta ? '_gwe_starter'`); got != "2" {
		t.Errorf("replace media: %s", got)
	}
	if _, kit = srv.gweKitSettings(ctx); !strings.Contains(gweJSON(kit["system_colors"]), `"color":"#1D4ED8"`) || srv.settings.String("blogname") != "FX Kit Site" {
		t.Errorf("replace globals: %s %s", gweJSON(kit["system_colors"]), srv.settings.String("blogname"))
	}
	if got := scalar(`SELECT status FROM posts WHERE id = $1`, ownPart[1]); got != "trash" {
		t.Errorf("the user's part after replace: %s", got)
	}
	page("/", http.StatusOK, "FX-HEADER", "FX-HOME welcome", "FX-FOOTER")

	// Remove: exactly the starter goes, everything before comes back.
	code, _, loc = post(t, c, base+gweSetupPath, url.Values{"_wpnonce": {nonce}, "action": {"remove"}})
	if code != http.StatusFound || loc != gweSetupPath+"?removed=1" {
		t.Fatalf("remove: %d %q", code, loc)
	}
	for _, q := range []string{`SELECT count(*)::text FROM posts WHERE meta ? '_gwe_starter'`, `SELECT count(*)::text FROM media WHERE meta ? '_gwe_starter'`,
		`SELECT count(*)::text FROM terms WHERE meta ? '_gwe_starter'`, `SELECT count(*)::text FROM menus`} {
		if got := scalar(q); got != "0" {
			t.Errorf("%s: %s", q, got)
		}
	}
	if got := scalar(`SELECT status FROM posts WHERE id = $1`, ownPart[1]); got != "draft" {
		t.Errorf("the user's part after remove: %s", got)
	}
	if got := scalar(`SELECT string_agg(status, ',' ORDER BY id) FROM posts WHERE slug IN ('hello-world', 'sample-page')`); got != "publish,publish" {
		t.Errorf("samples after remove: %s", got)
	}
	if srv.settings.String("show_on_front") != "posts" || srv.settings.Int("page_on_front") != 0 || srv.settings.Int("page_for_posts") != 0 ||
		srv.settings.String("blogname") != "Toggle Site" || srv.settings.String("stylesheet") != themeBefore || srv.gweStarter() != nil {
		t.Errorf("settings after remove: %s %d %d %q %s", srv.settings.String("show_on_front"), srv.settings.Int("page_on_front"), srv.settings.Int("page_for_posts"),
			srv.settings.String("blogname"), srv.settings.String("stylesheet"))
	}
	if _, kit = srv.gweKitSettings(ctx); gweJSON(kit["system_colors"]) != strings.TrimSpace(string(colorsBefore)) {
		t.Errorf("kit colors after remove: %s, want %s", gweJSON(kit["system_colors"]), colorsBefore)
	}
	_, body, _ = get(t, c, base+gweSetupPath+"?removed=1")
	if !strings.Contains(body, "The starter site was removed") || strings.Contains(body, "Remove starter site") {
		t.Error("removed screen")
	}

	// Offered after activating Golementor; gone with it.
	togglePlugin(t, c, base, "deactivate")
	if code, _, _ := get(t, c, base+gweSetupPath); code != http.StatusNotFound {
		t.Errorf("wizard while Pursae Web Builder is off: %d", code)
	}
	loc = togglePlugin(t, c, base, "activate")
	if !strings.Contains(loc, "gwe_setup=1") {
		t.Fatalf("activation redirect: %s", loc)
	}
	if _, body, _ = get(t, c, base+loc); !strings.Contains(body, `href="/wp-admin/web-builder-setup">Run Site Setup</a>`) {
		t.Error("no Site Setup offer after activation")
	}
}

// TestGolementorSetupKits installs every kit of a library with the wizard and renders the site:
// the shipped library (web/gowebeditor/library) or the directory in WPGO_GWE_LIBRARY. Each kit
// must load without a problem; front page, every page, the blog, a post, search and 404 must
// render through the kit's header and footer (and its single/archive/search/404 parts).
func TestGolementorSetupKits(t *testing.T) {
	t.Skip("old builder detached from routing, admin and front (R1c/R1d); this file goes in R2")
	dir := os.Getenv("WPGO_GWE_LIBRARY")
	if dir == "" {
		dir = "../../web/gowebeditor/library"
	}
	lib := gweLoadLibrary(os.DirFS(dir))
	for _, e := range lib.Errors {
		t.Errorf("library: %s", e)
	}
	if len(lib.Kits) == 0 {
		t.Skip("no kits in " + dir)
	}
	ts, c, srv := newTestServer(t, true)
	srv.gweLibOverride = os.DirFS(dir)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	pool := getPool(t)
	_, body, _ := get(t, c, base+gweSetupPath)
	nonce := nonceRe.FindStringSubmatch(body)[1]
	for _, k := range lib.Kits {
		code, body, loc := post(t, c, base+gweSetupPath, url.Values{"_wpnonce": {nonce}, "action": {"install"}, "kit": {k.Slug}, "mode": {"replace"}})
		if code != http.StatusFound || loc != gweSetupPath+"?done=1" {
			t.Errorf("%s: install: %d %q %.200s", k.Slug, code, loc, body)
			continue
		}
		var paths []string
		rows, err := pool.Query(ctx, `SELECT id FROM posts WHERE meta->>'_gwe_starter' = $1 AND type IN ('page', 'post') ORDER BY type, id`, k.Slug)
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for rows.Next() {
			var id int64
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		posts := 0
		for _, id := range ids {
			p, err := srv.store.GetPost(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if p.Type == "post" {
				if posts++; posts > 1 {
					continue // one post is enough
				}
			}
			paths = append(paths, strings.TrimPrefix(srv.links(ctx).Permalink(p), base))
		}
		if posts != len(k.Posts) {
			t.Errorf("%s: %d posts, want %d", k.Slug, posts, len(k.Posts))
		}
		paths = append(paths, "/?s=the", "/no-such-page-for-the-kit-test/")
		for _, path := range paths {
			code, body, _ := get(t, c, base+path)
			want := http.StatusOK
			if strings.HasPrefix(path, "/no-such") {
				want = http.StatusNotFound
			}
			if code != want || !strings.Contains(body, "elementor-location-header") || !strings.Contains(body, "elementor-location-footer") || strings.Contains(body, "{{") {
				t.Errorf("%s %s: %d, header %v, footer %v, raw token %v", k.Slug, path, code, strings.Contains(body, "elementor-location-header"),
					strings.Contains(body, "elementor-location-footer"), strings.Contains(body, "{{"))
			}
		}
		t.Logf("%s: installed and rendered %d URLs", k.Slug, len(paths))
	}
	if code, _, _ := post(t, c, base+gweSetupPath, url.Values{"_wpnonce": {nonce}, "action": {"remove"}}); code != http.StatusFound {
		t.Errorf("remove: %d", code)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE meta ? '_gwe_starter'`).Scan(&n)
	if n != 0 {
		t.Errorf("%d starter posts left after remove", n)
	}
}
