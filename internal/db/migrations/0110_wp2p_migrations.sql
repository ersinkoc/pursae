-- WP to Pursae (Tools › Import › From WordPress): imports of a whole WordPress site from the package the
-- "WP to Pursae" WordPress plugin builds. One row per import: a package uploaded in the admin, given to
-- `pursae import wordpress`, or pushed by the plugin with a one-time migration token. The row keeps the
-- state, the progress of the background run (so a retry resumes) and the final report.
CREATE TABLE IF NOT EXISTS wp2p_migrations (
	id          text PRIMARY KEY,                    -- 32 hex digits
	token_hash  text NOT NULL DEFAULT '',            -- SHA-256 (hex) of the push token, '' for uploads
	package_key bytea,                               -- the package key until the package is opened
	created_by  bigint NOT NULL DEFAULT 0,           -- the administrator who started it (owns the import)
	source      text NOT NULL DEFAULT '',            -- the WordPress site's home URL (from the manifest)
	state       text NOT NULL DEFAULT 'waiting',     -- waiting, receiving, queued, running, done, failed, revoked
	size        bigint NOT NULL DEFAULT 0,           -- size of the package the plugin announced
	received    bigint NOT NULL DEFAULT 0,           -- bytes stored so far (push mode)
	sha256      text NOT NULL DEFAULT '',            -- checksum the plugin announced
	progress    jsonb NOT NULL DEFAULT '{}',
	report      jsonb NOT NULL DEFAULT '{}',
	error       text NOT NULL DEFAULT '',
	created_at  timestamptz NOT NULL DEFAULT now(),
	updated_at  timestamptz NOT NULL DEFAULT now(),
	expires_at  timestamptz                          -- when an unused token stops working
);
CREATE UNIQUE INDEX IF NOT EXISTS wp2p_migrations_token ON wp2p_migrations (token_hash) WHERE token_hash <> '';

-- Ids of the WordPress site mapped to the ones the import created here, per source site and kind (user,
-- post, term, order, coupon, tax_rate): importing the same site again finds what is already here.
CREATE TABLE IF NOT EXISTS wp2p_map (
	source text NOT NULL,
	kind   text NOT NULL,
	old_id bigint NOT NULL,
	new_id bigint NOT NULL,
	PRIMARY KEY (source, kind, old_id)
);
