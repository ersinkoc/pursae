package builder

// GoWebEditor Pro query widgets: Posts (classic, cards and full content skins, with pagination and
// load more), Portfolio, Loop Grid and Loop Carousel (each item is a loop-item template rendered
// with the item post as the current post), Taxonomy Filter and Sitemap. Ports of upstream Pro's
// modules/posts, modules/loop-builder, modules/loop-filter and theme-elements/widgets/sitemap.php.
// Posts come from RenderContext.Pro.Query (the server runs Pro's query control against the site).

import (
	"sort"
	"strconv"
	"strings"
)

func init() {
	img := []string{"imagesloaded"}
	registerWidget("posts", &widgetDef{render: proDRenderPosts, styles: static("widget-posts"), scripts: img})
	registerWidget("portfolio", &widgetDef{render: proDRenderPortfolio, styles: static("widget-posts"), scripts: img})
	registerWidget("loop-grid", &widgetDef{render: proDRenderLoopGrid, styles: static("widget-loop-grid"), scripts: img})
	registerWidget("loop-carousel", &widgetDef{render: proDRenderLoopCarousel, styles: static("widget-loop-carousel"), scripts: img})
	registerWidget("taxonomy-filter", &widgetDef{render: proDRenderTaxonomyFilter, styles: static("widget-loop-filter")})
	registerWidget("sitemap", &widgetDef{render: proDRenderSitemap, styles: static("widget-sitemap")})
}

// proDPaginatedWidgets are Posts_Base widgets (and global widgets), counted by
// has_multiple_posts_widgets_on_page.
var proDPaginatedWidgets = map[string]bool{"posts": true, "archive-posts": true, "loop-grid": true, "loop-carousel": true, "global": true}

// --- query ---------------------------------------------------------------------------------------

func proDHas(list any, v string) bool {
	for _, x := range asList(list) {
		if phpString(x) == v {
			return true
		}
	}
	return phpString(list) == v
}

func proDIDs(v any) []int64 {
	var out []int64
	for _, x := range asList(v) {
		if m := asMap(x); m != nil {
			x = m["id"]
		}
		if n := toInt64(x); n > 0 {
			out = append(out, n)
		}
	}
	return out
}

// proDQuery maps Pro's query group control (prefix "posts_" or "post_query_") onto PostQuery
// (upstream Post_Query::get_query_args; loop_query.go).
func (e *elemCtx) proDQuery(prefix string, perPage, page int) *PostQuery {
	return e.loopQuery(prefix, perPage, page)
}

// proDRun runs a query; the loop filter's selections narrow it (loop_query.go).
func (e *elemCtx) proDRun(q *PostQuery) *PostList {
	return e.loopRun(q, true)
}

// --- pagination ----------------------------------------------------------------------------------

// proDCurrentPage mirrors Posts_Base::get_current_page.
func (e *elemCtx) proDCurrentPage() int {
	if e.s("pagination_type") == "" {
		return 1
	}
	page := 1
	pd := e.r.pro()
	if pd.Request != nil {
		for _, k := range []string{"e-page-" + e.el.ID, "paged", "page"} {
			if n, _ := strconv.Atoi(pd.Request("get", k)); n > page {
				page = n
			}
		}
	}
	if a := e.archiveAny(); a != nil && a.Page > page {
		page = a.Page
	}
	return page
}

// proDCustomPageOption mirrors is_allow_to_use_custom_page_option.
func (e *elemCtx) proDCustomPageOption() bool {
	if e.s("pagination_load_type") == "ajax" || e.s("pagination_individual_handle") == "yes" {
		return true
	}
	if e.r.doc == nil {
		return false
	}
	n := 0
	var walk func(list []*Element)
	walk = func(list []*Element) {
		for _, el := range list {
			if el == nil {
				continue
			}
			if el.ElType == "widget" && proDPaginatedWidgets[el.WidgetType] {
				n++
			}
			walk(el.Elements)
		}
	}
	walk(e.r.doc.Elements)
	return n >= 2
}

// proDBaseURL mirrors Pagination_Trait::get_base_url.
func (e *elemCtx) proDBaseURL() string {
	if p := e.currentPostAny(); p != nil && p.URL != "" {
		return p.URL
	}
	if a := e.archiveAny(); a != nil && a.PageURL != nil {
		return a.PageURL(1)
	}
	return e.homeURL()
}

func proDTrailing(u string) string { return strings.TrimRight(u, "/") + "/" }

func proDAddQuery(u, kv string) string {
	if strings.Contains(u, "?") {
		return u + "&" + kv
	}
	return u + "?" + kv
}

// proDLinkPage mirrors Posts_Base::get_wp_link_page (pretty permalinks).
func (e *elemCtx) proDLinkPage(i int, custom bool) string {
	singular := e.currentPostAny() != nil
	st := e.r.loopState()
	if !singular && !custom && !st.REST {
		if a := e.archiveAny(); a != nil && a.PageURL != nil {
			return escURL(a.PageURL(i))
		}
		if i <= 1 {
			return escURL(e.homeURL())
		}
		return escURL(proDTrailing(e.homeURL()) + "page/" + strconv.Itoa(i) + "/")
	}
	u := e.proDBaseURL()
	if st.REST && !singular && st.BaseURL != "" {
		u = st.BaseURL
		if !custom {
			u = proDTrailing(u) + "page/"
		}
	}
	if i > 1 {
		if custom {
			key := "e-page-" + e.el.ID
			extra := ""
			if e.s("pagination_individual_handle") == "yes" {
				extra = loopArgs(loopParseQuery(st.RawQuery), false, func(k string) bool { return strings.Contains(k, "e-page") && k != key })
			}
			u = loopConcat(e.proDBaseURL() + extra + "&" + key + "=" + strconv.Itoa(i))
		} else {
			u = proDTrailing(u) + strconv.Itoa(i) + "/"
		}
	}
	if st.REST {
		if args := loopArgs(loopParseQuery(st.RefererQuery), true, nil); args != "" {
			u = loopConcat(u + args)
		}
	} else if extra := loopArgs(loopParseQuery(st.RawQuery), false, func(k string) bool { return strings.Contains(k, "e-filter") }); extra != "" {
		u = loopConcat(u + extra)
	}
	return escURL(u)
}

// proDNumberLink mirrors the paginate_links() URLs of Skin_Base::render_loop_footer: the
// singular post's permalink (or the archive's page), the custom e-page-<id> format when several
// paginated widgets share the page, and the request's other query arguments.
func (e *elemCtx) proDNumberLink(n int, custom bool) string {
	st := e.r.loopState()
	singular := e.currentPostAny() != nil
	var base, format string
	var add []loopKV
	switch {
	case st.REST:
		base, format = proDTrailing(e.proDBaseURL())+"%_%", "%#%/"
		for _, kv := range loopParseQuery(st.RefererQuery) {
			add = append(add, loopKV{phpURLEncode(kv.K), phpURLEncode(kv.V)})
		}
	case singular:
		base, format = proDTrailing(e.proDBaseURL())+"%_%", "%#%/"
	default:
		if !custom {
			if a := e.archiveAny(); a != nil && a.PageURL != nil {
				return escURL(a.PageURL(n))
			}
		}
		base, format = proDTrailing(e.homeURL())+"%_%", "page/%#%/"
		if a := e.archiveAny(); a != nil && a.PageURL != nil {
			base = proDTrailing(a.PageURL(1)) + "%_%"
		}
	}
	if custom {
		sep := "?"
		if strings.Contains(base, "?") {
			sep = "&"
		}
		format = sep + "e-page-" + e.el.ID + "=%#%"
	}
	if !st.REST {
		// paginate_links merges the requested URL's arguments, minus the format's own.
		_, fq, _ := strings.Cut(strings.ReplaceAll(base, "%_%", format), "?")
		skip := map[string]bool{}
		for _, kv := range loopParseQuery(fq) {
			skip[kv.K] = true
		}
		for _, kv := range loopParseQuery(st.RawQuery) {
			if !skip[kv.K] {
				add = append(add, loopKV{phpURLEncode(kv.K), phpURLEncode(kv.V)})
			}
		}
	}
	f := format
	if n == 1 {
		f = ""
	}
	link := strings.ReplaceAll(strings.ReplaceAll(base, "%_%", f), "%#%", strconv.Itoa(n))
	return escURL(loopAddQueryArgs(link, add))
}

// proDPaginateLinks mirrors paginate_links( type => array, prev_next => false ).
func proDPaginateLinks(current, total int, showAll bool, link func(int) string) []string {
	const endSize, midSize = 1, 2
	var out []string
	dots := false
	before := `<span class="elementor-screen-only">Page</span>`
	for n := 1; n <= total; n++ {
		if n == current {
			out = append(out, `<span aria-current="page" class="page-numbers current">`+before+strconv.Itoa(n)+`</span>`)
			dots = true
			continue
		}
		if showAll || n <= endSize || (current > 0 && n >= current-midSize && n <= current+midSize) || n > total-endSize {
			out = append(out, `<a class="page-numbers" href="`+link(n)+`">`+before+strconv.Itoa(n)+`</a>`)
			dots = true
		} else if dots && !showAll {
			out = append(out, `<span class="page-numbers dots">&hellip;</span>`)
			dots = false
		}
	}
	return out
}

// proDLoopFooter mirrors Skin_Base::render_loop_footer after the container closes: load-more
// anchor, button or numbers/prev-next navigation.
func (e *elemCtx) proDLoopFooter(list *PostList) string {
	var b strings.Builder
	pt := e.s("pagination_type")
	ajax := pt == "load_more_on_click" || pt == "load_more_infinite_scroll"
	if ajax {
		if sp := asMap(e.get("load_more_spinner")); sp != nil && truthy(sp["value"]) {
			b.WriteString(`<span class="e-load-more-spinner">` + renderIcon(sp, newAttrs().add("aria-hidden", "true"), "i") + `</span>`)
		}
	}
	if pt == "" {
		return b.String()
	}
	limit := list.Pages
	if pl := e.s("pagination_page_limit"); pl != "" && !ajax {
		if n := toInt(pl); n < limit {
			limit = n
		}
	}
	if limit < 2 {
		return b.String()
	}
	custom := e.proDCustomPageOption()
	cur := e.proDCurrentPage()
	b.WriteString(`<div class="e-load-more-anchor" data-page="` + strconv.Itoa(cur) + `" data-max-page="` + strconv.Itoa(list.Pages) +
		`" data-next-page="` + e.proDLinkPage(cur+1, custom) + `"></div>` + "\n")
	if ajax {
		if pt == "load_more_on_click" {
			b.WriteString(e.proDLoadMoreButton())
		}
		b.WriteString(`<div class="e-load-more-message">` + escHTML(e.s("load_more_no_posts_custom_message")) + "</div>\n")
		return b.String()
	}
	var links []string
	if pt == "numbers" || pt == "numbers_and_prev_next" {
		links = proDPaginateLinks(cur, limit, e.s("pagination_numbers_shorten") != "yes", func(n int) string { return e.proDNumberLink(n, custom) })
	}
	if pt == "prev_next" || pt == "numbers_and_prev_next" {
		var prev, next string
		if cur > 1 {
			prev = `<a class="page-numbers prev" href="` + e.proDLinkPage(cur-1, custom) + `">` + e.s("pagination_prev_label") + `</a>`
		} else {
			prev = `<span class="page-numbers prev">` + e.s("pagination_prev_label") + `</span>`
		}
		if cur+1 <= limit {
			next = `<a class="page-numbers next" href="` + e.proDLinkPage(cur+1, custom) + `">` + e.s("pagination_next_label") + `</a>`
		} else {
			next = `<span class="page-numbers next">` + e.s("pagination_next_label") + `</span>`
		}
		links = append(append([]string{prev}, links...), next)
	}
	b.WriteString(`<nav class="elementor-pagination" aria-label="Pagination">` + "\n" + strings.Join(links, "\n") + "</nav>\n")
	return b.String()
}

// proDLoadMoreButton mirrors Button_Widget_Trait::render_button for the load more button.
func (e *elemCtx) proDLoadMoreButton() string {
	text := e.s("text")
	icon := asMap(e.get("selected_icon"))
	hasIcon := icon != nil && truthy(icon["value"])
	if text == "" && !hasIcon {
		return ""
	}
	wrap := e.attr("wrapper")
	if e.el.WidgetType == "loop-grid" { // Skin_Loop_Base::maybe_add_load_more_wrapper_class runs first
		wrap.add("class", "e-loop__load-more")
	}
	wrap.add("class", "elementor-button-wrapper")
	btn := e.attr("button")
	btn.add("class", "elementor-button")
	// Upstream sets link "#" with set_settings, but the cached display settings never see it.
	btn.add("role", "button")
	if id := e.s("button_css_id"); id != "" {
		btn.add("id", id)
	}
	if sz := e.s("size"); sz != "" {
		btn.add("class", "elementor-size-"+sz)
	} else {
		btn.add("class", "elementor-size-sm")
	}
	if ha := e.s("hover_animation"); ha != "" {
		btn.add("class", "elementor-animation-"+ha)
	}
	var b strings.Builder
	b.WriteString("<div " + wrap.String() + ">\n<a " + btn.String() + ">\n<span class=\"elementor-button-content-wrapper\">\n")
	if hasIcon {
		b.WriteString(`<span class="elementor-button-icon">` + renderIcon(icon, newAttrs().add("aria-hidden", "true"), "i") + "</span>\n")
	}
	if text != "" {
		b.WriteString(`<span class="elementor-button-text">` + text + "</span>\n")
	}
	b.WriteString("</span>\n</a>\n</div>\n")
	return b.String()
}

// --- posts ---------------------------------------------------------------------------------------

type proDSkin struct {
	e    *elemCtx
	id   string // classic, cards, full_content
	link string // current permalink
}

func (k *proDSkin) v(name string) any    { return k.e.get(k.id + "_" + name) }
func (k *proDSkin) s(name string) string { return phpString(k.v(name)) }

// optionalAttrs mirrors get_optional_link_attributes_html.
func (k *proDSkin) optionalAttrs() string {
	if phpString(k.e.raw(k.id+"_open_new_tab")) == "yes" {
		return `target="_blank"`
	}
	return ""
}

func proDRenderPosts(e *elemCtx) string {
	skin := e.s("_skin")
	if skin == "" {
		skin = "classic"
	}
	k := &proDSkin{e: e, id: skin}
	perPage := toInt(k.v("posts_per_page"))
	list := e.proDRun(e.proDQuery("posts_", perPage, e.proDCurrentPage()))
	if len(list.Posts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(k.loopHeader([]string{"elementor-posts-container", "elementor-posts", "elementor-posts--skin-" + skin}, nil))
	for _, p := range list.Posts {
		k.link = p.URL
		b.WriteString(k.renderPost(p))
	}
	b.WriteString("</div>\n")
	b.WriteString(e.proDLoopFooter(list))
	return b.String()
}

// loopHeader mirrors Skin_Base::render_loop_header (the "container" attributes).
func (k *proDSkin) loopHeader(classes []string, extra func(a *attrs)) string {
	a := k.e.attr("container")
	a.add("class", append(classes, k.e.loopGridClass()...)...) // gap-loops2: no grid for an empty term loop
	a.add("role", "list")
	if extra != nil {
		extra(a)
	}
	return "<div " + a.String() + ">\n"
}

func (k *proDSkin) renderPost(p *PostData) string {
	var b strings.Builder
	cls := "elementor-post elementor-grid-item"
	if p.Class != "" {
		cls += " " + p.Class
	}
	b.WriteString(`<article class="` + escAttr(cls) + `" role="listitem">` + "\n")
	switch k.id {
	case "cards":
		b.WriteString(`<div class="elementor-post__card">` + "\n")
		b.WriteString(k.cardsThumbnail(p))
		b.WriteString(`<div class="elementor-post__text">` + "\n")
		b.WriteString(k.title(p))
		b.WriteString(k.excerpt(p))
		b.WriteString(k.readMore(p))
		b.WriteString("</div>\n")
		b.WriteString(k.metaData(p))
		b.WriteString("</div>\n")
	case "full_content":
		b.WriteString(k.thumbnail(p, true))
		b.WriteString(`<div class="elementor-post__text">` + "\n")
		b.WriteString(k.title(p))
		b.WriteString(k.metaData(p))
		b.WriteString(k.e.proDPostContent(p))
		b.WriteString("</div>\n")
	default:
		b.WriteString(k.thumbnail(p, false))
		b.WriteString(`<div class="elementor-post__text">` + "\n")
		b.WriteString(k.title(p))
		b.WriteString(k.metaData(p))
		b.WriteString(k.excerpt(p))
		b.WriteString(k.readMore(p))
		b.WriteString("</div>\n")
	}
	b.WriteString("</article>\n")
	return b.String()
}

// thumbImage is Group_Control_Image_Size::get_attachment_image_html for the post thumbnail.
func (e *elemCtx) proDThumbImage(sizeKey string, id int64) string {
	if id <= 0 {
		return ""
	}
	m := make(map[string]any, len(e.disp.m)+1)
	for key, v := range e.disp.m {
		m[key] = v
	}
	delete(m, "hover_animation")
	m["__proDThumb"] = map[string]any{"id": id, "url": ""}
	return e.r.attachmentImageHTML(m, sizeKey, "__proDThumb")
}

func (k *proDSkin) thumbnail(p *PostData, content bool) string {
	th := k.s("thumbnail")
	if content {
		if th == "" && !k.e.editing() {
			return ""
		}
	} else if th == "none" && !k.e.editing() {
		return ""
	}
	img := k.e.proDThumbImage(k.id+"_thumbnail_size", p.ImageID)
	if img == "" {
		return ""
	}
	return `<a class="elementor-post__thumbnail__link" href="` + escAttr(k.link) + `" tabindex="-1" ` + escAttr(k.optionalAttrs()) + ">\n" +
		`<div class="elementor-post__thumbnail">` + img + "</div>\n</a>\n"
}

func (k *proDSkin) cardsThumbnail(p *PostData) string {
	if k.s("thumbnail") == "none" {
		return ""
	}
	img := k.e.proDThumbImage(k.id+"_thumbnail_size", p.ImageID)
	if img == "" {
		return ""
	}
	out := `<a class="elementor-post__thumbnail__link" href="` + escURL(p.URL) + `" tabindex="-1" ` + k.optionalAttrs() + `><div class="elementor-post__thumbnail">` + img + "</div></a>\n"
	if truthy(k.v("show_badge")) {
		out += k.badge(p)
	}
	if truthy(k.v("show_avatar")) {
		out += k.avatar(p)
	}
	return out
}

func (k *proDSkin) badge(p *PostData) string {
	tax := k.s("badge_taxonomy")
	var terms []TermLink
	switch tax {
	case "":
		return ""
	case "category":
		terms = p.Categories
	case "post_tag":
		terms = p.Tags
	default:
		if pd := k.e.r.pro(); pd.PostTerms != nil {
			terms = pd.PostTerms(p.ID, tax)
		}
	}
	if len(terms) == 0 {
		return ""
	}
	return `<div class="elementor-post__badge">` + terms[0].Name + "</div>\n"
}

func (k *proDSkin) avatar(p *PostData) string {
	pd := k.e.r.pro()
	if pd.AuthorOf == nil {
		return ""
	}
	u := pd.AuthorOf(p.ID)
	if u == nil || u.AvatarURL == "" {
		return ""
	}
	return `<div class="elementor-post__avatar">` + "\n" + `<img alt='` + escAttr(u.DisplayName) + `' src='` + escURL(u.AvatarURL) +
		`' class='avatar avatar-128 photo' height='128' width='128' loading='lazy' decoding='async'/>` + "</div>\n"
}

func (k *proDSkin) title(p *PostData) string {
	if !truthy(k.v("show_title")) {
		return ""
	}
	tag := validateHTMLTag(k.s("title_tag"))
	return "<" + tag + ` class="elementor-post__title">` + "\n" + `<a href="` + escAttr(k.link) + `" ` + escAttr(k.optionalAttrs()) + ">\n" +
		p.Title + "</a>\n</" + tag + ">\n"
}

// excerpt mirrors Skin_Base::render_excerpt (the_excerpt with Pro's length filter and no "more").
func (k *proDSkin) excerpt(p *PostData) string {
	if !truthy(k.v("show_excerpt")) {
		return ""
	}
	return `<div class="elementor-post__excerpt">` + "\n" + k.e.proDExcerpt(p, toInt(k.v("excerpt_length")), k.s("apply_to_custom_excerpt") == "yes") + "</div>\n"
}

// proDExcerpt is the_excerpt(): a manual excerpt as written, otherwise the content trimmed to
// length words; then wptexturize and wpautop.
func (e *elemCtx) proDExcerpt(p *PostData, length int, applyToCustom bool) string {
	manual := ""
	if pd := e.r.pro(); pd.PostMeta != nil {
		manual = phpString(pd.PostMeta(p.ID, "_gwe_raw_excerpt"))
	}
	var text string
	if manual != "" {
		text = wpautop(wptexturize(manual), true)
		if applyToCustom && length > 0 {
			text = proDTrimWords(stripTags(text), length)
		}
		return text
	}
	text = proDTrimWords(p.Excerpt, length)
	if text == "" {
		return ""
	}
	return wpautop(wptexturize(text), true)
}

// proDTrimWords is wp_trim_words with an empty "more".
func proDTrimWords(text string, n int) string {
	words := strings.Fields(stripTags(text))
	if n <= 0 || len(words) <= n {
		return strings.Join(words, " ")
	}
	return strings.Join(words[:n], " ")
}

func (k *proDSkin) readMoreBottom() bool {
	if k.id == "full_content" {
		return false
	}
	return phpString(k.e.raw(k.id+"_read_more_alignment")) == "yes" && phpString(k.e.raw(k.id+"_show_read_more")) == "yes" &&
		phpString(k.e.raw(k.id+"_masonry")) != "yes"
}

func (k *proDSkin) readMore(p *PostData) string {
	if !truthy(k.v("show_read_more")) {
		return ""
	}
	a := `<a class="elementor-post__read-more" href="` + escURL(k.link) + `" aria-label="` + escAttr("Read more about "+p.Title) +
		`" tabindex="-1" ` + k.optionalAttrs() + ">\n" + k.s("read_more_text") + "</a>\n"
	if k.readMoreBottom() {
		return `<div class="elementor-post__read-more-wrapper">` + "\n" + a + "</div>\n"
	}
	return a
}

func (k *proDSkin) metaData(p *PostData) string {
	meta := k.v("meta_data")
	if len(asList(meta)) == 0 {
		return ""
	}
	pd := k.e.r.pro()
	var b strings.Builder
	b.WriteString(`<div class="elementor-post__meta-data">` + "\n")
	if proDHas(meta, "author") {
		b.WriteString(`<span class="elementor-post-author">` + "\n" + p.AuthorName + "</span>\n")
	}
	if proDHas(meta, "date") {
		b.WriteString(`<span class="elementor-post-date">` + "\n" + p.Date + "</span>\n")
	}
	if proDHas(meta, "time") {
		b.WriteString(`<span class="elementor-post-time">` + "\n" + p.Time + "</span>\n")
	}
	if proDHas(meta, "comments") {
		var c string
		switch p.CommentCount {
		case 0:
			c = "No Comments"
		case 1:
			c = "1 Comment"
		default:
			c = strconv.Itoa(p.CommentCount) + " Comments"
		}
		b.WriteString(`<span class="elementor-post-avatar">` + "\n" + c + "</span>\n")
	}
	if proDHas(meta, "modified") {
		d := p.Date
		if pd.PostMeta != nil {
			if t, ok := parseISO(phpString(pd.PostMeta(p.ID, "_modified_iso"))); ok {
				d = phpDate(t, pd.DateFormat)
			}
		}
		b.WriteString(`<span class="elementor-post-date">` + "\n" + d + "</span>\n")
	}
	b.WriteString("</div>\n")
	return b.String()
}

// proDPostContent mirrors Skin_Content_Base::render_post_content( true ): a GoWebEditor-built
// post renders its document inside .elementor-post__content, any other post prints the_content.
func (e *elemCtx) proDPostContent(p *PostData) string {
	pd := e.r.pro()
	if pd.Template != nil && (e.r.doc == nil || e.r.doc.ID != p.ID) {
		if doc := pd.Template(p.ID); doc != nil && len(doc.Elements) > 0 {
			return `<div class="elementor-post__content">` + e.proDRenderDocument(doc, p, nil) + "</div>"
		}
	}
	if pd.PostMeta != nil {
		return phpString(pd.PostMeta(p.ID, "_gwe_the_content"))
	}
	return ""
}

// proDRenderDocument renders another document (a loop item template or a built post) with post as
// the current post; wrapper returns the opening tag ("" = the usual document wrapper).
func (e *elemCtx) proDRenderDocument(doc *Document, post *PostData, wrapper func(doc *Document) string) string {
	rc := *e.r.rc
	th := ThemeData{}
	if rc.Theme != nil {
		th = *rc.Theme
	}
	th.Post = func() *PostData { return post }
	th.Archive = func() *ArchiveData { return nil }
	rc.Theme = &th
	r2 := newRenderer(doc, &rc)
	r2.resolveDynamic(doc)
	var b strings.Builder
	if wrapper != nil {
		b.WriteString(wrapper(doc))
	} else {
		b.WriteString(`<div data-elementor-type="` + escAttr(doc.Type) + `" data-elementor-id="` + itoa64(doc.ID) + `" class="elementor elementor-` + itoa64(doc.ID) + `">` + "\n")
	}
	for _, el := range doc.Elements {
		if el != nil {
			b.WriteString(r2.printElement(el))
		}
	}
	b.WriteString("</div>\n")
	e.r.styles = addUnique(e.r.styles, r2.styles...)
	e.r.scripts = addUnique(e.r.scripts, r2.scripts...)
	return b.String()
}

// --- portfolio -----------------------------------------------------------------------------------

type proDTerm struct {
	ID   int64
	Name string
}

func (e *elemCtx) proDPostTerms(p *PostData, tax string) []proDTerm {
	pd := e.r.pro()
	if tax == "" || pd.PostTermIDs == nil {
		return nil
	}
	ids := pd.PostTermIDs(p.ID, tax)
	var names []TermLink
	switch {
	case tax == "category" && len(p.Categories) == len(ids):
		names = p.Categories
	case tax == "post_tag" && len(p.Tags) == len(ids):
		names = p.Tags
	case pd.PostTerms != nil:
		names = pd.PostTerms(p.ID, tax)
	}
	var out []proDTerm
	for i, id := range ids {
		t := proDTerm{ID: id}
		if i < len(names) {
			t.Name = names[i].Name
		}
		out = append(out, t)
	}
	return out
}

func proDRenderPortfolio(e *elemCtx) string {
	list := e.proDRun(e.proDQuery("posts_", toInt(e.raw("posts_per_page")), 1))
	if len(list.Posts) == 0 {
		return ""
	}
	tax := phpString(e.raw("taxonomy")) // get_settings: no condition filtering
	tags := make([][]proDTerm, len(list.Posts))
	for i, p := range list.Posts {
		tags[i] = e.proDPostTerms(p, tax)
	}
	var b strings.Builder
	if truthy(e.raw("show_filter_bar")) && tax != "" {
		seen := map[int64]bool{}
		var terms []proDTerm
		for _, ts := range tags {
			for _, t := range ts {
				if !seen[t.ID] {
					seen[t.ID] = true
					terms = append(terms, t)
				}
			}
		}
		if len(terms) > 0 {
			sort.SliceStable(terms, func(i, j int) bool { return terms[i].Name < terms[j].Name })
			b.WriteString(`<ul class="elementor-portfolio__filters">` + "\n" +
				`<li class="elementor-portfolio__filter elementor-active" tabindex="0" data-filter="__all">All</li>` + "\n")
			for _, t := range terms {
				b.WriteString(`<li class="elementor-portfolio__filter" tabindex="0" data-filter="` + itoa64(t.ID) + `">` + t.Name + "</li>\n")
			}
			b.WriteString("</ul>\n")
		}
	}
	b.WriteString(`<div class="elementor-portfolio elementor-grid elementor-posts-container" role="list">` + "\n")
	for i, p := range list.Posts {
		cls := []string{"elementor-portfolio-item", "elementor-post"}
		var fc []string
		for _, t := range tags[i] {
			fc = append(fc, "elementor-filter-"+itoa64(t.ID))
		}
		if len(fc) > 0 {
			cls = append(cls, strings.Join(fc, " "))
		}
		if p.Class != "" {
			cls = append(cls, p.Class)
		}
		b.WriteString(`<article class="` + escAttr(strings.Join(cls, " ")) + `" role="listitem">` + "\n")
		b.WriteString(`<a class="elementor-post__thumbnail__link" href="` + p.URL + `">` + "\n")
		b.WriteString(`<div class="elementor-portfolio-item__img elementor-post__thumbnail">` + "\n" + e.proDThumbImage("thumbnail_size", p.ImageID) + "</div>\n")
		b.WriteString(`<div class="elementor-portfolio-item__overlay">` + "\n")
		if truthy(e.raw("show_title")) {
			tag := validateHTMLTag(phpString(e.raw("title_tag")))
			b.WriteString("<" + tag + ` class="elementor-portfolio-item__title">` + "\n" + p.Title + "</" + tag + ">\n")
		}
		b.WriteString("</div>\n</a>\n</article>\n")
	}
	b.WriteString("</div>\n")
	return b.String()
}

// --- loop grid / loop carousel ---------------------------------------------------------------------

// proDLoopItem prints one loop item: the template document with the post as the current post.
// extra is appended to the wrapper's classes (alternate templates).
func (e *elemCtx) proDLoopItem(tplID int64, post *PostData, withCSS, slide bool, extra string) string {
	pd := e.r.pro()
	if pd.Template == nil {
		return ""
	}
	doc := pd.Template(tplID)
	if doc == nil {
		return ""
	}
	doc.Type = "loop-item"
	var b strings.Builder
	if withCSS {
		css, _, _ := postCSS(doc, e.r.rc)
		b.WriteString(`<style id="loop-` + itoa64(tplID) + `">` + escHTML(phpString(doc.Settings["custom_css"])) + css + "</style>")
	}
	cls := "elementor elementor-" + itoa64(doc.ID) + extra
	if slide {
		cls += " swiper-slide"
	}
	cls += " e-loop-item e-loop-item-" + itoa64(post.ID)
	pc := post.Class
	if pc == "" && pd.LoopItemClasses != nil {
		pc = pd.LoopItemClasses(post.ID, post.Type)
	}
	if pc != "" {
		cls += " " + pc
	}
	b.WriteString(e.proDRenderDocument(doc, post, func(doc *Document) string {
		s := `<div data-elementor-type="loop-item" data-elementor-id="` + itoa64(doc.ID) + `" class="` + escAttr(cls) + `" data-elementor-post-type="elementor_library"`
		if slide {
			s += ` role="group" aria-roledescription="slide"`
		}
		return s + ` data-custom-edit-handle="1">` + "\n"
	}))
	return b.String()
}

func (e *elemCtx) proDNothingFound() string {
	var b strings.Builder
	b.WriteString(`<div class="e-loop-nothing-found-message">` + "\n")
	if e.s("enable_nothing_found_message") == "yes" {
		tag := validateHTMLTag(e.s("nothing_found_message_html_tag"))
		b.WriteString("<" + tag + ` class="e-loop-nothing-found-message__text">` + "\n" + e.s("nothing_found_message_text") + "</" + tag + ">\n")
	}
	b.WriteString("</div>\n")
	return b.String()
}

// proDLoopList runs the loop widget's query and prints its items (loop_builder.go).
func (e *elemCtx) proDLoopList(slide bool) (list *PostList, items string, ok bool, empty string) {
	if e.s("_skin") == "post_taxonomy" { // gap-loops2: the Post Taxonomy skin (loop_terms.go)
		return e.loopTermList(slide)
	}
	id := toInt64(e.s("template_id"))
	if id == 0 {
		if e.editing() {
			return nil, "", false, `<div class="e-loop-empty-view__wrapper"><!-- Will be filled with JS --></div>` + "\n"
		}
		return nil, "", false, ""
	}
	a := e.loopAlternates()
	perPage, page := toInt(e.get("posts_per_page")), e.proDCurrentPage()
	list, static := e.loopPosts(a, perPage, page)
	if len(list.Posts) == 0 {
		return list, "", false, e.proDNothingFound()
	}
	return list, e.loopItems(list, a, static, perPage, page, slide), true, ""
}

func proDRenderLoopGrid(e *elemCtx) string {
	list, items, ok, empty := e.proDLoopList(false)
	if !ok {
		return empty
	}
	k := &proDSkin{e: e, id: "post"}
	var b strings.Builder
	b.WriteString(k.loopHeader([]string{"elementor-loop-container"}, nil))
	b.WriteString(items)
	b.WriteString("</div>\n")
	b.WriteString(e.proDLoopFooter(list))
	return b.String()
}

func proDRenderLoopCarousel(e *elemCtx) string {
	_, items, ok, empty := e.proDLoopList(true)
	if !ok {
		return empty
	}
	k := &proDSkin{e: e, id: "post"}
	var b strings.Builder
	b.WriteString(k.loopHeader([]string{"swiper", "elementor-loop-container"}, func(a *attrs) {
		if d := e.s("direction"); d != "" {
			a.add("dir", d)
		}
	}))
	live := "polite"
	if e.s("autoplay") == "yes" {
		live = "off"
	}
	b.WriteString(`<div class="swiper-wrapper" aria-live="` + live + `">` + "\n")
	b.WriteString(items)
	b.WriteString("</div>\n</div>\n")
	b.WriteString(e.proDCarouselFooter())
	return b.String()
}

// proDCarouselFooter mirrors Base_Carousel_Trait::render_carousel_footer.
func (e *elemCtx) proDCarouselFooter() string {
	n := e.s("posts_per_page")
	if !(n == "" || toInt(n) > 1) {
		return ""
	}
	var b strings.Builder
	if e.s("arrows") == "yes" {
		for _, side := range [][3]string{{"prev", "Previous", "previous"}, {"next", "Next", "next"}} {
			b.WriteString(`<div class="elementor-swiper-button elementor-swiper-button-` + side[0] + `" role="button" tabindex="0" aria-label="` + side[1] + `">` + "\n")
			if ic := asMap(e.get("navigation_" + side[2] + "_icon")); ic != nil && truthy(ic["value"]) {
				b.WriteString(renderIcon(ic, newAttrs().add("aria-hidden", "true"), "i"))
			}
			b.WriteString("</div>\n")
		}
	}
	if e.s("pagination") != "" {
		b.WriteString(`<div class="swiper-pagination"></div>` + "\n")
	}
	return b.String()
}

// --- taxonomy filter -------------------------------------------------------------------------------

// --- sitemap -----------------------------------------------------------------------------------------

var proDTaxLabels = map[string]string{"category": "Categories", "post_tag": "Tags", "post_format": "Formats"}

func proDRenderSitemap(e *elemCtx) string {
	items := asList(e.get("sitemap_items"))
	if len(items) == 0 {
		return ""
	}
	titleTag := validateHTMLTag(e.s("sitemap_title_tag"))
	nofollow := e.s("sitemap_add_nofollow") == "yes"
	exclude := proDIDs(e.get("sitemap_exclude_ids"))
	var b strings.Builder
	b.WriteString(`<div class="elementor-sitemap-wrap">`)
	for _, it := range items {
		m := asMap(it)
		if m == nil {
			continue
		}
		b.WriteString(e.proDSitemapSection(m, titleTag, nofollow, exclude))
	}
	b.WriteString("</div>")
	return b.String()
}

func (e *elemCtx) proDSitemapSection(m map[string]any, titleTag string, nofollow bool, exclude []int64) string {
	get := func(k string) string { return phpString(m[k]) }
	isTax := get("sitemap_type_selector") == "taxonomy"
	hier := get("sitemap_hierarchical") == "yes"
	depth := toInt(m["sitemap_depth"])
	itemType := get("sitemap_source_post_type")
	if isTax {
		itemType = get("sitemap_source_taxonomy")
	}
	title := get("sitemap_title")
	if title == "" {
		if isTax {
			title = proDTaxLabels[itemType]
		} else if pd := e.r.pro(); pd.PostTypeLabel != nil && itemType != "" {
			title = pd.PostTypeLabel(itemType)
		}
	}
	rel := ""
	if nofollow {
		rel = `rel="nofollow" `
	}
	itemCls := "elementor-sitemap-item elementor-sitemap-item-" + itemType + " "
	var items string
	if isTax {
		items = e.proDSitemapTerms(itemType, get("sitemap_orderby_taxonomy"), get("sitemap_order"), get("sitemap_hide_empty") == "yes", hier, depth, itemCls, rel)
	} else {
		items = e.proDSitemapPosts(itemType, get("sitemap_orderby_post_type"), get("sitemap_order"), hier, depth, exclude, itemCls, rel)
	}
	var b strings.Builder
	b.WriteString(`<div class="elementor-sitemap-section">`)
	if title != "" {
		b.WriteString("<" + titleTag + ` class="elementor-sitemap-title elementor-sitemap-` + itemType + `-title">` + title + "</" + titleTag + ">")
	}
	listCls := "elementor-sitemap-list elementor-sitemap-" + itemType + "-list"
	if items == "" {
		b.WriteString(`<span class="` + listCls + `">None</span>`)
	} else {
		b.WriteString(`<ul class="` + listCls + `">` + items + "</ul>")
	}
	b.WriteString("</div>")
	return b.String()
}

// proDSitemapPosts mirrors Walker_Page over the post type's published posts.
func (e *elemCtx) proDSitemapPosts(typ, orderby, order string, hier bool, depth int, exclude []int64, itemCls, rel string) string {
	pd := e.r.pro()
	if pd.Query == nil || typ == "" {
		return ""
	}
	list := pd.Query(&PostQuery{PostTypes: []string{typ}, OrderBy: orderby, Order: order, PerPage: 100000, Page: 1, IgnoreSticky: true, ExcludeIDs: exclude})
	if list == nil || len(list.Posts) == 0 {
		return ""
	}
	parent := map[int64]int64{}
	if hier && pd.PostMeta != nil {
		for _, p := range list.Posts {
			parent[p.ID] = toInt64(pd.PostMeta(p.ID, "_parent_id"))
		}
	}
	inList := map[int64]bool{}
	for _, p := range list.Posts {
		inList[p.ID] = true
	}
	var walk func(pid int64, level int) string
	walk = func(pid int64, level int) string {
		var b strings.Builder
		for _, p := range list.Posts {
			par := parent[p.ID]
			if hier && !inList[par] {
				par = 0
			}
			if hier && par != pid {
				continue
			}
			b.WriteString(`<li class="` + itemCls + `page_item page-item-` + itoa64(p.ID) + `"><a ` + rel + `href="` + escURL(p.URL) + `">` + p.Title + "</a>")
			if hier && (depth == 0 || level+1 < depth) {
				if sub := walk(p.ID, level+1); sub != "" {
					b.WriteString("<ul class='children'>" + sub + "</ul>")
				}
			}
			b.WriteString("</li>")
		}
		return b.String()
	}
	return walk(0, 0)
}

// proDSitemapTerms mirrors wp_list_categories( title_li => ” ).
func (e *elemCtx) proDSitemapTerms(tax, orderby, order string, hideEmpty, hier bool, depth int, itemCls, rel string) string {
	pd := e.r.pro()
	if pd.Terms == nil || tax == "" {
		return ""
	}
	var terms []*TermItem
	for _, t := range pd.Terms(tax, map[string]any{"hide_empty": hideEmpty}) {
		if hideEmpty && t.Count == 0 {
			continue
		}
		terms = append(terms, t)
	}
	sort.SliceStable(terms, func(i, j int) bool {
		if orderby == "id" {
			return terms[i].ID < terms[j].ID
		}
		return strings.ToLower(terms[i].Name) < strings.ToLower(terms[j].Name)
	})
	if strings.EqualFold(order, "desc") {
		for i, j := 0, len(terms)-1; i < j; i, j = i+1, j-1 {
			terms[i], terms[j] = terms[j], terms[i]
		}
	}
	inList := map[int64]bool{}
	for _, t := range terms {
		inList[t.ID] = true
	}
	var walk func(pid int64, level int) string
	walk = func(pid int64, level int) string {
		var b strings.Builder
		for _, t := range terms {
			par := t.ParentID
			if hier && !inList[par] {
				par = 0
			}
			if hier && par != pid {
				continue
			}
			b.WriteString("\t" + `<li class="` + itemCls + `cat-item cat-item-` + itoa64(t.ID) + `"><a ` + rel + `href="` + escURL(t.URL) + `">` + t.Name + "</a>\n")
			if hier && (depth == 0 || level+1 < depth) {
				if sub := walk(t.ID, level+1); sub != "" {
					b.WriteString("<ul class='children'>\n" + sub + "</ul>\n")
				}
			}
			b.WriteString("</li>\n")
		}
		return b.String()
	}
	return walk(0, 0)
}
