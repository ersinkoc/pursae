package server

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"wpgo/internal/auth"
	"wpgo/internal/store"
)

func TestPreviewSandboxCSP(t *testing.T) {
	for in, want := range map[string]string{
		"": "sandbox allow-scripts allow-forms allow-popups; frame-ancestors 'self'",
		"default-src 'self'; script-src 'nonce-x' 'strict-dynamic'; frame-ancestors 'self'": "sandbox allow-scripts allow-forms allow-popups; default-src 'self'; script-src 'nonce-x' 'strict-dynamic'; frame-ancestors 'self'",
		// An upload's own, stricter sandbox stays.
		"sandbox; default-src 'none'; img-src 'self' data:": "sandbox; default-src 'none'; img-src 'self' data:; frame-ancestors 'self'",
		// A sandbox that allows more than a preview may is replaced.
		"sandbox allow-scripts allow-same-origin; default-src 'self'":    "sandbox allow-scripts allow-forms allow-popups; default-src 'self'; frame-ancestors 'self'",
		"SANDBOX allow-top-navigation; frame-ancestors 'none'":           "sandbox allow-scripts allow-forms allow-popups; frame-ancestors 'none'",
		"default-src 'self'; sandbox allow-popups-to-escape-sandbox;  ;": "sandbox allow-scripts allow-forms allow-popups; default-src 'self'; frame-ancestors 'self'",
	} {
		if got := previewSandboxCSP(in); got != want {
			t.Errorf("previewSandboxCSP(%q)\n got %q\nwant %q", in, got, want)
		}
	}

	h := http.Header{}
	h.Add("Content-Security-Policy", "default-src 'self'")
	h.Add("Content-Security-Policy", "script-src 'self'")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Cache-Control", "public, max-age=31536000")
	previewSandboxHeaders(h)
	if vs := h.Values("Content-Security-Policy"); len(vs) != 2 || !strings.HasPrefix(vs[0], previewSandboxPolicy+"; ") || vs[1] != "script-src 'self'" {
		t.Errorf("CSP %q", vs)
	}
	if h.Get("X-Frame-Options") != "" || h.Get("Access-Control-Allow-Origin") != "*" || h.Get("Referrer-Policy") != "same-origin" ||
		h.Get("Cache-Control") != "private, public, max-age=31536000" {
		t.Errorf("headers %v", h)
	}
	h = http.Header{}
	previewSandboxHeaders(h)
	if h.Get("Content-Security-Policy") != previewSandboxPolicy+"; frame-ancestors 'self'" || h.Get("Cache-Control") != "private" {
		t.Errorf("headers of a response without CSP: %v", h)
	}
}

func TestPreviewNavigation(t *testing.T) {
	for _, tc := range []struct {
		method string
		hdr    map[string]string
		want   bool
	}{
		{http.MethodGet, map[string]string{"Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe"}, true},
		{http.MethodGet, map[string]string{"Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}, true},
		{http.MethodGet, map[string]string{"Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "style"}, false},
		{http.MethodGet, map[string]string{"Sec-Fetch-Mode": "cors", "Upgrade-Insecure-Requests": "1"}, false},
		{http.MethodGet, map[string]string{"Upgrade-Insecure-Requests": "1"}, true},
		{http.MethodGet, nil, false},
		{http.MethodHead, map[string]string{"Sec-Fetch-Mode": "navigate"}, false},
		{http.MethodPost, map[string]string{"Sec-Fetch-Mode": "navigate"}, false},
	} {
		r, _ := http.NewRequest(tc.method, "http://x/wp-admin/theme-preview/a/", nil)
		for k, v := range tc.hdr {
			r.Header.Set(k, v)
		}
		if got := previewNavigation(r); got != tc.want {
			t.Errorf("%s %v: %v, want %v", tc.method, tc.hdr, got, tc.want)
		}
	}
}

// TestThemePreviewSandboxedFrame: Live Preview runs the previewed theme in an opaque origin. Every
// sandbox response carries the CSP sandbox (no allow-same-origin) and no X-Frame-Options; a browser
// loading the cookie-checked preview is sent on to the token address, which serves the preview
// without a cookie, keeps every link under the token, and refuses a forged, expired, foreign or
// unprivileged token, writes and the sandbox's dashboard.
func TestThemePreviewSandboxedFrame(t *testing.T) {
	t.Setenv("PURSAE_THEMES_DIR", t.TempDir())
	lib := libraryCopy(t)
	ts, c, srv := testServerBuilderOff(t)
	srv.themeLibrary = os.DirFS(lib)
	t.Cleanup(srv.closePreviews)
	t.Cleanup(func() {
		unregisterTheme("wpgo-fixture")
		unregisterTheme("wpgo-fixture-two")
	})
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Main Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK {
		t.Fatalf("install: %d %s", code, body)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	admin, err := srv.store.UserByLoginOrEmail(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}

	// The screen still frames the cookie-checked address (the parent never reaches into the frame).
	_, screen, _ := get(t, c, base+"/wp-admin/theme-preview?theme=wpgo-fixture")
	if !strings.Contains(screen, `<iframe src="/wp-admin/theme-preview/wpgo-fixture/"`) || strings.Contains(screen, "contentWindow") || strings.Contains(screen, "contentDocument") {
		t.Fatal("preview screen frame")
	}
	waitReady(t, c, base, "wpgo-fixture")

	sandboxed := func(t *testing.T, what string, h http.Header) {
		t.Helper()
		csp := h.Get("Content-Security-Policy")
		if !strings.HasPrefix(csp, "sandbox allow-scripts allow-forms allow-popups;") || strings.Contains(csp, "allow-same-origin") ||
			!strings.Contains(csp, "frame-ancestors 'self'") || h.Get("X-Frame-Options") != "" || len(h.Values("Set-Cookie")) != 0 ||
			!strings.Contains(h.Get("Cache-Control"), "private") {
			t.Errorf("%s: headers %v", what, h)
		}
	}
	fetch := func(cl *http.Client, u string, hdr map[string]string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}

	// A tool without Fetch Metadata is served at the cookie address, sandboxed as well.
	resp, page := fetch(c, base+"/wp-admin/theme-preview/wpgo-fixture/", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "fixture-page-wide") {
		t.Fatalf("cookie address: %d", resp.StatusCode)
	}
	sandboxed(t, "cookie address", resp.Header)

	// A browser loading the frame is sent to the token address (path and query kept).
	nav := map[string]string{"Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe", "Sec-Fetch-Site": "same-origin"}
	resp, _ = fetch(c, base+"/wp-admin/theme-preview/wpgo-fixture/blog/?x=1", nav)
	loc := resp.Header.Get("Location")
	m := regexp.MustCompile(`^/wp-content/theme-preview/wpgo-fixture/([0-9a-z]+\.[0-9a-z]+\.[0-9a-f]{32})/blog/\?x=1$`).FindStringSubmatch(loc)
	if resp.StatusCode != http.StatusFound || m == nil || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("navigation: %d %q %v", resp.StatusCode, loc, resp.Header)
	}
	token := m[1]
	if srv.previewTokenUser(context.Background(), "wpgo-fixture", token, time.Now()) == nil {
		t.Fatal("the minted token does not verify")
	}
	prefix := previewFramePrefix("wpgo-fixture", token)

	// The sandboxed frame sends no cookie: the token alone serves the preview, its assets and links.
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, page = fetch(anon, base+prefix+"/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token address: %d %s", resp.StatusCode, page)
	}
	sandboxed(t, "token address", resp.Header)
	for _, want := range []string{"fixture-page-wide", prefix + "/wp-content/themes/wpgo-fixture/style.css?ver=1.1.0", "Fixture Journal"} {
		if !strings.Contains(page, want) {
			t.Fatalf("token preview lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "/wp-admin/theme-preview/wpgo-fixture/") {
		t.Fatal("the token preview links to the cookie address")
	}
	checkPreviewLinks(t, page, base, prefix)
	resp, page = fetch(anon, base+prefix+"/blog/", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "First light") {
		t.Fatalf("token posts page: %d", resp.StatusCode)
	}
	checkPreviewLinks(t, page, base, prefix)
	resp, css := fetch(anon, base+prefix+"/wp-content/themes/wpgo-fixture/style.css?ver=1.1.0", nil)
	if resp.StatusCode != http.StatusOK || css == "" || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("token stylesheet: %d %v", resp.StatusCode, resp.Header)
	}
	sandboxed(t, "token stylesheet", resp.Header)
	if resp, _ := fetch(anon, base+prefix, nil); resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != prefix+"/" {
		t.Fatalf("bare token address: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	// Page cache or not, a token response is never stored for others.
	if resp, _ := fetch(anon, base+prefix+"/about", nil); resp.StatusCode != http.StatusMovedPermanently ||
		!strings.HasPrefix(resp.Header.Get("Location"), base+prefix+"/about/") || !strings.Contains(resp.Header.Get("Cache-Control"), "private") {
		t.Fatalf("canonical redirect: %d %v", resp.StatusCode, resp.Header)
	}

	// Refused: writes, the sandbox's dashboard and login, the cookie address for visitors.
	for _, p := range []string{"/", "/wp-comments-post", "/wp-json/wp/v2/posts"} {
		resp, err := anon.PostForm(base+prefix+p, url.Values{"comment": {"x"}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("POST %s: %d", p, resp.StatusCode)
		}
	}
	for _, p := range []string{"/wp-admin/", "/wp-admin/options-general", "/wp-login"} {
		if resp, _ := fetch(anon, base+prefix+p, nil); resp.StatusCode != http.StatusForbidden {
			t.Errorf("GET %s: %d", p, resp.StatusCode)
		}
	}
	if resp, _ := fetch(anon, base+"/wp-admin/theme-preview/wpgo-fixture/", nav); resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "/wp-login") {
		t.Errorf("visitor at the cookie address: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Bad tokens: forged, for another theme, expired, too far in the future, for a user who may not preview.
	hash, _ := auth.HashPassword("editor pass")
	editorID, err := srv.store.CreateUser(context.Background(), store.NewUser{Login: "writer", Email: "writer@example.test", PasswordHash: hash, Role: "editor"})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	forged := parts[0] + "." + parts[1] + "." + strings.Repeat("0", 32)
	future := time.Now().Add(48 * time.Hour)
	for name, bad := range map[string]string{
		"forged":  previewFramePrefix("wpgo-fixture", forged),
		"foreign": previewFramePrefix("wpgo-fixture-two", token),
		"expired": previewFramePrefix("wpgo-fixture", srv.previewToken("wpgo-fixture", admin.ID, time.Now().Add(-8*time.Hour))),
		"future":  previewFramePrefix("wpgo-fixture", srv.previewToken("wpgo-fixture", admin.ID, future)),
		"editor":  previewFramePrefix("wpgo-fixture", srv.previewToken("wpgo-fixture", editorID, time.Now())),
		"garbage": previewFramePrefix("wpgo-fixture", "x.y.z"),
		"slug":    previewFrameRoute + "/..%2Fwp-admin/" + token,
	} {
		resp, body := fetch(anon, base+bad+"/", nil)
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s token: %d", name, resp.StatusCode)
		}
		if strings.Contains(body, "fixture-page-wide") {
			t.Errorf("%s token served the preview", name)
		}
	}

	// While a sandbox is built the frame shows Preparing preview, which is not sandboxed: its status
	// poll needs the login cookie.
	get(t, c, base+"/wp-admin/theme-preview?theme=wpgo-fixture-two")
	two := previewFramePrefix("wpgo-fixture-two", srv.previewToken("wpgo-fixture-two", admin.ID, time.Now()))
	if resp, page := fetch(anon, base+two+"/", nil); strings.Contains(page, "Preparing preview") && strings.HasPrefix(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Error("the Preparing preview page is sandboxed")
	}
	waitReady(t, c, base, "wpgo-fixture-two")
	if resp, page := fetch(anon, base+two+"/", nil); resp.StatusCode != http.StatusOK || !strings.Contains(page, "Hello world!") {
		t.Fatalf("second preview: %d", resp.StatusCode)
	} else {
		sandboxed(t, "second preview", resp.Header)
	}
}
