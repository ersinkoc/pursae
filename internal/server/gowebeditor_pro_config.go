package server

import (
	"encoding/json"

	"github.com/gin-gonic/gin"

	"wpgo/internal/builder"
	"wpgo/internal/store"
)

// Golementor Pro: per-request patches of the captured widgets config. Pro's Nav Menu lists the
// site's menus (captured from a lab without menus, so the control arrives as an "alert"), and Pro
// widgets the capture lacks (breadcrumbs needs Yoast upstream) are added from their Go definitions.

// gweProMenuControl is Nav Menu's "menu" select for this site (menu ids as keys), or nil without menus.
func (s *Server) gweProMenuControl(c *gin.Context) map[string]any {
	list, err := s.store.Menus(c)
	if err != nil || len(list) == 0 {
		return nil
	}
	opts := builder.Ordered{{K: "", V: "— Theme location —"}}
	for _, m := range list {
		opts = append(opts, builder.KV{K: itoa64(m.ID), V: m.Name})
	}
	return map[string]any{"type": "select", "tab": "content", "section": "section_layout", "label": "Menu", "options": opts,
		"default": itoa64(list[0].ID), "save_default": true, "separator": "after", "name": "menu",
		"description": `Go to the <a href="` + s.links(c).HomeURL("/wp-admin/nav-menus") + `" target="_blank">Menus screen</a> to manage your menus.`}
}

// gweProWidgetsConfig patches get_widgets_config's payload.
func (s *Server) gweProWidgetsConfig(c *gin.Context, cfg []byte) []byte {
	cfg = s.gweIntegrationsWidgetsConfig(c, cfg) // gap-integrations: settings links, API key alerts
	obj, err := gweOrderedRaw(cfg)
	if err != nil {
		return cfg
	}
	for i := range obj {
		if obj[i].K != "nav-menu" {
			continue
		}
		var w map[string]json.RawMessage
		if json.Unmarshal(obj[i].V, &w) != nil {
			break
		}
		ctrls, err := gweOrderedRaw(w["controls"])
		if err != nil {
			break
		}
		if ctl := s.gweProMenuControl(c); ctl != nil {
			raw, _ := json.Marshal(ctl)
			ctrls = ctrls.set("menu", raw)
		}
		// Theme Location: used when no menu is chosen (pages saved with the Site Nav Menu use it).
		raw, _ := json.Marshal(s.gweProMenuLocationControl())
		ctrls = ctrls.insertAfter("menu", "menu_location", raw)
		w["controls"], _ = json.Marshal(ctrls)
		obj[i].V, _ = json.Marshal(w)
	}
	for _, w := range builder.ProFExtraWidgets() {
		entry := map[string]json.RawMessage{"controls": w.ControlsJSON(),
			"tabs_controls": json.RawMessage(`{"content":"Content","style":"Style","advanced":"Advanced"}`)}
		raw, _ := json.Marshal(entry)
		obj = obj.set(w.Name, raw)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return cfg
	}
	return out
}

// gweProDocWidgets adds the extra Pro widgets to a document's panel widget list.
func gweProDocWidgets(widgets json.RawMessage) json.RawMessage {
	obj, err := gweOrderedRaw(widgets)
	if err != nil {
		return widgets
	}
	for _, w := range builder.ProFExtraWidgets() {
		cats := w.Categories
		if len(cats) == 0 {
			cats = []string{"theme-elements"}
		}
		entry := map[string]any{"name": w.Name, "elType": "widget", "title": w.Title, "icon": w.Icon, "reload_preview": false,
			"help_url": "#", "widget_type": w.Name, "keywords": w.Keywords, "categories": cats,
			"html_wrapper_class": "elementor-widget-" + w.Name, "show_in_panel": true, "hide_on_search": false, "upsale_data": nil,
			"is_dynamic_content": true, "has_widget_inner_wrapper": true, "controls": w.ControlsJSON(),
			"tabs_controls": json.RawMessage(`{"content":"Content","style":"Style","advanced":"Advanced"}`)}
		raw, _ := json.Marshal(entry)
		obj = obj.set(w.Name, raw)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return widgets
	}
	return out
}

// gweProPostType is the wrapper's data-elementor-post-type (theme parts are library templates upstream).
func gweProPostType(p *store.Post) string {
	if p.Type == gwePartType {
		return gweLibrary
	}
	return p.Type
}

// gweProMenuLocationControl is Nav Menu's theme location select.
func (s *Server) gweProMenuLocationControl() map[string]any {
	locs := builder.Ordered{}
	seen := map[string]bool{}
	for _, l := range append(append([]MenuLocation{}, s.activeTheme().MenuLocations...), gweMenuLocations...) {
		if !seen[l.Slug] {
			seen[l.Slug] = true
			locs = append(locs, builder.KV{K: l.Slug, V: l.Name})
		}
	}
	return map[string]any{"type": "select", "tab": "content", "section": "section_layout", "label": "Theme Location", "options": locs,
		"default": "primary", "name": "menu_location", "condition": map[string]any{"menu": ""},
		"description": "Shown when no menu is selected: the menu assigned to this location in Appearance › Menus."}
}
