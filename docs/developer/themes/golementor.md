---
title: Golementor and Hello Golementor
description: The page builder's base theme, the Theme Builder, saved themes and how they relate to theme packages.
weight: 80
section: themes
---

**Golementor** is WordPress Go's visual page builder — a built-in plugin, off on a fresh install,
activated under Plugins. It brings its own base theme, **Hello Golementor**, and a **Theme
Builder** for headers, footers, single and archive templates, popups and loops.

## Hello Golementor

A blank canvas theme (`web/themes/hello-gowebeditor`): minimal markup, no styling of its own,
the right place for a design built entirely in the builder. It appears on the Themes screen only
while Golementor is active; deactivating the plugin switches the site to Twenty Sixteen (the
Plugins screen says so) and hides everything the builder added. Saved themes and built pages are
kept in the database and return on the next activation.

## Theme parts

Appearance › **Theme Builder** (and the Golementor menu) edits theme parts: documents of type
`gwe_template` with a part type (`header`, `footer`, `single-post`, `single-page`, `archive`,
`search-results`, `error-404`) and **display conditions** (`include/general`,
`include/singular/page/12`, `exclude/archive/category/3`, …). When Hello Golementor is active —
or when a part's conditions match a page on another bundled theme — the page renders through the
Hello Golementor templates: `wp_head`, the header part, the matching single/archive/404 part (or
the template's own minimal markup), the footer part, `wp_footer`.

Parts, global styles ("Site Settings": fonts, colours, custom CSS) and the kit live in the
database, not in files.

## Save as theme

The Theme Builder's **Save as theme** takes a snapshot of the current design — kit settings and
custom CSS, every theme part with its element tree, page settings and display conditions, and
the menu locations — and stores it as a saved theme (a row of the themes table with
`custom = true`). Saved themes:

- appear in their own section on the Themes screen with a generated or uploaded screenshot;
- can be **live previewed** with their own global styles;
- when **activated**, load the snapshot into the site's working copy and make Hello Golementor
  the active theme;
- are fixed: editing the site afterwards changes the working copy, not the saved theme. Save
  again to make a new one;
- can be **exported** as a zip (with `manifest.json` and the media they reference) and imported
  on another site.

## How this relates to theme packages

| | Theme package | Saved Golementor theme |
|---|---|---|
| Made with | an editor and `theme.json` | the visual builder |
| Stored as | files under `<data>/themes/<slug>` | a database row + kit post |
| Distributed as | zip with templates/assets ([Hub themes](../hub/marketplace.md)) | Golementor export zip |
| Needs Golementor | no | yes (it renders through Hello Golementor) |
| Customizer | sections from `theme.json` | Golementor's Site Settings |

A theme package can coexist with Golementor: pages edited with the builder render inside the
package's templates through `the_content`, and the builder's theme parts take over only when
their display conditions say so.
