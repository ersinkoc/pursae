-- Search that finds Turkish and Azerbaijani words whatever the i is typed as.
--
-- PostgreSQL lower-cases with the database collation, not the site language: "IŞIK" becomes "işik"
-- and never matches "ışık", and İ may not fold at all. The search texts are now folded: lower() in the
-- database collation, then ı and İ become i and a combining dot above (U+0307) is dropped. The search
-- term goes through the very same expression in SQL (store.FoldedArgSQL), so both sides agree on any
-- collation (a Turkish one lower-cases I to ı, which the fold then makes i). English "INFO" is still
-- "info", and a site that changes its language keeps working without a rebuild.

-- The 'simple' vector (0001) is a generated column: its expression can only change by adding it again.
ALTER TABLE posts DROP COLUMN IF EXISTS search;
ALTER TABLE posts ADD COLUMN search tsvector GENERATED ALWAYS AS (
	setweight(to_tsvector('simple', translate(lower(title), 'ıİ̇', 'ii')), 'A') ||
	setweight(to_tsvector('simple', translate(lower(excerpt), 'ıİ̇', 'ii')), 'B') ||
	setweight(to_tsvector('simple', translate(lower(regexp_replace(content, '<[^>]*>', ' ', 'g')), 'ıİ̇', 'ii')), 'C')
) STORED;
CREATE INDEX IF NOT EXISTS posts_search ON posts USING gin(search);

-- The stemmed vector (0101) folds the same way. Existing vectors are cleared so the background
-- rebuild (store.SyncSearchStem, run at start-up) redoes them in batches with the new function.
CREATE OR REPLACE FUNCTION wpgo_search_stem_vector(cfg regconfig, title text, excerpt text, content text) RETURNS tsvector
	LANGUAGE sql IMMUTABLE AS $$
	SELECT CASE WHEN cfg = 'simple'::regconfig THEN NULL ELSE
		setweight(to_tsvector(cfg, translate(lower(coalesce(title, '')), 'ıİ̇', 'ii')), 'A') ||
		setweight(to_tsvector(cfg, translate(lower(coalesce(excerpt, '')), 'ıİ̇', 'ii')), 'B') ||
		setweight(to_tsvector(cfg, translate(lower(regexp_replace(coalesce(content, ''), '<[^>]*>', ' ', 'g')), 'ıİ̇', 'ii')), 'C') END $$;
UPDATE posts SET search_stem = NULL WHERE search_stem IS NOT NULL;

-- The title branch of search is now "translate(lower(title), 'ıİ̇', 'ii') LIKE …": the trigram index (0032)
-- follows it, under the same name.
DO $$
DECLARE
	ext_schema text;
BEGIN
	SELECT n.nspname INTO ext_schema FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace
		WHERE e.extname = 'pg_trgm';
	IF ext_schema IS NOT NULL THEN
		DROP INDEX IF EXISTS posts_title_trgm;
		EXECUTE format('CREATE INDEX posts_title_trgm ON posts USING gin (translate(lower(title), %L, %L) %I.gin_trgm_ops)', 'ıİ̇', 'ii', ext_schema);
	END IF;
END
$$;
