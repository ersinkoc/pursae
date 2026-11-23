package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWBLivePath(t *testing.T) {
	type want struct {
		path, query string
		ok          bool
	}
	for _, home := range []string{"https://site.test/", "https://site.test/blog/"} {
		pre := strings.TrimSuffix(strings.TrimPrefix(home, "https://site.test"), "/")
		for in, w := range map[string]want{
			home + "a/b/":                     {pre + "/a/b/", "", true},
			home + "?page_id=5":               {pre + "/", "page_id=5", true},
			home + "?p=5&preview=true":        {pre + "/", "p=5&preview=true", true},
			pre + "/a/":                       {pre + "/a/", "", true},
			"https://evil.test" + pre + "/x/": {},
			"//evil.test/x":                   {},
			"javascript:alert(1)":             {},
			"":                                {},
			home + "wp-admin/":                {},
			home + "wp-admin":                 {},
			home + "WP-Admin/post":            {},
			home + "wp-login.php":             {},
			home + "wp-json/wp/v2/users":      {},
			home + "wp-content/uploads/x/":    {},
			home + "xmlrpc.php":               {},
			home + "index.php":                {},
			home + "a/../wp-admin/":           {},
			home + "a//b/":                    {},
			home + "?rest_route=/wp/v2/users": {},
		} {
			p, q, ok := wbLivePath(in, home)
			if p != w.path || q != w.query || ok != w.ok {
				t.Errorf("wbLivePath(%q, %q) = %q %q %v, want %+v", in, home, p, q, ok, w)
			}
		}
	}
	// A page whose slug starts with "wp-" is a front-end page.
	if p, _, ok := wbLivePath("https://site.test/wp-guide/", "https://site.test/"); !ok || p != "/wp-guide/" {
		t.Errorf("wp-guide: %q %v", p, ok)
	}
	// Outside the home path.
	if _, _, ok := wbLivePath("https://site.test/other/", "https://site.test/blog/"); ok {
		t.Error("a path outside the home path was accepted")
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
	reset := func() {
		wbLiveFrames.Lock()
		wbLiveFrames.m, wbLiveFrames.order, wbLiveFrames.bytes = map[string]*wbLiveEntry{}, nil, 0
		wbLiveFrames.Unlock()
	}
	reset()
	t.Cleanup(reset)
	tok := wbLivePut(&wbLiveEntry{user: 1, path: "/", html: "x"})
	if !wbLiveTokenRE.MatchString(tok) || wbLiveGet(tok) == nil {
		t.Fatalf("token %q", tok)
	}
	for i := 0; i < wbLivePerUser+2; i++ {
		wbLivePut(&wbLiveEntry{user: 2, path: "/", html: "y"})
	}
	count := func(user int64) (n, bytes int) {
		wbLiveFrames.Lock()
		defer wbLiveFrames.Unlock()
		for _, e := range wbLiveFrames.m {
			if e.user == user {
				n++
				bytes += len(e.html)
			}
		}
		return n, bytes
	}
	if n, _ := count(2); n > wbLivePerUser {
		t.Errorf("user 2 keeps %d frames", n)
	}
	wbLiveFrames.Lock()
	wbLiveFrames.m[tok].at = time.Now().Add(-wbLiveTTL)
	wbLiveFrames.Unlock()
	if wbLiveGet(tok) != nil {
		t.Error("an expired frame is served")
	}
	// Expired frames are dropped on reads too.
	wbLiveFrames.Lock()
	_, kept := wbLiveFrames.m[tok]
	wbLiveFrames.Unlock()
	if kept {
		t.Error("an expired frame is kept after a read")
	}

	// Bytes: per user, and in all (the oldest go first); a frame larger than a user may keep is refused.
	reset()
	big := strings.Repeat("z", 7<<20)
	for range 5 {
		if wbLivePut(&wbLiveEntry{user: 3, path: "/", html: big}) == "" {
			t.Fatal("a 7 MB frame was refused")
		}
	}
	if _, b := count(3); b > wbLiveUserBytes {
		t.Errorf("user 3 keeps %d bytes", b)
	}
	for u := int64(10); u < 30; u++ {
		wbLivePut(&wbLiveEntry{user: u, path: "/", html: big})
		wbLivePut(&wbLiveEntry{user: u, path: "/", html: big})
	}
	wbLiveFrames.Lock()
	total, n := 0, len(wbLiveFrames.m)
	for _, e := range wbLiveFrames.m {
		total += wbLiveSize(e)
	}
	if total > wbLiveMaxTotal || total != wbLiveFrames.bytes || n != len(wbLiveFrames.order) {
		t.Errorf("kept %d frames, %d bytes (counted %d)", n, total, wbLiveFrames.bytes)
	}
	wbLiveFrames.Unlock()
	if n, _ := count(29); n != 2 {
		t.Errorf("the newest user keeps %d frames", n)
	}
	if wbLivePut(&wbLiveEntry{user: 4, path: "/", html: strings.Repeat("z", wbLiveUserBytes)}) != "" {
		t.Error("a frame larger than a user may keep was stored")
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
		"url":   "/wp-admin/", // ignored: the server builds the path from the post
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
	if hdr.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("referrer-policy %q", hdr.Get("Referrer-Policy"))
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
	// An Application Password needs the site-code scope (wbUnfiltered), as the trust route does.
	contentPass, _ := mcpTestAgent(t, base, admin.c, "Live content", []string{"content"})
	if code, out := wbsBasic(t, base, contentPass, http.MethodPost, wbrDocPath(post.ID, "/live-frame"), body); code != http.StatusForbidden {
		t.Errorf("content scope: %d %v", code, out)
	}
	themesPass, _ := mcpTestAgent(t, base, admin.c, "Live themes", []string{"content", "themes"})
	if code, out := wbsBasic(t, base, themesPass, http.MethodPost, wbrDocPath(post.ID, "/live-frame"), body); code != http.StatusOK {
		t.Errorf("themes scope: %d %v", code, out)
	}

	// A draft: its ?page_id= address, answered with the token like any other.
	draft := wbrPost(t, srv, "page", "Live draft", "draft", 1)
	code, v, _ = admin.do(http.MethodPost, wbrDocPath(draft.ID, "/live-frame"), body, nil)
	if code != http.StatusOK {
		t.Fatalf("draft: %d %v", code, v)
	}
	du, _ := wbrObj(v)["url"].(string)
	if !strings.HasPrefix(du, "/?") || !strings.Contains(du, "wb-live-frame=") || !strings.Contains(du, "="+strconv.FormatInt(draft.ID, 10)) {
		t.Fatalf("draft url %q", du)
	}
	if st, doc, _ := fetch(admin.c, base+du, "iframe"); st != http.StatusOK || !strings.Contains(doc, `<section data-wbc="1">`) {
		t.Errorf("draft frame: %d %.200s", st, doc)
	}
}
