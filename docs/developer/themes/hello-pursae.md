---
title: Hello Pursae and the Theme Builder
description: The bundled blank theme of Pursae Web Builder, and how Theme Builder templates replace a theme's header, content and footer.
weight: 80
section: themes
---

**Hello Pursae** (`hello-pursae`) is a bundled Go theme made for
[Pursae Web Builder](../web-builder.md): a blank canvas whose header, footer, single, archive,
search and 404 views are designed in the builder's **Theme Builder**
([guide](../web-builder-theme-builder.md)). Without templates it is a plain, usable theme on its
own: a minimal header, the content and a footer.

## What it offers

| | |
|---|---|
| Menu locations | `primary` (Primary Menu), `footer` (Footer Menu) |
| Supports | `custom-logo`, `title-tag`, `post-thumbnails`, `menus`, `html5`, `align-wide`, `responsive-embeds`, `editor-styles`, `automatic-feed-links` |
| Files | `web/themes/hello-pursae/templates/header.html`, `pages.html`, `content.html`; `assets/style.css` (a few KB), `editor-style.css`, `screenshot.png` |
| Code | `internal/server/theme_hellopursae.go` |
| Body classes | `hello-pursae`, plus `wb-site` when at least one Theme Builder part renders |

Store pages (shop, product, cart, checkout, account) print their content like any page, as in
Twenty Twenty-Five.

## When it becomes active

Activating Pursae Web Builder switches the site to Hello Pursae and stores the previous theme in
`web_builder_previous_theme`; deactivating the plugin switches back while the site is still on
Hello Pursae (the default theme when the previous one is gone). If the user picked another theme in
between, nothing changes. Sites that had the builder on before Hello Pursae existed get a one-time
"Switch to Hello Pursae" notice instead of a silent switch. Appearance › Themes can activate it at
any time like any theme.

## How templates replace the theme's output

Theme Builder templates are used **only while Hello Pursae is the active theme** (or the theme
being previewed). The check is on the slug, so a theme package or another bundled theme never
receives template parts: there, builder pages keep the full-page canvas
([contract §10](../web-builder-contract.md#10-front-end-page-for-reference)) and templates are
ignored.

For each request the core picks the templates by their display conditions and hands the finished
markup to the theme in `.Data.wb` (`wbThemePartsView`):

| Field | What | Without it |
|-------|------|------------|
| `.Data.wb.Header` | the header template, filled | Hello Pursae's header: custom logo or site title, tagline, the `primary` menu |
| `.Data.wb.Main` | the page's own builder design, else the matching Single Post, Single Page, Single, Front Page, Archive, Search Results or 404 template, filled | the theme's default view (the post, the loop with pagination, the 404 message) |
| `.Data.wb.HasMain` | `Main` carries its own `<main>` landmark | — |
| `.Data.wb.Footer` | the footer template, filled | the `footer` menu, copyright and privacy link |

`.Data.wb` is absent when the plugin is off; then every view prints the theme's own markup. The
templates wrap each part:

```html
{{define "site-header"}}{{$wb := .Data.wb}}{{if and $wb $wb.Header}}<div class="wb-part wb-part-header">{{$wb.Header}}</div>{{else}}<header id="site-header" class="site-header">…</header>{{end}}{{end}}

{{define "main-open"}}{{$wb := .Data.wb}}{{if and $wb $wb.Main}}{{if $wb.HasMain}}<div id="content" class="site-main wb-main-part">{{else}}<main id="content" class="site-main wb-main-part">{{end}}{{else}}<main id="content" class="site-main">{{end}}{{end}}
```

so the page always has exactly one `<main>` and the skip link's `#content` target. A builder page
set to **This page only (canvas)** gets no header or footer part: only its own document is printed
between Hello Pursae's `<head>` and the footer hooks.

The core, not the theme, does the rest: it merges the fonts of all parts, prints each part's CSS
as `<style id="wb-css-<post id>">`, adds the builder's base CSS and the runtime script once, adds
**Edit Header / Edit Template / Edit Footer** to the admin bar, and tags the cached page so a
template change or a post save refreshes it. `wp_head`, body classes, the admin bar and every
plugin's head and footer output work as on any theme.

## Editing Hello Pursae

The templates can be changed in Appearance › Theme File Editor like any bundled theme
([file editor](file-editor.md)); keep the `.Data.wb` branches, or the Theme Builder parts no
longer show. To change the look of a site built with the Theme Builder, edit the templates in the
builder rather than the theme.
