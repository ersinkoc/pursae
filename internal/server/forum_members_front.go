package server

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

// The public member list /<base>/members/[page/N/]?q=&sort= (WP7). The route is parsed
// (forumViewMembers) and gated on forumSettings.ShowMembers by forumServe; the rows come from
// forumMembers (forum_gamify.go) without the admin scope, so no email is ever selected and blocked
// members are left out. While the core blocks user enumeration only members who posted are listed
// (the profile rule). A search (?q=) is noindex and no-store like the forum search; the plain pages
// are cacheable (every forum view carries the forum cache tag, purged on each forum write).

const (
	forumMembersPerPage  = 24
	forumMembersMaxQuery = 60
)

// forumMemberFrontSorts are the sorts the front offers, in order (the first is the default).
var forumMemberFrontSorts = []struct{ Key, Label string }{
	{"newest", "Newest"},
	{"active", "Most active"},
	{"name", "Name"},
}

// forumMemberSortLink is one sort link of the members toolbar.
type forumMemberSortLink struct {
	Key, Label, URL string
	Current         bool
}

// forumMembersQuery is ?q=&sort= for a members URL (sort left out when it is the default).
func forumMembersQuery(q, sort string) string {
	v := url.Values{}
	if q != "" {
		v.Set("q", q)
	}
	if sort != "" && sort != forumMemberFrontSorts[0].Key {
		v.Set("sort", sort)
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

// forumMemberFrontSort is the front sort key of ?sort= (the default for an unknown one).
func forumMemberFrontSort(v string) string {
	for _, so := range forumMemberFrontSorts {
		if v == so.Key {
			return so.Key
		}
	}
	return forumMemberFrontSorts[0].Key
}

// forumServeMembers renders the member list; false = 404.
func (s *Server) forumServeMembers(c *gin.Context, rt forumRoute, p *forumPage) bool {
	ctx := c.Request.Context()
	q := strings.Join(strings.Fields(c.Query("q")), " ")
	if utf8.RuneCountInString(q) > forumMembersMaxQuery {
		q = string([]rune(q)[:forumMembersMaxQuery])
	}
	sort := forumMemberFrontSort(c.Query("sort"))
	canonical := s.forumURL(ctx, "members")
	if forumCanonicalPage301(c, rt, canonical) {
		return true
	}
	mq := forumNormalizeMemberQuery(forumMemberQuery{Search: q, Sort: sort, Page: rt.Page, PerPage: forumMembersPerPage,
		PostedOnly: s.blockEnumeration()})
	members, total, err := s.forumMembers(ctx, mq)
	if err != nil {
		return false
	}
	pages := forumPages(total, forumMembersPerPage)
	if rt.Page > pages {
		return false
	}
	p = s.forumPageFor(c, p, forumViewMembers, "")
	p.Title = p.T("Members")
	p.Crumbs = append(p.Crumbs, forumCrumb{Label: p.Title})
	p.Members, p.MemberQuery, p.MembersTotal = members, mq, total
	p.Query = q
	p.Pagination = s.forumPagination(c, canonical+forumMembersQuery(q, sort), rt.Page, pages)
	links := make([]forumMemberSortLink, 0, len(forumMemberFrontSorts))
	for _, so := range forumMemberFrontSorts {
		links = append(links, forumMemberSortLink{Key: so.Key, Label: p.T(so.Label), URL: canonical + forumMembersQuery(q, so.Key), Current: so.Key == sort})
	}
	p.Extra["sorts"] = links
	p.Extra["sort"] = sort
	p.Extra["action"] = canonical
	if q != "" {
		c.Set(forumCtxNoindex, true) // a member search is not indexed nor cached (forumRender, wpHead)
		return s.forumFinish(c, p, "", rt.Page)
	}
	return s.forumFinish(c, p, canonical, rt.Page)
}
