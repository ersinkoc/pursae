-- GoCDN: one row per file of the uploads directory that was copied to object storage (an
-- attachment's main file, each generated size and the original of a "-scaled" image). path is
-- relative to the uploads directory, as media.file_path; object_key is the key in the bucket.
-- local_removed is set once the local copy was deleted ("Remove files from the server"), so
-- /wp-content/uploads/<path> answers from the bucket. Rows of a deleted attachment go with it.
CREATE TABLE IF NOT EXISTS gocdn_items (
	id            bigserial PRIMARY KEY,
	media_id      bigint REFERENCES media(id) ON DELETE CASCADE,
	path          text NOT NULL UNIQUE,
	provider      text NOT NULL,
	region        text NOT NULL DEFAULT '',
	bucket        text NOT NULL,
	object_key    text NOT NULL,
	size          bigint NOT NULL DEFAULT 0,
	mime_type     text NOT NULL DEFAULT '',
	local_removed boolean NOT NULL DEFAULT false,
	created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS gocdn_items_media ON gocdn_items (media_id);
