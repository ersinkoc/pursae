package server

import (
	"log"
	"os"
	"path/filepath"
)

// wbOldBuilderUploads are the uploads folders only the old (Elementor-derived) builder wrote: its
// generated page and global-class CSS, the design system sync stylesheet, form upload fields and
// custom icon sets (under the builder folder, and under its name before the Pursae rename when
// both folders existed). Migration 0028 deletes the data they belonged to.
//
// The new builder's folders under uploads/web-builder stay: css (its page CSS, webbuilder.PageCSSDir),
// fonts (local fonts, which the new builder serves too) and google-fonts (the localized Google Fonts).
var wbOldBuilderUploads = []string{
	"elementor/css",
	"elementor/design-system-sync",
	"elementor/forms",
	builderUploadsDir + "/custom-icons",
	legacyBuilderUploadsDir + "/custom-icons",
}

// wbRemoveOldBuilderUploads deletes the old builder's generated and uploaded files at start-up, and
// the uploads/elementor folder once nothing else is left in it. It runs on every start and does
// nothing once the folders are gone.
func wbRemoveOldBuilderUploads(uploadsDir string) {
	if uploadsDir == "" {
		return
	}
	for _, rel := range wbOldBuilderUploads {
		dir := filepath.Join(uploadsDir, filepath.FromSlash(rel))
		if _, err := os.Lstat(dir); err != nil {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("web builder: removing the old builder's %s: %v", dir, err)
		}
	}
	_ = os.Remove(filepath.Join(uploadsDir, "elementor")) // only succeeds when empty
}
