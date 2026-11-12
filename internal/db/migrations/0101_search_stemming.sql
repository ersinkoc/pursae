-- Search stemming in the site's language.
--
-- posts.search (0001) is a 'simple' vector: exact words only. search_stem holds the same text through
-- the PostgreSQL text search configuration of the site language (english, turkish, german, …), so a
-- search for "running" also finds "run". The configuration in use is the one row of search_stem_state;
-- the server sets it from WPLANG and rebuilds the column in the background when the language changes
-- (store.SyncSearchStem). A language without a stemmer keeps 'simple' and search_stem stays NULL.
CREATE TABLE IF NOT EXISTS search_stem_state (
	id  boolean  PRIMARY KEY DEFAULT true CHECK (id),
	cfg regconfig NOT NULL DEFAULT 'simple'
);
INSERT INTO search_stem_state DEFAULT VALUES ON CONFLICT DO NOTHING;

CREATE OR REPLACE FUNCTION wpgo_search_stem_config() RETURNS regconfig
	LANGUAGE sql STABLE AS $$ SELECT coalesce((SELECT cfg FROM search_stem_state), 'simple'::regconfig) $$;

-- The vector of one post under cfg (NULL for 'simple': the plain search column already covers it).
CREATE OR REPLACE FUNCTION wpgo_search_stem_vector(cfg regconfig, title text, excerpt text, content text) RETURNS tsvector
	LANGUAGE sql IMMUTABLE AS $$
	SELECT CASE WHEN cfg = 'simple'::regconfig THEN NULL ELSE
		setweight(to_tsvector(cfg, coalesce(title, '')), 'A') ||
		setweight(to_tsvector(cfg, coalesce(excerpt, '')), 'B') ||
		setweight(to_tsvector(cfg, regexp_replace(coalesce(content, ''), '<[^>]*>', ' ', 'g')), 'C') END $$;

ALTER TABLE posts ADD COLUMN IF NOT EXISTS search_stem tsvector;

CREATE OR REPLACE FUNCTION wpgo_posts_search_stem() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	NEW.search_stem := wpgo_search_stem_vector(wpgo_search_stem_config(), NEW.title, NEW.excerpt, NEW.content);
	RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS posts_search_stem ON posts;
CREATE TRIGGER posts_search_stem BEFORE INSERT OR UPDATE OF title, excerpt, content ON posts
	FOR EACH ROW EXECUTE FUNCTION wpgo_posts_search_stem();

CREATE INDEX IF NOT EXISTS posts_search_stem ON posts USING gin(search_stem);
