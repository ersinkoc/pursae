package builder

// The CSS engine: a port of upstream's Core\Files\CSS\Base / Post and upstream Stylesheet.

import (
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---- Stylesheet (includes/stylesheet.php) ----

type cssRuleSet struct {
	sels  []string
	props map[string]*omap
}

type stylesheet struct {
	bps    []Breakpoint
	hashes []string
	rules  map[string]*cssRuleSet
	raw    []string // Stylesheet::add_raw_css (GoWebEditor Pro custom CSS), printed after the rules
}

func newStylesheet(bps []Breakpoint) *stylesheet {
	return &stylesheet{bps: bps, rules: map[string]*cssRuleSet{}}
}

type queryPart struct{ endpoint, device string }

func (s *stylesheet) addRules(selector, styleRules string, query []queryPart) {
	hash := "all"
	if len(query) > 0 {
		var parts []string
		for _, q := range query {
			parts = append(parts, q.endpoint+"_"+q.device)
		}
		hash = strings.Join(parts, "-")
	}
	if _, ok := s.rules[hash]; !ok {
		s.addQueryHash(hash)
	}
	rs := s.rules[hash]
	if _, ok := rs.props[selector]; !ok {
		rs.sels = append(rs.sels, selector)
		rs.props[selector] = newOmap()
	}
	ordered := newOmap()
	for _, rule := range strings.Split(phpTrim(styleRules, " \t\n\r\x00\x0B"), ";") {
		if rule == "" || rule == "0" {
			continue
		}
		p := strings.SplitN(rule, ":", 2)
		if len(p) < 2 {
			break
		}
		ordered.Set(phpTrim(p[0], " \t\n\r\x00\x0B"), phpTrim(p[1], " ;"))
	}
	for _, k := range ordered.keys {
		rs.props[selector].Set(k, ordered.m[k])
	}
}

func phpTrim(s, chars string) string { return strings.Trim(s, chars) }

func (s *stylesheet) addQueryHash(hash string) {
	s.rules[hash] = &cssRuleSet{props: map[string]*omap{}}
	s.hashes = append(s.hashes, hash)
	cmp := func(a, b string) int {
		if a == "all" {
			return -1
		}
		if b == "all" {
			return 1
		}
		aq := s.hashToQuery(a)
		bq := s.hashToQuery(b)
		_, amin := aq["min"]
		_, bmin := bq["min"]
		if amin != bmin {
			return 1
		}
		if amin {
			r := aq["min"] - bq["min"]
			if r != 0 {
				return r
			}
			_, amax := aq["max"]
			_, bmax := bq["max"]
			if amax != bmax {
				if amax {
					return 1
				}
				return -1
			}
			if !amax {
				return 0
			}
		}
		return bq["max"] - aq["max"]
	}
	zendSort(s.hashes, cmp)
}

// zendSort mirrors PHP 8's zend_sort (insertion sort for small arrays) with uksort's stable
// fallback, so the order of media query blocks matches even for upstream's inconsistent comparator.
func zendSort(items []string, cmp func(a, b string) int) {
	pos := map[string]int{}
	for i, it := range items {
		pos[it] = i
	}
	c := func(a, b string) int {
		r := cmp(a, b)
		if r == 0 {
			return pos[a] - pos[b]
		}
		return r
	}
	n := len(items)
	if n > 16 {
		sort.SliceStable(items, func(i, j int) bool { return c(items[i], items[j]) < 0 })
		return
	}
	swp := func(i, j int) { items[i], items[j] = items[j], items[i] }
	sort2 := func(a, b int) {
		if c(items[a], items[b]) > 0 {
			swp(a, b)
		}
	}
	sort3 := func(a, b, d int) {
		if !(c(items[a], items[b]) > 0) {
			if !(c(items[b], items[d]) > 0) {
				return
			}
			swp(b, d)
			if c(items[a], items[b]) > 0 {
				swp(a, b)
			}
			return
		}
		if !(c(items[d], items[b]) > 0) {
			swp(a, d)
			return
		}
		swp(a, b)
		if c(items[b], items[d]) > 0 {
			swp(b, d)
		}
	}
	sort4 := func(a, b, d, e int) {
		sort3(a, b, d)
		if c(items[d], items[e]) > 0 {
			swp(d, e)
			if c(items[b], items[d]) > 0 {
				swp(b, d)
				if c(items[a], items[b]) > 0 {
					swp(a, b)
				}
			}
		}
	}
	sort5 := func(a, b, d, e, f int) {
		sort4(a, b, d, e)
		if c(items[e], items[f]) > 0 {
			swp(e, f)
			if c(items[d], items[e]) > 0 {
				swp(d, e)
				if c(items[b], items[d]) > 0 {
					swp(b, d)
					if c(items[a], items[b]) > 0 {
						swp(a, b)
					}
				}
			}
		}
	}
	switch n {
	case 0, 1:
		return
	case 2:
		sort2(0, 1)
		return
	case 3:
		sort3(0, 1, 2)
		return
	case 4:
		sort4(0, 1, 2, 3)
		return
	case 5:
		sort5(0, 1, 2, 3, 4)
		return
	}
	sentry := 6
	for i := 1; i < sentry; i++ {
		j := i - 1
		if !(c(items[j], items[i]) > 0) {
			continue
		}
		for j != 0 {
			j--
			if !(c(items[j], items[i]) > 0) {
				j++
				break
			}
		}
		for k := i; k > j; k-- {
			swp(k, k-1)
		}
	}
	for i := sentry; i < n; i++ {
		j := i - 1
		if !(c(items[j], items[i]) > 0) {
			continue
		}
		for {
			j -= 2
			if j < 0 {
				j = 0
				break
			}
			if !(c(items[j], items[i]) > 0) {
				j++
				if !(c(items[j], items[i]) > 0) {
					j++
				}
				break
			}
			if j == 0 {
				break
			}
			if j == 1 {
				j--
				if c(items[i], items[j]) > 0 {
					j++
				}
				break
			}
		}
		for k := i; k > j; k-- {
			swp(k, k-1)
		}
	}
}

func (s *stylesheet) deviceValue(name string) (int, bool) {
	for _, bp := range s.bps {
		if bp.Name == name {
			return bp.Value, true
		}
	}
	return 0, false
}

// deviceMinBreakpoint mirrors Breakpoints\Manager::get_device_min_breakpoint.
func (s *stylesheet) deviceMinBreakpoint(name string) int {
	if name == "desktop" {
		// desktop min point = previous device (the last max breakpoint) + 1
		prev := 0
		for _, bp := range s.bps {
			if bp.Name == "widescreen" {
				break
			}
			prev = bp.Value
		}
		return prev + 1
	}
	for i, bp := range s.bps {
		if bp.Name != name {
			continue
		}
		if i == 0 {
			return 320
		}
		if bp.Direction == "min" {
			return bp.Value
		}
		return s.bps[i-1].Value + 1
	}
	return 0
}

func (s *stylesheet) hashToQuery(hash string) map[string]int {
	q := map[string]int{}
	for _, part := range s.hashParts(hash) {
		if part.endpoint == "max" {
			v, _ := s.deviceValue(part.device)
			q["max"] = v
		} else {
			q["min"] = s.deviceMinBreakpoint(part.device)
		}
	}
	return q
}

var hashPartRe = regexp.MustCompile(`(min|max)_(.*)`)

func (s *stylesheet) hashParts(hash string) []queryPart {
	var out []queryPart
	for _, p := range strings.Split(hash, "-") {
		if p == "" {
			continue
		}
		m := hashPartRe.FindStringSubmatch(p)
		if m == nil {
			continue
		}
		out = append(out, queryPart{m[1], m[2]})
	}
	return out
}

func (s *stylesheet) String() string {
	var b strings.Builder
	for _, hash := range s.hashes {
		rs := s.rules[hash]
		var body strings.Builder
		for _, sel := range rs.sels {
			props := rs.props[sel]
			var content strings.Builder
			for _, k := range props.keys {
				v := phpString(props.m[k])
				if v != "" {
					content.WriteString(k + ":" + v + ";")
				}
			}
			if content.Len() > 0 {
				body.WriteString(sel + "{" + content.String() + "}")
			}
		}
		if hash != "all" {
			var parts []string
			for _, p := range s.hashParts(hash) {
				var v int
				if p.endpoint == "max" {
					v, _ = s.deviceValue(p.device)
				} else {
					v = s.deviceMinBreakpoint(p.device)
				}
				parts = append(parts, "("+p.endpoint+"-width:"+strconv.Itoa(v)+"px)")
			}
			b.WriteString("@media" + strings.Join(parts, " and ") + "{" + body.String() + "}")
		} else {
			b.WriteString(body.String())
		}
	}
	b.WriteString(strings.Join(s.raw, "\n")) // GoWebEditor Pro: raw custom CSS
	return b.String()
}

// ---- CSS file generation (core/files/css/base.php + post.php) ----

type cssGen struct {
	rc      *RenderContext
	bps     []Breakpoint
	sheet   *stylesheet
	fonts   []string
	icons   []string
	globals *kitGlobals
	// stackEl is the element whose controls are being processed: Post::add_controls_stack_style_rules
	// re-renders an element's children after every repeater item (nested widgets rely on it).
	stackEl     *Element
	stackPostID int64
}

func newCSSGen(rc *RenderContext) *cssGen {
	bps := activeBreakpoints(rc)
	return &cssGen{rc: rc, bps: bps, sheet: newStylesheet(bps), globals: kitGlobalsFor(rc)}
}

func (g *cssGen) addFont(v any) {
	f := phpString(v)
	for _, x := range g.fonts {
		if x == f {
			return
		}
	}
	g.fonts = append(g.fonts, f)
}

var errSkipControl = errors.New("skip control")

var placeholderRe = regexp.MustCompile(`{{(?:([^.}]+)\.)?([^}| ]*)(?: *\|\| *(?:([^.}]+)\.)?([^}| ]*) *)*}}`)

var devicePrefixRe = regexp.MustCompile(`^(?:\([^\)]+\)){1,2}`)

var deviceRuleRe = regexp.MustCompile(`\(([^)]+)\)`)

var globalPropRe = regexp.MustCompile(`(:)[^;]+(;?)`)

var quotedRe = regexp.MustCompile(`^(['"])(.*)(['"])$`)

type valueCallback func(c *ctrl) any

// addControlRules mirrors Base::add_control_rules.
func (g *cssGen) addControlRules(c *ctrl, stackCtrls map[string]*ctrl, cb valueCallback, placeholders, replacements []string, values map[string]any) {
	if len(c.Selectors) == 0 {
		return
	}
	globalKeyName := c.Name
	if c.GroupType != "" {
		globalKeyName = c.GroupPrefix + c.GroupType
	}
	globalKey := ""
	if gv := asMap(values["__globals__"]); gv != nil {
		if k, ok := gv[globalKeyName]; ok && truthy(k) {
			globalKey = phpString(k)
		}
	}
	var value any
	if globalKey == "" {
		value = cb(c)
		if value == nil {
			return
		}
	}
	for _, sp := range c.Selectors {
		selector, cssProperty := sp[0], sp[1]
		output := ""
		if globalKey != "" {
			if gval, ok := g.selectorGlobalValue(c, globalKey); ok && gval != "" {
				output = globalPropRe.ReplaceAllStringFunc(cssProperty, func(m string) string {
					sm := globalPropRe.FindStringSubmatch(m)
					return sm[1] + gval + sm[2]
				})
			}
		} else {
			if c.UnitSelDict != nil {
				if unit, ok := phpKey(sub(value, "unit")); ok && isArray(value) {
					if p, ok := c.UnitSelDict[unit]; ok && p != nil {
						cssProperty = phpString(p)
					}
				}
			}
			var err error
			output, err = g.replacePlaceholders(c, cssProperty, value, stackCtrls, cb)
			if err != nil {
				return
			}
		}
		if output == "" || output == "0" {
			continue
		}
		var query []queryPart
		if dr := devicePrefixRe.FindString(selector); dr != "" {
			selector = selector[len(dr):]
			for _, m := range deviceRuleRe.FindAllStringSubmatch(dr, -1) {
				rule := m[1]
				if rule == "desktop" {
					continue
				}
				dev := strings.TrimSuffix(rule, "+")
				ep := "max"
				if dev != rule {
					ep = "min"
				}
				query = setQuery(query, ep, dev)
			}
		}
		parsed := selector
		for i, ph := range placeholders {
			parsed = strings.ReplaceAll(parsed, ph, replacements[i])
		}
		if len(query) == 0 && c.Responsive != nil {
			for _, k := range c.Responsive.keys {
				if k == "min" || k == "max" {
					v := phpString(c.Responsive.Get(k))
					if k == "max" && v == "desktop" {
						continue
					}
					query = append(query, queryPart{k, v})
				}
			}
		}
		g.sheet.addRules(parsed, output, query)
	}
}

func setQuery(q []queryPart, ep, dev string) []queryPart {
	for i := range q {
		if q[i].endpoint == ep {
			q[i].device = dev
			return q
		}
	}
	return append(q, queryPart{ep, dev})
}

func (g *cssGen) replacePlaceholders(c *ctrl, cssProperty string, value any, stackCtrls map[string]*ctrl, cb valueCallback) (string, error) {
	var failed error
	out := placeholderRe.ReplaceAllStringFunc(cssProperty, func(match string) string {
		if failed != nil {
			return ""
		}
		idx := placeholderRe.FindStringSubmatchIndex(match)
		grp := func(n int) (string, bool) {
			if idx[2*n] < 0 {
				return "", false
			}
			return match[idx[2*n]:idx[2*n+1]], true
		}
		m1, _ := grp(1)
		m2, _ := grp(2)
		m3, _ := grp(3)
		m4, has4 := grp(4)
		externalMissing := m1 != "" && stackCtrls[m1] == nil
		parsed := ""
		if !externalMissing {
			parsed = g.parsePropertyPlaceholder(c, value, stackCtrls, cb, m2, m1)
		}
		if parsed == "" {
			if has4 {
				parsed = m4
				if qm := quotedRe.FindStringSubmatch(parsed); qm != nil && qm[1] == qm[3] {
					parsed = qm[2]
				} else if !numericString(parsed) {
					if m3 != "" && stackCtrls[m3] == nil {
						return ""
					}
					parsed = g.parsePropertyPlaceholder(c, value, stackCtrls, cb, m4, m3)
				}
			}
			if parsed == "" {
				if externalMissing {
					return ""
				}
				failed = errSkipControl
				return ""
			}
		}
		if parsed == "__EMPTY__" {
			parsed = ""
		}
		return parsed
	})
	return out, failed
}

// parsePropertyPlaceholder mirrors Base::parse_property_placeholder.
func (g *cssGen) parsePropertyPlaceholder(c *ctrl, value any, stackCtrls map[string]*ctrl, cb valueCallback, placeholder, parserControl string) string {
	if parserControl != "" {
		other := stackCtrls[parserControl]
		if c.hasResponsive() && other != nil && other.hasResponsive() {
			if dc := stackCtrls[parserControl+c.responsiveSuffix()]; dc != nil {
				other = dc
			}
		}
		if other == nil {
			return ""
		}
		c = other
		value = cb(c)
	}
	if isEmpty(value) && !strictEq(value, "0") && !strictEq(value, float64(0)) {
		if gv, ok := g.controlGlobalDefault(c); ok {
			value = gv
		} else {
			value = nil
		}
	}
	if c.Type == "font" {
		g.addFont(value)
	}
	return phpString(g.styleValue(c, placeholder, value))
}

// styleValue mirrors the control types' get_style_value.
func (g *cssGen) styleValue(c *ctrl, placeholder string, value any) any {
	t := c.t
	if t == nil {
		return value
	}
	if t.Media && placeholder == "URL" && isArray(value) && truthy(sub(value, "id")) {
		size := phpString(sub(value, "size"))
		if size == "" {
			size = "full"
		}
		if g.rc != nil && g.rc.MediaURL != nil {
			if u := g.rc.MediaURL(int64(toInt(sub(value, "id"))), size); u != "" {
				return u
			}
		}
		return sub(value, "url")
	}
	if t.Multiple {
		var ret any
		if m := asMap(value); m != nil {
			ret = m[strings.ToLower(placeholder)]
		} else if s, ok := value.(string); ok {
			// a global value (var(...)) string: PHP string offset -> first char; treat as the value
			ret = s
		}
		if t.Units && placeholder == "UNIT" && strictEq(ret, "custom") {
			return "__EMPTY__"
		}
		return ret
	}
	if t.Number {
		if placeholder == "DEFAULT" {
			return c.Default
		}
		if value == nil || strictEq(value, "") {
			return value
		}
		if !isNumeric(value) {
			if truthy(c.Default) {
				return c.Default
			}
			return ""
		}
		return value
	}
	if placeholder == "DEFAULT" {
		return c.Default
	}
	return value
}

// styleControlValue mirrors Base::get_style_control_value.
func (g *cssGen) styleControlValue(c *ctrl, values map[string]any) any {
	if gv := asMap(values["__globals__"]); gv != nil {
		if k, ok := gv[c.Name]; ok && truthy(k) {
			if v, ok := g.selectorGlobalValue(c, phpString(k)); ok {
				return v
			}
			return nil
		}
	}
	value := values[c.Name]
	if c.SelDict != nil && !isArray(value) {
		if k, ok := phpKey(value); ok {
			if dv, ok := c.SelDict[k]; ok && dv != nil {
				value = dv
			}
		}
	}
	if !isNumeric(value) && isEmpty(value) {
		return nil
	}
	return value
}

// controlGlobalDefault mirrors Base::get_control_global_default_value.
func (g *cssGen) controlGlobalDefault(c *ctrl) (string, bool) {
	if c.Global == nil || c.Global.Default == "" {
		return "", false
	}
	enabled := false
	if c.Type == "color" {
		enabled = g.globals.colorsEnabled
	} else if c.GroupType == "typography" {
		enabled = g.globals.typographyEnabled
	}
	if !enabled {
		return "", false
	}
	return g.selectorGlobalValue(c, c.Global.Default)
}

// selectorGlobalValue mirrors Base::get_selector_global_value.
func (g *cssGen) selectorGlobalValue(c *ctrl, globalKey string) (string, bool) {
	kind, id, ok := strings.Cut(globalKey, "?id=")
	if !ok {
		return "", false
	}
	kind = strings.TrimPrefix(kind, "globals/")
	if c.GroupType != "" {
		item := g.globals.typography[id]
		if kind != "typography" || item == nil {
			return "", false
		}
		prop := strings.ReplaceAll(c.Name, c.GroupPrefix, "")
		for _, bp := range g.bps {
			prop = strings.ReplaceAll(prop, "_"+bp.Name, "")
		}
		if isEmpty(item["typography_"+prop]) {
			return "", false
		}
		v := "var( --e-global-" + c.GroupType + "-" + id + "-" + strings.ReplaceAll(prop, "_", "-") + " )"
		if c.Name == c.GroupPrefix+"font_family" && g.globals.genericFonts != "" {
			v += ", " + g.globals.genericFonts
		}
		return v, true
	}
	if kind != "colors" {
		return "", false
	}
	if isEmpty(g.globals.colors[id]) {
		return "", false
	}
	return "var( --e-global-" + c.Type + "-" + id + " )", true
}

// styleControls mirrors Base::get_style_controls (active data controls) for a settings map.
type styleCtrl struct {
	c           *ctrl
	styleFields [][]styleCtrl
}

func (g *cssGen) styleControls(controls []*ctrl, byName map[string]*ctrl, settings map[string]any) []styleCtrl {
	parsed := parseGlobalSettings(settings, controls)
	var out []styleCtrl
	for _, c := range controls {
		if !c.isData() {
			continue
		}
		if !isVisible(c, parsed, byName) {
			continue
		}
		sc := styleCtrl{c: c}
		if c.isRepeater() {
			for _, it := range asList(settings[c.Name]) {
				im := asMap(it)
				if im == nil {
					im = map[string]any{}
				}
				sc.styleFields = append(sc.styleFields, g.styleControls(c.Fields, c.FieldsByKey, im))
			}
		}
		if len(c.Selectors) > 0 || c.Dynamic || len(sc.styleFields) > 0 || isGlobalControl(c, byName, settings) {
			out = append(out, sc)
		}
	}
	return out
}

func isGlobalControl(c *ctrl, byName map[string]*ctrl, settings map[string]any) bool {
	key := c.Name
	if c.GroupType != "" {
		key = c.GroupPrefix + c.GroupType
	}
	gc := byName[key]
	if gc == nil || gc.Global == nil || !gc.Global.Active {
		return false
	}
	return truthy(sub(settings["__globals__"], key))
}

func parseGlobalSettings(settings map[string]any, controls []*ctrl) map[string]any {
	out := make(map[string]any, len(settings))
	for k, v := range settings {
		out[k] = v
	}
	globals := asMap(settings["__globals__"])
	for _, c := range controls {
		if !c.isData() {
			continue
		}
		if c.isRepeater() {
			items := asList(out[c.Name])
			if items != nil {
				nl := make([]any, len(items))
				for i, it := range items {
					if im := asMap(it); im != nil {
						nl[i] = parseGlobalSettings(im, c.Fields)
					} else {
						nl[i] = it
					}
				}
				out[c.Name] = nl
			}
			continue
		}
		if c.Global == nil || !c.Global.Active {
			continue
		}
		if globals == nil || !truthy(globals[c.Name]) {
			continue
		}
		out[c.Name] = "global"
	}
	return out
}

// addStackStyleRules mirrors Base::add_controls_stack_style_rules for one settings map.
func (g *cssGen) addStackStyleRules(scs []styleCtrl, values map[string]any, placeholders, replacements []string, all map[string]*ctrl) {
	dynamic := asMap(values["__dynamic__"])
	for _, sc := range scs {
		c := sc.c
		if len(sc.styleFields) > 0 {
			items := asList(values[c.Name])
			phs := append(append([]string(nil), placeholders...), "{{CURRENT_ITEM}}")
			for i, fields := range sc.styleFields {
				if i >= len(items) {
					break
				}
				im := asMap(items[i])
				if im == nil {
					continue
				}
				reps := append(append([]string(nil), replacements...), ".elementor-repeater-item-"+phpString(im["_id"]))
				g.addStackStyleRules(fields, im, phs, reps, c.FieldsByKey)
				if el := g.stackEl; el != nil {
					for _, ch := range el.Elements {
						if ch != nil {
							g.renderStyles(ch, g.stackPostID)
						}
					}
					g.stackEl = el
				}
			}
		}
		if c.Type == "icons" {
			g.icons = append(g.icons, phpString(sub(values[c.Name], "library")))
		}
		if dynamic != nil && truthy(dynamic[c.Name]) {
			continue
		}
		if len(c.Selectors) == 0 {
			continue
		}
		g.addControlRules(c, all, func(x *ctrl) any { return g.styleControlValue(x, values) }, placeholders, replacements, values)
	}
}

// ---- Post CSS (post-<id>.css) ----

type cssElement struct {
	el       *Element
	name     string
	st       *stack
	settings *omap
}

// renderStyles mirrors Post::render_styles for an element and its children.
func (g *cssGen) renderStyles(el *Element, postID int64) {
	name, st := elementStack(el, g.bps)
	if st == nil {
		return
	}
	settings := initSettings(rawSettings(el), st.all)
	// global defaults per element type (render_element_global_styles)
	globalValues := map[string]any{}
	var globalCtrls []*ctrl
	for _, c := range st.all {
		isColor := c.Type == "color"
		isTypo := c.GroupType == "typography"
		if (isColor && !g.globals.colorsEnabled) || (isTypo && !g.globals.typographyEnabled) {
			continue
		}
		gc := c
		if c.GroupType != "" {
			gc = st.byName[c.GroupPrefix+c.GroupType]
		}
		if c.Global != nil && c.Global.Default != "" {
			globalValues[c.Name] = c.Global.Default
		}
		if gc != nil && gc.Global != nil && gc.Global.Default != "" {
			globalCtrls = append(globalCtrls, c)
		}
	}
	gv := map[string]any{"__globals__": globalValues}
	for _, c := range globalCtrls {
		g.addControlRules(c, st.byName, func(*ctrl) any { return nil }, []string{"{{WRAPPER}}"}, []string{".elementor-widget-" + name}, gv)
	}
	// element styles
	scs := g.styleControls(st.all, st.byName, settings.m)
	wrapper := ".elementor-" + strconv.FormatInt(postID, 10) + " .elementor-element.elementor-element-" + el.ID
	g.stackEl, g.stackPostID = el, postID
	if el.globalOf != "" { // GoWebEditor Pro: a global widget's styles live in its template's CSS
		scs = nil
	}
	g.addStackStyleRules(scs, settings.m, []string{"{{ID}}", "{{WRAPPER}}"}, []string{el.ID, wrapper}, st.byName)
	g.stackEl = nil
	proElementCustomCSS(g, el, name, settings.m, wrapper) // GoWebEditor Pro: custom CSS
	for _, ch := range el.Elements {
		if ch != nil {
			g.renderStyles(ch, postID)
		}
	}
}

// renderDocumentStyles adds the document (page settings / kit) rules.
func (g *cssGen) renderDocumentStyles(kind string, settings map[string]any, rawOrder []string, wrapper string) {
	st := stackFor(kind, g.bps)
	if st == nil {
		return
	}
	raw := newOmap()
	for _, k := range rawOrder {
		raw.Set(k, settings[k])
	}
	for _, k := range sortedPlainKeys(settings) {
		if !raw.Has(k) {
			raw.Set(k, settings[k])
		}
	}
	all := initSettings(raw, st.all)
	scs := g.styleControls(st.all, st.byName, all.m)
	g.stackEl = nil
	g.addStackStyleRules(scs, all.m, []string{"{{WRAPPER}}"}, []string{wrapper}, st.byName)
	proDocumentCustomCSS(g, all.m, wrapper) // GoWebEditor Pro: page / kit custom CSS
}

// elementStack returns the element's name (as used by .elementor-widget-<name>) and stack.
func elementStack(el *Element, bps []Breakpoint) (string, *stack) {
	switch el.ElType {
	case "widget":
		if el.globalOf != "" { // GoWebEditor Pro global widget: the original widget's controls
			return el.WidgetType, stackFor("widget:"+el.globalOf, bps)
		}
		return el.WidgetType, stackFor("widget:"+el.WidgetType, bps)
	case "section", "column", "container":
		return el.ElType, stackFor(el.ElType, bps)
	}
	return el.ElType, nil
}

// PostCSS renders post-<id>.css for a document.
func postCSS(doc *Document, rc *RenderContext) (string, []string, []string) {
	g := newCSSGen(rc)
	for _, el := range doc.Elements {
		if el != nil {
			g.renderStyles(el, doc.ID)
		}
	}
	kind, wrapper := documentCSSKind(doc)
	g.renderDocumentStyles(kind, doc.Settings, settingsOrder(doc), wrapper)
	return sanitizeInlineCSS(g.sheet.String()), g.fonts, g.icons
}

func documentCSSKind(doc *Document) (string, string) {
	id := strconv.FormatInt(doc.ID, 10)
	if kind, wrapper, ok := proDocumentCSSKind(doc); ok { // GoWebEditor Pro: popups, loop items
		return kind, wrapper
	}
	if doc.Type == "kit" {
		return "kit", ".elementor-kit-" + id
	}
	return "page", "body.elementor-page-" + id
}

var styleCloseRe = regexp.MustCompile(`(?i)</(style)`)

func sanitizeInlineCSS(css string) string {
	return styleCloseRe.ReplaceAllString(css, `<\/$1`)
}
