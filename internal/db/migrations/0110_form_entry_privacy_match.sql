-- Personal data export and erasure of stored form messages (Pursae Contact Form entries, Web Builder
-- form submissions and code-defined forms): an entry belongs to a person through the form's e-mail
-- field(s), not through any field that happens to hold an address (a "friend's e-mail" field or a
-- message that is just an address), and to the account that was signed in when it was sent.
--   email_fields: the names of the entry's e-mail fields when it was stored (e-mail type fields and
--                 the field the notification answers to); NULL for rows stored before this release,
--                 which match on a fixed list of e-mail field names instead (privacy_sources.go).
--   user_id:      the signed-in visitor, NULL for anonymous ones; cleared when the account goes.
-- Only columns are added: the previous release inserts without them and still works.
ALTER TABLE gontact_entries ADD COLUMN IF NOT EXISTS email_fields text[];
ALTER TABLE gontact_entries ADD COLUMN IF NOT EXISTS user_id bigint REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS gontact_entries_user ON gontact_entries (user_id) WHERE user_id IS NOT NULL;
ALTER TABLE web_builder_form_submissions ADD COLUMN IF NOT EXISTS email_fields text[];
ALTER TABLE web_builder_form_submissions ADD COLUMN IF NOT EXISTS user_id bigint REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS web_builder_form_submissions_user ON web_builder_form_submissions (user_id) WHERE user_id IS NOT NULL;
