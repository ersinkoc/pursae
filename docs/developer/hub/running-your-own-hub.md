---
title: Running your own Hub
description: Deploy wpgo-hub for an organisation, connect sites to it and operate the panel.
weight: 40
section: hub
---

Run a private Hub when you distribute internal plugins and themes, want an internal update
channel, or do not want sites to contact the public Hub at all.

## Deploy

`wpgo-hub` is a single binary with a PostgreSQL database and a data directory for packages and
keys. In the WordPress Go compose stack it is published on host port **8090**.

| Variable | Default | Meaning |
|----------|---------|---------|
| `WPGOHUB_ADDR` | `:8080` | listen address |
| `WPGOHUB_PUBLIC_URL` | `http://localhost:8090` | used in `download_url` and other links; set it to the public `https://` address |
| `DATABASE_URL` | `postgres://wpgohub:wpgohub@localhost:5432/wpgohub?sslmode=disable` | PostgreSQL |
| `WPGOHUB_DATA_DIR` | `./data` | packages and keys |
| `WPGOHUB_SECRET` | random | session signing; set it for restarts and replicas |
| `WPGOHUB_TEST_DB` | — | integration tests run only when set |

Put it behind TLS like a site ([reverse proxy and TLS](../operations/reverse-proxy-and-tls.md)).
`GET /healthz` returns `ok` for load balancers.

## First run

Open `/setup` to create the operator account; the panel is unusable before. Then `/login`
(session cookie, argon2id password hash, login throttling) and:

| Screen | Does |
|--------|------|
| `/admin` | overview: site count, plugins and themes, last pings, latest core release |
| `/admin/sites` | hosts, versions, first/last seen, `local` flag; search; delete |
| `/admin/plugins`, `/admin/themes` | upload a zip, publish/unpublish, versions, edit the listing, delete |
| `/admin/releases` | core releases: version, download and notes URLs, release notes; publish |
| `/admin/keys` | signing keys; generate a new one |
| `/admin/settings` | hub name, public URL, rate limits |

Generate a signing key under `/admin/keys` before publishing the first plugin: packages are
re-signed with it, and sites add it to their trusted keys from `GET /api/v1/index`.

## Connect sites

On each site set `WPGO_HUB_URL=https://hub.internal.example.com` (or the `wpgo_hub_url` option).
The site then reads your catalog on Plugins › Add Plugin and Appearance › Add Theme, trusts your
keys, shows your releases on Dashboard › Updates and pings your Hub once a day. An empty
`WPGO_HUB_URL` disconnects a site from every Hub.

## Publishing a core release

`/admin/releases` stores the version, a download URL, a notes URL and Markdown notes; `core` in
`GET /api/v1/index` and the `core` field of the ping response carry it to sites, which show it as
an update notice. The Hub does not host the binary itself unless you point `download_url` at a
file it serves.

## API

The public API is exactly the one the public Hub exposes — `GET /api/v1/index`, `/plugins`,
`/plugins/{slug}`, `/plugins/{slug}/download[/{version}]`, `/plugins/{slug}/icon`, the same for
`/themes` (with `/screenshot`), `POST /api/v1/sites/ping`, `GET /api/v1/sites/count` — so tools
written for one work with the other. Responses are JSON with `Cache-Control: public, max-age=300`
except the ping (`no-store`) and downloads.
