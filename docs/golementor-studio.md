# Golementor Studio — plan and contracts

Golementor Studio turns Golementor into a site-building app: a fresh, simple editing panel on top
of the existing builder engine, a setup wizard that installs a complete starter site (header,
footer, home, blog list, single post, page, contact, search, 404) as editable Golementor documents,
and a large library of ready sections. Nothing of the existing builder is removed: the full
upstream panel stays one click away ("Advanced") for detailed editing.

## 1. Studio panel (replaces the upstream panel's UI)

- Our own UI (plain JavaScript modules + CSS under `web/gowebeditor/studio/`, no build step, no CDN)
  mounted in the editor shell in place of the upstream left panel. It drives the engine only through
  the editor's public APIs (`$e.run('document/elements/create' | 'settings' | 'delete' | 'move' …)`,
  `elementor.getPreviewContainer()`, `elementor.documents`, `$e.internal`, the selection channel)
  so saving, history, undo/redo, revisions, responsive modes and the canvas keep working.
- Look: WordPress-like, square, quiet: white/neutral surfaces, 1px borders, 2–4px radius, the
  WordPress blue accent, system font, compact spacing; dark mode follows the existing Golementor
  dark palette.
- Layout:
  - **Top bar**: Golementor logo menu (Exit to dashboard, Site Setup, Theme Builder, Settings),
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

`/wp-admin/golementor-setup` (also linked from the Golementor menu and offered right after
activation): 1) choose a starter site (6 kits, with screenshots and live preview), 2) pick a color
palette and a font pairing (presets + custom), 3) site name/logo and which pages to create, 4)
install. It installs, as Golementor documents bound to Hello Golementor: theme parts (header,
footer, single post, single page, archive/blog list, search results, 404) with display conditions,
pages (Home, About, Services or equivalent, Blog, Contact) with the contact form, menus (primary,
footer), Settings › Reading (static front page + posts page), and the kit's global colors and fonts.
Everything created is tagged so **Remove starter site** cleans it up and restores the previous
reading settings. Re-running the wizard offers to replace or keep the current starter.

## 3. Content formats

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

- `content` is upstream element JSON (containers + classic widgets), ids omitted or any value
  (fresh ids are assigned on insert). Colors and typography use the kit's global references
  (`__globals__`: `globals/colors?id=primary|secondary|text|accent`, `globals/typography?id=…`) so
  a section adopts the site's palette and fonts.
- Categories: `hero`, `features`, `about`, `services`, `cta`, `testimonials`, `pricing`, `team`,
  `faq`, `stats`, `gallery`, `logos`, `blog`, `contact`, `newsletter`, `content`.
- Thumbnail: `<slug>.webp` next to the JSON, 600px wide, a real render of the section with the
  default kit.
- Text is original English; images use the shared placeholder set in
  `web/gowebeditor/library/images/` (generated artwork) referenced as
  `{{library:images/<file>}}`, replaced with the asset URL on insert.

### Site kits — `web/gowebeditor/library/kits/<slug>/kit.json`

```json
{
  "title": "Northwind Business",
  "category": "business",
  "description": "…",
  "screenshot": "screenshot.webp",
  "globals": { "colors": [ {"_id":"primary","title":"Primary","color":"#1d4ed8"}, … ],
               "typography": [ {"_id":"primary","title":"Headings", "typography_font_family":"Inter", …}, … ] },
  "parts": [ { "type": "header", "title": "Header", "conditions": ["include/general"], "content": [ … ] },
             { "type": "footer", … }, { "type": "single-post", … }, { "type": "single-page", … },
             { "type": "archive", … }, { "type": "search-results", … }, { "type": "error-404", … } ],
  "pages": [ { "slug": "home", "title": "Home", "front_page": true, "content": [ … ] },
             { "slug": "blog", "title": "Blog", "posts_page": true },
             { "slug": "about", … }, { "slug": "contact", … } ],
  "menus": [ { "location": "primary", "name": "Main menu", "items": [ {"title":"Home","page":"home"}, … ] } ],
  "posts": [ { "title": "…", "content": "<!-- wp:paragraph -->…", "image": "{{library:images/…}}" } ]
}
```

- Parts use the theme widgets Golementor already has (site logo, nav menu, post title, post
  content, featured image, post info, archive posts/loop, search form, copyright/text) so they are
  dynamic. A few sample posts make the blog list and single post look real.
- `{{page:slug}}` and `{{library:…}}` tokens work in any string.
- 6 kits: Business/consulting, Restaurant/café, Portfolio/creative, Blog/magazine, Local
  service/shop, Personal/creator — each with its own palette, fonts and page designs, built
  mostly from the section library plus page-specific blocks.

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
