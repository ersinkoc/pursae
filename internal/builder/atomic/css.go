package atomic

// The atomic styles engine: Styles_Renderer (style definitions → CSS), the per-breakpoint
// grouping of Atomic_Styles_Manager, and the style sources it registers — the elements' base
// styles, the default (per tag) styles, a document's global classes and its local styles.

import (
	"encoding/base64"
	"strconv"
	"strings"
)

// Breakpoint is an active responsive breakpoint.
type Breakpoint struct {
	Name      string // mobile, tablet, ...
	Value     int
	Direction string // max or min
}

// GlobalClass is a global class (id g-xxxxxxx, label = CSS class name).
type GlobalClass struct {
	ID       string
	Label    string
	Variants []any
}

// GlobalClasses is the ordered set of global classes of a context (frontend or preview).
type GlobalClasses struct {
	Items []GlobalClass
}

// Label maps a class id to its label.
func (g *GlobalClasses) Label(id string) (string, bool) {
	if g == nil {
		return "", false
	}
	for _, c := range g.Items {
		if c.ID == id {
			return c.Label, true
		}
	}
	return "", false
}

// StylesFor returns the style definitions of the given class ids for a document's global
// stylesheet: in reverse global order (the first class wins), with the label as id
// (Atomic_Global_Styles::get_document_global_styles).
func (g *GlobalClasses) StylesFor(ids []string) []*Map {
	if g == nil {
		return nil
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []*Map
	for i := len(g.Items) - 1; i >= 0; i-- {
		c := g.Items[i]
		if !want[c.ID] {
			continue
		}
		out = append(out, MapOf("id", c.Label, "label", c.Label, "type", "class", "variants", c.Variants))
	}
	return out
}

// VariableValue converts a stored variable value (v2 {$$type,value}) to the v1 string the client
// and the CSS use (sizes become "12px", custom sizes their raw value).
func VariableValue(v any) string {
	m, _, ok := transformable(v)
	if !ok {
		return phpString(v)
	}
	val := m.Get("value")
	if vm := asMap(val); vm != nil && vm.Has("unit") {
		unit := phpString(vm.Get("unit"))
		if unit == "custom" {
			return phpString(vm.Get("size"))
		}
		if unit == "" {
			unit = "px"
		}
		return phpString(vm.Get("size")) + unit
	}
	return phpString(val)
}

// VariablesCSS mirrors Variables CSS_Renderer::raw_css (appended to the kit stylesheet).
func VariablesCSS(ids []string, vars map[string]Variable) string {
	var entries []string
	for _, id := range ids {
		v := vars[id]
		name := id
		if !v.Deleted {
			name = strings.TrimSpace(v.Label)
		}
		value := strings.TrimSpace(v.Value)
		if value == "" || name == "" {
			continue
		}
		entries = append(entries, "--"+name+":"+value+";")
	}
	if len(entries) == 0 {
		return ""
	}
	return ":root { " + strings.Join(entries, " ") + " }"
}

// BaseStyles returns every atomic element's base style definitions (Atomic_Widget_Base_Styles).
func BaseStyles() []*Map {
	var out []*Map
	for _, name := range data().order {
		et := data().elements[name]
		for _, k := range et.BaseStyles.Keys() {
			if m := asMap(et.BaseStyles.Get(k)); m != nil {
				out = append(out, m)
			}
		}
	}
	return out
}

// LocalStyles collects the "styles" of every atomic element in a tree, children first.
func LocalStyles(nodes []*Node) []*Map {
	var out []*Map
	var walk func([]*Node)
	walk = func(list []*Node) {
		for _, n := range list {
			walk(n.Children) // DB::iterate_data visits children before their parent
			if Lookup(n.Type()) != nil {
				for _, k := range n.Styles.Keys() {
					if m := asMap(n.Styles.Get(k)); m != nil {
						out = append(out, m)
					}
				}
			}
		}
	}
	walk(nodes)
	return out
}

// ClassIDs collects the class ids an atomic tree references (classes props), unique, in order.
func ClassIDs(nodes []*Node) []string {
	var out []string
	seen := map[string]bool{}
	var walk func([]*Node)
	walk = func(list []*Node) {
		for _, n := range list {
			if et := Lookup(n.Type()); et != nil {
				for _, key := range et.Schema.Keys {
					pt := et.Schema.Props[key]
					if pt.Kind != "plain" || pt.Key != "classes" {
						continue
					}
					for _, id := range listOf(get(n.Settings.Get(key), "value")) {
						s, ok := id.(string)
						if ok && s != "" && !seen[s] {
							seen[s] = true
							out = append(out, s)
						}
					}
				}
			}
			walk(n.Children)
		}
	}
	walk(nodes)
	return out
}

// ComponentIDs collects the component ids instanced in a tree (e-component widgets).
func ComponentIDs(nodes []*Node) []int64 {
	var out []int64
	seen := map[int64]bool{}
	var walk func([]*Node)
	walk = func(list []*Node) {
		for _, n := range list {
			if n.Type() == "e-component" {
				if id := intOf(get(n.Settings.Get("component_instance"), "value", "component_id", "value")); id > 0 && !seen[id] {
					seen[id] = true
					out = append(out, id)
				}
			}
			walk(n.Children)
		}
	}
	walk(nodes)
	return out
}

// CSSFile is one generated stylesheet: <handle>.css, enqueued with the given media.
type CSSFile struct {
	Handle string
	Media  string
	CSS    string
}

// StyleSource is one registered style path (base, default, global-<id>-<ctx>, local-<id>-<ctx>).
type StyleSource struct {
	Key    string
	Styles []*Map
}

// CSSFiles mirrors Atomic_Styles_Manager::render: for desktop and every active breakpoint (widest
// first), each source's variants of that breakpoint rendered into <key>-<breakpoint>.css; empty
// files are skipped.
func (e *Env) CSSFiles(sources []StyleSource, bps []Breakpoint) []CSSFile {
	order := []string{"desktop"}
	media := map[string]string{"desktop": "all"}
	for i := len(bps) - 1; i >= 0; i-- {
		order = append(order, bps[i].Name)
		media[bps[i].Name] = mediaQuery(bps[i])
	}
	var out []CSSFile
	grouped := make([]map[string][]*Map, len(sources))
	for i, s := range sources {
		grouped[i] = groupByBreakpoint(s.Styles)
	}
	for _, bp := range order {
		for i, s := range sources {
			css := e.RenderStyles(grouped[i][bp], bps)
			if css == "" {
				continue
			}
			out = append(out, CSSFile{Handle: s.Key + "-" + bp, Media: media[bp], CSS: css})
		}
	}
	return out
}

func mediaQuery(bp Breakpoint) string {
	bound := "max-width"
	if bp.Direction == "min" {
		bound = "min-width"
	}
	return "@media(" + bound + ":" + strconv.Itoa(bp.Value) + "px)"
}

// MediaAttr is the media attribute of a breakpoint's stylesheet link ("(max-width:767px)").
func MediaAttr(m string) string { return strings.TrimPrefix(m, "@media") }

func groupByBreakpoint(styles []*Map) map[string][]*Map {
	out := map[string][]*Map{}
	index := map[string]map[string]*Map{}
	for _, st := range styles {
		id := phpString(st.Get("id"))
		for _, v := range listOf(st.Get("variants")) {
			vm := asMap(v)
			bp := phpString(get(vm, "meta", "breakpoint"))
			if bp == "" {
				bp = "desktop"
			}
			if index[bp] == nil {
				index[bp] = map[string]*Map{}
			}
			g := index[bp][id]
			if g == nil {
				g = MapOf("id", st.Get("id"), "type", st.Get("type"), "variants", []any{})
				if st.Has("cssName") {
					g.Set("cssName", st.Get("cssName"))
				}
				index[bp][id] = g
				out[bp] = append(out[bp], g)
			}
			g.Set("variants", append(listOf(g.Get("variants")), vm))
		}
	}
	return out
}

// RenderStyles mirrors Styles_Renderer::render with the ".elementor" selector prefix.
func (e *Env) RenderStyles(styles []*Map, bps []Breakpoint) string {
	var b strings.Builder
	r := &resolver{env: e, transformers: styleTransformers()}
	for _, st := range styles {
		if phpString(st.Get("type")) != "class" || phpEmpty(st.Get("id")) {
			continue
		}
		name := phpString(st.Get("id"))
		if st.Has("cssName") && st.Get("cssName") != nil {
			name = phpString(st.Get("cssName"))
		}
		base := ".elementor ." + name
		for _, v := range listOf(st.Get("variants")) {
			b.WriteString(e.variantCSS(r, base, asMap(v), bps))
		}
	}
	return b.String()
}

func (e *Env) variantCSS(r *resolver, base string, v *Map, bps []Breakpoint) string {
	css := e.propsCSS(r, asMap(v.Get("props")))
	custom := ""
	if raw := phpString(get(v, "custom_css", "raw")); raw != "" {
		if dec, err := base64.StdEncoding.DecodeString(raw); err == nil {
			custom = string(dec) + `\n`
		} else {
			custom = `\n`
		}
	}
	if css == "" && custom == "" {
		return ""
	}
	selector := base
	if st, ok := get(v, "meta").(*Map); ok && st.Get("state") != nil {
		selector = selectorWithState(base, phpString(st.Get("state")))
	}
	out := selector + "{" + css + custom + "}"
	if bp := phpString(get(v, "meta", "breakpoint")); bp != "" {
		for _, x := range bps {
			if x.Name == bp {
				out = mediaQuery(x) + "{" + out + "}"
			}
		}
	}
	return out
}

func (e *Env) propsCSS(r *resolver, props *Map) string {
	if props == nil {
		props = NewMap()
	}
	schema := StyleSchema()
	resolved := r.resolve(schema, props)
	var b strings.Builder
	for _, k := range resolved.Keys() {
		v := resolved.Get(k)
		if !(isNumeric(v) || !phpEmpty(v)) {
			continue
		}
		if k == "font-family" && e.Font != nil {
			if fm, typ, ok := transformable(props.Get(k)); ok && (typ == "font-family" || typ == "global-font-variable") {
				f := strings.TrimSpace(phpString(fm.Get("value")))
				if typ == "global-font-variable" {
					f = ""
					if e.Variable != nil {
						if v, ok := e.Variable(phpString(fm.Get("value"))); ok && !v.Deleted {
							f = strings.TrimSpace(v.Value)
						}
					}
				}
				if isQuoted(f) {
					f = strings.TrimSpace(f[1 : len(f)-1])
				}
				if f != "" {
					e.Font(f)
				}
			}
		}
		b.WriteString(k + ":" + phpString(v) + ";")
	}
	return b.String()
}

var pseudoStates = map[string]bool{"hover": true, "active": true, "focus": true, "focus-visible": true, "checked": true}
var classStates = map[string]bool{"e--selected": true, "e--disabled": true, "e--playing": true, "e--paused": true}

// selectorWithState mirrors Style_States::get_selector_with_state.
func selectorWithState(base, state string) string {
	states := []string{state}
	if state == "hover" {
		states = append(states, "focus-visible")
	}
	var parts []string
	for _, s := range states {
		switch {
		case classStates[s]:
			parts = append(parts, base+"."+s)
		case pseudoStates[s]:
			parts = append(parts, base+":"+s)
		default:
			parts = append(parts, base+s)
		}
	}
	return strings.Join(parts, ",")
}

// StylePropValues resolves a style variant's props to their CSS values (Render_Props_Resolver
// for styles), keyed by CSS property; props that resolve empty are left out.
func (e *Env) StylePropValues(props any) map[string]string {
	r := &resolver{env: e, transformers: styleTransformers()}
	m := asMap(FromAny(props))
	if m == nil {
		m = NewMap()
	}
	out := map[string]string{}
	resolved := r.resolve(StyleSchema(), m)
	for _, k := range resolved.Keys() {
		if v := resolved.Get(k); !phpEmpty(v) {
			out[k] = phpString(v)
		}
	}
	return out
}
