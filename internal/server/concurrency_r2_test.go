package server

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Browser events stop being queued once pixelQueueMax deliveries wait; order purchases never do.
func TestPixelAdmitBoundsQueue(t *testing.T) {
	pixelQueued.Store(0)
	defer pixelQueued.Store(0)
	for i := 0; i < pixelQueueMax; i++ {
		if !pixelAdmit("browser") {
			t.Fatalf("event %d refused below the cap", i)
		}
	}
	if pixelAdmit("browser") {
		t.Fatal("browser event admitted past the cap")
	}
	if got := pixelQueued.Load(); got != pixelQueueMax {
		t.Fatalf("a refused event kept its place: queued = %d", got)
	}
	if !pixelAdmit("order") {
		t.Fatal("order purchase refused")
	}
	pixelQueued.Add(-1)
	pixelQueued.Add(-1)
	if !pixelAdmit("browser") {
		t.Fatal("browser event refused after a delivery finished")
	}
}

// Alert de-duplication entries older than a day are swept instead of kept forever.
func TestMsSweepSeenFailures(t *testing.T) {
	msSeenFailures.Store("old", time.Now().Add(-25*time.Hour))
	msSeenFailures.Store("new", time.Now())
	defer msSeenFailures.Delete("new")
	msSeenSwept.Store(0)
	msSweepSeenFailures()
	if _, ok := msSeenFailures.Load("old"); ok {
		t.Fatal("expired entry kept")
	}
	if _, ok := msSeenFailures.Load("new"); !ok {
		t.Fatal("recent entry dropped")
	}
	msSeenFailures.Store("old", time.Now().Add(-25*time.Hour))
	defer msSeenFailures.Delete("old")
	msSweepSeenFailures() // within the hour: no second sweep
	if _, ok := msSeenFailures.Load("old"); !ok {
		t.Fatal("swept twice within the hour")
	}
}

// An event stream whose client stops reading ends after the write deadline instead of blocking
// its handler forever.
func TestSSEStreamDeadlineEndsStalledStream(t *testing.T) {
	old := sseWriteTimeout
	sseWriteTimeout = 200 * time.Millisecond
	defer func() { sseWriteTimeout = old }()
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		arm, reset := sseStreamDeadlines(w)
		defer reset()
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := ": " + strings.Repeat("x", 64<<10) + "\n\n"
		for r.Context().Err() == nil {
			arm()
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetReadBuffer(4096)
	}
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n") // and never read
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("stream handler still blocked in a write")
	}
}

// Block lists taken from page text a visitor controls (the echoed search term) cannot grow the
// block style cache without bound.
func TestBlockStyleCacheBounded(t *testing.T) {
	s := &Server{web: os.DirFS("../../web")}
	names := []string{}
	for n := range s.blockStyleDirs() {
		names = append(names, n)
	}
	if len(names) < 12 {
		t.Skip("block styles not found")
	}
	blockStyleCache.Clear()
	blockStyleCacheN.Store(0)
	defer func() { blockStyleCache.Clear(); blockStyleCacheN.Store(0) }()
	for i := 0; i < 2*blockStyleCacheMax; i++ {
		page := blockStylesMarker
		for b := 0; b < 12; b++ { // a different subset of 12 blocks per "search"
			if i&(1<<b) != 0 {
				page += " wp-block-" + names[b]
			}
		}
		s.blockStylesFor(nil, []byte(page))
	}
	n := 0
	blockStyleCache.Range(func(_, _ any) bool { n++; return true })
	if n > blockStyleCacheMax {
		t.Fatalf("%d cached block lists, cap %d", n, blockStyleCacheMax)
	}
}
