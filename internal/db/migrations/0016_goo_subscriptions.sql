-- Goocommerce › Subscriptions (module "subscriptions", like WooCommerce Subscriptions): a subscription
-- is created from its parent order and renews through renewal orders (goo_orders with the meta
-- _subscription_renewal). The table exists on every site; nothing reads it while the module is off.
CREATE TABLE IF NOT EXISTS goo_subscriptions (
    id              BIGSERIAL PRIMARY KEY,
    parent_order_id BIGINT NOT NULL DEFAULT 0,
    customer_id     BIGINT NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'pending',
    billing_period  TEXT NOT NULL DEFAULT 'month',
    billing_interval INT NOT NULL DEFAULT 1,
    items           JSONB NOT NULL DEFAULT '[]',
    currency        TEXT NOT NULL DEFAULT '',
    recurring_total NUMERIC(19,4) NOT NULL DEFAULT 0,
    recurring_tax   NUMERIC(19,4) NOT NULL DEFAULT 0,
    start_date      TIMESTAMPTZ NOT NULL DEFAULT now(),
    trial_end       TIMESTAMPTZ,
    next_payment    TIMESTAMPTZ,
    end_date        TIMESTAMPTZ,
    last_order_id   BIGINT NOT NULL DEFAULT 0,
    billing         JSONB NOT NULL DEFAULT '{}',
    shipping        JSONB NOT NULL DEFAULT '{}',
    payment_method  TEXT NOT NULL DEFAULT '',
    payment_method_title TEXT NOT NULL DEFAULT '',
    meta            JSONB NOT NULL DEFAULT '{}',
    date_created    TIMESTAMPTZ NOT NULL DEFAULT now(),
    date_modified   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS goo_subscriptions_customer ON goo_subscriptions (customer_id);
CREATE INDEX IF NOT EXISTS goo_subscriptions_parent ON goo_subscriptions (parent_order_id);
CREATE INDEX IF NOT EXISTS goo_subscriptions_due ON goo_subscriptions (status, next_payment);
