-- Menu locations live in their own table (WordPress' nav_menu_locations theme mod: location => menu id),
-- so one menu can be assigned to several locations (header and footer, header and mobile). The old
-- menus.location column held a single location per menu.
CREATE TABLE IF NOT EXISTS menu_locations (
	location text PRIMARY KEY,
	menu_id  bigint NOT NULL REFERENCES menus(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS menu_locations_menu ON menu_locations (menu_id);

INSERT INTO menu_locations (location, menu_id)
	SELECT DISTINCT ON (location) location, id FROM menus
	WHERE location IS NOT NULL AND location <> '' ORDER BY location, id
	ON CONFLICT (location) DO NOTHING;

ALTER TABLE menus DROP COLUMN IF EXISTS location;

-- Comment submission lookups (WordPress' comment_date_gmt and comment_author_email indexes): the flood
-- check's one-hour window and the previously-approved / duplicate checks by author e-mail.
CREATE INDEX IF NOT EXISTS comments_created ON comments (created_at DESC);
CREATE INDEX IF NOT EXISTS comments_author_email ON comments (author_email, status);
