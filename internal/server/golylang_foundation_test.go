package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

func TestPllUCFinish(t *testing.T) {
	page := []byte(`<html><body><h2><!--pll:uc-->Title<!--/pll:uc--></h2><div><!--pll:uc post=7--><p>Body <!--pll:uc-->c<!--/pll:uc--></p><!--/pll:uc--></div></body></html>`)
	if got := string(pllUCFinish(page, false)); got != `<html><body><h2>Title</h2><div><p>Body c</p></div></body></html>` {
		t.Fatalf("strip: %s", got)
	}
	want := `<html><body><h2>Title</h2><div><pll-uc data-pll-post="7" style="display:contents"><p>Body c</p></pll-uc></div></body></html>`
	if got := string(pllUCFinish(page, true)); got != want {
		t.Fatalf("visual: %s", got)
	}
	plain := []byte("<p>x</p>")
	if got := pllUCFinish(plain, false); &got[0] != &plain[0] {
		t.Fatal("a page without sentinels must be returned as it is")
	}
}

func TestPllMTKeySeal(t *testing.T) {
	s := &Server{}
	s.cfg.Secret = "test-secret"
	sealed := s.pllSealMTKey(" abc:fx ")
	if !strings.HasPrefix(sealed, pllSealPrefix) || strings.Contains(sealed, "abc") {
		t.Fatalf("sealed: %q", sealed)
	}
	if s.pllSealMTKey(sealed) != sealed || s.pllSealMTKey("") != "" {
		t.Fatal("sealing must keep sealed and empty values")
	}
	if got := s.pllRevealMTKey(sealed); got != "abc:fx" {
		t.Fatalf("reveal: %q", got)
	}
	if got := s.pllRevealMTKey(pllSealPrefix + "!!"); got != "" {
		t.Fatalf("a broken value reveals %q", got)
	}
	if s.pllMTKeyInput(sealed, pllMTKeyPlaceholder, false) != sealed || s.pllMTKeyInput(sealed, "", false) != sealed ||
		s.pllMTKeyInput(sealed, "x", true) != "" {
		t.Fatal("key input merge")
	}
	if got := s.pllRevealMTKey(s.pllMTKeyInput(sealed, "new-key", false)); got != "new-key" {
		t.Fatalf("replaced key reveals %q", got)
	}
	o := pllDefaultOptions()
	if !o.StringCapture || o.MT || o.MTKey != "" {
		t.Fatalf("defaults: %+v", o)
	}
}

// TestGolylangFoundation: user content sentinels never reach a page (and are only emitted while
// the plugin is on), the capture writer, and the media overlay guard.
func TestGolylangFoundation(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	jar, _ := cookiejar.New(nil)
	anon := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// Plugin off: nothing is marked.
	gc, _ := gin.CreateTestContext(httptest.NewRecorder())
	gc.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	pllSet(gc, &pllReq{srv: srv, Lang: &pllLanguage{Slug: "en"}, Front: true})
	if got := srv.pllUC(gc, "<p>x</p>", 3); got != "<p>x</p>" {
		t.Fatalf("marked while off: %s", got)
	}
	_, first := restGet(t, c, base, "/wp-json/wp/v2/posts/1")
	hello := regexp.MustCompile(`"link":"([^"]+)"`).FindStringSubmatch(first)
	if hello == nil {
		t.Fatal("no link of the first post")
	}
	helloURL := strings.ReplaceAll(hello[1], `\/`, "/")
	_, offBody, _ := get(t, anon, helloURL)
	if !strings.Contains(offBody, "Welcome to") {
		t.Fatalf("first post page: %s", helloURL)
	}
	if strings.Contains(offBody, "pll:uc") || strings.Contains(offBody, "pll-uc") {
		t.Fatal("a sentinel on a page while the plugin is off")
	}

	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	en := srv.pllLang(ctx, "en")
	if en == nil {
		t.Fatal("no en language")
	}
	pllSet(gc, &pllReq{srv: srv, Lang: en, Front: true})
	if got := srv.pllUC(gc, "<p>x</p>", 3); got != "<!--pll:uc post=3--><p>x</p><!--/pll:uc-->" {
		t.Fatalf("not marked while on: %s", got)
	}
	if got := srv.pllUC(gc, " \n", 3); got != " \n" {
		t.Fatalf("blank content marked: %q", got)
	}
	pllSet(gc, &pllReq{srv: srv, Lang: en})
	if got := srv.pllUC(gc, "<p>x</p>", 3); got != "<p>x</p>" {
		t.Fatal("marked outside a front-end request")
	}

	id, link := pllCreatePost(t, c, base, "?lang=fr", "Bonjour sentinel")
	if code, obj, _ := pllREST(t, c, base, http.MethodPost, "/wp-json/wp/v2/comments", map[string]any{"post": id, "content": "A comment body", "status": "approved"}); code != http.StatusCreated {
		t.Fatalf("comment: %d %v", code, obj)
	}
	for _, cl := range []*http.Client{c, anon} {
		for _, u := range []string{link, base + "/", base + "/fr/", helloURL, base + "/?s=sentinel"} {
			code, body, _ := get(t, cl, u)
			if code != http.StatusOK {
				t.Fatalf("%s: %d", u, code)
			}
			if strings.Contains(body, "pll:uc") || strings.Contains(body, "pll-uc") {
				t.Fatalf("a sentinel leaked on %s", u)
			}
		}
	}
	if _, body, _ := get(t, anon, link); !strings.Contains(body, "Bonjour sentinel body") || !strings.Contains(body, "A comment body") {
		t.Fatal("the post or its comment is missing")
	}
	if _, feed, _ := get(t, anon, base+"/fr/feed/"); strings.Contains(feed, "pll:uc") {
		t.Fatal("a sentinel in the feed")
	}

	// Capture: deduplicated, capped per request, written on flush.
	srv.pllCaptureString(ctx, "  Read more  ", "", "", "page", "/fr/")
	srv.pllCaptureString(ctx, "Read more", "", "", "page", "/other/")
	srv.pllCaptureString(ctx, "Read more", "", "tt5", "theme:twentytwentyfive", "/")
	srv.pllCaptureString(ctx, "2026", "", "", "page", "/")
	srv.pllCaptureString(ctx, strings.Repeat("a", 2001), "", "", "page", "/")
	srv.pllCaptureString(ctx, "bad\x00\xff utf", "", "", "page", "/")
	rc, _ := gin.CreateTestContext(httptest.NewRecorder())
	rc.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	pllSet(rc, &pllReq{srv: srv, Lang: en, Front: true})
	for i := 0; i < 600; i++ {
		srv.pllCaptureString(rc, "Captured string "+itoa64(int64(i)), "", "", "page", "/")
	}
	srv.pllCaptureFlush(ctx)
	var n, rm, bad int
	var url, src string
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM pll_string_sources WHERE original LIKE 'Captured string %'`).Scan(&n)
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM pll_string_sources WHERE original = 'Read more'`).Scan(&rm)
	_ = srv.store.Pool.QueryRow(ctx, `SELECT url, source FROM pll_string_sources WHERE original = 'Read more' AND domain = ''`).Scan(&url, &src)
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM pll_string_sources WHERE original IN ('2026', 'bad utf') OR length(original) > 2000`).Scan(&bad)
	if n != pllCapturePerRequest {
		t.Errorf("captured %d strings in one request, want %d", n, pllCapturePerRequest)
	}
	if rm != 2 || url != "/fr/" || src != "page" {
		t.Errorf("Read more rows %d (url %q, source %q), want 2 (/fr/, page)", rm, url, src)
	}
	if bad != 1 { // "bad utf" is stored cleaned; the number and the long string are not
		t.Errorf("filtered strings stored: %d", bad)
	}
	o := srv.pllOpts()
	o.StringCapture = false
	if err := srv.pllSaveOpts(ctx, o); err != nil {
		t.Fatal(err)
	}
	srv.pllCaptureString(ctx, "Not captured", "", "", "page", "/")
	srv.pllCaptureString(ctx, "Theme string", "", "tt5", "theme:twentytwentyfive", "/")
	srv.pllCaptureFlush(ctx)
	var nc, ts2 int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM pll_string_sources WHERE original = 'Not captured'`).Scan(&nc)
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM pll_string_sources WHERE original = 'Theme string'`).Scan(&ts2)
	if nc != 0 || ts2 != 1 {
		t.Errorf("String capture off: page %d (want 0), theme %d (want 1)", nc, ts2)
	}

	// Media: an overlaid attachment saves its base texts.
	a := &store.Attachment{Slug: "pic", FilePath: "2026/11/pic.png", Mime: "image/png", Alt: "Base alt", Title: "Base title", Caption: "Base caption"}
	if err := srv.store.InsertAttachment(ctx, a); err != nil {
		t.Fatal(err)
	}
	prev := store.AttachmentFilter
	defer func() { store.AttachmentFilter = prev }()
	calls := 0
	store.AttachmentFilter = func(_ context.Context, list []*store.Attachment) {
		calls++
		for _, x := range list {
			x.Base = &store.AttachmentText{Alt: x.Alt, Title: x.Title, Caption: x.Caption, Description: x.Description}
			x.Alt, x.Caption = "Texte alt", "Légende"
		}
	}
	got, err := srv.store.GetAttachment(ctx, a.ID)
	if err != nil || got.Alt != "Texte alt" || got.Base == nil {
		t.Fatalf("overlay: %v %+v", err, got)
	}
	got.MenuOrder = 4
	if err := srv.store.UpdateAttachment(ctx, got); err != nil {
		t.Fatal(err)
	}
	if m, _ := srv.store.GetAttachments(ctx, []int64{a.ID}); m[a.ID] == nil || m[a.ID].Alt != "Texte alt" {
		t.Fatal("GetAttachments is not filtered")
	}
	if l, _, _ := srv.store.QueryAttachments(ctx, store.AttachmentQuery{IDs: []int64{a.ID}}); len(l) != 1 || l[0].Caption != "Légende" {
		t.Fatal("QueryAttachments is not filtered")
	}
	if calls != 3 {
		t.Errorf("filter calls %d, want 3", calls)
	}
	store.AttachmentFilter = nil
	raw, _ := srv.store.GetAttachment(ctx, a.ID)
	if raw.Alt != "Base alt" || raw.Caption != "Base caption" || raw.MenuOrder != 4 || raw.Base != nil {
		t.Fatalf("the translation overwrote the media row: %+v", raw)
	}
}
