---
title: Configuration
description: Every WPGO_* environment variable, what it does and its default.
weight: 10
section: operations
---

Pursae is configured entirely through environment variables. Everything else (site title,
permalinks, mail sender, cache settings, trusted plugin keys) lives in the options table and is
edited in the admin.

## Core

| Variable | Default | Purpose |
|----------|---------|---------|
| `DATABASE_URL` | `postgres://wpgo:wpgo@localhost:5432/wpgo?sslmode=disable` | PostgreSQL connection string (pgx URL format) |
| `WPGO_ADDR` | `:8080` | address the HTTP server listens on |
| `WPGO_SITE_URL` | `http://localhost:8080` | public URL without a trailing slash; written to the `siteurl` and `home` options by the installer |
| `WPGO_UPLOADS_DIR` | `./data/uploads` | media library files. The directories next to it hold plugins (`../plugins`), the compiled-plugin cache (`../plugin-cache`), themes (`../themes`) and development mail (`../mail`) |
| `WPGO_SECRET` | generated on first start and stored in the settings | secret for session cookies and nonces. Set it when you run more than one instance against one database so every instance signs the same way |
| `WPGO_WEB_DIR` | embedded | read admin templates, theme templates, CSS and JS from this directory instead of the binary, reloading per request (development) |

## Mail

Without `WPGO_SMTP_HOST`, messages are written as `.eml` files to `WPGO_MAIL_DIR` so you can
inspect them. See [Mail](mail.md).

| Variable | Default | Purpose |
|----------|---------|---------|
| `WPGO_SMTP_HOST` | — | SMTP server |
| `WPGO_SMTP_PORT` | `587` | `465` means implicit TLS, anything else STARTTLS when offered |
| `WPGO_SMTP_USER`, `WPGO_SMTP_PASS` | — | credentials |
| `WPGO_MAIL_FROM` | `<Site title> <wordpress@host>` | sender; the host is taken from the site URL |
| `WPGO_MAIL_DIR` | `mail/` next to uploads | where `.eml` files go when SMTP is off |

## Themes and plugins

| Variable | Default | Purpose |
|----------|---------|---------|
| `WPGO_THEMES_DIR` | `<data>/themes` | theme packages (`<slug>/theme.json`) and the overrides saved by the Theme File Editor |
| `WPGO_DISALLOW_FILE_EDIT` | off | set to `1`/`true` to make the Theme File Editor read-only (like `DISALLOW_FILE_EDIT`) |
| `WPGO_PLUGIN_KEYS` | — | comma-separated base64 Ed25519 public keys trusted to sign plugin packages, in addition to the `plugin_trusted_keys` option |
| `WPGO_MARKET_DIR` | `examples/plugins/dist`, then `/usr/share/wpgo/plugins` | a directory of `.zip` packages offered on Plugins › Add Plugin |
| `WPGO_TRUST_DEV_KEY` | unset (`1` in compose) | trust the `dev-key.pub` found in the market directory. **Development only** |
| `WPGO_EXPOSE_USERS` | unset | `1` lets visitors list accounts (public REST users, `/?author=N`, users sitemap). Off by default because those routes disclose account names |
| `WPGO_TRUSTED_PROXIES` | loopback and private ranges | proxies (comma separated IPs or CIDRs) whose `X-Forwarded-For` / `X-Real-IP` is believed when deriving the visitor address; `none` trusts no proxy ([reverse proxy](reverse-proxy-and-tls.md)) |
| `WPGO_HUB_URL` | `https://hub.wordpressgo.dev` | the Hub the site talks to; empty disables it. Also the `wpgo_hub_url` option ([Hub](../hub/index.md)) |

## Compose only

| Variable | Default | Purpose |
|----------|---------|---------|
| `WPGO_PORT` | `8088` | host port for the site |
| `WPGO_DB_PORT` | `5432` | host port for PostgreSQL |

## Test suite

| Variable | Purpose |
|----------|---------|
| `WPGO_TEST_ADMIN_DB` | a connection used to drop and recreate the test database |
| `WPGO_TEST_DB` | the test database URL; both unset skips the integration tests |

## Options worth knowing

These are options in the database (Settings screens or `/wp-json/wp/v2/settings`), not
environment variables:

| Option | Where | Meaning |
|--------|-------|---------|
| `plugin_trusted_keys` | — | newline- or comma-separated public keys, merged with `WPGO_PLUGIN_KEYS` |
| `plugin_allow_unsigned` | — | `true` accepts unsigned plugin packages (development only) |
| `wpgo_cache_*`, `wpgo_opt_*` | WPGo Cache screens | page cache and page optimisation ([caching](caching.md)) |
| `wpgo_hub_ping` | — | `false` stops the daily ping to the Hub |
