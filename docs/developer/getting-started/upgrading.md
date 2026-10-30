---
title: Upgrading
description: How to update WordPress Go, what migrations do and how to roll back.
weight: 50
section: getting-started
---

WordPress Go has no in-place updater: you replace the binary (or rebuild the image) and start it.
Database migrations run automatically on start, before the server listens.

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
