package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Widget administration (the block widgets editor and the wp/v2/widgets REST API).
// A widget whose area_id is NULL sits in the inactive widgets area (wp_inactive_widgets).

// AdminWidget is a widget with its position; AreaSlug is "" for inactive widgets.
type AdminWidget struct {
	ID        int64
	AreaSlug  string
	AreaTheme string
	Type      string
	SortOrder int
	Settings  map[string]any
}

// ErrWidgetNotFound is returned when a widget id does not exist.
var ErrWidgetNotFound = errors.New("widget not found")

func scanAdminWidgets(rows pgx.Rows) ([]*AdminWidget, error) {
	defer rows.Close()
	var out []*AdminWidget
	for rows.Next() {
		w := &AdminWidget{}
		var raw []byte
		if err := rows.Scan(&w.ID, &w.AreaSlug, &w.AreaTheme, &w.Type, &w.SortOrder, &raw); err != nil {
			return nil, err
		}
		w.Settings = map[string]any{}
		_ = json.Unmarshal(raw, &w.Settings)
		out = append(out, w)
	}
	return out, rows.Err()
}

const adminWidgetCols = `w.id, coalesce(a.slug,''), coalesce(a.theme,''), w.type, w.sort_order, w.settings`

// ThemeWidgets returns the widgets of a theme's areas plus the inactive widgets, ordered by area and position.
func (s *Store) ThemeWidgets(ctx context.Context, theme string) ([]*AdminWidget, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+adminWidgetCols+` FROM widgets w LEFT JOIN widget_areas a ON a.id = w.area_id
		WHERE w.area_id IS NULL OR a.theme = $1 ORDER BY a.slug NULLS LAST, w.sort_order, w.id`, theme)
	if err != nil {
		return nil, err
	}
	return scanAdminWidgets(rows)
}

// GetAdminWidget loads one widget with its area.
func (s *Store) GetAdminWidget(ctx context.Context, id int64) (*AdminWidget, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+adminWidgetCols+` FROM widgets w LEFT JOIN widget_areas a ON a.id = w.area_id WHERE w.id = $1`, id)
	if err != nil {
		return nil, err
	}
	list, err := scanAdminWidgets(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrWidgetNotFound
	}
	return list[0], nil
}

// nextWidgetOrder is the sort order that appends to an area (nil = inactive).
func (s *Store) nextWidgetOrder(ctx context.Context, q pgxQuerier, areaID *int64) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT coalesce(max(sort_order)+1, 0) FROM widgets WHERE area_id IS NOT DISTINCT FROM $1`, areaID).Scan(&n)
	return n, err
}

type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// CreateWidget appends a widget to an area (nil = inactive) and returns its id.
func (s *Store) CreateWidget(ctx context.Context, areaID *int64, typ string, settings map[string]any) (int64, error) {
	order, err := s.nextWidgetOrder(ctx, s.Pool, areaID)
	if err != nil {
		return 0, err
	}
	raw, _ := json.Marshal(orEmpty(settings))
	var id int64
	err = s.Pool.QueryRow(ctx, `INSERT INTO widgets(area_id, type, sort_order, settings) VALUES ($1,$2,$3,$4) RETURNING id`,
		areaID, typ, order, raw).Scan(&id)
	return id, err
}

// UpdateWidgetSettings replaces a widget's settings.
func (s *Store) UpdateWidgetSettings(ctx context.Context, id int64, settings map[string]any) error {
	raw, _ := json.Marshal(orEmpty(settings))
	tag, err := s.Pool.Exec(ctx, `UPDATE widgets SET settings = $2, updated_at = now() WHERE id = $1`, id, raw)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrWidgetNotFound
	}
	return err
}

// MoveWidget moves a widget to the end of another area (nil = inactive).
func (s *Store) MoveWidget(ctx context.Context, id int64, areaID *int64) error {
	order, err := s.nextWidgetOrder(ctx, s.Pool, areaID)
	if err != nil {
		return err
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE widgets SET area_id = $2, sort_order = $3, updated_at = now() WHERE id = $1`, id, areaID, order)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrWidgetNotFound
	}
	return err
}

// DeleteWidget removes a widget permanently.
func (s *Store) DeleteWidget(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM widgets WHERE id = $1`, id)
	return err
}

// SetAreaWidgets is the sidebars endpoint's widgets update: the listed widgets move into the area
// (nil = inactive) in that order, and widgets that were in the area but are not listed become inactive.
func (s *Store) SetAreaWidgets(ctx context.Context, areaID *int64, ids []int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if areaID != nil {
		order, err := s.nextWidgetOrder(ctx, tx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE widgets SET area_id = NULL, sort_order = $2 + sub.n, updated_at = now()
			FROM (SELECT id, row_number() OVER (ORDER BY sort_order, id) AS n FROM widgets WHERE area_id = $1 AND NOT (id = ANY($3))) sub
			WHERE widgets.id = sub.id`, *areaID, order, ids); err != nil {
			return err
		}
	}
	for i, id := range ids {
		if _, err := tx.Exec(ctx, `UPDATE widgets SET area_id = $2, sort_order = $3, updated_at = now() WHERE id = $1`, id, areaID, i); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// WidgetAreaIDs maps the area slugs of a theme to their ids.
func (s *Store) WidgetAreaIDs(ctx context.Context, theme string) (map[string]int64, error) {
	rows, err := s.Pool.Query(ctx, `SELECT slug, id FROM widget_areas WHERE theme = $1`, theme)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var slug string
		var id int64
		if err := rows.Scan(&slug, &id); err != nil {
			return nil, err
		}
		out[slug] = id
	}
	return out, rows.Err()
}
