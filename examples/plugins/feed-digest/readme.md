# Feed Digest

Example plugin for Pursae showing outbound HTTP from a scheduled event.

- Scheduled event `cron_feed_digest_refresh` (hourly): fetches the JSON feed at the option
  `feed_digest_url` with `wpgo.HTTPGet` and caches it in a transient for two hours.
- Shortcode `[feed-digest limit="5"]`: renders the cached items as an escaped list.

Permissions: `http:outbound`, `storage`, `cron`, `content:filter`, `options:read`.
