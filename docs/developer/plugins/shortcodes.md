---
title: Shortcodes
description: Register [shortcodes] from a plugin, read attributes and content, and how the core expands them.
weight: 60
section: plugins
---

Shortcodes follow WordPress syntax and are expanded in `the_content` after blocks are rendered
and paragraphs added, before the `the_content` filters run (the same place as `do_shortcode` at
priority 11 in WordPress).

```text
[gopher-facts]                       self-closing
[gopher-facts count="3" style=plain] attributes: quoted or bare, names are lower-cased
[box title="Note"]Some *text*[/box]  enclosing: the content is passed to the callback
[[gopher-facts]]                     escaped: rendered as the literal text [gopher-facts]
```

Only tags registered by an active plugin are recognised; anything else stays as written. A
paragraph that contains nothing but a shortcode loses its `<p>` wrapper (`shortcode_unautop`).

## Register one

List the tag in `plugin.json` under `shortcodes` (needs the `content:filter` permission), then:

```go
func init() {
	wpgo.AddShortcode("box", func(ctx wpgo.Context, attrs map[string]string, content string) string {
		title := attrs["title"]
		if title == "" {
			title = "Note"
		}
		return `<div class="box"><strong>` + html.EscapeString(title) + `</strong>` + content + `</div>`
	})
}
```

- `attrs` holds the named attributes plus positional ones under `"0"`, `"1"`, … (`[box "A" b]`
  gives `{"0": "A", "1": "b"}`).
- `content` is the raw text between the tags, already autop'd by the core. Expand nested
  shortcodes yourself if you need them: the core does not recurse.
- The return value replaces the whole shortcode. Escape anything you did not generate.
- The core passes the return value through KSES (`wp_kses_post`: no `<script>`, `<iframe>`,
  `<form>`, event handlers or `javascript:` URLs) unless the plugin holds `content:html`, which the
  activation screen presents as full control of the site.
- `ctx.Hook` is the tag, so one callback can serve several tags.

A callback that panics or times out makes the shortcode disappear from the page (an empty
string), like a WordPress callback that returned nothing, and is logged.

## Reading settings and the user

Shortcodes run with the visitor's request, so `wpgo.CurrentUser()` (permission `users:read`)
tells you who is reading, and `wpgo.SettingString("count", "3")` reads the plugin's settings
page ([settings pages](settings-pages.md)).

## Where shortcodes work

Post and page content on the site, including the Shortcode block and classic content. They are
not expanded in excerpts, widget text or the REST API's `content.rendered`.

## Testing

Outside the host, `wpgo.Dispatch` runs the callback with the JSON the host would send
([testing](testing.md)):

```go
out := wpgo.Dispatch([]byte(`{"callback":"shortcode:box#1","hook":"box","type":"shortcode","args":[{"title":"Hi"},"body"]}`))
```
