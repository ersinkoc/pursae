---
title: Reverse proxy and TLS
description: Putting nginx, Caddy or another proxy in front of WordPress Go, and what headers it needs.
weight: 40
section: operations
---

WordPress Go serves plain HTTP. Terminate TLS in a proxy and set `WPGO_SITE_URL` to the public
`https://` address (or change Settings › General after installing).

The server trusts `X-Forwarded-Proto: https` to decide that a request is secure: it then sets
`Strict-Transport-Security` and secure cookies, and generates `https://` links.

Send `X-Forwarded-For` so login throttling, the comment flood check and the logs see the visitor's address
instead of the proxy's. The header is believed only when the request comes from a **trusted proxy**: by
default loopback and the private ranges (`127.0.0.0/8`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `::1`,
`fc00::/7`), which is where a proxy next to the server normally sits. A client that reaches the server directly
from the internet is not trusted, so it cannot pick its own address. If your proxy has a public address, list it
in `WPGO_TRUSTED_PROXIES` (comma separated IPs and CIDRs); `none` trusts no proxy. Without a trusted proxy in
front, every visitor would otherwise share the proxy's address, and five failed logins from one visitor would lock
everybody out for 15 minutes.

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
`proxy_cache`; WPGo Cache can emit `X-Accel-Expires` for whole pages too ([caching](caching.md)).

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
Description=WordPress Go
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
same `WPGO_SECRET`.
