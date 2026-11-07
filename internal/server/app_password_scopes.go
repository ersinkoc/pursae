package server

import (
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/caps"
	"wpgo/internal/store"
)

// Application Password scopes narrow what a password may do. A password without scopes keeps the
// user's full capabilities (WordPress behaviour); a password with scopes is limited twice:
//
//  1. the user's capabilities are cut down to the ones the scopes need (appScopeCaps), so a handler's
//     own u.Can checks deny anything else, and
//  2. each REST route belongs to one scope (appScopeForRoute); a route of another scope, or one that
//     belongs to no scope (plugin routes, application passwords themselves), is refused.
//
// Scopes only ever remove capabilities: a scope's list is intersected with what the user already has.

// appScope describes one scope for the profile screen and the docs.
type appScope struct {
	Slug, Label, Description string
	Caps                     []string
}

var postCaps = []string{"read", "edit_posts", "edit_others_posts", "edit_published_posts", "edit_private_posts", "publish_posts",
	"delete_posts", "delete_others_posts", "delete_published_posts", "delete_private_posts", "read_private_posts",
	"edit_pages", "edit_others_pages", "edit_published_pages", "edit_private_pages", "publish_pages",
	"delete_pages", "delete_others_pages", "delete_published_pages", "delete_private_pages", "read_private_pages"}

// appScopes are the scopes in display order.
var appScopes = []appScope{
	{"content", "Content", "Posts, pages, reusable blocks, categories, tags, comments and Web Builder documents.",
		append(slices.Clone(postCaps), "manage_categories", "moderate_comments", "edit_comment", "unfiltered_html", "list_users")},
	{"media", "Media", "Upload and edit files in the Media Library.",
		[]string{"read", "upload_files", "edit_posts", "edit_others_posts", "edit_published_posts", "delete_posts", "delete_others_posts", "delete_published_posts"}},
	{"themes", "Appearance", "Themes, theme files, templates, global styles, menus and widgets.",
		[]string{"read", "switch_themes", "edit_theme_options", "edit_themes", "install_themes", "update_themes", "delete_themes", "customize"}},
	{"plugins", "Plugins", "List, install, activate and deactivate plugins.",
		[]string{"read", "activate_plugins", "install_plugins", "update_plugins", "delete_plugins"}},
	{"cache", "Cache", "Pursae Cache status, purging and settings.",
		[]string{"read", "manage_options"}},
	{"seo", "SEO", "Pursae SEO meta, analysis, site-wide settings and redirects.",
		append(slices.Clone(postCaps), "manage_options", "seogo_onpage_general", "seogo_onpage_advanced", "seogo_onpage_snippet", "seogo_onpage_social",
			"seogo_titles", "seogo_general", "seogo_sitemap", "seogo_redirections", "seogo_404_monitor", "seogo_link_builder", "seogo_onpage_analysis")},
	{"settings", "Settings", "Site settings and the options of the built-in plugins.",
		[]string{"read", "manage_options"}},
	{"users", "Users", "List, create and edit users.",
		[]string{"read", "list_users", "create_users", "edit_users", "promote_users", "delete_users"}},
}

func appScopeBySlug(slug string) *appScope {
	for i := range appScopes {
		if appScopes[i].Slug == slug {
			return &appScopes[i]
		}
	}
	return nil
}

// normalizeAppScopes keeps the known scopes, once each, in display order.
func normalizeAppScopes(in []string) []string {
	want := map[string]bool{}
	for _, v := range in {
		want[strings.TrimSpace(strings.ToLower(v))] = true
	}
	out := []string{}
	for _, sc := range appScopes {
		if want[sc.Slug] {
			out = append(out, sc.Slug)
		}
	}
	return out
}

// scopedUser is a copy of u whose capabilities are cut down to the scopes. Roles are kept for
// display; every capability check goes through Caps.
func scopedUser(u *store.User, scopes []string) *store.User {
	if u == nil || len(scopes) == 0 {
		return u
	}
	allowed := map[string]bool{}
	for _, slug := range scopes {
		if sc := appScopeBySlug(slug); sc != nil {
			for _, c := range sc.Caps {
				allowed[c] = true
			}
		}
	}
	cp := *u
	cp.Caps = caps.Grants{}
	for c, ok := range u.Caps {
		if ok && allowed[c] {
			cp.Caps[c] = true
		}
	}
	return &cp
}

const restAppScopesKey = "wpgo.restAppScopes" // []string: scopes of the application password in use

// restScopes are the scopes of the application password that authenticated the request (nil: none,
// so the user's full capabilities apply).
func restScopes(c *gin.Context) []string {
	if v, ok := c.Get(restAppScopesKey); ok {
		if sc, ok := v.([]string); ok {
			return sc
		}
	}
	return nil
}

// appScopeRoutes maps route prefixes to the scope that covers them. The longest matching prefix wins.
var appScopeRoutes = map[string]string{
	"/wp/v2/posts": "content", "/wp/v2/pages": "content", "/wp/v2/blocks": "content", "/wp/v2/categories": "content",
	"/wp/v2/tags": "content", "/wp/v2/comments": "content", "/wp/v2/taxonomies": "content", "/wp/v2/types": "content",
	"/wp/v2/statuses": "content", "/wp/v2/search": "content", "/wp/v2/block-renderer": "content", "/wp/v2/block-types": "content",
	"/wp/v2/block-patterns": "content", "/wp/v2/pattern-directory": "content", "/wp/v2/wp_pattern_category": "content",
	"/web-builder/v2": "content", "/headlessgo/v1": "content", "/pll/v1": "content", "/batch/v1": "content", "/oembed/1.0": "content",
	"/wp/v2/media":     "media",
	"/wp/v2/templates": "themes", "/wp/v2/template-parts": "themes", "/wp/v2/global-styles": "themes", "/wp/v2/themes": "themes",
	"/wp/v2/menus": "themes", "/wp/v2/menu-items": "themes", "/wp/v2/menu-locations": "themes", "/wp/v2/navigation": "themes",
	"/wp/v2/sidebars": "themes", "/wp/v2/widgets": "themes", "/wp/v2/widget-types": "themes", "/pursae/v1/themes": "themes",
	"/pursae/v1/plugins": "plugins",
	"/pursae/v1/cache":   "cache",
	"/seogo/v1":          "seo", "/pursae/v1/seo": "seo",
	"/wp/v2/settings": "settings", "/pursae/v1/options": "settings",
	"/wp/v2/users": "users",
}

// appScopeForRoute is the scope a route belongs to: "" for routes no scope covers, "*" for routes any
// scoped password may use (the index, the MCP endpoint, the signed-in user's own record).
func appScopeForRoute(route string) string {
	route = "/" + strings.Trim(route, "/")
	if strings.Contains(route, "/application-passwords") {
		return "" // a scoped password must not mint or revoke passwords
	}
	switch route {
	case "/", "/wp/v2", "/pursae/v1", "/pursae/v1/mcp", "/wp/v2/users/me":
		return "*"
	}
	best, scope := "", ""
	for p, sc := range appScopeRoutes {
		if (route == p || strings.HasPrefix(route, p+"/")) && len(p) > len(best) {
			best, scope = p, sc
		}
	}
	return scope
}

// restScopeAllows reports whether the scopes of the request's password cover the route.
func restScopeAllows(c *gin.Context, route string) bool {
	scopes := restScopes(c)
	if len(scopes) == 0 {
		return true
	}
	need := appScopeForRoute(route)
	return need == "*" || (need != "" && slices.Contains(scopes, need))
}
