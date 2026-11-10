# Pursae Commerce: overview

Pursae Commerce is a native (compiled-in) Pursae plugin that clones **WooCommerce**: products, cart,
checkout, tax, shipping, coupons, orders, payment gateways, e-mails and both REST APIs, with the premium
extensions built in as modules. Like every native plugin it is listed on **Plugins**, starts **off**
(option `native_goocommerce`), and while it is off its screens answer 404 and its REST routes answer
`rest_no_route`. This page is an overview for theme and API developers; it is not a feature-by-feature
inventory like the other built-in plugin pages.

## Data

- Products are posts of type `product` (and `product_variation`) plus a row in `goo_products`.
- Orders are HPOS-like: `goo_orders`, `goo_order_items`, `goo_order_notes`.
- Other tables: `goo_attributes`, `goo_coupons`, `goo_tax_rates`, `goo_shipping_zones`,
  `goo_shipping_methods`, `goo_sessions`, `goo_download_permissions`, `goo_api_keys`, `goo_webhooks`.
  Migrations: `0015_goocommerce.sql` and the `0016_goo_*` files of the modules.

## Modules

Fourteen modules sit on **Pursae Commerce › Extensions**, all off by default and grouped by category:

| Category | Modules |
|----------|---------|
| Merchandising | Product Bundles, Pre-Orders, Product Add-Ons, Product Compare, Quick View, Dynamic Pricing |
| Store management | Bookings, PDF Invoices & Packing Slips |
| Payments & checkout | Subscriptions, Gift Cards |
| Shipping & delivery | Table Rate Shipping |
| Customer engagement | Memberships, Points and Rewards, Wishlists |

A module that is off changes nothing: every one of its hooks checks that the module is on.

**Product Add-Ons details.** File uploads (`/?goo-ajax=addon_upload`) need the nonce printed on the product page
(`goo-addon-upload`), are limited to 20 stored files per client address per 10 minutes, and the daily job deletes
uploads older than 24 hours that no cart session or order item references. A field can be shown conditionally
(`condition_field` / `condition_value`: another field's title and the option label or text that reveals it; hidden
fields are neither required nor stored). Date pickers take `date_min_days` (earliest = today + N days) and
`date_max`, enforced in the HTML and on the server. The Store API product `extensions.product_addons` lists the
fields (name, label, type, required, options, condition, date limits); `POST /cart/add-item` reads the values from
`extensions.product_addons` as `{<field name>: value | [values]}`.

## Storefront and themes

Store URLs render inside the active theme the way WooCommerce's theme-compatibility mode does: the main
query is a page whose content is the store markup (a synthetic page for products and archives, the real
Cart, Checkout and My account pages for those). The markup keeps WooCommerce's templates, classes and
field names, so themes made for WooCommerce style it. Shortcodes are named `goocommerce_*` and every one
also answers to its `woocommerce_*` alias.

To tell the store that a theme is made for it, declare support in `theme.json`:

```json
"supports": { "woocommerce": true }
```

A theme that does so prints the store titles itself through the store templates instead of the page
title, and is listed under **Pursae Commerce › Store Themes** (the library's store themes, plus installed packages that declare it; a library that cannot be reached shows a notice). Practical rules from the bundled store
themes (`library/themes`, published to Pursae Manager; `category: shop`):

- scope the store CSS under `#page` so it wins over `goocommerce.css`;
- the checkout lays its rows on a grid, so reset the width and float of `.form-row-first` and
  `.form-row-last`;
- store asset URLs carry a content hash, so a changed stylesheet is picked up without a version bump.

The store markup is shown in the site language (with Pursae Multi Language, the language of the
page), like WooCommerce loading its text domain on the front end: the WordPress.org WooCommerce
language pack, downloaded with the site language, translates it, and Pursae's own catalog
(`web/languages`) covers the texts that differ. Only the store's own markup is translated; product
names, descriptions, reviews and the theme around it stay as written. E-mails and the optional
modules' front-end texts are still English.

A store theme may also ship demo content with a `goocommerce` section in `demo/demo.json` (products,
reviews, coupons, shipping), imported with the rest of the demo. The package format is in
`docs/theme-package.md`.

## REST APIs

| Namespace | What | Authentication |
|-----------|------|----------------|
| `wc/store/v1` | The Store API: public cart, checkout and catalog | The storefront session with a `Nonce` header, or a `Cart-Token` |
| `wc/v3` | The management API: products, orders, customers, coupons, settings | Logged-in cookie with `X-WP-Nonce`, an application password, or a consumer key and secret from `goo_api_keys` |

Both follow WooCommerce's JSON shapes, query parameters, error codes and headers, so clients written
for WooCommerce work unchanged.

## Extension points

The store's actions (`product_saved`, `low_stock`, `no_stock`, `order_created`, `order_status_changed`,
`payment_complete`, `order_refunded`, `customer_created`, `cart_emptied`) and product types are Go
registrations (`gooOn`, `gooRegisterProductType` in `internal/server/goo_hooks.go`) used by the
compiled-in modules. Outside the binary, integrate through the REST APIs and webhooks (`goo_webhooks`).
Webhook deliveries run on a small pool of background workers; a failed delivery is tried twice more
(after 10 s and 1 min) with the same `X-WC-Webhook-Delivery-ID`, then counts as one failure, and a
webhook is disabled after 5 consecutive failed deliveries. Deliveries still waiting are lost on restart.

Webhook topics: `coupon|customer|order|product` `.created/.updated/.deleted` (+ `.restored`), the module
topics `subscription.created|updated`, `booking.created|updated` and `membership.created|updated`
(offered while the module is on; in-cart bookings are not delivered), and `action.<hook>`, which
delivers `{"action": hook, "arg": id}` when the internal action of that name fires (with or without
the `woocommerce_` prefix, for example `action.woocommerce_order_status_changed`). Every attempt
is logged in `goo_webhook_deliveries` (time, duration, HTTP code, first 1 KB of the response or the
transport error; the last 50 per webhook), shown on the webhook edit screen with a "Send test
delivery" button; REST: `GET /wc/v3/webhooks/{id}/deliveries`, `POST /wc/v3/webhooks/{id}/ping`.

Module REST (wc/v3, `manage_woocommerce`, 404 while the module is off): `/subscriptions`, `/bookings`,
`/memberships` (user memberships) and `/gift-cards` with list (filters `status`, `customer`, ...),
read and update (status, dates). The Store API add-item body passes `extensions.<namespace>` objects
(`product_addons`, `bookings`, `gift_cards`, ...) to the add-to-cart validation as the posted form.

## Orders under concurrency

A status change is serialized per order and starts from the status stored in the database, so a
payment and the hourly "cancel unpaid orders" job cannot both win (the job only cancels an order that
is still pending). Saving an order loaded earlier does not undo a status change or the stock / coupon
bookkeeping made meanwhile. Stock and coupon uses are taken with conditional updates when an order
goes on hold or is paid: when another order got the last unit or the coupon's last use first, a
payment puts the order on hold with a note ("Not enough units of … are available", "Usage limit for
coupon … has been reached") instead of overselling; products that allow backorders go below zero as
before, and a status set by a person in the admin or through `wc/v3` is applied like WooCommerce does.
Checkout still validates stock but does not reserve it (WooCommerce's `wc_reserved_stock`).

## Known limits

Bookings and some payment methods do not support refunds.

## Analytics

The `wc-analytics` REST namespace serves `reports/revenue/stats`, `reports/orders/stats` (totals and per-interval subtotals; `after`, `before`, `interval=hour|day|week|month|quarter|year`) and `reports/products` for external dashboards, authenticated like wc/v3 (consumer key or core auth, `view_woocommerce_reports`). WooCommerce > Settings > Emails > Store summary enables a weekly e-mail (job `goocommerce_weekly`) with last week's orders, sales, refunds and AOV to the stock notification recipient(s).

Analytics reports (Overview, Products, Revenue, Orders, Variations, Categories, Coupons, Taxes, Downloads, Stock, Customers) and
their CSV downloads carry every row (no server-side cap); the screen lists the first 1,000 rows and says so. Products, Variations
and Categories subtract refunds, matching the Overview. Revenue computes all days with grouped queries. Subscriptions (active,
MRR, new, cancelled, churn), Bookings (bookings, booked hours, cancellations, revenue) and Gift Cards (sold, redeemed,
outstanding balance) tabs appear in the Analytics menu while the module is switched on.

## CSV import and export

- **Products** (Products › Export / Import) use WooCommerce's column names. The exporter has an "Export custom meta" option:
  every product meta key (subscription, booking, gift card and add-on settings, ...) is written as a `Meta: <key>` column
  (strings as they are, numbers, booleans, lists and objects as JSON); the importer maps `Meta: ` columns back (an empty cell
  leaves the stored value alone).
- The importer downloads remote image URLs (http/https, public addresses only) into the Media Library; the source URL is kept
  in the attachment meta `_wc_attachment_source_url`, so the same URL is downloaded once. A URL that cannot be fetched or is
  not an image fails its row.
- The import runs in batches of 50 rows (`step=batch`, JSON progress; the mapping screen shows a progress bar). Without
  JavaScript the form still imports in one request. Abandoned uploads (`goo-import-*.csv` and their state files in the temp
  directory) are deleted after 24 hours by the daily `goo_import_cleanup` event.
- **Orders**, **Coupons** and **Subscriptions** lists have an "Export CSV" link that exports every row matching the list's
  filters (status view, search, month, customer); Customers has "Download". The links carry the store nonce.
