<p align="center">
  <img src="web/wp-includes/images/brand/logo-256.png" alt="Pursae" width="96">
</p>

<h1 align="center">Pursae</h1>

<p align="center">
  WordPress core rewritten in Go — one binary, PostgreSQL, the familiar WordPress admin,<br>
  the block editor, a page builder, sandboxed WebAssembly plugins and classic themes.
</p>

<p align="center">
  <img alt="Go 1.27" src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white">
  <img alt="PostgreSQL 17" src="https://img.shields.io/badge/PostgreSQL-17-336791?logo=postgresql&logoColor=white">
  <img alt="License GPL" src="https://img.shields.io/badge/license-GPL--2.0--or--later-blue">
</p>

---

## Contents

- [Quick start (Docker)](#quick-start-docker)
- [First run: the installer](#first-run-the-installer)
- [Run without Docker](#run-without-docker)
- [Configuration](#configuration)
- [Data and volumes](#data-and-volumes)
- [Everyday commands](#everyday-commands)
- [Editing theme files](#editing-theme-files)
- [Plugins](#plugins)
- [Pursae Web Builder (page builder)](#pursae-web-builder-page-builder)
- [Production checklist](#production-checklist)
- [Troubleshooting](#troubleshooting)
- [What works in 0.1](#what-works-in-01)
- [Project layout](#project-layout)
- [License](#license)

## Quick start (Docker)

The fastest way to try Pursae. You only need **Docker** (Docker Desktop on macOS/Windows,
Docker Engine + Compose v2 on Linux) and **git**.

```sh
git clone https://github.com/pursae/pursae-dev.git
cd pursae-dev
make docker-up
```

No `make`? Run the same thing directly:

```sh
docker compose -f docker/compose.yaml up -d --build
```

The first build downloads Go modules and compiles the binary, so give it a minute or two.
When it finishes, open **http://localhost:8088** — you will land on the installer.

| Service | What it is | Host port |
|---------|------------|-----------|
| `wpgo`  | The Pursae server | `8088` |
| `db`    | PostgreSQL 17 | `5432` |

### Use another port or address

The site URL is stored at install time, so set it **before** the first run (or reset, see
[Troubleshooting](#troubleshooting)):

```sh
WPGO_PORT=9000 WPGO_SITE_URL=http://localhost:9000 make docker-up
```

To reach the site from another device on your network, use your machine's LAN address:

```sh
WPGO_SITE_URL=http://192.168.1.50:8088 make docker-up
```

## First run: the installer

Open the site and Pursae redirects to `/wp-admin/install`. Fill in the same fields as
WordPress' five-minute install:

1. **Site title**
2. **Username** and **password** for the first administrator
3. **Email**
4. Whether to ask search engines not to index the site

The installer creates "Hello world!", its first comment, the Sample Page, a draft Privacy
Policy page and the default sidebar widgets. Then sign in at `/wp-login` and you are in the
admin (`/wp-admin/`).

> Admin URLs have no `.php` suffix (`/wp-admin/edit`, `/wp-login`). The WordPress spellings
> (`/wp-admin/edit.php`, `/wp-login.php`) still work: GET requests redirect permanently, POSTs
> are served in place.

## Run without Docker

You need **Go 1.27** and a **PostgreSQL (17 is what the compose file uses)** database you can create tables in.

```sh
# 1. Start only the database from the compose file (or point DATABASE_URL at your own)
make db

# 2. Run the app; templates, CSS and JS are read from ./web so edits show up on refresh
export DATABASE_URL='postgres://wpgo:wpgo@localhost:5432/wpgo?sslmode=disable'
export WPGO_SITE_URL='http://localhost:8080'
WPGO_WEB_DIR=web go run ./cmd/wpgo serve
```

`make dev` does steps 1 and 2 for you. To build a single binary instead:

```sh
make build              # writes bin/wpgo
./bin/wpgo serve
```

Migrations run automatically on start (under a database advisory lock, so several instances
can start safely). You can also run them alone with `wpgo migrate`.

## Configuration

Everything is configured with environment variables.

**Core**

| Variable | Default | Purpose |
|----------|---------|---------|
| `DATABASE_URL` | `postgres://wpgo:wpgo@localhost:5432/wpgo?sslmode=disable` | PostgreSQL connection string |
| `WPGO_ADDR` | `:8080` | Address the server listens on |
| `WPGO_SITE_URL` | `http://localhost:8080` | Public URL; written to the settings at install |
| `WPGO_UPLOADS_DIR` | `./data/uploads` | Where media uploads are stored |
| `WPGO_SECRET` | generated | Signing secret; generated and stored on first start if unset |
| `WPGO_WEB_DIR` | embedded | Read templates/CSS/JS from disk instead of the embedded copy (development) |
| `WPGO_BACKUPS_DIR` | `backups/` next to uploads | Backup Manager archives (keep it outside the public uploads) |
| `WPGO_LANG_DIR` | `languages/` next to uploads | Installed language packs |
| `WPGO_TRANSLATIONS_API` | `https://api.wordpress.org/translations` | Where language packs are downloaded from |
| `WPGO_BIN_DIR` | `/data/bin` in the image | Where `wpgo launch` keeps the (self-updated) binary |

**Compose-only**

| Variable | Default | Purpose |
|----------|---------|---------|
| `WPGO_PORT` | `8088` | Host port for the site |
| `WPGO_DB_PORT` | `5432` | Host port for PostgreSQL |

**Mail** — without an SMTP host, messages are written as `.eml` files so you can inspect them.

| Variable | Default | Purpose |
|----------|---------|---------|
| `WPGO_SMTP_HOST` | — | SMTP server |
| `WPGO_SMTP_PORT` | `587` | `465` = implicit TLS |
| `WPGO_SMTP_USER` / `WPGO_SMTP_PASS` | — | Credentials |
| `WPGO_MAIL_FROM` | — | Sender address |
| `WPGO_MAIL_DIR` | `mail/` next to uploads | Where `.eml` files go when SMTP is off |

**Themes and plugins**

| Variable | Default | Purpose |
|----------|---------|---------|
| `WPGO_THEMES_DIR` | `<data>/themes` | Where edited theme files are saved |
| `WPGO_DISALLOW_FILE_EDIT` | off | Set to make the Theme File Editor read-only |
| `WPGO_PLUGIN_KEYS` | — | Comma-separated Ed25519 public keys trusted to sign plugin packages |
| `WPGO_MARKET_DIR` | — | Local plugin packages offered on Plugins › Add Plugin (besides the Hub catalog) |
| `WPGO_HUB_URL` | `https://hub.wordpressgo.dev` | Pursae Manager: marketplace, licenses, release checks and self-update, and the site statistics (daily ping with version and inventory, usage events; always on). `off` disables it |
| `WPGO_TRUST_HUB_KEYS` | `1` in compose | Builds without a pinned release root key (`ROOT_KEYS` build arg / `-ldflags -X wpgo/internal/server.rootKeysLinked=…`) refuse core self-updates; `1` trusts the keys Pursae Manager lists instead (**development only**) |
| `WPGO_TRUST_DEV_KEY` | `1` in compose | Trust the demo signing key of the bundled example plugins (**development only**) |
| `WPGO_EXPOSE_USERS` | unset | `1` lets visitors list accounts (REST users, `/?author=N`, users sitemap) |
| `WPGO_TRUSTED_PROXIES` | loopback and private ranges | Proxies whose `X-Forwarded-For` is believed; `none` trusts none |
| `WPGO_THEME_LIBRARY_DIR` | — (from the Hub) | Serve the theme library from a local directory (development, offline) |
| `WPGO_ADMINSEC_DISABLE_HIDE_LOGIN` | unset | `1` brings `/wp-login` back when Pursae Security's custom login URL locked you out |
| `WPGO_CDN_SETTINGS` | unset | Pursae CDN settings as a JSON object; its keys override the saved settings and are read-only in the admin |

## Data and volumes

Compose keeps state in named volumes, so `docker compose down` does not lose anything:

| Volume | Mounted at | Contains |
|--------|------------|----------|
| `pgdata` | `/var/lib/postgresql/data` | The database |
| `uploads` | `/data/uploads` | Media library files |
| `plugins` | `/data/plugins` | Installed plugin packages |
| `themes` | `/data/themes` | Theme files you edited in the admin |

Back up the database:

```sh
docker compose -f docker/compose.yaml exec db pg_dump -U wpgo -Fc wpgo > wpgo.dump
```

Restore it:

```sh
docker compose -f docker/compose.yaml exec -T db pg_restore -U wpgo -d wpgo --clean --if-exists < wpgo.dump
```

## Everyday commands

```sh
make docker-up          # build and start everything in the background
make docker-down        # stop (data is kept)
make test               # unit tests + PostgreSQL integration tests
make docker-test        # the same suite inside a Go container
make build              # compile bin/wpgo
make builder            # rebuild the Web Builder editor into web/web-builder (needs Node 22+)
```

Logs and restart:

```sh
docker compose -f docker/compose.yaml logs -f wpgo
docker compose -f docker/compose.yaml restart wpgo
```

Update to a newer version:

```sh
git pull
make docker-up          # rebuilds the image; migrations run on start
```

## Editing theme files

Appearance › **Theme File Editor** works without recompiling. Saving a file writes an override
into the themes volume (`/data/themes`, or `WPGO_THEMES_DIR`); the template set is re-parsed
immediately and the page is test-rendered. If the change has a template or render error it is
**rolled back automatically** and the editor shows the error. **Restore original** removes the
override. Set `WPGO_DISALLOW_FILE_EDIT=1` to turn editing off.

Themes can also be installed as code-free **theme packages** (`theme.json`, Go templates, assets)
through Appearance › Themes › Add Theme; the format is in [docs/theme-package.md](docs/theme-package.md)
and the theme developer guide at [/docs/themes/](https://wordpressgo.dev/docs/themes/).

## Plugins

Plugins are signed WebAssembly packages that run in a sandbox (wazero) with permissions you
grant at activation. A crash or runaway loop in a plugin is contained and does not take the
site down. Plugins can filter content, register shortcodes, add REST routes under their own
namespace, read and write posts, declare a settings page under Settings, schedule events, cache
data in transients and ship CSS/JS assets. The Go SDK lives in `sdk/go/wpgo`; examples are in
`examples/plugins` (`hello-gopher`, `word-counter` and `shortcode-demo`, which uses every SDK
feature); the ABI is documented in [internal/plugins/README.md](internal/plugins/README.md) and
the plugin developer guide at [/docs/plugins/](https://wordpressgo.dev/docs/plugins/).

```sh
tools/build-plugins.sh      # builds the example plugins into examples/plugins/dist
```

Rebuild the image afterwards and they appear under Plugins › Add New.

## Pursae Web Builder (page builder)

Pursae Web Builder is a built-in plugin, like Classic Editor and Pursae Cache, and it is **off on a fresh
install**. Switch it on under Plugins › Pursae Web Builder › **Activate** (or Plugins › Add Plugin ›
Native Plugins). Until then it leaves no trace: no menu entries, "Edit with Pursae Web Builder" links,
builder routes, assets or front-end output.

It is a section-based visual editor (a React app built from `github.com/pursae/Builder`, embedded in
the binary). Open a page with **Edit with Pursae Web Builder** (block or classic editor, the Pages
list, the toolbar on the site); edits autosave as your draft, and **Publish** stores the page document
plus a sanitized HTML/CSS snapshot that visitors get without any React. Settings › Web Builder picks the
post types (pages and posts by default).

Deactivating it again hides everything. Pages built with Pursae Web Builder then show, inside the
theme, the readable HTML version saved with them; the builder data stays in the database and comes
back on the next activation. Details: [docs/developer/web-builder.md](docs/developer/web-builder.md).

**Upgrading from a release with the previous builder:** that builder and all its data (templates,
theme parts, kits, popups, form submissions and the page data it stored) are deleted on the first start.
Back up the database first; see [Upgrading](docs/developer/getting-started/upgrading.md).

## Production checklist

- Put Pursae behind a TLS-terminating reverse proxy and set `WPGO_SITE_URL` to the
  `https://` address.
- Start with the production override, which sets the database password from `WPGO_DB_PASSWORD`,
  does not publish PostgreSQL and does not trust the public demo plugin key (`WPGO_TRUST_DEV_KEY=0`):

  ```sh
  WPGO_DB_PASSWORD=... WPGO_SITE_URL=https://example.com \
    docker compose -f docker/compose.yaml -f docker/compose.prod.yaml up -d --build
  ```

  For real plugins list your own signing keys in `WPGO_PLUGIN_KEYS`.
- Configure SMTP (`WPGO_SMTP_*`) so password resets and notifications are delivered.
- Back up the `pgdata` and `uploads` volumes.
- Pick a strong admin password; Pursae never calls Gravatar or other third-party
  services on its own.

## Troubleshooting

**The site redirects to `/wp-admin/install` again after a restart.** The database is empty or
a different one is being used. Check that the `pgdata` volume still exists
(`docker volume ls`) and that `DATABASE_URL` points to the database you installed into.

**Docker restarted and the site is down.** Containers do not always come back after the Docker
daemon restarts. Start them again — data is kept:

```sh
docker compose -f docker/compose.yaml up -d
```

**Links or assets point at the wrong address (old IP or port).** The site URL was stored at
install time. Either change it in Settings › General, or start over with the right
`WPGO_SITE_URL` (see the next item).

**Start from scratch.** This **deletes** the database, uploads, plugins and edited themes:

```sh
docker compose -f docker/compose.yaml down -v
make docker-up
```

**Port 8088 or 5432 is already in use.** Pick other host ports:

```sh
WPGO_PORT=9000 WPGO_DB_PORT=5433 WPGO_SITE_URL=http://localhost:9000 make docker-up
```

**The first build is slow or fails to download modules.** The Go image downloads modules on the
first build; make sure Docker has network access. Later builds are cached.

**Port already taken by a local WordPress.** Stop it, or use `WPGO_PORT` as above.

## What works in 0.1

- **Core** — typed PostgreSQL schema with JSONB, migrations on start, install wizard, argon2id
  passwords, sessions, login throttling, WordPress nonces, security headers, roles and
  capabilities with multi-role union and per-user allow/deny (deny wins).
- **Admin** (WordPress 7.1 markup and CSS, English) — Dashboard, Posts, Pages, Media Library
  (grid with the real `wp.media` modal, list, uploads with image sizes), Comments, Appearance
  (Themes with Live Preview, Customize, Widgets, Menus, Theme File Editor), Plugins, Users,
  Tools (Import/Export WXR, Site Health, personal data export/erase), Settings (General,
  Writing, Reading, Discussion, Media, Permalinks, Privacy), Updates, About.
- **Pursae Editor** — WordPress' block editor (the 7.1 `@wordpress/*` bundles) on wpgo's REST API:
  preloading, autosaves, revisions, media, patterns, oEmbed proxy.
- **Classic Editor** (built-in plugin) — TinyMCE + Quicktags, Add Media, featured image;
  default editor and per-user switching under Settings › Writing.
- **Pursae Web Builder** (built-in plugin) — a section-based visual page builder: ready-made
  sections and designs, desktop/tablet/mobile views, brand colours, self-hosted Google Fonts,
  menus, FAQ, tabs, forms and motion; autosave, revisions and saved sections. Pages publish as a
  static HTML/CSS snapshot with a small optional script.
- **REST API `wp/v2`** — posts, pages, media, blocks, terms, users, comments, settings, types,
  taxonomies, statuses, themes, search, patterns, sidebars/widgets; `_fields`, `_embed`,
  pagination headers, cookie + nonce and Application Password authentication.
- **Themes** — **Twenty Twenty-Five**, the default theme: WordPress' block theme ported to Go
  templates with the same markup and its theme.json design (Manrope and Fira Code self-hosted,
  the style variations' palettes as a Customizer color scheme, editor styles for Pursae Editor), one
  small stylesheet plus the core styles of the blocks a page uses, and four demo content sets
  (Appearance › Themes › Import Demo Content). The
  theme library (from Pursae Manager, not part of the binary) offers more themes to install.
- **Pursae Cache** (built-in plugin) — in-process full-page cache with tag-based purges, crawler,
  gzip + ETag, CDN purge headers, lazy images, deferred scripts, font preloads.
- **Plugins** — signed WebAssembly packages, granted permissions, hooks `init`, `the_content`,
  `admin_notices`, `wpgo_post_stats` and scheduled `cron_*` events; shortcodes, REST routes,
  posts and current-user APIs, settings pages, transients and package assets.
- **System** — job runner on the `jobs` table (multi-instance safe) with WordPress' scheduled
  events, e-mail, WXR import/export.

- **Built-in plugins** (listed on Plugins, off on a fresh install; one page each in the
  [developer docs](docs/developer/built-in-plugins/index.md)) — Pursae SEO, Pursae Custom Fields, Pursae Headless, Pursae CDN,
  Pursae Contact Form, Pursae Multi Language, Pursae Security, Pursae Cookie, Pursae Maintenance, Pursae SMTP and Pursae Commerce.
- **Pursae Commerce** — a native WooCommerce clone (products, cart, checkout, tax, shipping, coupons,
  orders, gateways, e-mails, REST) with the premium extensions as built-in modules (off by default)
  and store themes in the theme library.

Open gaps are tracked in the project notes.

## Project layout

```
builder/            Pursae Web Builder editor sources: upstream submodule, patches, host adapter (Node)
cmd/wpgo            entry point (serve, migrate, version)
docker/             Dockerfile and compose.yaml
internal/blocks     block parser, renderer, serializer
internal/store      data access (pgx)
internal/server     routes, admin screens, REST API, editors, themes, cron, mail glue
internal/cache      Pursae Cache page-cache engine
internal/plugins    Wasm plugin runtime, hooks, permissions, signed packages
internal/webbuilder Pursae Web Builder document schema, sanitizer, CSS, fallback and forms
internal/mail       wp_mail(): SMTP or .eml files
internal/links      permalinks and URL builders
internal/caps       capabilities and meta-capability mapping
internal/wp         WordPress formatting helpers (dates, slugs, autop, kses)
sdk/go/wpgo         plugin SDK; examples in examples/plugins
web/admin           admin templates and the vendored wp-admin CSS/JS
web/web-builder     the built editor and site runtime (output of `make builder`, committed)
web/themes          bundled themes: assets (GPL) and their Go templates
web/wp-includes     vendored core CSS/JS (block library, dashicons, admin bar, jQuery)
```

## License

GPL-2.0-or-later. The bundled themes and the vendored WordPress CSS/JS are GPL-licensed by the
WordPress contributors. Pursae Web Builder's editor is built from `github.com/pursae/Builder`
(`builder/upstream`); that repository needs a GPL-compatible LICENSE before a release ships it.
"WordPress" is a trademark of its owner;
Pursae is not affiliated with it. Third-party attributions are in [NOTICE](NOTICE).
