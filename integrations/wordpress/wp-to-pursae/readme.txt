=== WP to Pursae ===
Contributors: pursae
Tags: migration, export, woocommerce, move site, pursae
Requires at least: 6.0
Tested up to: 7.1
Requires PHP: 7.4
Stable tag: 1.0.0
License: GPL-2.0-or-later
License URI: https://www.gnu.org/licenses/gpl-2.0.html

Move your WordPress site to Pursae in one go: users with their passwords, content, media, menus, settings and your WooCommerce store.

== Description ==

[Pursae](https://pursae.com/) is WordPress rewritten in Go: one binary on PostgreSQL, with the WordPress admin, the block editor and a WooCommerce-compatible store built in. WP to Pursae moves an existing WordPress site there.

What moves:

* **Users**, with their roles and profiles. Their password hashes come along as WordPress stored them, so everyone signs in with the password they already have.
* **Content**: posts, pages, custom post types, comments, categories, tags, menus and patterns (WordPress' own export format).
* **Media**: the original files travel inside the package; nothing has to be downloaded from the old site.
* **Settings**: site title and tagline, permalinks, time zone, formats, reading, discussion and media settings.
* **WooCommerce**: products and variations, store settings, tax rates, coupons, orders (line items, shipping, fees, taxes, refunds, notes, download permissions) and customers. It works with both order storages (High-Performance Order Storage and the older posts storage).

What does not move: PHP themes and plugins (Pursae does not run PHP; it has built-in equivalents of the popular ones), payment gateway settings and API keys (set up payments again), and the data of WooCommerce extensions such as subscriptions.

Two ways to move:

1. **Send to Pursae** (recommended). In Pursae, generate a one-time migration token (Tools › Import › From WordPress). Enter the Pursae address and the token here. The plugin builds the package and uploads it in chunks, resuming if the connection drops, and Pursae imports it on its own.
2. **Download a package**, then upload it in Pursae with its key, or import it on the server with `pursae import wordpress site.wp2p --key=…`.

WP-CLI: `wp pursae export --file=site.wp2p` and `wp pursae push --to=https://new.example.com --token=…`.

= Security =

* The package holds your users' password hashes, so it is always encrypted (libsodium secretbox, in authenticated chunks). A downloaded package has a random key that only you see. A pushed package uses a key derived from the one-time token.
* Nothing is written unencrypted. Every batch goes straight into the encrypted file, in a working directory with a random name that refuses web access. Define `WP2P_WORK_DIR` in `wp-config.php` to keep it outside the web root.
* The package is deleted once it is sent, when you delete it, or after 24 hours.
* Only administrators (`manage_options`) can use the plugin, and on multisite only super admins; every request carries a nonce. Sending requires HTTPS, except for local test hosts. Define `WP2P_ALLOW_HTTP` to allow plain HTTP elsewhere.
* Password hashes are never displayed or logged.

== Installation ==

1. Upload the `wp-to-pursae` folder to `/wp-content/plugins/`, or upload the zip under Plugins › Add New › Upload Plugin.
2. Activate the plugin.
3. Open Tools › Move to Pursae.

== Frequently Asked Questions ==

= Do my users have to reset their passwords? =

No. Pursae checks WordPress password hashes (WordPress 6.8's bcrypt, the older phpass hashes, plain bcrypt and MD5) and moves each one to argon2id at the user's first sign-in.

= Can I run it again? =

Yes. Pursae remembers what it imported from your site. A second package only adds what is new: new orders, new posts, new users.

= My store keeps selling while I move. =

Orders placed after the package was built are not in it. Put the store in maintenance mode first, or move again just before you switch the domain over: the second move adds only the new orders.

= Large sites? =

The package is built in short steps that survive PHP time limits, and uploaded in 8 MB chunks. Media files over 64 MB are left out and listed (copy them by hand). The filter `wp2p_max_media_bytes` changes that limit.

== Changelog ==

= 1.0.0 =
* First release: users with password hashes, content, media, menus, settings, WooCommerce products, tax rates, coupons, orders, refunds and customers; download or send to Pursae; WP-CLI commands; Turkish translation.
