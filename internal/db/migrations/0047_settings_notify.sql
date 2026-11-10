-- Settings are cached in memory by every wpgo process (internal/settings). When a transaction
-- that changed the settings table commits, notify the other processes so they reload their
-- cache (settings.Store.Watch). Statement level with an empty payload: PostgreSQL folds the
-- identical notifications of one transaction into one, and the listener reloads everything.
CREATE OR REPLACE FUNCTION wpgo_settings_notify() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	PERFORM pg_notify('wpgo_settings', '');
	RETURN NULL;
END
$$;

DROP TRIGGER IF EXISTS settings_notify ON settings;
CREATE TRIGGER settings_notify AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON settings
	FOR EACH STATEMENT EXECUTE FUNCTION wpgo_settings_notify();
