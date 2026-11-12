-- Saved payment methods (WC_Payment_Token): a provider reference only, never card data.
CREATE TABLE IF NOT EXISTS goo_payment_tokens (
	id           bigserial PRIMARY KEY,
	user_id      bigint NOT NULL,
	gateway_id   text NOT NULL,
	token        text NOT NULL,
	customer_ref text NOT NULL DEFAULT '',
	mode         text NOT NULL DEFAULT 'live',
	type         text NOT NULL DEFAULT 'CC',
	brand        text NOT NULL DEFAULT '',
	last4        text NOT NULL DEFAULT '',
	exp_month    integer NOT NULL DEFAULT 0,
	exp_year     integer NOT NULL DEFAULT 0,
	is_default   boolean NOT NULL DEFAULT false,
	created_at   timestamptz NOT NULL DEFAULT now(),
	UNIQUE (gateway_id, token)
);
CREATE INDEX IF NOT EXISTS goo_payment_tokens_user ON goo_payment_tokens (user_id, gateway_id);
