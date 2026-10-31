---
title: Upgrading
description: How to update Pursae, what migrations do and how to roll back.
weight: 50
section: getting-started
---

A connected site can update itself from Dashboard › Updates (below); otherwise you replace the
binary (or rebuild the image) and start it. Database migrations run automatically on start, before
the server listens.

## From Dashboard › Updates

When the Hub publishes a release built for the site's platform (`core.artifacts` in
`GET /api/v1/index` has the running `GOOS`/`GOARCH`), Dashboard › Updates shows **Update to version
X**. The site then:

1. downloads the binary from the Hub (at most 1 GB), checking its size and SHA-256;
2. verifies the Ed25519 signature by the Hub key named in the release (one of the index `keys`)
   over `pursae-core-v1\n{version}\n{os}-{arch}\n{sha256 hex}`;
3. writes it next to the running executable (temporary file, fsync, mode 0755) and renames it over
   the old one;
4. shows *Updated, restarting…*, records a `core_update` event and re-executes itself; the new
   version runs its migrations on start.

Nothing is replaced when a check fails. The button is replaced by the reason when the executable
cannot be replaced: a `go run` build, or a binary (or directory) the server may not write — as in
the Docker image, where you pull the new image instead. Beta releases are offered only to sites
whose license includes the beta channel. Back up first, as for any upgrade.

Release builds set the version with
`go build -ldflags "-X wpgo/internal/server.Version=0.2.0" ./cmd/wpgo`.

## Docker

```bash
git pull
make docker-up            # rebuilds the image; migrations run on start
```

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

## Rolling back

Stop the new version, restore the database backup, start the previous binary. Uploads, plugin
packages and theme packages are plain files and are not changed by an upgrade.

## Plugins and themes

Plugin and theme packages are updated through Plugins › Add Plugin and Appearance › Add Theme
(upload a newer package, or install the newer version from the marketplace). Re-installing a plugin
with the same slug replaces the package and keeps the granted permissions; deactivate and activate
it again to load the new version, and review the permissions it asks for on the activation screen.
