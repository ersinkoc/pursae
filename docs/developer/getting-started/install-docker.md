---
title: Install with Docker
description: Run WordPress Go and PostgreSQL with docker compose in a couple of minutes.
weight: 20
section: getting-started
---

You need **Docker** (Docker Desktop on macOS/Windows, Docker Engine with Compose v2 on Linux) and
**git**.

## Start

```bash
git clone https://github.com/boracomet/wordpress-go.git
cd wordpress-go
make docker-up
```

Without `make`, run the same thing directly:

```bash
docker compose -f docker/compose.yaml up -d --build
```

The first build downloads Go modules and compiles the binary, so it takes a minute or two. When
it finishes, open **http://localhost:8088** — you land on the installer
([Your first site](first-site.md)).

| Service | What it is | Host port |
|---------|------------|-----------|
| `wpgo` | the WordPress Go server | `8088` |
| `db` | PostgreSQL 17 | `5432` |

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
| `uploads` | `/data/uploads` | media library files |
| `plugins` | `/data/plugins` | installed plugin packages |
| `themes` | `/data/themes` | theme packages and files edited in the Theme File Editor |

## Everyday commands

```bash
make docker-up            # build and start in the background
make docker-down          # stop; data is kept
docker compose -f docker/compose.yaml logs -f wpgo
docker compose -f docker/compose.yaml restart wpgo
```

The compose file sets `WPGO_TRUST_DEV_KEY=1` so the bundled example plugins install. Turn that
off in production — see the [security](../operations/security.md) page and the full
[configuration reference](../operations/configuration.md).
