package server

import (
	"context"
	"encoding/json"
	"fmt"
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

// Theme library, group h: Velvet Salon, Paws & Whiskers, Gearhead Garage, Driftwood Inn and
// Greenleaf Market. Each package is zipped from web/theme-library/<slug>, uploaded through
// Appearance › Add Themes › Upload, activated and rendered for every kind of request, then its
// demo content is imported with the real importer and the imported site is rendered too.

var libHThemes = []string{"velvet-salon", "paws-and-whiskers", "gearhead-garage", "driftwood-inn", "greenleaf-market"}

const libHDir = "../../web/theme-library"

type libHDemo struct {
	Version    int                 `json:"version"`
	Options    map[string]any      `json:"options"`
	Categories []libHTerm          `json:"categories"`
	Tags       []libHTerm          `json:"tags"`
	Media      []libHMedia         `json:"media"`
	Pages      []libHItem          `json:"pages"`
	Posts      []libHItem          `json:"posts"`
	Menus      []libHMenu          `json:"menus"`
	Widgets    map[string][]string `json:"widgets"`
	Customizer map[string]any      `json:"customizer"`
}

type libHTerm struct{ Slug, Name, Description string }

type libHMedia struct{ ID, File, Alt string }

type libHItem struct {
	Slug          string        `json:"slug"`
	Title         string        `json:"title"`
	Content       string        `json:"content"`
	Excerpt       string        `json:"excerpt"`
	Date          string        `json:"date"`
	FeaturedMedia string        `json:"featured_media"`
	Template      string        `json:"template"`
	Parent        string        `json:"parent"`
	FrontPage     bool          `json:"front_page"`
	PostsPage     bool          `json:"posts_page"`
	Categories    []string      `json:"categories"`
	Tags          []string      `json:"tags"`
	Comments      []libHComment `json:"comments"`
}

type libHComment struct{ Author, Content, Date string }

type libHMenu struct {
	Name     string         `json:"name"`
	Location string         `json:"location"`
	Items    []libHMenuItem `json:"items"`
}

type libHMenuItem struct {
	Title    string         `json:"title"`
	Page     string         `json:"page"`
	Post     string         `json:"post"`
	Category string         `json:"category"`
	URL      string         `json:"url"`
	Children []libHMenuItem `json:"children"`
}

var libHTokenRe = regexp.MustCompile(`\{\{(media|media_id|page|post|category|tag):([^}]*)\}\}`)

// libHCheckPackage validates a library directory offline: manifest, Customizer, assets and the
// demo file, first with the real parser and then for the library's own quality rules.
func libHCheckPackage(t *testing.T, slug string) {
	t.Helper()
	dir := filepath.Join(libHDir, slug)
	mb, err := os.ReadFile(filepath.Join(dir, "theme.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := parseThemeManifest(mb)
	if err != nil {
		t.Fatalf("theme.json: %v", err)
	}
	if m.Slug != slug || m.Version != "1.0.0" || m.RequiresCore != "0.1.0" || m.Author != "WordPress Go" {
		t.Errorf("manifest identity: %s %s %s %s", m.Slug, m.Version, m.RequiresCore, m.Author)
	}
	var meta struct{ Category, Style, Demo string }
	_ = json.Unmarshal(mb, &meta)
	if themeCategoryLabel(meta.Category) == "" || meta.Style == "" || len(meta.Style) > 80 || meta.Demo != "demo/demo.json" {
		t.Fatalf("library metadata: %+v", meta)
	}

	// The real demo parser (the installer runs the same one).
	if _, err := parseThemeDemo(os.DirFS(dir), m); err != nil {
		t.Fatalf("parseThemeDemo: %v", err)
	}

	// Customizer: 8+ settings in 2–4 sections; every setting changes the site somehow.
	settings := map[string]themeCzSettings{}
	for _, sec := range m.Customizer.Sections {
		for _, st := range sec.Settings {
			settings[st.ID] = st
		}
	}
	if len(settings) < 8 || len(m.Customizer.Sections) < 2 || len(m.Customizer.Sections) > 4 {
		t.Errorf("%d settings in %d sections, want 8+ in 2–4", len(settings), len(m.Customizer.Sections))
	}
	var tpl strings.Builder
	files, _ := filepath.Glob(filepath.Join(dir, "templates", "*.html"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		tpl.Write(b)
	}
	css, err := os.ReadFile(filepath.Join(dir, "assets", "style.css"))
	if err != nil {
		t.Fatal(err)
	}
	for id, st := range settings {
		switch {
		case st.CSSVar != "":
			if !strings.Contains(string(css), "var("+st.CSSVar+")") {
				t.Errorf("setting %s: %s is not used by style.css", id, st.CSSVar)
			}
		case st.BodyClass != "":
			if strings.Contains(st.BodyClass, "{value}") {
				for _, ch := range st.Choices {
					cls := strings.ReplaceAll(st.BodyClass, "{value}", ch[0])
					if !strings.Contains(string(css), "."+cls) && ch[0] != fmt.Sprint(st.Default) {
						t.Errorf("setting %s: choice %q has no .%s rule", id, ch[0], cls)
					}
				}
			} else if !strings.Contains(string(css), "."+st.BodyClass) {
				t.Errorf("setting %s: no .%s rule", id, st.BodyClass)
			}
		default:
			if !strings.Contains(tpl.String(), `"`+id+`"`) {
				t.Errorf("setting %s is not used by any template", id)
			}
		}
	}

	// Fonts stay small.
	fonts, _ := filepath.Glob(filepath.Join(dir, "assets", "fonts", "*.woff2"))
	var fontBytes int64
	for _, f := range fonts {
		st, _ := os.Stat(f)
		fontBytes += st.Size()
	}
	if fontBytes > 300<<10 {
		t.Errorf("fonts weigh %d bytes", fontBytes)
	}

	// The demo, item by item.
	b, err := os.ReadFile(filepath.Join(dir, meta.Demo))
	if err != nil {
		t.Fatal(err)
	}
	var d libHDemo
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("demo.json: %v", err)
	}
	if d.Version != 1 || d.Options["blogname"] == nil || d.Options["blogdescription"] == nil {
		t.Errorf("demo version/options: %d %v", d.Version, d.Options)
	}
	ids := map[string]map[string]bool{"media": {}, "media_id": {}, "page": {}, "post": {}, "category": {}, "tag": {}}
	for _, md := range d.Media {
		ids["media"][md.ID], ids["media_id"][md.ID] = true, true
		st, err := os.Stat(filepath.Join(dir, md.File))
		switch {
		case err != nil:
			t.Errorf("media %s: %v", md.ID, err)
		case st.Size() > 400<<10:
			t.Errorf("media %s: %d bytes", md.ID, st.Size())
		}
		if md.Alt == "" {
			t.Errorf("media %s has no alt text", md.ID)
		}
	}
	if n := len(d.Media); n < 10 || n > 16 {
		t.Errorf("%d images, want 10–16", n)
	}
	if imgs, _ := filepath.Glob(filepath.Join(dir, "demo", "images", "*")); len(imgs) != len(d.Media) {
		t.Errorf("%d files in demo/images, %d media entries", len(imgs), len(d.Media))
	}
	for _, c := range d.Categories {
		ids["category"][c.Slug] = true
	}
	for _, tg := range d.Tags {
		ids["tag"][tg.Slug] = true
	}
	for _, pg := range d.Pages {
		ids["page"][pg.Slug] = true
	}
	for _, ps := range d.Posts {
		ids["post"][ps.Slug] = true
	}
	if n := len(d.Posts); n < 6 || n > 10 {
		t.Errorf("%d posts, want 6–10", n)
	}
	if n := len(d.Pages); n < 3 || n > 8 {
		t.Errorf("%d pages, want 3–8", n)
	}
	refs := func(where, s string) {
		for _, r := range libHTokenRe.FindAllStringSubmatch(s, -1) {
			if !ids[r[1]][r[2]] {
				t.Errorf("%s: %s refers to nothing", where, r[0])
			}
		}
		if strings.Contains(strings.ToLower(s), "lorem") {
			t.Errorf("%s: lorem ipsum", where)
		}
	}
	front, blog, comments := 0, 0, 0
	for _, pg := range d.Pages {
		refs("page "+pg.Slug, pg.Content)
		if pg.FeaturedMedia != "" && !ids["media"][pg.FeaturedMedia] {
			t.Errorf("page %s: featured %q", pg.Slug, pg.FeaturedMedia)
		}
		if pg.Parent != "" && !ids["page"][pg.Parent] {
			t.Errorf("page %s: parent %q", pg.Slug, pg.Parent)
		}
		if pg.Template != "" {
			if _, err := os.Stat(filepath.Join(dir, "templates", pg.Template+".html")); err != nil {
				t.Errorf("page %s: template %q", pg.Slug, pg.Template)
			}
		}
		if pg.FrontPage {
			front++
		}
		if pg.PostsPage {
			blog++
		}
	}
	if front != 1 || blog != 1 {
		t.Errorf("%d front pages and %d posts pages, want one each", front, blog)
	}
	for _, ps := range d.Posts {
		refs("post "+ps.Slug, ps.Content)
		if ps.Excerpt == "" || ps.Date == "" || ps.FeaturedMedia == "" || len(ps.Categories) == 0 {
			t.Errorf("post %s: excerpt, date, image and category are expected", ps.Slug)
		}
		if ps.Date > "2026-10-28" {
			t.Errorf("post %s is dated in the future: %s", ps.Slug, ps.Date)
		}
		if n := strings.Count(ps.Content, "<!-- wp:paragraph"); n < 3 {
			t.Errorf("post %s: %d paragraphs", ps.Slug, n)
		}
		if !strings.Contains(ps.Content, "<!-- wp:heading") && !strings.Contains(ps.Content, "<!-- wp:list") {
			t.Errorf("post %s: no headings or lists", ps.Slug)
		}
		for _, c := range ps.Categories {
			if !ids["category"][c] {
				t.Errorf("post %s: category %q", ps.Slug, c)
			}
		}
		for _, tg := range ps.Tags {
			if !ids["tag"][tg] {
				t.Errorf("post %s: tag %q", ps.Slug, tg)
			}
		}
		comments += len(ps.Comments)
	}
	if comments < 2 {
		t.Errorf("%d comments", comments)
	}
	hasSub := false
	var walk func(items []libHMenuItem, depth int)
	walk = func(items []libHMenuItem, depth int) {
		for _, it := range items {
			if depth > 1 {
				hasSub = true
			}
			switch {
			case it.Page != "" && !ids["page"][it.Page], it.Post != "" && !ids["post"][it.Post], it.Category != "" && !ids["category"][it.Category]:
				t.Errorf("menu item %+v refers to nothing", it)
			}
			walk(it.Children, depth+1)
		}
	}
	locs := map[string]bool{}
	for _, mn := range d.Menus {
		locs[mn.Location] = true
		walk(mn.Items, 1)
	}
	if !hasSub || !locs["primary"] || !locs["social"] {
		t.Errorf("menus: submenu %v, locations %v", hasSub, locs)
	}
	for area, ws := range d.Widgets {
		for _, w := range ws {
			refs("widget "+area, w)
		}
	}
	if len(d.Widgets["sidebar-1"]) == 0 || len(d.Widgets["footer-1"]) == 0 {
		t.Error("both widget areas should get demo widgets")
	}
	for k, v := range d.Customizer {
		if _, ok := settings[k]; !ok {
			t.Errorf("customizer %s is not a setting", k)
		}
		if s, ok := v.(string); ok {
			refs("customizer "+k, s)
		}
	}
}

func TestThemeLibraryHPackages(t *testing.T) {
	for _, slug := range libHThemes {
		t.Run(slug, func(t *testing.T) { libHCheckPackage(t, slug) })
	}
}

func TestThemeLibraryH(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WPGO_THEMES_DIR", dir)
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Library"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK {
		t.Fatalf("install: %d %s", code, body)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	t.Cleanup(func() {
		for _, slug := range libHThemes {
			unregisterTheme(slug)
		}
	})

	lb := srv.links(ctx)
	abs := func(u string) string {
		if strings.HasPrefix(u, "http") {
			return u
		}
		return base + u
	}
	hello, err := srv.store.PostBySlug(ctx, "post", "hello-world")
	if err != nil {
		t.Fatal(err)
	}
	sample, err := srv.store.PostBySlug(ctx, "page", "sample-page")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := srv.store.TermBySlug(ctx, "category", "uncategorized")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.InsertComment(ctx, &store.Comment{PostID: hello.ID, AuthorName: "Visitor", AuthorEmail: "v@example.test",
		Content: "A first comment.", Status: "approved"}); err != nil {
		t.Fatal(err)
	}
	plain := map[string]string{
		"single":   abs(lb.Permalink(hello)),
		"page":     abs(lb.Permalink(sample)),
		"category": abs(lb.TermLink(cat)),
		"search":   base + "/?s=hello",
	}

	_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
	nonce := formNonce(t, body)

	for _, slug := range libHThemes {
		t.Run(slug, func(t *testing.T) {
			pkg := zipDir(t, filepath.Join(libHDir, slug))
			code, body, loc := postZip(t, c, base+"/wp-admin/theme-install", nonce, "themezip", slug+".zip", pkg)
			if code != http.StatusFound || loc != "/wp-admin/themes?installed="+slug {
				t.Fatalf("upload: %d %q %s", code, loc, body)
			}
			th := themeBySlug(slug)
			if th == nil || th.Package == nil {
				t.Fatalf("%s not registered", slug)
			}
			_, body, _ = get(t, c, base+"/wp-admin/themes")
			m := regexp.MustCompile(`href="(/wp-admin/themes\?action=activate&amp;stylesheet=` + slug + `&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
			if m == nil {
				t.Fatalf("no activate link for %s", slug)
			}
			if code, _, _ := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound {
				t.Fatalf("activate: %d", code)
			}
			if srv.activeTheme().Slug != slug {
				t.Fatalf("active theme %s", srv.activeTheme().Slug)
			}

			styleLink := fmt.Sprintf(`id='%s-style-css' href='/wp-content/themes/%s/style.css?ver=1.0.0'`, slug, slug)
			check := func(what, u string, want int, marks ...string) string {
				t.Helper()
				code, body, _ := get(t, c, u)
				if code != want {
					t.Fatalf("%s %s: %d (want %d)", what, u, code, want)
				}
				if strings.Contains(body, "Template error") || strings.Contains(body, "template: ") {
					t.Fatalf("%s: template error in %s", what, body)
				}
				for _, mk := range append([]string{styleLink, `class="site-header"`, `id="colophon"`, "/js/navigation.js?ver=1.0.0", `class="skip-link`}, marks...) {
					if !strings.Contains(body, mk) {
						t.Fatalf("%s lacks %q", what, mk)
					}
				}
				return body
			}

			// A fresh site: blog on the front page, the install samples.
			srv.purgeAll(ctx)
			body = check("home", base+"/", http.StatusOK, `class="post-list"`, "hfeed", hello.Title, "page-header--blog")
			check("single", plain["single"], http.StatusOK, "entry--single", `class="comments-area"`, "A first comment.")
			check("page", plain["page"], http.StatusOK, "entry--page")
			check("category", plain["category"], http.StatusOK, "page-header--archive")
			check("search", plain["search"], http.StatusOK, "page-header--search")
			check("404", base+"/wpgo-no-such-page-at-all/", http.StatusNotFound, "error-404")

			// Every css_var setting is printed with its default; every body_class setting adds its class.
			bodyTag := regexp.MustCompile(`<body class="([^"]*)"`).FindStringSubmatch(body)
			if bodyTag == nil {
				t.Fatal("no body class")
			}
			classes := " " + bodyTag[1] + " "
			for _, st := range th.Package.settings {
				def, _ := themeCzDefault(st)
				if st.CSSVar != "" && !strings.Contains(body, st.CSSVar+":"+def+";") {
					t.Errorf("css var %s:%s not printed", st.CSSVar, def)
				}
				if st.BodyClass != "" && (strings.Contains(st.BodyClass, "{value}") || def == "1") {
					cls := strings.ReplaceAll(st.BodyClass, "{value}", def)
					if !strings.Contains(classes, " "+cls+" ") {
						t.Errorf("body class %q missing from %q", cls, bodyTag[1])
					}
				}
			}

			// Assets.
			for _, a := range append([]string{"style.css", "editor-style.css", "js/navigation.js", "screenshot.png"}, th.PreloadFonts...) {
				if code, _, _ := get(t, c, base+"/wp-content/themes/"+slug+"/"+strings.TrimPrefix(a, "assets/")); code != http.StatusOK {
					t.Errorf("asset %s: %d", a, code)
				}
			}

			// Import the demo with the real importer and render the imported site.
			rep, err := srv.importThemeDemo(ctx, 1, th)
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			b, _ := os.ReadFile(filepath.Join(libHDir, slug, "demo", "demo.json"))
			var d libHDemo
			_ = json.Unmarshal(b, &d)
			if rep.Posts != len(d.Posts) || rep.Pages != len(d.Pages) || rep.Media != len(d.Media) || rep.Menus != len(d.Menus) {
				t.Fatalf("import report %+v", rep)
			}
			srv.purgeAll(ctx)
			var frontTitle, blogSlug, fullPage string
			for _, pg := range d.Pages {
				switch {
				case pg.FrontPage:
					frontTitle = pg.Title
				case pg.PostsPage:
					blogSlug = pg.Slug
				case pg.Template == "page-full" && fullPage == "":
					fullPage = pg.Slug
				}
			}
			_ = frontTitle
			body = check("demo front", base+"/", http.StatusOK, "entry--front", `class="hero`, "site-main--front", `id="site-navigation"`, `class="social-navigation"`)
			if !strings.Contains(body, html.EscapeString(fmt.Sprint(d.Options["blogname"]))) {
				t.Errorf("front page lacks the demo site name")
			}
			blogPage, err := srv.store.PostBySlug(ctx, "page", blogSlug)
			if err != nil {
				t.Fatal(err)
			}
			check("demo blog", abs(lb.Permalink(blogPage)), http.StatusOK, `class="post-list"`, "page-header--blog", d.Posts[0].Title)
			for i, ps := range d.Posts {
				p, err := srv.store.PostBySlug(ctx, "post", ps.Slug)
				if err != nil {
					t.Fatalf("post %s: %v", ps.Slug, err)
				}
				marks := []string{"entry--single", "post-thumbnail", `id="secondary"`}
				if len(ps.Comments) > 0 {
					marks = append(marks, html.EscapeString(ps.Comments[0].Author))
				}
				if i < 3 || len(ps.Comments) > 0 {
					check("demo post "+ps.Slug, abs(lb.Permalink(p)), http.StatusOK, marks...)
				}
			}
			for _, pg := range d.Pages {
				if pg.FrontPage || pg.PostsPage {
					continue
				}
				p, err := srv.store.PostBySlug(ctx, "page", pg.Slug)
				if err != nil {
					t.Fatalf("page %s: %v", pg.Slug, err)
				}
				check("demo page "+pg.Slug, abs(lb.Permalink(p)), http.StatusOK, "entry--page")
			}
			if fullPage == "" {
				t.Error("no demo page uses the page-full template")
			}
			term, err := srv.store.TermBySlug(ctx, "category", d.Categories[0].Slug)
			if err != nil {
				t.Fatal(err)
			}
			check("demo category", abs(lb.TermLink(term)), http.StatusOK, "page-header--archive", `class="card`)
			word := strings.Fields(d.Posts[0].Title)[len(strings.Fields(d.Posts[0].Title))-1]
			check("demo search", base+"/?s="+url.QueryEscape(word), http.StatusOK, "page-header--search", `class="card`)
			check("demo 404", base+"/wpgo-no-such-page-at-all/", http.StatusNotFound, "error-404")

			// Remove the demo so the next theme starts from the install samples again.
			if err := srv.removeThemeDemo(ctx, slug); err != nil {
				t.Fatalf("remove demo: %v", err)
			}
			srv.purgeAll(ctx)
		})
	}
}
