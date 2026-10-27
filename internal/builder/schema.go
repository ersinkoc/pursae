package builder

// Control schema: the control stacks of every element/widget/document type as upstream's PHP side
// sees them while generating CSS (dumped from upstream 4.3.5 by testdata/tools/dump-controls.php,
// responsive duplication mode "off"); responsive controls are duplicated per breakpoint here, the
// way Controls_Stack::add_responsive_control does in duplication mode "on".

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
)

//go:embed data/controls.json.gz
var controlsGz []byte

type ctrlType struct {
	Data     bool
	Default  any
	Multiple bool
	Units    bool
	Number   bool
	Media    bool
	Repeater bool
	Global   map[string]any
}

type globalDef struct {
	Active  bool
	Default string
}

type ctrl struct {
	Name        string
	Type        string
	Default     any
	Selectors   [][2]string
	SelDict     map[string]any
	UnitSelDict map[string]any
	Condition   map[string]any
	Conditions  any
	Responsive  *omap // min/max/devices
	PrefixClass string
	HasPrefix   bool
	Global      *globalDef
	GroupType   string
	GroupPrefix string
	Fields      []*ctrl
	FieldsByKey map[string]*ctrl
	Frontend    bool
	RenderType  string
	Parent      string
	ReturnValue any
	ClassesDict map[string]any
	Style       bool
	Dynamic     bool
	ControlType string
	Options     []string
	Assets      map[string]any
	t           *ctrlType
}

type schemaData struct {
	types   map[string]*ctrlType
	stacks  map[string][]map[string]any
	widgets map[string]*widgetSchema
}

type widgetSchema struct {
	Common       string
	InnerWrapper bool
	Controls     []map[string]any
}

var (
	schemaOnce sync.Once
	schema     *schemaData
)

func loadSchema() *schemaData {
	schemaOnce.Do(func() {
		src := controlsGz
		if proControlsGz != nil { // GoWebEditor Pro: the dump with Pro's widgets and element controls (pro_schema.go)
			src = proControlsGz
		}
		r, err := gzip.NewReader(bytes.NewReader(src))
		if err != nil {
			panic("builder: controls data: " + err.Error())
		}
		raw, err := io.ReadAll(r)
		if err != nil {
			panic("builder: controls data: " + err.Error())
		}
		var d struct {
			Types   map[string]map[string]any   `json:"types"`
			Stacks  map[string][]map[string]any `json:"stacks"`
			Widgets map[string]struct {
				Common       string           `json:"common"`
				InnerWrapper bool             `json:"inner_wrapper"`
				Controls     []map[string]any `json:"controls"`
			} `json:"widgets"`
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			panic("builder: controls data: " + err.Error())
		}
		s := &schemaData{types: map[string]*ctrlType{}, stacks: d.Stacks, widgets: map[string]*widgetSchema{}}
		for name, t := range d.Types {
			ct := &ctrlType{Data: t["data"] == true, Default: t["default"]}
			if g, ok := t["global"].(map[string]any); ok {
				ct.Global = g
			}
			cls, _ := t["class"].(string)
			parents := []string{cls}
			for _, p := range asList(t["parents"]) {
				parents = append(parents, phpString(p))
			}
			for _, p := range parents {
				switch p {
				case `Elementor\Control_Base_Multiple`:
					ct.Multiple = true
				case `Elementor\Control_Base_Units`:
					ct.Units = true
				case `Elementor\Control_Number`:
					ct.Number = true
				case `Elementor\Control_Media`:
					ct.Media = true
				case `Elementor\Control_Repeater`:
					ct.Repeater = true
				}
			}
			s.types[name] = ct
		}
		for name, w := range d.Widgets {
			s.widgets[name] = &widgetSchema{Common: w.Common, InnerWrapper: w.InnerWrapper, Controls: w.Controls}
		}
		schema = s
	})
	return schema
}

// device is one entry of the responsive device list.
type device struct {
	Name      string
	Direction string
	Value     int
}

var defaultBreakpoints = []Breakpoint{{Name: "mobile", Value: 767, Direction: "max"}, {Name: "tablet", Value: 1024, Direction: "max"}}

// activeBreakpoints returns the breakpoints sorted by value (upstream's active breakpoints order).
func activeBreakpoints(rc *RenderContext) []Breakpoint {
	var bps []Breakpoint
	if rc != nil && len(rc.Breakpoints) > 0 {
		bps = append(bps, rc.Breakpoints...)
	} else if rc != nil && rc.Kit != nil {
		bps = KitBreakpoints(rc.Kit)
	} else {
		bps = append(bps, defaultBreakpoints...)
	}
	for i := range bps {
		if bps[i].Direction == "" {
			bps[i].Direction = "max"
			if bps[i].Name == "widescreen" {
				bps[i].Direction = "min"
			}
		}
	}
	sort.SliceStable(bps, func(i, j int) bool { return bps[i].Value < bps[j].Value })
	return bps
}

func bpKey(bps []Breakpoint) string {
	var b strings.Builder
	for _, bp := range bps {
		b.WriteString(bp.Name + ":" + strconv.Itoa(bp.Value) + bp.Direction + ";")
	}
	return b.String()
}

// stack is a control stack ready for use.
type stack struct {
	all       []*ctrl // CSS order (controls + style controls), responsive duplicated
	byName    map[string]*ctrl
	display   []*ctrl // non-style controls in order (frontend render)
	displayBy map[string]*ctrl
	frontKeys []string
}

var (
	stackMu    sync.Mutex
	stackCache = map[string]*stack{}
)

// stackFor returns the stack for kind "section", "column", "container", "page", "kit" or
// "widget:<type>". It returns nil for unknown widget types.
func stackFor(kind string, bps []Breakpoint) *stack {
	key := kind + "|" + bpKey(bps)
	stackMu.Lock()
	defer stackMu.Unlock()
	if st, ok := stackCache[key]; ok {
		return st
	}
	s := loadSchema()
	var raws []map[string]any
	if strings.HasPrefix(kind, "widget:") {
		w := s.widgets[strings.TrimPrefix(kind, "widget:")]
		if w == nil {
			stackCache[key] = nil
			return nil
		}
		var own, ownStyle []map[string]any
		for _, c := range w.Controls {
			if c["_style"] == true {
				ownStyle = append(ownStyle, c)
			} else {
				own = append(own, c)
			}
		}
		raws = append(raws, own...)
		if w.Common != "" && s.widgets[w.Common] != nil {
			var cn, cs []map[string]any
			for _, c := range s.widgets[w.Common].Controls {
				if c["_style"] == true {
					cs = append(cs, c)
				} else {
					cn = append(cn, c)
				}
			}
			// array_merge(own controls, common get_controls()) then += own style controls
			var merged []map[string]any
			merged = append(merged, own...)
			idx := map[string]int{}
			for i, c := range merged {
				idx[phpString(c["name"])] = i
			}
			for _, c := range append(cn, cs...) {
				n := phpString(c["name"])
				if i, ok := idx[n]; ok {
					merged[i] = c
					continue
				}
				idx[n] = len(merged)
				merged = append(merged, c)
			}
			for _, c := range ownStyle {
				if _, ok := idx[phpString(c["name"])]; ok {
					continue
				}
				merged = append(merged, c)
			}
			raws = merged
		} else {
			raws = append(raws, ownStyle...)
		}
	} else {
		raws = s.stacks[kind]
		if raws == nil {
			stackCache[key] = nil
			return nil
		}
		// controls first, then style controls (the dump lists them in that order already)
	}
	devs := deviceList(bps)
	expanded := duplicateResponsive(raws, devs)
	st := &stack{byName: map[string]*ctrl{}, displayBy: map[string]*ctrl{}}
	for _, r := range expanded {
		c := buildCtrl(r, s, devs)
		if c == nil {
			continue
		}
		st.all = append(st.all, c)
		st.byName[c.Name] = c
		if !c.Style {
			st.display = append(st.display, c)
			st.displayBy[c.Name] = c
			if c.Frontend {
				st.frontKeys = append(st.frontKeys, c.Name)
			}
		}
	}
	stackCache[key] = st
	return st
}

// deviceList mirrors get_active_devices_list(['reverse' => true, 'desktop_first' => true]).
func deviceList(bps []Breakpoint) []device {
	var out []device
	out = append(out, device{Name: "desktop", Direction: "max"})
	for i := len(bps) - 1; i >= 0; i-- {
		out = append(out, device{Name: bps[i].Name, Direction: bps[i].Direction, Value: bps[i].Value})
	}
	return out
}

func duplicateResponsive(raws []map[string]any, devs []device) []map[string]any {
	var out []map[string]any
	for _, r := range raws {
		if f, ok := r["fields"].([]any); ok {
			var fm []map[string]any
			for _, x := range f {
				if m, ok := x.(map[string]any); ok {
					fm = append(fm, m)
				}
			}
			dup := duplicateResponsive(fm, devs)
			nr := shallowCopy(r)
			lst := make([]any, len(dup))
			for i, d := range dup {
				lst[i] = d
			}
			nr["fields"] = lst
			r = nr
		}
		if r["is_responsive"] != true {
			out = append(out, r)
			continue
		}
		out = append(out, expandResponsive(r, devs)...)
	}
	return out
}

func shallowCopy(m map[string]any) map[string]any {
	n := make(map[string]any, len(m))
	for k, v := range m {
		n[k] = v
	}
	return n
}

func expandResponsive(r map[string]any, devs []device) []map[string]any {
	name := phpString(r["name"])
	args := shallowCopy(r)
	delete(args, "is_responsive")
	resp := newOmap()
	devices := devs
	if rm := asMap(r["responsive"]); rm != nil {
		if dl, ok := rm["devices"]; ok {
			allowed := map[string]bool{}
			for _, d := range asList(dl) {
				allowed[phpString(d)] = true
			}
			var filtered []device
			for _, d := range devs {
				if allowed[d.Name] {
					filtered = append(filtered, d)
				}
			}
			devices = filtered
			resp.Set("devices", dl)
		}
	}
	t := loadSchema().types[phpString(r["type"])]
	var typeDefault any
	if t != nil {
		typeDefault = t.Default
	}
	// desktop default: the stored default, unless only desktop_default was given
	desktopDefault, hasDesktop := args["default"]
	if dd, ok := args["desktop_default"]; ok && (!hasDesktop || strictEq(desktopDefault, typeDefault)) {
		desktopDefault = mergeDefault(typeDefault, dd)
		hasDesktop = true
	}
	var out []map[string]any
	prev := ""
	for _, d := range devices {
		c := shallowCopy(args)
		if prev != "" {
			if strings.HasSuffix(prev, "_widescreen") {
				c["parent"] = name
			} else {
				c["parent"] = prev
			}
		} else {
			c["parent"] = nil
		}
		if da := asMap(args["device_args"]); da != nil {
			if dv := asMap(da[d.Name]); len(dv) > 0 {
				for k, v := range dv {
					c[k] = v
				}
			}
		}
		delete(c, "device_args")
		cr := newOmap()
		for _, k := range resp.keys {
			cr.Set(k, resp.Get(k))
		}
		dir := "max"
		if d.Name != "desktop" {
			dir = d.Direction
		}
		cr.Set(dir, d.Name)
		if mad := asMap(args["min_affected_device"]); mad != nil {
			if v, ok := mad[d.Name]; ok && truthy(v) {
				cr.Set("min", v)
			}
		}
		delete(c, "min_affected_device")
		c["responsive"] = cr
		if d.Name == "desktop" {
			if hasDesktop {
				c["default"] = desktopDefault
			}
		} else if dv, ok := args[d.Name+"_default"]; ok {
			c["default"] = mergeDefault(typeDefault, dv)
		} else {
			c["default"] = deepCopy(typeDefault)
		}
		for k := range c {
			if strings.HasSuffix(k, "_default") {
				delete(c, k)
			}
		}
		cname := name
		if d.Name != "desktop" {
			cname = name + "_" + d.Name
		}
		c["name"] = cname
		// GoWebEditor theme widgets: a responsive prefix_class with %s gets the device suffix, like
		// add_responsive_control's sprintf (elementor-grid%s- -> elementor-grid-tablet-).
		if pc, ok := c["prefix_class"].(string); ok && strings.Contains(pc, "%s") {
			suffix := ""
			if d.Name != "desktop" {
				suffix = "-" + d.Name
			}
			c["prefix_class"] = strings.Replace(pc, "%s", suffix, 1)
		}
		prev = cname
		out = append(out, c)
	}
	return out
}

func mergeDefault(typeDefault, v any) any {
	tm, ok := typeDefault.(map[string]any)
	if !ok {
		return deepCopy(v)
	}
	out := deepCopy(tm).(map[string]any)
	for k, x := range asMap(v) {
		out[k] = deepCopy(x)
	}
	return out
}

func buildCtrl(r map[string]any, s *schemaData, devs []device) *ctrl {
	c := &ctrl{Name: phpString(r["name"]), Type: phpString(r["type"])}
	if c.Type == "" {
		c.Type = "text"
	}
	c.t = s.types[c.Type]
	c.Default = r["default"]
	if sel, ok := r["selectors"].([]any); ok {
		for _, p := range sel {
			if pp, ok := p.([]any); ok && len(pp) == 2 {
				c.Selectors = append(c.Selectors, [2]string{phpString(pp[0]), phpString(pp[1])})
			}
		}
	} else if sm, ok := r["selectors"].(map[string]any); ok {
		// device_args may carry selectors as an (unordered) object
		for _, k := range sortedPlainKeys(sm) {
			c.Selectors = append(c.Selectors, [2]string{k, phpString(sm[k])})
		}
	}
	c.SelDict = asMap(r["selectors_dictionary"])
	c.UnitSelDict = asMap(r["unit_selectors_dictionary"])
	if cond := asMap(r["condition"]); len(cond) > 0 {
		c.Condition = cond
	}
	if cs, ok := r["conditions"]; ok && truthy(cs) {
		c.Conditions = cs
	}
	switch rv := r["responsive"].(type) {
	case *omap:
		c.Responsive = rv
	case map[string]any:
		o := newOmap()
		for _, k := range []string{"devices", "max", "min"} {
			if v, ok := rv[k]; ok {
				o.Set(k, v)
			}
		}
		c.Responsive = o
	}
	if pc, ok := r["prefix_class"]; ok {
		c.HasPrefix = true
		c.PrefixClass = phpString(pc)
	}
	if g := asMap(r["global"]); g != nil {
		c.Global = &globalDef{Active: truthy(g["active"]), Default: phpString(g["default"])}
	}
	c.GroupType = phpString(r["groupType"])
	c.GroupPrefix = phpString(r["groupPrefix"])
	if f, ok := r["fields"].([]any); ok {
		c.FieldsByKey = map[string]*ctrl{}
		for _, x := range f {
			if m, ok := x.(map[string]any); ok {
				fc := buildCtrl(m, s, devs)
				c.Fields = append(c.Fields, fc)
				c.FieldsByKey[fc.Name] = fc
			}
		}
	}
	c.Frontend = truthy(r["frontend_available"])
	c.RenderType = phpString(r["render_type"])
	c.Parent = phpString(r["parent"])
	c.ReturnValue = r["return_value"]
	c.ClassesDict = asMap(r["classes_dictionary"])
	c.Style = r["_style"] == true
	c.Dynamic = r["dynamic"] == true
	c.ControlType = phpString(r["control_type"])
	c.Assets = asMap(r["assets"])
	for _, o := range asList(r["options"]) {
		c.Options = append(c.Options, phpString(o))
	}
	return c
}

func sortedPlainKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (c *ctrl) isData() bool { return c.t != nil && c.t.Data }

func (c *ctrl) isRepeater() bool { return c.t != nil && c.t.Repeater }

func (c *ctrl) isMultiple() bool { return c.t != nil && c.t.Multiple }

// responsiveSuffix mirrors Controls_Manager::get_responsive_control_device_suffix.
func (c *ctrl) responsiveSuffix() string {
	if c.Responsive == nil {
		return ""
	}
	dev := ""
	if v := c.Responsive.Get("max"); truthy(v) {
		dev = phpString(v)
	} else if v := c.Responsive.Get("min"); truthy(v) {
		dev = phpString(v)
	} else {
		return ""
	}
	if dev == "desktop" {
		return ""
	}
	return "_" + dev
}

func (c *ctrl) hasResponsive() bool { return c.Responsive != nil && c.Responsive.Len() > 0 }

// getValue mirrors Base_Data_Control::get_value (and the multiple/gaps/repeater overrides).
func (c *ctrl) getValue(settings map[string]any, has bool, raw any) any {
	def := c.Default
	if def == nil && c.t != nil {
		def = c.t.Default
	}
	var value any
	if has && raw != nil {
		value = raw
	} else {
		value = def
	}
	if c.t == nil {
		return value
	}
	switch {
	case c.t.Repeater:
		items := asList(value)
		if len(items) == 0 {
			return value
		}
		out := make([]any, len(items))
		for i, it := range items {
			im := asMap(it)
			if im == nil {
				out[i] = it
				continue
			}
			nm := make(map[string]any, len(im))
			for k, v := range im {
				nm[k] = v
			}
			for _, f := range c.Fields {
				if !f.isData() {
					continue
				}
				fv, fh := im[f.Name]
				nm[f.Name] = f.getValue(im, fh, fv)
			}
			out[i] = nm
		}
		return out
	case c.t.Multiple:
		merged := map[string]any{}
		for k, v := range asMap(c.t.Default) {
			merged[k] = v
		}
		if dm, ok := c.Default.(map[string]any); ok {
			for k, v := range dm {
				merged[k] = v
			}
		}
		if vm, ok := value.(map[string]any); ok {
			for k, v := range vm {
				merged[k] = v
			}
		}
		if c.Type == "gaps" {
			if sz, ok := merged["size"]; ok && sz != nil && phpString(sz) != "" && strictEq(merged["column"], "") {
				merged["column"] = phpString(sz)
				merged["row"] = phpString(sz)
			}
		}
		return merged
	}
	return value
}

// initSettings mirrors Controls_Stack::get_init_settings over the given controls.
func initSettings(raw *omap, controls []*ctrl) *omap {
	out := newOmap()
	if raw != nil {
		for _, k := range raw.keys {
			out.Set(k, raw.m[k])
		}
	}
	for _, c := range controls {
		if !c.isData() {
			continue
		}
		v, has := out.m[c.Name]
		out.Set(c.Name, c.getValue(out.m, has, v))
	}
	return out
}
