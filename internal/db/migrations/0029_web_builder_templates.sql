-- Pursae Web Builder Theme Builder (docs/developer/web-builder-contract.md §11): header, footer and
-- main-content templates are posts of type wb_template. Their doc and snapshot live in the existing
-- web_builder_* tables; type, display conditions, priority, preview object and starter are post
-- meta (_wb_template_type, _wb_conditions, _wb_priority, _wb_preview, _wb_starter).
-- Every statement is idempotent.

-- Not public: no front URL, no archive, no core editor (the builder edits it), never in the
-- Settings › Web Builder post type list (that list only shows public types).
INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels)
VALUES ('wb_template', 'Templates', false, false, '', false,
	'{"title":true,"author":true,"revisions":true}',
	'{"name":"Templates","singular_name":"Template","add_new":"Add New Template","add_new_item":"Add New Template","edit_item":"Edit Template","new_item":"New Template","view_item":"View Template","search_items":"Search Templates","not_found":"No templates found.","not_found_in_trash":"No templates found in Trash.","all_items":"All Templates","menu_name":"Theme Builder"}')
ON CONFLICT (slug) DO NOTHING;

-- The template index (published templates, read whenever its generation changes) and the
-- Theme Builder list (newest first, per status) both filter on status and sort by modified_at.
CREATE INDEX IF NOT EXISTS posts_wb_templates ON posts (status, modified_at DESC)
	WHERE type = 'wb_template';
