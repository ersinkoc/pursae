package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// gapOAuthToken registers an MCP client, runs the consent screen as the signed-in admin with the
// given scope request and extra form values, and returns the access token and the token response.
func gapOAuthToken(t *testing.T, base string, c *http.Client, askScope string, extra url.Values) (string, map[string]any) {
	t.Helper()
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	do := func(method, u string, body io.Reader, hdr map[string]string) []byte {
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
		return b
	}
	var cl map[string]any
	regBody := do("POST", base+"/oauth/register", strings.NewReader(`{"client_name":"Gap AI","redirect_uris":["http://localhost:9999/cb"]}`), map[string]string{"Content-Type": "application/json"})
	_ = json.Unmarshal(regBody, &cl)
	if cl["client_id"] == nil {
		t.Fatalf("register: %s", regBody)
	}
	clientID, _ := cl["client_id"].(string)
	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	q := url.Values{"client_id": {clientID}, "redirect_uri": {"http://localhost:9999/cb"}, "response_type": {"code"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	if askScope != "" {
		q.Set("scope", askScope)
	}
	_, page, _ := get(t, c, base+"/wp-admin/oauth-authorize?"+q.Encode())
	if askScope == "" {
		if !strings.Contains(page, `value="content" checked`) || !strings.Contains(page, `value="media" checked`) || strings.Contains(page, `value="plugins" checked`) {
			t.Errorf("consent screen without a scope request must preselect content and media only")
		}
		if !strings.Contains(page, `name="full_access"`) || strings.Contains(page, "Leave every box unticked") {
			t.Errorf("consent screen needs an explicit full access box and no 'leave unticked' hint")
		}
	}
	form := url.Values{"_wpnonce": {formNonce(t, page)}, "action": {"oauth_authorize"}, "approve": {"1"}, "client_id": {clientID},
		"redirect_uri": {"http://localhost:9999/cb"}, "response_type": {"code"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	for k, v := range extra {
		form[k] = v
	}
	_, _, loc := post(t, c, base+"/wp-admin/oauth-authorize", form)
	lu, _ := url.Parse(loc)
	code := lu.Query().Get("code")
	if code == "" {
		t.Fatalf("no code: %s", loc)
	}
	var tok map[string]any
	_ = json.Unmarshal(do("POST", base+"/oauth/token", strings.NewReader(url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code},
		"redirect_uri": {"http://localhost:9999/cb"}, "code_verifier": {verifier}}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"}), &tok)
	access, _ := tok["access_token"].(string)
	if access == "" {
		t.Fatalf("no token: %v", tok)
	}
	return access, tok
}

func gapREST(t *testing.T, base, token, method, route string, body any) (int, map[string]any, []any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, base+"/wp-json"+route, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	var l []any
	if json.Unmarshal(raw, &m) != nil {
		_ = json.Unmarshal(raw, &l)
	}
	return resp.StatusCode, m, l
}

func gapToolNames(t *testing.T, base, token string) map[string]bool {
	t.Helper()
	req, _ := http.NewRequest("POST", base+"/wp-json/pursae/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Result struct {
			Tools []struct{ Name string } `json:"tools"`
		} `json:"result"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	names := map[string]bool{}
	for _, tl := range out.Result.Tools {
		names[tl.Name] = true
	}
	return names
}

func TestMCPHeadlessAndOAuthDefaults(t *testing.T) {
	base, c, _ := hgSite(t)

	// headless-mcp/1.1: no scope asked, none ticked -> a narrow token, not every permission.
	narrow, tok := gapOAuthToken(t, base, c, "", url.Values{})
	if tok["scope"] != "content media" {
		t.Errorf("default scope = %v, want content media", tok["scope"])
	}
	// Full access only with the explicit box.
	full, ftok := gapOAuthToken(t, base, c, "", url.Values{"full_access": {"1"}})
	if ftok["scope"] != "" {
		t.Errorf("full access scope = %q, want empty (all permissions)", ftok["scope"])
	}

	// headless-mcp/1.4: the tool list follows scopes.
	names := gapToolNames(t, base, narrow)
	if !names["create_post"] || !names["upload_media"] {
		t.Error("content+media token lacks its tools")
	}
	for _, n := range []string{"install_plugin", "write_theme_file", "update_settings", "headless_save_type", "cache_purge", "seo_update_meta"} {
		if names[n] {
			t.Errorf("content+media token is offered %s", n)
		}
	}
	fnames := gapToolNames(t, base, full)
	for _, n := range []string{"install_plugin", "headless_save_type", "headless_find", "headless_publish"} {
		if !fnames[n] {
			t.Errorf("full token misses %s", n)
		}
	}
	if fnames["seo_update_meta"] || fnames["seo_get_settings"] {
		t.Error("seo tools listed while Pursae SEO is off")
	}

	// headless-mcp/1.0: manage schemas and entries over REST with an OAuth token.
	if st, _, _ := gapREST(t, base, narrow, "GET", "/pursae/v1/headless/types", nil); st != 403 {
		t.Errorf("content token on headless types: %d, want 403 (needs the settings scope)", st)
	}
	st, sc, _ := gapREST(t, base, full, "POST", "/pursae/v1/headless/types", map[string]any{"schema": map[string]any{
		"kind": "collectionType", "displayName": "Blog", "singularName": "blog", "pluralName": "blogs", "draftAndPublish": true,
		"attributes": []any{map[string]any{"name": "title", "type": "text", "required": true}}}})
	if st != 201 || sc["uid"] != "blog" {
		t.Fatalf("create type: %d %v", st, sc)
	}
	if st, _, l := gapREST(t, base, full, "GET", "/pursae/v1/headless/types", nil); st != 200 || len(l) == 0 {
		t.Errorf("list types: %d %v", st, l)
	}
	if st, _, _ := gapREST(t, base, full, "POST", "/pursae/v1/headless/types", map[string]any{"schema": map[string]any{"kind": "collectionType", "displayName": "Blog", "singularName": "blog", "pluralName": "blogs"}}); st != 409 {
		t.Errorf("duplicate type: %d, want 409", st)
	}
	st, e, _ := gapREST(t, base, full, "POST", "/pursae/v1/headless/entries/blog", map[string]any{"data": map[string]any{"title": "Hello"}})
	doc, _ := e["documentId"].(string)
	if st != 201 || doc == "" || e["status"] != "draft" {
		t.Fatalf("create entry: %d %v", st, e)
	}
	// A draft may be incomplete; publishing it is where required fields are enforced.
	st, e2, _ := gapREST(t, base, full, "POST", "/pursae/v1/headless/entries/blog", map[string]any{"data": map[string]any{}})
	if st != 201 {
		t.Fatalf("empty draft: %d %v", st, e2)
	}
	doc2, _ := e2["documentId"].(string)
	if st, e, _ := gapREST(t, base, full, "POST", "/pursae/v1/headless/entries/blog/"+doc2+"/publish", nil); st != 400 {
		t.Errorf("publishing without the required title: %d %v", st, e)
	}
	if st, e, _ := gapREST(t, base, full, "POST", "/pursae/v1/headless/entries/blog/"+doc+"/publish", nil); st != 200 || e["status"] != "published" {
		t.Errorf("publish: %d %v", st, e)
	}
	if st, _, l := gapREST(t, base, full, "GET", "/pursae/v1/headless/entries/blog?status=published", nil); st != 200 || len(l) != 1 {
		t.Errorf("find published: %d %v", st, l)
	}
	if st, _, _ := gapREST(t, base, full, "DELETE", "/pursae/v1/headless/entries/blog/"+doc, nil); st != 501 {
		t.Errorf("delete without force: %d", st)
	}
	if st, _, _ := gapREST(t, base, full, "DELETE", "/pursae/v1/headless/entries/blog/"+doc+"?force=true", nil); st != 200 {
		t.Errorf("delete entry: %d", st)
	}
	if st, _, _ := gapREST(t, base, full, "DELETE", "/pursae/v1/headless/types/blog?force=true", nil); st != 200 {
		t.Errorf("delete type: %d", st)
	}

	// The plugin off: routes answer 404 and the tools disappear.
	hgToggle(t, c, base, "deactivate")
	if st, _, _ := gapREST(t, base, full, "GET", "/pursae/v1/headless/types", nil); st != 404 {
		t.Errorf("headless off: %d, want 404", st)
	}
	if gapToolNames(t, base, full)["headless_find"] {
		t.Error("headless tools listed while the plugin is off")
	}
}
