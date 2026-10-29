package server

import (
	"fmt"
	"regexp"
	"strings"
)

// Gooike's banner themes. A theme is a layout (where and how the first layer sits) with a palette
// and shape; applying one copies its values into the design, which the admin can then customize
// (colors, font, radius, position, custom CSS). The layouts live in gooike.css; the palette is
// printed as CSS custom properties.

type gooikeTheme struct {
	Key, Name, Description string
	Layout                 string // bar, box, modal, pill, toast, wall, split, sidebar, card
	Position               string
	Background, Text       string
	Primary, PrimaryText   string
	Font                   string
	Radius                 int
}

func (t *gooikeTheme) apply(d *gooikeDesign) {
	d.Theme, d.Position = t.Key, t.Position
	d.Background, d.Text, d.Primary, d.PrimaryText = t.Background, t.Text, t.Primary, t.PrimaryText
	d.Font, d.Radius = t.Font, t.Radius
	if d.FontSize == 0 {
		d.FontSize = 15
	}
	if d.FloatPosition == "" {
		d.FloatPosition = "left"
		d.Floating = true
	}
}

var gooikeThemes = []*gooikeTheme{
	{Key: "classic", Name: "Classic Bar", Description: "A full-width bar along the bottom of the screen. Clear, familiar and out of the way.",
		Layout: "bar", Position: "bottom", Background: "#ffffff", Text: "#1d2327", Primary: "#2563eb", PrimaryText: "#ffffff", Font: "inherit", Radius: 6},
	{Key: "topbar", Name: "Top Bar", Description: "A slim notice across the top of the page that pushes nothing around.",
		Layout: "bar", Position: "top", Background: "#f6f7f7", Text: "#1d2327", Primary: "#0f766e", PrimaryText: "#ffffff", Font: "system", Radius: 4},
	{Key: "box-left", Name: "Floating Box Left", Description: "A compact card floating in the bottom-left corner.",
		Layout: "box", Position: "bottom-left", Background: "#ffffff", Text: "#1f2937", Primary: "#7c3aed", PrimaryText: "#ffffff", Font: "inherit", Radius: 12},
	{Key: "box-right", Name: "Floating Box Right", Description: "The same compact card, in the bottom-right corner.",
		Layout: "box", Position: "bottom-right", Background: "#ffffff", Text: "#1f2937", Primary: "#0369a1", PrimaryText: "#ffffff", Font: "inherit", Radius: 12},
	{Key: "modal", Name: "Centered Modal", Description: "A dialog in the middle of the screen over a soft backdrop. Hard to miss, easy to answer.",
		Layout: "modal", Position: "center", Background: "#ffffff", Text: "#111827", Primary: "#1d4ed8", PrimaryText: "#ffffff", Font: "system", Radius: 16},
	{Key: "pill", Name: "Minimal Pill", Description: "One line, rounded, centered at the bottom. The quietest banner that still asks properly.",
		Layout: "pill", Position: "bottom", Background: "#111827", Text: "#f9fafb", Primary: "#f9fafb", PrimaryText: "#111827", Font: "system", Radius: 999},
	{Key: "glass", Name: "Glass", Description: "A frosted, translucent card with a backdrop blur that lets the site shine through.",
		Layout: "box", Position: "bottom-right", Background: "#ffffff", Text: "#0f172a", Primary: "#0f172a", PrimaryText: "#ffffff", Font: "rounded", Radius: 20},
	{Key: "midnight", Name: "Midnight Dark", Description: "A dark bar for dark sites, with bright buttons.",
		Layout: "bar", Position: "bottom", Background: "#0b1220", Text: "#e5e7eb", Primary: "#38bdf8", PrimaryText: "#0b1220", Font: "inherit", Radius: 8},
	{Key: "brand", Name: "Brand Color", Description: "A bold bar in your brand color with white buttons. Change the background to match your logo.",
		Layout: "bar", Position: "bottom", Background: "#4f46e5", Text: "#ffffff", Primary: "#ffffff", PrimaryText: "#3730a3", Font: "inherit", Radius: 999},
	{Key: "illustrated", Name: "Cookie Card", Description: "A friendly card with a cookie illustration, for sites with a playful voice.",
		Layout: "card", Position: "bottom-left", Background: "#fffbeb", Text: "#451a03", Primary: "#b45309", PrimaryText: "#ffffff", Font: "rounded", Radius: 18},
	{Key: "toast", Name: "Corner Toast", Description: "A small notification-style toast in the top-right corner.",
		Layout: "toast", Position: "top-right", Background: "#ffffff", Text: "#1f2937", Primary: "#16a34a", PrimaryText: "#ffffff", Font: "system", Radius: 10},
	{Key: "wall", Name: "Consent Wall", Description: "Strict mode: a full-screen wall that keeps the page covered until the visitor answers.",
		Layout: "wall", Position: "center", Background: "#ffffff", Text: "#111827", Primary: "#111827", PrimaryText: "#ffffff", Font: "serif", Radius: 4},
	{Key: "split", Name: "Split Panel", Description: "A wide two-column panel: the explanation on the left, the choices on the right.",
		Layout: "split", Position: "bottom", Background: "#f8fafc", Text: "#0f172a", Primary: "#be123c", PrimaryText: "#ffffff", Font: "humanist", Radius: 10},
	{Key: "sidebar", Name: "Side Panel", Description: "A tall panel docked to the side of the screen, with room for a longer message.",
		Layout: "sidebar", Position: "right", Background: "#ffffff", Text: "#1e293b", Primary: "#0e7490", PrimaryText: "#ffffff", Font: "inherit", Radius: 0},
}

func gooikeThemeByKey(k string) *gooikeTheme {
	for _, t := range gooikeThemes {
		if t.Key == k {
			return t
		}
	}
	return nil
}

// gooikePositions are the positions each layout offers.
var gooikePositions = map[string][]string{
	"bar":     {"bottom", "top"},
	"pill":    {"bottom", "top"},
	"split":   {"bottom", "top"},
	"box":     {"bottom-left", "bottom-right", "top-left", "top-right"},
	"card":    {"bottom-left", "bottom-right", "top-left", "top-right"},
	"toast":   {"top-right", "top-left", "bottom-right", "bottom-left"},
	"sidebar": {"right", "left"},
	"modal":   {"center"},
	"wall":    {"center"},
}

// gooikeFonts are the font choices; "inherit" keeps the theme's font.
var gooikeFonts = []struct{ Key, Label, Stack string }{
	{"inherit", "Site font (inherit)", "inherit"},
	{"system", "System UI", `system-ui,-apple-system,"Segoe UI",Roboto,"Helvetica Neue",Arial,sans-serif`},
	{"humanist", "Humanist sans", `"Seravek","Gill Sans Nova",Ubuntu,Calibri,"DejaVu Sans",source-sans-pro,sans-serif`},
	{"rounded", "Rounded", `ui-rounded,"Hiragino Maru Gothic ProN",Quicksand,Comfortaa,Manjari,"Arial Rounded MT","Arial Rounded MT Bold",Calibri,source-sans-pro,sans-serif`},
	{"serif", "Serif", `Charter,"Bitstream Charter","Sitka Text",Cambria,Georgia,serif`},
	{"mono", "Monospace", `ui-monospace,"Cascadia Code","Source Code Pro",Menlo,Consolas,"DejaVu Sans Mono",monospace`},
}

func gooikeFontStack(k string) string {
	for _, f := range gooikeFonts {
		if f.Key == k {
			return f.Stack
		}
	}
	return "inherit"
}

var gooikeColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

func gooikeColor(v, def string) string {
	v = strings.TrimSpace(v)
	if gooikeColorRe.MatchString(v) {
		return v
	}
	return def
}

// gooikeSanitizeDesign keeps colors, sizes and positions valid for the theme's layout.
func gooikeSanitizeDesign(d gooikeDesign) gooikeDesign {
	t := gooikeThemeByKey(d.Theme)
	if t == nil {
		t = gooikeThemes[0]
		d.Theme = t.Key
	}
	d.Background = gooikeColor(d.Background, t.Background)
	d.Text = gooikeColor(d.Text, t.Text)
	d.Primary = gooikeColor(d.Primary, t.Primary)
	d.PrimaryText = gooikeColor(d.PrimaryText, t.PrimaryText)
	ok := false
	for _, p := range gooikePositions[t.Layout] {
		ok = ok || p == d.Position
	}
	if !ok {
		d.Position = t.Position
	}
	if gooikeFontStack(d.Font) == "inherit" && d.Font != "inherit" {
		d.Font = "inherit"
	}
	d.Radius = min(max(d.Radius, 0), 999)
	if d.FontSize < 11 || d.FontSize > 22 {
		d.FontSize = 15
	}
	if d.FloatPosition != "right" {
		d.FloatPosition = "left"
	}
	d.CustomCSS = strings.ReplaceAll(d.CustomCSS, "</", "<\\/") // never closes the <style> element
	return d
}

// gooikeCSSVars is the palette and shape of a design as custom properties on the plugin's roots.
func gooikeCSSVars(d gooikeDesign) string {
	r := d.Radius
	btn := min(r, 999)
	if r > 24 && r < 999 {
		btn = 24
	}
	return fmt.Sprintf(".gooike{--gk-bg:%s;--gk-fg:%s;--gk-primary:%s;--gk-primary-fg:%s;--gk-radius:%dpx;--gk-btn-radius:%dpx;--gk-font:%s;--gk-size:%dpx}",
		d.Background, d.Text, d.Primary, d.PrimaryText, r, btn, gooikeFontStack(d.Font), d.FontSize)
}
