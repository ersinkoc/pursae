---
name: pursae-site-builder
description: Build or change a whole Pursae site through its MCP server — company sites, online stores, forums, blogs, landing pages — with Pursae Web Builder pages the owner can keep editing by drag and drop. Use when the user gives a Pursae MCP URL (…/wp-json/pursae/v1/mcp) or an Application Password, asks to build/redesign a Pursae site or page, import HTML into the builder, set up Pursae Commerce or Pursae Forum, or when pursae MCP tools (site_overview, web_builder_*) are available.
---

# Pursae site builder

Pursae is a WordPress-compatible CMS written in Go. Its MCP server exposes the whole site: Web Builder
pages (built and **published on the server**, editable afterwards in the visual editor), content, media,
menus, plugins, themes, Pursae Commerce, Pursae Forum, contact forms, SEO, cache and settings.

## 1. Connect

You need the site's MCP URL (`https://<site>/wp-json/pursae/v1/mcp`), a user name and an
**Application Password** (Users › Profile › Application Passwords, or Tools › MCP in the admin, which
shows the ready command).

- **pursae tools already loaded** (names like `site_overview`, `web_builder_build_page`): use them.
- **Claude Code, credentials pasted in chat**: register the server for the next sessions —

  ```bash
  claude mcp add --transport http pursae "<MCP URL>" --header "Authorization: Basic $(printf '%s' '<user>:<app password>' | base64)"
  ```

  New MCP servers load when a session starts, so **for this session use the harness** (next bullet).
  Never write the password into memory files, the repository or a commit.
- **Harness (no MCP client needed)**: `scripts/pursae_mcp.py` speaks the same JSON-RPC over HTTPS.

  ```bash
  export PURSAE_URL="https://example.com" PURSAE_USER="admin" PURSAE_APP_PASSWORD="xxxx xxxx xxxx xxxx xxxx xxxx"
  python3 scripts/pursae_mcp.py check                       # who am I, what is on
  python3 scripts/pursae_mcp.py tools                       # tool names + one-line descriptions
  python3 scripts/pursae_mcp.py call site_overview
  python3 scripts/pursae_mcp.py call web_builder_build_page @page.json   # arguments from a file
  python3 scripts/pursae_mcp.py prompt build_site kind=store brief="Handmade ceramics shop"
  python3 scripts/pursae_mcp.py describe web_builder_edit_page          # full input schema
  ```

  Arguments are JSON inline, `@file.json`, or `-` for stdin. Exit code 1 on a tool error, with the
  server's message. Keep the password in the environment, not in arguments.

## 2. Ground rules

1. **Start with `site_overview`.** Plan with the user before building: site type, pages, language,
   brand color, tone. One short question round, then a written plan, then build.
2. **Ask before turning plugins on or off.** Web Builder (`golementor`), Commerce, Forum, SEO… may be
   off; tools say so. Activating Web Builder switches the theme to Hello Pursae.
3. **Build visible pages with Web Builder**, not raw post HTML: `web_builder_catalog` →
   `web_builder_build_page`. Pages publish immediately and the owner edits them in the editor
   (`edit_url`).
4. **Real copy everywhere**, in the site's language. The build answer lists fields still holding sample
   text (`warnings`) — fix them with `web_builder_edit_page`.
5. **Never invent secrets** (payment keys, SMTP passwords, Google client secrets). Leave them to the user.
6. **Verify before you report**: open the returned URLs (browser tools if you have them, or fetch them),
   then give the user every page URL and edit link.

## 3. The building blocks

- `web_builder_catalog` — section kinds (nav, hero, banner, features, media, steps, quote, faq, cta,
  footer, text, image, form, layout, custom, html) and ~120 designs with style tags. `kind=<kind>`
  gives fields, design extras and defaults. See [references/sections.md](references/sections.md).
- `web_builder_build_page {title | post_id, sections, site, layout, status, front_page}`.
- `web_builder_edit_page {post_id, ops}` — `add`, `update` (content/items/style/design merge),
  `remove`, `move`, `duplicate`.
- HTML design → page, 1:1: `web_builder_kit_upload {kit, files}` (the design's CSS, JS, fonts, images),
  then `web_builder_import_html {html, kit, path, link_map, templates, title | post_id}` — `method: "code"`
  (default for admins) keeps the design's own code, every element a block editable on the canvas; `method: "exact"` rebuilds it as
  builder sections (open the returned `import_url` in a browser, then `web_builder_import_finish {job}`).
  Harness: `kit-upload ./design mydesign`, `import-html ./design/index.html kit=mydesign path=index.html`.
  A whole folder of pages: `web_builder_import_site {kit, pages: [{path, html}]}` (harness:
  `import-site ./design kit=mydesign`) — pages, parents, links, header/footer templates with the active
  menu link, 404 template; idempotent.
  See [references/playbook-pages.md](references/playbook-pages.md).
- Theme Builder: `web_builder_create_template {type: header|footer|single|archive|…}` →
  `web_builder_build_page {post_id: <template>}`; headers and footers apply site-wide by default, other templates take rules like `include/singular/post` via `web_builder_set_template_conditions`. Or a whole
  kit: `web_builder_list_theme_kits` → `web_builder_import_theme_kit`.
- `menu_build {name, locations, items: [{title, page_id|url, children}]}` — idempotent by name.
- Commerce: `commerce_*` (products, categories, coupons, settings, payment gateways, shipping zones).
- Forum: `forum_*` (categories/forums, topics); settings with `update_options {group: "forum"}`.
- Anything else: `rest_request` with the same permissions.

## 4. Playbooks

Pick the one that fits and follow it step by step (the server has the same text as the MCP prompt
`build_site`):

- Company / service site → [references/playbook-business.md](references/playbook-business.md)
- Online store → [references/playbook-store.md](references/playbook-store.md)
- Forum / community → [references/playbook-forum.md](references/playbook-forum.md)
- One extra page, a redesign, or HTML import → [references/playbook-pages.md](references/playbook-pages.md)
