-- Goocommerce › Dynamic Pricing: pricing rule sets (bulk, special offers, cart totals, role pricing).
CREATE TABLE IF NOT EXISTS goo_pricing_rules (
	id          BIGSERIAL PRIMARY KEY,
	name        TEXT NOT NULL DEFAULT '',
	type        TEXT NOT NULL DEFAULT 'product_bulk',
	active      BOOLEAN NOT NULL DEFAULT true,
	priority    INTEGER NOT NULL DEFAULT 10,
	date_from   TIMESTAMPTZ,
	date_to     TIMESTAMPTZ,
	conditions  JSONB NOT NULL DEFAULT '{}',
	rules       JSONB NOT NULL DEFAULT '{}',
	created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS goo_pricing_rules_active ON goo_pricing_rules (active, priority);
