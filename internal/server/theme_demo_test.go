package server

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

const fixtureLibrary = "testdata/theme-library"

// libraryCopy copies the fixture library to a temp dir and adds a second theme (another category,
// no demo), so the tests can edit versions and break files.
func libraryCopy(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixtureLibrary)); err != nil {
		t.Fatal(err)
	}
	two := filepath.Join(dir, "wpgo-fixture-two")
	if err := os.CopyFS(two, os.DirFS(filepath.Join(fixtureLibrary, "wpgo-fixture"))); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(filepath.Join(two, "demo"))
	editFile(t, filepath.Join(two, "theme.json"), map[string]string{
		`"slug": "wpgo-fixture"`: `"slug": "wpgo-fixture-two"`, `"name": "Fixture Journal"`: `"name": "Fixture Folio"`,
		`"category": "blog"`: `"category": "portfolio"`, `"style": "Quiet serif journal"`: `"style": "Bold grid of work"`,
		`"demo": "demo/demo.json",`: ``, `"version": "1.1.0"`: `"version": "1.0.0"`,
	})
	return dir
}

func editFile(t *testing.T, file string, edits map[string]string) {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for from, to := range edits {
		if !strings.Contains(s, from) {
			t.Fatalf("%s does not contain %q", file, from)
		}
		s = strings.Replace(s, from, to, 1)
	}
	if err := os.WriteFile(file, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestThemeDemoValidation(t *testing.T) {
	lib := libraryCopy(t)
	dir := filepath.Join(lib, "wpgo-fixture")
	b, err := os.ReadFile(filepath.Join(dir, "theme.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := parseThemeManifest(b)
	if err != nil {
		t.Fatal(err)
	}
	if m.Category != "blog" || m.Style != "Quiet serif journal" || m.Demo != "demo/demo.json" {
		t.Fatalf("library metadata: %+v", m)
	}
	if _, err := parseThemeDemo(os.DirFS(dir), m); err != nil {
		t.Fatalf("the fixture demo is rejected: %v", err)
	}
	demoFile := filepath.Join(dir, "demo", "demo.json")
	orig, _ := os.ReadFile(demoFile)
	cases := map[string]struct {
		from, to, want string
	}{
		"version":         {`"version": 1,`, `"version": 2,`, "version must be 1"},
		"option":          {`"posts_per_page": 5`, `"siteurl": "x"`, "options.siteurl may not be set"},
		"posts per page":  {`"posts_per_page": 5`, `"posts_per_page": 0`, "posts_per_page must be an integer"},
		"missing image":   {`"demo/images/cup.png"`, `"demo/images/nope.png"`, `"demo/images/nope.png" is missing`},
		"image outside":   {`"demo/images/cup.png"`, `"assets/screenshot.png"`, "under demo/images/"},
		"bad token":       {`{{post:first-light}}`, `{{post:nowhere}}`, "{{post:nowhere}} does not refer to an item"},
		"unknown media":   {`"featured_media": "cup"`, `"featured_media": "mug"`, `"mug" is not a media id`},
		"unknown cat":     {`"categories": ["kitchen"]`, `"categories": ["garden"]`, `"garden" is not a category`},
		"menu location":   {`"location": "primary"`, `"location": "header"`, `location "header" is not a menu location`},
		"menu item":       {`{"title": "Elsewhere", "url": "https://example.org/"}`, `{"title": "Both", "page": "home", "url": "/x"}`, "exactly one of page, post, category, url"},
		"widget area":     {`"widgets": {"sidebar-1"`, `"widgets": {"sidebar-9"`, "widgets.sidebar-9 is not a widget area"},
		"customizer key":  {`"layout": "grid"`, `"columns": 3`, "customizer.columns is not a Customizer setting"},
		"customizer type": {`"layout": "grid"`, `"layout": "masonry"`, "customizer.layout: must be one of the choices"},
		"checkbox":        {`"show_hero": true`, `"show_hero": "yes"`, "must be true or false"},
		"page template":   {`"template": "page-wide"`, `"template": "page-gone"`, "templates/page-gone.html is missing"},
		"parent cycle":    {`"slug": "about", "title": "About"`, `"slug": "about", "parent": "contact", "title": "About"`, "form a cycle"},
		"future date":     {`"2026-08-01T08:00:00"`, `"2099-08-01T08:00:00"`, "is in the future"},
		"post fields":     {`"posts_page": true`, `"posts_page": true, "sticky": true`, "are for posts"},
		"two front pages": {`"posts_page": true`, `"front_page": true`, "only one front_page"},
		"broken json":     {`"version": 1,`, `"version": 1,,`, "demo/demo.json:"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := string(orig)
			if !strings.Contains(s, tc.from) {
				t.Fatalf("demo.json lacks %q", tc.from)
			}
			if err := os.WriteFile(demoFile, []byte(strings.Replace(s, tc.from, tc.to, 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			defer os.WriteFile(demoFile, orig, 0o644)
			_, err := parseThemeDemo(os.DirFS(dir), m)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error mentioning %q", err, tc.want)
			}
			if !strings.HasPrefix(err.Error(), "demo/demo.json: ") {
				t.Fatalf("error does not name the file: %v", err)
			}
		})
	}
	// The manifest's own fields.
	for _, tc := range [][2]string{
		{`"category": "blog"`, `"category": "cooking"`},
		{`"demo": "demo/demo.json"`, `"demo": "assets/demo.json"`},
		{`"style": "Quiet serif journal"`, `"style": "<b>Bold</b>"`},
	} {
		if _, err := parseThemeManifest([]byte(strings.Replace(string(b), tc[0], tc[1], 1))); err == nil {
			t.Fatalf("manifest with %s accepted", tc[1])
		}
	}
}

func TestThemeLibraryAndDemoContent(t *testing.T) {
	themesDir := t.TempDir()
	t.Setenv("WPGO_THEMES_DIR", themesDir)
	lib := libraryCopy(t)
	ts, c, srv := testServerBuilderOff(t)
	srv.themeLibrary = os.DirFS(lib)
	t.Cleanup(func() {
		unregisterTheme("wpgo-fixture")
		unregisterTheme("wpgo-fixture-two")
		unregisterTheme("wpgo-fixture-bad")
	})
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Library"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK {
		t.Fatalf("install: %d %s", code, body)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	ctx := context.Background()
	must := func(what string, code, want int, body string) {
		t.Helper()
		if code != want {
			t.Fatalf("%s: %d (want %d) %s", what, code, want, body)
		}
	}
	link := func(body, pattern string) string {
		t.Helper()
		m := regexp.MustCompile(`href="(` + pattern + `)"`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no link matching %s", pattern)
		}
		return base + html.UnescapeString(m[1])
	}

	// Library is the default tab: both themes with name, style and category, the category filters.
	code, body, _ := get(t, c, base+"/wp-admin/theme-install")
	must("library", code, http.StatusOK, body)
	for _, want := range []string{`data-slug="wpgo-fixture"`, `data-slug="wpgo-fixture-two"`, "Fixture Journal", "Blog &middot; Quiet serif journal",
		"Portfolio &middot; Bold grid of work", `>Library</a>`, `class="current" aria-current="page">Library`, `All <span class="count">(2)</span>`,
		`category=portfolio"`, `Portfolio <span class="count">(1)</span>`, "Install &amp; Import Demo"} {
		if !strings.Contains(body, want) {
			t.Fatalf("library tab lacks %q", want)
		}
	}
	if strings.Contains(body, `category=travel"`) || strings.Contains(body, `data-slug="twentysixteen"`) {
		t.Fatal("library tab lists an empty category or a bundled theme")
	}
	_, body, _ = get(t, c, base+"/wp-admin/theme-install?browse=library&category=portfolio")
	if !strings.Contains(body, `data-slug="wpgo-fixture-two"`) || strings.Contains(body, `data-slug="wpgo-fixture"`) {
		t.Fatal("category filter")
	}
	_, body, _ = get(t, c, base+"/wp-admin/theme-install?browse=library&search=serif")
	if !strings.Contains(body, `data-slug="wpgo-fixture"`) || strings.Contains(body, `data-slug="wpgo-fixture-two"`) {
		t.Fatal("library search")
	}
	if _, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=all"); !strings.Contains(body, `data-slug="twentysixteen"`) {
		t.Fatal("All tab lost the installed themes")
	}
	if code, body, _ := get(t, c, base+"/wp-admin/theme-install?action=library-screenshot&theme=wpgo-fixture&ver=1.1.0"); code != http.StatusOK || !strings.HasPrefix(body, "\x89PNG") {
		t.Fatalf("library screenshot: %d", code)
	}

	// Install from the library: the same installer as an upload.
	_, body, _ = get(t, c, base+"/wp-admin/theme-install")
	install := link(body, `/wp-admin/theme-install\?action=install-theme&amp;theme=wpgo-fixture-two&amp;_wpnonce=[^"&]+`)
	if code, _, _ := get(t, c, strings.Replace(install, "_wpnonce=", "_wpnonce=x", 1)); code != http.StatusForbidden {
		t.Fatalf("bad nonce: %d", code)
	}
	code, body, loc := get(t, c, install)
	must("install", code, http.StatusFound, body)
	if loc != "/wp-admin/themes?installed=wpgo-fixture-two" {
		t.Fatalf("install redirect %q", loc)
	}
	if _, err := os.Stat(filepath.Join(themesDir, "wpgo-fixture-two", "templates", "index.html")); err != nil {
		t.Fatalf("not extracted: %v", err)
	}
	if th := themeBySlug("wpgo-fixture-two"); th == nil || th.Package == nil || th.Version != "1.0.0" {
		t.Fatalf("not registered: %+v", th)
	}
	_, body, _ = get(t, c, base+"/wp-admin/theme-install")
	if !strings.Contains(body, `action=activate&amp;stylesheet=wpgo-fixture-two`) || strings.Contains(body, `theme=wpgo-fixture-two&amp;_wpnonce`) {
		t.Fatal("installed library theme should offer Activate, not Install")
	}

	// A newer library version: Update.
	editFile(t, filepath.Join(lib, "wpgo-fixture-two", "theme.json"), map[string]string{`"version": "1.0.0"`: `"version": "1.2.0"`})
	_, body, _ = get(t, c, base+"/wp-admin/theme-install")
	if !strings.Contains(body, "New version available.") {
		t.Fatal("no update notice")
	}
	code, body, loc = get(t, c, link(body, `/wp-admin/theme-install\?action=install-theme&amp;theme=wpgo-fixture-two&amp;_wpnonce=[^"&]+`))
	must("update", code, http.StatusFound, body)
	if loc != "/wp-admin/themes?updated=wpgo-fixture-two" || themeBySlug("wpgo-fixture-two").Version != "1.2.0" {
		t.Fatalf("update: %q %s", loc, themeBySlug("wpgo-fixture-two").Version)
	}
	if _, body, _ := get(t, c, base+"/wp-admin/themes?updated=wpgo-fixture-two"); !strings.Contains(body, "Fixture Folio has been updated to version 1.2.0.") {
		t.Fatal("update notice")
	}

	// A library theme with an invalid demo.json is refused with a message naming the problem.
	bad := filepath.Join(lib, "wpgo-fixture-bad")
	if err := os.CopyFS(bad, os.DirFS(filepath.Join(lib, "wpgo-fixture"))); err != nil {
		t.Fatal(err)
	}
	editFile(t, filepath.Join(bad, "theme.json"), map[string]string{`"slug": "wpgo-fixture"`: `"slug": "wpgo-fixture-bad"`})
	editFile(t, filepath.Join(bad, "demo", "demo.json"), map[string]string{`{{post:first-light}}`: `{{post:missing}}`})
	_, body, _ = get(t, c, base+"/wp-admin/theme-install")
	code, _, loc = get(t, c, link(body, `/wp-admin/theme-install\?action=install-theme&amp;theme=wpgo-fixture-bad&amp;_wpnonce=[^"&]+`))
	if code != http.StatusFound || !strings.Contains(loc, "error=") {
		t.Fatalf("bad demo install: %d %q", code, loc)
	}
	msg, _ := url.QueryUnescape(strings.SplitN(loc, "error=", 2)[1])
	if !strings.Contains(msg, "demo/demo.json: pages[1]: {{post:missing}} does not refer to an item of the demo") {
		t.Fatalf("bad demo message: %s", msg)
	}
	if themeBySlug("wpgo-fixture-bad") != nil {
		t.Fatal("theme with a broken demo was installed")
	}
	_ = os.RemoveAll(bad)

	// Install & Import Demo: installed, activated, content imported.
	beforeName := srv.settings.String("blogname")
	_, body, _ = get(t, c, base+"/wp-admin/theme-install")
	code, body, loc = get(t, c, link(body, `/wp-admin/theme-install\?action=install-theme&amp;theme=wpgo-fixture&amp;_wpnonce=[^"&]+&amp;demo=1`))
	must("install and import", code, http.StatusFound, body)
	if !strings.HasPrefix(loc, "/wp-admin/themes?activated=true&counts=4.3.2.4.1.1.2&demo=imported") {
		t.Fatalf("import redirect %q", loc)
	}
	th := themeBySlug("wpgo-fixture")
	if srv.activeTheme().Slug != "wpgo-fixture" || th == nil {
		t.Fatalf("active theme %s", srv.activeTheme().Slug)
	}
	_, body, _ = get(t, c, base+loc)
	for _, want := range []string{"Demo content imported: 4 pages, 3 posts, 2 images, 4 categories and tags, 1 comment, 1 menu, 2 widgets.",
		`"wpgoDemoRemove":"/wp-admin/themes?action=remove-demo`, "Remove Demo Content"} {
		if !strings.Contains(body, want) {
			t.Fatalf("themes screen lacks %q", want)
		}
	}
	if strings.Contains(body, "This theme comes with demo content.") {
		t.Fatal("import offered again after importing")
	}

	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := srv.store.Pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	demoCounts := func() [4]int {
		return [4]int{
			count(`SELECT count(*) FROM posts WHERE type = 'page' AND meta->>'_wpgo_demo' = 'wpgo-fixture'`),
			count(`SELECT count(*) FROM posts WHERE type = 'post' AND meta->>'_wpgo_demo' = 'wpgo-fixture'`),
			count(`SELECT count(*) FROM media WHERE meta->>'_wpgo_demo' = 'wpgo-fixture'`),
			count(`SELECT count(*) FROM terms WHERE meta->>'_wpgo_demo' = 'wpgo-fixture'`),
		}
	}
	if got := demoCounts(); got != [4]int{4, 3, 2, 4} {
		t.Fatalf("imported items %v", got)
	}
	page := func(ref string) int64 {
		t.Helper()
		id := srv.demoItemID(ctx, "posts", "page", "wpgo-fixture", ref)
		if id == 0 {
			t.Fatalf("page %s missing", ref)
		}
		return id
	}
	home, _ := srv.store.GetPost(ctx, page("home"))
	about, contact := page("about"), page("contact")
	firstID := srv.demoItemID(ctx, "posts", "post", "wpgo-fixture", "first-light")
	first, err := srv.store.GetPost(ctx, firstID)
	if err != nil {
		t.Fatal(err)
	}
	heroID := srv.demoItemID(ctx, "media", "", "wpgo-fixture", "hero")
	hero, err := srv.store.GetAttachment(ctx, heroID)
	if err != nil || len(hero.Variants) == 0 || hero.Alt != "A warm gradient sky" {
		t.Fatalf("hero attachment: %v %+v", err, hero)
	}
	lb := srv.links(ctx)
	travel, _ := srv.store.TermBySlug(ctx, "category", "travel")
	coastal, _ := srv.store.TermBySlug(ctx, "category", "coastal")
	if travel == nil || coastal == nil || coastal.ParentID != travel.ID {
		t.Fatalf("categories: %+v %+v", travel, coastal)
	}
	for _, want := range []string{`href="` + lb.Permalink(first) + `"`, `href="` + lb.TermLink(travel) + `"`, srv.uploadURL(ctx, hero.FilePath),
		`"id":` + itoa64(heroID), "wp-image-" + itoa64(heroID)} {
		if !strings.Contains(home.Content, want) {
			t.Fatalf("home content lacks %q: %s", want, home.Content)
		}
	}
	if strings.Contains(home.Content, "{{") || home.FeaturedMediaID != heroID || home.Meta["_wp_page_template"] != "page-wide" {
		t.Fatalf("home page: %+v", home)
	}
	if p, _ := srv.store.GetPost(ctx, contact); p.ParentID != about {
		t.Fatalf("contact parent %d, want %d", p.ParentID, about)
	}
	if !first.Sticky || first.FeaturedMediaID != heroID || first.CommentCount != 1 || first.Excerpt != "Morning on the coast." ||
		first.Date().In(srv.loc()).Format("2006-01-02 15:04") != "2026-09-12 09:30" || first.Status != "publish" {
		t.Fatalf("first post: %+v", first)
	}
	if n := count(`SELECT count(*) FROM post_revisions WHERE post_id = $1`, home.ID); n != 0 {
		t.Fatalf("import left %d revisions", n)
	}
	if n := count(`SELECT count(*) FROM posts WHERE format = 'aside' AND meta->>'_wpgo_demo' = 'wpgo-fixture'`); n != 1 {
		t.Fatal("post format")
	}
	if n := count(`SELECT count(*) FROM post_terms pt JOIN terms t ON t.id = pt.term_id WHERE pt.post_id = $1 AND t.slug IN ('coastal','coffee')`, firstID); n != 2 {
		t.Fatalf("first post terms: %d", n)
	}
	if srv.settings.String("show_on_front") != "page" || int64(srv.settings.Int("page_on_front")) != home.ID ||
		int64(srv.settings.Int("page_for_posts")) != page("blog") || srv.settings.String("blogname") != "Fixture Journal" || srv.settings.Int("posts_per_page") != 5 {
		t.Fatal("reading settings and options not set")
	}
	if n := count(`SELECT count(*) FROM menus m JOIN menu_items i ON i.menu_id = m.id WHERE m.location = 'primary' AND m.name = 'Main menu'`); n != 6 {
		t.Fatalf("menu items %d", n)
	}
	if n := count(`SELECT count(*) FROM menu_items WHERE label = 'Contact' AND parent_id IS NOT NULL`); n != 1 {
		t.Fatal("nested menu item")
	}
	if n := count(`SELECT count(*) FROM menu_items WHERE label = 'About' AND kind = 'post_type' AND target_id = $1`, about); n != 1 {
		t.Fatal("menu item title from the page")
	}
	rec := srv.demoRecord("wpgo-fixture")
	if rec == nil || !rec.Complete || len(rec.Widgets) != 2 || len(rec.Menus) != 1 {
		t.Fatalf("record %+v", rec)
	}
	widgets, _ := srv.store.ThemeWidgets(ctx, "wpgo-fixture")
	var sidebar []string
	for _, w := range widgets {
		if w.AreaSlug == "sidebar-1" {
			sidebar = append(sidebar, w.Settings["content"].(string))
		}
	}
	if len(sidebar) != 2 || sidebar[0] != "<!-- wp:search /-->" || !strings.Contains(sidebar[1], `href="`+lb.Permalink(mustPost(t, srv, about))+`"`) {
		t.Fatalf("sidebar widgets %q", sidebar)
	}
	mods := srv.themeMods("wpgo-fixture")
	if mods["accent"] != "#c2410c" || mods["layout"] != "grid" || mods["show_hero"] != true || int64(modInt(mods, "hero_image", 0)) != heroID {
		t.Fatalf("theme mods %v", mods)
	}

	// The site: the front page uses the page's template, the menu and the accent color.
	code, body, _ = get(t, c, base+"/")
	must("front", code, http.StatusOK, body)
	for _, want := range []string{"fixture-page-wide", "--fixture-accent:#c2410c", "Fixture Journal"} {
		if !strings.Contains(body, want) {
			t.Fatalf("front page lacks %q", want)
		}
	}
	code, body, _ = get(t, c, lb.Permalink(first))
	must("post", code, http.StatusOK, body)
	if !strings.Contains(body, "Elsewhere") || !strings.Contains(body, "See <a href=") {
		t.Fatal("menu or widget missing on the post page")
	}

	// REST: library metadata on the themes endpoint.
	_, body, _ = get(t, c, base+"/wp-json/wp/v2/themes/wpgo-fixture")
	for _, want := range []string{`"category":"blog"`, `"style":"Quiet serif journal"`, `"has_demo":true`} {
		if !strings.Contains(body, want) {
			t.Fatalf("rest theme lacks %q: %s", want, body)
		}
	}

	// A second import is a no-op.
	rep, err := srv.importThemeDemo(ctx, 1, th)
	if err != nil || *rep != (demoReport{}) {
		t.Fatalf("second import: %v %+v", err, rep)
	}
	if got := demoCounts(); got != [4]int{4, 3, 2, 4} || count(`SELECT count(*) FROM menus`) != 1 || len(srv.demoRecord("wpgo-fixture").Widgets) != 2 {
		t.Fatalf("second import created items: %v", got)
	}

	// A post the user wrote in a demo category survives removal (in the default category).
	own := &store.Post{Type: "post", AuthorID: 1, Title: "Mine", Status: "publish", CommentStatus: "open", PingStatus: "open", Format: "standard", Meta: map[string]any{}}
	if err := srv.store.InsertPost(ctx, own); err != nil {
		t.Fatal(err)
	}
	_ = srv.store.SetPostTerms(ctx, own.ID, "category", []int64{travel.ID})
	ownID := own.ID

	// Remove Demo Content: everything imported goes, the settings come back.
	_, body, _ = get(t, c, base+"/wp-admin/themes")
	m := regexp.MustCompile(`"wpgoDemoRemove":"([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no remove URL")
	}
	removeURL := base + strings.ReplaceAll(m[1], `\u0026`, "&")
	code, body, loc = get(t, c, removeURL)
	must("remove", code, http.StatusFound, body)
	if loc != "/wp-admin/themes?demo=removed" {
		t.Fatalf("remove redirect %q", loc)
	}
	if got := demoCounts(); got != [4]int{} {
		t.Fatalf("left after removal %v", got)
	}
	if _, err := os.Stat(filepath.Join(srv.cfg.UploadsDir, filepath.FromSlash(hero.FilePath))); !os.IsNotExist(err) {
		t.Fatal("image file not deleted")
	}
	if count(`SELECT count(*) FROM menus`) != 0 || count(`SELECT count(*) FROM widgets WHERE id = ANY($1)`, rec.Widgets) != 0 {
		t.Fatal("menus or widgets left")
	}
	for key, area := range rec.Moved {
		if count(`SELECT count(*) FROM widgets WHERE id = $1 AND area_id = $2`, key, area) != 1 {
			t.Fatalf("moved widget %s not restored", key)
		}
	}
	if srv.settings.String("show_on_front") != "posts" || srv.settings.Int("page_on_front") != 0 || srv.settings.Int("page_for_posts") != 0 ||
		srv.settings.String("blogname") != beforeName || srv.settings.Int("posts_per_page") != 10 {
		t.Fatalf("settings not restored: %s %d %q", srv.settings.String("show_on_front"), srv.settings.Int("page_on_front"), srv.settings.String("blogname"))
	}
	if mods := srv.themeMods("wpgo-fixture"); len(mods) != 0 {
		t.Fatalf("theme mods left %v", mods)
	}
	if _, err := srv.store.GetPost(ctx, ownID); err != nil {
		t.Fatal("the user's own post was deleted")
	}
	if srv.demoImported("wpgo-fixture") {
		t.Fatal("still marked imported")
	}
	_, body, _ = get(t, c, base+"/wp-admin/themes?demo=removed")
	if !strings.Contains(body, "Demo content removed.") || !strings.Contains(body, `"wpgoDemoImport":"/wp-admin/themes?action=import-demo`) {
		t.Fatal("themes screen after removal")
	}
	// Activating a theme with demo content offers to import it.
	_, body, _ = get(t, c, base+"/wp-admin/themes?activated=true")
	if !strings.Contains(body, "This theme comes with demo content. Import it?") {
		t.Fatal("no import notice after activation")
	}
}

func mustPost(t *testing.T, srv *Server, id int64) *store.Post {
	t.Helper()
	p, err := srv.store.GetPost(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
