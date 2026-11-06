package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/cache"
	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Tests of the Theme Builder server side (webbuilder_templates*.go, webbuilder_dynamic_ctx.go):
// the capability gate, the REST routes, the picker and its cache, the preview override, WXR and
// Pursae Multi Language. Integration tests need WPGO_TEST_ADMIN_DB / WPGO_TEST_DB.

// --- unit ------------------------------------------------------------------------------------------

func TestWBTemplateFactsOf(t *testing.T) {
	post := &store.Post{ID: 7, Type: "post", ParentID: 0, AuthorID: 3}
	page := &store.Post{ID: 8, Type: "page", ParentID: 2}
	cases := []struct {
		q    *siteQuery
		want webbuilder.QueryFacts
	}{
		{&siteQuery{Kind: "single", Post: post}, webbuilder.QueryFacts{View: webbuilder.ViewSingular, PostType: "post", PostID: 7, AuthorID: 3}},
		{&siteQuery{Kind: "front-page", Post: page}, webbuilder.QueryFacts{View: webbuilder.ViewSingular, FrontPage: true, PostType: "page", PostID: 8, ParentID: 2}},
		{&siteQuery{Kind: "home"}, webbuilder.QueryFacts{View: webbuilder.ViewArchive, Archive: webbuilder.ArchiveHome, FrontPage: true}},
		{&siteQuery{Kind: "home", PostsPage: page}, webbuilder.QueryFacts{View: webbuilder.ViewArchive, Archive: webbuilder.ArchiveHome}},
		{&siteQuery{Kind: "category", Term: &store.Term{ID: 5, Taxonomy: "category"}}, webbuilder.QueryFacts{View: webbuilder.ViewArchive, Archive: webbuilder.ArchiveTerm, Taxonomy: "category", TermID: 5}},
		{&siteQuery{Kind: "author", Author: &store.User{ID: 4}}, webbuilder.QueryFacts{View: webbuilder.ViewArchive, Archive: webbuilder.ArchiveAuthor, AuthorID: 4}},
		{&siteQuery{Kind: "search", Search: "x"}, webbuilder.QueryFacts{View: webbuilder.ViewArchive, Archive: webbuilder.ArchiveSearch}},
		{&siteQuery{Kind: "post-type-archive", PostType: "book"}, webbuilder.QueryFacts{View: webbuilder.ViewArchive, Archive: webbuilder.ArchivePostType, PostType: "book"}},
		{&siteQuery{Kind: "404"}, webbuilder.QueryFacts{View: webbuilder.View404}},
		{&siteQuery{Kind: "single", Post: &store.Post{ID: 9, Type: "product"}, Store: "product"}, webbuilder.QueryFacts{View: webbuilder.ViewSingular, Store: true, PostType: "product", PostID: 9}},
		{&siteQuery{Kind: "home", Store: "shop"}, webbuilder.QueryFacts{View: webbuilder.ViewArchive, Archive: webbuilder.ArchiveHome, FrontPage: true, Store: true}},
		// The store renders every view as a synthetic page: facts follow Store, never the page.
		{&siteQuery{Kind: "page", Post: &store.Post{ID: 9, Type: "page"}, Store: "product"}, webbuilder.QueryFacts{View: webbuilder.ViewSingular, Store: true, PostType: "product", PostID: 9}},
		{&siteQuery{Kind: "page", Post: &store.Post{ID: 42, Type: "page"}, Store: "product_cat", StoreTax: "product_cat", StoreTerm: 42}, webbuilder.QueryFacts{View: webbuilder.ViewArchive, Store: true, Archive: webbuilder.ArchiveTerm, Taxonomy: "product_cat", TermID: 42}},
		{&siteQuery{Kind: "page", Post: &store.Post{ID: 11, Type: "page"}, Store: "shop"}, webbuilder.QueryFacts{View: webbuilder.ViewArchive, Store: true, Archive: webbuilder.ArchivePostType, PostType: "product"}},
		{&siteQuery{Kind: "page", Post: &store.Post{ID: 12, Type: "page"}, Store: "cart"}, webbuilder.QueryFacts{View: webbuilder.ViewSingular, Store: true, PostType: "page", PostID: 12}},
	}
	for i, tc := range cases {
		got := wbFactsOf(tc.q)
		if got.View != tc.want.View || got.FrontPage != tc.want.FrontPage || got.Store != tc.want.Store || got.PostType != tc.want.PostType ||
			got.PostID != tc.want.PostID || got.ParentID != tc.want.ParentID || got.AuthorID != tc.want.AuthorID ||
			got.Archive != tc.want.Archive || got.Taxonomy != tc.want.Taxonomy || got.TermID != tc.want.TermID {
			t.Errorf("%d (%s): %+v, want %+v", i, tc.q.Kind, got, tc.want)
		}
	}
}

func TestWBTemplateParsePreview(t *testing.T) {
	good := map[string]wbPreview{
		"post:12":         {Kind: "post", ID: 12},
		"term:category:3": {Kind: "term", Name: "category", ID: 3},
		"author:1":        {Kind: "author", ID: 1},
		"type:book":       {Kind: "type", Name: "book"},
		"search:a b":      {Kind: "search", Search: "a b"},
		"search:":         {Kind: "search"},
		"front":           {Kind: "front"},
		"home":            {Kind: "home"},
		"404":             {Kind: "404"},
		"latest":          {Kind: "latest"},
	}
	for in, want := range good {
		got, err := wbParsePreview(in)
		if err != nil || got.Kind != want.Kind || got.ID != want.ID || got.Name != want.Name || got.Search != want.Search {
			t.Errorf("%q: %+v %v, want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "post:", "post:0", "post:012", "post:-1", "post:x", "term:Cat:1", "term:category", "type:", "type:Book",
		"author:99999999999999999999", "search:" + strings.Repeat("a", 101), "search:a\nb", "nope", "nope:1", strings.Repeat("x", 201)} {
		if _, err := wbParsePreview(in); err == nil {
			t.Errorf("%q parsed", in)
		}
	}
}

func TestWBTemplateCandidatesLanguage(t *testing.T) {
	cand := func(id int64) webbuilder.TemplateCandidate {
		return webbuilder.TemplateCandidate{ID: id, Type: webbuilder.TemplateHeader, Conditions: webbuilder.ParseConditions([]string{"include/general"})}
	}
	idx := &wbTemplateIndex{
		entries: []store.WBTemplateIndexEntry{{ID: 1, HasHTML: true}, {ID: 2, HasHTML: true}, {ID: 3, HasHTML: true}, {ID: 4, HasHTML: true}, {ID: 5, HasHTML: true}},
		cands:   []webbuilder.TemplateCandidate{cand(1), cand(2), cand(3), cand(4), cand(5)},
		// 1 (en) + 2 (fr) are one group, 3 (de) + 4 (fr) another; 5 has no language row.
		lang:  map[int64]string{1: "en", 2: "fr", 3: "de", 4: "fr"},
		group: map[int64]int64{1: 100, 2: 100, 3: 200, 4: 200},
	}
	ids := func(cs []webbuilder.TemplateCandidate) string {
		var out []string
		for _, c := range cs {
			out = append(out, itoa64(c.ID))
		}
		return strings.Join(out, ",")
	}
	if got := ids(idx.candidates("fr", "en")); got != "2,4,5" {
		t.Errorf("fr: %s", got)
	}
	if got := ids(idx.candidates("en", "en")); got != "1,3,5" { // group 200 has no en member: the oldest
		t.Errorf("en: %s", got)
	}
	if got := ids(idx.candidates("", "de")); got != "1,3,5" {
		t.Errorf("no language, default de: %s", got)
	}
	// A request-language member without a snapshot (an import not rendered yet) does not hide
	// its usable default-language sibling, and never competes itself.
	idx.entries[1].HasHTML = false
	if got := ids(idx.candidates("fr", "en")); got != "1,4,5" {
		t.Errorf("fr with an unrendered fr member: %s", got)
	}
	idx.entries[1].HasHTML = true
	if idx.translation(1, "fr") != 2 || idx.translation(2, "en") != 1 || idx.translation(3, "en") != 3 || idx.translation(5, "fr") != 5 {
		t.Error("translation mapping")
	}
}

// --- integration helpers ---------------------------------------------------------------------------

// wbtTemplate creates a template with a published snapshot (status publish unless draft).
func wbtTemplate(t *testing.T, srv *Server, typ webbuilder.TemplateType, title string, conds []string, prio int, status string) *store.WBTemplate {
	t.Helper()
	ctx := context.Background()
	wb := srv.store.WebBuilder()
	tp, err := wb.CreateTemplate(ctx, store.NewWBTemplate{Type: typ, Title: title, Conditions: conds, Priority: prio, AuthorID: 1})
	if err != nil {
		t.Fatal(err)
	}
	d := &store.WebBuilderDoc{PostID: tp.ID, Doc: []byte(`{"sections":[]}`), Page: []byte(`{}`),
		HTML: `<div class="wb-page" data-wb-root data-wb-post="` + itoa64(tp.ID) + `">` + title + `</div>`, Fonts: []byte(`[]`),
		Features: []string{}, Renderer: webbuilder.RendererVersion}
	if err := wb.PublishDoc(ctx, d, 0); err != nil {
		t.Fatal(err)
	}
	if status == "" {
		status = "publish"
	}
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET status = $2 WHERE id = $1`, tp.ID, status); err != nil {
		t.Fatal(err)
	}
	srv.wbTemplatesChanged(ctx)
	return tp
}

func wbtCtx(srv *Server, u *store.User, target string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	if u != nil {
		c.Set(ctxUser, u)
	}
	return c, w
}

func wbtPick(srv *Server, location string, q *siteQuery) int64 {
	c, _ := wbtCtx(srv, nil, "/")
	_, id := srv.wbPickTemplate(c, location, q)
	return id
}

// --- integration -----------------------------------------------------------------------------------

func TestWBTemplateCaps(t *testing.T) {
	srv, base, admin := wbrStart(t)
	code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/templates", map[string]any{"type": "header", "title": "Main header"}, nil)
	if code != http.StatusCreated {
		t.Fatalf("admin create: %d %v", code, v)
	}
	id := int64(wbrObj(v)["id"].(float64))
	if !strings.HasSuffix(wbrObj(v)["editUrl"].(string), wbEditURL(id)) {
		t.Errorf("editUrl %v", v)
	}
	if code, v, _ := admin.do(http.MethodGet, "/web-builder/v2/templates?type=header", nil, nil); code != http.StatusOK || len(v.([]any)) != 1 {
		t.Fatalf("admin list: %d %v", code, v)
	} else if row := wbrObj(v.([]any)[0]); row["status"] != "draft" || row["type"] != "header" || !strings.Contains(row["previewUrl"].(string), webbuilder.PreviewQuery+"="+itoa64(id)) {
		t.Errorf("summary %v", row)
	}
	if code, _, _ := admin.do(http.MethodGet, wbrDocPath(id, ""), nil, nil); code != http.StatusOK {
		t.Errorf("admin document: %d", code)
	}
	doc, render := wbrFixture(t, "initial")
	for _, role := range []string{"author", "editor"} {
		cl := wbrUser(t, srv, base, "tpl-"+role, role)
		for _, rq := range []struct {
			m, p string
			body any
		}{
			{http.MethodGet, "/web-builder/v2/templates", nil},
			{http.MethodPost, "/web-builder/v2/templates", map[string]any{"type": "footer"}},
			{http.MethodPut, "/web-builder/v2/templates/" + itoa64(id) + "/conditions", map[string]any{"conditions": []string{"include/general"}}},
			{http.MethodPut, "/web-builder/v2/templates/" + itoa64(id) + "/preview", map[string]any{"object": "front"}},
			{http.MethodGet, "/web-builder/v2/templates/" + itoa64(id) + "/conflicts", nil},
			{http.MethodGet, wbrDocPath(id, ""), nil},
			{http.MethodPut, wbrDocPath(id, "/autosave"), map[string]any{"doc": doc}},
			{http.MethodPost, wbrDocPath(id, "/publish"), wbrPublishBody(doc, render, 0, "publish", nil)},
			{http.MethodGet, wbrDocPath(id, "/revisions"), nil},
		} {
			if code, v, _ := cl.do(rq.m, rq.p, rq.body, nil); code != http.StatusForbidden || wbrCode(v) != webbuilder.ErrForbidden {
				t.Errorf("%s %s %s: %d %v", role, rq.m, rq.p, code, v)
			}
		}
		code, body, _ := get(t, cl.c, base+wbEditURL(id))
		if code != http.StatusForbidden || strings.Contains(body, webbuilder.BootGlobal) {
			t.Errorf("%s editor page: %d", role, code)
		}
		// The editor may still read sample data of what it can read.
		if code, v, _ := cl.do(http.MethodGet, "/web-builder/v2/preview-context?object=front", nil, nil); code != http.StatusOK || wbrObj(v)["object"] != "front" {
			t.Errorf("%s preview-context: %d %v", role, code, v)
		}
	}
	sub := wbrUser(t, srv, base, "tpl-sub", "subscriber")
	if code, _, _ := sub.do(http.MethodGet, "/web-builder/v2/preview-context?object=front", nil, nil); code != http.StatusForbidden {
		t.Errorf("subscriber preview-context: %d", code)
	}

	// The admin's editor page boots in template mode.
	code, page, _ := get(t, admin.c, base+wbEditURL(id))
	if code != http.StatusOK {
		t.Fatalf("admin editor: %d", code)
	}
	for _, want := range []string{`"template":{"type":"header","conditions":["include/general"]`, `"starter":"header"`, `"manageUrl":`,
		`"exit":"` + admin.base + wbThemeBuilderURL(), `"editPost":""`, `"publish":true`, `"postTypes":[{"slug":"post"`, `"taxonomies":[{"slug":"category"`} {
		if !strings.Contains(page, want) {
			t.Errorf("boot lacks %s", want)
		}
	}
}

func TestWBTemplateRESTValidation(t *testing.T) {
	srv, _, admin := wbrStart(t)
	for _, body := range []map[string]any{{"type": "nope"}, {"type": "header", "starter": "nope"}, {"type": "single"},
		{"type": "single", "postType": "post"}, {"type": "header", "postType": "book"}} {
		if code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/templates", body, nil); code != http.StatusUnprocessableEntity || wbrCode(v) != webbuilder.ErrBadTemplate {
			t.Errorf("create %v: %d %v", body, code, v)
		}
	}
	if code, v, _ := admin.do(http.MethodGet, "/web-builder/v2/templates?type=nope", nil, nil); code != http.StatusUnprocessableEntity || wbrCode(v) != webbuilder.ErrBadTemplate {
		t.Errorf("list bad type: %d %v", code, v)
	}
	_, v, _ := admin.do(http.MethodPost, "/web-builder/v2/templates", map[string]any{"type": "single-post"}, nil)
	id := itoa64(int64(wbrObj(v)["id"].(float64)))
	tp, _ := srv.store.WebBuilder().Template(context.Background(), int64(wbrObj(v)["id"].(float64)))
	if tp.Title != "Single Post" || strings.Join(tp.Conditions, ",") != "include/singular/post" || tp.Starter != webbuilder.StarterSingle {
		t.Errorf("defaults %+v", tp)
	}
	cond := "/web-builder/v2/templates/" + id + "/conditions"
	code, v, _ := admin.do(http.MethodPut, cond, map[string]any{"conditions": []string{"include/general", "include/bogus", "exclude/singular/page/0"}}, nil)
	errs, _ := wbrData(v)["errors"].([]any)
	if code != http.StatusUnprocessableEntity || wbrCode(v) != webbuilder.ErrBadCondition || len(errs) != 2 || wbrObj(errs[0])["path"] != "/conditions/1" || wbrObj(errs[0])["code"] != webbuilder.VErrCondition {
		t.Errorf("bad rules: %d %v", code, v)
	}
	if code, v, _ := admin.do(http.MethodPut, cond, map[string]any{"conditions": []string{}, "priority": 101}, nil); code != http.StatusUnprocessableEntity ||
		wbrObj(wbrData(v)["errors"].([]any)[0])["path"] != "/priority" {
		t.Errorf("bad priority: %d %v", code, v)
	}
	many := make([]string, webbuilder.MaxConditions+1)
	for i := range many {
		many[i] = "include/singular/post/" + itoa64(int64(i+1))
	}
	if code, v, _ := admin.do(http.MethodPut, cond, map[string]any{"conditions": many}, nil); code != http.StatusUnprocessableEntity || wbrCode(v) != webbuilder.ErrBadCondition {
		t.Errorf("too many rules: %d %v", code, v)
	}
	code, v, _ = admin.do(http.MethodPut, cond, map[string]any{"conditions": []string{" include/singular/post ", "include/singular/post", "exclude/singular/post/5"}, "priority": -3}, nil)
	if code != http.StatusOK || len(wbrObj(v)["conditions"].([]any)) != 2 || wbrObj(v)["priority"] != float64(-3) {
		t.Errorf("normalized: %d %v", code, v)
	}
	tp, _ = srv.store.WebBuilder().Template(context.Background(), tp.ID)
	if strings.Join(tp.Conditions, ",") != "include/singular/post,exclude/singular/post/5" || tp.Priority != -3 {
		t.Errorf("stored %+v", tp)
	}
	if code, _, _ := admin.do(http.MethodPut, "/web-builder/v2/templates/999999/conditions", map[string]any{"conditions": []string{}}, nil); code != http.StatusNotFound {
		t.Errorf("missing template: %d", code)
	}
	// A page id is not a template.
	page := wbrPost(t, srv, "page", "Not a template", "draft", 1)
	if code, _, _ := admin.do(http.MethodPut, "/web-builder/v2/templates/"+itoa64(page.ID)+"/conditions", map[string]any{"conditions": []string{}}, nil); code != http.StatusNotFound {
		t.Errorf("page as template: %d", code)
	}
	// A template is published or a draft, never pending or private.
	doc, render := wbrFixture(t, "initial")
	for _, st := range []string{"pending", "private"} {
		if code, v, _ := admin.do(http.MethodPost, wbrDocPath(tp.ID, "/publish"), wbrPublishBody(doc, render, 0, st, nil), nil); code != http.StatusUnprocessableEntity || wbrCode(v) != webbuilder.ErrBadTemplate {
			t.Errorf("publish %s: %d %v", st, code, v)
		}
	}
}

func TestWBTemplatePreviewContext(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	p := wbrPost(t, srv, "post", "Preview Me", "publish", 1)
	if code, v, _ := admin.do(http.MethodGet, "/web-builder/v2/preview-context?object=post:"+itoa64(p.ID), nil, nil); code != http.StatusOK {
		t.Fatalf("post: %d %v", code, v)
	} else if post := wbrObj(wbrObj(v)["post"]); post["title"] != "Preview Me" || wbrObj(wbrObj(v)["view"])["kind"] != "single" || wbrObj(v)["object"] != "post:"+itoa64(p.ID) {
		t.Errorf("post context %v", v)
	}
	if code, v, _ := admin.do(http.MethodGet, "/web-builder/v2/preview-context?object=home", nil, nil); code != http.StatusOK || wbrObj(wbrObj(v)["view"])["kind"] != "home" || len(wbrObj(v)["posts"].([]any)) == 0 {
		t.Errorf("home: %d %v", code, v)
	}
	if code, v, _ := admin.do(http.MethodGet, "/web-builder/v2/preview-context?object=search:Preview", nil, nil); code != http.StatusOK || wbrObj(wbrObj(v)["view"])["search"] != "Preview" {
		t.Errorf("search: %d %v", code, v)
	}
	if code, v, _ := admin.do(http.MethodGet, "/web-builder/v2/preview-context?object=404", nil, nil); code != http.StatusOK || wbrObj(wbrObj(v)["view"])["kind"] != "404" {
		t.Errorf("404: %d %v", code, v)
	}
	if code, v, _ := admin.do(http.MethodGet, "/web-builder/v2/preview-context?object=post:x", nil, nil); code != http.StatusUnprocessableEntity || wbrCode(v) != webbuilder.ErrBadPreview {
		t.Errorf("bad object: %d %v", code, v)
	}
	for _, obj := range []string{"post:999999", "term:category:999999", "type:nope", "author:999999"} {
		if code, v, _ := admin.do(http.MethodGet, "/web-builder/v2/preview-context?object="+obj, nil, nil); code != http.StatusNotFound || wbrCode(v) != webbuilder.ErrNotFound {
			t.Errorf("%s: %d %v", obj, code, v)
		}
	}
	// An author may not read another user's draft through the sample data.
	draft := wbrPost(t, srv, "post", "Secret draft", "draft", 1)
	auth := wbrUser(t, srv, base, "pc-author", "author")
	if code, _, _ := auth.do(http.MethodGet, "/web-builder/v2/preview-context?object=post:"+itoa64(draft.ID), nil, nil); code != http.StatusNotFound {
		t.Errorf("author reads a draft: %d", code)
	}
	// Post content comes UGC-sanitized.
	evil := &store.Post{Type: "post", Title: "Evil", Slug: "evil", Status: "publish", AuthorID: 1, Content: `<p>ok</p><script>alert(1)</script><img src=x onerror=alert(2)>`}
	if err := srv.store.InsertPost(ctx, evil); err != nil {
		t.Fatal(err)
	}
	_, v, _ := admin.do(http.MethodGet, "/web-builder/v2/preview-context?object=post:"+itoa64(evil.ID), nil, nil)
	if html := wbrObj(wbrObj(v)["post"])["contentHtml"].(string); !strings.Contains(html, "<p>ok</p>") || strings.Contains(html, "<script") || strings.Contains(html, "onerror") {
		t.Errorf("contentHtml %q", html)
	}

	// A singular preview samples recent posts (with the current one) so loops and post.nav draw;
	// a page preview also gets posts. Drafts never appear.
	ids := func(v any) map[float64]string {
		out := map[float64]string{}
		for _, x := range wbrObj(v)["posts"].([]any) {
			out[wbrObj(x)["id"].(float64)] = wbrObj(x)["type"].(string)
		}
		return out
	}
	_, v, _ = admin.do(http.MethodGet, "/web-builder/v2/preview-context?object=post:"+itoa64(p.ID), nil, nil)
	if got := ids(v); got[float64(p.ID)] != "post" || got[float64(evil.ID)] != "post" || got[float64(draft.ID)] != "" {
		t.Errorf("single sample posts %v", got)
	}
	pg := wbrPost(t, srv, "page", "Sample Page", "publish", 1)
	_, v, _ = admin.do(http.MethodGet, "/web-builder/v2/preview-context?object=post:"+itoa64(pg.ID), nil, nil)
	if got := ids(v); got[float64(pg.ID)] != "page" || got[float64(p.ID)] != "post" {
		t.Errorf("page sample posts %v", got)
	}

	// PUT templates/{id}/preview stores the object and answers its label, URL and sample data.
	tp := wbtTemplate(t, srv, webbuilder.TemplateSinglePost, "Single", []string{"include/singular/post"}, 0, "draft")
	code, v, _ := admin.do(http.MethodPut, "/web-builder/v2/templates/"+itoa64(tp.ID)+"/preview", map[string]any{"object": "post:" + itoa64(p.ID)}, nil)
	pr := wbrObj(wbrObj(v)["preview"])
	if code != http.StatusOK || pr["label"] != "Post: Preview Me" || !strings.Contains(pr["url"].(string), webbuilder.PreviewQuery+"="+itoa64(tp.ID)) ||
		wbrObj(wbrObj(wbrObj(v)["dynamic"])["post"])["id"] != float64(p.ID) {
		t.Errorf("preview: %d %v", code, v)
	}
	if got, _ := srv.store.WebBuilder().Template(ctx, tp.ID); got.Preview != "post:"+itoa64(p.ID) {
		t.Errorf("stored preview %q", got.Preview)
	}
	if code, v, _ := admin.do(http.MethodPut, "/web-builder/v2/templates/"+itoa64(tp.ID)+"/preview", map[string]any{"object": "bogus"}, nil); code != http.StatusUnprocessableEntity || wbrCode(v) != webbuilder.ErrBadPreview {
		t.Errorf("bad preview: %d %v", code, v)
	}
	// latest of a single-post template is the newest post.
	_, _ = srv.store.WebBuilder().Template(ctx, tp.ID)
	_ = srv.store.WebBuilder().SetTemplatePreview(ctx, tp.ID, "")
	tp, _ = srv.store.WebBuilder().Template(ctx, tp.ID)
	c, _ := wbtCtx(srv, nil, "/")
	info, _ := srv.wbTemplatePreviewInfo(c, tp)
	if info.Object != webbuilder.PreviewLatest || !strings.HasPrefix(info.Label, "Post: ") {
		t.Errorf("latest: %+v", info)
	}
}

func TestWBTemplatePick(t *testing.T) {
	srv, _, _ := wbrStart(t)
	ctx := context.Background()
	post := wbrPost(t, srv, "post", "Pick Post", "publish", 1)
	page := wbrPost(t, srv, "page", "Pick Page", "publish", 1)
	cat := &store.Term{Taxonomy: "category", Slug: "news", Name: "News"}
	if err := srv.store.InsertTerm(ctx, cat); err != nil {
		t.Fatal(err)
	}
	_ = srv.store.SetPostTerms(ctx, post.ID, "category", []int64{cat.ID})
	single := &siteQuery{Kind: "single", Post: post}
	pageQ := &siteQuery{Kind: "page", Post: page}
	archive := &siteQuery{Kind: "category", Term: cat}
	search := &siteQuery{Kind: "search", Search: "x"}

	if id := wbtPick(srv, webbuilder.LocationHeader, single); id != 0 {
		t.Fatalf("no templates: %d", id)
	}
	general := wbtTemplate(t, srv, webbuilder.TemplateHeader, "General header", []string{"include/general"}, 0, "")
	pageHeader := wbtTemplate(t, srv, webbuilder.TemplateHeader, "Page header", []string{"include/singular/page/" + itoa64(page.ID)}, 0, "")
	if id := wbtPick(srv, webbuilder.LocationHeader, single); id != general.ID {
		t.Errorf("general header on a post: %d", id)
	}
	if id := wbtPick(srv, webbuilder.LocationHeader, pageQ); id != pageHeader.ID {
		t.Errorf("specific header on the page: %d", id)
	}
	if id := wbtPick(srv, webbuilder.LocationFooter, single); id != 0 {
		t.Errorf("a header fills the footer: %d", id)
	}
	// Exclude vetoes.
	if err := srv.store.WebBuilder().SetTemplateConditions(ctx, general.ID, []string{"include/general", "exclude/archive"}, 0); err != nil {
		t.Fatal(err)
	}
	srv.wbTemplatesChanged(ctx)
	if id := wbtPick(srv, webbuilder.LocationHeader, archive); id != 0 {
		t.Errorf("excluded on the archive: %d", id)
	}
	// Ties: priority, then the newer.
	f1 := wbtTemplate(t, srv, webbuilder.TemplateFooter, "Footer A", []string{"include/general"}, 0, "")
	f2 := wbtTemplate(t, srv, webbuilder.TemplateFooter, "Footer B", []string{"include/general"}, 0, "")
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE posts SET modified_at = now() - interval '1 day' WHERE id = $1`, f2.ID)
	srv.wbTemplatesChanged(ctx)
	if id := wbtPick(srv, webbuilder.LocationFooter, single); id != f1.ID {
		t.Errorf("newer footer: %d", id)
	}
	_ = srv.store.WebBuilder().SetTemplateConditions(ctx, f2.ID, []string{"include/general"}, 5)
	srv.wbTemplatesChanged(ctx)
	if id := wbtPick(srv, webbuilder.LocationFooter, single); id != f2.ID {
		t.Errorf("higher priority footer: %d", id)
	}
	// Main: view gate, term relation, search bonus.
	sp := wbtTemplate(t, srv, webbuilder.TemplateSinglePost, "Single", []string{"include/general"}, 0, "")
	inNews := wbtTemplate(t, srv, webbuilder.TemplateSinglePost, "News single", []string{"include/singular/post/in/category/" + itoa64(cat.ID)}, -50, "")
	arch := wbtTemplate(t, srv, webbuilder.TemplateArchive, "Archive", []string{"include/archive"}, 50, "")
	srch := wbtTemplate(t, srv, webbuilder.TemplateSearch, "Search", []string{"include/archive"}, 0, "")
	if id := wbtPick(srv, webbuilder.LocationMain, single); id != inNews.ID {
		t.Errorf("post in News: %d (single %d)", id, sp.ID)
	}
	if id := wbtPick(srv, webbuilder.LocationMain, archive); id != arch.ID {
		t.Errorf("archive (single-post must not render it): %d", id)
	}
	if id := wbtPick(srv, webbuilder.LocationMain, search); id != srch.ID {
		t.Errorf("search: %d (archive %d)", id, arch.ID)
	}
	if id := wbtPick(srv, webbuilder.LocationMain, &siteQuery{Kind: "single", Post: post, Store: "product"}); id != 0 {
		t.Errorf("store view got a main template: %d", id)
	}
	if id := wbtPick(srv, webbuilder.LocationMain, &siteQuery{Kind: "single", Post: post, Feed: "rss2"}); id != 0 {
		t.Errorf("feed got a template: %d", id)
	}
	// A draft is disabled; a template without a snapshot does not hide the next one.
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE posts SET status = 'draft' WHERE id = $1`, inNews.ID)
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE web_builder_docs SET html = '' WHERE post_id = $1`, srch.ID)
	srv.wbTemplatesChanged(ctx)
	if id := wbtPick(srv, webbuilder.LocationMain, single); id != sp.ID {
		t.Errorf("after disabling: %d", id)
	}
	if id := wbtPick(srv, webbuilder.LocationMain, search); id != arch.ID {
		t.Errorf("snapshot-less search template: %d", id)
	}
	// The returned doc is a copy of the published snapshot.
	c, _ := wbtCtx(srv, nil, "/")
	d, id := srv.wbPickTemplate(c, webbuilder.LocationMain, single)
	if d == nil || id != sp.ID || !strings.Contains(d.HTML, "Single") {
		t.Fatalf("doc %v %d", d, id)
	}
	d.HTML = "mutated"
	if d2, _ := srv.wbPickTemplate(c, webbuilder.LocationMain, single); d2.HTML == "mutated" {
		t.Error("the index doc was mutated through the result")
	}
	// The plugin off: nothing.
	_ = srv.settings.Set(ctx, map[string]any{webbuilder.PluginOption: false})
	if id := wbtPick(srv, webbuilder.LocationHeader, single); id != 0 {
		t.Errorf("plugin off: %d", id)
	}
}

func TestWBTemplatePreviewQuery(t *testing.T) {
	srv, base, _ := wbrStart(t)
	ctx := context.Background()
	live := wbtTemplate(t, srv, webbuilder.TemplateHeader, "Live", []string{"include/general"}, 0, "")
	draft := wbtTemplate(t, srv, webbuilder.TemplateHeader, "Draft", nil, 0, "draft")
	footer := wbtTemplate(t, srv, webbuilder.TemplateFooter, "Footer", nil, 0, "draft")
	admin, _ := srv.store.UserByID(ctx, 1)
	edID := asCreateUser(t, srv, "pq-editor", "editor", asStrongPass)
	editor, _ := srv.store.UserByID(ctx, edID)
	q := &siteQuery{Kind: "home"}

	c, w := wbtCtx(srv, admin, "/?"+webbuilder.PreviewQuery+"="+itoa64(draft.ID))
	if _, id := srv.wbPickTemplate(c, webbuilder.LocationHeader, q); id != draft.ID {
		t.Errorf("admin preview: %d", id)
	}
	if w.Header().Get(cache.HeaderControl) != "no-store" || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Errorf("preview cacheable: %v", w.Header())
	}
	if _, id := srv.wbPickTemplate(c, webbuilder.LocationFooter, q); id != 0 {
		t.Errorf("header preview fills the footer: %d", id)
	}
	c, _ = wbtCtx(srv, admin, "/?"+webbuilder.PreviewQuery+"="+itoa64(footer.ID))
	if _, id := srv.wbPickTemplate(c, webbuilder.LocationHeader, q); id != live.ID {
		t.Errorf("footer preview changed the header: %d", id)
	}
	for _, u := range []*store.User{nil, editor} {
		c, w := wbtCtx(srv, u, "/?"+webbuilder.PreviewQuery+"="+itoa64(draft.ID))
		if _, id := srv.wbPickTemplate(c, webbuilder.LocationHeader, q); id != live.ID {
			t.Errorf("preview without edit_theme_options: %d", id)
		}
		if w.Header().Get(cache.HeaderControl) != "" {
			t.Errorf("no-store for a visitor: %v", w.Header())
		}
	}
	_ = srv.store.TrashPost(ctx, draft.ID)
	c, _ = wbtCtx(srv, admin, "/?"+webbuilder.PreviewQuery+"="+itoa64(draft.ID))
	if _, id := srv.wbPickTemplate(c, webbuilder.LocationHeader, q); id != live.ID {
		t.Errorf("trashed preview: %d", id)
	}
	_ = base
}

func TestWBTemplatePurge(t *testing.T) {
	srv, _, admin := wbrStart(t)
	ctx := context.Background()
	var purges atomic.Int32
	prev := srv.onPurgeAll
	srv.onPurgeAll = func(c context.Context) {
		purges.Add(1)
		if prev != nil {
			prev(c)
		}
	}
	_, v, _ := admin.do(http.MethodPost, "/web-builder/v2/templates", map[string]any{"type": "footer"}, nil)
	id := int64(wbrObj(v)["id"].(float64))
	page := wbrPost(t, srv, "page", "Purge Page", "publish", 1)
	single := &siteQuery{Kind: "page", Post: page}

	// Publish (with a stale index built first): the template shows at once.
	if got := wbtPick(srv, webbuilder.LocationFooter, single); got != 0 {
		t.Fatalf("draft picked: %d", got)
	}
	gen := srv.wbTemplateState().gen.Load()
	n := purges.Load()
	doc, render := wbrFixture(t, "initial")
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(id, "/publish"), wbrPublishBody(doc, render, 0, "publish", map[string]any{"title": "Site footer"}), nil); code != http.StatusOK {
		t.Fatalf("publish: %d %v", code, v)
	} else if pl, _ := wbrObj(v)["permalink"].(string); !strings.Contains(pl, webbuilder.PreviewQuery+"="+itoa64(id)) || strings.Contains(pl, "?p=") {
		t.Errorf("template publish permalink %q, want its preview URL", pl)
	}
	if purges.Load() == n || srv.wbTemplateState().gen.Load() == gen {
		t.Error("publishing a template did not purge / bump the index")
	}
	if got := wbtPick(srv, webbuilder.LocationFooter, single); got != id {
		t.Errorf("published footer: %d", got)
	}
	// Conditions change: purge + new rules apply immediately.
	n = purges.Load()
	if code, v, _ := admin.do(http.MethodPut, "/web-builder/v2/templates/"+itoa64(id)+"/conditions", map[string]any{"conditions": []string{"include/archive"}}, nil); code != http.StatusOK {
		t.Fatalf("conditions: %d %v", code, v)
	}
	if purges.Load() == n {
		t.Error("conditions did not purge")
	}
	if got := wbtPick(srv, webbuilder.LocationFooter, single); got != 0 {
		t.Errorf("stale rules: %d", got)
	}
	// Unpublish (draft) through the builder.
	_ = srv.store.WebBuilder().SetTemplateConditions(ctx, id, []string{"include/general"}, 0)
	srv.wbTemplatesChanged(ctx)
	rev, _ := srv.store.WebBuilder().DocRev(ctx, id)
	n = purges.Load()
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(id, "/publish"), wbrPublishBody(doc, render, rev, "draft", map[string]any{"title": "Site footer"}), nil); code != http.StatusOK {
		t.Fatalf("draft: %d %v", code, v)
	}
	if purges.Load() == n || wbtPick(srv, webbuilder.LocationFooter, single) != 0 {
		t.Error("disabling did not take effect")
	}
	// The TTL bounds an index that missed a change.
	st := srv.wbTemplateState()
	st.idx.Store(&wbTemplateIndex{gen: st.gen.Load(), built: time.Now().Add(-wbTemplateTTL - time.Second), docs: map[int64]*store.WebBuilderDoc{}})
	if idx := srv.wbTemplateIndex(ctx); time.Since(idx.built) > time.Minute {
		t.Error("expired index was not rebuilt")
	}
}

func TestWBTemplateForms(t *testing.T) {
	srv, base, admin := wbrStart(t)
	_, v, _ := admin.do(http.MethodPost, "/web-builder/v2/templates", map[string]any{"type": "footer"}, nil)
	id := int64(wbrObj(v)["id"].(float64))
	doc, render := wbrFixture(t, "variants-form")
	if h, ok := render["html"].(string); ok {
		render["html"] = strings.ReplaceAll(h, "https://example.com/wp-json/web-builder/v2/forms/42/", admin.base+"/wp-json/web-builder/v2/forms/"+itoa64(id)+"/")
	}
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(id, "/publish"), wbrPublishBody(doc, render, 0, "publish", map[string]any{"title": "Footer"}), nil); code != http.StatusOK {
		t.Fatalf("publish: %d %v", code, v)
	}
	d := wbhDoc(t, srv, id)
	if !strings.Contains(d.HTML, "/web-builder/v2/forms/"+itoa64(id)+"/") {
		t.Fatalf("form action does not name the template: %s", d.HTML)
	}
	c, _ := wbtCtx(srv, nil, "/")
	p, _ := srv.store.GetPost(context.Background(), id)
	home := srv.links(c).HomeURL("/")
	origin := strings.TrimSuffix(home, "/")
	c.Request.Header.Set("Referer", origin+"/about/?x=1")
	if got := srv.wbFormPageURL(c, p); got != origin+"/about/?x=1" {
		t.Errorf("same-origin referer: %s", got)
	}
	// The request's Host header proves nothing: only the home URL's host counts.
	c.Request.Host = "example.test"
	for _, ref := range []string{"http://example.test/about/", "https://evil.test/x", "javascript:alert(1)", "", "http://user@example.test/x", "//evil.test/"} {
		c.Request.Header.Set("Referer", ref)
		if got := srv.wbFormPageURL(c, p); got != home {
			t.Errorf("referer %q: %s", ref, got)
		}
	}
	_ = base
}

func TestWBTemplateWXRRoundTrip(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	post := wbrPost(t, srv, "post", "Target Post", "publish", 1)
	cat := &store.Term{Taxonomy: "category", Slug: "wxr-news", Name: "WXR News"}
	if err := srv.store.InsertTerm(ctx, cat); err != nil {
		t.Fatal(err)
	}
	_ = srv.store.SetPostTerms(ctx, post.ID, "category", []int64{cat.ID})
	tp := wbtTemplate(t, srv, webbuilder.TemplateSinglePost, "WXR Single", []string{"include/singular/post/" + itoa64(post.ID),
		"include/singular/post/in/category/" + itoa64(cat.ID), "exclude/singular/page/999999", "include/singular/post/by_author/1"}, 7, "")
	_ = srv.store.WebBuilder().SetTemplatePreview(ctx, tp.ID, "post:"+itoa64(post.ID))

	code, xml, _ := get(t, admin.c, base+"/wp-admin/export?download=true&content=all")
	if code != http.StatusOK {
		t.Fatalf("export: %d", code)
	}
	for _, want := range []string{"<wp:post_type><![CDATA[wb_template]]></wp:post_type>", webbuilder.MetaTemplateType, webbuilder.MetaConditions, webbuilder.MetaPriority, wbWXRDocMeta} {
		if !strings.Contains(xml, want) {
			t.Fatalf("export lacks %s", want)
		}
	}
	// Import into a site without the template and the post: the post gets a new id.
	for _, id := range []int64{tp.ID, post.ID} {
		if err := srv.store.DeletePost(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	body := wbhImport(t, admin, base, xml)
	if !strings.Contains(body, " 1 patterns, 1 Theme Builder templates, ") && !strings.Contains(body, " 0 patterns, 1 Theme Builder templates, ") {
		t.Errorf("the report does not count the template on its own: %s", body)
	}
	if !strings.Contains(body, "exclude/singular/page/999999") {
		t.Errorf("the dropped rule is not in the report")
	}
	np, err := srv.store.PostBySlug(ctx, "post", post.Slug)
	if err != nil || np.ID == post.ID {
		t.Fatalf("post not re-imported: %v", err)
	}
	list, _ := srv.store.WebBuilder().Templates(ctx, store.WBTemplateFilter{Type: webbuilder.TemplateSinglePost})
	if len(list) != 1 {
		t.Fatalf("templates after import: %d", len(list))
	}
	got := list[0]
	want := "include/singular/post/" + itoa64(np.ID) + ",include/singular/post/in/category/" + itoa64(cat.ID) + ",include/singular/post/by_author/1"
	if strings.Join(got.Conditions, ",") != want || got.Priority != 7 || got.Type != webbuilder.TemplateSinglePost || got.Preview != "post:"+itoa64(np.ID) {
		t.Errorf("imported template %+v, want rules %s", got, want)
	}
	d := wbhDoc(t, srv, got.ID)
	if d.HTML != "" || d.Renderer != 0 {
		t.Errorf("imported snapshot %q", d.HTML)
	}
	// Without a snapshot it does not render until it is published in the editor.
	if id := wbtPick(srv, webbuilder.LocationMain, &siteQuery{Kind: "single", Post: np}); id != 0 {
		t.Errorf("snapshot-less import rendered: %d", id)
	}
	// Importing the same file again does not add a second copy of a template without a slug.
	wbhImport(t, admin, base, xml)
	if list, _ = srv.store.WebBuilder().Templates(ctx, store.WBTemplateFilter{Type: webbuilder.TemplateSinglePost}); len(list) != 1 {
		t.Errorf("templates after a second import: %d", len(list))
	}
	// An author the target site does not have: the rule about them is dropped, not moved to the
	// importing user.
	if err := srv.store.DeletePost(ctx, got.ID); err != nil {
		t.Fatal(err)
	}
	ghost := regexp.MustCompile(`<wp:author_login>(<!\[CDATA\[)?admin(\]\]>)?</wp:author_login>`).ReplaceAllString(xml, "<wp:author_login>ghost-author</wp:author_login>")
	if ghost == xml {
		t.Fatal("no admin author in the export")
	}
	body = wbhImport(t, admin, base, ghost)
	list, _ = srv.store.WebBuilder().Templates(ctx, store.WBTemplateFilter{Type: webbuilder.TemplateSinglePost})
	if len(list) != 1 {
		t.Fatalf("templates after the ghost import: %d", len(list))
	}
	if strings.Contains(strings.Join(list[0].Conditions, ","), "by_author") {
		t.Errorf("an unmatched author's rule was kept: %v", list[0].Conditions)
	}
	if !strings.Contains(body, "by_author/1") {
		t.Errorf("the dropped author rule is not in the report")
	}
}

func TestWBTemplateGolylang(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	if !containsStr(srv.pllPostTypes(), webbuilder.TemplatePostType) {
		t.Fatal("templates are not a translated post type")
	}
	golylangToggle(t, admin.c, base, "activate")
	pllAddLanguage(t, admin.c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, admin.c, base, "Français", "fr_FR", "fr", "fr")
	en := wbtTemplate(t, srv, webbuilder.TemplateHeader, "Header EN", []string{"include/general"}, 0, "")
	fr := wbtTemplate(t, srv, webbuilder.TemplateHeader, "Header FR", []string{"include/general"}, 0, "")
	_ = srv.pllSetLang(ctx, pllPost, en.ID, "en")
	_ = srv.pllSetLang(ctx, pllPost, fr.ID, "fr")
	if err := srv.pllSaveTranslations(ctx, pllPost, map[string]int64{"en": en.ID, "fr": fr.ID}); err != nil {
		t.Fatal(err)
	}
	srv.wbTemplatesChanged(ctx)
	q := &siteQuery{Kind: "home"}
	for lang, want := range map[string]int64{"fr": fr.ID, "en": en.ID} {
		c, _ := wbtCtx(srv, nil, "/")
		pllSet(c, &pllReq{srv: srv, Lang: srv.pllLang(ctx, lang), Filter: true, Front: true})
		if _, id := srv.wbPickTemplate(c, webbuilder.LocationHeader, q); id != want {
			t.Errorf("%s: %d, want %d", lang, id, want)
		}
	}

	// Duplicate keeps the language, not the translation group.
	src, err := srv.store.WebBuilder().Template(ctx, fr.ID)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := srv.wbDuplicateTemplate(ctx, src, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.pllObjLang(ctx, pllPost, cp.ID); got != "fr" {
		t.Errorf("duplicate language = %q, want fr", got)
	}
	if tr := srv.pllTranslations(ctx, pllPost, cp.ID); len(tr) != 1 || tr["fr"] != cp.ID {
		t.Errorf("duplicate joined the translation group: %v", tr)
	}
	if tr := srv.pllTranslations(ctx, pllPost, fr.ID); tr["fr"] != fr.ID || tr["en"] != en.ID {
		t.Errorf("source group changed: %v", tr)
	}

	// A new translation copies the template's type, conditions and priority.
	foot := wbtTemplate(t, srv, webbuilder.TemplateFooter, "Footer EN", []string{"include/general", "exclude/404"}, 9, "")
	_ = srv.pllSetLang(ctx, pllPost, foot.ID, "en")
	dst, err := srv.store.WebBuilder().CreateTemplate(ctx, store.NewWBTemplate{Type: webbuilder.TemplateHeader, Title: "Pied FR", AuthorID: 1})
	if err != nil {
		t.Fatal(err)
	}
	srcPost, _ := srv.store.GetPost(ctx, foot.ID)
	dstPost, _ := srv.store.GetPost(ctx, dst.ID)
	srv.pllCopyForTranslation(ctx, srcPost, dstPost, srv.pllLang(ctx, "fr"), false)
	got, err := srv.store.WebBuilder().Template(ctx, dst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != webbuilder.TemplateFooter || strings.Join(got.Conditions, ",") != "include/general,exclude/404" || got.Priority != 9 {
		t.Errorf("translation template meta: %+v", got)
	}
}

// A failed index build renders the theme defaults but marks the response uncacheable, so the
// degraded page does not outlive the error in WPGo Cache.
func TestWBTemplateIndexErrorNotCached(t *testing.T) {
	srv, _, _ := wbrStart(t)
	post := wbrPost(t, srv, "post", "Err Post", "publish", 1)
	wbtTemplate(t, srv, webbuilder.TemplateHeader, "General header", []string{"include/general"}, 0, "")
	srv.wbTemplatesChanged(context.Background()) // the next pick rebuilds the index
	c, _ := wbtCtx(srv, nil, "/")
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = c.Request.WithContext(cctx)
	if _, id := srv.wbPickTemplate(c, webbuilder.LocationHeader, &siteQuery{Kind: "single", Post: post}); id != 0 {
		t.Fatalf("picked %d with a failed index", id)
	}
	if got := c.Writer.Header().Get(cache.HeaderControl); got != "no-store" {
		t.Errorf("%s = %q after a failed index build", cache.HeaderControl, got)
	}
	if id := wbtPick(srv, webbuilder.LocationHeader, &siteQuery{Kind: "single", Post: post}); id == 0 {
		t.Error("the index did not recover")
	}
}
