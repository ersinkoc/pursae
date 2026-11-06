package server

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

// The builder's names before the Pursae rename (gowebeditor, golementor) no longer show in URLs.
// What older pages, stylesheets and bookmarks still point at keeps working through this file.

// gweLegacyAdminPaths are the builder's admin screens under their names before the Pursae rename
// (relative to /wp-admin), with the screen each one moved to.
var gweLegacyAdminPaths = map[string]string{
	"/gowebeditor-pro":           "/wp-admin/web-builder",
	"/gowebeditor-icons":         "/wp-admin/web-builder-icons",
	"/gowebeditor-theme-builder": "/wp-admin/theme-builder",
	"/golementor-setup":          "/wp-admin/web-builder-setup",
	"/golementor-setup/preview":  "/wp-admin/web-builder-setup/preview",
}

// gweLegacyRedirect sends an old builder URL to its new one with the same query; a POST from a page
// loaded before the rename keeps its method and body (307).
func gweLegacyRedirect(to string) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := http.StatusMovedPermanently
		if c.Request.Method != http.MethodGet {
			code = http.StatusTemporaryRedirect
		}
		u := to
		if q := c.Request.URL.RawQuery; q != "" {
			u += "?" + q
		}
		c.Redirect(code, u)
	}
}

// gweMoveLegacyDirs renames, once, the folders that carried the old names: uploads/gowebeditor
// and the Theme File Editor's copy of the hello-gowebeditor theme.
func gweMoveLegacyDirs(uploadsDir, themesDir string) {
	moveDir(filepath.Join(uploadsDir, gweLegacyUploadsDir), filepath.Join(uploadsDir, gweUploadsDir))
	moveDir(filepath.Join(themesDir, "hello-gowebeditor"), filepath.Join(themesDir, helloThemeSlug))
}

// moveDir renames old to cur when old exists and cur does not.
func moveDir(old, cur string) {
	if _, err := os.Stat(old); err != nil {
		return
	}
	if _, err := os.Stat(cur); err == nil {
		return
	}
	_ = os.Rename(old, cur)
}
