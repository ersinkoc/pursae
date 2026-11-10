package server

import (
	"bytes"
	"context"
	"html"
	"html/template"
	"image/png"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Hello Pursae: the theme's registration and templates (unit tests), and the plugin's theme
// switching (integration tests, need the test database).

// helloTemplates parses Hello Pursae's templates from the source tree.
func helloTemplates(t *testing.T) *template.Template {
	t.Helper()
	set, err := parseTree(os.DirFS("../../web"), "themes/"+wbHelloSlug+"/templates", (&Server{}).pllGettextFuncs()) // __, _x: the msgid without a request
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// helloRender executes one of the theme's page templates with a view.
func helloRender(t *testing.T, set *template.Template, name string, v *siteView) string {
	t.Helper()
	var buf bytes.Buffer
	if err := set.ExecuteTemplate(&buf, name, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return buf.String()
}

func TestHelloPursaeRegistration(t *testing.T) {
	th := themeBySlug(wbHelloSlug)
	if th == nil || th.Name != "Hello Pursae" || th.Author != "Pursae" || th.Head == nil {
		t.Fatalf("theme: %+v", th)
	}
	locs := map[string]string{}
	for _, l := range th.MenuLocations {
		locs[l.Slug] = l.Name
	}
	if len(locs) != 2 || locs["primary"] != "Primary Menu" || locs["footer"] != "Footer Menu" {
		t.Errorf("menu locations: %v", th.MenuLocations)
	}
	for _, f := range []string{"custom-logo", "title-tag", "post-thumbnails", "html5", "menus", "align-wide"} {
		if th.Supports[f] == nil {
			t.Errorf("no theme support for %s", f)
		}
	}
	if !themeSupportsLogo(th) {
		t.Error("custom logo not supported")
	}
	if got := th.BodyClasses(nil, &siteView{Data: map[string]any{}}); strings.Join(got, " ") != "hello-pursae" {
		t.Errorf("body classes: %v", got)
	}
	if got := th.BodyClasses(nil, &siteView{Data: map[string]any{wbThemePartsKey: &wbThemePartsView{}}}); strings.Join(got, " ") != "hello-pursae wb-site" {
		t.Errorf("body classes with parts: %v", got)
	}

	// Assets: a small stylesheet, the editor style and a 1200x900 screenshot; no foreign brand.
	css, err := os.ReadFile("../../web/themes/hello-pursae/assets/style.css")
	if err != nil || len(css) > 5000 || !bytes.Contains(css, []byte("Theme Name: Hello Pursae")) {
		t.Errorf("style.css: %d bytes, %v", len(css), err)
	}
	// Every rule sits in the low "hello-pursae" layer, so the builder's layered Tailwind CSS (its
	// reset and utilities) beats the theme's element defaults on builder parts and pages.
	if !regexp.MustCompile(`(?s)^/\*.*?\*/\s*(/\*.*?\*/\s*)?@layer hello-pursae\{.*\}\s*$`).Match(css) || bytes.Count(css, []byte("@layer")) != 1 {
		t.Error("style.css rules are not all inside @layer hello-pursae")
	}
	if _, err := os.Stat("../../web/themes/hello-pursae/assets/editor-style.css"); err != nil {
		t.Error(err)
	}
	f, err := os.Open("../../web/themes/hello-pursae/assets/screenshot.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if cfg, err := png.DecodeConfig(f); err != nil || cfg.Width != 1200 || cfg.Height != 900 {
		t.Errorf("screenshot: %+v %v", cfg, err)
	}
	for _, p := range []string{"internal/server/theme_hellopursae.go", "web/themes/hello-pursae/templates/header.html",
		"web/themes/hello-pursae/templates/pages.html", "web/themes/hello-pursae/templates/content.html", "web/themes/hello-pursae/assets/style.css",
		"web/themes/hello-pursae/assets/editor-style.css"} {
		b, err := os.ReadFile("../../" + p)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(bytes.ToLower(b), []byte("elementor")) {
			t.Errorf("%s names the upstream builder", p)
		}
	}
}

// The templates print the minimal defaults without parts, and the parts of wbThemePartsView in
// their place when they are set: Header, Footer, and Main inside main#content or, when the part has
// its own <main>, div#content.
func TestHelloPursaeTemplates(t *testing.T) {
	set := helloTemplates(t)
	base := func() *siteView {
		post := &store.Post{ID: 7, Type: "page", Title: "About"}
		return &siteView{Q: &siteQuery{Kind: "page", Post: post}, SiteName: "Hello Site", Description: "Just testing", HomeURL: "/",
			PrimaryMenu: `<ul id="primary-menu"><li><a href="/x/">Menu X</a></li></ul>`, CustomLogo: `<a class="custom-logo-link"><img class="custom-logo"></a>`,
			Post: &postView{ID: 7, Type: "page", Title: "About", Content: "<p>Page body</p>", Class: "post-7 page"},
			Data: map[string]any{"Year": "2026", "FooterMenu": template.HTML(`<ul id="footer-menu"><li>Foot</li></ul>`)}}
	}
	out := helloRender(t, set, "theme/page", base())
	for _, want := range []string{`<a class="skip-link screen-reader-text" href="#content">`, `<header id="site-header" class="site-header">`,
		`class="custom-logo"`, `<p class="site-title"><a href="/" rel="home">Hello Site</a></p>`, `<nav class="site-navigation" aria-label="Menu"><ul id="primary-menu">`,
		`<main id="content" class="site-main">`, `<h1 class="entry-title">About</h1>`, `<p>Page body</p>`, `<footer id="site-footer" class="site-footer">`,
		`<ul id="footer-menu">`, `&copy; 2026`} {
		if !strings.Contains(out, want) {
			t.Errorf("defaults lack %s", want)
		}
	}
	if strings.Contains(out, "wb-part") {
		t.Error("defaults print part wrappers")
	}

	// Parts.
	v := base()
	v.Data[wbThemePartsKey] = &wbThemePartsView{Header: "<header>HDR</header>", Footer: "<footer>FTR</footer>", Main: "<section>MAIN</section>"}
	out = helloRender(t, set, "theme/page", v)
	for _, want := range []string{`<div class="wb-part wb-part-header"><header>HDR</header></div>`, `<div class="wb-part wb-part-footer"><footer>FTR</footer></div>`,
		`<main id="content" class="site-main wb-main-part">`, `<section>MAIN</section>`} {
		if !strings.Contains(out, want) {
			t.Errorf("parts: lack %s", want)
		}
	}
	for _, bad := range []string{"site-header", "site-footer", "Page body", "Menu X"} {
		if strings.Contains(out, bad) {
			t.Errorf("parts: the default %s is still printed", bad)
		}
	}
	if !strings.Contains(out, "</main>") || strings.Count(out, "<main") != 1 {
		t.Error("parts: main element")
	}
	v.Data[wbThemePartsKey] = &wbThemePartsView{Main: "<main>OWN</main>", HasMain: true}
	out = helloRender(t, set, "theme/single", v)
	if !strings.Contains(out, `<div id="content" class="site-main wb-main-part"><main>OWN</main>`) && !strings.Contains(out, `<div id="content" class="site-main wb-main-part">`+"\n<main>OWN</main>") {
		t.Errorf("own main: %s", out)
	}
	if strings.Count(out, "<main") != 1 || !strings.Contains(out, `<header id="site-header"`) || !strings.Contains(out, `<footer id="site-footer"`) {
		t.Error("own main: one main element, default header and footer")
	}
	// Canvas: no header or footer, neither the parts' wrappers nor the defaults.
	v.Data[wbThemePartsKey] = &wbThemePartsView{Main: "<section>CANVAS</section>", Canvas: true}
	out = helloRender(t, set, "theme/page", v)
	if !strings.Contains(out, "<section>CANVAS</section>") {
		t.Error("canvas: main missing")
	}
	for _, bad := range []string{"wb-part", "site-header", "site-footer"} {
		if strings.Contains(out, bad) {
			t.Errorf("canvas prints %s", bad)
		}
	}
	// An empty Main keeps the theme's content.
	v.Data[wbThemePartsKey] = &wbThemePartsView{Header: "<header>HDR</header>"}
	if out = helloRender(t, set, "theme/page", v); !strings.Contains(out, "Page body") || !strings.Contains(out, `<main id="content" class="site-main">`) {
		t.Error("an empty Main part hides the content")
	}

	// Every view renders without parts.
	for _, name := range []string{"theme/index", "theme/single", "theme/archive", "theme/search", "theme/404"} {
		v := base()
		v.Q = &siteQuery{Kind: "search", Search: "q"}
		v.SearchQuery = "<q>"
		out := helloRender(t, set, name, v)
		if !strings.Contains(out, `<main id="content" class="site-main">`) || !strings.Contains(out, "</html>") {
			t.Errorf("%s: %.300s", name, out)
		}
		if name == "theme/search" && !strings.Contains(out, "&lt;q&gt;") {
			t.Error("the search query is not escaped")
		}
	}
}

// helloNoticeLink finds a link of the Hello Pursae notice by its id.
func helloNoticeLink(t *testing.T, body, id string) string {
	t.Helper()
	m := regexp.MustCompile(`href="([^"]+)" id="` + id + `"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s link", id)
	}
	return html.UnescapeString(m[1])
}

// Activating the plugin switches to Hello Pursae and remembers the theme; deactivating goes back.
func TestHelloPursaeSwitching(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if srv.settings.String("stylesheet") != defaultTheme {
		t.Fatalf("start theme %s", srv.settings.String("stylesheet"))
	}

	// Activate: Hello Pursae, the previous theme remembered, the notice on the Plugins screen.
	loc := togglePlugin(t, c, base, "activate")
	if got := srv.settings.String("stylesheet"); got != wbHelloSlug || srv.settings.String("template") != wbHelloSlug {
		t.Fatalf("after activation the theme is %s", got)
	}
	if got := srv.settings.String(wbPreviousThemeOption); got != defaultTheme {
		t.Errorf("previous theme %q", got)
	}
	_, body, _ := get(t, c, base+loc)
	if !strings.Contains(body, "Your site now uses Hello Pursae") || !strings.Contains(body, "Previous theme: <strong>Twenty Twenty-Five</strong>") {
		t.Fatal("no Hello Pursae notice on the Plugins screen")
	}
	if strings.Contains(body, "Switch to Hello Pursae") {
		t.Error("the offer shows after activation")
	}

	// The front end renders through Hello Pursae, with menus at its locations and the custom logo.
	var admin int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&admin)
	for loc, label := range map[string]string{"primary": "Primary Link", "footer": "Footer Link"} {
		id, err := srv.store.CreateMenu(ctx, loc+" menu")
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.store.AddMenuItem(ctx, &store.EditItem{MenuID: id, Label: label, Kind: "custom", Object: "custom", URL: "https://example.test/" + loc, Order: 1, Status: "publish"}); err != nil {
			t.Fatal(err)
		}
		if err := srv.store.SetMenuLocations(ctx, map[string]int64{loc: id}); err != nil {
			t.Fatal(err)
		}
	}
	logo := mustUploadTestImage(t, srv, admin)
	if err := srv.settings.Set(ctx, map[string]any{"theme_mods_" + wbHelloSlug: map[string]any{"custom_logo": logo}}); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	anon := &http.Client{CheckRedirect: c.CheckRedirect}
	code, body, _ := get(t, anon, base+"/")
	if code != http.StatusOK {
		t.Fatalf("front: %d", code)
	}
	for _, want := range []string{"id='hello-pursae-css'", "/wp-content/themes/hello-pursae/style.css", `class="skip-link screen-reader-text" href="#content"`,
		`<header id="site-header" class="site-header">`, `class="custom-logo"`, ">Primary Link<", ">Footer Link<", `<main id="content" class="site-main">`,
		`<footer id="site-footer" class="site-footer">`} {
		if !strings.Contains(body, want) {
			t.Errorf("front page lacks %s", want)
		}
	}
	if !regexp.MustCompile(`<body class="[^"]*\bhello-pursae\b`).MatchString(body) {
		t.Error("no hello-pursae body class")
	}
	if code, b, _ := get(t, anon, base+"/wp-content/themes/hello-pursae/style.css"); code != http.StatusOK || !strings.Contains(b, "Hello Pursae") {
		t.Errorf("style.css: %d", code)
	}
	p := wbTestPost(t, srv, "post", "Hello Post", "publish", admin)
	purl := base + "/?p=" + itoa64(p.ID)
	if code, _, loc := get(t, anon, purl); code == http.StatusMovedPermanently {
		purl = loc
		if strings.HasPrefix(loc, "/") {
			purl = base + loc
		}
	}
	if code, b, _ := get(t, anon, purl); code != http.StatusOK || !strings.Contains(b, `<h1 class="entry-title">Hello Post</h1>`) {
		t.Errorf("single post: %d", code)
	}
	if code, b, _ := get(t, anon, base+"/no-such-page-at-all/"); code != http.StatusNotFound || !strings.Contains(b, "The page can&rsquo;t be found.") {
		t.Errorf("404: %d", code)
	}
	// Appearance › Menus offers the theme's locations.
	if _, b, _ := get(t, c, base+"/wp-admin/nav-menus?action=locations"); !strings.Contains(b, "Primary Menu") || !strings.Contains(b, "Footer Menu") {
		t.Error("menu locations screen lacks Hello Pursae's locations")
	}

	// Deactivate: back to the previous theme, options cleared, no notice.
	togglePlugin(t, c, base, "deactivate")
	if got := srv.settings.String("stylesheet"); got != defaultTheme {
		t.Errorf("after deactivation the theme is %s", got)
	}
	if srv.settings.Get(wbPreviousThemeOption) != nil || srv.settings.Get(wbThemeNoticeOption) != nil {
		t.Error("deactivation left its options")
	}
	if _, b, _ := get(t, c, base+"/wp-admin/plugins"); strings.Contains(b, "Hello Pursae</strong>") {
		t.Error("a Hello Pursae notice while the plugin is off")
	}

	// The user changes the theme while the plugin is on: deactivation leaves it.
	wbuRegisterTheme(t, &Theme{Slug: "hp-other", Name: "HP Other", Version: "1.0"})
	togglePlugin(t, c, base, "activate")
	if err := srv.switchTheme(ctx, "hp-other"); err != nil {
		t.Fatal(err)
	}
	if _, b, _ := get(t, c, base+"/wp-admin/"); strings.Contains(b, "Your site now uses Hello Pursae") || strings.Contains(b, "Switch to Hello Pursae") {
		t.Error("a Hello Pursae notice on another theme")
	}
	togglePlugin(t, c, base, "deactivate")
	if got := srv.settings.String("stylesheet"); got != "hp-other" {
		t.Errorf("deactivation switched the user's theme to %s", got)
	}

	// The owner leaves Hello Pursae and later picks it again: activation's switch is over, so no
	// notice offers the old theme and deactivation keeps the Hello Pursae they chose.
	togglePlugin(t, c, base, "activate")
	if err := srv.switchTheme(ctx, defaultTheme); err != nil {
		t.Fatal(err)
	}
	if err := srv.switchTheme(ctx, wbHelloSlug); err != nil {
		t.Fatal(err)
	}
	if _, b, _ := get(t, c, base+"/wp-admin/"); strings.Contains(b, "Your site now uses Hello Pursae") {
		t.Error("a stale Hello Pursae notice after the owner chose Hello Pursae again")
	}
	togglePlugin(t, c, base, "deactivate")
	if got := srv.settings.String("stylesheet"); got != wbHelloSlug {
		t.Errorf("deactivation moved the owner's Hello Pursae to %s", got)
	}
	if err := srv.switchTheme(ctx, "hp-other"); err != nil {
		t.Fatal(err)
	}

	// Activation from hp-other remembers it; a previous theme that is gone falls back to the default.
	togglePlugin(t, c, base, "activate")
	if srv.settings.String(wbPreviousThemeOption) != "hp-other" {
		t.Fatalf("previous %q", srv.settings.String(wbPreviousThemeOption))
	}
	_ = srv.settings.Set(ctx, map[string]any{wbPreviousThemeOption: "no-such-theme"})
	togglePlugin(t, c, base, "deactivate")
	if got := srv.settings.String("stylesheet"); got != defaultTheme {
		t.Errorf("missing previous theme: now %s, want %s", got, defaultTheme)
	}

	// Hello Pursae was deleted: activation installs it again.
	if err := srv.deleteTheme(ctx, wbHelloSlug); err != nil {
		t.Fatal(err)
	}
	togglePlugin(t, c, base, "activate")
	if srv.settings.String("stylesheet") != wbHelloSlug || srv.themeDeleted(wbHelloSlug) {
		t.Fatal("activation did not restore a deleted Hello Pursae")
	}

	// Switch back: a bad nonce is refused, the link goes back to the previous theme and keeps the
	// plugin on; no offer follows.
	_, body, _ = get(t, c, base+"/wp-admin/themes")
	back := helloNoticeLink(t, body, "web-builder-theme-switch-back")
	if code, _, _ := get(t, c, base+strings.Replace(back, "_wpnonce=", "_wpnonce=x", 1)); code != http.StatusForbidden {
		t.Errorf("switch back with a bad nonce: %d", code)
	}
	if code, _, loc := get(t, c, base+back); code != http.StatusFound || loc != "/wp-admin/themes" {
		t.Fatalf("switch back: %d %s", code, loc)
	}
	if srv.settings.String("stylesheet") != defaultTheme || !srv.wbOn() || srv.settings.Get(wbThemeNoticeOption) != nil {
		t.Errorf("after Switch back: theme %s on=%v", srv.settings.String("stylesheet"), srv.wbOn())
	}
	if _, b, _ := get(t, c, base+"/wp-admin/"); strings.Contains(b, "Hello Pursae</strong>") {
		t.Error("a notice after Switch back")
	}

	// An editor (no switch_themes) neither sees the notice nor runs its links.
	asCreateUser(t, srv, "hp-ed", "editor", asStrongPass)
	ec := asNewClient()
	if code, _, _ := asLogin(t, ec, base, "/wp-login", "hp-ed", asStrongPass); code != http.StatusFound {
		t.Fatalf("editor login: %d", code)
	}
	_ = srv.settings.Set(ctx, map[string]any{wbHelloOfferedOption: nil})
	if _, b, _ := get(t, ec, base+"/wp-admin/"); strings.Contains(b, "Switch to Hello Pursae") {
		t.Error("offer shown to an editor")
	}
	if code, _, _ := get(t, ec, base+wbSettingsURL+"&wb-theme=hello&_wpnonce=x"); code != http.StatusForbidden {
		t.Errorf("editor running the offer: %d", code)
	}
}

// A site that had the plugin on before Hello Pursae existed is not switched silently: it gets a
// one-time offer, which switches or is dismissed for good.
func TestHelloPursaeOffer(t *testing.T) {
	ts, c, srv := newTestServer(t, true)
	base := ts.URL
	installAndLogin(t, ts, c)
	if !srv.wbOn() || srv.settings.String("stylesheet") != defaultTheme {
		t.Fatalf("start: on=%v theme=%s", srv.wbOn(), srv.settings.String("stylesheet"))
	}
	_, body, _ := get(t, c, base+"/wp-admin/")
	if !strings.Contains(body, "Switch to Hello Pursae") {
		t.Fatal("no offer on the Dashboard")
	}
	if _, b, _ := get(t, c, base+"/wp-admin/edit"); strings.Contains(b, "Switch to Hello Pursae") {
		t.Error("offer on the Posts screen")
	}
	if code, _, loc := get(t, c, base+helloNoticeLink(t, body, "web-builder-theme-switch-hello")); code != http.StatusFound || loc != "/wp-admin/" {
		t.Fatalf("switch: %d %s", code, loc)
	}
	if srv.settings.String("stylesheet") != wbHelloSlug || srv.settings.String(wbPreviousThemeOption) != defaultTheme {
		t.Fatalf("after the offer: theme %s", srv.settings.String("stylesheet"))
	}
	_, body, _ = get(t, c, base+"/wp-admin/")
	if !strings.Contains(body, "Your site now uses Hello Pursae") || strings.Contains(body, "Switch to Hello Pursae") {
		t.Error("after the offer: notices")
	}
	// Dismiss sticks.
	if code, _, _ := get(t, c, base+helloNoticeLink(t, body, "web-builder-theme-dismiss")); code != http.StatusFound {
		t.Fatal("dismiss")
	}
	if _, b, _ := get(t, c, base+"/wp-admin/"); strings.Contains(b, "Hello Pursae</strong>") {
		t.Error("notice after Dismiss")
	}
	if srv.settings.String("stylesheet") != wbHelloSlug {
		t.Error("Dismiss switched the theme")
	}

	// The offer's own Dismiss: no switch, never shown again.
	ctx := context.Background()
	if err := srv.switchTheme(ctx, defaultTheme); err != nil {
		t.Fatal(err)
	}
	_ = srv.settings.Set(ctx, map[string]any{wbHelloOfferedOption: nil, webbuilder.PluginOption: true})
	_, body, _ = get(t, c, base+"/wp-admin/plugins")
	if code, _, loc := get(t, c, base+helloNoticeLink(t, body, "web-builder-theme-dismiss")); code != http.StatusFound || loc != "/wp-admin/plugins" {
		t.Fatalf("dismiss the offer: %d %s", code, loc)
	}
	if _, b, _ := get(t, c, base+"/wp-admin/plugins"); strings.Contains(b, "Switch to Hello Pursae") || srv.settings.String("stylesheet") != defaultTheme {
		t.Error("the offer is back, or Dismiss switched the theme")
	}
}

// Store pages (Pursae Commerce) print their markup as the page content inside Hello Pursae.
func TestHelloPursaeStorePages(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.wbSetActive(ctx, true); err != nil {
		t.Fatal(err)
	}
	if srv.settings.String("stylesheet") != wbHelloSlug {
		t.Fatal("not on Hello Pursae")
	}
	gooMakeSimple(t, srv, "Hello Shirt", 12, nil)
	v := gooFrontClient()
	code, body, _ := get(t, v, base+"/shop/")
	if code != http.StatusOK || !strings.Contains(body, "Hello Shirt") || !strings.Contains(body, `class="woocommerce`) || !strings.Contains(body, "hello-pursae-css") {
		t.Fatalf("shop: %d %.400s", code, lastRenderError.Load())
	}
	code, body, _ = get(t, v, base+"/cart/")
	if code != http.StatusOK || !strings.Contains(body, "woocommerce") || !strings.Contains(body, `<main id="content" class="site-main">`) {
		t.Errorf("cart: %d", code)
	}
}
