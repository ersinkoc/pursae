package server

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// mcpToolRoute is the REST route a tool reaches, with {placeholders} left as plain words.
func mcpToolRoute(t *mcpTool) string {
	return mcpPathArg.ReplaceAllString(t.Path, "x")
}

// mcpRouteCaps are the capabilities a route's handlers need, by prefix (longest wins); a route
// without an entry is not filtered by capability (its handler checks per object).
var mcpRouteCaps = map[string]string{
	"/pursae/v1/plugins": "activate_plugins", "/pursae/v1/themes": "switch_themes", "/pursae/v1/cache": "manage_options",
	"/pursae/v1/seo": "manage_options", "/pursae/v1/options": "manage_options", "/pursae/v1/headless": "manage_options",
	"/wp/v2/settings": "manage_options", "/wp/v2/users": "list_users", "/pursae/v1/mcp/activity": "manage_options",
	"/pursae/v1/health": "manage_options", "/pursae/v1/backups": "manage_options", "/pursae/v1/updates": "update_plugins",
	"/wc/v3/products": "edit_products", "/wc/v3/orders": "edit_shop_orders", "/web-builder/v2": "edit_posts",
}

// mcpToolAvailable reports whether the caller can use a tool: the scopes of the credential cover its
// route, the user holds the capability it needs, and the built-in plugin it belongs to is active.
// tools/call checks this before running a tool; the REST layer checks the route again.
func (s *Server) mcpToolAvailable(c *gin.Context, t *mcpTool) bool {
	if t.special != "" {
		return true // site_info, rest_request
	}
	route := mcpToolRoute(t)
	if !restScopeAllows(c, route) {
		return false
	}
	if strings.HasPrefix(route, "/seogo/v1") || strings.HasPrefix(route, "/pursae/v1/seo") {
		if !s.seogoOn() {
			return false
		}
	}
	if strings.HasPrefix(route, "/pursae/v1/headless") && !s.hgOn() {
		return false
	}
	if strings.HasPrefix(route, "/wc/v3") && !s.gooOn() {
		return false
	}
	if strings.HasPrefix(route, "/web-builder/v2") && !s.wbOn() {
		return false
	}
	if u := restCurrent(c); u != nil {
		best, cp := "", ""
		for p, k := range mcpRouteCaps {
			if (route == p || strings.HasPrefix(route, p+"/")) && len(p) > len(best) {
				best, cp = p, k
			}
		}
		if cp != "" && !u.Can(cp, nil) {
			return false
		}
	}
	return true
}
