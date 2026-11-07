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
| `wp_scheduled_delete` | daily | empties posts and comments that have been in the trash for 30 days (`WPGO_EMPTY_TRASH_DAYS`, [configuration](configuration.md)) |
| `wp_scheduled_auto_draft_delete` | daily | removes auto-drafts older than 7 days |
| `delete_expired_transients` | daily | drops expired transients |
| `wp_session_gc` | hourly | expires login sessions and old login attempts |
| `wpgo_cache_janitor` | hourly | drops expired page-cache entries |
| `seogo_404_trim` | hourly | keeps Pursae SEO's 404 log at the monitor's limit |
| `wp_mail` | one-off | delivers a queued e-mail ([mail](mail.md)) |
| `publish_future_post` | every tick | publishes posts whose date has come |
| `wpgo_backup_scheduled` | daily or weekly | Backup Manager's scheduled local backup, only while a schedule is set (Backup Manager › Schedule); not recreated by `ensureEvents` |

## Plugin events

Plugins with the `cron` permission schedule their own hooks (`cron_<name>`) with an interval of
at least a minute or as a one-off run; the runner fires the hook as an action and reschedules it
([scheduled events](../plugins/scheduled-events.md)). Those rows carry `every:<seconds>` as the
recurrence and the plugin slug in the payload.

## Failures and retries

A job that returns an error (or panics) is retried after 1, 4, 9 and 16 minutes.

- A **one-off** job is marked `failed` with the error text after the fifth failure.
- A **recurring** job never fails for good: its retries are never later than its next regular run,
  and after the fifth failure in a row it simply waits for that run, keeping `attempts` and
  `last_error` for Site Health until it succeeds again. Recurring rows an older version marked
  `failed` are put back on the queue at start-up.

While a job runs, its instance refreshes `locked_at` every minute; a row still `running` whose
lock is more than ten minutes old belonged to an instance that died and goes back to the queue.
On shutdown (SIGTERM) the job in hand gets a cancelled context: if it stops with an error it goes
back to the queue without counting an attempt, and so do the jobs claimed but not started.
Finished one-off jobs are kept for a week.

## Inspecting

Tools › Site Health lists the scheduled events and failed jobs. From PostgreSQL:

```sql
SELECT name, recurrence, status, run_at, attempts, last_error FROM jobs ORDER BY run_at;
```

A recurring event that went missing is recreated at the next start (`ensureEvents`).
