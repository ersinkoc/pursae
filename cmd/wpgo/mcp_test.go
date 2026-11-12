package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
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

// TestMCPBridgeTransport: the password comes from the environment (the flag still works, with a warning),
// and plain http is refused except to this machine.
func TestMCPBridgeTransport(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, p, _ := r.BasicAuth(); p != "from env" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{}})
	}))
	defer ts.Close()
	t.Setenv("PURSAE_APP_PASSWORD", "from env")
	var out, errs bytes.Buffer
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")
	if err := runMCP(context.Background(), []string{"--url", ts.URL, "--user", "admin"}, in, &out, &errs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"result"`) || strings.Contains(errs.String(), "warning") {
		t.Errorf("env password: out=%q errs=%q", out.String(), errs.String())
	}
	errs.Reset()
	_ = runMCP(context.Background(), []string{"--url", ts.URL, "--user", "admin", "--password", "x"}, strings.NewReader(""), &out, &errs)
	if !strings.Contains(errs.String(), "PURSAE_APP_PASSWORD") {
		t.Errorf("no warning for --password: %q", errs.String())
	}
	for _, u := range []string{"http://example.com", "http://10.0.0.5:8088", "ftp://example.com"} {
		if err := runMCP(context.Background(), []string{"--url", u, "--user", "admin"}, strings.NewReader(""), &out, &errs); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
	for _, u := range []string{"https://example.com", "http://localhost:8088", "http://[::1]:8088", "http://127.0.0.1"} {
		if err := runMCP(context.Background(), []string{"--url", u, "--user", "admin"}, strings.NewReader(""), &out, &errs); err != nil {
			t.Errorf("%s refused: %v", u, err)
		}
	}
}

// TestMCPBridgeSessionStream: the bridge keeps the session the site opens at initialize, sends its id
// with later messages, prints what the site pushes on the GET event stream (resuming with
// Last-Event-ID after a drop) and ends the session when stdin closes.
func TestMCPBridgeSessionStream(t *testing.T) {
	old := mcpStreamRetry
	mcpStreamRetry = 10 * time.Millisecond
	defer func() { mcpStreamRetry = old }()
	var mu sync.Mutex
	var streams, deleted int
	var resumedFrom, postSession string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.Header.Get("Mcp-Session-Id") != "sess-1" || r.Header.Get("Accept") != "text/event-stream" {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			mu.Lock()
			streams++
			n := streams
			if n == 2 {
				resumedFrom = r.Header.Get("Last-Event-ID")
			}
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			if n == 1 { // one message, then the stream drops
				_, _ = io.WriteString(w, ": keep-alive\n\nid: 7\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n")
				return
			}
			_, _ = io.WriteString(w, "id: 8\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/second\"}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case http.MethodDelete:
			mu.Lock()
			if r.Header.Get("Mcp-Session-Id") == "sess-1" {
				deleted++
			}
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			if m["method"] == "initialize" {
				w.Header().Set("Mcp-Session-Id", "sess-1")
			} else {
				mu.Lock()
				postSession = r.Header.Get("Mcp-Session-Id")
				mu.Unlock()
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": map[string]any{}})
		}
	}))
	defer ts.Close()
	pr, pw := io.Pipe()
	out := &syncBuf{}
	done := make(chan error, 1)
	go func() {
		done <- runMCP(context.Background(), []string{"--url", ts.URL, "--user", "admin", "--password", "x"}, pr, out, io.Discard)
	}()
	_, _ = io.WriteString(pw, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`+"\n")
	_, _ = io.WriteString(pw, `{"jsonrpc":"2.0","id":2,"method":"ping"}`+"\n")
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "notifications/second") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "notifications/tools/list_changed") || !strings.Contains(got, "notifications/second") {
		t.Fatalf("pushed messages missing: %q", got)
	}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if !json.Valid([]byte(line)) {
			t.Errorf("not one JSON message per line: %q", line)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if postSession != "sess-1" {
		t.Errorf("session id on later POST = %q", postSession)
	}
	if resumedFrom != "7" {
		t.Errorf("Last-Event-ID on resume = %q, want 7", resumedFrom)
	}
	if deleted != 1 {
		t.Errorf("session DELETE calls = %d, want 1", deleted)
	}
}
