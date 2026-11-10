package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthzAndRequestID(t *testing.T) {
	s := &Server{}
	var seen string
	h := s.opsHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get(HeaderRequestID)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("/healthz: %d %q %q", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /healthz: %d", rec.Code)
	}

	// A sane incoming ID is kept; a missing or hostile one is replaced.
	for in, keep := range map[string]bool{"abc-123": true, "": false, "bad id\r\n": false, strings.Repeat("a", 200): false} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if in != "" {
			req.Header.Set(HeaderRequestID, in)
		}
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		got := rec.Header().Get(HeaderRequestID)
		if got == "" || got != seen {
			t.Fatalf("%q: response ID %q, handler saw %q", in, got, seen)
		}
		if (got == in) != keep {
			t.Errorf("incoming %q: got %q, keep=%v", in, got, keep)
		}
	}
}

// TestBodyIdleTimeout: a client that stops sending its body fails after bodyIdleTimeout, while a
// handler that keeps working after reading the whole body is not cancelled by the deadline.
func TestBodyIdleTimeout(t *testing.T) {
	old := bodyIdleTimeout
	bodyIdleTimeout = 150 * time.Millisecond
	t.Cleanup(func() { bodyIdleTimeout = old })
	s := &Server{}
	type result struct {
		readErr error
		ctxErr  error
	}
	results := make(chan result, 1)
	ts := httptest.NewServer(s.opsHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err == nil {
			time.Sleep(3 * bodyIdleTimeout) // slow work after the body
		}
		results <- result{err, r.Context().Err()}
	})))
	defer ts.Close()

	send := func(raw string) net.Conn {
		conn, err := net.Dial("tcp", ts.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(conn, raw)
		return conn
	}
	// Stalled upload.
	conn := send("POST /up HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\nabc")
	defer conn.Close()
	select {
	case r := <-results:
		if r.readErr == nil {
			t.Fatal("stalled body read did not fail")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stalled body still blocks the handler")
	}

	// Complete body, slow handler.
	conn2 := send("POST /up HTTP/1.1\r\nHost: x\r\nContent-Length: 3\r\n\r\nabc")
	defer conn2.Close()
	select {
	case r := <-results:
		if r.readErr != nil || r.ctxErr != nil {
			t.Fatalf("complete body: read %v, context %v", r.readErr, r.ctxErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not finish")
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn2), nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("response after slow work: %v %v", resp, err)
	}
}

func TestReadyz(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)
	h := srv.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != 200 {
		t.Fatalf("/readyz: %d %s", rec.Code, rec.Body.String())
	}
	// Probes answer in maintenance mode too and are never cached.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != 200 || rec.Header().Get("X-WPGo-Cache") != "" {
		t.Fatalf("/healthz through the site handler: %d %v", rec.Code, rec.Header())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz with an unusable database context: %d", rec.Code)
	}
}

// Every request body is bounded, declared or streamed; the Backup Manager import is not.
func TestRequestBodyLimit(t *testing.T) {
	old := maxRequestBody
	maxRequestBody = 1 << 10
	t.Cleanup(func() { maxRequestBody = old })
	s := &Server{}
	var read int64
	var readErr error
	h := s.opsHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read, readErr = io.Copy(io.Discard, r.Body)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/wp-login", strings.NewReader(strings.Repeat("a", 2<<10))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("declared oversize body: %d", rec.Code)
	}

	read, readErr = 0, nil
	req := httptest.NewRequest(http.MethodPost, "/wp-comments-post", io.MultiReader(strings.NewReader(strings.Repeat("a", 4<<10))))
	req.ContentLength = -1 // chunked: the size is not known up front
	h.ServeHTTP(httptest.NewRecorder(), req)
	if readErr == nil || read > 1<<10 {
		t.Errorf("streamed oversize body: read %d, err %v", read, readErr)
	}

	read, readErr = 0, nil
	req = httptest.NewRequest(http.MethodPost, "/wp-admin/admin?page=backup-manager&action=upload&_wpnonce=x", strings.NewReader(strings.Repeat("a", 4<<10)))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if readErr != nil || read != 4<<10 {
		t.Errorf("backup import: read %d, err %v", read, readErr)
	}
}
