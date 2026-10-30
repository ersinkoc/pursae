-- Goocommerce Product Add-Ons module: global add-on groups. A group applies to all products or to
-- the products of the listed product_cat terms; fields is the add-on list (the product_addons format:
-- name, type, display, options with label / price / price_type, restrictions, …). Product-level
-- add-ons live in the product's meta (_product_addons).
CREATE TABLE IF NOT EXISTS goo_addon_groups (
    id             bigserial PRIMARY KEY,
    name           text NOT NULL DEFAULT '',
    priority       int NOT NULL DEFAULT 10,
    all_products   boolean NOT NULL DEFAULT true,
    categories     bigint[] NOT NULL DEFAULT '{}',
    fields         jsonb NOT NULL DEFAULT '[]',
    date_created   timestamptz NOT NULL DEFAULT now(),
    date_modified  timestamptz NOT NULL DEFAULT now()
);
