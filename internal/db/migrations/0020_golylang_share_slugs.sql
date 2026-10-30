-- Golylang's Share slugs module (Polylang Pro): translations may use the same slug, so slugs are no
-- longer unique in the database. The store keeps them unique in code, like wp_unique_post_slug() and
-- wp_unique_term_slug(): per post type and parent (per taxonomy for terms), and per language while the
-- module is on (store.SlugScope).
DROP INDEX IF EXISTS posts_type_parent_slug;
CREATE INDEX IF NOT EXISTS posts_type_parent_slug_lookup ON posts(type, coalesce(parent_id, 0), slug)
	WHERE slug <> '' AND status NOT IN ('auto-draft', 'trash');
ALTER TABLE terms DROP CONSTRAINT IF EXISTS terms_taxonomy_slug_key;
CREATE INDEX IF NOT EXISTS terms_taxonomy_slug ON terms(taxonomy, slug);
