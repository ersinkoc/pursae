package store

import (
	"context"
	"encoding/json"
)

type Widget struct {
	ID       int64
	AreaSlug string
	Type     string
	Settings map[string]any
}

// WidgetsByArea returns active widgets of a theme grouped by widget area slug, in order.
func (s *Store) WidgetsByArea(ctx context.Context, theme string) (map[string][]*Widget, error) {
	rows, err := s.Pool.Query(ctx, `SELECT w.id, a.slug, w.type, w.settings FROM widgets w
		JOIN widget_areas a ON a.id = w.area_id WHERE a.theme = $1 ORDER BY a.slug, w.sort_order, w.id`, theme)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]*Widget{}
	for rows.Next() {
		w := &Widget{}
		var raw []byte
		if err := rows.Scan(&w.ID, &w.AreaSlug, &w.Type, &raw); err != nil {
			return nil, err
		}
		w.Settings = map[string]any{}
		_ = json.Unmarshal(raw, &w.Settings)
		out[w.AreaSlug] = append(out[w.AreaSlug], w)
	}
	return out, rows.Err()
}

// EnsureWidgetArea creates a widget area if missing and returns its id.
func (s *Store) EnsureWidgetArea(ctx context.Context, theme, slug, name string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `INSERT INTO widget_areas(theme, slug, name) VALUES ($1,$2,$3)
		ON CONFLICT (theme, slug) DO UPDATE SET name = EXCLUDED.name RETURNING id`, theme, slug, name).Scan(&id)
	return id, err
}

func (s *Store) AddWidget(ctx context.Context, areaID int64, typ string, order int, settings map[string]any) error {
	raw, _ := json.Marshal(orEmpty(settings))
	_, err := s.Pool.Exec(ctx, `INSERT INTO widgets(area_id, type, sort_order, settings) VALUES ($1,$2,$3,$4)`, areaID, typ, order, raw)
	return err
}

// MenuItem is one entry of a navigation menu with its resolved URL.
type MenuItem struct {
	ID       int64
	ParentID int64
	Kind     string
	Object   string
	TargetID int64
	URL      string
	Label    string
	Target   string
	Classes  string
	Children []*MenuItem
	// Current and CurrentAncestor are set by the core for the page being viewed (current-menu-item).
	Current, CurrentAncestor bool
}

// MenuAt returns the items of the menu assigned to a theme location, nested; nil when none is assigned.
func (s *Store) MenuAt(ctx context.Context, location string) ([]*MenuItem, error) {
	rows, err := s.Pool.Query(ctx, `SELECT i.id, coalesce(i.parent_id,0), i.kind, i.object, coalesce(i.target_id,0),
		i.url, i.label, i.target, i.css_classes FROM menu_items i JOIN menu_locations ml ON ml.menu_id = i.menu_id
		WHERE ml.location = $1 AND i.status = 'publish' ORDER BY i.sort_order, i.id`, location)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var flat []*MenuItem
	for rows.Next() {
		it := &MenuItem{}
		if err := rows.Scan(&it.ID, &it.ParentID, &it.Kind, &it.Object, &it.TargetID, &it.URL, &it.Label, &it.Target, &it.Classes); err != nil {
			return nil, err
		}
		flat = append(flat, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	byID := map[int64]*MenuItem{}
	for _, it := range flat {
		byID[it.ID] = it
	}
	var roots []*MenuItem
	for _, it := range flat {
		if p, ok := byID[it.ParentID]; ok {
			p.Children = append(p.Children, it)
		} else {
			roots = append(roots, it)
		}
	}
	return roots, nil
}
