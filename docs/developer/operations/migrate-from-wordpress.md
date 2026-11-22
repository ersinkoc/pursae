---
title: Migrate from WordPress
description: Move a whole WordPress site, WooCommerce store included, with the WP to Pursae plugin.
weight: 32
section: operations
---

The **WP to Pursae** WordPress plugin moves a whole site to Pursae in one go. Users keep their
passwords, and the content, media, menus, settings and the WooCommerce store (products, orders,
customers, coupons) come along. The plugin lives in this repository under
[`integrations/wordpress/wp-to-pursae`](../../../integrations/wordpress/wp-to-pursae/).

For a quick content-only move, the plain WXR export and import still works (Tools › Export in
WordPress, Tools › Import here, see [Backups](backups.md#content-export)). It does not carry
passwords, settings or orders.

## Before you start

1. Install Pursae and finish its setup ([Docker](docker.md) or [the binary](../getting-started/install-binary.md)).
   Use the administrator login you want to keep. If a WordPress user has the same login or e-mail,
   the Pursae account wins and keeps its password.
2. On the WordPress site, install the plugin: zip the `wp-to-pursae` folder and upload it under
   Plugins › Add New › Upload Plugin, then activate it. It needs WordPress 6.0+ and PHP 7.4+.
3. If the store sells, put WordPress in maintenance mode first. Orders placed after the package is
   built are not in it.

## Way 1: send it from WordPress (recommended)

1. In Pursae, open **Tools › Import › From WordPress** and click **Generate a migration token**.
   The token is shown once. It works for a single import and expires after 24 hours. On the command
   line, `pursae import wordpress-token` does the same.
2. In WordPress, open **Tools › Move to Pursae**, choose **Send to Pursae**, and enter the Pursae
   address and the token. Click **Move to Pursae**.
3. The plugin builds the package in short steps, then uploads it in 8 MB chunks. A dropped
   connection only costs one chunk: the plugin asks Pursae how much arrived and goes on from there.
   Keep the tab open until it says the package was sent.
4. Pursae imports in the background. The plugin and the Pursae screen both show the progress, and
   Pursae shows the report at the end.

With WP-CLI: `wp pursae push --to=https://new.example.com --token=pmt_…`.

Instead of a token you can use a Pursae administrator's Application Password with full access
(the plugin's "Use an Application Password instead"). The plugin then sends the package key with
the first request, so this only works over HTTPS.

The plugin refuses plain `http://` addresses, because the package holds password hashes. The
exceptions are local test hosts (`localhost`, `*.test`, `*.local`, `host.docker.internal`). To
allow anything else, define `WP2P_ALLOW_HTTP` as `true` in `wp-config.php`.

## Way 2: download a package

1. In WordPress, open **Tools › Move to Pursae**, choose **Download a package** and click
   **Move to Pursae**. When the package is ready, copy the **package key**. It is shown on that
   screen only, and Pursae cannot open the package without it. Then download the `.wp2p` file.
2. In Pursae, open **Tools › Import › From WordPress › Upload a package**, enter the key, choose
   the file and click **Upload and import**. For large packages, the command line is faster:

   ```sh
   PURSAE_WP2P_KEY='…' pursae import wordpress example.com-2026-11-22.wp2p
   ```

   (or `--key=…`; the environment variable keeps the key out of the shell history). Run it with
   the server's environment, where the uploads directory is mounted.

With WP-CLI: `wp pursae export --file=site.wp2p` prints the key.

## What moves

| | |
|---|---|
| **Users** | Login, e-mail, display name, website, registration date, roles (a role Pursae does not have becomes Subscriber, as the report says), first and last name, nickname, bio, locale, WooCommerce billing and shipping addresses. The **password hash** is kept as WordPress stored it (`$wp$2y$…` from 6.8, phpass `$P$…` before, bcrypt, MD5), so users sign in with their old password. The first sign-in moves the hash to argon2id. A user whose login or e-mail already exists here is not changed: their content is assigned to the existing account. |
| **Content** | Everything Tools › Export › All content exports: posts, pages, custom post types, comments, categories, tags and custom taxonomies, patterns, navigation menus. Pursae imports it with its WXR importer, so content runs through the same checks and mapping (Yoast and Rank Math SEO fields too). |
| **Media** | The original file of every attachment travels in the package, so nothing is downloaded from the old site. Image sizes are generated again. Links in the content are rewritten to the new files. Files over 64 MB (Pursae's upload limit) are left out and listed in the report. Copy those by hand. |
| **Settings** | Title, tagline, permalink structure, time zone, date and time formats, reading settings (front page, posts page, posts per page), discussion and media settings, site icon, language. Menus go to theme locations with the same name, if the location is free. |
| **WooCommerce** | Products and variations, with attributes, categories, tags, images, prices, stock and downloads (through the WXR importer). Store settings that Pursae Commerce has (currency, address, taxes, …). Tax rates. Coupons with their type, amount, expiry, limits, usage counts and product, category and e-mail restrictions. Orders with statuses, line items (linked to the imported products and variations), shipping, fee, coupon and tax lines, totals, customer, addresses, payment method title, transaction id, notes, refunds, download permissions and dates. Customer accounts with their addresses. |

Orders are read through WooCommerce's own API, so it does not matter whether the store uses the
High-Performance Order Storage tables or the older posts storage. Orders arrive with the bookkeeping
they had: no stock is reduced again, no e-mails are sent, sales are not counted twice. Each order
keeps its WordPress number in the meta field `_wp2p_order_id`.

## What does not move

- **PHP themes and plugins.** Pursae does not run PHP. Pick a Pursae theme under Appearance ›
  Themes. Most popular plugins have a built-in equivalent; the report names the active plugins
  that did not move.
- **Payment gateway settings and API keys.** They are deliberately not exported. Set up payments
  again in Pursae Commerce.
- **WooCommerce extensions' own data** (subscriptions, bookings, memberships) and order statuses
  that come from other plugins. Such orders become *On hold*, and their original status is kept
  in the meta field `_wp2p_original_status`.
- **The Cart and Checkout blocks.** Pursae Commerce renders its own cart, checkout and account
  pages. The importer replaces the WooCommerce blocks or shortcodes on the store's cart, checkout
  and My account pages with Pursae Commerce's (`[goocommerce_cart]`, …), and the report says so.
  Other Woo blocks placed elsewhere in the content stay as they are.
- **Multisite networks.** The plugin moves the site it runs on, with the users of that site. On a
  network only a super admin can use it: a site's own administrators would otherwise export the
  password hashes of network users, super admins included.
- **Domain.** If the site moves to another address, links inside the content still point at the
  old one. The report then gives the `pursae search-replace` command that fixes them.

## Running it again

Importing the same site again is safe. Every user, post, term, order, coupon and tax rate is
recorded with its WordPress id (table `wp2p_map`, per source site), so a second import only adds
what is new. If an import stops (a restart, an error), the screen offers **Resume the import**,
which continues with the step that was running. Running `pursae import wordpress` again does the
same.

## Security

- The package is encrypted with libsodium's `crypto_secretbox` (XSalsa20-Poly1305), in chunks that
  cannot be reordered, dropped or cut off without the import noticing. A downloaded package uses a
  random key that only the owner sees. A pushed package uses a key derived from the migration token.
- The plugin writes nothing unencrypted. Every batch of users, orders or content goes straight into
  the encrypted file. Its working directory has a random name and refuses web access (`.htaccess`,
  `web.config`); define `WP2P_WORK_DIR` to keep it outside the web root. The package is deleted once
  it is sent, when the owner deletes it, or after 24 hours (an hourly cron event, also removed when
  the plugin is deactivated or deleted).
- The plugin's screen and its requests need `manage_options` (on multisite: a super admin) and
  carry nonces. Password hashes are never shown or logged.
- The build runs in short requests that each stop after a time budget, including the content: it
  is exported with WordPress' own `export_wp()` in ranges of post ids, so a large site never needs
  one long request, and a request that dies redoes only its own batch.
- In Pursae, the import routes accept either the token or an administrator with `manage_options`
  and `import`. A token is stored only as a SHA-256 hash, works only for its own import, cannot
  start a second upload, and expires after 24 hours. Uploads and extracted files are kept outside
  the uploads directory (`<uploads>/../wp2p-imports`). The package key is erased once the package is
  opened, and the files are removed when the import ends.

## The package format

A `.wp2p` file starts with `PURSAEWP`, a format byte (2) and a 16-byte random package id. Then
come chunks: a big-endian `uint32` with the length of what follows, a random 24-byte nonce, then
the chunk sealed with `crypto_secretbox` under the 32-byte key and that nonce. The sealed plaintext
starts with the big-endian `uint64` chunk index, with the top bit set on the last chunk, so chunks
cannot be reordered, dropped or cut off. Nonces are random because the plugin seals a chunk again
when a request died after writing it; format 1, which used the id followed by the index as the
nonce, then reused a nonce, and is not read any more. Keys are shown as 43 characters of
unpadded base64url. A token's key is `HMAC-SHA256(key = token, message = "pursae-wp2p-package-key-v1")`.

The plaintext is a ustar archive with:

| Entry | Content |
|---|---|
| `manifest.json` | format, generator, site and home URL, WordPress, WooCommerce and PHP versions, order storage, counts, items left out |
| `users.NNNNNN.jsonl` | one user per line: id, login, email, pass (the stored hash), nicename, display_name, url, registered, roles, meta |
| `options.json` | `options` (the moved settings), `menu_locations`, `theme`, `active_plugins` |
| `content.NNNNNN.xml` | the WXR of `export_wp()`, in parts read one after the other |
| `media.NNNNNN.jsonl`, `media/<id>` | the attachment files by attachment id, and their names |
| `woo/tax_rates.jsonl`, `woo/coupons.NNNNNN.jsonl`, `woo/orders.NNNNNN.jsonl`, `woo/customers.NNNNNN.jsonl` | WooCommerce data; order items carry WooCommerce's raw item meta |

The importer reads only these names and ignores anything else.

## Testing

`make wp2p-test` runs the end-to-end test in `integrations/wordpress/test`. It needs Docker and
internet access. It starts WordPress with WooCommerce, seeds users with known passwords, content,
media, a menu, products (one variable), orders (one refunded), coupons and customers, and builds
the package with `wp pursae export`. A binary built from the checkout imports it into the
`wpgo_wp2p` database. The test then checks that every user signs in with their WordPress password,
and that content, media, menus, products, orders, refunds, coupons and customers match. Finally it
sends the same site again with `wp pursae push` and checks that nothing is duplicated. The Go
tests (`internal/server/wp2p_test.go`) cover the format, the importer and the push API.
