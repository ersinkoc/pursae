-- Performance indexes and the published_at guarantee the post lists order by.

-- Published, private and scheduled posts always have a date: list queries for those statuses order
-- by published_at (not coalesce(published_at, created_at)) so posts_list_date serves them in order.
-- Rows written without one (raw inserts, status flips) fall back to their creation time, which is
-- what the old coalesce ordering used.
UPDATE posts SET published_at = created_at
	WHERE published_at IS NULL AND status IN ('publish', 'private', 'future');

CREATE OR REPLACE FUNCTION posts_published_at_default() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.published_at IS NULL AND NEW.status IN ('publish', 'private', 'future') THEN
		NEW.published_at := coalesce(NEW.created_at, now());
	END IF;
	RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS posts_published_at_default ON posts;
CREATE TRIGGER posts_published_at_default BEFORE INSERT OR UPDATE OF status, published_at ON posts
	FOR EACH ROW EXECUTE FUNCTION posts_published_at_default();

-- The main post list: type and status, newest first with the id as tie-breaker (replaces posts_list,
-- its prefix).
CREATE INDEX IF NOT EXISTS posts_list_date ON posts (type, status, published_at DESC, id DESC);
DROP INDEX IF EXISTS posts_list;

-- Case-insensitive login and email lookups (login form, avatars, uniqueness checks).
CREATE INDEX IF NOT EXISTS users_lower_email ON users (lower(email));
CREATE INDEX IF NOT EXISTS users_lower_login ON users (lower(login));

-- Foreign keys that are filtered on but had no index.
CREATE INDEX IF NOT EXISTS comments_parent ON comments (parent_id);
CREATE INDEX IF NOT EXISTS posts_featured_media ON posts (featured_media_id);
CREATE INDEX IF NOT EXISTS widgets_area ON widgets (area_id);

-- Search's "title ILIKE '%term%'" branch: a trigram index when pg_trgm can be created (it ships with
-- PostgreSQL's contrib, part of the official images, and is a trusted extension). Without it search
-- still works, only without this index.
DO $$
DECLARE
	ext_schema text;
BEGIN
	BEGIN
		CREATE EXTENSION IF NOT EXISTS pg_trgm;
	EXCEPTION WHEN others THEN
		RAISE NOTICE 'pg_trgm unavailable (%): search titles stay unindexed', SQLERRM;
	END;
	SELECT n.nspname INTO ext_schema FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace
		WHERE e.extname = 'pg_trgm';
	IF ext_schema IS NOT NULL THEN
		EXECUTE format('CREATE INDEX IF NOT EXISTS posts_title_trgm ON posts USING gin (title %I.gin_trgm_ops)', ext_schema);
	END IF;
END
$$;
