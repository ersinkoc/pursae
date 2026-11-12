package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/auth"
	"wpgo/internal/store"
)

// seedUserCredentials gives the user one application password, one OAuth grant and one pending OAuth code,
// as whoever holds the account could mint them. It returns the application password.
func seedUserCredentials(t *testing.T, srv *Server, uid int64) string {
	t.Helper()
	ctx := context.Background()
	plain, _, err := srv.createAppPassword(gooTestCtx(), uid, "minted app", "")
	if err != nil {
		t.Fatal(err)
	}
	client := fmt.Sprintf("00000000-0000-4000-8000-%012d", uid)
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO oauth_clients (client_id, name, redirect_uris) VALUES ($1, 'Minted', '{http://localhost/cb}')`, client); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO oauth_tokens (client_id, user_id, access_hash, refresh_hash, access_expires_at, refresh_expires_at)
		VALUES ($1, $2, $3, $4, now() + interval '1 hour', now() + interval '1 day')`, client, uid, fmt.Sprintf("acc-%d", uid), fmt.Sprintf("ref-%d", uid)); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO oauth_codes (code_hash, client_id, user_id, redirect_uri, challenge, expires_at)
		VALUES ($1, $2, $3, 'http://localhost/cb', 'x', now() + interval '5 minutes')`, fmt.Sprintf("code-%d", uid), client, uid); err != nil {
		t.Fatal(err)
	}
	return plain
}

// assertCredentialsRevoked fails when the user still has an application password, OAuth grant or OAuth code.
func assertCredentialsRevoked(t *testing.T, srv *Server, uid int64, what string) {
	t.Helper()
	ctx := context.Background()
	for _, table := range []string{"app_passwords", "oauth_tokens", "oauth_codes"} {
		var n int
		if err := srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE user_id = $1`, uid).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s kept %d row(s) in %s (%v)", what, n, table, err)
		}
	}
}

// basicStatus is the status of GET /wp-json/wp/v2/users/me?context=edit with Basic auth.
func basicStatus(t *testing.T, base, login, pass string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+"/wp-json/wp/v2/users/me?context=edit", nil)
	req.SetBasicAuth(login, pass)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	_, _ = io.ReadAll(r.Body)
	return r.StatusCode
}

// Account pre-hijacking through Pursae Forum's Google auto-link: whoever made an account with the
// victim's (unproven) address loses its password and sessions when the victim signs in with Google,
// and also every other credential they minted on it (application passwords, OAuth grants and codes),
// exactly as a core password change revokes them.
func TestForumGoogleAutoLinkRevokesCredentials(t *testing.T) {
	ts, _, srv := forumAuthSetup(t)
	base := ts.URL
	ctx := context.Background()
	fg := newFakeGoogle(t)
	forumGoogleEnable(t, srv, fg)

	hash, _ := auth.HashPassword("attacker password")
	vid, err := srv.store.CreateUser(ctx, store.NewUser{Login: "victim", Email: "victim@example.test", PasswordHash: hash, Role: "subscriber"})
	if err != nil {
		t.Fatal(err)
	}
	plain := seedUserCredentials(t, srv, vid)
	if code := basicStatus(t, base, "victim", plain); code != http.StatusOK {
		t.Fatalf("precondition: app password does not authenticate (%d)", code)
	}

	// The victim signs in with Google; the account is auto-linked to their Google identity.
	if code, loc := forumGoogleRun(t, base, forumJarClient(), fg.as("sub-victim", "victim@example.test"), "", ""); code != http.StatusFound || loc != "/forums/" {
		t.Fatalf("auto-link: %d %s", code, loc)
	}
	u, _ := srv.store.UserByID(ctx, vid)
	if forumMetaStr(u.Meta, forumUMetaGoogleSub) != "sub-victim" {
		t.Fatalf("not linked: %v", u.Meta)
	}
	assertCredentialsRevoked(t, srv, vid, "Google auto-link of an unproven address")
	// The match cache still holds the earlier hit; it must not outlive the deleted row.
	if code := basicStatus(t, base, "victim", plain); code == http.StatusOK {
		t.Errorf("the attacker's application password still authenticates as the Google-linked account (%d)", code)
	}
}

// A proven address is handed to Google without touching the owner's credentials.
func TestForumGoogleAutoLinkProvenKeepsCredentials(t *testing.T) {
	ts, _, srv := forumAuthSetup(t)
	ctx := context.Background()
	fg := newFakeGoogle(t)
	forumGoogleEnable(t, srv, fg)
	hash, _ := auth.HashPassword("owner password")
	uid, err := srv.store.CreateUser(ctx, store.NewUser{Login: "owner", Email: "owner@example.test", PasswordHash: hash, Role: "subscriber"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.forumSetUserMeta(ctx, uid, map[string]any{forumUMetaEmailVerified: "owner@example.test"}); err != nil {
		t.Fatal(err)
	}
	plain := seedUserCredentials(t, srv, uid)
	if code, loc := forumGoogleRun(t, ts.URL, forumJarClient(), fg.as("sub-owner", "owner@example.test"), "", ""); code != http.StatusFound || loc != "/forums/" {
		t.Fatalf("auto-link: %d %s", code, loc)
	}
	if code := basicStatus(t, ts.URL, "owner", plain); code != http.StatusOK {
		t.Errorf("a proven owner's application password stopped working after linking Google: %d", code)
	}
}

// Changing the password on the forum account page revokes credentials like every core password change.
func TestForumAccountPasswordChangeRevokesCredentials(t *testing.T) {
	ts, _, srv := forumAuthSetup(t)
	base := ts.URL
	ctx := context.Background()
	hash, _ := auth.HashPassword("long enough pw")
	uid, err := srv.store.CreateUser(ctx, store.NewUser{Login: "member", Email: "member@example.test", PasswordHash: hash, Role: "subscriber"})
	if err != nil {
		t.Fatal(err)
	}
	plain := seedUserCredentials(t, srv, uid)
	c := forumJarClient()
	if code, _, _ := forumLogin(t, base, c, "member", "long enough pw", ""); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	_, body, _ := get(t, c, base+"/forums/account/")
	pn := forumAuthFormNonce(t, body, "password")
	if code, _, loc := post(t, c, base+"/forums/account/", url.Values{"forum_action": {"password"}, "_wpnonce": {pn}, "current_pass": {"long enough pw"}, "new_pass": {"a brand new pw"}}); code != http.StatusFound || !strings.Contains(loc, "password_changed") {
		t.Fatalf("password change: %d %s", code, loc)
	}
	assertCredentialsRevoked(t, srv, uid, "forum account password change")
	if code := basicStatus(t, base, "member", plain); code == http.StatusOK {
		t.Errorf("application password survived the forum password change (%d)", code)
	}
}
