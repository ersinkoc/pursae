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

Cards show *Requires Pursae X* and *Tested up to Y* from the listing's `requires_core` and
`tested_core`; a listing that needs a newer core than the running one shows a disabled
**Incompatible** button (a dev build is never reported incompatible). The listing's `tags` are links
that filter the screen (`?tag=<tag>`). Not built yet: a detail modal and server-side paging/sorting.

Listings that need a license carry a **Pro** (a Pro license) or **Private** (granted to your
license) badge, and a beta version a **Beta** badge; they only appear once the key is entered under
Settings › License ([licenses](index.md#licenses)).

**Install Now** downloads the package from `download_url`, verifies that its SHA-256 matches
`sha256` from the listing and that its signature verifies against a trusted key (the Hub's keys
are trusted automatically), then stores it as an inactive plugin and records the key that signed
it: later versions must come from the same publisher and must not be older
([updates and the signer](../plugins/packaging-and-signing.md#updates-and-the-signer)). **Activate** opens the
permission screen where you grant what the plugin may do ([permissions](../plugins/permissions.md)).

The **Upload Plugin** tab installs a zip you have on disk; it goes through the same signature
check, so your own key must be in `WPGO_PLUGIN_KEYS` or `plugin_trusted_keys`, or
`plugin_allow_unsigned` must be on for development.

## Appearance › Themes › Add Theme

The **Library** tab is the Hub's theme catalog (`GET /api/v1/themes`, every page, cached 12 hours
per license key): screenshot, name, category and style from each listing's `manifest` (the
version's `theme.json`), category filters and search, plus **Upload Theme**. Theme packages contain
no code, so they are not signed; **Install** downloads the package, checks its size and the SHA-256
from the listing, validates `theme.json`, the templates and the demo like an upload, and unpacks it
into `<data>/themes/<slug>/`. **Live Preview** builds a sandbox with the theme's demo content from
the downloaded package (kept in `<data>/theme-library-cache/`) before anything is installed.
Screenshots are fetched once by the site and served from that cache.

Listings marked **preinstall** by the operator are installed (not activated) on every new site
right after its installer, in the background; existing sites get them with **Install recommended
themes** on the Library tab. Without a reachable Hub the tab says so; `WPGO_THEME_LIBRARY_DIR`
serves the library from a local directory instead (development, offline sites).

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
that were uploaded are updated by uploading the new version. The update row shows the listing's
optional `changelog` (plain text) under the message.

**Automatic updates.** Plugins installed from the Hub have an *Enable auto-updates* link on the
Plugins screen (option `pursae_plugin_auto_updates`). The daily `wpgo_hub_check` then updates them
by itself. An active plugin is only updated this way when the new version asks for no permission that
was not granted before; otherwise the administrator reviews it from the Plugins screen.

Themes installed from the Hub (and library themes installed before the source was recorded) are
updated the same way: Appearance › Themes shows *New version available. Update now* on the card and
in the details, Dashboard › Updates lists them under **Themes** with **Update Themes**. The package's
size and SHA-256 must match; the theme is replaced in place and stays active if it was. Uploaded
theme packages are updated by uploading the new version. Nothing is updated automatically.

## Offline sites

Without a reachable Hub the screens show only built-in plugins, bundled themes and local packages
(and the theme library of `WPGO_THEME_LIBRARY_DIR`, if set), and uploads still work. Nothing else in the admin depends on the Hub.
