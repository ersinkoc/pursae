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
| `options:read` | Read site options | `GetOption` and friends: the plugin's own options (`<slug>_*`, `plugin_<slug>_*`) and the public core options listed below. Everything else — secrets, users, mail, licenses, other plugins' options, the built-in plugins' settings — reads as not found |
| `options:write` | Create and change site options | `UpdateOption` for `<slug>_*` and `plugin_<slug>_*` options, stored in the plugin's own namespace (see below), so they can never overwrite a core or built-in option. Names of core options are refused even when they start with your slug (a plugin slugged `default` cannot write `default_role`), and so are names starting with `auth_`, `wpgo_`, `native_`, `pursae_` or another plugin's `plugin_` prefix |
| `posts:read` | Read posts and receive post data from post lifecycle hooks | `GetPost`, `QueryPosts`; hooks `save_post`, `save_post_*` and `wpgo_post_stats` (fired today), and the reserved `wp_insert_post`, `publish_post`, `transition_post_status`, `before_delete_post`, `deleted_post`, `trashed_post`, `untrashed_post`, `post_updated` ([not fired yet](hooks.md#reserved-names)) |
| `posts:write` | Create and change posts | `InsertPost`, `UpdatePost`; the reserved hooks `wp_insert_post_data`, `wp_insert_attachment_data`, `pre_post_update`, `content_save_pre`, `title_save_pre`, `excerpt_save_pre` ([not fired yet](hooks.md#reserved-names)) |
| `users:read` | See who the current user is | `CurrentUser` |
| `forms:access` | See what visitors submit in contact forms and decide what counts as spam | hooks `wpcf7_*` (Pursae Contact Form): `wpcf7_mail_sent` carries the submitted fields, `wpcf7_spam` the fields, IP and user agent and lets the plugin change the spam verdict |
| `content:filter` | Modify content shown to visitors | hooks `the_*`, `comment_text`, `widget_text`, `widget_title`, `get_the_excerpt`, `wp_title`, `document_title_parts`, `body_class`, `post_class`; `AddShortcode`; `EnqueueStyle`, `EnqueueScript` (scripts also need one of the two below). HTML the plugin adds goes through KSES (`wp_kses_post`) |
| `content:html` | Show unfiltered HTML and scripts to visitors (they also run for logged-in administrators: full control of the site) | shortcode and content-filter output is not sanitised; `EnqueueScript` on the front end. **Not ticked by default** |
| `admin:scripts` | Run code in the admin (full control of the site) | `EnqueueScript` with `wpgo.Admin` or `wpgo.Both`; admin styles on every admin screen. **Not ticked by default** |
| `rest:routes` | Add REST API endpoints | `RegisterRestRoute` |
| `admin:settings` | Add a settings page to the admin | `RegisterSettingsPage`; `EnqueueStyle` with `wpgo.Admin` or `wpgo.Both` (printed on the plugin's own settings page only) |
| `admin:notices` | Show notices in the admin dashboard | hook `admin_notices` (and `all_admin_notices`, …); `AdminNotice` |
| `cron` | Run scheduled tasks | hooks `cron_*`, `wpgo_cron*`; `ScheduleEvent`, `ScheduleSingleEvent`, `UnscheduleEvent` |
| `storage` | Store private plugin data | `Storage`, `Transients` |
| `http:outbound` | Make outbound HTTP requests | `HTTPGet` |

No permission is needed for `Log`, for `init`, or for hooks the vocabulary does not classify.

### Options a plugin can read

Its own `<slug>_*` (dashes as underscores) and `plugin_<slug>_*` options, plus these public core
options: `blogname`, `blogdescription`, `siteurl`, `home`, `WPLANG`, `timezone_string`,
`gmt_offset`, `date_format`, `time_format`, `start_of_week`, `use_smilies`, `default_category`,
`default_post_format`, `posts_per_page`, `posts_per_rss`, `rss_use_excerpt`, `show_on_front`,
`page_on_front`, `page_for_posts`, `blog_public`, `default_ping_status`, `default_comment_status`,
`require_name_email`, `comment_registration`, `close_comments_for_old_posts`,
`close_comments_days_old`, `show_comments_cookies_opt_in`, `thread_comments`,
`thread_comments_depth`, `page_comments`, `comments_per_page`, `default_comments_page`,
`comment_order`, `show_avatars`, `avatar_rating`, `avatar_default`, the image size options
(`thumbnail_size_w` … `large_size_h`, `thumbnail_crop`), `uploads_use_yearmonth_folders`,
`permalink_structure`, `category_base`, `tag_base`, `template`, `stylesheet`, `sticky_posts`,
`wp_page_for_privacy_policy`, `site_icon`, `users_can_register`, `blog_charset`, `html_type`.

A plugin's options are stored as `wasm:<slug>:<name>` in the options table; the plugin keeps using
the plain name (`GetOption("hello_gopher_footer")`), the namespace is added by the host. Built-in
plugin slugs (`classic-editor`, `wpgo-cache`, `golementor`/`web-builder`, `adminsecgo`,
`backup-manager`, `pixelgo`, … every plugin on the Plugins screen marked built in) cannot be used by
a package.

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
