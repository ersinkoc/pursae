---
title: Publishing
description: How plugins and themes get onto the Hub, and what the Hub verifies and changes.
weight: 30
section: hub
---

Publishing goes through the Hub's admin panel (Admin › Plugins, Admin › Themes): the operator
uploads a zip, reviews the listing and **publishes** it. Developers deliver the package and the
listing details; for the public Hub, through the project's submission process.

## Plugins

The package must follow the plugin package format: `plugin.json`, `plugin.wasm`, optional
`readme.md`, `icon.svg`, `assets/`, `signature` ([packaging and signing](../plugins/packaging-and-signing.md)).
On publish the Hub:

1. parses and validates the zip and the manifest; a version that already exists for the slug,
   or is lower than the latest published one, is rejected;
2. computes `digest = SHA-256("wpgo-plugin-v1\n" || u64be(len(json)) || json || u64be(len(wasm)) || wasm)`;
3. keeps the developer's `signature` (if any) as provenance and re-packs the zip with the Hub's
   own signature over the same digest;
4. stores the repacked zip, its SHA-256 and size.

Listing fields come from `plugin.json` (`slug`, `name`, `version`, `description`, `author`,
`author_url`/`author_uri`, `homepage`/`plugin_uri`, `requires_core`/`requires`, `tested_core`,
`permissions`, `hooks`) and can be overridden by the operator (description, homepage).
`readme.md` becomes the detail page, `icon.svg` the icon (a placeholder is generated otherwise).

## Themes

A theme package is a zip with `theme.json`, `templates/` and `assets/`
([your first theme](../themes/your-first-theme.md)). The Hub validates it, records `tags` and
the screenshot, and serves it with its SHA-256 and the version's `theme.json` as `manifest`
(sites build their theme library from it without downloading packages). Themes are not signed.
The core repository's theme library (`library/themes`) is published in bulk with
`make publish-themes PURSAE_MANAGER_DIR=/path/to/pursae-manager [PREINSTALL=slug,slug|all]`, which
runs `pursae-manager import -kind theme -publish …`; listings marked `preinstall` are installed on
every new site.

## After publishing

The item appears in `GET /api/v1/plugins` or `/api/v1/themes` immediately, and on sites within
their 12-hour catalog cache. Each download increments `downloads`. Older versions remain
available under `versions` in the item detail; **unpublish** hides an item without deleting its
files.

## Keys

The Hub's active signing key lives in `WPGOHUB_DATA_DIR/keys/<id>.key` (the format `wpgo-pkg
keygen` writes). The panel can generate a new key; old public keys stay listed in
`GET /api/v1/index` so already-installed packages keep verifying on sites.
