package builder

// Control definitions of the theme-building ("Site") widgets. Upstream these widgets are not part
// of the free editor, so there is no dump to load: each widget's controls are written here once, in
// the shape the editor's widgets config uses (tab, section, label, options, selectors as an object),
// and converted at init into the schema form the renderer and the CSS engine read (selectors as a
// list of pairs, style controls marked _style). The server adds the same definitions to the widgets
// config the editor loads (ThemeWidgets), so the panel and the renderer can never disagree.

import (
	"bytes"
	"encoding/json"
	"sort"
)

// Ordered is a JSON object that keeps its key order (select options, choose options and the
// controls list itself are shown in this order by the editor).
type Ordered []KV

// KV is one key of an Ordered object.
type KV struct {
	K string
	V any
}

// MarshalJSON writes the keys in order.
func (o Ordered) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, kv := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(kv.K)
		b.Write(k)
		b.WriteByte(':')
		v, err := json.Marshal(kv.V)
		if err != nil {
			return nil, err
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// ThemeWidget is one Site widget as the editor panel lists it.
type ThemeWidget struct {
	Name     string
	Title    string
	Icon     string
	Keywords []string
	// Categories are panel categories ("theme-elements" is shown as "Site",
	// "theme-elements-single" as "Single").
	Categories []string
	// WrapperClass is the widget wrapper's class (html_wrapper_class).
	WrapperClass string
	// Controls are the widget's own controls in editor form, in panel order. The common
	// (Advanced tab) controls are merged in by the editor and the schema, like for core widgets.
	Controls []map[string]any
}

// ControlsJSON is the widget's "controls" object for the editor's widgets config.
func (w *ThemeWidget) ControlsJSON() json.RawMessage {
	o := make(Ordered, 0, len(w.Controls))
	for _, c := range w.Controls {
		o = append(o, KV{phpString(c["name"]), c})
	}
	b, _ := json.Marshal(o)
	return b
}

var themeWidgetList []*ThemeWidget

// ThemeWidgets lists the Site widgets in panel order.
func ThemeWidgets() []*ThemeWidget { return themeWidgetList }

// ctlList builds a widget's controls section by section.
type ctlList struct {
	out          []map[string]any
	tab, section string
}

func (l *ctlList) start(name, label, tab string) {
	l.tab, l.section = tab, name
	l.out = append(l.out, map[string]any{"type": "section", "tab": tab, "label": label, "name": name})
}

// add appends a control in the current section; kv are extra key/value pairs.
func (l *ctlList) add(name, typ, label string, kv ...any) map[string]any {
	c := map[string]any{"type": typ, "tab": l.tab, "section": l.section, "label": label, "name": name}
	for i := 0; i+1 < len(kv); i += 2 {
		c[kv[i].(string)] = kv[i+1]
	}
	if _, ok := c["default"]; !ok {
		switch typ {
		case "text", "textarea", "select", "choose", "color", "switcher", "number":
			c["default"] = ""
		case "slider":
			c["default"] = map[string]any{"unit": "px", "size": "", "sizes": []any{}}
		}
	}
	l.out = append(l.out, c)
	return c
}

// sel is a one-selector selectors object.
func sel(selector, css string) Ordered { return Ordered{{selector, css}} }

var alignOptions = Ordered{
	{"start", map[string]any{"title": "Start", "icon": "eicon-text-align-left"}},
	{"center", map[string]any{"title": "Center", "icon": "eicon-text-align-center"}},
	{"end", map[string]any{"title": "End", "icon": "eicon-text-align-right"}},
	{"justify", map[string]any{"title": "Justified", "icon": "eicon-text-align-justify"}},
}

var htmlTagOptions = Ordered{{"h1", "H1"}, {"h2", "H2"}, {"h3", "H3"}, {"h4", "H4"}, {"h5", "H5"}, {"h6", "H6"},
	{"div", "div"}, {"span", "span"}, {"p", "p"}}

var imageSizeOptions = Ordered{{"thumbnail", "Thumbnail"}, {"medium", "Medium"}, {"medium_large", "Medium Large"},
	{"large", "Large"}, {"1536x1536", "1536x1536"}, {"2048x2048", "2048x2048"}, {"full", "Full"}}

// align adds the usual responsive text-align control.
func (l *ctlList) align(name, selector string) {
	l.add(name, "choose", "Alignment", "options", alignOptions, "classes", "elementor-control-start-end",
		"selectors_dictionary", map[string]any{"left": "start", "right": "end"},
		"selectors", sel(selector, "text-align: {{VALUE}};"), "responsive", []any{}, "is_responsive", true)
}

// color adds a color control; global is a kit color id ("" for none).
func (l *ctlList) color(name, label, selector, prop, global string) {
	c := l.add(name, "color", label, "selectors", sel(selector, prop+": {{VALUE}};"))
	if global != "" {
		c["global"] = map[string]any{"default": "globals/colors?id=" + global}
	}
}

// slider adds a responsive slider; css uses {{SIZE}}{{UNIT}}.
func (l *ctlList) slider(name, label, selector, css string, units []string, rng map[string]any, def map[string]any) {
	d := map[string]any{"unit": units[0], "size": "", "sizes": []any{}}
	for k, v := range def {
		d[k] = v
	}
	l.add(name, "slider", label, "size_units", units, "range", rng, "selectors", sel(selector, css),
		"responsive", []any{}, "is_responsive", true, "default", d)
}

// typography adds the typography group control (Group_Control_Typography) named name, like core
// widgets: a popover toggle with a global default and the font fields.
func (l *ctlList) typography(name, selector, global string) {
	p := name + "_"
	cond := map[string]any{p + "typography!": ""}
	cls := func(f string) string {
		return "elementor-group-control-typography elementor-group-control elementor-group-control-" + f
	}
	tg := l.add(p+"typography", "popover_toggle", "Typography", "return_value", "custom", "render_type", "ui",
		"groupType", "typography", "groupPrefix", p, "default", "")
	if global != "" {
		tg["global"] = map[string]any{"active": true, "default": "globals/typography?id=" + global}
	}
	field := func(f, typ, label string, kv ...any) map[string]any {
		c := l.add(p+f, typ, label, kv...)
		c["condition"] = cond
		c["classes"] = cls(f)
		c["groupPrefix"] = p
		c["groupType"] = "typography"
		if _, ok := c["popover"]; !ok {
			c["popover"] = []any{}
		}
		return c
	}
	emptySlider := func() map[string]any { return map[string]any{"unit": "px", "size": "", "sizes": []any{}} }
	field("font_family", "font", "Family", "default", "", "selector_value", `font-family: "{{VALUE}}", Sans-serif;`,
		"selectors", sel(selector, `font-family: "{{VALUE}}", Sans-serif;`), "popover", map[string]any{"start": true})
	field("font_size", "slider", "Size", "size_units", []string{"px", "em", "rem", "vw", "custom"},
		"range", map[string]any{"px": map[string]any{"min": 1, "max": 200}, "em": map[string]any{"max": 20}, "rem": map[string]any{"max": 20}, "vw": map[string]any{"min": 0.1, "max": 10, "step": 0.1}},
		"selector_value", "font-size: {{SIZE}}{{UNIT}}", "selectors", sel(selector, "font-size: {{SIZE}}{{UNIT}}"),
		"responsive", []any{}, "is_responsive", true, "default", emptySlider())
	field("font_weight", "select", "Weight", "default", "", "options", Ordered{{"100", "100 (Thin)"}, {"200", "200 (Extra Light)"},
		{"300", "300 (Light)"}, {"400", "400 (Normal)"}, {"500", "500 (Medium)"}, {"600", "600 (Semi Bold)"}, {"700", "700 (Bold)"},
		{"800", "800 (Extra Bold)"}, {"900", "900 (Black)"}, {"", "Default"}, {"normal", "Normal"}, {"bold", "Bold"}},
		"selectors", sel(selector, "font-weight: {{VALUE}};"))
	field("text_transform", "select", "Transform", "default", "", "options", Ordered{{"", "Default"}, {"uppercase", "Uppercase"},
		{"lowercase", "Lowercase"}, {"capitalize", "Capitalize"}, {"none", "Normal"}}, "selectors", sel(selector, "text-transform: {{VALUE}};"))
	field("font_style", "select", "Style", "default", "", "options", Ordered{{"", "Default"}, {"normal", "Normal"}, {"italic", "Italic"},
		{"oblique", "Oblique"}}, "selectors", sel(selector, "font-style: {{VALUE}};"))
	field("text_decoration", "select", "Decoration", "default", "", "options", Ordered{{"", "Default"}, {"underline", "Underline"},
		{"overline", "Overline"}, {"line-through", "Line Through"}, {"none", "None"}}, "selectors", sel(selector, "text-decoration: {{VALUE}};"))
	field("line_height", "slider", "Line Height", "desktop_default", map[string]any{"unit": "em"}, "tablet_default", map[string]any{"unit": "em"},
		"mobile_default", map[string]any{"unit": "em"}, "range", map[string]any{"px": map[string]any{"min": 1}},
		"size_units", []string{"px", "em", "rem", "custom"}, "selector_value", "line-height: {{SIZE}}{{UNIT}}",
		"selectors", sel(selector, "line-height: {{SIZE}}{{UNIT}}"), "responsive", []any{}, "is_responsive", true, "default", emptySlider())
	field("letter_spacing", "slider", "Letter Spacing", "size_units", []string{"px", "em", "rem", "custom"},
		"range", map[string]any{"px": map[string]any{"min": -5, "max": 10, "step": 0.1}},
		"selector_value", "letter-spacing: {{SIZE}}{{UNIT}}", "selectors", sel(selector, "letter-spacing: {{SIZE}}{{UNIT}}"),
		"responsive", []any{}, "is_responsive", true, "default", emptySlider())
	field("word_spacing", "slider", "Word Spacing", "size_units", []string{"px", "em", "rem", "custom"},
		"range", map[string]any{"px": map[string]any{"max": 50}}, "desktop_default", map[string]any{"unit": "em"},
		"tablet_default", map[string]any{"unit": "em"}, "mobile_default", map[string]any{"unit": "em"},
		"selector_value", "word-spacing: {{SIZE}}{{UNIT}}", "selectors", sel(selector, "word-spacing: {{SIZE}}{{UNIT}}"),
		"responsive", []any{}, "is_responsive", true, "popover", map[string]any{"end": true}, "default", emptySlider())
}

// heading adds the controls of a heading-like widget (site title, post title, ...): HTML tag, an
// optional link switch, alignment, color and typography on .elementor-heading-title.
func (l *ctlList) heading(section, label, tag, linkLabel, linkDefault, colorGlobal, typoGlobal string) {
	l.start(section, label, "content")
	l.add("header_size", "select", "HTML Tag", "options", htmlTagOptions, "default", tag)
	if linkLabel != "" {
		l.add("link_to", "switcher", linkLabel, "label_on", "Yes", "label_off", "No", "return_value", "yes", "default", linkDefault)
	}
	l.start(section+"_style", label, "style")
	l.align("align", "{{WRAPPER}}")
	l.color("title_color", "Text Color", "{{WRAPPER}} .elementor-heading-title, {{WRAPPER}} .elementor-heading-title a", "color", colorGlobal)
	l.color("title_hover_color", "Link Hover Color", "{{WRAPPER}} .elementor-heading-title a:hover", "color", "")
	l.typography("typography", "{{WRAPPER}} .elementor-heading-title", typoGlobal)
}

// schemaControl converts an editor-form control to the schema form (see the dump format in
// data/controls.json.gz).
func schemaControl(c map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range c {
		switch k {
		case "label", "tab", "section", "classes", "label_on", "label_off", "placeholder", "description", "size_units", "range",
			"popover", "selector_value", "separator", "options", "ai", "dynamic", "show_label", "label_block", "rows", "inner_tab", "tabs_wrapper":
			continue
		case "selectors":
			var pairs []any
			switch s := v.(type) {
			case Ordered:
				for _, kv := range s {
					pairs = append(pairs, []any{kv.K, kv.V})
				}
			case map[string]any:
				keys := make([]string, 0, len(s))
				for k := range s {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					pairs = append(pairs, []any{k, s[k]})
				}
			}
			out[k] = pairs
		default:
			out[k] = jsonRoundTrip(v)
		}
	}
	if c["tab"] == "style" && c["type"] != "section" {
		out["_style"] = true
	}
	return out
}

// jsonRoundTrip turns Ordered and typed slices into the plain values the schema loader produces.
func jsonRoundTrip(v any) any {
	switch v.(type) {
	case string, bool, nil, float64:
		return v
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return v
	}
	return out
}

// registerThemeWidget adds a Site widget to the panel list, the schema and the renderer.
func registerThemeWidget(w *ThemeWidget, def *widgetDef) {
	if proSupersedes[w.Name] { // GoWebEditor Pro ships the full widget (widgets_pro_*.go)
		return
	}
	if len(w.Categories) == 0 {
		w.Categories = []string{"theme-elements"}
	}
	w.WrapperClass = def.wrapperClass
	if w.WrapperClass == "" {
		w.WrapperClass = "elementor-widget-" + w.Name
	}
	themeWidgetList = append(themeWidgetList, w)
	ctrls := make([]map[string]any, 0, len(w.Controls))
	for _, c := range w.Controls {
		ctrls = append(ctrls, schemaControl(c))
	}
	s := loadSchema()
	s.widgets[w.Name] = &widgetSchema{Common: "common", InnerWrapper: true, Controls: ctrls}
	registerWidget(w.Name, def)
}
