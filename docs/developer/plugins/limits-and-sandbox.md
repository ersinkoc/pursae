---
title: Limits and the sandbox
description: "What a plugin can and cannot do at runtime: memory, time, messages, failures, isolation."
weight: 150
section: plugins
---

Plugins run inside wazero, a WebAssembly runtime written in Go. There is no cgo and no process
boundary; isolation comes from the Wasm module having nothing but linear memory and the imports
the host chose to give it.

## Isolation

| | |
|---|---|
| Filesystem | none: no preopens, so WASI file calls fail |
| Network | none: the only way out is `HTTPGet` / `HTTPRequest`, with the `http:outbound` permission, through the site's client (8 s timeout, 1 MiB body, loopback and private addresses blocked) |
| Environment, arguments | none |
| Clocks and randomness | available (`time.Now`, `crypto/rand`) |
| stdout / stderr | captured into the plugin log |
| Other plugins | invisible; options, storage and transients are scoped by slug |
| The site | only through host functions, each gated by a permission |

## Resource limits

| Limit | Default |
|-------|---------|
| Linear memory | 1024 pages = 64 MiB per plugin |
| Concurrency | one call at a time per plugin; a request waits at most one call timeout for a busy plugin, then skips it |
| Compiled code | kept in memory, compiled again at start-up (never loaded from a disk cache) |
| Hook, shortcode callback | 2 s |
| REST route handler | 10 s |
| `cron_*` callback | 30 s |
| Instantiation + `wpgo_init` | 10 s |
| Message across the boundary | 8 MiB |
| Post content | 1 MiB; title 1000, excerpt 10 000, slug 200 characters |
| REST request / response body | 1 MiB |
| Storage value, transient value | 1 MiB; keys 256 / 172 characters |
| HTTP response body | 1 MiB |
| Admin notice | 2000 characters |
| Settings page | 50 fields; labels 200, descriptions 2000 characters |
| Cron interval | 60 s to 1 year |
| Assets | 2 MiB per file, 16 MiB and 256 files per package |

## Failure handling

- A trap, timeout, `os.Exit`, panic (recovered by the SDK and reported) or malformed response is
  logged; the filter keeps its original value, a shortcode renders as empty, a REST route answers
  `500`.
- If the module instance died, the next call creates a new one and the plugin's `init` runs
  again; registrations it repeats are de-duplicated.
- After **five consecutive failures** the plugin is disabled: its hooks, shortcodes, routes,
  settings page and assets disappear, the Plugins screen shows the reason, and the status is
  saved so a restart does not revive it. Activate it again to reset. Permission errors never
  count.
- Calls into one plugin are serialised (a Wasm instance is single-threaded). A host function that
  fires a hook which would re-enter the same plugin on the same call chain is skipped and logged.

## What this means for your code

- Keep callbacks short; move slow work to a [scheduled event](scheduled-events.md) and cache
  results in transients.
- Do not rely on package-level state between calls: it survives while the instance lives, but an
  instance can be replaced at any time. Use storage for anything that must persist.
- Goroutines run only while the host is inside your export; there is no background execution.
- Memory use counts against the 64 MiB cap, and Go's runtime needs part of it — stream and page
  rather than loading everything.
