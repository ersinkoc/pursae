package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"wpgo/internal/caps"
	"wpgo/internal/config"
	"wpgo/internal/plugins"
	"wpgo/internal/store"
)

// fscCall calls the REST API as admin with an Application Password.
func fscCall(t *testing.T, base, pass, method, route string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+"/wp-json"+route, rd)
	req.SetBasicAuth("admin", pass)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// fscRequiredScope is data.required_scope of a REST error.
func fscRequiredScope(out map[string]any) string {
	d, _ := out["data"].(map[string]any)
	return strAny(d["required_scope"])
}

// O6: unfiltered_html belongs to the Appearance scope only, so a credential with Content (alone, or
// the OAuth default Content and Media) is filtered like a user without unfiltered_html.
func TestScopedCredentialCapsNoUnfilteredHTML(t *testing.T) {
	admin := &store.User{ID: 1, Caps: caps.Grants{"read": true, "edit_posts": true, "unfiltered_html": true, "edit_theme_options": true,
		"edit_users": true, "promote_users": true, "create_users": true, "list_users": true}}
	for _, scopes := range [][]string{{"content"}, oauthDefaultScopes, {"content", "media", "seo", "settings", "commerce", "cache"}, {"users"}, {"plugins"}} {
		if scopedUser(admin, scopes).Can("unfiltered_html", nil) {
			t.Errorf("scopes %v keep unfiltered_html", scopes)
		}
	}
	if !scopedUser(admin, []string{"content", "themes"}).Can("unfiltered_html", nil) {
		t.Error("content + themes lost unfiltered_html")
	}
	if !scopedUser(admin, nil).Can("unfiltered_html", nil) {
		t.Error("full access lost unfiltered_html")
	}
	if scopedUser(admin, []string{"users"}).Can("promote_users", nil) {
		t.Error("the users scope keeps promote_users (roles need full access)")
	}
	for slug, want := range map[string]bool{"themes": true, "plugins": true, "users": true, "content": false, "media": false, "settings": false} {
		if got := appScopeBySlug(slug).FullEquivalent(); got != want {
			t.Errorf("%s FullEquivalent = %v, want %v", slug, got, want)
		}
	}

	self, other := &store.User{ID: 1}, &store.User{ID: 2}
	for _, c := range []struct {
		scopes        []string
		target        *store.User
		field, expect string
	}{
		{nil, other, "password", ""},
		{[]string{"content"}, self, "email", "users"},
		{[]string{"content", "users"}, self, "email", ""},
		{[]string{"content", "users"}, self, "password", ""},
		{[]string{"content", "users"}, self, "roles", appScopeFullAccess},
		{[]string{"content", "users"}, other, "password", appScopeFullAccess},
		{[]string{"content", "users"}, other, "email", appScopeFullAccess},
		{[]string{"users"}, other, "roles", appScopeFullAccess},
		{[]string{"users"}, nil, "email", appScopeFullAccess},
	} {
		if got := appScopeCredentialNeed(c.scopes, self, c.target, c.field); got != c.expect {
			t.Errorf("appScopeCredentialNeed(%v, target %v, %s) = %q, want %q", c.scopes, c.target, c.field, got, c.expect)
		}
	}
}

// O6 end to end: a content-scoped Application Password on an administrator's account cannot store a
// script in a post (REST and MCP), while Content + Appearance and full access still can.
func TestScopedCredentialPostsAreKSESFiltered(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	base := ts.URL
	ctx := context.Background()
	payload := `<p>Hello</p><script>alert(1)</script><img src="x" onerror="alert(2)">`
	stored := func(id float64) string {
		var content string
		if err := srv.store.Pool.QueryRow(ctx, `SELECT content FROM posts WHERE id = $1`, int64(id)).Scan(&content); err != nil {
			t.Fatal(err)
		}
		return content
	}
	for _, tc := range []struct {
		name   string
		scopes []string
		raw    bool
	}{
		{"content", []string{"content"}, false},
		{"oauth default", oauthDefaultScopes, false},
		{"content and seo", []string{"content", "seo"}, false},
		{"content and themes", []string{"content", "themes"}, true},
		{"full access", nil, true},
	} {
		pass, call := mcpTestAgent(t, base, c, "O6 "+tc.name, tc.scopes)
		code, out := fscCall(t, base, pass, http.MethodPost, "/wp/v2/posts", map[string]any{"title": "O6 " + tc.name, "status": "draft", "content": payload})
		if code != http.StatusCreated {
			t.Fatalf("%s: create post: %d %v", tc.name, code, out)
		}
		id, _ := out["id"].(float64)
		got := stored(id)
		if hasScript := strings.Contains(got, "<script") || strings.Contains(got, "onerror"); hasScript != tc.raw {
			t.Errorf("%s: stored %q (raw HTML kept: %v, want %v)", tc.name, got, hasScript, tc.raw)
		}
		if !strings.Contains(got, "<p>Hello</p>") {
			t.Errorf("%s: the allowed markup was lost: %q", tc.name, got)
		}
		// The same through MCP (create_post runs as a REST request with the same credential).
		title := "O6 MCP " + tc.name
		if r := call("create_post", map[string]any{"title": title, "status": "draft", "content": payload}); r["isError"] == true {
			t.Fatalf("%s: MCP create_post: %s", tc.name, mcpText(r))
		}
		var mcpContent string
		if err := srv.store.Pool.QueryRow(ctx, `SELECT content FROM posts WHERE title = $1`, title).Scan(&mcpContent); err != nil {
			t.Fatal(err)
		}
		if hasScript := strings.Contains(mcpContent, "<script") || strings.Contains(mcpContent, "onerror"); hasScript != tc.raw {
			t.Errorf("%s: MCP stored %q (raw HTML kept: %v, want %v)", tc.name, mcpContent, hasScript, tc.raw)
		}
		// Updating an existing post is filtered the same way.
		if !tc.raw {
			code, out := fscCall(t, base, pass, http.MethodPost, "/wp/v2/posts/"+strconv.FormatInt(int64(id), 10), map[string]any{"content": `<p>x</p><script>alert(3)</script>`})
			if code != http.StatusOK || strings.Contains(stored(id), "<script") {
				t.Errorf("%s: update kept a script: %d %v %q", tc.name, code, out, stored(id))
			}
		}
	}
}

// O6, users scope: the account's own email and password with the Users scope (as documented), but
// roles and another user's email and password need full access, and so does creating a user with a
// role.
func TestScopedCredentialUsersScope(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	base := ts.URL
	ctx := context.Background()
	full, _ := mcpTestAgent(t, base, c, "Full", nil)
	users, _ := mcpTestAgent(t, base, c, "Users", []string{"content", "users"})

	code, out := fscCall(t, base, full, http.MethodPost, "/wp/v2/users", map[string]any{"username": "victim", "email": "victim@example.test",
		"password": "Victim-pass-0123456789!", "roles": []string{"administrator"}})
	if code != http.StatusCreated {
		t.Fatalf("create victim: %d %v", code, out)
	}
	victim := "/wp/v2/users/" + strconv.FormatInt(int64(out["id"].(float64)), 10)

	for _, body := range []map[string]any{
		{"password": "Taken-over-0123456789!"},
		{"email": "attacker@example.test"},
		{"roles": []string{"subscriber"}},
	} {
		if code, out := fscCall(t, base, users, http.MethodPost, victim, body); code != http.StatusForbidden || out["code"] != "rest_scope_forbidden" || fscRequiredScope(out) != appScopeFullAccess {
			t.Errorf("users scope on another user %v: %d %v", body, code, out)
		}
	}
	var email string
	_ = srv.store.Pool.QueryRow(ctx, `SELECT email FROM users WHERE login = 'victim'`).Scan(&email)
	if email != "victim@example.test" {
		t.Errorf("victim's email changed to %q", email)
	}
	// Profile basics of other users stay editable with the scope (edit_users).
	if code, out := fscCall(t, base, users, http.MethodPost, victim, map[string]any{"name": "Victim Renamed"}); code != http.StatusOK || out["name"] != "Victim Renamed" {
		t.Errorf("users scope, another user's name: %d %v", code, out)
	}
	// Own roles: full access only.
	if code, out := fscCall(t, base, users, http.MethodPost, "/wp/v2/users/me", map[string]any{"roles": []string{"administrator"}}); code != http.StatusForbidden || fscRequiredScope(out) != appScopeFullAccess {
		t.Errorf("users scope, own roles: %d %v", code, out)
	}
	// Own email: the Users scope (documented behaviour).
	if code, out := fscCall(t, base, users, http.MethodPost, "/wp/v2/users/me", map[string]any{"email": "admin-new@example.test"}); code != http.StatusOK || out["email"] != "admin-new@example.test" {
		t.Errorf("users scope, own email: %d %v", code, out)
	}
	// Creating an administrator would hand out full access; a user with the default role is fine.
	if code, out := fscCall(t, base, users, http.MethodPost, "/wp/v2/users", map[string]any{"username": "minted", "email": "minted@example.test",
		"password": "Minted-pass-0123456789!", "roles": []string{"administrator"}}); code != http.StatusForbidden {
		t.Errorf("users scope created an administrator: %d %v", code, out)
	}
	code, out = fscCall(t, base, users, http.MethodPost, "/wp/v2/users", map[string]any{"username": "member", "email": "member@example.test",
		"password": "Member-pass-0123456789!"})
	if code != http.StatusCreated {
		t.Errorf("users scope, user with the default role: %d %v", code, out)
	} else if roles, _ := out["roles"].([]any); len(roles) != 1 || roles[0] != "subscriber" {
		t.Errorf("default role: %v", out["roles"])
	}
	// Full access still may.
	if code, out := fscCall(t, base, full, http.MethodPost, victim, map[string]any{"email": "victim2@example.test"}); code != http.StatusOK || out["email"] != "victim2@example.test" {
		t.Errorf("full access, another user's email: %d %v", code, out)
	}
}

// The scope picker marks Appearance, Plugins and Users as equivalent to full access.
func TestScopePickerMarksFullEquivalentScopes(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	_, body, _ := get(t, c, ts.URL+"/wp-admin/profile")
	rows := regexp.MustCompile(`(?s)<tr[^>]*>\s*<th scope="row" class="check-column"><input type="checkbox" id="scope-[^"]*" name="[^"]*" value="([a-z]+)".*?</tr>`).FindAllStringSubmatch(body, -1)
	if len(rows) < len(appScopes) {
		t.Fatalf("scope picker rows: %d", len(rows))
	}
	for _, r := range rows {
		marked := strings.Contains(r[0], "Equivalent to full access")
		if want := appScopeFull[r[1]]; marked != want {
			t.Errorf("scope %s marked %v, want %v", r[1], marked, want)
		}
	}
}

// fscDangerousDemo is the demo plugin with content:html and admin:scripts added to its manifest.
func fscDangerousDemo(t *testing.T) []byte {
	t.Helper()
	data := demoPackage(t)
	pkg, err := plugins.LoadPackage(bytes.NewReader(data), int64(len(data)), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	manifest := bytes.Replace(pkg.ManifestJSON, []byte(`"permissions": [`), []byte(`"permissions": ["content:html", "admin:scripts", `), 1)
	if bytes.Equal(manifest, pkg.ManifestJSON) {
		t.Fatal("manifest not changed")
	}
	extras := map[string][]byte{}
	for k, v := range pkg.Assets {
		extras["assets/"+strings.TrimPrefix(k, "assets/")] = v
	}
	var buf bytes.Buffer
	if err := plugins.WritePackage(&buf, manifest, pkg.Wasm, extras, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func fscGranted(t *testing.T, srv *Server, slug string) []string {
	t.Helper()
	var perms []string
	_ = srv.store.Pool.QueryRow(context.Background(), `SELECT coalesce(array(SELECT jsonb_array_elements_text(permissions)), '{}') FROM plugins
		WHERE slug = $1 AND jsonb_typeof(permissions) = 'array'`, slug).Scan(&perms)
	return perms
}

// S1: activating a plugin through REST, MCP or the CLI without naming permissions grants the
// declared ones except content:html and admin:scripts; those are granted only when named.
func TestPluginActivationLeavesDangerousPermissionsUnlessNamed(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	base := ts.URL
	ctx := context.Background()
	srv.cfg.AllowUnsignedPlugins = true
	if err := srv.settings.Set(ctx, map[string]any{"plugin_allow_unsigned": true}); err != nil {
		t.Fatal(err)
	}
	zip := fscDangerousDemo(t)
	m, err := srv.installPackage(ctx, zip)
	if err != nil {
		t.Fatal(err)
	}
	slug := m.Slug
	check := func(path string, wantDanger []string) {
		t.Helper()
		got := fscGranted(t, srv, slug)
		if !slices.Contains(got, "options:read") || !slices.Contains(got, "storage") {
			t.Errorf("%s: safe permissions not granted: %v", path, got)
		}
		for _, d := range []string{"content:html", "admin:scripts"} {
			if slices.Contains(got, d) != slices.Contains(wantDanger, d) {
				t.Errorf("%s: granted %v, dangerous wanted %v", path, got, wantDanger)
			}
		}
	}
	full, _ := mcpTestAgent(t, base, c, "Plugins full", nil)
	deactivate := func() {
		t.Helper()
		if code, out := fscCall(t, base, full, http.MethodPost, "/pursae/v1/plugins/"+slug+"/deactivate", map[string]any{}); code != http.StatusOK {
			t.Fatalf("deactivate: %d %v", code, out)
		}
	}

	// REST without permissions, then with them named.
	if code, out := fscCall(t, base, full, http.MethodPost, "/pursae/v1/plugins/"+slug+"/activate", map[string]any{}); code != http.StatusOK || out["active"] != true {
		t.Fatalf("REST activate: %d %v", code, out)
	}
	check("REST default", nil)
	deactivate()
	if code, out := fscCall(t, base, full, http.MethodPost, "/pursae/v1/plugins/"+slug+"/activate", map[string]any{"permissions": []string{"options:read", "storage", "content:html"}}); code != http.StatusOK {
		t.Fatalf("REST activate with permissions: %d %v", code, out)
	}
	check("REST named", []string{"content:html"})
	deactivate()

	// MCP with a plugins-scoped password.
	_, call := mcpTestAgent(t, base, c, "Plugins agent", []string{"plugins"})
	if r := call("activate_plugin", map[string]any{"slug": slug}); r["isError"] == true {
		t.Fatalf("MCP activate: %s", mcpText(r))
	}
	check("MCP default", nil)
	if r := call("deactivate_plugin", map[string]any{"slug": slug}); r["isError"] == true {
		t.Fatalf("MCP deactivate: %s", mcpText(r))
	}
	if r := call("activate_plugin", map[string]any{"slug": slug, "permissions": []string{"options:read", "storage", "admin:scripts"}}); r["isError"] == true {
		t.Fatalf("MCP activate with permissions: %s", mcpText(r))
	}
	check("MCP named", []string{"admin:scripts"})
	deactivate()

	// CLI: the default and --grant=all leave them out; naming one grants it.
	cfg := srv.cfg
	cfg.DatabaseURL = config.Env("TEST_DB")
	run := func(args ...string) (int, string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := CLI(ctx, cfg, "pursae", args, strings.NewReader(""), &out, &errb)
		return code, out.String() + errb.String()
	}
	if code, out := run("plugin", "activate", slug); code != 0 {
		t.Fatalf("CLI activate: %d %s", code, out)
	} else if !strings.Contains(out, "content:html") || !strings.Contains(out, "not granted") {
		t.Errorf("CLI activate does not warn about the dangerous permissions it left out: %s", out)
	}
	check("CLI default", nil)
	if code, out := run("plugin", "deactivate", slug); code != 0 {
		t.Fatalf("CLI deactivate: %d %s", code, out)
	}
	if code, out := run("plugin", "activate", slug, "--grant=all"); code != 0 {
		t.Fatalf("CLI activate --grant=all: %d %s", code, out)
	}
	check("CLI all", nil)
	if code, out := run("plugin", "deactivate", slug); code != 0 {
		t.Fatalf("CLI deactivate: %d %s", code, out)
	}
	if code, out := run("plugin", "activate", slug, "--grant=all,content:html,admin:scripts"); code != 0 {
		t.Fatalf("CLI activate --grant=all,content:html,admin:scripts: %d %s", code, out)
	}
	check("CLI named", []string{"content:html", "admin:scripts"})
	if code, out := run("plugin", "deactivate", slug); code != 0 {
		t.Fatalf("CLI deactivate: %d %s", code, out)
	}

	// install --activate shares the default.
	if code, out := run("plugin", "delete", slug, "--yes"); code != 0 {
		t.Fatalf("CLI delete: %d %s", code, out)
	}
	file := filepath.Join(t.TempDir(), "dangerous-demo.zip")
	if err := os.WriteFile(file, zip, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := run("plugin", "install", file, "--activate"); code != 0 {
		t.Fatalf("CLI install --activate: %d %s", code, out)
	}
	check("CLI install --activate", nil)
}

// The CLI's decision: dangerous permissions only when named, "all" means the safe ones.
func TestGrantedPermissionsDangerousOnlyWhenNamed(t *testing.T) {
	declared := []permRow{{Name: "options:read"}, {Name: "content:html", Danger: true}, {Name: "admin:scripts"}, {Name: "storage"}}
	for _, c := range []struct {
		grant []string
		given bool
		want  string
	}{
		{nil, false, "options:read,storage"},
		{[]string{"all"}, true, "options:read,storage"},
		{[]string{"none"}, true, ""},
		{[]string{"admin:scripts"}, true, "admin:scripts"},
		{[]string{"all", "content:html"}, true, "options:read,storage,content:html"},
		{[]string{"content:html", "all", "storage"}, true, "content:html,options:read,storage"},
	} {
		got, err := grantedPermissions(declared, c.grant, c.given)
		if err != nil || strings.Join(got, ",") != c.want {
			t.Errorf("grantedPermissions(%v, %v) = %v %v, want %s", c.grant, c.given, got, err, c.want)
		}
	}
	if got := safePluginPermissions(declared); strings.Join(got, ",") != "options:read,storage" {
		t.Errorf("safePluginPermissions = %v", got)
	}
}
