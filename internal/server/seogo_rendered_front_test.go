package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// With Pursae Multi Language, the front page's translation is the front page of its language and the
// posts page (and its translation) is the blog index: the analysis renders them that way, and the
// front page, whose address is the site root, has no "Keyphrase in slug" assessment.
func TestSEOGoAnalyzesTranslatedFrontAndPostsPage(t *testing.T) {
	themesDir := t.TempDir()
	t.Setenv("PURSAE_THEMES_DIR", themesDir)
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	seogoToggle(t, c, base, "activate")
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Türkçe", "tr_TR", "tr", "tr")
	ctx := context.Background()

	// A theme whose front-page template prints the home page's sections (the page template does not).
	dir := filepath.Join(themesDir, "seofront")
	if err := os.CopyFS(dir, os.DirFS(starterDir)); err != nil {
		t.Fatal(err)
	}
	manifest := starterManifest(t, map[string]string{`"slug": "starter",`: `"slug": "seofront",`})
	if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	hero := strings.Repeat("Quiet craft shapes every room we build for you. ", 40) // 360 words
	tpl := `{{template "header" .}}
<section class="hero"><h2>Our studio</h2><p>` + hero + `</p>
<figure><img src="/wp-content/uploads/one.jpg" alt="Workshop bench" /></figure>
<p>See <a href="https://example.org/press">our press page</a>.</p></section>
{{template "content-page" .Post}}
{{template "footer" .}}`
	if err := os.WriteFile(filepath.Join(dir, "templates", "front-page.html"), []byte(tpl), 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := loadThemePackage(dir, "seofront")
	if err != nil {
		t.Fatal(err)
	}
	registerTheme(th)
	t.Cleanup(func() { unregisterTheme("seofront") })
	if err := srv.settings.Set(ctx, map[string]any{"template": "seofront", "stylesheet": "seofront"}); err != nil {
		t.Fatal(err)
	}

	create := func(path string, body map[string]any) int64 {
		t.Helper()
		code, obj, _ := pllREST(t, c, base, http.MethodPost, path, body)
		if code != http.StatusCreated {
			t.Fatalf("create %s: %d %v", path, code, obj)
		}
		return int64(obj["id"].(float64))
	}
	page := func(query, title, content string) int64 {
		return create("/wp-json/wp/v2/pages"+query, map[string]any{"title": title, "content": content, "status": "publish"})
	}
	analyze := func(id int64, focus string) seogoAnalysis {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"post_id": id, "fields": map[string]any{"focus": focus}})
		code, out := restSend(t, c, base, http.MethodPost, "/wp-json/seogo/v1/analyze", string(body))
		var an struct {
			SEO seogoAnalysis `json:"seo"`
		}
		if code != http.StatusOK || json.Unmarshal([]byte(out), &an) != nil {
			t.Fatalf("analyze %d: %d %s", id, code, out)
		}
		return an.SEO
	}
	tr := func(id int64) string { return "?lang=tr&translations[en]=" + strconv.FormatInt(id, 10) }

	homeEN := page("?lang=en", "Home", "")
	homeTR := page(tr(homeEN), "Anasayfa", "")
	intro := `<!-- wp:paragraph --><p>Notes from the workshop, written down as they happen.</p><!-- /wp:paragraph -->`
	blogEN := page("?lang=en", "Writing", intro)
	blogTR := page(tr(blogEN), "Yazılar", `<!-- wp:paragraph --><p>Atölyeden notlar, oldukları gibi yazıldı.</p><!-- /wp:paragraph -->`)
	otherEN := page("?lang=en", "About", `<!-- wp:paragraph --><p>About quiet craft.</p><!-- /wp:paragraph -->`)
	if err := srv.settings.Set(ctx, map[string]any{"show_on_front": "page", "page_on_front": homeEN, "page_for_posts": blogEN}); err != nil {
		t.Fatal(err)
	}
	postBody := func(word string) string {
		return `<!-- wp:paragraph --><p>` + strings.Repeat(word+" ", 120) + `</p><!-- /wp:paragraph -->`
	}
	var enTitles, trTitles []string
	for i, w := range []string{"chisel", "plane", "saw"} {
		title := "Workshop note " + strconv.Itoa(i+1)
		enID := create("/wp-json/wp/v2/posts?lang=en", map[string]any{"title": title, "content": postBody(w), "status": "publish"})
		enTitles = append(enTitles, title)
		if i < 2 {
			trTitle := "Atölye notu " + strconv.Itoa(i+1)
			create("/wp-json/wp/v2/posts"+tr(enID), map[string]any{"title": trTitle, "content": postBody("keski"), "status": "publish"})
			trTitles = append(trTitles, trTitle)
		}
	}

	// The front end's view of these pages, which the analysis follows.
	for id, want := range map[int64]string{homeEN: "front-page", homeTR: "front-page", blogEN: "home", blogTR: "home", otherEN: "page"} {
		p, err := srv.store.GetPost(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		q := srv.seogoQueryForPost(ctx, p)
		if q.Kind != want {
			t.Errorf("page %d (%s): kind %s, want %s", id, p.Title, q.Kind, want)
		}
		if want == "home" && (q.PostsPage == nil || q.PostsPage.ID != id || q.Post != nil) {
			t.Errorf("posts page %d: query %+v", id, q)
		}
	}

	// 1. The Turkish front page renders the front-page template and scores like the English one.
	aEN, aTR := analyze(homeEN, "quiet craft"), analyze(homeTR, "quiet craft")
	nEN, nTR := seogoWordCount(t, aEN), seogoWordCount(t, aTR)
	if nEN < 300 || nEN > 420 || nTR != nEN {
		t.Errorf("front page words: en %d, tr %d, want the template's ~360 for both", nEN, nTR)
	}
	for _, id := range []string{"images", "externalLinks", "textLength"} {
		if r, _ := seogoResultOf(aTR, id); r.Rating != "good" {
			t.Errorf("Turkish front page %s: %+v", id, r)
		}
	}

	// 2. No "Keyphrase in slug" on the front page and its translation (its URL is the site root); a
	// regular page keeps it. Rank Math's URL length check still runs.
	for name, a := range map[string]seogoAnalysis{"en": aEN, "tr": aTR} {
		if r, ok := seogoResultOf(a, "slugKeyword"); ok {
			t.Errorf("%s front page has a slug assessment: %+v", name, r)
		}
		if _, ok := seogoResultOf(a, "lengthPermalink"); !ok {
			t.Errorf("%s front page lost the permalink length check", name)
		}
	}
	if _, ok := seogoResultOf(analyze(otherEN, "about"), "slugKeyword"); !ok {
		t.Error("a regular page has no slug assessment")
	}

	// 3. The posts page is scored on the listing a visitor sees, in its own language.
	visible := func(id int64) string {
		t.Helper()
		p, err := srv.store.GetPost(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		out, _ := srv.seogoVisibleContent(ctx, p)
		return out
	}
	vEN, vTR := visible(blogEN), visible(blogTR)
	for _, title := range enTitles {
		if !strings.Contains(vEN, title) {
			t.Errorf("English posts page analysis misses %q: %s", title, vEN)
		}
	}
	for _, title := range trTitles {
		if !strings.Contains(vTR, title) {
			t.Errorf("Turkish posts page analysis misses %q: %s", title, vTR)
		}
		if strings.Contains(vEN, title) {
			t.Errorf("English posts page lists the Turkish post %q", title)
		}
	}
	if strings.Contains(vTR, enTitles[2]) {
		t.Errorf("Turkish posts page lists an English post: %s", vTR)
	}
	if n := seogoWordCount(t, analyze(blogEN, "workshop")); n < 150 {
		t.Errorf("English posts page: %d words, want the listing (intro alone is 9)", n)
	}
	if n := seogoWordCount(t, analyze(blogTR, "atölye")); n < 100 {
		t.Errorf("Turkish posts page: %d words, want the listing", n)
	}
}
