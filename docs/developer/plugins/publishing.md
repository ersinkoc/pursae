---
title: Publishing to the Hub
description: How to get a plugin into the marketplace that every Pursae site sees, and what the Hub does with it.
weight: 130
section: plugins
---

The [Hub](../hub/index.md) is the marketplace sites read from Plugins › Add Plugin. Publishing is
done by a Hub operator through its admin panel; as a developer you deliver a package and the
listing details.

## Prepare the package

1. Build and pack as in [packaging and signing](packaging-and-signing.md). Sign it with your own
   key: the Hub keeps your signature as provenance and re-signs the package with its key, so
   sites that trust the Hub install it without further configuration.
2. Fill in `plugin.json` completely. The Hub reads `slug`, `name`, `version`, `description`,
   `author`, `author_url`, `homepage`, `requires_core`, `tested_core`, `permissions` and
   `hooks` (the manifest's `author_uri`, `plugin_uri` and `requires` are the same data). The
   `description` is the card text, so keep it to one or two sentences.
3. Add `readme.md` (Markdown; it becomes the listing page) and `icon.svg` (square; a placeholder
   is generated otherwise).
4. Test on a clean site with only the permissions you ask for.

## Submit

Send the zip to the Hub operator (for the public Hub: through the repository's plugin submission
process; for a private Hub, however your organisation agreed). The operator uploads it under
Admin › Plugins and publishes it. On publish the Hub:

1. parses and validates the zip and the manifest, and rejects a version that already exists or
   is lower than the latest published version of the slug;
2. computes the package digest;
3. keeps your `signature` file as provenance and re-packs the zip with the Hub's own signature;
4. stores the repacked zip, its SHA-256 and size.

The listing then appears in `GET /api/v1/plugins` with `download_url`, `sha256`, `signed_by`
and your permissions list, and in every site's Plugins › Add Plugin within 12 hours (the catalog
is cached that long). Sites verify the SHA-256 and the Hub signature on install.

## Updates

Publish a new version the same way with a higher `version`. Old versions stay downloadable
(`versions` in `GET /api/v1/plugins/{slug}`); sites install the latest.

## Running your own Hub

Production sites always use `https://hub.pursae.com`. A Hub of your own serves development
builds (`-tags devhub`, `WPGO_DEV_HUB_URL`) for testing packages before you publish them
([running your own Hub](../hub/running-your-own-hub.md)).
