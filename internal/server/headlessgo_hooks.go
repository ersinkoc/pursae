package server

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

// Plugin hooks of the Headless entry lifecycle (docs/developer/plugins/hooks.md). All of them need
// the plugin permission headless:entries.
//
//	headless_entry_validate     filter  before an entry is stored; can reject it or derive values
//	headless_entry_saved        action  after an entry was created or updated
//	headless_entry_published    action  after an entry was published
//	headless_entry_unpublished  action  after an entry was unpublished
//	headless_entry_deleted      action  after an entry was deleted
//
// The validate filter receives {"type", "documentId" ("" for a new entry), "locale", "action"
// (create, update, publish), "data"} and returns the same object. "errors" (a list of strings or of
// {"field","message"} objects) rejects the save with a 400; changes to scalar attributes in "data"
// are applied (validated again like API input), changes to anything else are ignored. The
// actions receive {"event", "type", "documentId", "locale", "status", "entry"}: the entry as
// webhooks carry it (private attributes never included).

const (
	hgHookValidate    = "headless_entry_validate"
	hgHookTimeout     = 5 * time.Second
	hgHookMaxErrors   = 20
	hgHookMaxErrorLen = 500
)

var hgEventHooks = map[string]string{
	"entry.create":    "headless_entry_saved",
	"entry.update":    "headless_entry_saved",
	"entry.publish":   "headless_entry_published",
	"entry.unpublish": "headless_entry_unpublished",
	"entry.delete":    "headless_entry_deleted",
}

// hgFireHook runs the action matching an entry event; it is called by hgEmit, so every write path
// (REST, GraphQL, admin, uploads, scheduler, relations) fires it exactly once per event.
func (s *Server) hgFireHook(ctx context.Context, event string, sc *hgSchema, e *hgEntry, status string) {
	name, ok := hgEventHooks[event]
	if !ok || s.plugins == nil || !s.plugins.hooks.HasAction(name) {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hgHookTimeout)
	defer cancel()
	s.plugins.hooks.DoAction(ctx, name, map[string]any{
		"event": event, "type": sc.UID, "documentId": e.DocumentID, "locale": e.Locale, "status": status,
		"entry": s.hgEntryPayload(ctx, sc, e, status),
	})
}

// hgPluginData is the data a validate plugin sees: no private or password attributes, no inverse relations.
func hgPluginData(sc *hgSchema, data map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range data {
		f := sc.Field(k)
		if f == nil || f.Private || f.Type == "password" || f.MappedBy != "" {
			continue
		}
		out[k] = v
	}
	return out
}

// hgValidateHook runs headless_entry_validate over the coerced data of a save. It returns the data
// to store (scalar changes of the plugins applied) or the validation errors to answer with.
func (s *Server) hgValidateHook(ctx context.Context, sc *hgSchema, h *hgCoercer, doc, locale, action string, data, old map[string]any) (map[string]any, hgValidationError) {
	if s.plugins == nil || !s.plugins.hooks.HasFilter(hgHookValidate) {
		return data, nil
	}
	view := hgPluginData(sc, data)
	// A round trip through JSON gives the plugin (and the comparison below) the shapes the API uses.
	var plain map[string]any
	raw, _ := json.Marshal(view)
	_ = json.Unmarshal(raw, &plain)
	var sent map[string]any // the plugin gets its own copy: changes are detected against plain
	_ = json.Unmarshal(raw, &sent)
	payload := map[string]any{"type": sc.UID, "documentId": doc, "locale": locale, "action": action, "data": sent}
	hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hgHookTimeout)
	defer cancel()
	res, _ := s.plugins.hooks.ApplyFilters(hctx, hgHookValidate, payload).(map[string]any)
	if res == nil {
		return data, nil
	}
	if errs := hgPluginErrors(res["errors"]); len(errs) > 0 {
		return nil, errs
	}
	out, _ := res["data"].(map[string]any)
	if out == nil {
		return data, nil
	}
	changes := map[string]any{}
	for k, v := range out {
		f := sc.Field(k)
		if f == nil || !f.scalar() || f.Private || f.Type == "password" || f.MappedBy != "" {
			continue
		}
		if !reflect.DeepEqual(v, plain[k]) {
			changes[k] = v
		}
	}
	if len(changes) == 0 {
		return data, nil
	}
	before := len(h.errs)
	merged := h.object(sc, changes, data, nil, true)
	if len(h.errs) > before {
		return nil, h.errs[before:]
	}
	return merged, nil
}

func hgPluginErrors(v any) hgValidationError {
	list, _ := v.([]any)
	var out hgValidationError
	for _, x := range list {
		if len(out) >= hgHookMaxErrors {
			break
		}
		switch t := x.(type) {
		case string:
			out = append(out, hgFieldError{Message: hgClip(t), Name: "ValidationError"})
		case map[string]any:
			msg, _ := t["message"].(string)
			field, _ := t["field"].(string)
			fe := hgFieldError{Message: hgClip(msg), Name: "ValidationError"}
			if field != "" {
				fe.Path = []string{field}
			}
			if msg == "" {
				fe.Message = "Rejected by a plugin."
			}
			out = append(out, fe)
		default:
			out = append(out, hgFieldError{Message: hgClip(fmt.Sprint(t)), Name: "ValidationError"})
		}
	}
	return out
}

func hgClip(s string) string {
	if len(s) > hgHookMaxErrorLen {
		return s[:hgHookMaxErrorLen]
	}
	return s
}
