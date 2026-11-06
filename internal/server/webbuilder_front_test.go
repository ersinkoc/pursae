package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Tests of the Pursae Web Builder front render (webbuilder_front.go). The unit tests run without a
// database; the integration tests need WPGO_TEST_ADMIN_DB / WPGO_TEST_DB like the rest of the
// package.

// --- unit ------------------------------------------------------------------------------------------

func TestWebBuilderFrontSiteAssets(t *testing.T) {
	good := fstest.MapFS{"web-builder/manifest.json": {Data: []byte(`{"editor":{"js":"assets/a.js"},"site":{"runtime":"runtime.AbCd1234.js","css":"wb.4d16e08a.css"}}`)}}
	m, err := wbReadSiteAssets(good)
	if err != nil || m.Runtime != "runtime.AbCd1234.js" || m.CSS != "wb.4d16e08a.css" {
		t.Fatalf("site assets: %+v %v", m, err)
	}
	for _, bad := range []string{`../x.js`, `a.js"><script>`, `sub/runtime.js`, `runtime.php`} {
		raw, _ := json.Marshal(map[string]any{"site": map[string]string{"runtime": bad, "css": "wb.css"}})
		if _, err := wbReadSiteAssets(fstest.MapFS{"web-builder/manifest.json": {Data: raw}}); err == nil {
			t.Errorf("accepted site asset %q", bad)
		}
	}
	if _, err := wbReadSiteAssets(fstest.MapFS{}); err == nil {
		t.Error("no manifest: no error")
	}
	// The committed build names both files and they exist.
	real, err := wbReadSiteAssets(os.DirFS("../../web"))
	if err != nil || real.Runtime == "" || real.CSS == "" {
		t.Fatalf("committed manifest: %+v %v", real, err)
	}
	for _, f := range []string{real.Runtime, real.CSS} {
		if _, err := os.Stat(filepath.Join("../../web/web-builder/site", f)); err != nil {
			t.Errorf("site bundle file %s: %v", f, err)
		}
	}
}

func TestWebBuilderFrontHTTPSOrigin(t *testing.T) {
	cases := map[string]string{
		"https://cdn.example.com/w.js":   "https://cdn.example.com",
		"https://CDN.Example.com:8443/x": "https://cdn.example.com:8443",
		"http://cdn.example.com/w.js":    "",
		"//cdn.example.com/w.js":         "",
		"/local.js":                      "",
		"javascript:alert(1)":            "",
		"https://u:p@cdn.example.com/":   "",
		"https://a;b.example.com/":       "",
	}
	for in, want := range cases {
		if got := wbHTTPSOrigin(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestWebBuilderFrontEmbedCSP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := `<div class="wb-page" data-wb-root>` +
		`<iframe src="https://www.youtube-nocookie.com/embed/abc"></iframe>` +
		`<iframe data-wb-embed="h1" sandbox="allow-scripts" srcdoc="&lt;script src=&quot;https://widgets.example.com/w.js&quot;&gt;&lt;/script&gt;&lt;img src=http://insecure.example.com/x.png&gt;"></iframe></div>`
	wbEmbedCSP(c, body)
	m, _ := c.Get(ctxCSPAllow)
	allow, _ := m.(map[string][]string)
	if !cspHas(allow["frame-src"], cspYouTubeNoCook) {
		t.Errorf("frame-src lacks the YouTube player: %v", allow)
	}
	if !cspHas(allow["script-src"], "https://widgets.example.com") {
		t.Errorf("script-src lacks the html section's host: %v", allow)
	}
	for _, list := range allow {
		for _, src := range list {
			if strings.Contains(src, "insecure") {
				t.Errorf("http origin allowed: %v", allow)
			}
		}
	}
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	wbEmbedCSP(c2, `<div class="wb-page" data-wb-root><p>no frames</p></div>`)
	if _, ok := c2.Get(ctxCSPAllow); ok {
		t.Error("a page without iframes changed the policy")
	}
}

func TestWebBuilderFrontPrunePageCSS(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"post-7-1.css", "post-7-2.css", "post-7-3.css", "post-7-4.css", "post-70-1.css", "post-7-x.css"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("a{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wbPrunePageCSS(dir, 7, 4)
	for n, keep := range map[string]bool{"post-7-1.css": false, "post-7-2.css": false, "post-7-3.css": true, "post-7-4.css": true, "post-70-1.css": true, "post-7-x.css": true} {
		_, err := os.Stat(filepath.Join(dir, n))
		if (err == nil) != keep {
			t.Errorf("%s: kept=%v, want %v", n, err == nil, keep)
		}
	}
}

// --- integration -----------------------------------------------------------------------------------

const (
	wbfSnapshotMark = "Builder Hero WB5"
	wbfFallbackMark = "Fallback Content WB5"
)

func wbfSnapshot(id int64, extra string) string {
	return `<div class="wb-page" data-wb-root data-wb-post="` + itoa64(id) + `" style="--brand:#2563eb">` +
		`<nav><details data-wb-menu><summary aria-label="Menu">Menu</summary><div><a href="#hero">Hero</a></div></details></nav>` +
		`<section id="hero"><h1>` + wbfSnapshotMark + `</h1><img src="/wp-content/uploads/hero.png" alt="Hero"></section>` + extra + `</div>`
}

// wbfPage creates a published page with the fallback as posts.content and, when features is not
// nil, a published builder doc.
func wbfPage(t *testing.T, srv *Server, typ, title string, features []string, css, extra string) *store.Post {
	t.Helper()
	ctx := context.Background()
	fallback := `<h1>` + wbfFallbackMark + `</h1><p>Readable without the builder.</p>`
	p := &store.Post{Type: typ, Title: title, Slug: strings.ToLower(strings.ReplaceAll(title, " ", "-")), Status: "publish", AuthorID: 1, Content: fallback}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	if features == nil {
		return p
	}
	d := &store.WebBuilderDoc{PostID: p.ID, Doc: json.RawMessage(`{"sections":[],"site":{}}`), Page: json.RawMessage(`{}`),
		HTML: wbfSnapshot(p.ID, extra), CSS: css, Fonts: json.RawMessage(`[{"family":"Inter","weights":[400,700],"italic":false}]`),
		Features: features, Renderer: webbuilder.RendererVersion, ContentHash: store.WebBuilderContentHash(fallback), SnapshotUser: 1, SnapshotUnfiltered: true}
	if err := srv.store.WebBuilder().PublishDoc(ctx, d, 0); err != nil {
		t.Fatal(err)
	}
	return p
}

func wbfStart(t *testing.T) (*Server, string, *http.Client) {
	t.Helper()
	ts, c, srv := newTestServer(t, true)
	installAndLogin(t, ts, c)
	if !srv.wbFrontOn() {
		t.Fatal("Pursae Web Builder is off")
	}
	return srv, ts.URL, c
}

// wbfURL is a page's permalink (no redirect: anonGet does not follow one).
func wbfURL(base string, p *store.Post) string {
	return base + "/" + p.Slug + "/"
}

// wbfPurge drops the page cache after a test changes the database behind the server's back (the
// REST publish and the drift hook purge on their own).
func wbfPurge(srv *Server) {
	if pc := srv.currentCache(); pc != nil {
		pc.PurgeAll()
	}
}

// wbfAssertTheme checks that the theme rendered posts.content.
func wbfAssertTheme(t *testing.T, what, body string) {
	t.Helper()
	if !strings.Contains(body, wbfFallbackMark) || strings.Contains(body, wbfSnapshotMark) || strings.Contains(body, "data-wb-root") {
		t.Errorf("%s: not the theme fallback:\n%s", what, body)
	}
	if !strings.Contains(body, "twentytwentyfive-style-css") || strings.Contains(body, "web-builder-css") {
		t.Errorf("%s: theme stylesheet missing or builder CSS present", what)
	}
}

func TestWebBuilderFrontCanvas(t *testing.T) {
	srv, base, admin := wbfStart(t)
	// A localized Inter (gfonts.go layout): the page links it and preloads its regular face.
	fontsCSS := srv.gfontsDir("css")
	if err := os.MkdirAll(fontsCSS, 0o755); err != nil {
		t.Fatal(err)
	}
	face := "/* latin */\n@font-face {\n  font-family: 'Inter';\n  font-style: normal;\n  font-weight: 400;\n  src: url(../fonts/inter-latin-400.woff2) format('woff2');\n}\n"
	if err := os.WriteFile(filepath.Join(fontsCSS, "inter.css"), []byte(face), 0o644); err != nil {
		t.Fatal(err)
	}
	p := wbfPage(t, srv, "page", "Landing WB", []string{"menu", "motion"}, ".wb-hero-x{color:red}", "")
	site, err := srv.wbSiteAssets()
	if err != nil {
		t.Fatal(err)
	}

	resp := anonGet(t, wbfURL(base, p))
	body := bodyOf(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if os.Getenv("WBF_DUMP") != "" {
		t.Log(body)
	}
	for _, want := range []string{
		"<!DOCTYPE html>",
		`<html lang="en-US">`,
		"<title>Landing WB &#8211; Toggle Site</title>",
		wbfSnapshot(p.ID, "")[:120], // the snapshot root as stored
		wbfSnapshotMark,
		"<script>document.documentElement.classList.add('wb-js')</script>",
		`<style id="wb-css">`, ".wb-hero-x{color:red}",
		"href='" + base + webbuilder.SiteAssetsPath + site.CSS + "'",
		`<script defer src="` + base + webbuilder.SiteAssetsPath + site.Runtime + `" id="web-builder-runtime-js"></script>`,
		"wb-font-inter-css",
		"/wp-content/uploads/web-builder/google-fonts/fonts/inter-latin-400.woff2\" as=\"font\" type=\"font/woff2\" crossorigin",
		"<link rel=\"canonical\" href=", // core / SEO head kept
		"<meta name='robots' content=",  // robots meta kept
		`decoding="async"`,              // filterContentTags ran over the snapshot images
		"web-builder-page",              // body class
	} {
		if !strings.Contains(body, want) {
			t.Errorf("canvas lacks %q", want)
		}
	}
	for _, bad := range []string{
		wbfFallbackMark,                  // posts.content is not printed
		"twentytwentyfive-style-css",     // no theme stylesheet
		"wp-block-library-css",           // no block library
		"global-styles-inline-css",       // no classic global styles
		`type="module"`, "type='module'", // no module scripts
		"react", "React", // no React for visitors
		"wpadminbar", // anonymous: no toolbar
		"wb-preview-notice",
	} {
		if strings.Contains(body, bad) {
			t.Errorf("canvas contains %q", bad)
		}
	}
	if regexp.MustCompile(`(?i)(?:<link[^>]+stylesheet[^>]+|<script[^>]+)/wp-content/themes/`).MatchString(body) {
		t.Error("canvas loads a theme stylesheet or script")
	}
	if strings.Count(body, "<html") != 1 || strings.Count(body, "</body>") != 1 || strings.Count(body, "data-wb-root") != 1 {
		t.Error("not exactly one document / one snapshot root")
	}
	// One main landmark, wrapping the snapshot root and nothing else (the toolbar and footer
	// scripts stay outside).
	if strings.Count(body, "<main") != 1 || !regexp.MustCompile(`<main id="wb-main"><div class="wb-page[^"]*" data-wb-root`).MatchString(body) ||
		!regexp.MustCompile(`(?s)</div></main>\s.*</body>`).MatchString(body) || strings.Index(body, "</main>") > strings.Index(body, "web-builder-runtime-js") {
		t.Error("the snapshot is not wrapped in one <main> landmark")
	}
	// The class script comes before the stylesheets, so .wb-js rules apply on first paint.
	if strings.Index(body, "classList.add('wb-js')") > strings.Index(body, "web-builder-css") {
		t.Error("wb-js script after the base CSS")
	}

	// A logged-in editor gets the toolbar with its styles and scripts, still without the theme.
	_, abody, _ := get(t, admin, wbfURL(base, p))
	for _, want := range []string{`id="wpadminbar"`, "admin-bar-css", "admin-bar.min.js", wbfSnapshotMark} {
		if !strings.Contains(abody, want) {
			t.Errorf("logged-in canvas lacks %q", want)
		}
	}
	if strings.Contains(abody, "twentytwentyfive-style-css") {
		t.Error("logged-in canvas carries the theme stylesheet")
	}

	// A page with no features: no runtime, no class script; its large CSS goes to a file.
	big := strings.Repeat(".wb-c{margin:0 auto;padding:1rem}\n", webbuilder.InlineCSSMax/30+10)
	q := wbfPage(t, srv, "page", "Quiet WB", []string{}, big, "")
	qbody := bodyOf(anonGet(t, wbfURL(base, q)))
	if !strings.Contains(qbody, wbfSnapshotMark) || strings.Contains(qbody, "web-builder-runtime-js") || strings.Contains(qbody, "wb-js") || strings.Contains(qbody, site.Runtime) {
		t.Error("a page without features loads the runtime")
	}
	if strings.Contains(qbody, `<style id="wb-css">`) {
		t.Error("CSS above the inline limit was inlined")
	}
	m := regexp.MustCompile(`<link rel='stylesheet' id='wb-css' href='([^']+)'`).FindStringSubmatch(qbody)
	if m == nil || !strings.Contains(m[1], "/wp-content/uploads/web-builder/css/post-"+itoa64(q.ID)+"-1.css?ver=") {
		t.Fatalf("no page CSS file link: %v", m)
	}
	file := filepath.Join(srv.cfg.UploadsDir, "web-builder", "css", "post-"+itoa64(q.ID)+"-1.css")
	if b, err := os.ReadFile(file); err != nil || string(b) != strings.TrimSpace(big) {
		t.Fatalf("page CSS file: %v (%d bytes)", err, len(b))
	}
	cssResp := anonGet(t, strings.ReplaceAll(m[1], "&amp;", "&"))
	if cssResp.StatusCode != http.StatusOK || bodyOf(cssResp) != strings.TrimSpace(big) {
		t.Fatalf("page CSS file over HTTP: %d", cssResp.StatusCode)
	}

	// Made private: the CSS is inlined for the viewer who may see it and the public file goes.
	q.Status = "private"
	if err := srv.store.UpdatePost(context.Background(), q, 1); err != nil {
		t.Fatal(err)
	}
	wbfPurge(srv)
	_, pbody, _ := get(t, admin, wbfURL(base, q))
	if !strings.Contains(pbody, wbfSnapshotMark) || !strings.Contains(pbody, `<style id="wb-css">`) || strings.Contains(pbody, "web-builder/css/post-") {
		t.Error("a private page's large CSS is not inline")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("a private page's CSS file is still public: %v", err)
	}
}

func TestWebBuilderFrontFallback(t *testing.T) {
	srv, base, _ := wbfStart(t)
	ctx := context.Background()
	built := wbfPage(t, srv, "page", "Built WB", []string{"menu"}, "", "")
	plain := wbfPage(t, srv, "page", "Plain WB", nil, "", "") // no builder doc: old-builder or core page

	wbfAssertTheme(t, "page without a doc", bodyOf(anonGet(t, wbfURL(base, plain))))

	// Detached: posts.content was edited outside the builder.
	if err := srv.store.WebBuilder().SetDetached(ctx, built.ID, true); err != nil {
		t.Fatal(err)
	}
	wbfPurge(srv)
	wbfAssertTheme(t, "detached", bodyOf(anonGet(t, wbfURL(base, built))))
	if err := srv.store.WebBuilder().SetDetached(ctx, built.ID, false); err != nil {
		t.Fatal(err)
	}
	wbfPurge(srv)
	if b := bodyOf(anonGet(t, wbfURL(base, built))); !strings.Contains(b, wbfSnapshotMark) {
		t.Fatalf("reattached page is not the canvas:\n%s", b)
	}

	// Password-protected: the theme asks for the password, the snapshot is not shown.
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET password = 'secret' WHERE id = $1`, built.ID); err != nil {
		t.Fatal(err)
	}
	wbfPurge(srv)
	pb := bodyOf(anonGet(t, wbfURL(base, built)))
	if strings.Contains(pb, wbfSnapshotMark) || !strings.Contains(pb, "post-password-form") {
		t.Error("password-protected builder page leaks the snapshot")
	}
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET password = '' WHERE id = $1`, built.ID); err != nil {
		t.Fatal(err)
	}
	wbfPurge(srv)

	// A builder post in the feed shows posts.content, never the snapshot.
	fp := wbfPage(t, srv, "post", "Feed WB", []string{}, "", "")
	if b := bodyOf(anonGet(t, srv.links(ctx).Permalink(fp))); !strings.Contains(b, wbfSnapshotMark) {
		t.Fatalf("builder post is not the canvas:\n%.600s", b)
	}
	feed := bodyOf(anonGet(t, base+"/feed/"))
	if !strings.Contains(feed, "Feed WB") || strings.Contains(feed, wbfSnapshotMark) || !strings.Contains(feed, wbfFallbackMark) {
		t.Errorf("feed does not carry the fallback:\n%s", feed)
	}

	// Plugin off: the theme renders posts.content.
	if err := srv.settings.Set(ctx, map[string]any{webbuilder.PluginOption: false}); err != nil {
		t.Fatal(err)
	}
	wbfPurge(srv)
	wbfAssertTheme(t, "plugin off", bodyOf(anonGet(t, wbfURL(base, built))))
}

func TestWebBuilderFrontPreview(t *testing.T) {
	srv, base, admin := wbfStart(t)
	ctx := context.Background()
	p := wbfPage(t, srv, "page", "Preview WB", []string{"menu"}, "", "")

	// No newer draft: the preview is the published canvas, without a notice, not cacheable.
	resp, err := admin.Get(wbfURL(base, p) + "?preview=true")
	if err != nil {
		t.Fatal(err)
	}
	b := bodyOf(resp)
	if !strings.Contains(b, wbfSnapshotMark) || strings.Contains(b, "wb-preview-notice") {
		t.Fatal("preview without a draft")
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Errorf("preview Cache-Control %q", cc)
	}

	// The editor's unpublished autosave: the preview says the snapshot is the published one.
	if err := srv.store.WebBuilder().SaveDraft(ctx, &store.WebBuilderDraft{PostID: p.ID, UserID: 1, Doc: json.RawMessage(`{"sections":[],"site":{}}`), BaseRev: 1}); err != nil {
		t.Fatal(err)
	}
	_, b, _ = get(t, admin, wbfURL(base, p)+"?preview=true")
	if !strings.Contains(b, wbfSnapshotMark) || !strings.Contains(b, `class="wb-preview-notice" role="status"`) {
		t.Fatalf("preview with a newer draft lacks the notice:\n%s", b)
	}
	// Visitors never see the notice, even with ?preview=true.
	if ab := bodyOf(anonGet(t, wbfURL(base, p)+"?preview=true")); strings.Contains(ab, "wb-preview-notice") || !strings.Contains(ab, wbfSnapshotMark) {
		t.Error("anonymous ?preview=true")
	}
	// A draft page published from the builder previews as the canvas for its editor only.
	d := wbfPage(t, srv, "page", "Draft WB", []string{}, "", "")
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET status = 'draft' WHERE id = $1`, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, b, _ := get(t, admin, wbfURL(base, d)+"?preview=true"); !strings.Contains(b, wbfSnapshotMark) {
		t.Error("draft preview is not the canvas")
	}
	if r := anonGet(t, wbfURL(base, d)+"?preview=true"); r.StatusCode != http.StatusNotFound || strings.Contains(bodyOf(r), wbfSnapshotMark) {
		t.Errorf("anonymous draft preview: %d", r.StatusCode)
	}
}

func TestWebBuilderFrontPageCache(t *testing.T) {
	srv, base, _ := wbfStart(t)
	if err := srv.settings.Set(context.Background(), map[string]any{"wpgo_cache_enabled": true}); err != nil {
		t.Fatal(err)
	}
	srv.configureCache()
	extra := `<iframe data-wb-embed="h1" sandbox="allow-scripts" srcdoc="&lt;script src=&quot;https://widgets.example.com/w.js&quot;&gt;&lt;/script&gt;"></iframe>`
	p := wbfPage(t, srv, "page", "Cached WB", []string{"embed"}, ".a{color:red}", extra)

	first := anonGet(t, wbfURL(base, p))
	b1 := bodyOf(first)
	second := anonGet(t, wbfURL(base, p))
	b2 := bodyOf(second)
	if first.StatusCode != http.StatusOK || !strings.Contains(b1, wbfSnapshotMark) {
		t.Fatalf("first view: %d", first.StatusCode)
	}
	if second.Header.Get("X-WPGo-Cache") != "hit" {
		t.Fatalf("builder page not served from the page cache: %q", second.Header.Get("X-WPGo-Cache"))
	}
	if b1 != b2 {
		t.Error("cached copy differs from the rendered page")
	}
	// The cache's theme-font preload is for pages that load the theme; the canvas has none.
	if strings.Contains(b1, "/wp-content/themes/twentytwentyfive/fonts/") {
		t.Error("canvas page got the theme's font preload")
	}
	// A theme page still gets it.
	if sp := bodyOf(anonGet(t, base+"/sample-page/")); !strings.Contains(sp, `rel="preload" href="/wp-content/themes/twentytwentyfive/fonts/`) {
		t.Errorf("theme page lost the font preload")
	}
	for _, r := range []*http.Response{first, second} {
		if csp := r.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "https://widgets.example.com") {
			t.Errorf("CSP (%s) lacks the embed host: %s", r.Header.Get("X-WPGo-Cache"), csp)
		}
	}
	// Tagged like any post, so a publish purge reaches it.
	if tags := first.Header.Get("X-WPGo-Cache-Tags"); tags != "" && !strings.Contains(tags, "post:"+itoa64(p.ID)) {
		t.Errorf("cache tags %q", tags)
	}
}

func TestWebBuilderFrontMainLandmark(t *testing.T) {
	for in, want := range map[string]bool{
		`<div class="wb-page" data-wb-root><section class="sf"></section></div>`: false,
		`<div class="wb-page" data-wb-root><main>x</main></div>`:                 true,
		`<div class="wb-page" data-wb-root><MAIN class="a">x</MAIN></div>`:       true,
		`<div class="wb-page" data-wb-root><div role="main">x</div></div>`:       true,
		`<div class="wb-page" data-wb-root><p>maintain the main road</p></div>`:  false,
		`<div class="wb-page" data-wb-root><mainx></mainx></div>`:                false,
	} {
		if got := wbHasMainLandmark(in); got != want {
			t.Errorf("wbHasMainLandmark(%q) = %v, want %v", in, got, want)
		}
	}
}
