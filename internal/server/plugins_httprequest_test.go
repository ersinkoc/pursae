package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"wpgo/internal/plugins"
)

func TestPluginHostHTTPRequestPost(t *testing.T) {
	var method, auth, body string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		method, auth, body = r.Method, r.Header.Get("Authorization"), string(b)
		w.WriteHeader(201)
		_, _ = w.Write([]byte("done"))
	}))
	defer ts.Close()
	old := pluginHTTP
	pluginHTTP = ts.Client() // loopback is blocked by the production client
	defer func() { pluginHTTP = old }()

	h := &pluginHost{}
	resp, err := h.HTTPRequest(context.Background(), "demo", plugins.HTTPRequest{
		Method: "POST", URL: ts.URL, Headers: map[string]string{"Authorization": "Bearer t"}, Body: `{"a":1}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if method != "POST" || auth != "Bearer t" || body != `{"a":1}` || resp.Status != 201 || resp.Body != "done" {
		t.Errorf("method=%s auth=%s body=%s resp=%+v", method, auth, body, resp)
	}
}
