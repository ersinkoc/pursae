package server

import (
	"context"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Pursae Multi Language: the "Untranslated" and "Outdated" views of the posts lists.
//
// For every other language, the list of a translated type gets two views next to the status links:
//
//   - Untranslated (French): the items of the default language without a French translation;
//   - Outdated (French): the items of the default language whose French translation was last
//     changed before the item itself (the same rule as the Languages screen, pllTranslationStatus).
//
// The query is ?pll_filter=untranslated|outdated&pll_target=<slug>.

const (
	pllFilterUntranslated = "untranslated"
	pllFilterOutdated     = "outdated"
)

// pllFilterIDs lists the posts of typ (not trashed) in the default language that match filter for target.
func (s *Server) pllFilterIDs(ctx context.Context, typ, filter, target string) []int64 {
	def := defaultSlug(s.pllDefault(ctx))
	if def == "" || target == def {
		return nil
	}
	const src = `SELECT p.id, p.modified_at, coalesce(sl.grp, 0) AS grp FROM posts p LEFT JOIN pll_post_langs sl ON sl.post_id = p.id
		WHERE p.type = $1 AND p.status NOT IN ('trash', 'auto-draft', 'inherit') AND coalesce(sl.lang, $2) = $2`
	var q string
	switch filter {
	case pllFilterUntranslated:
		q = `WITH src AS (` + src + `) SELECT s.id FROM src s WHERE s.grp = 0 OR NOT EXISTS
			(SELECT 1 FROM pll_post_langs t JOIN posts tp ON tp.id = t.post_id WHERE t.grp = s.grp AND t.lang = $3 AND tp.status <> 'trash')`
	case pllFilterOutdated:
		q = `WITH src AS (` + src + `) SELECT s.id FROM src s JOIN pll_post_langs t ON t.grp = s.grp AND t.lang = $3
			JOIN posts tp ON tp.id = t.post_id WHERE s.grp <> 0 AND tp.status <> 'trash' AND tp.modified_at < s.modified_at`
	default:
		return nil
	}
	rows, err := s.store.Pool.Query(ctx, q, typ, def, target)
	if err != nil {
		_ = pllLog("listing "+filter+" items", err)
		return nil
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// golylangListFilter narrows the posts list query to an "Untranslated" or "Outdated" view; it
// returns the active view key ("untranslated:fr"), "" when none.
func (s *Server) golylangListFilter(c *gin.Context, typ string, q *store.PostQuery) string {
	filter, target := c.Query("pll_filter"), c.Query("pll_target")
	if filter == "" || !s.golylangOn() || !s.pllTranslatedType(typ) || s.pllLang(c, target) == nil {
		return ""
	}
	ids := s.pllFilterIDs(c.Request.Context(), typ, filter, target)
	if ids == nil {
		return ""
	}
	if q.IDs != nil { // another view (Pursae SEO) already narrowed the list: keep both
		var both []int64
		for _, id := range ids {
			if slices.Contains(q.IDs, id) {
				both = append(both, id)
			}
		}
		ids = both
	}
	q.IDs = append([]int64{-1}, ids...)
	return filter + ":" + target
}

// golylangListViews adds the "Untranslated" and "Outdated" views of each other language.
func (s *Server) golylangListViews(c *gin.Context, d *postsListData, typ, base, active string) {
	if !s.golylangOn() || !s.pllTranslatedType(typ) {
		return
	}
	ctx := c.Request.Context()
	def := defaultSlug(s.pllDefault(ctx))
	if def == "" {
		return
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	for _, l := range s.pllLanguages(ctx) {
		if l.Slug == def {
			continue
		}
		for _, f := range []struct{ key, label string }{{pllFilterUntranslated, "Untranslated"}, {pllFilterOutdated, "Outdated"}} {
			key := f.key + ":" + l.Slug
			n := len(s.pllFilterIDs(ctx, typ, f.key, l.Slug))
			if n == 0 && active != key {
				continue
			}
			d.Statuses = append(d.Statuses, statusLink{"pll_" + f.key + "_" + l.Slug, f.label + " (" + l.Name + ")",
				base + sep + "pll_filter=" + f.key + "&pll_target=" + l.Slug, n, active == key})
		}
	}
	if active != "" {
		for i := range d.Statuses {
			if !strings.HasPrefix(d.Statuses[i].Key, "pll_") {
				d.Statuses[i].Current = false
			}
		}
	}
}

// pllOutdatedSet lists, among the translations ids, those last changed before another member of
// their group (the language columns show them with the "outdated" icon).
func (s *Server) pllOutdatedSet(ctx context.Context, ids []int64) map[int64]bool {
	out := map[int64]bool{}
	if len(ids) == 0 {
		return out
	}
	def := defaultSlug(s.pllDefault(ctx))
	rows, err := s.store.Pool.Query(ctx, `SELECT t.post_id FROM pll_post_langs t JOIN posts tp ON tp.id = t.post_id
		JOIN pll_post_langs sl ON sl.grp = t.grp AND sl.lang = $2 AND sl.post_id <> t.post_id JOIN posts sp ON sp.id = sl.post_id
		WHERE t.post_id = ANY($1) AND t.grp <> 0 AND sp.status <> 'trash' AND tp.modified_at < sp.modified_at`, ids, def)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}
