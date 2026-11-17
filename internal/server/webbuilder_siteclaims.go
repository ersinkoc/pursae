package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/links"
	"wpgo/internal/store"
)

// Addresses a site import claims (docs/developer/web-builder-code-sections.md, "Importing a site"):
// a static copy of a WordPress site holds pages at addresses that are WordPress archives here
// (/author/admin/, /category/news/, /2026/09/). The page tree makes them pages at exactly those
// paths, but the router matches the archive rules (author, category, tag, date, a permalink
// structure, a custom post type's base) before it looks for a page, so the imported page was never
// reached: one showed this site's own author archive, the others 404. The import records each such
// page's path in the option wb_site_claims ({path: {id, kit}}); resolvePretty consults it before
// the archive rules, so the imported page answers at its own path 1:1 (page 1 of the path, no
// feed). A claim holds only while its page is published (or private) and still has that path;
// "Remove this import" and a later import of the kit drop the kit's stale claims.

const wbSiteClaimsOption = "wb_site_claims"

type wbSiteClaim struct {
	ID  int64  `json:"id"`
	Kit string `json:"kit"`
}

// wbClaimKey is a path as resolvePretty sees it: no leading or trailing slash.
func wbClaimKey(p string) string { return strings.Trim(p, "/") }

func (s *Server) wbSiteClaims() map[string]wbSiteClaim {
	out := map[string]wbSiteClaim{}
	if raw := s.settings.Get(wbSiteClaimsOption); raw != nil {
		if b, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(b, &out)
		}
	}
	return out
}

// wbSiteSetClaims replaces the claims of kit with claims (path → page id).
func (s *Server) wbSiteSetClaims(c context.Context, kit string, claims map[string]int64) error {
	all := s.wbSiteClaims()
	for p, cl := range all {
		if cl.Kit == kit {
			delete(all, p)
		}
	}
	for p, id := range claims {
		all[wbClaimKey(p)] = wbSiteClaim{ID: id, Kit: kit}
	}
	return s.settings.Set(c, map[string]any{wbSiteClaimsOption: all})
}

// wbSiteDropClaims removes the claims of pages that no longer exist (after "Remove this import").
func (s *Server) wbSiteDropClaims(c context.Context, ids []int64) {
	gone := map[int64]bool{}
	for _, id := range ids {
		gone[id] = true
	}
	all := s.wbSiteClaims()
	changed := false
	for p, cl := range all {
		if gone[cl.ID] {
			delete(all, p)
			changed = true
		}
	}
	if changed {
		_ = s.settings.Set(c, map[string]any{wbSiteClaimsOption: all})
	}
}

// wbSiteClaimed is the imported page that claims path (as resolvePretty sees it), nil when none.
func (s *Server) wbSiteClaimed(c *gin.Context, path string) *store.Post {
	if s.settings.Get(wbSiteClaimsOption) == nil { // most sites: no import claimed anything
		return nil
	}
	cl, ok := s.wbSiteClaims()[wbClaimKey(path)]
	if !ok || cl.ID <= 0 {
		return nil
	}
	p, err := s.store.GetPost(c.Request.Context(), cl.ID)
	if err != nil || p.Type != "page" || (p.Status != "publish" && p.Status != "private") {
		return nil
	}
	// Still at that path (a page moved or renamed since gives its address back).
	if pg, err := s.store.PageByPath(c.Request.Context(), wbClaimKey(path)); err != nil || pg.ID != p.ID {
		return nil
	}
	return p
}

// wbRouteShadowed says which rule of the router answers path before its pages ("" when a page at
// path is reached): what an imported page at that path would lose to without a claim.
func (s *Server) wbRouteShadowed(c *gin.Context, path string) string {
	path = wbClaimKey(path)
	if path == "" {
		return ""
	}
	catBase := strings.Trim(defaultStr(s.settings.String("category_base"), "category"), "/")
	tagBase := strings.Trim(defaultStr(s.settings.String("tag_base"), "tag"), "/")
	switch {
	case strings.HasPrefix(path, catBase+"/"):
		return "category archive"
	case strings.HasPrefix(path, tagBase+"/"):
		return "tag archive"
	case strings.HasPrefix(path, "type/"):
		return "post format archive"
	case strings.HasPrefix(path, "author/"):
		return "author archive"
	case strings.HasPrefix(path, "search/"):
		return "search"
	}
	if m := feedRe.FindStringSubmatch(path); m != nil {
		return "feed"
	}
	if m := pagedRe.FindStringSubmatch(path); m != nil {
		return "paged archive"
	}
	for _, pt := range siteRoutedTypes() {
		base, archive := siteCPTSlugs(pt)
		if archive != "" && path == archive || base != "" && strings.HasPrefix(path, base+"/") {
			return pt.Name + " post type"
		}
	}
	lb := s.links(c.Request.Context())
	if lb.Structure != "" && !verbosePageRulesRe.MatchString(lb.Structure) {
		if m, ok := links.NewMatcher(lb.Structure).Match(path); ok && (m["post_id"] != "" || m["postname"] != "") {
			return "post permalink"
		}
	}
	if dateRe.MatchString(path) {
		return "date archive"
	}
	return ""
}
