package atomic

import (
	"fmt"
	"strconv"
	"strings"
)

// Env is what rendering needs from the site. Every callback may be nil.
type Env struct {
	// Editing is true for the editor (render_atomic_element and the preview iframe).
	Editing bool
	// AssetsURL is the URL of the editor assets folder (replaces @@GWEASSETS@@ in defaults).
	AssetsURL string
	// Image resolves an attachment (wp_get_attachment_image_src / srcset / alt).
	Image func(id int64, size string) (img Image, ok bool)
	// AttachmentURL is wp_get_attachment_url.
	AttachmentURL func(id int64) string
	// SVG returns the markup of an SVG attachment or a local/remote SVG URL.
	SVG func(id int64, url string) string
	// Permalink resolves a post id link destination.
	Permalink func(id int64) string
	// Variable looks up a global variable by id: its label, value, type and whether it is deleted.
	Variable func(id string) (v Variable, ok bool)
	// ClassLabel maps a global class id to its label (the CSS class name); ok false for ids that
	// are not global classes.
	ClassLabel func(id string) (string, bool)
	// Component loads a component's element tree (raw _elementor_data JSON).
	Component func(id int64) ([]byte, bool)
	// RenderChild renders a non-atomic child element (the v3 renderer); the argument is Node.Ref.
	RenderChild func(n *Node) string
	// Font is called for every font family a style uses.
	Font func(family string)
	// Visible evaluates an element's display conditions on the front end (Pro's display-conditions
	// module, before_element_render): conditions is the JSON the v3 control stores in
	// e_display_conditions ([[{condition, comparator, ...}, ...], ...], OR of AND groups). Elements
	// whose conditions fail are not printed, children included. Nil = always visible.
	Visible func(n *Node, conditions string) bool

	Loop *LoopHooks // gap-loops: the atomic Loop's site data (loop.go)

	ctx map[string][]*Map // Render_Context stacks
}

// Image is a resolved attachment image.
type Image struct {
	URL    string
	Width  int
	Height int
	Srcset string
	Alt    string
}

// Variable is a global variable (v1 client shape: value is a plain string).
type Variable struct {
	Label   string
	Value   string
	Type    string
	Deleted bool
}

func (e *Env) pushCtx(key string, m *Map) {
	if e.ctx == nil {
		e.ctx = map[string][]*Map{}
	}
	e.ctx[key] = append(e.ctx[key], m)
}

func (e *Env) popCtx(key string) {
	if l := e.ctx[key]; len(l) > 0 {
		e.ctx[key] = l[:len(l)-1]
	}
}

func (e *Env) getCtx(key string) *Map {
	if l := e.ctx[key]; len(l) > 0 {
		return l[len(l)-1]
	}
	return NewMap()
}

func (e *Env) assets(s string) string {
	return strings.ReplaceAll(s, "@@GWEASSETS@@", e.AssetsURL)
}

func intOf(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n
	case bool:
		if x {
			return 1
		}
	}
	return 0
}

// ---- settings transformers --------------------------------------------------------------------

func settingsTransformers() map[string]transformer {
	return map[string]transformer{
		"classes": func(r *resolver, v any, _ string, _ *PropType) any {
			if !isArray(v) {
				return nil
			}
			out := []any{}
			for _, id := range listOf(v) {
				s := phpString(id)
				if r.env.ClassLabel != nil {
					if l, ok := r.env.ClassLabel(s); ok {
						s = l
					}
				}
				if !phpEmpty(s) {
					out = append(out, s)
				}
			}
			return out
		},
		"image":        imageTransformer,
		"image-src":    imageSrcTransformer,
		"svg-src":      svgSrcTransformer,
		"icon":         func(*resolver, any, string, *PropType) any { return MapOf("html", "", "url", nil) },
		"video-src":    videoSrcTransformer,
		"link":         linkTransformer,
		"query":        func(_ *resolver, v any, _ string, _ *PropType) any { return get(v, "id") },
		"attributes":   attributesTransformer,
		"date-time":    dateTimeTransformer,
		"date-range":   minMaxTransformer,
		"time-range":   minMaxTransformer,
		"number-range": minMaxTransformer,
		"html-v2":      func(_ *resolver, v any, _ string, _ *PropType) any { return contentOf(v) },
		"html-v3":      func(_ *resolver, v any, _ string, _ *PropType) any { return contentOf(v) },
		"escaped-html": func(_ *resolver, v any, _ string, _ *PropType) any {
			s, ok := v.(string)
			if !ok {
				return ""
			}
			return ksesInline(s)
		},
		"component-instance": componentInstanceTransformer,
		"overridable":        overridableTransformer,
		"override": func(_ *resolver, v any, _ string, _ *PropType) any {
			return MapOf("override_key", get(v, "override_key"), "override_value", get(v, "override_value"))
		},
		"display-conditions": func(*resolver, any, string, *PropType) any { return nil },
		"dynamic":            loopDynamic, // gap-loops: dynamic tags (loop.go)
	}
}

func contentOf(v any) any {
	c := get(v, "content")
	if c == nil {
		return ""
	}
	return c
}

func imageSrcTransformer(_ *resolver, v any, _ string, _ *PropType) any {
	m := asMap(v)
	var id any
	if x := m.Get("id"); x != nil {
		id = float64(intOf(x))
	}
	return MapOf("id", id, "url", m.Get("url"), "alt", m.Get("alt"))
}

func imageTransformer(r *resolver, v any, _ string, _ *PropType) any {
	src := asMap(get(v, "src"))
	size := phpString(get(v, "size"))
	if size == "" {
		size = "full"
	}
	if id := intOf(src.Get("id")); id > 0 {
		if r.env.Image == nil {
			return nil
		}
		img, ok := r.env.Image(id, size)
		if !ok {
			return nil // "Cannot get image src." (the resolver drops the prop)
		}
		srcset := any(img.Srcset)
		if img.Srcset == "" {
			srcset = false
		}
		return MapOf("id", src.Get("id"), "src", img.URL, "width", float64(img.Width), "height", float64(img.Height), "srcset", srcset, "alt", img.Alt)
	}
	url := phpString(src.Get("url"))
	if url == "" {
		return nil
	}
	alt := src.Get("alt")
	if alt == nil {
		alt = ""
	}
	return MapOf("src", r.env.assets(url), "alt", alt)
}

func svgSrcTransformer(r *resolver, v any, _ string, _ *PropType) any {
	m := asMap(v)
	id := intOf(m.Get("id"))
	url := phpString(m.Get("url"))
	if id > 0 && r.env.AttachmentURL != nil {
		if u := r.env.AttachmentURL(id); u != "" {
			url = u
		}
	}
	url = r.env.assets(url)
	var content string
	if r.env.SVG != nil {
		content = r.env.SVG(id, url)
	}
	html := ""
	if content != "" {
		html = processSVG(content)
	}
	var u any = url
	if url == "" {
		u = nil
	}
	return MapOf("html", html, "url", u)
}

func videoSrcTransformer(r *resolver, v any, _ string, _ *PropType) any {
	m := asMap(v)
	var id any
	url := m.Get("url")
	if n := intOf(m.Get("id")); n > 0 {
		id = float64(n)
		if r.env.AttachmentURL != nil {
			url = r.env.AttachmentURL(n)
		}
	}
	return MapOf("id", id, "url", url)
}

func linkTransformer(r *resolver, v any, _ string, _ *PropType) any {
	m := asMap(v)
	dest := m.Get("destination")
	var href any = dest
	if isNumeric(dest) {
		href = nil
		if r.env.Permalink != nil {
			if p := r.env.Permalink(intOf(dest)); p != "" {
				href = p
			}
		}
		if href == nil {
			href = dest
		}
	}
	target := "_self"
	if m.Get("isTargetBlank") == true {
		target = "_blank"
	}
	tag := "a"
	if !phpEmpty(href) && m.Get("tag") == "button" {
		tag = "button"
	}
	out := NewMap()
	if !phpEmpty(href) {
		out.Set("href", href)
	}
	out.Set("target", target)
	out.Set("tag", tag)
	return out
}

// attributesTransformer is Pro's Pro_Attributes_Transformer: key="value" pairs.
func attributesTransformer(_ *resolver, v any, _ string, _ *PropType) any {
	if !isArray(v) {
		return nil
	}
	var parts []string
	for _, it := range listOf(v) {
		m := asMap(it)
		k, val := m.Get("key"), m.Get("value")
		if k == nil || val == nil || phpString(k) == "" || phpString(val) == "" {
			parts = append(parts, "")
			continue
		}
		parts = append(parts, phpString(k)+`="`+phpString(val)+`"`)
	}
	return strings.Join(parts, " ")
}

func dateTimeTransformer(_ *resolver, v any, _ string, _ *PropType) any {
	m := asMap(v)
	if m == nil {
		return nil
	}
	d := strings.TrimSpace(phpString(m.Get("date")))
	t := strings.TrimSpace(phpString(m.Get("time")))
	if d == "" && t == "" {
		return ""
	}
	return escAttr(strings.TrimSpace(d + " " + t))
}

func minMaxTransformer(_ *resolver, v any, _ string, _ *PropType) any {
	if phpEmpty(v) {
		return nil
	}
	m := asMap(v)
	out := NewMap()
	for _, k := range []string{"min", "max"} {
		if phpEmpty(m.Get(k)) {
			out.Set(k, nil)
		} else {
			out.Set(k, m.Get(k))
		}
	}
	return out
}

func overridableTransformer(r *resolver, v any, _ string, _ *PropType) any {
	m := asMap(v)
	key := phpString(m.Get("override_key"))
	origin := m.Get("origin_value")
	overrides := asMap(r.env.getCtx("overridable").Get("overrides"))
	if ov, ok := overrides.Lookup(key); ok && ov != nil {
		if om, typ, ok := transformable(origin); ok && typ == "override" {
			inner := asMap(om.Get("value"))
			return MapOf("override_key", inner.Get("override_key"), "override_value", ov)
		}
		return ov
	}
	return origin
}

// ---- style transformers -----------------------------------------------------------------------

func styleTransformers() map[string]transformer {
	t := map[string]transformer{
		"font-family": func(_ *resolver, v any, _ string, _ *PropType) any {
			s, ok := v.(string)
			if !ok {
				return nil
			}
			s = strings.TrimSpace(s)
			if s == "" || isQuoted(s) {
				return s
			}
			if strings.ContainsAny(s, " \t\n\r\f\v") {
				return `"` + s + `"`
			}
			return s
		},
		"size": func(_ *resolver, v any, _ string, _ *PropType) any {
			m := asMap(v)
			size, unit := m.Get("size"), phpString(m.Get("unit"))
			switch unit {
			case "custom":
				return size
			case "auto":
				return "auto"
			}
			if !isNumeric(size) {
				return nil
			}
			return phpNumber(toFloat(size)) + unit
		},
		"grid-track-size": func(_ *resolver, v any, _ string, _ *PropType) any {
			m := asMap(v)
			size, unit := m.Get("size"), phpString(m.Get("unit"))
			switch unit {
			case "custom":
				return size
			case "fr":
				return formatRepeat(int(toFloat(size)))
			}
			return phpNumber(toFloat(size)) + unit
		},
		"box-shadow":          combineArray(","),
		"gradient-color-stop": combineArray(","),
		"shadow": func(_ *resolver, v any, _ string, _ *PropType) any {
			m := asMap(v)
			var parts []string
			for _, k := range []string{"hOffset", "vOffset", "blur", "spread", "color", "position"} {
				if x := m.Get(k); !phpEmpty(x) {
					parts = append(parts, phpString(x))
				}
			}
			return strings.Join(parts, " ")
		},
		"flex": func(_ *resolver, v any, _ string, _ *PropType) any {
			m := asMap(v)
			has := func(k string) bool { x := m.Get(k); return x != nil && x != "" }
			if !has("flexGrow") && !has("flexShrink") && !has("flexBasis") {
				return nil
			}
			grow, shrink, basis := "0", "1", "auto"
			if has("flexGrow") {
				grow = phpString(m.Get("flexGrow"))
			}
			if has("flexShrink") {
				shrink = phpString(m.Get("flexShrink"))
			}
			if has("flexBasis") {
				b := m.Get("flexBasis")
				if bm := asMap(b); bm != nil && bm.Has("size") {
					basis = phpString(bm.Get("size")) + phpString(bm.Get("unit"))
				} else {
					basis = phpString(b)
				}
			}
			return grow + " " + shrink + " " + basis
		},
		"stroke": func(_ *resolver, v any, _ string, _ *PropType) any {
			m := asMap(v)
			c, w := phpString(m.Get("color")), phpString(m.Get("width"))
			return multiProps{MapOf("-webkit-text-stroke", w+" "+c, "stroke", m.Get("color"), "stroke-width", m.Get("width"))}
		},
		"image":     imageTransformer,
		"image-src": imageSrcTransformer,
		"span": func(_ *resolver, v any, _ string, _ *PropType) any {
			if v == nil {
				return nil
			}
			return strings.TrimSpace(phpString(v))
		},
		"background-image-overlay": func(_ *resolver, v any, _ string, _ *PropType) any {
			m := asMap(v)
			if !m.Has("image") || m.Get("image") == nil {
				return ""
			}
			src := phpString(get(m.Get("image"), "src"))
			return MapOf("src", `url("`+src+`")`, "repeat", m.Get("repeat"), "attachment", m.Get("attachment"), "size", m.Get("size"), "position", m.Get("position"))
		},
		"background-image-size-scale": func(_ *resolver, v any, _ string, _ *PropType) any {
			m := asMap(v)
			w, h := m.Get("width"), m.Get("height")
			if w == nil {
				w = "auto"
			}
			if h == nil {
				h = "auto"
			}
			return phpString(w) + " " + phpString(h)
		},
		"background-image-position-offset": positionTransformer,
		"object-position":                  positionTransformer,
		"background-color-overlay": func(_ *resolver, v any, _ string, _ *PropType) any {
			c := phpString(get(v, "color"))
			if c == "" {
				return nil
			}
			return "linear-gradient(" + c + ", " + c + ")"
		},
		"background-overlay": backgroundOverlayTransformer,
		"background": func(_ *resolver, v any, _ string, _ *PropType) any {
			m := asMap(v)
			out := NewMap()
			if ov := asMap(m.Get("background-overlay")); ov != nil {
				for _, k := range ov.Keys() {
					out.Set(k, ov.Get(k))
				}
			}
			out.Set("background-color", m.Get("color"))
			out.Set("background-clip", m.Get("clip"))
			return multiProps{out}
		},
		"background-gradient-overlay": func(_ *resolver, v any, _ string, _ *PropType) any {
			m := asMap(v)
			stops := phpString(m.Get("stops"))
			if phpString(m.Get("type")) == "radial" {
				return "radial-gradient(circle at " + phpString(m.Get("positions")) + ", " + stops + ")"
			}
			return fmt.Sprintf("linear-gradient(%ddeg, %s)", int64(toFloat(m.Get("angle"))), stops)
		},
		"filter":          filterTransformer,
		"backdrop-filter": filterTransformer,
		"transition":      transitionTransformer,
		"color-stop": func(_ *resolver, v any, _ string, _ *PropType) any {
			m := asMap(v)
			off := m.Get("offset")
			if off == nil || off == "" {
				return phpString(m.Get("color"))
			}
			return phpString(m.Get("color")) + " " + phpString(off) + "%"
		},
		"transform-move": func(_ *resolver, v any, _ string, _ *PropType) any {
			m := asMap(v)
			return fmt.Sprintf("translate3d(%s, %s, %s)", orStr(m.Get("x"), "0px"), orStr(m.Get("y"), "0px"), orStr(m.Get("z"), "0px"))
		},
		"transform-scale": func(_ *resolver, v any, _ string, _ *PropType) any {
			m := asMap(v)
			return fmt.Sprintf("scale3d(%s, %s, %s)", orStr(m.Get("x"), "1"), orStr(m.Get("y"), "1"), orStr(m.Get("z"), "1"))
		},
		"transform-rotate": func(_ *resolver, v any, _ string, _ *PropType) any {
			m := asMap(v)
			return fmt.Sprintf("rotateX(%s) rotateY(%s) rotateZ(%s)", orStr(m.Get("x"), "0deg"), orStr(m.Get("y"), "0deg"), orStr(m.Get("z"), "0deg"))
		},
		"transform-skew": func(_ *resolver, v any, _ string, _ *PropType) any {
			m := asMap(v)
			return fmt.Sprintf("skew(%s, %s)", orStr(m.Get("x"), "0deg"), orStr(m.Get("y"), "0deg"))
		},
		"transform-functions": func(_ *resolver, v any, _ string, _ *PropType) any {
			var parts []string
			for _, x := range listOf(v) {
				parts = append(parts, phpString(x))
			}
			return strings.Join(parts, " ")
		},
		"transform-origin": func(_ *resolver, v any, _ string, _ *PropType) any {
			m := asMap(v)
			x, y, z := orStr(m.Get("x"), "0px"), orStr(m.Get("y"), "0px"), orStr(m.Get("z"), "0px")
			if x == "50%" && y == "50%" && z == "0px" {
				return nil
			}
			return x + " " + y + " " + z
		},
		"perspective-origin": func(_ *resolver, v any, _ string, _ *PropType) any {
			m := asMap(v)
			return orStr(m.Get("x"), "0px") + " " + orStr(m.Get("y"), "0px")
		},
		"transform": multiPropsTransformer([]string{"transform-functions", "transform-origin", "perspective", "perspective-origin"}, func(_, k string) string {
			if k == "transform-functions" {
				return "transform"
			}
			return k
		}),
		"border-radius-v2": multiPropsTransformer([]string{"start-start", "start-end", "end-start", "end-end"}, func(_, k string) string { return "border-" + k + "-radius" }),
		"border-width-v2":  multiPropsTransformer([]string{"block-start", "block-end", "inline-start", "inline-end"}, func(_, k string) string { return "border-" + k + "-width" }),
		"layout-direction": multiPropsTransformer([]string{"column", "row"}, func(p, k string) string { return k + "-" + p }),
		"dimensions":       multiPropsTransformer([]string{"block-start", "block-end", "inline-start", "inline-end"}, func(p, k string) string { return p + "-" + k }),
		"dynamic":          func(*resolver, any, string, *PropType) any { return nil },
	}
	for _, k := range []string{"global-color-variable", "global-font-variable", "global-size-variable", "global-custom-size-variable"} {
		t[k] = variableTransformer
	}
	return t
}

func isQuoted(s string) bool {
	return len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'')
}

func orStr(v any, def string) string {
	if v == nil {
		return def
	}
	return phpString(v)
}

func formatRepeat(n int) any {
	if n < 1 {
		return nil
	}
	return "repeat(" + strconv.Itoa(n) + ", 1fr)"
}

func positionTransformer(_ *resolver, v any, _ string, _ *PropType) any {
	m := asMap(v)
	return orStr(m.Get("x"), "0px") + " " + orStr(m.Get("y"), "0px")
}

func combineArray(sep string) transformer {
	return func(_ *resolver, v any, _ string, _ *PropType) any {
		if !isArray(v) {
			return nil
		}
		var parts []string
		for _, x := range listOf(v) {
			if !phpEmpty(x) {
				parts = append(parts, phpString(x))
			}
		}
		return strings.Join(parts, sep)
	}
}

func multiPropsTransformer(keys []string, name func(prop, key string) string) transformer {
	return func(_ *resolver, v any, key string, _ *PropType) any {
		m := asMap(v)
		out := NewMap()
		for _, k := range keys {
			if x := m.Get(k); x != nil {
				out.Set(name(key, k), x)
			}
		}
		return multiProps{out}
	}
}

func backgroundOverlayTransformer(_ *resolver, v any, _ string, _ *PropType) any {
	var items []*Map
	for _, it := range listOf(v) {
		var m *Map
		if s, ok := it.(string); ok {
			m = MapOf("src", s, "repeat", nil, "attachment", nil, "size", nil, "position", nil)
		} else {
			m = asMap(it)
		}
		if m != nil && !phpEmpty(m.Get("src")) {
			items = append(items, m)
		}
	}
	if len(items) == 0 {
		return nil
	}
	values := func(prop, def string, noUnify bool) string {
		empty := true
		for _, it := range items {
			if it.Get(prop) != nil {
				empty = false
				break
			}
		}
		if empty {
			return def
		}
		var vals []string
		for _, it := range items {
			if x := it.Get(prop); x != nil {
				vals = append(vals, phpString(x))
			} else {
				vals = append(vals, def)
			}
		}
		if !noUnify {
			same := true
			for _, x := range vals {
				if x != vals[0] {
					same = false
					break
				}
			}
			if same {
				return vals[0]
			}
		}
		return strings.Join(vals, ",")
	}
	return MapOf(
		"background-image", values("src", "none", true),
		"background-repeat", values("repeat", "repeat", false),
		"background-attachment", values("attachment", "scroll", false),
		"background-size", values("size", "auto auto", false),
		"background-position", values("position", "0% 0%", false),
	)
}

func filterTransformer(_ *resolver, v any, _ string, _ *PropType) any {
	var parts []string
	for _, it := range listOf(v) {
		m := asMap(it)
		fn := phpString(m.Get("func"))
		args := asMap(m.Get("args"))
		if fn == "drop-shadow" {
			parts = append(parts, fmt.Sprintf("drop-shadow(%s %s %s %s)", orStr(args.Get("xAxis"), "0px"), orStr(args.Get("yAxis"), "0px"),
				orStr(args.Get("blur"), "10px"), orStr(args.Get("color"), "transparent")))
			continue
		}
		parts = append(parts, fn+"("+phpString(args.Get("size"))+")")
	}
	return strings.Join(parts, " ")
}

func transitionTransformer(_ *resolver, v any, _ string, _ *PropType) any {
	if !isArray(v) {
		return ""
	}
	var parts []string
	for _, it := range listOf(v) {
		m := asMap(it)
		sel, size := m.Get("selection"), m.Get("size")
		if phpEmpty(sel) || phpEmpty(size) {
			continue
		}
		prop := get(sel, "value")
		if phpEmpty(prop) {
			continue
		}
		parts = append(parts, strings.TrimSpace(phpString(prop)+" "+phpString(size)))
	}
	return strings.Join(parts, ", ")
}

func variableTransformer(r *resolver, v any, key string, _ *PropType) any {
	id := phpString(v)
	if r.env.Variable == nil {
		return nil
	}
	vr, ok := r.env.Variable(id)
	if !ok {
		return nil
	}
	if key == "grid-template-columns" || key == "grid-template-rows" {
		return formatRepeat(int(intOf(strings.TrimSpace(vr.Value))))
	}
	if vr.Deleted {
		return "var(--" + id + ")"
	}
	label := strings.TrimSpace(vr.Label)
	if label == "" {
		return nil
	}
	return "var(--" + label + ")"
}
