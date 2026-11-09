---
title: Endpoints
description: Every route the core registers, one line each.
weight: 30
section: rest-api
---

Routes are regular expressions; `{id}` stands for `(?P<id>[\d]+)`. Methods are the registered
ones; `HEAD` works wherever `GET` does and `PUT`/`PATCH` wherever `POST` does.

## Posts, pages, blocks (`wp/v2`)

| Route | Methods | Notes |
|-------|---------|-------|
| `/posts`, `/pages`, `/blocks` | GET, POST | `page`, `per_page`, `search`, `author`, `categories`, `tags`, `status`, `orderby`, `order`, `slug`, `include`, `exclude`, `parent`, `sticky`, `_embed`, `_fields`; `context=edit` for raw content |
| `/posts/{id}`, `/pages/{id}`, `/blocks/{id}` | GET, POST, DELETE | `?force=true` deletes instead of trashing. Pages read and write `template` (`_wp_page_template`): `""` is the default template, otherwise one of the theme's page templates (a package's `page_templates`) or the page's current template; another value is a 400 |
| `/posts/{id}/autosaves`, `/pages/{id}/autosaves`, `/blocks/{id}/autosaves` | GET, POST | the editor's autosave |
| `/posts/{parent}/autosaves/{id}`, … | GET | |
| `/posts/{id}/revisions`, `/pages/{id}/revisions`, … | GET | |

With Pursae Multi Language on, posts, pages, categories and tags (and the translated custom types and
taxonomies) carry `lang` and `translations` (`{"en": 12, "fr": 15}`). Collections take `?lang=fr` (that
language only; objects without a language count as the default language). Writes take `lang` and
`translations` in the JSON or form body, as Polylang's REST fields, or in the query string
(`?lang=fr&translations[en]=12`); the body wins. A translation link is only made to an object in that
language, of the same type, that the user may edit.

## Media

| Route | Methods | Notes |
|-------|---------|-------|
| `/media` | GET, POST | upload with `multipart/form-data` (`file`) or a raw body with `Content-Disposition: attachment; filename=…`; image sizes are generated (in the format of Settings › Media › Image output format) |
| `/media/{id}` | GET, POST, DELETE | `title`, `caption`, `alt_text`, `description`, `post`; with Pursae Multi Language and its Media module on: `?lang=` returns that language's texts, `context=edit` adds `pll_i18n` (per-language overrides) and updates accept it |
| `/media/{id}/post-process` | POST | |

## Terms

| Route | Methods | Notes |
|-------|---------|-------|
| `/categories`, `/tags` | GET, POST | `search`, `parent`, `post`, `slug`, `hide_empty`, `orderby` |
| `/categories/{id}`, `/tags/{id}` | GET, POST, DELETE | |
| `/taxonomies`, `/taxonomies/{taxonomy}` | GET | |

## Users and comments

| Route | Methods | Notes |
|-------|---------|-------|
| `/users` | GET | public profiles (`who=authors`), or all users with `list_users` |
| `/users/{id}` | GET | |
| `/users/me` | GET, POST | the current user; `context=edit` for the full record |
| `/comments` | GET, POST | `post`, `parent`, `status`, `author_email`; anonymous comments honour the Discussion settings |
| `/comments/{id}` | GET, POST, DELETE | |

## Site

| Route | Methods | Notes |
|-------|---------|-------|
| `/settings` | GET, POST | `title`, `description`, `timezone`, `date_format`, `posts_per_page`, `default_category`, `show_on_front`, `page_on_front`, … (`manage_options`) |
| `/types`, `/types/{type}` | GET | `post`, `page`, `attachment`, `wp_block`, … |
| `/statuses`, `/statuses/{status}` | GET | |
| `/themes`, `/themes/{stylesheet}` | GET | `status=active` returns the active theme with its `theme_supports` |
| `/search` | GET | `search`, `type`, `subtype` |
| `/sidebars`, `/sidebars/{id}` | GET, POST | widget areas |
| `/widgets`, `/widgets/{id}` | GET, POST, DELETE | |
| `/widget-types`, `/widget-types/{id}`, `/widget-types/{id}/encode`, `/widget-types/{id}/render` | GET, POST | |
| `/menu-locations` | GET | |
| `/block-renderer/{name}` | GET, POST | server-side rendering of dynamic blocks |
| `/block-patterns/patterns`, `/block-patterns/categories` | GET | core's patterns and the active theme's (Twenty Twenty-Five); users who can edit posts |
| `/block-types`, `/block-directory/search`, `/pattern-directory/patterns`, `/wp_pattern_category` | GET | empty lists |
| `/templates`, `/templates/lookup`, `/template-parts` | GET | empty (no site editor) |
| `/global-styles/{id}`, `/global-styles/themes/{stylesheet}`, `/global-styles/themes/{stylesheet}/variations` | GET, POST | |

## Site management (`pursae/v1`)

The routes AI agents and scripts use to run a site. Each checks the capability of its admin screen
(shown in brackets) and belongs to an [Application Password scope](authentication.md#application-password-scopes).

| Route | Methods | Notes |
|-------|---------|-------|
| `/pursae/v1/plugins` | GET, POST | list built-in and Wasm plugins (`activate_plugins`); `POST {"package": "<base64 zip>"}` installs a signed package, inactive (`install_plugins`) |
| `/pursae/v1/plugins/{slug}` | GET, DELETE | one plugin; `DELETE` removes an installed Wasm plugin (`delete_plugins`; built-in plugins only deactivate) |
| `/pursae/v1/plugins/{slug}/activate`, `/deactivate` | POST | the Plugins screen's own action; a Wasm plugin gets `permissions` (array; default: all its manifest asks for) |
| `/pursae/v1/themes` | GET, POST | list themes (`switch_themes`); `POST {"package": "<base64 zip>", "activate": true}` installs a [theme package](../themes/index.md) (`install_themes`) |
| `/pursae/v1/themes/{slug}/activate` | POST | switch theme |
| `/pursae/v1/themes/{slug}/files` | GET | editable files (`edit_themes`): `style.css`, asset paths, `templates/…` |
| `/pursae/v1/themes/{slug}/file?file=…` | GET, POST, DELETE | read; `POST {"file", "content"}` saves like the Theme File Editor (template parse + render check, rolled back on failure); `DELETE` restores the original. Refused while file editing is disabled (`WPGO_DISALLOW_FILE_EDIT` or Pursae Security) |
| `/pursae/v1/cache` | GET | Pursae Cache status, stats and crawler (`manage_options`) |
| `/pursae/v1/cache/purge` | POST | `{"all": true}`, `{"url": "/about/"}`, `{"post": 12}` or `{"tags": ["home"]}` |
| `/pursae/v1/cache/settings` | GET, POST | the `wpgo_cache_*` / `wpgo_opt_*` options (typed); saving rebuilds the cache |
| `/pursae/v1/seo/settings` | GET, POST | Pursae SEO site-wide settings; `POST {"settings": {…}}` merges a partial object |
| `/pursae/v1/seo/redirects` | GET, POST | list; create or update (`id`) with `source`, `target`, `code` (301, 302, 307, 410, 451), `match_type`, `status` |
| `/pursae/v1/seo/redirects/{id}` | DELETE | |
| `/pursae/v1/options` | GET | the option groups below (`manage_options`) |
| `/pursae/v1/options/{group}` | GET, POST | `gocdn`, `headless`, `maintenance`, `security`, `seo`, `smtp`: the built-in plugin's settings object. Secrets (passwords, keys, tokens) read as `********`; `POST {"settings": {…}}` deep-merges a partial object, and a `********` sent back keeps the stored secret |
| `/pursae/v1/mcp` | POST, GET, DELETE | the [MCP server](../operations/ai-mcp.md) (streamable HTTP) |

## Other namespaces

| Route | Methods | Notes |
|-------|---------|-------|
| `/` (`/wp-json/`) | GET | the index: site info, namespaces, every route with its methods |
| `/batch/v1` | POST | up to 25 requests in one call |
| `/oembed/1.0/proxy` | GET | `url=` — fetches the provider's oEmbed for the editor |
| `/wp-block-editor/v1/url-details` | GET | `url=` — title, icon, description of a link |
| `/pll/v1/languages`, `/pll/v1/languages/{id or code}`, `/pll/v1/settings` | GET, POST, DELETE | Pursae Multi Language (Polylang's routes); only while the plugin is on |
| `/pll/v1/media/{id}/i18n` | GET, POST | per-language `alt_text`, `title`, `caption`, `description` of a media item: `{"fr":{"alt_text":"…"}}`, null or blank falls back to the item; `upload_files` plus own item or `edit_others_posts`; 404 while the Media module is off |
| `/pll/v1/translate`, `/pll/v1/translate/test` | POST | machine translation proxy: `{texts[], target, source?, html?}` → `{translations, target}`; `manage_options`, cookie auth with the `wp_rest` nonce, 60 calls per user per minute, 200 texts / 200 KB, never saves |
| `/pll/v1/visual/strings` | POST | saves string translations `{lang, items:[{original, translation}]}` (visual translation); `manage_options` and the `wp_rest` nonce, ≤200 items |
| `/web-builder/v2/…` | various | Pursae Web Builder: `documents/{id}` (load, autosave, publish, lock, revisions), `library`, `prefs`, `fonts/css2` and the public `forms/{id}/{section}`; only while the builder is on. Requests and errors are in the [contract](../web-builder-contract.md#3-rest-namespace-web-builderv2) |
| `/<plugin-slug>/v1/…` | as registered | [plugin routes](plugin-routes.md) |
