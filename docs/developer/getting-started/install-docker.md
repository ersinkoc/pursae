---
title: Install with Docker
description: Run Pursae and PostgreSQL with docker compose in a couple of minutes.
weight: 20
section: getting-started
---

You need **Docker** (Docker Desktop on macOS/Windows, Docker Engine with Compose v2 on Linux) and
**git**.

## On a server

The production build talks to `https://hub.pursae.com`, so core updates are one click in
Dashboard › Updates. Settings go in `docker/.env` (git-ignored):

```bash
git clone https://github.com/pursae/pursae-dev.git
cd pursae-dev
cp docker/.env.example docker/.env   # set WPGO_DB_PASSWORD, WPGO_SITE_URL (https://…), WPGO_SMTP_*, WPGO_MAIL_FROM
make prod-up                         # site on 127.0.0.1:8088; point Caddy or nginx at it
```

Without `make`:
`docker compose --env-file docker/.env -f docker/compose.yaml -f docker/compose.prod.yaml up -d --build`.
`make prod-logs` follows the log, `make prod-down` stops the stack (data is kept). Put a TLS proxy in
front ([reverse proxy and TLS](../operations/reverse-proxy-and-tls.md)), then open the `https://`
address and finish the installer **right away**: until then anyone who reaches the site can run it.
What the production file changes: [Docker](../operations/docker.md#production-changes).

## On your machine

```bash
git clone https://github.com/pursae/pursae-dev.git
cd pursae-dev
make docker-up
```

Without `make`, run the same thing directly:

```bash
docker compose -f docker/compose.yaml up -d --build
```

This is the **development build**: it does not talk to `hub.pursae.com` (no core updates; it looks
for a local Pursae Manager instead), uses the test database password `wpgo` and trusts the demo
plugin signing key. Use it to try Pursae, not to run a site.

The first build downloads Go modules and compiles the binary, so it takes a minute or two. When
it finishes, open **http://localhost:8088** — you land on the installer
([Your first site](first-site.md)).

| Service | What it is | Host port |
|---------|------------|-----------|
| `wpgo` | the Pursae server | `8088` (production: `127.0.0.1:8088`) |
| `db` | PostgreSQL 17 | `127.0.0.1:5432` (production: not published) |

## Another port or address

The site URL is written into the settings at install time, so set it **before** the first run:

```bash
WPGO_PORT=9000 WPGO_SITE_URL=http://localhost:9000 make docker-up
```

To reach the site from another device on your network use your machine's LAN address:

```bash
WPGO_SITE_URL=http://192.168.1.50:8088 make docker-up
```

If you already installed with the wrong URL, change it under Settings › General or start over
(see [Troubleshooting](../operations/troubleshooting.md)).

## Volumes

Compose keeps state in named volumes, so `docker compose down` does not lose anything:

| Volume | Mounted at | Contains |
|--------|------------|----------|
| `pgdata` | `/var/lib/postgresql/data` | the database |
| `bin` | `/data/bin` | the running binary; Dashboard › Updates replaces it |
| `uploads` | `/data/uploads` | media library files |
| `plugins` | `/data/plugins` | installed plugin packages |
| `themes` | `/data/themes` | theme packages and files edited in the Theme File Editor |
| `pagecache` | `/data/wpgo-cache` | Pursae Cache disk copy |
| `languages` | `/data/languages` | downloaded language packs |
| `backups` | `/data/backups` | Tools › Backups archives |
| `mail` | `/data/mail` | `.eml` files of messages sent without SMTP |

## Everyday commands

```bash
make docker-up            # development: build and start in the background
make docker-down          # stop; data is kept
make prod-up              # production: the same with docker/compose.prod.yaml and docker/.env
make prod-down
make prod-logs
docker compose -f docker/compose.yaml logs -f wpgo
docker compose -f docker/compose.yaml restart wpgo
```

The compose file sets `WPGO_TRUST_DEV_KEY=1` so the bundled example plugins install. That is for
development only: `make prod-up` adds `docker/compose.prod.yaml`, which turns it off — see
[Docker](../operations/docker.md#production-changes), the [security](../operations/security.md) page and the
full [configuration reference](../operations/configuration.md).
