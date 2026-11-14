package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/cache"
	"wpgo/internal/store"
)

// forum/v1 v0.2.5 routes (registerForumRESTEngage):
//
//	POST   /topics/<id>/view          count a view (the beacon of a cached page): {views, counted}
//	POST   /presence                  mark the visitor online (body forum=<id>): {online, members, forum_online}
//	GET    /online?forum=<id>         the same counts, nothing recorded
//	GET    /stats                     site totals (forums, topics, replies, members, solved %, online …)
//	GET    /tags[?forum=<id>]         tags in use with their topic counts
//	GET    /topics/similar?q=&tags=   topics like a title (the new-topic form)
//	GET    /levels  /badges  /weekly  the levels, enabled badges and members of the week
//	GET    /me[?topic=<id>]           the member's state on a topic: likes, subscription, may solve (no-store)
//	POST   /posts/<id>/like           like (DELETE: unlike): {liked, likes}
//	POST   /posts/<id>/report         report (reason, note)
//	POST   /topics/<id>/subscription  follow by email (DELETE: stop)
//	POST   /topics/<id>/solution      accept reply=<id> (DELETE: withdraw)
//	POST   /preview                   render content as it would be posted: {rendered}
//	GET    /draft  POST /draft  DELETE /draft   the member's new-topic draft
//
// Member routes need cookie + X-WP-Nonce (wp_rest) or an Application Password; they answer
// rest_not_logged_in (401) otherwise and keep the front end's rules (forumPostGate, rate limits).

func (s *Server) registerForumRESTEngage() {
	ns, base := forumRESTNamespace, "/"+forumRESTNamespace
	reg := func(path string, h map[string]restHandler) { s.rest.register(ns, base+path, h, nil) }
	reg(`/topics/(?P<id>[\d]+)/view`, map[string]restHandler{http.MethodPost: s.forumRESTView})
	reg(`/presence`, map[string]restHandler{http.MethodPost: s.forumRESTPresence})
	reg(`/online`, map[string]restHandler{http.MethodGet: s.forumRESTOnline})
	reg(`/stats`, map[string]restHandler{http.MethodGet: s.forumRESTStats})
	reg(`/tags`, map[string]restHandler{http.MethodGet: s.forumRESTTags})
	reg(`/topics/similar`, map[string]restHandler{http.MethodGet: s.forumRESTSimilar})
	reg(`/levels`, map[string]restHandler{http.MethodGet: s.forumRESTLevels})
	reg(`/badges`, map[string]restHandler{http.MethodGet: s.forumRESTBadges})
	reg(`/weekly`, map[string]restHandler{http.MethodGet: s.forumRESTWeekly})
	reg(`/me`, map[string]restHandler{http.MethodGet: s.forumRESTMe})
	reg(`/posts/(?P<id>[\d]+)/like`, map[string]restHandler{http.MethodPost: s.forumRESTLike, http.MethodDelete: s.forumRESTLike})
	reg(`/posts/(?P<id>[\d]+)/report`, map[string]restHandler{http.MethodPost: s.forumRESTReport})
	reg(`/topics/(?P<id>[\d]+)/subscription`, map[string]restHandler{http.MethodPost: s.forumRESTSubscription, http.MethodDelete: s.forumRESTSubscription})
	reg(`/topics/(?P<id>[\d]+)/solution`, map[string]restHandler{http.MethodPost: s.forumRESTSolution, http.MethodDelete: s.forumRESTSolution})
	reg(`/preview`, map[string]restHandler{http.MethodPost: s.forumRESTPreview})
	reg(`/draft`, map[string]restHandler{http.MethodGet: s.forumRESTDraft, http.MethodPost: s.forumRESTDraft, http.MethodDelete: s.forumRESTDraft})
}

// forumRESTPrivate marks a response visitor-specific (never cached, never stored).
func forumRESTPrivate(c *gin.Context) {
	c.Header("Cache-Control", "no-store, private")
	c.Header(cache.HeaderControl, "no-store")
}

// forumRESTMember is the authenticated member of a write route (nil: answered 401).
func (s *Server) forumRESTMember(c *gin.Context) *store.User {
	u := restCurrent(c)
	if u == nil {
		restErr(c, http.StatusUnauthorized, "rest_not_logged_in", "You are not currently logged in.")
		return nil
	}
	return u
}

// forumRESTMethod is the effective method (POST may carry _method / X-HTTP-Method-Override).
func forumRESTMethod(r *restRequest, c *gin.Context) string {
	if r != nil && r.Method != "" {
		return r.Method
	}
	return c.Request.Method
}

// Public ------------------------------------------------------------------------------------------

func (s *Server) forumRESTView(c *gin.Context, r *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	forumRESTPrivate(c)
	views, counted, err := s.forumCountView(c, forumRESTID(r))
	if errors.Is(err, errForumNotFound) {
		restErr(c, http.StatusNotFound, "rest_topic_invalid_id", "Invalid topic ID.")
		return
	}
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_forum_error", "The view could not be counted.")
		return
	}
	// The visitor is on the topic's forum.
	var forumID int64
	_ = s.store.Pool.QueryRow(c, `SELECT coalesce(parent_id,0) FROM posts WHERE id = $1`, forumRESTID(r)).Scan(&forumID)
	s.forumMarkSeen(c, forumID)
	c.JSON(http.StatusOK, map[string]any{"views": views, "counted": counted})
}

func (s *Server) forumRESTOnlineJSON(forumID int64) map[string]any {
	all, members := s.forumOnlineCount(0)
	out := map[string]any{"online": all, "members": members, "minutes": s.forumLoad().OnlineMinutes}
	if forumID > 0 {
		n, _ := s.forumOnlineCount(forumID)
		out["forum_online"] = n
	}
	return out
}

func (s *Server) forumRESTPresence(c *gin.Context, r *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	forumRESTPrivate(c)
	id, _ := r.Int(c, "forum")
	if !forumAllow("presence:"+gooikeTruncateIP(clientIP(c)), 240, 10*time.Minute) {
		c.JSON(http.StatusOK, s.forumRESTOnlineJSON(id))
		return
	}
	if id > 0 {
		if f, err := s.forumByID(c, id); err != nil || f.Kind != "forum" {
			id = 0
		}
	}
	s.forumMarkSeen(c, id)
	c.JSON(http.StatusOK, s.forumRESTOnlineJSON(id))
}

func (s *Server) forumRESTOnline(c *gin.Context, _ *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	forumRESTPrivate(c)
	id, _ := strconv.ParseInt(c.Query("forum"), 10, 64)
	c.JSON(http.StatusOK, s.forumRESTOnlineJSON(id))
}

func (s *Server) forumRESTStats(c *gin.Context, _ *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	st, err := s.forumStatsOf(c)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_forum_error", "The statistics could not be read.")
		return
	}
	forumRESTPrivate(c) // the online count changes by the minute
	c.JSON(http.StatusOK, map[string]any{"forums": st.Forums, "topics": st.Topics, "replies": st.Replies, "members": st.Members,
		"solved": st.Solved, "solved_pct": st.SolvedPct, "new_topics_week": st.NewTopicsWeek, "new_members_week": st.NewMembersWeek,
		"online": st.Online, "online_members": st.OnlineMembers})
}

func (s *Server) forumRESTTags(c *gin.Context, _ *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	fid, _ := strconv.ParseInt(c.Query("forum"), 10, 64)
	tags, err := s.forumTagList(c, fid, 200)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_forum_error", "The tags could not be read.")
		return
	}
	c.JSON(http.StatusOK, forumRESTTags(tags))
}

func (s *Server) forumRESTSimilar(c *gin.Context, _ *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	q := forumTruncRunes(strings.TrimSpace(c.Query("q")), forumTitleMax)
	_, slugs := forumParseTags(c.Query("tags"))
	list, err := s.forumSimilarTopics(c, q, slugs, 0, 5)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_forum_error", "The topics could not be read.")
		return
	}
	out := []map[string]any{}
	for _, t := range list {
		out = append(out, map[string]any{"id": t.ID, "title": t.Title, "link": t.URL, "forum_title": t.ForumTitle,
			"reply_count": t.ReplyCount, "solved": t.Solved})
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) forumRESTLevels(c *gin.Context, _ *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	lv := s.forumLevelsLoad(c)
	out := []map[string]any{}
	for _, l := range lv.Levels {
		out = append(out, map[string]any{"id": l.ID, "name": l.Name, "rank": l.Rank, "min": l.Min, "icon": l.Icon, "color": l.Color, "image": l.ImageURL})
	}
	c.JSON(http.StatusOK, map[string]any{"levels": out, "points": map[string]any{"topic": lv.TopicPoints, "reply": lv.ReplyPoints,
		"solution": lv.SolutionPoints, "like": lv.LikePoints}})
}

func (s *Server) forumRESTBadges(c *gin.Context, _ *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	out := []map[string]any{}
	for _, b := range s.forumBadges(c) {
		if b.Enabled {
			out = append(out, map[string]any{"id": b.ID, "name": b.Name, "description": b.Description, "icon": b.Icon, "color": b.Color,
				"image": b.ImageURL, "rule": b.Rule, "threshold": b.Threshold})
		}
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) forumRESTWeekly(c *gin.Context, _ *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	n, _ := strconv.Atoi(c.Query("per_page"))
	list, err := s.forumWeeklyMembers(c, n)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_forum_error", "The members could not be read.")
		return
	}
	out := []map[string]any{}
	for _, w := range list {
		out = append(out, map[string]any{"member": forumRESTAuthor(w.Author), "delta": w.Delta})
	}
	c.JSON(http.StatusOK, out)
}

// Member ------------------------------------------------------------------------------------------

func (s *Server) forumRESTMe(c *gin.Context, _ *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	forumRESTPrivate(c)
	u := restCurrent(c)
	if u == nil {
		c.JSON(http.StatusOK, map[string]any{"logged_in": false})
		return
	}
	st := s.forumLoad()
	code, _ := forumPostGate(u, st)
	out := map[string]any{"logged_in": true, "id": u.ID, "can_post": code == 0, "moderator": u.Can(capForumModerate, nil)}
	if tid, _ := strconv.ParseInt(c.Query("topic"), 10, 64); tid > 0 {
		if t, err := s.forumTopicByID(c, tid); err == nil {
			if _, ferr := s.forumByID(c, t.ForumID); forumTopicVisible(u, t, ferr == nil) {
				ids := []int64{t.ID}
				rows, err := s.store.Pool.Query(c, `SELECT id FROM posts WHERE type = 'reply' AND parent_id = $1`, t.ID)
				if err == nil {
					for rows.Next() {
						var id int64
						if rows.Scan(&id) == nil {
							ids = append(ids, id)
						}
					}
					rows.Close()
				}
				liked := []int64{}
				for id := range s.forumLikedBy(c, u.ID, ids) {
					liked = append(liked, id)
				}
				out["liked"] = liked
				out["subscribed"] = s.forumSubscribed(c, u.ID, t.ID)
				out["can_solve"] = forumCanSolve(u, st, t)
			}
		}
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) forumRESTLike(c *gin.Context, r *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	forumRESTPrivate(c)
	u := s.forumRESTMember(c)
	if u == nil {
		return
	}
	if code, msg := forumPostGate(u, s.forumLoad()); code != 0 {
		restErr(c, http.StatusForbidden, "rest_forum_cannot_post", msg)
		return
	}
	if !forumAllow("like:"+strconv.FormatInt(u.ID, 10), forumLikeLimit, 10*time.Minute) {
		restErr(c, http.StatusTooManyRequests, "rest_forum_too_many", "Too many requests. Please try again later.")
		return
	}
	liked, n, err := s.forumSetLike(c, u, forumRESTID(r), forumRESTMethod(r, c) != http.MethodDelete)
	switch {
	case errors.Is(err, errForumOwnPost):
		restErr(c, http.StatusForbidden, "rest_forum_own_post", "You cannot like your own post.")
	case errors.Is(err, errForumNotFound):
		restErr(c, http.StatusNotFound, "rest_post_invalid_id", "Invalid post ID.")
	case err != nil:
		restErr(c, http.StatusInternalServerError, "rest_forum_error", "The like could not be saved.")
	default:
		c.JSON(http.StatusOK, map[string]any{"liked": liked, "likes": n})
	}
}

func (s *Server) forumRESTReport(c *gin.Context, r *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	forumRESTPrivate(c)
	u := s.forumRESTMember(c)
	if u == nil {
		return
	}
	if code, msg := forumPostGate(u, s.forumLoad()); code != 0 {
		restErr(c, http.StatusForbidden, "rest_forum_cannot_post", msg)
		return
	}
	if !forumAllow("report:"+strconv.FormatInt(u.ID, 10), 10, time.Hour) {
		restErr(c, http.StatusTooManyRequests, "rest_forum_too_many", "Too many requests. Please try again later.")
		return
	}
	reason, _ := r.Str(c, "reason")
	note, _ := r.Str(c, "note")
	if _, ok := forumReportReasons[reason]; !ok {
		reason = "other"
	}
	if err := s.forumReport(c, u, forumRESTID(r), reason, note); err != nil {
		restErr(c, http.StatusNotFound, "rest_post_invalid_id", "Invalid post ID.")
		return
	}
	c.JSON(http.StatusOK, map[string]any{"reported": true})
}

func (s *Server) forumRESTSubscription(c *gin.Context, r *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	forumRESTPrivate(c)
	u := s.forumRESTMember(c)
	if u == nil {
		return
	}
	t, err := s.forumTopicByID(c, forumRESTID(r))
	if err != nil {
		restErr(c, http.StatusNotFound, "rest_topic_invalid_id", "Invalid topic ID.")
		return
	}
	if _, ferr := s.forumByID(c, t.ForumID); !forumTopicVisible(u, t, ferr == nil) {
		restErr(c, http.StatusNotFound, "rest_topic_invalid_id", "Invalid topic ID.")
		return
	}
	on := forumRESTMethod(r, c) != http.MethodDelete
	if err := s.forumSubscribe(c, u.ID, t.ID, on); err != nil {
		restErr(c, http.StatusInternalServerError, "rest_forum_error", "The subscription could not be saved.")
		return
	}
	c.JSON(http.StatusOK, map[string]any{"subscribed": on})
}

func (s *Server) forumRESTSolution(c *gin.Context, r *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	forumRESTPrivate(c)
	u := s.forumRESTMember(c)
	if u == nil {
		return
	}
	var reply int64
	if forumRESTMethod(r, c) != http.MethodDelete {
		reply, _ = r.Int(c, "reply")
		if reply <= 0 {
			restErr(c, http.StatusBadRequest, "rest_missing_callback_param", "Missing parameter(s): reply")
			return
		}
	}
	err := s.forumSolve(c, u, forumRESTID(r), reply)
	switch {
	case errors.Is(err, errForumSolveDenied):
		restErr(c, http.StatusForbidden, "rest_forum_cannot_solve", "Only the topic's author or a moderator can choose the solution.")
	case err != nil:
		restErr(c, http.StatusNotFound, "rest_topic_invalid_id", "Invalid topic or reply ID.")
	default:
		c.JSON(http.StatusOK, map[string]any{"solved": reply > 0, "solution": forumNullID(reply)})
	}
}

func (s *Server) forumRESTPreview(c *gin.Context, r *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	forumRESTPrivate(c)
	u := s.forumRESTMember(c)
	if u == nil {
		return
	}
	raw, _ := r.Str(c, "content")
	if len(raw) > forumContentMax*4 {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "The text is too long (at most 20000 characters).")
		return
	}
	c.JSON(http.StatusOK, map[string]any{"rendered": string(forumFormat(forumClean(u, raw)))})
}

func (s *Server) forumRESTDraft(c *gin.Context, r *restRequest) {
	if s.forumRESTOff(c) {
		return
	}
	forumRESTPrivate(c)
	u := s.forumRESTMember(c)
	if u == nil {
		return
	}
	draftJSON := func(d *forumDraft) any {
		if d == nil {
			return nil
		}
		return map[string]any{"forum": d.ForumID, "title": d.Title, "content": d.Content, "tags": d.Tags, "notify": d.Notify, "saved": forumRESTTime(d.Saved)}
	}
	switch forumRESTMethod(r, c) {
	case http.MethodGet:
		c.JSON(http.StatusOK, map[string]any{"draft": draftJSON(forumDraftOf(u))})
	case http.MethodDelete:
		if err := s.forumSaveDraft(c, u.ID, nil); err != nil {
			restErr(c, http.StatusInternalServerError, "rest_forum_error", "The draft could not be deleted.")
			return
		}
		c.JSON(http.StatusOK, map[string]any{"draft": nil})
	default:
		if !forumAllow("draft:"+strconv.FormatInt(u.ID, 10), 120, 10*time.Minute) {
			restErr(c, http.StatusTooManyRequests, "rest_forum_too_many", "Too many requests. Please try again later.")
			return
		}
		d := &forumDraft{}
		d.ForumID, _ = r.Int(c, "forum")
		d.Title, _ = r.Str(c, "title")
		d.Content, _ = r.Str(c, "content")
		d.Tags, _ = r.Str(c, "tags")
		d.Notify, _ = r.Bool(c, "notify")
		if err := s.forumSaveDraft(c, u.ID, d); err != nil {
			restErr(c, http.StatusInternalServerError, "rest_forum_error", "The draft could not be saved.")
			return
		}
		c.JSON(http.StatusOK, map[string]any{"draft": draftJSON(d)})
	}
}
