package webbuilder

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseCondition(t *testing.T) {
	cases := []struct {
		rule  string
		scope string
		ptype string
		tax   string
		id    int64
		score int
		excl  bool
	}{
		{"include/general", ScopeGeneral, "", "", 0, ScoreGeneral, false},
		{"exclude/general", ScopeGeneral, "", "", 0, ScoreGeneral, true},
		{"include/404", Scope404, "", "", 0, ScoreSpecial, false},
		{"include/singular", ScopeSingular, "", "", 0, ScoreView, false},
		{"include/singular/front_page", ScopeFrontPage, "", "", 0, ScoreSpecial, false},
		{"include/singular/post", ScopePostType, "post", "", 0, ScoreType, false},
		{"include/singular/page/12", ScopeEntry, "page", "", 12, ScoreSpecificID, false},
		{"include/singular/post/in/category/3", ScopeInTerm, "post", "category", 3, ScoreRelation, false},
		{"include/singular/page/child_of/7", ScopeChildOf, "page", "", 7, ScoreRelation, false},
		{"include/singular/post/by_author/2", ScopeByAuthor, "post", "", 2, ScoreRelation, false},
		{"include/singular/post/in/in/3", ScopeInTerm, "post", "in", 3, ScoreRelation, false},
		{"include/archive", ScopeArchive, "", "", 0, ScoreView, false},
		{"include/archive/home", ScopeHome, "", "", 0, ScoreType, false},
		{"include/archive/date", ScopeDate, "", "", 0, ScoreType, false},
		{"include/archive/search", ScopeSearch, "", "", 0, ScoreType, false},
		{"include/archive/author", ScopeAuthor, "", "", 0, ScoreType, false},
		{"include/archive/author/5", ScopeAuthor, "", "", 5, ScoreSpecificID, false},
		{"include/archive/type/book", ScopeTypeArchive, "book", "", 0, ScoreType, false},
		{"include/archive/category", ScopeTaxonomy, "", "category", 0, ScoreType, false},
		{"exclude/archive/post_tag/4", ScopeTaxonomy, "", "post_tag", 4, ScoreSpecificID, true},
		{"include/singular/post/9223372036854775807", ScopeEntry, "post", "", 9223372036854775807, ScoreSpecificID, false},
	}
	for _, c := range cases {
		got, err := ParseCondition(c.rule)
		if err != nil {
			t.Errorf("%s: %v", c.rule, err)
			continue
		}
		if got.Scope != c.scope || got.PostType != c.ptype || got.Taxonomy != c.tax || got.ID != c.id || got.Exclude != c.excl || got.Score() != c.score || got.Raw != c.rule {
			t.Errorf("%s: %+v score %d", c.rule, got, got.Score())
		}
	}
	bad := []string{
		"", "general", "include", "include/", "both/general", "Include/general", "include/general/1",
		"include/404/1", "include/singular/post/0", "include/singular/post/012", "include/singular/Post",
		"include/singular/post/in/category", "include/singular/post/child_of", "include/singular/post/in/category/x",
		"include/archive/author/x", "include/archive/category/1/2", "include/singular/post/12/x",
		"include/singular/post/9223372036854775808", " include/general", "include/general ",
		"include/archive/" + strings.Repeat("a", 33), "include/singular/front_page/1",
		// Reserved words in the <ptype> / <tax> position.
		"include/singular/in", "include/singular/child_of", "include/singular/by_author/2", "include/singular/front_page/in/x/1",
		"include/archive/type", "include/archive/home/1", "include/archive/search/2", "include/archive/date/3",
		"include/archive/author/1/2", "include/archive/type/book/1", "include/singular/post/likes/2",
		"include/singular//1", "include//general", "include/singular/post/in/Cat/1",
		"include/" + strings.Repeat("x", MaxConditionBytes),
	}
	for _, r := range bad {
		if c, err := ParseCondition(r); err == nil {
			t.Errorf("accepted %q as %+v", r, c)
		}
	}
}

func TestNormalizeConditions(t *testing.T) {
	out, errs := NormalizeConditions([]string{" include/general ", "include/general", "exclude/singular/page/2", "nope", "include/singular/in"})
	if strings.Join(out, ",") != "include/general,exclude/singular/page/2" {
		t.Errorf("out %v", out)
	}
	if len(errs) != 2 || errs[0].Path != "/conditions/3" || errs[1].Path != "/conditions/4" || errs[0].Code != VErrCondition {
		t.Errorf("errs %+v", errs)
	}
	var many []string
	for i := 1; i <= MaxConditions+1; i++ {
		many = append(many, "include/singular/post/"+strconv.Itoa(i))
	}
	if _, errs := NormalizeConditions(many); len(errs) != 1 || errs[0].Path != "/conditions" || errs[0].Code != VErrTooMany {
		t.Errorf("too many: %+v", errs)
	}
	// Duplicates do not count toward the limit.
	dup := append(append([]string{}, many[:MaxConditions]...), many[0], many[1])
	if out, errs := NormalizeConditions(dup); len(errs) != 0 || len(out) != MaxConditions {
		t.Errorf("dups: %d %+v", len(out), errs)
	}
	if out, errs := NormalizeConditions(nil); out == nil || len(out) != 0 || errs != nil {
		t.Errorf("nil input: %#v %v", out, errs)
	}

	req, errs := ValidateConditionsRequest(TemplateConditionsRequest{Conditions: []string{"include/404"}, Priority: 101})
	if len(errs) != 1 || errs[0].Path != "/priority" || req.Priority != 101 {
		t.Errorf("priority: %+v", errs)
	}
	if _, errs := ValidateConditionsRequest(TemplateConditionsRequest{Conditions: []string{"include/404"}, Priority: -100}); len(errs) != 0 {
		t.Errorf("priority -100: %+v", errs)
	}
}

func TestConditionsMeta(t *testing.T) {
	if got := DecodeConditionsMeta(`["include/general","exclude/404"]`); strings.Join(got, ",") != "include/general,exclude/404" {
		t.Errorf("decode %v", got)
	}
	for _, bad := range []string{"", "null", `"x"`, `[1]`, `{}`} {
		if got := DecodeConditionsMeta(bad); len(got) != 0 {
			t.Errorf("decode %q = %v", bad, got)
		}
	}
	if EncodeConditionsMeta(nil) != "[]" || EncodeConditionsMeta([]string{"include/404"}) != `["include/404"]` {
		t.Error("encode")
	}
	if got := ParseConditions([]string{"include/general", "bogus", " exclude/404 "}); len(got) != 2 || !got[1].Exclude {
		t.Errorf("lenient parse %+v", got)
	}
}

// Every documented scope matches exactly the views it names.
func TestConditionMatch(t *testing.T) {
	post := QueryFacts{View: ViewSingular, PostType: "post", PostID: 10, AuthorID: 2, Terms: map[string][]int64{"category": {3, 4}, "post_tag": {9}}}
	page := QueryFacts{View: ViewSingular, PostType: "page", PostID: 12, ParentID: 7, AuthorID: 1}
	front := QueryFacts{View: ViewSingular, PostType: "page", PostID: 5, FrontPage: true}
	frontIndex := QueryFacts{View: ViewArchive, Archive: ArchiveHome, FrontPage: true}
	home := QueryFacts{View: ViewArchive, Archive: ArchiveHome}
	cat := QueryFacts{View: ViewArchive, Archive: ArchiveTerm, Taxonomy: "category", TermID: 3}
	author := QueryFacts{View: ViewArchive, Archive: ArchiveAuthor, AuthorID: 5}
	date := QueryFacts{View: ViewArchive, Archive: ArchiveDate}
	search := QueryFacts{View: ViewArchive, Archive: ArchiveSearch}
	book := QueryFacts{View: ViewSingular, PostType: "book", PostID: 99}
	books := QueryFacts{View: ViewArchive, Archive: ArchivePostType, PostType: "book"}
	nf := QueryFacts{View: View404}
	product := QueryFacts{View: ViewSingular, PostType: "product", PostID: 77}
	tpl := QueryFacts{View: ViewSingular, PostType: TemplatePostType, PostID: 78}
	views := map[string]QueryFacts{"post": post, "page": page, "front": front, "frontIndex": frontIndex, "home": home, "cat": cat, "author": author,
		"date": date, "search": search, "book": book, "books": books, "404": nf, "product": product, "tpl": tpl}

	want := map[string][]string{
		"include/general":                     {"post", "page", "front", "frontIndex", "home", "cat", "author", "date", "search", "book", "books", "404", "product", "tpl"},
		"include/404":                         {"404"},
		"include/singular":                    {"post", "page", "front", "book", "product", "tpl"},
		"include/singular/front_page":         {"front", "frontIndex"},
		"include/singular/post":               {"post"},
		"include/singular/page":               {"page", "front"},
		"include/singular/post/10":            {"post"},
		"include/singular/post/11":            {},
		"include/singular/post/in/category/4": {"post"},
		"include/singular/post/in/post_tag/4": {},
		"include/singular/page/child_of/7":    {"page"},
		"include/singular/post/by_author/2":   {"post"},
		"include/singular/page/by_author/2":   {},
		"include/singular/book":               {"book"},
		"include/singular/product":            {},
		"include/singular/wb_template":        {},
		"include/archive":                     {"frontIndex", "home", "cat", "author", "date", "search", "books"},
		"include/archive/home":                {"frontIndex", "home"},
		"include/archive/date":                {"date"},
		"include/archive/search":              {"search"},
		"include/archive/author":              {"author"},
		"include/archive/author/5":            {"author"},
		"include/archive/author/6":            {},
		"include/archive/type/book":           {"books"},
		"include/archive/type/product":        {},
		"include/archive/category":            {"cat"},
		"include/archive/category/3":          {"cat"},
		"include/archive/category/4":          {},
		"include/archive/post_tag":            {},
		"include/archive/genre":               {},
	}
	for rule, names := range want {
		c, err := ParseCondition(rule)
		if err != nil {
			t.Fatal(rule, err)
		}
		set := toSet(names)
		for name, q := range views {
			if got := c.Match(q); got != set[name] {
				t.Errorf("%s on %s: %v", rule, name, got)
			}
		}
	}
}

func conds(rules ...string) []Condition {
	out := ParseConditions(rules)
	if len(out) != len(rules) {
		panic("bad test rule in " + strings.Join(rules, " "))
	}
	return out
}

func TestScoreConditions(t *testing.T) {
	post := QueryFacts{View: ViewSingular, PostType: "post", PostID: 10, Terms: map[string][]int64{"category": {3}}}
	if s, ok := ScoreConditions(conds("include/general", "include/singular/post", "include/singular/post/in/category/3"), post); !ok || s != ScoreRelation {
		t.Errorf("best include: %d %v", s, ok)
	}
	if _, ok := ScoreConditions(conds("include/singular/post/10", "exclude/general"), post); ok {
		t.Error("exclude/general must veto")
	}
	if _, ok := ScoreConditions(conds("include/general", "exclude/singular/post/in/category/3"), post); ok {
		t.Error("exclude by term must veto")
	}
	if s, ok := ScoreConditions(conds("include/general", "exclude/singular/post/11"), post); !ok || s != ScoreGeneral {
		t.Error("non-matching exclude must not veto")
	}
	if _, ok := ScoreConditions(conds("exclude/singular/page"), post); ok {
		t.Error("only excludes match nothing")
	}
	if _, ok := ScoreConditions(nil, post); ok {
		t.Error("no rules match nothing")
	}
}

func TestPickTemplate(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	post := QueryFacts{View: ViewSingular, PostType: "post", PostID: 10}
	search := QueryFacts{View: ViewArchive, Archive: ArchiveSearch}
	cands := []TemplateCandidate{
		{ID: 1, Type: TemplateHeader, Conditions: conds("include/general"), Modified: t0},
		{ID: 2, Type: TemplateHeader, Conditions: conds("include/singular/post"), Modified: t0},
		{ID: 3, Type: TemplateFooter, Conditions: conds("include/general"), Modified: t0},
		{ID: 4, Type: TemplateSinglePost, Conditions: conds("include/general"), Modified: t0},
		{ID: 5, Type: TemplateArchive, Conditions: conds("include/general"), Modified: t0.Add(time.Hour)},
		{ID: 6, Type: TemplateArchive, Conditions: conds("include/archive"), Modified: t0},
		{ID: 7, Type: TemplateSearch, Conditions: conds("include/archive"), Modified: t0},
		{ID: 8, Type: Template404, Conditions: conds("include/404"), Modified: t0},
	}
	id := func(loc string, q QueryFacts) int64 {
		if i := PickTemplate(loc, cands, q); i >= 0 {
			return cands[i].ID
		}
		return 0
	}
	if id(LocationHeader, post) != 2 || id(LocationHeader, search) != 1 || id(LocationFooter, post) != 3 {
		t.Error("header/footer")
	}
	// View gate: an archive template with include/general never renders a single post.
	if id(LocationMain, post) != 4 {
		t.Errorf("main on post = %d", id(LocationMain, post))
	}
	// Search bonus: search-results beats archive with the same rule (and the newer general one).
	if id(LocationMain, search) != 7 {
		t.Errorf("main on search = %d", id(LocationMain, search))
	}
	cat := QueryFacts{View: ViewArchive, Archive: ArchiveTerm, Taxonomy: "category", TermID: 1}
	// Contract §11.2: search-results only passes the archive gate on search views, so a category
	// archive goes to the archive template with the same rule (6) despite the higher id (7).
	if id(LocationMain, cat) != 6 {
		t.Errorf("main on category = %d", id(LocationMain, cat))
	}
	if id(LocationMain, QueryFacts{View: View404}) != 8 {
		t.Error("404")
	}
	// Store views: no main template, header and footer still apply.
	store := QueryFacts{View: ViewSingular, PostType: "product", PostID: 3, Store: true}
	if id(LocationMain, store) != 0 || id(LocationHeader, store) != 1 {
		t.Error("store")
	}
	// Ties: priority, then modified, then id.
	tie := []TemplateCandidate{
		{ID: 10, Type: TemplateSinglePost, Conditions: conds("include/singular/post"), Priority: 0, Modified: t0.Add(2 * time.Hour)},
		{ID: 11, Type: TemplateSinglePost, Conditions: conds("include/singular/post"), Priority: 5, Modified: t0},
		{ID: 12, Type: TemplateSinglePost, Conditions: conds("include/singular/post"), Priority: 5, Modified: t0.Add(time.Hour)},
		{ID: 13, Type: TemplateSinglePost, Conditions: conds("include/singular/post"), Priority: 5, Modified: t0.Add(time.Hour)},
		{ID: 9, Type: TemplateSinglePost, Conditions: conds("include/singular/post"), Priority: 5, Modified: t0.Add(time.Hour)},
	}
	if i := PickTemplate(LocationMain, tie, post); i < 0 || tie[i].ID != 13 {
		t.Errorf("tie break picked %v", i)
	}
	// A more specific rule beats a higher priority.
	spec := []TemplateCandidate{
		{ID: 1, Type: TemplateSinglePost, Conditions: conds("include/singular/post"), Priority: 100},
		{ID: 2, Type: TemplateSinglePost, Conditions: conds("include/singular/post/10"), Priority: -100},
	}
	if i := PickTemplate(LocationMain, spec, post); spec[i].ID != 2 {
		t.Error("score must win over priority")
	}
	// The front page: front-page passes the archive gate on a posts-index front page.
	fp := []TemplateCandidate{
		{ID: 1, Type: TemplateArchive, Conditions: conds("include/archive/home")},
		{ID: 2, Type: TemplateFrontPage, Conditions: conds("include/singular/front_page")},
	}
	if i := PickTemplate(LocationMain, fp, QueryFacts{View: ViewArchive, Archive: ArchiveHome, FrontPage: true}); fp[i].ID != 2 {
		t.Error("front page on posts index")
	}
	if i := PickTemplate(LocationMain, fp, QueryFacts{View: ViewArchive, Archive: ArchiveHome}); fp[i].ID != 1 {
		t.Error("posts index off the front")
	}
	if PickTemplate(LocationMain, nil, post) != -1 {
		t.Error("empty")
	}
}

func TestTemplatesConflict(t *testing.T) {
	mk := func(id int64, typ TemplateType, rules ...string) *TemplateCandidate {
		return &TemplateCandidate{ID: id, Type: typ, Conditions: conds(rules...)}
	}
	cases := []struct {
		a, b *TemplateCandidate
		want bool
	}{
		{mk(1, TemplateHeader, "include/general"), mk(2, TemplateHeader, "include/singular/post"), true},
		{mk(1, TemplateHeader, "include/general"), mk(1, TemplateHeader, "include/general"), false}, // itself
		{mk(1, TemplateHeader, "include/general"), mk(2, TemplateFooter, "include/general"), false},
		{mk(1, TemplateSinglePost, "include/general"), mk(2, TemplateArchive, "include/general"), false}, // view gates
		{mk(1, TemplateSinglePost, "include/singular/post"), mk(2, TemplateSinglePage, "include/singular/page"), false},
		{mk(1, TemplateSinglePost, "include/singular/post/1"), mk(2, TemplateSinglePost, "include/singular/post/2"), false},
		{mk(1, TemplateSinglePost, "include/singular/post/1"), mk(2, TemplateSinglePost, "include/singular/post/in/category/2"), true},
		{mk(1, TemplateSinglePost, "include/singular/post"), mk(2, TemplateSinglePost, "exclude/general"), false},
		{mk(1, TemplateArchive, "include/archive/category/1"), mk(2, TemplateArchive, "include/archive/category"), true},
		{mk(1, TemplateArchive, "include/archive/category/1"), mk(2, TemplateArchive, "include/archive/category/2"), false},
		{mk(1, TemplateArchive, "include/archive/category"), mk(2, TemplateArchive, "include/archive/post_tag"), false},
		{mk(1, TemplateArchive, "include/archive/search"), mk(2, TemplateSearch, "include/archive"), true},
		{mk(1, TemplateArchive, "include/archive/date"), mk(2, TemplateSearch, "include/archive/search"), false},
		{mk(1, TemplateArchive, "include/archive/category"), mk(2, TemplateSearch, "include/archive"), false}, // search gate
		{mk(1, TemplateArchive, "include/archive"), mk(2, TemplateSearch, "include/general"), true},
		{mk(1, TemplateSearch, "include/archive/date"), mk(2, TemplateSearch, "include/archive"), false},
		{mk(1, TemplateFrontPage, "include/singular/front_page"), mk(2, TemplateArchive, "include/archive/home"), true},
		{mk(1, TemplateFrontPage, "include/singular/front_page"), mk(2, TemplateSinglePage, "include/singular/page/4"), true},
		{mk(1, TemplateFrontPage, "include/singular/front_page"), mk(2, TemplateSinglePost, "include/singular/post"), false},
		{mk(1, Template404, "include/404"), mk(2, Template404, "include/general"), true},
		{mk(1, TemplateHeader, "include/404"), mk(2, TemplateHeader, "include/singular"), false},
		{mk(1, TemplateSingle, "include/singular/product"), mk(2, TemplateSingle, "include/singular"), false},
	}
	for i, c := range cases {
		if got := TemplatesConflict(c.a, c.b); got != c.want {
			t.Errorf("case %d: got %v", i, got)
		}
		if got := TemplatesConflict(c.b, c.a); got != c.want {
			t.Errorf("case %d (swapped): got %v", i, got)
		}
	}
}

// The parser and ConditionPattern agree (the parser additionally refuses reserved words and
// ids outside int64).
func checkConditionAgainstPattern(t *testing.T, rule string) {
	re := regexp.MustCompile(ConditionPattern)
	c, err := ParseCondition(rule)
	if err == nil && !re.MatchString(rule) {
		t.Fatalf("parser accepted %q, pattern did not", rule)
	}
	if err == nil {
		if c.Score() == 0 || len(rule) > MaxConditionBytes {
			t.Fatalf("accepted %q with score %d", rule, c.Score())
		}
		// Re-serializing the parse gives the rule back.
		if got := c.String(); got != rule {
			t.Fatalf("String() = %q for %q", got, rule)
		}
	}
	if err != nil && re.MatchString(rule) {
		parts := strings.Split(rule, "/")
		reserved := false
		for i, p := range parts {
			if i >= 2 && (i == 2 && (parts[1] == "singular" && toSet(ReservedSingularWords)[p] || parts[1] == "archive" && toSet(ReservedArchiveWords)[p])) {
				reserved = true
			}
		}
		last := parts[len(parts)-1]
		if !reserved && !(len(last) == 19 && last > "9223372036854775807") {
			t.Fatalf("pattern accepted %q, parser refused: %v", rule, err)
		}
	}
}

func TestConditionPatternAgreement(t *testing.T) {
	for _, r := range []string{"include/singular/post/in/category/3", "include/archive/type", "include/singular/in", "include/archive/author/5", "include/singular/post/9999999999999999999", "include/archive/home"} {
		checkConditionAgainstPattern(t, r)
	}
}

// Rules naming a post, parent or term match the aliases (Pursae Multi Language translations).
func TestConditionMatchAliases(t *testing.T) {
	parse := func(rule string) Condition {
		c := ParseConditions([]string{rule})
		if len(c) != 1 {
			t.Fatalf("parse %q", rule)
		}
		return c[0]
	}
	single := QueryFacts{View: ViewSingular, PostType: "page", PostID: 40, PostIDs: []int64{12}, ParentID: 41, ParentIDs: []int64{13}}
	if !parse("include/singular/page/12").Match(single) || !parse("include/singular/page/40").Match(single) || parse("include/singular/page/14").Match(single) {
		t.Error("entry aliases")
	}
	if !parse("include/singular/page/child_of/13").Match(single) || parse("include/singular/page/child_of/12").Match(single) {
		t.Error("parent aliases")
	}
	orphan := QueryFacts{View: ViewSingular, PostType: "page", PostID: 40}
	if parse("include/singular/page/child_of/40").Match(orphan) {
		t.Error("child_of matched a page without parent")
	}
	arch := QueryFacts{View: ViewArchive, Archive: ArchiveTerm, Taxonomy: "category", TermID: 9, TermIDs: []int64{5}}
	if !parse("include/archive/category/5").Match(arch) || !parse("include/archive/category/9").Match(arch) || parse("include/archive/category/6").Match(arch) {
		t.Error("term aliases")
	}
}
