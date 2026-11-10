package i18n

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Two installs of one pack at the same time both succeed and leave a complete file.
func TestExtractConcurrent(t *testing.T) {
	body := strings.Repeat("x", 1<<20)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("tr_TR.mo")
	_, _ = w.Write([]byte(body))
	_ = zw.Close()
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = extract(buf.Bytes(), dir)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("extract %d: %v", i, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "tr_TR.mo"))
	if err != nil || string(b) != body {
		t.Fatalf("file incomplete: %d bytes, %v", len(b), err)
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".*.tmp"))
	if len(left) > 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}
