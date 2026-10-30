-- Goocommerce › Memberships (module "memberships", like WooCommerce Memberships): membership plans with
-- their access rules, and the members (user memberships). Nothing reads them while the module is off.
CREATE TABLE IF NOT EXISTS goo_membership_plans (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT NOT NULL,
    slug          TEXT NOT NULL UNIQUE,
    access_method TEXT NOT NULL DEFAULT 'manual', -- manual, purchase, signup
    product_ids   BIGINT[] NOT NULL DEFAULT '{}',
    access_length TEXT NOT NULL DEFAULT 'unlimited', -- unlimited, specific, fixed
    access_amount INT NOT NULL DEFAULT 0,
    access_period TEXT NOT NULL DEFAULT 'months',
    access_end    TIMESTAMPTZ,
    rules         JSONB NOT NULL DEFAULT '{}', -- content restriction, member discounts, member-only products
    message       TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'publish',
    date_created  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS goo_user_memberships (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT NOT NULL,
    plan_id      BIGINT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'active', -- active, delayed, complimentary, pending-cancel, paused, expired, cancelled
    start_date   TIMESTAMPTZ NOT NULL DEFAULT now(),
    end_date     TIMESTAMPTZ,
    order_id     BIGINT NOT NULL DEFAULT 0,
    product_id   BIGINT NOT NULL DEFAULT 0,
    notes        JSONB NOT NULL DEFAULT '[]',
    date_created TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS goo_user_memberships_user ON goo_user_memberships (user_id);
CREATE INDEX IF NOT EXISTS goo_user_memberships_plan ON goo_user_memberships (plan_id);
