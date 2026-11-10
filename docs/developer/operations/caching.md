---
title: Caching (Pursae Cache)
description: The built-in full-page cache, its settings, tags and purging, and how to pair it with nginx, LiteSpeed or a CDN.
weight: 50
section: operations
---

**Pursae Cache** is a built-in plugin (on by default) that caches rendered public pages inside the
Go process, purges them precisely by tags when content changes, and can tell a front proxy or CDN
to cache them as well. It is modelled on LiteSpeed Cache but works with any server.

Find it under **Pursae Cache** in the admin menu (Dashboard, Cache, Page Optimization, Toolbox) and
switch it off or on under Plugins.

## What is cached

GET requests of the public site from visitors who are not logged in and carry no comment-author
cookie. Pages that set cookies, send `Cache-Control: private`/`no-store`, or that the app marks
with `X-WPGo-Cache-Control: no-cache` (forms with nonces) are never cached. Responses carry
`X-WPGo-Cache: hit|miss|bypass`.

## Settings (Pursae Cache › Cache)

| Option | Default | Meaning |
|--------|---------|---------|
| `wpgo_cache_enabled` | on | the plugin |
| `wpgo_cache_ttl_public` | 7 days | TTL of posts, pages and archives |
| `wpgo_cache_ttl_front` | 7 days | TTL of the front page |
| `wpgo_cache_ttl_feed` | 7 days | TTL of feeds |
| `wpgo_cache_ttl_404` | 1 hour | TTL of not-found pages |
| `wpgo_cache_mobile` | off | separate cache entries for mobile user agents |
| `wpgo_cache_logged_in` | off | cache for logged-in users too (per user) |
| `wpgo_cache_exclude_uri` | — | one path prefix per line that is never cached |
| `wpgo_cache_drop_qs` | — | query parameters ignored for the cache key (tracking parameters) |
| `wpgo_cache_disk` | off | persist entries to disk so they survive a restart |
| `wpgo_cache_server` | `auto` | which proxy headers to emit: `auto`, `litespeed`, `nginx`, `generic`, `none` |

Page Optimization (`wpgo_opt_*`): lazy-load images (skipping the first ones above the fold),
defer scripts (with an exclusion list), minify HTML, preload the theme's fonts.

## Tags and purging

Every cached page is tagged (`home`, `post:12`, `term:3`, `author:1`, `media:7`, `feed`,
`date:2026-10`, `archive:post`, `404`, `search`). Publishing or editing a post purges its own
page, the home page, feeds, its author and term archives and the dates it belongs to; comments purge the post; editing or deleting a term purges
the pages listing or linking it (deleting a category also its posts' new default category);
editing or deleting a media item purges the pages showing it as a featured image; settings,
theme, menu and widget changes purge everything. With several instances on one database every
purge reaches all of them ([several instances](reverse-proxy-and-tls.md#several-instances)). Plugins that change posts through the
[posts API](../plugins/host-api.md) trigger the same purges, and saving a plugin settings page
purges everything.

A page whose render overlaps a purge that covers it (a post saved while the page was being
built) is sent to that visitor but not stored, so the purge always wins. Concurrent misses of the
same anonymous page are rendered once: the other requests wait (up to 10 s) and are then served
from the cache; when that render was not cacheable they render the page themselves.

The admin bar has **Purge this page** and **Purge all**; Toolbox has a crawler that warms the
cache after a purge.

## Front proxies

| `wpgo_cache_server` | Headers emitted |
|---------------------|-----------------|
| `litespeed` | `X-LiteSpeed-Cache-Control`, `X-LiteSpeed-Tag`, and `X-LiteSpeed-Purge` on admin responses |
| `nginx` | `X-Accel-Expires` for `proxy_cache` / `fastcgi_cache` |
| `generic` | `Cache-Control: s-maxage`, `Surrogate-Key` and `Cache-Tag` (Varnish, Fastly, Cloudflare) |
| `auto` | LiteSpeed headers only when the request came through LiteSpeed; nothing otherwise |
| `none` | in-process cache only |

A CDN in front of the site should honour `Cache-Control` and purge by `Cache-Tag` /
`Surrogate-Key` when it receives the purge; without tag support, set a short `s-maxage`.
