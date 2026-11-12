package server

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// REST routes of the custom post types registered in ACF › Post Types with "Show in REST":
// /wp/v2/<rest_base> (collection) and /wp/v2/<rest_base>/<id>, served by the same handlers as
// posts and pages. The types come and go while the process runs (the plugin can be switched
// on and off), so the routes are built from acfgoRegistry per request rather than at start-up.

var acfgoRESTRoutes = struct {
	sync.Mutex
	sig    string
	routes []*restRoute
}{}

// acfgoRESTTypeFor maps a rest_base to the key of a REST-enabled ACF post type ("" when none).
func acfgoRESTTypeFor(base string) string {
	acfgoRegistry.RLock()
	defer acfgoRegistry.RUnlock()
	for _, pt := range acfgoRegistry.types {
		if pt.ShowInREST && pt.RestBase == base {
			return pt.Name
		}
	}
	return ""
}

// acfgoRESTBaseFor is the rest_base of a REST-enabled ACF post type ("" when none).
func acfgoRESTBaseFor(typ string) string {
	acfgoRegistry.RLock()
	defer acfgoRegistry.RUnlock()
	if pt := acfgoRegistry.types[typ]; pt != nil && pt.ShowInREST {
		return pt.RestBase
	}
	return ""
}

// acfgoRESTReserved: bases of the core's own /wp/v2 routes can not be taken over by a type.
func (api *restAPI) acfgoRESTReserved(base string) bool {
	probe := "/wp/v2/" + base
	for _, r := range api.routes {
		if r.re.MatchString(probe) || r.re.MatchString(probe+"/1") {
			return true
		}
	}
	return false
}

func (api *restAPI) acfgoTypeRoutes() []*restRoute {
	acfgoRegistry.RLock()
	var typs []string
	for _, pt := range acfgoRegistry.types {
		if pt.ShowInREST && pt.RestBase != "" {
			typs = append(typs, pt.Name+"="+pt.RestBase)
		}
	}
	acfgoRegistry.RUnlock()
	if len(typs) == 0 {
		return nil
	}
	sort.Strings(typs)
	sig := strings.Join(typs, ",")
	acfgoRESTRoutes.Lock()
	defer acfgoRESTRoutes.Unlock()
	if sig == acfgoRESTRoutes.sig {
		return acfgoRESTRoutes.routes
	}
	var out []*restRoute
	for _, e := range typs {
		typ, base, _ := strings.Cut(e, "=")
		if api.acfgoRESTReserved(base) {
			continue
		}
		typ, q := typ, regexp.QuoteMeta(base)
		out = append(out,
			&restRoute{Namespace: "wp/v2", Pattern: "/wp/v2/" + base, re: regexp.MustCompile("^/wp/v2/" + q + "$"), Methods: map[string]restHandler{
				http.MethodGet:  func(c *gin.Context, r *restRequest) { api.listPosts(c, r, typ) },
				http.MethodPost: func(c *gin.Context, r *restRequest) { api.savePost(c, r, typ, 0) },
			}},
			&restRoute{Namespace: "wp/v2", Pattern: "/wp/v2/" + base + `/(?P<id>[\d]+)`, re: regexp.MustCompile("^/wp/v2/" + q + `/(?P<id>[\d]+)$`), Methods: map[string]restHandler{
				http.MethodGet:    func(c *gin.Context, r *restRequest) { api.getPost(c, r, typ) },
				http.MethodPost:   func(c *gin.Context, r *restRequest) { api.savePost(c, r, typ, r.ID()) },
				http.MethodPut:    func(c *gin.Context, r *restRequest) { api.savePost(c, r, typ, r.ID()) },
				http.MethodDelete: func(c *gin.Context, r *restRequest) { api.deletePost(c, r, typ) },
			}},
			// The block editor's autosaves and revisions, as for posts and pages (rest_posts.go).
			&restRoute{Namespace: "wp/v2", Pattern: "/wp/v2/" + base + `/(?P<id>[\d]+)/autosaves`, re: regexp.MustCompile("^/wp/v2/" + q + `/(?P<id>[\d]+)/autosaves$`), Methods: map[string]restHandler{
				http.MethodGet:  func(c *gin.Context, r *restRequest) { api.listAutosaves(c, r, typ) },
				http.MethodPost: func(c *gin.Context, r *restRequest) { api.createAutosave(c, r, typ) },
			}},
			&restRoute{Namespace: "wp/v2", Pattern: "/wp/v2/" + base + `/(?P<parent>[\d]+)/autosaves/(?P<id>[\d]+)`, re: regexp.MustCompile("^/wp/v2/" + q + `/(?P<parent>[\d]+)/autosaves/(?P<id>[\d]+)$`), Methods: map[string]restHandler{
				http.MethodGet: func(c *gin.Context, r *restRequest) { api.getAutosave(c, r, typ) },
			}},
			&restRoute{Namespace: "wp/v2", Pattern: "/wp/v2/" + base + `/(?P<id>[\d]+)/revisions`, re: regexp.MustCompile("^/wp/v2/" + q + `/(?P<id>[\d]+)/revisions$`), Methods: map[string]restHandler{
				http.MethodGet: func(c *gin.Context, r *restRequest) { api.listRevisions(c, r, typ) },
			}},
			&restRoute{Namespace: "wp/v2", Pattern: "/wp/v2/" + base + `/(?P<parent>[\d]+)/revisions/(?P<id>[\d]+)`, re: regexp.MustCompile("^/wp/v2/" + q + `/(?P<parent>[\d]+)/revisions/(?P<id>[\d]+)$`), Methods: map[string]restHandler{
				http.MethodGet:    func(c *gin.Context, r *restRequest) { api.getRevision(c, r, typ) },
				http.MethodDelete: func(c *gin.Context, r *restRequest) { api.deleteRevision(c, r, typ) },
			}})
	}
	acfgoRESTRoutes.sig, acfgoRESTRoutes.routes = sig, out
	return out
}

// acfgoBlockEditorType reports whether a Custom Fields post type opens in the block editor:
// use_block_editor_for_post_type() asks for Show in REST and the "editor" support.
func acfgoBlockEditorType(typ string) bool {
	acfgoRegistry.RLock()
	defer acfgoRegistry.RUnlock()
	pt := acfgoRegistry.types[typ]
	if pt == nil || !pt.ShowInREST || pt.RestBase == "" {
		return false
	}
	for _, f := range pt.Supports {
		if f == "editor" {
			return true
		}
	}
	return false
}

// acfgoRESTTypeObjects adds the Show in REST post types to /wp/v2/types.
func (api *restAPI) acfgoRESTTypeObjects(home string, edit bool, all map[string]map[string]any) {
	acfgoRegistry.RLock()
	defer acfgoRegistry.RUnlock()
	for _, pt := range acfgoRegistry.types {
		if !pt.ShowInREST || pt.RestBase == "" || all[pt.Name] != nil || api.acfgoRESTReserved(pt.RestBase) {
			continue
		}
		sup := map[string]bool{}
		for _, f := range pt.Supports {
			sup[f] = true
		}
		if sup["editor"] {
			sup["autosave"] = true
		}
		labels := typeLabelsFull(pt.label("singular_name"), pt.label("name"))
		for k, v := range pt.Labels {
			if v != "" {
				labels[k] = v
			}
		}
		// The taxonomies stay empty: Custom Fields taxonomies have no REST routes, and the editor
		// would ask /wp/v2/<taxonomy> for every one listed here.
		o := typeObject(home, pt.Name, pt.label("name"), pt.label("singular_name"), pt.RestBase, pt.Hierarchical, sup, []string{}, labels, edit)
		o["has_archive"] = pt.HasArchive
		if pt.MenuIcon != "" {
			o["icon"] = pt.MenuIcon
		}
		if edit {
			o["viewable"] = pt.Public
			o["visibility"] = map[string]bool{"show_ui": pt.ShowUI, "show_in_nav_menus": pt.Public}
			caps := map[string]string{"edit_post": "edit_post", "read_post": "read_post", "delete_post": "delete_post"}
			for k, f := range map[string]string{"edit_posts": "edit_%ss", "edit_others_posts": "edit_others_%ss", "publish_posts": "publish_%ss",
				"read_private_posts": "read_private_%ss", "create_posts": "edit_%ss", "delete_posts": "delete_%ss"} {
				caps[k] = strings.Replace(f, "%s", pt.Name, 1)
			}
			o["capabilities"] = caps
		}
		all[pt.Name] = o
	}
}
