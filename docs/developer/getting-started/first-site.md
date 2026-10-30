---
title: Your first site
description: The installer, the admin, the first post and what a fresh install contains.
weight: 40
section: getting-started
---

## The installer

Open the site. Until it is installed every request redirects to `/wp-admin/install`, the same
five-minute install as WordPress:

1. **Site title**
2. **Username** and **password** of the first administrator
3. **Email** of that administrator
4. whether to ask search engines not to index the site

The installer creates the "Hello world!" post with its first comment, the Sample Page, a draft
Privacy Policy page and the default sidebar widgets. Then sign in at `/wp-login`.

> Admin URLs have no `.php` suffix (`/wp-admin/edit`, `/wp-login`). The WordPress spellings
> (`/wp-admin/edit.php`, `/wp-login.php`) still work: GET requests redirect permanently and POSTs
> are served in place, so bookmarks, clients and tutorials written for WordPress keep working.

## The admin

`/wp-admin/` is the WordPress dashboard. The menu is the one you know:

| Menu | Notes |
|------|-------|
| Posts, Pages, Media, Comments | the block editor opens by default; switch to the Classic Editor under Settings › Writing |
| Appearance | Themes (with Live Preview), Customize, Widgets, Menus, Theme File Editor; Add Theme uploads [theme packages](../themes/index.md) |
| Plugins | Installed Plugins, Add Plugin (marketplace, upload); activating a plugin shows the permissions it asks for |
| Users | roles administrator, editor, author, contributor, subscriber, with per-user capability overrides |
| Tools | Import and Export (WXR), Site Health, personal data export and erasure |
| Settings | General, Writing, Reading, Discussion, Media, Permalinks, Privacy, plus the pages plugins add |
| WPGo Cache | the built-in page cache |

## Write a post

Posts › Add New opens the block editor. Write, add media (the Media Library modal is the real
`wp.media`), pick categories and tags, then **Publish**. The post appears on the front page with
the active theme and in the REST API at `/wp-json/wp/v2/posts`.

To use shortcodes from a plugin, type `[gopher-facts]` in a Shortcode block or a paragraph; the
plugin expands it when the page renders ([shortcodes](../plugins/shortcodes.md)).

## Pretty permalinks

Settings › Permalinks offers the WordPress structures (`/%year%/%monthnum%/%postname%/`,
`/%postname%/`, …). No web-server rewrite rules are needed: the Go server routes them itself.

## Next steps

- Install a theme: Appearance › Themes › Add Theme ([marketplace](../hub/marketplace.md)).
- Install a plugin: Plugins › Add Plugin, or upload a package.
- Put the site behind TLS: [reverse proxy and TLS](../operations/reverse-proxy-and-tls.md).
