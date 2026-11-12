package cache

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewSnapshotsConfigSlices(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ExcludePaths = []string{"/private"}
	cfg.ExcludeUserAgents = []string{"bad-bot"}
	cfg.Optimize.Preconnect = []string{"https://one.test"}
	c := New(cfg, Options{})
	ready, proceed := make(chan struct{}), make(chan struct{})
	result := make(chan bool, 1)
	go func() {
		close(ready)
		<-proceed
		r := httptest.NewRequest("GET", "http://example.test/private", nil)
		snapshot := c.Config()
		result <- c.Classify(r).Bypass == ReasonExcludedPath && snapshot.ExcludeUserAgents[0] == "bad-bot" && snapshot.Optimize.Preconnect[0] == "https://one.test"
	}()
	<-ready
	cfg.ExcludePaths[0] = "/other"
	cfg.ExcludeUserAgents[0] = "changed"
	cfg.Optimize.Preconnect[0] = "changed"
	close(proceed)
	if !<-result {
		t.Fatal("cache retained caller-owned configuration slices")
	}
}

func TestConfigReturnsIndependentSlices(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ExcludePaths = []string{"/private"}
	cfg.Optimize.Preconnect = []string{"https://one.test"}
	cfg.Optimize.Preload = []PreloadAsset{{Href: "/one.css"}}
	c := New(cfg, Options{})
	view := c.Config()
	ready, proceed := make(chan struct{}), make(chan struct{})
	result := make(chan bool, 1)
	go func() {
		close(ready)
		<-proceed
		r := httptest.NewRequest("GET", "http://example.test/private", nil)
		next := c.Config()
		result <- c.Classify(r).Bypass == ReasonExcludedPath && next.Optimize.Preconnect[0] == "https://one.test" && next.Optimize.Preload[0].Href == "/one.css"
	}()
	<-ready
	view.ExcludePaths[0] = "/other"
	view.Optimize.Preconnect[0] = "changed"
	view.Optimize.Preload[0].Href = "changed"
	close(proceed)
	if !<-result {
		t.Fatal("returned configuration aliases the live cache")
	}
}

func TestStoreSnapshotsInputEntry(t *testing.T) {
	s, err := NewStore(StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEntry("http://example.test/#d", 200, http.Header{"X-Test": {"before"}}, []byte("body"), []string{"tag"}, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(e); err != nil {
		t.Fatal(err)
	}
	ready, proceed := make(chan struct{}), make(chan struct{})
	result := make(chan bool, 1)
	go func() {
		close(ready)
		<-proceed
		got, ok := s.Get("http://example.test/#d")
		result <- ok && string(got.Body) == "body" && got.Header.Get("X-Test") == "before" && got.Tags[0] == "tag"
	}()
	<-ready
	e.Body[0] = 'X'
	e.Header.Set("X-Test", "after")
	e.Tags[0] = "other"
	close(proceed)
	if !<-result || s.PurgeTags("tag") != 1 {
		t.Fatal("stored entry or tag index changed with caller-owned data")
	}
}

func TestStoreGetReturnsIndependentEntry(t *testing.T) {
	s, err := NewStore(StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEntry("k", 200, http.Header{"X-Test": {"before"}}, []byte("body"), []string{"tag"}, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(e); err != nil {
		t.Fatal(err)
	}
	view, ok := s.Get("k")
	if !ok {
		t.Fatal("missing entry")
	}
	ready, proceed := make(chan struct{}), make(chan struct{})
	result := make(chan bool, 1)
	go func() {
		close(ready)
		<-proceed
		got, ok := s.Get("k")
		result <- ok && string(got.Body) == "body" && got.Header.Get("X-Test") == "before" && got.Tags[0] == "tag"
	}()
	<-ready
	view.Body[0] = 'X'
	view.Header.Set("X-Test", "changed")
	view.Tags[0] = "other"
	close(proceed)
	if !<-result || s.PurgeTags("tag") != 1 {
		t.Fatal("returned entry aliases the store or its tag index")
	}
}
