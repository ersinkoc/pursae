package cache

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// buildNonceGzRef is buildNonceGz as it was before its writers were pooled: a new deflate writer
// per part. The pooled version must produce the same bytes.
func buildNonceGzRef(body, ph []byte) (data []byte, cuts, at []int, ok bool) {
	if len(body) < nonceGzMinSize || len(ph) == 0 || len(ph) > 0xffff {
		return nil, nil, nil, false
	}
	var seen [256]bool
	for _, b := range body {
		seen[b] = true
	}
	filler, found := byte(0), false
	for i := 255; i >= 0; i-- {
		if !seen[i] {
			filler, found = byte(i), true
			break
		}
	}
	if !found {
		return nil, nil, nil, false
	}
	for off := 0; ; {
		i := bytes.Index(body[off:], ph)
		if i < 0 {
			break
		}
		if len(at) == nonceGzMaxParts {
			return nil, nil, nil, false
		}
		at = append(at, off+i)
		off += i + len(ph)
	}
	if len(at) == 0 {
		return nil, nil, nil, false
	}
	dict := bytes.Clone(body)
	for _, p := range at {
		for j := range ph {
			dict[p+j] = filler
		}
	}
	var buf bytes.Buffer
	start := 0
	for i := 0; i <= len(at); i++ {
		end := len(body)
		if i < len(at) {
			end = at[i]
		}
		var zw *flate.Writer
		if start == 0 {
			zw, _ = flate.NewWriter(&buf, flate.DefaultCompression)
		} else {
			zw, _ = flate.NewWriterDict(&buf, flate.DefaultCompression, dict[max(0, start-32*1024):start])
		}
		if _, err := zw.Write(body[start:end]); err != nil {
			return nil, nil, nil, false
		}
		if err := zw.Flush(); err != nil {
			return nil, nil, nil, false
		}
		cuts = append(cuts, buf.Len())
		start = end + len(ph)
	}
	if buf.Len()+len(at)*(5+len(ph))+20 >= len(body) {
		return nil, nil, nil, false
	}
	return buf.Bytes(), cuts, at, true
}

func gzipRef(body []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
	zw.Write(body)
	zw.Close()
	return buf.Bytes()
}

// spare reports whether b keeps more capacity than the allocator's rounding (size classes waste at
// most 1/8, large objects round up to 8 KB pages): a bytes.Buffer's doubled backing array would.
func spare(b []byte) bool { return cap(b)-len(b) > len(b)/8+8<<10 }

func perfPage(sz int) []byte {
	var body []byte
	for i := 0; len(body) < sz; i++ {
		body = append(body, fmt.Sprintf("<div class=\"wp-block-group\"><p>Lorem ipsum %d dolor sit amet</p><a href=\"/post-%d\">Post %x</a></div>\n", i, i, i*7919)...)
	}
	return body
}

// TestPooledWritersByteIdentical checks that the pooled gzip and deflate writers give the same
// bytes as fresh ones, run after run (a reused writer must carry nothing over), and that the stored
// slices carry no spare capacity.
func TestPooledWritersByteIdentical(t *testing.T) {
	ph := []byte(NoncePlaceholder)
	for round := range 3 {
		for name, src := range nonceBodies() {
			for _, rep := range []int{1, 3} {
				body := []byte(strings.Repeat(src, rep))
				wd, wc, wa, wok := buildNonceGzRef(body, ph)
				gd, gc, ga, gok := buildNonceGz(body, ph)
				if wok != gok || !bytes.Equal(wd, gd) || fmt.Sprint(wc, wa) != fmt.Sprint(gc, ga) {
					t.Fatalf("round %d %s x%d: pooled buildNonceGz differs (ok %v/%v, %d/%d bytes)", round, name, rep, wok, gok, len(wd), len(gd))
				}
				if gok && spare(gd) {
					t.Fatalf("%s: NonceGz cap %d > len %d", name, cap(gd), len(gd))
				}
				// The same body without placeholders goes through NewEntry's gzip copy.
				plain := bytes.ReplaceAll(body, ph, bytes.Repeat([]byte{'z'}, len(ph)))
				e, err := NewEntry("k", 200, http.Header{}, plain, nil, time.Now(), time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				if want := gzipRef(plain); len(want) < len(plain) && !bytes.Equal(e.Gzip, want) {
					t.Fatalf("round %d %s x%d: pooled gzip differs from a fresh writer's", round, name, rep)
				}
				if spare(e.Gzip) {
					t.Fatalf("%s: Gzip cap %d > len %d", name, cap(e.Gzip), len(e.Gzip))
				}
			}
		}
	}
	for _, sz := range []int{256, 4 << 10, 40 << 10, 300 << 10} {
		body := perfPage(sz)
		e, _ := NewEntry("k", 200, http.Header{}, body, nil, time.Now(), time.Hour)
		if !bytes.Equal(e.Gzip, gzipRef(body)) {
			t.Fatalf("%d bytes: pooled gzip differs from a fresh writer's", sz)
		}
	}
}

// TestPooledWritersConcurrent stores entries from many goroutines: a pooled writer must never be
// shared by two stores at once.
func TestPooledWritersConcurrent(t *testing.T) {
	bodies := [][]byte{perfPage(8 << 10), perfPage(30 << 10), []byte(nonceBodies()["page"]), []byte(nonceBodies()["window"])}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 20 {
				body := bodies[(g+i)%len(bodies)]
				e, _ := NewEntry("k", 200, http.Header{}, body, nil, time.Now(), time.Hour)
				if e.Gzip != nil && !bytes.Equal(e.Gzip, gzipRef(body)) {
					t.Error("gzip differs")
					return
				}
				if e.NonceGz != nil {
					if wd, _, _, _ := buildNonceGzRef(body, []byte(NoncePlaceholder)); !bytes.Equal(wd, e.NonceGz) {
						t.Error("NonceGz differs")
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}

func BenchmarkNewEntry40K(b *testing.B) {
	body := perfPage(40 << 10)
	h := http.Header{"Content-Type": {"text/html"}}
	b.ReportAllocs()
	for b.Loop() {
		_, _ = NewEntry("k", 200, h, body, []string{"a"}, time.Now(), time.Hour)
	}
}

func BenchmarkBuildNonceGz20(b *testing.B) {
	ph := "PH0123456789abcdefPH0123"
	var buf bytes.Buffer
	buf.WriteString("<html><head>")
	for i := range 20 {
		buf.WriteString(`<script nonce="` + ph + `">var x` + strings.Repeat("a", i%7) + `=1;</script>` + strings.Repeat("<p>Lorem ipsum dolor sit amet consectetur</p>", 40))
	}
	buf.WriteString("</body></html>")
	body := buf.Bytes()
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		buildNonceGz(body, []byte(ph))
	}
}

func BenchmarkNewEntryNonced150K(b *testing.B) {
	body := []byte(strings.Repeat(nonceBodies()["page"], 30))
	b.ReportAllocs()
	for b.Loop() {
		if e, _ := NewEntry("k", 200, http.Header{}, body, nil, time.Now(), time.Hour); e.NonceGz == nil {
			b.Fatal("no NonceGz")
		}
	}
}
