package server

import (
	"encoding/json"
	"testing"
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
