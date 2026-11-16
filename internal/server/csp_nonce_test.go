package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/cspnonce"
	"wpgo/internal/store"
)

// The placeholder is swapped for one nonce per response, in headers and in text bodies, also when
// it is split across writes; binary bodies are untouched; a 304 carries no policy.
func TestNonceWriterSwap(t *testing.T) {
	ph := cspnonce.Placeholder
	rec := httptest.NewRecorder()
	nw := newNonceWriter(rec)
	nw.Header().Set("Content-Type", "text/html; charset=utf-8")
	nw.Header().Set("Content-Security-Policy-Report-Only", "script-src 'nonce-"+ph+"'")
	body := `<script nonce="` + ph + `">a</script><script nonce="` + ph + `">b</script>`
	cut := strings.Index(body, ph) + 7 // inside the first placeholder
	_, _ = nw.Write([]byte(body[:cut]))
	_, _ = nw.Write([]byte(body[cut:]))
	nw.close()
	got := rec.Body.String()
	nonce := string(nw.nonce)
	if strings.Contains(got, ph) || strings.Count(got, nonce) != 2 || len(got) != len(body) {
		t.Fatalf("body not swapped: %q", got)
	}
	if h := rec.Header().Get("Content-Security-Policy-Report-Only"); h != "script-src 'nonce-"+nonce+"'" {
		t.Fatalf("header not swapped: %q", h)
	}
	if n2 := string(newNonceWriter(httptest.NewRecorder()).nonce); n2 == nonce || len(n2) != cspnonce.Len {
		t.Fatalf("nonces repeat or have the wrong length: %q %q", nonce, n2)
	}

	rec = httptest.NewRecorder()
	nw = newNonceWriter(rec)
	nw.Header().Set("Content-Type", "image/png")
	_, _ = nw.Write([]byte(ph))
	nw.close()
	if rec.Body.String() != ph {
		t.Fatal("a binary body was rewritten")
	}

	rec = httptest.NewRecorder()
	nw = newNonceWriter(rec)
	nw.Header().Set("Content-Security-Policy", "script-src 'nonce-"+ph+"'")
	nw.WriteHeader(http.StatusNotModified)
	nw.close()
	if rec.Header().Get("Content-Security-Policy") != "" {
		t.Fatal("a 304 carries a policy")
	}

	// Event streams are swapped too (MCP over SSE carries rendered content).
	rec = httptest.NewRecorder()
	nw = newNonceWriter(rec)
	nw.Header().Set("Content-Type", "text/event-stream")
	_, _ = nw.Write([]byte("data: {\"x\":\"" + ph + "\"}\n\n"))
	nw.close()
	if strings.Contains(rec.Body.String(), ph) {
		t.Fatal("placeholder left in an event stream")
	}

	// A handler that writes nothing still gets its headers swapped.
	rec = httptest.NewRecorder()
	nw = newNonceWriter(rec)
	nw.Header().Set("Content-Security-Policy", "script-src 'nonce-"+ph+"'")
	nw.close()
	if strings.Contains(rec.Header().Get("Content-Security-Policy"), ph) {
		t.Fatal("placeholder left in the headers of an empty response")
	}
}

var reportOnlyNonce = regexp.MustCompile(`'nonce-([A-Za-z0-9_-]{22})'`)

// Every page, a page-cache hit included, carries its own nonce, the same in the report-only policy
// and on the page's scripts; the placeholder never leaves the server.
func TestCSPNonceFrontAndCache(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if err := srv.settings.Set(ctx, map[string]any{"wpgo_cache_enabled": true}); err != nil {
		t.Fatal(err)
	}
	srv.configureCache()
	var seen []string
	for i := 0; i < 3; i++ {
		resp := anonGet(t, ts.URL+"/")
		body := bodyOf(resp)
		ro := resp.Header.Get("Content-Security-Policy-Report-Only")
		m := reportOnlyNonce.FindStringSubmatch(ro)
		if m == nil {
			t.Fatalf("no nonce policy: %q", ro)
		}
		if strings.Contains(body, cspnonce.Placeholder) || strings.Contains(ro, cspnonce.Placeholder) {
			t.Fatal("the placeholder left the server")
		}
		if strings.Contains(body, "<script") && !strings.Contains(body, `nonce="`+m[1]+`"`) {
			t.Fatalf("page scripts do not carry the response's nonce %s", m[1])
		}
		for _, n := range seen {
			if n == m[1] {
				t.Fatalf("nonce reused (cache %q)", resp.Header.Get("X-WPGo-Cache"))
			}
		}
		seen = append(seen, m[1])
		if i == 2 && resp.Header.Get("X-WPGo-Cache") != "hit" {
			t.Logf("third request not a cache hit: %q", resp.Header.Get("X-WPGo-Cache"))
		}
	}

	// The admin gets the same treatment, with 'unsafe-eval' kept for wp.template.
	resp, err := c.Get(ts.URL + "/wp-admin/")
	if err != nil {
		t.Fatal(err)
	}
	body := bodyOf(resp)
	ro := resp.Header.Get("Content-Security-Policy-Report-Only")
	m := reportOnlyNonce.FindStringSubmatch(ro)
	if m == nil || !strings.Contains(ro, "'unsafe-eval'") || !strings.Contains(ro, "'strict-dynamic'") {
		t.Fatalf("admin nonce policy: %q", ro)
	}
	if !strings.Contains(body, `<script nonce="`+m[1]+`" src="/wp-admin/assets/js/csp-actions.js`) {
		t.Fatal("the admin does not load csp-actions.js with the response's nonce")
	}

	// A stray Upgrade header does not get a response past the swap.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	req.Header.Set("Upgrade", "x")
	if r, err := http.DefaultClient.Do(req); err != nil {
		t.Fatal(err)
	} else if b := bodyOf(r); strings.Contains(b, cspnonce.Placeholder) || strings.Contains(r.Header.Get("Content-Security-Policy-Report-Only"), cspnonce.Placeholder) {
		t.Fatal("the placeholder left the server on a request with an Upgrade header")
	}

	// Enforced mode moves the nonce policy into Content-Security-Policy, also for a page the cache
	// stored under the report mode.
	if err := srv.settings.Set(ctx, map[string]any{"csp_mode": cspModeEnforce}); err != nil {
		t.Fatal(err)
	}
	resp = anonGet(t, ts.URL+"/")
	_ = bodyOf(resp)
	if p := resp.Header.Get("Content-Security-Policy"); !strings.Contains(p, "'strict-dynamic'") || strings.Contains(p, "'unsafe-inline' 'nonce") ||
		strings.Contains(p, "script-src 'self' 'unsafe-inline'") || resp.Header.Get("Content-Security-Policy-Report-Only") != "" {
		t.Fatalf("enforced policy: %q / %q", p, resp.Header.Get("Content-Security-Policy-Report-Only"))
	}
}

// A script an unfiltered_html author stored in a post gets the page's nonce (its own nonce="…" is
// replaced); the same post outside a page (REST) keeps no nonce, nor does any post when
// csp_admin_scripts is off.
func TestCSPNonceStoredScripts(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	p := &store.Post{Type: "post", AuthorID: 1, Status: "publish", Title: "CSP probe",
		Content: `<p>x</p><script>window.ok=1</script><script nonce="guess">window.ok=2</script>`}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	scripts := regexp.MustCompile(`<script[^>]*>window\.ok=[12]</script>`)
	page := func() (string, string) {
		resp := anonGet(t, ts.URL+"/?p="+itoa64(p.ID))
		if loc := resp.Header.Get("Location"); loc != "" { // to the permalink
			resp = anonGet(t, loc)
		}
		body := bodyOf(resp)
		m := reportOnlyNonce.FindStringSubmatch(resp.Header.Get("Content-Security-Policy-Report-Only"))
		if m == nil {
			t.Fatal("no nonce policy")
		}
		if strings.Contains(body, cspnonce.Placeholder) {
			t.Fatal("placeholder left in the page")
		}
		return body, m[1]
	}
	body, nonce := page()
	if strings.Contains(body, "guess") {
		t.Fatal("the stored nonce was kept")
	}
	tags := scripts.FindAllString(body, -1)
	if len(tags) != 2 {
		t.Fatalf("stored scripts: %q", tags)
	}
	for _, tag := range tags {
		if !strings.Contains(tag, `nonce="`+nonce+`"`) {
			t.Fatalf("stored script without the page's nonce: %s", tag)
		}
	}
	if code, rest := restSend(t, c, ts.URL, http.MethodGet, "/wp-json/wp/v2/posts/"+itoa64(p.ID), ""); code != http.StatusOK ||
		!strings.Contains(rest, `\u003cscript\u003ewindow.ok=1`) || !strings.Contains(rest, `nonce=\"guess\"`) {
		t.Fatalf("REST rendered content: %d %s", code, rest)
	}

	if err := srv.settings.Set(ctx, map[string]any{"csp_admin_scripts": false}); err != nil {
		t.Fatal(err)
	}
	body, nonce = page()
	for _, tag := range scripts.FindAllString(body, -1) {
		if strings.Contains(tag, nonce) {
			t.Fatalf("csp_admin_scripts off, script got the nonce: %s", tag)
		}
	}
}

// Reports are collected (bounded, without query strings) and listed to administrators only.
func TestCSPReportEndpoint(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	cspReportLog.clear()
	report := `{"csp-report":{"document-uri":"` + ts.URL + `/hello?secret=1","blocked-uri":"inline","effective-directive":"script-src-elem","source-file":"` + ts.URL + `/x.js?v=2","line-number":7,"script-sample":"alert(1)"}}`
	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+cspReportPath, strings.NewReader(report))
		req.Header.Set("Content-Type", "application/csp-report")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("report: %d", resp.StatusCode)
		}
	}
	list, _ := cspReportLog.list()
	if len(list) != 1 || list[0].Count != 3 || list[0].Page != "/hello" || list[0].Source != ts.URL+"/x.js:7" || list[0].Directive != "script-src-elem" {
		t.Fatalf("collected: %+v", list)
	}
	if code, body := restSend(t, c, ts.URL, http.MethodGet, "/wp-json/pursae/v1/csp-reports", ""); code != http.StatusOK || !strings.Contains(body, `"count":3`) {
		t.Fatalf("list: %d %s", code, body)
	}
	resp, err := http.Get(ts.URL + "/wp-json/pursae/v1/csp-reports")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous list: %d", resp.StatusCode)
	}
}

var nonceAttrRe = regexp.MustCompile(` nonce=(?:"|&#34;|&quot;)[A-Za-z0-9_-]{22}(?:"|&#34;|&quot;)`) // in srcdoc too

// stripNonces drops the CSP nonce attributes, for tests that compare markup: every response has its
// own nonce (a page-cache hit included).
func stripNonces(s string) string { return nonceAttrRe.ReplaceAllString(s, "") }
