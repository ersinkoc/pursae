package server

import (
	"encoding/json"
	"html"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Theme library, group b: Lumen Folio, Atelier Nord, Studio Fizz, Velvet Runway and Encore. Each
// package directory is zipped in memory, uploaded through Appearance › Add Themes › Upload Theme,
// activated and rendered for the main kinds of request; its demo content file is checked for
// dangling references.

var themeLibraryB = []string{"lumen-folio", "atelier-nord", "studio-fizz", "velvet-runway", "encore"}

const libBDir = "../../web/theme-library"

// libBDemoFile mirrors demo/demo.json (docs/theme-package.md, "Demo content").
type libBDemoFile struct {
	Version    int                 `json:"version"`
	Options    map[string]any      `json:"options"`
	Categories []libBDemoTerm      `json:"categories"`
	Tags       []libBDemoTerm      `json:"tags"`
	Media      []libBDemoMedia     `json:"media"`
	Pages      []libBDemoPost      `json:"pages"`
	Posts      []libBDemoPost      `json:"posts"`
	Menus      []libBDemoMenu      `json:"menus"`
	Widgets    map[string][]string `json:"widgets"`
	Customizer map[string]any      `json:"customizer"`
}

type libBDemoTerm struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Parent      string `json:"parent"`
}

type libBDemoMedia struct {
	ID      string `json:"id"`
	File    string `json:"file"`
	Title   string `json:"title"`
	Alt     string `json:"alt"`
	Caption string `json:"caption"`
}

type libBDemoPost struct {
	Slug          string   `json:"slug"`
	Title         string   `json:"title"`
	Date          string   `json:"date"`
	Excerpt       string   `json:"excerpt"`
	Content       string   `json:"content"`
	Template      string   `json:"template"`
	Parent        string   `json:"parent"`
	FeaturedMedia string   `json:"featured_media"`
	MenuOrder     int      `json:"menu_order"`
	FrontPage     bool     `json:"front_page"`
	PostsPage     bool     `json:"posts_page"`
	Categories    []string `json:"categories"`
	Tags          []string `json:"tags"`
	Format        string   `json:"format"`
	Sticky        bool     `json:"sticky"`
	Comments      []struct {
		Author  string `json:"author"`
		Email   string `json:"email"`
		URL     string `json:"url"`
		Content string `json:"content"`
		Date    string `json:"date"`
	} `json:"comments"`
}

type libBDemoMenu struct {
	Name     string             `json:"name"`
	Location string             `json:"location"`
	Items    []libBDemoMenuItem `json:"items"`
}

type libBDemoMenuItem struct {
	Title    string             `json:"title"`
	Page     string             `json:"page"`
	Post     string             `json:"post"`
	Category string             `json:"category"`
	URL      string             `json:"url"`
	Children []libBDemoMenuItem `json:"children"`
}

var libBCategories = map[string]bool{"blog": true, "magazine": true, "portfolio": true, "photography": true, "business": true, "agency": true,
	"restaurant": true, "food": true, "travel": true, "fashion": true, "technology": true, "education": true, "health": true, "fitness": true,
	"nonprofit": true, "music": true, "wedding": true, "real-estate": true, "personal": true, "minimal": true}

var libBPlaceholderRe = regexp.MustCompile(`\{\{(media|media_id|page|post|category|tag):([a-z0-9_-]+)\}\}`)

// TestThemeLibraryBPackages checks the packages without a database: the manifest validates and
// carries the library metadata, the templates parse, the assets stay within the budgets and the
// demo content references only things it defines.
func TestThemeLibraryBPackages(t *testing.T) {
	for _, slug := range themeLibraryB {
		t.Run(slug, func(t *testing.T) {
			dir := filepath.Join(libBDir, slug)
			files, m, err := unpackThemeZip(zipDir(t, dir))
			if err != nil {
				t.Fatalf("package: %v", err)
			}
			if m.Slug != slug || m.Version != "1.0.0" || m.RequiresCore != "0.1.0" || m.Author != "Pursae" {
				t.Fatalf("manifest identity: %s %s %s %s", m.Slug, m.Version, m.RequiresCore, m.Author)
			}
			if _, err := parsePackageTemplates(os.DirFS(dir), "templates", (&templates{s: &Server{}}).funcs()); err != nil {
				t.Fatalf("templates: %v", err)
			}
			if _, err := loadThemePackage(dir, slug); err != nil {
				t.Fatalf("load: %v", err)
			}

			var meta struct {
				Category, Style, Demo string
			}
			b, _ := os.ReadFile(filepath.Join(dir, "theme.json"))
			if err := json.Unmarshal(b, &meta); err != nil {
				t.Fatal(err)
			}
			if !libBCategories[meta.Category] || meta.Style == "" || meta.Demo != "demo/demo.json" {
				t.Fatalf("library metadata: %+v", meta)
			}
			settings := 0
			for _, sec := range m.Customizer.Sections {
				settings += len(sec.Settings)
			}
			if n := len(m.Customizer.Sections); n < 2 || n > 4 || settings < 8 {
				t.Fatalf("customizer: %d sections, %d settings", n, settings)
			}
			if len(m.Palette) == 0 || len(m.EditorStyles) == 0 || len(m.Supports) == 0 {
				t.Fatal("palette, editor styles or supports missing")
			}

			// Budgets: fonts ≤ 300 KB, images ≤ 400 KB, a 1200×900 screenshot.
			var fonts int
			for _, f := range files {
				switch {
				case strings.HasPrefix(f.Path, "assets/fonts/"):
					fonts += len(f.Data)
				case strings.HasPrefix(f.Path, "demo/images/"):
					if len(f.Data) > 400<<10 {
						t.Errorf("%s is %d KB", f.Path, len(f.Data)>>10)
					}
				}
			}
			if fonts > 300<<10 {
				t.Errorf("fonts are %d KB", fonts>>10)
			}
			if sf, err := os.Open(filepath.Join(dir, "assets", "screenshot.png")); err != nil {
				t.Error(err)
			} else {
				cfg, format, err := image.DecodeConfig(sf)
				sf.Close()
				if err != nil || format != "png" || cfg.Width != 1200 || cfg.Height != 900 {
					t.Errorf("screenshot: %v %s %dx%d", err, format, cfg.Width, cfg.Height)
				}
			}

			libBCheckDemo(t, dir, m.MenuLocations, m.WidgetAreas, m.Customizer.Sections)
		})
	}
}

// libBCheckDemo unmarshals demo/demo.json and checks that every referenced media, page, post,
// category, tag, menu location, widget area and Customizer setting exists.
func libBCheckDemo(t *testing.T, dir string, locations []themeManifestLocation, areas []themeManifestArea, sections []themeCzSection) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "demo", "demo.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var d libBDemoFile
	if err := dec.Decode(&d); err != nil {
		t.Fatalf("demo.json: %v", err)
	}
	if d.Version != 1 {
		t.Fatalf("demo version %d", d.Version)
	}
	for k := range d.Options {
		if k != "blogname" && k != "blogdescription" && k != "posts_per_page" && k != "date_format" {
			t.Errorf("option %q is not allowed", k)
		}
	}
	if d.Options["blogname"] == nil || d.Options["blogdescription"] == nil {
		t.Error("blogname/blogdescription missing")
	}

	media, pages, posts, cats, tags := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	if n := len(d.Media); n < 10 || n > 24 {
		t.Errorf("%d demo images (want 10-24)", n)
	}
	for _, m := range d.Media {
		if media[m.ID] {
			t.Errorf("media %q twice", m.ID)
		}
		media[m.ID] = true
		if m.Alt == "" {
			t.Errorf("media %q has no alt text", m.ID)
		}
		st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(m.File)))
		if err != nil || !strings.HasPrefix(m.File, "demo/images/") || st.Size() > 1<<20 {
			t.Errorf("media %q: file %s: %v", m.ID, m.File, err)
		}
	}
	for _, c := range d.Categories {
		cats[c.Slug] = true
	}
	for _, tg := range d.Tags {
		tags[tg.Slug] = true
	}
	for _, p := range d.Pages {
		pages[p.Slug] = true
	}
	for _, p := range d.Posts {
		posts[p.Slug] = true
	}
	if n := len(d.Posts); n < 6 || n > 10 {
		t.Errorf("%d posts (want 6-10)", n)
	}
	if n := len(d.Pages); n < 3 || n > 7 {
		t.Errorf("%d pages (want 3-6, plus a posts page)", n)
	}
	if !pages["about"] || !pages["contact"] {
		t.Error("About and Contact pages are required")
	}
	checkContent := func(what, content string) {
		if strings.Contains(strings.ToLower(content), "lorem") {
			t.Errorf("%s: lorem ipsum", what)
		}
		for _, m := range libBPlaceholderRe.FindAllStringSubmatch(content, -1) {
			ok := map[string]map[string]bool{"media": media, "media_id": media, "page": pages, "post": posts, "category": cats, "tag": tags}[m[1]][m[2]]
			if !ok {
				t.Errorf("%s: %s does not exist", what, m[0])
			}
		}
		if strings.Count(content, "<!-- wp:") != strings.Count(content, "<!-- /wp:")+strings.Count(content, "/-->") {
			t.Errorf("%s: unbalanced block comments", what)
		}
	}
	fronts, postsPages := 0, 0
	for _, p := range d.Pages {
		if p.Parent != "" && !pages[p.Parent] {
			t.Errorf("page %s: parent %q missing", p.Slug, p.Parent)
		}
		if p.FeaturedMedia != "" && !media[p.FeaturedMedia] {
			t.Errorf("page %s: featured media %q missing", p.Slug, p.FeaturedMedia)
		}
		if p.FrontPage {
			fronts++
		}
		if p.PostsPage {
			postsPages++
		}
		checkContent("page "+p.Slug, p.Content)
	}
	if fronts > 1 || postsPages > 1 || (fronts == 1) != (postsPages == 1) {
		t.Errorf("front page %d / posts page %d", fronts, postsPages)
	}
	for _, p := range d.Posts {
		if p.FeaturedMedia != "" && !media[p.FeaturedMedia] {
			t.Errorf("post %s: featured media %q missing", p.Slug, p.FeaturedMedia)
		}
		for _, c := range p.Categories {
			if !cats[c] {
				t.Errorf("post %s: category %q missing", p.Slug, c)
			}
		}
		for _, tg := range p.Tags {
			if !tags[tg] {
				t.Errorf("post %s: tag %q missing", p.Slug, tg)
			}
		}
		if dt, err := time.Parse("2006-01-02T15:04:05", p.Date); err != nil || dt.After(time.Now()) {
			t.Errorf("post %s: date %q is missing, malformed or in the future", p.Slug, p.Date)
		}
		for _, cm := range p.Comments {
			if cm.Author == "" || cm.Content == "" || !strings.Contains(cm.Email, "@") {
				t.Errorf("post %s: incomplete comment", p.Slug)
			}
			if _, err := time.Parse("2006-01-02T15:04:05", cm.Date); err != nil {
				t.Errorf("post %s: comment date %q", p.Slug, cm.Date)
			}
		}
		if strings.Count(p.Content, "<!-- wp:paragraph") < 3 {
			t.Errorf("post %s: fewer than 3 paragraphs", p.Slug)
		}
		checkContent("post "+p.Slug, p.Content)
	}
	locs := map[string]bool{}
	for _, l := range locations {
		locs[l.ID] = true
	}
	var walk func(menu string, items []libBDemoMenuItem, depth int) bool
	walk = func(menu string, items []libBDemoMenuItem, depth int) bool {
		nested := depth > 0 && len(items) > 0
		for _, it := range items {
			n := 0
			for _, v := range []string{it.Page, it.Post, it.Category, it.URL} {
				if v != "" {
					n++
				}
			}
			if n > 1 || (it.URL != "" && it.Title == "") {
				t.Errorf("menu %s: item %q needs exactly one target (and a title for a url)", menu, it.Title)
			}
			switch {
			case it.Page != "" && !pages[it.Page], it.Post != "" && !posts[it.Post], it.Category != "" && !cats[it.Category]:
				t.Errorf("menu %s: item %q points at a missing target", menu, it.Title)
			case it.Page == "" && it.Post == "" && it.Category == "" && it.URL == "":
				t.Errorf("menu %s: item %q has no target", menu, it.Title)
			}
			if walk(menu, it.Children, depth+1) {
				nested = true
			}
		}
		return nested
	}
	submenu := false
	for _, mn := range d.Menus {
		if !locs[mn.Location] {
			t.Errorf("menu %s: location %q is not registered", mn.Name, mn.Location)
		}
		if walk(mn.Name, mn.Items, 0) && mn.Location == "primary" {
			submenu = true
		}
	}
	if !submenu {
		t.Error("the primary menu has no submenu")
	}
	ids := map[string]bool{}
	for _, a := range areas {
		ids[a.ID] = true
	}
	for area, ws := range d.Widgets {
		if !ids[area] || len(ws) == 0 {
			t.Errorf("widgets for %q: area not registered or empty", area)
		}
		for i, w := range ws {
			checkContent("widget "+area, w)
			if !strings.HasPrefix(w, "<!-- wp:") {
				t.Errorf("widget %s[%d] is not a block", area, i)
			}
		}
	}
	settings := map[string]themeCzSettings{}
	for _, sec := range sections {
		for _, st := range sec.Settings {
			settings[st.ID] = st
		}
	}
	for k, v := range d.Customizer {
		st, ok := settings[k]
		if !ok {
			t.Errorf("customizer %q is not a setting of the theme", k)
			continue
		}
		if st.Type == "image" {
			if s, _ := v.(string); !media[strings.TrimSuffix(strings.TrimPrefix(s, "{{media:"), "}}")] {
				t.Errorf("customizer %q = %v is not a media id of the demo", k, v)
			}
			continue
		}
		cp := st
		cp.Default = v
		if _, err := themeCzDefault(cp); err != nil {
			t.Errorf("customizer %q = %v: %v", k, v, err)
		}
	}
}

// TestThemeLibraryBRender installs each package through the upload form, activates it and renders
// the front page, a post, a page, a category archive, a search and a 404.
func TestThemeLibraryBRender(t *testing.T) {
	t.Setenv("WPGO_THEMES_DIR", t.TempDir())
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)

	// fetch follows same-site redirects (canonical URLs) like a browser.
	fetch := func(u string) (int, string) {
		t.Helper()
		for i := 0; i < 5; i++ {
			code, body, loc := get(t, c, base+u)
			if code != http.StatusMovedPermanently && code != http.StatusFound {
				return code, body
			}
			lu, err := url.Parse(loc)
			if err != nil {
				t.Fatal(err)
			}
			u = lu.RequestURI()
		}
		t.Fatalf("too many redirects for %s", u)
		return 0, ""
	}

	for _, slug := range themeLibraryB {
		t.Run(slug, func(t *testing.T) {
			code, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
			if code != http.StatusOK {
				t.Fatalf("upload form: %d", code)
			}
			nonce := formNonce(t, body)
			code, body, loc := postZip(t, c, base+"/wp-admin/theme-install", nonce, "themezip", slug+".zip", zipDir(t, filepath.Join(libBDir, slug)))
			if code != http.StatusFound || loc != "/wp-admin/themes?installed="+slug {
				t.Fatalf("upload: %d %q %s", code, loc, body)
			}
			t.Cleanup(func() { unregisterTheme(slug) })

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

			stylesheet := `id='` + slug + `-style-css' href='/wp-content/themes/` + slug + `/style.css?ver=1.0.0'`
			for _, tc := range []struct {
				name, url string
				want      int
			}{
				{"home", "/", http.StatusOK},
				{"post", "/?p=1", http.StatusOK},
				{"page", "/?page_id=2", http.StatusOK},
				{"category", "/?cat=1", http.StatusOK},
				{"search", "/?s=hello", http.StatusOK},
				{"search without results", "/?s=zzqx-nothing", http.StatusOK},
				{"404", "/wpgo-no-such-page/", http.StatusNotFound},
			} {
				code, body := fetch(tc.url)
				if code != tc.want {
					t.Errorf("%s: %d (want %d)", tc.name, code, tc.want)
					continue
				}
				if !strings.Contains(body, stylesheet) {
					t.Errorf("%s: the theme stylesheet is not linked", tc.name)
				}
				if strings.Contains(body, "Template error") || !strings.Contains(body, "</html>") {
					t.Errorf("%s: the page did not render completely", tc.name)
				}
				if !strings.Contains(body, `class="site-title"`) || !strings.Contains(body, `id="colophon"`) {
					t.Errorf("%s: header or footer missing", tc.name)
				}
			}
			if code, body := fetch("/?p=1"); code == http.StatusOK && !strings.Contains(body, "Hello world") {
				t.Error("the post did not render its title")
			}
			for _, asset := range []string{"style.css", "js/navigation.js", "screenshot.png"} {
				if code, _, _ := get(t, c, base+"/wp-content/themes/"+slug+"/"+asset); code != http.StatusOK {
					t.Errorf("%s: %d", asset, code)
				}
			}
		})
	}
}
