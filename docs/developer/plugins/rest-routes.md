---
title: REST routes
description: Add endpoints under /wp-json/<slug>/v1/, handle methods, parameters, bodies and authentication.
weight: 70
section: plugins
---

With the `rest:routes` permission a plugin registers routes under its own namespace. The core
authenticates the request like any other REST request (cookie + `X-WP-Nonce`, or an Application
Password) and hands the plugin a `RestRequest`; the plugin returns a status, a JSON body and
optional headers.

## Namespace and route

- The namespace must be the plugin slug or `<slug>/v<N>`: `shortcode-demo/v1`. Plugins can never
  shadow core routes: the core's own routes are matched first, and a foreign namespace is refused.
- The route is a path under the namespace starting with `/`. It is a regular expression anchored
  at both ends, with named groups for parameters — the same convention as WordPress:
  `/items/(?P<id>\d+)`.
- Methods: any of `GET, POST, PUT, PATCH, DELETE` (`GET` when empty). `HEAD` is served by the
  `GET` handler; `OPTIONS` answers with the allowed methods.

```go
func init() {
	wpgo.RegisterRestRoute("shortcode-demo/v1", "/facts", []string{"GET", "POST"}, facts)
	wpgo.RegisterRestRoute("shortcode-demo/v1", `/facts/(?P<id>\d+)`, nil, fact)
}

func facts(_ wpgo.Context, req *wpgo.RestRequest) *wpgo.RestResponse {
	if req.Method == "GET" {
		return wpgo.JSON(200, map[string]any{"facts": allFacts()})
	}
	if !req.User.Can("edit_posts") { // nil user → false
		return wpgo.RestError(403, "rest_forbidden", "Sorry, you are not allowed to add facts.")
	}
	var in struct{ Fact string `json:"fact"` }
	if err := req.BindJSON(&in); err != nil || in.Fact == "" {
		return wpgo.RestError(400, "rest_invalid_param", "fact is required")
	}
	// …store it…
	return wpgo.JSON(201, map[string]any{"fact": in.Fact})
}
```

## The request

| Field | Content |
|-------|---------|
| `Method` | `GET`, `POST`, … after `X-HTTP-Method-Override` / `_method` handling |
| `Route` | the matched path, e.g. `/shortcode-demo/v1/facts/7` |
| `Params` | the named groups; `req.Param("id")` |
| `Query` | URL query parameters, first value each |
| `Headers` | `Accept`, `Content-Type`, `Accept-Language`, `User-Agent`, `Referer`, `Origin`, `X-Requested-With` |
| `Body` | the JSON body (`req.BindJSON(&v)`), or a JSON string for other content types; at most 1 MiB |
| `User` | `{ID, Login, DisplayName, Roles, Caps}` or `nil` for anonymous requests |

## The response

`wpgo.JSON(status, v)` encodes `v`; `wpgo.RestError(status, code, message)` builds the WordPress
error shape `{"code":…,"message":…,"data":{"status":…}}`. Set `Headers` for extra response
headers. Only these are sent: `Cache-Control`, `Expires`, `ETag`, `Last-Modified`, `Vary`, `Link`,
`Retry-After`, `Content-Language`, `Content-Disposition`, `X-WP-Total`, `X-WP-TotalPages`,
`Location` (a same-site path starting with `/`) and the plugin's own `X-<Slug>-*` headers; any
other header (cookies, CORS, CSP, `Content-Type`, …) is dropped and logged once in the plugin's
log. Bodies are limited to 1 MiB; a handler
that panics, times out (10 s) or returns something malformed yields `500 rest_plugin_error`.

## Authentication and permissions

There is no `permission_callback`: check `req.User` yourself. `req.User.Can("edit_posts")` is
safe on a nil user. Anonymous requests have `User == nil`; a cookie without a nonce is anonymous
too, exactly as in WordPress ([REST authentication](../rest-api/authentication.md)).

```bash
curl http://localhost:8088/wp-json/shortcode-demo/v1/facts
curl -u admin:'xxxx xxxx xxxx xxxx xxxx xxxx' -H 'Content-Type: application/json' \
     -d '{"fact":"Gophers read JSON."}' http://localhost:8088/wp-json/shortcode-demo/v1/facts
```

Plugin routes are listed under their namespace in `/wp-json/` and are only served while the
plugin is active. See also [plugin routes](../rest-api/plugin-routes.md) from the API side.

## Webhook receivers and non-JSON responses

- `RegisterRestRouteWithHeaders` asks for extra request headers (for example `Stripe-Signature`,
  `X-Hub-Signature-256`); they appear in `req.Headers`. At most 16 per route; `Cookie`,
  `Authorization` and `Proxy-Authorization` are refused.
- `req.RawBodyBytes()` returns the unmodified body (the wire field `raw_body` is base64) so an HMAC can
  be verified over the exact bytes. `req.QueryAll` lists every value of a parameter sent more than once
  (`req.Query` keeps the first).
- `wpgo.Text(status, contentType, text)` answers with CSV, XML, a feed or HTML instead of JSON. Allowed
  types: `text/plain`, `text/csv`, `text/html`, `text/xml`, `application/xml`, `application/rss+xml`,
  `application/atom+xml`. Responses are sent with `X-Content-Type-Options: nosniff`; HTML is sandboxed by a
  `Content-Security-Policy: sandbox` header.
