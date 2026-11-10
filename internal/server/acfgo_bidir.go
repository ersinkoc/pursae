package server

import (
	"context"
	"log"
	"strconv"
)

// Bidirectional relationships (ACF 6.2): a relationship, post object or user field with
// "Bidirectional" on and target field keys in "bidirectional_target" mirrors its value into those
// fields of the related objects. Saving post A with B in the field adds A to B's target field;
// removing B removes A from it again. Only top-level fields are mirrored (as in ACF).

// acfgoBidirectional mirrors the change of field f (stored under name) on o into its targets.
// newV is the sanitized value that is about to be stored.
func (s *Server) acfgoBidirectional(ctx context.Context, o *acfObject, f acfField, name string, newV any) {
	if o == nil || !acfgoBool(f["bidirectional"]) || (o.Kind != "post" && o.Kind != "user") {
		return
	}
	switch acfgoStr(f["type"]) {
	case "relationship", "post_object", "user":
	default:
		return
	}
	targets := acfgoStrList(f["bidirectional_target"])
	if len(targets) == 0 {
		return
	}
	old, _ := s.acfgoGet(o, name)
	oldSet, newSet := acfgoIDSet(old), acfgoIDSet(newV)
	self := strconv.FormatInt(o.ID, 10)
	for _, tk := range targets {
		tf := s.acfgoFieldByKey(ctx, tk)
		if tf == nil || acfgoStr(tf["name"]) == "" {
			continue
		}
		switch acfgoStr(tf["type"]) {
		case "relationship", "post_object", "user":
		default:
			continue
		}
		// A post field points at posts, a user field at users: the target field must match.
		wantUser := acfgoStr(f["type"]) == "user"
		if (acfgoStr(tf["type"]) == "user") != wantUser {
			continue
		}
		for id := range newSet {
			if !oldSet[id] {
				s.acfgoBidirectionalApply(ctx, wantUser, id, tf, self, true)
			}
		}
		for id := range oldSet {
			if !newSet[id] {
				s.acfgoBidirectionalApply(ctx, wantUser, id, tf, self, false)
			}
		}
	}
}

// acfgoBidirectionalApply adds (or removes) self in field tf of the object id.
func (s *Server) acfgoBidirectionalApply(ctx context.Context, user bool, id int64, tf acfField, self string, add bool) {
	var t *acfObject
	if user {
		u := s.acfgoUserByID(ctx, id)
		if u == nil {
			return
		}
		if u.Meta == nil {
			u.Meta = map[string]any{}
		}
		t = &acfObject{Kind: "user", ID: u.ID, meta: u.Meta}
	} else {
		p, err := s.store.GetPost(ctx, id)
		if err != nil || p.Status == "trash" {
			return
		}
		t = s.acfgoPostObject(p)
	}
	name, key := acfgoStr(tf["name"]), acfgoStr(tf["key"])
	cur, _ := s.acfgoGet(t, name)
	multi := acfgoStr(tf["type"]) == "relationship" || acfgoBool(tf["multiple"])
	var val any
	if multi {
		var list []any
		seen := false
		for _, x := range acfgoList(cur) {
			if acfgoStr(x) == self {
				seen = true
				if !add {
					continue
				}
			}
			list = append(list, acfgoStr(x))
		}
		if add && !seen {
			list = append(list, self)
		}
		if add == seen {
			return // already mirrored, or nothing to remove
		}
		if len(list) == 0 {
			val = ""
		} else {
			val = list
		}
	} else {
		have := acfgoStr(acfgoScalar(cur))
		switch {
		case add && have == self, !add && have != self:
			return
		case add:
			val = self
		default:
			val = ""
		}
	}
	w := newACFWrite()
	w.put(name, key, val)
	if err := s.acfgoCommit(ctx, t, w); err != nil {
		log.Printf("acf-go: bidirectional %s %d: %v", t.Kind, t.ID, err)
	}
}

// acfgoIDSet is the set of object ids in a stored relationship value.
func acfgoIDSet(v any) map[int64]bool {
	out := map[int64]bool{}
	for _, x := range acfgoList(v) {
		if n := acfgoInt(x); n > 0 {
			out[n] = true
		}
	}
	return out
}
