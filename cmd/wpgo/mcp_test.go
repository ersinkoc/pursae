package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMCPBridge checks that `wpgo mcp` forwards stdio messages with Basic auth, prints answers one per
// line, stays quiet for notifications and turns HTTP failures into JSON-RPC errors.
func TestMCPBridge(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wp-json/pursae/v1/mcp" {
			http.NotFound(w, r)
			return
		}
		if u, p, ok := r.BasicAuth(); !ok || u != "admin" || p != "abcd efgh" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"code":"incorrect_password","message":"The provided password is an invalid application password."}`)
			return
		}
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		if _, ok := m["id"]; !ok {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": map[string]any{"echo": m["method"]}})
	}))
	defer ts.Close()

	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":"two","method":"tools/list"}` + "\n")
	var out, errs bytes.Buffer
	if err := runMCP(context.Background(), []string{"--url", ts.URL + "/", "--user", "admin", "--password", "abcd efgh"}, in, &out, &errs); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"echo":"initialize"`) || !strings.Contains(lines[1], `"id":"two"`) {
		t.Fatalf("output = %q", out.String())
	}

	out.Reset()
	in = strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"ping"}` + "\n")
	if err := runMCP(context.Background(), []string{"--url", ts.URL, "--user", "admin", "--password", "wrong"}, in, &out, &errs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"id":3`) || !strings.Contains(out.String(), "invalid application password") {
		t.Errorf("auth failure output = %q", out.String())
	}
	if err := runMCP(context.Background(), nil, strings.NewReader(""), &out, &errs); err == nil {
		t.Error("missing flags accepted")
	}
}
