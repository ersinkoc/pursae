package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Golementor Studio section library, group SA: the hero, features, about, services, cta,
// testimonials, pricing and team sections in web/gowebeditor/library/sections. Every template is
// checked against the editor's widget and container controls (docs/golementor-studio.md §3), then
// saved as a Golementor page through save_builder and rendered on the front end.

const glibSADir = "../../web/gowebeditor/library"

var glibSACategories = []string{"hero", "features", "about", "services", "cta", "testimonials", "pricing", "team"}

// glibSAAllCategories are the categories the format allows.
var glibSAAllCategories = map[string]bool{"hero": true, "features": true, "about": true, "services": true, "cta": true,
	"testimonials": true, "pricing": true, "team": true, "faq": true, "stats": true, "gallery": true, "logos": true,
	"blog": true, "contact": true, "newsletter": true, "content": true}

var glibSAGlobalIDs = map[string]bool{"primary": true, "secondary": true, "text": true, "accent": true}

var (
	glibSAGlobalRe = regexp.MustCompile(`^globals/(colors|typography)\?id=([a-z0-9_-]+)$`)
	glibSATokenRe  = regexp.MustCompile(`\{\{[^}]*\}\}`)
	glibSALibImgRe = regexp.MustCompile(`^\{\{library:images/([a-z0-9][a-z0-9._-]*\.(?:webp|jpg|png|svg))\}\}$`)
	glibSAPageRe   = regexp.MustCompile(`^\{\{page:[a-z0-9-]+\}\}$`)
	glibSASuffixes = []string{"_widescreen", "_laptop", "_tablet_extra", "_tablet", "_mobile_extra", "_mobile"}
)

type glibSASection struct {
	Title     string            `json:"title"`
	Category  string            `json:"category"`
	Tags      []string          `json:"tags"`
	Thumbnail string            `json:"thumbnail"`
	Content   []json.RawMessage `json:"content"`

	slug, path string
	raw        []byte
}

type glibSAControl struct {
	Type       string          `json:"type"`
	Options    json.RawMessage `json:"options"`
	Responsive json.RawMessage `json:"responsive"`
	Fields     json.RawMessage `json:"fields"`
}

type glibSAControls map[string]*glibSAControl

func glibSAGz(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open(filepath.Join("gowebeditor", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// glibSAWidgetControls returns the controls of every widget (free config overlaid with Pro's, which
// is always on) plus "#container" and "#common" (the advanced tab every widget has).
func glibSAWidgetControls(t *testing.T) map[string]glibSAControls {
	t.Helper()
	out := map[string]glibSAControls{}
	for _, name := range []string{"widgets-config.json.gz", "pro-widgets-config.json.gz"} {
		var cfg map[string]json.RawMessage
		if err := json.Unmarshal(glibSAGz(t, name), &cfg); err != nil {
			t.Fatal(name, err)
		}
		for wt, raw := range cfg {
			var w struct {
				Controls glibSAControls `json:"controls"`
			}
			if json.Unmarshal(raw, &w) != nil || len(w.Controls) == 0 {
				continue // promotion stubs
			}
			out[wt] = w.Controls
		}
	}
	out["#common"] = out["common"]
	cont := glibSAControls{}
	for _, name := range []string{"doc-widgets.json.gz", "pro-doc-widgets.json.gz"} {
		var cfg map[string]json.RawMessage
		if err := json.Unmarshal(glibSAGz(t, name), &cfg); err != nil {
			t.Fatal(name, err)
		}
		var c struct {
			Controls glibSAControls `json:"controls"`
		}
		if err := json.Unmarshal(cfg["container"], &c); err != nil {
			t.Fatal(err)
		}
		for k, v := range c.Controls {
			cont[k] = v
		}
	}
	out["#container"] = cont
	if len(out["heading"]) == 0 || len(cont) == 0 || len(out["#common"]) == 0 {
		t.Fatal("widget configs did not load")
	}
	return out
}

func glibSALoad(t *testing.T) []*glibSASection {
	t.Helper()
	var out []*glibSASection
	for _, cat := range glibSACategories {
		files, _ := filepath.Glob(filepath.Join(glibSADir, "sections", cat, "*.json"))
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			s := &glibSASection{slug: strings.TrimSuffix(filepath.Base(f), ".json"), path: f, raw: raw}
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(s); err != nil {
				t.Errorf("%s: %v", f, err)
				continue
			}
			out = append(out, s)
		}
	}
	return out
}

// glibSAControl finds the control of a settings key, allowing responsive suffixes.
func glibSAFind(ctrls []glibSAControls, key string) *glibSAControl {
	for _, cs := range ctrls {
		if c := cs[key]; c != nil {
			return c
		}
	}
	for _, suf := range glibSASuffixes {
		if base, ok := strings.CutSuffix(key, suf); ok {
			for _, cs := range ctrls {
				if c := cs[base]; c != nil && len(c.Responsive) > 0 && string(c.Responsive) != "null" {
					return c
				}
			}
		}
	}
	return nil
}

func glibSAOptionOK(c *glibSAControl, v any) bool {
	if c.Type != "select" && c.Type != "choose" {
		return true
	}
	var opts map[string]any
	if json.Unmarshal(c.Options, &opts) != nil || len(opts) == 0 {
		return true
	}
	s, ok := v.(string)
	if !ok {
		return true
	}
	_, known := opts[s]
	return s == "" || known
}

func glibSAFields(c *glibSAControl) glibSAControls {
	out := glibSAControls{}
	if json.Unmarshal(c.Fields, &out) == nil && len(out) > 0 {
		return out
	}
	var list []struct {
		Name string `json:"name"`
		glibSAControl
	}
	if json.Unmarshal(c.Fields, &list) == nil {
		for i := range list {
			out[list[i].Name] = &list[i].glibSAControl
		}
	}
	return out
}

// glibSACheckSettings validates settings against controls; repeater items recurse into their fields.
func glibSACheckSettings(where string, settings map[string]any, ctrls []glibSAControls, errs *[]string) {
	for k, v := range settings {
		if k == "__globals__" {
			g, ok := v.(map[string]any)
			if !ok {
				*errs = append(*errs, where+": __globals__ is not an object")
				continue
			}
			for gk, gv := range g {
				if glibSAFind(ctrls, gk) == nil {
					*errs = append(*errs, fmt.Sprintf("%s: __globals__ key %q is not a control", where, gk))
				}
				m := glibSAGlobalRe.FindStringSubmatch(fmt.Sprint(gv))
				if m == nil || !glibSAGlobalIDs[m[2]] {
					*errs = append(*errs, fmt.Sprintf("%s: unknown global %s=%v", where, gk, gv))
				}
				if m != nil && (m[1] == "typography") != strings.HasSuffix(gk, "typography") {
					*errs = append(*errs, fmt.Sprintf("%s: global %s=%v has the wrong kind", where, gk, gv))
				}
			}
			continue
		}
		if k == "_id" {
			continue
		}
		if icon, ok := v.(map[string]any); ok && icon["library"] != nil && icon["value"] != nil {
			lib, val := fmt.Sprint(icon["library"]), fmt.Sprint(icon["value"])
			name := val[strings.LastIndex(val, " fa-")+4:]
			if _, known := glibSAIcons[lib][name]; !known || !strings.HasPrefix(val, "fa") {
				*errs = append(*errs, fmt.Sprintf("%s: icon %s %q is not in the renderer's icon set (Font Awesome 5)", where, k, val))
			}
		}
		c := glibSAFind(ctrls, k)
		if c == nil {
			*errs = append(*errs, fmt.Sprintf("%s: unknown setting %q", where, k))
			continue
		}
		if !glibSAOptionOK(c, v) {
			*errs = append(*errs, fmt.Sprintf("%s: %s=%v is not one of the control's options", where, k, v))
		}
		if c.Type == "repeater" {
			items, _ := v.([]any)
			fields := glibSAFields(c)
			for i, it := range items {
				m, _ := it.(map[string]any)
				glibSACheckSettings(fmt.Sprintf("%s.%s[%d]", where, k, i), m, []glibSAControls{fields}, errs)
			}
		}
	}
}

type glibSAElement struct {
	ID         string           `json:"id"`
	ElType     string           `json:"elType"`
	WidgetType string           `json:"widgetType"`
	IsInner    bool             `json:"isInner"`
	Settings   map[string]any   `json:"settings"`
	Elements   []*glibSAElement `json:"elements"`
}

func glibSAWalk(els []*glibSAElement, fn func(e *glibSAElement, depth int), depth int) {
	for _, e := range els {
		fn(e, depth)
		glibSAWalk(e.Elements, fn, depth+1)
	}
}

func glibSAElements(t *testing.T, s *glibSASection) []*glibSAElement {
	t.Helper()
	var els []*glibSAElement
	for _, raw := range s.Content {
		var e glibSAElement
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatalf("%s: %v", s.path, err)
		}
		els = append(els, &e)
	}
	return els
}

// glibSAIcons are the icons the front end prints as inline SVG (internal/builder/data/icons.json.gz);
// any other name would print an empty <i> with no icon font loaded.
var glibSAIcons map[string]map[string]json.RawMessage

func TestGolementorLibrarySA_Schema(t *testing.T) {
	ctrls := glibSAWidgetControls(t)
	f, err := os.Open("../builder/data/icons.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(zr).Decode(&glibSAIcons); err != nil || len(glibSAIcons["fa-solid"]) == 0 {
		t.Fatal("icon set", err)
	}
	f.Close()
	sections := glibSALoad(t)
	if len(sections) != 30 {
		t.Errorf("group SA has %d sections, want 30", len(sections))
	}
	perCat := map[string]int{}
	titles := map[string]string{}
	for _, s := range sections {
		var errs []string
		cat := filepath.Base(filepath.Dir(s.path))
		perCat[cat]++
		if strings.TrimSpace(s.Title) == "" || len(s.Tags) == 0 || len(s.Content) == 0 {
			errs = append(errs, "title, tags and content are required")
		}
		if prev, dup := titles[s.Title]; dup {
			errs = append(errs, "same title as "+prev)
		}
		titles[s.Title] = s.slug
		if !glibSAAllCategories[s.Category] || s.Category != cat {
			errs = append(errs, fmt.Sprintf("category %q (folder %q)", s.Category, cat))
		}
		if !regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`).MatchString(s.slug) {
			errs = append(errs, "slug is not kebab-case")
		}
		if s.Thumbnail != s.slug+".webp" {
			errs = append(errs, "thumbnail must be "+s.slug+".webp")
		} else if st, err := os.Stat(filepath.Join(filepath.Dir(s.path), s.Thumbnail)); err != nil || st.Size() == 0 {
			errs = append(errs, "thumbnail file missing")
		}
		for _, tok := range glibSATokenRe.FindAllString(string(s.raw), -1) {
			if m := glibSALibImgRe.FindStringSubmatch(tok); m != nil {
				if _, err := os.Stat(filepath.Join(glibSADir, "images", m[1])); err != nil {
					errs = append(errs, "missing library image "+m[1])
				}
			} else if !glibSAPageRe.MatchString(tok) {
				errs = append(errs, "malformed token "+tok)
			}
		}
		if strings.Contains(strings.ToLower(string(s.raw)), "lorem") {
			errs = append(errs, "placeholder copy")
		}
		els := glibSAElements(t, s)
		globals := 0
		glibSAWalk(els, func(e *glibSAElement, depth int) {
			where := fmt.Sprintf("%s#%s", e.WidgetType, e.ID)
			if e.ElType == "container" {
				where = "container#" + e.ID
			}
			if (e.ElType == "container" && (depth == 0) == e.IsInner) || (depth == 0 && e.ElType != "container") {
				errs = append(errs, where+": top level must be outer containers, everything else inner")
			}
			if g, ok := e.Settings["__globals__"].(map[string]any); ok {
				globals += len(g)
			}
			switch e.ElType {
			case "container":
				glibSACheckSettings(where, e.Settings, []glibSAControls{ctrls["#container"]}, &errs)
			case "widget":
				wc := ctrls[e.WidgetType]
				if wc == nil {
					errs = append(errs, "unknown widget "+e.WidgetType)
					return
				}
				if len(e.Elements) > 0 {
					errs = append(errs, where+": widgets have no children")
				}
				glibSACheckSettings(where, e.Settings, []glibSAControls{wc, ctrls["#common"]}, &errs)
			default:
				errs = append(errs, where+": elType "+e.ElType)
			}
		}, 0)
		if globals < 3 {
			errs = append(errs, fmt.Sprintf("only %d kit global references", globals))
		}
		for _, e := range errs {
			t.Errorf("%s: %s", s.path, e)
		}
	}
	want := map[string]int{"hero": 8, "features": 5, "about": 3, "services": 4, "cta": 4, "testimonials": 3, "pricing": 2, "team": 1}
	for cat, n := range want {
		if perCat[cat] != n {
			t.Errorf("category %s has %d sections, want %d", cat, perCat[cat], n)
		}
	}
}

// glibSAInsert replaces the library tokens like the insert endpoint does.
func glibSAInsert(raw []byte, base string) []byte {
	return glibSATokenRe.ReplaceAllFunc(raw, func(tok []byte) []byte {
		if m := glibSALibImgRe.FindSubmatch(tok); m != nil {
			return []byte(base + gweAssetsPath + "library/images/" + string(m[1]))
		}
		return []byte(base + "/")
	})
}

func TestGolementorLibrarySA_Render(t *testing.T) {
	t.Skip("old builder detached from routing, admin and front (R1c/R1d); this file goes in R2")
	sections := glibSALoad(t)
	ts, c := testServer(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, body, _ := get(t, c, base+"/wp-admin/post?post=2&action=web-builder")
	nonce := regexp.MustCompile(`"ajax":\{"nonce":"([^"]+)"`).FindStringSubmatch(body)
	if nonce == nil {
		t.Fatal("no elementor_ajax nonce")
	}
	imgOK := map[string]bool{}
	sort.Slice(sections, func(i, j int) bool { return sections[i].path < sections[j].path })
	for _, s := range sections {
		code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/pages", fmt.Sprintf(`{"title":%q,"status":"publish"}`, s.Title))
		var page struct {
			ID   int64  `json:"id"`
			Link string `json:"link"`
		}
		if code != http.StatusCreated && code != http.StatusOK || json.Unmarshal([]byte(b), &page) != nil || page.ID == 0 {
			t.Fatalf("create page for %s: %d %.200s", s.slug, code, b)
		}
		var content bytes.Buffer
		content.WriteByte('[')
		for i, el := range s.Content {
			if i > 0 {
				content.WriteByte(',')
			}
			content.Write(glibSAInsert(el, base))
		}
		content.WriteByte(']')
		actions := `{"s":{"action":"save_builder","data":{"status":"publish","elements":` + content.String() +
			`,"settings":{"post_title":` + fmt.Sprintf("%q", s.Title) + `,"post_status":"publish","template":"elementor_canvas"}}}}`
		resp, err := c.PostForm(base+"/wp-admin/admin-ajax", url.Values{"action": {"elementor_ajax"}, "_nonce": {nonce[1]},
			"editor_post_id": {fmt.Sprint(page.ID)}, "initial_document_id": {fmt.Sprint(page.ID)}, "actions": {actions}})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(raw), `"success":true`) {
			t.Errorf("%s: save_builder: %.300s", s.slug, raw)
			continue
		}

		code, html, _ := get(t, c, page.Link)
		if code != http.StatusOK {
			t.Errorf("%s: front end %d", s.slug, code)
			continue
		}
		for _, bad := range []string{"{{", "template:", "<no value>", "%!"} {
			if strings.Contains(html, bad) {
				t.Errorf("%s: page contains %q", s.slug, bad)
			}
		}
		els := glibSAElements(t, s)
		glibSAWalk(els, func(e *glibSAElement, _ int) {
			if !strings.Contains(html, `elementor-element-`+e.ID+` `) {
				t.Errorf("%s: element %s (%s%s) not rendered", s.slug, e.ID, e.ElType, e.WidgetType)
			}
			switch e.WidgetType {
			case "heading":
				if title, _ := e.Settings["title"].(string); !strings.Contains(html, glibSAHTMLText(title)) {
					t.Errorf("%s: heading %q missing", s.slug, title)
				}
			case "button":
				if text, _ := e.Settings["text"].(string); !strings.Contains(html, glibSAHTMLText(text)) {
					t.Errorf("%s: button %q missing", s.slug, text)
				}
			case "image":
				if !strings.Contains(html, `elementor-element-`+e.ID) || !strings.Contains(html, gweAssetsPath+"library/images/") {
					t.Errorf("%s: image %s has no library src", s.slug, e.ID)
				}
			}
		}, 0)
		for _, m := range regexp.MustCompile(`library/images/([a-z0-9._-]+)`).FindAllStringSubmatch(html, -1) {
			if !imgOK[m[1]] {
				if code, _, _ := get(t, c, base+gweAssetsPath+"library/images/"+m[1]); code != http.StatusOK {
					t.Errorf("%s: image %s: %d", s.slug, m[1], code)
				}
				imgOK[m[1]] = true
			}
		}
		css := regexp.MustCompile(`href=['"]([^'"]*/post-` + fmt.Sprint(page.ID) + `\.css[^'"]*)['"]`).FindStringSubmatch(html)
		if css == nil {
			t.Errorf("%s: no post stylesheet", s.slug)
			continue
		}
		if code, sheet, _ := get(t, c, strings.ReplaceAll(css[1], "&#038;", "&")); code != http.StatusOK || !strings.Contains(sheet, "var( --e-global-color-") {
			t.Errorf("%s: post stylesheet %d, uses kit colors: %v", s.slug, code, strings.Contains(sheet, "--e-global-color-"))
		}
	}
}

// glibSAHTMLText is how a plain text setting appears in the page (typographic quotes are kept).
func glibSAHTMLText(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	return s
}
