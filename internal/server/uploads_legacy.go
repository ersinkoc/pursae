package server

import "strings"

// Uploads that older pages and stylesheets still point at under the builder's name before the
// Pursae rename. Stored URLs keep working: the uploads route maps them onto the moved folder.

// gweUploadsDir is the builder's folder in the uploads dir (fonts, Google Fonts cache, custom icons);
// gweLegacyUploadsDir is its name before the rename.
const (
	gweUploadsDir       = "web-builder"
	gweLegacyUploadsDir = "gowebeditor"
)

// gweLegacyUploadPath maps a stored /wp-content/uploads/gowebeditor/... URL path onto the moved folder.
func gweLegacyUploadPath(p string) string {
	if rest, ok := strings.CutPrefix(p, "/wp-content/uploads/"+gweLegacyUploadsDir+"/"); ok {
		return "/wp-content/uploads/" + gweUploadsDir + "/" + rest
	}
	return p
}
