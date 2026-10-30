package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// hgLibrary is the content model of the relation tests: authors, books, tags and profiles with
// bidirectional relations of every kind.
func hgLibrary(t *testing.T, srv *Server) {
	t.Helper()
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Writer", SingularName: "writer", PluralName: "writers", Fields: []hgField{
		{Name: "name", Type: "text", Required: true}}})
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Tag", SingularName: "tag", PluralName: "tags", Fields: []hgField{
		{Name: "label", Type: "text"}}})
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Profile", SingularName: "profile", PluralName: "profiles", Fields: []hgField{
		{Name: "bio", Type: "text"}}})
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Book", SingularName: "book", PluralName: "books", DraftAndPublish: true, Fields: []hgField{
		{Name: "title", Type: "text", Required: true}, {Name: "pages", Type: "number", Format: "integer"},
		{Name: "writer", Type: "relation", Relation: "manyToOne", Target: "writer", InversedBy: "books"},
		{Name: "tags", Type: "relation", Relation: "manyToMany", Target: "tag", InversedBy: "books"}}})
	w := hgCopySchema(srv.hgReg(context.Background()).Get("writer"))
	w.Fields = append(w.Fields, hgField{Name: "profile", Type: "relation", Relation: "oneToOne", Target: "profile", InversedBy: "owner"})
	hgMustSchema(t, srv, w)
}

func hgDocOf(t *testing.T, r hgResp) string {
	t.Helper()
	d, _ := r.data()["documentId"].(string)
	if d == "" {
		t.Fatalf("no documentId: %d %s", r.Code, r.Raw)
	}
	return d
}

func hgTitles(list []any, key string) []string {
	var out []string
	for _, x := range list {
		if m, ok := x.(map[string]any); ok {
			out = append(out, fmt.Sprint(m[key]))
		}
	}
	return out
}

func TestHeadlessGoInverseRelations(t *testing.T) {
	base, c, srv := hgSite(t)
	ctx := context.Background()
	hgLibrary(t, srv)
	reg := srv.hgReg(ctx)
	books := reg.Get("writer").Field("books")
	if books == nil || books.MappedBy != "writer" || books.Relation != "oneToMany" || books.Target != "book" {
		t.Fatalf("inverse field on writer: %+v", books)
	}
	if f := reg.Get("tag").Field("books"); f == nil || f.MappedBy != "tags" || f.Relation != "manyToMany" {
		t.Fatalf("inverse field on tag: %+v", f)
	}
	if f := reg.Get("profile").Field("owner"); f == nil || f.MappedBy != "profile" || f.Relation != "oneToOne" {
		t.Fatalf("inverse field on profile: %+v", f)
	}
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	ada := hgDocOf(t, hgCall(t, base, "POST", "/writers", full, map[string]any{"data": map[string]any{"name": "Ada"}}))
	bob := hgDocOf(t, hgCall(t, base, "POST", "/writers", full, map[string]any{"data": map[string]any{"name": "Bob"}}))
	zed := hgDocOf(t, hgCall(t, base, "POST", "/writers", full, map[string]any{"data": map[string]any{"name": "Zed"}}))
	b1 := hgDocOf(t, hgCall(t, base, "POST", "/books?status=published", full, map[string]any{"data": map[string]any{"title": "One", "pages": 100, "writer": ada}}))
	b2 := hgDocOf(t, hgCall(t, base, "POST", "/books?status=published", full, map[string]any{"data": map[string]any{"title": "Two", "pages": 200, "writer": ada}}))
	b3 := hgDocOf(t, hgCall(t, base, "POST", "/books?status=published", full, map[string]any{"data": map[string]any{"title": "Three", "pages": 300, "writer": zed}}))

	// The inverse side is populated from the owners.
	r := hgCall(t, base, "GET", "/writers/"+ada+"?populate=books", full, nil)
	if got := hgTitles(r.data()["books"].([]any), "title"); strings.Join(got, ",") != "One,Two" {
		t.Fatalf("ada's books: %v %s", got, r.Raw)
	}
	if r := hgCall(t, base, "GET", "/writers/"+bob+"?populate=*", full, nil); len(r.data()["books"].([]any)) != 0 {
		t.Fatalf("bob's books: %s", r.Raw)
	}
	// Writing the inverse side moves the owners' links (draft and published).
	r = hgCall(t, base, "PUT", "/writers/"+bob, full, map[string]any{"data": map[string]any{"books": []any{b2}}})
	if r.Code != 200 {
		t.Fatalf("set bob's books: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "GET", "/books/"+b2+"?populate=writer", full, nil); r.data()["writer"].(map[string]any)["name"] != "Bob" {
		t.Fatalf("book two's writer after the inverse write (published): %s", r.Raw)
	}
	if r := hgCall(t, base, "GET", "/books/"+b2+"?populate=writer&status=draft", full, nil); r.data()["writer"].(map[string]any)["name"] != "Bob" {
		t.Fatalf("book two's writer (draft): %s", r.Raw)
	}
	if r := hgCall(t, base, "GET", "/writers/"+ada+"?populate=books", full, nil); strings.Join(hgTitles(r.data()["books"].([]any), "title"), ",") != "One" {
		t.Fatalf("ada lost book two: %s", r.Raw)
	}
	// connect / disconnect on the inverse side.
	if r := hgCall(t, base, "PUT", "/writers/"+ada, full, map[string]any{"data": map[string]any{"books": map[string]any{"connect": []any{b2}, "disconnect": []any{b1}}}}); r.Code != 200 {
		t.Fatalf("connect: %s", r.Raw)
	}
	if r := hgCall(t, base, "GET", "/writers/"+ada+"?populate=books", full, nil); strings.Join(hgTitles(r.data()["books"].([]any), "title"), ",") != "Two" {
		t.Fatalf("after connect/disconnect: %s", r.Raw)
	}
	if r := hgCall(t, base, "GET", "/books/"+b1+"?populate=writer", full, nil); r.data()["writer"] != nil {
		t.Fatalf("book one still has a writer: %s", r.Raw)
	}
	// A single-valued inverse side takes one entry; unknown targets are refused.
	p1 := hgDocOf(t, hgCall(t, base, "POST", "/profiles", full, map[string]any{"data": map[string]any{"bio": "p1", "owner": ada}}))
	if r := hgCall(t, base, "GET", "/writers/"+ada+"?populate=profile", full, nil); r.data()["profile"].(map[string]any)["bio"] != "p1" {
		t.Fatalf("one-to-one written from the inverse side: %s", r.Raw)
	}
	if r := hgCall(t, base, "PUT", "/profiles/"+p1, full, map[string]any{"data": map[string]any{"owner": []any{ada, bob}}}); r.Code != 400 {
		t.Fatalf("two owners on a one-to-one: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "PUT", "/writers/"+bob, full, map[string]any{"data": map[string]any{"books": []any{"nope"}}}); r.Code != 400 {
		t.Fatalf("unknown inverse target: %d", r.Code)
	}
	// Many-to-many from the inverse side.
	tg := hgDocOf(t, hgCall(t, base, "POST", "/tags", full, map[string]any{"data": map[string]any{"label": "classic", "books": []any{b1, b3}}}))
	if r := hgCall(t, base, "GET", "/books/"+b3+"?populate=tags", full, nil); len(r.data()["tags"].([]any)) != 1 {
		t.Fatalf("book three's tags: %s", r.Raw)
	}
	// Filtering through the inverse side.
	r = hgCall(t, base, "GET", "/writers?filters[books][title][$eq]=Three", full, nil)
	if got := hgTitles(r.list(), "name"); strings.Join(got, ",") != "Zed" {
		t.Fatalf("filter through the inverse side: %v %s", got, r.Raw)
	}
	r = hgCall(t, base, "GET", "/writers?filters[books][$null]=true&sort=name", full, nil)
	if got := hgTitles(r.list(), "name"); strings.Join(got, ",") != "Bob" {
		t.Fatalf("$null on the inverse side: %v", got)
	}
	if r := hgCall(t, base, "GET", "/tags?filters[books][documentId][$eq]="+b1, full, nil); len(r.list()) != 1 {
		t.Fatalf("tags of book one: %s", r.Raw)
	}
	// Sorting through relations.
	r = hgCall(t, base, "GET", "/books?sort=writer.name:desc,title", full, nil)
	if got := hgTitles(r.list(), "title"); strings.Join(got, ",") != "Three,Two,One" {
		t.Fatalf("sort by writer.name: %v %s", got, r.Raw)
	}
	r = hgCall(t, base, "GET", "/books?sort[0]=writer.name:asc&sort[1]=pages:desc", full, nil)
	if got := hgTitles(r.list(), "title"); strings.Join(got, ",") != "Two,Three,One" {
		t.Fatalf("sort by writer.name asc (nulls last): %v", got)
	}
	r = hgCall(t, base, "GET", "/profiles?sort=owner.name", full, nil)
	if r.Code != 200 || len(r.list()) != 1 {
		t.Fatalf("sort through a single inverse relation: %s", r.Raw)
	}
	for _, bad := range []string{"tags.label", "writer.nope", "writer.books.title", "title.x"} {
		if r := hgCall(t, base, "GET", "/books?sort="+bad, full, nil); r.Code != 400 {
			t.Errorf("sort %s: %d", bad, r.Code)
		}
	}
	// GraphQL reads the inverse side too.
	g := hgCall(t, base, "POST", "/graphql", full, map[string]any{"query": `{ writer(documentId: "` + ada + `") { name books { title } } }`})
	if bs := g.data()["writer"].(map[string]any)["books"].([]any); len(bs) != 1 || bs[0].(map[string]any)["title"] != "Two" {
		t.Fatalf("GraphQL inverse: %s", g.Raw)
	}
	// The Content Manager shows the inverse values.
	_, body, _ := get(t, c, base+"/wp-admin/headlessgo?page=entry&type=writer&doc="+ada)
	if !strings.Contains(body, `"books":["`+b2+`"]`) || !strings.Contains(body, `"mappedBy":"writer"`) {
		t.Fatal("inverse values missing in the edit screen's model")
	}
	// Deleting a document removes it from every relation that points at it.
	if r := hgCall(t, base, "DELETE", "/writers/"+ada, full, nil); r.Code != 204 {
		t.Fatalf("delete ada: %d", r.Code)
	}
	var stale int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM hg_entries WHERE data::text LIKE '%' || $1 || '%' OR published_data::text LIKE '%' || $1 || '%'`, ada).Scan(&stale)
	if stale != 0 {
		t.Fatalf("%d entries still point at a deleted writer", stale)
	}
	if r := hgCall(t, base, "DELETE", "/tags/"+tg, full, nil); r.Code != 204 {
		t.Fatal("delete tag")
	}
	if r := hgCall(t, base, "GET", "/books/"+b3+"?populate=tags", full, nil); len(r.data()["tags"].([]any)) != 0 {
		t.Fatalf("book three keeps a deleted tag: %s", r.Raw)
	}

	// The builder keeps both sides in step: renaming either side, then removing them.
	book := hgCopySchema(srv.hgReg(ctx).Get("book"))
	for i := range book.Fields {
		if book.Fields[i].Name == "writer" {
			book.Fields[i].Name, book.Fields[i].RenamedFrom = "author", "writer"
		}
	}
	hgMustSchema(t, srv, book)
	if f := srv.hgReg(ctx).Get("writer").Field("books"); f == nil || f.MappedBy != "author" {
		t.Fatalf("inverse after renaming the owner: %+v", f)
	}
	if r := hgCall(t, base, "GET", "/books/"+b3+"?populate=author", full, nil); r.data()["author"].(map[string]any)["name"] != "Zed" {
		t.Fatalf("values after the rename: %s", r.Raw)
	}
	wr := hgCopySchema(srv.hgReg(ctx).Get("writer"))
	for i := range wr.Fields {
		if wr.Fields[i].Name == "books" {
			wr.Fields[i].Name, wr.Fields[i].RenamedFrom = "works", "books"
		}
	}
	hgMustSchema(t, srv, wr)
	if f := srv.hgReg(ctx).Get("book").Field("author"); f.InversedBy != "works" {
		t.Fatalf("owner after renaming the inverse side: %+v", f)
	}
	if r := hgCall(t, base, "GET", "/writers/"+zed+"?populate=works", full, nil); len(r.data()["works"].([]any)) != 1 {
		t.Fatalf("renamed inverse field: %s", r.Raw)
	}
	// Exporting and importing again keeps the links.
	raw, _ := json.Marshal(srv.hgExportSchemas(ctx))
	if _, err := srv.hgImportSchemas(ctx, raw); err != nil {
		t.Fatalf("import: %v", err)
	}
	if f := srv.hgReg(ctx).Get("writer").Field("works"); f == nil || f.MappedBy != "author" {
		t.Fatalf("inverse after import: %+v", f)
	}
	// Removing the inverse field clears inversedBy; removing the owner removes the inverse side.
	wr = hgCopySchema(srv.hgReg(ctx).Get("writer"))
	hgRemoveField(wr, "works")
	hgMustSchema(t, srv, wr)
	if f := srv.hgReg(ctx).Get("book").Field("author"); f.InversedBy != "" {
		t.Fatalf("owner after removing the inverse: %+v", f)
	}
	book = hgCopySchema(srv.hgReg(ctx).Get("book"))
	hgRemoveField(book, "tags")
	hgMustSchema(t, srv, book)
	if f := srv.hgReg(ctx).Get("tag").Field("books"); f != nil {
		t.Fatalf("inverse kept after removing the owner: %+v", f)
	}
	// An inverse field that mirrors nothing, or a name already taken, is refused.
	tag := hgCopySchema(srv.hgReg(ctx).Get("tag"))
	tag.Fields = append(tag.Fields, hgField{Name: "ghost", Type: "relation", Relation: "manyToMany", Target: "book", MappedBy: "nothing"})
	if err := srv.hgSaveSchema(ctx, tag); err == nil {
		t.Fatal("an inverse field without an owner was accepted")
	}
	book = hgCopySchema(srv.hgReg(ctx).Get("book"))
	book.Fields = append(book.Fields, hgField{Name: "labels", Type: "relation", Relation: "manyToMany", Target: "tag", InversedBy: "label"})
	if err := srv.hgSaveSchema(ctx, book); err == nil || !strings.Contains(err.Error(), "already has a field") {
		t.Fatalf("inverse name collision: %v", err)
	}
	// Self relations: the inverse field lives on the same type.
	wr = hgCopySchema(srv.hgReg(ctx).Get("writer"))
	wr.Fields = append(wr.Fields, hgField{Name: "mentor", Type: "relation", Relation: "manyToOne", Target: "writer", InversedBy: "mentees"})
	hgMustSchema(t, srv, wr)
	if f := srv.hgReg(ctx).Get("writer").Field("mentees"); f == nil || f.MappedBy != "mentor" || f.Relation != "oneToMany" {
		t.Fatalf("self inverse: %+v", f)
	}
	if r := hgCall(t, base, "PUT", "/writers/"+zed, full, map[string]any{"data": map[string]any{"mentees": []any{bob}}}); r.Code != 200 {
		t.Fatalf("self inverse write: %s", r.Raw)
	}
	if r := hgCall(t, base, "GET", "/writers/"+bob+"?populate=mentor", full, nil); r.data()["mentor"].(map[string]any)["name"] != "Zed" {
		t.Fatalf("self inverse owner: %s", r.Raw)
	}
	wr = hgCopySchema(srv.hgReg(ctx).Get("writer"))
	for i := range wr.Fields {
		if wr.Fields[i].Name == "mentor" {
			wr.Fields[i].InversedBy = ""
		}
	}
	hgMustSchema(t, srv, wr)
	if srv.hgReg(ctx).Get("writer").Field("mentees") != nil {
		t.Fatal("self inverse kept after clearing inversedBy")
	}
}

// hgPNG is a w×h PNG image.
func hgPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// hgUpload posts files (name -> content) and form values to /upload.
func hgUpload(t *testing.T, base, token string, files map[string][]byte, vals map[string]string) hgResp {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, _ := mw.CreateFormFile("files", n)
		_, _ = w.Write(files[n])
	}
	for k, v := range vals {
		_ = mw.WriteField(k, v)
	}
	mw.Close()
	req, _ := http.NewRequest("POST", base+hgAPIPrefix+"/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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

func TestHeadlessGoUpload(t *testing.T) {
	base, c, srv := hgSite(t)
	ctx := context.Background()
	hgModel(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	ro := hgNewToken(t, srv, &hgToken{Name: "RO", Kind: "read-only", Upload: true})
	up := hgNewToken(t, srv, &hgToken{Name: "Uploader", Kind: "custom", Upload: true, Permissions: map[string][]string{"article": {"find", "findOne"}}})
	pic := hgPNG(400, 300)
	if r := hgUpload(t, base, "", map[string][]byte{"a.png": pic}, nil); r.Code != 403 {
		t.Fatalf("public upload: %d", r.Code)
	}
	if r := hgUpload(t, base, ro, map[string][]byte{"a.png": pic}, nil); r.Code != 403 {
		t.Fatalf("read-only upload: %d", r.Code)
	}
	if r := hgCall(t, base, "POST", "/upload", full, map[string]any{"data": 1}); r.Code != 400 {
		t.Fatalf("JSON upload: %d", r.Code)
	}
	r := hgUpload(t, base, up, map[string][]byte{"photo.png": pic}, map[string]string{"fileInfo": `{"alternativeText":"A photo","caption":"Cap","name":"My photo"}`})
	if r.Code != 201 {
		t.Fatalf("upload: %d %s", r.Code, r.Raw)
	}
	var list []map[string]any
	_ = json.Unmarshal([]byte(r.Raw), &list)
	if len(list) != 1 || list[0]["mime"] != "image/png" || list[0]["alternativeText"] != "A photo" || list[0]["title"] != "My photo" {
		t.Fatalf("uploaded file: %s", r.Raw)
	}
	if f, _ := list[0]["formats"].(map[string]any); f["thumbnail"] == nil || f["medium"] == nil {
		t.Fatalf("image sizes were not generated: %v", list[0]["formats"])
	}
	id := int64(list[0]["id"].(float64))
	if a, err := srv.store.GetAttachment(ctx, id); err != nil || a.Caption != "Cap" {
		t.Fatalf("attachment row: %v %+v", err, a)
	}
	if r := hgCall(t, base, "GET", fmt.Sprintf("/upload/files/%d", id), up, nil); r.Code != 200 || r.Body["id"] != float64(id) {
		t.Fatalf("get file: %d %s", r.Code, r.Raw)
	}
	// Attaching needs update on the type.
	doc := hgDocOf(t, hgCall(t, base, "POST", "/articles", full, map[string]any{"data": map[string]any{"title": "With cover"}}))
	if r := hgUpload(t, base, up, map[string][]byte{"c.png": pic}, map[string]string{"ref": "article", "refId": doc, "field": "cover"}); r.Code != 403 {
		t.Fatalf("attach without update: %d", r.Code)
	}
	r = hgUpload(t, base, full, map[string][]byte{"c.png": pic}, map[string]string{"ref": "articles", "refId": doc, "field": "cover"})
	if r.Code != 201 {
		t.Fatalf("attach: %d %s", r.Code, r.Raw)
	}
	_ = json.Unmarshal([]byte(r.Raw), &list)
	r = hgCall(t, base, "GET", "/articles/"+doc+"?status=draft&populate=cover", full, nil)
	if cv, _ := r.data()["cover"].(map[string]any); cv == nil || cv["id"] != list[0]["id"] {
		t.Fatalf("cover after attaching: %s", r.Raw)
	}
	// A multiple media field appends; a single one refuses two files.
	if r := hgUpload(t, base, full, map[string][]byte{"1.png": pic, "2.png": pic}, map[string]string{"ref": "article", "refId": doc, "field": "cover"}); r.Code != 400 {
		t.Fatalf("two files on a single media field: %d", r.Code)
	}
	// A file type the field does not allow is refused and not kept.
	var before, after int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM media`).Scan(&before)
	auth := hgDocOf(t, hgCall(t, base, "POST", "/authors", full, map[string]any{"data": map[string]any{"name": "Ann"}}))
	r = hgUpload(t, base, full, map[string][]byte{"notes.txt": []byte("hello world")}, map[string]string{"ref": "author", "refId": auth, "field": "avatar"})
	if r.Code != 400 {
		t.Fatalf("text file on an image field: %d %s", r.Code, r.Raw)
	}
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM media`).Scan(&after)
	if after != before {
		t.Fatalf("a refused upload left %d files", after-before)
	}
	for _, bad := range []map[string]string{{"ref": "nope", "refId": doc, "field": "cover"}, {"ref": "article", "refId": doc, "field": "title"}, {"ref": "article", "refId": "missing", "field": "cover"}} {
		if r := hgUpload(t, base, full, map[string][]byte{"x.png": pic}, bad); r.Code != 400 && r.Code != 404 {
			t.Errorf("bad attach %v: %d", bad, r.Code)
		}
	}
	if r := hgUpload(t, base, full, nil, nil); r.Code != 400 {
		t.Fatalf("no files: %d", r.Code)
	}
	// The token screen offers the permission.
	_, body, _ := get(t, c, base+"/wp-admin/headlessgo?page=tokens&new=1")
	if !strings.Contains(body, `name="upload"`) {
		t.Fatal("no upload checkbox on the token screen")
	}
	_, body, _ = get(t, c, base+"/wp-admin/headlessgo?page=tokens")
	if !strings.Contains(body, "+ upload") {
		t.Fatal("the custom token's upload permission is not listed")
	}
}

func TestHeadlessGoHistoryAndSchedule(t *testing.T) {
	base, c, srv := hgSite(t)
	ctx := context.Background()
	hgModel(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	doc := hgDocOf(t, hgCall(t, base, "POST", "/articles", full, map[string]any{"data": map[string]any{"title": "First title", "views": 1}}))
	hgCall(t, base, "PUT", "/articles/"+doc, full, map[string]any{"data": map[string]any{"title": "Second title", "views": 2}})
	hgCall(t, base, "POST", "/articles/"+doc+"/publish", full, nil)
	hgCall(t, base, "PUT", "/articles/"+doc, full, map[string]any{"data": map[string]any{"title": "Third title"}})
	e, err := srv.hgGetEntry(ctx, "article", doc, "en")
	if err != nil {
		t.Fatal(err)
	}
	vs := srv.hgVersions(ctx, e.ID, 50)
	var actions []string
	for _, v := range vs {
		actions = append(actions, v.Action)
	}
	if strings.Join(actions, ",") != "save,publish,save,create" {
		t.Fatalf("history: %v", actions)
	}
	// The edit screen lists the versions; the history screen compares one with the draft.
	_, body, _ := get(t, c, base+"/wp-admin/headlessgo?page=entry&type=article&doc="+doc+"&locale=en")
	if !strings.Contains(body, "Compare and restore versions") || !strings.Contains(body, "Published") || !strings.Contains(body, "Schedule") {
		t.Fatal("history / schedule boxes missing on the edit screen")
	}
	first := vs[len(vs)-1]
	code, body, _ := get(t, c, base+fmt.Sprintf("/wp-admin/headlessgo?page=history&type=article&doc=%s&locale=en&v=%d", doc, first.ID))
	if code != 200 || !strings.Contains(body, "First title") || !strings.Contains(body, "Third title") || !strings.Contains(body, "hg-changed") || !strings.Contains(body, "Restore this version") {
		t.Fatalf("history screen: %d", code)
	}
	// Restore the first version as the draft.
	code, _, loc := hgAdminSubmit(t, c, base, url.Values{"page": {"entry"}, "do": {"restore_version"}, "type": {"article"}, "doc": {doc}, "locale": {"en"}, "version": {fmt.Sprint(first.ID)}})
	if code != http.StatusFound || strings.Contains(loc, "hg-error") {
		t.Fatalf("restore: %d %s", code, loc)
	}
	r := hgCall(t, base, "GET", "/articles/"+doc+"?status=draft", full, nil)
	if r.data()["title"] != "First title" || r.data()["views"] != 1.0 {
		t.Fatalf("draft after restore: %s", r.Raw)
	}
	if r := hgCall(t, base, "GET", "/articles/"+doc, full, nil); r.data()["title"] != "Second title" {
		t.Fatalf("the published version changed on restore: %s", r.Raw)
	}
	// A version of another document cannot be restored here.
	other := hgDocOf(t, hgCall(t, base, "POST", "/articles", full, map[string]any{"data": map[string]any{"title": "Other one"}}))
	_, _, loc = hgAdminSubmit(t, c, base, url.Values{"page": {"entry"}, "do": {"restore_version"}, "type": {"article"}, "doc": {other}, "locale": {"en"}, "version": {fmt.Sprint(first.ID)}})
	if !strings.Contains(loc, "hg-error") {
		t.Fatal("restored a version of another document")
	}
	// The history is capped.
	saved := hgHistoryKeep
	hgHistoryKeep = 3
	for i := 0; i < 5; i++ {
		hgCall(t, base, "PUT", "/articles/"+other, full, map[string]any{"data": map[string]any{"title": fmt.Sprintf("Edit %d", i)}})
	}
	hgHistoryKeep = saved
	oe, _ := srv.hgGetEntry(ctx, "article", other, "en")
	if n := len(srv.hgVersions(ctx, oe.ID, 50)); n != 3 {
		t.Fatalf("kept %d versions", n)
	}

	// Scheduling from the edit screen (site time zone), run by the tick.
	var jobs int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE name = $1 AND recurrence <> ''`, hgTickJob).Scan(&jobs)
	if jobs != 1 {
		t.Fatalf("the tick job is not scheduled: %d", jobs)
	}
	past := time.Now().In(srv.loc()).Add(-time.Minute).Format("2006-01-02T15:04")
	later := time.Now().In(srv.loc()).Add(2 * time.Hour).Format("2006-01-02T15:04")
	code, _, loc = hgAdminSubmit(t, c, base, url.Values{"page": {"entry"}, "do": {"schedule_entry"}, "type": {"article"}, "doc": {other}, "locale": {"en"},
		"publish_at": {past}, "unpublish_at": {later}})
	if code != http.StatusFound || strings.Contains(loc, "hg-error") {
		t.Fatalf("schedule: %s", loc)
	}
	_, body, _ = get(t, c, base+"/wp-admin/headlessgo?page=entries&type=article")
	if !strings.Contains(body, "Publishes ") {
		t.Fatal("the list does not show the schedule")
	}
	if r := hgCall(t, base, "GET", "/articles/"+other, full, nil); r.Code != 404 {
		t.Fatalf("published before the tick: %d", r.Code)
	}
	if err := srv.hgTick(ctx); err != nil {
		t.Fatal(err)
	}
	if r := hgCall(t, base, "GET", "/articles/"+other, full, nil); r.Code != 200 || r.data()["title"] != "Edit 4" {
		t.Fatalf("not published by the tick: %d %s", r.Code, r.Raw)
	}
	oe, _ = srv.hgGetEntry(ctx, "article", other, "en")
	if oe.PublishAt != nil || oe.UnpublishAt == nil {
		t.Fatalf("schedule after publishing: %v %v", oe.PublishAt, oe.UnpublishAt)
	}
	if vs := srv.hgVersions(ctx, oe.ID, 1); vs[0].Action != "schedule" {
		t.Fatalf("history of the scheduled publication: %s", vs[0].Action)
	}
	// Unpublication, through the REST endpoint (UTC), run after a "restart" (only the database).
	r = hgCall(t, base, "POST", "/articles/"+other+"/schedule", full, map[string]any{"data": map[string]any{"unpublishAt": time.Now().UTC().Add(-time.Second).Format(time.RFC3339)}})
	if r.Code != 200 || r.data()["unpublishAt"] == nil || r.data()["publishAt"] != nil {
		t.Fatalf("schedule API: %d %s", r.Code, r.Raw)
	}
	if n := srv.hgRunSchedules(ctx); n != 1 {
		t.Fatalf("schedules run: %d", n)
	}
	if r := hgCall(t, base, "GET", "/articles/"+other, full, nil); r.Code != 404 {
		t.Fatalf("still published after the scheduled unpublication: %d", r.Code)
	}
	// A scheduled publication that fails validation keeps its reason.
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE hg_entries SET data = data - 'title' WHERE document_id = $1`, other)
	if r := hgCall(t, base, "POST", "/articles/"+other+"/schedule", full, map[string]any{"data": map[string]any{"publishAt": "2020-01-01T00:00:00Z"}}); r.Code != 200 {
		t.Fatalf("schedule: %s", r.Raw)
	}
	srv.hgRunSchedules(ctx)
	oe, _ = srv.hgGetEntry(ctx, "article", other, "en")
	if oe.Published != nil || oe.PublishAt != nil || !strings.Contains(oe.ScheduleError, "title") {
		t.Fatalf("failed scheduled publication: %+v %q", oe.Published, oe.ScheduleError)
	}
	_, body, _ = get(t, c, base+"/wp-admin/headlessgo?page=entry&type=article&doc="+other+"&locale=en")
	if !strings.Contains(body, "The scheduled publication failed") {
		t.Fatal("the schedule error is not shown")
	}
	// Bad schedules.
	if r := hgCall(t, base, "POST", "/articles/"+other+"/schedule", full, map[string]any{"data": map[string]any{"publishAt": "2030-01-02T00:00:00Z", "unpublishAt": "2030-01-01T00:00:00Z"}}); r.Code != 400 {
		t.Fatalf("unpublication before publication: %d", r.Code)
	}
	if r := hgCall(t, base, "POST", "/authors/x/schedule", full, map[string]any{"data": map[string]any{"publishAt": "2030-01-02T00:00:00Z"}}); r.Code != 400 && r.Code != 404 {
		t.Fatalf("schedule without draft & publish: %d", r.Code)
	}
	ro := hgNewToken(t, srv, &hgToken{Name: "RO", Kind: "read-only"})
	if r := hgCall(t, base, "POST", "/articles/"+other+"/schedule", ro, map[string]any{"data": map[string]any{}}); r.Code != 403 {
		t.Fatalf("schedule with a read-only token: %d", r.Code)
	}
	// Deactivation removes the job; activation brings it back.
	hgToggle(t, c, base, "deactivate")
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE name = $1`, hgTickJob).Scan(&jobs)
	if jobs != 0 {
		t.Fatal("the tick job survived deactivation")
	}
	hgToggle(t, c, base, "activate")
}

func TestHeadlessGoBulkActions(t *testing.T) {
	base, c, srv := hgSite(t)
	ctx := context.Background()
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	hgModel(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	var docs []string
	for _, title := range []string{"Alpha", "Beta", "Gamma"} {
		d := hgDocOf(t, hgCall(t, base, "POST", "/articles", full, map[string]any{"data": map[string]any{"title": title}}))
		hgCall(t, base, "PUT", "/articles/"+d+"?locale=fr", full, map[string]any{"data": map[string]any{"title": title + " fr"}})
		docs = append(docs, d)
	}
	_, body, _ := get(t, c, base+"/wp-admin/headlessgo?page=entries&type=article&locale=en")
	if !strings.Contains(body, `name="bulk_action"`) || !strings.Contains(body, `name="docs[]" value="`+docs[0]+`"`) {
		t.Fatal("no bulk actions on the list")
	}
	bulk := func(action string, ds []string, extra url.Values) string {
		f := url.Values{"page": {"entries"}, "do": {"bulk_entries"}, "type": {"article"}, "locale": {"en"}, "bulk_action": {action}, "docs[]": ds}
		for k, v := range extra {
			f[k] = v
		}
		code, _, loc := hgAdminSubmit(t, c, base, f)
		if code != http.StatusFound {
			t.Fatalf("bulk %s: %d", action, code)
		}
		u, _ := url.Parse(loc)
		return u.Query().Get("hg-msg") + u.Query().Get("hg-error")
	}
	if msg := bulk("publish", docs[:2], nil); msg != "2 entries published." {
		t.Fatalf("bulk publish: %q", msg)
	}
	if r := hgCall(t, base, "GET", "/articles?locale=en", full, nil); len(r.list()) != 2 {
		t.Fatalf("published en: %s", r.Raw)
	}
	if r := hgCall(t, base, "GET", "/articles?locale=fr", full, nil); len(r.list()) != 0 {
		t.Fatalf("the fr versions were published too: %s", r.Raw)
	}
	if msg := bulk("publish", docs[2:], url.Values{"all_locales": {"1"}}); msg != "1 entry published." {
		t.Fatalf("bulk publish in every locale: %q", msg)
	}
	if r := hgCall(t, base, "GET", "/articles?locale=fr", full, nil); len(r.list()) != 1 {
		t.Fatalf("fr after publishing in every locale: %s", r.Raw)
	}
	if msg := bulk("unpublish", docs, nil); msg != "3 entries unpublished." {
		t.Fatalf("bulk unpublish: %q", msg)
	}
	if r := hgCall(t, base, "GET", "/articles?locale=en", full, nil); len(r.list()) != 0 {
		t.Fatalf("en after unpublishing: %s", r.Raw)
	}
	// A failed publication is reported (a required field is empty).
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE hg_entries SET data = data - 'title' WHERE document_id = $1 AND locale = 'en'`, docs[1])
	if msg := bulk("publish", docs[:2], nil); !strings.Contains(msg, "1 entry published.") || !strings.Contains(msg, "1 failed") {
		t.Fatalf("partial bulk publish: %q", msg)
	}
	if msg := bulk("", docs[:1], nil); !strings.Contains(msg, "bulk action") {
		t.Fatalf("no action: %q", msg)
	}
	// Delete one locale, then everything.
	if msg := bulk("delete", docs[:1], nil); msg != "1 entry deleted." {
		t.Fatalf("bulk delete en: %q", msg)
	}
	if len(srv.hgDocLocales(ctx, "article", docs[0])) != 1 {
		t.Fatal("deleting the en version removed the fr one")
	}
	if msg := bulk("delete", docs, url.Values{"all_locales": {"1"}}); msg != "3 entries deleted." {
		t.Fatalf("bulk delete everywhere: %q", msg)
	}
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM hg_entries WHERE type_uid = 'article'`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d entries left", n)
	}
}

func TestHeadlessGoDurableWebhooks(t *testing.T) {
	base, c, srv := hgSite(t)
	ctx := context.Background()
	saved := hgWebhookBase
	hgWebhookBase = 15 * time.Millisecond
	t.Cleanup(func() { hgWebhookBase = saved })
	hgModel(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	var mu sync.Mutex
	failing := true
	calls := 0
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if failing {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer hook.Close()
	st := srv.hgLoad()
	st.WebhookAttempts = 3
	_ = srv.hgSaveSettings(ctx, st)
	w := &hgWebhook{Name: "Flaky", URL: hook.URL, Events: []string{"entry.create"}, Enabled: true}
	if err := srv.hgSaveWebhook(ctx, w); err != nil {
		t.Fatal(err)
	}
	// Every attempt fails: three attempts with a backoff, then the delivery is failed.
	hgCall(t, base, "POST", "/articles", full, map[string]any{"data": map[string]any{"title": "Lost"}})
	hgWebhookWG.Wait()
	var status, lastErr string
	var attempts int
	var qid int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id, status, attempts, last_error FROM hg_webhook_queue WHERE webhook_id = $1`, w.ID).Scan(&qid, &status, &attempts, &lastErr)
	if status != "failed" || attempts != 3 || calls != 3 || lastErr != "HTTP 502" {
		t.Fatalf("after max attempts: %s %d (calls %d) %q", status, attempts, calls, lastErr)
	}
	var logged int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM hg_webhook_deliveries WHERE webhook_id = $1`, w.ID).Scan(&logged)
	if logged != 3 {
		t.Fatalf("delivery log: %d", logged)
	}
	var gaps []time.Duration
	rows, _ := srv.store.Pool.Query(ctx, `SELECT created_at FROM hg_webhook_deliveries WHERE webhook_id = $1 ORDER BY id`, w.ID)
	var prev time.Time
	for rows.Next() {
		var at time.Time
		_ = rows.Scan(&at)
		if !prev.IsZero() {
			gaps = append(gaps, at.Sub(prev))
		}
		prev = at
	}
	rows.Close()
	if len(gaps) != 2 || gaps[0] < 15*time.Millisecond || gaps[1] < 60*time.Millisecond {
		t.Fatalf("no exponential backoff: %v", gaps)
	}
	// The webhook screen lists it; "Retry now" sends it again.
	_, body, _ := get(t, c, base+fmt.Sprintf("/wp-admin/headlessgo?page=webhooks&id=%d", w.ID))
	if !strings.Contains(body, "Waiting and failed deliveries") || !strings.Contains(body, "HTTP 502") {
		t.Fatal("queue not shown")
	}
	mu.Lock()
	failing = false
	mu.Unlock()
	code, _, loc := hgAdminSubmit(t, c, base, url.Values{"page": {"webhooks"}, "do": {"retry_delivery"}, "id": {fmt.Sprint(qid)}, "webhook": {fmt.Sprint(w.ID)}})
	if code != http.StatusFound || strings.Contains(loc, "hg-error") {
		t.Fatalf("retry: %s", loc)
	}
	hgWebhookWG.Wait()
	_ = srv.store.Pool.QueryRow(ctx, `SELECT status, attempts FROM hg_webhook_queue WHERE id = $1`, qid).Scan(&status, &attempts)
	if status != "done" || attempts != 4 {
		t.Fatalf("after retry: %s %d", status, attempts)
	}

	// A restart: the retry is far away (no in-process timer), only the queue row remains; the
	// scheduler's tick delivers it once it is due.
	hgWebhookBase = time.Hour
	mu.Lock()
	failing, calls = true, 0
	mu.Unlock()
	hgCall(t, base, "POST", "/articles", full, map[string]any{"data": map[string]any{"title": "Later"}})
	hgWebhookWG.Wait()
	var next time.Time
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id, status, attempts, next_at FROM hg_webhook_queue WHERE webhook_id = $1 ORDER BY id DESC LIMIT 1`, w.ID).Scan(&qid, &status, &attempts, &next)
	if status != "pending" || attempts != 1 || time.Until(next) < 50*time.Minute {
		t.Fatalf("waiting delivery: %s %d %v", status, attempts, time.Until(next))
	}
	mu.Lock()
	failing = false
	mu.Unlock()
	if n := srv.hgProcessQueue(ctx); n != 0 {
		t.Fatalf("a delivery was sent before it was due: %d", n)
	}
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE hg_webhook_queue SET next_at = now() - interval '1 second' WHERE id = $1`, qid) // time passes
	if err := srv.hgTick(ctx); err != nil {
		t.Fatal(err)
	}
	_ = srv.store.Pool.QueryRow(ctx, `SELECT status, attempts FROM hg_webhook_queue WHERE id = $1`, qid).Scan(&status, &attempts)
	if status != "done" || attempts != 2 {
		t.Fatalf("after the tick: %s %d", status, attempts)
	}
	// A row left "running" by a crashed instance goes back to the queue.
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE hg_webhook_queue SET status = 'running', locked_at = now() - interval '1 hour', next_at = now() WHERE id = $1`, qid)
	srv.hgProcessQueue(ctx)
	_ = srv.store.Pool.QueryRow(ctx, `SELECT status, attempts FROM hg_webhook_queue WHERE id = $1`, qid).Scan(&status, &attempts)
	if status != "done" || attempts != 3 {
		t.Fatalf("stale running row: %s %d", status, attempts)
	}
	// A disabled webhook's waiting deliveries fail instead of being sent.
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE hg_webhook_queue SET status = 'pending', next_at = now() WHERE id = $1`, qid)
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE hg_webhooks SET enabled = false WHERE id = $1`, w.ID)
	before := calls
	srv.hgProcessQueue(ctx)
	_ = srv.store.Pool.QueryRow(ctx, `SELECT status FROM hg_webhook_queue WHERE id = $1`, qid).Scan(&status)
	if status != "failed" || calls != before {
		t.Fatalf("disabled webhook: %s, %d calls", status, calls-before)
	}
}

func TestHeadlessGoGraphQLMore(t *testing.T) {
	base, c, srv := hgSite(t)
	hgModel(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	ro := hgNewToken(t, srv, &hgToken{Name: "RO", Kind: "read-only"})
	gql := func(token, query string, vars map[string]any, op string) hgResp {
		body := map[string]any{"query": query, "variables": vars}
		if op != "" {
			body["operationName"] = op
		}
		return hgCall(t, base, "POST", "/graphql", token, body)
	}
	ok := func(r hgResp) map[string]any {
		t.Helper()
		if r.Code != 200 || r.Body["errors"] != nil {
			t.Fatalf("GraphQL: %d %s", r.Code, r.Raw)
		}
		return r.data()
	}
	// Mutations.
	d := ok(gql(full, `mutation New($t: String!) { createArticle(data: {title: $t, views: 5}, status: PUBLISHED) { documentId title views publishedAt } }`, map[string]any{"t": "Made by GraphQL"}, ""))
	art := d["createArticle"].(map[string]any)
	doc, _ := art["documentId"].(string)
	if doc == "" || art["title"] != "Made by GraphQL" || art["views"] != 5.0 || art["publishedAt"] == nil {
		t.Fatalf("createArticle: %v", art)
	}
	d = ok(gql(full, `mutation { updateArticle(documentId: "`+doc+`", data: {views: 7}) { title views } }`, nil, ""))
	if d["updateArticle"].(map[string]any)["views"] != 7.0 {
		t.Fatalf("updateArticle: %v", d)
	}
	ok(gql(full, `mutation { publishArticle(documentId: "`+doc+`") { views } }`, nil, ""))
	if r := hgCall(t, base, "GET", "/articles/"+doc, full, nil); r.data()["views"] != 7.0 {
		t.Fatalf("published after publishArticle: %s", r.Raw)
	}
	d = ok(gql(full, `mutation { updateHomepage(data: {headline: "Hi"}, status: PUBLISHED) { headline } }`, nil, ""))
	if d["updateHomepage"].(map[string]any)["headline"] != "Hi" {
		t.Fatalf("updateHomepage: %v", d)
	}
	if r := gql(full, `mutation { createArticle(data: {title: "x"}) { title } }`, nil, ""); r.Body["errors"] == nil || !strings.Contains(r.Raw, "Validation") {
		t.Fatalf("validation error: %s", r.Raw)
	}
	if r := gql(ro, `mutation { createArticle(data: {title: "Nope"}) { title } }`, nil, ""); r.Body["errors"] == nil {
		t.Fatalf("read-only token mutated: %s", r.Raw)
	}
	if r := hgCall(t, base, "GET", "/graphql?query="+url.QueryEscape(`mutation { deleteArticle(documentId: "`+doc+`") { documentId } }`), full, nil); r.Code != 405 {
		t.Fatalf("mutation over GET: %d", r.Code)
	}
	// Named fragments, inline fragments without a type, directives with variables and defaults,
	// several operations with operationName.
	q := `
	query One($withViews: Boolean = false, $skipTitle: Boolean!) {
		articles { ...Card ... @include(if: $withViews) { views } }
		... on Query { homepage { headline } }
	}
	query Two { articles { documentId } }
	fragment Card on Article { title @skip(if: $skipTitle) documentId ...Inner }
	fragment Inner on Article { publishedAt }`
	d = ok(gql(full, q, map[string]any{"skipTitle": false}, "One"))
	a0 := d["articles"].([]any)[0].(map[string]any)
	if a0["title"] != "Made by GraphQL" || a0["publishedAt"] == nil || a0["views"] != nil || d["homepage"].(map[string]any)["headline"] != "Hi" {
		t.Fatalf("fragments: %v", d)
	}
	if _, has := a0["views"]; has {
		t.Fatalf("@include(if: false) kept views: %v", a0)
	}
	d = ok(gql(full, q, map[string]any{"withViews": true, "skipTitle": true}, "One"))
	a0 = d["articles"].([]any)[0].(map[string]any)
	if _, has := a0["title"]; has || a0["views"] != 7.0 {
		t.Fatalf("directives: %v", a0)
	}
	d = ok(gql(full, q, nil, "Two"))
	if len(d["articles"].([]any)) != 1 {
		t.Fatalf("operationName: %v", d)
	}
	for _, bad := range []string{q, `{ articles { ...Missing } }`, `{ articles { ...A } } fragment A on Article { ...A }`, `{ articles { title @nope } }`} {
		if r := gql(full, bad, map[string]any{"skipTitle": false}, ""); r.Body["errors"] == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
	// Introspection: the query GraphiQL sends.
	d = ok(gql(full, hgIntrospectionQuery, nil, "IntrospectionQuery"))
	sch := d["__schema"].(map[string]any)
	if sch["queryType"].(map[string]any)["name"] != "Query" || sch["mutationType"].(map[string]any)["name"] != "Mutation" {
		t.Fatalf("root types: %v", sch["queryType"])
	}
	types := map[string]map[string]any{}
	for _, x := range sch["types"].([]any) {
		m := x.(map[string]any)
		types[m["name"].(string)] = m
	}
	for _, name := range []string{"Article", "ArticleFiltersInput", "ArticleInput", "ComponentSharedSeo", "ArticleBlocksDynamicZone", "UploadFile", "StringFilterInput", "PublicationStatus", "String", "Pagination"} {
		if types[name] == nil {
			t.Errorf("type %s missing", name)
		}
	}
	if k := types["ArticleBlocksDynamicZone"]["kind"]; k != "UNION" || len(types["ArticleBlocksDynamicZone"]["possibleTypes"].([]any)) != 2 {
		t.Errorf("dynamic zone union: %v", types["ArticleBlocksDynamicZone"])
	}
	var fieldNames []string
	for _, f := range types["Article"]["fields"].([]any) {
		fieldNames = append(fieldNames, f.(map[string]any)["name"].(string))
	}
	joined := strings.Join(fieldNames, ",")
	if !strings.Contains(joined, "title") || !strings.Contains(joined, "documentId") || strings.Contains(joined, "internal") {
		t.Errorf("Article fields: %s", joined)
	}
	var mutNames []string
	for _, f := range types["Mutation"]["fields"].([]any) {
		mutNames = append(mutNames, f.(map[string]any)["name"].(string))
	}
	if !strings.Contains(strings.Join(mutNames, ","), "createArticle") || !strings.Contains(strings.Join(mutNames, ","), "unpublishHomepage") {
		t.Errorf("mutations: %v", mutNames)
	}
	if len(sch["directives"].([]any)) != 2 {
		t.Errorf("directives: %v", sch["directives"])
	}
	d = ok(gql(full, `{ __type(name: "Article") { kind name fields { name type { kind name ofType { kind name } } } } nope: __type(name: "Nope") { name } __typename }`, nil, ""))
	if d["__type"].(map[string]any)["kind"] != "OBJECT" || d["nope"] != nil || d["__typename"] != "Query" {
		t.Fatalf("__type: %v", d)
	}
	for _, f := range d["__type"].(map[string]any)["fields"].([]any) {
		m := f.(map[string]any)
		if m["name"] == "documentId" && m["type"].(map[string]any)["kind"] != "NON_NULL" {
			t.Errorf("documentId type: %v", m)
		}
	}
	// Delete through GraphQL.
	d = ok(gql(full, `mutation { deleteArticle(documentId: "`+doc+`") { documentId __typename } }`, nil, ""))
	if d["deleteArticle"].(map[string]any)["documentId"] != doc {
		t.Fatalf("deleteArticle: %v", d)
	}
	// The playground: admin screen and its endpoint (nonce required).
	_, body, _ := get(t, c, base+"/wp-admin/headlessgo?page=graphql")
	if !strings.Contains(body, "hg-gql-form") || !strings.Contains(body, "createArticle(data: ArticleInput!") {
		t.Fatal("playground screen")
	}
	nonce := regexp.MustCompile(`data-nonce="([^"]+)"`).FindStringSubmatch(body)
	if nonce == nil {
		t.Fatal("no nonce on the playground")
	}
	play := func(n string) (int, string) {
		req, _ := http.NewRequest("POST", base+"/wp-admin/headlessgo/graphql", strings.NewReader(`{"query":"{ articles { title } }"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-HG-Nonce", n)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	if code, raw := play(nonce[1]); code != 200 || !strings.Contains(raw, `"articles":[]`) {
		t.Fatalf("playground run: %d %s", code, raw)
	}
	if code, _ := play("bad"); code != 403 {
		t.Fatalf("playground without a nonce: %d", code)
	}
}

// hgIntrospectionQuery is GraphiQL's introspection query.
const hgIntrospectionQuery = `query IntrospectionQuery {
  __schema {
    queryType { name } mutationType { name } subscriptionType { name }
    types { ...FullType }
    directives { name description locations args { ...InputValue } }
  }
}
fragment FullType on __Type {
  kind name description
  fields(includeDeprecated: true) { name description args { ...InputValue } type { ...TypeRef } isDeprecated deprecationReason }
  inputFields { ...InputValue }
  interfaces { ...TypeRef }
  enumValues(includeDeprecated: true) { name description isDeprecated deprecationReason }
  possibleTypes { ...TypeRef }
}
fragment InputValue on __InputValue { name description type { ...TypeRef } defaultValue }
fragment TypeRef on __Type { kind name ofType { kind name ofType { kind name ofType { kind name ofType { kind name ofType { kind name ofType { kind name ofType { kind name } } } } } } } }`

func TestHeadlessGoFieldTypeChanges(t *testing.T) {
	base, _, srv := hgSite(t)
	ctx := context.Background()
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindComponent, DisplayName: "Spec", Category: "shared", Fields: []hgField{{Name: "weight", Type: "text"}}})
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Item", SingularName: "item", PluralName: "items", Fields: []hgField{
		{Name: "count", Type: "text"}, {Name: "flag", Type: "text"}, {Name: "price", Type: "number", Format: "decimal"},
		{Name: "size", Type: "enumeration", Enum: []string{"s", "m", "l"}}, {Name: "when", Type: "date"},
		{Name: "pics", Type: "media", Multiple: true}, {Name: "spec", Type: "component", Component: "shared.spec", Repeatable: true},
		{Name: "body", Type: "richtext"}}})
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	i1, i2 := hgImage(t, srv, "one"), hgImage(t, srv, "two")
	a := hgDocOf(t, hgCall(t, base, "POST", "/items", full, map[string]any{"data": map[string]any{"count": "12", "flag": "yes", "price": 9.6, "size": "m",
		"when": "2026-03-04", "pics": []any{i1, i2}, "spec": []any{map[string]any{"weight": "3.5"}, map[string]any{"weight": "heavy"}},
		"body": "First paragraph\n\nSecond paragraph"}}))
	b := hgDocOf(t, hgCall(t, base, "POST", "/items", full, map[string]any{"data": map[string]any{"count": "many", "flag": "maybe", "size": "s"}}))
	sc := hgCopySchema(srv.hgReg(ctx).Get("item"))
	set := func(name string, fn func(f *hgField)) {
		for i := range sc.Fields {
			if sc.Fields[i].Name == name {
				fn(&sc.Fields[i])
			}
		}
	}
	set("count", func(f *hgField) { f.Type, f.Format = "number", "integer" })
	set("flag", func(f *hgField) { f.Type = "boolean" })
	set("price", func(f *hgField) { f.Format = "integer" })
	set("size", func(f *hgField) { f.Enum, f.EnumRenames = []string{"small", "medium", "large"}, map[string]string{"m": "medium", "l": "large"} })
	set("when", func(f *hgField) { f.Type = "datetime" })
	set("pics", func(f *hgField) { f.Multiple = false })
	set("body", func(f *hgField) { f.Format = "markdown" })
	hgMustSchema(t, srv, sc)
	comp := hgCopySchema(srv.hgReg(ctx).Get("shared.spec"))
	comp.Fields[0].Type, comp.Fields[0].Format = "number", "decimal"
	hgMustSchema(t, srv, comp)
	if f := srv.hgReg(ctx).Get("item").Field("size"); f.EnumRenames != nil {
		t.Fatal("enumRenames was stored")
	}
	r := hgCall(t, base, "GET", "/items/"+a+"?populate=*", full, nil)
	got := r.data()
	if got["count"] != 12.0 || got["flag"] != true || got["price"] != 10.0 || got["size"] != "medium" || got["when"] != "2026-03-04T00:00:00.000Z" {
		t.Fatalf("converted scalars: %s", r.Raw)
	}
	if pics, ok := got["pics"].(map[string]any); !ok || pics["id"] != float64(i1) {
		t.Fatalf("multiple -> single media: %v", got["pics"])
	}
	if got["body"] != "First paragraph\n\nSecond paragraph" {
		t.Fatalf("blocks -> markdown: %q", got["body"])
	}
	spec := got["spec"].([]any)
	if spec[0].(map[string]any)["weight"] != 3.5 || spec[1].(map[string]any)["weight"] != nil {
		t.Fatalf("component field conversion: %v", spec)
	}
	r = hgCall(t, base, "GET", "/items/"+b, full, nil)
	if r.data()["count"] != nil || r.data()["flag"] != nil || r.data()["size"] != nil {
		t.Fatalf("values that cannot be converted are removed: %s", r.Raw)
	}
	// Back to text: numbers and booleans become their text.
	sc = hgCopySchema(srv.hgReg(ctx).Get("item"))
	set("count", func(f *hgField) { f.Type, f.Format = "text", "" })
	set("flag", func(f *hgField) { f.Type = "text" })
	hgMustSchema(t, srv, sc)
	if r := hgCall(t, base, "GET", "/items/"+a, full, nil); r.data()["count"] != "12" || r.data()["flag"] != "true" {
		t.Fatalf("back to text: %s", r.Raw)
	}
}

func TestHGConvertValue(t *testing.T) {
	txt := &hgField{Type: "text"}
	num := &hgField{Type: "number", Format: "decimal"}
	integer := &hgField{Type: "number", Format: "integer"}
	big := &hgField{Type: "number", Format: "biginteger"}
	boolean := &hgField{Type: "boolean"}
	enum := &hgField{Type: "enumeration", Enum: []string{"Red", "Blue"}}
	email := &hgField{Type: "email"}
	uid := &hgField{Type: "uid"}
	date := &hgField{Type: "date"}
	dt := &hgField{Type: "datetime"}
	js := &hgField{Type: "json"}
	blocks := &hgField{Type: "richtext", Format: "blocks"}
	rel := &hgField{Type: "relation", Relation: "manyToMany", Target: "a"}
	relOne := &hgField{Type: "relation", Relation: "manyToOne", Target: "a"}
	relB := &hgField{Type: "relation", Relation: "manyToMany", Target: "b"}
	cases := []struct {
		from, to *hgField
		in, out  any
		keep     bool
	}{
		{txt, num, " 3.25 ", 3.25, true},
		{txt, num, "abc", nil, false},
		{num, integer, 2.5, 3.0, true},
		{num, big, 12.0, "12", true},
		{txt, big, "123456789012345678901", "123456789012345678901", true},
		{num, txt, 1.5, "1.5", true},
		{txt, boolean, "Off", false, true},
		{num, boolean, 0.0, false, true},
		{boolean, num, true, 1.0, true},
		{boolean, txt, false, "false", true},
		{txt, enum, "blue", "Blue", true},
		{txt, enum, "green", nil, false},
		{txt, email, "a@b.test", "a@b.test", true},
		{txt, email, "not mail", nil, false},
		{txt, uid, "Hello World", "hello-world", true},
		{dt, date, "2026-05-06T10:00:00.000Z", "2026-05-06", true},
		{txt, dt, "yesterday", nil, false},
		{js, txt, map[string]any{"a": 1.0}, `{"a":1}`, true},
		{txt, js, "plain", "plain", true},
		{blocks, txt, []any{map[string]any{"type": "paragraph", "children": []any{map[string]any{"type": "text", "text": "Hi"}}}}, "Hi", true},
		{rel, relOne, []any{"x", "y"}, []any{"x"}, true},
		{rel, relB, []any{"x"}, nil, false},
		{rel, txt, []any{"x"}, nil, false},
		{&hgField{Type: "password"}, txt, "$argon2", nil, false},
	}
	for i, tc := range cases {
		out, keep := hgConvertValue(tc.from, tc.to, tc.in)
		if keep != tc.keep || (keep && fmt.Sprint(out) != fmt.Sprint(tc.out)) {
			t.Errorf("%d %s->%s %v: got %v %v, want %v %v", i, tc.from.Type, tc.to.Type, tc.in, out, keep, tc.out, tc.keep)
		}
	}
	if out, keep := hgConvertValue(txt, &hgField{Type: "richtext", Format: "blocks"}, "a\n\nb"); !keep || len(out.([]any)) != 2 {
		t.Errorf("text -> blocks: %v", out)
	}
	if hgNeedsConversion(&hgField{Type: "enumeration", Enum: []string{"a"}}, &hgField{Type: "enumeration", Enum: []string{"a", "b"}}) {
		t.Error("adding an enumeration value needs no conversion")
	}
	if !hgNeedsConversion(&hgField{Type: "media", Multiple: true}, &hgField{Type: "media"}) || hgNeedsConversion(&hgField{Type: "media"}, &hgField{Type: "media", Multiple: true}) {
		t.Error("media cardinality")
	}
}

func TestHGWebhookBackoff(t *testing.T) {
	saved := hgWebhookBase
	defer func() { hgWebhookBase = saved }()
	hgWebhookBase = 10 * time.Second
	want := []time.Duration{10 * time.Second, 40 * time.Second, 160 * time.Second, 640 * time.Second}
	for i, w := range want {
		if d := hgWebhookBackoff(i + 1); d != w {
			t.Errorf("backoff(%d) = %v, want %v", i+1, d, w)
		}
	}
	if d := hgWebhookBackoff(20); d != 6*time.Hour {
		t.Errorf("backoff cap: %v", d)
	}
}

func TestHGGraphQLParser(t *testing.T) {
	doc, err := hgGQLParse(`
		# comment
		query A($x: Int = 3, $y: [String!]! = ["a"], $z: Ord = DESC) @live { a: articles(pagination: {pageSize: $x}) { ...F ... on Article @skip(if: true) { id } ... { b } } }
		mutation B { createArticle(data: {title: "t\"q"}) { title } }
		fragment F on Article { title }`)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.ops) != 2 || doc.frags["F"] == nil || doc.frags["F"].on != "Article" {
		t.Fatalf("definitions: %+v", doc)
	}
	op, err := doc.operation("A")
	if err != nil || op.defaults["x"] != 3.0 || fmt.Sprint(op.defaults["y"]) != "[a]" || op.defaults["z"] != "DESC" {
		t.Fatalf("variable defaults: %v %v", op, err)
	}
	if _, err := doc.operation(""); err == nil {
		t.Error("several operations without operationName")
	}
	sel := op.sel[0].sel
	if sel[0].spread != "F" || sel[1].on != "Article" || len(sel[1].dirs) != 1 || sel[2].on != "" || sel[2].spread != "" {
		t.Fatalf("selection: %+v %+v %+v", sel[0], sel[1], sel[2])
	}
	ex := &hgGQLExec{vars: map[string]any{}}
	out, err := doc.expand(op.sel, ex, 0, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	inner := out[0].sel
	if len(inner) != 2 || inner[0].on != "Article" || inner[0].sel[0].name != "title" || inner[1].sel[0].name != "b" {
		t.Fatalf("expanded: %+v", inner)
	}
	m, _ := doc.operation("B")
	if v := m.sel[0].args["data"].(map[string]any)["title"]; v != `t"q` {
		t.Errorf("string escape: %v", v)
	}
	for _, bad := range []string{``, `query`, `{ a`, `fragment on X { a }`, `{ a(x: ) }`, `query Q($x Int) { a }`, `{ ... on { a } }`, `{ a } extra`} {
		if _, err := hgGQLParse(bad); err == nil {
			t.Errorf("parsed %q", bad)
		}
	}
}

func TestHGSortSQLAndInverseSync(t *testing.T) {
	reg := &hgRegistry{all: map[string]*hgSchema{}}
	add := func(sc *hgSchema) {
		reg.all[sc.UID] = sc
		reg.order = append(reg.order, sc)
	}
	add(&hgSchema{UID: "author", Kind: hgKindCollection, SingularName: "author", PluralName: "authors", Localized: true, Fields: []hgField{{Name: "name", Type: "text"},
		{Name: "posts", Type: "relation", Relation: "oneToMany", Target: "post", MappedBy: "author"}}})
	add(&hgSchema{UID: "post", Kind: hgKindCollection, SingularName: "post", PluralName: "posts", Fields: []hgField{{Name: "title", Type: "text"},
		{Name: "author", Type: "relation", Relation: "manyToOne", Target: "author", InversedBy: "posts"},
		{Name: "tags", Type: "relation", Relation: "manyToMany", Target: "author"}, {Name: "page", Type: "relation", Relation: "manyToOne", Target: "wp:page"}}})
	q := &hgQuery{reg: reg, sql: &hgSQL{}, status: "published", locale: "en"}
	order, err := q.orderBy(reg.Get("post"), []hgSortKey{{Field: "author.name", Desc: true}, {Field: "page.title"}}, "e")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(order, "(SELECT lower(s1.published_data->>'name') FROM hg_entries s1 WHERE s1.type_uid = $1 AND s1.document_id = (e.published_data->'author'->>0) AND s1.locale = $2 AND s1.published_data IS NOT NULL") ||
		!strings.Contains(order, "DESC NULLS LAST") || !strings.Contains(order, "FROM posts w2") {
		t.Fatalf("order by: %s", order)
	}
	q = &hgQuery{reg: reg, sql: &hgSQL{}, status: "draft", locale: "en"}
	if _, err := q.orderBy(reg.Get("post"), []hgSortKey{{Field: "tags.name"}}, "e"); err == nil {
		t.Error("sorting by a to-many relation")
	}
	if _, err := q.orderBy(reg.Get("author"), []hgSortKey{{Field: "posts.title"}}, "e"); err == nil {
		t.Error("sorting by a to-many inverse relation")
	}
	f, err := q.filters(reg.Get("author"), map[string]any{"posts": map[string]any{"title": map[string]any{"$eq": "x"}}}, "e.data", "e")
	if err != nil || !strings.Contains(f, "o1.data->'author' @> jsonb_build_array(e.document_id)") {
		t.Fatalf("inverse filter: %s %v", f, err)
	}
	// Sync: renaming the owning field renames mappedBy; a new owner creates the inverse field.
	post := hgCopySchema(reg.Get("post"))
	post.Fields[1].Name = "writer"
	others, err := hgSyncInverse(reg, post, map[string]string{"author": "writer"})
	if err != nil || len(others) != 1 || others[0].Field("posts").MappedBy != "writer" {
		t.Fatalf("sync rename: %v %+v", err, others)
	}
	post = hgCopySchema(reg.Get("post"))
	post.Fields[2].InversedBy = "tagged"
	others, err = hgSyncInverse(reg, post, nil)
	if err != nil || len(others) != 1 || others[0].Field("tagged") == nil || others[0].Field("tagged").Relation != "manyToMany" {
		t.Fatalf("sync new inverse: %v %+v", err, others)
	}
	post = hgCopySchema(reg.Get("post"))
	post.Fields[1].Relation = "oneToOne"
	others, _ = hgSyncInverse(reg, post, nil)
	if len(others) != 1 || others[0].Field("posts").Relation != "oneToOne" {
		t.Fatalf("sync kind: %+v", others)
	}
	if hgMirror("oneToMany") != "manyToOne" || hgMirror("manyToOne") != "oneToMany" || hgMirror("manyToMany") != "manyToMany" {
		t.Error("mirror")
	}
}
