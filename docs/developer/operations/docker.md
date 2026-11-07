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
`/data/mail` and `/data/themes`. The entrypoint is `wpgo`, the default command `serve`, the
exposed port 8080. Example plugin packages from `examples/plugins/dist` are copied to
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
| `db` | `postgres:17-alpine`, database `wpgo`, user and password `wpgo`, health-checked with `pg_isready` |
| `wpgo` | the site, built from the Dockerfile, waits for the database to be healthy |
| `test` | profile `test`: runs `go test ./...` in a Go container against the same database (`make docker-test`) |

Environment of the `wpgo` service:

```yaml
DATABASE_URL: postgres://wpgo:wpgo@db:5432/wpgo?sslmode=disable
WPGO_SITE_URL: ${WPGO_SITE_URL:-http://localhost:8088}
WPGO_UPLOADS_DIR: /data/uploads
WPGO_TRUST_DEV_KEY: ${WPGO_TRUST_DEV_KEY:-1}   # demo only
```

Add anything from the [configuration reference](configuration.md) there, for example the SMTP
settings or `WPGO_PLUGIN_KEYS`.

## Production changes

`compose.yaml` is the development setup (demo database password, PostgreSQL published on the host, the
demo plugin signing key trusted). For production add the override `compose.prod.yaml`, which fixes all of
that and refuses to start until the secrets are set:

```bash
WPGO_DB_PASSWORD=<strong password> WPGO_SITE_URL=https://example.com \
  docker compose -f docker/compose.yaml -f docker/compose.prod.yaml up -d --build
```

It takes the database password from `WPGO_DB_PASSWORD` (used for `POSTGRES_PASSWORD` and the
`DATABASE_URL`), does not publish PostgreSQL, publishes the site on `127.0.0.1:8088` only (for a TLS proxy
on the same host; `WPGO_BIND` changes the address), sets `WPGO_TRUST_DEV_KEY=0` and passes
`WPGO_PLUGIN_KEYS` and `WPGO_TRUSTED_PROXIES` through. `POSTGRES_PASSWORD` only applies when the `pgdata` volume is first created; on
an existing database change the password first (`ALTER USER wpgo PASSWORD '...'`).

Also:

- Put a TLS-terminating proxy in front and set `WPGO_SITE_URL` to the `https://` address
  ([reverse proxy and TLS](reverse-proxy-and-tls.md)).
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
pool. Docker's default grace period is 10 s; give the service `stop_grace_period: 30s` if long jobs
should get the full time.

The HTTP server limits request headers to 10 s, idle keep-alive connections to 120 s, and a request
body that makes no progress for 60 s fails. There is deliberately no overall read or write timeout:
backup downloads and imports, uploads over slow links and the Pursae Headless subscription
streams run for minutes. Put per-request limits in the reverse proxy if you need them.

Every response carries an `X-Request-ID` (the proxy's, when it sends a sane one, else a generated
one); the access log line ends with `id=<request id>`.

## Logs and restarts

```bash
docker compose -f docker/compose.yaml logs -f wpgo
docker compose -f docker/compose.yaml restart wpgo
```

Containers do not always come back after the Docker daemon restarts; `docker compose up -d`
starts them again with their data intact. Add `restart: unless-stopped` to both services if you
want that automatically.
