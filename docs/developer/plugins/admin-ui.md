---
title: Admin pages, meta boxes, blocks and widgets
description: Add admin pages, Dashboard widgets, post meta boxes, Pursae Editor blocks and Pursae Web Builder widgets from a plugin.
weight: 85
section: plugins
---

Beyond [settings pages](settings-pages.md) a plugin can add five kinds of UI. Each has its own
permission, so the consent screen says exactly what the plugin will add. The plugin returns HTML
from a callback; the core decides where it goes, wraps it in the WordPress markup and **sanitises
it**: admin output keeps form controls but loses scripts, event handlers and iframes
(`wp.KsesAdmin`) unless the plugin holds `admin:scripts`; front-end output (blocks, widgets) goes
through KSES like a shortcode unless the plugin holds `content:html`.

The full example is `examples/plugins/ui-demo` (built by `tools/build-plugins.sh`).

| What | SDK | Host import | Permission |
|------|-----|-------------|------------|
| a page in the admin menu | `RegisterAdminPage(AdminPage{…})` | `register_admin_page` | `admin:pages` |
| a box on the Dashboard | `RegisterDashboardWidget(id, title, cap, fn)` | `register_dashboard_widget` | `admin:dashboard` |
| a box on the post edit screen | `RegisterMetaBox(MetaBox{…})` | `register_meta_box` | `admin:metaboxes` |
| a block for Pursae Editor | `RegisterBlock(Block{…})` | `register_block` | `blocks:register` |
| a Pursae Web Builder widget | `RegisterBuilderWidget(BuilderWidget{…})` | `register_builder_widget` | `builder:widgets` |

Registrations are kept per plugin and re-sent automatically after the instance is re-created,
like hooks and shortcodes. Limits: 20 admin pages, 10 Dashboard widgets, 20 meta boxes, 50 blocks,
50 Web Builder widgets; a render callback may return 1 MiB of HTML and receives at most 256 KiB
of input. Admin pages get the REST budget (10 s), the others the content budget (2 s).

## Admin pages

```go
wpgo.RegisterAdminPage(wpgo.AdminPage{
	Slug: "", PageTitle: "Gopher Board", MenuTitle: "Gopher Board",
	Parent: "tools", // "" = top-level, "self" = under the plugin's main page, or a core menu
	Render: func(ctx wpgo.Context, req wpgo.AdminRequest) wpgo.AdminResponse {
		if req.Method == "POST" { /* req.Form["message"] … */ }
		return wpgo.AdminResponse{HTML: `<input name="message"> <button class="button button-primary">Save</button>`,
			Notices: []wpgo.PageNotice{{Type: "success", Message: "Saved."}}}
	},
})
```

- URL: `/wp-admin/admin?page=<plugin slug>` for the main page (`Slug: ""`), `…?page=<plugin slug>.<Slug>` for others.
- `Parent`: `""` (a top-level menu with `Icon`, a `dashicons-*` class), `self` (a sub page of the plugin's main
  page, which then becomes the first sub item) or one of `dashboard`, `posts`, `media`, `pages`, `comments`,
  `appearance`, `plugins`, `users`, `tools`, `settings`.
- `Capability` (default `manage_options`) is checked for the menu entry and the page.
- The core prints `<h1>`, the notices and a `<form method="post">` with a nonce around your HTML, so
  inputs and a submit button post back to the same URL. A POST reaches the plugin only after the
  nonce check; `req.Form` and `req.Query` carry the first value of each field.
- Notices are plain text (types `info`, `success`, `warning`, `error`, at most 10).

## Dashboard widgets

```go
wpgo.RegisterDashboardWidget("board", "Gopher Board", "", func(ctx wpgo.Context, req wpgo.AdminRequest) string {
	return "<p>Hello from the plugin.</p>"
})
```

The box id is `plugin_<slug>_<id>` (dashes as underscores); it starts in the left column and can
be dragged, collapsed and reordered like the core boxes. `capability` ("" = `read`) limits who sees it.

## Meta boxes

```go
wpgo.RegisterMetaBox(wpgo.MetaBox{
	ID: "notes", Title: "Gopher Notes", Context: "side", PostTypes: []string{"post"},
	Fields: []wpgo.SettingsField{{ID: "subtitle", Label: "Subtitle", Type: "text"}},
	Render: func(ctx wpgo.Context, req wpgo.AdminRequest) string { return "<p>Shown above the fields.</p>" },
	OnSave: func(ctx wpgo.Context, postID int64, values map[string]any) { /* after the fields were stored */ },
})
```

- Shown on the classic editor and in Pursae Editor (its meta box area below the content or in the
  sidebar), for `post` and `page` by default, to users who may edit the post.
- The core renders the `Fields` (the [field types](settings-pages.md#fields)) and stores each as
  post meta `plugin_<slug>_<field id>` (dashes as underscores), validated like a settings page.
  Read it back with `GetPost(id).Meta` (`posts:read`). Pursae Editor saves the boxes after the
  post itself, as WordPress does.
- `Render` output is sanitised **without** form controls even in the admin (KSES), so a box can
  never add inputs to the post form; fields are the only way to collect input.
- `req.Values` holds the stored field values; `OnSave` receives the post id and the values by field id.

## Blocks

```go
wpgo.RegisterBlock(wpgo.Block{
	Name: "badge", Title: "Gopher Badge", Category: "widgets", Icon: "awards",
	Attributes: []wpgo.SettingsField{{ID: "text", Label: "Text", Type: "text", Default: "Gopher"}},
	Render: func(ctx wpgo.Context, attrs map[string]any, content string) string { return "<span>…</span>" },
})
```

- The block name is `<plugin slug>/<name>`; the slug is added when you leave it out.
- In Pursae Editor the block shows the `Attributes` as inspector controls (text, textarea, number,
  checkbox, select, radio, email, url, color, date) and a server-rendered preview
  (`/wp/v2/block-renderer/<name>`). It is a dynamic block: only the attributes are saved in the
  post, the HTML is rendered for every view (and cached by Pursae Cache with the page).
- `attrs` holds the declared attributes (defaults filled in) plus `className`, `align` and
  `anchor`; anything else in the saved comment is dropped. When the plugin is inactive the block
  renders nothing.

## Web Builder widgets

```go
wpgo.RegisterBuilderWidget(wpgo.BuilderWidget{ID: "badge", Title: "Gopher Badge", Controls: fields, Render: badge})
```

The widget type is `<plugin slug>/<ID>` (`ID`: lowercase letters, digits and `-`). The core
exposes the registered widgets to the builder through
`GET /wp-json/web-builder/v2/plugin-widgets` (type, title, description, icon and the `controls`
schema) and renders one with `POST /wp-json/web-builder/v2/plugin-widgets/render`
(`{"type", "settings"}` → `{"rendered"}`); both need `edit_posts`. `settings` holds the declared
controls only, with defaults. **The builder's widget panel does not list plugin widgets yet**:
the editor side (a builder patch and a contract slot for published pages) is pending, so today
these endpoints are what the widget is reachable through.

## Testing

`wpgotest` records the registrations and runs their callbacks:

```go
h := wpgotest.New(t)
wpgo.Init()
resp, _ := h.RenderAdminPage("", wpgo.AdminRequest{Method: "POST", Form: map[string]string{"message": "hi"}})
html, _ := h.RenderBlock("badge", map[string]any{"text": "Go"}, "")
h.Registered("register_meta_box") // what was sent to the host
```
