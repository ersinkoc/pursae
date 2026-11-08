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
title, and is listed under **Pursae Commerce › Store Themes**. Practical rules from the bundled store
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
