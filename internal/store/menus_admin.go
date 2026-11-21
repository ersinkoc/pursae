package store

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/wp"
)

// Menu is a navigation menu (a nav_menu term in WordPress).
type Menu struct {
	ID   int64
	Slug string
	Name string
	// Locations are the theme locations the menu is assigned to, sorted (a menu may serve several).
	Locations []string
	AutoAdd   bool
	Count     int
}

// HasLocation reports whether the menu is assigned to the theme location loc.
func (m *Menu) HasLocation(loc string) bool {
	return slices.Contains(m.Locations, loc)
}

// EditItem is a menu item as the Menus screen edits it (wp_setup_nav_menu_item()).
type EditItem struct {
	ID, MenuID, ParentID, TargetID int64
	Kind, Object, Status           string
	URL, Label, Target, Classes    string
	AttrTitle, Description, XFN    string
	Order                          int
}

// Menus lists all menus by name.
func (s *Store) Menus(ctx context.Context) ([]*Menu, error) {
	rows, err := s.Pool.Query(ctx, `SELECT m.id, m.slug, m.name,
		coalesce((SELECT array_agg(l.location ORDER BY l.location) FROM menu_locations l WHERE l.menu_id = m.id), '{}'), m.auto_add,
		(SELECT count(*) FROM menu_items i WHERE i.menu_id = m.id AND i.status = 'publish') FROM menus m ORDER BY lower(m.name), m.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Menu
	for rows.Next() {
		m := &Menu{}
		if err := rows.Scan(&m.ID, &m.Slug, &m.Name, &m.Locations, &m.AutoAdd, &m.Count); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CreateMenu is wp_create_nav_menu(); the slug is made unique.
func (s *Store) CreateMenu(ctx context.Context, name string) (int64, error) {
	return createMenu(ctx, s.Pool, name)
}

func createMenu(ctx context.Context, q DBTX, name string) (int64, error) {
	base := wp.SanitizeTitle(name)
	if base == "" {
		base = "menu"
	}
	slug := base
	for i := 2; ; i++ {
		var taken bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM menus WHERE slug = $1)`, slug).Scan(&taken); err != nil {
			return 0, err
		}
		if !taken {
			break
		}
		slug = base + "-" + strconv.Itoa(i)
	}
	var id int64
	err := q.QueryRow(ctx, `INSERT INTO menus(slug, name) VALUES ($1, $2) RETURNING id`, slug, name).Scan(&id)
	return id, err
}

func (s *Store) UpdateMenu(ctx context.Context, id int64, name string, autoAdd bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE menus SET name = $2, auto_add = $3 WHERE id = $1`, id, name, autoAdd)
	return err
}

func (s *Store) DeleteMenu(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM menus WHERE id = $1`, id)
	return err
}

// SetMenuLocations assigns theme locations: location -> menu id (0 unassigns). Each location holds one
// menu; one menu may hold several locations (nav_menu_locations).
func (s *Store) SetMenuLocations(ctx context.Context, locs map[string]int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		for loc, id := range locs {
			if err := setMenuLocation(ctx, tx, loc, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func setMenuLocation(ctx context.Context, q DBTX, location string, id int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM menu_locations WHERE location = $1`, location); err != nil {
		return err
	}
	// An ID that names no menu leaves the location unassigned, like 0.
	if id > 0 {
		_, err := q.Exec(ctx, `INSERT INTO menu_locations (location, menu_id)
			SELECT $1, id FROM menus WHERE id = $2`, location, id)
		return err
	}
	return nil
}

// MenuTreeItem describes a menu item and its children before the database assigns their IDs.
type MenuTreeItem struct {
	Item     EditItem
	Children []MenuTreeItem
}

// ReplaceNamedMenu saves an imported menu and its location in one transaction.
// It returns the menu ID and the item IDs in depth-first order.
func (s *Store) ReplaceNamedMenu(ctx context.Context, name, location string, items []MenuTreeItem) (int64, []int64, error) {
	var id int64
	var ids []int64
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := lockSlug(ctx, tx, "menu", "", name); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT id FROM menus WHERE name = $1 ORDER BY id LIMIT 1 FOR UPDATE`, name).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			id, err = createMenu(ctx, tx, name)
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE menu_items SET parent_id = NULL WHERE menu_id <> $1
			AND parent_id IN (SELECT id FROM menu_items WHERE menu_id = $1)`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM menu_items WHERE menu_id = $1`, id); err != nil {
			return err
		}
		var insert func([]MenuTreeItem, int64) error
		insert = func(nodes []MenuTreeItem, parent int64) error {
			for _, node := range nodes {
				item := node.Item
				item.MenuID, item.ParentID, item.Order = id, parent, len(ids)+1
				if err := addMenuItem(ctx, tx, &item); err != nil {
					return err
				}
				ids = append(ids, item.ID)
				if err := insert(node.Children, item.ID); err != nil {
					return err
				}
			}
			return nil
		}
		if err := insert(items, 0); err != nil {
			return err
		}
		return setMenuLocation(ctx, tx, location, id)
	})
	if err != nil {
		return 0, nil, err
	}
	return id, ids, nil
}

const editItemCols = `id, menu_id, coalesce(parent_id,0), coalesce(target_id,0), kind, object, status, url, label, target,
	css_classes, attr_title, description, xfn, sort_order`

func scanEditItem(row pgx.Row) (*EditItem, error) {
	it := &EditItem{}
	err := row.Scan(&it.ID, &it.MenuID, &it.ParentID, &it.TargetID, &it.Kind, &it.Object, &it.Status, &it.URL, &it.Label,
		&it.Target, &it.Classes, &it.AttrTitle, &it.Description, &it.XFN, &it.Order)
	return it, err
}

// MenuItems returns a menu's saved items in order (flat, parents before children).
func (s *Store) MenuItems(ctx context.Context, menuID int64) ([]*EditItem, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+editItemCols+` FROM menu_items WHERE menu_id = $1 AND status = 'publish' ORDER BY sort_order, id`, menuID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*EditItem
	for rows.Next() {
		it, err := scanEditItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) MenuItem(ctx context.Context, id int64) (*EditItem, error) {
	return scanEditItem(s.Pool.QueryRow(ctx, `SELECT `+editItemCols+` FROM menu_items WHERE id = $1`, id))
}

// AddMenuItem inserts an item (status draft for items added on the Menus screen until it is saved).
func (s *Store) AddMenuItem(ctx context.Context, it *EditItem) error {
	return addMenuItem(ctx, s.Pool, it)
}

func addMenuItem(ctx context.Context, q DBTX, it *EditItem) error {
	return q.QueryRow(ctx, `INSERT INTO menu_items(menu_id, parent_id, sort_order, kind, object, target_id, url, label, target,
		css_classes, attr_title, description, xfn, status) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id`,
		it.MenuID, nullable(it.ParentID), it.Order, it.Kind, it.Object, nullable(it.TargetID), it.URL, it.Label, it.Target,
		it.Classes, it.AttrTitle, it.Description, it.XFN, defaultStatus(it.Status)).Scan(&it.ID)
}

func defaultStatus(v string) string {
	if v == "" {
		return "publish"
	}
	return v
}

// DeleteOrphanedDraftMenuItems deletes menu items added more than a day ago that were never saved into
// their menu (still draft), like WordPress' _wp_delete_orphaned_draft_menu_items() on nav-menus load.
func (s *Store) DeleteOrphanedDraftMenuItems(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM menu_items WHERE status = 'draft' AND created_at < now() - interval '1 day'`)
	return err
}

// AllMenuItems returns a menu's items of every status in order (the REST menu-items collection).
func (s *Store) AllMenuItems(ctx context.Context, menuID int64) ([]*EditItem, error) {
	q := `SELECT ` + editItemCols + ` FROM menu_items ORDER BY menu_id, sort_order, id`
	args := []any{}
	if menuID > 0 {
		q = `SELECT ` + editItemCols + ` FROM menu_items WHERE menu_id = $1 ORDER BY sort_order, id`
		args = append(args, menuID)
	}
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*EditItem
	for rows.Next() {
		it, err := scanEditItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// UpdateMenuItem saves every editable field of one item (wp_update_nav_menu_item()).
func (s *Store) UpdateMenuItem(ctx context.Context, it *EditItem) error {
	_, err := s.Pool.Exec(ctx, `UPDATE menu_items SET menu_id = $2, parent_id = $3, sort_order = $4, kind = $5, object = $6, target_id = $7,
		url = $8, label = $9, target = $10, css_classes = $11, attr_title = $12, description = $13, xfn = $14, status = $15 WHERE id = $1`,
		it.ID, it.MenuID, nullable(it.ParentID), it.Order, it.Kind, it.Object, nullable(it.TargetID), it.URL, it.Label, it.Target,
		it.Classes, it.AttrTitle, it.Description, it.XFN, defaultStatus(it.Status))
	return err
}

// DeleteMenuItem removes one item; its children are re-parented to the top level like wp_delete_post() does for menu items.
func (s *Store) DeleteMenuItem(ctx context.Context, id int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE menu_items SET parent_id = NULL WHERE parent_id = $1`, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM menu_items WHERE id = $1`, id)
		return err
	})
}

// SaveMenuItems is wp_nav_menu_update_menu_items(): the posted items are published in order and the
// menu's other items are deleted.
func (s *Store) SaveMenuItems(ctx context.Context, menuID int64, items []*EditItem) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		keep := []int64{}
		for _, it := range items {
			keep = append(keep, it.ID)
		}
		if _, err := tx.Exec(ctx, `UPDATE menu_items SET parent_id = NULL WHERE menu_id = $1`, menuID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM menu_items WHERE menu_id = $1 AND NOT (id = ANY($2))`, menuID, keep); err != nil {
			return err
		}
		for _, it := range items {
			if _, err := tx.Exec(ctx, `UPDATE menu_items SET menu_id = $2, parent_id = $3, sort_order = $4, url = $5, label = $6, target = $7,
				css_classes = $8, attr_title = $9, description = $10, xfn = $11, status = 'publish' WHERE id = $1`,
				it.ID, menuID, nullable(it.ParentID), it.Order, it.URL, it.Label, it.Target, it.Classes, it.AttrTitle, it.Description, it.XFN); err != nil {
				return err
			}
		}
		return nil
	})
}
