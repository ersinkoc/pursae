package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Tests of the Web Builder REST API (web-builder/v2). The unit tests run without a database; the
// integration tests need WPGO_TEST_ADMIN_DB / WPGO_TEST_DB like the rest of the package.

// --- unit ------------------------------------------------------------------------------------------

func TestWebBuilderCSS2FamilyParse(t *testing.T) {
	cases := []struct {
		in             string
		ok             bool
		family         string
		normal, italic []int
	}{
		{"Inter", true, "Inter", []int{400}, nil},
		{"Inter:wght@400;700", true, "Inter", []int{400, 700}, nil},
		{"Inter:ital,wght@0,400;1,700", true, "Inter", []int{400}, []int{700}},
		{"Inter:wght@450;1000;50", true, "Inter", []int{400}, nil}, // snapped / out of range ignored → default
		{"Inter:wght@100..300", true, "Inter", []int{100, 200, 300}, nil},
		{"Space Grotesk:wght@900", true, "Space Grotesk", []int{700}, nil}, // snapped to the shipped weights
		{"Not A Font:wght@400", false, "", nil, nil},
		{"../../etc/passwd", false, "", nil, nil},
	}
	keys := func(m map[int]bool) []int {
		var out []int
		for w := 100; w <= 900; w += 100 {
			if m[w] {
				out = append(out, w)
			}
		}
		return out
	}
	for _, tc := range cases {
		req, ok := wbParseCSS2Family(tc.in)
		if ok != tc.ok {
			t.Errorf("%q: ok=%v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if req.family != tc.family || !equalInts(keys(req.normal), tc.normal) || !equalInts(keys(req.italic), tc.italic) {
			t.Errorf("%q: %s %v %v, want %s %v %v", tc.in, req.family, keys(req.normal), keys(req.italic), tc.family, tc.normal, tc.italic)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestWebBuilderFontFacesFilter(t *testing.T) {
	css := `/* Inter */
/* latin */
@font-face {
  font-family: 'Inter';
  font-style: normal;
  font-weight: 400;
  src: url(../fonts/inter-aaaa.woff2) format('woff2');
}
/* latin */
@font-face {
  font-family: 'Inter';
  font-style: normal;
  font-weight: 700;
  src: url(../fonts/inter-bbbb.woff2) format('woff2');
}
/* latin */
@font-face {
  font-family: 'Inter';
  font-style: italic;
  font-weight: 700;
  src: url(../fonts/inter-cccc.woff2) format('woff2');
}`
	req, _ := wbParseCSS2Family("Inter:ital,wght@0,700;1,700")
	out := wbFontFaces(css, req, "https://example.test/wp-content/uploads/web-builder/google-fonts/fonts/")
	if strings.Contains(out, "inter-aaaa") || !strings.Contains(out, "inter-bbbb") || !strings.Contains(out, "inter-cccc") {
		t.Fatalf("faces: %s", out)
	}
	if strings.Contains(out, "../fonts/") || !strings.Contains(out, "url(https://example.test/wp-content/uploads/web-builder/google-fonts/fonts/inter-bbbb.woff2)") {
		t.Fatalf("src not made absolute: %s", out)
	}
}

func TestWebBuilderFeatures(t *testing.T) {
	got := wbFeatures([]webbuilder.Feature{"tabs", "menu", "evil", "menu", "acc"})
	if strings.Join(got, ",") != "acc,menu,tabs" {
		t.Fatalf("features %v", got)
	}
}

// --- integration helpers ---------------------------------------------------------------------------

type wbrClient struct {
	t     *testing.T
	c     *http.Client
	base  string
	nonce string
}

func newWBRClient(t *testing.T, c *http.Client, base string) *wbrClient {
	t.Helper()
	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	return &wbrClient{t: t, c: c, base: base, nonce: strings.TrimSpace(nonce)}
}

// do sends a web-builder/v2 request (path below /wp-json) with the nonce and the API header unless
// hdr overrides them ("" deletes). body is JSON-encoded unless it is []byte.
func (w *wbrClient) do(method, path string, body any, hdr map[string]string) (int, any, http.Header) {
	w.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			w.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, w.base+"/wp-json"+path, rd)
	h := map[string]string{"X-WP-Nonce": w.nonce, webbuilder.APIHeader: strconv.Itoa(webbuilder.APIVersion)}
	if body != nil {
		h["Content-Type"] = "application/json"
	}
	for k, v := range hdr {
		h[k] = v
	}
	for k, v := range h {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := w.c.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out any
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(raw, &out)
	} else {
		out = string(raw)
	}
	return resp.StatusCode, out, resp.Header
}

func wbrObj(v any) map[string]any { m, _ := v.(map[string]any); return m }

func wbrCode(v any) string { s, _ := wbrObj(v)["code"].(string); return s }

func wbrData(v any) map[string]any { return wbrObj(wbrObj(v)["data"]) }

// wbrFixture loads a renderStatic fixture of internal/webbuilder (doc + RenderResult).
func wbrFixture(t *testing.T, name string) (map[string]any, map[string]any) {
	t.Helper()
	read := func(f string) map[string]any {
		b, err := os.ReadFile(filepath.Join("..", "webbuilder", "testdata", "render", f))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	return read(name + ".doc.json"), read(name + ".render.json")
}

func wbrPublishBody(doc, render map[string]any, baseRev int, status string, page map[string]any) map[string]any {
	if page == nil {
		page = map[string]any{"title": "Landing", "slug": "landing", "description": "A landing page", "indexable": true}
	}
	return map[string]any{"baseRev": baseRev, "status": status, "doc": doc, "page": page, "render": render, "utilityCss": ".p-4{padding:1rem}"}
}

func wbrStart(t *testing.T) (*Server, string, *wbrClient) {
	t.Helper()
	ts, c, srv := newTestServer(t, true)
	installAndLogin(t, ts, c)
	if !srv.wbOn() {
		t.Fatal("Pursae Web Builder is off")
	}
	return srv, ts.URL, newWBRClient(t, c, ts.URL)
}

func wbrUser(t *testing.T, srv *Server, base, login, role string) *wbrClient {
	t.Helper()
	asCreateUser(t, srv, login, role, asStrongPass)
	cc := asNewClient()
	if code, _, _ := asLogin(t, cc, base, "/wp-login", login, asStrongPass); code != http.StatusFound {
		t.Fatalf("login %s: %d", login, code)
	}
	return newWBRClient(t, cc, base)
}

func wbrPost(t *testing.T, srv *Server, typ, title, status string, author int64) *store.Post {
	t.Helper()
	p := &store.Post{Type: typ, Title: title, Slug: strings.ToLower(strings.ReplaceAll(title, " ", "-")), Status: status, AuthorID: author}
	if err := srv.store.InsertPost(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func wbrDocPath(id int64, rest string) string {
	return "/web-builder/v2/documents/" + itoa64(id) + rest
}

// --- integration -----------------------------------------------------------------------------------

func TestWebBuilderRESTAuth(t *testing.T) {
	srv, base, admin := wbrStart(t)
	page := wbrPost(t, srv, "page", "Auth Page", "draft", 1)
	doc := wbrDocPath(page.ID, "")

	if code, v, _ := admin.do(http.MethodGet, doc, nil, nil); code != http.StatusOK || wbrObj(v)["postId"] != float64(page.ID) {
		t.Fatalf("admin GET: %d %v", code, v)
	}
	// No nonce: the cookie alone does not authenticate (CSRF).
	if code, v, _ := admin.do(http.MethodGet, doc, nil, map[string]string{"X-WP-Nonce": ""}); code != http.StatusUnauthorized {
		t.Errorf("no nonce: %d %v", code, v)
	}
	if code, v, _ := admin.do(http.MethodGet, doc, nil, map[string]string{"X-WP-Nonce": "bogus"}); code != http.StatusForbidden || wbrCode(v) != "rest_cookie_invalid_nonce" {
		t.Errorf("bad nonce: %d %v", code, v)
	}
	// Anonymous client.
	anon := &wbrClient{t: t, c: asNewClient(), base: base}
	if code, _, _ := anon.do(http.MethodPut, wbrDocPath(page.ID, "/autosave"), map[string]any{"doc": map[string]any{}}, nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous autosave: %d", code)
	}
	// API handshake.
	for _, h := range []string{"", "0", "2"} {
		code, v, _ := admin.do(http.MethodGet, doc, nil, map[string]string{webbuilder.APIHeader: h})
		if code != http.StatusConflict || wbrCode(v) != webbuilder.ErrAPIMismatch || wbrData(v)["reload"] != true || wbrData(v)["apiVersion"] != float64(webbuilder.APIVersion) {
			t.Errorf("X-WB-Api %q: %d %v", h, code, v)
		}
	}
	// A subscriber may not touch documents, the library or prefs.
	sub := wbrUser(t, srv, base, "sub", "subscriber")
	for _, rq := range []struct{ m, p string }{{http.MethodGet, doc}, {http.MethodGet, "/web-builder/v2/library"}, {http.MethodGet, "/web-builder/v2/prefs"},
		{http.MethodPost, wbrDocPath(page.ID, "/publish")}} {
		if code, v, _ := sub.do(rq.m, rq.p, map[string]any{}, nil); code != http.StatusForbidden || wbrCode(v) != webbuilder.ErrForbidden {
			t.Errorf("subscriber %s %s: %d %v", rq.m, rq.p, code, v)
		}
	}
	// Post types and states.
	if code, v, _ := admin.do(http.MethodGet, wbrDocPath(999999, ""), nil, nil); code != http.StatusNotFound || wbrCode(v) != webbuilder.ErrNotFound {
		t.Errorf("missing post: %d %v", code, v)
	}
	block := wbrPost(t, srv, "wp_block", "Pattern", "publish", 1)
	if code, v, _ := admin.do(http.MethodGet, wbrDocPath(block.ID, ""), nil, nil); code != http.StatusNotFound || wbrCode(v) != webbuilder.ErrNotBuilder {
		t.Errorf("wp_block: %d %v", code, v)
	}
	trashed := wbrPost(t, srv, "page", "Trashed", "draft", 1)
	_ = srv.store.TrashPost(context.Background(), trashed.ID)
	if code, v, _ := admin.do(http.MethodGet, wbrDocPath(trashed.ID, ""), nil, nil); code != http.StatusNotFound || wbrCode(v) != webbuilder.ErrNotFound {
		t.Errorf("trashed: %d %v", code, v)
	}
	// Plugin off: the namespace is gone.
	if err := srv.settings.Set(context.Background(), map[string]any{webbuilder.PluginOption: false}); err != nil {
		t.Fatal(err)
	}
	if code, v, _ := admin.do(http.MethodGet, doc, nil, nil); code != http.StatusNotFound || wbrCode(v) != "rest_no_route" {
		t.Errorf("plugin off: %d %v", code, v)
	}
}

func TestWebBuilderRESTPublish(t *testing.T) {
	srv, _, admin := wbrStart(t)
	ctx := context.Background()
	page := wbrPost(t, srv, "page", "Old Title", "draft", 1)
	doc, render := wbrFixture(t, "initial")

	// Autosave: the user's draft; the live page is untouched.
	code, v, _ := admin.do(http.MethodPut, wbrDocPath(page.ID, "/autosave"), map[string]any{"doc": doc, "page": map[string]any{"title": "Draft title"}, "baseRev": 0}, nil)
	if code != http.StatusOK || wbrObj(v)["rev"] != float64(0) || wbrObj(v)["savedAt"] == "" {
		t.Fatalf("autosave: %d %v", code, v)
	}
	_, v, _ = admin.do(http.MethodGet, wbrDocPath(page.ID, ""), nil, nil)
	if d := wbrObj(wbrObj(v)["draft"]); d == nil || wbrObj(d["page"])["title"] != "Draft title" || wbrObj(v)["doc"] != nil {
		t.Fatalf("document after autosave: %v", v)
	}
	if lock, _ := srv.store.GetPost(ctx, page.ID); !strings.HasSuffix(lock.Meta[webbuilder.EditLockMeta].(string), ":1") {
		t.Errorf("autosave did not take the edit lock: %v", lock.Meta[webbuilder.EditLockMeta])
	}
	// Invalid doc → 422.
	if code, v, _ := admin.do(http.MethodPut, wbrDocPath(page.ID, "/autosave"), map[string]any{"doc": map[string]any{"sections": []any{map[string]any{"id": "x", "kind": "evil"}}}}, nil); code != http.StatusUnprocessableEntity || wbrCode(v) != webbuilder.ErrInvalidDoc {
		t.Errorf("invalid doc: %d %v", code, v)
	}

	// Publish.
	pageSettings := map[string]any{"title": "Landing Page", "slug": "landing", "description": "Our landing page", "indexable": false}
	code, v, _ = admin.do(http.MethodPost, wbrDocPath(page.ID, "/publish"), wbrPublishBody(doc, render, 0, "publish", pageSettings), nil)
	if code != http.StatusOK {
		t.Fatalf("publish: %d %v", code, v)
	}
	out := wbrObj(v)
	if out["rev"] != float64(1) || out["status"] != "publish" || !strings.Contains(out["permalink"].(string), "landing") || out["revisionId"] == float64(0) {
		t.Fatalf("publish answer: %v", out)
	}
	if st, _ := out["stripped"].([]any); st == nil {
		t.Errorf("stripped must be an array: %v", out["stripped"])
	}
	p, _ := srv.store.GetPost(ctx, page.ID)
	var parsed webbuilder.DocState
	rawDoc, _ := json.Marshal(doc)
	_ = json.Unmarshal(rawDoc, &parsed)
	if p.Title != "Landing Page" || p.Slug != "landing" || p.Excerpt != "Our landing page" || p.Status != "publish" {
		t.Errorf("post fields: %q %q %q %q", p.Title, p.Slug, p.Excerpt, p.Status)
	}
	if p.Content != webbuilder.FallbackHTML(&parsed) || !strings.Contains(p.Content, "<h1") {
		t.Errorf("posts.content is not the fallback HTML:\n%.400s", p.Content)
	}
	if p.Meta[webbuilder.PostMarkerMeta] != "1" || seogoMeta(p.Meta, "noindex") != "noindex" || seogoMeta(p.Meta, "description") != "Our landing page" {
		t.Errorf("meta: %v", p.Meta)
	}
	row, err := srv.store.WebBuilder().Doc(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Rev != 1 || row.Renderer != webbuilder.RendererVersion || row.ContentHash != store.WebBuilderContentHash(p.Content) || row.Detached ||
		!strings.HasPrefix(row.HTML, "<div") || !strings.Contains(row.CSS, "padding:1rem") || strings.Join(row.Features, ",") != "acc,menu" ||
		row.SnapshotUser != 1 || !row.SnapshotUnfiltered || !strings.Contains(string(row.Fonts), "Inter") {
		t.Errorf("docs row: rev %d renderer %d hash %v detached %v features %v user %d fonts %s css %q", row.Rev, row.Renderer,
			row.ContentHash == store.WebBuilderContentHash(p.Content), row.Detached, row.Features, row.SnapshotUser, row.Fonts, row.CSS)
	}
	var storedPage webbuilder.PageSettings
	if json.Unmarshal(row.Page, &storedPage) != nil || storedPage.Indexable || storedPage.Title != "Landing Page" || storedPage.Slug != "landing" {
		t.Errorf("page json: %s", row.Page)
	}
	revID := int64(out["revisionId"].(float64))
	var revPost int64
	if err := srv.store.Pool.QueryRow(ctx, `SELECT post_id FROM post_revisions WHERE id = $1`, revID).Scan(&revPost); err != nil || revPost != page.ID {
		t.Errorf("core revision %d: %v %d", revID, err, revPost)
	}
	if n, _ := srv.store.WebBuilder().CountRevisions(ctx, page.ID); n != 1 {
		t.Errorf("builder revisions: %d", n)
	}
	if _, err := srv.store.WebBuilder().Draft(ctx, page.ID, 1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("draft not deleted: %v", err)
	}

	// Stale rev → 409 with the stored state.
	code, v, _ = admin.do(http.MethodPost, wbrDocPath(page.ID, "/publish"), wbrPublishBody(doc, render, 0, "publish", nil), nil)
	if code != http.StatusConflict || wbrCode(v) != webbuilder.ErrRevConflict || wbrData(v)["rev"] != float64(1) || wbrObj(wbrData(v)["modifiedBy"])["id"] != float64(1) {
		t.Errorf("stale rev: %d %v", code, v)
	}
	// Stale editor bundle → 422 bad_renderer.
	stale := map[string]any{}
	for k, x := range render {
		stale[k] = x
	}
	stale["renderer"] = 99
	code, v, _ = admin.do(http.MethodPost, wbrDocPath(page.ID, "/publish"), wbrPublishBody(doc, stale, 1, "publish", nil), nil)
	if code != http.StatusUnprocessableEntity || !strings.Contains(mustJSON(v), webbuilder.VErrRenderer) {
		t.Errorf("renderer mismatch: %d %v", code, v)
	}
	// Oversize body → 413.
	big := wbrPublishBody(doc, render, 1, "publish", nil)
	big["pad"] = strings.Repeat("x", webbuilder.MaxDocBytes+10)
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(page.ID, "/publish"), big, nil); code != http.StatusRequestEntityTooLarge || wbrCode(v) != webbuilder.ErrTooLarge || wbrData(v)["limit"] != float64(webbuilder.MaxDocBytes) {
		t.Errorf("oversize: %d %v", code, v)
	}
	// Detached page: publish needs reattach.
	if err := srv.store.WebBuilder().SetDetached(ctx, page.ID, true); err != nil {
		t.Fatal(err)
	}
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(page.ID, "/publish"), wbrPublishBody(doc, render, 1, "publish", nil), nil); code != http.StatusConflict || wbrCode(v) != webbuilder.ErrDetached {
		t.Errorf("detached: %d %v", code, v)
	}
	second := wbrPublishBody(doc, render, 1, "publish", nil)
	second["reattach"] = true
	code, v, _ = admin.do(http.MethodPost, wbrDocPath(page.ID, "/publish"), second, nil)
	if code != http.StatusOK || wbrObj(v)["rev"] != float64(2) {
		t.Fatalf("second publish: %d %v", code, v)
	}
	if row, _ := srv.store.WebBuilder().Doc(ctx, page.ID); row.Detached || row.Rev != 2 {
		t.Errorf("after reattach: detached %v rev %d", row.Detached, row.Rev)
	}
	if p, _ := srv.store.GetPost(ctx, page.ID); seogoMeta(p.Meta, "noindex") == "noindex" || p.Title != "Landing" {
		t.Errorf("second publish meta/title: %q %v", p.Title, p.Meta)
	}

	// Revisions: list, get, restore.
	code, v, h := admin.do(http.MethodGet, wbrDocPath(page.ID, "/revisions"), nil, nil)
	list, _ := v.([]any)
	if code != http.StatusOK || len(list) != 2 || h.Get("X-WP-Total") != "2" || h.Get("X-WP-TotalPages") != "1" {
		t.Fatalf("revisions: %d %v %v", code, v, h)
	}
	first := wbrObj(list[1])
	if first["rev"] != float64(1) || wbrObj(first["author"])["name"] == "" || wbrObj(list[0])["rev"] != float64(2) {
		t.Errorf("revision order/author: %v", list)
	}
	rid := itoa64(int64(first["id"].(float64)))
	code, v, _ = admin.do(http.MethodGet, wbrDocPath(page.ID, "/revisions/"+rid), nil, nil)
	if code != http.StatusOK || wbrObj(v)["rev"] != float64(1) || wbrObj(wbrObj(v)["page"])["title"] != "Landing Page" || wbrObj(v)["doc"] == nil {
		t.Errorf("revision: %d %v", code, v)
	}
	if code, _, _ := admin.do(http.MethodGet, wbrDocPath(page.ID, "/revisions/999999"), nil, nil); code != http.StatusNotFound {
		t.Errorf("missing revision: %d", code)
	}
	code, v, _ = admin.do(http.MethodPost, wbrDocPath(page.ID, "/revisions/"+rid+"/restore"), map[string]any{"baseRev": 2}, nil)
	if code != http.StatusOK || wbrObj(v)["baseRev"] != float64(2) || wbrObj(wbrObj(v)["page"])["title"] != "Landing Page" {
		t.Fatalf("restore: %d %v", code, v)
	}
	_, v, _ = admin.do(http.MethodGet, wbrDocPath(page.ID, ""), nil, nil)
	if d := wbrObj(wbrObj(v)["draft"]); d == nil || d["baseRev"] != float64(2) || wbrObj(v)["rev"] != float64(2) {
		t.Errorf("document after restore: %v", v)
	}
	if row, _ := srv.store.WebBuilder().Doc(ctx, page.ID); row.Rev != 2 {
		t.Errorf("restore changed the live doc: rev %d", row.Rev)
	}
	// DELETE autosave drops the draft.
	if code, v, _ := admin.do(http.MethodDelete, wbrDocPath(page.ID, "/autosave"), nil, nil); code != http.StatusOK || wbrObj(v)["deleted"] != true {
		t.Errorf("delete autosave: %d %v", code, v)
	}
	if _, err := srv.store.WebBuilder().Draft(ctx, page.ID, 1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("draft still there: %v", err)
	}
}

func TestWebBuilderRESTContributorForged(t *testing.T) {
	srv, base, _ := wbrStart(t)
	ctx := context.Background()
	contrib := wbrUser(t, srv, base, "contrib", "contributor")
	u, _ := srv.store.UserByLoginOrEmail(ctx, "contrib")
	post := wbrPost(t, srv, "post", "Contributor Post", "draft", u.ID)
	doc, render := wbrFixture(t, "initial")

	// A forged request: an html section and custom CSS in the doc, script / handlers / a srcdoc
	// embed in the snapshot, section CSS and @import in the CSS.
	sections := doc["sections"].([]any)
	htmlSec := map[string]any{}
	for k, x := range wbrObj(sections[len(sections)-2]) {
		htmlSec[k] = x
	}
	htmlSec["id"], htmlSec["kind"], htmlSec["content"] = "s_evilhtml", "html", map[string]any{"html": "<script>alert(1)</script>"}
	hero := wbrObj(sections[1])
	style := wbrObj(hero["style"])
	style["css"] = "selector{background:url(javascript:alert(1))}"
	doc["sections"] = append(sections, htmlSec)
	h := render["html"].(string)
	i := strings.LastIndex(h, "</div>")
	render["html"] = h[:i] + `<script>alert(1)</script><img src="x.png" onerror="alert(2)"><a href="javascript:alert(3)">x</a><iframe data-wb-embed="e1" sandbox="allow-scripts" srcdoc="&lt;script&gt;parent.alert(4)&lt;/script&gt;"></iframe>` + h[i:]
	render["css"] = "#x{color:red}"
	body := wbrPublishBody(doc, render, 0, "publish", nil)
	body["utilityCss"] = "@import url(https://evil.test/x.css);.p-4{padding:1rem}"

	code, v, _ := contrib.do(http.MethodPost, wbrDocPath(post.ID, "/publish"), body, nil)
	if code != http.StatusOK {
		t.Fatalf("contributor publish: %d %v", code, v)
	}
	if wbrObj(v)["status"] != "pending" {
		t.Errorf("contributor status %v, want pending", wbrObj(v)["status"])
	}
	stripped := mustJSON(wbrObj(v)["stripped"])
	for _, want := range []string{webbuilder.StrippedHTMLSections, webbuilder.StrippedCustomCSS, webbuilder.StrippedMarkup, webbuilder.StrippedCSS} {
		if !strings.Contains(stripped, want) {
			t.Errorf("stripped %s lacks %s", stripped, want)
		}
	}
	if p, _ := srv.store.GetPost(ctx, post.ID); p.Status != "pending" {
		t.Errorf("post status %q", p.Status)
	}
	row, err := srv.store.WebBuilder().Doc(ctx, post.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<script", "onerror", "javascript:", "srcdoc", "alert("} {
		if strings.Contains(row.HTML, bad) {
			t.Errorf("stored snapshot contains %q", bad)
		}
	}
	if strings.Contains(row.CSS, "@import") || strings.Contains(row.CSS, "#x{") || !strings.Contains(row.CSS, "padding:1rem") {
		t.Errorf("stored css %q", row.CSS)
	}
	if strings.Contains(string(row.Doc), "s_evilhtml") || strings.Contains(string(row.Doc), "javascript:") || strings.Contains(string(row.Doc), `"css"`) {
		t.Errorf("stored doc keeps privileged parts: %.300s", row.Doc)
	}
	if row.SnapshotUnfiltered || row.SnapshotUser != u.ID {
		t.Errorf("snapshot user %d unfiltered %v", row.SnapshotUser, row.SnapshotUnfiltered)
	}
	// A contributor cannot open the administrator's page.
	adminPage := wbrPost(t, srv, "page", "Admin Page", "draft", 1)
	if code, _, _ := contrib.do(http.MethodGet, wbrDocPath(adminPage.ID, ""), nil, nil); code != http.StatusForbidden {
		t.Errorf("contributor on admin page: %d", code)
	}
}

func TestWebBuilderRESTLock(t *testing.T) {
	srv, base, admin := wbrStart(t)
	editor := wbrUser(t, srv, base, "ed", "editor")
	page := wbrPost(t, srv, "page", "Locked Page", "draft", 1)
	doc, render := wbrFixture(t, "initial")

	code, v, _ := admin.do(http.MethodPost, wbrDocPath(page.ID, "/lock"), map[string]any{}, nil)
	if code != http.StatusOK || wbrObj(v)["lock"] != nil || wbrObj(v)["expires"] == "" {
		t.Fatalf("admin lock: %d %v", code, v)
	}
	code, v, _ = editor.do(http.MethodPost, wbrDocPath(page.ID, "/lock"), map[string]any{}, nil)
	if code != http.StatusOK || wbrObj(wbrObj(wbrObj(v)["lock"])["user"])["id"] != float64(1) {
		t.Fatalf("editor lock while held: %d %v", code, v)
	}
	if _, v, _ := editor.do(http.MethodGet, wbrDocPath(page.ID, ""), nil, nil); wbrObj(v)["lock"] == nil {
		t.Errorf("document lacks the other user's lock: %v", v)
	}
	code, v, _ = editor.do(http.MethodPost, wbrDocPath(page.ID, "/publish"), wbrPublishBody(doc, render, 0, "publish", nil), nil)
	if code != http.StatusConflict || wbrCode(v) != webbuilder.ErrLocked || wbrData(v)["lock"] == nil {
		t.Fatalf("publish while locked: %d %v", code, v)
	}
	if code, v, _ := editor.do(http.MethodPost, wbrDocPath(page.ID, "/lock"), map[string]any{"take": true}, nil); code != http.StatusOK || wbrObj(v)["lock"] != nil {
		t.Fatalf("take over: %d %v", code, v)
	}
	if code, v, _ := editor.do(http.MethodPost, wbrDocPath(page.ID, "/publish"), wbrPublishBody(doc, render, 0, "publish", nil), nil); code != http.StatusOK {
		t.Fatalf("publish after take over: %d %v", code, v)
	}
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(page.ID, "/publish"), wbrPublishBody(doc, render, 1, "publish", nil), nil); code != http.StatusConflict || wbrCode(v) != webbuilder.ErrLocked {
		t.Errorf("admin publish after losing the lock: %d %v", code, v)
	}
}

func TestWebBuilderRESTLibraryAndPrefs(t *testing.T) {
	srv, base, admin := wbrStart(t)
	contrib := wbrUser(t, srv, base, "contrib", "contributor")
	doc, _ := wbrFixture(t, "initial")
	sec := wbrObj(doc["sections"].([]any)[1])

	code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/library", map[string]any{"name": " My hero ", "section": sec}, nil)
	if code != http.StatusCreated || wbrObj(v)["name"] != "My hero" || wbrObj(wbrObj(v)["author"])["id"] != float64(1) {
		t.Fatalf("library create: %d %v", code, v)
	}
	adminItem, _ := wbrObj(v)["id"].(string)
	if _, err := strconv.ParseInt(adminItem, 10, 64); err != nil {
		t.Fatalf("library id must be a decimal string: %v", wbrObj(v)["id"])
	}
	code, v, _ = contrib.do(http.MethodPost, "/web-builder/v2/library", map[string]any{"name": "Mine", "section": sec}, nil)
	if code != http.StatusCreated {
		t.Fatalf("contributor create: %d %v", code, v)
	}
	contribItem := wbrObj(v)["id"].(string)
	// A contributor may not save an html section.
	htmlSec := map[string]any{}
	for k, x := range sec {
		htmlSec[k] = x
	}
	htmlSec["id"], htmlSec["kind"], htmlSec["content"] = "s_h", "html", map[string]any{"html": "<b>x</b>"}
	if code, v, _ := contrib.do(http.MethodPost, "/web-builder/v2/library", map[string]any{"name": "HTML", "section": htmlSec}, nil); code != http.StatusForbidden {
		t.Errorf("contributor html section: %d %v", code, v)
	}
	if code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/library", map[string]any{"name": "", "section": sec}, nil); code != http.StatusUnprocessableEntity {
		t.Errorf("empty name: %d %v", code, v)
	}
	if code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/library", map[string]any{"name": "Bad", "section": map[string]any{"id": "!", "kind": "nope"}}, nil); code != http.StatusUnprocessableEntity || wbrCode(v) != webbuilder.ErrInvalidDoc {
		t.Errorf("invalid section: %d %v", code, v)
	}
	code, v, _ = admin.do(http.MethodGet, "/web-builder/v2/library", nil, nil)
	if list, _ := v.([]any); code != http.StatusOK || len(list) != 2 || wbrObj(list[0])["id"] != contribItem {
		t.Fatalf("library list: %d %v", code, v)
	}
	if code, _, _ := contrib.do(http.MethodDelete, "/web-builder/v2/library/"+adminItem, nil, nil); code != http.StatusForbidden {
		t.Errorf("contributor deletes the admin's item: %d", code)
	}
	if code, v, _ := contrib.do(http.MethodDelete, "/web-builder/v2/library/"+contribItem, nil, nil); code != http.StatusOK || wbrObj(v)["deleted"] != true {
		t.Errorf("contributor deletes own item: %d %v", code, v)
	}
	if code, _, _ := admin.do(http.MethodDelete, "/web-builder/v2/library/"+adminItem, nil, nil); code != http.StatusOK {
		t.Errorf("admin delete: %d", code)
	}
	if code, _, _ := admin.do(http.MethodDelete, "/web-builder/v2/library/"+adminItem, nil, nil); code != http.StatusNotFound {
		t.Errorf("delete again: %d", code)
	}

	// Prefs.
	if code, v, _ := admin.do(http.MethodGet, "/web-builder/v2/prefs", nil, nil); code != http.StatusOK || len(wbrObj(v)) != 0 {
		t.Fatalf("prefs empty: %d %v", code, v)
	}
	if code, v, _ := admin.do(http.MethodPut, "/web-builder/v2/prefs", map[string]any{"pursae-builder:recent-fonts": []string{"Inter"}, "pursae-builder:layout-width": 1280}, nil); code != http.StatusOK || wbrObj(v)["pursae-builder:layout-width"] != float64(1280) {
		t.Fatalf("prefs put: %d %v", code, v)
	}
	if code, v, _ := admin.do(http.MethodPut, "/web-builder/v2/prefs", map[string]any{"pursae-builder:layout-width": nil}, nil); code != http.StatusOK || wbrObj(v)["pursae-builder:layout-width"] != nil || wbrObj(v)["pursae-builder:recent-fonts"] == nil {
		t.Fatalf("prefs merge/delete: %d %v", code, v)
	}
	if code, _, _ := admin.do(http.MethodPut, "/web-builder/v2/prefs", map[string]any{"Bad Key": 1}, nil); code != http.StatusBadRequest {
		t.Errorf("bad key: %d", code)
	}
	if code, v, _ := admin.do(http.MethodPut, "/web-builder/v2/prefs", map[string]any{"big": strings.Repeat("x", webbuilder.MaxPrefsBytes)}, nil); code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize prefs: %d %v", code, v)
	}
	if _, v, _ := contrib.do(http.MethodGet, "/web-builder/v2/prefs", nil, nil); len(wbrObj(v)) != 0 {
		t.Errorf("prefs are per user: %v", v)
	}
	if _, v, _ := admin.do(http.MethodGet, "/web-builder/v2/prefs", nil, nil); wbrObj(v)["pursae-builder:recent-fonts"] == nil {
		t.Errorf("prefs not persisted: %v", v)
	}
}

func TestWebBuilderRESTFonts(t *testing.T) {
	srv, base, admin := wbrStart(t)
	var fetches atomic.Int32
	prev := gfontFetch
	t.Cleanup(func() { gfontFetch = prev })
	gfontFetch = func(_ context.Context, raw string) ([]byte, error) {
		fetches.Add(1)
		if strings.Contains(raw, "fonts.googleapis.com") {
			return []byte(`/* latin */
@font-face { font-family: 'Inter'; font-style: normal; font-weight: 400; src: url(https://fonts.gstatic.com/s/inter/v1/r400.woff2) format('woff2'); }
/* latin */
@font-face { font-family: 'Inter'; font-style: normal; font-weight: 700; src: url(https://fonts.gstatic.com/s/inter/v1/r700.woff2) format('woff2'); }
/* latin */
@font-face { font-family: 'Inter'; font-style: italic; font-weight: 400; src: url(https://fonts.gstatic.com/s/inter/v1/i400.woff2) format('woff2'); }`), nil
		}
		return []byte("wOF2fake"), nil
	}
	path := "/web-builder/v2/fonts/css2?family=Inter:wght@700&display=swap"

	// No anonymous downloads; the API header is not needed (a stylesheet), the nonce is.
	anon := &wbrClient{t: t, c: asNewClient(), base: base}
	if code, _, _ := anon.do(http.MethodGet, path, nil, nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous fonts: %d", code)
	}
	sub := wbrUser(t, srv, base, "sub", "subscriber")
	if code, _, _ := sub.do(http.MethodGet, path, nil, nil); code != http.StatusForbidden {
		t.Errorf("subscriber fonts: %d", code)
	}
	if n := fetches.Load(); n != 0 {
		t.Fatalf("%d downloads before an editor asked", n)
	}
	code, v, h := admin.do(http.MethodGet, path+"&_wpnonce="+admin.nonce, nil, map[string]string{"X-WP-Nonce": "", webbuilder.APIHeader: ""})
	css, _ := v.(string)
	if code != http.StatusOK || !strings.HasPrefix(h.Get("Content-Type"), "text/css") {
		t.Fatalf("fonts: %d %v %v", code, h, v)
	}
	if !strings.Contains(css, "font-weight: 700") || strings.Contains(css, "font-weight: 400") || strings.Contains(css, "gstatic") ||
		!strings.Contains(css, base+"/wp-content/uploads/web-builder/google-fonts/fonts/inter-") {
		t.Errorf("fonts css:\n%s", css)
	}
	if fetches.Load() == 0 {
		t.Error("the family was not downloaded")
	}
	// Unknown families are never fetched.
	before := fetches.Load()
	code, v, _ = admin.do(http.MethodGet, "/web-builder/v2/fonts/css2?family=Evil+Font&_wpnonce="+admin.nonce, nil, map[string]string{"X-WP-Nonce": ""})
	if code != http.StatusOK || fetches.Load() != before || strings.Contains(v.(string), "@font-face") {
		t.Errorf("unknown family: %d %v (fetches %d → %d)", code, v, before, fetches.Load())
	}
	if code, _, _ := admin.do(http.MethodGet, "/web-builder/v2/fonts/css2?_wpnonce="+admin.nonce, nil, nil); code != http.StatusBadRequest {
		t.Errorf("no family: %d", code)
	}
}
