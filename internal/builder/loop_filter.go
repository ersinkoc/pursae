package builder

// GoWebEditor Pro Taxonomy Filter widget: a port of Taxonomy_Filter::render and
// Taxonomy_Filter_Trait (modules/loop-filter). The terms are those of the filtered loop's posts
// (unless empty items show), minus terms the loop's query control leaves out; the selections
// come from the e-filter-<loop id>-<taxonomy> query string (LoopState.Filters).

import (
	"sort"
	"strconv"
	"strings"
)

func proDRenderTaxonomyFilter(e *elemCtx) string {
	sel, tax := e.s("selected_element"), e.s("taxonomy")
	terms := e.loopFilterTerms(sel, tax)
	switch {
	case sel == "":
		return e.proPlaceholder("Select a loop widget to filter.")
	case tax == "":
		return e.proPlaceholder("Select a taxonomy.")
	case len(terms) == 0:
		return e.proPlaceholder("No terms found.")
	}
	st := e.r.loopState()
	active := map[string]bool{}
	if f := st.Filters[sel][tax]; f != nil {
		for _, t := range f.Terms {
			active[t] = true
		}
	}
	page := 1
	pd := e.r.pro()
	if pd.Request != nil {
		for _, k := range []string{"paged", "page"} {
			if n, _ := strconv.Atoi(pd.Request("get", k)); n > page {
				page = n
			}
		}
	}
	if a := e.archiveAny(); a != nil && a.Page > page {
		page = a.Page
	}
	loop := e.loopFilterTarget(sel)
	a := e.attr("filter-bar")
	a.add("class", "e-filter").add("role", "search").add("data-base-url", e.proDBaseURL()).add("data-page-num", strconv.Itoa(page))
	var b strings.Builder
	b.WriteString("<search " + a.String() + ">\n")
	limit := toInt(e.get("number_of_taxonomies"))
	activeN, total := 0, 0
	for _, t := range terms {
		total++
		if loop != nil && loop.loopFilterExcludesTerm(t, tax) {
			continue
		}
		pressed := "false"
		if active[t.Slug] {
			pressed = "true"
			activeN++
		}
		if limit > 0 && total > limit {
			continue
		}
		b.WriteString(`<button class="e-filter-item" data-filter="` + escAttr(t.Slug) + `" aria-pressed="` + pressed + `">` + t.Name + "</button>\n")
	}
	if e.s("show_first_item") == "yes" {
		pressed := "false"
		if activeN == 0 {
			pressed = "true"
		}
		b.WriteString(`<button class="e-filter-item" data-filter="__all" aria-pressed="` + pressed + `">` + "\n" + e.s("first_item_title") + "</button>\n")
	}
	b.WriteString("</search>\n")
	return b.String()
}

// loopFilterTarget is the filtered loop widget of the current document.
func (e *elemCtx) loopFilterTarget(sel string) *elemCtx {
	if sel == "" || e.r.doc == nil {
		return nil
	}
	el := FindElement(e.r.doc.Elements, sel)
	if el == nil || el.ElType != "widget" {
		return nil
	}
	return e.r.newElemCtx(el)
}

// loopFilterTerms mirrors get_filtered_taxonomies: get_terms ordered by name, top level only
// unless child terms show (then flattened depth first to child_taxonomy_depth), limited to the
// loop's posts when empty items hide.
func (e *elemCtx) loopFilterTerms(sel, tax string) []*TermItem {
	pd := e.r.pro()
	if tax == "" || pd.Terms == nil {
		return nil
	}
	hideEmpty := e.s("show_empty_items") != "yes"
	children := e.s("show_child_taxonomy") == "yes"
	all := pd.Terms(tax, map[string]any{"hide_empty": hideEmpty})
	var objects map[int64]bool
	if hideEmpty {
		if loop := e.loopFilterTarget(sel); loop != nil && pd.PostTermIDs != nil {
			posts := loop.loopFilterPosts()
			if len(posts) > 0 { // get_terms ignores an empty object_ids
				objects = map[int64]bool{}
			}
			for _, p := range posts {
				for _, id := range pd.PostTermIDs(p.ID, tax) {
					objects[id] = true
				}
			}
		}
	}
	var terms []*TermItem
	for _, t := range all {
		if t == nil || (hideEmpty && t.Count == 0) || (!children && t.ParentID != 0) {
			continue
		}
		if objects != nil && !objects[t.ID] {
			continue
		}
		terms = append(terms, t)
	}
	sort.SliceStable(terms, func(i, j int) bool { return terms[i].Name < terms[j].Name })
	if !children {
		return terms
	}
	depth := toInt(e.get("child_taxonomy_depth"))
	byID := map[int64]*TermItem{}
	for _, t := range terms {
		byID[t.ID] = t
	}
	levelOf := func(t *TermItem) int {
		d := 0
		for p := t; p != nil && p.ParentID != 0; p = byID[p.ParentID] {
			d++
			if d > 100 {
				break
			}
		}
		return d
	}
	kids := map[int64][]*TermItem{}
	for _, t := range terms {
		if t.ParentID == 0 || levelOf(t) <= depth {
			kids[t.ParentID] = append(kids[t.ParentID], t)
		}
	}
	var flat []*TermItem
	var walk func(parent int64)
	walk = func(parent int64) {
		for _, t := range kids[parent] {
			flat = append(flat, t)
			walk(t.ID)
		}
	}
	walk(0)
	if len(flat) == 0 {
		return terms
	}
	return flat
}

// loopFilterPosts mirrors get_elementor_post_query: every post of the loop's own query (its
// include/exclude terms, no filter selections, no avoid list).
func (e *elemCtx) loopFilterPosts() []*PostData {
	st := e.r.loopState()
	st.ignoreAvoid = true
	q := e.loopQuery("post_query_", -1, 1)
	st.ignoreAvoid = false
	saved := append([]int64(nil), st.Displayed...)
	l := e.loopRun(q, false)
	st.Displayed = saved
	return l.Posts
}

// loopFilterExcludesTerm mirrors is_term_excluded_by_query_control for the loop widget e.
func (e *elemCtx) loopFilterExcludesTerm(t *TermItem, tax string) bool {
	prefix := "post_query_"
	if sk := phpString(e.raw("_skin")); sk != "" && sk != "post" {
		prefix = sk + "_query_"
	}
	get := func(k string) any { return e.raw(prefix + k) }
	pd := e.r.pro()
	include, exclude := get("include"), get("exclude")
	incIDs, excIDs := proDIDs(get("include_term_ids")), proDIDs(get("exclude_term_ids"))
	sameTax := false
	if pd.TermByID != nil {
		for _, id := range append(append([]int64{}, incIDs...), excIDs...) {
			if x := pd.TermByID(id); x != nil && x.Taxonomy == t.Taxonomy {
				sameTax = true
				break
			}
		}
	}
	if sameTax && proDHas(include, "terms") && len(incIDs) > 0 && !containsInt64(incIDs, t.ID) {
		return true
	}
	if proDHas(exclude, "terms") && containsInt64(excIDs, t.ID) {
		return true
	}
	// should_exclude_term_by_manual_selection: every post of the term is excluded by hand.
	if ids := proDIDs(get("exclude_ids")); proDHas(exclude, "manual_selection") && len(ids) > 0 && pd.PostTermIDs != nil {
		n := 0
		for _, id := range ids {
			if containsInt64(pd.PostTermIDs(id, tax), t.ID) {
				n++
			}
		}
		if n > 0 && n >= t.Count {
			return true
		}
	}
	return false
}
