---
title: Marketplace
description: Installing plugins and themes from the Hub in the admin, and what happens on install.
weight: 20
section: hub
---

## Plugins › Add Plugin

The screen lists the Hub catalog (`GET /api/v1/plugins`, sorted by last update, searchable by
name, slug, description and author) next to the built-in plugins and any packages in
`WPGO_MARKET_DIR`. A card shows the name, author, version, description, icon, the **permissions
the plugin asks for**, download count and minimum core version; the detail view shows the
package's `readme.md`.

**Install Now** downloads the package from `download_url`, verifies that its SHA-256 matches
`sha256` from the listing and that its signature verifies against a trusted key (the Hub's keys
are trusted automatically), then stores it as an inactive plugin. **Activate** opens the
permission screen where you grant what the plugin may do ([permissions](../plugins/permissions.md)).

The **Upload Plugin** tab installs a zip you have on disk; it goes through the same signature
check, so your own key must be in `WPGO_PLUGIN_KEYS` or `plugin_trusted_keys`, or
`plugin_allow_unsigned` must be on for development.

## Appearance › Themes › Add Theme

Lists themes from `GET /api/v1/themes` with screenshot, author, description and tags, with
search and tag filters, plus **Upload Theme**. Theme packages contain no code, so they are not
signed; the installer checks the SHA-256 from the listing, validates `theme.json` and the
templates, and unpacks the package into `<data>/themes/<slug>/`. **Preview** renders the site with
the theme through the Customizer before you **Activate** it.

## Updates

A listing's `version` is compared with the installed package. A newer version shows **Update**
on the Plugins and Themes screens; updating downloads and replaces the package the same way an
install does. Plugins keep their granted permissions; review them on the activation screen if a
new version asks for more.

## Offline sites

Without a reachable Hub the screens show only built-in plugins and local packages, and uploads
still work. Nothing else in the admin depends on the Hub.
