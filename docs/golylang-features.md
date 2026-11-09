# Pursae Multi Language: Polylang Pro feature inventory

Pursae Multi Language is Pursae's built-in multilingual plugin, a 1:1 functional clone of **Polylang Pro**. It is a
native (compiled-in) plugin like Classic Editor, Pursae Cache and Pursae Web Builder: it is **off** on a fresh
install, it is switched on under **Plugins**, and while it is off it adds nothing. There are no menus, the
`admin?page=mlang*` screens answer 403, the `pll/v1` REST namespace is hidden, posts and terms carry no
`lang` field, there is no front-end output, and `/fr/` URLs are 404.

This document lists what Polylang Pro 3.9 does. The list comes from the polylang/polylang GPL source
(free core, 3.9.1), the polylang.pro documentation (Pro-only features) and wordpress.org. Each item is
marked as follows:

- ✅ implemented
- ◐ implemented with a difference
- ✗ left out, with the reason

Code: `internal/server/golylang*.go`, `web/admin/templates/pages/golylang.html`,
`web/wp-includes/js/pll-visual.js`, `internal/i18n/front.go` (the page string pass),
`internal/db/migrations/0009_golylang.sql` and `0036_golylang_translate.sql`. Tests:
`internal/server/golylang*_test.go`, `internal/i18n/front_test.go`.

Beyond Polylang Pro, Pursae Multi Language also has the TranslatePress-style features: [visual
translation](#visual-translation) of a page, [automatic string capture](#automatic-string-capture)
of theme, plugin and page text, per-language [media texts](#media) and [machine
translation](#machine-translation) with DeepL, Google or an OpenAI-compatible API.

## Data model

| Polylang | Pursae Multi Language | |
|---|---|---|
| `language` taxonomy: name, slug, locale, rtl, flag, order (`term_group`) | `pll_languages` table, plus Pro's `active` flag | ✅ |
| `post_language` and `post_translations` (one group term per translation set) | `pll_post_langs` (`post_id`, `lang`, `grp`) | ✅ |
| `term_language` and `term_translations` | `pll_term_langs` | ✅ |
| Media languages (attachments) | `pll_media_langs` for the language of an item, plus `pll_media_i18n` (`media_id`, `lang`, `alt_text`, `title`, `caption`, `description`) for per-language texts of one shared item | ◐ one item with translated texts instead of one copy per language (see [Media](#media)) |
| `polylang_mo` posts for string translations | `pll_strings` (`lang`, `original`, `translation`) | ✅ |
| Registered strings (the `polylang_strings` option) | `pll_string_sources` (`original`, `context`, `domain`, `source`, `first_seen`, `last_seen`, `url`); the old option `golylang_strings` is still read and copied into the table | ✅ |
| Option `polylang` | Option `golylang`, same keys: `force_lang`, `hide_default`, `rewrite`, `redirect_lang`, `browser`, `media_support`, `post_types`, `taxonomies`, `sync`, `domains`, `default_lang`, `nav_menus`, plus Pro `media_duplicate`; Pursae adds `string_capture`, `machine_translation`, `mt_provider`, `mt_key` (sealed with AES-GCM, never shown back), `mt_endpoint`, `mt_model`, `mt_formality` | ✅ |
| User meta `pll_filter_content` (admin language filter) | Same key in the user's meta | ✅ |
| Cookie `pll_language`, 1 year, SameSite Lax | Same; with Pursae Cache on it is set by an inline script instead of `Set-Cookie` (Polylang's cache mode), so first visits are cached | ✅ |
| Objects without a language are hidden on the front end; admin offers "You can set them all to the default language." | The notice and the action are the same. An object with no row is treated as the default language instead of hidden, so nothing vanishes before the admin acts. The first language added takes all existing content. | ◐ |

## Languages > Languages

| Feature | |
|---|---|
| "Add new language" form: Choose a language (predefined list, about 100 locales, fills the form), Full name, Locale, Language code, Text direction (left to right / right to left), Flag, Order; WordPress help texts | ✅ (the flag is a two-letter country code shown as an emoji flag; Polylang ships PNG flags) |
| Validation: locale pattern `^[a-z]{2,3}(?:_[A-Z]{2})?(?:_[a-z0-9]+)?$`, code pattern `^[a-z][a-z0-9_-]*$`, unique code | ✅ |
| Edit language ("Edit language", "Update"); changing the code updates `default_lang`, `domains` and `nav_menus` | ✅ |
| List columns: Full name, Locale, Code, Default language star (click to make default), Order, Flag, Posts | ✅ |
| Row actions Edit and Delete (with the JS confirm), plus Pro Activate/Deactivate | ✅ |
| Notices: Language added. / updated. / deleted. | ✅ |
| Creating a language creates its default category, linked to the default category | ✅ |
| Deleting the default language picks another default; the language's strings and menu locations go | ✅ |
| Downloading the WordPress `.mo` language pack | ✗ Pursae has no `.mo` translations of core |
| Setup wizard | ✗ the Languages screen does the same steps: the first language becomes the default and takes the existing content |
| Custom flags in `/wp-content/polylang/` | ✗ there is no `wp-content` directory for site files; any two-letter code can be used as the flag |
| Pro: locale fallback | ✗ there are no `.mo` files to fall back between |

## Languages > Settings (modules)

| Module | |
|---|---|
| Module list table with Settings, Activate and Deactivate actions, inline configuration rows, Cancel and Save Changes | ✅ |
| **URL modifications**: `force_lang` 0 (from content) / 1 (directory) / 2 (subdomain) / 3 (domains, with one URL per language), the plain-permalink variant ("from the code in the URL", `?lang=`), "Hide URL language information for default language", "Remove/Keep /language/ in pretty permalinks", "The front page URL contains the language code…", domain validation messages | ✅ |
| **Detect browser language**: Accept-Language with q-values matched on the full tag, then the primary subtag; `Vary: Accept-Language`; cookie first | ✅ |
| **Media**: translate media, Pro "automatically duplicate media in all languages" | ✅ with a different design: one Media Library item and one file per upload, with its alternative text, title, caption and description translated per language (see [Media](#media)). Duplication at upload is not needed, so `media_duplicate` is unused |
| **Record strings on pages** (Pursae) | ✅ on by default; see [Automatic string capture](#automatic-string-capture) |
| **Custom post types and Taxonomies**: checkbox lists of non-builtin public types and taxonomies | ✅ |
| **Synchronization**: Taxonomies, Custom fields, Comment status, Ping status, Sticky posts, Published date, Post format, Page parent, Page template, Page order, Featured image | ✅ terms, parent and featured image are mapped to their translations; Custom fields leaves each translation's own Pursae SEO text (title, description, focus keyphrase, canonical, Open Graph and X texts, breadcrumb title, custom schema, scores, and their Yoast / Rank Math keys) alone, as Polylang's Yoast integration does; a new translation never copies the canonical |
| Pro **Translate slugs**: translated category, tag, author and search bases per language (strings group "URL slugs") | ✅ |
| Pro **Share slugs** | ✓ Settings › Share slugs: translations may use the same slug; uniqueness is checked per language and URLs resolve to the request language first (needs the language from the URL; migration 0020 moves slug uniqueness from the database to the store, like WordPress) |
| Pro **Machine Translation (DeepL)** | ✅ as "Machine translation", with DeepL, Google Cloud Translation and OpenAI-compatible APIs; off by default, text leaves the site only when a manager asks for a translation (see [Machine translation](#machine-translation)) |

## Languages > Translations (strings)

| Feature | |
|---|---|
| Table with checkbox, String, Name, Group, Translations (one input or textarea per language), search "Search translations", "Filter by group" ("View all groups"), pagination | ✅ |
| Built-in strings: Site Title and Tagline (group WordPress) | ✅ translated on the front end; Settings › General with a non-default language selected in the admin bar links each field to Languages › Translations |
| Date Format and Time Format strings | ✗ Pursae formats dates without a request context, so there is no place to apply them |
| Widget titles and text (legacy widgets) | ✗ Pursae only has block widgets |
| Strings registered by themes (`pll_register_string`, `pll__`, `pll_e`) | ✅ registered on first use, kept in the table `pll_string_sources` (the old option `golylang_strings` is still read and copied into the table once) |
| Theme gettext strings (`__`, `_e`, `_x`, `_n`, `esc_html__`, …) and text/textarea Customizer settings | ✅ listed under "Theme: *name*"; see [API](#api) |
| Plugin strings | ✅ Wasm plugins call the `translate` host import (`wpgo.T`, `wpgo.X`); listed under "Plugin: *name*" |
| Text found on pages (TranslatePress-style) | ✅ listed under "Page text"; see [Automatic string capture](#automatic-string-capture) |
| Group labels | "WordPress" (Site Title, Tagline), "URL slugs", "Pursae" (core text), "Theme: *name*", "Plugin: *name*", "Page text", or the group given to `pll_register_string`. A string listed by several sources shows once; a theme or plugin source wins over page text |
| Strings with a gettext context (`_x`) | ✅ translated separately per context; PO export writes the context as `msgctxt` and import reads it back |
| Bulk Delete for registered strings (WordPress and URL slugs strings are greyed) | ✅ |
| "Clean strings translation database" | ✅ removes recorded strings not seen for 30 days that were never translated, untranslated page text recorded on pages of other languages or naming a language, then translations whose string is no longer listed anywhere |
| Admin language filter limits the columns to one language | ✅ |
| Pro: export (Target language, group, Download) and import (Upload) | ✅ in PO format, with the `Language` and `Site-Reference` headers; XLIFF is left out |
| Machine translation on the screen (Pursae) | ✅ while the module is on: a "Suggest" button per input, "Translate empty strings on this page" (fills the inputs; nothing is saved until **Save Changes**) and "Translate all empty strings" (server side, at most 1000 strings per run, after a confirmation). URL slug strings are never sent |
| "Translate visually" (Pursae) | ✅ a page-title action that opens the [visual translation](#visual-translation) screen |

## Content editing

| Feature | |
|---|---|
| Posts and pages lists: one column per language (flag header, hidden for the filtered language), tick for the item's own language, pencil for an existing translation, plus to add one | ✅ |
| Quick Edit and Bulk Edit language select, the "Filter untranslated items" dropdown | ✗ not added, to keep the shared list-table code small; the language changes in the editor |
| Classic editor **Languages** meta box: Language select, Translations table (flag, edit/add icon, choose an existing post to link), changes saved at once over admin-ajax | ✅ |
| Block editor **Languages** panel (document sidebar) with the same content | ✅ |
| Pursae Web Builder editor: **Page tools** › **Languages** tab in the builder menu shows the page's language, its translations (open in the builder) and a button per missing language that creates a draft translation with a copy of the builder document (REST `web-builder/v2/documents/{id}/languages` and `/translations`, docs/developer/web-builder-contract.md §3.7 in the repository); needs `edit_post` on the page, the REST route lists a template read-only and never translates it | ✅ |
| Add translation (`post-new?from_post=&new_lang=`, nonce `new-post-translation`): links the translation, copies terms (translated), featured image, custom fields, order, template, parent (translated), format and discussion settings | ✅ |
| Pro **Duplicate content** toggle (per user and post type): copies title, content and excerpt | ✅ |
| Pro **machine translation of a new translation** | ✅ "Machine translate the copy" under Duplicate content (per user and post type, shown only while the Machine translation module works): the copied title, excerpt and content are translated. Block comment delimiters are kept byte for byte and only the HTML between them is sent; HTML that KSES would change (a Custom HTML block with a script) is neither sent nor changed, and every translated part goes through KSES. Web Builder posts get the title and excerpt only (the builder document is translated in the builder or visually). At most 10 copies per user per minute; a failure keeps the untranslated copy |
| Posts list row action "Translate visually" (Pursae) | ✅ on published posts, for users with `manage_options` |
| Pro **Synchronized posts**, **Bulk translate**, **XLIFF export/import of content** | ✗ out of scope for this pass (large Pro workflows on top of the parts above) |
| New content gets the admin filter language, or the default one | ✅ |
| Categories and tags: language columns, Language field on the add and edit forms, Translations rows (link an existing term, add with plus), the same name allowed in different languages, `from_tag`/`new_lang` add links | ✅ |
| Admin bar language filter ("Filters content by language", "Show all languages", one entry per language) narrows posts, pages, categories, tags and menus lists | ✅ |
| Static front page and posts page per language | ✅ each language shows its translation of the front page at its home URL, and the posts page likewise |
| Per-language user biography | ✗ Pursae's profile screen is not hooked |

## Menus and the language switcher

| Feature | |
|---|---|
| Appearance > Menus > Manage Locations: one location per language ("Primary Menu English", "Primary Menu Français"); `nav_menus` option | ✅ |
| "Language switcher" box in Appearance > Menus (adds the "Languages" item) | ✅ on the front end the item expands into one item per language |
| Switcher options on the menu item (show flags, hide names, hide current, force home, hide if no translation) | ◐ set through the item's CSS classes (`pll-show-flags`, `pll-hide-names`, `pll-hide-current`, `pll-force-home`, `pll-hide-if-no-translation`), as Pursae's menu items have no meta fields |
| Language switcher block (`polylang/language-switcher`, block editor and block widgets): dropdown, names, flags, force home, hide current, hide if no translation | ✅ |
| `pll_the_languages()` with `dropdown`, `show_names`, `show_flags`, `display_names_as`, `force_home`, `hide_current`, `hide_if_no_translation`, `hide_if_empty`, `post_id`; classes `lang-item`, `lang-item-{id}`, `lang-item-{slug}`, `current-lang`, `no-translation`, `lang-item-first` | ✅ as the template function `{{pll_the_languages . "dropdown=1"}}` |
| 3.9 layouts (horizontal, vertical, selector), alignment, flag aspect ratio and size | ✗ the legacy option set is used |
| Classic "Language switcher" widget | ✗ Pursae only has block widgets; the block covers it |

## Front end

| Feature | |
|---|---|
| Language from the directory (`/fr/`, `/language/fr/`), subdomain, domains or content (`?lang=`, then from the post or term) | ✅ |
| Hidden default language; `/en/…` redirects to `/…` | ✅ |
| Home page: redirect to the preferred language (cookie, then browser) unless the default language is hidden and browser detection is off | ✅ |
| All links carry the language: posts, pages, terms, archives, feeds, search, author, pagination | ✅ through a `links.Builder` filter |
| Canonical redirect of an object requested under another language's URL | ✅ |
| Queries narrowed to the current language: home, archives, feeds, search, sitemaps, Recent Posts and other blocks | ✅ through a `store.QueryFilter` hook |
| `<html lang>` and `dir="rtl"` | ✅ |
| hreflang links (region dropped unless two languages share the language), x-default on the front page when the default language code is shown, none on paged views | ✅ |
| Per-language sitemaps (`/fr/wp-sitemap.xml` listing French content) | ✅ |
| Translated site title and tagline | ✅ |
| Translated theme, plugin and builder template text (Pursae, TranslatePress-style) | ✅ the page string pass replaces every text with a translation on Languages › Translations; see [Automatic string capture](#automatic-string-capture) |
| Translated media texts (alt, caption, title, description) | ✅ while the Media module is on; see [Media](#media) |
| Per-language menus | ✅ |
| Widgets restricted to one language | ✗ Pursae's block widgets have no per-widget settings UI to hook |
| Auto-translation of IDs in queries (`PLL_AUTO_TRANSLATE`) | ✗ themes in Pursae don't query by hard-coded IDs |

## Media

Polylang Pro translates media by making one attachment post per language (the file is shared).
Pursae keeps **one Media Library item and one file**: the media row holds the texts of the default
language, and `pll_media_i18n` holds per-language overrides of the alternative text, title, caption
and description. An empty field falls back to the media row. Everything below needs the plugin on
and the **Media** module on.

| Feature | |
|---|---|
| Media modal (grid view, block editor, featured image): one fieldset per non-default language with Alternative Text (images only), Title, Caption, Description, saved with the attachment details | ✅ |
| Attachment edit screen: a **Translations** box with the same fields | ✅ |
| Media list view: one column per language (tick, pencil or plus, linking to the language's fields on the edit screen) | ✅ |
| Media Library not filtered by the admin language | ✅ one item serves every language |
| Front end: featured images, logos, attachment pages, Web Builder dynamic images, Commerce and custom-field images use the language's texts | ✅ through a store-level overlay on attachment reads (`store.AttachmentFilter`): one query per read, none for a language without overrides; admin screens and writes always see the media row |
| Images already inside post content (`wp-image-N`, `data-wb-media-id`, or an uploads URL) and their `figcaption` | ✅ the alt and caption are replaced only while they still hold the media row's text; text the author changed in the content is kept. The caption goes through KSES |
| REST | ✅ `?lang=fr` on `wp/v2/media` reads return the French texts; `context=edit` returns the media row plus `pll_i18n`, and updates accept `pll_i18n` (checked before anything is saved); `GET`/`POST pll/v1/media/{id}/i18n` |
| WXR export and import | ✅ as the attachment meta `_pll_media_i18n` (JSON); import keeps the languages the site has |
| Page cache | ✅ purged when an override really changes |
| Pro "automatically duplicate media" | ✗ not needed: there is nothing to duplicate |
| Grid view language indicator | ✗ translations are edited in the modal fields |
| MCP `update_media` | ✗ does not take `pll_i18n` yet; use the REST route |

## Visual translation

A TranslatePress-style screen that shows a page of the site with a translation panel next to it.
It works with every kind of theme: Twenty Twenty-Five, package themes with Customizer settings,
Hello Pursae with Theme Builder header and footer, and Web Builder canvas pages.

| Feature | |
|---|---|
| Entry points: front toolbar **Translate Page** (one item per language), **Translate visually** on Languages › Translations, and the posts list row action | ✅ for users with `manage_options`, while the plugin is on and the site has at least two languages |
| Screen `/wp-admin/admin?page=mlang_visual&url=<path>&lang=<code>`: a full-screen layout like the Customizer, panel on the left, the page in an iframe on the right, language select, **Close** back to the page shown | ✅ `url` must be a path on the site (other hosts, `//`, `javascript:` and admin, login or REST paths fall back to `/`) |
| String list of the page with search and a translated / untranslated filter; click any text on the page to select it | ✅ theme text, builder template text, Customizer settings and attribute text (`alt`, `title`, `placeholder`, `aria-label`, button values) |
| String editor: the original read-only, the translation with live preview in the page, **Save**, **Discard**, **Suggest** (machine translation, when on) | ✅ Save stores the translation like the Translations screen and purges the page cache |
| Image alt text editor | ✅ needs the Media module (writes `pll/v1/media/{id}/i18n`) |
| Post content, titles and comments | ✅ shown as a region with buttons: **Edit translation in Pursae Editor**, **Edit in Web Builder** (for builder posts) or **Create translation** (the normal new-translation flow), opening in a new tab. They are never edited as strings |
| Navigation inside the frame | ✅ Shift+click follows a link and keeps the session; links to other sites and to admin, login or REST URLs are blocked |
| Right-to-left languages | ✅ the panel's text fields take the language's direction |
| Subdomain and domain URL modes | ✗ the frame would be on another origin; the entry points are hidden and the screen shows a notice |

How it is kept safe and fast:

- The screen creates a session token bound to the user and valid 12 hours (kept in memory, lost on
  restart). The frame loads the page with `?pll_visual=<token>`. Only that user, logged in with
  `manage_options`, gets the visual page: no toolbar, `Cache-Control: no-store`, markers around
  the strings (`<pll-t>`, `data-pll-a-*`, `data-pll-img`), a JSON data island and
  `pll-visual.js`/`.css`. Every other request (anonymous, no token, someone else's or an expired
  token) gets the normal page, so visitors never see a marker or the script.
- Saving needs `manage_options` and the `wp_rest` nonce; at most 200 strings per save, translations
  up to 10000 characters. The panel and the frame only accept messages from the same origin and
  the same window.
- Pages in the default language are read-only: the panel says so and offers the translation of
  the page when it exists.

## Automatic string capture

Text that a theme, a plugin or a Theme Builder template prints is translated as a string, the way
TranslatePress does it, and the strings are found for you.

| Feature | |
|---|---|
| Page string pass: on every page of a non-default language, each text node and translatable attribute whose exact text (whitespace collapsed, entities decoded) has a translation on Languages › Translations is replaced | ✅ exact matches only: no patterns, no dates, no partial text |
| Left alone: post content, titles, excerpts and comments (marked as user content while the page renders and translated per post instead), `script`, `style`, `textarea`, `pre`, `code`, `svg`, `math`, `translate="no"` | ✅ |
| Theme and plugin strings (`__`, `_x`, `_n`, theme mods, `wpgo.T`) are listed the first time a page uses them, under "Theme: *name*" or "Plugin: *name*" | ✅ always, whether **Record strings on pages** is on or off |
| **Record strings on pages** (module, on by default): other text found on pages is listed under "Page text" with the URL it was first seen on | ✅ only on pages of the default language (pages of other languages show their own text, which is no source string), while an administrator (`manage_options`) browses the site or in visual translation |
| Language switcher output (language names, with or without flag) | ✅ never recorded as page text |
| Visitors never record page text; search results, 404 pages, previews and Customizer previews never record | ✅ pages echo request data (`?s=`, 404 paths) |
| Limits | at most 500 new strings per request and 5000 per minute per server, strings up to 2000 characters; theme and plugin strings seen by visitors are capped at 200 new per source per hour. Strings are written in batches in the background, never while the page renders |
| Cost for visitors | nothing while the plugin is off or the language has no string translation; otherwise one walk over the page, cached by Pursae Cache. No markup is added: only the text changes |
| **Clean strings translation database** | removes page strings not seen for 30 days that were never translated, and untranslated page strings recorded on a page of another language (URL `/<slug>/…` or `/language/<slug>/…`, recorded before 0.1.9) or that are a language name |
| Not marked as user content yet | the blog title of Twenty Twenty-Five and the aria-label "Reply to *name*" of comment reply links: an administrator's visit can list them as page text |

## Machine translation

The Pro DeepL module, with two more services. It is off by default and the API key is required.
Settings › **Machine translation**:

| Setting | |
|---|---|
| Service | DeepL (Free keys ending in `:fx` use `api-free.deepl.com`, others `api.deepl.com`), Google Cloud Translation (v2, key sent in a header), or an OpenAI-compatible chat completions API (base URL, `https` only, default `https://api.openai.com/v1`; model, default `gpt-4o-mini`) |
| API key | stored sealed (AES-GCM with the site secret), never shown again: the field shows "(saved)"; leaving it empty keeps the key, **Remove** deletes it. Changing the service or the OpenAI-compatible host needs the key typed again, so a saved key is never sent to a new service |
| Formality | Default, More formal, Less formal (DeepL; a hint for chat models) |
| Test connection | translates a short text with the saved settings, even before the module is activated |

Where it is used: the **Suggest** buttons and the two "Translate empty strings" buttons on
Languages › Translations, **Suggest** in visual translation, and **Machine translate the copy**
for new post translations. Language codes come from each language's locale (DeepL `EN-US`/`EN-GB`,
`PT-BR`/`PT-PT`, `ZH`/`ZH-HANT`, `NB`; Google `zh-CN`/`zh-TW`, `pt`/`pt-PT`, `no`, `tl`). Placeholders
such as `%s`, `%1$s` and `{name}` are kept unchanged.

Privacy and security:

- Text is sent to the chosen service only when a manager asks for a translation. Nothing is
  translated automatically, and visitors' pages never call a service.
- Requests go from the server, not the browser (the admin keeps `connect-src 'self'`). The client
  connects only to public addresses, checked after DNS resolution (a custom endpoint pointing at
  `127.0.0.1`, a private network or a cloud metadata address is refused), follows no redirects,
  uses no proxy, times out after 20 s and reads at most 8 MB. Error messages never contain the key.
- The REST proxy `pll/v1/translate` needs `manage_options` and the `wp_rest` nonce, allows 60 calls
  per user per minute, 200 texts and 200 KB per call, and saves nothing.
- Batches follow the services' limits (DeepL 50 texts, Google 128, chat APIs 50).

## API

| Feature | |
|---|---|
| `pll_current_language`, `pll_default_language`, `pll__`, `pll_e`, `pll_register_string`, `pll_translate_string`, `pll_home_url`, `pll_languages_list`, `pll_get_post`, `pll_get_term`, `pll_the_languages` | ✅ as theme template functions that take the view (`.`) first |
| `pll_set_post_language`, `pll_save_post_translations`, `pll_get_post_translations`, `pll_count_posts`, `pll_insert_post` and the rest of the PHP API | ◐ available in Go (`pllSetLang`, `pllSaveTranslations`, `pllTranslations`, `pllGet`) and over REST; no PHP |
| WPML compatibility API and `wpml-config.xml` | ✗ there is no PHP and no plugin or theme `wpml-config.xml` to read |
| REST `pll/v1/languages` (list, get by id or slug, create from a locale with the predefined defaults, update, delete) | ✅ |
| REST `pll/v1/settings` (GET, POST) | ✅ (the machine translation and string capture settings are not exposed there; the API key never is) |
| Gettext template functions for themes: `__`, `_e`, `_x`, `_ex`, `_n`, `esc_html__`, `esc_attr__`, `esc_html_e`, `esc_attr_e`, `esc_html_x`, `esc_attr_x` (view first, optional domain last) | ✅ Pursae addition: site translation, then the language pack, else the text; the text unchanged while the plugin is off. Text and textarea `theme_mod` values are translated too (docs/developer/themes/templates.md) |
| Wasm plugin host import `translate` (SDK `wpgo.T`, `wpgo.X`), no permission needed | ✅ Pursae addition: the domain is the plugin's slug (docs/developer/plugins/host-api.md) |
| REST `pll/v1/media/{id}/i18n` (GET, POST `{"fr":{"alt_text":"…"}}`) and `pll_i18n` on `wp/v2/media` (`context=edit`, updates) | ✅ Pursae addition; needs `upload_files` plus the item's own author or `edit_others_posts`; 404 while the Media module is off |
| REST `pll/v1/translate` (POST `{texts[], target, source?, html?}` → `{translations, target}`) and `pll/v1/translate/test` | ✅ Pursae addition; `manage_options`, cookie auth with the `wp_rest` nonce, rate limited, never saves; 400 `pll_mt_disabled` while the module is not usable |
| REST `pll/v1/visual/strings` (POST `{lang, items:[{original, translation}]}`) | ✅ Pursae addition; `manage_options` and the `wp_rest` nonce; an empty translation removes it; the default language is refused |
| REST `lang` parameter on wp/v2 collections; `lang` and `translations` fields on posts, pages, categories and tags; on writes in the JSON or form body (`{"lang":"fr","translations":{"en":12}}`) or the query string (`?lang=fr&translations[en]=12`) | ✅ |
| Block editor: `apiFetch` middleware adds `lang=<post language>` to the term panels and the parent page selector (follows the Languages panel); the classic editor's category checklist and parent dropdown show the post's language | ✅ |
| WP-CLI `wp pll` | ✗ there is no WP-CLI |
| Language capabilities (`translate_{lang}`, `manage_translations`, `manage_languages`) | ✗ the screens need `manage_options`, as in free Polylang; Pursae has no role editor to grant custom caps |
| Integrations (ACF, Yoast, WooCommerce, page builders) | ✗ not applicable. Pursae Web Builder pages translate like any page: "Duplicate content" copies the builder document and its published snapshot to the new translation (form actions point at it); custom-field synchronization never copies the builder's `_web_builder*` meta. While Pursae Web Builder is on, Theme Builder templates (`wb_template`) are a translated type: each language shows the published translation of the template that wins the display conditions |

## Shared-file changes (kept small for the parallel plugin sessions)

- `plugins.go`: plugin row, activate/deactivate case, icon, reserved slug.
- `admin.go`: one call each in `adminPage` and `adminAjax`.
- `wpgocache.go` (`pluginAdmin`): the `mlang*` pages.
- `server.go`: the request middleware, the REST registration and the link filter in `links()`.
- `settings.go`: `native_golylang: false`.
- `site.go`, `site_canonical.go`, `site_view.go`, `render.go`, `feed.go`: one-line hooks.
- `rest.go`, `rest_posts.go`, `rest_misc.go`: one-line hooks.
- `admin_posts.go`, `admin_terms.go`, `admin_menus.go`, `editor_block.go`: one-line hooks, plus generic `PluginCols`, `PluginHeads`, `PluginFields` and `PluginBoxes` fields that other plugins can reuse. The matching templates print them.
- `internal/links/links.go`: a `LinkFilter` hook and `Front()`.
- `internal/store/posts.go`, `terms.go`: a `QueryFilter` hook.
- `internal/store/media.go`: an `AttachmentFilter` hook and `Attachment.Base` (the media row's texts, which `UpdateAttachment` writes back).
- `site_view.go`, `site_comments.go`, `blocks_core.go`, `theme_twentytwentyfive.go`, `webbuilder_front.go`, `webbuilder_theme_front.go`, `webbuilder_slots.go`, `goo_front_*.go`: user content wrapped in `<!--pll:uc-->` sentinels (only on front requests with a language; `golylangHTML` removes them, so the final page has none).
- `site_images.go`, `media.go`, `admin_media.go`, `rest_misc.go`, `wxr.go`: one-line media hooks; `site_head.go`: the "Translate Page" toolbar node; `theme_package.go`: the `theme_mod` translation hook; `internal/plugins/abi.go`: the `translate` import.
