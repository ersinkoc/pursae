---
title: Styles and scripts
description: Enqueue stylesheets, scripts, editor styles and fonts from theme.json, and what wp_head prints.
weight: 60
section: themes
---

A theme package loads its assets through `theme.json`; the core prints the tags in `wp_head()`
(`{{.Head}}`) and `wp_footer()` (`{{.Footer}}`) in dependency order, with a `?ver=<theme version>`
query so browsers refetch after an update.

```json
"styles": [
  {"handle": "starter-fonts", "src": "https://fonts.example.com/inter.css"},
  {"handle": "starter-style", "src": "assets/style.css", "deps": ["starter-fonts", "wp-block-library"], "media": "all"},
  {"handle": "starter-print", "src": "assets/print.css", "media": "print"}
],
"scripts": [
  {"handle": "starter-nav", "src": "assets/js/navigation.js", "in_footer": true},
  {"handle": "starter-gallery", "src": "assets/js/gallery.js", "deps": ["jquery"], "in_footer": true}
],
"editor_styles": ["assets/editor-style.css"],
"preload_fonts": ["assets/fonts/inter-var.woff2"]
```

| Field | Meaning |
|-------|---------|
| `handle` | the tag id: `<handle>-css`, `<handle>-js` |
| `src` | a path inside the package (served at `/wp-content/themes/<slug>/<path>`) or an absolute URL. Remote URLs must be allowed by the site's Content Security Policy (`style-src 'self'`; remote stylesheets are not allowed by default, remote scripts never) — ship fonts and scripts in the package |
| `deps` | handles loaded first: the theme's own or core ones |
| `media` | `all` (default), `print`, `screen and (min-width: …)` |
| `in_footer` | print the script before `</body>` |

## Core handles you can depend on

| Handle | What |
|--------|------|
| `wp-block-library` | the block library stylesheet (`/wp-includes/css/dist/block-library/style.min.css`), printed on every page unless `block_styles` is on (then the used blocks' styles are inlined instead) |
| `dashicons` | the icon font (loaded with the admin bar) |
| `jquery` | jQuery from `/wp-includes/js/jquery/` |
| `comment-reply` | threaded comment replies; the core enqueues it itself on singular pages with threaded comments |

## What wp_head prints, in order

1. the theme's `head_top` (bundled themes only: e.g. a no-js class swap script),
2. `robots`, feed links, the admin-bar styles for logged-in users,
3. the block library stylesheet and the inline theme/global styles (with `block_styles`: only the
   styles of the blocks the page uses, inline),
4. **your `preload_fonts[]` preloads, `styles[]` and head `scripts[]`**, then `css_var` custom
   properties from the Customizer,
5. `rel="https://api.w.org/"`, the generator meta, canonical and shortlink,
6. custom background, site icon, Additional CSS,
7. plugin stylesheets and head scripts.

`wp_footer` prints `comment-reply`, the admin bar script, your footer scripts and plugin footer scripts.

A page published from Pursae Web Builder is not rendered through the theme, so none of the theme's
styles and scripts load there; it prints its own page CSS and runtime
([Pursae Web Builder](../web-builder.md)).

## Editor styles

`editor_styles[]` stylesheets are loaded into the block editor canvas and TinyMCE so the editor
looks like the front end. Write them against the editor's wrapper (`.editor-styles-wrapper`),
exactly as for WordPress; the core scopes them for you.

## Fonts

Put font files under `assets/fonts/` and reference them from your CSS with relative URLs.
`preload_fonts[]` adds `<link rel="preload" as="font" crossorigin>` in `wp_head` on every page for
the fonts the first screen paints with. The preload URL is the URL the font is served from
(`assets/fonts/inter.woff2` → `/wp-content/themes/<slug>/fonts/inter.woff2`), which is also what a
relative `url(fonts/inter.woff2)` in `assets/style.css` resolves to, so the browser reuses the
preloaded file. No external font service is contacted unless you add one.

## Per-block styles

`"block_styles": true` switches the core's block styles to per-block loading, as Twenty
Twenty-Five does: the 140 KB render-blocking `block-library/style.min.css` is no longer printed,
and each page inlines only the styles of the core blocks it contains. List block names instead
(`"block_styles": ["navigation", "post-title"]`) to leave out blocks your own stylesheet styles
completely. With it on, also drop `wp-block-library` from your `deps`.

## Browser caching

Your `styles[]` and `scripts[]` are linked with `?ver=<theme version>` and served with
`Cache-Control: public, max-age=31536000, immutable`; bump `version` when you ship changes.
Files saved in the Theme File Editor and unversioned URLs (fonts, images from `url()`) are served
with `no-cache` and revalidate.

## Pursae Cache

Page optimisation (lazy images, deferred scripts, minified HTML) applies to your output too.
With "Defer JavaScript" on and its handle list left empty, your `scripts[]` are deferred along
with the core's safe defaults; only scripts in the body are deferred, so put scripts that can
wait in the footer (`in_footer`).
Mark a script that must not be deferred by listing it in Pursae Cache › Page Optimization ›
exclusions, or inline it.
