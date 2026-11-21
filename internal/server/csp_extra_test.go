package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

func TestCSPValidateExtra(t *testing.T) {
	ok := []struct{ dir, in, want string }{
		{"connect-src", "https://api.github.com", "https://api.github.com"},
		{"connect-src", "HTTPS://API.GitHub.com/", "https://api.github.com"},
		{"connect-src", "https://api.example.com:8443", "https://api.example.com:8443"},
		{"connect-src", "https://api.example.com:443", "https://api.example.com"},
		{"connect-src", "https://*.example.com", "https://*.example.com"},
		{"connect-src", "wss://live.example.com", "wss://live.example.com"},
		{"font-src", "https://fonts.gstatic.com", "https://fonts.gstatic.com"},
		{"style-src", "https://fonts.googleapis.com", "https://fonts.googleapis.com"},
		{"connect-src", "https://bücher.example", "https://xn--bcher-kva.example"},
		{"connect-src", "https://ÖRNEK.com.tr", "https://xn--rnek-4qa.com.tr"},
	}
	for _, c := range ok {
		got, err := cspValidateExtra(c.dir, c.in)
		if err != nil || got != c.want {
			t.Errorf("%s %q: got %q, %v; want %q", c.dir, c.in, got, err, c.want)
		}
	}
	bad := []struct{ dir, in string }{
		{"script-src", "https://cdn.example.com"}, {"default-src", "https://a.example.com"}, {"worker-src", "https://a.example.com"},
		{"object-src", "https://a.example.com"}, {"base-uri", "https://a.example.com"}, {"form-action", "https://a.example.com"},
		{"frame-ancestors", "https://a.example.com"}, {"report-uri", "https://a.example.com"}, {"sandbox", "https://a.example.com"},
		{"img-src", "https://a.example.com"}, {"Connect-Src", "https://a.example.com"},
		{"connect-src", "*"}, {"connect-src", "https:"}, {"connect-src", "http:"}, {"connect-src", "http://api.example.com"},
		{"connect-src", "data:"}, {"connect-src", "blob:"}, {"connect-src", "filesystem:"},
		{"connect-src", "'self'"}, {"connect-src", "'unsafe-inline'"}, {"connect-src", "'unsafe-eval'"}, {"connect-src", "'nonce-abc'"},
		{"connect-src", "'sha256-abc'"}, {"connect-src", "'strict-dynamic'"},
		{"connect-src", "https://*.com"}, {"connect-src", "https://*.co.uk"}, {"connect-src", "https://com"}, {"connect-src", "https://*"},
		{"connect-src", "https://a.*.example.com"}, {"connect-src", "https://*a.example.com"},
		{"connect-src", "https://127.0.0.1"}, {"connect-src", "https://10.0.0.0"}, {"connect-src", "https://[::1]"}, {"connect-src", "https://1.2.3.4:8080"},
		{"connect-src", "https://localhost"}, {"connect-src", "https://app.localhost"}, {"connect-src", "https://localhost:3000"},
		{"connect-src", "https://api.example.com/path"}, {"connect-src", "https://api.example.com?q=1"}, {"connect-src", "https://api.example.com#x"},
		{"connect-src", "https://user@api.example.com"}, {"connect-src", "https://api.example.com:0"}, {"connect-src", "https://api.example.com:99999"},
		{"connect-src", "https://api.example.com:08443"},
		{"font-src", "wss://live.example.com"}, {"connect-src", "ws://live.example.com"},
		{"connect-src", "https://a.example.com\r\nSet-Cookie: x=1"}, {"connect-src", "https://a.example.com;script-src *"},
		{"connect-src", "https://a.example.com; script-src *"}, {"connect-src", "https://a.example.com,https://b.example.com"},
		{"connect-src", "https://a.example.com https://b.example.com"}, {"connect-src", "https://a.example.com\x00"},
		{"connect-src", "https://a.example.com\t"}, {"connect-src", `https://a.example.com"`}, {"connect-src", "https://a.example.com'"},
		{"connect-src", ""}, {"connect-src", "api.github.com"}, {"connect-src", "https://" + strings.Repeat("a", 250) + ".com"},
	}
	for _, c := range bad {
		if got, err := cspValidateExtra(c.dir, c.in); err == nil {
			t.Errorf("%s %q accepted as %q", c.dir, c.in, got)
		}
	}
}

func TestCSPNormalizeExtraLimitsAndDuplicates(t *testing.T) {
	var many []string
	for i := 0; i < 25; i++ {
		many = append(many, "https://h"+string(rune('a'+i))+".example.com")
	}
	out, issues := cspNormalizeExtra(map[string][]string{"connect-src": {"https://api.github.com", " https://API.github.com/ ", ""}, "font-src": many})
	if got := out["connect-src"]; len(got) != 1 || got[0] != "https://api.github.com" {
		t.Errorf("duplicates kept: %v", got)
	}
	if len(issues) != 1 || issues[0].Directive != "font-src" || len(out["font-src"]) != cspExtraPerDirective {
		t.Errorf("limit: %v, %d kept", issues, len(out["font-src"]))
	}
}

// The front-end policy: unchanged byte for byte without sources; connect-src (absent from the policy)
// starts from 'self'; admin, login and REST responses are not touched; the page cache does not serve
// a page with the policy it was stored under.
func TestCSPExtraHeader(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if err := srv.settings.Set(ctx, map[string]any{"wpgo_cache_enabled": true}); err != nil {
		t.Fatal(err)
	}
	srv.configureCache()
	front := func() string {
		t.Helper()
		resp := anonGet(t, ts.URL+"/")
		_ = bodyOf(resp)
		return resp.Header.Get("Content-Security-Policy")
	}
	const base = "default-src 'self'; img-src 'self' data: https:; media-src 'self' https:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; " +
		"script-src 'self' 'unsafe-inline'; frame-src 'self' https:; object-src 'none'; base-uri 'self'; frame-ancestors 'self'; form-action 'self'"
	if p := front(); p != base {
		t.Fatalf("policy without sources changed:\n%s\nwant\n%s", p, base)
	}
	if p := front(); p != base { // cached copy
		t.Fatalf("cached policy changed: %s", p)
	}
	adminBefore := func() string {
		resp, err := c.Get(ts.URL + "/wp-admin/")
		if err != nil {
			t.Fatal(err)
		}
		_ = bodyOf(resp)
		return resp.Header.Get("Content-Security-Policy")
	}()

	if err := srv.settings.Set(ctx, map[string]any{cspExtraOption: map[string]any{"connect-src": []any{"https://api.github.com"}, "font-src": []any{"https://fonts.gstatic.com"}}}); err != nil {
		t.Fatal(err)
	}
	p := front()
	want := strings.Replace(base, "font-src 'self' data:", "font-src 'self' data: https://fonts.gstatic.com", 1) + "; connect-src 'self' https://api.github.com"
	if p != want {
		t.Fatalf("policy with sources (cache %v):\n%s\nwant\n%s", srv.cspCacheState(), p, want)
	}
	if ro := anonGet(t, ts.URL+"/").Header.Get("Content-Security-Policy-Report-Only"); strings.Contains(ro, "github") {
		t.Fatalf("report-only policy got the sources: %s", ro)
	}
	resp, err := c.Get(ts.URL + "/wp-admin/")
	if err != nil {
		t.Fatal(err)
	}
	_ = bodyOf(resp)
	if a := resp.Header.Get("Content-Security-Policy"); a != adminBefore || strings.Contains(a, "github") {
		t.Fatalf("admin policy changed: %s", a)
	}
	for _, u := range []string{"/wp-login", "/wp-json/"} {
		r := anonGet(t, ts.URL+u)
		_ = bodyOf(r)
		if strings.Contains(r.Header.Get("Content-Security-Policy"), "github") {
			t.Errorf("%s got the sources", u)
		}
	}

	// A stored value written around the validator never reaches the header raw.
	if err := srv.settings.Set(ctx, map[string]any{cspExtraOption: map[string]any{"script-src": []any{"*"}, "connect-src": []any{"https://a.example.com; script-src *", "*"}}}); err != nil {
		t.Fatal(err)
	}
	if p := front(); p != base {
		t.Fatalf("invalid stored sources reached the header: %s", p)
	}
}

// REST and MCP: manage_options only, invalid input is 400 with nothing saved, writes are logged.
func TestCSPExtraRESTAndMCP(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()

	code, body := restSend(t, c, ts.URL, http.MethodPost, "/wp-json/pursae/v1/csp", `{"sources":{"connect-src":["https://api.github.com"]}}`)
	if code != 200 || !strings.Contains(body, `"connect-src":["https://api.github.com"]`) {
		t.Fatalf("save: %d %s", code, body)
	}
	code, body = restSend(t, c, ts.URL, http.MethodPost, "/wp-json/pursae/v1/csp", `{"sources":{"connect-src":["https://api.github.com","https://ok.example.com"],"font-src":["http://bad.example.com"]}}`)
	if code != 400 || !strings.Contains(body, "http://bad.example.com") || !strings.Contains(body, `"invalid"`) {
		t.Fatalf("invalid: %d %s", code, body)
	}
	if got := srv.cspExtraSources()["connect-src"]; len(got) != 1 {
		t.Fatalf("partial save after 400: %v", got)
	}
	if code, body := restSend(t, c, ts.URL, http.MethodPost, "/wp-json/pursae/v1/csp", `{"sources":{"script-src":["https://cdn.example.com"]}}`); code != 400 {
		t.Fatalf("script-src: %d %s", code, body)
	}
	if code, _ := restSend(t, c, ts.URL, http.MethodPost, "/wp-json/pursae/v1/csp", `{"sources":"x"}`); code != 400 {
		t.Fatalf("bad shape: %d", code)
	}
	code, body = restGet(t, c, ts.URL, "/wp-json/pursae/v1/csp")
	if code != 200 || !strings.Contains(body, `"font-src":[]`) || !strings.Contains(body, "https://api.github.com") {
		t.Fatalf("get: %d %s", code, body)
	}

	// An editor gets 403 and does not see the tools.
	uid, err := srv.store.CreateUser(ctx, store.NewUser{Login: "ed", Email: "ed@example.test", PasswordHash: "x", Role: "editor"})
	if err != nil {
		t.Fatal(err)
	}
	edPass, _, err := srv.createAppPassword(ctx, uid, "ed agent", "", appScopeFullAccess)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		req, _ := http.NewRequest(m, ts.URL+"/wp-json/pursae/v1/csp", strings.NewReader(`{"sources":{"connect-src":["https://x.example.com"]}}`))
		req.SetBasicAuth("ed", edPass)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = bodyOf(resp)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("editor %s: %d", m, resp.StatusCode)
		}
	}

	admin, err := srv.store.UserByLoginOrEmail(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	adminPass, _, err := srv.createAppPassword(ctx, admin.ID, "CSP agent", "", "settings")
	if err != nil {
		t.Fatal(err)
	}
	contentPass, _, err := srv.createAppPassword(ctx, admin.ID, "Content agent", "") // the default scopes: content and media
	if err != nil {
		t.Fatal(err)
	}
	tools := func(base, login, pass string) map[string]bool {
		b := mcpRPCAs(t, base, login, pass, "tools/list", nil)
		out := map[string]bool{}
		res, _ := b["result"].(map[string]any)
		list, _ := res["tools"].([]any)
		for _, x := range list {
			out[x.(map[string]any)["name"].(string)] = true
		}
		return out
	}
	if ed := tools(ts.URL, "ed", edPass); ed["csp_get_settings"] || ed["csp_update_settings"] {
		t.Error("editor offered the csp tools")
	}
	if ct := tools(ts.URL, "admin", contentPass); ct["csp_get_settings"] || ct["csp_update_settings"] {
		t.Error("a content-scoped password offered the csp tools")
	}
	if ad := tools(ts.URL, "admin", adminPass); !ad["csp_get_settings"] || !ad["csp_update_settings"] {
		t.Fatal("administrator lacks the csp tools")
	}
	res := mcpRPCAs(t, ts.URL, "admin", adminPass, "tools/call", map[string]any{"name": "csp_update_settings",
		"arguments": map[string]any{"sources": map[string]any{"connect-src": []any{"https://api.github.com", "wss://live.example.com"}}}})
	if r, _ := res["result"].(map[string]any); r == nil || r["isError"] == true {
		t.Fatalf("csp_update_settings: %v", res)
	}
	if got := srv.cspExtraSources()["connect-src"]; len(got) != 2 {
		t.Fatalf("MCP save: %v", got)
	}
	res = mcpRPCAs(t, ts.URL, "admin", adminPass, "tools/call", map[string]any{"name": "csp_update_settings",
		"arguments": map[string]any{"sources": map[string]any{"connect-src": []any{"https://*"}}}})
	if r, _ := res["result"].(map[string]any); r == nil || r["isError"] != true || !strings.Contains(mcpText(r), "Nothing was saved") {
		t.Fatalf("invalid MCP call: %v", res)
	}
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM mcp_activity WHERE tool = 'csp_update_settings' AND route = '/pursae/v1/csp' AND client = 'Application Password: CSP agent'`).Scan(&n)
	if n < 1 {
		t.Error("csp_update_settings not in the MCP activity log")
	}
}

// Settings › Security: the sources save with the form; an invalid line saves nothing and is shown
// beside its field.
func TestCSPExtraAdminForm(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	_, page, _ := get(t, c, ts.URL+"/wp-admin/options-security")
	if !strings.Contains(page, `name="csp_extra_connect-src"`) || !strings.Contains(page, "Only add services you trust") {
		t.Fatal("the screen lacks the allowed external sources")
	}
	m := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no nonce")
	}
	nonce := m[1]
	post := func(conn string) (int, string) {
		form := url.Values{"_wpnonce": {nonce}, "csp_mode": {"report"}, "csp_admin_scripts": {"0", "1"}, "csp_extra_connect-src": {conn}, "csp_extra_font-src": {""}, "csp_extra_style-src": {""}}
		resp, err := c.PostForm(ts.URL+"/wp-admin/options-security", form)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, bodyOf(resp)
	}
	if _, body := post("https://api.github.com\r\nhttps://*.example.com"); strings.Contains(body, "notice-error") {
		t.Fatalf("valid save failed: %s", body)
	}
	if got := srv.cspExtraSources()["connect-src"]; len(got) != 2 {
		t.Fatalf("saved: %v", got)
	}
	_, body := post("https://api.github.com\nhttp://plain.example.com")
	if !strings.Contains(body, "http://plain.example.com: only https://") || !strings.Contains(body, `aria-invalid="true"`) {
		t.Fatalf("error not shown beside the field: %s", body)
	}
	if got := srv.cspExtraSources()["connect-src"]; len(got) != 2 {
		t.Fatalf("invalid submit saved: %v", got)
	}
}

// mcpRPCAs sends one JSON-RPC request to the MCP endpoint as login with an Application Password.
func mcpRPCAs(t *testing.T, base, login, pass, method string, params map[string]any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, _ := http.NewRequest("POST", base+"/wp-json/pursae/v1/mcp", bytes.NewReader(b))
	req.SetBasicAuth(login, pass)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}
