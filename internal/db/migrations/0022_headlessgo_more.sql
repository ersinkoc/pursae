-- HeadlessGo, second part: scheduled publishing, content history, upload permission of API tokens
-- and the durable webhook delivery queue.

-- Scheduled publishing / unpublishing of one document version; the scheduler (the headlessgo_tick
-- job) publishes or unpublishes the rows whose time has come. schedule_error keeps why a
-- scheduled publication failed (a required field was empty, for example).
ALTER TABLE hg_entries ADD COLUMN IF NOT EXISTS publish_at timestamptz;
ALTER TABLE hg_entries ADD COLUMN IF NOT EXISTS unpublish_at timestamptz;
ALTER TABLE hg_entries ADD COLUMN IF NOT EXISTS schedule_error text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS hg_entries_publish_at ON hg_entries (publish_at) WHERE publish_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS hg_entries_unpublish_at ON hg_entries (unpublish_at) WHERE unpublish_at IS NOT NULL;

-- Content history: the draft of a document version after every save, publish, unpublish,
-- discard and restore (the last 50 per version are kept). Deleting the entry deletes its history.
CREATE TABLE IF NOT EXISTS hg_entry_versions (
    id          bigserial PRIMARY KEY,
    entry_id    bigint NOT NULL REFERENCES hg_entries(id) ON DELETE CASCADE,
    type_uid    text NOT NULL,
    document_id text NOT NULL,
    locale      text NOT NULL DEFAULT '',
    action      text NOT NULL,              -- create, save, publish, unpublish, discard, restore, schedule
    data        jsonb NOT NULL DEFAULT '{}',
    published   boolean NOT NULL DEFAULT false, -- the version was live after this action
    created_by  bigint NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS hg_entry_versions_entry ON hg_entry_versions (entry_id, id DESC);

-- API tokens that may upload media through POST /wp-json/headlessgo/v1/upload (full-access tokens
-- always may).
ALTER TABLE hg_api_tokens ADD COLUMN IF NOT EXISTS can_upload boolean NOT NULL DEFAULT false;

-- Webhook deliveries waiting for (another) attempt. A worker claims due rows with FOR UPDATE SKIP
-- LOCKED; failed attempts come back with an exponential backoff until max_attempts. Rows survive
-- restarts; finished ones are removed after a day. Each attempt is logged in hg_webhook_deliveries.
CREATE TABLE IF NOT EXISTS hg_webhook_queue (
    id           bigserial PRIMARY KEY,
    webhook_id   bigint NOT NULL REFERENCES hg_webhooks(id) ON DELETE CASCADE,
    delivery_id  text NOT NULL,
    event        text NOT NULL,
    body         bytea NOT NULL,
    attempts     int NOT NULL DEFAULT 0,
    max_attempts int NOT NULL DEFAULT 5,
    status       text NOT NULL DEFAULT 'pending', -- pending, running, done, failed
    next_at      timestamptz NOT NULL DEFAULT now(),
    locked_at    timestamptz,
    last_error   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS hg_webhook_queue_due ON hg_webhook_queue (next_at) WHERE status = 'pending';
