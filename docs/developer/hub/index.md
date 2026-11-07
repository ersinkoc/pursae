---
title: The Hub
description: The central service behind the marketplace and update checks, and what a site shares with it.
weight: 10
section: hub
---

The **Hub** is a small service (`wpgo-hub`) every Pursae site talks to. It has two faces:

- a **public JSON API** under `/api/v1/` that sites and the public site read — no authentication,
  cacheable, rate limited;
- an **admin panel** under `/admin/` where the operator manages the plugin and theme marketplace,
  core releases and signing keys.

The public Hub is `https://hub.wordpressgo.dev`. Organisations can run their own
([running your own Hub](running-your-own-hub.md)). Sites only talk to a Hub over **https**; plain
http is accepted for `localhost`, loopback addresses and `*.localhost` / `*.test` development hosts.
Redirects never downgrade to http and drop the license and site headers when they leave the Hub's
host.

## What a site gets from it

| Feature | Endpoint | Where it shows |
|---------|----------|----------------|
| Plugin marketplace | `GET /api/v1/plugins` | Plugins › Add Plugin ([marketplace](marketplace.md)) |
| Theme marketplace | `GET /api/v1/themes` | Appearance › Themes › Add Theme |
| Package downloads | `…/{slug}/download[/{version}]` | one-click install; the site checks the SHA-256 and, for plugins, the Hub signature |
| Core releases | `GET /api/v1/index` → `core` | Dashboard › Updates; with a signed build for the site's platform, **Update to version X** installs it ([upgrading](../getting-started/upgrading.md)) |
| Plugin updates | `GET /api/v1/plugins` | Plugins and Dashboard › Updates, for plugins installed from the Hub ([marketplace](marketplace.md#updates)) |
| License check | `GET /api/v1/license` | Settings › License (see below) |
| Signing keys | `GET /api/v1/index` → `keys` | added to the site's trusted plugin keys automatically (replaced on every refresh, so a key the Hub drops is no longer trusted); verify core builds only when a release root key endorses them ([root of trust](#root-of-trust)) |
| Product logo | `GET /api/v1/index` → `brand` | replaces the shipped logo on every screen (see below) |

## Product logo

The logo on the admin bar, login screen, favicons and dashboard is
served from `/wp-includes/images/brand/` (`logo-32.png` … `logo-512.png`, `logo.svg`). A release
changes it by replacing those files in `web/wp-includes/images/brand`. A Hub changes it on every
connected site without a release by adding a `brand` block to `GET /api/v1/index`:

```json
"brand": {"logo_url": "/media/brand/logo.svg", "logo_version": "2026-11-01"}
```

`logo_url` is absolute or relative to the Hub; it must be a PNG, WebP or SVG of at most 1 MB. A
logo hosted elsewhere than the Hub must be https on a public address (private and loopback
addresses are refused at connect time). The logo is served, `/favicon.ico` included, with a
sandbox Content-Security-Policy and `nosniff`, so an SVG can never run as a page of the site.
When `logo_version` changes the site downloads the image into `uploads/brand/` and serves it for
every size; when the block goes away the shipped logo comes back. Sites read the index when they
check for updates (at most every 12 hours, or at once from Dashboard › Updates › Check again).

## Root of trust

A key listed in the same answer as the release it verifies proves nothing, so a core self-update
needs a **release root key** compiled into the binary:

- `rootKeysBuiltin` in `internal/server/hub_trust.go` (empty until Pursae publishes a production
  root key), plus
- `go build -ldflags "-X wpgo/internal/server.rootKeysLinked=<base64>[,<base64>…]"` (the Docker
  image: `--build-arg ROOT_KEYS=…`).

A Hub key verifies a release only if a root key endorsed it. The index then carries the
endorsement next to the key:

```json
"keys": [{"id": "hub-2026", "public": "<base64 Ed25519>", "endorsement": "<base64 Ed25519 signature>"}]
```

where `endorsement = base64(Ed25519.Sign(rootKey, "pursae-hub-key-v1\n" + id + "\n" + public))`
(`public` exactly as listed, no trailing newline). The root's private key stays offline; the
Hub operator asks for an endorsement whenever they generate a new key.

A build **without** any root key (development, `go run`, a fork) cannot verify a release:
Dashboard › Updates says the site cannot update itself, and the update is refused. Setting
`WPGO_TRUST_HUB_KEYS=1` (the development compose file does) restores the old behaviour of
trusting the listed keys — for development only. With a root key pinned, plugin packages are also
verified only against endorsed Hub keys; without one, the listed keys are trusted for plugins as
before (`wpgo_hub_trust_keys`).

## Licenses

Settings › License takes the license key bought from Pursae (option `pursae_license_key`, never
readable by plugins). The site checks it with `GET /api/v1/license` and shows the answer: valid or
not (with the Hub's reason: unknown, revoked, expired, not valid for this domain, activation limit),
the plan, whether it includes Pro packages and the beta channel, the expiry date and the sites it is
active on. The answer is cached for 12 hours; saving the key and **Check now** ask again. Code that
needs it calls `licensePro()` / `licenseBeta()` (cached answer only, never the network).

Every request to the Hub carries `X-Pursae-Site` (the site's home URL, `scheme://host[:port]`) and,
when a key is entered, `X-Pursae-License`. The Hub then also lists the Pro and private packages and
the beta versions and core releases the license unlocks (cards show **Pro**, **Private** and
**Beta** badges). Because the answers depend on the key, the cached catalog and index are kept per
key and dropped when it changes.

## What a site sends (statistics)

Statistics are always on; there is no opt-out. Only a site with `WPGO_HUB_URL=off` (or empty) sends
nothing — it then has no marketplace, no license and no update notices either.

**Every request** carries the site's home URL and, if one is entered, its license key (above).

**The daily ping** (`POST /api/v1/sites/ping`, also run by Dashboard › Updates' cron event
`wpgo_hub_check`):

```json
{"url": "https://shop.example.com", "version": "0.2.0", "locale": "tr_TR", "os": "linux", "arch": "amd64",
 "license": "PRS-…", "theme": "twentytwentyfive",
 "plugins": [{"slug": "goocommerce", "version": "1.0.0", "active": true, "source": "native"},
             {"slug": "word-counter", "version": "1.0.0", "active": false, "source": "hub"}],
 "themes":  [{"slug": "twentytwentyfive", "version": "1.5", "active": true, "source": "native"}]}
```

`plugins` lists every plugin, built-in (`native`), installed from the Hub (`hub`) or uploaded
(`upload`); `themes` lists the installed themes (bundled themes are `native`, library themes
installed from the Hub `hub`, uploaded packages `upload`).

**Events** (`POST /api/v1/sites/events`) record what happens on the site: `plugin_install`,
`plugin_update`, `plugin_activate`, `plugin_deactivate`, `plugin_delete` (built-in and package
plugins), `theme_install`, `theme_update`, `theme_activate`, `theme_delete`, `theme_preview` (Add
Themes › Live Preview), `demo_import` (a theme's demo content, with kind `theme`) and `core_update`. Each event is
`{"type", "kind", "slug", "version", "at"}`. They are queued in memory and sent in batches of at
most 100: every 30 seconds, as soon as 50 are waiting, and with the daily ping. While the Hub cannot
be reached up to 500 are kept (the oldest are dropped first); nothing is written to disk. Theme
preview sandboxes never send anything.

The site never sends user data, e-mail addresses, post content, titles or visitor information. The
public count of sites is `GET /api/v1/sites/count`; the list of sites is not public.

## Caching and limits

Catalog responses carry `Cache-Control: public, max-age=300`; sites cache the catalog for 12
hours and the index for the Updates screen. One ping per host per 10 minutes is accepted
(`429` otherwise); invalid or non-http(s) URLs are rejected with `400`.
