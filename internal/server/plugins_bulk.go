package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// Plugins › Bulk actions (activate-selected, deactivate-selected, delete-selected), as in
// WordPress's plugins.php. Each selected plugin goes through the same pluginAction as its row
// link, so native plugins keep their own activation work. A Wasm plugin is bulk-activated only
// with the permissions the admin approved before; one that was never approved is left for its
// own Activate link, which shows the permission review.

func (s *Server) pluginsBulk(c *gin.Context, action string) {
	if !s.checkNonce(c, "bulk-plugins") {
		s.denyNonce(c)
		return
	}
	u := currentUser(c)
	slugs := c.PostFormArray("checked[]")
	if len(slugs) == 0 {
		c.Redirect(http.StatusFound, "/wp-admin/plugins")
		return
	}
	rows := map[string]pluginRow{}
	for _, r := range s.installedPlugins(c) {
		rows[r.Slug] = r
	}
	var done int
	var skipped, errs []string
	for _, slug := range slugs {
		r, ok := rows[slug]
		if !ok {
			continue
		}
		var single string
		switch action {
		case "activate-selected":
			if r.Active {
				continue
			}
			single = "activate"
		case "deactivate-selected":
			if !r.Active {
				continue
			}
			single = "deactivate"
		case "delete-selected":
			if r.Native || r.Active || !u.Can("delete_plugins", nil) {
				skipped = append(skipped, r.Name)
				continue
			}
			single = "delete"
		default:
			c.Redirect(http.StatusFound, "/wp-admin/plugins")
			return
		}
		form := url.Values{"_wpnonce": {s.nonce(c, single+"-plugin_"+slug)}, "action": {single}, "plugin": {slug}}
		if single == "activate" && !r.Native {
			var perms []string
			_ = s.store.Pool.QueryRow(c, `SELECT coalesce(array(SELECT jsonb_array_elements_text(permissions)), '{}') FROM plugins WHERE slug = $1 AND permissions IS NOT NULL AND jsonb_typeof(permissions) = 'array'`, slug).Scan(&perms)
			if len(r.Permissions) > 0 && len(perms) == 0 { // never reviewed: needs its own consent screen
				skipped = append(skipped, r.Name)
				continue
			}
			for _, p := range perms {
				form.Add("permissions[]", p)
			}
		}
		loc := s.runPluginAction(c, form)
		if strings.Contains(loc, "error=") {
			if q, err := url.ParseQuery(loc[strings.Index(loc, "?")+1:]); err == nil {
				errs = append(errs, r.Name+": "+q.Get("error"))
			}
			continue
		}
		done++
	}
	q := url.Values{}
	switch action {
	case "activate-selected":
		q.Set("activate-multi", fmt.Sprint(done))
	case "deactivate-selected":
		q.Set("deactivate-multi", fmt.Sprint(done))
	case "delete-selected":
		q.Set("deleted-multi", fmt.Sprint(done))
	}
	if len(skipped) > 0 {
		q.Set("skipped", strings.Join(skipped, ", "))
	}
	if len(errs) > 0 {
		q.Set("error", strings.Join(errs, "; "))
	}
	c.Redirect(http.StatusFound, "/wp-admin/plugins?"+q.Encode())
}

// runPluginAction runs pluginAction for one plugin on a copy of the request and returns where it
// would have redirected.
func (s *Server) runPluginAction(c *gin.Context, form url.Values) string {
	rec := httptest.NewRecorder()
	sub, _ := gin.CreateTestContext(rec)
	req := c.Request.Clone(c.Request.Context())
	req.Method = http.MethodPost
	req.Body = nil
	req.PostForm = form
	req.Form = form
	sub.Request = req
	sub.Keys = c.Keys
	s.pluginAction(sub, form.Get("action"), form.Get("plugin"))
	return rec.Header().Get("Location")
}
