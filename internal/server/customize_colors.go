package server

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Customizer colors of the bundled themes: the controls each theme's inc/customizer.php registers in
// the Colors section, and the CSS the theme prints for the saved theme mods (ported from
// twentysixteen/inc/customizer.php, twentyseventeen/inc/color-patterns.php,
// twentytwenty/inc/custom-css.php and twentytwentyone/classes/class-twenty-twenty-one-custom-colors.php).

// czControl is one customizer control backed by a theme mod.
type czControl struct {
	ID, Label, Description string
	// Type is select, radio, color, hue, json (hidden, filled by the theme's script), checkbox ("1" or
	// "0", stored as a boolean), number (an int within Min and Max), text or textarea.
	Type    string
	Choices [][2]string
	Default string
	Hidden  bool // starts hidden (shown by the theme's script)
	// Section is the pane of a control in czThemeSpec.Controls: title_tagline (Site Identity) or the
	// id of one of the spec's Sections.
	Section  string
	Min, Max int
	// Sanitize cleans a text or textarea value; without it tags are stripped.
	Sanitize func(string) string
}

// czSection is a theme's own Customizer section (add_section()).
type czSection struct {
	ID, Title, Description string
}

// czThemeSpec is what the Customizer knows about a theme.
type czThemeSpec struct {
	Colors []czControl
	// Controls are the theme's controls outside the Colors section, each in its Section, and Sections
	// the theme's own sections (listed after Colors).
	Controls []czControl
	Sections []czSection
	// PostMessage lists setting ids the theme's preview script applies in place (transport
	// postMessage); every other change reloads the preview.
	PostMessage []string
	// BackgroundDefault is add_theme_support( 'custom-background' )'s default-color (no #); nil when the
	// theme has no custom background.
	BackgroundDefault func(mods map[string]any) string
	// Script runs on the Customizer screen (like the theme's customize-controls.js).
	Script string
	// Scripts are extra script URLs the theme's control script needs.
	Scripts []string
}

var czSpecs = map[string]*czThemeSpec{
	"twentysixteen": {
		Colors: []czControl{
			{ID: "color_scheme", Label: "Base Color Scheme", Type: "select", Default: "default",
				Choices: [][2]string{{"default", "Default"}, {"dark", "Dark"}, {"gray", "Gray"}, {"red", "Red"}, {"yellow", "Yellow"}}},
			{ID: "background_color", Label: "Background Color", Type: "color"},
			{ID: "page_background_color", Label: "Page Background Color", Type: "color"},
			{ID: "link_color", Label: "Link Color", Type: "color"},
			{ID: "main_text_color", Label: "Main Text Color", Type: "color"},
			{ID: "secondary_text_color", Label: "Secondary Text Color", Type: "color"},
		},
		BackgroundDefault: func(mods map[string]any) string { return strings.TrimPrefix(tt16Scheme(mods)[0], "#") },
		Script:            tt16ControlsJS,
	},
	"twentyseventeen": {
		Colors: []czControl{
			{ID: "colorscheme", Label: "Color Scheme", Type: "radio", Default: "light",
				Choices: [][2]string{{"light", "Light"}, {"dark", "Dark"}, {"custom", "Custom"}}},
			{ID: "colorscheme_hue", Label: "Hue", Type: "hue", Default: "250", Hidden: true},
		},
		Script: `wpgoCustomize.toggleWith( 'mod:colorscheme', 'mod:colorscheme_hue', function( v ) { return 'custom' === v; } );`,
	},
	"twentytwenty": {
		Colors: []czControl{
			{ID: "background_color", Label: "Background Color", Type: "color"},
			{ID: "header_footer_background_color", Label: "Header & Footer Background Color", Type: "color", Default: "#ffffff"},
			{ID: "accent_hue_active", Label: "Primary Color", Type: "radio", Default: "default",
				Choices: [][2]string{{"default", "Default"}, {"custom", "Custom"}}},
			{ID: "accent_hue", Label: "Custom Color", Description: "Apply a custom color for links, buttons, featured images.", Type: "hue", Default: "344", Hidden: true},
			{ID: "accent_accessible_colors", Type: "json"},
		},
		BackgroundDefault: func(map[string]any) string { return "f5efe0" },
		Script:            tt20ControlsJS,
		Scripts:           []string{"/wp-content/themes/twentytwenty/assets/js/color-calculations.js?ver=2.9"},
	},
	"twentytwentyone": {
		Colors: []czControl{
			{ID: "background_color", Label: "Background color", Type: "color"},
		},
		BackgroundDefault: func(map[string]any) string { return "d1e4dd" },
	},
}

// control finds a theme mod's control in Colors or Controls.
func (sp *czThemeSpec) control(id string) *czControl {
	for _, list := range [][]czControl{sp.Colors, sp.Controls} {
		for i := range list {
			if list[i].ID == id {
				return &list[i]
			}
		}
	}
	return nil
}

// Theme mods ------------------------------------------------------------------------

// themeMods is get_theme_mods(): the theme_mods_<slug> option.
func (s *Server) themeMods(slug string) map[string]any {
	out := map[string]any{}
	if m, ok := s.settings.Get("theme_mods_" + slug).(map[string]any); ok {
		for k, v := range m {
			out[k] = v
		}
	}
	if snap := gweSnapshotFor(slug); snap != nil && snap.CustomCSS != "" { // live preview of a saved Golementor theme
		out["custom_css"] = snap.CustomCSS
	}
	return out
}

func modString(mods map[string]any, key, def string) string {
	switch v := mods[key].(type) {
	case string:
		if v != "" {
			return v
		}
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return def
}

// modBool reads a checkbox theme mod (a boolean, or "1"/"0" from older saves).
func modBool(mods map[string]any, key string, def bool) bool {
	switch v := mods[key].(type) {
	case bool:
		return v
	case string:
		return v == "1" || v == "true"
	case float64:
		return v != 0
	}
	return def
}

func modInt(mods map[string]any, key string, def int) int {
	switch v := mods[key].(type) {
	case float64:
		return int(v)
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

var hexColorRe = regexp.MustCompile(`^#?([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// sanitizeHex is sanitize_hex_color(): "#abc" or "#aabbcc", or "" when invalid.
func sanitizeHex(v string) string {
	v = strings.TrimSpace(v)
	if !hexColorRe.MatchString(v) {
		return ""
	}
	return "#" + strings.ToLower(strings.TrimPrefix(v, "#"))
}

// hexRGB is twentysixteen_hex2rgb().
func hexRGB(hex string) (r, g, b int, ok bool) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return 0, 0, 0, false
	}
	n, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(n >> 16 & 0xff), int(n >> 8 & 0xff), int(n & 0xff), true
}

func fillCSS(tmpl string, vals map[string]string) string {
	pairs := make([]string, 0, len(vals)*2)
	for k, v := range vals {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

// inlineStyle prints wp_add_inline_style() output for a handle.
func inlineStyle(handle string, parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	if len(keep) == 0 {
		return ""
	}
	return "<style id='" + handle + "-inline-css'>\n" + strings.Join(keep, "\n") + "\n/*# sourceURL=" + handle + "-inline-css */\n</style>\n"
}

// Custom background (core's _custom_background_cb()) --------------------------------

// customBackground returns the background color (no #) when it differs from the theme's default.
func customBackground(v *siteView) string {
	spec := czSpecFor(v.Theme)
	if spec == nil || spec.BackgroundDefault == nil {
		return ""
	}
	c := strings.TrimPrefix(modString(v.ThemeMods, "background_color", ""), "#")
	if c == "" || strings.EqualFold(c, spec.BackgroundDefault(v.ThemeMods)) {
		return ""
	}
	return c
}

// Twenty Sixteen --------------------------------------------------------------------

var tt16Schemes = map[string][5]string{
	"default": {"#1a1a1a", "#ffffff", "#007acc", "#1a1a1a", "#686868"},
	"dark":    {"#262626", "#1a1a1a", "#9adffd", "#e5e5e5", "#c1c1c1"},
	"gray":    {"#616a73", "#4d545c", "#c7c7c7", "#f2f2f2", "#f2f2f2"},
	"red":     {"#ffffff", "#ff675f", "#640c1f", "#402b30", "#402b30"},
	"yellow":  {"#3b3721", "#ffef8e", "#774e24", "#3b3721", "#5b4d3e"},
}

// tt16Scheme is twentysixteen_get_color_scheme().
func tt16Scheme(mods map[string]any) [5]string {
	if c, ok := tt16Schemes[modString(mods, "color_scheme", "default")]; ok {
		return c
	}
	return tt16Schemes["default"]
}

// tt16ColorCSS is everything Twenty Sixteen adds inline to twentysixteen-style:
// twentysixteen_color_scheme_css() and the four per-color overrides.
func tt16ColorCSS(v *siteView) string {
	mods := v.ThemeMods
	scheme := tt16Scheme(mods)
	var parts []string
	if modString(mods, "color_scheme", "default") != "default" {
		if r, g, b, ok := hexRGB(scheme[3]); ok {
			parts = append(parts, fillCSS(tt16SchemeCSS, map[string]string{
				"background_color": scheme[0], "page_background_color": scheme[1], "link_color": scheme[2],
				"main_text_color": scheme[3], "secondary_text_color": scheme[4],
				"border_color": fmt.Sprintf("rgba( %d, %d, %d, 0.2)", r, g, b)}))
		}
	}
	color := func(key string, def string) string { return modString(mods, key, def) }
	if c := color("page_background_color", scheme[1]); c != scheme[1] {
		parts = append(parts, fillCSS(tt16PageBackgroundCSS, map[string]string{"1": c}))
	}
	if c := color("link_color", scheme[2]); c != scheme[2] {
		parts = append(parts, fillCSS(tt16LinkColorCSS, map[string]string{"1": c}))
	}
	if c := color("main_text_color", scheme[3]); c != scheme[3] {
		if r, g, b, ok := hexRGB(c); ok {
			parts = append(parts, fillCSS(tt16MainTextCSS, map[string]string{"1": c, "2": fmt.Sprintf("rgba( %d, %d, %d, 0.2)", r, g, b)}))
		}
	}
	if c := color("secondary_text_color", scheme[4]); c != scheme[4] {
		parts = append(parts, fillCSS(tt16SecondaryTextCSS, map[string]string{"1": c}))
	}
	return inlineStyle("twentysixteen-style", parts...)
}

// tt16ControlsJS is js/color-scheme-control.js: picking a scheme resets the color controls to it.
var tt16ControlsJS = `( function( schemes ) {
	var keys = [ 'background_color', 'page_background_color', 'link_color', 'main_text_color', 'secondary_text_color' ];
	wpgoCustomize.onChange( 'mod:color_scheme', function( value ) {
		var colors = schemes[ value ] || schemes[ 'default' ];
		keys.forEach( function( key, i ) {
			wpgoCustomize.set( 'mod:' + key, colors[ i ], colors[ i ] );
		} );
	} );
} )( ` + tt16SchemesJSON() + ` );`

func tt16SchemesJSON() string {
	var sb strings.Builder
	sb.WriteString("{")
	for i, k := range []string{"default", "dark", "gray", "red", "yellow"} {
		if i > 0 {
			sb.WriteString(",")
		}
		c := tt16Schemes[k]
		fmt.Fprintf(&sb, "%q:[%q,%q,%q,%q,%q]", k, c[0], c[1], c[2], c[3], c[4])
	}
	sb.WriteString("}")
	return sb.String()
}

// Twenty Seventeen ------------------------------------------------------------------

// tt17Scheme is the sanitized colorscheme theme mod.
func tt17Scheme(v *siteView) string {
	switch c := modString(v.ThemeMods, "colorscheme", "light"); c {
	case "light", "dark", "custom":
		return c
	}
	return "light"
}

// tt17ColorHead is the dark scheme stylesheet (enqueued after blocks.css) and, for the custom scheme,
// twentyseventeen_colors_css_wrap().
func tt17ColorStyles(v *siteView) string {
	if tt17Scheme(v) == "dark" {
		return "<link rel='stylesheet' id='twentyseventeen-colors-dark-css' href='" + v.ThemeURI + "/assets/css/colors-dark.css?ver=20240412' media='all' />\n"
	}
	return ""
}

func tt17CustomColors(v *siteView) string {
	if tt17Scheme(v) != "custom" {
		return ""
	}
	hue := modInt(v.ThemeMods, "colorscheme_hue", 250)
	if hue < 0 {
		hue = -hue
	}
	css := fillCSS(tt17CustomColorsCSS, map[string]string{"hue": strconv.Itoa(hue), "sat": "50%", "rsat": "40%"})
	return "<style id=\"custom-theme-colors\" >\n\t\t" + css + "\n\t</style>\n"
}

// Twenty Twenty ---------------------------------------------------------------------

type tt20Element struct{ Context, Key, Property, Selectors string }

var tt20DefaultColors = map[string]map[string]string{
	"content":       {"text": "#000000", "accent": "#cd2653", "secondary": "#6d6d6d", "borders": "#dcd7ca"},
	"header-footer": {"text": "#000000", "accent": "#cd2653", "secondary": "#6d6d6d", "borders": "#dcd7ca"},
}

// tt20Colors is the accent_accessible_colors theme mod (twentytwenty_get_color_for_area()).
func tt20Colors(mods map[string]any) map[string]map[string]string {
	raw, ok := mods["accent_accessible_colors"].(map[string]any)
	if !ok {
		return tt20DefaultColors
	}
	out := map[string]map[string]string{}
	for ctx, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		out[ctx] = map[string]string{}
		for k, c := range m {
			if s, ok := c.(string); ok {
				out[ctx][k] = s
			}
		}
	}
	return out
}

// tt20ColorCSS is twentytwenty_get_customizer_css( 'front-end' ).
func tt20ColorCSS(v *siteView) string {
	colors := tt20Colors(v.ThemeMods)
	var sb strings.Builder
	for _, e := range tt20Elements {
		if c := sanitizeHex(colors[e.Context][e.Key]); c != "" {
			fmt.Fprintf(&sb, "%s { %s: %s; }", e.Selectors, e.Property, c)
		}
	}
	if cover := sanitizeHex(modString(v.ThemeMods, "cover_template_overlay_text_color", "")); cover != "" && cover != "#ffffff" {
		fmt.Fprintf(&sb, "%s { %s: %s; }", ".overlay-header .header-inner", "color", cover)
		fmt.Fprintf(&sb, "%s { %s: %s; }", ".cover-header .entry-header *", "color", cover)
	}
	return sb.String()
}

// sanitizeTT20Colors is TwentyTwenty_Customize::sanitize_accent_accessible_colors().
func sanitizeTT20Colors(raw string) map[string]any {
	var in map[string]map[string]string
	if err := jsonUnmarshalString(raw, &in); err != nil {
		return nil
	}
	out := map[string]any{}
	for _, ctx := range []string{"content", "header-footer"} {
		m := map[string]any{}
		for _, k := range []string{"text", "accent", "secondary", "borders", "background"} {
			if c := sanitizeHex(in[ctx][k]); c != "" {
				m[k] = c
			}
		}
		if len(m) > 0 {
			out[ctx] = m
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// tt20ControlsJS is assets/js/customize-controls.js: the accent hue and the two background colors
// recalculate the accessible color set with the theme's own color-calculations.js.
var tt20ControlsJS = `( function() {
	var bg = { 'content': 'mod:background_color', 'header-footer': 'mod:header_footer_background_color' };
	function recalc() {
		if ( 'function' !== typeof twentyTwentyColor ) {
			return;
		}
		var value = {}, hue = parseInt( wpgoCustomize.get( 'mod:accent_hue' ), 10 ) || 344;
		Object.keys( bg ).forEach( function( context ) {
			var background = wpgoCustomize.get( bg[ context ] ) || ( 'content' === context ? '#f5efe0' : '#ffffff' ),
				colors = twentyTwentyColor( background, hue );
			if ( colors.getAccentColor() && 'function' === typeof colors.getAccentColor().toCSS ) {
				value[ context ] = { text: colors.getTextColor(), accent: colors.getAccentColor().toCSS(), background: background };
				value[ context ].borders = colors.bgColorObj.clone().getReadableContrastingColor( colors.bgColorObj, 1.36 ).toCSS();
				value[ context ].secondary = colors.bgColorObj.clone().getReadableContrastingColor( colors.bgColorObj ).s( colors.bgColorObj.s() / 2 ).toCSS();
			}
		} );
		wpgoCustomize.set( 'mod:accent_accessible_colors', JSON.stringify( value ) );
	}
	wpgoCustomize.toggleWith( 'mod:accent_hue_active', 'mod:accent_hue', function( v ) { return 'custom' === v; } );
	wpgoCustomize.onChange( 'mod:accent_hue_active', function( v ) {
		if ( 'custom' !== v ) {
			wpgoCustomize.set( 'mod:accent_hue', '344', '344' );
		}
	} );
	[ 'mod:accent_hue', 'mod:background_color', 'mod:header_footer_background_color' ].forEach( function( id ) {
		wpgoCustomize.onChange( id, recalc );
	} );
} )();`

// Twenty Twenty-One ----------------------------------------------------------------

// tt1Luminance is Twenty_Twenty_One_Custom_Colors::get_relative_luminance_from_hex().
func tt1Luminance(hex string) int {
	r, g, b, ok := hexRGB(hex)
	if !ok {
		return 0
	}
	l := 0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)
	return int(l + 0.5)
}

func tt1Background(v *siteView) string {
	return strings.TrimPrefix(modString(v.ThemeMods, "background_color", "D1E4DD"), "#")
}

// tt1ColorCSS is Twenty_Twenty_One_Custom_Colors::custom_color_variables().
func tt1ColorCSS(v *siteView) string {
	bg := tt1Background(v)
	if strings.EqualFold(bg, "d1e4dd") {
		return ""
	}
	readable := "#fff"
	if tt1Luminance(bg) > 127 {
		readable = "#000"
	}
	css := ":root{--global--color-background: #" + bg + ";--global--color-primary: " + readable + ";--global--color-secondary: " + readable +
		";--button--color-background: " + readable + ";--button--color-text-hover: " + readable + ";"
	if readable == "#fff" {
		css += "--table--stripes-border-color: rgba(240, 240, 240, 0.15);--table--stripes-background-color: rgba(240, 240, 240, 0.15);"
	}
	return inlineStyle("twenty-twenty-one-style", css+"}")
}

// tt1ColorClasses is Twenty_Twenty_One_Custom_Colors::body_class().
func tt1ColorClasses(v *siteView) []string {
	l := tt1Luminance(tt1Background(v))
	out := []string{"is-light-theme"}
	if l < 127 {
		out[0] = "is-dark-theme"
	}
	if l >= 225 {
		out = append(out, "has-background-white")
	}
	return out
}
