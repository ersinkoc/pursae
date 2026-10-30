---
title: Plugin routes
description: How routes registered by plugins appear to API clients.
weight: 40
section: rest-api
---

Plugins add endpoints under their own namespace: `/wp-json/<slug>/v1/…` (the version number may
differ). They are listed in the index (`/wp-json/`) under `routes` and `namespaces` while the
plugin is active, and disappear when it is deactivated or disabled.

```bash
curl -s https://blog.example.com/wp-json/ | jq '.namespaces'
# ["batch/v1", "oembed/1.0", "shortcode-demo/v1", "wp-block-editor/v1", "wp/v2"]
curl -s https://blog.example.com/wp-json/shortcode-demo/v1/facts/1
# {"id":1,"fact":"Gophers can dig a tunnel of 60 cm per hour, which is slower than Go compiles."}
```

## Behaviour

- Core routes are matched first, so a plugin can never take over a `wp/v2` path.
- Authentication is the core's ([authentication](authentication.md)); the plugin receives the
  authenticated user, if any, and applies its own permission checks. Expect WordPress-style error
  bodies (`rest_forbidden`, `rest_invalid_param`) when a plugin follows the SDK's helpers.
- `OPTIONS` returns the allowed methods; a method the route does not accept is `404 rest_no_route`.
- Request bodies are limited to 1 MiB; JSON is decoded for the plugin, other content types are
  passed as text.
- A plugin that fails while handling a request answers `500 rest_plugin_error`; after repeated
  failures the plugin is disabled and the route returns `404` until an administrator re-activates
  it.
- Responses are JSON (`application/json; charset=UTF-8`) with the status and extra headers the
  plugin set. `_fields` and `_envelope` do not apply to plugin routes.

Writing one is covered in [REST routes](../plugins/rest-routes.md) of the plugin guide.
