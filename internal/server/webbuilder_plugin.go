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
// While both builders ship side by side, the new editor opens with action=web-builder-next
// (wbEditAction) and action=web-builder keeps opening the old editor; the switchover points
// wbEditAction at webbuilder.EditAction. New code uses wb* names and webbuilder_*.go files, the
// REST namespace web-builder/v2 and the asset prefixes /wp-content/plugins/web-builder/dist/ and
// /wp-content/web-builder/site/, none of which the old builder uses.

// wbEditAction is the postEdit action that opens the new editor (webbuilder_editor.go).
const wbEditAction = "web-builder-next"

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

// wbPostTypes are the post types the builder edits: pages and posts.
var wbPostTypes = []string{"page", "post"}

// wbEnabledType reports whether posts of the type open in the builder.
func wbEnabledType(typ string) bool {
	for _, t := range wbPostTypes {
		if t == typ {
			return true
		}
	}
	return false
}

// registerWebBuilder adds the builder's routes that live outside wp-admin and the REST API.
func (s *Server) registerWebBuilder(r *gin.Engine) {
	s.wbRegisterAssets(r) // webbuilder_assets.go
}
