package server

import (
	"bytes"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Theme library group c: Ledger & Co., Trattoria, Crumb & Crust, Keystone Estates and Pulse Fit.
var themeLibraryC = []string{"ledger-co", "trattoria", "crumb-and-crust", "keystone-estates", "pulse-fit"}

// themeLibDemo is the part of demo/demo.json the checks below read (docs/theme-package.md, "Demo content").
type themeLibDemo struct {
	Version    int                                        `json:"version"`
	Options    map[string]any                             `json:"options"`
	Categories []struct{ Slug, Name, Description string } `json:"categories"`
	Tags       []struct{ Slug, Name string }              `json:"tags"`
	Media      []struct{ ID, File, Alt, Caption string }  `json:"media"`
	Pages      []themeLibDemoItem                         `json:"pages"`
	Posts      []themeLibDemoItem                         `json:"posts"`
	Menus      []struct {
		Name, Location string
		Items          []themeLibMenuItem `json:"items"`
	} `json:"menus"`
	Widgets    map[string][]string `json:"widgets"`
	Customizer map[string]any      `json:"customizer"`
}

type themeLibDemoItem struct {
	Slug          string   `json:"slug"`
	Title         string   `json:"title"`
	Content       string   `json:"content"`
	Excerpt       string   `json:"excerpt"`
	Date          string   `json:"date"`
	Parent        string   `json:"parent"`
	FeaturedMedia string   `json:"featured_media"`
	FrontPage     bool     `json:"front_page"`
	PostsPage     bool     `json:"posts_page"`
	Categories    []string `json:"categories"`
	Tags          []string `json:"tags"`
	Comments      []struct {
		Author, Email, Content, Date string
	} `json:"comments"`
}

type themeLibMenuItem struct {
	Title    string             `json:"title"`
	Page     string             `json:"page"`
	Post     string             `json:"post"`
	Category string             `json:"category"`
	URL      string             `json:"url"`
	Children []themeLibMenuItem `json:"children"`
}

var themeLibPlaceholder = regexp.MustCompile(`\{\{(media|media_id|page|post|category|tag):([a-z0-9-]+)\}\}`)

// checkThemeLibDemo validates demo/demo.json: every image, page, post, category, tag and media id it
// references exists, and the Customizer values name the theme's own settings.
func checkThemeLibDemo(t *testing.T, dir string, settings map[string]themeCzSettings) *themeLibDemo {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "demo", "demo.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d themeLibDemo
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("demo.json: %v", err)
	}
	if d.Version != 1 || d.Options["blogname"] == nil || len(d.Posts) < 6 || len(d.Posts) > 10 || len(d.Pages) < 3 {
		t.Fatalf("demo.json: version %d, %d posts, %d pages", d.Version, len(d.Posts), len(d.Pages))
	}
	for k := range d.Options {
		if k != "blogname" && k != "blogdescription" && k != "posts_per_page" && k != "date_format" {
			t.Errorf("demo option %q is not allowed", k)
		}
	}
	ids := map[string]map[string]bool{"media": {}, "page": {}, "post": {}, "category": {}, "tag": {}}
	if len(d.Media) == 0 || len(d.Media) > 24 {
		t.Errorf("%d demo images", len(d.Media))
	}
	for _, m := range d.Media {
		ids["media"][m.ID] = true
		if !strings.HasPrefix(m.File, "demo/images/") || m.Alt == "" {
			t.Errorf("media %s: file %q, alt %q", m.ID, m.File, m.Alt)
		}
		st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(m.File)))
		if err != nil {
			t.Errorf("media %s: %v", m.ID, err)
		} else if st.Size() > 1<<20 {
			t.Errorf("media %s is larger than 1 MiB", m.ID)
		}
		if ext := path.Ext(m.File); ext != ".jpg" && ext != ".png" && ext != ".webp" {
			t.Errorf("media %s: type %s", m.ID, ext)
		}
	}
	for _, c := range d.Categories {
		ids["category"][c.Slug] = true
	}
	for _, tg := range d.Tags {
		ids["tag"][tg.Slug] = true
	}
	front := 0
	for _, p := range d.Pages {
		ids["page"][p.Slug] = true
		if p.FrontPage {
			front++
		}
	}
	for _, p := range d.Posts {
		ids["post"][p.Slug] = true
	}
	has := func(kind, id, where string) {
		if !ids[kind][id] {
			t.Errorf("%s references the unknown %s %q", where, kind, id)
		}
	}
	for _, p := range append(append([]themeLibDemoItem{}, d.Pages...), d.Posts...) {
		if p.FeaturedMedia != "" {
			has("media", p.FeaturedMedia, p.Slug)
		}
		if p.Parent != "" {
			has("page", p.Parent, p.Slug)
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
		if strings.Count(p.Content, "<!-- wp:") < 4 || p.Date == "" || len(p.Categories) == 0 {
			t.Errorf("post %s is too thin (blocks, date or categories missing)", p.Slug)
		}
		if d, err := time.Parse("2006-01-02T15:04:05", p.Date); err != nil || d.After(time.Now()) {
			t.Errorf("post %s: date %q is invalid or in the future", p.Slug, p.Date)
		}
	}
	for _, m := range themeLibPlaceholder.FindAllStringSubmatch(string(raw), -1) {
		has(strings.TrimSuffix(m[1], "_id"), m[2], "a placeholder")
	}
	var walk func(items []themeLibMenuItem)
	walk = func(items []themeLibMenuItem) {
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
	for _, m := range d.Menus {
		walk(m.Items)
		for _, it := range m.Items {
			submenu = submenu || len(it.Children) > 0
		}
	}
	if !submenu {
		t.Error("no menu has a submenu")
	}
	for k := range d.Customizer {
		if _, ok := settings[k]; !ok {
			t.Errorf("demo customizer value %q is not a setting of the theme", k)
		}
	}
	if front > 1 {
		t.Error("more than one front page")
	}
	return &d
}

// themeLibREST sends a cookie-authenticated REST request.
func themeLibREST(t *testing.T, c *http.Client, base, method, p string, body any) (int, map[string]any) {
	t.Helper()
	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, base+p, bytes.NewReader(b))
	req.Header.Set("X-WP-Nonce", strings.TrimSpace(nonce))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var obj map[string]any
	_ = json.Unmarshal(raw, &obj)
	if resp.StatusCode >= 300 {
		t.Fatalf("%s %s: %d %s", method, p, resp.StatusCode, raw)
	}
	return resp.StatusCode, obj
}

func TestThemeLibraryC(t *testing.T) {
	t.Setenv("WPGO_THEMES_DIR", t.TempDir())
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	t.Cleanup(func() {
		for _, slug := range themeLibraryC {
			unregisterTheme(slug)
		}
	})

	for _, slug := range themeLibraryC {
		t.Run(slug, func(t *testing.T) {
			dir := filepath.Join("..", "..", "web", "theme-library", slug)
			if _, err := os.Stat(filepath.Join(dir, "theme.json")); err != nil {
				t.Fatal(err)
			}
			mb, _ := os.ReadFile(filepath.Join(dir, "theme.json"))
			m, err := parseThemeManifest(mb)
			if err != nil {
				t.Fatalf("theme.json: %v", err)
			}
			var lib struct{ Category, Style, Demo string }
			_ = json.Unmarshal(mb, &lib)
			if lib.Category == "" || lib.Style == "" || lib.Demo != "demo/demo.json" || m.Version != "1.0.0" || m.Author != "Pursae" {
				t.Fatalf("library metadata: %+v %s %s", lib, m.Version, m.Author)
			}
			settings := map[string]themeCzSettings{}
			for _, sec := range m.Customizer.Sections {
				for _, st := range sec.Settings {
					settings[st.ID] = st
				}
			}
			if len(settings) < 8 || len(m.Customizer.Sections) < 2 || len(m.Customizer.Sections) > 4 {
				t.Fatalf("%d settings in %d sections", len(settings), len(m.Customizer.Sections))
			}
			demo := checkThemeLibDemo(t, dir, settings)

			// Install through Appearance › Add Themes › Upload Theme, then activate.
			_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
			code, body, loc := postZip(t, c, base+"/wp-admin/theme-install", formNonce(t, body), "themezip", slug+".zip", zipDir(t, dir))
			if code != http.StatusFound || loc != "/wp-admin/themes?installed="+slug {
				t.Fatalf("upload: %d %q %s", code, loc, body)
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
			page := func(what, u string, want int) string {
				t.Helper()
				code, body, _ := get(t, c, base+u)
				if code != want {
					t.Fatalf("%s %s: %d (want %d)", what, u, code, want)
				}
				if !strings.Contains(body, styleLink) {
					t.Fatalf("%s: the theme stylesheet is not linked", what)
				}
				if strings.Contains(body, "Template error") || strings.Contains(body, "<no value>") {
					t.Fatalf("%s: template error", what)
				}
				return body
			}
			page("blog home", "/", http.StatusOK)
			for _, f := range []string{"style.css", "editor-style.css", "screenshot.png"} {
				if code, _, _ := get(t, c, base+"/wp-content/themes/"+slug+"/"+f); code != http.StatusOK {
					t.Fatalf("%s: %d", f, code)
				}
			}

			// Content from the demo: a category, a post and the designed pages (images point at the screenshot).
			fill := func(s string) string {
				return themeLibPlaceholder.ReplaceAllStringFunc(s, func(p string) string {
					if strings.HasPrefix(p, "{{media_id:") {
						return "0"
					}
					if strings.HasPrefix(p, "{{media:") {
						return "/wp-content/themes/" + slug + "/screenshot.png"
					}
					return "/"
				})
			}
			cat := demo.Categories[0]
			_, term := themeLibREST(t, c, base, "POST", "/wp-json/wp/v2/categories", map[string]any{"name": cat.Name, "slug": slug + "-" + cat.Slug, "description": cat.Description})
			catID := term["id"]
			dp := demo.Posts[0]
			_, pj := themeLibREST(t, c, base, "POST", "/wp-json/wp/v2/posts", map[string]any{"title": dp.Title, "slug": slug + "-" + dp.Slug, "status": "publish",
				"content": fill(dp.Content), "excerpt": dp.Excerpt, "categories": []any{catID}})
			_, cm := themeLibREST(t, c, base, "POST", "/wp-json/wp/v2/comments", map[string]any{"post": pj["id"], "content": "A first comment on the demo post."})
			_ = cm
			var frontID any
			pageLinks := []string{}
			for _, pg := range demo.Pages {
				if pg.PostsPage {
					continue
				}
				_, pgj := themeLibREST(t, c, base, "POST", "/wp-json/wp/v2/pages", map[string]any{"title": pg.Title, "slug": slug + "-" + pg.Slug, "status": "publish", "content": fill(pg.Content)})
				if pg.FrontPage {
					frontID = pgj["id"]
				} else {
					pageLinks = append(pageLinks, pgj["link"].(string))
				}
			}
			rel := func(link string) string {
				u, _ := url.Parse(link)
				return u.RequestURI()
			}
			body = page("single post", rel(pj["link"].(string)), http.StatusOK)
			if !strings.Contains(body, "A first comment on the demo post.") {
				t.Fatal("single post: comment missing")
			}
			for _, l := range pageLinks {
				page("page", rel(l), http.StatusOK)
			}
			page("category archive", "/category/"+slug+"-"+cat.Slug+"/", http.StatusOK)
			body = page("search", "/?s="+url.QueryEscape(strings.Fields(dp.Title)[1]), http.StatusOK)
			if !strings.Contains(body, dp.Title[:12]) {
				t.Fatal("search: the post is not found")
			}
			page("404", "/no-such-page-"+slug+"/", http.StatusNotFound)

			// The demo's Customizer values publish, and a static front page renders the theme's front page.
			if frontID != nil {
				themeLibREST(t, c, base, "POST", "/wp-json/wp/v2/settings", map[string]any{"show_on_front": "page", "page_on_front": frontID})
			}
			_, body, _ = get(t, c, base+"/wp-admin/customize")
			czn := regexp.MustCompile(`nonce = "([^"]+)"`).FindStringSubmatch(body)
			if czn == nil {
				t.Fatal("no customize nonce")
			}
			vals := map[string]string{}
			for k, v := range demo.Customizer {
				if settings[k].Type == "image" {
					continue // a media id of the demo, resolved by the importer
				}
				switch x := v.(type) {
				case bool:
					vals["mod:"+k] = map[bool]string{true: "1", false: "0"}[x]
				case float64:
					vals["mod:"+k] = strconv.Itoa(int(x))
				default:
					vals["mod:"+k] = x.(string)
				}
			}
			cz, _ := json.Marshal(vals)
			if code, body, _ := post(t, c, base+"/wp-admin/customize", url.Values{"_wpnonce": {czn[1]}, "action": {"publish"}, "customized": {string(cz)}}); code != http.StatusOK || !strings.Contains(body, "Published") {
				t.Fatalf("customizer publish: %d %s", code, body)
			}
			body = page("front page", "/", http.StatusOK)
			for k, st := range settings {
				if st.CSSVar != "" && !strings.Contains(body, st.CSSVar+":") {
					t.Errorf("css var of %s missing", k)
				}
			}
			if frontID != nil {
				themeLibREST(t, c, base, "POST", "/wp-json/wp/v2/settings", map[string]any{"show_on_front": "posts"})
			}
		})
	}
}
