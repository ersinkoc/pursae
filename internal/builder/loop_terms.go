package builder

// Golementor Pro term loops: the Loop Grid / Loop Carousel "Post Taxonomy" skin
// (modules/loop-builder: Skin_Loop_Taxonomy_Base, Taxonomy_Loop_Provider and the loop filter's
// Taxonomy_Filter_Trait / Hierarchical_Taxonomy_Trait) and the atomic Loop's "taxonomy" template
// type (modules/collection-loop: Taxonomy_Template_Type_Base, Hierarchical_Term_Depth,
// Taxonomy_Avoid_List). Terms come from get_terms (loopGetTerms below); each term renders the loop
// item template while it is the loop term ($wp_query->loop_term), which the archive dynamic tags
// read (loopTermTag).

import (
	"sort"
	"strconv"
	"strings"
)

// loopTermArgs are the get_terms arguments the term loops use (WP_Term_Query).
type loopTermArgs struct {
	Taxonomy   string
	HideEmpty  bool
	Include    []int64 // non-empty: only these (exclude is then ignored)
	Exclude    []int64
	TTIDs      []int64 // term_taxonomy_id
	ChildOf    int64
	OrderBy    string // name (default) or term_id
	Order      string // ASC or DESC (default ASC, as get_terms)
	Offset     int
	Number     int // 0: all
	includeSet bool
}

// loopGetTerms mirrors get_terms() for the arguments above: the SQL filters and order, child_of
// (_get_term_children), hide_empty keeping empty terms with non-empty descendants (hierarchical
// queries) and the number/offset window.
func (r *renderer) loopGetTerms(a loopTermArgs) []*TermItem {
	pd := r.pro()
	if pd.Terms == nil || a.Taxonomy == "" {
		return nil
	}
	all := pd.Terms(a.Taxonomy, map[string]any{"hide_empty": false})
	byID := map[int64]*TermItem{}
	children := map[int64][]int64{}
	for _, t := range all {
		if t == nil {
			continue
		}
		byID[t.ID] = t
	}
	for _, t := range all {
		if t != nil && t.ParentID != 0 && byID[t.ParentID] != nil {
			children[t.ParentID] = append(children[t.ParentID], t.ID)
		}
	}
	hierarchical := len(children) > 0
	if a.ChildOf != 0 && len(children[a.ChildOf]) == 0 {
		return nil // the queried child_of term has no descendants
	}
	var list []*TermItem
	for _, t := range all {
		if t == nil {
			continue
		}
		if a.includeSet || len(a.Include) > 0 {
			if !containsInt64(a.Include, t.ID) {
				continue
			}
		} else if containsInt64(a.Exclude, t.ID) {
			continue
		}
		if len(a.TTIDs) > 0 && !containsInt64(a.TTIDs, t.ID) {
			continue
		}
		if a.HideEmpty && !hierarchical && t.Count == 0 {
			continue
		}
		list = append(list, t)
	}
	desc := strings.EqualFold(a.Order, "desc")
	byName := a.OrderBy != "term_id" && a.OrderBy != "id"
	sort.SliceStable(list, func(i, j int) bool {
		x, y := list[i], list[j]
		if byName {
			nx, ny := strings.ToLower(x.Name), strings.ToLower(y.Name)
			if nx != ny {
				if desc {
					return nx > ny
				}
				return nx < ny
			}
		}
		if desc {
			return x.ID > y.ID
		}
		return x.ID < y.ID
	})
	if a.ChildOf != 0 {
		// _get_term_children: the descendants in query order, each followed by its own.
		var out []*TermItem
		seen := map[int64]bool{a.ChildOf: true}
		var walk func(parent int64)
		walk = func(parent int64) {
			for _, t := range list {
				if seen[t.ID] || t.ParentID != parent {
					continue
				}
				out = append(out, t)
				if len(children[t.ID]) > 0 {
					seen[t.ID] = true
					walk(t.ID)
				}
			}
		}
		walk(a.ChildOf)
		list = out
	}
	if a.HideEmpty && hierarchical {
		var keep []*TermItem
		for _, t := range list {
			if t.Count > 0 || loopTermHasCount(t.ID, children, byID) {
				keep = append(keep, t)
			}
		}
		list = keep
	}
	if a.Number > 0 {
		if a.Offset >= len(list) {
			return nil
		}
		list = list[a.Offset:]
		if len(list) > a.Number {
			list = list[:a.Number]
		}
	}
	return list
}

func loopTermHasCount(id int64, children map[int64][]int64, byID map[int64]*TermItem) bool {
	for _, c := range children[id] {
		if t := byID[c]; t != nil && (t.Count > 0 || loopTermHasCount(c, children, byID)) {
			return true
		}
	}
	return false
}

// loopTermDepth is the term's depth in the taxonomy tree (count(get_ancestors)).
func (r *renderer) loopTermDepth(t *TermItem) int {
	pd := r.pro()
	d := 0
	seen := map[int64]bool{t.ID: true}
	for p := t.ParentID; p != 0 && !seen[p]; d++ {
		seen[p] = true
		if pd.TermByID == nil {
			return d + 1
		}
		pt := pd.TermByID(p)
		if pt == nil {
			return d + 1
		}
		p = pt.ParentID
	}
	return d
}

// --- the loop term (dynamic tags) ---------------------------------------------------------------

// loopTermEnter makes t the loop term until the returned function restores the previous one.
func (r *renderer) loopTermEnter(t *TermItem) func() {
	st := r.loopState()
	prev, prevTax := st.Term, st.InTaxonomy
	st.Term, st.InTaxonomy = t, true
	return func() { st.Term, st.InTaxonomy = prev, prevTax }
}

// loopTermTag renders an archive dynamic tag inside a term loop (Tag_Trait::
// render_taxonomy_content_by_key, Utils::get_the_archive_url, Archive_Meta); ok is false outside
// one.
func (t *tagCtx) loopTermTag(key string) (any, bool) {
	if t.r.rc.Pro == nil || t.r.rc.Pro.Loop == nil {
		return nil, false
	}
	st := t.r.rc.Pro.Loop
	if !st.InTaxonomy {
		return nil, false
	}
	term := st.Term
	if term == nil {
		if key == "title" || key == "description" { // is_loop_taxonomy() without a loop term
			return "", true
		}
		return nil, false
	}
	switch key {
	case "title":
		return ksesPost(term.Name), true
	case "description":
		return ksesPost(term.Description), true
	case "url":
		return term.URL, true
	case "meta":
		k := t.str("key")
		if k == "" {
			return "", true
		}
		return ksesPost(phpString(term.Meta[k])), true
	}
	return nil, false
}

// --- Loop Grid / Loop Carousel: the Post Taxonomy skin --------------------------------------------

// loopTermSkinArgs is Taxonomy_Loop_Provider::get_query_settings with
// Taxonomy_Filter_Trait::get_filtered_taxonomies' arguments.
func (e *elemCtx) loopTermSkinTerms() []*TermItem {
	const p = "post_taxonomy_"
	st := e.r.loopState()
	tax := e.s(p + "query_post_type")
	if tax == "" {
		tax = "category"
	}
	a := loopTermArgs{Taxonomy: tax, HideEmpty: e.s(p+"hide_empty") == "yes"}
	if v := e.get(p + "posts_ids"); v != nil {
		a.Include, a.includeSet = proDIDs(v), len(proDIDs(v)) > 0
	}
	if v := e.get(p + "exclude_ids"); v != nil {
		a.Exclude = proDIDs(v)
	}
	if v := e.s(p + "term_taxonomy_id"); v != "" {
		for _, s := range strings.Split(v, ",") {
			if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && n > 0 {
				a.TTIDs = append(a.TTIDs, n)
			}
		}
		if len(a.TTIDs) == 0 {
			a.TTIDs = []int64{0}
		}
	}
	a.ChildOf = toInt64(e.get(p + "child_of"))
	a.Offset = max(0, toInt(e.get(p+"offset")))
	a.Number = max(0, toInt(e.get("posts_per_page")))
	a.OrderBy, a.Order = e.s(p+"orderby"), e.s(p+"order")
	if a.OrderBy == "" {
		a.OrderBy = "name"
	}
	if e.s(p+"avoid_duplicates") == "yes" {
		a.Exclude = append(a.Exclude, st.TermsDisplayed...)
	}
	terms := e.r.loopGetTerms(a)
	if depth := toInt(e.get(p + "child_taxonomy_depth")); depth > 0 && e.s(p+"hierarchical") == "yes" {
		terms = loopTermsByDepth(terms, depth-1)
	}
	for _, t := range terms {
		if !containsInt64(st.TermsDisplayed, t.ID) {
			st.TermsDisplayed = append(st.TermsDisplayed, t.ID)
		}
	}
	return terms
}

// loopTermsByDepth mirrors filter_child_terms_by_depth + transform_taxonomy_hierarchy_to_plain:
// terms down to target depth (depth counted within the list), regrouped parent first.
func loopTermsByDepth(terms []*TermItem, target int) []*TermItem {
	byID := map[int64]*TermItem{}
	for _, t := range terms {
		byID[t.ID] = t
	}
	depthOf := func(t *TermItem) int {
		d := 0
		seen := map[int64]bool{}
		for cur := t; cur.ParentID != 0; {
			d++
			p := byID[cur.ParentID]
			if p == nil || seen[p.ID] {
				return 1 << 30 // calculate_depth_for_child_term returns null: never kept
			}
			seen[p.ID] = true
			cur = p
		}
		return d
	}
	groups := map[int64][]*TermItem{}
	for _, t := range terms {
		if t.ParentID == 0 || depthOf(t) <= target {
			groups[t.ParentID] = append(groups[t.ParentID], t)
		}
	}
	var out []*TermItem
	var walk func(parent int64)
	walk = func(parent int64) {
		for _, t := range groups[parent] {
			out = append(out, t)
			walk(t.ID)
		}
	}
	walk(0)
	if len(out) == 0 {
		return terms
	}
	return out
}

// loopTermList is proDLoopList for the Post Taxonomy skin (Skin_Loop_Taxonomy_Base::render): every
// term renders the loop item template; there is no pagination and no "nothing found" message.
func (e *elemCtx) loopTermList(slide bool) (*PostList, string, bool, string) {
	tpl := toInt64(e.s("template_id"))
	if tpl == 0 {
		if e.editing() {
			return nil, "", false, `<div class="e-loop-empty-view__wrapper"><!-- Will be filled with JS --></div>` + "\n"
		}
		return nil, "", false, ""
	}
	terms := e.loopTermSkinTerms()
	st := e.r.loopState()
	if st.termEmpty == nil {
		st.termEmpty = map[string]bool{}
	}
	st.termEmpty[e.el.ID] = len(terms) == 0
	var b strings.Builder
	for _, t := range terms {
		restore := e.r.loopTermEnter(t)
		b.WriteString(e.loopDynamicCSS(tpl, e.loopTermPost(t)))
		b.WriteString(e.loopTermItem(tpl, t, e.loopCSSOnce(tpl), slide))
		restore()
	}
	return &PostList{Total: len(terms), Pages: 1, CurrentPage: 1}, b.String(), true, ""
}

// loopGridClass is the loop container's grid class: Skin_Base::render_loop_header adds it when the
// query found posts or a term loop has terms (is_loop_taxonomy).
func (e *elemCtx) loopGridClass() []string {
	if st := e.r.pro().Loop; st != nil && st.termEmpty[e.el.ID] && e.s("_skin") == "post_taxonomy" {
		return nil
	}
	return []string{"elementor-grid"}
}

// loopTermPost is the current post with the term's id: the loop item CSS and wrapper use the term
// id (get_data_id_from_taxonomy_loop_query) while the global post stays the page's.
func (e *elemCtx) loopTermPost(t *TermItem) *PostData {
	p := PostData{}
	if cur := e.currentPostAny(); cur != nil {
		p = *cur
	}
	p.ID, p.Class = t.ID, ""
	return &p
}

// loopTermItem is Skin_Loop_Taxonomy_Base::render_post: the loop item document with the
// wrapper of Loop document get_container_attributes (e-loop-item-<term id> and get_post_class of
// the post with that id, an upstream quirk kept for parity).
func (e *elemCtx) loopTermItem(tplID int64, t *TermItem, withCSS, slide bool) string {
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
	cls := "elementor elementor-" + itoa64(doc.ID)
	if slide {
		cls += " swiper-slide"
	}
	cls += " e-loop-item e-loop-item-" + itoa64(t.ID) + " "
	if pd.LoopItemClasses != nil {
		cls += pd.LoopItemClasses(t.ID, "")
	}
	b.WriteString(e.proDRenderDocument(doc, e.currentPostAny(), func(doc *Document) string {
		s := `<div data-elementor-type="loop-item" data-elementor-id="` + itoa64(doc.ID) + `" class="` + escAttr(cls) + `" data-elementor-post-type="elementor_library"`
		if slide {
			s += ` role="group" aria-roledescription="slide"`
		}
		return s + ` data-custom-edit-handle="1">` + "\n"
	}))
	return b.String()
}

// --- atomic Loop: the "taxonomy" template type ------------------------------------------------------

// loopAtomicTerms is Taxonomy_Template_Type_Base::build_item_provider for the post taxonomy type
// (prop prefix "taxonomy").
func (r *renderer) loopAtomicTerms(s map[string]any) []*TermItem {
	const p = "taxonomy_"
	str := func(k string) string { return phpString(s[p+k]) }
	st := r.loopState()
	a := loopTermArgs{Taxonomy: str("source"), HideEmpty: truthy(s[p+"hide_empty"]), OrderBy: str("orderby"), Order: strings.ToUpper(str("order"))}
	if a.Taxonomy == "" {
		a.Taxonomy = "category"
	}
	if a.OrderBy == "" {
		a.OrderBy = "name"
	}
	if a.Order == "" {
		a.Order = "DESC"
	}
	a.Number = 3
	if v, ok := s["posts_per_page"]; ok && v != nil {
		a.Number = min(100, max(1, toInt(v)))
	}
	if off := toInt(s[p+"offset"]); off > 0 {
		a.Offset = off
	}
	if str("filter_by") == "manual_selection" {
		a.Include, a.includeSet = proDIDs(s[p+"include"]), true
		if len(a.Include) == 0 {
			a.Include = []int64{0}
		}
	}
	a.Exclude = proDIDs(s[p+"exclude"])
	if truthy(s[p+"avoid_duplicates"]) {
		for _, id := range st.AtomicTermsDisplayed {
			if !containsInt64(a.Exclude, id) {
				a.Exclude = append(a.Exclude, id)
			}
		}
	}
	depth := toInt(s[p+"depth"])
	filtered := truthy(s[p+"hierarchical"]) && depth > 0
	offset, number := a.Offset, a.Number
	if filtered {
		a.Offset, a.Number = 0, 0
	}
	terms := r.loopGetTerms(a)
	if filtered {
		var keep []*TermItem
		for _, t := range terms {
			if r.loopTermDepth(t) <= depth-1 {
				keep = append(keep, t)
			}
		}
		terms = keep
		if offset >= len(terms) {
			terms = nil
		} else {
			terms = terms[offset:]
		}
		if number > 0 && len(terms) > number {
			terms = terms[:number]
		}
	}
	for _, t := range terms {
		st.AtomicTermsDisplayed = append(st.AtomicTermsDisplayed, t.ID)
	}
	return terms
}

// loopHomeTitle is Page_Title::render on the blog home (is_home()): empty unless "show home title"
// is on, then Utils::get_page_title's wp_get_document_title().
func loopHomeTitle(a *ArchiveData, showHome bool) string {
	if !showHome {
		return ""
	}
	if a.HomeTitle != "" {
		return a.HomeTitle
	}
	return a.Title
}
