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
| `WPGO_EMPTY_TRASH_DAYS` | `30` | days trashed posts and comments are kept before the daily `wp_scheduled_delete` job removes them (WordPress' `EMPTY_TRASH_DAYS`). `0` empties the trash at every daily run; unlike WordPress it does not hide the Trash links in the admin |
| `WPGO_LOG_FORMAT` | text | `json` writes the access log as one JSON object per request (`status`, `method`, `uri`, `duration`, `ip`, `request_id`) for log shippers; other log lines are unchanged |
| `WPGO_BACKUPS_DIR` | `backups/` next to uploads | where Backup Manager keeps its archives (manual, scheduled and safety snapshots). Keep it outside the uploads directory, which is served publicly |
| `WPGO_LANG_DIR` | `languages/` next to uploads | installed language packs (Settings › General › Site Language, Dashboard › Updates › Translations) |
| `WPGO_TRANSLATIONS_API` | `https://api.wordpress.org/translations` | the translation API language packs are downloaded from (a mirror, or a fake in tests) |
| `WPGO_BIN_DIR` | — (`/data/bin` in the image) | persistent directory `wpgo launch` installs the binary into, so a core update made from the admin survives a container restart ([upgrading](../getting-started/upgrading.md)) |
| `WPGO_LISTEN_FD` | — | **internal**: set by the running binary when it hands its listening socket to an updated binary; do not set it yourself |
## Database pool

`DATABASE_URL` takes pgx's pool parameters as query parameters; each one you leave out gets the
default below (pgx alone would allow only `max(4, CPUs)` connections):

| Parameter | Default | Purpose |
|-----------|---------|---------|
| `pool_max_conns` | 4 × CPUs, at least 10, at most 40 | connections per instance. Keep the sum over all instances (plus backups and `psql`) under PostgreSQL's `max_connections` (100 by default) |
| `pool_min_conns` | `2` | connections kept open when idle |
| `pool_max_conn_lifetime` | `1h` (+ up to 5 min jitter) | a connection is replaced after this long |
| `pool_max_conn_idle_time` | `15m` | idle connections above the minimum are closed after this long |
| `pool_health_check_period` | `30s` | how often idle connections are checked |

```
DATABASE_URL=postgres://wpgo:secret@db:5432/wpgo?sslmode=disable&pool_max_conns=20
```

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
| `WPGO_THEME_LIBRARY_DIR` | — (the library comes from the Hub) | serve Add Themes › Library from this directory of theme package directories instead of the Hub (development, offline sites; the repository's is `library/themes`). Hub downloads are cached in `theme-library-cache/` next to the uploads |
| `WPGO_DISALLOW_FILE_EDIT` | off | set to `1`/`true` to make the Theme File Editor read-only (like `DISALLOW_FILE_EDIT`) |
| `WPGO_PLUGIN_KEYS` | — | comma-separated base64 Ed25519 public keys trusted to sign plugin packages, in addition to the `plugin_trusted_keys` option |
| `WPGO_MARKET_DIR` | `examples/plugins/dist`, then `/usr/share/wpgo/plugins` | a directory of `.zip` packages offered on Plugins › Add Plugin |
| `WPGO_TRUST_DEV_KEY` | unset (`1` in compose) | trust the `dev-key.pub` found in the market directory. **Development only** |
| `WPGO_EXPOSE_USERS` | unset | `1` lets visitors list accounts (public REST users, `/?author=N`, users sitemap). Off by default because those routes disclose account names |
| `WPGO_TRUSTED_PROXIES` | loopback and private ranges | proxies (comma separated IPs or CIDRs) whose `X-Forwarded-For` / `X-Real-IP` is believed when deriving the visitor address; `none` trusts no proxy ([reverse proxy](reverse-proxy-and-tls.md)) |
| `WPGO_HUB_URL` | `https://hub.wordpressgo.dev` | the Hub the site talks to; empty disables it. Also the `wpgo_hub_url` option ([Hub](../hub/index.md)) |

## Built-in plugins

| Variable | Default | Purpose |
|----------|---------|---------|
| `WPGO_ADMINSEC_DISABLE_HIDE_LOGIN` | unset | recovery switch for Pursae Security's custom login URL: `1`/`true`/`yes` brings `/wp-login` back while it is set (restart the server after setting it). `wpgo adminsec disable-hide-login` turns the setting off for good |
| `WPGO_CDN_SETTINGS` | unset | Pursae CDN settings as one JSON object (the keys of the `gocdn_settings` option, in the style of WP Offload Media's `AS3CF_SETTINGS`, e.g. `{"provider":"aws","bucket":"media","access-key-id":"…","secret-access-key":"…"}`). Keys given here override the saved settings and are shown read-only on the CDN screen, so credentials can stay out of the database. Invalid JSON is ignored with a warning in the log |

The `WPGO_BUILDER_AI*` variables in `internal/webbuilder/contract.go` belong to the Web Builder AI module,
which is not shipped (its upstream modules are stubbed); setting them has no effect.

## Compose only

| Variable | Default | Purpose |
|----------|---------|---------|
| `WPGO_PORT` | `8088` | host port for the site |
| `WPGO_BIND` | `127.0.0.1` | host address the site is published on, with `compose.prod.yaml` only (the development file publishes on every interface) |
| `WPGO_DB_PORT` | `5432` | host port for PostgreSQL |

## Test suite

| Variable | Purpose |
|----------|---------|
| `WPGO_TEST_ADMIN_DB` | a connection used to drop and recreate the test database |
| `WPGO_TEST_DB` | the test database URL; both unset skips the integration tests |
| `WPGO_I18N_MISSES` | development: append every UI string that has no translation to this file (JSON lines); `WPGO_I18N_MISSES_*` are read by the i18n coverage tests only |

## Options worth knowing

These are options in the database (Settings screens or `/wp-json/wp/v2/settings`), not
environment variables:

| Option | Where | Meaning |
|--------|-------|---------|
| `plugin_trusted_keys` | — | newline- or comma-separated public keys, merged with `WPGO_PLUGIN_KEYS` |
| `plugin_allow_unsigned` | — | `true` accepts unsigned plugin packages (development only) |
| `wpgo_cache_*`, `wpgo_opt_*` | Pursae Cache screens | page cache and page optimisation ([caching](caching.md)) |
| `image_output_format` | Settings › Media | the format of the sizes generated for each uploaded image: `""` (default) JPEG sizes for WebP uploads (PNG with transparency); `webp` WebP sizes for WebP uploads, as WordPress does; `webp-all` WebP for JPEG, PNG and WebP uploads: the image itself is saved as WebP (`photo.jpg` → `photo.webp`, or `photo-scaled.webp` above 2560 px) and the upload is kept as the original image. WebP is written at quality 86 by libwebp transpiled to Go (no cgo). Plugins can change the mapping with the `image_editor_output_format` filter ([hooks](../plugins/hooks.md#media)). Existing images are not regenerated |
| `pursae_license_key` | Settings › License | the license key sent to the Hub ([licenses](../hub/index.md#licenses)) |
