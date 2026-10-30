-- GoCDN, second part: the endpoint each object was written to (so items of an older bucket on
-- another provider/endpoint can still be served and moved after the storage settings changed;
-- '' for rows written before this column existed, which are taken to be on the configured
-- endpoint). See internal/server/gocdn_move.go.
ALTER TABLE gocdn_items ADD COLUMN IF NOT EXISTS endpoint text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS gocdn_items_removed ON gocdn_items (local_removed) WHERE local_removed;
