---
title: Install the binary
description: Build Pursae from source and run it against your own PostgreSQL.
weight: 30
section: getting-started
---

You need **Go 1.27** and a **PostgreSQL** database (14 or newer; the compose file uses 17) that
the configured user can create tables in.

## Build

```bash
git clone https://github.com/pursae/pursae.git
cd pursae
make build                # writes bin/wpgo
```

`make build` is `CGO_ENABLED=0 go build -o bin/wpgo ./cmd/wpgo`: the result is a static binary
with the admin, the editors and the bundled themes embedded. Copy it anywhere.

## Run

```bash
export DATABASE_URL='postgres://wpgo:wpgo@localhost:5432/wpgo?sslmode=disable'
export WPGO_SITE_URL='https://blog.example.com'   # the public address, without a trailing slash
export WPGO_UPLOADS_DIR=/var/lib/wpgo/uploads
./bin/wpgo serve
```

On start the server runs the database migrations (under an advisory lock, so several instances
can start at once), creates the uploads directory and listens on `WPGO_ADDR` (default `:8080`).
Open the site and the [installer](first-site.md) appears.

The binary has three commands:

| Command | Does |
|---------|------|
| `wpgo serve` | migrate, then serve (the default) |
| `wpgo migrate` | apply pending migrations and exit |
| `wpgo version` | print the version |

## Development mode

During development read templates, CSS and JS from the working tree instead of the embedded copy
so edits show up on refresh:

```bash
make db                                  # starts only PostgreSQL from the compose file
WPGO_WEB_DIR=web go run ./cmd/wpgo serve
```

`make dev` does both. `make test` runs the unit tests and the PostgreSQL integration tests
against the compose database.

## Environment variables

The ones you will set first:

| Variable | Default | Purpose |
|----------|---------|---------|
| `DATABASE_URL` | `postgres://wpgo:wpgo@localhost:5432/wpgo?sslmode=disable` | PostgreSQL connection string |
| `WPGO_ADDR` | `:8080` | listen address |
| `WPGO_SITE_URL` | `http://localhost:8080` | public URL, written to the settings at install |
| `WPGO_UPLOADS_DIR` | `./data/uploads` | media uploads; plugins, themes, the plugin cache and mail go next to it |
| `WPGO_SECRET` | generated | signing secret for sessions and nonces |
| `WPGO_SMTP_HOST` … | — | outgoing mail; without it messages become `.eml` files |

The complete list, including plugin keys and theme directories, is in the
[configuration reference](../operations/configuration.md). A systemd unit and reverse-proxy
examples are on the [reverse proxy and TLS](../operations/reverse-proxy-and-tls.md) page.
