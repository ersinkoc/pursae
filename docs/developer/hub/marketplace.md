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

Listings that need a license carry a **Pro** (a Pro license) or **Private** (granted to your
license) badge, and a beta version a **Beta** badge; they only appear once the key is entered under
Settings › License ([licenses](index.md#licenses)).

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

Plugins installed from the Hub remember it (`source` = `hub`). When the catalog (cached 12 hours;
Dashboard › Updates › Check again refreshes it) lists a higher `version` than the installed one:

- the Plugins screen shows WordPress' row under the plugin: *There is a new version of X available.
  View version Y details or update now.*
- Dashboard › Updates lists the plugin under **Plugins** with a checkbox and **Update Plugins**.

Updating downloads the package and verifies its SHA-256 and signature exactly like an install;
nothing is replaced when either does not match. The package is then replaced and, if the plugin was
active, the new version is loaded straight away with the permissions granted before (only those the
new version still asks for; if it asks for more, deactivate and activate it to review them). Plugins
that were uploaded are updated by uploading the new version.

Appearance › Add Theme does not list Hub themes yet, so there are no theme updates from the Hub.

## Offline sites

Without a reachable Hub the screens show only built-in plugins and local packages, and uploads
still work. Nothing else in the admin depends on the Hub.
