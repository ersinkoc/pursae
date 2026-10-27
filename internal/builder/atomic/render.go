package atomic

import (
	"encoding/json"
	"html"
	"strconv"
	"strings"

	"wpgo/internal/wp"
)

// Node is one element of a saved tree (_elementor_data) as the atomic renderer sees it.
type Node struct {
	ID             string
	ElType         string
	WidgetType     string
	Settings       *Map
	Styles         *Map // style id → style definition, in saved order
	Interactions   any
	EditorSettings *Map
	OriginID       string
	Children       []*Node
	// Raw is the element's JSON (for handing non-atomic elements back to the v3 renderer).
	Raw json.RawMessage
	// Ref is the caller's own element (builder.Element) when the node came from it.
	Ref any
}

// Type is the element type name (widgetType for widgets).
func (n *Node) Type() string {
	if n.ElType == "widget" {
		return n.WidgetType
	}
	return n.ElType
}

// ParseNodes decodes a saved element tree.
func ParseNodes(raw []byte) ([]*Node, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	out := make([]*Node, 0, len(list))
	for _, r := range list {
		n, err := ParseNode(r)
		if err != nil {
			return nil, err
		}
		if n != nil {
			out = append(out, n)
		}
	}
	return out, nil
}

// ParseNode decodes one element (with its children).
func ParseNode(raw []byte) (*Node, error) {
	var aux struct {
		Elements []json.RawMessage `json:"elements"`
	}
	_ = json.Unmarshal(raw, &aux)
	n := NodeFromJSON(raw)
	if n == nil {
		return nil, nil
	}
	for _, c := range aux.Elements {
		ch, err := ParseNode(c)
		if err != nil {
			return nil, err
		}
		if ch != nil {
			n.Children = append(n.Children, ch)
		}
	}
	return n, nil
}

// NodeFromJSON decodes one element without its children.
func NodeFromJSON(raw []byte) *Node {
	v, err := Decode(raw)
	if err != nil {
		return nil
	}
	m := asMap(v)
	if m == nil {
		return nil
	}
	n := &Node{
		ID: phpString(m.Get("id")), ElType: phpString(m.Get("elType")), WidgetType: phpString(m.Get("widgetType")),
		Settings: asMap(m.Get("settings")), Styles: asMap(m.Get("styles")), Interactions: m.Get("interactions"),
		EditorSettings: asMap(m.Get("editor_settings")), OriginID: phpString(m.Get("origin_id")), Raw: raw,
	}
	if n.Settings == nil {
		n.Settings = NewMap()
	}
	if n.Styles == nil {
		n.Styles = NewMap()
	}
	return n
}

const childrenPlaceholder = "<!-- elementor-children-placeholder -->"

func (e *Env) settingsResolver() *resolver {
	return &resolver{env: e, transformers: settingsTransformers()}
}

// atomicSettings mirrors Has_Atomic_Base::get_atomic_settings (the link stays as parsed: every
// atomic element renders through a template, whose trait keeps it).
func (e *Env) atomicSettings(n *Node, et *ElementType) *Map {
	props := n.Settings.Clone()
	typ := n.Type()
	if et.Widget {
		typ = "widget"
	}
	attrs := []any{
		MapOf("$$type", "key-value", "value", MapOf("key", MapOf("$$type", "string", "value", "data-e-type"), "value", typ)),
		MapOf("$$type", "key-value", "value", MapOf("key", MapOf("$$type", "string", "value", "data-id"), "value", n.ID)),
	}
	if saved, _, ok := transformable(props.Get("attributes")); ok {
		attrs = append(attrs, listOf(saved.Get("value"))...)
	}
	props.Set("attributes", MapOf("$$type", "attributes", "value", attrs))
	return e.settingsResolver().resolve(et.Schema, props)
}

// computedTag mirrors Html_Tag_Computer::compute.
func computedTag(settings *Map, et *ElementType) string {
	switch et.Name {
	case "e-list":
		return "ul"
	case "e-background-video":
		return validateHTMLTag(computeTag(settings, et))
	}
	return computeTag(settings, et)
}

func computeTag(settings *Map, et *ElementType) string {
	if et.FollowsLink {
		if link := asMap(settings.Get("link")); link != nil {
			if h := tagValue(link.Get("href")); h != "" || phpString(link.Get("attributes")) != "" {
				if t := tagValue(link.Get("tag")); t != "" {
					return t
				}
				return "a"
			}
		}
	}
	if t := tagValue(settings.Get("tag")); t != "" {
		return t
	}
	return et.DefaultTag
}

func tagValue(v any) string {
	if m := asMap(v); m != nil {
		if s, ok := m.Get("value").(string); ok {
			return s
		}
		return ""
	}
	s, _ := v.(string)
	return s
}

// Render prints an atomic element (Element_Base::print_element). Children that are not atomic
// are printed by env.RenderChild.
func (e *Env) Render(n *Node) string {
	et := Lookup(n.Type())
	if et == nil {
		if e.RenderChild != nil {
			return e.RenderChild(n)
		}
		return ""
	}
	if data().err != nil || data().twig == nil {
		return ""
	}
	if !e.Editing && e.Visible != nil {
		if conds := DisplayConditions(n); conds != "" && !e.Visible(n, conds) {
			return ""
		}
	}
	ctxKeys := e.defineRenderContext(n, et)
	defer func() {
		for _, k := range ctxKeys {
			e.popCtx(k)
		}
	}()
	settings := e.atomicSettings(n, et)
	interactionID := n.ID
	if n.OriginID != "" {
		interactionID = n.OriginID
	}
	ctx := MapOf("id", n.ID, "interaction_id", interactionID, "type", n.Type(), "settings", settings,
		"tag", computedTag(settings, et), "base_styles", et.Dictionary)
	if !et.Widget {
		ctx.Set("children_placeholder", childrenPlaceholder)
	}
	e.templateContext(n, et, settings, ctx)
	out, err := data().twig.Render(et.Template, ctx)
	if err != nil {
		return "<!-- Golementor: " + strings.ReplaceAll(err.Error(), "--", "-") + " -->\n"
	}
	if et.Widget {
		if out == "" {
			return ""
		}
		return "\t\t\t" + out + "\t\t"
	}
	var children strings.Builder
	if loop, ok := e.loopChildren(n, et); ok { // gap-loops: loop layouts repeat their item (loop.go)
		children.WriteString(loop)
	} else {
		for _, c := range n.Children {
			children.WriteString(e.Render(c))
		}
	}
	out = strings.ReplaceAll(out, childrenPlaceholder, children.String())
	return out
}

// defineRenderContext mirrors define_render_context of the compound elements.
func (e *Env) defineRenderContext(n *Node, et *ElementType) []string {
	var keys []string
	push := func(k string, m *Map) {
		e.pushCtx(k, m)
		keys = append(keys, k)
	}
	single := func(prop string) any {
		pt := et.Schema.Props[prop]
		if pt == nil {
			return nil
		}
		return e.settingsResolver().resolveValue(n.Settings.Get(prop), pt)
	}
	keys = append(keys, e.loopDefineContext(n, et)...) // gap-loops: the atomic Loop (loop.go)
	switch et.Name {
	case "e-tabs":
		tabs, contents := NewMap(), NewMap()
		for _, c := range n.Children {
			switch c.Type() {
			case "e-tabs-menu":
				i := 0
				for _, t := range c.Children {
					if t.Type() == "e-tab" {
						tabs.Set(t.ID, float64(i))
						i++
					}
				}
			case "e-tabs-content-area":
				i := 0
				for _, t := range c.Children {
					if t.Type() == "e-tab-content" {
						contents.Set(t.ID, float64(i))
						i++
					}
				}
			}
		}
		push("tabs", MapOf("default-active-tab", single("default-active-tab"), "tabs", tabs, "contents", contents, "tabs-id", n.ID))
	case "e-accordion":
		items := NewMap()
		i := 0
		for _, c := range n.Children {
			if c.Type() == "e-accordion-item" {
				items.Set(c.ID, float64(i))
				i++
			}
		}
		push("accordion", MapOf("accordion-id", n.ID, "items", items, "default-state", single("default_state"), "max-expanded", single("max_expanded")))
	case "e-component":
		overrides := NewMap()
		if ci, _, ok := transformable(n.Settings.Get("component_instance")); ok {
			if ov := get(ci.Get("value"), "overrides"); ov != nil {
				if pt := et.Schema.Props["component_instance"]; pt != nil && pt.Shape != nil {
					res := e.settingsResolver().resolve(&Schema{Keys: []string{"overrides"}, Props: map[string]*PropType{"overrides": pt.Shape.Props["overrides"]}}, MapOf("overrides", ov))
					for _, it := range listOf(res.Get("overrides")) {
						m := asMap(it)
						overrides.Set(phpString(m.Get("override_key")), m.Get("override_value"))
					}
				}
			}
		}
		push("overridable", MapOf("overrides", overrides))
		push("component-instance", MapOf("instance_id", n.ID))
	}
	return keys
}

// templateContext adds the build_template_context extras of individual elements.
func (e *Env) templateContext(n *Node, et *ElementType, settings *Map, ctx *Map) {
	e.loopTemplateContext(n, et, ctx) // gap-loops: the atomic Loop (loop.go)
	switch et.Name {
	case "e-tab", "e-tab-content":
		tc := e.getCtx("tabs")
		list := asMap(tc.Get("tabs"))
		if et.Name == "e-tab-content" {
			list = asMap(tc.Get("contents"))
		}
		idx, ok := list.Lookup(n.ID)
		active := ok && looseEq(tc.Get("default-active-tab"), idx) && tc.Get("default-active-tab") != nil
		tabsID := phpString(tc.Get("tabs-id"))
		i := phpString(idx)
		ctx.Set("is_active", active)
		ctx.Set("tab_id", tabsID+"-tab-"+i)
		ctx.Set("tab_content_id", tabsID+"-tab-content-"+i)
	case "e-accordion-item":
		ac := e.getCtx("accordion")
		idx, ok := asMap(ac.Get("items")).Lookup(n.ID)
		var index any
		if ok {
			index = idx
		}
		ctx.Set("item_index", index)
		ctx.Set("is_open", ac.Get("default-state") == "first_expanded" && ok && toFloat(idx) == 0)
		var group any
		if ac.Get("max-expanded") == "one" && !phpEmpty(ac.Get("accordion-id")) {
			group = ac.Get("accordion-id")
		}
		ctx.Set("group_name", group)
	case "e-accordion":
		ctx.Set("faq_schema_json", nil)
		if settings.Get("faq_schema") == true && !e.Editing {
			ctx.Set("faq_schema_json", e.faqSchema(n))
		}
	case "e-form":
		// Atomic_Form::build_template_context: the WebMCP tool attributes (front end only).
		ctx.Set("form_state", "default")
		if !e.Editing {
			name := phpString(settings.Get("form-name"))
			ctx.Set("webmcp_tool_name", webmcpToolName(name, n.ID))
			ctx.Set("webmcp_tool_description", webmcpToolDescription(name))
		}
	case "e-background-video":
		timings := "#t=0"
		start, end := settings.Get("start_time"), settings.Get("end_time")
		if !phpEmpty(start) {
			timings = "#t=" + phpString(start)
		}
		if !phpEmpty(end) && toFloat(end) > toFloat(start) {
			timings += "," + phpString(end)
		}
		ctx.Set("video_timings", timings)
	case "e-background-video-play":
		ctx.Set("initial_attributes", MapOf("type", "button", "aria-label", "Play video"))
	case "e-background-video-pause":
		ctx.Set("initial_attributes", MapOf("type", "button", "aria-label", "Pause video"))
	}
}

// faqSchema mirrors Atomic_Accordion::build_faq_schema_json: each item's title (question) and
// content (answer) as plain text from their markdown (RenderMarkdown, stripMarkdownSyntax).
func (e *Env) faqSchema(n *Node) any {
	var entities []any
	for _, item := range n.Children {
		if item.Type() != "e-accordion-item" {
			continue
		}
		q := e.faqText(item, "e-accordion-item-header", "e-accordion-item-title")
		a := e.faqText(item, "", "e-accordion-item-content")
		if q == "" || a == "" {
			continue
		}
		entities = append(entities, MapOf("@type", "Question", "name", q, "acceptedAnswer", MapOf("@type", "Answer", "text", a)))
	}
	if len(entities) == 0 {
		return nil
	}
	return phpJSONEncode(MapOf("@context", "https://schema.org", "@type", "FAQPage", "mainEntity", entities))
}

func (e *Env) renderChildren(n *Node) string {
	var b strings.Builder
	for _, c := range n.Children {
		b.WriteString(e.Render(c))
	}
	return b.String()
}

func plainText(html string) string {
	return strings.Join(strings.Fields(stripTags(html, "")), " ")
}

// ---- components -------------------------------------------------------------------------------

// componentInstanceTransformer renders the component's tree with ids scoped to the instance
// (Component_Instance_Transformer).
func componentInstanceTransformer(r *resolver, v any, _ string, _ *PropType) any {
	e := r.env
	id := intOf(get(v, "component_id"))
	if id <= 0 || e.Component == nil {
		return ""
	}
	key := "component-stack"
	for _, x := range e.ctx[key] {
		if intOf(x.Get("id")) == id {
			return "" // circular reference
		}
	}
	raw, ok := e.Component(id)
	if !ok {
		return ""
	}
	nodes, err := ParseNodes(raw)
	if err != nil {
		return ""
	}
	instance := phpString(e.getCtx("component-instance").Get("instance_id"))
	FormatIDs(nodes, []string{instance})
	e.pushCtx(key, MapOf("id", float64(id)))
	defer e.popCtx(key)
	var b strings.Builder
	for _, nd := range nodes {
		b.WriteString(e.Render(nd))
	}
	return Markup(b.String())
}

// FormatIDs mirrors Format_Element_Ids::format: inner ids become hashes of the nesting path and
// the original id moves to origin_id.
func FormatIDs(nodes []*Node, path []string) {
	for _, n := range nodes {
		origin := n.ID
		p := append(append([]string{}, path...), origin)
		n.ID = HashString(strings.Join(p, "_"), 7)
		n.OriginID = origin
		FormatIDs(n.Children, p)
	}
}

// HashString mirrors Format_Element_Ids::hash_string (djb2 over the reversed string, base 36).
func HashString(s string, length int) string {
	h := uint64(5381)
	for i := len(s) - 1; i >= 0; i-- {
		h = ((h * 33) ^ uint64(s[i])) & 0xFFFFFFFF
	}
	out := strconv.FormatUint(h, 36)
	if length <= 0 {
		return out
	}
	if len(out) > length {
		out = out[len(out)-length:]
	}
	for len(out) < length {
		out = "0" + out
	}
	return out
}

// webmcpToolName mirrors Webmcp_Utils::build_tool_name.
func webmcpToolName(formName, elementID string) string {
	slug := strings.ReplaceAll(wp.SanitizeTitle(formName), "-", "_")
	if slug == "" {
		slug = "submit_form"
	}
	suffix := strings.ReplaceAll(wp.SanitizeTitle(elementID), "-", "_")
	if suffix == "" {
		return slug
	}
	return slug + "_" + suffix
}

// webmcpToolDescription mirrors Webmcp_Utils::build_tool_description.
func webmcpToolDescription(formName string) string {
	label := strings.TrimSpace(formName)
	if label == "" {
		return "Submit this form with the provided field values."
	}
	return "Submit the " + html.EscapeString(label) + " form with the provided field values."
}

// DisplayConditions returns an element's display conditions as the JSON Pro's v3 control stores
// (Display_Conditions::extract_from_settings: the display-conditions prop resolved, each
// condition-group a list of JSON-decoded conditions), or "" when it has none.
func DisplayConditions(n *Node) string {
	m, typ, ok := transformable(n.Settings.Get("display-conditions"))
	if !ok || typ != "display-conditions" || m.Get("disabled") == true {
		return ""
	}
	var groups []any
	for _, g := range listOf(m.Get("value")) {
		gm, gt, ok := transformable(g)
		if !ok || gt != "condition-group" || gm.Get("disabled") == true {
			continue
		}
		var conds []any
		for _, c := range listOf(gm.Get("value")) {
			var text string
			if cm, ct, ok := transformable(c); ok && ct == "string" {
				text = phpString(cm.Get("value"))
			} else if str, ok := c.(string); ok {
				text = str
			}
			var v any
			if text == "" || json.Unmarshal([]byte(text), &v) != nil || v == nil {
				continue
			}
			conds = append(conds, v)
		}
		if len(conds) > 0 {
			groups = append(groups, conds)
		}
	}
	if len(groups) == 0 {
		return ""
	}
	b, err := json.Marshal(groups)
	if err != nil {
		return ""
	}
	return string(b)
}

// ResolvedSettings returns an element's settings resolved like the template sees them
// (get_atomic_settings: schema defaults, transformers), as plain JSON values (objects are
// map[string]any). Nil for an unknown element type.
func (e *Env) ResolvedSettings(n *Node) map[string]any {
	et := Lookup(n.Type())
	if et == nil {
		return nil
	}
	b, err := json.Marshal(e.atomicSettings(n, et))
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

// FindNode returns the element with the given id in a tree (depth first), or nil.
func FindNode(nodes []*Node, id string) *Node {
	for _, n := range nodes {
		if n.ID == id {
			return n
		}
		if f := FindNode(n.Children, id); f != nil {
			return f
		}
	}
	return nil
}

// Walk calls fn for every node of a tree, parents first.
func Walk(nodes []*Node, fn func(n *Node)) {
	for _, n := range nodes {
		fn(n)
		Walk(n.Children, fn)
	}
}
