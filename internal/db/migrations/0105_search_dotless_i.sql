-- Search that finds Turkish and Azerbaijani words whatever the i is typed as.
--
-- PostgreSQL lower-cases with the database collation, not the site language: "IŞIK" becomes "işik"
-- and never matches "ışık", and İ may not fold at all. The search texts are now i-folded first
-- (ı and İ become i, translate() before the vector lower-cases), and the server folds the search term
-- the same way (textfold.Fold). The fold is the same for every language, so English "INFO" is still
-- "info" and a site that changes its language keeps working without a rebuild.

-- The 'simple' vector (0001) is a generated column: its expression can only change by adding it again.
ALTER TABLE posts DROP COLUMN IF EXISTS search;
ALTER TABLE posts ADD COLUMN search tsvector GENERATED ALWAYS AS (
	setweight(to_tsvector('simple', translate(title, 'ıİ', 'ii')), 'A') ||
	setweight(to_tsvector('simple', translate(excerpt, 'ıİ', 'ii')), 'B') ||
	setweight(to_tsvector('simple', translate(regexp_replace(content, '<[^>]*>', ' ', 'g'), 'ıİ', 'ii')), 'C')
) STORED;
CREATE INDEX IF NOT EXISTS posts_search ON posts USING gin(search);

-- The stemmed vector (0101) folds the same way. Existing vectors are cleared so the background
-- rebuild (store.SyncSearchStem, run at start-up) redoes them in batches with the new function.
CREATE OR REPLACE FUNCTION wpgo_search_stem_vector(cfg regconfig, title text, excerpt text, content text) RETURNS tsvector
	LANGUAGE sql IMMUTABLE AS $$
	SELECT CASE WHEN cfg = 'simple'::regconfig THEN NULL ELSE
		setweight(to_tsvector(cfg, translate(coalesce(title, ''), 'ıİ', 'ii')), 'A') ||
		setweight(to_tsvector(cfg, translate(coalesce(excerpt, ''), 'ıİ', 'ii')), 'B') ||
		setweight(to_tsvector(cfg, translate(regexp_replace(coalesce(content, ''), '<[^>]*>', ' ', 'g'), 'ıİ', 'ii')), 'C') END $$;
UPDATE posts SET search_stem = NULL WHERE search_stem IS NOT NULL;

-- The title branch of search is now "translate(title, 'ıİ', 'ii') ILIKE …": the trigram index (0032)
-- follows it, under the same name.
DO $$
DECLARE
	ext_schema text;
BEGIN
	SELECT n.nspname INTO ext_schema FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace
		WHERE e.extname = 'pg_trgm';
	IF ext_schema IS NOT NULL THEN
		DROP INDEX IF EXISTS posts_title_trgm;
		EXECUTE format('CREATE INDEX posts_title_trgm ON posts USING gin (translate(title, %L, %L) %I.gin_trgm_ops)', 'ıİ', 'ii', ext_schema);
	END IF;
END
$$;
