package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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

func TestRedactRequestURI(t *testing.T) {
	plain := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts?per_page=10&page=2", nil)
	if got := redactRequestURI(plain.URL); got != plain.URL.RequestURI() {
		t.Errorf("URI without credentials changed: %q", got)
	}
	secret := httptest.NewRequest(http.MethodGet, "/wp-json/wc/v3/orders?consumer_key=ck_abc&consumer_secret=cs_supersecret&page=2", nil)
	got := redactRequestURI(secret.URL)
	if strings.Contains(got, "cs_supersecret") || !strings.Contains(got, "consumer_secret=redacted") {
		t.Fatalf("consumer_secret leaked: %q", got)
	}
	if !strings.Contains(got, "consumer_key=ck_abc") || !strings.Contains(got, "page=2") {
		t.Fatalf("non-secret parameters dropped: %q", got)
	}
	keyed := httptest.NewRequest(http.MethodGet, "/checkout/order-received/9/?key=wc_order_secret", nil)
	if got := redactRequestURI(keyed.URL); strings.Contains(got, "wc_order_secret") || !strings.Contains(got, "key=redacted") {
		t.Fatalf("order key leaked: %q", got)
	}
}

// Credentials Pursae itself puts in URLs: OAuth callbacks, previews, privacy and admin e-mail
// confirmations, the Web Builder live frame and nonces.
func TestRedactRequestURIPursaeCredentials(t *testing.T) {
	for _, q := range []string{
		"code=oauth-secret&state=state-secret",
		"previewToken=oauth-secret",
		"action=confirmaction&request_id=3&confirm_key=oauth-secret",
		"adminhash=oauth-secret",
		"wb-live-frame=oauth-secret",
		"_wpnonce=oauth-secret",
		"_ajax_nonce=oauth-secret",
		"_wcsnonce=oauth-secret",
		"preview_id=4&preview_nonce=oauth-secret&preview=true",
		"token=oauth-secret",
		"%5Fwpnonce=oauth-secret", // escaped key
		"TOKEN=oauth-secret",
	} {
		u := &url.URL{Path: "/x", RawQuery: q}
		got := redactRequestURI(u)
		if strings.Contains(got, "secret") || !strings.Contains(got, "=redacted") {
			t.Errorf("%q: credential leaked: %q", q, got)
		}
	}
	u := &url.URL{Path: "/x", RawQuery: "preview_id=4&preview_nonce=n&preview=true"}
	if got, want := redactRequestURI(u), "/x?preview_id=4&preview_nonce=redacted&preview=true"; got != want {
		t.Errorf("got %q, want %q (order and other parameters kept)", got, want)
	}
}

// A query url.ParseQuery rejects (a bad escape, a semicolon) is still redacted.
func TestRedactRequestURIMalformedQuery(t *testing.T) {
	for q, want := range map[string]string{
		"a=%zz&token=abc":        "/x?a=%zz&token=redacted",
		"token=abc&a=%zz":        "/x?token=redacted&a=%zz",
		"a=1;key=abc":            "/x?a=1;key=redacted",
		"to%6Ben=abc&b=%":        "/x?to%6Ben=redacted&b=%",
		"%zz=1&_wpnonce=abc&c":   "/x?%zz=1&_wpnonce=redacted&c",
		"code=abc%zz&state=s%zz": "/x?code=redacted&state=redacted",
	} {
		u := &url.URL{Path: "/x", RawQuery: q}
		if _, err := url.ParseQuery(q); err == nil {
			t.Fatalf("%q parses; the test wants a malformed query", q)
		}
		if got := redactRequestURI(u); got != want {
			t.Errorf("%q: got %q, want %q", q, got, want)
		}
	}
}

// Read and Close of a request body run on different goroutines (the handler reads, the server
// closes when the request is canceled). idleBody's done flag must not be a data race.
func TestIdleBodyReadCloseRace(t *testing.T) {
	rec := httptest.NewRecorder()
	body := &idleBody{
		ReadCloser: io.NopCloser(strings.NewReader(strings.Repeat("a", 1<<16))),
		rc:         http.NewResponseController(rec),
		idle:       time.Second,
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := make([]byte, 32)
		for {
			if _, err := body.Read(buf); err != nil {
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		_ = body.Close()
	}()
	wg.Wait()
}
