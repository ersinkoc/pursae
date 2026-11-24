package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/auth"
)

// Front-end members (forum participants, subscribers) get neither wp-admin nor Application Passwords;
// roles with real admin work (authors, forum moderators) keep both.
func TestFrontEndMembersNoAdminNoAppPasswords(t *testing.T) {
	s := secStart(t)
	ctx := context.Background()
	if err := s.srv.forumActivate(ctx); err != nil { // the forum roles
		t.Fatal(err)
	}
	const coll = "/wp/v2/users/me/application-passwords"

	for _, role := range []string{"forum_participant", "subscriber"} {
		c, rc := s.user("m_"+role, role)
		// wp-admin: a page view goes to the front end, a form post is refused.
		code, _, loc := get(t, c, s.base+"/wp-admin/profile")
		if code != http.StatusFound || strings.Contains(loc, "/wp-admin") {
			t.Errorf("%s GET /wp-admin/profile: %d → %q", role, code, loc)
		}
		if code, _, _ := get(t, c, s.base+"/wp-admin/"); code != http.StatusFound {
			t.Errorf("%s GET /wp-admin/: %d", role, code)
		}
		if code, _, _ := post(t, c, s.base+"/wp-admin/profile", url.Values{"do_new_application_password": {"1"}, "new_application_password_name": {"x"}, "full_access": {"1"}}); code != http.StatusForbidden {
			t.Errorf("%s POST /wp-admin/profile: %d", role, code)
		}
		if code, _, _ := get(t, c, s.base+"/wp-admin/authorize-application?app_name=x"); code != http.StatusFound {
			t.Errorf("%s authorize-application: %d", role, code)
		}
		// admin-ajax still serves the front end (the REST nonce came from it).
		if rc.nonce == "" || strings.Contains(rc.nonce, "<") {
			t.Errorf("%s admin-ajax rest-nonce: %q", role, rc.nonce)
		}
		// REST: no new Application Password.
		if code, out, _, _ := rc.do("POST", coll, map[string]any{"name": "Bot", "full_access": true}); code == http.StatusCreated {
			t.Errorf("%s created an application password: %v", role, out)
		}
		// One stored earlier does not sign in.
		u, err := s.srv.store.UserByLoginOrEmail(ctx, "m_"+role)
		if err != nil {
			t.Fatal(err)
		}
		hash, _ := auth.HashPassword("abcdefghijklmnopqrstuvwx")
		if _, err := s.srv.store.Pool.Exec(ctx, `INSERT INTO app_passwords(user_id, uuid, app_id, name, password_hash, scopes) VALUES ($1, $2::uuid, '', 'old', $3, '{full_access}')`, u.ID, newUUID(), hash); err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest("GET", s.base+"/wp-json/wp/v2/users/me", nil)
		req.SetBasicAuth("m_"+role, "abcdefghijklmnopqrstuvwx")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode == http.StatusOK {
			t.Errorf("%s signed in with a stored application password", role)
		}
		// Signing in never lands in wp-admin.
		lc := asNewClient()
		code, _, loc = post(t, lc, s.base+"/wp-login", url.Values{"log": {"m_" + role}, "pwd": {asStrongPass}, "redirect_to": {"/wp-admin/profile"}})
		if code != http.StatusFound || strings.Contains(loc, "/wp-admin") {
			t.Errorf("%s login: %d → %q", role, code, loc)
		}
	}

	for _, role := range []string{"author", "forum_moderator"} {
		c, rc := s.user("k_"+role, role)
		if code, _, loc := get(t, c, s.base+"/wp-admin/profile"); code != http.StatusOK {
			t.Errorf("%s GET /wp-admin/profile: %d → %q", role, code, loc)
		}
		if code, out, _, _ := rc.do("POST", coll, map[string]any{"name": "Bot"}); code != http.StatusCreated {
			t.Errorf("%s application password: %d %v", role, code, out)
		}
	}
}
