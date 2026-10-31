package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Theme library, group a: inkwell, paper-lantern, headline-daily, quiet-hours and byte-notes.
// TestThemeLibraryAPackages validates each package and its demo content without a database;
// TestThemeLibraryAInstall installs every package through Upload Theme, activates it, imports a
// slice of its demo content over REST and renders the main template kinds.

const themeLibraryDir = "../../web/theme-library"

var themeLibraryASlugs = []string{"inkwell", "paper-lantern", "headline-daily", "quiet-hours", "byte-notes"}

var themeLibraryCategories = map[string]bool{"blog": true, "magazine": true, "portfolio": true, "photography": true, "business": true,
	"agency": true, "restaurant": true, "food": true, "travel": true, "fashion": true, "technology": true, "education": true,
	"health": true, "fitness": true, "nonprofit": true, "music": true, "wedding": true, "real-estate": true, "personal": true, "minimal": true}

// themeLibraryDemo is demo/demo.json (docs/theme-package.md, "Demo content").
type themeLibraryDemo struct {
	Version    int            `json:"version"`
	Options    map[string]any `json:"options"`
	Categories []struct {
		Slug, Name, Description, Parent string
	} `json:"categories"`
	Tags []struct {
		Slug, Name string
	} `json:"tags"`
	Media []struct {
		ID, File, Alt, Caption string
	} `json:"media"`
	Pages []struct {
		Slug          string `json:"slug"`
		Title         string `json:"title"`
		Content       string `json:"content"`
		Template      string `json:"template"`
		FeaturedMedia string `json:"featured_media"`
		FrontPage     bool   `json:"front_page"`
		PostsPage     bool   `json:"posts_page"`
		Parent        string `json:"parent"`
	} `json:"pages"`
	Posts []struct {
		Slug          string   `json:"slug"`
		Title         string   `json:"title"`
		Date          string   `json:"date"`
		Excerpt       string   `json:"excerpt"`
		Content       string   `json:"content"`
		Categories    []string `json:"categories"`
		Tags          []string `json:"tags"`
		FeaturedMedia string   `json:"featured_media"`
		Format        string   `json:"format"`
		Sticky        bool     `json:"sticky"`
		Comments      []struct {
			Author, Email, Content, Date string
		} `json:"comments"`
	} `json:"posts"`
	Menus []struct {
		Name     string                 `json:"name"`
		Location string                 `json:"location"`
		Items    []themeLibraryDemoItem `json:"items"`
	} `json:"menus"`
	Widgets    map[string][]string `json:"widgets"`
	Customizer map[string]any      `json:"customizer"`
}

type themeLibraryDemoItem struct {
	Title    string                 `json:"title"`
	Page     string                 `json:"page"`
	Post     string                 `json:"post"`
	Category string                 `json:"category"`
	URL      string                 `json:"url"`
	Children []themeLibraryDemoItem `json:"children"`
}

func readThemeLibraryDemo(t *testing.T, dir string) *themeLibraryDemo {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "demo", "demo.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d themeLibraryDemo
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("demo.json: %v", err)
	}
	return &d
}

var themeLibraryPlaceholderRe = regexp.MustCompile(`\{\{(media|media_id|page|post|category|tag):([a-z0-9-]+)\}\}`)

func TestThemeLibraryAPackages(t *testing.T) {
	funcs := (&templates{s: &Server{}}).funcs()
	for _, slug := range themeLibraryASlugs {
		t.Run(slug, func(t *testing.T) {
			dir := filepath.Join(themeLibraryDir, slug)
			files, m, err := unpackThemeZip(zipDir(t, dir))
			if err != nil {
				t.Fatalf("package: %v", err)
			}
			if m.Slug != slug || m.Version != "1.0.0" || m.RequiresCore != "0.1.0" || m.Author != "Pursae" {
				t.Fatalf("manifest identity: %s %s %s %s", m.Slug, m.Version, m.RequiresCore, m.Author)
			}
			if _, err := loadThemePackage(dir, slug); err != nil {
				t.Fatalf("load: %v", err)
			}
			if _, err := parsePackageTemplates(os.DirFS(dir), "templates", funcs); err != nil {
				t.Fatalf("templates: %v", err)
			}
			for _, name := range []string{"index", "header", "footer", "single", "page", "archive", "search", "404", "comments", "searchform"} {
				if _, err := os.Stat(filepath.Join(dir, "templates", name+".html")); err != nil {
					t.Errorf("templates/%s.html missing", name)
				}
			}
			for _, f := range []string{"readme.md", "assets/style.css", "assets/editor-style.css", "assets/screenshot.png"} {
				if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
					t.Errorf("%s missing", f)
				}
			}

			// Library metadata.
			var meta struct {
				Category, Style, Demo string
				Palette               []any
			}
			raw, _ := os.ReadFile(filepath.Join(dir, "theme.json"))
			if err := json.Unmarshal(raw, &meta); err != nil {
				t.Fatal(err)
			}
			if !themeLibraryCategories[meta.Category] || strings.TrimSpace(meta.Style) == "" || len(meta.Style) > 80 || strings.ContainsAny(meta.Style, "<>\n") || meta.Demo != "demo/demo.json" || len(meta.Palette) == 0 {
				t.Fatalf("library metadata: %+v", meta)
			}
			settings := map[string]themeCzSettings{}
			for _, sec := range m.Customizer.Sections {
				for _, st := range sec.Settings {
					settings[st.ID] = st
				}
			}
			if n := len(m.Customizer.Sections); n < 2 || n > 4 {
				t.Errorf("%d customizer sections, want 2–4", n)
			}
			if len(settings) < 8 {
				t.Errorf("%d customizer settings, want at least 8", len(settings))
			}

			// Assets: fonts ≤ 300 KB, images ≤ 400 KB, 10–16 demo images.
			var fontBytes, images int
			inPkg := map[string]int{}
			for _, f := range files {
				inPkg[f.Path] = len(f.Data)
				ext := strings.ToLower(path.Ext(f.Path))
				if strings.HasPrefix(f.Path, "assets/fonts/") {
					fontBytes += len(f.Data)
				}
				if strings.HasPrefix(f.Path, "demo/images/") {
					images++
					if ext != ".jpg" && ext != ".png" && ext != ".webp" {
						t.Errorf("%s: demo images are .jpg, .png or .webp", f.Path)
					}
					if len(f.Data) > 400<<10 {
						t.Errorf("%s is %d KB (max 400)", f.Path, len(f.Data)>>10)
					}
				}
			}
			if fontBytes > 300<<10 {
				t.Errorf("fonts total %d KB (max 300)", fontBytes>>10)
			}
			if images < 10 || images > 16 {
				t.Errorf("%d demo images, want 10–16", images)
			}

			// Demo content: every reference resolves.
			d := readThemeLibraryDemo(t, dir)
			if d.Version != 1 {
				t.Errorf("demo version %d", d.Version)
			}
			for k := range d.Options {
				if k != "blogname" && k != "blogdescription" && k != "posts_per_page" && k != "date_format" {
					t.Errorf("option %s may not be set by a demo", k)
				}
			}
			if d.Options["blogname"] == nil || d.Options["blogdescription"] == nil {
				t.Error("blogname/blogdescription missing")
			}
			media, pages, posts, cats, tags := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
			for _, md := range d.Media {
				if media[md.ID] {
					t.Errorf("media %s twice", md.ID)
				}
				media[md.ID] = true
				if _, ok := inPkg[md.File]; !ok || !strings.HasPrefix(md.File, "demo/images/") {
					t.Errorf("media %s: %s not in the package", md.ID, md.File)
				}
				if strings.TrimSpace(md.Alt) == "" {
					t.Errorf("media %s has no alt text", md.ID)
				}
			}
			for _, c := range d.Categories {
				cats[c.Slug] = true
			}
			for _, c := range d.Categories {
				if c.Parent != "" && !cats[c.Parent] {
					t.Errorf("category %s: parent %s", c.Slug, c.Parent)
				}
			}
			for _, tg := range d.Tags {
				tags[tg.Slug] = true
			}
			front, postsPage := 0, 0
			for _, p := range d.Pages {
				if pages[p.Slug] {
					t.Errorf("page %s twice", p.Slug)
				}
				pages[p.Slug] = true
				if p.FrontPage {
					front++
				}
				if p.PostsPage {
					postsPage++
				}
			}
			if front > 1 || postsPage > 1 {
				t.Errorf("front pages %d, posts pages %d", front, postsPage)
			}
			for _, p := range d.Posts {
				posts[p.Slug] = true
			}
			if n := len(d.Posts); n < 6 || n > 10 {
				t.Errorf("%d posts, want 6–10", n)
			}
			if n := len(d.Pages); n < 3 || n > 6 {
				t.Errorf("%d pages, want 3–6", n)
			}
			if !pages["about"] || !pages["contact"] {
				t.Error("about and contact pages are required")
			}
			refs := map[string]map[string]bool{"media": media, "media_id": media, "page": pages, "post": posts, "category": cats, "tag": tags}
			checkContent := func(what, content string) {
				for _, m := range themeLibraryPlaceholderRe.FindAllStringSubmatch(content, -1) {
					if !refs[m[1]][m[2]] {
						t.Errorf("%s: %s does not resolve", what, m[0])
					}
				}
				if strings.Contains(strings.ToLower(content), "lorem ipsum") {
					t.Errorf("%s: lorem ipsum", what)
				}
			}
			comments := 0
			for _, p := range d.Pages {
				if p.Parent != "" && !pages[p.Parent] {
					t.Errorf("page %s: parent %s", p.Slug, p.Parent)
				}
				if p.FeaturedMedia != "" && !media[p.FeaturedMedia] {
					t.Errorf("page %s: featured media %s", p.Slug, p.FeaturedMedia)
				}
				if p.Template != "" {
					if _, err := os.Stat(filepath.Join(dir, "templates", p.Template+".html")); err != nil {
						t.Errorf("page %s: template %s is not in templates/", p.Slug, p.Template)
					}
				}
				checkContent("page "+p.Slug, p.Content)
			}
			for _, p := range d.Posts {
				for _, c := range p.Categories {
					if !cats[c] {
						t.Errorf("post %s: category %s", p.Slug, c)
					}
				}
				for _, tg := range p.Tags {
					if !tags[tg] {
						t.Errorf("post %s: tag %s", p.Slug, tg)
					}
				}
				if p.FeaturedMedia != "" && !media[p.FeaturedMedia] {
					t.Errorf("post %s: featured media %s", p.Slug, p.FeaturedMedia)
				}
				if p.Date != "" {
					if when, err := time.Parse("2006-01-02T15:04:05", p.Date); err != nil || when.After(time.Now()) {
						t.Errorf("post %s: date %q is unreadable or in the future", p.Slug, p.Date)
					}
				}
				if strings.Count(p.Content, "<!-- wp:paragraph") < 3 {
					t.Errorf("post %s: fewer than 3 paragraphs", p.Slug)
				}
				comments += len(p.Comments)
				checkContent("post "+p.Slug, p.Content)
			}
			if comments == 0 {
				t.Error("no demo comments")
			}
			locations := map[string]bool{}
			for _, l := range m.MenuLocations {
				locations[l.ID] = true
			}
			var checkItems func(menu string, items []themeLibraryDemoItem, depth int) bool
			checkItems = func(menu string, items []themeLibraryDemoItem, depth int) bool {
				nested := false
				for _, it := range items {
					switch {
					case it.Page != "" && !pages[it.Page], it.Post != "" && !posts[it.Post], it.Category != "" && !cats[it.Category]:
						t.Errorf("menu %s: item %q does not resolve", menu, it.Title)
					case it.Page == "" && it.Post == "" && it.Category == "" && it.URL == "":
						t.Errorf("menu %s: item %q links nowhere", menu, it.Title)
					}
					if len(it.Children) > 0 {
						nested = true
						checkItems(menu, it.Children, depth+1)
					}
				}
				return nested
			}
			submenu := false
			for _, mn := range d.Menus {
				if !locations[mn.Location] {
					t.Errorf("menu %s: location %s is not declared", mn.Name, mn.Location)
				}
				if checkItems(mn.Name, mn.Items, 0) {
					submenu = true
				}
			}
			if !submenu {
				t.Error("no menu has a submenu")
			}
			areas := map[string]bool{}
			for _, a := range m.WidgetAreas {
				areas[a.ID] = true
			}
			for id := range d.Widgets {
				if !areas[id] {
					t.Errorf("widgets: area %s is not declared", id)
				}
			}
			for k, v := range d.Customizer {
				st, ok := settings[k]
				if !ok {
					t.Errorf("customizer: %s is not a setting", k)
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
					s, _ := v.(string)
					found := false
					for _, c := range st.Choices {
						found = found || c[0] == s
					}
					if !found {
						t.Errorf("customizer %s: %v is not a choice", k, v)
					}
				case "image":
					if s, _ := v.(string); !media[strings.TrimSuffix(strings.TrimPrefix(s, "{{media:"), "}}")] {
						t.Errorf("customizer %s: %v is not a demo media id", k, v)
					}
				}
			}
		})
	}
}

func TestThemeLibraryAInstall(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WPGO_THEMES_DIR", dir)
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Library A"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK {
		t.Fatalf("install: %d %s", code, body)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	_, restNonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	rest := func(t *testing.T, method, p string, payload any) (int, map[string]any) {
		t.Helper()
		var rd io.Reader
		if payload != nil {
			b, _ := json.Marshal(payload)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+"/wp-json"+p, rd)
		req.Header.Set("X-WP-Nonce", restNonce)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		if resp.StatusCode >= 300 {
			t.Fatalf("%s %s: %d %s", method, p, resp.StatusCode, raw)
		}
		return resp.StatusCode, out
	}
	upload := func(t *testing.T, file string, data []byte, alt string) (int64, string) {
		t.Helper()
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("file", path.Base(file))
		_, _ = fw.Write(data)
		_ = mw.WriteField("alt_text", alt)
		mw.Close()
		req, _ := http.NewRequest(http.MethodPost, base+"/wp-json/wp/v2/media", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("X-WP-Nonce", restNonce)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("upload %s: %d %s", file, resp.StatusCode, raw)
		}
		u, _ := out["source_url"].(string)
		return int64(out["id"].(float64)), u
	}
	activate := func(t *testing.T, slug string) {
		t.Helper()
		_, body, _ := get(t, c, base+"/wp-admin/themes")
		m := regexp.MustCompile(`href="(/wp-admin/themes\?action=activate&amp;stylesheet=` + slug + `&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no activate link for %s", slug)
		}
		if code, body, _ := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound {
			t.Fatalf("activate %s: %d %s", slug, code, body)
		}
	}
	_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
	nonce := formNonce(t, body)

	for i, slug := range themeLibraryASlugs {
		t.Run(slug, func(t *testing.T) {
			tdir := filepath.Join(themeLibraryDir, slug)
			code, body, loc := postZip(t, c, base+"/wp-admin/theme-install", nonce, "themezip", slug+".zip", zipDir(t, tdir))
			if code != http.StatusFound || loc != "/wp-admin/themes?installed="+slug {
				t.Fatalf("upload: %d %q %s", code, loc, body)
			}
			t.Cleanup(func() { unregisterTheme(slug) })
			activate(t, slug)
			if srv.activeTheme().Slug != slug {
				t.Fatalf("active theme %s", srv.activeTheme().Slug)
			}

			// A slice of the demo: its categories, tags, three posts with their images, the pages.
			d := readThemeLibraryDemo(t, tdir)
			repl := map[string]string{}
			mediaID := map[string]int64{}
			for _, md := range d.Media {
				data, err := os.ReadFile(filepath.Join(tdir, md.File))
				if err != nil {
					t.Fatal(err)
				}
				id, u := upload(t, md.File, data, md.Alt)
				mediaID[md.ID], repl["{{media:"+md.ID+"}}"] = id, u
			}
			catID, tagID := map[string]int64{}, map[string]int64{}
			for _, cat := range d.Categories {
				_, out := rest(t, "POST", "/wp/v2/categories", map[string]any{"name": fmt.Sprintf("%s %d", cat.Name, i), "slug": fmt.Sprintf("%s-%d", cat.Slug, i), "description": cat.Description})
				catID[cat.Slug] = int64(out["id"].(float64))
				repl["{{category:"+cat.Slug+"}}"], _ = out["link"].(string)
			}
			for _, tg := range d.Tags {
				_, out := rest(t, "POST", "/wp/v2/tags", map[string]any{"name": fmt.Sprintf("%s %d", tg.Name, i), "slug": fmt.Sprintf("%s-%d", tg.Slug, i)})
				tagID[tg.Slug] = int64(out["id"].(float64))
			}
			// Pages and posts first, their contents once every placeholder is known.
			type created struct {
				route   string
				id      int64
				content string
			}
			var items []created
			var pageLinks, postLinks []string
			for _, p := range d.Pages {
				_, out := rest(t, "POST", "/wp/v2/pages", map[string]any{"title": p.Title, "slug": fmt.Sprintf("%s-%d", p.Slug, i), "status": "publish", "featured_media": mediaID[p.FeaturedMedia]})
				link, _ := out["link"].(string)
				repl["{{page:"+p.Slug+"}}"] = link
				pageLinks = append(pageLinks, link)
				items = append(items, created{"pages", int64(out["id"].(float64)), p.Content})
			}
			for j, p := range d.Posts {
				if j >= 3 {
					break
				}
				var cs, ts []int64
				for _, s := range p.Categories {
					cs = append(cs, catID[s])
				}
				for _, s := range p.Tags {
					ts = append(ts, tagID[s])
				}
				_, out := rest(t, "POST", "/wp/v2/posts", map[string]any{"title": p.Title, "slug": fmt.Sprintf("%s-%d", p.Slug, i), "status": "publish",
					"excerpt": p.Excerpt, "categories": cs, "tags": ts, "featured_media": mediaID[p.FeaturedMedia]})
				link, _ := out["link"].(string)
				repl["{{post:"+p.Slug+"}}"] = link
				postLinks = append(postLinks, link)
				postID := int64(out["id"].(float64))
				items = append(items, created{"posts", postID, p.Content})
				for _, cm := range p.Comments {
					rest(t, "POST", "/wp/v2/comments", map[string]any{"post": postID, "author_name": cm.Author, "author_email": cm.Email, "content": cm.Content})
				}
			}
			for _, it := range items {
				content := themeLibraryPlaceholderRe.ReplaceAllStringFunc(it.content, func(m string) string {
					if v, ok := repl[m]; ok {
						return v
					}
					return "#"
				})
				rest(t, "POST", fmt.Sprintf("/wp/v2/%s/%d", it.route, it.id), map[string]any{"content": content})
			}

			stylesheet := fmt.Sprintf(`href='/wp-content/themes/%s/`, slug)
			check := func(what, u string, want int) string {
				t.Helper()
				code, body, _ := get(t, c, u)
				if code != want {
					t.Fatalf("%s %s: %d (want %d) %.400s", what, u, code, want, body)
				}
				if strings.Contains(body, "Template error") || strings.Contains(body, "<no value>") || strings.Contains(body, "%!") {
					t.Fatalf("%s: template problem in output", what)
				}
				if !strings.Contains(body, stylesheet) {
					t.Fatalf("%s: theme stylesheet not linked", what)
				}
				return body
			}
			home := check("home", base+"/", http.StatusOK)
			if !strings.Contains(home, d.Posts[0].Title) && !strings.Contains(home, html.EscapeString(d.Posts[0].Title)) {
				t.Logf("home page does not show %q", d.Posts[0].Title)
			}
			check("single", postLinks[0], http.StatusOK)
			check("page", pageLinks[0], http.StatusOK)
			check("category", repl["{{category:"+d.Categories[0].Slug+"}}"], http.StatusOK)
			check("search", base+"/?s=the", http.StatusOK)
			check("empty search", base+"/?s=zzzqqq", http.StatusOK)
			check("404", base+"/wpgo-no-such-page-"+slug+"/", http.StatusNotFound)
			if code, _, _ := get(t, c, base+fmt.Sprintf("/wp-content/themes/%s/style.css", slug)); code != http.StatusOK {
				t.Fatalf("style.css: %d", code)
			}
		})
	}
}
