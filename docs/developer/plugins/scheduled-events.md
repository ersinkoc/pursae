---
title: Scheduled events
description: Run plugin code on a schedule with the core job runner instead of WP-Cron.
weight: 90
section: plugins
---

With the `cron` permission a plugin schedules recurring or one-off events. The core's job runner
([cron](../operations/cron.md)) stores them in the `jobs` table and, when one is due, fires the
plugin's hook as an **action**. No visitor is needed and an event runs once even with several
server instances.

## Register the action and schedule it

The hook name must start with `cron_`, be listed in `plugin.json` `hooks`, and have an action
registered. Schedule from `OnInit` so the host is available; scheduling an already-scheduled hook
replaces its schedule, so doing it on every start is safe.

```go
const refresh = "cron_gopher_facts_refresh"

func init() {
	wpgo.AddAction(refresh, wpgo.DefaultPriority, func(_ wpgo.Context, args []any) {
		payload, _ := args[0].(map[string]any)      // {"plugin": "...", "args": {...}}
		// fetch, compute, store…
		_ = wpgo.Transients.Set("facts", fetchFacts(), 2*time.Hour)
	})
	wpgo.OnInit(func() {
		_ = wpgo.ScheduleEvent(refresh, time.Hour, map[string]any{"source": "api"})
	})
}
```

| Function | Effect |
|----------|--------|
| `ScheduleEvent(hook, interval, args)` | runs every `interval` (≥ 1 minute, ≤ 1 year), first run one interval from now. Calling it again with the same interval and args keeps the existing schedule and its next run (safe in `OnInit`, which runs on every start); a different interval or args replaces it |
| `ScheduleSingleEvent(hook, at, args)` | runs once at `at` (zero time = as soon as possible; at most a year ahead). One-off events are keyed by hook **and args**: several can be queued for one hook with different args (one reminder per order); the same args again replaces the earlier run. A recurring `ScheduleEvent` of the same hook is independent of them |
| `UnscheduleEvent(hook)` | removes the plugin's runs of the hook, one-off and recurring; a run in progress finishes and is not rescheduled |
| `UnscheduleEventArgs(hook, args)` | removes only the runs of the hook scheduled with exactly these args |

`args` (≤64 KiB of JSON) is delivered in the action's single argument under `"args"`.

## Behaviour

- Events run in the background with no request and no user: `wpgo.CurrentUser()` returns `nil`.
  Posts created from cron have no author.
- A run that fails (panic, timeout) is logged and counts as a plugin failure; the event stays
  scheduled. The job runner's retry logic applies to core jobs, not to plugin actions.
- An event fires only the scheduling plugin's own action: another plugin listening on the same
  hook name never receives it.
- A deactivated plugin's events stay in the table and are skipped (not counted as failures) until
  it is active again. Deleting the plugin removes its events, options and transients.
- A `cron_*` callback has 30 seconds (enough for an `HTTPGet` / `HTTPRequest`) and is exempt from the slow-call failure rule. Split long work into smaller events or keep state in
  storage between runs.

Inspect pending events in Tools › Site Health or with
`SELECT name, run_at, recurrence, payload FROM jobs WHERE name LIKE 'cron_%'`.
