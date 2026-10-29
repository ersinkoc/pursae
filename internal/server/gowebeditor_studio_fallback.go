package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// gweStudioFallbackSections is a stand-in for the section library (golementor/v1/library/sections,
// built on its own branch): a few sections cut from the homepage designs (gowebeditor_designs.go),
// served in the same shape so the Studio's gallery works before the library lands. It registers only
// when no other handler owns the route; drop this file when the library is merged.
func (s *Server) gweStudioFallbackSections() {
	const list, one = "/golementor/v1/library/sections", "/golementor/v1/library/sections/(?P<id>[\\w-]+)"
	for _, r := range s.rest.routes {
		if r.Pattern == list {
			return
		}
	}
	type entry struct{ design, title, category string }
	// One entry per top-level section of each design, in order.
	entries := []entry{
		{"gwe-home-business", "Dark hero with two buttons", "hero"},
		{"gwe-home-business", "Three services", "services"},
		{"gwe-home-business", "Quote and a big number", "testimonials"},
		{"gwe-home-business", "Blue call to action", "cta"},
		{"gwe-home-personal", "Centered introduction", "hero"},
		{"gwe-home-personal", "Latest writing", "blog"},
		{"gwe-home-personal", "Newsletter sign-up", "newsletter"},
		{"gwe-home-portfolio", "Portfolio introduction", "about"},
		{"gwe-home-portfolio", "Selected work", "content"},
		{"gwe-home-portfolio", "Dark contact banner", "contact"},
		{"gwe-home-product", "Product launch hero", "hero"},
		{"gwe-home-product", "Four features", "features"},
		{"gwe-home-product", "Three pricing plans", "pricing"},
		{"gwe-home-product", "Dark call to action", "cta"},
	}
	titles := map[string]string{"hero": "Hero", "features": "Features", "about": "About", "services": "Services", "cta": "Call to action",
		"testimonials": "Testimonials", "pricing": "Pricing", "team": "Team", "faq": "FAQ", "stats": "Stats", "gallery": "Gallery", "logos": "Logos",
		"blog": "Blog", "contact": "Contact", "newsletter": "Newsletter", "content": "Content"}
	order := []string{"hero", "features", "about", "services", "cta", "testimonials", "pricing", "team", "faq", "stats", "gallery", "logos", "blog", "contact", "newsletter", "content"}
	type item struct {
		ID, Title, Category string
		build               func() any
	}
	var items []item
	seen := map[string]int{}
	for _, e := range entries {
		d := gweDesignByID(e.design)
		if d == nil {
			continue
		}
		n := seen[e.design]
		seen[e.design]++
		build := d.Build
		items = append(items, item{
			ID:       "fallback-" + strings.TrimPrefix(e.design, "gwe-home-") + "-" + itoa64(int64(n+1)),
			Title:    e.title,
			Category: e.category,
			build: func() any {
				secs := build()
				if n < len(secs) {
					return secs[n]
				}
				return nil
			},
		})
	}
	editor := func(h restHandler) restHandler {
		return func(c *gin.Context, r *restRequest) {
			if u := restCurrent(c); u == nil || !u.Can("edit_posts", nil) {
				restErr(c, http.StatusUnauthorized, "rest_forbidden", "Sorry, you are not allowed to do that.")
				return
			}
			h(c, r)
		}
	}
	s.rest.register("golementor/v1", list, map[string]restHandler{http.MethodGet: editor(func(c *gin.Context, _ *restRequest) {
		counts := map[string]int{}
		out := []map[string]any{}
		for _, it := range items {
			counts[it.Category]++
			out = append(out, map[string]any{"id": it.ID, "title": it.Title, "category": it.Category, "tags": []string{}, "thumbnail": ""})
		}
		cats := []map[string]any{}
		for _, slug := range order {
			if counts[slug] > 0 {
				cats = append(cats, map[string]any{"slug": slug, "title": titles[slug], "count": counts[slug]})
			}
		}
		c.JSON(http.StatusOK, map[string]any{"categories": cats, "items": out})
	})}, nil)
	s.rest.register("golementor/v1", one, map[string]restHandler{http.MethodGet: editor(func(c *gin.Context, r *restRequest) {
		for _, it := range items {
			if it.ID == r.Params["id"] {
				if el := it.build(); el != nil {
					c.JSON(http.StatusOK, map[string]any{"content": []any{el}})
					return
				}
			}
		}
		restErr(c, http.StatusNotFound, "rest_not_found", "Section not found.")
	})}, nil)
}
