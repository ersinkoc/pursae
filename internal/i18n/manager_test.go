package i18n

import (
	"io/fs"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

// slowFS blocks reads of files containing block until release is closed and counts opens.
type slowFS struct {
	fs      fs.FS
	block   string
	release chan struct{}
	opens   atomic.Int64
}

func (s *slowFS) Open(name string) (fs.File, error) {
	s.opens.Add(1)
	if s.block != "" && strings.Contains(name, s.block) {
		<-s.release
	}
	return s.fs.Open(name)
}

// TestCatalogLoadNotUnderManagerLock: parsing one locale's files does not hold up lookups of
// other locales, and concurrent first lookups of one locale load it once.
func TestCatalogLoadNotUnderManagerLock(t *testing.T) {
	sf := &slowFS{
		fs: fstest.MapFS{
			"wpgo-fr_FR.json": {Data: []byte(`{"Hello":"Bonjour"}`)},
			"wpgo-de_DE.json": {Data: []byte(`{"Hello":"Hallo"}`)},
		},
		block:   "fr_FR",
		release: make(chan struct{}),
	}
	m := NewManager("", sf)
	var wg sync.WaitGroup
	got := make([]string, 5)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = m.Catalog("fr_FR").T("Hello")
		}()
	}
	time.Sleep(50 * time.Millisecond)
	done := make(chan string)
	go func() { done <- m.Catalog("de_DE").T("Hello") }()
	select {
	case v := <-done:
		if v != "Hallo" {
			t.Fatalf("de_DE: %q", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("de_DE lookup blocked by the fr_FR load")
	}
	before := sf.opens.Load()
	close(sf.release)
	wg.Wait()
	for i, v := range got {
		if v != "Bonjour" {
			t.Errorf("caller %d: %q", i, v)
		}
	}
	// One load of fr_FR: two files (.mo, .json) opened once each, whatever the caller count.
	if extra := sf.opens.Load() - before; extra > 2 {
		t.Errorf("fr_FR files opened %d more times after release; want a single load", extra)
	}
	m.Reset()
	if m.Catalog("fr_FR").T("Hello") != "Bonjour" {
		t.Error("reload after Reset")
	}
}
