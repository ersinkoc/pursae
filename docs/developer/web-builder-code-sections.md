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
dragged into another container, duplicated, wrapped, deleted and restyled — texts, links and images are changed in
place, and the sections are reordered, hidden and removed like any other.

Use code sections for a finished design (an agency hand-off, an exported landing page, an animated
product page). Use the builder's own sections (`custom` free-form sections, the design library) for
pages that people will restyle by dragging; those are what `method: "exact"` produces.

Code sections are privileged, like `html` sections: only users with `unfiltered_html` can write them
(administrators and editors on a single site). Everyone else who edits the page keeps them as
stored (see [Limits](#limits)).

## The pieces

| Piece | Where | What |
|-------|-------|------|
| Design kit | `uploads/web-builder/kits/<slug>/` | The design's files (CSS, JS, fonts, images) with their folder structure, so the design's relative references keep working. Option `wb_design_kits` lists kits: name, version, file count, size, and the https origins their scripts and stylesheets name. |
| Code section | doc section `kind: "code"` | `content.html` — the part's markup; `content.head` — head markup it needs (stylesheets, preloads, head scripts, inline styles); `content.wrap` — the wrappers it sits in (opening tags, outermost first, one per line, e.g. `<main id="main">` then `<div class="aft">`); `content.kit`, `content.lang`, `content.body` (body class) for the editor; `content.htmlAttrs` / `content.bodyAttrs` — the design's other `<html>` attributes and its `<body>` attributes as written in an opening tag (`class="no-js" data-theme="dark"`), printed by the front end; `content.active` — on a header or footer template made by a site import, the rule that marks the current page's link ([Active links](#active-links)). Top-level only. |
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

- the design's document: the front end prints its `<html lang>` and other `<html>` attributes and its
  `<body>` attributes (`content.lang`, `content.htmlAttrs`, `content.bodyAttrs`; the page's first,
  then the header's and the footer's, merged by name, classes joined, inline event handlers dropped):
  on a canvas page (`wbCanvas`) and under Hello Pursae, whose `<html lang>` is otherwise `en-US`. The
  lang matters to the pixel: `text-transform: uppercase` writes Turkish `i` as `İ` only under `lang="tr"`;
- a code page's markup is printed as written: `wp_filter_content_tags` (image dimensions, `loading`,
  `decoding`, `srcset`) does not run over it (`wbContentTags`). Pursae Cache's page optimizer, when its
  options are on, still adds `loading="lazy"` / `decoding="async"` and collapses whitespace in the whole
  response (it does not change what is drawn).

### Shared shell

Many designs put one wrapper around their header, main and footer (`<div class="flex min-h-screen
flex-col">`): the import keeps it as the first line of every part's `wrap` (the *shell*, see
[Importing](#importing)). When the header template, the page and the footer template of a Hello Pursae
request are all code parts, `FillCode` has printed the shell in each of them; the front end
(`wbCodeParts`, `webbuilder.ShareShell`) prints it once around all three: the header keeps the shell's
opening tags and loses their closing tags, the page loses both, the footer loses the opening tags. The
wrappers shared are the common start of the header's last section's chain, the footer's first section's
chain and every section of the page (`webbuilder.ShellLen`). A main that is not a code part (a theme
view such as a blog post, builder sections, a forum view) is printed inside the header's and footer's
shell; a code page in other wrappers (another design) is not, and then nothing is shared. The markup is
checked before anything is cut (the shell must be the header's last element, the footer's first and the
page's only one); otherwise each part keeps its own copy, as before. A canvas page (no header and footer
part) prints its own shell once by construction.

`posts.content` (feeds, search, excerpts) gets the sections' markup without scripts, styles and
templates.

Kit file URLs in the published markup get `?v=<kit version>`; requests that name the current version
are served `immutable`, others revalidate (`Cache-Control: public, no-cache`), so a changed kit is
picked up at once. Uploading files to a kit purges the page cache.

### Content Security Policy

Code sections work under the site's nonce policy (`csp_mode` `report` and `enforce`, see
`internal/server/csp_nonce.go`):

- **Scripts get the nonce where the stored markup is read**, never by rewriting the finished page
  (`cspTrustContent`, `internal/server/csp_trusted.go`): the published snapshot of a page, a header
  or footer template and a popup is passed through it before it is filled (`wbFill`), so the
  sections' own `<script>` elements and the head scripts travelling in
  `<template data-wb-code-head>` carry the nonce placeholder before the front end lifts the head
  into `<head>` (`CodeHead` / `MergeHeads`). The outermost handler swaps the placeholder for the
  response's nonce.
- **External scripts** (`<script src="https://cdn…">`) load because the tag carries the nonce:
  under the enforced policy (`'strict-dynamic'`) browsers ignore host lists, and the modules such a
  script imports are allowed by `'strict-dynamic'` too. The hosts are still added (below) for
  browsers without `'strict-dynamic'` and for the report-only policy.
- **Origins** (`wbCodeCSP`): same-origin kit files need nothing. When a kit is published Pursae
  reads its scripts and stylesheets for `https://host` references (CDN modules, APIs: at most 24
  origins); pages whose code sections load from that kit allow those origins in `script-src`,
  `connect-src`, `style-src` and `font-src`. From the sections' own markup and head: `<script src>`
  origins go to `script-src` and `connect-src`; `<link href>` origins and the `url()` / `@import`
  origins of inline CSS go to `style-src` and `font-src` (`fonts.googleapis.com` brings
  `fonts.gstatic.com`); `<iframe src>` origins (a Google Maps embed) go to `frame-src`. Only https
  origins, at most 24 per directive. `img-src` and `media-src` already allow `https:`. In report
  mode the additions to `script-src` are also merged into the report-only policy (only into the
  directives it has). The editor page allows the kits' origins for users who may publish code
  sections (the design's frame inherits the editor's policy).
- **Inline event handlers** (`onclick="…"`, `onload="…"`) cannot run under a nonce policy, and
  Pursae adds neither `'unsafe-inline'` nor `'unsafe-hashes'` for them. The importer counts them
  and warns (`ConvertCode` → `warnings`, per attribute and element); move them into the design's
  scripts (`addEventListener`). Under the report-only policy they run and are reported.
- **`csp_admin_scripts` off** (Settings › Security):
  stored administrator scripts, the code sections' included, get no nonce. Under the enforced policy
  the design's scripts are then blocked (the page shows its markup and CSS without its JavaScript);
  under the report-only policy they run and are reported. `web_builder_import_html` warns when it
  imports a design with scripts on such a site.

## Kits: REST

All under `web-builder/v2` (Web Builder on, the usual `X-WB-Api` handshake). Reading needs `edit_posts`;
writing needs `edit_theme_options` **and** `unfiltered_html`.

| Route | |
|-------|-|
| `GET kits` | `[{slug, name, version, files, bytes, updated, url}]` |
| `GET kits/{slug}` | the kit and `fileList: [{path, bytes}]` |
| `POST kits/{slug}/files` | `{files: [{path, text} \| {path, data: base64}], delete?: [path], fresh?: bool}` — writes into the kit's staging folder (a copy of the live files, or empty with `fresh`). Paths are relative, plain segment names, web asset extensions only (css, js, mjs, map, json, txt, fonts, images, svg, video/audio, glb/gltf, wasm, pdf; no HTML, no server code); 15 MB per file, 2000 files and 100 MB per kit, 24 MB per request. |
| `POST kits/{slug}/commit` | `{name?, breakpoints?: {tablet, mobile}}` — the staged files replace the live ones in one swap; the version goes up. Without `breakpoints` the local-style breakpoints are suggested from the kit's stylesheets (see [Styling](#styling)). |
| `DELETE kits/{slug}` | removes the kit and its files. |

## Importing

`webbuilder.ConvertCode(html, CodeImportOptions{KitURL, KitFiles, Base, LinkMap, Split})`:

- **Head**: `<title>`, meta description, robots and Open Graph tags become the page's SEO (page
  settings and Pursae SEO); stylesheets, preloads, inline styles, scripts and `theme-color` become the
  sections' `head`; site icon, canonical, alternates, manifest and `<base>` are dropped (the site's own).
- **Shell**: the importer finds the page's `main` (the first `<main>`), its site header (the first
  `<header>` before main that no `article`, `section`, `aside`, `nav`, `form` or `dialog` holds) and its
  site footer (the first such `<footer>` after main; the last one of a page without main) at any depth,
  and the nearest element that holds them all. The wrappers from the body down to it are the shell
  (`CodeImport.Shell`): every part's `wrap` starts with them. A header or footer inside a wrapper of
  the shell takes that wrapper along; a main inside wrappers that hold nothing else gets them in its
  sections' `wrap`.
- **Body**: each element child of `<main>` (or of the shell without one) is a section. A plain `div`
  holding two or more sections (`section`, `article`, `header`, `footer`, `aside`, `nav`, or elements
  with an id) is opened, and the sections inside it carry it in `wrap`. Scripts and styles go with the
  section before them; icon sprites and skip links with the section after them; scripts after `main`
  (and after the shell) with the last section.
- **Split** (`templates: true`): the site header with everything the shell holds before it (skip link,
  sprites, an announcement bar) becomes the header template, the site footer with everything the shell
  holds after it (a to-top button, modal and lightbox containers) the footer template; both keep the
  shell as their `wrap`. Elements beside the shell's wrappers (a sprite before it in the body, a
  lightbox after it) become further sections of those templates (`HeaderBefore`, `FooterAfter`), each
  in the wrappers it sat in. A footer the design placed inside `main` stays on the page as well (the
  design's CSS expects it there) and the footer template excludes that page.
- **Document**: `<html lang>` (`Lang`), the other `<html>` attributes (`HTMLAttrs`) and the `<body>`
  attributes (`BodyAttrs`, class included) are kept on every section.
- **References**: relative and root-relative URLs in `src`, `href`, `srcset`, `poster`, `data-src`,
  `action`, `style` and `<style>` `url()` / `@import` resolve against the file's folder in the kit
  (`Base`). A kit file becomes its kit URL; a link in `LinkMap` (by the path resolved in the kit, then
  as written) becomes its site URL; other page links and missing files are reported. A query string
  stays on the mapped URL (`hakkimizda.html?tab=2#x` → `/hakkimizda/?tab=2#x`, `app.js?x=1` → the kit
  URL with `?x=1`); a kit file's `?v=` is replaced by the kit version when the page is printed.
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

### Importing a site

`web_builder_import_site {kit, pages: [{path, html}], status?, front_page?, site_name?}` imports a whole
static site in one call (`internal/server/webbuilder_siteimport.go`, `webbuilder.ConvertSite`). Upload
the folder's CSS, JS, fonts and images as the kit first (`web_builder_kit_upload`); the HTML files travel
in `pages` (a kit holds no HTML, so nothing of the design is served as a page from uploads). Harness:
`pursae_mcp.py kit-upload <folder> <kit>` then `pursae_mcp.py import-site <folder> kit=<kit>`.

1. **Page tree** (`webbuilder.PlanSite`): `index.html` at the top is the front page (slug `home`, set as
   the static front page unless `front_page: false`); `a/b.html` is page `b` under the page of folder
   `a`, which is `a.html` or `a/index.html` when the site has one, else a **draft placeholder** (so
   `/a/b/` resolves and `/a/` stays a 404, as in the design); `a/index.html` is folder `a`'s own page;
   `404.html` becomes the Theme Builder **404 template** (`error-404`, `include/404`). Slugs are the
   lowercased file names (`hizmet/3.html` → `/hizmet/3/`).
2. **Links**: the pages are created (drafts) or found first, and their permalinks (as published) make
   the link map of every link between the design's pages, folder links (`hizmet/`, `./`) included.
   Pretty permalinks are needed for the menu's active link (plain `?page_id=` links are warned about).
3. **Templates**: every page's header (with what goes with it) and footer are clustered
   (`webbuilder.ClusterParts`): copies that differ only in the current page's menu link are one
   template with an [active-link rule](#active-links); copies that differ otherwise, or that the rule
   does not reproduce byte for byte, are clusters of their own. The largest header and footer cluster
   apply to the whole site (`include/general`), the others to their pages
   (`include/singular/page/<id>`, `include/404`); all with priority 10, so they win a tie with other
   whole-site templates (which are listed in the warnings).
4. **Pages**: each page is published with `layout: site` (the templates bring its header and footer),
   its title, description, robots and Open Graph tags (page settings and Pursae SEO).
5. **Idempotent**: everything the import makes carries the post meta `_wb_site_kit` (the kit) and
   `_wb_site_key` (the page's file, `dir:<folder>` for a placeholder, `header-<n>`, `footer-<n>`, `404`).
   A second import of the kit updates the same pages (slug and parent too) and templates; templates of an
   earlier import it no longer needs are turned into drafts.

The answer lists the pages (`post_id`, `url`, sections), `pages_created` / `pages_updated`, the templates
(type, id, pages, conditions, `active_links`), `templates_disabled`, `unmapped_links`, `missing_files`
and `warnings`. The import needs `unfiltered_html` and `edit_theme_options`; the templates show with the
Hello Pursae theme.

### Active links

A static design marks the current page in its menu by hand: its header differs from page to page only
in a few classes and attributes (enerjiosgb: `text-brand-600` and `aria-current="page"` instead of
`text-ink-700 hover:text-ink-900 hover:bg-ink-100/80` on "Anasayfa" of `index.html`; the same classes on
the "Kurumsal" menu button of `hakkimizda.html` and of every `kadro/*.html`; `bg-brand-50 text-brand-700`
on the dropdown item of the page itself; the "Bizden Haberler" link on every `haber/*.html`). The site
import learns that from the pages' copies (`webbuilder.ClusterParts`, `internal/webbuilder/activelinks.go`):

- copies with the same skeleton (tags, text and every attribute but `class` and the state attributes
  `aria-current`, `aria-selected`, `data-active`, `data-current`, `data-state`, `data-selected`) are
  aligned element by element; an element whose class or state attributes vary is *marked* on some pages;
- its plain state is the one the varying elements show most across the site (so a menu button marked
  on most pages, because most pages are services, still gets the plain look of the other buttons);
- a **role** is a kind of element (tag + plain state) and how it looks marked: `exact` on the page it
  links to, `near` on a page of its section (classes only, no `aria-current`, as the design does);
- an **item** is one element (named by `href:/path/`, `controls:<aria-controls>`, `id:<id>` or its
  place `#n` among its role's elements) with `exact` (marked on the page it links to), `paths` (other
  pages that mark it) and `prefixes`: a folder all of whose imported pages mark it (`/haber/`,
  `/kadro/`, `/hizmet/`) — so a page added there later is marked too. A single-page folder stays a path.

The rule (`ActiveRule` JSON) is stored as `content.active` of the header template's first section; the
template itself holds the header with every element in its plain state. On each request the front end
(`webbuilder.ApplyActive`) rewrites only the opening tags of the elements the requested path marks
(`c.Request.URL.Path`, matched as a site path with a trailing slash), copying the rest of the markup as
it is; a new attribute goes where the import would have written it (attributes in name order — the HTML
parser sorts those of `<a>` — stay so, else at the end). The import checks every page: the rule applied
to the template must give that page's header byte for byte, or the page gets a header template of its
own. Editing the template in the editor keeps the rule; an element whose plain classes are changed is no
longer recognised (its role no longer matches) and is simply not marked.

### Fidelity check

`builder/test/fidelity/fidelity.mjs` (Node + Playwright, `make builder-fidelity SRC=<folder>
SITE=<url> [MAP=map.json] [OUT=dir] [SCENARIOS=file.json] [ARGS=…]`) serves the source folder and
compares each page with its imported URL, full page at 1440, 1024, 768 and 390 px, as the share of
differing pixels (YIQ distance, threshold 0.1, as `builder/test/parity`). Without `MAP` the URLs follow
the site import's plan (`404.html` against a missing URL). Both sides load with reduced motion, a style
that turns transitions, animations and the caret off, a paused clock (timers run for `--clock` ms, 1000
by default, so a slider does not move on), are scrolled through (scroll reveals finish) and wait for
fonts and loaded images; each side is captured alone, in Chromium without LCD text and GPU (otherwise
text antialiasing varies with load). `--self` compares the source with itself (the noise floor),
`--mask <selector>` hides live third-party content on both sides (a Google Maps iframe draws its tiles
at its own pace), `--only <substring>` narrows the pages, `--images all` keeps every image. A scenario
file adds states: `[{"name", "pages", "widths", "steps": [{"hover"|"click"|"focus": selector},
{"press": key}, {"scroll": y}, {"wait": ms}], "fullPage": false}]`
(`builder/test/fidelity/scenarios/enerjiosgb.json` opens the "Kurumsal" menu). Output: `report.json`
(per page, scenario and width: `diffPct`, sizes, differing bands) and `report.html` with the source,
target and diff images of everything that differs; the exit status is 1 when a shot is over `--target`
(0.1 %).

### Forum views

Pursae Forum draws its views (forum list, topics, login, …) itself, so they are not code sections. A
design's forum mockups reach them as forum **site templates** (`forum_set_template`, see
[Forum themes › Site templates](themes/forum.md#site-templates)): import one mockup with
`templates_only: true` for the header and footer, then give each view the mockup's `<main>` as a
template with `main: true`. Under Hello Pursae such a view is printed like a code page: the design's
main between the code header and footer, no theme title, wrapper or stylesheet, no `forum.css`.

## Editing

A page made only of code sections (every imported page, and a header or footer template of code
sections) is edited by the **element editor** (`builder/host/elements/`, installed through the thin
seam patch `0020-elements-seam`, which hands the canvas and the navigator rows of code sections to
the host). A page that mixes code sections with the builder's own sections keeps the per-run frames
of patches `0018-code-sections` / `0019-code-blocks` (see below).

**The canvas is one frame with the whole page**: the header template, the page's code sections and
the footer template that Hello Pursae draws around the page on the site (`Boot.codeParts`, read on
the server by `wbEditorCodeParts`; contract §2), in their shared wrappers, so fixed headers, sticky
footers and the shared shell look as they do on the site. The header and footer are locked: hovering
them shows **Edit header** / **Edit footer**, which opens that template in the editor. The device
buttons set the frame's width — desktop is a fixed 1440 px page scaled to fit the panel, tablet
768 px, mobile 390 px — so the design's own `@media` queries apply.

**Static edit mode.** The frame gets a copy of the markup in which the design's scripts do not run
(a `<script>`'s type becomes `text/x-wb-inert`, inline `on…` handlers are dropped, head scripts
included); the stored markup is never changed. What is on screen is therefore the stored markup, and
every operation is applied to the frame at once without reloading it (the changed sections' new
markup is reconciled with the elements already there). **Live edit** (bottom bar, per editing
session) runs the scripts again, for designs whose content only appears through them (sliders,
counters); a structural change then reloads the frame. **Preview** always runs them. An element the
design hides (a closed menu, the second slide) can be selected in the navigator and **shown
temporarily** (toolbar or context menu); that is editor-only, like **Hide in the editor**.

**Selecting and editing.** Every control is drawn over the frame, outside the design's DOM (the
frame only gets the `data-wbc` ids and one `<style data-wb-edit>`):

- a click selects the innermost element (a header or footer element: nothing); the outline shows its
  type — Section, Flexbox, Grid, Div, Heading, Text, Link, Button, Image, Icon, Video / embed, List,
  Form element, Code — derived from the tag and the computed `display`, never stored; the breadcrumb
  in the bottom bar shows its path;
- the toolbar selects the **parent**, moves the element **up** / **down**, **edits its text**,
  **duplicates**, **wraps it in a Div** and **deletes** it; its handle (or pressing on the selected
  element) **drags** it: the drop indicator follows the target container's direction (flex row /
  column, grid, flow), the frame scrolls at its edges, and void elements, the inside of an `svg`,
  elements with text of their own, the element itself and the header / footer refuse the drop.
  Elements move into any container, also into an empty one and into another section;
- the context menu copies and pastes elements (after or inside), duplicates, wraps, unwraps, selects
  the parent, hides or temporarily shows;
- a double-click on a text (or Enter, or a second click on the selected text) edits it in place, in
  the frame, with bold / italic / link / clear formatting; Enter or a click elsewhere saves, Escape
  cancels;
- keys work inside the frame too: undo / redo, copy / paste, duplicate (Ctrl/⌘ D), Delete, Escape
  (parent), the arrows (previous / next sibling, parent, first child), Alt + arrows (move); keys are
  left alone while a text is being edited.

The **navigator** (Studio › Structure, or **Layers** in the bottom bar) lists each code section's
elements with their type icons. Rows are dragged to reorder or reparent elements (also across
sections), renamed with a double-click — the names are stored in the section's `content.names` (a
JSON map keyed by `data-wbc`), so the design's markup stays clean — hidden in the editor and
collapsed.

Every operation splices the source markup of the section(s) it changes at the element's ranges (a
small source-preserving parser, `elements/html.ts`), so nothing outside the edited element changes
byte for byte (whitespace, quoting, entities), and writes them back as **one** history entry with its
own label. A page opened, edited and edited back publishes exactly what it published before. A
duplicate or a pasted element gets new `data-wbc` ids, and its HTML ids become `name-2`, `name-3`, …
(free on the page) with the `aria-controls`, `aria-labelledby`, `for` and `href="#…"` references
inside the copy following them, so the design's accordions, tabs and labels work on both; the inside
of an `svg` is copied as is.

**Mixed pages** (patches `0018` / `0019`): a run of consecutive code sections is drawn as one frame
with the design's scripts running; a click selects an element and edits its text in place, the
toolbar selects the parent, moves the element among its siblings, duplicates and deletes it, and
**Style** sets inline styles (text color, background, font size and weight, alignment, padding,
margin, gap, corner radius, width, max width, opacity), classes and the raw `style` attribute;
**Layers** shows the run's elements. Structural changes reload the frame.

In both, the section list reorders, hides, duplicates and removes code sections, and the inspector
shows the section's HTML and head code for anything the canvas does not reach.

## Styling

The element editor styles any element of a code page without touching the design's markup or CSS
(plan §4.3a). Select an element and press **Style** on its toolbar (or open Studio › Style /
Advanced):

- **Device and state.** The device switcher is the canvas device (desktop, tablet, mobile), so the
  design's own `@media` queries show what that device shows; the state switcher edits Normal,
  Hover, Focus, Focus visible or Active — while a state is edited the canvas shows the element in it.
  A dot marks devices and states with values. Devices are desktop first: a desktop value applies
  everywhere, a tablet value at the tablet breakpoint and below (mobile inherits it), a mobile value
  only on mobile. A state's value is added to the normal one.
- **Style tab:** Layout (display; flex direction, wrap, justify, align, gap; grid columns, rows, gaps,
  place items; as a flex or grid child grow, shrink, basis, order, align self, grid column / row),
  Spacing (margin and padding as a box model), Size, Position, Typography (font family from the
  design's own families or the font library, size, weight, line height, letter spacing, color,
  align, transform, decoration), Background, Border (per side, radius per corner) and Effects
  (shadow, opacity, transform, transition, filter, backdrop filter). **Advanced tab:** custom
  properties (any CSS property, custom properties included), copy / paste / clear style, the
  element's classes and inline style.
- Every control shows the value in force as its placeholder and **where it comes from**: Local
  style, the design rule's selector (`.sm\:py-24`, `#hero .title`), Inline style, Inherited from
  an ancestor, Default — read from the frame's stylesheets through the CSSOM (cascade layers,
  `!important`, media, nesting and specificity; flow-relative properties such as Tailwind's
  `padding-block` count for their physical twin). A reset button clears the value of this device
  and state; lengths have a unit selector, and every field takes raw CSS (`calc()`, `var()`,
  `clamp()`); a number alone gets the unit.
- When the design's winning declaration is `!important`, the local one is written `!important`.
- **Inline styles.** Editing a property that the element's `style` attribute sets moves only that
  declaration (or the shorthand that covers it) into the local style; the attribute's other
  declarations stay. Inline styles written by the mixed-page Style panel (patch `0019`) are read
  as they are, so they need no migration.
- **Copy / paste style** (context menu, Advanced tab) copies an element's whole local style.
  Duplicating or pasting an element copies its local styles to the copy; moving an element to
  another section takes them along; deleting it drops them (undo brings them back).

The values are stored in the section's `local` field (contract §8.1 "Local styles"), one history
entry per change. On publish the element gets the class `e{post}-{id}` and the page one stylesheet
with rules `.e{post}-{id}:not(#_):not(#_)` (the two `:not(#_)` raise the specificity by one id
without changing what matches, so the rule beats the design's own rules without editing them);
the editor's frame prints the same stylesheet with `[data-wbc="…"]` selectors (the same weight) in
one `<style data-wb-local>` it rewrites on every change, so nothing reloads. Both come from the same
generator (Go `LocalCSS`, TypeScript `localCSS`, tested byte for byte on shared fixtures).

**Breakpoints** are a design kit setting (`breakpoints: {tablet, mobile}` in `wb_design_kits`, max-width
in px): on commit they are suggested from the kit's stylesheets — the most used `@media` width
switch between 800 and 1280 px for tablet and between 480 and 768 px for mobile (enerjiosgb's
Tailwind: ≤ 1023 and ≤ 639 px) — or taken from the commit request. Kits committed earlier get the
suggestion on first use. Tablet must be 768–1439 and mobile 390–767, so the editor's 768 px tablet
and 390 px mobile frames are inside their ranges. A changed breakpoint applies when the page is
published again.

Library fonts chosen in the panel are linked on the published code page (only those its local
styles name) and in the editor's frame.

## Limits

- The element editor draws only header and footer templates made of code sections; a header made
  of builder sections is not shown around a code page in the editor (it is on the site).
- Local styles (code pages) style one element; a change to every card at once (a class) and the
  design's variables come with Faz 5. `::before` / `::after` are not styled yet. Style edits on mixed
  pages stay inline styles (patch `0019`), and their frames do not show local styles.
- Where a value comes from is computed for a horizontal, left-to-right page; shadow DOM, `@scope` and
  user-agent styles are not modelled, and stylesheets from other origins cannot be read.
- New elements come from duplicating or pasting an existing one, which keeps the design's classes;
  the palette of new elements comes later.
- Editing a text replaces the element's inner markup with what the browser serialises (only when the
  text changed); inline `on…` handlers inside that element are not kept in static edit mode, and an
  element holding scripts or styles is not edited in place.
- Live edit is a setting of the editing session, not stored per page yet.

- A code section is published as written: its markup is trusted (the author has `unfiltered_html`).
  Kits are site-wide code: only administrators should hold them.
- **Permissions.** Users without `unfiltered_html` (authors, contributors, editors on a network)
  can open and save a page or template with code sections, but cannot change them: on autosave,
  publish and revision restore the server takes each code or html section the editor sends by id
  from the stored doc (the published doc, then the user's own draft), with its stored content, at
  the position the editor gives it, and keeps the stored `style.css` of every section
  (`webbuilder.DocOptions.Stored`). They can reorder, hide and remove code sections; their edits of
  a code section's markup or head, an html section or custom CSS are ignored and reported as
  `stripped`, and new code sections from them are dropped. Their publish fills the snapshot with
  the stored, validated code markup (`FillCode` on the parsed doc, never on the request's), drawn
  on the server from that doc so html sections and custom CSS stay too. See the contract, §8.1
  "Privileged parts on save".
- Mixed pages (code sections and builder sections) keep the builder's root element and stylesheet, so
  the design's `position: fixed` elements are placed relative to the page root and its CSS meets the
  builder's reset; import whole designs as code pages.
- The editor shows scroll-driven effects at their scroll position inside the frame; viewport units
  follow the frame, which is at most as tall as the screen.
- Hello Pursae wraps non-code parts in its own elements; a header or footer template made of code
  sections is printed without a wrapper.
- A site import needs every page in one call (at most 500); the active-link rule matches request paths,
  so it needs pretty permalinks. Forms keep the design's own handling (enerjiosgb's opens `mailto:`);
  nothing on the site receives them yet.
