package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/builder"
)

// Golementor Studio library (docs/golementor-studio.md, section 3): ready sections and site kits,
// JSON files under web/gowebeditor/library/ (served with the editor assets at
// /wp-content/plugins/gowebeditor/assets/library/). The files are loaded from the web FS, validated
// and cached; invalid files are left out (and logged) so one broken file never hides the rest.
//
//   sections/<category>/<slug>.json + <slug>.webp   one section (upstream element JSON)
//   kits/<slug>/kit.json + screenshot               one starter site (globals, theme parts, pages,
//                                                   menus, sample posts) for the setup wizard
//   images/…                                        shared placeholder artwork
//
// Strings may hold tokens: {{library:images/x.webp}} (the asset URL), {{page:slug}} (the permalink
// of that kit page when a starter site is installed, else "#") and {{page_id:slug}} (its id, "0"
// when there is none; meant for display conditions like "include/singular/page/{{page_id:about}}").
//
// REST (namespace golementor/v1, editors only, cookie + X-WP-Nonce like elementor/v1):
//   GET /wp-json/golementor/v1/library/sections                    categories and items
//   GET /wp-json/golementor/v1/library/sections/<category>/<slug>  {"content":[…]} ready to insert
//   GET /wp-json/golementor/v1/library/kits                        the kits with screenshot URLs
// The sections also list in the classic template library's Blocks tab (source remote, type block).

const (
	gweLibDir       = "gowebeditor/library" // inside the web FS
	gweLibMaxFile   = 1 << 20               // a section or kit.json larger than this is refused
	gweLibSectionID = "gwe-section:"        // classic library template_id prefix
)

// gweLibCategories are the section categories, in the order the library shows them.
var gweLibCategories = []struct{ Slug, Title string }{
	{"hero", "Hero"}, {"features", "Features"}, {"about", "About"}, {"services", "Services"},
	{"cta", "Call to action"}, {"testimonials", "Testimonials"}, {"pricing", "Pricing"}, {"team", "Team"},
	{"faq", "FAQ"}, {"stats", "Stats"}, {"gallery", "Gallery"}, {"logos", "Logos"}, {"blog", "Blog"},
	{"contact", "Contact"}, {"newsletter", "Newsletter"}, {"content", "Content"},
}

func gweLibCategoryTitle(slug string) string {
	for _, c := range gweLibCategories {
		if c.Slug == slug {
			return c.Title
		}
	}
	return ""
}

func gweLibCategoryIndex(slug string) int {
	for i, c := range gweLibCategories {
		if c.Slug == slug {
			return i
		}
	}
	return len(gweLibCategories)
}

// gweLibGlobalIDs are the kit's system colors and fonts every section may reference.
var gweLibGlobalIDs = []string{"primary", "secondary", "text", "accent"}

var (
	gweLibSlugRe   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	gweLibTokenRe  = regexp.MustCompile(`\{\{([a-z_]+):([^{}]*)\}\}`)
	gweLibColorRe  = regexp.MustCompile(`^#([0-9A-Fa-f]{3}|[0-9A-Fa-f]{6}|[0-9A-Fa-f]{8})$`)
	gweLibGlobalRe = regexp.MustCompile(`^globals/(colors|typography)\?id=([A-Za-z0-9_-]+)$`)
)

// gweLibSection is one section template.
type gweLibSection struct {
	ID        string // <category>/<slug>
	Category  string
	Slug      string
	Title     string
	Tags      []string
	Thumbnail string          // path inside the library ("sections/hero/x.webp")
	Content   json.RawMessage // the element array as written
}

type gweLibSectionFile struct {
	Title     string          `json:"title"`
	Category  string          `json:"category"`
	Tags      []string        `json:"tags"`
	Thumbnail string          `json:"thumbnail"`
	Content   json.RawMessage `json:"content"`
}

// gweLibKit is kits/<slug>/kit.json.
type gweLibKit struct {
	Slug        string            `json:"-"`
	Title       string            `json:"title"`
	Category    string            `json:"category"`
	Description string            `json:"description"`
	Screenshot  string            `json:"screenshot"`
	Tags        []string          `json:"tags"`
	Site        gweLibKitSite     `json:"site"` // default title and tagline (used when the wizard field is empty)
	Globals     gweLibKitGlobals  `json:"globals"`
	Parts       []gweLibKitPart   `json:"parts"`
	Pages       []gweLibKitPage   `json:"pages"`
	Menus       []gweLibKitMenu   `json:"menus"`
	Posts       []gweLibKitPost   `json:"posts"`
	Settings    map[string]any    `json:"settings"` // extra kit (Site Settings) values, e.g. body background
	Options     map[string]string `json:"options"`  // blogname / blogdescription defaults (site wins)
}

type gweLibKitSite struct {
	Name    string `json:"name"`
	Tagline string `json:"tagline"`
}

// pageTemplate is the page template of a kit page: "template", else settings.template, else
// elementor_header_footer (header and footer parts around full-width content).
func (p *gweLibKitPage) pageTemplate() string {
	if p.Template != "" {
		return p.Template
	}
	if t, _ := p.Settings["template"].(string); t != "" {
		return t
	}
	return "elementor_header_footer"
}

type gweLibKitGlobals struct {
	Colors     []map[string]any `json:"colors"`
	Typography []map[string]any `json:"typography"`
	Settings   map[string]any   `json:"settings"` // kit style settings (body, links, headings, buttons, form fields, container width …)
}

type gweLibKitPart struct {
	Type       string          `json:"type"`
	Title      string          `json:"title"`
	Conditions []string        `json:"conditions"`
	Settings   map[string]any  `json:"settings"`
	Content    json.RawMessage `json:"content"`
}

type gweLibKitPage struct {
	Slug      string          `json:"slug"`
	Title     string          `json:"title"`
	FrontPage bool            `json:"front_page"`
	PostsPage bool            `json:"posts_page"`
	Optional  bool            `json:"optional"` // unchecked by default in the wizard
	Template  string          `json:"template"` // default: elementor_header_footer (theme header and footer, full-width content)
	Settings  map[string]any  `json:"settings"`
	Content   json.RawMessage `json:"content"`
}

type gweLibKitMenu struct {
	Location string              `json:"location"`
	Name     string              `json:"name"`
	Items    []gweLibKitMenuItem `json:"items"`
}

type gweLibKitMenuItem struct {
	Title    string              `json:"title"`
	Page     string              `json:"page"`
	URL      string              `json:"url"`
	Children []gweLibKitMenuItem `json:"children"`
}

type gweLibKitPost struct {
	Title      string   `json:"title"`
	Slug       string   `json:"slug"`
	Content    string   `json:"content"`
	Excerpt    string   `json:"excerpt"`
	Image      string   `json:"image"`
	Category   string   `json:"category"` // one category name (or use categories)
	Categories []string `json:"categories"`
}

// categories are the post's category names.
func (p *gweLibKitPost) categories() []string {
	out := append([]string{}, p.Categories...)
	if c := strings.TrimSpace(p.Category); c != "" {
		out = append([]string{c}, out...)
	}
	return out
}

// gweStudioLib is the loaded library.
type gweStudioLib struct {
	Sections []*gweLibSection
	Kits     []*gweLibKit
	Errors   []string // one line per invalid file (the file is left out)
}

func (l *gweStudioLib) section(id string) *gweLibSection {
	for _, s := range l.Sections {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (l *gweStudioLib) kit(slug string) *gweLibKit {
	for _, k := range l.Kits {
		if k.Slug == slug {
			return k
		}
	}
	return nil
}

func (k *gweLibKit) page(slug string) *gweLibKitPage {
	for i := range k.Pages {
		if k.Pages[i].Slug == slug {
			return &k.Pages[i]
		}
	}
	return nil
}

// gweLibFS is the library directory: web/gowebeditor/library, or the tests' fixture.
func (s *Server) gweLibFS() fs.FS {
	if s.gweLibOverride != nil {
		return s.gweLibOverride
	}
	sub, err := fs.Sub(s.web, gweLibDir)
	if err != nil {
		return nil
	}
	return sub
}

// gweLib returns the library, loaded once (on every call while serving the web files from disk,
// so edits show up during development).
func (s *Server) gweLib() *gweStudioLib {
	if l := s.gweLibCache.Load(); l != nil && s.cfg.WebDir == "" && s.gweLibOverride == nil {
		return l
	}
	l := gweLoadLibrary(s.gweLibFS())
	for _, e := range l.Errors {
		log.Printf("golementor library: %s", e)
	}
	s.gweLibCache.Store(l)
	return l
}

// gweLoadLibrary reads and validates every section and kit of a library directory.
func gweLoadLibrary(fsys fs.FS) *gweStudioLib {
	l := &gweStudioLib{}
	if fsys == nil {
		return l
	}
	files, _ := fs.Glob(fsys, "sections/*/*.json")
	sort.Strings(files)
	for _, f := range files {
		sec, problems := gweLibLoadSection(fsys, f)
		if len(problems) > 0 {
			l.Errors = append(l.Errors, f+": "+strings.Join(problems, "; "))
			continue
		}
		l.Sections = append(l.Sections, sec)
	}
	// Files deeper than sections/<category>/ are never listed: say so instead of ignoring them.
	_ = fs.WalkDir(fsys, "sections", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".json") && strings.Count(p, "/") != 2 {
			l.Errors = append(l.Errors, p+": sections must be sections/<category>/<slug>.json")
		}
		return nil
	})
	sort.SliceStable(l.Sections, func(i, j int) bool {
		a, b := l.Sections[i], l.Sections[j]
		if ca, cb := gweLibCategoryIndex(a.Category), gweLibCategoryIndex(b.Category); ca != cb {
			return ca < cb
		}
		return a.Slug < b.Slug
	})
	kits, _ := fs.Glob(fsys, "kits/*/kit.json")
	sort.Strings(kits)
	for _, f := range kits {
		k, problems := gweLibLoadKit(fsys, f)
		if len(problems) > 0 {
			l.Errors = append(l.Errors, f+": "+strings.Join(problems, "; "))
			continue
		}
		l.Kits = append(l.Kits, k)
	}
	return l
}

// gweLibRead reads a JSON file strictly (unknown keys are errors: a typo must not pass silently).
func gweLibRead(fsys fs.FS, name string, v any) error {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return err
	}
	if len(b) > gweLibMaxFile {
		return fmt.Errorf("larger than %d KB", gweLibMaxFile>>10)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON: %v", err)
	}
	if dec.More() {
		return errors.New("invalid JSON: data after the object")
	}
	return nil
}

func gweLibExists(fsys fs.FS, name string) bool {
	st, err := fs.Stat(fsys, name)
	return err == nil && !st.IsDir()
}

func gweLibLoadSection(fsys fs.FS, name string) (*gweLibSection, []string) {
	var problems []string
	bad := func(f string, a ...any) { problems = append(problems, fmt.Sprintf(f, a...)) }
	dir := path.Dir(name)
	category := path.Base(dir)
	slug := strings.TrimSuffix(path.Base(name), ".json")
	var f gweLibSectionFile
	if err := gweLibRead(fsys, name, &f); err != nil {
		return nil, []string{err.Error()}
	}
	if !gweLibSlugRe.MatchString(slug) {
		bad("file name %q is not a slug (a-z, 0-9, -)", slug)
	}
	if gweLibCategoryTitle(category) == "" {
		bad("unknown category directory %q", category)
	}
	if f.Category != category {
		bad("category %q does not match its directory %q", f.Category, category)
	}
	if strings.TrimSpace(f.Title) == "" {
		bad("title is missing")
	}
	for _, t := range f.Tags {
		if strings.TrimSpace(t) == "" {
			bad("empty tag")
		}
	}
	if f.Thumbnail != slug+".webp" {
		bad("thumbnail must be %q", slug+".webp")
	} else if !gweLibExists(fsys, dir+"/"+f.Thumbnail) {
		bad("thumbnail %s is missing", f.Thumbnail)
	}
	problems = append(problems, gweLibCheckTree(fsys, f.Content, "content", gweLibGlobalIDs, gweLibGlobalIDs, nil)...)
	if len(problems) > 0 {
		return nil, problems
	}
	return &gweLibSection{ID: category + "/" + slug, Category: category, Slug: slug, Title: strings.TrimSpace(f.Title), Tags: f.Tags,
		Thumbnail: dir + "/" + f.Thumbnail, Content: f.Content}, nil
}

// gweLibCheckTree validates an element array: it parses with the renderer's loader, elements are
// containers (or sections/columns) and widgets the renderer knows, __globals__ name known kit
// globals and the tokens are well-formed. pages limits {{page:…}} to those slugs (nil = any).
func gweLibCheckTree(fsys fs.FS, raw json.RawMessage, where string, colors, fonts []string, pages map[string]bool) []string {
	var problems []string
	bad := func(f string, a ...any) { problems = append(problems, fmt.Sprintf(where+": "+f, a...)) }
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return []string{where + ": must be a JSON array of elements"}
	}
	els, err := builder.ParseElements(trimmed)
	if err != nil {
		return []string{where + ": " + err.Error()}
	}
	if len(els) == 0 {
		bad("is empty")
	}
	known := func(list []string, id string) bool {
		for _, x := range list {
			if x == id {
				return true
			}
		}
		return false
	}
	var walk func(list []*builder.Element, depth int)
	walk = func(list []*builder.Element, depth int) {
		for _, el := range list {
			if el == nil {
				bad("null element")
				continue
			}
			switch el.ElType {
			case "container", "section", "column":
				if el.WidgetType != "" {
					bad("%s with a widgetType", el.ElType)
				}
			case "widget":
				if !builder.WidgetKnown(el.WidgetType) {
					bad("unknown widget %q", el.WidgetType)
				}
			default:
				bad("unknown elType %q", el.ElType)
			}
			if depth == 0 && el.ElType == "widget" {
				bad("top-level elements must be containers (found widget %q)", el.WidgetType)
			}
			if g, ok := el.Settings["__globals__"].(map[string]any); ok {
				keys := make([]string, 0, len(g))
				for k := range g {
					keys = append(keys, k)
				}
				sort.Strings(keys) // stable messages: map order would shuffle them
				for _, k := range keys {
					ref, _ := g[k].(string)
					if ref == "" {
						continue
					}
					m := gweLibGlobalRe.FindStringSubmatch(ref)
					switch {
					case m == nil:
						bad("__globals__.%s: malformed reference %q", k, ref)
					case m[1] == "colors" && !known(colors, m[2]):
						bad("__globals__.%s: unknown global color %q", k, m[2])
					case m[1] == "typography" && !known(fonts, m[2]):
						bad("__globals__.%s: unknown global font %q", k, m[2])
					}
				}
			}
			walk(el.Elements, depth+1)
		}
	}
	walk(els, 0)
	problems = append(problems, gweLibCheckTokens(fsys, string(trimmed), where, pages)...)
	return problems
}

// gweLibCheckTokens checks the {{…}} tokens of a text.
func gweLibCheckTokens(fsys fs.FS, text, where string, pages map[string]bool) []string {
	var problems []string
	for _, m := range gweLibTokenRe.FindAllStringSubmatch(text, -1) {
		switch m[1] {
		case "library":
			if !gweLibValidAsset(m[2]) {
				problems = append(problems, fmt.Sprintf("%s: token %s: not a library file path", where, m[0]))
			} else if !gweLibExists(fsys, m[2]) {
				problems = append(problems, fmt.Sprintf("%s: token %s: file is missing", where, m[0]))
			}
		case "page", "page_id":
			if !gweLibSlugRe.MatchString(m[2]) {
				problems = append(problems, fmt.Sprintf("%s: token %s: not a page slug", where, m[0]))
			} else if pages != nil && !pages[m[2]] {
				problems = append(problems, fmt.Sprintf("%s: token %s: the kit has no such page", where, m[0]))
			}
		default:
			problems = append(problems, fmt.Sprintf("%s: unknown token %s", where, m[0]))
		}
	}
	return problems
}

// gweLibValidAsset accepts a clean relative path inside the library (images/x.webp).
func gweLibValidAsset(p string) bool {
	return p != "" && fs.ValidPath(p) && !strings.HasSuffix(p, ".json") && path.Clean(p) == p
}

func gweLibLoadKit(fsys fs.FS, name string) (*gweLibKit, []string) {
	var problems []string
	bad := func(f string, a ...any) { problems = append(problems, fmt.Sprintf(f, a...)) }
	dir := path.Dir(name)
	k := &gweLibKit{Slug: path.Base(dir)}
	if err := gweLibRead(fsys, name, k); err != nil {
		return nil, []string{err.Error()}
	}
	if !gweLibSlugRe.MatchString(k.Slug) {
		bad("directory %q is not a slug", k.Slug)
	}
	if strings.TrimSpace(k.Title) == "" {
		bad("title is missing")
	}
	if strings.TrimSpace(k.Category) == "" {
		bad("category is missing")
	}
	if k.Screenshot == "" || strings.Contains(k.Screenshot, "/") || !gweLibExists(fsys, dir+"/"+k.Screenshot) {
		bad("screenshot %q is missing (a file next to kit.json)", k.Screenshot)
	}
	// Globals: the four system colors and fonts, plus any custom ones.
	ids := func(list []map[string]any, what string, check func(m map[string]any) string) []string {
		var out []string
		seen := map[string]bool{}
		for i, m := range list {
			id, _ := m["_id"].(string)
			if !gweLibSlugRe.MatchString(strings.ReplaceAll(id, "_", "-")) {
				bad("globals.%s[%d]: _id %q is not a valid id", what, i, id)
				continue
			}
			if seen[id] {
				bad("globals.%s: duplicate _id %q", what, id)
			}
			seen[id] = true
			if t, _ := m["title"].(string); strings.TrimSpace(t) == "" {
				bad("globals.%s[%d]: title is missing", what, i)
			}
			if msg := check(m); msg != "" {
				bad("globals.%s[%d]: %s", what, i, msg)
			}
			out = append(out, id)
		}
		for _, want := range gweLibGlobalIDs {
			if !seen[want] {
				bad("globals.%s: %q is missing", what, want)
			}
		}
		return out
	}
	colors := ids(k.Globals.Colors, "colors", func(m map[string]any) string {
		if c, _ := m["color"].(string); !gweLibColorRe.MatchString(c) {
			return fmt.Sprintf("color %q is not a hex color", m["color"])
		}
		return ""
	})
	fonts := ids(k.Globals.Typography, "typography", func(m map[string]any) string {
		fam, _ := m["typography_font_family"].(string)
		if strings.TrimSpace(fam) == "" {
			return "typography_font_family is missing"
		}
		if gweGoogleFontType(fam) == "" && !gweSetupSystemFonts[fam] {
			return fmt.Sprintf("font %q is neither a Google font nor a system font", fam)
		}
		return ""
	})
	pages := map[string]bool{}
	front, posts := 0, 0
	for i, p := range k.Pages {
		where := fmt.Sprintf("pages[%d]", i)
		if !gweLibSlugRe.MatchString(p.Slug) {
			bad("%s: slug %q is not a slug", where, p.Slug)
		}
		if pages[p.Slug] {
			bad("%s: duplicate slug %q", where, p.Slug)
		}
		pages[p.Slug] = true
		if strings.TrimSpace(p.Title) == "" {
			bad("%s: title is missing", where)
		}
		if p.FrontPage {
			front++
		}
		if p.PostsPage {
			posts++
			if p.FrontPage {
				bad("%s: a page cannot be both the front page and the posts page", where)
			}
		}
		switch p.pageTemplate() {
		case "default", "elementor_header_footer", "elementor_canvas":
		default:
			bad("%s: unknown template %q", where, p.pageTemplate())
		}
	}
	if front > 1 || posts > 1 {
		bad("at most one front_page and one posts_page")
	}
	for i, p := range k.Pages {
		where := fmt.Sprintf("pages[%d] (%s)", i, p.Slug)
		if p.PostsPage {
			if len(bytes.TrimSpace(p.Content)) > 0 && string(bytes.TrimSpace(p.Content)) != "[]" {
				bad("%s: the posts page shows the archive part; its content would never show", where)
			}
			continue
		}
		problems = append(problems, gweLibCheckTree(fsys, p.Content, where, colors, fonts, pages)...)
	}
	types := map[string]int{}
	for i, p := range k.Parts {
		where := fmt.Sprintf("parts[%d] (%s)", i, p.Type)
		if gwePartLabel(p.Type) == "" {
			bad("%s: unknown part type %q", where, p.Type)
		}
		types[p.Type]++
		if len(p.Conditions) == 0 {
			bad("%s: conditions are missing", where)
		}
		for _, cd := range p.Conditions {
			if !gweSetupValidCondition(cd, pages) {
				bad("%s: invalid condition %q", where, cd)
			}
		}
		problems = append(problems, gweLibCheckTree(fsys, p.Content, where, colors, fonts, pages)...)
	}
	for _, t := range []string{"header", "footer"} {
		if types[t] == 0 {
			bad("parts: a %s is required", t)
		}
	}
	for i, m := range k.Menus {
		where := fmt.Sprintf("menus[%d]", i)
		ok := false
		for _, l := range helloMenuLocations {
			ok = ok || l.Slug == m.Location
		}
		if !ok {
			bad("%s: location %q is not primary or footer", where, m.Location)
		}
		if strings.TrimSpace(m.Name) == "" {
			bad("%s: name is missing", where)
		}
		var check func(items []gweLibKitMenuItem, where string)
		check = func(items []gweLibKitMenuItem, where string) {
			for j, it := range items {
				w := fmt.Sprintf("%s.items[%d]", where, j)
				switch {
				case it.Page != "" && it.URL != "":
					bad("%s: give page or url, not both", w)
				case it.Page != "" && !pages[it.Page]:
					bad("%s: the kit has no page %q", w, it.Page)
				case it.Page == "" && it.URL == "":
					bad("%s: page or url is required", w)
				case it.Page == "" && strings.TrimSpace(it.Title) == "":
					bad("%s: a url item needs a title", w)
				}
				problems = append(problems, gweLibCheckTokens(fsys, it.URL, w, pages)...)
				check(it.Children, w)
			}
		}
		check(m.Items, where)
	}
	for i, p := range k.Posts {
		where := fmt.Sprintf("posts[%d]", i)
		if strings.TrimSpace(p.Title) == "" {
			bad("%s: title is missing", where)
		}
		if p.Slug != "" && !gweLibSlugRe.MatchString(p.Slug) {
			bad("%s: slug %q is not a slug", where, p.Slug)
		}
		if p.Image != "" {
			m := gweLibTokenRe.FindStringSubmatch(p.Image)
			if m == nil || m[0] != p.Image || m[1] != "library" {
				bad("%s: image must be a {{library:images/…}} token", where)
			} else if ext := strings.ToLower(path.Ext(m[2])); ext != ".webp" && ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
				bad("%s: image %s is not a webp, jpeg or png file", where, m[2])
			}
		}
		problems = append(problems, gweLibCheckTokens(fsys, p.Content+p.Excerpt+p.Image, where, pages)...)
	}
	for key, v := range k.Options {
		if key != "blogname" && key != "blogdescription" {
			bad("options: %q is not blogname or blogdescription", key)
		}
		problems = append(problems, gweLibCheckTokens(fsys, v, "options."+key, pages)...)
	}
	for key := range k.Settings {
		if gweSetupKitReserved[key] {
			bad("settings: %q is set through globals or the wizard", key)
		}
	}
	for key := range k.Globals.Settings {
		if gweSetupKitReserved[key] {
			bad("globals.settings: %q is set through globals or the wizard", key)
		}
	}
	for _, t := range k.Tags {
		if strings.TrimSpace(t) == "" {
			bad("tags: empty tag")
		}
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return k, nil
}

// --- tokens and fresh ids ----------------------------------------------------------------------

// gweLibAssetURL is the URL of a library file.
func (s *Server) gweLibAssetURL(ctx context.Context, rel string) string {
	return s.links(ctx).HomeURL(gweAssetsPath + "library/" + rel)
}

// gweLibReplace swaps the tokens of a text; JSON-escaped when inJSON (the text is raw JSON).
func gweLibReplace(text string, inJSON bool, resolve func(kind, arg string) string) string {
	return gweLibTokenRe.ReplaceAllStringFunc(text, func(tok string) string {
		m := gweLibTokenRe.FindStringSubmatch(tok)
		v := resolve(m[1], m[2])
		if inJSON {
			b, _ := json.Marshal(v)
			v = string(b[1 : len(b)-1])
		}
		return v
	})
}

// gweLibFreshTree gives every element of a raw element array a fresh id, keeping each object's key
// order (it shows in the rendered markup, so the tree is never round-tripped through a map).
func gweLibFreshTree(raw json.RawMessage) (json.RawMessage, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(list))
	for _, item := range list {
		obj, err := gweOrderedRaw(item)
		if err != nil {
			return nil, err
		}
		id, _ := json.Marshal(gweNewID())
		found := false
		for i := range obj {
			switch obj[i].K {
			case "id":
				obj[i].V, found = id, true
			case "elements":
				kids, err := gweLibFreshTree(obj[i].V)
				if err != nil {
					return nil, err
				}
				obj[i].V = kids
			}
		}
		if !found {
			obj = append(gweRawObject{{K: "id", V: id}}, obj...)
		}
		if !gweRawHas(obj, "elements") {
			obj = append(obj, struct {
				K string
				V json.RawMessage
			}{"elements", json.RawMessage("[]")})
		}
		if !gweRawHas(obj, "settings") {
			obj = obj.insertAfter("elType", "settings", json.RawMessage("{}"))
		}
		b, _ := obj.MarshalJSON()
		out = append(out, b)
	}
	b, err := json.Marshal(out)
	return b, err
}

func gweRawHas(o gweRawObject, k string) bool {
	for _, kv := range o {
		if kv.K == k {
			return true
		}
	}
	return false
}

// gweLibInstalledPages maps the slugs of the installed starter site's pages to their ids.
func (s *Server) gweLibInstalledPages(ctx context.Context) map[string]int64 {
	out := map[string]int64{}
	rows, err := s.store.Pool.Query(ctx, `SELECT id, meta->>'`+gweStarterRef+`' FROM posts WHERE type = 'page' AND status = 'publish'
		AND coalesce(meta->>'`+gweStarterMeta+`','') <> '' ORDER BY id`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var ref *string
		if rows.Scan(&id, &ref) == nil && ref != nil && *ref != "" {
			if _, ok := out[*ref]; !ok {
				out[*ref] = id
			}
		}
	}
	return out
}

// gweLibSectionContent is a section ready to insert: tokens replaced, fresh element ids.
func (s *Server) gweLibSectionContent(ctx context.Context, sec *gweLibSection) ([]any, error) {
	pages := s.gweLibInstalledPages(ctx)
	lb := s.links(ctx)
	text := gweLibReplace(string(sec.Content), true, func(kind, arg string) string {
		switch kind {
		case "library":
			return s.gweLibAssetURL(ctx, arg)
		case "page":
			if id := pages[arg]; id > 0 {
				if p, err := s.store.GetPost(ctx, id); err == nil {
					return lb.Permalink(p)
				}
			}
			return "#"
		case "page_id":
			return itoa64(pages[arg])
		}
		return ""
	})
	fresh, err := gweLibFreshTree(json.RawMessage(text))
	if err != nil {
		return nil, err
	}
	var tree []any
	if err := json.Unmarshal(fresh, &tree); err != nil {
		return nil, err
	}
	return tree, nil
}

// --- REST --------------------------------------------------------------------------------------

func (s *Server) registerGolementorLibraryREST(editor func(restHandler) restHandler) {
	const ns = "golementor/v1"
	api := s.rest
	api.register(ns, "/golementor/v1/library/sections", map[string]restHandler{http.MethodGet: editor(func(c *gin.Context, _ *restRequest) {
		c.JSON(http.StatusOK, s.gweLibSectionsPayload(c))
	})}, nil)
	api.register(ns, "/golementor/v1/library/sections/(?P<category>[a-z0-9-]+)/(?P<slug>[a-z0-9-]+)", map[string]restHandler{http.MethodGet: editor(func(c *gin.Context, r *restRequest) {
		sec := s.gweLib().section(r.Params["category"] + "/" + r.Params["slug"])
		if sec == nil {
			restErr(c, http.StatusNotFound, "rest_not_found", "Section not found.")
			return
		}
		tree, err := s.gweLibSectionContent(c, sec)
		if err != nil {
			restErr(c, http.StatusInternalServerError, "rest_error", err.Error())
			return
		}
		c.JSON(http.StatusOK, map[string]any{"content": tree})
	})}, nil)
	api.register(ns, "/golementor/v1/library/kits", map[string]restHandler{http.MethodGet: editor(func(c *gin.Context, _ *restRequest) {
		c.JSON(http.StatusOK, map[string]any{"kits": s.gweLibKitsPayload(c)})
	})}, nil)
}

func (s *Server) gweLibSectionsPayload(ctx context.Context) map[string]any {
	lib := s.gweLib()
	counts := map[string]int{}
	items := make([]map[string]any, 0, len(lib.Sections))
	for _, sec := range lib.Sections {
		counts[sec.Category]++
		tags := sec.Tags
		if tags == nil {
			tags = []string{}
		}
		items = append(items, map[string]any{"id": sec.ID, "title": sec.Title, "category": sec.Category, "tags": tags,
			"thumbnail": s.gweLibAssetURL(ctx, sec.Thumbnail)})
	}
	cats := []map[string]any{}
	for _, c := range gweLibCategories {
		if counts[c.Slug] > 0 {
			cats = append(cats, map[string]any{"slug": c.Slug, "title": c.Title, "count": counts[c.Slug]})
		}
	}
	return map[string]any{"categories": cats, "items": items}
}

func (s *Server) gweLibKitsPayload(ctx context.Context) []map[string]any {
	lib := s.gweLib()
	out := make([]map[string]any, 0, len(lib.Kits))
	for _, k := range lib.Kits {
		pages := []map[string]any{}
		for _, p := range k.Pages {
			pages = append(pages, map[string]any{"slug": p.Slug, "title": p.Title, "front_page": p.FrontPage, "posts_page": p.PostsPage, "optional": p.Optional})
		}
		parts := []string{}
		for _, p := range k.Parts {
			parts = append(parts, p.Type)
		}
		colors := []map[string]any{}
		for _, m := range k.Globals.Colors {
			colors = append(colors, map[string]any{"_id": m["_id"], "title": m["title"], "color": m["color"]})
		}
		fonts := []map[string]any{}
		for _, m := range k.Globals.Typography {
			fonts = append(fonts, map[string]any{"_id": m["_id"], "title": m["title"], "family": m["typography_font_family"]})
		}
		tags := append([]string{}, k.Tags...)
		out = append(out, map[string]any{"slug": k.Slug, "title": k.Title, "category": k.Category, "description": k.Description, "tags": tags,
			"screenshot": s.gweLibAssetURL(ctx, "kits/"+k.Slug+"/"+k.Screenshot), "pages": pages, "parts": parts,
			"colors": colors, "fonts": fonts, "posts": len(k.Posts)})
	}
	return out
}

// --- classic template library (Blocks tab) -----------------------------------------------------

// gweLibBlockItems are the sections as remote "block" templates of the classic library modal.
func (s *Server) gweLibBlockItems(c *gin.Context) []map[string]any {
	lib := s.gweLib()
	out := make([]map[string]any, 0, len(lib.Sections))
	for i, sec := range lib.Sections {
		tags := append([]string{}, sec.Tags...)
		out = append(out, map[string]any{
			"template_id": gweLibSectionID + sec.ID, "source": "remote", "type": "block", "subtype": gweLibCategoryTitle(sec.Category),
			"title": sec.Title, "thumbnail": s.gweLibAssetURL(c, sec.Thumbnail), "date": 1792000000 - i, "human_date": "",
			"author": "Pursae Web Builder", "tags": tags, "isPro": false, "accessLevel": 0, "accessTier": "free",
			"popularityIndex": len(lib.Sections) - i, "trendIndex": len(lib.Sections) - i, "hasPageSettings": false, "url": "", "favorite": false,
		})
	}
	return out
}

// gweLibBlocksTabStyle shows the library's Blocks tab (gowebeditor-editor.css hides it while there
// are no sections to list).
const gweLibBlocksTabStyle = `<style id="golementor-library-blocks">#elementor-template-library-header-menu .elementor-template-library-menu-item[data-tab="templates/blocks"]{display:inline-block!important}</style>
`

// gweLibBlockConfig is the library config of the Blocks tab: its category filter.
func (s *Server) gweLibBlockConfig() map[string]any {
	lib := s.gweLib()
	seen := map[string]bool{}
	cats := []string{}
	for _, sec := range lib.Sections {
		if t := gweLibCategoryTitle(sec.Category); !seen[t] {
			seen[t] = true
			cats = append(cats, t)
		}
	}
	if len(cats) == 0 {
		return map[string]any{}
	}
	return map[string]any{"block": map[string]any{"categories": cats}}
}
