// Package web embeds the admin UI, the bundled themes, the theme library and the core static assets.
package web

import (
	"embed"
	"io/fs"
	"os"
)

//go:embed admin themes wp-includes wp-admin gowebeditor theme-library gooike goocommerce languages
var embedded embed.FS

// FS returns the web assets. With dir set (development) files are read from disk
// so template and CSS edits show up without rebuilding.
func FS(dir string) fs.FS {
	if dir != "" {
		return os.DirFS(dir)
	}
	return embedded
}
