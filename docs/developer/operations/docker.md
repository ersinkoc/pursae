---
title: Docker
description: The image, the compose file, volumes, environment and running behind another compose stack.
weight: 20
section: operations
---

## The image

`docker/Dockerfile` is a two-stage build: `golang:1.27-alpine` compiles a static binary, the
final stage is `alpine:3.22` with `ca-certificates` and `tzdata`, an unprivileged user `wpgo`
(uid 10001) and the data directories `/data/uploads`, `/data/plugins`, `/data/plugin-cache`,
`/data/mail`, `/data/themes`, `/data/wpgo-cache`, `/data/languages`, `/data/bin` and `/data/backups`.
The entrypoint is `wpgo launch` (it installs the binary into `/data/bin` and runs it from there, see
[upgrading](../getting-started/upgrading.md#docker)), the default command `serve`, the exposed port 8080. Example plugin packages from `examples/plugins/dist` are copied to
`/usr/share/wpgo/plugins` if they exist.

```bash
docker build -f docker/Dockerfile -t wpgo:local .
docker run --rm -p 8080:8080 \
  -e DATABASE_URL='postgres://wpgo:wpgo@db:5432/wpgo?sslmode=disable' \
  -e WPGO_SITE_URL=https://blog.example.com \
  -e WPGO_UPLOADS_DIR=/data/uploads \
  -v wpgo-uploads:/data/uploads -v wpgo-plugins:/data/plugins -v wpgo-themes:/data/themes \
  wpgo:local
```

## compose.yaml

`docker/compose.yaml` defines three services:

| Service | Purpose |
|---------|---------|
| `db` | `postgres:17-alpine`, database `wpgo`, user and password `wpgo`, health-checked with `pg_isready`, published on `127.0.0.1:${WPGO_DB_PORT:-5432}` |
| `wpgo` | the site, built from the Dockerfile (`GO_TAGS=devhub`), waits for the database to be healthy, `restart: unless-stopped` |
| `test` | profile `test`: runs `go test ./... -p 1` in a Go container against the same database (`make docker-test`; one package at a time, since the integration tests recreate `wpgo_test`) |

Environment of the `wpgo` service:

```yaml
DATABASE_URL: postgres://wpgo:wpgo@db:5432/wpgo?sslmode=disable
WPGO_SITE_URL: ${WPGO_SITE_URL:-http://localhost:8088}
WPGO_UPLOADS_DIR: /data/uploads
WPGO_TRUST_DEV_KEY: ${WPGO_TRUST_DEV_KEY:-1}   # demo only
WPGO_DEV_HUB_URL: ${WPGO_DEV_HUB_URL:-http://pursae-manager.test:8090}   # devhub builds only
WPGO_TRUST_HUB_KEYS: ${WPGO_TRUST_HUB_KEYS:-1}                           # devhub builds only
WPGO_TRUSTED_PROXIES: ${WPGO_TRUSTED_PROXIES:-}
WPGO_SMTP_HOST: ${WPGO_SMTP_HOST:-}      # outgoing mail; without a host: .eml files in /data/mail
WPGO_SMTP_PORT: ${WPGO_SMTP_PORT:-587}
WPGO_SMTP_USER: ${WPGO_SMTP_USER:-}
WPGO_SMTP_PASS: ${WPGO_SMTP_PASS:-}
WPGO_MAIL_FROM: ${WPGO_MAIL_FROM:-}
```

Volumes: `pgdata`, `bin` (`/data/bin`), `uploads`, `plugins`, `themes`, `pagecache` (`/data/wpgo-cache`),
`languages`, `backups` and `mail` (each under `/data/<name>` unless noted). Add anything else from the
[configuration reference](configuration.md) to the environment, for example `WPGO_PLUGIN_KEYS`.

## Production changes

`compose.yaml` is the development setup (demo database password, PostgreSQL published on
`127.0.0.1`, the demo plugin signing key trusted, a `devhub` build that looks for a local Pursae
Manager instead of `https://hub.pursae.com`). For production `make prod-up` adds the override
`compose.prod.yaml`, which fixes all of that, with the settings from `docker/.env` (copy
`docker/.env.example`; the file is git-ignored). It refuses to start until `WPGO_DB_PASSWORD` and
`WPGO_SITE_URL` are set:

```bash
cp docker/.env.example docker/.env   # set WPGO_DB_PASSWORD, WPGO_SITE_URL, WPGO_SMTP_*, WPGO_MAIL_FROM
make prod-up                         # make prod-logs, make prod-down
# without make:
docker compose --env-file docker/.env -f docker/compose.yaml -f docker/compose.prod.yaml up -d --build
```

It builds without the `devhub` tag (the binary talks only to `https://hub.pursae.com`, so core updates
come from Dashboard › Updates, and `WPGO_DEV_HUB_URL` / `WPGO_TRUST_HUB_KEYS` are removed), takes the
database password from `WPGO_DB_PASSWORD` (used for `POSTGRES_PASSWORD` and the `DATABASE_URL`), does
not publish PostgreSQL, publishes the site on `127.0.0.1:8088` only (for a TLS proxy on the same host;
`WPGO_BIND` changes the address), sets `WPGO_TRUST_DEV_KEY=0`, passes `WPGO_PLUGIN_KEYS` and
`WPGO_TRUSTED_PROXIES` through and restarts both services unless stopped. `POSTGRES_PASSWORD` only applies when the `pgdata` volume is first created; on
an existing database change the password first (`ALTER USER wpgo PASSWORD '...'`).

Also:

- Put a TLS-terminating proxy in front and set `WPGO_SITE_URL` to the `https://` address
  ([reverse proxy and TLS](reverse-proxy-and-tls.md)).
- Finish the installer right after the site goes live: until then anyone who reaches it can run it.
- Set `WPGO_SECRET` explicitly if you run more than one `wpgo` replica.

## Health checks

| Endpoint | Answers 200 when | Use it for |
|----------|------------------|------------|
| `GET /healthz` | the process serves HTTP (the database is not touched) | liveness: Docker `HEALTHCHECK`, a Kubernetes `livenessProbe` |
| `GET /readyz` | the database answers a ping within 2 s and every migration of the binary is applied; `503` otherwise | readiness: load balancers, a Kubernetes `readinessProbe` |

Both are unauthenticated, answer before maintenance mode, Pursae Security and the page cache,
send `Cache-Control: no-store` and are not written to the access log. The image's `HEALTHCHECK` runs
`wpgo healthcheck` (the binary asks `/healthz` on `WPGO_ADDR`; `wpgo healthcheck ready` asks
`/readyz`), so it needs no curl; `compose.prod.yaml` declares the same check.

## Shutdown and timeouts

On SIGTERM the server stops accepting connections and lets requests in flight finish (up to 10 s),
then stops the job runner (the job in hand finishes or goes back to the queue, up to 10 s), drains
the background writes (404 log, counters, pending CDN purges; up to 5 s) and closes the database
pool. Docker's default grace period is 10 s, so `compose.prod.yaml` gives the `wpgo` service
`stop_grace_period: 35s`; set the same (or Kubernetes' `terminationGracePeriodSeconds`) when you run
the image another way.

The HTTP server limits request headers to 10 s, idle keep-alive connections to 120 s, and a request
body that makes no progress for 60 s fails. There is deliberately no overall read or write timeout:
backup downloads and imports, uploads over slow links and the Pursae Headless subscription
streams run for minutes. Put per-request limits in the reverse proxy if you need them. A request
body is limited to 512 MB (PHP's `post_max_size`; the largest upload a screen accepts is a 256 MB
WXR import), except the Backup Manager import, which takes archives of up to 16 GB; a larger body
is answered `413`.

Every response carries an `X-Request-ID` (the proxy's, when it sends a sane one, else a generated
one); the access log line ends with `id=<request id>`.

## Logs and restarts

```bash
docker compose -f docker/compose.yaml logs -f wpgo   # production: make prod-logs
docker compose -f docker/compose.yaml restart wpgo
```

The `wpgo` service has `restart: unless-stopped` (it also brings back a core update that did not
start, see [upgrading](../getting-started/upgrading.md)); `compose.prod.yaml` adds it to `db` too.
If containers still do not come back after the Docker daemon restarts, `docker compose up -d`
(or `make prod-up`) starts them again with their data intact.

## Several instances

Instances that share one database see each other's plugin changes: every process compares the
`plugins` table with what it has loaded every 10 seconds and loads, unloads or reloads the
difference. Plugin packages live in the plugins directory (`WPGO_PLUGINS_DIR`), so every instance
needs the same volume; an instance cannot load a package it does not have on disk.
