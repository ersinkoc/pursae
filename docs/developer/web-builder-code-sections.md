---
title: Code sections and design kits
description: Import a finished HTML design 1:1 as code sections — its own markup, CSS and JavaScript, served from a design kit — and still edit its texts, links and images and reorder its parts in Pursae Web Builder.
weight: 893
section: built-in-plugins
---

A **code section** is a part of an imported HTML design kept as the design's own markup. Its CSS and
JavaScript stay the design's, so the page is the design 1:1 by construction: scroll effects, canvas
and WebGL scenes, custom fonts and every layout trick keep working, because nothing is converted.
Pursae Web Builder still edits it: every element of the design is a block on the canvas — selected,
moved among its siblings, duplicated, deleted and restyled — texts, links and images are changed in
place, and the sections are reordered, hidden and removed like any other.

Use code sections for a finished design (an agency hand-off, an exported landing page, an animated
product page). Use the builder's own sections (`custom` free-form sections, the design library) for
pages that people will restyle by dragging; those are what `method: "exact"` produces.

Code sections are privileged, like `html` sections: only users with `unfiltered_html` can save them
(administrators and editors on a single site); for everyone else they are stripped on save.

## The pieces

| Piece | Where | What |
|-------|-------|------|
| Design kit | `uploads/web-builder/kits/<slug>/` | The design's files (CSS, JS, fonts, images) with their folder structure, so the design's relative references keep working. Option `wb_design_kits` lists kits: name, version, file count, size, and the https origins their scripts and stylesheets name. |
| Code section | doc section `kind: "code"` | `content.html` — the part's markup; `content.head` — head markup it needs (stylesheets, preloads, head scripts, inline styles); `content.wrap` — the wrappers it sits in (opening tags, outermost first, one per line, e.g. `<main id="main">` then `<div class="aft">`); `content.kit`, `content.lang`, `content.body` (body class) for the editor. Top-level only. |
| Importer | `webbuilder.ConvertCode`, MCP `web_builder_import_html` with `method: "code"` | Splits an HTML document into code sections, gives every element a `data-wbc` id, points file references at the kit. No browser needed. |

## Publishing

The static renderer draws a code section as a placeholder, `<div data-wb-code="{section id}"></div>`,
which the snapshot sanitizer keeps. After sanitizing, the publish route (`wbRESTPublish`) replaces each
placeholder with the section's markup, as written (`webbuilder.FillCode`):

- no element of its own around it, so selectors such as `main > section` keep matching;
- consecutive sections with the same `wrap` share one copy of each wrapper (the design's
  `<main><div class="aft">…</div></main>` comes back as one tree);
- the editor's `data-wbc` ids are removed;
- the sections' head markup travels in the stored snapshot as `<template data-wb-code-head>`; the
  front end lifts it into `<head>`, each distinct tag once (`webbuilder.MergeHeads`), also across the
  header and footer templates of the page;
- a page made only of code sections ("code page") is marked `data-wb-code-page` on its root; the front
  end drops that root element, and prints neither the builder's site stylesheet (`wb.css`, a global
  reset and a size container) nor the builder's font links, the block library stylesheet or Hello
  Pursae's stylesheet, skip link and default header/footer. The design gets the document to itself.

`posts.content` (feeds, search, excerpts) gets the sections' markup without scripts, styles and
templates.

Kit file URLs in the published markup get `?v=<kit version>`; requests that name the current version
are served `immutable`, others revalidate (`Cache-Control: public, no-cache`), so a changed kit is
picked up at once. Uploading files to a kit purges the page cache.

### Content Security Policy

Same-origin kit files need nothing. When a kit is published Pursae reads its scripts and stylesheets
for `https://host` references (CDN modules, APIs: at most 24 origins); pages whose code sections load
from that kit allow those origins in `script-src`, `connect-src`, `style-src` and `font-src`. External
`<script src>` and `<link href>` in the sections' own markup are allowed the same way. The editor page
allows the kits' origins for users who may publish code sections (the design's frame inherits the
editor's policy).

## Kits: REST

All under `web-builder/v2` (Web Builder on, the usual `X-WB-Api` handshake). Reading needs `edit_posts`;
writing needs `edit_theme_options` **and** `unfiltered_html`.

| Route | |
|-------|-|
| `GET kits` | `[{slug, name, version, files, bytes, updated, url}]` |
| `GET kits/{slug}` | the kit and `fileList: [{path, bytes}]` |
| `POST kits/{slug}/files` | `{files: [{path, text} \| {path, data: base64}], delete?: [path], fresh?: bool}` — writes into the kit's staging folder (a copy of the live files, or empty with `fresh`). Paths are relative, plain segment names, web asset extensions only (css, js, mjs, map, json, txt, fonts, images, svg, video/audio, glb/gltf, wasm, pdf; no HTML, no server code); 15 MB per file, 2000 files and 100 MB per kit, 24 MB per request. |
| `POST kits/{slug}/commit` | `{name?}` — the staged files replace the live ones in one swap; the version goes up. |
| `DELETE kits/{slug}` | removes the kit and its files. |

## Importing

`webbuilder.ConvertCode(html, CodeImportOptions{KitURL, KitFiles, Base, LinkMap, Split})`:

- **Head**: `<title>`, meta description, robots and Open Graph tags become the page's SEO (page
  settings and Pursae SEO); stylesheets, preloads, inline styles, scripts and `theme-color` become the
  sections' `head`; site icon, canonical, alternates, manifest and `<base>` are dropped (the site's own).
- **Body**: each element child of `<main>` (or of the body without one) is a section. A plain `div`
  holding two or more sections (`section`, `article`, `header`, `footer`, `aside`, `nav`, or elements
  with an id) is opened, and the sections inside it carry it in `wrap`. Scripts and styles go with the
  section before them; icon sprites and skip links with the section after them; scripts after `main`
  with the last section.
- **Split** (`templates: true`): the first header (with the sprites and skip links before it) and the
  footer become the site's header and footer templates. A footer the design placed inside `main` stays
  on the page as well (the design's CSS expects it there) and the footer template excludes that page.
- **References**: relative and root-relative URLs in `src`, `href`, `srcset`, `poster`, `data-src`,
  `action`, `style` and `<style>` `url()` / `@import` resolve against the file's folder in the kit
  (`Base`). A kit file becomes its kit URL; a link in `LinkMap` (by the path resolved in the kit, then
  as written) becomes its site URL; other page links and missing files are reported.
- **Element ids**: every element outside an SVG drawing (the `<svg>` itself included) gets a
  `data-wbc` id, numbered across the import; `script`, `style`, `template`, `noscript`, `link`, `meta`,
  `br` and the like do not. Sections imported before every element was tagged get the missing ids
  (above the highest one in use) when the editor loads them (`webbuilder.TagCodeDoc`, on the document,
  the user's draft and a revision); they are stored with the next save.

MCP: `web_builder_kit_upload {kit, name, files, delete, fresh, commit}` (several calls with
`commit: false` for a large folder), `web_builder_list_kits`, then
`web_builder_import_html {html, method: "code", kit, path, link_map, templates, templates_only,
header_template, footer_template, title | post_id, status, front_page}`. `method` defaults to `code` for
users with `unfiltered_html`. `templates_only` imports only the header and footer of a file (e.g. a
design's forum or shop page whose content the site renders itself). The skill harness does it from a
folder: `pursae_mcp.py kit-upload <folder> <kit>` then
`pursae_mcp.py import-html <folder>/index.html kit=<kit> path=index.html link_map=@links.json`.

### Forum views

Pursae Forum draws its views (forum list, topics, login, …) itself, so they are not code sections. A
design's forum mockups reach them as forum **site templates** (`forum_set_template`, see
[Forum themes › Site templates](themes/forum.md#site-templates)): import one mockup with
`templates_only: true` for the header and footer, then give each view the mockup's `<main>` as a
template with `main: true`. Under Hello Pursae such a view is printed like a code page: the design's
main between the code header and footer, no theme title, wrapper or stylesheet, no `forum.css`.

## Editing

Builder patch `0018-code-sections` adds the `code` kind to the editor (`src/builder/CodeSection.tsx`).
A run of consecutive code sections is drawn as one page in one frame (`sandbox="allow-scripts
allow-same-origin"`, the design's head plus its wrappers), so the design's scripts see all of its
markup; the frame is as tall as the run, up to the screen, and scrolls inside the canvas beyond that.
In the frame:

- clicking an element selects it (the innermost element with a `data-wbc` id); an element with text
  of its own is edited in place at once (Enter or clicking away saves, Escape cancels); text a script
  drew differently from the source (split letters, counters) is edited as plain text only;
- the toolbar over the selection (patch `0019-code-blocks`) selects the **parent** (also Escape),
  moves the element **up** / **down** among its element siblings, **duplicates** it and **deletes**
  it (not a section's only top-level element: remove the section instead). A duplicate gets new
  `data-wbc` ids, and its HTML ids become `name-2`, `name-3`, … (free on the page) with the
  `aria-controls`, `aria-labelledby`, `for` and `href="#…"` references inside the copy following them,
  so the design's accordions, tabs and labels work on both;
- **Style** sets the element's look as inline style, which wins over the design's classes: text color,
  background, font size and weight, alignment, padding, margin, gap, corner radius, width, max width,
  opacity (a bare number is pixels), plus its classes and the raw `style` attribute. Each field shows
  the computed value as its placeholder;
- **Layers** shows the run's sections and their elements as a tree, open along the path to the
  selection; a click selects and scrolls to an element;
- clicking an image shows **Replace** (Media Library) and its alt text; clicking a link shows its URL;
- links and forms never navigate the frame, and the design's own click handlers (accordions, tabs) do
  not run while editing — use Preview.

Every edit is written back to the section whose markup holds that id (`patchCode`, through DOMParser),
so undo, autosave and publishing work as for any section. The section list reorders, hides, duplicates
and removes code sections; the inspector shows the section's HTML and head code for anything the
canvas does not reach. Every operation works on the section's source markup, never on what the
design's scripts changed in the frame (an accordion's `open` class, split letters), so the page stays
the design's markup. Structural changes (move, duplicate, delete), an undo and a section move reload
the frame, keeping its scroll and selection, and re-run the design's scripts; text and style edits do
not.

## Limits

- Style edits are inline styles: they apply at every screen width. A phone-only change, a hover state
  or a change to every card at once belongs in the kit's CSS (or a `<style>` in the section's HTML).
- Elements move only among their siblings; to move one into another parent, cut and paste it in the
  section's HTML code. New elements come from duplicating an existing one, which keeps the design's
  classes.

- A code section is published as written: its markup is trusted (the author has `unfiltered_html`).
  Kits are site-wide code: only administrators should hold them.
- Mixed pages (code sections and builder sections) keep the builder's root element and stylesheet, so
  the design's `position: fixed` elements are placed relative to the page root and its CSS meets the
  builder's reset; import whole designs as code pages.
- The editor shows scroll-driven effects at their scroll position inside the frame; viewport units
  follow the frame, which is at most as tall as the screen.
- Hello Pursae wraps non-code parts in its own elements; a header or footer template made of code
  sections is printed without a wrapper.
