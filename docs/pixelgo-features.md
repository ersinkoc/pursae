# Pursae Pixel: feature inventory (0.1)

Pursae Pixel is a native (compiled-in) Pursae plugin for ad and analytics tracking, in the class of
PixelYourSite Pro, Pixel Manager for WooCommerce and GTM4WP. It is written from scratch for Pursae; it
shares no code with those plugins. Like every native plugin it is **off on a fresh install**
(`native_pixelgo`). While it is off, nothing of it is printed on the site, and its admin menu, screens,
REST route, feed and assets answer 404. Deactivating keeps its settings and the delivery log.

Legend: **✓** implemented · **≈** implemented differently (reason given) · **✗** left out (reason given).

## Files

| File | What it holds |
| --- | --- |
| `internal/server/pixelgo.go` | settings model, defaults and sanitizer, platform definitions, on/off, plugin row, menu, routes |
| `internal/server/pixelgo_events.go` | the shared event model, platform event mapping, WooCommerce data (products, cart, order), hashing |
| `internal/server/pixelgo_front.go` | head tags (inert until consent), page configuration and `pixelgo.js` in the footer |
| `internal/server/pixelgo_capi.go` | `pixelgo/v1/event`, the four server-side senders, order hooks, delivery log, purge, test send |
| `internal/server/pixelgo_feed.go` | the product catalog feed |
| `internal/server/pixelgo_admin.go`, `web/admin/templates/pages/pixelgo.html` | the admin screens |
| `web/pixelgo/pixelgo.js` | the front-end runtime, served at `/wp-content/plugins/pixelgo/assets/` |
| `internal/db/migrations/0030_pixelgo.sql` | the `pixelgo_log` table |
| `internal/server/pixelgo_test.go`, `pixelgo_woo_test.go`, `pixelgo_admin_test.go` | unit and integration tests |

Settings are one JSON option, `pixelgo_settings`. The page configuration is a JSON data block
(`#pixelgo-config`), not an inline script, so it needs no CSP exception.

## Platforms

| Platform | Browser tag | Server-side | Multiple IDs |
| --- | --- | --- | --- |
| Meta Pixel (Facebook, Instagram) | ✓ | ✓ Conversions API | ✓ |
| Google Analytics 4 | ✓ | ✓ Measurement Protocol, **Purchase only** (see below) | ✓ |
| Google Ads | ✓ conversions by label, remarketing events | ✗ (no event-id deduplication; enhanced conversions are not in 0.1) | ✓ |
| TikTok | ✓ | ✓ Events API | ✓ |
| Pinterest | ✓ | ✓ Conversions API (needs the ad account id) | ✓ |
| Microsoft Advertising (UET) | ✓ | ✗ | first ID only |

## Events

One event model for every platform: canonical names (the Meta standard names, plus ViewCategory,
RemoveFromCart, ViewCart, Download, OutboundClick, Comment, Scroll, TimeOnPage, VideoPlay, VideoComplete)
and canonical parameters (`value`, `currency`, `items`, `content_name`, `content_category`,
`search_string`, `order_id`, `num_items`, `tax`, `shipping`, `payment_type`, `extra`). The mapping to each
platform's own names lives in `pixelEventMap` and is sent to `pixelgo.js`, so browser and server agree.

- **General:** page view, search, scroll depth, time on page, `tel:` and `mailto:` clicks, outbound links,
  file downloads (extension list), form submissions (Lead), comments, registrations, HTML5 video. ✓
  YouTube and Vimeo embeds are not tracked in 0.1. ✗
- **WooCommerce (Pursae Commerce):** ViewContent, ViewCategory, AddToCart (single product, loop buttons,
  AJAX), RemoveFromCart, ViewCart, InitiateCheckout, AddPaymentInfo, Purchase. ✓
  Product ids can be the product id or SKU, with a prefix and suffix, matching the catalog feed. Purchase
  value can be total, subtotal with tax or net. Purchase fires once per order and browser.
  A variable product is tracked by its parent id; the loop's AddToCart carries ids and quantity but no price
  (the loop markup has none). ≈
- **Custom events:** name, trigger (page visit, click on a CSS selector, scroll percent, time on page, form
  submit), URL filter, fixed value and currency, `key=value` parameters (`{url}`, `{title}`, `{referrer}`),
  platform selection, once per session. ✓
- **Advanced matching:** hashed customer data (e-mail, phone, name, address) on the order-received page and
  in server-side events, only when the setting is on. ✓
- **Attribution:** UTM parameters and click ids (`fbclid`, `gclid`, `ttclid`, `epik`, `msclkid`) are kept in a
  first-party cookie (`pixelgo_attr`, 90 days by default), added to events as parameters and used by the
  server-side senders. The cookie is only set when a platform is allowed. ✓

## Consent

With Pursae Cookie active and **Require consent** on (the default), every tag is printed inert
(`gooikeInert`) in its platform's category (GA4: statistics; the rest: marketing) and Pursae Cookie runs it
when the visitor accepts. `pixelgo.js` queues events until their platform is loaded and consent is given,
and a platform that was refused never receives one. A visitor who accepts later still gets the events of the
page they are on. The server-side route reads the consent cookie again. A completed payment is sent
server-side only when the checkout request recorded consent for that platform (stored with the order). ✓

Without Pursae Cookie the tags load directly; the Dashboard says so.

## Server-side delivery and deduplication

Browser events and their server copies share one event id (`pixelgo.js` forwards each event to
`POST /wp-json/pixelgo/v1/event`; the server does not wait for the browser tag, so it survives ad blockers).
Meta, TikTok and Pinterest deduplicate on that id. A completed payment (`payment_complete`) sends the
Purchase with the id `purchase_<order id>`, the same id the order-received page uses, so off-site payments
are counted even when the customer never returns. A webhook retry sends nothing twice.

GA4's Measurement Protocol cannot deduplicate by event id, so it only gets Purchase (deduplicated by
`transaction_id`). The route accepts only canonical events and the enabled custom events, from the same
origin, rate-limited per address; parameters are whitelisted and bounded. Hosts are fixed, redirects are not
followed, access tokens go in request headers or bodies (Google asks for the secret in the query, so that URL
is never logged), are never shown again after saving, and never reach the page. Every delivery is logged
(platform, event, id, status, HTTP code, a short message) with a retention in days. ✓

## Product catalog feed

`/pixelgo-feed/<token>` serves a Google Merchant / Meta Commerce Manager RSS 2.0 feed (id, title, description,
link, image, availability, price and sale price, condition, brand, MPN, product type, Google category). One item
per product (a variable product at its lowest price); hidden, password-protected and unpublished products are
left out. The token is the only access control and can be regenerated. ✓

## Other

- Exclude roles from tracking (administrators by default) ✓
- Debug mode logs every dispatch to the browser console ✓
- `window.pixelgo.track(name, params, opts)` and the `pixelgo:event` DOM event for custom code ✓
- Settings export (tokens left out) and import (stored tokens kept) ✓
- Test event per server-side platform, using the platform's test code (GA4: the validation endpoint) ✓

## Not in 0.1

Google Ads enhanced conversions, Microsoft Advertising server-side events, YouTube and Vimeo video tracking,
Google Tag Manager container and its data layer, per-variation catalog items, Snapchat, Reddit and Twitter/X
tags, WooCommerce subscriptions and bookings specific events, automatic page-view deduplication for
single-page apps.
