package server

import (
	"fmt"
	"sync"
	"testing"
)

// TestCZSpecsConcurrentAccess: installing and deleting theme packages while requests read the
// Customizer specs must not race on the czSpecs map (run with -race; unguarded, the runtime
// aborts the whole process with "concurrent map read and map write").
func TestCZSpecsConcurrentAccess(t *testing.T) {
	bundled := themeBySlug("twentytwentyfive")
	if bundled == nil {
		t.Skip("twentytwentyfive not registered")
	}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				slug := fmt.Sprintf("race-pkg-%d-%d", w, i)
				registerThemePackage(&Theme{Slug: slug, Package: &themePackage{spec: &czThemeSpec{}}})
				unregisterTheme(slug)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if czSpecFor(bundled) == nil {
					t.Error("twentytwentyfive lost its Customizer spec")
					return
				}
			}
		}()
	}
	wg.Wait()
}
