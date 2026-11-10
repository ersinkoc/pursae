---
title: Reverse proxy and TLS
description: Putting nginx, Caddy or another proxy in front of Pursae, and what headers it needs.
weight: 40
section: operations
---

Pursae serves plain HTTP. Terminate TLS in a proxy and set `WPGO_SITE_URL` to the public
`https://` address (or change Settings › General after installing).

The server trusts `X-Forwarded-Proto: https` to decide that a request is secure: it then sets
`Strict-Transport-Security` and secure cookies, and generates `https://` links.

Send `X-Forwarded-For` so login throttling, the comment flood check, the Pursae Security IP block list, the
maintenance IP whitelist and the logs see the visitor's address
instead of the proxy's. The header is believed only when the request comes from a **trusted proxy**: by
default loopback and the private ranges (`127.0.0.0/8`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `::1`,
`fc00::/7`), which is where a proxy next to the server normally sits. A client that reaches the server directly
from the internet is not trusted, so it cannot pick its own address. If your proxy has a public address, list it
in `WPGO_TRUSTED_PROXIES` (comma separated IPs and CIDRs); `none` trusts no proxy. Without a trusted proxy in
front, every visitor would otherwise share the proxy's address, and five failed logins from one visitor would lock
everybody out for 15 minutes.

The private ranges are trusted by default because a proxy in Docker (on the host or in another container)
reaches the server from a bridge address. That means **any** machine on those networks that can reach the
server's port directly could pick its own address: publish the port only where the proxy can reach it
(`docker/compose.prod.yaml` binds `127.0.0.1:8088`; `WPGO_BIND` changes the address) or set
`WPGO_TRUSTED_PROXIES` to exactly your proxy's address. With `WPGO_SITE_URL` on `https://` and no
`WPGO_TRUSTED_PROXIES`, the server logs a reminder at start-up. Sub-requests of `/batch/v1` cannot set
`X-Forwarded-*`, `X-Real-IP` or `Forwarded` themselves; they carry the outer request's headers.

## Caddy

```text
blog.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy sets the forwarded headers and obtains certificates on its own.

## nginx

```nginx
server {
    listen 443 ssl http2;
    server_name blog.example.com;
    ssl_certificate     /etc/letsencrypt/live/blog.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/blog.example.com/privkey.pem;
    client_max_body_size 64m;          # media uploads

    location / {
        proxy_pass         http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Real-IP         $remote_addr;
    }
}
server {
    listen 80;
    server_name blog.example.com;
    return 301 https://$host$request_uri;
}
```

Static files (`/wp-includes/`, `/wp-admin/css/`, theme and plugin assets) are served by the Go
binary with long cache headers, so no `root` or `try_files` is needed. nginx may cache them in a
`proxy_cache`; Pursae Cache can emit `X-Accel-Expires` for whole pages too ([caching](caching.md)).

### Live GraphQL subscriptions (Pursae Headless)

Pursae Headless delivers GraphQL subscriptions as server-sent events and over a WebSocket on
`/wp-json/headlessgo/v1/graphql`. Server-sent events need nothing more (the response carries
`X-Accel-Buffering: no`, so nginx does not buffer it). A WebSocket needs the upgrade headers passed on:

```nginx
    location = /wp-json/headlessgo/v1/graphql {
        proxy_pass         http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header   Upgrade           $http_upgrade;
        proxy_set_header   Connection        "upgrade";
        proxy_set_header   Host              $host;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_read_timeout 120s;           # the server pings every 20 s
    }
```

Caddy passes WebSocket upgrades on its own; Apache needs `mod_proxy_wstunnel` (or
`ProxyPass / http://127.0.0.1:8080/ upgrade=websocket` with Apache 2.4.47 and later). Keep the `Host` header:
a browser's WebSocket is accepted only from the site's own origin or a CORS origin of the Headless settings.

## Apache

```apache
<VirtualHost *:443>
    ServerName blog.example.com
    SSLEngine on
    ProxyPreserveHost On
    RequestHeader set X-Forwarded-Proto "https"
    ProxyPass        / http://127.0.0.1:8080/
    ProxyPassReverse / http://127.0.0.1:8080/
</VirtualHost>
```

## systemd unit

```ini
[Unit]
Description=Pursae
After=network.target postgresql.service

[Service]
User=wpgo
EnvironmentFile=/etc/wpgo.env
ExecStart=/usr/local/bin/wpgo serve
Restart=always

[Install]
WantedBy=multi-user.target
```

`/etc/wpgo.env` holds `DATABASE_URL`, `WPGO_SITE_URL`, `WPGO_UPLOADS_DIR`, `WPGO_SECRET` and the
mail settings ([configuration](configuration.md)).

## Several instances

Run as many `wpgo` processes as you like against one database with a shared uploads directory
(NFS or an object-storage mount): the scheduler claims jobs with `FOR UPDATE SKIP LOCKED`, so
events run once, and migrations are serialised with an advisory lock. Give every instance the
same `WPGO_SECRET`. Settings are cached in memory per process; a change saved on one instance
reaches the others through PostgreSQL `LISTEN`/`NOTIFY` within moments (each instance holds one
extra database connection for it, and reloads every minute in case a notification was lost). Side
effects of saving a setting, such as starting a plugin or reconfiguring the page cache, run only on
the instance that saved it; restart the others after switching plugins on or off. Pursae Headless's GraphQL subscriptions only see the changes made through the instance a
client is connected to; use its webhooks to follow changes across instances.
