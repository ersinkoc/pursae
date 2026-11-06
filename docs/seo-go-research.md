# Pursae SEO research: Yoast SEO (Premium) + Rank Math (Pro) reference facts

Collected 2026-10-28 from official docs and the plugins' GitHub sources (`Yoast/wordpress-seo@trunk`, `rankmath/seo-by-rank-math@master`).
Source code beats docs where they disagree, and those cases are flagged. **unverified** marks a value from memory or secondary sources that I couldn't confirm in a primary source.

Key source files:
- Yoast analysis: `packages/yoastseo/src/scoring/assessments/{seo,readability}/*.js`, `packages/yoastseo/src/scoring/assessors/*.js`, `packages/yoastseo/src/scoring/assessments/SCORING SEO.md`, `SCORING READABILITY.md`
- Rank Math analysis: `assets/admin/src/analyzer/analysis/*.js`, `assets/admin/src/analyzer/ResultManager.js`

---

## 1. Yoast SEO analysis

### 1.1 Score → traffic light → overall score

| Layer | Rule | Source |
|---|---|---|
| Per-assessment score → rating | `-1`=error, `0`=feedback (grey), `≤4`=bad (red), `5–7`=ok (orange), `>7` (8–9)=good (green) | `scoring/interpreters/scoreToRating.js` |
| SEO overall (0–100) | `round( Σscores / (nValidResults × 9) × 100 )`, or 0 if there are no valid results. Negative scores (-10, -20, -50, -999) pull the total down hard | `scoring/assessors/assessor.js` |
| Readability overall (content assessor) | **Not** an average. It uses penalty points: bad=3, ok=2, good=0 (with partial language support: bad=4, ok=2). Full support: total >6 → 30, >4 → 60, else 90. Partial: >4 → 30, >2 → 60, else 90. Exactly one valid result → 30 | `scoring/assessors/contentAssessor.js` |
| 0–100 → bullet (server side) | `na` (no focus) = 0, `bad` = 1–40, `ok` = 41–70, `good` = 71–100, `noindex` = separate state. Fractional gaps such as 40.5 fall back to bad | `inc/class-wpseo-rank.php` |
| Stored | `_yoast_wpseo_linkdex` (SEO 0–100), `_yoast_wpseo_content_score` (readability), `_yoast_wpseo_inclusive_language_score` | `inc/class-wpseo-meta.php` |

### 1.2 Which assessments run where

| Assessor | Assessments |
|---|---|
| Regular post/page (`seoAssessor.js`, 17) | Keyphrase in introduction, Keyphrase length, Keyphrase density, Keyphrase in meta description, Meta description length, Keyphrase in subheading, Competing links, Image keyphrase, Images (count), Text length, Outbound links, Keyphrase in SEO title, Internal links, SEO title width, Keyphrase in slug, Function words in keyphrase, Single title (H1). "Previously used keyphrase" is added by the WP plugin layer |
| Cornerstone (`assessors/cornerstone/seoAssessor.js`) | Same set, with stricter overrides (see below) |
| Taxonomy (`taxonomyAssessor.js`, 11) | Intro, keyphrase length, density, meta desc keyphrase, meta desc length, text length (taxonomy thresholds), keyphrase in SEO title, title width, slug, function words, single H1 (+ previously used). No images, links, competing links or subheading keyphrase |
| Related keyphrases (Premium) | Intro, length, density, meta desc, image keyphrase, competing links, previously used. Taxonomy variant drops image keyphrase and competing links |
| Premium extras | Keyphrase distribution, Title (`TextTitleAssessment`); readability: Word complexity, Text alignment |
| Readability (`contentAssessor.js`, 7) | Subheading distribution, Paragraph length, Sentence length, Transition words, Passive voice, Text presence, Consecutive sentences (SentenceBeginnings) |

### 1.3 SEO assessments: exact thresholds (score in parentheses)

| Assessment | Red | Orange | Green | Cornerstone / notes |
|---|---|---|---|---|
| **Keyphrase in introduction** | no keyphrase/content (3); not all content words in first paragraph (3) | all content words in first paragraph, but not in one sentence (6) | all content words in one sentence of the first paragraph (9) | Synonyms count |
| **Keyphrase length** (content words) | not set (-999); >8 words (3) | 5–8 words (6) | 1–4 words (9) | Languages without function-word support: green 1–6, orange 7–9, red >9. Config `recommendedMaximum 4, acceptableMaximum 8` |
| **Keyphrase density** (text ≥100 words) | no keyphrase/text (-50); 0 occurrences (4); <0.5% (4); >3% to 4% (-10); >4% (-50) | — | 0.5–3% (9) | With morphology (Premium word forms) green goes up to **3.5%**. The density is multiplied by `keyphraseLengthFactor = 0.7 + nWords/3` before comparison, so longer keyphrases need fewer occurrences. The doc says the step size for a 1-word keyphrase is 214 words |
| Density, text 51–99 words | 3× (-10); ≥4× (-50) | — | 1–2× (9) | count based |
| Density, text <51 words | 2× (-10); ≥3× (-50) | — | 1× (9) | count based |
| **Keyphrase in meta description** | none/no desc (3); 0 matches (3); >2 sentences match (3) | — | 1–2 sentences (9) | Synonyms count |
| **Meta description length** | empty (1) | ≤120 chars (6); >156 chars (6) | 121–156 chars (9) | Cornerstone: too short/long = **3 (red)**. JA: 60/80. Length includes the date + separator if the date shows in the snippet |
| **SEO title width** | empty (1); **>600 px (3)** | — | 1–600 px (9) | Config min 400, max 600 px, but the regular, cornerstone and taxonomy assessors all override `widthTooShort: 9`, so a short title is still green. Measured without separator + site name |
| **Text length** (post) | 0–99 (-20); 100–199 (-10); 200–249 (3) | 250–299 (6) | ≥300 (9) | |
| Text length (cornerstone) | <400 (-20) | 400–899 (6) | **≥900 (9)** | override `recommendedMinimum 900, slightlyBelow 400, below 300` |
| Text length (taxonomy) | 0 words (-20); 1–9 (3) | 10–29 (6) | **≥30 words (9)** | override `recommendedMinimum 30, slightlyBelowMinimum 10, veryFarBelowMinimum 1` |
| **Outbound links** | none (3) | all nofollow (7) | mixed (8); all followed (9) | Note: 7 counts as "ok" |
| **Internal links** | none (3) | only nofollow (7) | mixed (8); all followed (9) | |
| **Images** | no images (3) | — | ≥1 (9) | |
| **Image keyphrase (alt)** | no images or no keyphrase (3) | images but no alt matches (6); ≥5 images and <30% or >75% match (6) | <5 images and ≥1 match; ≥5 images and 30–75% match (9) | A match needs ≥50% of the content words. Cornerstone: "no match" = 3 (red). Not on taxonomies |
| **Keyphrase in subheading** (H2/H3) | no keyphrase (1); >300 words and no subheading (2); <30% or >75% of H2/H3 match (3) | — | 30–75% match, or the only H2/H3 matches, or ≤300 words with no subheading (9) | Cornerstone: 250 words. A subheading matches when ≥half of the keyphrase words are present |
| **Keyphrase in SEO title** | none (2); not all words and not at start (2) | exact match but not at the beginning (6); no exact match (6) | exact match at the beginning (9) | Leading function words are ignored. No synonyms |
| **Keyphrase in slug** | none (3) | not all content words (6) | 1–2-word keyphrase: all words; >2 words: more than half (9) | Cornerstone: orange becomes 3 (red) |
| **Previously used keyphrase** | not set (1); used >1× before (1) | used once before (6) | never used (9) | |
| **Single title (H1)** | ≥2 H1s in the body (1) | — | <2 (8) | |
| **Competing links** | a link anchor = keyphrase/synonym (2) | — | none (8) | Exact content-word match only |
| Function words in keyphrase | grey (0) when the keyphrase is only function words | | | |
| Keyphrase distribution (Premium) | not found (1); score >0.5 (1) | 0.3–0.5 (6) | <0.3 (9); <15 sentences and found ≥1 (9) | score = longest gap without keyphrase / total sentences |
| Title (Premium) | no title (-10000) | — | present (9) | |

### 1.4 Readability assessments

| Assessment | Red | Orange | Green | Cornerstone / notes |
|---|---|---|---|---|
| **Subheading distribution** | no subheadings and >300 words (2); a section >350 words (3) | a section 300–350 words (6) | all sections <300, or ≤300 words with no subheading (9) | Cornerstone: 250 / 300 |
| **Paragraph length** | >200 words (3) | 150–200 (6) | ≤150 (9) | JA: 300/400 chars |
| **Sentence length** (too long = >20 words) | >30% (3) | 25–30% (6) | ≤25% (9) | Cornerstone: green ≤20%, red >25%. Limit is 25 words for ca/es/fa/it/pt, 15 for he/ru/tr, 40 chars for ja |
| **Transition words** (text >200 words) | none (3); <20% of sentences (3) | 20–30% (6) | ≥30% (9) | Short texts are always green |
| **Passive voice** | >15% (3) | 10–15% (6) | ≤10% (9) | |
| **Consecutive sentences** | ≥3 consecutive sentences start with the same word (3) | — | otherwise (9) | |
| Text presence | <50 chars (3) | | | |
| Word complexity (Premium) | — | >10% complex words (6) | <10% (9) | Cornerstone: 3 (red) |
| Text alignment (Premium) | center-aligned block >50 chars (2) | | | |
| **Flesch reading ease** | **Removed from the readability analysis in Yoast 19.3 (July 2022).** It is now only shown in the Insights tab, English only | | | Bands (yoast.com): 90–100 very easy, 80–89 easy, 70–79 fairly easy, 60–69 standard ("OK"), 50–59 fairly difficult, 30–49 difficult, 0–29 very confusing. Formula 206.835 − 1.015·ASL − 84.6·ASW |

Sources:
- https://github.com/Yoast/wordpress-seo/blob/trunk/packages/yoastseo/src/scoring/assessments/SCORING%20SEO.md
- https://github.com/Yoast/wordpress-seo/blob/trunk/packages/yoastseo/src/scoring/assessments/SCORING%20READABILITY.md
- https://github.com/Yoast/wordpress-seo/tree/trunk/packages/yoastseo/src/scoring/assessments/seo (KeywordDensityAssessment.js, MetaDescriptionLengthAssessment.js, TextLengthAssessment.js, PageTitleWidthAssessment.js, KeyphraseLengthAssessment.js)
- https://github.com/Yoast/wordpress-seo/blob/trunk/packages/yoastseo/src/scoring/helpers/assessments/keyphraseLengthFactor.js
- https://github.com/Yoast/wordpress-seo/tree/trunk/packages/yoastseo/src/scoring/assessors (seoAssessor.js, cornerstone/seoAssessor.js, taxonomyAssessor.js, contentAssessor.js, assessor.js, ASSESSORS OVERVIEW.md)
- https://github.com/Yoast/wordpress-seo/blob/trunk/inc/class-wpseo-rank.php
- https://yoast.com/flesch-reading-ease-score/ and https://yoast.com/yoast-seo-july-12-2022/

---

## 2. Rank Math content analysis

### 2.1 Scoring model
- Each test has a **max score (points)**. The total is `round( Σearned / Σmax × 100 )`, computed only over the tests that apply (`ResultManager.refreshScore`). Every value can be overridden with a filter, `rankMath_analysis_<test>_score`.
- Colour bands (KB): **green ≥81, yellow 51–80, red ≤50**. Stored in post meta `rank_math_seo_score`.
- Only the primary keyword's score counts toward the overall score.

### 2.2 Tests, thresholds and max points (source defaults)

| Group | Test id | Rule | Max pts |
|---|---|---|---|
| Basic SEO | `keywordInTitle` | Focus keyword in SEO title | **36 (en), 38 (other locales)** |
| Basic | `keywordInMetaDescription` | keyword in meta description | 2 |
| Basic | `keywordInPermalink` | keyword in URL slug | 5 |
| Basic | `keywordIn10Percent` | keyword in first 10% of content. **Source: only truncated when content is >400 words**, otherwise the whole text is checked (the KB says 300) | 3 |
| Basic | `keywordInContent` | keyword appears in content | 3 |
| Basic | `lengthContent` | words ≥2500 → 8; 2000–2499 → 5; 1500–1999 → 4; 1000–1499 → 3; 600–999 → 2; <600 → 0 | 8 |
| Additional | `keywordInSubheadings` | keyword in an H2–H6 | 3 |
| Additional | `keywordInImageAlt` | keyword in an `<img alt>` | 2 |
| Additional | `keywordDensity` | <0.5% → 0; 0.5–0.75 → 2; 0.76–1.0 → 3; >1.0–2.5 → 6 (best); >2.5% → 0 | 6 |
| Additional | `lengthPermalink` | full URL ≤75 chars | 4 |
| Additional | `linksHasExternals` | ≥1 external link | 4 |
| Additional | `linksNotAllExternals` | ≥1 dofollow external link | 2 |
| Additional | `linksHasInternal` | ≥1 internal link | 5 |
| Additional | `keywordNotUsed` | keyword not used as focus keyword elsewhere (AJAX `rank_math_is_keyword_new`) | no own max score in source (unverified: probably 0) |
| Additional | `hasContentAI` | Content AI used | 5 |
| Additional (Pro, Woo/EDD) | `hasProductSchema` / `isReviewEnabled` | product schema / reviews enabled | 2 / 2 |
| Title readability | `titleStartWithKeyword` | keyword position < half of the title length | 3 |
| Title readability | `titleSentiment` | positive/negative sentiment (English only) | 1 |
| Title readability | `titleHasPowerWords` | contains a word from the power-word list | 1 |
| Title readability | `titleHasNumber` | contains a number | 1 |
| Content readability | `contentHasTOC` | TOC block or known TOC plugin detected | 2 |
| Content readability | `contentHasShortParagraphs` | no paragraph **>120 words** | 3 |
| Content readability | `contentHasAssets` | images: 0→0, 1→1, 2→2, 3→4, ≥4→6; videos: 0→0, 1→1, ≥2→2; total capped at 6 | 6 |
| (legacy, not in the default list) | `fleschReading` | ≥90 → 6, 80–89 → 5, 70–79 → 5, 60–69 → 4, 50–59 → 3, 30–49 → 2, <30 → 1 | 6 |

- The KB documents density as "1–1.5% best", but the source gives full points for anything from 1.0% to 2.5%.
- The KB documents content length as percentages (2500+ = 100%, 2000 = 70%, 1500 = 60%, 1000 = 40%, 600 = 20%). The source points are 8/5/4/3/2 out of 8.
- **Excluded on the homepage:** TOC, keywordInPermalink, lengthPermalink, external links, title sentiment/power/number.
- **Excluded on products:** TOC, short paragraphs, 10%, keyword in content, subheadings, density, length, internal/external links.
- **Default enabled tests:** see `includes/admin/metabox/class-post-screen.php::get_analysis()`.

Sources:
- https://rankmath.com/kb/score-100-in-tests/
- https://github.com/rankmath/seo-by-rank-math/tree/master/assets/admin/src/analyzer/analysis
- https://github.com/rankmath/seo-by-rank-math/blob/master/assets/admin/src/analyzer/ResultManager.js
- https://github.com/rankmath/seo-by-rank-math/blob/master/includes/admin/metabox/class-post-screen.php

---

## 3. Title/description variables

### 3.1 Yoast (`%%var%%`)
Retrievers in `inc/class-wpseo-replace-vars.php`:

| Group | Variables |
|---|---|
| Basic | `%%date%% %%title%% %%parent_title%% %%archive_title%% %%sitename%% %%sitedesc%% %%excerpt%% %%excerpt_only%% %%tag%% %%category%% %%primary_category%% %%category_description%% %%tag_description%% %%term_description%% %%term_title%% %%searchphrase%% %%sep%%` |
| Advanced | `%%pt_single%% %%pt_plural%% %%modified%% %%id%% %%name%% %%user_description%% %%userid%% %%page%% %%pagetotal%% %%pagenumber%% %%caption%% %%focuskw%% %%term404%% %%currentdate%% %%currentday%% %%currentmonth%% %%currentyear%% %%currenttime%% %%post_year%% %%post_month%% %%post_day%% %%author_first_name%% %%author_last_name%% %%permalink%% %%post_content%% %%category_title%% %%term_hierarchy%%` |
| Dynamic | `%%cf_<field>%%`, `%%ct_<taxonomy>%%`, `%%ct_desc_<taxonomy>%%` |

- `%%page%%` renders as "Page 2 of 4" and is empty on page 1. `%%name%%` is the author display name.
- `rssafter` uses `%%POSTLINK%% %%BLOGLINK%%`.

**Separator** (`separator` default `sc-dash`): sc-dash `-`, sc-ndash `–`, sc-mdash `—`, sc-colon `:`, sc-middot `·`, sc-bull `•`, sc-star `*`, sc-smstar `⋆`, sc-pipe `|`, sc-tilde `~`, sc-laquo `«`, sc-raquo `»`, sc-lt `>`, sc-gt `<`.
- Note: in the source, `sc-lt` maps to `&#062;` (`>`) and `sc-gt` maps to `&#060;` (`<`). The names are swapped relative to their output.

**Default title templates** (`inc/options/class-wpseo-option-titles.php`):

| Key | Default |
|---|---|
| `title-home-wpseo` | `%%sitename%% %%page%% %%sep%% %%sitedesc%%` |
| `title-{post_type}` (post, page, CPT) | `%%title%% %%page%% %%sep%% %%sitename%%` |
| `title-ptarchive-{pt}` | `%%pt_plural%% Archive %%page%% %%sep%% %%sitename%%` |
| `title-tax-{tax}` (category, tag…) | `%%term_title%% Archives %%page%% %%sep%% %%sitename%%` |
| `title-author-wpseo` | `%%name%%, Author at %%sitename%% %%page%% ` |
| `title-archive-wpseo` (date) | `%%date%% %%page%% %%sep%% %%sitename%%` |
| `title-search-wpseo` | `You searched for %%searchphrase%% %%page%% %%sep%% %%sitename%%` |
| `title-404-wpseo` | `Page not found %%sep%% %%sitename%%` |
| `metadesc-*` | `''` (empty) |
| social | `social-title-{pt}` = `%%title%%`, `social-title-author-wpseo` = `%%name%%`, `social-title-archive-wpseo` = `%%date%%`, `open_graph_frontpage_title` = `%%sitename%%` |

### 3.2 Rank Math (`%var%`)
- Site and search: `%sep% %search_query% %count(varname)% %sitename% %sitedesc%`
- Current date and time: `%currentdate% %currentday% %currentmonth% %currentyear% %currenttime% %currenttime(F jS, Y)%`
- Organization: `%org_name% %org_logo% %org_url%`
- Post: `%title% %parent_title% %excerpt% %excerpt_only% %url% %post_thumbnail% %id% %date% %date(fmt)% %modified% %modified(fmt)% %seo_title% %seo_description%`
- Categories, tags and terms: `%category% %categories% %categories(limit=3&separator= | &exclude=12,23)% %tag% %tags% %tags(...)% %term% %term_description% %customterm(tax)% %customterm_desc(tax)%`
- Author: `%userid% %name% %user_description%`
- Keywords and custom fields: `%focuskw% %keywords% %customfield(name)%`
- Pagination and post type: `%page% %pagenumber% %pagetotal% %pt_single% %pt_plural%`
- Attachments: `%filename% %imagealt%(Pro) %imagetitle%(Pro)`
- Pro: `%randomword(a|b)% %randomword_np(a|b)%`
- BuddyPress: `%group_name% %group_desc%`
- WooCommerce: `%wc_price% %wc_sku% %wc_shortdesc% %wc_brand%`

**Rank Math default title templates** (`includes/class-installer.php`): separator `-`.

| Context | Title | Description |
|---|---|---|
| Home | `%sitename% %page% %sep% %sitedesc%` | `''` |
| Post type | `%title% %sep% %sitename%` (no `%page%`) | `%excerpt%` |
| CPT archive | `%title% %page% %sep% %sitename%` | |
| Taxonomy | `%term% %sep% %sitename%` | `%term_description%` |
| Author | `%name% %sep% %sitename% %page%` | |
| Date | `%date% %page% %sep% %sitename%` | |
| Search | `%search_query% %page% %sep% %sitename%` | |
| 404 | `Page Not Found %sep% %sitename%` | |

Other Rank Math defaults:
- Author archives get `noindex`. Date archives are **disabled** and noindexed. Search pages are noindexed. Empty taxonomies are noindexed.
- Twitter card is `summary_large_image`.
- Knowledge graph type is `company` if WooCommerce/EDD is active, otherwise `person`.
- Image alt format is ` %filename%` and image title format is `%title% %count(title)%`.

Sources:
- https://yoast.com/help/list-available-snippet-variables-yoast-seo/
- https://github.com/Yoast/wordpress-seo/blob/trunk/inc/class-wpseo-replace-vars.php
- https://github.com/Yoast/wordpress-seo/blob/trunk/inc/options/class-wpseo-option-titles.php
- https://rankmath.com/kb/variables-in-seo-title-description/
- https://github.com/rankmath/seo-by-rank-math/blob/master/includes/class-installer.php

---

## 4. Yoast schema graph

### 4.1 @id conventions (`src/config/schema-ids.php`, generators)

| Node | @id |
|---|---|
| WebSite | `{site_url}#website`, where site_url = home URL with a trailing slash |
| Organization | `{site_url}#organization` |
| Organization logo | `{site_url}#/schema/logo/image/` |
| Person (site or author) | `{site_url}#/schema/person/{wp_hash(user_login . user_id)}` |
| Person image | `{site_url}#/schema/person/image/` (+hash) |
| WebPage | `{permalink}` (the main schema id is the plain permalink, no fragment) |
| Article | `{canonical}#article` |
| Primary image (ImageObject) | `{canonical}#primaryimage` |
| BreadcrumbList | `{canonical}#breadcrumb` |
| Legacy constants | `#author`, `#authorlogo` |

- `wp_hash()` is salted per site, so person ids are site-specific and not reproducible. Pursae SEO needs its own stable salted hash.
- developer.yoast.com proposes a newer `{site}/#/schema/{type}/{id}` pattern, but the shipping code still uses the constants above.

### 4.2 Graph shape
- Pieces are evaluated in this order, each with an `is_needed()` check: **Article, WebPage, Main_Image, Breadcrumb, Website, Organization, Person, Author, FAQ, HowTo**.
- Password-protected posts only get WebPage, Website and Organization.

**Single post:**
- `Article`: `@type` from `_yoast_wpseo_schema_article_type` or default. `isPartOf` → WebPage, `author` → Person @id, `publisher` → Org/Person, `mainEntityOfPage` → WebPage, `image` → #primaryimage. Also headline, datePublished, dateModified, wordCount, commentCount, keywords, articleSection, inLanguage, potentialAction CommentAction.
- `WebPage`: `@type` `["WebPage", <page type>]`. url, name, `isPartOf` #website, primaryImageOfPage, image, thumbnailUrl, datePublished, dateModified, `breadcrumb` → #breadcrumb, inLanguage, potentialAction ReadAction.
- `ImageObject#primaryimage`, `BreadcrumbList#breadcrumb`.
- `WebSite`: name, description, publisher, potentialAction SearchAction `?s={search_term_string}`, inLanguage.
- `Organization` (logo, sameAs) **or** `Person` (site representation).
- Author `Person`.

**Home page:** WebPage `@type` is
- `CollectionPage` for the home page, term, date and post-type archives
- `WebPage` (+ `CollectionPage` when it is the posts page) for a static front page
- `ProfilePage` for author pages
- `["CollectionPage","SearchResultsPage"]` for search

The home page graph is CollectionPage/WebPage + BreadcrumbList + WebSite + Organization/Person (no Article).

**Allowed types** (`src/config/schema-types.php`):
- PAGE_TYPES: WebPage, ItemPage, AboutPage, FAQPage, QAPage, ProfilePage, ContactPage, MedicalWebPage, CollectionPage, CheckoutPage, RealEstateListing, SearchResultsPage
- ARTICLE_TYPES: Article, BlogPosting, SocialMediaPosting, NewsArticle, AdvertiserContentArticle, SatiricalArticle, ScholarlyArticle, TechArticle, Report, None

Sources:
- https://github.com/Yoast/wordpress-seo/blob/trunk/src/config/schema-ids.php
- https://github.com/Yoast/wordpress-seo/blob/trunk/src/generators/schema-generator.php
- https://github.com/Yoast/wordpress-seo/blob/trunk/src/generators/schema/article.php
- https://github.com/Yoast/wordpress-seo/blob/trunk/src/helpers/schema/id-helper.php
- https://github.com/Yoast/wordpress-seo/blob/trunk/src/context/meta-tags-context.php
- https://developer.yoast.com/features/schema/technology-approach/
- Exact property lists for Article/WebPage/WebSite are partly **unverified** (from memory of the plugin's output).

---

## 5. Post meta keys

### 5.1 Yoast (prefix `_yoast_wpseo_`, `inc/class-wpseo-meta.php`)

| Key | Values |
|---|---|
| `_yoast_wpseo_focuskw` | string |
| `_yoast_wpseo_title`, `_yoast_wpseo_metadesc` | string (may contain `%%vars%%`) |
| `_yoast_wpseo_linkdex` / `_content_score` / `_inclusive_language_score` | int 0–100 |
| `_yoast_wpseo_is_cornerstone` | `'1'` when set (default `'false'`) |
| `_yoast_wpseo_meta-robots-noindex` | **`0` = post-type default, `1` = noindex, `2` = index** |
| `_yoast_wpseo_meta-robots-nofollow` | `0` = follow, `1` = nofollow |
| `_yoast_wpseo_meta-robots-adv` | comma-separated subset of `noimageindex,noarchive,nosnippet` |
| `_yoast_wpseo_bctitle` | breadcrumb title |
| `_yoast_wpseo_canonical` | URL |
| `_yoast_wpseo_redirect` | URL (legacy 301) |
| `_yoast_wpseo_opengraph-title` / `-description` / `-image` / `-image-id` | Facebook/OG |
| `_yoast_wpseo_twitter-title` / `-description` / `-image` / `-image-id` | X/Twitter |
| `_yoast_wpseo_schema_page_type`, `_yoast_wpseo_schema_article_type` | one of the PAGE_TYPES / ARTICLE_TYPES |
| `_yoast_wpseo_primary_{taxonomy}` | term id, e.g. `_yoast_wpseo_primary_category` |
| `_yoast_wpseo_focuskeywords` (Premium) | JSON `[{"keyword":"…","score":"good"}]` (related keyphrases) |
| `_yoast_wpseo_keywordsynonyms` (Premium) | JSON array of strings, index 0 = synonyms for the main keyphrase (comma-separated) |
| `_yoast_wpseo_seo_title_score`, `_meta_description_score` | internal |

- Term meta is **not** postmeta. It lives in the `wpseo_taxonomy_meta` option with keys `wpseo_title`, `wpseo_desc`, `wpseo_focuskw`, `wpseo_focuskeywords`, `wpseo_keywordsynonyms`, …
- Since Yoast 14, everything is also mirrored into the `wp_yoast_indexable` table.

### 5.2 Rank Math (no underscore prefix)

| Key | Values |
|---|---|
| `rank_math_title`, `rank_math_description` | string with `%vars%` |
| `rank_math_focus_keyword` | **comma-separated**, first = primary (Yoast import appends related keyphrases as `", kw2, kw3"`) |
| `rank_math_canonical_url` | URL |
| `rank_math_robots` | serialized PHP array, e.g. `['index','nofollow','noarchive']`. Empty array = use defaults |
| `rank_math_advanced_robots` | serialized assoc array `max-snippet`, `max-video-preview`, `max-image-preview` (defaults from `Helper::get_advanced_robots_defaults()`; values `-1`, `-1`, `large`, unverified) |
| `rank_math_facebook_title` / `_description` / `_image` / `_image_id` / `_enable_image_overlay` / `_image_overlay` | OG |
| `rank_math_twitter_use_facebook`, `rank_math_twitter_card_type`, `rank_math_twitter_title` / `_description` / `_image` / `_image_id` / `_enable_image_overlay` / `_image_overlay` | Twitter |
| `rank_math_pillar_content` | `'on'` |
| `rank_math_breadcrumb_title` | string |
| `rank_math_primary_{taxonomy}` | term id (`rank_math_primary_category`) |
| `rank_math_schema_{Type}` | one meta row per schema entity (e.g. `rank_math_schema_Article`), serialized array |
| `rank_math_rich_snippet` | legacy (pre-1.0.48) schema type key; its presence disables the default schema |
| `rank_math_seo_score` | int 0–100 |
| `rank_math_news_sitemap_exclude`, `rank_math_news_sitemap_robots` (Pro), `rank_math_lock_modified_date`, `rank_math_permalink` | misc |

Yoast → Rank Math mapping (Rank Math importer): title, metadesc, focuskw, canonical, OG/twitter title/description, bctitle map 1:1. `is_cornerstone` → `pillar_content='on'`. Robots: noindex `0` → post-type default, `1` → `noindex`, `2` → `index`, nofollow `1` → `nofollow`, adv list appended.

Sources:
- https://github.com/Yoast/wordpress-seo/blob/trunk/inc/class-wpseo-meta.php
- https://github.com/Yoast/wordpress-seo/blob/trunk/inc/options/class-wpseo-taxonomy-meta.php
- https://github.com/rankmath/seo-by-rank-math/blob/master/includes/admin/importers/class-yoast.php
- https://github.com/rankmath/seo-by-rank-math/blob/master/wpml-config.xml
- https://github.com/rankmath/seo-by-rank-math/blob/master/includes/admin/metabox/class-screen.php

---

## 6. Headless "head" REST endpoints

| | Yoast | Rank Math |
|---|---|---|
| Route | `GET /wp-json/yoast/v1/get_head?url=<absolute url>` | `GET /wp-json/rankmath/v1/getHead?url=<absolute url>` |
| Enabled | always (when the REST API is on) | only if **General Settings → Others → Headless CMS Support** (`general.headless_support`) is on, otherwise "no route" |
| Auth | public | public (`permission_callback __return_true`) |
| Response | `{ "html": "<escaped head blob>", "json": { …yoast_head_json… }, "status": 200 }` (`status` = 200/404 of the target URL) | `{ "success": true, "head": "<meta tags html>" }` |
| Also | `yoast_head` (HTML string) and `yoast_head_json` (object) are added to every core `/wp/v2/*` response | — |

`yoast_head_json` keys: `title`, `description`, `robots{index,follow,max-snippet,max-image-preview,max-video-preview}`, `canonical`, `og_locale`, `og_type`, `og_title`, `og_description`, `og_url`, `og_site_name`, `article_publisher`, `article_author`, `article_published_time`, `article_modified_time`, `og_image[{width,height,url,type}]`, `author`, `twitter_card`, `twitter_creator`, `twitter_site`, `twitter_misc`, `schema`.

Sources:
- https://developer.yoast.com/customization/apis/rest-api/
- https://rankmath.com/kb/headless-cms-support/
- https://github.com/rankmath/seo-by-rank-math/blob/master/includes/rest/class-headless.php

---

## 7. XML sitemaps

| | Yoast | Rank Math |
|---|---|---|
| Index | `/sitemap_index.xml` | `/sitemap_index.xml` (slug filterable via `rank_math/sitemap/index/slug`) |
| Children | `/{type}-sitemap{N}.xml`, rewrite `([^/]+?)-sitemap([0-9]+)?\.xml$` → post-, page-, category-, post_tag-, author-, {cpt}- | same rewrite regex. Pro adds news/video sitemaps (`news-sitemap.xml`, `video-sitemap.xml`, unverified names) and `local-sitemap.xml` |
| XSL | `([a-z]+)?-?sitemap\.xsl$` → `/main-sitemap.xsl` | same pattern (`?xsl=`) |
| Entries per sitemap | **1000** (`wpseo_sitemap_entries_per_page`) | **200** (`items_per_page`) |
| Namespaces | `sitemap/0.9` + `xmlns:image` (google sitemap-image/1.1). `<url><loc><lastmod><image:image><image:loc>` only, **no changefreq/priority** | include_images on, featured image off, HTML sitemap on (shortcode) |
| `/sitemap.xml` | 301 → `/sitemap_index.xml` (only when it would 404) | — |
| Core `wp-sitemap.xml` | `wp_sitemaps_enabled` → false when the Yoast sitemap is on. 301 redirects: `/wp-sitemap.xml` → `/sitemap_index.xml`; `/wp-sitemap-(posts\|taxonomies)-{name}-{n}.xml` → `/{name}-sitemap{n-1 or ''}.xml`; `/wp-sitemap-users-{n}.xml` → `/author-sitemap{…}.xml` | disabling core sitemaps not found in source search (**unverified**) |
| Paging redirects | — | `maybe_redirect`: `x-sitemap.xml` → `x-sitemap1.xml` when more pages exist, and back |
| Defaults included | — | all public post types on; taxonomies: only `category` on; authors on (excluded roles configurable) |

- Quirk in Yoast's core-sitemap redirect: `wp-sitemap-posts-post-2.xml` maps to `post-sitemap1.xml`, whose page numbering doesn't line up with Yoast's own `post-sitemap2.xml`.

Sources:
- https://github.com/Yoast/wordpress-seo/blob/trunk/inc/sitemaps/class-sitemaps-router.php
- https://github.com/Yoast/wordpress-seo/blob/trunk/inc/sitemaps/class-sitemaps.php
- https://github.com/Yoast/wordpress-seo/blob/trunk/inc/sitemaps/class-sitemaps-renderer.php
- https://github.com/Yoast/wordpress-seo/blob/trunk/src/initializers/disable-core-sitemaps.php
- https://yoast.com/help/xml-sitemaps-in-the-wordpress-seo-plugin/
- https://github.com/rankmath/seo-by-rank-math/blob/master/includes/modules/sitemap/class-router.php
- https://github.com/rankmath/seo-by-rank-math/blob/master/includes/modules/sitemap/class-sitemap.php
- https://github.com/rankmath/seo-by-rank-math/blob/master/includes/class-installer.php

---

## 8. Redirections and 404 monitor

### 8.1 Rank Math Redirections
- **Types** (`Helper::choices_redirection_types`): `301` Permanent Move, `302` Temporary Move, `307` Temporary Redirect, `410` Content Deleted, `451` Content Unavailable for Legal Reasons. Default header code is `301`.
- **Source comparison** (`choices_comparison_types`): `exact`, `contains`, `start` (Starts With), `end` (End With), `regex`. Each source also has an ignore-case flag.
- **Multiple sources** per redirection.
- **Table `rank_math_redirections`:** `id, sources (longtext, serialized [{pattern, comparison, ignore}]), url_to, header_code, hits, status ('active'|'inactive'|'trashed'), created, updated, last_accessed`.
- **Cache table `rank_math_redirections_cache`:** `from_url, redirection_id, object_id, object_type ('post'|'term'|'user'), is_redirected`.
- **Auto redirect** on post/term slug change when `general.redirections_post_redirect` is on: hooks `pre_post_update` and `post_updated`, plus term equivalents. It creates a 301 from the old permalink and skips the post if a redirection already exists for it.
- **Other settings:**
  - `redirections_debug` (default off)
  - fallback behaviour for 404s: default 404 / homepage / custom URL
  - cleanup of trashed redirects runs daily via cron
  - Categories (Pro)
  - Import/export (CSV, `.htaccess`, nginx), unverified

### 8.2 Rank Math 404 Monitor
- **Simple mode** (default `404_monitor_mode = simple`): one row per URI with hit count. Fields: URI, Hits, Access Time.
- **Advanced mode:** one row per hit, adding **Referer** and **User-Agent** (no IP).
- **Settings:**
  - log limit `404_monitor_limit = 100` (0 = unlimited; the log is cleared when exceeded)
  - exclude paths with Exact/Contains/Starts With/End With/Regex
  - `404_monitor_ignore_query_parameters = on` (strips the query string)
- **Actions:** redirect, delete, bulk delete, clear log, CSV export (Pro).
- **Table `rank_math_404_logs`:** `id, uri, accessed, times_accessed, referer, user_agent`.

### 8.3 Yoast Premium Redirect Manager
- **Types:** 301 Moved Permanently, 302 Found, 307 Temporary Redirect, 410 Content Deleted, 451 Unavailable for Legal Reasons.
- **Format:** `plain` or `regex`, in separate tabs.
- **CSV import columns:** origin, target, type, format. The target must be empty for 410/451.
- **Prompts** offer a redirect when a slug changes or a post or term is deleted/trashed.
- **Redirect method:** PHP, or web-server files (Apache `.htaccess` / nginx include). Unverified detail.
- No bulk type change.

Sources:
- https://github.com/rankmath/seo-by-rank-math/blob/master/includes/helpers/class-choices.php
- https://github.com/rankmath/seo-by-rank-math/blob/master/includes/modules/redirections/class-watcher.php
- https://github.com/rankmath/seo-by-rank-math/blob/master/includes/class-installer.php
- https://rankmath.com/kb/monitor-404-errors/
- https://yoast.com/features/redirect-manager/
- https://yoast.com/help/import-redirects/
- https://yoast.com/which-redirect/
- Note: rankmath.com/kb/redirections/ returned 404, so the Rank Math redirection facts above come from source.

---

## 9. Setup wizards, dashboards, roles

### 9.1 Yoast first-time configuration
Steps:
1. **SEO data optimization:** runs the indexables indexing ("Start SEO data optimization").
2. **Site representation:** Organization vs Person, name, logo or user, alternate name, tagline.
3. **Social profiles:** Organization sameAs URLs, or a pointer to the user profile for Person.
4. **Personal preferences:** usage tracking and newsletter.
5. **Finish configuration.**

Yoast dashboard (General page) contents, **unverified**:
- **Dashboard tab:** problems / notifications list, SEO and readability score overview per post type (also as a WP dashboard widget), optional Site Kit (Search Console / Analytics) widgets.
- **First-time configuration tab.**
- **Alerts.**

### 9.2 Rank Math setup wizard
Steps:
1. Connect account (optional)
2. Mode select (Easy / Advanced / Custom-Pro) + compatibility check (conflicting SEO plugins)
3. **Import** (Yoast, AIOSEO, SEOPress, Redirection, schema plugins)
4. **Your Site:** site type personal/business, business type, name, alternate name, logo, default social image
5. **Analytics:** Search Console, GA, AdSense-Pro, email reports
6. **Sitemap:** on/off, images, post types, taxonomies
7. **Optimization:** noindex empty taxonomies, nofollow external links, external links in new tab
8. **Ready.** Advanced mode then adds Role Manager → 404 + Redirections → Schema Markup defaults.

Rank Math dashboard: Modules grid (toggle modules), Help, Setup Wizard, Import & Export, Status & Tools. This list is **unverified** (from product UI).

### 9.3 Rank Math Role Manager capabilities (`class-capability-manager.php`)
`rank_math_titles` (Titles & Meta), `rank_math_general`, `rank_math_sitemap`, `rank_math_404_monitor`, `rank_math_link_builder`, `rank_math_redirections`, `rank_math_role_manager`, `rank_math_analytics`, `rank_math_site_analysis`, `rank_math_onpage_analysis`, `rank_math_onpage_general`, `rank_math_onpage_advanced`, `rank_math_onpage_snippet` (On-Page Schema), `rank_math_onpage_social`, `rank_math_content_ai`, `rank_math_admin_bar`.

Defaults (from source):
- Administrators get everything.
- Editors and Authors get a subset: site/onpage analysis, onpage general/snippet/social for editors, and onpage analysis/general/snippet/social for authors.
- The KB says the Administrator Role Manager setting is locked.

Sources:
- https://yoast.com/help/yoast-seo-first-time-configuration/
- https://academy.yoast.com/app/uploads/sites/4/2023/08/first-time-configuration-yoast-seo-for-wordpress-course.pdf
- https://rankmath.com/kb/how-to-setup/
- https://rankmath.com/kb/role-manager/
- https://github.com/rankmath/seo-by-rank-math/blob/master/includes/modules/role-manager/class-capability-manager.php

---

## 10. Rank Math IndexNow (Instant Indexing module)

| Item | Value |
|---|---|
| API key | `wp_generate_uuid4()` with dashes stripped (32 hex chars). Can be regenerated ("Change Key") |
| Key location | `{home_url}/{key}.txt`, served dynamically (filter `rank_math/instant_indexing/indexnow_key_location`). "Check Key" button. A physical file is a fallback |
| Endpoint | `POST https://api.indexnow.org/indexnow/` with JSON `{host, key, keyLocation, urlList:[…]}` and header `X-Source-Info: https://rankmath.com/{version}/{1 if manual}` |
| Auto-submit | `save_post_{pt}` for the selected post types (`instant_indexing.bing_post_types`). Fires on transition to **publish** (new or updated) and on **trash of a previously published** post (old permalink). Skips revisions/autosaves, `lock_modified_date`, noindex posts, hidden Woo products, CSV imports, and posts already submitted in this request |
| Manual | Submit URLs page (one per line), row action "Instant Indexing: Submit Page", bulk action "Submit Pages" |
| History | log of URL + response code |

Response codes:
- 200: ok
- 202: accepted, key pending
- 400: bad request
- 403: key invalid
- 422: URL not on this host
- 429: too many requests

Sources:
- https://rankmath.com/kb/instant-indexing/
- https://github.com/rankmath/seo-by-rank-math/blob/master/includes/modules/instant-indexing/class-api.php
- https://github.com/rankmath/seo-by-rank-math/blob/master/includes/modules/instant-indexing/class-instant-indexing.php
