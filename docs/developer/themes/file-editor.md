---
title: Theme File Editor
description: Edit templates and CSS in the admin without recompiling, and where the overlay directory lives.
weight: 90
section: themes
---

Appearance › **Theme File Editor** opens any installed theme's templates, stylesheets and scripts
in the browser, as in WordPress — bundled themes included, although their Go glue
(`theme_<slug>.go`) is compiled and cannot be edited.

## How it works

Theme files are read through an **overlay**: the server first looks in
`<data>/themes/<slug>/<path>` (`WPGO_THEMES_DIR`, the `themes` volume in Docker), then in the
copy embedded in the binary or, for packages, the installed package directory. Saving a file in
the editor writes it to the overlay, re-parses the theme's template set and **test-renders a
page**. If the save produces a template or render error it is **rolled back automatically** and
the editor shows the error, so the site never serves a broken page.

**Restore original** deletes the overlay file, bringing back the embedded or packaged version.
Assets (`style.css`, `js/…`) saved in the overlay are served with `Cache-Control: no-cache`, so
browsers revalidate and pick up the change.

For a theme package, the overlay directory and the package directory are the same, so edits
are edits of the installed files. An edited asset keeps its `?ver=<version>` URL, which is
otherwise cached for a year, so the package records it in `.edited-files` and serves it with
`no-cache` until the package is updated or reinstalled.

## Editing on disk

Anything you put under `WPGO_THEMES_DIR/<slug>/` is picked up the same way; templates are parsed
on the next request. This is the quickest loop while developing a package: point
`WPGO_THEMES_DIR` at your working copy and edit with your editor.

## Turning it off

`WPGO_DISALLOW_FILE_EDIT=1` makes the editor read-only (WordPress' `DISALLOW_FILE_EDIT`); the
overlay still applies to files already there. The editor requires the `edit_themes` capability
(administrators).

## What you cannot edit

Theme glue written in Go, `theme.json` semantics (widget areas, menu locations, Customizer
controls — re-upload the package for those), and plugins: the Plugin File Editor only shows
packages, since plugin code is compiled WebAssembly.
