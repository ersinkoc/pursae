package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// adminTheme is one look of the whole admin. WordPress Classic is the stock WordPress admin and stays
// the default; Pursae Modern restyles the same markup (dark top bar and menu, panels on a soft canvas,
// Lexend type) with one extra stylesheet, so every core and plugin screen follows it without changes.
type adminTheme struct {
	Slug, Name, Description string
	// Mode is Pursae Modern's colour mode for the content: "light" (default), "dark", or "auto" (follows
	// the operating system). The top bar and menu are dark in every mode.
	Mode string
}

// adminThemeModes are the colour modes of Pursae Modern, in the order the profile shows them.
var adminThemeModes = []struct{ Slug, Name string }{{"light", "Light"}, {"dark", "Dark"}, {"auto", "Match the system"}}

func validThemeMode(m string) bool { return m == "light" || m == "dark" || m == "auto" }

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
	if t.Slug != "modern" {
		return ""
	}
	switch t.Mode {
	case "dark":
		return "pursae-modern pm-dark"
	case "auto":
		return "pursae-modern pm-auto"
	}
	return "pursae-modern"
}

// adminThemeFor returns the user's admin_theme (WordPress Classic when unset or unknown).
func adminThemeFor(u *store.User) adminTheme {
	if u != nil {
		if slug, ok := u.Meta["admin_theme"].(string); ok {
			for _, t := range adminThemes {
				if t.Slug == slug {
					t.Mode = "light"
					if m, _ := u.Meta["admin_theme_mode"].(string); validThemeMode(m) {
						t.Mode = m
					}
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

// themeModeAjax switches the current user's Pursae Modern colour mode (the account menu's
// "Dark mode" item, pursae-modern.js). Responds "1" or an error.
func (s *Server) themeModeAjax(c *gin.Context) {
	if !s.checkNonceValue(c, c.PostForm("_wpnonce"), "pursae-theme-mode") {
		c.String(http.StatusForbidden, "-1")
		return
	}
	m := c.PostForm("mode")
	if !validThemeMode(m) {
		c.String(http.StatusBadRequest, "Unknown mode.")
		return
	}
	u := currentUser(c)
	if u == nil || u.ID == 0 {
		c.String(http.StatusForbidden, "-1")
		return
	}
	if err := s.setUserMeta(c, u, "admin_theme_mode", m); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	rememberAdminLook(c, adminThemeFor(u))
	c.String(http.StatusOK, "1")
}

// adminLookCookie remembers, in this browser, that its last admin user chose Pursae Modern and which
// colour mode, so the log-in, lost-password and register screens (which have no user yet) can wear
// the same look. It holds only "modern-light", "modern-dark" or "modern-auto"; it is read by an inline
// script on those screens, so it is not HttpOnly. WordPress Classic users get no cookie.
const adminLookCookie = "wpgo_admin_look"

func rememberAdminLook(c *gin.Context, t adminTheme) {
	want := ""
	if t.Modern() {
		want = "modern-" + t.Mode
	}
	have, _ := c.Cookie(adminLookCookie)
	if have == want {
		return
	}
	secure := c.Request.TLS != nil
	if want == "" {
		c.SetCookie(adminLookCookie, "", -1, "/", "", secure, false)
		return
	}
	c.SetCookie(adminLookCookie, want, 365*24*3600, "/", "", secure, false)
}
