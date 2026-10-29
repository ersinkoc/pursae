// Package builder is Golementor's server side: the document model the editor saves, the front-end
// renderer that turns an element tree into HTML, and the CSS engine that turns control settings into
// the per-document stylesheet. The editor UI is upstream's GPL-3.0 front end, served by
// internal/server; nothing here depends on HTTP.
package builder

import (
	"encoding/json"

	"wpgo/internal/builder/atomic"
)

// Element is one node of the element tree (_elementor_data): a section, column, container or widget.
type Element struct {
	ID         string         `json:"id"`
	ElType     string         `json:"elType"`
	WidgetType string         `json:"widgetType,omitempty"`
	IsInner    bool           `json:"isInner,omitempty"`
	Settings   map[string]any `json:"settings"`
	Elements   []*Element     `json:"elements"`

	// TemplateID is a global widget's template (Golementor Pro); globalOf is the widget type the
	// template holds once resolved (widgets_pro_common.go).
	TemplateID int64 `json:"templateID,omitempty"`
	globalOf   string

	// settingsKeys keeps the saved key order of Settings (PHP array order is observable in the
	// rendered class list and data-settings). Filled by UnmarshalJSON (element.go).
	settingsKeys []string
	// nestedKeys keeps the saved key order of nested settings objects, keyed by path
	// ("key/0/url"); see element.go.
	nestedKeys map[string][]string
	// Raw is the element's saved JSON (atomic elements keep styles, interactions, editor settings
	// and $$type props the renderer reads from it). Golementor v4: atomic_hook.go
	Raw json.RawMessage `json:"-"`
}

// Document is a page, post or template built with Golementor.
type Document struct {
	ID   int64
	Type string // wp-page, wp-post, kit, section, header, footer, single, archive, ...
	// PostType is the content type of the post (page, post, elementor_library); Golementor Pro
	// prints it as data-elementor-post-type on the wrapper ("" = not printed).
	PostType string
	Elements []*Element
	Settings map[string]any // page settings (_elementor_page_settings)
}

// ParseElements decodes a saved tree. Empty input is an empty tree.
func ParseElements(raw []byte) ([]*Element, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []*Element
	err := json.Unmarshal(raw, &out)
	return out, err
}

// Breakpoint is a responsive breakpoint from the kit (mobile 767, tablet 1024 by default).
type Breakpoint struct {
	Name      string // mobile, tablet, ...
	Value     int    // max-width in px
	Direction string // max or min
}

// RenderContext carries the site data the renderer needs.
type RenderContext struct {
	KitID       int64
	HomeURL     string
	UploadsURL  string // base URL of the uploads directory
	Breakpoints []Breakpoint
	// MediaURL resolves an attachment id (and image size, "" for full) to a URL; nil means use the url stored in the setting.
	MediaURL func(id int64, size string) string
	// Shortcode renders [shortcode] text (may be nil).
	Shortcode func(text string) string
	// Editing is true when rendering inside the editor preview (render_widget).
	Editing bool
	// Kit is the active kit document (global colors/typography, generic fonts, breakpoints);
	// nil means upstream's default kit values.
	Kit *Document
	// DisableGlobalColors / DisableGlobalTypography mirror the elementor_disable_color_schemes /
	// elementor_disable_typography_schemes options (false = the default "enabled").
	DisableGlobalColors     bool
	DisableGlobalTypography bool
	// Attachment returns an attachment's metadata (for <img> width/height/srcset/alt); may be nil.
	Attachment func(id int64) *Attachment
	// PostTitle returns a post's title (image captions, read-more, ...); may be nil.
	PostTitle func(id int64) string
	// Theme is the site and query data the theme-building ("Site") widgets read (widgets_theme.go);
	// nil makes them render placeholders.
	Theme *ThemeData
	// Pro is what Golementor Pro's dynamic tags, query widgets and documents read (dynamic_tags.go);
	// nil disables them.
	Pro *ProData
	// Atomic carries the site callbacks of the atomic (v4) element renderer (global classes,
	// variables, components, media); nil renders atomic elements without them. Golementor v4.
	Atomic *atomic.Env
}

// Result is a rendered document.
type Result struct {
	HTML    string   // the <div class="elementor elementor-ID" ...> wrapper with all elements
	CSS     string   // contents of post-<ID>.css
	Styles  []string // frontend style handles the page needs (widget-heading, e-animations, ...)
	Scripts []string // frontend script handles the page needs (swiper, ...)
	Fonts   []string // font families used (for Google Fonts or local fonts)
}

// Render and RenderWidget are implemented in render.go. They are variables so the server can be
// built while the renderer is being written; the defaults return empty output.
var (
	Render       = func(doc *Document, rc *RenderContext) (*Result, error) { return &Result{}, nil }
	RenderWidget = func(el *Element, doc *Document, rc *RenderContext) (string, error) { return "", nil }
	// KitCSS renders the kit stylesheet (post-<kitID>.css: --e-global-* variables and theme style).
	KitCSS = func(kit *Document, rc *RenderContext) string { return "" }
)

// WidgetKnown reports whether the renderer has a widget type (the Studio library validates its
// JSON with it).
func WidgetKnown(name string) bool { return name != "" && widgetDefs[name] != nil }
