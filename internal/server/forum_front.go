package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// The forum's front-end entry: site() hands every path to forumSiteIntercept after the Commerce
// intercept; paths under the forum base are the forum's (§ forum_urls.go). GET views go to the front
// end (forum_views.go) or membership (forum_account.go); POST forms carry forum_action.

// forumCtxRoute is the gin key of the parsed forum route of the request.
const forumCtxRoute = "forum.front.route"

// forumRouteFrom is the forum route forumSiteIntercept parsed (nil off the forum).
func forumRouteFrom(c *gin.Context) *forumRoute {
	if c == nil {
		return nil
	}
	if v, ok := c.Get(forumCtxRoute); ok {
		rt, _ := v.(*forumRoute)
		return rt
	}
	return nil
}

// forumAccountActions are the POST actions membership handles; forumFrontActions the front end's.
var (
	forumAccountActions = map[string]bool{"login": true, "register": true, "resend": true, "account": true, "password": true, "google_unlink": true,
		"avatar_upload": true, "avatar_remove": true}
	forumFrontActions = map[string]bool{"new_topic": true, "reply": true, "edit": true, "moderate": true, "block": true, "unblock": true,
		"badge_award": true, "badge_revoke": true}
)

// forumMaxBody bounds the body of a forum form POST. A profile photo may be at most 2 MB, but a
// larger phone photo still reaches the form handler, which says so (above this the form fails).
const forumMaxBody = 16 << 20

// forumAcceptsPost reports whether a POST to a site URL is a forum form: the plugin is on, the path
// is under the forum base and the form carries forum_action.
func (s *Server) forumAcceptsPost(c *gin.Context) bool {
	if c.Request.Method != http.MethodPost || !s.forumOn() {
		return false
	}
	path := s.golylangSitePath(c, strings.Trim(c.Request.URL.Path, "/"))
	if _, ok := forumParsePath(s.forumBase(), path); !ok {
		return false
	}
	// Bound the body before parsing it: ParseMultipartForm only caps the memory it uses and spools
	// the rest of any size to temporary files.
	if c.Request.Body != nil && c.Request.Body != http.NoBody {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, forumMaxBody)
	}
	if err := c.Request.ParseMultipartForm(8 << 20); err != nil && c.Request.PostForm == nil {
		_ = c.Request.ParseForm()
	}
	_, ok := c.Request.PostForm["forum_action"]
	return ok
}

// forumSiteIntercept serves the forum's URLs; false lets the core resolve the request. A path under
// the base that no view serves is the theme's 404 (the forum owns its base).
func (s *Server) forumSiteIntercept(c *gin.Context, path string) bool {
	if !s.forumOn() {
		return false
	}
	rt, ok := forumParsePath(s.forumBase(), path)
	if !ok {
		return false
	}
	c.Set(forumCtxRoute, &rt)
	switch c.Request.Method {
	case http.MethodPost:
		action := c.PostForm("forum_action")
		switch {
		case forumAccountActions[action]:
			s.forumAccountPost(c, rt, action) // WP2 (forum_account.go)
		case forumFrontActions[action]:
			s.forumFrontPost(c, rt, action) // WP1 (forum_views.go)
		default:
			c.String(http.StatusBadRequest, "Unknown forum action.")
		}
		return true
	case http.MethodGet, http.MethodHead:
	default:
		return false
	}
	var handled bool
	switch rt.View {
	case forumViewLogin, forumViewRegister, forumViewVerify, forumViewAccount:
		handled = s.forumRouteAccount(c, rt) // WP2 (forum_account.go)
	default:
		handled = s.forumRouteFront(c, rt) // WP1 (forum_views.go)
	}
	if !handled && !c.Writer.Written() {
		s.forumNotFound(c)
	}
	return true
}
