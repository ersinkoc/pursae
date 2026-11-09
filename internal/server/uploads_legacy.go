package server

import (
	"net/http"
	"os"
	"strings"
)

// Uploads that older pages and stylesheets still point at under the builder's name before the
// Pursae rename. Stored URLs keep working: the uploads route maps them onto the moved folder.

// builderUploadsDir is the builder's folder in the uploads dir (fonts, Google Fonts cache, custom icons);
// legacyBuilderUploadsDir is its name before the rename.
const (
	builderUploadsDir       = "web-builder"
	legacyBuilderUploadsDir = "gowebeditor"
)

// legacyUploadPath maps a stored /wp-content/uploads/gowebeditor/... URL path onto the moved folder.
func legacyUploadPath(p string) string {
	if rest, ok := strings.CutPrefix(p, "/wp-content/uploads/"+legacyBuilderUploadsDir+"/"); ok {
		return "/wp-content/uploads/" + builderUploadsDir + "/" + rest
	}
	return p
}

// noListFS hides directories from http.FileServer: a directory URL answers
// 404 instead of an HTML listing. WordPress relies on index.php files and the
// web server's config for this; the uploads tree holds files whose secrecy
// rests on random names (personal-data exports, customer add-on uploads).
type noListFS struct{ http.FileSystem }

func (fsys noListFS) Open(name string) (http.File, error) {
	f, err := fsys.FileSystem.Open(name)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}
