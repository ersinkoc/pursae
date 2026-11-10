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
| `admin_notices` | action | — | none. Queue a notice with `wpgo.AdminNotice`; it is escaped and shown on the current admin screen | `users:read` | `user_register`, `wp_login` (action arguments: the user id and the user as `CurrentUser` returns it) |
| `admin:notices` |
| `wpgo_post_stats` | filter | `map[string]any` of statistics shown in the classic editor's publish box (key → value; underscores become spaces) | `post`: `{"id", "title", "content"}` | `posts:read` |

### Forms

Fired by Pursae Contact Form. The permission is `forms:access` for both.

| Hook | Type | Value | Arguments | Permission |
|------|------|-------|-----------|------------|
| `wpcf7_mail_sent` | action | — | one map: `contact_form_id` (int), `title` (string), `posted_data` (map of field name → submitted value). Fired after the mail was sent | `forms:access` |
| `wpcf7_spam` | filter | the spam verdict (bool, `true` = spam); return a bool to override it | `contact_form_id` (int), `posted_data` (map), `remote_ip` (string), `user_agent` (string) | `forms:access` |

### Media

| Hook | Type | Value | Arguments | Permission |
|------|------|-------|-----------|------------|
| `image_editor_output_format` | filter | `map[string]any` source MIME type → output MIME type for the generated image sizes, e.g. `{"image/jpeg": "image/webp"}`; it starts as WordPress' default (HEIC → JPEG) plus the Settings › Media › Image output format choice. A mapping that changes the format converts the main image too; `image/jpeg`, `image/png`, `image/gif` and `image/webp` can be written | the upload's file name, its MIME type | — |

### Posts

| Hook | Type | Arguments | Permission |
|------|------|-----------|------------|
| `save_post_<type>`, then `save_post` | action | the post id and the post (the shape `GetPost` returns). Fired after a post or page was saved — editor, REST, quick edit, scheduled publishing, a plugin's `InsertPost`/`UpdatePost` — never for other post types | `posts:read` |

### Pursae Headless entries

Fired for the entries of the Headless content types (REST, GraphQL, admin, uploads, the scheduler and
relation changes alike). All need the `headless:entries` permission. Private and password attributes
are never passed to a plugin.

| Hook | Type | Value / arguments | Permission |
|------|------|-------------------|------------|
| `headless_entry_validate` | filter | Before an entry is stored. Value: `{"type": "<uid>", "documentId": "" (new entry) or the id, "locale", "action": "create"/"update"/"publish", "data": {…attributes}}`. Return the same object. Add `"errors": ["message", {"field": "title", "message": "…"}]` to reject the save with a 400 `ValidationError` (at most 20 errors); change scalar attributes in `data` to derive values (they are validated again like API input; a bad value rejects the save). Changes to relations, media, components and other keys are ignored. 5 s budget | `headless:entries` |
| `headless_entry_saved` | action | After an entry was created or updated: `{"event": "entry.create"/"entry.update", "type", "documentId", "locale", "status", "entry": {…}}` (the entry as webhooks carry it) | `headless:entries` |
| `headless_entry_published`, `headless_entry_unpublished`, `headless_entry_deleted` | action | Same payload, with `event` `entry.publish`, `entry.unpublish`, `entry.delete` | `headless:entries` |

### Lifecycle

| Hook | Type | Arguments | Permission |
|------|------|-----------|------------|
| `init` | action | none. Fired after all active plugins are loaded at start-up; after a plugin is activated, updated or loaded by another instance it fires for that plugin only | — |
| `activate` | action | none. Fired for the plugin itself when an administrator activates it (SDK: `wpgo.OnActivate`) | — |
| `deactivate` | action | none. Fired for the plugin itself when it is deactivated, before it is unloaded (`wpgo.OnDeactivate`); 5 s budget | — |
| `uninstall` | action | none. Fired for the plugin itself when it is deleted; an inactive plugin is loaded just for this call (`wpgo.OnUninstall`); 5 s budget. Afterwards its options, transients and events are removed | — |
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
| `headless:entries` | everything starting with `headless_entry_` |

Subscribing to a hook the core does not fire yet is allowed; the callback simply never runs. Of the
names above the core fires today `the_content`, `save_post`, `save_post_<type>`,
`wpgo_post_stats`, `admin_notices`, the `cron_*` events, `user_register` and `wp_login`
(actions), and the `wp_head` / `wp_footer` filters (`content:filter`): the value is the markup
printed so far, return it with your additions; without `content:html` the additions are
sanitised like any content filter (scripts and styles are dropped). `the_title`,
`transition_post_status`, `wp_insert_post_data` and the rest are reserved.

## Behaviour to know

- **Timeouts.** A content hook or shortcode callback has 2 seconds, a REST route handler 10
  seconds and a `cron_*` callback 30 seconds (REST and cron calls are exempt from the slow-call
  rule below; only the hard timeout applies). A callback that overruns is killed, the filter keeps
  its value, and the plugin is re-instantiated on the next call.
- **Failures count.** Five consecutive failures (panic, timeout, malformed response) disable the
  plugin until it is activated again. A call that succeeds but takes 75 % of its budget (1.5 s)
  counts too: it held up every other request to the plugin. Permission errors do not count.
- **A skipped call is never cached.** When a callback is skipped because the instance stayed busy for a
  call timeout, the request is flagged and Pursae Cache does not store that (incomplete) page.
- **One call at a time.** A plugin instance runs one callback at a time (its memory is its state,
  so there is no pool of instances). A request that finds the plugin busy waits at most one call
  timeout, or less if the request's own deadline is nearer, then skips the callback (logged) and
  the filter keeps its value.
- **Re-instantiation.** After a timeout or a crash the next call starts a fresh instance, which
  runs your `init` functions and `OnInit` again; the registrations of the old instance are dropped
  first, so register everything there (conditional registrations follow the current options).
- **Sanitised output.** Without `content:html`, the HTML a `content:filter` hook adds is passed
  through KSES (only the part you changed; the core's markup around it is kept), and a change
  inside a `<script>`, `<style>` or comment is dropped.
- **Re-entrancy.** If a host function you call fires a hook that would call back into your
  plugin on the same call chain, that callback is skipped (and logged) instead of deadlocking.
- **Types survive.** The host decodes your return value into the Go type of the original value,
  so a `string` filter must return a string, and `wpgo_post_stats` a JSON object.
