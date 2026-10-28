package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"wpgo/internal/wp"
)

// ACF › Post Types, Taxonomies and Options Pages (ACF 6.1/6.2) are stored like field groups: posts
// of type acf-post-type, acf-taxonomy and acf-ui-options-page whose content is the item in ACF's
// export format. Active post types and taxonomies are registered as content_types / taxonomies
// rows (the core's registry) and in acfgoRegistry, which the core's screens consult for types they
// do not know (labelsFor, taxLabelsFor).

// acfgoItem is a stored post type, taxonomy or options page.
type acfgoItem struct {
	ID     int64
	Type   string // acf-post-type, acf-taxonomy, acf-ui-options-page
	Status string
	Data   map[string]any
}

func (it *acfgoItem) Key() string   { return acfgoStr(it.Data["key"]) }
func (it *acfgoItem) Title() string { return acfgoStr(it.Data["title"]) }
func (it *acfgoItem) Active() bool  { return it.Status == "publish" }

// label reads labels.<name>.
func acfgoLabel(data map[string]any, name string) string {
	if l, ok := data["labels"].(map[string]any); ok {
		return acfgoStr(l[name])
	}
	return ""
}

// acfgoPostTypeDef is the part of an ACF post type the server uses.
type acfgoPostTypeDef struct {
	Name, MenuIcon       string
	Labels               map[string]string
	MenuPosition         float64
	Public, Hierarchical bool
	ShowUI, ShowInREST   bool
	RestBase             string
	Supports             []string
	Taxonomies           []string
	HasArchive           bool
	ArchiveSlug, Slug    string
}

func (d *acfgoPostTypeDef) label(name string) string {
	if v := d.Labels[name]; v != "" {
		return v
	}
	return d.Labels["name"]
}

type acfgoTaxonomyDef struct {
	Name                 string
	Labels               map[string]string
	ObjectTypes          []string
	Public, Hierarchical bool
	ShowUI, ShowInREST   bool
	RestBase             string
}

func (d *acfgoTaxonomyDef) label(name string) string {
	if v := d.Labels[name]; v != "" {
		return v
	}
	return d.Labels["name"]
}

func (d *acfgoTaxonomyDef) forType(t string) bool {
	for _, o := range d.ObjectTypes {
		if o == t {
			return true
		}
	}
	return false
}

type acfgoOptionsPage struct {
	Key, PageTitle, MenuTitle, MenuSlug, ParentSlug string
	IconURL, Capability, UpdateButton, Updated      string
	Description, PostID                             string
	Position                                        float64
	Redirect, Autoload                              bool
}

// acfgoRegistry holds the post types and taxonomies registered by active ACF-Go sites of the
// process (like the theme registry). The core's label helpers consult it for unknown types.
var acfgoRegistry = struct {
	sync.RWMutex
	types map[string]*acfgoPostTypeDef
	taxes map[string]*acfgoTaxonomyDef
}{types: map[string]*acfgoPostTypeDef{}, taxes: map[string]*acfgoTaxonomyDef{}}

// Loading and saving -------------------------------------------------------------------------

func (s *Server) acfgoItems(ctx context.Context, typ string, withTrash bool) []*acfgoItem {
	rows, err := s.store.Pool.Query(ctx, `SELECT id, status, content FROM posts WHERE type = $1 ORDER BY menu_order, title, id`, typ)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*acfgoItem
	for rows.Next() {
		it := &acfgoItem{Type: typ}
		var content string
		if rows.Scan(&it.ID, &it.Status, &content) != nil {
			continue
		}
		if !withTrash && it.Status == "trash" {
			continue
		}
		_ = json.Unmarshal([]byte(content), &it.Data)
		if it.Data == nil {
			it.Data = map[string]any{}
		}
		out = append(out, it)
	}
	return out
}

func (s *Server) acfgoItem(ctx context.Context, typ string, id int64) *acfgoItem {
	for _, it := range s.acfgoItems(ctx, typ, true) {
		if it.ID == id {
			return it
		}
	}
	return nil
}

// acfgoSaveObject stores a post type / taxonomy / options page (matched by key), then registers.
func (s *Server) acfgoSaveObject(ctx context.Context, typ string, data map[string]any) error {
	prefix := map[string]string{acfgoPostType: "post_type_", acfgoTaxType: "taxonomy_", acfgoOptionsType: "ui_options_page_"}[typ]
	key := acfgoStr(data["key"])
	if key == "" {
		key = acfgoUniqid(prefix)
		data["key"] = key
	}
	if _, ok := data["active"]; !ok {
		data["active"] = true
	}
	data["modified"] = time.Now().Unix()
	status := "draft"
	if acfgoBool(data["active"]) {
		status = "publish"
	}
	if st := acfgoStr(data["_status"]); st == "trash" {
		status = "trash"
	}
	delete(data, "_status")
	content, _ := json.Marshal(data)
	tag, err := s.store.Pool.Exec(ctx, `UPDATE posts SET title = $3, content = $4, status = $5, modified_at = now(), updated_at = now() WHERE type = $1 AND slug = $2`,
		typ, key, acfgoStr(data["title"]), string(content), status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		_, err = s.store.Pool.Exec(ctx, `INSERT INTO posts (type, slug, title, content, status, published_at, comment_status, ping_status)
			VALUES ($1, $2, $3, $4, $5, now(), 'closed', 'closed')`, typ, key, acfgoStr(data["title"]), string(content), status)
		if err != nil {
			return err
		}
	}
	s.acfgoWriteLocalJSON(key, data)
	s.acfgoRegisterTypes(ctx)
	return nil
}

func (s *Server) acfgoSetObjectStatus(ctx context.Context, it *acfgoItem, status string) error {
	switch status {
	case "trash":
		it.Data["_status"] = "trash"
	case "publish", "untrash":
		it.Data["active"] = true
	case "draft":
		it.Data["active"] = false
	}
	return s.acfgoSaveObject(ctx, it.Type, it.Data)
}

// Definitions ---------------------------------------------------------------------------------

func acfgoStrMap(v any) map[string]string {
	out := map[string]string{}
	if m, ok := v.(map[string]any); ok {
		for k, val := range m {
			out[k] = acfgoStr(val)
		}
	}
	return out
}

func acfgoStrList(v any) []string {
	var out []string
	for _, x := range acfgoList(v) {
		if s := acfgoStr(x); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func acfgoPostTypeFrom(d map[string]any) *acfgoPostTypeDef {
	pt := &acfgoPostTypeDef{Name: acfgoStr(d["post_type"]), Labels: acfgoStrMap(d["labels"]), MenuIcon: acfgoStr(d["menu_icon"]),
		Public: acfgoBool(d["public"]), Hierarchical: acfgoBool(d["hierarchical"]), ShowUI: true, ShowInREST: true,
		RestBase: acfgoStr(d["rest_base"]), Supports: acfgoStrList(d["supports"]), Taxonomies: acfgoStrList(d["taxonomies"]),
		HasArchive: acfgoBool(d["has_archive"]), ArchiveSlug: acfgoStr(d["has_archive_slug"])}
	if v, ok := d["show_ui"]; ok {
		pt.ShowUI = acfgoBool(v)
	}
	if v, ok := d["show_in_rest"]; ok {
		pt.ShowInREST = acfgoBool(v)
	}
	if m, ok := d["menu_icon"].(map[string]any); ok { // ACF 6.3 stores {type, value}
		pt.MenuIcon = acfgoStr(m["value"])
	}
	if pt.MenuIcon == "" {
		pt.MenuIcon = "dashicons-admin-post"
	}
	pt.MenuPosition = float64(acfgoInt(d["menu_position"]))
	if rw, ok := d["rewrite"].(map[string]any); ok {
		pt.Slug = acfgoStr(rw["slug"])
	}
	if pt.Slug == "" {
		pt.Slug = pt.Name
	}
	if pt.RestBase == "" {
		pt.RestBase = pt.Name
	}
	if pt.Labels["name"] == "" {
		pt.Labels["name"] = acfgoStr(d["title"])
	}
	return pt
}

func acfgoTaxonomyFrom(d map[string]any) *acfgoTaxonomyDef {
	tx := &acfgoTaxonomyDef{Name: acfgoStr(d["taxonomy"]), Labels: acfgoStrMap(d["labels"]), ObjectTypes: acfgoStrList(d["object_type"]),
		Public: acfgoBool(d["public"]), Hierarchical: acfgoBool(d["hierarchical"]), ShowUI: true, ShowInREST: true, RestBase: acfgoStr(d["rest_base"])}
	if v, ok := d["show_ui"]; ok {
		tx.ShowUI = acfgoBool(v)
	}
	if v, ok := d["show_in_rest"]; ok {
		tx.ShowInREST = acfgoBool(v)
	}
	if tx.RestBase == "" {
		tx.RestBase = tx.Name
	}
	if tx.Labels["name"] == "" {
		tx.Labels["name"] = acfgoStr(d["title"])
	}
	return tx
}

func acfgoOptionsPageFrom(d map[string]any) *acfgoOptionsPage {
	op := &acfgoOptionsPage{Key: acfgoStr(d["key"]), PageTitle: acfgoStr(d["page_title"]), MenuTitle: acfgoStr(d["menu_title"]),
		MenuSlug: acfgoStr(d["menu_slug"]), ParentSlug: acfgoStr(d["parent_slug"]), Capability: acfgoStr(d["capability"]),
		UpdateButton: acfgoStr(d["update_button"]), Updated: acfgoStr(d["updated_message"]), Description: acfgoStr(d["description"]),
		PostID: acfgoStr(d["post_id"]), Position: float64(acfgoInt(d["position"])), Redirect: acfgoBool(d["redirect"]), Autoload: acfgoBool(d["autoload"])}
	switch ic := d["menu_icon"].(type) {
	case map[string]any:
		op.IconURL = acfgoStr(ic["value"])
	case string:
		op.IconURL = ic
	}
	if op.IconURL == "" {
		op.IconURL = acfgoStr(d["icon_url"])
	}
	if op.IconURL == "" {
		op.IconURL = "dashicons-admin-generic"
	}
	if op.PageTitle == "" {
		op.PageTitle = acfgoStr(d["title"])
	}
	if op.MenuTitle == "" {
		op.MenuTitle = op.PageTitle
	}
	if op.MenuSlug == "" {
		op.MenuSlug = wp.SanitizeTitle(op.PageTitle)
	}
	if op.ParentSlug == "none" {
		op.ParentSlug = ""
	}
	if op.Capability == "" {
		op.Capability = "edit_posts"
	}
	if op.UpdateButton == "" {
		op.UpdateButton = "Update"
	}
	if op.Updated == "" {
		op.Updated = "Options Updated"
	}
	if acfgoStr(d["data_storage"]) != "post_id" || op.PostID == "" {
		op.PostID = "options"
	}
	return op
}

func (s *Server) acfgoPostTypes(ctx context.Context) []*acfgoPostTypeDef {
	var out []*acfgoPostTypeDef
	for _, it := range s.acfgoItems(ctx, acfgoPostType, false) {
		if it.Active() {
			if pt := acfgoPostTypeFrom(it.Data); pt.Name != "" {
				out = append(out, pt)
			}
		}
	}
	return out
}

func (s *Server) acfgoTaxonomies(ctx context.Context) []*acfgoTaxonomyDef {
	var out []*acfgoTaxonomyDef
	for _, it := range s.acfgoItems(ctx, acfgoTaxType, false) {
		if it.Active() {
			if tx := acfgoTaxonomyFrom(it.Data); tx.Name != "" {
				out = append(out, tx)
			}
		}
	}
	return out
}

func (s *Server) acfgoOptionsPages(ctx context.Context) []*acfgoOptionsPage {
	var out []*acfgoOptionsPage
	for _, it := range s.acfgoItems(ctx, acfgoOptionsType, false) {
		if it.Active() {
			if op := acfgoOptionsPageFrom(it.Data); op.MenuSlug != "" {
				out = append(out, op)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out
}

func (s *Server) acfgoOptionsPage(ctx context.Context, slug string) *acfgoOptionsPage {
	for _, op := range s.acfgoOptionsPages(ctx) {
		if op.MenuSlug == slug {
			return op
		}
	}
	return nil
}

// acfgoRegisterTypes writes the active post types and taxonomies into the core's registry tables
// (content types keep existing when they are deactivated: posts reference them) and the process
// registry the admin screens read.
func (s *Server) acfgoRegisterTypes(ctx context.Context) {
	types := s.acfgoPostTypes(ctx)
	taxes := s.acfgoTaxonomies(ctx)
	for _, pt := range types {
		sup := map[string]bool{}
		for _, f := range pt.Supports {
			sup[f] = true
		}
		supJSON, _ := json.Marshal(sup)
		labJSON, _ := json.Marshal(pt.Labels)
		if _, err := s.store.Pool.Exec(ctx, `INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels)
			VALUES ($1, $2, $3, $4, $5, false, $6, $7) ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name, hierarchical = EXCLUDED.hierarchical,
			public = EXCLUDED.public, rest_base = EXCLUDED.rest_base, supports = EXCLUDED.supports, labels = EXCLUDED.labels, updated_at = now()
			WHERE NOT content_types.is_builtin`, pt.Name, pt.Labels["name"], pt.Hierarchical, pt.Public, pt.RestBase, supJSON, labJSON); err != nil {
			log.Printf("acf-go: register post type %s: %v", pt.Name, err)
		}
		// The core checks edit_<type>s and friends; like capability_type "post", every role
		// gets them exactly when it has the matching post capability.
		for _, cp := range []string{"edit_%ss", "edit_others_%ss", "publish_%ss", "read_private_%ss", "delete_%ss", "delete_others_%ss",
			"delete_published_%ss", "delete_private_%ss", "edit_published_%ss", "edit_private_%ss"} {
			from, to := fmt.Sprintf(cp, "post"), fmt.Sprintf(cp, pt.Name)
			if _, err := s.store.Pool.Exec(ctx, `UPDATE roles SET capabilities = capabilities || jsonb_build_object($2::text, capabilities->$1)
				WHERE capabilities ? $1 AND capabilities->$2 IS DISTINCT FROM capabilities->$1`, from, to); err != nil {
				log.Printf("acf-go: capabilities of %s: %v", pt.Name, err)
			}
		}
	}
	for _, tx := range taxes {
		objJSON, _ := json.Marshal(tx.ObjectTypes)
		labJSON, _ := json.Marshal(tx.Labels)
		if _, err := s.store.Pool.Exec(ctx, `INSERT INTO taxonomies (slug, name, hierarchical, object_types, rest_base, public, is_builtin, labels)
			VALUES ($1, $2, $3, $4, $5, $6, false, $7) ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name, hierarchical = EXCLUDED.hierarchical,
			object_types = EXCLUDED.object_types, rest_base = EXCLUDED.rest_base, public = EXCLUDED.public, labels = EXCLUDED.labels, updated_at = now()
			WHERE NOT taxonomies.is_builtin`, tx.Name, tx.Labels["name"], tx.Hierarchical, objJSON, tx.RestBase, tx.Public, labJSON); err != nil {
			log.Printf("acf-go: register taxonomy %s: %v", tx.Name, err)
		}
	}
	acfgoRegistry.Lock()
	acfgoRegistry.types = map[string]*acfgoPostTypeDef{}
	acfgoRegistry.taxes = map[string]*acfgoTaxonomyDef{}
	if s.acfgoOn() {
		for _, pt := range types {
			acfgoRegistry.types[pt.Name] = pt
		}
		for _, tx := range taxes {
			acfgoRegistry.taxes[tx.Name] = tx
		}
	}
	acfgoRegistry.Unlock()
}

// acfgoTypeLabels is labelsFor for a post type registered in ACF › Post Types.
func acfgoTypeLabels(typ string) (typeLabels, bool) {
	acfgoRegistry.RLock()
	pt := acfgoRegistry.types[typ]
	acfgoRegistry.RUnlock()
	if pt == nil {
		return typeLabels{}, false
	}
	name := pt.label("name")
	return typeLabels{Type: typ, Name: name, Singular: pt.label("singular_name"), AddNew: pt.label("add_new_item"), Search: pt.label("search_items"),
		NotFound: pt.label("not_found"), NotFoundTrash: pt.label("not_found_in_trash")}, true
}

// acfgoTaxLabels is taxLabelsFor for a taxonomy registered in ACF › Taxonomies.
func acfgoTaxLabels(tax string) (taxLabels, bool) {
	acfgoRegistry.RLock()
	tx := acfgoRegistry.taxes[tax]
	acfgoRegistry.RUnlock()
	if tx == nil {
		return taxLabels{}, false
	}
	l := taxLabels{Tax: tax, Name: tx.label("name"), Singular: tx.label("singular_name"), AddNew: tx.label("add_new_item"), Search: tx.label("search_items"),
		NotFound: tx.label("not_found"), Screen: "edit-tags?taxonomy=" + tax, Hierarchical: tx.Hierarchical, FilterParam: tax}
	if tx.Hierarchical {
		l.ParentLabel = tx.label("parent_item")
	}
	return l, true
}

// acfgoIsPostType reports whether typ is a post type registered by ACF-Go.
func acfgoIsPostType(typ string) bool {
	acfgoRegistry.RLock()
	defer acfgoRegistry.RUnlock()
	return acfgoRegistry.types[typ] != nil
}

// acfgoGenerateLabels fills every label of a post type or taxonomy from singular and plural,
// like ACF's label generator.
func acfgoGenerateLabels(isTax bool, singular, plural string) map[string]any {
	ls, lp := strings.ToLower(singular), strings.ToLower(plural)
	if isTax {
		return map[string]any{"name": plural, "singular_name": singular, "menu_name": plural, "all_items": "All " + plural, "edit_item": "Edit " + singular,
			"view_item": "View " + singular, "update_item": "Update " + singular, "add_new_item": "Add New " + singular, "new_item_name": "New " + singular + " Name",
			"parent_item": "Parent " + singular, "parent_item_colon": "Parent " + singular + ":", "search_items": "Search " + plural,
			"popular_items": "Popular " + plural, "separate_items_with_commas": "Separate " + lp + " with commas", "add_or_remove_items": "Add or remove " + lp,
			"choose_from_most_used": "Choose from the most used " + lp, "not_found": "No " + lp + " found", "no_terms": "No " + lp,
			"items_list_navigation": plural + " list navigation", "items_list": plural + " list", "back_to_items": "← Go to " + lp, "item_link": singular + " Link",
			"item_link_description": "A link to a " + ls}
	}
	return map[string]any{"name": plural, "singular_name": singular, "menu_name": plural, "all_items": "All " + plural, "edit_item": "Edit " + singular,
		"view_item": "View " + singular, "view_items": "View " + plural, "add_new_item": "Add New " + singular, "add_new": "Add New " + singular,
		"new_item": "New " + singular, "parent_item_colon": "Parent " + singular + ":", "search_items": "Search " + plural,
		"not_found": "No " + lp + " found", "not_found_in_trash": "No " + lp + " found in Trash", "archives": singular + " Archives",
		"attributes": singular + " Attributes", "insert_into_item": "Insert into " + ls, "uploaded_to_this_item": "Uploaded to this " + ls,
		"filter_items_list": "Filter " + lp + " list", "filter_by_date": "Filter " + lp + " by date", "items_list_navigation": plural + " list navigation",
		"items_list": plural + " list", "item_published": singular + " published.", "item_published_privately": singular + " published privately.",
		"item_reverted_to_draft": singular + " reverted to draft.", "item_scheduled": singular + " scheduled.", "item_updated": singular + " updated.",
		"item_link": singular + " Link", "item_link_description": "A link to a " + ls + "."}
}
