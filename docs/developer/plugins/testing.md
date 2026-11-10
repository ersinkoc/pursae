---
title: Testing plugins
description: Unit-test callbacks with wpgo.Dispatch on a normal Go toolchain, and run a plugin inside the real runtime.
weight: 140
section: plugins
---

The SDK compiles on every platform, not only wasip1: outside the host every host function returns
`wpgo.ErrNotInHost`, while hook registration and `wpgo.Dispatch` work. That is enough to unit
test callbacks with `go test` and no WebAssembly.

## Dispatch

`wpgo.Dispatch(request)` takes the JSON the host would send to `wpgo_handle` and returns the
JSON the plugin would answer: `{"value": …}` or `{"error": "…"}`.

```go
package main

import (
	"encoding/json"
	"strings"
	"testing"

	"wpgo/sdk/go/wpgo"
)

func TestFooterOnSinglePosts(t *testing.T) {
	wpgo.Init() // flushes registrations made in init(); safe to call more than once

	req := map[string]any{
		"plugin": "hello-reader", "hook": "the_content", "type": "filter",
		"callback": callbackFor(t, "the_content"),
		"value":    "<p>Hi</p>",
		"args":     []any{map[string]any{"id": 1, "type": "post", "is_single": true}},
	}
	raw, _ := json.Marshal(req)
	var resp struct {
		Value string `json:"value"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(wpgo.Dispatch(raw), &resp); err != nil || resp.Error != "" {
		t.Fatalf("dispatch: %v %s", err, resp.Error)
	}
	if !strings.HasSuffix(resp.Value, "Thanks for reading, reader!</p>") {
		t.Fatalf("got %q", resp.Value)
	}
}
```

Callback names are generated (`the_content#1`, `shortcode:box#2`, `rest:ns/route#3`). Keep the
logic in plain functions you can call directly and use `Dispatch` for the wiring; or find the name
by registering the hook in the test itself. Shortcode requests use `"type": "shortcode"` with
`"args": [attrs, content]`; REST requests `"type": "rest"` with `"args": [request]`.

Host functions in tests return `wpgo.ErrNotInHost`, so code that reads settings should fall back
to defaults — which the `Setting*` helpers do.

## Inside the runtime

The repository's `internal/plugins` tests show how to run a real `plugin.wasm` without a site:
build it, wrap it with `plugins.WritePackage`, load it into `plugins.NewRuntime` with a
`plugins.MemoryHost` (in-memory options, storage, posts, transients, events and a configurable
current user), then fire hooks on `rt.Hooks()` and check the results. That package is `internal/`,
so this only works for tests inside the Pursae repository; plugin authors use `wpgotest` below.

## Faking the host: `wpgotest`

`wpgo/sdk/go/wpgo/wpgotest` fakes the host for plain `go test`, so code that calls `GetOption`,
`Storage`, `Transients`, `GetPost`, `QueryPosts`, `CurrentUser`, `HTTPGet`/`HTTPRequest`,
`SendMail` or `AdminNotice` runs without Wasm or a site. The host is removed when the test ends.

```go
func TestRates(t *testing.T) {
	h := wpgotest.New(t)
	h.SetOption("plugin_rates_currency", "EUR")
	h.StubHTTP("https://api.example.com/rates", 200, `{"eur":1.1}`)
	h.AddPost(wpgo.Post{Title: "Hello", Content: "body"})
	h.SetUser(&wpgo.User{ID: 1, Caps: []string{"edit_posts"}})

	// ... call your code, then inspect what it did:
	_ = h.Storage()  // plugin storage
	_ = h.Mails()    // queued mail
	_ = h.Notices()  // admin notices
	_ = h.Requests() // outbound HTTP requests
}
```

An outbound URL without a stub fails like a refused request. Permissions are not enforced by the
fake; declare them in `plugin.json` as usual.

## On a site

- Run the site with `WPGO_WEB_DIR=web` and `plugin_allow_unsigned` during development; upload,
  activate, read the plugin log on the Plugins screen.
- The log receives your `wpgo.Log` lines, `println` output (stderr), refused registrations and
  failures. The server log has the same lines with `plugin=<slug>`.
- `WPGO_MARKET_DIR=/path/to/zips` lists your packages on Plugins › Add Plugin so you can install
  them with one click.
