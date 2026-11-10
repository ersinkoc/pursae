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
			}})
	}
	acfgoRESTRoutes.sig, acfgoRESTRoutes.routes = sig, out
	return out
}
