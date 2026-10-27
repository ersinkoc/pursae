package builder

// Fixture context for the pro-d-loops-* goldens (loop builder: alternate and static templates,
// dynamic CSS, avoid duplicates, term includes, manual selection, live search): the loop item
// templates 315 and 318 (testdata/golden/pro-d-loop-<id>.json) and the lab's terms by id.

import (
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
)

func init() {
	prevRC := proTestRC
	proTestRC = func(rc *RenderContext) {
		if prevRC != nil {
			prevRC(rc)
		}
		if rc.Pro == nil {
			return
		}
		pro := rc.Pro
		// Every fixture is its own request: a fresh avoid list and loop CSS state.
		if inner := proFixtureCurrent; inner != nil {
			proFixtureCurrent = func(doc *Document) {
				inner(doc)
				pro.Loop = nil
			}
		}
		baseTpl, baseTerm, baseQuery := pro.Template, pro.TermByID, pro.Query
		// The lab posts narrowed by LoopQueryExt (tax_query, search); the pro-d context orders,
		// excludes and paginates.
		pro.Query = func(q *PostQuery) *PostList {
			if baseQuery == nil {
				return nil
			}
			if !proDActive || q == nil || (q.Ext == nil && q.Search == "") {
				return baseQuery(q)
			}
			posts, terms := proDLoadLab()
			cp := *q
			var allowed []int64
			for _, p := range posts {
				if loopTestMatch(p, q, terms) {
					allowed = append(allowed, p.ID)
				}
			}
			if len(q.IncludeIDs) > 0 {
				var both []int64
				for _, id := range allowed {
					if containsID(q.IncludeIDs, id) {
						both = append(both, id)
					}
				}
				allowed = both
			}
			if len(allowed) == 0 {
				allowed = []int64{-1}
			}
			cp.IncludeIDs = allowed
			return baseQuery(&cp)
		}
		pro.Template = func(id int64) *Document {
			if id == 315 || id == 318 {
				raw, err := os.ReadFile("testdata/golden/pro-d-loop-" + itoa64(id) + ".json")
				if err != nil {
					return nil
				}
				els, err := ParseElements(raw)
				if err != nil {
					return nil
				}
				return &Document{ID: id, Type: "loop-item", Elements: els, Settings: map[string]any{}}
			}
			if baseTpl != nil {
				return baseTpl(id)
			}
			return nil
		}
		pro.TermByID = func(id int64) *TermItem {
			_, terms := proDLoadLab()
			for _, t := range terms {
				if t.ID == id {
					return &TermItem{ID: t.ID, Taxonomy: t.Taxonomy, Name: t.Name, Slug: t.Slug, URL: t.URL, Count: t.Count, ParentID: t.ParentID}
				}
			}
			if baseTerm != nil {
				return baseTerm(id)
			}
			return nil
		}
	}
}

// loopTestMatch applies a query's tax clauses and search to a lab post.
func loopTestMatch(p *proDLabPost, q *PostQuery, terms []proDLabTerm) bool {
	if q.Search != "" {
		hay := strings.ToLower(p.Title + " " + p.Excerpt + " " + p.RawContent)
		for _, w := range strings.Fields(strings.ToLower(q.Search)) {
			if !strings.Contains(hay, w) {
				return false
			}
		}
	}
	if q.Ext == nil {
		return true
	}
	for _, cl := range q.Ext.Tax {
		if !loopTestClause(p, cl, terms) {
			return false
		}
	}
	return true
}

func loopTestClause(p *proDLabPost, cl LoopTaxClause, terms []proDLabTerm) bool {
	if len(cl.Or) > 0 {
		for _, sub := range cl.Or {
			if loopTestClause(p, sub, terms) {
				return true
			}
		}
		return false
	}
	has := map[int64]bool{}
	for _, t := range append(append([]proDLabTerm{}, p.Categories...), p.Tags...) {
		has[t.ID] = true
	}
	var ids []int64
	ids = append(ids, cl.TermIDs...)
	for _, s := range cl.Slugs {
		for _, t := range terms {
			if t.Taxonomy == cl.Taxonomy && t.Slug == s {
				ids = append(ids, t.ID)
			}
		}
	}
	any, all := false, len(ids) > 0
	for _, id := range ids {
		if has[id] {
			any = true
		} else {
			all = false
		}
	}
	switch cl.Op {
	case "NOT IN":
		return !any
	case "AND":
		return all
	}
	return any
}

// loopFixture returns the pro-d-loops-1 fixture.
func loopFixture(t *testing.T) fixture {
	t.Helper()
	for _, f := range loadFixtures(t) {
		if f.name == "pro-d-loops-1" {
			return f
		}
	}
	t.Fatal("pro-d-loops-1 fixture missing")
	return fixture{}
}

// loopRequest makes rc answer for a request with query string q (refresh requests: rest).
func loopRequest(rc *RenderContext, f fixture, raw string, rest bool, referer string) {
	q, _ := url.ParseQuery(raw)
	if proFixtureCurrent != nil {
		proFixtureCurrent(f.doc)
	}
	rc.Pro.Request = func(kind, name string) string {
		if kind == "get" {
			return q.Get(name)
		}
		return ""
	}
	rc.Pro.Loop = &LoopState{Query: q, RawQuery: raw, Filters: ParseLoopFilters(q), REST: rest}
	if rest {
		if u, err := url.Parse(referer); err == nil {
			rc.Pro.Loop.RefererQuery = u.RawQuery
		}
	}
}

// TestLoopFilteredPages compares the fixture page rendered for loop filter and pagination query
// strings with the lab's pages (testdata/golden/loops/*.html).
func TestLoopFilteredPages(t *testing.T) {
	f := loopFixture(t)
	for name, qs := range map[string]string{
		"filter-news":   "e-filter-lp00001-category=news",
		"filter-or":     "e-filter-lp00001-category=guides~news",
		"filter-and":    "e-filter-lp00001-category=guides+news",
		"page-alt-2":    "e-page-lp00001=2",
		"page-static-2": "e-page-lp00002=2",
	} {
		want, err := os.ReadFile("testdata/golden/loops/" + name + ".html")
		if err != nil {
			t.Fatal(err)
		}
		rc := testRC(t)
		loopRequest(rc, f, qs, false, "")
		res, err := Render(f.doc, rc)
		if err != nil {
			t.Fatal(err)
		}
		if got, w := normalizeHTML(res.HTML), normalizeHTML(string(want)); got != w {
			t.Errorf("%s: HTML differs%s", name, firstDiff(got, w))
		}
	}
}

// TestLoopRefreshEndpoints compares refresh-loop and refresh-search output with the lab's REST
// responses (testdata/golden/loops/*.json).
func TestLoopRefreshEndpoints(t *testing.T) {
	f := loopFixture(t)
	page := "http://localhost:8095/wpgo-fx-pro-d-loops-1/"
	read := func(name string) map[string]string {
		raw, err := os.ReadFile("testdata/golden/loops/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	for _, c := range []struct {
		name, referer string
		filter        *LoopFilter
	}{
		{"loop-guides", page, &LoopFilter{Terms: []string{"guides"}, Join: "AND"}},
		{"loop-guides-or-news", page + "?e-filter-lp00001-category=guides", &LoopFilter{Terms: []string{"guides", "news"}, Join: "OR"}},
	} {
		rc := testRC(t)
		loopRequest(rc, f, "", true, c.referer)
		rc.Pro.Loop.Filters = map[string]map[string]*LoopFilter{"lp00001": {"category": c.filter}}
		rc.Pro.Loop.BaseURL = page
		got := RenderWidgetFront(FindElement(f.doc.Elements, "lp00001"), f.doc, rc)
		if g, w := normalizeHTML(got), normalizeHTML(read(c.name)["data"]); g != w {
			t.Errorf("%s: differs%s", c.name, firstDiff(g, w))
		}
	}
	for _, c := range []struct {
		name, term string
		page       int
	}{{"search-gopher-1", "gopher", 1}, {"search-a-1", "a", 1}, {"search-a-2", "a", 2}, {"search-zzzz-1", "zzzz", 1}} {
		rc := testRC(t)
		loopRequest(rc, f, "", true, page)
		rc.Pro.Loop.Search = &LoopSearch{Term: c.term, Page: c.page, Breakpoint: "desktop"}
		data, pagination := RenderSearchResults(FindElement(f.doc.Elements, "se00001"), f.doc, rc)
		want := read(c.name)
		if g, w := normalizeHTML(data), normalizeHTML(want["data"]); g != w {
			t.Errorf("%s: data differs%s", c.name, firstDiff(g, w))
		}
		if pagination != want["pagination"] {
			t.Errorf("%s: pagination differs\n got: %q\nwant: %q", c.name, pagination, want["pagination"])
		}
	}
}

// TestAtomicLoop renders the lab's atomic Loop page (pages 1 and 2) with the lab posts.
func TestAtomicLoop(t *testing.T) {
	for name, qs := range map[string]string{"v4-loop-1": "", "v4-loop-1-page-2": "e-page-cl00001=2",
		"v4-loop-2": "", "v4-loop-2-page-2": "e-page-cm00001=2", "v4-loop-2-page-3": "e-page-cm00001=3"} {
		fx, _, _ := strings.Cut(name, "-page")
		raw, err := os.ReadFile("testdata/golden/loops/" + fx + ".data.json")
		if err != nil {
			t.Fatal(err)
		}
		id := int64(334)
		if fx == "v4-loop-2" {
			id = 339
		}
		els, err := ParseElements(raw)
		if err != nil {
			t.Fatal(err)
		}
		doc := &Document{ID: id, Type: "wp-page", PostType: "page", Elements: els, Settings: map[string]any{"__fixture_name": "pro-d-v4-loop-1"}}
		rc := testRC(t)
		loopRequest(rc, fixture{doc: doc}, qs, false, "")
		rc.Pro.CurrentURL = "http://localhost:8095/wpgo-fx-" + fx + "/"
		if qs != "" {
			rc.Pro.CurrentURL += "?" + qs
		}
		res, err := Render(doc, rc)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := os.ReadFile("testdata/golden/loops/" + name + ".html")
		if got, w := normalizeHTML(res.HTML), normalizeHTML(string(want)); got != w {
			t.Errorf("%s: HTML differs%s", name, firstDiff(got, w))
		}
	}
}

func TestParseLoopFilters(t *testing.T) {
	q, _ := url.ParseQuery("e-filter-lp00001-category=guides~news&e-filter-lp00001-post_tag=go+speed&e-filter-bad-x=1&e-page-lp00001=2")
	f := ParseLoopFilters(q)
	if c := f["lp00001"]["category"]; c == nil || c.Join != "OR" || strings.Join(c.Terms, ",") != "guides,news" {
		t.Errorf("category: %+v", c)
	}
	if c := f["lp00001"]["post_tag"]; c == nil || c.Join != "AND" || strings.Join(c.Terms, ",") != "go,speed" {
		t.Errorf("post_tag: %+v", c)
	}
	if len(f) != 1 || len(f["lp00001"]) != 2 {
		t.Errorf("unexpected filters %v", f)
	}
}
