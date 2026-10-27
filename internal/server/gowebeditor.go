package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/blocks"
	"wpgo/internal/builder"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Golementor is WordPress Go's page builder. Its editor UI is upstream 4.3.5's GPL-3.0 front end
// (the built JS/CSS under web/gowebeditor, served at /wp-content/plugins/gowebeditor/assets/), rebranded
// and talking to this Go back end: the editor page and preview (this file), the elementor_ajax protocol
// (gowebeditor_ajax.go), the REST routes the editor shell calls (gowebeditor_rest.go), the site kit
// (gowebeditor_kit.go) and the front end (gowebeditor_front.go). Rendering and CSS generation live in
// internal/builder.
//
// Storage follows the upstream plugin so imports keep working: post meta _elementor_edit_mode=builder,
// _elementor_data (the element tree as a JSON string), _elementor_page_settings, _elementor_template_type,
// _elementor_version, the page template in _wp_page_template, and a plain-HTML fallback in posts.content.
//
// The data in internal/server/gowebeditor/ is captured from the upstream editor with the v4/atomic
// experiments off and rebranded (tools: scratchpad gwe-tools/gen_data.py): the editor shell with its 188
// Backbone templates, the editor config without the document, the per-type document configs, the widget
// controls and the i18n strings that name the upstream product.

//go:embed gowebeditor/*.gz gowebeditor/preview-styles.json
var gweFS embed.FS

const (
	gweVersion    = "4.3.5" // upstream editor build
	gweAssetsPath = "/wp-content/plugins/gowebeditor/assets/"
	gweLibrary    = "elementor_library" // content type of templates and the kit
)

var gweOnce sync.Once
var gwe gweStore

// gweStore holds the decoded editor data.
type gweStore struct {
	shell         string
	editorCfg     map[string]json.RawMessage
	commonCfg     map[string]any
	appCfg        json.RawMessage
	frontCfg      map[string]any
	docs          map[string]map[string]json.RawMessage // by document type
	docWidgets    json.RawMessage
	widgetsCfg    json.RawMessage
	i18n          json.RawMessage
	previewStyles [][2]string
}

func gweRead(name string) []byte {
	f, err := gweFS.Open("gowebeditor/" + name)
	if err != nil {
		panic("gowebeditor data: " + err.Error())
	}
	defer f.Close()
	if !strings.HasSuffix(name, ".gz") {
		b, _ := io.ReadAll(f)
		return b
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		panic("gowebeditor data: " + err.Error())
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		panic("gowebeditor data: " + err.Error())
	}
	return b
}

func gweData() *gweStore {
	gweOnce.Do(func() {
		must := func(err error) {
			if err != nil {
				panic("gowebeditor data: " + err.Error())
			}
		}
		gwe.shell = string(gweRead("editor-shell.html.gz"))
		must(json.Unmarshal(gweRead("editor-config.json.gz"), &gwe.editorCfg))
		must(json.Unmarshal(gweRead("common-config.json.gz"), &gwe.commonCfg))
		gwe.appCfg = gweRead("app-config.json.gz")
		must(json.Unmarshal(gweRead("frontend-config.json.gz"), &gwe.frontCfg))
		gwe.docs = map[string]map[string]json.RawMessage{}
		for _, t := range []string{"wp-page", "wp-post", "kit", "section"} {
			m := map[string]json.RawMessage{}
			must(json.Unmarshal(gweRead("doc-"+t+".json.gz"), &m))
			gwe.docs[t] = m
		}
		gwe.docWidgets = gweRead("doc-widgets.json.gz")
		gwe.widgetsCfg = gweRead("widgets-config.json.gz")
		gwe.i18n = gweRead("i18n-rebrand.json.gz")
		must(json.Unmarshal(gweRead("preview-styles.json"), &gwe.previewStyles))
		gweProLoad(&gwe) // Golementor Pro: Pro's widgets, document types and editor bundle (gowebeditor_pro.go)
		gweV4Apply(&gwe) // Golementor v4 (atomic): gowebeditor_v4_data.go
	})
	return &gwe
}

var gwePlaceholder = regexp.MustCompile(`@@(HOME|ASSETS|GWEASSETS|VER|GWEVER|MAXUPLOAD|MAXUPLOADMB|NONCE:[A-Za-z0-9_\-]+)@@`)

// gweFill swaps the data files' placeholders for this site and visitor.
func (s *Server) gweFill(c *gin.Context, text string) string {
	text = s.gweV4Fill(c, text) // gap-atomic: the atomic form's site e-mail defaults
	home := s.links(c).Home
	return gwePlaceholder.ReplaceAllStringFunc(text, func(m string) string {
		key := m[2 : len(m)-2]
		switch key {
		case "HOME":
			return home
		case "ASSETS", "GWEASSETS":
			return home + gweAssetsPath
		case "VER":
			return gweVersion
		case "GWEVER": // Golementor's own (patched) assets: a new wpgo build must bust browser caches
			return gweVersion + "-" + Version
		case "MAXUPLOAD":
			return strconv.Itoa(maxUploadBytes)
		case "MAXUPLOADMB":
			return strconv.Itoa(maxUploadBytes >> 20)
		}
		return s.nonce(c, strings.TrimPrefix(key, "NONCE:"))
	})
}

// gweJSON marshals a config without HTML escaping (the editor's inline scripts print it raw).
func gweJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	out := strings.TrimSuffix(buf.String(), "\n")
	// </script> inside a string must not end the inline script.
	return strings.ReplaceAll(out, "</", `<\/`)
}

// --- documents --------------------------------------------------------------------------------

// gweBuilt reports whether a post is edited with Golementor (_elementor_edit_mode = builder).
func gweBuilt(p *store.Post) bool {
	if p == nil {
		return false
	}
	v, _ := p.Meta["_elementor_edit_mode"].(string)
	return v == "builder"
}

// gweDocType is the document type of a post (_elementor_template_type, or wp-page/wp-post).
func gweDocType(p *store.Post) string {
	if t, _ := p.Meta["_elementor_template_type"].(string); t != "" {
		return t
	}
	if p.Type == "page" {
		return "wp-page"
	}
	return "wp-post"
}

// gweElementsRaw returns the saved element tree as JSON (stored as a JSON string, like upstream).
func gweElementsRaw(p *store.Post) json.RawMessage {
	switch v := p.Meta["_elementor_data"].(type) {
	case string:
		if json.Valid([]byte(v)) && strings.HasPrefix(strings.TrimSpace(v), "[") {
			return json.RawMessage(v)
		}
	case []any:
		b, _ := json.Marshal(v)
		return b
	}
	return json.RawMessage("[]")
}

func gwePageSettings(p *store.Post) map[string]any {
	out := map[string]any{}
	switch v := p.Meta["_elementor_page_settings"].(type) {
	case map[string]any:
		for k, x := range v {
			out[k] = x
		}
	case string:
		_ = json.Unmarshal([]byte(v), &out)
	}
	return out
}

// gweDocument turns a post into the renderer's document.
func gweDocument(p *store.Post) *builder.Document {
	els, _ := builder.ParseElements(gweElementsRaw(p))
	doc := &builder.Document{ID: p.ID, Type: gweDocType(p), Elements: els, Settings: gwePageSettings(p)}
	doc.PostType = gweProPostType(p) // Golementor Pro: data-elementor-post-type
	return doc
}

// gweEditable lists the content types Golementor edits.
func gweEditable(p *store.Post) bool {
	return p != nil && (p.Type == "post" || p.Type == "page" || p.Type == gweLibrary || p.Type == gwePartType || gweV4Editable(p)) // + Golementor v4 components
}

// gweEditURL is the editor URL for a post.
func gweEditURL(id int64) string {
	return "/wp-admin/post?post=" + itoa64(id) + "&action=elementor"
}

var gweStatusLabels = map[string]string{"publish": "Published", "draft": "Draft", "pending": "Pending", "private": "Private",
	"future": "Scheduled", "auto-draft": "Draft", "inherit": "Revision", "trash": "Trash"}

// gwePreviewURL is the preview iframe URL: the post's permalink with ?elementor-preview.
func (s *Server) gwePreviewURL(c *gin.Context, p *store.Post) string {
	link := s.links(c).Permalink(p)
	return link + previewSep(link) + "preview=true&elementor-preview=" + itoa64(p.ID) + "&ver=" + strconv.FormatInt(time.Now().Unix(), 10)
}

func (s *Server) gweLastEdited(c *gin.Context, p *store.Post, autosave bool) string {
	name := ""
	if u, err := s.store.UserByID(c, p.AuthorID); err == nil {
		name = u.DisplayName
	}
	t := p.ModifiedAt.In(s.loc())
	if autosave {
		t = time.Now().In(s.loc())
		return "Draft saved on <time>" + wp.FormatDate(t, "M j, H:i") + "</time> by " + html.EscapeString(name)
	}
	return "Last edited on <time>" + wp.FormatDate(t, "M j, H:i") + "</time> by " + html.EscapeString(name)
}

// gweDocURLs is the document's urls block.
func (s *Server) gweDocURLs(c *gin.Context, p *store.Post) map[string]string {
	lb := s.links(c)
	link := lb.Permalink(p)
	all := "/wp-admin/edit?post_type=" + p.Type
	if p.Type == "post" {
		all = "/wp-admin/edit"
	}
	look := link
	if p.Type == gweLibrary && gweDocType(p) == "kit" {
		look = ""
	}
	return map[string]string{
		"exit_to_dashboard": lb.HomeURL("/wp-admin/post?post=" + itoa64(p.ID) + "&action=edit"),
		"all_post_type":     lb.HomeURL(all),
		"preview":           s.gwePreviewURL(c, p),
		"wp_preview":        link + previewSep(link) + "preview_id=" + itoa64(p.ID) + "&preview=true",
		"permalink":         link,
		"have_a_look":       look,
		"main_dashboard":    lb.HomeURL("/wp-admin/"),
	}
}

// gweSettingsValues is the document's settings model: saved page settings plus the post fields upstream
// exposes as settings (title, status, excerpt, featured image, template, ...).
func (s *Server) gweSettingsValues(c *gin.Context, p *store.Post, base json.RawMessage) map[string]any {
	vals := map[string]any{}
	_ = json.Unmarshal(base, &vals)
	for k, v := range gwePageSettings(p) {
		vals[k] = v
	}
	typ := gweDocType(p)
	if typ == "kit" {
		vals["post_title"] = p.Title
		vals["site_name"] = s.settings.String("blogname")
		vals["site_description"] = s.settings.String("blogdescription")
		return vals
	}
	vals["post_title"] = p.Title
	vals["post_status"] = p.Status
	if p.Status == "auto-draft" {
		vals["post_status"] = "draft"
	}
	if tpl, _ := p.Meta["_wp_page_template"].(string); tpl != "" {
		vals["template"] = tpl
	} else {
		vals["template"] = "default"
	}
	if p.Type == "post" {
		vals["post_excerpt"] = p.Excerpt
	}
	if p.Type == "page" {
		vals["menu_order"] = p.MenuOrder
	}
	vals["comment_status"] = p.CommentStatus
	img := map[string]any{"url": "", "id": 0, "size": ""}
	if p.FeaturedMediaID > 0 {
		if a, err := s.store.GetAttachment(c, p.FeaturedMediaID); err == nil {
			img = map[string]any{"url": s.uploadURL(c, a.FilePath), "id": a.ID, "size": ""}
		}
	}
	vals["post_featured_image"] = img
	return vals
}

// gweDocConfig is the document config (initial_document, get_document_config).
func (s *Server) gweDocConfig(c *gin.Context, p *store.Post) map[string]any {
	d := gweData()
	typ := gweDocType(p)
	base, ok := d.docs[typ]
	if !ok {
		switch {
		case p.Type == "post":
			base = d.docs["wp-post"]
		case typ == "page" || p.Type == "page":
			base = d.docs["wp-page"]
		default:
			base = d.docs["section"]
		}
	}
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	var settings map[string]json.RawMessage
	_ = json.Unmarshal(base["settings"], &settings)
	st := map[string]any{}
	for k, v := range settings {
		st[k] = v
	}
	st["settings"] = s.gweSettingsValues(c, p, settings["settings"])
	out["settings"] = st
	out["id"] = p.ID
	out["type"] = typ
	out["version"] = gweVersion
	out["last_edited"] = s.gweLastEdited(c, p, false)
	out["urls"] = s.gweDocURLs(c, p)
	label := gweStatusLabels[p.Status]
	status := p.Status
	if status == "auto-draft" {
		status = "draft"
	}
	out["status"] = map[string]string{"value": status, "label": label}
	out["revisions"] = map[string]any{"enabled": true, "current_id": p.ID}
	u := currentUser(c)
	out["user"] = map[string]any{"can_publish": u.Can("publish_post", p) || u.Can("publish_"+gwePlural(p), nil), "locked": false}
	if typ == "kit" {
		delete(out, "elements") // the kit has no canvas of its own
	} else {
		out["elements"] = s.gweInitialElements(c, p)
		out["widgets"] = d.docWidgets
	}
	if p.Type == "page" {
		out["post_type_title"] = "Page"
	} else if p.Type == "post" {
		out["post_type_title"] = "Post"
	} else {
		out["post_type_title"] = "Template"
	}
	s.gweThemeDocConfig(c, p, out) // Golementor theme building: Site widgets, theme part panels
	s.gweProDocConfig(c, p, out)   // Golementor Pro: conditions block, popup display settings
	s.gweUsedWidgetControls(c, out)
	return out
}

// gweUsedWidgetControls gives the widgets used in the document their full controls in the document
// config, like upstream (Document::get_config's used-widget config). The rest of the controls only
// arrive later with get_widgets_config, but the preview's handlers read the frontend_available
// controls of the widgets on the page as soon as the preview loads: without them a Video widget
// already on the page never gets its player in the editor.
func (s *Server) gweUsedWidgetControls(c *gin.Context, out map[string]any) {
	elems, err := json.Marshal(out["elements"])
	if err != nil {
		return
	}
	var tree []map[string]any
	if json.Unmarshal(elems, &tree) != nil {
		return
	}
	used := map[string]bool{}
	var walk func([]map[string]any)
	walk = func(list []map[string]any) {
		for _, el := range list {
			if t, _ := el["widgetType"].(string); t != "" {
				used[t] = true
			}
			if kids, ok := el["elements"].([]any); ok {
				var sub []map[string]any
				for _, k := range kids {
					if m, ok := k.(map[string]any); ok {
						sub = append(sub, m)
					}
				}
				walk(sub)
			}
		}
	}
	walk(tree)
	if len(used) == 0 {
		return
	}
	var full map[string]map[string]json.RawMessage
	if json.Unmarshal(s.gweWidgetsConfig(c), &full) != nil {
		return
	}
	docRaw, err := json.Marshal(out["widgets"])
	if err != nil {
		return
	}
	widgets, err := gweOrderedRaw(docRaw) // keeps the panel's widget order
	if err != nil {
		return
	}
	for i := range widgets {
		ctl, ok := full[widgets[i].K]["controls"]
		if !used[widgets[i].K] || !ok {
			continue
		}
		if w, err := gweOrderedRaw(widgets[i].V); err == nil {
			if b, err := json.Marshal(w.set("controls", ctl)); err == nil {
				widgets[i].V = b
			}
		}
	}
	if b, err := json.Marshal(widgets); err == nil {
		out["widgets"] = json.RawMessage(b)
	}
}

func gwePlural(p *store.Post) string {
	if p.Type == "page" {
		return "pages"
	}
	return "posts"
}

// gweInitialElements is the tree the editor opens with. A post that was never edited with Golementor
// but has content starts with that content in a Text Editor widget, like the upstream converter.
func (s *Server) gweInitialElements(c *gin.Context, p *store.Post) json.RawMessage {
	if raw := s.gweV4NewerAutosave(c, p); raw != nil { // gap-atomic: newer autosave (get_doc_or_auto_save)
		return s.gweWithHTMLCache(c, p, raw)
	}
	if gweBuilt(p) || strings.TrimSpace(p.Content) == "" {
		return s.gweWithHTMLCache(c, p, gweElementsRaw(p))
	}
	content := p.Content
	if blocks.HasBlocks(content) {
		content = blocks.Render(c.Request.Context(), content)
	} else {
		content = wp.Autop(content)
	}
	tree := []map[string]any{{
		"id": gweNewID(), "elType": "container", "isInner": false, "settings": map[string]any{},
		"elements": []map[string]any{{
			"id": gweNewID(), "elType": "widget", "widgetType": "text-editor", "isInner": false,
			"settings": map[string]any{"editor": content}, "elements": []any{},
		}},
	}}
	b, _ := json.Marshal(tree)
	return b
}

// gweWithHTMLCache adds each widget's server render as htmlCache, like upstream's editor data: the
// editor paints saved widgets from it before their controls (and so their defaults) are loaded. The
// tree is rendered from builder.ParseElements and the caches are spliced into the saved JSON, so the
// settings keep their order.
func (s *Server) gweWithHTMLCache(c *gin.Context, p *store.Post, raw json.RawMessage) json.RawMessage {
	s.gweThemeSample(c, p) // theme parts render their Site widgets with sample content
	els, err := builder.ParseElements(raw)
	if err != nil || len(els) == 0 {
		return raw
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return raw
	}
	out := compact.String()
	doc := gweDocument(p)
	rc := s.gweRenderContext(c, true)
	var walk func(list []*builder.Element)
	walk = func(list []*builder.Element) {
		for _, el := range list {
			if el == nil {
				continue
			}
			if el.ElType == "widget" && el.ID != "" {
				if h, err := builder.RenderWidget(el, doc, rc); err == nil && h != "" {
					key, _ := json.Marshal(el.ID)
					cache, _ := json.Marshal(s.gweFixURLs(c, h))
					needle := `"id":` + string(key) + `,`
					out = strings.Replace(out, needle, needle+`"htmlCache":`+string(cache)+`,`, 1)
				}
			}
			walk(el.Elements)
		}
	}
	walk(els)
	if !json.Valid([]byte(out)) {
		return raw
	}
	return json.RawMessage(out)
}

var gweIDCounter uint32

// gweNewID makes a 7-hex element id like upstream's.
func gweNewID() string {
	n := uint32(time.Now().UnixNano()) ^ (gweIDCounterNext() * 2654435761)
	return fmt.Sprintf("%07x", n&0xfffffff)
}

var gweIDMu sync.Mutex

func gweIDCounterNext() uint32 {
	gweIDMu.Lock()
	defer gweIDMu.Unlock()
	gweIDCounter++
	return gweIDCounter
}

// --- editor page ------------------------------------------------------------------------------

// gweEditor is post.php?action=elementor: the Golementor editor for one post.
func (s *Server) gweEditor(c *gin.Context, p *store.Post) {
	u := currentUser(c)
	if !gweEditable(p) {
		c.String(http.StatusForbidden, "This item cannot be edited with Golementor.")
		return
	}
	if !u.Can("edit_post", p) || s.gweProRoleExcluded(u) { // Golementor Pro: Role Manager
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit this item.")
		return
	}
	kit, err := s.gweKit(c)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	d := gweData()
	cfg := make(map[string]any, len(d.editorCfg)+4)
	for k, v := range d.editorCfg {
		cfg[k] = v
	}
	cfg["initial_document"] = s.gweDocConfig(c, p)
	lb := s.links(c)
	for k, path := range map[string]string{"admin_settings_url": "/wp-admin/options-general", "settings_page_link": "/wp-admin/options-general",
		"admin_tools_url": "/wp-admin/tools", "tools_page_link": "/wp-admin/tools", "admin_apps_url": "/wp-admin/plugins",
		"admin_floating_button_admin_url": "/wp-admin/"} {
		cfg[k] = lb.HomeURL(path)
	}
	cfg["kit_id"] = kit.ID
	cfg["autosave_interval"] = 60
	s.gweProEditorConfig(c, cfg) // Golementor Pro: dynamic tags, display conditions, Pro controls
	cfg["user"] = map[string]any{
		"restrictions": []any{}, "is_administrator": u.Can("manage_options", nil), "introduction": gweUserMap(u, "gwe_introduction"),
		"dismissed_editor_notices": []any{}, "locale": "en_US", "top_bar": map[string]string{"connect_url": "", "my_elementor_url": ""},
		"can_edit_kit": u.Can("manage_options", nil) || u.Can("edit_theme_options", nil),
	}
	var settings map[string]map[string]any
	if json.Unmarshal(d.editorCfg["settings"], &settings) == nil {
		if ep, ok := settings["editorPreferences"]; ok {
			ep["settings"] = gweUserMap(u, "gwe_editor_preferences")
		}
		cfg["settings"] = settings
	}
	title := "Edit “" + p.Title + "” with Golementor"
	if p.Title == "" {
		title = "Golementor"
	}
	common := map[string]any{}
	for k, v := range d.commonCfg {
		common[k] = v
	}
	common["ajax"] = map[string]string{"url": s.links(c).HomeURL("/wp-admin/admin-ajax"), "nonce": s.nonce(c, "elementor_ajax")}
	common["filesUpload"] = map[string]bool{"unfilteredFiles": false}
	common["isRTL"] = false
	uid := int64(0)
	if u != nil {
		uid = u.ID
	}

	parts := map[string]string{
		"elementor-editor-js-before":     "var ElementorConfig = " + gweJSON(cfg) + ";",
		"elementor-common-js-before":     "var elementorCommonConfig = " + gweJSON(common) + ";",
		"elementor-app-loader-js-before": "var elementorAppConfig = " + string(d.appCfg) + ";",
		"utils-js-extra":                 fmt.Sprintf(`var userSettings = {"url":"/","uid":"%d","time":"%d","secure":""};`, uid, time.Now().Unix()),
		"wp-date-js-after":               s.dateSettingsJS(),
		"wp-i18n-js-after":               "wp.i18n.setLocaleData( { 'text direction\\u0004ltr': [ 'ltr' ] } );\nwp.i18n.setLocaleData( " + string(d.i18n) + ", 'elementor' );",
		"elementor-styleguide-js-extra":  `var elementorStyleguideConfig = {"activeKitId":"` + itoa64(kit.ID) + `"};`,
	}
	s.gweProEditorParts(c, p, parts) // Golementor Pro: elementorProEditorConfig and friends
	page := d.shell
	for id, js := range parts {
		page = strings.Replace(page, "@@CFG:"+id+"@@", js, 1)
	}
	page = strings.ReplaceAll(page, "@@TITLE@@", html.EscapeString(title))
	page = s.gweFill(c, page)
	// gap-integrations: local Google Fonts in the preview
	page = strings.Replace(page, "</body>", s.gweFontsEditorScript(c)+"</body>", 1)
	if js := s.gweEditorThemeScript(c, p); js != "" { // theme parts: Theme Builder / Save as theme
		page = strings.Replace(page, "</body>", js+"</body>", 1)
	}
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate, max-age=0")
	c.Data(http.StatusOK, "text/html; charset=UTF-8", []byte(page))
}

// gweUserMap reads a per-user settings map from user meta.
func gweUserMap(u *store.User, key string) map[string]any {
	out := map[string]any{}
	if u == nil {
		return out
	}
	if m, ok := u.Meta[key].(map[string]any); ok {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// gweSetUserMeta merges values into a per-user settings map.
func (s *Server) gweSetUserMeta(ctx context.Context, u *store.User, key string, values map[string]any) error {
	b, _ := json.Marshal(values)
	_, err := s.store.Pool.Exec(ctx, `UPDATE users SET meta = jsonb_set(coalesce(meta,'{}'::jsonb), ARRAY[$2::text],
		coalesce(meta->$2, '{}'::jsonb) || $3::jsonb) WHERE id = $1`, u.ID, key, string(b))
	return err
}

// gweNewPost is edit.php?action=elementor_new_post: creates a draft and opens it in Golementor.
func (s *Server) gweNewPost(c *gin.Context) {
	u := currentUser(c)
	if !s.checkNonce(c, "elementor_action_new_post") {
		s.denyNonce(c)
		return
	}
	typ := c.DefaultQuery("post_type", "page")
	tplType := c.Query("template_type")
	if typ != "post" && typ != "page" && typ != gweLibrary {
		c.String(http.StatusBadRequest, "Invalid post type.")
		return
	}
	capType := typ
	if typ == gweLibrary {
		capType = "post"
	}
	if !u.Can("edit_"+capType+"s", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to create posts as this user.")
		return
	}
	if typ == gweLibrary {
		if part, ok := s.gweProNewThemePart(c, tplType); ok { // Golementor Pro: theme types become theme parts
			c.Redirect(http.StatusFound, gweEditURL(part.ID))
			return
		}
		if err := s.gweEnsureLibraryType(c); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
	}
	p := &store.Post{Type: typ, AuthorID: u.ID, Status: "draft", Meta: map[string]any{"_elementor_edit_mode": "builder"}}
	switch {
	case typ == gweLibrary:
		if tplType == "" {
			tplType = "page"
		}
		p.Meta["_elementor_template_type"] = tplType
		p.Title = "Golementor " + strings.ToUpper(tplType[:1]) + tplType[1:] + " #"
	case typ == "page":
		p.Meta["_elementor_template_type"] = "wp-page"
		p.Title = "Golementor #"
	default:
		p.Meta["_elementor_template_type"] = "wp-post"
		p.Title = "Golementor #"
	}
	p.Meta["_elementor_data"] = "[]"
	if err := s.store.InsertPost(c, p); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	p.Title += itoa64(p.ID)
	_, _ = s.store.Pool.Exec(c, `UPDATE posts SET title = $2 WHERE id = $1`, p.ID, p.Title)
	c.Redirect(http.StatusFound, gweEditURL(p.ID))
}

// gweEnsureLibraryType registers the template content type (templates and the site kit) once.
func (s *Server) gweEnsureLibraryType(ctx context.Context) error {
	_, err := s.store.Pool.Exec(ctx, `INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels)
		VALUES ($1, 'Templates', false, false, '', false, '{"title":true,"editor":true,"author":true,"thumbnail":true,"revisions":true}',
		'{"name":"Templates","singular_name":"Template"}') ON CONFLICT (slug) DO NOTHING`, gweLibrary)
	return err
}

// initGoWebEditor creates Golementor's revision table (element trees per revision). Migration
// 0005_builder.sql creates it too; this keeps databases migrated before that working.
func (s *Server) initGoWebEditor(ctx context.Context) {
	if _, err := s.store.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS builder_revisions (
		revision_id bigint PRIMARY KEY REFERENCES post_revisions(id) ON DELETE CASCADE,
		data        jsonb NOT NULL DEFAULT '[]',
		settings    jsonb NOT NULL DEFAULT '{}'
	)`); err != nil {
		log.Printf("gowebeditor: revisions table: %v", err)
	}
}
