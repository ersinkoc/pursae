package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/links"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Twenty Seventeen 4.2: the parts of functions.php and inc/*.php that need code. Templates live in
// web/themes/twentyseventeen/templates; the theme's files are served from its assets/ directory, so
// /wp-content/themes/twentyseventeen/assets/css/blocks.css is assets/assets/css/blocks.css on disk.
//
// The template tags are precomputed into v.Data by tt17Prepare:
//
//	Data["HeaderImage"]   the_custom_header_markup() image (the bundled default header)
//	Data["TopMenu"]       wp_nav_menu( top ) with the angle-down icons
//	Data["SocialMenu"]    wp_nav_menu( social ) with the social icons
//	Data["IsFrontPage"]   is_front_page()
//	Data["StaticFront"]   twentyseventeen_is_frontpage() (front-page.php)
//	Data["FeaturedImage"] the .single-featured-image-header thumbnail
//	Data["Posts"]         []*tt17Post for the loop
//	Data["Post"]          *tt17Post for singular views
//	Data["Pagination"]    the_posts_pagination() with the arrow icons
//	Data["PostNav"]       the_post_navigation() with the arrow icons
//	Data["Comments"]      *commentsView with Twenty Seventeen's title, avatars and reply icons
//	Data["FooterWidgets"] template-parts/footer/footer-widgets.php

const tt17StyleVer = "20260819"

func init() {
	registerTheme(&Theme{
		Slug: "twentyseventeen", Name: "Twenty Seventeen", Version: "4.2",
		Description: "Twenty Seventeen brings your site to life with header video and immersive featured images. With a focus on business sites, it features multiple sections on the front page as well as widgets, navigation and social menus, a logo, and more. Personalize its asymmetrical grid with a custom color scheme and showcase your multimedia content with post formats. Our default theme for 2017 works great in many languages, for any abilities, and on any device.",
		Author:      "the WordPress team", AuthorURI: "https://wordpress.org/", ThemeURI: "https://wordpress.org/themes/twentyseventeen/",
		Tags: []string{"One Column", "Two Columns", "Right Sidebar", "Flexible Header", "Accessibility Ready", "Custom Colors", "Custom Header", "Custom Menu",
			"Custom Logo", "Editor Style", "Featured Images", "Footer Widgets", "Post Formats", "RTL Language Support", "Sticky Post", "Theme Options",
			"Threaded Comments", "Translation Ready", "Block Patterns"},
		Screenshot: "screenshot.png",
		WidgetAreas: []WidgetArea{
			{ID: "sidebar-1", Name: "Blog Sidebar", Description: "Add widgets here to appear in your sidebar on blog posts and archive pages.",
				BeforeWidget: `<section id="%1$s" class="widget %2$s">`, AfterWidget: `</section>`, BeforeTitle: `<h2 class="widget-title">`, AfterTitle: `</h2>`},
			{ID: "sidebar-2", Name: "Footer 1", Description: "Add widgets here to appear in your footer.",
				BeforeWidget: `<section id="%1$s" class="widget %2$s">`, AfterWidget: `</section>`, BeforeTitle: `<h2 class="widget-title">`, AfterTitle: `</h2>`},
			{ID: "sidebar-3", Name: "Footer 2", Description: "Add widgets here to appear in your footer.",
				BeforeWidget: `<section id="%1$s" class="widget %2$s">`, AfterWidget: `</section>`, BeforeTitle: `<h2 class="widget-title">`, AfterTitle: `</h2>`},
		},
		MenuLocations:     []MenuLocation{{"top", "Top Menu"}, {"social", "Social Links Menu"}},
		EditorStyles:      []string{"assets/css/editor-style.css", "assets/fonts/font-libre-franklin.css"},
		PreloadFonts:      []string{"assets/fonts/libre-franklin/libre-franklin-latin-300-normal.woff2", "assets/fonts/libre-franklin/libre-franklin-latin-800-normal.woff2"},
		EditorBlockStyles: []string{"assets/css/editor-blocks.css"},
		Supports: map[string]any{
			"automatic-feed-links": true, "title-tag": true, "custom-line-height": true, "post-thumbnails": true,
			"html5":         []string{"comment-form", "comment-list", "gallery", "caption", "script", "style", "navigation-widgets"},
			"post-formats":  []string{"aside", "image", "video", "quote", "link", "gallery", "audio"},
			"custom-logo":   map[string]any{"width": 250, "height": 250, "flex-width": true},
			"editor-styles": true, "wp-block-styles": true, "responsive-embeds": true, "customize-selective-refresh-widgets": true,
		},
		Head:        tt17Head,
		Footer:      tt17Footer,
		Prepare:     tt17Prepare,
		BodyClasses: tt17BodyClasses,
	})
}

// tt17SVG is twentyseventeen_get_svg().
func tt17SVG(icon string, fallback bool) string {
	fb := ""
	if fallback {
		fb = `<span class="svg-fallback icon-` + icon + `"></span>`
	}
	return `<svg class="icon icon-` + icon + `" aria-hidden="true" role="img"> <use href="#icon-` + icon + `" xlink:href="#icon-` + icon + `"></use> ` + fb + `</svg>`
}

// tt17JSON is wp_json_encode( $l10n, JSON_HEX_TAG | JSON_UNESCAPED_SLASHES ), as wp_localize_script() prints it.
func tt17JSON(v any) string {
	b, _ := json.Marshal(v)
	return strings.NewReplacer(`\`+"u003c", `\`+"u003C", `\`+"u003e", `\`+"u003E", `\`+"u0026", "&").Replace(string(b))
}

// tt17Head is twentyseventeen_scripts(): fonts, style.css, blocks.css, global.js (+ navigation.js with a top menu).
func tt17Head(s *Server, c *gin.Context, v *siteView) string {
	u := v.ThemeURI
	_, hasTop := v.Menus["top"]
	type l10n struct {
		Quote    string `json:"quote"`
		Expand   string `json:"expand,omitempty"`
		Collapse string `json:"collapse,omitempty"`
		Icon     string `json:"icon,omitempty"`
	}
	strs := l10n{Quote: tt17SVG("quote-right", false)}
	if hasTop {
		strs.Expand, strs.Collapse, strs.Icon = "Expand child menu", "Collapse child menu", tt17SVG("angle-down", true)
	}
	var sb strings.Builder
	sb.WriteString("<link rel='stylesheet' id='twentyseventeen-fonts-css' href='" + u + "/assets/fonts/font-libre-franklin.css?ver=20230328' media='all' />\n")
	sb.WriteString("<link rel='stylesheet' id='twentyseventeen-style-css' href='" + u + "/style.css?ver=" + tt17StyleVer + "' media='all' />\n")
	sb.WriteString("<link rel='stylesheet' id='twentyseventeen-block-style-css' href='" + u + "/assets/css/blocks.css?ver=20240729' media='all' />\n")
	sb.WriteString(tt17ColorStyles(v))
	sb.WriteString("<script src=\"/wp-includes/js/jquery/jquery.min.js?ver=3.7.1\" id=\"jquery-core-js\"></script>\n")
	sb.WriteString("<script src=\"/wp-includes/js/jquery/jquery-migrate.min.js?ver=3.4.1\" id=\"jquery-migrate-js\"></script>\n")
	sb.WriteString("<script id=\"twentyseventeen-global-js-extra\">\nvar twentyseventeenScreenReaderText = " + tt17JSON(strs) + ";\n//# sourceURL=twentyseventeen-global-js-extra\n</script>\n")
	sb.WriteString("<script src=\"" + u + "/assets/js/global.js?ver=20211130\" id=\"twentyseventeen-global-js\" defer data-wp-strategy=\"defer\"></script>\n")
	if hasTop {
		sb.WriteString("<script src=\"" + u + "/assets/js/navigation.js?ver=20210122\" id=\"twentyseventeen-navigation-js\" defer data-wp-strategy=\"defer\"></script>\n")
	}
	sb.WriteString(tt17CustomColors(v))
	return sb.String()
}

// tt17Footer prints jquery.scrollTo (in the footer, deferred) and, at wp_footer priority 9999,
// twentyseventeen_include_svg_icons(): the inline SVG sprite every icon <use>s.
func tt17Footer(s *Server, c *gin.Context, v *siteView) string {
	return "<script src=\"" + v.ThemeURI + "/assets/js/jquery.scrollTo.js?ver=2.1.3\" id=\"jquery-scrollto-js\" defer data-wp-strategy=\"defer\"></script>\n" +
		s.webFile("themes/twentyseventeen/assets/assets/images/svg-icons.svg") + "\n"
}

// tt17BodyClasses is twentyseventeen_body_classes() on a fresh install (no theme mods: the default
// header image, the light color scheme and the two-column page layout).
func tt17BodyClasses(s *Server, v *siteView) []string {
	var out []string
	if multi, _ := s.multiAuthor(v.ctx); multi {
		out = append(out, "group-blog")
	}
	singular := v.Template == "single" || v.Template == "page"
	if !singular {
		out = append(out, "hfeed")
	}
	if v.Q.Kind == "front-page" {
		out = append(out, "twentyseventeen-front-page")
	}
	out = append(out, "has-header-image")
	if v.Areas["sidebar-1"] != "" && v.Template != "page" {
		out = append(out, "has-sidebar")
	}
	switch v.Q.Kind {
	case "page", "front-page", "category", "tag", "author", "date":
		out = append(out, "page-two-column")
	}
	return append(out, "colors-"+tt17Scheme(v))
}

// tt17Post is one post in the loop with Twenty Seventeen's template tags.
type tt17Post struct {
	P        *postView
	Class    string
	Meta     template.HTML // .entry-meta contents
	Footer   template.HTML // twentyseventeen_entry_footer()
	EditLink template.HTML // twentyseventeen_edit_link()
	Thumb    template.HTML // the_post_thumbnail( 'twentyseventeen-featured-image' )
	Excerpt  template.HTML
	H3       bool // is_front_page() && is_home(): titles are h3
}

const tt17ListSizes = "(max-width: 767px) 89vw, (max-width: 1000px) 54vw, (max-width: 1071px) 543px, 580px"

var tt17SizesRe = regexp.MustCompile(`sizes="[^"]*"`)

// tt17Thumbnail is get_the_post_thumbnail( 'twentyseventeen-featured-image' ) with
// twentyseventeen_post_thumbnail_sizes_attr()'s sizes.
func (s *Server) tt17Thumbnail(ctx context.Context, p *store.Post, sizes string) template.HTML {
	if p == nil || p.FeaturedMediaID == 0 {
		return ""
	}
	a, err := s.store.GetAttachment(ctx, p.FeaturedMediaID)
	if err != nil {
		return ""
	}
	img := string(s.attachmentImage(ctx, a, "twentyseventeen-featured-image",
		"attachment-twentyseventeen-featured-image size-twentyseventeen-featured-image wp-post-image"))
	return template.HTML(tt17SizesRe.ReplaceAllString(img, `sizes="`+sizes+`"`))
}

// tt17TimeLink is twentyseventeen_time_link().
func tt17TimeLink(pv *postView) string {
	t := fmt.Sprintf(`<time class="entry-date published updated" datetime="%s">%s</time>`, pv.DateISO, html.EscapeString(pv.DateText))
	if pv.Date.Unix() != pv.Modified.Unix() {
		t = fmt.Sprintf(`<time class="entry-date published" datetime="%s">%s</time><time class="updated" datetime="%s">%s</time>`,
			pv.DateISO, html.EscapeString(pv.DateText), pv.ModifiedISO, html.EscapeString(pv.ModifiedText))
	}
	return `<span class="screen-reader-text">Posted on</span> <a href="` + html.EscapeString(pv.Permalink) + `" rel="bookmark">` + t + `</a>`
}

func tt17EditLink(pv *postView) string {
	if pv.EditLink == "" {
		return ""
	}
	return `<span class="edit-link">` + string(pv.EditLink) + `</span>`
}

// tt17EntryFooter is twentyseventeen_entry_footer().
func (s *Server) tt17EntryFooter(ctx context.Context, pv *postView) string {
	var cats, tags []string
	if s.categorizedBlog(ctx) {
		for _, t := range pv.Categories {
			cats = append(cats, `<a href="`+html.EscapeString(t.URL)+`" rel="category tag">`+html.EscapeString(t.Name)+`</a>`)
		}
	}
	for _, t := range pv.Tags {
		tags = append(tags, `<a href="`+html.EscapeString(t.URL)+`" rel="tag">`+html.EscapeString(t.Name)+`</a>`)
	}
	if len(cats) == 0 && len(tags) == 0 && pv.EditLink == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(`<footer class="entry-footer">`)
	if pv.Type == "post" && len(cats)+len(tags) > 0 {
		sb.WriteString(`<span class="cat-tags-links">`)
		if len(cats) > 0 {
			sb.WriteString(`<span class="cat-links">` + tt17SVG("folder-open", false) + `<span class="screen-reader-text">Categories</span>` + strings.Join(cats, ", ") + `</span>`)
		}
		if len(tags) > 0 {
			sb.WriteString(`<span class="tags-links">` + tt17SVG("hashtag", false) + `<span class="screen-reader-text">Tags</span>` + strings.Join(tags, ", ") + `</span>`)
		}
		sb.WriteString(`</span>`)
	}
	sb.WriteString(tt17EditLink(pv))
	sb.WriteString(`</footer> <!-- .entry-footer -->`)
	return sb.String()
}

// tt17Excerpt is the_excerpt() with twentyseventeen_excerpt_more().
func tt17Excerpt(p *store.Post, permalink string) template.HTML {
	if p.Password != "" {
		return "<p>There is no excerpt because this is a protected post.</p>\n"
	}
	if p.Excerpt != "" {
		return template.HTML(wp.Autop(wp.Texturize(p.Excerpt)))
	}
	more := ` &hellip; <p class="link-more"><a href="` + html.EscapeString(permalink) + `" class="more-link">Continue reading<span class="screen-reader-text"> "` + theTitle(p) + `"</span></a></p>`
	text, _, _, _ := wp.SplitMore(stripBlocks(p.Content))
	return template.HTML(wp.Autop(wp.TrimWords(text, 55, more)))
}

// tt17Menu is wp_nav_menu() for the top and social locations with Twenty Seventeen's icon filters.
func (s *Server) tt17Menu(ctx context.Context, location string, items []*store.MenuItem) template.HTML {
	slug := location
	_ = s.store.Pool.QueryRow(ctx, `SELECT slug FROM menus WHERE location = $1 ORDER BY id LIMIT 1`, location).Scan(&slug)
	var sb strings.Builder
	var walk func(list []*store.MenuItem, depth int)
	walk = func(list []*store.MenuItem, depth int) {
		for _, it := range list {
			hasKids := len(it.Children) > 0 && location == "top"
			classes := "menu-item menu-item-type-" + it.Kind + " menu-item-object-" + it.Object
			if hasKids {
				classes += " menu-item-has-children"
			}
			classes += " menu-item-" + itoa64(it.ID)
			if it.Classes != "" {
				classes = it.Classes + " " + classes
			}
			fmt.Fprintf(&sb, `<li id="menu-item-%d" class="%s"><a href="%s"`, it.ID, html.EscapeString(classes), html.EscapeString(it.URL))
			if it.Target != "" {
				fmt.Fprintf(&sb, ` target="%s"`, html.EscapeString(it.Target))
			}
			sb.WriteString(">")
			label := html.EscapeString(it.Label)
			if location == "social" {
				icon := "chain"
				for _, m := range tt17SocialIcons {
					if strings.Contains(it.URL, m[0]) || strings.Contains(label, m[0]) {
						icon = m[1]
					}
				}
				sb.WriteString(`<span class="screen-reader-text">` + label + `</span>` + tt17SVG(icon, false))
			} else {
				sb.WriteString(label)
				if hasKids {
					sb.WriteString(tt17SVG("angle-down", false))
				}
			}
			sb.WriteString("</a>")
			if hasKids {
				sb.WriteString("\n<ul class=\"sub-menu\">\n")
				walk(it.Children, depth+1)
				sb.WriteString("</ul>\n")
			}
			sb.WriteString("</li>\n")
		}
	}
	walk(items, 0)
	id, class := "top-menu", "menu"
	if location == "social" {
		id, class = "menu-"+slug, "social-links-menu"
	}
	return template.HTML(fmt.Sprintf(`<div class="menu-%s-container"><ul id="%s" class="%s">%s</ul></div>`, html.EscapeString(slug), html.EscapeString(id), class, sb.String()))
}

// tt17SocialIcons is twentyseventeen_social_links_icons(), in its order (the last match wins).
var tt17SocialIcons = [][2]string{
	{"behance.net", "behance"}, {"codepen.io", "codepen"}, {"deviantart.com", "deviantart"}, {"digg.com", "digg"},
	{"docker.com", "dockerhub"}, {"dribbble.com", "dribbble"}, {"dropbox.com", "dropbox"}, {"facebook.com", "facebook"},
	{"flickr.com", "flickr"}, {"foursquare.com", "foursquare"}, {"plus.google.com", "google-plus"}, {"github.com", "github"},
	{"instagram.com", "instagram"}, {"linkedin.com", "linkedin"}, {"mailto:", "envelope-o"}, {"medium.com", "medium"},
	{"pinterest.com", "pinterest-p"}, {"pscp.tv", "periscope"}, {"getpocket.com", "get-pocket"}, {"reddit.com", "reddit-alien"},
	{"skype.com", "skype"}, {"skype:", "skype"}, {"slideshare.net", "slideshare"}, {"snapchat.com", "snapchat-ghost"},
	{"soundcloud.com", "soundcloud"}, {"spotify.com", "spotify"}, {"stumbleupon.com", "stumbleupon"}, {"t.me", "telegram"},
	{"telegram.me", "telegram"}, {"tumblr.com", "tumblr"}, {"twitch.tv", "twitch"}, {"twitter.com", "twitter"},
	{"vimeo.com", "vimeo"}, {"vine.co", "vine"}, {"vk.com", "vk"}, {"wa.me", "whatsapp"}, {"whatsapp.com", "whatsapp"},
	{"wordpress.org", "wordpress"}, {"wordpress.com", "wordpress"}, {"yelp.com", "yelp"}, {"youtube.com", "youtube"},
}

// tt17PostNav is the_post_navigation() with single.php's arguments.
func (s *Server) tt17PostNav(ctx context.Context, p *store.Post, lb *links.Builder) template.HTML {
	prev, _ := s.gooAdjacent(ctx, p, true) // Goocommerce Memberships: hidden posts are skipped
	next, _ := s.gooAdjacent(ctx, p, false)
	if prev == nil && next == nil {
		return ""
	}
	var sb strings.Builder
	if prev != nil {
		fmt.Fprintf(&sb, `<div class="nav-previous"><a href="%s" rel="prev"><span class="screen-reader-text">Previous Post</span><span aria-hidden="true" class="nav-subtitle">Previous</span> <span class="nav-title"><span class="nav-title-icon-wrapper">%s</span>%s</span></a></div>`,
			html.EscapeString(lb.Permalink(prev)), tt17SVG("arrow-left", false), theTitle(prev))
	}
	if next != nil {
		fmt.Fprintf(&sb, `<div class="nav-next"><a href="%s" rel="next"><span class="screen-reader-text">Next Post</span><span aria-hidden="true" class="nav-subtitle">Next</span> <span class="nav-title">%s<span class="nav-title-icon-wrapper">%s</span></span></a></div>`,
			html.EscapeString(lb.Permalink(next)), theTitle(next), tt17SVG("arrow-right", false))
	}
	return template.HTML(`
	<nav class="navigation post-navigation" aria-label="Posts">
		<h2 class="screen-reader-text">Post navigation</h2>
		<div class="nav-links">` + sb.String() + `</div>
	</nav>`)
}

var (
	tt17AvatarRe = regexp.MustCompile(`class='avatar avatar-42 photo' height='42' width='42'`)
	tt17ReplyRe  = regexp.MustCompile(`(class="comment-reply-link"[^>]*>)Reply</a>`)
)

// tt17Comments adapts comments.php: "One Reply to" titles, 100px avatars, the mail-reply icon and
// comment_form()'s default h3 reply title.
func tt17Comments(cv *commentsView, title template.HTML) *commentsView {
	if cv == nil {
		return nil
	}
	out := *cv
	if out.Count == 1 {
		out.Title = "One Reply to &ldquo;" + title + "&rdquo;"
	} else if out.Count > 1 {
		out.Title = template.HTML(formatNumber(out.Count)) + " Replies to &ldquo;" + title + "&rdquo;"
	}
	list := tt17AvatarRe.ReplaceAllString(string(out.List), `class='avatar avatar-100 photo' height='100' width='100'`)
	out.List = template.HTML(tt17ReplyRe.ReplaceAllString(list, `${1}`+tt17SVG("mail-reply", false)+`Reply</a>`))
	form := strings.Replace(string(out.Form), `<h2 id="reply-title" class="comment-reply-title">`, `<h3 id="reply-title" class="comment-reply-title">`, 1)
	out.Form = template.HTML(strings.Replace(form, `</small></h2>`, `</small></h3>`, 1))
	return &out
}

// tt17Pagination swaps the_posts_pagination()'s prev/next text for Twenty Seventeen's icons.
func tt17Pagination(p template.HTML) template.HTML {
	return template.HTML(strings.NewReplacer(
		`">Previous page</a>`, `">`+tt17SVG("arrow-left", false)+`<span class="screen-reader-text">Previous page</span></a>`,
		`">Next page</a>`, `"><span class="screen-reader-text">Next page</span>`+tt17SVG("arrow-right", false)+`</a>`,
	).Replace(string(p)))
}

func tt17Prepare(s *Server, c *gin.Context, v *siteView) {
	ctx := v.ctx
	lb := s.links(ctx)
	q := v.Q
	isFront := q.Kind == "front-page" || (q.Kind == "home" && q.PostsPage == nil)
	v.Data["IsFrontPage"] = isFront
	v.Data["StaticFront"] = q.Kind == "front-page"
	v.Data["HeaderImage"] = template.HTML(`<img src="` + v.ThemeURI + `/assets/images/header.jpg" width="2000" height="1200" alt="" decoding="async" fetchpriority="high" />`)
	if items := v.Menus["top"]; len(items) > 0 {
		v.Data["TopMenu"] = s.tt17Menu(ctx, "top", items)
	}
	if items := v.Menus["social"]; len(items) > 0 {
		v.Data["SocialMenu"] = s.tt17Menu(ctx, "social", items)
	}

	wrap := func(pv *postView, singular bool) *tt17Post {
		tp := &tt17Post{P: pv, Class: pv.Class, EditLink: template.HTML(tt17EditLink(pv)), H3: isFront && q.Kind == "home"}
		if pv.Type == "post" {
			if singular {
				by := `<span class="author vcard"><a class="url fn n" href="` + html.EscapeString(pv.AuthorURL) + `">` + html.EscapeString(pv.AuthorName) + `</a></span>`
				tp.Meta = template.HTML(`<span class="posted-on">` + tt17TimeLink(pv) + `</span><span class="byline"> by ` + by + `</span>`)
				tp.Footer = template.HTML(s.tt17EntryFooter(ctx, pv))
			} else {
				tp.Meta = template.HTML(tt17TimeLink(pv) + tt17EditLink(pv))
			}
		}
		if !singular {
			tp.Thumb = s.tt17Thumbnail(ctx, pv.Post, tt17ListSizes)
			if q.Kind == "search" {
				tp.Excerpt = tt17Excerpt(pv.Post, pv.Permalink)
			}
		}
		return tp
	}

	if v.Post != nil {
		tp := wrap(v.Post, true)
		if q.Kind == "front-page" {
			tp.Class = "twentyseventeen-panel " + tp.Class
			if pv := v.Post; pv.Post != nil && pv.Post.FeaturedMediaID > 0 {
				if a, err := s.store.GetAttachment(ctx, pv.Post.FeaturedMediaID); err == nil {
					src, w, h := s.uploadURL(ctx, a.FilePath), a.Width, a.Height
					if vr := a.Variant("twentyseventeen-featured-image"); vr != nil {
						src, w, h = s.uploadURL(ctx, vr.FilePath), vr.Width, vr.Height
					}
					if w > 0 {
						v.Data["PanelImage"] = template.HTML(fmt.Sprintf(`<div class="panel-image" style="background-image: url(%s);">
				<div class="panel-image-prop" style="padding-top: %s%%"></div>
			</div><!-- .panel-image -->`, html.EscapeString(src), tt17Float(float64(h)/float64(w)*100)))
					}
				}
			}
		} else {
			// twentyseventeen_should_show_featured_image()
			v.Data["FeaturedImage"] = s.tt17Thumbnail(ctx, v.Post.Post, "100vw")
		}
		v.Data["Post"] = tp
		if q.Post != nil && q.Post.Type == "post" {
			v.Data["PostNav"] = s.tt17PostNav(ctx, q.Post, lb)
		}
		v.Data["Comments"] = tt17Comments(v.Comments, v.Post.Title)
	}
	posts := make([]*tt17Post, 0, len(v.Posts))
	for _, pv := range v.Posts {
		posts = append(posts, wrap(pv, false))
	}
	v.Data["Posts"] = posts
	v.Data["Pagination"] = tt17Pagination(v.Pagination)

	// template-parts/footer/footer-widgets.php
	if v.Areas["sidebar-2"]+v.Areas["sidebar-3"] != "" {
		var sb strings.Builder
		sb.WriteString("<aside class=\"widget-area\" aria-label=\"Footer\">\n")
		if v.Areas["sidebar-2"] != "" {
			sb.WriteString("\t\t\t<div class=\"widget-column footer-widget-1\">\n\t\t\t\t" + string(v.Areas["sidebar-2"]) + "\t\t\t</div>\n")
		}
		if v.Areas["sidebar-3"] != "" {
			sb.WriteString("\t\t\t<div class=\"widget-column footer-widget-2\">\n\t\t\t\t" + string(v.Areas["sidebar-3"]) + "\t\t\t</div>\n")
		}
		sb.WriteString("\t</aside><!-- .widget-area -->\n")
		v.Data["FooterWidgets"] = template.HTML(sb.String())
	}
}

// tt17Float prints a float the way PHP's string conversion does (up to 14 significant digits, no trailing zeros).
func tt17Float(f float64) string {
	out := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.12f", f), "0"), ".")
	if out == "" {
		return "0"
	}
	return out
}
