package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/caps"
	"wpgo/internal/store"
)

func TestAppScopeForRoute(t *testing.T) {
	for route, want := range map[string]string{
		"/wp/v2/posts":                                    "content",
		"/wp/v2/posts/12":                                 "content",
		"/wp/v2/postsx":                                   "",
		"/wp/v2/media/3":                                  "media",
		"/wp/v2/menu-items/4":                             "themes",
		"/pursae/v1/plugins/seo-go/activate":              "plugins",
		"/pursae/v1/cache/purge":                          "cache",
		"/pursae/v1/seo/settings":                         "seo",
		"/seogo/v1/meta/5":                                "seo",
		"/pursae/v1/options/smtp":                         "settings",
		"/wp/v2/users/me":                                 "*",
		"/wp/v2/users/7":                                  "users",
		"/wp/v2/users/me/application-passwords":           "",
		"/wp/v2/users/1/application-passwords/introspect": "",
		"/pursae/v1/mcp":                                  "*",
		"/myplugin/v1/thing":                              "",
		"/":                                               "*",
	} {
		if got := appScopeForRoute(route); got != want {
			t.Errorf("appScopeForRoute(%q) = %q, want %q", route, got, want)
		}
	}
}

func TestScopedUserNarrowsOnly(t *testing.T) {
	u := &store.User{ID: 2, Caps: caps.Grants{"read": true, "edit_posts": true, "manage_options": true, "activate_plugins": true}}
	if got := scopedUser(u, nil); got != u {
		t.Fatal("no scopes must keep the user")
	}
	cu := scopedUser(u, []string{"content"})
	if !cu.Can("edit_posts", nil) || cu.Can("manage_options", nil) || cu.Can("activate_plugins", nil) {
		t.Errorf("content scope caps = %v", cu.Caps)
	}
	if cu.Can("publish_pages", nil) { // in the scope but not a cap the user has: never widened
		t.Error("scope widened a capability")
	}
	if !u.Can("manage_options", nil) {
		t.Error("the original user was changed")
	}
	if got := normalizeAppScopes([]string{"SEO", "content", "nope", "content"}); strings.Join(got, ",") != "content,seo" {
		t.Errorf("normalizeAppScopes = %v", got)
	}
}

func TestMaskSecrets(t *testing.T) {
	v := map[string]any{"host": "smtp.example.test", "password": "hunter2", "api_key": "", "nested": map[string]any{"client_secret": "x", "port": 25.0},
		"list": []any{map[string]any{"token": "t"}}}
	out := maskSecrets(v, "").(map[string]any)
	if out["host"] != "smtp.example.test" || out["password"] != secretMask || out["api_key"] != "" {
		t.Errorf("masked = %v", out)
	}
	if out["nested"].(map[string]any)["client_secret"] != secretMask || out["list"].([]any)[0].(map[string]any)["token"] != secretMask {
		t.Errorf("nested masked = %v", out)
	}
	back := dropMasked(map[string]any{"password": secretMask, "host": "h", "nested": map[string]any{"client_secret": secretMask, "port": 26.0}}).(map[string]any)
	if _, ok := back["password"]; ok {
		t.Error("masked password kept in the patch")
	}
	if _, ok := back["nested"].(map[string]any)["client_secret"]; ok || back["host"] != "h" {
		t.Errorf("dropMasked = %v", back)
	}
}

// TestPursaeREST covers pursae/v1 and Application Password scopes end to end.
func TestPursaeREST(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	if code, _, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"MCP Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK {
		t.Fatalf("install: %d", code)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	do := func(client *http.Client, auth [2]string, method, path string, payload any) (int, map[string]any, []any) {
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
		raw, _ := io.ReadAll(resp.Body)
		var out any
		_ = json.Unmarshal(raw, &out)
		obj, _ := out.(map[string]any)
		list, _ := out.([]any)
		if obj == nil && list == nil && len(raw) > 0 {
			t.Logf("%s %s: %s", method, path, raw)
		}
		return resp.StatusCode, obj, list
	}
	cookie := func(method, path string, payload any) (int, map[string]any, []any) {
		return do(c, [2]string{}, method, path, payload)
	}
	// Application passwords: one with full access, one limited to content.
	code, full, _ := cookie("POST", "/wp/v2/users/me/application-passwords", map[string]any{"name": "Claude"})
	if code != http.StatusCreated || len(full["scopes"].([]any)) != 0 {
		t.Fatalf("create full password: %d %v", code, full)
	}
	code, scoped, _ := cookie("POST", "/wp/v2/users/me/application-passwords", map[string]any{"name": "Cursor", "scopes": []string{"content"}})
	if code != http.StatusCreated || len(scoped["scopes"].([]any)) != 1 {
		t.Fatalf("create scoped password: %d %v", code, scoped)
	}
	if code, body, _ := cookie("POST", "/wp/v2/users/me/application-passwords", map[string]any{"name": "Bad", "scopes": []string{"root"}}); code != http.StatusBadRequest {
		t.Errorf("unknown scope: %d %v", code, body)
	}
	anon := &http.Client{}
	fa := [2]string{"admin", full["password"].(string)}
	sa := [2]string{"admin", scoped["password"].(string)}
	api := func(method, path string, payload any) (int, map[string]any, []any) {
		return do(anon, fa, method, path, payload)
	}
	limited := func(method, path string, payload any) (int, map[string]any, []any) {
		return do(anon, sa, method, path, payload)
	}

	// Plugins.
	code, _, plugins := api("GET", "/pursae/v1/plugins", nil)
	if code != http.StatusOK || len(plugins) < 5 {
		t.Fatalf("plugins: %d %d", code, len(plugins))
	}
	if code, body, _ := api("POST", "/pursae/v1/plugins/classic-editor/activate", nil); code != http.StatusOK || body["active"] != true {
		t.Errorf("activate classic-editor: %d %v", code, body)
	}
	if code, body, _ := api("POST", "/pursae/v1/plugins/classic-editor/deactivate", nil); code != http.StatusOK || body["active"] != false {
		t.Errorf("deactivate classic-editor: %d %v", code, body)
	}
	if code, _, _ := api("DELETE", "/pursae/v1/plugins/classic-editor", nil); code != http.StatusBadRequest {
		t.Errorf("deleting a built-in plugin: %d", code)
	}
	if code, _, _ := api("POST", "/pursae/v1/plugins/no-such/activate", nil); code != http.StatusNotFound {
		t.Errorf("unknown plugin: %d", code)
	}

	// Themes and theme files.
	code, _, themes := api("GET", "/pursae/v1/themes", nil)
	if code != http.StatusOK || len(themes) == 0 {
		t.Fatalf("themes: %d %v", code, themes)
	}
	var active string
	for _, th := range themes {
		if m := th.(map[string]any); m["active"] == true {
			active = m["slug"].(string)
		}
	}
	code, files, _ := api("GET", "/pursae/v1/themes/"+active+"/files", nil)
	if code != http.StatusOK || len(files["files"].([]any)) == 0 {
		t.Fatalf("theme files: %d %v", code, files)
	}
	first := files["files"].([]any)[0].(map[string]any)["file"].(string)
	if code, body, _ := api("GET", "/pursae/v1/themes/"+active+"/file?file="+url.QueryEscape(first), nil); code != http.StatusOK || body["content"] == nil {
		t.Errorf("read %s: %d %v", first, code, body)
	}
	if code, _, _ := api("GET", "/pursae/v1/themes/"+active+"/file?file=../../../etc/passwd", nil); code != http.StatusNotFound && code != http.StatusForbidden { // 403: the core firewall
		t.Errorf("path traversal: %d", code)
	}
	if code, body, _ := api("POST", "/pursae/v1/themes/"+active+"/activate", nil); code != http.StatusOK || body["active"] != true {
		t.Errorf("activate theme: %d %v", code, body)
	}

	// Cache.
	if code, body, _ := api("POST", "/pursae/v1/cache/settings", map[string]any{"wpgo_cache_ttl_public": 600, "wpgo_cache_mobile": true}); code != http.StatusOK ||
		body["wpgo_cache_ttl_public"] != 600.0 || body["wpgo_cache_mobile"] != true {
		t.Errorf("cache settings: %d %v", code, body)
	}
	if code, _, _ := api("POST", "/pursae/v1/cache/settings", map[string]any{"wpgo_cache_mobile": "yes"}); code != http.StatusBadRequest {
		t.Errorf("cache settings type check: %d", code)
	}
	if code, body, _ := api("POST", "/pursae/v1/cache/purge", map[string]any{"all": true}); code != http.StatusOK {
		t.Errorf("purge: %d %v", code, body)
	}
	if code, _, _ := api("GET", "/pursae/v1/cache", nil); code != http.StatusOK {
		t.Errorf("cache status: %d", code)
	}

	// Option groups: secrets are masked and survive a round trip.
	if code, body, _ := api("POST", "/pursae/v1/options/smtp", map[string]any{"settings": map[string]any{
		"mail": map[string]any{"from_email": "site@example.test"}, "mailers": map[string]any{"smtp": map[string]any{"host": "smtp.example.test", "pass": "s3cret"}}}}); code != http.StatusOK {
		t.Fatalf("update smtp: %d %v", code, body)
	}
	code, smtp, _ := api("GET", "/pursae/v1/options/smtp", nil)
	st := smtp["settings"].(map[string]any)
	if code != http.StatusOK || st["mail"].(map[string]any)["from_email"] != "site@example.test" {
		t.Fatalf("read smtp: %d %v", code, smtp)
	}
	if sm := st["mailers"].(map[string]any)["smtp"].(map[string]any); sm["pass"] != secretMask || sm["host"] != "smtp.example.test" {
		t.Errorf("smtp password not masked: %v", sm)
	}
	if code, body, _ := api("POST", "/pursae/v1/options/smtp", map[string]any{"settings": st}); code != http.StatusOK {
		t.Errorf("send back masked settings: %d %v", code, body)
	}
	if code, body, _ := api("GET", "/pursae/v1/options/smtp", nil); code != http.StatusOK || body["settings"].(map[string]any)["mailers"].(map[string]any)["smtp"].(map[string]any)["pass"] != secretMask {
		t.Errorf("smtp after the round trip: %v", body)
	}
	if got := srv.msLoadRaw().Mailers["smtp"]["pass"]; got != "s3cret" {
		t.Errorf("stored smtp password after sending back the mask = %q", got)
	}
	if code, _, _ := api("GET", "/pursae/v1/options/nope", nil); code != http.StatusNotFound {
		t.Errorf("unknown group: %d", code)
	}

	// The security group follows the settings screen's rules, so the API cannot lock everyone out.
	if code, body, _ := api("POST", "/pursae/v1/options/security", map[string]any{"settings": map[string]any{"idle_minutes": 30, "block_list": "203.0.113.7"}}); code != http.StatusOK {
		t.Errorf("valid security update: %d %v", code, body)
	}
	if st := srv.asLoad(); st.IdleMinutes != 30 || st.BlockList != "203.0.113.7" {
		t.Errorf("security update not stored: %d %q", st.IdleMinutes, st.BlockList)
	}
	for name, patch := range map[string]map[string]any{
		"own address blocked": {"block_list": "10.0.0.1\n127.0.0.1"},
		"bad IP entry":        {"allow_list": "not-an-ip"},
		"reserved login slug": {"hide_login": true, "login_slug": "wp-admin"},
		"bad login slug":      {"hide_login": true, "login_slug": "a b"},
		"idle below minimum":  {"idle_minutes": 2},
		"window out of range": {"fail_window": 0},
		"bad alert address":   {"alert_email": "nobody"},
		"bad frame options":   {"frame_options": "ALLOWALL"},
	} {
		if code, body, _ := api("POST", "/pursae/v1/options/security", map[string]any{"settings": patch}); code != http.StatusBadRequest {
			t.Errorf("security %s: %d %v", name, code, body)
		}
	}
	if ok, _ := srv.asMailReady(); !ok {
		if code, _, _ := api("POST", "/pursae/v1/options/security", map[string]any{"settings": map[string]any{"require_roles": map[string]any{"editor": "mfa"}}}); code != http.StatusBadRequest {
			t.Errorf("two-step requirement without mail: %d", code)
		}
		if got := srv.asLoad().RequireRoles["editor"]; got != "" { // a refused update must not reach the cached settings
			t.Errorf("refused require_roles is live: %q", got)
		}
	}
	if st := srv.asLoad(); st.BlockList != "203.0.113.7" || st.HideLogin || st.IdleMinutes != 30 || st.FailWindow == 0 {
		t.Errorf("refused security settings were stored: %+v", st)
	}

	// SEO settings and redirects.
	if code, body, _ := api("POST", "/pursae/v1/seo/settings", map[string]any{"settings": map[string]any{"separator": "|"}}); code != http.StatusOK ||
		body["settings"].(map[string]any)["separator"] != "|" {
		t.Errorf("seo settings: %d %v", code, body)
	}
	code, red, _ := api("POST", "/pursae/v1/seo/redirects", map[string]any{"source": "/old", "target": "/new", "code": 301})
	if code != http.StatusCreated {
		t.Fatalf("redirect: %d %v", code, red)
	}
	if code, _, list := api("GET", "/pursae/v1/seo/redirects", nil); code != http.StatusOK || len(list) != 1 {
		t.Fatalf("redirects: %d %v", code, list)
	} else if id := list[0].(map[string]any)["id"].(float64); true {
		if code, _, _ := api("DELETE", "/pursae/v1/seo/redirects/"+itoa64(int64(id)), nil); code != http.StatusOK {
			t.Errorf("delete redirect: %d", code)
		}
	}

	// The content-only password.
	if code, body, _ := limited("GET", "/pursae/v1/plugins", nil); code != http.StatusForbidden || body["code"] != "rest_scope_forbidden" {
		t.Errorf("scoped plugins: %d %v", code, body)
	}
	if code, _, _ := limited("GET", "/wp/v2/settings", nil); code != http.StatusForbidden {
		t.Errorf("scoped settings: %d", code)
	}
	if code, _, _ := limited("POST", "/wp/v2/users/me/application-passwords", map[string]any{"name": "escalate"}); code != http.StatusForbidden {
		t.Errorf("scoped password minting passwords: %d", code)
	}
	code, p, _ := limited("POST", "/wp/v2/posts", map[string]any{"title": "From an agent", "content": "<!-- wp:paragraph --><p>Hi</p><!-- /wp:paragraph -->", "status": "publish"})
	if code != http.StatusCreated {
		t.Fatalf("scoped create post: %d %v", code, p)
	}
	if code, _, _ := limited("GET", "/wp/v2/users/me", nil); code != http.StatusOK {
		t.Errorf("scoped users/me: %d", code)
	}

	// MCP over HTTP.
	rpc := func(auth [2]string, id int, method string, params any) map[string]any {
		t.Helper()
		code, body, _ := do(anon, auth, "POST", "/pursae/v1/mcp", map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if code != http.StatusOK {
			t.Fatalf("mcp %s: %d %v", method, code, body)
		}
		if body["error"] != nil {
			t.Fatalf("mcp %s: %v", method, body["error"])
		}
		return body["result"].(map[string]any)
	}
	init := rpc(fa, 1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test"}})
	if init["protocolVersion"] != "2025-06-18" || init["serverInfo"].(map[string]any)["name"] != "pursae" {
		t.Errorf("initialize = %v", init)
	}
	if code, _, _ := do(anon, fa, "POST", "/pursae/v1/mcp", map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); code != http.StatusAccepted {
		t.Errorf("notification: %d", code)
	}
	if tools := rpc(fa, 2, "tools/list", nil)["tools"].([]any); len(tools) < 40 {
		t.Errorf("tools/list: %d tools", len(tools))
	}
	res := rpc(fa, 3, "tools/call", map[string]any{"name": "create_page", "arguments": map[string]any{"title": "About", "status": "publish"}})
	if res["isError"] == true || res["structuredContent"].(map[string]any)["type"] != "page" {
		t.Errorf("create_page = %v", res)
	}
	res = rpc(fa, 4, "tools/call", map[string]any{"name": "list_posts", "arguments": map[string]any{"per_page": 5}})
	if res["isError"] == true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), "From an agent") {
		t.Errorf("list_posts = %v", res)
	}
	res = rpc(fa, 5, "tools/call", map[string]any{"name": "upload_media", "arguments": map[string]any{"filename": "dot.gif",
		"data_base64": "R0lGODlhAQABAIAAAP///wAAACH5BAEAAAAALAAAAAABAAEAAAICRAEAOw==", "alt_text": "A dot"}})
	if res["isError"] == true || res["structuredContent"].(map[string]any)["alt_text"] != "A dot" {
		t.Errorf("upload_media = %v", res)
	}
	res = rpc(sa, 6, "tools/call", map[string]any{"name": "list_plugins", "arguments": map[string]any{}})
	if res["isError"] != true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), "rest_scope_forbidden") {
		t.Errorf("scoped list_plugins = %v", res)
	}
	res = rpc(fa, 7, "tools/call", map[string]any{"name": "get_post", "arguments": map[string]any{"id": "1/../../settings"}})
	if res["isError"] != true {
		t.Errorf("path argument injection accepted: %v", res)
	}
	res = rpc(fa, 10, "tools/call", map[string]any{"name": "rest_request", "arguments": map[string]any{"method": "GET", "path": "/wp/v2/../../wp-admin/"}})
	if res["isError"] != true {
		t.Errorf("rest_request left /wp-json: %v", res)
	}
	res = rpc(fa, 11, "tools/call", map[string]any{"name": "rest_request", "arguments": map[string]any{"method": "GET", "path": "/wp/v2/comments"}})
	if res["isError"] == true {
		t.Errorf("rest_request comments: %v", res)
	}
	docs := rpc(fa, 8, "resources/list", nil)["resources"].([]any)
	if len(docs) == 0 {
		t.Fatal("no doc resources")
	}
	uri := docs[0].(map[string]any)["uri"].(string)
	if got := rpc(fa, 9, "resources/read", map[string]any{"uri": uri})["contents"].([]any); len(got) != 1 {
		t.Errorf("resources/read %s = %v", uri, got)
	}
	if code, _, _ := do(anon, [2]string{"admin", "wrong"}, "POST", "/pursae/v1/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}); code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", code)
	}
}
