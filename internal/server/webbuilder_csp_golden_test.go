package server

import (
	"encoding/json"
	"html"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/settings"
	"wpgo/internal/store"
)

// refWBCodeCSP is wbCodeCSP as it was before the origins were read once per doc revision (the
// regular expressions over the whole markup on every response), with the site's kits given. The
// policy wbCodeCSP and wbCodeCSPDocs build must stay the one this builds, byte for byte.
func refWBCodeCSP(c *gin.Context, code string, kits map[string]*wbKit) {
	if c == nil || strings.TrimSpace(code) == "" {
		return
	}
	src := code
	cspAllow(c, "img-src", "blob:")
	cspAllow(c, "media-src", "blob:")
	if strings.Contains(src, wbKitURLPrefix) {
		done := map[string]bool{}
		for _, m := range refKitRefRE.FindAllStringSubmatch(src, -1) {
			if k := kits[m[2]]; k != nil && !done[m[2]] {
				done[m[2]] = true
				for _, dir := range []string{"script-src", "connect-src", "style-src", "font-src"} {
					cspAllow(c, dir, k.Origins...)
				}
				if cspHas(k.Origins, wbGoogleFontsCSS) {
					cspAllow(c, "font-src", wbGoogleFontsFiles)
				}
			}
		}
	}
	if !strings.Contains(src, "https://") {
		return
	}
	var scripts, styles, fonts, frames []string
	add := func(list *[]string, o string) {
		if o != "" && len(*list) < wbCodeOriginMax && !cspHas(*list, o) {
			*list = append(*list, o)
		}
	}
	style := func(o string) {
		add(&styles, o)
		add(&fonts, o)
		if o == wbGoogleFontsCSS {
			add(&fonts, wbGoogleFontsFiles)
		}
	}
	for _, m := range wbCodeScriptRE.FindAllStringSubmatch(src, -1) {
		add(&scripts, wbHTTPSOrigin(html.UnescapeString(m[1]+m[2]+m[3])))
	}
	for _, m := range wbCodeLinkRE.FindAllStringSubmatch(src, -1) {
		style(wbHTTPSOrigin(html.UnescapeString(m[1] + m[2] + m[3])))
	}
	for _, m := range refCSSURLRE.FindAllStringSubmatch(src, -1) {
		style(wbHTTPSOrigin(m[1]))
	}
	for _, m := range wbCodeFrameRE.FindAllStringSubmatch(src, -1) {
		add(&frames, wbHTTPSOrigin(html.UnescapeString(m[1]+m[2]+m[3])))
	}
	cspAllow(c, "script-src", scripts...)
	cspAllow(c, "connect-src", scripts...)
	cspAllow(c, "style-src", styles...)
	cspAllow(c, "font-src", fonts...)
	cspAllow(c, "frame-src", frames...)
}

func goldenCtx() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	return c
}

// goldenPolicy is the policy a context's additions give a fixed base policy.
func goldenPolicy(c *gin.Context) (map[string][]string, string) {
	m, _ := c.Get(ctxCSPAllow)
	allow, _ := m.(map[string][]string)
	return allow, cspMerge("default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:", allow)
}

var goldenKits = map[string]*wbKit{
	"kit-a": {Slug: "kit-a", Origins: []string{"https://cdn.kit-a.example", wbGoogleFontsCSS}},
	"kit-b": {Slug: "kit-b", Origins: []string{"https://api.kit-b.example"}},
	"empty": {Slug: "empty"},
}

// goldenMarkups are the shapes of the code sections of imported designs: head links and scripts,
// kit files, inline CSS with url() and @import, entities, iframes, more origins than a directive
// takes, broken markup.
func goldenMarkups(t *testing.T) []string {
	many := func(f func(i int) string) string {
		var b strings.Builder
		for i := 0; i < 40; i++ {
			b.WriteString(f(i))
		}
		return b.String()
	}
	out := []string{
		"", "   \n", "<div>no origins</div>",
		`<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter"><script src="https://cdn.example.com/lib.js"></script>`,
		`<link href='/wp-content/uploads/web-builder/kits/kit-a/css/app.css'><script src=/wp-content/uploads/web-builder/kits/kit-b/js/app.js?v=2></script><img src="/wp-content/uploads/web-builder/kits/nokit/x.png">`,
		`<style>@import url("https://styles.example.org/a.css"); .x{background:url( 'HTTPS://IMG.example.net/b.png' )} @font-face{src:url(https://fonts.gstatic.com/x.woff2)}</style>`,
		`<div style="background-image:url(&quot;https://bg.example.com/1.jpg&quot;)"></div><div style="background:url(&#39;https://bg2.example.com/1.jpg&#39;)"></div>`,
		`<iframe src="https://www.google.com/maps/embed?pb=1" loading="lazy"></iframe><iframe src='https://player.vimeo.com/video/1'></iframe><iframe srcdoc="<p>x</p>"></iframe>`,
		`<script src="https&#x3a;//entity.example.com/x.js"></script><script src="https://user:pw@bad.example.com/x.js"></script><script src="http://plain.example.com/x.js"></script>`,
		`<link href="https://a.example.com/x.css"` + "\n" + `<script src="https://b.example.com/x.js"`,
		`url(httpſ://fold.example.com/x) @IMPORT 'https://Upper.Example.com/x.css'`,
		many(func(i int) string { return `<script src="https://s` + strconv.Itoa(i) + `.example.com/x.js"></script>` }),
		many(func(i int) string {
			return `<link rel=stylesheet href="https://l` + strconv.Itoa(i) + `.example.com/x.css">`
		}),
		many(func(i int) string {
			return `.c` + strconv.Itoa(i) + `{background:url(https://u` + strconv.Itoa(i) + `.example.com/x.png)}`
		}),
		many(func(i int) string { return `<iframe src="https://f` + strconv.Itoa(i) + `.example.com/"></iframe>` }),
		`<link href="https://fonts.googleapis.com/css"><style>` + many(func(i int) string { return `.c{background:url(https://u` + strconv.Itoa(i) + `.example.com/x.png)}` }) + `</style>`,
	}
	for _, dir := range []string{"../webbuilder/testdata", "testdata"} {
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && (strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".json")) {
				if b, err := os.ReadFile(p); err == nil && len(b) < 8<<20 {
					out = append(out, string(b))
				}
			}
			return nil
		})
	}
	if v := os.Getenv("WPGO_SCAN_CORPUS"); v != "" {
		for _, dir := range filepath.SplitList(v) {
			_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && strings.HasSuffix(strings.ToLower(p), ".html") {
					if b, err := os.ReadFile(p); err == nil && len(b) < 8<<20 {
						out = append(out, string(b))
					}
				}
				return nil
			})
		}
	}
	t.Logf("%d markups", len(out))
	return out
}

// TestWBCodeCSPGolden: the origins read once (wbCodeOriginsOf, the literal scanners) give the
// policy the regular expressions gave, for every markup alone and for pairs of them.
func TestWBCodeCSPGolden(t *testing.T) {
	markups := goldenMarkups(t)
	check := func(code string) {
		t.Helper()
		want, got := goldenCtx(), goldenCtx()
		want.Set(ctxCSPAllow, map[string][]string{"img-src": {"https:"}}) // what the page added before
		got.Set(ctxCSPAllow, map[string][]string{"img-src": {"https:"}})
		refWBCodeCSP(want, code, goldenKits)
		if o := wbCodeOriginsOf(code); o != nil {
			wbCodeOriginsAllow(got, o, goldenKits)
		}
		wa, wp := goldenPolicy(want)
		ga, gp := goldenPolicy(got)
		if !reflect.DeepEqual(wa, ga) || wp != gp {
			t.Fatalf("policy differs for %.200q\n got %s\nwant %s", code, gp, wp)
		}
	}
	for i, a := range markups {
		check(a)
		check(a + "\n" + markups[(i+1)%len(markups)])
	}
}

// TestWBCodeCSPDocsCached: wbCodeCSPDocs gives the policy of the docs' concatenated code markup,
// on the first response and from the cache, and a new revision is read again.
func TestWBCodeCSPDocsCached(t *testing.T) {
	s := &Server{settings: settings.New(nil)} // no kits: the kit origins are TestWBCodeCSPGolden's
	doc := func(id int64, rev int, html, head string) *store.WebBuilderDoc {
		b, _ := json.Marshal(map[string]any{"sections": []any{
			map[string]any{"id": "c1", "kind": "code", "content": map[string]string{"html": html, "head": head}},
			map[string]any{"id": "c2", "kind": "code", "hidden": true, "content": map[string]string{"html": `<script src="https://hidden.example.com/x.js"></script>`}},
		}})
		return &store.WebBuilderDoc{PostID: id, Rev: rev, UpdatedAt: time.Unix(1700000000, int64(id)), Doc: b}
	}
	header := doc(9101, 1, `<header style="background:url(https://hdr.example.com/a.png)"></header>`, `<link href="https://fonts.googleapis.com/css2">`)
	main := doc(9102, 3, `<iframe src="https://maps.example.com/embed"></iframe>`, `<script src="https://cdn.example.com/lib.js"></script>`)
	footer := &store.WebBuilderDoc{PostID: 9103, Rev: 1, Doc: json.RawMessage(`{"sections":[{"kind":"hero"}]}`)}
	docs := []*store.WebBuilderDoc{main, header, footer, nil}
	policy := func(f func(c *gin.Context)) string {
		c := goldenCtx()
		f(c)
		_, p := goldenPolicy(c)
		return p
	}
	want := policy(func(c *gin.Context) {
		refWBCodeCSP(c, wbCodeSource(main)+wbCodeSource(header)+wbCodeSource(footer), nil)
	})
	if !strings.Contains(want, "https://hdr.example.com") || strings.Contains(want, "hidden.example.com") {
		t.Fatalf("reference policy: %s", want)
	}
	for i := 0; i < 3; i++ { // a miss, then hits
		if got := policy(func(c *gin.Context) { s.wbCodeCSPDocs(c, docs, "") }); got != want {
			t.Fatalf("request %d: got %s\nwant %s", i, got, want)
		}
	}
	// With forum templates (extra markup): read for the response.
	extra := `<script src="https://forum.example.com/x.js"></script>` + "\n"
	wantExtra := policy(func(c *gin.Context) {
		refWBCodeCSP(c, wbCodeSource(main)+wbCodeSource(header)+wbCodeSource(footer)+extra, nil)
	})
	if got := policy(func(c *gin.Context) { s.wbCodeCSPDocs(c, docs, extra) }); got != wantExtra || !strings.Contains(got, "forum.example.com") {
		t.Fatalf("with extra markup: got %s\nwant %s", got, wantExtra)
	}
	// A new revision of the main doc: its new markup counts.
	main2 := doc(9102, 4, `<p>moved</p>`, `<script src="https://cdn2.example.com/lib.js"></script>`)
	got := policy(func(c *gin.Context) { s.wbCodeCSPDocs(c, []*store.WebBuilderDoc{main2, header, footer, nil}, "") })
	if !strings.Contains(got, "https://cdn2.example.com") || strings.Contains(got, "https://cdn.example.com ") || strings.Contains(got, "maps.example.com") {
		t.Fatalf("new revision: %s", got)
	}
}

// BenchmarkWBCodeCSP: the origins of a large design's code markup, read on every response as
// before (the regular expressions) and from the per-revision cache.
func BenchmarkWBCodeCSP(b *testing.B) {
	code := benchPage(b)
	s := &Server{settings: settings.New(nil)}
	d := &store.WebBuilderDoc{PostID: 9201, Rev: 1, UpdatedAt: time.Unix(1700000000, 0)}
	d.Doc, _ = json.Marshal(map[string]any{"sections": []any{map[string]any{"id": "c", "kind": "code", "content": map[string]string{"html": code}}}})
	docs := []*store.WebBuilderDoc{d}
	b.Run("regexp", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			refWBCodeCSP(goldenCtx(), wbCodeSource(d), nil)
		}
	})
	b.Run("scan", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			s.wbCodeCSP(goldenCtx(), wbCodeSource(d))
		}
	})
	b.Run("cached", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			s.wbCodeCSPDocs(goldenCtx(), docs, "")
		}
	})
}
