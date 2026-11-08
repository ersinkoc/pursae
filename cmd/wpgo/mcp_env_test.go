package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMCPBridgeEnvAndTransportErrors: the PURSAE_* variables stand in for the flags, a server that
// cannot be reached answers requests with a JSON-RPC error (never a silent hang) and drops
// notifications, and a non-JSON error body is passed on as the message.
func TestMCPBridgeEnvAndTransportErrors(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		gotAuth = u + ":" + p
		if r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream down"))
	}))
	defer ts.Close()
	t.Setenv("PURSAE_URL", ts.URL)
	t.Setenv("PURSAE_USER", "agent")
	t.Setenv("PURSAE_APP_PASSWORD", "abcd efgh ijkl")
	var out, errs bytes.Buffer
	in := strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"tools/list"}` + "\n" + "   \n")
	if err := runMCP(context.Background(), nil, in, &out, &errs); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "agent:abcd efgh ijkl" {
		t.Errorf("Basic auth from the environment = %q", gotAuth)
	}
	var resp struct {
		ID    int `json:"id"`
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil || resp.ID != 7 || resp.Error.Code != -32000 || !strings.Contains(resp.Error.Message, "upstream down") {
		t.Fatalf("502 answer = %q (%v)", out.String(), err)
	}

	// Nothing listens: requests get an error, notifications nothing.
	ts.Close()
	out.Reset()
	in = strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" + `{"jsonrpc":"2.0","id":"x","method":"ping"}`)
	if err := runMCP(context.Background(), []string{"--url", ts.URL}, in, &out, &errs); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"id":"x"`) || !strings.Contains(lines[0], `-32000`) {
		t.Errorf("unreachable server output = %q", out.String())
	}
}
