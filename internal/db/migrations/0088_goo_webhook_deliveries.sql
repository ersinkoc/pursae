-- Webhook delivery log (Settings > Advanced > Webhooks): the last deliveries of each webhook.
CREATE TABLE IF NOT EXISTS goo_webhook_deliveries (
	id          bigserial PRIMARY KEY,
	webhook_id  bigint NOT NULL,
	delivery_id text NOT NULL DEFAULT '',
	topic       text NOT NULL DEFAULT '',
	url         text NOT NULL DEFAULT '',
	status_code integer NOT NULL DEFAULT 0,
	duration_ms integer NOT NULL DEFAULT 0,
	response    text NOT NULL DEFAULT '',
	error       text NOT NULL DEFAULT '',
	success     boolean NOT NULL DEFAULT false,
	created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS goo_webhook_deliveries_hook ON goo_webhook_deliveries (webhook_id, id DESC);
