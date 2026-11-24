-- The attachment transaction records its local files here before deleting the attachment.
-- The paths stay reserved until cleanup finishes, including when a worker is interrupted.
CREATE TABLE IF NOT EXISTS media_file_deletes (
    path text PRIMARY KEY,
    attachment_id bigint NOT NULL
);

CREATE INDEX IF NOT EXISTS media_file_deletes_attachment ON media_file_deletes (attachment_id, path);
