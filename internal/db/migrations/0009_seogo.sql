-- SEO-Go (native plugin): the redirections manager uses the core redirects table, extended with the
-- match type (exact, contains, start, end, regex), an on/off status, a category and the last hit;
-- the 404 monitor keeps one row per missing URL.
ALTER TABLE redirects ADD COLUMN IF NOT EXISTS match_type text NOT NULL DEFAULT 'exact';
ALTER TABLE redirects ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'active';
ALTER TABLE redirects ADD COLUMN IF NOT EXISTS category text NOT NULL DEFAULT '';
ALTER TABLE redirects ADD COLUMN IF NOT EXISTS last_hit_at timestamptz;
ALTER TABLE redirects ADD COLUMN IF NOT EXISTS origin text NOT NULL DEFAULT 'manual'; -- manual, auto (slug change), import

CREATE TABLE IF NOT EXISTS seogo_404 (
	id          bigserial PRIMARY KEY,
	uri         text NOT NULL UNIQUE,
	hits        bigint NOT NULL DEFAULT 1,
	referer     text NOT NULL DEFAULT '',
	user_agent  text NOT NULL DEFAULT '',
	first_seen  timestamptz NOT NULL DEFAULT now(),
	last_seen   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS seogo_404_last_seen ON seogo_404 (last_seen DESC);
