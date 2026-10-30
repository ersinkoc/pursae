package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"wpgo/internal/store"
)

// One-click demo content of theme packages (docs/theme-package.md, "Demo content"): theme.json's
// "demo" names a JSON file (demo/demo.json) with media, categories, tags, pages, posts, menus,
// block widgets, Customizer values and a few options. Importing creates the items through the same
// store calls as the WXR importer (and the same media pipeline, so image sizes are generated);
// every created post, page, attachment and term carries the meta _wpgo_demo = <theme slug>, so a
// second import skips what exists and Remove Demo Content deletes exactly the imported items. The
// menus and widgets it created, the widgets it moved aside and the values of the options, reading
// settings and theme mods it changed are kept in the wpgo_demo_<slug> option and restored on removal.

const (
	demoMetaKey     = "_wpgo_demo"     // the theme slug, on every imported post, page, attachment and term
	demoMetaRef     = "_wpgo_demo_ref" // the item's id/slug inside demo.json
	demoMaxMedia    = 24
	demoMaxImage    = 1 << 20
	demoMaxPages    = 50
	demoMaxPosts    = 100
	demoMaxMenus    = 10
	demoMaxComments = 20
	demoTimeLimit   = 2 * time.Minute
)

// themeDemo is demo/demo.json.
type themeDemo struct {
	Version    int                 `json:"version"`
	Options    map[string]any      `json:"options"`
	Categories []demoTerm          `json:"categories"`
	Tags       []demoTerm          `json:"tags"`
	Media      []demoMedia         `json:"media"`
	Pages      []demoPost          `json:"pages"`
	Posts      []demoPost          `json:"posts"`
	Menus      []demoMenu          `json:"menus"`
	Widgets    map[string][]string `json:"widgets"`
	Customizer map[string]any      `json:"customizer"`
	Store      json.RawMessage     `json:"goocommerce"` // Goocommerce: products, categories, coupons (goo_demo.go)
}

type demoTerm struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Parent      string `json:"parent"` // categories only: the parent's slug
}

type demoMedia struct {
	ID      string `json:"id"`
	File    string `json:"file"`
	Title   string `json:"title"`
	Alt     string `json:"alt"`
	Caption string `json:"caption"`
}

// demoPost is a page or a post; Template, Parent, FrontPage, PostsPage and MenuOrder are for pages,
// Date, Categories, Tags, Format, Sticky and Comments for posts.
type demoPost struct {
	Slug          string        `json:"slug"`
	Title         string        `json:"title"`
	Content       string        `json:"content"`
	Excerpt       string        `json:"excerpt"`
	FeaturedMedia string        `json:"featured_media"`
	Template      string        `json:"template"`
	Parent        string        `json:"parent"`
	FrontPage     bool          `json:"front_page"`
	PostsPage     bool          `json:"posts_page"`
	MenuOrder     int           `json:"menu_order"`
	Date          string        `json:"date"`
	Categories    []string      `json:"categories"`
	Tags          []string      `json:"tags"`
	Format        string        `json:"format"`
	Sticky        bool          `json:"sticky"`
	Comments      []demoComment `json:"comments"`
}

type demoComment struct {
	Author  string `json:"author"`
	Email   string `json:"email"`
	URL     string `json:"url"`
	Content string `json:"content"`
	Date    string `json:"date"`
}

type demoMenu struct {
	Name     string         `json:"name"`
	Location string         `json:"location"`
	Items    []demoMenuItem `json:"items"`
}

type demoMenuItem struct {
	Title    string         `json:"title"`
	Page     string         `json:"page"`
	Post     string         `json:"post"`
	Category string         `json:"category"`
	URL      string         `json:"url"`
	Children []demoMenuItem `json:"children"`
}

// demoOptions are the options demo.json may set.
var demoOptions = map[string]bool{"blogname": true, "blogdescription": true, "posts_per_page": true, "date_format": true}

var (
	demoTokenRe  = regexp.MustCompile(`\{\{(media|media_id|page|post|category|tag):([A-Za-z0-9_-]+)\}\}`)
	demoImageExt = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}
	demoTplRe    = regexp.MustCompile(`^[a-z0-9]+([_-][a-z0-9]+)*(\.html)?$`)
	demoFormats  = map[string]bool{"standard": true}
)

func init() {
	for _, f := range postFormats {
		demoFormats[f] = true
	}
}

// demoError lists every problem of a demo.json, like manifestError.
type demoError struct {
	file     string
	problems []string
}

func (e *demoError) Error() string { return e.file + ": " + strings.Join(e.problems, "; ") }

// parseDemoDate reads a demo date ("2026-09-12T09:30:00", "2026-09-12 09:30:00", RFC 3339 or a bare day).
func parseDemoDate(v string, loc *time.Location) (time.Time, error) {
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, v, loc); err == nil {
			return t, nil
		}
	}
	return time.Parse(time.RFC3339, v)
}

// parseThemeDemo reads and validates the demo file of a package (fsys is the package directory).
func parseThemeDemo(fsys fs.FS, m *themeManifest) (*themeDemo, error) {
	b, err := fs.ReadFile(fsys, m.Demo)
	if err != nil {
		return nil, fmt.Errorf("%s: the demo content file is missing", m.Demo)
	}
	var d themeDemo
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("%s: %v", m.Demo, err)
	}
	var problems []string
	bad := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if d.Version != 1 {
		bad("version must be 1")
	}
	for k, v := range d.Options {
		switch {
		case !demoOptions[k]:
			bad("options.%s may not be set (only blogname, blogdescription, posts_per_page, date_format)", k)
		case k == "posts_per_page":
			if n, ok := v.(float64); !ok || n != float64(int(n)) || n < 1 || n > 100 {
				bad("options.posts_per_page must be an integer from 1 to 100")
			}
		default:
			if _, ok := v.(string); !ok {
				bad("options.%s must be a string", k)
			}
		}
	}

	categories, tags := map[string]bool{}, map[string]bool{}
	for i, c := range d.Categories {
		if !themeSlugRe.MatchString(c.Slug) {
			bad("categories[%d].slug %q must match ^[a-z0-9]+(-[a-z0-9]+)*$", i, c.Slug)
		} else if categories[c.Slug] {
			bad("categories[%d].slug %q is listed twice", i, c.Slug)
		}
		categories[c.Slug] = true
		if strings.TrimSpace(c.Name) == "" {
			bad("categories[%d].name is required", i)
		}
	}
	catParents := map[string]string{}
	for i, c := range d.Categories {
		if c.Parent != "" && (!categories[c.Parent] || c.Parent == c.Slug) {
			bad("categories[%d].parent %q is not another category of the demo", i, c.Parent)
		} else if c.Parent != "" {
			catParents[c.Slug] = c.Parent
		}
	}
	for _, slug := range demoCycles(catParents) {
		bad("categories: the parents of %q form a cycle", slug)
	}
	for i, t := range d.Tags {
		if !themeSlugRe.MatchString(t.Slug) {
			bad("tags[%d].slug %q must match ^[a-z0-9]+(-[a-z0-9]+)*$", i, t.Slug)
		} else if tags[t.Slug] {
			bad("tags[%d].slug %q is listed twice", i, t.Slug)
		}
		tags[t.Slug] = true
		if strings.TrimSpace(t.Name) == "" {
			bad("tags[%d].name is required", i)
		}
		if t.Parent != "" {
			bad("tags[%d]: tags have no parent", i)
		}
	}

	mediaIDs := map[string]bool{}
	if len(d.Media) > demoMaxMedia {
		bad("media: at most %d images", demoMaxMedia)
	}
	for i, md := range d.Media {
		if !themeHandleRe.MatchString(md.ID) {
			bad("media[%d].id %q must match ^[a-z0-9]+([_-][a-z0-9]+)*$", i, md.ID)
		} else if mediaIDs[md.ID] {
			bad("media[%d].id %q is listed twice", i, md.ID)
		}
		mediaIDs[md.ID] = true
		if !strings.HasPrefix(md.File, "demo/images/") || !fs.ValidPath(md.File) || !demoImageExt[strings.ToLower(path.Ext(md.File))] {
			bad("media[%d].file %q must be a .jpg, .png or .webp file under demo/images/", i, md.File)
			continue
		}
		st, err := fs.Stat(fsys, md.File)
		switch {
		case err != nil || st.IsDir():
			bad("media[%d].file %q is missing from the package", i, md.File)
		case st.Size() > demoMaxImage:
			bad("media[%d].file %q is larger than 1 MiB", i, md.File)
		case st.Size() == 0:
			bad("media[%d].file %q is empty", i, md.File)
		}
	}
	media := func(at, ref string) {
		if ref != "" && !mediaIDs[ref] {
			bad("%s: %q is not a media id of the demo", at, ref)
		}
	}

	pages, posts := map[string]bool{}, map[string]bool{}
	if len(d.Pages) > demoMaxPages {
		bad("pages: at most %d", demoMaxPages)
	}
	if len(d.Posts) > demoMaxPosts {
		bad("posts: at most %d", demoMaxPosts)
	}
	front, blog := 0, 0
	for i, p := range d.Pages {
		at := fmt.Sprintf("pages[%d]", i)
		if !themeSlugRe.MatchString(p.Slug) {
			bad("%s.slug %q must match ^[a-z0-9]+(-[a-z0-9]+)*$", at, p.Slug)
		} else if pages[p.Slug] {
			bad("%s.slug %q is listed twice", at, p.Slug)
		}
		pages[p.Slug] = true
		if strings.TrimSpace(p.Title) == "" {
			bad("%s.title is required", at)
		}
		media(at+".featured_media", p.FeaturedMedia)
		if p.Template != "" {
			name := strings.TrimSuffix(p.Template, ".html")
			if !demoTplRe.MatchString(p.Template) {
				bad("%s.template %q must be the name of a file in templates/", at, p.Template)
			} else if _, err := fs.Stat(fsys, "templates/"+name+".html"); err != nil {
				bad("%s.template: templates/%s.html is missing", at, name)
			}
		}
		if p.FrontPage {
			front++
		}
		if p.PostsPage {
			blog++
		}
		if p.FrontPage && p.PostsPage {
			bad("%s: a page cannot be both the front page and the posts page", at)
		}
		if p.Date != "" || len(p.Categories) > 0 || len(p.Tags) > 0 || p.Sticky || len(p.Comments) > 0 || (p.Format != "" && p.Format != "standard") {
			bad("%s: date, categories, tags, format, sticky and comments are for posts", at)
		}
	}
	if front > 1 || blog > 1 {
		bad("pages: only one front_page and one posts_page")
	}
	parents := map[string]string{}
	for i, p := range d.Pages {
		if p.Parent == "" {
			continue
		}
		if !pages[p.Parent] || p.Parent == p.Slug {
			bad("pages[%d].parent %q is not another page of the demo", i, p.Parent)
			continue
		}
		parents[p.Slug] = p.Parent
	}
	for _, slug := range demoCycles(parents) {
		bad("pages: the parents of %q form a cycle", slug)
	}
	loc := time.UTC
	for i, p := range d.Posts {
		at := fmt.Sprintf("posts[%d]", i)
		if !themeSlugRe.MatchString(p.Slug) {
			bad("%s.slug %q must match ^[a-z0-9]+(-[a-z0-9]+)*$", at, p.Slug)
		} else if posts[p.Slug] {
			bad("%s.slug %q is listed twice", at, p.Slug)
		}
		posts[p.Slug] = true
		if strings.TrimSpace(p.Title) == "" {
			bad("%s.title is required", at)
		}
		media(at+".featured_media", p.FeaturedMedia)
		if p.Date != "" {
			if t, err := parseDemoDate(p.Date, loc); err != nil {
				bad("%s.date %q is not a date like 2026-09-12T09:30:00", at, p.Date)
			} else if t.After(time.Now()) {
				bad("%s.date %q is in the future (the post would be scheduled)", at, p.Date)
			}
		}
		for _, c := range p.Categories {
			if !categories[c] {
				bad("%s.categories: %q is not a category of the demo", at, c)
			}
		}
		for _, tg := range p.Tags {
			if !tags[tg] {
				bad("%s.tags: %q is not a tag of the demo", at, tg)
			}
		}
		if p.Format != "" && !demoFormats[p.Format] {
			bad("%s.format %q is not a post format", at, p.Format)
		}
		if p.Template != "" || p.Parent != "" || p.FrontPage || p.PostsPage {
			bad("%s: template, parent, front_page and posts_page are for pages", at)
		}
		if len(p.Comments) > demoMaxComments {
			bad("%s.comments: at most %d", at, demoMaxComments)
		}
		for j, c := range p.Comments {
			if strings.TrimSpace(c.Author) == "" || strings.TrimSpace(c.Content) == "" {
				bad("%s.comments[%d] needs an author and content", at, j)
			}
			if c.Email != "" && !strings.Contains(c.Email, "@") {
				bad("%s.comments[%d].email %q is not an email address", at, j, c.Email)
			}
			if c.URL != "" && !isHTTPURL(c.URL) {
				bad("%s.comments[%d].url %q must be an http(s) URL", at, j, c.URL)
			}
			if c.Date != "" {
				if _, err := parseDemoDate(c.Date, loc); err != nil {
					bad("%s.comments[%d].date %q is not a date", at, j, c.Date)
				}
			}
		}
	}

	// Tokens must point at items of the demo.
	tokens := func(at, text string) {
		for _, t := range demoTokenRe.FindAllStringSubmatch(text, -1) {
			ok := false
			switch t[1] {
			case "media", "media_id":
				ok = mediaIDs[t[2]]
			case "page":
				ok = pages[t[2]]
			case "post":
				ok = posts[t[2]]
			case "category":
				ok = categories[t[2]]
			case "tag":
				ok = tags[t[2]]
			}
			if !ok {
				bad("%s: %s does not refer to an item of the demo", at, t[0])
			}
		}
	}
	for i, p := range d.Pages {
		tokens(fmt.Sprintf("pages[%d]", i), p.Content+p.Excerpt)
	}
	for i, p := range d.Posts {
		tokens(fmt.Sprintf("posts[%d]", i), p.Content+p.Excerpt)
	}

	locations := map[string]bool{}
	for _, l := range m.MenuLocations {
		locations[l.ID] = true
	}
	if len(d.Menus) > demoMaxMenus {
		bad("menus: at most %d", demoMaxMenus)
	}
	usedLoc := map[string]bool{}
	for i, mn := range d.Menus {
		at := fmt.Sprintf("menus[%d]", i)
		if strings.TrimSpace(mn.Name) == "" {
			bad("%s.name is required", at)
		}
		if mn.Location != "" {
			if !locations[mn.Location] {
				bad("%s.location %q is not a menu location of the theme", at, mn.Location)
			} else if usedLoc[mn.Location] {
				bad("%s.location %q is used by another menu", at, mn.Location)
			}
			usedLoc[mn.Location] = true
		}
		var items func(at string, list []demoMenuItem, depth int)
		items = func(at string, list []demoMenuItem, depth int) {
			if depth > 3 {
				bad("%s: menus nest at most 3 levels", at)
				return
			}
			for j, it := range list {
				iat := fmt.Sprintf("%s[%d]", at, j)
				n := 0
				for _, v := range []string{it.Page, it.Post, it.Category, it.URL} {
					if v != "" {
						n++
					}
				}
				switch {
				case n != 1:
					bad("%s needs exactly one of page, post, category, url", iat)
				case it.Page != "" && !pages[it.Page]:
					bad("%s.page %q is not a page of the demo", iat, it.Page)
				case it.Post != "" && !posts[it.Post]:
					bad("%s.post %q is not a post of the demo", iat, it.Post)
				case it.Category != "" && !categories[it.Category]:
					bad("%s.category %q is not a category of the demo", iat, it.Category)
				case it.URL != "" && !isHTTPURL(it.URL) && !strings.HasPrefix(it.URL, "/") && !strings.HasPrefix(it.URL, "#"):
					bad("%s.url %q must be an http(s) URL, a site path or #anchor", iat, it.URL)
				}
				if it.URL != "" && strings.TrimSpace(it.Title) == "" {
					bad("%s.title is required for a url item", iat)
				}
				items(iat+".children", it.Children, depth+1)
			}
		}
		items(at+".items", mn.Items, 1)
	}

	areas := map[string]bool{}
	for _, a := range m.WidgetAreas {
		areas[a.ID] = true
	}
	for id, list := range d.Widgets {
		if !areas[id] {
			bad("widgets.%s is not a widget area of the theme", id)
		}
		for j, w := range list {
			if strings.TrimSpace(w) == "" {
				bad("widgets.%s[%d] is empty", id, j)
			}
			tokens(fmt.Sprintf("widgets.%s[%d]", id, j), w)
		}
	}

	settings := map[string]themeCzSettings{}
	for _, sec := range m.Customizer.Sections {
		for _, st := range sec.Settings {
			settings[st.ID] = st
		}
	}
	for k, v := range d.Customizer {
		at := "customizer." + k
		st, ok := settings[k]
		if k == "custom_logo" {
			st, ok = themeCzSettings{ID: k, Type: "image"}, true
		}
		if !ok {
			bad("%s is not a Customizer setting of the theme", at)
			continue
		}
		if _, err := demoModValue(st, v); err != nil {
			bad("%s: %v", at, err)
			continue
		}
		if st.Type == "image" {
			media(at, demoMediaRef(v.(string)))
		} else if s, ok := v.(string); ok {
			tokens(at, s)
		}
	}
	problems = append(problems, gooValidateDemo(d.Store, d.Media)...) // Goocommerce section (goo_demo.go)
	if len(problems) > 0 {
		return nil, &demoError{file: m.Demo, problems: problems}
	}
	return &d, nil
}

// demoCycles lists the items (sorted) whose parent chain loops.
func demoCycles(parents map[string]string) []string {
	var out []string
	for slug := range parents {
		seen := map[string]bool{}
		for cur := slug; cur != ""; cur = parents[cur] {
			if seen[cur] {
				out = append(out, slug)
				break
			}
			seen[cur] = true
		}
	}
	sort.Strings(out)
	return out
}

// demoMediaRef is the media id of an image setting's value ("hero" or "{{media:hero}}").
func demoMediaRef(v string) string {
	if m := demoTokenRe.FindStringSubmatch(v); m != nil && (m[1] == "media" || m[1] == "media_id") {
		return m[2]
	}
	return v
}

// demoModValue checks a customizer value of demo.json against the setting and returns it in the
// Customizer's raw string form (an image setting returns the media id; the caller maps it).
func demoModValue(st themeCzSettings, v any) (string, error) {
	switch st.Type {
	case "checkbox":
		b, ok := v.(bool)
		if !ok {
			return "", errors.New("must be true or false")
		}
		if b {
			return "1", nil
		}
		return "0", nil
	case "number":
		n, ok := v.(float64)
		if !ok || n != float64(int(n)) || int(n) < themeCzMin(st) || int(n) > themeCzMax(st) {
			return "", errors.New("must be an integer within min and max")
		}
		return strconv.Itoa(int(n)), nil
	case "color":
		s, _ := v.(string)
		if c := sanitizeHex(s); c != "" {
			return c, nil
		}
		return "", errors.New("must be a hex color")
	case "select":
		s, _ := v.(string)
		for _, c := range st.Choices {
			if c[0] == s {
				return s, nil
			}
		}
		return "", errors.New("must be one of the choices")
	case "image":
		s, ok := v.(string)
		if !ok || s == "" {
			return "", errors.New("must be a media id of the demo")
		}
		return demoMediaRef(s), nil
	}
	switch x := v.(type) {
	case string:
		return x, nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	}
	return "", errors.New("must be a string")
}

// themeDemoOf returns the parsed demo of an installed package theme (nil when it has none).
func themeDemoOf(t *Theme) (*themeDemo, error) {
	if t == nil || t.Package == nil || t.Package.Manifest.Demo == "" {
		return nil, nil
	}
	return parseThemeDemo(os.DirFS(t.Package.Dir), t.Package.Manifest)
}

// Import record ------------------------------------------------------------------------

// demoRecord is the wpgo_demo_<slug> option: what an import created and changed besides the
// items marked with _wpgo_demo.
type demoRecord struct {
	Complete   bool             `json:"complete"`
	Done       map[string]bool  `json:"done"`        // record-level steps already applied: options, reading, customizer, menus, widgets
	Options    map[string]any   `json:"options"`     // previous values of the options and reading settings it set
	Mods       map[string]any   `json:"mods"`        // previous values of the theme mods it set
	UnsetMods  []string         `json:"unset_mods"`  // theme mods that were not set before
	Menus      []int64          `json:"menus"`       // menus it created
	Locations  map[string]int64 `json:"locations"`   // menu location -> the menu assigned before (0 = none)
	Widgets    []int64          `json:"widgets"`     // widgets it created
	Moved      map[string]int64 `json:"moved"`       // widget id -> area id it was moved out of (into the inactive widgets)
	ImportedAt string           `json:"imported_at"` // RFC 3339
	Trashed    []int64          `json:"trashed"`     // untouched install samples it moved to the trash
}

func demoRecordKey(slug string) string { return "wpgo_demo_" + slug }

func (s *Server) demoRecord(slug string) *demoRecord {
	raw := s.settings.Get(demoRecordKey(slug))
	if raw == nil {
		return nil
	}
	b, _ := json.Marshal(raw)
	var r demoRecord
	if json.Unmarshal(b, &r) != nil {
		return nil
	}
	return &r
}

// demoImported reports whether the theme's demo content has been imported (and not removed).
func (s *Server) demoImported(slug string) bool {
	r := s.demoRecord(slug)
	return r != nil && r.Complete
}

func (s *Server) saveDemoRecord(ctx context.Context, slug string, r *demoRecord) error {
	return s.settings.Set(ctx, map[string]any{demoRecordKey(slug): r})
}

// demoReport counts what an import created.
type demoReport struct {
	Media, Terms, Pages, Posts, Comments, Menus, MenuItems, Widgets int
	Settings                                                        bool
}

// demoItemID finds an item imported before: table posts or media, matched on the meta.
func (s *Server) demoItemID(ctx context.Context, table, typ, slug, ref string) int64 {
	var id int64
	var err error
	switch table {
	case "posts":
		err = s.store.Pool.QueryRow(ctx, `SELECT id FROM posts WHERE type = $1 AND meta->>'`+demoMetaKey+`' = $2 AND meta->>'`+demoMetaRef+`' = $3 ORDER BY id LIMIT 1`, typ, slug, ref).Scan(&id)
	case "media":
		err = s.store.Pool.QueryRow(ctx, `SELECT id FROM media WHERE meta->>'`+demoMetaKey+`' = $1 AND meta->>'`+demoMetaRef+`' = $2 ORDER BY id LIMIT 1`, slug, ref).Scan(&id)
	}
	if err != nil {
		return 0
	}
	return id
}

// importThemeDemo imports the demo content of an installed package theme for user uid.
func (s *Server) importThemeDemo(ctx context.Context, uid int64, t *Theme) (*demoReport, error) {
	d, err := themeDemoOf(t)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, errors.New("This theme has no demo content.")
	}
	ctx, cancel := context.WithTimeout(ctx, demoTimeLimit)
	defer cancel()
	fsys := os.DirFS(t.Package.Dir)
	slug := t.Slug
	rep := &demoReport{}
	rec := s.demoRecord(slug)
	if rec == nil {
		rec = &demoRecord{}
	}
	if rec.Done == nil {
		rec.Done = map[string]bool{}
	}
	if rec.Options == nil {
		rec.Options = map[string]any{}
	}
	save := func() error { return s.saveDemoRecord(ctx, slug, rec) }
	mark := func(ref string) map[string]any { return map[string]any{demoMetaKey: slug, demoMetaRef: ref} }
	loc := s.loc()

	// The samples of a fresh install ("Hello world!", "Sample Page") would sit among the demo's
	// posts and pages; while nobody has edited them they go to the trash, and Remove Demo
	// Content brings them back.
	if !rec.Done["samples"] {
		rows, err := s.store.Pool.Query(ctx, `SELECT id FROM posts WHERE status = 'publish'
			AND ((type = 'post' AND slug = 'hello-world') OR (type = 'page' AND slug = 'sample-page'))
			AND updated_at - created_at < interval '5 seconds' AND coalesce(meta->>$1, '') = ''`, demoMetaKey)
		if err == nil {
			var ids []int64
			for rows.Next() {
				var id int64
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
			for _, id := range ids {
				if s.store.TrashPost(ctx, id) == nil {
					rec.Trashed = append(rec.Trashed, id)
				}
			}
		}
		rec.Done["samples"] = true
		if err := save(); err != nil {
			return rep, err
		}
	}

	// Media, through the upload pipeline (image sizes are generated).
	mediaIDs, mediaURL := map[string]int64{}, map[string]string{}
	for _, md := range d.Media {
		if id := s.demoItemID(ctx, "media", "", slug, md.ID); id > 0 {
			if a, err := s.store.GetAttachment(ctx, id); err == nil {
				mediaIDs[md.ID], mediaURL[md.ID] = id, s.uploadURL(ctx, a.FilePath)
				continue
			}
		}
		b, err := fs.ReadFile(fsys, md.File)
		if err != nil {
			return rep, fmt.Errorf("%s: %v", md.File, err)
		}
		// Prefixed with the theme: uploads are cached as immutable, so two demos must never reuse a
		// file name (one demo's hero.jpg removed, the next one's served from the browser cache).
		name := path.Base(md.File)
		if !strings.HasPrefix(name, slug+"-") {
			name = slug + "-" + name
		}
		a, err := s.insertAttachmentFile(ctx, name, bytes.NewReader(b), uid, 0)
		if err != nil {
			return rep, fmt.Errorf("%s: %v", md.File, err)
		}
		if md.Title != "" {
			a.Title = md.Title
		}
		a.Alt, a.Caption = md.Alt, md.Caption
		for k, v := range mark(md.ID) {
			a.Meta[k] = v
		}
		if err := s.store.UpdateAttachment(ctx, a); err != nil {
			return rep, err
		}
		mediaIDs[md.ID], mediaURL[md.ID] = a.ID, s.uploadURL(ctx, a.FilePath)
		rep.Media++
	}

	// Goocommerce: the store, its products, categories and coupons (goo_demo.go).
	if !rec.Done["goocommerce"] && len(d.Store) > 0 {
		n, err := s.gooImportDemo(ctx, uid, slug, d.Store, mediaIDs)
		if err != nil {
			return rep, err
		}
		rep.Posts += n
		rec.Done["goocommerce"] = true
		if err := save(); err != nil {
			return rep, err
		}
	}

	// Categories and tags: an existing term with the slug is used as it is (and never removed).
	termIDs := map[string]int64{} // "category:slug" -> id
	var allTerms []int64
	ensureTerm := func(tax string, dt demoTerm) error {
		key := tax + ":" + dt.Slug
		if _, ok := termIDs[key]; ok {
			return nil
		}
		if ex, err := s.store.TermBySlug(ctx, tax, dt.Slug); err == nil {
			termIDs[key] = ex.ID
			return nil
		}
		var parent int64
		if dt.Parent != "" {
			parent = termIDs[tax+":"+dt.Parent]
		}
		nt := &store.Term{Taxonomy: tax, Slug: dt.Slug, Name: dt.Name, Description: dt.Description, ParentID: parent, Meta: mark(dt.Slug)}
		if err := s.store.InsertTerm(ctx, nt); err != nil {
			if errors.Is(err, store.ErrTermExists) { // same name under another slug: use that one
				if ex, err := s.store.TermByName(ctx, tax, dt.Name); err == nil {
					termIDs[key] = ex.ID
					return nil
				}
			}
			return fmt.Errorf("%s %q: %v", tax, dt.Name, err)
		}
		termIDs[key] = nt.ID
		allTerms = append(allTerms, nt.ID)
		rep.Terms++
		return nil
	}
	// Parents first: categories whose parent is listed later are created after it.
	cats := append([]demoTerm{}, d.Categories...)
	sort.SliceStable(cats, func(i, j int) bool { return cats[i].Parent == "" && cats[j].Parent != "" })
	for pass := 0; pass < len(cats)+1; pass++ {
		left := 0
		for _, c := range cats {
			if _, ok := termIDs["category:"+c.Slug]; ok {
				continue
			}
			if c.Parent != "" {
				if _, ok := termIDs["category:"+c.Parent]; !ok {
					left++
					continue
				}
			}
			if err := ensureTerm("category", c); err != nil {
				return rep, err
			}
		}
		if left == 0 {
			break
		}
	}
	for _, tg := range d.Tags {
		if err := ensureTerm("post_tag", tg); err != nil {
			return rep, err
		}
	}

	// Pages and posts are inserted with their content as written; the tokens are replaced once every
	// item exists (permalinks need the ids and parents), then the revisions that makes are dropped.
	type created struct {
		p    *store.Post
		item demoPost
	}
	var fresh []created
	ids := map[string]int64{} // "page:slug" / "post:slug" -> id
	insert := func(typ string, it demoPost, i int) error {
		key := typ + ":" + it.Slug
		if id := s.demoItemID(ctx, "posts", typ, slug, it.Slug); id > 0 {
			ids[key] = id
			return nil
		}
		p := &store.Post{Type: typ, AuthorID: uid, Title: it.Title, Content: it.Content, Excerpt: it.Excerpt, Status: "publish",
			Slug: it.Slug, MenuOrder: it.MenuOrder, CommentStatus: "open", PingStatus: "closed", Sticky: it.Sticky,
			Format: defaultStr(it.Format, "standard"), FeaturedMediaID: mediaIDs[it.FeaturedMedia], Meta: mark(it.Slug)}
		if typ == "page" {
			p.CommentStatus = "closed"
			if it.Parent != "" {
				p.ParentID = ids["page:"+it.Parent]
			}
			if it.Template != "" {
				p.Meta["_wp_page_template"] = it.Template
			}
		}
		if it.Date != "" {
			if dt, err := parseDemoDate(it.Date, loc); err == nil {
				p.PublishedAt = &dt
			}
		} else if typ == "post" { // undated posts: one day apart, newest first
			dt := time.Now().Add(-time.Duration(i+1) * 24 * time.Hour).Truncate(time.Minute)
			p.PublishedAt = &dt
		}
		if err := s.store.InsertPost(ctx, p); err != nil {
			return fmt.Errorf("%s %q: %v", typ, it.Title, err)
		}
		ids[key] = p.ID
		fresh = append(fresh, created{p, it})
		if typ == "post" {
			var catIDs, tagIDs []int64
			for _, c := range it.Categories {
				catIDs = append(catIDs, termIDs["category:"+c])
			}
			for _, tg := range it.Tags {
				tagIDs = append(tagIDs, termIDs["post_tag:"+tg])
			}
			if len(catIDs) == 0 {
				catIDs = []int64{int64(s.settings.Int("default_category"))}
			}
			if err := s.store.SetPostTerms(ctx, p.ID, "category", catIDs); err != nil {
				return err
			}
			if err := s.store.SetPostTerms(ctx, p.ID, "post_tag", tagIDs); err != nil {
				return err
			}
			for _, cm := range it.Comments {
				nc := &store.Comment{PostID: p.ID, AuthorName: cm.Author, AuthorEmail: cm.Email, AuthorURL: cm.URL, Content: cm.Content,
					Type: "comment", Status: "approved", AuthorIP: "127.0.0.1"}
				if err := s.store.InsertComment(ctx, nc); err != nil {
					return err
				}
				if cm.Date != "" {
					if dt, err := parseDemoDate(cm.Date, loc); err == nil {
						_, _ = s.store.Pool.Exec(ctx, `UPDATE comments SET created_at = $2 WHERE id = $1`, nc.ID, dt)
					}
				}
				rep.Comments++
			}
			rep.Posts++
		} else {
			rep.Pages++
		}
		return nil
	}
	for i, p := range d.Pages {
		if err := insert("page", p, i); err != nil {
			return rep, err
		}
	}
	for _, f := range fresh { // backfill_parents(): parents listed after their children
		if f.p.Type == "page" && f.item.Parent != "" && f.p.ParentID == 0 {
			if pid := ids["page:"+f.item.Parent]; pid > 0 {
				f.p.ParentID = pid
				_, _ = s.store.Pool.Exec(ctx, `UPDATE posts SET parent_id = $2 WHERE id = $1`, f.p.ID, pid)
			}
		}
	}
	for i, p := range d.Posts {
		if err := insert("post", p, i); err != nil {
			return rep, err
		}
	}

	lb := s.links(ctx)
	permalinks := map[string]string{}
	for key, id := range ids {
		if p, err := s.store.GetPost(ctx, id); err == nil {
			permalinks[key] = lb.Permalink(p)
		}
	}
	termLinks := map[string]string{}
	for key, id := range termIDs {
		if tm, err := s.store.GetTerm(ctx, id); err == nil {
			termLinks[key] = lb.TermLink(tm)
		}
	}
	replace := func(text string) string {
		return demoTokenRe.ReplaceAllStringFunc(text, func(tok string) string {
			m := demoTokenRe.FindStringSubmatch(tok)
			switch m[1] {
			case "media":
				return mediaURL[m[2]]
			case "media_id":
				return strconv.FormatInt(mediaIDs[m[2]], 10)
			case "page", "post":
				return permalinks[m[1]+":"+m[2]]
			case "category":
				return termLinks["category:"+m[2]]
			case "tag":
				return termLinks["post_tag:"+m[2]]
			}
			return tok
		})
	}
	for _, f := range fresh {
		content, excerpt := replace(f.p.Content), replace(f.p.Excerpt)
		if content == f.p.Content && excerpt == f.p.Excerpt {
			continue
		}
		p, err := s.store.GetPost(ctx, f.p.ID)
		if err != nil {
			return rep, err
		}
		p.Content, p.Excerpt = content, excerpt
		if err := s.store.UpdatePost(ctx, p, uid); err != nil {
			return rep, err
		}
		_, _ = s.store.Pool.Exec(ctx, `DELETE FROM post_revisions WHERE post_id = $1`, p.ID)
	}
	_ = s.store.RecountTerms(ctx, allTerms)

	// Options and Settings › Reading: the previous values are kept for Remove Demo Content.
	remember := func(keys ...string) {
		for _, k := range keys {
			if _, ok := rec.Options[k]; !ok {
				rec.Options[k] = s.settings.Get(k)
			}
		}
	}
	if !rec.Done["options"] {
		values := map[string]any{}
		for k, v := range d.Options {
			remember(k)
			if k == "posts_per_page" {
				values[k] = int(v.(float64))
			} else {
				values[k] = strings.TrimSpace(czTagRe.ReplaceAllString(v.(string), ""))
			}
		}
		if len(values) > 0 {
			if err := s.settings.Set(ctx, values); err != nil {
				return rep, err
			}
			rep.Settings = true
		}
		rec.Done["options"] = true
		if err := save(); err != nil {
			return rep, err
		}
	}
	if !rec.Done["reading"] {
		values := map[string]any{}
		for _, p := range d.Pages {
			id := ids["page:"+p.Slug]
			if p.FrontPage && id > 0 {
				values["show_on_front"], values["page_on_front"] = "page", id
			}
			if p.PostsPage && id > 0 {
				values["page_for_posts"] = id
			}
		}
		if _, static := values["show_on_front"]; !static && s.settings.String("show_on_front") == "page" {
			// A blog-style demo: the front page shows the latest posts, not a page left from another demo.
			values["show_on_front"] = "posts"
		}
		if len(values) > 0 {
			remember("show_on_front", "page_on_front", "page_for_posts")
			if err := s.settings.Set(ctx, values); err != nil {
				return rep, err
			}
			rep.Settings = true
		}
		rec.Done["reading"] = true
		if err := save(); err != nil {
			return rep, err
		}
	}

	// Customizer values: theme mods of this theme, sanitized like a Customizer publish.
	if !rec.Done["customizer"] && len(d.Customizer) > 0 {
		settings := map[string]themeCzSettings{"custom_logo": {ID: "custom_logo", Type: "image"}}
		for _, st := range t.Package.settings {
			settings[st.ID] = st
		}
		values := map[string]string{}
		for k, v := range d.Customizer {
			raw, err := demoModValue(settings[k], v)
			if err != nil {
				return rep, fmt.Errorf("customizer.%s: %v", k, err)
			}
			if settings[k].Type == "image" {
				raw = strconv.FormatInt(mediaIDs[raw], 10)
			} else {
				raw = replace(raw)
			}
			values["mod:"+k] = raw
		}
		current := s.themeMods(slug)
		if rec.Mods == nil {
			rec.Mods = map[string]any{}
		}
		for k := range d.Customizer {
			if v, ok := current[k]; ok {
				rec.Mods[k] = v
			} else {
				rec.UnsetMods = append(rec.UnsetMods, k)
			}
		}
		_, mods, err := s.czSanitize(ctx, t, values)
		if err != nil {
			return rep, fmt.Errorf("customizer: %v", err)
		}
		if err := s.settings.Set(ctx, map[string]any{"theme_mods_" + slug: mods}); err != nil {
			return rep, err
		}
		rep.Settings = true
		rec.Done["customizer"] = true
		if err := save(); err != nil {
			return rep, err
		}
	}

	// Menus, assigned to the theme's locations (the menu there before is restored on removal).
	if !rec.Done["menus"] && len(d.Menus) > 0 {
		existing, err := s.store.Menus(ctx)
		if err != nil {
			return rep, err
		}
		if rec.Locations == nil {
			rec.Locations = map[string]int64{}
		}
		for i, mn := range d.Menus {
			if i < len(rec.Menus) { // created by an import that stopped half-way
				continue
			}
			menuID, err := s.store.CreateMenu(ctx, mn.Name)
			if err != nil {
				return rep, err
			}
			rec.Menus = append(rec.Menus, menuID)
			rep.Menus++
			order := 0
			var add func(list []demoMenuItem, parent int64) error
			add = func(list []demoMenuItem, parent int64) error {
				for _, it := range list {
					order++
					mi := &store.EditItem{MenuID: menuID, ParentID: parent, Label: it.Title, Order: order, Status: "publish"}
					switch {
					case it.Page != "":
						mi.Kind, mi.Object, mi.TargetID = "post_type", "page", ids["page:"+it.Page]
					case it.Post != "":
						mi.Kind, mi.Object, mi.TargetID = "post_type", "post", ids["post:"+it.Post]
					case it.Category != "":
						mi.Kind, mi.Object, mi.TargetID = "taxonomy", "category", termIDs["category:"+it.Category]
					default:
						mi.Kind, mi.Object, mi.URL = "custom", "custom", it.URL
					}
					if mi.Label == "" {
						if mi.Kind == "taxonomy" {
							if tm, err := s.store.GetTerm(ctx, mi.TargetID); err == nil {
								mi.Label = tm.Name
							}
						} else if p, err := s.store.GetPost(ctx, mi.TargetID); err == nil {
							mi.Label = p.Title
						}
					}
					if err := s.store.AddMenuItem(ctx, mi); err != nil {
						return err
					}
					rep.MenuItems++
					if err := add(it.Children, mi.ID); err != nil {
						return err
					}
				}
				return nil
			}
			if err := add(mn.Items, 0); err != nil {
				return rep, err
			}
			if mn.Location != "" {
				if _, ok := rec.Locations[mn.Location]; !ok {
					rec.Locations[mn.Location] = 0
					for _, m := range existing {
						if m.Location == mn.Location {
							rec.Locations[mn.Location] = m.ID
						}
					}
				}
				if err := s.store.SetMenuLocations(ctx, map[string]int64{mn.Location: menuID}); err != nil {
					return rep, err
				}
			}
			if err := save(); err != nil {
				return rep, err
			}
		}
		rec.Done["menus"] = true
		if err := save(); err != nil {
			return rep, err
		}
	}

	// Block widgets: the widgets already in those areas move to the inactive widgets.
	if !rec.Done["widgets"] && len(d.Widgets) > 0 {
		var noArea *int64
		areaIDs, err := s.store.WidgetAreaIDs(ctx, slug)
		if err != nil {
			return rep, err
		}
		current, err := s.store.ThemeWidgets(ctx, slug)
		if err != nil {
			return rep, err
		}
		if rec.Moved == nil {
			rec.Moved = map[string]int64{}
		}
		areas := make([]string, 0, len(d.Widgets))
		for id := range d.Widgets {
			areas = append(areas, id)
		}
		sort.Strings(areas)
		for _, areaSlug := range areas {
			aid, ok := areaIDs[areaSlug]
			if !ok {
				name := areaSlug
				if a := t.widgetArea(areaSlug); a != nil {
					name = a.Name
				}
				if aid, err = s.store.EnsureWidgetArea(ctx, slug, areaSlug, name); err != nil {
					return rep, err
				}
			}
			for _, w := range current {
				if w.AreaSlug == areaSlug && w.AreaTheme == slug {
					if err := s.store.MoveWidget(ctx, w.ID, noArea); err != nil {
						return rep, err
					}
					rec.Moved[strconv.FormatInt(w.ID, 10)] = aid
				}
			}
			for _, content := range d.Widgets[areaSlug] {
				id, err := s.store.CreateWidget(ctx, &aid, "block", map[string]any{"content": replace(content)})
				if err != nil {
					return rep, err
				}
				rec.Widgets = append(rec.Widgets, id)
				rep.Widgets++
			}
		}
		rec.Done["widgets"] = true
		if err := save(); err != nil {
			return rep, err
		}
	}

	if !rec.Complete {
		rec.Complete, rec.ImportedAt = true, time.Now().UTC().Format(time.RFC3339)
		if err := save(); err != nil {
			return rep, err
		}
	}
	s.purgeAll(ctx)
	return rep, nil
}

// removeThemeDemo is Remove Demo Content: the items marked with the theme's _wpgo_demo meta, the
// menus and widgets the import created go; moved widgets, options, reading settings, menu locations
// and theme mods get their previous values back.
func (s *Server) removeThemeDemo(ctx context.Context, slug string) error {
	ctx, cancel := context.WithTimeout(ctx, demoTimeLimit)
	defer cancel()
	rec := s.demoRecord(slug)
	if rec == nil {
		rec = &demoRecord{}
	}
	idsOf := func(q string) ([]int64, error) {
		rows, err := s.store.Pool.Query(ctx, q, slug)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			out = append(out, id)
		}
		return out, rows.Err()
	}
	for _, id := range rec.Trashed { // the install samples it put in the trash, back as they were
		_, _ = s.store.Pool.Exec(ctx, `UPDATE posts SET status = coalesce(trashed_status, 'publish'), trashed_status = NULL, deleted_at = NULL
			WHERE id = $1 AND status = 'trash'`, id)
	}
	// Menus and widgets first (menu items point at the pages and terms).
	for _, id := range rec.Menus {
		if err := s.store.DeleteMenu(ctx, id); err != nil {
			return err
		}
	}
	if len(rec.Locations) > 0 {
		menus, _ := s.store.Menus(ctx)
		exists := map[int64]bool{}
		for _, m := range menus {
			exists[m.ID] = true
		}
		locs := map[string]int64{}
		for l, id := range rec.Locations {
			if id > 0 && exists[id] {
				locs[l] = id
			}
		}
		if len(locs) > 0 {
			if err := s.store.SetMenuLocations(ctx, locs); err != nil {
				return err
			}
		}
	}
	for _, id := range rec.Widgets {
		if err := s.store.DeleteWidget(ctx, id); err != nil {
			return err
		}
	}
	for key, area := range rec.Moved {
		id, _ := strconv.ParseInt(key, 10, 64)
		if w, err := s.store.GetAdminWidget(ctx, id); err == nil && w.AreaSlug == "" {
			if err := s.store.MoveWidget(ctx, id, &area); err != nil {
				return err
			}
		}
	}
	s.gooRemoveDemo(ctx, slug) // Goocommerce: demo coupons, shipping methods, reviews (goo_demo.go)
	posts, err := idsOf(`SELECT id FROM posts WHERE meta->>'` + demoMetaKey + `' = $1 ORDER BY id DESC`)
	if err != nil {
		return err
	}
	for _, id := range posts {
		if err := s.store.DeletePost(ctx, id); err != nil {
			return err
		}
	}
	media, err := idsOf(`SELECT id FROM media WHERE meta->>'` + demoMetaKey + `' = $1`)
	if err != nil {
		return err
	}
	for _, id := range media {
		if a, err := s.store.GetAttachment(ctx, id); err == nil {
			s.deleteAttachmentFiles(a)
		}
		if err := s.store.DeleteAttachment(ctx, id); err != nil {
			return err
		}
	}
	terms, err := idsOf(`SELECT id FROM terms WHERE meta->>'` + demoMetaKey + `' = $1 ORDER BY id DESC`)
	if err != nil {
		return err
	}
	def := int64(s.settings.Int("default_category"))
	for _, id := range terms {
		if id == def {
			continue
		}
		if tm, err := s.store.GetTerm(ctx, id); err == nil {
			if err := s.store.DeleteTerm(ctx, tm, def); err != nil {
				return err
			}
		}
	}
	_ = s.store.RecountTerms(ctx, []int64{def})
	values := map[string]any{}
	for k, v := range rec.Options {
		values[k] = v
	}
	if len(rec.Mods) > 0 || len(rec.UnsetMods) > 0 {
		mods := map[string]any{}
		if m, ok := s.settings.Get("theme_mods_" + slug).(map[string]any); ok {
			for k, v := range m {
				mods[k] = v
			}
		}
		for k, v := range rec.Mods {
			mods[k] = v
		}
		for _, k := range rec.UnsetMods {
			delete(mods, k)
		}
		values["theme_mods_"+slug] = mods
	}
	values[demoRecordKey(slug)] = nil
	if err := s.settings.Set(ctx, values); err != nil {
		return err
	}
	s.purgeAll(ctx)
	return nil
}

// summary is the admin notice text of an import.
func (r *demoReport) summary() string {
	var parts []string
	add := func(n int, one, many string) {
		if n > 0 {
			if n != 1 {
				one = many
			}
			parts = append(parts, fmt.Sprintf("%d %s", n, one))
		}
	}
	add(r.Pages, "page", "pages")
	add(r.Posts, "post", "posts")
	add(r.Media, "image", "images")
	add(r.Terms, "category or tag", "categories and tags")
	add(r.Comments, "comment", "comments")
	add(r.Menus, "menu", "menus")
	add(r.Widgets, "widget", "widgets")
	if len(parts) == 0 {
		return "Nothing new was imported: the demo content is already on the site."
	}
	return "Demo content imported: " + strings.Join(parts, ", ") + "."
}

// query carries the counts of an import to the Themes screen's notice.
func (r *demoReport) query() string {
	return url.Values{"demo": {"imported"}, "counts": {fmt.Sprintf("%d.%d.%d.%d.%d.%d.%d", r.Pages, r.Posts, r.Media, r.Terms,
		r.Comments, r.Menus, r.Widgets)}}.Encode()
}

// demoReportFromQuery reads the counts written by query.
func demoReportFromQuery(v string) *demoReport {
	var n [7]int
	for i, part := range strings.SplitN(v, ".", 7) {
		n[i], _ = strconv.Atoi(part)
	}
	return &demoReport{Pages: n[0], Posts: n[1], Media: n[2], Terms: n[3], Comments: n[4], Menus: n[5], Widgets: n[6]}
}
