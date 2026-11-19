package cache

import (
	"bytes"
	"compress/gzip"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"wpgo/internal/cspnonce"
)

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gzip header: %v", err)
	}
	zr.Multistream(false)
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gzip body: %v", err) // also catches a wrong CRC-32 or length
	}
	return out
}

// nonceBodies are bodies with the placeholder in awkward places: at both ends, back to back,
// next to text that repeats around it, and around a part longer than the 32 KB window.
func nonceBodies() map[string]string {
	ph := NoncePlaceholder
	script := `<script nonce="` + ph + `">var a=1;</script>`
	page := "<html><head>" + script + "</head><body>" + strings.Repeat("<p>lorem ipsum "+ph[:6]+" dolor</p>", 40) + script + script + "</body></html>"
	rnd := rand.New(rand.NewPCG(1, 2))
	big := make([]byte, 40*1024)
	for i := range big {
		big[i] = "abcdefgh<>/= \n"[rnd.IntN(14)]
	}
	return map[string]string{
		"page":      page,
		"start-end": ph + strings.Repeat("x", 300) + ph,
		"adjacent":  strings.Repeat("y", 300) + ph + ph + ph + strings.Repeat("y", 300),
		// The text after a placeholder repeats the placeholder's own prefix and suffix: a dictionary that
		// held the placeholder would let a back-reference reach into the nonce.
		"echo":   strings.Repeat(ph[:11]+"|"+ph[11:]+strings.Repeat(" lorem "+ph[5:16], 8)+ph+ph[:11]+" ipsum "+ph[11:], 20),
		"window": string(big) + script + string(big[:20000]) + script + string(big[100:5000]),
	}
}

func TestNonceGzipRoundTrip(t *testing.T) {
	for name, body := range nonceBodies() {
		e, err := NewEntry("k", 200, http.Header{"Content-Type": {"text/html"}}, []byte(body), nil, time.Now(), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if e.Gzip != nil || e.NonceGz == nil {
			t.Fatalf("%s: Gzip %d bytes, NonceGz %d bytes; want only a NonceGz copy", name, len(e.Gzip), len(e.NonceGz))
		}
		for range 3 {
			nonce := cspnonce.New()
			gz, ok := e.nonceGzip(nonce)
			if !ok {
				t.Fatalf("%s: nonceGzip not ok", name)
			}
			want := strings.ReplaceAll(body, NoncePlaceholder, nonce)
			if got := gunzip(t, gz); string(got) != want {
				t.Fatalf("%s: decoded body differs from the swapped body", name)
			}
			if bytes.Contains(gz, []byte(NoncePlaceholder)) {
				t.Fatalf("%s: placeholder leaked into the gzip body", name)
			}
		}
	}
}

func TestNonceGzipSkips(t *testing.T) {
	ph := NoncePlaceholder
	every := make([]byte, 0, 1024)
	for i := range 1024 {
		every = append(every, byte(i))
	}
	for name, body := range map[string]string{
		"small":      "<p>" + ph + "</p>",
		"every-byte": string(every) + ph,
		"incompressible": func() string {
			rnd := rand.New(rand.NewPCG(3, 4))
			b := make([]byte, 2000)
			for i := range b {
				b[i] = byte(rnd.IntN(250))
			}
			return string(b) + ph
		}(),
	} {
		e, err := NewEntry("k", 200, http.Header{}, []byte(body), nil, time.Now(), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if e.NonceGz != nil {
			t.Errorf("%s: got a NonceGz copy", name)
		}
		if _, ok := e.nonceGzip(cspnonce.New()); ok {
			t.Errorf("%s: nonceGzip ok without a copy", name)
		}
	}
	e, _ := NewEntry("k", 200, http.Header{}, []byte(nonceBodies()["page"]), nil, time.Now(), time.Hour)
	if _, ok := e.nonceGzip("short"); ok {
		t.Error("nonceGzip took a nonce of the wrong length")
	}
}

// nonceRecorder stands in for the server's outermost writer: it knows the response's nonce.
type nonceRecorder struct {
	*httptest.ResponseRecorder
	nonce string
}

func (n *nonceRecorder) CSPNonce() string { return n.nonce }

func TestServeNoncedHitPrecompressed(t *testing.T) {
	c := New(func() Config { cfg := DefaultConfig(); cfg.ServerHint = HintNone; return cfg }(), Options{})
	body := nonceBodies()["page"]
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, body)
	}))
	get := func(nonce, ae string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/nonced", nil)
		if ae != "" {
			r.Header.Set("Accept-Encoding", ae)
		}
		w := &nonceRecorder{httptest.NewRecorder(), nonce}
		var rw http.ResponseWriter = w
		if nonce == "" {
			rw = w.ResponseRecorder // no writer on the chain knows a nonce
		}
		h.ServeHTTP(rw, r)
		return w.ResponseRecorder
	}
	get("", "") // miss: stores the entry
	nonce := cspnonce.New()
	w := get(nonce, "gzip")
	if status(w) != "hit" || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("hit %q, Content-Encoding %q; want a gzip hit", status(w), w.Header().Get("Content-Encoding"))
	}
	if got := gunzip(t, w.Body.Bytes()); string(got) != strings.ReplaceAll(body, NoncePlaceholder, nonce) {
		t.Fatal("gzip hit does not decode to the body with this response's nonce")
	}
	if w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) || !strings.Contains(w.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("Content-Length %q for %d bytes, Vary %q", w.Header().Get("Content-Length"), w.Body.Len(), w.Header().Get("Vary"))
	}
	// Without gzip, or without a nonce source, the identity body goes out for the server to swap.
	for _, w := range []*httptest.ResponseRecorder{get(nonce, ""), get("", "gzip")} {
		if w.Header().Get("Content-Encoding") != "" || w.Body.String() != body {
			t.Fatalf("Content-Encoding %q; want the identity body with the placeholder", w.Header().Get("Content-Encoding"))
		}
	}
}

func BenchmarkNoncedHit(b *testing.B) {
	body := []byte(strings.Repeat(nonceBodies()["page"], 30)) // ~150 KB, ~90 placeholders
	e, _ := NewEntry("k", 200, http.Header{}, body, nil, time.Now(), time.Hour)
	nonce := cspnonce.New()
	b.Run("precompressed", func(b *testing.B) {
		b.SetBytes(int64(len(body)))
		for b.Loop() {
			e.nonceGzip(nonce)
		}
	})
	b.Run("swap+gzip", func(b *testing.B) { // what a hit cost before: the server's swap and gzip
		b.SetBytes(int64(len(body)))
		for b.Loop() {
			swapped := bytes.ReplaceAll(body, []byte(NoncePlaceholder), []byte(nonce))
			zw, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
			zw.Write(swapped)
			zw.Close()
		}
	})
}
