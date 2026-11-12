package server

import (
	"context"
	"encoding/json"
	"html/template"
	"log"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Pursae Custom Fields on Appearance › Menus: the Menu and Menu Item location rules (ACF's
// forms/form-nav-menu.php). Menu fields sit under Menu Settings and are posted as
// menu-acf[field_key]; the fields of an item sit in the item's settings and are posted as
// menu-item-acf[<item id>][field_key]. Values live in menus.meta and menu_items.meta
// (migration 0103) and are read with get_field("name", "nav_menu_<id>") and
// get_field("name", "menu_item_<id>").

// acfgoMenuRuleMatch matches a nav_menu / nav_menu_item rule value: "all", a menu ID, or
// "location/<slug>" for the menu assigned to a theme location.
func acfgoMenuRuleMatch(v, menuID string, locations []string) bool {
	if v == "all" || v == menuID {
		return true
	}
	if loc, ok := strings.CutPrefix(v, "location/"); ok {
		return slices.Contains(locations, loc)
	}
	return false
}

// acfgoMenuMeta loads the meta of a menu ("menus") or a menu item ("menu_items") and the ID and
// theme locations of the menu it belongs to.
func (s *Server) acfgoMenuMeta(ctx context.Context, table string, id int64) (meta map[string]any, menuID int64, locations []string, ok bool) {
	var raw []byte
	q := `SELECT m.meta, m.id FROM menus m WHERE m.id = $1`
	if table == "menu_items" {
		q = `SELECT i.meta, i.menu_id FROM menu_items i WHERE i.id = $1`
	}
	if err := s.store.Pool.QueryRow(ctx, q, id).Scan(&raw, &menuID); err != nil {
		return nil, 0, nil, false
	}
	meta = map[string]any{}
	_ = json.Unmarshal(raw, &meta)
	rows, err := s.store.Pool.Query(ctx, `SELECT location FROM menu_locations WHERE menu_id = $1 ORDER BY location`, menuID)
	if err == nil {
		for rows.Next() {
			var l string
			if rows.Scan(&l) == nil {
				locations = append(locations, l)
			}
		}
		rows.Close()
	}
	return meta, menuID, locations, true
}

// acfgoMenuObject resolves "nav_menu_<id>" (kind "menu") and "menu_item_<id>" (kind "menu_item").
func (s *Server) acfgoMenuObject(ctx context.Context, kind string, id int64) *acfObject {
	table := "menus"
	if kind == "menu_item" {
		table = "menu_items"
	}
	meta, _, _, ok := s.acfgoMenuMeta(ctx, table, id)
	if !ok {
		return nil
	}
	return &acfObject{Kind: kind, ID: id, meta: meta}
}

// acfgoMenuObjectScreen is the location screen of a menu or menu item object.
func (s *Server) acfgoMenuObjectScreen(ctx context.Context, o *acfObject) *acfScreen {
	table := "menus"
	if o.Kind == "menu_item" {
		table = "menu_items"
	}
	_, menuID, locs, ok := s.acfgoMenuMeta(ctx, table, o.ID)
	if !ok {
		return &acfScreen{}
	}
	if o.Kind == "menu_item" {
		return &acfScreen{NavMenuItem: itoa64(menuID), NavMenuLocations: locs}
	}
	return &acfScreen{NavMenu: itoa64(menuID), NavMenuLocations: locs}
}

// acfgoMenuLocationChoices are the values of the Menu and Menu Item rules: all, the theme's
// menu locations and the menus.
func (s *Server) acfgoMenuLocationChoices(ctx context.Context) []acfgoChoice {
	out := []acfgoChoice{{"all", "All", ""}}
	for _, loc := range s.activeTheme().MenuLocations {
		out = append(out, acfgoChoice{"location/" + loc.Slug, loc.Name, "Menu Locations"})
	}
	menus, _ := s.store.Menus(ctx)
	for _, m := range menus {
		out = append(out, acfgoChoice{itoa64(m.ID), m.Name, "Menus"})
	}
	return out
}

// acfgoMenuFieldsHTML renders the groups of a menu screen with inputs named prefix[field_key].
func (s *Server) acfgoMenuFieldsHTML(ctx context.Context, groups []*acfGroup, o *acfObject, prefix string) (string, bool) {
	r := &acfgoRender{s: s, ctx: ctx}
	for _, g := range groups {
		vals := map[string]any{}
		if o != nil {
			for _, f := range g.Fields {
				s.acfgoLoadInto(ctx, o, vals, f, "", false)
				typ := acfgoStr(f["type"])
				if typ == "clone" && acfgoStr(f["display"]) != "group" {
					continue
				}
				if _, stored := s.acfgoGet(o, acfgoStr(f["name"])); !stored && typ != "repeater" && typ != "flexible_content" && typ != "group" {
					delete(vals, acfgoStr(f["name"])) // the default applies
				}
			}
		}
		r.w(`<div class="acf-form-group acf-menu-fields" data-key="%s">`, esc(g.Key))
		if g.Style != "seamless" {
			r.w(`<h4 class="acf-menu-fields-title">%s</h4>`, esc(g.Title))
		}
		r.w(`<div class="acf-fields -%s acf-form-fields">`, esc(defaultStr(g.LabelPlacement, "top")))
		r.fields(g.Fields, prefix, vals, defaultStr(g.LabelPlacement, "top"), g.InstructionPlacement)
		r.w(`</div></div>`)
	}
	return r.sb.String(), r.media
}

// acfgoMenuItemFields is the field markup of one menu item (the wp_nav_menu_item_custom_fields
// spot of Walker_Nav_Menu_Edit), "" when no group targets the items of this menu.
func (s *Server) acfgoMenuItemFields(c *gin.Context, menu *store.Menu, it *store.EditItem) template.HTML {
	if !s.acfgoOn() || menu == nil || it == nil {
		return ""
	}
	ctx := c.Request.Context()
	groups := s.acfgoGroupsFor(ctx, &acfScreen{NavMenuItem: itoa64(menu.ID), NavMenuLocations: menu.Locations, User: currentUser(c)})
	if len(groups) == 0 {
		return ""
	}
	h, media := s.acfgoMenuFieldsHTML(ctx, groups, s.acfgoMenuObject(ctx, "menu_item", it.ID), "menu-item-acf["+itoa64(it.ID)+"]")
	if media {
		s.enqueueMedia(c, 0)
	}
	return template.HTML(`<div class="acf-menu-item-fields description-wide">` + h + `</div>`)
}

// acfgoNewMenuItemFields is acfgoMenuItemFields for an item nav-menu.js just added by ajax.
func (s *Server) acfgoNewMenuItemFields(c *gin.Context, menuID int64, it *store.EditItem) template.HTML {
	if !s.acfgoOn() {
		return ""
	}
	_, _, locs, ok := s.acfgoMenuMeta(c.Request.Context(), "menus", menuID)
	if !ok {
		return ""
	}
	return s.acfgoMenuItemFields(c, &store.Menu{ID: menuID, Locations: locs}, it)
}

// acfgoNavMenus adds the fields to the Menus screen: every item's and the menu's own.
func (s *Server) acfgoNavMenus(c *gin.Context, d *navMenusData) {
	if !s.acfgoOn() || d.Selected == nil {
		return
	}
	ctx := c.Request.Context()
	u := currentUser(c)
	m := d.Selected
	itemGroups := s.acfgoGroupsFor(ctx, &acfScreen{NavMenuItem: itoa64(m.ID), NavMenuLocations: m.Locations, User: u})
	menuGroups := s.acfgoGroupsFor(ctx, &acfScreen{NavMenu: itoa64(m.ID), NavMenuLocations: m.Locations, User: u})
	if len(itemGroups) == 0 && len(menuGroups) == 0 {
		return
	}
	for i := range d.Rows {
		d.Rows[i].ACF = s.acfgoMenuItemFields(c, m, d.Rows[i].Item)
	}
	if len(menuGroups) > 0 {
		h, media := s.acfgoMenuFieldsHTML(ctx, menuGroups, s.acfgoMenuObject(ctx, "menu", m.ID), "menu-acf")
		if media {
			s.enqueueMedia(c, 0)
		}
		d.ACFMenu = template.HTML(`<div class="acf-menu-settings">` + h + `</div>`)
	}
	// The (empty) placement element carries the nonces of the ajax searches; new items get
	// their fields initialised when nav-menu.js adds them.
	addBodyHTML(c, template.HTML(`<div id="acfgo-boxes" style="display:none" data-kind="menu" data-form="" data-before="" data-nonce="`+
		esc(s.nonce(c, "acfgo-save"))+`" data-validate="`+esc(s.nonce(c, "acfgo-validate"))+`"></div>`))
	a := s.pageAssets(c)
	a.Script("acfgo-input").Style("acfgo")
	a.AddInline("acfgo-input", `jQuery( document ).on( 'menu-item-added', function ( e, $item ) { jQuery( $item ).find( '.acf-form-group' ).each( function () { window.acfgoInit( this ); } ); } );`)
}

// acfgoMenuInput reads the posted menu fields: the menu's own and the items' by item ID.
func acfgoMenuInput(c *gin.Context) (menu map[string]any, items map[int64]map[string]any) {
	form := c.Request.PostForm
	menu = acfgoParseForm(form, "menu-acf")
	items = map[int64]map[string]any{}
	for k, v := range acfgoParseForm(form, "menu-item-acf") {
		id, err := strconv.ParseInt(k, 10, 64)
		if in, ok := v.(map[string]any); ok && err == nil && len(in) > 0 {
			items[id] = in
		}
	}
	return menu, items
}

// acfgoNavMenusValidate runs before the menu is saved: input that fails the fields' rules ends
// the request and nothing is stored (acf_validate_save_post()).
func (s *Server) acfgoNavMenusValidate(c *gin.Context) bool {
	if !s.acfgoOn() {
		return true
	}
	ctx := c.Request.Context()
	menu, items := acfgoMenuInput(c)
	errs := s.acfgoValidate(ctx, menu)
	for _, in := range items {
		errs = append(errs, s.acfgoValidate(ctx, in)...)
	}
	if len(errs) == 0 {
		return true
	}
	s.acfgoValidationDie(c, errs)
	return false
}

// acfgoNavMenusSave stores the posted fields after the menu was saved. Only items that belong
// to the saved menu are written, and only fields of a group that targets this screen.
func (s *Server) acfgoNavMenusSave(c *gin.Context, menuID int64) {
	if !s.acfgoOn() {
		return
	}
	menu, items := acfgoMenuInput(c)
	if len(menu) == 0 && len(items) == 0 {
		return
	}
	ctx := c.Request.Context()
	u := currentUser(c)
	unfiltered := u.Can("unfiltered_html", nil)
	editorCtx := acfgoWithEditor(ctx, u)
	_, _, locs, ok := s.acfgoMenuMeta(ctx, "menus", menuID)
	if !ok {
		return
	}
	allowed := func(sc *acfScreen, in map[string]any) map[string]any {
		keys := map[string]bool{}
		for _, g := range s.acfgoGroupsFor(ctx, sc) {
			for _, f := range g.Fields {
				keys[acfgoStr(f["key"])] = true
			}
		}
		out := map[string]any{}
		for k, v := range in {
			if keys[k] {
				out[k] = v
			}
		}
		return out
	}
	if in := allowed(&acfScreen{NavMenu: itoa64(menuID), NavMenuLocations: locs, User: u}, menu); len(in) > 0 {
		if o := s.acfgoMenuObject(ctx, "menu", menuID); o != nil {
			if err := s.acfgoSaveInput(editorCtx, o, in, unfiltered); err != nil {
				log.Printf("acf-go: save menu %d: %v", menuID, err)
			}
		}
	}
	itemScreen := &acfScreen{NavMenuItem: itoa64(menuID), NavMenuLocations: locs, User: u}
	for id, raw := range items {
		_, owner, _, ok := s.acfgoMenuMeta(ctx, "menu_items", id)
		if !ok || owner != menuID {
			continue
		}
		in := allowed(itemScreen, raw)
		if len(in) == 0 {
			continue
		}
		if o := s.acfgoMenuObject(ctx, "menu_item", id); o != nil {
			if err := s.acfgoSaveInput(editorCtx, o, in, unfiltered); err != nil {
				log.Printf("acf-go: save menu item %d: %v", id, err)
			}
		}
	}
}
