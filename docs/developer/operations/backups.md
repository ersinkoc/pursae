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
| Backup Manager archives | volume `backups` (`/data/backups`) | `WPGO_BACKUPS_DIR` |

The other volumes need no backup: `bin` (the updated binary; the image or Dashboard › Updates puts
one back), `pagecache`, `languages` (downloaded again) and `mail` (development `.eml` files).

## Database

Dump (on a `make prod-up` stack add `-f docker/compose.prod.yaml` after `-f docker/compose.yaml`):

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
  -v wpgo_backups:/data/backups -v "$PWD":/backup alpine tar czf /backup/wpgo-files.tgz /data
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
`WPGO_BACKUPS_DIR` (default `<uploads>/../backups`, the `backups` volume in Docker, never inside the
public uploads). Scheduled backups
stay on the same server: copy them elsewhere (for example with the `tar` command above) — remote
storage such as S3 or FTP is not included.

A restore keeps this site's own secret (`auth_secret`), address and Backup Manager settings. Secrets
the built-in plugins store encrypted under the archive site's secret (Pursae SMTP passwords and
tokens, Pursae CDN credentials, the Multi Language translation key, Pursae Security authenticator
apps) are re-encrypted for this site, so they keep working after a move; Pursae Security backup codes
are only stored as hashes and have to be generated again. If the archive's site encrypted with
`WPGO_SECRET`, start this site with the same value instead. An archive with accounts whose username or
e-mail differs only by case restores too: those columns then stay non-unique regardless of case, as
on the site the archive came from.

## Content export

Tools › Export writes a WXR file with posts, pages, media references, comments and terms; "All
content" also carries the posts and terms of Pursae Custom Fields post types and taxonomies. Tools ›
Import reads one; items of a post type not registered on the site are skipped and listed in the
report. It is a content transfer, not a backup: it does not carry users' passwords,
settings, plugins or themes.
