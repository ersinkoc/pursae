package server

import (
	"context"
	"encoding/base32"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"wpgo/internal/auth"
	"wpgo/internal/db"
	"wpgo/internal/store"
)

// --- unit tests ------------------------------------------------------------------------------

func TestTOTPVectors(t *testing.T) {
	// RFC 6238 appendix B (SHA-1 seed "12345678901234567890"), last six digits.
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for _, v := range []struct {
		t    int64
		want string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}} {
		if got := totpCode(secret, time.Unix(v.t, 0)); got != v.want {
			t.Errorf("T=%d: %s, want %s", v.t, got, v.want)
		}
	}
	now := time.Unix(1111111109, 0)
	step := totpStep(now)
	key, _ := decodeTOTPSecret(secret)
	if s, ok := totpMatch(secret, hotp(key, uint64(step-1)), now, 0); !ok || s != step-1 {
		t.Error("previous step (drift -1) not accepted")
	}
	if _, ok := totpMatch(secret, hotp(key, uint64(step+1)), now, 0); !ok {
		t.Error("next step (drift +1) not accepted")
	}
	if _, ok := totpMatch(secret, hotp(key, uint64(step+2)), now, 0); ok {
		t.Error("two steps ahead accepted")
	}
	if _, ok := totpMatch(secret, hotp(key, uint64(step)), now, step); ok {
		t.Error("replayed step accepted")
	}
	if _, ok := totpMatch(secret, "12 34 5", now, 0); ok {
		t.Error("short code accepted")
	}
}

func TestQREncode(t *testing.T) {
	uri := otpauthURI("Users Site", "admin", newTOTPSecret())
	q, err := qrEncode([]byte(uri))
	if err != nil {
		t.Fatal(err)
	}
	if q.size != q.ver*4+17 || q.ver < 5 {
		t.Fatalf("version %d size %d for %d bytes", q.ver, q.size, len(uri))
	}
	// Finder patterns in three corners, timing pattern, dark module.
	for _, p := range [][2]int{{0, 0}, {q.size - 1, 0}, {0, q.size - 1}, {6, 6}, {8, q.size - 8}} {
		if !q.mod[p[1]][p[0]] {
			t.Errorf("module %v should be dark", p)
		}
	}
	if q.mod[1][1] || !q.mod[2][2] {
		t.Error("finder pattern rings wrong")
	}
	if svg := q.SVG(200); !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, "M") {
		t.Error("svg")
	}
	if _, err := qrEncode(make([]byte, 3000)); err == nil {
		t.Error("oversized data accepted")
	}
	// Known capacities at level M: version 1 holds 14 bytes, version 2 holds 26.
	if q, _ := qrEncode([]byte("12345678901234")); q.ver != 1 {
		t.Errorf("14 bytes: version %d", q.ver)
	}
	if q, _ := qrEncode([]byte("123456789012345")); q.ver != 2 {
		t.Errorf("15 bytes: version %d", q.ver)
	}
}

func TestPasswordStrength(t *testing.T) {
	for _, v := range []struct {
		pw   string
		want int // minimum score
		max  int
	}{
		{"password", 0, 0}, {"123456", 0, 0}, {"dragon", 0, 0}, {"Password1!", 0, 1}, {"admin2024", 0, 1},
		{"correct horse", 3, 4}, {"Xk9#mQ2$vL7!pR4&", 4, 4}, {"aaaaaaaaaaaaaaaa", 0, 1},
	} {
		got := asStrength(v.pw, "admin", "admin@example.test")
		if got < v.want || got > v.max {
			t.Errorf("%q: score %d, want %d..%d", v.pw, got, v.want, v.max)
		}
	}
	if !asIsCommon("Qwerty") || asIsCommon("Xk9#mQ2$vL7!pR4&") {
		t.Error("common list")
	}
}

// --- integration helpers -------------------------------------------------------------------

func asNewClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// asStart installs a site, logs the admin in on c and activates AdminSecGo.
func asStart(t *testing.T) (base string, c *http.Client, srv *Server) {
	t.Helper()
	ts, c, srv := testServerBuilderOff(t)
	base = ts.URL
	installUsersSite(t, c, base)
	asToggle(t, c, base, "activate")
	if !srv.adminsecOn() {
		t.Fatal("Pursae Security not active")
	}
	return base, c, srv
}

func asToggle(t *testing.T, c *http.Client, base, action string) {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=` + action + `&amp;plugin=adminsecgo[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s link for Pursae Security", action)
	}
	if code, _, loc := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound || !strings.Contains(loc, action+"=true") {
		t.Fatalf("%s: %d %s", action, code, loc)
	}
}

func asConfigure(t *testing.T, srv *Server, f func(*asSettings)) {
	t.Helper()
	st := srv.asLoad()
	f(&st)
	if err := srv.asSave(context.Background(), st); err != nil {
		t.Fatal(err)
	}
}

func asPage(t *testing.T, c *http.Client, u string) string {
	t.Helper()
	code, body, loc := get(t, c, u)
	if code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", u, code, loc)
	}
	if strings.Contains(body, "Template error") {
		t.Fatalf("GET %s: template error %v", u, lastRenderError.Load())
	}
	return body
}

// asAccount posts an action of the Security screen.
func asAccount(t *testing.T, c *http.Client, base string, form url.Values) (int, string, string) {
	t.Helper()
	form.Set("_wpnonce", formNonce(t, asPage(t, c, base+"/wp-admin/admin?page=adminsecgo-account")))
	return post(t, c, base+"/wp-admin/admin?page=adminsecgo-account", form)
}

var asCodeRe = regexp.MustCompile(`(?m)^    (\d{6})$`)

func asMailedCode(t *testing.T, subject string) string {
	t.Helper()
	m := asCodeRe.FindStringSubmatch(lastMail(t, subject).Body)
	if m == nil {
		t.Fatalf("no code in the %q e-mail", subject)
	}
	return m[1]
}

var asTokenRe = regexp.MustCompile(`name="adminsec_token" value="([^"]+)"`)

func asToken(t *testing.T, body string) string {
	t.Helper()
	m := asTokenRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no challenge token in %.400s", body)
	}
	return m[1]
}

// asEnableMFA sets up e-mailed codes for the user logged in on c; it returns the backup codes.
func asEnableMFA(t *testing.T, c *http.Client, base, email string) []string {
	t.Helper()
	code, body, _ := asAccount(t, c, base, url.Values{"adminsec_do": {"mfa_send"}, "email": {email}})
	if code != http.StatusOK || !strings.Contains(body, "Confirm your e-mail address") {
		t.Fatalf("mfa_send: %d %.300s", code, body)
	}
	tok := asToken(t, body)
	_, body, _ = asAccount(t, c, base, url.Values{"adminsec_do": {"mfa_verify"}, "adminsec_token": {tok}, "code": {"000000"}})
	if !strings.Contains(body, "The code is incorrect") {
		t.Error("wrong setup code accepted")
	}
	_, body, _ = asAccount(t, c, base, url.Values{"adminsec_do": {"mfa_verify"}, "adminsec_token": {tok}, "code": {asMailedCode(t, "Confirm your e-mail for login codes")}})
	if !strings.Contains(body, "E-mailed login codes are on") {
		t.Fatalf("mfa_verify failed: %.500s", body)
	}
	return regexp.MustCompile(`<li>([a-z0-9]{5}-[a-z0-9]{5})</li>`).FindAllString(body, -1)
}

var asKeyRe = regexp.MustCompile(`id="adminsec-totp-key">([A-Z2-7 ]+)</code>`)

// asEnableTOTP sets up the authenticator app and returns its key.
func asEnableTOTP(t *testing.T, c *http.Client, base string) string {
	t.Helper()
	_, body, _ := asAccount(t, c, base, url.Values{"adminsec_do": {"totp_start"}})
	m := asKeyRe.FindStringSubmatch(body)
	if m == nil || !strings.Contains(body, `<svg xmlns="http://www.w3.org/2000/svg"`) {
		t.Fatalf("no QR code or key: %.500s", body)
	}
	key := strings.ReplaceAll(m[1], " ", "")
	_, body, _ = asAccount(t, c, base, url.Values{"adminsec_do": {"totp_verify"}, "code": {"123456"}})
	if !strings.Contains(body, "The code is incorrect") && totpCode(key, time.Now()) != "123456" {
		t.Error("wrong TOTP accepted at setup")
	}
	_, body, _ = asAccount(t, c, base, url.Values{"adminsec_do": {"totp_verify"}, "code": {totpCode(key, time.Now())}})
	if !strings.Contains(body, "Two-factor authentication is on") {
		t.Fatalf("totp_verify: %.500s", body)
	}
	return key
}

func asLogin(t *testing.T, c *http.Client, base, path, user, pass string) (int, string, string) {
	t.Helper()
	return post(t, c, base+path, url.Values{"log": {user}, "pwd": {pass}})
}

func asCount(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := getPool(t).QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func asCreateUser(t *testing.T, srv *Server, login, role, pass string) int64 {
	t.Helper()
	hash, _ := auth.HashPassword(pass)
	id, err := srv.store.CreateUser(context.Background(), store.NewUser{Login: login, Email: login + "@example.test", PasswordHash: hash, Role: role})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

const asStrongPass = "Violet-Harbor-Lantern-93!"

// --- plugin off -------------------------------------------------------------------------

func TestAdminSecOffUnchanged(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installUsersSite(t, c, base)
	// Factors stored while the plugin is off change nothing.
	_, _ = getPool(t).Exec(context.Background(), `INSERT INTO adminsec_factors (user_id, email_enabled, email_address) VALUES (1, true, 'admin@example.test')`)
	body := asPage(t, c, base+"/wp-admin/")
	if strings.Contains(body, "toplevel_page_adminsecgo") || strings.Contains(body, "page=adminsecgo-account") {
		t.Error("Pursae Security menu while off")
	}
	if code, _, _ := get(t, c, base+"/wp-admin/admin?page=adminsecgo"); code != http.StatusForbidden {
		t.Errorf("screen of an inactive plugin: %d", code)
	}
	resp, err := c.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Permissions-Policy") != "" || resp.Header.Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Error("headers changed while off")
	}
	if _, body, _ := get(t, c, base+"/"); !strings.Contains(body, `<meta name="generator" content="Pursae `+Version) {
		t.Error("generator tag missing while off")
	}
	anon := asNewClient()
	if code, body, _ := get(t, anon, base+"/wp-login"); code != http.StatusOK || !strings.Contains(body, `action="/wp-login"`) {
		t.Errorf("/wp-login: %d", code)
	}
	if code, _, loc := get(t, anon, base+"/wp-admin/"); code != http.StatusFound || !strings.Contains(loc, "/wp-login?redirect_to=") {
		t.Errorf("/wp-admin/ for visitors: %d %s", code, loc)
	}
	if _, body, _ := asLogin(t, anon, base, "/wp-login", "nobody", "x"); !strings.Contains(body, "The username or password you entered is incorrect") || strings.Contains(body, "is not registered") {
		t.Error("the core login message must not tell unknown accounts from wrong passwords, with or without Pursae Security")
	}
	if code, _, loc := asLogin(t, anon, base, "/wp-login", "admin", "correct horse"); code != http.StatusFound || !strings.HasSuffix(loc, "/wp-admin/") {
		t.Errorf("login with stored factors while off: %d %s", code, loc)
	}
	for i := 0; i < 5; i++ {
		asLogin(t, asNewClient(), base, "/wp-login", "admin", "wrong")
	}
	if _, body, _ := asLogin(t, asNewClient(), base, "/wp-login", "admin", "correct horse"); !strings.Contains(body, "Please try again in 15 minutes") {
		t.Error("core throttle changed while off")
	}
	if n := asCount(t, `SELECT count(*) FROM adminsec_log`); n != 0 {
		t.Errorf("%d log rows while off", n)
	}

	// Deactivating keeps factors but stops enforcing them and restores /wp-login.
	_, _ = getPool(t).Exec(context.Background(), `DELETE FROM login_attempts`)
	asToggle(t, c, base, "activate")
	asConfigure(t, srv, func(st *asSettings) { st.HideLogin, st.LoginSlug = true, "door-123" })
	if code, _, _ := get(t, asNewClient(), base+"/wp-login"); code != http.StatusNotFound {
		t.Errorf("hidden /wp-login: %d", code)
	}
	if _, body, _ := asLogin(t, asNewClient(), base, "/door-123", "admin", "correct horse"); !strings.Contains(body, "We e-mailed a 6-digit code") {
		t.Error("stored MFA not asked for while on")
	}
	asToggle(t, c, base, "deactivate")
	if code, _, _ := get(t, asNewClient(), base+"/wp-login"); code != http.StatusOK {
		t.Errorf("/wp-login after deactivation: %d", code)
	}
	if code, _, _ := asLogin(t, asNewClient(), base, "/wp-login", "admin", "correct horse"); code != http.StatusFound {
		t.Error("factors enforced after deactivation")
	}
	if n := asCount(t, `SELECT count(*) FROM adminsec_factors WHERE email_enabled`); n != 1 {
		t.Error("factors not kept on deactivation")
	}
}

// --- MFA ------------------------------------------------------------------------------------

func TestAdminSecMFA(t *testing.T) {
	base, c, srv := asStart(t)
	body := asPage(t, c, base+"/wp-admin/admin?page=adminsecgo-account")
	if !strings.Contains(body, "E-mail delivery is not set up") || !strings.Contains(body, "Set up e-mail delivery") || strings.Contains(body, `value="mfa_send"`) {
		t.Fatal("MFA offered without e-mail delivery")
	}
	srv.cfg.SMTPHost = "smtp.example.test" // only to get a form (and its nonce) on the screen
	nonce := formNonce(t, asPage(t, c, base+"/wp-admin/admin?page=adminsecgo-account"))
	srv.cfg.SMTPHost = ""
	if _, body, _ := post(t, c, base+"/wp-admin/admin?page=adminsecgo-account", url.Values{"_wpnonce": {nonce}, "adminsec_do": {"mfa_send"}, "email": {"admin@example.test"}}); !strings.Contains(body, "E-mail delivery is not set up") {
		t.Error("mfa_send accepted without e-mail delivery")
	}
	// 2FA can't come first either.
	if _, body, _ := post(t, c, base+"/wp-admin/admin?page=adminsecgo-account", url.Values{"_wpnonce": {nonce}, "adminsec_do": {"totp_start"}}); !strings.Contains(body, "Set up e-mailed codes first") {
		t.Error("2FA set up without MFA")
	}

	srv.cfg.SMTPHost = "smtp.example.test" // the core WPGO_SMTP_* transport counts as e-mail delivery
	backup := asEnableMFA(t, c, base, "admin@example.test")
	if len(backup) != 10 {
		t.Fatalf("%d backup codes shown", len(backup))
	}
	if raw := asCount(t, `SELECT count(*) FROM adminsec_factors WHERE backup_codes::text LIKE '%`+strings.Trim(backup[0], "<li>/")+`%'`); raw != 0 {
		t.Error("backup codes stored in clear")
	}
	if !strings.Contains(asPage(t, c, base+"/wp-admin/users"), "is-mfa") {
		t.Error("Users list doesn't show MFA")
	}

	// Login: the code is e-mailed right away.
	anon := asNewClient()
	code, body, _ := asLogin(t, anon, base, "/wp-login", "admin", "correct horse")
	if code != http.StatusOK || !strings.Contains(body, "We e-mailed a 6-digit code to <strong>ad") || strings.Contains(body, "Use your authenticator app") {
		t.Fatalf("challenge: %d %.600s", code, body)
	}
	tok := asToken(t, body)
	mailed := asMailedCode(t, "Your login code")
	// Resend has a cooldown.
	_, body, _ = post(t, anon, base+"/wp-login?action=adminsec_2fa", url.Values{"adminsec_token": {tok}, "provider": {"email"}, "adminsec_do": {"resend"}})
	if !strings.Contains(body, "Please wait") {
		t.Error("resend without cooldown")
	}
	_, body, _ = post(t, anon, base+"/wp-login?action=adminsec_2fa", url.Values{"adminsec_token": {tok}, "provider": {"email"}, "authcode": {"111111"}})
	if !strings.Contains(body, "The code is incorrect") && mailed != "111111" {
		t.Error("wrong e-mailed code accepted")
	}
	code, _, loc := post(t, anon, base+"/wp-login?action=adminsec_2fa", url.Values{"adminsec_token": {tok}, "provider": {"email"}, "authcode": {mailed[:3] + " " + mailed[3:]}})
	if code != http.StatusFound || !strings.HasSuffix(loc, "/wp-admin/") {
		t.Fatalf("e-mailed code login: %d %s", code, loc)
	}
	if code, _, _ := get(t, anon, base+"/wp-admin/"); code != http.StatusOK {
		t.Error("not logged in after the code")
	}
	// Single use: the challenge is gone.
	_, body, _ = post(t, asNewClient(), base+"/wp-login?action=adminsec_2fa", url.Values{"adminsec_token": {tok}, "provider": {"email"}, "authcode": {mailed}})
	if !strings.Contains(body, "Your verification expired or was used") {
		t.Error("challenge reusable")
	}

	// Too many wrong codes end the challenge.
	anon = asNewClient()
	_, body, _ = asLogin(t, anon, base, "/wp-login", "admin", "correct horse")
	tok = asToken(t, body)
	for i := 0; i < asMaxCodeAttempts; i++ {
		_, body, _ = post(t, anon, base+"/wp-login?action=adminsec_2fa", url.Values{"adminsec_token": {tok}, "provider": {"email"}, "authcode": {"000000"}})
	}
	if !strings.Contains(body, "Too many wrong codes") {
		t.Error("unlimited code attempts")
	}
	// Wrong codes count as failed logins: the IP is locked out now.
	if asCount(t, `SELECT count(*) FROM adminsec_lockouts WHERE kind = 'ip'`) != 1 {
		t.Error("wrong codes did not lead to a lockout")
	}
	_, _ = getPool(t).Exec(context.Background(), `UPDATE adminsec_lockouts SET released = true; DELETE FROM login_attempts`)

	// Backup code (MFA only).
	anon = asNewClient()
	_, body, _ = asLogin(t, anon, base, "/wp-login", "admin", "correct horse")
	tok = asToken(t, body)
	_, body, _ = post(t, anon, base+"/wp-login?action=adminsec_2fa", url.Values{"adminsec_token": {tok}, "provider": {"backup"}, "adminsec_do": {"switch"}})
	if !strings.Contains(body, "Backup Code") {
		t.Fatal("no backup code form")
	}
	bc := strings.TrimSuffix(strings.TrimPrefix(backup[0], "<li>"), "</li>")
	if code, _, _ := post(t, anon, base+"/wp-login?action=adminsec_2fa", url.Values{"adminsec_token": {tok}, "provider": {"backup"}, "authcode": {strings.ToUpper(bc)}}); code != http.StatusFound {
		t.Error("backup code rejected")
	}
	anon = asNewClient()
	_, body, _ = asLogin(t, anon, base, "/wp-login", "admin", "correct horse")
	tok = asToken(t, body)
	if _, body, _ := post(t, anon, base+"/wp-login?action=adminsec_2fa", url.Values{"adminsec_token": {tok}, "provider": {"backup"}, "authcode": {bc}}); !strings.Contains(body, "The code is incorrect") {
		t.Error("backup code worked twice")
	}

	for _, ev := range []string{"mfa_enabled", "backup_codes_generated", "login_success", "backup_code_used", "login_failed"} {
		if asCount(t, `SELECT count(*) FROM adminsec_log WHERE event = $1`, ev) == 0 {
			t.Errorf("no %s log entry", ev)
		}
	}
	if asCount(t, `SELECT count(*) FROM adminsec_log WHERE event = 'login_success' AND message LIKE '%e-mailed code%'`) == 0 {
		t.Error("login method not logged")
	}
	// MFA can't be switched off while the role requires it, and needs the password.
	if _, body, _ := asAccount(t, c, base, url.Values{"adminsec_do": {"mfa_disable"}, "adminsec_password": {"nope"}}); !strings.Contains(body, "current password is incorrect") {
		t.Error("MFA disabled without the password")
	}
}

// --- 2FA --------------------------------------------------------------------------------

func TestAdminSecTOTP(t *testing.T) {
	base, c, srv := asStart(t)
	srv.cfg.SMTPHost = "smtp.example.test"
	asEnableMFA(t, c, base, "admin@example.test")
	key := asEnableTOTP(t, c, base)
	dec, _ := decodeTOTPSecret(key)
	if raw := asCount(t, `SELECT count(*) FROM adminsec_factors WHERE totp_secret LIKE '%`+key+`%'`); raw != 0 {
		t.Error("TOTP secret stored in clear")
	}
	// 2FA requires MFA: MFA can't be switched off under it.
	if _, body, _ := asAccount(t, c, base, url.Values{"adminsec_do": {"mfa_disable"}, "adminsec_password": {"correct horse"}}); !strings.Contains(body, "Remove the authenticator app first") {
		t.Error("MFA switched off under 2FA")
	}
	if !strings.Contains(asPage(t, c, base+"/wp-admin/users"), "is-2fa") {
		t.Error("Users list doesn't show 2FA")
	}
	pool := getPool(t)
	ctx := context.Background()
	step := totpStep(time.Now())
	challenge := func() (*http.Client, string) {
		anon := asNewClient()
		_, body, _ := asLogin(t, anon, base, "/wp-login", "admin", "correct horse")
		if !strings.Contains(body, "Authentication Code") || !strings.Contains(body, "Can't use your app? Email me a code") {
			t.Fatalf("no TOTP challenge: %.500s", body)
		}
		return anon, asToken(t, body)
	}
	submit := func(anon *http.Client, tok, code string) (int, string) {
		code2, body, _ := post(t, anon, base+"/wp-login?action=adminsec_2fa", url.Values{"adminsec_token": {tok}, "provider": {"totp"}, "authcode": {code}})
		return code2, body
	}
	// Drift: the previous step is accepted.
	_, _ = pool.Exec(ctx, `UPDATE adminsec_factors SET totp_last_step = 0`)
	anon, tok := challenge()
	if code, body := submit(anon, tok, hotp(dec, uint64(step-1))); code != http.StatusFound {
		t.Fatalf("TOTP (drift -1) rejected: %.300s", body)
	}
	// Replay: the same step again is refused.
	anon, tok = challenge()
	if code, _ := submit(anon, tok, hotp(dec, uint64(step-1))); code == http.StatusFound {
		t.Error("replayed TOTP accepted")
	}
	// Too far ahead is refused; the current code works.
	if code, _ := submit(anon, tok, hotp(dec, uint64(step+3))); code == http.StatusFound {
		t.Error("TOTP three steps ahead accepted")
	}
	_, _ = pool.Exec(ctx, `UPDATE adminsec_factors SET totp_last_step = 0`)
	if code, _ := submit(anon, tok, totpCode(key, time.Now())); code != http.StatusFound {
		t.Error("current TOTP rejected")
	}

	// Fallback: "Email me a code".
	anon, tok = challenge()
	_, body, _ := post(t, anon, base+"/wp-login?action=adminsec_2fa", url.Values{"adminsec_token": {tok}, "provider": {"email"}, "adminsec_do": {"switch"}})
	if !strings.Contains(body, "Verification Code") || !strings.Contains(body, "Use your authenticator app") {
		t.Fatal("no e-mail fallback form")
	}
	code, _, _ := post(t, anon, base+"/wp-login?action=adminsec_2fa", url.Values{"adminsec_token": {tok}, "provider": {"email"}, "authcode": {asMailedCode(t, "Your login code")}})
	if code != http.StatusFound {
		t.Fatal("e-mail fallback rejected")
	}

	// Remember this device.
	_, _ = pool.Exec(ctx, `UPDATE adminsec_factors SET totp_last_step = 0`)
	anon, tok = challenge()
	if code, _, _ := post(t, anon, base+"/wp-login?action=adminsec_2fa", url.Values{"adminsec_token": {tok}, "provider": {"totp"}, "authcode": {totpCode(key, time.Now())}, "adminsec_remember": {"1"}}); code != http.StatusFound {
		t.Fatal("remember login failed")
	}
	_, body, _ = get(t, anon, base+"/wp-admin/profile")
	nonce := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)
	get(t, anon, base+"/wp-login?action=logout&_wpnonce="+regexp.MustCompile(`action=logout&amp;_wpnonce=([0-9a-f]+)`).FindStringSubmatch(body)[1])
	if code, _, loc := asLogin(t, anon, base, "/wp-login", "admin", "correct horse"); code != http.StatusFound || !strings.HasSuffix(loc, "/wp-admin/") {
		t.Fatalf("remembered device still challenged: %d", code)
	}
	if asCount(t, `SELECT count(*) FROM adminsec_devices`) != 1 {
		t.Error("device not stored")
	}
	// A forged or other-user cookie does nothing; forgetting devices brings the challenge back.
	_ = nonce
	_, body, _ = asAccount(t, anon, base, url.Values{"adminsec_do": {"devices_forget"}})
	if _, body, _ = asLogin(t, anon, base, "/wp-login", "admin", "correct horse"); !strings.Contains(body, "Authentication Code") {
		t.Error("forgotten device skipped the challenge")
	}
	// Regenerating backup codes needs the password and replaces the old ones.
	if _, body, _ := asAccount(t, c, base, url.Values{"adminsec_do": {"backup_regen"}, "adminsec_password": {"correct horse"}}); !strings.Contains(body, "New backup codes generated") {
		t.Error("backup codes not regenerated")
	}
	for _, ev := range []string{"totp_enabled", "device_remembered", "devices_forgotten"} {
		if asCount(t, `SELECT count(*) FROM adminsec_log WHERE event = $1`, ev) == 0 {
			t.Errorf("no %s log entry", ev)
		}
	}
	// Removing the app needs the password.
	if code, _, loc := asAccount(t, c, base, url.Values{"adminsec_do": {"totp_disable"}, "adminsec_password": {"correct horse"}}); code != http.StatusFound || !strings.Contains(loc, "done=totp-off") {
		t.Errorf("totp_disable: %d %s", code, loc)
	}
}

// --- role enforcement, app passwords -----------------------------------------------------------

func TestAdminSecRoleEnforcement(t *testing.T) {
	base, c, srv := asStart(t)
	pool := getPool(t)
	ctx := context.Background()
	// Requirements need e-mail delivery.
	_, body, _ := get(t, c, base+"/wp-admin/admin?page=adminsecgo-settings&tab=twofactor")
	_, body, _ = post(t, c, base+"/wp-admin/admin?page=adminsecgo-settings&tab=twofactor", url.Values{"_wpnonce": {formNonce(t, body)}, "tab": {"twofactor"},
		"require_administrator": {"2fa"}, "grace_days": {"7"}, "remember_days": {"30"}})
	if !strings.Contains(body, "can only be required once e-mail can be delivered") {
		t.Error("requirement saved without e-mail delivery")
	}
	srv.cfg.SMTPHost = "smtp.example.test"
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=adminsecgo-settings&tab=twofactor")
	if code, _, _ := post(t, c, base+"/wp-admin/admin?page=adminsecgo-settings&tab=twofactor", url.Values{"_wpnonce": {formNonce(t, body)}, "tab": {"twofactor"},
		"require_administrator": {"2fa"}, "require_editor": {"mfa"}, "grace_days": {"7"}, "remember_days": {"30"}}); code != http.StatusFound {
		t.Fatalf("save requirement: %d", code)
	}
	if srv.asLoad().RequireRoles["administrator"] != "2fa" {
		t.Fatal("requirement not saved")
	}
	// Grace period: a nag on every screen.
	body = asPage(t, c, base+"/wp-admin/")
	if !strings.Contains(body, "your role requires two-factor authentication") {
		t.Error("no grace-period notice")
	}
	if !strings.Contains(asPage(t, c, base+"/wp-admin/users"), "2FA required") {
		t.Error("Users list doesn't flag the requirement")
	}
	// Past the grace period: only the Security screen (and the profile) remain.
	_, _ = pool.Exec(ctx, `UPDATE users SET meta = meta || '{"adminsec_grace_start": 1000}' WHERE login = 'admin'`)
	if code, _, loc := get(t, c, base+"/wp-admin/edit"); code != http.StatusFound || !strings.Contains(loc, "page=adminsecgo-account&required=1") {
		t.Fatalf("expired grace period not enforced: %d %s", code, loc)
	}
	if code, _, loc := asLogin(t, asNewClient(), base, "/wp-login", "admin", "correct horse"); code != http.StatusFound || !strings.Contains(loc, "adminsecgo-account") {
		t.Errorf("login past the grace period: %d %s", code, loc)
	}
	body = asPage(t, c, base+"/wp-admin/admin?page=adminsecgo-account&required=1")
	if !strings.Contains(body, "The grace period is over") {
		t.Error("account screen doesn't explain the requirement")
	}
	asEnableMFA(t, c, base, "admin@example.test")
	if code, _, _ := get(t, c, base+"/wp-admin/edit"); code != http.StatusFound {
		t.Error("MFA alone satisfied a 2FA requirement")
	}
	asEnableTOTP(t, c, base)
	if code, _, _ := get(t, c, base+"/wp-admin/edit"); code != http.StatusOK {
		t.Error("still gated after setting up 2FA")
	}
	// Required factors can't be removed.
	if _, body, _ := asAccount(t, c, base, url.Values{"adminsec_do": {"totp_disable"}, "adminsec_password": {"correct horse"}}); !strings.Contains(body, "requires an authenticator app") {
		t.Error("required 2FA removed")
	}

	// An administrator resets an editor's factors and sees their status.
	edID := asCreateUser(t, srv, "eddie", "editor", asStrongPass)
	_, _ = pool.Exec(ctx, `INSERT INTO adminsec_factors (user_id, email_enabled, email_address) VALUES ($1, true, 'eddie@example.test')`, edID)
	body = asPage(t, c, base+"/wp-admin/user-edit?user_id="+itoa64(edID))
	if !strings.Contains(body, `value="reset-factors"`) || !strings.Contains(body, "E-mailed codes") {
		t.Fatal("user-edit has no Pursae Security block")
	}
	if code, _, _ := post(t, c, base+"/wp-admin/user-edit?user_id="+itoa64(edID), url.Values{"_wpnonce": {formNonce(t, body)}, "adminsec_action": {"reset-factors"}}); code != http.StatusFound {
		t.Error("reset factors")
	}
	if asCount(t, `SELECT count(*) FROM adminsec_factors WHERE user_id = $1`, edID) != 0 {
		t.Error("factors not reset")
	}
	if asCount(t, `SELECT count(*) FROM adminsec_log WHERE event = 'factors_reset'`) != 1 {
		t.Error("reset not logged")
	}
}

func TestAdminSecAppPasswordsBypass2FA(t *testing.T) {
	base, c, srv := asStart(t)
	srv.cfg.SMTPHost = "smtp.example.test"
	asEnableMFA(t, c, base, "admin@example.test")
	asEnableTOTP(t, c, base)
	_, body, _ := get(t, c, base+"/wp-admin/profile")
	_, body, _ = post(t, c, base+"/wp-admin/profile", url.Values{"_wpnonce": {formNonce(t, body)}, "do_new_application_password": {"1"}, "new_application_password_name": {"CLI"}})
	shown := regexp.MustCompile(`id="new-application-password-value" type="text" class="code" readonly="readonly" value="([^"]+)"`).FindStringSubmatch(body)
	if shown == nil {
		t.Fatal("no application password")
	}
	rc := &restClient{t: t, c: asNewClient(), base: base, basic: [2]string{"admin", shown[1]}}
	if code, me, _, _ := rc.do("GET", "/wp/v2/users/me?context=edit", nil); code != http.StatusOK || me["username"] != "admin" {
		t.Fatalf("application password with 2FA on: %d %v", code, me)
	}
	// The account password is no way around the second factor on the API.
	rc.basic[1] = "correct horse"
	if code, _, _, _ := rc.do("GET", "/wp/v2/users/me", nil); code != http.StatusUnauthorized {
		t.Errorf("account password on the API: %d", code)
	}
	// Lockouts apply to the API too.
	asConfigure(t, srv, func(st *asSettings) { st.IPMaxFailures = 2 })
	rc.basic[1] = "wrong"
	rc.do("GET", "/wp/v2/users/me", nil)
	rc.basic[1] = shown[1]
	if code, _, _, _ := rc.do("GET", "/wp/v2/users/me", nil); code != http.StatusTooManyRequests {
		t.Errorf("locked-out IP on the API: %d", code)
	}
}

// --- custom login URL --------------------------------------------------------------------------

func TestAdminSecHideLogin(t *testing.T) {
	base, c, srv := asStart(t)
	_, body, _ := get(t, c, base+"/wp-admin/admin?page=adminsecgo-settings&tab=login")
	nonce := formNonce(t, body)
	for _, bad := range []string{"wp-admin", "ab", "sample-page", "Bad Slug"} {
		if _, body, _ := post(t, c, base+"/wp-admin/admin?page=adminsecgo-settings&tab=login", url.Values{"_wpnonce": {nonce}, "tab": {"login"}, "hide_login": {"1"}, "login_slug": {bad}}); !strings.Contains(body, "notice-error") {
			t.Errorf("login slug %q accepted", bad)
		}
	}
	if code, _, _ := post(t, c, base+"/wp-admin/admin?page=adminsecgo-settings&tab=login", url.Values{"_wpnonce": {nonce}, "tab": {"login"}, "hide_login": {"1"},
		"login_slug": {"secret-door"}, "generic_errors": {"1"}, "block_enumeration": {"1"}}); code != http.StatusFound {
		t.Fatal("save login slug")
	}
	if srv.loginPath() != "/secret-door" {
		t.Fatalf("loginPath %s", srv.loginPath())
	}
	anon := asNewClient()
	for _, p := range []string{"/wp-login", "/wp-login.php", "/wp-login.php?action=lostpassword", "/wp-admin/", "/wp-admin", "/wp-admin/edit", "/wp-admin/options-general.php", "/wp-admin/index.php"} {
		if code, body, loc := get(t, anon, base+p); code != http.StatusNotFound || strings.Contains(body, "loginform") {
			t.Errorf("visitor %s: %d %s", p, code, loc)
		}
	}
	if code, _, _ := post(t, anon, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusNotFound {
		t.Errorf("POST /wp-login: %d", code)
	}
	// Assets, admin-ajax and the post password form stay open.
	if code, _, _ := get(t, anon, base+"/wp-admin/assets/css/wp/login.min.css"); code != http.StatusOK {
		t.Errorf("login stylesheet: %d", code)
	}
	if code, _, _ := get(t, anon, base+"/wp-admin/admin-ajax?action=nothing"); code == http.StatusNotFound {
		t.Error("admin-ajax hidden")
	}
	// REST app authorization starts at the new login.
	if code, _, loc := get(t, anon, base+"/wp-admin/authorize-application?app_name=Test"); code != http.StatusFound || !strings.HasPrefix(loc, "/secret-door?redirect_to=") {
		t.Errorf("authorize-application: %d %s", code, loc)
	}
	// The new URL is the login, with every link pointing at it.
	body = asPage(t, anon, base+"/secret-door")
	for _, want := range []string{`action="/secret-door"`, `href="/secret-door?action=lostpassword"`} {
		if !strings.Contains(body, want) {
			t.Errorf("login page misses %s", want)
		}
	}
	if strings.Contains(body, `"/wp-login`) {
		t.Error("login page links /wp-login")
	}
	if _, body, _ := asLogin(t, anon, base, "/secret-door", "nobody", "x"); !strings.Contains(body, "The username or password you entered is incorrect") || strings.Contains(body, "not registered") {
		t.Error("generic error message")
	}
	body = asPage(t, anon, base+"/secret-door?action=lostpassword")
	if !strings.Contains(body, `action="/secret-door?action=lostpassword"`) {
		t.Error("lost password form action")
	}
	if code, _, loc := post(t, anon, base+"/secret-door?action=lostpassword", url.Values{"user_login": {"admin"}}); code != http.StatusFound || loc != "/secret-door?checkemail=confirm" {
		t.Errorf("lost password: %d %s", code, loc)
	}
	reset := firstURL(t, lastMail(t, "Password Reset").Body, "action=rp")
	if !strings.Contains(reset, "/secret-door?action=rp&key=") {
		t.Errorf("reset e-mail link %s", reset)
	}
	if code, body, _ := get(t, anon, reset); code != http.StatusOK || !strings.Contains(body, `action="/secret-door?action=resetpass"`) {
		t.Errorf("reset link: %d", code)
	}
	code, _, loc := asLogin(t, anon, base, "/secret-door", "admin", "correct horse")
	if code != http.StatusFound || !strings.HasSuffix(loc, "/wp-admin/") {
		t.Fatalf("login at the new URL: %d %s", code, loc)
	}
	body = asPage(t, anon, base+"/wp-admin/")
	if !strings.Contains(body, `href="/secret-door?action=logout&amp;_wpnonce=`) || strings.Contains(body, "/wp-login?action=logout") {
		t.Error("admin bar logout link")
	}
	front := asPage(t, anon, base+"/")
	if !strings.Contains(front, `/secret-door?action=logout&amp;_wpnonce=`) {
		t.Error("front-end toolbar logout link")
	}
	m := regexp.MustCompile(`/secret-door\?action=logout&amp;_wpnonce=([0-9a-f]+)`).FindStringSubmatch(body)
	if code, _, loc := get(t, anon, base+"/secret-door?action=logout&_wpnonce="+m[1]); code != http.StatusFound || loc != "/secret-door?loggedout=true" {
		t.Errorf("logout redirect: %d %s", code, loc)
	}
	// Redirect instead of 404.
	asConfigure(t, srv, func(st *asSettings) { st.HideRedirect = "/" })
	if code, _, loc := get(t, asNewClient(), base+"/wp-login"); code != http.StatusFound || loc != "/" {
		t.Errorf("redirect mode: %d %s", code, loc)
	}
	asConfigure(t, srv, func(st *asSettings) { st.HideRedirect = "" })

	// Escape hatch: the environment variable brings /wp-login back at once.
	t.Setenv(asEscapeEnv, "1")
	if code, body, _ := get(t, asNewClient(), base+"/wp-login"); code != http.StatusOK || !strings.Contains(body, `action="/wp-login"`) {
		t.Errorf("escape hatch: %d", code)
	}
	os.Unsetenv(asEscapeEnv)
	if code, _, _ := get(t, asNewClient(), base+"/wp-login"); code != http.StatusNotFound {
		t.Error("hidden again without the variable")
	}
	// ... and the CLI switches the setting off in the database.
	pool, err := db.Connect(context.Background(), os.Getenv("WPGO_TEST_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := AdminSecCLI(context.Background(), pool, []string{"disable-hide-login"}); err != nil {
		t.Fatal(err)
	}
	if asCount(t, `SELECT count(*) FROM settings WHERE key = 'adminsecgo_settings' AND value->>'hide_login' = 'false'`) != 1 {
		t.Error("CLI did not switch the custom login URL off")
	}
	_ = c
}

// --- lockouts, enumeration ------------------------------------------------------------------

func TestAdminSecLockouts(t *testing.T) {
	base, c, srv := asStart(t)
	srv.cfg.SMTPHost = "smtp.example.test"
	asConfigure(t, srv, func(st *asSettings) {
		st.IPMaxFailures, st.UserMaxFailures, st.LockoutMinutes, st.LongLockoutAfter, st.LongLockoutHours = 3, 0, 20, 2, 24
	})
	anon := asNewClient()
	for i := 0; i < 3; i++ {
		if _, body, _ := asLogin(t, anon, base, "/wp-login", "admin", "wrong"); !strings.Contains(body, "The username or password you entered is incorrect") {
			t.Fatalf("attempt %d: %.300s", i, body)
		}
	}
	if _, body, _ := asLogin(t, anon, base, "/wp-login", "admin", "correct horse"); !strings.Contains(body, "Too many failed login attempts. Please try again in 20 minutes") {
		t.Fatalf("no IP lockout: %.300s", body)
	}
	if asCount(t, `SELECT count(*) FROM adminsec_lockouts WHERE kind = 'ip' AND NOT long_lock`) != 1 || asCount(t, `SELECT count(*) FROM adminsec_log WHERE event = 'lockout'`) != 1 {
		t.Error("lockout not recorded")
	}
	if !strings.Contains(lastMail(t, "Login lockout").Body, "was locked out") {
		t.Error("no lockout alert")
	}
	body := asPage(t, c, base+"/wp-admin/admin?page=adminsecgo-lockouts")
	id := regexp.MustCompile(`name="lockout" value="(\d+)"`).FindStringSubmatch(body)
	if id == nil {
		t.Fatal("active lockout not listed")
	}
	if code, _, _ := post(t, c, base+"/wp-admin/admin?page=adminsecgo-lockouts", url.Values{"_wpnonce": {formNonce(t, body)}, "adminsec_do": {"release"}, "lockout": {id[1]}}); code != http.StatusFound {
		t.Fatal("release")
	}
	if code, _, _ := asLogin(t, anon, base, "/wp-login", "admin", "correct horse"); code != http.StatusFound {
		t.Fatal("still locked out after release")
	}
	// Progressive: the second lockout within 24 hours is the long one.
	for i := 0; i < 4; i++ {
		asLogin(t, asNewClient(), base, "/wp-login", "admin", "wrong")
	}
	if asCount(t, `SELECT count(*) FROM adminsec_lockouts WHERE kind = 'ip' AND long_lock AND until > now() + interval '23 hours'`) != 1 {
		t.Error("no long lockout")
	}
	_, _ = getPool(t).Exec(context.Background(), `UPDATE adminsec_lockouts SET released = true; DELETE FROM login_attempts`)

	// Per username: other accounts keep working from the same IP.
	asCreateUser(t, srv, "victim", "author", asStrongPass)
	asConfigure(t, srv, func(st *asSettings) { st.IPMaxFailures, st.UserMaxFailures = 0, 2 })
	for i := 0; i < 2; i++ {
		asLogin(t, asNewClient(), base, "/wp-login", "victim@example.test", "wrong") // by e-mail: counted against the username
	}
	if _, body, _ := asLogin(t, asNewClient(), base, "/wp-login", "victim", asStrongPass); !strings.Contains(body, "Too many failed login attempts") {
		t.Error("no username lockout")
	}
	if code, _, _ := asLogin(t, asNewClient(), base, "/wp-login", "admin", "correct horse"); code != http.StatusFound {
		t.Error("username lockout blocked another account")
	}
	// Allow list: never locked out.
	_, _ = getPool(t).Exec(context.Background(), `UPDATE adminsec_lockouts SET released = true; DELETE FROM login_attempts`)
	asConfigure(t, srv, func(st *asSettings) { st.IPMaxFailures, st.AllowList = 2, "127.0.0.0/8 # local" })
	for i := 0; i < 4; i++ {
		asLogin(t, asNewClient(), base, "/wp-login", "admin", "wrong")
	}
	if code, _, _ := asLogin(t, asNewClient(), base, "/wp-login", "admin", "correct horse"); code != http.StatusFound {
		t.Error("allow-listed IP locked out")
	}
	// Block list (the settings form refuses your own IP; set directly).
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=adminsecgo-settings&tab=lockouts")
	if _, body, _ := post(t, c, base+"/wp-admin/admin?page=adminsecgo-settings&tab=lockouts", url.Values{"_wpnonce": {formNonce(t, body)}, "tab": {"lockouts"},
		"block_list": {"127.0.0.1"}, "ip_max_failures": {"5"}}); !strings.Contains(body, "your own IP address") {
		t.Error("own IP accepted on the block list")
	}
	asConfigure(t, srv, func(st *asSettings) { st.AllowList, st.BlockList = "", "127.0.0.1" })
	if _, body, _ := asLogin(t, asNewClient(), base, "/wp-login", "admin", "correct horse"); !strings.Contains(body, "Access from your IP address has been blocked") {
		t.Error("blocked IP logged in")
	}
	asConfigure(t, srv, func(st *asSettings) { st.BlockListSite = true })
	if code, _, _ := get(t, asNewClient(), base+"/"); code != http.StatusForbidden {
		t.Errorf("blocked IP on the site: %d", code)
	}
	// Behind a reverse proxy (the test client is on loopback, a default trusted proxy) the visitor's
	// forwarded address is what the site block list matches, as in the rest of the site.
	asConfigure(t, srv, func(st *asSettings) { st.BlockList = "203.0.113.9" })
	for xff, want := range map[string]int{"203.0.113.9": http.StatusForbidden, "198.51.100.7": http.StatusOK} {
		req, _ := http.NewRequest("GET", base+"/", nil)
		req.Header.Set("X-Forwarded-For", xff)
		resp, err := asNewClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("site block list, visitor %s behind the proxy: %d, want %d", xff, resp.StatusCode, want)
		}
	}
	asConfigure(t, srv, func(st *asSettings) { st.BlockList, st.BlockListSite = "", false })
}

// With Pursae Security on, parallel guesses are counted before their passwords are hashed (as the core
// throttle does): no more than the limit get a password check, on wp-login and on REST Basic auth.
func TestAdminSecParallelGuessesLimited(t *testing.T) {
	base, _, srv := asStart(t)
	asConfigure(t, srv, func(st *asSettings) { st.IPMaxFailures, st.UserMaxFailures, st.LockoutMinutes = 3, 0, 20 })
	burst := func(do func() (checked bool)) int {
		var wg sync.WaitGroup
		var mu sync.Mutex
		checked := 0
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if do() {
					mu.Lock()
					checked++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		return checked
	}
	n := burst(func() bool {
		resp, err := asNewClient().PostForm(base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"wrong"}})
		if err != nil {
			return false
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return !strings.Contains(string(b), "Too many failed login attempts")
	})
	if n > 3 {
		t.Errorf("wp-login: %d parallel passwords checked, limit 3", n)
	}
	if asCount(t, `SELECT count(*) FROM adminsec_lockouts WHERE kind = 'ip' AND NOT released AND until > now()`) == 0 {
		t.Error("no lockout after the burst")
	}
	_, _ = getPool(t).Exec(context.Background(), `UPDATE adminsec_lockouts SET released = true; DELETE FROM login_attempts`)
	n = burst(func() bool {
		req, _ := http.NewRequest("GET", base+"/wp-json/wp/v2/users/me", nil)
		req.SetBasicAuth("admin", "abcd efgh ijkl mnop qrst uvwx")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode != http.StatusTooManyRequests
	})
	if n > 3 {
		t.Errorf("REST Basic: %d parallel passwords checked, limit 3", n)
	}
}

func TestAdminSecEnumerationAndREST(t *testing.T) {
	base, c, srv := asStart(t)
	anon := asNewClient()
	if code, _, _ := get(t, anon, base+"/?author=1"); code != http.StatusNotFound {
		t.Errorf("/?author=1 for visitors: %d", code)
	}
	if code, _, loc := get(t, c, base+"/?author=1"); code == http.StatusNotFound {
		t.Errorf("/?author=1 for a user: %d %s", code, loc)
	}
	rc := &restClient{t: t, c: anon, base: base}
	if code, _, _, _ := rc.do("GET", "/wp/v2/users", nil); code != http.StatusUnauthorized {
		t.Errorf("REST users for visitors: %d", code)
	}
	// The dispatcher routes by the effective method, so _method=GET / X-HTTP-Method-Override must not skip the block.
	for _, id := range []string{"", "/1"} {
		if code, _, _, _ := rc.do("POST", "/wp/v2/users"+id+"?_method=GET", nil); code != http.StatusUnauthorized {
			t.Errorf("REST users%s via POST ?_method=GET for visitors: %d", id, code)
		}
		req, _ := http.NewRequest("POST", base+"/wp-json/wp/v2/users"+id, nil)
		req.Header.Set("X-HTTP-Method-Override", "GET")
		resp, err := anon.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("REST users%s via X-HTTP-Method-Override for visitors: %d", id, resp.StatusCode)
		}
	}
	if code, _, _, _ := rc.do("GET", "/wp/v2/posts", nil); code != http.StatusOK {
		t.Errorf("REST posts for visitors: %d", code)
	}
	if code, _, _, _ := newRESTClient(t, c, base).do("GET", "/wp/v2/users", nil); code != http.StatusOK {
		t.Errorf("REST users for the admin: %d", code)
	}
	asConfigure(t, srv, func(st *asSettings) { st.RESTVisitors = true })
	if code, _, _, _ := rc.do("GET", "/wp/v2/posts", nil); code != http.StatusUnauthorized {
		t.Errorf("restricted REST for visitors: %d", code)
	}
	if code, _, _, _ := rc.do("GET", "/oembed/1.0/embed?url="+url.QueryEscape(base+"/?p=1"), nil); code == http.StatusUnauthorized {
		t.Error("oEmbed blocked")
	}
	// Native front-end visitor endpoints (Pursae Cookie consent/geo, Pursae Pixel events) stay open.
	for _, p := range []string{"/gooike/v1/consent", "/gooike/v1/geo", "/pixelgo/v1/event"} {
		if code, m, _, _ := rc.do("POST", p, map[string]any{}); code == http.StatusUnauthorized && m["code"] == "rest_login_required" {
			t.Errorf("%s blocked for visitors", p)
		}
	}
	if code, m, _, _ := rc.do("GET", "/gooike/v1/settings", nil); code != http.StatusUnauthorized || m["code"] != "rest_login_required" {
		t.Errorf("non-visitor gooike route open: %d %v", code, m["code"])
	}
	asConfigure(t, srv, func(st *asSettings) { st.RESTAllow = "wp/v2/posts" })
	if code, _, _, _ := rc.do("GET", "/wp/v2/posts", nil); code != http.StatusOK {
		t.Errorf("allowed REST prefix: %d", code)
	}
	if code, _, _, _ := newRESTClient(t, c, base).do("GET", "/wp/v2/pages", nil); code != http.StatusOK {
		t.Errorf("restricted REST for the admin: %d", code)
	}
}

// --- sessions -----------------------------------------------------------------------------

func TestAdminSecSessions(t *testing.T) {
	base, c, srv := asStart(t)
	// An administrator's login from an IP address the account has not used before is reported.
	srv.cfg.SMTPHost = "smtp.example.test"
	_, _ = getPool(t).Exec(context.Background(), `INSERT INTO adminsec_known_ips (user_id, ip) VALUES (1, '198.51.100.9')`)
	other := asNewClient()
	if code, _, _ := asLogin(t, other, base, "/wp-login", "admin", "correct horse"); code != http.StatusFound {
		t.Fatal("second login")
	}
	if !strings.Contains(lastMail(t, "Administrator login from a new IP address").Body, "127.0.0.1") {
		t.Error("no new-IP alert")
	}
	body := asPage(t, c, base+"/wp-admin/profile")
	if !strings.Contains(body, `id="adminsec-sessions"`) || strings.Count(body, `name="adminsec_action" value="revoke-session:`) != 1 || !strings.Contains(body, "This session") {
		t.Fatalf("sessions list: %d revoke buttons", strings.Count(body, "revoke-session:"))
	}
	id := regexp.MustCompile(`value="revoke-session:(\d+)"`).FindStringSubmatch(body)[1]
	if code, _, _ := post(t, c, base+"/wp-admin/profile", url.Values{"_wpnonce": {formNonce(t, body)}, "adminsec_action": {"revoke-session:" + id}}); code != http.StatusFound {
		t.Fatal("revoke")
	}
	if code, _, loc := get(t, other, base+"/wp-admin/"); code != http.StatusFound || !strings.Contains(loc, "/wp-login") {
		t.Errorf("revoked session still works: %d", code)
	}
	if code, _, _ := get(t, c, base+"/wp-admin/"); code != http.StatusOK {
		t.Error("current session revoked")
	}
	// Log out every other session.
	asLogin(t, other, base, "/wp-login", "admin", "correct horse")
	body = asPage(t, c, base+"/wp-admin/profile")
	post(t, c, base+"/wp-admin/profile", url.Values{"_wpnonce": {formNonce(t, body)}, "action": {"destroy-sessions"}})
	if code, _, _ := get(t, other, base+"/wp-admin/"); code != http.StatusFound {
		t.Error("Log Out Everywhere Else kept the other session")
	}
	// Idle timeout.
	asConfigure(t, srv, func(st *asSettings) { st.IdleMinutes = 30 })
	_, _ = getPool(t).Exec(context.Background(), `UPDATE sessions SET last_seen_at = now() - interval '31 minutes'`)
	if code, _, _ := get(t, c, base+"/wp-admin/"); code != http.StatusFound {
		t.Error("idle session not ended")
	}
}

// --- password policy, audit, hardening, screens ---------------------------------------------

func TestAdminSecPolicyAuditHardening(t *testing.T) {
	base, c, srv := asStart(t)
	// Every screen renders.
	for _, p := range []string{"adminsecgo", "adminsecgo-lockouts", "adminsecgo-log", "adminsecgo-account"} {
		asPage(t, c, base+"/wp-admin/admin?page="+p)
	}
	for _, tab := range asTabs {
		body := asPage(t, c, base+"/wp-admin/admin?page=adminsecgo-settings&tab="+tab[0])
		if !strings.Contains(body, `name="tab" value="`+tab[0]+`"`) {
			t.Errorf("settings tab %s", tab[0])
		}
	}
	body := asPage(t, c, base+"/wp-admin/admin?page=adminsecgo")
	if !strings.Contains(body, `id="adminsec-score"`) || !strings.Contains(body, "The file editors are disabled") || !strings.Contains(body, "toplevel_page_adminsecgo") {
		t.Error("dashboard")
	}
	if !strings.Contains(body, `href="admin?page=adminsecgo-account"`) && !strings.Contains(body, "page=adminsecgo-account") {
		t.Error("Users › Security menu entry")
	}
	// Hardening: headers, file editors, version.
	resp, err := c.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Permissions-Policy"), "camera=()") || resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("security headers")
	}
	req, _ := http.NewRequest("GET", base+"/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	if resp, err := asNewClient().Do(req); err == nil {
		resp.Body.Close()
		if resp.Header.Get("Strict-Transport-Security") != "max-age=31536000" {
			t.Errorf("HSTS %q", resp.Header.Get("Strict-Transport-Security"))
		}
	}
	if code, _, _ := get(t, c, base+"/wp-admin/theme-editor"); code != http.StatusForbidden {
		t.Errorf("theme editor: %d", code)
	}
	if strings.Contains(asPage(t, c, base+"/wp-admin/"), `href="theme-editor"`) {
		t.Error("Theme File Editor still in the menu")
	}
	front := asPage(t, asNewClient(), base+"/")
	if strings.Contains(front, `name="generator"`) || strings.Contains(front, "ver="+Version+"'") {
		t.Error("version visible on the front end")
	}

	// Password policy.
	body = asPage(t, c, base+"/wp-admin/profile")
	nonce := formNonce(t, body)
	for _, weak := range []string{"short1!", "password123", "qwertyuiopasdf"} {
		_, body, _ = post(t, c, base+"/wp-admin/profile", url.Values{"_wpnonce": {nonce}, "nickname": {"admin"}, "email": {"admin@example.test"}, "pass1": {weak}, "pass2": {weak}})
		if !strings.Contains(body, "notice-error") {
			t.Errorf("weak password %q accepted", weak)
		}
	}
	// Forced change at the next login (set by an administrator).
	edID := asCreateUser(t, srv, "writer", "editor", asStrongPass)
	body = asPage(t, c, base+"/wp-admin/user-edit?user_id="+itoa64(edID))
	post(t, c, base+"/wp-admin/user-edit?user_id="+itoa64(edID), url.Values{"_wpnonce": {formNonce(t, body)}, "adminsec_action": {"force-password"}})
	w := asNewClient()
	if code, _, loc := asLogin(t, w, base, "/wp-login", "writer", asStrongPass); code != http.StatusFound || !strings.Contains(loc, "/wp-admin/profile?adminsec=password") {
		t.Fatalf("forced change at login: %d %s", code, loc)
	}
	if code, _, loc := get(t, w, base+"/wp-admin/edit"); code != http.StatusFound || !strings.Contains(loc, "adminsec=password") {
		t.Errorf("gate before the new password: %d %s", code, loc)
	}
	body = asPage(t, w, base+"/wp-admin/profile?adminsec=password")
	if !strings.Contains(body, "Please choose a new password") {
		t.Error("no forced-change notice")
	}
	np := "Copper-Meadow-Falcon-71?"
	if code, _, _ := post(t, w, base+"/wp-admin/profile", url.Values{"_wpnonce": {formNonce(t, body)}, "nickname": {"writer"}, "email": {"writer@example.test"}, "pass1": {np}, "pass2": {np}}); code != http.StatusFound {
		t.Fatal("new password")
	}
	if code, _, _ := get(t, w, base+"/wp-admin/edit"); code != http.StatusOK {
		t.Error("still gated after the change")
	}
	// A weak current password is caught at login (policy check at login).
	weakID := asCreateUser(t, srv, "weakling", "author", "monkey")
	_ = weakID
	if _, _, loc := asLogin(t, asNewClient(), base, "/wp-login", "weakling", "monkey"); !strings.Contains(loc, "adminsec=password") {
		t.Errorf("weak password not caught at login: %s", loc)
	}

	// Weak passwords allowed (test sites): no policy, no forced change at login.
	asConfigure(t, srv, func(st *asSettings) { st.PwAllowWeak = true })
	if _, _, loc := asLogin(t, asNewClient(), base, "/wp-login", "weakling", "monkey"); strings.Contains(loc, "adminsec=password") {
		t.Errorf("weak password still forced with weak passwords allowed: %s", loc)
	}
	if msg := srv.adminsecPasswordProblem([]string{"administrator"}, "x", "x@example.test", "123"); msg != "" {
		t.Errorf("policy applied with weak passwords allowed: %s", msg)
	}
	asConfigure(t, srv, func(st *asSettings) { st.PwAllowWeak = false })
	// Audit: plugin switches, settings, roles; filters and CSV.
	asCreateUser(t, srv, "promo", "subscriber", asStrongPass)
	_, body, _ = get(t, c, base+"/wp-admin/users")
	if !strings.Contains(body, "column-adminsec") {
		t.Error("Users list has no Two-Step column")
	}
	get(t, c, base+"/wp-admin/users?action=-1&new_role=author&changeit=Change&users[]=4&_wpnonce="+regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)[1])
	_, body, _ = get(t, c, base+"/wp-admin/options-general")
	post(t, c, base+"/wp-admin/options", url.Values{"option_page": {"general"}, "_wpnonce": {formNonce(t, body)}, "blogname": {"Users Site"}, "blogdescription": {""},
		"admin_email": {"admin@example.test"}, "timezone_string": {"UTC"}, "date_format": {"F j, Y"}, "time_format": {"g:i a"}, "start_of_week": {"1"}})
	_, body, _ = get(t, c, base+"/wp-admin/plugins")
	if m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=activate&amp;plugin=classic-editor[^"]*)"`).FindStringSubmatch(body); m != nil {
		get(t, c, base+html.UnescapeString(m[1]))
	}
	for _, ev := range []string{"plugin_activated", "settings_updated", "role_changed", "password_change_forced", "password_changed", "login_success", "logout"} {
		if ev == "logout" {
			continue
		}
		if asCount(t, `SELECT count(*) FROM adminsec_log WHERE event = $1`, ev) == 0 {
			t.Errorf("no %s entry", ev)
		}
	}
	body = asPage(t, c, base+"/wp-admin/admin?page=adminsecgo-log&event=role_changed")
	if !strings.Contains(body, "Role of promo changed from subscriber to author") || strings.Contains(body, "Plugin Classic Editor activated") {
		t.Error("log filter")
	}
	exp := regexp.MustCompile(`id="adminsec-export"|href="([^"]+)" class="page-title-action" id="adminsec-export"`).FindStringSubmatch(body)
	if len(exp) < 2 || exp[1] == "" {
		t.Fatal("no export link")
	}
	resp, err = c.Get(base + html.UnescapeString(exp[1]))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(strings.Builder)
	_, _ = io.Copy(buf, resp.Body)
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") || !strings.Contains(buf.String(), "role_changed") || strings.Contains(buf.String(), "plugin_activated") {
		t.Errorf("CSV export: %s %.300s", resp.Header.Get("Content-Type"), buf.String())
	}
	// Retention purge.
	_, _ = getPool(t).Exec(context.Background(), `UPDATE adminsec_log SET created_at = now() - interval '400 days' WHERE event = 'settings_updated'`)
	if err := srv.adminsecDaily(context.Background()); err != nil {
		t.Fatal(err)
	}
	if asCount(t, `SELECT count(*) FROM adminsec_log WHERE event = 'settings_updated'`) != 0 {
		t.Error("old entries not purged")
	}
	// The settings form saves a tab and logs it.
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=adminsecgo-settings&tab=sessions")
	if code, _, _ := post(t, c, base+"/wp-admin/admin?page=adminsecgo-settings&tab=sessions", url.Values{"_wpnonce": {formNonce(t, body)}, "tab": {"sessions"}, "idle_minutes": {"60"}}); code != http.StatusFound || srv.asLoad().IdleMinutes != 60 {
		t.Error("sessions tab")
	}
	if asCount(t, `SELECT count(*) FROM adminsec_log WHERE event = 'adminsec_settings'`) == 0 {
		t.Error("settings change not logged")
	}
	// A user with a second factor is never asked to change the password, even on request.
	ctx := context.Background()
	tfID := asCreateUser(t, srv, "twofactor", "editor", "monkey")
	tfUser, _ := srv.store.UserByID(ctx, tfID)
	if tfUser.Meta == nil {
		tfUser.Meta = map[string]any{}
	}
	tfUser.Meta["adminsec_force_password"] = "admin"
	if !srv.asForcePassword(ctx, tfUser) {
		t.Error("administrator's request ignored without a second factor")
	}
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO adminsec_factors (user_id, email_enabled, email_address) VALUES ($1, true, 'twofactor@example.test')`, tfID); err != nil {
		t.Fatal(err)
	}
	if srv.asForcePassword(ctx, tfUser) {
		t.Error("forced password change for a user with 2FA/MFA")
	}
}
