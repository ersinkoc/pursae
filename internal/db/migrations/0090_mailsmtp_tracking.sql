-- Pursae SMTP: open/click tracking and delivery status (bounces, complaints) on the email log.
ALTER TABLE wpmailsmtp_emails_log ADD COLUMN IF NOT EXISTS track_id        text NOT NULL DEFAULT '';
ALTER TABLE wpmailsmtp_emails_log ADD COLUMN IF NOT EXISTS open_count      int  NOT NULL DEFAULT 0;
ALTER TABLE wpmailsmtp_emails_log ADD COLUMN IF NOT EXISTS click_count     int  NOT NULL DEFAULT 0;
ALTER TABLE wpmailsmtp_emails_log ADD COLUMN IF NOT EXISTS first_opened_at timestamptz;
ALTER TABLE wpmailsmtp_emails_log ADD COLUMN IF NOT EXISTS delivery_status text NOT NULL DEFAULT ''; -- '', delivered, bounced, complained
ALTER TABLE wpmailsmtp_emails_log ADD COLUMN IF NOT EXISTS delivery_detail text NOT NULL DEFAULT '';
ALTER TABLE wpmailsmtp_emails_log ADD COLUMN IF NOT EXISTS delivery_at     timestamptz;
CREATE INDEX IF NOT EXISTS wpmailsmtp_emails_log_track ON wpmailsmtp_emails_log (track_id) WHERE track_id <> '';
CREATE INDEX IF NOT EXISTS wpmailsmtp_emails_log_msgid ON wpmailsmtp_emails_log (message_id) WHERE message_id <> '';
