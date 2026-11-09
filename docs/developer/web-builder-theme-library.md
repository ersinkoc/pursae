---
title: Theme library in Pursae Web Builder
description: Import a ready-made full-site theme (header, footer, every page template, demo pages and menus) from the editor's Items panel, and make a library theme importable.
weight: 892
section: built-in-plugins
---

The **theme library** offers the themes of the theme market (the same packages as Appearance › Add Themes ›
Library, served by Pursae Manager) as ready-made designs for [Pursae Web Builder](web-builder.md). Open the
editor, switch to **Studio**, then **Items › Components › Browse theme library**. The market itself is
unchanged: the same theme can still be installed as a classic package theme.

## Importing a theme

There are two ways in, with the same gallery and the same import:

- **Web Builder › Theme Library** in the admin menu (needs `edit_theme_options`). The screen embeds the
  gallery in a frame (`themes?page=web-builder-library`; the frame document is `&frame=1`). The frame loads
  the editor bundle in library mode (`window.__WEB_BUILDER_LIBRARY__`, `builder/host/library-main.tsx`)
  because the browser draws and publishes every imported template (contract §4); the bundle's page reset
  is why it lives in a frame of its own. Links in the result leave the frame (`<base target="_top">`).
- the **Browse theme library** card in the editor's Items panel.

Pick a theme, read what it contains and press **Import theme**. One import creates:

- the Theme Builder [templates](web-builder-theme-builder.md): Header, Footer, Front Page, Single Post, Single
  Page, Archive, Search Results and 404 Page, each with display conditions;
- the theme's demo pages (for example "Our promise" and "Contact"), published, plus their images in the Media
  Library;
- the theme's menus, assigned to the Primary and Footer menu locations of Hello Pursae.

When the site already has published templates of the same types, the dialog asks:

| Choice | Result |
|--------|--------|
| **Replace them** | the theme's templates go live; yours are *disabled* (drafts, never deleted) and stay in the Theme Builder |
| **Keep both** | the theme's templates are saved as drafts; nothing changes on the site until you publish them (no menus) |

Your posts, pages, categories, users and settings are never touched. Importing the same theme again is
idempotent: it finds its own templates and pages (post meta `_wb_theme`, `_wb_theme_key`) and updates them
instead of making copies; images already in the Media Library are reused. Shop pages of Pursae Commerce
(product, cart, checkout) use the store's own views unless a Single Product or Product Archive template
exists (the library themes do not ship one yet); the theme designs the header, footer and the pages around
them.

Needs `edit_theme_options`, plus `edit_pages` and `publish_pages` for the demo pages.

## How an import runs

The front-end snapshot of a design is drawn by the editor in the browser (contract §4), so an import is a short
conversation:

1. `POST /web-builder/v2/themes/{slug}/import` `{mode: "replace" | "keep", pages: bool}`: the server imports the
   demo images and creates (or updates) the templates and pages as **drafts** with their docs. It answers
   `{mode, templates: [{id, key, type, title, rev}], pages: […], media}`. A failure trashes what the call
   created.
2. The editor fetches each doc, draws it and publishes it through the ordinary publish route (`status:
   publish`; templates are skipped in *keep* mode).
3. `POST /web-builder/v2/themes/{slug}/finish` `{mode, templates: [ids], pages: [ids]}`: only posts of this
   theme that are published count. In *replace* mode the other published templates of the same types are set
   to draft; the demo menus are created and assigned; the template index and Pursae Cache are purged
   (`wbTemplatesChanged`).

Until step 3 the old templates stay live, so a stopped import changes nothing visible; running it again resumes.
`GET /web-builder/v2/themes` lists the cards (`slug, name, description, screenshot, templates, pages, menus,
images, store, imported`) and `existing`, the number of published templates per type. All three routes need
the `X-WB-Api` handshake and the nonce like the other routes.

## Making a library theme importable

Add `"web_builder": "web-builder/parts.json"` to the package's `theme.json` and ship that file
(format `web-builder-theme`, version 1):

```json
{
  "format": "web-builder-theme", "version": 1,
  "templates": [
    {"key": "header", "type": "header", "title": "Header", "conditions": ["include/general"], "priority": 0, "doc": {"site": {}, "sections": []}}
  ],
  "pages": [
    {"key": "about", "slug": "about", "title": "Our promise", "doc": {}, "page": {"layout": "site"}}
  ]
}
```

- `doc` is a builder `DocState` (contract §8.1), validated like a publish. Strings `{{media:<id>}}` are replaced
  by the URL of the demo image with that id (`demo/demo.json` › `media`).
- Menus and images come from the package's `demo/demo.json` (`menus` for locations `primary` / `footer`, `media`):
  the market's demo import and the builder import share them.
- `type` is any template type except `single`; `key` is `a-z0-9-` and unique; conditions follow contract §11.3.
- Bind the header and footer to the menus (`menu: "primary"`, `logoSource: "site"`) and use the dynamic widgets
  and loops for content, so one theme fits any site.
- The three store themes (Kite & Crayon, Good Boy Supply, Fernwood) are generated with the editor's own
  factories: `cd builder && npm run gen:themes` rewrites `library/themes/<slug>/web-builder/parts.json`.
  Bump the theme's `version` when you change the file.

The package validator checks the manifest key (a `.json` file under `web-builder/`); the parts file is checked
when the theme is listed in the editor, and a theme whose file is invalid shows its error on its card.
