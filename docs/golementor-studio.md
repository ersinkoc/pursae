# Pursae Web Builder Studio — plan and contracts

Pursae Web Builder Studio turns Pursae Web Builder into a site-building app: a fresh, simple editing panel on top
of the existing builder engine, a setup wizard that installs a complete starter site (header,
footer, home, blog list, single post, page, contact, search, 404) as editable Pursae Web Builder documents,
and a large library of ready sections. Nothing of the existing builder is removed: the full
upstream panel stays one click away ("Advanced") for detailed editing.

## 1. Studio panel (replaces the upstream panel's UI)

- Our own UI (plain JavaScript modules + CSS under `web/gowebeditor/studio/`, no build step, no CDN)
  mounted in the editor shell in place of the upstream left panel. It drives the engine only through
  the editor's public APIs (`$e.run('document/elements/create' | 'settings' | 'delete' | 'move' …)`,
  `elementor.getPreviewContainer()`, `elementor.documents`, `$e.internal`, the selection channel)
  so saving, history, undo/redo, revisions, responsive modes and the canvas keep working.
- Look: WordPress-like, square, quiet: white/neutral surfaces, 1px borders, 2–4px radius, the
  WordPress blue accent, system font, compact spacing; dark mode follows the existing Pursae Web Builder
  dark palette.
- Layout:
  - **Top bar**: Pursae Web Builder logo menu (Exit to dashboard, Site Setup, Theme Builder, Settings),
    page switcher (Pages panel), device switcher, undo/redo, preview, Save/Publish.
  - **Left rail** with four tabs:
    1. **Add** — "Sections" (the section library: categories, thumbnails, search, click or drag to
       insert after the selected section) and "Elements" (≈14 basics: Heading, Text, Image, Button,
       Icon, Icon list, Video, Divider, Spacer, Columns/Container, Gallery, Form, Map, Posts).
    2. **Edit** — the inspector for the selected element: a small set of plain-language fields
       (content first, then Style: color pickers bound to global colors, size S/M/L/XL presets,
       alignment, spacing presets, background, rounded corners, visibility per device) and an
       **Advanced settings** button that opens the full upstream panel for that element.
    3. **Layers** — outline of the page (select, rename, reorder by drag, hide, delete, duplicate).
    4. **Site** — global colors and fonts (the kit), logo and site name, header/footer/templates of
       the site (open in the builder), pages list.
  - **On canvas**: hovering a section shows a small square toolbar (move up/down, duplicate,
    delete, save as template) and a "+ Add section" divider between sections that opens the section
    gallery in place.
- Advanced mode: a toggle in the top bar shows the original upstream panel (all features); the
  choice is remembered per user.

## 2. Setup wizard

`/wp-admin/golementor-setup` (Pursae Web Builder › Site Setup, `edit_theme_options`; offered on the Plugins
screen right after activating Pursae Web Builder while no starter site is installed): 1) choose a starter
site (kit cards with screenshot and **Live Preview**), 2) pick a color palette (the kit's, 8 presets
or 4 custom colors) and a font pairing (the kit's or 7 presets: heading font → global fonts
`primary`/`secondary`, body font → `text`/`accent`), 3) site title, tagline, logo (media library) and
which pages to create, 4) install. Server side: `internal/server/golementor_setup.go`.

- Live Preview (`/wp-admin/golementor-setup/preview?kit=<slug>&page=<page slug>|blog|post-<n>&palette=…&fonts=…`)
  renders the kit's header, page and footer with the chosen colors and fonts without installing
  anything (menus link between preview pages, the blog and single post use the kit's sample posts).
- Install creates, as Pursae Web Builder documents of the Hello Pursae Web Builder working copy: the theme parts
  (Theme Builder storage, with their display conditions), the pages (built with Pursae Web Builder, template
  `elementor_header_footer` unless the kit says otherwise; the posts page is a plain page), the menus
  (assigned to `primary`/`footer`), the sample posts (featured images imported into the media
  library, categories created when missing), Settings › Reading (static front page + posts page),
  title and tagline, the kit's global colors and fonts (system ids from the kit, extra ids added as
  custom globals, `settings` merged) and the logo (Site Settings `site_logo`) in the active kit
  document; then Hello Pursae Web Builder is activated. The previous working copy's theme parts and the
  untouched install samples ("Hello world!", "Sample Page") go to the trash.
- Everything created carries the meta `_gwe_starter=<kit slug>` (pages and posts also
  `_gwe_starter_ref=<slug in kit.json>`); the option `golementor_starter` records the previous
  reading settings, title, tagline, kit Site Settings, theme and menu locations, the menus made and
  the posts trashed. **Remove starter site** deletes exactly the tagged posts, pages, parts, media and
  categories and the menus, and restores all of that.
- Re-running the wizard offers **Replace** (remove, then install the chosen kit) or **Keep** (keep
  the content; apply only colors, fonts, title, tagline and logo).

## 3. Content formats

Loaded, validated and cached by `internal/server/golementor_library.go`; an invalid file is left
out of the library and logged with every problem (`go test -run TestGolementorLibraryContent
./internal/server/` lists them). JSON is read strictly: unknown keys are errors. Files are served at
`/wp-content/plugins/gowebeditor/assets/library/…`.

### Section templates — `web/gowebeditor/library/sections/<category>/<slug>.json`

```json
{
  "title": "Hero with image on the right",
  "category": "hero",
  "tags": ["image", "two columns", "cta"],
  "thumbnail": "<slug>.webp",
  "content": [ { "elType": "container", "settings": { … }, "elements": [ … ] } ]
}
```

- `<slug>`: `a-z`, `0-9` and single dashes. `category` must equal the directory name. `thumbnail`
  must be exactly `<slug>.webp` and the file must exist next to the JSON.
- `content` is upstream element JSON (containers + classic widgets), ids omitted or any value
  (fresh ids are assigned on insert, key order is kept). Top-level elements must be containers
  (`section`/`column` are accepted too); every `widgetType` must be one the renderer knows. Colors
  and typography use the kit's global references (`__globals__`:
  `globals/colors?id=primary|secondary|text|accent`, `globals/typography?id=primary|secondary|text|accent`;
  other ids are errors in a section) so a section adopts the site's palette and fonts.
- Categories: `hero`, `features`, `about`, `services`, `cta`, `testimonials`, `pricing`, `team`,
  `faq`, `stats`, `gallery`, `logos`, `blog`, `contact`, `newsletter`, `content` (listed in this
  order; items within a category by slug).
- Thumbnail: `<slug>.webp` next to the JSON, 600px wide, a real render of the section with the
  default kit.
- Text is original English; images use the shared placeholder set in
  `web/gowebeditor/library/images/` (generated artwork) referenced as
  `{{library:images/<file>}}` (the file must exist), replaced with the asset URL on insert.

### Site kits — `web/gowebeditor/library/kits/<slug>/kit.json`

```json
{
  "title": "Northwind Business",
  "category": "business",
  "description": "…",
  "screenshot": "screenshot.webp",
  "options": { "blogdescription": "…" },
  "settings": { "body_background_color": "#FFFFFF" },
  "globals": { "colors": [ {"_id":"primary","title":"Primary","color":"#1d4ed8"}, … ],
               "typography": [ {"_id":"primary","title":"Headings", "typography_font_family":"Inter", …}, … ] },
  "parts": [ { "type": "header", "title": "Header", "conditions": ["include/general"], "content": [ … ] },
             { "type": "footer", … }, { "type": "single-post", … }, { "type": "single-page", … },
             { "type": "archive", … }, { "type": "search-results", … }, { "type": "error-404", … } ],
  "pages": [ { "slug": "home", "title": "Home", "front_page": true, "content": [ … ] },
             { "slug": "blog", "title": "Blog", "posts_page": true },
             { "slug": "about", … }, { "slug": "contact", … },
             { "slug": "faq", "title": "FAQ", "optional": true, "content": [ … ] } ],
  "menus": [ { "location": "primary", "name": "Main menu", "items": [ {"title":"Home","page":"home"}, … ] } ],
  "posts": [ { "title": "…", "content": "<!-- wp:paragraph -->…", "image": "{{library:images/…}}" } ]
}
```

- `screenshot`: a file name next to kit.json (required). `category`, `title` required.
- `globals`: `colors` (`_id`, `title`, hex `color`) and `typography` (`_id`, `title`,
  `typography_font_family` from Google Fonts or a system font, plus any `typography_*` keys) must
  each contain `primary`, `secondary`, `text` and `accent`; further ids become custom globals that
  the kit's own content may reference.
- `parts`: `type` is one of the seven above; a `header` and a `footer` are required; `conditions` are
  Theme Builder conditions (`include|exclude/` + `general`, `singular`, `singular/front_page`,
  `singular/post`, `singular/page`, `singular/not_found404`, `archive`, `archive/post_archive`,
  `archive/category`, `archive/post_tag`, `archive/author`, `archive/date`, `archive/search`, or
  `singular/page/{{page_id:<slug>}}`); optional `settings` (document settings). Pages use the
  template `elementor_header_footer` by default, so the `single-page` part only shows on pages with
  `"template": "default"`; exclude the front page if a kit gives pages that template
  (`exclude/singular/front_page`).
- `pages`: unique slugs; at most one `front_page` (always created) and one `posts_page` (no
  `content`: it shows the `archive` part); optional `template` (`default`,
  `elementor_header_footer`, `elementor_canvas`), `settings` (page settings) and `optional: true`
  (unchecked in the wizard). Put the contact form (the `form` widget) on the contact page.
- `menus`: `location` `primary` or `footer`; items have `page` (a kit slug; title defaults to the
  page title) or `url` + `title`, and optional `children`. Items of pages the user did not create
  are skipped.
- `posts`: `title`, block `content`, optional `slug`, `excerpt`, `categories` (names) and `image`
  (exactly one `{{library:images/…}}` token, webp/jpeg/png; imported into the media library).
- `options`: only `blogname` / `blogdescription`, used when the wizard field is left empty.
  `settings`: extra kit Site Settings (not globals, logo or site name).
- Parts use the theme widgets Pursae Web Builder already has (site logo, nav menu, post title, post
  content, featured image, post info, archive posts/loop, search form, copyright/text) so they are
  dynamic. A few sample posts make the blog list and single post look real.
- Tokens work in any string: `{{library:<path>}}` (asset URL), `{{page:<slug>}}` (the page's
  permalink; `#` when that page is not created — and in sections when no starter site is
  installed), `{{page_id:<slug>}}` (its id, `0` when missing). Anything else in `{{…:…}}` is an error.
- Canonical field names are the ones above. The loader also accepts these aliases (used by the
  first kits): `site: {name, tagline}` for `options.blogname/blogdescription` (`site` wins),
  `globals.settings` for kit style settings (merged before `settings`; `__globals__` maps are
  merged key by key), `pages[].settings.template` for `pages[].template` (moved to the page template,
  not kept as a page setting), `posts[].category` (one name) next to `categories`; kit-level `tags`
  (strings) are listed by the kits endpoint.
- Hello Pursae Web Builder lays out a Theme Builder part that fills the main area full width (`<main
  class="site-main gwe-main-part">`, no 1140px cap or padding); parts set their own widths with
  boxed containers, so kits need no `.site-main` override.
- Icons: only the Font Awesome 5 names (`fas fa-…`, `far fa-…`, `fab fa-…`) render.
- 6 kits: Business/consulting, Restaurant/café, Portfolio/creative, Blog/magazine, Local
  service/shop, Personal/creator — each with its own palette, fonts and page designs, built
  mostly from the section library plus page-specific blocks.

### REST and classic library

Editors only (`edit_posts`; cookie + `X-WP-Nonce` like `elementor/v1`; 404 while Pursae Web Builder is off):

- `GET /wp-json/golementor/v1/library/sections` →
  `{"categories":[{"slug","title","count"}],"items":[{"id":"<category>/<slug>","title","category","tags","thumbnail":"<url>"}]}`
  (only non-empty categories).
- `GET /wp-json/golementor/v1/library/sections/<category>/<slug>` → `{"content":[…]}`: tokens
  replaced, fresh element ids, ready for `document/elements/import` or `create`.
- `GET /wp-json/golementor/v1/library/kits` →
  `{"kits":[{"slug","title","category","description","screenshot":"<url>","pages":[{"slug","title","front_page","posts_page","optional"}],"parts":["header",…],"colors":[{"_id","title","color"}],"fonts":[{"_id","title","family"}],"posts":<n>}]}`.
- The classic template library's **Blocks** tab lists every section (`source: remote`, `type: block`,
  `template_id: "gwe-section:<category>/<slug>"`, `subtype`: the category title) and inserts it
  through `get_template_data` like the homepage designs.

## 4. Work split

1. Studio panel (UI, inspector, section gallery UI, canvas toolbar, advanced toggle).
2. Wizard + library server side (listing, thumbnails, insert endpoint with token replacement,
   kit install/remove, globals) + docs.
3. Sections 1–30 (hero, features, about, services, cta, testimonials, pricing, team).
4. Sections 31–60 (faq, stats, gallery, logos, blog, contact, newsletter, content) + shared
   placeholder images.
5. Site kits 1–3. 6. Site kits 4–6.

Each part lands on its own branch with tests (Go httptest for the server side; JSON validation
of every section/kit through the real element-tree loader; a headless render check of every
section and kit page) and is merged and verified in the browser at the end.
