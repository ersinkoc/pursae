---
title: Upgrading
description: How to update Pursae, what migrations do and how to roll back.
weight: 50
section: getting-started
---

A production site updates itself from Dashboard › Updates in one click (below); when a newer release
exists, every admin page says *Pursae X is available! Please update now.* Otherwise you replace the
binary (or rebuild the image) and start it. Development builds (`make docker-up`, `make dev`) do not
talk to `https://hub.pursae.com` and get no updates from it. Database migrations run automatically on start, before
the server listens.

## From Dashboard › Updates

When Pursae Manager publishes a release built for the site's platform (`core.artifacts` in
`GET /api/v1/index` has the running `GOOS`/`GOARCH`), Dashboard › Updates shows **Update to version
X**. The site then:

1. downloads the binary from the Manager (at most 1 GB), checking its size and SHA-256;
2. verifies the Ed25519 signature by the Manager key named in the release (one of the index `keys`)
   over `pursae-core-v1\n{version}\n{os}-{arch}\n{sha256 hex}`;
3. runs the downloaded binary once (`version`): a binary this system cannot start, or one that
   reports another version, is refused while nothing has changed;
4. keeps the running binary as `<executable>.prev`, records the update in
   `<executable>.update.json`, and renames the new binary over the old one;
5. shows *Updated, restarting…*, records a `core_update` event and re-executes itself; the new
   version runs its migrations on start.

The restart does not drop visitors: the listening socket is handed to the new binary
(`WPGO_LISTEN_FD`), requests in flight finish first (up to 10 seconds), and connections that arrive
meanwhile wait in the kernel's queue until the new version accepts them — a second or two, no
error page, also behind a proxy such as Traefik in Dokploy.

**Rollback.** The new binary confirms the update once it is ready: `/readyz` answers 200, which means the
database is reachable, no migration is pending and the [schema guard](#schema-changes) accepts it
(within two minutes);
until then `.update.json` stays *pending*. If it dies first and is started again — by Docker's
`restart: unless-stopped` (set in `docker/compose.yaml`), systemd's `Restart=always` or any other
supervisor — the second start puts `<executable>.prev` back and runs it, and Dashboard › Updates
says *Pursae X did not start, so the site went back to Y*. If it keeps running but never becomes ready
within the two minutes, it puts `<executable>.prev` back itself and hands the socket over to it
(*Pursae X started but did not answer requests, so the site went back to Y*). If re-executing fails
outright, the previous binary is put back at once. Without a supervisor nothing restarts a crashed process, so
run Pursae under one. Migrations are forward-only: a rolled-back site keeps any migration the new
version applied, so the previous release must still run on the new schema. New migrations follow
the expand/contract rule that guarantees this ([schema changes](#schema-changes)); a few older
ones did not (`0028`, `0038`, `0105` drop tables or columns), and their release notes ask for a
backup.

**Automatic updates.** Dashboard › Updates › *Automatic updates* (off by default, option
`pursae_core_auto_update`) lets the daily Manager check (`wpgo_hub_check`) install a newer release
by itself, through the same steps as the button, rollback included. Only **patch releases of the
running minor version** are installed automatically (0.3.1 → 0.3.2); a new minor or major version
(0.3.x → 0.4.0) waits for a click on *Update to version X*. Before each automatic update the
database is backed up into the backups directory (`WPGO_BACKUPS_DIR`) as a database-only Backup
Manager archive (`pre-update-*.zip`: every table and user, no media, theme or plugin files), also
when the Backup Manager plugin is off; the newest **3** of these are kept. When that backup fails
(the disk is full, another backup or restore is running) the update is skipped, nothing changes and
the next daily check tries again. A release the site went back from is remembered
(`pursae_core_auto_update_skip`) and not installed automatically again — a newer release is, and the
button still offers it. The screen shows the last attempt: installed (with the backup's name),
failed (nothing changed, with the reason), skipped because the backup failed, waiting for a click
(new minor version) or rolled back. Sites that cannot replace their binary (see below) get no
automatic updates either.

**Keeping the previous binary.** Once the update is confirmed, `.update.json` records it as
*confirmed* (with the version the site came from) and `<executable>.prev` is kept for **24 hours**;
then it is removed (checked at start and every hour), so a site never holds more than one extra
binary, and only for a day. A `.prev` left by an older release with no update record is removed
the same way.

**Reinstall the previous version.** Dashboard › Updates › *Previous version* offers **Reinstall
version Y** (Y = the version the site was updated from, when it is older than the running one):

- within the 24 hours the kept `.prev` is checked (`version`) and swapped back over the running
  binary, which becomes the new `.prev`; no download;
- later the signed build of Y is downloaded again from Pursae Manager
  (`GET /api/v1/core/{version}`, [Hub API](../hub/index.md)) and verified exactly like an update;
  without a Manager the button is not shown once `.prev` is gone.

Either way the reinstall is recorded as a *pending* update, so a reinstalled binary that does not
start goes back like any update, and the site restarts with *Reinstalled Pursae Y, restarting…*.
It never happens on its own: a crash after the confirmation is left to the supervisor and to you.
Database migrations of the newer version are **not** undone (the button asks for confirmation);
restore a backup when the older version cannot work with them.

Nothing is replaced when a check fails. The button is replaced by the reason when the executable
cannot be replaced: a `go run` build, or a directory the server may not write. Beta releases are
offered only to sites whose license includes the beta channel. Back up first, as for any upgrade.

Release builds are static and set the version:
`CGO_ENABLED=0 go build -buildmode=exe -tags nodynamic -ldflags "-X wpgo/internal/server.Version=0.2.0" ./cmd/wpgo`
(`make build` and `make release` do this; see [publishing a release](../operations/releases.md)). Static matters: Go's default Linux build asks for the
build host's ELF interpreter, and `nodynamic` keeps the AVIF decoder off purego, which adds one —
such a binary built on a glibc machine does not start in the Alpine image.

## Docker

The image starts with `wpgo launch`: it installs its binary into the `bin` volume (`/data/bin`,
`WPGO_BIN_DIR`) and runs it from there, so an update made from Dashboard › Updates replaces that
copy and survives container restarts. A newer image wins: when the image's binary reports a higher
version than the installed one, `launch` installs the image's. A rebuilt image that reports the
same version (development builds without `VERSION`) also wins when its binary differs from the
installed copy; an installed binary with a higher version (an admin update) is kept.

On a production stack (`make prod-up`) use Dashboard › Updates. To update from source instead:

```bash
git pull && make prod-up  # rebuilds the image; migrations run on start
```

A development stack is updated the same way with `git pull && make docker-up`.

## Binary

```bash
git pull
make build
systemctl restart wpgo    # or however you run it
```

Several instances may start at the same time: the migration runner takes a PostgreSQL advisory
lock, applies the missing migrations once and releases it. `wpgo migrate` applies them without
serving, which is handy in a deploy step.

## Before you upgrade

1. **Back up** the database and the uploads directory ([backups](../operations/backups.md)).
   Migrations are forward-only; the backup is your rollback.
2. Read the release notes. The Hub lists them on Dashboard › Updates when the site is connected
   ([Hub](../hub/index.md)).
3. Check plugin compatibility: a plugin's `plugin.json` names the minimum core version it needs
   (`requires`); the Plugins screen refuses to activate one that needs a newer core.

## Schema changes

For contributors: how a migration keeps the previous release working, so an update can roll back
(above) and several instances can run different versions during a deploy.

- **Expand, then contract.** A release may add tables, columns (nullable or with a default),
  indexes and constraints the previous release does not violate. It may not drop, rename or
  retype anything the previous release still reads or writes. To remove or change something,
  release N stops using it (and, for a rename, writes both the old and the new place); release
  N+1 or later drops it. The previous minor release then still runs on the new schema.
- **Mark the contract step.** A migration that drops a table or column, changes a column's type
  or renames anything carries a `-- pursae:contract` comment that says which release stopped
  using it. `TestMigrationsExpandContract` (`internal/db/migrations_lint_test.go`) fails on such
  a statement in a new migration without the marker; `0028`, `0038` and `0105` predate the rule.
- **File names.** `NNNN_name.sql` with the next free number. The duplicate numbers that shipped
  (`0016` ×9, `0020`, `0032`, `0098` ×2) stay as they are, since `schema_migrations` records the
  full name; `TestMigrationFileNames` refuses new ones.
- **Large rewrites.** A migration that rewrites a large table (like `0105`, which re-adds the
  generated `posts.search` column and so rewrites every post) or locks it for long is called out
  in the CHANGELOG and the release notes, with its expected duration. Index builds on large
  tables use `CREATE INDEX CONCURRENTLY` in their own `-- wpgo:no-transaction` file
  (`internal/db/db.go`).
- **The upgrade test.** `make upgrade-test` (the `upgrade` CI job, on every pull request) builds
  v0.2.0 and the three newest release tags, lets each install and fill a site through its own
  installer, login and REST API (users, terms, media, posts, pages, a custom field, comments, a
  menu, settings, an Application Password, a Web Builder page), then runs this checkout's
  `migrate` and `serve` on it. It fails when a table loses rows in the migration, a seeded user
  cannot log in with the old password, any recorded field differs, the uploaded file changed, or
  the front page or a permalink stops answering 200 ([install from source](install-binary.md)).

## Release notes

- **Pursae Web Builder: Theme Builder and Hello Pursae.** Headers, footers and single, page,
  custom post type, front page, archive, search and 404 templates are designed in the builder
  (Web Builder › Theme Builder) and shown by the new bundled theme **Hello Pursae**
  ([Theme Builder guide](../web-builder-theme-builder.md)). Migration
  `0029_web_builder_templates.sql` adds the `wb_template` post type; nothing existing is changed.
  - **Sites with the plugin already on are not switched.** The upgrade keeps the active theme and
    every page as it was; the Dashboard, Plugins and Themes screens show a one-time notice
    **Switch to Hello Pursae** (or **Dismiss**) to users who may switch themes. Templates show
    only once Hello Pursae is active. From now on, activating the plugin switches to Hello Pursae
    (remembering the previous theme, with a **Switch back** link) and deactivating it switches
    back.
  - Pages built before this release that have their own navigation or footer section keep
    rendering as full-page canvases (no doubled header); other builder pages show the header and
    footer templates around them once Hello Pursae is active. Each page can choose under
    **Header & footer** in its page settings.
  - The builder's API version is now 2: an editor tab left open across the upgrade asks to reload.
    Published pages need no regeneration.
  - Public Pursae Custom Fields post types now have pretty permalinks (`/<slug>/<post>/`) and,
    when enabled, an archive (`/<archive slug>/`); old `?p=<id>` links redirect to them. Check
    that no page or post uses the same slug as a custom post type's rewrite or archive slug.
- **New Pursae Web Builder — back up the database before upgrading: old Web Builder data is
  deleted.** The release that brings the new builder replaces the previous Elementor-derived builder
  and deletes everything it stored, with no converter (migration `0028_drop_old_builder`): its tables (builder revisions,
  global classes and variables, default styles, form submissions), saved templates, theme parts,
  popups and components, every `_elementor_*`/`_gwe_*` post meta key, its options (integration API
  keys, reCAPTCHA keys, custom fonts and icon sets, role manager, site kit) and the themes saved
  with it. Pages keep their content as plain HTML; pages on the old Canvas or Full Width template
  go back to the theme's default template. At the next start the old builder's generated CSS, form
  uploads and custom icon sets are removed from the uploads directory (local fonts stay), and a site
  on Hello Web Builder or a theme saved with the old builder moves to its previous theme or Twenty
  Twenty-Five. Restoring the backup is the only way back. The plugin keeps its switch (active stays
  active); a page built with the old builder opens in the new one as a new document, so rebuild it
  there and publish. Features the new builder does not have yet (theme builder, popups, form
  integrations, payments, Pursae Commerce widgets, global classes and variables, kits) are gone with
  it; see [Pursae Web Builder](../web-builder.md#limits).

## Rolling back

Stop the new version, restore the database backup, start the previous binary. Uploads, plugin
packages and theme packages are plain files and are not changed by an upgrade.

## Plugins and themes

Plugin and theme packages are updated through Plugins › Add Plugin and Appearance › Add Theme
(upload a newer package, or install the newer version from the marketplace). Re-installing a plugin
with the same slug replaces the package and keeps the granted permissions; deactivate and activate
it again to load the new version, and review the permissions it asks for on the activation screen.
