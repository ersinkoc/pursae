package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"wpgo/internal/auth"
	"wpgo/internal/store"
)

// fakeGoogle is an httptest stand-in for Google's authorize, token and userinfo endpoints. The test
// plays the browser on the consent screen: it reads state, nonce and code_challenge from the
// authorize redirect and calls the callback with a code the token endpoint knows.
type fakeGoogle struct {
	srv          *httptest.Server
	clientID     string
	secret       string
	mu           sync.Mutex
	codes        map[string]fakeGoogleCode
	sub, email   string
	verified     any
	aud          any
	nonce        string // overrides the nonce in the id_token when set
	userinfoSub  string // overrides the userinfo sub when set
	exp          time.Time
	name         string
	lastVerifier string
}

type fakeGoogleCode struct{ nonce, challenge, redirectURI string }

func newFakeGoogle(t *testing.T) *fakeGoogle {
	fg := &fakeGoogle{clientID: "cid.apps.googleusercontent.com", secret: "shh", codes: map[string]fakeGoogleCode{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		fg.mu.Lock()
		cd, ok := fg.codes[r.PostForm.Get("code")]
		delete(fg.codes, r.PostForm.Get("code"))
		fg.lastVerifier = r.PostForm.Get("code_verifier")
		fg.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || r.PostForm.Get("client_id") != fg.clientID || r.PostForm.Get("client_secret") != fg.secret ||
			r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("redirect_uri") != cd.redirectURI ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != cd.challenge {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		nonce := cd.nonce
		if fg.nonce != "" {
			nonce = fg.nonce
		}
		var aud any = fg.clientID
		if fg.aud != nil {
			aud = fg.aud
		}
		exp := fg.exp
		if exp.IsZero() {
			exp = time.Now().Add(time.Hour)
		}
		claims := map[string]any{"iss": "https://accounts.google.com", "aud": aud, "exp": exp.Unix(), "iat": time.Now().Unix(),
			"nonce": nonce, "sub": fg.sub, "email": fg.email, "email_verified": true}
		hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
		pl, _ := json.Marshal(claims)
		idt := base64.RawURLEncoding.EncodeToString(hdr) + "." + base64.RawURLEncoding.EncodeToString(pl) + ".c2ln"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-" + fg.sub, "id_token": idt, "token_type": "Bearer", "expires_in": 3600})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at-"+fg.sub {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		sub := fg.sub
		if fg.userinfoSub != "" {
			sub = fg.userinfoSub
		}
		var verified any = true
		if fg.verified != nil {
			verified = fg.verified
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"sub": sub, "email": fg.email, "email_verified": verified, "name": fg.name,
			"given_name": "", "family_name": ""})
	})
	fg.srv = httptest.NewServer(mux)
	t.Cleanup(fg.srv.Close)
	oldA, oldT, oldU := forumGoogleAuthURL, forumGoogleTokenURL, forumGoogleUserinfoURL
	forumGoogleAuthURL, forumGoogleTokenURL, forumGoogleUserinfoURL = fg.srv.URL+"/auth", fg.srv.URL+"/token", fg.srv.URL+"/userinfo"
	t.Cleanup(func() { forumGoogleAuthURL, forumGoogleTokenURL, forumGoogleUserinfoURL = oldA, oldT, oldU })
	return fg
}

// as sets the identity Google returns next and clears every override.
func (fg *fakeGoogle) as(sub, email string) *fakeGoogle {
	fg.sub, fg.email, fg.name = sub, email, "Test Person"
	fg.verified, fg.aud, fg.nonce, fg.userinfoSub, fg.exp = nil, nil, "", "", time.Time{}
	return fg
}

// forumGoogleRun drives one sign-in: start (with query), the consent screen, the callback. It
// returns the callback's status and Location. stateOverride replaces the state sent back when set.
func forumGoogleRun(t *testing.T, base string, c *http.Client, fg *fakeGoogle, query, stateOverride string) (int, string) {
	t.Helper()
	code, _, loc := get(t, c, base+"/forum-auth/google"+query)
	if code != http.StatusFound || !strings.HasPrefix(loc, fg.srv.URL+"/auth?") {
		t.Fatalf("start: %d %s", code, loc)
	}
	u, _ := url.Parse(loc)
	q := u.Query()
	if q.Get("client_id") != fg.clientID || q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" ||
		q.Get("scope") != "openid email profile" || q.Get("redirect_uri") != base+"/forum-auth/google/callback" || q.Get("nonce") == "" {
		t.Fatalf("authorize params: %v", q)
	}
	authCode := auth.RandomToken(12)
	fg.mu.Lock()
	fg.codes[authCode] = fakeGoogleCode{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirectURI: q.Get("redirect_uri")}
	fg.mu.Unlock()
	state := q.Get("state")
	if stateOverride != "" {
		state = stateOverride
	}
	code, _, loc = get(t, c, base+"/forum-auth/google/callback?code="+url.QueryEscape(authCode)+"&state="+url.QueryEscape(state))
	return code, loc
}

func forumGoogleEnable(t *testing.T, srv *Server, fg *fakeGoogle) {
	t.Helper()
	st := srv.forumLoad()
	st.GoogleEnabled, st.GoogleClientID, st.GoogleClientSecret = true, fg.clientID, fg.secret
	if err := srv.forumSave(context.Background(), st); err != nil {
		t.Fatal(err)
	}
}

// Google sign-in against a fake Google: sign-up with the role, every refused check, the account
// resolution rules, link and unlink from the account page, and off-site redirects.
func TestForumGoogle(t *testing.T) {
	ts, _, srv := forumAuthSetup(t)
	base := ts.URL
	ctx := context.Background()
	fg := newFakeGoogle(t)

	// Not configured: no routes, no button.
	if code, _, _ := get(t, forumJarClient(), base+"/forum-auth/google"); code != http.StatusNotFound {
		t.Errorf("start while not configured: %d", code)
	}
	forumGoogleEnable(t, srv, fg)
	_, body, _ := get(t, forumJarClient(), base+"/forums/login/?redirect=/forums/t/7/hello/")
	if !strings.Contains(body, "Continue with Google") || !strings.Contains(body, `class="pf-divider"`) ||
		!strings.Contains(body, `href="/forum-auth/google?redirect=%2Fforums%2Ft%2F7%2Fhello%2F"`) {
		t.Error("no Google button on the login view")
	}
	if _, body, _ := get(t, forumJarClient(), base+"/forums/register/"); !strings.Contains(body, "Continue with Google") {
		t.Error("no Google button on the register view")
	}

	// Happy path: a new member with the forum role, logged in, back on the page they came from.
	c := forumJarClient()
	code, loc := forumGoogleRun(t, base, c, fg.as("sub-new", "newbie@example.test"), "?redirect=/forums/t/7/hello/", "")
	if code != http.StatusFound || loc != "/forums/t/7/hello/" {
		t.Fatalf("happy path: %d %s", code, loc)
	}
	if len(fg.lastVerifier) < 43 {
		t.Errorf("PKCE verifier too short: %d", len(fg.lastVerifier))
	}
	nu := forumUserByLogin(t, srv, "newbie@example.test")
	if nu.PrimaryRole() != forumRoleParticipant || forumMetaStr(nu.Meta, forumUMetaGoogleSub) != "sub-new" || forumMetaBool(nu.Meta, forumUMetaUnverified) ||
		!forumNoPassword(nu) || !forumEmailProven(nu) || nu.DisplayName != "Test Person" {
		t.Errorf("new Google member: role %s meta %v name %q", nu.PrimaryRole(), nu.Meta, nu.DisplayName)
	}
	if code, body, _ := get(t, c, base+"/forums/account/"); code != http.StatusOK || !strings.Contains(body, "Connected as newbie@example.test") ||
		strings.Contains(body, `value="google_unlink"`) || strings.Contains(body, `name="current_pass"`) {
		t.Errorf("account after Google sign-up: %d", code)
	}
	// Unlink is refused while the member has no password.
	_, body, _ = get(t, c, base+"/forums/account/")
	pn := forumAuthFormNonce(t, body, "password")
	if code, _, _ := post(t, c, base+"/forums/account/", url.Values{"forum_action": {"google_unlink"}, "_wpnonce": {pn}}); code != http.StatusUnprocessableEntity {
		t.Errorf("unlink without a password: %d", code)
	}
	if forumMetaStr(forumUserByLogin(t, srv, "newbie@example.test").Meta, forumUMetaGoogleSub) == "" {
		t.Fatal("unlinked without a password")
	}
	// The same subject again signs the same member in (no second account).
	c2 := forumJarClient()
	if code, loc := forumGoogleRun(t, base, c2, fg.as("sub-new", "newbie@example.test"), "", ""); code != http.StatusFound || loc != "/forums/" {
		t.Errorf("known subject: %d %s", code, loc)
	}
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email) = 'newbie@example.test'`).Scan(&n)
	if n != 1 {
		t.Errorf("accounts for one subject: %d", n)
	}

	// Every refused check ends on the login view with google_failed and creates nobody.
	refusals := []struct {
		name string
		set  func(*fakeGoogle)
	}{
		{"wrong aud", func(f *fakeGoogle) { f.aud = "someone-else" }},
		{"aud list without azp", func(f *fakeGoogle) { f.aud = []string{f.clientID, "other"} }},
		{"wrong nonce", func(f *fakeGoogle) { f.nonce = "not-the-nonce" }},
		{"expired id_token", func(f *fakeGoogle) { f.exp = time.Now().Add(-time.Minute) }},
		{"email not verified", func(f *fakeGoogle) { f.verified = false }},
		{"userinfo sub mismatch", func(f *fakeGoogle) { f.userinfoSub = "sub-other" }},
	}
	for _, r := range refusals {
		fg.as("sub-x", "x@example.test")
		r.set(fg)
		code, loc := forumGoogleRun(t, base, forumJarClient(), fg, "", "")
		if code != http.StatusFound || !strings.HasSuffix(loc, "/forums/login/?forum_msg=google_failed") {
			t.Errorf("%s: %d %s", r.name, code, loc)
		}
	}
	if code, loc := forumGoogleRun(t, base, forumJarClient(), fg.as("sub-x", "x@example.test"), "", "forged-state"); code != http.StatusFound || !strings.Contains(loc, "google_failed") {
		t.Errorf("state mismatch: %d %s", code, loc)
	}
	if _, err := srv.store.UserByLoginOrEmail(ctx, "x@example.test"); err == nil {
		t.Error("a refused sign-in created a user")
	}
	// The cookie: a bad HMAC, an expired one, none at all.
	cb := func(cl *http.Client, value, state string) string {
		cbURL, _ := url.Parse(base + forumAuthPrefix + "/")
		if value != "" {
			cl.Jar.SetCookies(cbURL, []*http.Cookie{{Name: forumOAuthCookie, Value: value, Path: forumAuthPrefix + "/"}})
		}
		_, _, loc := get(t, cl, base+"/forum-auth/google/callback?code=abc&state="+state)
		return loc
	}
	good := srv.forumOAuthEncode(forumOAuthState{S: "st", V: strings.Repeat("v", 50), N: "nn", R: "/forums/", E: time.Now().Add(time.Minute).Unix()})
	payload, _, _ := strings.Cut(good, ".")
	if loc := cb(forumJarClient(), payload+"."+strings.Repeat("0", 64), "st"); !strings.Contains(loc, "google_failed") {
		t.Errorf("bad HMAC: %s", loc)
	}
	expired := srv.forumOAuthEncode(forumOAuthState{S: "st", V: strings.Repeat("v", 50), N: "nn", R: "/forums/", E: time.Now().Add(-time.Second).Unix()})
	if loc := cb(forumJarClient(), expired, "st"); !strings.Contains(loc, "google_failed") {
		t.Errorf("expired cookie: %s", loc)
	}
	if loc := cb(forumJarClient(), "", "st"); !strings.Contains(loc, "google_failed") {
		t.Errorf("no cookie: %s", loc)
	}
	// Cancelled on Google's screen.
	{
		cl := forumJarClient()
		_, _, start := get(t, cl, base+"/forum-auth/google")
		su, _ := url.Parse(start)
		if _, _, loc := get(t, cl, base+"/forum-auth/google/callback?error=access_denied&state="+su.Query().Get("state")); !strings.Contains(loc, "google_cancelled") {
			t.Errorf("cancel: %s", loc)
		}
		if _, body, _ := get(t, cl, base+"/forums/login/?forum_msg=google_cancelled"); !strings.Contains(body, "pf-notice--error") {
			t.Error("a refusal notice is not shown as an error")
		}
	}

	// Account resolution by email: a subscriber links automatically, an editor must link by hand.
	sid, _ := srv.store.CreateUser(ctx, store.NewUser{Login: "sam", Email: "Sam@Example.test", PasswordHash: "x", Role: "subscriber"})
	_ = srv.forumSetUserMeta(ctx, sid, map[string]any{forumUMetaUnverified: true})
	cs := forumJarClient()
	if code, loc := forumGoogleRun(t, base, cs, fg.as("sub-sam", "sam@example.test"), "", ""); code != http.StatusFound || loc != "/forums/" {
		t.Errorf("subscriber auto-link: %d %s", code, loc)
	}
	if sam, _ := srv.store.UserByID(ctx, sid); forumMetaStr(sam.Meta, forumUMetaGoogleSub) != "sub-sam" || forumMetaBool(sam.Meta, forumUMetaUnverified) {
		t.Errorf("auto-link meta: %v", sam.Meta)
	} else if sam.PasswordHash == "x" {
		t.Error("auto-linking an unconfirmed account kept the password of whoever signed up with the address")
	}
	if _, body, _ := get(t, cs, base+"/forums/account/"); !strings.Contains(body, "Sam@Example.test") {
		t.Error("auto-link did not sign the subscriber in")
	}
	eid, _ := srv.store.CreateUser(ctx, store.NewUser{Login: "eddie", Email: "eddie@example.test", PasswordHash: "x", Role: "editor"})
	if code, loc := forumGoogleRun(t, base, forumJarClient(), fg.as("sub-eddie", "eddie@example.test"), "", ""); code != http.StatusFound || !strings.HasSuffix(loc, "forum_msg=google_link_required") {
		t.Errorf("editor email: %d %s", code, loc)
	}
	if ed, _ := srv.store.UserByID(ctx, eid); forumMetaStr(ed.Meta, forumUMetaGoogleSub) != "" {
		t.Error("editor auto-linked")
	}
	if code, loc := forumGoogleRun(t, base, forumJarClient(), fg.as("sub-admin", "admin@example.test"), "", ""); code != http.StatusFound || !strings.HasSuffix(loc, "forum_msg=google_link_required") {
		t.Errorf("admin email: %d %s", code, loc)
	}
	// An administrator with a linked Google account still cannot sign in with it.
	admin := forumUserByLogin(t, srv, "admin")
	_ = srv.forumSetUserMeta(ctx, admin.ID, map[string]any{forumUMetaGoogleSub: "sub-admin"})
	ca := forumJarClient()
	if code, loc := forumGoogleRun(t, base, ca, fg.as("sub-admin", "admin@example.test"), "", ""); code != http.StatusFound || !strings.HasSuffix(loc, "forum_msg=google_blocked") {
		t.Errorf("admin sign-in: %d %s", code, loc)
	}
	if code, _, _ := get(t, ca, base+"/forums/account/"); code != http.StatusFound {
		t.Error("blocked admin got a session")
	}

	// Link mode from the account page; a subject linked elsewhere is refused; unlink with a password.
	hash, _ := auth.HashPassword("pat password")
	pid, _ := srv.store.CreateUser(ctx, store.NewUser{Login: "pat", Email: "pat@example.test", PasswordHash: hash, Role: forumRoleParticipant})
	cp := forumJarClient()
	if code, _, _ := forumLogin(t, base, cp, "pat", "pat password", ""); code != http.StatusFound {
		t.Fatal("pat login")
	}
	if pat, _ := srv.store.UserByID(ctx, pid); forumNoPassword(pat) || !forumNeedCurrent(pat) {
		t.Error("an account made outside the forum counts as having no password")
	}
	_, body, _ = get(t, cp, base+"/forums/account/")
	if !strings.Contains(body, `href="/forum-auth/google?link=1&amp;redirect=%2Fforums%2Faccount%2F"`) {
		t.Error("no Connect Google link on the account page")
	}
	if code, loc := forumGoogleRun(t, base, cp, fg.as("sub-new", "pat-google@example.test"), "?link=1", ""); code != http.StatusFound || !strings.HasSuffix(loc, "/forums/account/?forum_msg=google_failed") {
		t.Errorf("link a subject of another member: %d %s", code, loc)
	}
	if code, loc := forumGoogleRun(t, base, cp, fg.as("sub-pat", "pat-google@example.test"), "?link=1", ""); code != http.StatusFound || !strings.HasSuffix(loc, "/forums/account/?forum_msg=google_linked") {
		t.Errorf("link: %d %s", code, loc)
	}
	if pat, _ := srv.store.UserByID(ctx, pid); forumMetaStr(pat.Meta, forumUMetaGoogleSub) != "sub-pat" || forumMetaStr(pat.Meta, forumUMetaGoogleEmail) != "pat-google@example.test" {
		t.Errorf("link meta: %v", pat.Meta)
	}
	// Linking Google never lets the session change the password without the current one.
	_, body, _ = get(t, cp, base+"/forums/account/")
	if !strings.Contains(body, `name="current_pass"`) {
		t.Error("account page of a linked member with a password does not ask for the current one")
	}
	pwn := forumAuthFormNonce(t, body, "password")
	if code, _, _ := post(t, cp, base+"/forums/account/", url.Values{"forum_action": {"password"}, "_wpnonce": {pwn}, "new_pass": {"Taken over 12345!"}}); code != http.StatusUnprocessableEntity {
		t.Errorf("password change without the current one after linking Google: %d", code)
	}
	if pat, _ := srv.store.UserByID(ctx, pid); pat.PasswordHash != hash {
		t.Fatal("linking Google let the session replace the password")
	}
	if code, _, loc := get(t, forumJarClient(), base+"/forum-auth/google?link=1"); code != http.StatusFound || !strings.Contains(loc, "/forums/login/") {
		t.Errorf("link mode logged out: %d %s", code, loc)
	}
	_, body, _ = get(t, cp, base+"/forums/account/")
	un := forumAuthFormNonce(t, body, "google_unlink")
	if code, _, loc := post(t, cp, base+"/forums/account/", url.Values{"forum_action": {"google_unlink"}, "_wpnonce": {un}}); code != http.StatusFound || !strings.Contains(loc, "google_unlinked") {
		t.Errorf("unlink: %d %s", code, loc)
	}
	if pat, _ := srv.store.UserByID(ctx, pid); forumMetaStr(pat.Meta, forumUMetaGoogleSub) != "" {
		t.Error("still linked")
	}

	// Off-site return paths fall back to the forum index.
	for _, bad := range []string{"//evil.example/x", "https://evil.example/", "/\\evil.example", "/\t/evil.example"} {
		code, loc := forumGoogleRun(t, base, forumJarClient(), fg.as("sub-new", "newbie@example.test"), "?redirect="+url.QueryEscape(bad), "")
		if code != http.StatusFound || loc != "/forums/" {
			t.Errorf("redirect %q: %d %s", bad, code, loc)
		}
	}

	// Registration closed: a new Google identity is refused.
	st := srv.forumLoad()
	st.Registration = false
	_ = srv.forumSave(ctx, st)
	if code, loc := forumGoogleRun(t, base, forumJarClient(), fg.as("sub-late", "late@example.test"), "", ""); code != http.StatusFound || !strings.HasSuffix(loc, "forum_msg=registration_closed") {
		t.Errorf("registration closed: %d %s", code, loc)
	}
	if _, err := srv.store.UserByLoginOrEmail(ctx, "late@example.test"); err == nil {
		t.Error("closed registration created a Google member")
	}
}

// Unit: the id_token claim checks and the confirmation key check.
func TestForumGoogleClaims(t *testing.T) {
	now := time.Now()
	mk := func(claims map[string]any) string {
		b, _ := json.Marshal(claims)
		return "e30." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
	}
	ok := map[string]any{"iss": "accounts.google.com", "aud": "cid", "exp": now.Add(time.Hour).Unix(), "nonce": "n1", "sub": "123"}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for a, b := range ok {
			m[a] = b
		}
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
		return m
	}
	if _, err := forumCheckIDToken(mk(ok), "cid", "n1", now); err != nil {
		t.Fatalf("valid token: %v", err)
	}
	if _, err := forumCheckIDToken(mk(with("aud", []string{"cid", "x"})), "cid", "n1", now); err == nil {
		t.Error("multi-aud without azp accepted")
	}
	m := with("aud", []string{"cid", "x"})
	m["azp"] = "cid"
	if _, err := forumCheckIDToken(mk(m), "cid", "n1", now); err != nil {
		t.Errorf("multi-aud with azp: %v", err)
	}
	for name, claims := range map[string]map[string]any{
		"iss": with("iss", "https://evil.example"), "aud": with("aud", "other"), "exp": with("exp", now.Add(-time.Second).Unix()),
		"no exp": with("exp", nil), "nonce": with("nonce", "n2"), "no nonce": with("nonce", nil), "no sub": with("sub", nil),
	} {
		if _, err := forumCheckIDToken(mk(claims), "cid", "n1", now); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := forumCheckIDToken("not.a-token", "cid", "n1", now); err == nil {
		t.Error("malformed token accepted")
	}
	if forumPKCEChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk") != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Error("PKCE S256 differs from RFC 7636 appendix B")
	}

	key := "abc123"
	sum := sha256.Sum256([]byte(key))
	h := ""
	for _, b := range sum {
		h += string("0123456789abcdef"[b>>4]) + string("0123456789abcdef"[b&15])
	}
	u := &store.User{Meta: map[string]any{forumUMetaVerify: map[string]any{"hash": h, "expires": float64(now.Add(time.Hour).Unix())}}}
	if !forumCheckVerify(u, key, now) || forumCheckVerify(u, key+"x", now) || forumCheckVerify(u, "", now) || forumCheckVerify(u, key, now.Add(2*time.Hour)) {
		t.Error("forumCheckVerify")
	}
	for raw, want := range map[string]string{"/forums/t/1/": "/forums/t/1/", "/forums/?a=b": "/forums/?a=b", "//evil": "", "https://x": "", "/\\x": "", "": ""} {
		if got := forumSafeLocal(raw); got != want {
			t.Errorf("forumSafeLocal(%q) = %q, want %q", raw, got, want)
		}
	}
}

// A low-privilege account whose address nobody proved (a Commerce checkout customer, no forum flags)
// is linked to the Google user only with a fresh password and every old session ended: whoever made
// it with the victim's address keeps neither (account pre-hijacking). A proven address links as is.
func TestForumGooglePreAccountTakeover(t *testing.T) {
	ts, _, srv := forumAuthSetup(t)
	base := ts.URL
	ctx := context.Background()
	fg := newFakeGoogle(t)
	forumGoogleEnable(t, srv, fg)

	hash, _ := auth.HashPassword("attacker password")
	// As a Commerce checkout makes it: the password chosen by whoever typed the address, no proof.
	vid, err := srv.store.CreateUser(ctx, store.NewUser{Login: "victim", Email: "victim@example.test", PasswordHash: hash, Role: "subscriber"})
	if err != nil {
		t.Fatal(err)
	}
	attacker := forumJarClient()
	if code, _, _ := forumLogin(t, base, attacker, "victim", "attacker password", ""); code != http.StatusFound {
		t.Fatal("attacker login")
	}
	if code, _, _ := get(t, attacker, base+"/forums/account/"); code != http.StatusOK {
		t.Fatal("attacker has no session")
	}
	victim := forumJarClient()
	if code, loc := forumGoogleRun(t, base, victim, fg.as("sub-victim", "victim@example.test"), "", ""); code != http.StatusFound || loc != "/forums/" {
		t.Fatalf("auto-link: %d %s", code, loc)
	}
	u, _ := srv.store.UserByID(ctx, vid)
	if forumMetaStr(u.Meta, forumUMetaGoogleSub) != "sub-victim" {
		t.Fatalf("not linked: %v", u.Meta)
	}
	if ok, _ := auth.CheckPasswordCtx(ctx, "attacker password", u.PasswordHash); ok {
		t.Error("auto-link kept the password chosen by whoever made the account")
	}
	if code, _, _ := get(t, attacker, base+"/forums/account/"); code == http.StatusOK {
		t.Error("auto-link kept the other session alive")
	}
	if !forumNoPassword(u) || !forumEmailProven(u) {
		t.Errorf("after auto-link: meta %v", u.Meta)
	}
	if code, _, _ := get(t, victim, base+"/forums/account/"); code != http.StatusOK {
		t.Error("the Google user was not signed in")
	}

	// A proven address (confirmation link done) links without touching the password.
	h2, _ := auth.HashPassword("own password")
	pid, _ := srv.store.CreateUser(ctx, store.NewUser{Login: "proven", Email: "proven@example.test", PasswordHash: h2, Role: "subscriber"})
	_ = srv.forumSetUserMeta(ctx, pid, map[string]any{forumUMetaEmailVerified: "proven@example.test"})
	if code, loc := forumGoogleRun(t, base, forumJarClient(), fg.as("sub-proven", "proven@example.test"), "", ""); code != http.StatusFound || loc != "/forums/" {
		t.Fatalf("proven auto-link: %d %s", code, loc)
	}
	if p, _ := srv.store.UserByID(ctx, pid); p.PasswordHash != h2 || forumNoPassword(p) {
		t.Error("a proven address lost its password on auto-link")
	}
	// A proof for an older address does not count once the email changed.
	cid, _ := srv.store.CreateUser(ctx, store.NewUser{Login: "changed", Email: "changed@example.test", PasswordHash: h2, Role: "subscriber"})
	_ = srv.forumSetUserMeta(ctx, cid, map[string]any{forumUMetaEmailVerified: "old@example.test"})
	if code, _ := forumGoogleRun(t, base, forumJarClient(), fg.as("sub-changed", "changed@example.test"), "", ""); code != http.StatusFound {
		t.Fatal("changed auto-link")
	}
	if c, _ := srv.store.UserByID(ctx, cid); c.PasswordHash == h2 {
		t.Error("a proof of an old address let the auto-link keep the password")
	}
}

// Pursae Security's login block list applies to Google sign-in, as to the password form.
func TestForumGoogleSecurityBlockList(t *testing.T) {
	ts, admin, srv := forumAuthSetup(t)
	base := ts.URL
	ctx := context.Background()
	fg := newFakeGoogle(t)
	forumGoogleEnable(t, srv, fg)
	asToggle(t, admin, base, "activate")
	if !srv.adminsecOn() {
		t.Fatal("Pursae Security not active")
	}
	_, _ = srv.store.CreateUser(ctx, store.NewUser{Login: "member", Email: "member@example.test", PasswordHash: "x", Role: forumRoleParticipant})
	asConfigure(t, srv, func(st *asSettings) { st.AllowList, st.BlockList, st.BlockListSite = "", "127.0.0.1", false })
	c := forumJarClient()
	if code, loc := forumGoogleRun(t, base, c, fg.as("sub-member", "member@example.test"), "", ""); code != http.StatusFound || !strings.HasSuffix(loc, "forum_msg=login_blocked") {
		t.Errorf("blocked IP: %d %s", code, loc)
	}
	if code, _, _ := get(t, c, base+"/forums/account/"); code == http.StatusOK {
		t.Error("a blocked IP got a session through Google")
	}
	asConfigure(t, srv, func(st *asSettings) { st.BlockList = "" })
	if code, loc := forumGoogleRun(t, base, c, fg.as("sub-member", "member@example.test"), "", ""); code != http.StatusFound || loc != "/forums/" {
		t.Errorf("unblocked: %d %s", code, loc)
	}
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM adminsec_known_ips ki JOIN users u ON u.id = ki.user_id WHERE u.login = 'member'`).Scan(&n)
	if n == 0 {
		t.Error("Google sign-in was not recorded by Pursae Security")
	}
}
