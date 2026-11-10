package cache

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type warmupRoundTrip func(*http.Request) (*http.Response, error)

func (f warmupRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type warmupBrokenBody struct{ read bool }

func (b *warmupBrokenBody) Read(p []byte) (int, error) {
	if !b.read {
		b.read = true
		p[0] = 'x'
		return 1, nil
	}
	return 0, errors.New("body interrupted")
}
func (*warmupBrokenBody) Close() error { return nil }

func TestWarmupInProcess(t *testing.T) {
	c, app, _ := newTestCache(t, nil)
	urls := []string{"https://example.com/", "https://example.com/a/", "https://example.com/b/", "/wp-admin/", "https://example.com/missing"}
	var calls int
	prog, err := c.Warm(context.Background(), app, urls, WarmupOptions{
		Concurrency: 3,
		BaseURL:     "https://example.com",
		Progress:    func(WarmupProgress) { calls++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	want := WarmupProgress{Total: 5, Done: 5, Warmed: 4, Skipped: 1}
	if prog != want || calls != 5 {
		t.Fatalf("progress %+v (callbacks %d)", prog, calls)
	}
	// Pages are now cached under the https key.
	r := httptest.NewRequest("GET", "http://example.com/a/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	c.Middleware(app).ServeHTTP(w, r)
	if w.Header().Get(HeaderStatus) != "hit" {
		t.Fatalf("not warmed: %q", w.Header().Get(HeaderStatus))
	}
	// A second run finds everything cached.
	prog, _ = c.Warm(context.Background(), app, urls[:3], WarmupOptions{})
	if prog.AlreadyCached != 3 {
		t.Fatalf("second run %+v", prog)
	}
}

func TestWarmupRateLimitAndCancel(t *testing.T) {
	c, app, _ := newTestCache(t, nil)
	urls := make([]string, 50)
	for i := range urls {
		urls[i] = "http://example.com/p" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "/"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	prog, err := c.Warm(ctx, app, urls, WarmupOptions{RatePerSecond: 50, Concurrency: 2})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if prog.Done == 0 || prog.Done >= 20 {
		t.Fatalf("rate limit not applied: %+v", prog)
	}
}

func TestWarmupOverNetwork(t *testing.T) {
	c, app, h := newTestCache(t, nil)
	srv := httptest.NewServer(h)
	defer srv.Close()
	prog, err := Warmup(context.Background(), []string{"/x/", "/y/"}, WarmupOptions{BaseURL: srv.URL, Client: srv.Client()})
	if err != nil || prog.Warmed != 2 {
		t.Fatalf("%+v %v", prog, err)
	}
	if app.calls.Load() != 2 || c.Stats().Entries != 2 {
		t.Fatalf("calls=%d entries=%d", app.calls.Load(), c.Stats().Entries)
	}
}

func TestWarmupCountsResponseBodyFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		body io.ReadCloser
		fail int
		warm int
	}{
		{name: "complete", body: io.NopCloser(http.NoBody), warm: 1},
		{name: "interrupted", body: &warmupBrokenBody{}, fail: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: warmupRoundTrip(func(*http.Request) (*http.Response, error) {
				h := http.Header{}
				h.Set(HeaderStatus, "miss")
				return &http.Response{StatusCode: http.StatusOK, Header: h, Body: tc.body}, nil
			})}
			prog, err := Warmup(context.Background(), []string{"https://example.test/"}, WarmupOptions{Client: client, Concurrency: 1})
			if err != nil || prog.Failed != tc.fail || prog.Warmed != tc.warm || prog.Done != 1 {
				t.Fatalf("progress=%+v err=%v", prog, err)
			}
		})
	}
}
