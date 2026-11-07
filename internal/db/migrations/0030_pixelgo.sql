-- Pursae Pixel (native plugin): the delivery log of server-side events (Meta Conversions API, TikTok
-- Events API, Pinterest Conversions API, GA4 Measurement Protocol). Secrets are never stored here.
CREATE TABLE IF NOT EXISTS pixelgo_log (
	id         bigserial PRIMARY KEY,
	platform   text NOT NULL,
	event      text NOT NULL,
	event_id   text NOT NULL DEFAULT '',
	source     text NOT NULL DEFAULT 'browser', -- browser (forwarded by pixelgo.js), order (payment complete), test
	status     text NOT NULL,                   -- ok, error, skipped
	http_code  integer NOT NULL DEFAULT 0,
	message    text NOT NULL DEFAULT '',
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS pixelgo_log_created ON pixelgo_log (created_at DESC);
CREATE INDEX IF NOT EXISTS pixelgo_log_platform ON pixelgo_log (platform, created_at DESC);
