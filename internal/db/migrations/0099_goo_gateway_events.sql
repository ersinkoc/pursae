-- Payment provider notifications already handled (Stripe event ids, iyzico tokens): a webhook that
-- is delivered twice is processed once.
CREATE TABLE IF NOT EXISTS goo_gateway_events (
	gateway     text NOT NULL,
	event_id    text NOT NULL,
	event_type  text NOT NULL DEFAULT '',
	received_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (gateway, event_id)
);
CREATE INDEX IF NOT EXISTS goo_gateway_events_received ON goo_gateway_events (received_at);
