---
title: What is Pursae
description: WordPress core rewritten in Go, one binary on PostgreSQL, with the WordPress admin, the block editor, sandboxed plugins and classic themes.
weight: 10
section: getting-started
---

Pursae is a rewrite of WordPress core in Go. It ships as **one binary** that talks to a
**PostgreSQL** database and serves the familiar WordPress admin (`/wp-admin/`), the block editor,
the `wp/v2` REST API, classic themes and a page builder. Plugins are **WebAssembly packages** that
run in a sandbox with permissions you grant at activation, so a broken plugin cannot take the site
down or read what it was not allowed to.

## What you get

- **Admin** — Dashboard, Posts, Pages, Media, Comments, Appearance (Themes, Customize, Widgets,
  Menus, Theme File Editor), Plugins, Users, Tools, Settings. Same markup and CSS as WordPress.
- **Editors** — the block editor (Pursae Editor, built on the `@wordpress/*` packages) and a built-in
  Classic Editor plugin (TinyMCE).
- **Pursae Web Builder** — a section-based visual page builder that publishes pages as static
  HTML, off on a fresh install. See [Pursae Web Builder](../web-builder.md).
- **REST API** — `wp/v2` for posts, pages, media, terms, users, comments, settings and more, with
  cookie + nonce and Application Password authentication. See the [REST API](../rest-api/index.md).
- **Themes** — Twenty Sixteen, Twenty Seventeen, Twenty Twenty, Twenty Twenty-One and
  Classic, plus [theme packages](../themes/index.md) you upload as a zip.
- **Plugins** — signed WebAssembly packages written in Go with the [plugin SDK](../plugins/index.md).
- **WPGo Cache** — a built-in full-page cache with tag-based purging.
- **System** — migrations on start, a job runner for scheduled events and e-mail, WXR import/export.

## Requirements

| | Docker route | Binary route |
|---|---|---|
| Runtime | Docker Desktop or Docker Engine + Compose v2 | Go 1.27 to build; nothing else to run |
| Database | started by compose (PostgreSQL 17) | PostgreSQL 14 or newer (17 is what compose uses) you can create tables in |
| Memory | 512 MB is comfortable | same |
| Ports | 8088 for the site, 5432 for PostgreSQL | whatever `WPGO_ADDR` says (default `:8080`) |

Pursae runs on Linux, macOS and Windows (amd64 and arm64). The Docker image is based on
Alpine and runs as an unprivileged user.

## Where to go next

1. [Install with Docker](install-docker.md) — the fastest way to a running site.
2. [Install the binary](install-binary.md) — build from source and run against your own PostgreSQL.
3. [Your first site](first-site.md) — the installer, logging in, the first post.
4. [Upgrading](upgrading.md) — how updates and migrations work.
