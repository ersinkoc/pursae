package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/gin-gonic/gin"

	"wpgo/internal/builder"
	"wpgo/internal/store"
)

// REST routes the Golementor shell calls (namespace elementor/v1, kept for compatibility with the
// upstream front end): kit globals, the current user's capabilities, recent posts for the site
// navigation; the v4 (atomic) routes live in gowebeditor_v4_rest.go.

// gweRESTNamespace reports whether a REST namespace belongs to Golementor (its routes are 404 while it is off).
func gweRESTNamespace(ns string) bool {
	return ns == "elementor/v1" || ns == "elementor-pro/v1" || ns == "golementor/v1"
}

// gweSetSVGLoader points builder.SVGLoader at this site's uploads.
func (s *Server) gweSetSVGLoader() {
	builder.SVGLoader = func(id int64) string {
		a, err := s.store.GetAttachment(context.Background(), id)
		if err != nil || a.Mime != "image/svg+xml" || a.FileSize > 1<<20 {
			return ""
		}
		b, err := os.ReadFile(s.gocdnFetchBack(context.Background(), filepath.Join(s.cfg.UploadsDir, filepath.Clean("/"+a.FilePath))))
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func (s *Server) registerGoWebEditorREST() {
	// Uploaded SVG icons render inline (Svg::get_inline_svg); the renderer asks for their markup.
	// The hook is process-wide: a theme-preview sandbox (no Golementor) leaves the site's in place.
	if s.sandbox == nil {
		s.gweSetSVGLoader()
	}
	api := s.rest
	const ns = "elementor/v1"
	editor := func(h restHandler) restHandler {
		return func(c *gin.Context, r *restRequest) {
			u := restCurrent(c)
			if u == nil || !u.Can("edit_posts", nil) {
				restErr(c, http.StatusUnauthorized, "rest_forbidden", "Sorry, you are not allowed to do that.")
				return
			}
			h(c, r)
		}
	}
	s.registerGolementorLibraryREST(editor) // Golementor Studio library (golementor_library.go)
	static := func(v any) restHandler {
		return editor(func(c *gin.Context, _ *restRequest) { c.JSON(http.StatusOK, v) })
	}

	globals := editor(func(c *gin.Context, r *restRequest) {
		_, settings := s.gweKitSettings(c)
		g := gweGlobals(settings)
		switch {
		case r.Params["kind"] == "":
			c.JSON(http.StatusOK, g)
		case r.Params["id"] == "":
			c.JSON(http.StatusOK, g[r.Params["kind"]])
		default:
			list, _ := g[r.Params["kind"]].(map[string]any)
			if v, ok := list[r.Params["id"]]; ok {
				c.JSON(http.StatusOK, v)
				return
			}
			restErr(c, http.StatusNotFound, "rest_not_found", "Global not found.")
		}
	})
	api.register(ns, "/elementor/v1/globals", map[string]restHandler{http.MethodGet: globals}, nil)
	api.register(ns, "/elementor/v1/globals/(?P<kind>colors|typography)", map[string]restHandler{http.MethodGet: globals}, nil)
	api.register(ns, "/elementor/v1/globals/(?P<kind>colors|typography)/(?P<id>[\\w-]+)", map[string]restHandler{http.MethodGet: globals}, nil)

	api.register(ns, "/elementor/v1/user-data/current-user", map[string]restHandler{http.MethodGet: editor(func(c *gin.Context, _ *restRequest) {
		u := restCurrent(c)
		var list []string
		for k, ok := range u.Caps {
			if ok {
				list = append(list, k)
			}
		}
		sort.Strings(list)
		list = append(list, gweV4UserCaps(u)...) // Golementor v4: atomic capabilities (gowebeditor_v4.go)
		list = append(list, u.Roles...)
		c.JSON(http.StatusOK, map[string]any{"suppressedMessages": []any{}, "capabilities": list})
	}), http.MethodPatch: editor(func(c *gin.Context, _ *restRequest) {
		c.JSON(http.StatusOK, map[string]any{"suppressedMessages": []any{}})
	})}, nil)

	api.register(ns, "/elementor/v1/site-navigation/recent-posts", map[string]restHandler{http.MethodGet: editor(func(c *gin.Context, _ *restRequest) {
		n, _ := strconv.Atoi(c.DefaultQuery("posts_per_page", "6"))
		if n <= 0 || n > 50 {
			n = 6
		}
		posts, _, err := s.store.QueryPosts(c, store.PostQuery{Types: []string{"page", "post"}, Statuses: []string{"publish", "draft", "private", "pending", "future"},
			PerPage: n * 3, Page: 1, OrderBy: "modified", Order: "DESC"})
		if err != nil {
			restErr(c, http.StatusInternalServerError, "rest_error", err.Error())
			return
		}
		u := restCurrent(c)
		out := []map[string]any{}
		lb := s.links(c)
		for _, p := range posts {
			if !gweBuilt(p) || !u.Can("edit_post", p) || len(out) >= n {
				continue
			}
			label := "Post"
			if p.Type == "page" {
				label = "Page"
			}
			out = append(out, map[string]any{"id": p.ID, "title": p.Title, "edit_url": lb.HomeURL(gweEditURL(p.ID)),
				"date_modified": p.ModifiedAt.Unix(), "type": map[string]any{"post_type": p.Type, "doc_type": gweDocType(p), "label": label},
				"user_can": map[string]bool{"edit": true}})
		}
		c.JSON(http.StatusOK, out)
	})}, nil)

	// The Pages panel (editor-site-navigation): the static front page it marks with the home icon, and
	// Duplicate. "Set as homepage" posts show_on_front/page_on_front to wp/v2/settings, so it is the
	// same setting as Settings › Reading.
	api.register(ns, "/elementor/v1/site-navigation/homepage", map[string]restHandler{http.MethodGet: editor(func(c *gin.Context, _ *restRequest) {
		id := 0
		if s.settings.String("show_on_front") == "page" {
			id = s.settings.Int("page_on_front")
		}
		c.JSON(http.StatusOK, id)
	})}, nil)
	api.register(ns, "/elementor/v1/site-navigation/duplicate-post", map[string]restHandler{http.MethodPost: editor(func(c *gin.Context, r *restRequest) {
		id, _ := r.Int(c, "post_id")
		src, err := s.store.GetPost(c, int64(id))
		u := restCurrent(c)
		if err != nil || (src.Type != "page" && src.Type != "post") || !u.Can("edit_post", src) || !u.Can("edit_"+src.Type+"s", nil) {
			restErr(c, http.StatusNotFound, "rest_post_invalid_id", "Invalid post ID.")
			return
		}
		title, _ := r.Str(c, "title")
		if title == "" {
			title = src.Title
		}
		meta := map[string]any{}
		for k, v := range src.Meta {
			if k != "_wp_old_slug" && k != "_edit_lock" {
				meta[k] = v
			}
		}
		p := &store.Post{Type: src.Type, AuthorID: u.ID, Status: "draft", Title: title + " (copy)", Content: src.Content,
			Excerpt: src.Excerpt, ParentID: src.ParentID, Meta: meta}
		if err := s.store.InsertPost(c, p); err != nil {
			restErr(c, http.StatusInternalServerError, "rest_error", err.Error())
			return
		}
		s.gweClearCSS()
		c.JSON(http.StatusOK, p.ID)
	})}, nil)

	api.register(ns, "/elementor/v1/site-navigation/add-new-post", map[string]restHandler{http.MethodPost: editor(func(c *gin.Context, r *restRequest) {
		typ, _ := r.Str(c, "post_type")
		if typ != "post" && typ != "page" {
			restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid post type.")
			return
		}
		u := restCurrent(c)
		if !u.Can("edit_"+typ+"s", nil) {
			restErr(c, http.StatusForbidden, "rest_forbidden", "Sorry, you are not allowed to do that.")
			return
		}
		p := &store.Post{Type: typ, AuthorID: u.ID, Status: "draft", Title: "(no title)", Meta: map[string]any{"_elementor_edit_mode": "builder", "_elementor_data": "[]"}}
		if err := s.store.InsertPost(c, p); err != nil {
			restErr(c, http.StatusInternalServerError, "rest_error", err.Error())
			return
		}
		c.JSON(http.StatusOK, map[string]any{"id": p.ID, "edit_url": s.links(c).HomeURL(gweEditURL(p.ID))})
	})}, nil)

	s.registerGoWebEditorV4REST() // Golementor v4 (atomic): gowebeditor_v4_rest.go
	// The template library's list ($e.data "library/templates"): saved templates (source local) and the
	// ready-made homepage designs (source remote, the Pages tab) and the Studio sections (source remote,
	// type block, the Blocks tab).
	api.register(ns, "/elementor/v1/template-library/templates", map[string]restHandler{http.MethodGet: editor(func(c *gin.Context, _ *restRequest) {
		res, err := s.gweLibraryData(c, nil)
		if err != nil {
			restErr(c, http.StatusInternalServerError, "rest_error", err.Error())
			return
		}
		all, _ := res.(map[string]any)["templates"].([]map[string]any)
		out := []map[string]any{}
		src := c.Query("source")
		for _, t := range all {
			if src == "" || t["source"] == src {
				out = append(out, t)
			}
		}
		c.JSON(http.StatusOK, map[string]any{"templates": out, "config": res.(map[string]any)["config"]})
	})}, nil)
	api.register(ns, "/elementor/v1/favorites", map[string]restHandler{http.MethodGet: static([]any{})}, nil)
	api.register(ns, "/elementor/v1/favorites/(?P<id>[\\w]+)", map[string]restHandler{http.MethodPost: static(map[string]any{}), http.MethodDelete: static(map[string]any{})}, nil)
	api.register(ns, "/elementor/v1/settings/(?P<key>[\\w_-]+)", map[string]restHandler{http.MethodPost: static(map[string]any{"success": true}), http.MethodPut: static(map[string]any{"success": true})}, nil)
	api.register(ns, "/elementor/v1/post", map[string]restHandler{http.MethodGet: static([]any{})}, nil)
	api.register(ns, "/elementor/v1/cache", map[string]restHandler{http.MethodDelete: editor(func(c *gin.Context, _ *restRequest) {
		s.gweClearCSS()
		c.JSON(http.StatusOK, map[string]any{"success": true})
	})}, nil)
	s.registerGoWebEditorProREST() // Golementor Pro routes (gowebeditor_pro.go)
}
