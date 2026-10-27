package server

import (
	"encoding/json"

	"wpgo/internal/store"
)

// gwePopupEditorDocConfig fixes a popup document's config for the editor preview: the page
// settings CSS (overlay, width, height, position, close button, entrance animation) is generated
// in the editor against document.config.settings.cssWrapperSelector, which upstream sets to
// Popup Document::get_css_wrapper_selector() — "#elementor-popup-modal-<id>". The captured config
// carries the lab's popup id, so the styles never matched the preview's dialog.
func gwePopupEditorDocConfig(p *store.Post, out map[string]any) {
	if p == nil || gweDocType(p) != "popup" {
		return
	}
	var settings map[string]any
	switch v := out["settings"].(type) {
	case json.RawMessage:
		if json.Unmarshal(v, &settings) != nil {
			return
		}
	case map[string]any:
		settings = v
	default:
		return
	}
	if settings == nil {
		return
	}
	settings["cssWrapperSelector"] = "#elementor-popup-modal-" + itoa64(p.ID)
	out["settings"] = settings
}
