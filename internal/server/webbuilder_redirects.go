package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/webbuilder"
)

// The old builder's admin URLs (bookmarks, links in old mails and pages) answer with a 301 to the
// closest screen of Pursae Web Builder. Its public asset prefixes (/wp-content/plugins/web-builder/
// assets, /wp-content/gowebeditor, …) are not redirected: they 404 like any missing file.

// wbOldAdminScreens are the old builder's own admin screens (under /wp-admin, their names before
// and after the Pursae rename) with where each one goes now. "" means: decided per request by
// wbOldAdminTarget.
var wbOldAdminScreens = map[string]string{
	"/web-builder":               "", // the old builder's admin page (?tab=popups|loops|submissions|code|fonts|roles|settings)
	"/web-builder-icons":         wbSettingsURL,
	"/web-builder-setup":         "/wp-admin/edit?post_type=page",
	"/web-builder-setup/preview": "/wp-admin/edit?post_type=page",
	"/theme-builder":             "/wp-admin/themes",
	"/gowebeditor-pro":           "", // as /web-builder
	"/gowebeditor-icons":         wbSettingsURL,
	"/gowebeditor-theme-builder": "/wp-admin/themes",
	"/golementor-setup":          "/wp-admin/edit?post_type=page",
	"/golementor-setup/preview":  "/wp-admin/edit?post_type=page",
}

// wbOldPostTypes are the old builder's own post types (templates, theme parts, components).
var wbOldPostTypes = map[string]bool{"elementor_library": true, "gwe_template": true, "elementor_component": true}

// wbOldEditActions are the post.php actions that opened the old editor.
var wbOldEditActions = map[string]bool{"elementor": true, "golementor": true, "gowebeditor": true}

// wbOldAdminPages are admin.php?page= screens of the old builder (upstream's names).
var wbOldAdminPages = map[string]bool{"elementor": true, "elementor-settings": true, "elementor-tools": true,
	"elementor-system-info": true, "elementor-role-manager": true, "elementor_custom_fonts": true, "elementor_custom_icons": true,
	"elementor_custom_code": true, "e-form-submissions": true, "go_knowledge_base_site": true, "elementor-app": true}

// wbOldAdminTarget is where the old builder's admin page goes: its Settings tab to Settings › Web
// Builder, everything else (popups, loops, submissions, custom code, fonts, roles) to the Pages
// list, where pages are opened in the builder.
func wbOldAdminTarget(c *gin.Context) string {
	if c.Query("tab") == "settings" {
		return wbSettingsURL
	}
	return "/wp-admin/edit?post_type=page"
}

// registerWebBuilderRedirects adds the 301s of the old builder's admin URLs. The screens with paths
// of their own become routes; the ones that differ only by query (post?action=elementor,
// edit?post_type=elementor_library, admin?page=elementor-settings) are a middleware, so this must
// run before the /wp-admin routes are registered (gin applies Use only to later routes), and the
// old builder's own routes must be gone (the paths would clash).
func (s *Server) registerWebBuilderRedirects(r *gin.Engine) {
	r.Use(wbOldQueryRedirect)
	for path, to := range wbOldAdminScreens {
		r.Match([]string{http.MethodGet, http.MethodHead}, "/wp-admin"+path, wbOldScreenRedirect(to))
	}
}

// wbOldScreenRedirect answers an old screen with its new one (to; "" picks it per request).
func wbOldScreenRedirect(to string) gin.HandlerFunc {
	return func(c *gin.Context) {
		target := to
		if target == "" {
			target = wbOldAdminTarget(c)
		}
		c.Redirect(http.StatusMovedPermanently, target)
	}
}

// wbOldQueryRedirect is the middleware half of registerWebBuilderRedirects: GET and HEAD of the old
// builder's query-string URLs on core screens. Everything else passes through untouched.
func wbOldQueryRedirect(c *gin.Context) {
	if to := wbOldQueryTarget(c.Request); to != "" {
		c.Redirect(http.StatusMovedPermanently, to)
		c.Abort()
		return
	}
	c.Next()
}

// wbOldQueryTarget is the new URL of an old builder URL that is a core screen plus a query, or "".
func wbOldQueryTarget(r *http.Request) string {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return ""
	}
	path := strings.TrimSuffix(r.URL.Path, ".php")
	if !strings.HasPrefix(path, "/wp-admin/") {
		return ""
	}
	q := r.URL.Query()
	switch strings.TrimPrefix(path, "/wp-admin/") {
	case "post":
		if !wbOldEditActions[q.Get("action")] {
			return ""
		}
		id, err := strconv.ParseInt(q.Get("post"), 10, 64)
		if err != nil || id <= 0 {
			return "/wp-admin/edit?post_type=page"
		}
		return "/wp-admin/post?post=" + strconv.FormatInt(id, 10) + "&action=" + webbuilder.EditAction
	case "edit", "post-new":
		if wbOldPostTypes[q.Get("post_type")] {
			return "/wp-admin/edit?post_type=page"
		}
		if strings.HasPrefix(q.Get("action"), "elementor_") { // upstream's "new page with the builder" links
			if pt := q.Get("post_type"); pt == "post" {
				return "/wp-admin/post-new"
			}
			return "/wp-admin/post-new?post_type=page"
		}
	case "admin":
		if wbOldAdminPages[q.Get("page")] {
			if p := q.Get("page"); p == "elementor-settings" || p == "elementor-role-manager" {
				return wbSettingsURL
			}
			return "/wp-admin/edit?post_type=page"
		}
	}
	return ""
}
