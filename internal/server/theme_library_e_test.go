package server

import (
	"context"
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

// Theme library, group E: Sugar & Saffron, Hearth & Herb, Bean There, Nest & Nook and Petal & Stem.
// Each package is zipped from web/theme-library/<slug>, uploaded through Appearance › Add Themes ›
// Upload, activated and rendered for every kind of request; then its demo content is imported with
// importThemeDemo, the demo's pages, posts and archives are rendered, and the demo is removed again.

var libEThemes = []string{"sugar-and-saffron", "hearth-and-herb", "bean-there", "nest-and-nook", "petal-and-stem"}

const libEDir = "../../web/theme-library"

var libETokenRe = regexp.MustCompile(`\{\{(media|media_id|page|post|category|tag):([^}]*)\}\}`)

// libEDemo parses a package's manifest and demo with the real parsers and checks what they leave
// to the theme: library metadata, counts, image files, texts and the Customizer values.
func libEDemo(t *testing.T, slug string) (*themeManifest, *themeDemo) {
	t.Helper()
	dir := filepath.Join(libEDir, slug)
	mb, err := os.ReadFile(filepath.Join(dir, "theme.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := parseThemeManifest(mb)
	if err != nil {
		t.Fatalf("theme.json: %v", err)
	}
	if m.Slug != slug || m.Version != "1.0.0" || m.Category == "" || m.Style == "" || m.Demo != "demo/demo.json" {
		t.Fatalf("manifest metadata: slug %q version %q category %q style %q demo %q", m.Slug, m.Version, m.Category, m.Style, m.Demo)
	}
	d, err := parseThemeDemo(os.DirFS(dir), m)
	if err != nil {
		t.Fatalf("demo: %v", err)
	}

	// Images: 10–16, each referenced file present, ≤ 400 KB, with alt text; no stray files.
	if n := len(d.Media); n < 10 || n > 16 {
		t.Errorf("%d demo images, want 10–16", n)
	}
	files := map[string]bool{}
	for _, md := range d.Media {
		files[md.File] = true
		st, err := os.Stat(filepath.Join(dir, md.File))
		if err != nil {
			t.Errorf("media %s: %v", md.ID, err)
		} else if st.Size() > 400<<10 {
			t.Errorf("media %s is %d bytes, more than 400 KB", md.ID, st.Size())
		}
		if strings.TrimSpace(md.Alt) == "" {
			t.Errorf("media %s has no alt text", md.ID)
		}
	}
	onDisk, _ := filepath.Glob(filepath.Join(dir, "demo", "images", "*"))
	for _, f := range onDisk {
		if !files["demo/images/"+filepath.Base(f)] {
			t.Errorf("%s is not listed in the demo media", f)
		}
	}
	if st, err := os.Stat(filepath.Join(dir, "assets", "screenshot.png")); err != nil || st.Size() < 20<<10 {
		t.Errorf("screenshot.png missing or empty: %v", err)
	}
	fonts, _ := filepath.Glob(filepath.Join(dir, "assets", "fonts", "*.woff2"))
	var fontBytes int64
	for _, f := range fonts {
		st, _ := os.Stat(f)
		fontBytes += st.Size()
	}
	if fontBytes > 300<<10 {
		t.Errorf("fonts take %d bytes, more than 300 KB", fontBytes)
	}

	// Content: 6–10 posts with excerpts and at least three paragraphs, 3+ pages, no lorem ipsum.
	if n := len(d.Posts); n < 6 || n > 10 {
		t.Errorf("%d demo posts, want 6–10", n)
	}
	if n := len(d.Pages); n < 3 {
		t.Errorf("%d demo pages, want at least 3", n)
	}
	text := func(where, s string) {
		if strings.Contains(strings.ToLower(s), "lorem ipsum") {
			t.Errorf("%s: lorem ipsum", where)
		}
		for _, r := range libETokenRe.FindAllStringSubmatch(s, -1) {
			if r[2] == "" {
				t.Errorf("%s: empty token %s", where, r[0])
			}
		}
	}
	comments := 0
	for _, ps := range d.Posts {
		text("post "+ps.Slug, ps.Content)
		if ps.Excerpt == "" || ps.Date == "" || ps.FeaturedMedia == "" || len(ps.Categories) == 0 {
			t.Errorf("post %s: excerpt, date, featured image and a category are expected", ps.Slug)
		}
		if n := strings.Count(ps.Content, "<!-- wp:paragraph"); n < 3 {
			t.Errorf("post %s: %d paragraphs, want at least 3", ps.Slug, n)
		}
		if strings.Contains(ps.Title, "&") && strings.Contains(ps.Title, ";") {
			t.Errorf("post %s: the title holds an HTML entity: %q", ps.Slug, ps.Title)
		}
		comments += len(ps.Comments)
	}
	if comments < 3 {
		t.Errorf("%d demo comments, want a few", comments)
	}
	for _, pg := range d.Pages {
		text("page "+pg.Slug, pg.Content)
	}
	for area, ws := range d.Widgets {
		for _, w := range ws {
			text("widget "+area, w)
		}
	}
	if d.Options["blogname"] == nil || d.Options["blogdescription"] == nil {
		t.Error("the demo sets no blogname / blogdescription")
	}
	sub := false
	for _, mn := range d.Menus {
		for _, it := range mn.Items {
			sub = sub || len(it.Children) > 0
		}
	}
	if !sub {
		t.Error("no menu has a submenu")
	}

	// Customizer: 8+ settings in 2–4 sections, every select/checkbox with a class, every color a css var.
	n := 0
	for _, sec := range m.Customizer.Sections {
		for _, st := range sec.Settings {
			n++
			switch st.Type {
			case "color":
				if st.CSSVar == "" {
					t.Errorf("color setting %s has no css_var", st.ID)
				}
			case "select", "checkbox":
				if st.BodyClass == "" {
					t.Errorf("setting %s has no body_class", st.ID)
				}
			}
		}
	}
	if secs := len(m.Customizer.Sections); n < 8 || secs < 2 || secs > 4 {
		t.Errorf("%d settings in %d sections, want at least 8 in 2–4", n, secs)
	}
	if len(d.Customizer) == 0 {
		t.Error("the demo sets no Customizer values")
	}
	return m, d
}

func TestThemeLibraryEDemo(t *testing.T) {
	for _, slug := range libEThemes {
		t.Run(slug, func(t *testing.T) { libEDemo(t, slug) })
	}
}

func TestThemeLibraryE(t *testing.T) {
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
		for _, slug := range libEThemes {
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
	urls := map[string]string{"single": abs(lb.Permalink(hello)), "page": abs(lb.Permalink(sample)), "category": abs(lb.TermLink(cat)), "search": base + "/?s=hello"}

	_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
	nonce := formNonce(t, body)

	for _, slug := range libEThemes {
		t.Run(slug, func(t *testing.T) {
			_, d := libEDemo(t, slug)
			pkg := zipDir(t, filepath.Join(libEDir, slug))
			code, body, loc := postZip(t, c, base+"/wp-admin/theme-install", nonce, "themezip", slug+".zip", pkg)
			if code != http.StatusFound || loc != "/wp-admin/themes?installed="+slug {
				t.Fatalf("upload: %d %q %s", code, loc, body)
			}
			th := themeBySlug(slug)
			if th == nil || th.Package == nil {
				t.Fatalf("%s not registered", slug)
			}
			_, body, _ = get(t, c, base+"/wp-admin/themes")
			mm := regexp.MustCompile(`href="(/wp-admin/themes\?action=activate&amp;stylesheet=` + slug + `&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
			if mm == nil {
				t.Fatalf("no activate link for %s", slug)
			}
			if code, _, _ := get(t, c, base+html.UnescapeString(mm[1])); code != http.StatusFound {
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
				if strings.Contains(body, "Template error") || strings.Contains(body, "template:") || strings.Contains(body, "ZgotmplZ") {
					t.Fatalf("%s: template error in %s", what, body)
				}
				for _, mk := range append([]string{styleLink, `class="site-header`, `id="colophon"`, "/js/navigation.js?ver=1.0.0", `class="skip-link`}, marks...) {
					if !strings.Contains(body, mk) {
						t.Fatalf("%s lacks %q", what, mk)
					}
				}
				return body
			}

			// A fresh site: the blog on the front page and every kind of request.
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
				if st.BodyClass != "" && (strings.Contains(st.BodyClass, "{value}") || def == "1") {
					if cls := strings.ReplaceAll(st.BodyClass, "{value}", def); !strings.Contains(classes, " "+cls+" ") {
						t.Errorf("body class %q missing from %q", cls, bodyTag[1])
					}
				}
			}

			// A static front page and a posts page.
			frontMark := "entry--page"
			if libEHasTemplate(slug, "front-page") {
				frontMark = "entry--front"
			}
			if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/settings",
				fmt.Sprintf(`{"show_on_front":"page","page_on_front":%d,"page_for_posts":%d}`, front.ID, blog.ID)); code != http.StatusOK {
				t.Fatalf("reading settings: %d %s", code, b)
			}
			srv.purgeAll(ctx)
			check("static front", base+"/", http.StatusOK, frontMark, "wp-block-latest-posts")
			check("posts page", abs(lb.Permalink(blog)), http.StatusOK, `class="post-list"`, "page-header--blog")
			if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/settings", `{"show_on_front":"posts"}`); code != http.StatusOK {
				t.Fatalf("reading settings: %d %s", code, b)
			}

			// Assets: stylesheet, editor style, script, screenshot, fonts and SVG ornaments are served.
			assets := []string{"style.css", "editor-style.css", "js/navigation.js", "screenshot.png"}
			assets = append(assets, th.PreloadFonts...)
			extra, _ := filepath.Glob(filepath.Join(libEDir, slug, "assets", "img", "*.svg"))
			for _, f := range extra {
				assets = append(assets, "img/"+filepath.Base(f))
			}
			for _, a := range assets {
				if code, _, _ := get(t, c, base+"/wp-content/themes/"+slug+"/"+strings.TrimPrefix(a, "assets/")); code != http.StatusOK {
					t.Errorf("asset %s: %d", a, code)
				}
			}

			// Import the demo and render what it created.
			rep, err := srv.importThemeDemo(ctx, 1, th)
			if err != nil {
				t.Fatalf("import demo: %v", err)
			}
			if rep.Posts != len(d.Posts) || rep.Pages != len(d.Pages) || rep.Media != len(d.Media) {
				t.Fatalf("import report %+v for %d posts, %d pages, %d images", rep, len(d.Posts), len(d.Pages), len(d.Media))
			}
			srv.purgeAll(ctx)
			home := check("demo home", base+"/", http.StatusOK)
			if name, _ := d.Options["blogname"].(string); !strings.Contains(home, html.EscapeString(name)) {
				t.Errorf("demo home lacks the blog name %q", name)
			}
			if strings.Contains(home, "{{") {
				t.Error("an unreplaced demo token on the home page")
			}
			for _, pg := range d.Pages {
				p, err := srv.store.PostBySlug(ctx, "page", pg.Slug)
				if err != nil {
					t.Fatalf("demo page %s: %v", pg.Slug, err)
				}
				b := check("demo page "+pg.Slug, abs(lb.Permalink(p)), http.StatusOK)
				if strings.Contains(b, "{{") {
					t.Errorf("page %s: an unreplaced demo token", pg.Slug)
				}
			}
			for _, ps := range d.Posts {
				p, err := srv.store.PostBySlug(ctx, "post", ps.Slug)
				if err != nil {
					t.Fatalf("demo post %s: %v", ps.Slug, err)
				}
				b := check("demo post "+ps.Slug, abs(lb.Permalink(p)), http.StatusOK, "entry--single")
				if strings.Contains(b, "{{") {
					t.Errorf("post %s: an unreplaced demo token", ps.Slug)
				}
			}
			for _, ct := range d.Categories {
				term, err := srv.store.TermBySlug(ctx, "category", ct.Slug)
				if err != nil {
					t.Fatalf("demo category %s: %v", ct.Slug, err)
				}
				check("demo category "+ct.Slug, abs(lb.TermLink(term)), http.StatusOK, "page-header--archive")
			}
			check("demo search", base+"/?s=the", http.StatusOK, "page-header--search", `class="post-list"`)
			for k := range d.Customizer {
				if _, found := libESetting(th, k); !found && k != "custom_logo" {
					t.Errorf("demo customizer %s is not a setting", k)
				}
			}
			if err := srv.removeThemeDemo(ctx, slug); err != nil {
				t.Fatalf("remove demo: %v", err)
			}
			if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/settings", `{"show_on_front":"posts"}`); code != http.StatusOK {
				t.Fatalf("reading settings: %d %s", code, b)
			}
			srv.purgeAll(ctx)
		})
	}
}

func libEHasTemplate(slug, name string) bool {
	_, err := os.Stat(filepath.Join(libEDir, slug, "templates", name+".html"))
	return err == nil
}

func libESetting(th *Theme, id string) (themeCzSettings, bool) {
	for _, st := range th.Package.settings {
		if st.ID == id {
			return st, true
		}
	}
	return themeCzSettings{}, false
}
