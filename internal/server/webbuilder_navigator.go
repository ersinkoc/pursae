package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// wbRESTNavigator is GET navigator: the editor's page switcher list. Templates need
// edit_theme_options; posts are the newest-modified ones of each enabled post type that the user may
// edit (only their own without edit_others_<type>s), trash and auto-drafts excluded. One query per
// post type, no per-row lookups.
func (s *Server) wbRESTNavigator(c *gin.Context, r *restRequest) {
	u := restCurrent(c)
	if u == nil || !u.Can("edit_posts", nil) && !u.Can(webbuilder.TemplateCap, nil) {
		wbForbidden(c, u)
		return
	}
	lb := s.links(c)
	out := webbuilder.NavigatorResponse{Templates: []webbuilder.NavigatorTemplate{}, Posts: []webbuilder.NavigatorPost{}}

	if u.Can(webbuilder.TemplateCap, nil) {
		list, err := s.store.WebBuilder().Templates(c, store.WBTemplateFilter{Limit: webbuilder.NavigatorMaxTemplates})
		if err != nil {
			restErr(c, http.StatusInternalServerError, "db_error", "Could not read the templates.")
			return
		}
		for _, t := range list {
			out.Templates = append(out.Templates, webbuilder.NavigatorTemplate{ID: t.ID, Title: t.Title, Type: t.Type,
				TypeLabel: webbuilder.TemplateTypeLabels[t.Type], Status: t.Status, EditURL: lb.HomeURL(wbEditURL(t.ID)),
				Disabled: t.Status != "publish"})
		}
	}

	labels := map[string]string{}
	for _, ch := range s.wbPostTypeChoices(c) {
		labels[ch.Slug] = ch.Label
	}
	for _, typ := range s.wbEnabledTypes() {
		if typ == webbuilder.TemplatePostType || !s.wbTypeRegistered(typ) {
			continue
		}
		q := store.PostQuery{Types: []string{typ}, Statuses: []string{"publish", "draft", "pending", "private", "future"},
			OrderBy: "modified", Order: "DESC", PerPage: webbuilder.NavigatorMaxPosts}
		if !u.Can("edit_others_"+typ+"s", nil) {
			if !u.Can("edit_"+typ+"s", nil) {
				continue
			}
			q.AuthorID = u.ID
		}
		posts, _, err := s.store.QueryPosts(c, q)
		if err != nil {
			restErr(c, http.StatusInternalServerError, "db_error", "Could not read the posts.")
			return
		}
		label := labels[typ]
		if label == "" {
			label = typ
		}
		for _, p := range posts {
			if !s.wbEditable(u, p) {
				continue
			}
			out.Posts = append(out.Posts, webbuilder.NavigatorPost{ID: p.ID, Title: p.Title, PostType: p.Type, TypeLabel: label,
				Status: p.Status, EditURL: lb.HomeURL(wbEditURL(p.ID))})
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}
