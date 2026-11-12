package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mcpTestAgent signs in as admin, creates an Application Password with the scopes and returns helpers
// that speak JSON-RPC to the MCP endpoint with it.
func mcpTestAgent(t *testing.T, base string, c *http.Client, name string, scopes []string) (pass string, call func(tool string, args map[string]any) map[string]any) {
	t.Helper()
	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	b, _ := json.Marshal(map[string]any{"name": name, "scopes": scopes})
	req, _ := http.NewRequest("POST", base+"/wp-json/wp/v2/users/me/application-passwords", bytes.NewReader(b))
	req.Header.Set("X-WP-Nonce", nonce)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var ap map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&ap)
	resp.Body.Close()
	pass = strAny(ap["password"])
	if pass == "" {
		t.Fatalf("no application password: %v", ap)
	}
	call = func(tool string, args map[string]any) map[string]any {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
		req, _ := http.NewRequest("POST", base+"/wp-json/pursae/v1/mcp", bytes.NewReader(b))
		req.SetBasicAuth("admin", pass)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		res, _ := out["result"].(map[string]any)
		if res == nil {
			t.Fatalf("%s: no result: %s", tool, raw)
		}
		return res
	}
	return pass, call
}

func mcpText(res map[string]any) string {
	list, _ := res["content"].([]any)
	if len(list) == 0 {
		return ""
	}
	m, _ := list[0].(map[string]any)
	return strAny(m["text"])
}

// TestMCPToolFamilies runs the menu, SEO and theme-file tools end to end through tools/call, and the
// size limit of upload_media's source_url.
func TestMCPToolFamilies(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, call := mcpTestAgent(t, base, c, "Family agent", nil)
	sc := func(res map[string]any) map[string]any {
		m, _ := res["structuredContent"].(map[string]any)
		if m == nil {
			m = map[string]any{}
		}
		return m
	}

	// Menus: create a menu, add, update, list and delete an item.
	menu := call("create_menu", map[string]any{"name": "Agent menu"})
	if menu["isError"] == true || sc(menu)["id"] == nil {
		t.Fatalf("create_menu: %v", menu)
	}
	menuID := sc(menu)["id"]
	if r := call("list_menus", nil); r["isError"] == true || !strings.Contains(mcpText(r), "Agent menu") {
		t.Errorf("list_menus: %v", r)
	}
	if r := call("list_menu_locations", nil); r["isError"] == true {
		t.Errorf("list_menu_locations: %v", r)
	}
	item := call("create_menu_item", map[string]any{"menus": menuID, "title": "Docs", "type": "custom", "url": "https://example.test/docs", "status": "publish"})
	if item["isError"] == true || sc(item)["id"] == nil {
		t.Fatalf("create_menu_item: %v", item)
	}
	itemID := sc(item)["id"]
	if r := call("update_menu_item", map[string]any{"id": itemID, "title": "Documentation"}); r["isError"] == true || !strings.Contains(mcpText(r), "Documentation") {
		t.Errorf("update_menu_item: %v", r)
	}
	if r := call("list_menu_items", map[string]any{"menus": menuID}); r["isError"] == true || !strings.Contains(mcpText(r), "Documentation") {
		t.Errorf("list_menu_items: %v", r)
	}
	if r := call("delete_menu_item", map[string]any{"id": itemID, "force": true}); r["isError"] == true {
		t.Errorf("delete_menu_item: %v", r)
	}
	if r := call("list_menu_items", map[string]any{"menus": menuID}); strings.Contains(mcpText(r), "Documentation") {
		t.Errorf("menu item survived delete: %v", r)
	}
	if r := call("update_menu_item", map[string]any{"id": 99999999, "title": "x"}); r["isError"] != true {
		t.Errorf("update of a missing menu item succeeded: %v", r)
	}

	// Pursae SEO: meta, analysis, settings and redirects.
	if r := call("activate_plugin", map[string]any{"slug": "seo-go"}); r["isError"] == true {
		t.Fatalf("activate seo-go: %v", r)
	}
	post := call("create_post", map[string]any{"title": "SEO target", "content": "<p>Gophers write Go. Gophers like Go.</p>", "status": "publish"})
	pid := sc(post)["id"]
	if pid == nil {
		t.Fatalf("create_post: %v", post)
	}
	if r := call("seo_update_meta", map[string]any{"id": pid, "fields": map[string]any{"title": "Custom SEO title", "focus_keyword": "gophers"}}); r["isError"] == true {
		t.Fatalf("seo_update_meta: %v", r)
	}
	if r := call("seo_get_meta", map[string]any{"id": pid}); r["isError"] == true || !strings.Contains(mcpText(r), "Custom SEO title") {
		t.Errorf("seo_get_meta: %v", r)
	}
	if r := call("seo_analyze", map[string]any{"post_id": pid}); r["isError"] == true || len(mcpText(r)) < 20 {
		t.Errorf("seo_analyze: %v", r)
	}
	if r := call("seo_update_settings", map[string]any{"settings": map[string]any{"separator": "|"}}); r["isError"] == true {
		t.Errorf("seo_update_settings: %v", r)
	}
	if r := call("seo_get_settings", nil); r["isError"] == true || !strings.Contains(mcpText(r), `"separator":"|"`) {
		t.Errorf("seo_get_settings: %s", mcpText(r))
	}
	red := call("seo_create_redirect", map[string]any{"source": "/old-agent", "target": "/new-agent", "code": 301})
	if red["isError"] == true || sc(red)["id"] == nil {
		t.Fatalf("seo_create_redirect: %v", red)
	}
	if r := call("seo_list_redirects", nil); !strings.Contains(mcpText(r), "/old-agent") {
		t.Errorf("seo_list_redirects: %v", r)
	}
	if r := call("seo_delete_redirect", map[string]any{"id": sc(red)["id"]}); r["isError"] == true {
		t.Errorf("seo_delete_redirect: %v", r)
	}
	if r := call("seo_list_redirects", nil); strings.Contains(mcpText(r), "/old-agent") {
		t.Errorf("redirect survived delete: %v", r)
	}

	// Theme files: write, read back, refuse a broken template and a path outside the theme, restore.
	themes := call("list_themes", nil)
	active := ""
	var rows []map[string]any
	_ = json.Unmarshal([]byte(mcpText(themes)), &rows)
	for _, r := range rows {
		if r["active"] == true {
			active = strAny(r["slug"])
		}
	}
	if active == "" {
		t.Fatalf("no active theme in %s", mcpText(themes))
	}
	orig := call("read_theme_file", map[string]any{"slug": active, "file": "style.css"})
	if orig["isError"] == true {
		t.Fatalf("read_theme_file: %v", orig)
	}
	origCSS := strAny(sc(orig)["content"])
	marker := "\n/* mcp-agent-marker */\n"
	if r := call("write_theme_file", map[string]any{"slug": active, "file": "style.css", "content": origCSS + marker}); r["isError"] == true {
		t.Fatalf("write_theme_file: %v", r)
	}
	if r := call("read_theme_file", map[string]any{"slug": active, "file": "style.css"}); !strings.Contains(strAny(sc(r)["content"]), "mcp-agent-marker") {
		t.Errorf("the written file does not read back: %v", r)
	}
	if r := call("write_theme_file", map[string]any{"slug": active, "file": "../../outside.css", "content": "x"}); r["isError"] != true {
		t.Errorf("write outside the theme succeeded: %v", r)
	}
	if r := call("restore_theme_file", map[string]any{"slug": active, "file": "style.css"}); r["isError"] == true {
		t.Errorf("restore_theme_file: %v", r)
	}
	if r := call("read_theme_file", map[string]any{"slug": active, "file": "style.css"}); strings.Contains(strAny(sc(r)["content"]), "mcp-agent-marker") {
		t.Errorf("restore kept the edit")
	}

	// upload_media source_url: a file above the limit is refused, one below it lands in the library.
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		n := 100
		if r.URL.Path == "/big.txt" {
			n = 5000
		}
		_, _ = w.Write(bytes.Repeat([]byte("a"), n))
	}))
	defer big.Close()
	oldClient, oldMax := mcpHTTPClient, mcpFetchMax
	mcpHTTPClient, mcpFetchMax = big.Client(), 1024 // the test server is on loopback, which the real client refuses
	defer func() { mcpHTTPClient, mcpFetchMax = oldClient, oldMax }()
	if r := call("upload_media", map[string]any{"filename": "big.txt", "source_url": big.URL + "/big.txt"}); r["isError"] != true || !strings.Contains(mcpText(r), "larger than") {
		t.Errorf("oversized source_url: %v", r)
	}
	if r := call("upload_media", map[string]any{"filename": "small.txt", "source_url": big.URL + "/small.txt"}); r["isError"] == true || sc(r)["id"] == nil {
		t.Errorf("small source_url: %v", r)
	}
}

// TestMCPPluginToggleNotifiesStreams: switching a plugin on or off pushes
// notifications/tools/list_changed to every open MCP stream, and the tool list really changed.
func TestMCPPluginToggleNotifiesStreams(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	pass, call := mcpTestAgent(t, base, c, "Stream agent", nil)
	rpc := func(body string, hdr map[string]string) (*http.Response, []byte) {
		req, _ := http.NewRequest("POST", base+"/wp-json/pursae/v1/mcp", strings.NewReader(body))
		req.SetBasicAuth("admin", pass)
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp, raw
	}
	resp, raw := rpc(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`, nil)
	sid := resp.Header.Get("Mcp-Session-Id")
	if sid == "" || !strings.Contains(string(raw), `"listChanged":true`) {
		t.Fatalf("initialize: %q %s", sid, raw)
	}
	hasSEO := func() bool {
		_, raw := rpc(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, map[string]string{"Mcp-Session-Id": sid})
		return strings.Contains(string(raw), `"seo_get_settings"`)
	}
	if r := call("deactivate_plugin", map[string]any{"slug": "seo-go"}); r["isError"] == true {
		t.Fatalf("deactivate: %v", r)
	}
	if hasSEO() {
		t.Fatal("seo tools listed while Pursae SEO is off")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/wp-json/pursae/v1/mcp", nil)
	req.SetBasicAuth("admin", pass)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sid)
	sresp, err := http.DefaultClient.Do(req)
	if err != nil || sresp.StatusCode != 200 {
		t.Fatalf("open stream: %v", err)
	}
	defer sresp.Body.Close()
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(sresp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	time.Sleep(200 * time.Millisecond)
	if r := call("activate_plugin", map[string]any{"slug": "seo-go"}); r["isError"] == true {
		t.Fatalf("activate: %v", r)
	}
	got := false
	for deadline := time.After(3 * time.Second); !got; {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("stream closed before the notification")
			}
			got = strings.Contains(l, `"method":"notifications/tools/list_changed"`)
		case <-deadline:
			t.Fatal("no tools/list_changed after a plugin was activated")
		}
	}
	if !hasSEO() {
		t.Error("seo tools missing after activation")
	}
}

// TestOAuthHousekeeping: deleting a user ends its OAuth grants and pending codes, and the hourly
// clean-up drops expired grants, expired codes, orphaned clients and old activity rows.
func TestOAuthHousekeeping(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	pool := srv.store.Pool
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	var uid int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (login, email, nicename, display_name, password_hash) VALUES ('gone', 'gone@example.test', 'gone', 'Gone', 'x') RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	const live, stale, orphan = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	for _, id := range []string{live, stale, orphan} {
		exec(`INSERT INTO oauth_clients (client_id, name, redirect_uris, created_at) VALUES ($1, $1, '{http://localhost/cb}', now() - interval '2 hours')`, id)
	}
	exec(`INSERT INTO oauth_tokens (client_id, user_id, access_hash, refresh_hash, access_expires_at, refresh_expires_at)
		VALUES ($1, $2, 'a-gone', 'r-gone', now() + interval '1 hour', now() + interval '30 days')`, live, uid)
	exec(`INSERT INTO oauth_codes (code_hash, client_id, user_id, redirect_uri, challenge, expires_at) VALUES ('c-gone', $1, $2, 'http://localhost/cb', 'x', now() + interval '5 minutes')`, live, uid)
	exec(`INSERT INTO oauth_tokens (client_id, user_id, access_hash, refresh_hash, access_expires_at, refresh_expires_at)
		VALUES ($1, 1, 'a-live', 'r-live', now() + interval '1 hour', now() + interval '30 days')`, live)
	exec(`INSERT INTO oauth_tokens (client_id, user_id, access_hash, refresh_hash, access_expires_at, refresh_expires_at)
		VALUES ($1, 1, 'a-stale', 'r-stale', now() - interval '31 days', now() - interval '1 day')`, stale)
	exec(`INSERT INTO oauth_codes (code_hash, client_id, user_id, redirect_uri, challenge, expires_at) VALUES ('c-old', $1, 1, 'http://localhost/cb', 'x', now() - interval '1 hour')`, live)
	exec(`INSERT INTO mcp_activity (tool, created_at) VALUES ('old_tool', now() - interval '91 days'), ('new_tool', now())`)

	if err := srv.store.DeleteUser(ctx, uid, 0); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM oauth_tokens WHERE user_id = $1`, uid) + count(`SELECT count(*) FROM oauth_codes WHERE user_id = $1`, uid); n != 0 {
		t.Errorf("%d OAuth rows outlived the deleted user", n)
	}
	if n := count(`SELECT count(*) FROM oauth_tokens WHERE access_hash = 'a-live'`); n != 1 {
		t.Error("deleting a user removed another user's grant")
	}

	if err := srv.oauthGC(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM oauth_tokens WHERE access_hash = 'a-stale'`); n != 0 {
		t.Error("an expired grant survived the clean-up")
	}
	if n := count(`SELECT count(*) FROM oauth_codes WHERE code_hash = 'c-old'`); n != 0 {
		t.Error("an expired code survived the clean-up")
	}
	if n := count(`SELECT count(*) FROM oauth_clients WHERE client_id = ANY($1)`, []string{stale, orphan}); n != 0 {
		t.Errorf("%d clients without a grant survived the clean-up", n)
	}
	if n := count(`SELECT count(*) FROM oauth_clients WHERE client_id = $1`, live) + count(`SELECT count(*) FROM oauth_tokens WHERE access_hash = 'a-live'`); n != 2 {
		t.Error("the clean-up removed a live grant or its client")
	}
	if old, fresh := count(`SELECT count(*) FROM mcp_activity WHERE tool = 'old_tool'`), count(`SELECT count(*) FROM mcp_activity WHERE tool = 'new_tool'`); old != 0 || fresh != 1 {
		t.Errorf("activity retention: old=%d fresh=%d", old, fresh)
	}
}
