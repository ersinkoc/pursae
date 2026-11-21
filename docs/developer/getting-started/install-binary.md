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
make build                # writes bin/pursae (and bin/wpgo, its deprecated alias)
```

`make build` is `CGO_ENABLED=0 go build -o bin/pursae ./cmd/wpgo` (plus a copy named `bin/wpgo`, the deprecated old name): the result is a static binary
with the admin, the editors and the bundled themes embedded. Copy it anywhere.

## Run

```bash
export DATABASE_URL='postgres://wpgo:wpgo@localhost:5432/wpgo?sslmode=disable'
export PURSAE_SITE_URL='https://blog.example.com'   # the public address, without a trailing slash
export PURSAE_UPLOADS_DIR=/var/lib/wpgo/uploads
./bin/pursae serve
```

On start the server runs the database migrations (under an advisory lock, so several instances
can start at once), creates the uploads directory and listens on `PURSAE_ADDR` (default `:8080`).
On a site that is not installed yet it also prints the install link with the setup token
(`http://localhost:8080/wp-admin/install?token=…`): open it and the [installer](first-site.md)
appears. `pursae setup-token` prints a new link; `PURSAE_SETUP_TOKEN` sets the token yourself.

The main commands:

| Command | Does |
|---------|------|
| `pursae serve` | migrate, then serve (the default) |
| `pursae migrate` | apply pending migrations and exit |
| `pursae version` | print the version |
| `pursae setup-token` | print the install link with the setup token (a new token unless `PURSAE_SETUP_TOKEN` is set; the old link stops working) |

## Development mode

During development read templates, CSS and JS from the working tree instead of the embedded copy
so edits show up on refresh:

```bash
make db                                  # starts only PostgreSQL from the compose file
PURSAE_WEB_DIR=web go run ./cmd/wpgo serve
```

`make dev` does both. `make test` runs the unit tests and the PostgreSQL integration tests
against the compose database, one package at a time; they drop and recreate the database
`TEST_DB` names (default `wpgo_test`), so give each checkout running tests at once its own,
e.g. `make test TEST_DB=wpgo_test_feature`.

`make upgrade-test` checks that sites made by older releases survive an upgrade to your checkout
(`tools/upgrade-test.sh`, a few minutes): it builds v0.2.0 and the three newest release tags
(fetch them first: `git fetch --tags`), lets each one install and fill a site over HTTP, then
migrates and serves that site with your code and compares users, posts, pages, terms, comments,
menus, media, options and a Web Builder page. `UPGRADE_TAGS="v0.2.5"` picks the releases; it
creates its own databases (`wpgo_upg_<tag>`, `UPGRADE_DB_PREFIX`) next to the compose one and
keeps a failed run's database, logs and snapshots. CI runs it on every pull request (job
`upgrade`). See [schema changes](upgrading.md#schema-changes) for the migration rules.

## Environment variables

The ones you will set first:

| Variable | Default | Purpose |
|----------|---------|---------|
| `DATABASE_URL` | `postgres://wpgo:wpgo@localhost:5432/wpgo?sslmode=disable` | PostgreSQL connection string |
| `PURSAE_ADDR` | `:8080` | listen address |
| `PURSAE_SITE_URL` | `http://localhost:8080` | public URL, written to the settings at install |
| `PURSAE_UPLOADS_DIR` | `./data/uploads` | media uploads; plugins, themes, the plugin cache and mail go next to it |
| `PURSAE_SECRET` | generated | signing secret for sessions and nonces |
| `PURSAE_SMTP_HOST` … | — | outgoing mail; without it messages become `.eml` files |

The complete list, including plugin keys and theme directories, is in the
[configuration reference](../operations/configuration.md). A systemd unit and reverse-proxy
examples are on the [reverse proxy and TLS](../operations/reverse-proxy-and-tls.md) page.
