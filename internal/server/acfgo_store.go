package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Field groups are acf-field-group posts whose content is the group in ACF's export format (fields
// nested under "fields", sub fields under "sub_fields", flexible content layouts under "layouts"),
// so Tools › Export/Import and local JSON move them unchanged between ACF and ACF-Go. A field is a
// map with ACF's setting names: key, label, name, type, instructions, required, wrapper, … .

type acfField = map[string]any

type acfRule struct {
	Param    string `json:"param"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

type acfGroup struct {
	ID       int64
	Key      string
	Title    string
	Fields   []acfField
	Location [][]acfRule
	Order    int64
	Position string // acf_after_title, normal, side
	Style    string // default, seamless
	// LabelPlacement is top or left; InstructionPlacement label, field or tooltip.
	LabelPlacement, InstructionPlacement string
	HideOnScreen                         []string
	Active                               bool
	Description                          string
	ShowInREST                           bool
	Modified                             int64
	Status                               string // publish (active), draft (inactive), trash
	Local                                string // "json" when loaded from (or newer in) local JSON, "go" for code-registered groups
	LocalFile                            string
	SyncAvailable                        bool
	Raw                                  map[string]any // every other key of the JSON, kept on round trips
}

var acfgoGroupKnown = map[string]bool{"key": true, "title": true, "fields": true, "location": true, "menu_order": true, "position": true, "style": true,
	"label_placement": true, "instruction_placement": true, "hide_on_screen": true, "active": true, "description": true, "show_in_rest": true, "modified": true}

// toMap is the group in ACF's JSON export format.
func (g *acfGroup) toMap() map[string]any {
	m := map[string]any{}
	for k, v := range g.Raw {
		m[k] = v
	}
	loc := make([]any, 0, len(g.Location))
	for _, grp := range g.Location {
		rules := make([]any, 0, len(grp))
		for _, r := range grp {
			rules = append(rules, map[string]any{"param": r.Param, "operator": r.Operator, "value": r.Value})
		}
		loc = append(loc, rules)
	}
	fields := make([]any, len(g.Fields))
	for i, f := range g.Fields {
		fields[i] = f
	}
	hide := make([]any, len(g.HideOnScreen))
	for i, h := range g.HideOnScreen {
		hide[i] = h
	}
	m["key"], m["title"], m["fields"], m["location"] = g.Key, g.Title, fields, loc
	m["menu_order"], m["position"], m["style"] = g.Order, defaultStr(g.Position, "normal"), defaultStr(g.Style, "default")
	m["label_placement"], m["instruction_placement"] = defaultStr(g.LabelPlacement, "top"), defaultStr(g.InstructionPlacement, "label")
	m["hide_on_screen"], m["active"], m["description"] = hide, g.Active, g.Description
	m["show_in_rest"] = boolInt(g.ShowInREST)
	if g.Modified != 0 {
		m["modified"] = g.Modified
	}
	return m
}

// acfgoGroupFromMap reads a group in ACF's JSON format (export files, local JSON, post content).
func acfgoGroupFromMap(m map[string]any) *acfGroup {
	g := &acfGroup{Raw: map[string]any{}, Active: true, Status: "publish"}
	for k, v := range m {
		if !acfgoGroupKnown[k] && !strings.HasPrefix(k, "ID") && k != "local" && k != "local_file" && k != "_valid" {
			g.Raw[k] = v
		}
	}
	g.Key, g.Title = acfgoStr(m["key"]), acfgoStr(m["title"])
	for _, f := range acfgoList(m["fields"]) {
		if fm, ok := f.(map[string]any); ok {
			g.Fields = append(g.Fields, acfgoNormalizeField(fm))
		}
	}
	for _, grp := range acfgoList(m["location"]) {
		var rules []acfRule
		for _, r := range acfgoList(grp) {
			if rm, ok := r.(map[string]any); ok {
				rules = append(rules, acfRule{Param: acfgoStr(rm["param"]), Operator: defaultStr(acfgoStr(rm["operator"]), "=="), Value: acfgoStr(rm["value"])})
			}
		}
		if len(rules) > 0 {
			g.Location = append(g.Location, rules)
		}
	}
	g.Order = acfgoInt(m["menu_order"])
	g.Position, g.Style = defaultStr(acfgoStr(m["position"]), "normal"), defaultStr(acfgoStr(m["style"]), "default")
	g.LabelPlacement = defaultStr(acfgoStr(m["label_placement"]), "top")
	g.InstructionPlacement = defaultStr(acfgoStr(m["instruction_placement"]), "label")
	for _, h := range acfgoList(m["hide_on_screen"]) {
		if hs := acfgoStr(h); hs != "" {
			g.HideOnScreen = append(g.HideOnScreen, hs)
		}
	}
	if v, ok := m["active"]; ok {
		g.Active = acfgoBool(v)
	}
	g.Description = acfgoStr(m["description"])
	g.ShowInREST = acfgoBool(m["show_in_rest"])
	g.Modified = acfgoInt(m["modified"])
	return g
}

// acfgoNormalizeField gives a field the keys every type has, recursively.
func acfgoNormalizeField(f acfField) acfField {
	if acfgoStr(f["key"]) == "" {
		f["key"] = acfgoUniqid("field_")
	}
	for k, def := range map[string]any{"label": "", "name": "", "type": "text", "instructions": "", "required": 0, "conditional_logic": 0} {
		if _, ok := f[k]; !ok {
			f[k] = def
		}
	}
	if _, ok := f["wrapper"].(map[string]any); !ok {
		f["wrapper"] = map[string]any{"width": "", "class": "", "id": ""}
	}
	if subs := acfgoList(f["sub_fields"]); subs != nil {
		out := make([]any, 0, len(subs))
		for _, sf := range subs {
			if sm, ok := sf.(map[string]any); ok {
				out = append(out, acfgoNormalizeField(sm))
			}
		}
		f["sub_fields"] = out
	}
	if f["type"] == "flexible_content" {
		f["layouts"] = acfgoLayoutsList(f)
	}
	return f
}

// acfgoLayoutsList returns flexible content layouts as a list (ACF exports them keyed by layout key).
func acfgoLayoutsList(f acfField) []any {
	var out []any
	add := func(v any) {
		if lm, ok := v.(map[string]any); ok {
			if acfgoStr(lm["key"]) == "" {
				lm["key"] = acfgoUniqid("layout_")
			}
			var subs []any
			for _, sf := range acfgoList(lm["sub_fields"]) {
				if sm, ok := sf.(map[string]any); ok {
					subs = append(subs, acfgoNormalizeField(sm))
				}
			}
			if subs == nil {
				subs = []any{}
			}
			lm["sub_fields"] = subs
			out = append(out, lm)
		}
	}
	switch t := f["layouts"].(type) {
	case []any:
		for _, l := range t {
			add(l)
		}
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys) // ACF keeps insertion order; JSON objects lose it, so key order it is
		for _, k := range keys {
			add(t[k])
		}
	}
	if out == nil {
		out = []any{}
	}
	return out
}

func acfgoSubFields(f acfField) []acfField {
	var out []acfField
	for _, sf := range acfgoList(f["sub_fields"]) {
		if sm, ok := sf.(map[string]any); ok {
			out = append(out, sm)
		}
	}
	return out
}

// acfgoUniqid is uniqid()-style: a prefix and 13 hex digits.
func acfgoUniqid(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	t := time.Now().UnixMicro()
	return prefix + fmt.Sprintf("%08x", t/1000000)[0:8] + hex.EncodeToString(b)[:5]
}

// Cache -------------------------------------------------------------------------------------------

// acfgoCache holds the field groups of one server; saves bump it. Template functions call
// get_field in loops, so the groups are not read from the database for every call.
type acfgoCacheEntry struct {
	mu     sync.Mutex
	groups []*acfGroup
	loaded bool // an empty result is a result too: it is cached for the TTL like any other
	at     time.Time
}

var acfgoCaches sync.Map // *Server → *acfgoCacheEntry

func (s *Server) acfgoCache() *acfgoCacheEntry {
	v, _ := acfgoCaches.LoadOrStore(s, &acfgoCacheEntry{})
	return v.(*acfgoCacheEntry)
}

func (s *Server) acfgoFlush() {
	e := s.acfgoCache()
	e.mu.Lock()
	e.groups, e.loaded = nil, false
	e.mu.Unlock()
}

// Loading -------------------------------------------------------------------------------------

// acfgoAllGroups returns every field group (DB, local JSON, code), trash excluded unless asked.
func (s *Server) acfgoAllGroups(ctx context.Context, withTrash bool) []*acfGroup {
	e := s.acfgoCache()
	e.mu.Lock()
	cached, fresh := e.groups, e.loaded && time.Since(e.at) <= 5*time.Second // local JSON files may change on disk
	e.mu.Unlock()
	if !fresh {
		cached = s.acfgoLoadGroups(ctx)
		e.mu.Lock()
		e.groups, e.loaded, e.at = cached, true, time.Now()
		e.mu.Unlock()
	}
	out := make([]*acfGroup, 0, len(cached))
	for _, g := range cached {
		if g.Status != "trash" || withTrash {
			out = append(out, g)
		}
	}
	return out
}

func (s *Server) acfgoLoadGroups(ctx context.Context) []*acfGroup {
	var out []*acfGroup
	byKey := map[string]*acfGroup{}
	rows, err := s.store.Pool.Query(ctx, `SELECT id, slug, title, content, status, menu_order FROM posts WHERE type = $1 ORDER BY menu_order, title, id`, acfgoGroupType)
	if err == nil {
		for rows.Next() {
			var id, order int64
			var slug, title, content, status string
			if rows.Scan(&id, &slug, &title, &content, &status, &order) != nil {
				continue
			}
			var m map[string]any
			if json.Unmarshal([]byte(content), &m) != nil || m == nil {
				m = map[string]any{}
			}
			g := acfgoGroupFromMap(m)
			g.ID, g.Title, g.Status, g.Order = id, title, status, order
			if g.Key == "" {
				g.Key = slug
			}
			g.Active = status == "publish"
			out = append(out, g)
			byKey[g.Key] = g
		}
		rows.Close()
	}
	for _, lg := range s.acfgoLocalJSON("group_") {
		g := acfgoGroupFromMap(lg.data)
		g.Local, g.LocalFile = "json", lg.file
		if db := byKey[g.Key]; db != nil {
			db.LocalFile = lg.file
			if db.Status == "trash" {
				continue
			}
			if g.Modified > db.Modified {
				db.SyncAvailable = true
			}
			continue
		}
		g.SyncAvailable = true // only on disk: importable
		g.Status = map[bool]string{true: "publish", false: "draft"}[g.Active]
		out = append(out, g)
		byKey[g.Key] = g
	}
	acfgoLocalMu.Lock()
	for _, lg := range acfgoLocalGroups {
		if byKey[lg.Key] == nil {
			cp := *lg
			cp.Local, cp.Status = "go", "publish"
			out = append(out, &cp)
		}
	}
	acfgoLocalMu.Unlock()
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out
}

var (
	acfgoLocalMu     sync.Mutex
	acfgoLocalGroups []*acfGroup
)

// acfgoRegisterLocalGroup is acf_add_local_field_group() for Go code: the group exists for every
// site of the process without being stored (it is listed with "Local" and can be synced to the DB).
func acfgoRegisterLocalGroup(m map[string]any) {
	g := acfgoGroupFromMap(m)
	acfgoLocalMu.Lock()
	defer acfgoLocalMu.Unlock()
	for i, lg := range acfgoLocalGroups {
		if lg.Key == g.Key {
			acfgoLocalGroups[i] = g
			return
		}
	}
	acfgoLocalGroups = append(acfgoLocalGroups, g)
}

func (s *Server) acfgoGroupByKey(ctx context.Context, key string) *acfGroup {
	for _, g := range s.acfgoAllGroups(ctx, true) {
		if g.Key == key {
			return g
		}
	}
	return nil
}

func (s *Server) acfgoGroupByID(ctx context.Context, id int64) *acfGroup {
	for _, g := range s.acfgoAllGroups(ctx, true) {
		if g.ID == id && id != 0 {
			return g
		}
	}
	return nil
}

// acfgoSaveGroup stores a group (insert or update by ID/key), refreshes its local JSON file and
// the cache.
func (s *Server) acfgoSaveGroup(ctx context.Context, g *acfGroup) error {
	if g.Key == "" {
		g.Key = acfgoUniqid("group_")
	}
	if strings.TrimSpace(g.Title) == "" {
		g.Title = "(no title)"
	}
	g.Modified = time.Now().Unix()
	if g.Status == "" || g.Status == "publish" || g.Status == "draft" {
		g.Status = map[bool]string{true: "publish", false: "draft"}[g.Active]
	}
	if g.ID == 0 {
		var id int64
		if err := s.store.Pool.QueryRow(ctx, `SELECT id FROM posts WHERE type = $1 AND slug = $2 LIMIT 1`, acfgoGroupType, g.Key).Scan(&id); err == nil {
			g.ID = id
		}
	}
	content, _ := json.Marshal(g.toMap())
	var err error
	if g.ID == 0 {
		err = s.store.Pool.QueryRow(ctx, `INSERT INTO posts (type, slug, title, content, status, menu_order, excerpt, published_at, comment_status, ping_status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now(), 'closed', 'closed') RETURNING id`, acfgoGroupType, g.Key, g.Title, string(content), g.Status, g.Order, g.Description).Scan(&g.ID)
	} else {
		_, err = s.store.Pool.Exec(ctx, `UPDATE posts SET slug = $2, title = $3, content = $4, status = $5, menu_order = $6, excerpt = $7, modified_at = now(), updated_at = now() WHERE id = $1`,
			g.ID, g.Key, g.Title, string(content), g.Status, g.Order, g.Description)
	}
	if err != nil {
		return err
	}
	if g.Status != "trash" {
		s.acfgoWriteLocalJSON(g.Key, g.toMap())
	}
	s.acfgoFlush()
	return nil
}

func (s *Server) acfgoSetGroupStatus(ctx context.Context, id int64, status string) error {
	g := s.acfgoGroupByID(ctx, id)
	if g == nil {
		return errors.New("field group not found")
	}
	if status == "trash" {
		g.Raw["_previous_status"] = g.Status
	} else if status == "untrash" {
		status = defaultStr(acfgoStr(g.Raw["_previous_status"]), "publish")
		delete(g.Raw, "_previous_status")
	}
	g.Status = status
	g.Active = status == "publish"
	return s.acfgoSaveGroup(ctx, g)
}

func (s *Server) acfgoDeleteGroup(ctx context.Context, id int64) error {
	g := s.acfgoGroupByID(ctx, id)
	_, err := s.store.Pool.Exec(ctx, `DELETE FROM posts WHERE id = $1 AND type = $2`, id, acfgoGroupType)
	if g != nil && g.LocalFile != "" {
		_ = os.Remove(g.LocalFile)
	}
	s.acfgoFlush()
	return err
}

// acfgoDuplicateGroup copies a group with fresh keys ("(copy)" title, inactive like ACF? ACF keeps
// the status; so does this).
func (s *Server) acfgoDuplicateGroup(ctx context.Context, g *acfGroup) (*acfGroup, error) {
	raw, _ := json.Marshal(g.toMap())
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	cp := acfgoGroupFromMap(m)
	cp.Key = acfgoUniqid("group_")
	cp.Title = g.Title + " (copy)"
	cp.Status = g.Status
	keys := map[string]string{}
	var rekey func(fs []acfField)
	rekey = func(fs []acfField) {
		for _, f := range fs {
			nk := acfgoUniqid("field_")
			keys[acfgoStr(f["key"])] = nk
			f["key"] = nk
			rekey(acfgoSubFields(f))
			for _, l := range acfgoList(f["layouts"]) {
				if lm, ok := l.(map[string]any); ok {
					lm["key"] = acfgoUniqid("layout_")
					var subs []acfField
					for _, sf := range acfgoList(lm["sub_fields"]) {
						if sm, ok := sf.(map[string]any); ok {
							subs = append(subs, sm)
						}
					}
					rekey(subs)
				}
			}
		}
	}
	rekey(cp.Fields)
	// Conditional logic points at the copied fields.
	acfgoWalkFields(cp.Fields, func(f acfField) {
		for _, grp := range acfgoList(f["conditional_logic"]) {
			for _, r := range acfgoList(grp) {
				if rm, ok := r.(map[string]any); ok {
					if nk, ok := keys[acfgoStr(rm["field"])]; ok {
						rm["field"] = nk
					}
				}
			}
		}
	})
	return cp, s.acfgoSaveGroup(ctx, cp)
}

// acfgoWalkFields calls fn for every field, sub field and layout sub field.
func acfgoWalkFields(fs []acfField, fn func(acfField)) {
	for _, f := range fs {
		fn(f)
		acfgoWalkFields(acfgoSubFields(f), fn)
		for _, l := range acfgoList(f["layouts"]) {
			if lm, ok := l.(map[string]any); ok {
				var subs []acfField
				for _, sf := range acfgoList(lm["sub_fields"]) {
					if sm, ok := sf.(map[string]any); ok {
						subs = append(subs, sm)
					}
				}
				acfgoWalkFields(subs, fn)
			}
		}
	}
}

// acfgoFieldByKey finds a field anywhere (sub fields and layouts included).
func (s *Server) acfgoFieldByKey(ctx context.Context, key string) acfField {
	if key == "" {
		return nil
	}
	var found acfField
	for _, g := range s.acfgoAllGroups(ctx, false) {
		acfgoWalkFields(g.Fields, func(f acfField) {
			if found == nil && acfgoStr(f["key"]) == key {
				found = f
			}
		})
		if found != nil {
			return found
		}
	}
	return nil
}

// Local JSON ----------------------------------------------------------------------------------

// acfgoJSONDirs are the acf-json load points: <data>/acf-json (the save point) and acf-json/ in the
// active theme package.
func (s *Server) acfgoJSONDirs() (save string, load []string) {
	save = filepath.Join(filepath.Dir(filepath.Clean(s.cfg.UploadsDir)), "acf-json")
	load = []string{save}
	if t := s.activeTheme(); t != nil && t.Package != nil && t.Package.Dir != "" {
		load = append(load, filepath.Join(t.Package.Dir, "acf-json"))
	}
	return save, load
}

type acfgoLocalFile struct {
	file string
	data map[string]any
}

// acfgoLocalJSON reads the JSON files of every load point whose key starts with prefix.
func (s *Server) acfgoLocalJSON(prefix string) []acfgoLocalFile {
	_, dirs := s.acfgoJSONDirs()
	var out []acfgoLocalFile
	seen := map[string]bool{}
	for _, dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		sort.Strings(files)
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var m map[string]any
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			k := acfgoStr(m["key"])
			if !strings.HasPrefix(k, prefix) || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, acfgoLocalFile{file: f, data: m})
		}
	}
	return out
}

// acfgoWriteLocalJSON saves <key>.json in the save point when that folder exists (ACF's rule: the
// folder opts in).
func (s *Server) acfgoWriteLocalJSON(key string, m map[string]any) {
	dir, _ := s.acfgoJSONDirs()
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return
	}
	b, err := json.MarshalIndent(m, "", "    ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, filepath.Base(key)+".json"), b, 0o644)
}

// acfgoImport imports ACF JSON (one object or a list): field groups, post types, taxonomies and
// options pages. Existing items with the same key are updated, like ACF's importer.
func (s *Server) acfgoImport(ctx context.Context, data []byte) (int, error) {
	var list []map[string]any
	if err := json.Unmarshal(data, &list); err != nil {
		var one map[string]any
		if err2 := json.Unmarshal(data, &one); err2 != nil {
			return 0, errors.New("the file is not valid JSON")
		}
		list = []map[string]any{one}
	}
	n := 0
	for _, m := range list {
		key := acfgoStr(m["key"])
		switch {
		case strings.HasPrefix(key, "group_"):
			g := acfgoGroupFromMap(m)
			if old := s.acfgoGroupByKey(ctx, key); old != nil && old.ID != 0 {
				g.ID = old.ID
			}
			if err := s.acfgoSaveGroup(ctx, g); err != nil {
				return n, err
			}
		case strings.HasPrefix(key, "post_type_"):
			if err := s.acfgoSaveObject(ctx, acfgoPostType, m); err != nil {
				return n, err
			}
		case strings.HasPrefix(key, "taxonomy_"):
			if err := s.acfgoSaveObject(ctx, acfgoTaxType, m); err != nil {
				return n, err
			}
		case strings.HasPrefix(key, "ui_options_page_"):
			if err := s.acfgoSaveObject(ctx, acfgoOptionsType, m); err != nil {
				return n, err
			}
		default:
			continue
		}
		n++
	}
	if n == 0 {
		return 0, errors.New("no field groups, post types, taxonomies or options pages found in the file")
	}
	s.acfgoRegisterTypes(ctx)
	return n, nil
}
