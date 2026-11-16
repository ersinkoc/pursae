# Web Builder sections — authoring cheat sheet

Always read `web_builder_catalog` on the target site: designs and fields come from the site's own
builder version. This page shows the shapes.

## A section spec

```jsonc
{
  "kind": "hero",
  "design": "split",                       // optional; ids from web_builder_catalog
  "name": "Welcome",                        // optional label in the editor's navigator
  "content": { "eyebrow": "…", "title": "…", "body": "…", "primary": "Book now", "primaryHref": "/contact" },
  "items": [ { "title": "…", "text": "…", "icon": "shield", "href": "…" } ],
  "style": { "palette": "dark", "align": "center", "padding": 112, "titleSize": 64 },
  "bg": { "type": "image", "image": "https://…", "overlay": 0.5 },
  "hidden": false
}
```

- Text: plain, or the rich subset `<b> <i> <u> <s> <code> <a href> <br> <ul>/<ol>/<li>`.
- `palette`: `brand` (brand color background), `dark`, `light`, `soft` (gray), `sand`.
- Site look (same on every page): `{"brand": "#0e7c66", "fontPair": "modern", "corners": "round"}`;
  `fontPair` elegant | modern | plain; `corners` sharp | soft | round. Optional `headingFont`, `bodyFont`.
- Images: absolute https URLs or Media Library URLs (`upload_media {filename, source_url}` → `source_url`).
- A design may add extra content keys (`extras` in the catalog, e.g. `stat1`, `image2`, `logos`).
  Unset keys keep the design's sample text — the build answer warns about them.

## Kinds at a glance

| kind | content keys (main) | items |
|------|---------------------|-------|
| nav | logo, brand, primary (+ design extras: search, cart, login, phone…) | menu links {title, href} |
| hero | eyebrow, title, accent, body, primary, secondary, image | small list (badges/points) |
| banner | text, link (+ badge, countdown) | — |
| features | eyebrow, title, body | cards {title, text, icon} |
| media | eyebrow, title, body, primary, image, imageSide | — |
| steps | eyebrow, title | steps {title, text} |
| quote | quote, author, role | — |
| faq | eyebrow, title | questions {title, text} |
| cta | title, body, primary, secondary | — |
| footer | logo, brand, note (+ columns, contact, social per design) | links {title, href} |
| text | title, body | — |
| image | image, caption, video | — |
| form | eyebrow, title, body, primary (+ labels per design) | — (submissions go to the builder's form inbox) |
| layout | type flex/grid, structure (50-50, 33-66, 2x2, g3c…) | cells {title: heading/text/button/image/layout, text} |
| custom | — | free node tree {node: box/heading/text/button/link/image/icon/input/divider, parent, title, tag, lay} |

## Edit operations (`web_builder_edit_page`)

```jsonc
{"post_id": 12, "ops": [
  {"op": "update", "section_id": "s_ab12", "content": {"title": "New headline"}, "style": {"palette": "brand"}},
  {"op": "update", "section_id": "s_cd34", "item_updates": [{"id": "i_x1", "text": "Shorter text"}]},
  {"op": "update", "section_id": "s_ab12", "design": "centered"},
  {"op": "add", "after": "s_ab12", "section": {"kind": "quote", "content": {"quote": "…", "author": "…", "role": "…"}}},
  {"op": "move", "section_id": "s_ef56", "index": 0},
  {"op": "duplicate", "section_id": "s_cd34"},
  {"op": "remove", "section_id": "s_zz99"}
]}
```

`status: "draft"` saves into the editor draft only (the live page stays); `web_builder_publish` puts it live.
