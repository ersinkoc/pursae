package builder

// Golementor Pro theme elements: author-box, post-navigation, post-comments, breadcrumbs,
// theme-page-title and theme-post-excerpt (ports of upstream Pro 4.3.0
// modules/theme-elements/widgets/*.php and modules/theme-builder/widgets/{page-title,post-excerpt}.php).
// They read the current post and its author from RenderContext.Theme / RenderContext.Pro.

import (
	"regexp"
	"strconv"
	"strings"
)

func init() {
	registerWidget("author-box", &widgetDef{render: proFRenderAuthorBox, styles: static("widget-author-box")})
	registerWidget("post-navigation", &widgetDef{render: proFRenderPostNavigation, styles: static("widget-post-navigation"),
		scripts: []string{"post-navigation"}})
	registerWidget("post-comments", &widgetDef{render: proFRenderPostComments})
	registerWidget("theme-page-title", &widgetDef{render: proFRenderPageTitle, styles: static("widget-heading"),
		wrapperClass: "elementor-widget-theme-page-title elementor-page-title elementor-widget-heading"})
	registerWidget("theme-post-excerpt", &widgetDef{render: proFRenderPostExcerpt})
	proFRegisterBreadcrumbs()
}

// proFCurrentPost is get_the_ID()'s post: the post being shown (the document itself on a page).
func proFCurrentPost(w *elemCtx) *PostData {
	if p := w.currentPostAny(); p != nil {
		return p
	}
	return nil
}

var proFAvatarSizeRe = regexp.MustCompile(`([?&](?:amp;)?s=)\d+`)

// proFAvatarSized asks the avatar URL for a size (get_avatar_url( $id, ['size' => n] )).
func proFAvatarSized(u string, size int) string {
	if u == "" || size <= 0 {
		return u
	}
	if proFAvatarSizeRe.MatchString(u) {
		return proFAvatarSizeRe.ReplaceAllString(u, "${1}"+strconv.Itoa(size))
	}
	return u
}

// --- author box --------------------------------------------------------------------------------------

func proFRenderAuthorBox(w *elemCtx) string {
	custom := w.s("source") == "custom"
	var avatar, displayName, website, bio, postsURL string
	if w.s("source") == "current" {
		if p := proFCurrentPost(w); p != nil {
			var u *UserInfo
			if pd := w.r.pro(); pd.AuthorOf != nil {
				u = pd.AuthorOf(p.ID)
			}
			if u != nil {
				avatar = proFAvatarSized(u.AvatarURL, toInt(w.get("avatar_size")))
				displayName = u.DisplayName
				website = u.URL
				bio = u.Description
				postsURL = u.PostsURL
			} else {
				displayName = p.AuthorName
				postsURL = p.AuthorURL
			}
		}
	} else if custom {
		if img := asMap(w.get("author_avatar")); img != nil && truthy(img["url"]) {
			avatar = phpString(img["url"])
			if id := toInt64(img["id"]); id > 0 {
				if a := w.r.attachment(id); a != nil {
					if src, _, _, ok := a.imageSrc("medium"); ok && src != "" {
						avatar = src
					}
				}
			}
		}
		displayName = w.s("author_name")
		website = phpString(sub(w.get("author_website"), "url"))
		bio = wpautop(w.s("author_bio"), true)
		postsURL = phpString(sub(w.get("posts_url"), "url"))
	}
	printAvatar := (!custom && w.s("show_avatar") == "yes") || (custom && avatar != "")
	printName := (!custom && w.s("show_name") == "yes") || (custom && displayName != "")
	printBio := (!custom && w.s("show_biography") == "yes") || (custom && bio != "")
	printLink := (!custom && w.s("show_link") == "yes" && w.has("link_text")) || (custom && postsURL != "" && w.has("link_text"))

	linkTag, linkURL, linkTarget := "div", "", ""
	if w.has("link_to") || custom {
		if (custom || w.s("link_to") == "website") && website != "" {
			linkTag, linkURL = "a", website
			if custom {
				if truthy(sub(w.get("author_website"), "is_external")) {
					linkTarget = "_blank"
				}
			} else {
				linkTarget = "_blank"
			}
		} else if w.s("link_to") == "posts_archive" && postsURL != "" {
			linkTag, linkURL = "a", postsURL
		}
		if linkURL != "" {
			w.attr("author_link").add("href", escURL(linkURL))
			if linkTarget != "" {
				w.attr("author_link").add("target", linkTarget)
			}
		}
	}
	btn := w.attr("button")
	btn.add("class", "elementor-author-box__button", "elementor-button", "elementor-size-xs")
	if printLink {
		btn.add("href", escURL(postsURL))
		if anim := w.s("button_hover_animation"); anim != "" {
			btn.add("class", "elementor-animation-"+anim)
		}
	}
	if printAvatar {
		alt := "Author picture"
		if displayName != "" {
			alt = "Picture of " + displayName
		}
		w.attr("avatar").add("src", escURL(avatar)).add("alt", alt).add("loading", "lazy")
	}
	nameTag := validateHTMLTag(w.s("author_name_tag"))
	link := w.attrString("author_link")
	var b strings.Builder
	b.WriteString("\t\t<div class=\"elementor-author-box\">\n")
	if printAvatar {
		b.WriteString("\t\t\t<" + linkTag + " " + link + " class=\"elementor-author-box__avatar\">\n")
		b.WriteString("\t\t\t\t<img " + w.attrString("avatar") + ">\n")
		b.WriteString("\t\t\t</" + linkTag + ">\n")
	}
	b.WriteString("\t\t\t<div class=\"elementor-author-box__text\">\n")
	if printName {
		b.WriteString("\t\t\t\t<" + linkTag + " " + link + ">\n")
		b.WriteString("\t\t\t\t\t<" + nameTag + " class=\"elementor-author-box__name\">\n\t\t\t\t\t\t" + displayName + "\t\t\t\t\t</" + nameTag + ">\n")
		b.WriteString("\t\t\t\t</" + linkTag + ">\n")
	}
	if printBio {
		b.WriteString("\t\t\t\t<div class=\"elementor-author-box__bio\">\n\t\t\t\t\t" + bio + "\t\t\t\t</div>\n")
	}
	if printLink {
		b.WriteString("\t\t\t\t<a " + w.attrString("button") + ">\n\t\t\t\t\t" + ksesPost(w.s("link_text")) + "\t\t\t\t</a>\n")
	}
	b.WriteString("\t\t\t</div>\n\t\t</div>\n")
	return b.String()
}

// --- post navigation ---------------------------------------------------------------------------------

func proFRenderPostNavigation(w *elemCtx) string {
	var prevLabel, nextLabel, prevArrow, nextArrow, prevTitle, nextTitle string
	if w.s("show_label") == "yes" {
		prevLabel = `<span class="post-navigation__prev--label">` + w.s("prev_label") + `</span>`
		nextLabel = `<span class="post-navigation__next--label">` + w.s("next_label") + `</span>`
	}
	if w.s("show_arrow") == "yes" {
		hidden := func() *attrs { return newAttrs().add("aria-hidden", "true") }
		prevArrow = `<span class="post-navigation__arrow-wrapper post-navigation__arrow-prev">` + renderIcon(w.get("arrow_previous_icon"), hidden(), "i") +
			`<span class="elementor-screen-only">Prev</span></span>`
		nextArrow = `<span class="post-navigation__arrow-wrapper post-navigation__arrow-next">` + renderIcon(w.get("arrow_next_icon"), hidden(), "i") +
			`<span class="elementor-screen-only">Next</span></span>`
	}
	if w.s("show_title") == "yes" {
		prevTitle = `<span class="post-navigation__prev--title">%title</span>`
		nextTitle = `<span class="post-navigation__next--title">%title</span>`
	}
	sameTerm, taxonomy := "", "category"
	cur := proFCurrentPost(w)
	if cur != nil {
		for _, t := range asList(w.get("in_same_term")) {
			if phpString(t) == cur.Type {
				if tx, ok := w.disp.m[cur.Type+"_taxonomy"]; ok {
					sameTerm, taxonomy = "yes", phpString(tx)
				}
			}
		}
	}
	link := func(prev bool, format string) string {
		if cur == nil {
			return ""
		}
		pd := w.r.pro()
		var adj *PostData
		rel := "prev"
		if prev {
			if pd.Previous != nil {
				adj = pd.Previous(cur.ID, sameTerm, taxonomy)
			}
		} else {
			rel = "next"
			if pd.Next != nil {
				adj = pd.Next(cur.ID, sameTerm, taxonomy)
			}
		}
		if adj == nil {
			return ""
		}
		return `<a href="` + adj.URL + `" rel="` + rel + `">` + strings.ReplaceAll(format, "%title", adj.Title) + `</a>`
	}
	var b strings.Builder
	b.WriteString("\t\t<div class=\"elementor-post-navigation\" role=\"navigation\" aria-label=\"Post Navigation\">\n")
	b.WriteString("\t\t\t<div class=\"elementor-post-navigation__prev elementor-post-navigation__link\">\n\t\t\t\t")
	b.WriteString(link(true, prevArrow+`<span class="elementor-post-navigation__link__prev">`+prevLabel+prevTitle+`</span>`))
	b.WriteString("\t\t\t</div>\n")
	if w.s("show_borders") == "yes" {
		b.WriteString("\t\t\t\t<div class=\"elementor-post-navigation__separator-wrapper\">\n\t\t\t\t\t<div class=\"elementor-post-navigation__separator\"></div>\n\t\t\t\t</div>\n")
	}
	b.WriteString("\t\t\t<div class=\"elementor-post-navigation__next elementor-post-navigation__link\">\n\t\t\t\t")
	b.WriteString(link(false, `<span class="elementor-post-navigation__link__next">`+nextLabel+nextTitle+`</span>`+nextArrow))
	b.WriteString("\t\t\t</div>\n\t\t</div>\n")
	return b.String()
}

// --- post comments -----------------------------------------------------------------------------------

// proFRenderPostComments prints the theme's comment list and form (comments_template()); the server
// fills ProData.CommentsHTML from the active theme's comments view.
func proFRenderPostComments(w *elemCtx) string {
	var id int64
	if w.s("source_type") == "custom" {
		id = toInt64(w.get("source_custom"))
	} else if p := proFCurrentPost(w); p != nil {
		id = p.ID
	} else if w.r.doc != nil {
		id = w.r.doc.ID // get_the_ID(): the document itself (editor render of a page or post)
	}
	pd := w.r.pro()
	html := ""
	if id > 0 && pd.CommentsHTML != nil {
		html = pd.CommentsHTML(id)
	}
	if strings.TrimSpace(html) == "" && w.editing() {
		return "\t\t\t<div class=\"elementor-alert elementor-alert-danger\" role=\"alert\">\n\t\t\t\t<span class=\"elementor-alert-title\">\n\t\t\t\t\tComments are closed.\t\t\t\t</span>\n" +
			"\t\t\t\t<span class=\"elementor-alert-description\">\n\t\t\t\t\tSwitch on comments from either the discussion box on the post edit screen or from the discussion settings.\t\t\t\t</span>\n\t\t\t</div>\n"
	}
	return html
}

// --- page title / post excerpt -------------------------------------------------------------------------

// proFRenderPageTitle mirrors Title_Widget_Base::render: the heading widget, hidden when the current
// document hides its title.
func proFRenderPageTitle(w *elemCtx) string {
	if doc := w.r.doc; doc != nil && phpString(doc.Settings["hide_title"]) == "yes" {
		if cur := proFCurrentPost(w); cur == nil || cur.ID == doc.ID {
			return ""
		}
	}
	return renderHeading(w)
}

func proFRenderPostExcerpt(w *elemCtx) string {
	out := w.s("excerpt")
	if out == "" {
		return ""
	}
	return "\t\t\t\t\t" + out + "\t\t\t\t"
}

// --- breadcrumbs -------------------------------------------------------------------------------------

// Breadcrumbs: upstream prints Yoast SEO's trail and registers the widget only with Yoast active, so
// the lab has no control dump. Golementor builds the trail natively (ProData.Breadcrumbs: Home ›
// ancestors/category › current, archives, search, 404) and prints it in Yoast's markup
// (<p id="breadcrumbs"><span><span><a>Home</a></span> » …), so upstream's widget CSS and the
// controls' selectors apply, with schema.org BreadcrumbList microdata. The controls are upstream's
// plus the separator, home label and current-item options Yoast keeps in its own settings.
func proFRegisterBreadcrumbs() {
	l := &ctlList{}
	l.start("section_breadcrumbs_content", "Breadcrumbs", "content")
	l.add("align", "choose", "Alignment", "options", Ordered{
		{"left", map[string]any{"title": "Left", "icon": "eicon-text-align-left"}},
		{"center", map[string]any{"title": "Center", "icon": "eicon-text-align-center"}},
		{"right", map[string]any{"title": "Right", "icon": "eicon-text-align-right"}}},
		"prefix_class", "elementor%s-align-", "responsive", []any{}, "is_responsive", true)
	l.add("html_tag", "select", "HTML Tag", "options", Ordered{{"", "Default"}, {"p", "p"}, {"div", "div"}, {"nav", "nav"}, {"span", "span"}}, "default", "")
	l.add("separator", "text", "Separator", "default", "»", "ai", map[string]any{"active": false})
	l.add("show_home", "switcher", "Home Link", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes")
	l.add("home_text", "text", "Home Text", "default", "Home", "condition", map[string]any{"show_home": "yes"}, "ai", map[string]any{"active": false})
	l.add("show_current", "switcher", "Current Item", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes")
	l.start("section_style", "Breadcrumbs", "style")
	l.typography("typography", "{{WRAPPER}}", "secondary")
	l.color("text_color", "Text Color", "{{WRAPPER}}", "color", "")
	l.color("link_color", "Link Color", "{{WRAPPER}} a", "color", "")
	l.color("link_hover_color", "Link Hover Color", "{{WRAPPER}} a:hover", "color", "")
	l.color("separator_color", "Separator Color", "{{WRAPPER}} .breadcrumb-separator", "color", "")
	l.color("current_color", "Current Item Color", "{{WRAPPER}} .breadcrumb_last", "color", "")
	l.slider("separator_spacing", "Separator Spacing", "{{WRAPPER}} .breadcrumb-separator", "margin: 0 {{SIZE}}{{UNIT}};",
		[]string{"px", "em", "rem", "custom"}, map[string]any{"px": map[string]any{"min": 0, "max": 50}}, nil)
	proFBreadcrumbs = &ThemeWidget{Name: "breadcrumbs", Title: "Breadcrumbs", Icon: "eicon-product-breadcrumbs",
		Keywords: []string{"seo", "breadcrumbs", "internal links", "navigation"}, Categories: []string{"theme-elements"},
		WrapperClass: "elementor-widget-breadcrumbs", Controls: l.out}
	ctrls := make([]map[string]any, 0, len(l.out))
	for _, c := range l.out {
		ctrls = append(ctrls, schemaControl(c))
	}
	loadSchema().widgets["breadcrumbs"] = &widgetSchema{Common: "common", InnerWrapper: true, Controls: ctrls}
	registerWidget("breadcrumbs", &widgetDef{render: proFRenderBreadcrumbs, styles: static("widget-breadcrumbs")})
}

var proFBreadcrumbs *ThemeWidget

// ProFExtraWidgets lists Pro widgets that are not in the captured Pro widgets config (upstream
// registers Breadcrumbs only with Yoast SEO active); the server adds them to the editor's widgets
// config and the document's widget list like the Site widgets (ThemeWidgets).
func ProFExtraWidgets() []*ThemeWidget { return []*ThemeWidget{proFBreadcrumbs} }

// proFBreadcrumbsSample is the trail the editor shows where the page has none (templates).
var proFBreadcrumbsSample = []TermLink{{Name: "Home", URL: "#"}, {Name: "Category", URL: "#"}, {Name: "Current Page"}}

func proFRenderBreadcrumbs(w *elemCtx) string {
	pd := w.r.pro()
	var trail []TermLink
	if pd.Breadcrumbs != nil {
		trail = pd.Breadcrumbs()
	}
	if w.editing() && len(trail) < 2 {
		trail = proFBreadcrumbsSample
	}
	if len(trail) == 0 {
		return ""
	}
	trail = append([]TermLink(nil), trail...)
	if w.s("show_home") != "" {
		if t := strings.TrimSpace(w.s("home_text")); t != "" {
			trail[0].Name = escHTML(t)
		}
	} else {
		trail = trail[1:]
	}
	if len(trail) > 1 && trail[len(trail)-1].URL == "" && w.s("show_current") == "" {
		trail = trail[:len(trail)-1]
	}
	if len(trail) == 0 {
		return ""
	}
	tag := w.s("html_tag")
	if tag == "" {
		tag = "p"
	}
	tag = validateHTMLTag(tag)
	sep := " " + escHTML(strings.TrimSpace(w.s("separator"))) + " "
	sepHTML := `<span class="breadcrumb-separator" aria-hidden="true">` + sep + `</span>`
	var b strings.Builder
	b.WriteString("<" + tag + ` id="breadcrumbs"`)
	if tag == "nav" {
		b.WriteString(` aria-label="Breadcrumb"`)
	}
	b.WriteString(`><span itemscope itemtype="https://schema.org/BreadcrumbList">`)
	for i, t := range trail {
		if i > 0 {
			b.WriteString(sepHTML)
		}
		pos := `<meta itemprop="position" content="` + strconv.Itoa(i+1) + `" />`
		last := i == len(trail)-1
		if last && t.URL == "" {
			b.WriteString(`<span class="breadcrumb_last" aria-current="page" itemprop="itemListElement" itemscope itemtype="https://schema.org/ListItem">` +
				`<span itemprop="name">` + t.Name + `</span>` + pos + `</span>`)
			continue
		}
		if t.URL == "" {
			b.WriteString(`<span itemprop="itemListElement" itemscope itemtype="https://schema.org/ListItem"><span itemprop="name">` + t.Name + `</span>` + pos + `</span>`)
			continue
		}
		b.WriteString(`<span itemprop="itemListElement" itemscope itemtype="https://schema.org/ListItem"><a itemprop="item" href="` + escURL(t.URL) + `">` +
			`<span itemprop="name">` + t.Name + `</span></a>` + pos + `</span>`)
	}
	b.WriteString("</span></" + tag + ">")
	return b.String()
}

// --- page title dynamic tag ------------------------------------------------------------------------

// proFSingularTitle is Utils::get_page_title on a singular page: the title, prefixed with the post
// type's singular name ("Post: Hello world!") when the tag includes the context.
func proFSingularTitle(r *renderer, p *PostData, includeContext bool) string {
	if !includeContext {
		return p.Title
	}
	return proFPostTypeSingular(r, p.Type) + ": " + p.Title
}

// proFPostTypeSingular is get_post_type_object( $type )->labels->singular_name.
func proFPostTypeSingular(r *renderer, typ string) string {
	switch typ {
	case "post", "":
		return "Post"
	case "page":
		return "Page"
	case "attachment":
		return "Media"
	case "elementor_library", "gwe_library":
		return "Template"
	case "e-landing-page":
		return "Landing Page"
	}
	if pd := r.pro(); pd.PostTypeLabel != nil {
		label := pd.PostTypeLabel(typ)
		if strings.HasSuffix(label, "s") && len(label) > 1 {
			label = label[:len(label)-1]
		}
		return escHTML(label)
	}
	return escHTML(strings.ToUpper(typ[:1]) + typ[1:])
}

// proFEditorPageTitle is the Page Title tag in the editor (render_tags) for a page or post document
// without theme post data: upstream queries the edited post, so the title is the document's own.
func proFEditorPageTitle(r *renderer, includeContext bool) string {
	pd := r.pro()
	if r.doc == nil || !pd.IsEditor || pd.PostByID == nil {
		return ""
	}
	switch r.doc.Type {
	case "wp-page", "wp-post", "page", "post", "":
	default:
		return ""
	}
	p := pd.PostByID(r.doc.ID)
	if p == nil {
		return ""
	}
	return proFSingularTitle(r, p, includeContext)
}
