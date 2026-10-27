package server

// GoWebEditor v4 (atomic): the site side of the atomic element renderer (internal/builder/atomic).
//   - gweAtomicEnv gives the renderer global classes, variables, components and media;
//   - gweAtomicCSS writes the atomic stylesheets of the rendered documents (base, default,
//     global-<id>-<context>, local-<id>-<context>, one file per breakpoint, like upstream's
//     Atomic_Styles_Manager) and gweV4HeadLinks prints them;
//   - render_atomic_element (elementor_ajax) renders one element for the editor;
//   - the kit stylesheet gets the variables' :root custom properties.

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/builder"
	"wpgo/internal/builder/atomic"
	"wpgo/internal/store"
)

const (
	ctxGweAtomicCSS   = "gwe.atomicCSS"   // []atomic.CSSFile written for this page
	ctxGweAtomicFonts = "gwe.atomicFonts" // []string font families the atomic styles use
)

// gweV4PageFonts lists the font families the atomic styles of this page use (global classes,
// local styles, default styles, font variables), in first-use order. Upstream loads them from
// Google; here they go through the local font hosting (gweGoogleFontsHead) in gweV4HeadLinks.
func (s *Server) gweV4PageFonts(c *gin.Context) []string {
	s.gweAtomicCSS(c)
	if v, ok := c.Get(ctxGweAtomicFonts); ok {
		return v.([]string)
	}
	return nil
}

var gweV4HookOnce sync.Once

// gweAtomicEnv builds the atomic renderer's site callbacks. Classes, variables and components are
// loaded lazily, once per environment.
func (s *Server) gweAtomicEnv(ctx context.Context, preview bool) *atomic.Env {
	gweV4HookOnce.Do(func() {
		gweV4OnChange = func() { s.gweClearCSS(); s.gweClearAtomicCSS(); s.gweV4DSSDelete() }
	})
	lb := s.links(ctx)
	env := &atomic.Env{AssetsURL: lb.HomeURL(gweAssetsPath)}
	var kitID int64
	if kit, err := s.gweKit(ctx); err == nil {
		kitID = kit.ID
	}
	var classesOnce, varsOnce sync.Once
	var classes *atomic.GlobalClasses
	var vars map[string]atomic.Variable
	env.ClassLabel = func(id string) (string, bool) {
		classesOnce.Do(func() { classes = s.gweAtomicClasses(ctx, kitID, preview) })
		return classes.Label(id)
	}
	env.Variable = func(id string) (atomic.Variable, bool) {
		varsOnce.Do(func() { vars, _ = s.gweAtomicVariables(ctx, kitID) })
		v, ok := vars[id]
		return v, ok
	}
	env.Component = func(id int64) ([]byte, bool) {
		p := s.gweV4Component(ctx, id)
		if p == nil || (!preview && p.Status != "publish") {
			return nil, false
		}
		return gweElementsRaw(p), true
	}
	env.Permalink = func(id int64) string {
		p, err := s.store.GetPost(ctx, id)
		if err != nil || p.Status == "trash" {
			return ""
		}
		return lb.Permalink(p)
	}
	env.AttachmentURL = func(id int64) string {
		a, err := s.store.GetAttachment(ctx, id)
		if err != nil {
			return ""
		}
		return s.uploadURL(ctx, a.FilePath)
	}
	env.Image = func(id int64, size string) (atomic.Image, bool) {
		a, err := s.store.GetAttachment(ctx, id)
		if err != nil {
			return atomic.Image{}, false
		}
		img := atomic.Image{URL: s.uploadURL(ctx, a.FilePath), Width: a.Width, Height: a.Height, Alt: a.Alt}
		var srcset []string
		if strings.HasPrefix(a.Mime, "image/") && a.Mime != "image/svg+xml" {
			if a.Width > 0 {
				srcset = append(srcset, img.URL+" "+strconv.Itoa(a.Width)+"w")
			}
			for _, v := range a.Variants {
				if v.Width > 0 && v.Height > 0 && a.Width > 0 && a.Height > 0 && v.Width*a.Height == v.Height*a.Width {
					srcset = append(srcset, s.uploadURL(ctx, v.FilePath)+" "+strconv.Itoa(v.Width)+"w")
				}
			}
		}
		if size != "" && size != "full" {
			if v := a.Variant(size); v != nil {
				img.URL, img.Width, img.Height = s.uploadURL(ctx, v.FilePath), v.Width, v.Height
			}
		}
		if len(srcset) > 1 {
			img.Srcset = strings.Join(srcset, ", ")
		}
		return img, true
	}
	env.SVG = func(id int64, url string) string {
		if id > 0 && builder.SVGLoader != nil {
			if svg := builder.SVGLoader(id); svg != "" {
				return svg
			}
		}
		return s.gweLocalSVG(ctx, url)
	}
	return env
}

// gweLocalSVG reads an SVG served by this site (editor assets or uploads); remote SVGs are not
// fetched.
func (s *Server) gweLocalSVG(ctx context.Context, url string) string {
	lb := s.links(ctx)
	assets := lb.HomeURL(gweAssetsPath)
	for _, prefix := range []string{assets, gweAssetsPath, "http://localhost:8095/wp-content/plugins/elementor/assets/"} {
		if strings.HasPrefix(url, prefix) {
			rel := filepath.Clean("/" + strings.SplitN(strings.TrimPrefix(url, prefix), "?", 2)[0])
			if !strings.HasSuffix(rel, ".svg") {
				return ""
			}
			f, err := s.web.Open("gowebeditor" + rel)
			if err != nil {
				return ""
			}
			defer f.Close()
			var b strings.Builder
			buf := make([]byte, 32<<10)
			for b.Len() < 1<<20 {
				n, err := f.Read(buf)
				b.Write(buf[:n])
				if err != nil {
					break
				}
			}
			return b.String()
		}
	}
	uploads := lb.HomeURL("/wp-content/uploads/")
	if strings.HasPrefix(url, uploads) {
		rel := filepath.Clean("/" + strings.SplitN(strings.TrimPrefix(url, uploads), "?", 2)[0])
		if !strings.HasSuffix(rel, ".svg") {
			return ""
		}
		if st, err := os.Stat(filepath.Join(s.cfg.UploadsDir, rel)); err != nil || st.Size() > 1<<20 {
			return ""
		}
		b, _ := os.ReadFile(filepath.Join(s.cfg.UploadsDir, rel))
		return string(b)
	}
	return ""
}

func (s *Server) gweAtomicClasses(ctx context.Context, kitID int64, preview bool) *atomic.GlobalClasses {
	out := &atomic.GlobalClasses{}
	list, err := s.gweV4GlobalClasses(ctx, kitID, preview)
	if err != nil {
		return out
	}
	for _, g := range list {
		var variants []any
		for _, v := range g.Variants {
			variants = append(variants, atomic.FromAny(v))
		}
		out.Items = append(out.Items, atomic.GlobalClass{ID: g.ID, Label: g.Label, Variants: variants})
	}
	return out
}

// gweAtomicVariables returns the kit's variables and their ids in stored order (by "order").
func (s *Server) gweAtomicVariables(ctx context.Context, kitID int64) (map[string]atomic.Variable, []string) {
	data, _, err := s.gweV4Variables(ctx, kitID)
	if err != nil {
		return map[string]atomic.Variable{}, nil
	}
	out := map[string]atomic.Variable{}
	ids := make([]string, 0, len(data))
	for id, v := range data {
		val := v["value"]
		if _, ok := val.(map[string]any); ok {
			val = atomic.VariableValue(atomic.FromAny(val))
		}
		label, _ := v["label"].(string)
		typ, _ := v["type"].(string)
		deleted, _ := v["deleted"].(bool)
		out[id] = atomic.Variable{Label: label, Type: typ, Value: fmt.Sprint(val), Deleted: deleted}
		ids = append(ids, id)
	}
	order := func(id string) float64 {
		f, _ := data[id]["order"].(float64)
		return f
	}
	sort.SliceStable(ids, func(i, j int) bool {
		if order(ids[i]) != order(ids[j]) {
			return order(ids[i]) < order(ids[j])
		}
		return ids[i] < ids[j]
	})
	return out, ids
}

// gweV4KitCSS is the variables' :root block the kit stylesheet ends with.
func (s *Server) gweV4KitCSS(ctx context.Context) string {
	kit, err := s.gweKit(ctx)
	if err != nil {
		return ""
	}
	vars, ids := s.gweAtomicVariables(ctx, kit.ID)
	var live []string
	for _, id := range ids {
		if !vars[id].Deleted {
			live = append(live, id)
		}
	}
	return atomic.VariablesCSS(live, vars)
}

// --- stylesheets -------------------------------------------------------------------------------

func (s *Server) gweAtomicCSSDir() string { return filepath.Join(s.cfg.UploadsDir, "elementor", "css") }

// gweClearAtomicCSS drops every generated atomic stylesheet (they are rebuilt on the next view).
func (s *Server) gweClearAtomicCSS() {
	entries, _ := os.ReadDir(s.gweAtomicCSSDir())
	for _, e := range entries {
		n := e.Name()
		for _, p := range []string{"base-", "default-", "global-", "local-"} {
			if strings.HasPrefix(n, p) && strings.HasSuffix(n, ".css") {
				_ = os.Remove(filepath.Join(s.gweAtomicCSSDir(), n))
			}
		}
	}
}

// gweAtomicCSS generates the atomic stylesheets for the documents rendered on this page and
// returns them in enqueue order (cached on the request).
func (s *Server) gweAtomicCSS(c *gin.Context) []atomic.CSSFile {
	if v, ok := c.Get(ctxGweAtomicCSS); ok {
		return v.([]atomic.CSSFile)
	}
	files := s.gweAtomicCSSFor(c, gweResults(c), gwePreviewing(c))
	c.Set(ctxGweAtomicCSS, files)
	return files
}

func (s *Server) gweAtomicCSSFor(c *gin.Context, results []gweRendered, preview bool) []atomic.CSSFile {
	context := "frontend"
	if preview {
		context = "preview"
	}
	kit, err := s.gweKit(c)
	if err != nil {
		return nil
	}
	env := s.gweAtomicEnv(c, preview)
	classes := s.gweAtomicClasses(c, kit.ID, preview)
	var globals, locals []atomic.StyleSource
	hasAtomic := false
	seenLocal := map[int64]bool{}
	for _, r := range results {
		if r.post == nil {
			continue
		}
		nodes, err := atomic.ParseNodes(gweElementsRaw(r.post))
		if err != nil {
			continue
		}
		if atomic.HasAtomic(nodes) {
			hasAtomic = true
			if !preview {
				gweV4FormCacheHints(c, nodes)
			}
		}
		// the document and the components it embeds (their styles join the document's files)
		ids := atomic.ClassIDs(nodes)
		type doc struct {
			id    int64
			nodes []*atomic.Node
		}
		docs := []doc{{r.post.ID, nodes}}
		queue := atomic.ComponentIDs(nodes)
		seenComp := map[int64]bool{}
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if seenComp[id] {
				continue
			}
			seenComp[id] = true
			p := s.gweV4Component(c, id)
			if p == nil {
				continue
			}
			cn, err := atomic.ParseNodes(gweElementsRaw(p))
			if err != nil {
				continue
			}
			docs = append(docs, doc{id, cn})
			ids = append(ids, atomic.ClassIDs(cn)...)
			queue = append(queue, atomic.ComponentIDs(cn)...)
		}
		globals = append(globals, atomic.StyleSource{Key: "global-" + itoa64(r.post.ID) + "-" + context, Styles: classes.StylesFor(ids)})
		for _, d := range docs {
			if seenLocal[d.id] {
				continue
			}
			seenLocal[d.id] = true
			locals = append(locals, atomic.StyleSource{Key: "local-" + itoa64(d.id) + "-" + context, Styles: atomic.LocalStyles(d.nodes)})
		}
	}
	if !hasAtomic && !preview {
		return nil
	}
	var defaults []*atomic.Map
	if ds, err := s.gweV4DefaultStyles(c, kit.ID); err == nil {
		tags := make([]string, 0, len(ds))
		for t := range ds {
			tags = append(tags, t)
		}
		sort.Strings(tags)
		for _, t := range tags {
			if m, ok := atomic.FromAny(ds[t]).(*atomic.Map); ok {
				defaults = append(defaults, m)
			}
		}
	}
	sources := append([]atomic.StyleSource{{Key: "base", Styles: atomic.BaseStyles()}, {Key: "default", Styles: defaults}}, globals...)
	sources = append(sources, locals...)
	rc := s.gweRenderContext(c, preview)
	var bps []atomic.Breakpoint
	for _, b := range rc.Breakpoints {
		bps = append(bps, atomic.Breakpoint{Name: b.Name, Value: b.Value, Direction: b.Direction})
	}
	var fonts []string
	seenFont := map[string]bool{}
	env.Font = func(family string) {
		if k := strings.ToLower(family); !seenFont[k] {
			seenFont[k] = true
			fonts = append(fonts, family)
		}
	}
	files := env.CSSFiles(sources, bps)
	c.Set(ctxGweAtomicFonts, fonts)
	dir := s.gweAtomicCSSDir()
	_ = os.MkdirAll(dir, 0o755)
	for i := range files {
		files[i].CSS = s.gweFixURLs(c, files[i].CSS)
		path := filepath.Join(dir, files[i].Handle+".css")
		if old, err := os.ReadFile(path); err == nil && string(old) == files[i].CSS {
			continue
		}
		tmp := path + ".tmp" + strconv.FormatInt(time.Now().UnixNano(), 36)
		if os.WriteFile(tmp, []byte(files[i].CSS), 0o644) == nil {
			_ = os.Rename(tmp, path)
		}
	}
	return files
}

// gweV4HeadLinks prints the inline atomic CSS and the atomic stylesheet links (after the kit
// stylesheet, like upstream's elementor/frontend/after_enqueue_post_styles).
func (s *Server) gweV4HeadLinks(c *gin.Context) string {
	dss := s.gweV4DSSLink(c)
	files := s.gweAtomicCSS(c)
	if len(files) == 0 {
		return dss
	}
	var sb strings.Builder
	sb.WriteString(dss)
	sb.WriteString(s.gweGoogleFontsHead(c, s.gweV4PageFonts(c)))
	sb.WriteString("<style id=\"elementor-frontend-inline-css\">\n" + atomic.FrontendInlineCSS + "\n</style>\n")
	lb := s.links(c)
	for _, f := range files {
		ver := "0"
		if st, err := os.Stat(filepath.Join(s.gweAtomicCSSDir(), f.Handle+".css")); err == nil {
			ver = strconv.FormatInt(st.ModTime().UnixNano(), 36)
		}
		href := lb.HomeURL("/wp-content/uploads/elementor/css/" + f.Handle + ".css?ver=" + ver)
		media := atomic.MediaAttr(f.Media)
		sb.WriteString(fmt.Sprintf("<link rel='stylesheet' id='%s-css' href='%s' media='%s' />\n", f.Handle, html.EscapeString(href), html.EscapeString(media)))
	}
	return sb.String()
}

// gweV4PreviewScripts are the atomic handlers the editor preview always loads.
var gweV4PreviewScripts = []string{"elementor-v2-frontend-handlers", "elementor-v2-action-link-handlers", "elementor-youtube-handler",
	"elementor-v2-alpinejs", "elementor-tabs-handler", "elementor-tabs-preview-handler", "elementor-accordion-preview-handler",
	"elementor-background-video-handler", "elementor-background-video-preview-handler", "elementor-v2-form-handlers"}

// gweV4FooterScripts prints the atomic elements' frontend handlers (before the frontend scripts).
func (s *Server) gweV4FooterScripts(c *gin.Context) string {
	var handles []string
	if gwePreviewing(c) {
		handles = gweV4PreviewScripts
	} else {
		for _, r := range gweResults(c) {
			if r.res != nil {
				handles = append(handles, builder.AtomicScripts(r.res.Scripts)...)
			}
		}
	}
	assets := s.links(c).HomeURL(gweAssetsPath)
	ver := "?ver=" + gweVersion + "-" + Version
	seen := map[string]bool{}
	var sb strings.Builder
	for _, h := range handles {
		if seen[h] || atomic.ScriptFiles[h] == "" {
			continue
		}
		seen[h] = true
		sb.WriteString(gweScript(h, assets+atomic.ScriptFiles[h]+ver))
	}
	return sb.String()
}

// --- render_atomic_element ---------------------------------------------------------------------

// gweRenderAtomic is the render_atomic_element action: {data: element} → {render: html}.
func (s *Server) gweRenderAtomic(c *gin.Context, raw json.RawMessage, editorID int64) (any, error) {
	p, err := s.gwePostFor(c, editorID, "edit_post")
	if err != nil {
		return nil, err
	}
	var req struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(raw, &req)
	if len(req.Data) == 0 || req.Data[0] != '{' {
		return nil, gweFail(http.StatusBadRequest, "Invalid request payload.")
	}
	els, err := builder.ParseElements(append(append([]byte("["), req.Data...), ']'))
	if err != nil || len(els) != 1 || els[0] == nil || els[0].ElType == "" {
		return nil, gweFail(http.StatusBadRequest, "Element could not be instantiated.")
	}
	out, err := builder.RenderWidget(els[0], gweDocument(p), s.gweRenderContext(c, true))
	if err != nil {
		return nil, gweFail(http.StatusInternalServerError, err.Error())
	}
	return map[string]any{"render": s.gweFixURLs(c, out)}, nil
}

// gweV4Editable reports whether a post is a component (edited in GoWebEditor too).
func gweV4Editable(p *store.Post) bool { return p != nil && p.Type == gweComponentType }

// gweV4UserCaps are the atomic capabilities upstream grants by role (atomic-widgets and
// global-classes Add_Capabilities migrations); the v2 panels hide the style tab and the class
// manager without them.
func gweV4UserCaps(u *store.User) []string {
	if u == nil {
		return nil
	}
	has := func(roles ...string) bool {
		for _, r := range u.Roles {
			for _, x := range roles {
				if r == x {
					return true
				}
			}
		}
		return false
	}
	var out []string
	if has("administrator", "editor", "author", "contributor", "shop_manager") || u.Can("manage_options", nil) {
		out = append(out, "elementor_atomic_widgets_access_styles_tab", "elementor_atomic_widgets_edit_local_css_class")
	}
	if has("administrator") || u.Can("manage_options", nil) {
		out = append(out, "elementor_global_classes_update_class")
	}
	if has("administrator", "editor", "author", "contributor", "shop_manager") || u.Can("manage_options", nil) {
		out = append(out, "elementor_global_classes_remove_class", "elementor_global_classes_apply_class")
	}
	return out
}

// gweV4TierFeatures are the license "tier features" the Pro editor packages ask for
// (elementor-pro/v1/license/tier-features): every feature is simply enabled in GoWebEditor.
// Without "form" the documents-extended package blocks creating e-form and its fields, without
// "atomic-components" the components-extended package hides "save as component".
var gweV4TierFeatures = []string{"form", "atomic-components", "atomic-loop", "display-conditions", "custom-css",
	"custom-attributes", "atomic-custom-css", "atomic-custom-attributes", "pro-interactions", "size-variable",
	"transitions", "theme-builder", "global-widget", "custom_code", "editor_comments", "form-submissions"}
