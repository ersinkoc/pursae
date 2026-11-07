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
`internal/db/migrations/0009_golylang.sql`. Tests: `internal/server/golylang_test.go`.

## Data model

| Polylang | Pursae Multi Language | |
|---|---|---|
| `language` taxonomy: name, slug, locale, rtl, flag, order (`term_group`) | `pll_languages` table, plus Pro's `active` flag | ✅ |
| `post_language` and `post_translations` (one group term per translation set) | `pll_post_langs` (`post_id`, `lang`, `grp`) | ✅ |
| `term_language` and `term_translations` | `pll_term_langs` | ✅ |
| Media languages (attachments) | `pll_media_langs` | ◐ the table, the setting and sync mapping are in; there is no Media Library UI (see Media) |
| `polylang_mo` posts for string translations | `pll_strings` (`lang`, `original`, `translation`) | ✅ |
| Option `polylang` | Option `golylang`, same keys: `force_lang`, `hide_default`, `rewrite`, `redirect_lang`, `browser`, `media_support`, `post_types`, `taxonomies`, `sync`, `domains`, `default_lang`, `nav_menus`, plus Pro `media_duplicate` | ✅ |
| User meta `pll_filter_content` (admin language filter) | Same key in the user's meta | ✅ |
| Cookie `pll_language`, 1 year, SameSite Lax | Same | ✅ |
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
| **Media**: translate media, Pro "automatically duplicate media in all languages" | ◐ the setting, the media language table and featured-image mapping in sync are in; the Media Library language column, the attachment translation UI and duplication at upload are not (Pursae's media screens are not hooked) |
| **Custom post types and Taxonomies**: checkbox lists of non-builtin public types and taxonomies | ✅ |
| **Synchronization**: Taxonomies, Custom fields, Comment status, Ping status, Sticky posts, Published date, Post format, Page parent, Page template, Page order, Featured image | ✅ terms, parent and featured image are mapped to their translations |
| Pro **Translate slugs**: translated category, tag, author and search bases per language (strings group "URL slugs") | ✅ |
| Pro **Share slugs** | ✓ Settings › Share slugs: translations may use the same slug; uniqueness is checked per language and URLs resolve to the request language first (needs the language from the URL; migration 0020 moves slug uniqueness from the database to the store, like WordPress) |
| Pro **Machine Translation (DeepL)** | ✗ it needs a paid third-party account and sends site content out; the module is listed with that reason |

## Languages > Translations (strings)

| Feature | |
|---|---|
| Table with checkbox, String, Name, Group, Translations (one input or textarea per language), search "Search translations", "Filter by group" ("View all groups"), pagination | ✅ |
| Built-in strings: Site Title and Tagline (group WordPress) | ✅ translated on the front end |
| Date Format and Time Format strings | ✗ Pursae formats dates without a request context, so there is no place to apply them |
| Widget titles and text (legacy widgets) | ✗ Pursae only has block widgets |
| Strings registered by themes (`pll_register_string`, `pll__`, `pll_e`) | ✅ registered on first use (group "Theme"), kept in the option `golylang_strings` |
| Bulk Delete for registered strings (WordPress and URL slugs strings are greyed) | ✅ |
| "Clean strings translation database" | ✅ |
| Admin language filter limits the columns to one language | ✅ |
| Pro: export (Target language, group, Download) and import (Upload) | ✅ in PO format, with the `Language` and `Site-Reference` headers; XLIFF is left out |

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
| Per-language menus | ✅ |
| Widgets restricted to one language | ✗ Pursae's block widgets have no per-widget settings UI to hook |
| Auto-translation of IDs in queries (`PLL_AUTO_TRANSLATE`) | ✗ themes in Pursae don't query by hard-coded IDs |

## API

| Feature | |
|---|---|
| `pll_current_language`, `pll_default_language`, `pll__`, `pll_e`, `pll_register_string`, `pll_translate_string`, `pll_home_url`, `pll_languages_list`, `pll_get_post`, `pll_get_term`, `pll_the_languages` | ✅ as theme template functions that take the view (`.`) first |
| `pll_set_post_language`, `pll_save_post_translations`, `pll_get_post_translations`, `pll_count_posts`, `pll_insert_post` and the rest of the PHP API | ◐ available in Go (`pllSetLang`, `pllSaveTranslations`, `pllTranslations`, `pllGet`) and over REST; no PHP |
| WPML compatibility API and `wpml-config.xml` | ✗ there is no PHP and no plugin or theme `wpml-config.xml` to read |
| REST `pll/v1/languages` (list, get by id or slug, create from a locale with the predefined defaults, update, delete) | ✅ |
| REST `pll/v1/settings` (GET, POST) | ✅ |
| REST `lang` parameter on wp/v2 collections; `lang` and `translations` fields on posts, pages, categories and tags; `?lang=fr&translations[en]=12` on writes | ✅ |
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
