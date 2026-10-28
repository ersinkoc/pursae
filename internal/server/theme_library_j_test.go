package server

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Theme library, group j: Faithful Commons, Green Pulse, Pageturner, Summit Trails and Atlas Academy.
// Each package is validated with the real manifest and demo parsers, zipped from
// web/theme-library/<slug>, uploaded through Appearance › Add Themes › Upload, activated, rendered for
// every kind of request, and then its demo content is imported and every imported page, post and
// category is rendered.

var libJThemes = []string{"faithful-commons", "green-pulse", "pageturner", "summit-trails", "atlas-academy"}

const libJDir = "../../web/theme-library"

// libJDemo is the part of demo/demo.json the offline checks read (docs/theme-package.md, "Demo content").
type libJDemo struct {
	Version    int            `json:"version"`
	Options    map[string]any `json:"options"`
	Categories []struct {
		Slug, Name, Description, Parent string
	} `json:"categories"`
	Tags  []struct{ Slug, Name string }             `json:"tags"`
	Media []struct{ ID, File, Alt, Caption string } `json:"media"`
	Pages []libJDemoItem                            `json:"pages"`
	Posts []libJDemoItem                            `json:"posts"`
	Menus []struct {
		Name, Location string
		Items          []libJMenuItem `json:"items"`
	} `json:"menus"`
	Widgets    map[string][]string `json:"widgets"`
	Customizer map[string]any      `json:"customizer"`
}

type libJDemoItem struct {
	Slug          string   `json:"slug"`
	Title         string   `json:"title"`
	Content       string   `json:"content"`
	Excerpt       string   `json:"excerpt"`
	Date          string   `json:"date"`
	Parent        string   `json:"parent"`
	Template      string   `json:"template"`
	FeaturedMedia string   `json:"featured_media"`
	FrontPage     bool     `json:"front_page"`
	PostsPage     bool     `json:"posts_page"`
	Categories    []string `json:"categories"`
	Tags          []string `json:"tags"`
	Comments      []struct {
		Author, Email, Content, Date string
	} `json:"comments"`
}

type libJMenuItem struct {
	Title    string         `json:"title"`
	Page     string         `json:"page"`
	Post     string         `json:"post"`
	Category string         `json:"category"`
	URL      string         `json:"url"`
	Children []libJMenuItem `json:"children"`
}

var libJToken = regexp.MustCompile(`\{\{(media|media_id|page|post|category|tag):([^}]*)\}\}`)

// libJCheckDemo validates demo/demo.json with the real parser and checks offline that every image,
// page, post, category, tag and media id it references exists.
func libJCheckDemo(t *testing.T, dir string, m *themeManifest) *libJDemo {
	t.Helper()
	if _, err := parseThemeDemo(os.DirFS(dir), m); err != nil {
		t.Fatalf("parseThemeDemo: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "demo", "demo.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d libJDemo
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("demo.json: %v", err)
	}
	if d.Version != 1 || d.Options["blogname"] == nil || d.Options["blogdescription"] == nil {
		t.Fatalf("demo.json: version %d, options %v", d.Version, d.Options)
	}
	if n := len(d.Posts); n < 6 || n > 10 {
		t.Errorf("%d demo posts, want 6–10", n)
	}
	if n := len(d.Pages); n < 3 || n > 7 {
		t.Errorf("%d demo pages, want 3–6 plus a posts page", n)
	}
	if n := len(d.Media); n < 10 || n > 16 {
		t.Errorf("%d demo images, want 10–16", n)
	}
	ids := map[string]map[string]bool{"media": {}, "page": {}, "post": {}, "category": {}, "tag": {}}
	files, _ := filepath.Glob(filepath.Join(dir, "demo", "images", "*"))
	if len(files) != len(d.Media) {
		t.Errorf("%d files in demo/images but %d media entries", len(files), len(d.Media))
	}
	for _, md := range d.Media {
		ids["media"][md.ID] = true
		if !strings.HasPrefix(md.File, "demo/images/") || md.Alt == "" {
			t.Errorf("media %s: file %q, alt %q", md.ID, md.File, md.Alt)
		}
		st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(md.File)))
		if err != nil {
			t.Errorf("media %s: %v", md.ID, err)
		} else if st.Size() > 400<<10 {
			t.Errorf("media %s is %d bytes, want ≤ 400 KB", md.ID, st.Size())
		}
	}
	for _, c := range d.Categories {
		ids["category"][c.Slug] = true
	}
	for _, tg := range d.Tags {
		ids["tag"][tg.Slug] = true
	}
	for _, p := range d.Pages {
		ids["page"][p.Slug] = true
	}
	for _, p := range d.Posts {
		ids["post"][p.Slug] = true
	}
	has := func(kind, id, where string) {
		t.Helper()
		if !ids[kind][id] {
			t.Errorf("%s references the unknown %s %q", where, kind, id)
		}
	}
	for _, c := range d.Categories {
		if c.Parent != "" {
			has("category", c.Parent, "category "+c.Slug)
		}
	}
	for _, p := range append(append([]libJDemoItem{}, d.Pages...), d.Posts...) {
		if p.FeaturedMedia != "" {
			has("media", p.FeaturedMedia, p.Slug)
		}
		if p.Parent != "" {
			has("page", p.Parent, p.Slug)
		}
		if p.Template != "" {
			if _, err := os.Stat(filepath.Join(dir, "templates", p.Template+".html")); err != nil {
				t.Errorf("page %s: template %s: %v", p.Slug, p.Template, err)
			}
		}
		for _, c := range p.Categories {
			has("category", c, p.Slug)
		}
		for _, tg := range p.Tags {
			has("tag", tg, p.Slug)
		}
		if strings.Contains(strings.ToLower(p.Content), "lorem ipsum") {
			t.Errorf("%s: lorem ipsum", p.Slug)
		}
	}
	for _, p := range d.Posts {
		if n := strings.Count(p.Content, "<!-- wp:paragraph"); n < 3 || p.Excerpt == "" || len(p.Categories) == 0 {
			t.Errorf("post %s is too thin (%d paragraphs, excerpt or categories missing)", p.Slug, n)
		}
		if dt, err := time.Parse("2006-01-02T15:04:05", p.Date); err != nil || dt.After(time.Now()) {
			t.Errorf("post %s: date %q is invalid or in the future", p.Slug, p.Date)
		}
	}
	for _, tk := range libJToken.FindAllStringSubmatch(string(raw), -1) {
		has(strings.TrimSuffix(tk[1], "_id"), tk[2], "a token")
	}
	var walk func(items []libJMenuItem)
	walk = func(items []libJMenuItem) {
		for _, it := range items {
			switch {
			case it.Page != "":
				has("page", it.Page, "menu item "+it.Title)
			case it.Post != "":
				has("post", it.Post, "menu item "+it.Title)
			case it.Category != "":
				has("category", it.Category, "menu item "+it.Title)
			case it.URL == "":
				t.Errorf("menu item %q has no target", it.Title)
			}
			walk(it.Children)
		}
	}
	submenu := false
	for _, mn := range d.Menus {
		walk(mn.Items)
		for _, it := range mn.Items {
			submenu = submenu || len(it.Children) > 0
		}
	}
	if !submenu {
		t.Error("no menu has a submenu")
	}
	if len(d.Widgets) == 0 {
		t.Error("no sidebar widgets")
	}
	return &d
}

func TestThemeLibraryJ(t *testing.T) {
	t.Setenv("WPGO_THEMES_DIR", t.TempDir())
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	installAndLogin(t, ts, c)
	t.Cleanup(func() {
		for _, slug := range libJThemes {
			unregisterTheme(slug)
		}
	})
	abs := func(u string) string {
		if strings.HasPrefix(u, "http") {
			if p, err := url.Parse(u); err == nil {
				return base + p.RequestURI()
			}
		}
		return base + u
	}

	for _, slug := range libJThemes {
		t.Run(slug, func(t *testing.T) {
			dir := filepath.Join(libJDir, slug)
			mb, err := os.ReadFile(filepath.Join(dir, "theme.json"))
			if err != nil {
				t.Fatal(err)
			}
			m, err := parseThemeManifest(mb)
			if err != nil {
				t.Fatalf("theme.json: %v", err)
			}
			if m.Category == "" || m.Style == "" || m.Demo != "demo/demo.json" || m.Version != "1.0.0" || m.Author != "WordPress Go" || m.RequiresCore != "0.1.0" {
				t.Fatalf("library metadata: %q %q %q %s %s", m.Category, m.Style, m.Demo, m.Version, m.Author)
			}
			nset := 0
			for _, sec := range m.Customizer.Sections {
				nset += len(sec.Settings)
			}
			if nset < 8 || len(m.Customizer.Sections) < 2 || len(m.Customizer.Sections) > 4 {
				t.Fatalf("%d settings in %d sections, want at least 8 in 2–4", nset, len(m.Customizer.Sections))
			}
			demo := libJCheckDemo(t, dir, m)

			// Install through Appearance › Add Themes › Upload Theme, then activate.
			_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
			code, body, loc := postZip(t, c, base+"/wp-admin/theme-install", formNonce(t, body), "themezip", slug+".zip", zipDir(t, dir))
			if code != http.StatusFound || loc != "/wp-admin/themes?installed="+slug {
				t.Fatalf("upload: %d %q %s", code, loc, body)
			}
			th := themeBySlug(slug)
			if th == nil || th.Package == nil {
				t.Fatalf("%s not registered", slug)
			}
			_, body, _ = get(t, c, base+"/wp-admin/themes")
			am := regexp.MustCompile(`href="(/wp-admin/themes\?action=activate&amp;stylesheet=` + slug + `&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
			if am == nil {
				t.Fatal("no activate link")
			}
			if code, _, _ := get(t, c, base+html.UnescapeString(am[1])); code != http.StatusFound || srv.activeTheme().Slug != slug {
				t.Fatalf("activate: %d, active %s", code, srv.activeTheme().Slug)
			}

			styleLink := `href='/wp-content/themes/` + slug + `/style.css?ver=1.0.0'`
			check := func(what, u string, want int, marks ...string) string {
				t.Helper()
				code, body, _ := get(t, c, abs(u))
				if code != want {
					t.Fatalf("%s %s: %d (want %d)", what, u, code, want)
				}
				if !strings.Contains(body, styleLink) {
					t.Fatalf("%s: the theme stylesheet is not linked", what)
				}
				if strings.Contains(body, "Template error") || strings.Contains(body, "<no value>") || strings.Contains(body, "{{") {
					t.Fatalf("%s: template error or unreplaced token", what)
				}
				for _, mk := range append([]string{`id="colophon"`, `class="site-header`}, marks...) {
					if !strings.Contains(body, mk) {
						t.Fatalf("%s lacks %q", what, mk)
					}
				}
				return body
			}

			// The fresh install's content: blog home, the sample post and page, a category, search, 404.
			lb := srv.links(ctx)
			hello, err := srv.store.PostBySlug(ctx, "post", "hello-world")
			if err != nil {
				t.Fatal(err)
			}
			sample, err := srv.store.PostBySlug(ctx, "page", "sample-page")
			if err != nil {
				t.Fatal(err)
			}
			uncat, err := srv.store.TermBySlug(ctx, "category", "uncategorized")
			if err != nil {
				t.Fatal(err)
			}
			srv.purgeAll(ctx)
			check("blog home", "/", http.StatusOK, hello.Title)
			check("single", lb.Permalink(hello), http.StatusOK, "entry--single", `class="comments-area"`)
			check("page", lb.Permalink(sample), http.StatusOK, "entry--page")
			check("category", lb.TermLink(uncat), http.StatusOK, "page-header--archive")
			check("search", "/?s=hello", http.StatusOK, "page-header--search")
			check("404", "/wpgo-no-such-page-"+slug+"/", http.StatusNotFound, "error-404")
			for _, a := range append([]string{"assets/style.css", "assets/editor-style.css", "assets/screenshot.png"}, m.PreloadFonts...) {
				if code, _, _ := get(t, c, base+"/wp-content/themes/"+slug+"/"+strings.TrimPrefix(a, "assets/")); code != http.StatusOK {
					t.Errorf("asset %s: %d", a, code)
				}
			}

			// Every css_var setting is printed; every body_class setting with a value adds its class.
			body = check("home again", "/", http.StatusOK)
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
					if cls := strings.ReplaceAll(st.BodyClass, "{value}", def); !strings.Contains(classes, " "+cls+" ") {
						t.Errorf("body class %q missing from %q", cls, bodyTag[1])
					}
				}
			}

			// Import the demo, then render every imported page, post and category.
			rep, err := srv.importThemeDemo(ctx, 1, th)
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if rep.Posts != len(demo.Posts) || rep.Pages != len(demo.Pages) || rep.Media != len(demo.Media) {
				t.Fatalf("import report %+v", rep)
			}
			srv.purgeAll(ctx)
			lb = srv.links(ctx)
			check("demo home", "/", http.StatusOK, `class="menu-toggle"`, `class="main-navigation`, "sub-menu")
			front := false
			for _, pg := range demo.Pages {
				p, err := srv.store.PostBySlug(ctx, "page", pg.Slug)
				if err != nil {
					t.Fatalf("page %s: %v", pg.Slug, err)
				}
				switch {
				case pg.FrontPage:
					front = true
					check("front page", "/", http.StatusOK, "entry--front")
				case pg.PostsPage:
					check("posts page "+pg.Slug, lb.Permalink(p), http.StatusOK, `class="post-list`, demo.Posts[0].Title)
				default:
					check("page "+pg.Slug, lb.Permalink(p), http.StatusOK, "entry--page")
				}
			}
			if !front {
				check("blog front", "/", http.StatusOK, `class="post-list`)
			}
			for _, dp := range demo.Posts {
				p, err := srv.store.PostBySlug(ctx, "post", dp.Slug)
				if err != nil {
					t.Fatalf("post %s: %v", dp.Slug, err)
				}
				body := check("post "+dp.Slug, lb.Permalink(p), http.StatusOK, "entry--single")
				if len(dp.Comments) > 0 && !strings.Contains(body, `class="comment-list"`) {
					t.Errorf("post %s: comments missing", dp.Slug)
				}
			}
			for _, dc := range demo.Categories {
				term, err := srv.store.TermBySlug(ctx, "category", dc.Slug)
				if err != nil {
					t.Fatalf("category %s: %v", dc.Slug, err)
				}
				check("category "+dc.Slug, lb.TermLink(term), http.StatusOK, "page-header--archive")
			}
			word := strings.Fields(demo.Posts[0].Title)[0]
			body = check("demo search", "/?s="+url.QueryEscape(word), http.StatusOK, "page-header--search")
			if !strings.Contains(body, html.EscapeString(demo.Posts[0].Title)) && !strings.Contains(body, demo.Posts[0].Title) {
				t.Errorf("search for %q does not find %q", word, demo.Posts[0].Title)
			}
			check("demo 404", "/no-such-demo-page-"+slug+"/", http.StatusNotFound, "error-404")

			// Remove the demo again so the next theme starts from the same site.
			if err := srv.removeThemeDemo(ctx, slug); err != nil {
				t.Fatalf("remove demo: %v", err)
			}
			srv.purgeAll(ctx)
		})
	}
}
