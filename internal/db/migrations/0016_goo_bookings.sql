-- Goocommerce Bookings module (WooCommerce Bookings): one row per booking. A booking is held
-- "in-cart" while it waits in a cart, then follows its order: unpaid / pending-confirmation →
-- confirmed → paid → complete, or cancelled. persons is {person type id: count}.
CREATE TABLE IF NOT EXISTS goo_bookings (
    id             bigserial PRIMARY KEY,
    product_id     bigint NOT NULL,
    order_id       bigint NOT NULL DEFAULT 0,
    order_item_id  bigint NOT NULL DEFAULT 0,
    customer_id    bigint NOT NULL DEFAULT 0,
    status         text NOT NULL DEFAULT 'unpaid',
    start_at       timestamptz NOT NULL,
    end_at         timestamptz NOT NULL,
    all_day        boolean NOT NULL DEFAULT false,
    persons        jsonb NOT NULL DEFAULT '{}',
    cost           numeric(19,4) NOT NULL DEFAULT 0,
    resource_id    bigint NOT NULL DEFAULT 0,
    reminder_sent  boolean NOT NULL DEFAULT false,
    date_created   timestamptz NOT NULL DEFAULT now(),
    date_modified  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS goo_bookings_product_start ON goo_bookings (product_id, start_at);
CREATE INDEX IF NOT EXISTS goo_bookings_order ON goo_bookings (order_id);
CREATE INDEX IF NOT EXISTS goo_bookings_customer ON goo_bookings (customer_id);
CREATE INDEX IF NOT EXISTS goo_bookings_status ON goo_bookings (status);
