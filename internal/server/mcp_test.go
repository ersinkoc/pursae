package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMCPToolsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range mcpTools {
		if seen[tool.Name] {
			t.Errorf("duplicate tool %s", tool.Name)
		}
		seen[tool.Name] = true
		if tool.Desc == "" {
			t.Errorf("%s: no description", tool.Name)
		}
		if _, err := json.Marshal(tool.schema()); err != nil {
			t.Errorf("%s: schema: %v", tool.Name, err)
		}
		props := tool.schema()["properties"].(map[string]any)
		for _, m := range mcpPathArg.FindAllStringSubmatch(tool.Path, -1) {
			if _, ok := props[m[1]]; !ok {
				t.Errorf("%s: path argument %s is not a property", tool.Name, m[1])
			}
		}
		for _, r := range tool.Required {
			if _, ok := props[r]; !ok {
				t.Errorf("%s: required %s is not a property", tool.Name, r)
			}
		}
		if tool.special == "" && appScopeForRoute(mcpPathArg.ReplaceAllString(tool.Path, "1")) == "" && tool.Path != "/wp/v2/{taxonomy}" && tool.Path != "/wp/v2/{taxonomy}/{id}" {
			t.Errorf("%s: route %s belongs to no Application Password scope", tool.Name, tool.Path)
		}
	}
}

func TestMCPDocsResources(t *testing.T) {
	list := mcpDocs()
	if len(list) < 10 {
		t.Fatalf("only %d docs", len(list))
	}
	if _, ok := mcpReadDoc(list[0].uri); !ok {
		t.Errorf("cannot read %s", list[0].uri)
	}
	for _, bad := range []string{"pursae-docs://developer/../embed.go", "pursae-docs://embed.go", "file:///etc/passwd", "pursae-docs://developer/_sections.yaml"} {
		if _, ok := mcpReadDoc(bad); ok {
			t.Errorf("read %s", bad)
		}
	}
}

func TestMCPHandleProtocol(t *testing.T) {
	s := &Server{}
	call := func(method string, params any) *mcpResponse {
		raw, _ := json.Marshal(params)
		return s.mcpHandle(nil, &mcpRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: method, Params: raw})
	}
	if r := call("initialize", map[string]any{"protocolVersion": "1999-01-01"}); r.Error != nil || r.Result.(map[string]any)["protocolVersion"] != mcpProtocolVersions[0] {
		t.Errorf("initialize with an unknown version = %+v", r)
	}
	if r := call("nope", nil); r.Error == nil || r.Error.Code != -32601 {
		t.Errorf("unknown method = %+v", r)
	}
	if r := call("tools/call", map[string]any{"name": "nope"}); r.Error == nil {
		t.Errorf("unknown tool = %+v", r)
	}
	if r := s.mcpHandle(nil, &mcpRequest{JSONRPC: "2.0", Method: "notifications/initialized"}); r != nil {
		t.Errorf("notification answered: %+v", r)
	}
}

// A tool's arguments cannot change its HTTP method or slip in parameters its schema does not have:
// list_posts / get_post with _method=POST or DELETE would otherwise write through a read-only tool.
func TestMCPCallRefusesMethodOverride(t *testing.T) {
	s := &Server{}
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"list_posts", map[string]any{"_method": "POST", "title": "x", "status": "publish"}},
		{"get_post", map[string]any{"id": float64(1), "_method": "DELETE", "force": true}},
		{"read_theme_file", map[string]any{"slug": "twentytwentyfive", "file": "style.css", "_METHOD": "DELETE"}},
		{"get_post", map[string]any{"id": float64(1), "force": true}}, // not in the schema
	} {
		res := s.mcpCall(nil, mcpToolByName(tc.tool), tc.args)
		if res["isError"] != true {
			t.Errorf("%s %v accepted", tc.tool, tc.args)
		}
	}
}

func TestMCPRestRouteRejectsEncodedTraversal(t *testing.T) {
	good, ok := mcpRestRoute("/wp/v2/comments?per_page=5")
	if !ok || good != "/wp/v2/comments?per_page=5" {
		t.Fatalf("good path: %q %v", good, ok)
	}
	if good, ok = mcpRestRoute("/wp-json/wp/v2/posts/"); !ok || good != "/wp/v2/posts/" {
		t.Fatalf("wp-json prefix: %q %v", good, ok)
	}
	for _, p := range []string{
		"/wp/v2/../../wp-admin/",
		"/wp/v2/%2e%2e/%2e%2e/wp-admin/",
		"/wp/v2/%2e%2e%2f%2e%2e%2fwp-admin/",
		"/wp/v2/%252e%252e/%252e%252e/wp-admin/",
		"/wp/v2/posts/%2E%2E/settings",
		"/wp/v2/posts/..%2f..%2fwp-admin/",
		`/wp/v2/\posts`,
		"//evil.example/wp/v2/posts",
		"/pursae/v1/mcp",
		"/pursae/v1/mcp/../options",
		"/%70ursae/v1/mcp",
		"/wp/v2/posts/%00",
		"/wp/v2/./posts",
		"",
		"wp/v2/posts",
	} {
		if _, ok := mcpRestRoute(p); ok {
			t.Errorf("accepted %s", p)
		}
	}
	res := (&Server{}).mcpCall(nil, mcpToolByName("rest_request"), map[string]any{"method": "GET", "path": "/wp/v2/%2e%2e/wp-admin/"})
	if res["isError"] != true {
		t.Fatalf("mcpCall accepted encoded traversal: %v", res)
	}
}

func TestMCPToolsCallFiltersUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/wp-json/pursae/v1/mcp", nil)
	c.Set(restAppScopesKey, []string{"content"})
	s := &Server{}
	if !s.mcpToolAvailable(c, mcpToolByName("list_posts")) {
		t.Fatal("content scope should allow list_posts")
	}
	if s.mcpToolAvailable(c, mcpToolByName("list_plugins")) {
		t.Fatal("content scope should hide list_plugins")
	}
	raw, _ := json.Marshal(map[string]any{"name": "list_plugins", "arguments": map[string]any{}})
	r := s.mcpHandle(c, &mcpRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: raw})
	if r == nil || r.Error == nil || r.Error.Code != -32602 {
		t.Fatalf("tools/call ran an out-of-scope tool: %+v", r)
	}
}

func TestMCPToolAnnotations(t *testing.T) {
	for name, want := range map[string][2]bool{ // readOnly, destructive
		"list_posts": {true, false}, "seo_analyze": {true, false}, "create_post": {false, false}, "upload_media": {false, false},
		"update_post": {false, true}, "delete_post": {false, true}, "update_settings": {false, true}, "write_theme_file": {false, true},
		"activate_plugin": {false, true}, "install_plugin": {false, true}, "rest_request": {false, true},
	} {
		a := mcpToolByName(name).annotations()
		if a["readOnlyHint"] != want[0] || a["destructiveHint"] != want[1] {
			t.Errorf("%s annotations = %v, want readOnly %v destructive %v", name, a, want[0], want[1])
		}
	}
}
