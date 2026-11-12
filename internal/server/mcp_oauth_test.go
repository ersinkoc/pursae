package server

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestWantsSSE(t *testing.T) {
	for accept, want := range map[string]bool{"": false, "application/json": false, "application/json, text/event-stream": false,
		"text/event-stream": true, "text/event-stream, application/json": true} {
		if wantsSSE(accept) != want {
			t.Errorf("wantsSSE(%q) = %v", accept, !want)
		}
	}
}

func TestScopePickerHelpers(t *testing.T) {
	if scopeLabels(nil) != "Full access" || scopeLabels([]string{"content", "seo"}) != "Content, SEO" {
		t.Errorf("scopeLabels: %q %q", scopeLabels(nil), scopeLabels([]string{"content", "seo"}))
	}
	ch := scopeChoices([]string{"seo"})
	n := 0
	for _, c := range ch {
		if c.Checked {
			n++
			if c.Slug != "seo" {
				t.Errorf("ticked %s", c.Slug)
			}
		}
	}
	if n != 1 || len(ch) != len(appScopes) {
		t.Errorf("scopeChoices ticked %d of %d", n, len(ch))
	}
}

func TestMCPOAuthAndSSE(t *testing.T) {
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
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	send := func(method, u string, body io.Reader, hdr map[string]string) (*http.Response, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, u, body)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := anon.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, b
	}
	asJSON := func(b []byte) map[string]any { var m map[string]any; _ = json.Unmarshal(b, &m); return m }

	// Discovery: the 401 of the MCP endpoint points at the resource metadata.
	resp, _ := send("POST", base+"/wp-json/pursae/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), map[string]string{"Content-Type": "application/json"})
	wa := strings.Join(resp.Header.Values("WWW-Authenticate"), " | ")
	if resp.StatusCode != 401 || !strings.Contains(wa, `resource_metadata="`+base+`/.well-known/oauth-protected-resource/wp-json/pursae/v1/mcp"`) || !strings.Contains(wa, "Basic") {
		t.Fatalf("anonymous MCP: %d %q", resp.StatusCode, wa)
	}
	_, b := send("GET", base+"/.well-known/oauth-protected-resource/wp-json/pursae/v1/mcp", nil, nil)
	if m := asJSON(b); m["resource"] != base+"/wp-json/pursae/v1/mcp" || m["authorization_servers"].([]any)[0] != base {
		t.Fatalf("resource metadata: %s", b)
	}
	_, b = send("GET", base+"/.well-known/oauth-authorization-server", nil, nil)
	meta := asJSON(b)
	if meta["token_endpoint"] != base+"/oauth/token" || meta["registration_endpoint"] != base+"/oauth/register" || meta["code_challenge_methods_supported"].([]any)[0] != "S256" {
		t.Fatalf("server metadata: %s", b)
	}

	// Dynamic client registration.
	resp, b = send("POST", base+"/oauth/register", strings.NewReader(`{"client_name":"Test AI","redirect_uris":["javascript:alert(1)"]}`), map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode != 400 {
		t.Errorf("bad redirect accepted: %d %s", resp.StatusCode, b)
	}
	big := `{"client_name":"` + strings.Repeat("x", 100<<10) + `","redirect_uris":["http://localhost:9999/cb"]}`
	if resp, b = send("POST", base+"/oauth/register", strings.NewReader(big), map[string]string{"Content-Type": "application/json"}); resp.StatusCode != 400 {
		t.Errorf("oversized registration accepted: %d %.80s", resp.StatusCode, b)
	}
	resp, b = send("POST", base+"/oauth/register", strings.NewReader(`{"client_name":"Test AI","redirect_uris":["http://localhost:9999/cb"]}`), map[string]string{"Content-Type": "application/json"})
	cl := asJSON(b)
	clientID, _ := cl["client_id"].(string)
	if resp.StatusCode != 201 || clientID == "" || cl["token_endpoint_auth_method"] != "none" {
		t.Fatalf("register: %d %s", resp.StatusCode, b)
	}

	// Consent screen: PKCE is mandatory, an unregistered redirect is never followed.
	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	authURL := func(redirect, ch string) string {
		return base + "/wp-admin/oauth-authorize?" + url.Values{"client_id": {clientID}, "redirect_uri": {redirect}, "response_type": {"code"},
			"state": {"xyz"}, "code_challenge": {ch}, "code_challenge_method": {"S256"}, "scope": {"content seo"}}.Encode()
	}
	if code, _, _ := get(t, c, authURL("https://evil.example/cb", challenge)); code != 400 {
		t.Errorf("unregistered redirect: %d", code)
	}
	if code, _, loc := get(t, c, authURL("http://localhost:9999/cb", "")); code != 302 || !strings.Contains(loc, "error=invalid_request") || !strings.Contains(loc, "state=xyz") {
		t.Errorf("no PKCE: %d %s", code, loc)
	}
	_, page, _ := get(t, c, authURL("http://localhost:9999/cb", challenge))
	for _, want := range []string{"Test AI", `name="action" value="oauth_authorize"`, `value="content" checked`, `value="seo" checked`, "Yes, I approve of this connection"} {
		if !strings.Contains(page, want) {
			t.Errorf("consent page misses %q", want)
		}
	}
	// The approve/deny POST answers 302 to the client: the page's form-action must allow it.
	if r, err := c.Get(authURL("http://localhost:9999/cb", challenge)); err != nil {
		t.Fatal(err)
	} else {
		r.Body.Close()
		if csp := r.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self' http://localhost:9999") {
			t.Errorf("consent CSP does not allow the redirect origin: %q", csp)
		}
	}
	if strings.Contains(page, `value="plugins" checked`) {
		t.Error("plugins ticked although not asked for")
	}
	form := func(extra url.Values) url.Values {
		v := url.Values{"_wpnonce": {formNonce(t, page)}, "action": {"oauth_authorize"}, "client_id": {clientID}, "redirect_uri": {"http://localhost:9999/cb"},
			"response_type": {"code"}, "state": {"xyz"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
		for k, vs := range extra {
			v[k] = vs
		}
		return v
	}
	if _, _, loc := post(t, c, base+"/wp-admin/oauth-authorize", form(url.Values{"deny": {"1"}})); !strings.Contains(loc, "error=access_denied") {
		t.Errorf("deny: %s", loc)
	}
	code, _, loc := post(t, c, base+"/wp-admin/oauth-authorize", form(url.Values{"approve": {"1"}, "scopes[]": {"content", "seo"}}))
	lu, _ := url.Parse(loc)
	authCode := lu.Query().Get("code")
	if code != 302 || authCode == "" || lu.Query().Get("state") != "xyz" || !strings.HasPrefix(loc, "http://localhost:9999/cb?") {
		t.Fatalf("approve: %d %s", code, loc)
	}
	token := func(v url.Values) (int, map[string]any) {
		v.Set("client_id", clientID)
		resp, b := send("POST", base+"/oauth/token", strings.NewReader(v.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
		return resp.StatusCode, asJSON(b)
	}
	exchange := url.Values{"grant_type": {"authorization_code"}, "code": {authCode}, "redirect_uri": {"http://localhost:9999/cb"}, "code_verifier": {strings.Repeat("w", 50)}}
	if st, m := token(exchange); st != 400 || m["error"] != "invalid_grant" {
		t.Errorf("wrong verifier: %d %v", st, m)
	}
	exchange.Set("code_verifier", verifier)
	if st, m := token(exchange); st != 400 { // the failed attempt used the code up
		t.Errorf("code reused: %d %v", st, m)
	}
	// A fresh code, redeemed properly.
	_, _, loc = post(t, c, base+"/wp-admin/oauth-authorize", form(url.Values{"approve": {"1"}, "scopes[]": {"content", "seo"}}))
	lu, _ = url.Parse(loc)
	exchange.Set("code", lu.Query().Get("code"))
	st, tok := token(exchange)
	access, _ := tok["access_token"].(string)
	refresh, _ := tok["refresh_token"].(string)
	if st != 200 || access == "" || refresh == "" || tok["token_type"] != "Bearer" || tok["scope"] != "content seo" || tok["expires_in"].(float64) != 3600 {
		t.Fatalf("token: %d %v", st, tok)
	}
	bearer := map[string]string{"Authorization": "Bearer " + access}
	var gotRes string
	if err := srv.store.Pool.QueryRow(context.Background(), `SELECT resource FROM oauth_tokens WHERE access_hash = $1`, oauthHash(access)).Scan(&gotRes); err != nil || gotRes != base+"/wp-json/pursae/v1/mcp" {
		t.Fatalf("grant resource = %q (%v), want the MCP resource", gotRes, err)
	}
	if _, err := srv.store.Pool.Exec(context.Background(), `UPDATE oauth_tokens SET resource = $2 WHERE access_hash = $1`, oauthHash(access), "https://evil.example/wp-json/pursae/v1/mcp"); err != nil {
		t.Fatal(err)
	}
	if resp, _ := send("GET", base+"/wp-json/wp/v2/posts", nil, bearer); resp.StatusCode != 401 {
		t.Errorf("token bound to another resource: %d", resp.StatusCode)
	}
	if _, err := srv.store.Pool.Exec(context.Background(), `UPDATE oauth_tokens SET resource = $2 WHERE access_hash = $1`, oauthHash(access), gotRes); err != nil {
		t.Fatal(err)
	}
	if resp, b := send("GET", base+"/wp-json/wp/v2/posts", nil, bearer); resp.StatusCode != 200 {
		t.Errorf("posts with token: %d %s", resp.StatusCode, b)
	}
	if resp, b := send("GET", base+"/wp-json/pursae/v1/plugins", nil, bearer); resp.StatusCode != 403 || !strings.Contains(string(b), "rest_scope_forbidden") {
		t.Errorf("plugins with a content token: %d %s", resp.StatusCode, b)
	}
	if resp, _ := send("GET", base+"/wp-json/wp/v2/posts", nil, map[string]string{"Authorization": "Bearer nope"}); resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Errorf("bad token: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}

	// MCP: a session, an SSE answer, the server→client stream, DELETE.
	mcp := func(method string, body string, hdr map[string]string) (*http.Response, []byte) {
		h := map[string]string{"Authorization": "Bearer " + access, "Content-Type": "application/json"}
		for k, v := range hdr {
			h[k] = v
		}
		return send(method, base+"/wp-json/pursae/v1/mcp", strings.NewReader(body), h)
	}
	resp, b = mcp("POST", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`, map[string]string{"Accept": "application/json, text/event-stream"})
	sid := resp.Header.Get("Mcp-Session-Id")
	if resp.StatusCode != 200 || sid == "" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") || asJSON(b)["result"] == nil {
		t.Fatalf("initialize: %d %q %s", resp.StatusCode, sid, b)
	}
	resp, b = mcp("POST", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, map[string]string{"Accept": "text/event-stream", "Mcp-Session-Id": sid})
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") || !strings.HasPrefix(string(b), "event: message\ndata: {") || !strings.Contains(string(b), `"list_posts"`) {
		t.Errorf("SSE answer: %q %.120s", resp.Header.Get("Content-Type"), b)
	}
	if resp, _ = mcp("POST", `{"jsonrpc":"2.0","id":3,"method":"ping"}`, map[string]string{"Mcp-Session-Id": "00000000-0000-4000-8000-000000000000"}); resp.StatusCode != 404 {
		t.Errorf("unknown session: %d", resp.StatusCode)
	}
	if resp, _ = mcp("GET", "", map[string]string{"Accept": "application/json"}); resp.StatusCode != 405 {
		t.Errorf("GET without a stream: %d", resp.StatusCode)
	}
	// The GET stream gets what mcpPush sends.
	req, _ := http.NewRequest("GET", base+"/wp-json/pursae/v1/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sid)
	sresp, err := anon.Do(req)
	if err != nil || sresp.StatusCode != 200 || !strings.HasPrefix(sresp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("open stream: %v %v", err, sresp)
	}
	lines := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(sresp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	time.Sleep(200 * time.Millisecond)
	mcpPush(1, "notifications/tools/list_changed", map[string]any{})
	gotPush := false
	for deadline := time.After(3 * time.Second); !gotPush; {
		select {
		case l := <-lines:
			gotPush = strings.Contains(l, "notifications/tools/list_changed")
		case <-deadline:
			t.Fatal("no pushed message on the stream")
		}
	}
	// Resume: a new stream with Last-Event-ID replays what the first one carried.
	mcpPush(1, "notifications/resources/list_changed", map[string]any{})
	req2, _ := http.NewRequest("GET", base+"/wp-json/pursae/v1/mcp", nil)
	req2.Header.Set("Authorization", "Bearer "+access)
	req2.Header.Set("Accept", "text/event-stream")
	req2.Header.Set("Mcp-Session-Id", sid)
	req2.Header.Set("Last-Event-ID", "0")
	rresp, err := anon.Do(req2)
	if err != nil || rresp.StatusCode != 200 {
		t.Fatalf("resume: %v", err)
	}
	rsc := bufio.NewScanner(rresp.Body)
	var replay []string
	for rsc.Scan() && len(replay) < 6 {
		if l := rsc.Text(); l != "" {
			replay = append(replay, l)
		}
	}
	rresp.Body.Close()
	if j := strings.Join(replay, "\n"); !strings.Contains(j, "id: 1") || !strings.Contains(j, "list_changed") || !strings.Contains(j, "id: 2") {
		t.Errorf("replay: %q", j)
	}
	if resp, _ = mcp("DELETE", "", map[string]string{"Mcp-Session-Id": sid}); resp.StatusCode != 204 {
		t.Errorf("DELETE session: %d", resp.StatusCode)
	}
	select { // the open stream ends with its session
	case _, open := <-lines:
		for open {
			_, open = <-lines
		}
	case <-time.After(3 * time.Second):
		t.Error("stream still open after DELETE")
	}
	sresp.Body.Close()
	if resp, _ = mcp("POST", `{"jsonrpc":"2.0","id":4,"method":"ping"}`, map[string]string{"Mcp-Session-Id": sid}); resp.StatusCode != 404 {
		t.Errorf("ended session: %d", resp.StatusCode)
	}
	if resp, _ = mcp("POST", `{"jsonrpc":"2.0","id":5,"method":"ping"}`, nil); resp.StatusCode != 200 {
		t.Errorf("stateless call: %d", resp.StatusCode)
	}

	// Refresh rotates the pair.
	st, tok2 := token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
	access2, _ := tok2["access_token"].(string)
	if st != 200 || access2 == "" || access2 == access || tok2["refresh_token"] == refresh {
		t.Fatalf("refresh: %d %v", st, tok2)
	}
	if resp, _ := send("GET", base+"/wp-json/wp/v2/posts", nil, bearer); resp.StatusCode != 401 {
		t.Errorf("rotated access token still works: %d", resp.StatusCode)
	}
	if resp, _ := send("GET", base+"/wp-json/wp/v2/posts", nil, map[string]string{"Authorization": "Bearer " + access2}); resp.StatusCode != 200 {
		t.Errorf("rotated pair does not work: %d", resp.StatusCode)
	}
	// Reuse detection: the replaced refresh token is refused and, after the grace period, ends the whole
	// grant (OAuth 2.1 §4.3.1).
	prevGrace := oauthReuseGrace
	oauthReuseGrace = 0
	if st, _ := token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}); st != 400 {
		t.Errorf("old refresh token still works: %d", st)
	}
	if resp, _ := send("GET", base+"/wp-json/wp/v2/posts", nil, map[string]string{"Authorization": "Bearer " + access2}); resp.StatusCode != 401 {
		t.Errorf("grant survives a reused refresh token: %d", resp.StatusCode)
	}
	if st, _ := token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok2["refresh_token"].(string)}}); st != 400 {
		t.Errorf("current refresh token works after reuse detection: %d", st)
	}
	oauthReuseGrace = prevGrace

	// Two refreshes with the same token at once: exactly one gets a pair.
	newGrant := func() map[string]any {
		t.Helper()
		_, _, loc := post(t, c, base+"/wp-admin/oauth-authorize", form(url.Values{"approve": {"1"}}))
		lu, _ := url.Parse(loc)
		exchange.Set("code", lu.Query().Get("code"))
		st, tk := token(exchange)
		if st != 200 {
			t.Fatalf("new grant: %d %v", st, tk)
		}
		return tk
	}
	race := newGrant()["refresh_token"].(string)
	results := make(chan int, 4)
	for i := 0; i < 4; i++ {
		go func() {
			st, _ := token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {race}})
			results <- st
		}()
	}
	ok200 := 0
	for i := 0; i < 4; i++ {
		if <-results == 200 {
			ok200++
		}
	}
	if ok200 != 1 {
		t.Errorf("concurrent refreshes with one token: %d succeeded, want 1", ok200)
	}
	// Within the grace period the losers were only refused: the grant lives on.
	var grants int
	_ = srv.store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM oauth_tokens WHERE prev_refresh_hash = $1`, oauthHash(race)).Scan(&grants)
	if grants != 1 {
		t.Errorf("grant after concurrent refreshes: %d rows, want 1", grants)
	}
	_, _ = srv.store.Pool.Exec(context.Background(), `DELETE FROM oauth_tokens WHERE prev_refresh_hash = $1`, oauthHash(race)) // the profile below revokes its first grant

	// A second rotation must not hide the stolen refresh token: any older token in the chain burns the
	// whole family, even inside the grace window of the latest rotation.
	fam := newGrant()
	st, mid := token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {fam["refresh_token"].(string)}})
	if st != 200 {
		t.Fatalf("family refresh: %d", st)
	}
	st, latest := token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {mid["refresh_token"].(string)}})
	if st != 200 {
		t.Fatalf("family second refresh: %d", st)
	}
	if st, _ := token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {fam["refresh_token"].(string)}}); st != 400 {
		t.Errorf("ancestor refresh token still works: %d", st)
	}
	if resp, _ := send("GET", base+"/wp-json/wp/v2/posts", nil, map[string]string{"Authorization": "Bearer " + latest["access_token"].(string)}); resp.StatusCode != 401 {
		t.Errorf("grant family survived reuse of an older refresh token: %d", resp.StatusCode)
	}
	// RFC 7009: the refresh token a rotation replaced still revokes the grant.
	alive := newGrant()
	st, rotated := token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {alive["refresh_token"].(string)}})
	if st != 200 {
		t.Fatalf("revoke setup refresh: %d", st)
	}
	rvPrev := url.Values{"token": {alive["refresh_token"].(string)}, "client_id": {clientID}}
	if resp, _ := send("POST", base+"/oauth/revoke", strings.NewReader(rvPrev.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"}); resp.StatusCode != 200 {
		t.Errorf("revoke with previous refresh: %d", resp.StatusCode)
	}
	if resp, _ := send("GET", base+"/wp-json/wp/v2/posts", nil, map[string]string{"Authorization": "Bearer " + rotated["access_token"].(string)}); resp.StatusCode != 401 {
		t.Errorf("token works after revoke with the previous refresh token: %d", resp.StatusCode)
	}

	// Replaying an authorization code revokes the grant issued for it (OAuth 2.1 §4.1.3).
	tk4 := newGrant()
	if st, _ := token(exchange); st != 400 {
		t.Errorf("replayed code accepted: %d", st)
	}
	if resp, _ := send("GET", base+"/wp-json/wp/v2/posts", nil, map[string]string{"Authorization": "Bearer " + tk4["access_token"].(string)}); resp.StatusCode != 401 {
		t.Errorf("grant survives a replayed code: %d", resp.StatusCode)
	}
	access2 = newGrant()["access_token"].(string) // the grant the profile revokes below

	// Revoking from the profile cuts the grant off.
	_, prof, _ := get(t, c, base+"/wp-admin/profile")
	if !strings.Contains(prof, "Connected applications") || !strings.Contains(prof, "Test AI") {
		t.Fatalf("profile lists no connected application")
	}
	m := strings.Index(prof, `name="revoke_oauth_grant" value="`)
	if m < 0 {
		t.Fatal("no revoke button")
	}
	id := strings.SplitN(prof[m+len(`name="revoke_oauth_grant" value="`):], `"`, 2)[0]
	pf := url.Values{"revoke_oauth_grant": {id}, "action": {"update"}, "_wpnonce": {formNonce(t, prof)}, "user_id": {"1"}}
	if code, _, _ := post(t, c, base+"/wp-admin/profile", pf); code != 302 {
		t.Fatalf("revoke: %d", code)
	}
	if resp, _ := send("GET", base+"/wp-json/wp/v2/posts", nil, map[string]string{"Authorization": "Bearer " + access2}); resp.StatusCode != 401 {
		t.Errorf("revoked token still works: %d", resp.StatusCode)
	}

	// RFC 7009: the client revokes its own token (a fresh grant, as the profile one is gone).
	_, _, loc = post(t, c, base+"/wp-admin/oauth-authorize", form(url.Values{"approve": {"1"}}))
	lu, _ = url.Parse(loc)
	exchange.Set("code", lu.Query().Get("code"))
	st, tok3 := token(exchange)
	if st != 200 {
		t.Fatalf("third token: %d %v", st, tok3)
	}
	h3 := map[string]string{"Authorization": "Bearer " + tok3["access_token"].(string)}
	if resp, _ := send("GET", base+"/wp-json/wp/v2/posts", nil, h3); resp.StatusCode != 200 {
		t.Fatalf("fresh token: %d", resp.StatusCode)
	}
	// A session has one GET stream: a second replaces the first. A stream ends when its token is revoked.
	prevKeepAlive := mcpStreamKeepAlive
	mcpStreamKeepAlive = 100 * time.Millisecond
	defer func() { mcpStreamKeepAlive = prevKeepAlive }()
	resp, _ = send("POST", base+"/wp-json/pursae/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`),
		map[string]string{"Authorization": h3["Authorization"], "Content-Type": "application/json"})
	sid3 := resp.Header.Get("Mcp-Session-Id")
	openStream := func() (chan struct{}, func()) {
		t.Helper()
		req, _ := http.NewRequest("GET", base+"/wp-json/pursae/v1/mcp", nil)
		req.Header.Set("Authorization", h3["Authorization"])
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Mcp-Session-Id", sid3)
		r, err := anon.Do(req)
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("stream: %v %v", err, r)
		}
		ended := make(chan struct{})
		go func() { _, _ = io.Copy(io.Discard, r.Body); close(ended) }()
		return ended, func() { r.Body.Close() }
	}
	waitEnd := func(ch chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
			t.Errorf("stream still open: %s", what)
		}
	}
	first, closeFirst := openStream()
	defer closeFirst()
	time.Sleep(150 * time.Millisecond)
	second, closeSecond := openStream()
	defer closeSecond()
	waitEnd(first, "after a second stream opened")
	rv := url.Values{"token": {tok3["refresh_token"].(string)}, "client_id": {clientID}}
	if resp, _ := send("POST", base+"/oauth/revoke", strings.NewReader(rv.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"}); resp.StatusCode != 200 {
		t.Errorf("revoke: %d", resp.StatusCode)
	}
	if resp, _ := send("GET", base+"/wp-json/wp/v2/posts", nil, h3); resp.StatusCode != 401 {
		t.Errorf("token works after RFC 7009 revoke: %d", resp.StatusCode)
	}
	waitEnd(second, "after its token was revoked")

	// Housekeeping: a user keeps at most oauthMaxGrantsPerUser grants, and registration removes expired
	// grants and then the clients left without one, so old connections cannot fill the client cap.
	ctx := context.Background()
	for i := 0; i < oauthMaxGrantsPerUser+5; i++ {
		_, _ = srv.store.Pool.Exec(ctx, `INSERT INTO oauth_tokens (client_id, user_id, access_hash, refresh_hash, access_expires_at, refresh_expires_at)
			VALUES ($1, 1, $2, $3, now(), now() + interval '1 day')`, clientID, oauthRandom("a"), oauthRandom("r"))
	}
	newGrant()
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM oauth_tokens WHERE user_id = 1`).Scan(&n)
	if n != oauthMaxGrantsPerUser {
		t.Errorf("grants of one user: %d, want %d", n, oauthMaxGrantsPerUser)
	}
	_, _ = srv.store.Pool.Exec(ctx, `INSERT INTO oauth_clients (client_id, name, redirect_uris, created_at) VALUES ('00000000-0000-4000-8000-0000000000aa', 'Old', '{http://localhost/cb}', now() - interval '60 days')`)
	_, _ = srv.store.Pool.Exec(ctx, `INSERT INTO oauth_tokens (client_id, user_id, access_hash, refresh_hash, access_expires_at, refresh_expires_at)
		VALUES ('00000000-0000-4000-8000-0000000000aa', 1, 'x1', 'y1', now() - interval '31 days', now() - interval '1 day')`)
	send("POST", base+"/oauth/register", strings.NewReader(`{"client_name":"Next","redirect_uris":["http://localhost:9999/cb"]}`), map[string]string{"Content-Type": "application/json"})
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM oauth_clients WHERE client_id = '00000000-0000-4000-8000-0000000000aa'`).Scan(&n)
	if n != 0 {
		t.Error("a client whose only grant expired is kept")
	}
}
