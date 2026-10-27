package builder

// GoWebEditor Pro atomic Loop (atomic/loop.go): the site side. Post_Template_Type::build_query_args
// maps the loop's query settings onto a PostQuery, items become the current post while they render,
// and dynamic tags render through the v3 tag implementations (dynamic_tags.go).

import (
	"html"
	"math"
	"net/url"
	"strconv"
	"strings"

	"wpgo/internal/builder/atomic"
)

// atomicLoopHooks wires the atomic Loop to this render.
func (r *renderer) atomicLoopHooks() *atomic.LoopHooks {
	var current *PostData
	posts := map[string]*PostData{}
	termsOf := map[string]map[string]*TermItem{} // term loops: loop id -> item id -> term
	pd := r.pro()
	return &atomic.LoopHooks{
		Query: func(q atomic.LoopQuery) atomic.LoopResult {
			st := r.loopState()
			if phpString(q.Settings["template_type"]) == "taxonomy" { // gap-loops2: term loops (loop_terms.go)
				terms := r.loopAtomicTerms(q.Settings)
				res := atomic.LoopResult{PerPage: len(terms), Found: len(terms), MaxPages: 1, Terms: true}
				if res.Found == 0 {
					res.MaxPages = 0
				}
				m := map[string]*TermItem{}
				for _, t := range terms {
					id := strconv.FormatInt(t.ID, 10)
					m[id] = t
					res.Items = append(res.Items, id)
				}
				termsOf[q.LoopID] = m
				return res
			}
			delete(termsOf, q.LoopID)
			pq := r.loopAtomicQuery(q)
			var list *PostList
			if pd.Query != nil {
				list = pd.Query(pq)
			}
			if list == nil {
				list = &PostList{}
			}
			res := atomic.LoopResult{PerPage: pq.PerPage, Found: list.Total + pq.Offset, MaxPages: list.Pages}
			if pq.Offset > 0 && pq.PerPage > 0 {
				res.MaxPages = int(math.Ceil(float64(res.Found) / float64(pq.PerPage)))
			}
			for _, p := range list.Posts {
				id := strconv.FormatInt(p.ID, 10)
				posts[id] = p
				res.Items = append(res.Items, id)
				if !containsInt64(st.Displayed, p.ID) {
					st.Displayed = append(st.Displayed, p.ID)
				}
			}
			return res
		},
		Enter: func(loopID, id string) func() {
			st := r.loopState()
			if m, ok := termsOf[loopID]; ok { // a term loop: the item is the loop term, the post stays
				restore := r.loopTermEnter(m[id])
				prevItem := st.atomicItem
				st.atomicItem = true
				return func() { restore(); st.atomicItem = prevItem }
			}
			prev, prevPost, prevItem := current, st.atomicPost, st.atomicItem
			current = posts[id]
			if current == nil && pd.PostByID != nil {
				n, _ := strconv.ParseInt(id, 10, 64)
				current = pd.PostByID(n)
			}
			st.atomicPost, st.atomicItem = current, true
			return func() { current, st.atomicPost, st.atomicItem = prev, prevPost, prevItem }
		},
		Dynamic: func(name string, settings map[string]any) any {
			rr := r
			if current != nil {
				rc := *r.rc
				th := ThemeData{}
				if rc.Theme != nil {
					th = *rc.Theme
				}
				p := current
				th.Post = func() *PostData { return p }
				rc.Theme = &th
				rr = newRenderer(r.doc, &rc)
			}
			return rr.renderTag(&dynamicTag{Name: name, Settings: settings})
		},
		CurrentPage: func(loopID string) int {
			if pd.Request == nil {
				return 1
			}
			n, _ := strconv.Atoi(pd.Request("get", "e-page-"+loopID))
			return max(1, n)
		},
		PageURL: func(loopID string, page int) string {
			key := "e-page-" + loopID
			base := pd.CurrentURL
			if u, err := url.Parse(base); err == nil {
				base = u.EscapedPath()
				if base == "" {
					base = "/"
				}
				var keep []string
				for _, kv := range loopParseQueryRaw(u.RawQuery) {
					if k, _ := url.QueryUnescape(kv.K); k != key {
						keep = append(keep, kv.K+"="+kv.V)
					}
				}
				if len(keep) > 0 {
					base += "?" + strings.Join(keep, "&")
				}
			}
			if page <= 1 {
				return base
			}
			sep := "?"
			if strings.Contains(base, "?") {
				sep = "&"
			}
			return base + sep + key + "=" + strconv.Itoa(page)
		},
	}
}

// loopAtomicQuery is Post_Template_Type::build_query_args (plus the page window).
func (r *renderer) loopAtomicQuery(q atomic.LoopQuery) *PostQuery {
	s := q.Settings
	str := func(k string) string { return phpString(s[k]) }
	per := 3
	if v, ok := s["posts_per_page"]; ok && v != nil {
		per = toInt(v)
	}
	per = min(100, max(1, per))
	pq := &PostQuery{PerPage: per, Page: 1, Ext: &LoopQueryExt{}}
	if q.Offset > 0 {
		pq.Offset = q.Offset
	} else if q.Page > 1 {
		pq.Page = q.Page
	}
	if tt := str("template_type"); tt != "" && tt != "post" {
		pq.IncludeIDs = []int64{0} // term loops do not run a post query (loopAtomicTerms)
		return pq
	}
	source := str("source")
	if source == "" {
		source = "post"
	}
	if source == "current_query" {
		pq.CurrentQuery = true
		return pq
	}
	pq.OrderBy, pq.Order = str("orderby"), str("order")
	if pq.OrderBy == "" {
		pq.OrderBy = "post_date"
	}
	if pq.Order == "" {
		pq.Order = "desc"
	}
	if source == "manual" {
		pq.Ext.AllPublicTypes = true
		ids := proDIDs(s["selection"])
		if len(ids) > per {
			ids = ids[:per]
		}
		if len(ids) == 0 {
			ids = []int64{0}
		}
		pq.IncludeIDs, pq.IgnoreSticky = ids, true
		return pq
	}
	pq.PostTypes = []string{source}
	switch d := str("select_date"); d {
	case "", "anytime":
	case "exact":
		pq.DateMode = "exact"
		pq.DateAfter, pq.DateBefore = str("date_after"), str("date_before")
	default:
		pq.DateMode = d
	}
	pq.IgnoreSticky = true
	if source == "post" {
		if v, ok := s["ignore_sticky_posts"]; ok && v != nil {
			pq.IgnoreSticky = truthy(v)
		}
	}
	e := &elemCtx{r: r}
	for _, mode := range []string{"include", "exclude"} {
		for _, f := range asList(s[mode+"_filters"]) {
			m := asMap(f)
			if m == nil {
				continue
			}
			ids := proDIDs(m["values"])
			switch phpString(m["key"]) {
			case "terms":
				op := "IN"
				if mode == "exclude" {
					op = "NOT IN"
				}
				pq.Ext.Tax = append(pq.Ext.Tax, e.loopTermsByTaxonomy(ids, op)...)
			case "authors":
				if mode == "include" {
					pq.IncludeAuthors = append(pq.IncludeAuthors, ids...)
				} else if len(pq.IncludeAuthors) == 0 {
					pq.ExcludeAuthors = append(pq.ExcludeAuthors, ids...)
				}
			case "manual_selection":
				if mode == "exclude" {
					pq.ExcludeIDs = append(pq.ExcludeIDs, ids...)
				}
			case "current_post":
				if mode == "exclude" {
					if th := r.rc.Theme; th != nil && th.Post != nil {
						if p := th.Post(); p != nil {
							pq.ExcludeIDs = append(pq.ExcludeIDs, p.ID)
						}
					}
				}
			}
		}
	}
	return pq
}

// LoopPreviewItems answers the editor's loop preview (Loop_Preview::get_preview): the items of
// an atomic Loop's resolved query settings ({id, title}).
func LoopPreviewItems(settings map[string]any, rc *RenderContext) []map[string]any {
	r := newRenderer(&Document{}, rc)
	pd := r.pro()
	out := []map[string]any{}
	if pd.Query == nil {
		return out
	}
	if s, ok := settings["settings"].(map[string]any); ok { // Loop_Query_Transformer shape
		settings = s
	}
	if phpString(settings["template_type"]) == "taxonomy" { // gap-loops2: Term_Loop_Item_Provider::items
		for _, t := range r.loopAtomicTerms(settings) {
			out = append(out, map[string]any{"id": t.ID, "title": html.UnescapeString(t.Name)})
		}
		return out
	}
	l := pd.Query(r.loopAtomicQuery(atomic.LoopQuery{Settings: settings}))
	if l == nil {
		return out
	}
	for _, p := range l.Posts {
		out = append(out, map[string]any{"id": p.ID, "title": p.Title})
	}
	return out
}

// AtomicLoopPaginationScript is the atomic Loop's pagination handler script handle.
const AtomicLoopPaginationScript = atomic.LoopPaginationScript

// loopRenderChild prints a non-atomic element inside an atomic Loop item: its dynamic tags resolve
// for the item (the doc-wide pass leaves loop subtrees alone) and it takes the item's formatted
// element ids. ok is false outside loop items.
func (r *renderer) loopRenderChild(n *atomic.Node) (string, bool) {
	el, isEl := n.Ref.(*Element)
	st := r.rc.Pro
	if !isEl || el == nil || st == nil || st.Loop == nil || !st.Loop.atomicItem {
		return "", false
	}
	c := loopCloneFromNode(n)
	if c == nil {
		return "", false
	}
	saved := r.rc
	if p := st.Loop.atomicPost; p != nil {
		rc := *r.rc
		th := ThemeData{}
		if rc.Theme != nil {
			th = *rc.Theme
		}
		th.Post = func() *PostData { return p }
		rc.Theme = &th
		r.rc = &rc
	}
	defer func() { r.rc = saved }()
	docID := int64(0)
	if r.doc != nil {
		docID = r.doc.ID
	}
	r.resolveDynamic(&Document{ID: docID, Elements: []*Element{c}})
	return r.printElement(c), true
}

// loopCloneFromNode copies the element tree behind an atomic node with the node's (formatted) ids.
func loopCloneFromNode(n *atomic.Node) *Element {
	el, ok := n.Ref.(*Element)
	if !ok || el == nil {
		return nil
	}
	c := *el
	c.ID = n.ID
	if m, ok := deepCopy(el.Settings).(map[string]any); ok && el.Settings != nil {
		c.Settings = m
	}
	c.settingsKeys = append([]string(nil), el.settingsKeys...)
	if el.nestedKeys != nil {
		c.nestedKeys = make(map[string][]string, len(el.nestedKeys))
		for k, v := range el.nestedKeys {
			c.nestedKeys[k] = append([]string(nil), v...)
		}
	}
	c.Elements = nil
	for _, ch := range n.Children {
		if cc := loopCloneFromNode(ch); cc != nil {
			c.Elements = append(c.Elements, cc)
		}
	}
	return &c
}
