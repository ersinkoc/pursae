package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"wpgo/internal/auth"
	"wpgo/internal/store"
)

// WordPress user_pass values made by WordPress's own code (see internal/auth/legacy_test.go).
var wpLegacyHashes = []struct{ login, kind, password, hash string }{
	{"wp_phpass", "phpass", "password", "$P$B2quFTY/dB/kUQqA.Jj1pGh9WakUwP."},
	{"wp_bcrypt68", "wp-bcrypt", "password", "$wp$2y$05$u//MIaiTMd889nQrv4tRLe6SFvcZuLdJ3PrPDBY/vdbwr4JX/Ye9u"},
	{"wp_bcrypt", "bcrypt", "password", "$2y$05$AAp6WbBcI7IPPADPfJFc0ew7e0BHKQN50PoHcW1YTnG5IGadAye.q"},
	{"wp_md5", "md5", "password", "5f4dcc3b5aa765d61d8327deb882cf99"},
}

// Users imported with a WordPress hash sign in through wp-login.php; the first sign-in replaces the
// hash with argon2id, a wrong password leaves it alone and still counts against the login throttle.
func TestLegacyPasswordLoginRehash(t *testing.T) {
	ts, c, srv := newTestServer(t, false)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	st := srv.store

	newClient := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar, CheckRedirect: c.CheckRedirect}
	}
	login := func(user, pass string) int {
		t.Helper()
		code, _, _ := post(t, newClient(), ts.URL+"/wp-login", url.Values{"log": {user}, "pwd": {pass}})
		return code
	}
	hashOf := func(login string) string {
		t.Helper()
		u, err := st.UserByLoginOrEmail(ctx, login)
		if err != nil {
			t.Fatal(err)
		}
		return u.PasswordHash
	}
	failures := func(login string) int {
		t.Helper()
		var n int
		if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM login_attempts WHERE lower(login) = lower($1) AND NOT success`, login).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	for _, v := range wpLegacyHashes {
		if _, err := st.CreateUser(ctx, store.NewUser{Login: v.login, Email: v.login + "@example.test", PasswordHash: v.hash, Role: "author"}); err != nil {
			t.Fatal(err)
		}
	}
	// One wrong password: refused, counted, hash untouched.
	if code := login("wp_phpass", "not-the-password"); code != http.StatusOK {
		t.Fatalf("wrong password: %d", code)
	}
	if got := hashOf("wp_phpass"); got != wpLegacyHashes[0].hash {
		t.Errorf("wrong password changed the hash to %q", got)
	}
	if n := failures("wp_phpass"); n != 1 {
		t.Errorf("failed attempts recorded = %d, want 1", n)
	}

	for _, v := range wpLegacyHashes {
		if code := login(v.login, v.password); code != http.StatusFound {
			t.Fatalf("%s: legacy login = %d, want 302", v.kind, code)
		}
		h := hashOf(v.login)
		if !strings.HasPrefix(h, "$argon2id$") {
			t.Fatalf("%s: hash after login = %q, want argon2id", v.kind, h)
		}
		if ok, _ := auth.CheckPassword(v.password, h); !ok {
			t.Errorf("%s: new argon2id hash does not verify the password", v.kind)
		}
		if code := login(v.login, v.password); code != http.StatusFound {
			t.Errorf("%s: second login = %d", v.kind, code)
		}
		if hashOf(v.login) != h {
			t.Errorf("%s: an argon2id hash was rehashed again", v.kind)
		}
	}

	// A malformed hash (a phpass hash with 2^30 rounds) is a wrong password, not a hang or a crash.
	if _, err := st.CreateUser(ctx, store.NewUser{Login: "wp_costly", Email: "costly@example.test", PasswordHash: "$P$S2quFTY/dB/kUQqA.Jj1pGh9WakUwP.", Role: "author"}); err != nil {
		t.Fatal(err)
	}
	if code := login("wp_costly", "password"); code != http.StatusOK {
		t.Errorf("malformed hash login = %d, want 200 (refused)", code)
	}
}

// checkUserPassword only replaces the hash it verified: a password changed in the meantime wins.
func TestCheckUserPasswordKeepsNewerHash(t *testing.T) {
	_, _, srv := newTestServer(t, false)
	ctx := context.Background()
	st := srv.store
	v := wpLegacyHashes[0]
	id, err := st.CreateUser(ctx, store.NewUser{Login: v.login, Email: "x@example.test", PasswordHash: v.hash, Role: "subscriber"})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := st.UserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := auth.HashPassword("changed elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserPassword(ctx, id, newer); err != nil {
		t.Fatal(err)
	}
	if !srv.checkUserPassword(ctx, stale, v.password) { // the copy read before the change still matches
		t.Fatal("stale copy did not verify")
	}
	if cur, _ := st.UserByID(ctx, id); cur.PasswordHash != newer {
		t.Errorf("rehash overwrote a newer password hash")
	}
	if stale.PasswordHash != v.hash {
		t.Errorf("in-memory hash changed although nothing was stored")
	}
	if srv.checkUserPassword(ctx, nil, "x") {
		t.Error("nil user accepted")
	}
}

// `user create --user_pass_hash` keeps a WordPress hash as-is (no double hashing) and refuses
// anything that is not one.
func TestCLIUserCreatePassHash(t *testing.T) {
	ts, c, srv := newTestServer(t, false)
	installAndLogin(t, ts, c)
	cfg := srv.cfg
	cfg.DatabaseURL = os.Getenv("WPGO_TEST_DB")
	ctx := context.Background()
	run := func(want bool, args ...string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := CLI(ctx, cfg, "pursae", args, strings.NewReader(""), &out, &errb)
		if (code == 0) != want {
			t.Fatalf("%v: exit %d\nstdout: %s\nstderr: %s", args, code, out.String(), errb.String())
		}
	}
	for _, v := range wpLegacyHashes {
		run(true, "user", "create", v.login, v.login+"@example.test", "--role=author", "--user_pass_hash="+v.hash, "--porcelain")
		u, err := srv.store.UserByLoginOrEmail(ctx, v.login)
		if err != nil {
			t.Fatal(err)
		}
		if u.PasswordHash != v.hash {
			t.Errorf("%s: stored %q, want the hash as given", v.kind, u.PasswordHash)
		}
	}
	// WordPress 6.8+ with wp_hash_password_algorithm = PASSWORD_ARGON2I / PASSWORD_ARGON2ID.
	argon2i := "$argon2i$v=19$m=1024,t=2,p=2$YzJBSzV4TUhkMzc3d3laeg$zqU/1IN0/AogfP4cmSJI1vc8lpXRW9/S0sYY2i2jHT0"
	argon2id := "$argon2id$v=19$m=65536,t=4,p=1$eHlUOXBqYXpaVUI0bkRTag$T0+xyqKdm6w6XcEjgzZvGbLwMYa5jUDZMlKW/AekIng"
	run(true, "user", "create", "wp_argon2i", "wp_argon2i@example.test", "--role=author", "--user_pass_hash="+argon2i, "--porcelain")
	run(true, "user", "create", "wp_argon2id", "wp_argon2id@example.test", "--role=author", "--user_pass_hash="+argon2id, "--porcelain")
	run(false, "user", "create", "bad4", "bad4@example.test", "--user_pass_hash=$argon2id$v=19$m=4194304,t=4,p=1$eHlUOXBqYXpaVUI0bkRTag$T0+xyqKdm6w6XcEjgzZvGbLwMYa5jUDZMlKW/AekIng")
	run(false, "user", "create", "bad1", "bad1@example.test", "--user_pass_hash=plaintext-password")
	run(false, "user", "create", "bad2", "bad2@example.test", "--user_pass_hash=$P$S2quFTY/dB/kUQqA.Jj1pGh9WakUwP.") // over the round cap
	run(false, "user", "create", "bad3", "bad3@example.test", "--user_pass=x", "--user_pass_hash="+wpLegacyHashes[0].hash)
	for _, login := range []string{"bad1", "bad2", "bad3", "bad4"} {
		if _, err := srv.store.UserByLoginOrEmail(ctx, login); err == nil {
			t.Errorf("%s was created", login)
		}
	}
	// The imported user signs in with the WordPress password.
	jar, _ := cookiejar.New(nil)
	cl := &http.Client{Jar: jar, CheckRedirect: c.CheckRedirect}
	if code, _, _ := post(t, cl, ts.URL+"/wp-login", url.Values{"log": {"wp_bcrypt68"}, "pwd": {"password"}}); code != http.StatusFound {
		t.Errorf("imported user login = %d", code)
	}
	for login, pw := range map[string]string{"wp_argon2i": "rasmuslerdorf", "wp_argon2id": "password"} {
		jar, _ := cookiejar.New(nil)
		cl := &http.Client{Jar: jar, CheckRedirect: c.CheckRedirect}
		if code, _, _ := post(t, cl, ts.URL+"/wp-login", url.Values{"log": {login}, "pwd": {pw}}); code != http.StatusFound {
			t.Errorf("%s login = %d", login, code)
		}
	}
	if u, _ := srv.store.UserByLoginOrEmail(ctx, "wp_argon2i"); u == nil || !strings.HasPrefix(u.PasswordHash, "$argon2id$") {
		t.Errorf("argon2i hash was not rehashed to argon2id")
	}
	if u, _ := srv.store.UserByLoginOrEmail(ctx, "wp_argon2id"); u == nil || u.PasswordHash != argon2id {
		t.Errorf("argon2id hash was rewritten")
	}
}

// Two sign-ins that verified the same legacy hash: the one whose conditional UPDATE loses adopts the
// stored argon2id hash, so a Headless Go token derived from u.PasswordHash still validates.
func TestCheckUserPasswordConcurrentRehash(t *testing.T) {
	_, _, srv := newTestServer(t, false)
	ctx := context.Background()
	st := srv.store
	v := wpLegacyHashes[0]
	id, err := st.CreateUser(ctx, store.NewUser{Login: v.login, Email: "x@example.test", PasswordHash: v.hash, Role: "subscriber"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.UserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	b := *a // both requests read the legacy hash before either stored the new one
	if !srv.checkUserPassword(ctx, a, v.password) || !srv.checkUserPassword(ctx, &b, v.password) {
		t.Fatal("legacy password did not verify")
	}
	cur, err := st.UserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cur.PasswordHash, "$argon2id$") || a.PasswordHash != cur.PasswordHash || b.PasswordHash != cur.PasswordHash {
		t.Errorf("stored %q, first %q, second %q: want all equal argon2id", cur.PasswordHash, a.PasswordHash, b.PasswordHash)
	}
}

// Two concurrent Headless Go logins on a legacy hash both return tokens that validate.
func TestHeadlessGoConcurrentLegacyLogin(t *testing.T) {
	base, _, srv := hgSite(t)
	ctx := context.Background()
	v := wpLegacyHashes[0]
	if _, err := srv.store.CreateUser(ctx, store.NewUser{Login: v.login, Email: "hg@example.test", PasswordHash: v.hash, Role: "subscriber"}); err != nil {
		t.Fatal(err)
	}
	hs := srv.hgLoad()
	hs.UserAuth = true
	if err := srv.hgSaveSettings(ctx, hs); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]hgResp, 2)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = hgCall(t, base, "POST", "/auth/local", "", map[string]any{"identifier": v.login, "password": v.password})
		}()
	}
	wg.Wait()
	for i, r := range results {
		jwt, _ := r.Body["jwt"].(string)
		if r.Code != http.StatusOK || jwt == "" {
			t.Fatalf("login %d: %d %s", i, r.Code, r.Raw)
		}
		if me := hgCall(t, base, "GET", "/users/me", jwt, nil); me.Code != http.StatusOK {
			t.Errorf("token of login %d rejected: %d %s", i, me.Code, me.Raw)
		}
	}
}

// A wrong password against a legacy hash also pays the argon2id dummy pass that an unknown login pays,
// so response times do not reveal which imported accounts exist. Checked by counting the dummy passes
// rather than timing them: wall-clock comparisons were flaky on shared CI runners.
func TestLegacyPasswordMismatchTiming(t *testing.T) {
	_, _, srv := newTestServer(t, false)
	ctx := context.Background()
	for _, v := range wpLegacyHashes {
		u := &store.User{ID: -1, PasswordHash: v.hash}
		before := auth.DummyPasswordChecks()
		if srv.checkUserPassword(ctx, u, "wrong") {
			t.Fatalf("%s: wrong password accepted", v.login)
		}
		if got := auth.DummyPasswordChecks() - before; got != 1 {
			t.Errorf("%s (%s): wrong password ran %d dummy argon2id passes, want 1", v.login, auth.HashKind(v.hash), got)
		}
	}
	// An argon2id hash already costs a full pass on a mismatch: no extra dummy pass.
	h, err := auth.HashPassword("right")
	if err != nil {
		t.Fatal(err)
	}
	before := auth.DummyPasswordChecks()
	if srv.checkUserPassword(ctx, &store.User{ID: -1, PasswordHash: h}, "wrong") {
		t.Fatal("wrong password accepted")
	}
	if got := auth.DummyPasswordChecks() - before; got != 0 {
		t.Errorf("argon2id mismatch ran %d dummy passes, want 0", got)
	}
}
