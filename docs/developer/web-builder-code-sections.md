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
| `POST kits/{slug}/commit` | `{name?}` — the staged files replace the live ones in one swap; the version goes up. |
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

`web_builder_import_site {kit, pages: [{path, html}], status?, front_page?, templates?, site_name?}` imports a whole
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

### Importing from the admin

**Web Builder › Import HTML site** (`/wp-admin/web-builder-import`, Turkish "HTML'den site") does the site
import without a command line or an agent (`internal/server/webbuilder_siteimport_admin.go`,
`webbuilder_siteimport_zip.go`). The menu entry and every route need `edit_theme_options` **and**
`unfiltered_html` with Web Builder on, like the MCP tool; the server checks the same on each request.

1. **Upload** a zip of the design's folder (a form field for the kit slug, default: the zip's name). The
   body is streamed to a file under `uploads/private/web-builder-imports/<id>/` (the private folder
   only serves signed links), then unpacked entry by entry with the kit's limits (100 MB, 2000 files, 15 MB
   a file). A path that leaves the folder (`..`, absolute, drive letter, backslashes counted as slashes),
   a symbolic link, an entry that is not a plain file, case-twin names, and files a kit cannot keep
   (extension outside the kit's list, names outside `wbKitRel`) reject the whole archive with the names
   listed; `.git`, `.DS_Store`, `__MACOSX`, `Thumbs.db` and other dot files are skipped silently. `.html` / `.htm`
   files are kept aside for the importer and never go into a kit. One top folder around everything is
   stripped. Unpacked sites are removed after a week or with "Discard this upload".
2. **Analysis** (nothing is written to the site): `webbuilder.PlanSite` and `ConvertSite` run on the staged
   files — they are pure, so the analysis is the real conversion. It lists the pages with their proposed
   addresses (`SitePlan.URLs`) and whether each creates or updates a page of an earlier import of the kit, the
   draft parent pages, the header and footer clusters (pages served, marked menu links), missing files,
   links to pages that are not in the zip, the external origins the design loads from (kit files and pages),
   the inline `on*` handlers the nonce policy blocks, the `csp_admin_scripts` / enforced-CSP warning
   (`wbCodeScriptsWarning`), plain permalinks and a theme other than Hello Pursae, and the viewport widths
   of the CSS `@media` conditions (`webbuilder.SuggestBreakpoints`, listed only).
3. **Options**: kit name and slug (an existing kit is replaced only after ticking a box), split the header
   and footer into templates (default on; off keeps them in every page, `layout: canvas`, MCP
   `templates: false`), use `index.html` as the front page (default on), publish or import as drafts. Then
   **Import**: the staged assets are installed as the kit (the same stage + commit as the kit REST routes,
   `wbKitCommitStage`) and `mcpWBImportSite` runs, the very function behind `web_builder_import_site`. The
   request is synchronous (a 55-page site takes about two seconds; the server has no write timeout). The
   report lists pages (edit and view links), templates and the importer's warnings. Drafts are published
   afterwards with "Publish the pages" (and take the front page then).
4. **Compare**: the page picker shows the original beside the imported page, both framed on the screen.
5. **Remove this import** (confirmation page first, then "Delete permanently"): the import is recorded in the
   option `wb_site_imports` as the posts it created — the owned ids (`_wb_site_kit`) before and after the
   run are compared, so a second import of a kit that only updates pages records nothing to delete for them —
   plus whether it created the kit and the front page setting it replaced. Removal deletes only posts that
   still exist, have the recorded type and still carry the kit's `_wb_site_kit` mark, and the kit only when
   this import created it; the front page setting returns to its earlier value when it still points at a
   deleted page. Anything else is left alone and counted.

#### Serving the uploaded design

The compare step has to show uploaded HTML, JavaScript included, on the admin's own origin, so it is
treated as hostile (`wbWizDesign`, route `/wb-design-preview/<id>/<path>`):

- `Content-Security-Policy: sandbox allow-scripts allow-forms allow-popups; …` without `allow-same-origin`:
  the document gets an opaque origin, so its scripts cannot read the site's cookies, call the REST API or
  the admin as the logged-in user, or reach the admin page that frames it, and cannot navigate the top
  window; popups keep the sandbox. The header travels with the response, so opening a file in a tab is
  sandboxed too, and the frame carries the same `sandbox` attribute. The rest of the policy lets the design load
  its own files and https resources (fonts, CDN scripts, images) and forbids form posts and `<base>`;
  `frame-ancestors 'self'`. `X-Frame-Options` is dropped from these responses (it makes Chromium refuse
  a sandboxed frame of the same site).
- The staging id (128 random bits) is the credential, like the exact-import page: a sandboxed frame never
  sends the admin's `SameSite=Lax` cookie for its own links and font requests, so a login check could not
  work. The id is shown only to an administrator and expires with the staging folder.
- Files are read only inside the session's folder (cleaned path, no symlinks exist there), with the type of
  their extension, `nosniff`, `no-store`, `no-referrer`, `noindex`, and `Access-Control-Allow-Origin: *`
  (fonts requested by an opaque origin need CORS).

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
