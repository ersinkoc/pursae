-- Goocommerce Bookings resources (WooCommerce Bookings "bookable_resource"): global resources shared
-- by bookable products. qty is the resource's available quantity per block; availability holds its
-- own availability rules ([{type, from, to, bookable, priority}]), which override the product's.
-- Products link resources (with their own base / block costs) in the _wc_booking_resources meta;
-- goo_bookings.resource_id (0016) records the resource a booking holds.
CREATE TABLE IF NOT EXISTS goo_booking_resources (
    id            bigserial PRIMARY KEY,
    name          text NOT NULL DEFAULT '',
    qty           integer NOT NULL DEFAULT 1,
    availability  jsonb NOT NULL DEFAULT '[]',
    menu_order    integer NOT NULL DEFAULT 0,
    date_created  timestamptz NOT NULL DEFAULT now(),
    date_modified timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS goo_bookings_resource_start ON goo_bookings (resource_id, start_at);
