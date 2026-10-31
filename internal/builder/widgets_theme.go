package builder

// Theme-building ("Site") widgets: the pieces Golementor theme parts (header, footer, single,
// archive, 404 templates) are made of. They read the site and the current query from
// RenderContext.Theme, which the server fills; without it (or without the piece of data they need)
// they print a placeholder in the editor and nothing on the front end. Markup follows the upstream
// theme widgets' class names (elementor-widget-theme-site-logo, elementor-nav-menu,
// elementor-posts, ...); their styles are in assets/css/widget-theme-elements.min.css.

import (
	"strconv"
	"strings"
	"time"
)

// ThemeData is what the Site widgets know about the site and the request being rendered.
type ThemeData struct {
	SiteName, Tagline, HomeURL string
	// LogoID is the site logo attachment (Site Identity / custom_logo); LogoURL is used when the
	// attachment cannot be resolved.
	LogoID  int64
	LogoURL string
	// SearchURL is the search form action, SearchQuery the current search.
	SearchURL, SearchQuery string
	// Now is the current time (copyright year); zero means time.Now().
	Now time.Time
	// Menu returns a menu by theme location or by id (id wins when > 0); nil when there is none.
	Menu func(location string, id int64) []*MenuItem
	// Post returns the singular post being shown (or the editor's preview post); nil elsewhere.
	Post func() *PostData
	// PostContent returns the_content() of Post.
	PostContent func() string
	// Archive returns the main query of an archive, blog or search page; nil elsewhere.
	Archive func() *ArchiveData
	// Store renders a store widget (widgets_theme_store.go) from its display settings: the storefront's
	// markup for the product, catalog, cart or checkout being shown; "" when there is nothing to show.
	// nil (no store) makes the store widgets print placeholders in the editor.
	Store func(widget string, settings map[string]any, editing bool) string
}

// MenuItem is one nav menu item with its children.
type MenuItem struct {
	ID                       int64
	Label, URL, Target       string
	Classes                  string // extra CSS classes set on the item
	Kind, Object             string // menu-item-type-<Kind> menu-item-object-<Object>
	Current, CurrentAncestor bool
	Children                 []*MenuItem
}

// TermLink is a category, tag or author link.
type TermLink struct {
	Name, URL string
}

// PostData is a post as the Site widgets print it.
type PostData struct {
	ID                    int64
	Type, Title, URL      string // Title is HTML-escaped text
	Excerpt               string // plain text
	Date, DateISO, Time   string
	AuthorName, AuthorURL string
	CommentCount          int
	CommentsURL           string
	Categories, Tags      []TermLink
	ImageID               int64
	ImageURL              string
	Class                 string // post_class()
}

// ArchiveData is the main query of an archive page.
type ArchiveData struct {
	Title       string // with its context ("Category: News"), HTML
	PlainTitle  string // without it ("News"), HTML
	Description string // HTML
	Posts       []*PostData
	Page, Pages int
	PageURL     func(n int) string
	Search      bool

	Home      bool   // gap-loops2: is_home() (the blog posts index), for Page Title's "show home title"
	HomeTitle string // gap-loops2: wp_get_document_title() there, HTML
}

func init() {
	themeWidgetDefs()
}

func (w *elemCtx) theme() *ThemeData { return w.r.rc.Theme }

// placeholder is the editor-only notice a Site widget prints when it has nothing to show.
func (w *elemCtx) placeholder(text string) string {
	if !w.editing() {
		return ""
	}
	return "\t\t\t<div class=\"elementor-theme-placeholder\">" + escHTML(text) + "</div>\n"
}

func (w *elemCtx) homeURL() string {
	if t := w.theme(); t != nil && t.HomeURL != "" {
		return t.HomeURL
	}
	if w.r.rc.HomeURL != "" {
		return strings.TrimRight(w.r.rc.HomeURL, "/") + "/"
	}
	return "/"
}

func (w *elemCtx) placeholderImage() string {
	return strings.TrimRight(w.r.rc.HomeURL, "/") + "/wp-content/plugins/gowebeditor/assets/images/placeholder.png"
}

// currentPost is the post the widget is about; a post never shows inside its own document.
func (w *elemCtx) currentPost() *PostData {
	t := w.theme()
	if t == nil || t.Post == nil {
		return nil
	}
	p := t.Post()
	if p == nil || (w.r.doc != nil && w.r.doc.ID == p.ID && w.r.doc.ID != 0) {
		return nil
	}
	return p
}

// headingMarkup prints <tag class="elementor-heading-title elementor-size-default">.
func (w *elemCtx) headingMarkup(text, link string) string {
	a := w.attr("title")
	a.add("class", "elementor-heading-title", "elementor-size-default")
	if link != "" {
		text = `<a href="` + escURL(link) + `">` + text + `</a>`
	}
	tag := validateHTMLTag(w.s("header_size"))
	return "\t\t\t<" + tag + " " + w.attrString("title") + ">" + text + "</" + tag + ">\t\t\n"
}

func themeWidgetDefs() {
	// Site logo -------------------------------------------------------------------------------
	l := &ctlList{}
	l.start("section_image", "Site Logo", "content")
	l.add("custom_image", "media", "Fallback Image", "description", "Shown when the site has no logo (Site Settings › Site Identity).",
		"default", map[string]any{"url": "", "id": ""})
	l.add("image_size", "select", "Image Resolution", "options", imageSizeOptions, "default", "full")
	l.add("link_to", "select", "Link", "options", Ordered{{"home", "Home Page"}, {"none", "None"}, {"custom", "Custom URL"}}, "default", "home")
	l.add("link", "url", "Custom Link", "condition", map[string]any{"link_to": "custom"},
		"default", map[string]any{"url": "", "is_external": "", "nofollow": "", "custom_attributes": ""})
	l.start("section_style_image", "Image", "style")
	l.align("align", "{{WRAPPER}}")
	l.slider("width", "Width", "{{WRAPPER}} img", "width: {{SIZE}}{{UNIT}};", []string{"%", "px", "vw"},
		map[string]any{"%": map[string]any{"min": 1, "max": 100}, "px": map[string]any{"min": 1, "max": 1000}}, nil)
	l.slider("max_width", "Max Width", "{{WRAPPER}} img", "max-width: {{SIZE}}{{UNIT}};", []string{"%", "px", "vw"},
		map[string]any{"%": map[string]any{"min": 1, "max": 100}, "px": map[string]any{"min": 1, "max": 1000}}, nil)
	l.slider("height", "Height", "{{WRAPPER}} img", "height: {{SIZE}}{{UNIT}};", []string{"px", "vh"},
		map[string]any{"px": map[string]any{"min": 1, "max": 500}}, nil)
	registerThemeWidget(&ThemeWidget{Name: "theme-site-logo", Title: "Site Logo", Icon: "eicon-site-logo",
		Keywords: []string{"site", "logo", "branding", "image"}, Controls: l.out},
		&widgetDef{render: renderSiteLogo, styles: static("widget-image", "widget-theme-elements"),
			wrapperClass: "elementor-widget-theme-site-logo elementor-widget-image"})

	// Site title ------------------------------------------------------------------------------
	l = &ctlList{}
	l.heading("section_title", "Site Title", "h2", "Link to Home Page", "yes", "primary", "primary")
	registerThemeWidget(&ThemeWidget{Name: "theme-site-title", Title: "Site Title", Icon: "eicon-site-title",
		Keywords: []string{"site", "title", "name", "branding"}, Controls: l.out},
		&widgetDef{render: renderSiteTitle, styles: static("widget-heading"),
			wrapperClass: "elementor-widget-theme-site-title elementor-widget-heading"})

	// Site tagline ----------------------------------------------------------------------------
	l = &ctlList{}
	l.heading("section_title", "Site Tagline", "p", "", "", "secondary", "secondary")
	registerThemeWidget(&ThemeWidget{Name: "theme-site-tagline", Title: "Site Tagline", Icon: "eicon-site-tagline",
		Keywords: []string{"site", "tagline", "description", "slogan"}, Controls: l.out},
		&widgetDef{render: renderSiteTagline, styles: static("widget-heading"),
			wrapperClass: "elementor-widget-theme-site-tagline elementor-widget-heading"})

	// Nav menu --------------------------------------------------------------------------------
	l = &ctlList{}
	l.start("section_layout", "Menu", "content")
	l.add("menu_source", "select", "Source", "options", Ordered{{"location", "Theme Location"}, {"menu", "Menu"}}, "default", "location")
	l.add("menu_location", "select", "Location", "options", Ordered{{"primary", "Primary Menu"}, {"footer", "Footer Menu"}},
		"default", "primary", "condition", map[string]any{"menu_source": "location"},
		"description", "Assign menus to locations in Appearance › Menus.")
	l.add("menu", "select", "Menu", "options", Ordered{{"", "— Select —"}}, "default", "", "condition", map[string]any{"menu_source": "menu"})
	l.add("layout", "select", "Layout", "options", Ordered{{"horizontal", "Horizontal"}, {"vertical", "Vertical"}}, "default", "horizontal",
		"prefix_class", "elementor-nav-menu--layout-")
	l.add("align_items", "choose", "Align", "options", alignOptions, "default", "",
		"selectors_dictionary", map[string]any{"start": "flex-start", "center": "center", "end": "flex-end", "justify": "space-between"},
		"selectors", sel("{{WRAPPER}} .elementor-nav-menu", "justify-content: {{VALUE}};"), "responsive", []any{}, "is_responsive", true)
	l.add("submenu_indicator", "switcher", "Submenu Indicator", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes")
	l.add("dropdown", "select", "Breakpoint", "options", Ordered{{"mobile", "Mobile (< 768px)"}, {"tablet", "Tablet (< 1025px)"}, {"none", "None"}},
		"default", "tablet", "prefix_class", "elementor-nav-menu--dropdown-", "description", "Below this width the menu collapses behind a toggle button.")
	l.start("section_style_main", "Main Menu", "style")
	l.color("color_menu_item", "Text Color", "{{WRAPPER}} .elementor-nav-menu .elementor-item", "color", "text")
	l.color("color_menu_item_hover", "Hover Color", "{{WRAPPER}} .elementor-nav-menu .elementor-item:hover, {{WRAPPER}} .elementor-nav-menu .elementor-item:focus", "color", "accent")
	l.color("color_menu_item_active", "Active Color", "{{WRAPPER}} .elementor-nav-menu .elementor-item.elementor-item-active", "color", "accent")
	l.typography("menu_typography", "{{WRAPPER}} .elementor-nav-menu .elementor-item", "primary")
	l.slider("padding_horizontal_menu_item", "Horizontal Padding", "{{WRAPPER}} .elementor-nav-menu .elementor-item",
		"padding-left: {{SIZE}}{{UNIT}}; padding-right: {{SIZE}}{{UNIT}};", []string{"px", "em"}, map[string]any{"px": map[string]any{"max": 50}}, nil)
	l.slider("padding_vertical_menu_item", "Vertical Padding", "{{WRAPPER}} .elementor-nav-menu .elementor-item",
		"padding-top: {{SIZE}}{{UNIT}}; padding-bottom: {{SIZE}}{{UNIT}};", []string{"px", "em"}, map[string]any{"px": map[string]any{"max": 50}}, nil)
	l.slider("menu_space_between", "Space Between", "{{WRAPPER}} .elementor-nav-menu", "gap: {{SIZE}}{{UNIT}};", []string{"px", "em"},
		map[string]any{"px": map[string]any{"max": 100}}, nil)
	// Submenus and the collapsed (mobile) menu share the dropdown colors through CSS variables, so
	// a light main menu on a dark header still reads on the white dropdown.
	l.start("section_style_dropdown", "Dropdown", "style")
	l.color("color_dropdown_item", "Text Color", "{{WRAPPER}}", "--e-nav-menu-dropdown-color", "")
	l.color("background_color_dropdown_item", "Background Color", "{{WRAPPER}}", "--e-nav-menu-dropdown-background", "")
	l.typography("dropdown_typography", "{{WRAPPER}} .elementor-nav-menu--dropdown a", "")
	l.color("toggle_color", "Toggle Button Color", "{{WRAPPER}} .elementor-menu-toggle", "color", "")
	registerThemeWidget(&ThemeWidget{Name: "nav-menu", Title: "Nav Menu", Icon: "eicon-nav-menu",
		Keywords: []string{"menu", "nav", "navigation", "button"}, Controls: l.out},
		&widgetDef{render: renderNavMenu, styles: static("widget-theme-elements")})

	// Search form -----------------------------------------------------------------------------
	l = &ctlList{}
	l.start("search_content", "Search Form", "content")
	l.add("placeholder", "text", "Placeholder", "default", "Search …")
	l.add("button_type", "select", "Button", "options", Ordered{{"text", "Text"}, {"icon", "Icon"}, {"none", "None"}}, "default", "text")
	l.add("button_text", "text", "Button Text", "default", "Search", "condition", map[string]any{"button_type": "text"})
	l.start("section_input_style", "Input", "style")
	l.color("input_text_color", "Text Color", "{{WRAPPER}} .elementor-search-form__input", "color", "text")
	l.color("input_background_color", "Background Color", "{{WRAPPER}} .elementor-search-form__input", "background-color", "")
	l.color("input_border_color", "Border Color", "{{WRAPPER}} .elementor-search-form__container", "border-color", "")
	l.typography("input_typography", "{{WRAPPER}} .elementor-search-form__input", "text")
	l.slider("border_radius", "Border Radius", "{{WRAPPER}} .elementor-search-form__container", "border-radius: {{SIZE}}{{UNIT}};",
		[]string{"px", "em"}, map[string]any{"px": map[string]any{"max": 200}}, nil)
	l.start("section_button_style", "Button", "style")
	l.color("button_text_color", "Text Color", "{{WRAPPER}} .elementor-search-form__submit", "color", "")
	l.color("button_background_color", "Background Color", "{{WRAPPER}} .elementor-search-form__submit", "background-color", "secondary")
	registerThemeWidget(&ThemeWidget{Name: "search-form", Title: "Search Form", Icon: "eicon-site-search",
		Keywords: []string{"search", "form"}, Controls: l.out},
		&widgetDef{render: renderSearchForm, styles: static("widget-theme-elements")})

	// Copyright -------------------------------------------------------------------------------
	l = &ctlList{}
	l.start("section_copyright", "Copyright", "content")
	l.add("text", "textarea", "Text", "default", "© [year] [site_title]. All rights reserved.", "rows", 3,
		"description", "Placeholders: [year], [site_title], [site_url].")
	l.start("section_copyright_style", "Copyright", "style")
	l.align("align", "{{WRAPPER}}")
	l.color("text_color", "Text Color", "{{WRAPPER}} .elementor-copyright, {{WRAPPER}} .elementor-copyright a", "color", "text")
	l.typography("typography", "{{WRAPPER}} .elementor-copyright", "text")
	registerThemeWidget(&ThemeWidget{Name: "theme-site-copyright", Title: "Copyright", Icon: "eicon-footer",
		Keywords: []string{"copyright", "footer", "credits", "year"}, Controls: l.out},
		&widgetDef{render: renderCopyright, styles: static("widget-theme-elements")})

	// Post title ------------------------------------------------------------------------------
	l = &ctlList{}
	l.heading("section_title", "Post Title", "h1", "Link to Post", "", "primary", "primary")
	registerThemeWidget(&ThemeWidget{Name: "theme-post-title", Title: "Post Title", Icon: "eicon-post-title",
		Keywords: []string{"title", "heading", "post"}, Categories: []string{"theme-elements-single"}, Controls: l.out},
		&widgetDef{render: renderPostTitle, styles: static("widget-heading"),
			wrapperClass: "elementor-widget-theme-post-title elementor-widget-heading"})

	// Post content ----------------------------------------------------------------------------
	l = &ctlList{}
	l.start("section_style", "Style", "style")
	l.align("align", "{{WRAPPER}}")
	l.color("text_color", "Text Color", "{{WRAPPER}}", "color", "text")
	l.typography("typography", "{{WRAPPER}}", "text")
	registerThemeWidget(&ThemeWidget{Name: "theme-post-content", Title: "Post Content", Icon: "eicon-post-content",
		Keywords: []string{"content", "post"}, Categories: []string{"theme-elements-single"}, Controls: l.out},
		&widgetDef{render: renderPostContent})

	// Featured image --------------------------------------------------------------------------
	l = &ctlList{}
	l.start("section_image", "Featured Image", "content")
	l.add("image_size", "select", "Image Resolution", "options", imageSizeOptions, "default", "large")
	l.add("link_to", "select", "Link", "options", Ordered{{"none", "None"}, {"post", "Post"}, {"file", "Media File"}}, "default", "none")
	l.start("section_style_image", "Image", "style")
	l.align("align", "{{WRAPPER}}")
	l.slider("width", "Width", "{{WRAPPER}} img", "width: {{SIZE}}{{UNIT}};", []string{"%", "px", "vw"},
		map[string]any{"%": map[string]any{"min": 1, "max": 100}, "px": map[string]any{"min": 1, "max": 1600}}, nil)
	l.slider("image_border_radius", "Border Radius", "{{WRAPPER}} img", "border-radius: {{SIZE}}{{UNIT}};", []string{"px", "%"},
		map[string]any{"px": map[string]any{"max": 200}}, nil)
	registerThemeWidget(&ThemeWidget{Name: "theme-post-featured-image", Title: "Featured Image", Icon: "eicon-featured-image",
		Keywords: []string{"image", "featured", "thumbnail"}, Categories: []string{"theme-elements-single"}, Controls: l.out},
		&widgetDef{render: renderFeaturedImage, styles: static("widget-image"),
			wrapperClass: "elementor-widget-theme-post-featured-image elementor-widget-image"})

	// Post info -------------------------------------------------------------------------------
	l = &ctlList{}
	l.start("section_icon", "Meta Data", "content")
	l.add("view", "select", "Layout", "options", Ordered{{"inline", "Inline"}, {"traditional", "Default"}}, "default", "inline")
	for _, f := range [][3]string{{"show_author", "Author", "yes"}, {"show_date", "Date", "yes"}, {"show_time", "Time", ""},
		{"show_terms", "Categories", "yes"}, {"show_comments", "Comments", ""}} {
		l.add(f[0], "switcher", f[1], "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", f[2])
	}
	l.add("show_icons", "switcher", "Icons", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes")
	l.start("section_text_style", "Text", "style")
	l.color("text_color", "Text Color", "{{WRAPPER}} .elementor-icon-list-text, {{WRAPPER}} .elementor-icon-list-text a", "color", "secondary")
	l.color("icon_color", "Icon Color", "{{WRAPPER}} .elementor-icon-list-icon svg", "fill", "")
	l.slider("space_between", "Space Between", "{{WRAPPER}} .elementor-post-info", "gap: {{SIZE}}{{UNIT}};", []string{"px"},
		map[string]any{"px": map[string]any{"max": 50}}, nil)
	l.typography("text_typography", "{{WRAPPER}} .elementor-icon-list-item", "text")
	registerThemeWidget(&ThemeWidget{Name: "post-info", Title: "Post Info", Icon: "eicon-post-info",
		Keywords:   []string{"post", "info", "date", "time", "author", "taxonomy", "comments", "terms", "avatar"},
		Categories: []string{"theme-elements-single"}, Controls: l.out},
		&widgetDef{render: renderPostInfo, styles: static("widget-icon-list", "widget-theme-elements")})

	// Archive title ---------------------------------------------------------------------------
	l = &ctlList{}
	l.heading("section_title", "Archive Title", "h1", "", "", "primary", "primary")
	for i, c := range l.out {
		if c["name"] == "header_size" {
			ctx := map[string]any{"type": "switcher", "tab": "content", "section": "section_title", "label": "Include Context",
				"label_on": "Yes", "label_off": "No", "return_value": "yes", "default": "yes", "name": "include_context"}
			l.out = append(l.out[:i+1], append([]map[string]any{ctx}, l.out[i+1:]...)...)
			break
		}
	}
	registerThemeWidget(&ThemeWidget{Name: "theme-archive-title", Title: "Archive Title", Icon: "eicon-archive-title",
		Keywords: []string{"title", "heading", "archive"}, Controls: l.out},
		&widgetDef{render: renderArchiveTitle, styles: static("widget-heading"),
			wrapperClass: "elementor-widget-theme-archive-title elementor-widget-heading"})

	// Archive posts ---------------------------------------------------------------------------
	l = &ctlList{}
	l.start("section_layout", "Layout", "content")
	l.add("columns", "select", "Columns", "options", Ordered{{"1", "1"}, {"2", "2"}, {"3", "3"}, {"4", "4"}, {"5", "5"}, {"6", "6"}},
		"default", "3", "tablet_default", "2", "mobile_default", "1", "prefix_class", "elementor-grid%s-", "responsive", []any{}, "is_responsive", true)
	l.add("show_thumbnail", "switcher", "Image", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes")
	l.add("thumbnail_size", "select", "Image Resolution", "options", imageSizeOptions, "default", "medium_large",
		"condition", map[string]any{"show_thumbnail": "yes"})
	l.add("show_title", "switcher", "Title", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes")
	l.add("title_tag", "select", "Title HTML Tag", "options", htmlTagOptions, "default", "h3", "condition", map[string]any{"show_title": "yes"})
	l.add("show_date", "switcher", "Date", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes")
	l.add("show_excerpt", "switcher", "Excerpt", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes")
	l.add("excerpt_length", "number", "Excerpt Length", "default", 25, "condition", map[string]any{"show_excerpt": "yes"})
	l.add("show_read_more", "switcher", "Read More", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes")
	l.add("read_more_text", "text", "Read More Text", "default", "Read More »", "condition", map[string]any{"show_read_more": "yes"})
	l.start("section_pagination", "Pagination", "content")
	l.add("pagination", "switcher", "Pagination", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes")
	l.add("nothing_found_message", "textarea", "Nothing Found Message", "default", "It seems we can’t find what you’re looking for.")
	l.start("section_design_layout", "Layout", "style")
	l.slider("column_gap", "Columns Gap", "{{WRAPPER}}", "--grid-column-gap: {{SIZE}}{{UNIT}}", []string{"px", "em"},
		map[string]any{"px": map[string]any{"max": 100}}, map[string]any{"size": 30})
	l.slider("row_gap", "Rows Gap", "{{WRAPPER}}", "--grid-row-gap: {{SIZE}}{{UNIT}}", []string{"px", "em"},
		map[string]any{"px": map[string]any{"max": 100}}, map[string]any{"size": 35})
	l.start("section_design_content", "Content", "style")
	l.color("title_color", "Title Color", "{{WRAPPER}} .elementor-post__title, {{WRAPPER}} .elementor-post__title a", "color", "secondary")
	l.typography("title_typography", "{{WRAPPER}} .elementor-post__title, {{WRAPPER}} .elementor-post__title a", "primary")
	l.color("meta_color", "Meta Color", "{{WRAPPER}} .elementor-post__meta-data", "color", "")
	l.color("excerpt_color", "Excerpt Color", "{{WRAPPER}} .elementor-post__excerpt p", "color", "")
	l.typography("excerpt_typography", "{{WRAPPER}} .elementor-post__excerpt p", "text")
	l.color("read_more_color", "Read More Color", "{{WRAPPER}} .elementor-post__read-more", "color", "accent")
	registerThemeWidget(&ThemeWidget{Name: "archive-posts", Title: "Archive Posts", Icon: "eicon-archive-posts",
		Keywords: []string{"posts", "cpt", "archive", "loop", "query", "blog"}, Controls: l.out},
		&widgetDef{render: renderArchivePosts, styles: static("widget-theme-elements")})
}

// --- renderers --------------------------------------------------------------------------------

func renderSiteLogo(w *elemCtx) string {
	t := w.theme()
	img := map[string]any{"id": "", "url": ""}
	if t != nil && (t.LogoID > 0 || t.LogoURL != "") {
		img = map[string]any{"id": t.LogoID, "url": t.LogoURL}
	} else if ci := asMap(w.get("custom_image")); ci != nil && (truthy(ci["id"]) || truthy(ci["url"])) {
		img = ci
	} else {
		img["url"] = w.placeholderImage()
	}
	if !truthy(img["id"]) && !truthy(img["url"]) {
		return ""
	}
	html := w.r.attachmentImageHTML(map[string]any{"image": img, "image_size": w.s("image_size")}, "image", "")
	if html == "" {
		return ""
	}
	var link map[string]any
	switch w.s("link_to") {
	case "none":
	case "custom":
		if lm := asMap(w.get("link")); lm != nil && truthy(lm["url"]) {
			link = lm
		}
	default:
		link = map[string]any{"url": w.homeURL()}
	}
	if link != nil {
		w.addLinkAttributes("link", link, false)
		html = "<a " + w.attrString("link") + ">\n\t\t\t" + html + "\t\t\t\t</a>\n"
	}
	return "\t\t\t" + html + "\t\t\t\t\t\t\t\n"
}

func renderSiteTitle(w *elemCtx) string {
	t := w.theme()
	name := "Site Title"
	if t != nil {
		name = t.SiteName
	}
	if name == "" {
		return w.placeholder("Set the site title in Settings › General.")
	}
	link := ""
	if w.s("link_to") == "yes" {
		link = w.homeURL()
	}
	return w.headingMarkup(escHTML(name), link)
}

func renderSiteTagline(w *elemCtx) string {
	t := w.theme()
	text := "Just another Pursae site"
	if t != nil {
		text = t.Tagline
	}
	if text == "" {
		return w.placeholder("Set the tagline in Settings › General.")
	}
	return w.headingMarkup(escHTML(text), "")
}

func renderPostTitle(w *elemCtx) string {
	p := w.currentPost()
	if p == nil {
		if w.editing() {
			return w.headingMarkup("Post Title", "")
		}
		return ""
	}
	link := ""
	if w.s("link_to") == "yes" {
		link = p.URL
	}
	return w.headingMarkup(p.Title, link)
}

func renderPostContent(w *elemCtx) string {
	t := w.theme()
	p := w.currentPost()
	if p == nil || t == nil || t.PostContent == nil {
		return w.placeholder("The post content will appear here. Preview it on a post or page.")
	}
	content := t.PostContent()
	if strings.TrimSpace(content) == "" {
		return w.placeholder("This post has no content yet.")
	}
	return content + "\n"
}

func renderFeaturedImage(w *elemCtx) string {
	p := w.currentPost()
	img := map[string]any{}
	switch {
	case p != nil && (p.ImageID > 0 || p.ImageURL != ""):
		img = map[string]any{"id": p.ImageID, "url": p.ImageURL}
	case w.editing():
		img = map[string]any{"id": "", "url": w.placeholderImage()}
	default:
		return ""
	}
	html := w.r.attachmentImageHTML(map[string]any{"image": img, "image_size": w.s("image_size")}, "image", "")
	if html == "" {
		return ""
	}
	link := ""
	switch w.s("link_to") {
	case "post":
		if p != nil {
			link = p.URL
		}
	case "file":
		if p != nil {
			link = p.ImageURL
		}
	}
	if link != "" {
		html = `<a href="` + escURL(link) + `">` + html + `</a>`
	}
	return "\t\t\t" + html + "\t\t\t\t\t\t\t\n"
}

var postInfoIcons = map[string]map[string]any{
	"author":   {"value": "far fa-user-circle", "library": "fa-regular"},
	"date":     {"value": "fas fa-calendar", "library": "fa-solid"},
	"time":     {"value": "far fa-clock", "library": "fa-regular"},
	"terms":    {"value": "fas fa-tags", "library": "fa-solid"},
	"comments": {"value": "far fa-comment-dots", "library": "fa-regular"},
}

func renderPostInfo(w *elemCtx) string {
	p := w.currentPost()
	if p == nil {
		if !w.editing() {
			return ""
		}
		p = &PostData{AuthorName: "Author", Date: "January 1, 2026", Time: "12:00 pm", Categories: []TermLink{{"Category", "#"}}}
	}
	type item struct{ kind, text, url, prop string }
	var items []item
	if w.s("show_author") == "yes" && p.AuthorName != "" {
		items = append(items, item{"author", escHTML(p.AuthorName), p.AuthorURL, "author"})
	}
	if w.s("show_date") == "yes" && p.Date != "" {
		items = append(items, item{"date", `<time datetime="` + escAttr(p.DateISO) + `">` + escHTML(p.Date) + `</time>`, "", "datePublished"})
	}
	if w.s("show_time") == "yes" && p.Time != "" {
		items = append(items, item{"time", escHTML(p.Time), "", ""})
	}
	if w.s("show_terms") == "yes" && len(p.Categories) > 0 {
		var links []string
		for _, c := range p.Categories {
			links = append(links, `<a href="`+escURL(c.URL)+`">`+escHTML(c.Name)+`</a>`)
		}
		items = append(items, item{"terms", strings.Join(links, ", "), "", "about"})
	}
	if w.s("show_comments") == "yes" {
		text := "No Comments"
		switch {
		case p.CommentCount == 1:
			text = "One Comment"
		case p.CommentCount > 1:
			text = strconv.Itoa(p.CommentCount) + " Comments"
		}
		items = append(items, item{"comments", text, p.CommentsURL, "commentCount"})
	}
	if len(items) == 0 {
		return w.placeholder("Choose the meta data to show.")
	}
	inline := w.s("view") != "traditional"
	ul := "elementor-icon-list-items elementor-post-info"
	li := "elementor-icon-list-item"
	if inline {
		ul = "elementor-inline-items " + ul
		li += " elementor-inline-item"
	}
	var b strings.Builder
	b.WriteString("\t\t<ul class=\"" + ul + "\">\n")
	for _, it := range items {
		b.WriteString("\t\t\t\t<li class=\"" + li + " elementor-post-info__item-type-" + it.kind + "\"")
		if it.prop != "" {
			b.WriteString(` itemprop="` + it.prop + `"`)
		}
		b.WriteString(">\n")
		if it.url != "" {
			b.WriteString("\t\t\t\t\t<a href=\"" + escURL(it.url) + "\">\n")
		}
		if w.s("show_icons") == "yes" {
			b.WriteString("\t\t\t\t\t\t<span class=\"elementor-icon-list-icon\">\n\t\t\t\t\t\t\t" +
				renderIcon(postInfoIcons[it.kind], newAttrs().add("aria-hidden", "true"), "i") + "\t\t\t\t\t\t\t</span>\n")
		}
		b.WriteString("\t\t\t\t\t<span class=\"elementor-icon-list-text elementor-post-info__item elementor-post-info__item--type-" + it.kind + "\">" + it.text + "</span>\n")
		if it.url != "" {
			b.WriteString("\t\t\t\t\t</a>\n")
		}
		b.WriteString("\t\t\t\t</li>\n")
	}
	b.WriteString("\t\t\t</ul>\n")
	return b.String()
}

func (w *elemCtx) archive() *ArchiveData {
	if t := w.theme(); t != nil && t.Archive != nil {
		return t.Archive()
	}
	return nil
}

func renderArchiveTitle(w *elemCtx) string {
	a := w.archive()
	if a == nil {
		if w.editing() {
			return w.headingMarkup("Archive Title", "")
		}
		return ""
	}
	title := a.Title
	if w.s("include_context") != "yes" && a.PlainTitle != "" {
		title = a.PlainTitle
	}
	if title == "" {
		return ""
	}
	return w.headingMarkup(title, "")
}

func renderArchivePosts(w *elemCtx) string {
	a := w.archive()
	if a == nil {
		if !w.editing() {
			return ""
		}
		a = &ArchiveData{Page: 1, Pages: 1}
		for i := 1; i <= 3; i++ {
			a.Posts = append(a.Posts, &PostData{ID: int64(i), Title: "Sample Post " + strconv.Itoa(i), URL: "#", Date: "January 1, 2026",
				Excerpt: "This is where the post excerpt appears on archive pages, blogs and search results.", ImageURL: w.placeholderImage()})
		}
	}
	if len(a.Posts) == 0 {
		msg := w.s("nothing_found_message")
		if msg == "" {
			return ""
		}
		return "\t\t<div class=\"elementor-posts-nothing-found\">" + ksesPost(msg) + "</div>\n"
	}
	var b strings.Builder
	b.WriteString("\t\t<div class=\"elementor-posts-container elementor-posts elementor-posts--skin-classic elementor-grid\" role=\"list\">\n")
	tag := validateHTMLTag(w.s("title_tag"))
	words := toInt(w.get("excerpt_length"))
	if words <= 0 {
		words = 25
	}
	for _, p := range a.Posts {
		cls := "elementor-post elementor-grid-item"
		if p.Class != "" {
			cls += " " + p.Class
		}
		b.WriteString("\t\t\t<article class=\"" + escAttr(cls) + "\" role=\"listitem\">\n")
		if w.s("show_thumbnail") == "yes" && (p.ImageID > 0 || p.ImageURL != "") {
			img := w.r.attachmentImageHTML(map[string]any{"image": map[string]any{"id": p.ImageID, "url": p.ImageURL},
				"thumbnail_size": w.s("thumbnail_size")}, "thumbnail", "image")
			if img != "" {
				b.WriteString("\t\t\t\t<a class=\"elementor-post__thumbnail__link\" href=\"" + escURL(p.URL) + "\" tabindex=\"-1\">\n" +
					"\t\t\t\t\t<div class=\"elementor-post__thumbnail\">" + img + "</div>\n\t\t\t\t</a>\n")
			}
		}
		b.WriteString("\t\t\t\t<div class=\"elementor-post__text\">\n")
		if w.s("show_title") == "yes" {
			b.WriteString("\t\t\t\t\t<" + tag + " class=\"elementor-post__title\"><a href=\"" + escURL(p.URL) + "\">" + p.Title + "</a></" + tag + ">\n")
		}
		if w.s("show_date") == "yes" && p.Date != "" {
			b.WriteString("\t\t\t\t\t<div class=\"elementor-post__meta-data\"><span class=\"elementor-post-date\">" + escHTML(p.Date) + "</span></div>\n")
		}
		if w.s("show_excerpt") == "yes" && p.Excerpt != "" {
			b.WriteString("\t\t\t\t\t<div class=\"elementor-post__excerpt\"><p>" + escHTML(trimWords(p.Excerpt, words)) + "</p></div>\n")
		}
		if w.s("show_read_more") == "yes" && w.s("read_more_text") != "" {
			b.WriteString("\t\t\t\t\t<a class=\"elementor-post__read-more\" href=\"" + escURL(p.URL) + "\" aria-label=\"Read more about " +
				escAttr(stripTags(p.Title)) + "\" tabindex=\"-1\">" + escHTML(w.s("read_more_text")) + "</a>\n")
		}
		b.WriteString("\t\t\t\t</div>\n\t\t\t</article>\n")
	}
	b.WriteString("\t\t</div>\n")
	if w.s("pagination") == "yes" && a.Pages > 1 && a.PageURL != nil {
		b.WriteString("\t\t<nav class=\"elementor-pagination\" aria-label=\"Pagination\">\n")
		if a.Page > 1 {
			b.WriteString("\t\t\t<a class=\"page-numbers prev\" href=\"" + escURL(a.PageURL(a.Page-1)) + "\">&laquo; Previous</a>\n")
		}
		for n := 1; n <= a.Pages; n++ {
			if n == a.Page {
				b.WriteString("\t\t\t<span aria-current=\"page\" class=\"page-numbers current\"><span class=\"elementor-screen-only\">Page</span>" + strconv.Itoa(n) + "</span>\n")
				continue
			}
			if n != 1 && n != a.Pages && (n < a.Page-2 || n > a.Page+2) {
				if n == a.Page-3 || n == a.Page+3 {
					b.WriteString("\t\t\t<span class=\"page-numbers dots\">&hellip;</span>\n")
				}
				continue
			}
			b.WriteString("\t\t\t<a class=\"page-numbers\" href=\"" + escURL(a.PageURL(n)) + "\"><span class=\"elementor-screen-only\">Page</span>" + strconv.Itoa(n) + "</a>\n")
		}
		if a.Page < a.Pages {
			b.WriteString("\t\t\t<a class=\"page-numbers next\" href=\"" + escURL(a.PageURL(a.Page+1)) + "\">Next &raquo;</a>\n")
		}
		b.WriteString("\t\t</nav>\n")
	}
	return b.String()
}

// trimWords is wp_trim_words() with "…" as the more text.
func trimWords(text string, n int) string {
	words := strings.Fields(stripTags(text))
	if len(words) <= n {
		return strings.Join(words, " ")
	}
	return strings.Join(words[:n], " ") + "…"
}

func renderSearchForm(w *elemCtx) string {
	action := w.homeURL()
	query := ""
	if t := w.theme(); t != nil {
		if t.SearchURL != "" {
			action = t.SearchURL
		}
		query = t.SearchQuery
	}
	id := "elementor-search-form-" + w.el.ID
	var b strings.Builder
	b.WriteString("\t\t<form class=\"elementor-search-form\" role=\"search\" action=\"" + escURL(action) + "\" method=\"get\">\n")
	b.WriteString("\t\t\t<div class=\"elementor-search-form__container\">\n")
	b.WriteString("\t\t\t\t<label class=\"elementor-screen-only\" for=\"" + escAttr(id) + "\">Search</label>\n")
	b.WriteString("\t\t\t\t<input id=\"" + escAttr(id) + "\" placeholder=\"" + escAttr(w.s("placeholder")) +
		"\" class=\"elementor-search-form__input\" type=\"search\" name=\"s\" value=\"" + escAttr(query) + "\">\n")
	switch w.s("button_type") {
	case "none":
	case "icon":
		b.WriteString("\t\t\t\t<button class=\"elementor-search-form__submit\" type=\"submit\" aria-label=\"Search\">" +
			renderIcon(map[string]any{"value": "fas fa-search", "library": "fa-solid"}, newAttrs().add("aria-hidden", "true"), "i") + "</button>\n")
	default:
		b.WriteString("\t\t\t\t<button class=\"elementor-search-form__submit\" type=\"submit\">" + escHTML(w.s("button_text")) + "</button>\n")
	}
	b.WriteString("\t\t\t</div>\n\t\t</form>\n")
	return b.String()
}

func renderCopyright(w *elemCtx) string {
	text := w.s("text")
	if text == "" {
		return ""
	}
	t := w.theme()
	now := time.Now()
	name := ""
	if t != nil {
		name = t.SiteName
		if !t.Now.IsZero() {
			now = t.Now
		}
	}
	text = strings.NewReplacer("[year]", strconv.Itoa(now.Year()), "[site_title]", escHTML(name), "[site_url]", escURL(w.homeURL())).Replace(text)
	return "\t\t<div class=\"elementor-copyright\">" + ksesPost(text) + "</div>\n"
}

func renderNavMenu(w *elemCtx) string {
	t := w.theme()
	var items []*MenuItem
	if t != nil && t.Menu != nil {
		if w.s("menu_source") == "menu" {
			items = t.Menu("", int64(toInt(w.get("menu"))))
		} else {
			items = t.Menu(w.s("menu_location"), 0)
		}
	}
	if len(items) == 0 {
		if w.s("menu_source") == "menu" {
			return w.placeholder("Choose a menu, or create one in Appearance › Menus.")
		}
		return w.placeholder("No menu is assigned to this location yet. Assign one in Appearance › Menus.")
	}
	indicator := w.s("submenu_indicator") == "yes"
	var walk func(list []*MenuItem, depth int, b *strings.Builder)
	walk = func(list []*MenuItem, depth int, b *strings.Builder) {
		for _, it := range list {
			cls := "menu-item"
			if it.Kind != "" {
				cls += " menu-item-type-" + it.Kind
			}
			if it.Object != "" {
				cls += " menu-item-object-" + it.Object
			}
			if it.Current {
				cls += " current-menu-item"
			}
			if it.CurrentAncestor {
				cls += " current-menu-ancestor current-menu-parent"
			}
			if len(it.Children) > 0 {
				cls += " menu-item-has-children"
			}
			if it.Classes != "" {
				cls = it.Classes + " " + cls
			}
			cls += " menu-item-" + strconv.FormatInt(it.ID, 10)
			b.WriteString("<li class=\"" + escAttr(cls) + "\"><a href=\"" + escURL(it.URL) + "\"")
			ac := "elementor-item"
			if depth > 0 {
				ac = "elementor-sub-item"
			}
			if it.Current || it.CurrentAncestor {
				ac += " elementor-item-active"
			}
			b.WriteString(" class=\"" + ac + "\"")
			if it.Target != "" {
				b.WriteString(" target=\"" + escAttr(it.Target) + "\"")
			}
			if it.Current {
				b.WriteString(" aria-current=\"page\"")
			}
			b.WriteString(">" + escHTML(it.Label))
			if len(it.Children) > 0 && indicator {
				b.WriteString("<span class=\"sub-arrow\" aria-hidden=\"true\"></span>")
			}
			b.WriteString("</a>")
			if len(it.Children) > 0 {
				b.WriteString("\n<ul class=\"sub-menu elementor-nav-menu--dropdown\">\n")
				walk(it.Children, depth+1, b)
				b.WriteString("</ul>\n")
			}
			b.WriteString("</li>\n")
		}
	}
	var list strings.Builder
	walk(items, 0, &list)
	id := "elementor-menu-toggle-" + w.el.ID
	var b strings.Builder
	b.WriteString("\t\t\t<nav aria-label=\"Menu\" class=\"elementor-nav-menu--main elementor-nav-menu__container\">\n")
	b.WriteString("\t\t\t\t<input type=\"checkbox\" id=\"" + id + "\" class=\"elementor-menu-toggle__state\" hidden>\n")
	b.WriteString("\t\t\t\t<label for=\"" + id + "\" class=\"elementor-menu-toggle\" role=\"button\" tabindex=\"0\" aria-label=\"Menu Toggle\">" +
		"<span class=\"elementor-menu-toggle__icon\" aria-hidden=\"true\"></span><span class=\"elementor-screen-only\">Menu</span></label>\n")
	b.WriteString("\t\t\t\t<ul class=\"elementor-nav-menu\">\n" + list.String() + "\t\t\t\t</ul>\n")
	b.WriteString("\t\t\t</nav>\n")
	return b.String()
}
