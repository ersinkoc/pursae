# Pursae Custom Fields — feature inventory

Pursae Custom Fields is Pursae's native (compiled-in) clone of **Advanced Custom Fields PRO** 6.x. This file is
the inventory of what ACF PRO does, written from ACF's documentation (advancedcustomfields.com/resources),
the wordpress.org plugin page and the `AdvancedCustomFields/acf` source, and what Pursae Custom Fields does with each
item. Status: **✓** implemented, **◐** implemented in a reduced form (the note says how), **✗** left out
(the note says why).

Pursae Custom Fields is a native plugin like Classic Editor, Pursae Cache and Pursae Web Builder: it is listed on Plugins,
starts **off**, adds nothing to the site while off (no menu, screens, meta boxes, REST field, template
output, routes answer 404) and keeps every field group and saved value when deactivated.

## 1. Field groups

| ACF PRO | Pursae Custom Fields |
|---|---|
| Field groups stored as `acf-field-group` posts, fields as `acf-field` posts | ✓ field groups are `acf-field-group` posts (content type registered on activation); the group's JSON (ACF's export format, fields nested) is the post content. Fields are not separate posts — the export/import format is unchanged |
| List screen (ACF › Field Groups): Title, Description, Key, Location, Local JSON, Fields columns | ✓ |
| Views: All / Active / Inactive / Sync available / Trash | ✓ |
| Row actions: Edit, Duplicate, Trash; Restore, Delete Permanently in Trash | ✓ |
| Bulk actions: Activate, Deactivate, Move to Trash, Sync changes, Restore, Delete | ✓ |
| Search field groups | ✓ |
| Add / edit screen: title, field list with drag-and-drop ordering (also between sub field lists), add field, duplicate field, delete field | ✓ |
| "Move" a field to another field group (a modal in ACF) | ✗ export/import or re-create the field |
| Field settings in tabs: General, Validation, Presentation, Conditional Logic | ✓ |
| Group settings: Location Rules, Presentation, Group Settings tabs | ✓ |
| Active toggle, Show in REST API toggle, description, menu order | ✓ |
| Field group key `group_…`, field key `field_…` (unique ids) | ✓ (same 13-hex style keys) |
| Local JSON (save to/load from `acf-json` folders, Sync) | ✓ folder `<data>/acf-json` (next to uploads) and `acf-json/` in the active theme package; written on save when the folder exists; "Sync available" when the file is newer than the database or not imported |
| `acf_add_local_field_group()` (PHP) | ◐ registration from Go code (`acfgoRegisterLocalGroup`) and from local JSON; PHP does not exist in Pursae |
| Revisions of field values with post revisions | ✗ Pursae revisions keep title/content/excerpt only |

## 2. Field types (all with ACF's settings)

General settings on every field: Label, Name, Key, Type, Instructions, Required, Default value
(where it applies), Wrapper width / class / id, Conditional Logic, `aria-label`.

| Type | Settings | Status |
|---|---|---|
| Text | default, placeholder, prepend, append, character limit | ✓ |
| Text Area | default, placeholder, character limit, rows, new lines (wpautop / br / none) | ✓ |
| Number | default, placeholder, prepend, append, min, max, step | ✓ |
| Range | default, min, max, step, prepend, append | ✓ |
| Email | default, placeholder, prepend, append | ✓ |
| URL | default, placeholder | ✓ |
| Password | placeholder, prepend, append | ✓ |
| Image | return format (array/url/id), library (all/uploadedTo), preview size, min/max width/height/size, allowed file types | ✓ (pickers use the WordPress media modal) |
| File | return format, library, min/max size, allowed types | ✓ |
| WYSIWYG Editor | default, tabs (all/visual/text), toolbar (full/basic), media upload, delay | ◐ TinyMCE (with the basic/full toolbar) on screens that load the classic editor; a textarea with formatting buttons elsewhere (Pursae Editor, users, terms, options pages) |
| oEmbed | width, height | ✓ (preview through Pursae's oEmbed proxy) |
| Gallery | return format, library, min/max, insert, min/max dimensions/size, types | ✓ |
| Select | choices, default, return format (value/label/array), allow null, multiple, stylised UI, ajax, placeholder | ✓ (stylised UI / ajax ◐: settings are kept, the control is a native select) |
| Checkbox | choices, default, return format, layout, toggle all, allow custom, save custom | ✓ |
| Radio Button | choices, default, return format, layout, allow null, other, save other | ✓ |
| Button Group | choices, default, return format, allow null, layout | ✓ |
| True / False | default, message, stylised UI, on/off text | ✓ |
| Link | return format (array/url) | ◐ URL / link text / new-tab inputs instead of the wpLink modal |
| Post Object | post type, post status, taxonomy filter, return format (object/id), allow null, multiple, bidirectional | ✓ (bidirectional ✗) |
| Page Link | post type, taxonomy, allow null, allow archives URLs, multiple | ✓ |
| Relationship | post type, post status, taxonomy, filters (search/post type/taxonomy), elements (featured image), min/max, return format, bidirectional | ✓ (bidirectional ✗) |
| Taxonomy | taxonomy, appearance (checkbox/multi select/radio/select), create terms, save terms, load terms, return format (object/id), allow null | ✓ (new terms are created when the post is saved) |
| User | role filter, allow null, multiple, return format (array/object/id) | ✓ |
| Google Map | center lat/lng, zoom, height | ◐ address + lat/lng/zoom inputs with a map preview link; the Google Maps JS picker needs an API key (`acfgo_google_api_key` setting enables the picker) |
| Date Picker | display format, return format, week starts on | ✓ stored `Ymd` like ACF, return format applied; ◐ the editing control is the browser's date input (display format / week start are kept but the browser decides) |
| Date Time Picker | display format, return format, week starts on | ✓ stored `Y-m-d H:i:s`; ◐ browser datetime input |
| Time Picker | display format, return format | ✓ stored `H:i:s`; ◐ browser time input |
| Color Picker | default, enable opacity, return format (string/array) | ✓ (swatch + text input; rgba() accepted with opacity) |
| Message | message, new lines, escape HTML | ✓ |
| Accordion | open, multi-expand, endpoint | ✓ |
| Tab | placement (top/left), endpoint, selected | ✓ |
| Group | sub fields, layout (block/table/row) | ✓ |
| Repeater (PRO) | sub fields, collapsed, min/max rows, layout (table/block/row), button label, pagination, rows per page | ✓ add / insert / duplicate / remove / drag / collapse rows; pagination ◐ setting kept, all rows on one page |
| Flexible Content (PRO) | layouts (label, name, layout, sub fields, min/max), button label, min/max | ✓ layout picker, insert / duplicate / remove / drag / collapse |
| Clone (PRO) | fields/groups to clone, display (seamless/group), layout, prefix label, prefix field names | ✓ |
| Icon Picker (6.3) | tabs (dashicons/media library/url), return format (string/array), library | ✓ |

Value storage is exactly ACF's: post meta `name` → value and `_name` → field key; repeater rows as
`name` = row count and `name_{i}_{sub}`; flexible content `name` = array of layout names; group
`name_{sub}`; options as `options_{name}` / `_options_{name}`; users in user meta, terms in term
meta, comments in comment meta. Values written by ACF export tools (WXR with ACF meta) read back the same way.

## 3. Location rules

OR groups of AND rules, operators `==` / `!=`.

| Param | Status |
|---|---|
| Post Type, Post Template, Post Status, Post Format, Post Category, Post Taxonomy, Post | ✓ |
| Page Template, Page Type (front/posts/top level/parent/child), Page Parent, Page | ✓ |
| Current User (logged in, viewing front end, viewing back end), Current User Role | ✓ |
| User Form (add/edit/register/all), User Role | ✓ (register ◐: there is no public registration form) |
| Taxonomy (term edit/add screens) | ✓ |
| Attachment | ✓ (attachment edit screen; values in the media item's meta) |
| Comment | ✓ (comment edit screen; values in comment meta) |
| Widget, Menu, Menu Item | ✗ widgets and nav menu items in Pursae have no meta storage that ACF could attach to; the rule types are listed (marked unavailable) and never match |
| Block (ACF Blocks) | ✓ |
| Options Page | ✓ |

## 4. Presentation and group settings

Style (standard/WP box, seamless), Position (high after title, normal, side), Label placement
(top/left), Instruction placement (below labels/below fields, tooltip), Order No., Description,
Hide on screen (permalink, the content editor, excerpt, discussion, comments, revisions, slug,
author, format, page attributes, featured image, categories, tags, send trackbacks) — **✓** in the
classic editor. In Pursae Editor the boxes sit in the editor's meta box pane (normal and side) and are
saved with the post through the meta box request, like ACF; `acf_after_title` falls back to normal
and hide-on-screen applies to the classic editor only (ACF behaves the same).

## 5. Conditional logic

Show this field if (OR groups of AND rules) against other fields of the group: has any value, has
no value, value is equal to, not equal to, matches pattern, contains, greater than, less than,
selection is greater/less than. **✓** in the editor (live) and on save (hidden fields are disabled,
so their stored values stay untouched, as in ACF).

## 6. Validation

Required, character limit, min/max number, email/URL format, min/max rows/selections/images, image
dimensions/size, file types — **✓** ajax validation before submit (`acf/validate_save_post`
equivalent) and server-side on REST writes. Custom `acf/validate_value` filters ✗ (no PHP hooks;
Wasm plugins can use REST).

## 7. Options pages (PRO, UI-created since 6.2)

ACF › Options Pages: page title, menu slug, parent page (top level or under any menu), menu
position, icon (dashicon), menu title, capability, redirect to first child, update button label,
updated message, description, data storage (options or a custom post id `post_id`), autoload — **✓**.
Each options page is `/wp-admin/admin?page=<slug>` with the field groups whose location is that page.

## 8. Post Types and Taxonomies UI (6.1+)

ACF › Post Types / Taxonomies: labels (all generated from singular/plural, editable), public,
hierarchical, supports, taxonomies, menu icon/position, show in REST, REST base, archive, rewrite slug,
capability type, exclude from search, delete with user… — **✓** stored as `acf-post-type` /
`acf-taxonomy` posts; on save the type/taxonomy is registered (content_types / taxonomies rows),
gets its admin menu (All / Add New, its taxonomies), the core's list, add and edit screens (classic
editor) and term screens, and every role receives the type's capabilities (capability type "post").
Public custom post types are routed on the front end: entries at `/<rewrite slug>/<post>/` (with
ancestors for hierarchical types), the archive at `/<archive slug>/` or `?post_type=` when the type
has one, with pagination, feed, body classes and the "Archives: <plural>" title; `?p=<id>` redirects
to the pretty permalink. Pursae Web Builder's Theme Builder can design their single and archive
templates. ◐ Pursae Editor and REST routes (`/wp/v2/<rest_base>`) of custom post types need core
support for arbitrary types and are not built (see "Left out").

## 9. ACF Blocks (PRO)

`block.json` with an `acf` key (`renderTemplate`, `mode`), registered with `register_block_type()` —
**◐** Pursae Custom Fields registers blocks from `acf-blocks/<name>/block.json` folders (in `<data>/acf-blocks` or the
active theme package) with a Go `html/template` render template (`render.html`); the block appears in
Pursae Editor's inserter (category from block.json), its fields are edited in the block (Edit/Preview
toggle, server-rendered preview), values live in the block's `data` attribute like ACF, and the front
end renders the template with `get_field` reading the block's data. Field groups target blocks with
the Block location rule.

## 10. REST API

`acf` object for field groups with "Show in REST API": **✓** `/wp/v2/posts` and `/pages` read and
write (`POST {"acf": {"field_name": value}}` with sub fields by name, validated before the post is
saved, unknown names rejected), `/users` and `/categories` / `/tags` read. `?acf_format=standard`
returns formatted values, the default (`light`) stored values with typed IDs/numbers/booleans.
◐ Writes to users/terms and the `/comments`, `/media` objects are not built.

## 11. Template functions

`get_field`, `the_field`, `get_field_object`, `get_fields`, `get_field_objects`, `have_rows` /
`get_sub_field` / `the_sub_field` / `get_row_layout` / `get_row_index` / `get_row` — **✓** as template
functions for every theme (bundled and theme packages; Go templates take the post explicitly and
range over rows instead of `the_row()`), and `acfGetField`, `acfGetFieldObject`, `acfGetFields`,
`acfUpdateField` (update_field), `acfDeleteField` (delete_field) for Go code.
Wasm plugins: **✓** `get_post` (host API, `posts:read`) returns an `acf` object with the post's field
values (stored format) while Pursae Custom Fields is active (`PostData.ACF` in the Go SDK).

Theme template usage:

```
{{get_field "subtitle" .Post}}                 value (formatted) of the post in the loop
{{the_field "subtitle" .Post}}                 the same, HTML-escaped for output
{{get_field "phone" "option"}}                 options page value; "user_3", "term_5", "comment_9" too
{{range have_rows "slides" .Post}}{{get_sub_field "title" .}} ({{.Index}}){{end}}
{{range have_rows "sections" .Post}}{{if eq .Layout "hero"}}…{{end}}{{end}}   flexible content
{{with get_field_object "price" .Post}}{{.label}}: {{.value}}{{end}}
```

## 12. Tools

ACF › Tools: Export field groups, post types, taxonomies and options pages (download a .json file or
show the JSON), Import JSON (any of them; existing items with the same key are updated) — **✓**.
Generate PHP ✗ (no PHP; the JSON is what local JSON and `acfgoRegisterLocalGroup` take). Import from
CPT UI ✗.

## 13. Settings / misc

The ACF menu needs `manage_options`; option `acfgo_show_admin` = "0" hides it (ACF's `show_admin`
setting) ✓. Field group / field keys visible in the editor and list ✓. ACF › Updates (licence) ✗ —
nothing to license.

## How it is wired

Native plugin switch `native_acfgo` (Plugins screen, default off). Code: `internal/server/acfgo*.go`,
templates `web/admin/templates/pages/acfgo.html`, assets `web/admin/assets/acfgo/`. No migration:
field groups, post types, taxonomies and options pages are posts of the `acf-*` content types
(created on activation). Values live in `posts.meta`, `users.meta`, `terms.meta`, `comments.meta`,
`media.meta` and the options table. The core's edit screens are reached through one gin middleware
(it queues the boxes, which `acfgo-input.js` moves into the form, and saves `acf[...]` before the
core's handler runs) plus small hooks: menu (adminPage), admin-ajax, REST serializers and post
writes, the template FuncMap, `admin?page=` options pages, label lookups for ACF post
types/taxonomies, the Wasm `get_post` payload.

## Left out (and why)

- Widget, nav menu and nav menu item locations: no meta storage in Pursae for them.
- Field value revisions: core revisions do not store meta.
- Bidirectional relationships (6.2): updating the other post's field on save.
- PHP APIs/hooks (`acf/load_value`, `acf/format_value`, `acf/validate_value` filters, `acf_form()`
  front-end forms): there is no PHP; Wasm plugins read values through `get_post` and write through REST.
- Repeater pagination, select2 "stylised UI"/ajax selects, the wpLink modal, jQuery UI date
  pickers and the Google Maps JS picker: replaced by simpler native controls (settings are kept
  and exported unchanged).
- Custom post types: Pursae Editor editing and REST routes. ACF taxonomies on posts/pages work
  everywhere the core's term screens and post terms do; their front-end archives are not routed.
- REST writes for users and terms; `acf` on `/comments` and `/media` responses.
