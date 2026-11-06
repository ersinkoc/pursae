---
title: REST API overview
description: The wp/v2 API WordPress clients expect, where it lives and how it differs.
weight: 10
section: rest-api
---

Pursae serves the WordPress REST API at `/wp-json/` (and `?rest_route=/…`). The `wp/v2`
namespace mirrors WordPress closely enough that the block editor, mobile apps and headless
clients written against WordPress work unchanged.

```bash
curl -s https://blog.example.com/wp-json/ | jq .namespaces
curl -s 'https://blog.example.com/wp-json/wp/v2/posts?per_page=2&_fields=id,title,link'
```

## Conventions

- JSON in and out; `Content-Type: application/json` bodies, or form bodies and query parameters.
- Collections paginate with `page`, `per_page` and return `X-WP-Total` and `X-WP-TotalPages`
  headers plus `Link: rel="next"/"prev"`.
- `_fields=id,title.rendered` trims responses, `_embed` inlines linked resources, `_envelope`
  wraps the body with status and headers.
- Method overrides: `X-HTTP-Method-Override` on a POST, or `?_method=DELETE`. `PUT`/`PATCH`
  fall back to the `POST` handler of a route; `HEAD` to `GET`.
- Errors are `{"code": "rest_no_route", "message": "…", "data": {"status": 404}}`.
- `OPTIONS` on a route answers with its methods and `Allow`, which the editor uses for
  `canUser()` checks.
- Every response sets `X-Content-Type-Options: nosniff` and the `Link: <…/wp-json/>; rel="https://api.w.org/"`
  discovery header; `/` and `<head>` carry the same link.

## Namespaces

| Namespace | Contents |
|-----------|----------|
| `wp/v2` | posts, pages, blocks, media, categories, tags, users, comments, settings, types, taxonomies, statuses, themes, search, patterns, sidebars, widgets, block renderer, global styles |
| `wp-block-editor/v1` | URL details for the editor's link preview |
| `oembed/1.0` | the oEmbed proxy the editor uses for embeds |
| `batch/v1` | batched requests (the editor's widget screen) |
| `web-builder/v2` | Pursae Web Builder: documents, autosave, publish, revisions, saved sections, fonts and the public form endpoint (only while the builder is on; [contract](../web-builder-contract.md#3-rest-namespace-web-builderv2)) |
| `<plugin-slug>/v1` | routes added by plugins ([plugin routes](plugin-routes.md)) |

The full list with methods is on the [endpoints](endpoints.md) page. Authentication —
cookie + nonce and Application Passwords — is on [authentication](authentication.md).

## Differences from WordPress

- No `wp-site-health/v1`, `wp/v2/plugins`, `wp/v2/templates` (returns an empty list) or
  `wp/v2/font-families` yet.
- Block types, pattern directory and block directory searches return empty lists; the editor
  ships its block types client-side.
- The index advertises `application-passwords` authentication; the admin screen to create them
  is not in this version ([authentication](authentication.md)).
