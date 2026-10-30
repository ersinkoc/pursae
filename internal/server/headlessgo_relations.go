package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Bidirectional relations. A relation between two content types is stored once, on its owning
// side: the owning field (inversedBy: the inverse field's name) keeps the related document ids in
// the entry data. The inverse field on the target type (mappedBy: the owning field's name) stores
// nothing; its value is every owning entry whose field points at the document, so both sides
// always agree. Writing the inverse side (API or Content Manager) changes the owning entries.
// Cardinalities mirror each other: oneToOne <-> oneToOne, oneToMany <-> manyToOne, manyToMany <->
// manyToMany. A link written from the inverse side changes both the draft and the published
// version of the owning entries: a link is shared by the two documents.

// hgMirror is the relation kind of the inverse side.
func hgMirror(rel string) string {
	switch rel {
	case "oneToMany":
		return "manyToOne"
	case "manyToOne":
		return "oneToMany"
	}
	return rel
}

func hgCopySchema(sc *hgSchema) *hgSchema {
	c := *sc
	c.Fields = append([]hgField(nil), sc.Fields...)
	return &c
}

func hgRemoveField(sc *hgSchema, name string) {
	out := sc.Fields[:0]
	for _, f := range sc.Fields {
		if f.Name != name {
			out = append(out, f)
		}
	}
	sc.Fields = out
}

// hgSyncInverse keeps the two sides of bidirectional relations in step when sc is saved: an
// owning field with inversedBy gets its inverse field on the target type (created, renamed with
// it, given the mirrored kind, or removed with it); an inverse field added, renamed or removed on
// sc updates the owning field's inversedBy. renames are sc's renamed fields (old -> new). It
// changes sc in place (self relations) and returns the other types to store with it.
func hgSyncInverse(reg *hgRegistry, sc *hgSchema, renames map[string]string) ([]*hgSchema, error) {
	if sc.IsComponent() {
		return nil, nil
	}
	copies := map[string]*hgSchema{}
	get := func(uid string) *hgSchema {
		if uid == sc.UID {
			return sc
		}
		if c, ok := copies[uid]; ok {
			return c
		}
		o := reg.Get(uid)
		if o == nil || o.IsComponent() {
			return nil
		}
		c := hgCopySchema(o)
		copies[uid] = c
		return c
	}
	newName := func(old string) string {
		if n, ok := renames[old]; ok {
			return n
		}
		return old
	}
	// 1. Links of other types to sc: inverse fields whose owning field changed or went away, and
	// owning fields whose inverse field on sc was renamed or removed.
	for _, other := range reg.Types() {
		if other.UID == sc.UID {
			continue
		}
		t := get(other.UID)
		var drop []string
		for i := range t.Fields {
			g := &t.Fields[i]
			if g.Type != "relation" || g.Target != sc.UID {
				continue
			}
			if g.MappedBy != "" {
				name := newName(g.MappedBy)
				of := sc.Field(name)
				if of == nil || of.Type != "relation" || of.Target != t.UID || of.MappedBy != "" || of.InversedBy != g.Name {
					drop = append(drop, g.Name)
					continue
				}
				g.MappedBy, g.Relation = name, hgMirror(of.Relation)
			} else if g.InversedBy != "" {
				name := newName(g.InversedBy)
				inf := sc.Field(name)
				if inf == nil || inf.Type != "relation" || inf.MappedBy != g.Name || inf.Target != t.UID {
					g.InversedBy = ""
					continue
				}
				g.InversedBy = name
			}
		}
		for _, d := range drop {
			hgRemoveField(t, d)
		}
	}
	// 2. sc's own links.
	var names []string
	for _, f := range sc.Fields {
		names = append(names, f.Name)
	}
	mirrored := map[string]string{} // owner uid.field -> inverse field of sc
	for _, name := range names {
		f := sc.Field(name)
		if f == nil || f.Type != "relation" {
			continue
		}
		switch {
		case f.InversedBy != "":
			t := get(f.Target)
			if t == nil {
				return nil, hgSchemaError{fmt.Sprintf("Field %s: the relation's target %q is not a content type.", f.Name, f.Target)}
			}
			if ex := t.Field(f.InversedBy); ex != nil {
				if ex.Type != "relation" || ex.MappedBy != f.Name || ex.Target != sc.UID {
					return nil, hgSchemaError{fmt.Sprintf("Field %s: %s already has a field called %q.", f.Name, t.DisplayName, f.InversedBy)}
				}
				ex.Relation = hgMirror(f.Relation)
				continue
			}
			t.Fields = append(t.Fields, hgField{Name: f.InversedBy, Type: "relation", Relation: hgMirror(f.Relation), Target: sc.UID, MappedBy: f.Name})
		case f.MappedBy != "":
			x := get(f.Target)
			if x == nil {
				return nil, hgSchemaError{fmt.Sprintf("Field %s: the relation's target %q is not a content type.", f.Name, f.Target)}
			}
			of := x.Field(f.MappedBy)
			ok := of != nil && of.Type == "relation" && of.Target == sc.UID && of.MappedBy == "" && (of.InversedBy == "" || of.InversedBy == f.Name)
			if x == sc && ok {
				ok = of.InversedBy == f.Name // a self relation: the owning field decides
			}
			if !ok {
				if x == sc {
					hgRemoveField(sc, f.Name) // its owning field went away
					continue
				}
				return nil, hgSchemaError{fmt.Sprintf("Field %s: %s has no relation %q to this type for it to mirror.", f.Name, x.DisplayName, f.MappedBy)}
			}
			key := x.UID + "." + f.MappedBy
			if prevName, dup := mirrored[key]; dup {
				return nil, hgSchemaError{fmt.Sprintf("Fields %s and %s mirror the same relation.", prevName, f.Name)}
			}
			mirrored[key] = f.Name
			of.InversedBy = f.Name
			f.Relation = hgMirror(of.Relation)
		}
	}
	var out []*hgSchema
	uids := make([]string, 0, len(copies))
	for uid := range copies {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	for _, uid := range uids {
		a, _ := json.Marshal(copies[uid])
		b, _ := json.Marshal(reg.Get(uid))
		if string(a) != string(b) {
			out = append(out, copies[uid])
		}
	}
	return out, nil
}

// Entries ---------------------------------------------------------------------------------------------

// hgOwnerLocale is the locale column of the owning entries of an inverse relation, for a document
// version in locale loc.
func (s *Server) hgOwnerLocale(ctx context.Context, owner *hgSchema, loc string) string {
	if !owner.Localized {
		return ""
	}
	if loc != "" {
		return loc
	}
	return s.hgDefaultLocale(ctx)
}

// hgInverseIDs lists the document ids of the owning entries whose field f.MappedBy points at doc
// (the value of inverse field f), in the given status, in stored order.
func (s *Server) hgInverseIDs(ctx context.Context, f *hgField, doc, loc, status string) []string {
	owner := s.hgReg(ctx).Get(f.Target)
	if owner == nil || f.MappedBy == "" {
		return nil
	}
	col := "published_data"
	if status == "draft" && owner.DraftAndPublish {
		col = "data"
	}
	probe, _ := json.Marshal(map[string]any{f.MappedBy: []any{doc}})
	rows, err := s.store.Pool.Query(ctx, `SELECT document_id FROM hg_entries WHERE type_uid = $1 AND locale = $2 AND `+col+` @> $3::jsonb ORDER BY id`,
		owner.UID, s.hgOwnerLocale(ctx, owner, loc), probe)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if rows.Scan(&d) == nil {
			out = append(out, d)
		}
	}
	return out
}

// hgInverseChange is what writing one inverse field does to the owning entries.
type hgInverseChange struct {
	owner  *hgSchema
	field  string // the owning field
	single bool   // the owning field holds one id
	locale string // owner locale column
	add    []string
	remove []string
}

// inverse plans the writes of the inverse fields of an entry save (doc in entry locale loc): the
// input values are read like any relation (an id, a list, or connect / disconnect / set) over the
// current owners. Problems are recorded as validation errors.
func (h *hgCoercer) inverse(sc *hgSchema, doc, loc string) []hgInverseChange {
	var out []hgInverseChange
	for i := range sc.Fields {
		f := &sc.Fields[i]
		v, ok := h.inv[f.Name]
		if !ok || f.MappedBy == "" {
			continue
		}
		owner := h.reg.Get(f.Target)
		of := (*hgField)(nil)
		if owner != nil {
			of = owner.Field(f.MappedBy)
		}
		if of == nil {
			continue
		}
		oloc := h.s.hgOwnerLocale(h.ctx, owner, loc)
		probe, _ := json.Marshal(map[string]any{f.MappedBy: []any{doc}})
		var cur []any
		rows, err := h.s.store.Pool.Query(h.ctx, `SELECT document_id FROM hg_entries WHERE type_uid = $1 AND locale = $2
			AND (data @> $3::jsonb OR published_data @> $3::jsonb) ORDER BY id`, owner.UID, oloc, probe)
		if err == nil {
			for rows.Next() {
				var d string
				if rows.Scan(&d) == nil {
					cur = append(cur, d)
				}
			}
			rows.Close()
		}
		path := []string{f.Name}
		nv, ok := h.relation(sc, f, v, cur, path)
		if !ok {
			continue
		}
		want, _ := nv.([]any)
		ch := hgInverseChange{owner: owner, field: of.Name, single: !of.multi(), locale: oloc}
		for _, x := range want {
			d := fmt.Sprint(x)
			if !hgAnyIn(d, cur) {
				var exists bool
				_ = h.s.store.Pool.QueryRow(h.ctx, `SELECT EXISTS(SELECT 1 FROM hg_entries WHERE type_uid = $1 AND document_id = $2 AND locale = $3)`, owner.UID, d, oloc).Scan(&exists)
				if !exists {
					h.fail(path, "%s has no version of %s in the locale %q", owner.DisplayName, d, oloc)
					continue
				}
				ch.add = append(ch.add, d)
			}
		}
		for _, x := range cur {
			if !hgAnyIn(x, want) {
				ch.remove = append(ch.remove, fmt.Sprint(x))
			}
		}
		if len(ch.add) > 0 || len(ch.remove) > 0 {
			out = append(out, ch)
		}
	}
	return out
}

// hgApplyInverse writes planned inverse changes for document doc: it is added to or removed from
// the owning field of each owning entry, in its draft and its published version.
func (s *Server) hgApplyInverse(ctx context.Context, doc string, changes []hgInverseChange) error {
	if len(changes) == 0 {
		return nil
	}
	tx, err := s.store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	edit := func(raw []byte, field string, add, single bool) ([]byte, bool) {
		if raw == nil {
			return nil, false
		}
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil || m == nil {
			m = map[string]any{}
		}
		list, _ := m[field].([]any)
		var next []any
		switch {
		case add && single:
			next = []any{doc}
		case add:
			next = list
			if !hgAnyIn(doc, list) {
				next = append(append([]any(nil), list...), doc)
			}
		default:
			for _, x := range list {
				if fmt.Sprint(x) != doc {
					next = append(next, x)
				}
			}
		}
		if len(next) == 0 {
			delete(m, field)
		} else {
			m[field] = next
		}
		out, _ := json.Marshal(m)
		return out, string(out) != string(raw)
	}
	for _, ch := range changes {
		for _, step := range []struct {
			docs []string
			add  bool
		}{{ch.remove, false}, {ch.add, true}} {
			for _, od := range step.docs {
				var id int64
				var data, pub []byte
				if err := tx.QueryRow(ctx, `SELECT id, data, published_data FROM hg_entries WHERE type_uid = $1 AND document_id = $2 AND locale = $3 FOR UPDATE`,
					ch.owner.UID, od, ch.locale).Scan(&id, &data, &pub); err != nil {
					continue
				}
				nd, c1 := edit(data, ch.field, step.add, ch.single)
				np, c2 := edit(pub, ch.field, step.add, ch.single)
				if !c1 && !c2 {
					continue
				}
				if !c1 {
					nd = data
				}
				var pubArg any
				if pub != nil {
					pubArg = pub
					if c2 {
						pubArg = np
					}
				}
				if _, err := tx.Exec(ctx, `UPDATE hg_entries SET data = $2, published_data = $3, updated_at = now() WHERE id = $1`, id, nd, pubArg); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit(ctx)
}

// hgUnlinkDocument removes a deleted document (no locale version left) from every relation that
// points at it: owning fields of every type, also inside components and dynamic zones, in drafts
// and published versions.
func (s *Server) hgUnlinkDocument(ctx context.Context, uid, doc string) error {
	var remaining bool
	if err := s.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hg_entries WHERE type_uid = $1 AND document_id = $2)`, uid, doc).Scan(&remaining); err != nil || remaining {
		return err
	}
	reg := s.hgReg(ctx)
	tx, err := s.store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, t := range reg.Types() {
		for _, f := range t.Fields {
			if f.Type != "relation" || f.Target != uid || f.MappedBy != "" {
				continue
			}
			for _, col := range []string{"data", "published_data"} {
				if _, err := tx.Exec(ctx, `UPDATE hg_entries SET `+col+` = CASE WHEN (`+col+`->$2::text) - $3::text = '[]'::jsonb THEN `+col+` - $2::text
					ELSE jsonb_set(`+col+`, ARRAY[$2::text], (`+col+`->$2::text) - $3::text) END
					WHERE type_uid = $1 AND `+col+` IS NOT NULL AND jsonb_typeof(`+col+`->$2::text) = 'array' AND (`+col+`->$2::text) ? $3::text`, t.UID, f.Name, doc); err != nil {
					return err
				}
			}
		}
	}
	for _, cs := range reg.Components() {
		var fields []string
		for _, f := range cs.Fields {
			if f.Type == "relation" && f.Target == uid {
				fields = append(fields, f.Name)
			}
		}
		if len(fields) == 0 {
			continue
		}
		fix := func(obj map[string]any) {
			for _, name := range fields {
				list, ok := obj[name].([]any)
				if !ok {
					continue
				}
				var keep []any
				for _, x := range list {
					if fmt.Sprint(x) != doc {
						keep = append(keep, x)
					}
				}
				if len(keep) == 0 {
					delete(obj, name)
				} else {
					obj[name] = keep
				}
			}
		}
		if err := hgMigrateEntries(ctx, tx, reg, cs.UID, fix); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Filtering and sorting through relations -----------------------------------------------------------

// inverseFilter compiles a filter through an inverse relation: the owning entries of the row
// (alias) that match m. With only $null / $notNull it tests whether there is any.
func (q *hgQuery) inverseFilter(f *hgField, m map[string]any, alias string) (string, error) {
	owner := q.reg.Get(f.Target)
	if owner == nil || alias == "" {
		return "", hgBad("Invalid key %s", f.Name)
	}
	a := q.sql.next("o")
	cond := a + ".type_uid = " + q.sql.arg(owner.UID)
	if owner.Localized {
		cond += " AND " + a + ".locale = " + q.sql.arg(q.locale)
	}
	if q.status != "draft" {
		cond += " AND " + a + ".published_data IS NOT NULL"
	}
	cond += " AND " + q.versionCol(a) + "->'" + f.MappedBy + "' @> jsonb_build_array(" + alias + ".document_id)"
	if only, ok := hgOnlyNullOps(m); ok {
		var parts []string
		for _, k := range []string{"$null", "$notNull"} {
			v, ok := only[k]
			if !ok {
				continue
			}
			none := hgTruthy(v)
			if k == "$notNull" {
				none = !none
			}
			if none {
				parts = append(parts, "NOT EXISTS (SELECT 1 FROM hg_entries "+a+" WHERE "+cond+")")
			} else {
				parts = append(parts, "EXISTS (SELECT 1 FROM hg_entries "+a+" WHERE "+cond+")")
			}
		}
		return strings.Join(parts, " AND "), nil
	}
	inner, err := q.filters(owner, m, q.versionCol(a), a)
	if err != nil {
		return "", err
	}
	return "EXISTS (SELECT 1 FROM hg_entries " + a + " WHERE " + cond + " AND " + inner + ")", nil
}

// sortExpr compiles a sort key path (title, author.name, seo.metaTitle, author.company.name) on
// objects of schema sc stored at col; alias is the entry row ("" inside a component). A path
// goes through single components and relations that hold one entry, post, page or user.
func (q *hgQuery) sortExpr(sc *hgSchema, path []string, col, alias string, depth int) (string, error) {
	key := strings.Join(path, ".")
	name := path[0]
	if len(path) == 1 {
		if sys, ok := hgSystemFields[name]; ok {
			if alias == "" {
				return "", hgBad("Invalid sort key %s", key)
			}
			return alias + "." + sys.col, nil
		}
		f := sc.Field(name)
		if f == nil || f.Private || f.Type == "password" || !f.scalar() || f.Type == "json" {
			return "", hgBad("Invalid sort key %s", key)
		}
		expr, _ := hgFieldExpr(f, col)
		if f.Type == "text" || f.Type == "uid" || f.Type == "email" || f.Type == "enumeration" {
			expr = "lower(" + expr + ")"
		}
		return expr, nil
	}
	if depth > 5 {
		return "", hgBad("The sort key %s is nested too deeply", key)
	}
	f := sc.Field(name)
	if f == nil || f.Private {
		return "", hgBad("Invalid sort key %s", key)
	}
	lit := "'" + f.Name + "'"
	switch f.Type {
	case "component":
		comp := q.reg.Get(f.Component)
		if comp == nil || f.Repeatable {
			return "", hgBad("Cannot sort by %s: a repeatable component holds several values", key)
		}
		return q.sortExpr(comp, path[1:], col+"->"+lit, "", depth+1)
	case "relation":
		if f.multi() {
			return "", hgBad("Cannot sort by %s: the relation holds several entries", key)
		}
		first := "(" + col + "->" + lit + "->>0)"
		if len(path) != 2 && strings.HasPrefix(f.Target, "wp:") {
			return "", hgBad("Invalid sort key %s", key)
		}
		switch f.Target {
		case "wp:post", "wp:page", "wp:user":
			cols := map[string]string{"id": "id", "slug": "slug", "title": "title"}
			table := "posts"
			if f.Target == "wp:user" {
				cols, table = map[string]string{"id": "id", "username": "login", "slug": "nicename", "name": "display_name"}, "users"
			}
			c, ok := cols[path[1]]
			if !ok {
				return "", hgBad("Invalid sort key %s", key)
			}
			a := q.sql.next("w")
			expr := a + "." + c
			if c != "id" {
				expr = "lower(" + expr + ")"
			}
			return "(SELECT " + expr + " FROM " + table + " " + a + " WHERE " + a + ".id::text = " + first + ")", nil
		}
		target := q.reg.Get(f.Target)
		if target == nil {
			return "", hgBad("Invalid sort key %s", key)
		}
		a := q.sql.next("s")
		cond := a + ".type_uid = " + q.sql.arg(target.UID)
		if f.MappedBy != "" {
			if alias == "" {
				return "", hgBad("Invalid sort key %s", key)
			}
			cond += " AND " + q.versionCol(a) + "->'" + f.MappedBy + "' @> jsonb_build_array(" + alias + ".document_id)"
		} else {
			cond += " AND " + a + ".document_id = " + first
		}
		if target.Localized {
			cond += " AND " + a + ".locale = " + q.sql.arg(q.locale)
		}
		if q.status != "draft" {
			cond += " AND " + a + ".published_data IS NOT NULL"
		}
		inner, err := q.sortExpr(target, path[1:], q.versionCol(a), a, depth+1)
		if err != nil {
			return "", err
		}
		return "(SELECT " + inner + " FROM hg_entries " + a + " WHERE " + cond + " ORDER BY " + a + ".id LIMIT 1)", nil
	}
	return "", hgBad("Invalid sort key %s", key)
}
