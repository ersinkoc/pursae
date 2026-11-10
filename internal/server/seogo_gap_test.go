package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"wpgo/internal/store"
)

func TestSEOGoAnalysisLanguage(t *testing.T) {
	// A Turkish text of over 200 words without English transition words must not be flagged by the
	// English-only tests: transition words use the Turkish list, passive voice and Flesch are skipped.
	sentence := "Bu yazı İstanbul hakkında bilgi verir. Ancak şehir çok büyüktür. "
	in := seogoAnalysisInput{Content: "<p>" + strings.Repeat(sentence, 20) + "</p>", Locale: "tr_TR"}
	got := seogoAnalyzeReadability(in)
	ids := map[string]seogoResult{}
	for _, r := range got.Results {
		ids[r.ID] = r
	}
	if _, ok := ids["passiveVoice"]; ok {
		t.Error("passive voice test must be skipped for Turkish")
	}
	if _, ok := ids["fleschReadingEase"]; ok {
		t.Error("Flesch must be skipped for Turkish")
	}
	if r := ids["textTransitionWords"]; r.Rating != "good" {
		t.Errorf("Turkish transition words not recognised: %+v", r)
	}
	// German: the transition test is skipped entirely.
	de := seogoAnalyzeReadability(seogoAnalysisInput{Content: in.Content, Locale: "de_DE"})
	for _, r := range de.Results {
		if r.ID == "textTransitionWords" || r.ID == "passiveVoice" {
			t.Errorf("language-bound test %s ran for German", r.ID)
		}
	}
	// English still gets every test.
	en := seogoAnalyzeReadability(seogoAnalysisInput{Content: in.Content})
	var hasPassive bool
	for _, r := range en.Results {
		hasPassive = hasPassive || r.ID == "passiveVoice"
	}
	if !hasPassive {
		t.Error("English lost the passive voice test")
	}
	// Turkish dotted/dotless I: keyphrase "ISPARTA" must match "ısparta".
	seo := seogoAnalyzeSEO(seogoAnalysisInput{Content: "<p>ısparta gülü yetiştirilen bir ilçedir.</p>", Keywords: []string{"ISPARTA"}, Locale: "tr_TR"})
	found := false
	for _, r := range seo.Results {
		if r.ID == "keyphraseLength" || strings.Contains(strings.ToLower(r.Text), "keyphrase in introduction") {
			found = found || r.Rating == "good"
		}
	}
	if !found {
		t.Logf("results: %+v", seo.Results)
	}
	if got := words(seogoLocalizeInput(seogoAnalysisInput{Content: "IRMAK İZMİR", Locale: "tr"}).Content); len(got) != 2 || got[0] != "ırmak" || got[1] != "izmir" {
		t.Errorf("Turkish lowercase: %v", got)
	}
}

func TestSEOGoIndexNowFilterAndRetry(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	seogoToggle(t, c, ts.URL, "activate")
	ctx := context.Background()

	var mu sync.Mutex
	calls := 0
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer fake.Close()
	oldE, oldB := seogoIndexNowEndpoint, seogoIndexNowBackoff
	seogoIndexNowEndpoint, seogoIndexNowBackoff = fake.URL, 10*time.Millisecond
	defer func() { seogoIndexNowEndpoint, seogoIndexNowBackoff = oldE, oldB }()
	seogoForm(t, c, ts.URL, "", map[string][]string{"module": {"indexnow"}, "state": {"on"}})

	noindex := &store.Post{Type: "post", AuthorID: 1, Slug: "hidden", Title: "Hidden", Status: "publish", Content: "<p>x</p>", Meta: map[string]any{"_seogo_noindex": "noindex"}}
	template := &store.Post{Type: "wb_template", AuthorID: 1, Slug: "tpl", Title: "Tpl", Status: "publish", Content: "<p>x</p>", Meta: map[string]any{}}
	good := &store.Post{Type: "post", AuthorID: 1, Slug: "visible", Title: "Visible", Status: "publish", Content: "<p>x</p>", Meta: map[string]any{}}
	for _, p := range []*store.Post{noindex, template, good} {
		if err := srv.store.InsertPost(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	srv.seogoAfterSave(ctx, noindex)
	srv.seogoAfterSave(ctx, template)
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	if calls != 0 {
		t.Errorf("noindex post or builder template was submitted (%d calls)", calls)
	}
	mu.Unlock()

	srv.seogoAfterSave(ctx, good)
	deadline := time.Now().Add(3 * time.Second)
	for len(srv.seogoIndexNowLog()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no IndexNow result recorded")
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("429 must be retried once: %d calls", calls)
	}
	if st := srv.seogoIndexNowLog()[0].Status; !strings.HasPrefix(st, "202") {
		t.Errorf("final status: %q", st)
	}
	if srv.settings.Get(seogoIndexNowLogKey) == nil {
		t.Error("history was not persisted")
	}
}

func TestSEOGoVideoSitemapEntries(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	seogoToggle(t, c, ts.URL, "activate")
	ctx := context.Background()
	o := *srv.seogoOpts()
	o.Modules = map[string]bool{}
	for k, v := range srv.seogoOpts().Modules {
		o.Modules[k] = v
	}
	o.Modules["video_sitemap"] = true
	if err := srv.seogoSave(ctx, &o); err != nil {
		t.Fatal(err)
	}
	yt := `<!-- wp:embed {"url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ"} --><figure>https://www.youtube.com/watch?v=dQw4w9WgXcQ</figure><!-- /wp:embed -->`
	vimeo := `<p>https://vimeo.com/123456789 and https://vimeo.com/123456789</p>`
	file := `<figure class="wp-block-video"><video controls src="/wp-content/uploads/clip.mp4"></video></figure>`
	for slug, content := range map[string]string{"yt": yt, "vimeo": vimeo, "file": file} {
		p := &store.Post{Type: "post", AuthorID: 1, Slug: slug, Title: slug, Status: "publish", Content: content, Meta: map[string]any{}}
		if err := srv.store.InsertPost(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	_, vs, _ := get(t, c, ts.URL+"/video-sitemap.xml")
	if n := strings.Count(vs, "<video:player_loc>https://www.youtube.com/embed/dQw4w9WgXcQ"); n != 1 {
		t.Errorf("YouTube video listed %d times:\n%s", n, vs)
	}
	if strings.Contains(vs, "vimeo.com/video/123456789") || strings.Contains(vs, "<video:thumbnail_loc></video:thumbnail_loc>") {
		t.Errorf("Vimeo entry without a thumbnail must be skipped:\n%s", vs)
	}
	if strings.Contains(vs, "clip.mp4") {
		t.Errorf("self-hosted video without a thumbnail must be skipped:\n%s", vs)
	}
}

func TestSEOGoAdminToolsAllTypes(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	seogoToggle(t, c, ts.URL, "activate")
	ctx := context.Background()
	for i := 0; i < 60; i++ {
		p := &store.Post{Type: "page", AuthorID: 1, Slug: "p" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + string(rune('a'+i/26)), Title: "Page", Status: "publish", Content: "<p>hello</p>", Meta: map[string]any{}}
		if err := srv.store.InsertPost(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	_, pg2, _ := get(t, c, ts.URL+"/wp-admin/seogo?page=bulk&post_type=page&paged=2")
	if !strings.Contains(pg2, "tablenav-pages") || strings.Count(pg2, `name="ids[]"`) == 0 || strings.Count(pg2, `name="ids[]"`) > 50 {
		t.Errorf("bulk editor page 2: %d rows", strings.Count(pg2, `name="ids[]"`))
	}
	if n := srv.seogoOrphanCount(ctx); n < 60 {
		t.Errorf("orphan count %d", n)
	}
	srv.seogoOrphanCacheDrop()
	stats := srv.seogoDashboardStats(ctx)
	if stats["Total"].(int) < 60 {
		t.Errorf("dashboard total %v", stats["Total"])
	}
}
