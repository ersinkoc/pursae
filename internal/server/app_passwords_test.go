package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestAppPasswordHelpers(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p := generateAppPassword()
		if !regexp.MustCompile(`^[A-Za-z0-9]{24}$`).MatchString(p) || seen[p] {
			t.Fatalf("generateAppPassword() = %q", p)
		}
		seen[p] = true
	}
	if got := chunkAppPassword("abcdefghijklmnopqrstuvwx"); got != "abcd efgh ijkl mnop qrst uvwx" {
		t.Errorf("chunkAppPassword = %q", got)
	}
	if got := chunkAppPassword("abcdef"); got != "abcd ef" {
		t.Errorf("chunkAppPassword short = %q", got)
	}
	for i := 0; i < 20; i++ {
		if u := newUUID(); !isUUID(u) || u[14] != '4' {
			t.Fatalf("newUUID() = %q", u)
		}
	}
	for v, want := range map[string]bool{"6F9619FF-8B86-4011-B42D-00CF4FC964FF": true, "6f9619ff-8b86-d011-b42d-00cf4fc964ff": false,
		"not-a-uuid": false, "": false, "6f9619ff8b864011b42d00cf4fc964ff": false} {
		if isUUID(v) != want {
			t.Errorf("isUUID(%q) = %v", v, !want)
		}
	}
	for raw, ok := range map[string]bool{"": true, "https://app.example/cb": true, "https://127.0.0.1/cb": true,
		"http://localhost:3000/cb": true, "http://127.0.0.1/cb": true, "http://127.0.0.1:9/cb": true,
		"myapp://callback": true, "cursor://cb": true, "vbscript:x": false, "file:///tmp/x": false, "http://example.com/cb": false,
		"http://app.local/cb": false, "https://app.local/cb": false, "https://printer.test/cb": false,
		"http://10.0.0.5/cb": false, "https://192.168.1.5/cb": false, "http://169.254.169.254/latest": false,
		"http://foo.localhost/cb": false, "http://[::1]/cb": false, "javascript:alert(1)": false,
		"data:text/html,x": false, "/relative": false, "JavaScript:alert(1)": false} {
		if err := validAuthorizeRedirect(raw); (err == nil) != ok {
			t.Errorf("validAuthorizeRedirect(%q) = %v", raw, err)
		}
	}
	for in, want := range map[string]string{"content,SEO": "content,seo", " media , nope": "media", "": ""} {
		if got := strings.Join(normalizeAppScopes(strings.Split(in, ",")), ","); got != want {
			t.Errorf("normalizeAppScopes(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAppPasswordLifecycle covers the application-passwords REST controller (create, list, read,
// update, introspect, revoke one, revoke all) and scope enforcement on routes outside a password's
// scopes. /wp/v2/users/me and routes no scope covers are left to their own tests.
func TestAppPasswordLifecycle(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	anon := &http.Client{}
	type resp struct {
		code int
		obj  map[string]any
		list []any
	}
	do := func(client *http.Client, user, pass, method, path string, payload any) resp {
		t.Helper()
		var rd io.Reader
		if payload != nil {
			b, _ := json.Marshal(payload)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+"/wp-json"+path, rd)
		if user != "" {
			req.SetBasicAuth(user, pass)
		} else {
			req.Header.Set("X-WP-Nonce", nonce)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		r, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		raw, _ := io.ReadAll(r.Body)
		var out any
		_ = json.Unmarshal(raw, &out)
		o, _ := out.(map[string]any)
		l, _ := out.([]any)
		return resp{r.StatusCode, o, l}
	}
	cookie := func(method, path string, payload any) resp { return do(c, "", "", method, path, payload) }
	const coll = "/wp/v2/users/me/application-passwords"

	// Create: the plain password is shown once (24 characters, unchunked like WordPress' REST API); names
	// are unique; empty names are refused.
	full := cookie("POST", coll, map[string]any{"name": "Deploy bot", "app_id": "8a6f2c1e-0b9d-4b7e-9d43-1f2e3a4b5c6d", "full_access": true})
	if full.code != http.StatusCreated || !regexp.MustCompile(`^[A-Za-z0-9]{24}$`).MatchString(strAny(full.obj["password"])) || joinAny(full.obj["scopes"]) != "full_access" {
		t.Fatalf("create: %d %v", full.code, full.obj)
	}
	if r := cookie("POST", coll, map[string]any{"name": "Deploy bot"}); r.code != http.StatusBadRequest || r.obj["code"] != "application_password_duplicate_name" {
		t.Errorf("duplicate name: %d %v", r.code, r.obj)
	}
	if r := cookie("POST", coll, map[string]any{"name": "  "}); r.code != http.StatusBadRequest {
		t.Errorf("empty name: %d %v", r.code, r.obj)
	}
	if r := cookie("POST", coll, map[string]any{}); r.code != http.StatusBadRequest || r.obj["code"] != "rest_missing_callback_param" {
		t.Errorf("missing name: %d %v", r.code, r.obj)
	}
	scoped := cookie("POST", coll, map[string]any{"name": "Writer", "scopes": "content, media"})
	if scoped.code != http.StatusCreated || joinAny(scoped.obj["scopes"]) != "content,media" {
		t.Fatalf("create scoped (comma list): %d %v", scoped.code, scoped.obj)
	}
	fullPass, scopedPass := strAny(full.obj["password"]), strAny(scoped.obj["password"])
	fullUUID, scopedUUID := strAny(full.obj["uuid"]), strAny(scoped.obj["uuid"])

	// List and read never show the password.
	l := cookie("GET", coll, nil)
	if l.code != http.StatusOK || len(l.list) != 2 {
		t.Fatalf("list: %d %v", l.code, l.list)
	}
	for _, it := range l.list {
		if _, has := it.(map[string]any)["password"]; has {
			t.Error("the list shows a password")
		}
	}
	if r := cookie("GET", coll+"/"+scopedUUID, nil); r.code != http.StatusOK || r.obj["name"] != "Writer" || r.obj["password"] != nil {
		t.Errorf("read: %d %v", r.code, r.obj)
	}
	if r := cookie("GET", coll+"/"+newUUID(), nil); r.code != http.StatusNotFound {
		t.Errorf("unknown uuid: %d", r.code)
	}

	// Update: rename and narrow the scopes; an unknown scope is refused and changes nothing.
	if r := cookie("POST", coll+"/"+scopedUUID, map[string]any{"scopes": []string{"content", "root"}}); r.code != http.StatusBadRequest {
		t.Errorf("unknown scope on update: %d", r.code)
	}
	if r := cookie("POST", coll+"/"+scopedUUID, map[string]any{"name": "Content writer", "scopes": []string{"content"}}); r.code != http.StatusOK ||
		r.obj["name"] != "Content writer" || joinAny(r.obj["scopes"]) != "content" {
		t.Fatalf("update: %d %v", r.code, r.obj)
	}

	// Basic auth with the passwords; the chunked form (spaces) works too, like WordPress.
	if r := do(anon, "admin", fullPass, "GET", "/wp/v2/settings", nil); r.code != http.StatusOK {
		t.Errorf("full password on settings: %d", r.code)
	}
	if r := do(anon, "admin", chunkAppPassword(fullPass), "GET", "/wp/v2/posts?status=publish", nil); r.code != http.StatusOK {
		t.Errorf("full password with spaces: %d", r.code)
	}
	if r := do(anon, "admin", "abcd abcd abcd abcd abcd abcd", "GET", "/wp/v2/posts", nil); r.code != http.StatusUnauthorized {
		t.Errorf("wrong application password: %d", r.code)
	}
	if r := do(anon, "admin", "correct horse", "GET", "/wp/v2/settings", nil); r.code != http.StatusUnauthorized {
		t.Errorf("the account password over Basic auth: %d", r.code)
	}

	// Introspect: the password that authenticated the request; not available to a cookie session.
	if r := do(anon, "admin", fullPass, "GET", coll+"/introspect", nil); r.code != http.StatusOK || r.obj["uuid"] != fullUUID || r.obj["password"] != nil {
		t.Errorf("introspect: %d %v", r.code, r.obj)
	}
	if r := cookie("GET", coll+"/introspect", nil); r.code != http.StatusNotFound || r.obj["code"] != "rest_no_authenticated_app_password" {
		t.Errorf("introspect with a cookie: %d %v", r.code, r.obj)
	}
	if r := do(anon, "admin", fullPass, "GET", coll+"/"+fullUUID, nil); r.code != http.StatusOK || r.obj["last_used"] == nil {
		t.Errorf("last_used not recorded: %d %v", r.code, r.obj)
	}

	// The content-only password: content routes work, every other scope's routes are refused
	// (media was removed by the update above).
	limited := func(method, path string, payload any) resp {
		return do(anon, "admin", scopedPass, method, path, payload)
	}
	if r := limited("GET", "/wp/v2/posts", nil); r.code != http.StatusOK {
		t.Errorf("scoped posts: %d", r.code)
	}
	if r := limited("GET", "/wp/v2/categories", nil); r.code != http.StatusOK {
		t.Errorf("scoped categories: %d", r.code)
	}
	for _, path := range []string{"/wp/v2/media", "/wp/v2/users", "/wp/v2/users/1", "/wp/v2/settings", "/wp/v2/themes", "/wp/v2/menus",
		"/pursae/v1/themes", "/pursae/v1/plugins", "/pursae/v1/cache", "/pursae/v1/options/smtp", coll, coll + "/introspect"} {
		if r := limited("GET", path, nil); r.code != http.StatusForbidden {
			t.Errorf("scoped GET %s: %d %v", path, r.code, r.obj)
		} else if path != coll && path != coll+"/introspect" && r.obj["code"] != "rest_scope_forbidden" {
			t.Errorf("scoped GET %s: code %v", path, r.obj["code"])
		}
	}
	if r := limited("POST", "/wp/v2/settings", map[string]any{"title": "Hijacked"}); r.code != http.StatusForbidden {
		t.Errorf("scoped settings update: %d", r.code)
	}
	if r := limited("DELETE", coll+"/"+fullUUID, nil); r.code != http.StatusForbidden {
		t.Errorf("scoped password revoking another: %d", r.code)
	}

	// Omitting scopes is content and media, not the user's full REST access.
	bare := cookie("POST", coll, map[string]any{"name": "Bare"})
	if bare.code != http.StatusCreated || joinAny(bare.obj["scopes"]) != "content,media" {
		t.Fatalf("omitted scopes: %d %v", bare.code, bare.obj)
	}
	if r := do(anon, "admin", strAny(bare.obj["password"]), "GET", "/wp/v2/settings", nil); r.code != http.StatusForbidden {
		t.Errorf("password with no scopes on settings: %d", r.code)
	}
	if r := do(anon, "admin", strAny(bare.obj["password"]), "GET", "/wp/v2/posts", nil); r.code != http.StatusOK {
		t.Errorf("password with no scopes on posts: %d", r.code)
	}
	if r := cookie("DELETE", coll+"/"+strAny(bare.obj["uuid"]), nil); r.code != http.StatusOK {
		t.Fatalf("delete bare: %d %v", r.code, r.obj)
	}

	// Revoke one: that password stops working, the other keeps working.
	if r := cookie("DELETE", coll+"/"+scopedUUID, nil); r.code != http.StatusOK || r.obj["deleted"] != true {
		t.Fatalf("revoke: %d %v", r.code, r.obj)
	}
	if r := limited("GET", "/wp/v2/posts", nil); r.code != http.StatusUnauthorized {
		t.Errorf("revoked password still works: %d", r.code)
	}
	if r := do(anon, "admin", fullPass, "GET", "/wp/v2/posts", nil); r.code != http.StatusOK {
		t.Errorf("the other password after a revoke: %d", r.code)
	}
	// Revoke all.
	if r := cookie("DELETE", coll, nil); r.code != http.StatusOK || r.obj["count"] != 1.0 {
		t.Fatalf("revoke all: %d %v", r.code, r.obj)
	}
	if r := do(anon, "admin", fullPass, "GET", "/wp/v2/posts", nil); r.code != http.StatusUnauthorized {
		t.Errorf("password after revoke all: %d", r.code)
	}
	if r := cookie("GET", coll, nil); r.code != http.StatusOK || len(r.list) != 0 {
		t.Errorf("list after revoke all: %d %v", r.code, r.list)
	}

	// Another user's passwords need edit_user.
	if r := cookie("POST", "/wp/v2/users", map[string]any{"username": "writer", "email": "writer@example.test", "password": "another horse", "roles": []string{"author"}}); r.code != http.StatusCreated {
		t.Fatalf("create author: %d %v", r.code, r.obj)
	}
	jar, _ := cookiejar.New(nil)
	ac := &http.Client{Jar: jar, CheckRedirect: c.CheckRedirect}
	if code, _, _ := post(t, ac, base+"/wp-login", url.Values{"log": {"writer"}, "pwd": {"another horse"}}); code != http.StatusFound {
		t.Fatalf("author login: %d", code)
	}
	_, anonce, _ := get(t, ac, base+"/wp-admin/admin-ajax?action=rest-nonce")
	req, _ := http.NewRequest("GET", base+"/wp-json/wp/v2/users/1/application-passwords", nil)
	req.Header.Set("X-WP-Nonce", anonce)
	r, err := ac.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("an author listing the administrator's passwords: %d", r.StatusCode)
	}
}

func joinAny(v any) string {
	l, _ := v.([]any)
	var out []string
	for _, x := range l {
		out = append(out, strAny(x))
	}
	return strings.Join(out, ",")
}
