package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGzipResponses(t *testing.T) {
	big := strings.Repeat("<p>Clover and buttercups.</p>\n", 200)
	cases := []struct {
		name, method, accept string
		handler              http.HandlerFunc
		gzipped              bool
		vary                 bool
	}{
		{"html", "GET", "gzip, deflate, br", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("ETag", `"abc"`)
			io.WriteString(w, big)
		}, true, true},
		{"sniffed html in small writes", "GET", "gzip", func(w http.ResponseWriter, r *http.Request) {
			for i := 0; i < 200; i++ {
				io.WriteString(w, "<p>Clover and buttercups.</p>\n")
			}
		}, true, true},
		{"json with length", "GET", "gzip", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", itoa(len(big)))
			io.WriteString(w, big)
		}, true, true},
		{"small", "GET", "gzip", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<p>hi</p>")
		}, false, true},
		{"image", "GET", "gzip", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			io.WriteString(w, big)
		}, false, false},
		{"already encoded", "GET", "gzip", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Content-Encoding", "gzip")
			io.WriteString(w, big)
		}, false, false},
		{"refused", "GET", "gzip;q=0, identity", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, big)
		}, false, false},
		{"no accept", "GET", "", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, big)
		}, false, false},
		{"head", "HEAD", "gzip", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, big)
		}, false, false},
		{"no-transform", "GET", "gzip", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Cache-Control", "public, no-transform")
			io.WriteString(w, big)
		}, false, false},
		{"not modified", "GET", "gzip", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNotModified)
		}, false, false},
		{"event stream", "GET", "gzip", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: 1\n\n")
			http.NewResponseController(w).Flush()
			io.WriteString(w, big)
		}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "/", nil)
			if c.accept != "" {
				req.Header.Set("Accept-Encoding", c.accept)
			}
			rec := httptest.NewRecorder()
			gzipResponses(c.handler).ServeHTTP(rec, req)
			gz := rec.Header().Get("Content-Encoding") == "gzip"
			if c.name == "already encoded" {
				gz = false // the handler's own encoding
			}
			if gz != c.gzipped {
				t.Fatalf("gzipped %v, want %v (headers %v)", gz, c.gzipped, rec.Header())
			}
			if v := strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding"); v != c.vary {
				t.Errorf("Vary %q", rec.Header().Get("Vary"))
			}
			if !c.gzipped {
				return
			}
			if rec.Header().Get("Content-Length") != "" {
				t.Error("compressed response keeps Content-Length")
			}
			if et := rec.Header().Get("ETag"); et != "" && !strings.HasPrefix(et, "W/") {
				t.Errorf("strong ETag %s on a compressed body", et)
			}
			zr, err := gzip.NewReader(rec.Body)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(zr)
			if string(b) != big {
				t.Errorf("decoded body differs (%d bytes)", len(b))
			}
			if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") && c.name != "json with length" {
				t.Errorf("Content-Type %q", rec.Header().Get("Content-Type"))
			}
		})
	}
}

// TestGzipStreamFlush: a flushed response of unknown type reaches the client at once, uncompressed.
func TestGzipStreamFlush(t *testing.T) {
	ts := httptest.NewServer(gzipResponses(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "first\n")
		http.NewResponseController(w).Flush()
		io.WriteString(w, strings.Repeat("x", 4000))
	})))
	defer ts.Close()
	req, _ := http.NewRequest("GET", ts.URL, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.Header.Get("Content-Encoding") != "" || !strings.HasPrefix(string(b), "first\n") || len(b) != 4006 {
		t.Errorf("encoding %q, %d bytes", resp.Header.Get("Content-Encoding"), len(b))
	}
}

// TestStaticETag: embedded static files carry a content-hash ETag (one per representation) and a
// matching If-None-Match gets 304, gzipped or not.
func TestStaticETag(t *testing.T) {
	ts, _, _ := testServerBuilderOff(t)
	do := func(path, ae, inm string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Accept-Encoding", ae)
		if inm != "" {
			req.Header.Set("If-None-Match", inm)
		}
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}
	const css = "/wp-includes/css/dashicons.min.css"
	gz := do(css, "gzip", "")
	plain := do(css, "identity", "")
	if gz.StatusCode != 200 || gz.Header.Get("Content-Encoding") != "gzip" || plain.Header.Get("Content-Encoding") != "" {
		t.Fatalf("encodings %q / %q", gz.Header.Get("Content-Encoding"), plain.Header.Get("Content-Encoding"))
	}
	ge, pe := gz.Header.Get("ETag"), plain.Header.Get("ETag")
	if ge == "" || pe == "" || ge == pe || !strings.HasSuffix(ge, `-gzip"`) {
		t.Fatalf("ETags %q / %q", ge, pe)
	}
	if r := do(css, "gzip", ge); r.StatusCode != http.StatusNotModified {
		t.Errorf("gzip revalidation: %d", r.StatusCode)
	}
	if r := do(css, "identity", pe); r.StatusCode != http.StatusNotModified {
		t.Errorf("plain revalidation: %d", r.StatusCode)
	}
	if r := do(css, "gzip", `"stale"`); r.StatusCode != http.StatusOK {
		t.Errorf("stale ETag: %d", r.StatusCode)
	}
	// A file too small to compress still revalidates.
	img := "/wp-includes/images/wpgo-avatar.svg"
	r := do(img, "gzip", "")
	if et := r.Header.Get("ETag"); et == "" || do(img, "gzip", et).StatusCode != http.StatusNotModified {
		t.Errorf("small file ETag %q", et)
	}
}
