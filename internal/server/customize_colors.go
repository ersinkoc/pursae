package server

import (
	"regexp"
	"strconv"
	"strings"
)

// Customizer colors of the themes: the controls a theme registers in the Colors section and the
// helpers the themes use to print CSS for the saved theme mods.

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

// czSpecs are the Customizer specs by theme slug: the bundled themes register theirs in init()
// (theme_twentytwentyfive.go), theme packages when they are loaded (registerThemePackage).
var czSpecs = map[string]*czThemeSpec{}

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

// hexRGB parses a hex color.
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

// sanitizeJSONMod keeps a json control's value (a JSON object filled by the theme's control script).
func sanitizeJSONMod(raw string) map[string]any {
	var out map[string]any
	if err := jsonUnmarshalString(raw, &out); err != nil || len(out) == 0 {
		return nil
	}
	return out
}
