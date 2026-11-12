package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestMCPToolEdges covers what the broader MCP tests leave out: an OAuth approval without scopes gets
// the narrow default, expired access and refresh tokens are refused, upload_media's source_url cannot
// reach this machine (SSRF guard), and the "settings" wrapper of update_options is opened.
func TestMCPToolEdges(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	do := func(method, u, body string, hdr map[string]string) (*http.Response, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, u, strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := anon.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return resp, m
	}
	_, reg := do("POST", base+"/oauth/register", `{"client_name":"Edge AI","redirect_uris":["http://localhost:9999/cb"]}`, map[string]string{"Content-Type": "application/json"})
	clientID, _ := reg["client_id"].(string)
	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	grant := func() map[string]any {
		t.Helper()
		_, page, _ := get(t, c, base+"/wp-admin/oauth-authorize?"+url.Values{"client_id": {clientID}, "redirect_uri": {"http://localhost:9999/cb"},
			"response_type": {"code"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}.Encode())
		_, _, loc := post(t, c, base+"/wp-admin/oauth-authorize", url.Values{"_wpnonce": {formNonce(t, page)}, "action": {"oauth_authorize"},
			"client_id": {clientID}, "redirect_uri": {"http://localhost:9999/cb"}, "response_type": {"code"}, "code_challenge": {challenge},
			"code_challenge_method": {"S256"}, "approve": {"1"}}) // no scopes[] at all
		lu, _ := url.Parse(loc)
		form := url.Values{"grant_type": {"authorization_code"}, "code": {lu.Query().Get("code")}, "redirect_uri": {"http://localhost:9999/cb"},
			"code_verifier": {verifier}, "client_id": {clientID}}
		resp, tok := do("POST", base+"/oauth/token", form.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
		if resp.StatusCode != 200 {
			t.Fatalf("token: %d %v", resp.StatusCode, tok)
		}
		return tok
	}
	tok := grant()
	if tok["scope"] != strings.Join(oauthDefaultScopes, " ") {
		t.Errorf("scope-less approval granted %q, want the default %q", tok["scope"], strings.Join(oauthDefaultScopes, " "))
	}
	access := tok["access_token"].(string)
	bearer := map[string]string{"Authorization": "Bearer " + access, "Content-Type": "application/json"}
	if resp, _ := do("GET", base+"/wp-json/pursae/v1/plugins", "", bearer); resp.StatusCode != 403 {
		t.Errorf("default scopes reach plugins: %d", resp.StatusCode)
	}

	// Expired access token: 401 invalid_token; the refresh token still works and yields a usable pair.
	ctx := context.Background()
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE oauth_tokens SET access_expires_at = now() - interval '1 minute' WHERE access_hash = $1`, oauthHash(access))
	if resp, _ := do("GET", base+"/wp-json/wp/v2/posts", "", bearer); resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "invalid_token") {
		t.Errorf("expired access token: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	refresh := func(rt string) (int, map[string]any) {
		resp, m := do("POST", base+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {clientID}}.Encode(),
			map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
		return resp.StatusCode, m
	}
	st, tok2 := refresh(tok["refresh_token"].(string))
	if st != 200 {
		t.Fatalf("refresh after access expiry: %d %v", st, tok2)
	}
	if resp, _ := do("GET", base+"/wp-json/wp/v2/posts", "", map[string]string{"Authorization": "Bearer " + tok2["access_token"].(string)}); resp.StatusCode != 200 {
		t.Errorf("refreshed token: %d", resp.StatusCode)
	}
	// Expired refresh token: invalid_grant.
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE oauth_tokens SET refresh_expires_at = now() - interval '1 minute' WHERE refresh_hash = $1`, oauthHash(tok2["refresh_token"].(string)))
	if st, m := refresh(tok2["refresh_token"].(string)); st != 400 || m["error"] != "invalid_grant" {
		t.Errorf("expired refresh token: %d %v", st, m)
	}

	// Tools, through an Application Password.
	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	req, _ := http.NewRequest("POST", base+"/wp-json/wp/v2/users/me/application-passwords", strings.NewReader(`{"name":"MCP edge","full_access":true}`))
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
	if got := joinAny(ap["scopes"]); got != "full_access" {
		t.Fatalf("application password scopes: %q body=%v", got, ap)
	}
	call := func(id int, tool string, args map[string]any) map[string]any {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
		r, _ := http.NewRequest("POST", base+"/wp-json/pursae/v1/mcp", bytes.NewReader(b))
		r.SetBasicAuth("admin", pass)
		r.Header.Set("Content-Type", "application/json")
		rr, err := anon.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer rr.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(rr.Body).Decode(&body)
		if body["result"] == nil {
			t.Fatalf("%s: %d %v", tool, rr.StatusCode, body)
		}
		return body["result"].(map[string]any)
	}
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("not-really-a-png"))
	}))
	defer local.Close()
	if res := call(1, "upload_media", map[string]any{"filename": "x.png", "source_url": local.URL + "/x.png"}); res["isError"] != true {
		t.Errorf("source_url on this machine was fetched: %v", res)
	}
	if res := call(2, "upload_media", map[string]any{"filename": "x.png", "source_url": "file:///etc/passwd"}); res["isError"] != true {
		t.Errorf("file:// source_url accepted: %v", res)
	}
	// The "settings" wrapper: update_options merges the patch, get_options shows it.
	if res := call(3, "update_options", map[string]any{"group": "maintenance", "settings": map[string]any{"header_text": "Back soon (edge test)"}}); res["isError"] == true {
		t.Fatalf("update_options: %v", res)
	}
	res := call(4, "get_options", map[string]any{"group": "maintenance"})
	if res["isError"] == true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), "Back soon (edge test)") {
		t.Errorf("get_options after update: %v", res)
	}
}
