---
title: Benchmarks
description: Pursae against WordPress on the same content, theme and limits — throughput, TTFB, DB queries, memory, cold start, size and Lighthouse — with every config, how to reproduce it, and where Pursae loses.
weight: 60
section: getting-started
---

Pursae and WordPress, measured on the same machine, the same content, the same theme and the same
container limits. Every config, version and raw result is in the repository under `bench/`; one
command reruns all of it. Where WordPress wins, the tables say so.

## Results (2026-11-09)

Apple M2 Pro (32 GB), Docker Desktop 29.7.2 on macOS with 12 CPUs / 7.7 GB for Docker. WordPress 7.1.4,
PHP 8.3.36, MariaDB 11.4, nginx 1.27, Redis 7, WooCommerce 11.2.2, Yoast SEO 28.8, Polylang 3.9.2,
ACF 6.8.11, Elementor 4.3.6, Contact Form 7 6.2.4.1, Redis Object Cache 3.0.0, WP Super Cache 3.1.5;
Pursae 0.1.9 (commit `cb856438`), PostgreSQL 17. k6 1.3.0, 10 virtual users, median of three 20 s runs.
Raw data: [`bench/results/2026-11-09/`](https://github.com/pursae/pursae/tree/main/bench/results/2026-11-09).

### Headlines

- **Uncached pages: about 8× WordPress' throughput.** Home 397 vs 51 req/s, single post 394 vs 58,
  REST posts list 1,046 vs 89, admin dashboard 627 vs 72 (stock against stock).
- **With the usual plugin stack: about 13× (single post 232 vs 19 req/s)** and a product page at
  194 vs 16 req/s. The six WordPress plugins cut its throughput to about a third and take a page
  from 32 to 139 queries; the six Pursae built-ins cost 30–40 % (13 → 16 queries on the home page).
- **Cached pages: a tie.** WP Super Cache served by nginx and Pursae Cache both answer in about 0.1 ms
  and both run into the load generator (28–35 k req/s).
- **Memory: 95 MiB idle for the whole Pursae stack against 233 MiB** (stock) and 330 MiB (with plugins);
  peak under load 184–202 MiB against 276–489 MiB.
- **Lighthouse (home, mobile): 100 against 96 stock, 99 tuned and 67 with the plugin stack.**

### Where Pursae does not win

- **Search is not page-cached** in Pursae (`/?s=` answers `Cache-Control: no-cache`): 105–119 req/s, while
  WP Super Cache does cache search results and serves 5,724 req/s. Uncached, Pursae search is still
  2.5× stock WordPress (119 vs 48), but it is its slowest page (93 ms TTFB at load, 26 ms alone).
- **Cached pages are no faster** than a well-set-up WordPress page cache (above).
- **Cold start is the same**: about 3 s for the whole stack on both sides, about 1 s for the app
  alone. Most of it is Docker and the database, not PHP or Go.
- **The database is bigger**: 30 MB and 92 tables against 16 MB and 59 tables for the same content
  (Pursae's migrations create the tables of the built-in plugins, such as the Commerce and Pixel ones,
  whether or not the plugin is on).
- **Turning Pursae Cache on slowed uncached routes a little** in this run (REST list 1,046 → 885 req/s,
  admin post list p50 27 → 70 ms): pages that are never cached pay for the cache lookup and tagging.
- **WordPress "tuned" was slower than stock on uncached requests** here (REST list 64 vs 89 req/s,
  admin 43 vs 72): the Redis round trips and a pool of 12 PHP workers on 2 CPUs cost more than they saved
  for a 1000-post site. We kept it as configured rather than tune it after seeing the results; its
  cached pages are what that setup is for.

### Full tables

### Throughput (req/s, higher is better)

| Endpoint | WP stock | WP tuned | WP + 6 plugins | Pursae, no cache | Pursae, cache | Pursae + 6 built-ins |
|---|---:|---:|---:|---:|---:|---:|
| Home | 51 | 31,414 | 19 | 397 | 34,128 | 273 |
| Single post | 58 | 30,248 | 19 | 394 | 34,698 | 232 |
| Category archive | 50 | 28,293 | 18 | 395 | 35,168 | 312 |
| Product | — | — | 16 | — | — | 194 |
| Search | 48 | 5,724 | 18 | 119 | 105 | 116 |
| REST posts list | 89 | 64 | 18 | 1,046 | 885 | 349 |
| Admin dashboard (logged in) | 72 | 43 | 19 | 627 | 478 | 510 |
| Admin post list (logged in) | 52 | 36 | 14 | 210 | 178 | 203 |

### TTFB p50 under load (ms)

| Endpoint | WP stock | WP tuned | WP + 6 plugins | Pursae, no cache | Pursae, cache | Pursae + 6 built-ins |
|---|---:|---:|---:|---:|---:|---:|
| Home | 195.8 | 0.1 | 505.6 | 24.2 | 0.1 | 34.3 |
| Single post | 183.6 | 0.1 | 500.9 | 15.7 | 0.1 | 27.8 |
| Category archive | 197.5 | 0.1 | 511.7 | 24.4 | 0.1 | 31.0 |
| Product | — | — | 592.7 | — | — | 60.7 |
| Search | 202.1 | 0.8 | 527.7 | 93.3 | 96.3 | 92.4 |
| REST posts list | 106.0 | 183.9 | 522.7 | 8.4 | 9.0 | 14.9 |
| Admin dashboard (logged in) | 107.9 | 207.3 | 513.5 | 8.5 | 10.2 | 11.1 |
| Admin post list (logged in) | 130.2 | 293.6 | 709.8 | 27.3 | 69.7 | 34.2 |

### Latency p95 under load (ms)

| Endpoint | WP stock | WP tuned | WP + 6 plugins | Pursae, no cache | Pursae, cache | Pursae + 6 built-ins |
|---|---:|---:|---:|---:|---:|---:|
| Home | 214.0 | 0.4 | 585.9 | 35.4 | 0.5 | 54.4 |
| Single post | 198.3 | 0.4 | 574.8 | 64.8 | 0.5 | 79.1 |
| Category archive | 263.6 | 0.4 | 589.2 | 34.7 | 0.5 | 44.1 |
| Product | — | — | 669.6 | — | — | 85.7 |
| Search | 260.5 | 1.8 | 596.8 | 107.2 | 179.3 | 157.7 |
| REST posts list | 160.6 | 196.0 | 589.1 | 17.8 | 27.9 | 71.0 |
| Admin dashboard (logged in) | 176.5 | 291.0 | 579.3 | 56.0 | 64.5 | 56.8 |
| Admin post list (logged in) | 204.1 | 306.3 | 786.2 | 84.7 | 96.0 | 84.1 |

### Latency p99 under load (ms)

| Endpoint | WP stock | WP tuned | WP + 6 plugins | Pursae, no cache | Pursae, cache | Pursae + 6 built-ins |
|---|---:|---:|---:|---:|---:|---:|
| Home | 268.0 | 0.7 | 594.8 | 50.2 | 1.1 | 89.2 |
| Single post | 208.8 | 0.7 | 582.2 | 81.5 | 1.0 | 104.5 |
| Category archive | 286.4 | 0.7 | 619.9 | 43.0 | 1.1 | 57.1 |
| Product | — | — | 681.3 | — | — | 94.6 |
| Search | 271.6 | 39.5 | 603.7 | 166.9 | 200.2 | 193.0 |
| REST posts list | 167.0 | 200.0 | 623.9 | 27.1 | 37.7 | 76.8 |
| Admin dashboard (logged in) | 180.7 | 298.5 | 590.8 | 62.8 | 75.9 | 66.8 |
| Admin post list (logged in) | 227.4 | 378.5 | 799.1 | 91.1 | 171.5 | 92.1 |

### First request after a restart (cold): single-request TTFB · DB queries

| Endpoint | WP stock | WP tuned | WP + 6 plugins | Pursae, no cache | Pursae, cache | Pursae + 6 built-ins |
|---|---:|---:|---:|---:|---:|---:|
| Home | 49.1 ms · 32 q | 2.5 ms · 0 q | 125.5 ms · 139 q | 13.6 ms · 13 q | 2.5 ms · 0 q | 17.1 ms · 16 q |
| Single post | 85.1 ms · 41 q | 57.0 ms · 12 q | 120.7 ms · 156 q | 9.4 ms · 19 q | 13.6 ms · 19 q | 14.0 ms · 37 q |
| Category archive | 47.8 ms · 34 q | 59.0 ms · 12 q | 125.9 ms · 145 q | 10.7 ms · 15 q | 13.3 ms · 15 q | 17.1 ms · 24 q |
| Product | — | — | 147.7 ms · 177 q | — | — | 20.0 ms · 55 q |
| Search | 85.4 ms · 37 q | 71.9 ms · 8 q | 133.1 ms · 146 q | 30.1 ms · 14 q | 34.9 ms · 14 q | 38.5 ms · 17 q |
| REST posts list | 33.6 ms · 42 q | 36.0 ms · 28 q | 172.2 ms · 205 q | 7.0 ms · 7 q | 6.0 ms · 7 q | 9.2 ms · 37 q |
| Admin dashboard (logged in) | 58.6 ms · 47 q | 58.9 ms · 8 q | 242.5 ms · 213 q | 13.0 ms · 21 q | 13.0 ms · 21 q | 15.1 ms · 25 q |
| Admin post list (logged in) | 31.4 ms · 48 q | 34.2 ms · 10 q | 185.3 ms · 205 q | 17.4 ms · 20 q | 13.7 ms · 20 q | 16.4 ms · 25 q |

### Second request (warm): single-request TTFB · DB queries

| Endpoint | WP stock | WP tuned | WP + 6 plugins | Pursae, no cache | Pursae, cache | Pursae + 6 built-ins |
|---|---:|---:|---:|---:|---:|---:|
| Home | 49.0 ms · 32 q | 2.1 ms · 0 q | 125.8 ms · 135 q | 9.2 ms · 13 q | 2.2 ms · 0 q | 16.6 ms · 16 q |
| Single post | 72.1 ms · 41 q | 2.2 ms · 0 q | 118.5 ms · 151 q | 8.7 ms · 19 q | 2.4 ms · 0 q | 13.3 ms · 37 q |
| Category archive | 46.1 ms · 34 q | 2.1 ms · 0 q | 120.0 ms · 140 q | 12.4 ms · 15 q | 2.2 ms · 0 q | 13.7 ms · 24 q |
| Product | — | — | 138.5 ms · 172 q | — | — | 15.3 ms · 55 q |
| Search | 64.1 ms · 37 q | 4.1 ms · 0 q | 128.5 ms · 146 q | 26.1 ms · 14 q | 25.8 ms · 14 q | 34.7 ms · 17 q |
| REST posts list | 40.8 ms · 42 q | 31.5 ms · 6 q | 141.6 ms · 155 q | 5.3 ms · 7 q | 6.4 ms · 7 q | 10.7 ms · 37 q |
| Admin dashboard (logged in) | 25.4 ms · 47 q | 29.9 ms · 8 q | 140.2 ms · 213 q | 7.0 ms · 20 q | 7.2 ms · 20 q | 9.6 ms · 24 q |
| Admin post list (logged in) | 27.3 ms · 48 q | 29.8 ms · 10 q | 170.1 ms · 205 q | 11.3 ms · 20 q | 11.3 ms · 20 q | 13.5 ms · 25 q |

### Resources

| Metric | WP stock | WP tuned | WP + 6 plugins | Pursae, no cache | Pursae, cache | Pursae + 6 built-ins |
|---|---:|---:|---:|---:|---:|---:|
| Memory idle (MiB, all containers) | 233 | 338 | 330 | 95 | 75 | 79 |
| Memory peak under load (MiB) | 276 | 489 | 428 | 202 | 184 | 184 |
| Cold start, whole stack (s) | 3.16 | 2.90 | 3.94 | 3.05 | 3.14 | 3.05 |
| Cold start, app only (s) | 1.08 | 0.79 | 1.79 | 1.03 | 0.98 | 0.84 |
| Docker images (MB) | 389 | 406 | 389 | 171 | 171 | 171 |
| Install on disk (MB, no uploads) | 342.0 | 355.7 | 342.0 | 139.0 | 139.0 | 139.0 |
| Database size (MB) | 16.4 | 16.4 | 16.3 | 30.1 | 30.1 | 30.1 |
| Database tables | 59 | 59 | 59 | 92 | 92 | 92 |
| Lighthouse performance (home, mobile) | 96 | 99 | 67 | 100 | 100 | 100 |
| Lighthouse LCP (ms) | 2,705 | 2,106 | 5,705 | 1,880 | 1,880 | 1,881 |
| Home page weight (KB) | 340 | 192 | 867 | 171 | 171 | 175 |


## What was measured

Six setups, three per side. Each WordPress setup has a Pursae counterpart:

| Setup | WordPress | Pursae |
|-------|-----------|--------|
| **Stock** | WordPress 7.1.4, PHP 8.3 php-fpm (official image, OPcache on with the image's settings, pool as shipped: `pm = dynamic`, 5 children), nginx, MariaDB 11.4, no plugins | Pursae, Pursae Cache **off**, no built-in plugins (Classic Editor stays on, as on a fresh install) |
| **Cached** | "Tuned": stock plus Redis 7 with Redis Object Cache (PhpRedis), WP Super Cache with its pages served straight from disk by nginx (precompressed `.gz`, `gzip_static`), a static php-fpm pool of 12, `innodb_buffer_pool_size = 512M` | Pursae Cache **on** (defaults) |
| **Real site** | Stock plus WooCommerce, Yoast SEO, Polylang, ACF, Elementor and Contact Form 7, each set up once (one Polylang language, Yoast indexables built), no page cache | Stock plus the built-ins that cover the same ground: Commerce, SEO, Multi Language, Custom Fields, Web Builder, Contact Form; Pursae Cache off |

**Content** (`bench/content/gen.py`, deterministic): 1000 posts with block markup, 10 categories,
60 tags, 50 pages and 200 products in 5 categories, 12 images (from Twenty Twenty-Five) as featured
images of every post and product. Posts and pages are one WXR file, imported with WP-CLI
`wp import` on WordPress and **Tools › Import** on Pursae; products go through the same
`wc/v3/products/batch` REST calls on both sides (Pursae's WXR importer does not take products);
images through `wp/v2/media`. Permalinks are `/%postname%/` on both.

**Theme**: Twenty Twenty-Five on both — WordPress' own (1.5), and Pursae's Go-template port of it. They
are not byte-identical: Pursae's home page HTML is about half the size, mostly because WordPress
inlines more block CSS.

**Limits** (Docker `deploy.resources.limits`, identical per tier): the app (php-fpm / Pursae) 2 CPUs and
1 GiB, the database (MariaDB / PostgreSQL 17) 1 CPU and 1 GiB. WordPress also needs nginx (1 CPU,
256 MiB) and, tuned, Redis (0.5 CPU, 256 MiB) — extra resources Pursae does not get.

**Endpoints**: home, single post, category archive, product (real-site setups only: the other setups
have no shop), search (`/?s=garden`), REST posts list (`/wp-json/wp/v2/posts`), and, logged in as the
administrator, the dashboard and the post list.

**Metrics**

- *Load* — [k6](https://k6.io) in its own container on the stack's Docker network (4 CPUs, so the
  load generator is not starved), 10 virtual users in a closed loop, each requesting a random URL of
  the endpoint's sample (100 posts, every category, 50 products). Every sample URL is requested once
  first (so a page cache really is warm), then a 5 s warm-up, then three 20 s runs. Reported:
  req/s, TTFB (`http_req_waiting`) p50 and latency p95/p99, median of the three runs.
- *Cold and warm single requests* — right after the app restarts, three URLs per endpoint that the
  load test never touches: the first request (cold: nothing cached) and the second (warm), one at a
  time, with TTFB and the number of database statements. Statements are counted on the server:
  MariaDB `Questions`, PostgreSQL `pg_stat_statements` calls (`BEGIN`/`COMMIT` count on both),
  minus the counter's own query.
- *Memory* — the sum of `docker stats` memory of every container in the stack, idle (10 s after the
  single-request phase) and the highest sample while the load tests ran.
- *Cold start* — seconds from `docker compose start` of the stopped stack to the first answer of the
  home page; and the same for the app containers only (database already up). Median of three.
- *Size* — `docker image inspect` size of every image the stack runs; the install on disk (WordPress
  core + plugins without uploads; the Pursae binary); database size and table count after seeding.
- *Lighthouse* — the home page, mobile preset (Lighthouse's default throttling), median of three runs.

## Fairness rules

- Each side runs its own recommended production shape: WordPress from the official Docker image behind the
  nginx server block from the [WordPress.org nginx guide](https://developer.wordpress.org/advanced-administration/server/web-server/nginx/),
  Pursae from its own Dockerfile, production build, hub off.
- WP-Cron is disabled on page views (`DISABLE_WP_CRON`; the harness runs due events once with
  WP-CLI). That spares WordPress the loopback request; Pursae runs its scheduler in-process.
- Versions are pinned: images by tag, WordPress.org plugins by version (`bench/run.py`, `WP_PLUGINS`).
- Nothing is tuned after looking at the results; losses stay in the tables.

## Reproduce

You need Docker with Compose v2 (8 GB of memory for Docker is plenty), Python 3 and, for the
Lighthouse column, Node and Chrome.

```sh
make bench                                   # about an hour; results in bench/results/<date>/
make bench BENCH_ARGS="--setups wp-stock,pursae-nocache --runs 1 --duration 5s"   # a quick look
```

`bench/run.py --help` lists the options (`--vus`, `--duration`, `--reuse` to skip seeding, `--keep`
to leave the stacks up on ports 18081 and 18082). Each setup leaves its raw k6 summaries, query
samples, Lighthouse scores and `result.json` in `bench/results/<date>/<setup>/`; `summary.json`
holds everything and `bench/report.py` turns it into `summary.md`.

| File | What |
|------|------|
| `bench/run.py` | orchestration: build, seed, switch setups, measure |
| `bench/wp/compose.yaml`, `bench/wp/nginx/*.conf`, `bench/wp/php/*` | the WordPress stack, nginx configs (stock and WP Super Cache), php-fpm pools, the PhpRedis image |
| `bench/pursae/compose.yaml` | the Pursae stack |
| `bench/content/gen.py` | the WXR file and the product payloads |
| `bench/k6/load.js` | the load test |

## Limits of these numbers

- One machine, Docker Desktop on macOS (a Linux VM): absolute numbers are lower than on bare Linux, and
  the ratio between the two sides matters more than the values.
- Cache hits on both sides run into the load generator (tens of thousands of req/s on four k6
  CPUs), so cached throughput says "both are fast enough", not who is faster.
- The themes are equivalent, not identical (see above), and Pursae's built-ins are not feature-for-feature
  copies of the WordPress plugins they are compared with.

## What's built in

Each built-in Pursae plugin covers the core features of a WordPress plugin people usually pay for. It is
not a copy: each feature inventory (linked) lists what is implemented, reduced or left out.

Prices are the cheapest one-site plan billed yearly, **list price** (what a renewal costs), read on the
vendor's own pricing page on **2026-11-09** in the currency it showed, without tax. Many vendors discount
the first year; that price is noted where the page showed one.

| Pursae built-in | Covers the core features of | Plan | Price / year | Checked on |
|-----------------|-----------------------------|------|-------------:|------------|
| [Commerce](../goocommerce-features.md) | WooCommerce (free) + premium extensions such as WooCommerce Subscriptions | Subscriptions, 1 year | $279 | [woocommerce.com](https://woocommerce.com/products/woocommerce-subscriptions/) |
| [Web Builder](web-builder.md) | Elementor Pro | Essential, 1 site | $60 | [elementor.com](https://elementor.com/pricing/) |
| [SEO](../seo-go-features.md) | Yoast SEO Premium (and Rank Math PRO) | 1 site | $118.80 | [yoast.com](https://yoast.com/wordpress/plugins/seo/) |
| [Cache](operations/caching.md) | WP Rocket | Single | $60 ($36 first year, offer until 2026-11-28) | [wp-rocket.me](https://wp-rocket.me/pricing/) |
| [CDN](../gocdn-features.md) | WP Offload Media | Bronze (up to 2,000 media items) | $69 ($39 first year) | [deliciousbrains.com](https://deliciousbrains.com/wp-offload-media/pricing/) |
| [Cookie](../gooike-features.md) | Complianz Premium | Personal, 1 site | $59 | [complianz.io](https://complianz.io/pricing/) |
| [Pixel](../pixelgo-features.md) | PixelYourSite Pro | Starter, 1 site | $199 | [pixelyoursite.com](https://www.pixelyoursite.com/pricing) |
| [SMTP](../wpgo-mail-smtp-features.md) | WP Mail SMTP Pro | Pro, 1 site | $99 ($49 first year) | [wpmailsmtp.com](https://wpmailsmtp.com/pricing/) |
| [Custom Fields](../acf-go-features.md) | ACF PRO | Personal, 1 site | $49 | [advancedcustomfields.com](https://www.advancedcustomfields.com/pro/) |
| [Multi Language](../golylang-features.md) | Polylang Pro | Pro | €99 (renewals 50 % off) | [polylang.pro](https://polylang.pro/pricing/) |
| [Backup Manager](../backup-manager-features.md) | All-in-One WP Migration Unlimited Extension | Standard (up to 50 sites) | $69 | [servmask.com](https://servmask.com/products/unlimited-extension) |
| [Contact Form](../gontact-form-features.md) | Contact Form 7 | — | free | — |
| [Security](../adminsecgo-features.md) | the login-security part of Wordfence / Solid Security (2FA, lockouts, hidden login URL, activity log) — **no firewall or malware scanner** | Wordfence Premium | ($149, not comparable) | [wordfence.com](https://www.wordfence.com/products/pricing/) |
| [Maintenance](../maintenance-go-features.md) | Maintenance PRO (WebFactory) | Personal | sold lifetime only ($59 once) | [wpmaintenancemode.com](https://wpmaintenancemode.com/) |
| [Headless](../headlessgo-features.md) | a headless CMS layer (content types, REST, GraphQL, webhooks); no single WordPress plugin matches | — | — | — |

Added up at list prices, the rows with a yearly price come to **about $1,062 plus €99 a year** for one
site; Security, Maintenance, Headless and Contact Form are left out of that sum. Pursae's built-ins ship in the
GPL binary at no extra cost.
