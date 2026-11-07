package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/blocks"
	"wpgo/internal/links"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Twenty Twenty-Five 1.5, the default theme. WordPress ships it as a block theme (theme.json,
// templates/*.html, parts and patterns rendered by the block engine); the port renders the same
// markup with Go templates (web/themes/twentytwentyfive/templates): the header and footer parts, the
// index/home, single, page, archive, search and 404 templates with their query loops, post meta,
// post navigation, comments and "More posts".
//
// Styles: assets/style.min.css is generated once by tools/twentytwentyfive/build.php from the
// theme's theme.json with WordPress itself (global styles, font faces with font-display: swap,
// core's common block styles, the styles of the blocks the templates always print, the layout
// container rules and the theme's style.min.css). Core block styles of the blocks a page's content
// uses are inlined per page (Theme.BlockStyles, blockStylesFor), instead of the whole block library.
// The style variations' palettes are the Customizer's Color scheme (css/styles/<slug>.min.css,
// printed inline after the stylesheet, with the section styles a variation changes, such as the
// buttons of is-style-section-N). The navigation overlay runs on a small deferred script
// (js/navigation.js) loaded only when the header has a menu; the front end loads no jQuery.

const tt5Version = "1.5"

//go:embed defaults/twentytwentyfive-theme.json
var tt5ThemeJSON []byte

// tt5Schemes are the color variations of styles/colors (the Customizer's Color scheme choices).
var tt5Schemes = [][2]string{{"default", "Default"}, {"evening", "Evening"}, {"noon", "Noon"}, {"dusk", "Dusk"},
	{"afternoon", "Afternoon"}, {"twilight", "Twilight"}, {"morning", "Morning"}, {"sunrise", "Sunrise"}, {"midnight", "Midnight"}}

// tt5BundledBlocks are the blocks whose core styles style.min.css already carries (the header, footer
// and query loops); the pages that use other blocks inline those blocks' styles.
var tt5BundledBlocks = []string{"site-title", "site-tagline", "navigation", "navigation-link", "page-list", "group", "columns", "spacer",
	"paragraph", "heading", "post-title", "post-date", "post-content", "post-template", "post-featured-image", "query-pagination"}

func init() {
	registerTheme(&Theme{
		Slug: "twentytwentyfive", Name: "Twenty Twenty-Five", Version: tt5Version,
		Description: "Twenty Twenty-Five emphasizes simplicity and adaptability. It offers flexible design options, supported by a variety of patterns for different page types, such as services and landing pages, making it ideal for building personal blogs, professional portfolios, online magazines, or business websites. Its templates cater to various blog styles, from text-focused to image-heavy layouts. Additionally, it supports international typography and diverse color palettes, ensuring accessibility and customization for users worldwide.",
		Author:      "the WordPress team", AuthorURI: "https://wordpress.org/", ThemeURI: "https://wordpress.org/themes/twentytwentyfive/",
		Tags: []string{"One Column", "Custom Colors", "Custom Menu", "Custom Logo", "Editor Style", "Featured Images", "Full Site Editing",
			"Block Patterns", "RTL Language Support", "Sticky Post", "Threaded Comments", "Translation Ready", "Wide Blocks", "Block Styles",
			"Style Variations", "Accessibility Ready", "Blog", "Portfolio", "News"},
		Screenshot:    "screenshot.png",
		MenuLocations: []MenuLocation{{"primary", "Primary Menu"}, {"footer", "Footer Menu"}},
		EditorStyles:  []string{"css/editor-style.css"},
		Palette:       tt5Palette(),
		PreloadFonts:  []string{"fonts/manrope/Manrope-VariableFont_wght.woff2"},
		Supports: map[string]any{
			"align-wide": true, "responsive-embeds": true, "editor-styles": true, "wp-block-styles": false, "block-templates": true,
			"custom-logo":  map[string]any{"width": 120, "height": 120, "flex-width": true, "flex-height": true, "header-text": "", "unlink-homepage-logo": false},
			"post-formats": []string{"aside", "audio", "chat", "gallery", "image", "link", "quote", "status", "video"},
			"html5":        []string{"comment-form", "comment-list", "gallery", "caption", "style", "script", "search-form"},
		},
		Templates: map[string]string{"page-no-title": "Page No Title"},
		// Demo content sets (theme_demo_sets.go), built by tools/twentytwentyfive/demos.py.
		Demos: []themeDemoSet{
			{Slug: "wildflower-journal", Name: "Wildflower Journal", Description: "A nature and travel blog: meadows, creeks and quiet trails, with field notes and walking guides.", Image: "demos/wildflower-journal.webp"},
			{Slug: "botanica", Name: "Botanica", Description: "A garden studio and flower shop: seasonal flowers, plant care notes, workshops and opening hours.", Image: "demos/botanica.webp"},
			{Slug: "field-and-light", Name: "Field & Light", Description: "A landscape photography portfolio: a gallery homepage, prints and commissions, and a journal of stories behind the pictures.", Image: "demos/field-and-light.webp"},
			{Slug: "slow-seasons", Name: "Slow Seasons", Description: "A slow-living magazine: home and garden, seasonal cooking, time outdoors and short essays, with a newsletter page.", Image: "demos/slow-seasons.webp"},
		},
		// The theme's block patterns (patterns/*.php), converted by tools/twentytwentyfive/build.php.
		Patterns:           "patterns.json",
		BlockStyles:        tt5BundledBlocks,
		Settings:           tt5ThemeSettings(),
		EditorGlobalStyles: "css/editor-global.min.css",
		Head: func(s *Server, c *gin.Context, v *siteView) string {
			u := v.ThemeURI
			out := `<link rel="preload" href="` + u + `/fonts/manrope/Manrope-VariableFont_wght.woff2" as="font" type="font/woff2" crossorigin>` + "\n" +
				"<link rel='stylesheet' id='twentytwentyfive-style-css' href='" + u + "/style.min.css?ver=" + tt5Version + "' media='all' />\n"
			if css := s.tt5SchemeCSS(v.ThemeMods); css != "" {
				out += "<style id='twentytwentyfive-color-scheme-inline-css'>\n" + css + "\n</style>\n"
			}
			if v.Data["HasNavScript"] == true {
				out += "<script src=\"" + u + "/js/navigation.js?ver=" + tt5Version + "\" id=\"twentytwentyfive-navigation-js\" defer data-wp-strategy=\"defer\"></script>\n"
			}
			return out
		},
		Prepare: func(s *Server, c *gin.Context, v *siteView) { s.tt5Prepare(c, v) },
	})
	czSpecsMu.Lock()
	czSpecs["twentytwentyfive"] = &czThemeSpec{
		Colors: []czControl{{ID: "color_scheme", Label: "Color scheme", Type: "select", Default: "default", Choices: tt5Schemes,
			Description: "The color palettes of Twenty Twenty-Five's style variations."}},
	}
	czSpecsMu.Unlock()
}

// tt5ThemeSettings is theme.json's settings (the editor's presets and layout).
func tt5ThemeSettings() map[string]any {
	var tj struct {
		Settings map[string]any `json:"settings"`
	}
	_ = json.Unmarshal(tt5ThemeJSON, &tj)
	return tj.Settings
}

// tt5Palette is theme.json's color palette as editor-color-palette.
func tt5Palette() []map[string]string {
	var out []map[string]string
	st := tt5ThemeSettings()
	color, _ := st["color"].(map[string]any)
	list, _ := color["palette"].([]any)
	for _, p := range list {
		if m, ok := p.(map[string]any); ok {
			out = append(out, map[string]string{"name": fmt.Sprint(m["name"]), "slug": fmt.Sprint(m["slug"]), "color": fmt.Sprint(m["color"])})
		}
	}
	return out
}

// tt5Scheme is the Color scheme theme mod ("" for the default palette).
func tt5Scheme(mods map[string]any) string {
	v := modString(mods, "color_scheme", "default")
	for _, sc := range tt5Schemes[1:] {
		if sc[0] == v {
			return v
		}
	}
	return ""
}

// tt5SchemeCSS is the stylesheet of the chosen color variation: what differs from the default.
func (s *Server) tt5SchemeCSS(mods map[string]any) string {
	if sc := tt5Scheme(mods); sc != "" {
		return s.webFile("themes/twentytwentyfive/assets/css/styles/" + sc + ".min.css")
	}
	return ""
}

// tt5Post is one post as the theme's templates print it.
type tt5Post struct {
	*postView
	Class     string        // post_class() on the post-template <li>
	Thumb     template.HTML // core/post-featured-image
	Body      template.HTML // core/post-content
	Date      template.HTML // core/post-date
	WrittenBy template.HTML // twentytwentyfive/hidden-written-by
	Tags      template.HTML // core/post-terms (post_tag, is-style-post-terms-1)
}

// tt5Prepare computes the template parts into v.Data.
func (s *Server) tt5Prepare(c *gin.Context, v *siteView) {
	ctx := v.ctx
	lb := s.links(ctx)
	q := v.Q
	d := v.Data
	front := q.Paged <= 1 && (q.Kind == "front-page" || (q.Kind == "home" && q.PostsPage == nil))
	esc := html.EscapeString

	// Header: site logo (when set), site title, navigation.
	aria := ""
	if front {
		aria = ` aria-current="page"`
	}
	if v.SiteName != "" {
		d["SiteTitle"] = template.HTML(`<p class="wp-block-site-title"><a href="` + esc(v.HomeURL) + `" target="_self" rel="home"` + aria + `>` + esc(v.SiteName) + `</a></p>`)
		d["FooterTitle"] = template.HTML(`<h2 class="wp-block-site-title"><a href="` + esc(v.HomeURL) + `" target="_self" rel="home"` + aria + `>` + esc(v.SiteName) + `</a></h2>`)
	}
	if v.CustomLogo != "" {
		d["Logo"] = template.HTML(`<div class="wp-block-site-logo">` + string(v.CustomLogo) + `</div>`)
	}
	if v.Description != "" {
		d["Tagline"] = template.HTML(`<p class="wp-block-site-tagline">` + esc(v.Description) + `</p>`)
	}
	items := tt5NavItems(v.Menus["primary"])
	if len(v.Menus["primary"]) == 0 {
		items = tt5PageListItems(blocks.Render(ctx, `<!-- wp:page-list {"isNested":true} /-->`))
	}
	if items != "" {
		d["Nav"] = template.HTML(tt5Navigation(items))
		d["HasNavScript"] = true
	}
	if fi := tt5NavItems(v.Menus["footer"]); fi != "" {
		d["FooterNav"] = template.HTML(`<nav class="is-vertical wp-block-navigation is-layout-flex wp-container-core-navigation-is-layout-831b2db5 wp-block-navigation-is-layout-flex" aria-label="Footer"><ul class="wp-block-navigation__container is-vertical wp-block-navigation">` + fi + `</ul></nav>`)
	}

	// templates/page-no-title.html: the content alone, no title or featured image.
	if v.Post != nil && v.Post.Type == "page" {
		if tpl, _ := v.Post.Post.Meta["_wp_page_template"].(string); tpl == "page-no-title" {
			d["NoTitle"] = true
		}
	}

	// Posts.
	eager := true // the first featured image is the largest paint: no lazy loading, high priority
	if v.Post != nil {
		d["Single"] = s.tt5PostData(c, v, v.Post, true, lb, &eager)
		if v.Post.Type == "post" {
			d["PostNav"] = s.tt5PostNav(ctx, v.Post.Post, lb)
		}
	}
	var list []*tt5Post
	for _, pv := range v.Posts {
		list = append(list, s.tt5PostData(c, v, pv, false, lb, &eager))
	}
	d["Items"] = list
	if v.TotalPages > 1 {
		d["Pagination"] = tt5Pagination(lb, s.archiveBase(q, lb), max(q.Paged, 1), v.TotalPages)
	}

	// Titles of the home, archive and search templates.
	d["BlogTitle"] = "Blog"
	if q.PostsPage != nil && q.PostsPage.Title != "" {
		d["BlogTitle"] = theTitle(q.PostsPage)
	}
	if q.Kind == "search" {
		d["QueryTitle"] = template.HTML("Search results for: &#8220;" + esc(q.Search) + "&#8221;")
	}
	d["SearchForm"] = tt5SearchForm(v.HomeURL, q.Search)

	// "More posts" (single and search).
	if v.Template == "single" || v.Template == "search" {
		d["MorePosts"] = s.tt5MorePosts(ctx, lb, currentUser(c))
	}

	// comments.php: the comments block's title, 50px avatars and the post-comments-form wrapper.
	if cv := v.Comments; cv != nil {
		title := theTitle(v.Post.Post)
		if cv.Count == 1 {
			cv.Title = template.HTML("One response to &#8220;" + title + "&#8221;")
		} else {
			cv.Title = template.HTML(formatNumber(cv.Count) + " responses to &#8220;" + title + "&#8221;")
		}
		cv.List = template.HTML(strings.ReplaceAll(string(cv.List), "avatar-42 photo' height='42' width='42'", "avatar-50 photo' height='50' width='50'"))
		form := strings.Replace(string(cv.Form), `<h2 id="reply-title" class="comment-reply-title">`, `<h3 id="reply-title" class="comment-reply-title">`, 1)
		form = strings.Replace(form, `</small></h2>`, `</small></h3>`, 1)
		cv.Form = template.HTML(strings.Replace(form, `class="submit"`, `class="submit wp-block-button__link wp-element-button"`, 1))
	}
}

// tt5Navigation is core/navigation with TT5's header attributes: responsive overlay, right-aligned.
func tt5Navigation(items string) string {
	return `<nav class="is-responsive items-justified-right wp-block-navigation is-content-justification-right is-layout-flex wp-container-core-navigation-is-layout-6e943505 wp-block-navigation-is-layout-flex" aria-label="Menu"><button aria-haspopup="dialog" aria-label="Open menu" class="wp-block-navigation__responsive-container-open"><svg width="24" height="24" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M4 7.5h16v1.5H4z"></path><path d="M4 15h16v1.5H4z"></path></svg></button>
				<div class="wp-block-navigation__responsive-container has-text-color has-contrast-color has-background has-base-background-color" id="modal-1">
					<div class="wp-block-navigation__responsive-close" tabindex="-1">
						<div class="wp-block-navigation__responsive-dialog">
							<button aria-label="Close menu" class="wp-block-navigation__responsive-container-close"><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" aria-hidden="true" focusable="false"><path d="m13.06 12 6.47-6.47-1.06-1.06L12 10.94 5.53 4.47 4.47 5.53 10.94 12l-6.47 6.47 1.06 1.06L12 13.06l6.47 6.47 1.06-1.06L13.06 12Z"></path></svg></button>
							<div class="wp-block-navigation__responsive-container-content" id="modal-1-content">
								<ul class="wp-block-navigation__container is-responsive items-justified-right wp-block-navigation">` + items + `</ul>
							</div>
						</div>
					</div>
				</div></nav>`
}

const tt5SubmenuIcon = `<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true" focusable="false"><path d="M1.50002 4L6.00002 8L10.5 4" stroke-width="1.5"></path></svg>`

// tt5NavItems renders menu items as core/navigation-link and core/navigation-submenu.
func tt5NavItems(items []*store.MenuItem) string {
	var sb strings.Builder
	for _, it := range items {
		label := html.EscapeString(it.Label)
		cls, aria := "", ""
		if it.Current {
			cls, aria = " current-menu-item", ` aria-current="page"`
		}
		if it.CurrentAncestor {
			cls += " current-menu-ancestor"
		}
		if it.Classes != "" {
			cls += " " + html.EscapeString(it.Classes)
		}
		target := ""
		if it.Target != "" {
			target = ` target="` + html.EscapeString(it.Target) + `"`
		}
		link := `<a class="wp-block-navigation-item__content" href="` + html.EscapeString(it.URL) + `"` + target + aria + `><span class="wp-block-navigation-item__label">` + label + `</span></a>`
		if len(it.Children) == 0 {
			sb.WriteString(`<li class="wp-block-navigation-item` + cls + ` wp-block-navigation-link">` + link + `</li>`)
			continue
		}
		sb.WriteString(`<li class="wp-block-navigation-item has-child open-on-hover-click` + cls + ` wp-block-navigation-submenu">` + link +
			`<button aria-label="` + label + ` submenu" class="wp-block-navigation__submenu-icon wp-block-navigation-submenu__toggle" aria-expanded="false">` + tt5SubmenuIcon +
			`</button><ul class="wp-block-navigation__submenu-container wp-block-navigation-submenu">` + tt5NavItems(it.Children) + `</ul></li>`)
	}
	return sb.String()
}

// tt5PageListItems turns core/page-list items into navigation items: the navigation block's
// fallback when no menu is assigned.
func tt5PageListItems(list string) string {
	if strings.TrimSpace(list) == "" {
		return ""
	}
	list = strings.ReplaceAll(list, `<li class="wp-block-pages-list__item`, `<li class="wp-block-pages-list__item wp-block-navigation-item open-on-hover-click wp-block-navigation-submenu`)
	list = strings.ReplaceAll(list, `class="wp-block-pages-list__item__link"`, `class="wp-block-pages-list__item__link wp-block-navigation-item__content"`)
	return strings.ReplaceAll(list, `<ul class="wp-block-navigation__submenu-container">`, `<ul class="wp-block-navigation__submenu-container wp-block-navigation-submenu">`)
}

// tt5SearchForm is core/search as twentytwentyfive/hidden-search configures it.
func tt5SearchForm(home, query string) template.HTML {
	return template.HTML(`<form role="search" method="get" action="` + html.EscapeString(home) + `" class="wp-block-search__button-outside wp-block-search__text-button wp-block-search"><label class="wp-block-search__label screen-reader-text" for="wp-block-search__input-1">Search</label><div class="wp-block-search__inside-wrapper"><input class="wp-block-search__input" id="wp-block-search__input-1" placeholder="Type here..." value="` +
		html.EscapeString(query) + `" type="search" name="s" required /><button aria-label="Search" class="wp-block-search__button wp-element-button" type="submit">Search</button></div></form>`)
}

// tt5PostData builds the block outputs of one post.
func (s *Server) tt5PostData(c *gin.Context, v *siteView, pv *postView, singular bool, lb *links.Builder, eager *bool) *tt5Post {
	ctx := v.ctx
	p := pv.Post
	esc := html.EscapeString
	tp := &tt5Post{postView: pv, Class: "wp-block-post " + pv.Class, Body: pv.Content}
	locked := s.passwordRequired(c, p)

	// core/post-featured-image: 3:2 and linked in the loop and on posts, as is on pages.
	if !locked && p.FeaturedMediaID > 0 {
		if a, err := s.store.GetAttachment(ctx, p.FeaturedMediaID); err == nil {
			aspect := p.Type == "post" || !singular
			img := s.tt5Image(ctx, a, aspect, *eager)
			*eager = false
			switch {
			case !singular:
				tp.Thumb = template.HTML(`<figure style="aspect-ratio:3/2;" class="wp-block-post-featured-image"><a href="` + esc(pv.Permalink) + `" target="_self">` + img + `</a></figure>`)
			case aspect:
				tp.Thumb = template.HTML(`<figure style="aspect-ratio:3/2;" class="wp-block-post-featured-image">` + img + `</figure>`)
			default:
				tp.Thumb = template.HTML(`<figure style="margin-bottom:var(--wp--preset--spacing--60)" class="wp-block-post-featured-image">` + img + `</figure>`)
			}
		}
	}

	if p.Type == "post" {
		tp.Date = template.HTML(`<a href="` + esc(pv.Permalink) + `"><time datetime="` + pv.DateISO + `">` + esc(pv.DateText) + `</time></a>`)
	}
	if singular && p.Type == "post" {
		var sb strings.Builder
		sb.WriteString(`<div class="wp-block-group has-small-font-size is-layout-flex wp-container-core-group-is-layout-fd1a9a6d wp-block-group-is-layout-flex" style="margin-bottom:var(--wp--preset--spacing--60)">` + "\n\t<p>Written by </p>\n")
		if pv.AuthorName != "" {
			sb.WriteString(`<div class="wp-block-post-author-name"><a href="` + esc(pv.AuthorURL) + `" target="_self" class="wp-block-post-author-name__link">` + esc(pv.AuthorName) + "</a></div>\n")
		}
		if len(pv.Categories) > 0 {
			sb.WriteString("\t<p>in</p>\n" + `<div style="font-weight:300" class="taxonomy-category wp-block-post-terms">` + tt5Terms(pv.Categories, ", ") + "</div>\n")
		}
		sb.WriteString("</div>")
		tp.WrittenBy = template.HTML(sb.String())
		if len(pv.Tags) > 0 {
			tp.Tags = template.HTML(`<div class="taxonomy-post_tag is-style-post-terms-1 wp-block-post-terms">` + tt5Terms(pv.Tags, "  ") + `</div>`)
		}
	}
	return tp
}

// tt5Image is the featured image <img>: the large size with srcset, sizes for the 645px content
// column, width and height, and lazy loading except for the first image of the page.
func (s *Server) tt5Image(ctx context.Context, a *store.Attachment, aspect bool, eager bool) string {
	src, w, h := s.uploadURL(ctx, a.FilePath), a.Width, a.Height
	if vr := a.Variant("large"); vr != nil {
		src, w, h = s.uploadURL(ctx, vr.FilePath), vr.Width, vr.Height
	}
	set := []string{fmt.Sprintf("%s %dw", s.uploadURL(ctx, a.FilePath), a.Width)}
	for _, n := range []string{"medium", "medium_large", "large", "1536x1536", "2048x2048"} {
		if vr := a.Variant(n); vr != nil {
			set = append(set, fmt.Sprintf("%s %dw", s.uploadURL(ctx, vr.FilePath), vr.Width))
		}
	}
	style := ""
	if aspect {
		style = ` style="width:100%;height:100%;object-fit:cover;aspect-ratio:3/2;"`
	}
	load := ` loading="lazy"`
	if eager {
		load = ` fetchpriority="high"`
	}
	return fmt.Sprintf(`<img width="%d" height="%d" src="%s" class="attachment-post-thumbnail size-post-thumbnail wp-post-image" alt="%s"%s decoding="async"%s srcset="%s" sizes="(max-width: 645px) 100vw, 645px" />`,
		w, h, html.EscapeString(src), html.EscapeString(a.Alt), style, load, html.EscapeString(strings.Join(set, ", ")))
}

func tt5Terms(terms []termLink, sep string) string {
	out := make([]string, len(terms))
	for i, t := range terms {
		out[i] = `<a href="` + html.EscapeString(t.URL) + `" rel="tag">` + html.EscapeString(t.Name) + `</a>`
	}
	return strings.Join(out, `<span class="wp-block-post-terms__separator">`+sep+`</span>`)
}

// tt5PostNav is twentytwentyfive/post-navigation: previous and next post links with arrows.
func (s *Server) tt5PostNav(ctx context.Context, p *store.Post, lb *links.Builder) template.HTML {
	prev, _ := s.gooAdjacent(ctx, p, true) // Goocommerce Memberships: hidden posts are skipped
	next, _ := s.gooAdjacent(ctx, p, false)
	if prev == nil && next == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(`<div class="wp-block-group alignwide is-layout-flow wp-block-group-is-layout-flow" style="margin-top:var(--wp--preset--spacing--60);margin-bottom:var(--wp--preset--spacing--60);">
	<nav class="wp-block-group alignwide is-content-justification-space-between is-nowrap is-layout-flex wp-container-core-group-is-layout-878fe601 wp-block-group-is-layout-flex" aria-label="Post navigation" style="border-top-color:var(--wp--preset--color--accent-6);border-top-width:1px;padding-top:var(--wp--preset--spacing--40);padding-bottom:var(--wp--preset--spacing--40)">
		<div class="post-navigation-link-previous wp-block-post-navigation-link">`)
	if prev != nil {
		sb.WriteString(`<span class="wp-block-post-navigation-link__arrow-previous is-arrow-arrow" aria-hidden="true">←</span><a href="` + html.EscapeString(lb.Permalink(prev)) + `" rel="prev">` + wp.Texturize(wp.EscText(prev.Title)) + `</a>`)
	}
	sb.WriteString(`</div>
		<div class="post-navigation-link-next wp-block-post-navigation-link">`)
	if next != nil {
		sb.WriteString(`<a href="` + html.EscapeString(lb.Permalink(next)) + `" rel="next">` + wp.Texturize(wp.EscText(next.Title)) + `</a><span class="wp-block-post-navigation-link__arrow-next is-arrow-arrow" aria-hidden="true">→</span>`)
	}
	sb.WriteString(`</div>
	</nav>
</div>`)
	return template.HTML(sb.String())
}

// tt5MorePosts is twentytwentyfive/more-posts: the four latest posts.
func (s *Server) tt5MorePosts(ctx context.Context, lb *links.Builder, u *store.User) template.HTML {
	pq := store.PostQuery{Types: []string{"post"}, Statuses: []string{"publish"}, OrderBy: "date", Order: "DESC", PerPage: 4, Page: 1}
	pq.ExcludeID = s.gooListingExclude(withPluginUser(ctx, u), "post") // Goocommerce Memberships: hidden restricted posts
	posts, _, err := s.store.QueryPosts(ctx, pq)
	if err != nil || len(posts) == 0 {
		return ""
	}
	loc := s.loc()
	df := s.settings.String("date_format")
	var sb strings.Builder
	for _, p := range posts {
		link := html.EscapeString(lb.Permalink(p))
		date := p.Date().In(loc)
		fmt.Fprintf(&sb, `<li class="wp-block-post post-%d post type-post status-publish format-%s hentry">
			<div class="wp-block-group alignfull is-content-justification-space-between is-nowrap is-layout-flex wp-container-core-group-is-layout-cba70755 wp-block-group-is-layout-flex" style="border-bottom-color:var(--wp--preset--color--accent-6);border-bottom-width:1px;padding-top:var(--wp--preset--spacing--30);padding-bottom:var(--wp--preset--spacing--30)">
				<h3 class="wp-block-post-title has-large-font-size"><a href="%s" target="_self">%s</a></h3>
				<div class="has-text-align-right wp-block-post-date"><a href="%s"><time datetime="%s">%s</time></a></div>
			</div>
		</li>`, p.ID, defaultStr(p.Format, "standard"), link, theTitle(p), link, date.Format("2006-01-02T15:04:05-07:00"), html.EscapeString(wp.FormatDate(date, df)))
	}
	return template.HTML(sb.String())
}

// tt5Pagination is core/query-pagination with previous/next arrows and the page numbers
// (paginate_links() with end_size 1 and mid_size 2).
func tt5Pagination(lb *links.Builder, base string, current, total int) template.HTML {
	if total < 2 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(`<nav class="alignwide wp-block-query-pagination is-content-justification-space-between is-layout-flex wp-container-core-query-pagination-is-layout-4dea2dca wp-block-query-pagination-is-layout-flex" aria-label="Pagination">` + "\n")
	if current > 1 {
		fmt.Fprintf(&sb, `<a href="%s" class="wp-block-query-pagination-previous"><span class="wp-block-query-pagination-previous-arrow is-arrow-arrow" aria-hidden="true">←</span>Previous Page</a>`+"\n", html.EscapeString(lb.Paged(base, current-1)))
	}
	sb.WriteString(`<div class="wp-block-query-pagination-numbers">`)
	var nums []string
	dots := false
	for n := 1; n <= total; n++ {
		switch {
		case n == current:
			nums = append(nums, fmt.Sprintf(`<span aria-current="page" class="page-numbers current">%d</span>`, n))
			dots = true
		case n <= 1 || (n >= current-2 && n <= current+2) || n > total-1:
			nums = append(nums, fmt.Sprintf(`<a class="page-numbers" href="%s">%d</a>`, html.EscapeString(lb.Paged(base, n)), n))
			dots = true
		case dots:
			nums = append(nums, `<span class="page-numbers dots">&hellip;</span>`)
			dots = false
		}
	}
	sb.WriteString(strings.Join(nums, "\n") + "</div>\n")
	if current < total {
		fmt.Fprintf(&sb, `<a href="%s" class="wp-block-query-pagination-next">Next Page<span class="wp-block-query-pagination-next-arrow is-arrow-arrow" aria-hidden="true">→</span></a>`+"\n", html.EscapeString(lb.Paged(base, current+1)))
	}
	sb.WriteString("</nav>")
	return template.HTML(sb.String())
}
