-- Pursae Web Builder replaced the old (Elementor-derived) builder, and the old builder's data goes
-- with it (owner decision: no converter, deleted completely). This cannot be undone: the release
-- notes ask for a database backup before upgrading (docs/developer/getting-started/upgrading.md).
--
-- What goes: the tables of migrations 0005/0006/0007, the template/theme-part/component posts and
-- their content types, every _elementor_*/_gwe_* post meta key, the old builder's options and user
-- meta, and its saved themes. What stays: the new builder's web_builder_* tables and options,
-- native_golementor (the plugin switch, shared with the new builder) and golementor_previous_theme
-- (read and cleared at the next start by the theme fallback, internal/server/webbuilder_upgrade.go).
-- Every statement is idempotent, so a database that never had the old builder is left as it was.

-- Tables. builder_revisions goes before the posts below cascade into post_revisions.
DROP TABLE IF EXISTS builder_revisions;
DROP TABLE IF EXISTS gwe_global_classes;
DROP TABLE IF EXISTS gwe_variables;
DROP TABLE IF EXISTS gwe_default_styles;
DROP TABLE IF EXISTS gwe_form_submissions;

DROP INDEX IF EXISTS posts_gwe_parts;
DROP INDEX IF EXISTS posts_gwe_components;

-- Saved templates and the site kit (elementor_library), theme parts (gwe_template) and components
-- (elementor_component). Their revisions, terms, comments and language rows cascade; children and
-- attached media lose the parent (ON DELETE SET NULL). The content types go once no post uses them.
DELETE FROM posts WHERE type IN ('elementor_library', 'gwe_template', 'elementor_component');
DELETE FROM content_types WHERE slug IN ('elementor_library', 'gwe_template', 'elementor_component');

-- Pages that used the old builder's page templates (Canvas, Full Width) get the theme's default.
UPDATE posts SET meta = jsonb_set(meta, '{_wp_page_template}', '"default"')
 WHERE meta->>'_wp_page_template' LIKE 'elementor\_%';

-- Post meta: _elementor_data, _elementor_edit_mode, _elementor_page_settings, _gwe_conditions, ...
UPDATE posts p SET meta = p.meta - ARRAY(
	SELECT k FROM jsonb_object_keys(p.meta) k WHERE k LIKE '\_elementor\_%' OR k LIKE '\_gwe\_%')
 WHERE EXISTS (SELECT 1 FROM jsonb_object_keys(p.meta) k WHERE k LIKE '\_elementor\_%' OR k LIKE '\_gwe\_%');

-- User meta of the old editor (gwe_introduction, gwe_editor_preferences).
UPDATE users u SET meta = u.meta - ARRAY(
	SELECT k FROM jsonb_object_keys(u.meta) k
	 WHERE k LIKE 'gwe\_%' OR k LIKE '\_gwe\_%' OR k LIKE 'elementor\_%' OR k LIKE '\_elementor\_%')
 WHERE EXISTS (SELECT 1 FROM jsonb_object_keys(u.meta) k
	 WHERE k LIKE 'gwe\_%' OR k LIKE '\_gwe\_%' OR k LIKE 'elementor\_%' OR k LIKE '\_elementor\_%');

-- Options: gwe_* (integrations' API keys, reCAPTCHA, custom fonts and icon sets, role manager, site
-- kit, ...), elementor_* (elementor_active_kit), _elementor_* (design system sync), the setup
-- wizard's record and the theme mods of the old builder's blank theme.
DELETE FROM settings
 WHERE key LIKE 'gwe\_%' OR key LIKE '\_gwe\_%'
    OR key LIKE 'elementor\_%' OR key LIKE '\_elementor\_%'
    OR key LIKE 'gowebeditor\_%'
    OR (key LIKE 'golementor\_%' AND key <> 'golementor_previous_theme')
    OR key IN ('theme_mods_hello-web-builder', 'theme_mods_hello-gowebeditor');

DELETE FROM transients WHERE key LIKE 'gwe\_%' OR key LIKE 'elementor\_%' OR key LIKE '\_elementor\_%';

-- Themes saved with the old builder ("Save as theme") and its blank theme, unless the site still
-- runs on one: the start-up fallback (webbuilder_upgrade.go) needs that row to recognise the theme
-- and switches away from it.
DELETE FROM themes
 WHERE (custom OR slug IN ('hello-web-builder', 'hello-gowebeditor'))
   AND status <> 'active'
   AND slug NOT IN (SELECT value #>> '{}' FROM settings WHERE key IN ('stylesheet', 'template') AND jsonb_typeof(value) = 'string');
