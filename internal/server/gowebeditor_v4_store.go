package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"

	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/store"
)

// GoWebEditor v4 (atomic / Editor V4) persistence: global classes, global variables, default
// styles and components (migration 0006_atomic.sql). The REST routes in gowebeditor_v4_rest.go
// write through these accessors; the CSS/rendering side reads through them.

const (
	gweComponentType  = "elementor_component" // content type of components
	gweV4MaxClasses   = 1000
	gweV4MaxVariables = 1000
	gweV4MaxComponent = 100
	gweV4LockDuration = time.Hour // Component_Lock_Manager: HOUR_IN_SECONDS
)

// gweV4OnChange is called after every global-classes, variables or default-styles write; the
// CSS generator sets it to drop its caches.
var gweV4OnChange func()

func (s *Server) gweV4Invalidate() {
	if gweV4OnChange != nil {
		gweV4OnChange()
	}
}

// --- global classes ----------------------------------------------------------------------------

type gweGlobalClass struct {
	ID       string
	Label    string
	Variants []any
	SyncToV3 *bool // "sync_to_v3" as sent (nil = not sent): the design-system sync map
}

// item is the REST/style shape of a class: {id, label, type: "class", variants}.
func (g gweGlobalClass) item() map[string]any {
	v := g.Variants
	if v == nil {
		v = []any{}
	}
	return map[string]any{"id": g.ID, "label": g.Label, "type": "class", "variants": v}
}

// gweV4ClassQuery selects the classes of a kit in one context, in order. Preview reads fall back
// to the frontend columns and skip preview-deleted classes; frontend reads skip unpublished ones.
func gweV4ClassQuery(preview bool) string {
	if preview {
		return `SELECT id, coalesce(preview_label, label), coalesce(preview_variants, variants)
			FROM gwe_global_classes WHERE kit_id = $1 AND NOT preview_deleted
			ORDER BY coalesce(preview_position, position), id`
	}
	return `SELECT id, label, variants FROM gwe_global_classes WHERE kit_id = $1 AND published ORDER BY position, id`
}

// gweV4GlobalClasses lists a kit's classes in order (the preview order in the preview context).
func (s *Server) gweV4GlobalClasses(ctx context.Context, kitID int64, preview bool) ([]gweGlobalClass, error) {
	rows, err := s.store.Pool.Query(ctx, gweV4ClassQuery(preview), kitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gweGlobalClass{}
	for rows.Next() {
		var g gweGlobalClass
		var variants []byte
		if err := rows.Scan(&g.ID, &g.Label, &variants); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(variants, &g.Variants)
		if g.Variants == nil {
			g.Variants = []any{}
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// gweV4GlobalClassesByID returns the requested classes by id and the requested ids that exist,
// in global order.
func (s *Server) gweV4GlobalClassesByID(ctx context.Context, kitID int64, preview bool, ids []string) (map[string]gweGlobalClass, []string, error) {
	all, err := s.gweV4GlobalClasses(ctx, kitID, preview)
	if err != nil {
		return nil, nil, err
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	byID := map[string]gweGlobalClass{}
	order := []string{}
	for _, g := range all {
		if want[g.ID] {
			byID[g.ID] = g
			order = append(order, g.ID)
		}
	}
	return byID, order, nil
}

// gweV4ClassIDsIn collects every class id applied in a saved element tree (_elementor_data): the
// values of `classes` props ({"$$type":"classes","value":[ids]}), depth-first and unique.
func gweV4ClassIDsIn(elements []byte) []string {
	var tree []any
	if err := json.Unmarshal(elements, &tree); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	var walk func(list []any)
	walk = func(list []any) {
		for _, it := range list {
			el, _ := it.(map[string]any)
			if el == nil {
				continue
			}
			settings, _ := el["settings"].(map[string]any)
			for _, v := range settings {
				for _, id := range gweV4ClassesProp(v) {
					if !seen[id] {
						seen[id] = true
						out = append(out, id)
					}
				}
			}
			if children, ok := el["elements"].([]any); ok {
				walk(children)
			}
		}
	}
	walk(tree)
	return out
}

// gweV4ClassesProp returns the ids of a classes prop value, or nil when v is not one.
func gweV4ClassesProp(v any) []string {
	m, _ := v.(map[string]any)
	if m == nil || m["$$type"] != "classes" {
		return nil
	}
	list, _ := m["value"].([]any)
	var out []string
	for _, x := range list {
		if id, ok := x.(string); ok && id != "" {
			out = append(out, id)
		}
	}
	return out
}

// gweV4ClassUsage is where one class is applied: one entry per document.
type gweV4ClassUsage struct {
	PageID   int64    `json:"pageId"`
	Title    string   `json:"title"`
	Type     string   `json:"type"`
	Total    int      `json:"total"`
	Elements []string `json:"elements"`
}

// gweV4ClassUsages scans every builder document for the given (global) class ids. Like upstream
// the kit and plain templates are left out.
func (s *Server) gweV4ClassUsages(ctx context.Context, classIDs []string) (map[string][]gweV4ClassUsage, error) {
	valid := map[string]bool{}
	for _, id := range classIDs {
		valid[id] = true
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT id, type, title, coalesce(meta->>'_elementor_template_type',''), coalesce(meta->>'_elementor_data','[]')
		FROM posts WHERE meta->>'_elementor_edit_mode' = 'builder' AND status NOT IN ('trash','auto-draft')
		AND coalesce(meta->>'_elementor_template_type','') NOT IN ('kit','template','e-flexbox') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]gweV4ClassUsage{}
	for rows.Next() {
		var id int64
		var typ, title, docType, data string
		if err := rows.Scan(&id, &typ, &title, &docType, &data); err != nil {
			return nil, err
		}
		if docType == "" {
			docType = "wp-post"
			if typ == "page" {
				docType = "wp-page"
			}
		}
		var tree []any
		if json.Unmarshal([]byte(data), &tree) != nil {
			continue
		}
		perClass := map[string]*gweV4ClassUsage{}
		var order []string
		var walk func(list []any)
		walk = func(list []any) {
			for _, it := range list {
				el, _ := it.(map[string]any)
				if el == nil {
					continue
				}
				elID, _ := el["id"].(string)
				if elID == "" {
					elID = "unknown"
				}
				settings, _ := el["settings"].(map[string]any)
				for _, cid := range gweV4ClassesProp(settings["classes"]) {
					if !valid[cid] {
						continue
					}
					u := perClass[cid]
					if u == nil {
						u = &gweV4ClassUsage{PageID: id, Title: title, Type: docType, Elements: []string{}}
						perClass[cid] = u
						order = append(order, cid)
					}
					u.Total++
					u.Elements = append(u.Elements, elID)
				}
				if children, ok := el["elements"].([]any); ok {
					walk(children)
				}
			}
		}
		walk(tree)
		for _, cid := range order {
			out[cid] = append(out[cid], *perClass[cid])
		}
	}
	return out, rows.Err()
}

// gweV4ClassChange is one global-classes PUT, already validated.
type gweV4ClassChange struct {
	Preview bool
	Deleted []string
	Items   map[string]gweGlobalClass // added and modified classes
	Order   []string                  // the full order after the change
}

// gweV4ApplyClasses persists a global-classes change in one transaction.
func (s *Server) gweV4ApplyClasses(ctx context.Context, kitID int64, ch gweV4ClassChange) error {
	return pgx.BeginFunc(ctx, s.store.Pool, func(tx pgx.Tx) error {
		for _, id := range ch.Deleted {
			var err error
			if ch.Preview {
				// A class that was never published just goes; a published one is hidden from the preview.
				_, err = tx.Exec(ctx, `DELETE FROM gwe_global_classes WHERE kit_id = $1 AND id = $2 AND NOT published`, kitID, id)
				if err == nil {
					_, err = tx.Exec(ctx, `UPDATE gwe_global_classes SET preview_deleted = true, preview_label = NULL, preview_variants = NULL,
						preview_position = NULL, edited_at = now() WHERE kit_id = $1 AND id = $2`, kitID, id)
				}
			} else {
				_, err = tx.Exec(ctx, `DELETE FROM gwe_global_classes WHERE kit_id = $1 AND id = $2`, kitID, id)
			}
			if err != nil {
				return err
			}
		}
		for id, g := range ch.Items {
			variants, _ := json.Marshal(g.Variants)
			var err error
			if ch.Preview {
				_, err = tx.Exec(ctx, `INSERT INTO gwe_global_classes (kit_id, id, preview_label, preview_variants, published)
					VALUES ($1, $2, $3, $4, false)
					ON CONFLICT (kit_id, id) DO UPDATE SET preview_label = EXCLUDED.preview_label, preview_variants = EXCLUDED.preview_variants,
					preview_deleted = false, edited_at = now()`, kitID, id, g.Label, variants)
			} else {
				_, err = tx.Exec(ctx, `INSERT INTO gwe_global_classes (kit_id, id, label, variants, published)
					VALUES ($1, $2, $3, $4, true)
					ON CONFLICT (kit_id, id) DO UPDATE SET label = EXCLUDED.label, variants = EXCLUDED.variants, published = true,
					preview_label = NULL, preview_variants = NULL, preview_deleted = false, edited_at = now()`, kitID, id, g.Label, variants)
			}
			if err != nil {
				return err
			}
		}
		if ch.Preview {
			for i, id := range ch.Order {
				if _, err := tx.Exec(ctx, `UPDATE gwe_global_classes SET preview_position = $3 WHERE kit_id = $1 AND id = $2`, kitID, id, i); err != nil {
					return err
				}
			}
			return nil
		}
		for i, id := range ch.Order {
			if _, err := tx.Exec(ctx, `UPDATE gwe_global_classes SET position = $3 WHERE kit_id = $1 AND id = $2`, kitID, id, i); err != nil {
				return err
			}
		}
		// Propagate the published order to the preview: unpublished preview classes keep their slot.
		rows, err := tx.Query(ctx, `SELECT id, published FROM gwe_global_classes WHERE kit_id = $1 AND NOT preview_deleted
			ORDER BY coalesce(preview_position, position), id`, kitID)
		if err != nil {
			return err
		}
		var previewOrder []string
		unpublished := map[string]bool{}
		for rows.Next() {
			var id string
			var published bool
			if err := rows.Scan(&id, &published); err != nil {
				rows.Close()
				return err
			}
			previewOrder = append(previewOrder, id)
			if !published {
				unpublished[id] = true
			}
		}
		rows.Close()
		merged := append([]string{}, ch.Order...)
		for pos, id := range previewOrder {
			if !unpublished[id] {
				continue
			}
			at := min(pos, len(merged))
			merged = append(merged[:at], append([]string{id}, merged[at:]...)...)
		}
		for i, id := range merged {
			if _, err := tx.Exec(ctx, `UPDATE gwe_global_classes SET preview_position = $3 WHERE kit_id = $1 AND id = $2`, kitID, id, i); err != nil {
				return err
			}
		}
		return nil
	})
}

// --- variables -----------------------------------------------------------------------------------

var gweV4VariableTypes = []string{"global-color-variable", "global-font-variable", "global-size-variable", "global-custom-size-variable"}

// gweV4VariableSet is a kit's variables as the editor sees them (v1 shape: plain string values).
type gweV4VariableSet struct {
	Data      map[string]map[string]any
	Watermark int
}

// gweV4LoadVariables reads a kit's variables, converting the stored prop-type values to the
// client (v1) shape: color "#fff", font "Roboto", size "12px", custom size the raw string.
func (s *Server) gweV4LoadVariables(ctx context.Context, kitID int64) (*gweV4VariableSet, error) {
	set := &gweV4VariableSet{Data: map[string]map[string]any{}}
	var raw []byte
	err := s.store.Pool.QueryRow(ctx, `SELECT data, watermark FROM gwe_variables WHERE kit_id = $1`, kitID).Scan(&raw, &set.Watermark)
	if err == pgx.ErrNoRows {
		return set, nil
	}
	if err != nil {
		return nil, err
	}
	var stored map[string]map[string]any
	_ = json.Unmarshal(raw, &stored)
	for id, v := range stored {
		if v == nil {
			continue
		}
		set.Data[id] = gweV4VariableFromStorage(v)
	}
	return set, nil
}

func gweV4VariableFromStorage(v map[string]any) map[string]any {
	out := map[string]any{}
	for k, x := range v {
		out[k] = x
	}
	wrapped, ok := out["value"].(map[string]any)
	if !ok {
		return out
	}
	val := wrapped["value"]
	if m, ok := val.(map[string]any); ok && m["unit"] == "custom" {
		val = m["size"]
		out["type"] = "global-custom-size-variable"
	}
	if out["type"] == "global-size-variable" {
		m, _ := val.(map[string]any)
		size, unit := "", "px"
		if m != nil {
			switch t := m["size"].(type) {
			case float64:
				size = strconv.FormatFloat(t, 'f', -1, 64)
			case string:
				size = t
			}
			if u, _ := m["unit"].(string); u != "" {
				unit = u
			}
		}
		val = size + unit
	}
	out["value"] = val
	return out
}

// gweV4VariableToStorage wraps a client (v1) value in its prop type for storage (v2).
func gweV4VariableToStorage(v map[string]any) map[string]any {
	out := map[string]any{}
	for k, x := range v {
		out[k] = x
	}
	val, isString := out["value"].(string)
	if !isString {
		return out
	}
	typ, _ := out["type"].(string)
	switch typ {
	case "global-color-variable":
		out["value"] = map[string]any{"$$type": "color", "value": val}
	case "global-font-variable":
		out["value"] = map[string]any{"$$type": "string", "value": val}
	case "global-size-variable":
		out["value"] = map[string]any{"$$type": "size", "value": gweV4ParseSize(val)}
	case "global-custom-size-variable":
		out["type"] = "global-size-variable"
		out["value"] = map[string]any{"$$type": "size", "value": map[string]any{"size": val, "unit": "custom"}}
	}
	return out
}

// gweV4ParseSize splits "12px" into {size: 12, unit: "px"} like upstream's adapter.
func gweV4ParseSize(v string) any {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "auto" {
		return map[string]any{"size": "", "unit": "auto"}
	}
	if v == "" {
		return map[string]any{"size": "", "unit": "px"}
	}
	i := 0
	for i < len(v) && (v[i] == '-' || v[i] == '.' || (v[i] >= '0' && v[i] <= '9')) {
		i++
	}
	num, unit := v[:i], v[i:]
	if num == "" || unit == "" {
		return v
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return v
	}
	for _, r := range unit {
		if !(r >= 'a' && r <= 'z' || r == '%') {
			return v
		}
	}
	return map[string]any{"size": f, "unit": unit}
}

// gweV4SaveVariables writes the set back (storage shape) and bumps the watermark, returning it.
func (s *Server) gweV4SaveVariables(ctx context.Context, kitID int64, set *gweV4VariableSet) (int, error) {
	stored := map[string]map[string]any{}
	for id, v := range set.Data {
		stored[id] = gweV4VariableToStorage(v)
	}
	b, _ := json.Marshal(stored)
	var watermark int
	err := s.store.Pool.QueryRow(ctx, `INSERT INTO gwe_variables (kit_id, data, watermark, version) VALUES ($1, $2, 1, 2)
		ON CONFLICT (kit_id) DO UPDATE SET data = EXCLUDED.data, watermark = CASE WHEN gwe_variables.watermark >= 2147483647 THEN 1 ELSE gwe_variables.watermark + 1 END
		RETURNING watermark`, kitID, b).Scan(&watermark)
	if err != nil {
		return 0, err
	}
	set.Watermark = watermark
	s.gweV4Invalidate()
	return watermark, nil
}

// gweV4Variables returns a kit's variables in the client shape (deleted ones flagged) and the watermark.
func (s *Server) gweV4Variables(ctx context.Context, kitID int64) (map[string]map[string]any, int, error) {
	set, err := s.gweV4LoadVariables(ctx, kitID)
	if err != nil {
		return nil, 0, err
	}
	out := map[string]map[string]any{}
	for id, v := range set.Data {
		item := map[string]any{}
		for k, x := range v {
			item[k] = x
		}
		if _, deleted := v["deleted_at"]; deleted {
			item["deleted"] = true
		}
		out[id] = item
	}
	return out, set.Watermark, nil
}

// gweV4NewVariableID makes an unused "e-gv-" + 7 hex id.
func gweV4NewVariableID(existing map[string]map[string]any) string {
	for {
		var b [4]byte
		_, _ = rand.Read(b[:])
		id := "e-gv-" + hex.EncodeToString(b[:])[:7]
		if _, taken := existing[id]; !taken {
			return id
		}
	}
}

func gweV4Now() string { return time.Now().UTC().Format("2006-01-02 15:04:05") }

// --- default styles --------------------------------------------------------------------------------

var gweV4DefaultStyleTags = []string{"h1", "h2", "h3", "h4", "h5", "h6", "p", "span", "a", "button", "div", "header", "section",
	"article", "aside", "footer", "main", "nav", "form", "ul", "li", "img", "video", "hr", "details", "summary", "input", "textarea", "select", "label"}

func gweV4DefaultStyleTag(tag string) bool {
	for _, t := range gweV4DefaultStyleTags {
		if t == tag {
			return true
		}
	}
	return false
}

func gweV4DefaultStyleItem(tag string, variants []any) map[string]any {
	if variants == nil {
		variants = []any{}
	}
	return map[string]any{"id": tag, "label": tag, "cssName": "e-default-" + tag, "type": "class", "variants": variants}
}

// gweV4DefaultStyles returns a kit's default styles by tag: {id: tag, label: tag, cssName, type: "class", variants}.
func (s *Server) gweV4DefaultStyles(ctx context.Context, kitID int64) (map[string]map[string]any, error) {
	rows, err := s.store.Pool.Query(ctx, `SELECT tag, variants FROM gwe_default_styles WHERE kit_id = $1 ORDER BY tag`, kitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]any{}
	for rows.Next() {
		var tag string
		var raw []byte
		if err := rows.Scan(&tag, &raw); err != nil {
			return nil, err
		}
		var variants []any
		_ = json.Unmarshal(raw, &variants)
		out[tag] = gweV4DefaultStyleItem(tag, variants)
	}
	return out, rows.Err()
}

func (s *Server) gweV4PutDefaultStyle(ctx context.Context, kitID int64, tag string, variants []any) error {
	b, _ := json.Marshal(variants)
	_, err := s.store.Pool.Exec(ctx, `INSERT INTO gwe_default_styles (kit_id, tag, variants) VALUES ($1, $2, $3)
		ON CONFLICT (kit_id, tag) DO UPDATE SET variants = EXCLUDED.variants`, kitID, tag, b)
	if err == nil {
		s.gweV4Invalidate()
	}
	return err
}

func (s *Server) gweV4DeleteDefaultStyle(ctx context.Context, kitID int64, tag string) error {
	_, err := s.store.Pool.Exec(ctx, `DELETE FROM gwe_default_styles WHERE kit_id = $1 AND tag = $2`, kitID, tag)
	if err == nil {
		s.gweV4Invalidate()
	}
	return err
}

// --- kit meta ------------------------------------------------------------------------------------------

// gweV4SetKitMeta replaces one key of the kit's meta in place (no revision, no other field touched).
func (s *Server) gweV4SetKitMeta(ctx context.Context, kitID int64, key string, value any) error {
	b, _ := json.Marshal(value)
	_, err := s.store.Pool.Exec(ctx, `UPDATE posts SET meta = jsonb_set(coalesce(meta,'{}'::jsonb), ARRAY[$2::text], $3::jsonb), modified_at = now() WHERE id = $1`,
		kitID, key, string(b))
	return err
}

// gweV4KitElementsDefaults is the kit meta _elementor_elements_default_values: element type -> settings.
func gweV4KitElementsDefaults(kit *store.Post) map[string]any {
	out := map[string]any{}
	switch v := kit.Meta["_elementor_elements_default_values"].(type) {
	case map[string]any:
		for k, x := range v {
			out[k] = x
		}
	case string:
		_ = json.Unmarshal([]byte(v), &out)
	}
	return out
}

// --- components ----------------------------------------------------------------------------------------

// gweV4Components lists the components (every status but trash), newest first, archived ones only when asked.
func (s *Server) gweV4Components(ctx context.Context, includeArchived ...bool) ([]*store.Post, error) {
	posts, _, err := s.store.QueryPosts(ctx, store.PostQuery{Types: []string{gweComponentType}, PerPage: gweV4MaxComponent, Page: 1})
	if err != nil {
		return nil, err
	}
	if len(includeArchived) > 0 && includeArchived[0] {
		return posts, nil
	}
	out := posts[:0]
	for _, p := range posts {
		if !gweV4ComponentArchived(p) {
			out = append(out, p)
		}
	}
	return out, nil
}

// gweV4Component loads one component post, or nil when the id is not a component.
func (s *Server) gweV4Component(ctx context.Context, id int64) *store.Post {
	p, err := s.store.GetPost(ctx, id)
	if err != nil || p.Type != gweComponentType || p.Status == "trash" {
		return nil
	}
	return p
}

func gweV4ComponentUID(p *store.Post) string {
	uid, _ := p.Meta["_elementor_component_uid"].(string)
	return uid
}

func gweV4ComponentArchived(p *store.Post) bool {
	m, _ := p.Meta["_elementor_component_is_archived"].(map[string]any)
	v, _ := m["is_archived"].(bool)
	return v
}

// gweV4ComponentOverridableProps is the component's overridable props meta, nil when none.
func gweV4ComponentOverridableProps(p *store.Post) any {
	switch v := p.Meta["_elementor_component_overridable_props"].(type) {
	case map[string]any:
		if len(v) == 0 {
			return nil
		}
		return v
	case string:
		var out any
		if json.Unmarshal([]byte(v), &out) == nil {
			if m, ok := out.(map[string]any); ok && len(m) == 0 {
				return nil
			}
			return out
		}
	}
	return nil
}

// gweV4ComponentStyles merges the local `styles` of every element in a component's tree.
func gweV4ComponentStyles(p *store.Post) map[string]any {
	var tree []any
	_ = json.Unmarshal(gweElementsRaw(p), &tree)
	out := map[string]any{}
	var walk func(list []any)
	walk = func(list []any) {
		for _, it := range list {
			el, _ := it.(map[string]any)
			if el == nil {
				continue
			}
			if styles, ok := el["styles"].(map[string]any); ok {
				for k, v := range styles {
					out[k] = v
				}
			}
			if children, ok := el["elements"].([]any); ok {
				walk(children)
			}
		}
	}
	walk(tree)
	return out
}

// gweV4ComponentLock is the lock meta _elementor_component_lock.
type gweV4ComponentLock struct {
	LockedBy int64
	LockedAt time.Time
}

func gweV4LockOf(p *store.Post) gweV4ComponentLock {
	m, _ := p.Meta["_elementor_component_lock"].(map[string]any)
	by, _ := m["locked_by"].(float64)
	at, _ := m["locked_at"].(float64)
	l := gweV4ComponentLock{LockedBy: int64(by)}
	if at > 0 {
		l.LockedAt = time.Unix(int64(at), 0)
	}
	if l.LockedBy > 0 && !l.LockedAt.IsZero() && time.Since(l.LockedAt) >= gweV4LockDuration {
		return gweV4ComponentLock{} // expired
	}
	return l
}

// gweV4SetPostMeta replaces one meta key of a post in place; a nil value removes it.
func (s *Server) gweV4SetPostMeta(ctx context.Context, postID int64, key string, value any) error {
	if value == nil {
		_, err := s.store.Pool.Exec(ctx, `UPDATE posts SET meta = coalesce(meta,'{}'::jsonb) - $2::text WHERE id = $1`, postID, key)
		return err
	}
	b, _ := json.Marshal(value)
	_, err := s.store.Pool.Exec(ctx, `UPDATE posts SET meta = jsonb_set(coalesce(meta,'{}'::jsonb), ARRAY[$2::text], $3::jsonb), modified_at = now() WHERE id = $1`,
		postID, key, string(b))
	return err
}

// gweV4NonAtomic lists the non-atomic element types in a tree (everything but e-* elements and widgets).
func gweV4NonAtomic(elements []any) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(list []any)
	walk = func(list []any) {
		for _, it := range list {
			el, _ := it.(map[string]any)
			if el == nil {
				continue
			}
			elType, _ := el["elType"].(string)
			widgetType, _ := el["widgetType"].(string)
			typ := widgetType
			if typ == "" {
				typ = elType
			}
			atomic := strings.HasPrefix(elType, "e-") || (elType == "widget" && strings.HasPrefix(widgetType, "e-"))
			if typ != "" && !atomic && !seen[typ] {
				seen[typ] = true
				out = append(out, typ)
			}
			if children, ok := el["elements"].([]any); ok {
				walk(children)
			}
		}
	}
	walk(elements)
	return out
}

// gweV4EnsureComponentType registers the component content type on databases migrated before 0006.
func (s *Server) gweV4EnsureComponentType(ctx context.Context) error {
	_, err := s.store.Pool.Exec(ctx, `INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels)
		VALUES ($1, 'Components', false, false, '', false, '{"title":true,"author":true,"thumbnail":true,"custom-fields":true,"revisions":true}',
		'{"name":"Components","singular_name":"Component"}') ON CONFLICT (slug) DO NOTHING`, gweComponentType)
	return err
}
