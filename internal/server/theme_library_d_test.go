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

// Theme library, group d: Wanderline, Evergreen Care, Open Hands, Campus Commons and Ever After.
// Each package is zipped from web/theme-library/<slug>, uploaded through Appearance › Add Themes ›
// Upload, activated and rendered for every kind of request; its demo content is checked offline.

var themeLibraryD = []string{"wanderline", "evergreen-care", "open-hands", "campus-commons", "ever-after"}

const libDDir = "../../web/theme-library"

// demoFile mirrors docs/theme-package.md, "Demo content".
type demoFile struct {
	Version    int                 `json:"version"`
	Options    map[string]any      `json:"options"`
	Categories []libDDemoTerm      `json:"categories"`
	Tags       []libDDemoTerm      `json:"tags"`
	Media      []libDDemoMedia     `json:"media"`
	Pages      []libDDemoPost      `json:"pages"`
	Posts      []libDDemoPost      `json:"posts"`
	Menus      []libDDemoMenu      `json:"menus"`
	Widgets    map[string][]string `json:"widgets"`
	Customizer map[string]any      `json:"customizer"`
}

type libDDemoTerm struct {
	Slug, Name, Description string
}

type libDDemoMedia struct {
	ID, File, Alt, Caption string
}

type libDDemoPost struct {
	Slug          string            `json:"slug"`
	Title         string            `json:"title"`
	Content       string            `json:"content"`
	Excerpt       string            `json:"excerpt"`
	Date          string            `json:"date"`
	FeaturedMedia string            `json:"featured_media"`
	Categories    []string          `json:"categories"`
	Tags          []string          `json:"tags"`
	Parent        string            `json:"parent"`
	FrontPage     bool              `json:"front_page"`
	PostsPage     bool              `json:"posts_page"`
	Comments      []libDDemoComment `json:"comments"`
}

type libDDemoComment struct {
	Author, Email, Content, Date string
}

type libDDemoMenu struct {
	Name     string             `json:"name"`
	Location string             `json:"location"`
	Items    []libDDemoMenuItem `json:"items"`
}

type libDDemoMenuItem struct {
	Title    string             `json:"title"`
	Page     string             `json:"page"`
	Post     string             `json:"post"`
	Category string             `json:"category"`
	URL      string             `json:"url"`
	Children []libDDemoMenuItem `json:"children"`
}

var demoRefRe = regexp.MustCompile(`\{\{(media|page|post|category):([^}]*)\}\}`)

// checkThemeLibraryDemo validates a package's demo/demo.json against the files and the manifest.
func checkThemeLibraryDemo(t *testing.T, slug string) {
	t.Helper()
	dir := filepath.Join(libDDir, slug)
	mb, err := os.ReadFile(filepath.Join(dir, "theme.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := parseThemeManifest(mb)
	if err != nil {
		t.Fatalf("theme.json: %v", err)
	}
	var meta struct {
		Category, Style, Demo string
	}
	_ = json.Unmarshal(mb, &meta)
	if meta.Category == "" || meta.Style == "" || meta.Demo != "demo/demo.json" {
		t.Fatalf("library metadata missing: %+v", meta)
	}
	b, err := os.ReadFile(filepath.Join(dir, meta.Demo))
	if err != nil {
		t.Fatal(err)
	}
	var d demoFile
	dec := json.NewDecoder(strings.NewReader(string(b)))
	if err := dec.Decode(&d); err != nil {
		t.Fatalf("demo.json: %v", err)
	}
	if d.Version != 1 {
		t.Fatalf("demo version %d", d.Version)
	}
	for k := range d.Options {
		if k != "blogname" && k != "blogdescription" && k != "posts_per_page" && k != "date_format" {
			t.Errorf("option %q may not be set by a demo", k)
		}
	}

	media := map[string]bool{}
	for _, md := range d.Media {
		if media[md.ID] {
			t.Errorf("media %q listed twice", md.ID)
		}
		media[md.ID] = true
		if !strings.HasPrefix(md.File, "demo/images/") || md.Alt == "" {
			t.Errorf("media %q: file %q alt %q", md.ID, md.File, md.Alt)
		}
		st, err := os.Stat(filepath.Join(dir, md.File))
		if err != nil {
			t.Errorf("media %q: %v", md.ID, err)
		} else if st.Size() > 1<<20 {
			t.Errorf("media %q is larger than 1 MiB", md.ID)
		}
	}
	if n := len(d.Media); n < 10 || n > 24 {
		t.Errorf("%d demo images, want 10–24", n)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "demo", "images", "*"))
	if len(files) != len(d.Media) {
		t.Errorf("%d files in demo/images but %d media entries", len(files), len(d.Media))
	}

	terms := func(list []libDDemoTerm, what string) map[string]bool {
		out := map[string]bool{}
		for _, x := range list {
			if x.Slug == "" || x.Name == "" || out[x.Slug] {
				t.Errorf("%s %+v: empty or duplicate", what, x)
			}
			out[x.Slug] = true
		}
		return out
	}
	cats, tags := terms(d.Categories, "category"), terms(d.Tags, "tag")
	pages, posts := map[string]bool{}, map[string]bool{}
	for _, pg := range d.Pages {
		if pages[pg.Slug] {
			t.Errorf("page %q listed twice", pg.Slug)
		}
		pages[pg.Slug] = true
	}
	for _, ps := range d.Posts {
		if posts[ps.Slug] {
			t.Errorf("post %q listed twice", ps.Slug)
		}
		posts[ps.Slug] = true
	}
	if n := len(d.Posts); n < 6 || n > 10 {
		t.Errorf("%d demo posts, want 6–10", n)
	}
	if n := len(d.Pages); n < 3 {
		t.Errorf("%d demo pages, want at least 3", n)
	}
	refs := func(where, content string) {
		for _, r := range demoRefRe.FindAllStringSubmatch(content, -1) {
			ok := map[string]map[string]bool{"media": media, "page": pages, "post": posts, "category": cats}[r[1]][r[2]]
			if !ok {
				t.Errorf("%s: %s does not exist", where, r[0])
			}
		}
		if strings.Contains(strings.ToLower(content), "lorem ipsum") {
			t.Errorf("%s: lorem ipsum", where)
		}
	}
	fronts, blogs := 0, 0
	for _, pg := range d.Pages {
		refs("page "+pg.Slug, pg.Content)
		if pg.FeaturedMedia != "" && !media[pg.FeaturedMedia] {
			t.Errorf("page %s: featured media %q does not exist", pg.Slug, pg.FeaturedMedia)
		}
		if pg.Parent != "" && !pages[pg.Parent] {
			t.Errorf("page %s: parent %q does not exist", pg.Slug, pg.Parent)
		}
		if pg.FrontPage {
			fronts++
		}
		if pg.PostsPage {
			blogs++
		}
	}
	if fronts > 1 || blogs > 1 {
		t.Errorf("%d front pages, %d posts pages", fronts, blogs)
	}
	for _, ps := range d.Posts {
		refs("post "+ps.Slug, ps.Content)
		if ps.Title == "" || ps.Excerpt == "" || ps.Date == "" {
			t.Errorf("post %s: title, excerpt and date are required here", ps.Slug)
		}
		if ps.FeaturedMedia != "" && !media[ps.FeaturedMedia] {
			t.Errorf("post %s: featured media %q does not exist", ps.Slug, ps.FeaturedMedia)
		}
		for _, c := range ps.Categories {
			if !cats[c] {
				t.Errorf("post %s: category %q does not exist", ps.Slug, c)
			}
		}
		for _, tg := range ps.Tags {
			if !tags[tg] {
				t.Errorf("post %s: tag %q does not exist", ps.Slug, tg)
			}
		}
		if n := strings.Count(ps.Content, "<!-- wp:paragraph"); n < 3 {
			t.Errorf("post %s: %d paragraphs, want at least 3", ps.Slug, n)
		}
	}

	locations := map[string]bool{}
	for _, l := range m.MenuLocations {
		locations[l.ID] = true
	}
	var walk func(menu string, items []libDDemoMenuItem)
	walk = func(menu string, items []libDDemoMenuItem) {
		for _, it := range items {
			switch {
			case it.Page != "" && !pages[it.Page]:
				t.Errorf("menu %s: page %q does not exist", menu, it.Page)
			case it.Post != "" && !posts[it.Post]:
				t.Errorf("menu %s: post %q does not exist", menu, it.Post)
			case it.Category != "" && !cats[it.Category]:
				t.Errorf("menu %s: category %q does not exist", menu, it.Category)
			case it.Page == "" && it.Post == "" && it.Category == "" && it.URL == "":
				t.Errorf("menu %s: item %q links nowhere", menu, it.Title)
			}
			walk(menu, it.Children)
		}
	}
	hasSub := false
	for _, mn := range d.Menus {
		if !locations[mn.Location] {
			t.Errorf("menu %s: location %q is not registered", mn.Name, mn.Location)
		}
		walk(mn.Name, mn.Items)
		for _, it := range mn.Items {
			if len(it.Children) > 0 {
				hasSub = true
			}
		}
	}
	if !hasSub {
		t.Error("no menu has a submenu")
	}

	areas := map[string]bool{}
	for _, a := range m.WidgetAreas {
		areas[a.ID] = true
	}
	for area, ws := range d.Widgets {
		if !areas[area] {
			t.Errorf("widgets for unregistered area %q", area)
		}
		for _, w := range ws {
			refs("widget "+area, w)
		}
	}

	settings := map[string]themeCzSettings{}
	n := 0
	for _, sec := range m.Customizer.Sections {
		for _, st := range sec.Settings {
			settings[st.ID] = st
			n++
		}
	}
	if n < 8 || len(m.Customizer.Sections) < 2 || len(m.Customizer.Sections) > 4 {
		t.Errorf("%d settings in %d sections, want at least 8 in 2–4", n, len(m.Customizer.Sections))
	}
	for k, v := range d.Customizer {
		st, ok := settings[k]
		if !ok {
			t.Errorf("customizer: %q is not a setting of the theme", k)
			continue
		}
		switch st.Type {
		case "checkbox":
			if _, ok := v.(bool); !ok {
				t.Errorf("customizer %s: %v is not a boolean", k, v)
			}
		case "color":
			if s, _ := v.(string); sanitizeHex(s) == "" {
				t.Errorf("customizer %s: %v is not a color", k, v)
			}
		case "select":
			found := false
			for _, ch := range st.Choices {
				found = found || ch[0] == fmt.Sprint(v)
			}
			if !found {
				t.Errorf("customizer %s: %v is not a choice", k, v)
			}
		}
	}
}

func TestThemeLibraryDDemo(t *testing.T) {
	for _, slug := range themeLibraryD {
		t.Run(slug, func(t *testing.T) { checkThemeLibraryDemo(t, slug) })
	}
}

func TestThemeLibraryD(t *testing.T) {
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
		for _, slug := range themeLibraryD {
			unregisterTheme(slug)
		}
	})

	lb := srv.links(ctx)
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
	// A comment, so the comment list renders too.
	if err := srv.store.InsertComment(ctx, &store.Comment{PostID: hello.ID, AuthorName: "Visitor", AuthorEmail: "v@example.test",
		Content: "A first comment.", Status: "approved"}); err != nil {
		t.Fatal(err)
	}
	front := mustInsertPost(t, srv, &store.Post{Type: "page", AuthorID: 1, Slug: "welcome", Title: "Welcome",
		Content: `<!-- wp:group {"align":"full","layout":{"type":"constrained"}} --><div class="wp-block-group alignfull"><!-- wp:heading --><h2 class="wp-block-heading">Hello</h2><!-- /wp:heading --><!-- wp:latest-posts {"postsToShow":3,"postLayout":"grid","columns":3} /--></div><!-- /wp:group -->`})
	blog := mustInsertPost(t, srv, &store.Post{Type: "page", AuthorID: 1, Slug: "blog", Title: "Blog"})

	urls := map[string]string{
		"single":   lb.Permalink(hello),
		"page":     lb.Permalink(sample),
		"category": lb.TermLink(cat),
		"search":   base + "/?s=hello",
	}
	abs := func(u string) string {
		if strings.HasPrefix(u, "http") {
			return u
		}
		return base + u
	}
	for k, u := range urls {
		urls[k] = abs(u)
	}

	_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
	nonce := formNonce(t, body)

	for _, slug := range themeLibraryD {
		t.Run(slug, func(t *testing.T) {
			pkg := zipDir(t, filepath.Join(libDDir, slug))
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
				if strings.Contains(body, "Template error") || strings.Contains(body, "template:") {
					t.Fatalf("%s: template error in %s", what, body)
				}
				for _, mk := range append([]string{styleLink, `class="site-header`, `id="colophon"`, "/js/navigation.js?ver=1.0.0"}, marks...) {
					if !strings.Contains(body, mk) {
						t.Fatalf("%s lacks %q", what, mk)
					}
				}
				return body
			}

			// Blog on the front page: the hero and the post list.
			srv.purgeAll(ctx)
			check("home", base+"/", http.StatusOK, `class="post-list"`, "hfeed", hello.Title)
			check("single", urls["single"], http.StatusOK, "entry--single", `class="comments-area"`)
			check("page", urls["page"], http.StatusOK, "entry--page")
			check("category", urls["category"], http.StatusOK, "page-header--archive")
			check("search", urls["search"], http.StatusOK, "page-header--search")
			check("404", base+"/wpgo-no-such-page-at-all/", http.StatusNotFound, "error-404")

			// Every css_var setting is printed with its default; every body_class setting adds its class.
			body = check("home again", base+"/", http.StatusOK)
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
				if st.BodyClass != "" {
					cls := strings.ReplaceAll(st.BodyClass, "{value}", def)
					if strings.Contains(st.BodyClass, "{value}") || def == "1" {
						if !strings.Contains(classes, " "+cls+" ") {
							t.Errorf("body class %q missing from %q", cls, bodyTag[1])
						}
					}
				}
			}

			// Static front page and posts page.
			if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/settings",
				fmt.Sprintf(`{"show_on_front":"page","page_on_front":%d,"page_for_posts":%d}`, front.ID, blog.ID)); code != http.StatusOK {
				t.Fatalf("reading settings: %d %s", code, b)
			}
			srv.purgeAll(ctx)
			check("static front", base+"/", http.StatusOK, "entry--front", "wp-block-latest-posts")
			check("posts page", abs(lb.Permalink(blog)), http.StatusOK, `class="post-list"`, "page-header")
			if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/settings", `{"show_on_front":"posts"}`); code != http.StatusOK {
				t.Fatalf("reading settings: %d %s", code, b)
			}

			// Assets: stylesheet, script, screenshot, fonts and the default hero image are served.
			for _, a := range append([]string{"style.css", "editor-style.css", "js/navigation.js", "screenshot.png"}, th.PreloadFonts...) {
				if code, _, _ := get(t, c, base+"/wp-content/themes/"+slug+"/"+strings.TrimPrefix(a, "assets/")); code != http.StatusOK {
					t.Errorf("asset %s: %d", a, code)
				}
			}
			srv.purgeAll(ctx)
		})
	}
}
