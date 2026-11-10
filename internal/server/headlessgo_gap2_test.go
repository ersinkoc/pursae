package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"wpgo/internal/auth"
	"wpgo/internal/plugins"
	"wpgo/internal/store"
)

func hgNote(t *testing.T, srv *Server) {
	t.Helper()
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Note", SingularName: "note", PluralName: "notes", DraftAndPublish: true,
		Fields: []hgField{{Name: "title", Type: "text", Required: true}, {Name: "body", Type: "text", Long: true}, {Name: "secret", Type: "text", Private: true},
			{Name: "readMinutes", Type: "number", Format: "integer"}}})
}

func TestHeadlessGoFullTextSearch(t *testing.T) {
	base, _, srv := hgSite(t)
	ctx := context.Background()
	hgNote(t, srv)
	st := srv.hgLoad()
	st.Public = map[string][]string{"note": {"find"}}
	_ = srv.hgSaveSettings(ctx, st)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	for _, n := range []map[string]any{
		{"title": "Gopher news", "body": "alpha", "secret": "hidden-needle"},
		{"title": "Other", "body": "has a GOPHER inside"},
		{"title": "Unrelated", "body": "nothing"},
	} {
		hgCall(t, base, "POST", "/notes?status=published", full, map[string]any{"data": n})
	}
	r := hgCall(t, base, "GET", "/notes?_q=gopher&sort=title", "", nil)
	if got := hgTitles(r.list(), "title"); len(got) != 2 || got[0] != "Gopher news" || got[1] != "Other" {
		t.Errorf("_q=gopher: %v %s", got, r.Raw)
	}
	if r := hgCall(t, base, "GET", "/notes?_q=hidden-needle", "", nil); len(r.list()) != 0 {
		t.Errorf("_q searched a private attribute: %s", r.Raw)
	}
	r = hgCall(t, base, "GET", "/notes?_q=gopher&filters[title][$eq]=Other", "", nil)
	if got := hgTitles(r.list(), "title"); len(got) != 1 || got[0] != "Other" {
		t.Errorf("_q with filters: %v", got)
	}
	if r := hgCall(t, base, "GET", "/notes?_q=", "", nil); len(r.list()) != 3 {
		t.Errorf("blank _q must not filter: %s", r.Raw)
	}
	if r := hgCall(t, base, "GET", "/notes?_q[a]=b", "", nil); r.Code != 400 {
		t.Errorf("_q as an object: %d", r.Code)
	}
}

func TestHeadlessGoOpenAPIAndTypeScript(t *testing.T) {
	base, _, srv := hgSite(t)
	ctx := context.Background()
	hgNote(t, srv)
	hgMemo(t, srv, false)
	// Nothing is public: the document has no paths.
	r := hgCall(t, base, "GET", "/openapi.json", "", nil)
	if r.Code != 200 || strings.Contains(r.Raw, "/notes") {
		t.Fatalf("public openapi leaks types: %d %s", r.Code, r.Raw)
	}
	st := srv.hgLoad()
	st.Public = map[string][]string{"note": {"find", "findOne"}}
	_ = srv.hgSaveSettings(ctx, st)
	r = hgCall(t, base, "GET", "/openapi.json", "", nil)
	paths, _ := r.Body["paths"].(map[string]any)
	if paths["/notes"] == nil || paths["/notes/{documentId}"] == nil || paths["/memos"] != nil {
		t.Fatalf("paths: %v", paths)
	}
	if get := paths["/notes"].(map[string]any); get["post"] != nil || get["get"] == nil {
		t.Errorf("a public read-only type offers writes: %v", get)
	}
	schemas := r.Body["components"].(map[string]any)["schemas"].(map[string]any)
	note := schemas["Note"].(map[string]any)["properties"].(map[string]any)
	if note["title"] == nil || note["secret"] != nil || note["documentId"] == nil {
		t.Errorf("Note schema: %v", note)
	}
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	r = hgCall(t, base, "GET", "/openapi.json", full, nil)
	paths, _ = r.Body["paths"].(map[string]any)
	if paths["/memos"] == nil || paths["/notes"].(map[string]any)["post"] == nil {
		t.Errorf("a full-access token must see every type and write: %v", paths)
	}
	req, _ := http.NewRequest("GET", base+hgAPIPrefix+"/typescript", nil)
	req.Header.Set("Authorization", "Bearer "+full)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	ts := buf.String()
	if resp.StatusCode != 200 || !strings.Contains(ts, "export interface Note {") || !strings.Contains(ts, "title?: string;") ||
		!strings.Contains(ts, "export interface NoteInput {") || !strings.Contains(ts, "  title: string;") || strings.Contains(ts, "secret") {
		t.Errorf("typescript: %d\n%s", resp.StatusCode, ts)
	}
}

func TestHGTypeScriptRendering(t *testing.T) {
	reg := &hgRegistry{all: map[string]*hgSchema{}}
	cases := map[string]string{
		hgTSType(hgFieldSchema(reg, &hgField{Name: "a", Type: "enumeration", Enum: []string{"x", "y"}, Required: true}, false)): `"x" | "y"`,
		hgTSType(hgFieldSchema(reg, &hgField{Name: "a", Type: "number", Format: "integer"}, false)):                             "number | null",
		hgTSType(hgFieldSchema(reg, &hgField{Name: "a", Type: "media", Multiple: true}, true)):                                  "number[]",
		hgTSType(hgFieldSchema(reg, &hgField{Name: "a", Type: "boolean", Required: true}, false)):                               "boolean",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}

func TestHeadlessGoEntryExportImport(t *testing.T) {
	_, _, srv := hgSite(t)
	ctx := context.Background()
	hgNote(t, srv)
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Tag", SingularName: "tag", PluralName: "tags", DraftAndPublish: true,
		Fields: []hgField{{Name: "label", Type: "text"}}})
	save := func(sc string, in map[string]any, pub bool) string {
		e, _, err := srv.hgSaveEntry(ctx, srv.hgReg(ctx).Get(sc), "", in, hgSaveOpts{Publish: pub})
		if err != nil {
			t.Fatal(err)
		}
		return e.DocumentID
	}
	pubDoc := save("note", map[string]any{"title": "Live", "secret": "s3"}, true)
	draftDoc := save("note", map[string]any{"title": "Draft only"}, false)
	save("tag", map[string]any{"label": "t1"}, true)

	var out bytes.Buffer
	n, err := srv.hgExportEntries(ctx, &out, []string{"note"})
	if err != nil || n != 2 {
		t.Fatalf("export: %d %v", n, err)
	}
	if strings.Contains(out.String(), `"label"`) {
		t.Error("export of one type contains another type")
	}
	if _, err := srv.hgExportEntries(ctx, &bytes.Buffer{}, []string{"nope"}); err == nil {
		t.Error("export of an unknown type succeeded")
	}
	dump := out.String()

	// Restore into a site where the entries are gone.
	if _, err := srv.store.Pool.Exec(ctx, `DELETE FROM hg_entries WHERE type_uid = 'note'`); err != nil {
		t.Fatal(err)
	}
	res, err := srv.hgImportEntries(ctx, strings.NewReader(dump), false, 7)
	if err != nil || res.Created != 2 || res.Skipped != 0 {
		t.Fatalf("import: %+v %v", res, err)
	}
	pe, err := srv.hgGetEntry(ctx, "note", pubDoc, "")
	if err != nil || pe.Published == nil || pe.Data["title"] != "Live" || pe.Data["secret"] != "s3" || pe.CreatedBy != 7 {
		t.Fatalf("published entry after import: %+v %v", pe, err)
	}
	de, err := srv.hgGetEntry(ctx, "note", draftDoc, "")
	if err != nil || de.Published != nil {
		t.Fatalf("draft entry after import: %+v %v", de, err)
	}
	// Running it again skips; with overwrite it replaces edits.
	res, _ = srv.hgImportEntries(ctx, strings.NewReader(dump), false, 7)
	if res.Created != 0 || res.Skipped != 2 {
		t.Errorf("second import: %+v", res)
	}
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE hg_entries SET data = jsonb_set(data, '{title}', '"Edited"') WHERE document_id = $1`, pubDoc)
	res, _ = srv.hgImportEntries(ctx, strings.NewReader(dump), true, 7)
	if res.Updated != 2 {
		t.Errorf("overwrite import: %+v", res)
	}
	if pe, _ := srv.hgGetEntry(ctx, "note", pubDoc, ""); pe.Data["title"] != "Live" {
		t.Errorf("overwrite kept the edit: %v", pe.Data)
	}
	// Bad input.
	for name, in := range map[string]string{
		"no header":    `{"type":"note","documentId":"x","data":{}}`,
		"bad json":     "{\"pursae\":\"headless-entries\"}\nnot json",
		"unknown type": "{\"pursae\":\"headless-entries\"}\n{\"type\":\"nope\",\"documentId\":\"x\",\"data\":{}}",
		"no data":      "{\"pursae\":\"headless-entries\"}\n{\"type\":\"note\",\"documentId\":\"x\"}",
		"wrong format": `{"pursae":"something-else"}`,
	} {
		if _, err := srv.hgImportEntries(ctx, strings.NewReader(in), false, 0); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// Unknown attributes and missing media are dropped with warnings.
	in := "{\"pursae\":\"headless-entries\"}\n" + `{"type":"note","documentId":"warn1","data":{"title":"W","ghost":1}}` + "\n"
	res, err = srv.hgImportEntries(ctx, strings.NewReader(in), false, 0)
	if err != nil || res.Created != 1 || len(res.Warnings) != 1 {
		t.Errorf("unknown attribute: %+v %v", res, err)
	}
	// The CLI goes through the same code.
	var cli bytes.Buffer
	if err := HeadlessCLI(ctx, srv.store.Pool, []string{"export", "--type", "tag"}, nil, &cli); err != nil || !strings.Contains(cli.String(), `"label":"t1"`) {
		t.Errorf("cli export: %v %s", err, cli.String())
	}
	var cliOut bytes.Buffer
	if err := HeadlessCLI(ctx, srv.store.Pool, []string{"import"}, strings.NewReader(cli.String()), &cliOut); err != nil || !strings.Contains(cliOut.String(), "1 skipped") {
		t.Errorf("cli import: %v %s", err, cliOut.String())
	}
}

func TestHeadlessGoEntryHooks(t *testing.T) {
	base, _, srv := hgSite(t)
	hgNote(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	if srv.plugins == nil {
		t.Skip("no plugin registry in this test server")
	}
	if plugins.HookPermission("headless_entry_validate") != plugins.PermHeadless {
		t.Fatal("headless hooks need the headless:entries permission")
	}
	hooks := srv.plugins.hooks
	var events []string
	hooks.AddFilter(hgHookValidate, 10, func(_ context.Context, v any, _ ...any) any {
		m := v.(map[string]any)
		data := m["data"].(map[string]any)
		if _, leaked := data["secret"]; leaked {
			t.Error("a private attribute reached the validate hook")
		}
		if data["title"] == "forbidden" {
			m["errors"] = []any{map[string]any{"field": "title", "message": "That title is not allowed"}}
			return m
		}
		if body, _ := data["body"].(string); body != "" {
			data["readMinutes"] = float64(len(strings.Fields(body))) // derived value
		}
		if data["title"] == "bad-derived" {
			data["readMinutes"] = "not a number"
		}
		return m
	})
	for _, h := range []string{"headless_entry_saved", "headless_entry_published", "headless_entry_unpublished", "headless_entry_deleted"} {
		h := h
		hooks.AddAction(h, 10, func(_ context.Context, args ...any) {
			p := args[0].(map[string]any)
			if _, leaked := p["entry"].(map[string]any)["secret"]; leaked {
				t.Error("a private attribute reached an action hook")
			}
			events = append(events, h+":"+p["type"].(string))
		})
	}
	r := hgCall(t, base, "POST", "/notes", full, map[string]any{"data": map[string]any{"title": "forbidden", "secret": "x"}})
	if r.Code != 400 || !strings.Contains(r.Raw, "That title is not allowed") {
		t.Fatalf("rejected by plugin: %d %s", r.Code, r.Raw)
	}
	if len(events) != 0 {
		t.Errorf("a rejected save fired %v", events)
	}
	r = hgCall(t, base, "POST", "/notes", full, map[string]any{"data": map[string]any{"title": "ok", "body": "one two three", "secret": "x"}})
	if r.Code != 201 && r.Code != 200 {
		t.Fatalf("create: %d %s", r.Code, r.Raw)
	}
	if got := r.data()["readMinutes"]; got != float64(3) {
		t.Errorf("derived value not stored: %v (%s)", got, r.Raw)
	}
	doc := hgDocOf(t, r)
	if r := hgCall(t, base, "POST", "/notes", full, map[string]any{"data": map[string]any{"title": "bad-derived"}}); r.Code != 400 {
		t.Errorf("an invalid derived value must be rejected: %d %s", r.Code, r.Raw)
	}
	hgCall(t, base, "POST", "/notes/"+doc+"/publish", full, nil)
	hgCall(t, base, "POST", "/notes/"+doc+"/unpublish", full, nil)
	hgCall(t, base, "DELETE", "/notes/"+doc, full, nil)
	want := "headless_entry_saved:note,headless_entry_published:note,headless_entry_unpublished:note,headless_entry_deleted:note"
	if got := strings.Join(events, ","); got != want {
		t.Errorf("events:\n got %s\nwant %s", got, want)
	}
}

func TestHeadlessGoGraphQLWordPressContent(t *testing.T) {
	base, _, srv := hgSite(t)
	ctx := context.Background()
	hgNote(t, srv)
	cat := &store.Term{Taxonomy: "category", Slug: "news", Name: "News"}
	if err := srv.store.InsertTerm(ctx, cat); err != nil {
		t.Fatal(err)
	}
	mk := func(typ, title, slug, status, pass string) *store.Post {
		p := &store.Post{Type: typ, Title: title, Slug: slug, Status: status, AuthorID: 1, Content: "<!-- wp:paragraph --><p>Body of " + title + "</p><!-- /wp:paragraph -->", Password: pass}
		if err := srv.store.InsertPost(ctx, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	live := mk("post", "Live post", "live-post", "publish", "")
	mk("post", "Draft post", "draft-post", "draft", "")
	mk("post", "Locked post", "locked-post", "publish", "pw")
	mk("page", "About", "about", "publish", "")
	_ = srv.store.SetPostTerms(ctx, live.ID, "category", []int64{cat.ID})
	gql := func(q string) hgResp {
		return hgCall(t, base, "POST", "/graphql", "", map[string]any{"query": q})
	}
	r := gql(`{ wpPosts(search: "Live") { slug title content author { name } categories { slug name } } }`)
	data, _ := r.Body["data"].(map[string]any)
	posts, _ := data["wpPosts"].([]any)
	if r.Code != 200 || len(posts) != 1 {
		t.Fatalf("wpPosts: %d %s", r.Code, r.Raw)
	}
	p := posts[0].(map[string]any)
	if p["slug"] != "live-post" || !strings.Contains(p["content"].(string), "Body of Live post") || len(p["categories"].([]any)) != 1 {
		t.Errorf("post: %v", p)
	}
	if r := gql(`{ wpPost(slug: "draft-post") { title } }`); strings.Contains(r.Raw, "Draft post") {
		t.Errorf("a draft is readable: %s", r.Raw)
	}
	if r := gql(`{ wpPost(slug: "locked-post") { title } }`); strings.Contains(r.Raw, "Locked post") {
		t.Errorf("a password-protected post is readable: %s", r.Raw)
	}
	if r := gql(`{ wpPost(slug: "live-post") { title __typename } }`); !strings.Contains(r.Raw, "Live post") || !strings.Contains(r.Raw, "WpContentNode") {
		t.Errorf("wpPost: %s", r.Raw)
	}
	if r := gql(`{ wpPages { title } }`); !strings.Contains(r.Raw, "About") || strings.Contains(r.Raw, "Live post") {
		t.Errorf("wpPages: %s", r.Raw)
	}
	if r := gql(`{ wpPosts(category: "news") { slug } }`); !strings.Contains(r.Raw, "live-post") {
		t.Errorf("category filter: %s", r.Raw)
	}
	if r := gql(`{ wpPosts(category: "missing") { slug } }`); strings.Contains(r.Raw, "live-post") {
		t.Errorf("unknown category must match nothing: %s", r.Raw)
	}
	if r := gql(`{ wpPosts(search: "Live") { slug } }`); !strings.Contains(r.Raw, "live-post") {
		t.Errorf("search: %s", r.Raw)
	}
	if r := gql(`{ wpPosts_connection(pageSize: 1, search: "Live") { nodes { slug } pageInfo { total pageCount } } }`); !strings.Contains(r.Raw, `"total":1`) {
		t.Errorf("connection: %s", r.Raw)
	}
	if r := gql(`{ wpCategories { slug count } wpTags { slug } }`); !strings.Contains(r.Raw, `"news"`) {
		t.Errorf("terms: %s", r.Raw)
	}
	if r := gql(`{ wpUsers { id username } }`); r.Code != 200 || !strings.Contains(r.Raw, "username") {
		t.Errorf("wpUsers: %d %s", r.Code, r.Raw)
	}
	id, _ := srv.store.CreateMenu(ctx, "Main")
	_ = srv.store.AddMenuItem(ctx, &store.EditItem{MenuID: id, Kind: "custom", URL: "https://example.test/x", Label: "Shown", Status: "publish", Order: 1})
	_ = srv.store.AddMenuItem(ctx, &store.EditItem{MenuID: id, Kind: "custom", URL: "https://example.test/y", Label: "Hidden", Status: "draft", Order: 2})
	if r := gql(`{ wpMenus { name items { title } } wpMenu(slug: "main") { name items { title url children { title } } } }`); !strings.Contains(r.Raw, "Shown") || strings.Contains(r.Raw, "Hidden") {
		t.Errorf("menus: %s", r.Raw)
	}
	if r := gql(`{ wpMediaItems { id } wpMediaItem(id: 999) { id } }`); r.Code != 200 || strings.Contains(r.Raw, "errors") {
		t.Errorf("media: %d %s", r.Code, r.Raw)
	}
	if r := gql(`{ wpPosts(orderBy: "bogus") { slug } }`); !strings.Contains(r.Raw, "orderBy") {
		t.Errorf("orderBy validation: %s", r.Raw)
	}
	if r := gql(`{ __type(name: "WpContentNode") { fields { name } } }`); !strings.Contains(r.Raw, "featuredImage") {
		t.Errorf("introspection: %s", r.Raw)
	}
}

func TestHeadlessGoFrontEndUsers(t *testing.T) {
	base, _, srv := hgSite(t)
	ctx := context.Background()
	hgNote(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	asCreateUser(t, srv, "alice", "subscriber", "alice-secret-pw")
	asCreateUser(t, srv, "bob", "subscriber", "bob-secret-pw")
	login := func(id, pw string) hgResp {
		return hgCall(t, base, "POST", "/auth/local", "", map[string]any{"identifier": id, "password": pw})
	}
	if r := login("alice", "alice-secret-pw"); r.Code != 404 {
		t.Fatalf("user auth is off by default: %d", r.Code)
	}
	st := srv.hgLoad()
	st.UserAuth = true
	st.Authenticated = map[string][]string{"note": {"find", "findOne", "create", "update", "delete"}}
	st.OwnerOnly = map[string][]string{"note": {"update", "delete"}}
	_ = srv.hgSaveSettings(ctx, st)

	if r := login("alice", "wrong"); r.Code != 400 {
		t.Errorf("wrong password: %d", r.Code)
	}
	if r := login("nobody", "x"); r.Code != 400 {
		t.Errorf("unknown user: %d", r.Code)
	}
	r := login("alice@example.test", "alice-secret-pw") // e-mail works too
	jwt, _ := r.Body["jwt"].(string)
	if r.Code != 200 || jwt == "" || r.Body["expiresIn"] != float64(3600) || strings.Contains(r.Raw, "password") {
		t.Fatalf("login: %d %s", r.Code, r.Raw)
	}
	bob, _ := login("bob", "bob-secret-pw").Body["jwt"].(string)
	if r := hgCall(t, base, "GET", "/users/me", jwt, nil); r.Code != 200 || !strings.Contains(r.Raw, `"alice"`) {
		t.Errorf("users/me: %d %s", r.Code, r.Raw)
	}
	// Anonymous: nothing.
	if r := hgCall(t, base, "POST", "/notes?status=published", "", map[string]any{"data": map[string]any{"title": "anon"}}); r.Code != 403 {
		t.Errorf("anonymous create: %d", r.Code)
	}
	// Alice creates (and publishes) an entry; created_by is her id.
	r = hgCall(t, base, "POST", "/notes?status=published", jwt, map[string]any{"data": map[string]any{"title": "Mine"}})
	if r.Code != 201 && r.Code != 200 {
		t.Fatalf("user create: %d %s", r.Code, r.Raw)
	}
	doc := hgDocOf(t, r)
	var owner int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT created_by FROM hg_entries WHERE document_id = $1`, doc).Scan(&owner)
	if owner == 0 {
		t.Error("created_by was not set for a user")
	}
	// Bob may read it, but not change, delete or unpublish it.
	if r := hgCall(t, base, "GET", "/notes/"+doc, bob, nil); r.Code != 200 {
		t.Errorf("bob read: %d", r.Code)
	}
	for _, c := range []struct{ m, p string }{{"PUT", "/notes/" + doc}, {"DELETE", "/notes/" + doc}, {"POST", "/notes/" + doc + "/unpublish"}} {
		if r := hgCall(t, base, c.m, c.p, bob, map[string]any{"data": map[string]any{"title": "Hijack"}}); r.Code != 403 {
			t.Errorf("bob %s %s: %d", c.m, c.p, r.Code)
		}
	}
	if r := hgCall(t, base, "PUT", "/notes/"+doc, jwt, map[string]any{"data": map[string]any{"title": "Mine 2"}}); r.Code != 200 {
		t.Errorf("owner update: %d %s", r.Code, r.Raw)
	}
	// An entry created by the admin token is not editable by a user.
	adminDoc := hgDocOf(t, hgCall(t, base, "POST", "/notes?status=published", full, map[string]any{"data": map[string]any{"title": "Admin's"}}))
	if r := hgCall(t, base, "PUT", "/notes/"+adminDoc, jwt, map[string]any{"data": map[string]any{"title": "x"}}); r.Code != 403 {
		t.Errorf("user edited an admin entry: %d", r.Code)
	}
	// Types outside the matrix stay closed.
	hgMemo(t, srv, false)
	if r := hgCall(t, base, "GET", "/memos", jwt, nil); r.Code != 403 {
		t.Errorf("user read a type outside the matrix: %d", r.Code)
	}
	// GraphQL honours it too.
	gq := func(tok, q string) hgResp {
		return hgCall(t, base, "POST", "/graphql", tok, map[string]any{"query": q})
	}
	if r := gq(bob, `mutation { deleteNote(documentId: "`+doc+`") { documentId } }`); !strings.Contains(r.Raw, "Forbidden") {
		t.Errorf("graphql owner check: %s", r.Raw)
	}
	if r := gq(jwt, `mutation { createNote(data: {title: "gql"}, status: PUBLISHED) { title } }`); !strings.Contains(r.Raw, "gql") {
		t.Errorf("graphql create as user: %s", r.Raw)
	}
	// Tampered, expired and revoked tokens.
	if r := hgCall(t, base, "GET", "/notes", jwt+"x", nil); r.Code != 401 {
		t.Errorf("tampered token: %d", r.Code)
	}
	expired := hgJWTSign(srv.hgUserKey(), map[string]any{"iss": hgUserIssuer, "sub": "1", "iat": 1, "exp": 2, "pw": "x"})
	if r := hgCall(t, base, "GET", "/notes", expired, nil); r.Code != 401 {
		t.Errorf("expired token: %d", r.Code)
	}
	none := base64Header(`{"alg":"none","typ":"JWT"}`) + ".e30."
	if r := hgCall(t, base, "GET", "/notes", none, nil); r.Code != 401 {
		t.Errorf("alg none: %d", r.Code)
	}
	hash, _ := auth.HashPassword("new-password-123")
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE login = 'alice'`, hash)
	if r := hgCall(t, base, "GET", "/notes", jwt, nil); r.Code != 401 {
		t.Errorf("token still valid after a password change: %d", r.Code)
	}
	// Switching the feature off revokes everything.
	st.UserAuth = false
	_ = srv.hgSaveSettings(ctx, st)
	if r := hgCall(t, base, "GET", "/notes", bob, nil); r.Code != 401 {
		t.Errorf("user tokens work while user auth is off: %d", r.Code)
	}
}

func base64Header(s string) string {
	return strings.TrimRight(strings.NewReplacer("+", "-", "/", "_").Replace(string(encodeB64([]byte(s)))), "=")
}

func TestHeadlessGoRegistration(t *testing.T) {
	base, _, srv := hgSite(t)
	ctx := context.Background()
	reg := func(u, e, p string) hgResp {
		return hgCall(t, base, "POST", "/auth/local/register", "", map[string]any{"username": u, "email": e, "password": p})
	}
	st := srv.hgLoad()
	st.UserAuth = true
	_ = srv.hgSaveSettings(ctx, st)
	if r := reg("carol", "carol@example.test", "carol-secret-pw"); r.Code != 403 {
		t.Fatalf("registration must be closed by default: %d", r.Code)
	}
	st.UserRegister = true
	_ = srv.hgSaveSettings(ctx, st)
	r := reg("carol", "carol@example.test", "carol-secret-pw")
	if r.Code != 201 || r.Body["jwt"] == "" {
		t.Fatalf("register: %d %s", r.Code, r.Raw)
	}
	if r := reg("carol", "other@example.test", "carol-secret-pw"); r.Code != 400 || !strings.Contains(r.Raw, "taken") {
		t.Errorf("duplicate username: %d %s", r.Code, r.Raw)
	}
	if r := reg("dave", "nope", "short"); r.Code != 400 || !strings.Contains(r.Raw, "email") || !strings.Contains(r.Raw, "password") {
		t.Errorf("invalid input: %d %s", r.Code, r.Raw)
	}
	u, err := srv.store.UserByLoginOrEmail(ctx, "carol")
	if err != nil || u.PrimaryRole() == "administrator" {
		t.Errorf("registered user: %v %v", u, err)
	}
	if r := hgCall(t, base, "POST", "/auth/local", "", map[string]any{"identifier": "carol", "password": "carol-secret-pw"}); r.Code != 200 {
		t.Errorf("login after register: %d", r.Code)
	}
}

func encodeB64(b []byte) []byte {
	out := make([]byte, base64.StdEncoding.EncodedLen(len(b)))
	base64.StdEncoding.Encode(out, b)
	return out
}
