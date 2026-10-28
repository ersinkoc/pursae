package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Theme library, group g: Pixel Arcade, Frame by Frame, Podwave, Ink & Needle and Little Sprouts.
// Each package is validated with the real manifest and demo parsers, zipped from
// web/theme-library/<slug>, uploaded through Appearance › Add Themes › Upload, activated, its demo
// imported with importThemeDemo, and every kind of request rendered; the demo is removed again so
// the next theme starts from a clean site.

var libGThemes = []string{"pixel-arcade", "frame-by-frame", "podwave", "ink-and-needle", "little-sprouts"}

const libGDir = "../../web/theme-library"

var libGTokenRe = regexp.MustCompile(`\{\{(media|media_id|page|post|category|tag):([^}]*)\}\}`)

// libGCheckPackage validates theme.json and demo/demo.json offline and returns the parsed demo.
func libGCheckPackage(t *testing.T, slug string) (*themeManifest, *themeDemo) {
	t.Helper()
	dir := filepath.Join(libGDir, slug)
	mb, err := os.ReadFile(filepath.Join(dir, "theme.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := parseThemeManifest(mb)
	if err != nil {
		t.Fatalf("theme.json: %v", err)
	}
	var lib struct{ Category, Style, Demo, Author, Version, RequiresCore string }
	_ = json.Unmarshal(mb, &lib)
	if lib.Category == "" || lib.Style == "" || len(lib.Style) > 80 || lib.Demo != "demo/demo.json" || m.Version != "1.0.0" || m.Author != "WordPress Go" {
		t.Fatalf("library metadata: %+v %s %s", lib, m.Version, m.Author)
	}
	if _, err := loadThemePackage(dir, slug); err != nil {
		t.Fatalf("package: %v", err)
	}
	d, err := parseThemeDemo(os.DirFS(dir), m)
	if err != nil {
		t.Fatalf("demo: %v", err)
	}

	// Customizer: at least 8 settings in 2–4 sections; demo values name real settings.
	settings := map[string]themeCzSettings{}
	for _, sec := range m.Customizer.Sections {
		for _, st := range sec.Settings {
			settings[st.ID] = st
		}
	}
	if len(settings) < 8 || len(m.Customizer.Sections) < 2 || len(m.Customizer.Sections) > 4 {
		t.Errorf("%d settings in %d sections, want at least 8 in 2–4", len(settings), len(m.Customizer.Sections))
	}
	for k := range d.Customizer {
		if _, ok := settings[k]; !ok && k != "custom_logo" {
			t.Errorf("demo customizer %q is not a setting of the theme", k)
		}
	}

	// Every referenced image, page, post, category, tag and media id exists.
	ids := map[string]map[string]bool{"media": {}, "page": {}, "post": {}, "category": {}, "tag": {}}
	for _, md := range d.Media {
		ids["media"][md.ID] = true
		st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(md.File)))
		if err != nil {
			t.Errorf("media %s: %v", md.ID, err)
		} else if st.Size() > 400<<10 {
			t.Errorf("media %s is %d KB, want at most 400 KB", md.ID, st.Size()>>10)
		}
		if md.Alt == "" {
			t.Errorf("media %s has no alt text", md.ID)
		}
	}
	if n := len(d.Media); n < 10 || n > 16 {
		t.Errorf("%d demo images, want 10–16", n)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "demo", "images", "*"))
	if len(files) != len(d.Media) {
		t.Errorf("%d files in demo/images, %d media entries", len(files), len(d.Media))
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
			t.Errorf("%s: the %s %q does not exist", where, kind, id)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "demo", "demo.json"))
	for _, mt := range libGTokenRe.FindAllStringSubmatch(string(raw), -1) {
		has(strings.TrimSuffix(mt[1], "_id"), mt[2], "token "+mt[0])
	}
	if strings.Contains(strings.ToLower(string(raw)), "lorem") {
		t.Error("demo contains lorem ipsum")
	}
	front, blog := false, false
	for _, p := range d.Pages {
		if p.FeaturedMedia != "" {
			has("media", p.FeaturedMedia, "page "+p.Slug)
		}
		if p.Parent != "" {
			has("page", p.Parent, "page "+p.Slug)
		}
		front, blog = front || p.FrontPage, blog || p.PostsPage
	}
	if n := len(d.Pages); n < 4 || n > 8 || !front || !blog {
		t.Errorf("%d pages (front %v, posts page %v)", n, front, blog)
	}
	if n := len(d.Posts); n < 6 || n > 10 {
		t.Errorf("%d posts, want 6–10", n)
	}
	comments := 0
	for _, p := range d.Posts {
		if p.FeaturedMedia != "" {
			has("media", p.FeaturedMedia, "post "+p.Slug)
		}
		for _, c := range p.Categories {
			has("category", c, "post "+p.Slug)
		}
		for _, tg := range p.Tags {
			has("tag", tg, "post "+p.Slug)
		}
		if strings.Count(p.Content, "<!-- wp:paragraph") < 3 || strings.Count(p.Content, "<!-- wp:") < 6 || p.Excerpt == "" {
			t.Errorf("post %s is too thin", p.Slug)
		}
		if dt, err := time.Parse("2006-01-02T15:04:05", p.Date); err != nil || dt.After(time.Now()) {
			t.Errorf("post %s: date %q", p.Slug, p.Date)
		}
		comments += len(p.Comments)
	}
	if comments < 3 {
		t.Errorf("%d comments, want a few", comments)
	}
	var walk func(menu string, items []demoMenuItem) bool
	walk = func(menu string, items []demoMenuItem) bool {
		sub := false
		for _, it := range items {
			switch {
			case it.Page != "":
				has("page", it.Page, "menu "+menu)
			case it.Post != "":
				has("post", it.Post, "menu "+menu)
			case it.Category != "":
				has("category", it.Category, "menu "+menu)
			case it.URL == "":
				t.Errorf("menu %s: %q links nowhere", menu, it.Title)
			}
			sub = sub || len(it.Children) > 0
			walk(menu, it.Children)
		}
		return sub
	}
	submenu := false
	for _, mn := range d.Menus {
		submenu = walk(mn.Name, mn.Items) || submenu
	}
	if !submenu {
		t.Error("no menu has a submenu")
	}
	if len(d.Widgets) == 0 || d.Options["blogname"] == nil || d.Options["blogdescription"] == nil {
		t.Error("demo lacks widgets or site options")
	}

	// Fonts stay small; the screenshot exists.
	var fonts int64
	fs, _ := filepath.Glob(filepath.Join(dir, "assets", "fonts", "*.woff2"))
	for _, f := range fs {
		if st, err := os.Stat(f); err == nil {
			fonts += st.Size()
		}
	}
	if fonts > 300<<10 {
		t.Errorf("fonts are %d KB, want at most 300 KB", fonts>>10)
	}
	if _, err := os.Stat(filepath.Join(dir, "assets", "screenshot.png")); err != nil {
		t.Error(err)
	}
	return m, d
}

func TestThemeLibraryGPackages(t *testing.T) {
	for _, slug := range libGThemes {
		t.Run(slug, func(t *testing.T) { libGCheckPackage(t, slug) })
	}
}

func TestThemeLibraryG(t *testing.T) {
	t.Setenv("WPGO_THEMES_DIR", t.TempDir())
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	installAndLogin(t, ts, c)
	t.Cleanup(func() {
		for _, slug := range libGThemes {
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

	for _, slug := range libGThemes {
		t.Run(slug, func(t *testing.T) {
			_, demo := libGCheckPackage(t, slug)

			// Install through Appearance › Add Themes › Upload Theme, then activate from Themes.
			_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
			code, body, loc := postZip(t, c, base+"/wp-admin/theme-install", formNonce(t, body), "themezip", slug+".zip", zipDir(t, filepath.Join(libGDir, slug)))
			if code != http.StatusFound || loc != "/wp-admin/themes?installed="+slug {
				t.Fatalf("upload: %d %q %s", code, loc, body)
			}
			th := themeBySlug(slug)
			if th == nil || th.Package == nil {
				t.Fatalf("%s is not registered", slug)
			}
			_, body, _ = get(t, c, base+"/wp-admin/themes")
			am := regexp.MustCompile(`href="(/wp-admin/themes\?action=activate&amp;stylesheet=` + slug + `&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
			if am == nil {
				t.Fatal("no activate link")
			}
			if code, _, _ := get(t, c, base+html.UnescapeString(am[1])); code != http.StatusFound || srv.activeTheme().Slug != slug {
				t.Fatalf("activate: %d, active %s", code, srv.activeTheme().Slug)
			}

			styleLink := fmt.Sprintf(`href='/wp-content/themes/%s/style.css?ver=1.0.0'`, slug)
			page := func(what, u string, want int, marks ...string) string {
				t.Helper()
				code, body, _ := get(t, c, abs(u))
				if code != want {
					t.Fatalf("%s %s: %d (want %d)", what, u, code, want)
				}
				if strings.Contains(body, "Template error") || strings.Contains(body, "<no value>") || strings.Contains(body, "{{") {
					t.Fatalf("%s: template error or unreplaced token", what)
				}
				for _, mk := range append([]string{styleLink, `class="site-header`, `id="colophon"`, "/js/navigation.js?ver=1.0.0"}, marks...) {
					if !strings.Contains(body, mk) {
						t.Fatalf("%s lacks %q", what, mk)
					}
				}
				return body
			}

			// The bare site (blog on the front page, no demo) renders.
			srv.purgeAll(ctx)
			page("blog home", "/", http.StatusOK, "post-list")

			// Import the demo, as Install & Import Demo does.
			rep, err := srv.importThemeDemo(ctx, 1, th)
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if rep.Posts != len(demo.Posts) || rep.Pages != len(demo.Pages) || rep.Media != len(demo.Media) || rep.Menus != len(demo.Menus) {
				t.Fatalf("import report %+v", rep)
			}
			t.Cleanup(func() { _ = srv.removeThemeDemo(ctx, slug) })
			srv.purgeAll(ctx)

			// Static front page with the hero, every Customizer css var and body class.
			body = page("front page", "/", http.StatusOK, "entry--front")
			for _, st := range th.Package.settings {
				if st.CSSVar != "" && !strings.Contains(body, st.CSSVar+":") {
					t.Errorf("css var %s missing", st.CSSVar)
				}
			}
			if bt := regexp.MustCompile(`<body class="([^"]*)"`).FindStringSubmatch(body); bt == nil || !strings.Contains(bt[1], slug[:2]) {
				t.Errorf("body classes: %v", bt)
			}
			if !strings.Contains(body, `class="main-navigation`) || !strings.Contains(body, "sub-menu") {
				t.Error("front page: the demo menu with a submenu is not shown")
			}

			var postsPage, otherPage string
			for _, p := range demo.Pages {
				switch {
				case p.PostsPage:
					postsPage = p.Slug
				case !p.FrontPage && otherPage == "":
					otherPage = p.Slug
				}
			}
			pp, err := srv.store.PostBySlug(ctx, "page", postsPage)
			if err != nil {
				t.Fatal(err)
			}
			page("posts page", lb.Permalink(pp), http.StatusOK, "post-list")
			for _, p := range demo.Pages {
				if p.FrontPage || p.PostsPage {
					continue
				}
				pg, err := srv.store.PostBySlug(ctx, "page", p.Slug)
				if err != nil {
					t.Fatalf("page %s: %v", p.Slug, err)
				}
				page("page "+p.Slug, lb.Permalink(pg), http.StatusOK, "entry--page", html.EscapeString(p.Title))
			}
			_ = otherPage

			// Every post renders; the first with comments shows them.
			for _, p := range demo.Posts {
				ps, err := srv.store.PostBySlug(ctx, "post", p.Slug)
				if err != nil {
					t.Fatalf("post %s: %v", p.Slug, err)
				}
				body := page("post "+p.Slug, lb.Permalink(ps), http.StatusOK, "entry--single", `class="comments-area"`)
				if len(p.Comments) > 0 && !strings.Contains(body, "comment-list") {
					t.Errorf("post %s: comments missing", p.Slug)
				}
			}

			cat, err := srv.store.TermBySlug(ctx, "category", demo.Categories[0].Slug)
			if err != nil {
				t.Fatal(err)
			}
			page("category", lb.TermLink(cat), http.StatusOK, "page-header--archive", "post-list")
			if len(demo.Tags) > 0 {
				tag, err := srv.store.TermBySlug(ctx, "post_tag", demo.Tags[0].Slug)
				if err == nil {
					page("tag", lb.TermLink(tag), http.StatusOK, "page-header--archive")
				}
			}
			word := strings.Fields(demo.Posts[0].Title)[0]
			page("search", "/?s="+word, http.StatusOK, "page-header--search", "post-list")
			page("empty search", "/?s=zzqxnothingmatches", http.StatusOK, "page-header--search", "no-results")
			page("404", "/wpgo-no-such-page-"+slug+"/", http.StatusNotFound, "error-404")

			// Assets are served.
			for _, a := range append([]string{"style.css", "editor-style.css", "js/navigation.js", "screenshot.png"}, th.PreloadFonts...) {
				if code, _, _ := get(t, c, base+"/wp-content/themes/"+slug+"/"+strings.TrimPrefix(a, "assets/")); code != http.StatusOK {
					t.Errorf("asset %s: %d", a, code)
				}
			}

			if err := srv.removeThemeDemo(ctx, slug); err != nil {
				t.Fatalf("remove demo: %v", err)
			}
			srv.purgeAll(ctx)
		})
	}
}
