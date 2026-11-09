---
title: Block editor support
description: "Tell the block editor about your theme: palette, alignments, supports, editor styles."
weight: 70
section: themes
---

The block editor (Pursae Editor) asks `/wp-json/wp/v2/themes?status=active` what the theme supports
and styles its canvas accordingly. A theme package answers through `theme.json`.

## Supports

```json
"supports": {
  "title-tag": true,
  "post-thumbnails": true,
  "custom-logo": {"width": 240, "height": 80, "flex-width": true, "flex-height": true},
  "html5": ["search-form", "comment-form", "gallery", "caption", "style", "script"],
  "responsive-embeds": true,
  "align-wide": true,
  "wp-block-styles": true,
  "custom-spacing": true,
  "custom-line-height": true
}
```

| Key | Effect |
|-----|--------|
| `title-tag` | the core prints `<title>`; always on in Pursae, kept for compatibility |
| `post-thumbnails` | the Featured Image panel appears in the editor; `.Thumbnail` is filled for templates |
| `custom-logo` | Site Identity gets a logo control with these dimensions; `.CustomLogo` renders it |
| `html5` | HTML5 markup for the listed parts (search form, comment form, galleries, captions) |
| `responsive-embeds` | embeds get the `wp-embed-responsive` body class and the aspect-ratio CSS |
| `align-wide` | Wide and Full alignments in the editor. Style `.alignwide` and `.alignfull` in your CSS |
| `wp-block-styles` | load the opinionated core block styles (`theme.min.css`) |
| `custom-spacing`, `custom-line-height` | padding/margin and line-height controls on blocks |

## Palette

```json
"palette": [
  {"name": "Ink", "slug": "ink", "color": "#1d2327"},
  {"name": "Paper", "slug": "paper", "color": "#ffffff"},
  {"name": "Accent", "slug": "accent", "color": "#2271b1"}
]
```

The editor shows these colours in every colour control, and blocks that use them get
`has-<slug>-color` / `has-<slug>-background-color` classes. The core prints
`--wp--preset--color--<slug>` custom properties and the matching utility classes, so style your
own elements with `var(--wp--preset--color--accent)`.

## Editor styles

`editor_styles[]` loads your stylesheet into the editor canvas (and TinyMCE in the Classic
Editor). Keep typography, colours and content width there, and the editor matches the front
end. Classic's `assets/editor-style.css` is a worked example.

## Rendering blocks on the front end

Block markup is rendered by the core (`the_content`): static blocks come out as saved, dynamic
blocks (latest posts, search, archives, categories, synced patterns, shortcode) are rendered in
Go, and the block library stylesheet is loaded (or, with `"block_styles": true` in theme.json,
only the styles of the blocks on the page). Your theme styles the result:
`.wp-block-*` classes, `.alignwide`, `.has-*-color`, `.wp-block-image figcaption`, and so on.

## Patterns and templates

The editor's Patterns inserter lists core's block patterns and, for the bundled Twenty
Twenty-Five, the theme's own patterns (`patterns/*.php` in WordPress) by category, from
`/wp/v2/block-patterns/patterns` and `/wp/v2/block-patterns/categories`. The core has no PHP, so
the pattern files are run by WordPress once, at build time (`tools/twentytwentyfive/build.php`
writes `web/themes/twentytwentyfive/patterns.json`; `{{theme_uri}}` in it is the theme's URL). Old
core categories are migrated as WordPress does (`query` → `posts`, `buttons` → `call-to-action`,
`columns` → `text`). Patterns built from blocks that only a block theme's templates render (site
title, navigation, query loop, comments, template parts) are returned with `inserter: false`: post
content does not render those blocks.

Block patterns and the site editor's block templates are not part of theme packages in this
version: `/wp/v2/templates` returns an empty list. Classic PHP-style templates (this section) are
the way to build a theme.
