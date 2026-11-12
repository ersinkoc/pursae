-- Pursae CDN: uploads whose copy to the bucket failed after the immediate retries. The daily
-- housekeeping pass and the Media Offload screen's "Retry failed" button try them again; a row
-- goes when the offload succeeds or the attachment is deleted. See internal/server/gocdn_housekeeping.go.
CREATE TABLE IF NOT EXISTS gocdn_offload_failures (
	media_id   bigint PRIMARY KEY REFERENCES media(id) ON DELETE CASCADE,
	attempts   integer NOT NULL DEFAULT 1,
	last_error text NOT NULL DEFAULT '',
	updated_at timestamptz NOT NULL DEFAULT now()
);
