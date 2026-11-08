---
title: Security
description: What Pursae does for you, and the checklist for a production site.
weight: 80
section: operations
---

## Built in

- **Passwords** are hashed with argon2id. Sessions are server-side rows tied to a random cookie
  token; the cookie is `HttpOnly`, `SameSite=Lax` and `Secure` behind TLS. Sessions last 2 days,
  14 with "Remember Me".
- **Login throttling**: five failed logins from one address within 15 minutes block further
  attempts from that address, and twenty failures for one user name (from any address) block that
  name. Each attempt is counted before its password is checked, so parallel requests cannot slip past
  the limit. REST Basic authentication counts too. An unknown user name costs as much time as a wrong
  password, and at most twice as many password hashes as the server has CPUs run at once.
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
- **No third-party calls**: no Gravatar. The only service a site talks to is the Hub
  (`https://hub.pursae.com`, fixed in production builds), which gets the site URL, version, platform, license key, installed plugins and
  themes and usage events, never users or content ([what a site sends](../hub/index.md#what-a-site-sends-statistics));
  Only a development build (`-tags devhub`, `WPGO_DEV_HUB_URL=off`) can disconnect it.

## Checklist

1. Terminate TLS in a proxy and set `WPGO_SITE_URL` to the `https://` address
   ([reverse proxy and TLS](reverse-proxy-and-tls.md)).
2. Start with `docker/compose.prod.yaml` (see [Docker](docker.md#production-changes)): it takes the
   database password from `WPGO_DB_PASSWORD`, does not publish port 5432 and sets `WPGO_TRUST_DEV_KEY=0`.
3. List your own plugin signing keys in `WPGO_PLUGIN_KEYS` or the `plugin_trusted_keys` option; leave
   `plugin_allow_unsigned` off and never trust the public demo key (`WPGO_TRUST_DEV_KEY` must stay `0`).
4. Set `WPGO_SECRET` yourself when running several instances, and keep it secret.
5. Set `WPGO_DISALLOW_FILE_EDIT=1` (or Pursae Security › Hardening › Disable file editors) if nobody
   should edit theme files; it applies to the admin editors and the REST theme-file routes alike.
6. Let clients reach the server only through your proxy (`docker/compose.prod.yaml` publishes the port on
   `127.0.0.1`), and set `WPGO_TRUSTED_PROXIES` to the proxy's address when other machines share its
   private network ([reverse proxy](reverse-proxy-and-tls.md)).
7. Configure SMTP so password resets reach their recipients ([mail](mail.md)).
8. Back up the database and the data directories ([backups](backups.md)).
9. Review plugin permissions on activation; grant only what a plugin needs. A plugin that keeps
   failing is disabled automatically after five consecutive failures.
10. The core already keeps visitors from listing accounts, without any plugin: `/?author=N` answers 404, `/wp/v2/users` needs a
   login, the users sitemap is empty and the login form gives the same message for an unknown account and a wrong password.
   Set `WPGO_EXPOSE_USERS=1` only if you need the public author list (a headless front end, say). Author archive pages
   (`/author/<nicename>`) stay public for authors with published posts and the slug comes from the account's login name, so
   set a *display name* and nickname that differ from the login for every account that publishes. **Pursae Security** (Plugins ›
   Installed Plugins; [built-in plugins](../built-in-plugins/adminsecgo.md)) adds two-step login, a custom login URL and
   an activity log on top, and can relax these defaults.
11. Keep the core up to date ([upgrading](../getting-started/upgrading.md)) and rebuild the images
    regularly: the Docker build uses the latest `golang:1.27` image, and `govulncheck ./...` should stay clean.

## Reporting a vulnerability

Open a private security advisory on the GitHub repository rather than a public issue.
