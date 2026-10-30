---
title: theme.json
description: Every field of the theme package manifest.
weight: 30
section: themes
---

`theme.json` describes what a Go theme would register in code. Unknown fields are rejected.

```json
{
  "slug": "starter",
  "name": "Starter",
  "version": "1.0.0",
  "description": "A minimal starting point.",
  "author": "Gopher Co.",
  "author_url": "https://example.com",
  "theme_url": "https://example.com/starter",
  "tags": ["blog", "one-column"],
  "requires_core": "0.1.0",
  "screenshot": "screenshot.png",

  "widget_areas": [
    {"id": "sidebar-1", "name": "Sidebar", "description": "Appears on the right."}
  ],
  "menu_locations": [
    {"id": "primary", "name": "Primary Menu"}
  ],

  "styles": [
    {"handle": "starter-style", "src": "assets/style.css", "deps": [], "media": "all"}
  ],
  "scripts": [
    {"handle": "starter-nav", "src": "assets/js/navigation.js", "deps": [], "in_footer": true}
  ],
  "editor_styles": ["assets/editor-style.css"],
  "preload_fonts": ["assets/fonts/inter.woff2"],

  "palette": [
    {"name": "Ink", "slug": "ink", "color": "#1d2327"}
  ],
  "supports": {
    "post-thumbnails": true,
    "custom-logo": true,
    "title-tag": true,
    "html5": ["search-form", "comment-form", "gallery", "caption"],
    "responsive-embeds": true,
    "align-wide": false
  },

  "customizer": {
    "sections": [
      {
        "id": "starter_colors", "title": "Colors", "priority": 40,
        "settings": [
          {"id": "accent", "label": "Accent color", "type": "color", "default": "#2271b1", "transport": "postMessage", "css_var": "--accent"},
          {"id": "show_sidebar", "label": "Show sidebar", "type": "checkbox", "default": true, "body_class": "has-sidebar"}
        ]
      }
    ]
  }
}
```

## Metadata

| Field | Required | Rules |
|-------|----------|-------|
| `slug` | yes | `^[a-z0-9]+(-[a-z0-9]+)*$`, 2–64 characters; the directory name and the theme's identifier (theme mods, widgets and menus are stored per slug). Bundled slugs are reserved |
| `name` | yes | shown on the Themes screen |
| `version` | yes | semver |
| `description`, `author`, `author_url`, `theme_url` | no | shown on the Themes screen and the marketplace |
| `tags` | no | marketplace filters (`blog`, `dark`, `one-column`, …) |
| `requires_core` | no | minimum WordPress Go version |
| `screenshot` | no | a file under `assets/`, usually `screenshot.png` |

## Structure

| Field | What it registers |
|-------|-------------------|
| `widget_areas[]` | `register_sidebar()`: `id`, `name`, `description`. Rendered areas are available to templates as `{{index .Areas "sidebar-1"}}` and managed under Appearance › Widgets. Optional `before_widget`, `after_widget`, `before_title`, `after_title` override the default `<section class="widget">` / `<h2 class="widget-title">` wrappers |
| `menu_locations[]` | `register_nav_menus()`: `id`, `name`. Assigned under Appearance › Menus; printed with `{{wp_nav_menu "primary"}}` |

## Assets

| Field | Meaning |
|-------|---------|
| `styles[]` | `wp_enqueue_style()`: `handle`, `src` (a path in the package, served under the theme URI, or an absolute URL), `deps` (core handles such as `wp-block-library`, or the theme's own), `media` |
| `scripts[]` | `wp_enqueue_script()`: `handle`, `src`, `deps` (`jquery`, the theme's own), `in_footer` |
| `editor_styles[]` | `add_editor_style()`: stylesheets loaded into the block editor and TinyMCE |
| `preload_fonts[]` | font files WPGo Cache preloads with `<link rel="preload">` |

Order follows dependencies, like `wp_enqueue_*`. See [styles and scripts](styles-and-scripts.md).

## Editor

| Field | Meaning |
|-------|---------|
| `palette[]` | `editor-color-palette`: `name`, `slug`, `color`. Also printed as `--wp--preset--color--<slug>` custom properties |
| `supports` | `add_theme_support()` features reported to the block editor: `title-tag`, `custom-logo` (`true` or `{width, height, flex-width, flex-height}`), `post-thumbnails`, `html5`, `responsive-embeds`, `align-wide`, `wp-block-styles`, `custom-spacing`, `custom-line-height` |

See [block editor support](block-editor-support.md).

## Customizer

`customizer.sections[]` with `id`, `title`, `priority` and `settings[]`:

| Setting field | Meaning |
|---------------|---------|
| `id` | the theme mod name |
| `label`, `description` | the control |
| `type` | `color`, `text`, `textarea`, `number`, `checkbox`, `select` (with `choices: {"value": "Label"}`), `radio` (same), `image` (an attachment id) |
| `default` | used until the user saves |
| `transport` | `refresh` (default) or `postMessage` for a live preview |
| `css_var` | print the value as a custom property on `:root` (`--accent: #2271b1`) |
| `body_class` | add this class to `<body>` when the value is truthy |

Values are theme mods of this theme, read in templates with `{{index .ThemeMods "accent"}}`
([customizer](customizer.md)).
