package server

import "wpgo/internal/store"

// adminTheme is one look of the whole admin. WordPress Classic is the stock WordPress admin and stays
// the default; Pursae Modern restyles the same markup (dark top bar and menu, panels on a soft canvas,
// Lexend type) with one extra stylesheet, so every core and plugin screen follows it without changes.
type adminTheme struct {
	Slug, Name, Description string
}

// adminThemes lists the themes in the order the profile screen shows them; the first is the default.
var adminThemes = []adminTheme{
	{Slug: "classic", Name: "WordPress Classic", Description: "The familiar WordPress admin."},
	{Slug: "modern", Name: "Pursae Modern", Description: "Dark top bar and menu, calm panels, softer type."},
}

// Modern reports whether this is the Pursae Modern theme.
func (t adminTheme) Modern() bool { return t.Slug == "modern" }

// CSS is the theme's stylesheet, loaded after every other admin stylesheet; Classic has none.
func (t adminTheme) CSS() string {
	if t.Slug == "modern" {
		return "/wp-admin/assets/css/pursae-modern.css"
	}
	return ""
}

// BodyClass is added to <body> while the theme is on.
func (t adminTheme) BodyClass() string {
	if t.Slug == "modern" {
		return "pursae-modern"
	}
	return ""
}

// adminThemeFor returns the user's admin_theme (WordPress Classic when unset or unknown).
func adminThemeFor(u *store.User) adminTheme {
	if u != nil {
		if slug, ok := u.Meta["admin_theme"].(string); ok {
			for _, t := range adminThemes {
				if t.Slug == slug {
					return t
				}
			}
		}
	}
	return adminThemes[0]
}

// pluginIcons are the dashicons of the built-in plugins on the Pursae Modern plugin cards (the same as their menus).
var pluginIcons = map[string]string{
	"goocommerce": "dashicons-cart", "golementor": "dashicons-layout", "seo-go": "dashicons-chart-line",
	"wpgo-cache": "dashicons-performance", "adminsecgo": "dashicons-shield", "gocdn": "dashicons-cloud-upload",
	"headlessgo": "dashicons-rest-api", "gooike": "dashicons-privacy", "pixelgo": "dashicons-chart-area",
	"pursae-analytics": "dashicons-chart-bar", "pursae-forum": "dashicons-format-chat", "wp-mail-smtp": "dashicons-email",
	"acf-go": "dashicons-welcome-widgets-menus", "golylang": "dashicons-translation", "gontact-form": "dashicons-email-alt",
	"maintenance": "dashicons-hammer", "backup-manager": "dashicons-database-export", "classic-editor": "dashicons-edit",
}

// Icon is the plugin card's dashicon (a puzzle piece for plugins without one).
func (r pluginRow) Icon() string {
	if i, ok := pluginIcons[r.Slug]; ok {
		return i
	}
	return "dashicons-admin-plugins"
}

// Tint is one of five soft backgrounds for the card icon, stable per plugin.
func (r pluginRow) Tint() int {
	h := 0
	for _, c := range r.Slug {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	return h % 5
}
