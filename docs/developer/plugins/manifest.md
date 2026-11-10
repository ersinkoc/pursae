---
title: plugin.json
description: Every field of the plugin manifest and how it is validated.
weight: 30
section: plugins
---

`plugin.json` sits at the root of the package. Unknown fields are rejected, so a typo cannot
silently drop a permission.

```json
{
  "slug": "gopher-facts",
  "name": "Gopher Facts",
  "version": "1.2.0",
  "description": "Random gopher facts as a shortcode and a REST route.",
  "author": "Gopher Co.",
  "author_uri": "https://example.com",
  "plugin_uri": "https://example.com/gopher-facts",
  "requires": "0.1.0",
  "license": "GPL-2.0-or-later",
  "permissions": ["content:filter", "rest:routes", "options:read", "admin:settings"],
  "hooks": ["the_content", "cron_gopher_facts_refresh"],
  "shortcodes": ["gopher-facts"],
  "assets": {"style.css": "3f1a…"}
}
```

| Field | Required | Rules |
|-------|----------|-------|
| `slug` | yes | 2–64 characters, `^[a-z0-9]+(-[a-z0-9]+)*$`. Identifies the plugin, owns its hooks, prefixes its REST namespace and option names. Reserved: `core`, the slugs of the built-in plugins (`classic-editor`, `wpgo-cache`, `goocommerce`, …) and the roots of core REST namespaces (`wp`, `wp-admin`, `wp-json`, `wp-content`, `wp-includes`, `wp-site-health`, `wp-block-editor`, `wp-abilities`, `admin`, `oembed`, `batch`, `pursae`, `wpgo`, `wc`, `wc-admin`, `wc-analytics`, `wc-store`, `wc-telemetry`, `pll`, `contact-form-7`, `web-builder`, `elementor`, `gooike`, `headlessgo`, `pixelgo`, `seogo`, `rankmath`, `yoast`); a package using one is refused at install and does not load |
| `name` | yes | up to 100 characters |
| `version` | yes | `1.2.3` or `1.2.3-beta.1`; the Hub refuses to publish a version that is not newer than the last |
| `description` | no | up to 2000 characters, plain text |
| `author`, `author_uri`, `plugin_uri` | no | URIs must be absolute `http(s)` URLs |
| `requires` | no | minimum Pursae version; the site refuses to install or activate a plugin that needs a newer core |
| `requires_plugins` | no | slugs of plugins that must be active before this one can be activated (a Wasm plugin slug or a built-in plugin's Plugins screen slug, e.g. `contact-form-7`); activation is refused and lists the missing ones |
| `license` | no | an SPDX identifier |
| `permissions` | yes (may be empty) | from the [permission vocabulary](permissions.md); no duplicates |
| `hooks` | yes (may be empty) | every hook the plugin may subscribe to (letters, digits, `_ . : / -`, max 128). Registering a hook that is not listed is refused at runtime. A hook that needs a permission requires that permission to be declared |
| `shortcodes` | no | every shortcode tag the plugin may register (`^[a-z0-9_-]{1,64}$`); needs `content:filter` |
| `assets` | filled in by `wpgo-pkg pack` | path → hex SHA-256 of every file under `assets/`. Do not write it by hand; see [assets](assets.md) |

The Hub reads the same file and additionally understands `tested_core`, `author_url` and
`homepage` as aliases shown on the marketplace listing ([publishing](publishing.md)).

## Validation

`wpgo-pkg pack` and the site's installer both run the validation and report every problem at
once, for example:

```text
plugin.json has 2 problems:
  - hook "the_content" requires the "content:filter" permission; add it to "permissions"
  - unknown permission "posts:all" (known: admin:notices, admin:scripts, admin:settings, content:filter, content:html, cron, forms:access, headless:entries, http:outbound, options:read, options:write, posts:read, posts:write, rest:routes, storage, users:read)
```

## What the administrator sees

The activation screen lists `permissions` with a one-line description each and lets the
administrator grant a subset. The plugin receives the intersection of what it declared and what
was granted; a call that needs more returns a `permission_denied` error to the plugin and is
logged, but does not count as a failure.
