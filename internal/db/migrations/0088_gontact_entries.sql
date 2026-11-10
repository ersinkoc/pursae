-- Pursae Contact Form entries: one row per accepted submission (validation and spam checks passed),
-- so a message survives a failed mail delivery. mail_status follows the queued wp_mail job:
-- queued, sent, failed, skipped. Forms with do_not_store: true never write here.
CREATE TABLE IF NOT EXISTS gontact_entries (
    id         bigserial PRIMARY KEY,
    form_id    bigint NOT NULL,
    form_title text NOT NULL DEFAULT '',
    posted     jsonb NOT NULL DEFAULT '{}',
    ip         text NOT NULL DEFAULT '',
    user_agent text NOT NULL DEFAULT '',
    page_url   text NOT NULL DEFAULT '',
    status     text NOT NULL DEFAULT 'received',
    mail_status text NOT NULL DEFAULT 'queued',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS gontact_entries_form ON gontact_entries (form_id, created_at DESC);
CREATE INDEX IF NOT EXISTS gontact_entries_created ON gontact_entries (created_at);
