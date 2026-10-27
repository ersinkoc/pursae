package builder

// Golementor Pro query control: a port of upstream Post_Query / upstream Related_Query
// (modules/query-control/classes) and the loop filter's tax_query builder (modules/loop-filter:
// Taxonomy_Query_Builder, Taxonomy_Manager and the query types). The posts, portfolio, loop grid,
// loop carousel and search widgets build their PostQuery here; the server runs it.

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// LoopQueryExt carries the parts of Pro's query control PostQuery has no fields for.
type LoopQueryExt struct {
	// Tax is the tax_query: every clause must match.
	Tax []LoopTaxClause
	// AllPublicTypes queries every public post type (manual selection; post type "any").
	AllPublicTypes bool
	// RelatedAuthorID limits related posts to one author (related query, include "authors").
	RelatedAuthorID int64
	// Fallback of a related query without results: "fallback_recent" or "fallback_by_id".
	Fallback    string
	FallbackIDs []int64
}

// LoopTaxClause is one tax_query clause. Op "IN" matches posts with any of the terms, "AND" with
// all of them, "NOT IN" with none; hierarchical taxonomies include descendants for IN and NOT IN.
// Terms are given by id or by slug. A clause with Or matches when any of its alternatives does.
type LoopTaxClause struct {
	Taxonomy string
	TermIDs  []int64
	Slugs    []string
	Op       string
	Or       []LoopTaxClause
}

// LoopState is the per-request state of the query widgets.
type LoopState struct {
	// Displayed is Query_Control's avoid list: every post a query widget printed so far.
	Displayed []int64
	// Filters are the loop filter selections by loop widget id and taxonomy (the e-filter-*
	// query string, or the refresh-loop request).
	Filters map[string]map[string]*LoopFilter
	// Search is the refresh-search request being answered (nil otherwise).
	Search *LoopSearch
	// Query is the request's query string (RawQuery as received): page links keep its
	// e-filter-* and other widgets' e-page-* arguments.
	Query    url.Values
	RawQuery string
	// REST marks a refresh request: page links carry the referring page's query arguments
	// (RefererQuery); BaseURL is its pagination_base_url.
	REST         bool
	RefererQuery string
	BaseURL      string

	// Term is the loop term of a term loop item ($wp_query->loop_term) and InTaxonomy is
	// is_loop_taxonomy(); TermsDisplayed and AtomicTermsDisplayed are the term avoid lists of the
	// Loop Grid's taxonomy skin and of the atomic Loop (loop_terms.go).
	Term                 *TermItem
	InTaxonomy           bool
	TermsDisplayed       []int64
	AtomicTermsDisplayed []int64
	termEmpty            map[string]bool // taxonomy skin loops without terms, by widget id
	atomicItem           bool            // an atomic Loop item is rendering
	atomicPost           *PostData       // its post (nil in term loops)

	printed     map[string]bool // loop templates whose CSS is printed
	ignoreAvoid bool            // get_query_ignoring_avoid_list
}

// LoopFilter is a taxonomy filter selection: term slugs and their logical join (AND, OR or
// DISABLED).
type LoopFilter struct {
	Terms []string
	Join  string
}

// LoopSearch is a live search request: the term, the results page and the device.
type LoopSearch struct {
	Term       string
	Page       int
	Breakpoint string
}

func (r *renderer) loopState() *LoopState {
	pd := r.pro()
	if pd.Loop == nil {
		pd.Loop = &LoopState{}
	}
	return pd.Loop
}

var loopFilterParamRe = regexp.MustCompile(`^e-filter-([a-z0-9]{7})-([a-z0-9_\-]+)$`)

// ParseLoopFilters reads the loop filter selections of a query string
// (Module::maybe_populate_filters_from_query_string): e-filter-<widget id>-<taxonomy>=<terms>,
// terms joined by "+" (AND, a space once decoded) or "~" (OR).
func ParseLoopFilters(q url.Values) map[string]map[string]*LoopFilter {
	out := map[string]map[string]*LoopFilter{}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		m := loopFilterParamRe.FindStringSubmatch(k)
		if m == nil || len(q[k]) == 0 {
			continue
		}
		v := q[k][len(q[k])-1]
		sep, join := " ", "AND"
		if strings.Contains(v, "~") {
			sep, join = "~", "OR"
		}
		terms := strings.Split(v, sep)
		if len(terms) == 0 {
			continue
		}
		if out[m[1]] == nil {
			out[m[1]] = map[string]*LoopFilter{}
		}
		out[m[1]][m[2]] = &LoopFilter{Terms: terms, Join: join}
	}
	return out
}

// loopQuery mirrors Module_Query::get_query for the query group control named by prefix
// ("posts_", "post_query_", "search_query_"): upstream Post_Query::get_query_args, or
// upstream Related_Query's for the "related" source.
func (e *elemCtx) loopQuery(prefix string, perPage, page int) *PostQuery {
	q := &PostQuery{PerPage: perPage, Page: page, Ext: &LoopQueryExt{}}
	get := func(k string) any { return e.get(prefix + k) }
	str := func(k string) string { return phpString(get(k)) }
	pd := e.r.pro()
	cur := e.currentPostAny()
	pt := str("post_type")
	if pt == "" {
		pt = "post"
	}
	related := pt == "related"
	manual := pt == "by_id"
	switch {
	case pt == "current_query":
		q.CurrentQuery = true
		return q
	case manual:
		q.Ext.AllPublicTypes = true
	case related:
		q.PostTypes = []string{"post"}
		if cur != nil && cur.Type != "" {
			q.PostTypes = []string{cur.Type}
		}
	case pt == "any":
		q.Ext.AllPublicTypes = true
	default:
		q.PostTypes = []string{pt}
	}
	if o := str("order"); o != "" {
		q.OrderBy, q.Order = str("orderby"), o
	}
	q.IgnoreSticky = truthy(get("ignore_sticky_posts"))
	if manual {
		q.IncludeIDs = proDIDs(get("posts_ids"))
		if len(q.IncludeIDs) == 0 {
			q.IncludeIDs = []int64{0}
		}
		q.IgnoreSticky = true
		return q
	}
	// set_post_exclude_args
	exclude := get("exclude")
	if proDHas(exclude, "current_post") && cur != nil {
		q.ExcludeCurrent = true
		q.ExcludeIDs = append(q.ExcludeIDs, cur.ID)
	}
	if proDHas(exclude, "manual_selection") {
		q.ExcludeIDs = append(q.ExcludeIDs, proDIDs(get("exclude_ids"))...)
	}
	if related && cur != nil {
		q.ExcludeIDs = append(q.ExcludeIDs, cur.ID)
	}
	// set_avoid_duplicates
	if str("avoid_duplicates") == "yes" && !e.r.loopState().ignoreAvoid {
		q.ExcludeIDs = append(q.ExcludeIDs, e.r.loopState().Displayed...)
	}
	// set_terms_args
	include := get("include")
	if related {
		if proDHas(include, "terms") && cur != nil && pd.PostTermIDs != nil {
			taxes := asList(get("related_taxonomies"))
			if len(taxes) == 0 && str("related_taxonomies") != "" {
				taxes = []any{str("related_taxonomies")}
			}
			for _, t := range taxes {
				tax := phpString(t)
				q.Ext.Tax = append(q.Ext.Tax, LoopTaxClause{Taxonomy: tax, TermIDs: append([]int64{}, pd.PostTermIDs(cur.ID, tax)...), Op: "IN"})
			}
			if len(taxes) > 0 {
				q.RelatedTo = cur.ID
			}
		}
	} else {
		if proDHas(include, "terms") {
			q.Ext.Tax = append(q.Ext.Tax, e.loopTermsByTaxonomy(proDIDs(get("include_term_ids")), "IN")...)
		}
	}
	if proDHas(exclude, "terms") {
		q.Ext.Tax = append(q.Ext.Tax, e.loopTermsByTaxonomy(proDIDs(get("exclude_term_ids")), "NOT IN")...)
	}
	// set_author_args
	if related {
		if proDHas(include, "authors") && cur != nil && pd.AuthorOf != nil {
			if u := pd.AuthorOf(cur.ID); u != nil {
				q.Ext.RelatedAuthorID = u.ID
				q.IncludeAuthors = []int64{u.ID}
			}
		}
		if f := str("related_fallback"); f == "fallback_recent" || (f == "fallback_by_id" && len(proDIDs(get("fallback_ids"))) > 0) {
			q.Ext.Fallback = f
			q.Ext.FallbackIDs = proDIDs(get("fallback_ids"))
		}
	} else {
		if proDHas(include, "authors") {
			q.IncludeAuthors = proDIDs(get("include_authors"))
		}
		if proDHas(exclude, "authors") && len(q.IncludeAuthors) == 0 {
			q.ExcludeAuthors = proDIDs(get("exclude_authors"))
		}
	}
	// set_date_args
	if d := str("select_date"); d != "" && d != "anytime" {
		q.DateMode = d
		q.DateAfter, q.DateBefore = str("date_after"), str("date_before")
	}
	// fix_query_offset
	q.Offset = toInt(get("offset"))
	return q
}

// loopTermsByTaxonomy mirrors build_terms_query: term ids grouped by taxonomy, one clause each.
func (e *elemCtx) loopTermsByTaxonomy(ids []int64, op string) []LoopTaxClause {
	pd := e.r.pro()
	var order []string
	by := map[string][]int64{}
	for _, id := range ids {
		tax := ""
		if pd.TermByID != nil {
			if t := pd.TermByID(id); t != nil {
				tax = t.Taxonomy
			}
		}
		if tax == "" {
			continue
		}
		if _, ok := by[tax]; !ok {
			order = append(order, tax)
		}
		by[tax] = append(by[tax], id)
	}
	var out []LoopTaxClause
	for _, tax := range order {
		out = append(out, LoopTaxClause{Taxonomy: tax, TermIDs: by[tax], Op: op})
	}
	return out
}

// loopRun runs a widget's query: the loop filter selections narrow it (Module::filter_loop_query)
// and the posts join the avoid list.
func (e *elemCtx) loopRun(q *PostQuery, filtered bool) *PostList {
	pd := e.r.pro()
	st := e.r.loopState()
	if filtered && !q.CurrentQuery {
		if f := st.Filters[e.el.ID]; len(f) > 0 {
			if q.Ext == nil {
				q.Ext = &LoopQueryExt{}
			}
			q.Ext.Tax = append(q.Ext.Tax, e.loopFilterClauses(f)...)
		}
	}
	if pd.Query == nil {
		return &PostList{CurrentPage: 1}
	}
	l := pd.Query(q)
	if l == nil {
		return &PostList{CurrentPage: 1}
	}
	for _, p := range l.Posts {
		if !containsInt64(st.Displayed, p.ID) {
			st.Displayed = append(st.Displayed, p.ID)
		}
	}
	return l
}

func containsInt64(list []int64, v int64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// loopTaxonomyTerms is Taxonomy_Manager::get_taxonomy_terms: the non-empty terms of a taxonomy.
type loopTaxonomyTerms struct {
	bySlug   map[string]*TermItem
	byID     map[int64]*TermItem
	children map[int64]int
}

func (e *elemCtx) loopTaxonomy(tax string) *loopTaxonomyTerms {
	t := &loopTaxonomyTerms{bySlug: map[string]*TermItem{}, byID: map[int64]*TermItem{}, children: map[int64]int{}}
	pd := e.r.pro()
	if pd.Terms == nil {
		return t
	}
	for _, it := range pd.Terms(tax, map[string]any{"hide_empty": true}) {
		if it == nil || it.Count == 0 {
			continue
		}
		if _, ok := t.bySlug[it.Slug]; !ok {
			t.bySlug[it.Slug] = it
		}
		t.byID[it.ID] = it
	}
	for _, it := range t.byID {
		if it.ParentID != 0 {
			t.children[it.ParentID]++
		}
	}
	return t
}

func (t *loopTaxonomyTerms) parentWithChildren(slug string) bool {
	it := t.bySlug[slug]
	return it != nil && t.children[it.ID] > 0
}

func (t *loopTaxonomyTerms) parentWithoutChildren(slug string) bool {
	it := t.bySlug[slug]
	return it != nil && it.ParentID == 0 && t.children[it.ID] == 0
}

var loopJoinOperator = map[string]string{"AND": "AND", "OR": "IN", "NOT": "NOT IN", "DISABLED": "AND"}

// loopFilterClauses mirrors filter_loop_query: one clause per filtered taxonomy.
func (e *elemCtx) loopFilterClauses(filters map[string]*LoopFilter) []LoopTaxClause {
	taxes := make([]string, 0, len(filters))
	for tax := range filters {
		taxes = append(taxes, tax)
	}
	sort.Strings(taxes)
	var out []LoopTaxClause
	for _, tax := range taxes {
		f := filters[tax]
		if f == nil || len(f.Terms) == 0 || f.Terms[0] == "" {
			continue
		}
		join := f.Join
		if join != "AND" && join != "OR" && join != "NOT" {
			join = "DISABLED"
		}
		tt := e.loopTaxonomy(tax)
		if len(f.Terms) == 1 {
			// Single_Terms_Query with one selected term.
			slug := loopSanitizeSlug(f.Terms[0])
			op := loopJoinOperator[join]
			if tt.parentWithChildren(slug) {
				op = "IN"
			}
			out = append(out, LoopTaxClause{Taxonomy: tax, Slugs: []string{slug}, Op: op})
			continue
		}
		// get_hierarchy_of_selected_terms
		var parentsNoChildren []string
		var groupOrder []string
		groups := map[string][]string{}
		addGroup := func(key, slug string) {
			if _, ok := groups[key]; !ok {
				groupOrder = append(groupOrder, key)
			}
			groups[key] = append(groups[key], slug)
		}
		for _, raw := range f.Terms {
			slug := loopSanitizeSlug(raw)
			switch {
			case tt.parentWithoutChildren(slug):
				parentsNoChildren = append(parentsNoChildren, slug)
			case tt.parentWithChildren(slug):
				addGroup(slug, slug)
			default:
				it := tt.bySlug[slug]
				if it == nil {
					continue
				}
				parent := tt.byID[it.ParentID]
				if parent == nil {
					continue
				}
				addGroup(parent.Slug, slug)
			}
		}
		var parts []LoopTaxClause
		if len(parentsNoChildren) > 0 {
			parts = append(parts, LoopTaxClause{Taxonomy: tax, Slugs: parentsNoChildren, Op: loopJoinOperator[join]})
		}
		if join == "AND" || join == "OR" {
			for _, parent := range groupOrder {
				terms := groups[parent]
				hasParent := false
				for _, s := range terms {
					if s == parent {
						hasParent = true
					}
				}
				if hasParent && len(terms) > 1 {
					if join == "AND" { // drop the parent term
						var rest []string
						for _, s := range terms {
							if s != parent {
								rest = append(rest, s)
							}
						}
						terms = rest
					} else { // drop the child terms
						terms = []string{parent}
					}
				}
				g := LoopTaxClause{}
				for _, s := range terms {
					g.Or = append(g.Or, LoopTaxClause{Taxonomy: tax, Slugs: []string{s}, Op: "IN"})
				}
				if len(g.Or) > 0 {
					parts = append(parts, g)
				}
			}
		}
		switch {
		case len(parts) == 0:
		case join == "OR":
			out = append(out, LoopTaxClause{Or: parts})
		default:
			out = append(out, parts...)
		}
	}
	return out
}

// loopSanitizeSlug approximates urldecode( sanitize_title( $term ) ) for filter slugs.
func loopSanitizeSlug(s string) string {
	if u, err := url.QueryUnescape(s); err == nil {
		s = u
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// loopKV is one query argument, in request order.
type loopKV struct{ K, V string }

// loopParseQuery mirrors wp_parse_str: arguments in order of first appearance, the last value
// winning.
func loopParseQuery(raw string) []loopKV {
	var out []loopKV
	idx := map[string]int{}
	for _, part := range strings.Split(raw, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		if uk, err := url.QueryUnescape(k); err == nil {
			k = uk
		}
		if uv, err := url.QueryUnescape(v); err == nil {
			v = uv
		}
		if i, ok := idx[k]; ok {
			out[i].V = v
			continue
		}
		idx[k] = len(out)
		out = append(out, loopKV{k, v})
	}
	return out
}

// phpURLEncode is PHP's urlencode.
func phpURLEncode(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "~", "%7E")
}

// loopArgs renders arguments as "&k=v" pairs; encode urlencodes keys and values.
func loopArgs(kvs []loopKV, encode bool, keep func(string) bool) string {
	var b strings.Builder
	for _, kv := range kvs {
		if keep != nil && !keep(kv.K) {
			continue
		}
		if encode {
			b.WriteString("&" + phpURLEncode(kv.K) + "=" + phpURLEncode(kv.V))
		} else {
			b.WriteString("&" + kv.K + "=" + kv.V)
		}
	}
	return b.String()
}

// loopAddQueryArgs mirrors add_query_arg( $args, $url ) for already encoded values: existing
// keys are replaced in place, new ones appended.
func loopAddQueryArgs(u string, kvs []loopKV) string {
	if len(kvs) == 0 {
		return u
	}
	base, frag, _ := strings.Cut(u, "#")
	path, query, _ := strings.Cut(base, "?")
	cur := loopParseQueryRaw(query)
	for _, kv := range kvs {
		found := false
		for i := range cur {
			if cur[i].K == kv.K {
				cur[i].V, found = kv.V, true
			}
		}
		if !found {
			cur = append(cur, kv)
		}
	}
	var parts []string
	for _, kv := range cur {
		parts = append(parts, kv.K+"="+kv.V)
	}
	out := path + "?" + strings.Join(parts, "&")
	if frag != "" {
		out += "#" + frag
	}
	return out
}

// loopParseQueryRaw splits a query string without decoding it.
func loopParseQueryRaw(raw string) []loopKV {
	var out []loopKV
	for _, part := range strings.Split(raw, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		out = append(out, loopKV{k, v})
	}
	return out
}

// loopConcat mirrors format_query_string_concatenation: the first "&" becomes "?" when the URL
// has no query yet.
func loopConcat(u string) string {
	if !strings.Contains(u, "?") {
		u = strings.Replace(u, "&", "?", 1)
	}
	return u
}
