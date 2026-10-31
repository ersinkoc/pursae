---
title: Scheduled events (cron)
description: The job runner that replaces WP-Cron, the built-in events, plugin events and how to inspect them.
weight: 70
section: operations
---

WordPress runs scheduled tasks when a visitor happens to load a page. Pursae has a real
**job runner**: every 15 seconds each instance publishes due scheduled posts and claims due rows
of the `jobs` table with `FOR UPDATE SKIP LOCKED`, so several instances share one database
without running a job twice. No visitor, no `wp-cron.php`, no system crontab needed.

## Built-in events

| Job | Recurrence | Does |
|-----|------------|------|
| `wp_scheduled_delete` | daily | empties posts and comments that have been in the trash for 30 days |
| `wp_scheduled_auto_draft_delete` | daily | removes auto-drafts older than 7 days |
| `delete_expired_transients` | daily | drops expired transients |
| `wp_session_gc` | hourly | expires login sessions and old login attempts |
| `wpgo_cache_janitor` | hourly | drops expired page-cache entries |
| `wp_mail` | one-off | delivers a queued e-mail ([mail](mail.md)) |
| `publish_future_post` | every tick | publishes posts whose date has come |

## Plugin events

Plugins with the `cron` permission schedule their own hooks (`cron_<name>`) with an interval of
at least a minute or as a one-off run; the runner fires the hook as an action and reschedules it
([scheduled events](../plugins/scheduled-events.md)). Those rows carry `every:<seconds>` as the
recurrence and the plugin slug in the payload.

## Failures and retries

A job that returns an error is retried after 1, 4, 9 and 16 minutes; after the fifth failure it is
marked `failed` with the error text. Jobs stuck in `running` for more than ten minutes (a crashed
instance) go back to the queue. Finished one-off jobs are kept for a week.

## Inspecting

Tools › Site Health lists the scheduled events and failed jobs. From PostgreSQL:

```sql
SELECT name, recurrence, status, run_at, attempts, last_error FROM jobs ORDER BY run_at;
```

A recurring event that went missing is recreated at the next start (`ensureEvents`).
