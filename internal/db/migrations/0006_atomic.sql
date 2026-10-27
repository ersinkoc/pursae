-- Golementor v4 (atomic / Editor V4): global classes, global variables, default styles and
-- components. The REST routes live in internal/server/gowebeditor_v4_rest.go, the typed
-- accessors in gowebeditor_v4_store.go.

-- Global CSS classes of a kit. The frontend (published) version lives in label/variants/position;
-- the preview (editor draft) version in the preview_* columns, each falling back to the frontend
-- value when NULL. A class added in the preview only has published=false until it is published;
-- a class deleted in the preview keeps its frontend data with preview_deleted=true until the
-- deletion is published (then the row goes).
CREATE TABLE IF NOT EXISTS gwe_global_classes (
	kit_id           bigint NOT NULL,
	id               text NOT NULL,
	label            text NOT NULL DEFAULT '',
	variants         jsonb NOT NULL DEFAULT '[]',
	position         int NOT NULL DEFAULT 0,
	published        boolean NOT NULL DEFAULT false,
	preview_label    text,
	preview_variants jsonb,
	preview_position int,
	preview_deleted  boolean NOT NULL DEFAULT false,
	created_at       timestamptz NOT NULL DEFAULT now(),
	edited_at        timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (kit_id, id)
);

-- Global variables of a kit (upstream kit meta _elementor_global_variables): data is
-- { "<id>": {type,label,value,order,created_at,updated_at,[deleted_at],[sync_to_v3]} } with the
-- value in the storage (v2) shape {"$$type":"color"|"string"|"size","value":...}; the watermark
-- increments on every save.
CREATE TABLE IF NOT EXISTS gwe_variables (
	kit_id    bigint PRIMARY KEY,
	data      jsonb NOT NULL DEFAULT '{}',
	watermark int NOT NULL DEFAULT 0,
	version   int NOT NULL DEFAULT 2
);

-- Default styles per HTML tag (h1, p, a, ...): the variants of the e-default-<tag> class.
CREATE TABLE IF NOT EXISTS gwe_default_styles (
	kit_id   bigint NOT NULL,
	tag      text NOT NULL,
	variants jsonb NOT NULL DEFAULT '[]',
	PRIMARY KEY (kit_id, tag)
);

-- Components are posts of their own content type (meta: _elementor_data, _elementor_component_uid,
-- _elementor_component_overridable_props, _elementor_component_is_archived, _elementor_component_lock).
INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels)
VALUES ('elementor_component', 'Components', false, false, '', false,
	'{"title":true,"author":true,"thumbnail":true,"custom-fields":true,"revisions":true}', '{"name":"Components","singular_name":"Component"}')
ON CONFLICT (slug) DO NOTHING;

CREATE INDEX IF NOT EXISTS posts_gwe_components ON posts ((meta->>'_elementor_component_uid')) WHERE type = 'elementor_component';
