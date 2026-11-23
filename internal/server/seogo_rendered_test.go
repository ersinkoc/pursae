package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"wpgo/internal/store"
)

func TestSEOGoCleanContentUnit(t *testing.T) {
	in := `<nav class="seogo-toc wp-block-seogo-toc"><p>Contents</p><ol><li><a href="#a">Alpha</a></li></ol></nav>` +
		`<h1 class="entry-title">My Page</h1><p>Body text here.</p><!--pll:uc--><aside>Sidebar words</aside>` +
		`<div id="comments"><p>Leave a reply</p></div><script>var x = 1;</script><h2 id="a">Alpha</h2>`
	out, toc := seogoCleanContent(in, "My Page", true)
	if !toc {
		t.Error("table of contents not reported")
	}
	for _, gone := range []string{"Contents", "My Page", "Sidebar", "Leave a reply", "var x", "pll:uc"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q kept: %s", gone, out)
		}
	}
	if !strings.Contains(out, "<p>Body text here.</p>") || !strings.Contains(out, `<h2 id="a">Alpha</h2>`) {
		t.Errorf("content lost: %s", out)
	}
	if out, _ := seogoCleanContent(`<h1>My Page</h1><p>x</p>`, "My Page", false); !strings.Contains(out, "<h1>My Page</h1>") {
		t.Errorf("rendered post content lost its H1: %s", out)
	}
	main, ok := seogoMainHTML(`<html><body><header>Site</header><main id="main"><p>Inside</p></main><footer>Foot</footer></body></html>`)
	if !ok || main != "<p>Inside</p>" {
		t.Errorf("main = %q, %v", main, ok)
	}
	if _, ok := seogoMainHTML(`<html><body><div>No landmark</div></body></html>`); ok {
		t.Error("a page without <main> reported one")
	}
}

// seogoWordsRe reads the word count of the Text length result.
var seogoWordsRe = regexp.MustCompile(`contains (\d+) words`)

func seogoResultOf(a seogoAnalysis, id string) (seogoResult, bool) {
	for _, r := range a.Results {
		if r.ID == id {
			return r, true
		}
	}
	return seogoResult{}, false
}

func seogoWordCount(t *testing.T, a seogoAnalysis) int {
	t.Helper()
	r, ok := seogoResultOf(a, "textLength")
	if !ok {
		t.Fatalf("no textLength result: %+v", a.Results)
	}
	m := seogoWordsRe.FindStringSubmatch(r.Text)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// The analysis scores what a visitor sees: a page whose text and images come from its theme
// template, or from a dynamic block, is not scored as empty; a regular post scores as before.
func TestSEOGoAnalyzesRenderedPage(t *testing.T) {
	themesDir := t.TempDir()
	t.Setenv("PURSAE_THEMES_DIR", themesDir)
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	seogoToggle(t, c, base, "activate")
	ctx := context.Background()

	// A theme package whose page template prints whole sections, with site chrome around <main>
	// and a sidebar, a navigation and comments that must not count.
	dir := filepath.Join(themesDir, "seorender")
	if err := os.CopyFS(dir, os.DirFS(starterDir)); err != nil {
		t.Fatal(err)
	}
	manifest := starterManifest(t, map[string]string{`"slug": "starter",`: `"slug": "seorender", "page_templates": {"page-showcase": "Showcase"},`})
	if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	hero := strings.Repeat("Quiet craft shapes every room we build for you. ", 40) // 360 words
	chrome := strings.Repeat("Sidebar chrome word. ", 200)
	tpl := `{{template "header" .}}
<section class="showcase"><h2>Our studio</h2><p>` + hero + `</p>
<figure><img src="/wp-content/uploads/one.jpg" alt="Workshop bench" /></figure>
<figure><img src="/wp-content/uploads/two.jpg" alt="Finished chair" /></figure>
<p>See <a href="https://example.org/press">our press page</a>.</p></section>
<nav class="in-page"><a href="/a">` + chrome + `</a></nav><aside>` + chrome + `</aside>
{{template "content-page" .Post}}
{{template "footer" .}}`
	if err := os.WriteFile(filepath.Join(dir, "templates", "page-showcase.html"), []byte(tpl), 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := loadThemePackage(dir, "seorender")
	if err != nil {
		t.Fatal(err)
	}
	registerTheme(th)
	t.Cleanup(func() { unregisterTheme("seorender") })
	if err := srv.settings.Set(ctx, map[string]any{"template": "seorender", "stylesheet": "seorender"}); err != nil {
		t.Fatal(err)
	}
	if srv.activeTheme().Slug != "seorender" {
		t.Fatalf("active theme %s", srv.activeTheme().Slug)
	}

	create := func(path, body string) int64 {
		t.Helper()
		code, out := restSend(t, c, base, http.MethodPost, path, body)
		if code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", path, code, out)
		}
		var v struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal([]byte(out), &v)
		return v.ID
	}
	analyze := func(body string) seogoAnalysis {
		t.Helper()
		code, out := restSend(t, c, base, http.MethodPost, "/wp-json/seogo/v1/analyze", body)
		var an struct {
			SEO seogoAnalysis `json:"seo"`
		}
		if code != http.StatusOK || json.Unmarshal([]byte(out), &an) != nil {
			t.Fatalf("analyze: %d %s", code, out)
		}
		return an.SEO
	}
	stored := func(id int64) *store.Post {
		t.Helper()
		p, err := srv.store.GetPost(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if p.Meta == nil {
			p.Meta = map[string]any{}
		}
		return p
	}

	// 1. Empty content, the template renders the page body.
	pageID := create("/wp-json/wp/v2/pages", `{"title":"Showcase","content":"","status":"publish","template":"page-showcase"}`)
	a := analyze(`{"post_id":` + itoa64(pageID) + `,"fields":{"focus":"quiet craft"}}`)
	if n := seogoWordCount(t, a); n < 300 || n > 420 {
		t.Errorf("template page: %d words, want the template's ~360 (no chrome): %+v", n, a.Results)
	}
	if r, _ := seogoResultOf(a, "textLength"); r.Rating != "good" {
		t.Errorf("template page textLength: %+v", r)
	}
	if r, _ := seogoResultOf(a, "images"); r.Rating != "good" {
		t.Errorf("template page images: %+v", r)
	}
	if r, _ := seogoResultOf(a, "externalLinks"); r.Rating != "good" {
		t.Errorf("template page outbound links: %+v", r)
	}
	// The stored scores (list column, dashboard) agree with the analysis of the rendered page.
	p := stored(pageID)
	seogoSetMeta(p.Meta, "focus", "quiet craft")
	srv.seogoStoreScores(ctx, p)
	if got := seogoMeta(p.Meta, "seo_score"); got != strconv.Itoa(a.Score) {
		t.Errorf("stored seo_score %s, analysis %d", got, a.Score)
	}

	// 2. A page made of a dynamic block (a synced pattern) under the default page template.
	body := strings.Repeat("Patterns carry the words of this page to visitors. ", 35) // 315 words
	blockID := create("/wp-json/wp/v2/blocks", `{"title":"Story","status":"publish","content":"<!-- wp:paragraph --><p>`+body+
		`</p><!-- /wp:paragraph --><!-- wp:image --><figure class=\"wp-block-image\"><img src=\"/wp-content/uploads/three.jpg\" alt=\"Story\"/></figure><!-- /wp:image -->"}`)
	content := `<!-- wp:block {\"ref\":` + itoa64(blockID) + `} /-->`
	dynID := create("/wp-json/wp/v2/pages", `{"title":"Story page","content":"`+content+`","status":"publish"}`)
	a = analyze(`{"post_id":` + itoa64(dynID) + `}`)
	if n := seogoWordCount(t, a); n < 300 || n > 330 {
		t.Errorf("dynamic block page: %d words, want the pattern's 315", n)
	}
	if r, _ := seogoResultOf(a, "images"); r.Rating != "good" {
		t.Errorf("dynamic block page images: %+v", r)
	}
	// Unsaved editor content is rendered too: the same block in a new draft's content.
	a = analyze(`{"title":"Draft","content":"` + content + `"}`)
	if n := seogoWordCount(t, a); n < 300 || n > 330 {
		t.Errorf("unsaved content with a dynamic block: %d words", n)
	}

	// 3. A regular post scores as its stored content does.
	postContent := `<!-- wp:paragraph --><p>` + strings.Repeat("Plain posts keep their score as before. ", 40) +
		`<a href="https://example.org/">source</a></p><!-- /wp:paragraph --><!-- wp:heading --><h2 class="wp-block-heading">Plain posts</h2><!-- /wp:heading -->`
	postBody, _ := json.Marshal(map[string]any{"title": "Plain posts", "content": postContent, "status": "publish"})
	postID := create("/wp-json/wp/v2/posts", string(postBody))
	pp := stored(postID)
	seogoSetMeta(pp.Meta, "focus", "plain posts")
	srv.seogoStoreScores(ctx, pp)
	sc := &seogoCtx{s: srv, ctx: ctx, lb: srv.links(ctx), Kind: srv.seogoQueryForPost(ctx, pp).Kind, Post: pp}
	in := srv.seogoAnalysisInputFor(ctx, sc)
	in.Content, in.TOC = pp.Content, false
	if want, got := seogoAnalyzeSEO(in).Score, seogoMeta(pp.Meta, "seo_score"); got != strconv.Itoa(want) {
		t.Errorf("post seo_score %s, stored content scores %d", got, want)
	}
	if want, got := seogoAnalyzeReadability(in).Score, seogoMeta(pp.Meta, "read_score"); got != strconv.Itoa(want) {
		t.Errorf("post read_score %s, stored content scores %d", got, want)
	}
}
