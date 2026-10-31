---
title: Permissions
description: The permission vocabulary, what each one unlocks, and how granting works.
weight: 110
section: plugins
---

A plugin declares the permissions it needs in `plugin.json`; the administrator sees them on the
activation screen and grants some or all. The plugin runs with the **intersection** of declared
and granted permissions. A host function or hook that needs a permission the plugin lacks fails
with `permission_denied` — logged, not fatal.

| Permission | Shown to the admin as | Unlocks |
|------------|----------------------|---------|
| `options:read` | Read site options | `GetOption` and friends (secrets and other plugins' options are hidden); reading the plugin's own settings page values |
| `options:write` | Create and change site options | `UpdateOption` for `<slug>_*` and `plugin_<slug>_*` options. A core option is never writable, even when its name starts with your slug (a plugin slugged `default` cannot write `default_role`), and neither are names starting with `auth_`, `wpgo_`, `native_` or another plugin's `plugin_` prefix |
| `posts:read` | Read posts and receive post data from post lifecycle hooks | `GetPost`, `QueryPosts`; hooks `save_post`, `save_post_*`, `wp_insert_post`, `publish_post`, `transition_post_status`, `before_delete_post`, `deleted_post`, `trashed_post`, `untrashed_post`, `post_updated`, `wpgo_post_stats` |
| `posts:write` | Create and change posts | `InsertPost`, `UpdatePost`; hooks `wp_insert_post_data`, `wp_insert_attachment_data`, `pre_post_update`, `content_save_pre`, `title_save_pre`, `excerpt_save_pre` |
| `users:read` | See who the current user is | `CurrentUser` |
| `forms:access` | See what visitors submit in contact forms and decide what counts as spam | hooks `wpcf7_*` (Pursae Contact Form): `wpcf7_mail_sent` carries the submitted fields, `wpcf7_spam` the fields, IP and user agent and lets the plugin change the spam verdict |
| `content:filter` | Modify content shown to visitors | hooks `the_*`, `comment_text`, `widget_text`, `widget_title`, `get_the_excerpt`, `wp_title`, `document_title_parts`, `body_class`, `post_class`; `AddShortcode`; `EnqueueStyle`, `EnqueueScript` |
| `rest:routes` | Add REST API endpoints | `RegisterRestRoute` |
| `admin:settings` | Add a settings page to the admin | `RegisterSettingsPage` |
| `admin:notices` | Show notices in the admin dashboard | hook `admin_notices` (and `all_admin_notices`, …); `AdminNotice` |
| `cron` | Run scheduled tasks | hooks `cron_*`, `wpgo_cron*`; `ScheduleEvent`, `ScheduleSingleEvent`, `UnscheduleEvent` |
| `storage` | Store private plugin data | `Storage`, `Transients` |
| `http:outbound` | Make outbound HTTP requests | `HTTPGet` |

No permission is needed for `Log`, for `init`, or for hooks the vocabulary does not classify.

## Guidelines

- Ask for what the plugin needs today, not what it might need. The activation screen is the
  moment a site owner decides whether to trust you; a content plugin asking for `posts:write`
  and `http:outbound` looks suspicious.
- Degrade gracefully: `wpgo.IsPermissionDenied(err)` lets you fall back when an optional
  permission was not granted.
- Explain permissions in `readme.md`; the Hub shows the list on the marketplace page.

## How it is enforced

The permission check happens in the host before your request is decoded, and again when a hook
is registered (`add_hook` is refused for a hook whose permission is missing, so the callback never
runs). The manifest validator also cross-checks: a hook or shortcode that needs a permission must
have it declared, or the package is rejected on pack and on install.
