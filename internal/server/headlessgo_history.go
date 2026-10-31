package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"time"
)

// Content history: every save, publish, unpublish, discard, restore and scheduled publication of
// a document version records its draft in hg_entry_versions (the last hgHistoryKeep are kept). The
// edit screen lists them; a version can be viewed next to the current draft (fields that differ
// are marked) and restored as the new draft, after its values were checked against the current
// schema. Inverse relation fields store nothing and are not part of a version; a link made or
// removed from the other side of a two-way relation is recorded on both entries instead: the one
// whose data changed (a "link" version with the new draft) and the one whose inverse field now lists
// something else (a "link" version with a note saying who linked or unlinked it).

// hgHistoryKeep is how many versions of one document version are kept.
var hgHistoryKeep = 50

type hgEntryVersion struct {
	ID         int64
	EntryID    int64
	TypeUID    string
	DocumentID string
	Locale     string
	Action     string
	Data       map[string]any
	Published  bool
	CreatedBy  int64
	CreatedAt  time.Time
	Author     string // display name of CreatedBy (admin lists)
	Note       string // a reviewer's comment (review actions)
}

// ActionLabel is the history list's text for the action.
func (v *hgEntryVersion) ActionLabel() string {
	switch v.Action {
	case "create":
		return "Created"
	case "publish":
		return "Published"
	case "unpublish":
		return "Unpublished"
	case "discard":
		return "Changes discarded"
	case "restore":
		return "Restored"
	case "link":
		return "Relations changed"
	case "submit":
		return "Submitted for review"
	case "approve":
		return "Approved"
	case "reject":
		return "Rejected"
	case "schedule":
		return "Published as scheduled"
	case "unschedule":
		return "Unpublished as scheduled"
	}
	return "Saved"
}

// hgRecordVersion stores the current draft of an entry as a version. Errors are logged only:
// history never blocks a save.
func (s *Server) hgRecordVersion(ctx context.Context, e *hgEntry, action string, userID int64) {
	s.hgRecordVersionNote(ctx, e, action, userID, "")
}

// hgRecordVersionNote is hgRecordVersion with a note (the reviewer's comment).
func (s *Server) hgRecordVersionNote(ctx context.Context, e *hgEntry, action string, userID int64, note string) {
	if e == nil {
		return
	}
	raw, _ := json.Marshal(e.Data)
	if _, err := s.store.Pool.Exec(ctx, `INSERT INTO hg_entry_versions(entry_id, type_uid, document_id, locale, action, data, published, created_by, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, e.ID, e.TypeUID, e.DocumentID, e.Locale, action, raw, e.Published != nil, userID, note); err != nil {
		log.Printf("headlessgo: history: %v", err)
		return
	}
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM hg_entry_versions WHERE entry_id = $1 AND id NOT IN (
		SELECT id FROM hg_entry_versions WHERE entry_id = $1 ORDER BY id DESC LIMIT $2)`, e.ID, hgHistoryKeep)
}

const hgVersionCols = `v.id, v.entry_id, v.type_uid, v.document_id, v.locale, v.action, v.data, v.published, v.created_by, v.created_at, coalesce(u.display_name, ''), v.note`

func hgScanVersion(row interface{ Scan(...any) error }) (*hgEntryVersion, error) {
	v := &hgEntryVersion{}
	var raw []byte
	if err := row.Scan(&v.ID, &v.EntryID, &v.TypeUID, &v.DocumentID, &v.Locale, &v.Action, &raw, &v.Published, &v.CreatedBy, &v.CreatedAt, &v.Author, &v.Note); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(raw, &v.Data)
	if v.Data == nil {
		v.Data = map[string]any{}
	}
	return v, nil
}

// hgVersions lists the versions of an entry, newest first.
func (s *Server) hgVersions(ctx context.Context, entryID int64, limit int) []*hgEntryVersion {
	rows, err := s.store.Pool.Query(ctx, `SELECT `+hgVersionCols+` FROM hg_entry_versions v LEFT JOIN users u ON u.id = v.created_by
		WHERE v.entry_id = $1 ORDER BY v.id DESC LIMIT $2`, entryID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*hgEntryVersion
	for rows.Next() {
		if v, err := hgScanVersion(rows); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func (s *Server) hgVersionByID(ctx context.Context, id int64) (*hgEntryVersion, error) {
	v, err := hgScanVersion(s.store.Pool.QueryRow(ctx, `SELECT `+hgVersionCols+` FROM hg_entry_versions v LEFT JOIN users u ON u.id = v.created_by WHERE v.id = $1`, id))
	if err != nil {
		return nil, errHGNotFound
	}
	return v, nil
}

// hgStripPasswords copies a value tree without its password fields (a stored hash must not go
// through the coercer, which would hash it again).
func hgStripPasswords(reg *hgRegistry, sc *hgSchema, data map[string]any, depth int) map[string]any {
	if data == nil || depth > 10 {
		return data
	}
	out := make(map[string]any, len(data))
	for k, v := range data {
		out[k] = v
	}
	for _, f := range sc.Fields {
		v, ok := out[f.Name]
		if !ok || v == nil {
			continue
		}
		switch f.Type {
		case "password":
			delete(out, f.Name)
		case "component", "dynamiczone":
			var list []any
			for _, item := range hgList(v) {
				m, ok := item.(map[string]any)
				if !ok {
					list = append(list, item)
					continue
				}
				cu := f.Component
				if f.Type == "dynamiczone" {
					cu, _ = m["__component"].(string)
				}
				if comp := reg.Get(cu); comp != nil {
					m = hgStripPasswords(reg, comp, m, depth+1)
				}
				list = append(list, m)
			}
			if f.Type == "component" && !f.Repeatable {
				if len(list) > 0 {
					out[f.Name] = list[0]
				}
			} else {
				out[f.Name] = list
			}
		}
	}
	return out
}

// hgCheckRestored validates the values of a version against the current schema, as a save of
// them would (types and formats, required, min / max, regex, enumerations, media and relation
// targets that still exist, unique fields). It writes nothing.
func (s *Server) hgCheckRestored(ctx context.Context, sc *hgSchema, doc, locale string, data map[string]any) hgValidationError {
	reg := s.hgReg(ctx)
	h := &hgCoercer{s: s, ctx: ctx, reg: reg, required: !sc.DraftAndPublish}
	h.object(sc, hgStripPasswords(reg, sc, data, 0), data, nil, true)
	errs := h.errs
	errs = append(errs, s.hgCheckUnique(ctx, sc, doc, locale, data)...)
	return errs
}

// hgVersionDropped lists the fields of a version's values that the type no longer has (removed or
// renamed since); a restore leaves them out.
func hgVersionDropped(sc *hgSchema, data map[string]any) []string {
	var out []string
	for k := range data {
		if sc.Field(k) == nil {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// hgRestoreVersion makes a version the entry's draft. Its values are checked against the current
// schema first (the schema may have changed since): fields the type no longer has are left out, and
// when a value does not validate any more the restore is refused with the list of mismatches and
// nothing changes. With partial, the mismatching fields keep their current draft values instead
// and the restore goes ahead; the skipped fields are returned.
func (s *Server) hgRestoreVersion(ctx context.Context, sc *hgSchema, versionID int64, userID int64, partial bool) (*hgEntry, []string, error) {
	v, err := s.hgVersionByID(ctx, versionID)
	if err != nil || v.TypeUID != sc.UID {
		return nil, nil, errHGNotFound
	}
	cur, err := hgScanEntry(s.store.Pool.QueryRow(ctx, `SELECT `+hgEntryCols+` FROM hg_entries WHERE id = $1`, v.EntryID))
	if err != nil {
		return nil, nil, errors.New("The entry no longer exists.")
	}
	data := map[string]any{}
	for k, val := range v.Data {
		if f := sc.Field(k); f != nil && f.MappedBy == "" {
			data[k] = val
		}
	}
	var skipped []string
	if errs := s.hgCheckRestored(ctx, sc, cur.DocumentID, cur.Locale, data); len(errs) > 0 {
		if !partial {
			return nil, nil, errs
		}
		for _, fe := range errs {
			if len(fe.Path) == 0 || containsStr(skipped, fe.Path[0]) {
				continue
			}
			skipped = append(skipped, fe.Path[0])
			if cv, ok := cur.Data[fe.Path[0]]; ok {
				data[fe.Path[0]] = cv
			} else {
				delete(data, fe.Path[0])
			}
		}
		if errs := s.hgCheckRestored(ctx, sc, cur.DocumentID, cur.Locale, data); len(errs) > 0 {
			return nil, nil, errs
		}
	}
	raw, _ := json.Marshal(data)
	e, err := hgScanEntry(s.store.Pool.QueryRow(ctx, `UPDATE hg_entries SET data = $2, updated_at = now(), updated_by = $3 WHERE id = $1 RETURNING `+hgEntryCols,
		v.EntryID, raw, userID))
	if err != nil {
		return nil, nil, errors.New("The entry no longer exists.")
	}
	if !sc.DraftAndPublish {
		// Without draft & publish the draft is the live version.
		e, err = hgScanEntry(s.store.Pool.QueryRow(ctx, `UPDATE hg_entries SET published_data = data, published_at = now() WHERE id = $1 RETURNING `+hgEntryCols, e.ID))
		if err != nil {
			return nil, nil, err
		}
	}
	s.hgRecordVersion(ctx, e, "restore", userID)
	return e, skipped, nil
}

// hgDiffRow is one field of a version compared with the current draft.
type hgDiffRow struct {
	Name    string
	Old     string // the version's value
	New     string // the current draft's value
	Changed bool
}

// hgDiff compares a version's values with the current draft, field by field (passwords masked).
func hgDiff(reg *hgRegistry, sc *hgSchema, version, current map[string]any) []hgDiffRow {
	a := hgMaskPasswords(reg, sc, version, 0)
	b := hgMaskPasswords(reg, sc, current, 0)
	var out []hgDiffRow
	for _, f := range sc.Fields {
		if f.MappedBy != "" {
			continue
		}
		ov, nv := hgHistoryValue(a[f.Name]), hgHistoryValue(b[f.Name])
		out = append(out, hgDiffRow{Name: f.Name, Old: ov, New: nv, Changed: ov != nv})
	}
	return out
}

// hgHistoryValue is a value as the history screen prints it.
func hgHistoryValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool, float64:
		return hgCell(t)
	}
	raw, _ := json.MarshalIndent(v, "", "  ")
	return string(raw)
}
