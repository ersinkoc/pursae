package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

func newJarClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

var setupTokenInstallForm = url.Values{
	"weblog_title": {"Token"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
}

func withSetupToken(form url.Values, token string) url.Values {
	out := url.Values{"setup_token": {token}}
	for k, v := range form {
		out[k] = v
	}
	return out
}

func adminCount(t *testing.T, srv *Server) int {
	t.Helper()
	var n int
	if err := srv.store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Without the token the installer shows only the token form, and a POST installs nothing.
func TestInstallSetupTokenMissingRejected(t *testing.T) {
	ts, _, srv := testServerBuilderOff(t)
	c := newJarClient()
	code, body, _ := get(t, c, ts.URL+"/wp-admin/install")
	if code != http.StatusForbidden || !strings.Contains(body, `name="token"`) || strings.Contains(body, `name="language"`) {
		t.Fatalf("install without token: %d, token form shown: %v", code, strings.Contains(body, `name="token"`))
	}
	if code, body, _ := post(t, c, ts.URL+"/wp-admin/install?step=2", setupTokenInstallForm); code != http.StatusForbidden || strings.Contains(body, "Success!") {
		t.Fatalf("POST without token: %d", code)
	}
	if n := adminCount(t, srv); n != 0 {
		t.Fatalf("users after tokenless install: %d", n)
	}
}

// A wrong token is refused, in the URL and in the form, and no cookie is handed out.
func TestInstallSetupTokenWrongRejected(t *testing.T) {
	ts, _, srv := testServerBuilderOff(t)
	c := newJarClient()
	code, body, _ := get(t, c, ts.URL+"/wp-admin/install?token=nope")
	if code != http.StatusForbidden || !strings.Contains(body, "not valid") {
		t.Fatalf("wrong token: %d", code)
	}
	if code, _, _ := post(t, c, ts.URL+"/wp-admin/install?step=2", withSetupToken(setupTokenInstallForm, testSetupToken+"x")); code != http.StatusForbidden {
		t.Fatalf("POST with wrong token: %d", code)
	}
	if code, _, _ := get(t, c, ts.URL+"/wp-admin/install"); code != http.StatusForbidden {
		t.Fatalf("a wrong token left a working cookie: %d", code)
	}
	// A forged cookie does not open it either.
	u, _ := url.Parse(ts.URL + "/wp-admin/install")
	c.Jar.SetCookies(u, []*http.Cookie{{Name: setupTokenCookie, Value: "99999999999.deadbeef", Path: "/wp-admin/install"}})
	if code, _, _ := get(t, c, ts.URL+"/wp-admin/install"); code != http.StatusForbidden {
		t.Fatalf("forged cookie: %d", code)
	}
	if n := adminCount(t, srv); n != 0 {
		t.Fatalf("users after wrong-token install: %d", n)
	}
}

// PURSAE_SETUP_TOKEN opens the installer: the link sets the cookie and drops the token from the URL,
// the cookie carries the visitor through the language step to the install.
func TestInstallSetupTokenFromEnv(t *testing.T) {
	ts, _, srv := testServerBuilderOff(t)
	c := newJarClient()
	code, _, loc := get(t, c, ts.URL+"/wp-admin/install?token="+testSetupToken+"&language=en_US")
	if code != http.StatusSeeOther || loc != "/wp-admin/install?language=en_US" {
		t.Fatalf("env token link: %d -> %q", code, loc)
	}
	if code, body, _ := get(t, c, ts.URL+loc); code != http.StatusOK || !strings.Contains(body, `name="user_name"`) {
		t.Fatalf("install form after the token: %d", code)
	}
	if code, body, _ := post(t, c, ts.URL+"/wp-admin/install?step=2", setupTokenInstallForm); code != http.StatusOK || !strings.Contains(body, "Success!") {
		t.Fatalf("install with the cookie: %d", code)
	}
	if n := adminCount(t, srv); n != 1 {
		t.Fatalf("users: %d", n)
	}
}

// Without PURSAE_SETUP_TOKEN the server generates a token: one per site however many processes start,
// it opens the installer, `wpgo setup-token` (IssueSetupToken) replaces it, and the install revokes it.
func TestInstallSetupTokenGenerated(t *testing.T) {
	ts, c, srv := newTestServerSetup(t, false, "")
	ctx := context.Background()
	if code, _, _ := get(t, c, ts.URL+"/wp-admin/install?token=anything"); code != http.StatusForbidden {
		t.Fatalf("before any token was issued: %d", code)
	}
	token, err := srv.EnsureSetupToken(ctx)
	if err != nil || len(token) < 40 {
		t.Fatalf("generated token %q: %v", token, err)
	}
	if again, err := srv.EnsureSetupToken(ctx); err != nil || again != "" {
		t.Fatalf("a second start generated another token: %q %v", again, err)
	}
	var stored []byte
	if err := srv.store.Pool.QueryRow(ctx, `SELECT token_hash FROM setup_token`).Scan(&stored); err != nil || strings.Contains(string(stored), token) {
		t.Fatalf("stored token: %v (plain text kept: %v)", err, strings.Contains(string(stored), token))
	}
	if !strings.HasSuffix(SetupURL(ts.URL+"/", token), "/wp-admin/install?token="+token) {
		t.Fatalf("setup URL: %s", SetupURL(ts.URL+"/", token))
	}

	// The CLI issues a new token: the old one and its cookie stop working.
	old := newJarClient()
	if code, _, _ := get(t, old, ts.URL+"/wp-admin/install?token="+url.QueryEscape(token)); code != http.StatusSeeOther {
		t.Fatalf("generated token: %d", code)
	}
	fresh, err := IssueSetupToken(ctx, srv.store.Pool)
	if err != nil || fresh == token {
		t.Fatalf("issue: %q %v", fresh, err)
	}
	if code, _, _ := get(t, old, ts.URL+"/wp-admin/install"); code != http.StatusForbidden {
		t.Fatalf("cookie of the replaced token: %d", code)
	}
	if code, _, _ := get(t, c, ts.URL+"/wp-admin/install?token="+url.QueryEscape(token)); code != http.StatusForbidden {
		t.Fatalf("replaced token: %d", code)
	}

	if code, _, _ := get(t, c, ts.URL+"/wp-admin/install?token="+url.QueryEscape(fresh)); code != http.StatusSeeOther {
		t.Fatalf("new token: %d", code)
	}
	if code, body, _ := post(t, c, ts.URL+"/wp-admin/install?step=2", setupTokenInstallForm); code != http.StatusOK || !strings.Contains(body, "Success!") {
		t.Fatalf("install with the generated token: %d", code)
	}

	// Installed: the token is gone and cannot be reissued.
	var left int
	if err := srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM setup_token`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("setup tokens after install: %d %v", left, err)
	}
	if _, err := IssueSetupToken(ctx, srv.store.Pool); err == nil {
		t.Fatal("issued a setup token on an installed site")
	}
	if tok, err := srv.EnsureSetupToken(ctx); err != nil || tok != "" {
		t.Fatalf("start-up generated a token on an installed site: %q %v", tok, err)
	}
}

// After the install the token opens nothing: the installer sends everyone to the login, and a second
// submission with the token creates no administrator.
func TestInstallSetupTokenInvalidAfterInstall(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	if code, body, _ := post(t, c, ts.URL+"/wp-admin/install?step=2", setupTokenInstallForm); code != http.StatusOK || !strings.Contains(body, "Success!") {
		t.Fatalf("install: %d", code)
	}
	other := newJarClient()
	if code, _, loc := get(t, other, ts.URL+"/wp-admin/install?token="+testSetupToken); code != http.StatusFound || loc != "/wp-login" {
		t.Fatalf("token after install: %d -> %q", code, loc)
	}
	form := withSetupToken(setupTokenInstallForm, testSetupToken)
	form.Set("user_name", "intruder")
	form.Set("admin_email", "intruder@example.test")
	if code, _, loc := post(t, other, ts.URL+"/wp-admin/install?step=2", form); code != http.StatusFound || loc != "/wp-login" {
		t.Fatalf("second install: %d -> %q", code, loc)
	}
	if n := adminCount(t, srv); n != 1 {
		t.Fatalf("users after a second install: %d", n)
	}
}
