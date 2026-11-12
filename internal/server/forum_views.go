package server

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// The forum front end (WP1): index, forum, topic, reply redirect, new topic, edit, search and
// profile. Every view renders as a synthetic page of the active theme (forumRender). Pages a
// visitor sees are cacheable and carry no forms or nonces; a logged-in member's page carries the
// forms (reply, mod bar, block) and is sent no-store, so no cached copy ever holds a stale nonce or
// time token. The POST handlers live in forum_post.go.

// forumRouteFront serves a GET forum view; false lets the intercept answer 404.
func (s *Server) forumRouteFront(c *gin.Context, rt forumRoute) bool {
	return s.forumServe(c, rt, nil)
}

// forumServe renders view rt; p is a prepared page (a form re-rendered after a failed POST) or nil.
func (s *Server) forumServe(c *gin.Context, rt forumRoute, p *forumPage) bool {
	switch rt.View {
	case forumViewIndex:
		return s.forumViewIndex(c, p)
	case forumViewForum:
		return s.forumViewForum(c, rt, p)
	case forumViewTopic:
		return s.forumViewTopic(c, rt, p)
	case forumViewReply:
		return s.forumViewReply(c, rt)
	case forumViewNewTopic:
		return s.forumViewNewTopic(c, rt, p)
	case forumViewEdit:
		return s.forumViewEdit(c, rt, p)
	case forumViewSearch:
		return s.forumViewSearch(c, rt, p)
	case forumViewProfile:
		return s.forumViewProfile(c, rt, p)
	case forumViewMembers:
		if !s.forumLoad().ShowMembers {
			return false
		}
		return s.forumServeMembers(c, rt, p) // WP7 front end (forum_members_front.go)
	}
	return false
}

// forumPageFor is p (re-titled) or a new page of view.
func (s *Server) forumPageFor(c *gin.Context, p *forumPage, view, title string) *forumPage {
	if p == nil {
		return s.forumNewPage(c, view, title)
	}
	p.View = view
	if title != "" {
		p.Title = title
	}
	return p
}

// forumFinish sends a member's page no-store (it holds their forms) and renders it.
func (s *Server) forumFinish(c *gin.Context, p *forumPage, canonical string, paged int) bool {
	if p.User != nil {
		forumNoStore(c)
	}
	s.forumRender(c, p, p.View, canonical, paged)
	return true
}

// forumCanonicalPage301 sends /page/1/ to the unpaged URL; true when it redirected.
func forumCanonicalPage301(c *gin.Context, rt forumRoute, unpaged string) bool {
	if rt.Page == 1 && strings.HasSuffix(strings.TrimSuffix(c.Request.URL.Path, "/"), "/page/1") {
		target := forumLocalPath(unpaged)
		if c.Request.URL.RawQuery != "" {
			target += "?" + c.Request.URL.RawQuery
		}
		c.Redirect(http.StatusMovedPermanently, target)
		return true
	}
	return false
}

// forumLoginRedirect sends a visitor to the forum login form, coming back here afterwards.
func (s *Server) forumLoginRedirect(c *gin.Context) {
	back := c.Request.URL.Path
	if safeRedirect(back, "") == "" {
		back = forumLocalPath(s.forumURL(c.Request.Context()))
	}
	c.Redirect(http.StatusFound, s.forumURL(c.Request.Context(), "login")+"?redirect="+url.QueryEscape(back))
}

// forumLocalRef is forumLocalPath keeping the #fragment.
func forumLocalRef(abs string) string {
	frag := ""
	if i := strings.IndexByte(abs, '#'); i >= 0 {
		abs, frag = abs[:i], abs[i:]
	}
	return forumLocalPath(abs) + frag
}

// forumPages is the number of pages of total items, per page each (at least 1).
func forumPages(total, per int) int {
	if per <= 0 {
		per = 20
	}
	if total <= 0 {
		return 1
	}
	return 1 + (total-1)/per
}

// forumTreeFind is forum id in the tree (Children filled).
func forumTreeFind(list []*forumForum, id int64) *forumForum {
	for _, f := range list {
		if f.ID == id {
			return f
		}
		for _, ch := range f.Children {
			if ch.ID == id {
				return ch
			}
		}
	}
	return nil
}

// forumCrumbsTo adds the forum (and its category) to the crumbs; link=false leaves the forum unlinked.
func (s *Server) forumCrumbsTo(c *gin.Context, p *forumPage, f *forumForum, link bool) {
	if f == nil {
		return
	}
	ctx := c.Request.Context()
	if f.ParentID > 0 {
		if parent, err := s.forumByID(ctx, f.ParentID); err == nil {
			p.Crumbs = append(p.Crumbs, forumCrumb{Label: parent.Title, URL: parent.URL})
		}
	}
	cr := forumCrumb{Label: f.Title}
	if link {
		cr.URL = f.URL
	}
	p.Crumbs = append(p.Crumbs, cr)
}

// Index ---------------------------------------------------------------------------------------------

func (s *Server) forumViewIndex(c *gin.Context, p *forumPage) bool {
	ctx := c.Request.Context()
	p = s.forumPageFor(c, p, forumViewIndex, "")
	list, err := s.forumList(ctx)
	if err != nil {
		return false
	}
	p.Forums = list
	if recent, err := s.forumRecentTopics(ctx, 5); err == nil {
		p.Topics = recent
	}
	return s.forumFinish(c, p, s.forumURL(ctx), 1)
}

// Forum -----------------------------------------------------------------------------------------------

func (s *Server) forumViewForum(c *gin.Context, rt forumRoute, p *forumPage) bool {
	ctx := c.Request.Context()
	f, err := s.forumBySlug(ctx, rt.Slug)
	if err != nil {
		return false
	}
	canonical := s.forumForumURL(ctx, f)
	if forumCanonicalPage301(c, rt, canonical) {
		return true
	}
	p = s.forumPageFor(c, p, forumViewForum, f.Title)
	p.Forum = f
	s.forumCrumbsTo(c, p, f, false)
	if f.Kind == "category" {
		if rt.Page > 1 {
			return false
		}
		if list, err := s.forumList(ctx); err == nil {
			if node := forumTreeFind(list, f.ID); node != nil {
				f.Children = node.Children
				f.TopicCount, f.ReplyCount = node.TopicCount, node.ReplyCount
			}
		}
		return s.forumFinish(c, p, canonical, 1)
	}
	st := s.forumLoad()
	q := forumTopicQuery{ForumID: f.ID, Page: rt.Page, PerPage: st.TopicsPerPage, StickyFirst: true}
	if p.CanModerate {
		q.Statuses = []string{"publish", "pending"}
		q.WithIP = true
	}
	topics, total, err := s.forumTopics(ctx, q)
	if err != nil {
		return false
	}
	pages := forumPages(total, st.TopicsPerPage)
	if rt.Page > pages {
		return false
	}
	p.Topics = topics
	p.Pagination = s.forumPagination(c, canonical, rt.Page, pages)
	if p.User != nil && (!f.Closed || p.CanModerate) {
		p.URLs.NewTopic = s.forumURL(ctx, "f", f.Slug, "new")
	}
	return s.forumFinish(c, p, canonical, rt.Page)
}

// Topic -----------------------------------------------------------------------------------------------

// forumTopicVisible reports whether u may see topic t: published (in a published forum) for
// everyone; pending for its author; anything for moderators.
func forumTopicVisible(u *store.User, t *forumTopic, forumOK bool) bool {
	if u.Can(capForumModerate, nil) {
		return true
	}
	if !forumOK || t.Spam {
		return false
	}
	switch t.Status {
	case "publish":
		return true
	case "pending":
		return u != nil && u.ID == t.AuthorID && t.AuthorID != 0
	}
	return false
}

func (s *Server) forumViewTopic(c *gin.Context, rt forumRoute, p *forumPage) bool {
	ctx := c.Request.Context()
	t, err := s.forumTopicByID(ctx, rt.ID)
	if err != nil {
		return false
	}
	u := currentUser(c)
	f, ferr := s.forumByID(ctx, t.ForumID)
	if !forumTopicVisible(u, t, ferr == nil) {
		return false
	}
	// The canonical slug (and no /page/1/).
	if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
		if rt.Slug != t.Slug || forumCanonicalPage301(c, rt, t.URL) {
			if !c.Writer.Written() {
				target := forumLocalPath(s.forumTopicURL(ctx, t, rt.Page))
				if c.Request.URL.RawQuery != "" {
					target += "?" + c.Request.URL.RawQuery
				}
				c.Redirect(http.StatusMovedPermanently, target)
			}
			return true
		}
	}
	p = s.forumPageFor(c, p, forumViewTopic, t.Title)
	mod := p.CanModerate
	st := s.forumLoad()
	if mod {
		t.IP = s.forumIPOf(ctx, t.ID)
	}
	p.Topic, p.Forum = t, f
	if f != nil {
		s.forumCrumbsTo(c, p, f, true)
	}
	p.Crumbs = append(p.Crumbs, forumCrumb{Label: t.Title})
	q := forumReplyQuery{TopicID: t.ID, Page: rt.Page, PerPage: st.RepliesPerPage}
	if mod {
		q.Statuses = []string{"publish", "pending"}
		q.WithIP = true
	}
	replies, total, err := s.forumReplies(ctx, q)
	if err != nil {
		return false
	}
	pages := forumPages(total, st.RepliesPerPage)
	if rt.Page > pages {
		return false
	}
	p.Replies = replies
	p.Pagination = s.forumPagination(c, t.URL, rt.Page, pages)
	p.Extra["page"] = rt.Page
	p.Extra["page_url"] = s.forumTopicURL(ctx, t, rt.Page)
	p.Extra["edit_base"] = s.forumURL(ctx, "edit")
	canEdit := map[int64]bool{}
	unverified := u != nil && forumMetaBool(u.Meta, forumUMetaUnverified)
	canEdit[t.ID] = forumCanEdit(u, st, t.AuthorID, t.CreatedAt, t.Status, unverified)
	for _, r := range replies {
		canEdit[r.ID] = forumCanEdit(u, st, r.AuthorID, r.CreatedAt, r.Status, unverified)
	}
	p.Extra["can_edit"] = canEdit
	// The reply box: members who may post, on an open published topic (moderators may always reply).
	open := !t.Closed && (f == nil || !f.Closed)
	p.Extra["open"] = open
	p.Extra["can_reply"] = p.User != nil && p.CanPost && t.Status == "publish" && !t.Spam && (open || mod)
	return s.forumFinish(c, p, t.URL, rt.Page)
}

// forumViewReply sends a reply's short URL to its topic page and anchor.
func (s *Server) forumViewReply(c *gin.Context, rt forumRoute) bool {
	ctx := c.Request.Context()
	r, err := s.forumReplyByID(ctx, rt.ID)
	if err != nil {
		return false
	}
	t, err := s.forumTopicByID(ctx, r.TopicID)
	if err != nil {
		return false
	}
	u := currentUser(c)
	_, ferr := s.forumByID(ctx, t.ForumID)
	if !forumTopicVisible(u, t, ferr == nil) {
		return false
	}
	if (r.Status != "publish" || r.Spam) && !u.Can(capForumModerate, nil) {
		return false
	}
	c.Redirect(http.StatusFound, forumLocalRef(s.forumReplyAnchor(ctx, t, r, u)))
	return true
}

// New topic -----------------------------------------------------------------------------------------

func (s *Server) forumViewNewTopic(c *gin.Context, rt forumRoute, p *forumPage) bool {
	ctx := c.Request.Context()
	if currentUser(c) == nil {
		s.forumLoginRedirect(c)
		return true
	}
	f, err := s.forumBySlug(ctx, rt.Slug)
	if err != nil || f.Kind != "forum" {
		return false
	}
	p = s.forumPageFor(c, p, forumViewNewTopic, "")
	p.Title = p.T("New topic")
	p.Forum = f
	s.forumCrumbsTo(c, p, f, true)
	p.Crumbs = append(p.Crumbs, forumCrumb{Label: p.T("New topic")})
	if code, _ := forumPostGate(currentUser(c), s.forumLoad()); code != 0 {
		p.Error = "" // the join card under the head says why (blocked or unconfirmed), once
	} else if f.Closed && !p.CanModerate {
		p.Error = p.T("This forum is closed to new topics.")
		p.Extra["no_form"] = true
	}
	p.Extra["action"] = s.forumURL(ctx, "f", f.Slug, "new")
	return s.forumFinish(c, p, "", 1)
}

// Edit ------------------------------------------------------------------------------------------------

func (s *Server) forumViewEdit(c *gin.Context, rt forumRoute, p *forumPage) bool {
	ctx := c.Request.Context()
	u := currentUser(c)
	if u == nil {
		s.forumLoginRedirect(c)
		return true
	}
	t, r, err := s.forumEditTarget(ctx, rt.ID)
	if err != nil {
		return false
	}
	f, ferr := s.forumByID(ctx, t.ForumID)
	if !forumTopicVisible(u, t, ferr == nil) {
		return false
	}
	st := s.forumLoad()
	author, created, status, content := t.AuthorID, t.CreatedAt, t.Status, t.Content
	if r != nil {
		author, created, status, content = r.AuthorID, r.CreatedAt, r.Status, r.Content
	}
	allowed := forumCanEdit(u, st, author, created, status, forumMetaBool(u.Meta, forumUMetaUnverified))
	if !allowed && p == nil {
		return false // not theirs, or the edit window is over: as if there were no such page
	}
	title := "Edit reply"
	if r == nil {
		title = "Edit topic"
	}
	p = s.forumPageFor(c, p, forumViewEdit, "")
	p.Title = p.T(title)
	p.Topic, p.Forum = t, f
	if f != nil {
		s.forumCrumbsTo(c, p, f, true)
	}
	p.Crumbs = append(p.Crumbs, forumCrumb{Label: t.Title, URL: t.URL}, forumCrumb{Label: p.T(title)})
	back := t.URL
	if r != nil {
		back = s.forumReplyAnchor(ctx, t, r, u)
	}
	p.Extra["back"] = back
	if !allowed { // a refused POST: the reason and a way back, no form
		return s.forumFinish(c, p, "", 1)
	}
	p.Edit = &forumEdit{ID: rt.ID, IsTopic: r == nil, Title: t.Title, Content: content}
	if v, ok := p.Form["title"]; ok && r == nil {
		p.Edit.Title = v
	}
	if v, ok := p.Form["content"]; ok {
		p.Edit.Content = v
	}
	p.Extra["action"] = s.forumURL(ctx, "edit", strconv.FormatInt(rt.ID, 10))
	if st.EditWindow > 0 && !p.CanModerate {
		p.Extra["window"] = st.EditWindow
	}
	return s.forumFinish(c, p, "", 1)
}

// Search ----------------------------------------------------------------------------------------------

func (s *Server) forumViewSearch(c *gin.Context, rt forumRoute, p *forumPage) bool {
	ctx := c.Request.Context()
	q := strings.TrimSpace(c.Query("q"))
	if utf8.RuneCountInString(q) > forumSearchMaxLen {
		q = string([]rune(q)[:forumSearchMaxLen])
	}
	title := "Search the forums"
	p = s.forumPageFor(c, p, forumViewSearch, "")
	p.Title = p.T(title)
	p.Query = q
	p.Crumbs = append(p.Crumbs, forumCrumb{Label: p.T("Search")})
	var hits []*forumHit
	total := 0
	if q != "" {
		st := s.forumLoad()
		var err error
		hits, total, err = s.forumActivity(ctx, q, 0, rt.Page, st.TopicsPerPage)
		if err != nil {
			return false
		}
		pages := forumPages(total, st.TopicsPerPage)
		if rt.Page > pages {
			return false
		}
		p.Pagination = s.forumPagination(c, s.forumURL(ctx, "search")+"?q="+url.QueryEscape(q), rt.Page, pages)
		p.Title = p.Tf("Search results for “%s”", q)
	} else if rt.Page > 1 {
		return false
	}
	p.Extra["hits"] = hits
	p.Extra["total"] = total
	return s.forumFinish(c, p, "", rt.Page)
}

// Profile ---------------------------------------------------------------------------------------------

func (s *Server) forumViewProfile(c *gin.Context, rt forumRoute, p *forumPage) bool {
	ctx := c.Request.Context()
	found, err := s.store.UserByNicename(ctx, rt.Slug)
	if err != nil {
		return false
	}
	authors, err := s.forumAuthors(ctx, []int64{found.ID})
	if err != nil || authors[found.ID] == nil {
		return false
	}
	a := authors[found.ID]
	canonical := s.forumProfileURL(ctx, found.Nicename)
	if forumCanonicalPage301(c, rt, canonical) {
		return true
	}
	p = s.forumPageFor(c, p, forumViewProfile, a.DisplayName)
	p.Profile = a
	if s.forumLoad().ShowMembers {
		p.Crumbs = append(p.Crumbs, forumCrumb{Label: p.T("Members"), URL: s.forumURL(ctx, "members")})
	}
	p.Crumbs = append(p.Crumbs, forumCrumb{Label: a.DisplayName})
	stats, err := s.forumUserStats(ctx, found.ID)
	if err != nil {
		return false
	}
	p.ProfileStats = stats
	// Visitors may not probe accounts (the core blocks user enumeration by default): a profile is a
	// 404 until the member has posted, except for the member and the moderators.
	if stats.Topics+stats.Replies == 0 && s.blockEnumeration() && !p.CanModerate {
		if u := currentUser(c); u == nil || u.ID != found.ID {
			return false
		}
	}
	st := s.forumLoad()
	hits, total, err := s.forumActivity(ctx, "", found.ID, rt.Page, st.TopicsPerPage)
	if err != nil {
		return false
	}
	pages := forumPages(total, st.TopicsPerPage)
	if rt.Page > pages {
		return false
	}
	p.Pagination = s.forumPagination(c, canonical, rt.Page, pages)
	p.Extra["hits"] = hits
	p.Extra["total"] = total
	p.Extra["bio"] = strings.TrimSpace(found.Profile["description"])
	if u := currentUser(c); u != nil && p.CanModerate {
		if target, err := s.store.UserByID(ctx, found.ID); err == nil && forumCanBlock(u, target) {
			p.Extra["can_block"] = true
			p.Extra["action"] = canonical
		}
	}
	if u := currentUser(c); u != nil && p.Nonce != "" && u.Can("manage_options", nil) {
		s.forumProfileBadgeAdmin(ctx, p, a)
		p.Extra["action"] = canonical
	}
	return s.forumFinish(c, p, canonical, rt.Page)
}

// forumBadgeAward is a badge an administrator may award on a profile, or revoke (Manual: it is a
// manual award of the member).
type forumBadgeAward struct {
	Badge  forumBadge
	Name   string // translated
	Manual bool
}

// forumProfileBadgeAdmin fills the profile's badge form for an administrator: Extra["award"] lists
// the enabled badges the member does not hold yet, Extra["revoke"] the member's manual awards.
func (s *Server) forumProfileBadgeAdmin(ctx context.Context, p *forumPage, a *forumAuthor) {
	stats, err := s.forumMemberStats(ctx, []int64{a.ID})
	if err != nil {
		return
	}
	manual := map[string]bool{}
	if st := stats[a.ID]; st != nil {
		for _, id := range st.Manual {
			manual[id] = true
		}
	}
	held := map[string]bool{}
	for _, b := range a.Badges {
		held[b.ID] = true
	}
	var award, revoke []forumBadgeAward
	for _, b := range s.forumBadges(ctx) {
		row := forumBadgeAward{Badge: b, Name: p.tx(b.Name), Manual: manual[b.ID]}
		if row.Manual {
			revoke = append(revoke, row)
		} else if b.Enabled && !held[b.ID] {
			award = append(award, row)
		}
	}
	p.Extra["badge_admin"] = true
	p.Extra["award"] = award
	p.Extra["revoke"] = revoke
}
