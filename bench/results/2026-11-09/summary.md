# Benchmark results 2026-11-09

Host: Apple M2 Pro, 32 GB; Docker 29.7.2 with 12 CPUs / 7.7 GB. Load: k6 (grafana/k6:1.3.0), 10 virtual users, 20s per run after a 5s warm-up, median of 3 runs. Limits per stack: app 2 CPU / 1 GiB, database 1 CPU / 1 GiB (WordPress also gets nginx 1 CPU / 256 MiB and, tuned, Redis 0.5 CPU / 256 MiB).

## Throughput (req/s, higher is better)

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

## TTFB p50 under load (ms)

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

## Latency p95 under load (ms)

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

## Latency p99 under load (ms)

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

## First request after a restart (cold): single-request TTFB · DB queries

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

## Second request (warm): single-request TTFB · DB queries

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

## Resources

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

Seeding wp: {'seconds': 125.2, 'wxr_import_seconds': 7.5}
Seeding pursae: {'seconds': 13.5, 'wxr_import_seconds': 5.5}
