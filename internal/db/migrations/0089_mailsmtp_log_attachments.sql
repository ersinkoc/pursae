-- Pursae SMTP: the email log records the name and size of each attachment (JSON list), so a
-- resend can warn that the files are not included.
ALTER TABLE wpmailsmtp_emails_log ADD COLUMN IF NOT EXISTS attachments text NOT NULL DEFAULT '';
