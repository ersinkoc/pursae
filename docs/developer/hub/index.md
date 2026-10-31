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
([running your own Hub](running-your-own-hub.md)).

## What a site gets from it

| Feature | Endpoint | Where it shows |
|---------|----------|----------------|
| Plugin marketplace | `GET /api/v1/plugins` | Plugins › Add Plugin ([marketplace](marketplace.md)) |
| Theme marketplace | `GET /api/v1/themes` | Appearance › Themes › Add Theme |
| Package downloads | `…/{slug}/download[/{version}]` | one-click install; the site checks the SHA-256 and, for plugins, the Hub signature |
| Core releases | `GET /api/v1/index` → `core` | Dashboard › Updates; the Hub never installs anything |
| Signing keys | `GET /api/v1/index` → `keys` | added to the site's trusted plugin keys automatically |
| Product logo | `GET /api/v1/index` → `brand` | replaces the shipped logo on every screen (see below) |

## Product logo

The logo on the admin bar, login screen, favicons, dashboard and the Web Builder's loading screen is
served from `/wp-includes/images/brand/` (`logo-32.png` … `logo-512.png`, `logo.svg`). A release
changes it by replacing those files in `web/wp-includes/images/brand`. A Hub changes it on every
connected site without a release by adding a `brand` block to `GET /api/v1/index`:

```json
"brand": {"logo_url": "/media/brand/logo.svg", "logo_version": "2026-11-01"}
```

`logo_url` is absolute or relative to the Hub; it must be a PNG, WebP or SVG of at most 1 MB.
When `logo_version` changes the site downloads the image into `uploads/brand/` and serves it for
every size; when the block goes away the shipped logo comes back. Sites read the index when they
check for updates (at most every 12 hours, or at once from Dashboard › Updates › Check again).

## Privacy

Everything a site sends to the Hub is public information: once a day (and after an update) it
pings `POST /api/v1/sites/ping` with `{"url": "https://blog.example.com", "version": "0.1.0"}`.
The Hub keeps only `scheme://host[:port]` (path and query dropped, host lower-cased), the version
and first/last-seen times. **No IP addresses, admin e-mails, titles, plugin lists or anything else
are stored.** Local and private hosts are accepted but flagged as local in the operator's panel.

The ping exists so the operator can count sites and see which versions are in use (the public
count is `GET /api/v1/sites/count`; the list of sites is not public). Switch it off with the
`wpgo_hub_ping` option; set `WPGO_HUB_URL` to an empty value to disconnect from the Hub entirely —
the site then has no marketplace and no update notices, and nothing leaves it.

## Caching and limits

Catalog responses carry `Cache-Control: public, max-age=300`; sites cache the catalog for 12
hours and the index for the Updates screen. One ping per host per 10 minutes is accepted
(`429` otherwise); invalid or non-http(s) URLs are rejected with `400`.
