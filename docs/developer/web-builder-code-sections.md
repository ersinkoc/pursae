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
  `decoding`, `srcset`) does not run over it (`wbContentTags`), and Pursae Cache's page optimizer adds
  no `loading="lazy"` / `decoding="async"` / `fetchpriority` to it either: the front end sets the
  internal header `X-WPGo-Cache-Optimize: as-written` on a code page (canvas, or a Hello Pursae page
  whose main is a code page), which the cache strips and obeys (`cache.HeaderOptimize`). The design
  decides what loads when (Salient's hero `<img fetchpriority="high">` was made lazy before). The
  optimizer's other options (whitespace, scripts) still apply when they are on.

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

A page that shares more wrappers with its footer than with its header (Salient: the header sits in the
shell, the page's rows and the footer in `#ajax-content-wrap` inside it) gets those printed once too
(`webbuilder.ShellDepths`, `ShareShellDeep`): the header opens the shell, the page opens the deeper
wrapper and keeps its content in it, the footer closes the deeper wrapper and the shell. A header that
shares more with the page works the same way the other way round.

A page without a footer (an app screen whose header bar, side panels and main sit in one grid wrapper:
umbral's Motif page) or without a header shares the shell with the one part it has
(`webbuilder.ShellPlan`): the header opens it and the page closes it, or the page opens it and the footer
closes it (what the design has after or before the shell — dialogs, a toast — stays where it is). Printing it twice would split the design's grid into two boxes.

`posts.content` (feeds, search, excerpts) gets the sections' markup without scripts, styles and
templates.

Kit file URLs in the published markup get `?v=<kit version>`; requests that name the current version
are served `immutable`, others revalidate (`Cache-Control: public, no-cache`), so a changed kit is
picked up at once. Uploading files to a kit purges the page cache. Versions never repeat: a staged file
is written through a temporary file renamed over its name (the staging folder starts as hard links to
the live files, which must not change before the commit), and a kit deleted and uploaded again under the
same slug goes on from its last version (option `wb_design_kits_gone`), so browsers never get an old
file for a new `?v=`.

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
  origins, at most 24 per directive. The origins are read from the stored code sections (their
  `content.head` and `content.html`, `wbCodeSource`), their kits and, for a forum view printed by a
  site template, the stored site templates — never from the filled page, whose comments, posts and
  field values anyone may have written: a visitor's `url(https://…)` adds no origin. `img-src` and `media-src` already allow `https:`. In report
  mode the additions to `script-src` are also merged into the report-only policy (only into the
  directives it has). The editor page, for users who may publish code sections, allows only what its
  own frames need (`wbEditorFrameCSP`): a page of code sections only gets the style and font origins
  (and `blob:` images and media), a mixed page also the script, connect and frame origins of the
  published page (see [Security model](#security-model)). The Live canvas frame does not inherit it:
  it is served with the front end's own policy; only the mixed-page frames inherit the editor's.
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

All under `web-builder/v2` (Web Builder on, the usual `X-WB-Api` handshake). The kit list and a kit's
files need `unfiltered_html` (who may use a kit in a code section); the override and the pages need
`edit_posts` (the editor reads them for every page of the kit); writing needs `edit_theme_options`
**and** `unfiltered_html`, and with an Application Password the Appearance (`themes`) scope or full
access ([scopes](rest-api/authentication.md#application-password-scopes)). The two routes at the end
of the table belong to the element editor's live canvas (see [Editing](#editing)).

| Route | |
|-------|-|
| `GET kits` | `[{slug, name, version, files, bytes, updated, url}]` |
| `GET kits/{slug}` | the kit and `fileList: [{path, bytes}]` |
| `POST kits/{slug}/files` | `{files: [{path, text} \| {path, data: base64}], delete?: [path], fresh?: bool}` — writes into the kit's staging folder (a copy of the live files, or empty with `fresh`). Paths are relative, plain segment names, web asset extensions only (css, js, mjs, map, json, txt, fonts, images, svg, video/audio, glb/gltf, wasm, pdf; no HTML, no server code); 15 MB per file, 2000 files and 256 MB per kit, 24 MB per request. |
| `POST kits/{slug}/commit` | `{name?, breakpoints?: {tablet, mobile}}` — the staged files replace the live ones in one swap; the version goes up. Without `breakpoints` the local-style breakpoints are suggested from the kit's stylesheets (see [Styling](#styling)). |
| `DELETE kits/{slug}` | removes the kit, its files and its override. |
| `GET kits/{slug}/override` | the kit's class and variable edits (see [Classes and variables](#classes-and-variables)): `{live: {rules}, version, draft: {rules}, draftVersion, pending, updated}` |
| `PUT kits/{slug}/override` | `{draft: {rules}, base: draftVersion}` — replaces the draft (the editor sends it on every change); a stale `base` is 409 `wb_kit_override_conflict` (with the current state in `data.override`), an override that does not validate is 422 `bad_css`, more than 256 KB 413. |
| `POST kits/{slug}/override/publish` | `{base?}` — the draft goes live on every page that loads the kit's files (the editor calls it after publishing a page of the kit). |
| `GET kits/{slug}/pages` | `{pages: [{id, title, type, html}]}`: the code-section markup of the builder pages and templates using the kit (at most 500 pages, 8 MB), for the "From this design" palette and the class counts. Who may write the kit gets every page that is not trashed; anyone else the published pages without a password and the pages they may edit themselves (never another author's draft, private or password-protected page). Reading only. |
| `PUT kits/{slug}/trust` | `{trust: bool, code?: [sha256 hex]}` → `{trust, scriptTrust: [slug], scriptCode: {slug: [sha256 hex]}}`: the **calling user** always runs (`true`) or is asked again about (`false`) this kit's scripts in the editor; with `true`, `code` adds the hashes of the page's inline code approved together with the kit (at most 512 per request; the newest 512 per kit are kept). Stored in the user's own meta `_web_builder_kit_trust` as `{slug: {i: installation id, h: hash of the kit's executable files, c: [inline code hashes]}}`; there is no user parameter. Needs `unfiltered_html` (with an Application Password the `themes` scope or full access, like every site-code write), the REST nonce under cookie authentication and the `X-WB-Api` header. 404 `wb_kit_not_found`, 400 `rest_invalid_param` without a boolean `trust` or with a `code` entry that is not a lowercase hex SHA-256, 413 `wb_too_large` past 512 hashes or 256 trusted kits; `Cache-Control: no-store`. `scriptTrust` is the user's whole list, as `Boot.scriptTrust` (sorted), and `scriptCode` the inline code approved per kit, as `Boot.scriptCode`: only kits that still exist as the installation **with the executable content** that was trusted. A kit deleted and uploaded again is a new installation, and a commit that changes one of its `.js`, `.mjs`, `.wasm` or `.svg` files changes its content: the trust is gone in both cases (for every user, the committer too); a commit of stylesheets, images or fonts only keeps it. Deleting a kit removes it from every user. |
| `POST documents/{id}/live-frame` | the editor's code sections and stylesheets `{title, shell: "theme"\|"canvas"\|"part", page, header, footer, css: {kit, local, edit}, hook}` (at most 8 MiB; a `url` older editors send is ignored) → `{url, path}`: `url` is the page's path — taken by the server from the post's permalink (a draft's `?p=` / `?page_id=` address, a template's preview address) — with `?wb-live-frame=<token>`, to be loaded as a same-origin frame. Needs `edit_post` on the document **and** `unfiltered_html` (403 otherwise). 409 `wb_live_path` when the item has no front-end address the frame can use (another host, a `.php` path, `/wp-admin`, `/wp-login.php`, `/wp-json`, `/wp-content`, `/wp-includes`, `/xmlrpc.php` and the like, a `rest_route` query); 413 `wb_too_large` when the rendered page is larger than a user may keep (24 MB). The rendered page is kept in memory for the calling user for 15 minutes and served only to them, at that path, as a frame; see [Security model](#security-model). Nothing is stored. |

## Importing

`webbuilder.ConvertCode(html, CodeImportOptions{KitURL, KitFiles, Base, LinkMap, Split})`:

- **Head**: `<title>`, meta description, robots and Open Graph tags become the page's SEO (page
  settings and Pursae SEO); stylesheets, preloads, inline styles, scripts and `theme-color` become the
  sections' `head`; site icon, canonical, alternates, manifest and `<base>` are dropped (the site's own).
- **Shell**: the importer finds the page's `main` (the first `<main>`), its site header (the first
  `<header>` before main that no `article`, `section`, `aside`, `nav`, `form` or `dialog` holds) and its
  site footer (the first such `<footer>` after main; the last one of a page without main; for a site
  import, a page without either, the block every page ends with — `CodeImportOptions.FooterAt`, see
  [Importing a site](#importing-a-site)) at any depth, and the nearest element that holds them all. The wrappers from the body down to it are the shell
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
  design's CSS expects it there) and the footer template excludes that page. A page without main whose
  footer sits in the content's wrapper (Salient: `#ajax-content-wrap` holds `.container-wrap` and
  `#footer-outer`) has the wrappers down to the footer opened: what precedes the footer in them is the
  page's content (in those wrappers), the footer and what follows it there the footer part (same
  wrappers), what follows the wrappers further footer parts. The page's **scripts after the shell**
  (WordPress's body-end scripts with the page's post id and nonces) stay on the page as **tail**
  sections (`CodePart.Tail`, `content.tail`, no wrap): `FillCode` marks the run of tail sections a page
  ends with (`CodeTailMark`) and a Hello Pursae request prints them after the footer template
  (`SplitCodeTail`, `wbThemePartsView.Tail`), where the design had them — before, they were printed
  inside the page's last wrapper, ahead of the footer. A canvas page prints them in place. A JSON-LD
  script (`application/ld+json`, the page's own structured data) in the header or footer moves to the
  page's head.
- **Document**: `<html lang>` (`Lang`), the other `<html>` attributes (`HTMLAttrs`) and the `<body>`
  attributes (`BodyAttrs`, class included) are kept on every section.
- **References**: relative and root-relative URLs in `src`, `href`, `srcset`, `poster`, `data-src`,
  `action`, `style` and `<style>` `url()` / `@import` resolve against the file's folder in the kit
  (`Base`). A kit file becomes its kit URL; a link in `LinkMap` (by the path resolved in the kit, then
  as written) becomes its site URL; other page links and missing files are reported. A query string
  stays on the mapped URL (`hakkimizda.html?tab=2#x` → `/hakkimizda/?tab=2#x`, `app.js?x=1` → the kit
  URL with `?x=1`); a kit file's `?v=` is replaced by the kit version when the page is printed.
  Any other `data-*` attribute whose value is the path of a kit file (`data-wide="../assets/film.mp4"`,
  read by the design's script) becomes its kit URL too; so does every candidate of a srcset-like list
  (`data-nectar-img-srcset="a.png 1489w, a-300x169.png 300w"`, a lazy loader's real `srcset`) of which
  one candidate names a kit file, and the string values of a JSON list or object
  (`data-slides='["a.jpg","b.jpg"]'`) that name kit files. A `data-*` value that reads as the path of a
  design file (an asset extension: images, video, audio, fonts, CSS, JS, JSON, PDF) and names no file is
  kept and reported missing; other `data-*` values are left alone (they need not be URLs). The `<source>`
  and `<track>` elements of a `<video>` or `<audio>` are rewritten too (the media element itself is not
  edited inside). `CodeImportOptions.Remap` gives files that are not in the kit a URL of their own (a
  file too large for a kit, put into the Media Library by the admin's import). The values of an `<script type="importmap">` (never its specifier
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

`web_builder_import_site {kit, pages: [{path, html}], status?, front_page?, templates?, menus?, collections?, site_name?, old_domains?, old_links?}` imports a whole
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
   **Archive addresses**: a static copy of a WordPress site keeps its archives as files
   (`author/admin/index.html`, `category/news/index.html`, `2026/09/index.html`); the router would give
   those paths to this site's author, category, tag, date, post-format, search, custom post type or
   post-permalink rules before looking for a page (`wbRouteShadowed`), so the imported page was never
   reached. The import **claims** each such path for its page (option `wb_site_claims`,
   `{path: {id, kit}}`, `internal/server/webbuilder_siteclaims.go`); `resolvePretty` serves a claimed
   path's page before the archive rules, for page 1 without a feed (`/author/admin/page/2/` and
   `/author/admin/feed/` stay the archive's), while the page is published or private and still has that
   path. The analysis lists these pages ("Pages at archive addresses"), the answer `claimed_paths` and a
   warning; a later import of the kit replaces its claims, "Remove this import" drops them, and a page
   given another address gives the path back. To show the site's real archive there instead, give the
   page another address (or bind its list to a loop, [Binding to content](#binding-to-content)).
3. **Templates**: every page's header (with what goes with it) and footer are clustered
   (`webbuilder.ClusterParts`): copies that differ only in the current page's menu link are one
   template with an [active-link rule](#active-links); copies that differ otherwise, or that the rule
   does not reproduce byte for byte, are clusters of their own. **Volatile bits** do not count
   (`internal/webbuilder/sitefooter.go`, `normVolatile`): a generated id (letters, then 8+ hex digits,
   WPBakery's `id="fws_6af66492d500e"`) that nothing else on its page names (`volatileIDs`: the value
   occurs only as an id), a cache-buster (`?ver=` / `?v=` / `?version=`) and a nonce-like token (8+ hex
   digits mixing digits and letters) in an inline script or a hidden input; the template keeps the first
   page's copy of them (nothing on the pages refers to them, and the original site's nonces mean
   nothing here). Page-specific scripts are not in the templates at all: they are the pages' tail. A
   **footer without a `<footer>` element** is found across the pages (`siteFooterHints`): from `<body>`,
   down the wrappers that hold the site header (while nothing varies after the child holding it) and
   then into the largest element after that child (the content's wrapper, not a drawer beside it), the
   first level where every page's element children end with a run of the same elements (compared as
   converted, volatile bits and classes aside) after content that differs: the run's first element is
   the footer (Decoland: `.nectar-global-section` before `#footer-outer`, inside `#ajax-content-wrap`).
   A template's **head** is what every page it serves has in its head (`CommonHead`; a page's JSON-LD
   or a stylesheet with a cache-buster of its own stay in that page's head), and a request merges the
   page's head first, so the design's order of stylesheets holds. Likewise a template's `<html>` and
   `<body>` attributes are those all its pages share (`CommonAttrs`: equal values, the classes every
   page has), so one page's classes (WordPress's `home page-id-10` on the template made from the front
   page) do not reach every page through the template; each page brings its own. The largest header and footer cluster
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

6. **The original site** (`internal/webbuilder/oldsite.go`): a saved copy of a live site still names
   the domain it was saved from — absolute links to its pages and files, and the endpoints its forms
   and scripts talk to (a plugin's inline `"ajax_url":"https:\/\/example.com\/wp-admin\/admin-ajax.php"`,
   a comment form's `action`). `webbuilder.DetectOldHosts` finds the domain: a host that on at least half of
   the pages is the absolute host of the `rel=canonical` link or `og:url`, of a WordPress endpoint or
   folder (`/wp-admin/admin-ajax.php` in a plugin's inline config, `/wp-json/`, `/wp-content/…`, also
   JSON-escaped), or of a link to one of the site's own pages other than its home (an agency's credit
   link on every page points at the agency's root and does not count); the `www.` and bare forms both;
   social, CDN and schema hosts never. Decoland, a wget copy whose canonical links are relative, is
   found by its `decolandavm.com/wp-admin/admin-ajax.php` and `wp-json` URLs, not by the
   `decoland.chainhouse.co` credit link. MCP `old_domains` overrides it (`[]` turns it off). Every URL of those hosts is then a path of this site: links, sources, srcsets,
   `data-*` values and CSS `url()` map like root-relative ones (an imported page → its URL, a kit file
   → the kit, anything else → the same path here) unless `old_links: "keep"` (admin: untick "Point links
   to the original site at this site"); a form's `action` and every occurrence of the old origin in an
   inline script or JSON (JSON-escaped too) **always** point at this site (its home URL), so by default
   nothing an imported page sends reaches the original site. A form that posted there is listed (bind it
   to a Pursae form, [Forms](#forms)). The answer's `original_site` gives the domains, the number of
   URLs, of script URLs and the forms; a kit script or stylesheet naming the original site is warned
   about (kit files are copied as they are). Third-party tracking (Google Tag Manager / Analytics, Meta
   Pixel, Hotjar, Clarity, TikTok, LinkedIn, Bing, Yandex, PixelYourSite) is kept and listed
   (`tracking`, the analysis's "Third-party tracking") so the site owner checks the ids and consent.
7. **Kit at the site's root**: the import marks its kit (`wbKit.Root`, the time of the import) and the site
   then serves the kit's files at its root as well, while a page or template of the import is published
   (a draft import serves nothing there until it is published), after everything the site answers at a
   path of its own and before the 404 (`wbKitRootAsset`, called by the front end after Pursae SEO's
   redirections, `robots.txt`, sitemaps and IndexNow key, the shop's and the forum's paths and the
   built-in `robots.txt`, `favicon.ico` and sitemaps; `robots.txt`, `favicon.ico`, `ads.txt`,
   `security.txt`, `sitemap*` and the other conventional root files are never a design's):
   `/assets/fonts/x.woff2` answers with the kit's `assets/fonts/x.woff2`. A static
   design addresses its files as its host serves its folder, and the site import maps that folder onto the
   site's paths (`a/index.html` → `/a/`), so URLs the design builds where the importer cannot see them keep
   working: root-relative `url(/assets/…)` in the kit's stylesheets, an inline module's
   `import "./js/engine.js"` on `/post/`, `fetch("data.json")`, a script reading `../assets/film.mp4` on
   `/identity/`. Only `GET` / `HEAD` of a path outside `/wp-*` that is a valid kit file (the kit's
   extensions, no HTML, no dot segments) and exists; a route of the site always wins; with several such
   kits the most recent import wins. `Cache-Control: public, no-cache`. "Remove this import" takes the
   mark away. The files are data on the site's own origin: the type comes from the extension and is
   never sniffed (`nosniff`; an unknown one is `application/octet-stream`, `.map` `application/json`,
   `.glb` `model/gltf-binary`, `.gltf` `model/gltf+json`, `.svg` `image/svg+xml`), every file but a
   script gets the uploads route's `Content-Security-Policy: sandbox; default-src 'none'; …` (an SVG or
   a JSON file opened in a tab runs nothing), and a request with `Service-Worker: script` is refused, so a
   design cannot install a service worker over the site. A response's policy applies to it only as a
   document: the design's pages load its scripts, modules, stylesheets, fonts and images the same.
   Scripts get no policy (also under `/wp-content/uploads/web-builder/kits/`): a script's own policy is
   its Web Worker's, and the sandbox would give the worker an opaque origin and no `fetch` (checked in
   Chromium); a `text/javascript` file with `nosniff` never runs as a page.

The answer lists the pages (`post_id`, `url`, sections), `pages_created` / `pages_updated`, the templates
(type, id, pages, conditions, `active_links`), `templates_disabled`, `claimed_paths`, `original_site`,
`tracking`, `unmapped_links`, `missing_files` and `warnings`. The import needs `unfiltered_html` and `edit_theme_options`; the templates show with the
Hello Pursae theme.

### Importing from the admin

**Web Builder › Import HTML site** (`/wp-admin/web-builder-import`, Turkish "HTML'den site") does the site
import without a command line or an agent (`internal/server/webbuilder_siteimport_admin.go`,
`webbuilder_siteimport_zip.go`). The menu entry and every route need `edit_theme_options` **and**
`unfiltered_html` with Web Builder on, like the MCP tool; the server checks the same on each request.

1. **Upload** a zip of the design's folder (a form field for the kit slug, default: the zip's name), up to
   512 MB. The body is streamed to a file under `uploads/private/web-builder-imports/<id>/` (the private
   folder only serves signed links) and read entry by entry (`wbZipExtract`). A path that leaves the
   folder (`..`, absolute, drive letter, backslashes counted as slashes), a symbolic link, an entry that
   is not a plain file, case-twin names and page names that cannot be imported reject the whole archive;
   `.git`, `.DS_Store`, `__MACOSX`, `Thumbs.db` and other dot files are skipped silently. `.html` / `.htm`
   files are kept aside for the importer, never go into a kit and do not count towards its limits. One
   top folder around everything is stripped. **Only the files the pages use are unpacked**
   (`webbuilder.SiteReferences`, followed transitively: the pages' URL attributes, `srcset` candidates,
   `poster`, the sources of `<video>` / `<audio>`, `data-*` values that are paths, srcset-like lists or
   JSON lists, `style` and `<style>` `url()` / `@import`, the head's links and meta images, the string
   literals of inline scripts, JSON and `<noscript>` markup; in CSS `url()` (fonts, `@font-face`) and
   `@import` relative to the stylesheet; in JS and JSON string literals naming a file — relative to the
   script, to the root, or any file whose path ends with the literal, a path a script builds from its
   theme's URL; the original domain's absolute URLs as root-relative paths). A saved copy of a live
   WordPress site brings far more than it shows; Decoland's 163 MB zip keeps 263 of 273 files. Files
   no page uses are left out and listed ("Files no page uses"), and so are used files a kit cannot hold
   (extension outside the kit's list, a name outside `wbKitRel`: the pages report them missing). A used
   file over a kit file's 15 MB that is a video, audio, image or PDF within the site's upload limit
   (`maxUploadBytes`, 64 MB) is unpacked for the **Media Library** (Decoland's 19 MB hero video): the
   analysis lists it with its future URL, the import puts it there (`insertAttachmentFile`) and passes
   it as `media` (`{"<path>": "/wp-content/uploads/2026/11/film.mp4"}`, `CodeImportOptions.Remap`) so
   every reference — a `data-nectar-video-src` too — points there, and "Remove this import" deletes it.
   That keeps a kit a set of design files of a sane size and makes large content media what WordPress
   makes of it, an item an editor can replace; a used file over that limit is left out and listed. The
   kit's own limits apply to the files kept (2000 files, 256 MB); over them the upload is refused with
   the largest used files named. Zip bombs: at most 20 000 entries, and every byte read from the archive
   is counted against 1 GB (declared sizes are only a first check). An upload, its design preview and its compare step expire a day after the upload (checked
   on every access, swept with every visit of the screen and hourly by the `wb_import_cleanup` event);
   "Discard this upload" and "Remove this import" delete it at once. The import's record outlives the
   upload: "Remove this import" works after it expired.
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
of patches `0018-code-sections` / `0019-code-blocks`, made static by `0021-mixed-frames` (see below).

**The canvas is one frame with the whole page**: the header template, the page's code sections and
the footer template that Hello Pursae draws around the page on the site (`Boot.codeParts`, read on
the server by `wbEditorCodeParts`; contract §2), in their shared wrappers, so fixed headers, sticky
footers and the shared shell look as they do on the site (an editor without `edit_theme_options` gets
their markup without their content bindings: a bound form's `to` / `cc` and redirects stay on the
server). The header and footer are locked: hovering
them shows **Edit header** / **Edit footer**, which opens that template in the editor. The device
buttons set the frame's width — desktop is a fixed 1440 px page scaled to fit the panel, tablet
768 px, mobile 390 px — so the design's own `@media` queries apply.

**Canvas modes: Live and Safe.** The canvas is the published page. In **Live** mode (bottom bar, the
default for a design you trust) the design's scripts run in the frame, so three.js / WebGL scenes,
dark-mode and menu toggles, sliders, counters, scroll effects and content that only a script draws
look and work as on the site, in the page's own shell, with its own address (`location`, relative
URLs, `fetch('data/x.json')`, module scripts and import maps behave as there). **Safe mode** is the
earlier static canvas: the design's scripts do not run, the frame is sandboxed without `allow-scripts`
and gets a neutralised copy of the markup (see [Security model](#security-model)); the stored markup
is never changed. Live and Safe are two buttons in the bottom bar (**Live** / **Safe mode**); the
bar's **Preview** (top bar) runs the scripts too, when they are allowed. A user without
`unfiltered_html` (see [Limits](#limits)) has no Live button: the canvas is in Safe mode and the bar
says **Safe mode · scripts run on the published page**.

**Trust: the first time per design.** Running a design's scripts is the author's explicit choice, asked
once per design (design kit), not once per session. What a page runs is read from its markup, never
from labels (`trust.ts` `needsOf`): the design kits it loads (every kit file address in a section's
HTML or head) and **its own inline code** — each inline `<script>`, `on…` handler, `javascript:` /
`vbscript:` URL, `srcdoc`, `<base>`, SVG animation of a handler or link and script loaded from
anywhere but a kit, hashed (SHA-256) one by one; markup a browser could read as more code than the
editor found counts as one item, the whole section. When a page that runs something opens in Safe
mode, a non-blocking prompt over the canvas asks **This design uses scripts. Run them so the editor
matches your site?** It warns that scripts run in the editor with your admin access and can do
anything you can do on the site, and offers three answers (the page stays editable under it):

| Answer | What it does |
|--------|--------------|
| **Always for this design** | Stores the trust for you and this design (see below) and switches to Live. Shown only when the page's code sections load a design kit; a page of several kits trusts all of them. |
| **This session only** | Switches to Live until the editor is closed, for exactly the kits and inline code the page had when you answered. Nothing is stored; the next session asks again. |
| **Not now (safe mode)** | Closes the prompt and stays in Safe mode. The **Live** button asks again. |

*What "Always for this design" stores.* For each kit: its slug, its **installation id**, the **hash
of its executable files** (every `.js`, `.mjs`, `.wasm` and `.svg` file of the kit, path and bytes),
and the hashes of the page's inline code you approved with it — in your own user meta
(`_web_builder_kit_trust`), on the server, not in the browser, through
`PUT web-builder/v2/kits/{slug}/trust` (see [Kits: REST](#kits-rest)); `Boot.scriptTrust` and
`Boot.scriptCode` hand it back. It is yours alone: another user is asked for themselves, and only a
user with `unfiltered_html` can give it. Opening a page whose every kit and every piece of inline code
is trusted starts in Live mode with no prompt, and the frame loads once. Your edits of text, styles,
classes and attributes keep the trust; a commit of the kit that changes only stylesheets, images or
fonts keeps it. The editor **asks again** when:

- the kit is **deleted and uploaded again** under the same name (another installation), or deleted;
- a commit changes one of the kit's `.js`, `.mjs`, `.wasm` or `.svg` files — for **every** user,
  the one who committed it too (uploading a design is never consent to run it in the editor);
- the page gains a kit or inline code nobody approved: a palette element or a section of another
  kit inserted, markup with a `<script>` or a handler pasted, **Edit code** or Section code adding or
  changing code, an undo or redo bringing old code back. The canvas drops to Safe mode in the same
  render, before the new markup reaches a frame that runs scripts, and the prompt appears; the same
  holds for a **This session only** answer.

Trust stored by an earlier version (a bare installation id per kit, before code hashes) is not
trust: after the upgrade each user is asked once more. Trust that no longer matches is never reported
to the editor and is removed from the user's list when it is next read or written; deleting a kit
removes it from every user.

*Revoke.* While a page's design is trusted, the bottom bar shows a **Stop always running this
design's scripts** button (shield with a slash). It removes the trust on the server, puts the canvas
in Safe mode at once (a **This session only** answer goes too) and says so in a notice; the next
opening asks again.

**Interact mode.** In Live mode a click on the canvas still selects an element, so the page can be
edited. The bottom bar's **Interact** button (pressed = on) sends clicks, keys and pointer movement to
the design instead: try the theme toggle, a menu, a tab, a slider. **Hold Alt (⌥ on a Mac)** to
interact for a moment without switching. Escape (outside the frame) or the button goes back to editing;
the bar says **Interacting with the page · Esc or Interact to go back to editing**, the element
toolbar and outlines rest, and the editor's keys are left to the design. Events the design's own
script sends (`el.click()`: a carousel clicking its own arrows) always reach it. What interaction
changes in the page (`html.dark`, a class a script set, `localStorage`) stays when you go back to
editing, and is never written into the document: the stored source is the only truth, saved byte for
byte as in Safe mode. A link or form the design does not handle itself and that would load another
document in the frame is stopped (the editor's page must stay); a notice shows **Links open on the
published page, not in the editor: {url}**. Links with a `#` target, and links opening a new window,
work as on the site.

**Edits without reloading.** In Live mode an edit — a text, a style or class, an attribute, moving,
duplicating, deleting or inserting an element, a tag change — is applied **to the running page**
(`applyToLive`, `elements/live.ts`): only what changed is written, matched by the editor's `data-wbc`
ids, so what the scripts did to the rest (a running animation, the drawn WebGL scene, classes a script
set, the slide that is showing) stays. A text edited in place is already shown. The canvas **reloads
the frame** (and says nothing about it) only when patching is not safe: a script was added, removed or
changed, new markup holds a script, a section was added or removed, the head, a wrapper or the
document attributes changed, an element the edit touches is not on the live page or is there more than
once, or the design's scripts have restructured the children the edit changes. A reload keeps the scroll
position and the `class`, `style` and `data-*` attributes of `<html>` and `<body>` (a dark-mode
toggle's `html.dark` or `data-theme`); `localStorage` and cookies outlive it as on any page. Every
load releases the old document's WebGL contexts first (the browser keeps only about 16 alive), so many
reloads never hit "too many active WebGL contexts". A text a script draws differently from its source
is edited as the stored text (the dialog says **Text shown by the design's script**: the script may
still change it on the page). A text edited in place in Live mode is watched while you type: if the
design's script changes it meanwhile (a counter, a re-render, a node added), nothing is saved, the
element gets back what it showed, and a notice (**The design's script changed this text while you
edited it, so it was not saved. Edit the stored text here instead.**) opens the stored text in the
text dialog. Changing the mode from Live to Safe loads the static frame again.

**When the live frame cannot be loaded** (the server refuses it, the page is too large, the site is
offline, an older server without the route), the canvas goes to **Safe mode** with the notice **The
live canvas could not be loaded ({message}). The canvas is in Safe mode.** There is no fallback that
runs the design's scripts in a `srcdoc` frame under the editor's own policy; **Live** asks the server
again.

**Geometry per device.** The device buttons size the live frame as that device's own viewport:
desktop **1440 × 900**, tablet **768 × 1024**, mobile **390 × 844**, scaled to fit the panel with a CSS
`transform` (never CSS `zoom`, which would change the design's `devicePixelRatio` and layout). So
`innerWidth`, `innerHeight`, `100vh` / `100dvh` heroes and the design's `@media` queries are the
device's, as on a phone or laptop, and a click or a hover through the scaled frame lands on the right
element. A Safe-mode frame is as before: a fixed 1440 px page scaled to fit, tablet 768 px, mobile
390 px.

**Embedded content.** In Live mode an `iframe`, `object` or `embed` (a YouTube or Vimeo player, a map)
loads and plays as on the site. In Safe mode it does not: the element stays (its tag, classes,
attributes and styles, so its box and the layout around it are the published page's), loads nothing,
and is drawn as a neutral placeholder with **Embedded content plays on the published page. Publish and
open the page to watch it.** A click on the placeholder selects the element, so its source is still
changed in the Content tab (`elements/embeds.ts`). A `<video>` or `<audio>` file stays a native player.
An element the design hides (a closed menu, the second slide) can be selected in the navigator and
**shown temporarily** (toolbar or context menu); that is editor-only, like **Hide in the editor**.

**Selecting and editing.** Every control is drawn over the frame, outside the design's DOM (the
frame only gets the `data-wbc` ids and one `<style data-wb-edit>`):

- a click selects the innermost element (a header or footer element: nothing); the outline shows its
  type and name (Section, Flexbox, Grid, Div, Heading, Text, Link, Button, Image, Icon, Video / embed,
  List, Menu, Slider, 3D / Canvas … see the table under the navigator), derived from the source and
  the computed `display`, never stored; the breadcrumb in the bottom bar shows its path. In Interact
  mode a click goes to the design instead;
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

**Live typing** (`elements/livefield.ts`). Text, link label, alt text and attribute fields of the
Content tab and the Basic panel change the canvas **while you type**: 250 ms after the last keystroke
the draft is written through the same element operation as a commit, so in Live mode it is patched
into the running page without a reload. A typing session is **one history entry** however long the
pauses (the store merges `field:` keys, patch `0024-basic-seam`); Enter or leaving the field ends it,
**Escape** takes it back. A value the rules refuse (an unsafe address) is skipped while typing and
refused with the reason when the field is left. Fields whose half-typed value would load something
(an image, video or poster source) are written on Enter or blur only. The writes go through the
builder's history like any other edit, so a typed change that adds code to the page is checked by the
trust rules at once ([Trust](#editing)).

The **navigator** (Studio › Structure, or **Layers** in the bottom bar) lists each code section's
elements with an icon and a readable **name**, the same name wherever the element is shown (navigator,
canvas outline and breadcrumb, Basic panel). The name is, in order: your own rename; for a top-level
block of a section its first heading, then its landmark (Header, Menu, Footer …), `aria-label`, id or
class as words, at worst **Section N**; for any other element its `aria-label`, `alt`, text, heading,
placeholder, title, file name, id or class as words, and, when nothing is found, its type. Types are
derived from the tag, `role`, the class names and the computed `display`, never stored:

| Type | From |
|------|------|
| Section, Flexbox, Grid, Div | top-level element, or `display` |
| Heading, Text, Link, Button, Image, Icon, List | `h1`–`h6`, phrasing text, `a`, `button`, `img`, small `svg`, `ul` / `ol` / `dl` |
| **3D / Canvas** | `canvas` |
| **Menu**, **Menu items** | `nav` or `role="navigation"`; the list inside it |
| **Header**, **Footer**, **Main content**, **Sidebar**, **Article**, **Figure**, **Table** | `header` / `banner`, `footer` / `contentinfo`, `main`, `aside`, `article`, `figure`, `table` |
| **Form**, **Field** | `form`; `input`, `select`, `textarea`, `label`, `fieldset` (submit inputs are Buttons) |
| **Video / embed**, **Map**, **Audio** | `video`, `embed`, `object`, `iframe` (a map address is a Map); `audio` |
| **Slider**, **Tabs**, **Cards**, **Hero**, **Badge**, **Logo**, **Graphic** | common class names (`swiper`, `carousel`, `tablist`, `hero`, `badge`, `logo` …), three or more alike children with media or a heading and text, a large `svg` |
| **Accordion**, **Popup**, **Quote**, **Divider** | `details`; `dialog`, `aria-modal`; `blockquote`; `hr` |
| Code | `script`, `style`, `template`, `noscript`, `link`, `meta` (listed last and collapsed) |

A set of repeated children shows its count (**Cards · 6**). Each parent shows its first 60 rows;
**Show N more** adds the rest. Rows are dragged to reorder or reparent elements (also across sections),
renamed with a double-click — the names are stored in the section's `content.names` (a JSON map keyed by
`data-wbc`), so the design's markup stays clean — hidden in the editor and collapsed.

**Basic mode: blocks, not HTML.** In Basic mode a code section of an element-editor page is a **named
block** (patch `0024-basic-seam`: the seam's `Basic` panel and `summary` row, `elements/BasicCode.tsx`,
`fields.ts`). The section list shows a readable title (its first heading, or **Hero**, **Features**,
**Pricing**, **Testimonials**, **FAQ**, **Contact**, **Menu**, **Footer** … recognised from its markup)
with a line of counts (**3 headings · 2 buttons · 4 images**); a section with nothing to name keeps its
own name. Opening it shows **On this section**: the fields found in its stored markup, in document order
— headings, texts, button and link labels with their address (and a page picker for the site's own
pages and posts), images (replace from the media library, alternative text), form field placeholders
and the source of a video or embed. Repeated siblings (cards, list items, slides, table rows: same tag
and classes, at least twice) are grouped (**Card 1**, **Card 2** …) and a run of three or more alike
links or buttons becomes **Links** / **Buttons**. Empty and hidden elements, scripts, styles and SVG
internals are left out. The panel shows 60 fields at a time (**Show N more**, at most 600 per section)
and offers **Switch to Studio** for more control. The HTML code and Head code fields **do not appear in
Basic mode**; they are in Studio, and the palette's **HTML / Embed** element is not offered in Basic
mode either. Each row of the section list shows a small **thumbnail** of the section (`SectionThumb.tsx`,
`thumb.ts`): built only once the row is near the viewport, drawn from the neutralised copy with the
design's stylesheets in a frame sandboxed without `allow-scripts` and under a `script-src 'none'`
policy, so nothing of the design runs in it, and cached by markup. The section settings of a code
section in Basic mode leave out **background, background decoration and motion**: a code section is
published as its own markup and those settings would have no effect on it. On a **mixed page** (code
and builder sections) a code section in Basic mode shows **This part of the design is its own code,
which Simple mode does not show. To change it, switch to Studio.** with a **Switch to Studio** button
instead of the HTML fields. Edits splice the stored source like every other element operation
(one history entry each). For a user without `unfiltered_html` the fields are shown **read-only**: the
server would drop the edit anyway (see [Limits](#limits)).

**Studio: blocks, and the code when you want it** (Studio › Content, `elements/ContentPanel.tsx`,
`CodeEditor.tsx`, `codeops.ts`). Besides the structured controls above, the selected element has an
**Edit code** group with two code boxes: **Whole element** (its own markup, outer HTML) and, for an
element with an inside, **HTML inside**. Both show the code as stored, without the editor's `data-wbc`
ids (kept for you: an element the new code still has at the same place keeps its id, a new one gets a
fresh id). The box is a small code editor (colouring, line numbers, Tab / Shift+Tab, find with Ctrl/⌘F,
a larger-editor dialog). **Apply** (Ctrl/⌘+Enter) checks the code and splices it at the element's range,
as one history entry (**Edit code**, **HTML changed**); **Revert** goes back to the stored code.
Validation refuses code with an unclosed or unopened tag, an unfinished tag (a quote or `>` missing) or
a closing tag that would change an element outside the one being edited, and says on which line
(`Line 4: <div> is never closed.`); empty code offers **Delete element** instead. What is typed is raw
code by a user who may write code sections: the Attributes list's rules (no `on…`, no `javascript:`)
do not apply, and the server's publish filter is the boundary, as for Section code. An unapplied draft
is kept while the page stays open (**A draft you had not applied was restored.**, **Discard draft**) and
leaving the element with changes unapplied says so. Without a selected element the tab shows the
section's HTML and head code (**Section code**) as before. For a user without `unfiltered_html` Edit
code and Section code are read-only.

Every operation splices the source markup of the section(s) it changes at the element's ranges (a
small source-preserving parser, `elements/html.ts`), so nothing outside the edited element changes
byte for byte (whitespace, quoting, entities), and writes them back as **one** history entry with its
own label. A page opened, edited and edited back publishes exactly what it published before. A
duplicate or a pasted element gets new `data-wbc` ids, and its HTML ids become `name-2`, `name-3`, …
(free on the page) with the `aria-controls`, `aria-labelledby`, `for` and `href="#…"` references
inside the copy following them, so the design's accordions, tabs and labels work on both; the inside
of an `svg` is copied as is.

**Mixed pages** (patches `0018` / `0019`, `0021`): a run of consecutive code sections is drawn as one
frame (`CodeSection.tsx`, its document from `codeFrameDoc.ts`); the builder's own sections between
the runs stay React components of the editor's page. The frame follows the element editor's static
Safe mode through the host's **frame guard** (`elements/guard.ts`, installed with the element editor's
seam): it is sandboxed without `allow-scripts`, its head and body are the neutralised copy, embeds are
placeholders, and its DOM is swept once loaded. A click selects an element (also an embed placeholder)
and edits its text in place — written back from the source, so the handlers, scripts and ids inside
the text stay as stored — the toolbar selects the parent, moves the element among its siblings,
duplicates and deletes it, **Replace** changes an image, and **Style** sets inline styles (text color,
background, font size and weight, alignment, padding, margin, gap, corner radius, width, max width,
opacity), classes and the raw `style` attribute; **Layers** shows the run's elements. Attributes the
toolbar writes are neutralised in the frame too. Structural changes reload the frame. **Preview**
runs the design's scripts only when they are allowed, with the same state as the element editor
(`state.ts` `scripts`): at once for a design the user trusts, else after the per-session confirmation
(**Run this design's scripts?**, not remembered); until then each run shows the page without its
scripts and the banner.
There is no Live mode on a mixed page (plan decision: mixed pages are out of scope of the live canvas).

Mixed pages are not routed to the element editor's canvas: its frame holds code sections only, while
the builder's sections are React components with the builder's own selection, inline editing and
stylesheet in the editor's page; drawing them in the design's frame would mean a static copy of them
(the published markup) without the builder's editing.

In both, the section list reorders, hides, duplicates and removes code sections, and the inspector
shows the section's HTML and head code for anything the canvas does not reach.

### Security model

**Trust model.** An administrator who imports a design trusts its scripts. The import itself treats
the upload as hostile (nothing of it runs while it is read, its preview is sandboxed, kit files are
served with a sandbox policy), but once imported the design's JavaScript is the site's own code, like
a script an `unfiltered_html` user publishes: it runs on the published pages with the CSP nonce, and
the editor's **Live** mode and **Preview** run it, once the user has chosen to (per design, remembered
per user), in the editor's origin with that user's access (their REST nonce, so whatever they may do).
Import only designs you would publish yourself; the site-wide summary is the
[trust model](operations/security.md#trust-model).

The editor runs on the site's own origin and holds the author's REST nonce
(`window.__WEB_BUILDER__`), so anything of the design that runs in a same-origin frame can act as the
author. A code section is trusted when it is **published** (its author has `unfiltered_html`), but
opening a page in the editor must not run a design the author has not chosen to run — an imported
design may come from anywhere. The element editor therefore has three rules
(`builder/host/elements/ElementsCanvas.tsx`, `trust.ts`, `ScriptsGate.tsx`, `frame.ts`, `neutral.ts`,
`richtext.ts`):

1. **Safe mode runs nothing.** The frame is `sandbox="allow-same-origin"` without
   `allow-scripts`: no script, handler, `javascript:` URL or nested document in it can run, whatever
   the markup holds or however a parser reads it (nested frames inherit the sandbox). The editor does
   all its work from outside — its listeners on the frame's document (selection, hover, drag and drop,
   keys forwarded), measuring (`getBoundingClientRect`, `getComputedStyle`, the CSSOM read by the
   Style panel), in-place text editing (`contenteditable`, the frame's own `execCommand`), the editor
   stylesheets — which a sandbox without scripts allows. The sandbox and the document always change
   together (a new frame element when they do). Safe mode is also the **only** mode of a user without
   `unfiltered_html`, and the mode every page opens in until its design is trusted.
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
3. **Running the design's scripts is the author's explicit choice, per design, remembered per user.**
   The prompt (**This design uses scripts. Run them so the editor matches your site?**) names the risk:
   the scripts run with the author's access, as on the published page. **Always for this design** is
   stored on the server in the user's own meta, bound to the kit's installation **and the hash of its
   executable files** (a kit deleted and uploaded again, or a commit changing its `.js`, `.mjs`,
   `.wasm` or `.svg` files, is asked about again, by every user), with the hashes of the page's inline
   code approved with it; revocable at any time, and only a user with `unfiltered_html` can give it.
   **This session only** is never stored and covers what the page ran when it was given. The page's
   needs are read again from the markup on every change of its code sections (an insert, a paste,
   Edit code, undo): new or changed inline code, or a kit not covered, drops the canvas (and the
   mixed pages' Preview frames) to Safe mode in the same render, before the markup reaches a frame
   that runs scripts, and asks again. Without an answer the canvas stays in Safe mode, which keeps
   rules 1 and 2. This replaces the earlier rule that asked every editor session and stored
   nothing. An opaque-origin frame was considered for Live and Preview: it breaks the editing itself
   (the editor could not reach the frame's DOM), the design's same-site fonts and module scripts (CORS
   from an origin of `null`), and its `localStorage` / cookies, so the author's choice is the boundary
   instead. What the trust does **not** do: the design's scripts never write into the document (the
   source is the only truth, and every save is written from it), and a trusted kit stays out of the
   hands of a user who may not publish scripts — their editor never runs it.

**The live frame route** (`internal/server/webbuilder_liveframe.go`). The Live canvas must be the
published page, so it is not a `srcdoc` document (location `about:srcdoc`, the editor's CSP, relative
URLs needing a `<base>`) but a same-origin document at the page's own address:

1. `POST web-builder/v2/documents/{id}/live-frame` (see [Kits: REST](#kits-rest)) sends the editor's
   code sections (the unsaved page, the locked header and footer parts) and its three stylesheets. The
   server renders them as the front end prints the page — `FillCode` per part, the shared shell, the
   parts' `<html>` / `<body>` attributes and active links, the heads merged page first, the tail after
   the footer, Hello Pursae's or the canvas page's `<main>` — keeps the editor's `data-wbc` ids, and
   keeps the document in memory under a random token (18 random bytes, hex). It writes nothing to the
   database. Only a user with `unfiltered_html` who may edit the post gets a frame. **The frame's path
   is the server's**: the post's permalink (a draft's `?p=` / `?page_id=` address, a template's preview
   address); a `url` in the request is ignored, and a path of the site's own machinery (`/wp-admin`,
   `/wp-login.php`, `/wp-json`, `/wp-content`, `/wp-includes`, `/xmlrpc.php`, any `.php` path, a
   `rest_route` query) or of another host is refused (409 `wb_live_path`).
2. The frame loads `<the page's path>?wb-live-frame=<token>`. A middleware answers it before any route,
   and only if **all** hold: the token exists and is under **15 minutes** old; the requester is the user
   who made it and still has `unfiltered_html`; the path is the page's path; it is a `GET` or `HEAD`;
   and the request is a frame of this origin (`Sec-Fetch-Dest: iframe`, `Sec-Fetch-Site: same-origin`
   when the browser sends them). Otherwise it answers 404 ("This live frame has expired. Reload the
   editor's canvas."). Frames are kept in memory only, at most **64 MB** and 64 frames in all and
   **24 MB** and 6 frames per user (the oldest go first); a page larger than 24 MB on its own is
   refused with 413 `wb_too_large`. A restart expires them and the canvas asks for a new one.
3. The response carries the **front end's** Content Security Policy for that path, with the origins the
   design names (`wbCodeCSP`, `wbEmbedCSP`) and, when stored administrator scripts get the nonce
   (Settings › Security `csp_admin_scripts`), the nonce on every script, as the published page gives the
   code sections' scripts (`cspTrustScripts`); `Cache-Control: no-store, private`,
   `X-Robots-Tag: noindex` and `Referrer-Policy: no-referrer` (the token in the frame's address is not
   sent on with the design's requests). Under the enforced policy with `csp_admin_scripts` off the design's scripts
   are blocked here exactly as they are on the published page.
4. The frame element has the sandbox `allow-scripts allow-same-origin allow-forms allow-popups
   allow-popups-to-escape-sandbox allow-modals allow-downloads allow-pointer-lock allow-presentation` and
   **never** `allow-top-navigation`, so the design cannot navigate the editor's page; the editor
   additionally stops a link or form the design does not handle itself (`guardLive`). The frame's
   first head script (editor-owned, removed at once) records WebGL contexts and takes the token out of
   `location` before the design runs.

**No fallback.** Every failure of the route — missing on an older server (`rest_no_route`), 403, 409,
413, offline — leaves the canvas in Safe mode with a notice. The design's scripts never run in a
`srcdoc` frame under the editor's own origin, policy and nonce.

The **editor page's own policy** adds, for a user with `unfiltered_html`, only what its own frames
need (`wbEditorFrameCSP`, `internal/server/webbuilder_editor.go`). A page of **code sections only**
(its frames are static: Safe mode and the thumbnails run nothing) gets the style and font origins of
the sections' markup, of their head and of the kits it loads, and `blob:` images and media — no
script, connect or frame origins. A **mixed page** (code and builder sections: its Preview runs the
design's scripts, after the author's confirmation, in `srcdoc` frames that inherit this policy) also
gets the `script-src`, `connect-src` and `frame-src` origins the published page allows for the same
markup (`wbCodeCSP`). The Live canvas and its Preview carry the front end's policy of the live frame.

**Users without `unfiltered_html`** (authors, contributors, editors on a network): **Safe mode only,
read-only markup.** `Boot.canRunScripts` is `false`, the Live button, the prompt and the trust button
are not shown (the bar says **Safe mode · scripts run on the published page**), the live-frame route
answers 403 and the trust route 403. The canvas's element operations (move, duplicate, delete, text
edit, insert), Basic fields, Edit code and Section code are read-only for them, with **Your role may not
change this part of the design.** — the server would drop such an edit anyway
([Permissions](#limits)); they still reorder, hide and remove sections.

The **Content panel** never puts design markup into the editor's page (the text box is built from
the source tree, see above), and text edits are written back from the source (`richtext.writeBack`).
Tests: `test/host/elements-neutral.test.ts` (every payload of the review and a mutation-XSS list,
jsdom) and the browser regression `node builder/test/security/editor-xss.mjs` (Playwright Chromium,
not part of `make builder-test`: run `npx playwright install chromium` once in `builder/`), which
draws all payloads with the editor's own modules under the report and the enforced CSP and edits
them through the reconciler; it fails when one reads the nonce, and checks that the unprotected
control does. The trust route and the live-frame route are covered by `webbuilder_trust_test.go` and
`webbuilder_liveframe_test.go`; the live frame against the published page by
`builder/test/live/live-frame.mjs` ([Testing the editor](#testing-the-editor)).

The per-run frames of mixed pages follow rules 1 to 3 as before (patch `0021-mixed-frames`, the frame
guard `elements/guard.ts`; see Editing): they are Safe-mode frames, and their **Preview** runs the
scripts only for a trusted design or after the per-session confirmation (**Run this design's scripts?**,
not remembered). `html` sections are drawn in a frame with `allow-scripts`
but without `allow-same-origin` (an opaque origin that cannot reach the editor's page or its nonce).
Tests: `test/host/elements-embeds.test.ts` (placeholders, the mixed frame's document with every
payload, write-back, neutralised toolbar attributes) and the mixed-page scenarios of
`editor-xss.mjs` (two runs around a builder hero section: edit mode and Preview before the
confirmation read nothing, under the report and the enforced policy, before and after an edit).

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
| `field` | any element (inside a loop's first card: that card's post; elsewhere the post a single template shows) | `title` (as stored, no texturizing), `excerpt` (`words`), `content` (imported markup is printed as stored; block content goes through the_content), `image` (the featured image's `src` and `alt` — `srcset: true` adds `srcset`, `sizes`, `width`, `height` of `size` —, else the URL field `name`), `url` (`href`), `link` (`href` and the title as its text), `date` / `modified` (`format`), `author`, `terms` (`tax`, `sep`; names as text, `links: true` term links), `custom` (`name`: a Pursae Custom Fields field, else — for the names a site import stores, `design_<n>`, `list<n>_<k>` and their `_href` / `_src` / `_alt` / … companions (`webbuilder.SiteFieldName`) — the post meta value of that name; any other meta is never printed, so a loop cannot show a plugin's or a user's unregistered meta. The compiled element carries `data-wb-meta`, which only bindings set (the snapshot sanitizer drops it from an author's markup, whose `post.field` slots read registered fields only); `attr` "" text, `html` sanitized like post content, `url` href / src, `attrs` the attributes from `<name>_href`, `<name>_src`, `<name>_alt`, … plus the text from `<name>` with `text: true`), `pagination` (the main loop's page links). An element with icons beside its text keeps them: only its own text is replaced. |
| `form` | a `<form>` | posts to the form endpoint (below). |

Editor: select an element and open **Advanced** in the inspector: **Content binding** binds it (the
kinds the element can take: a list or container → Menu or Loop, a list inside a menu item → Submenu, an
element of a loop's first card or of a template → Field, a `<form>` → Form) and shows what the site
will list (the menu location's items, the loop query's posts). The frame keeps showing the design's
own markup. MCP: `web_builder_bind {post_id, element | html_id, bind | null}` (publishes the page;
its result has a `warning` when a `custom` or `image` field name is neither a registered Pursae Custom
Fields field nor a site import's field, since such a binding prints nothing).

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
  qualifies when its pages share their sections, wrappers and head (their JSON-LD aside, cache-busters
  and nonces normalized), have one varying `<h1>`, and besides the title and the content at most eight
  regions differ (≤ 20 % of a page). What a saved WordPress site printed per page and nothing on this
  site uses is not a difference: the original record numbers in `id`, `class` and `data-*` values and a
  hidden input (`id="post-1699"`, `class="post-1699"`, `comment_post_ID`), an inline script's code (its
  post id, a nonce) and the page's tail scripts when they load the same files — the template keeps the
  first page's (the check compares the pages that way). Pages whose side menus come
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

  **Pretty-permalink posts** (`webbuilder.SiteLeafSets`): a WordPress copy saves every post as
  `<slug>/index.html`, alone in its folder, at the site's top beside the pages (`blog/index.html`,
  `hakkimizda/index.html`), so no folder holds them. The leaf pages of each parent folder (`*` at the
  top, `<parent>/*` below) are a candidate set too, split by kind when their `<body>` classes (those
  without digits) differ, each kind named by a class only its pages have (`*:single-post`, Decoland's six
  posts; `*:page-template-default` its pages, which do not share a skeleton and stay pages). With the
  `/%postname%/` permalink structure the top-level sets become **posts** (`SiteBindOptions.PostSets`,
  `wbSitePostSets`): the pages are made posts (same address), the largest variant is the
  **single-post** template of every post (`include/singular/post`, so a post written later is drawn by
  it), and a list of them is a loop of posts, newest first (`postType: post`, `orderBy: date desc`; the
  import dates them a second apart in the list's order, so the site's earlier posts come after them
  and a later post first). Otherwise a set is grouped as pages (each page's own condition) and its
  lists stay markup (a loop of pages selects by parent page, which the set's pages do not share). A
  second import finds the posts again (`_wb_site_key`), makes them pages and decides anew; "Remove this
  import" removes them as it removes pages. The admin lists such a set as `*/index.html (single-post)`.
- **Listings**: a list on another page whose element children each link to a different page of a
  collection folder (`haberler.html`'s news cards) becomes a loop over the folder's pages: query
  `postType: page`, `parent` = the folder's parent page, `orderBy: menu_order asc`, `perPage` = the
  number of cards, `offset`. The cards are aligned like pages: the link to the page is `url` (`link` when
  it holds the title), a text equal to the page's `<h1>` is `title` (the pages' titles become their
  `<h1>`), the longest other text (40+ characters) the `excerpt` (set on the page), a varying image the
  `image` field (no featured image yet: its URL in the field `list<n>_<k>`), the rest custom fields
  `list<n>_<k>`. The longest list sets the pages' `menu_order`; another list is bound when it shows a
  window of that order (`offset`); the longest list that can be a loop sets the order (an author archive
  whose cards differ in structure does not). A new page under the folder shows in the lists by its `menu_order`
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
- Collections are detected among the pages of one folder, and among the pretty-permalink pages
  (`<slug>/index.html`) of one parent folder; other pages in the site's root are not grouped. Posts are
  made only under the `/%postname%/` structure, from the site's top.
- A group's template shows the first page's values of what the original site printed per page (its
  record numbers in ids and classes, nonces, its post id in inline scripts) and loses the pages' JSON-LD
  (Pursae SEO prints the posts' own structured data).
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
stored option is validated again when it is read (an override that no longer passes is left out and
logged), and `KitOverrideCSS` never prints a selector or context the parser refuses, so an option
written by anything else cannot close the `<style>` element. The
override is kept in `wb_kit_overrides` beside the kit, so a new upload of the kit keeps it; deleting
the kit drops it. Go's `KitOverrideCSS` and the editor's `overrideCSS` print the same bytes (shared
fixtures `internal/webbuilder/testdata/override`).

## Limits

- The element editor draws only header and footer templates made of code sections; a header made
  of builder sections is not shown around a code page in the editor (it is on the site).
- `::before` / `::after` are not styled yet (neither locally nor in a class edit). Style edits on mixed
  pages stay inline styles (patch `0019`), and their frames do not show local styles or class edits.
  Mixed pages have no Live mode: content only a design's script draws shows in Preview (for a trusted
  design, else after the confirmation) and on the published page.
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
  an `li`, and elements the design's scripts reveal (`.reveal`, scroll animations) stay hidden in
  Safe mode until Live mode or the published page runs the script. Thumbnails are drawn without
  the design's scripts.
- The palette's Image starts with an inline placeholder and the Video without a source: set them in
  the Content tab. Form fields keep the design's own form handling (none for a field outside a form).
- Editing a text rewrites the element's inner markup (only when the text changed): elements inside
  keep their stored tags, text the edit did not touch keeps its spelling, but changed text is written
  with the editor's escaping (`&amp;`, `&lt;`, `&gt;`; other entities as characters), and formatting
  the browser adds that is not on the allowlist (a `style` on a span, a `<font>`) is dropped. An
  element holding scripts or styles is not edited in place.
- Safe mode shows what the neutralised copy shows (Live mode shows the design itself): embedded content (`iframe`, `object`,
  `embed`: a YouTube or Vimeo player, a map) is a placeholder of the same box that does not play — it
  plays on the published page; declarative shadow roots (`<template shadowrootmode>`) and
  `<noscript>` content are not drawn; `data:` URLs other than images, fonts, audio and video (and SVG
  images outside `img`, `image`, `source`, `video`, `input`, `link`) are blocked. An `object` or
  `embed` without a `width` / `height` has no size of its own in the editor.
- **Where Live mode still differs from the published page.** The Live frame is the page as the front
  end prints it for the markup in the editor, but:
  - `wp_head` and `wp_footer` additions of the site (plugin scripts and styles, analytics, the cookie
    banner, the admin bar, site-wide CSS from other plugins) are not in the frame; a design that depends
    on one of them needs it on the published page.
  - **Bound content is shown as stored.** A bound element (a menu, a loop, a post field, a form's
    settings, [Binding to content](#binding-to-content)) shows the design's own sample markup, not the
    content the published page fills in. A form the design does not handle itself is not submitted (the
    frame stops it and shows a notice).
  - The frame is the device's viewport (1440 × 900, 768 × 1024, 390 × 844) scaled by a transform, but its
    `devicePixelRatio` and the browser's user-agent, touch input and pointer type are those of the
    editor's window, not a phone's: a design that reads them (a canvas sized by the ratio, a
    `(hover: none)` query) may differ from a visitor's device.
  - The token is taken out of the frame's address before the design runs, so `location.href`,
    `location.search` and relative URLs are the page's. Links and forms the design does not
    handle itself are stopped with a notice, but a script that navigates the frame itself
    (`location.reload()`, `location.href = …`) is not: the frame then shows the published page (or the
    target) instead of the editor's frame until the canvas loads a new one (a reload it needs, or a
    mode change).
  - Under the enforced Content Security Policy the frame has the front end's policy, which has neither
    `'unsafe-eval'` nor `'wasm-unsafe-eval'`: `WebAssembly` modules the published page's policy blocks
    are blocked in the Live frame too. The editor page's own policy allows `'unsafe-eval'`, so the
    mixed-page frames, which inherit it, are more permissive than the site. With `csp_admin_scripts` off the design's scripts are blocked on both.
  - Edits are patched into the running page without running the design's scripts again, so a script
    that read the DOM once at load (a counted list, a slider that cloned its slides) does not see an
    edit until the frame reloads; the canvas reloads by itself when it finds a script's copy of an
    element or restructured children, and otherwise leaves the script's state alone. A reload keeps the scroll position and `<html>` / `<body>`
    `class`, `style` and `data-*`; state a script keeps in variables is lost.
  - **Kits and trust.** A page of designs the user does not trust opens in Safe mode with a prompt
    (only when its markup holds scripts); a page with no design kit (code sections without a kit) can be
    run for the session only. Trust belongs to the design kit's installation and executable content
    (and the inline code approved with it), never to a page or a site. A commit that changes a kit's
    scripts asks every user again, the one who committed it too.
- **Basic mode** shows at most 600 fields per section and 60 at a time (**Show N more**); a section with
  nothing the panel recognises says **Nothing to edit here yet.** and points to Studio. The fields are
  those found in the stored markup: text a script draws is not a field.
- Under the enforced nonce policy the frames of mixed pages inherit the editor's policy, so a design's scripts without the nonce do not run there (they do on the published page); the
  Live canvas frame carries the front end's policy and the nonce the published page gives.

- A code section is published as written: its markup is trusted (the author has `unfiltered_html`).
  Kits are site-wide code: only administrators should hold them.
- **Permissions.** Users without `unfiltered_html` (authors, contributors, editors on a network)
  can open and save a page or template with code sections, but cannot change them: on autosave,
  publish and revision restore the server takes each code or html section the editor sends by id
  from the stored doc (the published doc, then the user's own draft), with its stored content, at
  the position the editor gives it, and keeps the stored `style.css` of every section
  (`webbuilder.DocOptions.Stored`). They can reorder, hide and remove code sections; their edits of
  a code section's markup or head, an html section or custom CSS are ignored and reported as
  `stripped`, and new code sections from them are dropped. The element editor's canvas does not offer them these edits at all: the element operations, Basic fields,
  Edit code and Section code are read-only for them (`Boot.canRunScripts` false). Their publish fills the snapshot with
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
- A design whose used files are over the kit limits (256 MB, 2000 files; files no page uses do not
  count, the admin's import leaves them out) or with more than 500 pages (landing/new-landing: 1110
  pages, 198 MB with documentation exports) is imported in parts (several kits) or trimmed first. The
  MCP harness uploads the folder as it is (`pursae_mcp.py kit-upload`); the admin's zip import keeps
  only the used files.
- An app shell whose page is drawn by a JavaScript bundle from an API or a build step (a Vite `dist/`
  served under a base path, a dashboard reading a backend) is not a static site; it can be imported, but
  only the empty shell is a code section to edit.
- A site import needs every page in one call (at most 500); the active-link rule matches request paths,
  so it needs pretty permalinks. A design's form keeps its own handling (enerjiosgb's opens `mailto:`)
  until it is bound to the form endpoint ([Binding to content](#binding-to-content)).
- Bindings: see [Binding to content › Limits](#binding-limits).

## Testing the editor

The editor's live canvas has two browser harnesses beside the vitest suite (`make builder-test`, which
runs the host tests of `builder/test/host/elements-*.test.ts`, the UI string lint and the dist audit).
Both need Playwright's Chromium (`cd builder && npm ci && npx playwright install chromium`, or a binary
of your own named in `PW_CHROMIUM`, a Chromium or headless-shell path, the same variable for all of
them) and a running Pursae with an administrator who has `unfiltered_html`; neither is part of
`make builder-test`.

- **Editor ↔ published parity** — `builder/test/editor-parity/` (`parity.mjs`, the fixture design with a
  WebGL scene, a theme toggle, a slider and script-drawn content). It imports the fixture as a code
  page, publishes it and compares the published page with the editor's canvas in Safe mode, Live
  (**This session only**) and **Always for this design** at 1440 / 768 / 390 px, pixel by pixel and
  with functional checks (scripts ran, WebGL drawn, toggle and slider through **Interact** mode, state
  kept after interacting, trust remembered and revoked). Exit 1 when a Live shot is over `--target` %
  (default 0.5) or a check differs.

  ```sh
  cd builder
  PW_CHROMIUM=/path/to/chromium node test/editor-parity/parity.mjs --site http://127.0.0.1:8191 \
    --user admin --pass '…' [--setup] [--csp keep|report|enforce|off] [--modes static,live,always]
  ```

  Options, the checks and a throwaway site for it (own database and port) are in
  [`builder/test/editor-parity/README.md`](https://github.com/pursae/pursae/blob/main/builder/test/editor-parity/README.md).
  The report (`report.json`, `report.html`) goes to `builder/.work/editor-parity`; `make builder` deletes
  `builder/.work`, so copy it first.
- **Live frame** — `builder/test/live/live-frame.mjs`: against a site with a published code page (for
  instance the parity fixture's), it mounts a live frame with the canvas's own modules and checks the
  published page against the frame at scale 1 and scaled, `applyToLive` (a text and a class edit patched
  without a reload, the WebGL canvas keeps its pixels), `reloadPreserving` (scroll and theme kept) and 24
  reloads without a "too many active WebGL contexts" warning.

  ```sh
  cd builder
  PW_CHROMIUM=/path/to/chromium node test/live/live-frame.mjs --site http://127.0.0.1:8193 \
    --user admin --pass '…' --post 4 [--out .work/live-frame] [--generic]
  ```

  `--post` is the post id of the page; `--generic` checks any design (pixels and structure only).
  `LIVE_SITE` and `LIVE_POST` can replace `--site` and `--post`.
- **Editor security** — `builder/test/security/editor-xss.mjs` (see [Security model](#security-model)).
- **Server** — `go test ./internal/server/ -run 'WBKitTrust|WBLive|WBEditorCodeCSP'` (trust route and Boot fields, the
  live-frame route and its protections; they need the Postgres test database like the other server tests).
