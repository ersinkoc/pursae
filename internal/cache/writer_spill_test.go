package cache

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

var errSpillWrite = errors.New("injected write failure")

type spillTestWriter struct {
	header http.Header
	mode   string
	writes int
}

func (w *spillTestWriter) Header() http.Header { return w.header }
func (w *spillTestWriter) WriteHeader(int)     {}
func (w *spillTestWriter) Write(b []byte) (int, error) {
	w.writes++
	if w.writes == 1 {
		switch w.mode {
		case "error":
			return 0, errSpillWrite
		case "short":
			return len(b) - 1, nil
		}
	}
	return len(b), nil
}

func TestSpillPropagatesBufferedWriteError(t *testing.T) {
	for _, tc := range []struct {
		mode   string
		want   error
		writes int
	}{{"", nil, 2}, {"error", errSpillWrite, 1}, {"short", io.ErrShortWrite, 1}} {
		cfg := DefaultConfig()
		cfg.MaxEntrySize = 4
		c := New(cfg, Options{})
		var got error
		h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			if _, got = w.Write([]byte("abcd")); got != nil {
				return
			}
			_, got = w.Write([]byte("ef"))
		}))
		w := &spillTestWriter{header: http.Header{}, mode: tc.mode}
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
		if !errors.Is(got, tc.want) || w.writes != tc.writes {
			t.Errorf("mode %q: error=%v writes=%d; want error=%v writes=%d", tc.mode, got, w.writes, tc.want, tc.writes)
		}
	}
}

func TestFlushStreamsCapturedHTML(t *testing.T) {
	for _, tc := range []struct{ contentType, first string }{{"text/html", "first"}, {"text/html", ""}, {"text/plain", "first"}} {
		c := New(DefaultConfig(), Options{})
		w := httptest.NewRecorder()
		h := c.Middleware(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
			out.Header().Set("Content-Type", tc.contentType)
			if tc.first != "" {
				_, _ = out.Write([]byte(tc.first))
			}
			if err := http.NewResponseController(out).Flush(); err != nil {
				t.Errorf("flush: %v", err)
			}
			if !w.Flushed || w.Body.String() != tc.first {
				t.Errorf("before handler returns: flushed=%t body=%q", w.Flushed, w.Body.String())
			}
			_, _ = out.Write([]byte("last"))
		}))
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
		if w.Body.String() != tc.first+"last" || c.Stats().Entries != 0 {
			t.Errorf("streaming response: body=%q entries=%d", w.Body.String(), c.Stats().Entries)
		}
	}
}

func TestCacheHonorsRepeatedControlHeaders(t *testing.T) {
	for _, tc := range []struct {
		key, value string
		want       int
	}{
		{"Cache-Control", "no-store", 2},
		{"Cache-Control", "PRIVATE", 2},
		{"Cache-Control", "no-cache", 2},
		{"Cache-Control", "max-age=10", 1},
		{HeaderControl, "no-store", 2},
	} {
		c := New(DefaultConfig(), Options{})
		calls := 0
		h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Content-Type", "text/html")
			w.Header().Add(tc.key, "public")
			w.Header().Add(tc.key, tc.value)
			_, _ = w.Write([]byte("page"))
		}))
		for i := 0; i < 2; i++ {
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
		}
		if calls != tc.want {
			t.Errorf("%s %q: rendered %d times, want %d", tc.key, tc.value, calls, tc.want)
		}
	}
}

func TestCapturedHeadersCommitAtWriteHeader(t *testing.T) {
	for _, tc := range []struct{ contentType, mode string }{
		{"text/html", "cached"}, {"text/html", "spill"}, {"text/html", "flush"}, {"text/plain", "pass"},
	} {
		cfg := DefaultConfig()
		if tc.mode == "spill" {
			cfg.MaxEntrySize = 2
		}
		c := New(cfg, Options{})
		h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", tc.contentType)
			w.Header().Set("X-Example", "before")
			w.Header().Set("X-Removed", "kept")
			w.WriteHeader(http.StatusOK)
			w.Header().Set("X-Example", "after")
			w.Header().Del("X-Removed")
			w.Header().Set("X-Late", "late")
			if tc.mode == "flush" {
				w.(http.Flusher).Flush()
			}
			_, _ = w.Write([]byte("page"))
		}))
		for i := 0; i < 2; i++ {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
			got := w.Result().Header
			if got.Get("X-Example") != "before" || got.Get("X-Removed") != "kept" || got.Get("X-Late") != "" {
				t.Errorf("%s response %d: headers %v", tc.mode, i, got)
			}
		}
	}
}

func TestDeclaredTrailersPassThrough(t *testing.T) {
	for _, contentType := range []string{"text/html", "text/plain"} {
		c := New(DefaultConfig(), Options{})
		h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", contentType)
			w.Header().Add("Trailer", "X-Checksum")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("body"))
			w.Header().Set("X-Checksum", "done")
		}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
		if got := w.Result().Trailer.Get("X-Checksum"); got != "done" || c.Stats().Entries != 0 {
			t.Errorf("%s: trailer=%q entries=%d", contentType, got, c.Stats().Entries)
		}
	}
}
