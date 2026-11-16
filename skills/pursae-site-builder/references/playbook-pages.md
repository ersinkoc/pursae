# Playbook — one page, a redesign, or HTML import

**Add a page** (`prompt add_page page=Pricing`): read the front page with `web_builder_get_page` to
copy its site look and design family → `web_builder_catalog` → `web_builder_build_page` → link it with
`menu_build` (`keep_items: true`) → `seo_update_meta`.

**Redesign a page**: `web_builder_get_page {post_id}` → `web_builder_edit_page` with `update`
(`design`, `content`, `style`) per section, `add`/`remove`/`move` for structure. Use `status: "draft"`
when the owner wants to review first; they open `edit_url`, then you (or they) publish.

**HTML import, 1:1** (`prompt import_html_page title=…`): check the design's SEO first — `<title>`,
`<meta name="description">`, Open Graph tags, `lang`; one `h1` and headings in order; `alt` on every
image. Then, for a design kept as its own code (the default, `method: "code"`, needs an administrator):

1. `web_builder_kit_upload {kit: "mydesign", files: [{path, text | data}]}` — every CSS, JS, font and image
   file with its folder path (harness: `pursae_mcp.py kit-upload ./design mydesign`).
2. `web_builder_import_html {html, kit: "mydesign", path: "index.html", title | post_id, link_map,
   front_page}` (harness: `pursae_mcp.py import-html ./design/index.html kit=mydesign path=index.html
   link_map=@links.json front_page=true`). Each top-level part becomes a code section: the design's markup,
   CSS and JavaScript as written (animations, canvas, scripts keep working); every element is a block
   the owner selects, moves, duplicates, deletes and restyles (Style, Layers), with texts, links and
   images edited in place; the parts are reordered like sections.
3. A whole site: import each page of the design the same way with `link_map` pointing the design's own
   links at site URLs (`{"about.html": "/about/", "forum/index.html": "/forums/"}`); `templates: true`
   (`templates_only: true` for a page whose content the site renders, like a forum or shop page) makes
   the header and footer site-wide Theme Builder templates.
4. Read `warnings` (unmapped links, files missing from the kit) and compare with the design.
5. Pages the site renders itself keep the design too: give each forum view the design's markup with
   `forum_set_template` (see playbook-forum.md, "From an HTML design").

**Rebuilt as builder sections** (`method: "exact"`, for owners who will restyle it by dragging):

1. `web_builder_import_html {title, html, method: "exact"}` → `import_url` + `job`.
2. Open `import_url` in a browser — your browser tool (navigate and wait for the title
   "Pursae import: done") or send the link to the user. With the harness: `pursae_mcp.py import-html
   design.html method=exact title="Home"` does steps 1–3 (Playwright when installed, else the default browser).
3. `web_builder_import_finish {job}` publishes and lists notes (SEO, anything not kept).
4. Compare with the design at desktop and phone width when you can take screenshots; fix details with
   `web_builder_edit_page`. `method: "quick"` (no browser) is only approximate.

What `exact` does not bring over: animations and hover effects, `position: absolute` overlays (they follow the
flow), pseudo-elements (`::before`), scripts and iframes; on phones the builder adds a working menu
button where the design hides its links.
