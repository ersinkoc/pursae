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
| Code section | doc section `kind: "code"` | `content.html` — the part's markup; `content.head` — head markup it needs (stylesheets, preloads, head scripts, inline styles); `content.wrap` — the wrappers it sits in (opening tags, outermost first, one per line, e.g. `<main id="main">` then `<div class="aft">`); `content.kit`, `content.lang`, `content.body` (body class) for the editor; `content.htmlAttrs` / `content.bodyAttrs` — the design's other `<html>` attributes and its `<body>` attributes as written in an opening tag (`class="no-js" data-theme="dark"`), printed by the front end; `content.active` — on a header or footer template made by a site import, the rule that marks the current page's link ([Active links](#active-links)); `content.binds` — the content bindings of its elements ([Binding to content](#binding-to-content)). Top-level only. |
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

A page without a footer (an app screen whose header bar, side panels and main sit in one grid wrapper:
umbral's Motif page) or without a header shares the shell with the one part it has
(`webbuilder.ShellPlan`): the header opens it and the page closes it, or the page opens it and the footer
closes it (what the design has after or before the shell — dialogs, a toast — stays where it is). Printing it twice would split the design's grid into two boxes.

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
- **`blob:` images and media**: a page with code sections allows `blob:` in `img-src` and `media-src`
  (a design that exports a canvas with `toBlob` and shows it, or plays a fetched file); a blob URL is
  content the page made itself.
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
| `DELETE kits/{slug}` | removes the kit, its files and its override. |
| `GET kits/{slug}/override` | the kit's class and variable edits (see [Classes and variables](#classes-and-variables)): `{live: {rules}, version, draft: {rules}, draftVersion, pending, updated}` |
| `PUT kits/{slug}/override` | `{draft: {rules}, base: draftVersion}` — replaces the draft (the editor sends it on every change); a stale `base` is 409 `wb_kit_override_conflict` (with the current state in `data.override`), an override that does not validate is 422 `bad_css`, more than 256 KB 413. |
| `POST kits/{slug}/override/publish` | `{base?}` — the draft goes live on every page that loads the kit's files (the editor calls it after publishing a page of the kit). |
| `GET kits/{slug}/pages` | `{pages: [{id, title, type, html}]}`: the code-section markup of every builder page and template using the kit (at most 500 pages, 8 MB), for the "From this design" palette and the class counts. Reading only. |

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
  Any other `data-*` attribute whose value is the path of a kit file (`data-wide="../assets/film.mp4"`,
  read by the design's script) becomes its kit URL too; other `data-*` values are left alone and never
  reported (they need not be URLs). The values of an `<script type="importmap">` (never its specifier
  keys) that name kit files, or folders holding kit files (`"three/addons/": "/assets/vendor/addons/"`),
  point at the kit.
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

`web_builder_import_site {kit, pages: [{path, html}], status?, front_page?, templates?, menus?, collections?, site_name?}` imports a whole
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
   whole-site templates (which are listed in the warnings). Pages that have no header (or footer) in
   the design — a bare `404.html`, a landing page without a footer — and a page whose footer sits inside
   its `main` (it keeps it there, `FooterInPage`) are excluded from the whole-site template
   (`exclude/singular/page/<id>`, `exclude/404`; `SiteImport.Uncovered`), so the theme does not draw
   another page's header or footer on them.
4. **Pages**: each page is published with `layout: site` (the templates bring its header and footer),
   its title, description, robots and Open Graph tags (page settings and Pursae SEO).
5. **Idempotent**: everything the import makes carries the post meta `_wb_site_kit` (the kit) and
   `_wb_site_key` (the page's file, `dir:<folder>` for a placeholder, `header-<n>`, `footer-<n>`, `404`).
   A second import of the kit updates the same pages (slug and parent too) and templates; templates of an
   earlier import it no longer needs are turned into drafts.

6. **Kit at the site's root**: the import marks its kit (`wbKit.Root`, the time of the import) and the site
   then serves the kit's files at its root as well, after every route and before the 404
   (`wbKitRootAsset`): `/assets/fonts/x.woff2` answers with the kit's `assets/fonts/x.woff2`. A static
   design addresses its files as its host serves its folder, and the site import maps that folder onto the
   site's paths (`a/index.html` → `/a/`), so URLs the design builds where the importer cannot see them keep
   working: root-relative `url(/assets/…)` in the kit's stylesheets, an inline module's
   `import "./js/engine.js"` on `/post/`, `fetch("data.json")`, a script reading `../assets/film.mp4` on
   `/identity/`. Only `GET` / `HEAD` of a path outside `/wp-*` that is a valid kit file (the kit's
   extensions, no HTML, no dot segments) and exists; a route of the site always wins; with several such
   kits the most recent import wins. `Cache-Control: public, no-cache`. "Remove this import" takes the
   mark away.

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
   `templates: false`), use `index.html` as the front page (default on), make a Pursae menu of the header's
   navigation (default on, MCP `menus`), the folders to import as collections (a checkbox per folder of two
   pages or more, ticked when the analysis found that its pages reproduce under single templates; MCP
   `collections`; the analysis's **Content** table lists the templates and the lists bound as loops, see
   [Binding to content](#binding-to-content)), publish or import as drafts. Then
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
every declared font face (`document.fonts`, also the ones the first layout did not ask for: a face that
starts loading late otherwise lands on one side only, and under load that showed as 0.5–1.4 % of
re-rasterized text on enerjiosgb) and loaded images; each side is captured alone, in Chromium without LCD text and GPU (otherwise
text antialiasing varies with load). `--self` compares the source with itself (the noise floor),
`--mask <selector>` hides live third-party content on both sides (a Google Maps iframe draws its tiles
at its own pace), `--only <substring>` narrows the pages, `--images all` keeps every image. A scenario
file adds states: `[{"name", "pages", "widths", "steps": [{"hover"|"click"|"focus": selector},
{"press": key}, {"scroll": y}, {"wait": ms}], "fullPage": false}]`
(`builder/test/fidelity/scenarios/enerjiosgb.json` opens the "Kurumsal" menu). Output: `report.json`
(per page, scenario and width: `diffPct`, sizes, differing bands, page errors, console errors and
failed requests of both sides) and `report.html` with the source, target and diff images of everything
that differs; the exit status is 1 when a shot is over `--target` (0.1 %). The run prints, per shot,
the errors only the target had (a CSP violation, a 404 of a file the import did not map). `--block
<url-substring,…>` aborts requests on both sides: a live third-party API (a GitHub star count) answers
differently and rate-limits. Mask or block only what differs when the source is compared with itself
(`--self`): canvas / WebGL frames, video, time- or scroll-driven demos, live data.

Corpus results (November 2026, 1440 / 1024 / 768 / 390 px, fresh site per design, see the table in the
branch's report): enerjiosgb (55 pages), landing/merge (29, tr + en + forum folders), landing/eyvah (15),
umbral-site (8, ES modules, import map, root-relative paths, tr/), umbral/tema (7, the same design with
relative paths and bundles), testlanding (1, everything inline, three.js from a CDN), kurumsal-kimlik (2),
splitkb-blog-tema (2), veteriner-site (1) are at 0 % or under their own noise floor once the
non-deterministic parts are masked.

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
footers and the shared shell look as they do on the site (an editor without `edit_theme_options` gets
their markup without their content bindings: a bound form's `to` / `cc` and redirects stay on the
server). The header and footer are locked: hovering
them shows **Edit header** / **Edit footer**, which opens that template in the editor. The device
buttons set the frame's width — desktop is a fixed 1440 px page scaled to fit the panel, tablet
768 px, mobile 390 px — so the design's own `@media` queries apply.

**Static edit mode.** The design's scripts do not run: the frame is sandboxed without
`allow-scripts`, and it gets a neutralised copy of the markup (see [Security model](#security-model));
the stored markup is never changed. What is on screen is therefore the stored markup, and every
operation is applied to the frame at once without reloading it (the changed sections' new markup is
reconciled with the elements already there, as neutralised nodes). **Live edit** (bottom bar) runs
the scripts again, for designs whose content only appears through them (sliders, counters); a
structural change then reloads the frame. **Preview** runs them too. Both run the design with the
author's admin access, so the first time in an editor session the editor asks **Run this design's
scripts with your admin access**; until the author confirms, Live edit stays off and Preview shows
the page without its scripts (a banner offers to run them). The answer is never stored: every editor
session asks again. An element the design hides (a closed menu, the second slide) can be selected in
the navigator and **shown temporarily** (toolbar or context menu); that is editor-only, like **Hide
in the editor**.

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
- the context menu copies and pastes elements (after or inside), copies and pastes styles and
  classes, duplicates, wraps, unwraps, selects the parent, hides or temporarily shows;
- a double-click on a text (or Enter, or a second click on the selected text) edits it in place, in
  the frame, with bold / italic / link / clear formatting; Enter or a click elsewhere saves, Escape
  cancels. The new markup is written from the source, not from the frame: the elements inside keep
  their stored tags byte for byte (classes, ids, `on…` handlers the static copy does not have), an
  `svg` or other code inside is copied as stored, and only what the edit added (bold, italic, a link
  with a safe address, a line break) is written new, without attributes;
- keys work inside the frame too: undo / redo, copy / paste, duplicate (Ctrl/⌘ D), Delete, Escape
  (parent), the arrows (previous / next sibling, parent, first child), Alt + arrows (move); keys are
  left alone while a text is being edited.

**Adding elements** (bottom bar › **Add**, `elements/PaletteDrawer.tsx`). The drawer over the canvas's
left edge has two palettes; an item is dragged onto the canvas (the drop indicator shows where it
goes, with the same rules as moving an element) or clicked (it goes after the selected element, into
it when that is an empty container, else at the end of the selected section's content). Each insert
is one history entry and selects the new element.

- **Elements** (`elements/palette.ts`): Section, Flexbox, Grid, Div; Heading, Text, Button, Link,
  Divider, List, HTML / Embed; Image (a neutral placeholder), Icon (inline SVG), Video; form fields
  (input, textarea, select, checkbox, submit). They are plain semantic HTML with a **local-style
  preset** (a heading's size and weight, a button's padding, color and hover, a field's border…), so
  they look reasonable inside any design without its classes, and every value of the preset is an
  ordinary local style to change. An empty container shows a dashed outline and a minimum height in
  the editor only.
- **From this design** (`elements/miner.ts`): the design's own building blocks. The editor mines the
  markup of every page and template using the kit (`GET kits/{slug}/pages`, plus the open page) for
  elements with the same tag and class list (order ignored) at least three times, groups them —
  buttons, headings, cards, badges, texts, links, list items, images, sections, containers — and shows
  each with a live thumbnail drawn in a small frame with the design's stylesheets (and the kit's
  override). Inserting one copies the first occurrence's markup with new `data-wbc` ids, its text
  kept as the placeholder. In a utility-class design (Tailwind) this is how a new button or card
  gets the design's look. The catalog is computed in the editor (it shares the editor's
  source-preserving parser and element types, and it follows the published markup without an import
  step) and cached in `localStorage` per kit and markup hash.

**Content tab** (Studio › Content, `elements/ContentPanel.tsx`, through the seam's `Content`): what the
selected element says and where it points — rich text (bold, italic, link, clear), a link (address,
a page or post of the site from the search, new tab with `rel="noopener"`), an image (replace from the
media library: `srcset` / `sizes` are removed and a `<picture>`'s sources point at the new file, width
and height follow the new image; alt text; object fit as a local style), an icon (pick one of the
editor's icons, paste SVG code or upload an `.svg`: sanitized to plain SVG, the icon keeps its classes,
size and ARIA attributes), a video / embed source (poster, controls, autoplay, muted, loop, inline; an
iframe's title), a form field (name, type, placeholder, value, required, a select's options), the
**tag** (h2 → h3, div → section, ul ↔ ol, a ↔ button: attributes and children kept; void, raw-text,
SVG elements and elements without an end tag are not changed), the **HTML inside** (embeds; new
elements get `data-wbc` ids) and the **attributes** (id, `aria-*`, `data-*`, title, rel, …: names and
values are checked — never `on…` handlers, `data-wbc`, `style`, `class` or `srcdoc`; `javascript:`,
`vbscript:` and `data:` URLs are refused, an image source may be a `data:image`). Without a selected
element the tab shows the section's HTML and head code as before (also under **Section code**). The
rich-text box is built node by node from the source tree, never by parsing the design's markup in the
editor's page: `b`, `strong`, `i`, `em`, `u`, `s`, `a`, `br`, `span`, `code`, `sub` and `sup` without
attributes (a link keeps its address when it is safe), text as text, anything else (an image, an
icon) as a placeholder chip that is written back as stored.

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

### Security model

The editor runs on the site's own origin and holds the author's REST nonce
(`window.__WEB_BUILDER__`), so anything of the design that runs in a same-origin frame can act as the
author. A code section is trusted when it is **published** (its author has `unfiltered_html`), but
opening a page in the editor must not run a design the author has not chosen to run — an imported
design may come from anywhere. The element editor therefore has three rules
(`builder/host/elements/ElementsCanvas.tsx`, `frame.ts`, `neutral.ts`, `richtext.ts`):

1. **Static edit mode runs nothing.** The frame is `sandbox="allow-same-origin"` without
   `allow-scripts`: no script, handler, `javascript:` URL or nested document in it can run, whatever
   the markup holds or however a parser reads it (nested frames inherit the sandbox). The editor does
   all its work from outside — its listeners on the frame's document (selection, hover, drag and drop,
   keys forwarded), measuring (`getBoundingClientRect`, `getComputedStyle`, the CSSOM read by the
   Style panel), in-place text editing (`contenteditable`, the frame's own `execCommand`), the editor
   stylesheets — which a sandbox without scripts allows. The sandbox and the document always change
   together (a new frame element when they do).
2. **The copy is neutralised by the browser's parser** (`neutral.ts`), independently of rule 1. The
   frame's copy is parsed like the frame parses it (a `<template>` or the parent element as context,
   in a document without a browsing context, so nothing loads or runs while it is read), every node is
   walked in every namespace (HTML, SVG, MathML, template contents), and: scripts of every namespace
   become empty `<script type="text/x-wb-inert">`; `on…` attributes, `srcdoc`, `xml:base` and a
   template's `shadowrootmode` / `shadowroot` go; `javascript:`, `vbscript:` and non-media `data:`
   URLs in any URL attribute (`href`, `src`, `action`, `formaction`, `data`, `poster`, `xlink:href`,
   `srcset` candidates, SVG animation `values` / `to` / `from` / `by`, …) become
   `about:blank#wb-blocked`; SVG animations of `href` or a handler, `<meta http-equiv=refresh>` and
   `<base>` are removed; `<object>` / `<embed>` lose what they load; nested frames get `sandbox=""`;
   `<noscript>` is emptied (as a frame with scripts would show it) and `<plaintext>` becomes `<pre>`.
   Serialising and parsing again is repeated until the result is stable (else the markup is shown as
   text), the frame's DOM is swept again after it loads, and the reconciler inserts only neutralised
   nodes imported into the frame (no `createContextualFragment` or `innerHTML` of design markup in a
   live document anywhere in the editor). The stored markup is never changed: only the frame's copy.
3. **Running the design's scripts is the author's explicit choice**, once per editor session (Live
   edit, Preview): they then run with the author's access, as on the published page. An opaque-origin
   frame was considered for Live edit and Preview: it breaks the editing itself (the editor could not
   reach the frame's DOM), the design's same-site fonts and module scripts (CORS from an origin of
   `null`), and its `localStorage` / cookies, so the confirmation is the boundary instead.

The **Content panel** never puts design markup into the editor's page (the text box is built from
the source tree, see above), and text edits are written back from the source (`richtext.writeBack`).
Tests: `test/host/elements-neutral.test.ts` (every payload of the review and a mutation-XSS list,
jsdom) and the browser regression `node builder/test/security/editor-xss.mjs` (Playwright Chromium,
not part of `make builder-test`: run `npx playwright install chromium` once in `builder/`), which
draws all payloads with the editor's own modules under the report and the enforced CSP and edits
them through the reconciler; it fails when one reads the nonce, and checks that the unprotected
control does.

The per-run frames of mixed pages (patches `0018` / `0019`) are not covered: they always run the
design's scripts (same origin), as before.

## Binding to content

An imported design need not stay a set of static pages: its navigation, its lists of posts, its
repeated pages and its forms can be bound to what content editors manage in Pursae, while the markup
and CSS stay the design's (`internal/webbuilder/codebind.go`, `sitebind.go`; server
`webbuilder_slots.go`, `webbuilder_sitebind.go`, `webbuilder_codeforms.go`).

### The model

A binding names an element of a code section by its `data-wbc` id and is stored in the section's
`content.binds` (JSON `{"<data-wbc>": CodeBind}`, contract §8.1); the markup is not touched. On publish
`FillCodeWith` → `CompileBinds` turns the bindings into the dynamic engine's placeholder attributes
(contract §11.5) on the bound elements only, splicing the source (every other byte stays), and the
snapshot is filled per request like a Theme Builder template's (cached programs, `wb-loop` cache tag for
loops, menu changes purge every page).

| `kind` | On | What the site shows |
|--------|----|---------------------|
| `menu` | a list (`ul`, `ol`, a nav container) | one item per item of a Pursae menu: `location` (a theme location, Hello Pursae `primary` / `footer`) or `menu` (a menu id). Every element child of the list is an item **template**: an item uses the template it was imported from (`items`: menu item id → template `data-wbc`), else the first child holding a submenu when the item has sub-items (`parent`), else the first plain child. In the template the first `<a>` gets the item's URL and target (`menu.link`), the first element with text of its own (the link, else a button) its title, replacing only that text (`data-wb-text`: a chevron icon beside it stays). |
| `submenu` | a list inside a menu item template | the item's sub-items, with the same template rules (one level). |
| `loop` | a container | its first element child (the card template) once per post of `query` (`source` query by default; `postType`, `perPage` 1–24, `orderBy` date / modified / title / menu_order / comment_count / rand, `order`, `terms` `category:12,…`, `author`, `offset`, `parent` (children of an entry, for pages), `exclude: current`; `source: main` takes the request's own posts on an archive). The other children are the design's sample cards and are left out; the white space after the template separates the cards as in the design. |
| `field` | any element (inside a loop's first card: that card's post; elsewhere the post a single template shows) | `title` (as stored, no texturizing), `excerpt` (`words`), `content` (imported markup is printed as stored; block content goes through the_content), `image` (the featured image's `src` and `alt` — `srcset: true` adds `srcset`, `sizes`, `width`, `height` of `size` —, else the URL field `name`), `url` (`href`), `link` (`href` and the title as its text), `date` / `modified` (`format`), `author`, `terms` (`tax`, `sep`; names as text, `links: true` term links), `custom` (`name`: a Pursae Custom Fields field, else the post meta value of that name — the compiled element carries `data-wb-meta`, which only bindings set (the snapshot sanitizer drops it from an author's markup, whose `post.field` slots read registered fields only, never other meta of the posts a loop shows); `attr` "" text, `html` sanitized like post content, `url` href / src, `attrs` the attributes from `<name>_href`, `<name>_src`, `<name>_alt`, … plus the text from `<name>` with `text: true`), `pagination` (the main loop's page links). An element with icons beside its text keeps them: only its own text is replaced. |
| `form` | a `<form>` | posts to the form endpoint (below). |

Editor: select an element and open **Advanced** in the inspector: **Content binding** binds it (the
kinds the element can take: a list or container → Menu or Loop, a list inside a menu item → Submenu, an
element of a loop's first card or of a template → Field, a `<form>` → Form) and shows what the site
will list (the menu location's items, the loop query's posts). The frame keeps showing the design's
own markup. MCP: `web_builder_bind {post_id, element | html_id, bind | null}` (publishes the page).

### Active links with a bound menu

The site import's active-link rule ([Active links](#active-links)) keeps working on a bound header: the
generated items are the imported ones with the menu's URLs, so the rule finds them by `href`. A link the
rule does not know (an item added in Appearance › Menus) that points at the requested page gets the
look of its role's current item when that role marks links on the page they link to
(`ApplyActive`). Pages of a group (below) are marked the same way in their template's own side menu
(the template's first section carries a rule, applied to the main part).

### Forms

A bound `<form>` gets `action` = `/wp-json/web-builder/v2/forms/{post}/{section}-w{data-wbc}`,
`method="post"`, `data-wb-form` (so it gets the nonce, the captcha widget and the redirect of builder
forms, contract §3.5) and `data-wb-code-form`, plus the hidden `wb_t` and honeypot `wb_hp` inputs. Its
schema is read from the stored markup on every submit (`CodeFormSchema`): every named `input`, `select`
and `textarea` (not buttons, files, hidden or password inputs) is a field, mapped by `fields` (input
name → `name email phone message date choice company subject` or `field_<name>`, `-` leaves it out;
unmapped inputs map to themselves when they are one of those names, else to `field_<name>`), labelled
by its `<label>`, required when it has `required` / `aria-required="true"` or is listed in `required`; a
`select`'s value must be one of its options, a radio group's or checkbox's one of the `value`s its
inputs declare (`on` without one). A refused submit answers 422 with `data.fields` keyed by the
design's input names (`full-name`, not `field_full_name`), so the script marks those inputs
`aria-invalid="true"`. Submissions are validated, stored and mailed like builder forms (Settings › Web
Builder lists every field: the named ones, then each `field_<name>` by name, the message last; `to`,
`cc`, `success`, `failure`, `redirect` override per form). The editor boot leaves the bindings of the
header and footer parts out for users without `edit_theme_options`, since a form's recipients are there.

Code pages load no builder runtime, so a small script (`wbCodeFormJS`) sends bound forms. The design's
own submit handlers run first, so its validation shows its own errors. A handler that builds a
`mailto:` link reads the address from a `data-*` attribute of the form (enerjiosgb: `data-mail`); while
the submit is dispatched, reading such an address stops that handler there and counts as "the design
accepted the form", so no mail program opens. After the handlers the form is sent with `fetch` when the
design accepted it (it read the address, showed its success element or hid the form, or did not handle
the submit at all and the browser's constraint validation passes); a field the design marked
`aria-invalid="true"` keeps it from being sent. The answer shows the design's success element (`done`:
its `data-wbc`; `hidden` attribute or `hidden` class removed, the form hidden the same way) or error
element (`error`), else a status line after the form. Without JavaScript the form posts as a plain
form and comes back with `?wb_form=sent`, where the script shows the success element.

### Site import

`web_builder_import_site` (and the admin's options) binds what a design shows to be content, and only
where the bound markup reproduces the design: every plan is rendered with the planned content and
compared with the design's markup (parsed, editor ids and binding attributes aside, white space and
staggered `animation-delay` / `transition-delay` values normalized); what does not reproduce stays
markup and is listed in the report's `bindings.notes`.

- **Menu** (`menus`, default on): the first list inside a `nav` of the header template whose items all
  have a label is made into the menu "Design navigation (<kit>)" (a page item for a link to an imported
  page, else a custom link; `#` for a button that opens a panel), assigned to the `primary` location and
  bound; a list inside an item whose items are links becomes its sub-items. A second list in the header
  with the same items (a mobile menu) is bound to the same menu when it reproduces too. A second import
  refills the menu; "Remove this import" deletes it.
- **Collections** (`collections`: `true` or a list of folders; default none, the admin ticks the folders
  that qualify): the pages of a folder (`hizmet/*.html`) are aligned element by element. Where they
  differ: an attribute that a slot may write (href, src, srcset, alt, …), an element's own text, or —
  where the structure differs — an element's whole content. Classes and state attributes are left to
  an active-link rule (`ClusterParts` over the pages with every difference set to the first page's). The
  varying `<h1>` is the **title** (other texts equal to it, a breadcrumb's last item, show the title
  too), the largest region that differs on every page is the **content**, every other difference a
  custom field `design_<n>` (`design_<n>_href`, `_src`, … for attributes) stored as post meta. A folder
  qualifies when its pages share their sections, wrappers and head, have one varying `<h1>`, and besides
  the title and the content at most two regions differ (≤ 20 % of a page). Pages whose side menus come
  in a few versions (enerjiosgb's four service categories) are split by that region into variants of two
  pages or more, one template each.

  **URLs**: the pages stay **pages** under the folder's parent page, so `/hizmet/3/` is still
  `/hizmet/3/` with no rewrite rule — the hierarchical page type is the simplest scheme that keeps every
  imported URL (a custom post type would need its own rewrite slug per folder, a category would change
  the permalinks). Each page keeps its id, SEO and menu items; it loses its builder doc and holds its
  title, its content (the varying region's markup) and its fields; the Theme Builder **single-page**
  template draws it (`include/singular/page/child_of/<folder parent>` for the largest variant, so a
  page added under the folder later gets it, `include/singular/page/<id>` for the other variants'
  pages; priority 10). A folder that does not qualify stays builder pages; the report says why.
- **Listings**: a list on another page whose element children each link to a different page of a
  collection folder (`haberler.html`'s news cards) becomes a loop over the folder's pages: query
  `postType: page`, `parent` = the folder's parent page, `orderBy: menu_order asc`, `perPage` = the
  number of cards, `offset`. The cards are aligned like pages: the link to the page is `url` (`link` when
  it holds the title), a text equal to the page's `<h1>` is `title` (the pages' titles become their
  `<h1>`), the longest other text (40+ characters) the `excerpt` (set on the page), a varying image the
  `image` field (no featured image yet: its URL in the field `list<n>_<k>`), the rest custom fields
  `list<n>_<k>`. The longest list sets the pages' `menu_order`; another list is bound when it shows a
  window of that order (`offset`). A new page under the folder shows in the lists by its `menu_order`
  (0 sorts first) with its title and link; its other fields are empty until they are filled.

<h3 id="binding-limits">Limits</h3>

- A menu item template is one element of the list. A design whose dropdown panel lives outside the
  list (enerjiosgb's "Kurumsal" and "Hizmetlerimiz" mega panels sit after the nav) keeps that panel as
  markup: the item's button is a menu item (`#`), its panel is not filled from sub-items. One submenu
  level; items added in Appearance › Menus use the first plain item's markup (or the first item with a
  submenu when they have sub-items).
- Loops repeat their first child; per-card differences that are not content (a staggered
  `animation-delay` in a `style` attribute) take the first card's value.
- A group's custom fields are per page values the import found (prev / next links, a category label);
  they are edited as post meta (Pursae Custom Fields shows them when it has a field of that name). A
  region that differs in structure is kept as the design wrote it in the protected meta
  `_wb_html_<name>` (printed as stored: only trusted code writes protected meta); a public field
  `<name>` an editor sets instead is sanitized like post content.
  Images of a group are URL fields, not featured images.
- Collections are detected among the pages of one folder; pages in the site's root are not grouped.
- The bound form script stops a design's `mailto:` handler only when it reads the address from a
  `data-*` attribute of the form; a design whose script hard-codes the address opens the mail program too
  (the submission is still stored).
- The editor shows the design's own markup in the frame (the bound loop's sample cards, the menu's
  imported items); the binding panel lists what the site will show.

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

### Classes and variables

A design's own classes and variables are edited without writing the kit's files (plan §4.3 b, c):

- **Class chips** (top of the Style tab, `elements/ClassPanel.tsx`): the element's classes; × removes
  one, the field adds one (autocomplete from the classes the design's stylesheets define, read through
  the frame's CSSOM), each one history entry. **Copy classes / Paste classes** (context menu) replace an
  element's class list with another's.
- **Editing a class.** A click on a chip first shows how many elements carry the class on this page
  and on the site (`GET kits/{slug}/pages`) and which design rule the edit goes to; **Edit class**
  turns the whole Style tab (the same controls, device and state switchers) into an editor of that
  class: a change applies to every element with it. The edit is a declaration in the kit's
  **override stylesheet** with the design rule's own selector inside the design rule's own at-rule
  context — `@layer utilities{.rounded-2xl{border-top-left-radius:40px}}` for Tailwind's
  `.rounded-2xl` — so it has the design rule's specificity and layer and wins exactly where the
  design rule did. Tablet and mobile add the kit's `@media (max-width:…)`, a state adds the
  pseudo-class (`.btn:hover`). A design `!important` declaration is overridden `!important`. A class
  the design has no rule for alone gets a new unlayered rule. **Reset every change of this class**
  removes its rules; **Done** returns to the element's own style.
- **Variables** (bottom bar › **Variables**): the custom properties the design defines on `:root`,
  `html` or `:host` (enerjiosgb: 65), grouped as colors (with a swatch and a color picker), fonts,
  spacing and sizes, other. A new value is written to the override as the defining rule —
  `@layer theme{:root, :host{--color-brand-600:#1d4ed8}}` — in the same layer, or the same media
  query when the design only defines it there (a variable redefined in a dark mode or at a breakpoint
  is edited at its first, unconditional definition). The reset button per variable goes back to the
  design's value.
- **Matched rules** (Advanced tab): every rule that applies to the selected element in the device and
  state being edited, the winner first — its selector, file (`css/style.css`, `<style>`, Local style,
  Class or variable edit) and context (`@layer utilities › @media (min-width: 40rem)`) — with each
  declaration struck through unless it is the one in force.

**Undo, draft, publish.** An edit writes the kit's whole new override into the document's `wbKits`
field, so undo / redo treat it like any other change; the field is the editor's working copy and is
never stored with the page (the host removes it before saving and publishing). Whenever the working
copy differs from the server's draft it is sent (`PUT kits/{slug}/override`, debounced, versioned),
so a reload or another page of the same design shows it. The frame prints every kit's working copy
in `<style data-wb-kit>`, after the design's stylesheets. **Publishing** a page publishes the drafts
of its kits (`POST kits/{slug}/override/publish`): from then on every page whose code head loads the
kit's files prints `<style data-wb-kit-override="{slug}">` right after its code head — after the
design's stylesheets and local styles — whether or not the page was published again. With no
override (or after every edit is reset) nothing is printed, so the pages are byte for byte what they
were. Writing the override needs `edit_theme_options` and `unfiltered_html`, like the kit; the
server validates every rule (`webbuilder.ParseKitOverride`: `@media` / `@layer` / `@supports` /
`@container` contexts only, selectors without blocks, markup, URLs or forbidden tokens, declarations
as local styles take them, and the printed stylesheet must pass the CSS sanitizer unchanged). The
override is kept in `wb_kit_overrides` beside the kit, so a new upload of the kit keeps it; deleting
the kit drops it. Go's `KitOverrideCSS` and the editor's `overrideCSS` print the same bytes (shared
fixtures `internal/webbuilder/testdata/override`).

## Limits

- The element editor draws only header and footer templates made of code sections; a header made
  of builder sections is not shown around a code page in the editor (it is on the site).
- `::before` / `::after` are not styled yet (neither locally nor in a class edit). Style edits on mixed
  pages stay inline styles (patch `0019`), and their frames do not show local styles or class edits.
- Where a value comes from is computed for a horizontal, left-to-right page; shadow DOM, `@scope` and
  user-agent styles are not modelled, and stylesheets from other origins cannot be read (their
  classes are not offered and cannot be edited; a class edit then makes a new unlayered rule). The
  matched-rules view names a rule's file and its position, not its line; nested CSS rules (CSS
  nesting) are listed under their parent's selector and are not class-editable.
- A class edit goes to the class's own rule (`.btn`); rules that only use the class in a compound or
  descendant selector (`.card .btn`, `.btn.primary`) keep their values and may still win. A variable
  defined in an anonymous `@layer` cannot be overridden in the same layer and is not listed.
- Class and variable edits are per kit and site-wide by design: there is no per-page variant of a
  class (use the element's local style). Two editors changing the same kit at once: the second save
  is refused (409) and asks for a reload.
- "From this design" offers a sample as the design wrote it: an `li` card dropped outside a list stays
  an `li`, and elements the design's scripts reveal (`.reveal`, scroll animations) stay hidden in the
  static editor until Live edit or the published page runs the script. Thumbnails are drawn without
  the design's scripts.
- The palette's Image starts with an inline placeholder and the Video without a source: set them in
  the Content tab. Form fields keep the design's own form handling (none for a field outside a form).
- Editing a text rewrites the element's inner markup (only when the text changed): elements inside
  keep their stored tags, text the edit did not touch keeps its spelling, but changed text is written
  with the editor's escaping (`&amp;`, `&lt;`, `&gt;`; other entities as characters), and formatting
  the browser adds that is not on the allowlist (a `style` on a span, a `<font>`) is dropped. An
  element holding scripts or styles is not edited in place.
- Static edit mode shows what the neutralised copy shows: a design's `<embed>` / `<object>` content,
  declarative shadow roots (`<template shadowrootmode>`), nested frames' scripts (a YouTube player)
  and `<noscript>` content are not drawn; `data:` URLs other than images, fonts, audio and video
  (and SVG images outside `img`, `image`, `source`, `video`, `input`, `link`) are blocked.
- Live edit and Preview ask once per editor session before running the design's scripts; the answer
  is not stored, and the mixed-page frames (patches `0018` / `0019`) do not ask.

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
- URLs a design's scripts build relative to the page (`img/x.png`, `../data.json`) resolve against the
  page's site URL. The site import maps `a/index.html` to `/a/` and the root kit serves `/a/img/x.png`
  from the kit's `a/img/x.png`, as the design's host did; but `a/b.html` becomes `/a/b/`, one folder
  deeper, so a relative URL built by a script on that page misses the kit (attributes and CSS are
  rewritten and are not affected). Root-relative URLs assume the site at the domain's root.
- A design over the kit limits (100 MB, 2000 files) or with more than 500 pages (landing/new-landing:
  1110 pages, 198 MB with documentation exports) is imported in parts (several kits) or trimmed first.
- An app shell whose page is drawn by a JavaScript bundle from an API or a build step (a Vite `dist/`
  served under a base path, a dashboard reading a backend) is not a static site; it can be imported, but
  only the empty shell is a code section to edit.
- A site import needs every page in one call (at most 500); the active-link rule matches request paths,
  so it needs pretty permalinks. A design's form keeps its own handling (enerjiosgb's opens `mailto:`)
  until it is bound to the form endpoint ([Binding to content](#binding-to-content)).
- Bindings: see [Binding to content › Limits](#binding-limits).
