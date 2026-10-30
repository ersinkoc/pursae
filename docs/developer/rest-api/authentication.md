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

Use Application Passwords only over TLS: Basic authentication sends the password with every
request.

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
