package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"wpgo/internal/wp"
)

// UI registrations: admin pages, dashboard widgets, meta boxes, dynamic blocks and Pursae Web
// Builder widgets. Like shortcodes and routes they live in the plugin (guarded by p.state), are
// re-sent by the guest after a re-instantiation and are read by the core through the Runtime
// lookups at the end of this file.
//
// Everything a plugin prints through them is HTML of the plugin's own making. Admin output (pages,
// dashboard widgets, meta boxes) goes through wp.KsesAdmin unless the plugin holds admin:scripts;
// front-end output (blocks, builder widgets) goes through KSES unless it holds content:html, the
// same rule as shortcodes.

// AdminPage is a page a plugin adds to the admin menu (register_admin_page). Its URL is
// /wp-admin/admin?page=<plugin slug> or, with a Slug, /wp-admin/admin?page=<plugin slug>.<Slug>.
type AdminPage struct {
	Slug       string `json:"slug,omitempty"`       // "" for the plugin's main page
	PageTitle  string `json:"page_title"`           // <h1> and the browser title
	MenuTitle  string `json:"menu_title"`           // the menu label
	Capability string `json:"capability,omitempty"` // who may open it; default manage_options
	Parent     string `json:"parent,omitempty"`     // "" = a top-level menu, "self" = under the plugin's top-level page, or one of AdminPageParents
	Icon       string `json:"icon,omitempty"`       // top-level only: a dashicons class, e.g. dashicons-chart-bar
	Callback   string `json:"callback"`
}

// AdminPageParents are the core menus a plugin page can sit under, with their menu ids.
var AdminPageParents = map[string]string{
	"dashboard": "menu-dashboard", "posts": "menu-posts", "media": "menu-media", "pages": "menu-pages",
	"comments": "menu-comments", "appearance": "menu-appearance", "plugins": "menu-plugins",
	"users": "menu-users", "tools": "menu-tools", "settings": "menu-settings",
}

// AdminPageID is the value of ?page= for a plugin's page.
func AdminPageID(plugin, slug string) string {
	if slug == "" {
		return plugin
	}
	return plugin + "." + slug
}

// AdminRequest is what an admin page, dashboard widget or meta box render callback receives.
type AdminRequest struct {
	Page   string            `json:"page,omitempty"`    // admin page: the page slug ("" for the main page)
	Method string            `json:"method"`            // GET or POST
	Query  map[string]string `json:"query"`             // URL query (first value each)
	Form   map[string]string `json:"form"`              // POST fields (first value each), after the nonce check
	PostID int64             `json:"post_id,omitempty"` // meta box: the post being edited (0 for a new one)
	Values map[string]any    `json:"values,omitempty"`  // meta box: the stored values of its fields and inputs
	User   *UserInfo         `json:"user"`
}

// AdminResponse is what such a callback returns. A bare JSON string is accepted as HTML.
type AdminResponse struct {
	HTML    string        `json:"html"`
	Notices []AdminNotice `json:"notices,omitempty"` // admin pages only: shown above the page
}

// AdminNotice is a notice printed above a plugin admin page. Message is plain text.
type AdminNotice struct {
	Type    string `json:"type"` // info, success, warning or error
	Message string `json:"message"`
}

// DashboardWidget is a box on the Dashboard (register_dashboard_widget).
type DashboardWidget struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Capability string `json:"capability,omitempty"` // default read (everyone who sees the Dashboard)
	Callback   string `json:"callback"`
}

// MetaBox is a box on the post edit screen (register_meta_box). Fields are rendered and saved by
// the core into post meta named plugin_<slug>_<field id>; Callback (optional) prints extra HTML
// above them and SaveCallback (optional) runs after the fields were stored.
type MetaBox struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	PostTypes    []string        `json:"post_types,omitempty"` // default post and page
	Context      string          `json:"context,omitempty"`    // normal (default) or side
	Fields       []SettingsField `json:"fields,omitempty"`
	Callback     string          `json:"callback,omitempty"`
	SaveCallback string          `json:"save_callback,omitempty"`
}

// MetaKey is the post meta key a meta box field is stored under.
func (f SettingsField) MetaKey(plugin string) string { return SettingsOptionPrefix(plugin) + f.ID }

// ForType reports whether the box shows for a post type.
func (b MetaBox) ForType(postType string) bool {
	for _, t := range b.PostTypes {
		if t == postType {
			return true
		}
	}
	return false
}

// BlockType is a dynamic block (register_block): the editor shows the Attributes as inspector
// controls and previews the block through the server, which calls Callback.
type BlockType struct {
	Name        string          `json:"name"` // <plugin slug>/<name>
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Category    string          `json:"category,omitempty"` // text, media, design, widgets (default), theme, embed
	Icon        string          `json:"icon,omitempty"`     // a dashicon name without the prefix, e.g. chart-bar
	Keywords    []string        `json:"keywords,omitempty"`
	Attributes  []SettingsField `json:"attributes,omitempty"`
	Callback    string          `json:"callback"`
}

// BuilderWidget is a Pursae Web Builder widget (register_builder_widget): a static schema of
// controls plus a server render callback that runs for every request of a page that uses it.
type BuilderWidget struct {
	ID          string          `json:"id"` // unique per plugin; the widget type is <plugin slug>/<id>
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Icon        string          `json:"icon,omitempty"` // a dashicon name without the prefix
	Controls    []SettingsField `json:"controls,omitempty"`
	Callback    string          `json:"callback"`
}

// Limits of the UI registrations.
const (
	maxAdminPages       = 20
	maxDashboardWidgets = 10
	maxMetaBoxes        = 20
	maxBlockTypes       = 50
	maxBuilderWidgets   = 50
	// MaxUIOutput caps the HTML one render callback may return.
	MaxUIOutput = 1 << 20
	// MaxUIInput caps the attributes / settings / form JSON sent to a render callback.
	MaxUIInput = 256 << 10
)

var (
	capRe           = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	iconRe          = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
	blockNameRe     = regexp.MustCompile(`^[a-z0-9-]+/[a-z0-9-]+$`)
	blockCategories = map[string]bool{"text": true, "media": true, "design": true, "widgets": true, "theme": true, "embed": true}
)

// sanitizeAdminHTML is the admin sanitiser (a variable for tests).
var sanitizeAdminHTML = wp.KsesAdmin

// sanitizeAdmin sanitises what a plugin prints in the admin unless it may run code there.
func (p *Plugin) sanitizeAdmin(out string) string {
	if p.perms.Has(PermAdminScripts) || out == "" {
		return out
	}
	return sanitizeAdminHTML(out)
}

// checkFields validates a list of declarative fields (settings pages, meta boxes, block
// attributes, widget controls). what names the owner in error messages.
func checkFields(fields []SettingsField, min, max int, what string) error {
	if len(fields) < min || len(fields) > max {
		return invalid("%s needs %d-%d fields", what, min, max)
	}
	seen := map[string]bool{}
	for i := range fields {
		f := &fields[i]
		if !fieldIDRe.MatchString(f.ID) {
			return invalid("field id %q must be 1-64 lowercase letters, digits or _", f.ID)
		}
		if seen[f.ID] {
			return invalid("field %q is listed twice", f.ID)
		}
		seen[f.ID] = true
		if !fieldTypes[f.Type] {
			return invalid("field %q: type must be one of %s", f.ID, strings.Join(FieldTypes(), ", "))
		}
		if strings.TrimSpace(f.Label) == "" {
			f.Label = f.ID
		}
		if len(f.Label) > maxLabel || len(f.Placeholder) > maxLabel || len(f.Description) > maxDescription {
			return invalid("field %q: label and placeholder are limited to %d characters, the description to %d", f.ID, maxLabel, maxDescription)
		}
		if f.Type == "select" || f.Type == "radio" {
			if len(f.Options) == 0 || len(f.Options) > 100 {
				return invalid("field %q: a %s needs 1-100 options", f.ID, f.Type)
			}
			for _, o := range f.Options {
				if len(o.Value) > maxLabel || len(o.Label) > maxLabel {
					return invalid("field %q: option values and labels are limited to %d characters", f.ID, maxLabel)
				}
			}
		} else if len(f.Options) > 0 {
			return invalid("field %q: only select and radio fields take options", f.ID)
		}
		if raw, _ := json.Marshal(f.Default); len(raw) > 4096 {
			return invalid("field %q: default is too large", f.ID)
		}
	}
	return nil
}

// FieldTypes lists the declarative field types, sorted.
func FieldTypes() []string {
	out := make([]string, 0, len(fieldTypes))
	for t := range fieldTypes {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func checkCallback(name string) error {
	if name == "" || len(name) > maxCallbackName {
		return invalid("callback name must be 1-%d characters", maxCallbackName)
	}
	return nil
}

func checkTitle(title, what string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || len(title) > maxLabel {
		return "", invalid("%s must be 1-%d characters", what, maxLabel)
	}
	return title, nil
}

func checkCapability(c, def string) (string, error) {
	if c == "" {
		return def, nil
	}
	if !capRe.MatchString(c) {
		return "", invalid("capability %q is not a capability name", c)
	}
	return c, nil
}

func hostRegisterAdminPage(_ context.Context, p *Plugin, req []byte) (any, error) {
	var pg AdminPage
	if err := decodeRequest(req, &pg); err != nil {
		return nil, err
	}
	if pg.Slug != "" && !handleRe.MatchString(pg.Slug) {
		return nil, invalid("slug %q must be 1-64 lowercase letters, digits, _ or -", pg.Slug)
	}
	pg.MenuTitle, pg.PageTitle = strings.TrimSpace(pg.MenuTitle), strings.TrimSpace(pg.PageTitle)
	if pg.MenuTitle == "" {
		pg.MenuTitle = p.manifest.Name
	}
	if pg.PageTitle == "" {
		pg.PageTitle = pg.MenuTitle
	}
	if len(pg.MenuTitle) > maxLabel || len(pg.PageTitle) > maxLabel {
		return nil, invalid("titles must be at most %d characters", maxLabel)
	}
	var err error
	if pg.Capability, err = checkCapability(pg.Capability, "manage_options"); err != nil {
		return nil, err
	}
	if pg.Parent == "self" && pg.Slug == "" {
		return nil, invalid(`the main page (no slug) cannot have parent "self"`)
	}
	if pg.Parent != "" && pg.Parent != "self" && AdminPageParents[pg.Parent] == "" {
		return nil, invalid("parent %q is not an admin menu (self, dashboard, posts, media, pages, comments, appearance, plugins, users, tools, settings)", pg.Parent)
	}
	if pg.Icon != "" && (!iconRe.MatchString(pg.Icon) || !strings.HasPrefix(pg.Icon, "dashicons-")) {
		return nil, invalid("icon must be a dashicons class such as dashicons-chart-bar")
	}
	if err := checkCallback(pg.Callback); err != nil {
		return nil, err
	}
	p.state.Lock()
	defer p.state.Unlock()
	if p.disabled.Load() {
		return nil, ErrPluginDisabled
	}
	for i, old := range p.adminPages {
		if old.Slug == pg.Slug {
			p.adminPages[i] = pg
			return map[string]any{"page": AdminPageID(p.Slug(), pg.Slug)}, nil
		}
	}
	if len(p.adminPages) >= maxAdminPages {
		return nil, invalid("a plugin may register at most %d admin pages", maxAdminPages)
	}
	p.adminPages = append(p.adminPages, pg)
	return map[string]any{"page": AdminPageID(p.Slug(), pg.Slug)}, nil
}

func hostRegisterDashboardWidget(_ context.Context, p *Plugin, req []byte) (any, error) {
	var w DashboardWidget
	if err := decodeRequest(req, &w); err != nil {
		return nil, err
	}
	if !handleRe.MatchString(w.ID) {
		return nil, invalid("id %q must be 1-64 lowercase letters, digits, _ or -", w.ID)
	}
	var err error
	if w.Title, err = checkTitle(w.Title, "title"); err != nil {
		return nil, err
	}
	if w.Capability, err = checkCapability(w.Capability, "read"); err != nil {
		return nil, err
	}
	if err := checkCallback(w.Callback); err != nil {
		return nil, err
	}
	p.state.Lock()
	defer p.state.Unlock()
	if p.disabled.Load() {
		return nil, ErrPluginDisabled
	}
	for i, old := range p.dashWidgets {
		if old.ID == w.ID {
			p.dashWidgets[i] = w
			return nil, nil
		}
	}
	if len(p.dashWidgets) >= maxDashboardWidgets {
		return nil, invalid("a plugin may register at most %d dashboard widgets", maxDashboardWidgets)
	}
	p.dashWidgets = append(p.dashWidgets, w)
	return nil, nil
}

func hostRegisterMetaBox(_ context.Context, p *Plugin, req []byte) (any, error) {
	var b MetaBox
	if err := decodeRequest(req, &b); err != nil {
		return nil, err
	}
	if !handleRe.MatchString(b.ID) {
		return nil, invalid("id %q must be 1-64 lowercase letters, digits, _ or -", b.ID)
	}
	var err error
	if b.Title, err = checkTitle(b.Title, "title"); err != nil {
		return nil, err
	}
	if len(b.PostTypes) == 0 {
		b.PostTypes = []string{"post", "page"}
	}
	if len(b.PostTypes) > 20 {
		return nil, invalid("at most 20 post types")
	}
	for _, t := range b.PostTypes {
		if !postTypeRe.MatchString(t) {
			return nil, invalid("post type %q is not a post type name", t)
		}
	}
	switch b.Context {
	case "":
		b.Context = "normal"
	case "normal", "side":
	default:
		return nil, invalid(`context must be "normal" or "side"`)
	}
	if err := checkFields(b.Fields, 0, maxSettingsField, "a meta box"); err != nil {
		return nil, err
	}
	if len(b.Fields) == 0 && b.Callback == "" {
		return nil, invalid("a meta box needs fields or a render callback")
	}
	if len(b.Callback) > maxCallbackName || len(b.SaveCallback) > maxCallbackName {
		return nil, invalid("callback names are limited to %d characters", maxCallbackName)
	}
	p.state.Lock()
	defer p.state.Unlock()
	if p.disabled.Load() {
		return nil, ErrPluginDisabled
	}
	res := map[string]any{"meta_prefix": SettingsOptionPrefix(p.Slug())}
	for i, old := range p.metaBoxes {
		if old.ID == b.ID {
			p.metaBoxes[i] = b
			return res, nil
		}
	}
	if len(p.metaBoxes) >= maxMetaBoxes {
		return nil, invalid("a plugin may register at most %d meta boxes", maxMetaBoxes)
	}
	p.metaBoxes = append(p.metaBoxes, b)
	return res, nil
}

func hostRegisterBlock(_ context.Context, p *Plugin, req []byte) (any, error) {
	var b BlockType
	if err := decodeRequest(req, &b); err != nil {
		return nil, err
	}
	if !strings.Contains(b.Name, "/") {
		b.Name = p.Slug() + "/" + b.Name
	}
	if len(b.Name) > 128 || !blockNameRe.MatchString(b.Name) || !strings.HasPrefix(b.Name, p.Slug()+"/") {
		return nil, invalid("block name must be %q followed by lowercase letters, digits or -, got %q", p.Slug()+"/", b.Name)
	}
	var err error
	if b.Title, err = checkTitle(b.Title, "title"); err != nil {
		return nil, err
	}
	if len(b.Description) > maxDescription {
		return nil, invalid("description is longer than %d characters", maxDescription)
	}
	if b.Category == "" {
		b.Category = "widgets"
	}
	if !blockCategories[b.Category] {
		return nil, invalid("category must be text, media, design, widgets, theme or embed")
	}
	if b.Icon != "" && !iconRe.MatchString(b.Icon) {
		return nil, invalid("icon must be a dashicon name such as chart-bar")
	}
	if len(b.Keywords) > 10 {
		return nil, invalid("at most 10 keywords")
	}
	for _, k := range b.Keywords {
		if len(k) > 64 {
			return nil, invalid("keywords are limited to 64 characters")
		}
	}
	if err := checkFields(b.Attributes, 0, maxSettingsField, "a block"); err != nil {
		return nil, err
	}
	if err := checkCallback(b.Callback); err != nil {
		return nil, err
	}
	p.state.Lock()
	defer p.state.Unlock()
	if p.disabled.Load() {
		return nil, ErrPluginDisabled
	}
	for i, old := range p.blocks {
		if old.Name == b.Name {
			p.blocks[i] = b
			return nil, nil
		}
	}
	if len(p.blocks) >= maxBlockTypes {
		return nil, invalid("a plugin may register at most %d blocks", maxBlockTypes)
	}
	p.blocks = append(p.blocks, b)
	return nil, nil
}

func hostRegisterBuilderWidget(_ context.Context, p *Plugin, req []byte) (any, error) {
	var w BuilderWidget
	if err := decodeRequest(req, &w); err != nil {
		return nil, err
	}
	if !handleRe.MatchString(w.ID) || strings.Contains(w.ID, "_") {
		return nil, invalid("id %q must be 1-64 lowercase letters, digits or -", w.ID)
	}
	var err error
	if w.Title, err = checkTitle(w.Title, "title"); err != nil {
		return nil, err
	}
	if len(w.Description) > maxDescription {
		return nil, invalid("description is longer than %d characters", maxDescription)
	}
	if w.Icon != "" && !iconRe.MatchString(w.Icon) {
		return nil, invalid("icon must be a dashicon name such as chart-bar")
	}
	if err := checkFields(w.Controls, 0, maxSettingsField, "a widget"); err != nil {
		return nil, err
	}
	if err := checkCallback(w.Callback); err != nil {
		return nil, err
	}
	p.state.Lock()
	defer p.state.Unlock()
	if p.disabled.Load() {
		return nil, ErrPluginDisabled
	}
	res := map[string]any{"type": p.Slug() + "/" + w.ID}
	for i, old := range p.builderWidgets {
		if old.ID == w.ID {
			p.builderWidgets[i] = w
			return res, nil
		}
	}
	if len(p.builderWidgets) >= maxBuilderWidgets {
		return nil, invalid("a plugin may register at most %d Web Builder widgets", maxBuilderWidgets)
	}
	p.builderWidgets = append(p.builderWidgets, w)
	return res, nil
}

// Accessors ------------------------------------------------------------------------

// AdminPages returns the plugin's admin pages.
func (p *Plugin) AdminPages() []AdminPage {
	p.state.Lock()
	defer p.state.Unlock()
	return append([]AdminPage(nil), p.adminPages...)
}

// DashboardWidgets returns the plugin's dashboard widgets.
func (p *Plugin) DashboardWidgets() []DashboardWidget {
	p.state.Lock()
	defer p.state.Unlock()
	return append([]DashboardWidget(nil), p.dashWidgets...)
}

// MetaBoxes returns the plugin's meta boxes.
func (p *Plugin) MetaBoxes() []MetaBox {
	p.state.Lock()
	defer p.state.Unlock()
	return append([]MetaBox(nil), p.metaBoxes...)
}

// BlockTypes returns the plugin's dynamic blocks.
func (p *Plugin) BlockTypes() []BlockType {
	p.state.Lock()
	defer p.state.Unlock()
	return append([]BlockType(nil), p.blocks...)
}

// BuilderWidgets returns the plugin's Web Builder widgets.
func (p *Plugin) BuilderWidgets() []BuilderWidget {
	p.state.Lock()
	defer p.state.Unlock()
	return append([]BuilderWidget(nil), p.builderWidgets...)
}

// Rendering ------------------------------------------------------------------------

// renderAdmin runs an admin render callback and returns its (sanitised) response.
func (p *Plugin) renderAdmin(ctx context.Context, kind, id, callback string, req AdminRequest) (*AdminResponse, error) {
	if req.Query == nil {
		req.Query = map[string]string{}
	}
	if req.Form == nil {
		req.Form = map[string]string{}
	}
	if req.Method == "" {
		req.Method = "GET"
	}
	if raw, _ := json.Marshal(req); len(raw) > MaxUIInput {
		return nil, fmt.Errorf("request exceeds %d bytes", MaxUIInput)
	}
	raw, err := p.call(ctx, &handleRequest{Plugin: p.Slug(), Callback: callback, Hook: id, Type: kind, Args: []any{req}})
	if err != nil {
		return nil, err
	}
	var resp AdminResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		var html string
		if json.Unmarshal(raw, &html) != nil {
			p.logs.add("warning", "runtime", fmt.Sprintf("%s %s returned neither HTML nor a response object", kind, id))
			return nil, errors.New("render callback must return a string or {html, notices}")
		}
		resp = AdminResponse{HTML: html}
	}
	if len(resp.HTML) > MaxUIOutput {
		return nil, fmt.Errorf("output exceeds %d bytes", MaxUIOutput)
	}
	resp.HTML = p.sanitizeAdmin(resp.HTML)
	if len(resp.Notices) > 10 {
		resp.Notices = resp.Notices[:10]
	}
	for i := range resp.Notices {
		n := &resp.Notices[i]
		switch n.Type {
		case "info", "success", "warning", "error":
		default:
			n.Type = "info"
		}
		if len(n.Message) > maxNoticeMessage {
			n.Message = n.Message[:maxNoticeMessage]
		}
	}
	return &resp, nil
}

// RenderAdminPage runs the render callback of the admin page slug.
func (p *Plugin) RenderAdminPage(ctx context.Context, slug string, req AdminRequest) (*AdminResponse, error) {
	for _, pg := range p.AdminPages() {
		if pg.Slug == slug {
			req.Page = slug
			return p.renderAdmin(ctx, "admin_page", AdminPageID(p.Slug(), slug), pg.Callback, req)
		}
	}
	return nil, fmt.Errorf("admin page %q is not registered by %s", slug, p.Slug())
}

// RenderDashboardWidget runs the render callback of a dashboard widget and returns its HTML.
func (p *Plugin) RenderDashboardWidget(ctx context.Context, id string, user *UserInfo) (string, error) {
	for _, w := range p.DashboardWidgets() {
		if w.ID == id {
			resp, err := p.renderAdmin(ctx, "dashboard_widget", id, w.Callback, AdminRequest{Method: "GET", User: user})
			if err != nil {
				return "", err
			}
			return resp.HTML, nil
		}
	}
	return "", fmt.Errorf("dashboard widget %q is not registered by %s", id, p.Slug())
}

// RenderMetaBox runs the optional render callback of a meta box ("" when it has none).
func (p *Plugin) RenderMetaBox(ctx context.Context, id string, postID int64, values map[string]any, user *UserInfo) (string, error) {
	for _, b := range p.MetaBoxes() {
		if b.ID == id {
			if b.Callback == "" {
				return "", nil
			}
			resp, err := p.renderAdmin(ctx, "meta_box", id, b.Callback, AdminRequest{Method: "GET", PostID: postID, Values: values, User: user})
			if err != nil {
				return "", err
			}
			return resp.HTML, nil
		}
	}
	return "", fmt.Errorf("meta box %q is not registered by %s", id, p.Slug())
}

// SaveMetaBox runs the optional save callback of a meta box after the core stored its fields:
// the post id and the field values by field id.
func (p *Plugin) SaveMetaBox(ctx context.Context, id string, postID int64, values map[string]any) error {
	for _, b := range p.MetaBoxes() {
		if b.ID == id {
			if b.SaveCallback == "" {
				return nil
			}
			if values == nil {
				values = map[string]any{}
			}
			_, err := p.call(ctx, &handleRequest{Plugin: p.Slug(), Callback: b.SaveCallback, Hook: id, Type: "meta_box_save", Args: []any{postID, values}})
			return err
		}
	}
	return fmt.Errorf("meta box %q is not registered by %s", id, p.Slug())
}

// renderFront runs a front-end render callback (block, builder widget): args are the settings and
// the inner content; the HTML it returns is sanitised like a shortcode's.
func (p *Plugin) renderFront(ctx context.Context, kind, id, callback string, settings map[string]any, content string) (string, error) {
	if settings == nil {
		settings = map[string]any{}
	}
	if raw, _ := json.Marshal(settings); len(raw)+len(content) > MaxUIInput+maxPostContent {
		return "", fmt.Errorf("%s %s: input is too large", kind, id)
	}
	raw, err := p.call(ctx, &handleRequest{Plugin: p.Slug(), Callback: callback, Hook: id, Type: kind, Args: []any{settings, content}})
	if err != nil {
		return "", err
	}
	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		p.logs.add("warning", "runtime", fmt.Sprintf("%s %s returned a non-string value", kind, id))
		return "", errors.New("render callback must return a string")
	}
	if len(out) > MaxUIOutput {
		return "", fmt.Errorf("output exceeds %d bytes", MaxUIOutput)
	}
	return p.sanitizeShortcode(out), nil
}

// FieldDefaults fills in the defaults of fields missing from values and drops keys that are not
// declared (plus the keys in keep, such as className).
func FieldDefaults(fields []SettingsField, values map[string]any, keep ...string) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if v, ok := values[f.ID]; ok && v != nil {
			out[f.ID] = v
		} else if f.Default != nil {
			out[f.ID] = f.Default
		}
	}
	for _, k := range keep {
		if v, ok := values[k]; ok {
			out[k] = v
		}
	}
	return out
}

// Runtime lookups ------------------------------------------------------------------

// PluginAdminPage pairs an admin page with its plugin.
type PluginAdminPage struct {
	Plugin *Plugin
	Page   AdminPage
}

// ID is the page's ?page= value.
func (a PluginAdminPage) ID() string { return AdminPageID(a.Plugin.Slug(), a.Page.Slug) }

// AdminPages returns the admin pages of the enabled plugins.
func (rt *Runtime) AdminPages() []PluginAdminPage {
	var out []PluginAdminPage
	for _, p := range rt.enabledPlugins() {
		for _, pg := range p.AdminPages() {
			out = append(out, PluginAdminPage{Plugin: p, Page: pg})
		}
	}
	return out
}

// FindAdminPage returns the page with the given ?page= value.
func (rt *Runtime) FindAdminPage(id string) (PluginAdminPage, bool) {
	for _, a := range rt.AdminPages() {
		if a.ID() == id {
			return a, true
		}
	}
	return PluginAdminPage{}, false
}

// PluginDashboardWidget pairs a dashboard widget with its plugin.
type PluginDashboardWidget struct {
	Plugin *Plugin
	Widget DashboardWidget
}

// DashboardWidgets returns the dashboard widgets of the enabled plugins.
func (rt *Runtime) DashboardWidgets() []PluginDashboardWidget {
	var out []PluginDashboardWidget
	for _, p := range rt.enabledPlugins() {
		for _, w := range p.DashboardWidgets() {
			out = append(out, PluginDashboardWidget{Plugin: p, Widget: w})
		}
	}
	return out
}

// PluginMetaBox pairs a meta box with its plugin.
type PluginMetaBox struct {
	Plugin *Plugin
	Box    MetaBox
}

// MetaBoxes returns the enabled plugins' meta boxes for a post type.
func (rt *Runtime) MetaBoxes(postType string) []PluginMetaBox {
	var out []PluginMetaBox
	for _, p := range rt.enabledPlugins() {
		for _, b := range p.MetaBoxes() {
			if b.ForType(postType) {
				out = append(out, PluginMetaBox{Plugin: p, Box: b})
			}
		}
	}
	return out
}

// PluginBlock pairs a block type with its plugin.
type PluginBlock struct {
	Plugin *Plugin
	Block  BlockType
}

// BlockTypes returns the dynamic blocks of the enabled plugins.
func (rt *Runtime) BlockTypes() []PluginBlock {
	var out []PluginBlock
	for _, p := range rt.enabledPlugins() {
		for _, b := range p.BlockTypes() {
			out = append(out, PluginBlock{Plugin: p, Block: b})
		}
	}
	return out
}

// RenderBlock renders the plugin block name with attrs (undeclared attributes other than
// className, align and anchor are dropped, defaults filled in) and the rendered inner content.
// ok is false when no enabled plugin registered the block; a failing callback renders "".
func (rt *Runtime) RenderBlock(ctx context.Context, name string, attrs map[string]any, content string) (html string, ok bool) {
	slug, _, found := strings.Cut(name, "/")
	if !found {
		return "", false
	}
	for _, p := range rt.enabledPlugins() {
		if p.Slug() != slug {
			continue
		}
		for _, b := range p.BlockTypes() {
			if b.Name != name {
				continue
			}
			out, err := p.renderFront(ctx, "block", name, b.Callback, FieldDefaults(b.Attributes, attrs, "className", "align", "anchor"), content)
			if err != nil {
				return "", true
			}
			return out, true
		}
	}
	return "", false
}

// PluginBuilderWidget pairs a Web Builder widget with its plugin.
type PluginBuilderWidget struct {
	Plugin *Plugin
	Widget BuilderWidget
}

// Type is the widget type: <plugin slug>/<id>.
func (w PluginBuilderWidget) Type() string { return w.Plugin.Slug() + "/" + w.Widget.ID }

// BuilderWidgets returns the Web Builder widgets of the enabled plugins.
func (rt *Runtime) BuilderWidgets() []PluginBuilderWidget {
	var out []PluginBuilderWidget
	for _, p := range rt.enabledPlugins() {
		for _, w := range p.BuilderWidgets() {
			out = append(out, PluginBuilderWidget{Plugin: p, Widget: w})
		}
	}
	return out
}

// RenderBuilderWidget renders the widget of type <plugin slug>/<id> with settings (undeclared
// keys dropped, defaults filled in). ok is false when no enabled plugin registered it.
func (rt *Runtime) RenderBuilderWidget(ctx context.Context, typ string, settings map[string]any) (html string, ok bool) {
	for _, w := range rt.BuilderWidgets() {
		if w.Type() != typ {
			continue
		}
		out, err := w.Plugin.renderFront(ctx, "builder_widget", typ, w.Widget.Callback, FieldDefaults(w.Widget.Controls, settings), "")
		if err != nil {
			return "", true
		}
		return out, true
	}
	return "", false
}
