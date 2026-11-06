# Pursae Maintenance: feature inventory

Pursae Maintenance is a native (compiled-in) Pursae plugin that clones **Maintenance** by WebFactory
(wordpress.org/plugins/maintenance, v4.32, 2026-07-13). It also includes the parts of **Maintenance PRO**
that make sense for a single-site Go install. Like Classic Editor, Pursae Cache and Pursae Web Builder, it is
**off on a fresh install**, and while it is off nothing of it runs or shows anywhere.

Sources: the plugin's wordpress.org page, readme and changelog, and the v4.32 source (`maintenance.php`
with class `MTNC`, and `frontend/index.php`); the PRO feature list on wpmaintenancemode.com. SeedProd
Coming Soon was consulted only where Maintenance lacks a basic piece: a separate Coming Soon / 200
mode, and a countdown that auto-disables.

Legend: **✓** implemented · **≈** implemented differently (reason given) · **✗** left out (reason given).

## 1. How the original works (summary of the research)

- **Storage.** All options live in one option, `mtnc-options`. The admin page is a top-level
  **Maintenance** menu (`manage_options`) with these tabs: Themes · Design · SEO · Access · Advanced · PRO.
- **The gate.** It is a `template_include` filter. It never touches cron, ajax, WP-CLI or `is_admin()`
  requests, nor URLs containing `/wp-login.php`, `/wp-admin/`, `/async-upload.php`, `/upgrade.php` or
  `/xmlrpc.php`.
  - REST, feeds, robots.txt and sitemaps exit before `template_include`, so the free plugin never
    gates them. PRO has an "Enable WordPress Rest API" switch.
- **Who sees the site, checked in this order:**
  1. Search engine crawlers (PRO `excludese`, a list of 24 user agents).
  2. Any logged-in user (`show_logged_in`, on by default; there is no role filter).
  3. An exact `REMOTE_ADDR` match in the IP whitelist (PRO).
  4. URL rules (`whitelist` / `blacklist`). The path is lowercased, `slashit()`-normalized, and must
     match exactly.
  5. The secret link `home/?<secret>`, which sets a 24-hour cookie (PRO).
  6. The direct-access password, also stored in a cookie (PRO).
- **Status code.** 503 with `Retry-After: 3600` when `blockse` is on. In 4.32 that toggle is not in
  the save whitelist, so the free plugin really serves **200**. `nocache_headers()` is always sent.
- **`?maintenance-preview`** shows the page to anyone, even while the mode is off.
- **Admin bar.** A "Maintenance" node with a status dot, an ON/OFF toggle, Preview and Settings.
- **Default page.** Full-screen background image with an overlay, a centered headline, text and footer.
  A padlock button in the lower right slides in a login form. There is no `wp_head()`.
- **PRO** adds: 200+ themes and a drag-and-drop builder with these modules: countdown, form (e-mail
  collection), social icons, progress bar, map, video and custom HTML. It also adds SEO fields
  (meta description, favicon, og image, tracking code), IP whitelist, secret link, site password,
  no-cache headers, toolbar on/off, force SSL, REST toggle, customizing the core update page and
  white-label.

## 2. Inventory: original vs Pursae Maintenance

### Switches and status

| Feature | Original | Pursae Maintenance |
|---|---|---|
| Plugin on/off | Plugins screen | ✓ Plugins screen, native row "Pursae Maintenance" (also on Add Plugins › Built-in). Off by default. |
| Maintenance mode on/off | Header switch `status` | ✓ General › Status. The state badge appears on every tab and a red "On" bubble on the menu. |
| Admin bar indicator | Status dot plus ON/OFF toggle, Preview, Settings (admins only) | ✓ The same, in wp-admin and on the front end. Nonce-checked **and** capability-checked (the original checks only the nonce). |
| Hide the toolbar menu | PRO `disable_toolbar_menu` | ✓ Advanced › Toolbar Menu |
| Deactivating opens the site | yes | ✓ The settings are kept for the next activation |

### Response and search engines

| Feature | Original | Pursae Maintenance |
|---|---|---|
| HTTP 503 + `Retry-After` | Toggle (broken in 4.32), 3600 s | ✓ "Maintenance" mode (default). `Retry-After` is the configured hours (SEO tab, default 1), or the countdown's remaining seconds while one runs. |
| 200 "Coming Soon" mode | — (SeedProd) | ✓ "Coming Soon" mode |
| No-cache headers | PRO, default on | ✓ Always sent: `Cache-Control: no-store…`, `Expires`, `X-LiteSpeed-Cache-Control: no-cache` |
| noindex | none | ✓ `<meta name="robots" content="noindex">` plus an `X-Robots-Tag` header (SEO tab, default on) |
| Show the normal site to crawlers | PRO `excludese` | ✓ The original's UA list plus DuckDuckBot, YandexBot, Baiduspider and Applebot |
| SEO title with `%sitetitle%` / `%sitetagline%` | ✓ | ✓ |
| Meta description, favicon, og:image | PRO | ✓ The images are picked from the Media Library modal |
| Target keyword / SEO analyzer | PRO | ✗ Editorial tooling, not part of a maintenance page |
| Google Analytics | Tracking ID (old analytics.js) | ≈ GA4 measurement ID (`G-…`) with gtag.js. The page's CSP allows googletagmanager and google-analytics only while an ID is set. |
| Tracking pixel / third-party code | PRO | ≈ The Custom HTML module. External scripts stay blocked by the page's CSP. |

### Access (who sees the normal site)

| Feature | Original | Pursae Maintenance |
|---|---|---|
| wp-admin and wp-login always reachable | ✓ | ✓ `/wp-admin*`, `/wp-login*` (including `.php`), `/wp-content/`, `/wp-includes/`, `/wp-cron`, `/xmlrpc`, `robots.txt` and `favicon.ico` are never gated |
| Logged-in users | `show_logged_in`, all roles | ✓ Same default (every role), plus a role filter (PRO's role denial). Administrators always pass. |
| IP whitelist | PRO, exact match | ✓ IPs **and CIDR ranges**, `#` comments. Uses the connection's address, like the rest of Pursae (no `X-Forwarded-For` trust). |
| URL Based Rules | whitelist / blacklist, exact path | ✓ Same matching (lowercased, `slashit()`, full URLs reduced to their path), plus a trailing `*` for prefixes |
| Secret access link `home/?<key>` | PRO, 24 h cookie | ✓ The lifetime is configurable. The cookie is an HMAC bound to the key, so changing the key revokes old links. |
| Direct-access password | PRO | ✓ A password button and popup with the original's texts; the cookie is an HMAC |
| Login form on the page | ✓ Slide-in panel, posts to `wp_signon` | ✓ A padlock button opens a panel that posts to `/wp-login` with `redirect_to` set to the current URL. The tooltip is configurable. |
| Custom login URL | PRO | ✗ Pursae has no login-URL hiding |
| Password-protect the maintenance page itself | PRO | ✗ Rarely useful; the site password covers the need |
| REST API | Free: never gated; PRO: toggle | ✓ Anonymous REST requests get a JSON 503 (Advanced, default on). Logged-in users keep REST, which the editor needs. |
| Feeds and sitemaps | never gated | ✓ Gated by default (Advanced › Feeds & Sitemaps) so content doesn't leak while the site is closed |
| `?maintenance-preview` | Anyone, even while off | ✓ Same. `&theme=` previews a theme without activating it. |

### Page content and design

| Feature | Original | Pursae Maintenance |
|---|---|---|
| Headline, text (HTML), footer | Header, Content and Footer modules | ✓ The original's default texts; `%sitetitle%`, `%sitetagline%` and `%year%` are replaced |
| Logo with width | Logo module | ✓ Picked from the Media Library modal (`enqueueMedia`) |
| Themes | 1 free, 200+ PRO | ≈ **7 built-in themes** (Default, Minimal, Card, Split, Neon, Sunrise, Editorial), each with Activate and Preview. As in the original, activating one copies its design values into Design. |
| Background: image, color | ✓ (also YouTube video) | ✓ Image (from Media, with size and position), solid color, or a two-color gradient. ✗ No video, because YouTube needs third-party frames, which the strict CSP forbids. |
| Background blur | ✓ | ✓ 0–40 px |
| Overlay | `content_overlay_color` | ✓ A dark overlay (0–90 %), plus an optional frosted "content box" |
| Content position and width | left / right / center / middle / bottom; width | ✓ middle / left / right / top / bottom; width |
| Font family and size, colors | Google fonts through Bunny Fonts | ≈ 7 system font stacks. The page loads nothing from other servers, which is also the GDPR-friendly choice. Headline, text, link and accent colors. |
| Custom CSS | ✓ | ✓ Cannot close the `<style>` element |
| Preloader | ✓ | ✗ The page is a small self-contained document, so a preloader would only slow it down |
| Multilingual (Weglot upsell) | upsell | ✗ |

### Modules (PRO in the original)

| Feature | Pursae Maintenance |
|---|---|
| Countdown to a date | ✓ Uses the site time zone. Days, hours, minutes and seconds tick in the browser; the label is configurable. |
| Auto-disable when the countdown ends | ✓ (SeedProd-style). The first request after the end switches the mode off and purges the cache; open pages reload. Without auto-disable, a warning notice explains that the mode stays on. |
| Progress bar | ✓ Percent and label |
| Subscribe form | ✓ E-mails go to the plugin's own `maintenance_subscribers` table, created on activation (no core migration). Nonce, honeypot, address validation, de-duplication. Optional e-mail to the admin for each new subscriber, through the core mail queue. |
| GDPR / privacy note | ✓ Optional required-consent checkbox with your text and a link to the published Privacy Policy page. Only the e-mail, the time and the consent are stored, with no IP or user agent. |
| Subscribers list | ✓ Subscribers tab: list, bulk delete, CSV export (protected against formula injection) |
| Mailing-list integrations (Mailchimp, Zapier…) | ✗ Needs third-party accounts and API keys; the CSV export covers the handover |
| Social icons | ✓ Facebook, X, Instagram, LinkedIn, YouTube, Pinterest, WhatsApp, Reddit, Twitch, Spotify and e-mail. They use dashicons served from `/wp-includes`; `javascript:` and other non-http(s) URLs are dropped. |
| Custom HTML | ✓ |
| Map, video, 2- and 3-column content, divider | ✗ Map and video need third-party frames; columns and divider are page-builder layout. Pursae Web Builder exists for that. |
| Drag-and-drop module ordering | ✗ The modules have a fixed order (logo, headline, text, countdown, progress, subscribe, social, custom HTML) |
| reCAPTCHA | ✗ Third party; a honeypot and a nonce are used instead |

### Advanced

| Feature | Original | Pursae Maintenance |
|---|---|---|
| Export / Import settings | ✓ Free | ✓ A JSON file. The site password is never exported. Importing keeps the current on/off state and password, and every value is re-sanitized. |
| Reset settings | ✓ | ✓ Keeps the on/off state |
| Force SSL on the page | PRO | ✗ TLS is the job of the server or proxy in front of Pursae |
| Customize the built-in WP update page | PRO | ✗ Pursae has no `.maintenance` update page |
| White-label, license dashboard | PRO | ✗ No licenses in Pursae |

## 3. How it is built

- **Files:**
  - `internal/server/maintenance.go`: settings, gate, bypass rules, headers
  - `maintenance_page.go`: themes, fonts and render
  - `maintenance/page.html`: the embedded public page
  - `maintenance_admin.go`: screen, actions, menu, toolbar, import/export
  - `maintenance_subscribers.go`
  - `web/admin/templates/pages/maintenance.html`
  - Tests: `maintenance_test.go`
- **Settings.** All settings sit in one option, `maintenance_settings`, read over the defaults in code,
  so `settings.Defaults` is untouched. The plugin switch is `native_maintenance`.
- **Where the gate runs.** `Server.Handler` calls `maintenanceServe` **before the page cache**. So:
  - a closed site never serves a cached page to a visitor;
  - the maintenance page is written by the gate with no-store headers and never enters the cache;
  - bypassing users go on to the normal cache and app as usual.
- **Cache purges.** These all call `purgeAll`: saving any tab, the toolbar toggle, activating or
  deactivating the plugin, import, reset, and the countdown's auto-disable.
- **The gate runs before the router**, so it resolves the login cookie itself (`maintUser`). Its
  security headers are the front end's, with Google Analytics added only while an ID is set.
- **Hooks into shared files**, one or two lines each:
  - `server.go` (Handler)
  - `plugins.go` (row, activate/deactivate case, reserved slug, icon)
  - `admin.go` (menu, toolbar node)
  - `site_head.go` (front-end toolbar node)
  - `wpgocache.go` (`admin?page=maintenance` dispatch)
