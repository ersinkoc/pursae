-- When a menu item was added, so drafts abandoned on the Menus screen (added but the menu never saved)
-- can be deleted after a day, like WordPress' _wp_delete_orphaned_draft_menu_items(). Existing rows get
-- the migration time.
ALTER TABLE menu_items ADD COLUMN IF NOT EXISTS created_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX IF NOT EXISTS menu_items_drafts ON menu_items (created_at) WHERE status = 'draft';
