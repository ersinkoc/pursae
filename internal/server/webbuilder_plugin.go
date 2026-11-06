package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wpgo/internal/webbuilder"
)

// Pursae Web Builder: the React editor built from github.com/pursae/Builder (builder/, embedded from
// web/web-builder) as a native built-in plugin. It keeps the plugin identity of the builder it
// replaces (webbuilder.PluginSlug, webbuilder.PluginOption, visible name "Pursae Web Builder"). The
// integration contract is docs/developer/web-builder-contract.md; the server-independent pieces
// (doc validation, sanitizers, fallback HTML) live in internal/webbuilder, the storage in
// internal/store/webbuilder.go.
//
// The editor opens with /wp-admin/post?post={id}&action=web-builder (wbEditAction); the
// development action web-builder-next of the side-by-side phase answers 301 to it. The entry
// points are WordPress' own: the editors' "Edit with" button, the list row action and post state
// (webbuilder_admin.go) and the toolbar node on the site (frontAdminBar). New code uses wb* names
// and webbuilder_*.go files, the REST namespace web-builder/v2 and the asset prefixes
// /wp-content/plugins/web-builder/dist/ and /wp-content/web-builder/site/.

// wbEditAction is the postEdit action that opens the editor (webbuilder_editor.go).
const wbEditAction = webbuilder.EditAction

// wbEditActionDev is the editor's action while both builders shipped; it redirects to wbEditAction.
const wbEditActionDev = "web-builder-next"

// wbOn reports whether Pursae Web Builder is active: the same option as the plugin it replaces,
// one cached read.
func (s *Server) wbOn() bool {
	return s.settings.Bool(webbuilder.PluginOption)
}

// wbOnly is the middleware of the builder's own routes (assets, REST): 404 while the plugin is off.
func (s *Server) wbOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.wbOn() {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}

// wbEditURL is the admin URL that opens a post in the builder (site-relative).
func wbEditURL(id int64) string {
	return "/wp-admin/post?post=" + itoa64(id) + "&action=" + wbEditAction
}

// registerWebBuilder adds the builder's routes that live outside wp-admin and the REST API.
func (s *Server) registerWebBuilder(r *gin.Engine) {
	s.wbRegisterAssets(r) // webbuilder_assets.go
}
