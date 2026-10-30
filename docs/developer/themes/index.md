---
title: Themes overview
description: Bundled Go themes versus theme packages, and what a theme can control.
weight: 10
section: themes
---

WordPress Go renders the site with Go `html/template` files. A theme is a set of those templates
plus assets, a description of the widget areas, menu locations and Customizer options it offers,
and the styles and scripts it loads. There are two kinds:

| | Bundled Go themes | Theme packages |
|---|---|---|
| Examples | Twenty Sixteen, Twenty Seventeen, Twenty Twenty, Twenty Twenty-One, Hello Golementor, Classic | anything you upload |
| Where | compiled into the binary (`web/themes/<slug>/` + a `theme_<slug>.go`) | `<data>/themes/<slug>/` (`WPGO_THEMES_DIR`) |
| `functions.php` equivalent | Go code: a `*Theme` registered at start-up with hooks for `wp_head`, body classes, template data | `theme.json`: everything a Go theme registers in code, declared as data |
| Install | ship with WordPress Go | Appearance › Themes › Add Theme › Upload, or from the [marketplace](../hub/marketplace.md) |
| Code | yes (Go, reviewed with the core) | **none** — templates, CSS, JS, images. Not signed; verified by SHA-256 |

This section is about **theme packages**: what you write to make a theme for WordPress Go.
Everything a package can express is also what the bundled Classic theme uses, so its templates in
`web/themes/classic/templates` are the reference implementation.

## What a theme controls

- the markup of every page kind: index, single, page, archive, search, 404 and the parts header,
  footer, sidebar, comments ([templates](templates.md));
- its stylesheets and scripts, editor styles and preloaded fonts ([styles and scripts](styles-and-scripts.md));
- widget areas and menu locations;
- Customizer sections and settings with live preview ([customizer](customizer.md));
- what the block editor knows about it: colour palette, wide alignment, supports
  ([block editor support](block-editor-support.md)).

## What the core does for every theme

The core builds the whole page model — the document title, `wp_head()` output, body classes,
the loop, pagination, comments, menus, widget areas, the admin bar — and the theme decides how
to print it. A theme never queries the database or sanitises input; it formats data the core
already prepared, which is why packages can be code-free and safe to install.

## Golementor themes

The page builder brings its own base theme, **Hello Golementor**, and can save a complete site
design (header, footer, templates, global styles) as a theme of its own
([Golementor](golementor.md)). Those live in the database, not as packages.

## Editing on a live site

Appearance › **Theme File Editor** edits any theme's templates and CSS in place; saves are
validated and rolled back on error ([file editor](file-editor.md)).

Start with [your first theme](your-first-theme.md).
