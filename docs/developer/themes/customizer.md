---
title: Customizer
description: Offer theme options under Appearance › Customize with a live preview, and use them in templates.
weight: 50
section: themes
---

Appearance › **Customize** is the WordPress Customizer: Site Identity (title, tagline, logo, site
icon), Colors, Menus, Widgets, Homepage Settings, Additional CSS — plus the sections and settings
a theme declares. Values are **theme mods**, stored per theme, so switching themes and back keeps
them.

## Declare settings

In `theme.json`:

```json
"customizer": {
  "sections": [
    {"id": "starter_layout", "title": "Layout", "priority": 40, "settings": [
      {"id": "content_width", "label": "Content width", "type": "radio", "default": "normal",
       "choices": {"narrow": "Narrow", "normal": "Normal", "wide": "Wide"}},
      {"id": "show_sidebar", "label": "Show sidebar", "type": "checkbox", "default": true, "body_class": "has-sidebar"},
      {"id": "footer_text", "label": "Footer text", "type": "textarea", "default": ""}
    ]},
    {"id": "starter_colors", "title": "Theme colors", "settings": [
      {"id": "accent", "label": "Accent", "type": "color", "default": "#2271b1", "css_var": "--accent", "transport": "postMessage"}
    ]}
  ]
}
```

| `type` | Control | Value |
|--------|---------|-------|
| `color` | colour picker | `#rrggbb` |
| `text`, `textarea` | text inputs | string |
| `number` | number input | number |
| `checkbox` | checkbox | `true` / `false` |
| `select`, `radio` | choices from `choices` | one of the keys |
| `image` | media chooser | attachment id |

Settings in a section named `title_tagline` appear under Site Identity, like the Classic theme's
"Display Site Title" and "Display Tagline" toggles.

## Use the values

In templates every theme mod is in `.ThemeMods`:

```html
<footer class="site-footer">{{with index .ThemeMods "footer_text"}}<p>{{.}}</p>{{end}}</footer>
```

Two shortcuts save template code:

- `css_var` prints the value as a custom property on `:root` inside `wp_head` (`.Head`), so your
  stylesheet can use `var(--accent)`.
- `body_class` adds that class to `.BodyClass` when the value is truthy (a checkbox). For a
  select or radio, read `.ThemeMods` in the template and print your own class.

## Live preview

`"transport": "postMessage"` tells the Customizer to update the preview without reloading: for
`css_var` and `body_class` settings the core's preview script applies the change itself; for
anything else add a script under `assets/` that listens for `wp.customize('setting_id', …)`,
exactly as a WordPress theme would (see Classic's `assets/js/customize-preview.js`), and enqueue
it in `scripts[]`; it only does something inside the Customizer frame, where `wp.customize` exists.
Settings without `postMessage` refresh the preview frame.

## What the core provides

- **Site Identity**: `blogname`, `blogdescription`, custom logo (`.CustomLogo` renders it with
  `supports.custom-logo` sizes), site icon (`.SiteIcon`).
- **Colors**: `background_color` and the `custom-background` support.
- **Menus** and **Widgets** panels edit the locations and areas from `theme.json`.
- **Homepage Settings**: latest posts or a static page (`show_on_front`, `page_on_front`,
  `page_for_posts`).
- **Additional CSS**: `.CustomCSS`, printed inside `.Head`.

Changes are previewed through a changeset and saved together with **Publish**; the preview frame
is the real site rendered with the unsaved values.
