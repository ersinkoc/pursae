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

**Rollback.** The new binary confirms the update once it answers requests (within two minutes);
until then `.update.json` stays *pending*. If it dies first and is started again — by Docker's
`restart: unless-stopped` (set in `docker/compose.yaml`), systemd's `Restart=always` or any other
supervisor — the second start puts `<executable>.prev` back and runs it, and Dashboard › Updates
says *Pursae X did not start, so the site went back to Y*. If re-executing fails outright, the
previous binary is put back at once. Without a supervisor nothing restarts a crashed process, so
run Pursae under one. Migrations are forward-only: a rolled-back site keeps any migration the new
version applied, which is why releases only add schema.

Nothing is replaced when a check fails. The button is replaced by the reason when the executable
cannot be replaced: a `go run` build, or a directory the server may not write. Beta releases are
offered only to sites whose license includes the beta channel. Back up first, as for any upgrade.

Release builds are static and set the version:
`CGO_ENABLED=0 go build -buildmode=exe -tags nodynamic -ldflags "-X wpgo/internal/server.Version=0.2.0" ./cmd/wpgo`
(`make build` and Manager's Rebuild do this). Static matters: Go's default Linux build asks for the
build host's ELF interpreter, and `nodynamic` keeps the AVIF decoder off purego, which adds one —
such a binary built on a glibc machine does not start in the Alpine image.

## Docker

The image starts with `wpgo launch`: it installs its binary into the `bin` volume (`/data/bin`,
`WPGO_BIN_DIR`) and runs it from there, so an update made from Dashboard › Updates replaces that
copy and survives container restarts. A newer image wins: when the image's binary reports a higher
version than the installed one, `launch` installs the image's.

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
