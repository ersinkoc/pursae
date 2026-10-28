-- WPGO Mail SMTP (native plugin): email log, debug events and the rate-limit counter. The tables
-- exist whether or not the plugin is active; they stay empty until it is used.
CREATE TABLE IF NOT EXISTS wpmailsmtp_emails_log (
    id            bigserial PRIMARY KEY,
    message_id    text NOT NULL DEFAULT '',
    subject       text NOT NULL DEFAULT '',
    from_addr     text NOT NULL DEFAULT '',
    to_addrs      text[] NOT NULL DEFAULT '{}',
    reply_to      text NOT NULL DEFAULT '',
    headers       text NOT NULL DEFAULT '',
    content_plain text NOT NULL DEFAULT '',
    content_html  text NOT NULL DEFAULT '',
    has_content   boolean NOT NULL DEFAULT false,
    status        text NOT NULL DEFAULT 'sent', -- sent, failed
    error_text    text NOT NULL DEFAULT '',
    mailer        text NOT NULL DEFAULT '',
    connection    text NOT NULL DEFAULT '',
    initiator     text NOT NULL DEFAULT '',
    attempts      int NOT NULL DEFAULT 1,
    hash          text NOT NULL DEFAULT '',
    parent_id     bigint, -- the entry this one was resent from
    date_sent     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS wpmailsmtp_emails_log_date ON wpmailsmtp_emails_log (date_sent DESC);
CREATE INDEX IF NOT EXISTS wpmailsmtp_emails_log_hash ON wpmailsmtp_emails_log (hash) WHERE status = 'failed';

CREATE TABLE IF NOT EXISTS wpmailsmtp_debug_events (
    id         bigserial PRIMARY KEY,
    event_type text NOT NULL DEFAULT 'error', -- error, debug
    content    text NOT NULL DEFAULT '',
    initiator  text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS wpmailsmtp_debug_events_date ON wpmailsmtp_debug_events (created_at DESC);

CREATE TABLE IF NOT EXISTS wpmailsmtp_rate (sent_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX IF NOT EXISTS wpmailsmtp_rate_sent ON wpmailsmtp_rate (sent_at);
