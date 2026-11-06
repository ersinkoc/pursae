package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/config"
	"wpgo/internal/webbuilder"
	"wpgo/web"
)

// Unit tests of the Web Builder plugin skeleton: no database needed.

// jsonKeys lists the JSON object keys a value encodes to.
func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// The raw-JSON boot types encode exactly the contract's keys.
func TestWebBuilderBootShape(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ours, ctr any
	}{
		{"boot", wbBoot{Document: &wbDocument{}}, webbuilder.Boot{}},
		{"document", wbDocument{}, webbuilder.DocumentResponse{}},
		{"draft", wbDraft{}, webbuilder.DraftState{}},
	} {
		if got, want := jsonKeys(t, tc.ours), jsonKeys(t, tc.ctr); !reflect.DeepEqual(got, want) {
			t.Errorf("%s keys = %v, contract %v", tc.name, got, want)
		}
	}
}

// A hostile string in the boot config cannot end the inline script or open an HTML comment, and
// the JSON still decodes to the same value.
func TestWebBuilderEditorHTMLEscapes(t *testing.T) {
	evil := `</script><script>alert(1)</script><!--<script>`
	boot := map[string]any{"title": evil, "doc": map[string]string{"html": "</SCRIPT>" + evil}}
	enc := wbScriptJSON(boot)
	page := wbEditorHTML(`T </title><x>`, "en-US", "ltr", "/wp-content/plugins/web-builder/dist/editor/",
		webbuilder.ManifestEntry{JS: "assets/index-abcdefgh.js", CSS: []string{"assets/index-12345678.css"}}, enc)
	if n := strings.Count(strings.ToLower(page), "</script"); n != 2 {
		t.Fatalf("%d </script in the page, want 2 (the page's own):\n%s", n, page)
	}
	if strings.Contains(page, "<!--") || strings.Contains(page, "<x>") {
		t.Fatalf("unescaped markup in the page:\n%s", page)
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(enc), &back); err != nil {
		t.Fatalf("boot JSON does not decode: %v\n%s", err, enc)
	}
	if back["title"] != evil {
		t.Fatalf("title round trip = %q", back["title"])
	}
	for _, want := range []string{`<div id="root"></div>`, `<script>window.__WEB_BUILDER__ = {`,
		`<script type="module" src="/wp-content/plugins/web-builder/dist/editor/assets/index-abcdefgh.js"></script>`,
		`<link rel="stylesheet" href="/wp-content/plugins/web-builder/dist/editor/assets/index-12345678.css">`,
		`<html lang="en-US" dir="ltr">`, `<meta name="robots" content="noindex, nofollow">`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	// The boot comes before the bundle.
	if strings.Index(page, "__WEB_BUILDER__") > strings.Index(page, `type="module"`) {
		t.Error("the boot script follows the bundle")
	}
}

func TestWebBuilderManifest(t *testing.T) {
	m, err := wbReadManifest(web.FS("../../web"))
	if err != nil {
		t.Fatal(err)
	}
	prefix, entry, err := wbEditorBundle(m)
	if err != nil {
		t.Fatal(err)
	}
	dir := webbuilder.EmbedDir + "/" + strings.TrimSuffix(strings.TrimPrefix(prefix, "/wp-content/plugins/web-builder/dist/"), "/")
	for _, f := range append([]string{entry.JS}, entry.CSS...) {
		if _, err := fs.Stat(web.FS("../../web"), dir+"/"+f); err != nil {
			t.Errorf("manifest names %s, not in the build: %v", f, err)
		}
	}
	for _, f := range []string{m.Site.Runtime, m.Site.CSS} {
		if _, err := fs.Stat(web.FS("../../web"), webbuilder.EmbedDir+"/site/"+f); err != nil {
			t.Errorf("manifest names site/%s, not in the build: %v", f, err)
		}
	}
	// The embedded copy is what a release serves.
	if _, err := wbReadManifest(web.FS("")); err != nil {
		t.Fatalf("embedded manifest: %v", err)
	}

	bad := map[string]string{
		"no editor":   `{"site":{"runtime":"runtime.abcdefgh.js","css":"wb.abcdefgh.css"}}`,
		"parent path": `{"editor":{"js":"../../admin/x.js","css":[]}}`,
		"absolute":    `{"editor":{"js":"/x.js","css":[]}}`,
		"quote":       `{"editor":{"js":"assets/x.js\" onload=\"x","css":[]}}`,
		"css url":     `{"editor":{"js":"assets/x.js","css":["https://evil.test/x.css"]}}`,
		"not json":    `{`,
	}
	for name, raw := range bad {
		fsys := fstest.MapFS{"web-builder/manifest.json": {Data: []byte(raw)}}
		if _, err := wbReadManifest(fsys); err == nil {
			t.Errorf("%s: manifest accepted", name)
		}
	}
	if _, err := wbReadManifest(fstest.MapFS{}); err == nil {
		t.Error("missing manifest accepted")
	}
	// The no-AI build wins when it exists.
	two := &webbuilder.Manifest{Editor: webbuilder.ManifestEntry{JS: "a.js"}, EditorNoAI: webbuilder.ManifestEntry{JS: "b.js"}}
	if p, e, _ := wbEditorBundle(two); p != webbuilder.EditorNoAIAssetsPath || e.JS != "b.js" {
		t.Errorf("bundle = %s %s, want editor-noai", p, e.JS)
	}
}

func TestWebBuilderParseLock(t *testing.T) {
	uid, at, ok := wbParseLock("1730000000:7")
	if !ok || uid != 7 || !at.Equal(time.Unix(1730000000, 0)) {
		t.Fatalf("parse = %d %v %v", uid, at, ok)
	}
	for _, v := range []any{nil, "", "x:1", "1:", ":1", "1:2:3", 12, "99999999999999999999:1"} {
		if _, _, ok := wbParseLock(v); ok {
			t.Errorf("%v parsed as a lock", v)
		}
	}
}

func TestWebBuilderHashedNames(t *testing.T) {
	for name, want := range map[string]bool{
		"index-r47ukO3c.js": true, "runtime.BUPc1PCe.js": true, "wb.4d16e08a.css": true,
		"inter-latin-400-normal-C38fXH4l.woff2": true, "index-Bhaz_rUy.css": true,
		"manifest.json": false, "index.js": false, "style.css": false, "index-abc.js": false,
	} {
		if got := wbHashedName.MatchString(name); got != want {
			t.Errorf("%s hashed = %v, want %v", name, got, want)
		}
	}
}

// The new builder's routes sit beside every catch-all they share a prefix with: registering them
// does not panic and each request reaches the route it belongs to.
func TestWebBuilderRoutesNoConflict(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	sentinel := func(code int) gin.HandlerFunc { return func(c *gin.Context) { c.Status(code) } }
	// The routes of server.go that share a prefix with ours.
	r.GET("/wp-content/uploads/*filepath", sentinel(251))
	r.GET(pluginAssetsPath+"/:slug/assets/*filepath", sentinel(252))
	s := &Server{cfg: config.Config{WebDir: "../../web"}, web: web.FS("../../web")}
	func() {
		defer func() {
			if v := recover(); v != nil {
				t.Fatalf("route conflict: %v", v)
			}
		}()
		for _, a := range []struct{ prefix, dir string }{
			{webbuilder.EditorAssetsPath, webbuilder.BundleEditor},
			{webbuilder.EditorNoAIAssetsPath, webbuilder.BundleEditorNoAI},
			{webbuilder.SiteAssetsPath, webbuilder.BundleSite},
		} {
			s.static(r, strings.TrimSuffix(a.prefix, "/"), webbuilder.EmbedDir+"/"+a.dir, wbAssetHeaders)
		}
	}()
	m, err := s.wbManifest()
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{
		"/wp-content/uploads/2026/11/a.png":                        251,
		"/wp-content/plugins/some-plugin/assets/x.js":              252,
		webbuilder.EditorAssetsPath + m.Editor.JS:                  200,
		webbuilder.SiteAssetsPath + m.Site.Runtime:                 200,
		webbuilder.SiteAssetsPath + m.Site.CSS:                     200,
		webbuilder.EditorAssetsPath + "assets/missing-abcdefgh.js": 404,
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
}

// Bundle files go out with their content type and, being hashed, as immutable.
func TestWebBuilderAssetHeaders(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	s := &Server{cfg: config.Config{}, web: web.FS("../../web")} // embedded files, as a release
	s.static(r, strings.TrimSuffix(webbuilder.EditorAssetsPath, "/"), webbuilder.EmbedDir+"/"+webbuilder.BundleEditor, wbAssetHeaders)
	s.static(r, strings.TrimSuffix(webbuilder.SiteAssetsPath, "/"), webbuilder.EmbedDir+"/"+webbuilder.BundleSite, wbAssetHeaders)
	m, err := wbReadManifest(s.web)
	if err != nil {
		t.Fatal(err)
	}
	font := ""
	entries, _ := fs.ReadDir(s.web, webbuilder.EmbedDir+"/editor/assets")
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".woff2") {
			font = "assets/" + e.Name()
			break
		}
	}
	if font == "" {
		t.Fatal("no woff2 in the editor build")
	}
	cases := []struct{ path, ctype string }{
		{webbuilder.EditorAssetsPath + m.Editor.JS, "text/javascript"},
		{webbuilder.EditorAssetsPath + m.Editor.CSS[0], "text/css"},
		{webbuilder.EditorAssetsPath + font, "font/woff2"},
		{webbuilder.SiteAssetsPath + m.Site.Runtime, "text/javascript"},
		{webbuilder.SiteAssetsPath + m.Site.CSS, "text/css"},
	}
	for _, gz := range []bool{false, true} {
		for _, tc := range cases {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if gz {
				req.Header.Set("Accept-Encoding", "gzip")
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("GET %s (gzip %v) = %d", tc.path, gz, rec.Code)
				continue
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, tc.ctype) {
				t.Errorf("GET %s (gzip %v): Content-Type %q, want %s", tc.path, gz, ct, tc.ctype)
			}
			if cc := rec.Header().Get("Cache-Control"); cc != wbImmutable {
				t.Errorf("GET %s (gzip %v): Cache-Control %q", tc.path, gz, cc)
			}
		}
	}
	// A conditional request keeps the immutable header on its 304.
	req := httptest.NewRequest(http.MethodGet, webbuilder.SiteAssetsPath+m.Site.Runtime, nil)
	req.Header.Set("If-Modified-Since", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotModified && rec.Header().Get("Cache-Control") != wbImmutable {
		t.Errorf("304 Cache-Control %q", rec.Header().Get("Cache-Control"))
	}
	// Directory listings and the manifest are not served.
	for _, p := range []string{webbuilder.EditorAssetsPath + "assets/", webbuilder.EditorAssetsPath + "../manifest.json", webbuilder.SiteAssetsPath + "../BUILD.json"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code == http.StatusOK {
			t.Errorf("GET %s = 200", p)
		}
	}
}
