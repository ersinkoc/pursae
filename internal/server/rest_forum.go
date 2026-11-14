package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// forum/v1: the REST API of Pursae Forum. Only published, non-spam items are listed. While the
// plugin is off every route answers rest_no_route, as an unknown route does.
//
//	GET /forum/v1/forums                    the forum tree, flat (categories first, then their forums)
//	GET /forum/v1/forums/<id>/topics        a forum's topics (?sort=&tag=), paged
//	GET /forum/v1/topics/<id>               one topic with its opening post
//	GET /forum/v1/topics/<id>/replies       a topic's replies, oldest first, paged
//
// v0.2.5 (rest_forum_engage.go): the view beacon, presence and online counts, likes, solutions,
// subscriptions, reports, previews, drafts, tags, similar topics, statistics, levels, badges and the
// members of the week. Writes need a logged-in member (cookie + X-WP-Nonce "wp_rest") except the
// view beacon and presence, which take no input but the visitor.

const forumRESTNamespace = "forum/v1"

// registerForumREST registers forum/v1 (hidden while the plugin is off).
func (s *Server) registerForumREST() {
	ns, base := forumRESTNamespace, "/"+forumRESTNamespace
	s.rest.register(ns, base+"/forums", map[string]restHandler{http.MethodGet: s.forumRESTForums}, nil)
	s.rest.register(ns, base+`/forums/(?P<id>[\d]+)/topics`, map[string]restHandler{http.MethodGet: s.forumRESTTopics}, nil)
	s.rest.register(ns, base+`/topics/(?P<id>[\d]+)`, map[string]restHandler{http.MethodGet: s.forumRESTTopic}, nil)
	s.rest.register(ns, base+`/topics/(?P<id>[\d]+)/replies`, map[string]restHandler{http.MethodGet: s.forumRESTReplies}, nil)
	s.registerForumRESTEngage() // v0.2.5
}

// forumRESTHidden reports whether a REST namespace belongs to an inactive Pursae Forum: rest.go skips
// its routes in the dispatcher and in the /wp-json index.
func (s *Server) forumRESTHidden(ns string) bool { return ns == forumRESTNamespace && !s.forumOn() }

// forumRESTOff answers rest_no_route while the plugin is off (defence in depth behind forumRESTHidden).
func (s *Server) forumRESTOff(c *gin.Context) bool {
	if s.forumOn() {
		return false
	}
	restErr(c, http.StatusNotFound, "rest_no_route", "No route was found matching the URL and request method.")
	return true
}

// forumRESTTime is a REST date (RFC 3339, UTC); null when unknown.
func forumRESTTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

// forumRESTAuthor is the public face of a member (no login, no email).
func forumRESTAuthor(a *forumAuthor) any {
	if a == nil {
		return nil
	}
	out := map[string]any{"id": a.ID, "name": a.DisplayName, "avatar": a.AvatarURL, "link": a.ProfileURL, "moderator": a.Moderator,
		"initials": a.Initials, "hue": a.Hue, "has_photo": a.HasPhoto}
	if a.Stats { // v0.2.5: points, level and progress
		out["points"] = a.Points
		out["progress"] = a.Progress
		if a.Level != nil {
			out["level"] = map[string]any{"id": a.Level.ID, "name": a.Level.Name, "rank": a.Level.Rank, "min": a.Level.Min}
		}
	}
	return out
}

func forumRESTForum(f *forumForum) map[string]any {
	out := map[string]any{"id": f.ID, "parent": f.ParentID, "slug": f.Slug, "title": f.Title, "kind": f.Kind,
		"description": string(f.Description), "closed": f.Closed, "order": f.Order, "topic_count": f.TopicCount,
		"reply_count": f.ReplyCount, "last_active": forumRESTTime(f.LastActive), "link": f.URL,
		// v0.2.5 design meta.
		"icon": f.Icon, "color": f.Color, "color_hex": f.ColorHex, "color_ink": f.ColorInk, "label": f.Label, "number": f.Number, "chips": forumRESTTags(f.Chips),
		"tips": forumRESTStrs(f.Tips), "staff_only": f.StaffOnly}
	if f.LastTopicID > 0 && f.LastTopicURL != "" {
		out["last_topic"] = map[string]any{"id": f.LastTopicID, "title": f.LastTopicTitle, "link": f.LastTopicURL}
	} else {
		out["last_topic"] = nil
	}
	return out
}

func forumRESTTopicJSON(t *forumTopic) map[string]any {
	return map[string]any{"id": t.ID, "forum": t.ForumID, "forum_title": t.ForumTitle, "title": t.Title, "slug": t.Slug,
		"content": map[string]any{"rendered": string(forumFormat(t.Content))}, "author": forumRESTAuthor(t.Author),
		"sticky": t.Sticky, "closed": t.Closed, "reply_count": t.ReplyCount, "date": forumRESTTime(t.CreatedAt),
		"last_active": forumRESTTime(t.LastActive), "edited": t.EditedAt != nil, "link": t.URL,
		// v0.2.5.
		"tags": forumRESTTags(t.Tags), "views": t.Views, "likes": t.Likes, "solved": t.Solved, "solution": forumNullID(t.SolutionID),
		"forum_color": t.ForumColor, "forum_color_hex": t.ForumColorHex, "forum_color_ink": t.ForumColorInk, "forum_icon": t.ForumIcon, "forum_label": t.ForumLabel}
}

// forumRESTTags is a tag list as JSON (never null).
func forumRESTTags(list []forumTag) []map[string]any {
	out := []map[string]any{}
	for _, t := range list {
		m := map[string]any{"name": t.Name, "slug": t.Slug, "link": t.URL}
		if t.Count > 0 {
			m["count"] = t.Count
		}
		out = append(out, m)
	}
	return out
}

// forumRESTStrs is a string list as JSON (never null).
func forumRESTStrs(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// forumRESTForums is GET /forum/v1/forums.
func (s *Server) forumRESTForums(c *gin.Context, _ *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	tree, err := s.forumList(c)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_forum_error", "The forums could not be read.")
		return
	}
	out := []map[string]any{}
	for _, f := range tree {
		out = append(out, forumRESTForum(f))
		for _, ch := range f.Children {
			out = append(out, forumRESTForum(ch))
		}
	}
	paginate(c, len(out), max(len(out), 1), 1)
	c.JSON(http.StatusOK, out)
}

// forumRESTID reads the id route parameter.
func forumRESTID(r *restRequest) int64 {
	id, _ := strconv.ParseInt(r.Params["id"], 10, 64)
	return id
}

// forumRESTTopics is GET /forum/v1/forums/<id>/topics.
func (s *Server) forumRESTTopics(c *gin.Context, r *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	f, err := s.forumByID(c, forumRESTID(r))
	if err != nil {
		restErr(c, http.StatusNotFound, "rest_forum_invalid_id", "Invalid forum ID.")
		return
	}
	page, per := pageArgs(c)
	out := []map[string]any{}
	total := 0
	if f.Kind != "category" { // categories hold forums, not topics
		sort, _ := forumSortOf(c.Query("sort"))
		topics, n, err := s.forumTopics(c, forumTopicQuery{ForumID: f.ID, Page: page, PerPage: per, StickyFirst: true, Sort: sort, Tag: forumTagSlug(c.Query("tag"))})
		if err != nil {
			restErr(c, http.StatusInternalServerError, "rest_forum_error", "The topics could not be read.")
			return
		}
		total = n
		for _, t := range topics {
			out = append(out, forumRESTTopicJSON(t))
		}
	}
	if pages := max(1, (total+per-1)/per); page > pages && total > 0 {
		restErr(c, http.StatusBadRequest, "rest_post_invalid_page_number", "The page number requested is larger than the number of pages available.")
		return
	}
	paginate(c, total, per, page)
	c.JSON(http.StatusOK, out)
}

// forumRESTPublicTopic is a published, non-spam topic in a published forum; nil otherwise.
func (s *Server) forumRESTPublicTopic(c *gin.Context, id int64) *forumTopic {
	t, err := s.forumTopicByID(c, id)
	if err != nil || t.Status != "publish" || t.Spam {
		return nil
	}
	if _, err := s.forumByID(c, t.ForumID); err != nil {
		return nil
	}
	return t
}

// forumRESTTopic is GET /forum/v1/topics/<id>.
func (s *Server) forumRESTTopic(c *gin.Context, r *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	t := s.forumRESTPublicTopic(c, forumRESTID(r))
	if t == nil {
		restErr(c, http.StatusNotFound, "rest_topic_invalid_id", "Invalid topic ID.")
		return
	}
	c.JSON(http.StatusOK, forumRESTTopicJSON(t))
}

// forumRESTReplies is GET /forum/v1/topics/<id>/replies.
func (s *Server) forumRESTReplies(c *gin.Context, r *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	t := s.forumRESTPublicTopic(c, forumRESTID(r))
	if t == nil {
		restErr(c, http.StatusNotFound, "rest_topic_invalid_id", "Invalid topic ID.")
		return
	}
	page, per := pageArgs(c)
	replies, total, err := s.forumReplies(c, forumReplyQuery{TopicID: t.ID, Page: page, PerPage: per})
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_forum_error", "The replies could not be read.")
		return
	}
	if pages := max(1, (total+per-1)/per); page > pages && total > 0 {
		restErr(c, http.StatusBadRequest, "rest_post_invalid_page_number", "The page number requested is larger than the number of pages available.")
		return
	}
	out := []map[string]any{}
	for _, rp := range replies {
		out = append(out, map[string]any{"id": rp.ID, "topic": rp.TopicID, "forum": rp.ForumID, "position": rp.Position,
			"content": map[string]any{"rendered": string(forumFormat(rp.Content))}, "author": forumRESTAuthor(rp.Author),
			"date": forumRESTTime(rp.CreatedAt), "edited": rp.EditedAt != nil, "link": rp.URL,
			"likes": rp.Likes, "solution": t.SolutionID > 0 && rp.ID == t.SolutionID}) // v0.2.5
	}
	paginate(c, total, per, page)
	c.JSON(http.StatusOK, out)
}
