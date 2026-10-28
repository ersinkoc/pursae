-- Core schema v1. Fixed fields are typed columns, flexible and plugin data lives in JSONB.
-- Status, type and capability names follow WordPress so the REST API and the block editor stay compatible.

CREATE FUNCTION set_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	NEW.updated_at = now();
	RETURN NEW;
END $$;

-- Users and permissions -------------------------------------------------------

CREATE TABLE users (
	id            bigserial PRIMARY KEY,
	login         text NOT NULL UNIQUE,
	email         text NOT NULL UNIQUE,
	password_hash text NOT NULL,
	nicename      text NOT NULL UNIQUE,          -- author archive slug
	display_name  text NOT NULL,
	url           text NOT NULL DEFAULT '',
	status        smallint NOT NULL DEFAULT 0,
	locale        text NOT NULL DEFAULT '',
	profile       jsonb NOT NULL DEFAULT '{}',   -- first_name, last_name, nickname, description, ...
	meta          jsonb NOT NULL DEFAULT '{}',   -- persisted_preferences and plugin data
	registered_at timestamptz NOT NULL DEFAULT now(),
	last_login_at timestamptz,
	created_at    timestamptz NOT NULL DEFAULT now(),
	updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE roles (
	id           bigserial PRIMARY KEY,
	slug         text NOT NULL UNIQUE,
	name         text NOT NULL,
	capabilities jsonb NOT NULL DEFAULT '{}',   -- {"edit_posts": true, ...}
	is_system    boolean NOT NULL DEFAULT false,
	created_at   timestamptz NOT NULL DEFAULT now(),
	updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_roles (
	user_id    bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	role_id    bigint NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
	is_primary boolean NOT NULL DEFAULT false,
	PRIMARY KEY (user_id, role_id)
);
CREATE UNIQUE INDEX user_roles_one_primary ON user_roles(user_id) WHERE is_primary;

-- Per-user exceptions on top of the role union; deny always wins.
CREATE TABLE user_capabilities (
	user_id    bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	capability text NOT NULL,
	effect     text NOT NULL CHECK (effect IN ('allow', 'deny')),
	PRIMARY KEY (user_id, capability)
);

CREATE TABLE sessions (
	id           bigserial PRIMARY KEY,
	user_id      bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	token_hash   bytea NOT NULL UNIQUE,
	ip           text NOT NULL DEFAULT '',
	user_agent   text NOT NULL DEFAULT '',
	expires_at   timestamptz NOT NULL,
	last_seen_at timestamptz NOT NULL DEFAULT now(),
	created_at   timestamptz NOT NULL DEFAULT now(),
	updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE app_passwords (
	id            bigserial PRIMARY KEY,
	user_id       bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	uuid          uuid NOT NULL UNIQUE,
	name          text NOT NULL,
	password_hash text NOT NULL,
	last_used_at  timestamptz,
	last_ip       text NOT NULL DEFAULT '',
	created_at    timestamptz NOT NULL DEFAULT now(),
	updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE login_attempts (
	id           bigserial PRIMARY KEY,
	ip           text NOT NULL,
	login        text NOT NULL,
	attempted_at timestamptz NOT NULL DEFAULT now(),
	success      boolean NOT NULL,
	created_at   timestamptz NOT NULL DEFAULT now(),
	updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_attempts_ip_time ON login_attempts(ip, attempted_at);

-- Content ---------------------------------------------------------------------

CREATE TABLE content_types (
	id           bigserial PRIMARY KEY,
	slug         text NOT NULL UNIQUE,              -- post, page, attachment, revision, wp_block
	name         text NOT NULL,
	labels       jsonb NOT NULL DEFAULT '{}',
	supports     jsonb NOT NULL DEFAULT '{}',
	hierarchical boolean NOT NULL DEFAULT false,
	public       boolean NOT NULL DEFAULT true,
	rest_base    text NOT NULL DEFAULT '',
	is_builtin   boolean NOT NULL DEFAULT false,
	created_at   timestamptz NOT NULL DEFAULT now(),
	updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE media (
	id          bigserial PRIMARY KEY,
	uploader_id bigint REFERENCES users(id) ON DELETE SET NULL,
	post_id     bigint,                              -- attached-to post (FK added below)
	slug        text NOT NULL DEFAULT '',
	file_path   text NOT NULL,                       -- relative to the uploads dir
	mime_type   text NOT NULL,
	file_size   bigint NOT NULL DEFAULT 0,
	width       integer,
	height      integer,
	alt_text    text NOT NULL DEFAULT '',
	title       text NOT NULL DEFAULT '',
	caption     text NOT NULL DEFAULT '',
	description text NOT NULL DEFAULT '',
	meta        jsonb NOT NULL DEFAULT '{}',         -- EXIF and image_meta
	deleted_at  timestamptz,
	created_at  timestamptz NOT NULL DEFAULT now(),
	updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE media_variants (
	id         bigserial PRIMARY KEY,
	media_id   bigint NOT NULL REFERENCES media(id) ON DELETE CASCADE,
	name       text NOT NULL,                         -- thumbnail, medium, medium_large, large
	file_path  text NOT NULL,
	width      integer NOT NULL,
	height     integer NOT NULL,
	mime_type  text NOT NULL,
	file_size  bigint NOT NULL DEFAULT 0,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (media_id, name)
);

CREATE TABLE posts (
	id                bigserial PRIMARY KEY,
	type              text NOT NULL REFERENCES content_types(slug) ON UPDATE CASCADE,
	author_id         bigint REFERENCES users(id) ON DELETE SET NULL,
	parent_id         bigint REFERENCES posts(id) ON DELETE SET NULL,
	slug              text NOT NULL DEFAULT '',
	title             text NOT NULL DEFAULT '',
	excerpt           text NOT NULL DEFAULT '',
	content           text NOT NULL DEFAULT '',      -- block HTML, the source of truth
	blocks            jsonb,                         -- parsed block tree derived from content
	status            text NOT NULL DEFAULT 'draft'
	                  CHECK (status IN ('publish','future','draft','pending','private','trash','auto-draft')),
	trashed_status    text,                          -- status to restore from the trash
	password          text NOT NULL DEFAULT '',      -- post password; plain like WordPress, the editor shows it
	published_at      timestamptz,                   -- NULL = floating date (unsaved draft)
	modified_at       timestamptz NOT NULL DEFAULT now(),
	featured_media_id bigint REFERENCES media(id) ON DELETE SET NULL,
	menu_order        integer NOT NULL DEFAULT 0,
	comment_status    text NOT NULL DEFAULT 'open' CHECK (comment_status IN ('open','closed')),
	ping_status       text NOT NULL DEFAULT 'open' CHECK (ping_status IN ('open','closed')),
	comment_count     integer NOT NULL DEFAULT 0,
	sticky            boolean NOT NULL DEFAULT false,
	format            text NOT NULL DEFAULT 'standard',
	meta              jsonb NOT NULL DEFAULT '{}',
	search            tsvector GENERATED ALWAYS AS (
	                    setweight(to_tsvector('simple', title), 'A') ||
	                    setweight(to_tsvector('simple', excerpt), 'B') ||
	                    setweight(to_tsvector('simple', regexp_replace(content, '<[^>]*>', ' ', 'g')), 'C')
	                  ) STORED,
	deleted_at        timestamptz,
	created_at        timestamptz NOT NULL DEFAULT now(),
	updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX posts_type_parent_slug ON posts(type, coalesce(parent_id, 0), slug)
	WHERE slug <> '' AND status NOT IN ('auto-draft', 'trash');
CREATE INDEX posts_list ON posts(type, status, published_at DESC);
CREATE INDEX posts_author ON posts(author_id);
CREATE INDEX posts_parent ON posts(parent_id);
CREATE INDEX posts_search ON posts USING gin(search);
CREATE INDEX posts_meta ON posts USING gin(meta);

ALTER TABLE media ADD CONSTRAINT media_post_fk FOREIGN KEY (post_id) REFERENCES posts(id) ON DELETE SET NULL;

CREATE TABLE post_revisions (
	id          bigserial PRIMARY KEY,
	post_id     bigint NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
	author_id   bigint REFERENCES users(id) ON DELETE SET NULL,
	title       text NOT NULL DEFAULT '',
	excerpt     text NOT NULL DEFAULT '',
	content     text NOT NULL DEFAULT '',
	is_autosave boolean NOT NULL DEFAULT false,
	created_at  timestamptz NOT NULL DEFAULT now(),
	updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX post_revisions_post ON post_revisions(post_id, created_at DESC);

-- Taxonomies ------------------------------------------------------------------

CREATE TABLE taxonomies (
	id           bigserial PRIMARY KEY,
	slug         text NOT NULL UNIQUE,               -- category, post_tag, post_format
	name         text NOT NULL,
	labels       jsonb NOT NULL DEFAULT '{}',
	hierarchical boolean NOT NULL DEFAULT false,
	object_types jsonb NOT NULL DEFAULT '[]',
	rest_base    text NOT NULL DEFAULT '',
	public       boolean NOT NULL DEFAULT true,
	is_builtin   boolean NOT NULL DEFAULT false,
	created_at   timestamptz NOT NULL DEFAULT now(),
	updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE terms (
	id          bigserial PRIMARY KEY,
	taxonomy    text NOT NULL REFERENCES taxonomies(slug) ON UPDATE CASCADE ON DELETE CASCADE,
	parent_id   bigint REFERENCES terms(id) ON DELETE SET NULL,
	slug        text NOT NULL,
	name        text NOT NULL,
	description text NOT NULL DEFAULT '',
	meta        jsonb NOT NULL DEFAULT '{}',
	post_count  integer NOT NULL DEFAULT 0,          -- published posts only, kept by store code
	created_at  timestamptz NOT NULL DEFAULT now(),
	updated_at  timestamptz NOT NULL DEFAULT now(),
	UNIQUE (taxonomy, slug)
);
CREATE INDEX terms_parent ON terms(parent_id);

CREATE TABLE post_terms (
	post_id    bigint NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
	term_id    bigint NOT NULL REFERENCES terms(id) ON DELETE CASCADE,
	sort_order integer NOT NULL DEFAULT 0,
	PRIMARY KEY (post_id, term_id)
);
CREATE INDEX post_terms_term ON post_terms(term_id);

-- Comments --------------------------------------------------------------------

CREATE TABLE comments (
	id           bigserial PRIMARY KEY,
	post_id      bigint NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
	parent_id    bigint REFERENCES comments(id) ON DELETE SET NULL,
	user_id      bigint REFERENCES users(id) ON DELETE SET NULL,
	author_name  text NOT NULL DEFAULT '',
	author_email text NOT NULL DEFAULT '',
	author_url   text NOT NULL DEFAULT '',
	author_ip    text NOT NULL DEFAULT '',
	user_agent   text NOT NULL DEFAULT '',
	content      text NOT NULL,
	type         text NOT NULL DEFAULT 'comment',    -- comment, pingback, trackback
	status       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','spam','trash')),
	meta         jsonb NOT NULL DEFAULT '{}',
	deleted_at   timestamptz,
	created_at   timestamptz NOT NULL DEFAULT now(),
	updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX comments_post ON comments(post_id, status, created_at);
CREATE INDEX comments_status ON comments(status, created_at DESC);

-- Menus and widgets -----------------------------------------------------------

CREATE TABLE menus (
	id         bigserial PRIMARY KEY,
	slug       text NOT NULL UNIQUE,
	name       text NOT NULL,
	location   text,                                 -- theme location, e.g. primary, social
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE menu_items (
	id          bigserial PRIMARY KEY,
	menu_id     bigint NOT NULL REFERENCES menus(id) ON DELETE CASCADE,
	parent_id   bigint REFERENCES menu_items(id) ON DELETE CASCADE,
	sort_order  integer NOT NULL DEFAULT 0,
	kind        text NOT NULL CHECK (kind IN ('post_type','taxonomy','custom')),
	object      text NOT NULL DEFAULT '',            -- post, page, category, ...
	target_id   bigint,
	url         text NOT NULL DEFAULT '',
	label       text NOT NULL DEFAULT '',
	target      text NOT NULL DEFAULT '',
	css_classes text NOT NULL DEFAULT '',
	created_at  timestamptz NOT NULL DEFAULT now(),
	updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE widget_areas (
	id         bigserial PRIMARY KEY,
	slug       text NOT NULL,
	name       text NOT NULL,
	theme      text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (theme, slug)
);

CREATE TABLE widgets (
	id         bigserial PRIMARY KEY,
	area_id    bigint REFERENCES widget_areas(id) ON DELETE SET NULL,  -- NULL = inactive widget
	type       text NOT NULL,
	sort_order integer NOT NULL DEFAULT 0,
	settings   jsonb NOT NULL DEFAULT '{}',
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);

-- Settings and system ---------------------------------------------------------

CREATE TABLE settings (
	key        text PRIMARY KEY,
	value      jsonb NOT NULL,
	grp        text NOT NULL DEFAULT 'general',
	autoload   boolean NOT NULL DEFAULT true,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE redirects (
	id         bigserial PRIMARY KEY,
	source     text NOT NULL UNIQUE,
	target     text NOT NULL,
	code       smallint NOT NULL DEFAULT 301,
	hits       bigint NOT NULL DEFAULT 0,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE jobs (
	id          bigserial PRIMARY KEY,
	name        text NOT NULL,
	payload     jsonb NOT NULL DEFAULT '{}',
	run_at      timestamptz NOT NULL,
	recurrence  text NOT NULL DEFAULT '',            -- '', hourly, twicedaily, daily, weekly or a cron expression
	status      text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','done','failed')),
	attempts    integer NOT NULL DEFAULT 0,
	locked_by   text,
	locked_at   timestamptz,
	last_error  text NOT NULL DEFAULT '',
	created_at  timestamptz NOT NULL DEFAULT now(),
	updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX jobs_due ON jobs(status, run_at);

CREATE TABLE transients (
	key        text PRIMARY KEY,
	value      jsonb NOT NULL,
	expires_at timestamptz,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE plugins (
	id           bigserial PRIMARY KEY,
	slug         text NOT NULL UNIQUE,
	version      text NOT NULL,
	status       text NOT NULL DEFAULT 'inactive' CHECK (status IN ('active','inactive')),
	permissions  jsonb NOT NULL DEFAULT '{}',
	signature    text NOT NULL DEFAULT '',
	installed_at timestamptz NOT NULL DEFAULT now(),
	created_at   timestamptz NOT NULL DEFAULT now(),
	updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE plugin_data (
	plugin_id  bigint NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
	key        text NOT NULL,
	value      jsonb NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (plugin_id, key)
);

CREATE TABLE themes (
	id         bigserial PRIMARY KEY,
	slug       text NOT NULL UNIQUE,
	version    text NOT NULL,
	status     text NOT NULL DEFAULT 'inactive' CHECK (status IN ('active','inactive')),
	parent     text,
	settings   jsonb NOT NULL DEFAULT '{}',          -- theme_mods
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_log (
	id          bigserial PRIMARY KEY,
	user_id     bigint REFERENCES users(id) ON DELETE SET NULL,
	action      text NOT NULL,
	target_type text NOT NULL DEFAULT '',
	target_id   bigint,
	details     jsonb NOT NULL DEFAULT '{}',
	ip          text NOT NULL DEFAULT '',
	created_at  timestamptz NOT NULL DEFAULT now(),
	updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_time ON audit_log(created_at DESC);

DO $$
DECLARE t text;
BEGIN
	FOR t IN SELECT table_name FROM information_schema.columns
	         WHERE table_schema = current_schema() AND column_name = 'updated_at'
	LOOP
		EXECUTE format('CREATE TRIGGER %I_updated_at BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION set_updated_at()', t, t);
	END LOOP;
END $$;
