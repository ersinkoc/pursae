package builder

// Golementor Pro document types: popups and loop items have their own settings stacks (the
// editor's document config carries their controls; the server registers them here so the CSS
// engine styles the popup dialog and the loop item wrapper) and their own CSS wrapper selectors.

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// RegisterDocumentStack adds a document settings stack from an editor-form controls object
// (document config settings.controls, in panel order).
func RegisterDocumentStack(kind string, controls json.RawMessage) {
	// keep the editor's order: decode keys in sequence
	dec := json.NewDecoder(bytes.NewReader(controls))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return
	}
	var list []map[string]any
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return
		}
		var c map[string]any
		if dec.Decode(&c) != nil {
			return
		}
		if c == nil {
			continue
		}
		if _, ok := c["name"]; !ok {
			c["name"] = kt
		}
		list = append(list, schemaControl(c))
	}
	s := loadSchema()
	s.stacks[kind] = list
	stackMu.Lock()
	for k := range stackCache {
		if len(k) > len(kind) && k[:len(kind)+1] == kind+"|" {
			delete(stackCache, k)
		}
	}
	stackMu.Unlock()
}

// proDocumentCSSKind gives Pro documents their wrapper selector; ok is false for other types.
func proDocumentCSSKind(doc *Document) (string, string, bool) {
	id := strconv.FormatInt(doc.ID, 10)
	switch doc.Type {
	case "popup":
		return "popup", "#elementor-popup-modal-" + id, true
	case "loop-item":
		return "loop-item", ".e-loop-item-" + id, true
	}
	return "", "", false
}

// DocumentFrontendSettings mirrors Document::get_frontend_settings: the frontend_available
// document settings (popup animations, scroll snap, ...).
func DocumentFrontendSettings(doc *Document, rc *RenderContext) map[string]any {
	r := newRenderer(doc, rc)
	kind, _ := documentCSSKind(doc)
	st := stackFor(kind, r.bps)
	if st == nil {
		return map[string]any{}
	}
	raw := newOmap()
	for _, k := range settingsOrder(doc) {
		raw.Set(k, doc.Settings[k])
	}
	for _, k := range sortedPlainKeys(doc.Settings) {
		if !raw.Has(k) {
			raw.Set(k, doc.Settings[k])
		}
	}
	all := initSettings(raw, st.all)
	disp := activeSettings(all, st.byName)
	out := map[string]any{}
	for _, c := range st.all {
		if !c.Frontend {
			continue
		}
		v, ok := disp.m[c.Name]
		if !ok || v == nil || strictEq(v, "") {
			continue
		}
		out[c.Name] = v
	}
	return out
}

// DocumentWrapperClass returns the extra classes of a document wrapper (loop items).
func DocumentWrapperClass(doc *Document) string {
	if doc.Type == "loop-item" {
		return "e-loop-item e-loop-item-" + strconv.FormatInt(doc.ID, 10)
	}
	return ""
}
