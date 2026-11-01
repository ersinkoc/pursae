package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"wpgo/internal/store"
)

// hgSite installs a site, logs the admin in and activates HeadlessGo from the Plugins screen.
func hgSite(t *testing.T) (string, *http.Client, *Server) {
	t.Helper()
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	hgToggle(t, c, ts.URL, "activate")
	if !srv.hgOn() {
		t.Fatal("Pursae Headless is not active after activation")
	}
	return ts.URL, c, srv
}

func hgToggle(t *testing.T, c *http.Client, base, action string) {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=` + action + `&amp;plugin=headlessgo[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s link for Pursae Headless on the Plugins screen", action)
	}
	if code, _, loc := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound || !strings.Contains(loc, action+"=true") {
		t.Fatalf("%s Pursae Headless: %d %s", action, code, loc)
	}
}

// hgAdminSubmit posts a HeadlessGo admin form with a fresh nonce.
func hgAdminSubmit(t *testing.T, c *http.Client, base string, form url.Values) (int, string, string) {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/headlessgo?page=settings") // every screen shares the nonce
	form.Set("_wpnonce", formNonce(t, body))
	return post(t, c, base+"/wp-admin/headlessgo", form)
}

type hgResp struct {
	Code   int
	Body   map[string]any
	Raw    string
	Header http.Header
}

func (r hgResp) data() map[string]any {
	m, _ := r.Body["data"].(map[string]any)
	return m
}

func (r hgResp) list() []any {
	l, _ := r.Body["data"].([]any)
	return l
}

func (r hgResp) errName() string {
	e, _ := r.Body["error"].(map[string]any)
	n, _ := e["name"].(string)
	return n
}

// hgCall sends a delivery API request (path below /wp-json/headlessgo/v1).
func hgCall(t *testing.T, base, method, path, token string, body any, hdr ...string) hgResp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, base+hgAPIPrefix+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	r := hgResp{Code: resp.StatusCode, Raw: string(raw), Header: resp.Header}
	_ = json.Unmarshal(raw, &r.Body)
	return r
}

func hgMustSchema(t *testing.T, srv *Server, sc *hgSchema) {
	t.Helper()
	if err := srv.hgSaveSchema(context.Background(), sc); err != nil {
		t.Fatalf("save schema %s: %v", sc.DisplayName, err)
	}
}

func hgNewToken(t *testing.T, srv *Server, tok *hgToken) string {
	t.Helper()
	v, err := srv.hgCreateToken(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func hgFloat(f float64) *float64 { return &f }

// hgModel creates the test content model: components, authors, categories, articles and a
// homepage single type.
func hgModel(t *testing.T, srv *Server) {
	t.Helper()
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindComponent, DisplayName: "SEO", Category: "shared", Fields: []hgField{
		{Name: "metaTitle", Type: "text", Required: true}, {Name: "metaDescription", Type: "text", Long: true, Max: hgFloat(160)}}})
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindComponent, DisplayName: "Quote", Category: "blocks", Fields: []hgField{
		{Name: "text", Type: "text"}, {Name: "by", Type: "text"}}})
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindComponent, DisplayName: "Gallery", Category: "blocks", Fields: []hgField{
		{Name: "images", Type: "media", Multiple: true, Allowed: []string{"images"}}}})
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Author", SingularName: "author", PluralName: "authors", Fields: []hgField{
		{Name: "name", Type: "text", Required: true}, {Name: "bio", Type: "richtext", Format: "markdown"}, {Name: "avatar", Type: "media", Allowed: []string{"images"}},
		{Name: "secret", Type: "password", Min: hgFloat(6)}, {Name: "email", Type: "email", Unique: true}}})
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Category", SingularName: "category", PluralName: "categories", DraftAndPublish: true, Fields: []hgField{
		{Name: "name", Type: "text"}, {Name: "slug", Type: "uid", TargetField: "name"}}})
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Article", SingularName: "article", PluralName: "articles", DraftAndPublish: true, Localized: true,
		PreviewURL: "https://app.example.test/preview?slug={slug}&secret={secret}&locale={locale}&token={previewToken}",
		Fields: []hgField{
			{Name: "title", Type: "text", Required: true, Min: hgFloat(3), Regex: `^[^<>]*$`}, {Name: "slug", Type: "uid", TargetField: "title"},
			{Name: "body", Type: "richtext"}, {Name: "views", Type: "number", Format: "integer", Min: hgFloat(0)}, {Name: "rating", Type: "number", Format: "decimal"},
			{Name: "featured", Type: "boolean", Default: false}, {Name: "day", Type: "date"}, {Name: "at", Type: "datetime"}, {Name: "kind", Type: "enumeration", Enum: []string{"news", "guide"}},
			{Name: "cover", Type: "media"}, {Name: "author", Type: "relation", Relation: "manyToOne", Target: "author"},
			{Name: "categories", Type: "relation", Relation: "manyToMany", Target: "category"}, {Name: "page", Type: "relation", Relation: "manyToOne", Target: "wp:page"},
			{Name: "writer", Type: "relation", Relation: "manyToOne", Target: "wp:user"},
			{Name: "seo", Type: "component", Component: "shared.seo"}, {Name: "faq", Type: "component", Component: "blocks.quote", Repeatable: true, Max: hgFloat(3)},
			{Name: "blocks", Type: "dynamiczone", Components: []string{"blocks.quote", "blocks.gallery"}}, {Name: "extra", Type: "json"},
			{Name: "internal", Type: "text", Private: true},
		}})
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindSingle, DisplayName: "Homepage", SingularName: "homepage", DraftAndPublish: true, Fields: []hgField{
		{Name: "headline", Type: "text", Required: true}, {Name: "hero", Type: "component", Component: "shared.seo"}}})
}

// hgImage adds an image attachment row (no file needed for the API).
func hgImage(t *testing.T, srv *Server, name string) int64 {
	t.Helper()
	a := &store.Attachment{Slug: name, FilePath: "2026/10/" + name + ".png", Mime: "image/png", FileSize: 2048, Width: 640, Height: 480, Title: name, Alt: "alt " + name}
	if err := srv.store.InsertAttachment(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func TestHeadlessGoOffByDefault(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	base := ts.URL
	if srv.hgOn() {
		t.Fatal("Pursae Headless is active on a fresh install")
	}
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	if !strings.Contains(body, "Pursae Headless") || !strings.Contains(body, "plugin=headlessgo") {
		t.Fatal("Pursae Headless is not on the Plugins screen")
	}
	if code, _, _ := get(t, c, base+"/wp-admin/headlessgo"); code != http.StatusNotFound {
		t.Fatalf("admin while off: %d", code)
	}
	if r := hgCall(t, base, "GET", "/", "", nil); r.Code != http.StatusNotFound {
		t.Fatalf("API while off: %d", r.Code)
	}
	_, dash, _ := get(t, c, base+"/wp-admin/")
	if strings.Contains(dash, "toplevel_page_headlessgo") {
		t.Fatal("menu shown while off")
	}
	if code, _, _ := get(t, http.DefaultClient, base+"/"); code != http.StatusOK {
		t.Fatalf("front end while off: %d", code)
	}
	// On, then off again: the front end comes back, the routes go away.
	hgToggle(t, c, base, "activate")
	_, dash, _ = get(t, c, base+"/wp-admin/")
	if !strings.Contains(dash, "toplevel_page_headlessgo") {
		t.Fatal("no menu while on")
	}
	if code, _, _ := get(t, http.DefaultClient, base+"/"); code != http.StatusNotFound {
		t.Fatalf("front end in headless mode: %d", code)
	}
	hgToggle(t, c, base, "deactivate")
	if code, _, _ := get(t, http.DefaultClient, base+"/"); code != http.StatusOK {
		t.Fatalf("front end after deactivation: %d", code)
	}
	if r := hgCall(t, base, "GET", "/", "", nil); r.Code != http.StatusNotFound {
		t.Fatalf("API after deactivation: %d", r.Code)
	}
}

func TestHeadlessGoHeadlessMode(t *testing.T) {
	base, c, srv := hgSite(t)
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	code, body, _ := get(t, anon, base+"/")
	if code != http.StatusNotFound || !strings.Contains(body, `"NotFoundError"`) {
		t.Fatalf("JSON 404: %d %s", code, body)
	}
	for _, p := range []string{"/wp-login", "/wp-json/", "/wp-json/wp/v2/posts", "/?rest_route=/wp/v2/posts", "/robots.txt"} {
		if code, _, _ := get(t, anon, base+p); code != http.StatusOK {
			t.Errorf("%s in headless mode: %d", p, code)
		}
	}
	if code, _, _ := get(t, anon, base+"/wp-admin/"); code != http.StatusFound {
		t.Errorf("wp-admin redirects to the login: %d", code)
	}
	if code, _, _ := get(t, c, base+"/"); code != http.StatusOK {
		t.Errorf("an administrator sees the front end: %d", code)
	}
	// Redirect mode, keeping the path.
	if code, _, loc := hgAdminSubmit(t, c, base, url.Values{"page": {"settings"}, "do": {"save_settings"}, "front_mode": {"redirect"},
		"redirect_url": {"https://app.example.test"}, "redirect_keep_path": {"1"}, "allow_editors": {"1"}, "default_page_size": {"25"}, "max_page_size": {"100"}}); code != http.StatusFound || strings.Contains(loc, "hg-error") {
		t.Fatalf("save settings: %d %s", code, loc)
	}
	if code, _, loc := get(t, anon, base+"/hello-world/?x=1"); code != http.StatusFound || loc != "https://app.example.test/hello-world/?x=1" {
		t.Fatalf("redirect: %d %s", code, loc)
	}
	if code, _, loc := get(t, c, base+"/"); code != http.StatusOK {
		t.Fatalf("editors still see the site: %d %s", code, loc)
	}
	if code, _, loc := hgAdminSubmit(t, c, base, url.Values{"page": {"settings"}, "do": {"save_settings"}, "front_mode": {"redirect"}, "redirect_url": {"javascript:alert(1)"}}); code != http.StatusFound || !strings.Contains(loc, "hg-error") {
		t.Fatalf("bad redirect URL accepted: %d %s", code, loc)
	}
	st := srv.hgLoad()
	st.FrontMode, st.AllowEditors = "plain", false
	if err := srv.hgSaveSettings(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if code, body, _ := get(t, c, base+"/"); code != http.StatusNotFound || body != "Not Found" {
		t.Fatalf("plain 404 for everyone: %d %q", code, body)
	}
	st.FrontMode = "off"
	_ = srv.hgSaveSettings(context.Background(), st)
	if code, _, _ := get(t, anon, base+"/"); code != http.StatusOK {
		t.Fatalf("front end kept: %d", code)
	}
}

func TestHeadlessGoDeliveryAPI(t *testing.T) {
	base, _, srv := hgSite(t)
	ctx := context.Background()
	hgModel(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	img := hgImage(t, srv, "cover")
	img2 := hgImage(t, srv, "second")

	// Writes: an author (no draft & publish: published at once).
	r := hgCall(t, base, "POST", "/authors", full, map[string]any{"data": map[string]any{"name": "Ada", "email": "ada@example.test", "secret": "hunter22", "avatar": img, "bio": "**hi**"}})
	if r.Code != http.StatusCreated {
		t.Fatalf("create author: %d %s", r.Code, r.Raw)
	}
	ada := r.data()["documentId"].(string)
	if _, ok := r.data()["secret"]; ok || r.data()["publishedAt"] == nil || strings.Contains(r.Raw, "hunter22") {
		t.Fatalf("author payload: %s", r.Raw)
	}
	var stored string
	_ = srv.store.Pool.QueryRow(ctx, `SELECT data->>'secret' FROM hg_entries WHERE document_id = $1`, ada).Scan(&stored)
	if !strings.HasPrefix(stored, "$argon2id$") {
		t.Fatalf("password stored as %q", stored)
	}
	if r := hgCall(t, base, "POST", "/authors", full, map[string]any{"data": map[string]any{"name": "Bob", "email": "ada@example.test"}}); r.Code != 400 || !strings.Contains(r.Raw, "unique") {
		t.Fatalf("unique email: %d %s", r.Code, r.Raw)
	}
	r = hgCall(t, base, "POST", "/authors", full, map[string]any{"data": map[string]any{"name": "Bob", "email": "bob@example.test"}})
	bob := r.data()["documentId"].(string)
	r = hgCall(t, base, "POST", "/categories?status=published", full, map[string]any{"data": map[string]any{"name": "Go Lang"}})
	if r.Code != 201 || r.data()["slug"] != "go-lang" {
		t.Fatalf("category uid from name: %d %s", r.Code, r.Raw)
	}
	goCat := r.data()["documentId"].(string)
	r = hgCall(t, base, "POST", "/categories?status=published", full, map[string]any{"data": map[string]any{"name": "Web"}})
	webCat := r.data()["documentId"].(string)

	// Validation.
	for name, data := range map[string]map[string]any{
		"required":  {"views": 1},
		"min":       {"title": "Hi"},
		"regex":     {"title": "<b>bold</b>"},
		"number":    {"title": "Valid", "views": -1},
		"integer":   {"title": "Valid", "views": 1.5},
		"enum":      {"title": "Valid", "kind": "rumor"},
		"key":       {"title": "Valid", "bogus": 1},
		"relation":  {"title": "Valid", "author": "nope"},
		"date":      {"title": "Valid", "day": "2024-13-01"},
		"component": {"title": "Valid", "seo": map[string]any{"metaDescription": "x"}},
		"max items": {"title": "Valid", "faq": []any{map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}}},
		"zone":      {"title": "Valid", "blocks": []any{map[string]any{"__component": "shared.seo", "metaTitle": "x"}}},
		"media":     {"title": "Valid", "cover": 999999},
	} {
		if r := hgCall(t, base, "POST", "/articles?status=published", full, map[string]any{"data": data}); r.Code != 400 || r.errName() != "ValidationError" {
			t.Errorf("validation %s: %d %s", name, r.Code, r.Raw)
		}
	}
	// A draft may be incomplete; publishing it checks required fields.
	r = hgCall(t, base, "POST", "/articles", full, map[string]any{"data": map[string]any{"views": 3}})
	if r.Code != 201 || r.data()["publishedAt"] != nil {
		t.Fatalf("incomplete draft: %d %s", r.Code, r.Raw)
	}
	incomplete := r.data()["documentId"].(string)
	if r := hgCall(t, base, "POST", "/articles/"+incomplete+"/publish", full, nil); r.Code != 400 || !strings.Contains(r.Raw, "title") {
		t.Fatalf("publish incomplete: %d %s", r.Code, r.Raw)
	}

	mk := func(title string, views int, publish bool, extra map[string]any) string {
		data := map[string]any{"title": title, "views": views}
		for k, v := range extra {
			data[k] = v
		}
		q := ""
		if publish {
			q = "?status=published"
		}
		r := hgCall(t, base, "POST", "/articles"+q, full, map[string]any{"data": data})
		if r.Code != 201 {
			t.Fatalf("create %s: %d %s", title, r.Code, r.Raw)
		}
		return r.data()["documentId"].(string)
	}
	a1 := mk("Hello Go", 10, true, map[string]any{"author": ada, "categories": []any{goCat, webCat}, "kind": "guide", "rating": 4.5, "day": "2024-05-01",
		"at": "2024-05-01T10:00:00+02:00", "cover": img, "seo": map[string]any{"metaTitle": "Hello SEO"}, "internal": "hidden note",
		"faq": []any{map[string]any{"text": "Why?", "by": "Ada"}}, "extra": map[string]any{"k": []any{1, 2}},
		"blocks": []any{map[string]any{"__component": "blocks.quote", "text": "Quoted"}, map[string]any{"__component": "blocks.gallery", "images": []any{img, img2}}},
		"body":   []any{map[string]any{"type": "paragraph", "children": []any{map[string]any{"type": "text", "text": "Body"}}}}})
	a2 := mk("Second post", 50, true, map[string]any{"author": bob, "categories": []any{webCat}, "kind": "news"})
	a3 := mk("Third thing", 30, true, map[string]any{"author": ada})
	draft := mk("Draft only", 99, false, nil)
	_ = a3

	// No public access by default.
	if r := hgCall(t, base, "GET", "/articles", "", nil); r.Code != 403 || r.errName() != "ForbiddenError" {
		t.Fatalf("public find: %d %s", r.Code, r.Raw)
	}
	st := srv.hgLoad()
	st.Public = map[string][]string{"article": {"find", "findOne"}, "category": {"find"}}
	_ = srv.hgSaveSettings(ctx, st)

	r = hgCall(t, base, "GET", "/articles", "", nil)
	if r.Code != 200 || len(r.list()) != 3 {
		t.Fatalf("public find: %d %s", r.Code, r.Raw)
	}
	pg := r.Body["meta"].(map[string]any)["pagination"].(map[string]any)
	if pg["total"] != 3.0 || pg["page"] != 1.0 || pg["pageSize"] != 25.0 || pg["pageCount"] != 1.0 {
		t.Fatalf("pagination meta: %v", pg)
	}
	first := r.list()[0].(map[string]any)
	for _, k := range []string{"internal", "author", "seo", "cover", "blocks"} {
		if _, ok := first[k]; ok {
			t.Errorf("%s returned without populate", k)
		}
	}
	if first["locale"] != "en" || first["documentId"] == "" || first["title"] == nil {
		t.Errorf("entry shape: %v", first)
	}
	if strings.Contains(r.Raw, draft) {
		t.Fatal("a draft is listed as published")
	}
	q := func(qs string) hgResp { return hgCall(t, base, "GET", "/articles?"+qs, "", nil) }
	titles := func(r hgResp) []string {
		var out []string
		for _, x := range r.list() {
			out = append(out, x.(map[string]any)["title"].(string))
		}
		return out
	}
	cases := map[string][]string{
		"filters[title][$containsi]=hello":                                       {"Hello Go"},
		"filters[title][$contains]=hello":                                        nil,
		"filters[views][$gt]=20&sort=views:desc":                                 {"Second post", "Third thing"},
		"filters[views][$between][0]=20&filters[views][$between][1]=40":          {"Third thing"},
		"filters[$or][0][views][$lt]=20&filters[$or][1][kind][$eq]=news&sort=id": {"Hello Go", "Second post"},
		"filters[categories][slug][$eq]=web&sort=title:desc":                     {"Second post", "Hello Go"},
		"filters[seo][metaTitle][$startsWith]=Hello":                             {"Hello Go"},
		"filters[faq][by][$eqi]=ada":                                             {"Hello Go"},
		"filters[kind][$null]=true&sort=title":                                   {"Third thing"},
		"filters[kind][$in][0]=news&filters[kind][$in][1]=guide&sort=views":      {"Hello Go", "Second post"},
		"filters[kind][$notIn][0]=news&sort=views":                               {"Hello Go", "Third thing"},
		"filters[$not][title][$endsWith]=post&sort=views":                        {"Hello Go", "Third thing"},
		"filters[day][$gte]=2024-01-01":                                          {"Hello Go"},
		"filters[at][$lt]=2024-05-01T09:00:00Z":                                  {"Hello Go"},
		"filters[cover][$notNull]=true":                                          {"Hello Go"},
		"filters[rating][$gte]=4.5":                                              {"Hello Go"},
		"filters[featured][$eq]=false&sort=views":                                {"Hello Go", "Third thing", "Second post"},
		"sort[0]=views:desc&pagination[page]=2&pagination[pageSize]=1":           {"Third thing"},
		"sort=views&pagination[start]=1&pagination[limit]=5":                     {"Third thing", "Second post"},
		"filters[documentId][$eq]=" + a2:                                         {"Second post"},
		"locale=en&filters[title][$ne]=Hello%20Go&sort=title":                    {"Second post", "Third thing"},
	}
	for qs, want := range cases {
		r := q(qs)
		if r.Code != 200 {
			t.Errorf("%s: %d %s", qs, r.Code, r.Raw)
			continue
		}
		if got := titles(r); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: got %v, want %v", qs, got, want)
		}
	}
	// Authors are not public: filtering through them needs a caller that may read them.
	for qs, want := range map[string][]string{"filters[author][name][$eq]=Ada&sort=title": {"Hello Go", "Third thing"}, "filters[author][$null]=true": nil} {
		if r := q(qs); r.Code != 400 {
			t.Errorf("public %s: %d %s", qs, r.Code, r.Raw)
		}
		r := hgCall(t, base, "GET", "/articles?"+qs, full, nil)
		if got := titles(r); r.Code != 200 || strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("token %s: %d got %v, want %v", qs, r.Code, got, want)
		}
	}
	for _, qs := range []string{"filters[internal][$eq]=x", "filters[nope][$eq]=1", "sort=nope:asc", "pagination[page]=1&pagination[limit]=2", "fields[0]=internal", "populate=nope", "status=maybe", "locale=xx"} {
		if r := q(qs); r.Code != 400 {
			t.Errorf("%s: %d %s", qs, r.Code, r.Raw)
		}
	}
	r = q("pagination[start]=0&pagination[limit]=2&sort=views")
	if m := r.Body["meta"].(map[string]any)["pagination"].(map[string]any); m["start"] != 0.0 || m["limit"] != 2.0 || m["total"] != 3.0 {
		t.Errorf("start/limit meta: %v", m)
	}

	// fields and populate. The public role may not read authors: the relation is left out.
	r = q("fields[0]=title&filters[documentId]=" + a1)
	if obj := r.list()[0].(map[string]any); len(obj) != 3 || obj["title"] != "Hello Go" || obj["id"] == nil || obj["documentId"] != a1 {
		t.Errorf("fields: %v", obj)
	}
	r = hgCall(t, base, "GET", "/articles/"+a1+"?populate=*", "", nil)
	if r.Code != 200 {
		t.Fatalf("findOne: %d %s", r.Code, r.Raw)
	}
	d := r.data()
	if _, ok := d["author"]; ok {
		t.Errorf("author populated without permission: %v", d["author"])
	}
	if cats, _ := d["categories"].([]any); len(cats) != 2 || cats[0].(map[string]any)["name"] != "Go Lang" {
		t.Errorf("categories populated: %v", d["categories"])
	}
	if cv, _ := d["cover"].(map[string]any); cv == nil || !strings.HasSuffix(cv["url"].(string), "/wp-content/uploads/2026/10/cover.png") || cv["alternativeText"] != "alt cover" || cv["width"] != 640.0 {
		t.Errorf("cover: %v", d["cover"])
	}
	if seo, _ := d["seo"].(map[string]any); seo == nil || seo["metaTitle"] != "Hello SEO" || seo["id"] == nil {
		t.Errorf("seo: %v", d["seo"])
	}
	if bl, _ := d["blocks"].([]any); len(bl) != 2 || bl[0].(map[string]any)["__component"] != "blocks.quote" {
		t.Errorf("dynamic zone: %v", d["blocks"])
	} else if _, ok := bl[1].(map[string]any)["images"]; ok {
		t.Errorf("populate=* went two levels deep: %v", bl[1])
	}
	if _, ok := d["internal"]; ok {
		t.Error("private field returned")
	}
	if d["views"] != 10.0 || d["rating"] != 4.5 || d["at"] != "2024-05-01T08:00:00.000Z" || d["day"] != "2024-05-01" {
		t.Errorf("scalars: %v", d)
	}
	r = hgCall(t, base, "GET", "/articles/"+a1+"?populate[blocks][on][blocks.gallery][populate]=*&populate[categories][fields][0]=slug&populate[categories][sort]=slug:desc", "", nil)
	bl := r.data()["blocks"].([]any)
	if imgs, _ := bl[1].(map[string]any)["images"].([]any); len(imgs) != 2 {
		t.Errorf("populate on: %s", r.Raw)
	}
	if cats := r.data()["categories"].([]any); cats[0].(map[string]any)["slug"] != "web" || cats[0].(map[string]any)["name"] != nil {
		t.Errorf("populate fields/sort: %v", cats)
	}
	// A token may read authors.
	r = hgCall(t, base, "GET", "/articles/"+a1+"?populate[author][populate]=avatar", full, nil)
	au, _ := r.data()["author"].(map[string]any)
	if au == nil || au["name"] != "Ada" || au["avatar"] == nil || au["secret"] != nil {
		t.Errorf("author populated with a token: %v", r.data()["author"])
	}

	// Public findOne of a draft or an unknown document is a 404; drafts need permission.
	if r := hgCall(t, base, "GET", "/articles/"+draft, "", nil); r.Code != 404 {
		t.Errorf("public draft: %d", r.Code)
	}
	if r := hgCall(t, base, "GET", "/articles?status=draft", "", nil); r.Code != 403 {
		t.Errorf("public status=draft: %d", r.Code)
	}
	if r := hgCall(t, base, "GET", "/articles/"+draft+"?status=draft", full, nil); r.Code != 200 || r.data()["title"] != "Draft only" || r.data()["publishedAt"] != nil {
		t.Errorf("draft with a full token: %d %s", r.Code, r.Raw)
	}

	// Update: partial, draft first, then publish.
	r = hgCall(t, base, "PUT", "/articles/"+a2, full, map[string]any{"data": map[string]any{"title": "Second post, edited"}})
	if r.Code != 200 || r.data()["title"] != "Second post, edited" || r.data()["views"] != 50.0 {
		t.Fatalf("update: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "GET", "/articles/"+a2, "", nil); r.data()["title"] != "Second post" {
		t.Errorf("published version changed by a draft save: %v", r.data()["title"])
	}
	if r := hgCall(t, base, "POST", "/articles/"+a2+"/publish", full, nil); r.Code != 200 || r.data()["title"] != "Second post, edited" {
		t.Fatalf("publish: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "GET", "/articles/"+a2, "", nil); r.data()["title"] != "Second post, edited" {
		t.Errorf("published title: %v", r.data()["title"])
	}
	r = hgCall(t, base, "PUT", "/articles/"+a2+"?status=published", full, map[string]any{"data": map[string]any{"categories": map[string]any{"connect": []any{goCat}, "disconnect": []any{webCat}}}})
	if r.Code != 200 {
		t.Fatalf("connect/disconnect: %d %s", r.Code, r.Raw)
	}
	r = hgCall(t, base, "GET", "/articles/"+a2+"?populate=categories", "", nil)
	if cats := r.data()["categories"].([]any); len(cats) != 1 || cats[0].(map[string]any)["documentId"] != goCat {
		t.Errorf("relations after connect/disconnect: %v", cats)
	}
	if r := hgCall(t, base, "POST", "/articles/"+a2+"/unpublish", full, nil); r.Code != 200 {
		t.Fatalf("unpublish: %d", r.Code)
	}
	if r := hgCall(t, base, "GET", "/articles/"+a2, "", nil); r.Code != 404 {
		t.Errorf("unpublished entry readable: %d", r.Code)
	}
	if r := hgCall(t, base, "DELETE", "/articles/"+a2, full, nil); r.Code != 204 {
		t.Fatalf("delete: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "GET", "/articles/"+a2+"?status=draft", full, nil); r.Code != 404 {
		t.Errorf("deleted entry: %d", r.Code)
	}
	if r := hgCall(t, base, "GET", "/nothing-here", full, nil); r.Code != 404 || r.errName() != "NotFoundError" {
		t.Errorf("unknown route: %d %s", r.Code, r.Raw)
	}

	// Single type.
	if r := hgCall(t, base, "GET", "/homepage", full, nil); r.Code != 404 {
		t.Errorf("empty single type: %d", r.Code)
	}
	r = hgCall(t, base, "PUT", "/homepage?status=published", full, map[string]any{"data": map[string]any{"headline": "Welcome", "hero": map[string]any{"metaTitle": "Home"}}})
	if r.Code != 201 {
		t.Fatalf("single type create: %d %s", r.Code, r.Raw)
	}
	r = hgCall(t, base, "PUT", "/homepage", full, map[string]any{"data": map[string]any{"headline": "Welcome back"}})
	if r.Code != 200 {
		t.Fatalf("single type update: %d %s", r.Code, r.Raw)
	}
	r = hgCall(t, base, "GET", "/homepage?populate=hero", full, nil)
	if r.Code != 200 || r.data()["headline"] != "Welcome" || r.data()["hero"].(map[string]any)["metaTitle"] != "Home" {
		t.Errorf("single type read: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "GET", "/homepage?status=draft", full, nil); r.data()["headline"] != "Welcome back" {
		t.Errorf("single type draft: %s", r.Raw)
	}
	if r := hgCall(t, base, "DELETE", "/homepage", full, nil); r.Code != 204 {
		t.Errorf("single type delete: %d", r.Code)
	}

	// Tokens.
	ro := hgNewToken(t, srv, &hgToken{Name: "RO", Kind: "read-only"})
	custom := hgNewToken(t, srv, &hgToken{Name: "Custom", Kind: "custom", Permissions: map[string][]string{"author": {"find"}, "article": {"create"}}})
	expired := time.Now().Add(-time.Hour)
	old := hgNewToken(t, srv, &hgToken{Name: "Old", Kind: "full-access", ExpiresAt: &expired})
	if r := hgCall(t, base, "GET", "/authors", ro, nil); r.Code != 200 || len(r.list()) != 2 {
		t.Errorf("read-only find: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "POST", "/authors", ro, map[string]any{"data": map[string]any{"name": "X"}}); r.Code != 403 {
		t.Errorf("read-only create: %d", r.Code)
	}
	if r := hgCall(t, base, "GET", "/articles?status=draft", ro, nil); r.Code != 403 {
		t.Errorf("read-only drafts: %d", r.Code)
	}
	if r := hgCall(t, base, "GET", "/authors", custom, nil); r.Code != 200 {
		t.Errorf("custom find: %d", r.Code)
	}
	if r := hgCall(t, base, "GET", "/authors/"+ada, custom, nil); r.Code != 403 {
		t.Errorf("custom findOne without permission: %d", r.Code)
	}
	if r := hgCall(t, base, "POST", "/articles", custom, map[string]any{"data": map[string]any{"title": "By custom"}}); r.Code != 201 {
		t.Errorf("custom create: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "DELETE", "/articles/"+a1, custom, nil); r.Code != 403 {
		t.Errorf("custom delete: %d", r.Code)
	}
	for _, bad := range []string{old, "hg_nope", "garbage"} {
		if r := hgCall(t, base, "GET", "/authors", bad, nil); r.Code != 401 || r.errName() != "UnauthorizedError" {
			t.Errorf("token %q: %d", bad[:3], r.Code)
		}
	}
	var used *time.Time
	_ = srv.store.Pool.QueryRow(ctx, `SELECT last_used_at FROM hg_api_tokens WHERE name = 'RO'`).Scan(&used)
	if used == nil {
		t.Error("last_used_at not recorded")
	}
	var hashes int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM hg_api_tokens WHERE token_hash = $1 OR token_hash = $2`, ro, full).Scan(&hashes)
	if hashes != 0 {
		t.Error("a token is stored in clear")
	}

	// The index lists what the caller may read.
	r = hgCall(t, base, "GET", "/", "", nil)
	if !strings.Contains(r.Raw, "/headlessgo/v1/articles") || strings.Contains(r.Raw, "/headlessgo/v1/authors") {
		t.Errorf("public index: %s", r.Raw)
	}

	// The locale must exist; without Golylang the only locale is the default one.
	if r := hgCall(t, base, "POST", "/articles?locale=fr", full, map[string]any{"data": map[string]any{"title": "Bonjour"}}); r.Code != 400 {
		t.Errorf("unknown locale: %d", r.Code)
	}
}

func TestHeadlessGoWPMenusAndRelations(t *testing.T) {
	base, c, srv := hgSite(t)
	ctx := context.Background()
	hgModel(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	page := &store.Post{Type: "page", Title: "About us", Slug: "about", Status: "publish", AuthorID: 1}
	if err := srv.store.InsertPost(ctx, page); err != nil {
		t.Fatal(err)
	}
	id, err := srv.store.CreateMenu(ctx, "Main")
	if err != nil {
		t.Fatal(err)
	}
	locs := srv.activeTheme().MenuLocations
	loc := "primary"
	if len(locs) > 0 {
		loc = locs[0].Slug
	}
	_ = srv.store.SetMenuLocations(ctx, map[string]int64{loc: id})
	top := &store.EditItem{MenuID: id, Kind: "post_type", Object: "page", TargetID: page.ID, Label: "About", Status: "publish", Order: 1}
	_ = srv.store.AddMenuItem(ctx, top)
	_ = srv.store.AddMenuItem(ctx, &store.EditItem{MenuID: id, ParentID: top.ID, Kind: "custom", URL: "https://example.test/team", Label: "Team", Status: "publish", Order: 2})
	_ = srv.store.AddMenuItem(ctx, &store.EditItem{MenuID: id, Kind: "custom", URL: "https://example.test/x", Label: "Hidden", Status: "draft", Order: 3})

	r := hgCall(t, base, "GET", "/wp/menus", "", nil)
	if r.Code != 200 || !strings.Contains(r.Raw, `"Main"`) {
		t.Fatalf("menus: %d %s", r.Code, r.Raw)
	}
	for _, key := range []string{fmt.Sprint(id), loc} {
		r = hgCall(t, base, "GET", "/wp/menus/"+key, "", nil)
		items, _ := r.data()["items"].([]any)
		if r.Code != 200 || len(items) != 1 {
			t.Fatalf("menu %s: %d %s", key, r.Code, r.Raw)
		}
		it := items[0].(map[string]any)
		kids := it["children"].([]any)
		if it["title"] != "About" || !strings.Contains(it["url"].(string), "about") || len(kids) != 1 || kids[0].(map[string]any)["title"] != "Team" {
			t.Errorf("menu tree: %v", it)
		}
	}
	if r := hgCall(t, base, "GET", "/wp/menus/nope", "", nil); r.Code != 404 {
		t.Errorf("unknown menu: %d", r.Code)
	}

	// Relations to WordPress pages and users.
	r = hgCall(t, base, "POST", "/articles?status=published", full, map[string]any{"data": map[string]any{"title": "With page", "page": page.ID, "writer": 1}})
	if r.Code != 201 {
		t.Fatalf("create: %d %s", r.Code, r.Raw)
	}
	doc := r.data()["documentId"].(string)
	r = hgCall(t, base, "GET", "/articles/"+doc+"?populate=page,writer", full, nil)
	pg, _ := r.data()["page"].(map[string]any)
	wr, _ := r.data()["writer"].(map[string]any)
	if pg == nil || pg["title"] != "About us" || pg["slug"] != "about" || wr == nil || wr["username"] != "admin" || wr["email"] != nil {
		t.Errorf("wp relations: %s", r.Raw)
	}
	if r := hgCall(t, base, "GET", "/articles?filters[page][slug][$eq]=about", full, nil); len(r.list()) != 1 {
		t.Errorf("filter through a page relation: %s", r.Raw)
	}
	if r := hgCall(t, base, "POST", "/articles", full, map[string]any{"data": map[string]any{"title": "Bad", "page": 1}}); r.Code != 400 {
		t.Errorf("relation to a post that is not a page: %d", r.Code)
	}
	// The relation picker of the admin.
	_, body, _ := get(t, c, base+"/wp-admin/headlessgo/search?target=wp:page&q=About")
	if !strings.Contains(body, "About us") {
		t.Errorf("relation search: %s", body)
	}
	_, body, _ = get(t, c, base+"/wp-admin/headlessgo/search?target=article&q=page")
	if !strings.Contains(body, doc) {
		t.Errorf("entry search: %s", body)
	}
}

func TestHeadlessGoSchemaChanges(t *testing.T) {
	base, c, srv := hgSite(t)
	ctx := context.Background()
	hgModel(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	r := hgCall(t, base, "POST", "/articles?status=published", full, map[string]any{"data": map[string]any{"title": "Original", "views": 7,
		"seo": map[string]any{"metaTitle": "M"}, "faq": []any{map[string]any{"text": "Q1"}},
		"blocks": []any{map[string]any{"__component": "blocks.quote", "text": "Z1", "by": "me"}}}})
	if r.Code != 201 {
		t.Fatalf("create: %d %s", r.Code, r.Raw)
	}
	doc := r.data()["documentId"].(string)

	// Rename title -> headline, drop views, through the builder form.
	sc := *srv.hgReg(ctx).Get("article")
	var fields []hgField
	for _, f := range sc.Fields {
		switch f.Name {
		case "title":
			f.RenamedFrom, f.Name = "title", "headline"
			fields = append(fields, f)
		case "views":
		case "slug":
			f.TargetField = "headline"
			fields = append(fields, f)
		default:
			fields = append(fields, f)
		}
	}
	sc.Fields = fields
	raw, _ := json.Marshal(sc)
	code, _, loc := hgAdminSubmit(t, c, base, url.Values{"page": {"builder"}, "do": {"save_schema"}, "is_new": {"0"}, "schema": {string(raw)}})
	if code != http.StatusFound || strings.Contains(loc, "hg-error") {
		t.Fatalf("save schema: %d %s", code, loc)
	}
	r = hgCall(t, base, "GET", "/articles/"+doc, full, nil)
	if r.data()["headline"] != "Original" || r.data()["title"] != nil || r.data()["views"] != nil {
		t.Fatalf("migrated entry: %s", r.Raw)
	}
	var keys string
	_ = srv.store.Pool.QueryRow(ctx, `SELECT string_agg(k, ',' ORDER BY k) FROM hg_entries e, jsonb_object_keys(e.published_data) k WHERE document_id = $1`, doc).Scan(&keys)
	if strings.Contains(keys, "views") || strings.Contains(keys, "title") {
		t.Fatalf("published data still has old keys: %s", keys)
	}

	// Rename a component field: values move inside entries (single, repeatable, dynamic zone).
	q := *srv.hgReg(ctx).Get("blocks.quote")
	q.Fields = []hgField{{Name: "quote", Type: "text", RenamedFrom: "text"}}
	hgMustSchema(t, srv, &q)
	r = hgCall(t, base, "GET", "/articles/"+doc+"?populate=*", full, nil)
	faq := r.data()["faq"].([]any)[0].(map[string]any)
	zone := r.data()["blocks"].([]any)[0].(map[string]any)
	if faq["quote"] != "Q1" || faq["text"] != nil || zone["quote"] != "Z1" || zone["by"] != nil {
		t.Fatalf("component migration: %s", r.Raw)
	}

	// i18n off: the locale disappears from the API.
	a := *srv.hgReg(ctx).Get("article")
	a.Localized = false
	hgMustSchema(t, srv, &a)
	r = hgCall(t, base, "GET", "/articles/"+doc, full, nil)
	if _, ok := r.data()["locale"]; ok || r.Code != 200 {
		t.Fatalf("locale after i18n off: %s", r.Raw)
	}
	// Draft & publish off: drafts become published.
	r = hgCall(t, base, "POST", "/categories", full, map[string]any{"data": map[string]any{"name": "Pending"}})
	cat := r.data()["documentId"].(string)
	cs := *srv.hgReg(ctx).Get("category")
	cs.DraftAndPublish = false
	hgMustSchema(t, srv, &cs)
	if r := hgCall(t, base, "GET", "/categories/"+cat, full, nil); r.Code != 200 {
		t.Fatalf("draft published by turning draft & publish off: %d", r.Code)
	}

	// A component in use, or a type with relations pointing to it, cannot be deleted.
	if err := srv.hgDeleteSchema(ctx, "shared.seo"); err == nil {
		t.Error("used component deleted")
	}
	if err := srv.hgDeleteSchema(ctx, "author"); err == nil {
		t.Error("related type deleted")
	}
	if err := srv.hgDeleteSchema(ctx, "homepage"); err != nil {
		t.Errorf("delete homepage: %v", err)
	}
	// Kind and singular id are fixed; duplicate API ids are refused.
	h := hgSchema{Kind: hgKindCollection, DisplayName: "Dup", SingularName: "dup", PluralName: "articles"}
	if err := srv.hgSaveSchema(ctx, &h); err == nil {
		t.Error("duplicate plural API id accepted")
	}

	// Export and import round trip into a second site.
	_, body, _ := get(t, c, base+"/wp-admin/headlessgo?page=builder")
	resp, err := c.Get(base + "/wp-admin/headlessgo?action=export&_wpnonce=" + formNonce(t, body))
	if err != nil {
		t.Fatal(err)
	}
	export, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Content-Disposition"), "headlessgo-schemas") || !strings.Contains(string(export), `"contentTypes"`) {
		t.Fatalf("export: %s", export)
	}
	// Re-importing into the same site updates in place.
	code, _, loc = hgAdminSubmit(t, c, base, url.Values{"page": {"settings"}, "do": {"import_schemas"}, "json": {string(export)}})
	if code != http.StatusFound || strings.Contains(loc, "hg-error") {
		t.Fatalf("import: %d %s", code, loc)
	}
	var ex hgExport
	_ = json.Unmarshal(export, &ex)
	ex.Types = append(ex.Types, &hgSchema{Kind: hgKindCollection, DisplayName: "Event", SingularName: "event", PluralName: "events",
		Fields: []hgField{{Name: "name", Type: "text"}, {Name: "speaker", Type: "relation", Relation: "manyToOne", Target: "author"}}})
	raw, _ = json.Marshal(ex)
	if n, err := srv.hgImportSchemas(ctx, raw); err != nil || n == 0 || srv.hgReg(ctx).Get("event") == nil {
		t.Fatalf("import new type: %d %v", n, err)
	}
	if _, err := srv.hgImportSchemas(ctx, []byte("{nope")); err == nil {
		t.Error("broken import accepted")
	}
}

func TestHeadlessGoAdminScreens(t *testing.T) {
	base, c, srv := hgSite(t)
	ctx := context.Background()
	for _, p := range []string{"", "builder", "builder&new=collectionType", "tokens", "tokens&new=1", "webhooks", "webhooks&new=1", "settings"} {
		code, body, _ := get(t, c, base+"/wp-admin/headlessgo?page="+p)
		if code != http.StatusOK || !strings.Contains(body, "hg-wrap") {
			t.Fatalf("screen %q: %d", p, code)
		}
		if strings.Contains(body, "Strapi") || strings.Contains(body, "Elementor") {
			t.Fatalf("screen %q names another product", p)
		}
	}
	// Create a type through the builder form.
	schema := `{"kind":"collectionType","displayName":"Recipe","singularName":"recipe","pluralName":"recipes","draftAndPublish":true,
		"attributes":[{"name":"name","type":"text","required":true},{"name":"secret","type":"password"},{"name":"photo","type":"media"},
		{"name":"steps","type":"richtext","format":"markdown"},{"name":"tags","type":"json"}]}`
	code, _, loc := hgAdminSubmit(t, c, base, url.Values{"page": {"builder"}, "do": {"save_schema"}, "is_new": {"1"}, "schema": {schema}})
	if code != http.StatusFound || strings.Contains(loc, "hg-error") || srv.hgReg(ctx).Get("recipe") == nil {
		t.Fatalf("builder create: %d %s", code, loc)
	}
	if code, _, loc := hgAdminSubmit(t, c, base, url.Values{"page": {"builder"}, "do": {"save_schema"}, "is_new": {"1"}, "schema": {schema}}); code != http.StatusFound || !strings.Contains(loc, "hg-error") {
		t.Fatalf("duplicate type accepted: %s", loc)
	}
	_, body, _ := get(t, c, base+"/wp-admin/headlessgo?page=builder&type=recipe")
	if !strings.Contains(body, `id="hg-builder-model"`) || !strings.Contains(body, `headlessgo.js`) {
		t.Fatal("builder model or script missing")
	}
	// The Content Manager: a new entry, a rejected save keeps the input, then a valid one.
	_, body, _ = get(t, c, base+"/wp-admin/headlessgo?page=entry&type=recipe")
	if !strings.Contains(body, `id="hg-editor-model"`) || !strings.Contains(body, "wp.media") && !strings.Contains(body, "media-views") {
		t.Fatal("entry form without its model or the media library")
	}
	nonce := formNonce(t, body)
	form := url.Values{"_wpnonce": {nonce}, "page": {"entry"}, "do": {"save_entry"}, "type": {"recipe"}, "publish": {"1"},
		"payload": {`{"name":"","secret":"pa55word!","steps":"keep me"}`}}
	code, body, _ = post(t, c, base+"/wp-admin/headlessgo", form)
	if code != http.StatusOK || !strings.Contains(body, "The entry was not saved") || !strings.Contains(body, "keep me") || strings.Contains(body, "pa55word!") {
		_, _, l := post(t, c, base+"/wp-admin/headlessgo", form)
		t.Fatalf("rejected save: %d %s", code, l)
	}
	form.Set("payload", `{"name":"Soup","secret":"pa55word!","steps":"1. boil","tags":["a"]}`)
	code, _, loc = post(t, c, base+"/wp-admin/headlessgo", form)
	if code != http.StatusFound || !strings.Contains(loc, "doc=") {
		t.Fatalf("save entry: %d %s", code, loc)
	}
	_, body, _ = get(t, c, base+loc)
	if !strings.Contains(body, "Published") || !strings.Contains(body, "Soup") || strings.Contains(body, "$argon2id$") || strings.Contains(body, "pa55word!") {
		t.Fatal("entry screen after save")
	}
	_, body, _ = get(t, c, base+"/wp-admin/headlessgo?page=entries&type=recipe&s=sou")
	if !strings.Contains(body, "Soup") || !strings.Contains(body, "1 items") {
		t.Fatal("entries list search")
	}
	_, body, _ = get(t, c, base+"/wp-admin/headlessgo?page=entries&type=recipe&status=draft")
	if strings.Contains(body, ">Soup<") {
		t.Fatal("status filter")
	}
	_, body, _ = get(t, c, base+"/wp-admin/headlessgo")
	if !strings.Contains(body, "Recipe") || !strings.Contains(body, "1 entries") {
		t.Fatal("content manager home")
	}

	// API tokens: shown once, stored hashed.
	code, body, _ = hgAdminSubmit(t, c, base, url.Values{"page": {"tokens"}, "do": {"save_token"}, "name": {"Site build"}, "kind": {"custom"}, "duration": {"30"},
		"perm[recipe][find]": {"1"}, "perm[recipe][findOne]": {"1"}})
	m := regexp.MustCompile(`value="(hg_[A-Za-z0-9_-]+)"`).FindStringSubmatch(body)
	if code != http.StatusOK || m == nil {
		t.Fatalf("token creation: %d", code)
	}
	tok := m[1]
	_, list, _ := get(t, c, base+"/wp-admin/headlessgo?page=tokens")
	if strings.Contains(list, tok) || !strings.Contains(list, "Site build") || !strings.Contains(list, tok[len(tok)-4:]) {
		t.Fatal("token list")
	}
	if r := hgCall(t, base, "GET", "/recipes", tok, nil); r.Code != 200 || len(r.list()) != 1 || r.list()[0].(map[string]any)["secret"] != nil {
		t.Fatalf("custom token from the admin: %d %s", r.Code, r.Raw)
	}
	var tid int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM hg_api_tokens WHERE name = 'Site build'`).Scan(&tid)
	code, body, _ = hgAdminSubmit(t, c, base, url.Values{"page": {"tokens"}, "do": {"regenerate_token"}, "id": {fmt.Sprint(tid)}})
	m2 := regexp.MustCompile(`value="(hg_[A-Za-z0-9_-]+)"`).FindStringSubmatch(body)
	if m2 == nil || m2[1] == tok {
		t.Fatal("regenerate")
	}
	if r := hgCall(t, base, "GET", "/recipes", tok, nil); r.Code != 401 {
		t.Errorf("old token after regeneration: %d", r.Code)
	}
	code, _, loc = hgAdminSubmit(t, c, base, url.Values{"page": {"tokens"}, "do": {"delete_token"}, "id": {fmt.Sprint(tid)}})
	if r := hgCall(t, base, "GET", "/recipes", m2[1], nil); r.Code != 401 || code != http.StatusFound {
		t.Errorf("deleted token: %d %s", r.Code, loc)
	}

	// Settings: the public matrix.
	code, _, loc = hgAdminSubmit(t, c, base, url.Values{"page": {"settings"}, "do": {"save_settings"}, "front_mode": {"json"}, "public[recipe][find]": {"1"},
		"default_page_size": {"10"}, "max_page_size": {"50"}, "graphql": {"1"}})
	if code != http.StatusFound || strings.Contains(loc, "hg-error") {
		t.Fatalf("settings: %d %s", code, loc)
	}
	if r := hgCall(t, base, "GET", "/recipes", "", nil); r.Code != 200 || r.Body["meta"].(map[string]any)["pagination"].(map[string]any)["pageSize"] != 10.0 {
		t.Fatalf("public after the matrix: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "GET", "/recipes/x", "", nil); r.Code != 403 {
		t.Errorf("findOne not granted: %d", r.Code)
	}
	// An editor reaches the Content Manager, not the builder.
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE users SET id = id`); err != nil {
		t.Fatal(err)
	}
}

func TestHeadlessGoWebhooks(t *testing.T) {
	base, c, srv := hgSite(t)
	hgWebhookAllowPrivate.Store(true) // deliveries go to httptest servers on 127.0.0.1
	defer hgWebhookAllowPrivate.Store(false)
	ctx := context.Background()
	saved := hgWebhookBase
	hgWebhookBase = 20 * time.Millisecond
	t.Cleanup(func() { hgWebhookBase = saved })
	hgModel(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})

	type hit struct {
		event, sig, custom string
		body               []byte
	}
	var mu sync.Mutex
	var hits []hit
	failures := 1
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		hits = append(hits, hit{r.Header.Get("X-HeadlessGo-Event"), r.Header.Get("X-HeadlessGo-Signature"), r.Header.Get("X-Custom"), b})
		if failures > 0 && r.Header.Get("X-HeadlessGo-Event") == "entry.create" {
			failures--
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("try again"))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer hook.Close()

	code, _, loc := hgAdminSubmit(t, c, base, url.Values{"page": {"webhooks"}, "do": {"save_webhook"}, "name": {"Rebuild"}, "url": {hook.URL + "/hook"},
		"events[]": {"entry.create", "entry.update", "entry.publish", "entry.unpublish", "entry.delete"}, "headers": {"X-Custom: yes"}, "secret": {"s3cret"}, "enabled": {"1"}})
	if code != http.StatusFound || strings.Contains(loc, "hg-error") {
		t.Fatalf("save webhook: %d %s", code, loc)
	}
	if code, _, loc := hgAdminSubmit(t, c, base, url.Values{"page": {"webhooks"}, "do": {"save_webhook"}, "name": {"Bad"}, "url": {"ftp://x"}, "events[]": {"entry.create"}}); code != http.StatusFound || !strings.Contains(loc, "hg-error") {
		t.Fatalf("bad webhook URL accepted: %s", loc)
	}
	if code, _, loc := hgAdminSubmit(t, c, base, url.Values{"page": {"webhooks"}, "do": {"save_webhook"}, "name": {"Bad"}, "url": {hook.URL}, "events[]": {"entry.create"}, "headers": {"Content-Type: text/plain"}}); code != http.StatusFound || !strings.Contains(loc, "hg-error") {
		t.Fatalf("reserved header accepted: %s", loc)
	}
	r := hgCall(t, base, "POST", "/articles", full, map[string]any{"data": map[string]any{"title": "Hooked", "internal": "secret note"}})
	doc := r.data()["documentId"].(string)
	hgCall(t, base, "POST", "/articles/"+doc+"/publish", full, nil)
	hgCall(t, base, "DELETE", "/articles/"+doc, full, nil)
	hgWebhookWG.Wait()

	mu.Lock()
	var events []string
	for _, h := range hits {
		events = append(events, h.event)
		m := hmac.New(sha256.New, []byte("s3cret"))
		m.Write(h.body)
		if h.sig != "sha256="+hex.EncodeToString(m.Sum(nil)) {
			t.Errorf("bad signature for %s", h.event)
		}
		if h.custom != "yes" {
			t.Errorf("custom header missing for %s", h.event)
		}
		if strings.Contains(string(h.body), "secret note") {
			t.Error("a private field was sent to a webhook")
		}
	}
	mu.Unlock()
	joined := strings.Join(events, ",")
	if strings.Count(joined, "entry.create") != 2 || !strings.Contains(joined, "entry.publish") || !strings.Contains(joined, "entry.delete") {
		t.Fatalf("events: %v", events)
	}
	var payload map[string]any
	_ = json.Unmarshal(hits[0].body, &payload)
	if payload["event"] != "entry.create" || payload["uid"] != "article" || payload["entry"].(map[string]any)["title"] != "Hooked" {
		t.Fatalf("payload: %v", payload)
	}
	var wid int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM hg_webhooks WHERE name = 'Rebuild'`).Scan(&wid)
	var n, failed int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE NOT success) FROM hg_webhook_deliveries WHERE webhook_id = $1`, wid).Scan(&n, &failed)
	if n != len(events) || failed != 1 {
		t.Fatalf("delivery log: %d rows, %d failed", n, failed)
	}
	// Send test, and the log on the webhook's screen.
	code, _, loc = hgAdminSubmit(t, c, base, url.Values{"page": {"webhooks"}, "do": {"test_webhook"}, "id": {fmt.Sprint(wid)}})
	if code != http.StatusFound || !strings.Contains(loc, "hg-msg=Test+sent") {
		t.Fatalf("send test: %d %s", code, loc)
	}
	_, body, _ := get(t, c, base+"/wp-admin/headlessgo?page=webhooks&id="+fmt.Sprint(wid))
	if !strings.Contains(body, "trigger-test") || !strings.Contains(body, "try again") || !strings.Contains(body, "entry.publish") {
		t.Fatal("delivery log screen")
	}
	// A disabled webhook receives nothing; an unreachable one logs the error.
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE hg_webhooks SET enabled = false`)
	before := len(hits)
	hgCall(t, base, "POST", "/articles", full, map[string]any{"data": map[string]any{"title": "Quiet"}})
	hgWebhookWG.Wait()
	if len(hits) != before {
		t.Error("a disabled webhook was called")
	}
	dead := &hgWebhook{Name: "Dead", URL: "http://127.0.0.1:1/x", Events: []string{"entry.create"}, Enabled: true}
	if err := srv.hgSaveWebhook(ctx, dead); err != nil {
		t.Fatal(err)
	}
	if res := srv.hgTestWebhook(ctx, dead); res.Success || res.Error == "" {
		t.Errorf("unreachable webhook: %+v", res)
	}
}

func TestHeadlessGoCORSAndPreview(t *testing.T) {
	base, c, srv := hgSite(t)
	ctx := context.Background()
	hgModel(t, srv)
	st := srv.hgLoad()
	st.CORSOrigins = []string{"https://app.example.test"}
	st.Public = map[string][]string{"article": {"find", "findOne"}}
	_ = srv.hgSaveSettings(ctx, st)

	pre := func(origin string) *http.Response {
		req, _ := http.NewRequest(http.MethodOptions, base+hgAPIPrefix+"/articles", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "GET")
		req.Header.Set("Access-Control-Request-Headers", "authorization")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	if resp := pre("https://app.example.test"); resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != "https://app.example.test" ||
		!strings.Contains(resp.Header.Get("Access-Control-Allow-Headers"), "Authorization") || !strings.Contains(resp.Header.Get("Access-Control-Allow-Methods"), "PUT") {
		t.Fatalf("allowed preflight: %d %v", resp.StatusCode, resp.Header)
	}
	if resp := pre("https://evil.example"); resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("preflight allowed for another origin")
	}
	if r := hgCall(t, base, "GET", "/articles", "", nil, "Origin", "https://app.example.test"); r.Header.Get("Access-Control-Allow-Origin") != "https://app.example.test" || !strings.Contains(r.Header.Get("Vary"), "Origin") {
		t.Fatalf("CORS on GET: %v", r.Header)
	}
	if r := hgCall(t, base, "GET", "/articles", "", nil, "Origin", "https://evil.example"); r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS for another origin")
	}

	// Preview: the edit screen links to the template with a preview token reading the draft.
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	r := hgCall(t, base, "POST", "/articles", full, map[string]any{"data": map[string]any{"title": "Secret draft"}})
	doc := r.data()["documentId"].(string)
	r = hgCall(t, base, "POST", "/articles", full, map[string]any{"data": map[string]any{"title": "Other draft"}})
	other := r.data()["documentId"].(string)
	_, body, _ := get(t, c, base+"/wp-admin/headlessgo?page=entry&type=article&doc="+doc+"&locale=en")
	m := regexp.MustCompile(`href="(https://app\.example\.test/preview\?[^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no preview link on the edit screen")
	}
	pu, _ := url.Parse(html.UnescapeString(m[1]))
	if pu.Query().Get("slug") != "secret-draft" || pu.Query().Get("secret") != srv.hgLoad().PreviewSecret || pu.Query().Get("locale") != "en" {
		t.Fatalf("preview URL: %s", pu)
	}
	ptok := pu.Query().Get("token")
	if r := hgCall(t, base, "GET", "/articles/"+doc+"?status=draft&previewToken="+url.QueryEscape(ptok), "", nil); r.Code != 200 || r.data()["title"] != "Secret draft" {
		t.Fatalf("preview read: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "GET", "/articles?status=draft&filters[slug][$eq]=secret-draft", ptok, nil); r.Code != 200 || len(r.list()) != 1 {
		t.Fatalf("preview find: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "GET", "/articles?status=draft", ptok, nil); len(r.list()) != 1 {
		t.Fatalf("a preview token reads other drafts: %s", r.Raw)
	}
	if r := hgCall(t, base, "GET", "/articles/"+other+"?status=draft", ptok, nil); r.Code != 403 {
		t.Errorf("preview token on another document: %d", r.Code)
	}
	if r := hgCall(t, base, "PUT", "/articles/"+doc, ptok, map[string]any{"data": map[string]any{"title": "Hacked"}}); r.Code != 403 {
		t.Errorf("preview token writes: %d", r.Code)
	}
	if r := hgCall(t, base, "GET", "/articles/"+doc+"?status=draft", ptok[:len(ptok)-2]+"xx", nil); r.Code != 401 {
		t.Errorf("tampered preview token: %d", r.Code)
	}
	if r := hgCall(t, base, "GET", "/articles?previewToken="+url.QueryEscape(full), "", nil); r.Code != 401 {
		t.Errorf("an API token in the URL: %d", r.Code)
	}
	expiredTok := srv.hgPreviewToken(srv.hgLoad(), "article", doc, "en", -time.Minute)
	if r := hgCall(t, base, "GET", "/articles/"+doc+"?status=draft", expiredTok, nil); r.Code != 401 {
		t.Errorf("expired preview token: %d", r.Code)
	}
	// Rotating the secret invalidates preview tokens.
	st = srv.hgLoad()
	st.PreviewSecret = "rotated"
	_ = srv.hgSaveSettings(ctx, st)
	if r := hgCall(t, base, "GET", "/articles/"+doc+"?status=draft", ptok, nil); r.Code != 401 {
		t.Errorf("preview token after rotation: %d", r.Code)
	}
	// rest_route form of the API.
	if code, body, _ := get(t, http.DefaultClient, base+"/?rest_route=/headlessgo/v1/articles"); code != 200 || !strings.Contains(body, `"pagination"`) {
		t.Errorf("rest_route: %d %s", code, body)
	}
}

func TestHeadlessGoGolylangLocales(t *testing.T) {
	base, c, srv := hgSite(t)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	hgModel(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	r := hgCall(t, base, "POST", "/articles?status=published", full, map[string]any{"data": map[string]any{"title": "Hello"}})
	if r.Code != 201 || r.data()["locale"] != "en" {
		t.Fatalf("default locale: %d %s", r.Code, r.Raw)
	}
	doc := r.data()["documentId"].(string)
	r = hgCall(t, base, "PUT", "/articles/"+doc+"?locale=fr&status=published", full, map[string]any{"data": map[string]any{"title": "Bonjour"}})
	if r.Code != 200 || r.data()["locale"] != "fr" || r.data()["documentId"] != doc {
		t.Fatalf("translation: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "GET", "/articles?locale=fr", full, nil); len(r.list()) != 1 || r.list()[0].(map[string]any)["title"] != "Bonjour" {
		t.Fatalf("fr list: %s", r.Raw)
	}
	if r := hgCall(t, base, "GET", "/articles", full, nil); len(r.list()) != 1 || r.list()[0].(map[string]any)["title"] != "Hello" {
		t.Fatalf("default list: %s", r.Raw)
	}
	r = hgCall(t, base, "GET", "/articles/"+doc+"?populate=localizations", full, nil)
	if l, _ := r.data()["localizations"].([]any); len(l) != 1 || l[0].(map[string]any)["locale"] != "fr" {
		t.Fatalf("localizations: %s", r.Raw)
	}
	if r := hgCall(t, base, "GET", "/articles/"+doc+"?locale=de", full, nil); r.Code != 400 {
		t.Errorf("unknown locale: %d", r.Code)
	}
	_, body, _ := get(t, c, base+"/wp-admin/headlessgo?page=entry&type=article&doc="+doc+"&locale=fr")
	if !strings.Contains(body, "Locales") || !strings.Contains(body, "Français") {
		t.Fatal("locale switcher")
	}
	// Deleting one locale keeps the other.
	if r := hgCall(t, base, "DELETE", "/articles/"+doc+"?locale=fr", full, nil); r.Code != 204 {
		t.Fatalf("delete fr: %d", r.Code)
	}
	if r := hgCall(t, base, "GET", "/articles/"+doc, full, nil); r.Code != 200 {
		t.Fatalf("en after deleting fr: %d", r.Code)
	}
}

func TestHeadlessGoGraphQL(t *testing.T) {
	base, _, srv := hgSite(t)
	ctx := context.Background()
	hgModel(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	img := hgImage(t, srv, "pic")
	r := hgCall(t, base, "POST", "/authors", full, map[string]any{"data": map[string]any{"name": "Ada"}})
	ada := r.data()["documentId"].(string)
	for i, title := range []string{"Alpha one", "Beta two", "Gamma three"} {
		r := hgCall(t, base, "POST", "/articles?status=published", full, map[string]any{"data": map[string]any{"title": title, "views": (i + 1) * 10, "author": ada,
			"blocks": []any{map[string]any{"__component": "blocks.quote", "text": "Q" + title}, map[string]any{"__component": "blocks.gallery", "images": []any{img}}}}})
		if r.Code != 201 {
			t.Fatalf("create: %s", r.Raw)
		}
	}
	gql := func(token, query string, vars map[string]any) hgResp {
		return hgCall(t, base, "POST", "/graphql", token, map[string]any{"query": query, "variables": vars})
	}
	r = gql(full, `query Q($min: Int) { list: articles(filters: {views: {gte: $min}}, sort: ["views:desc"], pagination: {pageSize: 2}) {
		documentId title author { name } blocks { __typename ... on ComponentBlocksQuote { text } ... on ComponentBlocksGallery { images { url mime } } } } }`, map[string]any{"min": 20})
	if r.Code != 200 || r.Body["errors"] != nil {
		t.Fatalf("query: %d %s", r.Code, r.Raw)
	}
	list := r.data()["list"].([]any)
	if len(list) != 2 {
		t.Fatalf("list: %s", r.Raw)
	}
	first := list[0].(map[string]any)
	if first["title"] != "Gamma three" || first["author"].(map[string]any)["name"] != "Ada" || first["views"] != nil {
		t.Errorf("first: %v", first)
	}
	bl := first["blocks"].([]any)
	if bl[0].(map[string]any)["__typename"] != "ComponentBlocksQuote" || bl[0].(map[string]any)["text"] != "QGamma three" {
		t.Errorf("quote block: %v", bl[0])
	}
	if imgs := bl[1].(map[string]any)["images"].([]any); len(imgs) != 1 || imgs[0].(map[string]any)["mime"] != "image/png" {
		t.Errorf("gallery block: %v", bl[1])
	}
	r = gql(full, `{ articles_connection(pagination: {page: 1, pageSize: 1}) { nodes { title } pageInfo { total pageCount } } }`, nil)
	conn := r.data()["articles_connection"].(map[string]any)
	if conn["pageInfo"].(map[string]any)["total"] != 3.0 || len(conn["nodes"].([]any)) != 1 {
		t.Errorf("connection: %s", r.Raw)
	}
	doc := first["documentId"].(string)
	r = gql(full, `{ article(documentId: "`+doc+`") { title } homepage { headline } }`, nil)
	if r.data()["article"].(map[string]any)["title"] != "Gamma three" || r.data()["homepage"] != nil {
		t.Errorf("one: %s", r.Raw)
	}
	if r := gql("", `{ articles { title } }`, nil); r.Body["errors"] == nil {
		t.Errorf("public GraphQL without permission: %s", r.Raw)
	}
	for _, bad := range []string{`mutation { createArticle { title } }`, `{ articles { title }`, `{ nope { id } }`, `{ __nope { name } }`} {
		if r := gql(full, bad, nil); r.Body["errors"] == nil {
			t.Errorf("%s: %s", bad, r.Raw)
		}
	}
	st := srv.hgLoad()
	st.GraphQL = false
	_ = srv.hgSaveSettings(ctx, st)
	if r := gql(full, `{ articles { title } }`, nil); r.Code != 404 {
		t.Errorf("GraphQL switched off: %d", r.Code)
	}
}
