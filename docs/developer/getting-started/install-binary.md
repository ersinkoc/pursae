---
title: Install the binary
description: Install a prebuilt Pursae release (or build one from source) and run it as a systemd service against your own PostgreSQL.
weight: 30
section: getting-started
---

Pursae is one static binary with the admin, the editors and the bundled themes embedded. You need
a **PostgreSQL 16 or 17** database that the configured user can create tables in (CI tests both;
the compose file uses 17). Releases are built for Linux (amd64, arm64), macOS (Apple silicon,
Intel) and Windows (amd64).

## Install a release

Each [GitHub release](https://github.com/pursae/pursae/releases) has, per platform, the bare
binary `pursae-<os>-<arch>` (`.exe` on Windows; the file sites update from), an archive
`pursae-<os>-<arch>.tar.gz` (`.zip` on Windows) with the binary as `pursae`, the license files
(`LICENSE`, `NOTICE`, `LICENSES/`) and a short README, and `SHA256SUMS` covering all of them.

The steps below set up a Linux server with systemd; on another system run the binary the same way
under your own supervisor.

### 1. Download and verify

```bash
VERSION=0.5.0                          # the release you install
ARCH=amd64                             # or arm64
cd "$(mktemp -d)"                      # a private working directory, not the shared /tmp
curl -fLO "https://github.com/pursae/pursae/releases/download/v$VERSION/pursae-linux-$ARCH.tar.gz"
curl -fLO "https://github.com/pursae/pursae/releases/download/v$VERSION/SHA256SUMS"
sha256sum --check --ignore-missing SHA256SUMS   # must print "pursae-linux-…tar.gz: OK"
tar -xzf "pursae-linux-$ARCH.tar.gz"
# the extracted binary is the release's bare pursae-linux-$ARCH asset, listed in SHA256SUMS too
echo "$(grep " pursae-linux-$ARCH\$" SHA256SUMS | cut -d" " -f1)  pursae-linux-$ARCH/pursae" | sha256sum --check
./pursae-linux-$ARCH/pursae version
```

(On macOS: `shasum -a 256 --check` in place of `sha256sum --check`.) Run steps 1 and 2 in the same
shell: step 2 installs from this directory.

### 2. User and directories

The service runs as its own user. The binary lives in a directory **that user owns**, so
**Dashboard › Updates** can replace it in place: a core update downloads the release's binary,
checks its signature, swaps it in (keeping the old one as `pursae.prev`) and restarts into it,
without root.

```bash
sudo useradd --system --home-dir /var/lib/pursae --shell /usr/sbin/nologin pursae
sudo install -d -o pursae -g pursae -m 0755 /opt/pursae /opt/pursae/bin
sudo install -d -o pursae -g pursae -m 0750 /var/lib/pursae
sudo install -o pursae -g pursae -m 0755 "pursae-linux-$ARCH/pursae" /opt/pursae/bin/pursae
sudo ln -sf /opt/pursae/bin/pursae /usr/local/bin/pursae   # for the CLI commands
sudo install -d -o root -g root -m 0755 /usr/share/doc/pursae
sudo cp -r "pursae-linux-$ARCH/LICENSE" "pursae-linux-$ARCH/NOTICE" "pursae-linux-$ARCH/LICENSES" /usr/share/doc/pursae/
```

`/var/lib/pursae` holds the uploads and, next to them, plugins, themes, languages, backups and the
caches.

### 3. Database

```bash
sudo -u postgres createuser --pwprompt pursae
sudo -u postgres createdb --owner pursae pursae
```

Pursae creates its tables on the first start. It also enables the `pg_trgm` extension for faster
title search when the server has it (the PostgreSQL contrib package; search works without it).

### 4. Environment file

`/etc/pursae/pursae.env`, readable by the service user only:

```bash
sudo install -d -m 0755 /etc/pursae
sudo install -m 0640 -o root -g pursae /dev/null /etc/pursae/pursae.env
sudoedit /etc/pursae/pursae.env
```

```ini
DATABASE_URL=postgres://pursae:DB_PASSWORD@localhost:5432/pursae?sslmode=disable
WPGO_SITE_URL=https://blog.example.com
WPGO_ADDR=127.0.0.1:8080
WPGO_UPLOADS_DIR=/var/lib/pursae/uploads
# openssl rand -hex 32
WPGO_SECRET=…
# Outgoing mail (password resets, notifications); without it messages become .eml files.
# WPGO_SMTP_HOST=smtp.example.com
```

`WPGO_SITE_URL` is the public address without a trailing slash. Keep `WPGO_ADDR` on `127.0.0.1`
behind a [reverse proxy that terminates TLS](../operations/reverse-proxy-and-tls.md). Every
setting is in the [configuration reference](../operations/configuration.md).

### 5. systemd unit and first start

The repository ships a hardened unit,
[`deploy/systemd/pursae.service`](https://github.com/pursae/pursae/blob/main/deploy/systemd/pursae.service):
it runs `/opt/pursae/bin/pursae serve` as `pursae` with the environment file above, restarts it
when it exits, and lets it write only to `/opt/pursae/bin` and `/var/lib/pursae`.

```bash
sudo curl -fL -o /etc/systemd/system/pursae.service \
  "https://raw.githubusercontent.com/pursae/pursae/v$VERSION/deploy/systemd/pursae.service"
sudo systemctl daemon-reload
sudo systemctl enable --now pursae
systemctl status pursae
curl -fsS http://127.0.0.1:8080/healthz
```

Change it with `sudo systemctl edit pursae` (a drop-in that survives a new copy of the unit), for
example other paths or `AmbientCapabilities=CAP_NET_BIND_SERVICE` to listen on port 443 directly.

### 6. Install the site

On a site that is not installed yet the installer is locked by a **setup token**: the server
prints the install link in its log, or print a new one (the old link stops working):

```bash
journalctl -u pursae | grep wp-admin/install
sudo systemd-run --pipe --wait --quiet -p User=pursae -p EnvironmentFile=/etc/pursae/pursae.env \
  /opt/pursae/bin/pursae setup-token
```

`systemd-run` reads the environment file the way the service does (a shell would not: `&` in a
`DATABASE_URL` with several query parameters, or `$` or a space in a password, would break it).

Open the link (`https://blog.example.com/wp-admin/install?token=…`) and the
[installer](first-site.md) appears. `WPGO_SETUP_TOKEN` in the environment file sets the token
yourself instead.

### Updating

**Dashboard › Updates › Update to version X** replaces `/opt/pursae/bin/pursae` and restarts the
service in place; migrations run on start. If the new version does not start, the next start puts
the previous binary back. To update by hand, verify the new release as in step 1, then
`sudo install -o pursae -g pursae -m 0755 pursae-linux-$ARCH/pursae /opt/pursae/bin/pursae` and
`sudo systemctl restart pursae`. See [upgrading](upgrading.md).

### Uninstall

```bash
sudo systemctl disable --now pursae
sudo rm /etc/systemd/system/pursae.service /usr/local/bin/pursae
sudo rm -rf /etc/systemd/system/pursae.service.d   # drop-ins from systemctl edit
sudo systemctl daemon-reload
sudo rm -r /opt/pursae /usr/share/doc/pursae
```

That leaves the site's data. Back it up first if you may want it again ([backups](../operations/backups.md):
`pg_dump` plus a copy of `/var/lib/pursae`, or the Backup Manager plugin), then remove it:

```bash
sudo rm -r /var/lib/pursae /etc/pursae
sudo -u postgres dropdb pursae
sudo -u postgres dropuser pursae
sudo userdel pursae
```

## Build from source

You need **Go 1.27** and git.

```bash
git clone https://github.com/pursae/pursae.git
cd pursae
make build                # writes bin/pursae (and bin/wpgo, the same binary under its older name)
```

`make build` is `CGO_ENABLED=0 go build -buildmode=exe -tags nodynamic -o bin/pursae ./cmd/wpgo`: a
static binary that runs on any Linux. Release builds add `-trimpath -ldflags "-s -w -X
wpgo/internal/server.Version=x.y.z"` (`tools/release.sh`). Install it as above, or run it directly:

```bash
export DATABASE_URL='postgres://wpgo:wpgo@localhost:5432/wpgo?sslmode=disable'
export WPGO_SITE_URL='https://blog.example.com'   # the public address, without a trailing slash
export WPGO_UPLOADS_DIR=/var/lib/wpgo/uploads
./bin/pursae serve
```

On start the server runs the database migrations (under an advisory lock, so several instances
can start at once), creates the uploads directory and listens on `WPGO_ADDR` (default `:8080`).
On a site that is not installed yet it also prints the install link with the setup token
(`http://localhost:8080/wp-admin/install?token=…`): open it and the [installer](first-site.md)
appears. `pursae setup-token` prints a new link; `WPGO_SETUP_TOKEN` sets the token yourself.

The main commands (`pursae help` lists them all):

| Command | Does |
|---------|------|
| `pursae serve` | migrate, then serve (the default) |
| `pursae migrate` | apply pending migrations and exit |
| `pursae version` | print the version |
| `pursae healthcheck` | `GET /healthz` on `WPGO_ADDR`; exit status 0 when the server answers |
| `pursae setup-token` | print the install link with the setup token (a new token unless `WPGO_SETUP_TOKEN` is set; the old link stops working) |

## Development mode

During development read templates, CSS and JS from the working tree instead of the embedded copy
so edits show up on refresh:

```bash
make db                                  # starts only PostgreSQL from the compose file
WPGO_WEB_DIR=web go run ./cmd/wpgo serve
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
| `WPGO_ADDR` | `:8080` | listen address |
| `WPGO_SITE_URL` | `http://localhost:8080` | public URL, written to the settings at install |
| `WPGO_UPLOADS_DIR` | `./data/uploads` | media uploads; plugins, themes, the plugin cache and mail go next to it |
| `WPGO_SECRET` | generated | signing secret for sessions and nonces |
| `WPGO_SMTP_HOST` … | — | outgoing mail; without it messages become `.eml` files |

The complete list, including plugin keys and theme directories, is in the
[configuration reference](../operations/configuration.md). Reverse-proxy examples are on the
[reverse proxy and TLS](../operations/reverse-proxy-and-tls.md) page.
