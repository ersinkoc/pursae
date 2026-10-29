package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Golementor Studio is the simple editing panel (web/gowebeditor/studio/) mounted over the
// upstream editor: a WordPress-like top bar and a left rail with Add, Edit, Layers and Site. It
// drives the engine only through the editor's public commands ($e.run …), so saving, history,
// revisions and responsive modes are upstream's. The full upstream panel stays in the page (hidden)
// and is one click away: "Advanced" in the top bar switches to it, and the choice is remembered per
// user in the gwe_studio user meta ({"mode": "studio" | "advanced"}).
//
// A Studio page loads studio.css and every script below; an Advanced page loads only the
// configuration and toggle.js, which adds a "Studio" button and loads the rest on demand.

// gweStudioScripts are the Studio's scripts in load order (each is a plain script that adds to
// window.GWEStudio; no build step).
var gweStudioScripts = []string{"core.js", "fields.js", "library.js", "inspector.js", "layers.js", "site.js", "canvas.js", "app.js"}

const gweStudioMeta = "gwe_studio"

// gweStudioMode is the user's editor mode: "studio" (the default) or "advanced".
func gweStudioMode(u *store.User) string {
	if m, _ := gweUserMap(u, gweStudioMeta)["mode"].(string); m == "advanced" {
		return m
	}
	return "studio"
}

// gweStudioInject adds the Studio to the editor page.
func (s *Server) gweStudioInject(c *gin.Context, p *store.Post, page string) string {
	u := currentUser(c)
	mode := gweStudioMode(u)
	lb := s.links(c)
	assets := lb.HomeURL(gweAssetsPath + "studio/")
	ver := gweAssetVer()
	nonceNew := s.nonce(c, "elementor_action_new_post")
	parts := []map[string]any{}
	if posts, err := s.gwePartPosts(c, ""); err == nil {
		for _, pp := range posts {
			typ := gweDocType(pp)
			parts = append(parts, map[string]any{"id": pp.ID, "title": pp.Title, "type": typ, "label": gwePartLabel(typ), "status": pp.Status, "edit": lb.HomeURL(gweEditURL(pp.ID))})
		}
	}
	scripts := make([]string, 0, len(gweStudioScripts))
	for _, f := range gweStudioScripts {
		scripts = append(scripts, assets+f+"?ver="+ver)
	}
	cfg := map[string]any{
		"mode":       mode,
		"postId":     p.ID,
		"rest":       lb.HomeURL("/wp-json/"),
		"nonce":      s.nonce(c, "wp_rest"),
		"assets":     lb.HomeURL(gweAssetsPath),
		"css":        assets + "studio.css?ver=" + ver,
		"scripts":    scripts,
		"editURL":    lb.HomeURL("/wp-admin/post?action=golementor&post="),
		"parts":      parts,
		"canEditKit": u != nil && (u.Can("manage_options", nil) || u.Can("edit_theme_options", nil)),
		"links": map[string]string{
			"dashboard":    lb.HomeURL("/wp-admin/"),
			"siteSetup":    lb.HomeURL("/wp-admin/golementor-setup"),
			"themeBuilder": lb.HomeURL(gweThemeBuilderPath),
			"templates":    lb.HomeURL("/wp-admin/edit?post_type=" + gweLibrary),
			"settings":     lb.HomeURL(gweProAdminPath + "?tab=settings"),
			"pages":        lb.HomeURL("/wp-admin/edit?post_type=page"),
			"newPage":      lb.HomeURL("/wp-admin/edit?action=elementor_new_post&post_type=page&_wpnonce=" + nonceNew),
			"newHeader":    lb.HomeURL("/wp-admin/edit?action=elementor_new_post&post_type=" + gweLibrary + "&template_type=header&_wpnonce=" + nonceNew),
			"newFooter":    lb.HomeURL("/wp-admin/edit?action=elementor_new_post&post_type=" + gweLibrary + "&template_type=footer&_wpnonce=" + nonceNew),
		},
	}
	head := ""
	body := "<script id=\"gwe-studio-config\">window.GWEStudioConfig = " + gweJSON(cfg) + ";</script>\n"
	if mode == "studio" {
		// The class is set before the upstream UI paints, so the Studio is what the splash reveals.
		head = `<link rel="stylesheet" id="gwe-studio-css" href="` + assets + `studio.css?ver=` + ver + `" media="all">` + "\n"
		body += "<script>document.body.classList.add('gwe-studio');</script>\n"
	}
	body += `<script id="gwe-studio-toggle" src="` + assets + `toggle.js?ver=` + ver + `"></script>` + "\n"
	if mode == "studio" {
		for _, src := range scripts {
			body += `<script src="` + src + `"></script>` + "\n"
		}
	}
	if head != "" {
		page = strings.Replace(page, "</head>", head+"</head>", 1)
	}
	return strings.Replace(page, "</body>", body+"</body>", 1)
}

// registerStudioREST adds golementor/v1/studio/preferences: GET returns and POST saves the
// current user's Studio settings ({"mode": "studio" | "advanced"}).
func (s *Server) registerStudioREST() {
	const ns = "golementor/v1"
	handler := func(c *gin.Context, r *restRequest) {
		u := restCurrent(c)
		if u == nil || !u.Can("edit_posts", nil) {
			restErr(c, http.StatusUnauthorized, "rest_forbidden", "Sorry, you are not allowed to do that.")
			return
		}
		if r.Method == http.MethodPost {
			mode, _ := r.Str(c, "mode")
			if mode != "studio" && mode != "advanced" {
				restErr(c, http.StatusBadRequest, "rest_invalid_param", "mode must be studio or advanced.")
				return
			}
			if err := s.gweSetUserMeta(c, u, gweStudioMeta, map[string]any{"mode": mode}); err != nil {
				restErr(c, http.StatusInternalServerError, "rest_error", err.Error())
				return
			}
			c.JSON(http.StatusOK, map[string]any{"mode": mode})
			return
		}
		c.JSON(http.StatusOK, map[string]any{"mode": gweStudioMode(u)})
	}
	s.rest.register(ns, "/golementor/v1/studio/preferences", map[string]restHandler{http.MethodGet: handler, http.MethodPost: handler}, nil)
}
