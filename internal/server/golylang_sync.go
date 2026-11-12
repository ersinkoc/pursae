package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Golylang synchronization: what a new translation copies from its source, and what the Synchronization
// settings keep identical (or translated: terms, parent page, featured image) between translations.

// pllMetaSkipped are post meta keys never copied between translations: editing state, slugs, page
// builder documents (they are content) and the keys with sync settings of their own.
func pllMetaSkipped(key string) bool {
	switch key {
	case "_edit_lock", "_edit_last", "_wp_old_slug", "_encloseme", "_pingme", "_wp_page_template", "_thumbnail_id", "classic-editor-remember":
		return true
	}
	return strings.HasPrefix(key, "_wp_trash_meta") || strings.HasPrefix(key, "_pll") || strings.HasPrefix(key, "_web_builder")
}

// pllSEOTranslatedFields are the Pursae SEO fields each translation writes in its own language (Polylang's
// Yoast integration leaves them out of the custom fields synchronization). Robots, cornerstone,
// primary category, schema types and images keep syncing.
var pllSEOTranslatedFields = []string{"title", "description", "focus", "canonical", "og_title", "og_description",
	"tw_title", "tw_description", "bc_title", "schema_custom", "seo_score", "read_score"}

// pllSEOKeys are the meta keys of those fields: _seogo_* and the Yoast and Rank Math keys they read
// (seogoFields). rank_math_robots and rank_math_advanced_robots hold robots settings shared with
// fields that keep syncing, so they never appear here.
var pllSEOKeys = func() map[string]bool {
	m := map[string]bool{}
	for _, f := range pllSEOTranslatedFields {
		m[seogoKey(f)] = true
		for _, k := range seogoFields[f] {
			if k != "" && k != "rank_math_robots" && k != "rank_math_advanced_robots" {
				m[k] = true
			}
		}
	}
	return m
}()

// pllSEOTranslatable reports whether a meta key holds translated SEO text (pllSEOKeys).
func pllSEOTranslatable(key string) bool { return pllSEOKeys[key] }

// pllSEOCanonical reports whether a meta key is a custom canonical URL: a new translation never starts
// out canonicalised to its source.
func pllSEOCanonical(key string) bool {
	return key == seogoKey("canonical") || key == seogoFields["canonical"][0] || key == seogoFields["canonical"][1]
}

// pllCopyForTranslation fills a new translation from its source like Polylang: terms (translated), featured
// image, custom fields, page order, template, parent (translated), format and discussion settings are always
// copied; title, content and excerpt too when Duplicate content is on (Pro).
func (s *Server) pllCopyForTranslation(ctx context.Context, src, dst *store.Post, l *pllLanguage, duplicate bool) {
	meta := map[string]any{}
	for k, v := range src.Meta {
		if !pllMetaSkipped(k) && !pllSEOCanonical(k) {
			meta[k] = v
		}
	}
	if tpl, ok := src.Meta["_wp_page_template"]; ok {
		meta["_wp_page_template"] = tpl
	}
	rawMeta, _ := json.Marshal(meta)
	parent := s.pllTranslatedID(ctx, pllPost, src.ParentID, l.Slug)
	featured := s.pllTranslatedMedia(ctx, src.FeaturedMediaID, l.Slug)
	dst.MenuOrder, dst.ParentID, dst.FeaturedMediaID, dst.Format = src.MenuOrder, parent, featured, store.CanonicalPostFormat(src.Format)
	dst.CommentStatus, dst.PingStatus = src.CommentStatus, src.PingStatus
	for k, v := range meta {
		dst.Meta[k] = v
	}
	if duplicate {
		dst.Title, dst.Content, dst.Excerpt = src.Title, src.Content, src.Excerpt
	}
	_, err := s.store.Pool.Exec(ctx, `UPDATE posts SET menu_order = $2, parent_id = $3, featured_media_id = $4, format = $5, comment_status = $6,
		ping_status = $7, meta = meta || $8::jsonb,
		title = CASE WHEN $9 THEN $10 ELSE title END, content = CASE WHEN $9 THEN $11 ELSE content END, excerpt = CASE WHEN $9 THEN $12 ELSE excerpt END
		WHERE id = $1`, dst.ID, dst.MenuOrder, nullable64(parent), nullable64(featured), dst.Format, dst.CommentStatus, dst.PingStatus, rawMeta,
		duplicate, src.Title, src.Content, src.Excerpt)
	_ = pllLog("copying fields to a new translation", err)
	if duplicate {
		s.wbCopyDoc(ctx, src, dst) // Pursae Web Builder: the translation starts from the source's doc; wbCopyDocErr re-runs SanitizeSnapshot
	}
	s.pllCopyTerms(ctx, src.ID, dst.ID, l.Slug)
}

// pllTranslatedID is the translation of an object in lang, the object itself when it has none.
func (s *Server) pllTranslatedID(ctx context.Context, k pllKind, id int64, lang string) int64 {
	if id <= 0 {
		return id
	}
	if t := s.pllGet(ctx, k, id, lang); t > 0 {
		return t
	}
	return id
}

// pllTranslatedMedia maps a media id to its translation when media are translated.
func (s *Server) pllTranslatedMedia(ctx context.Context, id int64, lang string) int64 {
	if id <= 0 || !s.pllOpts().MediaSupport {
		return id
	}
	return s.pllTranslatedID(ctx, pllMedia, id, lang)
}

// pllCopyTerms gives dst the terms of src: the translations in lang for translated taxonomies (terms without
// one are left out), the same terms for the other taxonomies.
func (s *Server) pllCopyTerms(ctx context.Context, srcID, dstID int64, lang string) {
	rows, err := s.store.Pool.Query(ctx, `SELECT t.id, t.taxonomy FROM post_terms pt JOIN terms t ON t.id = pt.term_id WHERE pt.post_id = $1 ORDER BY pt.sort_order`, srcID)
	if err != nil {
		return
	}
	byTax := map[string][]int64{}
	for rows.Next() {
		var id int64
		var tax string
		if rows.Scan(&id, &tax) == nil {
			byTax[tax] = append(byTax[tax], id)
		}
	}
	rows.Close()
	var taxes []string
	_ = s.store.Pool.QueryRow(ctx, `SELECT coalesce(array_agg(DISTINCT t.taxonomy), '{}') FROM post_terms pt JOIN terms t ON t.id = pt.term_id WHERE pt.post_id = $1`, dstID).Scan(&taxes)
	for _, tax := range taxes {
		if _, ok := byTax[tax]; !ok {
			byTax[tax] = nil
		}
	}
	for tax, ids := range byTax {
		var out []int64
		for _, id := range ids {
			if !s.pllTranslatedTaxonomy(tax) {
				out = append(out, id)
			} else if t := s.pllGet(ctx, pllTerm, id, lang); t > 0 {
				out = append(out, t)
			}
		}
		_ = s.store.SetPostTerms(ctx, dstID, tax, out)
	}
}

// golylangSaved runs after every post save: a translated post gets a language (the REST lang parameter,
// the admin language filter or the default one), REST translations[lang]=id links translations, and
// the Synchronization settings are applied to the post's translations.
func (s *Server) golylangSaved(ctx context.Context, p *store.Post) {
	if !s.golylangOn() || p == nil || !s.pllTranslatedType(p.Type) || p.Status == "auto-draft" || len(s.pllLanguages(ctx)) == 0 {
		return
	}
	if gc, ok := ctx.(*gin.Context); ok && pllIsREST(gc) {
		if l := s.pllLang(ctx, pllRESTLang(gc)); l != nil {
			_ = s.pllSetLang(ctx, pllPost, p.ID, l.Slug)
			s.pllReslug(ctx, p) // Share slugs: the slug was checked in the post's former language
		}
		if m := pllRESTTranslations(gc); len(m) > 0 {
			u := pllActor(ctx)
			tr := s.pllTranslations(ctx, pllPost, p.ID)
			own := s.pllObjLang(ctx, pllPost, p.ID)
			for lang, id := range m {
				if lang == own || id == p.ID {
					continue // the saved post keeps its own place in the group
				}
				// Linking moves the other post into this translation group (and under its
				// synchronization): only for posts the user may edit, as the admin-ajax linking checks.
				if t, err := s.store.GetPost(ctx, id); err == nil && t.Type == p.Type && s.pllObjLang(ctx, pllPost, id) == lang && u != nil && u.Can("edit_post", t) {
					tr[lang] = id
				}
			}
			_ = s.pllSaveTranslations(ctx, pllPost, tr)
		}
	}
	s.pllEnsureLang(ctx, p)
	if p.Status != "trash" {
		s.pllSync(ctx, p)
	}
}

// pllActor is the user saving: the REST user, else the cookie user; nil outside a request. A
// wp-admin save that passes the request context (classic editor, Quick Edit, Bulk Edit, custom
// fields) is known by the user pllDetect put on its pllReq.
func pllActor(ctx context.Context) *store.User {
	gc, ok := ctx.(*gin.Context)
	if !ok {
		if r := pllFrom(ctx); r != nil && r.actorSet {
			return r.actor
		}
		return nil
	}
	if u := restCurrent(gc); u != nil {
		return u
	}
	return currentUser(gc)
}

// pllUserRequest reports whether ctx belongs to a user's request (a *gin.Context, or the request
// context of a wp-admin screen), as opposed to background work (cron, imports) that has no user.
func pllUserRequest(ctx context.Context) bool {
	if _, ok := ctx.(*gin.Context); ok {
		return true
	}
	r := pllFrom(ctx)
	return r != nil && r.actorSet
}

func pllIsREST(c *gin.Context) bool {
	return strings.HasPrefix(c.Request.URL.Path, "/wp-json") || c.Query("rest_route") != ""
}

// pllSync copies the synchronized fields of p to its translations.
func (s *Server) pllSync(ctx context.Context, p *store.Post) {
	o := s.pllOpts()
	if len(o.Sync) == 0 {
		return
	}
	own := s.pllObjLang(ctx, pllPost, p.ID)
	inRequest := pllUserRequest(ctx)
	u := pllActor(ctx)
	for lang, id := range s.pllTranslations(ctx, pllPost, p.ID) {
		if id == p.ID || lang == own {
			continue
		}
		t, err := s.store.GetPost(ctx, id)
		if err != nil {
			continue
		}
		// A save copies its fields only onto translations the saving user may edit too (Polylang
		// does not synchronize what the user could not change by hand).
		if inRequest && (u == nil || !u.Can("edit_post", t)) {
			continue
		}
		if o.syncs("comment_status") {
			t.CommentStatus = p.CommentStatus
		}
		if o.syncs("ping_status") {
			t.PingStatus = p.PingStatus
		}
		if o.syncs("sticky_posts") {
			t.Sticky = p.Sticky
		}
		if o.syncs("post_date") && p.PublishedAt != nil {
			t.PublishedAt = p.PublishedAt
		}
		if o.syncs("post_format") {
			t.Format = store.CanonicalPostFormat(p.Format)
		}
		if o.syncs("menu_order") {
			t.MenuOrder = p.MenuOrder
		}
		if o.syncs("post_parent") {
			t.ParentID = s.pllTranslatedID(ctx, pllPost, p.ParentID, lang)
		}
		if o.syncs("_thumbnail_id") {
			t.FeaturedMediaID = s.pllTranslatedMedia(ctx, p.FeaturedMediaID, lang)
		}
		if o.syncs("post_meta") {
			for k := range t.Meta {
				if !pllMetaSkipped(k) && !pllSEOTranslatable(k) {
					if _, ok := p.Meta[k]; !ok {
						delete(t.Meta, k)
					}
				}
			}
			for k, v := range p.Meta {
				if !pllMetaSkipped(k) && !pllSEOTranslatable(k) {
					t.Meta[k] = v
				}
			}
		}
		if o.syncs("_wp_page_template") {
			if tpl, ok := p.Meta["_wp_page_template"]; ok {
				t.Meta["_wp_page_template"] = tpl
			} else {
				delete(t.Meta, "_wp_page_template")
			}
		}
		rawMeta, _ := json.Marshal(t.Meta)
		if _, err := s.store.Pool.Exec(ctx, `UPDATE posts SET comment_status = $2, ping_status = $3, sticky = $4, published_at = $5, format = $6,
			menu_order = $7, parent_id = $8, featured_media_id = $9, meta = $10::jsonb WHERE id = $1`,
			t.ID, t.CommentStatus, t.PingStatus, t.Sticky, t.PublishedAt, t.Format, t.MenuOrder, nullable64(t.ParentID), nullable64(t.FeaturedMediaID), rawMeta); err != nil {
			continue
		}
		if o.syncs("taxonomies") {
			s.pllCopyTerms(ctx, p.ID, t.ID, lang)
		}
		if s.onPostSaved != nil {
			s.onPostSaved(ctx, t) // cache purge of the translation
		}
	}
}
