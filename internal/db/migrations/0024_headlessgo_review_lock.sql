-- HeadlessGo: edit locks and the review/approval flow.

-- One lock per document version (entry) being edited in the Content Manager. The holder keeps it
-- alive with a heartbeat (touched_at); a lock nobody touched for a while is free again.
CREATE TABLE IF NOT EXISTS hg_entry_locks (
    entry_id   bigint PRIMARY KEY REFERENCES hg_entries(id) ON DELETE CASCADE,
    user_id    bigint NOT NULL,
    taken_at   timestamptz NOT NULL DEFAULT now(),
    touched_at timestamptz NOT NULL DEFAULT now()
);

-- Review state of the draft: '' (none), pending, approved, rejected. review_hash is the hash of the
-- draft that was submitted / approved: a draft that changed since is not approved any more.
ALTER TABLE hg_entries ADD COLUMN IF NOT EXISTS review_status text NOT NULL DEFAULT '';
ALTER TABLE hg_entries ADD COLUMN IF NOT EXISTS review_hash text NOT NULL DEFAULT '';
ALTER TABLE hg_entries ADD COLUMN IF NOT EXISTS review_by bigint NOT NULL DEFAULT 0;
ALTER TABLE hg_entries ADD COLUMN IF NOT EXISTS review_note text NOT NULL DEFAULT '';
ALTER TABLE hg_entries ADD COLUMN IF NOT EXISTS review_at timestamptz;

-- A note on a history entry (the reviewer's comment); actions submit, approve and reject join the others.
ALTER TABLE hg_entry_versions ADD COLUMN IF NOT EXISTS note text NOT NULL DEFAULT '';
