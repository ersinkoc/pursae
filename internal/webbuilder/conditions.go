package webbuilder

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Display conditions (contract §11.3): parse, validate, score, match and pick, over a
// QueryFacts description of the front request. Pure code; the server builds QueryFacts from its
// query and the template index from the database.

// Condition scopes (Condition.Scope).
const (
	ScopeGeneral     = "general"          // general
	Scope404         = "404"              // 404
	ScopeSingular    = "singular"         // singular
	ScopeFrontPage   = "front_page"       // singular/front_page
	ScopePostType    = "post_type"        // singular/<ptype>
	ScopeEntry       = "entry"            // singular/<ptype>/<id>
	ScopeInTerm      = "in"               // singular/<ptype>/in/<tax>/<id>
	ScopeChildOf     = "child_of"         // singular/<ptype>/child_of/<id>
	ScopeByAuthor    = "by_author"        // singular/<ptype>/by_author/<id>
	ScopeArchive     = "archive"          // archive
	ScopeHome        = "archive_home"     // archive/home
	ScopeDate        = "archive_date"     // archive/date
	ScopeSearch      = "archive_search"   // archive/search
	ScopeAuthor      = "archive_author"   // archive/author[/<id>] (ID 0 = any author)
	ScopeTypeArchive = "archive_type"     // archive/type/<ptype>
	ScopeTaxonomy    = "archive_taxonomy" // archive/<tax>[/<id>] (ID 0 = any term)
)

// Archive kinds of QueryFacts.Archive.
const (
	ArchiveHome     = "home"   // the posts index (is_home)
	ArchiveDate     = "date"   // a date archive
	ArchiveSearch   = "search" // search results
	ArchiveAuthor   = "author" // an author archive (QueryFacts.AuthorID)
	ArchiveTerm     = "term"   // a term archive (QueryFacts.Taxonomy, TermID)
	ArchivePostType = "type"   // a post type archive (QueryFacts.PostType)
)

// neverMatchTypes are post types no rule ever matches (templates themselves).
var neverMatchTypes = map[string]bool{TemplatePostType: true}

var (
	reservedSingular = toSet(ReservedSingularWords)
	reservedArchive  = toSet(ReservedArchiveWords)
)

// Condition is one parsed rule.
type Condition struct {
	Raw      string // the rule as written
	Exclude  bool
	Scope    string // Scope* constant
	PostType string // ScopePostType, ScopeEntry, ScopeInTerm, ScopeChildOf, ScopeByAuthor, ScopeTypeArchive
	Taxonomy string // ScopeInTerm, ScopeTaxonomy
	ID       int64  // entry, term, parent, author id; 0 = none
}

// ErrBadConditionRule is wrapped by every ParseCondition error.
var ErrBadConditionRule = errors.New("webbuilder: bad condition")

func badRule(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrBadConditionRule}, a...)...)
}

// parseID: a positive decimal without leading zeros, at most 19 digits, within int64.
func parseID(s string) (int64, bool) {
	if s == "" || len(s) > 19 || s[0] < '1' || s[0] > '9' {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

// ParseCondition parses one rule (exact text: no trimming). It is the authority on the grammar;
// ConditionPattern describes the same language minus the reserved words and the int64 range.
func ParseCondition(raw string) (Condition, error) {
	c := Condition{Raw: raw}
	if raw == "" {
		return c, badRule("empty rule")
	}
	if len(raw) > MaxConditionBytes {
		return c, badRule("longer than %d bytes", MaxConditionBytes)
	}
	parts := strings.Split(raw, "/")
	switch parts[0] {
	case CondInclude:
	case CondExclude:
		c.Exclude = true
	default:
		return c, badRule("a rule starts with include/ or exclude/")
	}
	p := parts[1:]
	if len(p) == 0 {
		return c, badRule("missing scope")
	}
	switch p[0] {
	case "general", "404":
		if len(p) != 1 {
			return c, badRule("%s takes no arguments", p[0])
		}
		c.Scope = ScopeGeneral
		if p[0] == "404" {
			c.Scope = Scope404
		}
		return c, nil
	case "singular":
		return parseSingular(c, p[1:])
	case "archive":
		return parseArchive(c, p[1:])
	}
	return c, badRule("unknown scope %q", trunc(p[0], 32))
}

func parseSingular(c Condition, p []string) (Condition, error) {
	if len(p) == 0 {
		c.Scope = ScopeSingular
		return c, nil
	}
	if p[0] == "front_page" {
		if len(p) != 1 {
			return c, badRule("front_page takes no arguments")
		}
		c.Scope = ScopeFrontPage
		return c, nil
	}
	if !slugRE.MatchString(p[0]) || reservedSingular[p[0]] {
		return c, badRule("bad post type %q", trunc(p[0], 32))
	}
	c.PostType = p[0]
	var ok bool
	switch len(p) {
	case 1:
		c.Scope = ScopePostType
		return c, nil
	case 2:
		c.Scope = ScopeEntry
		if c.ID, ok = parseID(p[1]); !ok {
			return c, badRule("bad id")
		}
		return c, nil
	case 3:
		switch p[1] {
		case "child_of":
			c.Scope = ScopeChildOf
		case "by_author":
			c.Scope = ScopeByAuthor
		default:
			return c, badRule("unknown relation %q", trunc(p[1], 32))
		}
		if c.ID, ok = parseID(p[2]); !ok {
			return c, badRule("bad id")
		}
		return c, nil
	case 4:
		if p[1] != "in" {
			return c, badRule("unknown relation %q", trunc(p[1], 32))
		}
		if !slugRE.MatchString(p[2]) {
			return c, badRule("bad taxonomy %q", trunc(p[2], 32))
		}
		c.Scope, c.Taxonomy = ScopeInTerm, p[2]
		if c.ID, ok = parseID(p[3]); !ok {
			return c, badRule("bad id")
		}
		return c, nil
	}
	return c, badRule("too many parts")
}

func parseArchive(c Condition, p []string) (Condition, error) {
	if len(p) == 0 {
		c.Scope = ScopeArchive
		return c, nil
	}
	var ok bool
	switch p[0] {
	case "home", "date", "search":
		if len(p) != 1 {
			return c, badRule("archive/%s takes no arguments", p[0])
		}
		c.Scope = map[string]string{"home": ScopeHome, "date": ScopeDate, "search": ScopeSearch}[p[0]]
		return c, nil
	case "author":
		c.Scope = ScopeAuthor
		switch len(p) {
		case 1:
			return c, nil
		case 2:
			if c.ID, ok = parseID(p[1]); !ok {
				return c, badRule("bad id")
			}
			return c, nil
		}
		return c, badRule("too many parts")
	case "type":
		if len(p) != 2 || !slugRE.MatchString(p[1]) {
			return c, badRule("archive/type needs a post type")
		}
		c.Scope, c.PostType = ScopeTypeArchive, p[1]
		return c, nil
	}
	if !slugRE.MatchString(p[0]) || reservedArchive[p[0]] {
		return c, badRule("bad taxonomy %q", trunc(p[0], 32))
	}
	c.Scope, c.Taxonomy = ScopeTaxonomy, p[0]
	switch len(p) {
	case 1:
		return c, nil
	case 2:
		if c.ID, ok = parseID(p[1]); !ok {
			return c, badRule("bad id")
		}
		return c, nil
	}
	return c, badRule("too many parts")
}

// String writes the rule in its canonical form (equal to Raw for a parsed rule).
func (c Condition) String() string {
	mode := CondInclude
	if c.Exclude {
		mode = CondExclude
	}
	id := strconv.FormatInt(c.ID, 10)
	var scope string
	switch c.Scope {
	case ScopeGeneral, Scope404, ScopeSingular, ScopeArchive:
		scope = c.Scope
	case ScopeFrontPage:
		scope = "singular/front_page"
	case ScopePostType:
		scope = "singular/" + c.PostType
	case ScopeEntry:
		scope = "singular/" + c.PostType + "/" + id
	case ScopeInTerm:
		scope = "singular/" + c.PostType + "/in/" + c.Taxonomy + "/" + id
	case ScopeChildOf, ScopeByAuthor:
		scope = "singular/" + c.PostType + "/" + c.Scope + "/" + id
	case ScopeHome:
		scope = "archive/home"
	case ScopeDate:
		scope = "archive/date"
	case ScopeSearch:
		scope = "archive/search"
	case ScopeAuthor:
		scope = "archive/author"
		if c.ID != 0 {
			scope += "/" + id
		}
	case ScopeTypeArchive:
		scope = "archive/type/" + c.PostType
	case ScopeTaxonomy:
		scope = "archive/" + c.Taxonomy
		if c.ID != 0 {
			scope += "/" + id
		}
	default:
		return ""
	}
	return mode + "/" + scope
}

// Score is the rule's specificity (contract §11.3 table).
func (c Condition) Score() int {
	switch c.Scope {
	case ScopeGeneral:
		return ScoreGeneral
	case ScopeSingular, ScopeArchive:
		return ScoreView
	case ScopePostType, ScopeHome, ScopeDate, ScopeSearch, ScopeTypeArchive:
		return ScoreType
	case ScopeAuthor, ScopeTaxonomy:
		if c.ID != 0 {
			return ScoreSpecificID
		}
		return ScoreType
	case ScopeInTerm, ScopeChildOf, ScopeByAuthor:
		return ScoreRelation
	case ScopeFrontPage, Scope404:
		return ScoreSpecial
	case ScopeEntry:
		return ScoreSpecificID
	}
	return 0
}

// QueryFacts describes the front request for the resolver.
type QueryFacts struct {
	View      string // ViewSingular, ViewArchive or View404
	FrontPage bool   // is_front_page: the static front page, or the posts index on the front
	Store     bool   // a Pursae Commerce store view: only store main templates (StoreTemplateKinds)
	StoreKind string // the store view kind (siteQuery.Store: product, shop, product_cat, …; "" off the store)

	// Singular: the entry. Post type archive: PostType is the archived type.
	PostType string
	PostID   int64
	ParentID int64
	// AuthorID is the entry's author (singular) or the archived author (ArchiveAuthor).
	AuthorID int64
	Terms    map[string][]int64 // singular: taxonomy → the entry's term ids (and their translations)
	// PostIDs / ParentIDs: other ids that stand for PostID / ParentID, the translations of the
	// entry and of its parent (Pursae Multi Language), so a rule naming one language version of a
	// post or page matches all of them.
	PostIDs   []int64
	ParentIDs []int64

	// Archive views.
	Archive  string  // Archive* constant ("" on other views)
	Taxonomy string  // ArchiveTerm
	TermID   int64   // ArchiveTerm
	TermIDs  []int64 // ArchiveTerm: translations of TermID (Pursae Multi Language)
}

// hasID reports whether want is the scalar id or one of its aliases.
func hasID(scalar int64, aliases []int64, want int64) bool {
	if scalar == want {
		return true
	}
	for _, id := range aliases {
		if id == want {
			return true
		}
	}
	return false
}

// Match reports whether the rule's scope covers the request (include and exclude alike).
func (c Condition) Match(q QueryFacts) bool {
	if neverMatchTypes[c.PostType] {
		return false
	}
	singular := q.View == ViewSingular
	archive := q.View == ViewArchive
	switch c.Scope {
	case ScopeGeneral:
		return true
	case Scope404:
		return q.View == View404
	case ScopeSingular:
		return singular
	case ScopeFrontPage:
		return q.FrontPage && q.View != View404
	case ScopePostType:
		return singular && q.PostType == c.PostType
	case ScopeEntry:
		return singular && q.PostType == c.PostType && hasID(q.PostID, q.PostIDs, c.ID)
	case ScopeInTerm:
		if !singular || q.PostType != c.PostType {
			return false
		}
		for _, id := range q.Terms[c.Taxonomy] {
			if id == c.ID {
				return true
			}
		}
		return false
	case ScopeChildOf:
		return singular && q.PostType == c.PostType && q.ParentID > 0 && hasID(q.ParentID, q.ParentIDs, c.ID)
	case ScopeByAuthor:
		return singular && q.PostType == c.PostType && q.AuthorID == c.ID
	case ScopeArchive:
		return archive
	case ScopeHome:
		return archive && q.Archive == ArchiveHome
	case ScopeDate:
		return archive && q.Archive == ArchiveDate
	case ScopeSearch:
		return archive && q.Archive == ArchiveSearch
	case ScopeAuthor:
		return archive && q.Archive == ArchiveAuthor && (c.ID == 0 || q.AuthorID == c.ID)
	case ScopeTypeArchive:
		return archive && q.Archive == ArchivePostType && q.PostType == c.PostType
	case ScopeTaxonomy:
		return archive && q.Archive == ArchiveTerm && q.Taxonomy == c.Taxonomy && (c.ID == 0 || hasID(q.TermID, q.TermIDs, c.ID))
	}
	return false
}

// ScoreConditions scores a rule list for the request: any matching exclude vetoes it, else the
// best matching include scores it; ok is false when no include matches.
func ScoreConditions(conds []Condition, q QueryFacts) (score int, ok bool) {
	for _, c := range conds {
		if !c.Match(q) {
			continue
		}
		if c.Exclude {
			return 0, false
		}
		if s := c.Score(); s > score {
			score, ok = s, true
		}
	}
	return score, ok
}

// NormalizeConditions validates a rule list from a client: rules are trimmed, duplicates dropped
// (order kept). Errors point at /conditions/<i> of the input (code VErrCondition); more than
// MaxConditions distinct rules is VErrTooMany at /conditions.
func NormalizeConditions(rules []string) ([]string, []ValidationError) {
	out := make([]string, 0, len(rules))
	var errs []ValidationError
	seen := map[string]bool{}
	for i, r := range rules {
		r = strings.TrimSpace(r)
		if _, err := ParseCondition(r); err != nil {
			if len(errs) < maxValidationErrors {
				msg := strings.TrimPrefix(err.Error(), ErrBadConditionRule.Error()+": ")
				errs = append(errs, ValidationError{Path: "/conditions/" + strconv.Itoa(i), Code: VErrCondition, Message: msg})
			}
			continue
		}
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	if len(out) > MaxConditions {
		errs = append(errs, ValidationError{Path: "/conditions", Code: VErrTooMany, Message: fmt.Sprintf("more than %d rules", MaxConditions)})
	}
	return out, errs
}

// ValidateConditionsRequest normalizes the body of PUT RouteTemplateConditions; a priority
// outside PriorityMin…PriorityMax is an error at /priority (code VErrCondition).
func ValidateConditionsRequest(req TemplateConditionsRequest) (TemplateConditionsRequest, []ValidationError) {
	rules, errs := NormalizeConditions(req.Conditions)
	if req.Priority < PriorityMin || req.Priority > PriorityMax {
		errs = append(errs, ValidationError{Path: "/priority", Code: VErrCondition, Message: fmt.Sprintf("priority must be %d…%d", PriorityMin, PriorityMax)})
	}
	return TemplateConditionsRequest{Conditions: rules, Priority: req.Priority}, errs
}

// ParseConditions parses stored rules leniently: invalid ones are skipped.
func ParseConditions(rules []string) []Condition {
	out := make([]Condition, 0, len(rules))
	for _, r := range rules {
		if c, err := ParseCondition(strings.TrimSpace(r)); err == nil {
			out = append(out, c)
		}
		if len(out) >= MaxConditions {
			break
		}
	}
	return out
}

// DecodeConditionsMeta reads the MetaConditions value (a JSON array of strings); anything else
// is no rules.
func DecodeConditionsMeta(meta string) []string {
	var rules []string
	if err := json.Unmarshal([]byte(meta), &rules); err != nil {
		return nil
	}
	return rules
}

// EncodeConditionsMeta is the MetaConditions value of rules ("[]" for none).
func EncodeConditionsMeta(rules []string) string {
	if rules == nil {
		rules = []string{}
	}
	b, _ := json.Marshal(rules)
	return string(b)
}

// TemplateCandidate is a published template as the resolver sees it.
type TemplateCandidate struct {
	ID         int64
	Type       TemplateType
	Conditions []Condition
	Priority   int
	Modified   time.Time
}

// ViewAllowed is the view gate: a template type renders only on its TemplateTypeViews.
func ViewAllowed(t TemplateType, view string) bool {
	for _, v := range TemplateTypeViews[t] {
		if v == view {
			return true
		}
	}
	return false
}

// ArchiveAllowed is the archive part of the view gate: a type with TemplateTypeArchives renders
// only on those archive kinds; any other type on every archive.
func ArchiveAllowed(t TemplateType, archive string) bool {
	kinds, ok := TemplateTypeArchives[t]
	return !ok || slices.Contains(kinds, archive)
}

// TemplateScore is the candidate's score for the request: view gate, exclude veto, best include,
// plus SearchTypeBonus for a search-results template on a search view.
func TemplateScore(c *TemplateCandidate, q QueryFacts) (int, bool) {
	if !ViewAllowed(c.Type, q.View) || (q.View == ViewArchive && !ArchiveAllowed(c.Type, q.Archive)) {
		return 0, false
	}
	if !StoreGateOK(c.Type, q) {
		return 0, false
	}
	s, ok := ScoreConditions(c.Conditions, q)
	if !ok {
		return 0, false
	}
	if c.Type == TemplateSearch && q.View == ViewArchive && q.Archive == ArchiveSearch {
		s += SearchTypeBonus
	}
	return s, true
}

// StoreGateOK is the store gate of main templates: generic main types never render on a Pursae
// Commerce store view, store types (StoreTemplateKinds) only on their store view kinds. Header
// and footer templates render everywhere.
func StoreGateOK(t TemplateType, q QueryFacts) bool {
	if TemplateLocation(t) != LocationMain {
		return true
	}
	kinds, storeType := StoreTemplateKinds[t]
	if !storeType {
		return !q.Store
	}
	return q.Store && slices.Contains(kinds, q.StoreKind)
}

// PickTemplate returns the index of the winning candidate for location (LocationHeader,
// LocationMain, LocationFooter), or -1. Ties: higher score, higher priority, newer Modified,
// higher ID.
func PickTemplate(location string, cands []TemplateCandidate, q QueryFacts) int {
	best, bestScore := -1, 0
	for i := range cands {
		c := &cands[i]
		if TemplateLocation(c.Type) != location {
			continue
		}
		s, ok := TemplateScore(c, q)
		if !ok {
			continue
		}
		if best < 0 || beats(c, s, &cands[best], bestScore) {
			best, bestScore = i, s
		}
	}
	return best
}

// PickTemplates returns the indexes of every candidate of location that matches the request, best
// first (same order as PickTemplate): for locations that several templates fill at once, the
// popups (LocationPopup).
func PickTemplates(location string, cands []TemplateCandidate, q QueryFacts) []int {
	type hit struct{ i, score int }
	var hits []hit
	for i := range cands {
		c := &cands[i]
		if TemplateLocation(c.Type) != location {
			continue
		}
		if s, ok := TemplateScore(c, q); ok {
			hits = append(hits, hit{i, s})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool {
		x, y := hits[a], hits[b]
		if x.score != y.score {
			return x.score > y.score
		}
		return beats(&cands[x.i], x.score, &cands[y.i], y.score)
	})
	out := make([]int, len(hits))
	for i, h := range hits {
		out[i] = h.i
	}
	return out
}

func beats(a *TemplateCandidate, as int, b *TemplateCandidate, bs int) bool {
	if as != bs {
		return as > bs
	}
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	if !a.Modified.Equal(b.Modified) {
		return a.Modified.After(b.Modified)
	}
	return a.ID > b.ID
}

// TemplatesConflict reports whether two templates of the same location have include rules that
// can both match one view their types both render (a warning for GET RouteTemplateConflicts;
// excludes and scores are ignored). It errs on the side of reporting.
func TemplatesConflict(a, b *TemplateCandidate) bool {
	if a.ID == b.ID || TemplateLocation(a.Type) != TemplateLocation(b.Type) {
		return false
	}
	if TemplateLocation(a.Type) == LocationMain && IsStoreTemplate(a.Type) != IsStoreTemplate(b.Type) {
		return false // a store type and a generic type never render on the same view
	}
	for _, view := range []string{ViewSingular, ViewArchive, View404} {
		if !ViewAllowed(a.Type, view) || !ViewAllowed(b.Type, view) {
			continue
		}
		for _, ra := range a.Conditions {
			if ra.Exclude {
				continue
			}
			ca, ok := ra.constraint(view)
			if !ok || !archiveGateOK(a.Type, view, ca) {
				continue
			}
			for _, rb := range b.Conditions {
				if rb.Exclude {
					continue
				}
				if cb, ok := rb.constraint(view); ok && archiveGateOK(b.Type, view, cb) && ca.compatible(cb) &&
					archiveGateOK(a.Type, view, cb) && archiveGateOK(b.Type, view, ca) {
					return true
				}
			}
		}
	}
	return false
}

// archiveGateOK: on the archive view, a constraint can be met by a request the type's archive
// gate (TemplateTypeArchives) admits.
func archiveGateOK(t TemplateType, view string, c viewConstraint) bool {
	kinds, ok := TemplateTypeArchives[t]
	if view != ViewArchive || !ok {
		return true
	}
	for _, k := range kinds {
		if c.compatible(viewConstraint{kind: k}) {
			return true
		}
	}
	return false
}

// viewConstraint is what a rule requires of a request on one view ("" / 0 = anything).
type viewConstraint struct {
	kind  string // archive kind, or "page" for the static front page on a singular view
	ptype string
	tax   string
	id    int64 // entry, author or term id
	rel   bool  // in / child_of / by_author: compatible with any entry of the type
}

func (c Condition) constraint(view string) (viewConstraint, bool) {
	if neverMatchTypes[c.PostType] {
		return viewConstraint{}, false
	}
	switch c.Scope {
	case ScopeGeneral:
		return viewConstraint{}, true
	case Scope404:
		return viewConstraint{}, view == View404
	case ScopeFrontPage:
		switch view {
		case ViewSingular:
			return viewConstraint{ptype: "page"}, true
		case ViewArchive:
			return viewConstraint{kind: ArchiveHome}, true
		}
		return viewConstraint{}, false
	}
	if view == ViewSingular {
		switch c.Scope {
		case ScopeSingular:
			return viewConstraint{}, true
		case ScopePostType:
			return viewConstraint{ptype: c.PostType}, true
		case ScopeEntry:
			return viewConstraint{ptype: c.PostType, id: c.ID}, true
		case ScopeInTerm, ScopeChildOf, ScopeByAuthor:
			return viewConstraint{ptype: c.PostType, rel: true}, true
		}
		return viewConstraint{}, false
	}
	if view == ViewArchive {
		switch c.Scope {
		case ScopeArchive:
			return viewConstraint{}, true
		case ScopeHome:
			return viewConstraint{kind: ArchiveHome}, true
		case ScopeDate:
			return viewConstraint{kind: ArchiveDate}, true
		case ScopeSearch:
			return viewConstraint{kind: ArchiveSearch}, true
		case ScopeAuthor:
			return viewConstraint{kind: ArchiveAuthor, id: c.ID}, true
		case ScopeTypeArchive:
			return viewConstraint{kind: ArchivePostType, ptype: c.PostType}, true
		case ScopeTaxonomy:
			return viewConstraint{kind: ArchiveTerm, tax: c.Taxonomy, id: c.ID}, true
		}
	}
	return viewConstraint{}, false
}

func (a viewConstraint) compatible(b viewConstraint) bool {
	eq := func(x, y string) bool { return x == "" || y == "" || x == y }
	if !eq(a.kind, b.kind) || !eq(a.ptype, b.ptype) || !eq(a.tax, b.tax) {
		return false
	}
	if a.id != 0 && b.id != 0 && a.id != b.id && !a.rel && !b.rel {
		return false
	}
	return true
}
