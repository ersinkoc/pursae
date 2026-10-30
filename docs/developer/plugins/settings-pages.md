---
title: Settings pages
description: Declare a settings page the admin renders under Settings, and read the saved values.
weight: 80
section: plugins
---

A plugin cannot render admin HTML itself (there is no PHP and no template access from the
sandbox). Instead it **declares** a page — title, description, fields — and the core renders it
under **Settings › \<Menu title\>** with the WordPress form styles, validates the input, saves
each field as an option and shows "Settings saved." Permission: `admin:settings` to register,
`options:read` to read the values.

```go
func init() {
	wpgo.RegisterSettingsPage(wpgo.SettingsPage{
		PageTitle:   "Gopher Facts",
		MenuTitle:   "Gopher Facts",
		Description: "Options of the Gopher Facts plugin.",
		Fields: []wpgo.SettingsField{
			{ID: "heading", Label: "Heading", Type: "text", Default: "Did you know?", Placeholder: "Shown above the list"},
			{ID: "count", Label: "Facts per page", Type: "number", Default: 3, Min: ptr(1), Max: ptr(10)},
			{ID: "greet", Label: "Greet logged-in readers", Type: "checkbox", Default: true,
				Description: "Adds a personal greeting."},
			{ID: "style", Label: "List style", Type: "select", Default: "cards",
				Options: []wpgo.SettingsOption{{Value: "cards", Label: "Cards"}, {Value: "plain", Label: "Plain list"}}},
			{ID: "footer", Label: "Footer note", Type: "textarea", Rows: 3},
		},
	})
}
```

## Fields

| Type | Saved as | Extra |
|------|----------|-------|
| `text` | string | `Placeholder` |
| `textarea` | string (CRLF normalised, ≤64 KiB) | `Rows`, `Placeholder` |
| `number` | JSON number | `Min`, `Max` (clamped on save), `Placeholder` |
| `checkbox` | `true` / `false` | the `Description` is the label next to the box |
| `select` | string; values not in `Options` are ignored | `Options` (1–100) |

Rules: `ID` is `^[a-z0-9_]{1,64}$` and unique; 1–50 fields per page; labels and placeholders
≤200 characters, descriptions ≤2000. Everything is escaped when rendered — HTML in labels is
shown as text.

## Where values live

Each field is the option `plugin_<slug>_<id>`, dashes in the slug replaced by underscores:
`plugin_shortcode_demo_heading`. Only the owning plugin can read and write them (`get_option` /
`update_option` allow the prefix), and they are hidden from other plugins.

Read them with the helpers, which know the prefix once the page is registered:

```go
heading := wpgo.SettingString("heading", "Did you know?")
count   := int(wpgo.SettingFloat("count", 3))
greet   := wpgo.SettingBool("greet", true)
name    := wpgo.SettingName("heading") // "plugin_shortcode_demo_heading"
```

Unsaved fields return the `Default` you gave (the helpers fall back to the `def` argument). Saving
the page purges the page cache, so shortcodes and filters pick up the new values at once.

## What you do not get

Custom HTML, buttons that call the plugin, file uploads, or pages outside Settings. For an
action-style screen use a REST route plus a script enqueued in the admin
([assets](assets.md)).
