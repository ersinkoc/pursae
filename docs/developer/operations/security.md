---
title: Security
description: What WordPress Go does for you, and the checklist for a production site.
weight: 80
section: operations
---

## Built in

- **Passwords** are hashed with argon2id. Sessions are server-side rows tied to a random cookie
  token; the cookie is `HttpOnly`, `SameSite=Lax` and `Secure` behind TLS. Sessions last 2 days,
  14 with "Remember Me".
- **Login throttling**: five failed logins from one address within 15 minutes block further
  attempts from that address.
- **Nonces** protect every admin form and the REST API (`X-WP-Nonce`), exactly like WordPress.
- **Security headers** on every response: `X-Content-Type-Options: nosniff`,
  `X-Frame-Options: SAMEORIGIN`, `Referrer-Policy: strict-origin-when-cross-origin`, a
  `Content-Security-Policy` (front end: `script-src 'self' 'unsafe-inline'`, no remote scripts;
  admin: what the block editor needs), and `Strict-Transport-Security` behind TLS.
- **Uploads are data, never code**: `/wp-content/uploads/` is served with a sandboxing CSP, so an
  uploaded HTML or SVG file cannot run scripts against the site.
- **Content filtering**: users without `unfiltered_html` and all plugins have their post content
  filtered (KSES) before it is stored.
- **Plugins are sandboxed**: WebAssembly with no filesystem, no network except what the host
  allows, a memory cap, a per-call timeout and permissions you approve
  ([limits and sandbox](../plugins/limits-and-sandbox.md)). Packages are signed; only keys you
  trust can install.
- **No third-party calls**: no Gravatar, no update pings except the opt-out Hub ping that sends
  only the site host and version ([Hub](../hub/index.md)).

## Checklist

1. Terminate TLS in a proxy and set `WPGO_SITE_URL` to the `https://` address
   ([reverse proxy and TLS](reverse-proxy-and-tls.md)).
2. Start with `docker/compose.prod.yaml` (see [Docker](docker.md#production-changes)): it takes the
   database password from `WPGO_DB_PASSWORD`, does not publish port 5432 and sets `WPGO_TRUST_DEV_KEY=0`.
3. List your own plugin signing keys in `WPGO_PLUGIN_KEYS` or the `plugin_trusted_keys` option; leave
   `plugin_allow_unsigned` off and never trust the public demo key (`WPGO_TRUST_DEV_KEY` must stay `0`).
4. Set `WPGO_SECRET` yourself when running several instances, and keep it secret.
5. Set `WPGO_DISALLOW_FILE_EDIT=1` if nobody should edit theme files from the admin.
6. Configure SMTP so password resets reach their recipients ([mail](mail.md)).
7. Back up the database and the data directories ([backups](backups.md)).
8. Review plugin permissions on activation; grant only what a plugin needs. A plugin that keeps
   failing is disabled automatically after five consecutive failures.
9. Keep the core up to date ([upgrading](../getting-started/upgrading.md)).

## Reporting a vulnerability

Open a private security advisory on the GitHub repository rather than a public issue.
