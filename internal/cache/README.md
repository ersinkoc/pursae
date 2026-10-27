# wpgo-cache

Full-page cache for WordPress Go, modeled on LiteSpeed Cache but server-agnostic:
pages are cached inside the Go process (memory LRU, optional disk persistence),
purged by tags, and annotated with headers so a front proxy can cache them too.

## Wiring

```go
c := cache.New(cfg, cache.Options{LoginCookie: auth.CookieName})
go c.RunJanitor(ctx, 10*time.Minute)         // drop expired entries

handler := c.Middleware(srv.Handler())        // wrap the whole public-site handler
```

Order of middleware, outermost first:

1. access log / recovery (so hits are logged)
2. **`c.Middleware`**
3. the app (security headers, routing, rendering)

Security headers set inside the app are captured with the page and replayed on hits.
Do not gzip inside the cache: responses with `Content-Encoding` are not cached. The cache
compresses once at store time and negotiates `Accept-Encoding` itself, so an outer
gzip layer is unnecessary (if one exists it must skip responses that already have
`Content-Encoding`).

## Response headers the app sets

| Header | Meaning |
| --- | --- |
| `X-WPGo-Cache-Tags: home,post:12,term:3,author:1,feed` | tags for purging (required for precise purges) |
| `X-WPGo-Cache-TTL: 300` | TTL override in seconds; `0` = don't cache |
| `X-WPGo-Cache-Control: no-cache` | don't cache this response (e.g. a page with a form nonce) |

All three are stripped before the response leaves the process. Any `Set-Cookie`,
`Cache-Control: private/no-store/no-cache` or `Content-Encoding` also prevents caching.
The cache adds `X-WPGo-Cache: hit|miss|bypass`.

Suggested tags: `home` (front page and blog index), `post:ID`, `term:ID`, `author:ID`,
`feed` (all feeds), `date:2026`, `date:2026-10`, `date:2026-10-27`, `archive:<post_type>`,
`404` (on not-found pages), `search` (search results).

## Purging

| Event | Call |
| --- | --- |
| post published/updated/trashed/unpublished | `PurgeTags("post:ID", "home", "feed", "author:ID", "term:A", "term:B", …old and new terms, "date:YYYY", "date:YYYY-MM", "date:YYYY-MM-DD", "archive:TYPE", "404", "search")` |
| comment approved/unapproved/edited/deleted | `PurgeTags("post:ID")` (plus `"feed"` if comment feeds are tagged `feed`) |
| term renamed/edited/deleted | `PurgeTags("term:ID")` |
| user display name / avatar changed | `PurgeTags("author:ID")` |
| settings, theme, menus, widgets, permalinks | `PurgeAll()` |
| single URL (admin bar "purge this page") | `PurgeURL(permalink)` |

`404` is purged on publish because the new URL may have been cached as a 404.

With a LiteSpeed front server, forward the same purge on the admin response:

```go
if v := c.PurgeHeader(tags...); v != "" { w.Header().Set("X-LiteSpeed-Purge", v) }
```

## Warmup

```go
prog, err := c.Warm(ctx, srv.Handler(), sitemapURLs, cache.WarmupOptions{Concurrency: 4, RatePerSecond: 10})
```

Requests run in-process (no network). `cache.Warmup` with `BaseURL` and no `Handler`
crawls over HTTP instead.
