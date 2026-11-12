package wpgo

import "encoding/json"

// Admin UI and editor registrations: admin pages, dashboard widgets, meta boxes, dynamic blocks
// and Pursae Web Builder widgets. Each needs its own permission in plugin.json (admin:pages,
// admin:dashboard, admin:metaboxes, blocks:register, builder:widgets). What the callbacks return
// is HTML; the core sanitises it (no scripts) unless the plugin holds admin:scripts (admin
// output) or content:html (front-end output).

// AdminRequest is what an admin page, dashboard widget or meta box callback receives.
type AdminRequest struct {
	Page   string            `json:"page,omitempty"`    // admin page: its slug ("" for the main page)
	Method string            `json:"method"`            // GET or POST (POSTs already passed the nonce check)
	Query  map[string]string `json:"query"`             // URL query, first value of each key
	Form   map[string]string `json:"form"`              // POST fields, first value of each key
	PostID int64             `json:"post_id,omitempty"` // meta box: the edited post (0 when new)
	Values map[string]any    `json:"values,omitempty"`  // meta box: stored values of its fields
	User   *User             `json:"user"`
}

// AdminResponse is what an admin page callback returns. Notices are printed above the page
// (types info, success, warning, error; plain text).
type AdminResponse struct {
	HTML    string       `json:"html"`
	Notices []PageNotice `json:"notices,omitempty"`
}

// PageNotice is a notice shown above a plugin admin page.
type PageNotice struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// AdminPageFunc renders a plugin admin page. The page body is wrapped by the core in a
// <form method="post"> carrying a nonce, so plain inputs and a submit button post back to the
// same page with Method "POST".
type AdminPageFunc func(ctx Context, req AdminRequest) AdminResponse

// AdminPage describes a page in the admin menu (register_admin_page). Its URL is
// /wp-admin/admin?page=<plugin slug> or, with a Slug, /wp-admin/admin?page=<plugin slug>.<Slug>.
type AdminPage struct {
	Slug       string // "" for the plugin's main page
	PageTitle  string
	MenuTitle  string
	Capability string // default manage_options
	// Parent is "" for a top-level menu, "self" for a sub page under the plugin's main page, or
	// one of dashboard, posts, media, pages, comments, appearance, plugins, users, tools, settings.
	Parent string
	Icon   string // top-level only: a dashicons class such as dashicons-chart-bar
	Render AdminPageFunc
}

// RegisterAdminPage adds a page to the admin menu. Requires "admin:pages".
func RegisterAdminPage(page AdminPage) {
	name := addCallback("admin_page", page.Slug, func(ctx Context, _ json.RawMessage, _ []any, raw []json.RawMessage) (json.RawMessage, error) {
		var req AdminRequest
		if len(raw) > 0 {
			_ = json.Unmarshal(raw[0], &req)
		}
		if page.Render == nil {
			return json.Marshal(AdminResponse{})
		}
		return json.Marshal(page.Render(ctx, req))
	})
	queueRegistration(func() {
		if _, err := hostCall("register_admin_page", map[string]any{"slug": page.Slug, "page_title": page.PageTitle,
			"menu_title": page.MenuTitle, "capability": page.Capability, "parent": page.Parent, "icon": page.Icon, "callback": name}); err != nil {
			Log("error", "register_admin_page: "+err.Error())
		}
	})
}

// DashboardWidgetFunc renders a dashboard widget's HTML.
type DashboardWidgetFunc func(ctx Context, req AdminRequest) string

// RegisterDashboardWidget adds a box to the Dashboard; capability ("" = read) limits who sees it.
// Requires "admin:dashboard".
func RegisterDashboardWidget(id, title, capability string, render DashboardWidgetFunc) {
	name := addCallback("dashboard_widget", id, func(ctx Context, _ json.RawMessage, _ []any, raw []json.RawMessage) (json.RawMessage, error) {
		var req AdminRequest
		if len(raw) > 0 {
			_ = json.Unmarshal(raw[0], &req)
		}
		return json.Marshal(render(ctx, req))
	})
	queueRegistration(func() {
		if _, err := hostCall("register_dashboard_widget", map[string]any{"id": id, "title": title, "capability": capability, "callback": name}); err != nil {
			Log("error", "register_dashboard_widget "+id+": "+err.Error())
		}
	})
}

// MetaBox describes a box on the post edit screen (classic editor and Pursae Editor). The core
// renders and saves its Fields as post meta named plugin_<slug>_<field id> (read them with
// GetPost(...).Meta). Render (optional) prints extra HTML above the fields; it is sanitised
// without form controls. OnSave (optional) runs after the fields were stored.
type MetaBox struct {
	ID        string
	Title     string
	PostTypes []string // default post and page
	Context   string   // normal (default) or side
	Fields    []SettingsField
	Render    func(ctx Context, req AdminRequest) string
	OnSave    func(ctx Context, postID int64, values map[string]any)
}

// RegisterMetaBox adds a meta box. Requires "admin:metaboxes".
func RegisterMetaBox(box MetaBox) {
	reg := map[string]any{"id": box.ID, "title": box.Title, "post_types": box.PostTypes, "context": box.Context, "fields": box.Fields}
	if box.Render != nil {
		reg["callback"] = addCallback("meta_box", box.ID, func(ctx Context, _ json.RawMessage, _ []any, raw []json.RawMessage) (json.RawMessage, error) {
			var req AdminRequest
			if len(raw) > 0 {
				_ = json.Unmarshal(raw[0], &req)
			}
			return json.Marshal(box.Render(ctx, req))
		})
	}
	if box.OnSave != nil {
		reg["save_callback"] = addCallback("meta_box_save", box.ID, func(ctx Context, _ json.RawMessage, _ []any, raw []json.RawMessage) (json.RawMessage, error) {
			var id int64
			values := map[string]any{}
			if len(raw) > 0 {
				_ = json.Unmarshal(raw[0], &id)
			}
			if len(raw) > 1 {
				_ = json.Unmarshal(raw[1], &values)
			}
			box.OnSave(ctx, id, values)
			return json.RawMessage("null"), nil
		})
	}
	queueRegistration(func() {
		if _, err := hostCall("register_meta_box", reg); err != nil {
			Log("error", "register_meta_box "+box.ID+": "+err.Error())
		}
	})
}

// RenderFunc renders a block or a Web Builder widget for visitors: the attributes / settings
// (declared ones only, defaults filled in) and, for blocks, the rendered inner content.
type RenderFunc func(ctx Context, attrs map[string]any, content string) string

// Block describes a dynamic block for Pursae Editor. Name is "<plugin slug>/<name>" (or just
// <name>; the slug is added). Attributes become inspector controls in the editor; the block is
// previewed and rendered on the server by Render.
type Block struct {
	Name        string
	Title       string
	Description string
	Category    string // text, media, design, widgets (default), theme, embed
	Icon        string // a dashicon name such as chart-bar
	Keywords    []string
	Attributes  []SettingsField
	Render      RenderFunc
}

func renderCallback(kind, id string, fn RenderFunc) string {
	return addCallback(kind, id, func(ctx Context, _ json.RawMessage, _ []any, raw []json.RawMessage) (json.RawMessage, error) {
		attrs := map[string]any{}
		content := ""
		if len(raw) > 0 {
			_ = json.Unmarshal(raw[0], &attrs)
		}
		if len(raw) > 1 {
			_ = json.Unmarshal(raw[1], &content)
		}
		return json.Marshal(fn(ctx, attrs, content))
	})
}

// RegisterBlock registers a dynamic block. Requires "blocks:register".
func RegisterBlock(b Block) {
	name := renderCallback("block", b.Name, b.Render)
	queueRegistration(func() {
		if _, err := hostCall("register_block", map[string]any{"name": b.Name, "title": b.Title, "description": b.Description,
			"category": b.Category, "icon": b.Icon, "keywords": b.Keywords, "attributes": b.Attributes, "callback": name}); err != nil {
			Log("error", "register_block "+b.Name+": "+err.Error())
		}
	})
}

// BuilderWidget describes a Pursae Web Builder widget; its type is "<plugin slug>/<ID>". Controls
// are shown in the builder's panel; Render runs on the server for every page view (content is "").
type BuilderWidget struct {
	ID          string // lowercase letters, digits and -
	Title       string
	Description string
	Icon        string
	Controls    []SettingsField
	Render      RenderFunc
}

// RegisterBuilderWidget registers a Web Builder widget. Requires "builder:widgets".
func RegisterBuilderWidget(w BuilderWidget) {
	name := renderCallback("builder_widget", w.ID, w.Render)
	queueRegistration(func() {
		if _, err := hostCall("register_builder_widget", map[string]any{"id": w.ID, "title": w.Title, "description": w.Description,
			"icon": w.Icon, "controls": w.Controls, "callback": name}); err != nil {
			Log("error", "register_builder_widget "+w.ID+": "+err.Error())
		}
	})
}
