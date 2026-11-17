package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// wbsKit uploads and commits a design kit as the admin.
func wbsKit(t *testing.T, admin *wbrClient, slug string, files map[string]string) {
	t.Helper()
	list := []any{}
	for p, text := range files {
		list = append(list, map[string]any{"path": p, "text": text})
	}
	if code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/kits/"+slug+"/files", map[string]any{"files": list}, nil); code != http.StatusOK {
		t.Fatalf("kit files: %d %v", code, v)
	}
	if code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/kits/"+slug+"/commit", map[string]any{}, nil); code != http.StatusOK {
		t.Fatalf("kit commit: %d %v", code, v)
	}
}

// wbsCodePage publishes a page of one code section of the kit as the admin.
func wbsCodePage(t *testing.T, srv *Server, admin *wbrClient, title, kit, html, head string) int64 {
	t.Helper()
	post := wbrPost(t, srv, "page", title, "draft", 1)
	style := map[string]any{"palette": "light", "align": "left", "padding": 0, "titleSize": 40, "hideOn": []any{}}
	if head == "" {
		head = `<link rel="stylesheet" href="/wp-content/uploads/web-builder/kits/` + kit + `/site.css">`
	}
	sec := map[string]any{"id": "c1", "kind": "code", "name": "Hero", "hidden": false, "items": []any{}, "style": style,
		"content": map[string]any{"html": html, "head": head, "kit": kit}}
	doc := map[string]any{"site": map[string]any{"brand": "#2271b1", "fontPair": "modern", "corners": "soft"}, "sections": []any{sec}}
	body := wbrPublishBody(doc, map[string]any{}, 0, "publish", map[string]any{"title": title, "slug": strings.ToLower(strings.ReplaceAll(title, " ", "-")), "indexable": true})
	body["serverRender"] = true
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(post.ID, "/publish"), body, nil); code != http.StatusOK {
		t.Fatalf("publish %s: %d %v", title, code, v)
	}
	return post.ID
}

// wbsLogin creates a user with the role and logs it in.
func wbsLogin(t *testing.T, srv *Server, base, login, role string) (*wbrClient, int64) {
	t.Helper()
	id := asCreateUser(t, srv, login, role, asStrongPass)
	cc := asNewClient()
	if code, _, _ := asLogin(t, cc, base, "/wp-login", login, asStrongPass); code != http.StatusFound {
		t.Fatalf("login %s: %d", login, code)
	}
	return newWBRClient(t, cc, base), id
}

// GET kits/{slug}/pages gives an author the published, public pages and their own, never another
// user's draft, private or password-protected page; the kit's writer gets every page. The kit list
// and file list are for unfiltered_html authors only.
func TestWBKitPagesVisibility(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	wbsKit(t, admin, "vis", map[string]string{"site.css": ".a{color:red}"})
	pub := wbsCodePage(t, srv, admin, "Public page", "vis", `<p class="pub">public</p>`, "")
	draft := wbsCodePage(t, srv, admin, "Draft page", "vis", `<p class="secret-draft">draft</p>`, "")
	locked := wbsCodePage(t, srv, admin, "Locked page", "vis", `<p class="secret-pass">locked</p>`, "")
	priv := wbsCodePage(t, srv, admin, "Private page", "vis", `<p class="secret-private">private</p>`, "")
	mine := wbsCodePage(t, srv, admin, "Own page", "vis", `<p class="own-draft">own</p>`, "")
	author, authorID := wbsLogin(t, srv, base, "kitauthor", "author")
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE posts SET status = 'draft' WHERE id = $1`, []any{draft}},
		{`UPDATE posts SET password = 'pw' WHERE id = $1`, []any{locked}},
		{`UPDATE posts SET status = 'private' WHERE id = $1`, []any{priv}},
		{`UPDATE posts SET status = 'draft', type = 'post', author_id = $2 WHERE id = $1`, []any{mine, authorID}}, // an author edits posts
	} {
		if _, err := srv.store.Pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(c *wbrClient) map[int64]string {
		code, v, _ := c.do(http.MethodGet, "/web-builder/v2/kits/vis/pages", nil, nil)
		if code != http.StatusOK {
			t.Fatalf("pages: %d %v", code, v)
		}
		out := map[int64]string{}
		pages, _ := wbrObj(v)["pages"].([]any)
		for _, p := range pages {
			out[int64(wbrObj(p)["id"].(float64))] = strAny(wbrObj(p)["html"])
		}
		return out
	}
	got := ids(author)
	if len(got) != 2 || !strings.Contains(got[pub], "pub") || !strings.Contains(got[mine], "own-draft") {
		t.Errorf("author sees %v", got)
	}
	for _, id := range []int64{draft, locked, priv} {
		if _, ok := got[id]; ok {
			t.Errorf("author sees page %d", id)
		}
	}
	if got := ids(admin); len(got) != 5 {
		t.Errorf("admin sees %d pages, want 5", len(got))
	}
	contributor, _ := wbsLogin(t, srv, base, "kitcontrib", "contributor")
	if got := ids(contributor); len(got) != 1 || got[pub] == "" {
		t.Errorf("contributor sees %v", got)
	}
	// The kit list and its files: unfiltered_html authors only.
	for _, p := range []string{"/web-builder/v2/kits", "/web-builder/v2/kits/vis"} {
		if code, _, _ := author.do(http.MethodGet, p, nil, nil); code != http.StatusForbidden {
			t.Errorf("author GET %s: %d", p, code)
		}
		if code, _, _ := admin.do(http.MethodGet, p, nil, nil); code != http.StatusOK {
			t.Errorf("admin GET %s: %d", p, code)
		}
	}
	// The override stays readable to the editor's users (the frame shows it).
	if code, _, _ := author.do(http.MethodGet, "/web-builder/v2/kits/vis/override", nil, nil); code != http.StatusOK {
		t.Errorf("author GET override: %d", code)
	}
}

// wbsGin is a gin context for calling the server's helpers directly.
func wbsGin() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c
}

// wbsFetch GETs a URL with the headers.
func wbsFetch(t *testing.T, u string, hdr map[string]string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, string(b), resp.Header
}

// A site-imported kit's files at the root: only while a page of the import is published, with a
// type from the extension, nosniff and the uploads route's sandbox policy; never as a service
// worker; never in place of the site's own robots.txt or favicon.ico.
func TestWBKitRootAssetHeaders(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	wbsKit(t, admin, "rootk", map[string]string{
		"robots.txt":    "User-agent: *\nDisallow: /design-robots",
		"favicon.ico":   "design-favicon",
		"app.js":        "console.log('design')",
		"sw.js":         "self.addEventListener('fetch', e => {})",
		"assets/a.svg":  `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(document.domain)</script></svg>`,
		"assets/x.map":  `<html><script>alert(document.domain)</script>`,
		"assets/m.glb":  `<html><script>alert(document.domain)</script>`,
		"assets/m.gltf": `<html><script>alert(document.domain)</script>`,
		"assets/s.css":  `body{color:red}`,
	})
	if err := srv.wbKitSetRoot(wbsGin(), "rootk", true); err != nil {
		t.Fatal(err)
	}
	fetch := func(p string, hdr map[string]string) (int, string, http.Header) {
		t.Helper()
		return wbsFetch(t, base+p, hdr)
	}
	// A draft import: nothing at the root.
	if code, body, _ := fetch("/app.js", nil); code == http.StatusOK && strings.Contains(body, "design") {
		t.Errorf("served before any page of the import is published: %d", code)
	}
	p := wbrPost(t, srv, "page", "Imported home", "publish", 1)
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET meta = meta || jsonb_build_object($2::text, $3::text) WHERE id = $1`, p.ID, wbSiteMetaKit, "rootk"); err != nil {
		t.Fatal(err)
	}
	wbKitRootLiveReset()
	srv.purgeAll(ctx) // the 404 the page cache kept (a publish purges)
	const sandbox = "sandbox; default-src 'none'"
	for path, ct := range map[string]string{
		"/app.js": "text/javascript; charset=utf-8", "/assets/a.svg": "image/svg+xml", "/assets/x.map": "application/json",
		"/assets/m.glb": "model/gltf-binary", "/assets/m.gltf": "model/gltf+json", "/assets/s.css": "text/css; charset=utf-8",
	} {
		code, _, h := fetch(path, nil)
		// A script gets no policy: it would be its Web Worker's (wbKitFilePolicy).
		policyOK := strings.HasPrefix(h.Get("Content-Security-Policy"), sandbox)
		if strings.HasSuffix(path, ".js") {
			policyOK = h.Get("Content-Security-Policy") == ""
		}
		if code != http.StatusOK || h.Get("Content-Type") != ct || h.Get("X-Content-Type-Options") != "nosniff" || !policyOK {
			t.Errorf("%s: %d %q %q %q", path, code, h.Get("Content-Type"), h.Get("X-Content-Type-Options"), h.Get("Content-Security-Policy"))
		}
	}
	// The same files under /wp-content/uploads/: a script without a policy, the rest sandboxed.
	if _, _, h := fetch("/wp-content/uploads/web-builder/kits/rootk/app.js", nil); h.Get("Content-Security-Policy") != "" {
		t.Errorf("kit script under uploads: %q", h.Get("Content-Security-Policy"))
	}
	if _, _, h := fetch("/wp-content/uploads/web-builder/kits/rootk/assets/a.svg", nil); !strings.HasPrefix(h.Get("Content-Security-Policy"), sandbox) {
		t.Errorf("kit SVG under uploads: %q", h.Get("Content-Security-Policy"))
	}
	if code, _, h := fetch("/sw.js", map[string]string{"Service-Worker": "script"}); code != http.StatusForbidden || h.Get("Service-Worker-Allowed") != "" {
		t.Errorf("service worker: %d %v", code, h)
	}
	if code, body, _ := fetch("/robots.txt", nil); code != http.StatusOK || strings.Contains(body, "design-robots") {
		t.Errorf("robots.txt is the design's: %d %q", code, body)
	}
	if _, body, _ := fetch("/favicon.ico", nil); strings.Contains(body, "design-favicon") {
		t.Error("favicon.ico is the design's")
	}
	// Pursae SEO answers first: a redirection of a design file's path, its IndexNow key file.
	if err := srv.seogoActivate(ctx); err != nil {
		t.Fatal(err)
	}
	o := *srv.seogoOpts()
	o.Modules["indexnow"], o.Modules["redirections"], o.IndexNowKey = true, true, "0123456789abcdef0123456789abcdef"
	if err := srv.seogoSave(ctx, &o); err != nil {
		t.Fatal(err)
	}
	if err := srv.seogoAddRedirect(ctx, &seogoRedirectRow{Source: "/assets/s.css", Target: "/assets/new.css", Code: 301, Status: "active"}); err != nil {
		t.Fatal(err)
	}
	wbsKit(t, admin, "rootk", map[string]string{"0123456789abcdef0123456789abcdef.txt": "design-key"})
	srv.purgeAll(ctx)
	nr := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if resp, err := nr.Get(base + "/assets/s.css"); err != nil || resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("the redirection of a design file's path: %v %v", resp, err)
	} else {
		resp.Body.Close()
	}
	if code, body, _ := fetch("/0123456789abcdef0123456789abcdef.txt", nil); code != http.StatusOK || body != "0123456789abcdef0123456789abcdef" {
		t.Errorf("IndexNow key: %d %q", code, body)
	}
	// Unpublished again: gone from the root.
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET status = 'draft' WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	wbKitRootLiveReset()
	srv.purgeAll(ctx) // the 404 the page cache kept (a publish purges)
	if code, body, _ := fetch("/app.js", nil); code == http.StatusOK && strings.Contains(body, "design") {
		t.Errorf("served with no published page: %d", code)
	}
}

// Staging a kit file never changes the live file before the commit (the staging folder starts as
// hard links to it), and a kit deleted and uploaded again never reuses a version.
func TestWBKitStagingAndVersions(t *testing.T) {
	srv, _, admin := wbrStart(t)
	wbsKit(t, admin, "stg", map[string]string{"css/a.css": "OLD", "css/b.css": "KEEP"})
	live := filepath.Join(srv.wbKitRoot(), "stg", "css", "a.css")
	if code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/kits/stg/files", map[string]any{"files": []any{map[string]any{"path": "css/a.css", "text": "NEW"}}}, nil); code != http.StatusOK {
		t.Fatalf("stage: %d %v", code, v)
	}
	if b, _ := os.ReadFile(live); string(b) != "OLD" {
		t.Fatalf("the live file changed before the commit: %q", b)
	}
	if code, v, _ := admin.do(http.MethodPost, "/web-builder/v2/kits/stg/commit", map[string]any{}, nil); code != http.StatusOK || wbrObj(v)["version"] != float64(2) {
		t.Fatalf("commit: %d %v", code, v)
	}
	if b, _ := os.ReadFile(live); string(b) != "NEW" {
		t.Errorf("after the commit: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(srv.wbKitRoot(), "stg", "css", "b.css")); string(b) != "KEEP" {
		t.Errorf("unchanged file: %q", b)
	}
	// Delete and upload again: the version goes on.
	if code, _, _ := admin.do(http.MethodDelete, "/web-builder/v2/kits/stg", nil, nil); code != http.StatusOK {
		t.Fatal("delete")
	}
	wbsKit(t, admin, "stg", map[string]string{"css/a.css": "AGAIN"})
	if v := srv.wbKitList()["stg"].Version; v != 3 {
		t.Errorf("version after delete and upload: %d, want 3", v)
	}
}

// The policy's origins of a code page come from its stored code sections and their kits, never
// from the page's other markup (a visitor's comment, a post's text, a field value): a
// url(https://evil.example/x in it adds nothing.
func TestWBCodeCSPOriginsFromCodeOnly(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	wbsKit(t, admin, "cspk", map[string]string{"site.css": ".a{color:red}", "app.js": `fetch("https://api.kit.example/x")`})
	head := `<link rel="stylesheet" href="/wp-content/uploads/web-builder/kits/cspk/site.css"><script src="https://cdn.code.example/lib.js"></script>`
	id := wbsCodePage(t, srv, admin, "CSP page", "cspk", `<section><p>Hi</p><script src="/wp-content/uploads/web-builder/kits/cspk/app.js"></script></section>`, head)
	// What fills the page around the code (a comment, a post, a field) — here written into the
	// stored snapshot itself, outside the code section.
	evil := `<p>url(https://evil.example/x</p><p>@import "https://evil2.example/a.css"</p><a title="<script src=https://evil3.example/x.js>">x</a>`
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE web_builder_docs SET html = regexp_replace(html, '</div>\s*$', $2 || '</div>') WHERE post_id = $1`, id, evil); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	code, body, h := wbsFetch(t, base+"/?page_id="+itoa64(id), nil)
	if code != http.StatusOK || !strings.Contains(body, "evil.example/x") {
		t.Fatalf("page: %d (the injected markup is not on the page: %v)", code, strings.Contains(body, "evil.example"))
	}
	policy := h.Get("Content-Security-Policy") + " " + h.Get("Content-Security-Policy-Report-Only")
	for _, bad := range []string{"evil.example", "evil2.example", "evil3.example"} {
		if strings.Contains(policy, bad) {
			t.Errorf("the policy allows %s: %s", bad, policy)
		}
	}
	for _, want := range []string{"https://cdn.code.example", "https://api.kit.example", "blob:"} {
		if !strings.Contains(policy, want) {
			t.Errorf("the policy lacks %s (the code section's and its kit's): %s", want, policy)
		}
	}
}

// A kit override is validated again when the stored option is read: a tampered option whose
// selector closes the <style> element prints nothing, on the page and in the REST view.
func TestWBKitOverrideTamperedOption(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	wbsKit(t, admin, "tamp", map[string]string{"site.css": ".a{color:red}"})
	id := wbsCodePage(t, srv, admin, "Tampered page", "tamp", `<section class="a"><p>Hi</p></section>`, "")
	bad := map[string]any{"rules": []any{
		map[string]any{"sel": ".a</style><script>alert(1)</script><style>", "p": map[string]any{"color": "red"}},
		map[string]any{"ctx": []any{"@media all{}</style><img src=x onerror=alert(2)>"}, "sel": ".b", "p": map[string]any{"color": "red"}},
	}}
	if err := srv.settings.Set(ctx, map[string]any{wbKitOverridesOption: map[string]any{"tamp": map[string]any{"live": bad, "version": 1, "draft": bad, "draftVersion": 1}}}); err != nil {
		t.Fatal(err)
	}
	wbKitOverrideCache.Delete("css")
	srv.purgeAll(ctx)
	_, body, _ := wbsFetch(t, base+"/?page_id="+itoa64(id), nil)
	if strings.Contains(body, "alert(1)") || strings.Contains(body, "alert(2)") {
		t.Errorf("the tampered override is printed: %s", wbSnippet(body, "data-wb-kit-override"))
	}
	if !strings.Contains(body, `<p>Hi</p>`) {
		t.Error("the page is not rendered")
	}
	code, v, _ := admin.do(http.MethodGet, "/web-builder/v2/kits/tamp/override", nil, nil)
	if code != http.StatusOK || strings.Contains(mcpJ(v), "alert(") {
		t.Errorf("override view: %d %v", code, v)
	}
}
