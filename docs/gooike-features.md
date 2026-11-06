# Pursae Cookie: feature inventory

Pursae Cookie is a native (compiled-in) Pursae plugin for cookie consent and cookie management. Its feature
set is on par with the best WordPress cookie plugins (Complianz, CookieYes, Cookie Notice & Compliance). It
is built to comply by design with the **GDPR / ePrivacy Directive** (EU, EEA, UK), Turkey's **KVKK**,
**CCPA/CPRA** (California) and **Google Consent Mode v2**. Like every native plugin it is **off on a fresh
install** (`native_gooike`). While it is off, nothing of it is printed on the site, and its admin menu,
screens, REST routes and assets answer 404. Deactivating keeps its settings and the consent log.

Legend: **✓** implemented · **≈** implemented differently (reason given) · **✗** left out (reason given).

## Files

| File | What it holds |
| --- | --- |
| `internal/server/gooike.go` | settings model and defaults, on/off, plugin row, menu, routes |
| `internal/server/gooike_themes.go` | the 14 banner themes, fonts, positions, CSS custom properties |
| `internal/server/gooike_services.go` | known services, inert script rewriting, CSP sources, embed detection |
| `internal/server/gooike_front.go` | head / body-open / footer output, Consent Mode script, shortcodes, embed blocking |
| `internal/server/gooike_consent.go` | consent REST endpoint, IP anonymization, geo endpoint, purge, log queries, stats |
| `internal/server/gooike_scan.go` | the cookie scanner |
| `internal/server/gooike_admin.go` | admin screens, form actions, CSV export, live preview |
| `web/gooike/gooike.js`, `web/gooike/gooike.css` | front-end runtime and styles, served at `/wp-content/plugins/gooike/assets/` |
| `web/admin/templates/pages/gooike.html` | the admin screens |
| `internal/db/migrations/0013_gooike.sql` | the `gooike_consent_log` table |
| `internal/server/gooike_test.go` | unit and integration tests |

Settings are one JSON option, `gooike_settings`: texts, design, categories, scripts, the cookie declaration
and settings. It is read from the in-memory settings cache, so rendering a page costs no extra query.

## 1. Banner and preferences

- ✓ **First layer**: a title, a message, links (privacy policy, cookie policy, and the KVKK information
  notice in KVKK mode) and three buttons: **Reject all**, **Accept all**, **Customize**. Reject and Accept
  are the same kind of button, so they are equally prominent (EDPB guidance on deceptive design).
  Customize is a secondary button.
- ✓ **Second layer**: a preferences modal (`role="dialog"`, `aria-modal`). It holds one switch per category
  (`role="switch"`). Necessary is shown as "Always active". Each category has its description and a
  collapsible list of its declared cookies (name, provider, purpose, duration). The modal also shows the
  visitor's **consent ID** and the date of their choice, and has the buttons Reject all, Save my choices
  and Accept all.
- ✓ **Categories**: Necessary (always on), Preferences/Functional, Statistics/Analytics and Marketing,
  plus **admin-defined custom categories** (key, name, description, shown or hidden, Consent Mode types).
  The four built-in categories cannot be deleted. If one goes missing, sanitizing restores it.
- ✓ **Reopening and withdrawing**: a floating cookie button (left or right, on by default) and the
  `[gooike_preferences]` shortcode link. Any `a[href="#gooike-preferences"]` works too, and so does
  `Gooike.open()`. Withdrawing a category deletes its declared cookies (`*` patterns included, on the
  host and its parent domains). The page then reloads (configurable), because scripts that already ran
  cannot be unloaded. The decision is logged as **withdraw**.
- ✓ **Storage**: a first-party cookie, `gooike_consent`. It is URL-encoded JSON:
  `{id, v (policy version), t (timestamp), c (granted categories), a (action)}`, with `SameSite=Lax`,
  `Secure` on https and `Max-Age` = the consent expiry (12 months by default, 1–24 allowed).
- ✓ **Re-prompt**: a stored choice no longer counts when its policy version differs from the current one
  ("Bump version and ask everyone again" in Settings) or when it is older than the expiry.
- ✓ **Accessibility**:
  - focus trap in the modal and in modal or wall banners, Esc closes the preferences, and focus returns
    to where it was;
  - all controls are native buttons, inputs and links, keyboard operable, with visible
    `:focus-visible` rings;
  - a polite live region announces "Your choice has been saved";
  - `prefers-reduced-motion` turns off every animation;
  - touch targets are at least 44px, and the layout is responsive (bottom-sheet modal on phones);
  - banners and the modal are hidden when printing.
- ✓ **Multilingual-ready texts**: all 26 visitor-facing strings are editable under Banner › Texts,
  with "Reset to defaults". There is no hard-coded English in the JS: the config carries the strings it
  needs.

## 2. Themes (14)

Each theme is a layout with a palette, a font and a corner radius. Activating one copies its values into
the design, which can then be customized: position (from the positions the layout offers),
background, text, button and button text colors, font (site font, system, humanist, rounded, serif,
mono), font size, radius, floating button and position, and **custom CSS** (it can never close its
`<style>` element).

| Key | Name | Layout |
| --- | --- | --- |
| `classic` | Classic Bar | full-width bar, bottom |
| `topbar` | Top Bar | slim bar, top |
| `box-left` | Floating Box Left | corner card |
| `box-right` | Floating Box Right | corner card |
| `modal` | Centered Modal | dialog over a backdrop |
| `pill` | Minimal Pill | one-line rounded pill |
| `glass` | Glass | translucent card with backdrop blur |
| `midnight` | Midnight Dark | dark bar |
| `brand` | Brand Color | bar in the brand color with white buttons |
| `illustrated` | Cookie Card | card with a cookie illustration |
| `toast` | Corner Toast | small toast, top right |
| `wall` | Consent Wall | full-screen wall for strict mode |
| `split` | Split Panel | wide two-column panel |
| `sidebar` | Side Panel | docked side panel (bottom sheet on phones) |

- ✓ **Live preview**: the Themes tab shows each theme as a scaled live iframe of
  `/wp-admin/gooike?page=preview&theme=…` (a sample page with the banner open). The Design tab has a
  large preview that reloads as the form changes (unsaved values travel as query parameters), with
  Desktop / Mobile and Banner / Preferences toggles.

## 3. Script manager

- ✓ **Scripts**: name, category, placement (**head**, **body open** right after `<body>` through
  `wp_body_open`, or **footer**), code, and active or disabled.
- ✓ **Inert output**: for every category except Necessary:
  - each `<script>` is printed as `type="text/plain" data-gooike-category="…"`, its `src` moved to
    `data-gooike-src` and a non-JS type kept in `data-gooike-type`;
  - `<iframe>`, `<img>` and `<embed>` lose their `src` and `srcset` the same way;
  - `<noscript>` fallbacks are dropped, because they would track without consent.

  Plain JavaScript without tags is wrapped in a script. **Necessary scripts are printed unchanged and run
  immediately.**
- ✓ **Activation** (`gooike.js`): once a category is granted, the scripts are re-created in document
  order. A blocking external script is waited for before the next one runs, so inline code that depends
  on it works. Iframes and images get their `src` back.
- ✓ **Known services**: you paste only the ID and Pursae Cookie writes the official snippet:
  - Google Analytics 4 (`G-…`)
  - Google Tag Manager (`GTM-…`)
  - Meta Pixel
  - Hotjar
  - Microsoft Clarity
  - LinkedIn Insight Tag
  - TikTok Pixel

  Each service brings its default category and placement and its cookies, which join the declaration.
  It also brings its hosts, which are added to the page's **Content-Security-Policy** (`script-src` and
  `connect-src`, per response through `cspAllow`). The CSP gets the https origins of custom snippets too,
  plus the "Extra script hosts" setting.
- ✓ **Embeds**: iframes of **YouTube** (including youtube-nocookie), **Vimeo** and **Google Maps** are held
  back on rendered pages, in theme templates and Pursae Web Builder pages. Each shows a placeholder: "This
  content is hosted by YouTube…", with a **Load content** button that grants the embed category
  (Marketing by default) and a link to open the content on the provider's site. These are switches on
  the Scripts screen.
- ✓ **Google Consent Mode v2**: an inline script is printed **first in `<head>`**, before the theme's
  own head script and before any tag. It runs:
  - `gtag('consent','default', …)` for `ad_storage`, `ad_user_data`, `ad_personalization`,
    `analytics_storage`, `functionality_storage`, `personalization_storage` and `security_storage`.
    The defaults are denied under opt-in laws and granted under CCPA (ads denied when GPC is sent).
    The necessary types are always granted.
  - `wait_for_update`, `ads_data_redaction` and `url_passthrough`.
  - If a current choice is stored, an immediate `gtag('consent','update', …)`.

  Every decision sends another update. Each category maps to Consent Mode types, and you can change the
  mapping per category. Tag Manager receives a `gooike_consent_update` dataLayer event, and the page
  receives a `gooike:consent` DOM event.

## 4. Cookie declaration and scanner

- ✓ **Declaration**: an admin-managed list (name, provider, category, purpose, duration). It supports add,
  edit, bulk delete and `*` prefix patterns. Pursae's own cookies are pre-declared. It is shown in
  the preferences and rendered by `[gooike_cookie_table]`, or `[gooike_cookie_table category="statistics"]`
  for one category.
- ✓ **Scanner**: it requests the home page and any extra paths you list *through the site itself*, as an
  anonymous visitor and bypassing the page cache. It then:
  - lists every `Set-Cookie`, with the duration read from Max-Age or Expires, and known first-party
    cookies with their purposes;
  - detects known services in the HTML (including in blocked scripts) and in the script manager, and
    adds their JavaScript cookies;
  - detects YouTube, Vimeo and Maps embeds.

  Each result is marked *declared* or *new*. "Add selected to the declaration" adds the new ones.
- ≈ **Crawling with a headless browser** (CookieYes, Complianz cloud scan) is left out. A server-side
  fetch cannot see cookies set by JavaScript, so those come from the known-services database instead.

## 5. Consent log

- ✓ **Endpoint**: `POST /wp-json/gooike/v1/consent`. `/wp-json` is never cached, so logging works with the
  full-page cache. The browser sends it with `sendBeacon` or `fetch keepalive`. It is validated as
  follows:
  - the consent ID is a UUID;
  - the action is `accept_all`, `reject_all`, `custom`, `withdraw` or `gpc`;
  - only known categories are kept;
  - the URL is reduced to a path;
  - a cross-site `Origin` is refused;
  - there is a per-/24 rate limit of 30 per minute.
- ✓ **Stored per decision**: timestamp, consent ID, action, granted categories, policy version, regulation,
  **anonymized IP**, **user-agent hash** (a keyed HMAC), page path and whether GPC was sent. The IP is
  truncated by default (IPv4 /24, IPv6 /48); it can instead be a keyed hash or not stored at all. No raw
  IP or user agent is ever written.
- ✓ **Consent Logs screen**:
  - filters by action and date range, and search by consent ID, IP or page;
  - pagination;
  - a details view with the full history of that consent ID;
  - **CSV export** with the current filters, and cells hardened against spreadsheet formulas;
  - "Delete expired records now".
- ✓ **Retention**: in months (12 by default; 0 keeps records forever). The daily cron event
  `gooike_purge_logs` is scheduled on activation and removed on deactivation.
- ✓ **Proof for the visitor**: the consent ID is shown in the preferences modal and by
  `[gooike_consent_id]`.
- ✓ **Dashboard**: the last 30 days, with totals for accept all, reject all, custom and
  changed/GPC and their percentages, a stacked bar chart per day, a status panel (theme, regulation,
  policy version, expiry, Consent Mode, scripts, cookies, privacy policy link, log retention, page cache)
  and the shortcode reference. If no privacy policy is linked, a warning shows.

## 6. Admin

- ✓ The top-level **Pursae Cookie** menu, after Settings, exists only while the plugin is active and only for
  administrators (`manage_options`). Its screens are Dashboard, Banner (Themes, Design with live preview,
  Texts), Categories & Cookies (Categories, Cookie Declaration, Cookie Scanner), Scripts, Consent Logs
  and Settings. They use WordPress admin markup throughout: `wrap`, `nav-tab-wrapper`, `form-table`,
  `wp-list-table`, `postbox`, notices and `row-actions`. Every form is nonce-checked.
- ✓ **Settings**:
  - regulation mode: **GDPR** (opt-in), **KVKK** (opt-in with an information notice link) or
    **CCPA/CPRA** (opt-out: optional categories run until the visitor opts out, and Reject becomes
    "Do Not Sell or Share My Personal Information", also available as `[gooike_do_not_sell]`);
  - **geo targeting**, off by default;
  - **Global Privacy Control** (on) and **Do Not Track** (off);
  - policy version and bump;
  - expiry;
  - reload after withdrawal;
  - privacy policy (a page, a URL, or the site's Settings › Privacy page), cookie policy URL and KVKK
    notice URL;
  - Consent Mode with `wait_for_update`, ads redaction and URL passthrough;
  - logging, IP storage mode and retention;
  - extra CSP hosts.
- ≈ **Geo targeting** reads the visitor's country from a CDN or proxy header (`CF-IPCountry`,
  CloudFront, Vercel, Fastly, `X-Country-Code`) through the uncached `GET /wp-json/gooike/v1/geo`. The
  page stays the same for everyone, and `gooike.js` asks once per session. The rules are:
  - EU, EEA, UK and CH: GDPR;
  - TR: KVKK;
  - US: CCPA;
  - other countries: configurable (no banner by default).

  Pursae ships no GeoIP database, so without a header everyone gets the main regulation.
- ✓ **Privacy signals**: a visitor sending GPC (or DNT, when respected) is treated as having opted out. No
  banner is shown, and the decision is logged as `gpc`. Under GDPR and KVKK nothing optional runs; under
  CCPA marketing (sale and sharing) stops.

## 7. Native plugin rules and the page cache

- ✓ It is off by default. While it is off: no output, the menu is gone, `/wp-admin/gooike`, the assets
  and `gooike/v1` all answer 404, and the shortcodes are left as typed.
- ✓ Activating and deactivating, and every settings change, purge the page cache.
- ✓ **Page cache**: the banner, the inert scripts, the Consent Mode script and the config are identical
  for every visitor. The consent cookie does not bypass the cache. The CSP allowance is part of the
  cached response headers. Tests check that visitors with and without consent get the same cached HTML
  (`X-WPGo-Cache: hit`).
- ✓ The Pursae Web Builder editor is an admin screen and gets no banner. Logged-in users see the banner like visitors do.

## 8. Tests

`internal/server/gooike_test.go`:

- **Unit tests**:
  - inert rewriting: scripts, module type, iframes, img `srcset`, noscript dropped, plain JS wrapped,
    necessary scripts untouched;
  - IP truncation;
  - embed host matching;
  - CSP sources, with disabled or invalid sources excluded;
  - sanitizing of categories;
  - at least 12 themes.
- **Integration tests** (httptest and Postgres):
  - off by default: no output, 404 routes, no menu;
  - every screen renders;
  - banner markup and equal buttons;
  - Consent Mode defaults are the first script;
  - GA4 is inert in `<head>`, a body-open pixel comes right after `<body>` with its noscript dropped,
    and a necessary script runs live in the footer;
  - the CSP allows the managed hosts;
  - selecting a theme changes the markup and the CSS variables;
  - design changes apply, and custom CSS cannot break out of its `<style>`;
  - the cookie table, preferences link and YouTube blocking work;
  - deactivation removes all output and keeps the settings.
- **Consent log tests**:
  - the purge job is scheduled;
  - a record is stored with the truncated IP `127.0.0.0`, a UA hash and filtered categories;
  - invalid records are rejected;
  - the logs screen shows the record and the filter works;
  - the CSV export works;
  - the dashboard counts the record;
  - hashed IP mode works;
  - the retention purge and the cron handler work, retention 0 keeps everything, and logging can be
    switched off.
- **Page cache test**: an identical cached page with and without the consent cookie, the CSP header kept,
  the endpoint never cached, and a purge on theme change.
- **Scanner and geo test**: Clarity cookies are found and added to the declaration; the geo endpoint
  answers DE→gdpr, TR→kvkk, US→ccpa, JP→none and no header→main regulation.
