package server

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestHGParseQS(t *testing.T) {
	v, _ := url.ParseQuery("filters[title][$eq]=Hello&filters[$or][0][views][$gt]=10&filters[$or][1][slug][$in][0]=a&filters[$or][1][slug][$in][1]=b" +
		"&sort[0]=title:asc&sort[1]=views:desc&fields[]=title&fields[]=slug&populate=*&pagination[page]=2&tags[$in]=x&tags[$in]=y")
	qs := hgParseQS(v)
	f := qs["filters"].(map[string]any)
	if f["title"].(map[string]any)["$eq"] != "Hello" {
		t.Fatalf("filters.title: %#v", f["title"])
	}
	or := hgList(f["$or"])
	if len(or) != 2 {
		t.Fatalf("$or: %#v", f["$or"])
	}
	in := hgList(or[1].(map[string]any)["slug"].(map[string]any)["$in"])
	if !reflect.DeepEqual(in, []any{"a", "b"}) {
		t.Fatalf("$in: %#v", in)
	}
	if got := hgStrings(qs["fields"]); !reflect.DeepEqual(got, []string{"title", "slug"}) {
		t.Fatalf("fields: %#v", got)
	}
	if got := hgList(qs["tags"].(map[string]any)["$in"]); len(got) != 2 {
		t.Fatalf("repeated key: %#v", got)
	}
	if qs["populate"] != "*" {
		t.Fatalf("populate: %#v", qs["populate"])
	}
	if got := hgSplitKey("a[b][c]"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("split: %v", got)
	}
}

func TestHGPaginationSortPopulate(t *testing.T) {
	p, err := hgParsePagination(map[string]any{"page": "3", "pageSize": "500"}, 25, 100)
	if err != nil || p.Page != 3 || p.PageSize != 100 {
		t.Fatalf("page: %+v %v", p, err)
	}
	if l, o := p.limitOffset(); l != 100 || o != 200 {
		t.Fatalf("limit/offset %d %d", l, o)
	}
	m := p.meta(250)
	if m["pageCount"] != 3 || m["total"] != 250 {
		t.Fatalf("meta: %v", m)
	}
	p, err = hgParsePagination(map[string]any{"start": "10", "limit": "5"}, 25, 100)
	if err != nil || !p.Offset || p.Start != 10 || p.Limit != 5 {
		t.Fatalf("start/limit: %+v %v", p, err)
	}
	if m := p.meta(42); m["start"] != 10 || m["limit"] != 5 || m["total"] != 42 {
		t.Fatalf("offset meta: %v", m)
	}
	if _, err := hgParsePagination(map[string]any{"page": "1", "limit": "5"}, 25, 100); err == nil {
		t.Fatal("mixed pagination accepted")
	}
	if _, err := hgParsePagination(map[string]any{"page": "0"}, 25, 100); err == nil {
		t.Fatal("page 0 accepted")
	}
	s, err := hgParseSort("title:asc,views:desc")
	if err != nil || len(s) != 2 || s[1].Field != "views" || !s[1].Desc {
		t.Fatalf("sort: %+v %v", s, err)
	}
	s, _ = hgParseSort(map[string]any{"0": "a", "1": "b:desc"})
	if len(s) != 2 || s[0].Desc || !s[1].Desc {
		t.Fatalf("sort list: %+v", s)
	}
	s, _ = hgParseSort(map[string]any{"title": "desc"})
	if len(s) != 1 || s[0].Field != "title" || !s[0].Desc {
		t.Fatalf("sort object: %+v", s)
	}
	if _, err := hgParseSort("title:sideways"); err == nil {
		t.Fatal("bad order accepted")
	}
	pop, err := hgParsePopulate("author.avatar,cover")
	if err != nil || pop.Populate["author"] == nil || pop.Populate["author"].Populate["avatar"] == nil || pop.Populate["cover"] == nil {
		t.Fatalf("populate list: %+v %v", pop, err)
	}
	v, _ := url.ParseQuery("populate[author][fields][0]=name&populate[author][populate]=*&populate[blocks][on][shared.quote][populate]=*&populate[cover]=true")
	pop, err = hgParsePopulate(hgParseQS(v)["populate"])
	if err != nil {
		t.Fatal(err)
	}
	if a := pop.Populate["author"]; a == nil || !a.All || !reflect.DeepEqual(a.Fields, []string{"name"}) {
		t.Fatalf("populate author: %+v", a)
	}
	if b := pop.Populate["blocks"]; b == nil || b.On["shared.quote"] == nil || !b.On["shared.quote"].All {
		t.Fatalf("populate on: %+v", b)
	}
	if _, err := hgParsePopulate(map[string]any{"x": map[string]any{"bogus": "1"}}); err == nil {
		t.Fatal("unknown populate key accepted")
	}
}

func hgUnitRegistry() *hgRegistry {
	reg := &hgRegistry{all: map[string]*hgSchema{}}
	add := func(sc *hgSchema) {
		reg.all[sc.UID] = sc
		reg.order = append(reg.order, sc)
	}
	add(&hgSchema{UID: "shared.seo", Kind: hgKindComponent, DisplayName: "SEO", Fields: []hgField{{Name: "metaTitle", Type: "text"}}})
	add(&hgSchema{UID: "author", Kind: hgKindCollection, DisplayName: "Author", SingularName: "author", PluralName: "authors",
		Fields: []hgField{{Name: "name", Type: "text"}, {Name: "secret", Type: "text", Private: true}}})
	add(&hgSchema{UID: "article", Kind: hgKindCollection, DisplayName: "Article", SingularName: "article", PluralName: "articles", DraftAndPublish: true,
		Fields: []hgField{{Name: "title", Type: "text"}, {Name: "views", Type: "number", Format: "integer"}, {Name: "featured", Type: "boolean"},
			{Name: "author", Type: "relation", Relation: "manyToOne", Target: "author"}, {Name: "seo", Type: "component", Component: "shared.seo"},
			{Name: "faq", Type: "component", Component: "shared.seo", Repeatable: true}, {Name: "date", Type: "datetime"}}})
	return reg
}

func TestHGFilterSQL(t *testing.T) {
	reg := hgUnitRegistry()
	sc := reg.Get("article")
	v, _ := url.ParseQuery("filters[title][$containsi]=go_1%25&filters[views][$between][0]=1&filters[views][$between][1]=9" +
		"&filters[$or][0][featured][$eq]=true&filters[$or][1][author][name][$startsWith]=A&filters[seo][metaTitle][$null]=true" +
		"&filters[faq][metaTitle][$eq]=Q&filters[$not][title][$eq]=x&filters[id][$in][0]=3&filters[date][$gt]=2024-01-02")
	q := &hgQuery{reg: reg, sql: &hgSQL{}, status: "published", locale: "en"}
	sql, err := q.filters(sc, hgParseQS(v)["filters"].(map[string]any), "e.published_data", "e")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ILIKE", "BETWEEN", "::numeric", "TRUE", "EXISTS (SELECT 1 FROM hg_entries r", "published_data IS NOT NULL",
		"e.published_data->'seo'->>'metaTitle' IS NULL", "jsonb_array_elements(", "NOT (", "e.id IN ($", "::bigint"} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL lacks %q:\n%s", want, sql)
		}
	}
	found := false
	for _, a := range q.sql.args {
		if a == `%go\_1\%%` {
			found = true
		}
		if s, ok := a.(string); ok && s == "2024-01-02T00:00:00.000Z" {
			found = found && true
		}
	}
	if !found {
		t.Errorf("LIKE pattern not escaped: %#v", q.sql.args)
	}
	for _, bad := range []string{"filters[secret][$eq]=x", "filters[nope][$eq]=x", "filters[title][$regex]=x", "filters[views][$gt]=abc", "filters[featured][$eq]=maybe"} {
		v, _ := url.ParseQuery(bad)
		q := &hgQuery{reg: reg, sql: &hgSQL{}, status: "draft"}
		if _, err := q.filters(sc, hgParseQS(v)["filters"].(map[string]any), "e.data", "e"); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	q = &hgQuery{reg: reg, sql: &hgSQL{}, status: "draft"}
	order, err := q.orderBy(sc, []hgSortKey{{Field: "title"}, {Field: "views", Desc: true}, {Field: "createdAt"}}, "e")
	if err != nil || !strings.Contains(order, "lower(e.data->>'title') ASC") || !strings.Contains(order, "::numeric DESC") || !strings.HasSuffix(order, "e.id ASC") {
		t.Fatalf("order: %s %v", order, err)
	}
	if _, err := q.orderBy(sc, []hgSortKey{{Field: "author"}}, "e"); err == nil {
		t.Fatal("sort on a relation accepted")
	}
}

func TestHGHelpers(t *testing.T) {
	for in, want := range map[string]string{"Hello World!": "hello-world", "Çok güzel şey": "cok-guzel-sey", "  --a--b--  ": "a-b"} {
		if got := hgSlugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
	cases := []struct{ kind, in, want string }{
		{"date", "2024-02-29", "2024-02-29"}, {"time", "9:5", ""}, {"time", "09:05", "09:05:00.000"}, {"time", "23:59:58.5", "23:59:58.500"},
		{"datetime", "2024-01-02T03:04:05+02:00", "2024-01-02T01:04:05.000Z"}, {"datetime", "2024-01-02T03:04", "2024-01-02T03:04:00.000Z"},
		{"date", "2023-02-29", ""},
	}
	for _, c := range cases {
		got, ok := hgNormTemporal(c.kind, c.in)
		if (c.want == "") == ok || got != c.want {
			t.Errorf("hgNormTemporal(%s, %s) = %q %v, want %q", c.kind, c.in, got, ok, c.want)
		}
	}
	if id := hgNewDocumentID(); len(id) != 24 || id[0] < 'a' {
		t.Errorf("document id %q", id)
	}
	if !hgOriginAllowed([]string{"https://app.example.com"}, "https://APP.example.com") || hgOriginAllowed([]string{"https://app.example.com"}, "https://evil.example") ||
		!hgOriginAllowed([]string{"*"}, "http://localhost:3000") || hgOriginAllowed(nil, "null") {
		t.Error("origin matching")
	}
	st := hgSanitize(hgSettings{FrontMode: "redirect", RedirectURL: "javascript:alert(1)", Public: map[string][]string{"a": {"find", "hack"}}, CORSOrigins: []string{"https://x.test/path", "ftp://y"}})
	if st.FrontMode != "json" || !reflect.DeepEqual(st.Public["a"], []string{"find"}) || !reflect.DeepEqual(st.CORSOrigins, []string{"https://x.test"}) {
		t.Errorf("sanitize: %+v", st)
	}
}

func TestHGSchemaValidation(t *testing.T) {
	reg := hgUnitRegistry()
	ok := &hgSchema{Kind: hgKindCollection, DisplayName: "Post", SingularName: "post", PluralName: "posts",
		Fields: []hgField{{Name: "title", Type: "text"}, {Name: "slug", Type: "uid", TargetField: "title"}, {Name: "kind", Type: "enumeration", Enum: []string{"a", "b"}, Default: "a"},
			{Name: "writer", Type: "relation", Relation: "manyToMany", Target: "wp:user"}, {Name: "blocks", Type: "dynamiczone", Components: []string{"shared.seo"}}}}
	hgNormalizeSchema(ok)
	if err := hgValidateSchema(reg, ok, nil); err != nil {
		t.Fatalf("valid schema rejected: %v", err)
	}
	bad := []*hgSchema{
		{Kind: hgKindCollection, DisplayName: "X", SingularName: "Bad Name", PluralName: "xs"},
		{Kind: hgKindCollection, DisplayName: "X", SingularName: "x", PluralName: "x"},
		{Kind: hgKindCollection, DisplayName: "X", SingularName: "x", PluralName: "authors"},
		{Kind: hgKindCollection, DisplayName: "X", SingularName: "wp", PluralName: "wps"},
		{Kind: hgKindCollection, DisplayName: "X", SingularName: "x", PluralName: "xs", Fields: []hgField{{Name: "id", Type: "text"}}},
		{Kind: hgKindCollection, DisplayName: "X", SingularName: "x", PluralName: "xs", Fields: []hgField{{Name: "a", Type: "text"}, {Name: "A", Type: "text"}}},
		{Kind: hgKindCollection, DisplayName: "X", SingularName: "x", PluralName: "xs", Fields: []hgField{{Name: "e", Type: "enumeration"}}},
		{Kind: hgKindCollection, DisplayName: "X", SingularName: "x", PluralName: "xs", Fields: []hgField{{Name: "r", Type: "relation", Relation: "oneToOne", Target: "nope"}}},
		{Kind: hgKindCollection, DisplayName: "X", SingularName: "x", PluralName: "xs", Fields: []hgField{{Name: "c", Type: "component", Component: "nope.nope"}}},
		{Kind: hgKindCollection, DisplayName: "X", SingularName: "x", PluralName: "xs", Fields: []hgField{{Name: "t", Type: "text", Regex: "("}}},
		{Kind: hgKindComponent, DisplayName: "Loop", Category: "shared", Fields: []hgField{{Name: "z", Type: "dynamiczone", Components: []string{"shared.seo"}}}},
		{Kind: hgKindComponent, DisplayName: "SEO", UID: "shared.seo", Category: "shared", Fields: []hgField{{Name: "self", Type: "component", Component: "shared.seo"}}},
	}
	for i, sc := range bad {
		hgNormalizeSchema(sc)
		var prev *hgSchema
		if sc.UID == "shared.seo" {
			prev = reg.Get("shared.seo")
		}
		if err := hgValidateSchema(reg, sc, prev); err == nil {
			t.Errorf("bad schema %d accepted: %+v", i, sc)
		}
	}
}
