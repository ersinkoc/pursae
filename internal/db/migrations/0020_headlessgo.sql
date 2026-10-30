-- HeadlessGo (native headless CMS plugin). Content types, components and single types are stored as
-- JSON schemas (hg_schemas); every entry of every type lives in hg_entries: the draft (data) and the
-- published snapshot (published_data, NULL while unpublished) as JSONB, with typed columns for the
-- document, its locale and timestamps. Schema changes migrate entry data in place (renamed and
-- removed fields), so no DDL runs per content type. The tables exist whether or not the plugin is
-- active; they stay empty until it is used and are kept when it is deactivated.
CREATE TABLE IF NOT EXISTS hg_schemas (
    uid        text PRIMARY KEY,               -- singular API id, or category.name for a component
    kind       text NOT NULL,                  -- collectionType, singleType, component
    schema     jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS hg_entries (
    id             bigserial PRIMARY KEY,
    document_id    text NOT NULL,
    type_uid       text NOT NULL REFERENCES hg_schemas(uid) ON DELETE CASCADE ON UPDATE CASCADE,
    locale         text NOT NULL DEFAULT '',   -- '' for a type without i18n
    data           jsonb NOT NULL DEFAULT '{}',
    published_data jsonb,
    published_at   timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    created_by     bigint NOT NULL DEFAULT 0,
    updated_by     bigint NOT NULL DEFAULT 0,
    UNIQUE (type_uid, document_id, locale)
);
CREATE INDEX IF NOT EXISTS hg_entries_type ON hg_entries (type_uid, locale, updated_at DESC);
CREATE INDEX IF NOT EXISTS hg_entries_doc ON hg_entries (document_id);
CREATE INDEX IF NOT EXISTS hg_entries_data ON hg_entries USING gin (data jsonb_path_ops);
CREATE INDEX IF NOT EXISTS hg_entries_pub ON hg_entries USING gin (published_data jsonb_path_ops);

-- API tokens: only a SHA-256 of the token is stored; the token is shown once on creation.
CREATE TABLE IF NOT EXISTS hg_api_tokens (
    id           bigserial PRIMARY KEY,
    name         text NOT NULL,
    description  text NOT NULL DEFAULT '',
    kind         text NOT NULL DEFAULT 'read-only', -- read-only, full-access, custom
    permissions  jsonb NOT NULL DEFAULT '{}',       -- custom: {"type-uid": ["find","findOne",...]}
    drafts       boolean NOT NULL DEFAULT false,    -- may read drafts (status=draft)
    token_hash   text NOT NULL UNIQUE,
    token_hint   text NOT NULL DEFAULT '',          -- the last four characters, for the list
    expires_at   timestamptz,
    last_used_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS hg_webhooks (
    id         bigserial PRIMARY KEY,
    name       text NOT NULL,
    url        text NOT NULL,
    events     text[] NOT NULL DEFAULT '{}',
    headers    jsonb NOT NULL DEFAULT '{}',
    secret     text NOT NULL DEFAULT '',
    enabled    boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS hg_webhook_deliveries (
    id          bigserial PRIMARY KEY,
    webhook_id  bigint NOT NULL REFERENCES hg_webhooks(id) ON DELETE CASCADE,
    delivery_id text NOT NULL,
    event       text NOT NULL,
    attempt     int NOT NULL DEFAULT 1,
    status_code int NOT NULL DEFAULT 0,
    duration_ms int NOT NULL DEFAULT 0,
    success     boolean NOT NULL DEFAULT false,
    error       text NOT NULL DEFAULT '',
    response    text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS hg_webhook_deliveries_hook ON hg_webhook_deliveries (webhook_id, created_at DESC);
