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

To publish a plugin for everyone:

1. Email the package (the zip), a link to the source and a short description to
   **hello@pursae.com**.
2. Open a topic in the Pursae forum at [pursae.com/forum](https://pursae.com/forum/) so people can
   discuss and test it.

After review the package is signed with the Pursae key and listed on the Hub, so it appears under
Plugins › Add Plugin on every site. For a private Hub, deliver the zip however your organisation
agreed. Native plugins are different: they are reviewed for inclusion in the core and ship in a core
release ([native plugins](native-plugins.md)).

On publish the Hub:

1. parses and validates the zip and the manifest, and rejects a version that already exists or
   is lower than the latest published version of the slug;
2. computes the package digest;
3. keeps your `signature` file as provenance and re-packs the zip with the Hub's own signature;
4. stores the repacked zip, its SHA-256 and size.

The listing then appears in `GET /api/v1/plugins` with `download_url`, `sha256`, `signed_by`
and your permissions list, and in every site's Plugins › Add Plugin within 12 hours (the catalog
is cached that long). Sites verify the SHA-256 and the Hub signature on install.

## Keeping a plugin to yourself

You do not have to submit anything. Sign the package with your own key, trust that key on your
sites and upload it:

```bash
pursae plugin keygen -out mykey
pursae plugin build -dir . -key mykey.key -out my-plugin.zip
pursae plugin verify -pub mykey.pub my-plugin.zip
```

Trust `mykey.pub` under Plugins › Trusted Keys (`/wp-admin/plugin-keys`), with
`pursae plugin key add`, or in `PURSAE_PLUGIN_KEYS`, then upload the zip under Plugins › Add Plugin ›
Upload. Keep `mykey.key` safe and backed up: without it you cannot sign updates that your sites
trust ([upgrading](../getting-started/upgrading.md#your-own-plugins-and-themes)).

## Updates

Publish a new version the same way with a higher `version`. Old versions stay downloadable
(`versions` in `GET /api/v1/plugins/{slug}`); sites install the latest.

## Running your own Hub

Production sites always use `https://hub.pursae.com`. A Hub of your own serves development
builds (`-tags devhub`, `PURSAE_DEV_HUB_URL`) for testing packages before you publish them
([running your own Hub](../hub/running-your-own-hub.md)).
