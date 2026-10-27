package builder

// The front-end renderer: a port of upstream's Element_Base::print_element and the section,
// column, container and widget wrappers. Widget bodies live in widgets_*.go.

import (
	"errors"

	"strconv"
	"strings"
	"wpgo/internal/builder/atomic"
)

func init() {
	Render = renderDocument
	RenderWidget = renderWidgetContent
}

// widgetDef describes a ported widget.
type widgetDef struct {
	render  func(w *elemCtx) string
	styles  func(w *elemCtx) []string // style handles (get_style_depends); nil = none
	scripts []string                  // script handles (get_script_depends)
	// wrapperClass overrides get_html_wrapper_class ("" = elementor-widget-<name>).
	wrapperClass string
}

var widgetDefs = map[string]*widgetDef{}

// repanic disables printElement's panic recovery (tests).
var repanic bool

func registerWidget(name string, d *widgetDef) { widgetDefs[name] = d }

type renderer struct {
	rc      *RenderContext
	doc     *Document
	bps     []Breakpoint
	styles  []string
	scripts []string
	globals *kitGlobals
	images  int // images printed so far (WP loading optimisation)
	// preAttrs lets a parent widget add wrapper attributes to a child before the child's own
	// add_render_attributes runs (the elementor/frontend/<type>/should_render filter).
	preAttrs map[*Element]func(*attrs)
	// Golementor v4 (atomic_hook.go)
	atomic    *atomic.Env
	hasAtomic bool
}

func newRenderer(doc *Document, rc *RenderContext) *renderer {
	if rc == nil {
		rc = &RenderContext{}
	}
	return &renderer{rc: rc, doc: doc, bps: activeBreakpoints(rc), globals: kitGlobalsFor(rc)}
}

func addUnique(list []string, items ...string) []string {
outer:
	for _, it := range items {
		if it == "" {
			continue
		}
		for _, x := range list {
			if x == it {
				continue outer
			}
		}
		list = append(list, it)
	}
	return list
}

// renderDocument implements Render.
func renderDocument(doc *Document, rc *RenderContext) (*Result, error) {
	if doc == nil {
		return &Result{}, nil
	}
	r := newRenderer(doc, rc)
	r.resolveGlobals(doc.Elements) // Golementor Pro: global widgets
	r.resolveDynamic(doc)          // Golementor Pro: dynamic tags
	res := &Result{}
	css, fonts, _ := postCSS(doc, r.rc)
	res.CSS = css
	res.Fonts = fonts
	var b strings.Builder
	docType := doc.Type
	if docType == "" {
		docType = "wp-page"
	}
	a := newAttrs()
	a.add("data-elementor-type", docType)
	a.add("data-elementor-id", strconv.FormatInt(doc.ID, 10))
	a.add("class", "elementor elementor-"+strconv.FormatInt(doc.ID, 10))
	if doc.PostType != "" { // Golementor Pro (theme builder wrapper_attributes filter)
		a.add("data-elementor-post-type", doc.PostType)
	}
	if r.rc.Editing {
		if t, ok := doc.Settings["post_title"]; ok {
			a.add("data-elementor-title", phpString(t))
		}
	}
	b.WriteString("<div " + a.String() + ">\n")
	for _, el := range doc.Elements {
		if el != nil {
			b.WriteString(r.printElement(el))
		}
	}
	b.WriteString("\t\t</div>\n")
	res.HTML = filterContentTags(processMoreTag(b.String(), doc.ID))
	r.documentAssets(doc)
	res.Styles = r.styles
	res.Scripts = r.scripts
	return res, nil
}

// renderWidgetContent implements RenderWidget (the editor's render_widget action).
func renderWidgetContent(el *Element, doc *Document, rc *RenderContext) (string, error) {
	if el == nil {
		return "", errors.New("Widget not found.")
	}
	lrc := RenderContext{Editing: true}
	if rc != nil {
		lrc = *rc
		lrc.Editing = true
	}
	r := newRenderer(doc, &lrc)
	r.resolveGlobals([]*Element{el})                                  // Golementor Pro: global widgets
	r.resolveDynamic(&Document{ID: doc.ID, Elements: []*Element{el}}) // Golementor Pro: dynamic tags
	if atomic.IsAtomic(el.ElType, el.WidgetType) {                    // Golementor v4: atomic_hook.go
		return r.printAtomic(el), nil
	}
	e := r.newElemCtx(el)
	if e == nil {
		return "", errors.New("Widget not found.")
	}
	if el.ElType != "widget" {
		return r.printElement(el), nil
	}
	return e.widgetContent(), nil
}

// elemCtx is one element instance being rendered.
type elemCtx struct {
	r        *renderer
	el       *Element
	name     string
	st       *stack
	settings *omap // get_settings()
	disp     *omap // get_settings_for_display()
	attrs    map[string]*attrs
	def      *widgetDef
}

func (r *renderer) newElemCtx(el *Element) *elemCtx {
	name, st := elementStack(el, r.bps)
	if st == nil {
		return nil
	}
	ctrls := st.display
	byName := st.displayBy
	if r.rc.Editing {
		ctrls = st.all
		byName = st.byName
	}
	settings := initSettings(rawSettings(el), ctrls)
	e := &elemCtx{r: r, el: el, name: name, st: st, settings: settings, attrs: map[string]*attrs{}}
	e.disp = activeSettings(settings, byName)
	if el.ElType == "widget" {
		e.def = widgetDefs[el.WidgetType]
		if el.globalOf != "" { // Golementor Pro global widget
			e.def = proGlobalDef(el)
		}
	}
	return e
}

// attr returns the render attribute set for key.
func (e *elemCtx) attr(key string) *attrs {
	a, ok := e.attrs[key]
	if !ok {
		a = newAttrs()
		e.attrs[key] = a
	}
	return a
}

// attrString mirrors get_render_attribute_string.
func (e *elemCtx) attrString(key string) string {
	a, ok := e.attrs[key]
	if !ok {
		return ""
	}
	return a.String()
}

// get returns a display setting (get_settings_for_display).
func (e *elemCtx) get(key string) any { return e.disp.m[key] }

// s returns a display setting as a string.
func (e *elemCtx) s(key string) string { return str(e.disp.m[key]) }

// raw returns a setting with defaults but without condition filtering (get_settings).
func (e *elemCtx) raw(key string) any { return e.settings.m[key] }

func (e *elemCtx) has(key string) bool { return truthy(e.disp.m[key]) }

// editing reports whether the editor (edit mode) is rendering.
func (e *elemCtx) editing() bool { return e.r.rc.Editing }

// addInlineEditing mirrors add_inline_editing_attributes.
func (e *elemCtx) addInlineEditing(key, toolbar string) {
	if !e.editing() {
		return
	}
	e.attr(key).add("class", "elementor-inline-editing").add("data-elementor-setting-key", key)
	if toolbar != "" && toolbar != "basic" {
		e.attr(key).add("data-elementor-inline-editing-toolbar", toolbar)
	}
}

// addLinkAttributes mirrors Element_Base::add_link_attributes.
func (e *elemCtx) addLinkAttributes(key string, link any, overwrite bool) {
	lm := asMap(link)
	if lm == nil {
		return
	}
	a := e.attr(key)
	apply := func(k, v string) {
		if overwrite {
			a.set(k, v)
		} else {
			a.add(k, v)
		}
	}
	if truthy(lm["url"]) {
		apply("href", escURL(phpString(lm["url"])))
	}
	if truthy(lm["is_external"]) {
		apply("target", "_blank")
	}
	if truthy(lm["nofollow"]) {
		apply("rel", "nofollow")
	}
	if truthy(lm["custom_attributes"]) {
		for _, kv := range parseCustomAttributes(phpString(lm["custom_attributes"])) {
			apply(kv[0], kv[1])
		}
	}
}

func parseCustomAttributes(s string) [][2]string {
	var out [][2]string
	idx := map[string]int{}
	for _, attr := range strings.Split(s, ",") {
		kv := strings.Split(attr, "|")
		key := strings.ToLower(kv[0])
		m := customAttrKeyRe.FindString(key)
		if m == "" {
			continue
		}
		if m == "href" || strings.HasPrefix(m, "on") {
			continue
		}
		val := ""
		if len(kv) > 1 {
			val = strings.TrimSpace(kv[1])
		}
		if i, ok := idx[m]; ok {
			out[i][1] = val
			continue
		}
		idx[m] = len(out)
		out = append(out, [2]string{m, val})
	}
	return out
}

// repeaterKey mirrors get_repeater_setting_key.
func repeaterKey(setting, repeater string, index int) string {
	return repeater + "." + strconv.Itoa(index) + "." + setting
}

// ---- print_element ----

func (r *renderer) printElement(el *Element) (out string) {
	defer func() {
		// A malformed element must never take the whole page down.
		if p := recover(); p != nil {
			if repanic {
				panic(p)
			}
			out = "<!-- Golementor: failed to render element (" + strings.ReplaceAll(el.ID, "--", "-") + ") -->\n"
		}
	}()
	if atomic.IsAtomic(el.ElType, el.WidgetType) { // Golementor v4: atomic_hook.go
		return r.printAtomic(el)
	}
	e := r.newElemCtx(el)
	if e == nil {
		return unsupportedComment(el)
	}
	if el.ElType == "widget" && e.def == nil {
		return unsupportedComment(el)
	}
	if !proShouldRender(e) { // Golementor Pro: display conditions
		return ""
	}
	var content string
	switch el.ElType {
	case "widget":
		content = e.widgetContent()
		if content == "" {
			return ""
		}
	default:
		var b strings.Builder
		for _, ch := range el.Elements {
			if ch != nil {
				b.WriteString(r.printElement(ch))
			}
		}
		content = b.String()
	}
	if f := r.preAttrs[el]; f != nil {
		f(e.attr("_wrapper"))
	}
	e.addRenderAttributes()
	r.collectAssets(e)
	var b strings.Builder
	switch el.ElType {
	case "section":
		b.WriteString(e.sectionBefore())
		b.WriteString(content)
		b.WriteString("\t\t</div>\n\t\t</" + e.sectionTag() + ">\n")
	case "column":
		b.WriteString(e.columnBefore())
		b.WriteString(content)
		b.WriteString("\t\t</div>\n\t\t</" + e.htmlTag("div") + ">\n")
	case "container":
		b.WriteString(e.containerBefore())
		b.WriteString(content)
		if e.boxed() {
			b.WriteString("\t\t\t</div>\n")
		}
		b.WriteString("\t\t</" + e.htmlTag("div") + ">\n")
	case "widget":
		b.WriteString("\t\t<div " + e.attrString("_wrapper") + ">\n")
		b.WriteString(content)
		b.WriteString("\t\t</div>\n")
	}
	return b.String()
}

func unsupportedComment(el *Element) string {
	name := el.ElType
	if el.ElType == "widget" {
		name = el.WidgetType
	}
	name = strings.ReplaceAll(name, "--", "-")
	return "<!-- Golementor: unsupported element \"" + name + "\" (" + strings.ReplaceAll(el.ID, "--", "-") + ") -->\n"
}

// collectAssets mirrors Elements_Iteration_Actions\Assets::element_action: assets declared by
// controls (control "assets" with conditions, animation controls), then the element's style and
// script dependencies plus the global "elementor-frontend" script.
func (r *renderer) collectAssets(e *elemCtx) {
	byName := e.st.displayBy
	if e.r.rc.Editing {
		byName = e.st.byName
	}
	styles, scripts := controlAssets(e.disp, byName)
	if e.el.ElType == "widget" && e.def != nil {
		if e.def.styles != nil {
			styles = addUnique(styles, e.def.styles(e)...)
		}
		scripts = addUnique(scripts, e.def.scripts...)
	}
	scripts = addUnique(scripts, "elementor-frontend")
	r.styles = addUnique(r.styles, styles...)
	r.scripts = addUnique(r.scripts, scripts...)
}

// documentAssets adds assets declared by the document (page settings) controls.
func (r *renderer) documentAssets(doc *Document) {
	kind, _ := documentCSSKind(doc)
	st := stackFor(kind, r.bps)
	if st == nil {
		return
	}
	raw := newOmap()
	for _, k := range settingsOrder(doc) {
		raw.Set(k, doc.Settings[k])
	}
	settings := initSettings(raw, st.display)
	styles, scripts := controlAssets(settings, st.displayBy)
	r.styles = addUnique(r.styles, styles...)
	r.scripts = addUnique(r.scripts, scripts...)
}

// controlAssets mirrors Assets::get_assets for one settings set.
func controlAssets(settings *omap, controls map[string]*ctrl) (styles, scripts []string) {
	for _, k := range settings.keys {
		c := controls[k]
		if c == nil {
			continue
		}
		for _, typ := range []string{"styles", "scripts"} {
			for _, dep := range asList(c.Assets[typ]) {
				dm := asMap(dep)
				if dm == nil {
					continue
				}
				if cond := asMap(dm["conditions"]); len(cond) > 0 && !checkConditions(cond, settings.m) {
					continue
				}
				if typ == "styles" {
					styles = append(styles, phpString(dm["name"]))
				} else {
					scripts = append(scripts, phpString(dm["name"]))
				}
			}
		}
		switch c.Type {
		case "animation", "hover_animation", "exit_animation", "hover_animation_contact_buttons":
			v := settings.m[k]
			if truthy(v) && phpString(v) != "none" && !isArray(v) {
				styles = append(styles, "e-animation-"+phpString(v))
			}
		}
	}
	return styles, scripts
}

// widgetContent mirrors Widget_Base::render_content.
func (e *elemCtx) widgetContent() string {
	if e.def == nil {
		return unsupportedComment(e.el)
	}
	content := e.def.render(e)
	if strings.TrimSpace(content) == "" && content == "" {
		return ""
	}
	if content == "" {
		return ""
	}
	if w := loadSchema().widgets[e.el.WidgetType]; w != nil && w.InnerWrapper {
		return "\t\t<div class=\"elementor-widget-container\">\n" + content + "\t\t</div>\n"
	}
	return content
}

// addRenderAttributes mirrors the add_render_attributes chain of each element type.
func (e *elemCtx) addRenderAttributes() {
	w := e.attr("_wrapper")
	switch e.el.ElType {
	case "section":
		t := "top"
		if e.el.IsInner {
			t = "inner"
		}
		w.add("class", "elementor-section", "elementor-"+t+"-section")
	case "column":
		t := "top"
		if e.el.IsInner {
			t = "inner"
		}
		w.add("class", "elementor-column", "elementor-col-"+phpString(e.raw("_column_size")), "elementor-"+t+"-column")
	}
	id := e.el.ID
	w.add("class", "elementor-element", "elementor-element-"+id)
	w.add("data-id", id)
	w.add("data-element_type", e.el.ElType)
	w.add("data-e-type", e.el.ElType)
	byName := e.st.displayBy
	if e.r.rc.Editing {
		byName = e.st.byName
	}
	for _, k := range e.disp.keys {
		c := byName[k]
		if c == nil || !c.HasPrefix {
			continue
		}
		v := e.disp.m[k]
		if c.ClassesDict != nil {
			if key, ok := phpKey(v); ok && !isArray(v) {
				if dv, ok := c.ClassesDict[key]; ok {
					v = dv
				}
			}
		}
		if isEmpty(v) && !strictEq(v, "0") {
			continue
		}
		w.add("class", c.PrefixClass+phpString(v))
	}
	anim := e.disp.m["_animation"]
	anim2 := e.disp.m["animation"]
	if (truthy(anim) && phpString(anim) != "none") || (truthy(anim2) && phpString(anim2) != "none") {
		w.add("class", "elementor-invisible")
	}
	if eid := e.s("_element_id"); eid != "" {
		w.add("id", strings.TrimSpace(eid))
	}
	if fs := e.frontendSettings(); fs.Len() > 0 {
		w.add("data-settings", phpJSON(fs))
	}
	switch e.el.ElType {
	case "container":
		t := "e-parent"
		if e.el.IsInner {
			t = "e-child"
		}
		w.add("class", "e-con", t)
	case "widget":
		wc := "elementor-widget-" + e.el.WidgetType
		if e.def != nil && e.def.wrapperClass != "" {
			wc = e.def.wrapperClass
		}
		w.add("class", "elementor-widget", wc)
		skin := e.s("_skin")
		if skin == "" || !truthy(e.raw("_skin")) {
			skin = "default"
		}
		wt := e.el.WidgetType
		if e.el.globalOf != "" {
			wt = e.el.globalOf
		}
		w.add("data-widget_type", wt+"."+skin)
	}
	proRenderAttributes(e, w) // Golementor Pro: custom attributes
}

// frontendSettings mirrors Controls_Stack::get_frontend_settings.
func (e *elemCtx) frontendSettings() *omap {
	keys := map[string]bool{}
	ctrls := e.st.display
	if e.r.rc.Editing {
		ctrls = e.st.all
	}
	for _, c := range ctrls {
		if c.Frontend {
			keys[c.Name] = true
		}
	}
	out := newOmap()
	for _, k := range e.disp.keys {
		if !keys[k] {
			continue
		}
		v := e.disp.m[k]
		if v == nil || strictEq(v, "") {
			continue
		}
		var def any
		if c := e.st.byName[k]; c != nil && c.isMultiple() {
			def = c.t.Default
		} else if c != nil && c.isRepeater() {
			var fo repeaterFieldOrder
			for _, f := range c.Fields {
				fo = append(fo, f.Name)
			}
			def = fo
		}
		out.Set(k, orderedValue(e.el, k, v, def))
	}
	return out
}

func (e *elemCtx) htmlTag(def string) string {
	t := phpString(e.raw("html_tag"))
	if t == "" {
		t = def
	}
	return validateHTMLTag(t)
}

var allowedHTMLTags = map[string]bool{"a": true, "article": true, "aside": true, "button": true, "div": true, "footer": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "header": true, "main": true, "nav": true, "p": true, "section": true, "span": true}

// validateHTMLTag mirrors Utils::validate_html_tag.
func validateHTMLTag(tag string) string {
	if allowedHTMLTags[strings.ToLower(tag)] {
		return tag
	}
	return "div"
}

func (e *elemCtx) sectionTag() string { return e.htmlTag("section") }

func (e *elemCtx) sectionBefore() string {
	var b strings.Builder
	b.WriteString("\t\t<" + e.sectionTag() + " " + e.attrString("_wrapper") + ">\n")
	b.WriteString(e.backgroundVideo(true))
	if hasOverlay(e) {
		b.WriteString("\t\t\t\t\t\t\t<div class=\"elementor-background-overlay\"></div>\n")
	}
	if e.has("shape_divider_top") {
		b.WriteString(e.shapeDivider("top"))
	}
	if e.has("shape_divider_bottom") {
		b.WriteString(e.shapeDivider("bottom"))
	}
	b.WriteString("\t\t\t\t\t\t\t<div class=\"elementor-container elementor-column-gap-" + escAttr(e.s("gap")) + "\">\n")
	return b.String()
}

func hasOverlay(e *elemCtx) bool {
	ov := e.s("background_overlay_background")
	ovh := e.s("background_overlay_hover_background")
	return ov == "classic" || ov == "gradient" || ovh == "classic" || ovh == "gradient"
}

func (e *elemCtx) backgroundVideo(section bool) string {
	if e.s("background_background") != "video" {
		return ""
	}
	link := e.s("background_video_link")
	if link == "" {
		return ""
	}
	a := e.attr("background-video-container")
	a.add("class", "elementor-background-video-container")
	if !e.has("background_play_on_mobile") {
		a.add("class", "elementor-hidden-mobile")
	}
	var b strings.Builder
	b.WriteString("<div " + a.String() + ">\n")
	if isEmbedVideo(link) {
		b.WriteString("\t\t\t\t\t\t\t\t\t<div class=\"elementor-background-video-embed\" role=\"presentation\"></div>\n")
	} else {
		attrsStr := "autoplay muted playsinline"
		if e.s("background_play_once") != "yes" {
			attrsStr += " loop"
		}
		b.WriteString("\t\t\t\t\t\t\t\t<video class=\"elementor-background-video-hosted\" role=\"presentation\" " + attrsStr + "></video>\n")
	}
	b.WriteString("\t\t\t\t\t</div>\n")
	return b.String()
}

func (e *elemCtx) columnBefore() string {
	ww := e.attr("_widget_wrapper")
	ww.add("class", "elementor-widget-wrap")
	if len(e.el.Elements) > 0 {
		ww.add("class", "elementor-element-populated")
	}
	var b strings.Builder
	b.WriteString("\t\t<" + e.htmlTag("div") + " " + e.attrString("_wrapper") + ">\n")
	b.WriteString("\t\t\t<div " + ww.String() + ">\n")
	if hasOverlay(e) {
		b.WriteString("\t\t\t\t\t<div class=\"elementor-background-overlay\"></div>\n")
	}
	return b.String()
}

func (e *elemCtx) boxed() bool { return e.s("content_width") == "boxed" }

func (e *elemCtx) containerBefore() string {
	if lm := asMap(e.get("link")); lm != nil && truthy(lm["url"]) {
		e.addLinkAttributes("_wrapper", lm, false)
	}
	var b strings.Builder
	b.WriteString("<" + e.htmlTag("div") + " " + e.attrString("_wrapper") + ">\n")
	if e.boxed() {
		b.WriteString("\t\t\t\t\t<div class=\"e-con-inner\">\n")
	}
	b.WriteString(e.backgroundVideo(false))
	if e.has("shape_divider_top") {
		b.WriteString(e.shapeDivider("top"))
	}
	if e.has("shape_divider_bottom") {
		b.WriteString(e.shapeDivider("bottom"))
	}
	return b.String()
}

func (e *elemCtx) shapeDivider(side string) string {
	key := "shape_divider_" + side
	negative := truthy(e.get(key + "_negative"))
	svg := shapeSVG(e.s(key), negative)
	if svg == "" {
		return ""
	}
	neg := "false"
	if negative {
		neg = "true"
	}
	return "\t\t\t\t<div class=\"elementor-shape elementor-shape-" + escAttr(side) + "\" aria-hidden=\"true\" data-negative=\"" + neg + "\">\n\t\t\t" + svg + "\t\t</div>\n"
}
