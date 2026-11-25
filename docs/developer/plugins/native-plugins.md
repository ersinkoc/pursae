---
title: Native plugins
description: What native plugins are, why almost every plugin should be a Wasm plugin instead, how to build a native one in your own fork, and how to propose it for the core.
weight: 135
section: plugins
---

A **native plugin** is Go code compiled into the Pursae binary. It has no sandbox and no permission
prompt: it reads the database, the settings and the request directly, like the rest of the core.
Pursae CDN, Pursae Cache, Pursae SMTP and Classic Editor are native plugins.

## Native or Wasm

| | Wasm plugin | Native plugin |
|---|---|---|
| Where it lives | a signed zip in `<data>/plugins` | the core's source tree |
| Install | upload, or from the [Hub](../hub/index.md) | ships in a core release |
| Access | host API behind [permissions](permissions.md) | everything the core can do |
| Isolation | sandbox, time and memory [limits](limits-and-sandbox.md) | none: a bug is a core bug |
| Updates | upload a new version | a new core release |

**Choose Wasm.** It covers hooks, shortcodes, REST routes, settings pages, admin pages, scheduled
events and assets, installs without rebuilding anything, and cannot take the site down. Choose native
only when the feature needs what the host API does not offer: new database tables, a new
admin-wide screen, or hooks into the request pipeline before routing.

## Anatomy of a native plugin

Every native plugin follows the same pattern (Pursae CDN is the reference: `internal/server/gocdn.go`):

1. A boolean option `native_<slug>`, **off by default**.
2. `xOn()` reads that option.
3. `xPluginRow()` returns the row shown on the Plugins screen, with `Native: true`.
4. Routes and screens answer **404 while the plugin is off**, and nothing of it touches a response.
5. An activate / deactivate handler flips the option (and purges the page cache when output changes).
6. An admin menu entry, shown only while active.
7. Templates under `web/admin/templates/pages`.
8. A feature inventory, `docs/<name>-features.md`.

## Building one in your fork

Fork [github.com/pursae/pursae](https://github.com/pursae/pursae), then touch these places:

| File | Change |
|---|---|
| `internal/server/<name>.go` | option, `xOn()`, `xPluginRow()`, handlers, routes |
| `internal/server/plugins.go` | append `s.xPluginRow()` in `nativePluginRows()` |
| `internal/server/admin.go` | add the admin menu entry |
| `web/admin/templates/pages/<name>.html` | the screens |
| `docs/<name>-features.md` | what the plugin does |

A minimal skeleton:

```go
const (
	hiSlug    = "hello-native"
	hiSetting = "native_hello_native" // off by default
)

func (s *Server) hiOn() bool { return s.settings.Bool(hiSetting) }

func (s *Server) hiPluginRow() pluginRow {
	return pluginRow{
		Slug: hiSlug, Name: "Hello Native", Version: builtinPluginVersion,
		Native: true, Active: s.hiOn(), Author: "You",
		Description: "Adds a hello screen under Tools.",
	}
}

func (s *Server) hiOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.hiOn() {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}

func (s *Server) hiPluginAction(c *gin.Context, action string) {
	_ = s.settings.Set(c, map[string]any{hiSetting: action == "activate"})
	c.Redirect(http.StatusFound, "/wp-admin/plugins?"+action+"=true")
}
```

Register the routes with `hiOnly()` as in `registerGoCDN`, append `s.hiPluginRow()` to
`nativePluginRows()`, and wire `hiPluginAction` where the other native plugins' actions are
dispatched. Read `gocdn.go` for the full pattern, and run `make test` before you go on.

Running your own build means you update it yourself: Dashboard › Updates replaces the binary with
the official one, which does not contain your plugin. Native plugins are meant to be proposed for
the core.

## Proposing it for the core

1. Open a topic in the [Pursae forum](https://pursae.com/forum/) describing the plugin and why it
   cannot be a Wasm plugin.
2. Email hello@pursae.com with a link to your source and a short description.
3. Open a pull request on [github.com/pursae/pursae](https://github.com/pursae/pursae) with the
   code, tests and `docs/<name>-features.md`.

The core's rules apply: the plugin is off by default, adds nothing while off, keeps security
measures on by default and passes `make test`. After review it ships in a core release and appears
on every site's Plugins screen. See also [publishing](publishing.md) for Wasm plugins.
