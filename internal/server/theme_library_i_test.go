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

// Theme library, group I: Launchpad, Cloudline, Datafield, AppNest and Northstar Ventures. Each
// package is zipped from web/theme-library/<slug>, uploaded through Appearance › Add Themes › Upload,
// activated and rendered for every kind of request; then its demo content is validated with the real
// parser, imported, browsed page by page and removed again.

var libIThemes = []string{"launchpad", "cloudline", "datafield", "appnest", "northstar-ventures"}

const libIDir = "../../web/theme-library"

// libIFront is what the home page shows once the demo is imported: a designed static front page,
// or (Datafield) the blog with its intro.
var libIFront = map[string][]string{
	"launchpad":          {"entry--front", `class="hero"`, "hero__shot", "pricing"},
	"cloudline":          {"entry--front", "hero--has-media", "hero__uptime", "plans"},
	"datafield":          {`class="intro"`, `class="post-list"`, "masthead-chart"},
	"appnest":            {"entry--front", `class="phone"`, "store-button", "wp-block-details"},
	"northstar-ventures": {"entry--front", "hero--has-media", "portfolio", "team"},
}

type libIDemo struct {
	Version    int                 `json:"version"`
	Options    map[string]any      `json:"options"`
	Categories []libIDemoTerm      `json:"categories"`
	Tags       []libIDemoTerm      `json:"tags"`
	Media      []libIDemoMedia     `json:"media"`
	Pages      []libIDemoPost      `json:"pages"`
	Posts      []libIDemoPost      `json:"posts"`
	Menus      []libIDemoMenu      `json:"menus"`
	Widgets    map[string][]string `json:"widgets"`
	Customizer map[string]any      `json:"customizer"`
}

type libIDemoTerm struct {
	Slug, Name, Description string
}

type libIDemoMedia struct {
	ID, File, Alt, Caption string
}

type libIDemoPost struct {
	Slug          string `json:"slug"`
	Title         string `json:"title"`
	Content       string `json:"content"`
	Excerpt       string `json:"excerpt"`
	Date          string `json:"date"`
	Template      string `json:"template"`
	FeaturedMedia string `json:"featured_media"`
	Categories    []string
	Tags          []string
	Parent        string `json:"parent"`
	FrontPage     bool   `json:"front_page"`
	PostsPage     bool   `json:"posts_page"`
	Comments      []struct {
		Author, Content string
	} `json:"comments"`
}

type libIDemoMenu struct {
	Name     string
	Location string
	Items    []libIDemoMenuItem
}

type libIDemoMenuItem struct {
	Title, Page, Post, Category, URL string
	Children                         []libIDemoMenuItem
}

var libIRefRe = regexp.MustCompile(`\{\{(media|media_id|page|post|category|tag):([^}]*)\}\}`)

// libICheckDemo validates demo/demo.json with the real parser and checks, by hand, that every
// reference names an item of the demo and that the content meets the library's bar.
func libICheckDemo(t *testing.T, slug string) {
	t.Helper()
	dir := filepath.Join(libIDir, slug)
	mb, err := os.ReadFile(filepath.Join(dir, "theme.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := parseThemeManifest(mb)
	if err != nil {
		t.Fatalf("theme.json: %v", err)
	}
	if m.Category == "" || m.Style == "" || m.Demo != "demo/demo.json" {
		t.Fatalf("library metadata: %q %q %q", m.Category, m.Style, m.Demo)
	}
	if _, err := parseThemeDemo(os.DirFS(dir), m); err != nil {
		t.Fatalf("parseThemeDemo: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, m.Demo))
	if err != nil {
		t.Fatal(err)
	}
	var d libIDemo
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("demo.json: %v", err)
	}

	media := map[string]bool{}
	for _, md := range d.Media {
		media[md.ID] = true
		if md.Alt == "" {
			t.Errorf("media %s has no alt text", md.ID)
		}
		st, err := os.Stat(filepath.Join(dir, md.File))
		if err != nil {
			t.Errorf("media %s: %v", md.ID, err)
		} else if st.Size() > 400<<10 {
			t.Errorf("media %s is %d KB, over 400 KB", md.ID, st.Size()>>10)
		}
	}
	if n := len(d.Media); n < 10 || n > 24 {
		t.Errorf("%d demo images, want 10–24", n)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "demo", "images", "*")); len(files) != len(d.Media) {
		t.Errorf("%d files in demo/images, %d media entries", len(files), len(d.Media))
	}
	set := func(list []libIDemoTerm) map[string]bool {
		out := map[string]bool{}
		for _, x := range list {
			out[x.Slug] = true
		}
		return out
	}
	cats, tags := set(d.Categories), set(d.Tags)
	pages, posts := map[string]bool{}, map[string]bool{}
	for _, p := range d.Pages {
		pages[p.Slug] = true
	}
	for _, p := range d.Posts {
		posts[p.Slug] = true
	}
	known := map[string]map[string]bool{"media": media, "media_id": media, "page": pages, "post": posts, "category": cats, "tag": tags}
	refs := func(where, s string) {
		for _, r := range libIRefRe.FindAllStringSubmatch(s, -1) {
			if !known[r[1]][r[2]] {
				t.Errorf("%s: %s does not exist", where, r[0])
			}
		}
		if strings.Contains(strings.ToLower(s), "lorem ipsum") {
			t.Errorf("%s: lorem ipsum", where)
		}
	}
	if n := len(d.Posts); n < 6 || n > 10 {
		t.Errorf("%d posts, want 6–10", n)
	}
	if n := len(d.Pages); n < 3 || n > 6 {
		t.Errorf("%d pages, want 3–6", n)
	}
	for _, p := range d.Pages {
		refs("page "+p.Slug, p.Content)
		if p.FeaturedMedia != "" && !media[p.FeaturedMedia] {
			t.Errorf("page %s: featured media %q", p.Slug, p.FeaturedMedia)
		}
		if p.Parent != "" && !pages[p.Parent] {
			t.Errorf("page %s: parent %q", p.Slug, p.Parent)
		}
	}
	comments := 0
	for _, p := range d.Posts {
		refs("post "+p.Slug, p.Content+p.Excerpt)
		if p.Excerpt == "" || p.Date == "" || p.FeaturedMedia == "" || len(p.Categories) == 0 {
			t.Errorf("post %s: excerpt, date, featured image and a category are expected", p.Slug)
		}
		if !media[p.FeaturedMedia] {
			t.Errorf("post %s: featured media %q", p.Slug, p.FeaturedMedia)
		}
		for _, c := range p.Categories {
			if !cats[c] {
				t.Errorf("post %s: category %q", p.Slug, c)
			}
		}
		for _, tg := range p.Tags {
			if !tags[tg] {
				t.Errorf("post %s: tag %q", p.Slug, tg)
			}
		}
		if n := strings.Count(p.Content, "<!-- wp:paragraph"); n < 3 {
			t.Errorf("post %s: %d paragraphs, want at least 3", p.Slug, n)
		}
		if !strings.Contains(p.Content, "<!-- wp:heading") || !strings.Contains(p.Content, "<!-- wp:list") && !strings.Contains(p.Content, "<!-- wp:table") {
			t.Errorf("post %s: expected headings and a list or table", p.Slug)
		}
		comments += len(p.Comments)
	}
	if comments < 2 {
		t.Errorf("%d comments, want a few", comments)
	}
	var walk func(items []libIDemoMenuItem) bool
	walk = func(items []libIDemoMenuItem) bool {
		sub := false
		for _, it := range items {
			if it.Page != "" && !pages[it.Page] || it.Post != "" && !posts[it.Post] || it.Category != "" && !cats[it.Category] {
				t.Errorf("menu item %+v links to a missing item", it)
			}
			sub = sub || len(it.Children) > 0
			walk(it.Children)
		}
		return sub
	}
	hasSub := false
	for _, mn := range d.Menus {
		if walk(mn.Items) {
			hasSub = true
		}
	}
	if !hasSub {
		t.Error("no menu has a submenu")
	}
	for area, ws := range d.Widgets {
		for _, w := range ws {
			refs("widget "+area, w)
		}
	}
	for k, v := range d.Customizer {
		if s, ok := v.(string); ok {
			refs("customizer "+k, s)
		}
	}
	if d.Options["blogname"] == nil || d.Options["blogdescription"] == nil {
		t.Error("options.blogname and blogdescription are expected")
	}

	// The manifest: 8+ settings in 2–4 sections, every one with a visible effect (a CSS variable,
	// a body class, or a theme_mod/Settings lookup in a template).
	tpl := ""
	files, _ := filepath.Glob(filepath.Join(dir, "templates", "*.html"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		tpl += string(b)
	}
	n := 0
	for _, sec := range m.Customizer.Sections {
		for _, st := range sec.Settings {
			n++
			if st.CSSVar == "" && st.BodyClass == "" && !strings.Contains(tpl, `"`+st.ID+`"`) {
				t.Errorf("setting %s has no visible effect", st.ID)
			}
		}
	}
	if n < 8 || len(m.Customizer.Sections) < 2 || len(m.Customizer.Sections) > 4 {
		t.Errorf("%d settings in %d sections", n, len(m.Customizer.Sections))
	}
	var size int64
	fonts, _ := filepath.Glob(filepath.Join(dir, "assets", "fonts", "*"))
	for _, f := range fonts {
		st, _ := os.Stat(f)
		size += st.Size()
	}
	if size > 300<<10 {
		t.Errorf("fonts are %d KB, over 300 KB", size>>10)
	}
}

func TestThemeLibraryIDemo(t *testing.T) {
	for _, slug := range libIThemes {
		t.Run(slug, func(t *testing.T) { libICheckDemo(t, slug) })
	}
}

func TestThemeLibraryI(t *testing.T) {
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
		for _, slug := range libIThemes {
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
	front := mustInsertPost(t, srv, &store.Post{Type: "page", AuthorID: 1, Slug: "welcome", Title: "Welcome",
		Content: `<!-- wp:group {"align":"full","layout":{"type":"constrained"}} --><div class="wp-block-group alignfull"><!-- wp:heading --><h2 class="wp-block-heading">Hello</h2><!-- /wp:heading --><!-- wp:latest-posts {"postsToShow":3,"postLayout":"grid","columns":3} /--></div><!-- /wp:group -->`})
	blog := mustInsertPost(t, srv, &store.Post{Type: "page", AuthorID: 1, Slug: "blog", Title: "Blog"})

	_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
	nonce := formNonce(t, body)

	for _, slug := range libIThemes {
		t.Run(slug, func(t *testing.T) {
			pkg := zipDir(t, filepath.Join(libIDir, slug))
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
				if strings.Contains(body, "Template error") || strings.Contains(body, "template:") || strings.Contains(body, "{{") {
					t.Fatalf("%s: template error or unreplaced token in %s", what, body)
				}
				for _, mk := range append([]string{styleLink, `class="site-header`, `id="colophon"`, "/js/navigation.js?ver=1.0.0"}, marks...) {
					if !strings.Contains(body, mk) {
						t.Fatalf("%s lacks %q", what, mk)
					}
				}
				return body
			}

			// Without the demo: blog on the front page and every kind of request.
			srv.purgeAll(ctx)
			check("home", base+"/", http.StatusOK, `class="post-list"`, "hfeed", hello.Title)
			check("single", abs(lb.Permalink(hello)), http.StatusOK, "entry--single", `class="comments-area"`, "A first comment.")
			check("page", abs(lb.Permalink(sample)), http.StatusOK, "entry--page")
			check("category", abs(lb.TermLink(cat)), http.StatusOK, "page-header--archive")
			check("search", base+"/?s=hello", http.StatusOK, "page-header--search")
			check("search, nothing found", base+"/?s=zzzqqq", http.StatusOK, "no-results")
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
				if st.BodyClass != "" && (strings.Contains(st.BodyClass, "{value}") || def == "1") {
					if cls := strings.ReplaceAll(st.BodyClass, "{value}", def); !strings.Contains(classes, " "+cls+" ") {
						t.Errorf("body class %q missing from %q", cls, bodyTag[1])
					}
				}
			}

			// A static front page and posts page made by hand.
			if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/settings",
				fmt.Sprintf(`{"show_on_front":"page","page_on_front":%d,"page_for_posts":%d}`, front.ID, blog.ID)); code != http.StatusOK {
				t.Fatalf("reading settings: %d %s", code, b)
			}
			srv.purgeAll(ctx)
			check("static front", base+"/", http.StatusOK, "wp-block-latest-posts")
			check("posts page", abs(lb.Permalink(blog)), http.StatusOK, `class="post-list"`, "page-header")
			if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/settings", `{"show_on_front":"posts","page_on_front":0,"page_for_posts":0}`); code != http.StatusOK {
				t.Fatalf("reading settings: %d %s", code, b)
			}

			// Assets.
			for _, a := range append([]string{"style.css", "editor-style.css", "js/navigation.js", "screenshot.png"}, th.PreloadFonts...) {
				if code, _, _ := get(t, c, base+"/wp-content/themes/"+slug+"/"+strings.TrimPrefix(a, "assets/")); code != http.StatusOK {
					t.Errorf("asset %s: %d", a, code)
				}
			}

			// The demo: import it, browse every page and post, then remove it.
			if _, err := themeDemoOf(th); err != nil {
				t.Fatalf("demo: %v", err)
			}
			rep, err := srv.importThemeDemo(ctx, 1, th)
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			b, _ := os.ReadFile(filepath.Join(libIDir, slug, "demo", "demo.json"))
			var d libIDemo
			_ = json.Unmarshal(b, &d)
			if rep.Pages != len(d.Pages) || rep.Posts != len(d.Posts) || rep.Media != len(d.Media) || rep.Menus != len(d.Menus) || !rep.Settings {
				t.Fatalf("import report %+v", rep)
			}
			srv.purgeAll(ctx)
			check("demo home", base+"/", http.StatusOK, libIFront[slug]...)
			for _, p := range d.Pages {
				id := srv.demoItemID(ctx, "posts", "page", slug, p.Slug)
				pg, err := srv.store.GetPost(ctx, id)
				if err != nil {
					t.Fatalf("demo page %s: %v", p.Slug, err)
				}
				switch {
				case p.FrontPage:
				case p.PostsPage:
					check("demo posts page", abs(lb.Permalink(pg)), http.StatusOK, `class="post-list"`)
				default:
					check("demo page "+p.Slug, abs(lb.Permalink(pg)), http.StatusOK, "entry--page")
				}
			}
			for i, p := range d.Posts {
				id := srv.demoItemID(ctx, "posts", "post", slug, p.Slug)
				ps, err := srv.store.GetPost(ctx, id)
				if err != nil {
					t.Fatalf("demo post %s: %v", p.Slug, err)
				}
				marks := []string{"entry--single", "post-thumbnail"}
				if i == 0 {
					marks = append(marks, "comment-list")
				}
				check("demo post "+p.Slug, abs(lb.Permalink(ps)), http.StatusOK, marks...)
			}
			for _, ct := range d.Categories {
				term, err := srv.store.TermBySlug(ctx, "category", ct.Slug)
				if err != nil {
					t.Fatalf("demo category %s: %v", ct.Slug, err)
				}
				check("demo category "+ct.Slug, abs(lb.TermLink(term)), http.StatusOK, "page-header--archive", `class="post-list"`)
			}
			check("demo search", base+"/?s=the", http.StatusOK, "page-header--search", `class="post-list"`)
			if err := srv.removeThemeDemo(ctx, slug); err != nil {
				t.Fatalf("remove demo: %v", err)
			}
			srv.purgeAll(ctx)
		})
	}
}
