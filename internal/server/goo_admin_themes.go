package server

import (
	"html/template"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
)

// Goocommerce › Store Themes (/wp-admin/goocommerce?page=themes): the store themes of the theme
// library (packages that declare supports.woocommerce) with their screenshots, a live preview with
// the demo store and one action that does the whole job: install the package, activate it and
// import its demo store. The steps run one request each
// so the screen can show progress; without JavaScript one POST runs them all. Nothing here installs
// or imports by itself: the steps call the same code as Appearance › Add Themes (installLibraryTheme,
// switchTheme, importThemeDemo), so an installed store theme is an ordinary theme of Appearance › Themes.

const gooThemesNonce = "goo-store-themes"

// gooStoreTheme is one card of the screen.
type gooStoreTheme struct {
	Slug, Name, Version, Description string
	Style, CategoryLabel, Screenshot string
	PreviewURL                       string // Live Preview with the demo store (theme_preview.go)
	Installed, Active, Imported      bool
	Update                           bool // an older version is installed
	HasDemo                          bool
	CustomizeURL                     string
}

// gooIsStoreManifest reports whether a theme package is a store theme.
func gooIsStoreManifest(m *themeManifest) bool {
	if m == nil {
		return false
	}
	v, _ := m.Supports["woocommerce"].(bool)
	return v
}

// gooStoreThemes lists the store themes of the library, the active one first, then by name.
func (s *Server) gooStoreThemes(c *gin.Context) []gooStoreTheme {
	active := s.activeTheme()
	var out []gooStoreTheme
	for _, lt := range s.libraryThemes(c) {
		m := lt.Manifest
		if !gooIsStoreManifest(m) {
			continue
		}
		st := gooStoreTheme{Slug: m.Slug, Name: m.Name, Version: m.Version, Description: m.Description, Style: m.Style,
			CategoryLabel: themeCategoryLabel(m.Category), HasDemo: m.Demo != "",
			Screenshot: libraryScreenshotURL(m)}
		if canPreview(currentUser(c), true) {
			st.PreviewURL = previewURL(m.Slug, false)
		}
		if t := themeBySlug(m.Slug); t != nil && t.Package != nil {
			st.Installed, st.Update = true, semverLess(t.Version, m.Version)
			st.Active = active != nil && active.Slug == m.Slug
			st.Imported = s.demoImported(m.Slug)
			if st.Active {
				st.CustomizeURL = "/wp-admin/customize"
			}
		}
		out = append(out, st)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Active && !out[j].Active
	})
	return out
}

// gooAdminThemes is the Store Themes screen and its step requests.
func (s *Server) gooAdminThemes(c *gin.Context) {
	if c.Request.Method == http.MethodPost {
		s.gooThemesPost(c)
		return
	}
	ctx := c.Request.Context()
	u := currentUser(c)
	themes := s.gooStoreThemes(c)
	data := map[string]any{"Themes": themes, "Nonce": s.nonce(c, gooThemesNonce), "ShopURL": s.gooShopURL(ctx),
		"HomeURL": s.links(ctx).HomeURL("/"), "CanInstall": u.Can("install_themes", nil) && u.Can("switch_themes", nil) && u.Can("import", nil),
		"Golylang": s.golylangOn()}
	var notices []notice
	switch c.Query("result") {
	case "done":
		notices = append(notices, notice{Type: "success", Dismissible: true, Message: template.HTML(template.HTMLEscapeString(c.Query("msg")) +
			` <a href="` + template.HTMLEscapeString(s.gooShopURL(ctx)) + `" target="_blank">Visit your store</a>`)})
	case "error":
		notices = append(notices, notice{Type: "error", Dismissible: true, Message: template.HTML(template.HTMLEscapeString(c.Query("msg")))})
	}
	s.gooStoreRender(c, "goocommerce?page=themes", "wc-store-themes", "Store Themes", "admin/goo-themes", data, notices...)
}

// gooThemeStep is the answer of one step request.
type gooThemeStep struct {
	OK      bool   `json:"ok"`
	Step    string `json:"step"`
	Next    string `json:"next,omitempty"`
	Message string `json:"message"`
	Done    bool   `json:"done,omitempty"`
	ShopURL string `json:"shop_url,omitempty"`
	HomeURL string `json:"home_url,omitempty"`
}

// gooThemeSteps are the steps of a setup, in order.
var gooThemeSteps = []string{"install", "activate", "demo"}

func gooNextStep(step string) string {
	for i, st := range gooThemeSteps {
		if st == step && i+1 < len(gooThemeSteps) {
			return gooThemeSteps[i+1]
		}
	}
	return ""
}

// gooThemesPost runs one step (action=step, answered with JSON) or every step (action=setup, a
// plain form post answered with a redirect).
func (s *Server) gooThemesPost(c *gin.Context) {
	if !s.checkNonce(c, gooThemesNonce) {
		s.gooDeny(c)
		return
	}
	u := currentUser(c)
	if !u.Can("install_themes", nil) || !u.Can("switch_themes", nil) || !u.Can("import", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to install themes on this site.")
		return
	}
	slug := c.PostForm("theme")
	lt := s.libraryTheme(c, slug)
	if lt == nil || !gooIsStoreManifest(lt.Manifest) {
		if c.PostForm("action") == "step" {
			c.JSON(http.StatusBadRequest, gooThemeStep{Message: "The requested theme does not exist."})
		} else {
			c.Redirect(http.StatusFound, gooAdminURL("themes", "result", "error", "msg", "The requested theme does not exist."))
		}
		return
	}
	if c.PostForm("action") == "step" {
		step := c.PostForm("step")
		res := s.gooRunThemeStep(c, lt, step)
		code := http.StatusOK
		if !res.OK {
			code = http.StatusInternalServerError
		}
		c.JSON(code, res)
		return
	}
	var last gooThemeStep
	for _, step := range gooThemeSteps {
		last = s.gooRunThemeStep(c, lt, step)
		if !last.OK {
			c.Redirect(http.StatusFound, gooAdminURL("themes", "result", "error", "msg", last.Message))
			return
		}
	}
	c.Redirect(http.StatusFound, gooAdminURL("themes", "result", "done", "msg", last.Message))
}

// gooRunThemeStep installs (or updates) the package, activates it, or imports its demo store.
func (s *Server) gooRunThemeStep(c *gin.Context, lt *libraryTheme, step string) gooThemeStep {
	ctx := c.Request.Context()
	m := lt.Manifest
	res := gooThemeStep{Step: step, Next: gooNextStep(step)}
	fail := func(msg string) gooThemeStep {
		res.OK, res.Next, res.Message = false, "", msg
		return res
	}
	switch step {
	case "install":
		t := themeBySlug(m.Slug)
		if t != nil && t.Package == nil {
			return fail("A bundled theme already uses this name.")
		}
		if t == nil || semverLess(t.Version, m.Version) {
			if _, err := s.installLibraryTheme(c, lt); err != nil {
				return fail("Installation failed: " + err.Error())
			}
			res.Message = m.Name + " is installed."
		} else {
			res.Message = m.Name + " is already installed."
		}
	case "activate":
		if themeBySlug(m.Slug) == nil {
			return fail("Install the theme first.")
		}
		if a := s.activeTheme(); a == nil || a.Slug != m.Slug {
			if err := s.switchTheme(c, m.Slug); err != nil {
				return fail(err.Error())
			}
		}
		res.Message = m.Name + " is the active theme."
	case "demo":
		t := themeBySlug(m.Slug)
		if t == nil || t.Package == nil {
			return fail("Install the theme first.")
		}
		res.Done, res.ShopURL, res.HomeURL = true, "", s.links(ctx).HomeURL("/")
		if m.Demo == "" {
			res.OK, res.Message, res.ShopURL = true, m.Name+" is ready.", s.gooShopURL(ctx)
			return res
		}
		rep, err := s.importThemeDemo(c, currentUser(c).ID, t)
		if err != nil {
			return fail("The demo store could not be imported: " + err.Error())
		}
		res.ShopURL = s.gooShopURL(ctx)
		res.Message = "Your store is ready with " + m.Name + ". " + rep.summary()
	default:
		return fail("Unknown step.")
	}
	res.OK = true
	return res
}

// gooThemeTask is the Home task of the store's look: done when the active theme is a store theme.
func (s *Server) gooThemeTask() gooHomeTask {
	done := false
	if t := s.activeTheme(); t != nil && t.Package != nil {
		done = gooIsStoreManifest(t.Package.Manifest)
	}
	return gooHomeTask{"Choose a store theme", "Install a store theme with its demo shop in one click, then make it yours.", gooAdminURL("themes"), done}
}

// gooThemeHint is the Home notice shown while the active theme is not a store theme.
func (s *Server) gooThemeHint() string {
	if s.gooThemeTask().Done {
		return ""
	}
	return `Your theme is not a store theme. <a href="` + gooAdminURL("themes") + `">Pick a store theme</a> and get a ready-made shop (products, cart, checkout, menus) in one click.`
}
