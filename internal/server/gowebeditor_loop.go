package server

// Golementor Pro query widgets (posts, portfolio, loop grid / carousel, taxonomy filter, search):
// the WP_Query behind Pro's query control (builder/loop_query.go builds the PostQuery) and the
// per-request loop state (avoid list, loop filter selections from the query string).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/builder"
	"wpgo/internal/links"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// gweLoopState is the request's loop state: the e-filter-* selections of the query string.
func (s *Server) gweLoopState(ctx context.Context) *builder.LoopState {
	st := &builder.LoopState{}
	if c, ok := ctx.(*gin.Context); ok && c != nil && c.Request != nil {
		st.Query, st.RawQuery = c.Request.URL.Query(), c.Request.URL.RawQuery
		st.Filters = builder.ParseLoopFilters(st.Query)
	}
	return st
}

// gweLoopPublicTypes are the post types of post_type "any" / manual selection (public, not
// attachments: those are never "publish").
func (s *Server) gweLoopPublicTypes(ctx context.Context) []string {
	rows, err := s.store.Pool.Query(ctx, `SELECT slug FROM content_types WHERE public AND slug <> 'attachment' ORDER BY id`)
	if err != nil {
		return []string{"post", "page"}
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var slug string
		if rows.Scan(&slug) == nil {
			out = append(out, slug)
		}
	}
	return out
}

// gweLoopTaxTerms resolves a tax_query clause to term ids: the given ids and slugs, plus their
// descendants when the taxonomy is hierarchical and children count (IN, NOT IN).
func (s *Server) gweLoopTaxTerms(ctx context.Context, cl builder.LoopTaxClause, children bool) []int64 {
	type term struct{ id, parent int64 }
	var all []term
	bySlug := map[string]int64{}
	hier := false
	if cl.Taxonomy != "" {
		_ = s.store.Pool.QueryRow(ctx, `SELECT hierarchical FROM taxonomies WHERE slug = $1`, cl.Taxonomy).Scan(&hier)
		rows, err := s.store.Pool.Query(ctx, `SELECT id, coalesce(parent_id,0), slug FROM terms WHERE taxonomy = $1`, cl.Taxonomy)
		if err == nil {
			for rows.Next() {
				var t term
				var slug string
				if rows.Scan(&t.id, &t.parent, &slug) == nil {
					all = append(all, t)
					bySlug[slug] = t.id
				}
			}
			rows.Close()
		}
	}
	seen := map[int64]bool{}
	var out []int64
	var add func(id int64)
	add = func(id int64) {
		if seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
		if children && hier {
			for _, t := range all {
				if t.parent == id {
					add(t.id)
				}
			}
		}
	}
	for _, id := range cl.TermIDs {
		add(id)
	}
	for _, slug := range cl.Slugs {
		if id, ok := bySlug[slug]; ok {
			add(id)
		}
	}
	return out
}

// gweLoopTaxSQL renders a tax_query clause.
func (s *Server) gweLoopTaxSQL(ctx context.Context, cl builder.LoopTaxClause, add func(any) string) string {
	if len(cl.Or) > 0 {
		var parts []string
		for _, sub := range cl.Or {
			if p := s.gweLoopTaxSQL(ctx, sub, add); p != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) == 0 {
			return ""
		}
		return "(" + strings.Join(parts, " OR ") + ")"
	}
	has := func(ids []int64) string {
		return "EXISTS (SELECT 1 FROM post_terms pt WHERE pt.post_id = p.id AND pt.term_id = ANY(" + add(ids) + "))"
	}
	switch cl.Op {
	case "NOT IN":
		ids := s.gweLoopTaxTerms(ctx, cl, true)
		if len(ids) == 0 {
			return ""
		}
		return "NOT " + has(ids)
	case "AND":
		// Every term (WP_Tax_Query AND); terms that do not exist match nothing.
		var parts []string
		for _, id := range cl.TermIDs {
			parts = append(parts, has([]int64{id}))
		}
		for _, slug := range cl.Slugs {
			ids := s.gweLoopTaxTerms(ctx, builder.LoopTaxClause{Taxonomy: cl.Taxonomy, Slugs: []string{slug}}, false)
			if len(ids) == 0 {
				return "false"
			}
			parts = append(parts, has(ids))
		}
		if len(parts) == 0 {
			return ""
		}
		return "(" + strings.Join(parts, " AND ") + ")"
	default: // IN
		ids := s.gweLoopTaxTerms(ctx, cl, true)
		if len(ids) == 0 {
			return "false"
		}
		return has(ids)
	}
}

// gweLoopQuery runs a query widget's PostQuery (WP_Query with Pro's query args).
func (s *Server) gweLoopQuery(ctx context.Context, q *builder.PostQuery, v *siteView, lb *links.Builder) *builder.PostList {
	out := &builder.PostList{CurrentPage: 1}
	if q == nil {
		return out
	}
	if q.CurrentQuery {
		if v != nil {
			for _, pv := range v.Posts {
				out.Posts = append(out.Posts, s.gwePostData(pv))
			}
			out.Total, out.Pages, out.CurrentPage = v.TotalPosts, v.TotalPages, max(1, v.Q.Paged)
		}
		return out
	}
	ext := q.Ext
	if ext == nil {
		ext = &builder.LoopQueryExt{}
	}
	out = s.gweLoopRun(ctx, q, ext, lb)
	if len(out.Posts) == 0 && ext.Fallback != "" {
		// upstream Related_Query::get_fallback_query
		fb := &builder.PostQuery{PostTypes: q.PostTypes, PerPage: q.PerPage, Page: 1, IgnoreSticky: true, ExcludeIDs: q.ExcludeIDs}
		if ext.Fallback == "fallback_by_id" {
			fb.IncludeIDs, fb.OrderBy = ext.FallbackIDs, "rand"
			if len(fb.IncludeIDs) == 0 {
				fb.IncludeIDs = []int64{0}
			}
		} else {
			fb.OrderBy, fb.Order = "date", "DESC"
		}
		out = s.gweLoopRun(ctx, fb, &builder.LoopQueryExt{}, lb)
	}
	return out
}

func (s *Server) gweLoopRun(ctx context.Context, q *builder.PostQuery, ext *builder.LoopQueryExt, lb *links.Builder) *builder.PostList {
	ids, out := s.gweLoopIDs(ctx, q, ext)
	for _, id := range ids {
		if p, err := s.store.GetPost(ctx, id); err == nil {
			out.Posts = append(out.Posts, s.gweProPostData(ctx, p, lb))
		}
	}
	return out
}

// gweLoopIDs runs a PostQuery: the page's post ids and the list's counts (no posts).
func (s *Server) gweLoopIDs(ctx context.Context, q *builder.PostQuery, ext *builder.LoopQueryExt) ([]int64, *builder.PostList) {
	q = s.gooLoopQuery(ctx, q) // Goocommerce Memberships: posts hidden from the visitor
	out := &builder.PostList{CurrentPage: 1}
	var args []any
	add := func(x any) string {
		args = append(args, x)
		return "$" + strconv.Itoa(len(args))
	}
	conds := []string{"p.status = 'publish'"}
	types := q.PostTypes
	anyType := ext.AllPublicTypes
	for _, t := range types {
		if t == "any" {
			anyType = true
		}
	}
	if anyType {
		types = s.gweLoopPublicTypes(ctx)
	}
	if len(types) == 0 {
		types = []string{"post"}
	}
	conds = append(conds, "p.type = ANY("+add(types)+")")
	if len(q.IncludeIDs) > 0 {
		conds = append(conds, "p.id = ANY("+add(q.IncludeIDs)+")")
	}
	if len(q.ExcludeIDs) > 0 {
		conds = append(conds, "NOT (p.id = ANY("+add(q.ExcludeIDs)+"))")
	}
	if len(q.IncludeAuthors) > 0 {
		conds = append(conds, "p.author_id = ANY("+add(q.IncludeAuthors)+")")
	}
	if len(q.ExcludeAuthors) > 0 {
		conds = append(conds, "NOT (coalesce(p.author_id,0) = ANY("+add(q.ExcludeAuthors)+"))")
	}
	// Callers that predate LoopQueryExt (term ids without a taxonomy).
	if len(q.IncludeTermIDs) > 0 {
		conds = append(conds, "EXISTS (SELECT 1 FROM post_terms pt WHERE pt.post_id = p.id AND pt.term_id = ANY("+add(q.IncludeTermIDs)+"))")
	}
	if len(q.ExcludeTermIDs) > 0 {
		conds = append(conds, "NOT EXISTS (SELECT 1 FROM post_terms pt WHERE pt.post_id = p.id AND pt.term_id = ANY("+add(q.ExcludeTermIDs)+"))")
	}
	for _, cl := range ext.Tax {
		if c := s.gweLoopTaxSQL(ctx, cl, add); c != "" {
			conds = append(conds, c)
		}
	}
	if q.Search != "" {
		// WP_Query::parse_search: every word in the title, excerpt or content.
		for _, w := range gweLoopSearchWords(q.Search) {
			not := strings.HasPrefix(w, "-") && len(w) > 1
			if not {
				w = w[1:]
			}
			like := add("%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(w) + "%")
			c := "(p.title ILIKE " + like + " OR p.excerpt ILIKE " + like + " OR p.content ILIKE " + like + ")"
			if not {
				c = "NOT " + c
			}
			conds = append(conds, c)
		}
	}
	now := time.Now().In(s.loc())
	switch q.DateMode {
	case "today":
		conds = append(conds, "p.published_at > "+add(now.Add(-24*time.Hour)))
	case "week":
		conds = append(conds, "p.published_at > "+add(now.AddDate(0, 0, -7)))
	case "month":
		conds = append(conds, "p.published_at > "+add(now.AddDate(0, -1, 0)))
	case "quarter":
		conds = append(conds, "p.published_at > "+add(now.AddDate(0, -3, 0)))
	case "year":
		conds = append(conds, "p.published_at > "+add(now.AddDate(-1, 0, 0)))
	case "exact":
		for _, d := range []struct {
			v  string
			op string
		}{{q.DateAfter, ">="}, {q.DateBefore, "<="}} {
			if d.v == "" {
				continue
			}
			for _, layout := range []string{"2006-01-02 15:04", "2006-01-02 15:04:05", "2006-01-02"} {
				if t, err := time.ParseInLocation(layout, d.v, s.loc()); err == nil {
					if layout == "2006-01-02" && d.op == "<=" {
						t = t.Add(24*time.Hour - time.Second)
					}
					conds = append(conds, "p.published_at "+d.op+" "+add(t))
					break
				}
			}
		}
	}
	dir := "DESC"
	if strings.EqualFold(q.Order, "asc") {
		dir = "ASC"
	}
	order := "p.published_at " + dir + " NULLS LAST, p.id " + dir
	switch q.OrderBy {
	case "post_title", "title":
		order = "lower(p.title) " + dir + ", p.id " + dir
	case "menu_order":
		order = "p.menu_order " + dir + ", p.id " + dir
	case "rand":
		order = "random()"
	case "comment_count":
		order = "p.comment_count " + dir + ", p.published_at DESC"
	case "modified", "post_modified":
		order = "p.modified_at " + dir
	case "ID", "id":
		order = "p.id " + dir
	case "post__in":
		if len(q.IncludeIDs) > 0 {
			order = "array_position(" + add(q.IncludeIDs) + "::bigint[], p.id)"
		}
	}
	// Sticky posts lead a home-like query's first page (WP_Query, is_home and not ignored).
	if !q.IgnoreSticky && q.OrderBy != "rand" && len(q.IncludeIDs) == 0 && len(ext.Tax) == 0 && q.Search == "" && max(q.Page, 1) == 1 {
		order = "p.sticky DESC, " + order
	}
	where := strings.Join(conds, " AND ")
	var total int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts p WHERE `+where, args...).Scan(&total); err != nil {
		return nil, out
	}
	perPage := q.PerPage
	page := max(q.Page, 1)
	limit := ""
	offset := q.Offset
	if perPage > 0 {
		offset += (page - 1) * perPage
		limit = " LIMIT " + add(perPage)
	}
	total -= q.Offset
	if total < 0 {
		total = 0
	}
	out.Total = total
	out.Pages = 1
	if perPage > 0 {
		out.Pages = (total + perPage - 1) / perPage
	}
	out.CurrentPage = page
	rows, err := s.store.Pool.Query(ctx, `SELECT p.id FROM posts p WHERE `+where+` ORDER BY `+order+limit+` OFFSET `+add(offset), args...)
	if err != nil {
		return nil, out
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	return ids, out
}

// gweLoopSearchWords splits a search like WP_Query::parse_search_terms: quoted phrases stay whole.
func gweLoopSearchWords(s string) []string {
	var out []string
	s = strings.TrimSpace(s)
	for s != "" {
		if s[0] == '"' {
			if j := strings.IndexByte(s[1:], '"'); j >= 0 {
				if w := strings.TrimSpace(s[1 : j+1]); w != "" {
					out = append(out, w)
				}
				s = strings.TrimSpace(s[j+2:])
				continue
			}
			s = s[1:]
			continue
		}
		j := strings.IndexAny(s, " \t,+")
		if j < 0 {
			out = append(out, s)
			break
		}
		if w := s[:j]; w != "" {
			out = append(out, w)
		}
		s = strings.TrimLeft(s[j:], " \t,+")
	}
	return out
}

// gweLoopWidget finds the widget a refresh request renders (Refresh_Base): from the saved
// document, or the editor's widget_model when the user may edit the document.
func (s *Server) gweLoopWidget(c *gin.Context, r *restRequest, widgetType string) (*builder.Element, *builder.Document, *store.Post, bool) {
	postID, _ := r.Int(c, "post_id")
	widgetID, _ := r.Str(c, "widget_id")
	if !gweLoopWidgetIDRe.MatchString(widgetID) {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): widget_id")
		return nil, nil, nil, false
	}
	p, err := s.store.GetPost(c, postID)
	if err != nil || !gweBuilt(p) {
		restErr(c, http.StatusNotFound, "rest_not_found", "Post not found.")
		return nil, nil, nil, false
	}
	u := restCurrent(c)
	editMode := u != nil && u.Can("edit_post", p)
	doc := gweDocument(p)
	if raw, ok := r.get(c, "widget_model"); ok && editMode {
		if b, err := json.Marshal(raw); err == nil {
			if els, err := builder.ParseElements(append(append([]byte("["), b...), ']')); err == nil && len(els) == 1 && els[0] != nil && els[0].ElType == "widget" {
				return els[0], doc, p, true
			}
		}
	}
	if !editMode && p.Status != "publish" {
		restErr(c, http.StatusUnauthorized, "rest_forbidden", "Sorry, you are not allowed to do that.")
		return nil, nil, nil, false
	}
	el := builder.FindElement(doc.Elements, widgetID)
	if el == nil || el.ElType != "widget" || (!editMode && el.WidgetType != widgetType) {
		restErr(c, http.StatusUnauthorized, "rest_forbidden", "Sorry, you are not allowed to do that.")
		return nil, nil, nil, false
	}
	return el, doc, p, true
}

var gweLoopWidgetIDRe = regexp.MustCompile(`^[a-zA-Z0-9]{7}$`)

// gweLoopRESTContext is the render context of a refresh request: the page holding the widget is
// the current post, and pagination links carry the referring page's query arguments.
func (s *Server) gweLoopRESTContext(c *gin.Context, p *store.Post) *builder.RenderContext {
	rc := s.gweRenderContext(c, false)
	lb := s.links(c)
	if rc.Theme != nil && (p.Type == "post" || p.Type == "page") {
		th := *rc.Theme
		pd := s.gweProPostData(c, p, lb)
		th.Post = func() *builder.PostData { return pd }
		rc.Theme = &th
	}
	if rc.Pro != nil {
		if rc.Pro.Loop == nil {
			rc.Pro.Loop = &builder.LoopState{}
		}
		rc.Pro.Loop.REST = true
		if ref, err := url.Parse(c.GetHeader("Referer")); err == nil {
			rc.Pro.Loop.RefererQuery = ref.RawQuery
		}
	}
	return rc
}

// gweLoopRefresh is POST elementor-pro/v1/refresh-loop (Refresh_Loop): a loop grid re-rendered with
// the taxonomy filter's selections ({widget_filters: {taxonomy: {<tax>: {terms, logicalJoin}}}}).
func (s *Server) gweLoopRefresh(c *gin.Context, r *restRequest) {
	el, doc, p, ok := s.gweLoopWidget(c, r, "loop-grid")
	if !ok {
		return
	}
	rc := s.gweLoopRESTContext(c, p)
	if raw, ok := r.get(c, "widget_filters"); ok && rc.Pro != nil {
		var m map[string]any
		switch v := raw.(type) {
		case map[string]any:
			m = v
		case string:
			_ = json.Unmarshal([]byte(v), &m)
		}
		st := rc.Pro.Loop
		if st.Filters == nil {
			st.Filters = map[string]map[string]*builder.LoopFilter{}
		}
		if tx, ok := m["taxonomy"].(map[string]any); ok {
			if st.Filters[el.ID] == nil {
				st.Filters[el.ID] = map[string]*builder.LoopFilter{}
			}
			for tax, f := range tx {
				fm, _ := f.(map[string]any)
				lf := &builder.LoopFilter{Join: gweStr(fm["logicalJoin"])}
				for _, t := range asAnyList(fm["terms"]) {
					lf.Terms = append(lf.Terms, gweStr(t))
				}
				st.Filters[el.ID][tax] = lf
			}
		}
	}
	if u, ok := r.Str(c, "pagination_base_url"); ok && u != "" && rc.Pro != nil {
		rc.Pro.CurrentURL = u
		rc.Pro.Loop.BaseURL = u
	}
	c.JSON(http.StatusOK, map[string]any{"data": s.gweFixURLs(c, builder.RenderWidgetFront(el, doc, rc))})
}

// gweLoopRefreshSearch is POST elementor-pro/v1/refresh-search (Refresh_Search): the search
// widget's live results ({data, pagination}).
func (s *Server) gweLoopRefreshSearch(c *gin.Context, r *restRequest) {
	el, doc, p, ok := s.gweLoopWidget(c, r, "search")
	if !ok {
		return
	}
	rc := s.gweLoopRESTContext(c, p)
	term, _ := r.Str(c, "search_term")
	page, _ := r.Int(c, "page_number")
	bp, _ := r.Str(c, "breakpoint")
	if rc.Pro != nil {
		rc.Pro.Loop.Search = &builder.LoopSearch{Term: term, Page: int(max(page, 1)), Breakpoint: bp}
	}
	data, pagination := builder.RenderSearchResults(el, doc, rc)
	c.JSON(http.StatusOK, map[string]any{"data": s.gweFixURLs(c, data), "pagination": s.gweFixURLs(c, pagination)})
}

// gweLoopPreview is POST elementor-pro/v1/collection-loop/loop-preview: the items the editor shows
// for an atomic Loop's query ({data: {query_id, has_items, items: [{id, title}]}}).
func (s *Server) gweLoopPreview(c *gin.Context, r *restRequest) {
	q := map[string]any{}
	if raw, ok := r.get(c, "query"); ok {
		switch v := raw.(type) {
		case map[string]any:
			q = v
		case string:
			_ = json.Unmarshal([]byte(v), &q)
		}
	}
	items := []map[string]any{}
	if force, _ := r.Bool(c, "force_empty"); !force {
		rc := s.gweRenderContext(c, true)
		if docID, ok := r.Int(c, "document_id"); ok && docID > 0 {
			if p, err := s.store.GetPost(c, docID); err == nil && (p.Type == "post" || p.Type == "page") && rc.Theme != nil {
				th := *rc.Theme
				pd := s.gweProPostData(c, p, s.links(c))
				th.Post = func() *builder.PostData { return pd }
				rc.Theme = &th
			}
		}
		items = builder.LoopPreviewItems(q, rc)
	}
	queryID, _ := q["query_id"].(string)
	c.JSON(http.StatusOK, map[string]any{"data": map[string]any{"query_id": queryID, "has_items": len(items) > 0, "items": items}})
}

// The atomic Loop's pagination handler prints with the other Pro scripts (after the atomic
// frontend handlers it depends on).
func init() { gweProScriptOrder = append(gweProScriptOrder, builder.AtomicLoopPaginationScript) }

// gweLoopItemsScreen is Golementor Pro › Loop Items: the loop item templates (the Theme
// Builder's "Loop Items" type) the Loop Grid, Loop Carousel and Search widgets render.
func (s *Server) gweLoopItemsScreen(c *gin.Context) string {
	newURL := "/wp-admin/edit?action=elementor_new_post&post_type=" + gweLibrary + "&template_type=loop-item&_wpnonce=" + s.nonce(c, "elementor_action_new_post")
	var b strings.Builder
	b.WriteString(`<p><a class="button button-primary" href="` + esc(newURL) + `">Add New Loop Item</a></p>`)
	b.WriteString(`<p class="description">A loop item is the design of one entry in a Loop Grid, Loop Carousel or live search: build it once with the post's dynamic tags (title, image, excerpt…), then pick it in the widget's Layout › Choose a template.</p>`)
	b.WriteString(`<table class="wp-list-table widefat fixed striped"><thead><tr><th>Title</th><th>Status</th><th>Last Modified</th></tr></thead><tbody>`)
	list := s.gweProLibraryPosts(c, "loop-item")
	for _, p := range list {
		b.WriteString(`<tr><td><strong><a class="row-title" href="` + esc(gweEditURL(p.ID)) + `">` + esc(p.Title) + `</a></strong><div class="row-actions"><a href="` +
			esc(gweEditURL(p.ID)) + `">Edit with Golementor</a></div></td><td>` + esc(gweStatusLabels[p.Status]) + `</td><td>` +
			esc(wp.FormatDate(p.ModifiedAt.In(s.loc()), "Y/m/d g:i a")) + `</td></tr>`)
	}
	if len(list) == 0 {
		b.WriteString(`<tr><td colspan="3">No loop items yet.</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

// gweLoopPostTypeTaxonomies is POST elementor-pro/v1/get-post-type-taxonomies
// (Get_Post_Type_Taxonomies): the taxonomy filter's options for the loop's post type,
// {name: label}, without post formats.
func (s *Server) gweLoopPostTypeTaxonomies(c *gin.Context, r *restRequest) {
	typ, _ := r.Str(c, "post_type")
	if typ == "" {
		typ = c.DefaultQuery("post_type", "post")
	}
	out := builder.Ordered{}
	rows, err := s.store.Pool.Query(c, `SELECT slug, coalesce(labels->>'name', name) FROM taxonomies WHERE object_types ? $1 ORDER BY id`, typ)
	if err == nil {
		for rows.Next() {
			var slug, label string
			if rows.Scan(&slug, &label) == nil && !(typ == "post" && slug == "post_format") {
				out = append(out, builder.KV{K: slug, V: label})
			}
		}
		rows.Close()
	}
	c.JSON(http.StatusOK, out)
}

// gweSearchPropsWidget finds the Search widget named by the e_search_props argument
// ("<widget id>-<post id>", Search Module::get_query_based_on_widget_props).
func (s *Server) gweSearchPropsWidget(ctx context.Context, props string) (*builder.Element, *builder.Document) {
	widgetID, postID, ok := strings.Cut(props, "-")
	id, err := strconv.ParseInt(postID, 10, 64)
	if !ok || err != nil || !gweLoopWidgetIDRe.MatchString(widgetID) {
		return nil, nil
	}
	p, err := s.store.GetPost(ctx, id)
	if err != nil || !gweBuilt(p) || (p.Status != "publish" && p.Status != "private") {
		return nil, nil
	}
	doc := gweDocument(p)
	el := builder.FindElement(doc.Elements, widgetID)
	if el == nil || el.ElType != "widget" {
		return nil, nil
	}
	return el, doc
}

// gweSearchPropsOK reports whether a search's e_search_props names a widget (set_query answers
// a 404 otherwise).
func (s *Server) gweSearchPropsOK(ctx context.Context, props string) bool {
	el, _ := s.gweSearchPropsWidget(ctx, props)
	return el != nil
}

// gweSearchPropsPosts is the main search query narrowed by the Search widget that submitted it
// (Search Module::set_query on pre_get_posts): the widget's search_query_* query args, every
// result on one page (posts_per_page -1), the search term kept.
func (s *Server) gweSearchPropsPosts(ctx context.Context, q *siteQuery, u *store.User) ([]*store.Post, int, int, error) {
	el, doc := s.gweSearchPropsWidget(ctx, q.SearchProps)
	if el == nil {
		return nil, 0, 1, nil
	}
	pq := builder.SearchPropsQuery(el, doc, s.gweRenderContext(ctx, false))
	if pq.CurrentQuery { // "current query": the main query's own vars
		plain := *q
		plain.SearchProps = ""
		return s.mainPosts(ctx, &plain, u)
	}
	pq.Search = q.Search
	ext := pq.Ext
	if ext == nil {
		ext = &builder.LoopQueryExt{}
	}
	ids, _ := s.gweLoopIDs(ctx, pq, ext)
	var posts []*store.Post
	for _, id := range ids {
		if p, err := s.store.GetPost(ctx, id); err == nil {
			posts = append(posts, p)
		}
	}
	return posts, len(posts), max(1, len(posts)), nil
}
