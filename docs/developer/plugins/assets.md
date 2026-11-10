---
title: Assets (CSS and JavaScript)
description: Ship stylesheets, scripts, images and fonts in the package and enqueue them on the site or in the admin.
weight: 100
section: plugins
---

A package may contain an `assets/` directory. Its files are served at
`/wp-content/plugins/<slug>/assets/<path>?ver=<version>` while the plugin is active and can be
enqueued on the front end, in the admin, or both. Permission: `content:filter`; a **script** also
needs `content:html` on the front end and `admin:scripts` in the admin (`wpgo.Both` needs both),
because a script runs with the session of whoever views the page — on the front end that includes
logged-in administrators. Both permissions are shown as "full control of the site" on the activation
screen and are not ticked by default. A **stylesheet** in the admin (`wpgo.Admin`, `wpgo.Both`) needs
`admin:settings` (or `admin:scripts`) and is printed on the plugin's own settings page
(`/wp-admin/options-general?page=<slug>`) only; with `admin:scripts` it is printed on every admin
screen. A refused enqueue is logged and the asset is not printed.

## Rules

| | |
|---|---|
| Extensions | `.css .js .svg .png .jpg .jpeg .webp .woff2` |
| Size | 2 MiB per file, 16 MiB and 256 files per package |
| Paths | plain segments of letters, digits, `.`, `-`, `_`; no segment starts with a dot; sub-directories allowed (`js/app.js`) |
| Integrity | `wpgo-pkg pack` writes each file's SHA-256 into `plugin.json` `assets`, which the signature covers; the installer rejects a package whose assets are unlisted, missing or modified |
| Serving | `Cache-Control: public, max-age=604800`, `nosniff`; SVGs get a sandboxing CSP |

## Enqueue

```go
func init() {
	wpgo.EnqueueStyle(wpgo.Style{Handle: "gopher-facts", Src: "style.css", Where: wpgo.Front})
	wpgo.EnqueueScript(wpgo.Script{Handle: "gopher-admin", Src: "js/admin.js", Where: wpgo.Admin, InFooter: true})
	wpgo.EnqueueScript(wpgo.Script{Handle: "gopher-widgets", Src: "js/widgets.js", Deps: []string{"gopher-core"}, Where: wpgo.Both})
}
```

| Field | Meaning |
|-------|---------|
| `Handle` | `^[a-z0-9_-]{1,64}$`, unique per plugin and kind; the tag id becomes `<slug>-<handle>-css` / `-js` |
| `Src` | path inside `assets/`; a style must end in `.css`, a script in `.js`, and the file must be in the package |
| `Where` | `wpgo.Front` (default), `wpgo.Admin` or `wpgo.Both` |
| `Deps` | handles of this plugin's assets of the same kind that must load first (≤10) |
| `Media` | the stylesheet's `media` attribute, default `all` |
| `InFooter` | print the script before `</body>` instead of in `<head>` |

Front-end tags are printed by `wp_head` after the theme's own assets (styles and head scripts)
and by `wp_footer` (footer scripts); admin tags after the core admin bundle. Call the enqueue
functions from `init` or `OnInit`: they are static registrations, not per-request decisions.
To load something only on some pages, add a class in a `the_content` filter and let CSS do the
rest, or check `document.body` in the script.

## Images and fonts

Reference them from your CSS with relative URLs (`url(fonts/inter.woff2)`): the stylesheet and
the font are served from the same `/assets/` directory. From HTML you return in a shortcode or
filter, use the absolute path `/wp-content/plugins/<slug>/assets/img/logo.svg`.

## Content Security Policy

The site's CSP allows scripts and styles from the site's own origin only, which is where plugin
assets are served from. HTML a shortcode or content filter returns goes through KSES unless the
plugin holds `content:html` ([shortcodes](shortcodes.md)), so inline `<script>` tags and event
handlers need that permission; prefer a packaged script.
