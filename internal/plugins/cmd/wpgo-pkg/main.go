// Command wpgo-pkg creates signing keys and builds, signs and verifies Pursae plugin packages. It
// is the same tool as `wpgo plugin` and is used by tools/build-plugins.sh.
//
//	wpgo-pkg keygen -out PREFIX [-comment TEXT]
//	    writes PREFIX.pub and PREFIX.key (mode 0600); existing files are kept
//	wpgo-pkg pack -dir PLUGIN_DIR -wasm plugin.wasm -key PREFIX.key -out pkg.zip
//	    packs PLUGIN_DIR/plugin.json (+ readme.md, icon.svg and assets/** if present);
//	    the assets' digests are written into the packed plugin.json
//	wpgo-pkg verify -pub PREFIX.pub pkg.zip
//	    verifies the signature and prints the manifest summary
//	wpgo-pkg new -slug SLUG [-dir DIR] [-sdk PATH]   scaffold a plugin
//	wpgo-pkg build -dir DIR [-key K] [-out ZIP]      compile to Wasm and pack
package main

import (
	"fmt"
	"os"

	"wpgo/internal/plugins/pkgtool"
)

func main() {
	if err := pkgtool.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "wpgo-pkg:", err)
		os.Exit(1)
	}
}
