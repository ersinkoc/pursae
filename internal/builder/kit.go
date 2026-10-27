package builder

// Kit (site settings) support: global colors/typography lookups used by the CSS engine and the kit
// stylesheet (post-<kitID>.css).

import (
	"strconv"
)

type kitGlobals struct {
	colors            map[string]any            // id -> color value
	typography        map[string]map[string]any // id -> typography_* values
	genericFonts      string
	colorsEnabled     bool
	typographyEnabled bool
}

func kitRawSettings(rc *RenderContext) map[string]any {
	if rc != nil && rc.Kit != nil && rc.Kit.Settings != nil {
		return rc.Kit.Settings
	}
	return map[string]any{}
}

func kitGlobalsFor(rc *RenderContext) *kitGlobals {
	raw := kitRawSettings(rc)
	st := stackFor("kit", activeBreakpoints(rc))
	g := &kitGlobals{colors: map[string]any{}, typography: map[string]map[string]any{}, genericFonts: "Sans-serif", colorsEnabled: true, typographyEnabled: true}
	if rc != nil {
		g.colorsEnabled = !rc.DisableGlobalColors
		g.typographyEnabled = !rc.DisableGlobalTypography
	}
	if v, ok := raw["default_generic_fonts"]; ok {
		g.genericFonts = phpString(v)
	}
	itemsOf := func(sysKey, customKey string) []any {
		var items []any
		if v, ok := raw[sysKey]; ok {
			items = append(items, asList(v)...)
		} else if st != nil && st.byName[sysKey] != nil {
			items = append(items, asList(st.byName[sysKey].Default)...)
		}
		items = append(items, asList(raw[customKey])...)
		return items
	}
	for _, it := range itemsOf("system_colors", "custom_colors") {
		m := asMap(it)
		if m == nil {
			continue
		}
		g.colors[phpString(m["_id"])] = m["color"]
	}
	for _, it := range itemsOf("system_typography", "custom_typography") {
		m := asMap(it)
		if m == nil {
			continue
		}
		v := map[string]any{}
		for k, x := range m {
			if k == "_id" || k == "title" {
				continue
			}
			if len(k) > 7 && k[:7] == "styles_" {
				k = k[7:]
			}
			v[k] = x
		}
		g.typography[phpString(m["_id"])] = v
	}
	return g
}

func init() {
	KitCSS = renderKitCSS
}

// renderKitCSS renders post-<kitID>.css.
func renderKitCSS(kit *Document, rc *RenderContext) string {
	if kit == nil {
		return ""
	}
	if rc == nil {
		rc = &RenderContext{}
	}
	lrc := *rc
	if lrc.Kit == nil {
		lrc.Kit = kit
	}
	if len(lrc.Breakpoints) == 0 {
		lrc.Breakpoints = KitBreakpoints(kit)
	}
	g := newCSSGen(&lrc)
	for _, el := range kit.Elements {
		if el != nil {
			g.renderStyles(el, kit.ID)
		}
	}
	g.renderDocumentStyles("kit", kit.Settings, settingsOrder(kit), ".elementor-kit-"+strconv.FormatInt(kit.ID, 10))
	return sanitizeInlineCSS(g.sheet.String())
}

// KitBreakpoints mirrors Breakpoints\Manager: mobile and tablet are always active, the other
// breakpoints when listed in the kit's active_breakpoints; values come from viewport_<name>.
func KitBreakpoints(kit *Document) []Breakpoint {
	config := []Breakpoint{
		{Name: "mobile", Value: 767, Direction: "max"},
		{Name: "mobile_extra", Value: 880, Direction: "max"},
		{Name: "tablet", Value: 1024, Direction: "max"},
		{Name: "tablet_extra", Value: 1200, Direction: "max"},
		{Name: "laptop", Value: 1366, Direction: "max"},
		{Name: "widescreen", Value: 2400, Direction: "min"},
	}
	var s map[string]any
	if kit != nil {
		s = kit.Settings
	}
	active := map[string]bool{"viewport_mobile": true, "viewport_tablet": true}
	if v, ok := s["active_breakpoints"]; ok {
		active = map[string]bool{}
		for _, x := range asList(v) {
			active[phpString(x)] = true
		}
	}
	var out []Breakpoint
	for _, bp := range config {
		if bp.Name != "mobile" && bp.Name != "tablet" && !active["viewport_"+bp.Name] {
			continue
		}
		if v := s["viewport_"+bp.Name]; truthy(v) {
			bp.Value = toInt(v)
		}
		out = append(out, bp)
	}
	return out
}
