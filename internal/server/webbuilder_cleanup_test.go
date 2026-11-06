package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWBRemoveOldBuilderUploads(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gone := []string{"elementor/css/post-12.css", "elementor/design-system-sync/design-system-sync.css",
		"elementor/forms/a.pdf", "web-builder/custom-icons/set/style.css", "gowebeditor/custom-icons/set/style.css"}
	kept := []string{"web-builder/css/post-5-2.css", "web-builder/fonts/inter.woff2",
		"web-builder/google-fonts/css/inter.css", "2026/11/photo.jpg"}
	for _, rel := range append(append([]string{}, gone...), kept...) {
		write(rel)
	}
	for i := 0; i < 2; i++ { // runs on every start
		wbRemoveOldBuilderUploads(dir)
	}
	for _, rel := range gone {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s left", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "elementor")); err == nil {
		t.Error("the empty uploads/elementor folder is left")
	}
	for _, rel := range kept {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s removed: %v", rel, err)
		}
	}

	// Something else in uploads/elementor keeps the folder.
	write("elementor/other.txt")
	wbRemoveOldBuilderUploads(dir)
	if _, err := os.Stat(filepath.Join(dir, "elementor", "other.txt")); err != nil {
		t.Error("a file the old builder did not write was removed")
	}
	wbRemoveOldBuilderUploads("") // no uploads dir: nothing to do
}
