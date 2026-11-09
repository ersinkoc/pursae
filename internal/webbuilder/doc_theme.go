package webbuilder

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Theme Builder additions to the doc model (contract §11.4): loop sections, dynamic layout
// cells (Item.Dyn), nav/footer menu binding and PageSettings.Layout.

var dynWidgetSet = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range DynWidgets {
		m[string(w)] = true
	}
	return m
}()

// IsDynWidget reports whether a layout cell type (Item.Title) is a dynamic widget.
func IsDynWidget(cell string) bool { return dynWidgetSet[cell] }

var shopWidgetSet = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range ShopWidgets {
		m[string(w)] = true
	}
	return m
}()

// IsShopWidget reports whether a layout cell type is a Pursae Commerce widget (ShopWidgets).
func IsShopWidget(cell string) bool { return shopWidgetSet[cell] }

// IsItemWidget reports whether the server reads a cell's settings from the stored doc through
// data-wb-item (Commerce widgets and the language switcher).
func IsItemWidget(cell string) bool {
	return shopWidgetSet[cell] || cell == string(DynLanguageSwitcher)
}

var shopIDsRE = regexp.MustCompile(ShopIDsPattern)

// ParseShopIDs parses a ShopIDsPattern list (nil when empty or invalid; at most max ids).
func ParseShopIDs(s string, max int) []int64 {
	if s == "" || !shopIDsRE.MatchString(s) {
		return nil
	}
	parts := strings.Split(s, ",")
	if len(parts) > max {
		parts = parts[:max]
	}
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		if id, ok := parseID(p); ok {
			out = append(out, id)
		}
	}
	return out
}

// IsCellType reports whether cell is a known layout cell type (static or dynamic).
func IsCellType(cell string) bool {
	if dynWidgetSet[cell] {
		return true
	}
	for _, t := range StaticCellTypes {
		if t == cell {
			return true
		}
	}
	return false
}

// ValidPageLayout: "", PageLayoutSite or PageLayoutCanvas.
func ValidPageLayout(l string) bool { return l == "" || enums["pageLayout"][l] }

// EffectivePageLayout is the page's layout: PageSettings.Layout when valid and set, else
// DefaultPageLayout(doc).
func EffectivePageLayout(ps PageSettings, d *DocState) string {
	if ps.Layout == PageLayoutSite || ps.Layout == PageLayoutCanvas {
		return ps.Layout
	}
	return DefaultPageLayout(d)
}

// FindSection returns the section with id anywhere in the doc (top level or nested in item
// subs), or nil.
func FindSection(d *DocState, id string) *Section {
	if d == nil || id == "" {
		return nil
	}
	var walk func(s *Section, depth int) *Section
	walk = func(s *Section, depth int) *Section {
		if s.ID == id {
			return s
		}
		if depth > MaxDepth+1 {
			return nil
		}
		for i := range s.Items {
			if sub := s.Items[i].Sub; sub != nil {
				if f := walk(sub, depth+1); f != nil {
					return f
				}
			}
		}
		return nil
	}
	for i := range d.Sections {
		if f := walk(&d.Sections[i], 0); f != nil {
			return f
		}
	}
	return nil
}

// LoopTerm is one taxonomy:id pair of a loop's terms filter.
type LoopTerm struct {
	Taxonomy string
	ID       int64
}

// LoopSpec is a loop section's query with defaults applied (contract §11.4 table).
type LoopSpec struct {
	Source         string // LoopSource*
	PostType       string
	PerPage        int // 1…MaxLoopPerPage (main: the caller uses posts_per_page)
	OrderBy        string
	Order          string
	Terms          []LoopTerm // query: filter; related: only the taxonomies matter
	Author         int64
	ExcludeCurrent bool
	Offset         int
	Pagination     string // main only; "none" for query and related
	Empty          string
}

// ParseLoopSpec reads a loop section's content leniently: a missing or invalid value takes the
// default (the stored doc was validated at publish; this guards older or edited rows).
func ParseLoopSpec(content map[string]string) LoopSpec {
	sp := LoopSpec{Source: LoopSourceMain, PostType: "post", PerPage: 6, OrderBy: "date", Order: "desc", Pagination: "numbers"}
	get := func(k string) string { return strings.TrimSpace(content[k]) }
	if v := get(LoopSource); enums["loopSource"][v] {
		sp.Source = v
	}
	if v := get(LoopPostType); slugRE.MatchString(v) {
		sp.PostType = v
	}
	if n, ok := intIn(get(LoopPerPage), 1, MaxLoopPerPage); ok {
		sp.PerPage = n
	}
	if v := get(LoopOrderBy); enums["loopOrderBy"][v] {
		sp.OrderBy = v
	}
	if v := get(LoopOrder); enums["loopOrder"][v] {
		sp.Order = v
	}
	if terms, ok := parseLoopTerms(get(LoopTerms)); ok {
		sp.Terms = terms
	}
	if id, ok := parseID(get(LoopAuthor)); ok {
		sp.Author = id
	}
	sp.ExcludeCurrent = get(LoopExclude) == "current"
	if n, ok := intIn(get(LoopOffset), 0, MaxLoopOffset); ok {
		sp.Offset = n
	}
	if v := get(LoopPagination); enums["loopPaging"][v] {
		sp.Pagination = v
	}
	if sp.Source != LoopSourceMain {
		sp.Pagination = "none"
	}
	if sp.Source == LoopSourceRelated && len(sp.Terms) == 0 {
		sp.Terms = []LoopTerm{{Taxonomy: "category"}}
	}
	if e := content[LoopEmpty]; utf8.RuneCountInString(e) <= MaxDynEmptyRunes {
		sp.Empty = e
	}
	return sp
}

func intIn(s string, lo, hi int) (int, bool) {
	if s == "" || len(s) > 4 {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo || n > hi || strconv.Itoa(n) != s {
		return 0, false
	}
	return n, true
}

// parseLoopTerms parses "tax:id,tax:id" (≤ MaxLoopTerms pairs). For related loops an id-less
// "tax" names a taxonomy to compare; it parses as ID 0.
func parseLoopTerms(s string) ([]LoopTerm, bool) {
	if s == "" {
		return nil, true
	}
	parts := strings.Split(s, ",")
	if len(parts) > MaxLoopTerms {
		return nil, false
	}
	out := make([]LoopTerm, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		tax, id, hasID := strings.Cut(p, ":")
		if !slugRE.MatchString(tax) {
			return nil, false
		}
		t := LoopTerm{Taxonomy: tax}
		if hasID {
			n, ok := parseID(id)
			if !ok {
				return nil, false
			}
			t.ID = n
		}
		out = append(out, t)
	}
	return out, true
}

// ---------------------------------------------------------------------------------------------
// Validation.

// loop validates a loop section's query keys and card.
func (v *validator) loop(s *Section, path string) {
	cp := func(k string) string { return ptr(path, "content", k) }
	c := s.Content
	v.enum(cp(LoopSource), "loopSource", c[LoopSource])
	if pt := c[LoopPostType]; pt != "" && !slugRE.MatchString(pt) {
		v.add(cp(LoopPostType), VErrEnum, "bad post type")
	}
	if pp := c[LoopPerPage]; pp != "" {
		if _, ok := intIn(pp, 1, MaxLoopPerPage); !ok {
			v.add(cp(LoopPerPage), VErrEnum, fmt.Sprintf("perPage must be 1…%d", MaxLoopPerPage))
		}
	}
	v.enum(cp(LoopOrderBy), "loopOrderBy", c[LoopOrderBy])
	v.enum(cp(LoopOrder), "loopOrder", c[LoopOrder])
	if t := c[LoopTerms]; t != "" {
		if _, ok := parseLoopTerms(t); !ok {
			v.add(cp(LoopTerms), VErrEnum, fmt.Sprintf("terms must be taxonomy:id pairs (at most %d)", MaxLoopTerms))
		}
	}
	if a := c[LoopAuthor]; a != "" {
		if _, ok := parseID(a); !ok {
			v.add(cp(LoopAuthor), VErrEnum, "author must be a user id")
		}
	}
	if e := c[LoopExclude]; e != "" && e != "current" {
		v.add(cp(LoopExclude), VErrEnum, `exclude must be "current" or empty`)
	}
	if o := c[LoopOffset]; o != "" {
		if _, ok := intIn(o, 0, MaxLoopOffset); !ok {
			v.add(cp(LoopOffset), VErrEnum, fmt.Sprintf("offset must be 0…%d", MaxLoopOffset))
		}
	}
	v.enum(cp(LoopPagination), "loopPaging", c[LoopPagination])
	if utf8.RuneCountInString(c[LoopEmpty]) > MaxDynEmptyRunes {
		v.add(cp(LoopEmpty), VErrTooLong, fmt.Sprintf("longer than %d characters", MaxDynEmptyRunes))
	}
	if len(s.Items) > 0 && s.Items[0].Sub != nil && s.Items[0].Sub.Kind != KindLayout {
		v.add(ptr(path, "items", 0, "sub", "kind"), VErrKind, "a loop card is a layout section")
	}
}

// binding validates the menu / logo binding keys of a nav or footer section.
func (v *validator) binding(s *Section, path string) {
	if m := s.Content[ContentMenu]; m != "" && !menuLocRE.MatchString(m) {
		v.add(ptr(path, "content", ContentMenu), VErrEnum, "bad menu location")
	}
	if l := s.Content[ContentLogoSource]; l != "" && l != LogoSourceSite {
		v.add(ptr(path, "content", ContentLogoSource), VErrEnum, fmt.Sprintf("unknown value %q", trunc(l, 40)))
	}
}

// dyn validates Item.Dyn. A ratio or words of 0 means "not set".
func (v *validator) dyn(d *DynSettings, path string) {
	v.enum(ptr(path, "tag"), "dynTag", d.Tag)
	if d.Format != "" && !dynFormatRE.MatchString(d.Format) {
		v.add(ptr(path, "format"), VErrEnum, "bad date format")
	}
	v.enum(ptr(path, "size"), "dynSize", d.Size)
	if d.Ratio != nil && *d.Ratio != 0 && (math.IsNaN(*d.Ratio) || *d.Ratio < MinDynRatio || *d.Ratio > MaxDynRatio) {
		v.add(ptr(path, "ratio"), VErrEnum, fmt.Sprintf("ratio must be %v…%v", MinDynRatio, MaxDynRatio))
	}
	if len(d.Fields) > len(DynMetaFields) {
		v.add(ptr(path, "fields"), VErrTooMany, fmt.Sprintf("more than %d fields", len(DynMetaFields)))
	} else {
		for i, f := range d.Fields {
			if !enums["dynField"][f] {
				v.add(ptr(path, "fields", i), VErrEnum, fmt.Sprintf("unknown value %q", trunc(f, 40)))
			}
		}
	}
	if utf8.RuneCountInString(d.Sep) > MaxDynSepRunes {
		v.add(ptr(path, "sep"), VErrTooLong, fmt.Sprintf("longer than %d characters", MaxDynSepRunes))
	}
	if d.Tax != "" && !slugRE.MatchString(d.Tax) {
		v.add(ptr(path, "tax"), VErrEnum, "bad taxonomy")
	}
	if d.Location != "" && !menuLocRE.MatchString(d.Location) {
		v.add(ptr(path, "location"), VErrEnum, "bad menu location")
	}
	v.enum(ptr(path, "menuLayout"), "menuLayout", d.MenuLayout)
	if d.BurgerAt != nil && (math.IsNaN(*d.BurgerAt) || *d.BurgerAt < 0 || *d.BurgerAt > 10000) {
		v.add(ptr(path, "burgerAt"), VErrEnum, "burgerAt must be 0…10000")
	}
	if d.Words != nil && *d.Words != 0 && (math.IsNaN(*d.Words) || *d.Words < MinDynWords || *d.Words > MaxDynWords) {
		v.add(ptr(path, "words"), VErrEnum, fmt.Sprintf("words must be %d…%d", MinDynWords, MaxDynWords))
	}
	if utf8.RuneCountInString(d.Empty) > MaxDynEmptyRunes {
		v.add(ptr(path, "empty"), VErrTooLong, fmt.Sprintf("longer than %d characters", MaxDynEmptyRunes))
	}
	v.enum(ptr(path, "source"), "shopSource", d.Source)
	v.enum(ptr(path, "orderBy"), "shopOrderBy", d.OrderBy)
	v.enum(ptr(path, "order"), "loopOrder", d.Order)
	num := func(key string, f *float64, lo, hi float64) {
		if f != nil && *f != 0 && (math.IsNaN(*f) || *f < lo || *f > hi || *f != math.Trunc(*f)) {
			v.add(ptr(path, key), VErrEnum, fmt.Sprintf("%s must be a whole number %v…%v", key, lo, hi))
		}
	}
	num("columns", d.Columns, 1, MaxShopColumns)
	num("limit", d.Limit, 1, MaxShopLimit)
	num("product", d.Product, 1, 9e15)
	for key, ids := range map[string]string{"cats": d.Cats, "tags": d.Tags, "ids": d.IDs} {
		if ids != "" && !shopIDsRE.MatchString(ids) {
			v.add(ptr(path, key), VErrEnum, fmt.Sprintf("%s must be up to %d comma-separated ids", key, MaxShopIDs))
		}
	}
	if d.Parent != "" && d.Parent != "0" {
		if _, ok := parseID(d.Parent); !ok {
			v.add(ptr(path, "parent"), VErrEnum, `parent must be "", "0" or a category id`)
		}
	}
}
