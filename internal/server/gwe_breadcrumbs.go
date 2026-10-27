package server

import (
	"context"
	"html"
	"strconv"
	"time"

	"wpgo/internal/builder"
	"wpgo/internal/links"
	"wpgo/internal/store"
)

// gweBreadcrumbTrail is the native trail of the Breadcrumbs widget (upstream prints Yoast SEO's):
// Home › ancestors › current item. Posts go through their first category and its parents, pages
// and other hierarchical posts through their parent pages; term archives list their parent
// terms, date archives year › month › day; search results and 404 pages end with a label. The
// blog page (page_for_posts) sits between Home and its posts. The last item has no URL.
func (s *Server) gweBreadcrumbTrail(ctx context.Context, v *siteView, lb *links.Builder) []builder.TermLink {
	out := []builder.TermLink{{Name: "Home", URL: lb.HomeURL("/")}}
	if v == nil || v.Q == nil {
		return out
	}
	q := v.Q
	esc := html.EscapeString
	termChain := func(t *store.Term) []builder.TermLink {
		var chain []builder.TermLink
		seen := map[int64]bool{t.ID: true}
		for cur, i := t, 0; i < 20 && cur.ParentID > 0 && !seen[cur.ParentID]; i++ {
			parent, err := s.store.GetTerm(ctx, cur.ParentID)
			if err != nil {
				break
			}
			seen[parent.ID] = true
			chain = append([]builder.TermLink{{Name: esc(parent.Name), URL: lb.TermLink(parent)}}, chain...)
			cur = parent
		}
		return chain
	}
	postsPage := func() {
		if q.PostsPage != nil && q.PostsPage.Status == "publish" {
			out = append(out, builder.TermLink{Name: esc(gweBreadcrumbTitle(q.PostsPage)), URL: lb.Permalink(q.PostsPage)})
		}
	}
	switch {
	case q.Kind == "front-page":
		out[0].URL = ""
	case q.Post != nil && (q.Kind == "single" || q.Kind == "page"):
		p := q.Post
		if p.Type == "post" {
			if q.PostsPage == nil {
				if id := s.settings.Int("page_for_posts"); id > 0 && s.settings.String("show_on_front") == "page" {
					if pp, err := s.store.GetPost(ctx, int64(id)); err == nil {
						q2 := *q
						q2.PostsPage = pp
						q = &q2
					}
				}
			}
			postsPage()
			cats, _ := s.store.TermsForPosts(ctx, []int64{p.ID}, "category")
			if list := cats[p.ID]; len(list) > 0 {
				t := list[0]
				out = append(out, termChain(t)...)
				out = append(out, builder.TermLink{Name: esc(t.Name), URL: lb.TermLink(t)})
			}
		}
		var chain []builder.TermLink
		seen := map[int64]bool{p.ID: true}
		for cur, i := p, 0; i < 20 && cur.ParentID > 0 && !seen[cur.ParentID]; i++ {
			parent, err := s.store.GetPost(ctx, cur.ParentID)
			if err != nil {
				break
			}
			seen[parent.ID] = true
			chain = append([]builder.TermLink{{Name: esc(gweBreadcrumbTitle(parent)), URL: lb.Permalink(parent)}}, chain...)
			cur = parent
		}
		out = append(out, chain...)
		out = append(out, builder.TermLink{Name: esc(gweBreadcrumbTitle(p))})
	case q.Term != nil:
		out = append(out, termChain(q.Term)...)
		out = append(out, builder.TermLink{Name: esc(q.Term.Name)})
	case q.Author != nil:
		out = append(out, builder.TermLink{Name: "Archives for " + esc(q.Author.DisplayName)})
	case q.Kind == "search":
		out = append(out, builder.TermLink{Name: "You searched for " + esc(q.Search)})
	case q.Kind == "404":
		out = append(out, builder.TermLink{Name: "Error 404: Page not found"})
	case q.Year > 0:
		y := strconv.Itoa(q.Year)
		switch {
		case q.Day > 0:
			out = append(out, builder.TermLink{Name: y, URL: lb.YearLink(q.Year)},
				builder.TermLink{Name: time.Month(q.Month).String(), URL: lb.MonthLink(q.Year, q.Month)},
				builder.TermLink{Name: strconv.Itoa(q.Day)})
		case q.Month > 0:
			out = append(out, builder.TermLink{Name: y, URL: lb.YearLink(q.Year)}, builder.TermLink{Name: time.Month(q.Month).String()})
		default:
			out = append(out, builder.TermLink{Name: y})
		}
	case q.Kind == "home" && q.PostsPage != nil:
		out = append(out, builder.TermLink{Name: esc(gweBreadcrumbTitle(q.PostsPage))})
	case q.Kind == "home":
		out[0].URL = ""
	}
	return out
}

func gweBreadcrumbTitle(p *store.Post) string {
	if p.Title == "" {
		return "(no title)"
	}
	return p.Title
}
