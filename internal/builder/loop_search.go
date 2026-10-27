package builder

// Golementor Pro Search widget, live results: a port of Search::render_results and
// render_pagination (modules/search/widgets/search.php), answered by the refresh-search endpoint.
// Each result is the widget's loop item template rendered with the found post.

import (
	"strconv"
	"strings"
)

// RenderSearchResults renders a search widget's live results for rc.Pro.Loop.Search: the
// results markup and the pagination links.
func RenderSearchResults(el *Element, doc *Document, rc *RenderContext) (string, string) {
	if el == nil || el.ElType != "widget" {
		return "", ""
	}
	r := newRenderer(doc, rc)
	r.resolveGlobals([]*Element{el})
	r.resolveDynamic(&Document{ID: doc.ID, Elements: []*Element{el}})
	e := r.newElemCtx(el)
	if e == nil {
		return "", ""
	}
	req := r.loopState().Search
	if req == nil {
		req = &LoopSearch{Page: 1}
	}
	if req.Page < 1 {
		req.Page = 1
	}
	q := e.loopQuery("search_query_", e.loopSearchPerPage(req.Breakpoint), req.Page)
	q.Search = req.Term
	list := e.loopRun(q, false)
	return e.loopSearchResults(list), e.loopSearchPagination(list, req.Page)
}

// loopSearchPerPage mirrors get_posts_per_page_for_breakpoint (number_of_items, the device's own
// value or the next larger device's).
func (e *elemCtx) loopSearchPerPage(device string) int {
	desktop := -1
	if v := e.get("number_of_items"); v != nil && phpString(v) != "" {
		desktop = toInt(v)
	}
	if device == "" {
		return desktop
	}
	devices := []string{}
	for _, bp := range e.r.bps {
		if bp.Direction == "max" {
			devices = append(devices, bp.Name)
		}
	}
	devices = append(devices, "desktop")
	for _, bp := range e.r.bps {
		if bp.Direction == "min" {
			devices = append(devices, bp.Name)
		}
	}
	idx := -1
	for i, d := range devices {
		if d == device {
			idx = i
		}
	}
	if idx < 0 {
		return desktop
	}
	for _, d := range devices[idx:] {
		key := "number_of_items"
		if d != "desktop" {
			key += "_" + d
		}
		v := phpString(e.get(key))
		if _, err := strconv.ParseFloat(v, 64); err == nil {
			return toInt(v)
		}
	}
	return desktop
}

// loopSearchResults mirrors render_results.
func (e *elemCtx) loopSearchResults(list *PostList) string {
	if list.Total == 0 || len(list.Posts) == 0 {
		if e.s("enable_nothing_found_message") != "yes" {
			return ""
		}
		tag := validateHTMLTag(e.s("nothing_found_message_html_tag"))
		return "\t\t\t<" + tag + " class=\"e-search-nothing-found-message\">\n\t\t\t\t" + ksesPost(e.s("nothing_found_message_text")) +
			"\t\t\t</" + tag + ">\n\t\t"
	}
	tpl := toInt64(e.s("template_id"))
	if tpl == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range list.Posts {
		b.WriteString(e.loopDynamicCSS(tpl, p))
		b.WriteString(e.proDLoopItem(tpl, p, e.loopCSSOnce(tpl), false, ""))
	}
	return b.String()
}

// loopSearchPagination mirrors render_pagination (paginate_links on the referring page with
// ?e-search-page=N, rel="nofollow" links, inactive prev/next ends).
func (e *elemCtx) loopSearchPagination(list *PostList, page int) string {
	typ := e.s("pagination_type_options")
	if typ == "none" || typ == "" || list.Pages <= 1 {
		return ""
	}
	total := list.Pages
	if lim := toInt(e.get("page_limit_settings")); lim > 0 && lim < total {
		total = lim
	}
	st := e.r.loopState()
	base := proDTrailing(e.proDBaseURL())
	var add []loopKV
	for _, kv := range loopParseQuery(st.RefererQuery) {
		if kv.K != "e-search-page" {
			add = append(add, loopKV{phpURLEncode(kv.K), phpURLEncode(kv.V)})
		}
	}
	link := func(n int) string {
		u := base
		if n > 1 {
			u += "?e-search-page=" + strconv.Itoa(n)
		}
		return escURL(loopAddQueryArgs(u, add))
	}
	prevNext := typ != "numbers"
	var links []string
	if prevNext && page > 1 {
		links = append(links, `<a class="prev page-numbers" href="`+link(page-1)+`">`+e.s("pagination_prev_label")+`</a>`)
	}
	links = append(links, proDPaginateLinks(page, total, e.s("pagination_shorten_settings") != "yes", link)...)
	if prevNext && page < total {
		links = append(links, `<a class="next page-numbers" href="`+link(page+1)+`">`+e.s("pagination_next_label")+`</a>`)
	}
	if page == 1 && prevNext {
		links = append([]string{`<span class="prev page-numbers inactive">` + e.s("pagination_prev_label") + `</span>`}, links...)
	}
	if page == total && prevNext {
		links = append(links, `<span class="next page-numbers inactive">`+e.s("pagination_next_label")+`</span>`)
	}
	for i, l := range links {
		links[i] = strings.ReplaceAll(l, "<a ", `<a rel="nofollow" `)
	}
	return strings.Join(links, "\n")
}

// SearchPropsQuery is the query a search results page runs when a Search widget submitted it
// (e_search_props; Search Module::get_query_based_on_widget_props): upstream Post_Query with the
// widget's search_query_* settings and posts_per_page -1 (no paging, so no offset either).
func SearchPropsQuery(el *Element, doc *Document, rc *RenderContext) *PostQuery {
	r := newRenderer(doc, rc)
	r.resolveGlobals([]*Element{el})
	e := r.newElemCtx(el)
	if e == nil {
		return &PostQuery{PerPage: -1, Page: 1, PostTypes: []string{"post"}, Ext: &LoopQueryExt{}}
	}
	r.loopState().ignoreAvoid = true
	q := e.loopQuery("search_query_", -1, 1)
	q.PerPage, q.Page, q.Offset = -1, 1, 0
	return q
}
