package atomic

// GoWebEditor Pro atomic Loop (e-collection-loop): a port of upstream Pro 4.3's
// modules/collection-loop. The loop runs its query (Env.Loop.Query, the site's posts), publishes
// the "collection-loop" render context (Has_Loop_Query, Collection_Loop::extend_loop_render_context)
// and the layout renders its item template once per post (Has_Loop_Iteration) with alternate
// items (Alternate_Selector: every Nth / once / static slots) and per-item element ids
// (Format_Element_Ids with [loop id, item id, slot]). Pagination buttons and the empty state read
// the same context. Dynamic tags ($$type "dynamic") resolve through Env.Loop.Dynamic for the
// item being rendered.

import (
	"encoding/json"
	"math"
	"strconv"
)

// LoopHooks are what the atomic Loop needs from the site (builder/atomic_hook.go fills them).
type LoopHooks struct {
	// Query runs a loop query (Template_Type_Base::build_item_provider).
	Query func(q LoopQuery) LoopResult
	// Enter makes an item of a loop the current post (the_post) or loop term until leave is called.
	Enter func(loopID, itemID string) (leave func())
	// Dynamic renders a dynamic tag (Dynamic_Tags_Manager::get_tag_data_content).
	Dynamic func(name string, settings map[string]any) any
	// CurrentPage is Loop_Query_Pagination::get_current_page_for_loop (e-page-<loop id>).
	CurrentPage func(loopID string) int
	// PageURL is Has_Pagination_Context::build_page_url.
	PageURL func(loopID string, page int) string
}

// LoopQuery is a loop's resolved query settings (plain values) plus the page window.
type LoopQuery struct {
	LoopID   string
	Settings map[string]any
	Page     int // > 1: paged
	Offset   int // > 0: offset (wins over Page)
}

// LoopResult is the query result (Post_Loop_Item_Provider).
type LoopResult struct {
	Items    []string // item (post) ids in order
	Found    int      // found_posts
	MaxPages int
	PerPage  int
	Terms    bool // a term loop: one page, every slot (Collection_Loop::resolve_slot_window)
}

const loopCtxKey = "collection-loop"

// loopSlotsPerPage is Loop_Query::clamp_posts_per_page.
func loopSlotsPerPage(settings map[string]any) int {
	n := 3
	if v, ok := settings["posts_per_page"]; ok && v != nil {
		n = int(toFloat(v))
	}
	return min(100, max(1, n))
}

// loopSelector is Alternate_Selector.
type loopSelector struct {
	template   *Node
	alternates []loopAlt // reversed: the last alternate wins
	usedOnce   map[string]bool
}

type loopAlt struct {
	n            *Node
	applyOnce    bool
	repeatEvery  int
	staticPosion bool
}

type loopPick struct {
	n        *Node
	isStatic bool
}

func (e *Env) loopSelectorFor(items []*Node) *loopSelector {
	var list []*Node
	for _, c := range items {
		if c.Type() == "e-collection-loop-item" {
			list = append(list, c)
		}
	}
	if len(list) == 0 {
		return nil
	}
	s := &loopSelector{template: list[0], usedOnce: map[string]bool{}}
	alts := list[1:]
	if len(alts) > 5 {
		alts = alts[:5]
	}
	for i := len(alts) - 1; i >= 0; i-- {
		n := alts[i]
		et := Lookup(n.Type())
		st := NewMap()
		if et != nil {
			st = e.settingsResolver().resolve(et.Schema, n.Settings)
		}
		r := int(math.Abs(toFloat(st.Get("alternate_repeat_every"))))
		s.alternates = append(s.alternates, loopAlt{n: n, applyOnce: st.Get("alternate_apply_once") == true, repeatEvery: r,
			staticPosion: st.Get("alternate_static_position") == true})
	}
	return s
}

func (s *loopSelector) hasStatic() bool {
	for _, a := range s.alternates {
		if a.staticPosion && a.repeatEvery != 0 {
			return true
		}
	}
	return false
}

func (s *loopSelector) find(index int, excluded map[string]bool) *loopAlt {
	for i := range s.alternates {
		a := &s.alternates[i]
		if excluded[a.n.ID] || (a.applyOnce && s.usedOnce[a.n.ID]) || a.repeatEvery == 0 {
			continue
		}
		one := index + 1
		if a.applyOnce {
			if one != a.repeatEvery {
				continue
			}
		} else if one%a.repeatEvery != 0 {
			continue
		}
		if a.applyOnce {
			s.usedOnce[a.n.ID] = true
		}
		return a
	}
	return nil
}

// consume is consume_item_slots: the slots one item occupies (static alternates first).
func (s *loopSelector) consume(start int) []loopPick {
	var picks []loopPick
	excluded := map[string]bool{}
	for slot := start; ; slot++ {
		a := s.find(slot, excluded)
		if a == nil {
			return append(picks, loopPick{n: s.template})
		}
		picks = append(picks, loopPick{n: a.n, isStatic: a.staticPosion})
		if !a.staticPosion {
			return picks
		}
		if !a.applyOnce && a.repeatEvery == 1 {
			excluded[a.n.ID] = true
		}
	}
}

// loopItemsBeforeSlot is Loop_Slot_Map::items_before_slot.
func (e *Env) loopItemsBeforeSlot(items []*Node, slots int) int {
	s := e.loopSelectorFor(items)
	n, slot := 0, 0
	for slot < slots {
		for _, p := range s.consume(slot) {
			if slot >= slots {
				return n
			}
			if !p.isStatic {
				n++
			}
			slot++
		}
	}
	return n
}

// loopTotalSlots is Loop_Slot_Map::total_slots.
func (e *Env) loopTotalSlots(items []*Node, total int) int {
	s := e.loopSelectorFor(items)
	n, slots := 0, 0
	for n < total {
		for _, p := range s.consume(slots) {
			slots++
			if !p.isStatic {
				n++
			}
		}
	}
	return slots
}

// plainMap converts resolved settings to plain JSON values.
func plainMap(m *Map) map[string]any {
	out := map[string]any{}
	if m == nil {
		return out
	}
	b, err := json.Marshal(m)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

func (e *Env) loopLayout(n *Node) *Node {
	for _, c := range n.Children {
		if c.Type() == "e-collection-loop-layout" {
			return c
		}
	}
	return nil
}

// loopDefineContext pushes the "collection-loop" render context of an e-collection-loop
// (Has_Loop_Query::define_render_context with Collection_Loop's extensions).
func (e *Env) loopDefineContext(n *Node, et *ElementType) []string {
	if et.Name != "e-collection-loop" {
		return nil
	}
	h := e.Loop
	loopID := n.ID
	settings := e.settingsResolver().resolve(et.Schema, n.Settings)
	query := plainMap(asMap(settings.Get("query")))
	page := 1
	if h != nil && h.CurrentPage != nil {
		page = max(1, h.CurrentPage(loopID))
	}
	layout := e.loopLayout(n)
	var items []*Node
	layoutID := ""
	if layout != nil {
		items, layoutID = layout.Children, layout.ID
	}
	perPage := loopSlotsPerPage(query)
	startSlot := (page - 1) * perPage
	sel := e.loopSelectorFor(items)
	q := LoopQuery{LoopID: loopID, Settings: query}
	if startSlot > 0 && sel != nil && sel.hasStatic() {
		q.Offset = e.loopItemsBeforeSlot(items, startSlot)
	} else if page > 1 {
		q.Page = page
	}
	var res LoopResult
	if h != nil && h.Query != nil {
		res = h.Query(q)
	}
	budget, maxPages := len(res.Items), res.MaxPages
	if sel != nil && sel.hasStatic() {
		total := e.loopTotalSlots(items, max(res.Found, len(res.Items)))
		budget = min(perPage, max(0, total-startSlot))
		maxPages = int(math.Ceil(float64(total) / float64(perPage)))
		if res.Terms {
			startSlot, budget, maxPages = 0, total, 1
		}
	}
	pagination := settings.Get("pagination") == true
	ptype := phpString(settings.Get("pagination_type"))
	if ptype == "" {
		ptype = "prev_next"
	}
	ltype := phpString(settings.Get("pagination_load_type"))
	if ltype == "" {
		ltype = "page_reload"
	}
	ids := make([]any, len(res.Items))
	for i, id := range res.Items {
		ids[i] = id
	}
	layoutContent := ""
	if layoutID != "" {
		layoutContent = "e-collection-loop-layout-" + layoutID
	}
	ctx := MapOf("has_items", len(res.Items) > 0, "loop_id", loopID, "layout_content_id", layoutContent,
		"current_page", float64(page), "max_pages", float64(maxPages), "start_slot", float64(startSlot),
		"slot_budget", float64(budget), "pagination_enabled", pagination && maxPages > 1, "pagination_type", ptype,
		"pagination_load_type", ltype, "empty_state_enabled", settings.Get("empty_state") == true, "__items", ids)
	e.pushCtx(loopCtxKey, ctx)
	return []string{loopCtxKey}
}

// loopTemplateContext adds the loop context to the template context of the loop's parts.
func (e *Env) loopTemplateContext(n *Node, et *ElementType, ctx *Map) {
	switch et.Name {
	case "e-collection-loop-layout", "e-collection-loop-empty-state":
		ctx.Set("loop_context", e.getCtx(loopCtxKey))
	case "e-pagination":
		lc := e.getCtx(loopCtxKey)
		a11y := MapOf("nav_label", "Pagination")
		if lc.Get("pagination_enabled") == true {
			a11y.Set("status_label", "Page "+phpString(lc.Get("current_page"))+" of "+phpString(lc.Get("max_pages")))
		}
		ctx.Set("loop_context", lc)
		ctx.Set("a11y", a11y)
	case "e-pagination-prev", "e-pagination-next":
		ctx.Set("pagination", e.loopPaginationModel(et.Name == "e-pagination-prev"))
	}
}

// loopPaginationModel is Collection_Loop_Pagination_Button::build_pagination_view_model.
func (e *Env) loopPaginationModel(prev bool) *Map {
	dir, label := "next", "Next page"
	if prev {
		dir, label = "prev", "Previous page"
	}
	lc := e.getCtx(loopCtxKey)
	loopID := phpString(lc.Get("loop_id"))
	maxPages := int(toFloat(lc.Get("max_pages")))
	if loopID == "" || maxPages <= 1 {
		return MapOf("aria_controls", "", "aria_label", label, "direction", dir, "has_context", false, "href", "", "is_disabled", false)
	}
	cur := int(toFloat(lc.Get("current_page")))
	target := cur + 1
	if prev {
		target = cur - 1
	}
	disabled := target < 1 || target > maxPages
	href := ""
	if !disabled && e.Loop != nil && e.Loop.PageURL != nil {
		href = e.Loop.PageURL(loopID, target)
	}
	return MapOf("aria_controls", phpString(lc.Get("layout_content_id")), "aria_label", label, "direction", dir, "has_context", true,
		"href", href, "is_disabled", disabled)
}

// loopChildren renders a loop layout's children once per item (Has_Loop_Iteration); ok is false
// for every other element (and a layout outside a loop renders its children as usual).
func (e *Env) loopChildren(n *Node, et *ElementType) (string, bool) {
	if et.Name != "e-collection-loop-layout" {
		return "", false
	}
	lc := e.getCtx(loopCtxKey)
	if lc.Len() == 0 {
		return "", false
	}
	if lc.Get("has_items") != true {
		return "", true
	}
	sel := e.loopSelectorFor(n.Children)
	if sel == nil {
		return "", true
	}
	loopID := phpString(lc.Get("loop_id"))
	slot := max(0, int(toFloat(lc.Get("start_slot"))))
	end := slot + max(0, int(toFloat(lc.Get("slot_budget"))))
	var out []byte
	for _, it := range listOf(lc.Get("__items")) {
		if slot >= end {
			break
		}
		itemID := phpString(it)
		var leave func()
		if e.Loop != nil && e.Loop.Enter != nil {
			leave = e.Loop.Enter(loopID, itemID)
		}
		for _, p := range sel.consume(slot) {
			if slot >= end {
				break
			}
			c := cloneNode(p.n)
			FormatIDs(c.Children, []string{loopID, itemID, strconv.Itoa(slot)})
			out = append(out, e.Render(c)...)
			slot++
		}
		if leave != nil {
			leave()
		}
	}
	return string(out), true
}

// cloneNode copies a node tree (settings and styles are shared: rendering does not change them).
func cloneNode(n *Node) *Node {
	c := *n
	c.Children = make([]*Node, len(n.Children))
	for i, ch := range n.Children {
		c.Children[i] = cloneNode(ch)
	}
	return &c
}

// loopDynamic is the "dynamic" settings transformer (Dynamic_Transformer).
func loopDynamic(r *resolver, v any, _ string, _ *PropType) any {
	if r.env == nil || r.env.Loop == nil || r.env.Loop.Dynamic == nil {
		return nil
	}
	m := asMap(v)
	name := phpString(m.Get("name"))
	if name == "" {
		return nil
	}
	settings := map[string]any{}
	if s := asMap(m.Get("settings")); s != nil {
		for _, k := range s.Keys() {
			settings[k] = loopPlainProp(s.Get(k))
		}
	}
	out := r.env.Loop.Dynamic(name, settings)
	if mm, ok := out.(map[string]any); ok {
		return FromAny(mm)
	}
	return out
}

// loopPlainProp unwraps {"$$type", "value"} props to plain values.
func loopPlainProp(v any) any {
	if m, _, ok := transformable(v); ok {
		return loopPlainProp(m.Get("value"))
	}
	switch x := v.(type) {
	case *Map:
		out := map[string]any{}
		for _, k := range x.Keys() {
			out[k] = loopPlainProp(x.Get(k))
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = loopPlainProp(e)
		}
		return out
	}
	return v
}

// LoopPaginationScript is Module::PAGINATION_SCRIPT_HANDLE (a Pro script the server serves).
const LoopPaginationScript = "elementor-pro-collection-loop-pagination-handler"

func init() { scriptDeps[LoopPaginationScript] = []string{"elementor-v2-frontend-handlers"} }

// loopScripts is Collection_Loop::get_script_depends.
func loopScripts(n *Node) []string {
	if n.Type() == "e-collection-loop" {
		return []string{LoopPaginationScript}
	}
	return nil
}
