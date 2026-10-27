-- Golementor: the template library and theme-part content types, builder revisions and
-- custom (Golementor-built) themes.

-- elementor_library holds saved templates and the site kit; gwe_template holds theme parts
-- (header, footer, single-post, single-page, archive, search-results, error-404).
INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels)
VALUES ('elementor_library', 'Templates', false, false, '', false,
	'{"title":true,"editor":true,"author":true,"thumbnail":true,"revisions":true}', '{"name":"Templates","singular_name":"Template"}'),
	('gwe_template', 'Theme Parts', false, false, '', false,
	'{"title":true,"editor":true,"author":true,"revisions":true}', '{"name":"Theme Parts","singular_name":"Theme Part"}')
ON CONFLICT (slug) DO NOTHING;

-- Element trees per revision. Older databases created this table at runtime, hence IF NOT EXISTS.
CREATE TABLE IF NOT EXISTS builder_revisions (
	revision_id bigint PRIMARY KEY REFERENCES post_revisions(id) ON DELETE CASCADE,
	data        jsonb NOT NULL DEFAULT '[]',
	settings    jsonb NOT NULL DEFAULT '{}'
);

-- Theme parts are looked up by type and owning theme on every front-end request.
CREATE INDEX IF NOT EXISTS posts_gwe_parts ON posts ((meta->>'_gwe_theme'), (meta->>'_elementor_template_type'))
	WHERE type = 'gwe_template';

-- Custom themes ("Save as theme"): the snapshot of kit settings, parts and conditions lives in data.
ALTER TABLE themes ADD COLUMN IF NOT EXISTS custom boolean NOT NULL DEFAULT false;
ALTER TABLE themes ADD COLUMN IF NOT EXISTS name text NOT NULL DEFAULT '';
ALTER TABLE themes ADD COLUMN IF NOT EXISTS description text NOT NULL DEFAULT '';
ALTER TABLE themes ADD COLUMN IF NOT EXISTS author text NOT NULL DEFAULT '';
ALTER TABLE themes ADD COLUMN IF NOT EXISTS screenshot_id bigint REFERENCES media(id) ON DELETE SET NULL;
ALTER TABLE themes ADD COLUMN IF NOT EXISTS data jsonb NOT NULL DEFAULT '{}';
