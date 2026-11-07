---
title: Authentication
description: Cookie + nonce for browser clients, Application Passwords for everything else, and what anonymous requests can do.
weight: 20
section: rest-api
---

Requests without credentials are **anonymous**: they can read public content (published posts,
pages, media, terms, public user profiles, comments) and nothing else.

## Cookie and nonce (browser clients)

A logged-in user's session cookie authenticates a request **only together with a nonce**, exactly
as in WordPress, to stop cross-site requests from using the cookie:

- header `X-WP-Nonce: <nonce>`, or query `?_wpnonce=<nonce>`, with a nonce for the action
  `wp_rest`;
- a cookie without a nonce is treated as anonymous (`401` on writes);
- a wrong nonce returns `403 rest_cookie_invalid_nonce`.

Admin pages expose the nonce in `wpApiSettings.nonce`, and every authenticated REST response
returns a fresh one in the `X-WP-Nonce` response header. The editor's `wp.apiFetch` sends it
automatically.

## Application Passwords (scripts, apps, CI)

HTTP Basic authentication with the user's login and an Application Password — a 24-character
password shown once, grouped in blocks of four; spaces are ignored:

```bash
curl -u 'editor:abcd efgh ijkl mnop qrst uvwx' \
     -H 'Content-Type: application/json' \
     -d '{"title":"From the API","status":"draft"}' \
     https://blog.example.com/wp-json/wp/v2/posts
```

Each password is hashed like a login password, records its last use and address, and can be
revoked on its own. Create and revoke them under Users › Profile › Application Passwords (the
password is shown once), through the REST controller
(`/wp/v2/users/me/application-passwords`, `POST` returns the plaintext password once), or let an
app request one through the authorization flow at `/wp-admin/authorize-application` (the REST
index advertises it). A wrong or missing password returns `401 incorrect_password`.

Basic authentication counts toward the login throttle: after five failures from one address within
15 minutes further Basic-auth requests from it get `429 too_many_retries` (Pursae Security, when active,
applies its own lockouts instead). The server verifies the most recently used password first and
remembers a successful match for a few minutes, so a working client does not pay for a password hash
on every request; a revoked password stops working at once.

Use Application Passwords only over TLS: Basic authentication sends the password with every
request.

## Application Password scopes

A password can be limited to **scopes**. A password without scopes has all of its user's
capabilities (WordPress behaviour); a password with scopes is narrowed twice:

1. the user's capabilities are cut down to the ones the scopes need, so every capability check
   in a handler sees the smaller set, and
2. each route belongs to one scope; a route of another scope, or a route no scope covers (plugin
   routes, the Application Password routes themselves), returns
   `403 rest_scope_forbidden` with `data.required_scope`.

Scopes never add a capability the user does not have: an editor's password with the `plugins`
scope still cannot activate plugins.

| Scope | Routes | Capabilities kept |
|-------|--------|-------------------|
| `content` | `/wp/v2/posts`, `pages`, `blocks`, `categories`, `tags`, `comments`, `types`, `taxonomies`, `search`, `block-renderer`, `/web-builder/v2`, `/headlessgo/v1`, `/pll/v1`, `/batch/v1` | post and page caps, `manage_categories`, `moderate_comments`, `unfiltered_html` |
| `media` | `/wp/v2/media` | `upload_files` and the post caps attachments use |
| `themes` | `/wp/v2/templates`, `template-parts`, `global-styles`, `themes`, `menus`, `menu-items`, `menu-locations`, `navigation`, `sidebars`, `widgets`, `/pursae/v1/themes` | `switch_themes`, `edit_theme_options`, `edit_themes`, `install_themes`, `delete_themes` |
| `plugins` | `/pursae/v1/plugins` | `activate_plugins`, `install_plugins`, `delete_plugins` |
| `cache` | `/pursae/v1/cache` | `manage_options` |
| `seo` | `/seogo/v1`, `/pursae/v1/seo` | post caps, `manage_options`, the `seogo_*` caps |
| `settings` | `/wp/v2/settings`, `/pursae/v1/options` | `manage_options` |
| `users` | `/wp/v2/users` | `list_users`, `create_users`, `edit_users`, `promote_users`, `delete_users` |

Every scoped password may also read the REST index, `/wp/v2/users/me` and use the MCP endpoint
`/pursae/v1/mcp` (whose tools are checked route by route). Batch requests (`/batch/v1`) re-check
each inner request. On `/wp/v2/users/me` a scoped password may edit the profile fields (name, URL,
description, …) but changing `email`, `password` or `roles` needs the `users` scope
(`403 rest_scope_forbidden` otherwise). A password whose stored scopes are all unknown (for example a
scope removed in a later release) is refused rather than given full access.

Set scopes with the checkboxes under **Profile › Application Passwords › Limit access to**, or
through REST: `scopes` is an array (or a comma list) on `POST /wp/v2/users/me/application-passwords`
and on `POST …/application-passwords/{uuid}`; the responses include `scopes` (`[]` means full
access). A scoped password cannot create, change or revoke Application Passwords.

```bash
curl -u admin:… -H 'Content-Type: application/json' \
     -d '{"name":"Cursor","scopes":["content","media","seo"]}' \
     https://blog.example.com/wp-json/wp/v2/users/me/application-passwords
```

## Capabilities

Writes are checked against the user's capabilities the way WordPress does: `edit_posts` for
posts and blocks, `edit_pages` for pages, `upload_files` for media, `manage_options` or
`edit_theme_options` for settings and global styles, `manage_categories` for terms,
`edit_theme_options` for sidebars and widgets, and the meta capabilities (`edit_post`,
`delete_post`, …) for individual items. An authenticated user without the capability receives
`403 rest_forbidden`; an anonymous one `401 rest_cannot_create`-style errors.

## Plugin routes

Plugin routes get the same authentication: the handler receives the user (or none) and decides
([plugin routes](plugin-routes.md)).
