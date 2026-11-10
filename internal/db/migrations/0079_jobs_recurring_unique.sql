-- A core or built-in recurring event (wp_schedule_event()) exists once. Every start (ensureEvents)
-- and plugin activation inserts the row "WHERE NOT EXISTS", which two instances starting at the same
-- time both pass: each kept its own row, so the event ran twice per period from then on, and more
-- with every simultaneous restart. The extra rows go (the one due first stays) and a unique index
-- makes the losing insert fail instead. Plugin events (payload.plugin) may repeat a hook with other
-- arguments and are serialized by their own advisory lock (plugins_host.go), so they are left out.
DELETE FROM jobs j
	USING jobs k
	WHERE j.recurrence <> '' AND k.recurrence <> ''
		AND NOT (j.payload ? 'plugin') AND NOT (k.payload ? 'plugin')
		AND j.name = k.name
		AND (k.run_at, k.id) < (j.run_at, j.id);

CREATE UNIQUE INDEX IF NOT EXISTS jobs_recurring_name ON jobs (name)
	WHERE recurrence <> '' AND NOT (payload ? 'plugin');
