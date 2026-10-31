package server

// Golementor Studio site kits, group KA: northwind (business), osteria (restaurant) and
// studio-mono (portfolio) under web/gowebeditor/library/kits/<slug>/kit.json. The first test checks
// every kit against the format in docs/golementor-studio.md section 3 and against the builder's
// control schema; the second installs each kit on a fresh site the way the setup wizard does
// (kit globals, theme parts with display conditions through the Theme Builder, pages and parts saved
// with save_builder, sample posts with featured images, menus, reading settings) and renders the
// home page, blog, a post, a page, contact, search results and the 404 page.

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"wpgo/internal/builder"
	"wpgo/internal/store"
)

var glibKASlugs = []string{"northwind", "osteria", "studio-mono"}

const glibKALibrary = "../../web/gowebeditor/library"

type glibKAPart struct {
	Type       string          `json:"type"`
	Title      string          `json:"title"`
	Conditions []string        `json:"conditions"`
	Settings   map[string]any  `json:"settings"`
	Content    json.RawMessage `json:"content"`
}

type glibKAPage struct {
	Slug      string          `json:"slug"`
	Title     string          `json:"title"`
	FrontPage bool            `json:"front_page"`
	PostsPage bool            `json:"posts_page"`
	Settings  map[string]any  `json:"settings"`
	Content   json.RawMessage `json:"content"`
}

type glibKAMenu struct {
	Location string `json:"location"`
	Name     string `json:"name"`
	Items    []struct {
		Title string `json:"title"`
		Page  string `json:"page"`
		URL   string `json:"url"`
	} `json:"items"`
}

type glibKAPost struct {
	Title    string `json:"title"`
	Slug     string `json:"slug"`
	Category string `json:"category"`
	Excerpt  string `json:"excerpt"`
	Content  string `json:"content"`
	Image    string `json:"image"`
}

type glibKAKit struct {
	Slug        string   `json:"-"`
	Title       string   `json:"title"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Screenshot  string   `json:"screenshot"`
	Tags        []string `json:"tags"`
	Site        struct {
		Name    string `json:"name"`
		Tagline string `json:"tagline"`
	} `json:"site"`
	Globals struct {
		Colors     []map[string]any `json:"colors"`
		Typography []map[string]any `json:"typography"`
		Settings   map[string]any   `json:"settings"`
	} `json:"globals"`
	Parts []glibKAPart `json:"parts"`
	Pages []glibKAPage `json:"pages"`
	Menus []glibKAMenu `json:"menus"`
	Posts []glibKAPost `json:"posts"`
	raw   []byte
}

func glibKALoad(t *testing.T, slug string) *glibKAKit {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(glibKALibrary, "kits", slug, "kit.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	k := &glibKAKit{}
	if err := dec.Decode(k); err != nil {
		t.Fatalf("%s: kit.json: %v", slug, err)
	}
	k.Slug, k.raw = slug, raw
	return k
}

// --- the control schema ------------------------------------------------------------------------

type glibKASchema struct {
	widgets map[string]map[string]bool // widget -> control names (with the common controls)
	stacks  map[string]map[string]bool // container, page, kit
	icons   map[string]map[string]bool // library (fa-solid, ...) -> icon names
}

func glibKALoadSchema(t *testing.T) *glibKASchema {
	t.Helper()
	read := func(p string) map[string]any {
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		zr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.NewDecoder(zr).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	names := func(list any) map[string]bool {
		m := map[string]bool{}
		switch v := list.(type) {
		case []any:
			for _, c := range v {
				if cm, ok := c.(map[string]any); ok {
					m[fmt.Sprint(cm["name"])] = true
				}
			}
		case map[string]any:
			for k := range v {
				m[k] = true
			}
		}
		return m
	}
	data := read("../builder/data/pro-controls.json.gz")
	s := &glibKASchema{widgets: map[string]map[string]bool{}, stacks: map[string]map[string]bool{}}
	for name, st := range data["stacks"].(map[string]any) {
		s.stacks[name] = names(st)
	}
	ws := data["widgets"].(map[string]any)
	common := names(ws["common"].(map[string]any)["controls"])
	add := func(name string, ctrls map[string]bool) {
		m := map[string]bool{}
		for k := range common {
			m[k] = true
		}
		for k := range ctrls {
			m[k] = true
		}
		s.widgets[name] = m
	}
	for name, w := range ws {
		add(name, names(w.(map[string]any)["controls"]))
	}
	// The editor's widget configs list every widget the panel offers.
	for _, cfg := range []string{"gowebeditor/widgets-config.json.gz", "gowebeditor/pro-widgets-config.json.gz"} {
		for name, w := range read(cfg) {
			if _, ok := s.widgets[name]; !ok {
				add(name, names(w.(map[string]any)["controls"]))
			}
		}
	}
	// Site widgets ported in Go replace the upstream controls (widgets_theme.go).
	for _, w := range builder.ThemeWidgets() {
		m := map[string]bool{}
		for _, c := range w.Controls {
			m[fmt.Sprint(c["name"])] = true
		}
		add(w.Name, m)
	}
	s.icons = map[string]map[string]bool{}
	for lib, list := range read("../builder/data/icons.json.gz") {
		s.icons[lib] = names(list)
	}
	s.widgets["nav-menu"]["menu_location"] = true // gowebeditor_pro_config.go adds the location select
	return s
}

var glibKASuffixes = []string{"_tablet_extra", "_mobile_extra", "_widescreen", "_laptop", "_tablet", "_mobile"}

func (s *glibKASchema) known(ctrls map[string]bool, key string) bool {
	if ctrls[key] || key == "__globals__" || key == "__dynamic__" || key == "_skin" {
		return true
	}
	for _, suf := range glibKASuffixes {
		if strings.HasSuffix(key, suf) && ctrls[strings.TrimSuffix(key, suf)] {
			return true
		}
	}
	return false
}

// --- validation ----------------------------------------------------------------------------------

var (
	glibKAToken      = regexp.MustCompile(`\{\{([a-z]+):([^{}]*)\}\}`)
	glibKAElementID  = regexp.MustCompile(`^[0-9a-f]{7}$`)
	glibKAHex        = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	glibKAGlobalRef  = regexp.MustCompile(`^globals/(colors|typography)\?id=([A-Za-z0-9_-]+)$`)
	glibKACategories = map[string]bool{"business": true, "restaurant": true, "portfolio": true, "blog": true, "shop": true, "personal": true}
	glibKAPartTypes  = []string{"header", "footer", "single-post", "single-page", "archive", "search-results", "error-404"}
)

type glibKAChecker struct {
	t       *testing.T
	kit     *glibKAKit
	schema  *glibKASchema
	colors  map[string]bool
	typos   map[string]bool
	ids     map[string]string
	widgets map[string]int
}

func (ck *glibKAChecker) errorf(where, format string, args ...any) {
	ck.t.Helper()
	ck.t.Errorf("%s: %s: %s", ck.kit.Slug, where, fmt.Sprintf(format, args...))
}

func (ck *glibKAChecker) globals(where string, ctrls map[string]bool, settings map[string]any) {
	g, ok := settings["__globals__"]
	if !ok {
		return
	}
	gm, ok := g.(map[string]any)
	if !ok {
		ck.errorf(where, "__globals__ is not an object")
		return
	}
	for k, v := range gm {
		if !ck.schema.known(ctrls, k) {
			ck.errorf(where, "__globals__ names unknown control %q", k)
		}
		m := glibKAGlobalRef.FindStringSubmatch(fmt.Sprint(v))
		if m == nil {
			ck.errorf(where, "bad global reference %s=%v", k, v)
			continue
		}
		isTypo := strings.HasSuffix(k, "typography")
		switch {
		case m[1] == "colors" && (isTypo || !ck.colors[m[2]]):
			ck.errorf(where, "%s references unknown color %q", k, m[2])
		case m[1] == "typography" && (!isTypo || !ck.typos[m[2]]):
			ck.errorf(where, "%s references unknown typography %q", k, m[2])
		}
	}
}

// icons checks every icon value ({"value":"fas fa-x","library":"fa-solid"}) against the icon set.
func (ck *glibKAChecker) icons(where string, v any) {
	switch x := v.(type) {
	case map[string]any:
		val, ok1 := x["value"].(string)
		lib, ok2 := x["library"].(string)
		if ok1 && ok2 && len(x) <= 3 {
			if val == "" && lib == "" {
				return
			}
			_, name, _ := strings.Cut(val, " fa-")
			if ck.schema.icons[lib] == nil || !ck.schema.icons[lib][name] {
				ck.errorf(where, "unknown icon %q in %q", val, lib)
			}
			return
		}
		for _, y := range x {
			ck.icons(where, y)
		}
	case []any:
		for _, y := range x {
			ck.icons(where, y)
		}
	}
}

func (ck *glibKAChecker) elements(where string, raw json.RawMessage) {
	var els []map[string]any
	if err := json.Unmarshal(raw, &els); err != nil || len(els) == 0 {
		ck.errorf(where, "content must be a non-empty element list: %v", err)
		return
	}
	if _, err := builder.ParseElements(raw); err != nil {
		ck.errorf(where, "element tree: %v", err)
	}
	var walk func(path string, list []any, depth int)
	walk = func(path string, list []any, depth int) {
		for i, x := range list {
			el, _ := x.(map[string]any)
			p := fmt.Sprintf("%s[%d]", path, i)
			if el == nil {
				ck.errorf(where, "%s is not an object", p)
				continue
			}
			id, _ := el["id"].(string)
			if !glibKAElementID.MatchString(id) {
				ck.errorf(where, "%s: bad id %q", p, id)
			} else if prev, dup := ck.ids[id]; dup {
				ck.errorf(where, "%s: id %s already used at %s", p, id, prev)
			} else {
				ck.ids[id] = where + " " + p
			}
			settings, _ := el["settings"].(map[string]any)
			kids, _ := el["elements"].([]any)
			var ctrls map[string]bool
			switch el["elType"] {
			case "container":
				ctrls = ck.schema.stacks["container"]
				if depth == 0 && el["isInner"] != false {
					ck.errorf(where, "%s: top-level container must have isInner false", p)
				}
			case "widget":
				wt, _ := el["widgetType"].(string)
				ctrls = ck.schema.widgets[wt]
				if ctrls == nil {
					ck.errorf(where, "%s: unknown widgetType %q", p, wt)
					continue
				}
				ck.widgets[wt]++
				if len(kids) > 0 {
					ck.errorf(where, "%s: a %s widget has children", p, wt)
				}
				p += "<" + wt + ">"
			default:
				ck.errorf(where, "%s: elType %v (only containers and widgets)", p, el["elType"])
				continue
			}
			for k := range settings {
				if !ck.schema.known(ctrls, k) {
					ck.errorf(where, "%s: unknown control %q", p, k)
				}
			}
			ck.globals(where+" "+p, ctrls, settings)
			ck.icons(where+" "+p, settings)
			walk(p, kids, depth+1)
		}
	}
	var list []any
	_ = json.Unmarshal(raw, &list)
	walk("content", list, 0)
}

func glibKAValidate(t *testing.T, k *glibKAKit, schema *glibKASchema) {
	t.Helper()
	ck := &glibKAChecker{t: t, kit: k, schema: schema, colors: map[string]bool{}, typos: map[string]bool{}, ids: map[string]string{}, widgets: map[string]int{}}
	dir := filepath.Join(glibKALibrary, "kits", k.Slug)
	if k.Title == "" || k.Description == "" || !glibKACategories[k.Category] {
		ck.errorf("kit", "title %q, description and a known category (got %q) are required", k.Title, k.Category)
	}
	if k.Site.Name == "" || k.Site.Tagline == "" {
		ck.errorf("kit", "site name and tagline are required")
	}
	if shot, err := os.ReadFile(filepath.Join(dir, k.Screenshot)); err != nil || k.Screenshot != "screenshot.webp" || len(shot) < 12 ||
		string(shot[:4]) != "RIFF" || string(shot[8:12]) != "WEBP" {
		ck.errorf("kit", "screenshot %q must be a WebP file next to kit.json (%v)", k.Screenshot, err)
	} else if len(shot) > 300<<10 {
		ck.errorf("kit", "screenshot is %d KB", len(shot)>>10)
	}

	// Globals: the four system colors and fonts plus the kit's own, all Google fonts.
	for _, col := range k.Globals.Colors {
		id, _ := col["_id"].(string)
		if id == "" || ck.colors[id] || !glibKAHex.MatchString(fmt.Sprint(col["color"])) || col["title"] == "" {
			ck.errorf("globals", "bad color %v", col)
		}
		ck.colors[id] = true
	}
	for _, ty := range k.Globals.Typography {
		id, _ := ty["_id"].(string)
		fam, _ := ty["typography_font_family"].(string)
		if id == "" || ck.typos[id] || ty["typography_typography"] != "custom" || gweGoogleFontType(fam) == "" {
			ck.errorf("globals", "bad typography %v (font family must be a Google font)", ty)
		}
		ck.typos[id] = true
	}
	for _, id := range []string{"primary", "secondary", "text", "accent"} {
		if !ck.colors[id] || !ck.typos[id] {
			ck.errorf("globals", "system color/typography %q missing", id)
		}
	}
	for key := range k.Globals.Settings {
		if !schema.known(schema.stacks["kit"], key) {
			ck.errorf("globals.settings", "unknown kit control %q", key)
		}
	}
	ck.globals("globals.settings", schema.stacks["kit"], k.Globals.Settings)

	// Parts: one of each type, with valid display conditions.
	seen := map[string]bool{}
	for _, p := range k.Parts {
		if seen[p.Type] || gwePartLabel(p.Type) == "" || p.Title == "" {
			ck.errorf("parts", "bad or repeated part %q (%q)", p.Type, p.Title)
		}
		seen[p.Type] = true
		if len(p.Conditions) == 0 {
			ck.errorf("parts", "%s has no conditions", p.Type)
		}
		for _, cd := range p.Conditions {
			mode, rule, _ := strings.Cut(cd, "/")
			if (mode != "include" && mode != "exclude") || !gweValidRule(rule) {
				ck.errorf("parts", "%s: bad condition %q", p.Type, cd)
			}
		}
		for key := range p.Settings {
			if !schema.known(schema.stacks["page"], key) {
				ck.errorf("parts", "%s: unknown document setting %q", p.Type, key)
			}
		}
		ck.elements("part "+p.Type, p.Content)
	}
	for _, typ := range glibKAPartTypes {
		if !seen[typ] {
			ck.errorf("parts", "missing %s", typ)
		}
	}

	// Pages: home (front page), the posts page, about, contact and the kit's services equivalent.
	pages := map[string]*glibKAPage{}
	front, posts := 0, 0
	for i := range k.Pages {
		p := &k.Pages[i]
		if p.Slug == "" || p.Title == "" || pages[p.Slug] != nil {
			ck.errorf("pages", "bad or repeated page %q", p.Slug)
		}
		pages[p.Slug] = p
		if p.FrontPage {
			front++
		}
		if p.PostsPage {
			posts++
			if len(p.Content) > 0 {
				ck.errorf("pages", "the posts page %q must not have content", p.Slug)
			}
			continue
		}
		for key := range p.Settings {
			if !schema.known(schema.stacks["page"], key) {
				ck.errorf("pages", "%s: unknown page setting %q", p.Slug, key)
			}
		}
		ck.elements("page "+p.Slug, p.Content)
	}
	if front != 1 || posts != 1 || pages["home"] == nil || !pages["home"].FrontPage {
		ck.errorf("pages", "need exactly one front page (home) and one posts page")
	}
	for _, slug := range []string{"about", "contact"} {
		if pages[slug] == nil {
			ck.errorf("pages", "missing %s", slug)
		}
	}
	if len(k.Pages) < 5 {
		ck.errorf("pages", "only %d pages", len(k.Pages))
	}

	// Menus: primary and footer, linking the kit's pages.
	locs := map[string]bool{}
	for _, m := range k.Menus {
		if (m.Location != "primary" && m.Location != "footer") || locs[m.Location] || m.Name == "" || len(m.Items) == 0 {
			ck.errorf("menus", "bad menu %q at %q", m.Name, m.Location)
		}
		locs[m.Location] = true
		for _, it := range m.Items {
			if it.Title == "" || (it.Page == "") == (it.URL == "") || (it.Page != "" && pages[it.Page] == nil) {
				ck.errorf("menus", "%s: bad item %+v", m.Location, it)
			}
		}
	}
	if !locs["primary"] || !locs["footer"] {
		ck.errorf("menus", "primary and footer menus are required")
	}

	// Posts.
	if len(k.Posts) < 4 || len(k.Posts) > 6 {
		ck.errorf("posts", "%d sample posts (want 4-6)", len(k.Posts))
	}
	for _, p := range k.Posts {
		if p.Title == "" || p.Slug == "" || p.Category == "" || p.Excerpt == "" || !strings.Contains(p.Content, "<!-- wp:paragraph -->") || p.Image == "" {
			ck.errorf("posts", "incomplete post %q", p.Title)
		}
	}

	// Tokens: {{page:slug}} names a kit page, {{library:path}} a file in the library.
	for _, m := range glibKAToken.FindAllStringSubmatch(string(k.raw), -1) {
		switch m[1] {
		case "page":
			if pages[m[2]] == nil {
				ck.errorf("tokens", "%s names no page", m[0])
			}
		case "library":
			clean := path.Clean(m[2])
			if clean != m[2] || strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") {
				ck.errorf("tokens", "bad library path %s", m[0])
			} else if st, err := os.Stat(filepath.Join(glibKALibrary, filepath.FromSlash(clean))); err != nil || st.IsDir() {
				ck.errorf("tokens", "%s: %v", m[0], err)
			} else if strings.HasSuffix(clean, ".webp") && st.Size() > 150<<10 {
				ck.errorf("tokens", "%s is %d KB (max 150)", m[0], st.Size()>>10)
			}
		default:
			ck.errorf("tokens", "unknown token %s", m[0])
		}
	}
	if n := strings.Count(string(k.raw), "{{") - len(glibKAToken.FindAllString(string(k.raw), -1)); n != 0 {
		ck.errorf("tokens", "%d malformed tokens", n)
	}

	// The dynamic pieces a working site needs.
	for _, w := range []string{"theme-site-title", "nav-menu", "theme-post-title", "theme-post-content", "theme-post-featured-image",
		"post-info", "archive-posts", "theme-archive-title", "search-form", "theme-site-copyright", "form"} {
		if ck.widgets[w] == 0 {
			ck.errorf("widgets", "the kit uses no %s widget", w)
		}
	}
}

// TestGolementorLibraryKAKits checks the kits' JSON against the format and the control schema.
func TestGolementorLibraryKAKits(t *testing.T) {
	schema := glibKALoadSchema(t)
	titles := map[string]bool{}
	for _, slug := range glibKASlugs {
		k := glibKALoad(t, slug)
		if titles[k.Title] {
			t.Errorf("%s: title %q repeats another kit's", slug, k.Title)
		}
		titles[k.Title] = true
		glibKAValidate(t, k, schema)
	}
}

// --- install ---------------------------------------------------------------------------------------

// glibKASite is a site with a kit installed.
type glibKASite struct {
	base  string
	c     *http.Client
	srv   *Server
	kit   *glibKAKit
	pages map[string]int64
	posts []*store.Post
	parts map[string]int64
}

func (st *glibKASite) pageURL(slug string) string { return st.base + "/" + slug + "/" }

// glibKAReplace resolves the kit tokens the way the wizard's insert does.
func (st *glibKASite) replace(s string) string {
	return glibKAToken.ReplaceAllStringFunc(s, func(tok string) string {
		m := glibKAToken.FindStringSubmatch(tok)
		switch m[1] {
		case "page":
			if m[2] == "home" {
				return st.base + "/"
			}
			return st.pageURL(m[2])
		case "library":
			return st.base + gweAssetsPath + "library/" + m[2]
		}
		return tok
	})
}

// glibKAInstall installs a fresh site and the kit on it.
func glibKAInstall(t *testing.T, base string, c *http.Client, srv *Server, k *glibKAKit) *glibKASite {
	t.Helper()
	ctx := context.Background()
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {k.Site.Name}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK || !strings.Contains(body, "Success!") {
		t.Fatalf("install: %d", code)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	if err := srv.settings.Set(ctx, map[string]any{"blogdescription": k.Site.Tagline, "permalink_structure": "/%postname%/", "posts_per_page": 3}); err != nil {
		t.Fatal(err)
	}
	st := &glibKASite{base: base, c: c, srv: srv, kit: k, pages: map[string]int64{}, parts: map[string]int64{}}
	var adminID int64
	if err := srv.store.Pool.QueryRow(ctx, `SELECT id FROM users WHERE login = 'admin'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}

	// Hello Golementor, the theme the parts are bound to.
	_, body, _ := get(t, c, base+"/wp-admin/themes")
	act := regexp.MustCompile(`href="(/wp-admin/themes\?action=activate&amp;stylesheet=hello-web-builder&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
	if act == nil {
		t.Fatal("no activate link for hello-web-builder")
	}
	if code, _, loc := get(t, c, base+html.UnescapeString(act[1])); code != http.StatusFound || !strings.Contains(loc, "activated=true") {
		t.Fatalf("activate: %d %s", code, loc)
	}
	// The sample content of a fresh install goes; the kit brings its own posts.
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET status = 'trash' WHERE type = 'post' AND slug = 'hello-world'`); err != nil {
		t.Fatal(err)
	}

	// Pages first, so {{page:…}} tokens resolve everywhere.
	for i, p := range k.Pages {
		pg := &store.Post{Type: "page", Title: p.Title, Slug: p.Slug, Status: "publish", AuthorID: adminID, MenuOrder: i,
			CommentStatus: "closed", PingStatus: "closed", Meta: map[string]any{}}
		if err := srv.store.InsertPost(ctx, pg); err != nil {
			t.Fatal(err)
		}
		st.pages[p.Slug] = pg.ID
	}

	// The editor's ajax nonce, then everything is saved like the editor saves it.
	_, body, _ = get(t, c, fmt.Sprintf("%s/wp-admin/post?post=%d&action=web-builder", base, st.pages["home"]))
	nonce := regexp.MustCompile(`"ajax":\{"nonce":"([^"]+)"`).FindStringSubmatch(body)
	if nonce == nil {
		t.Fatal("no elementor_ajax nonce")
	}
	save := func(id int64, elements string, settings map[string]any) {
		t.Helper()
		sj, _ := json.Marshal(settings)
		actions := `{"s":{"action":"save_builder","data":{"status":"publish","elements":` + elements + `,"settings":` + string(sj) + `}}}`
		resp, err := c.PostForm(base+"/wp-admin/admin-ajax", url.Values{"action": {"elementor_ajax"}, "_nonce": {nonce[1]},
			"editor_post_id": {fmt.Sprint(id)}, "initial_document_id": {fmt.Sprint(id)}, "actions": {actions}})
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var out struct {
			Data struct {
				Responses map[string]struct {
					Success bool `json:"success"`
				} `json:"responses"`
			} `json:"data"`
		}
		if err := json.Unmarshal(b, &out); err != nil || !out.Data.Responses["s"].Success {
			t.Fatalf("save_builder %d: %v %.300s", id, err, b)
		}
	}

	// Kit globals: system colors and fonts, the kit's own, and the site style settings.
	kitPost, err := srv.gweKit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ks := gwePageSettings(kitPost)
	var sysC, cusC, sysT, cusT []any
	system := map[string]bool{"primary": true, "secondary": true, "text": true, "accent": true}
	for _, col := range k.Globals.Colors {
		if system[col["_id"].(string)] {
			sysC = append(sysC, col)
		} else {
			cusC = append(cusC, col)
		}
	}
	for _, ty := range k.Globals.Typography {
		if system[ty["_id"].(string)] {
			sysT = append(sysT, ty)
		} else {
			cusT = append(cusT, ty)
		}
	}
	ks["system_colors"], ks["custom_colors"], ks["system_typography"], ks["custom_typography"] = sysC, cusC, sysT, cusT
	for key, v := range k.Globals.Settings {
		ks[key] = v
	}
	save(kitPost.ID, "[]", ks)

	// Sample posts with featured images and categories, a day apart.
	for i, p := range k.Posts {
		file := strings.TrimSuffix(strings.TrimPrefix(p.Image, "{{library:"), "}}")
		f, err := os.Open(filepath.Join(glibKALibrary, filepath.FromSlash(file)))
		if err != nil {
			t.Fatal(err)
		}
		att, err := srv.insertAttachmentFile(ctx, path.Base(file), f, adminID, 0)
		f.Close()
		if err != nil {
			t.Fatalf("featured image %s: %v", file, err)
		}
		when := time.Now().Add(-time.Duration(len(k.Posts)-i) * 24 * time.Hour)
		ps := &store.Post{Type: "post", Title: p.Title, Slug: p.Slug, Excerpt: p.Excerpt, Content: st.replace(p.Content), Status: "publish",
			AuthorID: adminID, FeaturedMediaID: att.ID, PublishedAt: &when, CommentStatus: "open", PingStatus: "open", Meta: map[string]any{}}
		if err := srv.store.InsertPost(ctx, ps); err != nil {
			t.Fatal(err)
		}
		term, err := srv.store.TermByName(ctx, "category", p.Category)
		if err != nil || term == nil {
			term = &store.Term{Taxonomy: "category", Name: p.Category, Slug: strings.ToLower(strings.ReplaceAll(p.Category, " ", "-"))}
			if err := srv.store.InsertTerm(ctx, term); err != nil {
				t.Fatal(err)
			}
		}
		if err := srv.store.SetPostTerms(ctx, ps.ID, "category", []int64{term.ID}); err != nil {
			t.Fatal(err)
		}
		st.posts = append(st.posts, ps)
	}

	// Page designs, saved through save_builder with the Full Width template.
	for _, p := range k.Pages {
		if p.PostsPage {
			continue
		}
		settings := map[string]any{"post_status": "publish"}
		for key, v := range p.Settings {
			settings[key] = v
		}
		save(st.pages[p.Slug], st.replace(string(p.Content)), settings)
	}

	// Theme parts: created in the Theme Builder, saved in the editor, conditions set on the
	// conditions screen.
	_, body, _ = get(t, c, base+"/wp-admin/theme-builder")
	tb := nonceRe.FindStringSubmatch(body)
	if tb == nil {
		t.Fatal("no theme builder nonce")
	}
	for _, p := range k.Parts {
		code, _, loc := post(t, c, base+"/wp-admin/theme-builder", url.Values{"_wpnonce": {tb[1]}, "action": {"new"}, "type": {p.Type}, "title": {p.Title}})
		m := regexp.MustCompile(`^/wp-admin/post\?post=(\d+)&action=web-builder$`).FindStringSubmatch(loc)
		if code != http.StatusFound || m == nil {
			t.Fatalf("new %s: %d %q", p.Type, code, loc)
		}
		var id int64
		fmt.Sscan(m[1], &id)
		st.parts[p.Type] = id
		settings := map[string]any{"post_status": "publish"}
		for key, v := range p.Settings {
			settings[key] = v
		}
		save(id, st.replace(string(p.Content)), settings)
		form := url.Values{"_wpnonce": {tb[1]}, "action": {"conditions"}, "post": {m[1]}}
		for _, cd := range p.Conditions {
			mode, rule, _ := strings.Cut(cd, "/")
			form.Add("mode[]", mode)
			form.Add("rule[]", rule)
		}
		if code, _, loc := post(t, c, base+"/wp-admin/theme-builder", form); code != http.StatusFound || !strings.Contains(loc, "message=conditions") {
			t.Fatalf("conditions %s: %d %s", p.Type, code, loc)
		}
	}

	// Menus at their locations.
	locs := map[string]int64{}
	for _, m := range k.Menus {
		id, err := srv.store.CreateMenu(ctx, m.Name)
		if err != nil {
			t.Fatal(err)
		}
		for i, it := range m.Items {
			e := &store.EditItem{MenuID: id, Label: it.Title, Order: i + 1, Status: "publish"}
			if it.Page != "" {
				e.Kind, e.Object, e.TargetID = "post_type", "page", st.pages[it.Page]
			} else {
				e.Kind, e.Object, e.URL = "custom", "custom", st.replace(it.URL)
			}
			if err := srv.store.AddMenuItem(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
		locs[m.Location] = id
	}
	if err := srv.store.SetMenuLocations(ctx, locs); err != nil {
		t.Fatal(err)
	}

	// Settings › Reading.
	reading := map[string]any{"show_on_front": "page"}
	for _, p := range k.Pages {
		if p.FrontPage {
			reading["page_on_front"] = st.pages[p.Slug]
		}
		if p.PostsPage {
			reading["page_for_posts"] = st.pages[p.Slug]
		}
	}
	if err := srv.settings.Set(ctx, reading); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	return st
}

// glibKAFirstHeading is a visible string of an element tree: the first heading's leading text.
func glibKAFirstHeading(raw json.RawMessage) string {
	var find func(list []any) string
	find = func(list []any) string {
		for _, x := range list {
			el, _ := x.(map[string]any)
			if el == nil {
				continue
			}
			if el["widgetType"] == "heading" {
				if s, _ := el["settings"].(map[string]any)["title"].(string); s != "" {
					s, _, _ = strings.Cut(s, "<") // the plain text before any inline markup
					return s
				}
			}
			if kids, _ := el["elements"].([]any); len(kids) > 0 {
				if s := find(kids); s != "" {
					return s
				}
			}
		}
		return ""
	}
	var list []any
	_ = json.Unmarshal(raw, &list)
	return find(list)
}

// TestGolementorLibraryKAInstall installs each kit on a fresh site and renders its pages.
func TestGolementorLibraryKAInstall(t *testing.T) {
	for _, slug := range glibKASlugs {
		t.Run(slug, func(t *testing.T) {
			k := glibKALoad(t, slug)
			fonts := fakeFontFetch(t)
			ts, c, srv := newTestServer(t, true)
			st := glibKAInstall(t, ts.URL, c, srv, k)
			glibKACheckSite(t, st, fonts)
		})
	}
}

func glibKACheckSite(t *testing.T, st *glibKASite, fonts *[]string) {
	k := st.kit
	esc := func(s string) string { return html.EscapeString(html.UnescapeString(s)) }
	fetch := func(name, u string, wantCode int, want ...string) string {
		t.Helper()
		code, body, _ := get(t, st.c, u)
		if code != wantCode {
			t.Errorf("%s: %s: HTTP %d (want %d) %.300s", k.Slug, name, code, wantCode, body)
			return body
		}
		want = append(want, "elementor-location-header", "elementor-location-footer", "elementor-kit-", esc(k.Site.Name))
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: %s misses %q", k.Slug, name, w)
			}
		}
		for _, bad := range []string{"{{page:", "{{library:", "elementor-theme-placeholder", "No menu is assigned", "Choose a menu",
			"template: ", "<no value>", "%!"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: %s contains %q", k.Slug, name, bad)
			}
		}
		// Both menus render with every item.
		for _, m := range k.Menus {
			for _, it := range m.Items {
				if !strings.Contains(body, ">"+esc(it.Title)+"<") {
					t.Errorf("%s: %s: menu %s misses %q", k.Slug, name, m.Location, it.Title)
				}
			}
		}
		return body
	}
	pages := map[string]*glibKAPage{}
	for i := range k.Pages {
		pages[k.Pages[i].Slug] = &k.Pages[i]
	}

	home := fetch("home", st.base+"/", http.StatusOK, esc(glibKAFirstHeading(pages["home"].Content)), "elementor-widget-nav-menu")
	if !strings.Contains(home, "e-con") {
		t.Errorf("%s: home has no containers", k.Slug)
	}
	for _, p := range k.Pages {
		if p.PostsPage || p.FrontPage {
			continue
		}
		fetch(p.Slug, st.pageURL(p.Slug), http.StatusOK, esc(glibKAFirstHeading(p.Content)))
	}
	contact := fetch("contact", st.pageURL("contact"), http.StatusOK, "elementor-form")
	if !strings.Contains(contact, `name="form_fields[email]"`) {
		t.Errorf("%s: contact form has no e-mail field", k.Slug)
	}

	// Blog: the archive part lists the newest posts with their images and paginates (3 per page).
	var blog string
	for _, p := range k.Pages {
		if p.PostsPage {
			blog = fetch("blog", st.pageURL(p.Slug), http.StatusOK, "elementor-location-archive", "elementor-posts-container",
				esc(k.Posts[len(k.Posts)-1].Title), "elementor-post__thumbnail", "elementor-pagination")
			fetch("blog page 2", st.pageURL(p.Slug)+"page/2/", http.StatusOK, "elementor-location-archive", esc(k.Posts[0].Title))
		}
	}
	_ = blog

	// A post: the single-post part with its title, meta, featured image and content.
	last := st.posts[len(st.posts)-1]
	firstPara := regexp.MustCompile(`<p>(.*?)</p>`).FindStringSubmatch(last.Content)
	single := fetch("single post", st.base+"/"+last.Slug+"/", http.StatusOK, "elementor-location-single-post", esc(last.Title),
		"elementor-widget-theme-post-featured-image", "elementor-post-info", k.Posts[len(k.Posts)-1].Category)
	if firstPara != nil && !strings.Contains(single, firstPara[1][:40]) {
		t.Errorf("%s: single post misses its content", k.Slug)
	}
	if !regexp.MustCompile(`elementor-widget-theme-post-featured-image[\s\S]*?<img[^>]+src="[^"]+\.webp"`).MatchString(single) {
		t.Errorf("%s: single post has no featured image", k.Slug)
	}

	// An ordinary page (the install's sample page) uses the single-page part.
	fetch("sample page", st.base+"/sample-page/", http.StatusOK, "elementor-location-single-page", "Sample Page")

	// Search: the search-results part with the query and a match; no match shows the message.
	word := strings.Fields(last.Title)[len(strings.Fields(last.Title))-1]
	fetch("search", st.base+"/?s="+url.QueryEscape(word), http.StatusOK, "elementor-location-search-results", "elementor-search-form", esc(last.Title))
	fetch("search miss", st.base+"/?s=zzqxv", http.StatusOK, "elementor-location-search-results", "elementor-posts-nothing-found")

	// 404.
	fetch("404", st.base+"/no-such-page-here/", http.StatusNotFound, "elementor-location-error-404", "elementor-search-form")

	// The kit stylesheet carries the palette, and the kit's Google fonts were loaded locally.
	kitPost, _ := st.srv.gweKit(context.Background())
	_, css, _ := get(t, st.c, fmt.Sprintf("%s/wp-content/uploads/elementor/css/post-%d.css", st.base, kitPost.ID))
	for _, col := range k.Globals.Colors {
		if !strings.Contains(strings.ToUpper(css), strings.ToUpper(fmt.Sprint(col["color"]))) {
			t.Errorf("%s: kit CSS misses color %v", k.Slug, col["color"])
		}
	}
	families := map[string]bool{}
	for _, ty := range k.Globals.Typography {
		families[ty["typography_font_family"].(string)] = true
	}
	var fams []string
	for f := range families {
		fams = append(fams, f)
	}
	sort.Strings(fams)
	got := strings.Join(*fonts, " ")
	for _, f := range fams {
		if !strings.Contains(got, "family="+strings.ReplaceAll(f, " ", "+")) && !strings.Contains(got, "family="+url.QueryEscape(f)) {
			t.Errorf("%s: Google font %s was not loaded (%v)", k.Slug, f, *fonts)
		}
	}
	// Every library asset the pages reference is served.
	seen := map[string]bool{}
	for _, m := range glibKAToken.FindAllStringSubmatch(string(k.raw), -1) {
		if m[1] != "library" || seen[m[2]] {
			continue
		}
		seen[m[2]] = true
		if code, _, _ := get(t, st.c, st.base+gweAssetsPath+"library/"+m[2]); code != http.StatusOK {
			t.Errorf("%s: %s: HTTP %d", k.Slug, m[2], code)
		}
	}
}
