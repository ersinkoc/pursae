package server

// GoWebEditor v4: the editor opens a document's newer autosave (Documents_Manager::
// get_doc_or_auto_save / Document::get_newer_autosave). The atomic editor autosaves while it
// works (creating a component saves the page with the new instance through document/save/auto
// and then opens the component), so switching back to the page or reloading the editor must show
// that autosave, not the last published tree.

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/builder/atomic"
	"wpgo/internal/store"
)

// gweV4NewerAutosave returns the elements of the current user's autosave of p when it is newer
// than the post itself, or nil.
func (s *Server) gweV4NewerAutosave(c *gin.Context, p *store.Post) json.RawMessage {
	u := currentUser(c)
	if p == nil || u == nil || u.ID == 0 {
		return nil
	}
	var data []byte
	err := s.store.Pool.QueryRow(c, `SELECT b.data FROM post_revisions r JOIN builder_revisions b ON b.revision_id = r.id
		WHERE r.post_id = $1 AND r.is_autosave AND r.author_id = $2 AND greatest(r.created_at, coalesce(r.updated_at, r.created_at)) > $3
		ORDER BY r.id DESC LIMIT 1`, p.ID, u.ID, p.ModifiedAt).Scan(&data)
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(data)), "[") {
		return nil
	}
	return data
}

// gweV4ElementKeys are the keys Element_Base::get_raw_data keeps for atomic elements (editor-only
// keys such as editSettings / defaultEditSettings / htmlCache are dropped).
var gweV4ElementKeys = map[string]bool{"id": true, "elType": true, "widgetType": true, "isInner": true, "isLocked": true,
	"settings": true, "styles": true, "interactions": true, "editor_settings": true, "elements": true, "version": true, "origin_id": true}

// gweV4ParseComponentElements mirrors what saving a component document does to its elements
// (Has_Atomic_Base::parse_atomic_settings / parse_atomic_styles): settings must validate against
// the element's props schema ("Settings validation failed. <errors>"), invalid styles are dropped,
// editor-only keys are removed.
func gweV4ParseComponentElements(els []any) ([]any, string) {
	out := make([]any, 0, len(els))
	for _, it := range els {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		clean := map[string]any{}
		for k, v := range m {
			if gweV4ElementKeys[k] {
				clean[k] = v
			}
		}
		typ := gweStr(m["elType"])
		if typ == "widget" {
			typ = gweStr(m["widgetType"])
		}
		if errs := atomic.ValidateSettings(typ, m["settings"]); len(errs) > 0 {
			return nil, "Settings validation failed. " + atomic.JoinErrors(errs)
		}
		if styles, ok := m["styles"].(map[string]any); ok {
			kept := map[string]any{}
			for id, st := range styles {
				if parsed, errs := atomic.ValidateStyle(st); len(errs) == 0 {
					b, _ := json.Marshal(parsed)
					var v any
					_ = json.Unmarshal(b, &v)
					kept[id] = v
				}
			}
			clean["styles"] = kept
		}
		if kids, ok := m["elements"].([]any); ok {
			parsed, msg := gweV4ParseComponentElements(kids)
			if msg != "" {
				return nil, msg
			}
			clean["elements"] = parsed
		}
		out = append(out, clean)
	}
	return out, ""
}
