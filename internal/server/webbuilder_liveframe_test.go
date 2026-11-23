package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWBLivePath(t *testing.T) {
	for in, want := range map[string][2]string{
		"https://site.test/a/b/":       {"/a/b/", ""},
		"https://site.test/?page_id=5": {"/", "page_id=5"},
		"//evil.test/x":                {"/x", ""}, // the host is dropped: only the path counts
		"javascript:alert(1)":          {"/", ""},
		"":                             {"/", ""},
	} {
		if p, q := wbLivePath(in); p != want[0] || q != want[1] {
			t.Errorf("wbLivePath(%q) = %q %q, want %q", in, p, q, want)
		}
	}
}

func TestWBLiveFillKeepsIDs(t *testing.T) {
	sec := func(id, html, wrap string, tail bool) json.RawMessage {
		c := map[string]string{"html": html, "wrap": wrap, "head": `<link rel="stylesheet" href="/k/a.css">`}
		if tail {
			c["tail"] = "1"
		}
		b, _ := json.Marshal(map[string]any{"id": id, "kind": "code", "content": c})
		return b
	}
	p := wbLiveFill(7, []json.RawMessage{
		sec("s1", `<section data-wbc="1"><h1 data-wbc="2">Hi</h1></section>`, `<div class="shell">`, false),
		sec("s2", `<p data-wbc="3">x</p>`, `<div class="shell">`, false),
		sec("t1", `<script src="/k/app.js"></script>`, "", true),
	})
	if p == nil {
		t.Fatal("nil part")
	}
	if !strings.Contains(p.html, `<div class="shell"><section data-wbc="1"><h1 data-wbc="2">Hi</h1></section><p data-wbc="3">x</p></div>`) {
		t.Errorf("filled: %s", p.html)
	}
	if strings.Contains(p.html, "data-wb-live-keep") || strings.Contains(p.html, `data-wb-code="`) {
		t.Errorf("editor attributes left: %s", p.html)
	}
	if !strings.Contains(p.head, "/k/a.css") {
		t.Errorf("head: %q", p.head)
	}
	if p.info == nil || len(p.info.First) != 1 {
		t.Errorf("info: %+v", p.info)
	}
}

func TestWBLiveFrames(t *testing.T) {
	tok := wbLivePut(&wbLiveEntry{user: 1, path: "/", html: "x"})
	if !wbLiveTokenRE.MatchString(tok) || wbLiveGet(tok) == nil {
		t.Fatalf("token %q", tok)
	}
	for i := 0; i < wbLivePerUser+2; i++ {
		wbLivePut(&wbLiveEntry{user: 2, path: "/", html: "y"})
	}
	n := 0
	wbLiveFrames.Lock()
	for _, e := range wbLiveFrames.m {
		if e.user == 2 {
			n++
		}
	}
	wbLiveFrames.m[tok].at = time.Now().Add(-wbLiveTTL)
	wbLiveFrames.Unlock()
	if n > wbLivePerUser {
		t.Errorf("user 2 keeps %d frames", n)
	}
	if wbLiveGet(tok) != nil {
		t.Error("an expired frame is served")
	}
}

// TestWBLiveFrameREST: the frame is rendered like the published page and served at the page's
// path, to its user only, as a frame only, with the nonce on the design's scripts.
func TestWBLiveFrameREST(t *testing.T) {
	srv, base, admin := wbrStart(t)
	if err := srv.settings.Set(t.Context(), map[string]any{"csp_mode": cspModeEnforce}); err != nil {
		t.Fatal(err)
	}
	post := wbrPost(t, srv, "page", "Live page", "publish", 1)
	sec := func(id, html, head string) map[string]any {
		return map[string]any{"id": id, "kind": "code", "content": map[string]any{"html": html, "head": head, "wrap": `<div class="shell">`, "lang": "tr",
			"htmlAttrs": `class="no-js" data-theme="light"`, "bodyAttrs": `class="antialiased"`}}
	}
	body := map[string]any{
		"url":   base + "/live-page/",
		"title": "Live",
		"shell": "theme",
		"page": []any{sec("p1", `<section data-wbc="1"><canvas data-wbc="2"></canvas><script type="module">import "x"</script></section>`,
			`<script type="importmap">{"imports":{"x":"/k/x.js"}}</script><script>document.documentElement.classList.add('dark')</script>`)},
		"header": []any{sec("h1", `<header data-wbc="h:1">H</header>`, `<link rel="stylesheet" href="/k/h.css">`)},
		"footer": []any{sec("f1", `<footer data-wbc="f:1">F</footer>`, "")},
		"hook":   "window.hook=1",
		"css":    map[string]any{"kit": ".a{}", "local": `[data-wbc="1"]{color:red}</style><script>`, "edit": ""},
	}
	code, v, _ := admin.do(http.MethodPost, wbrDocPath(post.ID, "/live-frame"), body, nil)
	if code != http.StatusOK {
		t.Fatalf("post: %d %v", code, v)
	}
	u, _ := wbrObj(v)["url"].(string)
	if !strings.HasPrefix(u, "/live-page/?wb-live-frame=") {
		t.Fatalf("url %q", u)
	}
	fetch := func(c *http.Client, url, dest string) (int, string, http.Header) {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		if dest != "" {
			req.Header.Set("Sec-Fetch-Dest", dest)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}
	st, doc, hdr := fetch(admin.c, base+u, "iframe")
	if st != http.StatusOK {
		t.Fatalf("get: %d %s", st, doc)
	}
	for _, want := range []string{
		"<!DOCTYPE html>\n<html lang=\"tr\" class=\"no-js\" data-theme=\"light\">",
		`<body class="antialiased">`,
		`<div class="shell"><header data-wbc="h:1">H</header>`,
		`<main id="content" class="site-main wb-main-part">`,
		`<section data-wbc="1"><canvas data-wbc="2"></canvas>`,
		`<footer data-wbc="f:1">F</footer></div>`,
		`<style data-wb-local>[data-wbc="1"]{color:red}<\/style><script></style>`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("missing %q in\n%s", want, doc)
		}
	}
	// Page head first, then the header's; every script with this response's nonce.
	if strings.Index(doc, "importmap") > strings.Index(doc, "/k/h.css") {
		t.Errorf("head order:\n%s", doc)
	}
	csp := hdr.Get("Content-Security-Policy")
	i := strings.Index(csp, "'nonce-")
	if i < 0 {
		t.Fatalf("no nonce policy: %q", csp)
	}
	nonce := csp[i+7 : i+7+strings.Index(csp[i+7:], "'")]
	if strings.Count(doc, `nonce="`+nonce+`"`) != 4 || !strings.Contains(doc, "<head>\n<script nonce=\""+nonce+"\">window.hook=1</script><meta charset") {
		t.Errorf("scripts without the nonce %q:\n%s", nonce, doc)
	}
	if hdr.Get("Cache-Control") != "no-store, private" {
		t.Errorf("cache-control %q", hdr.Get("Cache-Control"))
	}
	// Not as a top-level document, not at another path, not for another user, not logged out.
	if st, _, _ := fetch(admin.c, base+u, "document"); st != http.StatusNotFound {
		t.Errorf("top-level: %d", st)
	}
	if st, _, _ := fetch(admin.c, base+strings.Replace(u, "/live-page/", "/other/", 1), "iframe"); st != http.StatusNotFound {
		t.Errorf("other path: %d", st)
	}
	if st, _, _ := fetch(&http.Client{}, base+u, "iframe"); st != http.StatusNotFound {
		t.Errorf("logged out: %d", st)
	}
	other := wbrUser(t, srv, base, "liveadmin2", "administrator")
	if st, _, _ := fetch(other.c, base+u, "iframe"); st != http.StatusNotFound {
		t.Errorf("another user: %d", st)
	}
	// Without unfiltered_html there is no live frame.
	author := wbrUser(t, srv, base, "liveauthor", "author")
	if code, _, _ := author.do(http.MethodPost, wbrDocPath(post.ID, "/live-frame"), body, nil); code != http.StatusForbidden {
		t.Errorf("author: %d", code)
	}
}
