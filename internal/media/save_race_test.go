package media

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

// Parallel uploads of one file name all succeed, each under its own name.
func TestSaveSameNameInParallel(t *testing.T) {
	dir := t.TempDir()
	const n = 16
	var wg sync.WaitGroup
	names := make([]string, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			st, err := Save(dir, false, time.Now(), "image.png", bytes.NewReader([]byte{byte(i)}))
			errs[i] = err
			if err == nil {
				names[i] = st.RelPath
			}
		}(i)
	}
	close(start)
	wg.Wait()
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("upload %d: %v", i, errs[i])
		}
		if seen[names[i]] {
			t.Fatalf("two uploads stored as %s", names[i])
		}
		seen[names[i]] = true
	}
}
