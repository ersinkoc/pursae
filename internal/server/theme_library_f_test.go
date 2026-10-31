package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// Theme library, group F: Justitia, Ironbeam Construction, Numera, Bright Smile Dental and Archform.
// Each package is checked offline (manifest, assets, demo file with the real parser), then zipped from
// library/themes/<slug>, uploaded through Appearance › Add Themes › Upload, activated and rendered for
// every kind of request, before and after its demo content is imported with importThemeDemo.

var libFThemes = []string{"justitia", "ironbeam", "numera", "bright-smile", "archform"}

const libFDir = "../../library/themes"

var libFCategories = map[string]bool{"blog": true, "magazine": true, "portfolio": true, "photography": true, "business": true,
	"agency": true, "restaurant": true, "food": true, "travel": true, "fashion": true, "technology": true, "education": true,
	"health": true, "fitness": true, "nonprofit": true, "music": true, "wedding": true, "real-estate": true, "personal": true,
	"minimal": true, "home": true, "shop": true, "legal": true, "construction": true, "finance": true, "architecture": true,
	"gaming": true, "entertainment": true, "podcast": true, "beauty": true, "kids": true, "pets": true, "automotive": true,
	"hotel": true, "startup": true, "science": true, "religion": true, "environment": true, "books": true, "sports": true}

var libFTokenRe = regexp.MustCompile(`\{\{(media|media_id|page|post|category|tag):([^}]*)\}\}`)

// libFDemo mirrors the parts of demo/demo.json the checks below look at.
type libFDemo struct {
	Version    int            `json:"version"`
	Options    map[string]any `json:"options"`
	Categories []struct {
		Slug, Name, Parent string
	} `json:"categories"`
	Tags []struct {
		Slug, Name string
	} `json:"tags"`
	Media []struct {
		ID, File, Alt string
	} `json:"media"`
	Pages      []libFDemoItem      `json:"pages"`
	Posts      []libFDemoItem      `json:"posts"`
	Menus      []libFDemoMenu      `json:"menus"`
	Widgets    map[string][]string `json:"widgets"`
	Customizer map[string]any      `json:"customizer"`
}

type libFDemoItem struct {
	Slug          string   `json:"slug"`
	Title         string   `json:"title"`
	Content       string   `json:"content"`
	Excerpt       string   `json:"excerpt"`
	Date          string   `json:"date"`
	FeaturedMedia string   `json:"featured_media"`
	Template      string   `json:"template"`
	Parent        string   `json:"parent"`
	FrontPage     bool     `json:"front_page"`
	PostsPage     bool     `json:"posts_page"`
	Categories    []string `json:"categories"`
	Tags          []string `json:"tags"`
	Comments      []struct {
		Author, Content string
	} `json:"comments"`
}

type libFDemoMenu struct {
	Name     string             `json:"name"`
	Location string             `json:"location"`
	Items    []libFDemoMenuItem `json:"items"`
}

type libFDemoMenuItem struct {
	Title    string             `json:"title"`
	Page     string             `json:"page"`
	Post     string             `json:"post"`
	Category string             `json:"category"`
	URL      string             `json:"url"`
	Children []libFDemoMenuItem `json:"children"`
}

// libFReadDemo checks a package directory offline and returns its parsed demo file.
func libFReadDemo(t *testing.T, slug string) (*themeManifest, *libFDemo) {
	t.Helper()
	dir := filepath.Join(libFDir, slug)
	mb, err := os.ReadFile(filepath.Join(dir, "theme.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := parseThemeManifest(mb)
	if err != nil {
		t.Fatalf("theme.json: %v", err)
	}
	if m.Slug != slug || m.Version != "1.0.0" || m.RequiresCore != "0.1.0" || m.Author != "Pursae" {
		t.Errorf("manifest %s %s %s %s", m.Slug, m.Version, m.RequiresCore, m.Author)
	}
	if !libFCategories[m.Category] || m.Style == "" || len(m.Style) > 80 || strings.ContainsAny(m.Style, "<>\n") || m.Demo != "demo/demo.json" {
		t.Errorf("library metadata: category %q style %q demo %q", m.Category, m.Style, m.Demo)
	}
	if len(m.Palette) < 4 || len(m.EditorStyles) == 0 {
		t.Errorf("palette %d, editor styles %v", len(m.Palette), m.EditorStyles)
	}

	// The real parser, exactly as the installer runs it.
	if _, err := parseThemeDemo(os.DirFS(dir), m); err != nil {
		t.Fatalf("parseThemeDemo: %v", err)
	}

	// Customizer: at least 8 settings in 2–4 sections; every setting is used by a template or the stylesheet.
	css, err := os.ReadFile(filepath.Join(dir, "assets", "style.css"))
	if err != nil {
		t.Fatal(err)
	}
	var tpl strings.Builder
	_ = fs.WalkDir(os.DirFS(dir), "templates", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(filepath.Join(dir, p))
			tpl.Write(b)
		}
		return nil
	})
	n := 0
	settings := map[string]themeCzSettings{}
	for _, sec := range m.Customizer.Sections {
		for _, st := range sec.Settings {
			n++
			settings[st.ID] = st
			used := false
			switch {
			case st.CSSVar != "":
				used = strings.Contains(string(css), "var("+st.CSSVar+")")
			case st.BodyClass != "":
				cls := strings.ReplaceAll(st.BodyClass, "{value}", "")
				used = strings.Contains(string(css), "."+cls)
			default:
				used = strings.Contains(tpl.String(), `"`+st.ID+`"`)
			}
			if !used {
				t.Errorf("setting %s changes nothing on the site", st.ID)
			}
		}
	}
	if n < 8 || len(m.Customizer.Sections) < 2 || len(m.Customizer.Sections) > 4 {
		t.Errorf("%d settings in %d sections, want at least 8 in 2–4", n, len(m.Customizer.Sections))
	}

	// Assets: fonts ≤ 300 KB in all, the screenshot is 1200×900, images ≤ 400 KB.
	var fontBytes int64
	fonts, _ := filepath.Glob(filepath.Join(dir, "assets", "fonts", "*.woff2"))
	for _, f := range fonts {
		st, _ := os.Stat(f)
		fontBytes += st.Size()
	}
	if fontBytes > 300<<10 {
		t.Errorf("fonts weigh %d KB", fontBytes>>10)
	}
	if f, err := os.Open(filepath.Join(dir, "assets", "screenshot.png")); err != nil {
		t.Error(err)
	} else {
		cfg, _, err := image.DecodeConfig(f)
		f.Close()
		if err != nil || cfg.Width != 1200 || cfg.Height != 900 {
			t.Errorf("screenshot %dx%d %v", cfg.Width, cfg.Height, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "readme.md")); err != nil {
		t.Error(err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "demo", "demo.json"))
	if err != nil {
		t.Fatal(err)
	}
	d := &libFDemo{}
	if err := json.Unmarshal(b, d); err != nil {
		t.Fatalf("demo.json: %v", err)
	}
	if d.Version != 1 || d.Options["blogname"] == nil || d.Options["blogdescription"] == nil {
		t.Errorf("demo version %d options %v", d.Version, d.Options)
	}
	media, pages, posts, cats, tags := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, md := range d.Media {
		media[md.ID] = true
		st, err := os.Stat(filepath.Join(dir, md.File))
		if err != nil {
			t.Errorf("media %s: %v", md.ID, err)
		} else if st.Size() > 400<<10 {
			t.Errorf("media %s weighs %d KB", md.ID, st.Size()>>10)
		}
		if md.Alt == "" {
			t.Errorf("media %s has no alt text", md.ID)
		}
	}
	if len(d.Media) < 10 || len(d.Media) > 16 {
		t.Errorf("%d images, want 10–16", len(d.Media))
	}
	files, _ := filepath.Glob(filepath.Join(dir, "demo", "images", "*"))
	if len(files) != len(d.Media) {
		t.Errorf("%d files in demo/images, %d media entries", len(files), len(d.Media))
	}
	for _, c := range d.Categories {
		cats[c.Slug] = true
	}
	for _, tg := range d.Tags {
		tags[tg.Slug] = true
	}
	for _, pg := range d.Pages {
		pages[pg.Slug] = true
	}
	for _, ps := range d.Posts {
		posts[ps.Slug] = true
	}
	kinds := map[string]map[string]bool{"media": media, "media_id": media, "page": pages, "post": posts, "category": cats, "tag": tags}
	refs := func(where, s string) {
		for _, r := range libFTokenRe.FindAllStringSubmatch(s, -1) {
			if !kinds[r[1]][r[2]] {
				t.Errorf("%s: %s names nothing in the demo", where, r[0])
			}
		}
		if strings.Contains(strings.ToLower(s), "lorem ipsum") {
			t.Errorf("%s: lorem ipsum", where)
		}
	}
	if len(d.Posts) < 6 || len(d.Posts) > 10 || len(d.Pages) < 3 || len(d.Pages) > 8 {
		t.Errorf("%d posts and %d pages", len(d.Posts), len(d.Pages))
	}
	front, blog, comments := 0, 0, 0
	for _, pg := range d.Pages {
		refs("page "+pg.Slug, pg.Content+pg.Excerpt)
		if pg.FeaturedMedia != "" && !media[pg.FeaturedMedia] {
			t.Errorf("page %s: featured media %q", pg.Slug, pg.FeaturedMedia)
		}
		if pg.Parent != "" && !pages[pg.Parent] {
			t.Errorf("page %s: parent %q", pg.Slug, pg.Parent)
		}
		if pg.Template != "" {
			if _, err := os.Stat(filepath.Join(dir, "templates", strings.TrimSuffix(pg.Template, ".html")+".html")); err != nil {
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
		t.Errorf("%d front pages, %d posts pages", front, blog)
	}
	for _, ps := range d.Posts {
		refs("post "+ps.Slug, ps.Content+ps.Excerpt)
		if ps.Excerpt == "" || ps.Date == "" || ps.FeaturedMedia == "" || !media[ps.FeaturedMedia] {
			t.Errorf("post %s: excerpt, date and featured image are expected", ps.Slug)
		}
		for _, c := range ps.Categories {
			if !cats[c] {
				t.Errorf("post %s: category %q", ps.Slug, c)
			}
		}
		for _, tg := range ps.Tags {
			if !tags[tg] {
				t.Errorf("post %s: tag %q", ps.Slug, tg)
			}
		}
		if k := strings.Count(ps.Content, "<!-- wp:paragraph"); k < 3 {
			t.Errorf("post %s: %d paragraphs", ps.Slug, k)
		}
		if !strings.Contains(ps.Content, "<!-- wp:heading") || !(strings.Contains(ps.Content, "<!-- wp:list") || strings.Contains(ps.Content, "<!-- wp:quote")) {
			t.Errorf("post %s: no headings, lists or quotes", ps.Slug)
		}
		comments += len(ps.Comments)
	}
	if comments < 3 {
		t.Errorf("%d comments", comments)
	}
	locs := map[string]bool{}
	for _, l := range m.MenuLocations {
		locs[l.ID] = true
	}
	sub := false
	var walk func(menu string, items []libFDemoMenuItem)
	walk = func(menu string, items []libFDemoMenuItem) {
		for _, it := range items {
			switch {
			case it.Page != "" && !pages[it.Page], it.Post != "" && !posts[it.Post], it.Category != "" && !cats[it.Category]:
				t.Errorf("menu %s: item %q links to nothing in the demo", menu, it.Title)
			}
			if len(it.Children) > 0 {
				sub = true
			}
			walk(menu, it.Children)
		}
	}
	for _, mn := range d.Menus {
		if !locs[mn.Location] {
			t.Errorf("menu %s: location %q", mn.Name, mn.Location)
		}
		walk(mn.Name, mn.Items)
	}
	if !sub {
		t.Error("no submenu in the demo menus")
	}
	areas := map[string]bool{}
	for _, a := range m.WidgetAreas {
		areas[a.ID] = true
	}
	for area, ws := range d.Widgets {
		if !areas[area] {
			t.Errorf("widgets for unknown area %q", area)
		}
		for _, w := range ws {
			refs("widget "+area, w)
		}
	}
	for k, v := range d.Customizer {
		if _, ok := settings[k]; !ok && k != "custom_logo" {
			t.Errorf("customizer: %q is not a setting", k)
		}
		if s, ok := v.(string); ok {
			refs("customizer "+k, s)
		}
	}
	return m, d
}

func TestThemeLibraryFPackages(t *testing.T) {
	for _, slug := range libFThemes {
		t.Run(slug, func(t *testing.T) { libFReadDemo(t, slug) })
	}
}

// libFZip zips a theme directory in memory, files at the zip root.
func libFZip(t *testing.T, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestThemeLibraryF(t *testing.T) {
	t.Setenv("WPGO_THEMES_DIR", t.TempDir())
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Library F"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK {
		t.Fatalf("install: %d %s", code, body)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	t.Cleanup(func() {
		for _, slug := range libFThemes {
			unregisterTheme(slug)
		}
	})
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
	if err := srv.store.InsertComment(ctx, &store.Comment{PostID: hello.ID, AuthorName: "Visitor", AuthorEmail: "v@example.test",
		Content: "A first comment.", Status: "approved"}); err != nil {
		t.Fatal(err)
	}
	_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
	nonce := formNonce(t, body)

	for _, slug := range libFThemes {
		t.Run(slug, func(t *testing.T) {
			_, demo := libFReadDemo(t, slug)
			code, body, loc := postZip(t, c, base+"/wp-admin/theme-install", nonce, "themezip", slug+".zip", libFZip(t, filepath.Join(libFDir, slug)))
			if code != http.StatusFound || loc != "/wp-admin/themes?installed="+slug {
				t.Fatalf("upload: %d %q %s", code, loc, body)
			}
			th := themeBySlug(slug)
			if th == nil || th.Package == nil {
				t.Fatalf("%s is not registered", slug)
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
				t.Fatalf("active theme is %s", srv.activeTheme().Slug)
			}

			styleLink := fmt.Sprintf(`id='%s-style-css' href='/wp-content/themes/%s/style.css?ver=1.0.0'`, slug, slug)
			check := func(what, u string, want int, marks ...string) string {
				t.Helper()
				code, body, _ := get(t, c, abs(u))
				if code != want {
					t.Fatalf("%s %s: %d (want %d)", what, u, code, want)
				}
				if strings.Contains(body, "Template error") || strings.Contains(body, "ZgotmplZ") {
					t.Fatalf("%s: template error: %s", what, body)
				}
				for _, mk := range append([]string{styleLink, `class="site-header`, `id="colophon"`, "/js/navigation.js?ver=1.0.0", `id="main"`}, marks...) {
					if !strings.Contains(body, mk) {
						t.Fatalf("%s (%s) lacks %q", what, u, mk)
					}
				}
				return body
			}

			// A fresh site: blog on the front page and the install samples.
			srv.purgeAll(ctx)
			lb := srv.links(ctx)
			sample, err := srv.store.PostBySlug(ctx, "page", "sample-page")
			if err != nil {
				t.Fatal(err)
			}
			uncat, err := srv.store.TermBySlug(ctx, "category", "uncategorized")
			if err != nil {
				t.Fatal(err)
			}
			body = check("home", base+"/", http.StatusOK, `class="post-list"`, "hfeed", "page-header--blog")
			check("single", lb.Permalink(hello), http.StatusOK, "entry--single", `class="comments-area"`, "A first comment.")
			check("page", lb.Permalink(sample), http.StatusOK, "entry--page")
			check("category", lb.TermLink(uncat), http.StatusOK, "page-header--archive", `class="post-list"`)
			check("search", base+"/?s=hello", http.StatusOK, "page-header--search", hello.Title)
			check("empty search", base+"/?s=zzqx-nothing", http.StatusOK, "page-header--search", "no-results")
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
					if cls := strings.ReplaceAll(st.BodyClass, "{value}", def); !strings.Contains(classes, " "+cls+" ") {
						t.Errorf("body class %q missing from %q", cls, bodyTag[1])
					}
				}
			}

			// Theme assets are served.
			for _, a := range append([]string{"style.css", "editor-style.css", "js/navigation.js", "screenshot.png"}, th.PreloadFonts...) {
				if code, _, _ := get(t, c, base+"/wp-content/themes/"+slug+"/"+strings.TrimPrefix(a, "assets/")); code != http.StatusOK {
					t.Errorf("asset %s: %d", a, code)
				}
			}

			// Import the demo content and render every page, post and category of it.
			rep, err := srv.importThemeDemo(ctx, 1, th)
			if err != nil {
				t.Fatalf("import demo: %v", err)
			}
			if rep.Posts != len(demo.Posts) || rep.Pages != len(demo.Pages) || rep.Media != len(demo.Media) || rep.Menus != len(demo.Menus) {
				t.Fatalf("import report %+v", rep)
			}
			srv.purgeAll(ctx)
			lb = srv.links(ctx)
			for _, pg := range demo.Pages {
				p, err := srv.store.PostBySlug(ctx, "page", pg.Slug)
				if err != nil {
					t.Fatalf("page %s: %v", pg.Slug, err)
				}
				switch {
				case pg.FrontPage:
					check("front page", base+"/", http.StatusOK, "entry--front", `class="hero`, "wp-block-group", `class="primary-menu-wrap"`, "sub-menu")
				case pg.PostsPage:
					check("posts page", lb.Permalink(p), http.StatusOK, "page-header--blog", `class="post-list"`, `class="card`, `id="secondary"`)
				default:
					body := check("page "+pg.Slug, lb.Permalink(p), http.StatusOK, "entry--page")
					if pg.Template == "page-wide" && !strings.Contains(body, "entry--wide") {
						t.Errorf("page %s does not use page-wide", pg.Slug)
					}
				}
			}
			for _, ps := range demo.Posts {
				p, err := srv.store.PostBySlug(ctx, "post", ps.Slug)
				if err != nil {
					t.Fatalf("post %s: %v", ps.Slug, err)
				}
				marks := []string{"entry--single", "post-thumbnail", "wp-block-heading"}
				if len(ps.Comments) > 0 {
					marks = append(marks, `class="comment-list"`)
				}
				check("post "+ps.Slug, lb.Permalink(p), http.StatusOK, marks...)
			}
			for _, cat := range demo.Categories {
				term, err := srv.store.TermBySlug(ctx, "category", cat.Slug)
				if err != nil {
					t.Fatalf("category %s: %v", cat.Slug, err)
				}
				check("category "+cat.Slug, lb.TermLink(term), http.StatusOK, "page-header--archive")
			}
			for _, tg := range demo.Tags[:1] {
				term, err := srv.store.TermBySlug(ctx, "post_tag", tg.Slug)
				if err != nil {
					t.Fatalf("tag %s: %v", tg.Slug, err)
				}
				check("tag "+tg.Slug, lb.TermLink(term), http.StatusOK, "page-header--archive", `class="post-list"`)
			}
			word := strings.Fields(demo.Posts[0].Title)[1]
			check("demo search", base+"/?s="+url.QueryEscape(word), http.StatusOK, "page-header--search", `class="card`)
			check("demo 404", base+"/wpgo-no-such-page-at-all/", http.StatusNotFound, "error-404")

			if err := srv.removeThemeDemo(ctx, slug); err != nil {
				t.Fatalf("remove demo: %v", err)
			}
			srv.purgeAll(ctx)
		})
	}
}
