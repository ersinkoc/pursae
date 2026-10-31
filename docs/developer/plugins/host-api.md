---
title: Host API reference
description: Every host function a plugin can call, its SDK wrapper, arguments, result and the permission it needs.
weight: 50
section: plugins
---

Host functions are how a plugin reaches the site. Each one is a JSON request to a `wpgo` import;
the Go SDK wraps them in typed functions. A call the plugin is not allowed to make returns
`*wpgo.Error` with code `permission_denied` (`wpgo.IsPermissionDenied(err)`); invalid arguments
return `invalid_request`; failures inside the site return `host_error`. Outside the Wasm host
(unit tests) every function returns `wpgo.ErrNotInHost`.

## Hooks and logging

| SDK | Host import | Permission |
|-----|-------------|------------|
| `AddFilter(hook, priority, fn)` (generic over the value type), `AddAction(hook, priority, fn)` | `add_hook` | the hook's ([hooks](hooks.md)) |
| `OnInit(fn)` | — | runs after registrations are sent |
| `Log(level, msg)`, `Logf(format, …)` | `log` | none. Levels `debug`, `info`, `warning`, `error`; shown on the Plugins screen and in the server log |

## Options

| SDK | Host import | Permission |
|-----|-------------|------------|
| `GetOption(name) (string, found, error)` — non-string values come back JSON-encoded | `get_option` | `options:read` |
| `GetOptionJSON(name, &v) (found, error)` | `get_option` | `options:read` |
| `OptionBool(name, def) bool` | `get_option` | `options:read` |
| `UpdateOption(name, value) error` | `update_option` | `options:write` |

Readable: any option except secrets (`auth_*`, names containing `secret` or `password`,
`admin_email`, `plugin_*` of other plugins). Writable: only `<slug>_*` (dashes → underscores)
and the plugin's own `plugin_<slug>_*` settings. Names are at most 191 characters.

## Posts

| SDK | Host import | Permission |
|-----|-------------|------------|
| `GetPost(id) (*Post, found, error)` | `get_post` | `posts:read` |
| `QueryPosts(PostQuery) (*PostList, error)` | `query_posts` | `posts:read` |
| `InsertPost(PostInput) (*Post, error)` | `insert_post` | `posts:write` |
| `UpdatePost(id, PostInput) (*Post, error)` | `update_post` | `posts:write` |

`Post` has `ID, Title, Content, Excerpt, Status, Type, Slug, Date, Modified` (RFC 3339 in the
site's time zone), `Author` (user id), `Link` (permalink of published posts), `Terms` (map by
taxonomy: `category`, `post_tag`, each `{ID, Name, Slug, Taxonomy}`) and `Meta` (the post's meta
map).
`InsertPost` and `UpdatePost` refuse meta keys that start with an underscore: those are protected, the core and
other plugins keep trusted data there (the page builder's element tree is rendered unescaped, and plugins never
hold `unfiltered_html`).

`PostQuery`: `Type` (`post` default, `page`), `Status` (`publish` default, `draft`, `pending`,
`private`, `future`, `any`), `Search`, `Author`, `Term` (term id), `OrderBy` (`date`, `title`,
`modified`, `id`), `Order` (`DESC`, `ASC`), `PerPage` (1–100, default 10), `Page`. The result has
`Posts`, `Total`, `Pages`.

`PostInput` fields are pointers (`wpgo.String("…")`) so an update changes only what you set:
`Title` (≤1000 chars), `Content` (≤1 MiB), `Excerpt` (≤10 000), `Status`, `Type` (`post` or
`page`), `Slug`, `Meta` (merged; a `nil` value deletes the key). Content, title and excerpt go
through the same KSES filter as a user without `unfiltered_html`. A new post defaults to type
`post`, status `draft`, the default category, and the current user as author (none in cron). Posts
go through the same store functions as the admin, so slugs are made unique, revisions are kept and
the page cache is purged.

```go
p, err := wpgo.InsertPost(wpgo.PostInput{
	Title:   wpgo.String("Weekly digest"),
	Content: wpgo.String("<p>…</p>"),
	Status:  wpgo.String("publish"),
	Meta:    map[string]any{"digest_week": 43},
})
```

## Users

| SDK | Host import | Permission |
|-----|-------------|------------|
| `CurrentUser() (*User, error)` — `nil` for anonymous visitors and in cron | `get_current_user` | `users:read` |

`User` has `ID, Login, DisplayName, Roles, Caps` and `Can(cap) bool`. Plugins never see e-mail
addresses or password hashes.

## Shortcodes, REST routes, settings pages, assets

| SDK | Host import | Permission | Guide |
|-----|-------------|------------|-------|
| `AddShortcode(tag, fn)` | `register_shortcode` | `content:filter` + tag in manifest | [shortcodes](shortcodes.md) |
| `RegisterRestRoute(ns, route, methods, handler)` | `register_rest_route` | `rest:routes` | [REST routes](rest-routes.md) |
| `RegisterSettingsPage(page)`, `Setting(id)`, `SettingString/Bool/Float`, `SettingName(id)` | `register_settings_page`, `get_option` | `admin:settings`, `options:read` | [settings pages](settings-pages.md) |
| `EnqueueStyle(Style)`, `EnqueueScript(Script)` | `enqueue_style`, `enqueue_script` | `content:filter` | [assets](assets.md) |

## Scheduled events

| SDK | Host import | Permission |
|-----|-------------|------------|
| `ScheduleEvent(hook, interval, args) error` | `schedule_event` | `cron` + hook in manifest |
| `ScheduleSingleEvent(hook, at, args) error` | `schedule_event` | `cron` |
| `UnscheduleEvent(hook) error` | `unschedule_event` | `cron` |

See [scheduled events](scheduled-events.md).

## Storage and transients

| SDK | Host import | Permission |
|-----|-------------|------------|
| `Storage.Get(key) (string, found, error)`, `Storage.Set(key, value) error` | `storage_get`, `storage_set` | `storage` |
| `Transients.Get(key, &v) (found, error)`, `Transients.Set(key, value, ttl) error`, `Transients.Delete(key) error` | `transient_get`, `transient_set`, `transient_delete` | `storage` |

Storage is a private key/value table per plugin (keys ≤256 chars, values ≤1 MiB, strings); it
is deleted with the plugin. Transients are JSON values with a time-to-live (0 = none, keys ≤172
chars, values ≤1 MiB) in the site's transients table, scoped to the plugin and cleaned up daily.

## HTTP and notices

| SDK | Host import | Permission |
|-----|-------------|------------|
| `HTTPGet(url) (*HTTPResponse, error)` — `Status`, `Header`, `Body` (≤1 MiB), 8 s timeout, private and loopback addresses are blocked | `http_get` | `http:outbound` |
| `AdminNotice(kind, message) error` — kinds `NoticeInfo/Success/Warning/Error`, plain text ≤2000 chars, only meaningful inside an `admin_notices` callback | `add_admin_notice` | `admin:notices` |

## The wire format

For other languages, or curiosity: every import takes `(ptr, len)` of a UTF-8 JSON request in
the guest's memory and returns a packed `ptr<<32|len` of a JSON envelope `{"ok":true,"result":…}`
or `{"ok":false,"code":"…","error":"…"}` allocated with the guest's `wpgo_alloc`. The full ABI
(exports, request shapes, limits) is in `internal/plugins/README.md` of the repository.
