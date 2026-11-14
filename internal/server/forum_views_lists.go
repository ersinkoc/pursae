package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/wp"
)

// v0.2.5 views and view helpers: every topic (/forums/topics/), a tag's topics (/forums/tag/<slug>/),
// the tag list (/forums/tags/), the unsubscribe page, and the extras the index, forum, topic and
// new-topic views gain (sort tabs, popular tags, likes, solution, similar topics, drafts).

// forumCtxTopic is the gin key of the topic a page shows (cacheTags adds its forum-topic:<id> tag).
const forumCtxTopic = "forum.front.topic"

// forumTopicCacheTags are the extra cache tags of a forum page (the topic's own tag on topic pages).
func forumTopicCacheTags(c *gin.Context) []string {
	if c == nil {
		return nil
	}
	if v, ok := c.Get(forumCtxTopic); ok {
		if id, _ := v.(int64); id > 0 {
			return []string{forumTopicCacheTag(id)}
		}
	}
	return nil
}

// forumListExtras fills the sort tabs, the tag filter and the popular tags of a topic list at base.
func (s *Server) forumListExtras(c *gin.Context, p *forumPage, base, sort, tag string, withSolved bool) {
	ctx := c.Request.Context()
	p.Sort = sort
	p.Sorts = p.forumSortTabs(base, sort, tag, withSolved)
	p.Extra["untag"] = forumListURL(base, sort, "")
	if tag != "" {
		if tg, err := s.forumTagBySlug(ctx, tag); err == nil {
			p.Tag = tg
		} else {
			p.Tag = &forumTag{Name: tag, Slug: tag, URL: s.forumURL(ctx, "tag", tag)}
		}
	}
	var forumID int64
	if p.Forum != nil && p.Forum.Kind == "forum" {
		forumID = p.Forum.ID
	}
	if tags, err := s.forumTagList(ctx, forumID, 12); err == nil {
		p.Tags = tags
	}
}

// forumTopicExtras fills a topic page's likes, the member's likes and subscription, the accepted
// answer (wherever it is) and similar topics.
func (s *Server) forumTopicExtras(c *gin.Context, p *forumPage, t *forumTopic) {
	ctx := c.Request.Context()
	ids := []int64{t.ID}
	for _, r := range p.Replies {
		ids = append(ids, r.ID)
	}
	if p.User != nil {
		p.Liked = s.forumLikedBy(ctx, p.User.ID, ids)
		p.Subscribed = s.forumSubscribed(ctx, p.User.ID, t.ID)
	}
	if t.SolutionID > 0 {
		if r, err := s.forumReplyByID(ctx, t.SolutionID); err == nil && r.Status == "publish" && !r.Spam && r.TopicID == t.ID {
			r.Solution = true
			p.Solution = r
		}
	}
	slugs := make([]string, 0, len(t.Tags))
	for _, tg := range t.Tags {
		slugs = append(slugs, tg.Slug)
	}
	if sim, err := s.forumSimilarTopics(ctx, t.Title, slugs, t.ID, 5); err == nil {
		p.Similar = sim
	}
	if list, err := s.forumList(ctx); err == nil {
		p.Forums = list
	}
	p.Extra["can_solve"] = forumCanSolve(currentUser(c), s.forumLoad(), t)
}

// forumNewTopicExtras fills the new-topic form from the member's draft (when nothing was typed yet)
// and lists topics similar to the title.
func (s *Server) forumNewTopicExtras(c *gin.Context, p *forumPage) {
	ctx := c.Request.Context()
	u := currentUser(c)
	if d := forumDraftOf(u); d != nil {
		p.Draft = d
		if _, typed := p.Form["title"]; !typed {
			p.Form["title"], p.Form["content"], p.Form["tags"] = d.Title, d.Content, d.Tags
			if d.Notify {
				p.Form["notify"] = "1"
			}
			if p.Forum == nil && d.ForumID > 0 {
				p.Form["forum"] = strconv.FormatInt(d.ForumID, 10)
			}
		}
	}
	if _, typed := p.Form["notify"]; !typed && p.Draft == nil {
		p.Form["notify"] = "1" // following your own topic is the default
	}
	if title := p.Form["title"]; title != "" {
		_, slugs := forumParseTags(p.Form["tags"])
		if sim, err := s.forumSimilarTopics(ctx, title, slugs, 0, 5); err == nil {
			p.Similar = sim
		}
	}
}

// Topic lists -------------------------------------------------------------------------------------

// forumViewTopicList is /forums/topics/ (every topic) and /forums/tag/<slug>/ (a tag's topics).
func (s *Server) forumViewTopicList(c *gin.Context, rt forumRoute, p *forumPage) bool {
	ctx := c.Request.Context()
	st := s.forumLoad()
	canonical := s.forumURL(ctx, "topics")
	var tagged *forumTag
	if rt.View == forumViewTag {
		tg, err := s.forumTagBySlug(ctx, rt.Slug)
		if err != nil {
			return false
		}
		tagged = tg
		canonical = tg.URL
	}
	if forumCanonicalPage301(c, rt, canonical) {
		return true
	}
	sort, tag, redirect, ok := s.forumListQuery(c, canonical)
	if tagged != nil {
		if c.Query("tag") != "" { // the tag is in the path
			ok, redirect = false, forumListURL(canonical, sort, "")
		}
		tag = tagged.Slug
	}
	if !ok {
		target := redirect
		if rt.Page > 1 {
			q := ""
			if i := strings.IndexByte(redirect, '?'); i >= 0 {
				q = redirect[i:]
			}
			target = strings.TrimSuffix(canonical, "/") + "/page/" + strconv.Itoa(rt.Page) + "/" + q
		}
		c.Redirect(http.StatusMovedPermanently, forumLocalPath(target))
		return true
	}
	view := forumViewTopics
	title := "All topics"
	if tagged != nil {
		view = forumViewTag
	}
	p = s.forumPageFor(c, p, view, "")
	p.Title = p.T(title)
	if tagged != nil {
		p.Title = p.Tf("Topics tagged “%s”", tagged.Name)
		p.Crumbs = append(p.Crumbs, forumCrumb{Label: p.T("Tags"), URL: s.forumURL(ctx, "tags")})
	}
	p.Crumbs = append(p.Crumbs, forumCrumb{Label: p.Title})
	s.forumMarkSeen(c, 0)
	topics, total, err := s.forumTopics(ctx, forumTopicQuery{Page: rt.Page, PerPage: st.TopicsPerPage, Sort: sort, Tag: tag})
	if err != nil {
		return false
	}
	pages := forumPages(total, st.TopicsPerPage)
	if rt.Page > pages {
		return false
	}
	p.Topics = topics
	p.Extra["total"] = total
	listTag := tag
	if tagged != nil {
		listTag = ""
	}
	p.Pagination = s.forumPagination(c, forumListURL(canonical, sort, listTag), rt.Page, pages)
	s.forumListExtras(c, p, canonical, sort, listTag, true)
	if tagged != nil {
		p.Tag = tagged
	}
	if list, err := s.forumList(ctx); err == nil {
		p.Forums = list
	}
	if p.User != nil && p.CanPost {
		p.URLs.NewTopic = p.URLs.NewAny
	}
	forumSetSEO(c, p.Title, "")
	return s.forumFinish(c, p, canonical, rt.Page)
}

// forumViewTagList is /forums/tags/: every tag in use with its topic count.
func (s *Server) forumViewTagList(c *gin.Context, rt forumRoute, p *forumPage) bool {
	ctx := c.Request.Context()
	if c.Request.URL.RawQuery != "" {
		c.Redirect(http.StatusMovedPermanently, forumLocalPath(s.forumURL(ctx, "tags")))
		return true
	}
	p = s.forumPageFor(c, p, forumViewTags, "")
	p.Title = p.T("Tags")
	p.Crumbs = append(p.Crumbs, forumCrumb{Label: p.Title})
	tags, err := s.forumTagList(ctx, 0, 200)
	if err != nil {
		return false
	}
	p.Tags = tags
	_ = rt
	return s.forumFinish(c, p, s.forumURL(ctx, "tags"), 1)
}

// forumViewUnsub is the unsubscribe page of a reply email: GET shows a button (mail scanners open
// links; only a POST unsubscribes), which posts forum_action=unsubscribe_link with the signed key.
func (s *Server) forumViewUnsub(c *gin.Context, p *forumPage) bool {
	ctx := c.Request.Context()
	tid, _ := strconv.ParseInt(c.Query("t"), 10, 64)
	uid, _ := strconv.ParseInt(c.Query("u"), 10, 64)
	key := c.Query("k")
	c.Header("Referrer-Policy", "no-referrer")
	p = s.forumPageFor(c, p, forumViewUnsubscribe, "")
	p.Title = p.T("Stop following a topic")
	p.Crumbs = append(p.Crumbs, forumCrumb{Label: p.Title})
	if !s.forumCheckUnsub(tid, uid, key) {
		p.Extra["failed"] = true
		if p.Error == "" {
			p.Error = p.T("This link is invalid.")
		}
		p.Extra["status"] = http.StatusNotFound
		return s.forumFinish(c, p, "", 1)
	}
	if t, err := s.forumTopicByID(ctx, tid); err == nil {
		p.Topic = t
	}
	p.Extra["t"], p.Extra["u"], p.Extra["k"] = tid, uid, key
	p.Extra["action"] = s.forumURL(ctx, "unsubscribe")
	p.Extra["done"] = !s.forumSubscribed(ctx, uid, tid)
	return s.forumFinish(c, p, "", 1)
}

// Post view additions -------------------------------------------------------------------------------

// forumPostEngage fills the v0.2.5 fields of a post view: likes, the viewer's like, the solution
// mark and what the viewer may do.
func (p *forumPage) forumPostEngage(v *forumPostView, likes int, solution bool) {
	v.Likes = likes
	v.Liked = p.Liked[v.ID]
	v.Solution = solution
	if p.Topic != nil {
		v.Solved = p.Topic.Solved
	}
	canSolve, _ := p.Extra["can_solve"].(bool)
	v.CanSolve = canSolve && !v.Lead && v.Status == "publish" && !v.Spam
	mine := p.User != nil && v.Author != nil && v.Author.ID == p.User.ID
	v.CanLike = p.User != nil && p.CanPost && p.Nonce != "" && !mine && v.Status == "publish" && !v.Spam
	v.CanReport = p.User != nil && p.CanPost && p.Nonce != "" && !mine && v.Status == "publish" && !v.Spam
}

// ReportReasons are the report form's reasons (translated).
func (p *forumPage) ReportReasons() []forumSortLink {
	out := make([]forumSortLink, 0, len(forumReportReasonKeys))
	for _, k := range forumReportReasonKeys {
		out = append(out, forumSortLink{Key: k, Label: p.T(forumReportReasons[k])})
	}
	return out
}

// Compact is a count for a narrow column: 950, 3.1K, 12K, 1.2M (the K and M in the viewer's language).
func (p *forumPage) Compact(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 10000:
		return p.Tf("%sK", forumOneDecimal(p, n, 1000))
	case n < 1000000:
		return p.Tf("%sK", strconv.Itoa(n/1000))
	case n < 10000000:
		return p.Tf("%sM", forumOneDecimal(p, n, 1000000))
	}
	return p.Tf("%sM", strconv.Itoa(n/1000000))
}

// forumOneDecimal is n/unit with one decimal (dropped when .0), the decimal mark of the viewer's language.
func forumOneDecimal(p *forumPage, n, unit int) string {
	whole, tenth := n/unit, (n%unit)*10/unit
	if tenth == 0 {
		return strconv.Itoa(whole)
	}
	mark := p.T("number_format_decimal_point") // WordPress's msgid: "," in Turkish
	if mark == "" || len([]rune(mark)) > 1 {
		mark = "."
	}
	return strconv.Itoa(whole) + mark + strconv.Itoa(tenth)
}

// Levels are every level, lowest first, names translated (the levels table of a sidebar).
func (p *forumPage) Levels() []forumLevel {
	if p == nil || p.levels == nil {
		return nil
	}
	list := p.levels()
	out := make([]forumLevel, len(list))
	for i, l := range list {
		l.Name = p.tx(l.Name)
		out[i] = l
	}
	return out
}

// Badges are the enabled badges, names and descriptions translated (a "badges" box).
func (p *forumPage) Badges() []forumBadge {
	if p == nil || p.badges == nil {
		return nil
	}
	var out []forumBadge
	for _, b := range p.badges() {
		if !b.Enabled {
			continue
		}
		b.Name, b.Description = p.tx(b.Name), p.tx(b.Description)
		out = append(out, b)
	}
	return out
}

// Points are the points a topic, reply, accepted answer and like earn ("+5 points for a reply").
func (p *forumPage) Points() forumLevels {
	if p == nil || p.points == nil {
		return forumDefaultLevels()
	}
	return p.points()
}

// LeadAuthor is the author of the topic being viewed, statistics, level and progress loaded.
func (p *forumPage) LeadAuthor() *forumAuthor {
	if p == nil || p.Topic == nil {
		return nil
	}
	return p.Topic.Author
}

// forumPageData wires the lazy level, badge and points loaders of a page.
func (s *Server) forumPageData(ctx context.Context, p *forumPage) {
	var lv *forumLevels
	load := func() forumLevels {
		if lv == nil {
			l := s.forumLevelsLoad(ctx)
			lv = &l
		}
		return *lv
	}
	p.levels = func() []forumLevel { return load().Levels }
	p.points = load
	p.badges = func() []forumBadge { return s.forumBadges(ctx) }
	p.dates = func(t time.Time, withTime bool) string {
		names := s.gooFrontCatalog(ctx).DateNames()
		f := s.settings.String("date_format")
		if f == "" {
			f = "F j, Y"
		}
		out := wp.FormatDateNames(t.In(s.loc()), f, names)
		if withTime {
			tf := s.settings.String("time_format")
			if tf == "" {
				tf = "g:i a"
			}
			out += " " + wp.FormatDateNames(t.In(s.loc()), tf, names)
		}
		return out
	}
}

// Date is t in the site's date format with month and day names in the viewer's language
// (date_i18n()); "" for a zero time.
func (p *forumPage) Date(t time.Time) string {
	if p == nil || p.dates == nil || t.IsZero() {
		return ""
	}
	return p.dates(t, false)
}

// DateTime is Date followed by the time in the site's time format.
func (p *forumPage) DateTime(t time.Time) string {
	if p == nil || p.dates == nil || t.IsZero() {
		return ""
	}
	return p.dates(t, true)
}
