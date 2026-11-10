package server

import (
	"context"

	"wpgo/internal/store"
)

// Golylang's Share slugs module (Polylang Pro): posts and terms in different languages may use the
// same slug (/en/product/linen-shirt/ and /fr/product/linen-shirt/). Slug uniqueness is checked per
// language, and a URL resolves to the object of the request language first. It needs the language to
// come from the URL (directory, subdomain or domain); with the language set from the content the URLs
// would collide, so the module then does nothing.

func init() {
	store.SlugScope = func(ctx context.Context, kind string, id int64, add func(any) string) string {
		r := pllFrom(ctx)
		if r == nil || r.srv == nil || !r.srv.pllShareSlugs() {
			return ""
		}
		s := r.srv
		lang := ""
		if r.Lang != nil {
			lang = r.Lang.Slug
		}
		def := defaultSlug(s.pllDefault(ctx))
		switch kind {
		case "post":
			if id > 0 {
				lang = s.pllObjLang(ctx, pllPost, id)
			}
			if lang == "" {
				lang = def
			}
			return "(NOT (type = ANY(" + add(s.pllPostTypes()) + ")) OR coalesce((SELECT lang FROM pll_post_langs WHERE post_id = posts.id), " +
				add(def) + ") = " + add(lang) + ")"
		case "term":
			if id > 0 {
				lang = s.pllObjLang(ctx, pllTerm, id)
			}
			if lang == "" {
				lang = def
			}
			return "(NOT (taxonomy = ANY(" + add(s.pllTaxonomies()) + ")) OR coalesce((SELECT lang FROM pll_term_langs WHERE term_id = terms.id), " +
				add(def) + ") = " + add(lang) + ")"
		}
		return ""
	}
	store.SlugPrefer = func(ctx context.Context, kind string, add func(any) string) string {
		r := pllFrom(ctx)
		if r == nil || r.srv == nil || r.Lang == nil || !r.srv.pllShareSlugs() {
			return ""
		}
		switch kind {
		case "post":
			return "(posts.id IN (SELECT post_id FROM pll_post_langs WHERE lang = " + add(r.Lang.Slug) + ")) DESC"
		case "term":
			return "(terms.id IN (SELECT term_id FROM pll_term_langs WHERE lang = " + add(r.Lang.Slug) + ")) DESC"
		}
		return ""
	}
}

// pllShareSlugs reports whether the Share slugs module is on and can work (language from the URL).
func (s *Server) pllShareSlugs() bool {
	if !s.golylangOn() {
		return false
	}
	o := s.pllOpts()
	return o.ShareSlugs && o.ForceLang > 0
}

// pllReslug re-checks the slug of a post whose language just changed. With Share slugs a slug is
// unique per language only, so the new language may already have a post with it, and the URL would
// then show that other post (Polylang runs wp_unique_post_slug on the save that changes the
// language). p.Slug is updated in place.
func (s *Server) pllReslug(ctx context.Context, p *store.Post) {
	if p == nil || p.Slug == "" || p.Status == "auto-draft" || p.Status == "trash" || !s.pllShareSlugs() {
		return
	}
	// A request cache of its own: the language was just written, a cached one would be stale.
	sctx := context.WithValue(ctx, pllCtxKey{}, &pllReq{srv: s})
	slug, err := s.store.UniqueSlug(sctx, p.Type, p.ParentID, p.Slug, p.ID)
	if err != nil || slug == "" || slug == p.Slug {
		return
	}
	if _, err := s.store.Pool.Exec(ctx, `UPDATE posts SET slug = $2 WHERE id = $1`, p.ID, slug); err == nil {
		p.Slug = slug
	}
}
