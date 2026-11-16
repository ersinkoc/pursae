package server

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/cli"
	"wpgo/internal/store"
)

// `menu`, `menu item` and `menu location`: navigation menus as the Menus screen and the
// /wp/v2/menus REST routes edit them. Every write ends with a whole-site page cache purge, as the
// admin does (menus appear on every page).

var menuListFields = []string{"term_id", "name", "slug", "locations", "count"}

var menuItemFields = []string{"db_id", "type", "title", "link", "position"}

// menuItemFlags are the item fields shared by the add-* commands and `menu item update`.
var menuItemFlags = []cli.Flag{
	{Name: "title", Usage: "Navigation label (add-post/add-term default: the post or term title)."},
	{Name: "description", Usage: "Item description."},
	{Name: "attr-title", Usage: "Title attribute of the link."},
	{Name: "target", Usage: "Link target: _blank opens a new tab, empty the same tab."},
	{Name: "classes", Usage: "CSS classes, space-separated."},
	{Name: "position", Usage: "Position (menu order) in the menu; default: last."},
	{Name: "parent-id", Usage: "Parent menu item ID (db_id) in the same menu; 0 for the top level."},
}

func registerMenuCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("menu", "List, create and delete navigation menus.")
	r.Noun("menu item", "List, add, update and delete menu items.")
	r.Noun("menu location", "Assign menus to theme locations.")

	r.Add(cli.Command{
		Path:     "menu list",
		Short:    "List navigation menus.",
		Long:     "Fields: term_id, name, slug, locations, count, auto_add.",
		Examples: []string{"menu list", "menu list --format=json"},
		Run: func(c *cli.Ctx) error {
			st, err := e.Store(c.Context)
			if err != nil {
				return err
			}
			menus, err := st.Menus(c.Context)
			if err != nil {
				return err
			}
			rows := make([]cli.Row, 0, len(menus))
			for _, m := range menus {
				rows = append(rows, cli.Row{"term_id": m.ID, "name": m.Name, "slug": m.Slug, "locations": orEmptyStrings(m.Locations),
					"count": m.Count, "auto_add": m.AutoAdd})
			}
			return c.Print(rows, cli.View{Fields: menuListFields, ID: "term_id"})
		},
	})

	r.Add(cli.Command{
		Path:     "menu create",
		Short:    "Create a navigation menu.",
		Args:     []cli.Arg{{Name: "menu-name", Usage: "Name of the new menu."}},
		Flags:    []cli.Flag{{Name: "porcelain", Bool: true, Usage: "Print only the new menu ID."}},
		Examples: []string{`menu create "Main menu"`, `menu create Footer --porcelain`},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			name := strings.TrimSpace(c.Arg(0))
			if name == "" {
				return cli.Usagef("the menu name is empty")
			}
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			menus, err := s.store.Menus(ctx)
			if err != nil {
				return err
			}
			for _, m := range menus {
				if strings.EqualFold(m.Name, name) {
					return cli.Errorf("the menu name %q conflicts with another menu (ID %d)", name, m.ID)
				}
			}
			id, err := s.store.CreateMenu(ctx, name)
			if err != nil {
				return err
			}
			s.purgeAll(ctx)
			if c.Bool("porcelain") {
				c.Println(id)
				return nil
			}
			c.Success("Created menu %d.", id)
			return nil
		},
	})

	r.Add(cli.Command{
		Path:     "menu delete",
		Short:    "Delete navigation menus and their items.",
		Long:     "Deletes the menus, their items and their location assignments. Asks for confirmation (--yes skips it).",
		Args:     []cli.Arg{{Name: "menu", Usage: "Menu ID, slug or name.", Repeat: true}},
		Examples: []string{"menu delete 3", "menu delete footer --yes"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			var list []*store.Menu
			for _, ref := range c.Args {
				m, err := cliMenu(ctx, s.store, ref)
				if err != nil {
					return err
				}
				list = append(list, m)
			}
			if err := c.Confirm("Delete %d menu(s) and their items?", len(list)); err != nil {
				return err
			}
			for _, m := range list {
				if err := s.store.DeleteMenu(ctx, m.ID); err != nil {
					return err
				}
				c.Log("Deleted menu %d (%s).", m.ID, m.Name)
			}
			s.purgeAll(ctx)
			c.Success("Deleted %d menu(s).", len(list))
			return nil
		},
	})

	// --- items ---

	r.Add(cli.Command{
		Path:  "menu item list",
		Short: "List the items of a menu.",
		Long: `Lists the menu's saved items in order. Fields: db_id, type, title, link, position,
menu_item_parent, object, object_id, target, classes, attr_title, description.`,
		Args:     []cli.Arg{{Name: "menu", Usage: "Menu ID, slug or name."}},
		Examples: []string{"menu item list main-menu", "menu item list 3 --fields=db_id,title,link --format=csv"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			m, err := cliMenu(ctx, s.store, c.Arg(0))
			if err != nil {
				return err
			}
			items, err := s.store.MenuItems(ctx, m.ID)
			if err != nil {
				return err
			}
			rows := make([]cli.Row, 0, len(items))
			for _, it := range items {
				rows = append(rows, s.cliMenuItemRow(ctx, it))
			}
			return c.Print(rows, cli.View{Fields: menuItemFields, ID: "db_id"})
		},
	})

	r.Add(cli.Command{
		Path:  "menu item add-post",
		Short: "Add a post (or page, or other content) to a menu.",
		Args:  []cli.Arg{{Name: "menu", Usage: "Menu ID, slug or name."}, {Name: "post-id", Usage: "Post ID."}},
		Flags: append(append([]cli.Flag{}, menuItemFlags...), cli.Flag{Name: "porcelain", Bool: true, Usage: "Print only the new item ID."}),
		Examples: []string{
			"menu item add-post main-menu 12",
			"menu item add-post main-menu 14 --title='About us' --parent-id=31",
		},
		Run: func(c *cli.Ctx) error {
			postID, err := c.ArgInt64(1)
			if err != nil {
				return err
			}
			return cliMenuItemAdd(c, e, &store.EditItem{Kind: "post_type", TargetID: postID})
		},
	})

	r.Add(cli.Command{
		Path:  "menu item add-term",
		Short: "Add a taxonomy term (category, tag …) to a menu.",
		Args:  []cli.Arg{{Name: "menu", Usage: "Menu ID, slug or name."}, {Name: "taxonomy", Usage: "Taxonomy of the term (category, post_tag …)."}, {Name: "term-id", Usage: "Term ID."}},
		Flags: append(append([]cli.Flag{}, menuItemFlags...), cli.Flag{Name: "porcelain", Bool: true, Usage: "Print only the new item ID."}),
		Examples: []string{
			"menu item add-term main-menu category 5",
		},
		Run: func(c *cli.Ctx) error {
			termID, err := c.ArgInt64(2)
			if err != nil {
				return err
			}
			return cliMenuItemAdd(c, e, &store.EditItem{Kind: "taxonomy", Object: c.Arg(1), TargetID: termID})
		},
	})

	r.Add(cli.Command{
		Path:  "menu item add-custom",
		Short: "Add a custom link to a menu.",
		Args:  []cli.Arg{{Name: "menu", Usage: "Menu ID, slug or name."}, {Name: "title", Usage: "Navigation label."}, {Name: "link", Usage: "URL (http, https, mailto, tel or relative)."}},
		Flags: append(append([]cli.Flag{}, menuItemFlags[1:]...), cli.Flag{Name: "porcelain", Bool: true, Usage: "Print only the new item ID."}),
		Examples: []string{
			"menu item add-custom main-menu Shop https://shop.example.com --target=_blank",
		},
		Run: func(c *cli.Ctx) error {
			return cliMenuItemAdd(c, e, &store.EditItem{Kind: "custom", Object: "custom", Label: c.Arg(1), URL: c.Arg(2)})
		},
	})

	r.Add(cli.Command{
		Path:  "menu item update",
		Short: "Update a menu item.",
		Long:  "Changes only the given fields. --link applies to custom links only.",
		Args:  []cli.Arg{{Name: "db-id", Usage: "Menu item ID."}},
		Flags: append(append([]cli.Flag{}, menuItemFlags...), cli.Flag{Name: "link", Usage: "URL of a custom link."}),
		Examples: []string{
			"menu item update 31 --title='Contact'",
			"menu item update 32 --position=1 --parent-id=0",
		},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			id, err := c.ArgInt64(0)
			if err != nil {
				return err
			}
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			it, err := s.store.MenuItem(ctx, id)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return cli.Errorf("invalid menu item ID: %d", id)
				}
				return err
			}
			if v, ok := c.Lookup("link"); ok {
				if it.Kind != "custom" {
					return cli.Errorf("--link applies to custom links only (item %d is a %s item)", id, it.Kind)
				}
				it.URL = strings.TrimSpace(v)
			}
			if err := applyMenuItemFlags(c, it); err != nil {
				return err
			}
			if err := s.cliCheckMenuItem(ctx, it); err != nil {
				return err
			}
			if err := s.store.UpdateMenuItem(ctx, it); err != nil {
				return err
			}
			s.purgeAll(ctx)
			c.Success("Updated menu item %d.", it.ID)
			return nil
		},
	})

	r.Add(cli.Command{
		Path:     "menu item delete",
		Short:    "Delete menu items.",
		Long:     "Children of a deleted item move to the top level. Asks for confirmation (--yes skips it).",
		Args:     []cli.Arg{{Name: "db-id", Usage: "Menu item ID.", Repeat: true}},
		Examples: []string{"menu item delete 31 32 --yes"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			ids, err := c.ArgIDs(0)
			if err != nil {
				return err
			}
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			for _, id := range ids {
				if _, err := s.store.MenuItem(ctx, id); err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						return cli.Errorf("invalid menu item ID: %d", id)
					}
					return err
				}
			}
			if err := c.Confirm("Delete %d menu item(s)?", len(ids)); err != nil {
				return err
			}
			for _, id := range ids {
				if err := s.store.DeleteMenuItem(ctx, id); err != nil {
					return err
				}
			}
			s.purgeAll(ctx)
			c.Success("Deleted %d menu item(s).", len(ids))
			return nil
		},
	})

	// --- locations ---

	r.Add(cli.Command{
		Path:  "menu location list",
		Short: "List the assigned theme menu locations.",
		Long: `Lists the theme locations that have a menu assigned (location, menu, term_id, slug).
Locations the active theme declares but that hold no menu are not listed: they come from the
theme's templates, which the command line does not load.`,
		Examples: []string{"menu location list", "menu location list --format=json"},
		Run: func(c *cli.Ctx) error {
			st, err := e.Store(c.Context)
			if err != nil {
				return err
			}
			menus, err := st.Menus(c.Context)
			if err != nil {
				return err
			}
			var rows []cli.Row
			for _, m := range menus {
				for _, loc := range m.Locations {
					rows = append(rows, cli.Row{"location": loc, "menu": m.Name, "term_id": m.ID, "slug": m.Slug})
				}
			}
			return c.Print(rows, cli.View{Fields: []string{"location", "menu", "term_id"}, ID: "location"})
		},
	})

	r.Add(cli.Command{
		Path:     "menu location assign",
		Short:    "Assign a menu to a theme location.",
		Long:     "A location holds one menu: a menu already assigned to it is replaced.",
		Args:     []cli.Arg{{Name: "menu", Usage: "Menu ID, slug or name."}, {Name: "location", Usage: "Theme location (e.g. primary)."}},
		Examples: []string{"menu location assign main-menu primary"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			loc := strings.TrimSpace(c.Arg(1))
			if loc == "" {
				return cli.Usagef("the location is empty")
			}
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			m, err := cliMenu(ctx, s.store, c.Arg(0))
			if err != nil {
				return err
			}
			if err := s.store.SetMenuLocations(ctx, map[string]int64{loc: m.ID}); err != nil {
				return err
			}
			s.purgeAll(ctx)
			c.Success("Assigned menu %q to location %q.", m.Name, loc)
			return nil
		},
	})

	r.Add(cli.Command{
		Path:     "menu location remove",
		Short:    "Remove a menu from a theme location.",
		Args:     []cli.Arg{{Name: "menu", Usage: "Menu ID, slug or name."}, {Name: "location", Usage: "Theme location."}},
		Examples: []string{"menu location remove main-menu primary"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			loc := strings.TrimSpace(c.Arg(1))
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			m, err := cliMenu(ctx, s.store, c.Arg(0))
			if err != nil {
				return err
			}
			if !m.HasLocation(loc) {
				return cli.Errorf("menu %q is not assigned to location %q", m.Name, loc)
			}
			if err := s.store.SetMenuLocations(ctx, map[string]int64{loc: 0}); err != nil {
				return err
			}
			s.purgeAll(ctx)
			c.Success("Removed menu %q from location %q.", m.Name, loc)
			return nil
		},
	})
}

func orEmptyStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// cliMenu finds a menu by ID, slug or name (case-insensitive).
func cliMenu(ctx context.Context, st *store.Store, ref string) (*store.Menu, error) {
	ref = strings.TrimSpace(ref)
	menus, err := st.Menus(ctx)
	if err != nil {
		return nil, err
	}
	if m := findMenu(menus, ref); m != nil {
		return m, nil
	}
	return nil, cli.Errorf("invalid menu ID, slug or name: '%s'", ref)
}

// findMenu picks the menu ref names: an ID first, then a slug, then a name.
func findMenu(menus []*store.Menu, ref string) *store.Menu {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		for _, m := range menus {
			if m.ID == id {
				return m
			}
		}
	}
	for _, m := range menus {
		if m.Slug == ref {
			return m
		}
	}
	for _, m := range menus {
		if strings.EqualFold(m.Name, ref) {
			return m
		}
	}
	return nil
}

// cliMenuItemRow is a menu item with WP-CLI's field names.
func (s *Server) cliMenuItemRow(ctx context.Context, it *store.EditItem) cli.Row {
	return cli.Row{
		"db_id":            it.ID,
		"type":             defaultStr(it.Kind, "custom"),
		"title":            it.Label,
		"link":             s.cliMenuItemURL(ctx, it),
		"position":         it.Order,
		"menu_item_parent": it.ParentID,
		"object":           defaultStr(it.Object, "custom"),
		"object_id":        it.TargetID,
		"target":           it.Target,
		"classes":          it.Classes,
		"attr_title":       it.AttrTitle,
		"description":      it.Description,
	}
}

// cliMenuItemURL is menuItemURL (rest_menus.go) without a request.
func (s *Server) cliMenuItemURL(ctx context.Context, it *store.EditItem) string {
	lb := s.links(ctx)
	switch it.Kind {
	case "post_type":
		if p, err := s.store.GetPost(ctx, it.TargetID); err == nil {
			return lb.Permalink(p)
		}
	case "taxonomy":
		if t, err := s.store.GetTerm(ctx, it.TargetID); err == nil {
			return lb.TermLink(t)
		}
	}
	return it.URL
}

// applyMenuItemFlags copies the given item flags onto it.
func applyMenuItemFlags(c *cli.Ctx, it *store.EditItem) error {
	for flag, dst := range map[string]*string{"title": &it.Label, "description": &it.Description, "attr-title": &it.AttrTitle,
		"target": &it.Target, "classes": &it.Classes} {
		if v, ok := c.Lookup(flag); ok {
			*dst = strings.TrimSpace(v)
		}
	}
	if c.Has("position") {
		n, err := c.Int("position", 0)
		if err != nil {
			return err
		}
		if n < 1 {
			return cli.Usagef("--position must be 1 or more")
		}
		it.Order = n
	}
	if c.Has("parent-id") {
		n, err := c.Int64("parent-id", 0)
		if err != nil {
			return err
		}
		if n < 0 {
			return cli.Usagef("--parent-id must be a menu item ID or 0")
		}
		it.ParentID = n
	}
	return nil
}

// validMenuItemURL is the REST check for custom link URLs: http, https, mailto, tel or relative.
func validMenuItemURL(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "", "http", "https", "mailto", "tel":
		return true
	}
	return false
}

// cliCheckMenuItem validates an item like the REST prepareMenuItem and fills the label and
// object of post and term items.
func (s *Server) cliCheckMenuItem(ctx context.Context, it *store.EditItem) error {
	if it.Target != "" && it.Target != "_blank" {
		return cli.Usagef("--target must be _blank or empty")
	}
	switch it.Kind {
	case "custom":
		if strings.TrimSpace(it.Label) == "" {
			return cli.Usagef("the title is required for a custom link")
		}
		if !validMenuItemURL(it.URL) {
			return cli.Errorf("invalid URL: %q", it.URL)
		}
		it.Object = "custom"
	case "post_type":
		p, err := s.store.GetPost(ctx, it.TargetID)
		if err != nil || p.Type == "revision" || (it.Object != "" && p.Type != it.Object) {
			return cli.Errorf("invalid post ID: %d", it.TargetID)
		}
		it.Object = p.Type
		if it.Label == "" {
			it.Label = p.Title
		}
	case "taxonomy":
		t, err := s.store.GetTerm(ctx, it.TargetID)
		if err != nil || (it.Object != "" && t.Taxonomy != it.Object) {
			return cli.Errorf("invalid term ID: %d (taxonomy %s)", it.TargetID, it.Object)
		}
		it.Object = t.Taxonomy
		if it.Label == "" {
			it.Label = t.Name
		}
	}
	if it.ParentID != 0 {
		parent, err := s.store.MenuItem(ctx, it.ParentID)
		if err != nil || parent.MenuID != it.MenuID || parent.ID == it.ID {
			return cli.Errorf("invalid parent menu item ID: %d", it.ParentID)
		}
	}
	return nil
}

// cliMenuItemAdd adds it (kind and target set) to the menu in argument 0.
func cliMenuItemAdd(c *cli.Ctx, e *cliEnv, it *store.EditItem) error {
	ctx := c.Context
	s, err := e.Server(ctx)
	if err != nil {
		return err
	}
	m, err := cliMenu(ctx, s.store, c.Arg(0))
	if err != nil {
		return err
	}
	it.MenuID, it.Status = m.ID, "publish"
	if err := applyMenuItemFlags(c, it); err != nil {
		return err
	}
	if err := s.cliCheckMenuItem(ctx, it); err != nil {
		return err
	}
	if it.Order == 0 { // last, like _wp_auto_add_pages_to_menu()
		if err := s.store.Pool.QueryRow(ctx, `SELECT coalesce(max(sort_order), 0) + 1 FROM menu_items WHERE menu_id = $1`, m.ID).Scan(&it.Order); err != nil {
			return err
		}
	}
	if err := s.store.AddMenuItem(ctx, it); err != nil {
		return err
	}
	s.purgeAll(ctx)
	if c.Bool("porcelain") {
		c.Println(it.ID)
		return nil
	}
	c.Success("Menu item %d added to %q.", it.ID, m.Name)
	return nil
}
