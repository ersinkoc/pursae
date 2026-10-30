---
title: Your first theme
description: Build a theme package from a directory, zip it, upload it and activate it.
weight: 20
section: themes
---

A theme package is a directory with no code in it:

```text
starter/
  theme.json          metadata, widget areas, menus, styles, customizer options
  templates/          Go html/template files
    index.html
    single.html
    page.html
    header.html
    footer.html
  assets/
    style.css
    screenshot.png
  readme.md           optional, shown on the marketplace page
```

## 1. theme.json

The minimum:

```json
{
  "slug": "starter",
  "name": "Starter",
  "version": "1.0.0",
  "description": "A minimal starting point.",
  "author": "Gopher Co.",
  "screenshot": "screenshot.png",
  "menu_locations": [{"id": "primary", "name": "Primary Menu"}],
  "widget_areas": [{"id": "sidebar-1", "name": "Sidebar", "description": "Appears on the right."}],
  "styles": [{"handle": "starter-style", "src": "assets/style.css"}]
}
```

Every field is described on the [theme.json](theme-json.md) page.

## 2. Templates

`templates/index.html` is the only required template; the others fall back to it along the
WordPress hierarchy ([templates](templates.md)). A complete `index.html` for the blog:

```html
{{template "header" .}}
<main id="main" class="site-main">
{{range .Posts}}
  <article id="post-{{.ID}}" class="{{.Class}}">
    <h2 class="entry-title"><a href="{{.Permalink}}">{{.Title}}</a></h2>
    <div class="entry-meta">{{.EntryMeta}}</div>
    <div class="entry-content">{{if .ShowExcerpt}}{{.Excerpt}}{{else}}{{.Content}}{{end}}</div>
  </article>
{{else}}
  <p>Nothing found.</p>
{{end}}
{{.Pagination}}
</main>
{{template "footer" .}}
```

`header.html` and `footer.html` define the parts the pages include:

```html
{{define "header"}}<!doctype html>
<html lang="en-US">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.DocumentTitle}}</title>
{{.Head}}
</head>
<body class="{{.BodyClass}}">
{{.BodyOpen}}
<header class="site-header">
  {{.CustomLogo}}
  <p class="site-title"><a href="{{.HomeURL}}" rel="home">{{.SiteName}}</a></p>
  <p class="site-description">{{.Description}}</p>
  {{with index .Menus "primary"}}<nav aria-label="Primary"><ul class="menu">
    {{range .}}<li class="menu-item{{if .Current}} current-menu-item{{end}}"><a href="{{.URL}}">{{.Label}}</a></li>{{end}}
  </ul></nav>{{end}}
</header>
{{end}}

{{define "footer"}}
{{with index .Areas "sidebar-1"}}<aside class="widget-area">{{.}}</aside>{{end}}
<footer class="site-footer">{{.PrivacyLink}}</footer>
{{.Footer}}{{.AdminBar}}
</body>
</html>
{{end}}
```

`{{.Head}}` prints `wp_head()` (feeds, block styles, your enqueued styles, the Customizer's CSS,
plugin assets) and `{{.Footer}}` prints `wp_footer()`. Leave them in: without them scripts, the
admin bar and plugins do not work.

## 3. Assets

Put `style.css`, scripts, fonts and images under `assets/` and reference them from `theme.json`
(`styles`, `scripts`, `preload_fonts`, `editor_styles`). `screenshot.png` (1200×900) is the
Themes screen thumbnail.

## 4. Zip it

```bash
cd starter
zip -r ../starter.zip theme.json templates assets readme.md
```

The files sit at the zip root (a single top-level folder is also accepted and stripped). Limits:
32 MiB per package, 4 MiB per file, and only these extensions: `.json .html .css .js .map .svg
.png .jpg .jpeg .gif .webp .avif .ico .woff .woff2 .ttf .otf .md .txt`.

## 5. Upload and activate

Appearance › Themes › **Add Theme** › **Upload Theme**, choose `starter.zip`. The package is
validated (`theme.json`, template syntax, file types), unpacked to `<data>/themes/starter/` and
listed on the Themes screen. **Live Preview** shows it without activating; **Activate** switches
the site. A package whose slug matches a bundled theme is rejected.

## 6. Iterate

Either upload again, or edit in place with Appearance › Theme File Editor — the overlay directory
is the same `<data>/themes/starter/` the installer wrote to ([file editor](file-editor.md)).
During development point `WPGO_THEMES_DIR` at your working copy: the server loads every
`<dir>/<slug>/theme.json` at start-up.
