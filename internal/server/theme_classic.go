package server

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/microcosm-cc/bluemonday"

	"wpgo/internal/links"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Classic 1.0.0: a white, minimal blog theme written for WordPress Go (it ports no WordPress theme).
// Templates live in web/themes/classic. Every option is a theme mod set in Appearance › Customize
// (classicCustomizer): colors become CSS custom properties in an inline style, the other options body
// classes the stylesheet keys on, so most of them preview in place (transport postMessage, applied
// by assets/js/customize-preview.js).

const classicVersion = "1.0.0"

func init() {
	area := func(id, name, desc string) WidgetArea {
		return WidgetArea{ID: id, Name: name, Description: desc,
			BeforeWidget: `<section id="%1$s" class="widget %2$s">`, AfterWidget: `</section>`,
			BeforeTitle: `<h2 class="widget-title">`, AfterTitle: `</h2>`}
	}
	registerTheme(&Theme{
		Slug: "classic", Name: "Classic", Version: classicVersion,
		Description: "Classic is a white, clean and minimal blog theme for WordPress Go. Calm typography on system fonts, a readable measure, accessible colors and an optional automatic dark mode put your writing first. List, grid or titles-only blog layouts, an optional sidebar, three footer widget columns and social links menus are all set up in the Customizer, with a live preview. One small stylesheet and no render-blocking scripts keep it fast.",
		Author:      "WordPress Go", AuthorURI: "https://github.com/boracomet/wordpress-go", ThemeURI: "https://github.com/boracomet/wordpress-go",
		Tags: []string{"Blog", "One Column", "Two Columns", "Left Sidebar", "Right Sidebar", "Grid Layout", "Accessibility Ready", "Custom Colors",
			"Custom Logo", "Custom Menu", "Editor Style", "Featured Images", "Footer Widgets", "Post Formats", "Sticky Post", "Theme Options",
			"Threaded Comments", "Translation Ready", "Wide Blocks", "Block Styles"},
		Screenshot: "screenshot.png",
		// The footer columns come first: switching from another theme carries its first areas' widgets
		// into them (ensureThemeWidgets), where they show without any layout change.
		WidgetAreas: []WidgetArea{
			area("footer-1", "Footer 1", "Widgets in the first footer column."),
			area("footer-2", "Footer 2", "Widgets in the second footer column."),
			area("footer-3", "Footer 3", "Widgets in the third footer column."),
			area("sidebar-1", "Sidebar", "Shown beside the content when Customize › Layout › Sidebar is set to left or right."),
		},
		MenuLocations: []MenuLocation{{"primary", "Primary Menu"}, {"footer", "Footer Menu"}, {"social", "Social Links Menu"}},
		EditorStyles:  []string{"editor-style.css"},
		Palette:       classicPalette,
		Supports: map[string]any{
			"align-wide":         true,
			"custom-logo":        map[string]any{"width": 240, "height": 80, "flex-width": true, "flex-height": true, "header-text": []string{"site-title", "site-description"}, "unlink-homepage-logo": true},
			"custom-spacing":     true,
			"custom-line-height": true,
			"editor-styles":      true,
			"responsive-embeds":  true,
			"wp-block-styles":    true,
			"formats":            []string{"standard", "aside", "image", "video", "quote", "link", "gallery", "status", "audio"},
			"editor-font-sizes": []map[string]any{
				{"name": "Small", "shortName": "S", "size": 15, "slug": "small"},
				{"name": "Normal", "shortName": "M", "size": 18, "slug": "normal"},
				{"name": "Large", "shortName": "L", "size": 24, "slug": "large"},
				{"name": "Extra large", "shortName": "XL", "size": 34, "slug": "x-large"},
			},
		},
		HeadTop: "<script>document.documentElement.classList.replace('no-js','js');</script>\n",
		Head:    classicHead,
		Footer: func(s *Server, c *gin.Context, v *siteView) string {
			if v.Customize == nil {
				return ""
			}
			return "<script src=\"" + v.ThemeURI + "/js/customize-preview.js?ver=" + classicVersion + "\" id=\"classic-customize-preview-js\" defer></script>\n"
		},
		Prepare:     func(s *Server, c *gin.Context, v *siteView) { s.classicPrepare(c, v) },
		BodyClasses: classicBodyClasses,
	})
	czSpecs["classic"] = classicCustomizer
}

var classicPalette = []map[string]string{
	{"name": "Accent", "slug": "accent", "color": "#2563eb"},
	{"name": "Foreground", "slug": "foreground", "color": "#1d1d1f"},
	{"name": "Background", "slug": "background", "color": "#ffffff"},
	{"name": "Muted", "slug": "muted", "color": "#62626b"},
	{"name": "Subtle", "slug": "subtle", "color": "#f4f4f5"},
}

// Customizer ---------------------------------------------------------------------------

func classicSwitch(id, label, def string) czControl {
	return czControl{ID: id, Label: label, Type: "checkbox", Default: def, Section: "classic_posts"}
}

var classicCustomizer = &czThemeSpec{
	Colors: []czControl{
		{ID: "accent_color", Label: "Accent color", Description: "Buttons, highlights, focus rings and, by default, links.", Type: "color", Default: "#2563eb"},
		{ID: "bg_color", Label: "Background color", Type: "color", Default: "#ffffff"},
		{ID: "text_color", Label: "Text color", Type: "color", Default: "#1d1d1f"},
		{ID: "link_color", Label: "Link color", Description: "Leave empty to use the accent color.", Type: "color"},
		{ID: "header_bg_color", Label: "Header background", Description: "Leave empty to use the background color.", Type: "color"},
		{ID: "color_scheme", Label: "Dark mode", Description: "Auto follows each visitor's system setting. Dark mode uses a matching dark palette with your accent color. Colors are adjusted when needed to keep text readable.",
			Type: "radio", Default: "light", Choices: [][2]string{{"light", "Light only"}, {"auto", "Auto (follow the system)"}, {"dark", "Dark"}}},
		{ID: "dark_toggle", Label: "Show a dark mode toggle button in the header", Type: "checkbox", Default: "0"},
	},
	Sections: []czSection{
		{ID: "classic_typography", Title: "Typography", Description: "Classic uses the fonts already on your visitors' devices: nothing is downloaded."},
		{ID: "classic_layout", Title: "Layout"},
		{ID: "classic_posts", Title: "Blog & Posts"},
		{ID: "classic_footer", Title: "Footer"},
	},
	Controls: []czControl{
		{ID: "show_title", Label: "Display Site Title", Type: "checkbox", Default: "1", Section: "title_tagline"},
		{ID: "show_tagline", Label: "Display Tagline", Type: "checkbox", Default: "1", Section: "title_tagline"},

		{ID: "font_pairing", Label: "Font pairing", Description: "Headings / body text.", Type: "select", Default: "editorial", Section: "classic_typography",
			Choices: [][2]string{{"editorial", "Editorial: serif headings, sans body"}, {"modern", "Modern sans"}, {"classic", "Classic serif"},
				{"humanist", "Humanist sans"}, {"mono", "Mono accents: mono headings, sans body"}}},
		{ID: "font_size", Label: "Base font size", Type: "radio", Default: "medium", Section: "classic_typography",
			Choices: [][2]string{{"small", "Small"}, {"medium", "Medium"}, {"large", "Large"}}},
		{ID: "heading_weight", Label: "Heading weight", Type: "select", Default: "700", Section: "classic_typography",
			Choices: [][2]string{{"400", "Regular"}, {"500", "Medium"}, {"600", "Semibold"}, {"700", "Bold"}, {"800", "Extra bold"}}},
		{ID: "line_height", Label: "Line height", Type: "radio", Default: "normal", Section: "classic_typography",
			Choices: [][2]string{{"compact", "Compact"}, {"normal", "Normal"}, {"relaxed", "Relaxed"}}},

		{ID: "content_width", Label: "Content width", Type: "radio", Default: "normal", Section: "classic_layout",
			Choices: [][2]string{{"narrow", "Narrow (680px)"}, {"normal", "Normal (760px)"}, {"wide", "Wide (960px)"}}},
		{ID: "sidebar", Label: "Sidebar", Description: "Shows the Sidebar widget area when it has widgets.", Type: "radio", Default: "none", Section: "classic_layout",
			Choices: [][2]string{{"none", "No sidebar"}, {"right", "Right"}, {"left", "Left"}}},
		{ID: "blog_layout", Label: "Blog layout", Description: "The blog, archives and search results.", Type: "radio", Default: "list", Section: "classic_layout",
			Choices: [][2]string{{"list", "List"}, {"grid", "Grid of cards"}, {"minimal", "Minimal: titles only"}}},
		{ID: "header_layout", Label: "Header layout", Type: "radio", Default: "inline", Section: "classic_layout",
			Choices: [][2]string{{"inline", "Logo left, menu right"}, {"centered", "Centered"}}},
		{ID: "sticky_header", Label: "Sticky header", Description: "Keep the header at the top while scrolling.", Type: "checkbox", Default: "0", Section: "classic_layout"},

		{ID: "blog_content", Label: "On the blog, show", Type: "radio", Default: "excerpt", Section: "classic_posts",
			Choices: [][2]string{{"excerpt", "Excerpts"}, {"full", "Full posts"}}},
		{ID: "excerpt_length", Label: "Excerpt length (words)", Type: "number", Default: "32", Min: 10, Max: 120, Section: "classic_posts"},
		classicSwitch("show_thumb_list", "Featured images on the blog", "1"),
		classicSwitch("show_thumb_single", "Featured image on single posts", "1"),
		classicSwitch("show_author", "Author", "1"),
		classicSwitch("show_date", "Date", "1"),
		classicSwitch("show_reading_time", "Reading time", "1"),
		classicSwitch("show_categories", "Categories", "1"),
		classicSwitch("show_tags", "Tags on single posts", "1"),
		classicSwitch("show_author_bio", "Author bio box on single posts", "1"),
		classicSwitch("show_post_nav", "Previous / next post links", "1"),

		{ID: "footer_text", Label: "Footer text", Description: "Simple HTML (links, bold, italic). {year} is the current year, {site} the site title.",
			Type: "textarea", Default: "© {year} {site}", Section: "classic_footer", Sanitize: classicSanitizeFooter},
		{ID: "show_credit", Label: "Show “Proudly powered by WordPress Go”", Type: "checkbox", Default: "1", Section: "classic_footer"},
	},
	PostMessage: []string{"blogname", "blogdescription", "mod:show_title", "mod:show_tagline", "mod:accent_color", "mod:bg_color", "mod:text_color",
		"mod:link_color", "mod:header_bg_color", "mod:color_scheme", "mod:font_pairing", "mod:font_size", "mod:heading_weight", "mod:line_height",
		"mod:content_width", "mod:header_layout", "mod:sticky_header"},
}

var (
	classicFooterOnce   sync.Once
	classicFooterPolicy *bluemonday.Policy
)

// classicSanitizeFooter keeps the inline tags the footer text allows (wp_kses() with a small list).
func classicSanitizeFooter(raw string) string {
	classicFooterOnce.Do(func() {
		p := bluemonday.NewPolicy()
		p.AllowElements("strong", "em", "b", "i", "br", "small", "span", "code", "abbr")
		p.AllowAttrs("href").OnElements("a")
		p.AllowAttrs("rel").Matching(regexp.MustCompile(`^[a-z ]+$`)).OnElements("a")
		p.AllowAttrs("title").OnElements("a", "abbr", "span")
		p.AllowStandardURLs()
		p.RequireParseableURLs(true)
		p.RequireNoFollowOnLinks(false)
		classicFooterPolicy = p
	})
	return strings.TrimSpace(classicFooterPolicy.Sanitize(raw))
}

// classicOptions are the theme mods with their defaults applied.
type classicOptions struct {
	ShowTitle, ShowTagline           bool
	Scheme                           string // light, auto, dark
	DarkToggle                       bool
	Fonts, FontSize, Weight, Leading string
	Width, Sidebar, Layout, Header   string
	Sticky                           bool
	FullPosts                        bool
	ExcerptLength                    int
	ThumbList, ThumbSingle           bool
	Author, Date, ReadingTime        bool
	Categories, Tags, Bio, PostNav   bool
	FooterText                       string
	Credit                           bool
}

func classicOpts(mods map[string]any) classicOptions {
	sp := classicCustomizer
	str := func(id string) string {
		ctl := sp.control(id)
		v := modString(mods, id, ctl.Default)
		for _, ch := range ctl.Choices {
			if ch[0] == v {
				return v
			}
		}
		return ctl.Default
	}
	on := func(id string) bool { return modBool(mods, id, sp.control(id).Default == "1") }
	n, _ := strconv.Atoi(sp.control("excerpt_length").Default)
	return classicOptions{
		ShowTitle: on("show_title"), ShowTagline: on("show_tagline"), Scheme: str("color_scheme"), DarkToggle: on("dark_toggle"),
		Fonts: str("font_pairing"), FontSize: str("font_size"), Weight: str("heading_weight"), Leading: str("line_height"),
		Width: str("content_width"), Sidebar: str("sidebar"), Layout: str("blog_layout"), Header: str("header_layout"), Sticky: on("sticky_header"),
		FullPosts: str("blog_content") == "full", ExcerptLength: min(max(modInt(mods, "excerpt_length", n), 10), 120),
		ThumbList: on("show_thumb_list"), ThumbSingle: on("show_thumb_single"), Author: on("show_author"), Date: on("show_date"),
		ReadingTime: on("show_reading_time"), Categories: on("show_categories"), Tags: on("show_tags"), Bio: on("show_author_bio"),
		PostNav: on("show_post_nav"), FooterText: modString(mods, "footer_text", sp.control("footer_text").Default), Credit: on("show_credit"),
	}
}

// Colors -------------------------------------------------------------------------------

// The dark palette: background and text; accents and links are lifted from the chosen colors.
const classicDarkBG, classicDarkText = "#121316", "#e6e6e9"

// classicLuminance is the WCAG relative luminance of a hex color.
func classicLuminance(hex string) float64 {
	r, g, b, _ := hexRGB(hex)
	lin := func(c int) float64 {
		v := float64(c) / 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// classicContrast is the WCAG contrast ratio of two colors.
func classicContrast(a, b string) float64 {
	la, lb := classicLuminance(a), classicLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// classicMix moves color a toward b by t (0–1).
func classicMix(a, b string, t float64) string {
	r1, g1, b1, _ := hexRGB(a)
	r2, g2, b2, _ := hexRGB(b)
	mix := func(x, y int) int { return int(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return fmt.Sprintf("#%02x%02x%02x", mix(r1, r2), mix(g1, g2), mix(b1, b2))
}

// classicReadable returns fg, or fg moved toward black or white until it reaches the contrast ratio on bg.
func classicReadable(fg, bg string, ratio float64) string {
	if classicContrast(fg, bg) >= ratio {
		return fg
	}
	to := classicOnColor(bg)
	for i := 1; i <= 20; i++ {
		if c := classicMix(fg, to, float64(i)/20); classicContrast(c, bg) >= ratio {
			return c
		}
	}
	return to
}

// classicOnColor is the text color (near white or near black) with the better contrast on bg.
func classicOnColor(bg string) string {
	if classicContrast("#ffffff", bg) >= classicContrast("#111111", bg) {
		return "#ffffff"
	}
	return "#111111"
}

func classicColor(mods map[string]any, id string) string {
	if c := sanitizeHex(modString(mods, id, "")); c != "" {
		return c
	}
	return classicCustomizer.control(id).Default
}

// classicColorCSS prints the color tokens: the chosen (light) colors, made readable, and the dark
// variants. assets/style.css maps them to the --cl-* tokens per color scheme.
func classicColorCSS(mods map[string]any) string {
	bg := classicColor(mods, "bg_color")
	text := classicReadable(classicColor(mods, "text_color"), bg, 4.5)
	accent := classicColor(mods, "accent_color")
	accentText := classicReadable(accent, bg, 4.5)
	vars := [][2]string{
		{"accent-light", accent}, {"accent-text-light", accentText}, {"on-accent-light", classicOnColor(accent)},
		{"bg-light", bg}, {"text-light", text},
	}
	link := accent
	if c := sanitizeHex(modString(mods, "link_color", "")); c != "" {
		link = c
		vars = append(vars, [2]string{"link-light", classicReadable(c, bg, 4.5)})
	}
	if hb := sanitizeHex(modString(mods, "header_bg_color", "")); hb != "" {
		ht := text
		if classicContrast(ht, hb) < 4.5 {
			ht = classicOnColor(hb)
		}
		vars = append(vars, [2]string{"header-bg-light", hb}, [2]string{"header-text-light", ht})
	}
	accentDark := classicReadable(accent, classicDarkBG, 4.5)
	vars = append(vars, [2]string{"accent-dark", accentDark}, [2]string{"on-accent-dark", classicOnColor(accentDark)},
		[2]string{"link-dark", classicReadable(link, classicDarkBG, 4.5)})
	var sb strings.Builder
	sb.WriteString(":root{")
	for _, v := range vars {
		sb.WriteString("--cl-" + v[0] + ":" + v[1] + ";")
	}
	sb.WriteString("}")
	return inlineStyle("classic-style", sb.String())
}

// Head and body classes ----------------------------------------------------------------

func classicHead(s *Server, c *gin.Context, v *siteView) string {
	o := classicOpts(v.ThemeMods)
	u, ver := v.ThemeURI, "?ver="+classicVersion
	out := "<link rel='stylesheet' id='classic-style-css' href='" + u + "/style.css" + ver + "' media='all' />\n" + classicColorCSS(v.ThemeMods)
	bg := classicColor(v.ThemeMods, "bg_color")
	switch o.Scheme {
	case "dark":
		out += "<meta name=\"theme-color\" content=\"" + classicDarkBG + "\" />\n"
	case "auto":
		out += "<meta name=\"theme-color\" content=\"" + bg + "\" media=\"(prefers-color-scheme: light)\" />\n" +
			"<meta name=\"theme-color\" content=\"" + classicDarkBG + "\" media=\"(prefers-color-scheme: dark)\" />\n"
	default:
		out += "<meta name=\"theme-color\" content=\"" + bg + "\" />\n"
	}
	if o.DarkToggle && v.Customize == nil {
		// The visitor's choice from the toggle, applied before the first paint.
		out += "<script id=\"classic-color-scheme-js\">try{var s=localStorage.getItem('classic-color-scheme');if(s==='light'||s==='dark'){document.documentElement.setAttribute('data-color-scheme',s);}}catch(e){}</script>\n"
	}
	if cv, ok := v.Data["Classic"].(*classicView); ok && (cv.PrimaryNav != "" || o.DarkToggle) {
		out += "<script src=\"" + u + "/js/navigation.js" + ver + "\" id=\"classic-navigation-js\" defer data-wp-strategy=\"defer\"></script>\n"
	}
	return out
}

func classicBodyClasses(s *Server, v *siteView) []string {
	o := classicOpts(v.ThemeMods)
	out := []string{"classic-scheme-" + o.Scheme, "classic-fonts-" + o.Fonts, "classic-size-" + o.FontSize, "classic-heading-" + o.Weight,
		"classic-leading-" + o.Leading, "classic-width-" + o.Width, "classic-layout-" + o.Layout, "classic-header-" + o.Header,
		"classic-sidebar-" + o.Sidebar}
	if v.Template == "single" || v.Template == "page" {
		out = append(out, "singular")
	} else {
		out = append(out, "hfeed")
	}
	if o.Sticky {
		out = append(out, "classic-sticky-header")
	}
	if !o.ShowTitle {
		out = append(out, "classic-hide-title")
	}
	if !o.ShowTagline {
		out = append(out, "classic-hide-tagline")
	}
	if cv, ok := v.Data["Classic"].(*classicView); ok {
		if cv.Sidebar != "" {
			out = append(out, "has-sidebar")
		}
		if cv.PrimaryNav != "" {
			out = append(out, "has-primary-menu")
		}
	}
	if o.DarkToggle {
		out = append(out, "has-scheme-toggle")
	}
	return out
}

// Views --------------------------------------------------------------------------------

// classicView is what the Classic templates print, in v.Data["Classic"].
type classicView struct {
	Opt         *classicOptions
	FrontTitle  bool // the site title is the page's <h1> (blog front page)
	CurrentHome bool
	PrimaryNav  template.HTML
	FooterNav   template.HTML
	SocialNav   template.HTML
	Sidebar     template.HTML
	FooterAreas []template.HTML
	Items       []*classicPost
	Single      *classicPost
	PostNav     template.HTML
	// Archive header: "Category" and "Travel", the author's avatar on author archives.
	ArchiveLabel, ArchiveName string
	ArchiveAvatar             template.HTML
	ResultCount               string
	FooterText                template.HTML
	Icons                     map[string]template.HTML
}

// classicPost is one post as the content templates print it.
type classicPost struct {
	*postView
	Opt         *classicOptions
	Icons       map[string]template.HTML
	Class       string
	Thumb       template.HTML
	Caption     template.HTML
	Body        template.HTML
	IsExcerpt   bool // Body is an excerpt: the card links to the post
	ShowTitle   bool // aside and status posts have no title in lists
	FormatLabel string
	ReadingTime string
	Avatar      template.HTML
	Front       bool // the static front page
}

func (s *Server) classicPrepare(c *gin.Context, v *siteView) {
	ctx := v.ctx
	lb := s.links(ctx)
	q := v.Q
	o := classicOpts(v.ThemeMods)
	cv := &classicView{Opt: &o, Icons: classicIcons}
	v.Data["Classic"] = cv
	front := q.Kind == "front-page" || (q.Kind == "home" && q.PostsPage == nil)
	cv.FrontTitle = q.Kind == "home" && q.PostsPage == nil && q.Paged <= 1
	cv.CurrentHome = front && q.Paged <= 1

	// Menus: the primary menu falls back to the published pages, like wp_page_menu().
	if items := v.Menus["primary"]; len(items) > 0 {
		cv.PrimaryNav = renderMenu(items, "primary-menu-container", "primary-menu", "", "")
	} else {
		var current int64
		if q.Post != nil {
			current = q.Post.ID
		}
		cv.PrimaryNav = s.classicPageMenu(ctx, lb, current)
	}
	if items := v.Menus["footer"]; len(items) > 0 {
		cv.FooterNav = renderMenu(items, "footer-menu-container", "footer-menu", "", "")
	}
	if items := v.Menus["social"]; len(items) > 0 {
		cv.SocialNav = classicSocialMenu(items)
	}

	// Widget areas.
	if o.Sidebar != "none" && v.Template != "404" {
		cv.Sidebar = v.Areas["sidebar-1"]
	}
	for _, id := range []string{"footer-1", "footer-2", "footer-3"} {
		if a := v.Areas[id]; a != "" {
			cv.FooterAreas = append(cv.FooterAreas, a)
		}
	}

	// Posts.
	if v.Post != nil {
		cv.Single = s.classicPostData(c, v, v.Post, true, new(int), &o, lb)
		if v.Post.Type == "post" && o.PostNav {
			cv.PostNav = s.classicPostNav(ctx, v.Post.Post, lb)
		}
	}
	thumbs := 0
	for _, pv := range v.Posts {
		cv.Items = append(cv.Items, s.classicPostData(c, v, pv, false, &thumbs, &o, lb))
	}

	// Archive and search headers.
	if v.Template == "archive" {
		label, name, ok := strings.Cut(string(v.ArchiveTitle), ": ")
		if !ok {
			label, name = "", label
		}
		cv.ArchiveLabel = label
		cv.ArchiveName = html.UnescapeString(strings.TrimSuffix(strings.TrimPrefix(name, "<span>"), "</span>"))
		if q.Kind == "author" && q.Author != nil {
			cv.ArchiveAvatar = template.HTML(s.avatarImg(ctx, q.Author.Email, 72))
		}
	}
	if v.Template == "search" {
		if v.TotalPosts == 1 {
			cv.ResultCount = "1 result"
		} else {
			cv.ResultCount = formatNumber(v.TotalPosts) + " results"
		}
	}

	// Comments: "N comments" title and 48px avatars.
	if cm := v.Comments; cm != nil {
		if cm.Count == 1 {
			cm.Title = "One comment"
		} else {
			cm.Title = template.HTML(formatNumber(cm.Count) + " comments")
		}
		cm.List = template.HTML(strings.ReplaceAll(string(cm.List), "avatar-42 photo' height='42' width='42'", "avatar-48 photo' height='48' width='48'"))
		cm.Form = template.HTML(strings.Replace(strings.Replace(string(cm.Form), `class="comment-reply-title">Leave a Reply <small>`, `class="comment-reply-title">Leave a comment <small>`, 1),
			`rows="8"`, `rows="6"`, 1))
	}

	cv.FooterText = classicFooterHTML(o.FooterText, v.SiteName, time.Now().In(s.loc()).Year())
}

// classicFooterHTML is the footer text with its placeholders filled.
func classicFooterHTML(text, site string, year int) template.HTML {
	text = classicSanitizeFooter(text)
	return template.HTML(strings.NewReplacer("{year}", strconv.Itoa(year), "{site}", html.EscapeString(site)).Replace(text))
}

// classicPostData builds one post's template data; thumbs counts the featured images printed so far.
func (s *Server) classicPostData(c *gin.Context, v *siteView, pv *postView, singular bool, thumbs *int, o *classicOptions, lb *links.Builder) *classicPost {
	ctx := v.ctx
	p := pv.Post
	if p.Title == "" {
		pv.Title = "Untitled"
	}
	cp := &classicPost{postView: pv, Opt: o, Icons: classicIcons, Class: pv.Class, ShowTitle: true, Front: singular && v.Q.Kind == "front-page"}
	if pv.ShowFeatured {
		cp.Class += " is-sticky"
	}
	locked := s.passwordRequired(c, p)
	if p.Type == "post" && p.Format != "" && p.Format != "standard" {
		cp.FormatLabel = strings.ToUpper(p.Format[:1]) + p.Format[1:]
	}
	short := p.Format == "aside" || p.Format == "status"

	// Featured image: lazy, but eager with high priority for the first one on the page.
	if !locked && p.FeaturedMediaID > 0 && ((singular && o.ThumbSingle) || (!singular && o.ThumbList && o.Layout != "minimal")) {
		if a, err := s.store.GetAttachment(ctx, p.FeaturedMediaID); err == nil {
			size, sizes := "medium_large", "(min-width: 60rem) 760px, 100vw"
			switch {
			case singular:
				size, sizes = "large", "(min-width: 75rem) 1140px, 100vw"
			case o.Layout == "grid":
				sizes = "(min-width: 75rem) 360px, (min-width: 40rem) 50vw, 100vw"
			}
			cp.Thumb = classicImage(string(s.attachmentImage(ctx, a, size, "attachment-"+size+" size-"+size+" wp-post-image")), sizes, *thumbs == 0)
			*thumbs++
			if singular && a.Caption != "" {
				cp.Caption = template.HTML(wp.KsesPost(a.Caption))
			}
			cp.Class += " has-post-thumbnail"
		}
	}

	switch {
	case singular || locked:
		cp.Body = pv.Content
	case short:
		cp.Body, cp.ShowTitle = pv.Content, false
	case o.FullPosts && v.Template != "search":
		cp.Body = pv.Content
	default:
		cp.Body, cp.IsExcerpt = template.HTML(classicExcerpt(p, o.ExcerptLength)), true
	}
	if p.Type == "post" {
		words := len(strings.Fields(html.UnescapeString(wp.StripTags(p.Content))))
		cp.ReadingTime = strconv.Itoa(max(1, int(math.Round(float64(words)/220)))) + " min read"
		if singular && o.Bio && pv.AuthorBio != "" {
			cp.Avatar = template.HTML(s.avatarImg(ctx, pv.AuthorEmail, 64))
		}
	}
	return cp
}

var (
	classicSizesRe    = regexp.MustCompile(` sizes="[^"]*"`)
	classicDecodingRe = regexp.MustCompile(` decoding="async"`)
)

// classicImage sets the sizes attribute and native lazy loading (or fetchpriority for the first image).
func classicImage(img, sizes string, first bool) template.HTML {
	img = classicSizesRe.ReplaceAllString(img, ` sizes="`+sizes+`"`)
	load := ` loading="lazy" decoding="async"`
	if first {
		load = ` fetchpriority="high" decoding="async"`
	}
	return template.HTML(classicDecodingRe.ReplaceAllString(img, load))
}

// classicExcerpt is the_excerpt() with the Customizer's length; the card prints its own "Continue reading".
func classicExcerpt(p *store.Post, words int) string {
	if p.Password != "" {
		return "<p>There is no excerpt because this is a protected post.</p>\n"
	}
	if p.Excerpt != "" {
		return wp.Autop(wp.Texturize(p.Excerpt))
	}
	text, _, _, _ := wp.SplitMore(stripBlocks(p.Content))
	return "<p>" + wp.Texturize(wp.TrimWords(text, words, "&hellip;")) + "</p>\n"
}

// classicPostNav is the_post_navigation() with "Previous post" / "Next post" labels.
func (s *Server) classicPostNav(ctx context.Context, p *store.Post, lb *links.Builder) template.HTML {
	prev, _ := s.gooAdjacent(ctx, p, true) // Goocommerce Memberships: hidden posts are skipped
	next, _ := s.gooAdjacent(ctx, p, false)
	if prev == nil && next == nil {
		return ""
	}
	var sb strings.Builder
	link := func(class, rel, label string, q *store.Post) {
		fmt.Fprintf(&sb, `<div class="%s"><a href="%s" rel="%s"><span class="nav-label">%s</span> <span class="screen-reader-text">post:</span> <span class="nav-title">%s</span></a></div>`,
			class, html.EscapeString(lb.Permalink(q)), rel, label, theTitle(q))
	}
	if prev != nil {
		link("nav-previous", "prev", `<span aria-hidden="true">&larr;</span> Previous`, prev)
	}
	if next != nil {
		link("nav-next", "next", `Next <span aria-hidden="true">&rarr;</span>`, next)
	}
	return template.HTML(`<nav class="navigation post-navigation" aria-label="Posts"><h2 class="screen-reader-text">Post navigation</h2><div class="nav-links">` + sb.String() + `</div></nav>`)
}

// classicPageMenu is the wp_page_menu() fallback: top-level published pages.
func (s *Server) classicPageMenu(ctx context.Context, lb *links.Builder, current int64) template.HTML {
	pages, _, err := s.store.QueryPosts(ctx, store.PostQuery{Types: []string{"page"}, Statuses: []string{"publish"}, OrderBy: "menu_order", Order: "ASC", PerPage: -1})
	if err != nil {
		return ""
	}
	var sb strings.Builder
	n := 0
	for _, p := range pages {
		if p.ParentID != 0 || n >= 8 {
			continue
		}
		n++
		title := p.Title
		if title == "" {
			title = fmt.Sprintf("#%d (no title)", p.ID)
		}
		class, attr := "page_item page-item-"+itoa64(p.ID)+" menu-item", ""
		if p.ID == current {
			class, attr = class+" current_page_item current-menu-item", ` aria-current="page"`
		}
		fmt.Fprintf(&sb, `<li class="%s"><a href="%s"%s>%s</a></li>`+"\n", class, html.EscapeString(lb.Permalink(p)), attr, html.EscapeString(title))
	}
	if n == 0 {
		return ""
	}
	return template.HTML(`<div class="primary-menu-container"><ul id="menu-primary-menu" class="primary-menu">` + "\n" + sb.String() + `</ul></div>`)
}

// classicSocialMenu prints the social links menu as icons with screen reader labels.
func classicSocialMenu(items []*store.MenuItem) template.HTML {
	var sb strings.Builder
	sb.WriteString(`<ul class="social-links">`)
	for _, it := range items {
		target := ""
		if it.Target != "" {
			target = ` target="` + html.EscapeString(it.Target) + `"`
		}
		fmt.Fprintf(&sb, `<li class="menu-item menu-item-%d"><a href="%s"%s>%s<span class="screen-reader-text">%s</span></a></li>`,
			it.ID, html.EscapeString(it.URL), target, classicSocialIcon(it.URL), html.EscapeString(it.Label))
	}
	sb.WriteString(`</ul>`)
	return template.HTML(sb.String())
}

// classicSocialIcon picks the icon by the link's domain (Twenty Twenty's set plus X and Bluesky).
func classicSocialIcon(uri string) string {
	lower := strings.ToLower(uri)
	icon := ""
	switch {
	case strings.Contains(lower, "://x.com") || strings.Contains(lower, ".x.com"):
		icon = classicIconSVG(`<path fill="currentColor" d="M18.244 2.25h3.308l-7.227 8.26 8.502 11.24H16.17l-5.214-6.817L4.99 21.75H1.68l7.73-8.835L1.254 2.25H8.08l4.713 6.231zm-1.161 17.52h1.833L7.084 4.126H5.117z"/>`)
	case strings.Contains(lower, "bsky.app"):
		icon = classicIconSVG(`<path fill="currentColor" d="M6.3 3.8C8.6 5.5 11.1 9 12 10.9c.9-1.9 3.4-5.4 5.7-7.1 1.7-1.2 4.3-2.2 4.3.8 0 .6-.3 5-.5 5.7-.7 2.5-3.3 3.1-5.6 2.7 4 .7 5 2.9 2.8 5.2-4.2 4.3-6-1.1-6.5-2.5l-.2-.6-.2.6c-.5 1.4-2.3 6.8-6.5 2.5-2.2-2.3-1.2-4.5 2.8-5.2-2.3.4-4.9-.2-5.6-2.7C2.3 9.6 2 5.2 2 4.6c0-3 2.6-2 4.3-.8z"/>`)
	default:
		for _, ic := range twentyTwentySocialIcons {
			for _, d := range ic.Domains {
				if strings.Contains(lower, strings.ToLower(d)) {
					icon = strings.Replace(ic.SVG, `class="svg-icon"`, `class="icon"`, 1)
				}
			}
			if icon != "" {
				break
			}
		}
	}
	if icon == "" {
		icon = string(classicIcons["link"])
	}
	return icon
}

func classicIconSVG(body string) string {
	return `<svg class="icon" aria-hidden="true" focusable="false" xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">` + body + `</svg>`
}

func classicStrokeIcon(body string) template.HTML {
	return template.HTML(`<svg class="icon" aria-hidden="true" focusable="false" xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`)
}

// classicIcons are the interface icons (inline SVG, no icon font).
var classicIcons = map[string]template.HTML{
	"menu":   classicStrokeIcon(`<path d="M4 7h16M4 12h16M4 17h16"/>`),
	"close":  classicStrokeIcon(`<path d="M6 6l12 12M18 6L6 18"/>`),
	"search": classicStrokeIcon(`<circle cx="11" cy="11" r="7"/><path d="M20 20l-3.5-3.5"/>`),
	"sun":    classicStrokeIcon(`<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/>`),
	"moon":   classicStrokeIcon(`<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/>`),
	"link":   classicStrokeIcon(`<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>`),
	"pin":    classicStrokeIcon(`<path d="M12 17v5M9 3h6l-1 7 3 3H7l3-3z"/>`),
}
