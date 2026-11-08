package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestMCPHTTPTransport covers /wp-json/pursae/v1/mcp at the HTTP level: GET without a session is 405,
// an anonymous call gets the OAuth Bearer challenge with the resource metadata URL, a message without a method is an Invalid Request, and the
// generic tools (CRUD through REST, argument checks) work end to end.
func TestMCPHTTPTransport(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	req, _ := http.NewRequest("POST", base+"/wp-json/wp/v2/users/me/application-passwords", strings.NewReader(`{"name":"MCP test"}`))
	req.Header.Set("X-WP-Nonce", nonce)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var ap map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&ap)
	resp.Body.Close()
	pass := strAny(ap["password"])
	if pass == "" {
		t.Fatalf("no application password: %v", ap)
	}
	anon := &http.Client{}
	send := func(auth bool, method, body string) (*http.Response, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, base+"/wp-json/pursae/v1/mcp", strings.NewReader(body))
		if auth {
			req.SetBasicAuth("admin", pass)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		r, err := anon.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		raw, _ := io.ReadAll(r.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return r, out
	}
	if r, _ := send(true, "GET", ""); r.StatusCode != http.StatusMethodNotAllowed || !strings.Contains(r.Header.Get("Allow"), "POST") {
		t.Errorf("GET: %d Allow=%q", r.StatusCode, r.Header.Get("Allow"))
	}
	if r, _ := send(false, "POST", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); r.StatusCode != http.StatusUnauthorized || !strings.Contains(r.Header.Get("WWW-Authenticate"), "resource_metadata=") {
		t.Errorf("anonymous: %d %q", r.StatusCode, r.Header.Get("WWW-Authenticate"))
	}
	if r, body := send(true, "POST", `{"jsonrpc":"2.0","id":1}`); r.StatusCode != http.StatusBadRequest || body["error"].(map[string]any)["code"] != -32600.0 {
		t.Errorf("no method: %d %v", r.StatusCode, body)
	}
	if r, body := send(true, "POST", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); r.StatusCode != http.StatusOK || body["error"] != nil {
		t.Errorf("ping: %d %v", r.StatusCode, body)
	}
	call := func(id int, tool string, args map[string]any) map[string]any {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
		r, body := send(true, "POST", string(bytes.TrimSpace(b)))
		if r.StatusCode != http.StatusOK || body["error"] != nil {
			t.Fatalf("%s: %d %v", tool, r.StatusCode, body)
		}
		return body["result"].(map[string]any)
	}
	res := call(2, "create_post", map[string]any{"title": "Via MCP", "status": "draft"})
	if res["isError"] == true {
		t.Fatalf("create_post: %v", res)
	}
	id := res["structuredContent"].(map[string]any)["id"].(float64)
	if res := call(3, "update_post", map[string]any{"id": id, "title": "Renamed via MCP"}); res["isError"] == true ||
		res["structuredContent"].(map[string]any)["title"].(map[string]any)["raw"] != "Renamed via MCP" {
		t.Errorf("update_post: %v", res)
	}
	if res := call(4, "get_post", map[string]any{"id": id, "context": "edit"}); res["isError"] == true || res["structuredContent"].(map[string]any)["status"] != "draft" {
		t.Errorf("get_post: %v", res)
	}
	if res := call(5, "delete_post", map[string]any{"id": id}); res["isError"] == true {
		t.Errorf("delete_post: %v", res)
	}
	if res := call(6, "get_post", map[string]any{}); res["isError"] != true {
		t.Errorf("get_post without an id: %v", res)
	}
	if res := call(7, "rest_request", map[string]any{"method": "POST", "path": "/pursae/v1/mcp", "body": map[string]any{}}); res["isError"] != true {
		t.Errorf("rest_request into the MCP endpoint itself: %v", res)
	}
	if res := call(8, "get_post", map[string]any{"id": 999999}); res["isError"] != true {
		t.Errorf("get_post of a missing post: %v", res)
	}
}
