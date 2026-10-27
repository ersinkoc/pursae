package builder

// gap-loops2 goldens: term loops (Loop Grid Post Taxonomy skin and the atomic Loop's taxonomy
// template type), classic widgets with dynamic tags inside atomic Loop items, and the Page Title
// tag's "show home title" option. Lab context: the loop item template 340 (archive title / url /
// description tags) and the lab's child category "Old News" (22, under News, empty).

import (
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
)

// loops2LabClasses is get_post_class() of the lab posts whose ids equal the term ids (the loop
// item wrapper's upstream quirk).
var loops2LabClasses = map[int64]string{
	1:  "post-1 post type-post status-publish format-standard hentry category-uncategorized entry",
	17: "post-17 revision type-revision status-inherit hentry entry",
	18: "post-18 revision type-revision status-inherit hentry entry",
	20: "post-20 revision type-revision status-inherit hentry entry",
	22: "post-22 revision type-revision status-inherit hentry entry",
}

var loops2OldNews = &TermItem{ID: 22, Taxonomy: "category", Name: "Old News", Slug: "old-news", URL: "http://localhost:8095/category/news/old-news/",
	ParentID: 17, Description: "Older stories, archived."}

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
		baseTpl, baseTerms, baseTerm, baseClasses := pro.Template, pro.Terms, pro.TermByID, pro.LoopItemClasses
		pro.Template = func(id int64) *Document {
			if id == 340 && proDActive {
				raw, err := os.ReadFile("testdata/golden/pro-d-loop-340.json")
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
		pro.Terms = func(tax string, args map[string]any) []*TermItem {
			var out []*TermItem
			if baseTerms != nil {
				out = baseTerms(tax, args)
			}
			if proDActive && tax == "category" {
				// HierarchicalTerms order: the child after its parent.
				var withChild []*TermItem
				for _, t := range out {
					withChild = append(withChild, t)
					if t.ID == 17 {
						cp := *loops2OldNews
						withChild = append(withChild, &cp)
					}
				}
				out = withChild
			}
			return out
		}
		pro.TermByID = func(id int64) *TermItem {
			if id == 22 && proDActive {
				cp := *loops2OldNews
				return &cp
			}
			if baseTerm != nil {
				return baseTerm(id)
			}
			return nil
		}
		pro.LoopItemClasses = func(id int64, typ string) string {
			if proDActive && typ == "" {
				return loops2LabClasses[id]
			}
			if baseClasses != nil {
				return baseClasses(id, typ)
			}
			return ""
		}
	}
}

// TestLoopTerms renders the lab's term loop page: Post Taxonomy skin grids (default, tags by id
// with an offset, manual selection with hide empty, parent ignored, exclusions with a limit, depth
// filter, avoid duplicates), atomic term loops with a classic heading inside the items, an atomic
// post loop with a classic heading (dynamic tags per item) and a search form.
func TestLoopTerms(t *testing.T) {
	raw, err := os.ReadFile("testdata/golden/loops/terms.data.json")
	if err != nil {
		t.Fatal(err)
	}
	els, err := ParseElements(raw)
	if err != nil {
		t.Fatal(err)
	}
	doc := &Document{ID: 343, Type: "wp-page", PostType: "page", Elements: els, Settings: map[string]any{"__fixture_name": "pro-d-loops2-terms"}}
	rc := testRC(t)
	loopRequest(rc, fixture{doc: doc}, "", false, "")
	rc.Pro.CurrentURL = "http://localhost:8095/?page_id=343"
	res, err := Render(doc, rc)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile("testdata/golden/loops/terms.html")
	if got, w := normalizeHTML(res.HTML), normalizeHTML(string(want)); got != w {
		t.Errorf("HTML differs%s", firstDiff(got, w))
	}
}

func TestLoopGetTerms(t *testing.T) {
	terms := []*TermItem{
		{ID: 1, Taxonomy: "category", Name: "Uncategorized", Count: 2},
		{ID: 17, Taxonomy: "category", Name: "News", Count: 0},
		{ID: 22, Taxonomy: "category", Name: "Old News", ParentID: 17, Count: 1},
		{ID: 23, Taxonomy: "category", Name: "Older", ParentID: 22, Count: 0},
		{ID: 18, Taxonomy: "category", Name: "guides", Count: 3},
	}
	rc := &RenderContext{Pro: &ProData{Terms: func(string, map[string]any) []*TermItem { return terms },
		TermByID: func(id int64) *TermItem {
			for _, x := range terms {
				if x.ID == id {
					return x
				}
			}
			return nil
		}}}
	r := newRenderer(&Document{}, rc)
	names := func(list []*TermItem) string {
		var out []string
		for _, x := range list {
			out = append(out, x.Name)
		}
		return strings.Join(out, ",")
	}
	for _, c := range []struct {
		a    loopTermArgs
		want string
	}{
		{loopTermArgs{Taxonomy: "category", OrderBy: "name", Order: "DESC"}, "Uncategorized,Older,Old News,News,guides"},
		{loopTermArgs{Taxonomy: "category", OrderBy: "name", HideEmpty: true}, "guides,News,Old News,Uncategorized"},
		{loopTermArgs{Taxonomy: "category", OrderBy: "term_id", ChildOf: 17}, "Old News,Older"},
		{loopTermArgs{Taxonomy: "category", OrderBy: "term_id", ChildOf: 18}, ""},
		{loopTermArgs{Taxonomy: "category", OrderBy: "term_id", Include: []int64{18, 22}, Exclude: []int64{18}}, "guides,Old News"},
		{loopTermArgs{Taxonomy: "category", OrderBy: "term_id", Exclude: []int64{18}, Offset: 1, Number: 2}, "News,Old News"},
		{loopTermArgs{Taxonomy: "category", OrderBy: "term_id", Offset: 9, Number: 2}, ""},
	} {
		if got := names(r.loopGetTerms(c.a)); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.a, got, c.want)
		}
	}
	if got := names(loopTermsByDepth(r.loopGetTerms(loopTermArgs{Taxonomy: "category", OrderBy: "term_id"}), 1)); got != "Uncategorized,News,Old News,guides" {
		t.Errorf("depth 2: got %q", got)
	}
	if got := names(r.loopAtomicTerms(map[string]any{"taxonomy_hierarchical": true, "taxonomy_depth": "2", "taxonomy_orderby": "term_id",
		"taxonomy_order": "asc", "posts_per_page": 10})); got != "Uncategorized,News,guides,Old News" {
		t.Errorf("atomic depth 2: got %q", got)
	}
}

// TestPageTitleHome renders the lab's archive template on the blog home (Twenty Twenty-One, site
// "Lab", no tagline): Page Title prints nothing there unless "show home title" is on, and then the
// document title (with before text, context ignored).
func TestPageTitleHome(t *testing.T) {
	raw, err := os.ReadFile("testdata/golden/loops/home-title.data.json")
	if err != nil {
		t.Fatal(err)
	}
	els, err := ParseElements(raw)
	if err != nil {
		t.Fatal(err)
	}
	doc := &Document{ID: 361, Type: "archive", Elements: els, Settings: map[string]any{}}
	rc := testRC(t)
	th := ThemeData{SiteName: "Lab"}
	if rc.Theme != nil {
		th = *rc.Theme
	}
	th.Post = func() *PostData { return nil }
	th.Archive = func() *ArchiveData {
		return &ArchiveData{Title: "Blog", PlainTitle: "Blog", Page: 1, Pages: 1, Home: true, HomeTitle: "Lab"}
	}
	rc.Theme = &th
	res, err := Render(doc, rc)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile("testdata/golden/loops/home-title.html")
	inner := func(s string) string {
		s = normalizeHTML(s)
		if i := strings.Index(s, `<div class="elementor-element elementor-element-h2f0001`); i >= 0 {
			s = s[i:]
		}
		return strings.TrimSuffix(s, "</div>")
	}
	if g, w := inner(res.HTML), inner(string(want)); g != w {
		t.Errorf("HTML differs%s", firstDiff(g, w))
	}
	// Not the blog home: the archive's own title.
	th.Archive = func() *ArchiveData {
		return &ArchiveData{Title: "Category: News", PlainTitle: "News", Page: 1, Pages: 1}
	}
	r := newRenderer(doc, rc)
	if got := r.pageTitle(false, false); got != "News" {
		t.Errorf("category archive: got %q", got)
	}
}

// TestSearchPropsQuery runs the query a search results page uses when the lab's Search widget
// (posts in Guides) submitted it, against the lab posts (testdata/golden/loops/search-props.json:
// the lab's result titles; an unknown widget is a 404, answered by the server).
func TestSearchPropsQuery(t *testing.T) {
	raw, err := os.ReadFile("testdata/golden/loops/terms.data.json")
	if err != nil {
		t.Fatal(err)
	}
	els, err := ParseElements(raw)
	if err != nil {
		t.Fatal(err)
	}
	doc := &Document{ID: 343, Type: "wp-page", PostType: "page", Elements: els, Settings: map[string]any{"__fixture_name": "pro-d-loops2-terms"}}
	var cases []struct {
		Query  string
		Status int
		Titles []string
	}
	gold, err := os.ReadFile("testdata/golden/loops/search-props.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(gold, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		v, _ := url.ParseQuery(c.Query)
		widget, _, _ := strings.Cut(v.Get("e_search_props"), "-")
		el := FindElement(doc.Elements, widget)
		if el == nil {
			if c.Status != 404 {
				t.Errorf("%s: widget missing", c.Query)
			}
			continue
		}
		rc := testRC(t)
		loopRequest(rc, fixture{doc: doc}, "", false, "")
		q := SearchPropsQuery(el, doc, rc)
		if q.PerPage != -1 || len(q.PostTypes) != 1 || q.PostTypes[0] != "post" {
			t.Errorf("%s: query %+v", c.Query, q)
		}
		q.Search = v.Get("s")
		var got []string
		for _, p := range rc.Pro.Query(q).Posts {
			got = append(got, p.Title)
		}
		if strings.Join(got, "|") != strings.Join(c.Titles, "|") {
			t.Errorf("%s: got %q, want %q", c.Query, got, c.Titles)
		}
	}
}
