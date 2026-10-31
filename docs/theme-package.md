# Theme packages

A Pursae theme is a directory (or a zip of that directory) with no Go code in it:

```
theme.json          metadata and everything a Go theme would register in code
templates/*.html    Go html/template files (the template hierarchy, see below)
assets/**           style.css, scripts, fonts, images, screenshot.png — the theme's web root
readme.md           optional, shown on the theme's marketplace page
```

Installed themes live in the themes directory (`WPGO_THEMES_DIR`, default `<data>/themes`,
the same directory the Theme File Editor writes its overrides to), one sub-directory per
slug: `<data>/themes/<slug>/theme.json`. The server loads every such directory at start-up
and right after Appearance › Themes › Add Theme › Upload Theme installs one; a package that
does not validate is logged and skipped. A package whose slug matches a bundled theme
(`twentytwentyfive`, `hello-web-builder`, …) is rejected, and the directory name must equal the slug.
Appearance › Themes › Theme Details › Delete removes the directory and the registration
(capability `delete_themes`; the active theme cannot be deleted).

`assets/` is served at the theme URI, `/wp-content/themes/<slug>/`, so `assets/style.css`
is `/wp-content/themes/<slug>/style.css`. Every path in theme.json is theme-relative and
must point under `assets/`. The Theme File Editor edits a package's files in place (there
is no "Restore original": the package's files are the originals).

## theme.json

```json
{
  "slug": "starter",
  "name": "Starter",
  "version": "1.0.0",
  "description": "A minimal starting point.",
  "author": "Gopher Co.",
  "author_url": "https://example.com",
  "theme_url": "https://example.com/starter",
  "tags": ["blog", "one-column"],
  "requires_core": "0.1.0",
  "screenshot": "assets/screenshot.png",

  "widget_areas": [
    {"id": "sidebar-1", "name": "Sidebar", "description": "Appears on the right."}
  ],
  "menu_locations": [
    {"id": "primary", "name": "Primary Menu"}
  ],

  "styles": [
    {"handle": "starter-style", "src": "assets/style.css", "deps": [], "media": "all"}
  ],
  "scripts": [
    {"handle": "starter-nav", "src": "assets/js/navigation.js", "deps": [], "in_footer": true}
  ],
  "editor_styles": ["assets/editor-style.css"],
  "preload_fonts": ["assets/fonts/inter.woff2"],

  "palette": [
    {"name": "Ink", "slug": "ink", "color": "#1d2327"}
  ],
  "supports": {
    "post-thumbnails": true,
    "custom-logo": true,
    "title-tag": true,
    "html5": ["search-form", "comment-form", "gallery", "caption"],
    "responsive-embeds": true,
    "align-wide": false
  },

  "customizer": {
    "sections": [
      {
        "id": "starter_colors", "title": "Colors", "priority": 40,
        "settings": [
          {"id": "accent", "label": "Accent color", "type": "color", "default": "#2271b1", "transport": "postMessage", "css_var": "--accent"},
          {"id": "show_sidebar", "label": "Show sidebar", "type": "checkbox", "default": true, "body_class": "has-sidebar"},
          {"id": "layout", "label": "Layout", "type": "select", "default": "list", "body_class": "layout-{value}",
           "choices": [{"value": "list", "label": "List"}, {"value": "grid", "label": "Grid"}]}
        ]
      }
    ]
  }
}
```

Rules:

- `slug`: `^[a-z0-9]+(-[a-z0-9]+)*$`, 2–64 chars; `version`: semver; `name` required.
  `requires_core` (semver) must not be newer than the running Pursae. `author_url`
  and `theme_url` are http(s) URLs. `screenshot` defaults to `assets/screenshot.png`.
- `styles`/`scripts`: `handle` is `^[a-z0-9]+([_-][a-z0-9]+)*$` and unique within its
  list; `src` is a theme-relative path under `assets/` (served under the theme URI with
  `?ver=<version>`) or an absolute http(s) URL. `deps` may reference core handles
  (`wp-block-library`, `jquery`, …) or the theme's own handles; the theme's assets are
  printed after core's, ordered so that each follows its own dependencies like
  `wp_enqueue_*` (a cycle is an error), in declared order otherwise. Styles print in
  `wp_head`, scripts in `wp_head` or, with `in_footer`, in `wp_footer`. Every referenced
  theme file must exist in the package. `editor_styles` are `.css` files, `preload_fonts`
  `.woff2` files, both under `assets/`.
- `widget_areas[].id` and `menu_locations[].id` are handles (same pattern), unique; `name`
  is required. Widget areas use the `<section id class="widget …">` / `<h2 class="widget-title">`
  markup of the bundled themes.
- `palette`: `name`, `slug` (handle) and a hex `color` each; it becomes the
  `editor-color-palette` support.
- `customizer.sections`: `id` (handle, unique), `title` required, sorted by `priority`.
  `settings[].id` is a handle, unique across the theme (`custom_logo` and `custom_css` are
  core's). `type`: `color`, `text`, `textarea`, `number`, `checkbox`, `select` (with
  `choices`, an ordered array of `{"value", "label"}` or an object `{"value": "Label"}`
  sorted by value), `image` (an attachment chosen from the media library). `default` must
  match the type (a boolean, an integer within `min`/`max` — defaults 0 and 1000000 —, a hex
  color, one of the choices, a string, an attachment id). A setting with `css_var`
  (`--name`) is printed as a CSS custom property on `:root` (checkboxes as `1`/`0`,
  numbers as plain numbers); one with `body_class` adds that class when truthy, or, when
  it contains `{value}`, the class with the value filled in (`layout-grid`).
  `transport: "postMessage"` previews live without a reload (not for `image`); the default
  is `refresh`. Values are stored as theme mods of the theme, exactly like a Go theme's.
- `supports` is reported to the block editor through the REST themes endpoint
  (`theme_supports`, with `editor-color-palette` from `palette` and `editor-styles` from
  `editor_styles`); `custom-logo` is on unless set, so Site Identity offers a logo. The
  bundled defaults (`title-tag`, `post-thumbnails`, `html5`, `responsive-embeds`,
  `wp-block-styles`) apply unless overridden.

## Templates

The templates directory follows the WordPress template hierarchy with Go templates: every
`templates/**/*.html` file is a template named by its path without the extension. For the
main query the first existing one of these is rendered:

| request      | candidates                                              |
|--------------|---------------------------------------------------------|
| front page   | `front-page`, `page`, `singular`, `index`               |
| blog         | `home`, `index`                                         |
| post         | `single-<format>`, `single`, `singular`, `index`        |
| page         | its template (below), `page`, `singular`, `index`       |
| category     | `category-<slug>`, `category`, `archive`, `index`       |
| tag          | `tag-<slug>`, `tag`, `archive`, `index`                 |
| author, date | `author` / `date`, `archive`, `index`                   |
| search       | `search`, `index`                                       |
| 404          | `404`, `index`                                          |

A page whose `_wp_page_template` is set (by demo content, see below) tries that template first,
also on the front page (`page-wide` or `page-wide.html` renders `templates/page-wide.html`).
Only `index.html` is required. Parts are ordinary templates included with
`{{template "header" .}}`, `{{template "content" .}}` and so on (`header`, `footer`,
`sidebar`, `comments`, `searchform`, `content*`, … by convention).

The data is the same `siteView` the bundled themes receive: `.DocumentTitle`, `.Head`
(`wp_head`), `.Footer` (`wp_footer`), `.BodyOpen`, `.BodyClass` (`body_class`),
`.AdminBar`, `.SiteName`, `.Description`, `.HomeURL`, `.CustomLogo`, `.TitleIsH1`,
`.Template`, `.Q` (the main query), `.Posts` / `.Post` (each with `.ID`, `.Title`,
`.Permalink`, `.Content`, `.Excerpt`, `.ShowExcerpt`, `.Class` (`post_class`), `.Thumbnail`,
`.AuthorName`, `.AuthorURL`, `.DateText`, `.DateISO`, `.Categories`, `.Tags`,
`.CommentCount`, `.CommentsURL`, `.EntryMeta`, `.EditLink`, `.Biography`, …),
`.Pagination`, `.PostNav`, `.Comments` (`.Title`, `.List`, `.Count`, `.Open`, `.Form`),
`.ArchiveTitle`, `.ArchiveDesc`, `.SearchQuery`, `.TotalPosts`, `.PrivacyLink`,
`.CanPublish`, `.IsHomeHeader`, `.PostsPageTitle`, `.Areas` and `.Menus`. Package themes
also get `.Data.Settings` (every Customizer setting, typed, with defaults applied; image
settings hold the image URL) and `.Data.Nav` (rendered menus by location).

Functions, on top of the generic ones (`date`, `plural`, `num`, `dict`, `safe`,
`settingStr`, …): `{{nav_menu . "primary"}}` and `{{has_nav_menu . "primary"}}`
(`wp_nav_menu` / `has_nav_menu`), `{{dynamic_sidebar . "sidebar-1"}}` and
`{{is_active_sidebar . "sidebar-1"}}`, and `{{theme_mod . "accent"}}` (`get_theme_mod`
with the declared default). Package themes get `singular` or `hfeed` on the body like the
bundled ones. See `examples/themes/starter` for a complete, minimal theme and
`web/themes/classic/templates` for a bundled one.

## Packaging

`zip -r starter.zip theme.json templates assets readme.md` from inside the theme directory
(the files sit at the zip root, not under a folder; a single top-level folder is also
accepted and stripped; `tools/build-themes.sh` does this for the examples). Limits: 32 MiB
per package, 4 MiB per file, 64 MiB extracted, 2000 files, only these extensions:
`.json .html .css .js .map .svg .png .jpg .jpeg .gif .webp .avif .ico .woff .woff2 .ttf
.otf .md .txt`. The templates must parse before anything is replaced. Uploading needs the
`install_themes` capability; a package whose slug is already installed replaces it only
when its version is higher (theme mods and widgets are kept). Themes carry no executable
code, so they are not signed; the Hub and the installer verify the SHA-256 of the zip.

## Library metadata

`theme.json` may also carry, for the theme library (Appearance › Add Themes › Library) and the
Hub:

```json
"category": "blog",
"style": "Minimal, editorial serif",
"demo": "demo/demo.json"
```

- `category` (optional, one of): `blog`, `magazine`, `portfolio`, `photography`, `business`, `agency`,
  `restaurant`, `food`, `travel`, `fashion`, `technology`, `education`, `health`, `fitness`,
  `nonprofit`, `music`, `wedding`, `real-estate`, `personal`, `minimal`, `home`, `shop`, `legal`,
  `construction`, `finance`, `architecture`, `gaming`, `entertainment`, `podcast`, `beauty`, `kids`,
  `pets`, `automotive`, `hotel`, `startup`, `science`, `religion`, `environment`, `books`, `sports`.
- `style`: a short phrase shown on the library card (at most 80 characters, one line, no markup).
- `demo`: path of the demo content file inside the package, a `.json` file under `demo/` (below).

The REST themes endpoint (`wp/v2/themes`) reports them as `category`, `style` and `has_demo`.

### Pursae Web Builder designs

A theme may be built with Pursae Web Builder: `"golementor": "golementor/design.json"` names a design in the
format of a saved Pursae Web Builder theme (`"format": "gowebeditor-theme"`): `kit` (Site Settings: global
colors and fonts, theme style), `custom_css`, and `parts`, each with a `type` (`header`, `footer`,
`single-post`, `single-page`, `archive`, `search-results`, `error-404`, and with Pursae Commerce
`single-product` and `product-archive`), a `title`, `elements` (the element tree, a JSON array) and
`conditions` (`include/general`, `include/product`, `include/product_archive/product_cat`,
`include/goocommerce/cart`, …). Activating the theme switches Pursae Web Builder on and loads the design
into the Theme Builder's working copy (unsaved work there is kept as a saved theme first); pages then
render through Hello Pursae Web Builder with the theme's own stylesheet added, so the store's header,
footer, product page, shop, cart and checkout are edited visually. The theme's `templates/` are the
fallback while Pursae Web Builder is off. A store theme declares `"supports": {"woocommerce": true}` and is
listed under Pursae Commerce › Store Themes. Fernwood, Good Boy Supply and Kite & Crayon are built this
way by `tools/goo-golementor-themes/` (`build.py` writes the packages, `art.py` draws the demo art).

## Demo content

A theme may ship one-click demo content: `demo/demo.json` plus images under `demo/images/`
(`.jpg`, `.jpeg`, `.png` or `.webp`, each ≤ 1 MiB, ≤ 24 images). The demo file is validated
when the package is installed: a package whose demo does not validate is rejected, with every
problem listed (`demo/demo.json: pages[1]: {{post:missing}} does not refer to an item of the
demo; …`). Appearance › Themes shows **Import Demo Content** on the active theme's card and in
its details when it has a demo, and right after activating such a theme a notice asks "This
theme comes with demo content. Import it?"; Add Themes › Library offers **Install & Import
Demo** (install, activate, import in one step). Importing needs the `import` capability.

Importing is idempotent: every created post, page, attachment and term carries the meta
`_wpgo_demo = "<theme slug>"` (and `_wpgo_demo_ref` = its id/slug in demo.json), so a second
import skips existing items, and **Remove Demo Content** deletes exactly them, the menus and
widgets the import created, and restores what it changed: the options and reading settings,
the menu previously assigned to each location, the theme mods it set, and the widgets that
were in the demo's widget areas (an import moves them to the inactive widgets). A category or
tag whose slug already exists on the site is used as it is and never removed.

```json
{
  "version": 1,
  "options": { "blogname": "Northwind Journal", "blogdescription": "Notes on slow living" },
  "categories": [
    { "slug": "travel", "name": "Travel", "description": "Places we loved." }
  ],
  "tags": [ { "slug": "coffee", "name": "Coffee" } ],
  "media": [
    { "id": "hero", "file": "demo/images/hero.jpg", "alt": "Morning light over the harbour", "caption": "" }
  ],
  "pages": [
    { "slug": "home", "title": "Home", "content": "<!-- wp:paragraph --><p>…</p><!-- /wp:paragraph -->",
      "template": "", "featured_media": "hero", "front_page": true },
    { "slug": "blog", "title": "Blog", "content": "", "posts_page": true },
    { "slug": "about", "title": "About", "content": "…", "parent": "" }
  ],
  "posts": [
    { "slug": "a-week-in-lisbon", "title": "A week in Lisbon", "date": "2026-09-12T09:30:00",
      "excerpt": "…", "content": "<!-- wp:paragraph -->…", "categories": ["travel"], "tags": ["coffee"],
      "featured_media": "hero", "format": "standard", "sticky": false,
      "comments": [ { "author": "Ada", "email": "ada@example.com", "content": "Lovely!", "date": "2026-09-13T10:00:00" } ] }
  ],
  "menus": [
    { "name": "Main menu", "location": "primary", "items": [
      { "title": "Home", "page": "home" },
      { "title": "Travel", "category": "travel" },
      { "title": "About", "page": "about", "children": [ { "title": "Contact", "page": "contact" } ] },
      { "title": "Instagram", "url": "https://instagram.com/" }
    ] }
  ],
  "widgets": { "sidebar-1": [ "<!-- wp:search /-->", "<!-- wp:latest-posts {\"postsToShow\":4} /-->" ] },
  "customizer": { "accent": "#c2410c", "layout": "grid" }
}
```

Rules:

- `version` is `1`. Unknown fields are ignored.
- Content (and excerpts, widgets and text Customizer values) is block markup
  (Gotenberg/WordPress block comments) or plain HTML. Tokens: `{{media:hero}}` is the imported
  image's URL, `{{media_id:hero}}` its attachment id (for `<!-- wp:image {"id":{{media_id:hero}}} -->`
  and `wp-image-{{media_id:hero}}`), `{{page:about}}`, `{{post:a-week-in-lisbon}}`,
  `{{category:travel}}` and `{{tag:coffee}}` permalinks. Every token must name an item of the demo.
- Slugs (categories, tags, pages, posts) match `^[a-z0-9]+(-[a-z0-9]+)*$` and are unique per
  kind; media ids are handles. A category may name a `parent` category of the demo.
- Pages: `title` required; optional `content`, `excerpt`, `featured_media` (a media id),
  `parent` (another page's slug, in any order), `menu_order`, `template` (the name of a file in
  the theme's `templates/`, e.g. `page-wide`; see Templates), `front_page`, `posts_page` (at
  most one each; they set Settings › Reading, `show_on_front=page`).
- Posts: `title` required; `date` like `2026-09-12T09:30:00` (also `2026-09-12 09:30:00` or a
  bare day, in the site's timezone) and not in the future — undated posts get one day apart
  before now; `categories` / `tags` are slugs of the demo's own lists (a post without
  categories goes to the default category); `format` is a post format; `sticky`; `comments`
  (≤ 20 per post, `author` and `content` required, optional `email`, `url`, `date`) are
  imported approved.
- Menus (≤ 10): `name` required; `location` one of the theme's `menu_locations` (one menu per
  location); each item has exactly one of `page`, `post`, `category` (slugs of the demo) or
  `url` (http(s), a site path or `#anchor`); `title` is required for `url` items and defaults
  to the page/post title or category name otherwise; `children` nest up to 3 levels.
- `widgets` keys are the theme's `widget_areas`; each value is a list of block markup strings,
  added as block widgets.
- `customizer` keys are the theme's own setting ids (or `custom_logo`); values match the
  setting type (a hex color, `true`/`false`, an integer within min/max, one of the choices, a
  string; an `image` setting or `custom_logo` takes a media id, `"hero"` or `"{{media:hero}}"`).
  They are stored as theme mods of that theme.
- `options` may only set `blogname`, `blogdescription` (strings), `posts_per_page` (1–100),
  `date_format`.
- A page may be built with Pursae Web Builder: `golementor` is its element tree (`_elementor_data`, a JSON
  array, with the same tokens as the content, e.g. `"url": "{{media:hero}}", "id": "{{media_id:hero}}"`)
  and `golementor_settings` its page settings (`{"hide_title": "yes"}`). Without a `template` the page
  uses Pursae Web Builder's Full Width template.
- A store theme's `goocommerce` section imports products, categories, attributes, coupons and
  shipping (`internal/server/goo_demo.go`). With Pursae Multi Language active every imported item gets the default
  language.
- Limits: 24 images, 50 pages, 100 posts, 10 menus. An import runs in the request, with a
  two-minute limit, and reports its counts in an admin notice.

Demo text is original (no lorem ipsum, no copyrighted text) and images are original
(generated) artwork.

The default theme, Twenty Twenty-Five, ships several demo sets in the same format, one per
`web/themes/twentytwentyfive/demos/<set>/demo/` (built by `tools/twentytwentyfive/demos.py` from
the theme's public-domain photographs). Its Import Demo Content opens a chooser; a set is marked
`_wpgo_demo = "twentytwentyfive-<set>"`, and importing another set first removes the one imported
before, so its pages, menus, front page and color scheme never stay behind.

## Theme library

Pursae ships a library of ready themes in `web/theme-library/<slug>/` (each a complete
theme package directory whose name is its slug, embedded in the binary; a directory that does
not validate is logged and left out). Appearance › Add Themes opens on the **Library** tab: the
themes with screenshot, name, category and style, filter links for the categories that have
themes, and search. **Install** zips the directory in memory and hands it to the same
installer as Upload Theme, so the package lands in `<data>/themes` with the same validation
(including the demo file); an installed theme shows Activate / Live Preview, and when the
library carries a higher `version` than the installed one the card offers **Update** (theme
mods and widgets are kept). The installed theme can be customized and deleted like any
package. The All, Bundled and Uploaded tabs list the installed themes as before.

## Live Preview with demo content

Every card of Add Themes › Library has **Live Preview** (the screenshot, or a button when the
card has no Install & Import Demo), and the details of an installed package theme that ships a
demo have **Preview with demo content** on Appearance › Themes. Both open
`/wp-admin/theme-preview?theme=<slug>` (`&source=installed` for the installed files): WordPress's
full-screen theme-install overlay with the theme's name, author, version, description and the
actions Add Themes or Themes would offer (Install, Install & Import Demo, Update, Activate, Import
Demo Content), previous/next library theme, collapse, and the desktop/tablet/mobile switcher. The
iframe shows the theme as it looks with its demo content imported — pages, posts, menus, widgets,
images and Customizer values — while the site, its database, uploads and settings stay untouched.
Previews need `switch_themes` (and `install_themes` for a library theme).

Each previewed theme gets a **sandbox**: a second, complete site in the PostgreSQL schema
`wpgo_preview_<slug with _>` of the same database (the pool's `search_path`; migrated with the
normal migrations) with its uploads and themes directory in `<data>/preview/<slug>/`. It is
installed like a fresh site that copies the site's address, permalink, time zone and date settings,
and the theme is installed, activated and given its demo with the same code as on the site. The
first preview builds it in the background (a few seconds; the iframe shows *Preparing preview…*
and reloads when it is ready); later previews reuse it, also after a restart. It is rebuilt when
the theme's files or version, or the copied settings, change. At most 8 sandboxes are kept, the
least recently used go first; **Clear theme previews** on the Library tab drops them all (schemas
and files).

The sandbox is served under `/wp-admin/theme-preview/<slug>/` as an anonymous visitor: no
cookies reach it and none come back, only GET and HEAD are allowed (comments, forms and REST
writes are refused), and its dashboard, login and comment posting answer 403. Responses carry
`X-Robots-Tag: noindex, nofollow` (the sandbox also discourages search engines). The sandbox runs
as if it were the site at the site's own address; the preview prefix is added to every URL of its
responses on the way out (absolute and root-relative links, assets, uploads, `srcset`, `url()`,
JSON-escaped URLs, redirects), so navigation stays inside the preview. A sandbox keeps its theme
packages in its own registry and has no page cache, plugin runtime, scheduler, mail or Hub.
