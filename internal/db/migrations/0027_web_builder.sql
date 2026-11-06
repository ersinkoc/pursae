-- Pursae Web Builder: the page doc (source of truth), its sanitized snapshot, per-user drafts,
-- doc revisions next to the core post revisions, and saved components (the library).
-- The contract (docs/developer/web-builder-contract.md §6.1) also lists web_builder_ai_usage;
-- AI is out of scope for now, so that table comes with the AI package in its own migration.
CREATE TABLE IF NOT EXISTS web_builder_docs (
	post_id             bigint PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
	doc                 jsonb   NOT NULL,
	page                jsonb   NOT NULL DEFAULT '{}',
	html                text    NOT NULL DEFAULT '',   -- sanitized snapshot (RenderResult.html)
	css                 text    NOT NULL DEFAULT '',   -- sanitized utility CSS + section CSS
	fonts               jsonb   NOT NULL DEFAULT '[]', -- FontNeed[]
	features            text[]  NOT NULL DEFAULT '{}',
	renderer            integer NOT NULL DEFAULT 0,
	rev                 integer NOT NULL DEFAULT 0,
	content_hash        text    NOT NULL DEFAULT '',   -- sha256 hex of the posts.content we wrote
	detached            boolean NOT NULL DEFAULT false,
	snapshot_user       bigint REFERENCES users(id) ON DELETE SET NULL,
	snapshot_unfiltered boolean NOT NULL DEFAULT false,
	created_at          timestamptz NOT NULL DEFAULT now(),
	updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS web_builder_drafts (
	post_id    bigint  NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
	user_id    bigint  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	doc        jsonb   NOT NULL,
	page       jsonb   NOT NULL DEFAULT '{}',
	base_rev   integer NOT NULL DEFAULT 0,
	updated_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (post_id, user_id)
);
-- Deleting a user cascades through user_id; this index keeps that delete from scanning the table.
CREATE INDEX IF NOT EXISTS web_builder_drafts_user ON web_builder_drafts (user_id);

CREATE TABLE IF NOT EXISTS web_builder_revisions (
	revision_id bigint  PRIMARY KEY REFERENCES post_revisions(id) ON DELETE CASCADE,
	post_id     bigint  NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
	rev         integer NOT NULL,
	doc         jsonb   NOT NULL,
	page        jsonb   NOT NULL DEFAULT '{}',
	renderer    integer NOT NULL DEFAULT 0,
	created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS web_builder_revisions_post ON web_builder_revisions (post_id, rev DESC);

CREATE TABLE IF NOT EXISTS web_builder_library (
	id         bigserial PRIMARY KEY,
	name       text   NOT NULL,
	data       jsonb  NOT NULL,                           -- Section
	author_id  bigint REFERENCES users(id) ON DELETE SET NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);
