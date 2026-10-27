-- Menu items added on the Menus screen stay drafts until the menu is saved (wp_save_nav_menu_items()).
ALTER TABLE menu_items ADD COLUMN status text NOT NULL DEFAULT 'publish' CHECK (status IN ('publish','draft'));
ALTER TABLE menu_items ADD COLUMN attr_title text NOT NULL DEFAULT '';
ALTER TABLE menu_items ADD COLUMN description text NOT NULL DEFAULT '';
ALTER TABLE menu_items ADD COLUMN xfn text NOT NULL DEFAULT '';
ALTER TABLE menus ADD COLUMN auto_add boolean NOT NULL DEFAULT false;
CREATE INDEX menu_items_menu ON menu_items(menu_id, sort_order);
