---
title: Troubleshooting
description: The common problems after install and what to do about them.
weight: 90
section: operations
---

**The site redirects to `/wp-admin/install` again after a restart.** The database is empty or a
different one is being used. Check that the `pgdata` volume still exists (`docker volume ls`) and
that `DATABASE_URL` points to the database you installed into.

**Docker restarted and the site is down.** Containers do not always come back after the Docker
daemon restarts. Start them again — data is kept:

```bash
docker compose -f docker/compose.yaml up -d   # production: make prod-up
```

**Links or assets point at the wrong address (old IP or port).** The site URL was stored at
install time. Change it under Settings › General, or start over with the right `WPGO_SITE_URL`.

**Start from scratch.** This **deletes** every volume: the database, uploads, plugins, edited
themes, backups and the updated binary:

```bash
docker compose -f docker/compose.yaml down -v
make docker-up
```

**Port 8088 or 5432 is already in use.** (The development file publishes PostgreSQL on
`127.0.0.1:5432`; the production file does not publish it.) Pick other host ports:

```bash
WPGO_PORT=9000 WPGO_DB_PORT=5433 WPGO_SITE_URL=http://localhost:9000 make docker-up
```

**The first build is slow or fails to download modules.** The Go image downloads modules on the
first build; make sure Docker has network access. Later builds are cached.

**Logging in works but every admin action says the nonce is invalid.** The instances behind your
load balancer have different `WPGO_SECRET`s. Set the same value on all of them.

**I see `http://` links behind my TLS proxy.** The proxy must send `X-Forwarded-Proto: https`,
and `WPGO_SITE_URL` (or Settings › General) must be the `https://` address.

**A plugin package will not install: "signature could not be verified".** The package is unsigned
or signed with a key the site does not trust. Add the author's public key to `WPGO_PLUGIN_KEYS`
or the `plugin_trusted_keys` option; for local development set `plugin_allow_unsigned`
([packaging and signing](../plugins/packaging-and-signing.md)).

**A plugin shows "disabled after 5 consecutive failures".** Open Plugins and read its log lines
(the last five are shown under the plugin). Deactivate and activate it to reset.

**Scheduled posts do not publish / e-mails are not sent.** The scheduler runs inside `wpgo serve`;
check the server log and Tools › Site Health for failed jobs ([cron](cron.md)).

**"Template error" on the front end after editing a theme file.** The Theme File Editor rolls
back a broken save automatically and shows the error; if you edited files on disk under
`WPGO_THEMES_DIR`, fix the file or delete it to fall back to the embedded copy.

**`make prod-up` says "Create docker/.env first" or "set WPGO_DB_PASSWORD".** Copy the template
(`cp docker/.env.example docker/.env`) and fill in `WPGO_DB_PASSWORD` and `WPGO_SITE_URL`.

**Dashboard › Updates never offers a new version.** A development build (`make docker-up`,
`make dev`) does not talk to `https://hub.pursae.com`; run the production build (`make prod-up`).

**Where are the logs?** Standard output of the process: `docker compose logs -f wpgo`
(`make prod-logs` on a production stack) or your service manager's journal. Every request is logged with status and duration.
