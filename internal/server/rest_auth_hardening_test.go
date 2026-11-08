package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

func TestBatchReservedHeader(t *testing.T) {
	for _, h := range []string{"Authorization", "cookie", "X-Forwarded-For", "x-forwarded-proto", "X-Real-IP", "Forwarded", "Host", " X-Forwarded-Host "} {
		if !batchReservedHeader(h) {
			t.Errorf("%q accepted from a batch sub-request", h)
		}
	}
	for _, h := range []string{"Content-Type", "X-WP-Nonce", "If-Match", "X-HTTP-Method-Override"} {
		if batchReservedHeader(h) {
			t.Errorf("%q refused", h)
		}
	}
}

// TestRESTAuthHardening covers the review fixes of REST authentication: scoped passwords and /users/me,
// fail-closed scopes, sticky permission, file-edit lock in saveThemeFile, the post-password cookie and the
// Basic-auth throttle (last: it throttles the test's address).
func TestRESTAuthHardening(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	if code, _, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Auth Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK {
		t.Fatalf("install: %d", code)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	do := func(client *http.Client, auth [2]string, method, path string, payload any) (int, map[string]any) {
		t.Helper()
		var rd io.Reader
		if payload != nil {
			b, _ := json.Marshal(payload)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+"/wp-json"+path, rd)
		if auth[0] != "" {
			req.SetBasicAuth(auth[0], auth[1])
		} else {
			req.Header.Set("X-WP-Nonce", nonce)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	newPass := func(path, name string, scopes []string) [2]string {
		t.Helper()
		code, body := do(c, [2]string{}, "POST", path, map[string]any{"name": name, "scopes": scopes})
		if code != http.StatusCreated {
			t.Fatalf("create password %s: %d %v", name, code, body)
		}
		return [2]string{"", body["password"].(string)}
	}
	full := newPass("/wp/v2/users/me/application-passwords", "Full", nil)
	full[0] = "admin"
	content := newPass("/wp/v2/users/me/application-passwords", "Content", []string{"content"})
	content[0] = "admin"
	users := newPass("/wp/v2/users/me/application-passwords", "Users", []string{"content", "users"})
	users[0] = "admin"

	// 1. A scoped password without "users" cannot change the account's credentials through /users/me.
	for _, body := range []map[string]any{
		{"email": "attacker@example.test"},
		{"password": "taken over"},
		{"roles": []string{"administrator"}},
	} {
		if code, out := do(anon, content, "POST", "/wp/v2/users/me", body); code != http.StatusForbidden || out["code"] != "rest_scope_forbidden" {
			t.Errorf("scoped /users/me %v: %d %v", body, code, out)
		}
	}
	if code, out := do(anon, content, "PUT", "/wp/v2/users/me", map[string]any{"name": "Agent Smith"}); code != http.StatusOK || out["name"] != "Agent Smith" {
		t.Errorf("scoped /users/me name: %d %v", code, out)
	}
	if code, _ := do(anon, content, "GET", "/wp/v2/users/me", nil); code != http.StatusOK {
		t.Errorf("scoped GET /users/me: %d", code)
	}
	if code, out := do(anon, users, "POST", "/wp/v2/users/me", map[string]any{"email": "admin2@example.test"}); code != http.StatusOK || out["email"] != "admin2@example.test" {
		t.Errorf("users-scoped /users/me email: %d %v", code, out)
	}
	if u, _ := srv.store.UserByLoginOrEmail(ctx, "admin"); u == nil || u.Email != "admin2@example.test" {
		t.Errorf("email not changed with the users scope: %+v", u)
	}

	// 2. Stored scopes that are all unknown refuse the password instead of granting everything.
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE app_passwords SET scopes = ARRAY['retired'] WHERE name = 'Content'`); err != nil {
		t.Fatal(err)
	}
	if code, _ := do(anon, content, "GET", "/wp/v2/users/me", nil); code != http.StatusUnauthorized {
		t.Errorf("password with only unknown scopes: %d, want 401", code)
	}
	if code, _ := do(anon, full, "GET", "/wp/v2/users/me", nil); code != http.StatusOK {
		t.Errorf("full password (and the match cache): %d", code)
	}
	if code, _ := do(anon, full, "GET", "/wp/v2/users/me", nil); code != http.StatusOK {
		t.Errorf("full password, second request: %d", code)
	}

	// 8. Making a post sticky needs edit_others_posts or publish_posts.
	code, contrib := do(anon, full, "POST", "/wp/v2/users", map[string]any{"username": "contrib", "email": "contrib@example.test", "password": "contrib pass 123", "roles": []string{"contributor"}})
	if code != http.StatusCreated {
		t.Fatalf("create contributor: %d %v", code, contrib)
	}
	cid := int64(contrib["id"].(float64))
	code, cp := do(c, [2]string{}, "POST", "/wp/v2/users/"+itoa64(cid)+"/application-passwords", map[string]any{"name": "Contrib"})
	if code != http.StatusCreated {
		t.Fatalf("contributor password: %d %v", code, cp)
	}
	ca := [2]string{"contrib", cp["password"].(string)}
	if code, out := do(anon, ca, "POST", "/wp/v2/posts", map[string]any{"title": "Mine", "sticky": true}); code != http.StatusForbidden || out["code"] != "rest_cannot_assign_sticky" {
		t.Errorf("contributor sticky: %d %v", code, out)
	}
	if code, out := do(anon, ca, "POST", "/wp/v2/posts", map[string]any{"title": "Mine"}); code != http.StatusCreated {
		t.Errorf("contributor draft: %d %v", code, out)
	}
	if code, out := do(anon, full, "POST", "/wp/v2/posts", map[string]any{"title": "Pinned", "sticky": true, "status": "publish"}); code != http.StatusCreated || out["sticky"] != true {
		t.Errorf("admin sticky: %d %v", code, out)
	}

	// 3. "Disable file editors" of Pursae Security is enforced by saveThemeFile / restoreThemeFile.
	gc, _ := gin.CreateTestContext(httptest.NewRecorder())
	gc.Request = httptest.NewRequest("POST", "/", nil)
	st := asDefaults()
	st.DisableFileEdit = true
	if err := srv.settings.Set(ctx, map[string]any{asSetting: true, asOption: st}); err != nil {
		t.Fatal(err)
	}
	theme := srv.activeTheme().Slug
	if err := srv.saveThemeFile(gc, theme, "style.css", "body{}"); err == nil || err.(*themeFileError).code != "disallowed" {
		t.Errorf("saveThemeFile with file editing disabled: %v", err)
	}
	if err := srv.restoreThemeFile(gc, theme, "style.css"); err == nil {
		t.Error("restoreThemeFile with file editing disabled succeeded")
	}
	if err := srv.settings.Set(ctx, map[string]any{asSetting: false}); err != nil {
		t.Fatal(err)
	}

	// 9. The post-password cookie does not depend on the time and stops matching when the password changes.
	p := &store.Post{Password: "open sesame"}
	pc, _ := gin.CreateTestContext(httptest.NewRecorder())
	pc.Request = httptest.NewRequest("GET", "/", nil)
	pc.Request.AddCookie(&http.Cookie{Name: postPassCookie, Value: srv.postPassValue("open sesame")})
	if srv.passwordRequired(pc, p) {
		t.Error("cookie for the right password not accepted")
	}
	if srv.postPassValue("open sesame") != srv.auth.Sign("postpass|open sesame") {
		t.Error("post-password cookie is not the tick-free signature")
	}
	p.Password = "changed"
	if !srv.passwordRequired(pc, p) {
		t.Error("cookie still accepted after the post password changed")
	}

	// 4. Basic auth counts toward the login throttle: after five failures the address gets 429 (the refused
	// password with unknown scopes above was the first).
	for i := 0; i < 4; i++ {
		user := "admin"
		if i%2 == 1 {
			user = "nobody"
		}
		if code, _ := do(anon, [2]string{user, "wrong wrong wrong"}, "GET", "/wp/v2/users/me", nil); code != http.StatusUnauthorized {
			t.Fatalf("wrong password %d: %d", i, code)
		}
	}
	if code, out := do(anon, [2]string{"admin", "wrong wrong wrong"}, "GET", "/wp/v2/users/me", nil); code != http.StatusTooManyRequests {
		t.Errorf("sixth failure: %d %v, want 429", code, out)
	}
}
