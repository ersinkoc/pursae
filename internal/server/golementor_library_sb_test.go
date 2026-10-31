package server

import (
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

	"wpgo/internal/builder"
)

// Golementor Studio section library, group SB (faq, stats, gallery, logos, blog, contact,
// newsletter, content) and the shared placeholder images: every section file is checked against
// the content format of docs/golementor-studio.md and the editor's control lists, then saved as a
// Golementor page through save_builder and rendered on the front end.

const glibSBDir = "../../web/gowebeditor/library"

var glibSBCategories = []string{"faq", "stats", "gallery", "logos", "blog", "contact", "newsletter", "content"}

var glibSBAllCategories = map[string]bool{"hero": true, "features": true, "about": true, "services": true, "cta": true,
	"testimonials": true, "pricing": true, "team": true, "faq": true, "stats": true, "gallery": true, "logos": true,
	"blog": true, "contact": true, "newsletter": true, "content": true}

var (
	glibSBSlugRe     = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	glibSBGlobalRe   = regexp.MustCompile(`^globals/(colors|typography)\?id=(primary|secondary|text|accent)$`)
	glibSBTokenRe    = regexp.MustCompile(`\{\{([a-z_]+):([^}]*)\}\}`)
	glibSBBraceRe    = regexp.MustCompile(`\{\{|\}\}`)
	glibSBSuffixRe   = regexp.MustCompile(`_(tablet|mobile|laptop|widescreen|tablet_extra|mobile_extra)$`)
	glibSBLoremRe    = regexp.MustCompile(`(?i)lorem ipsum|dolor sit amet`)
	glibSBHeadingsRe = regexp.MustCompile(`^[A-Za-z ,.]+$`)
)

type glibSBSection struct {
	Path, Category, Slug string
	Title                string          `json:"title"`
	Cat                  string          `json:"category"`
	Tags                 []string        `json:"tags"`
	Thumbnail            string          `json:"thumbnail"`
	Content              json.RawMessage `json:"content"`
}

func glibSBLoad(t *testing.T) []glibSBSection {
	t.Helper()
	var out []glibSBSection
	for _, cat := range glibSBCategories {
		files, _ := filepath.Glob(filepath.Join(glibSBDir, "sections", cat, "*.json"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var s glibSBSection
			dec := json.NewDecoder(strings.NewReader(string(b)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&s); err != nil {
				t.Errorf("%s: %v", f, err)
				continue
			}
			s.Path, s.Category, s.Slug = f, cat, strings.TrimSuffix(filepath.Base(f), ".json")
			out = append(out, s)
		}
	}
	return out
}

// glibSBControls are the control names of every widget and of the container (common controls included).
func glibSBControls(t *testing.T) (widgets map[string]map[string]map[string]any, container map[string]map[string]any) {
	t.Helper()
	type cfg map[string]struct {
		Controls map[string]map[string]any `json:"controls"`
	}
	read := func(name string) cfg {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(gweRead(name), &raw); err != nil {
			t.Fatal(err)
		}
		out := cfg{}
		for k, v := range raw {
			var def struct {
				Controls map[string]map[string]any `json:"controls"`
			}
			if json.Unmarshal(v, &def) == nil { // a few legacy entries (inner-section) list no controls map
				out[k] = def
			}
		}
		return out
	}
	w, p := read("widgets-config.json.gz"), read("pro-widgets-config.json.gz")
	var ed struct {
		Elements map[string]struct {
			Controls map[string]map[string]any `json:"controls"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(gweRead("editor-config.json.gz"), &ed); err != nil {
		t.Fatal(err)
	}
	common := map[string]map[string]any{}
	for _, src := range []cfg{w, p} {
		for _, k := range []string{"common", "common-base", "common-optimized"} {
			for n, c := range src[k].Controls {
				common[n] = c
			}
		}
	}
	widgets = map[string]map[string]map[string]any{}
	for _, src := range []cfg{w, p} {
		for name, def := range src {
			m := map[string]map[string]any{}
			for n, c := range common {
				m[n] = c
			}
			for n, c := range def.Controls {
				m[n] = c
			}
			widgets[name] = m
		}
	}
	return widgets, ed.Elements["container"].Controls
}

func glibSBFieldNames(c map[string]any) map[string]bool {
	out := map[string]bool{}
	switch f := c["fields"].(type) {
	case map[string]any:
		for k := range f {
			out[k] = true
		}
	case []any:
		for _, x := range f {
			if m, ok := x.(map[string]any); ok {
				out[m["name"].(string)] = true
			}
		}
	}
	return out
}

// glibSBCheckSettings reports settings keys that are not controls, select values that are not options
// and malformed or unknown __globals__ references.
func glibSBCheckSettings(where string, st map[string]any, controls map[string]map[string]any) []string {
	var bad []string
	lookup := func(k string) map[string]any {
		if c := controls[k]; c != nil {
			return c
		}
		return controls[glibSBSuffixRe.ReplaceAllString(k, "")]
	}
	for k, v := range st {
		if k == "__globals__" {
			g, _ := v.(map[string]any)
			for gk, gv := range g {
				ref, _ := gv.(string)
				if lookup(gk) == nil {
					bad = append(bad, where+": __globals__ names unknown control "+gk)
				}
				if ref != "" && !glibSBGlobalRe.MatchString(ref) {
					bad = append(bad, where+": __globals__."+gk+" is not a kit global: "+ref)
				}
			}
			continue
		}
		c := lookup(k)
		if c == nil {
			bad = append(bad, where+": unknown control "+k)
			continue
		}
		typ, _ := c["type"].(string)
		if (typ == "repeater" || typ == "form-fields-repeater") && v != nil {
			names := glibSBFieldNames(c)
			items, _ := v.([]any)
			for _, it := range items {
				m, _ := it.(map[string]any)
				for ik := range m {
					if ik != "_id" && !names[glibSBSuffixRe.ReplaceAllString(ik, "")] {
						bad = append(bad, where+": "+k+" item has unknown field "+ik)
					}
				}
			}
		}
		if s, ok := v.(string); ok && s != "" && (typ == "select" || typ == "choose") {
			if opts, ok := c["options"].(map[string]any); ok {
				if _, ok := opts[s]; !ok {
					bad = append(bad, where+": "+k+" has no option "+s)
				}
			}
		}
	}
	return bad
}

// The section files: format, categories, thumbnails, element tree, controls, globals and tokens.
func TestGolementorLibrarySBFiles(t *testing.T) {
	secs := glibSBLoad(t)
	if len(secs) != 30 {
		t.Errorf("group SB has %d sections, want 30", len(secs))
	}
	want := map[string]int{"faq": 3, "stats": 3, "gallery": 4, "logos": 2, "blog": 4, "contact": 5, "newsletter": 3, "content": 6}
	got := map[string]int{}
	widgets, container := glibSBControls(t)
	seenTitle := map[string]string{}
	for _, s := range secs {
		got[s.Category]++
		bad := func(f string, a ...any) { t.Errorf(s.Category+"/"+s.Slug+": "+f, a...) }
		if !glibSBSlugRe.MatchString(s.Slug) {
			bad("file name is not a slug")
		}
		if s.Cat != s.Category || !glibSBAllCategories[s.Cat] {
			bad("category %q (directory %q)", s.Cat, s.Category)
		}
		if strings.TrimSpace(s.Title) == "" {
			bad("no title")
		} else if other, dup := seenTitle[s.Title]; dup {
			bad("title %q already used by %s", s.Title, other)
		}
		seenTitle[s.Title] = s.Slug
		if len(s.Tags) == 0 {
			bad("no tags")
		}
		for _, tag := range s.Tags {
			if strings.TrimSpace(tag) == "" {
				bad("empty tag")
			}
		}
		if s.Thumbnail != s.Slug+".webp" {
			bad("thumbnail %q, want %q", s.Thumbnail, s.Slug+".webp")
		} else if b, err := os.ReadFile(filepath.Join(filepath.Dir(s.Path), s.Thumbnail)); err != nil || len(b) < 1000 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
			bad("thumbnail is missing or not a webp")
		}
		raw := string(s.Content)
		if glibSBLoremRe.MatchString(raw) {
			bad("placeholder text")
		}
		// tokens: only {{library:images/<existing file>}}; no stray braces
		for _, m := range glibSBTokenRe.FindAllStringSubmatch(raw, -1) {
			if m[1] != "library" || !strings.HasPrefix(m[2], "images/") || path.Clean(m[2]) != m[2] {
				bad("bad token %s", m[0])
			} else if _, err := os.Stat(filepath.Join(glibSBDir, m[2])); err != nil {
				bad("token %s: file is missing", m[0])
			}
		}
		if n := len(glibSBBraceRe.FindAllString(glibSBTokenRe.ReplaceAllString(raw, ""), -1)); n > 0 {
			bad("%d malformed token braces", n)
		}
		els, err := builder.ParseElements(s.Content)
		if err != nil || len(els) == 0 {
			bad("content does not parse as an element tree: %v", err)
			continue
		}
		var walk func(list []*builder.Element, depth int)
		walk = func(list []*builder.Element, depth int) {
			for _, el := range list {
				switch {
				case el.ElType == "container":
					if depth == 0 && el.IsInner || depth > 0 && !el.IsInner {
						bad("container %s: isInner=%v at depth %d", el.ID, el.IsInner, depth)
					}
					for _, p := range glibSBCheckSettings("container "+el.ID, el.Settings, container) {
						bad("%s", p)
					}
				case el.ElType == "widget":
					if depth == 0 {
						bad("top-level widget %s", el.WidgetType)
					}
					ctrls := widgets[el.WidgetType]
					if ctrls == nil {
						bad("unknown widget %q", el.WidgetType)
						continue
					}
					for _, p := range glibSBCheckSettings(el.WidgetType+" "+el.ID, el.Settings, ctrls) {
						bad("%s", p)
					}
				default:
					bad("elType %q", el.ElType)
				}
				walk(el.Elements, depth+1)
			}
		}
		walk(els, 0)
	}
	for c, n := range want {
		if got[c] != n {
			t.Errorf("category %s has %d sections, want %d", c, got[c], n)
		}
	}
}

// The shared placeholder images: webp, at most 150 KB, each listed in the README.
func TestGolementorLibrarySBImages(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join(glibSBDir, "images", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(glibSBDir, "images", "*.webp"))
	if len(files) < 40 {
		t.Errorf("%d placeholder images, want at least 40", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(f)
		if len(b) > 150*1024 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
			t.Errorf("%s: %d bytes or not a webp", name, len(b))
		}
		if !strings.Contains(string(readme), "`"+name+"`") {
			t.Errorf("%s is not listed in the README", name)
		}
	}
}

// Every section saved as a Golementor page through save_builder renders on the front end.
func TestGolementorLibrarySBRender(t *testing.T) {
	secs := glibSBLoad(t)
	ts, c := testServer(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	assets := base + gweAssetsPath + "library/"
	if code, _, _ := get(t, c, assets+"images/team-1.webp"); code != http.StatusOK {
		t.Fatalf("library image is not served: %d", code)
	}
	// a few posts, so post lists with an offset (featured post + list) have something to show
	for _, title := range []string{"First sample story", "Second sample story", "Third sample story"} {
		if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts", `{"title":"`+title+`","status":"publish","content":"<p>Sample.</p>"}`); code != http.StatusCreated {
			t.Fatalf("create post: %d %.200s", code, b)
		}
	}
	nonceRe := regexp.MustCompile(`"ajax":\{"nonce":"([^"]+)"`)
	for _, s := range secs {
		name := s.Category + "/" + s.Slug
		code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/pages", `{"title":"`+s.Slug+`","status":"publish"}`)
		var page struct {
			ID   int64  `json:"id"`
			Link string `json:"link"`
		}
		if code != http.StatusCreated && code != http.StatusOK || json.Unmarshal([]byte(b), &page) != nil || page.ID == 0 {
			t.Fatalf("%s: create page: %d %.200s", name, code, b)
		}
		id := strconv.FormatInt(page.ID, 10)
		_, editor, _ := get(t, c, base+"/wp-admin/post?post="+id+"&action=golementor")
		m := nonceRe.FindStringSubmatch(editor)
		if m == nil {
			t.Fatalf("%s: no editor nonce", name)
		}
		tree := glibSBTokenRe.ReplaceAllStringFunc(string(s.Content), func(tok string) string {
			return assets + glibSBTokenRe.FindStringSubmatch(tok)[2]
		})
		actions := `{"s":{"action":"save_builder","data":{"status":"publish","elements":` + tree +
			`,"settings":{"post_title":` + string(glibSBJSON(s.Title)) + `,"post_status":"publish","template":"elementor_canvas"}}}}`
		resp, err := c.PostForm(base+"/wp-admin/admin-ajax", url.Values{"action": {"elementor_ajax"}, "_nonce": {m[1]},
			"editor_post_id": {id}, "initial_document_id": {id}, "actions": {actions}})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(raw), `"success":true`) {
			t.Errorf("%s: save_builder: %.300s", name, raw)
			continue
		}
		link := page.Link
		if link == "" {
			link = base + "/?page_id=" + id
		}
		code, body, loc := get(t, c, link)
		if code == http.StatusMovedPermanently || code == http.StatusFound {
			code, body, _ = get(t, c, loc)
		}
		if code != http.StatusOK {
			t.Errorf("%s: front end %d", name, code)
			continue
		}
		for _, badText := range []string{"Pursae Web Builder: unsupported element", "Pursae Web Builder: failed to render", "{{library:", "@@ASSETS@@"} {
			if strings.Contains(body, badText) {
				t.Errorf("%s: page contains %q", name, badText)
			}
		}
		if !strings.Contains(body, `data-elementor-id="`+id+`"`) {
			t.Errorf("%s: no Pursae Web Builder document wrapper", name)
		}
		// every widget of the section is on the page, and its plain headings read as written
		els, _ := builder.ParseElements(s.Content)
		var walk func(list []*builder.Element)
		walk = func(list []*builder.Element) {
			for _, el := range list {
				if el.ElType == "widget" && !strings.Contains(body, `data-id="`+el.ID+`"`) {
					t.Errorf("%s: widget %s %s is not rendered", name, el.WidgetType, el.ID)
				}
				if el.WidgetType == "heading" {
					if title, _ := el.Settings["title"].(string); glibSBHeadingsRe.MatchString(title) && !strings.Contains(body, html.EscapeString(title)) {
						t.Errorf("%s: heading %q is missing", name, title)
					}
				}
				walk(el.Elements)
			}
		}
		walk(els)
	}
}

func glibSBJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
