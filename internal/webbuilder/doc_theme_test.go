package webbuilder

import (
	"encoding/json"
	"strings"
	"testing"
)

func parseDocT(t *testing.T, raw string) *DocResult {
	t.Helper()
	r, err := ParseDoc([]byte(raw), DocOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func errPaths(r *DocResult) string {
	var p []string
	for _, e := range r.Errors {
		p = append(p, e.Path+"="+e.Code)
	}
	return strings.Join(p, " ")
}

const loopDoc = `{"sections":[{"id":"L1","kind":"loop","name":"Posts","hidden":false,
	"content":{"source":"query","postType":"post","perPage":"9","orderBy":"title","order":"asc","terms":"category:12,post_tag:4","author":"3","exclude":"current","offset":"2","pagination":"none","empty":"No posts"},
	"items":[{"id":"card","title":"","text":"","sub":{"id":"C1","kind":"layout","name":"Card","hidden":false,"content":{},
		"items":[{"id":"c1","title":"post-title","text":"","dyn":{"tag":"h3","link":true}},
		         {"id":"c2","title":"post-meta","text":"","dyn":{"fields":["date","author"],"sep":" · ","format":"j F Y"}},
		         {"id":"c3","title":"post-image","text":"","dyn":{"size":"medium","ratio":1.5}},
		         {"id":"c4","title":"post-excerpt","text":"","dyn":{"words":20,"empty":""}}],
		"style":{"palette":"light","align":"left","padding":0,"titleSize":24,"hideOn":[]}}}],
	"style":{"palette":"light","align":"left","padding":48,"titleSize":32,"hideOn":[]},"grid":{"cols":3}},
	{"id":"N1","kind":"nav","name":"Nav","hidden":false,"content":{"menu":"primary","logoSource":"site","brand":"Acme"},
	 "items":[{"id":"n1","title":"Stale","text":"","href":"/stale/"}],"style":{"palette":"light","align":"left","padding":0,"titleSize":0,"hideOn":[]}}],
	"site":{"brand":"#2271b1","fontPair":"modern","corners":"soft"}}`

func TestLoopDocValid(t *testing.T) {
	r := parseDocT(t, loopDoc)
	if !r.Valid() {
		t.Fatalf("errors: %s", errPaths(r))
	}
	s := FindSection(&r.Doc, "L1")
	if s == nil || s.Kind != KindLoop || FindSection(&r.Doc, "C1") == nil || FindSection(&r.Doc, "nope") != nil || FindSection(nil, "L1") != nil {
		t.Fatal("FindSection")
	}
	sp := ParseLoopSpec(s.Content)
	want := LoopSpec{Source: "query", PostType: "post", PerPage: 9, OrderBy: "title", Order: "asc",
		Terms: []LoopTerm{{"category", 12}, {"post_tag", 4}}, Author: 3, ExcludeCurrent: true, Offset: 2, Pagination: "none", Empty: "No posts"}
	if got, _ := json.Marshal(sp); string(got) != mustJSON(want) {
		t.Errorf("spec %s", got)
	}
	// Item.dyn survives normalization.
	if it := FindSection(&r.Doc, "C1").Items[1]; it.Dyn == nil || strings.Join(it.Dyn.Fields, ",") != "date,author" || it.Dyn.Sep != " · " {
		t.Errorf("dyn lost: %+v", it.Dyn)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestLoopSpecDefaults(t *testing.T) {
	sp := ParseLoopSpec(nil)
	if sp.Source != "main" || sp.PostType != "post" || sp.PerPage != 6 || sp.OrderBy != "date" || sp.Order != "desc" || sp.Pagination != "numbers" || sp.Terms != nil || sp.Offset != 0 {
		t.Errorf("defaults %+v", sp)
	}
	sp = ParseLoopSpec(map[string]string{"source": "related", "perPage": "99", "offset": "-1", "orderBy": "evil", "terms": "x:0", "author": "01", "pagination": "numbers"})
	if sp.PerPage != 6 || sp.Offset != 0 || sp.OrderBy != "date" || sp.Pagination != "none" || len(sp.Terms) != 1 || sp.Terms[0] != (LoopTerm{Taxonomy: "category"}) || sp.Author != 0 {
		t.Errorf("lenient %+v", sp)
	}
	sp = ParseLoopSpec(map[string]string{"source": "related", "terms": "post_tag"})
	if len(sp.Terms) != 1 || sp.Terms[0] != (LoopTerm{Taxonomy: "post_tag"}) {
		t.Errorf("related taxonomy %+v", sp.Terms)
	}
}

func TestLoopDocInvalid(t *testing.T) {
	bad := strings.NewReplacer(
		`"source":"query"`, `"source":"sql"`,
		`"perPage":"9"`, `"perPage":"25"`,
		`"orderBy":"title"`, `"orderBy":"ID; DROP"`,
		`"terms":"category:12,post_tag:4"`, `"terms":"category:012"`,
		`"author":"3"`, `"author":"me"`,
		`"exclude":"current"`, `"exclude":"all"`,
		`"offset":"2"`, `"offset":"51"`,
		`"pagination":"none"`, `"pagination":"infinite"`,
		`"empty":"No posts"`, `"empty":"`+strings.Repeat("é", 201)+`"`,
		`"tag":"h3"`, `"tag":"script"`,
		`"format":"j F Y"`, `"format":"<b>"`,
		`"size":"medium"`, `"size":"huge"`,
		`"ratio":1.5`, `"ratio":9`,
		`"fields":["date","author"]`, `"fields":["date","password"]`,
		`"sep":" · "`, `"sep":"123456789"`,
		`"words":20`, `"words":101`,
		`"menu":"primary"`, `"menu":"bad loc!"`,
		`"logoSource":"site"`, `"logoSource":"url"`,
		`"kind":"layout","name":"Card"`, `"kind":"hero","name":"Card"`,
	).Replace(loopDoc)
	r := parseDocT(t, bad)
	got := errPaths(r)
	for _, want := range []string{
		"/sections/0/content/source=bad_value", "/sections/0/content/perPage=bad_value", "/sections/0/content/orderBy=bad_value",
		"/sections/0/content/terms=bad_value", "/sections/0/content/author=bad_value", "/sections/0/content/exclude=bad_value",
		"/sections/0/content/offset=bad_value", "/sections/0/content/pagination=bad_value", "/sections/0/content/empty=too_long",
		"/sections/0/items/0/sub/kind=unknown_kind",
		"/sections/0/items/0/sub/items/0/dyn/tag=bad_value", "/sections/0/items/0/sub/items/1/dyn/format=bad_value",
		"/sections/0/items/0/sub/items/1/dyn/fields/1=bad_value", "/sections/0/items/0/sub/items/1/dyn/sep=too_long",
		"/sections/0/items/0/sub/items/2/dyn/size=bad_value", "/sections/0/items/0/sub/items/2/dyn/ratio=bad_value",
		"/sections/0/items/0/sub/items/3/dyn/words=bad_value",
		"/sections/1/content/menu=bad_value", "/sections/1/content/logoSource=bad_value",
	} {
		if !strings.Contains(" "+got+" ", " "+want+" ") {
			t.Errorf("missing %s in %s", want, got)
		}
	}
}

func TestDynZeroIsUnset(t *testing.T) {
	r := parseDocT(t, strings.Replace(loopDoc, `"words":20`, `"words":0,"ratio":0,"burgerAt":0`, 1))
	if !r.Valid() {
		t.Fatal(errPaths(r))
	}
}

func TestLoopDepth(t *testing.T) {
	loop := func(id, inner string) string {
		sub := `{"id":"` + id + `c","kind":"layout","name":"","hidden":false,"content":{},"items":[` + inner + `],"style":{"palette":"light","align":"left","padding":0,"titleSize":0,"hideOn":[]}}`
		return `{"id":"` + id + `","kind":"loop","name":"","hidden":false,"content":{},"items":[{"id":"i","title":"","text":"","sub":` + sub + `}],"style":{"palette":"light","align":"left","padding":0,"titleSize":0,"hideOn":[]}}`
	}
	cell := func(sec string) string {
		return `{"id":"x` + sec[7:9] + `","title":"layout","text":"","sub":` + sec + `}`
	}
	two := loop("A1", cell(loop("B1", "")))
	r := parseDocT(t, `{"sections":[`+two+`],"site":{}}`)
	if !r.Valid() {
		t.Fatalf("two levels: %s", errPaths(r))
	}
	three := loop("A1", cell(loop("B1", cell(loop("C1", "")))))
	r = parseDocT(t, `{"sections":[`+three+`],"site":{}}`)
	if !strings.Contains(errPaths(r), "=too_deep") {
		t.Fatalf("three levels accepted: %s", errPaths(r))
	}
}

// Case variants of dyn keys are dropped, like every other known key.
func TestDynCaseVariants(t *testing.T) {
	r := parseDocT(t, strings.Replace(loopDoc, `"dyn":{"tag":"h3","link":true}`, `"dyn":{"tag":"h3","TAG":"script","link":true}`, 1))
	if !r.Valid() || strings.Contains(string(r.JSON), "TAG") {
		t.Fatalf("errors %s json %s", errPaths(r), r.JSON)
	}
}

func TestCellTypesAndLayout(t *testing.T) {
	for _, c := range []string{"", "heading", "layout", "post-title", "pagination"} {
		if !IsCellType(c) {
			t.Errorf("IsCellType(%q)", c)
		}
	}
	if IsCellType("post-body") || IsDynWidget("heading") || !IsDynWidget("site-logo") {
		t.Error("cell type sets")
	}
	if !ValidPageLayout("") || !ValidPageLayout("site") || !ValidPageLayout("canvas") || ValidPageLayout("full") {
		t.Error("ValidPageLayout")
	}
	nav := &DocState{Sections: []Section{{Kind: KindNav}}}
	if EffectivePageLayout(PageSettings{}, nav) != PageLayoutCanvas || EffectivePageLayout(PageSettings{Layout: "site"}, nav) != PageLayoutSite ||
		EffectivePageLayout(PageSettings{Layout: "bogus"}, &DocState{}) != PageLayoutSite {
		t.Error("EffectivePageLayout")
	}
}

func TestFallbackSkipsDynamic(t *testing.T) {
	r := parseDocT(t, strings.Replace(loopDoc, `{"id":"N1"`, `{"id":"T1","kind":"layout","name":"","hidden":false,"content":{},"items":[{"id":"h","title":"heading","text":"Static heading"},{"id":"d","title":"post-title","text":"Sample title"}],"style":{"palette":"light","align":"left","padding":0,"titleSize":0,"hideOn":[]}},{"id":"N1"`, 1))
	if !r.Valid() {
		t.Fatal(errPaths(r))
	}
	out := FallbackHTML(&r.Doc)
	if !strings.Contains(out, "Static heading") {
		t.Errorf("static cell lost: %s", out)
	}
	for _, bad := range []string{"Sample title", "No posts", "Stale", "Acme", `id="L1"`, `<nav`} {
		if strings.Contains(out, bad) {
			t.Errorf("%q in fallback %s", bad, out)
		}
	}
	// An unbound nav keeps its links and brand.
	r = parseDocT(t, strings.Replace(loopDoc, `"menu":"primary","logoSource":"site",`, ``, 1))
	if out := FallbackHTML(&r.Doc); !strings.Contains(out, "Stale") || !strings.Contains(out, "Acme") {
		t.Errorf("unbound nav: %s", out)
	}
}

const shopDoc = `{"sections":[{"id":"S1","kind":"layout","name":"Shop","hidden":false,"content":{},
	"items":[{"id":"p1","title":"shop-products","text":"","dyn":{"source":"ids","columns":3,"limit":6,"orderBy":"price-desc","order":"asc","cats":"4,5","tags":"7","ids":"10,11","paginate":true}},
	         {"id":"p2","title":"shop-categories","text":"","dyn":{"columns":4,"hideEmpty":false,"parent":"0"}},
	         {"id":"p3","title":"product-add-to-cart","text":"","dyn":{"product":42}},
	         {"id":"p4","title":"shop-cart","text":"","dyn":{"showTotal":true,"dropdown":true}},
	         {"id":"p5","title":"language-switcher","text":"","dyn":{"dropdown":false,"flags":true,"names":true,"hideCurrent":true}},
	         {"id":"p6","title":"shop-cart-page","text":""},
	         {"id":"p7","title":"product-images","text":"","dyn":{"gallery":false}}],
	"style":{"palette":"light","align":"left","padding":0,"titleSize":0,"hideOn":[]}}],
	"site":{"brand":"#2271b1","fontPair":"modern","corners":"soft"}}`

// Commerce and language-switcher settings validate and round-trip.
func TestShopDocValid(t *testing.T) {
	r := parseDocT(t, shopDoc)
	if !r.Valid() {
		t.Fatalf("errors: %s", errPaths(r))
	}
	d := r.Doc.Sections[0].Items[0].Dyn
	if d == nil || d.Source != "ids" || *d.Columns != 3 || *d.Limit != 6 || d.OrderBy != "price-desc" || d.Cats != "4,5" || d.IDs != "10,11" || !*d.Paginate {
		t.Errorf("dyn %+v", d)
	}
	if !IsShopWidget("product-price") || IsShopWidget("post-title") || !IsItemWidget("language-switcher") || IsItemWidget("nav-menu") {
		t.Error("IsShopWidget / IsItemWidget")
	}
	if got := ParseShopIDs("3,4,5", 2); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Errorf("ParseShopIDs %v", got)
	}
	if ParseShopIDs("1,,2", 24) != nil || ParseShopIDs("01", 24) != nil {
		t.Error("ParseShopIDs accepted a bad list")
	}
	out := FallbackHTML(&r.Doc)
	if !strings.Contains(out, "[goocommerce_cart]") {
		t.Errorf("cart page fallback: %s", out)
	}
}

func TestShopDocInvalid(t *testing.T) {
	bad := strings.NewReplacer(
		`"source":"ids"`, `"source":"all"`,
		`"columns":3`, `"columns":7`,
		`"limit":6`, `"limit":2.5`,
		`"orderBy":"price-desc"`, `"orderBy":"id; drop"`,
		`"order":"asc"`, `"order":"up"`,
		`"cats":"4,5"`, `"cats":"4,x"`,
		`"ids":"10,11"`, `"ids":"`+strings.Repeat("1,", 24)+`1"`,
		`"parent":"0"`, `"parent":"-1"`,
		`"product":42`, `"product":-3`,
	).Replace(shopDoc)
	got := errPaths(parseDocT(t, bad))
	for _, want := range []string{"/items/0/dyn/source=", "/items/0/dyn/columns=", "/items/0/dyn/limit=", "/items/0/dyn/orderBy=", "/items/0/dyn/order=",
		"/items/0/dyn/cats=", "/items/0/dyn/ids=", "/items/1/dyn/parent=", "/items/2/dyn/product="} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
}
