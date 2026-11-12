-- Pursae Custom Fields: field values of menus and menu items (the Menu and Menu Item location rules).
ALTER TABLE menu_items ADD COLUMN IF NOT EXISTS meta jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE menus ADD COLUMN IF NOT EXISTS meta jsonb NOT NULL DEFAULT '{}'::jsonb;
