---
title: Your first plugin
description: Write, build, pack and install a Pursae plugin in Go, step by step.
weight: 20
section: plugins
---

You need Go 1.27 and a checkout of the Pursae repository (for the SDK module and the
`wpgo-pkg` tool). The plugin itself is an ordinary Go `main` package.

## 1. Create the module

The SDK lives in the Pursae repository as `wpgo/sdk/go/wpgo`. The simplest layout is a
directory inside that repository (the example plugins live in `examples/plugins/`), because the
module `wpgo` is not published separately yet:

```bash
cd pursae
mkdir -p examples/plugins/hello-reader
cd examples/plugins/hello-reader
```

To keep the plugin in its own repository instead, create a module and point it at your checkout:

```bash
go mod init example.com/hello-reader
go mod edit -require=wpgo@v0.0.0 -replace=wpgo=../pursae
```

## 2. Write the code

`main.go`:

```go
// Command hello-reader greets readers at the end of every post.
package main

import (
	"html"

	"wpgo/sdk/go/wpgo"
)

func init() {
	// Filters receive the current value and the extra arguments the core passed.
	wpgo.AddFilter("the_content", wpgo.DefaultPriority, func(_ wpgo.Context, content string, args []any) string {
		post, _ := args[0].(map[string]any)
		if single, _ := post["is_single"].(bool); !single {
			return content
		}
		name := "reader"
		if u, _ := wpgo.CurrentUser(); u != nil { // needs users:read
			name = u.DisplayName
		}
		return content + "<p class=\"hello-reader\">Thanks for reading, " + html.EscapeString(name) + "!</p>"
	})
}

func main() {} // never called: the host drives the plugin through its exports
```

Hooks registered in `init` are queued and sent to the host when the plugin is initialised;
callbacks registered with `wpgo.OnInit` run right after that and may already call host functions.

## 3. Describe it: plugin.json

```json
{
  "slug": "hello-reader",
  "name": "Hello Reader",
  "version": "1.0.0",
  "description": "Greets readers at the end of every post.",
  "author": "You",
  "requires": "0.1.0",
  "license": "GPL-2.0-or-later",
  "permissions": ["content:filter", "users:read"],
  "hooks": ["the_content"]
}
```

Every hook the plugin subscribes to must be listed in `hooks`, and every permission a hook or
host function needs must be in `permissions` — the manifest is validated on pack and on install.
All fields are described on the [manifest](manifest.md) page.

## 4. Build

```bash
GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -buildmode=c-shared -o plugin.wasm .
```

`-buildmode=c-shared` matters: it produces a WASI *reactor* whose exports can be called after
initialisation. The default build mode produces a command whose runtime exits when `main`
returns, and the loader rejects it with a message telling you to use `c-shared`.

## 5. Pack and sign

```bash
go build -o wpgo-pkg ./internal/plugins/cmd/wpgo-pkg     # once, from the Pursae checkout
./wpgo-pkg keygen -out mykey                             # writes mykey.pub and mykey.key (keep .key private)
./wpgo-pkg pack -dir examples/plugins/hello-reader -wasm plugin.wasm -key mykey.key -out hello-reader-1.0.0.zip
./wpgo-pkg verify -pub mykey.pub hello-reader-1.0.0.zip
```

`pack` takes `plugin.json`, `readme.md`, `icon.svg` and the `assets/` directory from `-dir`.
`tools/build-plugins.sh` does steps 4 and 5 for every example plugin with a development key.

## 6. Install

Tell the site to trust your key, then upload the zip:

- `WPGO_PLUGIN_KEYS='<contents of mykey.pub>'` in the environment, or paste the key into the
  `plugin_trusted_keys` option. For local hacking you can instead set `plugin_allow_unsigned`.
- Plugins › Add Plugin › **Upload Plugin**, choose the zip, then **Activate**. The activation
  screen lists the permissions from the manifest; tick the ones you grant.

Open a post: the greeting is there. The plugin's log (the last lines are shown on the Plugins
screen) tells you when a call was refused or failed.

## 7. Iterate

`wpgo plugin new -slug my-plugin -sdk /path/to/pursae` writes `plugin.json`, `main.go`, a unit
test that uses `wpgotest`, `readme.md` and a `go.mod`; `wpgo plugin build -dir my-plugin [-key
mykey.key]` compiles to Wasm and packs in one step (`wpgo plugin keygen|pack|verify` are the
`wpgo-pkg` commands, so the released `wpgo` binary is enough to package a plugin).

Rebuild, re-pack, upload again (the same slug replaces the package), deactivate and activate.
During development run the site with `plugin_allow_unsigned` and skip the key. Unit-test your
callbacks on a normal Go toolchain with `wpgo.Dispatch` ([testing](testing.md)).

Next: the [shortcode-demo](https://github.com/pursae/pursae/tree/main/examples/plugins/shortcode-demo)
example uses shortcodes, REST routes, a settings page, assets, transients and a scheduled event
in about 150 lines.
