package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"wpgo/internal/auth"
)

// Account lockout redesign (review 2026-11-26 O3/D5): failures at an account from many networks must not
// lock its owner out; brute force from one network (an IPv4 address, an IPv6 /64) still is; Application
// Passwords are not refused because of wrong account passwords.

// stubLoginSlowdown makes the wait at an account under attack instant for the test and returns a
// function listing the waits asked for so far.
func stubLoginSlowdown(t *testing.T) func() []time.Duration {
	t.Helper()
	var mu sync.Mutex
	var got []time.Duration
	old := loginSlowdown
	loginSlowdown = func(_ context.Context, d time.Duration) {
		if d > 0 {
			mu.Lock()
			got = append(got, d)
			mu.Unlock()
		}
	}
	t.Cleanup(func() { loginSlowdown = old })
	return func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Duration(nil), got...)
	}
}

// fromAddr sends a request as the client address addr: the test client is on loopback, a default trusted
// proxy, so X-Forwarded-For is believed.
func fromAddr(t *testing.T, c *http.Client, method, u, addr string, form url.Values, basic ...string) (int, string, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, u, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("X-Forwarded-For", addr)
	if len(basic) == 2 {
		req.SetBasicAuth(basic[0], basic[1])
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header.Get("Location")
}

func loginFrom(t *testing.T, base, addr, user, pass string) (int, string, string) {
	t.Helper()
	return fromAddr(t, asNewClient(), http.MethodPost, base+"/wp-login", addr, url.Values{"log": {user}, "pwd": {pass}})
}

var appPassShownRe = regexp.MustCompile(`id="new-application-password-value" type="text" class="code" readonly="readonly" value="([^"]+)"`)

// newAppPassword creates an Application Password for the user logged in on c and returns it.
func newAppPassword(t *testing.T, c *http.Client, base string) string {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/profile")
	_, body, _ = post(t, c, base+"/wp-admin/profile", url.Values{"_wpnonce": {formNonce(t, body)}, "do_new_application_password": {"1"}, "new_application_password_name": {"Deploy"}})
	m := appPassShownRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no application password")
	}
	return m[1]
}

// Core (Pursae Security off).
func TestLoginLockoutCoreDistributed(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	appPass := newAppPassword(t, c, base)
	waits := stubLoginSlowdown(t)
	ctx := context.Background()
	if _, err := getPool(t).Exec(ctx, `DELETE FROM login_attempts`); err != nil { // forget the admin's known network
		t.Fatal(err)
	}

	// Brute force from one IPv6 /64, rotating addresses inside it: the sixth guess is refused, even with
	// the right password.
	for i := 1; i <= auth.MaxFailedLogins; i++ {
		if _, body, _ := loginFrom(t, base, fmt.Sprintf("2001:db8:bad:1::%x", i), "admin", "wrong"); strings.Contains(body, "Too many") {
			t.Fatalf("guess %d from the /64 refused too early", i)
		}
	}
	if code, body, _ := loginFrom(t, base, "2001:db8:bad:1:ffff::1", "admin", "correct horse"); code == http.StatusFound || !strings.Contains(body, "Too many failed login attempts") {
		t.Fatalf("a sixth sign-in from the same /64 was not refused: %d", code)
	}

	// The report's case: an Application Password from that same /64 is not refused because of the wrong
	// account passwords.
	if code, _, _ := fromAddr(t, asNewClient(), http.MethodGet, base+"/wp-json/wp/v2/users/me", "2001:db8:bad:1::77", nil, "admin", appPass); code != http.StatusOK {
		t.Errorf("Application Password from the throttled network: %d, want 200", code)
	}

	// Failures spread over many networks put the account past the old per-login limit.
	for i := 0; i < auth.MaxFailedLoginsPerLogin+5; i++ {
		if _, body, _ := loginFrom(t, base, fmt.Sprintf("2001:db8:%x::1", i+1), "admin", "wrong"); strings.Contains(body, "Too many") {
			t.Fatalf("distributed guess %d refused", i)
		}
	}
	if n := len(waits()); n == 0 {
		t.Error("no network was slowed down at an account under attack")
	}
	// The owner, from a network that has not failed: signed in (after a capped wait).
	before := len(waits())
	if code, _, loc := loginFrom(t, base, "198.51.100.77", "admin", "correct horse"); code != http.StatusFound || !strings.HasSuffix(loc, "/wp-admin/") {
		t.Fatalf("owner refused from a fresh network after distributed failures: %d %s", code, loc)
	}
	if w := waits(); len(w) != before+1 || w[len(w)-1] > auth.MaxAccountDelay {
		t.Errorf("owner's wait: %v", w[before:])
	}
	// The network that just signed in is known now: no wait any more.
	before = len(waits())
	if code, _, _ := loginFrom(t, base, "198.51.100.77", "admin", "correct horse"); code != http.StatusFound {
		t.Fatalf("second sign-in: %d", code)
	}
	if len(waits()) != before {
		t.Error("a known network was slowed down")
	}
	// Application Passwords from any network keep working.
	if code, _, _ := fromAddr(t, asNewClient(), http.MethodGet, base+"/wp-json/wp/v2/users/me", "203.0.113.99", nil, "admin", appPass); code != http.StatusOK {
		t.Errorf("Application Password after distributed failures: %d", code)
	}
}

// Pursae Security on, default lockout settings (5 per network, 5 per account, 20 minutes, a 24-hour
// lockout after 4): ~25 failures from many networks used to lock the account for 24 hours.
func TestLoginLockoutAdminSecDistributed(t *testing.T) {
	base, c, srv := asStart(t)
	appPass := newAppPassword(t, c, base)
	waits := stubLoginSlowdown(t)
	ctx := context.Background()
	if _, err := getPool(t).Exec(ctx, `DELETE FROM login_attempts`); err != nil {
		t.Fatal(err)
	}
	st := srv.asLoad()
	if st.IPMaxFailures != 5 || st.UserMaxFailures != 5 {
		t.Fatalf("defaults changed: %d/%d", st.IPMaxFailures, st.UserMaxFailures)
	}
	for i := 0; i < 30; i++ {
		if _, body, _ := loginFrom(t, base, fmt.Sprintf("2001:db8:%x::1", i+1), "admin", "wrong"); strings.Contains(body, "Too many") {
			t.Fatalf("distributed guess %d refused", i)
		}
	}
	if n := asCount(t, `SELECT count(*) FROM adminsec_lockouts WHERE NOT released AND until > now()`); n != 0 {
		t.Errorf("distributed failures created %d lockouts", n)
	}
	if len(waits()) == 0 {
		t.Error("no network was slowed down at an account under attack")
	}
	if code, _, loc := loginFrom(t, base, "198.51.100.77", "admin", "correct horse"); code != http.StatusFound || !strings.HasSuffix(loc, "/wp-admin/") {
		t.Fatalf("owner refused from a fresh network: %d %s", code, loc)
	}
	if code, _, _ := fromAddr(t, asNewClient(), http.MethodGet, base+"/wp-json/wp/v2/users/me", "203.0.113.99", nil, "admin", appPass); code != http.StatusOK {
		t.Errorf("Application Password after distributed failures: %d", code)
	}

	// Brute force from one /64 (rotating addresses) locks that network, and the account from it, only.
	if _, err := getPool(t).Exec(ctx, `DELETE FROM login_attempts`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		loginFrom(t, base, fmt.Sprintf("2001:db8:bad:1::%x", i), "admin", "wrong")
	}
	if n := asCount(t, `SELECT count(*) FROM adminsec_lockouts WHERE kind = 'ip' AND value = '2001:db8:bad:1::/64' AND NOT released`); n != 1 {
		t.Errorf("network lockouts of the /64: %d", n)
	}
	if n := asCount(t, `SELECT count(*) FROM adminsec_lockouts WHERE kind = 'user' AND value = 'admin from 2001:db8:bad:1::/64' AND NOT released`); n != 1 {
		t.Errorf("account-from-network lockouts: %d", n)
	}
	if n := asCount(t, `SELECT count(*) FROM adminsec_lockouts WHERE kind = 'user' AND value = 'admin'`); n != 0 {
		t.Errorf("%d account-wide lockouts", n)
	}
	if _, body, _ := loginFrom(t, base, "2001:db8:bad:1:abcd::9", "admin", "correct horse"); !strings.Contains(body, "Too many failed login attempts") {
		t.Error("another address of the locked /64 signed in")
	}
	if code, _, _ := loginFrom(t, base, "198.51.100.78", "admin", "correct horse"); code != http.StatusFound {
		t.Errorf("owner from another network after a network lockout: %d", code)
	}
	// An Application Password from another network is not stopped by the lockout.
	if code, _, _ := fromAddr(t, asNewClient(), http.MethodGet, base+"/wp-json/wp/v2/users/me", "203.0.113.98", nil, "admin", appPass); code != http.StatusOK {
		t.Errorf("Application Password from another network: %d", code)
	}

	// Under attack, a network the account does not know gets two guesses there, not five.
	if _, err := getPool(t).Exec(ctx, `UPDATE adminsec_lockouts SET released = true; DELETE FROM login_attempts`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		loginFrom(t, base, fmt.Sprintf("203.0.113.%d", i+1), "admin", "wrong")
	}
	for i := 0; i < auth.MaxFailedPerLoginNetUnderAttack; i++ {
		if _, body, _ := loginFrom(t, base, "192.0.2.50", "admin", "wrong"); strings.Contains(body, "Too many") {
			t.Fatalf("guess %d of a fresh network refused", i+1)
		}
	}
	if _, body, _ := loginFrom(t, base, "192.0.2.50", "admin", "wrong"); !strings.Contains(body, "Too many failed login attempts") {
		t.Error("a fresh network got more than its budget at an account under attack")
	}
}

// With CAPTCHA keys saved, an account under attack asks networks it does not know for the CAPTCHA
// (without the Pursae Security option that asks it on every sign-in).
func TestLoginPressureCaptcha(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	stubLoginSlowdown(t)
	ctx := context.Background()
	verify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		_, _ = w.Write([]byte(`{"success":` + strconv.FormatBool(r.PostForm.Get("response") == "good") + `}`))
	}))
	defer verify.Close()
	old := captchaClient
	captchaClient = &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme, r.URL.Host = "http", strings.TrimPrefix(verify.URL, "http://")
		return http.DefaultTransport.RoundTrip(r)
	})}
	t.Cleanup(func() { captchaClient = old })
	if err := srv.settings.Set(ctx, map[string]any{"gontact_turnstile_sitekey": "site123", "gontact_turnstile_secret": "sec"}); err != nil {
		t.Fatal(err)
	}
	// Not under attack: no CAPTCHA.
	if code, _, _ := loginFrom(t, base, "198.51.100.10", "admin", "correct horse"); code != http.StatusFound {
		t.Fatalf("sign-in without pressure: %d", code)
	}
	if _, err := getPool(t).Exec(ctx, `DELETE FROM login_attempts`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < auth.MaxFailedLoginsPerLogin; i++ {
		if _, err := getPool(t).Exec(ctx, `INSERT INTO login_attempts(ip, login, success, pending) VALUES ($1, 'admin', false, false)`, fmt.Sprintf("203.0.113.%d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	code, body, _ := loginFrom(t, base, "198.51.100.11", "admin", "correct horse")
	if code == http.StatusFound || !strings.Contains(body, "complete the CAPTCHA") || !strings.Contains(body, `data-sitekey="site123"`) {
		t.Fatalf("no CAPTCHA asked at an account under attack: %d", code)
	}
	if n := asCount(t, `SELECT count(*) FROM login_attempts WHERE ip = '198.51.100.11'`); n != 0 {
		t.Errorf("a missing CAPTCHA was counted as a guess (%d rows)", n)
	}
	form := url.Values{"log": {"admin"}, "pwd": {"correct horse"}, "cf-turnstile-response": {"good"}}
	if code, _, _ := fromAddr(t, asNewClient(), http.MethodPost, base+"/wp-login", "198.51.100.11", form); code != http.StatusFound {
		t.Errorf("owner with a solved CAPTCHA: %d", code)
	}
	// Known now: no CAPTCHA.
	if code, _, _ := loginFrom(t, base, "198.51.100.11", "admin", "correct horse"); code != http.StatusFound {
		t.Errorf("known network asked again: %d", code)
	}
}

// D3: Cache-Control: no-store, private on admin pages and on every response to a signed-in request,
// unless the handler chose its own.
func TestNoStoreForSignedInResponses(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	appPass := newAppPassword(t, c, base)
	cc := func(client *http.Client, path string, basic ...string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		if len(basic) == 2 {
			req.SetBasicAuth(basic[0], basic[1])
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode, resp.Header.Get("Cache-Control")
	}
	anon := asNewClient()
	for _, tc := range []struct {
		name   string
		client *http.Client
		path   string
		basic  []string
		want   string // "" = must not be the no-store default
	}{
		{"admin screen", c, "/wp-admin/", nil, noStoreCacheControl},
		{"admin-ajax", c, "/wp-admin/admin-ajax?action=rest-nonce", nil, noStoreCacheControl},
		{"front page, signed in", c, "/", nil, noStoreCacheControl},
		{"admin redirect for visitors", anon, "/wp-admin/", nil, noStoreCacheControl},
		{"REST with an Application Password", anon, "/wp-json/wp/v2/users/me", []string{"admin", appPass}, noStoreCacheControl},
		{"front page, visitor", anon, "/", nil, ""},
		{"REST, visitor", anon, "/wp-json/wp/v2/posts", nil, ""},
	} {
		code, got := cc(tc.client, tc.path, tc.basic...)
		if code >= 400 {
			t.Errorf("%s: %d", tc.name, code)
		}
		if tc.want != "" && got != tc.want {
			t.Errorf("%s: Cache-Control %q, want %q", tc.name, got, tc.want)
		}
		if tc.want == "" && got == noStoreCacheControl {
			t.Errorf("%s: anonymous public response got %q", tc.name, got)
		}
	}
	// A handler's own Cache-Control wins, also for a signed-in user (the shipped logo).
	if _, got := cc(c, "/wp-includes/images/brand/logo-64.png"); got != "public, max-age=3600" {
		t.Errorf("static asset for a signed-in user: %q", got)
	}
	// The login form keeps its own nocache header.
	if _, got := cc(anon, "/wp-login"); !strings.Contains(got, "no-store") {
		t.Errorf("login form: %q", got)
	}
}

// D6: a lost-password request is answered after the same time whether or not the account exists, and an
// existing account still gets its mail.
func TestLostPasswordConstantTime(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	timed := func(login string) time.Duration {
		t.Helper()
		start := time.Now()
		_, _, loc := post(t, asNewClient(), base+"/wp-login?action=lostpassword", url.Values{"user_login": {login}})
		if !strings.Contains(loc, "checkemail=confirm") {
			t.Fatalf("%s: redirect %q", login, loc)
		}
		return time.Since(start)
	}
	for _, login := range []string{"admin", "nobody-here", "admin@example.test", "nobody@example.test"} {
		if d := timed(login); d < resetResponseTime {
			t.Errorf("%s answered after %v, before %v", login, d, resetResponseTime)
		}
	}
	m := lastMail(t, "Password Reset")
	if !strings.Contains(m.Body, "Username: admin") {
		t.Errorf("reset mail: %q", m.Body)
	}
	if n := asCount(t, `SELECT count(*) FROM jobs WHERE name = 'wp_mail' AND payload->>'Subject' LIKE '%Password Reset%'`); n != 2 {
		t.Errorf("%d reset mails, want 2 (by login and by e-mail)", n)
	}
}

// D7: errors found before the user's consent are shown on the site, never redirected to the redirect_uri
// of an anonymously registered client; the user's own answer still goes back to the client.
func TestOAuthPreConsentErrorsStayLocal(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	resp, err := http.Post(base+"/oauth/register", "application/json", strings.NewReader(`{"client_name":"Evil","redirect_uris":["https://evil.example/cb"]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := regexp.MustCompile(`"client_id":"([^"]+)"`).FindStringSubmatch(string(b))
	if resp.StatusCode != http.StatusCreated || m == nil {
		t.Fatalf("register: %d %s", resp.StatusCode, b)
	}
	challenge := strings.Repeat("A", 43)
	authURL := func(over url.Values) string {
		q := url.Values{"client_id": {m[1]}, "redirect_uri": {"https://evil.example/cb"}, "response_type": {"code"}, "state": {"s"},
			"code_challenge": {challenge}, "code_challenge_method": {"S256"}}
		for k, v := range over {
			q[k] = v
		}
		return base + "/wp-admin/oauth-authorize?" + q.Encode()
	}
	for name, over := range map[string]url.Values{
		"response_type": {"response_type": {"token"}},
		"no PKCE":       {"code_challenge": {""}},
		"plain PKCE":    {"code_challenge_method": {"plain"}},
		"resource":      {"resource": {"https://other.example/mcp"}},
	} {
		code, _, loc := get(t, c, authURL(over))
		if code != http.StatusBadRequest || loc != "" {
			t.Errorf("%s: %d -> %q, want a local 400", name, code, loc)
		}
	}
	// A well-formed request shows the consent screen; denying it goes back to the client.
	code, page, _ := get(t, c, authURL(nil))
	if code != http.StatusOK || !strings.Contains(page, `value="oauth_authorize"`) {
		t.Fatalf("consent screen: %d", code)
	}
	form := url.Values{"_wpnonce": {formNonce(t, page)}, "action": {"oauth_authorize"}, "deny": {"1"}, "client_id": {m[1]}, "redirect_uri": {"https://evil.example/cb"},
		"response_type": {"code"}, "state": {"s"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	if code, _, loc := post(t, c, base+"/wp-admin/oauth-authorize", form); code != http.StatusFound || !strings.HasPrefix(loc, "https://evil.example/cb?") || !strings.Contains(loc, "error=access_denied") {
		t.Errorf("deny: %d %q", code, loc)
	}
}

// D16: a site password saved through the REST option group is stored as a bcrypt hash, as the settings
// form stores it.
func TestMaintenanceRESTPasswordHashed(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	code, body := restSend(t, c, base, http.MethodPost, "/wp-json/pursae/v1/options/maintenance", `{"site_password":"open sesame 42"}`)
	if code != http.StatusOK {
		t.Fatalf("save: %d %s", code, body)
	}
	stored := srv.maintLoad().SitePassword
	if !strings.HasPrefix(stored, "$2") {
		t.Fatalf("site password stored as %q, want a bcrypt hash", stored)
	}
	if ok, legacy := maintPasswordOK(stored, "open sesame 42"); !ok || legacy {
		t.Errorf("hash does not match the password: ok=%v legacy=%v", ok, legacy)
	}
	if strings.Contains(body, "open sesame") || strings.Contains(body, stored) {
		t.Error("the answer shows the password or its hash")
	}
	// Another change that leaves the password out (or sends the mask back) keeps the hash as it is.
	if code, body := restSend(t, c, base, http.MethodPost, "/wp-json/pursae/v1/options/maintenance", `{"site_password":"********","title":"Soon"}`); code != http.StatusOK {
		t.Fatalf("second save: %d %s", code, body)
	}
	if got := srv.maintLoad().SitePassword; got != stored {
		t.Errorf("unchanged password re-hashed or lost: %q", got)
	}
	// The form's rules: too short is refused; "" removes it.
	if code, _ := restSend(t, c, base, http.MethodPost, "/wp-json/pursae/v1/options/maintenance", `{"site_password":"short"}`); code != http.StatusBadRequest {
		t.Errorf("short password: %d, want 400", code)
	}
	if got := srv.maintLoad().SitePassword; got != stored {
		t.Error("a refused password changed the stored one")
	}
	if code, _ := restSend(t, c, base, http.MethodPost, "/wp-json/pursae/v1/options/maintenance", `{"site_password":""}`); code != http.StatusOK || srv.maintLoad().SitePassword != "" {
		t.Errorf("remove: %d %q", code, srv.maintLoad().SitePassword)
	}
}
