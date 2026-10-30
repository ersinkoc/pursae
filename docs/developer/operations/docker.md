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

- Change the database password in `compose.yaml` and the `DATABASE_URL`.
- Remove the `ports:` entry of the `db` service so PostgreSQL is not published.
- Set `WPGO_TRUST_DEV_KEY=0` and list your own keys in `WPGO_PLUGIN_KEYS`.
- Put a TLS-terminating proxy in front and set `WPGO_SITE_URL` to the `https://` address
  ([reverse proxy and TLS](reverse-proxy-and-tls.md)).
- Set `WPGO_SECRET` explicitly if you run more than one `wpgo` replica.

## Logs and restarts

```bash
docker compose -f docker/compose.yaml logs -f wpgo
docker compose -f docker/compose.yaml restart wpgo
```

Containers do not always come back after the Docker daemon restarts; `docker compose up -d`
starts them again with their data intact. Add `restart: unless-stopped` to both services if you
want that automatically.
