package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Pursae Forum REST (pursae/v1/forum): what the Forums admin screens do, for scripts and Pursae MCP.
//
//	GET    forum/forums              forums and categories (manage_options)
//	POST   forum/forums              create one {title, kind: forum|category, parent, slug, description, closed, order}
//	POST   forum/forums/{id}         update one (fields as above; omitted ones keep their value)
//	DELETE forum/forums/{id}         delete one (its topics go with it, like the admin screen)
//	POST   forum/topics              start a topic as the caller {forum_id, title, content} (forum_participate)
//	…      forum/templates[/{view}]  the site's own view templates (forum_site_templates.go)
//
// The settings are the option group pursae/v1/options/forum (rest_pursae_options_more.go).
// Every route answers 409 forum_off while the plugin is off, naming the slug to activate.

func (s *Server) registerForumManageREST() {
	api := s.rest
	need := func(cp string, h restHandler) restHandler {
		return func(c *gin.Context, r *restRequest) {
			u := restCurrent(c)
			if u == nil {
				restErr(c, http.StatusUnauthorized, "rest_not_logged_in", "You are not currently logged in.")
				return
			}
			if !s.forumOn() {
				restErr(c, http.StatusConflict, "forum_off", "Pursae Forum is turned off. Activate the plugin "+forumSlug+" first.")
				return
			}
			if !u.Can(cp, nil) {
				restErr(c, http.StatusForbidden, "rest_forbidden", "Sorry, you are not allowed to do that.")
				return
			}
			h(c, r)
		}
	}
	api.register(pursaeNS, "/pursae/v1/forum/forums", map[string]restHandler{
		http.MethodGet:  need("manage_options", s.restForumList),
		http.MethodPost: need("manage_options", s.restForumSave),
	}, nil)
	api.register(pursaeNS, `/pursae/v1/forum/forums/(?P<id>[\d]+)`, map[string]restHandler{
		http.MethodPost:   need("manage_options", s.restForumSave),
		http.MethodPut:    need("manage_options", s.restForumSave),
		http.MethodDelete: need("manage_options", s.restForumDelete),
	}, nil)
	api.register(pursaeNS, "/pursae/v1/forum/topics", map[string]restHandler{http.MethodPost: need(capForumParticipate, s.restForumTopic)}, nil)
	s.registerForumTemplatesREST(need) // forum_site_templates.go
}

func (s *Server) restForumJSON(c *gin.Context, f *forumForum) map[string]any {
	return map[string]any{"id": f.ID, "parent": f.ParentID, "slug": f.Slug, "title": f.Title, "description": string(f.Description),
		"kind": f.Kind, "closed": f.Closed, "order": f.Order, "topics": f.TopicCount, "replies": f.ReplyCount, "url": s.forumForumURL(c, f)}
}

func (s *Server) restForumList(c *gin.Context, _ *restRequest) {
	list, err := s.forumList(c)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not read the forums.")
		return
	}
	out := []map[string]any{}
	var walk func([]*forumForum)
	walk = func(l []*forumForum) {
		for _, f := range l {
			out = append(out, s.restForumJSON(c, f))
			walk(f.Children)
		}
	}
	walk(list)
	c.JSON(http.StatusOK, map[string]any{"forums": out, "index": s.forumURL(c)})
}

func (s *Server) restForumSave(c *gin.Context, r *restRequest) {
	in := forumForumInput{ID: r.ID(), Kind: "forum"}
	if in.ID > 0 {
		cur, err := s.forumByID(c, in.ID)
		if err != nil || cur == nil {
			restErr(c, http.StatusNotFound, "forum_not_found", "Invalid forum ID.")
			return
		}
		in = forumForumInput{ID: cur.ID, ParentID: cur.ParentID, Title: cur.Title, Slug: cur.Slug, Description: string(cur.Description),
			Kind: cur.Kind, Closed: cur.Closed, Order: cur.Order}
	}
	if v, ok := r.Str(c, "title"); ok {
		in.Title = v
	}
	if v, ok := r.Str(c, "slug"); ok {
		in.Slug = v
	}
	if v, ok := r.Str(c, "description"); ok {
		in.Description = v
	}
	if v, ok := r.Str(c, "kind"); ok && v != "" {
		if v != "forum" && v != "category" {
			restErr(c, http.StatusBadRequest, "rest_invalid_param", "kind must be forum or category.")
			return
		}
		in.Kind = v
	}
	if v, ok := r.Int(c, "parent"); ok {
		in.ParentID = v
	}
	if v, ok := r.Bool(c, "closed"); ok {
		in.Closed = v
	}
	if v, ok := r.Int(c, "order"); ok {
		in.Order = int(v)
	}
	id, err := s.forumSaveForum(c, in, restCurrent(c).ID)
	if err != nil {
		restErr(c, http.StatusBadRequest, "forum_invalid", strings.TrimPrefix(err.Error(), "forum: "))
		return
	}
	s.purgeAll(c)
	f, err := s.forumByID(c, id)
	if err != nil || f == nil {
		c.JSON(http.StatusOK, map[string]any{"id": id})
		return
	}
	status := http.StatusOK
	if r.ID() == 0 {
		status = http.StatusCreated
	}
	c.JSON(status, s.restForumJSON(c, f))
}

func (s *Server) restForumDelete(c *gin.Context, r *restRequest) {
	if f, err := s.forumByID(c, r.ID()); err != nil || f == nil {
		restErr(c, http.StatusNotFound, "forum_not_found", "Invalid forum ID.")
		return
	}
	if err := s.forumDeleteForum(c, r.ID()); err != nil {
		restErr(c, http.StatusBadRequest, "forum_invalid", strings.TrimPrefix(err.Error(), "forum: "))
		return
	}
	s.purgeAll(c)
	c.JSON(http.StatusOK, map[string]any{"deleted": true, "id": r.ID()})
}

func (s *Server) restForumTopic(c *gin.Context, r *restRequest) {
	fid, _ := r.Int(c, "forum_id")
	title, _ := r.Str(c, "title")
	content, _ := r.Str(c, "content")
	f, err := s.forumByID(c, fid)
	if err != nil || f == nil || f.Kind != "forum" {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "forum_id must be a forum (not a category).")
		return
	}
	if strings.TrimSpace(title) == "" || strings.TrimSpace(content) == "" {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "title and content are required.")
		return
	}
	u := restCurrent(c)
	t, err := s.forumCreateTopic(c, forumNewTopic{ForumID: f.ID, AuthorID: u.ID, Title: title, Content: content, IP: c.ClientIP(), At: time.Now()})
	if err != nil {
		restErr(c, http.StatusBadRequest, "forum_invalid", strings.TrimPrefix(err.Error(), "forum: "))
		return
	}
	s.purgeAll(c)
	c.JSON(http.StatusCreated, map[string]any{"id": t.ID, "title": t.Title, "forum_id": f.ID, "url": s.forumTopicURL(c, t, 1)})
}
