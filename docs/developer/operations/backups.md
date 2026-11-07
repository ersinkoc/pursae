---
title: Backups
description: What to back up, how to dump and restore PostgreSQL, and how to move a site.
weight: 30
section: operations
---

A site is three things: the **database**, the **uploads** directory and the **plugins and themes**
directories next to it. The binary and the bundled themes need no backup.

| What | Where (compose) | Where (binary) |
|------|-----------------|----------------|
| database | volume `pgdata` | your PostgreSQL |
| media | volume `uploads` (`/data/uploads`) | `WPGO_UPLOADS_DIR` |
| plugin packages | volume `plugins` (`/data/plugins`) | `<uploads>/../plugins` |
| theme packages and edited theme files | volume `themes` (`/data/themes`) | `WPGO_THEMES_DIR` |

## Database

Dump:

```bash
docker compose -f docker/compose.yaml exec db pg_dump -U wpgo -Fc wpgo > wpgo.dump
```

Restore into the same database:

```bash
docker compose -f docker/compose.yaml exec -T db pg_restore -U wpgo -d wpgo --clean --if-exists < wpgo.dump
```

With your own PostgreSQL use `pg_dump -Fc "$DATABASE_URL" > wpgo.dump` and
`pg_restore -d "$DATABASE_URL" --clean --if-exists wpgo.dump`.

## Files

```bash
docker run --rm -v wpgo_uploads:/data/uploads -v wpgo_plugins:/data/plugins -v wpgo_themes:/data/themes \
  -v "$PWD":/backup alpine tar czf /backup/wpgo-files.tgz /data
```

(Volume names are prefixed with the compose project name, `wpgo`.)

## Moving a site

1. Dump the database and copy the files to the new host.
2. Start the new instance with the same `WPGO_SECRET` if you want existing sessions to survive.
3. Restore the dump, then update the URL under Settings › General if the address changed. The
   site URL is stored in the database, not in the environment, once installed.

## Backup Manager

The built-in **Backup Manager** plugin (off by default) makes the same backup from the admin screen as
one `.zip` (database rows as JSON lines plus the files), restores it on this or another site, and can
run **scheduled local backups** (daily or weekly, keeping the newest *N*). Archives go to
`WPGO_BACKUPS_DIR` (default `<uploads>/../backups`, never inside the public uploads). Scheduled backups
stay on the same server: copy them elsewhere (for example with the `tar` command above) — remote
storage such as S3 or FTP is not included.

## Content export

Tools › Export writes a WXR file with posts, pages, media references, comments and terms; Tools ›
Import reads one. It is a content transfer, not a backup: it does not carry users' passwords,
settings, plugins or themes.
