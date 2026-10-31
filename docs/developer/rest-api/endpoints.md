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
| `/posts/{id}`, `/pages/{id}`, `/blocks/{id}` | GET, POST, DELETE | `?force=true` deletes instead of trashing |
| `/posts/{id}/autosaves`, `/pages/{id}/autosaves`, `/blocks/{id}/autosaves` | GET, POST | the editor's autosave |
| `/posts/{parent}/autosaves/{id}`, … | GET | |
| `/posts/{id}/revisions`, `/pages/{id}/revisions`, … | GET | |

## Media

| Route | Methods | Notes |
|-------|---------|-------|
| `/media` | GET, POST | upload with `multipart/form-data` (`file`) or a raw body with `Content-Disposition: attachment; filename=…`; image sizes are generated |
| `/media/{id}` | GET, POST, DELETE | `title`, `caption`, `alt_text`, `description`, `post` |
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
| `/block-patterns/patterns`, `/block-patterns/categories` | GET | the core patterns |
| `/block-types`, `/block-directory/search`, `/pattern-directory/patterns`, `/wp_pattern_category` | GET | empty lists |
| `/templates`, `/templates/lookup`, `/template-parts` | GET | empty (no site editor) |
| `/global-styles/{id}`, `/global-styles/themes/{stylesheet}`, `/global-styles/themes/{stylesheet}/variations` | GET, POST | |

## Other namespaces

| Route | Methods | Notes |
|-------|---------|-------|
| `/` (`/wp-json/`) | GET | the index: site info, namespaces, every route with its methods |
| `/batch/v1` | POST | up to 25 requests in one call |
| `/oembed/1.0/proxy` | GET | `url=` — fetches the provider's oEmbed for the editor |
| `/wp-block-editor/v1/url-details` | GET | `url=` — title, icon, description of a link |
| `/elementor/v1/…` | various | Pursae Web Builder's editor API (global classes, variables, components, kit defaults, default styles, design-system sync); only while the builder is on |
| `/<plugin-slug>/v1/…` | as registered | [plugin routes](plugin-routes.md) |
