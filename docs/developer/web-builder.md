---
title: Pursae Web Builder
description: The built-in visual page builder — how editing and publishing work, where the data lives, the web-builder/v2 REST API, how the editor is built, and what it does not do yet.
weight: 890
section: built-in-plugins
---

Pursae Web Builder is a section-based visual page builder that ships inside the binary as a
built-in plugin (slug `golementor`, option `native_golementor`). The editor is a React app built
from [`github.com/pursae/Builder`](https://github.com/pursae/Builder); visitors never load React:
a published page is a static HTML/CSS snapshot with a small optional script. The field-by-field
specification is the [integration contract](web-builder-contract.md); this page is the overview.

## For site owners

### Switching it on

Like Classic Editor and WPGo Cache it is listed on Plugins and is **off on a fresh install**.
Plugins › Pursae Web Builder › **Activate** turns it on; nothing else changes (no theme switch, no
setup wizard). While it is off there are no menu entries, "Edit with" links, routes, assets or
front-end output, and built pages show their readable fallback inside the theme.

**Settings › Web Builder** (`manage_options`) picks the post types the builder edits — pages and
posts by default — and lists the pages whose published markup came from an older renderer
("Regenerate pages"; each one is refreshed by opening it in the builder and publishing again).

### Editing flow

1. Open a page with **Edit with Pursae Web Builder**: the button in the block editor's toolbar or
   under the classic editor's title (a new or changed post is saved first), the row action on the
   Pages/Posts list, **Add New** followed by that button, or the toolbar node on the site. A built
   page is marked "Pursae Web Builder" in the list. The editor URL is
   `/wp-admin/post?post={id}&action=web-builder`; it needs `edit_post`, refuses trashed posts and
   takes the WordPress post lock (another user's live lock is shown, and can be taken over).
2. Build the page from sections — navigation, hero, banner, features, media, steps, quote, FAQ,
   call to action, footer, text, image, form, layout and custom — or a saved section from the
   library, and switch between desktop, tablet and mobile views. Images come from the Media
   Library (uploads go through `/wp/v2/media`); Google Fonts are downloaded once and served from
   the site.
3. Changes **autosave** every few seconds as your own draft. The live page is untouched; reopening
   the editor offers the draft when it is newer than what is published.
4. **Publish** renders the page in the browser, sends the document plus the snapshot to the
   server, and the server validates and sanitizes both. Users who may not publish get **Pending
   Review** instead. If someone else published in the meantime the editor shows a conflict instead
   of overwriting it.
5. Every publish is also a core revision, and the server keeps the builder document of each one
   (REST `revisions`; restoring one writes it as your draft to review and publish). The editor
   has no revisions panel yet.
6. **Preview** (`?preview=true`) shows the last published snapshot; when your draft is newer a notice
   says so.

Editing a built page somewhere else (block or classic editor, Quick Edit, REST) **detaches** it: the
site then shows that edited content inside the theme, and the builder warns "Edited outside the
builder" with two choices — publish again from the builder (discarding the outside edits) or leave
the page to the other editor.

The editor UI is English; Turkish is included, picked from the user's language. Other languages
follow later.

### What visitors get

A published page is a canvas document: no theme header, footer or stylesheet; SEO, analytics,
cookie-consent and plugin heads are kept. The page CSS is inlined up to 24 KiB (a cached file
above that), fonts are local `@font-face`, images go through the core image filters (sizes,
`srcset`, lazy loading). The runtime (`runtime.<hash>.js`, under 8 KB gzipped) is loaded only when
the page uses menus, FAQ/accordions, tabs, dismissible bars, forms, motion or embeds, and the
content is readable with JavaScript off. Builder pages are cached by WPGo Cache and purged on
publish.

Forms post to the site itself (`web-builder/v2/forms/{page}/{section}`): the fields come from the
stored page, not the request; a honeypot, a minimum fill time, a per-IP rate limit and the Pursae
Contact Form captcha (when one is configured) filter spam, and the message goes to the site's admin
e-mail through the core mailer.

Everywhere else — REST `content.rendered`, Pursae Headless, feeds, search, sitemaps, exports, and
the theme when the plugin is off or the page is detached — the page is the readable fallback HTML
the server generates from the document into `posts.content`.

### Limits

Not in the builder (yet):

- **AI** — the upstream AI assistant, AI images and site cloning are stubbed out of the build; no
  AI request leaves the site. The design for a server-side proxy is in the contract (§9).
- **Theme builder** (headers, footers, archive and single templates), **popups**, **loops** and
  dynamic content.
- **Form integrations** (mailing lists, webhooks, CRMs), stored form submissions and **payments**.
- **Pursae Commerce widgets** (product grids, cart, checkout); store pages use the store theme.
- **Global classes and variables**, **kits** and site-wide design settings; colours and fonts are
  set per page.
- A setup wizard, a template library beyond saved sections, and a revisions panel in the editor.

These come back as later features. The previous builder and its data were removed in the release
that introduced this one ([upgrading](getting-started/upgrading.md)).

## For developers

### Code map

| Path | What |
|------|------|
| `internal/webbuilder/` | server-independent pieces: contract types (`contract.go`), doc validation, HTML and CSS sanitizers, fallback HTML, forms, fonts |
| `internal/store/webbuilder.go` | storage (docs, drafts, revisions, library) |
| `internal/server/webbuilder_*.go` | plugin switch and settings, admin entry points, editor screen, assets, REST, front-end render, forms, hooks (drift, copies, export, cache), redirects of old URLs, one-time upgrade and cleanup |
| `builder/` | Node workspace: `upstream/` (git submodule, pinned by `UPSTREAM_SHA`), `patches/`, `host/` (CMS adapter), `vite/`, `scripts/`, `test/` |
| `web/web-builder/` | the committed build output, embedded in the binary |

### Data model

The page **document** (`doc`, JSON) is the source of truth; everything the browser renders from it
is untrusted. Migration `0027_web_builder.sql` adds:

- `web_builder_docs` — one row per built post: `doc`, `page` settings, the sanitized snapshot
  (`html`, `css`, `fonts`, `features`), `renderer`, the publish counter `rev`, `content_hash` of the
  fallback written to `posts.content`, and `detached`.
- `web_builder_drafts` — per-user autosaves (`post_id`, `user_id`, `base_rev`).
- `web_builder_revisions` — the doc of each publish, keyed by the core `post_revisions` row it was
  created with.
- `web_builder_library` — saved sections.

The post itself only gets the marker meta `_web_builder = "1"`. Page settings map to the post:
title, slug, excerpt, and Pursae SEO description and noindex. WXR export carries the doc
(`_web_builder_doc`, `_web_builder_page`), import validates it again; Pursae Multi Language's
"Duplicate content" copies doc and snapshot to the translation.

### Publish = static snapshot

The editor calls the upstream live renderer (`renderStatic`, from `builder/patches/0002`) and
compiles the Tailwind classes it used. The server then, in one transaction: checks the API version
header, `edit_post`, the post lock and the revision (`baseRev`, else 409); validates the doc
(kinds, sizes, depth, URL schemes); strips html sections and custom CSS for users without
`unfiltered_html`; sanitizes the snapshot HTML (allowlist, one `div.wb-page` root, `data-wb-*`
attributes, restricted iframes) and the CSS (no `@import`, `expression()`, unsafe `url()`); writes
the fallback HTML to `posts.content` (a core revision); upserts the docs row; and purges the page
cache. The response lists what was stripped.

### REST: `web-builder/v2`

Cookie + `X-WP-Nonce` (or an Application Password), plus `X-WB-Api: 1` on every editor request
(a mismatch answers 409 so a stale editor reloads). Routes exist only while the plugin is on.

| Route | Purpose |
|-------|---------|
| `GET documents/{id}` | doc, page settings, your newer draft, lock, detached flag |
| `PUT` / `DELETE documents/{id}/autosave` | your draft |
| `POST documents/{id}/publish` | publish (validate, sanitize, store) |
| `POST documents/{id}/lock` | take or refresh the edit lock |
| `GET documents/{id}/revisions[/{rid}]`, `POST …/{rid}/restore` | revisions |
| `GET` / `POST library`, `DELETE library/{id}` | saved sections |
| `GET` / `PUT prefs` | the user's editor preferences |
| `GET fonts/css2` | Google Fonts css2 query answered with local files |
| `POST forms/{id}/{section}` | public form endpoint |

Payloads, status codes and limits: [contract §3](web-builder-contract.md#3-rest-namespace-web-builderv2).

### Building the editor

The dist in `web/web-builder/` is committed, so `go build` and the Docker image need no Node.
Rebuild it after changing `builder/`:

```sh
make builder          # submodule at UPSTREAM_SHA → patched copy in builder/.work → vite build → manifest, BUILD.json, fixtures, audit
make builder-watch    # rebuild the editor on change (with `make dev`, which reads web/ live)
make builder-test     # vitest (host), UI string lint, audit of the dist
```

Node 22+ (`builder/.nvmrc`). Commit `web/web-builder/` together with the source change.

**Patches.** The upstream code is never edited in place. `builder/patches/*.patch` are applied in
name order with `git apply --3way` to a copy of the submodule; a patch that no longer applies fails
the build loudly.

- `0001-host-seam` — the `BuilderHost` interface: load, autosave, publish, media, fonts, exit and
  site URLs go through the host (`builder/host/cms-host.ts`); upstream still runs standalone.
- `0002-live-render` — `src/builder/live/`: `renderStatic(doc)` and the vanilla `data-wb-*`
  runtime.
- `0004-i18n` — `t()` with English sources and the Turkish catalog
  ([contract §2.1](web-builder-contract.md#21-editor-ui-language)).

(`0003`, the AI seam, is reserved for when AI comes back.) Change a patch by editing the patched
copy, regenerating the diff against the pinned upstream and rebuilding.

**Bumping upstream.** `make builder-bump SHA=<commit>` moves the submodule and
`builder/UPSTREAM_SHA` and rebuilds; fix any patch that stops applying, then commit the pin, the
patches and `web/web-builder/` together. `BUILD.json` records the upstream SHA, the patch hashes
and the API and renderer versions; a Go test checks it against the repository. An incompatible
change to the static markup or the `data-wb-*` attributes bumps `RendererVersion` (pages then show
up under Regenerate pages; additive attributes do not); an incompatible payload change bumps
`APIVersion`.

`builder/test/audit.mjs` fails the build when an AI provider URL or key header shows up in the
bundles, and enforces the size budgets.

### License

The editor is built from `github.com/pursae/Builder`, which has **no LICENSE file yet**. Before the
builder is merged to `main` and shipped, that repository needs a GPL-compatible license (the repo
owner adds it); until then the integration lives on the `web-builder` branch.
