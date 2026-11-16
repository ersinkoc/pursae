package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/cspnonce"
	"wpgo/internal/store"
)

// cspTrustScripts gives the placeholder to every script start tag, inside srcdoc documents too,
// drops a nonce the markup carries and leaves everything else byte for byte.
func TestCSPTrustScripts(t *testing.T) {
	ph := cspnonce.Placeholder
	for _, in := range []string{"", "plain <b>text</b> & more", "<p>x</p><!-- <script>no</script> -->", "abc <scr", "<iframe src=\"https://x\">", "<a href='x'>y",
		"<script>a<!--more-->", "<p>x</p><script src=x>a</script", "<script>never closed", "<script src=x/>"} {
		if got := cspTrustScripts(in); got != in {
			t.Fatalf("changed %q to %q", in, got)
		}
	}
	got := cspTrustScripts(`<p>a</p><SCRIPT type="text/javascript" nonce="guess" async>if (a<b && "</p>") x()</SCRIPT>` +
		`<iframe sandbox="allow-scripts" srcdoc="&lt;p&gt;hi&lt;/p&gt;&lt;script&gt;go(&quot;&amp;&quot;)&lt;/script&gt;" title='t'></iframe><textarea><script>t</script></textarea><script src="https://cdn.example/w.js"></script>`)
	want := `<p>a</p><script type="text/javascript" async=""` + cspnonce.Attr() + `>if (a<b && "</p>") x()</SCRIPT>` +
		`<iframe sandbox="allow-scripts" srcdoc="&lt;p&gt;hi&lt;/p&gt;&lt;script nonce=&#34;` + ph + `&#34;&gt;go(&#34;&amp;&#34;)&lt;/script&gt;" title="t"></iframe><textarea><script>t</script></textarea><script src="https://cdn.example/w.js"` + cspnonce.Attr() + `></script>`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "guess") {
		t.Fatal("kept the markup's own nonce")
	}
}

// pageNonce is the nonce of a response's policy (enforced or report-only) and its body.
func pageNonce(t *testing.T, resp *http.Response) (string, string) {
	t.Helper()
	body := bodyOf(resp)
	m := reportOnlyNonce.FindStringSubmatch(resp.Header.Get("Content-Security-Policy-Report-Only") + resp.Header.Get("Content-Security-Policy"))
	if m == nil {
		t.Fatalf("no nonce policy (%d)", resp.StatusCode)
	}
	if strings.Contains(body, cspnonce.Placeholder) {
		t.Fatal("placeholder left in the page")
	}
	return m[1], body
}

// The administrator's own Gooike snippets get the nonce (also in their inert form, which gooike.js
// copies); off, they keep none.
func TestCSPTrustGooikeSnippets(t *testing.T) {
	st := &gooikeSettings{Scripts: []gooikeScript{
		{Enabled: true, Placement: "head", Category: "necessary", Code: "window.n=1"},
		{Enabled: true, Placement: "head", Category: "analytics", Code: `<script src="https://a.example/t.js" nonce="x"></script>`},
	}}
	out := gooikeScriptsAt(st, "head", true)
	if strings.Count(out, cspnonce.Attr()) != 2 || strings.Contains(out, `nonce="x"`) || !strings.Contains(out, `type="text/plain" data-gooike-category="analytics"`) {
		t.Fatalf("trusted: %s", out)
	}
	if out := gooikeScriptsAt(st, "head", false); strings.Contains(out, cspnonce.Placeholder) {
		t.Fatalf("untrusted: %s", out)
	}
}

// The maintenance page's raw parts (unfiltered_html only) get the nonce of the response.
func TestCSPTrustMaintenance(t *testing.T) {
	ts, _, srv := maintenanceSite(t)
	maintEdit(t, srv, func(st *maintSettings) {
		st.Enabled = true
		st.Content = `<p>soon</p><script>window.c=1</script>`
		st.CustomHTML = `<script src="https://a.example/m.js"></script>`
	})
	nonce, body := pageNonce(t, anonGet(t, ts.URL+"/"))
	for _, want := range []string{`<script nonce="` + nonce + `">window.c=1</script>`, `<script src="https://a.example/m.js" nonce="` + nonce + `"></script>`} {
		if !strings.Contains(body, want) {
			t.Errorf("maintenance page lacks %s", want)
		}
	}
}

// A Web Builder html section's scripts (inside its srcdoc, which inherits the page's policy) get
// the page's nonce.
func TestCSPTrustWebBuilderSrcdoc(t *testing.T) {
	srv, base, _ := wbfStart(t)
	extra := `<iframe data-wb-embed="h1" sandbox="allow-scripts" srcdoc="&lt;p&gt;w&lt;/p&gt;&lt;script&gt;window.w=1&lt;/script&gt;"></iframe>`
	p := wbfPage(t, srv, "page", "Srcdoc WB", []string{"embed"}, "", extra)
	nonce, body := pageNonce(t, anonGet(t, wbfURL(base, p)))
	if !strings.Contains(body, `&lt;script nonce=&#34;`+nonce+`&#34;&gt;window.w=1&lt;/script&gt;`) {
		i := strings.Index(body, "srcdoc=")
		t.Fatalf("srcdoc script without the nonce: %.200s", body[max(i, 0):])
	}
}

// Settings › Security saves the mode and the administrator-scripts switch and clears the reports.
func TestCSPSecurityScreen(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	cspReportLog.clear()
	cspReportLog.add(cspViolationOf("script-src-elem", "inline", "", 0, "/probe-page", ""), time.Now())
	code, body, _ := get(t, c, ts.URL+"/wp-admin/options-security")
	if code != http.StatusOK || !strings.Contains(body, "Security Settings") || !strings.Contains(body, "/probe-page") ||
		!strings.Contains(body, `value="report" checked="checked"`) || !strings.Contains(body, `id="csp_admin_scripts" value="1" checked="checked"`) {
		t.Fatalf("screen: %d", code)
	}
	nonce := formNonce(t, body)
	post(t, c, ts.URL+"/wp-admin/options-security", url.Values{"_wpnonce": {nonce}, "csp_mode": {"enforce"}, "csp_admin_scripts": {"0"}})
	if srv.cspMode() != cspModeEnforce || srv.cspTrustOn() {
		t.Fatalf("saved: %s %v", srv.cspMode(), srv.cspTrustOn())
	}
	post(t, c, ts.URL+"/wp-admin/options-security", url.Values{"_wpnonce": {nonce}, "csp_mode": {"bogus"}, "csp_admin_scripts": {"0", "1"}})
	if srv.cspMode() != cspModeEnforce || srv.cspTrustOn() {
		t.Fatal("an invalid mode was saved")
	}
	post(t, c, ts.URL+"/wp-admin/options-security", url.Values{"_wpnonce": {nonce}, "clear_reports": {"Clear reports"}})
	if list, _ := cspReportLog.list(); len(list) != 0 {
		t.Fatal("reports not cleared")
	}
	post(t, c, ts.URL+"/wp-admin/options-security", url.Values{"_wpnonce": {"bad"}, "csp_mode": {"off"}})
	if srv.cspMode() != cspModeEnforce {
		t.Fatal("saved without a valid nonce")
	}
}

// A plugin filter keeps the nonce of a trusted script only on that script, unchanged.
func TestMaskNoncePlaceholderScripts(t *testing.T) {
	a := cspnonce.Attr()
	in := `<script` + a + `>ok()</script><p>x</p><iframe srcdoc="&lt;script nonce=&#34;` + cspnonce.Placeholder + `&#34;&gt;s()&lt;/script&gt;"></iframe>`
	if out := maskNoncePlaceholder(in, func(m string) string { return m + "<p>more</p>" }); out != in+"<p>more</p>" {
		t.Fatalf("kept: %s", out)
	}
	moved := maskNoncePlaceholder(`<script`+a+`>ok()</script>`, func(m string) string {
		return strings.Replace(strings.Replace(m, "ok()", "evil()", 1), "<script", "<script", 1)
	})
	if strings.Contains(moved, cspnonce.Placeholder) {
		t.Fatalf("changed script kept the nonce: %s", moved)
	}
	moved = maskNoncePlaceholder(`<script`+a+`>ok()</script>`, func(m string) string {
		i := strings.Index(m, `nonce="`)
		tok := m[i+7 : i+7+cspnonce.Len]
		return `<script>ok()</script><script nonce="` + tok + `">evil()</script>`
	})
	if strings.Contains(moved, cspnonce.Placeholder) {
		t.Fatalf("moved token kept the nonce: %s", moved)
	}
}

// The settings API changes raw code only for unfiltered_html users.
func TestCSPRawSettingsNeedUnfiltered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctxFor := func(u *store.User) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set(ctxUser, u)
		return c
	}
	lim := ctxFor(&store.User{ID: 2, Caps: map[string]bool{"manage_options": true}})
	full := ctxFor(&store.User{ID: 1, Caps: map[string]bool{"manage_options": true, "unfiltered_html": true}})
	srv := &Server{}
	old := maintSettings{Content: "<p>a</p>"}
	for _, st := range []maintSettings{{Content: "<p>a</p>", CustomHTML: "<script>x()</script>"}, {Content: "<script>x()</script>"}, {Content: "<p>a</p>", FooterText: "<script>x()</script>"}} {
		if len(srv.maintCheckRaw(lim, old, st)) == 0 || len(srv.maintCheckRaw(full, old, st)) != 0 {
			t.Errorf("maintenance %+v", st)
		}
	}
	if errs := srv.maintCheckRaw(lim, old, maintSettings{Content: "<p>b</p>"}); len(errs) != 0 {
		t.Errorf("filtered content refused: %v", errs)
	}
	g := gooikeSettings{Scripts: []gooikeScript{{ID: "s-1", Code: "a()"}}}
	add := gooikeSettings{Scripts: []gooikeScript{{ID: "s-1", Code: "a()"}, {ID: "s-2", Code: "b()"}}}
	edit := gooikeSettings{Scripts: []gooikeScript{{ID: "s-1", Code: "b()"}}}
	same := gooikeSettings{Scripts: []gooikeScript{{ID: "s-1", Code: "a()", Enabled: true}}}
	if len(srv.gooikeCheckRaw(lim, g, add)) == 0 || len(srv.gooikeCheckRaw(lim, g, edit)) == 0 || len(srv.gooikeCheckRaw(lim, g, same)) != 0 || len(srv.gooikeCheckRaw(full, g, edit)) != 0 {
		t.Error("gooike checks")
	}
}
