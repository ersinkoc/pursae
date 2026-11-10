package cache

import (
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestSetSinceRefusesPurgedRenders: an entry rendered before a purge that covers it (everything,
// one of its tags, its URL on its host or on every host, its key) is not stored; an unrelated
// purge does not stop it.
func TestSetSinceRefusesPurgedRenders(t *testing.T) {
	s, err := NewStore(StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	key := "http://example.com/hello/"
	entry := func() *Entry {
		e, err := NewEntry(key, 200, http.Header{"Content-Type": {"text/html"}}, []byte("x"), []string{"post:1", "home"}, time.Now(), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	for name, purge := range map[string]func(){
		"all":      func() { s.PurgeAll() },
		"tag":      func() { s.PurgeTags("post:1") },
		"url":      func() { s.PurgeURL("http://example.com/hello/") },
		"path":     func() { s.PurgeURL("/hello/") },
		"key":      func() { s.Delete(key) },
		"two tags": func() { s.PurgeTags("feed", " home ") },
	} {
		gen := s.Generation()
		purge()
		if err := s.SetSince(entry(), gen); err != ErrPurged {
			t.Errorf("%s: SetSince = %v, want ErrPurged", name, err)
		}
		if _, ok := s.Get(key); ok {
			t.Errorf("%s: purged render was stored", name)
		}
	}
	for name, purge := range map[string]func(){
		"other tag":  func() { s.PurgeTags("post:2") },
		"other url":  func() { s.PurgeURL("http://example.com/other/") },
		"other host": func() { s.PurgeURL("http://other.test/hello/") },
		"expired":    func() { s.PurgeExpired() },
	} {
		gen := s.Generation()
		purge()
		if err := s.SetSince(entry(), gen); err != nil {
			t.Errorf("%s: SetSince = %v, want stored", name, err)
		}
		s.Delete(key)
	}
}

// TestRenderRacingPurgeNotStored: a page whose render overlaps a purge of its tags is served but
// not stored, so the next visitor gets a fresh render rather than the content just purged.
func TestRenderRacingPurgeNotStored(t *testing.T) {
	c, app, h := newTestCache(t, nil)
	app.mod = func(http.ResponseWriter, *http.Request) {
		if app.calls.Load() == 1 {
			c.PurgeTags("post:12") // a post saved while the first render runs
		}
	}
	if w := do(h, "GET", "http://example.com/hello/"); w.Code != 200 || status(w) != "miss" {
		t.Fatalf("first: %d %q", w.Code, status(w))
	}
	if w := do(h, "GET", "http://example.com/hello/"); status(w) != "miss" {
		t.Fatalf("second: %q, want a fresh render (miss)", status(w))
	}
	if w := do(h, "GET", "http://example.com/hello/"); status(w) != "hit" {
		t.Fatalf("third: %q, want the second render cached", status(w))
	}
	if n := app.calls.Load(); n != 2 {
		t.Fatalf("%d renders, want 2", n)
	}
}

// TestConcurrentMissesCoalesced: simultaneous misses of one page render it once; the waiting
// requests are served from the cache, each with its own conditional/encoding negotiation.
func TestConcurrentMissesCoalesced(t *testing.T) {
	_, app, h := newTestCache(t, nil)
	gate := make(chan struct{})
	app.mod = func(http.ResponseWriter, *http.Request) { <-gate }
	const n = 12
	var wg sync.WaitGroup
	codes := make([]int, n)
	bodies := make([]string, n)
	gz := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hdr := []string{}
			if i%2 == 0 {
				hdr = []string{"Accept-Encoding", "gzip"}
			}
			w := do(h, "GET", "http://example.com/slow/", hdr...)
			codes[i], bodies[i], gz[i] = w.Code, w.Body.String(), w.Header().Get("Content-Encoding")
		}()
	}
	time.Sleep(100 * time.Millisecond) // let them all queue behind the first render
	close(gate)
	wg.Wait()
	if c := app.calls.Load(); c != 1 {
		t.Fatalf("%d renders for %d concurrent misses, want 1", c, n)
	}
	for i := range n {
		if codes[i] != 200 {
			t.Fatalf("request %d: %d", i, codes[i])
		}
		if want := map[bool]string{true: "gzip", false: ""}[i%2 == 0]; gz[i] != want {
			t.Errorf("request %d: Content-Encoding %q, want %q", i, gz[i], want)
		}
	}
}

// TestConcurrentMissesNotCacheable: when the first render turns out not to be cacheable, the
// waiting requests render the page themselves (they never get another visitor's response).
func TestConcurrentMissesNotCacheable(t *testing.T) {
	_, app, h := newTestCache(t, nil)
	gate := make(chan struct{})
	app.mod = func(w http.ResponseWriter, r *http.Request) {
		<-gate
		w.Header().Set("Set-Cookie", "v="+r.Header.Get("X-Who"))
	}
	const n = 6
	var wg sync.WaitGroup
	cookies := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := do(h, "GET", "http://example.com/personal/", "X-Who", string(rune('a'+i)))
			cookies[i] = w.Header().Get("Set-Cookie")
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()
	if c := app.calls.Load(); c != n {
		t.Fatalf("%d renders, want %d (each request its own)", c, n)
	}
	for i := range n {
		if want := "v=" + string(rune('a'+i)); cookies[i] != want {
			t.Errorf("request %d got cookie %q, want %q", i, cookies[i], want)
		}
	}
}

// TestFlightWaitBounded: a request does not wait longer than flightWait for a stuck render.
func TestFlightWaitBounded(t *testing.T) {
	c, app, h := newTestCache(t, nil)
	c.flightWait = 50 * time.Millisecond
	gate := make(chan struct{})
	app.mod = func(http.ResponseWriter, *http.Request) {
		if app.calls.Load() == 1 {
			<-gate
		}
	}
	go do(h, "GET", "http://example.com/stuck/")
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	if w := do(h, "GET", "http://example.com/stuck/"); w.Code != 200 {
		t.Fatalf("waiting request: %d", w.Code)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("waited %v for a stuck render", d)
	}
	close(gate)
}

// TestFlightReleasedWhenNotCacheable: once the first render turns out not to be cacheable, the
// requests waiting for it render their own copy at once instead of waiting until that response
// has been streamed to its (possibly slow) client.
func TestFlightReleasedWhenNotCacheable(t *testing.T) {
	c, app, h := newTestCache(t, nil)
	c.flightWait = 5 * time.Second
	gate := make(chan struct{})
	defer close(gate)
	first := make(chan struct{})
	app.mod = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "v=1") // never stored
		if app.calls.Load() == 1 {
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, "<html>")
			close(first)
			<-gate // a slow client: the body of the first response is still being written
		}
	}
	go do(h, "GET", "http://example.com/personal/")
	<-first
	start := time.Now()
	if w := do(h, "GET", "http://example.com/personal/"); w.Code != 200 || status(w) != "bypass" {
		t.Fatalf("second request: %d %q", w.Code, status(w))
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the second request waited %v for the first, uncacheable, response", d)
	}
}
