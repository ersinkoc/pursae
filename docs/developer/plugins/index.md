---
title: Plugins overview
description: How WordPress Go plugins work — WebAssembly in a sandbox, permissions you approve, signed packages — and what the SDK offers.
weight: 10
section: plugins
---

A WordPress Go plugin is a Go program compiled to **WebAssembly** and packed in a signed zip. The
site runs it in a sandbox ([wazero](https://wazero.io), pure Go) and lets it reach the site only
through a small host API gated by **permissions** the administrator approves when activating it.

```text
┌──────────── WordPress Go ────────────┐      ┌──── plugin.wasm (sandbox) ────┐
│ the_content ──► hooks ───────────────┼─────►│ wpgo.AddFilter("the_content") │
│ [tag] ───────► shortcodes ───────────┼─────►│ wpgo.AddShortcode("tag")      │
│ /wp-json/<slug>/v1/… ► REST routes ──┼─────►│ wpgo.RegisterRestRoute(...)   │
│ Settings › Plugin ◄── settings page ─┼──────┤ wpgo.RegisterSettingsPage()   │
│ jobs table ───► cron_* actions ──────┼─────►│ wpgo.ScheduleEvent(...)       │
│ options, posts, users, storage,      │◄─────┤ wpgo.GetOption, GetPost, …    │
│ transients, HTTP, notices, assets    │      │ (each needs a permission)     │
└──────────────────────────────────────┘      └───────────────────────────────┘
```

## Why Wasm

- **Isolation.** A plugin has no filesystem, no sockets, no environment. It sees only JSON
  messages from the host and can call only the host functions its permissions allow.
- **Containment.** A panic, an infinite loop or `os.Exit` in a plugin is caught: the call times
  out or fails, the filter keeps its original value, the module is re-instantiated. After five
  consecutive failures the plugin is disabled and the admin sees why.
- **Portability.** One `plugin.wasm` runs on every platform WordPress Go runs on. The SDK is Go
  today (`wpgo/sdk/go/wpgo`); the ABI is plain JSON, so other languages can follow.

## What a plugin can do

| Need | SDK | Permission |
|------|-----|------------|
| change rendered content, titles, excerpts | `AddFilter("the_content", …)` | `content:filter` |
| run code at start-up or on admin screens | `AddAction("init", …)`, `AddAction("admin_notices", …)` | — / `admin:notices` |
| shortcodes `[tag attr="v"]…[/tag]` | `AddShortcode` | `content:filter` |
| REST endpoints under `/wp-json/<slug>/v1/` | `RegisterRestRoute` | `rest:routes` |
| read and write posts | `GetPost`, `QueryPosts`, `InsertPost`, `UpdatePost` | `posts:read`, `posts:write` |
| know who is logged in | `CurrentUser` | `users:read` |
| scheduled tasks | `ScheduleEvent`, `AddAction("cron_…")` | `cron` |
| a settings page under Settings | `RegisterSettingsPage`, `Setting*` | `admin:settings`, `options:read` |
| options | `GetOption`, `UpdateOption` | `options:read`, `options:write` |
| private data and caches | `Storage`, `Transients` | `storage` |
| outbound HTTP GET | `HTTPGet` | `http:outbound` |
| CSS and JS on the site or in the admin | `EnqueueStyle`, `EnqueueScript` + `assets/` in the package | `content:filter` |
| dashboard notices | `AdminNotice` | `admin:notices` |

## Signing and trust

Packages are signed with Ed25519. A site installs a package only if its signature verifies
against a key the site trusts (`WPGO_PLUGIN_KEYS` or the `plugin_trusted_keys` option), or if
the administrator explicitly allows unsigned packages for development. Plugins published through
the [Hub](../hub/publishing.md) are re-signed with the Hub's key, which sites trust
automatically. Static files under `assets/` are covered by the signature through their digests
in `plugin.json`.

## Start here

1. [Your first plugin](your-first-plugin.md) — from `go mod init` to an installed package.
2. [The manifest](manifest.md), [hooks](hooks.md) and the [host API](host-api.md) references.
3. Feature guides: [shortcodes](shortcodes.md), [REST routes](rest-routes.md),
   [settings pages](settings-pages.md), [scheduled events](scheduled-events.md),
   [assets](assets.md).
4. [Permissions](permissions.md), [packaging and signing](packaging-and-signing.md),
   [publishing](publishing.md), [testing](testing.md), [limits and sandbox](limits-and-sandbox.md).
