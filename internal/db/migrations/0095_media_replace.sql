-- Media Library: replacing a file in place and focal points.
--
-- media_moved_files: uploads paths an attachment no longer has after its file was replaced (a new
-- file type, other sizes): requests for them are redirected to the file that took their place, and
-- the front end rewrites them in page markup. The rows go with the attachment.
CREATE TABLE IF NOT EXISTS media_moved_files (
	path       text PRIMARY KEY,                                        -- relative to the uploads dir
	media_id   bigint NOT NULL REFERENCES media(id) ON DELETE CASCADE,
	target     text NOT NULL,                                           -- the path it redirects to
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS media_moved_files_media ON media_moved_files (media_id);

-- The attachments whose files carry a version (replaced, re-cropped) or a focal point: the server
-- keeps their files in memory to version URLs and position images; this index finds them quickly.
CREATE INDEX IF NOT EXISTS media_file_state ON media (id) WHERE meta ? 'file_version' OR meta ? 'focal_point';
