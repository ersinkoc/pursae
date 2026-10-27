package server

import (
	"html/template"

	"wpgo/internal/store"
)

// adminColor is one registered admin color scheme (wp_admin_css_color()).
type adminColor struct {
	Slug, Name string
	Colors     []string
	Icons      template.JS // _wpColorScheme for svg-painter.js
}

// CSS is the scheme's stylesheet; the Fresh scheme is WordPress's base CSS and has none.
func (a adminColor) CSS() string {
	if a.Slug == "fresh" {
		return ""
	}
	return "/wp-admin/css/colors/" + a.Slug + "/colors.min.css"
}

// adminColors lists WordPress 7.1's schemes in the order the profile screen shows them.
var adminColors = []adminColor{
	{Slug: "modern", Name: "Default", Colors: []string{"#1e1e1e", "#3858e9", "#7b90ff"}, Icons: `{"icons":{"base":"#f3f1f1","focus":"#fff","current":"#fff"}}`},
	{Slug: "fresh", Name: "Fresh", Colors: []string{"#1d2327", "#2c3338", "#2271b1", "#72aee6"}, Icons: `{"icons":{"base":"#a7aaad","focus":"#72aee6","current":"#fff"}}`},
	{Slug: "light", Name: "Light", Colors: []string{"#e5e5e5", "#6a6a6a", "#c64606", "#007cba"}, Icons: `{"icons":{"base":"#999","focus":"#ccc","current":"#ccc"}}`},
	{Slug: "blue", Name: "Blue", Colors: []string{"#183751", "#245278", "#437aa8", "#e1a948"}, Icons: `{"icons":{"base":"#e5f8ff","focus":"#fff","current":"#fff"}}`},
	{Slug: "coffee", Name: "Coffee", Colors: []string{"#382e27", "#5c4c40", "#916745", "#9ea476"}, Icons: `{"icons":{"base":"#f3f2f1","focus":"#fff","current":"#fff"}}`},
	{Slug: "ectoplasm", Name: "Ectoplasm", Colors: []string{"#392751", "#4a3369", "#646c3e", "#d46f15"}, Icons: `{"icons":{"base":"#ece6f6","focus":"#fff","current":"#fff"}}`},
	{Slug: "midnight", Name: "Midnight", Colors: []string{"#232a2e", "#333c42", "#69a8bb", "#cf4339"}, Icons: `{"icons":{"base":"#f1f2f3","focus":"#fff","current":"#fff"}}`},
	{Slug: "ocean", Name: "Ocean", Colors: []string{"#2b3f44", "#39535a", "#567958", "#aa9d88"}, Icons: `{"icons":{"base":"#f2fcff","focus":"#fff","current":"#fff"}}`},
	{Slug: "sunrise", Name: "Sunrise", Colors: []string{"#6f2724", "#8a312d", "#ad631e", "#ccaf0b"}, Icons: `{"icons":{"base":"#f3f1f1","focus":"#fff","current":"#fff"}}`},
}

// adminColorFor returns the user's admin_color scheme (Default/modern when unset or unknown).
func adminColorFor(u *store.User) adminColor {
	if u != nil {
		if slug, ok := u.Meta["admin_color"].(string); ok {
			for _, c := range adminColors {
				if c.Slug == slug {
					return c
				}
			}
		}
	}
	return adminColors[0]
}

// syntaxHighlighting reports the user's "Syntax Highlighting" personal option (on unless disabled).
func syntaxHighlighting(u *store.User) bool {
	v, _ := u.Meta["syntax_highlighting"].(string)
	return v != "false"
}
