---
title: Sharing your theme
description: How to get a theme package onto every Pursae site through the Hub, or keep it to yourself, and how versions replace each other.
weight: 95
section: themes
---

A theme package is a zip with `theme.json`, `templates/` and `assets/`
([your first theme](your-first-theme.md)). You can share it with everyone or keep it private.

## Submit it for everyone

1. Email the zip, a link to the source and a short description to **hello@pursae.com**.
2. Open a topic in the Pursae forum at [pursae.com/forum](https://pursae.com/forum/).

After review it is signed with the Pursae key and listed on the [Hub](../hub/publishing.md), so it
appears under Appearance › Themes › Add Theme on every site. Add a screenshot and `tags` in
`theme.json` so it is easy to find.

## Keep it to yourself

Nothing needs to be submitted. Upload the zip under Appearance › Themes › Add Theme › Upload on
each site, or place it in `PURSAE_THEMES_DIR` (default `<data>/themes`). Theme packages are not
signed; they are verified by SHA-256 only when they come from the Hub.

## Versions

- Raise `version` in `theme.json` for every release. Installing the same slug at a **higher
  version replaces the folder**.
- Edits made to an installed package on the server (Theme File Editor) are lost when it is replaced.
  Make changes in your source and upload a new version.
- Do not edit a bundled theme in place: after a core update the edit shadows the new version of that
  file. Copy it into a package of your own instead
  ([upgrading](../getting-started/upgrading.md#your-own-plugins-and-themes)).

Designing in the builder instead? See [themes with Pursae Web Builder](builder-themes.md); those
designs are not packages.
