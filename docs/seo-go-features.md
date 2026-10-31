# SEO-Go: feature map (Yoast SEO Premium + Rank Math Pro → Pursae)

SEO-Go is the native SEO plugin of Pursae. It takes the union of what Yoast SEO (Premium)
and Rank Math (Pro) do and presents it as one plugin with a WordPress-style admin UI. It is
compiled in like Classic Editor, WPGo Cache and Pursae Web Builder. It is **off on a fresh install**, and
while it is off the site, the editors and the admin menu are exactly as without it: its routes
answer 404, nothing is added to `<head>`, and core's `wp-sitemap.xml` and `robots.txt` are served.

Verified reference facts (thresholds, meta keys, URL schemes, with source links) are in
[`seo-go-research.md`](seo-go-research.md). This document maps every feature to both plugins and
says what SEO-Go does with it.

Legend: **✓** implemented · **◐** partly (see note) · **✗** left out (see "Left out" below).

## Design decisions

| Topic | Decision |
|---|---|
| Plugin switch | Option `native_seogo` (Plugins screen, slug `seo-go`). All settings live in one JSON option, `seogo_options` (`seogo.go`). Turning the plugin off keeps settings, meta, redirects and the 404 log. |
| Variables | SEO-Go writes Rank Math's `%var%` syntax and also accepts Yoast's `%%var%%`. Both plugins' names work (`%term%` and `%term_title%`, `%search_query%` and `%searchphrase%`, `%tagline%` and `%sitedesc%`). After replacement, doubled spaces and dangling separators are removed, as both plugins do. |
| Post and term meta | SEO-Go stores its own `_seogo_*` keys. Every read falls back to Yoast's `_yoast_wpseo_*` keys and then to Rank Math's `rank_math_*` keys, including Yoast's 0/1/2 noindex flag and Rank Math's serialized PHP robots arrays. A site imported from WordPress keeps its SEO data with no conversion step. *Tools › Import* and the WXR importer also copy that data into SEO-Go's keys, and the originals stay. |
| Scores | The traffic light uses Yoast's assessments and scores: SEO = Σscore / (n×9) × 100, and readability uses penalty points, giving 30, 60 or 90. Rank Math's tests that Yoast has no counterpart for are listed as unscored "Additional checks". Rank Math's 100-point score is shown next to the SEO heading. |
| Analysis engine | One Go implementation (`seogo_analysis.go`) behind `POST /wp-json/seogo/v1/analyze`, used by both editors, the bulk editor and the stored scores. |
| Editors | One box markup (`seogo/metabox`) and one script (`seogo-editor.js`). In the classic editor it is a meta box at the top of the main column. In Pursae Editor it is a `PluginSidebar` with a toolbar icon and a More-menu item, plus a "SEO-Go" document settings panel with both scores. |
| Redirect storage | The core `redirects` table, extended by migration `0010_seogo.sql` (match type, status, category, origin, last hit). A new `seogo_404` table holds the 404 log. |
| Sitemaps | Yoast's URL scheme (`/sitemap_index.xml`, `/{type}-sitemap{N}.xml`, `/main-sitemap.xsl`), 1000 URLs per file (configurable), with image entries. `/sitemap.xml` and every `/wp-sitemap*.xml` redirect (301) to the index, as Yoast does. |

## Feature map

### Setup and dashboard

| Feature | Yoast | Rank Math | SEO-Go |
|---|---|---|---|
| Setup wizard | First-time configuration (site representation, social profiles, personal preferences) | Setup Wizard (site type, logo, Search Console, sitemaps, optimization, role manager) | ✓ Five steps: site representation (organization or person, logo, separator), social profiles, search engines (sitemaps, redirections, 404 monitor, breadcrumbs, IndexNow, single-author site, empty terms), import from Yoast/Rank Math, done. It opens on the first activation. |
| Dashboard | SEO and readability score overview, problems and notifications | Modules grid, Analytics | ✓ SEO and readability distribution of published content, cornerstone count, orphaned content, redirections, logged 404s, SEO problems (search engines discouraged, plain permalinks, default tagline), sitemap link. |
| Modules on/off | (feature toggles) | Modules screen | ✓ Content Analysis, Schema, XML Sitemaps, Breadcrumbs, Redirections, 404 Monitor, Link Counter, Image SEO, Local SEO, News Sitemap, Video Sitemap, Instant Indexing, Role Manager. A module that is off adds nothing. |
| Role manager | — | Role Manager (`rank_math_*` capabilities) | ✓ `seogo_*` capabilities per role (Search Appearance, General, Sitemaps, 404 Monitor, Redirections, Link Builder, Role Manager, and On-Page Analysis/General/Advanced/Schema/Social). Administrators always have all of them. The menu, screens, REST routes and editor tabs check them. |

### Per-post SEO box (classic editor meta box + Pursae Editor sidebar)

| Feature | Yoast | Rank Math | SEO-Go |
|---|---|---|---|
| Focus keyphrase | Focus keyphrase (+ Premium related keyphrases) | Focus keyword (+ secondary keywords) | ✓ A comma list; the first entry is the focus keyphrase, the others are related keyphrases (checked in the text, unscored). |
| Snippet preview | Google preview, mobile and desktop | Google preview, desktop and mobile | ✓ Mobile and desktop, server-resolved title and description, breadcrumb-style URL, keyphrase highlighted, length/width bars. |
| SEO title and meta description with variables | ✓ | ✓ | ✓ An "Insert variable" menu; empty fields fall back to the Search Appearance template. |
| SEO analysis | 17 assessments | Basic SEO, Additional, Title Readability tests | ✓ All of Yoast's (keyphrase in introduction, length, density with the keyphrase-length factor, in meta description, in subheading, in SEO title, in slug, meta description length, SEO title width, text length for post/cornerstone/taxonomy, outbound and internal links, images, image keyphrase, single H1, competing links, previously used keyphrase) plus Rank Math's keyword in first 10%, URL length, number in title, power words, short paragraphs, table of contents. |
| Readability analysis | Subheading distribution, paragraph length, sentence length, transition words, passive voice, consecutive sentences, text presence; Flesch as an insight | Content readability tests, legacy Flesch | ✓ Yoast's 7 assessments with Yoast's thresholds, including the cornerstone variants. Flesch reading ease and reading time are shown as insights, as Yoast does since 19.3. English only. |
| Traffic lights | Bullets in box, posts list, admin bar | Score badge | ✓ Bullets in the box header, in the Pursae Editor document panel, in the bulk editor and on the dashboard. Scores are stored in `_seogo_seo_score`, `_seogo_read_score` and `_seogo_rm_score`. |
| Cornerstone / pillar content | Cornerstone (stricter analysis) | Pillar content | ✓ Stricter thresholds; shown first in internal linking suggestions; starred in the Link Counter. |
| Canonical URL | ✓ | ✓ | ✓ |
| Robots meta | noindex, nofollow, advanced (noarchive, nosnippet, noimageindex) | index/noindex, nofollow, noarchive, noimageindex, nosnippet, max-snippet, max-video-preview, max-image-preview | ✓ All of them; site-wide limits default to `max-snippet:-1, max-image-preview:large, max-video-preview:-1`. |
| Breadcrumbs title | ✓ | ✓ | ✓ |
| Primary category | ✓ | ✓ (Pro) | ✓ Used in breadcrumbs and `%primary_category%`. |
| Social previews | Facebook and X previews with image, title, description | Same | ✓ Open Graph preview card; OG and X title, description and image (Media Library picker). |
| Schema tab | Page type, article type | Schema generator, templates | ◐ Page type (WebPage, AboutPage, FAQPage…), article type (Article, BlogPosting, NewsArticle…, None), **Product** (price, currency, SKU, brand, availability, rating) and **custom JSON-LD**. No visual schema builder or templates. |
| Internal linking suggestions | Premium | Link suggestions (Pro) | ✓ Full-text related content (cornerstone first); "Copy link"; already linked items marked. |

### Site-wide settings

| Feature | Yoast | Rank Math | SEO-Go |
|---|---|---|---|
| Search appearance per type | Content types, taxonomies, archives, special pages | Titles & Meta | ✓ Home, posts, pages, categories, tags, author archives, date archives, search, 404: title and description templates, noindex, sitemap inclusion, schema defaults (posts/pages), disable author/date archives (301 to home). |
| Separator | 14 choices | ✓ | ✓ The same 14 characters. |
| Knowledge Graph / site representation | Organization or person, logo, other profiles | Local SEO & Knowledge Graph | ✓ Organization (name, alternate name, logo) or person (a user), social profiles as `sameAs`. |
| Social settings | Facebook (default image, front page), X card type, other profiles | Same | ✓ Open Graph on/off, X cards on/off, card type, default image, home image, Facebook page URL and app ID, X username. |
| Webmaster verification | Google, Bing, Baidu, Yandex | Google, Bing, Baidu, Yandex, Pinterest, Norton | ✓ Google, Bing, Baidu, Yandex, Pinterest, Naver. Accepts the bare code or the whole `<meta>` tag. Output only on the home page. |
| RSS before/after content | ✓ (`%%POSTLINK%%`, `%%BLOGLINK%%`…) | ✓ | ✓ `%postlink%`, `%bloglink%`, `%blogdesclink%`, `%authorlink%` plus all variables. |
| robots.txt editor | File editor | Edit robots.txt | ✓ Tools › robots.txt editor, served virtually; the default adds the sitemap index. |
| .htaccess editor | ✓ | ✓ | ✗ Not applicable: Pursae serves every request itself. |
| Noindex empty terms / paginated pages | — | ✓ | ✓ |
| Import/export settings | ✓ | ✓ | ✓ JSON file. |

### Head output and schema

| Feature | Yoast | Rank Math | SEO-Go |
|---|---|---|---|
| Title tag | ✓ | ✓ | ✓ Replaces `wp_get_document_title()`. |
| Meta description, robots, canonical, rel prev | ✓ | ✓ | ✓ Core's robots meta and canonical link are replaced (one canonical); no canonical on noindexed pages. |
| Open Graph, article times, image size/type | ✓ | ✓ | ✓ |
| X cards, "Written by" and "Est. reading time" labels | ✓ | ✓ | ✓ |
| Schema graph | WebSite, Organization/Person, WebPage, ImageObject, BreadcrumbList, Article, Person, with Yoast's @id scheme | Similar | ✓ Yoast's @ids (`#website`, `#organization`, `#/schema/person/<hash>`, the permalink for WebPage, `#article`, `#breadcrumb`, `#primaryimage`). CollectionPage, ProfilePage and SearchResultsPage on archives. The person hash is stable (md5 of login + "user"), not Yoast's salted `wp_hash`. |
| FAQ block | FAQ block | FAQ block | ✓ `seogo/faq` block, which adds FAQPage and Question nodes. Yoast's `yoast/faq-block` and Rank Math's `rank-math/faq-block` in imported content are read too. |
| How-to block | How-to block | HowTo block | ✓ `seogo/howto` block and the imported `yoast/how-to-block` and `rank-math/howto-block`. |
| Product schema | (WooCommerce SEO) | ✓ | ✓ Per post (article type Product). |
| LocalBusiness | Local SEO add-on | Local SEO | ✓ Local SEO module: business type, address, phone, email, price range, coordinates, opening hours. The organization node becomes a LocalBusiness. |
| Custom JSON-LD | — | Custom schema (Pro) | ✓ Per post: a node, a list or `@graph`. |

### Breadcrumbs

| Feature | Yoast | Rank Math | SEO-Go |
|---|---|---|---|
| Settings | Separator, home anchor, prefixes, 404 text, bold last, blog page | Same, plus hide title | ✓ All of them. |
| Output | `yoast_breadcrumb()`, shortcode, block | `rank_math_the_breadcrumbs()`, shortcode, block | ✓ The `seogo/breadcrumbs` block (and `yoast-seo/breadcrumbs`). The `[seogo_breadcrumb]`, `[wpseo_breadcrumb]` and `[rank_math_breadcrumb]` shortcodes. BreadcrumbList schema. ◐ No PHP-style template function: Pursae themes are compiled, so the block and shortcode take its place. |

### Sitemaps, robots and indexing

| Feature | Yoast | Rank Math | SEO-Go |
|---|---|---|---|
| XML sitemap index and per-type sitemaps | ✓ | ✓ | ✓ post, page, category, post_tag and author sitemaps; paged at the configured size; lastmod; image entries (featured image and content images). The XSL stylesheet shows them in a browser. |
| Exclusions | noindex, per type | Per post/term IDs, per type | ✓ Noindexed, password-protected and canonicalized-elsewhere content, excluded post and term IDs, types switched off in Search Appearance, empty terms. |
| Replace core sitemaps | Disables `wp-sitemap.xml` and redirects | — | ✓ 301 from `/wp-sitemap*.xml` and `/sitemap.xml` to `/sitemap_index.xml`. |
| News sitemap | News SEO add-on | Pro | ✓ `news-sitemap.xml`: posts of the last 48 hours, publication name and language. |
| Video sitemap | Video SEO add-on | Pro | ◐ `video-sitemap.xml` for YouTube/Vimeo embeds, with title, description, thumbnail and player URL. No duration or video metadata lookup. |
| IndexNow | Yoast IndexNow (Premium) | Instant Indexing | ✓ Key file at `/<key>.txt`, automatic submission on publish/update, manual submission, last 20 responses, key regeneration. |

### Redirections and 404 monitor

| Feature | Yoast | Rank Math | SEO-Go |
|---|---|---|---|
| Redirect types | 301, 302, 307, 410, 451 | 301, 302, 307, 410, 451 | ✓ |
| Matching | Plain, regex | Exact, contains, starts with, ends with, regex | ✓ All five; regex targets may use `$1`, `$2`… |
| Manager | List, add, edit, delete, filter | Same, plus categories, active/inactive, hit counter | ✓ Status filter, bulk activate/deactivate/delete, category, hits, last accessed, origin (manual, auto, import). |
| Import/export | CSV | CSV / Redirection plugin | ✓ CSV import (file or pasted) and CSV export. |
| Automatic redirect on slug change | Premium | Auto post redirect | ✓ For published posts and pages. A redirect away from the post's current address is removed. |
| 404 monitor | — | Simple and advanced mode | ✓ URI, hits, last referer and user agent, last seen. Search, delete, clear, "Redirect" button that prefills the form, size limit, ignore list. |
| Fallback for 404s | — | Redirect all 404s | ✓ An optional URL that every 404 is sent to. |

### Links and bulk editing

| Feature | Yoast | Rank Math | SEO-Go |
|---|---|---|---|
| Link counter | Text link counter (internal/received) | Link counter | ✓ Internal, outbound and received links per post and page. |
| Orphaned content | Premium | — | ✓ Filter of the Link Counter, count on the dashboard. |
| Bulk editor | Tools › Bulk editor (titles, descriptions) | Bulk edit in posts list | ✓ SEO title, meta description and focus keyphrase for all posts or pages on one screen, with SEO and readability scores. |

### Images, import, REST

| Feature | Yoast | Rank Math | SEO-Go |
|---|---|---|---|
| Image SEO (missing alt/title) | — | Image SEO (Pro) | ✓ Module: fills empty alt and missing title attributes in rendered content from templates (`%filename%`, `%title%`…). Tools: writes alt texts into the Media Library. |
| Import from Yoast / Rank Math | Import from other plugins | Import from Yoast and others | ✓ Data is read in place, copied during WXR import, and copied for all content in Tools. Redirects come in through CSV. |
| REST head endpoint | `GET /wp-json/yoast/v1/get_head?url=` → `{html, json, status}` | `GET /wp-json/rankmath/v1/getHead?url=` → `{success, head}` | ✓ Both, with the same shapes, plus `/wp-json/seogo/v1/get_head`. Only for URLs of this site; unpublished or protected content answers like a 404. |
| Term SEO fields | ✓ | ✓ | ✓ Edit Category / Edit Tag: focus keyphrase, title, description, noindex, canonical, breadcrumbs title, social fields. |

## Left out (and why)

- **Rank Math Analytics, Search Console, keyword rank tracking, Content AI, Yoast AI features, SEMrush/Wincher integrations.** These need external accounts and services; out of scope for a self-contained single site.
- **Yoast Insights, word complexity, inclusive language, text alignment, keyphrase distribution, Premium word forms and synonyms.** The analysis uses simple English stemming. Separate synonym fields and non-English language rules are not implemented.
- **Visual schema generator and schema templates (Rank Math Pro).** Covered by page/article type, Product, the FAQ and How-to blocks and custom JSON-LD.
- **`yoast_head` / `yoast_head_json` fields on `wp/v2` responses.** Adding them would change core REST output; headless clients use the get_head endpoints instead.
- **SEO columns and filters in the posts list, the admin bar SEO menu, the per-post redirect field, author-profile SEO fields, crawl optimization (removing feeds, shortlinks…), social image overlays, `llms.txt`, multiple local locations / KML.** The bulk editor and dashboard cover the list use; the rest is small and can follow.
- **SEO analysis on the term edit screen.** Terms get the fields and head output, not the live traffic lights.
- **.htaccess editor.** Not applicable to Pursae.

## Where the code is

| File | Contents |
|---|---|
| `internal/server/seogo.go` | Switch, options and defaults, modules, capabilities, Plugins row, admin menu |
| `internal/server/seogo_meta.go` | Field keys with Yoast/Rank Math fallbacks, import |
| `internal/server/seogo_vars.go` | Context and replacement variables |
| `internal/server/seogo_head.go` | Title, description, robots, canonical, Open Graph, X, verification |
| `internal/server/seogo_schema.go` | JSON-LD graph, FAQ/How-to blocks, Product, LocalBusiness, custom |
| `internal/server/seogo_breadcrumbs.go` | Trail, HTML, shortcodes, blocks |
| `internal/server/seogo_front.go` | Redirections, robots.txt, sitemaps, 404 monitor, RSS, IndexNow |
| `internal/server/seogo_analysis.go` | SEO and readability analysis, Rank Math points |
| `internal/server/seogo_editor.go` | Editor box, REST routes (analyze, meta, link suggestions, get_head), term fields |
| `internal/server/seogo_admin.go` | Admin screens, wizard, import, bulk editor, Image SEO |
| `web/admin/templates/pages/seogo.html` | Admin screens, the editor box, the term box |
| `web/wp-admin/js/seogo-*.js`, `web/wp-admin/css/seogo.css` | Editor box, Pursae Editor blocks, admin helpers, styles |
| `internal/db/migrations/0010_seogo.sql` | Redirects columns, 404 log |

Hooks into shared files are one or two lines each and are marked "SEO-Go". They are in
`plugins.go`, `admin.go`, `server.go`, `site.go`, `site_head.go`, `site_view.go`, `feed.go`,
`admin_posts.go`, `rest_posts.go`, `admin_terms.go` and `wxr.go`.
