-- Goocommerce › Subscriptions, continued: subscription switching and synchronised renewals.
-- Switch orders are goo_orders with the meta _subscription_switch (the subscription id); renewal orders
-- keep the meta _subscription_renewal. Both are looked up when listing a subscription's related orders.
CREATE INDEX IF NOT EXISTS goo_orders_subscription_renewal ON goo_orders (((meta->>'_subscription_renewal')))
    WHERE meta ? '_subscription_renewal';
CREATE INDEX IF NOT EXISTS goo_orders_subscription_switch ON goo_orders (((meta->>'_subscription_switch')))
    WHERE meta ? '_subscription_switch';
-- A subscription's renewal day when it is synchronised (week: 1-7, month: 1-28, year: MMDD; 0 = not synced).
ALTER TABLE goo_subscriptions ADD COLUMN IF NOT EXISTS sync_day INT NOT NULL DEFAULT 0;
