package server

import (
	"html/template"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// Hello Pursae: the blank, builder-first theme that comes with Pursae Web Builder. Templates live in web/themes/hello-pursae. While the
// plugin is on, the Theme Builder's header, main and footer parts of the request arrive in
// v.Data["wb"] as a *wbThemePartsView and replace the theme's minimal markup; without a part (or with
// the plugin off) the theme prints its own minimal header, content and footer, so it is a usable
// theme on its own.
//
// Activating the plugin switches the site to this theme and deactivating it switches back
// (wbSetActive, webbuilder_settings.go).

const (
	// wbHelloSlug is Hello Pursae's slug (theme directory, stylesheet option).
	wbHelloSlug = "hello-pursae"
	// wbHelloVersion is the theme's version (asset cache busting).
	wbHelloVersion = "1.0.0"
	// wbThemePartsKey is the v.Data key of the Theme Builder parts (*wbThemePartsView).
	wbThemePartsKey = "wb"
)

// wbThemePartsView is the contract between the Theme Builder's front end (wbThemeParts,
// webbuilder_theme_front.go) and Hello Pursae's templates: v.Data["wb"] holds a *wbThemePartsView
// when the plugin is on and Hello Pursae renders the request. Each field is the finished markup of
// one location, empty when no template applies (the theme then prints its own default for it).
type wbThemePartsView struct {
	// Header and Footer replace the theme's site header and footer; they are printed inside
	// div.wb-part.wb-part-header and div.wb-part.wb-part-footer.
	Header, Footer template.HTML
	// Main replaces the content of the view (the page's own builder document, or the Single,
	// Archive, Search Results, 404 … template that matched).
	Main template.HTML
	// HasMain reports that Main carries its own <main> landmark: the theme then wraps it in
	// div#content instead of main#content, so the page has exactly one main element.
	HasMain bool
	// Canvas is a builder page with layout canvas: the theme prints neither a header nor a footer
	// (no part, no wrapper, no default markup), only Main between its head and footer hooks.
	Canvas bool
}

// wbHelloMenuLocations are Hello Pursae's menu locations.
var wbHelloMenuLocations = []MenuLocation{{"primary", "Primary Menu"}, {"footer", "Footer Menu"}}

func init() {
	registerTheme(&Theme{
		Slug: wbHelloSlug, Name: "Hello Pursae", Version: wbHelloVersion,
		Description: "A plain, lightweight theme for Pursae Web Builder: a blank canvas whose header, footer, single, archive, search and 404 templates you design with the Theme Builder. Without a template it shows a minimal header, the content and a footer.",
		Author:      "Pursae", AuthorURI: "https://github.com/pursae", ThemeURI: "https://github.com/pursae",
		Tags: []string{"Blog", "One Column", "Custom Logo", "Custom Menu", "Featured Images", "Full Width Template",
			"Accessibility Ready", "Theme Builder", "Wide Blocks"},
		Screenshot:    "screenshot.png",
		MenuLocations: wbHelloMenuLocations,
		EditorStyles:  []string{"editor-style.css"},
		Supports: map[string]any{
			"custom-logo":          map[string]any{"height": 100, "width": 350, "flex-height": true, "flex-width": true},
			"title-tag":            true,
			"post-thumbnails":      true,
			"menus":                true,
			"html5":                []string{"search-form", "comment-form", "comment-list", "gallery", "caption", "script", "style"},
			"align-wide":           true,
			"responsive-embeds":    true,
			"editor-styles":        true,
			"automatic-feed-links": true,
		},
		Head: func(s *Server, c *gin.Context, v *siteView) string {
			return "<link rel='stylesheet' id='hello-pursae-css' href='" + v.ThemeURI + "/style.css?ver=" + wbHelloVersion + "' media='all' />\n"
		},
		Prepare: func(s *Server, c *gin.Context, v *siteView) { s.wbHelloPrepare(v) },
		BodyClasses: func(s *Server, v *siteView) []string {
			out := []string{wbHelloSlug}
			if wbThemePartsOf(v) != nil {
				out = append(out, "wb-site")
			}
			return out
		},
	})
}

// wbHelloPrepare fills the template data of the theme's minimal defaults: the footer menu and the
// copyright year (in the site's time zone).
func (s *Server) wbHelloPrepare(v *siteView) {
	if items := v.Menus["footer"]; len(items) > 0 {
		v.Data["FooterMenu"] = renderMenu(items, "menu-footer-container", "footer-menu", "", "")
	}
	v.Data["Year"] = strconv.Itoa(time.Now().In(s.loc()).Year())
}

// wbThemePartsOf is the Theme Builder parts of a view, nil when there are none.
func wbThemePartsOf(v *siteView) *wbThemePartsView {
	if v == nil || v.Data == nil {
		return nil
	}
	p, _ := v.Data[wbThemePartsKey].(*wbThemePartsView)
	return p
}
