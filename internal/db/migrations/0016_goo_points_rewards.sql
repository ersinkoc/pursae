-- Goocommerce › Points and Rewards (module "points_rewards", like WooCommerce Points and Rewards): the
-- points ledger. A customer's balance is the sum of their entries (cached in the user meta
-- goo_points_balance). Nothing reads it while the module is off.
CREATE TABLE IF NOT EXISTS goo_points_log (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL,
    points     INT NOT NULL,
    type       TEXT NOT NULL, -- order-placed, order-cancelled, order-redeem, admin-adjustment, account-signup, product-review, expire
    order_id   BIGINT NOT NULL DEFAULT 0,
    note       TEXT NOT NULL DEFAULT '',
    used       INT NOT NULL DEFAULT 0, -- points of a positive entry consumed (expiry bookkeeping)
    date       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS goo_points_log_user ON goo_points_log (user_id, date);
CREATE INDEX IF NOT EXISTS goo_points_log_order ON goo_points_log (order_id);
