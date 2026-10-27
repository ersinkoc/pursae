package server

import (
	"context"
	"sync"

	"wpgo/internal/store"
)

// The kit is the site-wide GoWebEditor document behind Site Settings: global colors and fonts, theme
// style, layout, breakpoints and site identity. Like upstream it is a template post (type
// elementor_library, _elementor_template_type=kit) whose id is the elementor_active_kit option; it is
// created on first use and saved through save_builder with the kit's id as editor_post_id.

var gweKitMu sync.Mutex

// gweKitDefaults are the settings of a fresh kit.
func (s *Server) gweKitDefaults() map[string]any {
	color := func(id, title, c string) map[string]any { return map[string]any{"_id": id, "title": title, "color": c} }
	font := func(id, title, family, weight string) map[string]any {
		return map[string]any{"_id": id, "title": title, "typography_typography": "custom", "typography_font_family": family, "typography_font_weight": weight}
	}
	return map[string]any{
		"system_colors": []any{color("primary", "Primary", "#6EC1E4"), color("secondary", "Secondary", "#54595F"),
			color("text", "Text", "#7A7A7A"), color("accent", "Accent", "#61CE70")},
		"custom_colors": []any{},
		"system_typography": []any{font("primary", "Primary", "Roboto", "600"), font("secondary", "Secondary", "Roboto Slab", "400"),
			font("text", "Text", "Roboto", "400"), font("accent", "Accent", "Roboto", "500")},
		"custom_typography":     []any{},
		"default_generic_fonts": "Sans-serif",
		"page_title_selector":   "h1.entry-title",
		"viewport_md":           768,
		"viewport_lg":           1025,
	}
}

// gweKit returns the active kit, creating it on first use.
func (s *Server) gweKit(ctx context.Context) (*store.Post, error) {
	if k, ok := ctx.Value(ctxGweKit).(*store.Post); ok && k != nil { // live preview of a custom theme
		return k, nil
	}
	if id := int64(s.settings.Int("elementor_active_kit")); id > 0 {
		if p, err := s.store.GetPost(ctx, id); err == nil && p.Status != "trash" {
			return p, nil
		}
	}
	gweKitMu.Lock()
	defer gweKitMu.Unlock()
	if id := int64(s.settings.Int("elementor_active_kit")); id > 0 {
		if p, err := s.store.GetPost(ctx, id); err == nil && p.Status != "trash" {
			return p, nil
		}
	}
	if err := s.gweEnsureLibraryType(ctx); err != nil {
		return nil, err
	}
	var authorID int64
	_ = s.store.Pool.QueryRow(ctx, `SELECT coalesce(min(user_id),0) FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE r.slug = 'administrator'`).Scan(&authorID)
	kit := &store.Post{Type: gweLibrary, Title: "Default Kit", Slug: "default-kit", Status: "publish", AuthorID: authorID,
		CommentStatus: "closed", PingStatus: "closed",
		Meta: map[string]any{
			"_elementor_edit_mode": "builder", "_elementor_template_type": "kit", "_elementor_version": gweVersion,
			"_elementor_data": "[]", "_elementor_page_settings": s.gweKitDefaults(),
		}}
	if err := s.store.InsertPost(ctx, kit); err != nil {
		return nil, err
	}
	if err := s.settings.Set(ctx, map[string]any{"elementor_active_kit": kit.ID}); err != nil {
		return nil, err
	}
	return kit, nil
}

// gweKitSettings returns the active kit's settings (empty when there is no kit yet).
func (s *Server) gweKitSettings(ctx context.Context) (*store.Post, map[string]any) {
	kit, err := s.gweKit(ctx)
	if err != nil {
		return nil, map[string]any{}
	}
	return kit, gwePageSettings(kit)
}

// gweGlobals is the elementor/v1/globals payload: the kit's system and custom colors and fonts.
func gweGlobals(settings map[string]any) map[string]any {
	colors := map[string]any{}
	typography := map[string]any{}
	for _, key := range []string{"system_colors", "custom_colors"} {
		list, _ := settings[key].([]any)
		for _, it := range list {
			m, _ := it.(map[string]any)
			id, _ := m["_id"].(string)
			if id == "" {
				continue
			}
			colors[id] = map[string]any{"id": id, "title": m["title"], "value": m["color"]}
		}
	}
	for _, key := range []string{"system_typography", "custom_typography"} {
		list, _ := settings[key].([]any)
		for _, it := range list {
			m, _ := it.(map[string]any)
			id, _ := m["_id"].(string)
			if id == "" {
				continue
			}
			val := map[string]any{}
			for k, v := range m {
				if k != "_id" && k != "title" {
					val[k] = v
				}
			}
			typography[id] = map[string]any{"id": id, "title": m["title"], "value": val}
		}
	}
	return map[string]any{"colors": colors, "typography": typography}
}
