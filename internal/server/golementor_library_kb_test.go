package server

import (
	"bytes"
	"compress/gzip"
	"context"
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
	"sort"
	"strings"
	"testing"
	"time"

	"wpgo/internal/builder"
	"wpgo/internal/store"
)

// Golementor Studio site kits 1–3 (group KB): Dispatch (blog/magazine), Fixit (local service
// business) and Jade (personal brand/creator), web/gowebeditor/library/kits/<slug>/kit.json.
// TestGolementorLibraryKBSchema checks every kit against the format in docs/golementor-studio.md §3
// (required fields, part types, conditions, pages, menus, posts), every element against the
// widget/container controls the editor loads, every __globals__ reference against the kit's own
// globals, every token and every image; TestGolementorLibraryKBInstall installs each kit on a fresh
// site the way the setup wizard will (kit globals, theme parts with conditions through the Theme
// Builder, pages and the kit's posts through REST, everything saved with save_builder, menus and
// Settings › Reading through the store) and renders the front page, the blog, a post, a page,
// contact, search and 404 with the kit's header and footer.

const glibKBDir = "../../web/gowebeditor/library"

var glibKBKits = []string{"dispatch", "fixit", "jade-creator"}

var glibKBPartTypes = []string{"header", "footer", "single-post", "single-page", "archive", "search-results", "error-404"}

var glibKBCategories = map[string]bool{"business": true, "restaurant": true, "portfolio": true, "blog": true, "service": true, "personal": true}

type glibKBPart struct {
	Type       string          `json:"type"`
	Title      string          `json:"title"`
	Conditions []string        `json:"conditions"`
	Settings   map[string]any  `json:"settings"`
	Content    json.RawMessage `json:"content"`
}

type glibKBPage struct {
	Slug      string          `json:"slug"`
	Title     string          `json:"title"`
	FrontPage bool            `json:"front_page"`
	PostsPage bool            `json:"posts_page"`
	Optional  bool            `json:"optional"`
	Template  string          `json:"template"` // default: elementor_header_footer
	Settings  map[string]any  `json:"settings"`
	Content   json.RawMessage `json:"content"`
}

type glibKBMenuItem struct {
	Title    string           `json:"title"`
	Page     string           `json:"page"`
	URL      string           `json:"url"`
	Children []glibKBMenuItem `json:"children"`
}

type glibKBMenu struct {
	Location string           `json:"location"`
	Name     string           `json:"name"`
	Items    []glibKBMenuItem `json:"items"`
}

type glibKBPost struct {
	Title      string   `json:"title"`
	Slug       string   `json:"slug"`
	Excerpt    string   `json:"excerpt"`
	Content    string   `json:"content"`
	Image      string   `json:"image"`
	Categories []string `json:"categories"`
}

type glibKBKit struct {
	Title       string            `json:"title"`
	Category    string            `json:"category"`
	Description string            `json:"description"`
	Screenshot  string            `json:"screenshot"`
	Options     map[string]string `json:"options"`  // blogname / blogdescription defaults
	Settings    map[string]any    `json:"settings"` // kit Site Settings (Theme Style, custom CSS)
	Globals     struct {
		Colors     []map[string]any `json:"colors"`
		Typography []map[string]any `json:"typography"`
	} `json:"globals"`
	Parts []glibKBPart `json:"parts"`
	Pages []glibKBPage `json:"pages"`
	Menus []glibKBMenu `json:"menus"`
	Posts []glibKBPost `json:"posts"`
}

func glibKBLoad(t *testing.T, slug string) *glibKBKit {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(glibKBDir, "kits", slug, "kit.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var k glibKBKit
	if err := dec.Decode(&k); err != nil {
		t.Fatalf("%s/kit.json: %v", slug, err)
	}
	return &k
}

// glibKBControls are the control names of every element type the editor knows: Golementor Pro's
// widgets config, the Site widgets of internal/builder, and the container's document controls.
type glibKBControls struct {
	widgets map[string]map[string]map[string]any // widget -> control -> control config
}

func glibKBReadGz(t *testing.T, name string) map[string]any {
	t.Helper()
	f, err := gweFS.Open("gowebeditor/" + name)
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

func glibKBLoadControls(t *testing.T) *glibKBControls {
	t.Helper()
	c := &glibKBControls{widgets: map[string]map[string]map[string]any{}}
	add := func(name string, ctrls map[string]any) {
		m := c.widgets[name]
		if m == nil {
			m = map[string]map[string]any{}
			c.widgets[name] = m
		}
		for k, v := range ctrls {
			cm, _ := v.(map[string]any)
			m[k] = cm
		}
	}
	for name, w := range glibKBReadGz(t, "pro-widgets-config.json.gz") {
		if wm, ok := w.(map[string]any); ok {
			ctrls, _ := wm["controls"].(map[string]any)
			add(name, ctrls)
		}
	}
	for name, w := range glibKBReadGz(t, "pro-doc-widgets.json.gz") {
		if name != "container" {
			continue
		}
		ctrls, _ := w.(map[string]any)["controls"].(map[string]any)
		add(name, ctrls)
	}
	// The Site widgets of internal/builder replace Pro's entries in the config the editor loads
	// (gweThemeWidgetsConfig), so their controls are the ones that count.
	for _, w := range builder.ThemeWidgets() {
		delete(c.widgets, w.Name)
		ctrls := map[string]any{}
		for _, ctl := range w.Controls {
			if n, _ := ctl["name"].(string); n != "" {
				ctrls[n] = ctl
			}
		}
		add(w.Name, ctrls)
	}
	return c
}

// glibKBControl finds a control, also as the responsive variant (<name>_tablet, <name>_mobile) the
// editor adds for the active breakpoints.
func glibKBControl(controls, common map[string]map[string]any, key string) map[string]any {
	for _, k := range []string{key, strings.TrimSuffix(key, "_tablet"), strings.TrimSuffix(key, "_mobile")} {
		if c := controls[k]; c != nil {
			return c
		}
		if c := common[k]; c != nil {
			return c
		}
	}
	return nil
}

// glibKBIcons lists the icons of internal/builder/data/icons.json.gz as "<library>/<name>".
func glibKBIcons(t *testing.T) map[string]bool {
	t.Helper()
	f, err := os.Open("../builder/data/icons.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var libs map[string]map[string]any
	if err := json.NewDecoder(zr).Decode(&libs); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for lib, icons := range libs {
		for name := range icons {
			out[lib+"/"+name] = true
		}
	}
	return out
}

var glibKBFieldWidths = map[string]bool{"20": true, "25": true, "30": true, "33": true, "70": true, "40": true, "50": true, "60": true, "66": true, "75": true, "80": true, "100": true}

var glibKBTokenRe = regexp.MustCompile(`\{\{([a-z]+):([^}]*)\}\}`)

var glibKBGlobalRe = regexp.MustCompile(`^globals/(colors|typography)\?id=([a-z0-9_]+)$`)

var glibKBHexRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// glibKBWalk checks one element tree (and returns the widget types it uses).
func glibKBWalk(t *testing.T, where string, raw json.RawMessage, ctl *glibKBControls, colors, typos map[string]bool, used map[string]int) {
	t.Helper()
	var els []map[string]any
	if err := json.Unmarshal(raw, &els); err != nil {
		t.Errorf("%s: content is not an element list: %v", where, err)
		return
	}
	if len(els) == 0 {
		t.Errorf("%s: empty content", where)
	}
	if _, err := builder.ParseElements(raw); err != nil {
		t.Errorf("%s: element tree does not load: %v", where, err)
	}
	common := ctl.widgets["common"]
	var walk func(path string, el map[string]any, depth int)
	walk = func(path string, el map[string]any, depth int) {
		typ, _ := el["elType"].(string)
		name := typ
		switch typ {
		case "container":
		case "widget":
			name, _ = el["widgetType"].(string)
			if len(el["elements"].([]any)) != 0 {
				t.Errorf("%s: widget %s has children", path, name)
			}
		default:
			t.Errorf("%s: elType %q (only containers and widgets)", path, typ)
			return
		}
		used[name]++
		controls := ctl.widgets[name]
		if controls == nil {
			t.Errorf("%s: unknown element %q", path, name)
			return
		}
		if id, _ := el["id"].(string); !regexp.MustCompile(`^[0-9a-f]{7}$`).MatchString(id) {
			t.Errorf("%s: bad id %q", path, id)
		}
		settings, _ := el["settings"].(map[string]any)
		for key, val := range settings {
			if key == "__globals__" {
				for gk, gv := range val.(map[string]any) {
					if glibKBControl(controls, common, gk) == nil {
						t.Errorf("%s (%s): __globals__ for unknown control %q", path, name, gk)
					}
					m := glibKBGlobalRe.FindStringSubmatch(fmt.Sprint(gv))
					switch {
					case m == nil:
						t.Errorf("%s (%s): bad global %q=%v", path, name, gk, gv)
					case m[1] == "colors" && !colors[m[2]]:
						t.Errorf("%s (%s): unknown global color %q", path, name, m[2])
					case m[1] == "typography" && !typos[m[2]]:
						t.Errorf("%s (%s): unknown global typography %q", path, name, m[2])
					}
				}
				continue
			}
			if key == "__dynamic__" { // dynamic tags (post excerpt, ...)
				for dk, dv := range val.(map[string]any) {
					if glibKBControl(controls, common, dk) == nil || !regexp.MustCompile(`^\[elementor-tag id="[0-9a-f]{7}" name="[a-z-]+" settings="[^"]*"\]$`).MatchString(fmt.Sprint(dv)) {
						t.Errorf("%s (%s): dynamic tag %s=%v", path, name, dk, dv)
					}
				}
				continue
			}
			c := glibKBControl(controls, common, key)
			if c == nil && name == "nav-menu" && key == "menu_location" {
				continue // read by the renderer: the theme location the menu comes from (Hello Golementor: primary, footer)
			}
			if c == nil {
				t.Errorf("%s (%s): unknown control %q", path, name, key)
				continue
			}
			// A literal color must not be used where a global belongs (transparent is fine).
			if ct, _ := c["type"].(string); ct == "color" {
				if s, _ := val.(string); s != "" && !strings.HasSuffix(s, "00000000") {
					t.Errorf("%s (%s): literal color %s=%s (use the kit globals)", path, name, key, s)
				}
			}
		}
		// Form field widths are the select's values (elementor-col-<n>); anything else has no CSS.
		if name == "form" {
			for _, ff := range settings["form_fields"].([]any) {
				m := ff.(map[string]any)
				for _, k := range []string{"width", "width_tablet", "width_mobile"} {
					if w, ok := m[k].(string); ok && w != "" && !glibKBFieldWidths[w] {
						t.Errorf("%s (form): field %v %s=%s", path, m["custom_id"], k, w)
					}
				}
			}
		}
		kids, _ := el["elements"].([]any)
		for i, k := range kids {
			km, _ := k.(map[string]any)
			walk(fmt.Sprintf("%s/%d", path, i), km, depth+1)
		}
	}
	for i, el := range els {
		walk(fmt.Sprintf("%s[%d]", where, i), el, 0)
	}
}

func TestGolementorLibraryKBSchema(t *testing.T) {
	ctl := glibKBLoadControls(t)
	for _, slug := range glibKBKits {
		t.Run(slug, func(t *testing.T) {
			k := glibKBLoad(t, slug)
			if k.Title == "" || k.Description == "" || !glibKBCategories[k.Category] {
				t.Errorf("title/description/category: %q %q %q", k.Title, k.Description, k.Category)
			}
			if k.Screenshot != "screenshot.webp" {
				t.Errorf("screenshot = %q", k.Screenshot)
			}
			if st, err := os.Stat(filepath.Join(glibKBDir, "kits", slug, k.Screenshot)); err != nil || st.Size() < 2000 || st.Size() > 400_000 {
				t.Errorf("screenshot file: %v", err)
			}

			// Globals: the four system colors and fonts, then the kit's own; families load locally.
			colors, typos := map[string]bool{}, map[string]bool{}
			for _, c := range k.Globals.Colors {
				id, _ := c["_id"].(string)
				if colors[id] || !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(id) || c["title"] == "" || !glibKBHexRe.MatchString(fmt.Sprint(c["color"])) {
					t.Errorf("color %v", c)
				}
				colors[id] = true
			}
			fonts := map[string]bool{}
			for _, ty := range k.Globals.Typography {
				id, _ := ty["_id"].(string)
				if typos[id] || id == "" || ty["title"] == "" || ty["typography_typography"] != "custom" {
					t.Errorf("typography %v", ty)
				}
				typos[id] = true
				fam, _ := ty["typography_font_family"].(string)
				if gweGoogleFontType(fam) == "" {
					t.Errorf("typography %s: %q is not a Google font Pursae Web Builder can load", id, fam)
				}
				fonts[fam] = true
				for key := range ty {
					if key != "_id" && key != "title" && !strings.HasPrefix(key, "typography_") {
						t.Errorf("typography %s: key %q", id, key)
					}
				}
			}
			for _, id := range []string{"primary", "secondary", "text", "accent"} {
				if !colors[id] || !typos[id] {
					t.Errorf("system global %q missing", id)
				}
			}
			if len(fonts) > 2 {
				t.Errorf("more than a font pairing: %v", fonts)
			}
			kitCtl := glibKBReadGz(t, "pro-doc-kit.json.gz")["settings"].(map[string]any)["controls"].(map[string]any)
			for key, val := range k.Settings {
				if key == "__globals__" {
					for gk, gv := range val.(map[string]any) {
						m := glibKBGlobalRe.FindStringSubmatch(fmt.Sprint(gv))
						if kitCtl[gk] == nil && kitCtl[strings.TrimSuffix(gk, "_typography")+"_typography"] == nil || m == nil ||
							(m[1] == "colors" && !colors[m[2]]) || (m[1] == "typography" && !typos[m[2]]) {
							t.Errorf("theme style global %s=%v", gk, gv)
						}
					}
					continue
				}
				if kitCtl[key] == nil {
					t.Errorf("theme style: unknown kit setting %q", key)
				}
			}

			// Parts: one of each type, upstream condition strings.
			seen := map[string]bool{}
			used := map[string]int{}
			for _, p := range k.Parts {
				if seen[p.Type] || p.Title == "" || len(p.Conditions) == 0 {
					t.Errorf("part %q: duplicate, untitled or without conditions", p.Type)
				}
				seen[p.Type] = true
				for _, c := range p.Conditions {
					mode, rule, _ := strings.Cut(c, "/")
					if (mode != "include" && mode != "exclude") || rule == "" {
						t.Errorf("part %s: condition %q", p.Type, c)
					}
				}
				if want := gweDefaultConditions[p.Type]; strings.Join(p.Conditions, ",") != strings.Join(want, ",") {
					t.Logf("part %s: conditions %v (Theme Builder default %v)", p.Type, p.Conditions, want)
				}
				glibKBWalk(t, slug+"/"+p.Type, p.Content, ctl, colors, typos, used)
			}
			for _, typ := range glibKBPartTypes {
				if !seen[typ] {
					t.Errorf("part %q missing", typ)
				}
			}
			// The parts are dynamic where it matters.
			for _, w := range []string{"theme-site-title", "nav-menu", "theme-post-title", "theme-post-content", "theme-post-featured-image",
				"post-info", "theme-archive-title", "archive-posts", "search-form", "theme-site-copyright"} {
				if used[w] == 0 {
					t.Errorf("no %s in the parts", w)
				}
			}

			// Pages.
			pages := map[string]*glibKBPage{}
			front, blog := 0, 0
			for i := range k.Pages {
				pg := &k.Pages[i]
				if pages[pg.Slug] != nil || !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(pg.Slug) || pg.Title == "" {
					t.Errorf("page %q", pg.Slug)
				}
				pages[pg.Slug] = pg
				if pg.FrontPage {
					front++
				}
				if pg.PostsPage {
					blog++
					if len(pg.Content) != 0 {
						t.Errorf("posts page %q has content (the archive part shows the posts)", pg.Slug)
					}
					continue
				}
				if pg.Template != "" || pg.Settings["template"] != nil {
					t.Errorf("page %q: template %q (kit pages use the default, Full Width)", pg.Slug, pg.Template)
				}
				glibKBWalk(t, slug+"/page:"+pg.Slug, pg.Content, ctl, colors, typos, used)
			}
			if front != 1 || blog != 1 || len(k.Pages) < 5 {
				t.Errorf("pages: %d front, %d posts page, %d in all", front, blog, len(k.Pages))
			}
			for _, s := range []string{"home", "blog", "about", "contact"} {
				if pages[s] == nil {
					t.Errorf("page %q missing", s)
				}
			}
			if used["form"] == 0 {
				t.Error("no contact form")
			}

			// Menus and posts.
			locs := map[string]bool{}
			for _, m := range k.Menus {
				if (m.Location != "primary" && m.Location != "footer") || locs[m.Location] || m.Name == "" || len(m.Items) == 0 {
					t.Errorf("menu %q at %q", m.Name, m.Location)
				}
				locs[m.Location] = true
				for _, it := range m.Items {
					n := 0
					if it.Page != "" {
						n++
						if pages[it.Page] == nil {
							t.Errorf("menu item %q: no page %q", it.Title, it.Page)
						}
					}
					if it.URL != "" {
						n++
						if !strings.HasPrefix(it.URL, "{{page:") && !strings.HasPrefix(it.URL, "http") && !strings.HasPrefix(it.URL, "/") {
							t.Errorf("menu item %q: url %q", it.Title, it.URL)
						}
					}
					if n != 1 || (it.Title == "" && it.Page == "") {
						t.Errorf("menu item %+v", it)
					}
				}
			}
			if !locs["primary"] || !locs["footer"] {
				t.Errorf("menus: %v", locs)
			}
			if len(k.Posts) < 4 || len(k.Posts) > 6 {
				t.Errorf("%d posts", len(k.Posts))
			}
			for _, p := range k.Posts {
				if p.Title == "" || p.Excerpt == "" || !strings.HasPrefix(p.Content, "<!-- wp:") || len(p.Content) < 800 || p.Image == "" {
					t.Errorf("post %q", p.Title)
				}
				if len(p.Categories) == 0 || strings.Count(p.Image, "{{library:") != 1 {
					t.Errorf("post %q: categories %v, image %q", p.Title, p.Categories, p.Image)
				}
			}

			// Tokens and images; no placeholder copy.
			raw, _ := os.ReadFile(filepath.Join(glibKBDir, "kits", slug, "kit.json"))
			for _, m := range glibKBTokenRe.FindAllStringSubmatch(string(raw), -1) {
				switch m[1] {
				case "page":
					if pages[m[2]] == nil {
						t.Errorf("token %s: no such page", m[0])
					}
				case "library":
					if !strings.HasPrefix(m[2], "images/") || !strings.HasSuffix(m[2], ".webp") {
						t.Errorf("token %s", m[0])
					} else if st, err := os.Stat(filepath.Join(glibKBDir, filepath.FromSlash(m[2]))); err != nil || st.Size() > 160_000 {
						t.Errorf("token %s: %v", m[0], err)
					}
				default:
					t.Errorf("unknown token %s", m[0])
				}
			}
			// Font Awesome icons must exist in the icon set the renderer ships (Font Awesome 5 names).
			icons := glibKBIcons(t)
			for _, m := range regexp.MustCompile(`"value": ?"(fa[srb]) fa-([a-z0-9-]+)"`).FindAllStringSubmatch(string(raw), -1) {
				lib := map[string]string{"fas": "fa-solid", "far": "fa-regular", "fab": "fa-brands"}[m[1]]
				if !icons[lib+"/"+m[2]] {
					t.Errorf("unknown icon %s fa-%s", m[1], m[2])
				}
			}
			if n := strings.Count(string(raw), "{{") - len(glibKBTokenRe.FindAllString(string(raw), -1)); n != 0 {
				t.Errorf("%d malformed tokens", n)
			}
			for _, bad := range []string{"Lorem", "lorem ipsum", "Add Your Heading Text Here", "Click here", "placeholder.png"} {
				if strings.Contains(string(raw), bad) {
					t.Errorf("placeholder copy %q", bad)
				}
			}
		})
	}
}

// --- install the way the wizard will, and render ------------------------------------------------

type glibKBSite struct {
	t        *testing.T
	base     string
	c        *http.Client
	srv      *Server
	rest     string // REST nonce
	ajax     string // elementor_ajax nonce
	tbNonce  string
	pages    map[string]int64
	pageURL  map[string]string
	catByNm  map[string]int64 // post categories by name
	catURL   map[string]string
	images   map[string]int64 // token -> attachment
	partIDs  map[string]int64
	postURLs []string
}

func (s *glibKBSite) restDo(method, p string, payload any) map[string]any {
	s.t.Helper()
	var rd io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, s.base+"/wp-json"+p, rd)
	req.Header.Set("X-WP-Nonce", s.rest)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode >= 300 {
		s.t.Fatalf("%s %s: %d %.300s", method, p, resp.StatusCode, raw)
	}
	return out
}

func (s *glibKBSite) ajaxDo(postID int64, actions string) map[string]any {
	s.t.Helper()
	resp, err := s.c.PostForm(s.base+"/wp-admin/admin-ajax", url.Values{"action": {"elementor_ajax"}, "_nonce": {s.ajax},
		"editor_post_id": {itoa64(postID)}, "initial_document_id": {itoa64(postID)}, "actions": {actions}})
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		s.t.Fatalf("ajax: %v %.300s", err, b)
	}
	r, _ := out["data"].(map[string]any)["responses"].(map[string]any)["a"].(map[string]any)
	if r == nil || r["success"] != true {
		s.t.Fatalf("ajax on %d: %.500s", postID, b)
	}
	return r
}

// fill replaces the kit tokens in a JSON text with this site's URLs.
func (s *glibKBSite) fill(raw string) string {
	return glibKBTokenRe.ReplaceAllStringFunc(raw, func(m string) string {
		sm := glibKBTokenRe.FindStringSubmatch(m)
		var u string
		switch sm[1] {
		case "page":
			u = s.pageURL[sm[2]]
		case "library":
			u = s.base + gweAssetsPath + "library/" + sm[2]
		}
		if u == "" {
			s.t.Errorf("unresolved token %s", m)
		}
		return u
	})
}

// save stores an element tree and document settings with save_builder.
func (s *glibKBSite) save(id int64, content json.RawMessage, settings map[string]any) {
	s.t.Helper()
	if settings == nil {
		settings = map[string]any{}
	}
	settings["post_status"] = "publish"
	st, _ := json.Marshal(settings)
	elements := "[]"
	if len(content) > 0 {
		elements = s.fill(string(content))
	}
	s.ajaxDo(id, `{"a":{"action":"save_builder","data":{"status":"publish","elements":`+elements+`,"settings":`+string(st)+`}}}`)
}

func (s *glibKBSite) upload(token string) int64 {
	s.t.Helper()
	if id, ok := s.images[token]; ok {
		return id
	}
	rel := strings.TrimSuffix(strings.TrimPrefix(token, "{{library:"), "}}")
	data, err := os.ReadFile(filepath.Join(glibKBDir, filepath.FromSlash(rel)))
	if err != nil {
		s.t.Fatal(err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", path.Base(rel))
	_, _ = fw.Write(data)
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, s.base+"/wp-json/wp/v2/media", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-WP-Nonce", s.rest)
	resp, err := s.c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusCreated {
		s.t.Fatalf("upload %s: %d %.300s", rel, resp.StatusCode, raw)
	}
	id := int64(out["id"].(float64))
	s.images[token] = id
	return id
}

// glibKBInstall installs a kit on a freshly installed site.
func glibKBInstall(t *testing.T, base string, c *http.Client, srv *Server, k *glibKBKit) *glibKBSite {
	t.Helper()
	ctx := context.Background()
	s := &glibKBSite{t: t, base: base, c: c, srv: srv, pages: map[string]int64{}, pageURL: map[string]string{},
		catByNm: map[string]int64{}, catURL: map[string]string{}, images: map[string]int64{}, partIDs: map[string]int64{}}
	_, s.rest, _ = get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")

	// Hello Golementor is the theme the kits are made for.
	_, body, _ := get(t, c, base+"/wp-admin/themes")
	act := regexp.MustCompile(`href="(/wp-admin/themes\?action=activate&amp;stylesheet=hello-gowebeditor&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
	if act == nil {
		t.Fatal("no activate link for Hello Pursae Web Builder")
	}
	if code, _, _ := get(t, c, base+html.UnescapeString(act[1])); code != http.StatusFound {
		t.Fatalf("activate Hello Pursae Web Builder: %d", code)
	}

	// A fresh site's "Hello world!" post would lead the kit's blog; the starter replaces it.
	s.restDo(http.MethodDelete, "/wp/v2/posts/1?force=true", nil)

	// Categories and pages first: the tokens point at them.
	for _, p := range k.Posts {
		for _, name := range p.Categories {
			if _, ok := s.catByNm[name]; !ok {
				out := s.restDo(http.MethodPost, "/wp/v2/categories", map[string]any{"name": name})
				s.catByNm[name] = int64(out["id"].(float64))
				s.catURL[name], _ = out["link"].(string)
			}
		}
	}
	for _, pg := range k.Pages {
		out := s.restDo(http.MethodPost, "/wp/v2/pages", map[string]any{"title": pg.Title, "slug": pg.Slug, "status": "publish"})
		s.pages[pg.Slug] = int64(out["id"].(float64))
		s.pageURL[pg.Slug], _ = out["link"].(string)
	}
	// The editor's nonce comes with the editor page.
	_, body, _ = get(t, c, base+"/wp-admin/post?post="+itoa64(s.pages[k.Pages[0].Slug])+"&action=golementor")
	m := regexp.MustCompile(`"ajax":\{"nonce":"([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no elementor_ajax nonce")
	}
	s.ajax = m[1]

	// Kit globals and theme style.
	kit, err := srv.gweKit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings := gwePageSettings(kit)
	var sysC, cusC, sysT, cusT []any
	for _, c := range k.Globals.Colors {
		if id := c["_id"].(string); id == "primary" || id == "secondary" || id == "text" || id == "accent" {
			sysC = append(sysC, c)
		} else {
			cusC = append(cusC, c)
		}
	}
	for _, ty := range k.Globals.Typography {
		if id := ty["_id"].(string); id == "primary" || id == "secondary" || id == "text" || id == "accent" {
			sysT = append(sysT, ty)
		} else {
			cusT = append(cusT, ty)
		}
	}
	settings["system_colors"], settings["custom_colors"], settings["system_typography"], settings["custom_typography"] = sysC, cusC, sysT, cusT
	for key, v := range k.Settings {
		settings[key] = v
	}
	s.save(kit.ID, nil, settings)

	// Posts, newest first, with their featured images.
	now := time.Now().UTC().Truncate(time.Hour)
	for i, p := range k.Posts {
		var cats []int64
		for _, name := range p.Categories {
			cats = append(cats, s.catByNm[name])
		}
		out := s.restDo(http.MethodPost, "/wp/v2/posts", map[string]any{"title": p.Title, "slug": p.Slug, "content": p.Content,
			"excerpt": p.Excerpt, "status": "publish", "categories": cats, "featured_media": s.upload(p.Image),
			"date_gmt": now.Add(-time.Duration(i*26+3) * time.Hour).Format("2006-01-02T15:04:05")})
		link, _ := out["link"].(string)
		s.postURLs = append(s.postURLs, link)
	}

	// Pages: the element tree and the Full Width template.
	for _, pg := range k.Pages {
		if pg.PostsPage {
			continue
		}
		st := map[string]any{"template": "elementor_header_footer"}
		if pg.Template != "" {
			st["template"] = pg.Template
		}
		for key, v := range pg.Settings {
			st[key] = v
		}
		s.save(s.pages[pg.Slug], pg.Content, st)
	}

	// Theme parts through the Theme Builder: Add New, save_builder, display conditions.
	_, body, _ = get(t, c, base+"/wp-admin/gowebeditor-theme-builder")
	s.tbNonce = formNonce(t, body)
	for _, part := range k.Parts {
		code, _, loc := post(t, c, base+"/wp-admin/gowebeditor-theme-builder", url.Values{"_wpnonce": {s.tbNonce}, "action": {"new"},
			"type": {part.Type}, "title": {part.Title}})
		pm := regexp.MustCompile(`^/wp-admin/post\?post=(\d+)&action=golementor$`).FindStringSubmatch(loc)
		if code != http.StatusFound || pm == nil {
			t.Fatalf("new %s: %d %q", part.Type, code, loc)
		}
		var id int64
		fmt.Sscan(pm[1], &id)
		s.partIDs[part.Type] = id
		s.save(id, part.Content, nil)
		form := url.Values{"_wpnonce": {s.tbNonce}, "action": {"conditions"}, "post": {pm[1]}}
		for _, cond := range part.Conditions {
			mode, rule, _ := strings.Cut(cond, "/")
			form.Add("mode[]", mode)
			form.Add("rule[]", rule)
		}
		if code, _, loc := post(t, c, base+"/wp-admin/gowebeditor-theme-builder", form); code != http.StatusFound || !strings.Contains(loc, "message=conditions") {
			t.Fatalf("conditions of %s: %d %s", part.Type, code, loc)
		}
		var conds string
		_ = srv.store.Pool.QueryRow(ctx, `SELECT meta->'_gwe_conditions'::text FROM posts WHERE id=$1`, id).Scan(&conds)
		want, _ := json.Marshal(part.Conditions)
		if strings.ReplaceAll(conds, " ", "") != string(want) {
			t.Errorf("%s conditions stored as %s, want %s", part.Type, conds, want)
		}
	}

	// Menus and Settings › Reading.
	locs := map[string]int64{}
	for _, mn := range k.Menus {
		id, err := srv.store.CreateMenu(ctx, mn.Name)
		if err != nil {
			t.Fatal(err)
		}
		for i, it := range mn.Items {
			e := &store.EditItem{MenuID: id, Label: it.Title, Order: i + 1, Status: "publish"}
			switch {
			case it.Page != "":
				e.Kind, e.Object, e.TargetID = "post_type", "page", s.pages[it.Page]
			default:
				e.Kind, e.Object, e.URL = "custom", "custom", s.fill(it.URL)
			}
			if err := srv.store.AddMenuItem(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
		locs[mn.Location] = id
	}
	if err := srv.store.SetMenuLocations(ctx, locs); err != nil {
		t.Fatal(err)
	}
	reading := map[string]any{"show_on_front": "page", "posts_per_page": 4}
	for _, pg := range k.Pages {
		if pg.FrontPage {
			reading["page_on_front"] = s.pages[pg.Slug]
		}
		if pg.PostsPage {
			reading["page_for_posts"] = s.pages[pg.Slug]
		}
	}
	if err := srv.settings.Set(ctx, reading); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	return s
}

// glibKBVisibleText is the page's text without tags, scripts and styles.
func glibKBVisibleText(body string) string {
	body = regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>`).ReplaceAllString(body, " ")
	body = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(body, " ")
	return strings.Join(strings.Fields(html.UnescapeString(body)), " ")
}

// glibKBTexts returns a few headings of a tree, to look for on the rendered page.
func glibKBTexts(raw json.RawMessage, n int) []string {
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["widgetType"] == "heading" {
				if st, ok := x["settings"].(map[string]any); ok {
					if s, _ := st["title"].(string); s != "" && !strings.Contains(s, "{{") && len(out) < n {
						out = append(out, strings.Join(strings.Fields(html.UnescapeString(s)), " "))
					}
				}
			}
			for _, k := range []string{"elements"} {
				walk(x[k])
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	var v any
	_ = json.Unmarshal(raw, &v)
	walk(v)
	return out
}

func TestGolementorLibraryKBInstall(t *testing.T) {
	hold := os.Getenv("GLIBKB_HOLD") // screenshots: keep each installed site up until this file is removed
	for _, slug := range glibKBKits {
		t.Run(slug, func(t *testing.T) {
			k := glibKBLoad(t, slug)
			ts, c, srv := newTestServer(t, true)
			base := ts.URL
			installAndLogin(t, ts, c)
			if hold == "" { // no Google Fonts downloads in the test run
				if err := srv.settings.Set(context.Background(), map[string]any{"gwe_local_google_fonts": "0"}); err != nil {
					t.Fatal(err)
				}
			} else {
				_ = srv.settings.Set(context.Background(), map[string]any{"blogname": k.Title, "blogdescription": k.Description})
			}
			site := glibKBInstall(t, base, c, srv, k)
			anon := &http.Client{CheckRedirect: c.CheckRedirect}

			check := func(name, u string, wantCode int, want ...string) string {
				t.Helper()
				code, body, _ := getPage(t, anon, u)
				if code != wantCode {
					t.Errorf("%s %s: HTTP %d, want %d", name, u, code, wantCode)
				}
				vis := glibKBVisibleText(body)
				for _, w := range append([]string{"elementor-location-header", "elementor-location-footer", "elementor-kit-",
					"/wp-content/uploads/elementor/css/post-"}, want...) {
					if !strings.Contains(body, w) && !strings.Contains(vis, w) {
						t.Errorf("%s: misses %q", name, w)
					}
				}
				for _, bad := range []string{"{{", "No menu is assigned", "Choose a menu", "elementor-theme-placeholder", "Lorem", "Post Title",
					"Add Your Heading Text Here", "Nothing Found"} {
					if strings.Contains(body, bad) && (bad != "{{" || strings.Contains(vis, bad)) {
						t.Errorf("%s: shows %q", name, bad)
					}
				}
				// The menus are on every page: header (primary) and footer.
				for _, mn := range k.Menus {
					if !strings.Contains(vis, mn.Items[len(mn.Items)-1].Title) {
						t.Errorf("%s: %s menu item %q not shown", name, mn.Location, mn.Items[len(mn.Items)-1].Title)
					}
				}
				return body
			}
			var home, contact *glibKBPage
			for i := range k.Pages {
				pg := &k.Pages[i]
				if pg.PostsPage {
					// Blog: the archive part with the posts loop and pagination (4 per page, 4–6 posts).
					body := check("blog", site.pageURL[pg.Slug], http.StatusOK, "elementor-widget-archive-posts", k.Posts[0].Title,
						"page-numbers", "elementor-location-archive")
					if strings.Contains(body, k.Posts[len(k.Posts)-1].Title) && len(k.Posts) > 4 {
						t.Errorf("blog: page 1 shows the oldest post")
					}
					continue
				}
				if pg.FrontPage {
					home = pg
				}
				if pg.Slug == "contact" {
					contact = pg
				}
				want := glibKBTexts(pg.Content, 4)
				body := check("page "+pg.Slug, site.pageURL[pg.Slug], http.StatusOK, append(want, "elementor-page-"+itoa64(site.pages[pg.Slug]))...)
				if strings.Contains(body, "elementor-location-single-page") {
					t.Errorf("page %s: the single-page part wraps a Full Width page", pg.Slug)
				}
			}
			if home == nil || contact == nil {
				t.Fatal("no home or contact page")
			}
			body := check("front page", base+"/", http.StatusOK, glibKBTexts(home.Content, 2)...)
			if !strings.Contains(body, k.Posts[0].Title) && strings.Contains(string(home.Content), `"posts"`) {
				t.Errorf("front page: latest post not listed")
			}
			check("contact form", site.pageURL["contact"], http.StatusOK, "elementor-form", `name="form_fields[email]"`)

			// A post: the single-post part with title, meta, featured image and content.
			check("single post", site.postURLs[0], http.StatusOK, "elementor-location-single-post", k.Posts[0].Title,
				"elementor-widget-theme-post-featured-image", "elementor-post-info", "wp-image-",
				strings.TrimSuffix(strings.SplitN(glibKBVisibleText(k.Posts[0].Content), ".", 2)[0], "."))
			// A plain page (not built): the single-page part.
			out := site.restDo(http.MethodPost, "/wp/v2/pages", map[string]any{"title": "Privacy Policy", "slug": "privacy-policy",
				"status": "publish", "content": "<!-- wp:paragraph -->\n<p>We keep only what we need to answer your messages.</p>\n<!-- /wp:paragraph -->"})
			check("plain page", out["link"].(string), http.StatusOK, "elementor-location-single-page", "Privacy Policy", "We keep only what we need")
			// A category archive (when the kit has categories), search and 404.
			cat := k.Posts[0].Categories[0]
			check("category", site.catURL[cat], http.StatusOK, "elementor-location-archive", cat, k.Posts[0].Title)
			word := strings.Fields(k.Posts[1].Title)[len(strings.Fields(k.Posts[1].Title))-1]
			check("search", base+"/?s="+url.QueryEscape(word), http.StatusOK, "elementor-location-search-results", k.Posts[1].Title,
				"elementor-search-form")
			check("search, nothing found", base+"/?s=zzqxv", http.StatusOK, "elementor-location-search-results")
			check("404", base+"/no-such-page-here/", http.StatusNotFound, "elementor-location-error-404", "elementor-search-form")

			// The kit stylesheet carries the kit's palette and fonts.
			_, css, _ := get(t, anon, base+"/wp-content/uploads/elementor/css/post-"+itoa64(int64(srv.settings.Int("elementor_active_kit")))+".css")
			for _, col := range k.Globals.Colors {
				if !strings.Contains(strings.ToUpper(css), strings.ToUpper(col["color"].(string))) {
					t.Errorf("kit CSS misses color %v", col["color"])
				}
			}
			if !strings.Contains(css, "main.site-main{max-width:none") {
				t.Errorf("kit CSS misses the Site Settings custom CSS")
			}
			for _, ty := range k.Globals.Typography[:1] {
				if !strings.Contains(css, ty["typography_font_family"].(string)) {
					t.Errorf("kit CSS misses font %v", ty["typography_font_family"])
				}
			}

			if hold != "" {
				urls := []string{base + "/", site.pageURL["blog"], site.postURLs[0], site.pageURL["about"], site.pageURL["contact"],
					base + "/?s=" + url.QueryEscape(word), base + "/no-such-page-here/"}
				var keys []string
				for key := range site.pageURL {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					urls = append(urls, site.pageURL[key])
				}
				if err := os.WriteFile(hold, []byte(slug+"\n"+strings.Join(urls, "\n")+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("holding %s at %s until %s is removed", slug, base, hold)
				for i := 0; i < 1800; i++ {
					if _, err := os.Stat(hold); err != nil {
						break
					}
					time.Sleep(time.Second)
				}
			}
		})
	}
}
