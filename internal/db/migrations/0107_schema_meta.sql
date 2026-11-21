-- Facts about the schema itself, one row per key (internal/db/schema_guard.go):
--   min_binary_version  the oldest Pursae release that may run on this schema. Migrate raises it to
--                       the migrating binary's db.MinCompatibleBinary and never lowers it, so a
--                       binary that is too old (after a rollback, a reinstall or a manual downgrade)
--                       refuses to start instead of running against a schema it does not understand;
--   migrated_by         the newest Pursae release that has migrated this database (for messages).
CREATE TABLE IF NOT EXISTS schema_meta (
  key        TEXT        PRIMARY KEY,
  value      TEXT        NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
