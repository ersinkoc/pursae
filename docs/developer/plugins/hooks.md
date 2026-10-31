---
title: Hooks
description: Every action and filter the core fires, with their arguments, and how plugin callbacks receive them.
weight: 40
section: plugins
---

Hooks work as in WordPress: **filters** receive a value and return it (changed or not),
**actions** receive arguments and return nothing. Callbacks run in ascending priority order
(default 10); the core's own callbacks and every plugin's share one registry, so a plugin can run
before or after core behaviour by choosing its priority.

```go
wpgo.AddFilter("the_content", 20, func(ctx wpgo.Context, content string, args []any) string { … })
wpgo.AddAction("admin_notices", wpgo.DefaultPriority, func(ctx wpgo.Context, args []any) { … })
```

`AddFilter` is generic: the value is decoded from JSON into the type you declare (`string`,
`map[string]any`, a struct). If decoding fails, the host keeps the original value and logs it.
`ctx` carries the plugin slug, the hook name and the callback id. Every hook must be listed in
the manifest's `hooks` array.

## Hooks the core fires

### Content

| Hook | Type | Value | Arguments | Permission |
|------|------|-------|-----------|------------|
| `the_content` | filter | the rendered post HTML (after blocks, autop and [shortcodes](shortcodes.md)) | `post`: `{"id": 12, "type": "post", "is_single": true}` — `is_single` is false in archives and the Twenty Twenty-One excerpt loop | `content:filter` |

### Admin

| Hook | Type | Value | Arguments | Permission |
|------|------|-------|-----------|------------|
| `admin_notices` | action | — | none. Queue a notice with `wpgo.AdminNotice`; it is escaped and shown on the current admin screen | `admin:notices` |
| `wpgo_post_stats` | filter | `map[string]any` of statistics shown in the classic editor's publish box (key → value; underscores become spaces) | `post`: `{"id", "title", "content"}` | `posts:read` |

### Media

| Hook | Type | Value | Arguments | Permission |
|------|------|-------|-----------|------------|
| `image_editor_output_format` | filter | `map[string]any` source MIME type → output MIME type for the generated image sizes, e.g. `{"image/jpeg": "image/webp"}`; it starts as WordPress' default (HEIC → JPEG) plus the Settings › Media › Image output format choice. A mapping that changes the format converts the main image too; `image/jpeg`, `image/png`, `image/gif` and `image/webp` can be written | the upload's file name, its MIME type | — |

### Lifecycle

| Hook | Type | Arguments | Permission |
|------|------|-----------|------------|
| `init` | action | none. Fired after all active plugins are loaded at start-up and after a plugin is activated | — |
| `cron_<name>` | action | the event payload: `{"plugin": "<slug>", "args": {…}}` as the single argument. Fired by the scheduler for events the plugin created with `ScheduleEvent` ([scheduled events](scheduled-events.md)) | `cron` |

Hooks fire with the request's user attached, so `wpgo.CurrentUser()` works inside `the_content`
and `admin_notices` callbacks; cron callbacks see no user.

## Reserved names

The permission rules already classify the WordPress hook names the core will fire in later
versions, so a manifest written today stays valid:

| Permission | Hook names |
|------------|-----------|
| `content:filter` | everything starting with `the_` (`the_title`, `the_excerpt`, …), `comment_text`, `widget_text`, `widget_title`, `get_the_excerpt`, `wp_title`, `document_title_parts`, `body_class`, `post_class` |
| `posts:read` | `save_post`, `save_post_<type>`, `wp_insert_post`, `publish_post`, `transition_post_status`, `before_delete_post`, `deleted_post`, `trashed_post`, `untrashed_post`, `post_updated`, `wpgo_post_stats` |
| `posts:write` | `wp_insert_post_data`, `wp_insert_attachment_data`, `pre_post_update`, `content_save_pre`, `title_save_pre`, `excerpt_save_pre` |
| `admin:notices` | `admin_notices`, `all_admin_notices`, `network_admin_notices`, `user_admin_notices` |
| `cron` | everything starting with `cron_` or `wpgo_cron` |

Subscribing to a hook the core does not fire yet is allowed; the callback simply never runs.

## Behaviour to know

- **Timeouts.** A callback has 2 seconds. A callback that overruns is killed, the filter keeps
  its value, and the plugin is re-instantiated on the next call.
- **Failures count.** Five consecutive failures (panic, timeout, malformed response) disable the
  plugin until it is activated again. Permission errors do not count.
- **Re-entrancy.** If a host function you call fires a hook that would call back into your
  plugin on the same call chain, that callback is skipped (and logged) instead of deadlocking.
- **Types survive.** The host decodes your return value into the Go type of the original value,
  so a `string` filter must return a string, and `wpgo_post_stats` a JSON object.
