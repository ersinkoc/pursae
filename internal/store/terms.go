package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/wp"
)

type Term struct {
	ID          int64
	Taxonomy    string
	ParentID    int64
	Slug        string
	Name        string
	Description string
	Count       int
	Meta        map[string]any
	Depth       int // set by HierarchicalTerms
}

const termCols = `id, taxonomy, coalesce(parent_id,0), slug, name, description, post_count, meta`

func scanTerm(row pgx.Row) (*Term, error) {
	t := &Term{}
	var meta []byte
	err := row.Scan(&t.ID, &t.Taxonomy, &t.ParentID, &t.Slug, &t.Name, &t.Description, &t.Count, &meta)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.Meta = map[string]any{}
	_ = json.Unmarshal(meta, &t.Meta)
	return t, nil
}

func scanTerms(rows pgx.Rows) ([]*Term, error) {
	defer rows.Close()
	var out []*Term
	for rows.Next() {
		t, err := scanTerm(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTerm(ctx context.Context, id int64) (*Term, error) {
	return scanTerm(s.Pool.QueryRow(ctx, `SELECT `+termCols+` FROM terms WHERE id = $1`, id))
}

func (s *Store) TermBySlug(ctx context.Context, taxonomy, slug string) (*Term, error) {
	return scanTerm(s.Pool.QueryRow(ctx, `SELECT `+termCols+` FROM terms WHERE taxonomy = $1 AND slug = $2`, taxonomy, slug))
}

func (s *Store) TermByName(ctx context.Context, taxonomy, name string) (*Term, error) {
	return scanTerm(s.Pool.QueryRow(ctx, `SELECT `+termCols+` FROM terms WHERE taxonomy = $1 AND lower(name) = lower($2)`, taxonomy, name))
}

type TermQuery struct {
	Taxonomy  string
	Search    string
	HideEmpty bool
	OrderBy   string // name, count, slug, id, description
	Order     string
	Page      int
	PerPage   int
	IDs       []int64
}

func (s *Store) QueryTerms(ctx context.Context, q TermQuery) ([]*Term, int, error) {
	args := []any{q.Taxonomy}
	conds := []string{"taxonomy = $1"}
	if q.Search != "" {
		args = append(args, "%"+q.Search+"%")
		conds = append(conds, "(name ILIKE $"+strconv.Itoa(len(args))+" OR slug ILIKE $"+strconv.Itoa(len(args))+")")
	}
	if q.HideEmpty {
		conds = append(conds, "post_count > 0")
	}
	if len(q.IDs) > 0 {
		args = append(args, q.IDs)
		conds = append(conds, "id = ANY($"+strconv.Itoa(len(args))+")")
	}
	where := strings.Join(conds, " AND ") + QueryFilter.cond(ctx, "term", &args)
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM terms WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	col := "lower(name)"
	switch q.OrderBy {
	case "count":
		col = "post_count"
	case "slug":
		col = "slug"
	case "id":
		col = "id"
	case "description":
		col = "description"
	}
	dir := "ASC"
	if strings.EqualFold(q.Order, "desc") {
		dir = "DESC"
	}
	sql := `SELECT ` + termCols + ` FROM terms WHERE ` + where + ` ORDER BY ` + col + ` ` + dir + `, id`
	if q.PerPage > 0 {
		page := q.Page
		if page < 1 {
			page = 1
		}
		sql += fmt.Sprintf(" LIMIT %d OFFSET %d", q.PerPage, (page-1)*q.PerPage)
	}
	rows, err := s.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, err
	}
	terms, err := scanTerms(rows)
	return terms, total, err
}

// HierarchicalTerms returns all terms of a taxonomy in tree order with Depth set (parents before children).
func (s *Store) HierarchicalTerms(ctx context.Context, taxonomy string, hideEmpty bool) ([]*Term, error) {
	all, _, err := s.QueryTerms(ctx, TermQuery{Taxonomy: taxonomy})
	if err != nil {
		return nil, err
	}
	children := map[int64][]*Term{}
	ids := map[int64]bool{}
	for _, t := range all {
		ids[t.ID] = true
	}
	for _, t := range all {
		parent := t.ParentID
		if !ids[parent] {
			parent = 0
		}
		children[parent] = append(children[parent], t)
	}
	var out []*Term
	var walk func(parent int64, depth int)
	walk = func(parent int64, depth int) {
		for _, t := range children[parent] {
			t.Depth = depth
			if !hideEmpty || t.Count > 0 {
				out = append(out, t)
			}
			walk(t.ID, depth+1)
		}
	}
	walk(0, 0)
	return out, nil
}

// InsertTerm creates a term with a unique slug; it fails with ErrTermExists for a duplicate name under the same parent.
var ErrTermExists = errors.New("A term with the name provided already exists with this parent.")

func (s *Store) InsertTerm(ctx context.Context, t *Term) error {
	var exists bool
	args := []any{t.Taxonomy, t.Name, t.ParentID}
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM terms WHERE taxonomy=$1 AND lower(name)=lower($2)
		AND coalesce(parent_id,0)=$3`+QueryFilter.cond(ctx, "term", &args)+`)`, args...).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrTermExists
	}
	slug, err := s.uniqueTermSlug(ctx, t.Taxonomy, t.Slug, t.Name, 0)
	if err != nil {
		return err
	}
	t.Slug = slug
	meta, _ := json.Marshal(orEmpty(t.Meta))
	return s.Pool.QueryRow(ctx, `INSERT INTO terms(taxonomy, parent_id, slug, name, description, meta)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, t.Taxonomy, nullable(t.ParentID), t.Slug, t.Name, t.Description, meta).Scan(&t.ID)
}

func (s *Store) UpdateTerm(ctx context.Context, t *Term) error {
	slug, err := s.uniqueTermSlug(ctx, t.Taxonomy, t.Slug, t.Name, t.ID)
	if err != nil {
		return err
	}
	t.Slug = slug
	if t.ParentID == t.ID {
		t.ParentID = 0
	}
	meta, _ := json.Marshal(orEmpty(t.Meta))
	_, err = s.Pool.Exec(ctx, `UPDATE terms SET parent_id=$2, slug=$3, name=$4, description=$5, meta=$6 WHERE id=$1`,
		t.ID, nullable(t.ParentID), t.Slug, t.Name, t.Description, meta)
	return err
}

// DeleteTerm removes a term. Posts left without a category get the default one, children move to the parent.
func (s *Store) DeleteTerm(ctx context.Context, t *Term, defaultCategory int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var orphaned []int64
		if t.Taxonomy == "category" {
			rows, err := tx.Query(ctx, `SELECT post_id FROM post_terms pt WHERE term_id = $1 AND NOT EXISTS (
				SELECT 1 FROM post_terms o JOIN terms ot ON ot.id = o.term_id
				WHERE o.post_id = pt.post_id AND ot.taxonomy = 'category' AND o.term_id <> $1)`, t.ID)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				orphaned = append(orphaned, id)
			}
			rows.Close()
		}
		if _, err := tx.Exec(ctx, `UPDATE terms SET parent_id = $2 WHERE parent_id = $1`, t.ID, nullable(t.ParentID)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM terms WHERE id = $1`, t.ID); err != nil {
			return err
		}
		for _, pid := range orphaned {
			if _, err := tx.Exec(ctx, `INSERT INTO post_terms(post_id, term_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, pid, defaultCategory); err != nil {
				return err
			}
		}
		if len(orphaned) > 0 {
			_, err := tx.Exec(ctx, `UPDATE terms SET post_count = (SELECT count(*) FROM post_terms pt JOIN posts p ON p.id = pt.post_id
				WHERE pt.term_id = $1 AND p.status = 'publish') WHERE id = $1`, defaultCategory)
			return err
		}
		return nil
	})
}

func (s *Store) uniqueTermSlug(ctx context.Context, taxonomy, slug, name string, excludeID int64) (string, error) {
	slug = wp.SanitizeTitle(slug)
	if slug == "" {
		slug = wp.SanitizeTitle(name)
	}
	if slug == "" {
		slug = "term"
	}
	cand := slug
	for i := 2; ; i++ {
		var taken bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM terms WHERE taxonomy=$1 AND slug=$2 AND id<>$3)`,
			taxonomy, cand, excludeID).Scan(&taken); err != nil {
			return "", err
		}
		if !taken {
			return cand, nil
		}
		cand = slug + "-" + strconv.Itoa(i)
	}
}

// SetPostTerms replaces the post's terms of one taxonomy and recounts the affected terms.
func (s *Store) SetPostTerms(ctx context.Context, postID int64, taxonomy string, termIDs []int64) error {
	before, err := s.postTermIDs(ctx, postID)
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM post_terms WHERE post_id = $1
			AND term_id IN (SELECT id FROM terms WHERE taxonomy = $2)`, postID, taxonomy); err != nil {
			return err
		}
		for i, id := range termIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO post_terms(post_id, term_id, sort_order)
				SELECT $1, id, $3 FROM terms WHERE id = $2 AND taxonomy = $4 ON CONFLICT DO NOTHING`, postID, id, i, taxonomy); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.RecountTerms(ctx, append(before, termIDs...))
}

func (s *Store) postTermIDs(ctx context.Context, postID int64) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `SELECT term_id FROM post_terms WHERE post_id = $1`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) recountPostTerms(ctx context.Context, postID int64) error {
	ids, err := s.postTermIDs(ctx, postID)
	if err != nil {
		return err
	}
	return s.RecountTerms(ctx, ids)
}

// RecountTerms refreshes the cached published-post count of the given terms (_update_post_term_count()).
func (s *Store) RecountTerms(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.Pool.Exec(ctx, `UPDATE terms t SET post_count = (SELECT count(*) FROM post_terms pt
		JOIN posts p ON p.id = pt.post_id WHERE pt.term_id = t.id AND p.status = 'publish')
		WHERE t.id = ANY($1)`, ids)
	return err
}

// TermsForPosts returns each post's terms of a taxonomy in assignment order.
func (s *Store) TermsForPosts(ctx context.Context, postIDs []int64, taxonomy string) (map[int64][]*Term, error) {
	out := map[int64][]*Term{}
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT pt.post_id, `+prefixed("t", termCols)+` FROM post_terms pt
		JOIN terms t ON t.id = pt.term_id WHERE pt.post_id = ANY($1) AND t.taxonomy = $2
		ORDER BY pt.post_id, lower(t.name)`, postIDs, taxonomy)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid int64
		t := &Term{}
		var meta []byte
		if err := rows.Scan(&pid, &t.ID, &t.Taxonomy, &t.ParentID, &t.Slug, &t.Name, &t.Description, &t.Count, &meta); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(meta, &t.Meta)
		out[pid] = append(out[pid], t)
	}
	return out, rows.Err()
}

// TermIDsByNames resolves comma-separated tag names, creating missing ones (the tag box behaviour).
func (s *Store) TermIDsByNames(ctx context.Context, taxonomy string, names []string) ([]int64, error) {
	var ids []int64
	seen := map[int64]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		t, err := s.TermByName(ctx, taxonomy, n)
		if errors.Is(err, ErrNotFound) {
			t = &Term{Taxonomy: taxonomy, Name: n}
			err = s.InsertTerm(ctx, t)
		}
		if err != nil {
			return nil, err
		}
		if !seen[t.ID] {
			seen[t.ID] = true
			ids = append(ids, t.ID)
		}
	}
	return ids, nil
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
